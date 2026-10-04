#!/bin/bash
# docker-entrypoint.sh
# Loads IPVS kernel modules inside the container before running the test command.
#
# This runs inside the container (requires --privileged on the docker run side).
# On macOS Docker Desktop, the underlying kernel is the Linux VM managed by
# Docker Desktop, which ships with IPVS modules built-in, so modprobe succeeds.
# On a Linux host the same approach works as long as the host kernel has IPVS.

set -euo pipefail

echo "==> Loading IPVS kernel modules..."
modprobe ip_vs
modprobe ip_vs_rr
modprobe ip_vs_wrr
modprobe ip_vs_lc
modprobe ip_vs_wlc
modprobe ip_vs_sh
modprobe ip_vs_dh
modprobe nf_conntrack
echo "==> IPVS modules loaded."

# FullNAT's conntrack tuple match and NAT forwarding require these namespaced
# kernel parameters. They are set only inside this disposable container.
sysctl -w net.ipv4.ip_forward=1
sysctl -w net.ipv4.vs.conntrack=1

# TestMain flushes the entire IPVS table. This flag is deliberately set only
# by the isolated container entrypoint, never by the Makefile.
export EZLB_TEST_EXCLUSIVE_NETNS=1

exec "$@"
