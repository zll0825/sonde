// Package store 提供 Core 服务的内存实现。
//
// MVP 阶段使用内存存储，后续替换为 TimescaleDB + PostgreSQL。
package store

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/capital-observatory/capital-observatory/core"
)

// ──────────────────────────────────────────────
// MemoryMetricStore
// ──────────────────────────────────────────────

// MemoryMetricStore 是 MetricService 的内存实现。
type MemoryMetricStore struct {
	mu        sync.RWMutex
	defs      map[core.MetricID]*core.Metric
	snapshots map[core.MetricID][]core.MetricSnapshot
}

func NewMemoryMetricStore() *MemoryMetricStore {
	return &MemoryMetricStore{
		defs:      make(map[core.MetricID]*core.Metric),
		snapshots: make(map[core.MetricID][]core.MetricSnapshot),
	}
}

func (s *MemoryMetricStore) Register(_ context.Context, m core.Metric) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defs[m.ID] = &m
	return nil
}

func (s *MemoryMetricStore) Get(_ context.Context, id core.MetricID) (*core.Metric, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.defs[id]
	if !ok {
		return nil, fmt.Errorf("metric %q not found", id)
	}
	return m, nil
}

func (s *MemoryMetricStore) List(_ context.Context) ([]core.Metric, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]core.Metric, 0, len(s.defs))
	for _, m := range s.defs {
		result = append(result, *m)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func (s *MemoryMetricStore) Write(_ context.Context, snapshot core.MetricSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[snapshot.MetricID] = append(s.snapshots[snapshot.MetricID], snapshot)
	return nil
}

func (s *MemoryMetricStore) WriteBatch(ctx context.Context, snapshots []core.MetricSnapshot) error {
	for _, snap := range snapshots {
		if err := s.Write(ctx, snap); err != nil {
			return err
		}
	}
	return nil
}

func (s *MemoryMetricStore) Query(_ context.Context, id core.MetricID, from, to time.Time) ([]core.MetricSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := s.snapshots[id]
	var result []core.MetricSnapshot
	for _, snap := range all {
		if !snap.Timestamp.Before(from) && !snap.Timestamp.After(to) {
			result = append(result, snap)
		}
	}
	return result, nil
}

func (s *MemoryMetricStore) QueryLatest(_ context.Context, id core.MetricID, n int) ([]core.MetricSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := s.snapshots[id]
	if len(all) <= n {
		result := make([]core.MetricSnapshot, len(all))
		copy(result, all)
		return result, nil
	}
	result := make([]core.MetricSnapshot, n)
	copy(result, all[len(all)-n:])
	return result, nil
}

// ──────────────────────────────────────────────
// MemoryOntologyStore
// ──────────────────────────────────────────────

// MemoryOntologyStore 是 OntologyService 的内存实现。
type MemoryOntologyStore struct {
	mu        sync.RWMutex
	entities  map[string]*core.Entity
	relations map[string]*core.Relation // key: "sourceID:targetID:relType"
}

func NewMemoryOntologyStore() *MemoryOntologyStore {
	return &MemoryOntologyStore{
		entities:  make(map[string]*core.Entity),
		relations: make(map[string]*core.Relation),
	}
}

func relationKey(sourceID, targetID string, relType core.RelationType) string {
	return fmt.Sprintf("%s:%s:%s", sourceID, targetID, relType)
}

func (s *MemoryOntologyStore) RegisterEntity(_ context.Context, e core.Entity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entities[e.ID] = &e
	return nil
}

func (s *MemoryOntologyStore) RegisterRelation(_ context.Context, r core.Relation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := relationKey(r.SourceID, r.TargetID, r.RelationType)
	s.relations[key] = &r
	return nil
}

func (s *MemoryOntologyStore) GetEntity(_ context.Context, id string) (*core.Entity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entities[id]
	if !ok {
		return nil, fmt.Errorf("entity %q not found", id)
	}
	return e, nil
}

func (s *MemoryOntologyStore) GetRelation(_ context.Context, sourceID, targetID string, relType core.RelationType) (*core.Relation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := relationKey(sourceID, targetID, relType)
	r, ok := s.relations[key]
	if !ok {
		return nil, fmt.Errorf("relation %s not found", key)
	}
	return r, nil
}

func (s *MemoryOntologyStore) ListEntities(_ context.Context) ([]core.Entity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]core.Entity, 0, len(s.entities))
	for _, e := range s.entities {
		result = append(result, *e)
	}
	return result, nil
}

func (s *MemoryOntologyStore) ListRelations(_ context.Context) ([]core.Relation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]core.Relation, 0, len(s.relations))
	for _, r := range s.relations {
		result = append(result, *r)
	}
	return result, nil
}

