# svc-scheduler

Generic, database-driven HTTP job scheduler. Zero domain knowledge. Two processes — planner and runner — in one container. Any service registers jobs by inserting rows into the `jobs` table and exposing a webhook endpoint.

## Architecture

- **Planner** — ticks on a configurable interval, evaluates each enabled job's cron schedule, and creates `runs` rows for any jobs that are due.
- **Runner** — worker pool that claims pending runs and executes them via HTTP POST to the job's `webhook_url`.
- **Admin API** — internal HTTP endpoints for listing jobs/runs and triggering manual runs.

## Webhook contract

Every job webhook receives a POST with this JSON body:

```json
{
  "run_id": "uuid",
  "job_name": "sync-mtgjson",
  "scheduled_for": "2026-03-25T02:00:00Z",
  "attempt": 1,
  "config": {}
}
```

If `webhook_secret` is set on the job, the request includes:

```
X-Scheduler-Signature: sha256=<hmac-sha256 of body>
```

Return `2xx` for success; anything else triggers retry logic. The response body is stored in `runs.response_body` (truncated to 4096 chars).

## Configuration

Copy `.env.example` to `.env` and adjust values:

| Variable                   | Default        | Description                        |
|----------------------------|----------------|------------------------------------|
| `DATABASE_URL`             | required       | PostgreSQL connection string       |
| `HTTP_PORT`                | `8080`         | Admin API port                     |
| `ADMIN_TOKEN`              | —              | Static token for admin endpoints   |
| `PLANNER_INTERVAL_SECONDS` | `30`           | How often the planner ticks        |
| `WORKER_POOL_SIZE`         | `5`            | Concurrent run workers             |
| `WORKER_INTERVAL_SECONDS`  | `5`            | How often the runner polls         |

## Admin API

All endpoints require `X-Admin-Token` header when `ADMIN_TOKEN` is set.

| Method | Path                          | Description                  |
|--------|-------------------------------|------------------------------|
| GET    | `/admin/jobs`                 | List enabled jobs             |
| GET    | `/admin/runs`                 | List last 100 runs            |
| POST   | `/admin/jobs/{jobName}/trigger` | Trigger an immediate run    |

## Development

```bash
make tidy      # go mod tidy
make vet       # go vet ./...
make build     # compile to bin/scheduler
make test      # run tests
```
