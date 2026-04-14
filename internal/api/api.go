package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eljayman/svc-scheduler/internal/models"
)

// Router returns the admin API router.
// Protected by a static admin token — not full JWT auth since this
// is an internal service not exposed to end users.
func Router(pool *pgxpool.Pool, adminToken string, logger *slog.Logger) http.Handler {
	r := chi.NewRouter()
	r.Use(staticTokenAuth(adminToken))

	h := &handler{pool: pool, logger: logger}

	r.Get("/jobs", h.listJobs)
	r.Get("/runs", h.listRuns)
	r.Post("/jobs/{jobName}/trigger", h.triggerJob)

	return r
}

type handler struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func (h *handler) listJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := models.EnabledJobs(r.Context(), h.pool)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load jobs")
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (h *handler) listRuns(w http.ResponseWriter, r *http.Request) {
	// Simplified: returns last 100 runs across all jobs.
	rows, err := h.pool.Query(r.Context(), `
        SELECT id, job_id, job_name, status, scheduled_for, attempt,
               max_attempts, started_at, finished_at, duration_ms,
               http_status, error_detail, created_at
        FROM runs
        ORDER BY created_at DESC
        LIMIT 100
    `)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load runs")
		return
	}
	defer rows.Close()

	type runSummary struct {
		ID           string  `json:"id"`
		JobID        string  `json:"job_id"`
		JobName      string  `json:"job_name"`
		Status       string  `json:"status"`
		ScheduledFor string  `json:"scheduled_for"`
		Attempt      int     `json:"attempt"`
		MaxAttempts  int     `json:"max_attempts"`
		StartedAt    *string `json:"started_at,omitempty"`
		FinishedAt   *string `json:"finished_at,omitempty"`
		DurationMs   *int    `json:"duration_ms,omitempty"`
		HTTPStatus   *int    `json:"http_status,omitempty"`
		ErrorDetail  *string `json:"error_detail,omitempty"`
		CreatedAt    string  `json:"created_at"`
	}

	var results []runSummary
	for rows.Next() {
		var s runSummary
		if err := rows.Scan(
			&s.ID, &s.JobID, &s.JobName, &s.Status, &s.ScheduledFor,
			&s.Attempt, &s.MaxAttempts, &s.StartedAt, &s.FinishedAt,
			&s.DurationMs, &s.HTTPStatus, &s.ErrorDetail, &s.CreatedAt,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		results = append(results, s)
	}
	writeJSON(w, http.StatusOK, results)
}

// triggerJob creates an immediate pending run for the named job,
// bypassing the cron schedule. Useful for manual reruns or testing.
func (h *handler) triggerJob(w http.ResponseWriter, r *http.Request) {
	jobName := chi.URLParam(r, "jobName")

	rows, err := h.pool.Query(r.Context(),
		`SELECT id, name, description, schedule, timezone, enabled,
                webhook_url, webhook_timeout, webhook_secret,
                max_attempts, backoff_strategy, backoff_seconds,
                missed_policy, priority, config,
                created_at, updated_at, last_scheduled_at,
                last_success_at, last_failure_at
         FROM jobs WHERE name = $1`, jobName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()

	if !rows.Next() {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}

	var j models.Job
	if err := rows.Scan(
		&j.ID, &j.Name, &j.Description, &j.Schedule, &j.Timezone,
		&j.Enabled, &j.WebhookURL, &j.WebhookTimeout, &j.WebhookSecret,
		&j.MaxAttempts, &j.BackoffStrategy, &j.BackoffSeconds,
		&j.MissedPolicy, &j.Priority, &j.Config,
		&j.CreatedAt, &j.UpdatedAt, &j.LastScheduledAt,
		&j.LastSuccessAt, &j.LastFailureAt,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "scan error")
		return
	}

	if err := models.CreateRun(r.Context(), h.pool, j, time.Now()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create run")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"status": "queued"})
}

func staticTokenAuth(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token != "" && r.Header.Get("X-Admin-Token") != token {
				writeError(w, http.StatusUnauthorized, "invalid admin token")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

