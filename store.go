package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The tapes read model, read directly. The queries here are ports of the
// exact reads core's export handlers perform (pkg/storage/postgres:
// ListSessionRecords, GetSessionRecord, getSessionPreviews,
// ListTraceSummariesBySession, ListSpanLinksBySession, ListSpansByTrace)
// so the cassette's row sets and orderings match core's byte for byte.
//
// The cassette's manifest declares these relations as `depends.views` on
// the v1 contract; until tapes publishes tapes_v1.* contract views, the
// deployment grants SELECT on the physical tables the views will front:
// sessions, span_turns_20260615, spans_20260615, span_links_20260615.

// singleTenantOrgID mirrors core's fixed single-tenant org scope.
const singleTenantOrgID = "00000000-0000-0000-0000-000000000000"

// sessionRecord is the flat sessions-table row (core's
// storage.SessionRecord, minus fields the export wire never carries).
type sessionRecord struct {
	ID                string
	HarnessID         string
	HarnessSessionID  string
	Name              string
	DisplayName       string
	DerivedTitle      string
	Cwd               string
	HarnessVersion    string
	ParentSessionID   string
	StartedAt         time.Time
	LastSeenAt        time.Time
	EndedAt           *time.Time
	HarnessMetadata   map[string]any
	TotalInputTokens  int64
	TotalOutputTokens int64
	TotalCostUsd      float64
	TurnCount         int
	DerivedStatus     string
	Model             string
	ModelUsage        []ModelUsage
	Tasks             json.RawMessage
	KindCounts        json.RawMessage
	Preview           string
	AuthSubject       string
	// SortVal is the canonical ::text form of last_seen_at, the keyset
	// cursor boundary for the export's internal paging.
	SortVal string
}

// spanTurnRecord is one user-visible turn (trace) header.
type spanTurnRecord struct {
	TraceID             string
	UserPrompt          string
	ResponsePreview     string
	Synthetic           string
	Status              string
	Source              string
	StartedAt           time.Time
	EndedAt             *time.Time
	DurationNS          int64
	TotalInputTokens    int64
	TotalOutputTokens   int64
	MainInputTokens     int64
	MainOutputTokens    int64
	CacheReadTokens     int64
	CacheCreationTokens int64
	TotalCostUSD        float64
}

// traceSummaryRecord is a turn header with its span count.
type traceSummaryRecord struct {
	spanTurnRecord
	SpanCount int
}

// spanRecord is one observed unit of work within a trace.
type spanRecord struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	Kind         string
	Name         string
	Status       string
	CallKind     string
	ThreadID     string
	Model        string
	StopReason   string
	StartedAt    time.Time
	DurationNS   int64
	Seq          int64
	Input        json.RawMessage
	Output       json.RawMessage
	Usage        json.RawMessage
	RawTurnID    int64
	Verdict      json.RawMessage
}

// spanLinkRecord is a dataflow edge between spans, possibly across
// traces.
type spanLinkRecord struct {
	FromTraceID string
	FromSpanID  string
	FromIO      string
	ToTraceID   string
	ToSpanID    string
	ToIO        string
	Kind        string
}

// sessionListOpts parameterizes the export's session walk: the activity
// window plus the keyset cursor (last_seen_at DESC, id DESC — the export
// path's fixed sort).
type sessionListOpts struct {
	Limit     int
	CursorVal *string
	CursorID  *string
	Since     *time.Time
	Until     *time.Time
}

// store reads the tapes read model. Nil when the deployment supplied no
// database URL.
type store struct {
	pool *pgxpool.Pool
}

func openStore(ctx context.Context, dsn string) (*store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, nil
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connecting to postgres: %w", err)
	}
	return &store{pool: pool}, nil
}

func (s *store) Close() { s.pool.Close() }

// sessionColumns is the select list every session read shares, scanned by
// scanSessionRecord in the same order.
const sessionColumns = `id::text, auth_subject, harness_id, harness_session_id, name, cwd, ` +
	`harness_version, parent_session_id::text, started_at, last_seen_at, ended_at, harness_metadata, ` +
	`total_input_tokens, total_output_tokens, COALESCE(total_cost_usd, 0)::float8, turn_count, ` +
	`derived_status, derived_title, derived_model, model_usage, tasks, kind_counts, display_name`