func (s *MemoryOntologyStore) GetNeighbors(_ context.Context, entityID string, depth int) (*core.EntityGraph, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	root, ok := s.entities[entityID]
	if !ok {
		return nil, fmt.Errorf("entity %q not found", entityID)
	}

	graph := &core.EntityGraph{
		Root: *root,
	}

	visited := map[string]bool{entityID: true}
	queue := []struct {
		id    string
		depth int
	}{{entityID, 0}}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.depth >= depth {
			continue
		}

		for _, rel := range s.relations {
			var neighborID string
			var edge core.GraphEdge

			if rel.SourceID == current.id && !visited[rel.TargetID] {
				neighborID = rel.TargetID
				if e, ok := s.entities[neighborID]; ok {
					edge = core.GraphEdge{Target: *e, Relation: *rel}
				}
			} else if rel.Direction == core.DirBidirectional && rel.TargetID == current.id && !visited[rel.SourceID] {
				neighborID = rel.SourceID
				if e, ok := s.entities[neighborID]; ok {
					edge = core.GraphEdge{Target: *e, Relation: *rel}
				}
			}

			if neighborID != "" {
				visited[neighborID] = true
				graph.Neighbors = append(graph.Neighbors, edge)
				queue = append(queue, struct {
					id    string
					depth int
				}{neighborID, current.depth + 1})
			}
		}
	}

	return graph, nil
}

func (s *MemoryOntologyStore) GetAlertContext(_ context.Context, metricID core.MetricID) (*core.ResearchContext, error) {
	// 通过 MetricID 找到关联的 Entity
	s.mu.RLock()
	defer s.mu.RUnlock()

	var entityID string
	for _, e := range s.entities {
		// 简单匹配：MetricID 的 namespace + entity 部分与 Entity ID 匹配
		// 实际实现中应该通过 metric_entities 表查询
		if e.Plugin != "" {
			entityID = e.ID
			break
		}
	}

	if entityID == "" {
		return &core.ResearchContext{}, nil
	}

	// TODO: 通过 metric_entities 表精确匹配
	_ = entityID

	return &core.ResearchContext{}, nil
}

func (s *MemoryOntologyStore) FindPath(_ context.Context, from, to string) ([]core.Relation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// BFS 查找最短路径
	type node struct {
		id   string
		path []core.Relation
	}

	visited := map[string]bool{from: true}
	queue := []node{{from, nil}}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.id == to {
			return current.path, nil
		}

		for _, rel := range s.relations {
			var nextID string
			if rel.SourceID == current.id {
				nextID = rel.TargetID
			} else if rel.Direction == core.DirBidirectional && rel.TargetID == current.id {
				nextID = rel.SourceID
			}

			if nextID != "" && !visited[nextID] {
				visited[nextID] = true
				newPath := make([]core.Relation, len(current.path)+1)
				copy(newPath, current.path)
				newPath[len(current.path)] = *rel
				queue = append(queue, node{nextID, newPath})
			}
		}
	}

	return nil, fmt.Errorf("no path found from %q to %q", from, to)
}

// ──────────────────────────────────────────────
// MemoryAlertStore
// ──────────────────────────────────────────────

// MemoryAlertStore 是 AlertService 的内存实现。
type MemoryAlertStore struct {
	mu     sync.RWMutex
	alerts map[string]*core.Alert
	dedup  map[string]bool // dedup_key -> exists
	order  []string        // 按时间排序的 ID
}

func NewMemoryAlertStore() *MemoryAlertStore {
	return &MemoryAlertStore{
		alerts: make(map[string]*core.Alert),
		dedup:  make(map[string]bool),
	}
}

func (s *MemoryAlertStore) Create(_ context.Context, alert core.Alert) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if alert.ID == "" {
		alert.ID = fmt.Sprintf("alert-%d", len(s.alerts)+1)
	}

	if alert.DedupKey != "" {
		if s.dedup[alert.DedupKey] {
			return nil // 已存在，去重
		}
		s.dedup[alert.DedupKey] = true
	}

	s.alerts[alert.ID] = &alert
	s.order = append(s.order, alert.ID)
	return nil
}

func (s *MemoryAlertStore) Get(_ context.Context, id string) (*core.Alert, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.alerts[id]
	if !ok {
		return nil, fmt.Errorf("alert %q not found", id)
	}
	return a, nil
}

func (s *MemoryAlertStore) ListRecent(_ context.Context, n int) ([]core.Alert, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	total := len(s.order)
	if n > total {
		n = total
	}

	result := make([]core.Alert, n)
	for i := 0; i < n; i++ {
		id := s.order[total-1-i]
		result[i] = *s.alerts[id]
	}
	return result, nil
}

func (s *MemoryAlertStore) ListBySeverity(_ context.Context, severity core.Severity, n int) ([]core.Alert, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []core.Alert
	for i := len(s.order) - 1; i >= 0 && len(result) < n; i-- {
		a := s.alerts[s.order[i]]
		if a.Severity == severity {
			result = append(result, *a)
		}
	}
	return result, nil
}

func (s *MemoryAlertStore) Dedup(_ context.Context, dedupKey string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dedup[dedupKey], nil
}
