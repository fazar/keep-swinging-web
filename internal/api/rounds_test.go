package api

import (
	"strings"
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

func TestValidatePlannedRest(t *testing.T) {
	players := []session.Player{
		{ID: "a", Name: "A"}, {ID: "b", Name: "B"}, {ID: "c", Name: "C"},
		{ID: "d", Name: "D"}, {ID: "e", Name: "E"}, {ID: "f", Name: "F"},
	}
	sess := &session.Session{
		ID: "s", MatchFormat: session.MatchFormatDoubles,
		Players: players,
		Courts:  []session.Court{{ID: "court-1", Name: "Court 1"}},
	}
	if err := validatePlannedRest(sess, []string{"a", "b"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := validatePlannedRest(sess, []string{"zz"}); err == nil {
		t.Fatal("expected unknown id error")
	}
	if err := validatePlannedRest(sess, []string{"a", "a"}); err == nil {
		t.Fatal("expected duplicate id error")
	}
	// 6 active players, 4 needed on court → resting 3 leaves 3 < 4
	if err := validatePlannedRest(sess, []string{"a", "b", "c"}); err == nil {
		t.Fatal("expected too-many-resting error")
	}
}

func TestGenerateNextRoundConsumesPlannedRest(t *testing.T) {
	sess := &session.Session{
		ID:          "s",
		Sport:       session.SportTennis,
		MatchFormat: session.MatchFormatDoubles,
		Players: []session.Player{
			{ID: "a", Name: "A"}, {ID: "b", Name: "B"}, {ID: "c", Name: "C"},
			{ID: "d", Name: "D"}, {ID: "e", Name: "E"}, {ID: "f", Name: "F"},
		},
		Courts:            []session.Court{{ID: "court-1", Name: "Court 1"}},
		RestNextPlayerIDs: []string{"a", "b"},
	}
	if err := generateNextRound(sess); err != nil {
		t.Fatal(err)
	}
	if len(sess.CurrentRound.RestingPlayerIDs) != 2 {
		t.Fatalf("expected planned rest recorded on round: %#v", sess.CurrentRound.RestingPlayerIDs)
	}
	for _, slot := range sess.CurrentRound.Slots {
		for _, p := range append(slot.TeamA, slot.TeamB...) {
			if p.ID == "a" || p.ID == "b" {
				t.Fatalf("planned-resting player assigned: %s", p.ID)
			}
		}
	}
	if len(sess.RestNextPlayerIDs) != 0 {
		t.Fatalf("planned rest should be consumed after generation: %#v", sess.RestNextPlayerIDs)
	}
}

func TestPreviewStateHypothetical(t *testing.T) {
	// No open round → clean state, not hypothetical.
	sess := &session.Session{
		ID:          "s",
		MatchFormat: session.MatchFormatDoubles,
		Players:     []session.Player{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}},
		Courts:      []session.Court{{ID: "court-1", Name: "Court 1"}},
	}
	_, matches, hyp := previewState(sess)
	if hyp || len(matches) != 0 {
		t.Fatalf("expected clean state, hyp=%v matches=%d", hyp, len(matches))
	}
	// Open round with a pending slot → hypothetical, GP bumped, virtual match.
	sess.CurrentRound = &session.Round{
		ID: "r", Status: session.RoundStatusOpen,
		Slots: []session.RoundSlot{{
			CourtID: "court-1", CourtName: "Court 1", Status: session.RoundSlotStatusPending,
			TeamA: []session.Player{{ID: "a"}, {ID: "b"}}, TeamB: []session.Player{{ID: "c"}, {ID: "d"}},
		}},
	}
	players, matches, hyp := previewState(sess)
	if !hyp || len(matches) != 1 {
		t.Fatalf("expected hypothetical state with one virtual match: hyp=%v matches=%d", hyp, len(matches))
	}
	for _, p := range players {
		if p.GamesPlayed != 1 {
			t.Fatalf("pending participants should have GP bumped: %#v", p)
		}
	}
}

func TestBuildPreviewGivesRestedPlayerAGame(t *testing.T) {
	// Mexicano keeps selection deterministic (lowest GP, ties by ID).
	sess := &session.Session{
		ID:             "s",
		Sport:          session.SportTennis,
		MatchFormat:    session.MatchFormatDoubles,
		ShufflingStyle: session.ShufflingStyleMexicano,
		Players: []session.Player{
			{ID: "a", Name: "A"}, {ID: "b", Name: "B"}, {ID: "c", Name: "C"},
			{ID: "d", Name: "D"}, {ID: "e", Name: "E"},
		},
		Courts: []session.Court{{ID: "court-1", Name: "Court 1"}},
	}
	prev, err := buildPreview(sess)
	if err != nil {
		t.Fatal(err)
	}
	if prev.Hypothetical {
		t.Fatal("no open round yet → preview must not be hypothetical")
	}
	// Open the generated round and preview the one after it.
	roundCopy := prev.Round
	sess.CurrentRound = &roundCopy
	prev2, err := buildPreview(sess)
	if err != nil {
		t.Fatal(err)
	}
	if !prev2.Hypothetical {
		t.Fatal("open round → preview must be hypothetical")
	}
	slot := prev2.Round.Slots[0]
	played := map[string]bool{}
	for _, p := range append(slot.TeamA, slot.TeamB...) {
		played[p.ID] = true
	}
	// Pending round-1 slot counts as played: GP a..d = 1, e = 0 → e plays now.
	if !played["e"] {
		t.Fatalf("preview should give the rested player a game: %#v", prev2.Round)
	}
	if len(prev2.RestingPlayerIDs) != 1 || played[prev2.RestingPlayerIDs[0]] {
		t.Fatalf("expected exactly one rested player among a..d: %#v", prev2.RestingPlayerIDs)
	}
}

func TestPreviewFrozenAcrossScoreSaves(t *testing.T) {
	sess := &session.Session{
		ID: "s", Sport: session.SportTennis, MatchFormat: session.MatchFormatDoubles,
		Players: []session.Player{
			{ID: "a", Name: "A"}, {ID: "b", Name: "B"}, {ID: "c", Name: "C"},
			{ID: "d", Name: "D"}, {ID: "e", Name: "E"}, {ID: "f", Name: "F"},
		},
		Courts: []session.Court{{ID: "court-1", Name: "Court 1"}, {ID: "court-2", Name: "Court 2"}},
	}
	if err := generateNextRound(sess); err != nil {
		t.Fatal(err)
	}
	refreshNextRoundPreview(sess)
	if sess.NextRoundPreview == nil {
		t.Fatal("expected preview to be stored")
	}
	frozen := *sess.NextRoundPreview

	// Save a court result — the preview must not move.
	if err := applyRoundCourtScore(sess, sess.CurrentRound.ID, "court-1", 21, 5); err != nil {
		t.Fatal(err)
	}
	if sess.NextRoundPreview == nil || !prevEqual(&frozen, sess.NextRoundPreview) {
		t.Fatalf("score save must not change the preview: before=%+v after=%+v", frozen, sess.NextRoundPreview)
	}

	// Planning rest DOES refresh the preview.
	sess.RestNextPlayerIDs = []string{"a", "b"}
	refreshNextRoundPreview(sess)
	if prevEqual(&frozen, sess.NextRoundPreview) {
		t.Fatal("rest-next change should refresh the preview")
	}
}

func prevEqual(a, b *session.RoundPreview) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Hypothetical != b.Hypothetical {
		return false
	}
	if len(a.Round.Slots) != len(b.Round.Slots) {
		return false
	}
	slotIDs := func(slot session.RoundSlot) string {
		ids := []string{}
		for _, p := range append(append([]session.Player{}, slot.TeamA...), slot.TeamB...) {
			ids = append(ids, p.ID)
		}
		return strings.Join(ids, ",")
	}
	for i := range a.Round.Slots {
		if slotIDs(a.Round.Slots[i]) != slotIDs(b.Round.Slots[i]) {
			return false
		}
	}
	return true
}

func TestRecordedMatchesIncludeOpenRoundResults(t *testing.T) {
	sess := &session.Session{
		ID: "s", MatchFormat: session.MatchFormatDoubles,
		Players: []session.Player{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}},
		Courts:  []session.Court{{ID: "court-1", Name: "Court 1"}},
	}
	scoreA, scoreB := 6, 4
	sess.CurrentRound = &session.Round{
		ID: "r", Status: session.RoundStatusOpen,
		Slots: []session.RoundSlot{{
			CourtID: "court-1", CourtName: "Court 1", Status: session.RoundSlotStatusCompleted,
			TeamA: []session.Player{{ID: "a"}, {ID: "b"}}, TeamB: []session.Player{{ID: "c"}, {ID: "d"}},
			ScoreA: &scoreA, ScoreB: &scoreB,
		}},
	}
	matches := recordedMatches(sess)
	if len(matches) != 1 || len(matches[0].TeamAIDs) != 2 {
		t.Fatalf("open round completed courts must be visible to the scheduler: %#v", matches)
	}
}

