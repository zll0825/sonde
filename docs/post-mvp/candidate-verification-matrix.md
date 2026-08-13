# Post-MVP Candidate Verification Matrix

This matrix separates implementation status from release evidence. A child
task marked `complete` means its code/documentation scope was delivered; it does
not by itself prove browser, backup/restore, or real-source soak acceptance.

**Overall release decision: NOT READY.** The bounded candidate verification
passed the gates recorded below, but the gold source and partial-plugin-health
blockers must be resolved and reverified before release sign-off.

| Contract | Implementation | Automated evidence | Candidate verification | Release status |
|---|---|---|---|---|
| FRED shared quota and provider safety | Implemented | Provider unit tests, root/plugin race | Isolated migration + bounded provider run | Verified for FRED; other providers use client-local budgets |
| Cluster snapshot durability | Implemented | Cluster persistence and Core tests | Isolated API `/api/clusters/` smoke | Verified |
| Distinct/representative metrics | Implemented | Ontology DB integration tests | Restore schema probe | Verified |
| Candidate statistical evidence | Implemented | Ontology/relation tests | API candidate list/action smoke | Verified |
| Research feedback | Implemented | API handler tests, DB integration | Authenticated mutation smoke | Verified |
| Rule audit/version restore | Implemented | Rules handler tests, DB integration | Rules list/history/restore API smoke | Verified |
| Historical analogs and research overlays | Implemented | Research view tests | Real alert produced an immutable research snapshot; browser Research controls verified | Verified |
| Alert classification and signal quality | Implemented | Classification/detector/signal tests and race | Real-source run produced one alert, one research snapshot, and no failed outbox | Verified for bounded candidate run |
| ETF/Crypto/Macro real-data coverage | Implemented | Collector tests and backfill fixtures | Yahoo 2 metrics, Crypto 4 metrics, Macro 6 metrics emitted real observations | Verified for bounded candidate run |
| Commodities plugin | Partial real coverage | Module tests, build, race | WTI and copper succeeded; FRED LBMA gold series returned HTTP 400 | Blocked: replace/retire gold source and surface partial failure in plugin health |
| Post-MVP frontend views | Implemented | Inline JS syntax and API contract checks | Chrome 150 desktop/mobile, empty and populated data, five post-MVP tabs | Verified after mobile overflow/favicon fix |
| Migrations 1-10 | Implemented | DB integration suite | Up/down and backup/restore on disposable DB | Verified |
| Full workspace quality gate | Implemented | lint, build, full DB tests, root/plugin race | Dedicated empty v10 gate database; no skipped integration suite | Verified |
| MVP soak deployment | Frozen | Existing `v0.1.0` release evidence | Read-only health baseline only | Must remain unchanged |

## Evidence Rules

- `Verified` requires a reproducible command or captured isolated result.
- `Pending` means the implementation may exist, but the requested release gate
  has not passed.
- Synthetic observations never count as real-source evidence.
- Provider quota rows are expected only for FRED's configured persistent quota;
  other providers require client safety/degraded-state evidence.
- The implemented health route is `GET /api/health`.
- Database integration tests require a separate empty migrated database because
  their fixture cleanup truncates shared tables; soak and restore evidence
  databases are never valid test targets.

## Current Release Blockers

1. FRED no longer serves the configured LBMA gold series. The collector skips
   the metric without mock fallback, but the commodities dimension is incomplete.
2. A partial collector failure is logged but not retained in
   `plugins.last_collect_error`; the plugin remains `healthy=true`, so the API
   cannot explain why the gold metric is red.

Neither blocker is waived by the successful bounded run. This candidate must
not be promoted until both are resolved and the affected real-source and
plugin-health checks are repeated.
