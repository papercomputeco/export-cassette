# export-cassette

Session export for [tapes](https://tapes.dev): a whole time window of sessions, or
one session by id, streamed as JSONL.

It is a [cassette](https://tapes.dev/docs/cassettes/) — an independently deployed
HTTP service that tapes discovers from its OpenAPI document and reverse-proxies
under `/v1/cassettes/export`. It is a pure read surface: it owns no tables, writes
nothing, and holds a read-only credential on the tapes read model.

**Documentation:** [tapes.dev/docs/export](https://tapes.dev/docs/export/) — the API
reference, deployment and database grants, and development.

## Run it

Tapes does not start cassettes, so `compose.yaml` ships the deployment that starts
everything: Postgres, the tapes API (built from a sibling checkout of the tapes
repository — adjust `build.context` to your layout), and this cassette.

```sh
docker compose up --build -d
```

Once tapes has resolved the source, on the tapes API port:

```sh
curl localhost:8081/v1/cassettes                                # discovery
curl -OJ localhost:8081/v1/cassettes/export/sessions            # bulk export, JSONL
curl -OJ "localhost:8081/v1/cassettes/export/sessions?detail=traces"
curl -OJ localhost:8081/v1/cassettes/export/sessions/<uuid>     # one session
```

The cassette is also published on `127.0.0.1:9998`, which is worth a look precisely
because it does not know tapes exists:

```sh
curl localhost:9998/ping
curl localhost:9998/api/export/sessions
```

## Develop

```sh
make help    # every target
make build   # build/export-cassette
make test    # go vet + go test
```

## License

Dual-licensed under either of

- Apache License, Version 2.0 ([LICENSE-APACHE](LICENSE-APACHE))
- MIT license ([LICENSE-MIT](LICENSE-MIT))

at your option. Unless you explicitly state otherwise, any contribution
intentionally submitted for inclusion in the work by you, as defined in the
Apache-2.0 license, shall be dual licensed as above, without any additional
terms or conditions.
