package scheduler

import (
	"testing"

	"keep-swinging-web/internal/session"
)

func TestPickRoundDoublesHasDisjointCourts(t *testing.T) {
	players := roundPlayers(8, 0)
	players[0].GamesPlayed = 10
	players[1].GamesPlayed = 10
	round, _, err := PickRound(players, nil, session.ShufflingStyleAmericano, []session.Court{
		{ID: "c1", Name: "Court 1"},
		{ID: "c2", Name: "Court 2"},
	}, session.MatchFormatDoubles, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, slot := range round.Slots {
		if slot.Status != session.RoundSlotStatusPending {
			continue
		}
		if len(slot.TeamA) != 2 || len(slot.TeamB) != 2 {
			t.Fatalf("unexpected doubles slot: %#v", slot)
		}
		for _, p := range append(slot.TeamA, slot.TeamB...) {
			if seen[p.ID] {
				t.Fatalf("player assigned twice: %s", p.ID)
			}
			seen[p.ID] = true
		}
	}
	if len(seen) != 8 {
		t.Fatalf("expected all 8 players assigned, got %d", len(seen))
	}
}

func TestPickRoundSinglesUsesTwoPlayersPerCourt(t *testing.T) {
	round, _, err := PickRound(roundPlayers(6, 0), nil, session.ShufflingStyleAmericano, courts(3), session.MatchFormatSingles, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, slot := range round.Slots {
		if slot.Status != session.RoundSlotStatusPending || len(slot.TeamA) != 1 || len(slot.TeamB) != 1 {
			t.Fatalf("unexpected singles slot: %#v", slot)
		}
	}
}

func TestPickRoundMarksUnfillableCourtsUnused(t *testing.T) {
	round, _, err := PickRound(roundPlayers(6, 0), nil, session.ShufflingStyleAmericano, courts(2), session.MatchFormatDoubles, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if round.Slots[0].Status != session.RoundSlotStatusPending || round.Slots[1].Status != session.RoundSlotStatusUnused {
		t.Fatalf("expected one active and one unused slot: %#v", round.Slots)
	}
}

func TestPickRoundPrefersUnderplayedPlayersAcrossWholeRound(t *testing.T) {
	players := roundPlayers(8, 5)
	players[0].GamesPlayed = 0
	players[1].GamesPlayed = 0
	players[2].GamesPlayed = 0
	players[3].GamesPlayed = 0
	round, _, err := PickRound(players, nil, session.ShufflingStyleAmericano, courts(1), session.MatchFormatDoubles, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range append(round.Slots[0].TeamA, round.Slots[0].TeamB...) {
		seen[p.ID] = true
	}
	for _, id := range []string{"p1", "p2", "p3", "p4"} {
		if !seen[id] {
			t.Fatalf("underplayed player %s was not selected: %#v", id, round)
		}
	}
}

func TestPickRoundReshuffleExcludesWholeRoundKey(t *testing.T) {
	players := roundPlayers(4, 0)
	first, key, err := PickRound(players, nil, session.ShufflingStyleAmericano, courts(1), session.MatchFormatDoubles, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, nextKey, err := PickRound(players, nil, session.ShufflingStyleAmericano, courts(1), session.MatchFormatDoubles, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	if key == nextKey || LineupKey([2]string{first.Slots[0].TeamA[0].ID, first.Slots[0].TeamA[1].ID}, [2]string{first.Slots[0].TeamB[0].ID, first.Slots[0].TeamB[1].ID}) == nextKey {
		t.Fatalf("reshuffle reused whole round: %q", nextKey)
	}
}

func TestPickRoundPreservesDoublesPartnerRotationAcrossCourts(t *testing.T) {
	players := roundPlayers(8, 1)
	matches := []session.RecordedMatch{{TeamAIDs: []string{"p1", "p2"}, TeamBIDs: []string{"p3", "p4"}}}
	round, _, err := PickRound(players, matches, session.ShufflingStyleAmericano, courts(2), session.MatchFormatDoubles, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, slot := range round.Slots {
		if slot.Status != session.RoundSlotStatusPending {
			continue
		}
		if samePair(slot.TeamA, "p1", "p2") || samePair(slot.TeamB, "p1", "p2") || samePair(slot.TeamA, "p3", "p4") || samePair(slot.TeamB, "p3", "p4") {
			t.Fatalf("repeated partner selected: %#v", round)
		}
	}
}

func TestPickRoundWithRestExcludesRestingPlayers(t *testing.T) {
	players := roundPlayers(8, 0)
	round, _, err := PickRoundWithRest(players, nil, session.ShufflingStyleAmericano, courts(2), session.MatchFormatDoubles, "", []string{"p1", "p2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(round.RestingPlayerIDs) != 2 {
		t.Fatalf("expected resting ids recorded: %#v", round.RestingPlayerIDs)
	}
	if round.Slots[1].Status != session.RoundSlotStatusUnused {
		t.Fatalf("expected second court unused with six eligible: %#v", round.Slots)
	}
	for _, slot := range round.Slots {
		for _, p := range append(append([]session.Player{}, slot.TeamA...), slot.TeamB...) {
			if p.ID == "p1" || p.ID == "p2" {
				t.Fatalf("resting player assigned: %s", p.ID)
			}
		}
	}
}

func TestPickCourtPlayersUsesOnlyUnassigned(t *testing.T) {
	players := roundPlayers(8, 0)
	teamA, teamB, err := PickCourtPlayers(players, nil, session.ShufflingStyleAmericano, session.MatchFormatDoubles, nil, []string{"p1", "p2", "p3", "p4"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range append(append([]session.Player{}, teamA...), teamB...) {
		if p.ID <= "p4" {
			t.Fatalf("assigned player reused: %s", p.ID)
		}
		seen[p.ID] = true
	}
	if len(seen) != 4 {
		t.Fatalf("expected four players, got %d", len(seen))
	}
}

func TestPickCourtPlayersErrorsWhenShort(t *testing.T) {
	players := roundPlayers(6, 0)
	if _, _, err := PickCourtPlayers(players, nil, session.ShufflingStyleAmericano, session.MatchFormatDoubles, nil, []string{"p1", "p2", "p3", "p4"}); err == nil {
		t.Fatal("expected error when fewer than four players remain")
	}
}

func TestLineupEncounterPenaltyWeightsPartnerOverOpponent(t *testing.T) {
	matches := []session.RecordedMatch{
		{TeamAIDs: []string{"p1", "p2"}, TeamBIDs: []string{"p3", "p4"}, ScoreA: 1, ScoreB: 0},
	}
	enc := buildEncounterIndex(matches)
	partnerRepeat := roundLineup{teamA: [2]string{"p1", "p2"}, teamB: [2]string{"p3", "p4"}}
	opponentRepeat := roundLineup{teamA: [2]string{"p1", "p3"}, teamB: [2]string{"p2", "p4"}}
	fresh := roundLineup{teamA: [2]string{"p1", "p5"}, teamB: [2]string{"p6", "p7"}}
	if got := lineupEncounterPenalty(enc, partnerRepeat); got != 2040 {
		t.Fatalf("partner repeat on both sides = 2x1000 + 4 repeated opponent pairs, got %d", got)
	}
	if got := lineupEncounterPenalty(enc, opponentRepeat); got != 20 {
		t.Fatalf("two repeated opponent pairs = 2x10, got %d", got)
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
	left := idsOfSlot(slot.TeamA)
	right := idsOfSlot(slot.TeamB)
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
	same := func(a, b map[string]bool) bool {
		for k := range a {
			if !b[k] {
				return false
			}
		}
		return len(a) == len(b)
	}
	top := map[string]bool{"p1": true, "p2": true}
	bottom := map[string]bool{"p3": true, "p4": true}
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
	_, key, err := PickRound(players, nil, session.ShufflingStyleMexicano, courts(1), session.MatchFormatDoubles, "", nil)
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
}

func TestPickRoundDeterministicForSameState(t *testing.T) {
	players := roundPlayers(8, 0)
	matches := []session.RecordedMatch{{TeamAIDs: []string{"p1", "p2"}, TeamBIDs: []string{"p3", "p4"}}}
	var first string
	for i := 0; i < 20; i++ {
		_, key, err := PickRound(players, matches, session.ShufflingStyleAmericano, courts(2), session.MatchFormatDoubles, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = key
			continue
		}
		if key != first {
			t.Fatalf("same inputs produced different rounds: %q vs %q", first, key)
		}
	}
}

func idsOfSlot(team []session.Player) map[string]bool {
	out := map[string]bool{}
	for _, p := range team {
		out[p.ID] = true
	}
	return out
}

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

func roundPlayers(n, games int) []session.Player {
	players := make([]session.Player, n)
	for i := range players {
		players[i] = session.Player{ID: "p" + string(rune('1'+i)), Name: "Player", GamesPlayed: games}
	}
	return players
}

func courts(n int) []session.Court {
	out := make([]session.Court, n)
	for i := range out {
		out[i] = session.Court{ID: "c" + string(rune('1'+i)), Name: "Court " + string(rune('1'+i))}
	}
	return out
}

func samePair(team []session.Player, a, b string) bool {
	if len(team) != 2 {
		return false
	}
	return (team[0].ID == a && team[1].ID == b) || (team[0].ID == b && team[1].ID == a)
}
