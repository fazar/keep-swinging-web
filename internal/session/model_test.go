package session

import "testing"

func TestDefaultSitOutScore(t *testing.T) {
	if g := DefaultSitOutScore(SportTennis); g != 2 {
		t.Fatalf("tennis default: got %v want 2", g)
	}
	if g := DefaultSitOutScore(SportPadel); g != 10 {
		t.Fatalf("padel default: got %v want 10", g)
	}
	if g := DefaultSitOutScore(""); g != 2 {
		t.Fatalf("unknown sport: got %v want 2", g)
	}
}

func TestUsesRoundModel(t *testing.T) {
	legacy := &Session{ID: "legacy"}
	if UsesRoundModel(legacy) {
		t.Fatal("session without courts must remain a legacy session")
	}
	configured := &Session{ID: "configured", Courts: []Court{{ID: "court-1", Name: "Court 1"}}}
	if !UsesRoundModel(configured) {
		t.Fatal("session with courts must use the round model")
	}
}

func TestEnsureCourtDefaultsCreatesCourtOneForNewSession(t *testing.T) {
	sess := &Session{ID: "new", MatchFormat: MatchFormatDoubles}
	EnsureCourtDefaults(sess)
	if len(sess.Courts) != 1 {
		t.Fatalf("expected one default court, got %d", len(sess.Courts))
	}
	if sess.Courts[0].ID == "" || sess.Courts[0].Name != "Court 1" {
		t.Fatalf("unexpected default court: %#v", sess.Courts[0])
	}
}

func TestEnsureCourtDefaultsDoesNotOverwriteConfiguredCourts(t *testing.T) {
	sess := &Session{Courts: []Court{{ID: "main", Name: "Center Court"}}}
	EnsureCourtDefaults(sess)
	if len(sess.Courts) != 1 || sess.Courts[0].ID != "main" || sess.Courts[0].Name != "Center Court" {
		t.Fatalf("configured courts were changed: %#v", sess.Courts)
	}
}
