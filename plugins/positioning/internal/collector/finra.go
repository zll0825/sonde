package collector

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"sonde/pkg/pluginrunner"
	"sonde/pkg/provider"
)

const (
	defaultFINRAPageURL = "https://www.finra.org/rules-guidance/key-topics/margin-accounts/margin-statistics"
	// fallbackFINRAXLSXURL is the link found on the page on 2026-09-29. The
	// "2021-03" path is historical: FINRA overwrites the same file monthly
	// (Last-Modified 2026-09-14). The page is still scraped first in case the
	// link moves.
	fallbackFINRAXLSXURL = "https://www.finra.org/sites/default/files/2021-03/margin-statistics.xlsx"

	metricMarginDebt = "us.mkt.margin_debt"
	providerFINRA    = "finra"

	finraDebitHeader = "debit balances in customers' securities margin accounts"
	finraDateHeader  = "year-month"
	// The sheet is published in $ millions.
	finraUnitScale = 1e6
	// finraMaxXLSX bounds the workbook download (the real file is ~20 KB).
	finraMaxXLSX = 8 << 20
)

var finraXLSXLink = regexp.MustCompile(`href="([^"]*margin-statistics[^"]*\.xlsx)"`)

// FINRACollector reads FINRA margin statistics from the XLSX linked on the
// FINRA margin statistics page. FINRA offers no API or feed.
type FINRACollector struct {
	client  *provider.SafeHTTPClient
	pageURL string
}

// NewFINRACollector builds a production client.
func NewFINRACollector() *FINRACollector {
	return NewFINRACollectorWithClient(provider.NewSafeHTTPClient(provider.FINRAConfig()))
}

// NewFINRACollectorWithClient injects the HTTP client (tests use httptest).
func NewFINRACollectorWithClient(client *provider.SafeHTTPClient) *FINRACollector {
	return &FINRACollector{client: client, pageURL: defaultFINRAPageURL}
}

type monthValue struct {
	month time.Time
	value float64
}

// locateXLSX finds the margin-statistics workbook link on the page, falling
// back to the last known URL when the page fails or no link is found.
func (c *FINRACollector) locateXLSX(ctx context.Context) string {
	body, err := doGET(ctx, c.client, c.pageURL, "text/html", nil)
	if err != nil {
		log.Warn().Err(err).Msg("FINRA margin page fetch failed; using last known XLSX URL")
		return fallbackFINRAXLSXURL
	}
	m := finraXLSXLink.FindSubmatch(body)
	if m == nil {
		log.Warn().Msg("FINRA margin page has no margin-statistics .xlsx link; using last known XLSX URL")
		return fallbackFINRAXLSXURL
	}
	base, err := url.Parse(c.pageURL)
	if err != nil {
		return fallbackFINRAXLSXURL
	}
	ref, err := url.Parse(string(m[1]))
	if err != nil {
		return fallbackFINRAXLSXURL
	}
	return base.ResolveReference(ref).String()
}

func (c *FINRACollector) fetch(ctx context.Context) ([]monthValue, error) {
	xlsxURL := c.locateXLSX(ctx)
	body, err := doGET(ctx, c.client, xlsxURL, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", nil)
	if err != nil {
		return nil, fmt.Errorf("FINRA margin XLSX: %w", err)
	}
	rows, err := readFirstSheet(body)
	if err != nil {
		return nil, fmt.Errorf("FINRA margin XLSX: %w", err)
	}
	points, err := parseMarginRows(rows)
	if err != nil {
		return nil, fmt.Errorf("FINRA margin XLSX: %w", err)
	}
	return points, nil
}

// GetSnapshots returns the most recent month.
func (c *FINRACollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	points, err := c.fetch(ctx)
	if err != nil {
		return nil, err
	}
	last := points[len(points)-1]
	return []pluginrunner.Snapshot{snap(metricMarginDebt, last.value, last.month, time.Now(), providerFINRA)}, nil
}

// GetSnapshotsForWindow returns months whose first day lies in [start, end].
// The workbook holds the full history (1997-01 onward) in one download.
func (c *FINRACollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	if end.Before(start) {
		return nil, fmt.Errorf("FINRA history end precedes start")
	}
	points, err := c.fetch(ctx)
	if err != nil {
		return nil, err
	}
	fetchedAt := time.Now()
	out := make([]pluginrunner.Snapshot, 0, len(points))
	for _, p := range points {
		if p.month.Before(start) || p.month.After(end) {
			continue
		}
		out = append(out, snap(metricMarginDebt, p.value, p.month, fetchedAt, providerFINRA))
	}
	return out, nil
}

// parseMarginRows locates the date and debit-balance columns by header text,
// converts $ millions to USD, and returns points sorted ascending by month.
func parseMarginRows(rows [][]string) ([]monthValue, error) {
	if len(rows) == 0 {
		return nil, fmt.Errorf("empty sheet")
	}
	dateCol, debitCol := -1, -1
	for i, h := range rows[0] {
		h = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(h, "’", "'")))
		switch {
		case h == finraDateHeader || strings.Contains(h, "month"):
			if dateCol < 0 {
				dateCol = i
			}
		case h == finraDebitHeader:
			debitCol = i
		}
	}
	if dateCol < 0 || debitCol < 0 {
		return nil, fmt.Errorf("header row %q lacks %q / %q", rows[0], finraDateHeader, finraDebitHeader)
	}
	out := make([]monthValue, 0, len(rows)-1)
	for _, r := range rows[1:] {
		if dateCol >= len(r) || debitCol >= len(r) {
			continue
		}
		month, err := parseFINRAMonth(r[dateCol])
		if err != nil {
			continue
		}
		v, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(r[debitCol]), ",", ""), 64)
		if err != nil || v <= 0 {
			continue
		}
		out = append(out, monthValue{month: month, value: v * finraUnitScale})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no data rows")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].month.Before(out[j].month) })
	return out, nil
}

