package main

import (
	"context"
	"errors"
	"testing"

	"sonde/internal/core/alert"
	"sonde/internal/core/classification"
	"sonde/internal/core/research"
	"sonde/pkg/model"
)

func TestDistinctMetricCountIncludesSeedAndDeduplicates(t *testing.T) {
	tests := []struct {
		name    string
		cluster classification.EventCluster
		want    int
	}{
		{name: "empty", cluster: classification.EventCluster{}, want: 0},
		{name: "legacy seed", cluster: classification.EventCluster{Alerts: []string{"a1"}}, want: 1},
		{
			name: "same metric alerts",
			cluster: classification.EventCluster{
				Alerts:    []string{"a1", "a2"},
				MetricIDs: []string{"metric.one", "metric.one"},
			},
			want: 1,
		},
		{
			name: "seed plus second metric",
			cluster: classification.EventCluster{
				Alerts:    []string{"a1", "a2"},
				MetricIDs: []string{"metric.one", "metric.two"},
			},
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := distinctMetricCount(&tt.cluster); got != tt.want {
				t.Fatalf("distinctMetricCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

type fakeAlertLoader struct {
	alert     *model.Alert
	err       error
	requested string
}

func (f *fakeAlertLoader) GetAlertByID(_ context.Context, alertID string) (*model.Alert, error) {
	f.requested = alertID
	return f.alert, f.err
}

type fakeResearchAssembler struct {
	assembleErr error
	saveErr     error
	assembled   *model.Alert
	saved       *research.ResearchContext
}

func (f *fakeResearchAssembler) Assemble(_ context.Context, persisted model.Alert) (*research.ResearchContext, error) {
	f.assembled = &persisted
	if f.assembleErr != nil {
		return nil, f.assembleErr
	}
	return &research.ResearchContext{AlertID: persisted.ID}, nil
}

func (f *fakeResearchAssembler) SaveSnapshot(_ context.Context, rc *research.ResearchContext) error {
	f.saved = rc
	return f.saveErr
}

func TestHandleResearchRequestUsesPersistedAlert(t *testing.T) {
	loader := &fakeAlertLoader{alert: &model.Alert{ID: "alt_001", MetricID: "metric.persisted"}}
	assembler := &fakeResearchAssembler{}
	event := alert.OutboxEvent{Payload: []byte(`{"alert_id":"alt_001"}`)}

	if err := handleResearchRequest(context.Background(), event, loader, assembler); err != nil {
		t.Fatalf("handleResearchRequest() error = %v", err)
	}
	if loader.requested != "alt_001" {
		t.Fatalf("loaded alert = %q, want alt_001", loader.requested)
	}
	if assembler.assembled == nil || assembler.assembled.MetricID != "metric.persisted" {
		t.Fatalf("assembled alert = %+v, want persisted row", assembler.assembled)
	}
	if assembler.saved == nil || assembler.saved.AlertID != "alt_001" {
		t.Fatalf("saved context = %+v, want alt_001", assembler.saved)
	}
}

func TestHandleResearchRequestReturnsRetryableFailures(t *testing.T) {
	transient := errors.New("database unavailable")
	tests := []struct {
		name      string
		event     alert.OutboxEvent
		loader    *fakeAlertLoader
		assembler *fakeResearchAssembler
		wantIs    error
	}{
		{
			name:      "alert load",
			event:     alert.OutboxEvent{Payload: []byte(`{"alert_id":"alt_001"}`)},
			loader:    &fakeAlertLoader{err: transient},
			assembler: &fakeResearchAssembler{},
			wantIs:    transient,
		},
		{
			name:      "assembly",
			event:     alert.OutboxEvent{Payload: []byte(`{"alert_id":"alt_001"}`)},
			loader:    &fakeAlertLoader{alert: &model.Alert{ID: "alt_001"}},
			assembler: &fakeResearchAssembler{assembleErr: transient},
			wantIs:    transient,
		},
		{
			name:      "save",
			event:     alert.OutboxEvent{Payload: []byte(`{"alert_id":"alt_001"}`)},
			loader:    &fakeAlertLoader{alert: &model.Alert{ID: "alt_001"}},
			assembler: &fakeResearchAssembler{saveErr: transient},
			wantIs:    transient,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handleResearchRequest(context.Background(), tt.event, tt.loader, tt.assembler)
			if !errors.Is(err, tt.wantIs) {
				t.Fatalf("error = %v, want wrapped %v", err, tt.wantIs)
			}
		})
	}
}

func TestHandleResearchRequestRejectsPermanentFailures(t *testing.T) {
	tests := []struct {
		name   string
		event  alert.OutboxEvent
		loader *fakeAlertLoader
	}{
		{name: "malformed json", event: alert.OutboxEvent{Payload: []byte(`{"alert_id"`)}, loader: &fakeAlertLoader{}},
		{name: "missing alert id", event: alert.OutboxEvent{Payload: []byte(`{}`)}, loader: &fakeAlertLoader{}},
		{name: "unknown alert", event: alert.OutboxEvent{Payload: []byte(`{"alert_id":"missing"}`)}, loader: &fakeAlertLoader{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := handleResearchRequest(context.Background(), tt.event, tt.loader, &fakeResearchAssembler{}); err == nil {
				t.Fatal("handleResearchRequest() returned nil")
			}
		})
	}
}

// TestDetectorRegistrationCoversEveryPluginDetector 钉住 Core 侧的注册面。
//
// 插件是独立模块，不能反向导入 internal/core（Core 不懂金融语义、插件不依赖
// Core 内部是硬约束），所以它们无从得知这里注册了什么。两侧各钉一半：Core 钉
// 注册面，每个插件的 catalog 测试钉「申报的检测器名都在这五个之内」。任一侧
// 漂移，都会有一边先红，而不是等到生产环境里每轮打 WARN 却没人看。
func TestDetectorRegistrationCoversEveryPluginDetector(t *testing.T) {
	want := []string{"moving_average", "percentile", "threshold", "trend", "volatility"}
	got := newDetectorEngine().Names()
	if len(got) != len(want) {
		t.Fatalf("registered detectors = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("registered detectors = %v, want %v", got, want)
		}
	}
}
