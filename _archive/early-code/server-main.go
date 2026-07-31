// Capital Observatory — 资本市场可观测性平台
//
// 启动方式：
//
//	go run ./cmd/server
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/capital-observatory/capital-observatory/core"
	"github.com/capital-observatory/capital-observatory/core/detector"
	"github.com/capital-observatory/capital-observatory/core/research"
	"github.com/capital-observatory/capital-observatory/core/scheduler"
	"github.com/capital-observatory/capital-observatory/internal/store"

	// 导入 Plugin（触发 init() 注册）
	_ "github.com/capital-observatory/capital-observatory/plugin/etf"
	_ "github.com/capital-observatory/capital-observatory/plugin/crypto"
	_ "github.com/capital-observatory/capital-observatory/plugin/macro"
)

func main() {
	log.SetFlags(log.Ltime | log.Lshortfile)
	log.Println("=== Capital Observatory ===")

	// ──────────────────────────────────────────
	// 1. 初始化存储层（MVP: 内存存储）
	// ──────────────────────────────────────────
	metricStore := store.NewMemoryMetricStore()
	ontologyStore := store.NewMemoryOntologyStore()
	alertStore := store.NewMemoryAlertStore()

	// ──────────────────────────────────────────
	// 2. 初始化检测引擎
	// ──────────────────────────────────────────
	detEngine := detector.NewEngine(metricStore, alertStore)

	// ──────────────────────────────────────────
	// 3. 构建 Plugin Registry（插件已在 init() 中注册到全局 registry）
	// ──────────────────────────────────────────
	pluginRegistry := buildRegistry()

	researchSvc := research.NewService(alertStore, metricStore, ontologyStore, pluginRegistry)

	// ──────────────────────────────────────────
	// 4. 启动 Scheduler
	// ──────────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sched := scheduler.NewScheduler(
		pluginRegistry,
		metricStore,
		ontologyStore,
		detEngine,
		1*time.Minute, // 每分钟采集一次（MVP 用模拟数据）
	)

	if err := sched.Start(ctx); err != nil {
		log.Fatalf("failed to start scheduler: %v", err)
	}

	// ──────────────────────────────────────────
	// 5. 打印当前状态
	// ──────────────────────────────────────────
	printStatus(ctx, pluginRegistry, metricStore, ontologyStore, alertStore, researchSvc)

	// ──────────────────────────────────────────
	// 6. 等待退出信号
	// ──────────────────────────────────────────
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Println("Press Ctrl+C to stop...")
	<-sigCh

	log.Println("Shutting down...")
	sched.Stop()
	cancel()
	fmt.Println("Bye!")
}

// buildRegistry 从全局注册的 Plugin 构建 Registry。
func buildRegistry() *core.Registry {
	registry := core.NewRegistry()
	for _, p := range core.AllPlugins() {
		if err := registry.Register(p); err != nil {
			log.Printf("warning: failed to register plugin %s: %v", p.Name(), err)
		}
	}
	return registry
}

func printStatus(
	ctx context.Context,
	registry *core.Registry,
	metricSvc core.MetricService,
	ontologySvc core.OntologyService,
	alertSvc core.AlertService,
	researchSvc *research.Service,
) {
	fmt.Println("\n─────────────────────────────────────────")
	fmt.Println("System Status")
	fmt.Println("─────────────────────────────────────────")

	// Plugin
	fmt.Printf("\nPlugins: %d\n", len(registry.All()))
	for _, p := range registry.All() {
		fmt.Printf("  • %s v%s (%d metrics, %d entities, %d relations)\n",
			p.Name(), p.Version(),
			len(p.Metrics()), len(p.Entities()), len(p.Relations()))
	}

	// Metric
	metrics, _ := metricSvc.List(ctx)
	fmt.Printf("\nMetrics: %d\n", len(metrics))

	// Entity
	entities, _ := ontologySvc.ListEntities(ctx)
	fmt.Printf("Entities: %d\n", len(entities))

	// Relation
	relations, _ := ontologySvc.ListRelations(ctx)
	fmt.Printf("Relations: %d\n", len(relations))

	// Alert
	alerts, _ := alertSvc.ListRecent(ctx, 10)
	fmt.Printf("Alerts: %d\n", len(alerts))
	for _, a := range alerts {
		fmt.Printf("  [%s] %s\n", a.Severity, a.Title)
	}

	fmt.Println("─────────────────────────────────────────\n")
}
