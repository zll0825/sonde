package collector

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"

	"sonde/pkg/model"
	"sonde/pkg/pluginrunner"
	"sonde/pkg/provider"
)

const (
	maxBody = 10 << 20
	// Several China-side hosts (pbc.gov.cn especially) reject or challenge
	// requests without a browser-like User-Agent.
	browserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"
)

// cst is China Standard Time (UTC+8, no DST). Calendar dates published by
// Chinese sources are CST dates; they are stored as UTC-midnight timestamps
// of that calendar date, matching fedops' date convention.
var cst = time.FixedZone("CST", 8*3600)

type fetched struct {
	body        []byte
	contentType string
}

func doGET(ctx context.Context, client *provider.SafeHTTPClient, rawURL, accept string) (fetched, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fetched{}, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	req.Header.Set("User-Agent", browserUserAgent)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	resp, err := client.Do(req)
	if err != nil {
		return fetched{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := provider.ReadAll(resp, 512)
		return fetched{}, fmt.Errorf("%s returned %d: %s", req.URL.Path, resp.StatusCode, body)
	}
	body, err := provider.ReadAll(resp, maxBody)
	if err != nil {
		return fetched{}, err
	}
	return fetched{body: body, contentType: resp.Header.Get("Content-Type")}, nil
}

var metaCharsetRe = regexp.MustCompile(`(?i)charset\s*=\s*["']?([a-z0-9_-]+)`)

// decodeHTML returns the page as UTF-8. PBoC statistics tables are exported
// from Excel as gb2312 while the portal pages are utf-8; the declared charset
// (HTTP header first, then <meta>) decides, with a UTF-8 validity fallback.
func decodeHTML(f fetched) (string, error) {
	charset := ""
	if m := metaCharsetRe.FindStringSubmatch(f.contentType); m != nil {
		charset = m[1]
	} else {
		head := f.body
		if len(head) > 4096 {
			head = head[:4096]
		}
		if m := metaCharsetRe.FindSubmatch(head); m != nil {
			charset = string(m[1])
		}
	}
	switch strings.ToLower(charset) {
	case "gb2312", "gbk", "gb18030", "x-gbk":
		return decodeGB18030(f.body)
	case "utf-8", "utf8":
		return string(f.body), nil
	}
	if utf8.Valid(f.body) {
		return string(f.body), nil
	}
	return decodeGB18030(f.body)
}

func decodeGB18030(b []byte) (string, error) {
	// GB18030 is a superset of GBK/GB2312, so it decodes all three.
	out, err := io.ReadAll(transform.NewReader(bytes.NewReader(b), simplifiedchinese.GB18030.NewDecoder()))
	if err != nil {
		return "", fmt.Errorf("decode GB18030: %w", err)
	}
	return string(out), nil
}

var (
	scriptStyleRe = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?</(script|style)>`)
	tagRe         = regexp.MustCompile(`(?s)<[^>]+>`)
	spaceRe       = regexp.MustCompile(`\s+`)
)

// htmlText strips script/style/tags, unescapes entities and collapses
// whitespace (including U+3000 ideographic space and NBSP) to single spaces.
func htmlText(s string) string {
	s = scriptStyleRe.ReplaceAllString(s, " ")
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	s = strings.ReplaceAll(s, "　", " ")
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}

func dateUTC(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// todayCST returns the CST calendar date of now as a UTC-midnight timestamp.
func todayCST(now time.Time) time.Time {
	c := now.In(cst)
	return dateUTC(c.Year(), c.Month(), c.Day())
}

// inWindow compares calendar dates: a source date d is in [start, end] when
// d is not before start's UTC date and not after end.
func inWindow(d, start, end time.Time) bool {
	s := start.UTC()
	s = dateUTC(s.Year(), s.Month(), s.Day())
	return !d.Before(s) && !d.After(end.UTC())
}

func snap(metricID string, value float64, ts, fetchedAt time.Time, providerName, grade string) pluginrunner.Snapshot {
	return pluginrunner.Snapshot{
		MetricID:    metricID,
		Value:       value,
		Timestamp:   ts,
		FetchedAt:   fetchedAt,
		Provider:    providerName,
		SourceClass: model.SourceClassReal,
		Grade:       grade,
	}
}

// finish applies the partial-failure contract: siblings survive a failed
// fetch, and an error is returned only alongside what did succeed. An empty,
// failure-free result is an error for latest collection (requireData) but
// legitimate for a backfill window the source does not reach.
func finish(snaps []pluginrunner.Snapshot, failures []pluginrunner.CollectionFailure, requireData bool) ([]pluginrunner.Snapshot, error) {
	if len(snaps) == 0 {
		if len(failures) == 0 {
			if requireData {
				return nil, fmt.Errorf("no observations returned")
			}
			return nil, nil
		}
		return nil, pluginrunner.SummarizeCollectionFailures(failures)
	}
	if len(failures) > 0 {
		return snaps, pluginrunner.SummarizeCollectionFailures(failures)
	}
	return snaps, nil
}
