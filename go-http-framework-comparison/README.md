# Go HTTP Framework Comparison

Six variants — same API spec, same business logic, different framework + code generation combinations.

## Variants

| # | Framework | Mode | Port | Error handler wiring |
|---|-----------|------|------|---------------------|
| 01 | Echo | Standard (manual) | 9001 | `e.HTTPErrorHandler` — one function, automatic |
| 02 | Echo | Strict (oapi-codegen) | 9002 | `e.HTTPErrorHandler` — same, works natively with strict mode |
| 03 | Chi | Standard (manual) | 9003 | `writeError()` helper — must be called manually in every handler |
| 04 | Chi | Strict (oapi-codegen) | 9004 | `StrictHTTPServerOptions.ResponseErrorHandlerFunc` — extra wiring |
| 05 | Gin | Standard (manual) | 9005 | `c.Error(err)` + middleware — easy to forget `c.Error()` |
| 06 | Gin | Strict (oapi-codegen) | 9006 | 3 separate callbacks — Request/Handler/ResponseErrorHandlerFunc |

All variants share the same OpenAPI spec (`spec/openapi.yaml`) and domain logic (`shared/`).

## API

```
POST   /compute          — submit provisioning (body: name, tenantId, provider, size)
GET    /compute          — list instances (query: tenantId, limit)
GET    /compute/{name}   — get instance (404 if not found)
DELETE /compute/{name}   — delete instance (404 if not found, 409 if already deleting)
```

Error response shape (consistent across all 6 variants):
```json
{ "code": "NOT_FOUND", "message": "compute instance \"x\" not found", "requestId": "abc-123" }
```

## Setup

```sh
make build   # build all 6 variants
make run     # start all 6 servers in background
make stop    # stop all servers
```

## Tests

**Error handler scenarios (k6):**
```sh
k6 run k6/error-scenarios.js
```
Verifies that all variants return consistent `{code, message, requestId}` shape for 400/404/409 errors and don't leak internal Go error strings.

**Load test (k6):**
```sh
k6 run k6/load-test.js
```
50 VUs × 30s per variant, all 4 endpoints. Measures p99 latency per variant.

## Results

### Load test — p99 latency (50 VUs, 30s, ~1.46M req/variant)

| Variant | p99 | avg | Δ from fastest |
|---------|-----|-----|----------------|
| gin-standard | 1.21ms | 1.00ms | — |
| gin-strict | 1.23ms | 1.00ms | +0.02ms |
| chi-standard | 1.25ms | 1.01ms | +0.04ms |
| chi-strict | 1.29ms | 1.01ms | +0.08ms |
| echo-strict | 1.30ms | 1.00ms | +0.09ms |
| echo-standard | 1.36ms | 0.96ms | +0.15ms |

**The difference between the fastest and slowest is 0.15ms.** Router performance is not a meaningful selection criterion for UCP — the workflow behind the endpoint takes 20-30 minutes.

### Error handler scenarios — all 6 variants

All variants return consistent `{code, message, requestId}` shape for all error cases. No internal error strings leaked.

**Key findings:**

**FINDING-1 — Enum validation not auto-enforced in strict mode:**
`oapi-codegen` does not validate `enum:` constraints at JSON binding time. A `provider: "azure"` (not in spec) passes through to the handler silently. Strict mode only enforces required field types, not enum values. Manual validation is required in handlers for all framework+mode combinations.

**FINDING-2 — Unmatched route 404 response format:**
Only Echo routes unmatched routes through `HTTPErrorHandler`, returning JSON. Chi and Gin return plain text `"404 page not found"` by default. Consistent JSON 404s on chi/gin require adding a custom `NotFound` handler.

**FINDING-3 — Global error handler complexity per framework+mode:**

| | Standard mode | Strict mode |
|---|---|---|
| **Echo** | 1 `HTTPErrorHandler` | 1 `HTTPErrorHandler` — works natively, errors return to framework |
| **Chi** | `writeError()` helper (manual call required) | 1 `StrictHTTPServerOptions.ResponseErrorHandlerFunc` |
| **Gin** | `c.Error(err)` + middleware | 3 callbacks: `RequestErrorHandlerFunc` + `HandlerErrorFunc` + `ResponseErrorHandlerFunc` |

Echo strict mode is the only combination where errors flow to a central handler **without any extra wiring** — the generated strict wrapper returns errors to the framework rather than handling them inline.

## Structure

```
.
├── spec/
│   └── openapi.yaml              # single shared spec for all 6 variants
├── shared/
│   └── domain.go                 # DomainError, Store, error constructors (shared logic)
├── 01-echo-standard/             # echo, manual handlers
├── 02-echo-strict/               # echo + oapi-codegen strict mode
│   └── oapi-codegen.yaml
├── 03-chi-standard/              # chi, manual handlers
├── 04-chi-strict/                # chi + oapi-codegen strict mode
│   └── oapi-codegen.yaml
├── 05-gin-standard/              # gin, manual handlers
├── 06-gin-strict/                # gin + oapi-codegen strict mode
│   └── oapi-codegen.yaml
├── k6/
│   ├── load-test.js              # throughput + latency benchmark
│   └── error-scenarios.js        # error handler consistency test
└── Makefile
```
