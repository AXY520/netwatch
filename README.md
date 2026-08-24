# Netwatch

Netwatch 是面向懒猫微服的主机网络观测应用，用于查看网站连通性、出口 IP、出口地区、NAT 类型、网卡实时速率、应用网桥流量、路由追踪和测速结果。项目以 LPK 形式构建和发布。

## 功能

- 国内网站连通性：默认探测 `Baidu`、`Bilibili`
- 国外网站连通性：默认探测 `GitHub`、`YouTube`
- 本机网络信息：接口、地址、默认路由、DNS 等
- 主机网络配置：通过懒猫 SDK 修改网卡 IP、网桥和 DNS，并支持超时自动回滚
- 出口信息：公网 IPv4/IPv6、国内出口、地区识别
- NAT 类型检测：基于 STUN 的手动探测
- 网卡实时速率：自动识别宿主物理有线和 Wi-Fi 网卡
- 应用流量：在懒猫环境中按 `lzc-br-*` 网桥展示应用流量
- 宽带测速：分别测量用户设备直连公网、服务器出口直连公网的下载/上传/延迟
- 传输测速：基于 LibreSpeed 测量浏览器到本项目服务端的下载/上传/延迟
- 路由追踪：基于 `mtr` 的异步 trace 任务
- 时序与历史：保存测速历史和网卡速率时序
- 可选告警：出口 IP 或 NAT 变化时向 webhook 发送通知

## 懒猫微服 LPK

LPK 打包由 `lzc-build.yml`、`lzc-manifest.yml` 和 `build.sh` 组成。

- `build.sh`：执行测试，构建 `netwatch` 和 `netwatch-proxy`，复制前端、证书、`mtr` 与运行依赖到 `dist/`
- `Dockerfile.lzc`：从 `dist/` 组装 scratch 镜像
- `lzc-manifest.yml`：定义应用路由和服务
- `lzc-build.yml`：定义打包入口、镜像构建和 compose 覆盖项

LPK 版本包含两个服务：

- `netwatch`：host 网络模式运行，默认监听 `23087`
- `proxy`：监听 `23088`，通过默认网关反向代理到宿主网络里的 `23087`

微服入口路由：

```yaml
application:
  routes:
    - /=http://proxy:23088
```

`lzc-build.yml` 还通过 `compose_override` 为 `netwatch` 增加 `NET_RAW`、`NET_ADMIN`，并只读挂载懒猫 Docker socket 与 package 元数据目录，用于把 `lzc-br-*` 网桥映射到应用名称。挂载不可用时，应用会降级为仅展示网桥级流量。

打包和安装：

```bash
./deploy.sh
```

`deploy.sh` 用于本地构建 LPK 并安装到默认懒猫微服，依赖 `lzc-cli` 以及 `build.sh` 所需的 Linux 工具。

自动构建、GitHub Release 和懒猫商店发布流程见
[LPK 自动构建与发布](docs/automated-lpk-release.md)。

## API

基础：

- `GET /healthz`：健康检查
- `GET /api/v1/summary`：完整摘要
- `GET /api/v1/events`：SSE 推送 summary
- `GET /api/v1/events/history?severity=warning&kind=nat_type_changed&since=2026-08-01&limit=100`：查询持久化网络事件
- `GET /metrics`：Prometheus 风格指标

当前状态类观测 API 会在保留原有 `generated_at`、`timestamp` 或 `checked_at` 字段的同时，
追加统一 freshness 元数据：

```json
{
  "sampled_at": "2026-08-04 12:00:00",
  "age_seconds": 8,
  "stale": false
}
```

`age_seconds` 在响应时根据原采样时间动态计算，不是写盘时的固定值。超过对应观测类型的
有效期会返回 `stale: true`；尚未获得有效采样时间时同样视为 stale。摘要、网站检测、
网络信息、NAT、网卡实时速率、出口查询、端口、LAN、trace、capabilities 和 SSE 中的
当前状态事件遵循同一规则。历史数组保持原有顶层 JSON 形状，通过逐点时间戳表达时间范围。

连通性与网络：

