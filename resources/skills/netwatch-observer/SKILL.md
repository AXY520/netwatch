---
name: netwatch-observer
description: Read network status from the Netwatch app, including connectivity, egress IP, NAT, interfaces, realtime throughput, history, and diagnostics.
---

Use this skill when the user wants current network status from the Netwatch app running on Lazycat MicroServer.

Netwatch package:

`cloud.lazycat.app.netwatch`

Primary base URL:

`http://app.cloud.lazycat.app.netwatch.lzcx`

If direct `.lzcx` resolution is unavailable in the current runtime, follow the platform's normal application access rules. Do not guess random ports.

Authentication:

- If the current app/runtime requires delegated user access for inter-app HTTP requests, forward the real user's ticket according to Lazycat's normal application-to-application access rules.
- If the Netwatch app is configured with HTTP Basic Auth, include the configured credentials. Otherwise, no extra auth is required.

Read-only rule:

- Prefer GET endpoints.
- Do not call mutation endpoints such as refresh, settings update, or speed-test start/cancel unless the user explicitly asks for an active operation.

Recommended read order:

1. Call `/healthz` first to confirm the app is alive and whether probe data is ready.
2. Call `/api/v1/summary` for the main dashboard view.
3. Only call narrower endpoints when the user asks for more detail or when a specific field is missing from `summary`.

Primary endpoints:

- `GET /healthz`
  Returns service status, current server time, and whether probe data is ready.
- `GET /api/v1/summary`
  Best first choice. Returns the combined snapshot for website connectivity, network info, egress IP, NAT, and related status.
- `GET /api/v1/connectivity/websites`
  Returns website reachability and latency details.
- `GET /api/v1/network`
  Returns network information details.
- `GET /api/v1/network/realtime`
  Returns realtime interface throughput stats for monitored NICs.
- `GET /api/v1/network/egress-lookups`
  Returns cached egress lookup details.
- `GET /api/v1/timeseries?limit=300`
  Returns recent timeseries history.
- `GET /api/v1/settings`
  Returns current mutable settings.
- `GET /api/v1/speed/config`
  Returns current speed-test configuration.
- `GET /api/v1/speed/broadband/history`
  Returns broadband speed-test history.
- `GET /api/v1/speed/local/history`
  Returns local transfer speed-test history.
- `GET /api/v1/diagnostics/trace/task`
  Returns current trace task state if a diagnostic trace was started earlier.
- `GET /metrics`
  Returns Prometheus-style metrics if raw metrics are needed.

Use active operations only with explicit user intent:

- `POST /api/v1/probe/run`
  Trigger a full refresh.
- `POST /api/v1/connectivity/websites/run`
  Refresh website connectivity only.
- `POST /api/v1/network/nat/run`
  Refresh NAT detection only.
- `GET /api/v1/diagnostics/trace?host=<host>`
  Run a network trace for a target host.
- `POST /api/v1/speed/broadband/start`
  Start a background broadband speed test.
- `GET /api/v1/speed/broadband/task`
  Poll broadband task progress.
- `POST /api/v1/speed/broadband/cancel`
  Cancel a running broadband speed test.
- `POST /api/v1/speed/broadband/run`
  Run a synchronous broadband speed test.

Response handling guidance:

- Treat `summary` as the source of truth for a concise user-facing answer.
- If `healthz.ready` is `false`, explain that Netwatch is still warming up and data may be incomplete.
- When reporting connectivity, distinguish domestic and global targets if the response separates them.
- When reporting NAT or egress details, quote the actual observed values instead of inferring from interface names.
- For realtime throughput, summarize the busiest interfaces and direction rather than dumping every counter unless the user asks for raw data.

Answer style:

1. Start with the current overall network state from `summary`.
2. Add the key findings the user asked for, such as egress IP, NAT type, unreachable targets, or busiest NIC.
3. Mention if data is stale, incomplete, or still being refreshed.
