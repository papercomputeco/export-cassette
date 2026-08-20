---
title: Deploying
description: The image, its configuration, the database grant the cassette needs, and how to point a running tapes at it.
sidebar:
  order: 3
---

Tapes does not start cassettes. A deployment starts the process, supplies its
configuration and credentials, and tells tapes where to find its OpenAPI document.

```text
public.ecr.aws/g4e5l3z3/papercomputeco/export-cassette:v<version>
```

Tags are the published release versions — see
[releases](https://github.com/papercomputeco/export-cassette/releases). Pin one;
`nightly` exists but is not a release.

The version is not a number anyone maintains. A release stamps the tag it is
publishing at link time, and the manifest version, the image reference the
manifest advertises, and the OpenAPI info block all derive from it. A source
build reports `0.0.0`, because a source tree is not a release and a
plausible-looking number there would describe one that never happened.

It listens on `9998` by default and serves `/ping`, `/openapi`, and its API under
`/api/export`.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `CASSETTE_NAME` | `export` | Installed name. Derives the route, the API prefix, and the database role. |
| `CASSETTE_LISTEN` | `0.0.0.0:9998` | Listen address. |
| `TAPES_DATABASE_URL` | *(empty)* | Read-only DSN for the tapes read model. |

Without `TAPES_DATABASE_URL` the process still starts and serves its anchors, and
the export endpoints answer `501`. That is deliberate: a cassette that cannot reach
its data should still be discoverable, so the failure reads as "not configured"
rather than "not deployed."

## Database access

The manifest declares what this cassette needs, and the deployment provides it —
core reads the declaration, publishes it, and grants nothing:

```toml
[depends]
core = "v1"
views = ["sessions", "span_links", "span_turns", "spans"]
```

Those are the `tapes_v1.*` contract views. The names hold stable across tapes'
projection-generation rotations, which is why a cassette reads them rather than the
physical tables underneath.

The role name is derived, not chosen: `cassette_` + the installed name, so
`cassette_export`. A production deployment grants exactly:

```sql
GRANT USAGE ON SCHEMA tapes_v1 TO "cassette_export";
GRANT SELECT ON tapes_v1.sessions, tapes_v1.spans,
                tapes_v1.span_turns, tapes_v1.span_links
  TO "cassette_export";
```

SELECT only. This cassette declares no tables and gets no schema of its own.

`provision.sql` in the repository is the example-deployment equivalent, and it takes
a shortcut worth knowing about: Postgres runs init scripts once, before tapes has
migrated, so the `tapes_v1` views do not exist yet and cannot be granted by name. It
uses `ALTER DEFAULT PRIVILEGES` instead, which covers whatever tapes creates later.
That is wider than the grant above, and the width is the price of a single-pass init
script.

**Upgrading an existing volume.** A volume provisioned before the `tapes_v1` views
existed carries public-schema-only privileges, and the init script will not run
again to widen them — reads fail with `permission denied`. Apply the explicit grant
above once, after tapes has migrated. It is idempotent. Do not reach for
`docker compose down -v`: that deletes the database, raw turns and read model
included.

## Pointing tapes at it

Tapes needs the exact URL of the metadata-bearing OpenAPI document:

```toml
# .tapes/config.toml
cassettes = ["http://127.0.0.1:9998/openapi"]
```

or without editing the config:

```sh
tapes serve --cassettes=http://127.0.0.1:9998/openapi
```

Confirm it resolved:

```sh
curl localhost:8081/v1/cassettes
curl -OJ localhost:8081/v1/cassettes/export/sessions
```
