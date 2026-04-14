package models

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Job struct {
	ID              string
	Name            string
	Description     string
	Schedule        string
	Timezone        string
	Enabled         bool
	WebhookURL      string
	WebhookTimeout  int
	WebhookSecret   string
	MaxAttempts     int
	BackoffStrategy string
	BackoffSeconds  int
	MissedPolicy    string
	Priority        int
	Config          json.RawMessage
	CreatedAt       time.Time
	UpdatedAt       time.Time
	LastScheduledAt *time.Time
	LastSuccessAt   *time.Time
	LastFailureAt   *time.Time
}

// EnabledJobs returns all jobs where enabled = true.
func EnabledJobs(ctx context.Context, pool *pgxpool.Pool) ([]Job, error) {
	rows, err := pool.Query(ctx, `
        SELECT id, name, description, schedule, timezone, enabled,
               webhook_url, webhook_timeout, webhook_secret,
               max_attempts, backoff_strategy, backoff_seconds,
               missed_policy, priority, config,
               created_at, updated_at, last_scheduled_at,
               last_success_at, last_failure_at
        FROM jobs
        WHERE enabled = true
        ORDER BY priority ASC
    `)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(
			&j.ID, &j.Name, &j.Description, &j.Schedule, &j.Timezone,
			&j.Enabled, &j.WebhookURL, &j.WebhookTimeout, &j.WebhookSecret,
			&j.MaxAttempts, &j.BackoffStrategy, &j.BackoffSeconds,
			&j.MissedPolicy, &j.Priority, &j.Config,
			&j.CreatedAt, &j.UpdatedAt, &j.LastScheduledAt,
			&j.LastSuccessAt, &j.LastFailureAt,
		); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// UpdateLastScheduled sets last_scheduled_at to now for the given job.
func UpdateLastScheduled(ctx context.Context, pool *pgxpool.Pool, jobID string, t time.Time) error {
	_, err := pool.Exec(ctx, `
        UPDATE jobs SET last_scheduled_at = $1, updated_at = now()
        WHERE id = $2
    `, t, jobID)
	return err
}

// UpdateLastResult sets last_success_at or last_failure_at depending on success.
func UpdateLastResult(ctx context.Context, pool *pgxpool.Pool, jobID string, success bool, t time.Time) error {
	var query string
	if success {
		query = `UPDATE jobs SET last_success_at = $1, updated_at = now() WHERE id = $2`
	} else {
		query = `UPDATE jobs SET last_failure_at = $1, updated_at = now() WHERE id = $2`
	}
	_, err := pool.Exec(ctx, query, t, jobID)
	return err
}
