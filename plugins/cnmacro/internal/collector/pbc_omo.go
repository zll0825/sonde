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
	metricOMO7D   = "cn.mkt.omo_7d_rate"
	omoListPath   = "/zhengcehuobisi/125207/125213/125431/125475/index.html"
	omoTitleMatch = "公开市场业务交易公告"

	// Politeness caps (pbc.gov.cn is a government portal). Latest collection
	// reads the first list page and at most omoLatestDetails announcements;
	// a backfill window reads at most omoWindowListPages list pages (~20
	// announcements each, ≈1 month) and omoWindowDetails announcements.
	omoLatestDetails   = 10
	omoWindowDetails   = 20
	omoWindowListPages = 6
)

// OMOCollector parses PBoC open-market-operation announcements for the
// 7-day reverse repo rate.
type OMOCollector struct {
	client  *provider.SafeHTTPClient
	baseURL string
}

// NewOMOCollector builds a collector on the shared PBoC client.
func NewOMOCollector(client *provider.SafeHTTPClient) *OMOCollector {
	return &OMOCollector{client: client, baseURL: defaultPBCBase}
}

type omoItem struct {
	href     string
	title    string
	listDate time.Time
}

var (
	omoItemRe = regexp.MustCompile(`(?is)<a\s[^>]*?href\s*=\s*"([^"]+)"[^>]*?title\s*=\s*"([^"]*` + omoTitleMatch + `[^"]*)"[^>]*>.*?</a>.*?<span class="hui12">\s*(\d{4}-\d{2}-\d{2})`)
	omoNextRe = regexp.MustCompile(`(?is)queryArticleByCondition\(this,\s*'([^']+)'\)[^>]*>\s*下一页`)
)

// parseOMOList returns announcement links (newest first, as listed) and the
// next-page href, if any. The column's own nav link has the same title text
// but no hui12 date, so it is not matched as an item.
func parseOMOList(page string) (items []omoItem, next string) {
	for _, m := range omoItemRe.FindAllStringSubmatch(page, -1) {
		d, err := time.ParseInLocation("2006-01-02", m[3], time.UTC)
		if err != nil {
			continue
		}
		items = append(items, omoItem{href: m[1], title: strings.TrimSpace(m[2]), listDate: d})
	}
	if m := omoNextRe.FindStringSubmatch(page); m != nil {
		next = m[1]
	}
	return items, next
}

var (
	omoDateRe = regexp.MustCompile(`(\d{4})年(\d{1,2})月(\d{1,2})日`)
	// Applied to whitespace-free text. Handles both table layouts:
	//   2026: 期限 操作 利率 投标量 中标量 / 7 天 1. 40 % 905 亿元 905 亿元
	//   ≤2024: 期限 中标量 中标利率 / 7 天 1000 亿元 1.80%
	// "[^0-9]7天" keeps 17天/27天 out; "7天期逆回购" fails because 期 is
	// neither an amount nor a rate.
	omoRate7DRe = regexp.MustCompile(`(?:^|[^0-9])7天(?:[0-9.]+亿元)*([0-9]+(?:\.[0-9]+)?)%`)
)