- `GET /api/v1/connectivity/websites`：网站连通性缓存
- `POST /api/v1/connectivity/websites/run`：刷新网站连通性
- `GET /api/v1/network`：网络信息缓存
- `POST /api/v1/network/nat/run`：刷新 NAT 检测
- `POST /api/v1/probe/run`：执行快速刷新
- `GET /api/v1/network/realtime`：网卡实时速率
- `GET /api/v1/network/egress-lookups`：出口查询缓存或触发查询
- `POST /api/v1/network/egress-lookups`：清除公网 IP 缓存并刷新出口查询
- `GET /api/v1/network/config/devices`：读取可配置网卡
- `POST /api/v1/network/config/apply`：应用网卡 IPv4 配置
- `POST /api/v1/network/config/confirm`：确认网卡配置
- `POST /api/v1/network/config/rollback`：回滚网卡配置
- `GET /api/v1/network/dns`：读取指定网卡的 DNS 配置
- `POST /api/v1/network/dns/apply`：应用自动或手动 DNS 配置
- `POST /api/v1/network/dns/confirm`：确认 DNS 配置
- `POST /api/v1/network/dns/rollback`：回滚 DNS 配置
- `GET /api/v1/network/dns/pending`：读取待确认 DNS 变更
- `GET /api/v1/network/bridges`：读取受管主机网桥
- `POST /api/v1/network/bridges/create`：创建主机网桥
- `POST /api/v1/network/bridges/confirm`：确认主机网桥变更
- `POST /api/v1/network/bridges/rollback`：回滚主机网桥变更
- `POST /api/v1/network/bridges/dissolve`：解散受管主机网桥
- `GET /api/v1/network/mutations/audit?limit=50`：读取最近的统一网络变更审计（最多 200 条）

设置：

- `GET /api/v1/settings`：读取持久化设置
- `POST /api/v1/settings`：更新持久化设置
- `PUT /api/v1/settings`：更新持久化设置
- `POST /api/v1/settings/persistent-traffic-bridges`：设置应用流量持久采样网桥

测速：

- `GET /api/v1/speed/config`：测速配置
- `GET /api/v1/speed/broadband/catalog`：读取两个公共 CDN 测速节点目录
- `POST /api/v1/speed/broadband/server/start`：启动服务器出口到公网的异步测速，可传 `{"node_id":"1"}`
- `GET /api/v1/speed/broadband/server/task`：读取服务器出口测速任务状态
- `POST /api/v1/speed/broadband/server/cancel`：取消服务器出口测速
- `POST /api/v1/speed/broadband/client/result`：保存用户设备直连公网的测速结果
- `GET /api/v1/speed/broadband/history`：宽带测速历史
- `GET /api/v1/speed/local/history`：本机传输测速历史
- `POST /api/v1/speed/local/result`：记录本机传输测速结果
- `GET /api/v1/speed/local/ping`：本机传输测速延迟探针
- `GET /api/v1/speed/local/download`：本机传输测速下载端点
- `POST /api/v1/speed/local/upload`：本机传输测速上传端点

诊断与流量：

- `GET /api/v1/diagnostics/trace`：读取最近一次 trace 任务状态
- `POST /api/v1/diagnostics/trace?host=github.com&hops=20`：启动 trace 任务
- `GET /api/v1/diagnostics/trace/task`：读取 trace 任务状态
- `POST /api/v1/diagnostics/dns`：查询 A、AAAA 或 CNAME，可对比系统 DNS 与指定服务器
- `GET /api/v1/timeseries?limit=300`：读取时序点
- `GET /api/v1/network/app-traffic`：读取原始网桥计数和按应用 ID 聚合的实时、今日、本月、累计流量
- `GET /api/v1/network/app-traffic/history?app_id=cloud.lazycat.app.example`：读取指定应用的分钟采样历史
- `POST /api/v1/network/app-traffic/limit`：设置应用上传/下载上限，Body 为 `{"app_id":"...","upload_kbps":1024,"download_kbps":4096}`，使用 `0` 取消对应方向限速

### 应用流量语义

应用流量来自宿主网络命名空间中的 Linux 网桥 sysfs 计数器，数据源标记为
`linux_bridge_sysfs`，计数视角标记为 `host_bridge`。为兼容旧客户端，API 继续返回
原始 `rx_bytes`、`tx_bytes`，同时提供语义化字段：

```json
{
  "rx_bytes": 100,
  "tx_bytes": 200,
  "upload_bytes": 100,
  "download_bytes": 200,
  "counter_perspective": "host_bridge",
  "source": "linux_bridge_sysfs"
}
```

在宿主 bridge 视角下，RX 是应用容器发往宿主网桥的流量，对应应用上传；TX 是宿主
网桥发往应用容器的流量，对应应用下载。该统计可能包含应用内部或局域网流量，不等同于
运营商公网账单。使用 `network_mode: host` 的应用服务绕过独立应用网桥，无法通过此方式
完整统计。应用级累计值按稳定 `app_id` 持久化；新建网桥或内核计数器归零时，NetWatch
仅重建该网桥基线，不会把重置前的旧字节数重复计入累计值。

限速仅适用于拥有 `lzc-br-*` 独立网桥的应用。下载通过该网桥的 `tc tbf` 出口队列限速，
上传通过 ingress policer 限速；`network_mode: host` 的服务没有可归属网桥，无法统计或限速。
NetWatch 使用 `nsenter` 调用宿主机的 `tc`，不在 scratch 镜像中复制该二进制；宿主必须提供
`tc`。NetWatch 只管理自身固定句柄/优先级的规则，发现其他 root qdisc 时会拒绝覆盖。

