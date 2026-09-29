package collector

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sonde/pkg/pluginrunner"
	"sonde/pkg/provider"
)

const (
	metricAFRE = "cn.mkt.social_financing"
	// Statistics root: lists "{YYYY}年统计数据" year pages. Year page paths are
	// not predictable (2026 is .../2026ntjsj/, 2025 is .../5570903/), so the
	// chain root → year → 社会融资规模 section → table .htm is discovered
	// from links every run (4 requests per year).
	afreRootPath      = "/diaochatongjisi/116219/116319/index.html"
	afreSectionTitle  = "社会融资规模"
	afreTableTitle    = "社会融资规模增量统计表"
	afreUnitCNY       = 1e8 // table unit: 亿元
	afreMaxWindowYear = 12
)

// AFRECollector reads the monthly AFRE flow (社会融资规模增量) from the
// PBoC statistics pages.
type AFRECollector struct {
	client  *provider.SafeHTTPClient
	baseURL string
	now     func() time.Time
}

// NewAFRECollector builds a collector on the shared PBoC client.
func NewAFRECollector(client *provider.SafeHTTPClient) *AFRECollector {
	return &AFRECollector{client: client, baseURL: defaultPBCBase, now: time.Now}
}

type monthValue struct {
	month time.Time // first day of month, UTC
	value float64   // 亿元 as published
}

// findYearPage returns the href of "{year}年统计数据" on the root page.
func findYearPage(root string, year int) (string, bool) {
	want := fmt.Sprintf("%d年统计数据", year)
	for _, a := range anchors(root) {
		if a.text == want {
			return a.href, true
		}
	}
	return "", false
}

// findAFRESection returns the 社会融资规模 section link on a year page. The
// anchor text is "社会融资规模" (nav) or "社会融资规模 Aggregate Financing…"
// (body); "地区社会融资规模…" must not match.
func findAFRESection(yearPage string) (string, bool) {
	for _, a := range anchors(yearPage) {
		if a.text == afreSectionTitle || strings.HasPrefix(a.text, afreSectionTitle+" ") {
			return a.href, true
		}
	}
	return "", false
}

var htmHrefRe = regexp.MustCompile(`(?i)href\s*=\s*['"]([^'"]+\.htm)['"]`)

// findAFRETable returns the .htm link in the row titled exactly
// 社会融资规模增量统计表. Each row starts with <div class="titp20">title<br>…;
// the page also lists 存量 and 地区…增量 tables, which must not match.
func findAFRETable(section string) (string, bool) {
	chunks := strings.Split(section, `class="titp20"`)
	for _, chunk := range chunks[1:] {
		titleEnd := len(chunk)
		if i := strings.Index(strings.ToLower(chunk), "<br"); i >= 0 {
			titleEnd = i
		}
		title := chunk[:titleEnd]
		if i := strings.Index(title, ">"); i >= 0 {
			title = title[i+1:]
		}
		if htmlText(title) != afreTableTitle {
			continue
		}
		if m := htmHrefRe.FindStringSubmatch(chunk); m != nil {
			return m[1], true
		}
		return "", false
	}
	return "", false
}

var (
	trRe       = regexp.MustCompile(`(?is)<tr\b[^>]*>(.*?)</tr>`)
	tdRe       = regexp.MustCompile(`(?is)<td\b[^>]*>(.*?)</td>`)
	monthRowRe = regexp.MustCompile(`^(\d{4})\.(\d{1,2})$`)
)

// parseAFRETable extracts (month, AFRE flow) for months of `year` from the
// Excel-exported table. Rules:
//   - first cell "YYYY.MM", second cell the AFRE flow (亿元);
//   - only rows of the page's own year (the 2019 table also repeats
//     2017–2018 rows);
//   - first occurrence of a month wins (older tables append a 结构/占比
//     section whose rows reuse "YYYY.MM" labels with 100.0 in column 2);
//   - blank cells (future months, U+3000 filler) are skipped.
func parseAFRETable(page string, year int) []monthValue {
	seen := map[int]bool{}
	var out []monthValue
	for _, tr := range trRe.FindAllStringSubmatch(page, -1) {
		cells := tdRe.FindAllStringSubmatch(tr[1], 3)
		if len(cells) < 2 {
			continue
		}
		m := monthRowRe.FindStringSubmatch(htmlText(cells[0][1]))
		if m == nil {
			continue
		}
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		if y != year || mo < 1 || mo > 12 || seen[mo] {
			continue
		}
		seen[mo] = true
		raw := strings.NewReplacer(",", "", " ", "").Replace(htmlText(cells[1][1]))
		if raw == "" {
			continue
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue
		}
		out = append(out, monthValue{month: dateUTC(y, time.Month(mo), 1), value: v})
	}
	return out
}