// scanSessionRecord scans one sessions row (sessionColumns order) into
// its flat record, applying the same NULL folds and the same name
// coalesce (name, else derived_title) core's sessionRecordFromRow does.
func scanSessionRecord(row pgx.Rows, withSortVal bool) (sessionRecord, error) {
	var (
		rec                                            sessionRecord
		name, cwd, harnessVersion, parentID            *string
		derivedTitle, displayName                      *string
		harnessMetadata, modelUsage, tasks, kindCounts []byte
		turnCount                                      int32
		sortVal                                        *string
	)
	dest := []any{
		&rec.ID, &rec.AuthSubject, &rec.HarnessID, &rec.HarnessSessionID, &name, &cwd,
		&harnessVersion, &parentID, &rec.StartedAt, &rec.LastSeenAt, &rec.EndedAt, &harnessMetadata,
		&rec.TotalInputTokens, &rec.TotalOutputTokens, &rec.TotalCostUsd, &turnCount,
		&rec.DerivedStatus, &derivedTitle, &rec.Model, &modelUsage, &tasks, &kindCounts, &displayName,
	}
	if withSortVal {
		dest = append(dest, &sortVal)
	}
	if err := row.Scan(dest...); err != nil {
		return sessionRecord{}, err
	}
	rec.TurnCount = int(turnCount)
	if displayName != nil {
		rec.DisplayName = *displayName
	}
	if derivedTitle != nil {
		rec.DerivedTitle = *derivedTitle
	}
	// A stored name is the session's label; the folded title-gen output
	// is only the fallback when no name is set — core's coalesce.
	if name != nil && *name != "" {
		rec.Name = *name
	} else if derivedTitle != nil && *derivedTitle != "" {
		rec.Name = *derivedTitle
	}
	if cwd != nil {
		rec.Cwd = *cwd
	}
	if harnessVersion != nil {
		rec.HarnessVersion = *harnessVersion
	}
	if parentID != nil {
		rec.ParentSessionID = *parentID
	}
	if len(harnessMetadata) > 0 {
		var m map[string]any
		if err := json.Unmarshal(harnessMetadata, &m); err == nil {
			rec.HarnessMetadata = m
		}
	}
	if len(modelUsage) > 0 {
		var mu []ModelUsage
		if err := json.Unmarshal(modelUsage, &mu); err == nil {
			rec.ModelUsage = mu
		}
	}
	rec.Tasks = json.RawMessage(tasks)
	rec.KindCounts = json.RawMessage(kindCounts)
	if sortVal != nil {
		rec.SortVal = *sortVal
	}
	return rec, nil
}

