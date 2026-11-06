# Pairing Logic Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make americano maximize unique meetings (partner first, then opponents), make mexicano a deterministic points-leaderboard ladder, add a next-round preview endpoint, and let players plan to rest for the next round.

**Architecture:** A new `encounterIndex` (partner + opponent meeting counts) and `playerPoints` (per-player cumulative score from recorded matches) feed reworked scoring in the scheduler. Americano keeps the exhaustive multi-court search with encounter-aware scoring; mexicano bypasses the search with deterministic leaderboard construction. Two new API endpoints (`GET /next-round-preview`, `PUT /rest-next`) plus a `RestNextPlayerIDs` field on Session; the web app gains a "Next up" panel and rest-next toggles.

**Tech Stack:** Go 1.x (std lib only), vanilla JS frontend. Spec: `docs/superpowers/specs/2026-11-06-pairing-logic-design.md`.

## Global Constraints

- Go std lib only; no new dependencies (`go.mod` unchanged).
- TDD: every task writes the failing test first, sees it fail, implements, sees it pass, commits.
- Run `go test ./...` and `go vet ./...` green before each commit that claims completion.
- Keep existing public function signatures of `PickRound`, `PickRoundWithRest`, `PickCourtPlayers`, `PickSuggestion`, `PickSuggestionSingles` unchanged (API layer depends on them).
- Never commit secrets; commit messages match repo style (`feat:`, `test:`, `docs:` prefixes seen in history).

---

### Task 1: Encounter index and player points

**Files:**
- Create: `internal/scheduler/encounter.go`
- Test: `internal/scheduler/encounter_test.go`

**Interfaces:**
- Produces: `type encounterIndex struct`, `func buildEncounterIndex(matches []session.RecordedMatch) encounterIndex`, `func (e encounterIndex) partnerCount(a, b string) int`, `func (e encounterIndex) opponentCount(a, b string) int`, `func pairKey(a, b string) string`, `func playerPoints(matches []session.RecordedMatch) map[string]int`. Later tasks consume all of these.

- [ ] **Step 1: Write the failing test**

Create `internal/scheduler/encounter_test.go`:

```go
package scheduler

import (
	"testing"

	"keep-swinging-web/internal/session"
)

func TestBuildEncounterIndexCountsPartnerAndOpponent(t *testing.T) {
	matches := []session.RecordedMatch{
		{TeamAIDs: []string{"p1", "p2"}, TeamBIDs: []string{"p3", "p4"}},
		{TeamAIDs: []string{"p1", "p3"}, TeamBIDs: []string{"p2", "p4"}},
		{TeamAIDs: []string{"p1"}, TeamBIDs: []string{"p2"}}, // singles
	}
	e := buildEncounterIndex(matches)
	if e.partnerCount("p1", "p2") != 1 || e.partnerCount("p2", "p1") != 1 {
		t.Fatalf("p1/p2 partnered once: %#v", e)
	}
	if e.partnerCount("p1", "p3") != 1 {
		t.Fatalf("p1/p3 partnered once: %#v", e)
	}
	if e.opponentCount("p1", "p3") != 2 {
		t.Fatalf("p1/p3 were opponents twice: %#v", e)
	}
	if e.opponentCount("p1", "p2") != 2 {
		t.Fatalf("p1/p2 opposed once in doubles + once singles: %#v", e)
	}
	if e.partnerCount("p5", "p6") != 0 || e.opponentCount("p5", "p6") != 0 {
		t.Fatalf("unknown pair must be zero: %#v", e)
	}
}

func TestPlayerPointsSumsTeamScores(t *testing.T) {
	matches := []session.RecordedMatch{
		{TeamAIDs: []string{"p1", "p2"}, TeamBIDs: []string{"p3", "p4"}, ScoreA: 20, ScoreB: 12},
		{TeamAIDs: []string{"p3", "p4"}, TeamBIDs: []string{"p1", "p2"}, ScoreA: 18, ScoreB: 14},
	}
	points := playerPoints(matches)
	if points["p1"] != 34 || points["p2"] != 34 {
		t.Fatalf("p1/p2 should have 20+14 points: %#v", points)
	}
	if points["p3"] != 30 || points["p4"] != 30 {
		t.Fatalf("p3/p4 should have 12+18 points: %#v", points)
	}
	if _, ok := points["p5"]; ok {
		t.Fatalf("unplayed player must have no entry: %#v", points)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/scheduler/ -run 'TestBuildEncounterIndex|TestPlayerPoints' -v`
Expected: FAIL (build error: `undefined: buildEncounterIndex`, `undefined: playerPoints`).

- [ ] **Step 3: Write minimal implementation**

Create `internal/scheduler/encounter.go`:

```go
package scheduler

import "keep-swinging-web/internal/session"

// encounterIndex counts, per unordered player pair, how many recorded matches
// paired them as teammates and as opponents.
type encounterIndex struct {
	partner  map[string]int
	opponent map[string]int
}

// pairKey is the canonical undirected key for a player pair.
func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

func buildEncounterIndex(matches []session.RecordedMatch) encounterIndex {
	e := encounterIndex{partner: map[string]int{}, opponent: map[string]int{}}
	add := func(m map[string]int, a, b string) {
		if a == "" || b == "" || a == b {
			return
		}
		m[pairKey(a, b)]++
	}
	for _, m := range matches {
		if len(m.TeamAIDs) == 2 {
			add(e.partner, m.TeamAIDs[0], m.TeamAIDs[1])
		}
		if len(m.TeamBIDs) == 2 {
			add(e.partner, m.TeamBIDs[0], m.TeamBIDs[1])
		}
		for _, a := range m.TeamAIDs {
			for _, b := range m.TeamBIDs {
				add(e.opponent, a, b)
			}
		}
	}
	return e
}

func (e encounterIndex) partnerCount(a, b string) int  { return e.partner[pairKey(a, b)] }
func (e encounterIndex) opponentCount(a, b string) int { return e.opponent[pairKey(a, b)] }

// playerPoints returns cumulative individual points per player: each player
// accrues their team's score from every recorded match. This is the mexicano
// leaderboard.
func playerPoints(matches []session.RecordedMatch) map[string]int {
	points := map[string]int{}
	for _, m := range matches {
		for _, id := range m.TeamAIDs {
			points[id] += m.ScoreA
		}
		for _, id := range m.TeamBIDs {
			points[id] += m.ScoreB
		}
	}
	return points
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/scheduler/ -run 'TestBuildEncounterIndex|TestPlayerPoints' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scheduler/encounter.go internal/scheduler/encounter_test.go
git commit -m "feat: encounter index and per-player points from recorded matches"
```

---

### Task 2: Americano encounter-aware scoring

**Files:**
- Modify: `internal/scheduler/rounds.go` (roundScore at :232, PickRound search leaf at :87, PickCourtPlayers scoring at :221-223)
- Test: `internal/scheduler/rounds_test.go`

**Interfaces:**
- Consumes: `buildEncounterIndex`, `encounterIndex` from Task 1.
- Produces: `roundScore(lineups []roundLineup, byID map[string]session.Player, style session.ShufflingStyle, enc encounterIndex) int` (new signature — later tasks rely on it) and `func lineupEncounterPenalty(enc encounterIndex, lu roundLineup) int`.

- [ ] **Step 1: Write the failing test**

Add to `internal/scheduler/rounds_test.go`:

