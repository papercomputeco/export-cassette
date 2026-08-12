-- Deployment-side provisioning for the export cassette.
--
-- This file is the deployment holding up its end of the manifest. The cassette
-- declares in cassette.toml that it depends on the v1 contract views
-- (sessions, span_links, span_turns, spans); core reads that declaration,
-- publishes it, and does nothing about it. Creating the role and granting the
-- reads is somebody else's job, and in this example that somebody is Postgres'
-- own init hook.
--
-- The role name is not invented here. It is what the manifest derives:
--
--   role = "cassette_" + name  ->  cassette_export
--
-- The tapes_v1 schema and its contract views are created by tapes'
-- migrations AFTER this init script runs, so neither can be GRANTed by name
-- here. Default privileges bridge the gap: everything the tapes role later
-- creates — the tapes_v1 schema (ON SCHEMAS covers USAGE) and the views in
-- it (ON TABLES covers views) — arrives readable by the cassette. A
-- production deployment (tko) instead grants USAGE on tapes_v1 and SELECT
-- on exactly the declared views once they exist; this example-deployment
-- shortcut is wider than that, and the width is the price of a single-pass
-- init script.
--
-- SELECT only: the cassette is a pure read surface. It declares no tables and
-- gets no schema of its own.
--
-- Postgres runs this exactly once, when the data directory is first
-- initialized. A stale volume skips it, and the only thing that re-runs it
-- is destroying the volume — see the upgrade note below before reaching
-- for `docker compose down -v`.
--
-- UPGRADING AN EXISTING VOLUME: a volume provisioned before the tapes_v1
-- contract views carries public-schema-only privileges, and this script
-- will not run again to widen them — the cassette's reads fail with
-- permission denied. Apply the one-time grant (after tapes has migrated,
-- so the views exist); it is idempotent and loses nothing:
--
--   GRANT USAGE ON SCHEMA tapes_v1 TO "cassette_export";
--   GRANT SELECT ON tapes_v1.sessions, tapes_v1.spans, tapes_v1.span_turns,
--       tapes_v1.span_links TO "cassette_export";
--
-- `docker compose down -v` also "fixes" it, by deleting the database —
-- raw turns, sessions, and the whole read model included. That is for
-- throwaway instances only, never an upgrade path.

CREATE ROLE "cassette_export" LOGIN PASSWORD 'cassette';

ALTER DEFAULT PRIVILEGES FOR ROLE tapes GRANT USAGE ON SCHEMAS TO "cassette_export";

ALTER DEFAULT PRIVILEGES FOR ROLE tapes GRANT SELECT ON TABLES TO "cassette_export";
