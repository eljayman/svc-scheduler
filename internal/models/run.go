package models

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type RunStatus string

const (
	RunStatusPending RunStatus = "pending"
	RunStatusRunning RunStatus = "running"
	RunStatusSuccess RunStatus = "success"
	RunStatusFailed  RunStatus = "failed"
	RunStatusMissed  RunStatus = "missed"
)

type Run struct {
	ID              string
	JobID           string
	JobName         string
	Status          RunStatus
	ScheduledFor    time.Time
	Attempt         int
	MaxAttempts     int
	Priority        int
	BackoffStrategy string
	BackoffSeconds  int
	RetryAfter      *time.Time
	WebhookURL      string
	WebhookTimeout  int
	WebhookSecret   string
	Config          json.RawMessage
	StartedAt       *time.Time
	FinishedAt      *time.Time
	DurationMs      *int
	HTTPStatus      *int
	ResponseBody    *string
	ErrorDetail     *string
	CreatedAt       time.Time
}

// CreateRun inserts a new pending run row.
func CreateRun(ctx context.Context, pool *pgxpool.Pool, j Job, scheduledFor time.Time) error {
	_, err := pool.Exec(ctx, `
        INSERT INTO runs (
            job_id, job_name, status, scheduled_for, attempt,
            max_attempts, priority, backoff_strategy, backoff_seconds,
            webhook_url, webhook_timeout, webhook_secret, config
        ) VALUES (
            $1, $2, 'pending', $3, 1,
            $4, $5, $6, $7,
            $8, $9, $10, $11
        )
    `,
		j.ID, j.Name, scheduledFor, j.MaxAttempts, j.Priority,
		j.BackoffStrategy, j.BackoffSeconds,
		j.WebhookURL, j.WebhookTimeout, j.WebhookSecret, j.Config,
	)
	return err
}

// CreateMissedRun inserts a run row already marked as missed.
func CreateMissedRun(ctx context.Context, pool *pgxpool.Pool, j Job, scheduledFor time.Time) error {
	_, err := pool.Exec(ctx, `
        INSERT INTO runs (
            job_id, job_name, status, scheduled_for, attempt,
            max_attempts, priority, backoff_strategy, backoff_seconds,
            webhook_url, webhook_timeout, webhook_secret, config
        ) VALUES (
            $1, $2, 'missed', $3, 1,
            $4, $5, $6, $7,
            $8, $9, $10, $11
        )
    `,
		j.ID, j.Name, scheduledFor, j.MaxAttempts, j.Priority,
		j.BackoffStrategy, j.BackoffSeconds,
		j.WebhookURL, j.WebhookTimeout, j.WebhookSecret, j.Config,
	)
	return err
}

// ClaimNextRun atomically claims the highest priority pending run
// that is ready to execute. Returns nil, nil if nothing is available.
// Uses SELECT FOR UPDATE SKIP LOCKED so concurrent runners never
// claim the same row.
func ClaimNextRun(ctx context.Context, pool *pgxpool.Pool) (*Run, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx, `
        SELECT id, job_id, job_name, status, scheduled_for, attempt,
               max_attempts, priority, backoff_strategy, backoff_seconds,
               retry_after, webhook_url, webhook_timeout, webhook_secret,
               config, created_at
        FROM runs
        WHERE status = 'pending'
          AND (retry_after IS NULL OR retry_after <= now())
        ORDER BY priority ASC, scheduled_for ASC
        LIMIT 1
        FOR UPDATE SKIP LOCKED
    `)

	var r Run
	err = row.Scan(
		&r.ID, &r.JobID, &r.JobName, &r.Status, &r.ScheduledFor,
		&r.Attempt, &r.MaxAttempts, &r.Priority,
		&r.BackoffStrategy, &r.BackoffSeconds, &r.RetryAfter,
		&r.WebhookURL, &r.WebhookTimeout, &r.WebhookSecret,
		&r.Config, &r.CreatedAt,
	)
	if err != nil {
		return nil, nil // no rows available
	}

	_, err = tx.Exec(ctx, `
        UPDATE runs SET status = 'running', started_at = now()
        WHERE id = $1
    `, r.ID)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	r.Status = RunStatusRunning
	return &r, nil
}

// MarkSuccess records a successful run completion.
func MarkSuccess(ctx context.Context, pool *pgxpool.Pool, runID string, httpStatus int, body string, durationMs int) error {
	truncated := body
	if len(truncated) > 4096 {
		truncated = truncated[:4096]
	}
	_, err := pool.Exec(ctx, `
        UPDATE runs
        SET status = 'success',
            finished_at = now(),
            duration_ms = $1,
            http_status = $2,
            response_body = $3
        WHERE id = $4
    `, durationMs, httpStatus, truncated, runID)
	return err
}

// MarkFailed records a failed attempt. If attempts remain, it requeues
// the run as pending with a retry_after delay. Otherwise marks it failed.
func MarkFailed(ctx context.Context, pool *pgxpool.Pool, r *Run, errDetail string, httpStatus *int) error {
	if r.Attempt < r.MaxAttempts {
		retryAfter := retryDelay(r)
		_, err := pool.Exec(ctx, `
            UPDATE runs
            SET status = 'pending',
                attempt = attempt + 1,
                retry_after = $1,
                error_detail = $2,
                http_status = $3,
                finished_at = now()
            WHERE id = $4
        `, retryAfter, errDetail, httpStatus, r.ID)
		return err
	}

	_, err := pool.Exec(ctx, `
        UPDATE runs
        SET status = 'failed',
            finished_at = now(),
            error_detail = $1,
            http_status = $2
        WHERE id = $3
    `, errDetail, httpStatus, r.ID)
	return err
}

// retryDelay computes the next retry_after timestamp based on the job's
// backoff strategy and current attempt number.
func retryDelay(r *Run) time.Time {
	var delaySeconds int
	switch r.BackoffStrategy {
	case "exponential":
		// 1st retry: backoff_seconds, 2nd: *2, 3rd: *4, etc.
		multiplier := 1
		for i := 1; i < r.Attempt; i++ {
			multiplier *= 2
		}
		delaySeconds = r.BackoffSeconds * multiplier
	default: // fixed
		delaySeconds = r.BackoffSeconds
	}
	return time.Now().Add(time.Duration(delaySeconds) * time.Second)
}

// LastRunForJob returns the most recent run for a given job, or nil.
func LastRunForJob(ctx context.Context, pool *pgxpool.Pool, jobID string) (*Run, error) {
	row := pool.QueryRow(ctx, `
        SELECT id, job_id, job_name, status, scheduled_for, attempt,
               max_attempts, priority, backoff_strategy, backoff_seconds,
               retry_after, webhook_url, webhook_timeout, webhook_secret,
               config, created_at
        FROM runs
        WHERE job_id = $1
        ORDER BY scheduled_for DESC
        LIMIT 1
    `, jobID)

	var r Run
	err := row.Scan(
		&r.ID, &r.JobID, &r.JobName, &r.Status, &r.ScheduledFor,
		&r.Attempt, &r.MaxAttempts, &r.Priority,
		&r.BackoffStrategy, &r.BackoffSeconds, &r.RetryAfter,
		&r.WebhookURL, &r.WebhookTimeout, &r.WebhookSecret,
		&r.Config, &r.CreatedAt,
	)
	if err != nil {
		return nil, nil
	}
	return &r, nil
}
