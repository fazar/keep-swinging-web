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