`/metrics` 同时暴露原始 `netwatch_app_traffic_rx_bytes`、
`netwatch_app_traffic_tx_bytes` 和语义化 `netwatch_app_traffic_upload_bytes`、
`netwatch_app_traffic_download_bytes`。

## 配置

主要环境变量：

- `PORT`：监听端口，默认 `8080`
- `CONFIG`：JSON 配置文件路径，优先覆盖默认配置
- `REFRESH_INTERVAL_SEC`：单次快速探测超时时间，默认 `10`
- `DOMESTIC_SITES`：国内站点，格式 `Name|URL,Name|URL`
- `GLOBAL_SITES`：国外站点，格式 `Name|URL,Name|URL`
- `STUN_SERVERS`：STUN 服务器，逗号分隔
- `PUBLIC_IPV4_ENDPOINT`：公网 IPv4 查询端点
- `PUBLIC_IPV6_ENDPOINT`：公网 IPv6 查询端点
- `DATA_DIR`：持久化数据目录，默认 `/app/data`
- `BROADBAND_TEST_SEC`：宽带测速时长，默认 `15`
- `LOCAL_TRANSFER_TEST_SEC`：本机传输测速时长，默认 `10`
- `LOCAL_TRANSFER_PAYLOAD_MB`：本机传输测速固定负载大小，默认 `32`
- `IPV6_HIGH_PORT_PROBE_HOST`：IPv6 高端口探测地址
- `IPV6_HIGH_PORT_PROBE_PORT`：IPv6 高端口探测端口
- `BASIC_AUTH_USER`：可选 Basic Auth 用户名
- `BASIC_AUTH_PASSWORD`：可选 Basic Auth 密码
- `ALERT_WEBHOOK_URL`：可选告警 webhook

`CONFIG` JSON 示例：

```json
{
  "port": "8080",
  "refresh_interval_sec": 10,
  "http_timeout_sec": 6,
  "nat_timeout_sec": 2,
  "data_dir": "/app/data",
  "broadband_test_sec": 15,
  "local_transfer_test_sec": 10,
  "local_transfer_payload_mb": 32
}
```

## 数据文件

`DATA_DIR` 下会写入：

- `settings.json`：页面设置和可变运行配置
- `broadband_history.json`：宽带测速历史
- `local_transfer_history.json`：本机传输测速历史
- `timeseries.json`：摘要时序数据，最多保留 2000 点
- `app_traffic_history.json`：按应用 ID 的流量基线、分钟采样、日/月累计与限速配置

## 认证与访问控制

默认不启用认证。只有同时设置 `BASIC_AUTH_USER` 和 `BASIC_AUTH_PASSWORD` 时，服务端才会对所有静态页面和 API 启用 Basic Auth。

如果部署在公网或不可信局域网，建议必须启用 Basic Auth，或者放在具备认证能力的反向代理之后。测速、trace、刷新和上传下载端点都可能消耗主机网络、CPU 或外部请求额度。

## 部署风险

- LPK 配置需要 `NET_RAW`/`NET_ADMIN`，用于路由追踪、NAT/连通性探测等能力；不要把这个容器当作低权限沙箱。
- 应用流量识别会挂载 Docker socket 和懒猫包元数据，只建议在可信主机上运行。
- 本机传输测速上传接口限制单次请求体最大 512 MiB，并限制同时进行的测速流数量；仍可能占用带宽和 CPU。

## 页面行为

- 前端打开页面时会触发首轮探测
- 主界面“快速刷新”只刷新网站延迟、出口 IP、出口地区和本机网络信息
- NAT 检测独立运行，支持单独手动刷新
- 宽带测速与网页到本机传输测速采用悬浮二级窗口，且同一时间只允许打开一个
- 网卡 IP 与网桥变更需要在 3 分钟内确认，DNS 变更需要在 60 秒内确认，否则自动回滚
- 测速界面会显示实时阶段和进度，关闭窗口会立即停止测速
- 时间显示直接来自这台机器本地时间

## 排障

- 页面能打开但路由追踪失败：确认镜像或宿主环境中存在 `mtr` 和 `mtr-packet`，并具备 `NET_RAW` 权限
- 看不到真实网卡或默认路由：确认使用 host 网络模式
- 应用流量只有网桥名没有应用名：确认懒猫 Docker socket 和 package 元数据目录挂载成功
- 网卡速率一直为空：确认使用 host 网络模式，并检查宿主机是否存在 `en*`/`eth*` 有线接口或 `wl*` Wi-Fi 接口
- LPK 打包失败并提示找不到 `mtr`：在执行 `build.sh` 的 Linux 环境安装 `mtr`
- 健康检查失败：确认 `PORT`、`LISTEN_PORT`、`TARGET_PORT` 与 `lzc-manifest.yml` 保持一致

## 前端结构

- `web/index.html`：页面骨架
- `web/app.css`：样式
- `web/app.js`：交互逻辑
- `web/speedtest.js`、`web/speedtest_worker.js`：浏览器本机传输测速逻辑
