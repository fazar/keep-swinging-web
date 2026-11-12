package api

import (
	"strings"
	"testing"

	"keep-swinging-web/internal/scheduler"
	"keep-swinging-web/internal/session"
)

func roundSlotIDs(r *session.Round) string {
	parts := make([]string, 0, len(r.Slots))
	for _, slot := range r.Slots {
		ids := []string{}
		for _, p := range append(append([]session.Player(nil), slot.TeamA...), slot.TeamB...) {
			ids = append(ids, p.ID)
		}
		parts = append(parts, slot.CourtID+":"+strings.Join(ids, ","))
	}
	return strings.Join(parts, ";")
}

func newRoundTestSession() *session.Session {
	return &session.Session{
		ID:          "s",
		Sport:       session.SportTennis,
		MatchFormat: session.MatchFormatDoubles,
		Players: []session.Player{
			{ID: "a", Name: "A"}, {ID: "b", Name: "B"}, {ID: "c", Name: "C"},
			{ID: "d", Name: "D"}, {ID: "e", Name: "E"}, {ID: "f", Name: "F"},
		},
		Courts: []session.Court{{ID: "court-1", Name: "Court 1"}},
	}
}

func TestAddPlayerKeepsCurrentRoundAndJoinsNextPreview(t *testing.T) {
	sess := newRoundTestSession()
	round, key, err := scheduler.PickRound(sess.Players, nil, sess.ShufflingStyle, sess.Courts, sess.MatchFormat, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	sess.CurrentRound = round
	sess.SuggestionKey = key
	before := roundSlotIDs(sess.CurrentRound)

	if _, err := applyAddSessionPlayer(sess, "Gina"); err != nil {
		t.Fatal(err)
	}
	refreshNextRoundPreview(sess)

	if got := roundSlotIDs(sess.CurrentRound); got != before {
		t.Fatalf("current round changed after adding a player: %s -> %s", before, got)
	}
	if sess.CurrentRound.Status != session.RoundStatusOpen {
		t.Fatalf("current round status changed: %v", sess.CurrentRound.Status)
	}
	prev := sess.NextRoundPreview
	if prev == nil {
		t.Fatal("expected refreshed next-round preview")
	}
	found := false
	for _, slot := range prev.Round.Slots {
		for _, p := range append(slot.TeamA, slot.TeamB...) {
			if p.Name == "Gina" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("new player missing from next-round preview")
	}
}

func TestAddPlayerAllowedAfterCompletedCourt(t *testing.T) {
	sess := newRoundTestSession()
	round, key, err := scheduler.PickRound(sess.Players, nil, sess.ShufflingStyle, sess.Courts, sess.MatchFormat, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	sess.CurrentRound = round
	sess.SuggestionKey = key
	score := 11
	sess.CurrentRound.Slots[0].Status = session.RoundSlotStatusCompleted
	sess.CurrentRound.Slots[0].ScoreA = &score
	sess.CurrentRound.Slots[0].ScoreB = &score

	if _, err := applyAddSessionPlayer(sess, "Gina"); err != nil {
		t.Fatalf("adding after a saved court result should not error: %v", err)
	}
	if len(sess.Players) != 7 {
		t.Fatalf("expected 7 players, got %d", len(sess.Players))
	}
	if sess.CurrentRound.Slots[0].Status != session.RoundSlotStatusCompleted {
		t.Fatal("completed court was touched")
	}
}

func TestApplyAddSessionPlayerReviveDuplicateAndCap(t *testing.T) {
	sess := newRoundTestSession()
	if _, err := applyAddSessionPlayer(sess, "Gina"); err != nil {
		t.Fatal(err)
	}
	if len(sess.Players) != 7 {
		t.Fatalf("expected 7 players, got %d", len(sess.Players))
	}

	if _, err := applyAddSessionPlayer(sess, "gina"); err == nil {
		t.Fatal("expected duplicate active name error")
	}

	// Deactivate Gina, then re-adding the same name must revive her.
	for i := range sess.Players {
		if sess.Players[i].Name == "Gina" {
			sess.Players[i].Inactive = true
		}
	}
	revived, err := applyAddSessionPlayer(sess, "Gina")
	if err != nil {
		t.Fatal(err)
	}
	if !revived {
		t.Fatal("expected revive of inactive player")
	}
	for _, p := range sess.Players {
		if p.Name == "Gina" && p.Inactive {
			t.Fatal("player still inactive after revive")
		}
	}

	for i := len(sess.Players); i < 16; i++ {
		if _, err := applyAddSessionPlayer(sess, "Extra"+strings.Repeat("X", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := applyAddSessionPlayer(sess, "Overfill"); err == nil {
		t.Fatal("expected roster cap error")
	}
}
