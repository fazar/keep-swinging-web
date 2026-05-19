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
	sug, _, err := PickSuggestion(players, nil, "", nil)
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
	_, key1, err := PickSuggestion(players, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, key2, err := PickSuggestion(players, nil, key1, nil)
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
	sug, _, err := PickSuggestion(players, nil, "", []string{"p5"})
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
	sug, _, err := PickSuggestion(players, matches, "", nil)
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
	_, _, err := PickSuggestion(players, matches, "", nil)
	if err != nil {
		t.Fatal(err)
	}
}
