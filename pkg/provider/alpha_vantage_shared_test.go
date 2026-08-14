package provider

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAlphaVantageQuotaMatchesFreeTier(t *testing.T) {
	if alphaVantageDailyLimit != 25 || alphaVantageQuotaWindow != 24*time.Hour {
		t.Fatalf("Alpha Vantage quota = %d/%s, want 25/24h", alphaVantageDailyLimit, alphaVantageQuotaWindow)
	}
}

func TestRollingQuotaLimiterEnforcesAndResetsWindow(t *testing.T) {
	now := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	limiter := newRollingQuotaLimiter(2, 24*time.Hour)
	limiter.now = func() time.Time { return now }

	if err := limiter.Reserve(context.Background(), "alpha_vantage"); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if err := limiter.Reserve(context.Background(), "alpha_vantage"); err != nil {
		t.Fatalf("second reserve: %v", err)
	}
	if err := limiter.Reserve(context.Background(), "alpha_vantage"); !errors.Is(err, ErrProviderQuotaExceeded) {
		t.Fatalf("third reserve = %v, want ErrProviderQuotaExceeded", err)
	}
	if err := limiter.Reserve(context.Background(), "fred"); err != nil {
		t.Fatalf("provider-isolated reserve: %v", err)
	}

	now = now.Add(24 * time.Hour)
	if err := limiter.Reserve(context.Background(), "alpha_vantage"); err != nil {
		t.Fatalf("reserve after window reset: %v", err)
	}
}

func TestRollingQuotaLimiterHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	limiter := newRollingQuotaLimiter(25, 24*time.Hour)
	if err := limiter.Reserve(ctx, "alpha_vantage"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Reserve = %v, want context.Canceled", err)
	}
}

func TestDBBackedQuotaEnforcesPersistentLimit(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	const providerName = "alpha_vantage_quota_test"
	if _, err := pool.Exec(ctx, `DELETE FROM provider_quota WHERE provider = $1`, providerName); err != nil {
		t.Fatalf("clean quota fixture: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM provider_quota WHERE provider = $1`, providerName); err != nil {
			t.Errorf("clean quota fixture after test: %v", err)
		}
	})

	limiter := NewDBBackedQuota(pool, 2, 24*time.Hour)
	if err := limiter.Reserve(ctx, providerName); err != nil {
		t.Fatalf("first persistent reserve: %v", err)
	}
	if err := limiter.Reserve(ctx, providerName); err != nil {
		t.Fatalf("second persistent reserve: %v", err)
	}
	if err := limiter.Reserve(ctx, providerName); !errors.Is(err, ErrProviderQuotaExceeded) {
		t.Fatalf("third persistent reserve = %v, want ErrProviderQuotaExceeded", err)
	}

	var used int
	if err := pool.QueryRow(ctx, `SELECT used FROM provider_quota WHERE provider = $1`, providerName).Scan(&used); err != nil {
		t.Fatalf("read persistent quota: %v", err)
	}
	if used != 2 {
		t.Fatalf("persistent quota used = %d, want 2", used)
	}
}
