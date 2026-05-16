---
name: netwatch-observer
description: Check network connectivity (domestic & global websites), egress IP & geolocation, NAT type, interface status, per-app traffic stats, traceroute, and speed tests.
---

## When to use this skill

Call Netwatch when the user's request involves any of these scenarios:

- **Network status**: "Is the network up?", "Can I access the internet?", "Is the connection working?"
- **Website reachability**: "Can I reach example.com?", "What's the latency to that site?", "Check if domestic/global sites are accessible"
- **Egress IP / proxy status**: "What's my IP?", "Am I going through a proxy?", "Where is my exit point?", "What's my NAT type?"
- **Interface & link status**: "Is the wired connection up?", "How's the Wi-Fi signal?", "What's the link speed?"
- **Per-app traffic**: "Which app uses the most bandwidth?", "How much has app X uploaded/downloaded?", "Traffic ranking"
- **Traceroute**: "What's the route to this host?", "Why is it slow to reach X?", "Run a traceroute"
- **Speed test**: "What's my bandwidth?", "What's the LAN transfer rate?"

## Access

Package: `cloud.lazycat.app.netwatch`

Inter-app URL: `http://app.cloud.lazycat.app.netwatch.lzcx`

If `.lzcx` resolution is unavailable in the current runtime, follow the platform's normal application access rules. Do not guess random ports.

Authentication: forward the real user's ticket according to Lazycat's inter-app access rules. Netwatch itself requires no extra auth.

## Recommended call order

1. `GET /healthz` — confirm the app is alive and data is ready.
2. `GET /api/v1/summary` — combined snapshot (connectivity, egress IP, NAT, interfaces, etc.).
3. If `summary` is empty or `ready` is `false`, call `POST /api/v1/probe/run` to trigger a probe, then re-read `summary`.
4. Call the narrower endpoints below only when the user needs more detail.

## Endpoints

### General

| Method | Path | Description |
|--------|------|-------------|
| GET | `/healthz` | App health, server time, whether probe data is ready |
| GET | `/api/v1/summary` | Combined snapshot: connectivity, egress IP, NAT, interfaces — preferred first call |
| GET | `/api/v1/events` | SSE stream; pushes `summary_update` events after probe cycles |

### Website connectivity

Check whether domestic and global websites are reachable, plus their latency.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/connectivity/websites` | Reachability, latency, and status code for each target |
| POST | `/api/v1/connectivity/websites/run` | Trigger a website connectivity refresh |

### Egress IP & network identity

View egress IPv4/IPv6, geolocation, ISP, and NAT type.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/network` | Full network detail: interfaces, egress IP, geolocation, default routes, platform connectivity |
| GET | `/api/v1/network/egress-lookups` | Per-provider egress lookup details |
| POST | `/api/v1/network/nat/run` | Trigger NAT type detection |

### Interfaces & realtime throughput

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/network/realtime` | Per-interface realtime throughput (bytes/sec) |

### Per-app traffic

Cumulative upload/download traffic grouped by bridge/app identity.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/network/app-traffic` | Per-app cumulative traffic, sorted by total bytes |

### Traceroute

Run a traceroute to a target host. Returns each hop's IP, latency, and geolocation.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/diagnostics/trace?host=<host>` | Start a traceroute; returns the full result when complete |
| GET | `/api/v1/diagnostics/trace/task` | Poll an in-progress trace task for intermediate results |

### Speed tests

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/speed/config` | Current speed-test configuration |
| POST | `/api/v1/speed/broadband/start` | Start a background broadband speed test |
| GET | `/api/v1/speed/broadband/task` | Poll broadband test progress and realtime speed |
| POST | `/api/v1/speed/broadband/cancel` | Cancel a running broadband test |
| POST | `/api/v1/speed/broadband/run` | Run a synchronous broadband test (blocks until done) |
| GET | `/api/v1/speed/broadband/history` | Broadband speed-test history |
| GET | `/api/v1/speed/local/history` | LAN transfer speed-test history |
| GET | `/api/v1/speed/local/ping` | Lightweight ping endpoint for LAN transfer tests |
| GET | `/api/v1/speed/local/download?sec=<n>` | LAN download payload (by duration) |
| GET | `/api/v1/speed/local/download?mb=<n>` | LAN download payload (by size) |
| POST | `/api/v1/speed/local/upload` | LAN upload target |
| POST | `/api/v1/speed/local/result` | Persist a LAN transfer test result |

### History & metrics

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/timeseries?limit=300` | Recent timeseries history |
| GET | `/metrics` | Prometheus-style raw metrics |

### Settings & refresh control

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/settings` | Current settings (read-only) |
| GET | `/api/v1/auto-refresh` | Auto-refresh state (enabled, interval seconds) |
| GET | `/api/v1/settings/refresh-interval` | Current refresh interval |
| POST | `/api/v1/probe/run` | Trigger a full probe (website checks, egress IP, NAT, etc.) |
| POST | `/api/v1/auto-refresh` | Set auto-refresh. Body: `{"enabled": true, "interval_sec": 30}` |
| POST | `/api/v1/settings/refresh-interval` | Update refresh interval. Body: `{"interval_sec": 30}` |

## Notes

- **Prefer `summary`**: it's the combined snapshot and covers most use cases in a single call.
- **Data may be stale**: Netwatch does not auto-refresh external probes by default. If `summary` is outdated or `ready` is `false`, call `POST /api/v1/probe/run` first.
- **Read-only by default**: use GET for queries; only use POST when explicitly triggering a probe or speed test.
- **Connectivity reporting**: distinguish domestic vs. global targets; quote observed values, not inferences.
- **Egress IP reporting**: use the returned IP and geolocation directly; do not infer from interface names.
- **App traffic reporting**: highlight the largest consumers by total bytes; note the sort basis (upload/download/total).
- **Traceroute is async**: `GET /diagnostics/trace?host=<host>` starts a background task; use `/diagnostics/trace/task` to poll intermediate progress.
