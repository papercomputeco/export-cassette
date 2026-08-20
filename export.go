package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// The export endpoints, ported 1:1 from core's api/sessions_handlers.go
// (handleExportSession / handleExportSessions and their render helpers).
// Query params, clamping, filenames, headers, status codes, and the JSONL
// line bytes match core for the same rows.

// exportDetail selects how much of the span projection an export line
// carries: full spans (the default) or trace headers only.
type exportDetail string

const (
	exportDetailSpans  exportDetail = "spans"
	exportDetailTraces exportDetail = "traces"
)

// exportDetailFromQuery maps the ?detail= query param to a grain; empty
// defaults to the full span grain. The second return is false for an
// unrecognized value so handlers can 400 instead of silently exporting
// a different grain than the caller asked for.
func exportDetailFromQuery(v string) (exportDetail, bool) {
	switch v {
	case "", string(exportDetailSpans):
		return exportDetailSpans, true
	case string(exportDetailTraces):
		return exportDetailTraces, true
	}
	return "", false
}

// exportTraceHeader is one trace in a detail=traces export line: the
// turn header alone, with no spans/links keys at all — omitting them
// distinguishes "not exported at this grain" from "zero spans".
type exportTraceHeader struct {
	Trace TraceItem `json:"trace"`
}

// exportSessionTraceHeaders is a detail=traces export line: the session
// with its turn headers. tasks/kind_counts are span-derived, so they are
// omitted along with the spans. `schema` stamps the projection
// generation so a traces-grain export line is self-describing like the
// composite.
type exportSessionTraceHeaders struct {
	Schema  string              `json:"schema"`
	Session SessionItem         `json:"session"`
	Traces  []exportTraceHeader `json:"traces"`
}

// exportSessionLine renders one session's export record as a single JSON
// line. At the spans grain (default) it is the same nested session →
// traces → spans projection core's GET /v1/sessions/{id}/traces serves
// (full payloads); at the traces grain it is the session with turn
// headers only, read via ListTraceSummaries so span payloads are never
// loaded. Both export endpoints emit these grains, so a bulk export is
// exactly a concatenation of per-session exports.
func exportSessionLine(ctx context.Context, store *store, sess sessionRecord, detail exportDetail, w io.Writer) error {
	if detail == exportDetailTraces {
		rows, err := store.ListTraceSummaries(ctx, sess.ID)
		if err != nil {
			return fmt.Errorf("list trace summaries for session %s: %w", sess.ID, err)
		}
		item := sessionItemFromRecord(sess, time.Now())
		line := exportSessionTraceHeaders{
			Schema:  ProjectionSchema,
			Session: item,
			Traces:  make([]exportTraceHeader, 0, len(rows)),
		}
		for _, row := range rows {
			ti := traceItemFromTurn(row.spanTurnRecord, row.SpanCount)
			line.Traces = append(line.Traces, exportTraceHeader{Trace: ti})
		}
		if err := json.NewEncoder(w).Encode(line); err != nil {
			return fmt.Errorf("encoding session %s: %w", sess.ID, err)
		}
		return nil
	}

	return streamSessionSpanExport(ctx, store, sess, w)
}

