package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"sonde/internal/core/ontology"
	"sonde/pkg/model"
)

// ReadOnlyTools defines the complete catalog of tools exposed by the MCP server.
// No write/mutation tools are ever permitted in this slice.
var ReadOnlyTools = []Tool{
	{
		Name:        "list_active_alerts",
		Description: "List active capital market anomaly alerts, ordered by triggered_at descending.",
		InputSchema: ToolInputSchema{
			Type: "object",
			Properties: map[string]ToolProperty{
				"limit": {
					Type:        "integer",
					Description: "Maximum number of alerts to return (default 50, max 200).",
				},
			},
		},
	},
	{
		Name:        "get_research_snapshot",
		Description: "Fetch the frozen research snapshot for a specific alert ID, including trend, timeline, overlays, and historical analogs.",
		InputSchema: ToolInputSchema{
			Type: "object",
			Properties: map[string]ToolProperty{
				"alert_id": {
					Type:        "string",
					Description: "The unique ID of the alert (e.g. alt_xxx).",
				},
			},
			Required: []string{"alert_id"},
		},
	},
	{
		Name:        "list_ontology_relations",
		Description: "List active ontology entity relations. Optionally filter by entity_id.",
		InputSchema: ToolInputSchema{
			Type: "object",
			Properties: map[string]ToolProperty{
				"entity_id": {
					Type:        "string",
					Description: "Optional entity ID to filter relations (matches source or target).",
				},
			},
		},
	},
	{
		Name:        "list_ontology_candidates",
		Description: "List discovered ontology relation candidates/suggestions.",
		InputSchema: ToolInputSchema{
			Type: "object",
			Properties: map[string]ToolProperty{
				"status": {
					Type:        "string",
					Description: "Filter candidate status (e.g. 'pending', 'accepted', 'rejected'). Default is 'pending'.",
				},
			},
		},
	},
}

// ToolHandler executes an MCP tool and returns the JSON or text response.
type ToolHandler func(ctx context.Context, args map[string]any) (string, error)

type ToolRegistry struct {
	db          *pgxpool.Pool
	store       *ontology.Store
	relationMgr *ontology.RelationManager
	overrides   map[string]ToolHandler
}

// NewToolRegistry initializes standard read-only tool handlers backed by PostgreSQL.
func NewToolRegistry(db *pgxpool.Pool) *ToolRegistry {
	reg := &ToolRegistry{
		db:        db,
		overrides: make(map[string]ToolHandler),
	}
	if db != nil {
		reg.store = ontology.NewStore(db)
		reg.relationMgr = ontology.NewRelationManager(reg.store)
	}
	return reg
}

// Execute dispatches a tool call to the corresponding handler.
func (r *ToolRegistry) Execute(ctx context.Context, name string, args map[string]any) (string, error) {
	if handler, ok := r.overrides[name]; ok {
		return handler(ctx, args)
	}

	switch name {
	case "list_active_alerts":
		return r.handleListActiveAlerts(ctx, args)
	case "get_research_snapshot":
		return r.handleGetResearchSnapshot(ctx, args)
	case "list_ontology_relations":
		return r.handleListOntologyRelations(ctx, args)
	case "list_ontology_candidates":
		return r.handleListOntologyCandidates(ctx, args)
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func (r *ToolRegistry) handleListActiveAlerts(ctx context.Context, args map[string]any) (string, error) {
	if r.db == nil {
		return "", errors.New("database not connected")
	}

	limit := 50
	if raw, ok := args["limit"]; ok {
		switch v := raw.(type) {
		case float64:
			limit = int(v)
		case int:
			limit = v
		}
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	rows, err := r.db.Query(ctx, `
		SELECT id, title, summary, severity, metric_id, triggered_at, status,
		       source_provider, source_class, dedup_count, last_deduplicated_at
		FROM alerts WHERE status = 'active'
		ORDER BY triggered_at DESC LIMIT $1
	`, limit)
	if err != nil {
		return "", fmt.Errorf("query alerts: %w", err)
	}
	defer rows.Close()

	var alerts []model.Alert
	for rows.Next() {
		var a model.Alert
		if err := rows.Scan(
			&a.ID, &a.Title, &a.Summary, &a.Severity, &a.MetricID,
			&a.TriggeredAt, &a.Status, &a.SourceProvider, &a.SourceClass,
			&a.DedupCount, &a.LastDeduplicatedAt,
		); err != nil {
			return "", fmt.Errorf("scan alert: %w", err)
		}
		alerts = append(alerts, a)
	}
	if alerts == nil {
		alerts = []model.Alert{}
	}

	data, err := json.MarshalIndent(alerts, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal alerts: %w", err)
	}
	return string(data), nil
}

func (r *ToolRegistry) handleGetResearchSnapshot(ctx context.Context, args map[string]any) (string, error) {
	if r.db == nil {
		return "", errors.New("database not connected")
	}

	alertID, _ := args["alert_id"].(string)
	alertID = strings.TrimSpace(alertID)
	if alertID == "" {
		return "", errors.New("alert_id is required")
	}

	var ctxData []byte
	var frozenAt time.Time
	err := r.db.QueryRow(ctx, `
		SELECT context, ontology_frozen_at FROM research_snapshots WHERE alert_id = $1
	`, alertID).Scan(&ctxData, &frozenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("research context not found for alert_id: %s", alertID)
	}
	if err != nil {
		return "", fmt.Errorf("query research snapshot: %w", err)
	}

	var result map[string]any
	if err := json.Unmarshal(ctxData, &result); err != nil {
		return "", fmt.Errorf("unmarshal snapshot: %w", err)
	}
	result["ontology_frozen_at"] = frozenAt

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal snapshot: %w", err)
	}
	return string(data), nil
}

func (r *ToolRegistry) handleListOntologyRelations(ctx context.Context, args map[string]any) (string, error) {
	if r.relationMgr == nil {
		return "", errors.New("database not connected")
	}

	entityID, _ := args["entity_id"].(string)
	entityID = strings.TrimSpace(entityID)

	relations, err := r.relationMgr.ListAllManualRelations(ctx, entityID)
	if err != nil {
		return "", fmt.Errorf("list manual relations: %w", err)
	}
	if relations == nil {
		relations = []ontology.ManualRelation{}
	}

	data, err := json.MarshalIndent(relations, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal relations: %w", err)
	}
	return string(data), nil
}

func (r *ToolRegistry) handleListOntologyCandidates(ctx context.Context, args map[string]any) (string, error) {
	if r.store == nil {
		return "", errors.New("database not connected")
	}

	suggestions, err := r.store.ListPendingSuggestions(ctx)
	if err != nil {
		return "", fmt.Errorf("list pending suggestions: %w", err)
	}
	if suggestions == nil {
		suggestions = []ontology.PendingSuggestion{}
	}

	data, err := json.MarshalIndent(suggestions, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal suggestions: %w", err)
	}
	return string(data), nil
}
