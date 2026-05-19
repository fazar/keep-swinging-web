package session

import "time"

// Sport is tennis or padel for display and filtering only.
type Sport string

const (
	SportTennis Sport = "tennis"
	SportPadel  Sport = "padel"
)

// Player is a participant in a session.
type Player struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
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
	ID            string           `json:"id"`
	Sport         Sport            `json:"sport"`
	Players       []Player         `json:"players"`
	Suggested     *SuggestedMatch  `json:"suggested,omitempty"`
	Matches       []RecordedMatch  `json:"matches"`
	SuggestionKey string           `json:"suggestion_key,omitempty"` // canonical lineup key for reshuffle exclusion
}