// streamSessionSpanExport renders the spans-grain export line for one
// session — the nested session → traces → spans projection — bounding
// peak memory to one trace's spans instead of the whole session's. The
// light, payload-free rows (turn headers, which carry the trace order
// and per-trace span counts, plus the session-scoped links) are loaded
// whole; each trace's heavy spans are read, encoded, and released one
// trace at a time.
//
// The array/object framing is written by hand and each element goes
// through the same json marshaller core uses, so the output is
// byte-for-byte the line core's export emits for the same rows.
func streamSessionSpanExport(ctx context.Context, store *store, sess sessionRecord, w io.Writer) error {
	// Light, payload-free rows loaded whole: turn headers are the trace
	// ordering authority, links are the flat session-scoped array.
	turns, err := store.ListTraceSummaries(ctx, sess.ID)
	if err != nil {
		return fmt.Errorf("list trace summaries for session %s: %w", sess.ID, err)
	}
	links, err := store.ListSessionLinks(ctx, sess.ID)
	if err != nil {
		return fmt.Errorf("list session links for session %s: %w", sess.ID, err)
	}

	// {"schema":...,"session":...,"traces":[ — the composite response
	// field order, written by hand so the framing matches json.Marshal of
	// the whole struct.
	if err := writeJSONRaw(w, `{"schema":`); err != nil {
		return fmt.Errorf("encoding session %s: %w", sess.ID, err)
	}
	if err := writeJSONValue(w, ProjectionSchema); err != nil {
		return fmt.Errorf("encoding session %s: %w", sess.ID, err)
	}
	if err := writeJSONRaw(w, `,"session":`); err != nil {
		return fmt.Errorf("encoding session %s: %w", sess.ID, err)
	}
	if err := writeJSONValue(w, sessionItemFromRecord(sess, time.Now())); err != nil {
		return fmt.Errorf("encoding session %s: %w", sess.ID, err)
	}
	if err := writeJSONRaw(w, `,"traces":[`); err != nil {
		return fmt.Errorf("encoding session %s: %w", sess.ID, err)
	}

	for i, turn := range turns {
		if i > 0 {
			if err := writeJSONRaw(w, ","); err != nil {
				return fmt.Errorf("encoding session %s: %w", sess.ID, err)
			}
		}
		if err := streamTraceDetail(ctx, store, turn.spanTurnRecord, w); err != nil {
			return fmt.Errorf("encoding session %s: %w", sess.ID, err)
		}
	}

	// ],"links":[ ...session links... ]}
	if err := writeJSONRaw(w, `],"links":[`); err != nil {
		return fmt.Errorf("encoding session %s: %w", sess.ID, err)
	}
	for i, l := range links {
		if i > 0 {
			if err := writeJSONRaw(w, ","); err != nil {
				return fmt.Errorf("encoding session %s: %w", sess.ID, err)
			}
		}
		if err := writeJSONValue(w, spanLinkItem(l)); err != nil {
			return fmt.Errorf("encoding session %s: %w", sess.ID, err)
		}
	}
	// The trailing newline matches json.Encoder.Encode, so a streamed
	// line terminates exactly like the materialized one.
	if err := writeJSONRaw(w, "]}\n"); err != nil {
		return fmt.Errorf("encoding session %s: %w", sess.ID, err)
	}
	return nil
}

// streamTraceDetail writes one trace's {"trace":...,"spans":[...]}
// object, loading that trace's spans on their own so only one trace's
// payloads are resident at a time. The span count on the header is
// len(spans), exactly as core computes it.
func streamTraceDetail(ctx context.Context, store *store, turn spanTurnRecord, w io.Writer) error {
	spans, err := store.ListTraceSpans(ctx, turn.TraceID)
	if err != nil {
		return fmt.Errorf("list spans for trace %s: %w", turn.TraceID, err)
	}
	if err := writeJSONRaw(w, `{"trace":`); err != nil {
		return err
	}
	if err := writeJSONValue(w, traceItemFromTurn(turn, len(spans))); err != nil {
		return err
	}
	if err := writeJSONRaw(w, `,"spans":[`); err != nil {
		return err
	}
	for i, sp := range spans {
		if i > 0 {
			if err := writeJSONRaw(w, ","); err != nil {
				return err
			}
		}
		// Encode one span at a time so the marshalling buffer stays
		// O(one span), never O(one trace).
		if err := writeJSONValue(w, spanItemFromRecord(sp)); err != nil {
			return err
		}
	}
	return writeJSONRaw(w, "]}")
}

