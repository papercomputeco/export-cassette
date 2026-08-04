package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/papercomputeco/tapes/pkg/cassette"
	"github.com/papercomputeco/tapes/pkg/cassette/manifest"
)

// contracts is the set of core contracts this cassette is written
// against, mirrored from the manifest's depends.core.
var contracts = []cassette.ContractVersion{"v1"}

// TestManifestCopiesAgree pins the builder-checklist invariant: the
// authored cassette.toml and the copy embedded in the served OpenAPI
// document are two encodings of one schema, so both must validate and
// produce the same canonical manifest digest.
func TestManifestCopiesAgree(t *testing.T) {
	declared, err := manifest.Load("cassette.toml")
	if err != nil {
		t.Fatalf("load cassette.toml: %v", err)
	}
	if err := declared.Validate(contracts); err != nil {
		t.Fatalf("validate cassette.toml: %v", err)
	}
	declaredDigest, err := declared.Digest()
	if err != nil {
		t.Fatalf("digest cassette.toml: %v", err)
	}

	var doc struct {
		Extension json.RawMessage `json:"x-tapes-cassette"`
		Paths     map[string]any  `json:"paths"`
	}
	if err := json.Unmarshal(openAPIDocument(defaultName), &doc); err != nil {
		t.Fatalf("parse served OpenAPI document: %v", err)
	}
	if len(doc.Extension) == 0 {
		t.Fatal("served OpenAPI document carries no x-tapes-cassette extension")
	}
	embedded, err := manifest.Parse(doc.Extension)
	if err != nil {
		t.Fatalf("parse embedded manifest: %v", err)
	}
	if err := embedded.Validate(contracts); err != nil {
		t.Fatalf("validate embedded manifest: %v", err)
	}
	embeddedDigest, err := embedded.Digest()
	if err != nil {
		t.Fatalf("digest embedded manifest: %v", err)
	}

	if declaredDigest != embeddedDigest {
		t.Fatalf("manifest digests diverge: cassette.toml %s != embedded %s",
			declaredDigest, embeddedDigest)
	}

	// Core refuses a document whose paths escape the declared prefix;
	// catch that here rather than at admission.
	prefix := "/api/" + defaultName + "/"
	for path := range doc.Paths {
		if !strings.HasPrefix(path, prefix) {
			t.Errorf("documented path %q is outside the declared prefix %q", path, prefix)
		}
	}
}
