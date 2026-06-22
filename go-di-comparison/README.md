# Go Dependency Injection Comparison

Three approaches to dependency injection applied to the same UCP-like service:
feature slices architecture, oapi-codegen strict mode, realistic dependency graph.

## Service graph under test

```
Config
  ├── K8sClient      (platform layer)
  └── TemporalClient (platform layer)
        ├── ComputeService  → ComputeHandler
        └── DatabaseService → DatabaseHandler
                                  ↓
                            handlers (StrictServerInterface)
                                  ↓
                         api.RegisterHandlers (oapi-codegen)
```

9 components total — representative of UCP MVP scale (not a toy hello world,
not a complex monolith with hundreds of dependencies).

## Variants

| # | Approach | Wiring mechanism |
|---|---------|-----------------|
| 01 | Manual constructor injection | Plain Go function calls in `buildApp()` |
| 02 | uber/fx | Reflection-based container — `fx.Provide` + `fx.Invoke` |
| 03 | google/wire | Code generation — `wire.Build` in `wire.go` → generates `wire_gen.go` |

All variants share:
- Identical `internal/` feature slices (compute, database, platform)
- Identical OpenAPI spec → same oapi-codegen generated code
- The DI mechanism ONLY affects `cmd/api-server/main.go` (and `wire.go` for 03)

## Quantitative results

### Scenario A — Initial wiring (2 feature slices)

| | 01-manual | 02-uber-fx | 03-wire |
|---|---|---|---|
| Wiring lines | 8 | 9 | 8 |
| Files involved in wiring | 1 (`main.go`) | 1 (`main.go`) | 2 (`wire.go` + generated `wire_gen.go`) |

All three are equivalent in initial wiring cost. The difference is not in line count.

### Scenario B — Add 1 new feature slice (e.g. storage)

New files required in all 3: `internal/storage/service.go` + `internal/storage/handler.go` (identical across approaches)

| | 01-manual | 02-uber-fx | 03-wire |
|---|---|---|---|
| Lines added to wiring file | ~3 | ~2 | ~2 in `wire.go` |
| Extra steps | none | none | run `wire gen` |
| Files changed | `main.go` | `main.go` | `wire.go` → `wire_gen.go` regenerated |

All three require the same number of code changes. wire requires an extra generate step.

### Scenario C — Missing dependency bug

Forget to wire `TemporalClient`:

**01-manual** — build fails immediately:
```
cmd/api-server/main.go:56:35: not enough arguments in call to compute.NewService
	have (shared.K8sClient)
	want (shared.K8sClient, shared.TemporalClient)
```

**02-uber-fx** — build succeeds, error at `app.Run()`:
```
[Fx] ERROR  missing dependencies for function "compute".NewService
            missing type: shared.TemporalClient
```

**03-wire** — `wire gen` fails (before compile):
```
wire: no provider found for shared.TemporalClient
      needed by *compute.Service in provider "NewService"
      needed by *compute.Handler in provider "NewHandler"
      needed by *handlers in provider "newHandlers"
```

### Dependency footprint

| | 01-manual | 02-uber-fx | 03-wire |
|---|---|---|---|
| New direct runtime deps | 0 | 1 (`go.uber.org/fx`) | 0 |
| New transitive runtime deps | 0 | 14 (`dig`, `zap`, `multierr`, ...) | 0 |
| Packages actually used in app code | — | `go.uber.org/fx` only | — |
| Build tool added | none | none | `wire` (dev only, not in binary) |

**uber/fx note:** fx pulls `go.uber.org/dig` (the reflection engine), `go.uber.org/zap`
(structured logging), `go.uber.org/multierr`, and others as transitive deps.
These ship in your binary but you never import them directly — they are fx internals.

**wire note:** `google/wire` is a build tool only. The generated `wire_gen.go` contains
plain Go constructor calls with zero runtime imports. Runtime footprint = 0.

## Qualitative findings

### Does uber/fx bring benefit to UCP?

