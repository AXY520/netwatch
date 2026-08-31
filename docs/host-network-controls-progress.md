# Host 网络控制与应用流量统计进度

更新时间：2026-08-31<br>
当前分支：`feat/host-network-controls`

本文总结当前分支对应用流量统计、限制网速、禁用外网和应用代理的实现状态。详细技术说明见 [app-traffic-controls.html](./app-traffic-controls.html)。

## 一、总体目标

- `app_id` 表示应用本身，`instance_id` 表示具体用户实例；单实例时两者保持相同以兼容旧数据和 API。
- 根据运行时拓扑，把策略展开到 Bridge 网桥或 Host cgroup。
- 分离期望策略、内核实际状态和能力限制。
- 应用重启、网桥重建和 Netwatch 重启后，尽可能恢复状态。

统一的是 API、策略、事务和状态模型，不强行让 Bridge 与 Host 使用同一种内核技术。

## 二、已完成

### 2.1 统一网络控制模型

已接入 `AppNetworkTarget`、`AppNetworkPolicy` 和 `AppNetworkPolicyStatus`：

- Bridge 目标为 `lzc-br-*` 网桥。
- Host 目标为 `host-app:<instance_id>`，运行时关联该用户实例的 cgroup。
- Mixed 应用可以同时发现 Bridge 和 Host 两类目标。
- API 支持字段级修改限速、外网或代理策略。
- 多目标更新先检查能力，再逐目标执行；失败时回滚，成功后持久化。
- 联合修改限速和外网策略时，最后一步持久化失败也会完整恢复防火墙、内存中的 `BlockedApps`、内核限速和持久化限速；回滚失败会和原始错误一起返回，不再只记录日志。

### 2.2 禁用外网

- 权威状态为 `BlockedApps`，即 `instance_id -> internet`；单实例的 key 仍是原 `app_id`。
- 旧 `BlockedBridges` 仅作为迁移来源。
- Bridge 使用宿主机 `iptables/ip6tables` 和 Netwatch 双缓冲 chain。
- Host 使用宿主机 `OUTPUT` 链的 cgroup 匹配规则。
- Host 控制受“Host 网络控制（实验性）”开关控制，流量统计不受影响。
- 移除了删除容器默认路由的断网 fallback。
- 服务启动、Docker 生命周期变化和周期 reconcile 会重建规则。
- `internet_in_sync`、目标 `blocked` 和 `diagnostic` 表示最近一次实际核验结果。

### 2.3 Bridge 限制网速

纯 Bridge 应用的限速已可用：

- 下载使用 root TBF，固定 handle `194:`。
- 上传使用 ingress `clsact`、局域网 bypass filter 和 `police` filter。
- 上传规则使用 `conform-exceed drop/ok`，正常包放行，超限包丢弃。
- 修改和取消会清理 Netwatch 自有优先级下的重复或旧规则。

### 2.4 Host/Mixed 限制网速

实验开关开启时，Host 和 Mixed 应用使用物理默认出口上的共享 TC 分类器：

- `cgroup/sock_create` 给应用父 slice 中新建的 Host socket 写入 socket-local-storage；懒猫的 system slice 和顶层 exec slice 会同时附着。
- 物理网卡 ingress 通过 socket lookup 识别 Host 下载，通过 egress 建立的反向流记录识别 NAT 后的 Bridge 下载；每应用一份 eBPF token bucket。
- 物理网卡 egress 只负责给 Host/Bridge 上传包写入应用 mark；同一应用的一条原生 `fw + act_police` 规则提供共享上传预算，避免 eBPF 在 GSO 下重复发放额度。
- Mixed 应用只调用一次 Host/Mixed limiter，不再保留独立的 Bridge TBF/police，因此不会产生第二份预算。
- IPv4/IPv6、TCP/UDP 共用该分类路径；RFC1918、loopback、IPv4/IPv6 link-local 和 IPv6 ULA 不进入公网限速。
- 只添加 `clsact` 和 Netwatch 保留优先级 `49160/49161`，不替换物理网卡 root qdisc；mark 只使用并清理掩码 `0x0fff0000` 内的位。
- 每应用独立程序/map 实例，Mixed 的 Host/Bridge 共享该实例；最多同时支持 4095 个 Host/Mixed 限速应用。

