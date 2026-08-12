package main

import (
	"context"
	"errors"
	"testing"

	"capital_observatory/internal/core/alert"
	"capital_observatory/internal/core/classification"
	"capital_observatory/internal/core/research"
	"capital_observatory/pkg/model"
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
