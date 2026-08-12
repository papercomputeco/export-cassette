# Export cassette

The tapes session export surface as a [cassette](https://github.com/papercomputeco/tapes):
an independently deployed HTTP service tapes admits, republishes, and proxies.
It is a 1:1 port of core's two export endpoints — same query parameters, same
JSONL line bytes, same download filenames, same status codes:

| tapes core                     | this cassette (local)          | via tapes                                |
| ------------------------------ | ------------------------------ | ---------------------------------------- |
| `GET /v1/sessions/export`      | `GET /api/export/sessions`     | `GET /v1/cassettes/export/sessions`      |
| `GET /v1/sessions/{id}/export` | `GET /api/export/sessions/{id}`| `GET /v1/cassettes/export/sessions/{id}` |

Both endpoints take `detail=spans` (default: nested session → traces → full
spans) or `detail=traces` (turn headers only). The bulk export takes
`since`/`until` (RFC3339) with the same 30-day floor clamp core enforces, and
pages internally past the UI list cap.

The cassette is its own Go module. It imports tapes only as a library
(`pkg/tapesoapi` for OpenAPI generation, `pkg/cassette/manifest` in tests);
its runtime contract with core is only HTTP plus the read-only Postgres
credential its deployment supplies.

## Data access

The manifest declares `depends.views = ["sessions", "span_links",
"span_turns", "spans"]` on the `v1` contract, and the queries read exactly
those `tapes_v1.*` views — names that hold stable across tapes'
projection-generation rotations. The example deployment covers them with
`ALTER DEFAULT PRIVILEGES` in [`provision.sql`](provision.sql), since the
views are created by tapes' migrations after init; a production deployment
grants USAGE on `tapes_v1` and SELECT on the declared views directly.

Without `TAPES_DATABASE_URL` the process still starts and serves its anchors;
the export endpoints answer `501`, mirroring core's answer when its driver
lacks the sessions capability.

## Run it

Tapes does not start cassettes, so the example ships the deployment that
starts everything. `compose.yaml` runs Postgres, the tapes API (built from a
sibling checkout of the tapes repository — adjust `build.context` to your
layout), and this cassette:

```sh
docker compose up --build -d
```

Once tapes has resolved the source, on the tapes API port:

```sh
curl localhost:8081/v1/cassettes                                # discovery
curl -OJ localhost:8081/v1/cassettes/export/sessions            # bulk export, JSONL
curl -OJ "localhost:8081/v1/cassettes/export/sessions?detail=traces"
curl -OJ localhost:8081/v1/cassettes/export/sessions/<uuid>     # one session
curl localhost:8081/v1/cassettes/export/openapi.json            # its spec, from core's cache
```

The cassette is also published on `127.0.0.1:9998`, which is worth a look
precisely because it does not know core exists:

```sh
curl localhost:9998/ping
curl localhost:9998/api/export/sessions
```

To point a tapes you already run at the cassette, configure the exact URL of
its metadata-bearing OpenAPI document:

```toml
# .tapes/config.toml
cassettes = ["http://127.0.0.1:9998/openapi"]
```

or without editing the config file:

```sh
tapes serve --cassettes=http://127.0.0.1:9998/openapi
```

## Configuration

| Variable             | Default        | Meaning                                            |
| -------------------- | -------------- | -------------------------------------------------- |
| `CASSETTE_NAME`      | `export`       | Installed name; derives the route and API prefix.  |
| `CASSETTE_LISTEN`    | `0.0.0.0:9998` | Listen address.                                    |
| `TAPES_DATABASE_URL` | *(empty)*      | Read-only DSN for the tapes read model. Empty → export endpoints answer 501. |

## Development

```sh
make help    # all targets
make build   # build/export-cassette
make test    # go vet + go test
```

`go test` pins the builder-checklist invariants: `cassette.toml` and the
manifest embedded in the served OpenAPI document validate and produce the
same canonical digest, and every documented path stays under the declared
prefix.
