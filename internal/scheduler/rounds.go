package scheduler

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"keep-swinging-web/internal/session"
)

var ErrNoRound = errors.New("no synchronized round available")

type roundLineup struct {
	teamA [2]string
	teamB [2]string
}

// PickRound chooses disjoint court lineups as one fairness decision. Courts
// that cannot be filled are returned as explicit unused slots.
func PickRound(players []session.Player, matches []session.RecordedMatch, style session.ShufflingStyle, courts []session.Court, format session.MatchFormat, excludeKey string, excludePlayerIDs []string) (*session.Round, string, error) {
	if len(courts) == 0 || len(courts) > 8 {
		return nil, "", fmt.Errorf("%w: court count must be between 1 and 8", ErrNoRound)
	}
	if style == "" {
		style = session.ShufflingStyleAmericano
	}
	blocked := make(map[string]bool, len(excludePlayerIDs))
	for _, id := range excludePlayerIDs {
		blocked[id] = true
	}
	eligible := activeShufflePlayers(players)
	eligible = filterPlayers(eligible, blocked)
	playersPerCourt := 4
	if format == session.MatchFormatSingles {
		playersPerCourt = 2
	}
	usable := len(eligible) / playersPerCourt
	if usable > len(courts) {
		usable = len(courts)
	}

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

	round := &session.Round{ID: fmt.Sprintf("round-%d", time.Now().UnixNano()), Status: session.RoundStatusOpen, CreatedAt: time.Now().UTC()}
	for i, court := range courts {
		slot := session.RoundSlot{CourtID: court.ID, CourtName: court.Name}
		if i >= usable {
			slot.Status = session.RoundSlotStatusUnused
		} else {
			slot.Status = session.RoundSlotStatusPending
			slot.TeamA = playersForIDs(byID, best[i].teamA[:])
			if format == session.MatchFormatSingles {
				slot.TeamB = playersForIDs(byID, best[i].teamB[:1])
			} else {
				slot.TeamB = playersForIDs(byID, best[i].teamB[:])
			}
			round.Slots = append(round.Slots, slot)
			continue
		}
		round.Slots = append(round.Slots, slot)
	}
	return round, roundKey(best), nil
}

// PickRoundWithRest builds a round that excludes resting players and records
// those ids on the returned round.
func PickRoundWithRest(players []session.Player, matches []session.RecordedMatch, style session.ShufflingStyle, courts []session.Court, format session.MatchFormat, excludeKey string, restingIDs []string) (*session.Round, string, error) {
	round, key, err := PickRound(players, matches, style, courts, format, excludeKey, restingIDs)
	if err != nil {
		return nil, "", err
	}
	round.RestingPlayerIDs = append([]string(nil), restingIDs...)
	return round, key, nil
}

// PickCourtPlayers chooses one court lineup using only active players that are
// neither resting nor already assigned, honoring fairness and partner rotation.
func PickCourtPlayers(players []session.Player, matches []session.RecordedMatch, style session.ShufflingStyle, format session.MatchFormat, restingIDs, assignedIDs []string) ([]session.Player, []session.Player, error) {
	if style == "" {
		style = session.ShufflingStyleAmericano
	}
	blocked := make(map[string]bool, len(restingIDs)+len(assignedIDs))
	for _, id := range restingIDs {
		blocked[id] = true
	}
	for _, id := range assignedIDs {
		blocked[id] = true
	}
	eligible := filterPlayers(activeShufflePlayers(players), blocked)
	perCourt := 4
	if format == session.MatchFormatSingles {
		perCourt = 2
	}
	if len(eligible) < perCourt {
		return nil, nil, ErrNoLineup
	}
	ids := make([]string, len(eligible))
	for i := range eligible {
		ids[i] = eligible[i].ID
	}
	sort.Strings(ids)
	byID := playerByID(players)
	rotation := buildPartnerIndex(matches)
	enc := buildEncounterIndex(matches)

	if isMexicanoStyle(style) {
		ranked := rankPlayersByPoints(ids, playerPoints(matches))
		if format == session.MatchFormatSingles {
			return playersForIDs(byID, []string{ranked[0]}), playersForIDs(byID, []string{ranked[1]}), nil
		}
		lu := mexicanoPairing(style, [4]string{ranked[0], ranked[1], ranked[2], ranked[3]})
		return playersForIDs(byID, lu.TeamA[:]), playersForIDs(byID, lu.TeamB[:]), nil
	}

	var cands []roundLineup
	if format == session.MatchFormatSingles {
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				cands = append(cands, roundLineup{teamA: [2]string{ids[i], ""}, teamB: [2]string{ids[j], ""}})
			}
		}
	} else {
		eachQuartet(ids, func(q [4]string) {
			for _, lu := range lineupsForQuartet(q) {
				if lineupPassesPartnerRotation(Lineup{TeamA: lu.TeamA, TeamB: lu.TeamB}, rotation, ids) {
					cands = append(cands, roundLineup{teamA: lu.TeamA, teamB: lu.TeamB})
				}
			}
		})
		if len(cands) == 0 {
			eachQuartet(ids, func(q [4]string) {
				for _, lu := range lineupsForQuartet(q) {
					cands = append(cands, roundLineup{teamA: lu.TeamA, teamB: lu.TeamB})
				}
			})
		}
	}
	if len(cands) == 0 {
		return nil, nil, ErrNoLineup
	}
	bestIdx := 0
	bestScore := roundScore(cands[:1], byID, style, enc)
	for i := 1; i < len(cands); i++ {
		if s := roundScore(cands[i:i+1], byID, style, enc); s < bestScore {
			bestScore = s
			bestIdx = i
		}
	}
	chosen := cands[bestIdx]
	return playersForIDs(byID, chosen.teamA[:]), playersForIDs(byID, chosen.teamB[:]), nil
}

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

func lineupKeyFor(lu roundLineup) string {
	if lu.teamA[1] == "" {
		a, b := lu.teamA[0], lu.teamB[0]
		if a > b {
			a, b = b, a
		}
		return a + "|" + b
	}
	return LineupKey(lu.teamA, lu.teamB)
}

func roundKey(lineups []roundLineup) string {
	keys := make([]string, len(lineups))
	for i, lu := range lineups {
		keys[i] = lineupKeyFor(lu)
	}
	sort.Strings(keys)
	return strings.Join(keys, ";")
}

func playersForIDs(byID map[string]session.Player, ids []string) []session.Player {
	out := make([]session.Player, 0, len(ids))
	for _, id := range ids {
		if id != "" {
			out = append(out, byID[id])
		}
	}
	return out
}
