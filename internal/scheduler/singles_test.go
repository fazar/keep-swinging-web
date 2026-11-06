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
