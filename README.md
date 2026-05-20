# Keep Swinging Web

Mobile-friendly web app to run **doubles** sessions for **tennis** and **padel**: fair **next matchup** suggestions (players with fewer games get priority), **reshuffle** when someone passes, and simple **score tracking**. Backend is **Go** + **Redis** (no separate SQL database).

See **[PLAN.md](./PLAN.md)** for product decisions and API overview.

## Requirements

- Go **1.22+**
- Redis **6+** (local Docker, Upstash, Redis Cloud, etc.)

## Run locally

1. Start Redis, for example:

   ```bash
   docker run --rm -p 6379:6379 redis:7-alpine
   ```

2. Copy env template and adjust if needed:

   ```bash
   cp .env.example .env
   ```

3. Run the server (loads `.env` is **not** automatic — export vars or use a tool; simplest is defaults):

   ```bash
   cd /path/to/keep-swinging-web
   go run ./cmd/server
   ```

   Defaults: Redis at `127.0.0.1:6379`, HTTP on `:8080`.

4. Open **http://localhost:8080**

Environment variables (see `.env.example`):

| Variable | Meaning |
|----------|---------|
| `REDIS_ADDR` | Redis host:port |
| `REDIS_TLS` | Set `true` for TLS Redis (e.g. Upstash) |
| `REDIS_PASSWORD` | Optional auth |
| `REDIS_DB` | Database number (default `0`) |
| `SESSION_TTL_DAYS` | TTL for session JSON in Redis (default `30`) |
| `LISTEN_ADDR` | Bind address (default `:8080`; on Render, `PORT` is used if `LISTEN_ADDR` unset) |
| `CORS_ALLOWED_ORIGINS` | Optional comma-separated browser origins (e.g. your Netlify URL). Empty = `*` |

## API

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/sessions` | Body: `{"sport":"padel","players":["Alice",...]}` (4–16 names) |
| `GET` | `/api/health` | `200` + `{"ok":true}` when Redis is reachable (use to detect cold start / readiness) |
| `GET` | `/api/sessions/{id}` | Full session JSON |
| `POST` | `/api/sessions/{id}/matches` | Body: `team_a_ids`, `team_b_ids` (2 each), `score_a`, `score_b` |
| `POST` | `/api/sessions/{id}/reshuffle` | Optional body: `{"exclude_player_ids":["..."]}` |
| `POST` | `/api/sessions/{id}/reset` | Clears match history and zeros standings; same player ids/names | For split hosting later, enable CORS on the API (already `*` for simple cases).

## Scheduling

- **Fairness:** Among valid lineups, prefer players with fewer total games played on court.
- **Partner rotation:** We avoid pairing two players together again until **each** has partnered **every other person** in the session at least once (using saved match history). If that leaves no legal lineup for a pick, we temporarily relax and use fairness only.
- **Standings:** **Match points** tab: raw total = sum of your side’s game scores in each match you played; **adj** adds +1 per match you sat out. **League** tab: raw pts = 3×wins + 1×draws; **adj** adds +1 per sit-out. Rank uses **adj** first, with competition-style ties.

Implementation: `internal/scheduler/doubles.go`.

## Deploy notes

Free-friendly steps (Netlify + Render + Upstash for ongoing $0 hobby; Oracle VM optional): **[DEPLOY.md](./DEPLOY.md)**. Plan to expose Render **only via** Netlify’s `/api` function (secrets, checklist): **[function.md](./function.md)**.

- **UI on Netlify (free):** connect the repo; set `KEEP_SWINGING_API_BASE` to your API URL. Build uses [`scripts/netlify-build.sh`](./scripts/netlify-build.sh) and publishes `web/static/`.
- **API:** must run on a host that supports Docker or Go (not Netlify). Use `REDIS_TLS=true` with **Upstash** and the other vars from [`.env.example`](./.env.example).
- **UI + API together:** deploy the [`Dockerfile`](./Dockerfile) only and open that URL; leave API base empty in `web/static/config.js`.

## Development

```bash
go test ./...
go build -o keep-swinging-web ./cmd/server
```

## Layout

```text
cmd/server/          # HTTP entrypoint
internal/api/        # REST handlers
internal/session/    # models
internal/scheduler/  # doubles fairness + reshuffle
internal/redisstore/ # Redis JSON persistence
web/static/          # embedded UI
web/embed.go         # embed directive (paths relative to package dir)
```
