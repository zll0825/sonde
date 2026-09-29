package collector

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

// Shapes copied from auctions_query on 2026-09-29: a pending auction reports
// the string "null" for its results.
const auctionsFixture = `{"data":[
{"issue_date":"2026-09-30","security_type":"Note","security_term":"2-Year","total_accepted":"79388014800","soma_accepted":"10387942900"},
{"issue_date":"2026-09-30","security_type":"Note","security_term":"5-Year","total_accepted":"80538513000","soma_accepted":"10538492700"},
{"issue_date":"2026-10-01","security_type":"Bill","security_term":"13-Week","total_accepted":"104090004400","soma_accepted":"9088654300"},
{"issue_date":"2026-10-01","security_type":"Bill","security_term":"52-Week","total_accepted":"null","soma_accepted":"null"}
],"meta":{"total_pages":1}}`

func TestSettlementsNetOutSOMAAndSkipIncompleteDates(t *testing.T) {
	client, srv := testClient(t, "fiscaldata-test", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("fields"); got != auctionsFields {
			t.Errorf("fields = %q", got)
		}
		_, _ = io.WriteString(w, auctionsFixture)
	})
	c := NewAuctionsCollectorWithClient(client)
	c.baseURL = srv.URL
	// Even once 10-01 arrives, its 52-week result is still "null": skip the day.
	c.now = func() time.Time { return time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC) }

	snaps, err := c.GetSnapshots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := (79388014800.0 - 10387942900) + (80538513000.0 - 10538492700)
	if len(snaps) != 1 || snaps[0].MetricID != metricAuctionSettle || snaps[0].Value != want ||
		!snaps[0].Timestamp.Equal(dateUTC(2026, 9, 30)) {
		t.Fatalf("snapshots = %+v, want 2026-09-30 = %v", snaps, want)
	}
}

func TestSettlementsSkipFutureIssueDates(t *testing.T) {
	rows := []auctionRow{
		{IssueDate: "2026-09-30", TotalAccepted: "100", SOMAAccepted: "10"},
		{IssueDate: "2026-10-01", TotalAccepted: "200", SOMAAccepted: "0"},
	}
	got, err := settlementsByDate(rows, time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].value != 90 {
		t.Fatalf("settlements = %+v", got)
	}
}

func TestSettlementsRejectGarbage(t *testing.T) {
	rows := []auctionRow{{IssueDate: "2026-09-30", TotalAccepted: "abc"}}
	if _, err := settlementsByDate(rows, dateUTC(2026, 10, 1)); err == nil {
		t.Fatal("want parse error")
	}
}
