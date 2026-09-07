package handler

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestFrontendFallsBackToRuleName locks B8: old plugins that omit
// display_name must keep rendering the slug, not a blank cell.
func TestFrontendFallsBackToRuleName(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	src, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "../../../web/src/common.js"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	if !strings.Contains(text, "rule.display_name || rule.name") {
		t.Fatal("web/src/common.js ruleDisplayName must fall back to name when display_name is empty")
	}
}

// TestAlertsRerenderAfterStatusLocksNameLookup locks the first-paint race:
// fetchStatus must re-render the alert list once metric names are available,
// not only the empty-state branch.
func TestAlertsRerenderAfterStatusLocksNameLookup(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	webSrc := filepath.Join(filepath.Dir(thisFile), "../../../web/src")
	status, err := os.ReadFile(filepath.Join(webSrc, "status.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(status), "if (Array.isArray(alertsList)) renderAlerts(alertsList)") {
		t.Fatal("fetchStatus must re-render alerts after statusData lands, including a non-empty list")
	}
	if strings.Contains(string(status), "alertsList.length === 0") {
		t.Fatal("empty-only re-render leaves first-paint alert rows on raw metric_id")
	}
}

func TestAlertsDefaultListDoesNotRequestObserve(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	webSrc := filepath.Join(filepath.Dir(thisFile), "../../../web/src")
	alerts, err := os.ReadFile(filepath.Join(webSrc, "alerts.js"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(alerts)
	if !strings.Contains(text, "queryParams.set('mode', 'observe')") {
		t.Fatal("observe filter must set mode=observe")
	}
	if !strings.Contains(text, "currentAlertFilter === 'observe'") {
		t.Fatal("default list must only add mode=observe when the observe filter is active")
	}
	statusGo, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "status.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(statusGo), "AND mode = 'live'") {
		t.Fatal("/api/status budget SQL must exclude observe alerts")
	}
}