### 2.5 限速实际状态核验与漂移修复

限速器区分三类状态：

- `desired`：持久化的期望限速。
- `applied`：最近一次被 `tc` 接受的规则，只用于回滚。
- `in_sync`：最近一次读取内核规则后的实际同步结果。

后台首次发现网桥或策略变化时立即核验，稳定状态下约每 20 秒读取一次 `tc`。规则缺失、速率不一致、动作错误或网桥同名重建时会自动修复。API/前端可读取 `limit_in_sync`、`limit_state` 和 `diagnostic`。

### 2.6 应用流量统计

Bridge 统计读取 `/sys/class/net/<bridge>/statistics/`，使用网桥名维护 baseline；网桥重建或计数回退时只重建对应 baseline。

Host 统计使用 `cgroup_skb` eBPF：

- ingress/egress 分别写入 RX/TX map。
- map key 为 cgroup ID，value 为字节数和包数。
- collector 为每个 Host 容器输出独立计数记录。
- 状态层按 cgroup 路径维护独立 baseline，再按 `instance_id` 聚合。
- 一个 Host 容器重启或计数归零，不会丢失同应用其他容器的增量。
- Bridge 与 Host 混合应用分别计算增量后再聚合。

### 2.7 Host eBPF map 持久化

Host 统计 map 优先 pin 到宿主机 bpffs：

```text
/sys/fs/bpf/netwatch/netwatch_cgrp_rx
/sys/fs/bpf/netwatch/netwatch_cgrp_tx
```

Netwatch 重启后会复用已有 map。如果未挂载 bpffs、bpffs 不可写或 pin 失败，则降级为临时 map，并在 Host 目标 diagnostic 中提示重启会重置计数。eBPF 程序和 cgroup attachment 仍由当前进程管理，服务退出时主动解除，避免挂住已删除应用的 cgroup。

### 2.8 Host eBPF 生命周期清理

- attachment 同时记录 cgroup 路径和 cgroup ID，可识别“路径相同但 cgroup inode 已变化”的复用场景。
- 获取到完整运行时 cgroup 清单后，关闭已停止或被替换 cgroup 的 link，并删除 RX/TX map 中不再属于任何活动 Host 容器的 key。
- 任一活动 Host 容器的应用归属或 cgroup 解析失败时跳过整轮清理，避免在不完整清单下误删有效计数。
- link 关闭失败时保留未关闭的引用，供后续 reconcile 重试；清理错误不会被静默吞掉。

### 2.9 应用列表和历史

- 活动列表只展示当前仍有运行容器的应用。
- 历史数据按 `instance_id` 持久化（单实例即 `app_id`），旧版 Bridge 历史支持迁移。
- Bridge baseline 的旧存储键保持兼容。
- 应用实时刷新只更新实时速率展示，不重建整行交互状态。
- 控制实现和边界已同步写入 [app-traffic-controls.html](./app-traffic-controls.html)。

## 三、当前能力矩阵

| 网络模式 | 流量统计 | 禁用外网 | 限制网速 | 设置代理 |
| --- | --- | --- | --- | --- |
| 纯 Bridge | 支持 | 支持 | 支持 | 支持 |
| 纯 Host | cgroup eBPF，真机已验证 | 实验性开关开启后支持 | 实验性开关开启后支持，真机已验证 | 实验性开关开启后支持 |
| Mixed | Bridge + Host 分别统计后聚合，真机已验证 | Bridge/Host 分别控制 | 实验性开关开启后共享应用预算，真机已验证 | Bridge/Host 分别代理 |

## 四、已知边界

### 4.1 Host 统计依赖环境

需要宿主机 cgroup v2、可解析的容器 cgroup 路径、PID/proc/cgroup/sysfs 挂载、BPF capability、内核 eBPF 支持和可用的 `cgroup_skb` attach。依赖不满足时，快照会携带诊断，Bridge 统计不受影响。

