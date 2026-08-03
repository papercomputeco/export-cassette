// Command export-cassette is the tapes export surface as a cassette.
//
// It republishes the two export endpoints tapes core serves today —
// GET /v1/sessions/export and GET /v1/sessions/{id}/export — as an
// independently deployed HTTP service that tapes admits and proxies:
//
//	tapes core                     this cassette (local)        via tapes
//	GET /v1/sessions/export        GET /api/export/sessions     GET /v1/cassettes/export/sessions
//	GET /v1/sessions/{id}/export   GET /api/export/sessions/{id} GET /v1/cassettes/export/sessions/{id}
//
// Query parameters, JSONL line shapes, download filenames, and status
// codes are 1:1 with the core endpoints. The cassette reads the same
// derived read model (sessions, span_turns, spans, span_links) directly
// from Postgres with a read-only credential the deployment supplies.
//
// Three things make it a cassette:
//
//  1. /ping answers 200, the api.health anchor.
//  2. /openapi serves its OpenAPI document with the x-tapes-cassette
//     manifest, which core fetches, admits, and aggregates.
//  3. Its API lives under /api/export, the declared prefix core strips
//     and republishes under /v1/cassettes/export.
//
// Configuration arrives entirely through the environment supplied by the
// deployment: no config file, no flags. Without TAPES_DATABASE_URL the
// process still starts and serves its anchors; the export endpoints
// answer 501, mirroring how core answers when its driver lacks the
// sessions capability.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// defaults for every environment variable this cassette reads, so it runs
// with none of them set.
const (
	defaultListen = "0.0.0.0:9998"
	defaultName   = "export"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if err := run(logger); err != nil {
		logger.Error("export cassette failed", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	name := envOrDefault("CASSETTE_NAME", defaultName)
	listen := envOrDefault("CASSETTE_LISTEN", defaultListen)

	store, err := openStore(ctx, os.Getenv("TAPES_DATABASE_URL"))
	if err != nil {
		return err
	}
	if store != nil {
		defer store.Close()
	}

	cassette := &app{name: name, store: store, logger: logger}
	server := &http.Server{
		Addr:              listen,
		Handler:           cassette.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Serve in the background so the signal context can shut the listener
	// down cleanly when its process manager asks it to stop.
	errs := make(chan error, 1)
	go func() {
		logger.Info("export cassette listening",
			"listen", listen, "name", name, "database", store != nil)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
			return
		}
		errs <- nil
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return server.Shutdown(shutdownCtx)
}

// app is the whole cassette: an identity, the tapes read model to
// export from, and a logger. store is nil when the deployment supplied no
// database, in which case the export endpoints answer 501.
type app struct {
	name   string
	store  *store
	logger *slog.Logger
}

func (c *app) routes() http.Handler {
	mux := http.NewServeMux()

	// Anchors. These live at the root of the listener because they
	// describe the process, not the API — core probes and fetches them
	// directly and never proxies them.
	mux.HandleFunc("GET /ping", c.handlePing)
	mux.HandleFunc("GET /openapi", c.handleOpenAPI)

	// The API itself, under the prefix clients call through tapes.
	prefix := "/api/" + c.name
	mux.HandleFunc("GET "+prefix+"/sessions", c.handleExportSessions)
	mux.HandleFunc("GET "+prefix+"/sessions/{id}", c.handleExportSession)

	return mux
}

func (c *app) handlePing(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "cassette": c.name})
}

func (c *app) handleOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openAPIDocument(c.name))
}

// errorResponse is the error body shape the core API uses
// (llm.ErrorResponse); kept identical so a caller moving from
// /v1/sessions/export sees the same errors.
type errorResponse struct {
	// Error is the human-readable failure description.
	Error string `json:"error"`
}

// writeJSON marshals without a trailing newline, matching the bytes
// core's fiber c.JSON responses carry.
func writeJSON(w http.ResponseWriter, status int, body any) {
	b, err := json.Marshal(body)
	if err != nil {
		http.Error(w, `{"error":"encoding response"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
