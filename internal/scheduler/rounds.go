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
	if len(lineups) == 0 && usable > 0 {
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
	var best []roundLineup
	bestScore := int(^uint(0) >> 1)
	var search func(int, int)
	search = func(courtIndex, start int) {
		if courtIndex == usable {
			score := roundScore(chosen, byID, style)
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
	if usable > 0 {
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

func roundScore(lineups []roundLineup, byID map[string]session.Player, style session.ShufflingStyle) int {
	sum := 0
	for _, lu := range lineups {
		sum += byID[lu.teamA[0]].GamesPlayed + byID[lu.teamB[0]].GamesPlayed
		if lu.teamA[1] != "" {
			sum += byID[lu.teamA[1]].GamesPlayed + byID[lu.teamB[1]].GamesPlayed
		}
	}
	if style == session.ShufflingStyleMexicanoTopVsTop {
		return -sum * 1000
	}
	return sum
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