### 4.2 Netwatch 停止期间

map 持久化只保留已经累计的计数。cgroup attachment 不 pin，因此 Netwatch 完全停止期间不会产生新的 Host 统计；这样可以避免永久持有已删除容器的 cgroup。目标盒子的 `/sys/fs/bpf` 当前只是普通 sysfs 目录而非 bpffs，因此本机使用临时 map，Netwatch 重启会归零；API 已明确暴露该降级状态。

### 4.3 Host/Mixed 限速

Host/Mixed 限速是 policing，不是 shaping：TCP 会通过重传和拥塞控制接近目标速率，UDP 超限包会直接丢弃，短流还会受到 128 KiB burst 影响。下载按物理 ingress `skb.len` 计费，上传由原生 `act_police` 计费；数值是应用级公网总预算，不是每容器预算。

当前只选择一个默认出口设备；IPv4 与 IPv6 默认路由若位于不同设备会拒绝启用。多 WAN、策略路由、VPN/tunnel、IPv6 扩展头和出口切换仍是明确边界。取消策略会删除所有应用 filter、attachment 和 map；空的 `clsact` 可以保留，因为它不替换也不改变原有 root qdisc。

## 五、验证记录

当前分支已通过：

```text
go test ./...
go test -race ./internal/probe ./internal/api
go vet ./...
go build ./...
node --check web/app-traffic-dashboard.js
node scripts/web-module-smoke.js
git diff --check
```

测试覆盖限速规则创建/清理/漂移修复、多 Host cgroup baseline、计数回退、持久化重载、Bridge + Host 混合聚合、Host 实验性开关、bpffs 识别、失效 attachment/map key 清理和联合策略完整回滚。

### 5.1 懒猫真机验证（盒子 `error`，Netwatch 0.9.9）

- 最终测试镜像为 `sha256:7a860db83aab389949f4c4f86784e1ce8449f819fc48132ad7d7de096adcd9de`，实例和代理均处于 running/healthy。
- cgroup v2、BPF 程序加载、ingress/egress attachment 均成功，Host 记录来源为 `cgroup_skb_ebpf`。
- AI Pod 和 FluxDown 的 Host RX/TX 原始计数持续增长；AI Pod 的空闲 Host cgroup 经一次只读 HTTP 请求后从 `0/0` 增至 RX `2264` / TX `448`。
- 对 FluxDown 的 Host HTTP 端口执行 100 次只读请求，RX 增加 `289682` 字节、TX 增加 `103062` 字节，RX/TX 包数分别增加 `4213` / `597`。
- `/api/v1/network/ports` 能把 Host 监听端口正确归属到 AI Pod 和 FluxDown 容器。
- 重装 Netwatch 前后应用 cgroup 路径不变且其他应用未被重启，但计数归零；检查确认 `/sys/fs/bpf` 与 `/host/sys/fs/bpf` 均为 sysfs 而非 bpffs。这验证了临时 map 降级路径，不能把本机记录为“跨重启持久化通过”。
- 宿主 iptables/ip6tables 是指向 `/etc/alternatives/` 的绝对符号链接。能力检测已改为不跟随容器根中的链接；防火墙命令同时使用 `nsenter -C` 进入 PID 1 的 cgroup namespace，避免 `xt_cgroup --path` 在容器 cgroup namespace 中解析失败并返回 `RULE_APPEND ... Invalid argument`。
- FluxDown 真机执行“禁用外网”成功，Bridge/Host 目标均为 `blocked=true`、整体 `internet_in_sync=true`。Host cgroup 规则实测丢弃 IPv4 `438` 包 / `34943` 字节和 IPv6 `16` 包 / `1264` 字节，同时放行 172.16/12 私网 `115` 包；恢复外网后策略、持久化状态及 IPv4/IPv6 cgroup 规则均已清空。
- 懒猫上的容器主进程位于 `system.slice/runc-lzc-os.scope/lzcapp.slice/...`，但 `lzc-docker exec` 创建的进程位于顶层 `lzcapp.slice/...`。Host 禁网现在同时覆盖两棵真实存在的应用 slice；复测 `lzc-docker exec ... curl baidu.com` 连接超时，exec-tree DROP 规则命中 `5` 包，同时 172.16/12 与 loopback 流量继续放行。
- 没有停止或重启用户应用。使用独立临时 Host 容器完成生命周期真机验证：停止前 cgroup ID `153510` 的 ingress/egress link 和 RX/TX map key 均存在；停止后活动应用条目消失、两条 link FD 关闭、两个 map key 均为 `absent`。临时容器、检查器和空父 slice 已清理。

