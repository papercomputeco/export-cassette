---
title: Export cassette
description: Exports tapes sessions as JSONL, one line per session, at either full-span or turn-header granularity.
sidebar:
  order: 1
---

`export-cassette` serves session exports: a whole time window of sessions, or one
session by id, streamed as JSONL. It is a [cassette](https://tapes.dev/docs/cassettes/)
— an independently deployed HTTP service that tapes discovers from its OpenAPI
document and reverse-proxies under the tapes namespace.

It is a pure read surface. It owns no tables, writes nothing, and holds a read-only
credential on the tapes read model.

## The two addresses

Every cassette has two, and both are worth understanding because they fail
differently. The cassette serves its API under a local prefix on its own listener;
tapes republishes that API under `/v1/cassettes/<name>`:

| On the cassette's own listener | Through tapes |
| --- | --- |
| `GET /api/export/sessions` | `GET /v1/cassettes/export/sessions` |
| `GET /api/export/sessions/{id}` | `GET /v1/cassettes/export/sessions/{id}` |

Clients use the tapes address. The local one is what tapes itself talks to, and
what you curl when you want to know whether a problem is the cassette or the
proxying in front of it — the cassette does not know tapes exists.

`/ping` and `/openapi` sit outside the prefix. They are the anchors tapes probes
and fetches, not part of the proxied API.

## What an export contains

Both endpoints take `detail`:

- `detail=spans` (default) — the nested session → traces → full spans projection.
- `detail=traces` — turn headers only, no spans or links.

Each output line is one session. Responses are `application/x-ndjson` and arrive as
a download attachment, so a `traces`-grain file is distinguishable from a full one
on disk by its `-traces.jsonl` suffix.

The bulk endpoint defaults to the trailing 30 days and pages internally, so it is
not bounded by the read API's session list cap.

## Next

- [API reference](./api.md) — parameters, responses, and status codes.
- [Deploying](./deploying.md) — image, configuration, and the database grant it needs.
