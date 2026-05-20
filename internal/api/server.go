package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
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
	mux.HandleFunc("POST /api/sessions", s.handleCreateSession)
	mux.HandleFunc("GET /api/sessions/{id}", s.handleGetSession)
	mux.HandleFunc("POST /api/sessions/{id}/matches", s.handleRecordMatch)
	mux.HandleFunc("POST /api/sessions/{id}/reshuffle", s.handleReshuffle)
	mux.HandleFunc("POST /api/sessions/{id}/reset", s.handleResetScores)
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Sport   string   `json:"sport"`
		Players []string `json:"players"`
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
	names := normalizeNames(req.Players)
	if !(len(names) >= 4 && len(names) <= 16) {
		writeError(w, http.StatusBadRequest, "need between 4 and 16 players")
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
		ID:      newSessionID(),
		Sport:   sp,
		Players: players,
		Matches: []session.RecordedMatch{},
	}
	sug, key, err := scheduler.PickSuggestion(sess.Players, sess.Matches, "", nil)
	if err != nil {
		s.Logger.Error("pick suggestion", "err", err)
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
	if len(body.TeamAIDs) != 2 || len(body.TeamBIDs) != 2 {
		writeError(w, http.StatusBadRequest, "team_a_ids and team_b_ids must each have 2 player ids")
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
	allIDs := append(append([]string{}, body.TeamAIDs...), body.TeamBIDs...)
	if !distinctFour(allIDs) {
		writeError(w, http.StatusBadRequest, "need four distinct player ids")
		return
	}
	idx := playerIndex(sess)
	for _, pid := range allIDs {
		if _, ok := idx[pid]; !ok {
			writeError(w, http.StatusBadRequest, "unknown player id")
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

	sug, key, err := scheduler.PickSuggestion(sess.Players, sess.Matches, "", nil)
	if err != nil {
		s.Logger.Error("pick suggestion after match", "err", err)
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

	sug, key, err := scheduler.PickSuggestion(sess.Players, sess.Matches, sess.SuggestionKey, body.ExcludePlayerIDs)
	if err != nil {
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

	sug, key, err := scheduler.PickSuggestion(sess.Players, sess.Matches, "", nil)
	if err != nil {
		s.Logger.Error("pick suggestion after reset", "err", err)
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