func (c *AFRECollector) fetchRoot(ctx context.Context) (string, error) {
	u, err := resolveURL(c.baseURL, afreRootPath)
	if err != nil {
		return "", err
	}
	return pbcGetHTML(ctx, c.client, u)
}

func (c *AFRECollector) fetchYear(ctx context.Context, root string, year int) ([]monthValue, error) {
	href, ok := findYearPage(root, year)
	if !ok {
		return nil, fmt.Errorf("pbc statistics root: no %d年统计数据 link", year)
	}
	yearURL, err := resolveURL(c.baseURL, href)
	if err != nil {
		return nil, err
	}
	yearPage, err := pbcGetHTML(ctx, c.client, yearURL)
	if err != nil {
		return nil, fmt.Errorf("year %d page: %w", year, err)
	}
	href, ok = findAFRESection(yearPage)
	if !ok {
		return nil, fmt.Errorf("year %d page: no %s section link", year, afreSectionTitle)
	}
	sectionURL, err := resolveURL(yearURL, href)
	if err != nil {
		return nil, err
	}
	section, err := pbcGetHTML(ctx, c.client, sectionURL)
	if err != nil {
		return nil, fmt.Errorf("year %d section: %w", year, err)
	}
	href, ok = findAFRETable(section)
	if !ok {
		return nil, fmt.Errorf("year %d section: no %s .htm link", year, afreTableTitle)
	}
	tableURL, err := resolveURL(sectionURL, href)
	if err != nil {
		return nil, err
	}
	table, err := pbcGetHTML(ctx, c.client, tableURL)
	if err != nil {
		return nil, fmt.Errorf("year %d table: %w", year, err)
	}
	return parseAFRETable(table, year), nil
}

func afreSnapshot(v monthValue, fetchedAt time.Time) pluginrunner.Snapshot {
	return snap(metricAFRE, v.value*afreUnitCNY, v.month, fetchedAt, providerPBC, "delayed")
}

// GetSnapshots emits the latest published month. Early in a year the
// current-year table has no values yet (January is published mid-February),
// so it falls back to the previous year's table.
func (c *AFRECollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	fetchedAt := time.Now()
	root, err := c.fetchRoot(ctx)
	if err != nil {
		return nil, fmt.Errorf("pbc statistics root: %w", err)
	}
	year := c.now().In(cst).Year()
	var failures []pluginrunner.CollectionFailure
	for _, y := range []int{year, year - 1} {
		vals, err := c.fetchYear(ctx, root, y)
		if err != nil {
			failures = append(failures, pluginrunner.CollectionFailure{Provider: providerPBC, MetricID: metricAFRE, Err: err})
			continue
		}
		if len(vals) == 0 {
			continue
		}
		latest := vals[0]
		for _, v := range vals[1:] {
			if v.month.After(latest.month) {
				latest = v
			}
		}
		return []pluginrunner.Snapshot{afreSnapshot(latest, fetchedAt)}, nil
	}
	return finish(nil, failures, true)
}

// GetSnapshotsForWindow reads each calendar year the window touches (at most
// afreMaxWindowYear, newest first). Each year's values come from that year's
// own table; later revisions published in subsequent years are not applied.
func (c *AFRECollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	if end.Before(start) {
		return nil, fmt.Errorf("afre window end precedes start")
	}
	fetchedAt := time.Now()
	root, err := c.fetchRoot(ctx)
	if err != nil {
		return nil, fmt.Errorf("pbc statistics root: %w", err)
	}
	startMonth := dateUTC(start.UTC().Year(), start.UTC().Month(), 1)
	var snaps []pluginrunner.Snapshot
	var failures []pluginrunner.CollectionFailure
	for y, n := end.UTC().Year(), 0; y >= start.UTC().Year() && n < afreMaxWindowYear; y, n = y-1, n+1 {
		vals, err := c.fetchYear(ctx, root, y)
		if err != nil {
			failures = append(failures, pluginrunner.CollectionFailure{Provider: providerPBC, MetricID: metricAFRE, Err: err})
			continue
		}
		for _, v := range vals {
			if !v.month.Before(startMonth) && !v.month.After(end.UTC()) {
				snaps = append(snaps, afreSnapshot(v, fetchedAt))
			}
		}
	}
	return finish(snaps, failures, false)
}
