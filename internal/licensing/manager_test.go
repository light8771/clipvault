package licensing

import (
	"path/filepath"
	"testing"
)

// TestUnloadedManagerDeniesPaidFeatures 锁定 NewManager 的 fail-closed 默认值。
//
// CanUsePaidFeatures() 的判定是 `Valid || !Enforced`。如果 NewManager 不初始化
// context 字段，零值 Context{Valid:false, Enforced:false} 会让该表达式退化为
// `false || !false` == true —— 也就是说"构造完 Manager 但忘记调用 Load()"会静默
// 放行全部付费功能。本用例断言这个缺口已被堵上。
//
// 若本用例失败，说明 NewManager 的 context 初始化被删掉或改坏了，付费墙在
// "已构造、未 Load"窗口内失效。这不是环境问题，不要跳过。
func TestUnloadedManagerDeniesPaidFeatures(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "absent.lic"), nil)
	if m.CanUsePaidFeatures() {
		t.Fatal("未调用 Load() 的 Manager 必须拒绝付费功能（fail-closed），否则构造后忘记 Load 会静默放行全量")
	}
}

// TestUnloadedManagerContextMatchesEnforcement 固定"构造后未 Load"这一刻的可观测
// 状态，防止后人在修 fail-open 时顺手把 Status 也填上。
//
//   - Enforced 必须与 EnforcementEnabled() 同源：Load() 的每条返回路径都写
//     Enforced: EnforcementEnabled()，构造期默认值若不同源，两者就会在未来某个
//     构建变体下分叉，fail-open 缺口重新打开。
//   - Status 必须保持零值 ""：/api/license/status 会把 Status 原样透出，构造期
//     擅自填 StatusUnconfigured 会改变未 Load 时的对外可观测行为，超出"只堵
//     fail-open"的范围。
func TestUnloadedManagerContextMatchesEnforcement(t *testing.T) {
	ctx := NewManager(filepath.Join(t.TempDir(), "absent.lic"), nil).Context()
	if ctx.Enforced != EnforcementEnabled() {
		t.Fatalf("未 Load 的 Context.Enforced = %v, 应与 EnforcementEnabled() = %v 同源", ctx.Enforced, EnforcementEnabled())
	}
	if ctx.Valid {
		t.Fatal("未 Load 的 Context.Valid 必须为 false：此时尚未校验任何凭据")
	}
	if ctx.Status != "" {
		t.Fatalf("未 Load 的 Context.Status 应保持零值以免改变对外可观测行为, got %q", ctx.Status)
	}
}
