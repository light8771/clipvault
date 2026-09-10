package clipvault

import (
	"crypto/rand"
	"errors"
	"reflect"
	"sync"
)

var ErrConfigConflict = errors.New("configuration changed before commit")

// ConfigRuntimeApplyError means the configuration is already persisted and
// cached, but one or more runtime listeners could not apply it completely.
// Callers that own post-commit security side effects must still perform them.
type ConfigRuntimeApplyError struct {
	Err error
}

func (e *ConfigRuntimeApplyError) Error() string { return e.Err.Error() }
func (e *ConfigRuntimeApplyError) Unwrap() error { return e.Err }

// ConfigStore 是 ConfigManager 对配置持久化依赖的抽象。
// 定义在此文件（消费方）而非 store.go，遵循依赖倒置：ConfigManager 只依赖
// 它真正需要的 GetConfig/SaveConfig 两个能力，而非具体的 *Store。
// *Store 已实现该接口，因此所有现有调用点（含 23 个测试）无需修改。
type ConfigStore interface {
	GetConfig() (Config, error)
	SaveConfig(Config) error
}

// ConfigManager 在内存中维护当前配置，并在变更时同步通知所有监听器。
// 配置持久化委托给 ConfigStore（生产环境由 *Store 实现）。
type ConfigManager struct {
	mu       sync.RWMutex
	updateMu sync.Mutex
	cfg      Config
	version  string
	store    ConfigStore
	onChange []func(Config) error
}

// NewConfigManager 从数据库加载配置，创建 ConfigManager。
func NewConfigManager(store ConfigStore) (*ConfigManager, error) {
	cfg, err := store.GetConfig()
	if err != nil {
		return nil, err
	}
	return &ConfigManager{cfg: cloneConfig(cfg), version: rand.Text(), store: store}, nil
}

// Get 返回当前配置的快照（并发安全）。
func (cm *ConfigManager) Get() Config {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cloneConfig(cm.cfg)
}

// Update 校验并持久化新配置，然后同步通知所有已注册的监听器。
// 所有监听器都会执行；多个错误聚合返回（存储错误仍会阻止提交与广播）。
func (cm *ConfigManager) Update(cfg Config) error {
	cm.updateMu.Lock()
	defer cm.updateMu.Unlock()
	return cm.commit(cfg)
}

// UpdateFrom commits only if the snapshot used for validation and mutation is
// still current. A conflicting writer must reread instead of restoring stale
// passwords, asset metadata or unrelated settings.
func (cm *ConfigManager) UpdateFrom(expected, cfg Config) error {
	cm.updateMu.Lock()
	defer cm.updateMu.Unlock()
	cm.mu.RLock()
	current := reflect.DeepEqual(cm.cfg, expected)
	cm.mu.RUnlock()
	if !current {
		return ErrConfigConflict
	}
	return cm.commit(cfg)
}

// commit requires updateMu and keeps persistence, cache and listeners ordered.
func (cm *ConfigManager) commit(cfg Config) error {
	cfg = cloneConfig(cfg)
	normalizeConfig(&cfg)
	if err := cm.store.SaveConfig(cfg); err != nil {
		return err
	}
	cm.mu.Lock()
	if !sameEditableConfig(cm.cfg, cfg) {
		cm.version = rand.Text()
	}
	cm.cfg = cloneConfig(cfg)
	listeners := append([]func(Config) error{}, cm.onChange...)
	cm.mu.Unlock()

	var listenerErrors []error
	for _, fn := range listeners {
		if err := fn(cloneConfig(cfg)); err != nil {
			listenerErrors = append(listenerErrors, err)
		}
	}
	if err := errors.Join(listenerErrors...); err != nil {
		return &ConfigRuntimeApplyError{Err: err}
	}
	return nil
}

// Reload 在事务性导入（ImportFullJSON/ImportXLSX）提交后刷新缓存配置，
// 并像 Update 一样同步广播给所有已注册 listener。所有 listener 都会执行，
// 多个错误聚合返回，避免某个运行时组件失败时跳过其后的同步操作。
func (cm *ConfigManager) Reload() error {
	cm.updateMu.Lock()
	defer cm.updateMu.Unlock()
	cfg, err := cm.store.GetConfig()
	if err != nil {
		return err
	}
	cm.mu.Lock()
	if !sameEditableConfig(cm.cfg, cfg) {
		cm.version = rand.Text()
	}
	cm.cfg = cloneConfig(cfg)
	listeners := append([]func(Config) error{}, cm.onChange...)
	cm.mu.Unlock()
	var listenerErrors []error
	for _, fn := range listeners {
		if err := fn(cloneConfig(cfg)); err != nil {
			listenerErrors = append(listenerErrors, err)
		}
	}
	if err := errors.Join(listenerErrors...); err != nil {
		return &ConfigRuntimeApplyError{Err: err}
	}
	return nil
}

// OnChange 注册一个配置变更监听器。监听器在 Update 与 Reload 成功提交配置后
// 都会被同步调用（注意：不只是 Update——另见 Reload 的副作用说明）。
//
// 所有监听器都会执行；多个失败通过 errors.Join 聚合返回，避免配置已提交后
// 因首个失败而跳过其他运行时同步，造成静默状态分叉。
func (cm *ConfigManager) OnChange(fn func(Config) error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.onChange = append(cm.onChange, fn)
}

// GetVersioned returns one atomic snapshot. Asset metadata is owned by separate
// endpoints and cannot be overwritten by a settings PUT, so it does not advance
// the editable configuration version.
func (cm *ConfigManager) GetVersioned() (Config, string) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cloneConfig(cm.cfg), cm.version
}

func sameEditableConfig(a, b Config) bool {
	preserveAssetMetadata(&a, Config{})
	preserveAssetMetadata(&b, Config{})
	return reflect.DeepEqual(a, b)
}