### 5.2 被淘汰的 cgroup_skb 限速原型

- 第一版把 token bucket 直接放在应用父 slice 的 `cgroup_skb` ingress/egress，证明了多个 Host 子 cgroup 和顶层 exec slice 可以共享状态。
- 该 hook 在 GRO/GSO 下的 `skb.len` 与应用实际字节不稳定对应，verifier 又禁止访问 `wire_len/gso_segs`；Host + Bridge 并发 1 Mbps 时应用层约收到 `10.22 MB/20s`，而 BPF 只观察到约 `4.15 MB`。
- 因此这版代码已被删除，没有作为产品 fallback 保留。保留它只会形成第二套无法精确计费的限速实现。

### 5.3 正式 Host/Mixed 限速真机验证

目标机为 Linux `6.5.0-0.deb12.4-amd64`，正式测试镜像为 `sha256:7d697be8c180983b931da8ed6cd588d2de3d4b18248a87fb0df1433820b5f735`。测试使用临时 Mixed 应用 `netwatch.hostlimit.probe`，未修改或重启 FluxDown：

- 正式 `sock_create + TC ingress/egress` 程序通过 verifier；`enp2s0` 只新增 `clsact`，原生 `mq + fq_codel` root qdisc 完整保留。
- ingress/egress BPF filter 使用 pref `49160`；上传 `fw + act_police` 使用 pref `49161`，内核读回 `1Mbit / 128Kb / mtu 64Kb / drop/pipe`，API 为 `limit_in_sync=true`。
- 1 Mbps 单流下载：Host `2097152` 字节 / `16.926s`，约 `991 Kbit/s`；Bridge `2097152` 字节 / `17.068s`，约 `983 Kbit/s`。
- 1 Mbps Mixed 并发下载：Host 与 Bridge 各 `1 MiB`，总完成时间 `19.548s`；两条流量竞争同一 bucket，而不是各自获得 1 Mbps。
- 1 Mbps 单流上传：Host 约 `971 Kbit/s`，Bridge 约 `995 Kbit/s`。Mixed 并发各上传 `1 MiB`，总完成时间 `16.804s`，聚合约 `998 Kbit/s`；同一 police 规则累计处理两类流量。
- loopback 读取 `8 MiB` 用时 `0.071s`，未进入公网预算。限速热更新为 2 Mbps 后，Host 下载约 `2.001 Mbit/s`。
- 手动删除 ingress BPF filter 后，周期 reconcile 自动恢复；重启 Netwatch 后 program ID 更新，持久化的 2 Mbps filter/police 和 `limit_in_sync=true` 自动恢复。
- 将双向速率清零后，pref `49160/49161`、应用 attachment/map 和持久化 limit 均被清理。
- 测试结束后删除了临时容器、网络、两棵父 slice、应用历史和测试 `clsact`；最终 `enp2s0` 仅剩原始 `mq + fq_codel`，生产环境无探针残留。

## 六、Host/Mixed 限速结论

