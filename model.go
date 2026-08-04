package main

import (
	"encoding/json"
	"strings"
	"time"
)

// Wire model — ported 1:1 from tapes' API layer (api/sessions_handlers.go,
// api/traces_handlers.go, api/trace_browse_handlers.go) so an export line
// produced here is byte-for-byte the line core's /v1/sessions/export
// produces for the same rows. Field order matters: json.Marshal emits
// struct fields in declaration order, and the golden shape is core's.

// ProjectionSchema is the compatibility date of the derived projection
// generation currently served (the dated *_20260615 table family). It is
// stamped onto the wire `schema` field of every export line.
const ProjectionSchema = "2026-06-15"

// sessionLiveWindow bounds how recently a session must have been seen to
// read as live — the same server-side window core applies.
const sessionLiveWindow = 5 * time.Minute

// SessionItem is the per-session shape: capture identity at the top
// level, the deriver-owned projection nested under `rollup`.
type SessionItem struct {
	// Identity — capture-side facts, ingest-written.
	ID               string         `json:"id"`
	HarnessID        string         `json:"harness_id"`
	HarnessSessionID string         `json:"harness_session_id"`
	Cwd              string         `json:"cwd,omitempty"`
	HarnessVersion   string         `json:"harness_version,omitempty"`
	ParentSessionID  string         `json:"parent_session_id,omitempty"`
	StartedAt        time.Time      `json:"started_at"`
	LastSeenAt       time.Time      `json:"last_seen_at"`
	EndedAt          *time.Time     `json:"ended_at,omitempty"`
	HarnessMetadata  map[string]any `json:"harness_metadata,omitempty"`
	// AuthSubject is the gateway-stamped JWT subject captured at ingest.
	AuthSubject string `json:"auth_subject,omitempty"`
	// Name is the harness identity-row label, or the folded title as a
	// fallback when no name was captured.
	Name string `json:"name,omitempty"`
	// DisplayName is the user's Console rename, empty unless a user set one.
	DisplayName string `json:"display_name,omitempty"`
	// DisplayTitle is the server-resolved label clients render:
	// DisplayName -> rollup.title -> preview -> Name -> id slice.
	DisplayTitle string `json:"display_title"`
	// Live is a runtime presence signal: no recorded end and seen within
	// the liveness window.
	Live bool `json:"live"`
	// Rollup is the deriver-owned projection over the session's spans.
	Rollup SessionRollup `json:"rollup"`
}

// SessionRollup is the deriver-owned session projection — status, title,
// counts, and spend, all folded from the span layer at derive time.
type SessionRollup struct {
	Status string `json:"status"`
	// Title is the deriver's folded session title (derived_title).
	Title     string `json:"title,omitempty"`
	Preview   string `json:"preview,omitempty"`
	TurnCount int    `json:"turn_count"`
	// Model is the dominant conversation-spine model; ModelUsage is the
	// per-model spend breakdown, cost-ordered.
	Model      string       `json:"model,omitempty"`
	ModelUsage []ModelUsage `json:"model_usage,omitempty"`
	// KindCounts and Tasks are pinned so the rollup shape is uniform.
	KindCounts map[string]int `json:"kind_counts"`
	Tasks      []TreeTask     `json:"tasks"`
	Usage      SessionUsage   `json:"usage"`
}

