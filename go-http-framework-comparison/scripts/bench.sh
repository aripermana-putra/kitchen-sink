#!/usr/bin/env bash
# bench.sh — runs load-test-multi.js N times with server restarts between runs.
# Outputs per-variant p99 and a min/max/avg summary table.
#
# Usage:
#   ./scripts/bench.sh        # 5 runs (default)
#   ./scripts/bench.sh 10     # 10 runs

set -euo pipefail

RUNS=${1:-5}
SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
RESULTS_FILE="/tmp/bench-results.txt"
K6_RAW="/tmp/bench-k6-raw.txt"

> "$RESULTS_FILE"

echo "Running $RUNS iterations of load-test-multi.js..."
echo "Each run: 6 variants simultaneously, 50 VUs, 30s"
echo ""

force_kill_servers() {
  # Kill by process name first
  pkill -f "echo-standard/server\|echo-strict/server\|chi-standard/server\|chi-strict/server\|gin-standard/server\|gin-strict/server" 2>/dev/null || true
  # Then force kill by port to be sure
  for port in 9001 9002 9003 9004 9005 9006; do
    lsof -ti :$port 2>/dev/null | xargs kill -9 2>/dev/null || true
  done
  sleep 1
}

for i in $(seq 1 "$RUNS"); do
  echo "==> Run $i/$RUNS — restarting servers..."
  force_kill_servers
  make -C "$SCRIPT_DIR" run 2>/dev/null
  sleep 2

  echo "==> Run $i/$RUNS — running k6..."
  k6 run "$SCRIPT_DIR/k6/load-test-multi.js" 2>&1 > "$K6_RAW"

  # Extract p99 per variant from threshold output block
  # k6 threshold output looks like:
  #   http_req_duration{variant:echo-standard}
  #   ✓ 'p(99)<50' p(99)=1.21ms
  grep -A1 "http_req_duration{variant:" "$K6_RAW" \
    | grep -v "^--$" \
    | paste - - \
    | sed "s/.*variant:\([^}]*\)}.* p(99)=\([0-9.]*\)ms/run-$i \1 \2/" \
    >> "$RESULTS_FILE" || true

  echo "==> Run $i/$RUNS done"
  echo ""
done

force_kill_servers

echo ""
echo "════════════════════════════════════════════════════════"
echo "  RAW RESULTS — p99 (ms) per variant per run"
echo "════════════════════════════════════════════════════════"
printf "  %-6s %-20s %s\n" "run" "variant" "p99"
echo "  ──────────────────────────────────────"
while read -r run variant value; do
  printf "  %-6s %-20s %sms\n" "$run" "$variant" "$value"
done < "$RESULTS_FILE"

echo ""
echo "════════════════════════════════════════════════════════"
echo "  SUMMARY — min / max / avg p99 across $RUNS runs"
echo "════════════════════════════════════════════════════════"
echo ""

for variant in echo-standard echo-strict chi-standard chi-strict gin-standard gin-strict; do
  values=$(grep " $variant " "$RESULTS_FILE" | awk '{print $3}')
  if [ -z "$values" ]; then
    printf "  %-20s  no data\n" "$variant"
    continue
  fi
  min=$(echo "$values" | sort -n | head -1)
  max=$(echo "$values" | sort -n | tail -1)
  avg=$(echo "$values" | awk '{sum+=$1} END {printf "%.2f", sum/NR}')
  printf "  %-20s  min=%-8s max=%-8s avg=%s ms\n" \
    "$variant" "${min}ms" "${max}ms" "$avg"
done

echo ""
echo "Results saved to: $RESULTS_FILE"
