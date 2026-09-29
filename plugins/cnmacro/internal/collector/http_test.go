package collector

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sonde/pkg/pluginrunner"
	"sonde/pkg/provider"
)

func testHTTPConfig(name string) provider.Config {
	return provider.Config{
		ProviderName: name,
		Timeout:      2 * time.Second,
		RPS:          100,
		Burst:        100,
		MaxRetries:   0,
		BaseDelay:    time.Millisecond,
		MaxDelay:     time.Millisecond,
	}
}

func testClient(t *testing.T, name string, h http.HandlerFunc) (*provider.SafeHTTPClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	hc := withCookieJar(srv.Client())
	safe := provider.NewSafeHTTPClientWithHTTPClient(testHTTPConfig(name), hc)
	return safe, srv
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func byMetric(snaps []pluginrunner.Snapshot) map[string][]pluginrunner.Snapshot {
	out := map[string][]pluginrunner.Snapshot{}
	for _, s := range snaps {
		out[s.MetricID] = append(out[s.MetricID], s)
	}
	return out
}

func TestDecodeHTMLGBKByMetaCharset(t *testing.T) {
	raw := fixture(t, "pbc_afre_table_2026.gbk.htm")
	if strings.Contains(string(raw), "社会融资规模") {
		t.Fatal("fixture must be GBK-encoded, not UTF-8")
	}
	got, err := decodeHTML(fetched{body: raw, contentType: "text/html"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "社会融资规模增量统计表") || !strings.Contains(got, "单位：亿元人民币") {
		t.Fatalf("GBK table not decoded: %.200q", got)
	}
}

func TestDecodeHTMLHeaderCharsetWinsAndUTF8PassesThrough(t *testing.T) {
	utf := []byte(`<html><head><meta charset="utf-8"></head><body>社会融资规模</body></html>`)
	got, err := decodeHTML(fetched{body: utf, contentType: "text/html; charset=utf-8"})
	if err != nil || !strings.Contains(got, "社会融资规模") {
		t.Fatalf("utf-8 decode = %q, %v", got, err)
	}
	gbk := fixture(t, "pbc_afre_table_2026.gbk.htm")
	got, err = decodeHTML(fetched{body: gbk, contentType: "text/html; charset=GBK"})
	if err != nil || !strings.Contains(got, "社会融资规模增量统计表") {
		t.Fatalf("header GBK decode failed: %v", err)
	}
}

func TestHTMLTextStripsScriptStyleAndIdeographicSpace(t *testing.T) {
	in := "<script>var a='2099年1月1日';</script><style>.x{}</style><p>1.</p><p>40　%&nbsp;</p>"
	if got := htmlText(in); got != "1. 40 %" {
		t.Fatalf("htmlText = %q", got)
	}
}

func TestTodayCSTRollsAtUTC16(t *testing.T) {
	if got := todayCST(time.Date(2026, 9, 29, 15, 59, 0, 0, time.UTC)); !got.Equal(dateUTC(2026, 9, 29)) {
		t.Fatalf("15:59Z = %v", got)
	}
	if got := todayCST(time.Date(2026, 9, 29, 16, 0, 0, 0, time.UTC)); !got.Equal(dateUTC(2026, 9, 30)) {
		t.Fatalf("16:00Z = %v", got)
	}
}
