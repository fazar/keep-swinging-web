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
	if e.opponentCount("p1", "p3") != 1 {
		t.Fatalf("p1/p3 were opponents once: %#v", e)
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
