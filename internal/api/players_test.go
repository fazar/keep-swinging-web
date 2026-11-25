package api

import (
	"slices"
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

func roundPlayers(r *session.Round) []string {
	var out []string
	for _, slot := range r.Slots {
		for _, p := range append(append([]session.Player(nil), slot.TeamA...), slot.TeamB...) {
			out = append(out, p.ID)
		}
	}
	return out
}

func completeSlot(t *testing.T, slot *session.RoundSlot) {
	t.Helper()
	score := 11
	slot.Status = session.RoundSlotStatusCompleted
	slot.ScoreA = &score
	slot.ScoreB = &score
}

func TestRemovePlayerReshufflesPendingCourt(t *testing.T) {
	sess := newRoundTestSession()
	round, _, err := scheduler.PickRound(sess.Players, nil, sess.ShufflingStyle, sess.Courts, sess.MatchFormat, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	sess.CurrentRound = round

	victim := round.Slots[0].TeamA[0].ID
	if err := applyRemoveSessionPlayer(sess, victim); err != nil {
		t.Fatal(err)
	}

	slot := sess.CurrentRound.Slots[0]
	if slot.Status != session.RoundSlotStatusPending {
		t.Fatalf("expected re-picked pending court, got %v", slot.Status)
	}
	playing := append(append([]session.Player(nil), slot.TeamA...), slot.TeamB...)
	if len(playing) != 4 {
		t.Fatalf("expected 4 players on re-picked court, got %d", len(playing))
	}
	for _, p := range playing {
		if p.ID == victim {
			t.Fatal("removed player is still paired in the pending court")
		}
		if p.Inactive {
			t.Fatal("inactive player scheduled on a court")
		}
	}
}

func TestRemovePlayerKeepsCompletedCourtAndReplacesPending(t *testing.T) {
	sess := newRoundTestSession()
	sess.Players = append(sess.Players, session.Player{ID: "g", Name: "G"}, session.Player{ID: "h", Name: "H"}, session.Player{ID: "i", Name: "I"})
	sess.Courts = append(sess.Courts, session.Court{ID: "court-2", Name: "Court 2"})
	round, _, err := scheduler.PickRound(sess.Players, nil, sess.ShufflingStyle, sess.Courts, sess.MatchFormat, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	sess.CurrentRound = round
	completeSlot(t, &sess.CurrentRound.Slots[0])
	completedBefore := roundSlotIDs(&session.Round{Slots: []session.RoundSlot{sess.CurrentRound.Slots[0]}})

	victim := sess.CurrentRound.Slots[1].TeamA[0].ID
	if err := applyRemoveSessionPlayer(sess, victim); err != nil {
		t.Fatal(err)
	}

	done := sess.CurrentRound.Slots[0]
	if got := roundSlotIDs(&session.Round{Slots: []session.RoundSlot{done}}); got != completedBefore {
		t.Fatalf("completed court changed: %s -> %s", completedBefore, got)
	}
	if done.Status != session.RoundSlotStatusCompleted || done.ScoreA == nil || done.ScoreB == nil {
		t.Fatal("completed court lost its result")
	}
	slot := sess.CurrentRound.Slots[1]
	playing := append(append([]session.Player(nil), slot.TeamA...), slot.TeamB...)
	for _, p := range playing {
		if p.ID == victim {
			t.Fatal("removed player is still paired in the pending court")
		}
	}
}

func TestRemovePlayerLeavesCourtUnusedWhenPoolTooSmall(t *testing.T) {
	sess := newRoundTestSession()
	sess.Players = append(sess.Players, session.Player{ID: "g", Name: "G"}, session.Player{ID: "h", Name: "H"})
	sess.Courts = append(sess.Courts, session.Court{ID: "court-2", Name: "Court 2"})
	round, _, err := scheduler.PickRound(sess.Players, nil, sess.ShufflingStyle, sess.Courts, sess.MatchFormat, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	sess.CurrentRound = round
	completeSlot(t, &sess.CurrentRound.Slots[0])

	victim := sess.CurrentRound.Slots[1].TeamA[0].ID
	if err := applyRemoveSessionPlayer(sess, victim); err != nil {
		t.Fatal(err)
	}

	if got := sess.CurrentRound.Slots[1].Status; got != session.RoundSlotStatusUnused {
		t.Fatalf("expected court 2 to become unused, got %v", got)
	}
	if ids := roundPlayers(sess.CurrentRound); slices.Contains(ids, victim) {
		t.Fatal("removed player still scheduled")
	}
}

func TestRemoveRestingPlayerKeepsPendingPairings(t *testing.T) {
	sess := newRoundTestSession()
	round, _, err := scheduler.PickRound(sess.Players, nil, sess.ShufflingStyle, sess.Courts, sess.MatchFormat, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	sess.CurrentRound = round
	assigned := map[string]bool{}
	for _, p := range roundPlayers(round) {
		assigned[p] = true
	}
	var resting string
	for _, p := range sess.Players {
		if !assigned[p.ID] {
			resting = p.ID
			break
		}
	}
	sess.CurrentRound.RestingPlayerIDs = []string{resting}
	before := roundSlotIDs(sess.CurrentRound)

	if err := applyRemoveSessionPlayer(sess, resting); err != nil {
		t.Fatal(err)
	}

	if got := roundSlotIDs(sess.CurrentRound); got != before {
		t.Fatalf("pending pairings changed for a resting removal: %s -> %s", before, got)
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
