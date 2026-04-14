package planner

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"

	"github.com/eljayman/mtg-common/logging"
	"github.com/eljayman/svc-scheduler/internal/models"
)

// Planner ticks on a fixed interval, evaluates each enabled job's cron
// schedule against its last_scheduled_at, and creates run rows for
// any jobs that are due.
type Planner struct {
	pool     *pgxpool.Pool
	interval time.Duration
	logger   *slog.Logger
	parser   cron.Parser
}

func New(pool *pgxpool.Pool, intervalSeconds int, logger *slog.Logger) *Planner {
	return &Planner{
		pool:     pool,
		interval: time.Duration(intervalSeconds) * time.Second,
		logger:   logger,
		// Standard 5-field cron: min hour dom mon dow
		parser: cron.NewParser(
			cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
		),
	}
}

// Run starts the planner loop. Blocks until ctx is cancelled.
func (p *Planner) Run(ctx context.Context) {
	p.logger.Info("planner started", slog.Duration("interval", p.interval))
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	// Run immediately on start, then on each tick.
	p.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			p.logger.Info("planner stopped")
			return
		case <-ticker.C:
			p.tick(ctx)
		}
	}
}

func (p *Planner) tick(ctx context.Context) {
	jobs, err := models.EnabledJobs(ctx, p.pool)
	if err != nil {
		logging.L(ctx, p.logger).Error("planner: failed to load jobs", slog.Any("error", err))
		return
	}

	now := time.Now().UTC()
	for _, job := range jobs {
		p.evaluateJob(ctx, job, now)
	}
}

func (p *Planner) evaluateJob(ctx context.Context, job models.Job, now time.Time) {
	log := logging.L(ctx, p.logger).With(
		slog.String("job", job.Name),
		slog.String("schedule", job.Schedule),
	)

	schedule, err := p.parser.Parse(job.Schedule)
	if err != nil {
		log.Error("planner: invalid cron expression", slog.Any("error", err))
		return
	}

	// Determine the window start — either last_scheduled_at or
	// a reasonable lookback (one planner interval) to avoid duplicates.
	windowStart := now.Add(-p.interval * 2)
	if job.LastScheduledAt != nil {
		windowStart = *job.LastScheduledAt
	}

	// Find all times the job was due between windowStart and now.
	due := nextOccurrences(schedule, windowStart, now)
	if len(due) == 0 {
		return
	}

	for _, t := range due {
		// Check if we already have a run for this scheduled time.
		last, err := models.LastRunForJob(ctx, p.pool, job.ID)
		if err != nil {
			log.Error("planner: failed to check last run", slog.Any("error", err))
			continue
		}
		if last != nil && !last.ScheduledFor.Before(t) {
			// Already scheduled or ran at this time.
			continue
		}

		// If the scheduler was down and missed this slot, apply missed policy.
		if t.Before(now.Add(-p.interval)) {
			log.Info("planner: missed run, marking as missed",
				slog.Time("scheduled_for", t))
			if err := models.CreateMissedRun(ctx, p.pool, job, t); err != nil {
				log.Error("planner: failed to create missed run", slog.Any("error", err))
			}
			continue
		}

		if err := models.CreateRun(ctx, p.pool, job, t); err != nil {
			log.Error("planner: failed to create run", slog.Any("error", err))
			continue
		}

		if err := models.UpdateLastScheduled(ctx, p.pool, job.ID, t); err != nil {
			log.Error("planner: failed to update last_scheduled_at", slog.Any("error", err))
		}

		log.Info("planner: scheduled run", slog.Time("scheduled_for", t))
	}
}

// nextOccurrences returns all times a schedule fires between after and before.
func nextOccurrences(s cron.Schedule, after, before time.Time) []time.Time {
	var times []time.Time
	t := s.Next(after)
	for !t.IsZero() && t.Before(before) {
		times = append(times, t)
		t = s.Next(t)
	}
	return times
}
