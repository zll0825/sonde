package event

import (
	"errors"
	"strings"
)

// TypeResearchRequested is emitted transactionally with a new alert and
// consumed by Core's research assembler.
const TypeResearchRequested = "research.requested"

// ResearchRequest is the durable alert-to-research work contract. AlertID is
// also used as the outbox deduplication key because an alert owns one immutable
// research snapshot.
type ResearchRequest struct {
	AlertID string `json:"alert_id"`
}

// NewResearchRequest creates research work for one persisted alert.
func NewResearchRequest(alertID string) ResearchRequest {
	return ResearchRequest{AlertID: alertID}
}

// Validate rejects malformed durable work before it reaches persistence code.
func (r ResearchRequest) Validate() error {
	if strings.TrimSpace(r.AlertID) == "" {
		return errors.New("research request alert_id is required")
	}
	return nil
}
