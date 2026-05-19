# Keep Swinging Web — Implementation Plan

This plan matches **`Keep Swinging Web Requirement.md`** (original goals and tech stack), with these choices locked in:

- **Sport mode: doubles** — each match is two pairs (four players on court).
- **No stamina modeling** — we do not encode rules like “cannot play three times in a row.” If someone is tired, that is handled socially.
- **Reshuffle** — when the app suggests a lineup that fairly includes someone with fewer games played, but that person **refuses to play**, the operator can **reshuffle** to get a **different suggested match** without recording a result.

---

## 1. Product scope

| Area | Decision |
|------|-----------|
| Sports | Tennis and padel (same app flow; scoring details can stay simple at first). |
| Mode | **Doubles only** for v1. |
| Group size | Designed for ~6 people: **4 play, 2 sit** each match; rotate across matches. |
| Fairness | Prefer players who have played **fewer completed matches** when proposing the **next** match. |
| Partner rotation | Do **not** repeat the same doubles pairing until **each** player has partnered **every other** session member at least once (see section 2). |
| Refusal | Not a data field. Use **Reshuffle** to propose another valid doubles matchup. |

---

## 2. Doubles scheduling (concept)

**Inputs:** session player list (e.g. 6 names), each player’s **completed match count**.

**Suggested next match:**

1. Consider all ways to choose **4 players** from the session (for 6 players: 15 quartets).
2. For each quartet, consider **team splits** into two pairs (three splits per quartet).
3. **Score** each candidate lineup (see below); pick a top suggestion (ties broken randomly for variety).

**Fairness (which lineup to suggest):** prefer quartets that include people who have played **fewer completed matches** — they are more “due” for court time.

**Partner rotation:** From recorded matches we know past doubles partners. For each side-by-side pair `(p1, p2)`, we **do not** pair them together again until **p1 has partnered every other player in the session at least once**, and the same check for **p2** with **p1**. After someone has played with all possible partners, repeats are allowed again (“full cycle”). If applying this rule removes every candidate lineup (very rare), we **fall back** to fairness-only lineups for that pick.

**Practical v1 rule:** among valid doubles lineups (choice of 4 players + split into two pairs), rank them by **how underplayed the participants are**. A simple score is **the sum of each selected player’s games played** — **lower sums are better** (the suggestion pulls in people who have sat more). Break ties with randomness so repeats do not feel stale.

Document the exact ranking in code comments when implemented; adjust later if the group wants stricter balance.

**Reshuffle:**

- Does **not** increment anyone’s match count or record a match.
- Picks **another** lineup using the **same fairness ranking**, but **excludes the current suggestion** (and any recent reshuffles if you want to avoid ping-pong — optional).
- Optional later: exclude a specific player for this round (“sitting out”) via UI → reshuffle with that constraint.

---

## 3. Architecture

```text
Mobile browser → Go HTTP API → Redis
```

- **Redis**: session metadata, player list, `games_played` counters, ordered history of completed matches, and **current suggested matchup** (four players + team A/B).
- **No relational DB** per requirements.

---

## 4. Deployment notes

- **Netlify** (or similar) fits the **static/mobile UI** well.
- A **Go API** is easier on **Fly.io**, **Railway**, or **Render** (verify current free tiers). Typical split: UI on Netlify, API + Redis URL env on the compute host.
- **Free Redis**: **Upstash** or **Redis Cloud** free tier — verify limits when provisioning.

---

## 5. API sketch (doubles + reshuffle)

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/sessions` | Create session (sport, player names). |
| `GET` | `/sessions/{id}` | Session state, standings, **current suggested doubles** matchup. |
| `POST` | `/sessions/{id}/matches` | Record finished match: teams, score; updates counts and history; **refresh suggestion**. |
| `POST` | `/sessions/{id}/reshuffle` | Replace **suggested** matchup only (no score, no count changes). Optional body: `{ "exclude_player_ids": ["..."] }` if/when implemented. |

---

## 6. Repository layout (when code lands here)

```text
cmd/server/
internal/api/
internal/session/
internal/scheduler/    # fairness + reshuffle for doubles
internal/redisstore/
web/ or embed/       # mobile-first UI
```

---

## 7. Phases

| Phase | Deliverable |
|-------|-------------|
| **P0** | Session + players in Redis; doubles **suggestion** + **reshuffle**; minimal UI. |
| **P1** | Record match results and scores; standings; suggestion updates after each match. |
| **P2** | UX polish (large tap targets, shareable session link), optional “exclude player this round” on reshuffle. |

---

## 8. Decisions still open (small)

1. **Scoring detail**: store “games won/lost” only vs full set scores — start with simple W/L per match if needed.
2. **Session TTL** in Redis for abandoned sessions (e.g. 30 days) vs manual reset.

---

## Reference

Original requirements live with the project notes (`Keep Swinging Web Requirement.md`). A duplicate of this plan lives next to that file in Obsidian: **`Projects/Keep Swinging Web/PLAN.md`** — update both when the plan changes.
