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
-- Tapes does not yet publish tapes_v1.* contract views, so the grants target
-- the physical read-model tables those views will front. They are created by
-- tapes' migrations AFTER this init script runs, which is why the grant is an
-- ALTER DEFAULT PRIVILEGES on the tapes role rather than a GRANT on tables
-- that do not exist yet: every table the tapes role later creates in `public`
-- arrives readable by the cassette. When the contract views land, this
-- becomes per-view GRANT SELECT statements and nothing else changes.
--
-- SELECT only: the cassette is a pure read surface. It declares no tables and
-- gets no schema of its own.
--
-- Postgres runs this exactly once, when the data directory is first
-- initialized. A stale volume will skip it: `docker compose down -v` to reset.

CREATE ROLE "cassette_export" LOGIN PASSWORD 'cassette';

GRANT USAGE ON SCHEMA public TO "cassette_export";

ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO "cassette_export";
