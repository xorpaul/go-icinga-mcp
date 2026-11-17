# Changelog

All notable changes to this project are documented here. The format is loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). The "version" headings track the in-binary `ServerVersion` constant (`internal/mcpserver/server.go`); the corresponding release tag (used by `go-build-release` for the published Linux/AMD64 binary) is shown in parentheses.

> Versions before v0.4.1 were developed in a private repository; their release tags exist only there. The public repository starts at v0.4.1.

## v0.4.1 — 2026-11-17 (release tag `v0.4.1`)

### Changed

- Updated vendored dependencies: `github.com/modelcontextprotocol/go-sdk` v1.5.0 → v1.8.0, `golang.org/x/oauth2` v0.36.0 → v0.38.0, `golang.org/x/sys` v0.43.0 → v0.50.0; new indirect dependencies `golang.org/x/sync` v0.25.0 and `golang.org/x/time` v0.16.0 (pulled in by the SDK).
- `go.mod` now requires Go 1.26.0 (was 1.25.0), as demanded by the updated `golang.org/x/*` modules.
- Go module path changed from `github.com/icinga/go-icinga-mcp` to `github.com/xorpaul/go-icinga-mcp` for the public GitHub repository.
- Example host wildcard in the `host` parameter description is now `*.example.com`.
- Updated the `go-build-release` submodule to 20425f6 (release assets are now named after the git repository instead of the current directory; the release text is taken from the `CHANGELOG.md` section; existing local tags are pushed).

## v0.4.0 — 2026-07-30 (release tag `v0.4.0`)

### Added

- HTTP transport support: two new `--transport` values `streamable-http` and `sse` alongside the existing default `stdio`. Pass `--port <n>` (default 8080) to set the listen port. The binary starts an `http.Server` on `0.0.0.0:<port>` and shuts it down gracefully on SIGINT/SIGTERM. This enables deployment behind a reverse proxy (e.g. Apache `ProxyPass /mcp-icinga http://127.0.0.1:8082`) without any wrapper script.
- `--transport` and `--port` flags are understood by the `mcp_deploy::go_instance` Puppet defined type, so the systemd unit template picks them up automatically.

## v0.3.1 — 2026-07-28 (release tag `v0.3.1`)

### Fixed

- `list_host_problems` and `list_service_problems` now support `*` / `?` glob wildcards in the `host` and `service_name` parameters. Previously any value containing a wildcard was passed as an exact equality filter (`host.name == "*.example.com"`), which always returned no results. The new `nameFilter` helper detects wildcards and switches to Icinga 2's `match("pattern", field)` DSL function automatically, matching the behaviour already available via the free-form `filter_expr` parameter in `list_hosts` / `list_services`. The `jsonschema` descriptions for both fields are updated to advertise this.

### Added

- New write tool `schedule_downtime`, wrapping Icinga 2's `POST /v1/actions/schedule-downtime`. Supports both host- and service-level downtimes; defaults `start_time` to now, derives `end_time` from a `duration_seconds` shortcut, and falls back to the configured API username for `author`. Fixed downtimes are the default; flexible downtimes require an explicit `duration_seconds`.
- `Client.PostJSON` helper in `internal/icinga/client.go`, mirroring `GetJSON`.
- `schedule_downtime` key in the default endpoint map → `v1/actions/schedule-downtime`.

### Notes

- Icinga 2 r2.14 returns a misleading `404 "No objects found"` when the API user lacks `actions/schedule-downtime`, even when the filter does match objects. Verify the user's permissions at `GET /v1/` before chasing filter or syntax issues. The minimum permission set for the new tool is `actions/schedule-downtime` (and pair it with `actions/remove-downtime` if the operator should be able to cancel downtimes too).

## v0.2.0 — 2026-04-28 (release tag `v0.0.3`)

### Changed

