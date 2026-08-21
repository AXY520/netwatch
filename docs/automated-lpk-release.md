# LPK 自动构建与发布

Netwatch 使用 GitHub Actions 自动构建 LPK、创建 GitHub Release，并通过
`lzc-cli` 将同一份 LPK 提交到懒猫应用商店。

工作流文件：`.github/workflows/release-lpk.yml`

## 触发条件

工作流在以下情况触发：

- 代码推送到 `main` 分支
- 在 GitHub Actions 页面手动运行 `Release LPK`

`dev` 分支不会触发发布。开发改动应先进入 `dev`，验证完成并更新版本后再合并到
`main`。

当前工作流会对 `main` 的每次推送执行完整发布，而不是只在 `package.yml` 变化时
执行。因此，合并到 `main` 前必须确认版本号已经递增。

## 版本和产物命名

版本和应用 ID 从 `package.yml` 读取：

```yaml
package: cloud.lazycat.app.netwatch
version: 0.9.9
```

以上配置会生成：

```text
Git tag: v0.9.9
GitHub Release: Netwatch 0.9.9
LPK: dist/cloud.lazycat.app.netwatch-v0.9.9.lpk
```

发布新版本时只修改 `package.yml` 中的 `version`，不要在工作流中单独维护版本。

如果相同版本再次推送到 `main`：

- `v<version>` 标签会被强制更新到最新的 `main` 提交
- GitHub Release 中的同名 LPK 会被覆盖
- 懒猫商店会拒绝小于或等于已审核版本的提交

因此，正式发布必须使用尚未在商店发布过的新版本号。

## GitHub Secret

仓库必须配置以下 Actions Secret：

```text
Name: LZC_CLI_TOKEN
Value: 懒猫开发者 Token
```

配置位置：

```text
GitHub Repository
  -> Settings
  -> Secrets and variables
  -> Actions
  -> New repository secret
```

工作流通过以下命令配置认证：

```bash
lzc-cli config set token "$LZC_CLI_TOKEN"
```

Token 只能存放在 GitHub Actions Secret 中，不要写入仓库文件、提交信息或远端
Git URL。

GitHub 推送代码使用的 Personal Access Token 与 `LZC_CLI_TOKEN` 不是同一个
Token。若使用 Classic PAT 推送包含 `.github/workflows/` 的提交，需要授予：

```text
repo
workflow
```

## 工作流步骤

### 1. 准备构建环境

GitHub Hosted Runner 使用 Ubuntu，并安装：

- Go：版本从 `go.mod` 读取
- Node.js 22
- `@lazycatcloud/lzc-cli@2.0.9`
- `mtr`、`iproute2`、`iptables` 等 `build.sh` 所需依赖

### 2. 读取包信息

工作流从 `package.yml` 读取应用 ID 和版本，并计算 LPK 文件名及 Release tag。
如果最终产物不存在，工作流会立即失败，不会创建 Release。

### 3. 构建 LPK

仓库默认的 `lzc-build.yml` 没有指定 `builder`，本地开发时由懒猫微服的远端
Developer Tools 构建嵌入镜像。

GitHub Runner 无法连接开发者的目标微服，因此工作流会临时生成：

```text
Dockerfile.release.lzc
lzc-build.release.yml
```

临时配置执行以下调整：

- 将嵌入镜像构建方式改为 `builder: local`
- 将最终镜像基底从 `scratch` 临时替换为 `alpine:3.20`
- 提前执行 `docker pull alpine:3.20`

这些临时文件只存在于 GitHub Runner，不会修改仓库中的 `Dockerfile.lzc` 和
`lzc-build.yml`，也不会影响本地 `deploy.sh`。

这样处理是因为 `lzc-cli 2.0.9` 的本地构建路径会解析 Dockerfile 的最终基础镜像
并执行：

```bash
docker image inspect <base-image>
```

