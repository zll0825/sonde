package pluginrunner

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	pb "sonde/pkg/proto/plugin/v1"
)

type manifestFile struct {
	Name         string           `yaml:"name"`
	Version      string           `yaml:"version"`
	Description  string           `yaml:"description"`
	Capabilities capabilitiesFile `yaml:"capabilities"`
	Entities     []entityFile     `yaml:"entities"`
	Metrics      []metricFile     `yaml:"metrics"`
	Relations    []relationFile   `yaml:"relations"`
	Rules        []ruleFile       `yaml:"rules"`
	ChangeLog    string           `yaml:"changeLog"`
}

type capabilitiesFile struct {
	WindowedBackfill bool     `yaml:"windowedBackfill"`
	MaxBackfillDays  int32    `yaml:"maxBackfillDays"`
	RequiresSecrets  []string `yaml:"requiresSecrets"`
	MockAvailable    bool     `yaml:"mockAvailable"`
}

type entityFile struct {
	ID         string   `yaml:"id"`
	Name       string   `yaml:"name"`
	Namespace  string   `yaml:"namespace"`
	EntityType string   `yaml:"type"`
	Tags       []string `yaml:"tags"`
}

type metricFile struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Unit        string `yaml:"unit"`
	Frequency   string `yaml:"frequency"`
	Entity      string `yaml:"entity"`
}

type relationFile struct {
	Source      string `yaml:"source"`
	Target      string `yaml:"target"`
	Type        string `yaml:"type"`
	Direction   string `yaml:"direction"`
	Description string `yaml:"description"`
}

type ruleFile struct {
	Name        string `yaml:"name"`
	Metric      string `yaml:"metric"`
	Detector    string `yaml:"detector"`
	Severity    string `yaml:"severity"`
	Config      string `yaml:"config"`
	Description string `yaml:"description"`
	DisplayName string `yaml:"display_name"`
}

// LoadRegistration compiles a plugin manifest.yaml into RegisterPluginRequest.
func LoadRegistration(manifestYAML []byte) (*pb.RegisterPluginRequest, error) {
	var m manifestFile
	if err := yaml.Unmarshal(manifestYAML, &m); err != nil {
		return nil, fmt.Errorf("parse manifest.yaml: %w", err)
	}
	if m.Name == "" || m.Version == "" {
		return nil, fmt.Errorf("manifest.yaml needs name and version")
	}
	entities := make([]*pb.EntityDeclaration, 0, len(m.Entities))
	for i, e := range m.Entities {
		et, err := parseEntityType(e.EntityType)
		if err != nil {
			return nil, fmt.Errorf("entities[%d]: %w", i, err)
		}
		entities = append(entities, &pb.EntityDeclaration{
			Id: e.ID, Name: e.Name, Namespace: e.Namespace, EntityType: et, Tags: e.Tags,
		})
	}
	metrics := make([]*pb.MetricDeclaration, 0, len(m.Metrics))
	for _, metric := range m.Metrics {
		metrics = append(metrics, &pb.MetricDeclaration{
			Id: metric.ID, Name: metric.Name, Description: metric.Description,
			Unit: metric.Unit, Frequency: metric.Frequency, EntityId: metric.Entity,
		})
	}
	relations := make([]*pb.RelationSuggestion, 0, len(m.Relations))
	for i, r := range m.Relations {
		dir, err := parseDirection(r.Direction)
		if err != nil {
			return nil, fmt.Errorf("relations[%d]: %w", i, err)
		}
		relations = append(relations, &pb.RelationSuggestion{
			SourceId: r.Source, TargetId: r.Target, RelationType: r.Type,
			Direction: dir, Description: r.Description,
		})
	}
	rules := make([]*pb.RuleSuggestion, 0, len(m.Rules))
	for i, r := range m.Rules {
		sev, err := parseSeverity(r.Severity)
		if err != nil {
			return nil, fmt.Errorf("rules[%d]: %w", i, err)
		}
		rules = append(rules, &pb.RuleSuggestion{
			Name: r.Name, MetricId: r.Metric, DetectorName: r.Detector,
			Severity: sev, Config: []byte(r.Config), Description: r.Description,
			DisplayName: r.DisplayName,
		})
	}
	return &pb.RegisterPluginRequest{
		Info: &pb.PluginInfo{
			Name: m.Name, Version: m.Version, Description: m.Description,
		},
		Entities:  entities,
		Metrics:   metrics,
		Relations: relations,
		Rules:     rules,
		ChangeLog: m.ChangeLog,
		Capabilities: &pb.PluginCapabilities{
			WindowedBackfill: m.Capabilities.WindowedBackfill,
			MaxBackfillDays:  m.Capabilities.MaxBackfillDays,
			RequiresSecrets:  m.Capabilities.RequiresSecrets,
			MockAvailable:    m.Capabilities.MockAvailable,
		},
	}, nil
}

// ValidateCapabilities refuses a catalog that claims windowed backfill
// without a WindowedProvider collector.
func ValidateCapabilities(req *pb.RegisterPluginRequest, collector Provider) error {
	if req.GetCapabilities().GetWindowedBackfill() {
		if _, ok := collector.(WindowedProvider); !ok {
			return fmt.Errorf("catalog declares windowedBackfill but collector does not implement WindowedProvider")
		}
	}
	return nil
}

func parseEntityType(s string) (pb.EntityType, error) {
	switch strings.ToLower(s) {
	case "asset":
		return pb.EntityType_ENTITY_TYPE_ASSET, nil
	case "instrument":
		return pb.EntityType_ENTITY_TYPE_INSTRUMENT, nil
	case "flow":
		return pb.EntityType_ENTITY_TYPE_FLOW, nil
	case "institution":
		return pb.EntityType_ENTITY_TYPE_INSTITUTION, nil
	case "indicator":
		return pb.EntityType_ENTITY_TYPE_INDICATOR, nil
	case "index":
		return pb.EntityType_ENTITY_TYPE_INDEX, nil
	case "market":
		return pb.EntityType_ENTITY_TYPE_MARKET, nil
	default:
		return pb.EntityType_ENTITY_TYPE_UNSPECIFIED, fmt.Errorf("unknown entity type %q", s)
	}
}

func parseDirection(s string) (pb.Direction, error) {
	switch strings.ToLower(s) {
	case "", "forward":
		return pb.Direction_DIRECTION_FORWARD, nil
	case "backward":
		return pb.Direction_DIRECTION_BACKWARD, nil
	case "bidirectional", "undirected":
		return pb.Direction_DIRECTION_BIDIRECTIONAL, nil
	default:
		return pb.Direction_DIRECTION_UNSPECIFIED, fmt.Errorf("unknown direction %q", s)
	}
}

func parseSeverity(s string) (pb.Severity, error) {
	switch strings.ToLower(s) {
	case "critical":
		return pb.Severity_SEVERITY_CRITICAL, nil
	case "warning":
		return pb.Severity_SEVERITY_WARNING, nil
	case "info", "":
		return pb.Severity_SEVERITY_INFO, nil
	default:
		return pb.Severity_SEVERITY_UNSPECIFIED, fmt.Errorf("unknown severity %q", s)
	}
}
