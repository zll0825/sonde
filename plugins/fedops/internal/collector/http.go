package collector

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"sonde/pkg/model"
	"sonde/pkg/pluginrunner"
	"sonde/pkg/provider"
)

const maxBody = 10 << 20

func doGET(ctx context.Context, client *provider.SafeHTTPClient, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := provider.ReadAll(resp, 512)
		return nil, fmt.Errorf("%s returned %d: %s", req.URL.Path, resp.StatusCode, body)
	}
	return provider.ReadAll(resp, maxBody)
}

func parseDateUTC(s string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", s, time.UTC)
}

func dateUTC(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func snap(metricID string, value float64, ts, fetchedAt time.Time, providerName string) pluginrunner.Snapshot {
	return pluginrunner.Snapshot{
		MetricID:    metricID,
		Value:       value,
		Timestamp:   ts,
		FetchedAt:   fetchedAt,
		Provider:    providerName,
		SourceClass: model.SourceClassReal,
		Grade:       "delayed",
	}
}
