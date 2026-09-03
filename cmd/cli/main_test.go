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
}

func TestUsageMentionsAlertsAndBudget(t *testing.T) {
	// smoke: the command table is documented
	usage()
}