// writeJSONValue marshals v with the same HTML-escaping json.Marshal
// applies and writes it out. Marshalling each element separately and
// gluing the framing by hand yields bytes identical to marshalling the
// whole composite, since JSON encoding of these payload types is
// context-free.
func writeJSONValue(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// writeJSONRaw writes literal structural bytes (brackets, commas, keys).
func writeJSONRaw(w io.Writer, s string) error {
	_, err := io.WriteString(w, s)
	return err
}

// countingWriter counts the bytes that actually reached the underlying
// writer, so the bulk handler knows whether the response is still
// uncommitted (zero bytes → a clean JSON 500 is possible) or already
// streaming (any bytes → only truncation remains).
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// exportHoldBytes is how much of a single-session export holdingWriter keeps
// in memory before it commits the response and streams the remainder.
//
// It buys error quality, not throughput: everything under the budget can
// still fail into a clean JSON 500, and the cost of that is bounded by this
// constant instead of by the session. Sessions in production run from under
// a megabyte to well past a hundred, so a few megabytes covers the ordinary
// case outright while the outliers — the ones that made buffering the whole
// line untenable — stream.
const exportHoldBytes = 8 << 20

// holdingWriter defers committing an HTTP response until the export either
// finishes inside a byte budget or outgrows it.
//
// The export endpoints want two things that pull against each other. A
// failure part-way through a render should not reach the client as a
// truncated 200 wearing an attachment header — a half-written JSON line is
// silently corrupt data, which is worse than an error. But a session's export
// is not bounded: this cassette has rendered a single one past 110 MB, so
// holding the whole line to preserve that property is what turns an ordinary
// GET into an OOM.
//
// Holding a prefix resolves it. Until the budget is spent the response is
// uncommitted and a clean error is still available; past it the headers go
// out and the rest streams, accepting truncation as the only remaining
// failure mode for exports big enough that the alternative is a dead pod.
//
// The budget bounds memory, never the export. Nothing is refused or truncated
// for being large: an export past the budget streams to completion with a
// bounded working set, and the only thing that changes at the boundary is
// what a failure would look like if one happened.
//
// The bulk endpoint deliberately does not use this: it must flush after each
// session so bytes arrive progressively over a long window, and flushing is
// exactly what committing early means. It gets its narrower answer — a clean
// error only before the very first byte — from countingWriter.
type holdingWriter struct {
	// w receives everything once the response is committed.
	w io.Writer
	// commit sets the response headers. It runs exactly once, immediately
	// before the first byte is written, so nothing an error path needs to
	// change has been set while a clean error is still possible.
	commit func()
	// hold is the byte budget; buf holds the export until it is spent.
	hold int
	buf  bytes.Buffer

	committed bool
}

func (h *holdingWriter) Write(p []byte) (int, error) {
	if h.committed {
		return h.w.Write(p)
	}
	// Decide before appending, not after. Appending first and then noticing
	// the budget is spent would copy the write into a buffer about to be
	// abandoned — so a single write larger than the budget would be held in
	// full, on top of the caller's own copy of it, which is the allocation
	// this type exists to avoid. Checking first means the held bytes never
	// exceed the budget and an oversized write is never copied at all.
	if h.buf.Len()+len(p) > h.hold {
		if err := h.release(); err != nil {
			return 0, err
		}
		return h.w.Write(p)
	}
	// bytes.Buffer.Write reports no error; it grows or panics.
	h.buf.Write(p)
	return len(p), nil
}

// release commits the response and writes everything held so far.
func (h *holdingWriter) release() error {
	h.commit()
	h.committed = true
	if h.buf.Len() == 0 {
		return nil
	}
	_, err := h.w.Write(h.buf.Bytes())
	// Drop the buffer rather than Reset it: Reset keeps the grown backing
	// array, so every concurrently streaming export would hold the whole
	// budget for its lifetime having no further use for a byte of it.
	h.buf = bytes.Buffer{}
	return err
}

// Close finishes a successful render. An export that never outgrew the budget
// is still held entirely in memory, so this is the call that writes it.
func (h *holdingWriter) Close() error {
	if h.committed {
		return nil
	}
	return h.release()
}

// Committed reports whether any byte has reached the response. While it is
// false the caller still owns the status line and can answer with an error.
func (h *holdingWriter) Committed() bool { return h.committed }

// exportFilename appends the non-default grain to an export filename so
// a traces-grain download is distinguishable from a full one on disk.
func exportFilename(base string, detail exportDetail) string {
	if detail == exportDetailTraces {
		return strings.TrimSuffix(base, ".jsonl") + "-traces.jsonl"
	}
	return base
}

// handleExportSession handles GET /api/export/sessions/{id} — core's
// GET /v1/sessions/{id}/export. It renders the session's full
// trace/span projection as one JSONL line.
func (c *app) handleExportSession(w http.ResponseWriter, r *http.Request) {
	if c.store == nil {
		writeError(w, http.StatusNotImplemented, "sessions not supported by this backend")
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id parameter required")
		return
	}
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, http.StatusBadRequest, "id must be a valid UUID")
		return
	}
	detail, ok := exportDetailFromQuery(r.URL.Query().Get("detail"))
	if !ok {
		writeError(w, http.StatusBadRequest, "detail must be spans or traces")
		return
	}

	// Resolve existence BEFORE anything is streamed, so an unknown id
	// gets a clean 404 with no headers or partial body committed.
	sess, err := c.store.GetSessionRecord(r.Context(), id)
	if err != nil {
		c.logger.Error("get session for export", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load session")
		return
	}
	if sess == nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	// Hold the first exportHoldBytes of the render, then stream. Every
	// session exports in full either way; the budget only decides whether a
	// mid-render failure can still be answered as a clean JSON error or has
	// to truncate a response already on the wire. The renderer itself
	// already bounds its working set to one trace's spans, so past the
	// budget the whole path is O(one trace) no matter how large the session.
	filename := exportFilename(fmt.Sprintf("session-%s-%s.jsonl", id, time.Now().UTC().Format("2006-01-02")), detail)
	body := &holdingWriter{
		w:    w,
		hold: exportHoldBytes,
		commit: func() {
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		},
	}

	if err := exportSessionLine(r.Context(), c.store, *sess, detail, body); err != nil {
		c.logger.Error("export session", "id", id, "error", err)
		if !body.Committed() {
			writeError(w, http.StatusInternalServerError, "failed to render session export")
			return
		}
		// Headers and a prefix of the line are already on the wire, so the
		// status cannot be changed; stopping here truncates the response,
		// which is the same tradeoff the bulk endpoint documents.
		return
	}
	if err := body.Close(); err != nil {
		// Close commits before it writes, so a failure here is the client
		// going away mid-flush — there is no uncommitted response left to
		// answer with.
		c.logger.Error("write session export", "id", id, "error", err)
	}
}

