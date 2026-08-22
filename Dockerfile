# The export cassette image.
#
# The build context is this repository. The cassette is its own module and
# depends on tapes only as a library (pkg/tapesoapi for OpenAPI generation);
# its runtime contract with core is only HTTP plus the Postgres credential its
# deployment supplies.
#
#   docker build -t tapes/export-cassette:0.1.0 .
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

# Dependencies first, so editing the cassette does not re-resolve the module
# graph on every rebuild.
COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
COPY internal/ internal/

# The release identity this image reports, stamped into the manifest it
# serves. Left unset the binary keeps internal/release's placeholder, which is
# what a build from a source tree should say: it is not a release. The release
# pipeline passes the tag it is publishing.
ARG CASSETTE_VERSION=0.0.0

# CGO off gives a static binary, which is what lets the final stage be
# distroless: nothing in this cassette needs libc.
ENV CGO_ENABLED=0
RUN GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath \
      -ldflags="-s -w -X github.com/papercomputeco/export-cassette/internal/release.Version=${CASSETTE_VERSION}" \
      -o /out/export-cassette .

FROM gcr.io/distroless/static-debian12:nonroot

# Matches cassette.port in cassette.toml and in the OpenAPI document's
# x-tapes-cassette metadata. The three are declared separately on purpose: this
# line is what the image does, and the other two are metadata — one for whoever
# deploys the image, one for core.
EXPOSE 9998

# Bind the wildcard address inside the container's own network namespace. The
# deployment publishes it to 127.0.0.1 on the host, so this is not an exposure —
# binding loopback here would make the published port unreachable.
ENV CASSETTE_LISTEN=0.0.0.0:9998

COPY --from=build /out/export-cassette /usr/local/bin/export-cassette

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/export-cassette"]
