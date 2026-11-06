# Pairing Logic Redesign — Americano / Mexicano, Next-Round Preview, Planned Rest

Date: 2026-11-06
Status: Approved

## Problem

With 14 players and 2 courts over ~2 hours, some players never met each other
(as teammates or opponents). Root causes in `internal/scheduler`:

1. Scoring (`roundScore`, `fairnessScore`, `mexicanoScore`) only considers
   GamesPlayed. There is no notion of "who has already met whom".
2. The only history signal is `buildPartnerIndex` (partner repeats only, with a
   silent fallback). Opponent repeats are invisible to the scheduler.
3. Mexicano ranks by GamesPlayed, not by performance, and has no canonical
   leaderboard pairing.

Reality check: 14 players = 91 unique pairs; a 2-court doubles round creates
only 12 meetings. Full coverage needs ~8 rounds, so 100% coverage in 2 hours is
mathematically impossible — but the scheduler should get as close as possible.

## Canonical definitions (researched)

- **Americano** (ilypadel.com, padeli.com): every player partners every other
  player at least once, and ideally plays against everyone as well. Schedule
  maximizes unique pairings. Odd counts: rotate sit-outs.
- **Mexicano** (liveforpadel.com): round 1 random; from round 2 on, rank players
  by cumulative individual points (fixed-point scoring, e.g. 32-point games).
  Pair per court: 1st+3rd vs 2nd+4th, 5th+7th vs 6th+8th, etc.

## Design

### 1. New `internal/scheduler/encounter.go`

- `buildEncounterIndex(matches)` → per unordered pair: partner count and
  opponent count (canonical key `min|max`).
- `playerPoints(matches)` → per player: sum of their team's score across
  recorded matches (TeamA → ScoreA, TeamB → ScoreB). Works for any scoring
  scale; this is the mexicano leaderboard.
- Both derive purely from `RecordedMatch` — no data model change.

### 2. Americano scoring (doubles + singles)

Per lineup: `penalty = 1000*partnerRepeats + 10*opponentRepeats`.
Round/suggestion score = Σ penalties + Σ GamesPlayed (rest fairness as
tiebreak). The existing hard partner-rotation pre-filter stays (it enforces
partner uniqueness and prunes the search; the 1000-weight term is a backstop
for the fallback path). Singles: only the 1 pair counts.

Exhaustive multi-court search in `PickRound` is kept; per-lineup penalties are
precomputed so complexity is unchanged.

### 3. Mexicano: deterministic leaderboard construction

Playing set (who plays vs rests) is unchanged: usable*4 players with the lowest
GamesPlayed (min Σ GP subset = the 8/4/… smallest GPs). Then rank the playing
players by cumulative points (desc, tie → player ID asc) and construct pairings
per style:

- `mexicano` (canonical): 1&3 vs 2&4, 5&7 vs 6&8, …
- `mexicano_top_vs_top`: 1&2 vs 3&4 per court.
- `mexicano_top_vs_bottom`: 1&4 vs 2&3 per court (strongest+weakest vs middle).

Courts receive quartets in rank order (first quartet → first court). No
randomness. Singles: 1v2, 3v4, …

**Reshuffle exception:** when `excludeKey` is set (user reshuffles the shown
round), deterministic construction would return the same lineup. Fall back to
a score-based search over candidate lineups using rank-distance scoring,
excluding the current lineup key.

`PickSuggestion` / `PickSuggestionSingles` (legacy single-court path) get the
same treatment: americano encounter-aware scoring; mexicano points-based
construction.

### 4. Next-round preview

`GET /api/sessions/{id}/next-round-preview` (read-only, nothing persisted).

- No open round: returns exactly what `POST /rounds` would create now.
- Open round: previews the round *after* it using a hypothetical state —
  recorded matches + the open round's pending slots treated as played:
  - Encounters and GamesPlayed are score-independent → americano preview is
    exact and stable.
  - Mexicano points need scores; pending games contribute 0 points until
    results land, so the preview refreshes automatically as courts finish.
- Response: the previewed `Round` shape + resting player IDs + a
  `hypothetical` flag. Never mutates session state or `SuggestionKey`.

### 5. Planned rest ("I want to rest next round")

- `Session.RestNextPlayerIDs []string` (`rest_next_player_ids`).
- `PUT /api/sessions/{id}/rest-next` body `{player_ids: [...]}`: set/clear the
  planned resting list; validates IDs exist and are active; empty list clears.
  Predictive 400 if resting would leave fewer than 4 eligible players.
- Preview and round generation (`generateNextRound`, `handleCreateRound`) use
  the existing `PickRoundWithRest` path with these IDs; the round records them
  as `RestingPlayerIDs` (UI already renders resting) and the planned list is
  cleared once consumed.
- Reshuffle keeps its own explicit resting behavior (unchanged).

### 6. Frontend (`web/static`)

- "Next up" panel: previewed court lineups + resting players, refreshed after
  every score entry; labels hypothetical previews as tentative.
- Roster: per-player "rest next round" toggle calling the rest-next endpoint;
  planned-rest badge; error surfaced if the request is rejected.

## Files touched

- `internal/scheduler/encounter.go` (new), `rounds.go`, `doubles.go`, `singles.go`
- `internal/session/model.go` (RestNextPlayerIDs)
- `internal/api/server.go` (preview + rest-next handlers, consumption)
- `web/static/index.html`, `app.js`, `style.css`
- Tests: `encounter_test.go` (new), updates to `rounds_test.go`,
  `doubles_test.go`, `internal/api/rounds_test.go`

## Edge cases

- No recorded matches → all points 0 → mexicano ranks by ID (round 1 acts as
  the random baseline); americano unaffected.
- Inactive players excluded everywhere (existing `activeShufflePlayers`).
- Planned rest + small roster → clear API error instead of a broken round.
- Deleted/pending courts: existing unused-slot behavior preserved.

## Testing

- Unit: encounter index, points derivation, americano repeat minimization with
  seeded match history, mexicano construction for all three styles, singles.
- API: preview with/without open round; rest-next validation, consumption and
  clearing; existing endpoint tests keep passing.
- `go test ./...` and `go vet ./...` must pass.
