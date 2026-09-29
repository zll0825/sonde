package collector

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"sonde/pkg/model"
)

// finraSheetXML mirrors the real sheet1.xml (2026-09-29): inline strings,
// "Year-Month" first column, newest month first, values in $ millions.
const finraSheetXML = `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><dimension ref="A1:D4"/><sheetData>` +
	`<row outlineLevel="0" r="1"><c r="A1" s="2" t="inlineStr"><is><t>Year-Month</t></is></c><c r="B1" s="2" t="inlineStr"><is><t>Debit Balances in Customers' Securities Margin Accounts</t></is></c><c r="C1" s="2" t="inlineStr"><is><t>Free Credit Balances in Customers' Cash Accounts</t></is></c><c r="D1" s="2" t="inlineStr"><is><t>Free Credit Balances in Customers' Securities Margin Accounts</t></is></c></row>` +
	`<row outlineLevel="0" r="2"><c r="A2" s="4" t="inlineStr"><is><t>2026-08</t></is></c><c r="B2" s="5"><v>1453832</v></c><c r="C2" s="5"><v>207641</v></c><c r="D2" s="5"><v>217499</v></c></row>` +
	`<row outlineLevel="0" r="3"><c r="A3" s="4" t="inlineStr"><is><t>2026-07</t></is></c><c r="B3" s="5"><v>1417225</v></c><c r="C3" s="5"><v>205132</v></c><c r="D3" s="5"><v>217305</v></c></row>` +
	`<row outlineLevel="0" r="4"><c r="A4" s="4" t="inlineStr"><is><t>1997-01</t></is></c><c r="B4" s="5"><v>103337</v></c><c r="C4" s="5"><v>68856</v></c></row>` +
	`</sheetData></worksheet>`

// Same data but with shared strings, as Excel itself would save it.
const finraSharedSheetXML = `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
	`<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>` +
	`<row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2"><v>1453832</v></c></row>` +
	`</sheetData></worksheet>`

const finraSharedStrings = `<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><si><t>Year-Month</t></si><si><r><t>Debit Balances in Customers' </t></r><r><t>Securities Margin Accounts</t></r></si><si><t>2026-08</t></si></sst>`

func buildXLSX(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newTestFINRA(t *testing.T, xlsx []byte, pageHTML string) (*FINRACollector, *[]string) {
	var paths []string
	client, srv := testClient(t, "finra-test", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch {
		case r.URL.Path == "/margin-statistics":
			_, _ = w.Write([]byte(pageHTML))
		case strings.HasSuffix(r.URL.Path, ".xlsx"):
			w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
			_, _ = w.Write(xlsx)
		default:
			http.NotFound(w, r)
		}
	})
	c := NewFINRACollectorWithClient(client)
	c.pageURL = srv.URL + "/margin-statistics"
	return c, &paths
}

const finraPageHTML = `<p>FINRA Statistics 1 (shown in $ millions)</p><a href="/sites/default/files/2026-09/margin-statistics.xlsx">Download</a>`

func TestFINRALocatesLinkParsesMillionsAndPicksLatest(t *testing.T) {
	xlsx := buildXLSX(t, map[string]string{"xl/worksheets/sheet1.xml": finraSheetXML})
	c, paths := newTestFINRA(t, xlsx, finraPageHTML)

	snaps, err := c.GetSnapshots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := (*paths)[1]; got != "/sites/default/files/2026-09/margin-statistics.xlsx" {
		t.Fatalf("xlsx path = %q (link from page must be followed)", got)
	}
	if len(snaps) != 1 {
		t.Fatalf("snaps = %+v", snaps)
	}
	s := snaps[0]
	if s.MetricID != metricMarginDebt || s.Value != 1_453_832e6 {
		t.Fatalf("latest = %+v, want 1,453,832 million USD", s)
	}
	if !s.Timestamp.Equal(dateUTC(2026, 8, 1)) {
		t.Fatalf("timestamp = %s, want 2026-08-01", s.Timestamp)
	}
	if s.SourceClass != model.SourceClassReal || s.Provider != providerFINRA {
		t.Fatalf("class/provider = %s/%s", s.SourceClass, s.Provider)
	}

	win, err := c.GetSnapshotsForWindow(context.Background(), dateUTC(1990, 1, 1), dateUTC(2026, 7, 31))
	if err != nil {
		t.Fatal(err)
	}
	if len(win) != 2 || !win[0].Timestamp.Equal(dateUTC(1997, 1, 1)) || win[1].Value != 1_417_225e6 {
		t.Fatalf("window = %+v", win)
	}
}

func TestFINRAReadsSharedStringsWorkbook(t *testing.T) {
	xlsx := buildXLSX(t, map[string]string{
		"xl/worksheets/sheet1.xml": finraSharedSheetXML,
		"xl/sharedStrings.xml":     finraSharedStrings,
	})
	rows, err := readFirstSheet(xlsx)
	if err != nil {
		t.Fatal(err)
	}
	points, err := parseMarginRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].value != 1_453_832e6 || !points[0].month.Equal(dateUTC(2026, 8, 1)) {
		t.Fatalf("points = %+v", points)
	}
}

func TestFINRAFallsBackWhenPageHasNoLink(t *testing.T) {
	c, _ := newTestFINRA(t, nil, "<html>no link</html>")
	if got := c.locateXLSX(context.Background()); got != fallbackFINRAXLSXURL {
		t.Fatalf("locateXLSX = %q", got)
	}
}

func TestFINRARejectsSheetWithoutDebitColumn(t *testing.T) {
	_, err := parseMarginRows([][]string{{"Year-Month", "Something Else"}, {"2026-08", "1"}})
	if err == nil {
		t.Fatal("missing debit column must fail, not guess a column")
	}
}

func TestParseFINRAMonthAcceptsExcelSerial(t *testing.T) {
	// 46235 = 2026-08-01 in the 1900 date system.
	got, err := parseFINRAMonth("46235")
	if err != nil || !got.Equal(dateUTC(2026, 8, 1)) {
		t.Fatalf("got %s, %v", got, err)
	}
	if _, err := parseFINRAMonth("Total"); err == nil {
		t.Fatal("non-date must fail")
	}
	_ = time.UTC
}
