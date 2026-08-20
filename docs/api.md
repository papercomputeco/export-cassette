---
title: API reference
description: The two export endpoints, their query parameters, response shape, download filenames, and status codes.
sidebar:
  order: 2
---

Paths below are given on the cassette's own listener. Through tapes, replace
`/api/export` with `/v1/cassettes/export`.

Both endpoints answer `application/x-ndjson` with a `Content-Disposition:
attachment` header, and both take `detail`:

| `detail` | Contents |
| --- | --- |
| `spans` (default) | Traces with their full spans — the nested session → traces → spans projection. |
| `traces` | Turn headers only. No spans, no links. |

An unrecognized `detail` is a `400`.

## `GET /api/export/sessions`

Streams one JSON line per session in a time window, newest first.

| Parameter | Type | Meaning |
| --- | --- | --- |
| `since` | RFC3339 | Only sessions with a turn started at or after this timestamp. Defaults to 30 days ago. |
| `until` | RFC3339 | Only sessions with a turn started before this timestamp. |
| `detail` | `spans` \| `traces` | Export granularity. |

`since` and `until` describe an *activity* window — they filter on when a session's
turns started, not on when the session was created.

The endpoint pages internally, so the result is not truncated at the read API's
session list cap.

Download filenames reflect the window:

```text
sessions-last-30-days-2026-08-20.jsonl        # default window
sessions-2026-07-01-to-2026-08-01.jsonl       # since/until given
sessions-last-30-days-2026-08-20-traces.jsonl # detail=traces
```

## `GET /api/export/sessions/{id}`

Returns one session as a single JSON line.

| Parameter | Type | Meaning |
| --- | --- | --- |
| `id` | UUID, in path | Session id. |
| `detail` | `spans` \| `traces` | Export granularity. |

```text
session-<id>-2026-08-20.jsonl
session-<id>-2026-08-20-traces.jsonl          # detail=traces
```

## Status codes

| Code | When |
| --- | --- |
| `200` | Export streamed. |
| `400` | Malformed `since`/`until`, unrecognized `detail`, or a missing or non-UUID `id`. |
| `404` | No session with that id. Point reads only. |
| `500` | The session could not be loaded. |
| `501` | The cassette has no database configured. See [Deploying](./deploying.md). |

## What a mid-render failure looks like

A half-written JSON line is silently corrupt data, which is worse than an error —
so the per-session export holds the first 8 MiB of a render before committing the
response. Fail inside that budget and you get a clean JSON error. Past it the
headers are already on the wire, and the only remaining failure mode is
truncation.

The budget bounds memory, never the export. Nothing is refused or shortened for
being large: a session past 8 MiB streams to completion with a bounded working
set, and the only thing that changes at the boundary is what a failure would look
like if one happened.

The bulk endpoint does not hold anything. It flushes after each session so bytes
arrive progressively over a long window, and flushing is what committing early
means — so its clean-error window closes after the very first byte.