// ListSessionRecords returns one page of sessions ordered last_seen_at
// DESC, id DESC — the export path's fixed sort — windowed by activity
// (a turn started in [since, until), the same predicate core's list and
// /v1/stats share) and resumable via the keyset cursor.
func (s *store) ListSessionRecords(ctx context.Context, opts sessionListOpts) ([]sessionRecord, error) {
	named := pgx.NamedArgs{"org_id": singleTenantOrgID, "lim": int32(opts.Limit)} //nolint:gosec // limit bounded by the handler
	where := []string{"org_id = @org_id"}

	if opts.Since != nil || opts.Until != nil {
		conds := []string{"t.session_id = sessions.id", "t.org_id = @org_id"}
		if opts.Since != nil {
			named["since"] = *opts.Since
			conds = append(conds, "t.started_at >= @since::timestamptz")
		}
		if opts.Until != nil {
			named["until"] = *opts.Until
			conds = append(conds, "t.started_at < @until::timestamptz")
		}
		where = append(where,
			"EXISTS (SELECT 1 FROM span_turns_20260615 t WHERE "+strings.Join(conds, " AND ")+")")
	}
	if opts.CursorVal != nil && opts.CursorID != nil {
		named["cursor_val"] = *opts.CursorVal
		named["cursor_id"] = *opts.CursorID
		where = append(where,
			"(last_seen_at < @cursor_val::timestamptz OR "+
				"(last_seen_at = @cursor_val::timestamptz AND id < @cursor_id::uuid))")
	}

	q := "SELECT " + sessionColumns + ", last_seen_at::text AS sort_val FROM sessions WHERE " +
		strings.Join(where, " AND ") + " ORDER BY last_seen_at DESC, id DESC LIMIT @lim"

	rows, err := s.pool.Query(ctx, q, named)
	if err != nil {
		return nil, fmt.Errorf("list session records: %w", err)
	}
	defer rows.Close()

	var out []sessionRecord
	for rows.Next() {
		rec, err := scanSessionRecord(rows, true)
		if err != nil {
			return nil, fmt.Errorf("list session records: scan: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list session records: %w", err)
	}
	s.attachPreviews(ctx, out)
	return out, nil
}

// GetSessionRecord returns a single session by its UUID, or nil if not
// found under the org.
func (s *store) GetSessionRecord(ctx context.Context, id string) (*sessionRecord, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT "+sessionColumns+" FROM sessions WHERE org_id = $1 AND id = $2",
		singleTenantOrgID, id)
	if err != nil {
		return nil, fmt.Errorf("get session record: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("get session record: %w", err)
		}
		return nil, nil
	}
	rec, err := scanSessionRecord(rows, false)
	if err != nil {
		return nil, fmt.Errorf("get session record: scan: %w", err)
	}
	rows.Close()

	recs := []sessionRecord{rec}
	s.attachPreviews(ctx, recs)
	return &recs[0], nil
}

const sessionPreviewMaxRunes = 120

// attachPreviews populates Preview on each record in place from a single
// batched preview query. Previews are decoration, so a fetch failure is
// logged and the records are returned without previews — core's policy.
func (s *store) attachPreviews(ctx context.Context, records []sessionRecord) {
	previews, err := s.getSessionPreviews(ctx, records)
	if err != nil {
		slog.WarnContext(ctx, "attach session previews", "error", err)
		return
	}
	for i := range records {
		records[i].Preview = previews[records[i].ID]
	}
}

// getSessionPreviews fetches the first genuine turn's user prompt for
// each session in one query — the same DISTINCT ON read core performs.
func (s *store) getSessionPreviews(ctx context.Context, sessions []sessionRecord) (map[string]string, error) {
	if len(sessions) == 0 {
		return nil, nil
	}
	ids := make([]string, len(sessions))
	for i, rec := range sessions {
		ids[i] = rec.ID
	}

	rows, err := s.pool.Query(ctx, `
SELECT DISTINCT ON (session_id) session_id::text, user_prompt
FROM span_turns_20260615
WHERE session_id = ANY($1::uuid[])
ORDER BY session_id, (synthetic = '' AND TRIM(user_prompt) <> '') DESC, started_at ASC
`, ids)
	if err != nil {
		return nil, fmt.Errorf("get session previews: %w", err)
	}
	defer rows.Close()

	out := make(map[string]string, len(sessions))
	for rows.Next() {
		var sessionID, userPrompt string
		if err := rows.Scan(&sessionID, &userPrompt); err != nil {
			continue
		}
		text := strings.TrimSpace(userPrompt)
		if utf8.RuneCountInString(text) > sessionPreviewMaxRunes {
			runes := []rune(text)
			text = string(runes[:sessionPreviewMaxRunes])
		}
		out[sessionID] = text
	}
	return out, rows.Err()
}