func TestPreviewMatchesRoundGeneratedAtCompletion(t *testing.T) {
	sess := &session.Session{
		ID:          "s",
		Sport:       session.SportTennis,
		MatchFormat: session.MatchFormatDoubles,
		Players: []session.Player{
			{ID: "a", Name: "A"}, {ID: "b", Name: "B"}, {ID: "c", Name: "C"},
			{ID: "d", Name: "D"}, {ID: "e", Name: "E"}, {ID: "f", Name: "F"},
		},
		Courts: []session.Court{{ID: "court-1", Name: "Court 1"}},
	}
	if err := generateNextRound(sess); err != nil {
		t.Fatal(err)
	}
	refreshNextRoundPreview(sess)
	preview := *sess.NextRoundPreview

	// Play and complete the current round with real scores.
	scoreA, scoreB := 21, 5
	for i := range sess.CurrentRound.Slots {
		slot := &sess.CurrentRound.Slots[i]
		slot.ScoreA, slot.ScoreB = &scoreA, &scoreB
		slot.Status = session.RoundSlotStatusCompleted
	}
	sess.Rounds = append(sess.Rounds, *sess.CurrentRound)
	rebuildRoundStats(sess)
	sess.CurrentRound = nil
	if err := generateNextRound(sess); err != nil {
		t.Fatal(err)
	}

	slotPair := func(slot session.RoundSlot) string {
		ids := []string{}
		for _, p := range append(append([]session.Player{}, slot.TeamA...), slot.TeamB...) {
			ids = append(ids, p.ID)
		}
		return strings.Join(ids, ",")
	}
	if len(preview.Round.Slots) != len(sess.CurrentRound.Slots) {
		t.Fatalf("slot count mismatch: preview=%d actual=%d", len(preview.Round.Slots), len(sess.CurrentRound.Slots))
	}
	for i := range preview.Round.Slots {
		if slotPair(preview.Round.Slots[i]) != slotPair(sess.CurrentRound.Slots[i]) {
			t.Fatalf("round %d differs from preview:\npreview %s\nactual  %s", i,
				slotPair(preview.Round.Slots[i]), slotPair(sess.CurrentRound.Slots[i]))
		}
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
