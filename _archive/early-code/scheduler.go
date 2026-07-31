// Package scheduler 编排 Plugin 的采集和检测周期。
package scheduler

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/capital-observatory/capital-observatory/core"
	"github.com/capital-observatory/capital-observatory/core/detector"
)

// Scheduler 管理采集和检测的定时任务。
type Scheduler struct {
	registry    *core.Registry
	metricSvc   core.MetricService
	ontologySvc core.OntologyService
	detEngine   *detector.Engine

	interval time.Duration
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewScheduler 创建一个 Scheduler。
func NewScheduler(
	registry *core.Registry,
	metricSvc core.MetricService,
	ontologySvc core.OntologyService,
	detEngine *detector.Engine,
	interval time.Duration,
) *Scheduler {
	return &Scheduler{
		registry:    registry,
		metricSvc:   metricSvc,
		ontologySvc: ontologySvc,
		detEngine:   detEngine,
		interval:    interval,
		stopCh:      make(chan struct{}),
	}
}

// Start 启动调度器。会立即执行一次采集和检测，然后按 interval 周期执行。
func (s *Scheduler) Start(ctx context.Context) error {
	// 1. 注册所有 Plugin 的 Metric 和 Ontology
	if err := s.registerAll(ctx); err != nil {
		return fmt.Errorf("register plugins: %w", err)
	}

	// 2. 立即执行一次
	s.runCycle(ctx)

	// 3. 启动周期任务
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				s.runCycle(ctx)
			case <-s.stopCh:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	log.Printf("[Scheduler] started with interval %s", s.interval)
	return nil
}

// Stop 停止调度器。
func (s *Scheduler) Stop() {
	close(s.stopCh)
	s.wg.Wait()
	log.Println("[Scheduler] stopped")
}

// registerAll 注册所有 Plugin 的 Metric 和 Ontology。
func (s *Scheduler) registerAll(ctx context.Context) error {
	for _, p := range s.registry.All() {
		log.Printf("[Scheduler] registering plugin: %s", p.Name())

		// 注册 Metric
		for _, m := range p.Metrics() {
			if err := s.metricSvc.Register(ctx, m); err != nil {
				return fmt.Errorf("register metric %s: %w", m.ID, err)
			}
		}

		// 注册 Entity
		for _, e := range p.Entities() {
			if err := s.ontologySvc.RegisterEntity(ctx, e); err != nil {
				return fmt.Errorf("register entity %s: %w", e.ID, err)
			}
		}

		// 注册 Relation
		for _, r := range p.Relations() {
			if err := s.ontologySvc.RegisterRelation(ctx, r); err != nil {
				return fmt.Errorf("register relation: %w", err)
			}
		}

		log.Printf("[Scheduler] plugin %s registered: %d metrics, %d entities, %d relations",
			p.Name(), len(p.Metrics()), len(p.Entities()), len(p.Relations()))
	}
	return nil
}

// runCycle 执行一个完整的采集-检测周期。
func (s *Scheduler) runCycle(ctx context.Context) {
	log.Println("[Scheduler] starting cycle...")

	// Phase 1: Collect
	for _, p := range s.registry.All() {
		snapshots, err := p.Collect(ctx)
		if err != nil {
			log.Printf("[Scheduler] plugin %s collect error: %v", p.Name(), err)
			continue
		}

		if err := s.metricSvc.WriteBatch(ctx, snapshots); err != nil {
			log.Printf("[Scheduler] plugin %s write error: %v", p.Name(), err)
			continue
		}

		log.Printf("[Scheduler] plugin %s collected %d snapshots", p.Name(), len(snapshots))
	}

	// Phase 2: Detect
	alerts, err := s.detEngine.RunAll(ctx)
	if err != nil {
		log.Printf("[Scheduler] detect error: %v", err)
		return
	}

	log.Printf("[Scheduler] detected %d alerts", len(alerts))
	for _, a := range alerts {
		log.Printf("  [%s] %s", a.Severity, a.Title)
	}

	log.Println("[Scheduler] cycle complete")
}

// RunOnce 执行一次采集和检测（不启动周期任务）。
func (s *Scheduler) RunOnce(ctx context.Context) error {
	if err := s.registerAll(ctx); err != nil {
		return fmt.Errorf("register plugins: %w", err)
	}
	s.runCycle(ctx)
	return nil
}
