// Export cassette CI/CD.
package main

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"dagger/export-cassette/internal/dagger"
)

const imageName = "export-cassette"

// versionSymbol is the variable the release stamps. Kept beside the flag that
// uses it so the Dockerfile and this module cannot drift apart silently.
const versionSymbol = "github.com/papercomputeco/export-cassette/internal/release.Version"

// versionFlag is the linker flag that stamps a release identity, identical to
// the one Dockerfile passes.
func versionFlag(version string) string {
	return "-X " + versionSymbol + "=" + version
}

// releaseTag matches a version that names a real release: the manifest
// advertises its own image as ":v<version>", so only this shape can be
// stamped without the manifest naming a reference that was never published.
var releaseTag = regexp.MustCompile(`^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)*$`)

// manifestStamp reports whether a publish is a release whose identity belongs
// in the manifest, and returns the bare version to stamp.
//
// Nightly and dev builds publish tags like "nightly", which is neither a
// version the manifest's schema accepts nor a tag that ":v"+it would resolve
// to. They keep the placeholder for the same reason a source tree does: they
// are not releases, and a manifest claiming otherwise sends an orchestrator
// to an image that does not exist.
func manifestStamp(version string) (string, bool) {
	bare := strings.TrimPrefix(version, "v")
	if !releaseTag.MatchString(bare) {
		return "", false
	}
	return bare, true
}

type ExportCassette struct {
	// +private
	Source *dagger.Directory
}

func New(
	// Project source directory.
	// +defaultPath="/"
	// +ignore=[".git", ".dagger", ".direnv", ".devenv", "build", "tmp"]
	source *dagger.Directory,
) *ExportCassette {
	return &ExportCassette{Source: source}
}

// image builds the cassette image, stamped with the release identity when one
// is given. version is the tag being released ("v0.2.1"); empty leaves the
// binary's placeholder, which is what a build from a source tree should
// report — it is not a release.
func (m *ExportCassette) image(version string) *dagger.Dockerimage {
	image := dag.Dockerimage(dagger.DockerimageOpts{Source: m.Source})
	bare, ok := manifestStamp(version)
	if !ok {
		return image
	}
	return image.WithBuildArg("CASSETTE_VERSION", bare)
}

// goContainer is the Go toolchain every check in this module runs in. Sharing
// it is what keeps the suite and the release's stamp verification provably on
// the same Go: two copies of these lines could drift onto different versions
// and the drift would show up as a release-time failure with no local
// reproduction.
func (m *ExportCassette) goContainer() *dagger.Container {
	return dag.Container().
		From("golang:1.26-bookworm").
		WithEnvVariable("CGO_ENABLED", "0").
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("go-build")).
		WithWorkdir("/src").
		WithDirectory("/src", m.Source)
}

// CheckTest runs the unit suite, as a check so it joins the ones the go and
// golangcilint toolchains contribute rather than being a second CI mechanism
// beside them.
//
// -count=1 because a check that can be satisfied from the test cache is not
// running the thing it claims to run; the mounted build cache is what keeps
// that affordable.
//
// +check
func (m *ExportCassette) CheckTest(ctx context.Context) (string, error) {
	return m.goContainer().
		WithExec([]string{"go", "test", "-count=1", "./..."}).
		Stdout(ctx)
}

// verifyStamp proves the release identity reaches the binary before anything
// is published. `-X` writes to package-level variables only and silently does
// nothing to a constant or a renamed symbol, so a refactor could otherwise
// publish images reporting the placeholder forever — with the build, the
// tests, and the manifest digest all still green.
//
// versionFlag below is byte-identical to the one the Dockerfile builds with,
// which is the whole reason the version lives outside package main: a symbol
// in main is addressed as "main.X" when linking a binary but by its real
// import path when compiled as the package under test, so a stamp there could
// only ever be verified through a different flag than the one that ships.
func (m *ExportCassette) verifyStamp(ctx context.Context, version string) error {
	_, err := m.goContainer().
		WithEnvVariable("CASSETTE_VERSION_WANT", version).
		WithExec([]string{
			"go", "test", "-count=1", "-run", "TestReleaseIdentityIsStamped",
			"-ldflags", versionFlag(version), ".",
		}).
		Sync(ctx)
	return err
}

// BuildImage builds the local-platform export cassette image. It is
// deliberately unstamped: a local build is not a release, and an image that
// claims a version it was never published under is the drift the stamping
// exists to prevent.
func (m *ExportCassette) BuildImage() *dagger.Container {
	return m.image("").Build()
}

// BuildPushImage builds and publishes the multi-platform export cassette image.
func (m *ExportCassette) BuildPushImage(
	ctx context.Context,

	// Registry namespace, for example public.ecr.aws/example/papercomputeco.
	registry string,

	// Tags to publish, for example ["v1.0.0", "latest"].
	tags []string,

	// Release identity to stamp, for example "v1.0.0". Empty publishes an
	// unstamped image, which only a non-release path should want.
	// +optional
	version string,
) ([]string, error) {
	if bare, ok := manifestStamp(version); ok {
		// The manifest will advertise ":v<bare>", so that exact tag has to be
		// among the ones being published — otherwise the manifest names an
		// image reference that does not exist. This is what catches a release
		// tagged "0.3.0" rather than "v0.3.0": it would publish ":0.3.0"
		// while the manifest advertised ":v0.3.0".
		advertised := "v" + bare
		if !slices.Contains(tags, advertised) {
			return nil, fmt.Errorf(
				"manifest will advertise image tag %q, which is not among the tags being published (%v): release tags must be v-prefixed",
				advertised, tags)
		}
		if err := m.verifyStamp(ctx, bare); err != nil {
			return nil, err
		}
	}
	return m.image(version).Publish(ctx, registry+"/"+imageName, tags)
}
