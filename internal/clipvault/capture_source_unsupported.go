//go:build !windows

package clipvault

// 非 Windows 平台没有剪贴板监听实现，来源捕获同样不可用。
// SourceInfo 类型定义在 recorder.go（平台中立），这里只补空实现。

func captureForegroundSourceFast() SourceInfo { return SourceInfo{} }

func captureClipboardOwnerSourceFast() SourceInfo { return SourceInfo{} }

func (s *SourceInfo) resolveDetails() {}