**No** — not at UCP's scale. The key question is: what problem does a DI container solve?

A DI container solves **graph complexity** — when you have dozens of components with
non-obvious dependency relationships, a container that resolves types automatically
reduces cognitive overhead and prevents wiring mistakes.

UCP MVP has ~10-15 components. The graph is shallow and mechanical:
every feature service takes the same 2-3 shared dependencies (K8sClient, TemporalClient).
At this scale, the `buildApp()` function in `main.go` is more readable than an fx app —
reading top-to-bottom shows the exact dependency graph in 10 lines.

Additional costs with uber/fx at UCP's scale:
- Runtime dependency: 14 transitive packages ship in your binary for a graph you could
  write by hand in 10 minutes
- Error discovery moved to runtime: the most dangerous class of wiring mistakes
  (missing dependency) only surfaces when the app starts, not at compile time
- AI friction: AI must know fx conventions (Provide/Invoke, fx.In struct tags) to
  correctly modify the wiring. With manual injection, AI just follows the Go function
  call pattern already in the file.

**When fx IS the right choice:** services with 50+ components, complex lifecycle
management needs (ordered shutdown, health-gated startup), or teams already invested
in the fx ecosystem.

### Is manual injection too verbose or a hassle?

At UCP's scale: **no**. The boilerplate per new feature slice is:

```go
// Add to buildApp() in main.go — 2 lines per new feature
storageSvc := storage.NewService(k8s, temporal)
storageH   := storage.NewHandler(storageSvc)
```

Plus delegation methods in the `handlers` struct, which are identical regardless of
DI approach (they exist because oapi-codegen requires a single StrictServerInterface
implementation). The DI framework does not change this.

The "copy-paste boilerplate" concern is real but applies to the delegation methods, not
to the wiring itself. All three approaches require the same delegation methods.

### google/wire — why it's an instant rejection

wire is in maintenance mode since ~2022. The repository is open but no new features
are being added. Introducing a build tool with no active development into the CI
pipeline is not justified. The generated code is excellent — plain Go constructor calls,
zero runtime overhead, graph errors caught at generate time — but the tool itself is
not actively maintained.

If code-generation DI were required at UCP's scale (it isn't), wire would be the
technically superior choice over fx. But the maintenance status makes it a non-starter.

### DI choice and oapi-codegen compatibility

All three approaches are fully compatible with oapi-codegen strict mode.
The DI framework constructs the `handlers` struct; oapi-codegen generates the
`StrictServerInterface` and routing wrapper. They are independent concerns.
The `handlers` struct, delegation methods, and `api.RegisterHandlers` call are
identical across all three variants — only `buildApp()` / `fx.New()` / `wire.Build()`
differs.

### AI-assisted development

| | 01-manual | 02-uber-fx | 03-wire |
|---|---|---|---|
| Framework knowledge required | None — plain Go | fx.Provide/Invoke conventions | wire.Build + wireinject tag |
| Graph visible in one file? | Yes — `buildApp()` | Implicit — fx resolves at runtime | Yes — `wire.go` |
| Risk of AI mistake | Low | Medium | Medium |
| AI mistake type | Wrong argument order (caught at compile) | Wrong/missing Provide (caught at runtime) | Edit wire_gen.go directly instead of wire.go |

Manual injection is the safest choice for AI-assisted development. The pattern is
idiomatic Go with no framework conventions to learn. AI sees the full graph in one
function and replicates the pattern mechanically. Mistakes are caught at compile time.

## Verdict

For UCP MVP: **01-manual**.

The dependency graph is simple enough that manual wiring is the clearest option.
No runtime overhead, no new transitive dependencies, compile-time error detection,
and AI-friendly. The "hassle" of manual wiring at UCP's scale is approximately 2 lines
per new feature — not a meaningful cost.

Revisit if the component count grows beyond ~30 and `main.go` becomes difficult to
read. At that point, wire (if it regains maintenance) or a re-evaluated fx would be
worth considering.
