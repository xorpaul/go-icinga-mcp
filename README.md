# go-icinga-mcp

A Go MCP (Model Context Protocol) bridge for **Icinga 2's REST API** (port 5665), distributed as a single static binary.

Originally a port of the Python [`icinga-mcp`](https://github.com/Icinga/icinga-mcp), which targets Icinga DB Web. v0.2 retargets at Icinga 2's own REST API directly so the bridge works against installs that don't have the Icinga DB Web module — only the legacy `monitoring` module, or no Icinga Web 2 at all.

## What it does

Speaks MCP over stdio and exposes 11 tools that map to Icinga 2 REST endpoints:

| Tool | Endpoint | Notes |
| --- | --- | --- |
| `list_hosts` | `v1/objects/hosts` | Summary projection by default |
| `list_services` | `v1/objects/services` | Summary projection by default |
| `list_host_problems` | `v1/objects/hosts` (filtered) | `host.state != 0 && host.state_type == 1` |
| `list_service_problems` | `v1/objects/services` (filtered) | `service.state != 0 && service.state_type == 1` |
| `get_host_detail` | `v1/objects/hosts` (filtered) | Full `attrs` by default |
| `get_service_detail` | `v1/objects/services` (filtered) | Full `attrs` by default |
| `list_hostgroups` | `v1/objects/hostgroups` | `name` narrows to one group via `name=="x"` |
| `list_servicegroups` | `v1/objects/servicegroups` | Same as above |
| `list_downtimes` | `v1/objects/downtimes` | |
| `list_comments` | `v1/objects/comments` | |
| `schedule_downtime` | `v1/actions/schedule-downtime` | Schedules a fixed or flexible downtime; accepts host, optional service, start/end times, and comment |

History tools (`list_host_history`, `list_service_history`) and other write actions (acknowledge, comments, reschedule check) are not exposed — Icinga 2's REST API does not stream history, and remaining writes are deferred to a future version.

`page` and `limit` are applied client-side after the request: `/v1/objects/<type>` does not paginate server-side.

## Filter expressions

`filter_expr` on the listing tools is passed through to Icinga 2 as a raw DSL expression. Examples:

- `host.name == "web01"`
- `"linux" in host.groups`
- `service.state == 2 && service.acknowledgement == 0`
- `host.vars.environment == "prod"`

Tool-built filters (problem-state, name-equality lookups in detail tools) are AND'd with the user-supplied `filter_expr`.

## Icinga 2 master setup

1. Enable the `api` feature: `icinga2 feature enable api && systemctl restart icinga2`.
2. Define an API user, e.g. in `/etc/icinga2/conf.d/api-users.conf`:

   ```
   object ApiUser "mcp-user" {
     password = "****"
     permissions = [
       "objects/query/Host",
       "objects/query/Service",
       "objects/query/Hostgroup",
       "objects/query/Servicegroup",
       "objects/query/Comment",
       "objects/query/Downtime",
       "actions/schedule-downtime",  // optional: required for schedule_downtime tool
       "actions/remove-downtime",    // optional: remove downtimes via the API
     ]
   }
   ```

3. Reload icinga2. The REST API now answers on `https://<master>:5665/`.

If `bind_host` on the `api` feature is `127.0.0.1`, port 5665 is loopback-only — open it (or run the MCP on the master itself).

## Configuration

The binary reads a YAML config file. By default it looks for `config.yaml` **next to the binary itself** (resolved via the executable path, not the current working directory). Pass `-config /path/to/file.yaml` to override.

A fully commented template lives at [`example.yaml`](example.yaml). Copy it to `config.yaml` next to the binary and edit:

```bash
cp example.yaml config.yaml
$EDITOR config.yaml
```

Schema (top-level keys, all optional unless noted):

```yaml
icinga2:
  base_url: https://monitoring.example.com:5665   # required
  username: mcp-readonly
  password: change-me
  verify_tls: true                # default: true
  ca_bundle: /var/lib/icinga2/certs/ca.crt        # optional; Icinga 2's internal CA
  client_cert: /path/to/cert.pem  # optional, cert auth — must be signed by Icinga 2 CA
  client_key:  /path/to/key.pem   # optional, paired with client_cert

http:
  request_timeout_sec: 15
  max_retries: 3
  retry_backoff_sec: 0.5

summary_fields:                   # optional; built-in defaults apply if omitted
  host:     [...]
  service:  [...]
  comment:  [...]
  downtime: [...]
```

`config.yaml` is gitignored — it normally contains secrets. Configs from v0.1 using `icinga_web:` as the top-level key still load (the field is read as a fallback) but you should rename to `icinga2:` and update `base_url` to the Icinga 2 master on port 5665.

## Build and run

```bash
go build -o go-icinga-mcp .
./go-icinga-mcp                          # uses <binary-dir>/config.yaml
./go-icinga-mcp -config /etc/go-icinga-mcp.yaml
```

The server reads MCP JSON-RPC on stdin and writes responses to stdout. It is meant to be launched by an MCP client.

## Register with Claude Code

```bash
claude mcp add --scope user go-icinga-mcp /absolute/path/to/go-icinga-mcp
```

The binary resolves `config.yaml` relative to its own location, so the MCP client can launch it from any CWD without a wrapper script. If you keep the config elsewhere, register it with the flag instead:

```bash
claude mcp add --scope user go-icinga-mcp /absolute/path/to/go-icinga-mcp -- -config /etc/go-icinga-mcp.yaml
```

## Smoke test

```bash
( printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"x","version":"0"}}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'; sleep 1 ) \
  | ./go-icinga-mcp
```

Should print two JSON-RPC responses: `initialize` (server name + protocol version) and `tools/list` (11 tools).

## Troubleshooting

The error shape on a failing tool call usually tells you which layer is broken:

- **`upstream 404 Not Found: {"status":"error","message":"Page not found."}`** — that JSON envelope is Icinga **Web 2**'s router, not Icinga 2's REST API. `base_url` is pointing at Icinga Web 2 (e.g. `…/icingaweb2`). Switch it to the Icinga 2 master on port 5665.
- **`upstream 401 {"error":401,"status":"Unauthorized..."}`** — that envelope (numeric `error` field) is Icinga 2's native format, so routing and TLS are fine. The username/password don't match an `ApiUser` block in `api-users.conf`. Same shape with HTTP 403 means the user exists but is missing one of the `objects/query/*` permissions.
- **`dial tcp …:5665: connect: connection refused`** — the master is reachable but nothing on its external interface accepts. Check, in order: (1) `icinga2 feature list | grep api`; (2) `ss -tlnp | grep 5665` — if it's bound to `127.0.0.1` only, set `bind_host = "0.0.0.0"` in `/etc/icinga2/features-enabled/api.conf`; (3) host firewall.
- **`dial tcp …:5665: i/o timeout`** — packets are being dropped silently. Almost always a network firewall between the MCP host and the master.

If your master sits behind an HA proxy (e.g. `monitoring-live` fronts `monitoring-live-bs01`), traffic to the VIP gets SNAT'd, and source-IP firewall ACLs on the master won't see the MCP host's IP. Point `base_url` at a backend node directly.

## Layout

```
main.go                          entry point, stdio transport
internal/config/config.go        YAML loader; endpoint table; summary defaults
internal/icinga/client.go        net/http client w/ TLS, Basic auth, retry; UnwrapResults helper
internal/icinga/services.go      filter composition, client-side pagination
internal/icinga/project.go       dotted-path projection (summary mode)
internal/mcpserver/server.go     MCP tool registration
```

## License

GPL-2.0-only, matching the upstream Python project.