1. Host/Mixed 限速已进入产品路径，但仍受“Host 网络控制（实验性）”开关控制；开关关闭时能力为 false，并清理已有 Host/Mixed 限速状态。
2. Mixed 的 Host 与 Bridge 共用一个下载 eBPF bucket 和一条上传原生 police，避免重复预算；纯 Bridge 仍沿用网桥 TBF/police。
3. 方案是 policing，TCP 会近似收敛，UDP 会丢弃超限包；不承诺无丢包 shaping，也不承诺短流瞬时速率等于配置值。
4. 当前只支持 IPv4/IPv6 默认路由位于同一出口设备的拓扑；多 WAN、策略路由、VPN/tunnel、IPv6 扩展头和出口切换需要后续单独设计。
5. bpffs 挂载仍只影响 Host 流量统计 map 的跨重启累计，不是 Host/Mixed 限速的依赖；Netwatch 不会自行修改宿主机全局 mount namespace。

## 七、每应用独立代理上游（已完成）

### 7.1 结论与原问题

每个应用使用不同的 HTTP/SOCKS5 上游可以实现，不需要新增 eBPF、内核模块或第三方依赖。Bridge、Host 和 Mixed 目标在进入代理规则生成前已经能关联到 `app_id`，因此可以继续复用现有目标发现、禁用外网优先级、双缓冲防火墙和失败回滚机制。

改造前的单上游原型存在三个根问题：

- `app_proxy` 只保存一份全局上游，`proxy_apps` 只记录应用是否启用。
- 所有应用都被重定向到固定本地端口 `23089`，该端口只有一个 `appProxyAdapter` 和一份当前配置。
- 弹窗先修改全局上游、再启用应用，是两个请求；修改一个应用会影响其他已代理应用，中间失败也不能形成完整事务。

这些问题已修正。代理认证、域名上游、规则分流仍属于后续扩展，不在当前版本范围内。

### 7.2 已落地模型

- `app_proxy` 保留为全局默认值，只用于新应用第一次设置代理时预填，不再改变已经配置的应用。
- 新增 `app_proxy_configs`，保存 `instance_id -> AppProxySettings`；现有 `proxy_apps` 继续只表示启用状态。这样“恢复直连”后仍能保留该实例上次填写的地址。
- 旧数据中已启用但没有独立配置的应用，在加载时复制当时的全局 `app_proxy`，保证升级后行为不变。
- “启用”通过一次应用策略请求同时提交 `proxy_enabled` 和代理配置；配置校验、规则切换、内存状态和持久化沿用现有事务回滚路径。
- 应用流量响应返回该应用保存的代理配置；弹窗优先显示应用配置，没有时才显示全局默认值。
- 设置页不展示全局代理项；后端默认值只用于未配置应用的弹窗预填，不会热切换已配置应用。

### 7.3 实现方式

1. 将 `appProxyAdapter` 的监听端口参数化，由控制器为每个启用代理的应用维护一个适配器和唯一内部端口。
2. 规则生成时按目标所属 `app_id` 使用对应端口；HTTP 只重定向 TCP，SOCKS5 重定向 TCP/UDP，其余公网协议继续失败关闭。
3. 应用修改代理时先启动新适配器，再用现有双缓冲 chain 切换规则；成功后关闭旧适配器，失败则保留旧规则和旧适配器。
4. 应用恢复直连时删除其重定向规则并关闭适配器，但保留上次填写的应用配置。
5. 当前保持“一应用一适配器”。只有实际出现大量代理应用导致监听 socket 成为问题时，才合并使用相同上游的适配器。

### 7.4 验证结果

- 本地通过 `go test ./...`、`go test -race ./internal/probe ./internal/api`、`go vet ./...`、`go build ./...`、前端语法/模块 smoke 和 `git diff --check`。
- 单测覆盖旧 `app_proxy + proxy_apps` 数据迁移、两个应用生成不同内部端口、HTTP 不重定向 UDP、SOCKS5 重定向 TCP/UDP，以及两个适配器不能复用监听端口。
- 真机镜像为 `sha256:5cf07ae2937c8a92f3d67c5eaa5973a0085aaa2a6fe46605099fae37f911aea3`。
- `cloud.lazycat.app.testflight` 使用 HTTP 上游时只生成 TCP 重定向，初始内部端口为 `35895`；`cloud.lazycat.networkdiagnostic` 使用 SOCKS5 上游时生成 TCP/UDP 重定向，内部端口为 `45091`。
- 修改 TestFlight 的上游后，其端口从 `35895` 切换到 `34883`，Network Diagnostic 始终保持 `45091`；恢复 TestFlight 直连后，Network Diagnostic 的规则仍保持不变。
- 重装 Netwatch 后，Network Diagnostic 的启用状态、SOCKS5 类型、IP 和端口完整恢复，`proxy_state=proxied`、`proxy_in_sync=true`。
- 验证结束后两个应用均恢复原默认配置并恢复直连；所有代理 NAT REDIRECT 和 filter 内容规则已清空，Firefox 原有 `internet_state=blocked` 未被改变。
- 最终收尾镜像为 `sha256:8c16270bc432045c798e90c2c071c62def8b5457524ce2e86fc16e1d922b21fe`；生产静态资源回读确认设置页不再显示全局代理项，应用弹窗不再显示 HTTP/UDP 说明，提交按钮为“启用”。

