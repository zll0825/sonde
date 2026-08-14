# Post-MVP Candidate Verification Matrix

This matrix separates implementation status from release evidence. A child
task marked `complete` means its code/documentation scope was delivered; it does
not by itself prove browser, backup/restore, or real-source soak acceptance.

**Overall release decision: READY FOR SIGN-OFF.** Gold replacement,
partial-plugin-health recovery, persistent provider quota, full workspace/race
gates, migration rollback, and isolated backup/restore are verified.

| Contract | Implementation | Automated evidence | Candidate verification | Release status |
|---|---|---|---|---|
| Shared provider quota and safety | Implemented | Unit/DB quota tests and root race | Final-code current/history created Alpha reservations; bad quota DB failed closed | Verified for FRED and Alpha Vantage |
| Cluster snapshot durability | Implemented | Cluster persistence and Core tests | Isolated API `/api/clusters/` smoke | Verified |
| Distinct/representative metrics | Implemented | Ontology DB integration tests | Restore schema probe | Verified |
| Candidate statistical evidence | Implemented | Ontology/relation tests | API candidate list/action smoke | Verified |
| Research feedback | Implemented | API handler tests, DB integration | Authenticated mutation smoke | Verified |
| Rule audit/version restore | Implemented | Rules handler tests, DB integration | Rules list/history/restore API smoke | Verified |
| Historical analogs and research overlays | Implemented | Research view tests | Real alert produced an immutable research snapshot; browser Research controls verified | Verified |
| Alert classification and signal quality | Implemented | Classification/detector/signal tests and race | Real-source run produced one alert, one research snapshot, and no failed outbox | Verified for bounded candidate run |
| ETF/Crypto/Macro real-data coverage | Implemented | Collector tests and backfill fixtures | Yahoo 2 metrics, Crypto 4 metrics, Macro 6 metrics emitted real observations | Verified for bounded candidate run |
| Commodities plugin | Alpha Vantage XAUUSD spot/history adapter implemented | Focused provider/composition tests and Commodities race gate | Isolated three-metric current collection, bounded gold history, deterministic partial failure, recovery, and short soak; 0 mock/test rows | Verified; copper freshness remains truthfully red due monthly publication lag |
| Post-MVP frontend views | Implemented | Inline JS syntax and API contract checks | Chrome 150 desktop/mobile, empty and populated data, five post-MVP tabs | Verified after mobile overflow/favicon fix |
| Migrations 1-11 | Implemented | DB integration suite | `11→10→11`, `11→6→11`, and populated v11 backup/restore on disposable DBs | Verified |
| Full workspace quality gate | Implemented | lint, build, full DB tests, root/all-plugin race | Dedicated empty v11 gate database; no skipped integration suite | Verified |
| MVP soak deployment | Frozen | Existing `v0.1.0` release evidence | Read-only health baseline only | Must remain unchanged |

## Evidence Rules

- `Verified` requires a reproducible command or captured isolated result.
- `Pending` means the implementation may exist, but the requested release gate
  has not passed.
- Synthetic observations never count as real-source evidence.
- Provider quota rows are expected for FRED and Alpha Vantage when their shared
  clients run with `PROVIDER_QUOTA_DB_URL`; other providers require client
  safety/degraded-state evidence.
- The implemented health route is `GET /api/health`.
- Database integration tests require a separate empty migrated database because
  their fixture cleanup truncates shared tables; soak and restore evidence
  databases are never valid test targets.

## Current Release Blockers

None. Preserve the verification boundary: destructive integration tests remain
restricted to dedicated empty databases, and the live MVP deployment remains
unchanged until an explicit promotion decision.
