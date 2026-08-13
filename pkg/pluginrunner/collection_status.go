package pluginrunner

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	pb "capital_observatory/pkg/proto/plugin/v1"
)

const maxCollectionErrorLength = 512

var (
	collectionURLPattern    = regexp.MustCompile(`(?i)https?://[^\s]+`)
	collectionBearerPattern = regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]+`)
	collectionSecretPattern = regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|refresh[_-]?token|token|secret|password)(["']?\s*[:=]\s*["']?)[^"'\s&,;}]+`)
	collectionSpacePattern  = regexp.MustCompile(`\s+`)
)

// CollectionFailure identifies one failed metric/provider operation while
// allowing sibling snapshots to remain usable.
type CollectionFailure struct {
	MetricID string
	Provider string
	Err      error
}

// SummarizeCollectionFailures returns one bounded, sanitized error suitable
// for heartbeat persistence, command ACKs, and logs.
func SummarizeCollectionFailures(failures []CollectionFailure) error {
	if len(failures) == 0 {
		return nil
	}
	parts := make([]string, 0, len(failures))
	for _, failure := range failures {
		metricID := sanitizeCollectionIdentity(failure.MetricID)
		provider := sanitizeCollectionIdentity(failure.Provider)
		identity := metricID
		if provider != "" {
			identity = provider + "/" + metricID
		}
		if identity == "" {
			identity = "collection"
		}
		parts = append(parts, fmt.Sprintf("%s: %s", identity, SanitizeCollectionError(failure.Err)))
	}
	return errors.New(boundCollectionError(strings.Join(parts, "; ")))
}

func sanitizeCollectionIdentity(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return SanitizeCollectionError(errors.New(value))
}

// SanitizeCollectionError removes credential-bearing material and bounds the
// result before it crosses a process boundary or enters a durable log/store.
func SanitizeCollectionError(err error) string {
	if err == nil {
		return "collection failed"
	}
	message := err.Error()
	message = collectionURLPattern.ReplaceAllString(message, "[redacted-url]")
	message = collectionBearerPattern.ReplaceAllString(message, "Bearer [redacted]")
	message = collectionSecretPattern.ReplaceAllString(message, "$1$2[redacted]")
	message = collectionSpacePattern.ReplaceAllString(message, " ")
	message = strings.TrimSpace(message)
	if message == "" {
		message = "collection failed"
	}
	return boundCollectionError(message)
}

func boundCollectionError(message string) string {
	if len(message) <= maxCollectionErrorLength {
		return message
	}
	return message[:maxCollectionErrorLength-3] + "..."
}

type collectionStatus struct {
	mu sync.RWMutex

	recorded            bool
	lastCompletedAt     time.Time
	lastCollectAt       int64
	lastCollectDuration int32
	lastCollectCount    int32
	lastCollectError    string
	consecutiveErrors   int32
}

func (s *collectionStatus) execute(collect func() ([]Snapshot, error)) ([]Snapshot, error) {
	startedAt := time.Now()
	snapshots, err := collect()
	s.record(startedAt, time.Now(), len(snapshots), err)
	return snapshots, err
}

func (s *collectionStatus) record(startedAt, completedAt time.Time, count int, err error) {
	duration := completedAt.Sub(startedAt).Milliseconds()
	if duration < 0 {
		duration = 0
	}
	if duration > math.MaxInt32 {
		duration = math.MaxInt32
	}
	if count > math.MaxInt32 {
		count = math.MaxInt32
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.lastCompletedAt.IsZero() && completedAt.Before(s.lastCompletedAt) {
		return
	}
	s.recorded = true
	s.lastCompletedAt = completedAt
	s.lastCollectDuration = int32(duration)
	s.lastCollectCount = int32(count)
	if err == nil {
		s.lastCollectAt = completedAt.Unix()
		s.lastCollectError = ""
		s.consecutiveErrors = 0
		return
	}
	if count > 0 {
		s.lastCollectAt = completedAt.Unix()
	}
	s.lastCollectError = SanitizeCollectionError(err)
	if s.consecutiveErrors < math.MaxInt32 {
		s.consecutiveErrors++
	}
}

func (s *collectionStatus) snapshot(runtime map[string]string) *pb.PluginStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.recorded && len(runtime) == 0 {
		return nil
	}
	status := &pb.PluginStatus{
		State:                 "running",
		LastCollectAt:         s.lastCollectAt,
		LastCollectDurationMs: s.lastCollectDuration,
		LastCollectCount:      s.lastCollectCount,
		LastCollectError:      s.lastCollectError,
		ConsecutiveErrors:     s.consecutiveErrors,
	}
	if len(runtime) > 0 {
		status.Runtime = make(map[string]string, len(runtime))
		for key, value := range runtime {
			status.Runtime[key] = value
		}
	}
	return status
}