// parseOMODetail returns the operation date and 7-day reverse repo rate.
// ok=false (no error) means the announcement carries no 7-day rate (zero
// volume, "不开展", or another tenor only).
func parseOMODetail(page string) (date time.Time, rate float64, ok bool, err error) {
	text := htmlText(page)
	if i := strings.Index(text, "打印本页"); i >= 0 {
		text = text[i:]
	}
	if i := strings.Index(text, "公开市场业务操作室"); i >= 0 {
		text = text[:i]
	}
	// The same column also carries central-bank-bill (央行票据), MLF and
	// other announcements; anything without 逆回购 is not ours.
	if !strings.Contains(text, "逆回购") {
		return time.Time{}, 0, false, nil
	}
	m := omoDateRe.FindStringSubmatch(text)
	if m == nil {
		return time.Time{}, 0, false, fmt.Errorf("omo announcement: no operation date")
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	date = dateUTC(y, time.Month(mo), d)
	if strings.Contains(text, "不开展") {
		return date, 0, false, nil
	}
	compact := strings.Join(strings.Fields(text), "")
	rm := omoRate7DRe.FindStringSubmatch(compact)
	if rm == nil {
		return date, 0, false, nil
	}
	rate, err = strconv.ParseFloat(rm[1], 64)
	if err != nil || rate <= 0 || rate >= 20 {
		return date, 0, false, fmt.Errorf("omo announcement %s: implausible 7-day rate %q", date.Format("2006-01-02"), rm[1])
	}
	return date, rate, true, nil
}

func (c *OMOCollector) listPage(ctx context.Context, href string) ([]omoItem, string, error) {
	u, err := resolveURL(c.baseURL, href)
	if err != nil {
		return nil, "", err
	}
	page, err := pbcGetHTML(ctx, c.client, u)
	if err != nil {
		return nil, "", fmt.Errorf("omo list: %w", err)
	}
	items, next := parseOMOList(page)
	if len(items) == 0 {
		return nil, "", fmt.Errorf("omo list %s: no announcements parsed", href)
	}
	return items, next, nil
}

func (c *OMOCollector) details(ctx context.Context, items []omoItem, fetchedAt time.Time) ([]pluginrunner.Snapshot, []pluginrunner.CollectionFailure) {
	var snaps []pluginrunner.Snapshot
	var failures []pluginrunner.CollectionFailure
	seen := map[time.Time]bool{}
	for _, it := range items {
		u, err := resolveURL(c.baseURL, it.href)
		if err != nil {
			failures = append(failures, pluginrunner.CollectionFailure{Provider: providerPBC, MetricID: metricOMO7D, Err: err})
			continue
		}
		page, err := pbcGetHTML(ctx, c.client, u)
		if err != nil {
			failures = append(failures, pluginrunner.CollectionFailure{Provider: providerPBC, MetricID: metricOMO7D, Err: fmt.Errorf("%s: %w", it.title, err)})
			continue
		}
		date, rate, ok, err := parseOMODetail(page)
		if err != nil {
			failures = append(failures, pluginrunner.CollectionFailure{Provider: providerPBC, MetricID: metricOMO7D, Err: fmt.Errorf("%s: %w", it.title, err)})
			continue
		}
		if !ok || seen[date] {
			continue
		}
		seen[date] = true
		snaps = append(snaps, snap(metricOMO7D, rate, date, fetchedAt, providerPBC, "delayed"))
	}
	return snaps, failures
}

func (c *OMOCollector) GetSnapshots(ctx context.Context) ([]pluginrunner.Snapshot, error) {
	fetchedAt := time.Now()
	items, _, err := c.listPage(ctx, omoListPath)
	if err != nil {
		return nil, err
	}
	if len(items) > omoLatestDetails {
		items = items[:omoLatestDetails]
	}
	snaps, failures := c.details(ctx, items, fetchedAt)
	return finish(snaps, failures, true)
}

// GetSnapshotsForWindow walks list pages newest-first to the window, then
// reads at most omoWindowDetails announcements dated inside it (newest
// first). Deep history is therefore NOT reachable in one call.
func (c *OMOCollector) GetSnapshotsForWindow(ctx context.Context, start, end time.Time) ([]pluginrunner.Snapshot, error) {
	if end.Before(start) {
		return nil, fmt.Errorf("omo window end precedes start")
	}
	fetchedAt := time.Now()
	var picked []omoItem
	var failures []pluginrunner.CollectionFailure
	href := omoListPath
	for page := 0; page < omoWindowListPages && href != "" && len(picked) < omoWindowDetails; page++ {
		items, next, err := c.listPage(ctx, href)
		if err != nil {
			failures = append(failures, pluginrunner.CollectionFailure{Provider: providerPBC, MetricID: metricOMO7D, Err: err})
			break
		}
		reachedStart := false
		for _, it := range items {
			if !inWindow(it.listDate, start, end) {
				if it.listDate.Before(start.UTC()) {
					reachedStart = true
				}
				continue
			}
			if len(picked) < omoWindowDetails {
				picked = append(picked, it)
			}
		}
		if reachedStart {
			break
		}
		href = next
	}
	snaps, detailFailures := c.details(ctx, picked, fetchedAt)
	failures = append(failures, detailFailures...)
	var out []pluginrunner.Snapshot
	for _, s := range snaps {
		if inWindow(s.Timestamp, start, end) {
			out = append(out, s)
		}
	}
	return finish(out, failures, false)
}
