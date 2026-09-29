package collector

import (
	"context"
	"net/http"
	"testing"
	"time"
)

const omoDir = "/zhengcehuobisi/125207/125213/125431/125475/"

func TestParseOMODetailFixtures(t *testing.T) {
	cases := []struct {
		file   string
		date   time.Time
		rate   float64
		wantOK bool
	}{
		// 2026 layout: 期限 操作利率 投标量 中标量 / "1." "40" "%" in separate spans.
		{"pbc_omo_detail_2026_fixed_rate.html", dateUTC(2026, 9, 29), 1.40, true},
		// ≤2024 layout: 期限 中标量 中标利率 / 7 天 1000 亿元 1.80%.
		{"pbc_omo_detail_2024_rate_tender.html", dateUTC(2024, 6, 27), 1.80, true},
		// "7天期逆回购操作量为零": no rate → skipped, not an error.
		{"pbc_omo_detail_zero_volume.html", dateUTC(2026, 9, 2), 0, false},
	}
	for _, tc := range cases {
		date, rate, ok, err := parseOMODetail(string(fixture(t, tc.file)))
		if err != nil {
			t.Fatalf("%s: %v", tc.file, err)
		}
		if ok != tc.wantOK || !date.Equal(tc.date) || (ok && rate != tc.rate) {
			t.Errorf("%s: date=%v rate=%v ok=%v, want %v %v %v", tc.file, date, rate, ok, tc.date, tc.rate, tc.wantOK)
		}
	}
}

func TestParseOMODetailSynthetic(t *testing.T) {
	cases := []struct {
		name string
		body string
		rate float64
		ok   bool
	}{
		{"not conducted", `<a>打印本页</a><p>2026年2月10日人民银行不开展逆回购操作。</p><p>中国人民银行公开市场业务操作室</p>`, 0, false},
		{"multi tenor picks 7d", `<a>打印本页</a><p>2019年1月4日人民银行以利率招标方式开展了逆回购操作。</p><table><tr><td>7天</td><td>300亿元</td><td>2.55%</td></tr><tr><td>14天</td><td>200亿元</td><td>2.70%</td></tr></table>中国人民银行公开市场业务操作室`, 2.55, true},
		{"14d only", `<a>打印本页</a><p>2019年1月5日人民银行开展了逆回购操作。</p><table><tr><td>14天</td><td>200亿元</td><td>2.70%</td></tr></table>`, 0, false},
		{"central bank bill", `<a>打印本页</a><p>中国人民银行于本周三（9月23日）通过香港金融管理局债务工具中央结算系统（CMU）债券投标平台，以利率招标方式发行了2026年第九期央行票据。</p><table><tr><td>2026年第九期央行票据（香港）</td><td>600亿元</td><td>6个月（182天）</td><td>1.37%</td></tr></table>中国人民银行公开市场业务操作室`, 0, false},
		{"overnight only", `<a>打印本页</a><p>2026年9月30日开展了6985亿元隔夜逆回购操作。</p>`, 0, false},
	}
	for _, tc := range cases {
		_, rate, ok, err := parseOMODetail(tc.body)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if ok != tc.ok || rate != tc.rate {
			t.Errorf("%s: rate=%v ok=%v, want %v %v", tc.name, rate, ok, tc.rate, tc.ok)
		}
	}
	if _, _, _, err := parseOMODetail(`<p>开展了逆回购操作 no date here</p>`); err == nil {
		t.Error("missing date must be an error")
	}
}

func TestParseOMOListItemsAndNext(t *testing.T) {
	items, next := parseOMOList(string(fixture(t, "pbc_omo_list.html")))
	if len(items) != 3 {
		t.Fatalf("items = %+v, want 3 (nav link excluded)", items)
	}
	if items[0].title != "公开市场业务交易公告 [2026]第191号" || !items[0].listDate.Equal(dateUTC(2026, 9, 29)) {
		t.Fatalf("first item = %+v", items[0])
	}
	if next != omoDir+"17081-2.html" {
		t.Fatalf("next = %q", next)
	}
}

func omoServer(t *testing.T, hits map[string]int) *OMOCollector {
	t.Helper()
	client, srv := testClient(t, "pbc-omo-test", func(w http.ResponseWriter, r *http.Request) {
		hits[r.URL.Path]++
		switch r.URL.Path {
		case omoListPath:
			_, _ = w.Write(fixture(t, "pbc_omo_list.html"))
		case omoDir + "2026092908461628271/index.html":
			_, _ = w.Write(fixture(t, "pbc_omo_detail_2026_fixed_rate.html"))
		case omoDir + "2026092808454683233/index.html":
			_, _ = w.Write(fixture(t, "pbc_omo_detail_zero_volume.html"))
		case omoDir + "2026092408454713496/index.html":
			http.Error(w, "boom", http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	})
	c := NewOMOCollector(client)
	c.baseURL = srv.URL
	return c
}

func TestOMOLatestKeepsParsedDespiteOneDetailFailure(t *testing.T) {
	hits := map[string]int{}
	c := omoServer(t, hits)
	snaps, err := c.GetSnapshots(context.Background())
	if err == nil {
		t.Fatal("want partial error for the failing announcement")
	}
	if len(snaps) != 1 || snaps[0].MetricID != metricOMO7D || snaps[0].Value != 1.40 || !snaps[0].Timestamp.Equal(dateUTC(2026, 9, 29)) {
		t.Fatalf("snaps = %+v", snaps)
	}
}

func TestOMOWindowStopsAtStartAndDoesNotPage(t *testing.T) {
	hits := map[string]int{}
	c := omoServer(t, hits)
	snaps, err := c.GetSnapshotsForWindow(context.Background(), dateUTC(2026, 9, 25), dateUTC(2026, 9, 30))
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 || snaps[0].Value != 1.40 {
		t.Fatalf("snaps = %+v", snaps)
	}
	if hits[omoDir+"17081-2.html"] != 0 {
		t.Fatal("must not page past the window start")
	}
	if hits[omoDir+"2026092408454713496/index.html"] != 0 {
		t.Fatal("must not fetch announcements listed before the window")
	}
}