- Retargeted the bridge from Icinga Web 2's Icinga DB Web module at HTTPS/443 to Icinga 2's own REST API on port 5665. The Icinga 2 API is built into every master with the `api` feature enabled and needs no Web 2 plumbing.
- The 10 read tools now hit `/v1/objects/<type>` and pass user filters through verbatim as Icinga 2 DSL (e.g. `host.name == "x"`, `"linux" in host.groups`). Tool-built filters (problem-state, name lookups in detail tools) are AND'd with the user-supplied `filter_expr`.
- `/v1/objects/<type>` does not paginate server-side, so `page` / `limit` are applied client-side after `UnwrapResults` flattens the `{"results":[{...,"attrs":{...}}]}` envelope.
- Config: top-level YAML key renamed `icinga_web:` → `icinga2:`. The old key is still parsed as a fallback so existing deployments keep loading until they're updated. `example.yaml` documents the new shape and points at the Icinga 2 internal CA at `/var/lib/icinga2/certs/ca.crt`.
- Tool-handler glue in `internal/mcpserver/server.go` now uses `Out=any` and returns the JSON-encoded payload via `CallToolResult.Content` (a single `TextContent` block). The previous typed-output shape caused the Go SDK to populate `structuredContent` with `{}`, which clients rendered in place of the real payload.

### Removed

- `list_host_history` and `list_service_history` tools — the Icinga 2 REST API does not stream history events the way Icinga DB Web does. `internal/icinga/timerange.go` was removed with them.

### Documentation

- README gains a Troubleshooting section keyed off the upstream error envelope shape: Icinga Web 2 returns `{"status":"error",...}` while Icinga 2's REST API returns `{"error":401,"status":"..."}`, which makes "wrong base URL" vs. "wrong creds" diagnosable from a single failed call. Also notes the HA-proxy SNAT gotcha (traffic to a VIP arrives at the master with the proxy's source IP).

## v0.1.0 — 2026-04-28 (release tags `v0.0.1`, `v0.0.2`)

### Added

- Initial implementation: a read-only Go port of the Python `icinga-mcp` project, distributed as a single static binary that speaks MCP over stdio. Built against the official MCP Go SDK (`github.com/modelcontextprotocol/go-sdk` v1.5.0).
- 12 read tools targeting Icinga DB Web: `list_hosts`, `list_services`, `list_host_problems`, `list_service_problems`, `get_host_detail`, `get_service_detail`, `list_host_history`, `list_service_history`, `list_hostgroups`, `list_servicegroups`, `list_downtimes`, `list_comments`.
- mTLS client-cert auth via `ICINGA_WEB_CLIENT_CERT` / `ICINGA_WEB_CLIENT_KEY`, for monitoring frontends whose reverse proxy requires a client cert.
- Compatible env-var names with the upstream Python project (`ICINGA_WEB_BASE_URL`, `ICINGA_WEB_USERNAME`, `ICINGA_WEB_PASSWORD`, `ICINGA_WEB_VERIFY_TLS`, `ICINGA_WEB_CA_BUNDLE`, `HTTP_REQUEST_TIMEOUT`, `HTTP_MAX_RETRIES`, `HTTP_RETRY_BACKOFF`) plus the new `ICINGA_WEB_CLIENT_CERT` / `KEY` pair. `.env` in CWD is loaded the same way `pydantic-settings` does it.
- `example.yaml` config template mirroring the loader schema in `internal/config/config.go`.
- `go-build-release` submodule + `.build.cfg` restricting the cross-compile to Linux/AMD64. Releases are produced via `./go-build-release/build_release.sh <version>`. Entry point lives at `./main.go` (project root) to match the layout other repos consuming this submodule use.

### Notes

- Preserves the upstream's two non-obvious URL-encoding quirks so filters round-trip identically: spaces become `%20` (not `+`), and operator-style filter keys ending in `~` (e.g. `name_ci~`) are emitted as `<key><value>` with no `=` separator.
- No FastAPI / REST surface; stdio MCP only.
