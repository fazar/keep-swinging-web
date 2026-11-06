package scheduler

import (
	"testing"

	"keep-swinging-web/internal/session"
)

func TestLineupKey_symmetric(t *testing.T) {
	k1 := LineupKey([2]string{"a", "b"}, [2]string{"c", "d"})
	k2 := LineupKey([2]string{"d", "c"}, [2]string{"b", "a"})
	if k1 != k2 {
		t.Fatalf("expected equal keys, got %q vs %q", k1, k2)
	}
}

func TestPickSuggestion_prefersUnderplayed(t *testing.T) {
	players := []session.Player{
		{ID: "p1", Name: "A", GamesPlayed: 0},
		{ID: "p2", Name: "B", GamesPlayed: 0},
		{ID: "p3", Name: "C", GamesPlayed: 5},
		{ID: "p4", Name: "D", GamesPlayed: 5},
		{ID: "p5", Name: "E", GamesPlayed: 5},
		{ID: "p6", Name: "F", GamesPlayed: 5},
	}
	sug, _, err := PickSuggestion(players, nil, session.ShufflingStyleAmericano, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, p := range sug.TeamA {
		ids[p.ID] = true
	}
	for _, p := range sug.TeamB {
		ids[p.ID] = true
	}
	if !ids["p1"] || !ids["p2"] {
		t.Fatalf("expected p1 and p2 in suggestion, got %#v", sug)
	}
}

func TestPickSuggestion_reshuffleExcludesKey(t *testing.T) {
	players := []session.Player{
		{ID: "a", Name: "A", GamesPlayed: 0},
		{ID: "b", Name: "B", GamesPlayed: 0},
		{ID: "c", Name: "C", GamesPlayed: 0},
		{ID: "d", Name: "D", GamesPlayed: 0},
	}
	_, key1, err := PickSuggestion(players, nil, session.ShufflingStyleAmericano, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, key2, err := PickSuggestion(players, nil, session.ShufflingStyleAmericano, key1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if key1 == key2 {
		t.Fatalf("reshuffle should change lineup among 4 players (3 splits), got same key %q", key1)
	}
}

func TestPickSuggestion_excludePlayer(t *testing.T) {
	players := []session.Player{
		{ID: "p1", Name: "A", GamesPlayed: 0},
		{ID: "p2", Name: "B", GamesPlayed: 0},
		{ID: "p3", Name: "C", GamesPlayed: 0},
		{ID: "p4", Name: "D", GamesPlayed: 0},
		{ID: "p5", Name: "E", GamesPlayed: 0},
	}
	sug, _, err := PickSuggestion(players, nil, session.ShufflingStyleAmericano, "", []string{"p5"})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, p := range append(sug.TeamA, sug.TeamB...) {
		ids[p.ID] = true
	}
	if ids["p5"] {
		t.Fatal("excluded player must not appear")
	}
}

func TestPickSuggestion_ignoresInactive(t *testing.T) {
	players := []session.Player{
		{ID: "a", Name: "A"},
		{ID: "b", Name: "B"},
		{ID: "c", Name: "C"},
		{ID: "d", Name: "D"},
		{ID: "away", Name: "Away", Inactive: true},
	}
	sug, _, err := PickSuggestion(players, nil, session.ShufflingStyleAmericano, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range append(sug.TeamA, sug.TeamB...) {
		if p.ID == "away" {
			t.Fatal("inactive player must not appear in suggestion")
		}
		seen[p.ID] = true
	}
	if len(seen) != 4 {
		t.Fatalf("expected 4 lineup players, got %d", len(seen))
	}
}

func pairNorm(team []session.Player) string {
	a, b := team[0].ID, team[1].ID
	if a > b {
		a, b = b, a
	}
	return a + "," + b
}

func TestPartnerRotation_blocksRepeatUntilEveryoneElse(t *testing.T) {
	players := []session.Player{
		{ID: "a", Name: "A", GamesPlayed: 1},
		{ID: "b", Name: "B", GamesPlayed: 1},
		{ID: "c", Name: "C", GamesPlayed: 1},
		{ID: "d", Name: "D", GamesPlayed: 1},
	}
	matches := []session.RecordedMatch{
		{TeamAIDs: []string{"a", "b"}, TeamBIDs: []string{"c", "d"}, ScoreA: 1, ScoreB: 0},
	}
	sug, _, err := PickSuggestion(players, matches, session.ShufflingStyleAmericano, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	pairs := []string{pairNorm(sug.TeamA), pairNorm(sug.TeamB)}
	for _, p := range pairs {
		if p == "a,b" || p == "c,d" {
			t.Fatalf("repeat partnership before full rotation: pairs=%v sug=%+v", pairs, sug)
		}
	}
}

func TestPartnerRotation_allowsRepeatAfterFullCycle(t *testing.T) {
	players := []session.Player{
		{ID: "a", Name: "A", GamesPlayed: 3},
		{ID: "b", Name: "B", GamesPlayed: 3},
		{ID: "c", Name: "C", GamesPlayed: 3},
		{ID: "d", Name: "D", GamesPlayed: 3},
	}
	matches := []session.RecordedMatch{
		{TeamAIDs: []string{"a", "b"}, TeamBIDs: []string{"c", "d"}},
		{TeamAIDs: []string{"a", "c"}, TeamBIDs: []string{"b", "d"}},
		{TeamAIDs: []string{"a", "d"}, TeamBIDs: []string{"b", "c"}},
	}
	_, _, err := PickSuggestion(players, matches, session.ShufflingStyleAmericano, "", nil)
	if err != nil {
		t.Fatal(err)
	}
}

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

func TestPickSuggestion_americanoAvoidsImmediateRematchPartners(t *testing.T) {
	players := []session.Player{
		{ID: "p1", Name: "A"}, {ID: "p2", Name: "B"}, {ID: "p3", Name: "C"}, {ID: "p4", Name: "D"},
		{ID: "p5", Name: "E"}, {ID: "p6", Name: "F"},
	}
	matches := []session.RecordedMatch{
		{TeamAIDs: []string{"p1", "p2"}, TeamBIDs: []string{"p3", "p4"}, ScoreA: 10, ScoreB: 10},
	}
	sug, _, err := PickSuggestion(players, matches, session.ShufflingStyleAmericano, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	// With six players there are fresh quartets (e.g. involving p5/p6); the
	// suggestion must not reuse the exact previous matchup.
	old := LineupKey([2]string{"p1", "p2"}, [2]string{"p3", "p4"})
	if LineupKey([2]string{sug.TeamA[0].ID, sug.TeamA[1].ID}, [2]string{sug.TeamB[0].ID, sug.TeamB[1].ID}) == old {
		t.Fatalf("americano should not repeat the previous matchup when fresh players exist: %#v", sug)
	}
}