// ListTraceSummaries returns a session's turn headers with span counts,
// in the presentation order core serves them (started_at ASC, trace_id
// ASC).
func (s *store) ListTraceSummaries(ctx context.Context, sessionID string) ([]traceSummaryRecord, error) {
	rows, err := s.pool.Query(ctx, `
SELECT t.trace_id, t.user_prompt, t.response_preview, t.synthetic,
       t.status, t.source, t.started_at, t.ended_at, t.duration_ns,
       t.total_input_tokens, t.total_output_tokens,
       t.main_input_tokens, t.main_output_tokens,
       t.cache_read_tokens, t.cache_creation_tokens,
       COALESCE(t.total_cost_usd, 0)::float8,
       count(s.span_id) AS span_count
FROM span_turns_20260615 t
LEFT JOIN spans_20260615 s ON s.org_id = t.org_id AND s.trace_id = t.trace_id
WHERE t.session_id = $1
GROUP BY t.org_id, t.trace_id
ORDER BY t.started_at ASC, t.trace_id ASC
`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list trace summaries: %w", err)
	}
	defer rows.Close()

	var out []traceSummaryRecord
	for rows.Next() {
		var (
			rec       traceSummaryRecord
			spanCount int64
		)
		if err := rows.Scan(
			&rec.TraceID, &rec.UserPrompt, &rec.ResponsePreview, &rec.Synthetic,
			&rec.Status, &rec.Source, &rec.StartedAt, &rec.EndedAt, &rec.DurationNS,
			&rec.TotalInputTokens, &rec.TotalOutputTokens,
			&rec.MainInputTokens, &rec.MainOutputTokens,
			&rec.CacheReadTokens, &rec.CacheCreationTokens,
			&rec.TotalCostUSD,
			&spanCount,
		); err != nil {
			return nil, fmt.Errorf("list trace summaries: scan: %w", err)
		}
		rec.SpanCount = int(spanCount)
		out = append(out, rec)
	}
	return out, rows.Err()
}

// ListSessionLinks returns a session's dataflow links in the same
// deterministic key order core serves them.
func (s *store) ListSessionLinks(ctx context.Context, sessionID string) ([]spanLinkRecord, error) {
	rows, err := s.pool.Query(ctx, `
SELECT from_trace_id, from_span_id, from_io, to_trace_id, to_span_id, to_io, kind
FROM span_links_20260615
WHERE session_id = $1
ORDER BY from_trace_id, from_span_id, to_trace_id, to_span_id, from_io, to_io
`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list span links: %w", err)
	}
	defer rows.Close()

	var out []spanLinkRecord
	for rows.Next() {
		var l spanLinkRecord
		if err := rows.Scan(&l.FromTraceID, &l.FromSpanID, &l.FromIO,
			&l.ToTraceID, &l.ToSpanID, &l.ToIO, &l.Kind); err != nil {
			return nil, fmt.Errorf("list span links: scan: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ListTraceSpans returns one trace's spans in presentation order (seq
// ASC, started_at ASC, span_id ASC) with full payloads.
func (s *store) ListTraceSpans(ctx context.Context, traceID string) ([]spanRecord, error) {
	rows, err := s.pool.Query(ctx, `
SELECT trace_id, span_id, parent_span_id, kind, name, status, call_kind,
       thread_id, model, stop_reason, started_at, duration_ns, seq,
       input, output, usage, raw_turn_id, verdict
FROM spans_20260615
WHERE org_id = $1 AND trace_id = $2
ORDER BY seq ASC, started_at ASC, span_id ASC
`, singleTenantOrgID, traceID)
	if err != nil {
		return nil, fmt.Errorf("list spans by trace: %w", err)
	}
	defer rows.Close()

	var out []spanRecord
	for rows.Next() {
		var (
			rec                           spanRecord
			input, output, usage, verdict []byte
			rawTurnID                     *int64
		)
		if err := rows.Scan(
			&rec.TraceID, &rec.SpanID, &rec.ParentSpanID, &rec.Kind, &rec.Name,
			&rec.Status, &rec.CallKind, &rec.ThreadID, &rec.Model, &rec.StopReason,
			&rec.StartedAt, &rec.DurationNS, &rec.Seq,
			&input, &output, &usage, &rawTurnID, &verdict,
		); err != nil {
			return nil, fmt.Errorf("list spans by trace: scan: %w", err)
		}
		rec.Input = json.RawMessage(input)
		rec.Output = json.RawMessage(output)
		rec.Usage = json.RawMessage(usage)
		rec.Verdict = json.RawMessage(verdict)
		if rawTurnID != nil {
			rec.RawTurnID = *rawTurnID
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
