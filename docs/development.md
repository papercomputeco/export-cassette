---
title: Development
description: Build and test from a checkout, and what the test suite pins about the manifest.
sidebar:
  order: 4
---

```sh
make help    # every target
make build   # build/export-cassette
make test    # go vet + go test
make image   # build and load the container image via Dagger
make check   # the Dagger checks CI runs
```

The cassette is its own Go module. It depends on tapes only as a library —
`pkg/tapesoapi` to generate the OpenAPI document, `pkg/cassette/manifest` in tests.
At runtime its contract with tapes is HTTP plus the read-only Postgres credential
its deployment supplies, and nothing else.

## What the tests pin

`cassette.toml` and the manifest embedded in the served OpenAPI document are two
encodings of one schema. The suite parses both and asserts they produce the same
canonical manifest digest, so they cannot drift apart silently — a change to one
that is not made to the other fails the build rather than shipping a cassette whose
published metadata disagrees with its authored metadata.

The suite also asserts every documented path stays under the declared prefix. A
route outside it would be served by the process but never proxied by tapes, which
is the kind of defect that looks fine locally and 404s through core.
