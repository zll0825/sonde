package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBudgetSQLFileExists(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// test cwd is cmd/cli; SQL lives at repo reports/
	p := filepath.Join(root, "..", "..", "reports", "daily_alert_budget.sql")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "real_count") {
		t.Fatalf("unexpected budget SQL: %s", p)
	}
	if !strings.Contains(string(b), "a.mode = 'live'") {
		t.Fatal("budget SQL must exclude observe alerts")
	}
}

func TestReadBudgetSQLFromPackageDir(t *testing.T) {
	b, err := readBudgetSQL()
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, "real_count") || !strings.Contains(got, "within_budget") {
		t.Fatalf("budget SQL missing expected columns")
	}
}

func TestReadBudgetSQLFromRepoRoot(t *testing.T) {
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(old, "..", "..")
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	b, err := readBudgetSQL()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "real_count") {
		t.Fatal("budget SQL from repo root missing real_count")
	}
}

func TestUsageMentionsAlertsAndBudget(t *testing.T) {
	if !strings.Contains(usageText, "cli alerts") {
		t.Error("usage missing alerts command")
	}
	if !strings.Contains(usageText, "cli budget") {
		t.Error("usage missing budget command")
	}
}

func TestAlertsSQLIsActiveOnly(t *testing.T) {
	if !strings.Contains(alertsActiveSQL, "status = 'active'") {
		t.Error("alerts SQL must filter status = 'active'")
	}
	if !strings.Contains(alertsActiveSQL, "mode = 'live'") {
		t.Error("alerts SQL must default to live mode like GET /api/alerts")
	}
	for _, col := range []string{"source_provider", "source_class", "dedup_count", "last_deduplicated_at"} {
		if !strings.Contains(alertsActiveSQL, col) {
			t.Errorf("alerts SQL missing column %s", col)
		}
	}
}