### 7.5 代理兼容性补充与最终真机复测

- 宿主机 DHCP 地址变化时，加载配置会先用旧默认值迁移已启用但尚无独立配置的应用，再把新应用预填地址刷新为当前宿主地址；本机地址从 `192.168.3.174` 变化到 `192.168.3.192` 后已验证。
- 用户填写宿主机自身 IP 时，适配器优先连接 `127.0.0.1` / `::1`，失败才回退用户填写的地址，避免容器访问宿主地址时遇到 hairpin 限制；远程代理地址不受影响。
- nftables 兼容层可能让 UDP `REDIRECT` 返回重定向后的本地地址。SOCKS5 UDP 会从当前 network namespace 的 `/proc/net/nf_conntrack` 恢复原始目的地址和 reply tuple，并用 `IP_PKTINFO` / `IPV6_PKTINFO` 指定回包源地址；仅在确认为本地重定向且 flow 暂不可见时最多查询 4 次、间隔 3ms。
- HTTP 与 SOCKS5 TCP 在 Host、Bridge 上均成功读取百度 `robots.txt`（`2814` 字节）；SOCKS5 UDP 在 Bridge、Host 上均完成 DNS 查询。最终 Host 首次 DNS 查询一次成功，日志未再出现 `nf_conntrack has no redirected UDP flow`。
- UDP 真机验证镜像为 `sha256:ca02d5c7e4dea979b61c0ccf3d15c2f4308931e1920011186428dce4d483a149`；把 conntrack 查询进一步收窄到本地重定向分支后的最终部署镜像为 `sha256:431c8a98cf28901eb640e399363d15f0d47a236278d4fe8a83c0c31939a3a69e`。临时 `netwatch.proxy.bridgeprobe` 在复测后立即恢复直连，容器、应用策略和两棵父 slice 均已清理。

### 7.6 后续顺序

1. 先观察实际使用反馈，确认 IP + 端口、无认证 HTTP/SOCKS5 是否覆盖主要场景。
2. 有明确需求后再评估代理鉴权和主机名上游。
3. 连通性检测和规则分流保持 YAGNI，不因本次改造提前实现。

## 八、网卡 MAC 地址配置（已完成）

### 8.1 交互与恢复设计

- “修改配置”窗口中 MAC 使用独立标签，与 IP 和 DNS 操作分开；表单同时展示当前 MAC、永久硬件 MAC 和新 MAC。
- 修改只提交 `mac_only=true`，不会改动 IPv4、网关或 DNS。以太网/网桥使用 NetworkManager 的 `802-3-ethernet.cloned-mac-address`，Wi-Fi 使用 `802-11-wireless.cloned-mac-address`。
- 应用前保存连接配置中的原始 cloned MAC；修改后必须在 3 分钟内确认，否则沿用统一网络事务自动回滚。手动回滚、校验失败回滚和服务重启恢复都使用同一快照。
- “恢复原始 MAC”使用内核报告的永久硬件地址，而不是猜测厂商地址或把当前地址当原始值。设备无法提供永久地址时按钮禁用，避免写入错误地址。
- MAC、IP、DNS 和网桥共享单一网络变更事务域；任一变更待确认时，其他高风险网络操作保持锁定。

