package clipvault

import "sync"

// ConfigManager 在内存中维护当前配置，并在变更时同步通知所有监听器。
// 配置持久化由 Store.SaveConfig 负责。
type ConfigManager struct {
	mu       sync.RWMutex
	cfg      Config
	store    *Store
	onChange []func(Config) error
}

// NewConfigManager 从数据库加载配置，创建 ConfigManager。
func NewConfigManager(store *Store) (*ConfigManager, error) {
	cfg, err := store.GetConfig()
	if err != nil {
		return nil, err
	}
	return &ConfigManager{cfg: cfg, store: store}, nil
}

// Get 返回当前配置的快照（并发安全）。
func (cm *ConfigManager) Get() Config {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.cfg
}

// Update 校验并持久化新配置，然后同步通知所有已注册的监听器。
// 返回监听器的第一个非 nil 错误（存储错误优先）。
func (cm *ConfigManager) Update(cfg Config) error {
	normalizeConfig(&cfg)
	if err := cm.store.SaveConfig(cfg); err != nil {
		return err
	}
	cm.mu.Lock()
	cm.cfg = cfg
	listeners := append([]func(Config) error{}, cm.onChange...)
	cm.mu.Unlock()

	for _, fn := range listeners {
		if err := fn(cfg); err != nil {
			return err
		}
	}
	return nil
}

// OnChange 注册一个配置变更监听器，在 Update 成功后被同步调用。
func (cm *ConfigManager) OnChange(fn func(Config) error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.onChange = append(cm.onChange, fn)
}
