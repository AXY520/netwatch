# Netwatch

当前版本聚焦两块内容：

- 国内网站连通性：`Baidu`、`Bilibili`
- 国外网站连通性：`GitHub`、`YouTube`

页面展示内容只保留：

- 网站名
- 延迟
- 是否连通
- 本机网络信息
- 出口 IP
- 出口地区
- NAT 类型

`IPv4/IPv6` 专门连通性模块已经移除。

## 运行

```bash
pg-docker compose up --build
```

或者直接本地运行：

```bash
/root/go/bin/go run ./cmd/server
```

默认监听 `http://localhost:8080`。
LPK 打包版本默认由微服入口转发到内部代理，再代理到 host 网络里的 `23087`。

## 页面行为

- 容器启动时立即执行首轮探测
- 后台每 10 秒自动执行一次快速探测
- 主界面“快速刷新”只刷新网站延迟、出口 IP、出口地区和本机网络信息
- `NAT` 检测独立运行，首轮启动后后台执行一次，并且支持单独手动刷新
- 宽带测速与网页到本机传输测速采用悬浮二级窗口，且同一时间只允许打开一个
- 测速历史会持久化写入 `/app/data`
- 测速界面会显示实时阶段和进度，关闭窗口会立即停止测速
- 时间显示直接来自这台机器本地时间

## API

- `GET /healthz`
- `GET /api/v1/summary`
- `GET /api/v1/connectivity/websites`
- `POST /api/v1/connectivity/websites/run`
- `GET /api/v1/network`
- `POST /api/v1/network/nat/run`
- `POST /api/v1/probe/run`
- `POST /api/v1/settings/refresh-interval?seconds=60`
- `GET /api/v1/speed/config`
- `POST /api/v1/speed/broadband/start`
- `GET /api/v1/speed/broadband/task`
- `POST /api/v1/speed/broadband/cancel`
- `POST /api/v1/speed/broadband/run`
- `GET /api/v1/speed/broadband/history`
- `GET /api/v1/speed/local/history`
- `POST /api/v1/speed/local/result`
- `GET /api/v1/speed/local/ping`
- `GET /api/v1/speed/local/download`
- `POST /api/v1/speed/local/upload`
- `GET /api/v1/network/realtime`
- `GET/POST /api/v1/network/egress-lookups`
- `GET/POST /api/v1/auto-refresh`
- `GET /api/v1/diagnostics/trace?host=github.com`
- `GET /api/v1/events`

## 配置项

- `PORT`
- `REFRESH_INTERVAL_SEC`
- `DOMESTIC_SITES`
- `GLOBAL_SITES`
- `STUN_SERVERS`
- `PUBLIC_IPV4_ENDPOINT`
- `PUBLIC_IPV6_ENDPOINT`
- `MONITORED_NICS`
- `DATA_DIR`
- `BROADBAND_TEST_SEC`
- `LOCAL_TRANSFER_TEST_SEC`
- `LOCAL_TRANSFER_PAYLOAD_MB`

默认网站：

- 国内：`Baidu`、`Bilibili`
- 国外：`GitHub`、`YouTube`

## host 网络建议

如果你想看到宿主机真实的 NAT、出口 IP、网卡和默认路由，建议继续使用：

```yaml
network_mode: host
```

## 直接运行

如果你不走容器，直接在项目目录执行：

```bash
cd /root/.codex/netwatch
/root/go/bin/go run ./cmd/server
```

打开 `http://127.0.0.1:8080` 即可。

## 前端结构

- `web/index.html`：页面骨架
- `web/app.css`：样式
- `web/app.js`：交互逻辑
