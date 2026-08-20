// Package release carries the identity a released image reports.
//
// The version is not maintained by hand. The release stamps the tag it is
// publishing into Version at link time, and a source tree always reads
// Placeholder, because a source tree is not a release. cassette.toml declares
// the same placeholder, so the manifest's two encodings still canonicalize to
// one digest.
//
// This lives outside package main on purpose. `-ldflags -X` addresses a
// variable by import path, and package main is addressed as "main" when the
// linker builds a binary but by its real import path when it is compiled as
// the package under test — so a symbol in main needs two different flag
// strings and the release could only ever verify one of them. Here one flag
// string works in both, which is what lets the release prove the exact stamp
// it ships (see .dagger/main.go).
package release

// Version is the release identity: the manifest's version, the tag in its
// image reference, and the OpenAPI info block.
//
// It must stay a package-level var. -X writes to variables only and silently
// does nothing to a constant, which would ship every image reporting the
// placeholder while the build, the tests, and the manifest digest all stayed
// green.
var Version = Placeholder

// Placeholder is what an unstamped build reports. A version no release can
// produce is the point: it reads as "this came from a source tree", where a
// plausible number would read as a release that never happened.
const Placeholder = "0.0.0"
