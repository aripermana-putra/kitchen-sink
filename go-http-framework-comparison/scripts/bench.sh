#!/usr/bin/env bash
# bench.sh — runs load-test-multi.js N times, restarts servers between runs
# to clear in-memory store state, and prints a summary table.
#
# Usage:
#   ./scripts/bench.sh        # 5 runs (default)
#   ./scripts/bench.sh 10     # 10 runs

set -euo pipefail

RUNS=${1:-5}
SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
RESULTS_FILE="/tmp/bench-results.txt"

> "$RESULTS_FILE"

echo "Running $RUNS iterations of load-test-multi.js..."
echo "Each run: 6 variants simultaneously, 50 VUs, 30s"
echo ""

for i in $(seq 1 "$RUNS"); do
  echo "==> Run $i/$RUNS — restarting servers..."

  # Restart all servers to clear in-memory store
  make -C "$SCRIPT_DIR" stop 2>/dev/null || true
  sleep 1
  make -C "$SCRIPT_DIR" run 2>/dev/null
  sleep 2

  echo "==> Run $i/$RUNS — running k6..."
  k6 run "$SCRIPT_DIR/k6/load-test-multi.js" 2>&1 \
    | grep "p(99)" \
    | sed "s/^/run-$i /" \
    >> "$RESULTS_FILE"

  echo "==> Run $i/$RUNS done"
  echo ""
done

make -C "$SCRIPT_DIR" stop 2>/dev/null || true

echo ""
echo "════════════════════════════════════════════════════════"
echo "  RESULTS ACROSS $RUNS RUNS (p99 latency in ms)"
echo "════════════════════════════════════════════════════════"
echo ""

# Print raw results
cat "$RESULTS_FILE"

echo ""
echo "════════════════════════════════════════════════════════"
echo "  SUMMARY — min/max/avg p99 per variant"
echo "════════════════════════════════════════════════════════"
echo ""

for variant in echo-standard echo-strict chi-standard chi-strict gin-standard gin-strict; do
  values=$(grep "$variant" "$RESULTS_FILE" | grep -oE 'p\(99\)=[0-9.]+ms' | grep -oE '[0-9.]+')
  count=$(echo "$values" | wc -l | tr -d ' ')
  min=$(echo "$values" | sort -n | head -1)
  max=$(echo "$values" | sort -n | tail -1)
  avg=$(echo "$values" | awk '{sum+=$1} END {printf "%.2f", sum/NR}')
  printf "  %-20s  min=%-8s max=%-8s avg=%-8s (n=%s)\n" \
    "$variant" "${min}ms" "${max}ms" "${avg}ms" "$count"
done

echo ""
