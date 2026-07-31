package metric

import (
	"testing"
	"time"

	pb "capital_observatory/pkg/proto/plugin/v1"
)

func TestGradeScore(t *testing.T) {
	cases := []struct {
		grade pb.QualityGrade
		want  float64
	}{
		{pb.QualityGrade_QUALITY_GRADE_REALTIME, 1.0},
		{pb.QualityGrade_QUALITY_GRADE_DELAYED, 0.7},
		{pb.QualityGrade_QUALITY_GRADE_ESTIMATED, 0.5},
		{pb.QualityGrade_QUALITY_GRADE_REVISED, 0.8},
		{pb.QualityGrade_QUALITY_GRADE_PRELIMINARY, 0.4},
		{pb.QualityGrade_QUALITY_GRADE_UNSPECIFIED, 0.6},
	}
	for _, c := range cases {
		if got := GradeScore(c.grade); got != c.want {
			t.Errorf("GradeScore(%v) = %v, want %v", c.grade, got, c.want)
		}
	}
}

func TestFreshnessScore(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name     string
		obs      time.Time
		fetched  time.Time
		min, max float64
	}{
		{"1sec_stale", now, now.Add(-1 * time.Second), 0.9, 1.0},
		{"5min_stale", now, now.Add(-5 * time.Minute), 0.8, 1.0},
		{"1day_stale", now, now.Add(-24 * time.Hour), 0.1, 0.2},
		{"zero_times", time.Time{}, time.Time{}, 0.1, 0.1},
		{"negative_age", now.Add(time.Hour), now, 0.8, 1.0},
	}
	for _, c := range cases {
		got := FreshnessScore(c.obs, c.fetched)
		if got < c.min || got > c.max {
			t.Errorf("FreshnessScore(%s) = %v, want [%v,%v]", c.name, got, c.min, c.max)
		}
	}
}

func TestScore_HealthyPlugin(t *testing.T) {
	now := time.Now()
	qr := Score(now, now.Add(-1*time.Minute), pb.QualityGrade_QUALITY_GRADE_REALTIME, true)
	if qr.Grade != "realtime" {
		t.Errorf("Grade = %q, want realtime", qr.Grade)
	}
	if qr.Confidence != 1.0 {
		t.Errorf("Confidence = %v, want 1.0", qr.Confidence)
	}
	if qr.SystemScore < 0.8 || qr.SystemScore > 1.0 {
		t.Errorf("SystemScore = %v, want [0.8, 1.0]", qr.SystemScore)
	}
}

func TestScore_UnhealthyPlugin(t *testing.T) {
	now := time.Now()
	qr := Score(now, now.Add(-1*time.Hour), pb.QualityGrade_QUALITY_GRADE_DELAYED, false)
	if qr.SystemScore > 0.8 {
		t.Errorf("SystemScore should be lower for unhealthy plugin, got %v", qr.SystemScore)
	}
}