`scratch` 是 Dockerfile 特殊关键字，不是真实镜像，因此
`docker image inspect scratch` 必然失败。显式拉取 Alpine 则保证它存在于 Docker
image store，可供 `lzc-cli` 后续检查。

当后续 `lzc-cli` 已正确支持 `builder: local + FROM scratch` 时，应删除这项兼容
处理，让自动发布包恢复使用 `scratch`。

### 4. 创建 GitHub Release

构建成功后，工作流会：

1. 将 `v<version>` 标签指向当前 `main` 提交并推送
2. 创建对应 GitHub Release
3. 上传构建生成的 LPK

如果 Release 已存在，则使用 `--clobber` 覆盖同名 LPK 文件。

### 5. 提交懒猫商店

GitHub Release 成功后执行：

```bash
lzc-cli config set token "$LZC_CLI_TOKEN"
lzc-cli appstore publish <lpk-path> \
  --changelog "Netwatch <version> release" \
  --changelog-locale en
```

必须显式提供 changelog。否则 `lzc-cli` 会打开交互式编辑提示，而 GitHub Actions
没有交互终端，发布会失败。

工作流只负责提交审核，不代表应用已经通过商店审核。最终状态需要在懒猫开发者
后台查看。

## 正常发布流程

1. 在 `dev` 完成功能开发和验证。
2. 修改 `package.yml`，将版本递增到尚未发布的新版本。
3. 提交版本修改并推送 `dev`。
4. 将 `dev` 合并或快进到 `main`。
5. 推送 `main`，触发 `Release LPK` 工作流。
6. 确认 GitHub Release 中存在正确版本的 LPK。
7. 确认 Action 的 `Publish to Lazycat App Store` 步骤成功。
8. 在懒猫开发者后台确认审核状态。

## 手动重跑

可在 GitHub 仓库的 Actions 页面选择 `Release LPK`，点击 `Run workflow` 手动
执行。

手动重跑仍然读取当前 `main` 的 `package.yml`。如果该版本已经通过商店审核，
GitHub Release 可以正常更新，但商店发布会返回类似错误：

```text
提交审核失败，提交的应用版本小于或等于已审核通过版本
```

这种情况说明版本已存在，不代表 LPK 构建或 GitHub Release 失败。需要再次正式
发布时，应先递增版本号。

## 常见失败排查

### Secret 未配置

失败步骤：`Validate Lazycat App Store token`

检查仓库 Actions Secret 是否存在且名称严格为：

```text
LZC_CLI_TOKEN
```

### 无法修改工作流

Git 推送返回：

```text
refusing to allow a Personal Access Token to create or update workflow
without workflow scope
```

说明用于 Git 推送的 Classic PAT 缺少 `workflow` 权限。这和懒猫商店 Secret
无关。

### `docker image inspect scratch` 失败

说明使用了 `builder: local`，但最终 Dockerfile 仍然是 `FROM scratch`。检查工作流
生成的 `Dockerfile.release.lzc` 和 `lzc-build.release.yml` 是否正确。

### `docker image inspect alpine:3.20` 失败

检查构建前是否成功执行：

```bash
docker pull alpine:3.20
```

BuildKit 在构建时使用过基础镜像，不保证该标签一定存在于普通 Docker image
store 中。

### 商店发布等待输入后失败

检查 `lzc-cli appstore publish` 是否传入 `--changelog`。未传时 CLI 会尝试打开
交互式编辑器。

### 商店拒绝重复版本

检查 `package.yml` 的版本是否已经发布或审核通过。正式发布应递增版本，而不是
在同一个版本上反复覆盖。

## 本地构建和安装

本地测试部署仍使用：

```bash
./deploy.sh
```

它使用仓库原始的 `lzc-build.yml` 和 `Dockerfile.lzc`，通过默认懒猫微服构建并
安装 LPK，不使用 GitHub Actions 中的 Alpine 兼容配置。

