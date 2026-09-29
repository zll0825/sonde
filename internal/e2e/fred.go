package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"
)

// FREDReplay serves recorded FRED observation files in place of
// api.stlouisfed.org. It answers the query parameters the collector actually
// sends (observation_start/end, sort_order, limit, offset), so the real
// fred.Collector code paths — latest poll and paged backfill — both run.
//
// Fixtures are FRED's own response body, one file per series:
// <dir>/<SERIES_ID>.json, or <dir>/<SERIES_ID>.<units>.json when the binding
// asks for a units transform (e.g. CPIAUCSL.pc1.json).
//
// Dates shift forward by whole weeks so that Anchor lands on the most recent
// same weekday on or before today. Rule evaluation looks back from
// time.Now(), so unshifted fixtures would age out of every window; whole
// weeks keep weekly series on their release weekday.
type FREDReplay struct {
	t     *testing.T
	dir   string
	shift time.Duration

	mu    sync.Mutex
	files map[string][]fredObs
}

type fredObs struct {
	Date  string `json:"date"`
	Value string `json:"value"`
}

// NewFREDReplay loads fixtures from dir; anchor is the fixture date that
// should read as "this week".
func NewFREDReplay(t *testing.T, dir string, anchor time.Time) *FREDReplay {
	t.Helper()
	return &FREDReplay{
		t:     t,
		dir:   dir,
		shift: WeekShift(anchor, time.Now()),
		files: make(map[string][]fredObs),
	}
}

// WeekShift returns the whole-week offset that moves anchor to the latest
// same weekday on or before now.
func WeekShift(anchor, now time.Time) time.Duration {
	a := time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, time.UTC)
	n := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	days := int(n.Sub(a).Hours() / 24)
	if days < 0 {
		days -= 6
	}
	return time.Duration(days/7*7) * 24 * time.Hour
}

// Shift is the offset added to every fixture date.
func (r *FREDReplay) Shift() time.Duration { return r.shift }

// Client returns an *http.Client whose transport is this replay.
func (r *FREDReplay) Client() *http.Client { return &http.Client{Transport: r} }

// RoundTrip implements http.RoundTripper.
func (r *FREDReplay) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "api.stlouisfed.org" || req.URL.Path != "/fred/series/observations" {
		r.t.Errorf("FRED replay: unexpected request %s %s%s", req.Method, req.URL.Host, req.URL.Path)
		return respond(req, http.StatusNotFound, []byte(`{"error_message":"not recorded"}`)), nil
	}
	q := req.URL.Query()
	if q.Get("api_key") == "" {
		return respond(req, http.StatusBadRequest, []byte(`{"error_message":"api_key is not set"}`)), nil
	}

	obs, err := r.load(q.Get("series_id"), q.Get("units"))
	if err != nil {
		r.t.Errorf("FRED replay: %v", err)
		return respond(req, http.StatusBadRequest, []byte(`{"error_message":"Bad Request.  The series does not exist."}`)), nil
	}

	start, _ := time.Parse("2006-01-02", q.Get("observation_start"))
	end, _ := time.Parse("2006-01-02", q.Get("observation_end"))
	var out []fredObs
	for _, o := range obs {
		d, err := time.Parse("2006-01-02", o.Date)
		if err != nil {
			continue
		}
		d = d.Add(r.shift)
		if (!start.IsZero() && d.Before(start)) || (!end.IsZero() && d.After(end)) {
			continue
		}
		out = append(out, fredObs{Date: d.Format("2006-01-02"), Value: o.Value})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	if q.Get("sort_order") == "desc" {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	if off, _ := strconv.Atoi(q.Get("offset")); off > 0 {
		if off >= len(out) {
			out = nil
		} else {
			out = out[off:]
		}
	}
	if limit, _ := strconv.Atoi(q.Get("limit")); limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	if out == nil {
		out = []fredObs{}
	}
	body, err := json.Marshal(map[string]any{"count": len(out), "observations": out})
	if err != nil {
		return nil, err
	}
	return respond(req, http.StatusOK, body), nil
}

func (r *FREDReplay) load(series, units string) ([]fredObs, error) {
	name := series + ".json"
	if units != "" && units != "lin" {
		name = series + "." + units + ".json"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if obs, ok := r.files[name]; ok {
		return obs, nil
	}
	raw, err := os.ReadFile(filepath.Join(r.dir, name))
	if err != nil {
		return nil, fmt.Errorf("no fixture for series %q units %q: %w", series, units, err)
	}
	var body struct {
		Observations []fredObs `json:"observations"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("decode fixture %s: %w", name, err)
	}
	r.files[name] = body.Observations
	return body.Observations, nil
}

func respond(req *http.Request, status int, body []byte) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}
