---
name: netwatch-observer
description: Check network connectivity, egress IP, NAT type, interface status, host port usage, LAN device discovery, per-app traffic, Linux bridge for VMs, network configuration, notifications, traceroute, and speed tests via Netwatch APIs.
---

## When to use this skill

Call Netwatch when the user's request involves any of these scenarios:

- **Network status**: "Is the network up?", "Can I access the internet?", "Is the connection working?"
- **Website reachability**: "Can I reach example.com?", "What's the latency to that site?", "Check if domestic/global sites are accessible"
- **Egress IP / proxy status**: "What's my IP?", "Am I going through a proxy?", "Where is my exit point?", "What's my NAT type?"
- **Interface & link status**: "Is the wired connection up?", "How's the Wi-Fi signal?", "List NICs / Meta TUN / host bridges"
- **Host ports**: "Which process/app is using this port?", "What ports are occupied on the host?", "Is port 8080 taken?"
- **LAN devices**: "What devices are on the network?", "Is device X online?", "When was device Y last seen?"
- **Per-app traffic**: "Which app uses the most bandwidth?", "How much has app X uploaded/downloaded?", "Traffic ranking"
- **Linux bridge (VM networking)**: "Create a host bridge", "Bridge eth0 for a VM", "Dissolve nw-eth0", "List netwatch bridges"
- **Host network config**: "Switch NIC to DHCP", "Set a static IPv4", "Renew IPv6"
- **Traceroute**: "What's the route to this host?", "Why is it slow to reach X?", "Run a traceroute"
- **Speed test**: "What's my bandwidth?", "What's the LAN transfer rate?"

## Access

Package: `cloud.lazycat.app.netwatch`

Inter-app URL: `http://app.cloud.lazycat.app.netwatch.lzcx`

If `.lzcx` resolution is unavailable in the current runtime, follow the platform's normal application access rules. Do not guess random ports.

Authentication: no upstream authentication is required by the default deployment. Do not add or forward a ticket unless the operator explicitly configured `BASIC_AUTH_*` or `MUTATE_AUTH_*` environment variables.

High-risk mutate paths (`/api/v1/settings`, network config apply/confirm/rollback, host bridge create/confirm/rollback/dissolve, IPv6 renew, container block/unblock) may require `MUTATE_AUTH_*` when the operator enabled mutate auth.

## Recommended call order

1. `GET /healthz` — confirm the app is alive and data is ready. During the first probe it returns HTTP `503` with `{"ready": false}`.
2. `GET /api/v1/summary` — combined snapshot (connectivity, egress IP, NAT, interfaces, etc.).
3. If `summary` is empty or `ready` is `false`, call `POST /api/v1/probe/run` to trigger a probe, then re-read `summary`.
4. Call the narrower endpoints below only when the user needs more detail.
5. For real-time updates, open SSE `GET /api/v1/events?since=<last_notification_id>` instead of polling everything.

## Endpoints

### General

| Method | Path | Description |
|--------|------|-------------|
| GET | `/healthz` | App health, server time, whether probe data is ready |
| GET | `/api/v1/summary` | Combined snapshot: connectivity, egress IP, NAT, interfaces — preferred first call |
| GET | `/api/v1/events?since=<id>` | SSE stream; pushes `summary`, `notification`, `nic_realtime`, `lan_devices`. Use `since` to replay missed notifications after reconnect |
| GET | `/api/v1/capabilities` | Runtime capability flags (app traffic, container control, network config, host bridge, etc.) |

### Website connectivity

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/connectivity/websites` | Reachability, latency, and status code for each target |
| POST | `/api/v1/connectivity/websites/run` | Trigger a website connectivity refresh |

### Egress IP & network identity

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/network` | Full network detail: interfaces, egress IP, geolocation, default routes, platform connectivity |
| POST | `/api/v1/network/interfaces/refresh` | Fast re-collect of host interfaces/routes (no public IP re-query); used after bridge create/dissolve |
| GET | `/api/v1/network/egress-lookups` | Per-provider egress lookup details |
| POST | `/api/v1/network/egress-lookups` | Force refresh egress multi-source lookups and clear public IP cache |
| POST | `/api/v1/network/nat/run` | Trigger NAT type detection |

### Interfaces & realtime throughput

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/network/realtime` | Per-interface realtime throughput (bytes/sec) |
| GET/POST | `/api/v1/network/realtime?force=1` | Force a double sample so bps is immediately usable |

Interface list includes physical ethernet/Wi-Fi, netwatch-managed host bridges (`nw-*`), and proxy TUN interfaces such as mihomo **Meta** when they exist on the host. Do not invent a Meta NIC when the interface is absent.

### Host port usage

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/network/ports` | Host port usage with protocol, state, listen address, process metadata, and Lazycat app/container ownership when available |

