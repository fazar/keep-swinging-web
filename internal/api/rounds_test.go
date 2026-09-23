package api

import (
	"testing"

	"keep-swinging-web/internal/session"
)

func TestValidateRestingIDsRejectsUnknownInactiveAndDuplicate(t *testing.T) {
	players := []session.Player{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}, {ID: "c", Name: "C", Inactive: true}}
	if err := validateRestingIDs(players, []string{"a", "b"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := validateRestingIDs(players, []string{"zz"}); err == nil {
		t.Fatal("expected unknown id error")
	}
	if err := validateRestingIDs(players, []string{"c"}); err == nil {
		t.Fatal("expected inactive id error")
	}
	if err := validateRestingIDs(players, []string{"a", "a"}); err == nil {
		t.Fatal("expected duplicate id error")
	}
}

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

func TestGrowCourtsKeepsStableIDsAndFillsGaps(t *testing.T) {
	existing := []session.Court{{ID: "court-1", Name: "Court 1"}, {ID: "court-3", Name: "Court 3"}}
	next := growCourts(existing, 3)
	if next[0].ID != "court-1" || next[1].ID != "court-3" {
		t.Fatalf("existing ids changed: %#v", next)
	}
	if next[2].ID != "court-2" {
		t.Fatalf("expected first free id court-2, got %s", next[2].ID)
	}
}

func TestScheduleAddedCourtsAddsPairingFromSparePlayers(t *testing.T) {
	sess := &session.Session{
		ID:          "s",
		MatchFormat: session.MatchFormatDoubles,
		Players: []session.Player{
			{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"},
			{ID: "e"}, {ID: "f"}, {ID: "g"}, {ID: "h"},
		},
		Courts: []session.Court{{ID: "court-1", Name: "Court 1"}},
	}
	sess.CurrentRound = &session.Round{
		ID:     "r",
		Status: session.RoundStatusOpen,
		Slots: []session.RoundSlot{{
			CourtID: "court-1", CourtName: "Court 1", Status: session.RoundSlotStatusPending,
			TeamA: []session.Player{{ID: "a"}, {ID: "b"}},
			TeamB: []session.Player{{ID: "c"}, {ID: "d"}},
		}},
	}
	next := []session.Court{{ID: "court-1", Name: "Court 1"}, {ID: "court-2", Name: "Court 2"}}
	if err := scheduleAddedCourts(sess, sess.Courts, next); err != nil {
		t.Fatal(err)
	}
	if len(sess.CurrentRound.Slots) != 2 {
		t.Fatalf("expected two slots, got %d", len(sess.CurrentRound.Slots))
	}
	slot := sess.CurrentRound.Slots[1]
	if len(slot.TeamA) != 2 || len(slot.TeamB) != 2 {
		t.Fatalf("unexpected new slot: %#v", slot)
	}
	for _, p := range append(append([]session.Player{}, slot.TeamA...), slot.TeamB...) {
		if p.ID <= "d" {
			t.Fatalf("reused assigned player: %s", p.ID)
		}
	}
}

func TestScheduleAddedCourtsRejectsWithoutSparePlayers(t *testing.T) {
	sess := &session.Session{
		ID:          "s",
		MatchFormat: session.MatchFormatDoubles,
		Players: []session.Player{
			{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}, {ID: "f"},
		},
		Courts: []session.Court{{ID: "court-1", Name: "Court 1"}},
	}
	sess.CurrentRound = &session.Round{
		ID:     "r",
		Status: session.RoundStatusOpen,
		Slots: []session.RoundSlot{{
			CourtID: "court-1", CourtName: "Court 1", Status: session.RoundSlotStatusPending,
			TeamA: []session.Player{{ID: "a"}, {ID: "b"}},
			TeamB: []session.Player{{ID: "c"}, {ID: "d"}},
		}},
	}
	next := []session.Court{{ID: "court-1", Name: "Court 1"}, {ID: "court-2", Name: "Court 2"}}
	if err := scheduleAddedCourts(sess, sess.Courts, next); err == nil {
		t.Fatal("expected error when fewer than four spare players remain")
	}
}

func TestRemoveSessionCourtDropsSlotAndStats(t *testing.T) {
	players := []session.Player{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
	sess := &session.Session{
		ID:          "s",
		MatchFormat: session.MatchFormatDoubles,
		Players:     players,
		Courts:      []session.Court{{ID: "court-1", Name: "Court 1"}, {ID: "court-2", Name: "Court 2"}},
	}
	scoreA, scoreB := 6, 4
	sess.CurrentRound = &session.Round{
		ID:     "r",
		Status: session.RoundStatusOpen,
		Slots: []session.RoundSlot{
			{
				CourtID: "court-1", CourtName: "Court 1", Status: session.RoundSlotStatusCompleted,
				TeamA: []session.Player{{ID: "a"}, {ID: "b"}}, TeamB: []session.Player{{ID: "c"}, {ID: "d"}},
				ScoreA: &scoreA, ScoreB: &scoreB,
			},
			{CourtID: "court-2", CourtName: "Court 2", Status: session.RoundSlotStatusPending},
		},
	}
	rebuildRoundStats(sess)
	if sess.Players[0].Wins != 1 {
		t.Fatalf("expected seeded win before removal: %#v", sess.Players)
	}
	if err := removeSessionCourt(sess, "court-1"); err != nil {
		t.Fatal(err)
	}
	if len(sess.Courts) != 1 || sess.Courts[0].ID != "court-2" {
		t.Fatalf("unexpected courts after removal: %#v", sess.Courts)
	}
	if len(sess.CurrentRound.Slots) != 1 || sess.CurrentRound.Slots[0].CourtID != "court-2" {
		t.Fatalf("unexpected slots after removal: %#v", sess.CurrentRound.Slots)
	}
	for _, p := range sess.Players {
		if p.GamesPlayed != 0 || p.Wins != 0 {
			t.Fatalf("stats not recalculated for %s: %#v", p.ID, p)
		}
	}
}

func TestRemoveSessionCourtRejectsLastCourt(t *testing.T) {
	sess := &session.Session{
		ID:     "s",
		Courts: []session.Court{{ID: "court-1", Name: "Court 1"}},
	}
	if err := removeSessionCourt(sess, "court-1"); err == nil {
		t.Fatal("expected error removing the final court")
	}
}
