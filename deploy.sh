#!/usr/bin/env bash
# 一键打包并部署 netwatch 到默认懒猫盒子
# 使用：./deploy.sh
set -euo pipefail

cd "$(dirname "$0")"

# 1) 解析 lpk 输出文件名
PKG=$(awk -F: '/^package:/ {gsub(/[ \t]/,"",$2); print $2}' package.yml)
VER=$(awk -F: '/^version:/ {gsub(/[ \t"]/,"",$2); print $2}' package.yml)
LPK="${PKG}-v${VER}.lpk"

echo "==> 准备 ${LPK}"

# 2) Go 依赖同步。默认只在 go.mod/go.sum 变化时执行，避免每次部署都拉依赖。
#    如需强制刷新 SDK/依赖：FORCE_GO_DEPS=1 ./deploy.sh
mkdir -p .cache
DEPS_HASH_FILE=".cache/deploy-go-deps.sha"
DEPS_HASH="$(sha256sum go.mod go.sum 2>/dev/null | sha256sum | awk '{print $1}')"
OLD_DEPS_HASH="$(cat "${DEPS_HASH_FILE}" 2>/dev/null || true)"
if [[ "${FORCE_GO_DEPS:-0}" == "1" || "${DEPS_HASH}" != "${OLD_DEPS_HASH}" ]]; then
    echo "==> 同步 Go 依赖"
    go mod download
    go mod tidy
    DEPS_HASH="$(sha256sum go.mod go.sum 2>/dev/null | sha256sum | awk '{print $1}')"
    printf '%s\n' "${DEPS_HASH}" > "${DEPS_HASH_FILE}"
else
    echo "==> Go 依赖未变化，跳过同步"
fi

# 3) 编译 + 装配 dist/
echo "==> 编译 dist/"
bash build.sh

# 4) 打包 .lpk
echo "==> 打包 lpk (lzc-cli project build)"
rm -f "${LPK}"
lzc-cli project build

if [[ ! -f "${LPK}" ]]; then
    echo "❌ 找不到 ${LPK}，检查 lzc-cli project build 的输出" >&2
    exit 1
fi

# 5) 安装到默认盒子
echo "==> 安装到默认盒子 (lzc-cli app install)"
lzc-cli app install "./${LPK}"

echo
echo "✅ 部署完成: ${LPK}"

echo "当前时间: $(date '+%Y-%m-%d %H:%M:%S')"
