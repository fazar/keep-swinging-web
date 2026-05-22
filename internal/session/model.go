package session

import "time"

// Sport is tennis or padel for display and filtering only.
type Sport string

const (
	SportTennis Sport = "tennis"
	SportPadel  Sport = "padel"
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
	Players       []Player        `json:"players"`
	Suggested     *SuggestedMatch `json:"suggested,omitempty"`
	Matches       []RecordedMatch `json:"matches"`
	SuggestionKey string          `json:"suggestion_key,omitempty"` // canonical lineup key for reshuffle exclusion
	// SitOutScore is multiplied by (leader GP − your GP) in standings parity.
	// Omitted/absent in storage ⇒ UI uses DefaultSitOutScore(sport).
	SitOutScore *float64 `json:"sit_out_score,omitempty"`
}
