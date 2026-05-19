# Deploy Keep Swinging Web (free-friendly)

## “Free forever” hobby (recommended)

**Fly.io** is only a **short trial** for new accounts, not an ongoing free tier—see [Fly.io free trial](https://fly.io/docs/about/free-trial/). For a hobby stack that stays **$0** month after month, use:

| Layer | Recommendation | Why |
|--------|----------------|-----|
| **UI** | **Netlify**, **Cloudflare Pages**, or **Render static** | Generous ongoing free static hosting |
| **API** | **Render** [Hobby / free web service](https://render.com/docs/free) (Dockerfile) | Ongoing free tier (idle spin-down, monthly hour cap—see their docs) |
| **Redis** | **[Upstash](https://upstash.com/)** free Redis (TLS) | Durable sessions; use **`REDIS_TLS=true`** |

Do **not** use **[Render’s free “Key Value”](https://render.com/docs/free#free-key-value)** as your only Redis for this app: it is **in-memory only**—restarts wipe all sessions. Upstash (or Redis in your own VM) is the right fit.

**More control / no spin-down:** **Oracle Cloud “Always Free”** (Ampere VM): run Docker yourself with this app + `redis:7-alpine` or still use Upstash. More setup, but no 15‑minute sleep.

---

This app has two parts:

| Part | What it is | Typical free host |
|------|------------|-------------------|
| **Frontend** | Static HTML/CSS/JS in `web/static/` | **Netlify**, Cloudflare Pages, GitHub Pages |
| **Backend** | Go server + **Redis** (sessions, API at `/api/...`) | **Render** (free web), **Oracle VM**, or any VPS |

**Netlify, Vercel, GitHub Pages, and Cloudflare Pages cannot run this Go server or Redis.** They only host the static UI. You deploy the **API somewhere that runs containers or binaries**, then point the static host at that API URL.

**Simplest ongoing $0 setup:** **Upstash Redis** + **Render free web service** (Docker) + **Netlify** for the UI.  
**Simplest single-URL setup:** **Render** Docker URL only (no Netlify), or **Oracle VM** + Docker Compose.

---

## A. Recommended: Netlify (UI) + Render (API) + Upstash (Redis)

### Step 1 — Create a free Redis (TLS)

1. Sign up at [Upstash](https://upstash.com/) and create a **Regional** Redis database (free tier is enough for hobby use).
2. In the console, copy:
   - **Endpoint** (host/port), e.g. `xxxxx.upstash.io:6379`
   - **Password**
3. This server expects `host:port` in `REDIS_ADDR` and **TLS** enabled (see Step 2).

### Step 2 — Deploy the API on Render (Docker)

1. New **Web Service** → connect this repo → **Docker** runtime → instance type **Free**.
2. Add **environment variables** (Render dashboard → **Environment**):
   - `REDIS_ADDR` = Upstash host:port
   - `REDIS_PASSWORD` = Upstash password
   - `REDIS_TLS` = `true`
   - Set `LISTEN_ADDR` to `:$PORT` (Render sets `PORT`; binding to `:$PORT` listens on all interfaces).
3. Deploy and copy the service URL (e.g. `https://keep-swinging-api.onrender.com`).

**Limits:** Free web apps **spin down after ~15 minutes idle** (~1 minute cold start), and Render caps **750 free instance hours/month** per workspace—enough for one always-running hobby service if it sleeps when idle. Read [Render free docs](https://render.com/docs/free).

**CORS:** The API already sends `Access-Control-Allow-Origin: *`, so the Netlify origin can call it.

**Fly.io (optional):** usable if you are **paying** or still on a trial; same env vars as Render.

### Step 3 — Deploy the UI on Netlify

1. Push this repo to GitHub (or GitLab/Bitbucket).
2. In [Netlify](https://www.netlify.com/): **Add new site** → **Import an existing project** → pick the repo.
3. Netlify reads [`netlify.toml`](./netlify.toml): build runs `scripts/netlify-build.sh` and publishes `web/static/`.
4. Under **Site settings → Environment variables**, add:
   - **`KEEP_SWINGING_API_BASE`** = your API URL **with no trailing slash**, e.g. `https://keep-swinging-api.onrender.com`
5. Trigger a new deploy (**Deploys → Trigger deploy**).

At build time, Netlify writes `web/static/config.js` so the browser calls your Render API. **Share links** (`Copy link`) use your Netlify URL, which is what you want.

### Step 4 — Smoke test

1. Open your Netlify URL; create a session with ≥4 players.
2. If the UI errors immediately, check: API URL typo, missing `REDIS_TLS=true`, wrong Redis password, or Render logs.

---

## B. All-in-one API + UI (no Netlify)

If you run a single Docker container (or `go run`) with Redis configured, open the app at **that host** only. Leave **`KEEP_SWINGING_API_BASE` unset** on Netlify if you are not using Netlify; the committed [`web/static/config.js`](./web/static/config.js) keeps `window.__KEEP_SWINGING_API_BASE__ = ''` so all `fetch` calls stay **same-origin**.

Good ongoing $0 options for “one URL”:

- **Render** — same Dockerfile as section A; open the Render URL (cold start after idle).
- **Oracle Cloud “Always Free” VM** — install Docker, `docker compose` with this image + `redis:7-alpine` on a private network (more setup, no host-enforced spin-down).

---

## Environment variables (API server)

| Variable | Default | Meaning |
|----------|---------|---------|
| `REDIS_ADDR` | `127.0.0.1:6379` | Redis `host:port` |
| `REDIS_TLS` | (off) | Set `true` for TLS (e.g. Upstash) |
| `REDIS_PASSWORD` | (empty) | Redis AUTH password |
| `REDIS_DB` | `0` | Redis DB index |
| `SESSION_TTL_DAYS` | `30` | Session TTL in Redis (days) |
| `LISTEN_ADDR` | `:8080` | HTTP bind (use `:$PORT` if the platform requires it) |

Frontend (Netlify build):

| Variable | Meaning |
|----------|---------|
| `KEEP_SWINGING_API_BASE` | Full API origin, no trailing slash (e.g. `https://api.example.com`) |

---

## Free tier caveats (read once)

- **Idle sleep:** Many free web services stop after inactivity; first request can take **30–60+ seconds**.
- **Redis caps:** Free Redis has connection and bandwidth limits; fine for personal use.
- **Netlify build:** If you change only the API URL, change **`KEEP_SWINGING_API_BASE`** in Netlify and **redeploy** so `config.js` is regenerated.

---

## Local development

Same as [README](./README.md): Redis + `go run ./cmd/server`, open `http://localhost:8080`. With empty `config.js` API base, the UI talks to the same origin.

---

## Checklist

- [ ] Redis reachable from the API host; `REDIS_ADDR` and password set.
- [ ] `REDIS_TLS=true` for Upstash (and most TLS-only providers).
- [ ] API serves over HTTPS; `KEEP_SWINGING_API_BASE` on Netlify matches that URL exactly (no trailing `/`).
- [ ] Netlify rebuild after changing `KEEP_SWINGING_API_BASE`.

---

## Troubleshooting

### `POST .../api/sessions` → **404** on `*.netlify.app`

The browser is calling **Netlify** instead of your Go API. Netlify has no `/api` route, so you get 404.

1. In Netlify: **Site configuration → Environment variables** add **`KEEP_SWINGING_API_BASE`** = your API origin only, e.g. `https://keep-swinging-api.onrender.com` (**no** trailing `/`, **no** `/api`).
2. **Trigger deploy** → **Clear cache and deploy site** (or push a commit). The build must rerun so `scripts/netlify-build.sh` regenerates `config.js`.
3. Hard-refresh the site; in DevTools → Network, `POST` should go to your Render (etc.) host, not `netlify.app`.

Also confirm the API itself works:

```bash
curl -sS -X POST https://YOUR-API-HOST/api/sessions \
  -H 'Content-Type: application/json' \
  -d '{"sport":"padel","players":["A","B","C","D"]}'
```

You should get JSON with a `id` field.

---

## Optional: other static hosts

**Cloudflare Pages** or **GitHub Pages:** publish the `web/static` folder. You must inject the API base manually or with a small CI step (equivalent to `scripts/netlify-build.sh`): produce `config.js` containing:

```js
window.__KEEP_SWINGING_API_BASE__ = 'https://your-api-host';
```

Then load `config.js` before `app.js` (already the order in [`index.html`](./web/static/index.html)).
