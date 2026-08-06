// Package event owns typed payload contracts carried by the database outbox.
package event

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// TypeDetectionRequested is emitted transactionally with an accepted
// observation and consumed by Core's detector pipeline.
const TypeDetectionRequested = "detection.requested"

// DetectionRequest is the durable observation-to-detection work contract.
type DetectionRequest struct {
	DetectionKey string `json:"detection_key"`
	MetricID     string `json:"metric_id"`
	MetricUID    string `json:"metric_uid"`
	PluginID     string `json:"plugin_id"`
}

// NewDetectionRequest builds a stable idempotency key from the observation's
// unique dimensions plus quality grade. A higher-grade correction therefore
// receives new detection work, while an exact replay does not.
func NewDetectionRequest(metricID, metricUID, pluginID, provider, labelsHash, grade string, observedAt int64) DetectionRequest {
	raw := fmt.Sprintf("%s\x1f%d\x1f%s\x1f%s\x1f%s\x1f%s",
		metricUID, observedAt, pluginID, provider, labelsHash, grade)
	sum := sha256.Sum256([]byte(raw))
	return DetectionRequest{
		DetectionKey: "observation:" + hex.EncodeToString(sum[:]),
		MetricID:     metricID,
		MetricUID:    metricUID,
		PluginID:     pluginID,
	}
}