### Per-app traffic

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/network/app-traffic` | Raw bridge counters plus per-app live rate, today/month/total usage, and active limits |
| GET | `/api/v1/network/app-traffic/history?app_id=<id>` | Persisted minute-level totals for one app ID |
| POST | `/api/v1/network/app-traffic/limit` | Set app traffic ceiling. Body: `{"app_id":"...","upload_kbps":1024,"download_kbps":4096}`; zero removes a direction limit |

### Container network control

Block or unblock external internet access per app (bridge-level iptables). LAN traffic is preserved.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/containers` | List containers grouped by app bridge; includes `block_mode` (`""` \| `"internet"`) |
| POST | `/api/v1/containers/block` | Block internet. Body: `{"bridge":"br-xxx"}` (`mode` defaults to `internet`) |
| POST | `/api/v1/containers/unblock` | Restore internet. Body: `{"bridge":"br-xxx"}` |

### LAN devices

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/lan/devices` | List discovered LAN devices (status, IP, MAC, vendor, first/last seen) |
| POST | `/api/v1/lan/devices` | Start async scan; returns cached snapshot with `scanning=true` immediately |
| POST | `/api/v1/lan/devices/meta` | Update metadata. Body: `{"mac":"...","note":"...","pinned":true,"ignored":false}` |

### Notifications

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/notifications/events?since=<id>` | Poll notification events since a given ID |
| POST | `/api/v1/notifications/bark/test` | Send a test Bark push |
| POST | `/api/v1/notifications/pushplus/test` | Send a test PushPlus notification |

### Traceroute

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/diagnostics/trace?host=<host>` | Start a traceroute (async task) |
| GET | `/api/v1/diagnostics/trace/task` | Poll intermediate traceroute progress |

### Speed tests

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/speed/config` | Current speed-test configuration |
| GET | `/api/v1/speed/broadband/catalog` | Read public broadband endpoint catalog |
| POST | `/api/v1/speed/broadband/server/start` | Start server-egress-to-internet broadband test |
| GET | `/api/v1/speed/broadband/server/task` | Poll server broadband progress / realtime speed |
| POST | `/api/v1/speed/broadband/server/cancel` | Cancel running server broadband test |
| POST | `/api/v1/speed/broadband/client/result` | Store browser-to-public-internet broadband result |
| GET | `/api/v1/speed/broadband/history` | Broadband history |
| GET | `/api/v1/speed/local/history` | LAN transfer history |
| GET | `/api/v1/speed/local/ping` | Lightweight ping for LAN transfer tests |
| GET | `/api/v1/speed/local/download?sec=<n>` | LAN download payload (by duration) |
| GET | `/api/v1/speed/local/download?mb=<n>` | LAN download payload (by size) |
| POST | `/api/v1/speed/local/upload` | LAN upload target |
| POST | `/api/v1/speed/local/result` | Persist a LAN transfer result |

### History & metrics

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/timeseries?limit=300` | Recent timeseries; `limit` clamped to 1–2000 |
| GET | `/metrics` | Prometheus-style raw metrics |

### Settings & refresh control

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/settings` | All mutable settings (notifications, DND, LAN policy, traffic sampling, etc.) |
| POST | `/api/v1/settings` | **Partial update**: omitted JSON keys keep current values; body is merged onto the existing config then normalized. Always use the response body as source of truth |
| POST | `/api/v1/probe/run` | Trigger a full probe (websites, egress IP, NAT, interfaces) |

Important LAN setting fields:

- `lan_device_auto_remove_days` — offline device auto-remove after N days; `0` disables
- `lan_device_offline_after_sec`, `lan_device_online_after_sec`
- `lan_device_offline_notify_delay_sec`, `lan_device_online_notify_delay_sec`
- `lan_max_check_attempts`, `lan_notify_cooldown_sec`, `lan_flapping_threshold`, `lan_flapping_window_sec`
- `notify_lan_device_change`

Do **not** send a partial settings object and assume missing fields will become defaults. Missing fields are preserved. To change a field, include it explicitly (including `0` / `false` when intentional).

### Network configuration (host NIC IPv4)

