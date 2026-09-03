// Package fred is the shared FRED pull implementation. Plugin packages
// supply bindings; they must not fork pagination, window caps, or HTTP.
package fred

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"capital_observatory/pkg/provider"
)

const (
	ProviderName = "fred"

	// MaxHistoricalWindow is the maximum span GetSnapshotsForWindow honors.
	MaxHistoricalWindow   = 3650 * 24 * time.Hour
	maxFREDPerPage        = 10000
	maxFREDTotal          = 100000
	defaultLatestLookback = 30 * 24 * time.Hour
)

// Binding maps one Observatory metric to a FRED series.
type Binding struct {
	MetricID  string  `yaml:"metric"`
	SeriesID  string  `yaml:"series"`
	UnitScale float64 `yaml:"scale"`
	Frequency string  `yaml:"frequency"`
	Units     string  `yaml:"units"`
}

type bindingsFile struct {
	Provider           string    `yaml:"provider"`
	LatestLookbackDays int       `yaml:"latestLookbackDays"`
	Series             []Binding `yaml:"series"`
}

// LoadBindings parses a plugin bindings.yaml. Scale defaults to 1 when omitted.
func LoadBindings(yamlBytes []byte) ([]Binding, error) {
	var file bindingsFile
	if err := yaml.Unmarshal(yamlBytes, &file); err != nil {
		return nil, fmt.Errorf("parse FRED bindings: %w", err)
	}
	if file.Provider != "" && file.Provider != ProviderName {
		return nil, fmt.Errorf("bindings provider %q is not %s", file.Provider, ProviderName)
	}
	if len(file.Series) == 0 {
		return nil, fmt.Errorf("bindings.yaml has no series")
	}
	out := make([]Binding, 0, len(file.Series))
	for i, b := range file.Series {
		if b.MetricID == "" || b.SeriesID == "" {
			return nil, fmt.Errorf("bindings series[%d] needs metric and series", i)
		}
		if b.UnitScale == 0 {
			b.UnitScale = 1
		}
		if b.Frequency == "" {
			b.Frequency = "daily"
		}
		out = append(out, b)
	}
	return out, nil
}

// LatestLookbackFromYAML returns the current-snapshot lookback encoded in
// bindings.yaml, or the 30-day default.
func LatestLookbackFromYAML(yamlBytes []byte) (time.Duration, error) {
	var file bindingsFile
	if err := yaml.Unmarshal(yamlBytes, &file); err != nil {
		return 0, fmt.Errorf("parse FRED bindings: %w", err)
	}
	if file.LatestLookbackDays <= 0 {
		return defaultLatestLookback, nil
	}
	return time.Duration(file.LatestLookbackDays) * 24 * time.Hour, nil
}

// RequireAPIKey fails fast so a missing key cannot start a mock under the
// "fred" provider name.
func RequireAPIKey() (string, error) {
	key := os.Getenv("FRED_API_KEY")
	if key == "" {
		return "", fmt.Errorf("FRED_API_KEY env var is required but not set")
	}
	return key, nil
}

// SharedClient is the process-wide FRED HTTP client (quota + circuit).
func SharedClient() *provider.SafeHTTPClient {
	return provider.SharedFREDClient()
}
