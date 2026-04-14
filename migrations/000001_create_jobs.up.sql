CREATE TABLE jobs (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name             TEXT        NOT NULL UNIQUE,
    description      TEXT,

    -- scheduling
    schedule         TEXT        NOT NULL,  -- standard 5-field cron expression
    timezone         TEXT        NOT NULL DEFAULT 'UTC',
    enabled          BOOLEAN     NOT NULL DEFAULT true,

    -- webhook
    webhook_url      TEXT        NOT NULL,
    webhook_timeout  INTEGER     NOT NULL DEFAULT 30,  -- seconds
    webhook_secret   TEXT,                              -- HMAC secret, optional

    -- retry policy
    max_attempts     INTEGER     NOT NULL DEFAULT 3,
    backoff_strategy TEXT        NOT NULL DEFAULT 'fixed'
                                 CHECK (backoff_strategy IN ('fixed', 'exponential')),
    backoff_seconds  INTEGER     NOT NULL DEFAULT 60,

    -- missed run policy
    -- 'skip' = if scheduler was down, mark as missed and move on
    missed_policy    TEXT        NOT NULL DEFAULT 'skip'
                                 CHECK (missed_policy IN ('skip')),

    -- priority (lower number = higher priority in runner)
    priority         INTEGER     NOT NULL DEFAULT 100,

    -- arbitrary JSON config passed to webhook as part of payload
    config           JSONB       NOT NULL DEFAULT '{}',

    -- metadata
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_scheduled_at TIMESTAMPTZ,  -- when planner last created a run for this job
    last_success_at  TIMESTAMPTZ,
    last_failure_at  TIMESTAMPTZ
);

CREATE INDEX idx_jobs_enabled_schedule ON jobs (enabled, schedule)
    WHERE enabled = true;
