#!/bin/bash

set -euo pipefail

CONFIG_PATH="${EZLB_CONFIG_PATH:-$(pwd)/config.yaml}"
DOCKER_NETWORK="${EZLB_DOCKER_NETWORK:-bridge}"
if [[ ! -f "${CONFIG_PATH}" ]]; then
  echo "Config file not found: ${CONFIG_PATH}" >&2
  exit 1
fi

# Docker gives this container a private network namespace. Provision the VIP,
# reachable routes and return path on that namespace before using it for traffic.

echo "Loading IPVS kernel modules..."
modprobe ip_vs
modprobe ip_vs_rr
modprobe ip_vs_wrr
modprobe ip_vs_lc
modprobe ip_vs_wlc
modprobe ip_vs_sh
modprobe ip_vs_dh
modprobe nf_conntrack

docker run -d \
  --name ezlb \
  --network "${DOCKER_NETWORK}" \
  --cap-add NET_ADMIN \
  --cap-add NET_RAW \
  --restart unless-stopped \
  -v /lib/modules:/lib/modules:ro \
  -v "${CONFIG_PATH}:/app/config.yaml:ro" \
  easzlab/ezlb:latest \
  /app/ezlb start --exclusive-netns -c /app/config.yaml
