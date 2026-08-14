package provider

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	alphaVantageDailyLimit  = 25
	alphaVantageQuotaWindow = 24 * time.Hour
)

var (
	alphaVantageShared     *SafeHTTPClient
	alphaVantageSharedOnce sync.Once
)

// SharedAlphaVantageClient keeps the daily budget across collector recreation.
// A database-backed limiter extends that protection across process restarts;
// otherwise the process-local limiter still protects reconnect loops.
func SharedAlphaVantageClient() *SafeHTTPClient {
	alphaVantageSharedOnce.Do(func() {
		alphaVantageShared = NewSafeHTTPClient(AlphaVantageConfig())
		limiter := ProviderLimiter(newRollingQuotaLimiter(alphaVantageDailyLimit, alphaVantageQuotaWindow))
		if databaseURL := os.Getenv("PROVIDER_QUOTA_DB_URL"); databaseURL != "" {
			pool, err := pgxpool.New(context.Background(), databaseURL)
			if err != nil {
				limiter = failingQuotaLimiter{}
			} else {
				limiter = NewDBBackedQuota(pool, alphaVantageDailyLimit, alphaVantageQuotaWindow)
			}
		}
		alphaVantageShared.SetProviderLimiter(limiter)
	})
	return alphaVantageShared
}

type rollingQuotaWindow struct {
	startedAt time.Time
	used      int
}

type rollingQuotaLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	windows map[string]rollingQuotaWindow
	now     func() time.Time
}

func newRollingQuotaLimiter(limit int, window time.Duration) *rollingQuotaLimiter {
	return &rollingQuotaLimiter{
		limit:   limit,
		window:  window,
		windows: make(map[string]rollingQuotaWindow),
		now:     time.Now,
	}
}

func (q *rollingQuotaLimiter) Reserve(ctx context.Context, provider string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if q.limit <= 0 || q.window <= 0 {
		return ErrProviderQuotaUnavailable
	}

	now := q.now()
	q.mu.Lock()
	defer q.mu.Unlock()
	current := q.windows[provider]
	if current.startedAt.IsZero() || now.Before(current.startedAt) || !now.Before(current.startedAt.Add(q.window)) {
		current = rollingQuotaWindow{startedAt: now}
	}
	if current.used >= q.limit {
		return ErrProviderQuotaExceeded
	}
	current.used++
	q.windows[provider] = current
	return nil
}

func (*rollingQuotaLimiter) RecordFailure(context.Context, string) error { return nil }
