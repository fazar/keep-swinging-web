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
	if req.ShufflingStyle != "" {
		sess.ShufflingStyle = session.ShufflingStyle(req.ShufflingStyle)
	}
	k0 := session.DefaultSitOutScore(sp)
	sess.SitOutScore = &k0

	session.EnsureHideInactiveDefaults(sess)

	var sug *session.SuggestedMatch
	var key string
	var errPick error
	if sess.MatchFormat == session.MatchFormatSingles {
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

type patchSessionConfigBody struct {
	SitOutScore               *float64 `json:"sit_out_score"`
	HideInactiveFromStandings *bool    `json:"hide_inactive_from_standings"`
	HideInactiveFromMatches   *bool    `json:"hide_inactive_from_matches"`
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
	if !hasK && !hasHS && !hasHM {
		writeError(w, http.StatusBadRequest, "provide sit_out_score and/or visibility flags")
		return
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
