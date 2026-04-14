package runner

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eljayman/mtg-common/logging"
	"github.com/eljayman/svc-scheduler/internal/models"
)

// WebhookPayload is the JSON body sent to every job webhook.
type WebhookPayload struct {
	RunID        string          `json:"run_id"`
	JobName      string          `json:"job_name"`
	ScheduledFor time.Time       `json:"scheduled_for"`
	Attempt      int             `json:"attempt"`
	Config       json.RawMessage `json:"config"`
}

// Runner maintains a pool of workers that claim and execute pending runs.
type Runner struct {
	pool       *pgxpool.Pool
	poolSize   int
	interval   time.Duration
	logger     *slog.Logger
	httpClient *http.Client
}

func New(pool *pgxpool.Pool, poolSize, intervalSeconds int, logger *slog.Logger) *Runner {
	return &Runner{
		pool:     pool,
		poolSize: poolSize,
		interval: time.Duration(intervalSeconds) * time.Second,
		logger:   logger,
		httpClient: &http.Client{
			// Timeout is overridden per-run using the job's webhook_timeout.
			// This is a fallback ceiling.
			Timeout: 5 * time.Minute,
		},
	}
}

// Run starts the worker pool. Blocks until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) {
	r.logger.Info("runner started",
		slog.Int("pool_size", r.poolSize),
		slog.Duration("poll_interval", r.interval),
	)

	sem := make(chan struct{}, r.poolSize)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("runner stopped")
			return
		case <-ticker.C:
			// Try to fill the worker pool with available runs.
			for {
				select {
				case sem <- struct{}{}:
					run, err := models.ClaimNextRun(ctx, r.pool)
					if err != nil {
						logging.L(ctx, r.logger).Error("runner: claim error",
							slog.Any("error", err))
						<-sem
						goto nextTick
					}
					if run == nil {
						// Nothing left in queue.
						<-sem
						goto nextTick
					}
					go func(run *models.Run) {
						defer func() { <-sem }()
						r.execute(ctx, run)
					}(run)
				default:
					// Pool is full.
					goto nextTick
				}
			}
		nextTick:
		}
	}
}

func (r *Runner) execute(ctx context.Context, run *models.Run) {
	log := logging.L(ctx, r.logger).With(
		slog.String("run_id", run.ID),
		slog.String("job", run.JobName),
		slog.Int("attempt", run.Attempt),
	)
	log.Info("runner: executing run")

	start := time.Now()

	payload := WebhookPayload{
		RunID:        run.ID,
		JobName:      run.JobName,
		ScheduledFor: run.ScheduledFor,
		Attempt:      run.Attempt,
		Config:       run.Config,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		r.failRun(ctx, run, "failed to marshal payload: "+err.Error(), nil)
		return
	}

	reqCtx, cancel := context.WithTimeout(ctx,
		time.Duration(run.WebhookTimeout)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, run.WebhookURL,
		bytes.NewReader(body))
	if err != nil {
		r.failRun(ctx, run, "failed to build request: "+err.Error(), nil)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Scheduler-Run-ID", run.ID)

	// Sign the request if a webhook secret is configured.
	if run.WebhookSecret != "" {
		sig := hmacSignature(body, run.WebhookSecret)
		req.Header.Set("X-Scheduler-Signature", "sha256="+sig)
	}

	resp, err := r.httpClient.Do(req)
	durationMs := int(time.Since(start).Milliseconds())

	if err != nil {
		r.failRun(ctx, run, "http error: "+err.Error(), nil)
		models.UpdateLastResult(ctx, r.pool, run.JobID, false, time.Now())
		return
	}
	defer resp.Body.Close()

	var respBuf bytes.Buffer
	respBuf.ReadFrom(resp.Body)
	respBody := respBuf.String()
	status := resp.StatusCode

	if status >= 200 && status < 300 {
		if err := models.MarkSuccess(ctx, r.pool, run.ID, status, respBody, durationMs); err != nil {
			log.Error("runner: failed to mark success", slog.Any("error", err))
		}
		models.UpdateLastResult(ctx, r.pool, run.JobID, true, time.Now())
		log.Info("runner: run succeeded",
			slog.Int("http_status", status),
			slog.Int("duration_ms", durationMs),
		)
		return
	}

	errDetail := fmt.Sprintf("webhook returned %d: %s", status, truncate(respBody, 512))
	r.failRun(ctx, run, errDetail, &status)
	models.UpdateLastResult(ctx, r.pool, run.JobID, false, time.Now())
}

func (r *Runner) failRun(ctx context.Context, run *models.Run, detail string, httpStatus *int) {
	log := logging.L(ctx, r.logger).With(
		slog.String("run_id", run.ID),
		slog.String("job", run.JobName),
	)
	log.Warn("runner: run failed", slog.String("detail", detail))

	if err := models.MarkFailed(ctx, r.pool, run, detail, httpStatus); err != nil {
		log.Error("runner: failed to mark run as failed", slog.Any("error", err))
	}
}

func hmacSignature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
