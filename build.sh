#!/bin/sh
set -eu

go test ./...
rm -rf dist
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/netwatch ./cmd/server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/netwatch-proxy ./cmd/hostproxy
cp -R web dist/web
mkdir -p dist/certs
cp -f /etc/ssl/certs/ca-certificates.crt dist/certs/ca-certificates.crt
chmod 0644 dist/certs/ca-certificates.crt
mkdir -p dist/rootfs/etc
cp -f /etc/protocols dist/rootfs/etc/protocols
chmod 0644 dist/rootfs/etc/protocols

MTR_BIN="$(command -v mtr)"
MTR_PACKET_BIN="$(command -v mtr-packet)"
if [ -z "${MTR_BIN}" ] || [ -z "${MTR_PACKET_BIN}" ]; then
  echo "mtr or mtr-packet not found in PATH" >&2
  exit 1
fi

copy_binary_with_libs() {
  bin="$1"
  root="$2"
  mkdir -p "${root}$(dirname "${bin}")"
  cp -L "${bin}" "${root}${bin}"
  if command -v readelf >/dev/null 2>&1; then
    interp="$(readelf -l "${bin}" 2>/dev/null | awk '/Requesting program interpreter/ {gsub(/[\[\]]/, "", $4); print $4; exit}')"
    if [ -n "${interp}" ]; then
      real_interp="$(readlink -f "${interp}" 2>/dev/null || printf '%s' "${interp}")"
      if [ -f "${real_interp}" ]; then
        mkdir -p "${root}$(dirname "${interp}")"
        cp -L "${real_interp}" "${root}${interp}"
      fi
    fi
  fi
  ldd "${bin}" | awk '/=> \// {print $3} /^\/.*ld-linux/ {print $1}' | sort -u | while read -r lib; do
    [ -n "${lib}" ] || continue
    mkdir -p "${root}$(dirname "${lib}")"
    cp -L "${lib}" "${root}${lib}"
  done
}

copy_binary_with_libs "${MTR_BIN}" dist/rootfs
copy_binary_with_libs "${MTR_PACKET_BIN}" dist/rootfs
