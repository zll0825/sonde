# Sonde — Go Code Conventions

## Project Structure

```
sonde/
├── cmd/
│   ├── core/main.go           # Core entry point
│   └── api/main.go            # API Server entry point
├── internal/
│   ├── core/                  # Core private implementation
│   │   ├── eventbus/          # Tier 2 async events
│   │   ├── pluginmgr/         # gRPC server + stream handler
│   │   ├── relationmgr/       # Relation review + versioning
│   │   ├── rulemgr/           # Rule review + versioning
│   │   ├── metric/            # Observation ingest + source preference
│   │   ├── ontology/          # Entity/Relation query
│   │   ├── detector/          # Detection engine
│   │   ├── alert/             # Alert generation + lifecycle
│   │   ├── scheduler/         # Cron + outbox retry
│   │   └── research/          # Snapshot mode assembler
│   └── api/
│       ├── handler/           # HTTP handlers
│       └── middleware/        # Logging, CORS, recovery
├── pkg/
│   ├── proto/plugin/v1/       # Generated proto Go code
│   ├── model/                 # Shared domain types
│   └── pluginrunner/          # Plugin gRPC client template
├── proto/plugin/v1/           # .proto source
├── plugins/                   # Each plugin is an independent Go module
│   ├── etf/
│   ├── crypto/
│   └── macro/
└── docs/                      # All documentation (frozen)
```

## Dependency Rules

```
Core    → PostgreSQL, Event Bus (internal)
API     → PostgreSQL (read-only)
Plugin  → gRPC client → Core
```

- Core never imports Plugin code.
- Plugin imports `pkg/proto` and `pkg/pluginrunner` only.
- API never imports Core internals.

## Naming

- Package names: lowercase, single word, no underscores. (`pluginmgr` not `plugin_mgr`)
- Interfaces: single-method interfaces preferred. Name = verb + er. (`Collector`, `Detector`)
- Error variables: `errXxx` prefix. `errInvalidMetricID`
- Test files: `xxx_test.go`, same package.

## Error Handling

- Never use `panic()` outside of `init()`.
- Always wrap errors with context: `fmt.Errorf("doing X: %w", err)`
- gRPC handlers return gRPC status codes, not raw errors.

## Logging

- Use `rs/zerolog`.
- Structured fields: `.Str("plugin_id", id)` `.Int("count", n)`
- Level guide:
  - Debug: internal state, fine-grained
  - Info: plugin connected, snapshot batch received, alert generated
  - Warn: plugin degraded, outbox retry, detector timeout
  - Error: plugin disconnected, DB error, stream broken

## Testing

- Table-driven tests preferred.
- Integration tests for DB queries use testcontainers or a local test DB.
- Mock gRPC clients with `gomock` or handwritten stubs.
