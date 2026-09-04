package pluginmgr

import (
	"testing"
	"time"

	pb "sonde/pkg/proto/plugin/v1"
)

func TestIsPluginHealthy_NilState(t *testing.T) {
	m := &Manager{
		sessions:      make(map[string]*StreamSession),
		health:        make(map[string]*PluginHealth),
		healthTimeout: 60 * time.Second,
	}

	// No session, no health → unhealthy.
	if m.isPluginHealthy("plg_unknown") {
		t.Error("plugin with no session should be unhealthy")
	}
}

func TestIsPluginHealthy_FreshHeartbeat(t *testing.T) {
	m := &Manager{
		sessions:      make(map[string]*StreamSession),
		health:        make(map[string]*PluginHealth),
		healthTimeout: 60 * time.Second,
	}

	// Simulate a registered session + fresh health entry.
	m.sessions["plg_etf"] = &StreamSession{pluginID: "plg_etf"}
	m.health["plg_etf"] = &PluginHealth{lastActivity: time.Now()}

	if !m.isPluginHealthy("plg_etf") {
		t.Error("plugin with active session and fresh heartbeat should be healthy")
	}
}

func TestIsPluginHealthy_StaleHeartbeat(t *testing.T) {
	m := &Manager{
		sessions:      make(map[string]*StreamSession),
		health:        make(map[string]*PluginHealth),
		healthTimeout: 60 * time.Second,
	}

	// Active session but last health update was 2 minutes ago.
	m.sessions["plg_etf"] = &StreamSession{pluginID: "plg_etf"}
	m.health["plg_etf"] = &PluginHealth{lastActivity: time.Now().Add(-2 * time.Minute)}

	if m.isPluginHealthy("plg_etf") {
		t.Error("plugin with stale (>60s) heartbeat should be unhealthy")
	}
}

func TestIsPluginHealthy_NoHealthEntry(t *testing.T) {
	m := &Manager{
		sessions:      make(map[string]*StreamSession),
		health:        make(map[string]*PluginHealth),
		healthTimeout: 60 * time.Second,
	}

	// Session exists but health map has no entry (shouldn't normally happen,
	// but the function should fail-closed → unhealthy).
	m.sessions["plg_etf"] = &StreamSession{pluginID: "plg_etf"}

	if m.isPluginHealthy("plg_etf") {
		t.Error("plugin with no health entry should be unhealthy (fail-closed)")
	}
}

func TestMarkHealthActivity_UpdatesTimestamp(t *testing.T) {
	m := &Manager{
		sessions: make(map[string]*StreamSession),
		health: map[string]*PluginHealth{
			"plg_etf": {lastActivity: time.Now().Add(-5 * time.Minute)},
		},
	}

	m.markHealthActivity("plg_etf")

	h := m.health["plg_etf"]
	h.mu.Lock()
	defer h.mu.Unlock()
	if time.Since(h.lastActivity) > 10*time.Second {
		t.Error("markHealthActivity should update lastActivity to now")
	}
}

func TestMarkHealthActivity_MissingPluginNoPanic(t *testing.T) {
	// Should not panic when pluginID not in health map.
	m := &Manager{
		sessions: make(map[string]*StreamSession),
		health:   make(map[string]*PluginHealth),
	}
	m.markHealthActivity("plg_nonexistent")
}

func TestCollectionHealthDeratesAndRecoversInMemoryReputation(t *testing.T) {
	m := &Manager{
		sessions:      map[string]*StreamSession{"plg_etf": {pluginID: "plg_etf"}},
		health:        map[string]*PluginHealth{"plg_etf": {lastActivity: time.Now()}},
		healthTimeout: 60 * time.Second,
	}

	m.markCollectionHealth("plg_etf", &pb.PluginStatus{
		LastCollectError:  "provider timeout",
		ConsecutiveErrors: 1,
	})
	if m.isPluginHealthy("plg_etf") {
		t.Fatal("fresh session with failed collection should be unhealthy")
	}

	// Runtime-only and legacy heartbeats refresh liveness but cannot erase the
	// most recent collection outcome.
	m.markCollectionHealth("plg_etf", &pb.PluginStatus{Runtime: map[string]string{"circuit_state": "open"}})
	m.markCollectionHealth("plg_etf", nil)
	if m.isPluginHealthy("plg_etf") {
		t.Fatal("status-less heartbeat erased failed collection outcome")
	}

	m.markCollectionHealth("plg_etf", &pb.PluginStatus{
		LastCollectAt:    time.Now().Unix(),
		LastCollectCount: 3,
	})
	if !m.isPluginHealthy("plg_etf") {
		t.Fatal("successful collection should restore in-memory health")
	}
}
