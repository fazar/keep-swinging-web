package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"keep-swinging-web/internal/redisstore"
	"keep-swinging-web/internal/scheduler"
	"keep-swinging-web/internal/session"
)

// Server exposes REST handlers backed by Redis.
type Server struct {
	Store  *redisstore.Store
	Logger *slog.Logger
}

// Mount registers API routes on mux (Go 1.22+ patterns).
func Mount(mux *http.ServeMux, s *Server) {
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("POST /api/sessions", s.handleCreateSession)
	mux.HandleFunc("GET /api/sessions/{id}", s.handleGetSession)
	mux.HandleFunc("POST /api/sessions/{id}/matches", s.handleRecordMatch)
	mux.HandleFunc("DELETE /api/sessions/{id}/matches/{match_index}", s.handleDeleteMatch)
	mux.HandleFunc("PATCH /api/sessions/{id}/matches/{match_index}", s.handlePatchMatchScores)
	mux.HandleFunc("POST /api/sessions/{id}/reshuffle", s.handleReshuffle)
	mux.HandleFunc("POST /api/sessions/{id}/rounds", s.handleCreateRound)
	mux.HandleFunc("PATCH /api/sessions/{id}/rounds/{round_id}/courts/{court_id}", s.handlePatchRoundCourt)
	mux.HandleFunc("DELETE /api/sessions/{id}/rounds/{round_id}/courts/{court_id}", s.handleDeleteRoundCourt)
	mux.HandleFunc("POST /api/sessions/{id}/rounds/{round_id}/complete", s.handleCompleteRound)
	mux.HandleFunc("POST /api/sessions/{id}/reset", s.handleResetScores)
	mux.HandleFunc("PATCH /api/sessions/{id}/config", s.handlePatchSessionConfig)
	mux.HandleFunc("POST /api/sessions/{id}/players", s.handleAddSessionPlayer)
	mux.HandleFunc("DELETE /api/sessions/{id}/players/{player_id}", s.handleRemoveSessionPlayer)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.Store.Ping(ctx); err != nil {
		s.Logger.Error("health ping", "err", err)
		writeError(w, http.StatusServiceUnavailable, "storage unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Sport          string   `json:"sport"`
		Players        []string `json:"players"`
		MatchFormat    string   `json:"match_format,omitempty"`
		ShufflingStyle string   `json:"shuffling_style,omitempty"`
		CourtCount     *int     `json:"court_count,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	sp, ok := parseSport(req.Sport)
	if !ok {
		writeError(w, http.StatusBadRequest, "sport must be tennis or padel")
		return
	}

	var mf session.MatchFormat
	switch req.MatchFormat {
	case "singles":
		mf = session.MatchFormatSingles
	case "doubles", "":
		mf = session.MatchFormatDoubles
	default:
		writeError(w, http.StatusBadRequest, "match_format must be doubles or singles")
		return
	}

	names := normalizeNames(req.Players)
	if mf == session.MatchFormatSingles && (len(names) < 2 || len(names) > 16) {
		writeError(w, http.StatusBadRequest, "need between 2 and 16 players for singles")
		return
	}
	if mf == session.MatchFormatDoubles && (len(names) < 4 || len(names) > 16) {
		writeError(w, http.StatusBadRequest, "need between 4 and 16 players for doubles")
		return
	}
	players := make([]session.Player, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		id := newPlayerID()
		for seen[id] {
			id = newPlayerID()
		}
		seen[id] = true
		players = append(players, session.Player{ID: id, Name: name})
	}
	sess := &session.Session{
		ID:          newSessionID(),
		Sport:       sp,
		MatchFormat: mf,
		Players:     players,
		Matches:     []session.RecordedMatch{},
	}
	if req.CourtCount != nil {
		if err := validateCourtCount(*req.CourtCount); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		sess.Courts = makeCourts(*req.CourtCount)
	}
	if req.ShufflingStyle != "" {
		sess.ShufflingStyle = session.ShufflingStyle(req.ShufflingStyle)
	}
	k0 := session.DefaultSitOutScore(sp)
	sess.SitOutScore = &k0

	session.EnsureHideInactiveDefaults(sess)

	var sug *session.SuggestedMatch
	var key string
	var errPick error
	if session.UsesRoundModel(sess) {
		var round *session.Round
		round, key, errPick = scheduler.PickRound(sess.Players, sess.Matches, sess.ShufflingStyle, sess.Courts, sess.MatchFormat, "", nil)
		sess.CurrentRound = round
	} else if sess.MatchFormat == session.MatchFormatSingles {
		sug, key, errPick = scheduler.PickSuggestionSingles(sess.Players, sess.Matches, sess.ShufflingStyle, "", nil)
	} else {
		sug, key, errPick = scheduler.PickSuggestion(sess.Players, sess.Matches, sess.ShufflingStyle, "", nil)
	}
	if errPick != nil {
		s.Logger.Error("pick suggestion", "err", errPick)
		writeError(w, http.StatusInternalServerError, "could not build lineup")
		return
	}
	sess.Suggested = sug
	sess.SuggestionKey = key
	ctx := r.Context()
	if err := s.Store.Save(ctx, sess); err != nil {
		s.Logger.Error("save session", "err", err)
		writeError(w, http.StatusInternalServerError, "could not save session")
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}
	sess, err := s.Store.Get(r.Context(), id)
	if errors.Is(err, redisstore.ErrNotFound) {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		s.Logger.Error("get session", "err", err)
		writeError(w, http.StatusInternalServerError, "could not load session")
		return
	}
	migrateLegacyRound(sess)
	writeJSON(w, http.StatusOK, sess)
}

type recordMatchBody struct {
	TeamAIDs []string `json:"team_a_ids"`
	TeamBIDs []string `json:"team_b_ids"`
	ScoreA   int      `json:"score_a"`
	ScoreB   int      `json:"score_b"`
}

func (s *Server) handleRecordMatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body recordMatchBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	ctx := r.Context()
	sess, err := s.loadSession(ctx, id, w)
	if sess == nil {
		return
	}
	if err != nil {
		return
	}
	if session.UsesRoundModel(sess) {
		writeError(w, http.StatusBadRequest, "round sessions use court result endpoints")
		return
	}
	if sess.MatchFormat == session.MatchFormatSingles {
		if len(body.TeamAIDs) != 1 || len(body.TeamBIDs) != 1 {
			writeError(w, http.StatusBadRequest, "team_a_ids and team_b_ids must each have 1 player id for singles")
			return
		}
	} else {
		if len(body.TeamAIDs) != 2 || len(body.TeamBIDs) != 2 {
			writeError(w, http.StatusBadRequest, "team_a_ids and team_b_ids must each have 2 player ids")
			return
		}
	}
	if body.ScoreA < 0 || body.ScoreB < 0 {
		writeError(w, http.StatusBadRequest, "scores must be non-negative")
		return
	}
	allIDs := append(append([]string{}, body.TeamAIDs...), body.TeamBIDs...)
	if sess.MatchFormat == session.MatchFormatSingles {
		if len(allIDs) != 2 || allIDs[0] == allIDs[1] {
			writeError(w, http.StatusBadRequest, "need two distinct player ids")
			return
		}
	} else {
		if !distinctFour(allIDs) {
			writeError(w, http.StatusBadRequest, "need four distinct player ids")
			return
		}
	}
	idx := playerIndex(sess)
	for _, pid := range allIDs {
		p, ok := idx[pid]
		if !ok {
			writeError(w, http.StatusBadRequest, "unknown player id")
			return
		}
		if p.Inactive {
			writeError(w, http.StatusBadRequest, "player is inactive (away from shuffle): re-add by name first")
			return
		}
	}
	rec := session.RecordedMatch{
		TeamAIDs: append([]string(nil), body.TeamAIDs...),
		TeamBIDs: append([]string(nil), body.TeamBIDs...),
		ScoreA:   body.ScoreA,
		ScoreB:   body.ScoreB,
		PlayedAt: time.Now().UTC(),
	}
	applyMatchResult(sess, rec)
	sess.Matches = append(sess.Matches, rec)

	if session.UsesRoundModel(sess) {
		if sess.CurrentRound == nil || sess.CurrentRound.Status != session.RoundStatusOpen {
			writeError(w, http.StatusBadRequest, "no open round to update")
			return
		}
		for _, slot := range sess.CurrentRound.Slots {
			if slot.Status == session.RoundSlotStatusCompleted {
				writeError(w, http.StatusBadRequest, "cannot add a player after saving a court result")
				return
			}
		}
		round, key, pickErr := scheduler.PickRound(sess.Players, recordedMatches(sess), sess.ShufflingStyle, sess.Courts, sess.MatchFormat, "", nil)
		if pickErr != nil {
			writeError(w, http.StatusBadRequest, "could not refresh round")
			return
		}
		sess.CurrentRound, sess.SuggestionKey = round, key
	} else {
		var sug *session.SuggestedMatch
		var key string
		var errPick error
		if sess.MatchFormat == session.MatchFormatSingles {
			sug, key, errPick = scheduler.PickSuggestionSingles(sess.Players, sess.Matches, sess.ShufflingStyle, "", nil)
		} else {
			sug, key, errPick = scheduler.PickSuggestion(sess.Players, sess.Matches, sess.ShufflingStyle, "", nil)
		}
		if errPick != nil {
			s.Logger.Error("pick suggestion after match", "err", errPick)
			writeError(w, http.StatusInternalServerError, "could not refresh lineup")
			return
		}
		sess.Suggested = sug
		sess.SuggestionKey = key
	}

	if err := s.Store.Save(ctx, sess); err != nil {
		s.Logger.Error("save session", "err", err)
		writeError(w, http.StatusInternalServerError, "could not save session")
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleDeleteMatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.PathValue("id")
	idxStr := strings.TrimSpace(r.PathValue("match_index"))
	idx, errConv := strconv.Atoi(idxStr)
	if errConv != nil || idx < 0 {
		writeError(w, http.StatusBadRequest, "invalid match index")
		return
	}

	ctx := r.Context()
	sess, err := s.loadSession(ctx, id, w)
	if sess == nil {
		return
	}
	if err != nil {
		return
	}
	if idx >= len(sess.Matches) {
		writeError(w, http.StatusBadRequest, "match index out of range")
		return
	}

	rec := sess.Matches[idx]
	revertMatchResult(sess, rec)
	sess.Matches = slices.Delete(sess.Matches, idx, idx+1)

	var sug *session.SuggestedMatch
	var key string
	var errPick error
	if sess.MatchFormat == session.MatchFormatSingles {
		sug, key, errPick = scheduler.PickSuggestionSingles(sess.Players, sess.Matches, sess.ShufflingStyle, "", nil)
	} else {
		sug, key, errPick = scheduler.PickSuggestion(sess.Players, sess.Matches, sess.ShufflingStyle, "", nil)
	}
	if errPick != nil {
		s.Logger.Error("pick suggestion after delete match", "err", errPick)
		writeError(w, http.StatusInternalServerError, "could not refresh lineup")
		return
	}
	sess.Suggested = sug
	sess.SuggestionKey = key

	if err := s.Store.Save(ctx, sess); err != nil {
		s.Logger.Error("save session", "err", err)
		writeError(w, http.StatusInternalServerError, "could not save session")
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

type patchMatchScoresBody struct {
	ScoreA int `json:"score_a"`
	ScoreB int `json:"score_b"`
}

func (s *Server) handlePatchMatchScores(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.PathValue("id")
	idxStr := strings.TrimSpace(r.PathValue("match_index"))
	idx, errConv := strconv.Atoi(idxStr)
	if errConv != nil || idx < 0 {
		writeError(w, http.StatusBadRequest, "invalid match index")
		return
	}

	var body patchMatchScoresBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.ScoreA < 0 || body.ScoreB < 0 {
		writeError(w, http.StatusBadRequest, "scores must be non-negative")
		return
	}

	ctx := r.Context()
	sess, err := s.loadSession(ctx, id, w)
	if sess == nil {
		return
	}
	if err != nil {
		return
	}
	if idx >= len(sess.Matches) {
		writeError(w, http.StatusBadRequest, "match index out of range")
		return
	}

	rec := sess.Matches[idx]
	revertMatchResult(sess, rec)
	upd := rec
	upd.ScoreA = body.ScoreA
	upd.ScoreB = body.ScoreB
	applyMatchResult(sess, upd)
	sess.Matches[idx] = upd

	var sug *session.SuggestedMatch
	var key string
	var errPick error
	if sess.MatchFormat == session.MatchFormatSingles {
		sug, key, errPick = scheduler.PickSuggestionSingles(sess.Players, sess.Matches, sess.ShufflingStyle, "", nil)
	} else {
		sug, key, errPick = scheduler.PickSuggestion(sess.Players, sess.Matches, sess.ShufflingStyle, "", nil)
	}
	if errPick != nil {
		s.Logger.Error("pick suggestion after patch match scores", "err", errPick)
		writeError(w, http.StatusInternalServerError, "could not refresh lineup")
		return
	}
	sess.Suggested = sug
	sess.SuggestionKey = key

	if err := s.Store.Save(ctx, sess); err != nil {
		s.Logger.Error("save session", "err", err)
		writeError(w, http.StatusInternalServerError, "could not save session")
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

type reshuffleBody struct {
	ExcludePlayerIDs []string `json:"exclude_player_ids"`
}

func (s *Server) handleReshuffle(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body reshuffleBody
	_ = json.NewDecoder(r.Body).Decode(&body) // optional body

	ctx := r.Context()
	sess, err := s.loadSession(ctx, id, w)
	if sess == nil {
		return
	}
	if err != nil {
		return
	}
	if session.UsesRoundModel(sess) {
		if sess.CurrentRound == nil || sess.CurrentRound.Status != session.RoundStatusOpen {
			writeError(w, http.StatusBadRequest, "no open round to reshuffle")
			return
		}
		for _, slot := range sess.CurrentRound.Slots {
			if slot.Status == session.RoundSlotStatusCompleted {
				writeError(w, http.StatusBadRequest, "cannot reshuffle after saving a court result")
				return
			}
		}
		round, key, pickErr := scheduler.PickRound(sess.Players, recordedMatches(sess), sess.ShufflingStyle, sess.Courts, sess.MatchFormat, sess.SuggestionKey, body.ExcludePlayerIDs)
		if pickErr != nil {
			writeError(w, http.StatusBadRequest, "no alternate round available")
			return
		}
		sess.CurrentRound, sess.SuggestionKey = round, key
		if err := s.Store.Save(ctx, sess); err != nil {
			writeError(w, http.StatusInternalServerError, "could not save session")
			return
		}
		writeJSON(w, http.StatusOK, sess)
		return
	}

	var sug *session.SuggestedMatch
	var key string
	var errPick error
	if sess.MatchFormat == session.MatchFormatSingles {
		sug, key, errPick = scheduler.PickSuggestionSingles(sess.Players, sess.Matches, sess.ShufflingStyle, sess.SuggestionKey, body.ExcludePlayerIDs)
	} else {
		sug, key, errPick = scheduler.PickSuggestion(sess.Players, sess.Matches, sess.ShufflingStyle, sess.SuggestionKey, body.ExcludePlayerIDs)
	}
	if errPick != nil {
		if errors.Is(err, scheduler.ErrNoLineup) {
			writeError(w, http.StatusBadRequest, "no alternate lineup (try fewer exclusions or add players)")
			return
		}
		s.Logger.Error("reshuffle", "err", err)
		writeError(w, http.StatusInternalServerError, "could not reshuffle")
		return
	}
	sess.Suggested = sug
	sess.SuggestionKey = key
	if err := s.Store.Save(ctx, sess); err != nil {
		s.Logger.Error("save session", "err", err)
		writeError(w, http.StatusInternalServerError, "could not save session")
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

type roundCourtScore struct {
	CourtID string `json:"court_id"`
	ScoreA  int    `json:"score_a"`
	ScoreB  int    `json:"score_b"`
}

type completeRoundBody struct {
	Courts []roundCourtScore `json:"courts"`
}

func (s *Server) handleCreateRound(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sess, err := s.loadSession(ctx, r.PathValue("id"), w)
	if sess == nil || err != nil {
		return
	}
	if !session.UsesRoundModel(sess) {
		writeError(w, http.StatusBadRequest, "legacy sessions use the match endpoint")
		return
	}
	if sess.CurrentRound != nil && sess.CurrentRound.Status == session.RoundStatusOpen {
		writeError(w, http.StatusBadRequest, "current round is still open")
		return
	}
	round, key, pickErr := scheduler.PickRound(sess.Players, recordedMatches(sess), sess.ShufflingStyle, sess.Courts, sess.MatchFormat, "", nil)
	if pickErr != nil {
		writeError(w, http.StatusBadRequest, pickErr.Error())
		return
	}
	sess.CurrentRound = round
	sess.SuggestionKey = key
	if err := s.Store.Save(ctx, sess); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save session")
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}

func (s *Server) handlePatchRoundCourt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sess, err := s.loadSession(ctx, r.PathValue("id"), w)
	if sess == nil || err != nil {
		return
	}
	if !session.UsesRoundModel(sess) {
		writeError(w, http.StatusBadRequest, "legacy sessions use the match endpoint")
		return
	}
	var body roundCourtScore
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := applyRoundCourtScore(sess, r.PathValue("round_id"), r.PathValue("court_id"), body.ScoreA, body.ScoreB); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.Save(ctx, sess); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save session")
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleDeleteRoundCourt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sess, err := s.loadSession(ctx, r.PathValue("id"), w)
	if sess == nil || err != nil {
		return
	}
	if sess.CurrentRound == nil || sess.CurrentRound.ID != r.PathValue("round_id") {
		writeError(w, http.StatusBadRequest, "round is not open")
		return
	}
	for i := range sess.CurrentRound.Slots {
		slot := &sess.CurrentRound.Slots[i]
		if slot.CourtID != r.PathValue("court_id") {
			continue
		}
		if slot.Status == session.RoundSlotStatusUnused {
			writeError(w, http.StatusBadRequest, "unused court cannot have a result")
			return
		}
		slot.Status = session.RoundSlotStatusPending
		slot.ScoreA, slot.ScoreB, slot.PlayedAt = nil, nil, nil
		rebuildRoundStats(sess)
		if err := s.Store.Save(ctx, sess); err != nil {
			writeError(w, http.StatusInternalServerError, "could not save session")
			return
		}
		writeJSON(w, http.StatusOK, sess)
		return
	}
	writeError(w, http.StatusBadRequest, "unknown court")
}

func (s *Server) handleCompleteRound(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sess, err := s.loadSession(ctx, r.PathValue("id"), w)
	if sess == nil || err != nil {
		return
	}
	if sess.CurrentRound == nil || sess.CurrentRound.ID != r.PathValue("round_id") || sess.CurrentRound.Status != session.RoundStatusOpen {
		writeError(w, http.StatusBadRequest, "round is not open")
		return
	}
	var body completeRoundBody
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	for _, result := range body.Courts {
		if err := applyRoundCourtScore(sess, sess.CurrentRound.ID, result.CourtID, result.ScoreA, result.ScoreB); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	for _, slot := range sess.CurrentRound.Slots {
		if slot.Status == session.RoundSlotStatusPending {
			writeError(w, http.StatusBadRequest, "every scheduled court needs a result")
			return
		}
	}
	now := time.Now().UTC()
	sess.CurrentRound.Status = session.RoundStatusCompleted
	sess.CurrentRound.ClosedAt = &now
	sess.Rounds = append(sess.Rounds, *sess.CurrentRound)
	rebuildRoundStats(sess)
	sess.CurrentRound = nil
	if err := generateNextRound(sess); err != nil {
		writeError(w, http.StatusInternalServerError, "could not build next round")
		return
	}
	if err := s.Store.Save(ctx, sess); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save session")
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func migrateLegacyRound(sess *session.Session) bool {
	if sess == nil || session.UsesRoundModel(sess) || sess.Suggested == nil {
		return false
	}
	sess.Courts = []session.Court{{ID: "court-1", Name: "Court 1"}}
	sess.CurrentRound = &session.Round{
		ID:        fmt.Sprintf("round-%d", time.Now().UnixNano()),
		Status:    session.RoundStatusOpen,
		CreatedAt: time.Now().UTC(),
		Slots: []session.RoundSlot{{
			CourtID:   "court-1",
			CourtName: "Court 1",
			Status:    session.RoundSlotStatusPending,
			TeamA:     append([]session.Player(nil), sess.Suggested.TeamA...),
			TeamB:     append([]session.Player(nil), sess.Suggested.TeamB...),
		}},
	}
	return true
}

func generateNextRound(sess *session.Session) error {
	if sess == nil || !session.UsesRoundModel(sess) {
		return errors.New("session is not round-based")
	}
	round, key, err := scheduler.PickRound(sess.Players, recordedMatches(sess), sess.ShufflingStyle, sess.Courts, sess.MatchFormat, "", nil)
	if err != nil {
		return err
	}
	sess.CurrentRound = round
	sess.SuggestionKey = key
	return nil
}

func applyRoundCourtScore(sess *session.Session, roundID, courtID string, scoreA, scoreB int) error {
	if scoreA < 0 || scoreB < 0 {
		return errors.New("scores must be non-negative")
	}
	if sess.CurrentRound == nil || sess.CurrentRound.ID != roundID || sess.CurrentRound.Status != session.RoundStatusOpen {
		return errors.New("round is not open")
	}
	for i := range sess.CurrentRound.Slots {
		slot := &sess.CurrentRound.Slots[i]
		if slot.CourtID != courtID {
			continue
		}
		if slot.Status == session.RoundSlotStatusUnused {
			return errors.New("unused court cannot have a result")
		}
		played := time.Now().UTC()
		slot.ScoreA, slot.ScoreB, slot.PlayedAt = &scoreA, &scoreB, &played
		slot.Status = session.RoundSlotStatusCompleted
		rebuildRoundStats(sess)
		return nil
	}
	return errors.New("unknown court")
}

func recordedMatches(sess *session.Session) []session.RecordedMatch {
	matches := append([]session.RecordedMatch(nil), sess.Matches...)
	for _, round := range sess.Rounds {
		matches = append(matches, roundRecordedMatches(round)...)
	}
	return matches
}

func roundRecordedMatches(round session.Round) []session.RecordedMatch {
	var matches []session.RecordedMatch
	for _, slot := range round.Slots {
		if slot.Status != session.RoundSlotStatusCompleted || slot.ScoreA == nil || slot.ScoreB == nil {
			continue
		}
		ids := func(players []session.Player) []string {
			out := make([]string, len(players))
			for i, p := range players {
				out[i] = p.ID
			}
			return out
		}
		playedAt := time.Now().UTC()
		if slot.PlayedAt != nil {
			playedAt = *slot.PlayedAt
		}
		matches = append(matches, session.RecordedMatch{TeamAIDs: ids(slot.TeamA), TeamBIDs: ids(slot.TeamB), ScoreA: *slot.ScoreA, ScoreB: *slot.ScoreB, PlayedAt: playedAt})
	}
	return matches
}

func rebuildRoundStats(sess *session.Session) {
	for i := range sess.Players {
		sess.Players[i].GamesPlayed = 0
		sess.Players[i].Wins = 0
		sess.Players[i].Losses = 0
		sess.Players[i].Draws = 0
	}
	for _, match := range sess.Matches {
		applyMatchResult(sess, match)
	}
	for _, round := range sess.Rounds {
		for _, match := range roundRecordedMatches(round) {
			applyMatchResult(sess, match)
		}
	}
	if sess.CurrentRound != nil {
		for _, match := range roundRecordedMatches(*sess.CurrentRound) {
			applyMatchResult(sess, match)
		}
	}
}

type patchSessionConfigBody struct {
	SitOutScore               *float64 `json:"sit_out_score"`
	HideInactiveFromStandings *bool    `json:"hide_inactive_from_standings"`
	HideInactiveFromMatches   *bool    `json:"hide_inactive_from_matches"`
	CourtCount                *int     `json:"court_count"`
	Courts                    []string `json:"courts"`
}

func (s *Server) handlePatchSessionConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.PathValue("id")
	ctx := r.Context()
	sess, err := s.loadSession(ctx, id, w)
	if sess == nil {
		return
	}
	if err != nil {
		return
	}
	var body patchSessionConfigBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	hasK := body.SitOutScore != nil
	hasHS := body.HideInactiveFromStandings != nil
	hasHM := body.HideInactiveFromMatches != nil
	hasCourtCount := body.CourtCount != nil
	hasCourts := body.Courts != nil
	if !hasK && !hasHS && !hasHM && !hasCourtCount && !hasCourts {
		writeError(w, http.StatusBadRequest, "provide session config fields")
		return
	}
	if hasCourtCount {
		if err := validateCourtCount(*body.CourtCount); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if hasCourts && len(body.Courts) == 0 {
		writeError(w, http.StatusBadRequest, "courts must not be empty")
		return
	}
	if hasCourts && hasCourtCount && len(body.Courts) != *body.CourtCount {
		writeError(w, http.StatusBadRequest, "court names must match court_count")
		return
	}
	if hasCourts {
		if err := validateCourtNames(body.Courts); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if hasK {
		v := *body.SitOutScore
		if math.IsNaN(v) || math.IsInf(v, 0) {
			writeError(w, http.StatusBadRequest, "sit_out_score must be a finite number")
			return
		}
		if v < 0 || v > 1000 {
			writeError(w, http.StatusBadRequest, "sit_out_score must be between 0 and 1000")
			return
		}
		sess.SitOutScore = &v
	}
	if hasHS {
		sess.HideInactiveFromStandings = body.HideInactiveFromStandings
	}
	if hasHM {
		sess.HideInactiveFromMatches = body.HideInactiveFromMatches
	}
	if hasCourtCount || hasCourts {
		count := len(sess.Courts)
		if hasCourtCount {
			count = *body.CourtCount
		}
		if count == 0 {
			count = 1
		}
		if hasCourts {
			names := body.Courts
			sess.Courts = makeCourtsWithNames(names)
		} else {
			sess.Courts = resizeCourts(sess.Courts, count)
		}
	}
	session.EnsureHideInactiveDefaults(sess)

	if err := s.Store.Save(ctx, sess); err != nil {
		s.Logger.Error("save session config", "err", err)
		writeError(w, http.StatusInternalServerError, "could not save session")
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

type addSessionPlayerBody struct {
	Name string `json:"name"`
}

func (s *Server) handleAddSessionPlayer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.PathValue("id")
	ctx := r.Context()
	sess, err := s.loadSession(ctx, id, w)
	if sess == nil {
		return
	}
	if err != nil {
		return
	}
	var body addSessionPlayerBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "player name required")
		return
	}

	for _, p := range sess.Players {
		if !p.Inactive && strings.EqualFold(strings.TrimSpace(p.Name), name) {
			writeError(w, http.StatusBadRequest, "a player with that name is already in the shuffle")
			return
		}
	}

	reviveIdx := -1
	for i := range sess.Players {
		p := sess.Players[i]
		if p.Inactive && strings.EqualFold(strings.TrimSpace(p.Name), name) {
			reviveIdx = i
			break
		}
	}

	if reviveIdx >= 0 {
		sess.Players[reviveIdx].Inactive = false
	} else {
		if len(sess.Players) >= 16 {
			writeError(w, http.StatusBadRequest, "maximum 16 players per session")
			return
		}
		newID := newPlayerID()
		seen := playerIDSet(sess.Players)
		for seen[newID] {
			newID = newPlayerID()
		}
		sess.Players = append(sess.Players, session.Player{ID: newID, Name: name})
	}

	if session.UsesRoundModel(sess) {
		if sess.CurrentRound == nil || sess.CurrentRound.Status != session.RoundStatusOpen {
			writeError(w, http.StatusBadRequest, "no open round to update")
			return
		}
		for _, slot := range sess.CurrentRound.Slots {
			if slot.Status == session.RoundSlotStatusCompleted {
				writeError(w, http.StatusBadRequest, "cannot add a player after saving a court result")
				return
			}
		}
		round, key, pickErr := scheduler.PickRound(sess.Players, recordedMatches(sess), sess.ShufflingStyle, sess.Courts, sess.MatchFormat, "", nil)
		if pickErr != nil {
			writeError(w, http.StatusBadRequest, "could not refresh round")
			return
		}
		sess.CurrentRound, sess.SuggestionKey = round, key
	} else {
		var sug *session.SuggestedMatch
		var key string
		var errPick error
		if sess.MatchFormat == session.MatchFormatSingles {
			sug, key, errPick = scheduler.PickSuggestionSingles(sess.Players, sess.Matches, sess.ShufflingStyle, "", nil)
		} else {
			sug, key, errPick = scheduler.PickSuggestion(sess.Players, sess.Matches, sess.ShufflingStyle, "", nil)
		}
		if errPick != nil {
			s.Logger.Error("pick suggestion after add player", "err", errPick)
			writeError(w, http.StatusInternalServerError, "could not refresh lineup")
			return
		}
		sess.Suggested = sug
		sess.SuggestionKey = key
	}
	if err := s.Store.Save(ctx, sess); err != nil {
		s.Logger.Error("save session", "err", err)
		writeError(w, http.StatusInternalServerError, "could not save session")
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleRemoveSessionPlayer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	pid := strings.TrimSpace(r.PathValue("player_id"))
	id := r.PathValue("id")
	ctx := r.Context()
	sess, err := s.loadSession(ctx, id, w)
	if sess == nil {
		return
	}
	if err != nil {
		return
	}
	idx := playerIndex(sess)
	target, ok := idx[pid]
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown player id")
		return
	}
	if target.Inactive {
		writeJSON(w, http.StatusOK, sess)
		return
	}

	var minActive int
	if sess.MatchFormat == session.MatchFormatSingles {
		minActive = 2
	} else {
		minActive = 4
	}
	if shuffleActiveCount(sess.Players) <= minActive {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("cannot remove players from shuffle: need at least %d active players", minActive))
		return
	}

	for i := range sess.Players {
		if sess.Players[i].ID == pid {
			sess.Players[i].Inactive = true
			break
		}
	}

	var sug *session.SuggestedMatch
	var key string
	var errPick error
	if sess.MatchFormat == session.MatchFormatSingles {
		sug, key, errPick = scheduler.PickSuggestionSingles(sess.Players, sess.Matches, sess.ShufflingStyle, "", nil)
	} else {
		sug, key, errPick = scheduler.PickSuggestion(sess.Players, sess.Matches, sess.ShufflingStyle, "", nil)
	}
	if errPick != nil {
		for i := range sess.Players {
			if sess.Players[i].ID == pid {
				sess.Players[i].Inactive = false
				break
			}
		}
		if errors.Is(errPick, scheduler.ErrNoSinglesLineup) {
			writeError(w, http.StatusBadRequest, "cannot remove player: lineup would drop below two active players")
			return
		}
		s.Logger.Error("pick suggestion after remove shuffle player", "err", errPick)
		writeError(w, http.StatusInternalServerError, "could not refresh lineup")
		return
	}
	sess.Suggested = sug
	sess.SuggestionKey = key
	if err := s.Store.Save(ctx, sess); err != nil {
		s.Logger.Error("save session", "err", err)
		writeError(w, http.StatusInternalServerError, "could not save session")
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func shuffleActiveCount(players []session.Player) int {
	n := 0
	for _, p := range players {
		if !p.Inactive {
			n++
		}
	}
	return n
}

func validateCourtCount(count int) error {
	if count < 1 || count > 8 {
		return errors.New("court_count must be between 1 and 8")
	}
	return nil
}

func validateCourtNames(names []string) error {
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return errors.New("court names must not be blank")
		}
		key := strings.ToLower(trimmed)
		if seen[key] {
			return errors.New("court names must be unique")
		}
		seen[key] = true
	}
	return nil
}

func makeCourts(count int) []session.Court {
	courts := make([]session.Court, count)
	for i := range courts {
		courts[i] = session.Court{ID: fmt.Sprintf("court-%d", i+1), Name: fmt.Sprintf("Court %d", i+1)}
	}
	return courts
}

func makeCourtsWithNames(names []string) []session.Court {
	courts := make([]session.Court, len(names))
	for i, name := range names {
		courts[i] = session.Court{ID: fmt.Sprintf("court-%d", i+1), Name: strings.TrimSpace(name)}
	}
	return courts
}

func resizeCourts(existing []session.Court, count int) []session.Court {
	courts := makeCourts(count)
	for i := 0; i < len(existing) && i < len(courts); i++ {
		courts[i].ID = existing[i].ID
		courts[i].Name = existing[i].Name
	}
	return courts
}

func playerIDSet(players []session.Player) map[string]bool {
	m := make(map[string]bool, len(players))
	for _, p := range players {
		m[p.ID] = true
	}
	return m
}

func (s *Server) handleResetScores(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.PathValue("id")
	ctx := r.Context()
	sess, err := s.loadSession(ctx, id, w)
	if sess == nil {
		return
	}
	if err != nil {
		return
	}
	if session.UsesRoundModel(sess) {
		sess.Matches = []session.RecordedMatch{}
		sess.Rounds = []session.Round{}
		sess.CurrentRound = nil
		rebuildRoundStats(sess)
		round, key, pickErr := scheduler.PickRound(sess.Players, nil, sess.ShufflingStyle, sess.Courts, sess.MatchFormat, "", nil)
		if pickErr != nil {
			writeError(w, http.StatusInternalServerError, "could not build lineup after reset")
			return
		}
		sess.CurrentRound, sess.SuggestionKey = round, key
		if err := s.Store.Save(ctx, sess); err != nil {
			writeError(w, http.StatusInternalServerError, "could not save session")
			return
		}
		writeJSON(w, http.StatusOK, sess)
		return
	}

	for i := range sess.Players {
		sess.Players[i].GamesPlayed = 0
		sess.Players[i].Wins = 0
		sess.Players[i].Losses = 0
		sess.Players[i].Draws = 0
	}
	sess.Matches = []session.RecordedMatch{}
	sess.SuggestionKey = ""

	var sug *session.SuggestedMatch
	var key string
	var errPick error
	if sess.MatchFormat == session.MatchFormatSingles {
		sug, key, errPick = scheduler.PickSuggestionSingles(sess.Players, sess.Matches, sess.ShufflingStyle, "", nil)
	} else {
		sug, key, errPick = scheduler.PickSuggestion(sess.Players, sess.Matches, sess.ShufflingStyle, "", nil)
	}
	if errPick != nil {
		s.Logger.Error("pick suggestion after reset", "err", errPick)
		writeError(w, http.StatusInternalServerError, "could not build lineup after reset")
		return
	}
	sess.Suggested = sug
	sess.SuggestionKey = key

	if err := s.Store.Save(ctx, sess); err != nil {
		s.Logger.Error("save session", "err", err)
		writeError(w, http.StatusInternalServerError, "could not save session")
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) loadSession(ctx context.Context, id string, w http.ResponseWriter) (*session.Session, error) {
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return nil, errors.New("missing id")
	}
	sess, err := s.Store.Get(ctx, id)
	if errors.Is(err, redisstore.ErrNotFound) {
		writeError(w, http.StatusNotFound, "session not found")
		return nil, err
	}
	if err != nil {
		s.Logger.Error("get session", "err", err)
		writeError(w, http.StatusInternalServerError, "could not load session")
		return nil, err
	}
	migrateLegacyRound(sess)
	return sess, nil
}

func revertMatchResult(sess *session.Session, rec session.RecordedMatch) {
	idx := playerIndex(sess)
	all := append(append([]string{}, rec.TeamAIDs...), rec.TeamBIDs...)
	for _, pid := range all {
		p := idx[pid]
		p.GamesPlayed--
		idx[pid] = p
	}
	switch {
	case rec.ScoreA > rec.ScoreB:
		for _, pid := range rec.TeamAIDs {
			p := idx[pid]
			p.Wins--
			idx[pid] = p
		}
		for _, pid := range rec.TeamBIDs {
			p := idx[pid]
			p.Losses--
			idx[pid] = p
		}
	case rec.ScoreB > rec.ScoreA:
		for _, pid := range rec.TeamBIDs {
			p := idx[pid]
			p.Wins--
			idx[pid] = p
		}
		for _, pid := range rec.TeamAIDs {
			p := idx[pid]
			p.Losses--
			idx[pid] = p
		}
	default:
		for _, pid := range all {
			p := idx[pid]
			p.Draws--
			idx[pid] = p
		}
	}
	for i := range sess.Players {
		pid := sess.Players[i].ID
		if upd, ok := idx[pid]; ok {
			sess.Players[i] = upd
		}
	}
}

func applyMatchResult(sess *session.Session, rec session.RecordedMatch) {
	idx := playerIndex(sess)
	for _, pid := range append(rec.TeamAIDs, rec.TeamBIDs...) {
		p := idx[pid]
		p.GamesPlayed++
		idx[pid] = p
	}
	switch {
	case rec.ScoreA > rec.ScoreB:
		for _, pid := range rec.TeamAIDs {
			p := idx[pid]
			p.Wins++
			idx[pid] = p
		}
		for _, pid := range rec.TeamBIDs {
			p := idx[pid]
			p.Losses++
			idx[pid] = p
		}
	case rec.ScoreB > rec.ScoreA:
		for _, pid := range rec.TeamBIDs {
			p := idx[pid]
			p.Wins++
			idx[pid] = p
		}
		for _, pid := range rec.TeamAIDs {
			p := idx[pid]
			p.Losses++
			idx[pid] = p
		}
	default:
		// Tie: games played already counted; award draws only (no W/L).
		for _, pid := range append(rec.TeamAIDs, rec.TeamBIDs...) {
			p := idx[pid]
			p.Draws++
			idx[pid] = p
		}
	}
	for i := range sess.Players {
		pid := sess.Players[i].ID
		if upd, ok := idx[pid]; ok {
			sess.Players[i] = upd
		}
	}
}

func playerIndex(sess *session.Session) map[string]session.Player {
	m := make(map[string]session.Player, len(sess.Players))
	for _, p := range sess.Players {
		m[p.ID] = p
	}
	return m
}

func distinctFour(ids []string) bool {
	if len(ids) != 4 {
		return false
	}
	s := map[string]bool{}
	for _, id := range ids {
		if s[id] {
			return false
		}
		s[id] = true
	}
	return true
}

func parseSport(v string) (session.Sport, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "tennis":
		return session.SportTennis, true
	case "padel":
		return session.SportPadel, true
	default:
		return "", false
	}
}

func normalizeNames(in []string) []string {
	var out []string
	for _, n := range in {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		out = append(out, n)
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func newSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[0:4]) + "-" + hex.EncodeToString(b[4:6]) + "-" + hex.EncodeToString(b[6:8]) + "-" + hex.EncodeToString(b[8:10]) + "-" + hex.EncodeToString(b[10:])
}

func newPlayerID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