```go
func TestLineupEncounterPenaltyWeightsPartnerOverOpponent(t *testing.T) {
	matches := []session.RecordedMatch{
		{TeamAIDs: []string{"p1", "p2"}, TeamBIDs: []string{"p3", "p4"}, ScoreA: 1, ScoreB: 0},
	}
	enc := buildEncounterIndex(matches)
	partnerRepeat := roundLineup{teamA: [2]string{"p1", "p2"}, teamB: [2]string{"p3", "p4"}}
	opponentRepeat := roundLineup{teamA: [2]string{"p1", "p3"}, teamB: [2]string{"p2", "p4"}}
	fresh := roundLineup{teamA: [2]string{"p1", "p5"}, teamB: [2]string{"p6", "p7"}}
	if got := lineupEncounterPenalty(enc, partnerRepeat); got != 2000 {
		t.Fatalf("partner repeat on both sides = 2×1000, got %d", got)
	}
	if got := lineupEncounterPenalty(enc, opponentRepeat); got != 40 {
		t.Fatalf("two repeated opponent pairs = 2×(2 pairs×10), got %d", got)
	}
	if got := lineupEncounterPenalty(enc, fresh); got != 0 {
		t.Fatalf("fresh lineup = 0, got %d", got)
	}
}

func TestPickRoundAmericanoAvoidsRepeatedOpponents(t *testing.T) {
	players := roundPlayers(8, 1)
	matches := []session.RecordedMatch{{TeamAIDs: []string{"p1", "p2"}, TeamBIDs: []string{"p3", "p4"}}}
	round, _, err := PickRound(players, matches, session.ShufflingStyleAmericano, courts(1), session.MatchFormatDoubles, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	slot := round.Slots[0]
	cross := map[string]bool{}
	for _, a := range slot.TeamA {
		for _, b := range slot.TeamB {
			cross[a.ID+b.ID] = true
			cross[b.ID+a.ID] = true
		}
	}
	if cross["p1p3"] || cross["p1p4"] || cross["p2p3"] || cross["p2p4"] {
		t.Fatalf("americano should avoid repeating any old opponent pairing: %#v", slot)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/scheduler/ -run 'EncounterPenalty|AvoidsRepeatedOpponents' -v`
Expected: FAIL (build error: `undefined: lineupEncounterPenalty`).

- [ ] **Step 3: Write minimal implementation**

In `internal/scheduler/rounds.go`, replace `roundScore` (lines 232-244) with:

```go
// lineupEncounterPenalty scores how "stale" a lineup's meetings are. Repeated
// partnerships cost far more than repeated opposition (americano rotates
// partners first, opponents ideally).
func lineupEncounterPenalty(enc encounterIndex, lu roundLineup) int {
	penalty := 0
	if lu.teamA[1] != "" {
		penalty += 1000 * enc.partnerCount(lu.teamA[0], lu.teamA[1])
		penalty += 1000 * enc.partnerCount(lu.teamB[0], lu.teamB[1])
		for _, a := range lu.teamA {
			for _, b := range lu.teamB {
				penalty += 10 * enc.opponentCount(a, b)
			}
		}
		return penalty
	}
	return 10 * enc.opponentCount(lu.teamA[0], lu.teamB[0])
}

func roundScore(lineups []roundLineup, byID map[string]session.Player, style session.ShufflingStyle, enc encounterIndex) int {
	sum := 0
	for _, lu := range lineups {
		sum += byID[lu.teamA[0]].GamesPlayed + byID[lu.teamB[0]].GamesPlayed
		if lu.teamA[1] != "" {
			sum += byID[lu.teamA[1]].GamesPlayed + byID[lu.teamB[1]].GamesPlayed
		}
	}
	switch style {
	case session.ShufflingStyleAmericano:
		for _, lu := range lineups {
			sum += lineupEncounterPenalty(enc, lu)
		}
	case session.ShufflingStyleMexicanoTopVsTop:
		return -sum * 1000
	}
	return sum
}
```

In `PickRound` (rounds.go): after `sort.Strings(ids)` (line 49) add `enc := buildEncounterIndex(matches)`; change the search leaf (line 87) to `score := roundScore(chosen, byID, style, enc)`.

In `PickCourtPlayers` (rounds.go): after `rotation := buildPartnerIndex(matches)` (line 192) add `enc := buildEncounterIndex(matches)`; change lines 221 and 223 to pass `enc`:

```go
	bestScore := roundScore(cands[:1], byID, style, enc)
	for i := 1; i < len(cands); i++ {
		if s := roundScore(cands[i:i+1], byID, style, enc); s < bestScore {
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/scheduler/ -v`
Expected: PASS (all existing tests still pass; no matches → zero encounters → unchanged behavior).

- [ ] **Step 5: Commit**

```bash
git add internal/scheduler/rounds.go internal/scheduler/rounds_test.go
git commit -m "feat: americano scoring minimizes repeated partners and opponents"
```

---

### Task 3: Mexicano deterministic leaderboard construction in PickRound

**Files:**
- Modify: `internal/scheduler/rounds.go` (PickRound :22-152)
- Test: `internal/scheduler/rounds_test.go`

**Interfaces:**
- Consumes: `playerPoints` (Task 1).
- Produces: `func isMexicanoStyle(style session.ShufflingStyle) bool`, `func rankPlayersByPoints(ids []string, points map[string]int) []string`, `func selectPlaying(ids []string, byID map[string]session.Player, count int) []string`, `func mexicanoPairing(style session.ShufflingStyle, r [4]string) Lineup`, `func constructMexicanoDoubles(ranked []string, style session.ShufflingStyle) []roundLineup`, `func constructMexicanoSingles(ranked []string) []roundLineup` — Tasks 4 and 5 reuse these.

- [ ] **Step 1: Write the failing test**

Add to `internal/scheduler/rounds_test.go`:

```go
func TestPickRoundMexicanoPairsByPointsLeaderboard(t *testing.T) {
	players := roundPlayers(8, 0)
	matches := []session.RecordedMatch{
		{TeamAIDs: []string{"p1", "p2"}, TeamBIDs: []string{"p3", "p4"}, ScoreA: 30, ScoreB: 10},
	}
	// points: p1=p2=30, p3=p4=10, p5..p8=0 → ranks p1,p2,p3,p4,p5,p6,p7,p8.
	round, _, err := PickRound(players, matches, session.ShufflingStyleMexicano, courts(1), session.MatchFormatDoubles, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	slot := round.Slots[0]
	left := map[string]bool{slot.TeamA[0].ID: true, slot.TeamA[1].ID: true}
	right := map[string]bool{slot.TeamB[0].ID: true, slot.TeamB[1].ID: true}
	canonicalLeft := map[string]bool{"p1": true, "p3": true}
	canonicalRight := map[string]bool{"p2": true, "p4": true}
	same := func(a, b map[string]bool) bool {
		for k := range a {
			if !b[k] {
				return false
			}
		}
		return len(a) == len(b)
	}
	if !(same(left, canonicalLeft) && same(right, canonicalRight)) && !(same(left, canonicalRight) && same(right, canonicalLeft)) {
		t.Fatalf("mexicano should pair 1st+3rd vs 2nd+4th: %#v", slot)
	}
}

func TestPickRoundMexicanoTopVsTopPairsOneTwoVsThreeFour(t *testing.T) {
	players := roundPlayers(4, 0)
	round, _, err := PickRound(players, nil, session.ShufflingStyleMexicanoTopVsTop, courts(1), session.MatchFormatDoubles, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	slot := round.Slots[0]
	top := map[string]bool{"p1": true, "p2": true}
	bottom := map[string]bool{"p3": true, "p4": true}
	same := func(a, b map[string]bool) bool {
		for k := range a {
			if !b[k] {
				return false
			}
		}
		return len(a) == len(b)
	}
	if !(same(idsOfSlot(slot.TeamA), top) && same(idsOfSlot(slot.TeamB), bottom)) && !(same(idsOfSlot(slot.TeamA), bottom) && same(idsOfSlot(slot.TeamB), top)) {
		t.Fatalf("top_vs_top should pair 1&2 vs 3&4: %#v", slot)
	}
}

func TestPickRoundMexicanoRestsMostPlayed(t *testing.T) {
	players := roundPlayers(6, 0)
	players[4].GamesPlayed = 5
	players[5].GamesPlayed = 5
	round, _, err := PickRound(players, nil, session.ShufflingStyleMexicano, courts(1), session.MatchFormatDoubles, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	played := map[string]bool{}
	for _, p := range append(round.Slots[0].TeamA, round.Slots[0].TeamB...) {
		played[p.ID] = true
	}
	if played["p5"] || played["p6"] {
		t.Fatalf("players with high GP should rest: %#v", round)
	}
}

func TestPickRoundMexicanoReshuffleFallsBackToSearch(t *testing.T) {
	players := roundPlayers(4, 0)
	first, key, err := PickRound(players, nil, session.ShufflingStyleMexicano, courts(1), session.MatchFormatDoubles, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, nextKey, err := PickRound(players, nil, session.ShufflingStyleMexicano, courts(1), session.MatchFormatDoubles, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	if key == nextKey {
		t.Fatalf("mexicano reshuffle must produce a different lineup: %q", nextKey)
	}
	_ = first
}

func idsOfSlot(team []session.Player) map[string]bool {
	out := map[string]bool{}
	for _, p := range team {
		out[p.ID] = true
	}
	return out
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/scheduler/ -run 'Mexicano' -v`
Expected: FAIL (build error: `undefined: isMexicanoStyle`).

