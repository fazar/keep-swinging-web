package api

import (
	"testing"

	"keep-swinging-web/internal/session"
)

func TestLegacySessionMigratesToRound(t *testing.T) {
	sess := &session.Session{
		ID:          "legacy",
		Sport:       session.SportTennis,
		MatchFormat: session.MatchFormatDoubles,
		Players: []session.Player{
			{ID: "a", Name: "A"}, {ID: "b", Name: "B"},
			{ID: "c", Name: "C"}, {ID: "d", Name: "D"},
		},
		Suggested: &session.SuggestedMatch{
			TeamA: []session.Player{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}},
			TeamB: []session.Player{{ID: "c", Name: "C"}, {ID: "d", Name: "D"}},
		},
		Matches: []session.RecordedMatch{{TeamAIDs: []string{"a", "b"}, TeamBIDs: []string{"c", "d"}}},
	}

	if !migrateLegacyRound(sess) {
		t.Fatal("expected legacy session to migrate")
	}
	if len(sess.Courts) != 1 || sess.Courts[0].Name != "Court 1" {
		t.Fatalf("unexpected migrated courts: %#v", sess.Courts)
	}
	if sess.CurrentRound == nil || sess.CurrentRound.Status != session.RoundStatusOpen {
		t.Fatalf("expected open current round: %#v", sess.CurrentRound)
	}
	if len(sess.CurrentRound.Slots) != 1 || sess.CurrentRound.Slots[0].Status != session.RoundSlotStatusPending {
		t.Fatalf("unexpected migrated slots: %#v", sess.CurrentRound.Slots)
	}
	if len(sess.CurrentRound.Slots[0].TeamA) != 2 || len(sess.Matches) != 1 {
		t.Fatalf("migration did not preserve assignments/history: %#v", sess)
	}
}

func TestCompleteRoundGeneratesNext(t *testing.T) {
	sess := &session.Session{
		ID:          "configured",
		Sport:       session.SportTennis,
		MatchFormat: session.MatchFormatDoubles,
		Players: []session.Player{
			{ID: "a", Name: "A"}, {ID: "b", Name: "B"},
			{ID: "c", Name: "C"}, {ID: "d", Name: "D"},
		},
		Courts: []session.Court{{ID: "court-1", Name: "Court 1"}},
	}
	if err := generateNextRound(sess); err != nil {
		t.Fatal(err)
	}
	firstID := sess.CurrentRound.ID
	slot := &sess.CurrentRound.Slots[0]
	scoreA, scoreB := 6, 4
	slot.ScoreA, slot.ScoreB = &scoreA, &scoreB
	slot.Status = session.RoundSlotStatusCompleted
	sess.Rounds = append(sess.Rounds, *sess.CurrentRound)
	sess.CurrentRound = nil
	if err := generateNextRound(sess); err != nil {
		t.Fatal(err)
	}
	if sess.CurrentRound == nil || sess.CurrentRound.Status != session.RoundStatusOpen {
		t.Fatalf("expected next open round: %#v", sess.CurrentRound)
	}
	if sess.CurrentRound.ID == firstID {
		t.Fatal("next round reused completed round id")
	}
}
