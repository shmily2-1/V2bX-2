#!/usr/bin/env bash
# Test machines only. Compile unprivileged, modify firewall ONLY inside unshare.
set -euo pipefail
cd "$(dirname "$0")/.."
backend="${1:-nftables}"
case "$backend" in nftables|iptables) ;; *) echo 'Use nftables or iptables' >&2; exit 2;; esac
for tool in go ip unshare; do command -v "$tool" >/dev/null; done
if [[ "$backend" == nftables ]]; then
    command -v nft >/dev/null
else
    for tool in iptables ip6tables iptables-save ip6tables-save; do command -v "$tool" >/dev/null; done
fi
tmp="$(mktemp -d)"
trap 'rm -f -- "$tmp/core.test" "$tmp/firewall.test"; rmdir -- "$tmp"' EXIT
export GOEXPERIMENT=jsonv2 CGO_ENABLED=0
tags='sing xray hysteria2 with_quic with_grpc with_utls with_wireguard with_acme with_gvisor'
go test -c -tags "$tags" -o "$tmp/core.test" ./core
go test -c -o "$tmp/firewall.test" ./common/porthop
elevate=()
if [[ $EUID != 0 ]]; then elevate=(sudo); fi
"${elevate[@]}" unshare --net -- bash -eu -c '
    ip link set lo up
    export V2BX_TEST_NETNS=1 V2BX_FIREWALL_BACKEND="$1"
    "$2/firewall.test" -test.run TestRedirectNetNS -test.v -test.timeout=90s
    "$2/core.test" -test.run TestPortHoppingNetNS -test.v -test.timeout=120s
' _ "$backend" "$tmp"
