package provider

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// FRED's documented repository budget is approximately 120 requests/hour.
	// A 2400-request rolling 24h window leaves operational headroom.
	defaultFREDDailyLimit = 2400
	fredQuotaWindow       = 24 * time.Hour
)

var (
	fredShared     *SafeHTTPClient
	fredSharedOnce sync.Once

	ErrProviderQuotaExceeded    = errors.New("provider quota exceeded")
	ErrProviderQuotaUnavailable = errors.New("provider quota unavailable")
)

// ProviderLimiter atomically reserves cross-process request budget. Reserve is
// called immediately before each wire attempt, including retries. RecordFailure
// records provider/network failures without consuming another quota unit.
type ProviderLimiter interface {
	Reserve(ctx context.Context, provider string) error
	RecordFailure(ctx context.Context, provider string) error
}

// SharedFREDClient returns the process singleton used by all FRED collectors.
// When PROVIDER_QUOTA_DB_URL is configured, every process reserves requests
// from the same provider_quota rows before touching FRED.
func SharedFREDClient() *SafeHTTPClient {
	fredSharedOnce.Do(func() {
		fredShared = NewSafeHTTPClient(FREDConfig())
		if databaseURL := os.Getenv("PROVIDER_QUOTA_DB_URL"); databaseURL != "" {
			pool, err := pgxpool.New(context.Background(), databaseURL)
			if err != nil {
				fredShared.SetProviderLimiter(failingQuotaLimiter{})
				return
			}
			fredShared.SetProviderLimiter(NewDBBackedQuota(pool, defaultFREDDailyLimit, fredQuotaWindow))
		}
	})
	return fredShared
}

// DBBackedQuota serializes reservations per provider with a PostgreSQL
// transaction-level advisory lock. This prevents two plugin processes from
// both opening a new active window or spending the final quota unit.
type DBBackedQuota struct {
	db     *pgxpool.Pool
	limit  int
	window time.Duration
}

func NewDBBackedQuota(db *pgxpool.Pool, limit int, window time.Duration) *DBBackedQuota {
	return &DBBackedQuota{db: db, limit: limit, window: window}
}

func (q *DBBackedQuota) Reserve(ctx context.Context, provider string) error {
	if q.db == nil || q.limit <= 0 || q.window <= 0 {
		return ErrProviderQuotaUnavailable
	}
	tx, err := q.db.Begin(ctx)
	if err != nil {
		return ErrProviderQuotaUnavailable
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, provider); err != nil {
		return ErrProviderQuotaUnavailable
	}

	var windowStart time.Time
	var used int
	err = tx.QueryRow(ctx, `
		SELECT window_start, used
		FROM provider_quota
		WHERE provider = $1 AND window_end > NOW()
		ORDER BY window_end DESC
		LIMIT 1
		FOR UPDATE
	`, provider).Scan(&windowStart, &used)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if _, err := tx.Exec(ctx, `
			INSERT INTO provider_quota (provider, window_start, window_end, used)
			VALUES ($1, NOW(), NOW() + ($2 * INTERVAL '1 second'), 1)
		`, provider, int64(q.window/time.Second)); err != nil {
			return ErrProviderQuotaUnavailable
		}
	case err != nil:
		return ErrProviderQuotaUnavailable
	case used >= q.limit:
		return ErrProviderQuotaExceeded
	default:
		if _, err := tx.Exec(ctx, `
			UPDATE provider_quota SET used = used + 1, updated_at = NOW()
			WHERE provider = $1 AND window_start = $2
		`, provider, windowStart); err != nil {
			return ErrProviderQuotaUnavailable
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return ErrProviderQuotaUnavailable
	}
	return nil
}

func (q *DBBackedQuota) RecordFailure(ctx context.Context, provider string) error {
	if q.db == nil {
		return ErrProviderQuotaUnavailable
	}
	_, err := q.db.Exec(ctx, `
		UPDATE provider_quota SET failures = failures + 1, updated_at = NOW()
		WHERE provider = $1 AND window_end > NOW()
	`, provider)
	if err != nil {
		return ErrProviderQuotaUnavailable
	}
	return nil
}

// failingQuotaLimiter keeps a configured-but-invalid quota connection fail
// closed instead of silently reverting to independent process budgets.
type failingQuotaLimiter struct{}

func (failingQuotaLimiter) Reserve(context.Context, string) error {
	return ErrProviderQuotaUnavailable
}

func (failingQuotaLimiter) RecordFailure(context.Context, string) error { return nil }
