// Export cassette CI/CD.
package main

import (
	"context"

	"dagger/export-cassette/internal/dagger"
)

const imageName = "export-cassette"

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

func (m *ExportCassette) image() *dagger.Dockerimage {
	return dag.Dockerimage(dagger.DockerimageOpts{Source: m.Source})
}

// BuildImage builds the local-platform export cassette image.
func (m *ExportCassette) BuildImage() *dagger.Container {
	return m.image().Build()
}

// BuildPushImage builds and publishes the multi-platform export cassette image.
func (m *ExportCassette) BuildPushImage(
	ctx context.Context,

	// Registry namespace, for example public.ecr.aws/example/papercomputeco.
	registry string,

	// Tags to publish, for example ["v1.0.0", "latest"].
	tags []string,
) ([]string, error) {
	return m.image().Publish(ctx, registry+"/"+imageName, tags)
}