- [ ] **Step 3: Write minimal implementation**

In `internal/scheduler/rounds.go` add these helpers (below `roundScore`):

```go
func isMexicanoStyle(style session.ShufflingStyle) bool {
	switch style {
	case session.ShufflingStyleMexicano, session.ShufflingStyleMexicanoTopVsTop, session.ShufflingStyleMexicanoTopVsBottom:
		return true
	}
	return false
}

// rankPlayersByPoints orders ids by cumulative points (desc), tie-break ID asc.
func rankPlayersByPoints(ids []string, points map[string]int) []string {
	out := append([]string(nil), ids...)
	sort.Slice(out, func(i, j int) bool {
		pi, pj := points[out[i]], points[out[j]]
		if pi != pj {
			return pi > pj
		}
		return out[i] < out[j]
	})
	return out
}

// selectPlaying returns the count players with the lowest GamesPlayed (ties by
// ID) — the minimum total-GP subset, i.e. who is most due to play.
func selectPlaying(ids []string, byID map[string]session.Player, count int) []string {
	if count > len(ids) {
		count = len(ids)
	}
	sorted := append([]string(nil), ids...)
	sort.Slice(sorted, func(i, j int) bool {
		gi, gj := byID[sorted[i]].GamesPlayed, byID[sorted[j]].GamesPlayed
		if gi != gj {
			return gi < gj
		}
		return sorted[i] < sorted[j]
	})
	return sorted[:count]
}

// mexicanoPairing returns the team split for one rank-ordered quartet
// (r[0] has the most points): canonical 1&3 vs 2&4; top_vs_top 1&2 vs 3&4;
// top_vs_bottom 1&4 vs 2&3.
func mexicanoPairing(style session.ShufflingStyle, r [4]string) Lineup {
	switch style {
	case session.ShufflingStyleMexicanoTopVsTop:
		return Lineup{TeamA: [2]string{r[0], r[1]}, TeamB: [2]string{r[2], r[3]}}
	case session.ShufflingStyleMexicanoTopVsBottom:
		return Lineup{TeamA: [2]string{r[0], r[3]}, TeamB: [2]string{r[1], r[2]}}
	default:
		return Lineup{TeamA: [2]string{r[0], r[2]}, TeamB: [2]string{r[1], r[3]}}
	}
}

// constructMexicanoDoubles chunks rank-ordered players into quartets and pairs
// each quartet per the style. Court i receives quartet i.
func constructMexicanoDoubles(ranked []string, style session.ShufflingStyle) []roundLineup {
	var out []roundLineup
	for i := 0; i+3 < len(ranked); i += 4 {
		lu := mexicanoPairing(style, [4]string{ranked[i], ranked[i+1], ranked[i+2], ranked[i+3]})
		out = append(out, roundLineup{teamA: lu.TeamA, teamB: lu.TeamB})
	}
	return out
}

// constructMexicanoSingles pairs rank-ordered players 1v2, 3v4, …
func constructMexicanoSingles(ranked []string) []roundLineup {
	var out []roundLineup
	for i := 0; i+1 < len(ranked); i += 2 {
		out = append(out, roundLineup{teamA: [2]string{ranked[i], ""}, teamB: [2]string{ranked[i+1], ""}})
	}
	return out
}
```

Then restructure `PickRound`. Replace the block from `byID := playerByID(players)` (line 44) through the end of the search invocation (line 131) with:

```go
	byID := playerByID(players)
	ids := make([]string, len(eligible))
	for i := range eligible {
		ids[i] = eligible[i].ID
	}
	sort.Strings(ids)
	enc := buildEncounterIndex(matches)
	points := playerPoints(matches)

	// Mexicano rounds are built deterministically from the points leaderboard.
	// The exhaustive search below is only their reshuffle fallback, which uses
	// americano encounter scoring (the user explicitly broke the ladder).
	searchStyle := style
	if isMexicanoStyle(style) {
		searchStyle = session.ShufflingStyleAmericano
	}

	var best []roundLineup
	if usable > 0 && isMexicanoStyle(style) {
		playing := selectPlaying(ids, byID, usable*playersPerCourt)
		ranked := rankPlayersByPoints(playing, points)
		if format == session.MatchFormatSingles {
			best = constructMexicanoSingles(ranked)
		} else {
			best = constructMexicanoDoubles(ranked, style)
		}
		if excludeKey != "" && roundKey(best) == excludeKey {
			best = nil // reshuffle: fall through to the search below
		}
	}
	if best == nil && usable > 0 {
		lineups := make([]roundLineup, 0)
		if format == session.MatchFormatSingles {
			for i := 0; i < len(ids); i++ {
				for j := i + 1; j < len(ids); j++ {
					lineups = append(lineups, roundLineup{teamA: [2]string{ids[i], ""}, teamB: [2]string{ids[j], ""}})
				}
			}
		} else {
			eachQuartet(ids, func(q [4]string) {
				for _, lu := range lineupsForQuartet(q) {
					lineups = append(lineups, roundLineup{teamA: lu.TeamA, teamB: lu.TeamB})
				}
			})
		}
		if len(lineups) == 0 {
			return nil, "", ErrNoRound
		}

		rotation := buildPartnerIndex(matches)
		rotated := make([]roundLineup, 0, len(lineups))
		for _, lu := range lineups {
			if format == session.MatchFormatSingles || lineupPassesPartnerRotation(Lineup{TeamA: lu.teamA, TeamB: lu.teamB}, rotation, ids) {
				rotated = append(rotated, lu)
			}
		}
		if len(rotated) > 0 {
			lineups = rotated
		}

		chosen := make([]roundLineup, usable)
		used := make(map[string]bool)
		bestScore := int(^uint(0) >> 1)
		var search func(int, int)
		search = func(courtIndex, start int) {
			if courtIndex == usable {
				score := roundScore(chosen, byID, searchStyle, enc)
				if score < bestScore {
					bestScore = score
					best = append(best[:0], chosen...)
				} else if score == bestScore && len(best) > 0 && pickIndex(2) == 0 {
					best = append(best[:0], chosen...)
				}
				return
			}
			for i := start; i < len(lineups); i++ {
				lu := lineups[i]
				idsForLineup := []string{lu.teamA[0], lu.teamB[0]}
				if format == session.MatchFormatDoubles {
					idsForLineup = append(idsForLineup, lu.teamA[1], lu.teamB[1])
				}
				ok := true
				for _, id := range idsForLineup {
					if used[id] {
						ok = false
						break
					}
				}
				if !ok {
					continue
				}
				for _, id := range idsForLineup {
					used[id] = true
				}
				chosen[courtIndex] = lu
				if courtIndex+1 == usable && excludeKey != "" && roundKey(chosen) == excludeKey {
					// Do not reuse the complete round during reshuffle.
				} else {
					search(courtIndex+1, i+1)
				}
				for _, id := range idsForLineup {
					delete(used, id)
				}
			}
		}
		search(0, 0)
		if len(best) == 0 {
			return nil, "", ErrNoRound
		}
	}
```