// exportSessionsPageLimit is the internal page size handleExportSessions
// uses when walking ListSessionRecords. It matches core's UI list cap on
// purpose: the point of this endpoint is to keep paging past that cap,
// one page at a time, so memory stays flat regardless of how many
// sessions fall in the window.
const exportSessionsPageLimit = 200

// handleExportSessions handles GET /api/export/sessions — core's
// GET /v1/sessions/export. It streams every session in the requested
// window (default: trailing 30 days) as one nested JSON line each,
// paging internally via the same keyset cursor core uses.
func (c *app) handleExportSessions(w http.ResponseWriter, r *http.Request) {
	if c.store == nil {
		writeError(w, http.StatusNotImplemented, "sessions not supported by this backend")
		return
	}
	detail, ok := exportDetailFromQuery(r.URL.Query().Get("detail"))
	if !ok {
		writeError(w, http.StatusBadRequest, "detail must be spans or traces")
		return
	}

	// The 30-day window is the maximum span for v1, not just the
	// default: the floor is enforced unconditionally, even when the
	// caller supplies an explicit since older than 30 days, so the
	// endpoint can never be used to stream the deployment's entire history.
	// In-window since/until overrides still work — only the lower bound
	// is clamped.
	floor := time.Now().UTC().AddDate(0, 0, -30)
	since := floor
	if raw := r.URL.Query().Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "since must be an RFC3339 timestamp")
			return
		}
		since = t
	}
	if since.Before(floor) {
		since = floor
	}
	var until *time.Time
	if raw := r.URL.Query().Get("until"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "until must be an RFC3339 timestamp")
			return
		}
		until = &t
	}
	if until != nil && !until.After(since) {
		writeError(w, http.StatusBadRequest, "until must be after since")
		return
	}

	// Name the file after the window that actually produced it. The
	// default (no since/until) is the trailing 30 days; an explicit
	// since/until gets a dated range so the filename never claims
	// "last-30-days" for a narrower window. `since` here is the
	// effective (clamped) lower bound.
	now := time.Now().UTC()
	filename := fmt.Sprintf("sessions-last-30-days-%s.jsonl", now.Format("2006-01-02"))
	if r.URL.Query().Get("since") != "" || r.URL.Query().Get("until") != "" {
		end := "now"
		if until != nil {
			end = until.UTC().Format("2006-01-02")
		}
		filename = fmt.Sprintf("sessions-%s-to-%s.jsonl", since.UTC().Format("2006-01-02"), end)
	}
	filename = exportFilename(filename, detail)
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

	// Once bytes are flowing the HTTP status is committed; a failure
	// mid-stream can only be logged and the loop stopped — the same
	// accepted tradeoff core documents on its streaming bulk endpoint.
	// Before the first body byte, though, net/http has not committed
	// anything (unlike core's fiber stream writer, which commits the 200
	// up front), so an initial-page failure can still return the
	// documented JSON 500 instead of masquerading as an empty export.
	ctx := r.Context()
	flush := func() error { return http.NewResponseController(w).Flush() }

	opts := sessionListOpts{
		Since: &since,
		Until: until,
		Limit: exportSessionsPageLimit,
	}
	// The 500 gate is "no body byte has reached the wire yet", not "no
	// session attempted": both grains run reads (trace summaries, links)
	// before their first write, so a first-session query failure still
	// has a clean response available. The counting writer is what makes
	// that distinction exact.
	body := &countingWriter{w: w}
	fail := func(msg string) {
		if body.n == 0 {
			w.Header().Del("Content-Disposition")
			writeError(w, http.StatusInternalServerError, msg)
		}
	}
	for {
		sessions, err := c.store.ListSessionRecords(ctx, opts)
		if err != nil {
			c.logger.Error("list sessions for export", "error", err)
			fail("failed to list sessions")
			return
		}
		for _, sess := range sessions {
			if err := exportSessionLine(ctx, c.store, sess, detail, body); err != nil {
				c.logger.Error("export session", "id", sess.ID, "error", err)
				fail("failed to render session export")
				return
			}
			// Flush after each session so bytes reach the client
			// incrementally instead of only at the end of the whole
			// window — the point of streaming.
			if err := flush(); err != nil {
				// Client went away or the connection failed; nothing
				// left to do but stop producing.
				return
			}
		}

		if len(sessions) < exportSessionsPageLimit {
			return
		}
		last := sessions[len(sessions)-1]
		opts.CursorVal = &last.SortVal
		opts.CursorID = &last.ID
	}
}
