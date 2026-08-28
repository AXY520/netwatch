# Host 网络控制与应用流量统计进度

更新时间：2026-08-28  
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

Netwatch 重启后会复用已有 map。如果 bpffs 可见但不可写，则降级为临时 map，并在诊断中提示重启会重置计数。eBPF 程序和 cgroup attachment 仍由当前进程管理，服务退出时主动解除，避免挂住已删除应用的 cgroup。

### 2.7 应用列表和历史

- 活动列表只展示当前仍有运行容器的应用。
- 历史数据按 `app_id` 持久化，旧版 Bridge 历史支持迁移。
- Bridge baseline 的旧存储键保持兼容。
- 应用实时刷新只更新实时速率展示，不重建整行交互状态。
- 控制实现和边界已同步写入 [app-traffic-controls.html](./app-traffic-controls.html)。

## 三、当前能力矩阵

| 网络模式 | 流量统计 | 禁用外网 | 限制网速 |
| --- | --- | --- | --- |
| 纯 Bridge | 支持 | 支持 | 支持 |
| 纯 Host | 尝试使用 cgroup eBPF | 实验性开关开启后支持 | 未实现 |
| Mixed | Bridge + Host 分别统计后聚合 | Bridge/Host 分别控制 | 未实现 |

## 四、已知边界

### 4.1 Host 统计依赖环境

需要宿主机 cgroup v2、可解析的容器 cgroup 路径、PID/proc/cgroup/sysfs 挂载、BPF capability、内核 eBPF 支持和可用的 `cgroup_skb` attach。依赖不满足时，快照会携带诊断，Bridge 统计不受影响。

### 4.2 Netwatch 停止期间

map 持久化只保留已经累计的计数。cgroup attachment 不 pin，因此 Netwatch 完全停止期间不会产生新的 Host 统计；这样可以避免永久持有已删除容器的 cgroup。

### 4.3 Host/Mixed 限速

Host 容器共享宿主机网络命名空间，不能直接套用 Bridge 的 root TBF。后续方案必须验证 cgroup 级上传/下载方向、多容器混合归属、宿主机隔离、应用重启恢复、规则清理和失败回滚。在验证前不开放 Host/Mixed 限速按钮。

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

测试覆盖限速规则创建/清理/漂移修复、多 Host cgroup baseline、计数回退、持久化重载、Bridge + Host 混合聚合、Host 实验性开关、bpffs 识别和禁网策略回滚。

## 六、后续建议

1. 部署后确认 `/sys/fs/bpf/netwatch/` 下两个 map 是否成功创建，并检查 Host 应用诊断信息。
2. 观察 Host 统计跨 Netwatch 重启和应用重启后的累计值是否符合预期。
3. Host/Mixed 限速单独做技术验证，不直接复用 Bridge `tc` 规则。
4. 只有确认限速、混合应用回滚和宿主机隔离后，才开放实验性限速控制。
