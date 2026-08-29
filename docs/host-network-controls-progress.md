# Host 网络控制与应用流量统计进度

更新时间：2026-08-29<br>
当前分支：`feat/host-network-controls`

本文总结当前分支对应用流量统计、限制网速和禁用外网的实现状态。详细技术说明见 [app-traffic-controls.html](./app-traffic-controls.html)。

## 一、总体目标

- 以 `app_id` 作为应用级策略和展示主键。
- 根据运行时拓扑，把策略展开到 Bridge 网桥或 Host cgroup。
- 分离期望策略、内核实际状态和能力限制。
- 应用重启、网桥重建和 Netwatch 重启后，尽可能恢复状态。

统一的是 API、策略、事务和状态模型，不强行让 Bridge 与 Host 使用同一种内核技术。

## 二、已完成

### 2.1 统一网络控制模型

已接入 `AppNetworkTarget`、`AppNetworkPolicy` 和 `AppNetworkPolicyStatus`：

- Bridge 目标为 `lzc-br-*` 网桥。
- Host 目标为 `host-app:<app_id>`，运行时关联应用 cgroup。
- Mixed 应用可以同时发现 Bridge 和 Host 两类目标。
- API 支持只修改限速或只修改外网策略。
- 多目标更新先检查能力，再逐目标执行；失败时回滚，成功后持久化。
- 联合修改限速和外网策略时，最后一步持久化失败也会完整恢复防火墙、内存中的 `BlockedApps`、内核限速和持久化限速；回滚失败会和原始错误一起返回，不再只记录日志。

### 2.2 禁用外网

- 权威状态为 `BlockedApps`，即 `app_id -> internet`。
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
- Host/Mixed 限速当前明确拒绝，避免只限制部分流量导致应用断网。

### 2.4 限速实际状态核验与漂移修复

限速器区分三类状态：

- `desired`：持久化的期望限速。
- `applied`：最近一次被 `tc` 接受的规则，只用于回滚。
- `in_sync`：最近一次读取内核规则后的实际同步结果。

后台首次发现网桥或策略变化时立即核验，稳定状态下约每 20 秒读取一次 `tc`。规则缺失、速率不一致、动作错误或网桥同名重建时会自动修复。API/前端可读取 `limit_in_sync`、`limit_state` 和 `diagnostic`。

### 2.5 应用流量统计

Bridge 统计读取 `/sys/class/net/<bridge>/statistics/`，使用网桥名维护 baseline；网桥重建或计数回退时只重建对应 baseline。

Host 统计使用 `cgroup_skb` eBPF：

- ingress/egress 分别写入 RX/TX map。
- map key 为 cgroup ID，value 为字节数和包数。
- collector 为每个 Host 容器输出独立计数记录。
- 状态层按 cgroup 路径维护独立 baseline，再按 `app_id` 聚合。
- 一个 Host 容器重启或计数归零，不会丢失同应用其他容器的增量。
- Bridge 与 Host 混合应用分别计算增量后再聚合。

### 2.6 Host eBPF map 持久化

Host 统计 map 优先 pin 到宿主机 bpffs：

```text
/sys/fs/bpf/netwatch/netwatch_cgrp_rx
/sys/fs/bpf/netwatch/netwatch_cgrp_tx
```

Netwatch 重启后会复用已有 map。如果未挂载 bpffs、bpffs 不可写或 pin 失败，则降级为临时 map，并在 Host 目标 diagnostic 中提示重启会重置计数。eBPF 程序和 cgroup attachment 仍由当前进程管理，服务退出时主动解除，避免挂住已删除应用的 cgroup。

### 2.7 Host eBPF 生命周期清理

- attachment 同时记录 cgroup 路径和 cgroup ID，可识别“路径相同但 cgroup inode 已变化”的复用场景。
- 获取到完整运行时 cgroup 清单后，关闭已停止或被替换 cgroup 的 link，并删除 RX/TX map 中不再属于任何活动 Host 容器的 key。
- 任一活动 Host 容器的应用归属或 cgroup 解析失败时跳过整轮清理，避免在不完整清单下误删有效计数。
- link 关闭失败时保留未关闭的引用，供后续 reconcile 重试；清理错误不会被静默吞掉。

