package collector

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSRFSumsWindowsAndZeroWhenNone(t *testing.T) {
	ops := []repoOp{
		{OperationDate: "2026-09-01", OperationType: "Repo", OperationMethod: "Full Allotment", TotalAmtAccepted: 7_000_000},
		{OperationDate: "2026-09-01", OperationType: "Repo", OperationMethod: "Full Allotment", TotalAmtAccepted: 4_000_000},
		{OperationDate: "2026-09-01", OperationType: "Reverse Repo", Term: "Overnight", TotalAmtAccepted: 500_000_000},
		{OperationDate: "2026-09-04", OperationType: "Reverse Repo", Term: "Overnight", TotalAmtAccepted: 675_000_000},
	}
	if got := srfSumForDate(ops, "2026-09-01"); got != 11_000_000 {
		t.Fatalf("SRF 2026-09-01 = %v, want 11000000", got)
	}
	if got := srfSumForDate(ops, "2026-09-04"); got != 0 {
		t.Fatalf("SRF 2026-09-04 = %v, want 0 (no windows)", got)
	}
	if v, ok := pickRRPForDate(ops, "2026-09-04"); !ok || v != 675_000_000 {
		t.Fatalf("RRP 2026-09-04 = %v %v", v, ok)
	}
}

func TestSOFRTailIsP99MinusRateInBP(t *testing.T) {
	rates := []sofrRate{{
		EffectiveDate: "2026-09-03", Type: "SOFR",
		PercentRate: 4.0, PercentPercentile99: 4.5,
	}}
	snaps := sofrSnapshots(rates, dateUTC(2026, 9, 3))
	got := map[string]float64{}
	for _, s := range snaps {
		got[s.MetricID] = s.Value
	}
	if got[metricSOFR] != 4.0 || got[metricSOFRP99] != 4.5 {
		t.Fatalf("sofr snapshots = %v", got)
	}
	if got[metricSOFRTail] != 50 {
		t.Fatalf("sofr_tail = %v, want 50 bp", got[metricSOFRTail])
	}
}

func TestNYFedUsesResultsSearchNotPropositions(t *testing.T) {
	var repoPath, sofrPath string
	client, srv := testClient(t, "nyfed-test", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/rp/"):
			repoPath = r.URL.Path
			if strings.Contains(r.URL.Path, "propositions") {
				t.Errorf("must not call propositions URL: %s", r.URL.Path)
			}
			_, _ = io.WriteString(w, `{
				"repo":{"operations":[
					{"operationDate":"2026-09-01","operationType":"Repo","operationMethod":"Full Allotment","totalAmtAccepted":7000000},
					{"operationDate":"2026-09-01","operationType":"Repo","operationMethod":"Full Allotment","totalAmtAccepted":4000000},
					{"operationDate":"2026-09-01","operationType":"Reverse Repo","term":"Overnight","totalAmtAccepted":500000000}
				]}
			}`)
		case strings.Contains(r.URL.Path, "/sofr/"):
			sofrPath = r.URL.Path
			_, _ = io.WriteString(w, `{"refRates":[{"effectiveDate":"2026-09-03","type":"SOFR","percentRate":4.0,"percentPercentile99":4.5}]}`)
		default:
			http.NotFound(w, r)
		}
	})
	c := NewNYFedCollectorWithClient(client)
	c.baseURL = srv.URL

	snaps, err := c.GetSnapshots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(repoPath, "/rp/results/search.json") {
		t.Fatalf("repo path = %q, want results/search.json", repoPath)
	}
	if !strings.Contains(sofrPath, "/rates/secured/sofr/last/1.json") {
		t.Fatalf("sofr path = %q, want last/1.json", sofrPath)
	}
	got := map[string]float64{}
	for _, s := range snaps {
		got[s.MetricID] = s.Value
	}
	if got[metricSRF] != 11_000_000 {
		t.Errorf("srf = %v, want 11000000", got[metricSRF])
	}
	if got[metricRRP] != 500_000_000 {
		t.Errorf("rrp = %v, want 500000000", got[metricRRP])
	}
	if got[metricSOFRTail] != 50 {
		t.Errorf("sofr_tail = %v, want 50", got[metricSOFRTail])
	}
}

func TestNYFedBackfillZeroSRFWindow(t *testing.T) {
	client, srv := testClient(t, "nyfed-test", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/rp/") {
			_, _ = io.WriteString(w, `{
				"repo":{"operations":[
					{"operationDate":"2026-09-04","operationType":"Reverse Repo","term":"Overnight","totalAmtAccepted":675000000}
				]}
			}`)
			return
		}
		_, _ = io.WriteString(w, `{"refRates":[{"effectiveDate":"2026-09-03","type":"SOFR","percentRate":4.2,"percentPercentile99":4.3}]}`)
	})
	c := NewNYFedCollectorWithClient(client)
	c.baseURL = srv.URL
	snaps, err := c.GetSnapshotsForWindow(context.Background(), dateUTC(2026, 9, 1), dateUTC(2026, 9, 4))
	if err != nil {
		t.Fatal(err)
	}
	var srf *float64
	for i := range snaps {
		if snaps[i].MetricID == metricSRF {
			v := snaps[i].Value
			srf = &v
		}
	}
	if srf == nil {
		t.Fatal("srf_usage missing; zero-window must write 0, not omit")
	}
	if *srf != 0 {
		t.Fatalf("srf_usage = %v, want 0", *srf)
	}
}
