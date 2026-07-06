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
  seen="/"
  queue="${bin}"
  while [ -n "${queue}" ]; do
    next=""
    for b in ${queue}; do
      libs="$(readelf -d "${b}" 2>/dev/null | sed -n 's/.*\[\(.*\)\].*/\1/p' | sort -u)"
      for lib in ${libs}; do
        [ -n "${lib}" ] || continue
        case "${seen}" in */"${lib}"/*) continue;; esac
        found="$(find /usr/lib /usr/lib64 /lib /lib64 -name "${lib}" \( -type f -o -type l \) 2>/dev/null | head -1)"
        [ -n "${found}" ] || continue
        mkdir -p "${root}$(dirname "${found}")"
        cp -L "${found}" "${root}${found}"
        seen="${seen}${lib}/"
        next="${next} ${found}"
      done
    done
    queue="${next}"
  done
}

copy_binary_with_libs "${MTR_BIN}" dist/rootfs
copy_binary_with_libs "${MTR_PACKET_BIN}" dist/rootfs

NSENTER_BIN="$(command -v nsenter || true)"
if [ -n "${NSENTER_BIN}" ]; then
  copy_binary_with_libs "${NSENTER_BIN}" dist/rootfs
fi

IP_BIN="$(command -v ip || true)"
if [ -z "${IP_BIN}" ] || [ ! -x "${IP_BIN}" ]; then
  for p in /usr/sbin/ip /sbin/ip /usr/bin/ip; do
    if [ -x "$p" ]; then
      IP_BIN="$p"
      break
    fi
  done
fi
if [ -n "${IP_BIN}" ]; then
  copy_binary_with_libs "${IP_BIN}" dist/rootfs
fi

ARPING_BIN="$(command -v arping || true)"
if [ -n "${ARPING_BIN}" ]; then
  copy_binary_with_libs "${ARPING_BIN}" dist/rootfs
fi

PING_BIN="$(command -v ping || true)"
if [ -n "${PING_BIN}" ]; then
  copy_binary_with_libs "${PING_BIN}" dist/rootfs
fi

IPTABLES_BIN="$(command -v iptables || true)"
if [ -z "${IPTABLES_BIN}" ] || [ ! -x "${IPTABLES_BIN}" ]; then
  for p in /usr/sbin/iptables /sbin/iptables /usr/bin/iptables; do
    if [ -x "$p" ]; then
      IPTABLES_BIN="$p"
      break
    fi
  done
fi
if [ -n "${IPTABLES_BIN}" ]; then
  copy_binary_with_libs "${IPTABLES_BIN}" dist/rootfs
fi

IP6TABLES_BIN="$(command -v ip6tables || true)"
if [ -z "${IP6TABLES_BIN}" ] || [ ! -x "${IP6TABLES_BIN}" ]; then
  for p in /usr/sbin/ip6tables /sbin/ip6tables /usr/bin/ip6tables; do
    if [ -x "$p" ]; then
      IP6TABLES_BIN="$p"
      break
    fi
  done
fi
if [ -n "${IP6TABLES_BIN}" ]; then
  copy_binary_with_libs "${IP6TABLES_BIN}" dist/rootfs
fi

XTABLES_DIR=""
for d in /usr/lib/x86_64-linux-gnu/xtables /usr/lib/xtables /usr/lib64/xtables; do
  if [ -d "$d" ]; then
    XTABLES_DIR="$d"
    break
  fi
done
if [ -n "${XTABLES_DIR}" ]; then
  mkdir -p "dist/rootfs${XTABLES_DIR}"
  for mod in "${XTABLES_DIR}"/libxt_*.so; do
    [ -f "$mod" ] && cp -L "$mod" "dist/rootfs${XTABLES_DIR}/" && chmod 0755 "dist/rootfs${XTABLES_DIR}/$(basename "$mod")"
  done
fi
