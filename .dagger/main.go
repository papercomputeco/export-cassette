// Export cassette checks. Container images are built directly from Dockerfile.
package main

import (
	"context"

	"dagger/export-cassette/internal/dagger"
)

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

// goContainer is the Go toolchain every check in this module runs in.
func (m *ExportCassette) goContainer() *dagger.Container {
	return dag.Container().
		From("golang:1.26-bookworm").
		WithEnvVariable("CGO_ENABLED", "0").
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("go-build")).
		WithWorkdir("/src").
		WithDirectory("/src", m.Source)
}

// Test runs the unit suite, as a check so it joins the ones the go and
// golangcilint toolchains contribute rather than being a second CI mechanism
// beside them.
//
// The name is what makes the reported check `export-cassette:test`, matching
// `search-cassette:test` and `skills-cassette:test` — the check name is the
// function name, so the three cassettes only read alike if they are declared
// alike.
//
// -count=1 because a check that can be satisfied from the test cache is not
// running the thing it claims to run; the mounted build cache is what keeps
// that affordable.
//
// +check
func (m *ExportCassette) Test(ctx context.Context) (string, error) {
	return m.goContainer().
		WithExec([]string{"go", "test", "-count=1", "./..."}).
		Stdout(ctx)
}
