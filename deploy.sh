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

# 2) 修复 SDK 间接依赖（gitee.com/linakesi/lzc-baseos-protos / remotesocks）
echo "==> 同步 Go 依赖"
go get gitee.com/linakesi/lzc-sdk/lang/go@latest >/dev/null
go mod tidy

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
