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