// parseFINRAMonth accepts "2026-08" (current format) or an Excel serial date.
func parseFINRAMonth(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.ParseInLocation("2006-01", s, time.UTC); err == nil {
		return t, nil
	}
	if serial, err := strconv.ParseFloat(s, 64); err == nil && serial > 20000 && serial < 80000 {
		t := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(serial))
		return dateUTC(t.Year(), t.Month(), 1), nil
	}
	return time.Time{}, fmt.Errorf("unrecognised month %q", s)
}

// --- minimal XLSX reader (stdlib only) ---

type xlsxSST struct {
	Items []xlsxRichText `xml:"si"`
}

type xlsxRichText struct {
	T    string `xml:"t"`
	Runs []struct {
		T string `xml:"t"`
	} `xml:"r"`
}

func (r xlsxRichText) text() string {
	if len(r.Runs) == 0 {
		return r.T
	}
	var b strings.Builder
	b.WriteString(r.T)
	for _, run := range r.Runs {
		b.WriteString(run.T)
	}
	return b.String()
}

type xlsxSheet struct {
	Rows []struct {
		Cells []struct {
			Ref    string       `xml:"r,attr"`
			Type   string       `xml:"t,attr"`
			V      string       `xml:"v"`
			Inline xlsxRichText `xml:"is"`
		} `xml:"c"`
	} `xml:"sheetData>row"`
}

// readFirstSheet returns the first worksheet as rows of cell strings, with
// cells placed by column letter so sparse rows keep their alignment.
func readFirstSheet(data []byte) ([][]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open xlsx: %w", err)
	}
	files := map[string]*zip.File{}
	var sheets []string
	for _, f := range zr.File {
		files[f.Name] = f
		if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") {
			sheets = append(sheets, f.Name)
		}
	}
	if len(sheets) == 0 {
		return nil, fmt.Errorf("xlsx has no worksheets")
	}
	sort.Strings(sheets)
	sheetName := "xl/worksheets/sheet1.xml"
	if files[sheetName] == nil {
		sheetName = sheets[0]
	}

	var shared []string
	if f := files["xl/sharedStrings.xml"]; f != nil {
		var sst xlsxSST
		if err := decodeZipXML(f, &sst); err != nil {
			return nil, fmt.Errorf("shared strings: %w", err)
		}
		for _, si := range sst.Items {
			shared = append(shared, si.text())
		}
	}

	var sheet xlsxSheet
	if err := decodeZipXML(files[sheetName], &sheet); err != nil {
		return nil, fmt.Errorf("%s: %w", sheetName, err)
	}
	out := make([][]string, 0, len(sheet.Rows))
	for _, row := range sheet.Rows {
		var cells []string
		for i, c := range row.Cells {
			col := columnIndex(c.Ref)
			if col < 0 {
				col = i
			}
			for len(cells) <= col {
				cells = append(cells, "")
			}
			switch c.Type {
			case "s":
				idx, err := strconv.Atoi(strings.TrimSpace(c.V))
				if err == nil && idx >= 0 && idx < len(shared) {
					cells[col] = shared[idx]
				}
			case "inlineStr":
				cells[col] = c.Inline.text()
			default:
				cells[col] = c.V
			}
		}
		out = append(out, cells)
	}
	return out, nil
}

func decodeZipXML(f *zip.File, v any) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	return xml.NewDecoder(io.LimitReader(rc, finraMaxXLSX*4)).Decode(v)
}

// columnIndex converts a cell ref like "B12" to a zero-based column index.
func columnIndex(ref string) int {
	n := 0
	seen := false
	for _, r := range ref {
		if r < 'A' || r > 'Z' {
			break
		}
		n = n*26 + int(r-'A'+1)
		seen = true
	}
	if !seen {
		return -1
	}
	return n - 1
}
