package core

import (
	"context"
	"fmt"
	"sync"
)

// ──────────────────────────────────────────────
// Plugin 接口
// ──────────────────────────────────────────────

// Plugin 是领域能力包的统一接口。
//
// 每个 Plugin 声明自己拥有的 Metric、Entity、Relation，
// 提供数据采集能力，并定义默认的检测策略和研究上下文。
//
// 加载方式可以演进：
//   - Phase 1: 编译时注册（init + import）
//   - Phase 2: 进程隔离（gRPC）
//   - Phase 3: WASM 沙箱（可选）
type Plugin interface {
	// Name 返回 Plugin 的唯一名称。
	Name() string

	// Version 返回版本号。
	Version() string

	// Metrics 返回本 Plugin 声明的所有 Metric。
	Metrics() []Metric

	// Entities 返回本 Plugin 声明的所有 Ontology Entity。
	Entities() []Entity

	// Relations 返回本 Plugin 声明的所有 Ontology Relation。
	Relations() []Relation

	// Collect 执行一次数据采集，返回当前所有 Metric 的快照。
	Collect(ctx context.Context) ([]MetricSnapshot, error)

	// DefaultDetectors 返回本 Plugin 推荐的检测器配置。
	DefaultDetectors() []DetectorConfig

	// BuildResearchContext 为一个 Alert 构建研究上下文。
	// Plugin 提供领域默认上下文，Ontology 提供跨实体扩展。
	BuildResearchContext(ctx context.Context, alert Alert) (*ResearchContext, error)
}

// ──────────────────────────────────────────────
// Plugin Registry
// ──────────────────────────────────────────────

// Registry 管理所有已注册的 Plugin。
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]Plugin
	order   []string // 保持注册顺序
}

// NewRegistry 创建一个新的 Plugin Registry。
func NewRegistry() *Registry {
	return &Registry{
		plugins: make(map[string]Plugin),
	}
}

// Register 注册一个 Plugin。如果同名 Plugin 已存在，返回错误。
func (r *Registry) Register(p Plugin) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := p.Name()
	if _, exists := r.plugins[name]; exists {
		return fmt.Errorf("plugin %q already registered", name)
	}

	r.plugins[name] = p
	r.order = append(r.order, name)
	return nil
}

// Get 根据名称获取 Plugin。
func (r *Registry) Get(name string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plugins[name]
	return p, ok
}

// All 返回所有已注册的 Plugin，按注册顺序。
func (r *Registry) All() []Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Plugin, 0, len(r.order))
	for _, name := range r.order {
		result = append(result, r.plugins[name])
	}
	return result
}

// Names 返回所有已注册 Plugin 的名称。
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]string, len(r.order))
	copy(result, r.order)
	return result
}

// ──────────────────────────────────────────────
// 全局注册函数（编译时注册模式）
// ──────────────────────────────────────────────

var defaultRegistry = NewRegistry()

// RegisterPlugin 向全局 Registry 注册一个 Plugin。
// 通常在 Plugin 的 init() 函数中调用。
func RegisterPlugin(p Plugin) {
	if err := defaultRegistry.Register(p); err != nil {
		panic(fmt.Sprintf("capital_observatory: failed to register plugin: %v", err))
	}
}

// GetPlugin 从全局 Registry 获取 Plugin。
func GetPlugin(name string) (Plugin, bool) {
	return defaultRegistry.Get(name)
}

// AllPlugins 返回全局 Registry 中的所有 Plugin。
func AllPlugins() []Plugin {
	return defaultRegistry.All()
}
