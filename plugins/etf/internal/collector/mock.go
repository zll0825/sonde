package collector

import (
	"context"
	"math/rand"
	"time"
)

// Provider fetches ETF flow snapshots.
type Provider interface {
	GetSnapshots(ctx context.Context) ([]Snapshot, error)
}

// Snapshot is one observed metric value.
type Snapshot struct {
	MetricID  string
	Value     float64
	Timestamp time.Time
	Provider  string
	Grade     string // Quality grade: "realtime", "delayed", "estimated", "preliminary", "revised".
}

// Mock returns deterministic-looking but slightly jittered ETF data.
type Mock struct{}

func (Mock) GetSnapshots(ctx context.Context) ([]Snapshot, error) {
	now := time.Now()
	return []Snapshot{
		{
			MetricID:  "gld_daily_flow",
			Value:     650_000_000 + rand.Float64()*10_000_000, // ~650M–660M, fires threshold
			Timestamp: now,
			Provider:  "mock_etf",
			Grade:     "estimated",
		},
		{
			MetricID:  "gld_price",
			Value:     2150.50 + rand.Float64()*10,
			Timestamp: now,
			Provider:  "mock_etf",
			Grade:     "estimated",
		},
	}, nil
}
