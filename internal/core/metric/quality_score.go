package metric

import (
	"time"

	pb "sonde/pkg/proto/plugin/v1"
)

// QualityResult holds the computed quality fields for an observation.
type QualityResult struct {
	Grade       string  // "realtime" | "delayed" | "estimated" | "preliminary" | "revised" | "unspecified"
	Confidence  float64 // 0..1, based on the declared grade
	SystemScore float64 // 0..1, weighted composite
}

// QualityGradeStrings maps the proto enum to a human-readable grade string.
var QualityGradeStrings = map[pb.QualityGrade]string{
	pb.QualityGrade_QUALITY_GRADE_REALTIME:    "realtime",
	pb.QualityGrade_QUALITY_GRADE_DELAYED:     "delayed",
	pb.QualityGrade_QUALITY_GRADE_ESTIMATED:   "estimated",
	pb.QualityGrade_QUALITY_GRADE_REVISED:     "revised",
	pb.QualityGrade_QUALITY_GRADE_PRELIMINARY: "preliminary",
}

// GradeString converts a QualityGrade enum to its string representation.
// Falls back to "unspecified" for unknown grades.
func GradeString(grade pb.QualityGrade) string {
	if s, ok := QualityGradeStrings[grade]; ok {
		return s
	}
	return "unspecified"
}

// GradeScore maps a QualityGrade enum to its base confidence score.
func GradeScore(grade pb.QualityGrade) float64 {
	switch grade {
	case pb.QualityGrade_QUALITY_GRADE_REALTIME:
		return 1.0
	case pb.QualityGrade_QUALITY_GRADE_DELAYED:
		return 0.7
	case pb.QualityGrade_QUALITY_GRADE_ESTIMATED:
		return 0.5
	case pb.QualityGrade_QUALITY_GRADE_REVISED:
		return 0.8
	case pb.QualityGrade_QUALITY_GRADE_PRELIMINARY:
		return 0.4
	default:
		return 0.6 // medium default for UNSPECIFIED or unknown
	}
}

// FreshnessScore computes a freshness score (0..1) based on how stale
// source_fetched_at is relative to the observation time. Fresher observations
// score higher. Returns 1.0 for data fetched within 1 minute, decays linearly
// to 0.1 at 1 day. Data older than 1 day still scores 0.1 residual.
func FreshnessScore(obsTime time.Time, fetchedAt time.Time) float64 {
	// Guard against zero times: if either is zero, treat as minimally fresh.
	if obsTime.IsZero() || fetchedAt.IsZero() {
		return 0.1
	}

	age := obsTime.Sub(fetchedAt)
	if age < 0 {
		age = -age // future-dated fetchedAt shouldn't penalize more than stale
	}

	switch {
	case age <= time.Minute:
		return 1.0
	case age >= 24*time.Hour:
		return 0.1
	default:
		// Linear interpolation from 1.0 at 1 minute to 0.1 at 1 day.
		total := 24*time.Hour - time.Minute
		progress := float64(age-time.Minute) / float64(total)
		return 1.0 - 0.9*progress
	}
}

// Score computes the full quality result for a snapshot.
//
//   - observedAt: the observation timestamp
//   - fetchedAt: when the plugin actually fetched the data
//   - grade: declared quality grade by the plugin
//   - isHealthy: whether the plugin is currently healthy
//
// The system score is a weighted composite: 60% grade confidence,
// 30% freshness, 10% source reputation.
func Score(observedAt, fetchedAt time.Time, grade pb.QualityGrade, isHealthy bool) QualityResult {
	gradeScore := GradeScore(grade)
	freshness := FreshnessScore(observedAt, fetchedAt)

	reputation := 0.5
	if isHealthy {
		reputation = 0.9
	}

	systemScore := 0.6*gradeScore + 0.3*freshness + 0.1*reputation
	if systemScore > 1.0 {
		systemScore = 1.0
	}

	return QualityResult{
		Grade:       GradeString(grade),
		Confidence:  gradeScore,
		SystemScore: systemScore,
	}
}