### 2.8 应用列表和历史

- 活动列表只展示当前仍有运行容器的应用。
- 历史数据按 `app_id` 持久化，旧版 Bridge 历史支持迁移。
- Bridge baseline 的旧存储键保持兼容。
- 应用实时刷新只更新实时速率展示，不重建整行交互状态。
- 控制实现和边界已同步写入 [app-traffic-controls.html](./app-traffic-controls.html)。

## 三、当前能力矩阵

| 网络模式 | 流量统计 | 禁用外网 | 限制网速 |
| --- | --- | --- | --- |
| 纯 Bridge | 支持 | 支持 | 支持 |
| 纯 Host | cgroup eBPF，真机已验证 | 实验性开关开启后支持 | 未实现，明确拒绝 |
| Mixed | Bridge + Host 分别统计后聚合，真机已验证 | Bridge/Host 分别控制 | 未实现，明确拒绝 |

## 四、已知边界

### 4.1 Host 统计依赖环境

需要宿主机 cgroup v2、可解析的容器 cgroup 路径、PID/proc/cgroup/sysfs 挂载、BPF capability、内核 eBPF 支持和可用的 `cgroup_skb` attach。依赖不满足时，快照会携带诊断，Bridge 统计不受影响。

### 4.2 Netwatch 停止期间

map 持久化只保留已经累计的计数。cgroup attachment 不 pin，因此 Netwatch 完全停止期间不会产生新的 Host 统计；这样可以避免永久持有已删除容器的 cgroup。目标盒子的 `/sys/fs/bpf` 当前只是普通 sysfs 目录而非 bpffs，因此本机使用临时 map，Netwatch 重启会归零；API 已明确暴露该降级状态。

### 4.3 Host/Mixed 限速

Host 容器共享宿主机网络命名空间，不能直接套用 Bridge 的 root TBF。cgroup_skb 可以实现按 cgroup 的 token bucket，但只能超限丢包（policing），不能排队整形（shaping）；TCP 会通过重传近似收敛，UDP 会直接丢包。宿主物理网卡上的 tc/HTB 较适合上传整形，但下载在 socket/cgroup 归属确定前经过物理入口，无法可靠按应用分类。

Mixed 应用还要求 Bridge 与一个或多个 Host cgroup 共享同一应用级预算。分别给网桥和 cgroup 配置相同速率会得到两份额度，并不满足应用级限速语义。因此在共享 meter、双向分类和事务回滚全部验证前，Host/Mixed 限速继续明确拒绝。

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
- 没有擅自停止或重启其他用户应用；停止应用后的 stale key 真机清理由逻辑/内核单测覆盖，等待安全维护窗口做破坏性生命周期验证。

## 六、Host/Mixed 限速评估结论

当前结论是“不开放，允许单独做纯 Host 原型”，不是直接进入产品实现：

1. 纯 Host 原型优先使用 cgroup_skb 共享 token bucket，明确标注为 policing；同一应用的多个 Host cgroup 必须映射到同一个应用级 bucket，而不是各自获得完整额度。
2. 原型必须覆盖 TCP/UDP、IPv4/IPv6、上传/下载、局域网 bypass、多网卡/回环、cgroup 重建和复用，并证明不影响宿主机及其他应用流量。
3. 策略更新必须使用 generation/staging 方式原子切换，失败时 fail-open 并完整回滚；同时暴露实际速率、丢包数、attachment 和 map 同步状态。
4. 物理网卡 tc/HTB 只能作为 Host 上传整形候选，不能单独解决下载归属，也不能直接解决 Mixed 共享预算。
5. Mixed 只有在 Bridge tc/eBPF 与 Host cgroup hook 能共享同一方向的应用级 meter，并完成多目标回滚和生命周期清理验证后才进入准入评审；否则持续拒绝。
6. bpffs 挂载应作为部署能力单独解决，不由 Netwatch 未经确认修改宿主机全局 mount namespace。
