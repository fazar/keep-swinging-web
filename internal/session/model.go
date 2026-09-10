package session

import "time"

// Sport is tennis or padel for display and filtering only.
type Sport string

const (
	SportTennis Sport = "tennis"
	SportPadel  Sport = "padel"
)

// MatchFormat controls the team size for matchmaking.
type MatchFormat string

const (
	MatchFormatDoubles MatchFormat = "doubles"
	MatchFormatSingles MatchFormat = "singles"
)

// ShufflingStyle controls how players are paired into doubles teams.
type ShufflingStyle string

const (
	ShufflingStyleAmericano           ShufflingStyle = "americano"
	ShufflingStyleMexicano            ShufflingStyle = "mexicano"
	ShufflingStyleMexicanoTopVsTop    ShufflingStyle = "mexicano_top_vs_top"
	ShufflingStyleMexicanoTopVsBottom ShufflingStyle = "mexicano_top_vs_bottom"
)

// DefaultSitOutScore is parity multiplier **k** for new sessions before any PATCH.
func DefaultSitOutScore(sp Sport) float64 {
	switch sp {
	case SportPadel:
		return 10
	case SportTennis:
		return 2
	default:
		return 2
	}
}

// Player is a participant in a session.
type Player struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Inactive    bool   `json:"inactive,omitempty"` // when true: out of reshuffle/next match rotation; counts & history kept
	GamesPlayed int    `json:"games_played"`
	Wins        int    `json:"wins"`
	Losses      int    `json:"losses"`
	Draws       int    `json:"draws"`
}

// SuggestedMatch is the proposed next doubles matchup.
type SuggestedMatch struct {
	TeamA []Player `json:"team_a"`
	TeamB []Player `json:"team_b"`
}

// Court is a named court available to a session.
type Court struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// RoundStatus describes the lifecycle of a synchronized round.
type RoundStatus string

const (
	RoundStatusOpen      RoundStatus = "open"
	RoundStatusCompleted RoundStatus = "completed"
)

// RoundSlotStatus describes whether a court is waiting for or has a result.
type RoundSlotStatus string

const (
	RoundSlotStatusPending   RoundSlotStatus = "pending"
	RoundSlotStatusCompleted RoundSlotStatus = "completed"
	RoundSlotStatusUnused    RoundSlotStatus = "unused"
)

// RoundSlot is one court assignment in a synchronized round.
type RoundSlot struct {
	CourtID   string          `json:"court_id"`
	CourtName string          `json:"court_name"`
	Status    RoundSlotStatus `json:"status"`
	TeamA     []Player        `json:"team_a,omitempty"`
	TeamB     []Player        `json:"team_b,omitempty"`
	ScoreA    *int            `json:"score_a,omitempty"`
	ScoreB    *int            `json:"score_b,omitempty"`
	PlayedAt  *time.Time      `json:"played_at,omitempty"`
}

// Round is a synchronized set of court assignments and their results.
type Round struct {
	ID        string      `json:"id"`
	Status    RoundStatus `json:"status"`
	Slots     []RoundSlot `json:"slots"`
	CreatedAt time.Time   `json:"created_at"`
	ClosedAt  *time.Time  `json:"closed_at,omitempty"`
}

// RecordedMatch is a finished doubles result.
type RecordedMatch struct {
	TeamAIDs []string  `json:"team_a_ids"`
	TeamBIDs []string  `json:"team_b_ids"`
	ScoreA   int       `json:"score_a"`
	ScoreB   int       `json:"score_b"`
	PlayedAt time.Time `json:"played_at"`
}

// Session holds doubles rotation state in Redis.
type Session struct {
	ID            string          `json:"id"`
	Sport         Sport           `json:"sport"`
	MatchFormat   MatchFormat     `json:"match_format"`
	Players       []Player        `json:"players"`
	Suggested     *SuggestedMatch `json:"suggested,omitempty"`
	Matches       []RecordedMatch `json:"matches"`
	Courts        []Court         `json:"courts,omitempty"`
	CurrentRound  *Round          `json:"current_round,omitempty"`
	Rounds        []Round         `json:"rounds,omitempty"`
	SuggestionKey string          `json:"suggestion_key,omitempty"` // canonical lineup key for reshuffle exclusion
	// SitOutScore is multiplied by (leader GP − your GP) in standings parity.
	// Omitted/absent in storage ⇒ UI uses DefaultSitOutScore(sport).
	SitOutScore    *float64       `json:"sit_out_score,omitempty"`
	ShufflingStyle ShufflingStyle `json:"shuffling_style,omitempty"`
	// HideInactiveFromStandings when true omits inactive (away) players from standings tables (default).
	HideInactiveFromStandings *bool `json:"hide_inactive_from_standings,omitempty"`
	// HideInactiveFromMatches when true hides entire finished games that touch an inactive (away) roster member in UI (default).
	HideInactiveFromMatches *bool `json:"hide_inactive_from_matches,omitempty"`
}

// UsesRoundModel distinguishes new court-configured sessions from legacy data.
func UsesRoundModel(s *Session) bool {
	return s != nil && len(s.Courts) > 0
}

// EnsureCourtDefaults initializes a new session with one named court. Callers
// loading legacy sessions should avoid persisting this default unless migrating.
func EnsureCourtDefaults(s *Session) {
	if s == nil || len(s.Courts) > 0 {
		return
	}
	s.Courts = []Court{{ID: "court-1", Name: "Court 1"}}
}

// EnsureHideInactiveDefaults sets hide flags to true when unset (backward compatible stored JSON).
func EnsureHideInactiveDefaults(s *Session) {
	if s == nil {
		return
	}
	if s.HideInactiveFromStandings == nil {
		t := true
		s.HideInactiveFromStandings = &t
	}
	if s.HideInactiveFromMatches == nil {
		t := true
		s.HideInactiveFromMatches = &t
	}
}
