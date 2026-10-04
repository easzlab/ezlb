#!/bin/sh

set -eu

for module in ip_vs ip_vs_rr ip_vs_wrr ip_vs_lc ip_vs_wlc ip_vs_sh ip_vs_dh nf_conntrack; do
  modprobe "$module"
done

sysctl -w net.ipv4.ip_forward=1
sysctl -w net.ipv4.vs.conntrack=1

exec /app/ezlb start --exclusive-netns -c /test/ezlb.yaml
