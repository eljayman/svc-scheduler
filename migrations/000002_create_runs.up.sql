CREATE TABLE runs (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id          UUID        NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    job_name        TEXT        NOT NULL,  -- denormalized for query convenience

    -- status lifecycle: pending → running → success | failed | missed
    status          TEXT        NOT NULL DEFAULT 'pending'
                                CHECK (status IN ('pending','running','success','failed','missed')),

    -- scheduling context
    scheduled_for   TIMESTAMPTZ NOT NULL,  -- when this run was due
    attempt         INTEGER     NOT NULL DEFAULT 1,
    max_attempts    INTEGER     NOT NULL,
    priority        INTEGER     NOT NULL,

    -- retry
    backoff_strategy TEXT       NOT NULL,
    backoff_seconds  INTEGER    NOT NULL,
    retry_after      TIMESTAMPTZ,          -- set after a failure, runner won't pick up until then

    -- runtime config snapshot — captured at planning time so job changes
    -- don't affect in-flight or queued runs
    webhook_url     TEXT        NOT NULL,
    webhook_timeout INTEGER     NOT NULL,
    webhook_secret  TEXT,
    config          JSONB       NOT NULL DEFAULT '{}',

    -- execution detail
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ,
    duration_ms     INTEGER,
    http_status     INTEGER,              -- response code from webhook
    response_body   TEXT,                -- truncated to 4096 chars
    error_detail    TEXT,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Runner uses this to find the next job to execute.
-- SKIP LOCKED in the query makes this safe for concurrent runners.
CREATE INDEX idx_runs_pending_priority ON runs (priority ASC, scheduled_for ASC)
    WHERE status = 'pending';

-- Planner uses this to find the most recent run per job.
CREATE INDEX idx_runs_job_scheduled ON runs (job_id, scheduled_for DESC);
