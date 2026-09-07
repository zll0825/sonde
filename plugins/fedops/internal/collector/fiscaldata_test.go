package collector

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestParseBalanceNullAndEmpty(t *testing.T) {
	for _, raw := range []string{"null", "", "  "} {
		if _, err := parseBalance(raw); err == nil {
			t.Errorf("parseBalance(%q) succeeded, want error", raw)
		}
	}
	v, err := parseBalance("1234.5")
	if err != nil || v != 1234.5 {
		t.Fatalf("parseBalance(1234.5) = %v, %v", v, err)
	}
}

func TestPickTGAThreeGenerations(t *testing.T) {
	cases := []struct {
		name string
		date time.Time
		rows []tgaRow
		want float64
	}{
		{
			name: "2021-09-30 Federal Reserve Account close_today_bal",
			date: dateUTC(2021, 9, 30),
			rows: []tgaRow{{
				RecordDate: "2021-09-30", AccountType: "Federal Reserve Account",
				CloseTodayBal: "1000.5", OpenTodayBal: "999",
			}},
			want: 1000.5 * 1e6,
		},
		{
			name: "2021-10-01 TGA close_today_bal",
			date: dateUTC(2021, 10, 1),
			rows: []tgaRow{{
				RecordDate: "2021-10-01", AccountType: "Treasury General Account (TGA)",
				CloseTodayBal: "2000", OpenTodayBal: "1900",
			}},
			want: 2000 * 1e6,
		},
		{
			name: "2022-04-15 still gen2 close_today_bal",
			date: dateUTC(2022, 4, 15),
			rows: []tgaRow{{
				RecordDate: "2022-04-15", AccountType: "Treasury General Account (TGA)",
				CloseTodayBal: "3000", OpenTodayBal: "2900",
			}},
			want: 3000 * 1e6,
		},
		{
			name: "2022-04-18 Closing Balance open_today_bal",
			date: dateUTC(2022, 4, 18),
			rows: []tgaRow{
				{
					RecordDate: "2022-04-18", AccountType: "Treasury General Account (TGA) Opening Balance",
					CloseTodayBal: "null", OpenTodayBal: "3999",
				},
				{
					RecordDate: "2022-04-18", AccountType: "Treasury General Account (TGA) Closing Balance",
					CloseTodayBal: "null", OpenTodayBal: "4000",
				},
			},
			want: 4000 * 1e6,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pickTGA(tc.date, tc.rows)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestPickTGANoMatchDoesNotFallBackToFirstRow(t *testing.T) {
	rows := []tgaRow{{
		RecordDate: "2022-04-18", AccountType: "Treasury General Account (TGA) Opening Balance",
		CloseTodayBal: "null", OpenTodayBal: "3999",
	}}
	_, err := pickTGA(dateUTC(2022, 4, 18), rows)
	if err == nil {
		t.Fatal("expected error when Closing Balance row is missing")
	}
	if !strings.Contains(err.Error(), "Closing Balance") {
		t.Fatalf("error = %v, want account_type in message", err)
	}
}

func TestPickTGANullOpenTodayIsInvalid(t *testing.T) {
	rows := []tgaRow{{
		RecordDate: "2022-04-18", AccountType: "Treasury General Account (TGA) Closing Balance",
		CloseTodayBal: "null", OpenTodayBal: "null",
	}}
	if _, err := pickTGA(dateUTC(2022, 4, 18), rows); err == nil {
		t.Fatal("null open_today_bal must be invalid")
	}
}

func TestTGARequestHasNoFieldsParam(t *testing.T) {
	var sawQuery string
	client, srv := testClient(t, "fiscaldata-test", func(w http.ResponseWriter, r *http.Request) {
		sawQuery = r.URL.RawQuery
		if r.URL.Query().Get("fields") != "" || strings.Contains(r.URL.RawQuery, "fields=") {
			t.Errorf("TGA request must not contain fields=: %s", r.URL.RawQuery)
		}
		body := `{"data":[{"record_date":"2022-04-18","account_type":"Treasury General Account (TGA) Closing Balance","close_today_bal":"null","open_today_bal":"5000"}]}`
		_, _ = io.WriteString(w, body)
	})
	c := NewFiscalDataCollectorWithClient(client)
	c.baseURL = srv.URL

	snaps, err := c.GetSnapshots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 || snaps[0].Value != 5000*1e6 {
		t.Fatalf("snapshots = %+v", snaps)
	}
	if sawQuery == "" {
		t.Fatal("collector made no request")
	}
}

func TestTGAWindowIncludesTransitionDates(t *testing.T) {
	payload := map[string]any{
		"data": []tgaRow{
			{RecordDate: "2021-09-30", AccountType: "Federal Reserve Account", CloseTodayBal: "100", OpenTodayBal: "90"},
			{RecordDate: "2021-10-01", AccountType: "Treasury General Account (TGA)", CloseTodayBal: "200", OpenTodayBal: "190"},
			{RecordDate: "2022-04-15", AccountType: "Treasury General Account (TGA)", CloseTodayBal: "300", OpenTodayBal: "290"},
			{RecordDate: "2022-04-18", AccountType: "Treasury General Account (TGA) Closing Balance", CloseTodayBal: "null", OpenTodayBal: "400"},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	client, srv := testClient(t, "fiscaldata-test", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "fields=") {
			t.Errorf("fields= present: %s", r.URL.RawQuery)
		}
		_, _ = w.Write(body)
	})
	c := NewFiscalDataCollectorWithClient(client)
	c.baseURL = srv.URL

	start := dateUTC(2021, 9, 30)
	end := dateUTC(2022, 4, 18)
	snaps, err := c.GetSnapshotsForWindow(context.Background(), start, end)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, s := range snaps {
		got[s.Timestamp.Format("2006-01-02")] = s.Value
	}
	want := map[string]float64{
		"2021-09-30": 100 * 1e6,
		"2021-10-01": 200 * 1e6,
		"2022-04-15": 300 * 1e6,
		"2022-04-18": 400 * 1e6,
	}
	if len(got) != len(want) {
		t.Fatalf("dates = %v, want %v", got, want)
	}
	for d, v := range want {
		if got[d] != v {
			t.Errorf("%s = %v, want %v", d, got[d], v)
		}
	}
}
