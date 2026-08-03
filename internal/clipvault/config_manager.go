package clipvault

import "sync"

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
	cfg      Config
	store    ConfigStore
	onChange []func(Config) error
}

// NewConfigManager 从数据库加载配置，创建 ConfigManager。
func NewConfigManager(store ConfigStore) (*ConfigManager, error) {
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

	// 首个 listener 失败即中断（配置已落库但状态可能静默分叉）。
	// 不变量见 OnChange doc；新增会失败的 listener 须注册末位或先落地 #28(B)。
	for _, fn := range listeners {
		if err := fn(cfg); err != nil {
			return err
		}
	}
	return nil
}

// Reload 在事务性导入（ImportFullJSON/ImportXLSX）提交后刷新缓存配置，
// 并像 Update 一样同步广播给所有已注册 listener（含其失败即中断的语义，
// 见 OnChange doc；配置已读回但被中断 listener 的次生状态会静默分叉）。
// 副作用清单：所有 listener 被同步调用；任一失败则中断并返回该错误。
func (cm *ConfigManager) Reload() error {
	cfg, err := cm.store.GetConfig()
	if err != nil {
		return err
	}
	cm.mu.Lock()
	cm.cfg = cfg
	listeners := append([]func(Config) error{}, cm.onChange...)
	cm.mu.Unlock()
	// 与 Update 相同的广播语义与中断不变量（见 OnChange doc）。
	for _, fn := range listeners {
		if err := fn(cfg); err != nil {
			return err
		}
	}
	return nil
}

// OnChange 注册一个配置变更监听器。监听器在 Update 与 Reload 成功提交配置后
// 都会被同步调用（注意：不只是 Update——另见 Reload 的副作用说明）。
//
// 监听器广播的不变量（新增或修改 listener 前必读，关系到配置已提交后状态是否一致）：
//   - 今天的语义：Update/Reload 在持久化（SaveConfig）或读回（GetConfig）成功后，
//     才依次同步调用各 listener；任一 listener 返回非 nil 错误时立即 return err 中断，
//     后续 listener 不再执行。此时配置本身已落库/已读回，但被中断那些 listener 所维护的
//     次生状态（如热键、运行时句柄）会与已提交配置静默分叉。
//   - 当前的巧合依赖（不是设计保证）：今天恰好只有一个可能失败的 listener（热键），
//     且它注册在末位，因此位置不变量 P（唯一可失败的 listener 在末位）成立；
//     由 P 可推出计数不变量 C（至多一个可失败的 listener）。这是巧合，依赖它在
//     未来不成立时会引爆。
//   - 对新 listener 的约束：新增任何可能返回非 nil 错误的 listener 时，必须满足其一：
//     (1) 注册在末位（保持 P）；或 (2) 先落地 #28(B) 的 errors.Join，让多 listener
//     失败能被聚合而非互相吞掉。
func (cm *ConfigManager) OnChange(fn func(Config) error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.onChange = append(cm.onChange, fn)
}