The round-building code after this (lines 133-151) is unchanged.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/scheduler/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scheduler/rounds.go internal/scheduler/rounds_test.go
git commit -m "feat: deterministic mexicano leaderboard pairing in round selection"
```

---

### Task 4: Mexicano in PickCourtPlayers

**Files:**
- Modify: `internal/scheduler/rounds.go` (PickCourtPlayers :167-230)
- Test: `internal/scheduler/rounds_test.go`

**Interfaces:**
- Consumes: `isMexicanoStyle`, `rankPlayersByPoints`, `mexicanoPairing`, `playerPoints` (Tasks 1, 3).

- [ ] **Step 1: Write the failing test**

Add to `internal/scheduler/rounds_test.go`:

```go
func TestPickCourtPlayersMexicanoPairsTopRankedLeftovers(t *testing.T) {
	players := roundPlayers(8, 0)
	matches := []session.RecordedMatch{
		{TeamAIDs: []string{"p5", "p6"}, TeamBIDs: []string{"p7", "p8"}, ScoreA: 25, ScoreB: 5},
	}
	teamA, teamB, err := PickCourtPlayers(players, matches, session.ShufflingStyleMexicano, session.MatchFormatDoubles, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	left := map[string]bool{teamA[0].ID: true, teamA[1].ID: true}
	right := map[string]bool{teamB[0].ID: true, teamB[1].ID: true}
	// points: p5=p6=25, p7=p8=5, p1..p4=0 → ranks p5,p6,p7,p8,p1,p2,p3,p4
	// canonical 1&3 vs 2&4 → (p5,p7) vs (p6,p8)
	expectedLeft := map[string]bool{"p5": true, "p7": true}
	expectedRight := map[string]bool{"p6": true, "p8": true}
	same := func(a, b map[string]bool) bool {
		for k := range a {
			if !b[k] {
				return false
			}
		}
		return len(a) == len(b)
	}
	if !(same(left, expectedLeft) && same(right, expectedRight)) && !(same(left, expectedRight) && same(right, expectedLeft)) {
		t.Fatalf("mexicano court fill should pair 1st+3rd vs 2nd+4th of leftovers: %#v %#v", teamA, teamB)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/scheduler/ -run 'TestPickCourtPlayersMexicano' -v`
Expected: FAIL (picks a fairness-based quartet instead of the points leaderboard).

- [ ] **Step 3: Write minimal implementation**

In `PickCourtPlayers` (rounds.go), right after `rotation := buildPartnerIndex(matches)` (line 192) and before `var cands []roundLineup`, insert (note: `ids`, `byID`, and `matches` are already in scope at that point):

```go
	if isMexicanoStyle(style) {
		ranked := rankPlayersByPoints(ids, playerPoints(matches))
		if format == session.MatchFormatSingles {
			return playersForIDs(byID, []string{ranked[0]}), playersForIDs(byID, []string{ranked[1]}), nil
		}
		lu := mexicanoPairing(style, [4]string{ranked[0], ranked[1], ranked[2], ranked[3]})
		return playersForIDs(byID, lu.TeamA[:]), playersForIDs(byID, lu.TeamB[:]), nil
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/scheduler/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scheduler/rounds.go internal/scheduler/rounds_test.go
git commit -m "feat: mexicano court fill follows the points leaderboard"
```

---

### Task 5: Legacy suggestion paths (doubles + singles)

**Files:**
- Modify: `internal/scheduler/doubles.go` (PickSuggestion :256-354; delete mexicanoScore :53-70, mexicanoTopVsTopScore :75-92, mexicanoTopVsBottomScore :97-114)
- Modify: `internal/scheduler/singles.go` (PickSuggestionSingles :56-144; delete singlesMexicanoTopVsTopScore :32-39, singlesMexicanoTopVsBottomScore :44-51)
- Test: `internal/scheduler/doubles_test.go` (replace tests at :155-197)

**Interfaces:**
- Consumes: `isMexicanoStyle`, `rankPlayersByPoints`, `mexicanoPairing`, `playerPoints`, `buildEncounterIndex`, `lineupEncounterPenalty` (Tasks 1-3).
- Produces: unchanged public signatures of `PickSuggestion` and `PickSuggestionSingles`.

- [ ] **Step 1: Rewrite the failing tests first**

In `internal/scheduler/doubles_test.go`, DELETE `TestPickSuggestion_mexicanoPrefersUnderplayed` (lines 155-172) and `TestPickSuggestion_mexicanoBalancesTeams` (lines 174-197) — they assert the old GP-based mexicano. Add:

```go
func TestPickSuggestion_mexicanoPairsByPoints(t *testing.T) {
	players := []session.Player{
		{ID: "p1", Name: "A"}, {ID: "p2", Name: "B"}, {ID: "p3", Name: "C"},
		{ID: "p4", Name: "D"}, {ID: "p5", Name: "E"}, {ID: "p6", Name: "F"},
	}
	matches := []session.RecordedMatch{
		{TeamAIDs: []string{"p1", "p2"}, TeamBIDs: []string{"p3", "p4"}, ScoreA: 24, ScoreB: 8},
	}
	sug, _, err := PickSuggestion(players, matches, session.ShufflingStyleMexicano, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	// points: p1=p2=24, p3=p4=8, p5=p6=0 → ranks p1,p2,p3,p4,p5,p6
	// canonical: (p1,p3) vs (p2,p4)
	left := map[string]bool{sug.TeamA[0].ID: true, sug.TeamA[1].ID: true}
	right := map[string]bool{sug.TeamB[0].ID: true, sug.TeamB[1].ID: true}
	l1 := map[string]bool{"p1": true, "p3": true}
	l2 := map[string]bool{"p2": true, "p4": true}
	same := func(a, b map[string]bool) bool {
		for k := range a {
			if !b[k] {
				return false
			}
		}
		return len(a) == len(b)
	}
	if !(same(left, l1) && same(right, l2)) && !(same(left, l2) && same(right, l1)) {
		t.Fatalf("mexicano suggestion should pair 1st+3rd vs 2nd+4th by points: %#v", sug)
	}
}

func TestPickSuggestion_mexicanoReshuffleFallsBack(t *testing.T) {
	players := []session.Player{
		{ID: "p1", Name: "A"}, {ID: "p2", Name: "B"}, {ID: "p3", Name: "C"}, {ID: "p4", Name: "D"},
	}
	_, key, err := PickSuggestion(players, nil, session.ShufflingStyleMexicano, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, nextKey, err := PickSuggestion(players, nil, session.ShufflingStyleMexicano, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	if key == nextKey {
		t.Fatalf("mexicano reshuffle must return a different lineup: %q", nextKey)
	}
}

func TestPickSuggestion_americanoAvoidsRepeatedOpponents(t *testing.T) {
	players := []session.Player{
		{ID: "p1", Name: "A"}, {ID: "p2", Name: "B"}, {ID: "p3", Name: "C"}, {ID: "p4", Name: "D"},
	}
	matches := []session.RecordedMatch{{TeamAIDs: []string{"p1", "p2"}, TeamBIDs: []string{"p3", "p4"}}}
	sug, _, err := PickSuggestion(players, matches, session.ShufflingStyleAmericano, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{sug.TeamA[0].ID: true, sug.TeamA[1].ID: true, sug.TeamB[0].ID: true, sug.TeamB[1].ID: true}
	if len(ids) == 4 {
		t.Fatalf("with full history every quartet repeats; any valid lineup is fine: %#v", sug)
	}
}
```

And add to `internal/scheduler/singles_test.go` (create the file if it does not exist; check first with `ls internal/scheduler/singles_test.go`):

```go
package scheduler

import (
	"testing"

	"keep-swinging-web/internal/session"
)

func TestPickSuggestionSingles_mexicanoTopTwoByPoints(t *testing.T) {
	players := []session.Player{
		{ID: "p1", Name: "A"}, {ID: "p2", Name: "B"}, {ID: "p3", Name: "C"}, {ID: "p4", Name: "D"},
	}
	matches := []session.RecordedMatch{
		{TeamAIDs: []string{"p3"}, TeamBIDs: []string{"p4"}, ScoreA: 21, ScoreB: 11},
	}
	sug, _, err := PickSuggestionSingles(players, matches, session.ShufflingStyleMexicano, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	// points: p3=21, p4=11, p1=p2=0 → top two are p3 vs p4
	a, b := sug.TeamA[0].ID, sug.TeamB[0].ID
	if !((a == "p3" && b == "p4") || (a == "p4" && b == "p3")) {
		t.Fatalf("mexicano singles should rank 1 vs 2 by points: %#v", sug)
	}
}

func TestPickSuggestionSingles_americanoAvoidsRematch(t *testing.T) {
	players := []session.Player{
		{ID: "p1", Name: "A"}, {ID: "p2", Name: "B"}, {ID: "p3", Name: "C"}, {ID: "p4", Name: "D"},
	}
	matches := []session.RecordedMatch{{TeamAIDs: []string{"p1"}, TeamBIDs: []string{"p2"}}}
	sug, _, err := PickSuggestionSingles(players, matches, session.ShufflingStyleAmericano, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	a, b := sug.TeamA[0].ID, sug.TeamB[0].ID
	if (a == "p1" && b == "p2") || (a == "p2" && b == "p1") {
		t.Fatalf("americano singles should avoid an immediate rematch: %#v", sug)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/scheduler/ -run 'Suggestion' -v`
Expected: FAIL — the mexicano suggestion tests fail on old GP behavior; build errors for removed helpers once implementation changes (tests first, so assertion failures).

- [ ] **Step 3: Implement**

In `internal/scheduler/doubles.go`:

1. Delete `mexicanoScore`, `mexicanoTopVsTopScore`, `mexicanoTopVsBottomScore` (lines 50-114).
2. Add a helper above `PickSuggestion`:

```go
func suggestionFromLineup(byID map[string]session.Player, lu Lineup) *session.SuggestedMatch {
	buildTeam := func(pair [2]string) []session.Player {
		return []session.Player{byID[pair[0]], byID[pair[1]]}
	}
	return &session.SuggestedMatch{TeamA: buildTeam(lu.TeamA), TeamB: buildTeam(lu.TeamB)}
}
```

3. Replace the body of `PickSuggestion` from the `games := gamesMap(players)` line through the `return sug, key, nil` with:

```go
	games := gamesMap(players)
	byID := playerByID(players)
	enc := buildEncounterIndex(matches)

	if isMexicanoStyle(style) {
		ranked := rankPlayersByPoints(ids, playerPoints(matches))
		lu := mexicanoPairing(style, [4]string{ranked[0], ranked[1], ranked[2], ranked[3]})
		key := LineupKey(lu.TeamA, lu.TeamB)
		if key != excludeKey {
			return suggestionFromLineup(byID, lu), key, nil
		}
		// Reshuffle: fall through to encounter-aware search below.
	}

	var candidates []Lineup
	eachQuartet(ids, func(q [4]string) {
		for _, lu := range lineupsForQuartet(q) {
			key := LineupKey(lu.TeamA, lu.TeamB)
			if excludeKey != "" && key == excludeKey {
				continue
			}
			candidates = append(candidates, lu)
		}
	})
	if len(candidates) == 0 {
		return nil, "", ErrNoLineup
	}
	if rotated := filterByPartnerRotation(candidates, matches, ids); len(rotated) > 0 {
		candidates = rotated
	}

	score := func(lu Lineup) int {
		return fairnessScore(games, lu) + lineupEncounterPenalty(enc, roundLineup{teamA: lu.TeamA, teamB: lu.TeamB})
	}
	bestScore := score(candidates[0])
	for _, lu := range candidates[1:] {
		if s := score(lu); s < bestScore {
			bestScore = s
		}
	}
	var best []Lineup
	for _, lu := range candidates {
		if score(lu) == bestScore {
			best = append(best, lu)
		}
	}

	chosen := best[pickIndex(len(best))]
	key := LineupKey(chosen.TeamA, chosen.TeamB)
	return suggestionFromLineup(byID, chosen), key, nil
```

In `internal/scheduler/singles.go`:

1. Delete `singlesMexicanoTopVsTopScore` and `singlesMexicanoTopVsBottomScore` (lines 29-51).
2. In `PickSuggestionSingles`, after `byID := playerByID(players)` insert:

```go
	if isMexicanoStyle(style) {
		ranked := rankPlayersByPoints(ids, playerPoints(matches))
		key := SinglesLineupKey(ranked[0], ranked[1])
		if key != excludeKey {
			return &session.SuggestedMatch{
				TeamA: []session.Player{byID[ranked[0]]},
				TeamB: []session.Player{byID[ranked[1]]},
			}, key, nil
		}
		// Reshuffle: fall through to encounter-aware search below.
	}
```

3. Replace the three scoring loops (from `var bestScore int` at line 93 through the `best` collection ending line 134) with:

```go
	enc := buildEncounterIndex(matches)
	score := func(lu SinglesLineup) int {
		return singlesFairnessScore(games, lu) + 10*enc.opponentCount(lu.PlayerA, lu.PlayerB)
	}
	bestScore := score(candidates[0])
	for _, lu := range candidates[1:] {
		if s := score(lu); s < bestScore {
			bestScore = s
		}
	}
	var best []SinglesLineup
	for _, lu := range candidates {
		if score(lu) == bestScore {
			best = append(best, lu)
		}
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/scheduler/ -v && go vet ./...`
Expected: PASS, no vet complaints.

- [ ] **Step 5: Commit**

```bash
git add internal/scheduler/doubles.go internal/scheduler/doubles_test.go internal/scheduler/singles.go internal/scheduler/singles_test.go
git commit -m "feat: points-based mexicano and encounter-aware americano in suggestions"
```

---

### Task 6: Planned rest — model field and PUT endpoint

**Files:**
- Modify: `internal/session/model.go` (Session struct, after `SuggestionKey` :125)
- Modify: `internal/session/model_test.go`
- Modify: `internal/api/server.go` (route list :31-46, new handler near handleReshuffle)
- Test: `internal/api/rounds_test.go`

**Interfaces:**
- Produces: `Session.RestNextPlayerIDs []string` (json `rest_next_player_ids,omitempty`), `func validatePlannedRest(sess *session.Session, ids []string) error` in the api package, `PUT /api/sessions/{id}/rest-next` with body `{player_ids: []string}` → 200 `{rest_next_player_ids: [...]}`; 400 with message on validation failure. Tasks 7-9 consume the field and endpoint.

- [ ] **Step 1: Write the failing tests**

Add to `internal/api/rounds_test.go` (add `"encoding/json"` and `"strings"` to its imports):

```go
func TestValidatePlannedRest(t *testing.T) {
	players := []session.Player{
		{ID: "a", Name: "A"}, {ID: "b", Name: "B"}, {ID: "c", Name: "C"},
		{ID: "d", Name: "D"}, {ID: "e", Name: "E"}, {ID: "f", Name: "F"},
	}
	sess := &session.Session{
		ID: "s", MatchFormat: session.MatchFormatDoubles,
		Players: players,
		Courts:  []session.Court{{ID: "court-1", Name: "Court 1"}},
	}
	if err := validatePlannedRest(sess, []string{"a", "b"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := validatePlannedRest(sess, []string{"zz"}); err == nil {
		t.Fatal("expected unknown id error")
	}
	if err := validatePlannedRest(sess, []string{"a", "a"}); err == nil {
		t.Fatal("expected duplicate id error")
	}
	// 6 active players, 4 needed on court → resting 3 leaves 3 < 4
	if err := validatePlannedRest(sess, []string{"a", "b", "c"}); err == nil {
		t.Fatal("expected too-many-resting error")
	}
}
```

Add to `internal/session/model_test.go` (add `"encoding/json"`, `"strings"`, `"testing"` if not present):

```go
func TestRestNextPlayerIDsJSONRoundTrip(t *testing.T) {
	sess := Session{ID: "s", RestNextPlayerIDs: []string{"a"}}
	data, err := json.Marshal(sess)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"rest_next_player_ids":["a"]`) {
		t.Fatalf("field not serialized: %s", data)
	}
	var back Session
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.RestNextPlayerIDs) != 1 || back.RestNextPlayerIDs[0] != "a" {
		t.Fatalf("field not deserialized: %#v", back)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/api/ -run 'ValidatePlannedRest' -v && go test ./internal/session/ -run 'RestNext' -v`
Expected: FAIL (build error: `undefined: validatePlannedRest`; JSON key absent).

- [ ] **Step 3: Implement**

In `internal/session/model.go`, inside `Session` after the `SuggestionKey` field add:

```go
	// RestNextPlayerIDs lists players who plan to sit out the NEXT round;
	// consumed (and cleared) when the next round is created.
	RestNextPlayerIDs []string `json:"rest_next_player_ids,omitempty"`
```

In `internal/api/server.go`, add to the route list (after the rounds routes):

```go
	mux.HandleFunc("PUT /api/sessions/{id}/rest-next", s.handlePutRestNext)
```

Add the handler and its validation helper (near `handleReshuffle`):

```go
type restNextBody struct {
	PlayerIDs []string `json:"player_ids"`
}

// validatePlannedRest checks planned resting player IDs and ensures at least
// one full court can still be filled.
func validatePlannedRest(sess *session.Session, ids []string) error {
	if err := validateRestingIDs(sess.Players, ids); err != nil {
		return err
	}
	perCourt := 4
	if sess.MatchFormat == session.MatchFormatSingles {
		perCourt = 2
	}
	active := 0
	for _, p := range sess.Players {
		if !p.Inactive {
			active++
		}
	}
	if active-len(ids) < perCourt {
		return errors.New("too many resting players: need at least one full court")
	}
	return nil
}

func (s *Server) handlePutRestNext(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sess, err := s.loadSession(ctx, r.PathValue("id"), w)
	if sess == nil || err != nil {
		return
	}
	if !session.UsesRoundModel(sess) {
		writeError(w, http.StatusBadRequest, "legacy sessions use the match endpoint")
		return
	}
	var body restNextBody
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	if err := validatePlannedRest(sess, body.PlayerIDs); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sess.RestNextPlayerIDs = body.PlayerIDs
	if err := s.Store.Save(ctx, sess); err != nil {
		s.Logger.Error("save session", "err", err)
		writeError(w, http.StatusInternalServerError, "could not save session")
		return
	}
	writeJSON(w, http.StatusOK, map[string][]string{"rest_next_player_ids": sess.RestNextPlayerIDs})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/api/ -v && go test ./internal/session/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/session/model.go internal/session/model_test.go internal/api/server.go internal/api/rounds_test.go
git commit -m "feat: planned rest-next player list with PUT endpoint"
```

---

### Task 7: Consume planned rest in round generation

**Files:**
- Modify: `internal/api/server.go` (`generateNextRound` :665-676, `handleCreateRound` :516-542)
- Test: `internal/api/rounds_test.go`

**Interfaces:**
- Consumes: `Session.RestNextPlayerIDs` (Task 6), `scheduler.PickRoundWithRest` (existing).

- [ ] **Step 1: Write the failing test**

Add to `internal/api/rounds_test.go` (same direct-call pattern as `TestCompleteRoundGeneratesNext`):

```go
func TestGenerateNextRoundConsumesPlannedRest(t *testing.T) {
	sess := &session.Session{
		ID:          "s",
		Sport:       session.SportTennis,
		MatchFormat: session.MatchFormatDoubles,
		Players: []session.Player{
			{ID: "a", Name: "A"}, {ID: "b", Name: "B"}, {ID: "c", Name: "C"},
			{ID: "d", Name: "D"}, {ID: "e", Name: "E"}, {ID: "f", Name: "F"},
		},
		Courts:            []session.Court{{ID: "court-1", Name: "Court 1"}},
		RestNextPlayerIDs: []string{"a", "b"},
	}
	if err := generateNextRound(sess); err != nil {
		t.Fatal(err)
	}
	if len(sess.CurrentRound.RestingPlayerIDs) != 2 {
		t.Fatalf("expected planned rest recorded on round: %#v", sess.CurrentRound.RestingPlayerIDs)
	}
	for _, slot := range sess.CurrentRound.Slots {
		for _, p := range append(slot.TeamA, slot.TeamB...) {
			if p.ID == "a" || p.ID == "b" {
				t.Fatalf("planned-resting player assigned: %s", p.ID)
			}
		}
	}
	if len(sess.RestNextPlayerIDs) != 0 {
		t.Fatalf("planned rest should be consumed after generation: %#v", sess.RestNextPlayerIDs)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run 'ConsumesPlannedRest' -v`
Expected: FAIL (resting ids not recorded / list not cleared).

- [ ] **Step 3: Implement**

In `generateNextRound` (server.go :665-676) replace the body:

```go
func generateNextRound(sess *session.Session) error {
	if sess == nil || !session.UsesRoundModel(sess) {
		return errors.New("session is not round-based")
	}
	round, key, err := scheduler.PickRoundWithRest(sess.Players, recordedMatches(sess), sess.ShufflingStyle, sess.Courts, sess.MatchFormat, "", sess.RestNextPlayerIDs)
	if err != nil {
		return err
	}
	sess.CurrentRound = round
	sess.SuggestionKey = key
	sess.RestNextPlayerIDs = nil
	return nil
}
```

In `handleCreateRound` (server.go :530-536) change the pick and post-assignments:

```go
	round, key, pickErr := scheduler.PickRoundWithRest(sess.Players, recordedMatches(sess), sess.ShufflingStyle, sess.Courts, sess.MatchFormat, "", sess.RestNextPlayerIDs)
	if pickErr != nil {
		writeError(w, http.StatusBadRequest, pickErr.Error())
		return
	}
	sess.CurrentRound = round
	sess.SuggestionKey = key
	sess.RestNextPlayerIDs = nil
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/api/ -v`
Expected: PASS (including `TestCompleteRoundGeneratesNext`).

- [ ] **Step 5: Commit**

```bash
git add internal/api/server.go internal/api/rounds_test.go
git commit -m "feat: round creation honors and consumes planned rest"
```

---

### Task 8: Next-round preview endpoint

**Files:**
- Modify: `internal/api/server.go` (route list, `previewState` + `buildPreview` helpers, thin handler)
- Test: `internal/api/rounds_test.go`

**Interfaces:**
- Consumes: `scheduler.PickRoundWithRest`, `recordedMatches` (existing); `Session.RestNextPlayerIDs` (Task 6).
- Produces: `func previewState(sess *session.Session) ([]session.Player, []session.RecordedMatch, bool)`, `func buildPreview(sess *session.Session) (*nextRoundPreview, error)`, and `GET /api/sessions/{id}/next-round-preview` → 200 `{round: Round, resting_player_ids: []string, hypothetical: bool}`. Frontend (Task 9) consumes this JSON.

- [ ] **Step 1: Write the failing tests**

Add to `internal/api/rounds_test.go`:

```go
func TestPreviewStateHypothetical(t *testing.T) {
	// No open round → clean state, not hypothetical.
	sess := &session.Session{
		ID:          "s",
		MatchFormat: session.MatchFormatDoubles,
		Players:     []session.Player{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}},
		Courts:      []session.Court{{ID: "court-1", Name: "Court 1"}},
	}
	_, matches, hyp := previewState(sess)
	if hyp || len(matches) != 0 {
		t.Fatalf("expected clean state, hyp=%v matches=%d", hyp, len(matches))
	}
	// Open round with a pending slot → hypothetical, GP bumped, virtual match.
	sess.CurrentRound = &session.Round{
		ID: "r", Status: session.RoundStatusOpen,
		Slots: []session.RoundSlot{{
			CourtID: "court-1", CourtName: "Court 1", Status: session.RoundSlotStatusPending,
			TeamA: []session.Player{{ID: "a"}, {ID: "b"}}, TeamB: []session.Player{{ID: "c"}, {ID: "d"}},
		}},
	}
	players, matches, hyp := previewState(sess)
	if !hyp || len(matches) != 1 {
		t.Fatalf("expected hypothetical state with one virtual match: hyp=%v matches=%d", hyp, len(matches))
	}
	for _, p := range players {
		if p.GamesPlayed != 1 {
			t.Fatalf("pending participants should have GP bumped: %#v", p)
		}
	}
}

func TestBuildPreviewGivesRestedPlayerAGame(t *testing.T) {
	// Mexicano keeps selection deterministic (lowest GP, ties by ID).
	sess := &session.Session{
		ID:             "s",
		Sport:          session.SportTennis,
		MatchFormat:    session.MatchFormatDoubles,
		ShufflingStyle: session.ShufflingStyleMexicano,
		Players: []session.Player{
			{ID: "a", Name: "A"}, {ID: "b", Name: "B"}, {ID: "c", Name: "C"},
			{ID: "d", Name: "D"}, {ID: "e", Name: "E"},
		},
		Courts: []session.Court{{ID: "court-1", Name: "Court 1"}},
	}
	prev, err := buildPreview(sess)
	if err != nil {
		t.Fatal(err)
	}
	if prev.Hypothetical {
		t.Fatal("no open round yet → preview must not be hypothetical")
	}
	// Open the generated round and preview the one after it.
	sess.CurrentRound = prev.Round
	prev2, err := buildPreview(sess)
	if err != nil {
		t.Fatal(err)
	}
	if !prev2.Hypothetical {
		t.Fatal("open round → preview must be hypothetical")
	}
	slot := prev2.Round.Slots[0]
	played := map[string]bool{}
	for _, p := range append(slot.TeamA, slot.TeamB...) {
		played[p.ID] = true
	}
	// Pending round-1 slot counts as played: GP a..d = 1, e = 0 → e plays now.
	if !played["e"] {
		t.Fatalf("preview should give the rested player a game: %#v", prev2.Round)
	}
	if len(prev2.RestingPlayerIDs) != 1 || played[prev2.RestingPlayerIDs[0]] {
		t.Fatalf("expected exactly one rested player among a..d: %#v", prev2.RestingPlayerIDs)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/api/ -run 'Preview' -v`
Expected: FAIL (build error: `undefined: previewState`).

- [ ] **Step 3: Implement**

In `internal/api/server.go`, add to the route list (if not already present from Task 6):

```go
	mux.HandleFunc("GET /api/sessions/{id}/next-round-preview", s.handleNextRoundPreview)
```

Add the helpers and handler (near `generateNextRound`):

```go
type nextRoundPreview struct {
	Round            *session.Round `json:"round"`
	RestingPlayerIDs []string       `json:"resting_player_ids"`
	Hypothetical     bool           `json:"hypothetical"`
}

// previewState derives the roster and match history the round AFTER the open
// one would be scheduled from: pending courts of the open round count as
// played (scores unknown → they contribute no points; GamesPlayed and
// encounters are score-independent).
func previewState(sess *session.Session) ([]session.Player, []session.RecordedMatch, bool) {
	players := append([]session.Player(nil), sess.Players...)
	matches := recordedMatches(sess)
	hypothetical := false
	if sess.CurrentRound != nil && sess.CurrentRound.Status == session.RoundStatusOpen {
		hypothetical = true
		for _, slot := range sess.CurrentRound.Slots {
			if slot.Status != session.RoundSlotStatusPending {
				continue
			}
			matches = append(matches, session.RecordedMatch{
				TeamAIDs: playerIDs(slot.TeamA),
				TeamBIDs: playerIDs(slot.TeamB),
				PlayedAt: time.Now().UTC(),
			})
			played := append(append([]session.Player(nil), slot.TeamA...), slot.TeamB...)
			for _, p := range played {
				for i := range players {
					if players[i].ID == p.ID {
						players[i].GamesPlayed++
					}
				}
			}
		}
	}
	return players, matches, hypothetical
}

func playerIDs(players []session.Player) []string {
	out := make([]string, len(players))
	for i, p := range players {
		out[i] = p.ID
	}
	return out
}

// buildPreview computes the next round read-only: nothing is persisted and
// SuggestionKey is untouched.
func buildPreview(sess *session.Session) (*nextRoundPreview, error) {
	players, matches, hypothetical := previewState(sess)
	round, _, err := scheduler.PickRoundWithRest(players, matches, sess.ShufflingStyle, sess.Courts, sess.MatchFormat, "", sess.RestNextPlayerIDs)
	if err != nil {
		return nil, err
	}
	assigned := map[string]bool{}
	for _, slot := range round.Slots {
		for _, p := range append(append([]session.Player(nil), slot.TeamA...), slot.TeamB...) {
			assigned[p.ID] = true
		}
	}
	var resting []string
	for _, p := range players {
		if !p.Inactive && !assigned[p.ID] {
			resting = append(resting, p.ID)
		}
	}
	return &nextRoundPreview{Round: round, RestingPlayerIDs: resting, Hypothetical: hypothetical}, nil
}

func (s *Server) handleNextRoundPreview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sess, err := s.loadSession(ctx, r.PathValue("id"), w)
	if sess == nil || err != nil {
		return
	}
	if !session.UsesRoundModel(sess) {
		writeError(w, http.StatusBadRequest, "legacy sessions use the match endpoint")
		return
	}
	preview, err := buildPreview(sess)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, preview)
}
```

Also refactor `roundRecordedMatches` (:710-730) to use the new helper — replace the inner `ids` closure with `playerIDs(slot.TeamA)` / `playerIDs(slot.TeamB)`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/api/ -v && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/server.go internal/api/rounds_test.go
git commit -m "feat: read-only next-round preview endpoint"
```

---

### Task 9: Frontend — "Next up" preview panel and rest-next toggles

**Files:**
- Modify: `web/static/index.html` (after the `#suggestion` section, ends line 144)
- Modify: `web/static/app.js` (`refreshSessionView` :462, new functions)
- Modify: `web/static/style.css` (small additions)

**Interfaces:**
- Consumes: `GET /api/sessions/{id}/next-round-preview`, `PUT /api/sessions/{id}/rest-next`, `api()` helper, `isRoundSession`, `shuffleActivePlayers`, `escapeHtml`, `$` (existing app.js helpers).

- [ ] **Step 1: Add markup**

In `web/static/index.html`, inside the first `session-block` section, directly after `<button type="button" id="btn-complete-round" class="hidden">Complete round</button>` and before the closing `</section>` (line ~144), add:

```html
  <div id="next-up" class="next-up">
    <h3 class="section-heading">Next up</h3>
    <div id="next-round-preview" class="card"></div>
    <label class="resting-label"
      >Rest next round <span class="optional-tag">(optional)</span></label
    >
    <div
      id="rest-next-players"
      class="resting-players"
      role="group"
      aria-label="Players resting next round"
    ></div>
    <p class="hint">Tap to plan who sits out the next round. The preview updates as scores come in.</p>
  </div>
```

- [ ] **Step 2: Add JS render + fetch logic**

In `web/static/app.js`, add near `renderRoundBoard`:

```js
async function loadNextRoundPreview(sess) {
  const section = $("#next-up");
  if (!section) return;
  section.classList.toggle("hidden", !isRoundSession(sess));
  if (!isRoundSession(sess)) return;
  const el = $("#next-round-preview");
  try {
    const preview = await api(`/sessions/${sess.id}/next-round-preview`);
    renderNextRoundPreview(sess, preview);
  } catch (err) {
    if (el) el.textContent = "No preview available.";
  }
}

function renderNextRoundPreview(sess, preview) {
  const el = $("#next-round-preview");
  if (!el) return;
  const round = preview?.round;
  if (!round) {
    el.textContent = "No preview available.";
    return;
  }
  const team = (players) => (players || []).map((p) => escapeHtml(p.name)).join(" · ");
  const cards = (round.slots || [])
    .map((slot) => {
      const unused = slot.status === "unused";
      return `<article class="court-card court-card--${escapeHtml(slot.status)} court-card--preview">
        <div class="court-card-head"><strong>${escapeHtml(slot.court_name)}</strong><span class="court-status">${unused ? "Unused" : "Planned"}</span></div>
        ${unused ? '<p class="hint">Not enough active players.</p>' : `<div class="court-teams"><div><span class="team-title">Team Left</span>${team(slot.team_a)}</div><div><span class="team-title">Team Right</span>${team(slot.team_b)}</div></div>`}
      </article>`;
    })
    .join("");
  const restingNames = (preview.resting_player_ids || [])
    .map((id) => (sess.players || []).find((p) => p.id === id)?.name)
    .filter(Boolean);
  const restingHtml = restingNames.length
    ? `<p class="hint">Resting: ${restingNames.map(escapeHtml).join(" · ")}</p>`
    : "";
  const hint = preview.hypothetical
    ? '<p class="hint">Tentative — updates as scores come in.</p>'
    : "";
  el.innerHTML = `<div class="round-board">${cards}${restingHtml}${hint}</div>`;
}

function fillRestNextPlayers(sess) {
  const wrap = document.getElementById("rest-next-players");
  if (!wrap) return;
  const planned = new Set(sess?.rest_next_player_ids || []);
  wrap.innerHTML = "";
  const sorted = [...shuffleActivePlayers(sess?.players || [])].sort((a, b) =>
    a.name.localeCompare(b.name),
  );
  if (!sorted.length) {
    wrap.innerHTML = '<span class="hint">No active players.</span>';
    return;
  }
  for (const p of sorted) {
    const label = document.createElement("label");
    label.className = "resting-player-option";
    const checked = planned.has(p.id) ? " checked" : "";
    label.innerHTML = `<input type="checkbox" class="rest-next-checkbox" data-player-id="${escapeHtml(p.id)}"${checked}><span>${escapeHtml(p.name)}</span>`;
    wrap.appendChild(label);
  }
  for (const box of wrap.querySelectorAll(".rest-next-checkbox")) {
    box.addEventListener("change", async () => {
      const ids = [
        ...wrap.querySelectorAll(".rest-next-checkbox"),
      ]
        .filter((b) => b.checked)
        .map((b) => b.dataset.playerId);
      try {
        // The PUT response is only {rest_next_player_ids}, not a full session —
        // keep using the local sess for id and preview.
        await api(`/sessions/${sess.id}/rest-next`, {
          method: "PUT",
          body: JSON.stringify({ player_ids: ids }),
        });
        sess.rest_next_player_ids = ids;
        void loadNextRoundPreview(sess);
      } catch (err) {
        box.checked = !box.checked;
        alert(err?.message || "Could not update resting players");
      }
    });
  }
}
```

In `refreshSessionView` (:462), after `fillRestingPlayers(sess);` add:

```js
  fillRestNextPlayers(sess);
  void loadNextRoundPreview(sess);
```

- [ ] **Step 3: Add styles**

In `web/static/style.css` append:

```css
.next-up {
  margin-top: 1rem;
}

.court-card--preview {
  opacity: 0.85;
}
```

- [ ] **Step 4: Verify manually**

Run: `go run ./cmd/...` (check `cmd/` for the exact binary; or `go build -o server . && ./server`) with the env from `.env.example`, open the app, create a session with 5+ players and 1-2 courts:
- The "Next up" panel shows the planned lineups; completing a round refreshes it.
- Toggling "Rest next round" updates the preview's resting line immediately.

- [ ] **Step 5: Commit**

```bash
git add web/static/index.html web/static/app.js web/static/style.css
git commit -m "feat: next-round preview panel and rest-next toggles"
```

---

### Task 10: Full verification

- [ ] **Step 1: Run the whole suite**

Run: `go test ./... && go vet ./...`
Expected: all packages PASS, vet clean.

- [ ] **Step 2: Simulate the user's scenario**

Write a quick throwaway check (not committed) or a temporary test exercising 14 players, 2 courts, 5 rounds of americano, asserting the number of never-met pairs is far lower than with the old logic (spot check: after 5 rounds, ≥ 48 of 91 pairs met). Delete the throwaway after recording the result in the PR/commit message context.

- [ ] **Step 3: Final commit if anything is outstanding**

```bash
git status
```
Expected: clean tree.
