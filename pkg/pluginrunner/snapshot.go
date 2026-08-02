package pluginrunner

import (
	"context"
	"time"

	pb "capital_observatory/pkg/proto/plugin/v1"
)

// Snapshot is one observed metric value. Plugin collectors return this shared
// type so that the lifecycle helpers can convert and submit without knowing the
// collector's own package.
type Snapshot struct {
	MetricID  string
	Value     float64
	Timestamp time.Time
	Provider  string
	Grade     string
}

// Provider fetches current snapshots for all metrics a plugin watches.
type Provider interface {
	GetSnapshots(ctx context.Context) ([]Snapshot, error)
}

// WindowedProvider extends Provider with historical backfill by time window.
// Plugins that implement this interface can serve BackfillCommands with a
// diverse series; non-windowable providers fall back to the latest snapshot.
type WindowedProvider interface {
	Provider
	GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]Snapshot, error)
}

// SnapshotsToProto converts plugin snapshots to proto MetricSnapshots.
func SnapshotsToProto(snapshots []Snapshot, version string) []*pb.MetricSnapshot {
	result := make([]*pb.MetricSnapshot, 0, len(snapshots))
	for _, s := range snapshots {
		result = append(result, &pb.MetricSnapshot{
			MetricId:            s.MetricID,
			Value:               s.Value,
			Timestamp:           s.Timestamp.Unix(),
			SourcePluginVersion: version,
			SourceProvider:      s.Provider,
			SourceFetchedAt:     s.Timestamp.Unix(),
			QualityGrade:        gradeToProto(s.Grade),
		})
	}
	return result
}

// gradeToProto converts a collector grade string to its proto enum value.
// Falls back to ESTIMATED for unknown/unset grades.
func gradeToProto(grade string) pb.QualityGrade {
	switch grade {
	case "realtime":
		return pb.QualityGrade_QUALITY_GRADE_REALTIME
	case "delayed":
		return pb.QualityGrade_QUALITY_GRADE_DELAYED
	case "estimated":
		return pb.QualityGrade_QUALITY_GRADE_ESTIMATED
	case "preliminary":
		return pb.QualityGrade_QUALITY_GRADE_PRELIMINARY
	case "revised":
		return pb.QualityGrade_QUALITY_GRADE_REVISED
	default:
		return pb.QualityGrade_QUALITY_GRADE_ESTIMATED
	}
}
