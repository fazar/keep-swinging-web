package redisstore

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"keep-swinging-web/internal/session"
)

const keyPrefix = "keep-swinging:session:"

// ErrNotFound is returned when a session key is missing.
var ErrNotFound = errors.New("session not found")

// Store persists sessions as JSON documents in Redis.
type Store struct {
	client *redis.Client
	ttl    time.Duration
}

func redisTLSEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("REDIS_TLS")))
	return v == "1" || v == "true" || v == "yes"
}

// NewFromEnv builds a Store using REDIS_ADDR (default localhost:6379), REDIS_PASSWORD, REDIS_DB, SESSION_TTL_DAYS.
// Set REDIS_TLS=true when connecting to TLS-only Redis (e.g. Upstash).
func NewFromEnv() (*Store, error) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	pass := os.Getenv("REDIS_PASSWORD")
	db := 0
	if v := os.Getenv("REDIS_DB"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, err
		}
		db = n
	}
	ttlDays := 30
	if v := os.Getenv("SESSION_TTL_DAYS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, err
		}
		ttlDays = n
	}
	opts := &redis.Options{
		Addr:     addr,
		Password: pass,
		DB:       db,
	}
	if redisTLSEnabled() {
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	rdb := redis.NewClient(opts)
	return &Store{client: rdb, ttl: time.Duration(ttlDays) * 24 * time.Hour}, nil
}

func sessionKey(id string) string {
	return keyPrefix + id
}

// Get loads a session by id.
func (s *Store) Get(ctx context.Context, id string) (*session.Session, error) {
	raw, err := s.client.Get(ctx, sessionKey(id)).Bytes()
	if err == redis.Nil {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var sess session.Session
	if err := json.Unmarshal(raw, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

// Save writes the session and refreshes TTL.
func (s *Store) Save(ctx context.Context, sess *session.Session) error {
	raw, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, sessionKey(sess.ID), raw, s.ttl).Err()
}

// Ping checks Redis connectivity.
func (s *Store) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}
