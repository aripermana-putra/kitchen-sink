# Benchmark Results

Results from `make bench RUNS=5` — all 6 variants running **simultaneously** (20 VUs each,
120 total), servers restarted between each run to clear in-memory store state.

Concurrent execution eliminates ordering bias from the sequential load test.

## Raw Results — p99 (ms) per variant per run

| run | echo-standard | echo-strict | chi-standard | chi-strict | gin-standard | gin-strict |
|-----|--------------|------------|-------------|-----------|-------------|-----------|
| 1   | 3.92ms | 3.91ms | 3.85ms | 3.90ms | 3.85ms | 3.88ms |
| 2   | 3.81ms | 3.81ms | 3.83ms | 3.83ms | 3.85ms | 3.82ms |
| 3   | 3.76ms | 3.79ms | 3.78ms | 3.76ms | 3.75ms | 3.77ms |
| 4   | 3.93ms | 3.90ms | 3.95ms | 3.93ms | 3.91ms | 3.94ms |
| 5   | 3.89ms | 3.93ms | 3.85ms | 3.89ms | 3.90ms | 3.86ms |

## Summary — min / max / avg p99 across 5 runs

| Variant | min | max | avg |
|---------|-----|-----|-----|
| echo-standard | 3.76ms | 3.93ms | 3.86ms |
| echo-strict | 3.79ms | 3.93ms | 3.87ms |
| chi-standard | 3.78ms | 3.95ms | 3.85ms |
| chi-strict | 3.76ms | 3.93ms | 3.86ms |
| gin-standard | 3.75ms | 3.91ms | 3.85ms |
| gin-strict | 3.77ms | 3.94ms | 3.85ms |

## Conclusion

**Total spread across all variants and all runs: 3.75ms – 3.95ms (0.20ms range)**

No framework has a consistent performance advantage. The ranking changes between runs —
in run 1 chi/gin lead, in run 2 echo leads, in run 3 gin leads. The variation within a
single variant across runs (~0.15ms) is comparable to the variation between variants
within a single run. This confirms the differences are system noise, not framework signal.

All variants comfortably within the p(99) < 50ms threshold.

## Test setup

- 6 variants running simultaneously, 20 VUs each (120 total concurrent users)
- 30 seconds per run
- 4 endpoints: POST /compute (40%), GET /compute list (20%), GET /compute/:name (20%), DELETE /compute/:name (20%)
- Servers restarted between runs to clear in-memory store
- Machine: Apple Silicon Mac (local development environment)
