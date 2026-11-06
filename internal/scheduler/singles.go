package scheduler

import (
	"keep-swinging-web/internal/session"
)

// ErrNoSinglesLineup is returned when fewer than two eligible players exist.
var ErrNoSinglesLineup = ErrNoLineup

// SinglesLineup is one player per team (1 vs 1).
type SinglesLineup struct {
	PlayerA string
	PlayerB string
}

// SinglesLineupKey returns a canonical key for comparing singles matchups.
func SinglesLineupKey(a, b string) string {
	if a < b {
		return a + "|" + b
	}
	return b + "|" + a
}

// singlesFairnessScore is the sum of games_played for the two players — lower means they are more "due".
func singlesFairnessScore(games map[string]int, lu SinglesLineup) int {
	return games[lu.PlayerA] + games[lu.PlayerB]
}

// PickSuggestionSingles chooses a singles matchup (1 vs 1):
// - americano: minimize sum(games_played) — fairness focus.
// - mexicano: prefer the two most-experienced players facing each other with max GP difference.
func PickSuggestionSingles(players []session.Player, matches []session.RecordedMatch, style session.ShufflingStyle, excludeKey string, excludePlayerIDs []string) (*session.SuggestedMatch, string, error) {
	if style == "" {
		style = session.ShufflingStyleAmericano
	}
	exSet := make(map[string]bool)
	for _, id := range excludePlayerIDs {
		exSet[id] = true
	}
	activePool := activeShufflePlayers(players)
	eligible := filterPlayers(activePool, exSet)
	if len(eligible) < 2 {
		return nil, "", ErrNoSinglesLineup
	}
	ids := make([]string, len(eligible))
	for i := range eligible {
		ids[i] = eligible[i].ID
	}

	games := gamesMap(players)
	byID := playerByID(players)

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

	var candidates []SinglesLineup
	for i := 0; i < len(eligible); i++ {
		for j := i + 1; j < len(eligible); j++ {
			a, b := ids[i], ids[j]
			key := SinglesLineupKey(a, b)
			if excludeKey != "" && key == excludeKey {
				continue
			}
			candidates = append(candidates, SinglesLineup{PlayerA: a, PlayerB: b})
		}
	}

	if len(candidates) == 0 {
		return nil, "", ErrNoSinglesLineup
	}

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

	chosen := best[pickIndex(len(best))]
	key := SinglesLineupKey(chosen.PlayerA, chosen.PlayerB)

	sug := &session.SuggestedMatch{
		TeamA: []session.Player{byID[chosen.PlayerA]},
		TeamB: []session.Player{byID[chosen.PlayerB]},
	}
	return sug, key, nil
}
