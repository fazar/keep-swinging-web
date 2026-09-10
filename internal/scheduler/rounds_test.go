package scheduler

import (
	"testing"

	"keep-swinging-web/internal/session"
)

func TestPickRoundDoublesHasDisjointCourts(t *testing.T) {
	players := roundPlayers(8, 0)
	players[0].GamesPlayed = 10
	players[1].GamesPlayed = 10
	round, _, err := PickRound(players, nil, session.ShufflingStyleAmericano, []session.Court{
		{ID: "c1", Name: "Court 1"},
		{ID: "c2", Name: "Court 2"},
	}, session.MatchFormatDoubles, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, slot := range round.Slots {
		if slot.Status != session.RoundSlotStatusPending {
			continue
		}
		if len(slot.TeamA) != 2 || len(slot.TeamB) != 2 {
			t.Fatalf("unexpected doubles slot: %#v", slot)
		}
		for _, p := range append(slot.TeamA, slot.TeamB...) {
			if seen[p.ID] {
				t.Fatalf("player assigned twice: %s", p.ID)
			}
			seen[p.ID] = true
		}
	}
	if len(seen) != 8 {
		t.Fatalf("expected all 8 players assigned, got %d", len(seen))
	}
}

func TestPickRoundSinglesUsesTwoPlayersPerCourt(t *testing.T) {
	round, _, err := PickRound(roundPlayers(6, 0), nil, session.ShufflingStyleAmericano, courts(3), session.MatchFormatSingles, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, slot := range round.Slots {
		if slot.Status != session.RoundSlotStatusPending || len(slot.TeamA) != 1 || len(slot.TeamB) != 1 {
			t.Fatalf("unexpected singles slot: %#v", slot)
		}
	}
}

func TestPickRoundMarksUnfillableCourtsUnused(t *testing.T) {
	round, _, err := PickRound(roundPlayers(6, 0), nil, session.ShufflingStyleAmericano, courts(2), session.MatchFormatDoubles, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if round.Slots[0].Status != session.RoundSlotStatusPending || round.Slots[1].Status != session.RoundSlotStatusUnused {
		t.Fatalf("expected one active and one unused slot: %#v", round.Slots)
	}
}

func TestPickRoundPrefersUnderplayedPlayersAcrossWholeRound(t *testing.T) {
	players := roundPlayers(8, 5)
	players[0].GamesPlayed = 0
	players[1].GamesPlayed = 0
	players[2].GamesPlayed = 0
	players[3].GamesPlayed = 0
	round, _, err := PickRound(players, nil, session.ShufflingStyleAmericano, courts(1), session.MatchFormatDoubles, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range append(round.Slots[0].TeamA, round.Slots[0].TeamB...) {
		seen[p.ID] = true
	}
	for _, id := range []string{"p1", "p2", "p3", "p4"} {
		if !seen[id] {
			t.Fatalf("underplayed player %s was not selected: %#v", id, round)
		}
	}
}

func TestPickRoundReshuffleExcludesWholeRoundKey(t *testing.T) {
	players := roundPlayers(4, 0)
	first, key, err := PickRound(players, nil, session.ShufflingStyleAmericano, courts(1), session.MatchFormatDoubles, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, nextKey, err := PickRound(players, nil, session.ShufflingStyleAmericano, courts(1), session.MatchFormatDoubles, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	if key == nextKey || LineupKey([2]string{first.Slots[0].TeamA[0].ID, first.Slots[0].TeamA[1].ID}, [2]string{first.Slots[0].TeamB[0].ID, first.Slots[0].TeamB[1].ID}) == nextKey {
		t.Fatalf("reshuffle reused whole round: %q", nextKey)
	}
}

func TestPickRoundPreservesDoublesPartnerRotationAcrossCourts(t *testing.T) {
	players := roundPlayers(8, 1)
	matches := []session.RecordedMatch{{TeamAIDs: []string{"p1", "p2"}, TeamBIDs: []string{"p3", "p4"}}}
	round, _, err := PickRound(players, matches, session.ShufflingStyleAmericano, courts(2), session.MatchFormatDoubles, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, slot := range round.Slots {
		if slot.Status != session.RoundSlotStatusPending {
			continue
		}
		if samePair(slot.TeamA, "p1", "p2") || samePair(slot.TeamB, "p1", "p2") || samePair(slot.TeamA, "p3", "p4") || samePair(slot.TeamB, "p3", "p4") {
			t.Fatalf("repeated partner selected: %#v", round)
		}
	}
}

func roundPlayers(n, games int) []session.Player {
	players := make([]session.Player, n)
	for i := range players {
		players[i] = session.Player{ID: "p" + string(rune('1'+i)), Name: "Player", GamesPlayed: games}
	}
	return players
}

func courts(n int) []session.Court {
	out := make([]session.Court, n)
	for i := range out {
		out[i] = session.Court{ID: "c" + string(rune('1'+i)), Name: "Court " + string(rune('1'+i))}
	}
	return out
}

func samePair(team []session.Player, a, b string) bool {
	if len(team) != 2 {
		return false
	}
	return (team[0].ID == a && team[1].ID == b) || (team[0].ID == b && team[1].ID == a)
}
