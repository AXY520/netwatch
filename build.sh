#!/bin/sh
set -eu

GOARCH="${GOARCH:-amd64}"

go test ./...
rm -rf dist
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH="${GOARCH}" go build -trimpath -ldflags="-s -w" -o dist/netwatch ./cmd/server
CGO_ENABLED=0 GOOS=linux GOARCH="${GOARCH}" go build -trimpath -ldflags="-s -w" -o dist/netwatch-proxy ./cmd/hostproxy
cp -R web dist/web
mkdir -p dist/certs
cp -f /etc/ssl/certs/ca-certificates.crt dist/certs/ca-certificates.crt
chmod 0644 dist/certs/ca-certificates.crt
mkdir -p dist/rootfs/etc
cp -f /etc/protocols dist/rootfs/etc/protocols
chmod 0644 dist/rootfs/etc/protocols
if [ -f /usr/share/nmap/nmap-mac-prefixes ]; then
  mkdir -p dist/rootfs/usr/share/nmap
  cp -f /usr/share/nmap/nmap-mac-prefixes dist/rootfs/usr/share/nmap/nmap-mac-prefixes
  chmod 0644 dist/rootfs/usr/share/nmap/nmap-mac-prefixes
fi

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
  if ! command -v readelf >/dev/null 2>&1; then
    echo "readelf not found, cannot copy dynamic linker" >&2
    return 1
  fi
  interp="$(readelf -l "${bin}" 2>/dev/null | awk '/Requesting program interpreter/ {gsub(/[\[\]]/, "", $4); print $4; exit}')"
  if [ -n "${interp}" ]; then
    real_interp="$(readlink -f "${interp}" 2>/dev/null || printf '%s' "${interp}")"
    if [ -f "${real_interp}" ]; then
      mkdir -p "${root}$(dirname "${interp}")"
      cp -L "${real_interp}" "${root}${interp}"
    fi
  fi
  readelf -d "${bin}" 2>/dev/null | awk '/NEEDED/ {print $NF}' | sort -u | while read -r lib; do
    [ -n "${lib}" ] || continue
    found="$(find /usr/lib /lib -name "${lib}" -type f 2>/dev/null | head -1)"
    [ -n "${found}" ] || continue
    mkdir -p "${root}$(dirname "${found}")"
    cp -L "${found}" "${root}${found}"
  done
}

copy_binary_with_libs "${MTR_BIN}" dist/rootfs
copy_binary_with_libs "${MTR_PACKET_BIN}" dist/rootfs