Optional host-level feature. Check `/api/v1/capabilities` first. Apply uses a ~3 minute confirm/rollback window.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/network/config/devices` | Configurable host interfaces and current IPv4 settings |
| GET | `/api/v1/network/config/pending` | Pending change + rollback countdown |
| POST | `/api/v1/network/config/check-ip` | Check whether a proposed address is free. Body: `{"device":"...","address":"..."}` |
| POST | `/api/v1/network/config/apply` | Apply IPv4 config with temporary rollback window |
| POST | `/api/v1/network/config/confirm` | Confirm pending config |
| POST | `/api/v1/network/config/rollback` | Roll back pending config |

### IPv6 renew

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/network/ipv6/renew-nics` | List NICs eligible for IPv6 renew (physical + netwatch bridges) |
| POST | `/api/v1/network/ipv6/renew` | Renew IPv6 on a NIC. Body includes target device name |

### Linux bridge (host bridge for VMs)

Creates a real L2 Linux bridge so VMs can share the host LAN and reach the internet without NAT. Requires host privileges (`network_config` / bridge capability).

Rules:

- Bridge name prefix is fixed: `nw-` (e.g. `nw-eth0`). Invalid Linux iface names are rejected.
- Only **ethernet** (including USB wired adapters) can be used as the bridge port. Wi-Fi is not supported.
- One physical NIC → at most one netwatch bridge; multiple NICs may each have their own bridge.
- IPv4 method: `inherit` | `auto` | `manual` (manual needs address/gateway/DNS as appropriate).
- IPv6 method is always **auto** (not user-configurable).
- Create starts a ~3 minute confirm window (same idea as NIC config). Confirm or wait for auto-rollback.
- Creating/dissolving may briefly interrupt connectivity; after reconnect, check `pending` and confirm if still open.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/network/bridges` | List managed bridges, candidates, backend (`nmcli`/`ip`), pending state |
| GET | `/api/v1/network/bridges/pending` | Active pending create + remaining seconds |
| POST | `/api/v1/network/bridges/create` | Create bridge. Body: `{"device":"eth0","bridge":"nw-eth0","method":"inherit"}` |
| POST | `/api/v1/network/bridges/confirm` | Confirm pending create. Body: `{"id":"<rollback_id>"}` |
| POST | `/api/v1/network/bridges/rollback` | Roll back pending create. Body: `{"id":"<rollback_id>"}` |
| POST | `/api/v1/network/bridges/dissolve` | Tear down a managed bridge. Body: `{"bridge":"nw-eth0"}` |

After create/dissolve, prefer `POST /api/v1/network/interfaces/refresh` (or wait for SSE `summary`) before reporting interface state.

## Web pages

| Path | Description |
|------|-------------|
| `/` | Main dashboard: connectivity, egress, NIC detail/realtime, app traffic, host ports, network config + bridge UI |
| `/lan.html` | LAN device discovery and LAN-specific settings |
| `/traffic.html` | Detailed per-app traffic analysis (shown when traffic sampling is enabled) |

## Notes

- **Prefer `summary`**: covers most read-only questions in one call.
- **Data may be stale**: external probes are not always continuous. If outdated or `ready` is false, call `POST /api/v1/probe/run`, then re-read `summary`.
- **SSE reconnect**: always pass the last seen notification id as `?since=` so offline/egress/LAN events are not lost.
- **Settings are merge/patch-style**: omitted keys keep previous values; use the POST response as the canonical settings object.
- **Read-only by default**: GET for queries; POST only for explicit probe/scan/sample/speed/notification/host mutations.
- **Connectivity reporting**: separate domestic vs global targets; quote measured values only.
- **Egress IP reporting**: use returned IP/geo/ISP; do not infer from interface names. Proxy/Meta TUN presence is independent of egress geo.
- **App traffic reporting**: rank by total bytes; bridge counters approximate app egress (host-network and some east-west traffic are incomplete).
- **Host port reporting**: report port/protocol/state and owner as host process or Lazycat app display name; avoid full package IDs unless asked.
- **Container network control**: bridge-level block; system/whitelisted apps cannot be blocked. UI requires the traffic-settings toggle.
- **LAN scans are async**: POST returns immediately with `scanning=true`; poll GET or wait for `lan_devices` SSE.
- **Linux bridge**: for VM L2 networking only; not the same as Docker app bridges used by traffic analysis.
- **Speed-test protection**: broadband/local streams share an eight-stream limit; `429` means honor `Retry-After`.
- **Traceroute is async**: start with GET `.../trace?host=`, poll `.../trace/task`.
- **i18n**: web UI is zh-CN / en from browser locale.