### 8.2 真机验证

- 未修改生产主链路 `enp2s0`。验证期间其当前 MAC 始终为 `aa:aa:aa:aa:aa:08`，永久硬件 MAC 始终为 `04:2b:58:14:fd:1e`。
- 在隔离的临时 NetworkManager 连接 `netwatch-mac-probe` / veth `nwmac0` 上完成验证：自定义 MAC 立即生效，必选 `interface_exists`、`link_up`、`mac_address` 检查均通过。
- 手动回滚成功恢复测试网卡初始地址；第二轮未确认变更在 3 分钟到期时自动触发 `rollback_start` / `rollback`，运行态地址恢复且 pending 清空。
- 第三轮确认后自定义 MAC 保持生效；随后通过同一 MAC-only 路径恢复测试网卡初始地址并确认。
- 测试结束后已删除临时 NetworkManager 连接、`nwmac0/nwpeer0`、辅助容器和辅助程序；生产环境没有测试 pending 任务或探针残留。

## 九、多实例应用（已部署并完成真机验收）

- 官方 `application.multi_instance: true` 会为每个用户启动独立容器。Netwatch 优先使用 `app_id + lzcapp.user-id` 构造稳定 `instance_id`；旧运行时缺少 `user-id` 且同应用出现多个 Compose project 时，使用 project 兜底。
- 单实例仍使用原 `app_id` 作为统计和策略 key；多实例使用如 `cloud.lazycat.app.downloader@user:axy` 的 key。
- Bridge/Host 发现、流量 baseline/历史、限速、禁网、代理、Host/Mixed limiter 和生命周期事件均已改为按 `instance_id` 聚合与执行。
- API 接受 `app_id + instance_id`。旧客户端只传 `app_id` 时，当前只有一个实例则继续兼容；同时发现多个实例则返回明确错误，防止误操作所有用户。
- 前端将多实例拆成独立行，显示为“应用名（用户）”，菜单、限速窗口和代理窗口全部携带对应 `instance_id`。
- 升级时，旧 `app_id` 限速/禁网/代理策略会先复制给当前已发现的每个实例，持久化成功后再删除模糊的基础 key；未来新用户实例不会意外继承旧策略。
- 旧的应用级聚合历史无法可靠拆分，因此保留在磁盘但不映射到某个用户；实例历史从首次识别到的当前计数建立。
- 本地单测已覆盖两个下载器用户的身份传播、统计隔离、模糊 API 拒绝、目标选择和旧策略迁移。
- Host 多实例只在各用户的 cgroup 父级确实不同时开放变更型控制。如果运行时把两个用户放在同一父级，统计仍按容器隔离，但限速/禁网/代理能力会失效关闭并给出 diagnostic，避免误伤另一用户。

### 9.1 生产部署验收（2026-08-31）

- 通过 `deploy.sh` 将 `cloud.lazycat.app.netwatch` 0.9.9 部署到默认生产盒子，运行镜像更新为 `sha256:940c5e0daca10b782f91884af540358c01be6aa1d1ac95ac81f581656abf4d15`；app、netwatch 和 proxy 服务均进入 Running/healthy。
- 懒猫下载器已拆为 `cloud.lazycat.app.downloader@user:axy` 和 `cloud.lazycat.app.downloader@user:damn`；分别只绑定 `lzc-br-896bc2d5` 与 `lzc-br-2ce090b4`，不再把两个用户的目标合并到一条策略中。
- 原应用级 10000/10000 Kbit/s 限速成功迁移到两个实例，两个实例均报告 `limited` 且 `limit_in_sync=true`；禁网和代理继续保持未启用。
- 两个实例分别建立独立历史采样；省略 `instance_id` 访问下载器历史返回 400 和明确的多实例错误，不会模糊选择用户。
- 启动日志没有策略迁移或控制恢复错误；容器列表中没有 `netwatch.proxy.probe` 或 `netwatch.proxy.bridgeprobe` 残留。本轮只读验收未主动执行断网、代理或限速扰动测试。
