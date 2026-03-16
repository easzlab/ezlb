#!/bin/bash

set -euo pipefail

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
  --network host \
  --cap-add NET_ADMIN \
  --cap-add NET_RAW \
  --restart unless-stopped \
  -v /lib/modules:/lib/modules:ro \
  -v ./config.yaml:/app/config.yaml \
  easzlab/ezlb:latest \
  /app/ezlb start -c /app/config.yaml