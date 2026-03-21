#!/usr/bin/env bash
# ezlb_bench.sh 使用 wrk 对 ezlb 转发的 (VIP:PORT) 做多档位压测，并采集 CPU 与连接状态
# 使用方式：
#  1. 在客户端压力机上安装 wrk，并确保能免密 SSH 访问负载均衡器节点（用于抓取 CPU、软中断、socket 状态）。
#  2. 将 VIP_HOST/VIP_PORT 修改为 ezlb 对外监听的地址端口；如需压测 TCP 其他协议，可换用 wrk --script 或自定义 TCP 脚本。
#  3. CONCURRENCY_LEVELS 可根据目标并发量增减；DURATION 设置为每档位压测时间（建议 ≥60s）。
#  4. 运行脚本：chmod +x ezlb_bench.sh && ./ezlb_bench.sh。
#  5. 结果会生成在 logs/ 目录：wrk_*.log 记录吞吐、延迟；lb_stats_*.log 记录压测期间负载均衡器CPU与连接概况，方便对照分析。
#
#  如果需要 UDP 或更底层 TCP 测试，可将 run_wrk 换成 iperf3 -u/netperf 等命令，其他结构保持不变。

VIP_HOST="10.0.0.1"
VIP_PORT="80"
DURATION="60s"          # 每档位压测时长
THREADS=4               # wrk 工作线程数
CONCURRENCY_LEVELS=("512" "1024" "4096" "8192")

# 被测负载均衡器节点，用于采集状态
LB_HOST="10.0.0.10"

run_wrk() {
  local c=$1
  echo "=== 并发 $c ==="
  wrk \
    -t "${THREADS}" \
    -c "${c}" \
    -d "${DURATION}" \
    --latency \
    "http://${VIP_HOST}:${VIP_PORT}/healthz" \
    | tee "logs/wrk_${c}.log"
}

collect_lb_stats() {
  ssh "${LB_HOST}" "date '+%F %T'; \
    mpstat 1 5 | tail -n +4; \
    echo '--- softirqs ---'; \
    cat /proc/softirqs | head -n 20; \
    echo '--- ss summary ---'; \
    ss -s" \
    > "logs/lb_stats_$(date +%s).log"
}

mkdir -p logs

for concurrency in "${CONCURRENCY_LEVELS[@]}"; do
  collect_lb_stats &
  RUN_ID=$!
  run_wrk "${concurrency}"
  wait "${RUN_ID}"
  echo ""
done
