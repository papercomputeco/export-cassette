package main

import (
	"context"
	"strings"

	oas "github.com/papercomputeco/tapes/pkg/tapesoapi"

	"github.com/papercomputeco/export-cassette/internal/release"
)

// openAPIDocument renders this cassette's OpenAPI document.
//
// Every path is written under /api/<name>, which is what core's prefix
// admission requires. The operation summaries, descriptions, parameters,
// and response codes are ported verbatim from core's export route
// registrations (api/openapi_routes.go), because the contract is the
// point: a consumer of /v1/sessions/export reads the same document here.
//
// The manifest core admits the cassette on rides inside the document as a
// root extension, so there is one artifact to fetch and one thing to
// configure — and so a spec and the metadata describing it can never be
// fetched at two different versions.
func openAPIDocument(name string) []byte {
	prefix := "/api/" + name

	parser := oas.NewParser(oas.WithInfo(oas.Info{
		Title:       "Export Cassette",
		Description: "The tapes session export surface as a cassette.",
		Version:     release.Version,
	}))

	provenance := oas.Provenance{Kind: oas.KindManual, Name: "export cassette"}

	// The manifest is contributed as a root extension on its own
	// fragment. Compile renders root extensions verbatim, so the shape
	// core parses is exactly the shape written here.
	_ = parser.AddFragment(oas.Fragment{
		Provenance: provenance,
		Extensions: map[string]any{"x-tapes-cassette": cassetteManifest(name)},
	})

	_ = parser.AddOperation("GET", prefix+"/sessions",
		oas.NewOperation("exportSessions").
			Summary("Export sessions in a time window as JSONL").
			Description("Streams one JSON line per session in the given window, newest-first, as a "+
				"downloadable attachment. Each line is the session object with its traces, each trace "+
				"carrying its full spans — the same shape as GET /v1/sessions/{id}/traces with "+
				"payload=full. detail=traces exports turn headers only (no spans or links). Thirty days "+
				"is the maximum window as well as the default: an earlier since is clamped to it. "+
				"Not bounded by the /v1/sessions list cap — pages internally.").
			Tag(name).
			QueryParam("since", oas.String(oas.Format("date-time")),
				oas.ParamDescription("Only include sessions with a turn started at or after this "+
					"RFC3339 timestamp (activity window; default and floor: now - 30 days, "+
					"earlier values are clamped)")).
			QueryParam("until", oas.String(oas.Format("date-time")),
				oas.ParamDescription("Only include sessions with a turn started before this RFC3339 "+
					"timestamp (activity window)")).
			QueryParam("detail", oas.String(oas.Enum("spans", "traces")),
				oas.ParamDescription("Export granularity: spans (default, traces with full spans) or "+
					"traces (turn headers only)")).
			ContentResponse(200, "JSONL body, one JSON object per session with nested traces (and "+
				"spans at detail=spans)", "application/x-ndjson", oas.String()).
			JSONResponse(400, "Malformed since/until, or unrecognized detail",
				parser.Schema(errorResponse{})).
			JSONResponse(500, "Failed to list or render sessions", parser.Schema(errorResponse{})).
			JSONResponse(501, "No database configured for this cassette", parser.Schema(errorResponse{})).
			Build(),
		provenance)

	_ = parser.AddOperation("GET", prefix+"/sessions/{id}",
		oas.NewOperation("exportSession").
			Summary("Export a session as JSONL").
			Description("Returns the session as a single JSON line (downloadable attachment): the "+
				"session object with its traces, each trace carrying its full spans — the same shape as "+
				"GET /v1/sessions/{id}/traces with payload=full. detail=traces exports turn headers "+
				"only (no spans or links).").
			Tag(name).
			PathParam("id", oas.String(), oas.ParamDescription("Session id (UUID)")).
			QueryParam("detail", oas.String(oas.Enum("spans", "traces")),
				oas.ParamDescription("Export granularity: spans (default, traces with full spans) or "+
					"traces (turn headers only)")).
			ContentResponse(200, "JSONL body, one session object with nested traces (and spans at "+
				"detail=spans)", "application/x-ndjson", oas.String()).
			JSONResponse(400, "Missing or malformed id, or unrecognized detail",
				parser.Schema(errorResponse{})).
			JSONResponse(404, "Session not found", parser.Schema(errorResponse{})).
			JSONResponse(500, "Failed to load or render the session", parser.Schema(errorResponse{})).
			JSONResponse(501, "No database configured for this cassette", parser.Schema(errorResponse{})).
			Build(),
		provenance)

	// Compile is a pure function of what was added above, and every Add
	// here is a literal that cannot fail — so an error would mean this
	// function is wrong, not that the request is. Core reports a cassette
	// whose document does not parse, which is the louder and more useful
	// failure than serving an empty body.
	compiled, err := parser.Compile(context.Background(), oas.WithTarget(oas.V30))
	if err != nil {
		return []byte(`{"error":"could not compile this cassette's OpenAPI document: ` +
			strings.ReplaceAll(err.Error(), `"`, `'`) + `"}`)
	}

	return compiled.JSON()
}

// cassetteManifest is the metadata core admits this cassette on. It must stay in
// sync with cassette.toml — two encodings of one schema with the same
// canonical digest.
func cassetteManifest(name string) map[string]any {
	return map[string]any{
		"kind": "cassette/v1alpha1",
		"cassette": map[string]any{
			"name":         name,
			"version":      release.Version,
			"display_name": "Export",
			"description":  "Session export as JSONL: the tapes export surface as a cassette.",
			"license":      "MIT OR Apache-2.0",
			"homepage":     "https://github.com/papercomputeco/export-cassette",
			"image":        "public.ecr.aws/g4e5l3z3/papercomputeco/export-cassette:v" + release.Version,
			"port":         9998,
		},
		"depends": map[string]any{
			"core":  "v1",
			"views": []string{"sessions", "span_links", "span_turns", "spans"},
		},
		"api": map[string]any{
			"health":      "/ping",
			"openapi":     "/openapi",
			"prefix_path": "api",
		},
	}
}