// SessionUsage is the session's total token/cost spend, folded from the
// span layer. Pinned (no omitempty) for a uniform object shape.
type SessionUsage struct {
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// ModelUsage is one model's contribution to a session: how many llm
// calls ran on it and what they spent.
type ModelUsage struct {
	Model        string  `json:"model"`
	Calls        int64   `json:"calls"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUsd      float64 `json:"cost_usd"`
}

// TreeTask is one task folded from the session's TaskCreate/TaskUpdate
// calls.
type TreeTask struct {
	ID          string `json:"id"`
	Subject     string `json:"subject"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
	Updates     int    `json:"updates"`
}

// TraceItem is one user-visible turn's header.
type TraceItem struct {
	TraceID string `json:"trace_id"`
	// UserPrompt is served explicitly (not omitempty): empty means a
	// synthetic opener, and the key must survive on the wire.
	UserPrompt string `json:"user_prompt"`
	// ResponsePreview is the derive-time fold of the closing spine llm
	// call's text output.
	ResponsePreview string `json:"response_preview,omitempty"`
	Status          string `json:"status"`
	// Source is the capture origin of the turn's rows ("wire" |
	// "transcript").
	Source     string     `json:"source"`
	StartedAt  time.Time  `json:"started_at"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
	DurationNS int64      `json:"duration_ns"`
	SpanCount  int        `json:"span_count"`
	// Usage is the trace's total spend over ALL llm spans; MainUsage is
	// the task slice (main agent and its subagents).
	Usage     TraceUsage `json:"usage"`
	MainUsage MainUsage  `json:"main_usage"`
	// Synthetic is a typed deriver signal ("post-compaction",
	// "shadow-opener"); absent for genuine prompt-opened turns.
	Synthetic string `json:"synthetic,omitempty"`
}

// TraceUsage is a trace's total token/cost rollup. Fields are pinned
// (no omitempty) so the object shape is uniform across traces.
type TraceUsage struct {
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	CostUSD             float64 `json:"cost_usd"`
}

// MainUsage is the task token slice of a trace: the main agent and its
// subagents, no cache split or cost.
type MainUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// SpanItem is one observed unit of work, input/output as uniform
// content-block arrays for all kinds. Exports always carry full
// payloads (core's payload=full mode).
type SpanItem struct {
	TraceID      string `json:"trace_id"`
	SpanID       string `json:"span_id"`
	ParentSpanID string `json:"parent_span_id,omitempty"`
	// Seq is the span's presentation ordinal within its trace.
	Seq        int64     `json:"seq"`
	Kind       string    `json:"kind"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	StartedAt  time.Time `json:"started_at"`
	DurationNS int64     `json:"duration_ns"`
	// Deriver-written taxonomy.
	CallKind   string `json:"call_kind"`
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	ThreadID   string `json:"thread_id"`
	RawTurnID  int64  `json:"raw_turn_id,omitempty"`
	// Verdict is the typed security-monitor disposition (null off
	// permission-check spans), deriver-written; object or null.
	Verdict json.RawMessage `json:"verdict" oas:"type=object,nullable"`
	// Input/Output are content-block arrays, uniform for every kind.
	// Pinned to [] when empty.
	Input  json.RawMessage `json:"input" oas:"type=array:object"`
	Output json.RawMessage `json:"output" oas:"type=array:object"`
	// Usage is an llm.Usage object on the wire — {}-pinned for
	// usage-less spans.
	Usage json.RawMessage `json:"usage" oas:"type=object"`
}

// SpanLinkItem is a dataflow edge. kind is a typed top-level field
// (rejoin / verdict / compaction-seam / emits / feeds).
type SpanLinkItem struct {
	Kind        string `json:"kind"`
	FromTraceID string `json:"from_trace_id"`
	FromSpanID  string `json:"from_span_id"`
	FromIO      string `json:"from_io,omitempty"`
	ToTraceID   string `json:"to_trace_id"`
	ToSpanID    string `json:"to_span_id"`
	ToIO        string `json:"to_io,omitempty"`
}

// resolveSessionDisplayTitle picks the label clients render, resolving
// the provenance tiers exactly as core does:
//
//	DisplayName -> DerivedTitle -> Preview (unless JSON-ish) -> Name
//	-> a short harness_session_id slice, then the session id.
func resolveSessionDisplayTitle(s sessionRecord) string {
	if t := strings.TrimSpace(s.DisplayName); t != "" {
		return t
	}
	if t := strings.TrimSpace(s.DerivedTitle); t != "" {
		return t
	}
	if t := strings.TrimSpace(s.Preview); t != "" && !looksLikeJSONPreview(t) {
		return t
	}
	if t := strings.TrimSpace(s.Name); t != "" {
		return t
	}
	if slug := shortHarnessSessionID(s.HarnessSessionID); slug != "" {
		return slug
	}
	return s.ID
}

// looksLikeJSONPreview is a cheap guard for previews that are really
// tool-result payloads rather than user prose (a leading { or [).
func looksLikeJSONPreview(s string) bool {
	t := strings.TrimLeft(s, " \t\r\n")
	return strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")
}

// shortHarnessSessionID is the last-resort label: a 12-char slice of the
// harness session id.
func shortHarnessSessionID(id string) string {
	const n = 12
	if len(id) <= n {
		return id
	}
	return id[:n]
}

func sessionItemFromRecord(s sessionRecord, now time.Time) SessionItem {
	item := SessionItem{
		ID:               s.ID,
		HarnessID:        s.HarnessID,
		HarnessSessionID: s.HarnessSessionID,
		Cwd:              s.Cwd,
		HarnessVersion:   s.HarnessVersion,
		ParentSessionID:  s.ParentSessionID,
		StartedAt:        s.StartedAt,
		LastSeenAt:       s.LastSeenAt,
		EndedAt:          s.EndedAt,
		HarnessMetadata:  s.HarnessMetadata,
		AuthSubject:      s.AuthSubject,
		Name:             s.Name,
		DisplayName:      s.DisplayName,
		DisplayTitle:     resolveSessionDisplayTitle(s),
		Live:             s.EndedAt == nil && now.Sub(s.LastSeenAt) < sessionLiveWindow,
		Rollup: SessionRollup{
			Status:     s.DerivedStatus,
			Title:      s.DerivedTitle,
			Preview:    s.Preview,
			TurnCount:  s.TurnCount,
			Model:      s.Model,
			ModelUsage: s.ModelUsage,
			KindCounts: map[string]int{},
			Tasks:      []TreeTask{},
			Usage: SessionUsage{
				InputTokens:  s.TotalInputTokens,
				OutputTokens: s.TotalOutputTokens,
				CostUSD:      s.TotalCostUsd,
			},
		},
	}
	// Tasks/kind_counts are stored as raw deriver JSON; decode them into
	// the rollup, leaving the pinned []/{} on absent or malformed values.
	if len(s.Tasks) > 0 {
		_ = json.Unmarshal(s.Tasks, &item.Rollup.Tasks)
	}
	if len(s.KindCounts) > 0 {
		_ = json.Unmarshal(s.KindCounts, &item.Rollup.KindCounts)
	}
	return item
}

func traceItemFromTurn(turn spanTurnRecord, spanCount int) TraceItem {
	return TraceItem{
		TraceID:         turn.TraceID,
		UserPrompt:      turn.UserPrompt,
		ResponsePreview: turn.ResponsePreview,
		Status:          turn.Status,
		Source:          turn.Source,
		StartedAt:       turn.StartedAt,
		EndedAt:         turn.EndedAt,
		DurationNS:      turn.DurationNS,
		SpanCount:       spanCount,
		Usage: TraceUsage{
			InputTokens:         turn.TotalInputTokens,
			OutputTokens:        turn.TotalOutputTokens,
			CacheReadTokens:     turn.CacheReadTokens,
			CacheCreationTokens: turn.CacheCreationTokens,
			CostUSD:             turn.TotalCostUSD,
		},
		MainUsage: MainUsage{
			InputTokens:  turn.MainInputTokens,
			OutputTokens: turn.MainOutputTokens,
		},
		Synthetic: turn.Synthetic,
	}
}

// spanItemFromRecord renders one stored span with full payloads — the
// only mode exports use (core's PayloadFull).
func spanItemFromRecord(sp spanRecord) SpanItem {
	return SpanItem{
		TraceID:      sp.TraceID,
		SpanID:       sp.SpanID,
		ParentSpanID: sp.ParentSpanID,
		Seq:          sp.Seq,
		Kind:         sp.Kind,
		Name:         sp.Name,
		Status:       sp.Status,
		StartedAt:    sp.StartedAt,
		DurationNS:   sp.DurationNS,
		CallKind:     sp.CallKind,
		Model:        sp.Model,
		StopReason:   sp.StopReason,
		ThreadID:     sp.ThreadID,
		RawTurnID:    sp.RawTurnID,
		Verdict:      sp.Verdict, // nil → null on the wire
		Input:        emptyArrayIfNil(sp.Input),
		Output:       emptyArrayIfNil(sp.Output),
		Usage:        emptyObjectIfNil(sp.Usage),
	}
}

// spanLinkItem renders a stored link with its kind as a typed field.
func spanLinkItem(l spanLinkRecord) SpanLinkItem {
	return SpanLinkItem{
		Kind:        l.Kind,
		FromTraceID: l.FromTraceID,
		FromSpanID:  l.FromSpanID,
		FromIO:      l.FromIO,
		ToTraceID:   l.ToTraceID,
		ToSpanID:    l.ToSpanID,
		ToIO:        l.ToIO,
	}
}

// emptyArrayIfNil keeps wire fields array-typed when the stored JSONB is
// NULL — core's contentArray in full mode.
func emptyArrayIfNil(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("[]")
	}
	return raw
}

// emptyObjectIfNil keeps wire fields object-typed when the stored JSONB
// is NULL.
func emptyObjectIfNil(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("{}")
	}
	return raw
}
