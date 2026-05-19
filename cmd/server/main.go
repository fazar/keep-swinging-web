package main

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"keep-swinging-web/internal/api"
	"keep-swinging-web/internal/redisstore"
	siteweb "keep-swinging-web/web"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	store, err := redisstore.NewFromEnv()
	if err != nil {
		log.Error("redis config", "err", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.Ping(ctx); err != nil {
		log.Error("redis ping", "err", err)
		os.Exit(1)
	}

	apiMux := http.NewServeMux()
	api.Mount(apiMux, &api.Server{Store: store, Logger: log})

	staticFS, err := fs.Sub(siteweb.Static, "static")
	if err != nil {
		log.Error("static fs", "err", err)
		os.Exit(1)
	}
	files := http.FileServer(http.FS(staticFS))

	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			apiMux.ServeHTTP(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})

	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		if p := strings.TrimSpace(os.Getenv("PORT")); p != "" {
			// Render and similar hosts set PORT; bind on all interfaces.
			addr = "0.0.0.0:" + p
		} else {
			addr = ":8080"
		}
	}
	handler := corsMiddleware(loggingMiddleware(log, root))
	log.Info("listening", "addr", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Error("server", "err", err)
		os.Exit(1)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func loggingMiddleware(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Info("request", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
