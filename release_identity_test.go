package main

import (
	"os"
	"testing"

	"github.com/papercomputeco/export-cassette/internal/release"
)

// TestReleaseIdentityIsStamped proves that `-ldflags -X` actually reaches
// release.Version, which is the one thing the stamping scheme cannot take on
// faith: -X writes to package-level variables only, and silently does nothing
// when its target is a constant, is renamed, or moves package. Any of those
// refactors would keep compiling, keep passing every other test, and ship
// images that report the placeholder version forever — the failure this
// scheme exists to prevent, reintroduced by the mechanism meant to fix it.
//
// The release runs this with the same flag it builds the binary with (see
// .dagger/main.go) and refuses to publish when it fails. An ordinary `go
// test ./...` skips it: without the flag there is no stamp to check.
//
// It is a plain test rather than a Ginkgo spec so the pipeline can select it
// by name with -run; the Ginkgo suite entry point does not match that filter.
func TestReleaseIdentityIsStamped(t *testing.T) {
	want := os.Getenv("CASSETTE_VERSION_WANT")
	if want == "" {
		t.Skip("unstamped build: no CASSETTE_VERSION_WANT to check against")
	}
	if release.Version != want {
		t.Fatalf("release identity did not reach the binary: release.Version = %q, want %q\n"+
			"the -X flag names internal/release.Version; check it is still a package-level var there",
			release.Version, want)
	}
	if release.Version == release.Placeholder {
		t.Fatalf("release identity is the placeholder %q; a release must stamp a real version", release.Placeholder)
	}
}
