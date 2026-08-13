package clipvault

import (
	"fmt"
	"strings"
	"time"
	"unsafe"
)

// clipWorkerDrainTimeout 是退出时等待剪贴板 worker 收敛的上限。
// 正常情况下 worker 会立即返回；此处设上限是为了避免一次卡住的
// store.Save 把整个进程退出流程无限期阻塞住。
const clipWorkerDrainTimeout = 5 * time.Second

// startClipWorker 启动唯一的剪贴板处理 goroutine。
// 旧实现对每个 WM_CLIPBOARDUPDATE 都 `go` 一个新 goroutine，剪贴板被高频写入时
// 会无上限堆积且处理顺序不确定；改为单 worker + 合并通道后并发度恒定为 1，
// 处理顺序与事件顺序一致。
func (r *WindowsRuntime) startClipWorker() {
	r.clipCh = make(chan clipEvent, 64)
	r.clipDone = make(chan struct{})
	ch := r.clipCh
	go func() {
		defer close(r.clipDone)
		for ev := range ch {
			r.recorder.ProcessClipboardUpdate(ev.source, ev.sequence)
		}
	}()
}

// notifyClipboardUpdate 以非阻塞方式投递一次「剪贴板已变化」信号。
// 队列满时淘汰最旧事件并尽量保留最新事件；该函数只在消息循环线程调用，
// 不会阻塞消息泵。
func (r *WindowsRuntime) notifyClipboardUpdate(source SourceInfo) {
	if r.clipCh == nil {
		return
	}
	ev := clipEvent{source: source, sequence: clipboardSequenceNumber()}
	if ev.sequence == 0 {

		ev.source = SourceInfo{}
	}
	select {
	case r.clipCh <- ev:
	default:

		select {
		case dropped := <-r.clipCh:
			r.logger.Warn("clipboard_queue_coalesced", map[string]any{
				"droppedSequence": dropped.sequence,
				"latestSequence":  ev.sequence,
			})
		default:
		}
		select {
		case r.clipCh <- ev:
		default:
			r.logger.Warn("clipboard_queue_drop", map[string]any{"sequence": ev.sequence})
		}
	}
}

// stopClipWorker 请求 worker 退出并等待其真正结束，避免 goroutine 泄漏。
// 可重复调用（Once 保护 close），未启动时为空操作。
func (r *WindowsRuntime) stopClipWorker() {
	if r.clipCh == nil {
		return
	}
	r.clipCloseOnce.Do(func() {
		ch := r.clipCh
		r.clipCh = nil
		close(ch)
	})
	select {
	case <-r.clipDone:
	case <-time.After(clipWorkerDrainTimeout):
		r.logger.Warn("clip_worker_drain_timeout", map[string]any{
			"timeoutSeconds": clipWorkerDrainTimeout.Seconds(),
		})
	}
}

func (r *WindowsRuntime) cleanup() {
	r.unregisterHotkey()
	r.unregisterSearchHotkey()
	procRemoveClipboardFormatListener.Call(r.hwnd)
	nid := r.notifyIconData()
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	if r.hIcon != 0 {
		procDestroyIcon.Call(r.hIcon)
		r.hIcon = 0
	}
}

func (r *WindowsRuntime) doRegisterHotkey(id int, hk HotkeyConfig) error {
	if err := validateHotkeyConfig("runtime hotkey", hk); err != nil {
		return &HotkeyError{Msg: "快捷键配置无效: " + err.Error()}
	}
	if !hk.Enabled {
		r.logger.Info("hotkey_disabled", map[string]any{"id": id})
		return nil
	}
	ok, _, err := procRegisterHotKey.Call(r.hwnd, uintptr(id), uintptr(hk.Modifiers), uintptr(hk.Key))
	if ok == 0 {
		r.logger.Warn("hotkey_register_failed", map[string]any{"id": id, "error": err.Error()})
		return &HotkeyError{Msg: fmt.Sprintf("快捷键注册失败（可能与其他应用冲突）: %v", err)}
	}
	r.logger.Info("hotkey_registered", map[string]any{"id": id, "modifiers": hk.Modifiers, "key": hk.Key})
	return nil
}

func (r *WindowsRuntime) registerHotkey() {
	_ = r.doRegisterHotkey(idHotkeyToggle, r.cfgMgr.Get().Hotkey)
}

func (r *WindowsRuntime) unregisterHotkey() {
	procUnregisterHotKey.Call(r.hwnd, idHotkeyToggle)
}

func (r *WindowsRuntime) registerSearchHotkey() {
	_ = r.doRegisterHotkey(idHotkeySearch, r.cfgMgr.Get().SearchHotkey)
}

func (r *WindowsRuntime) unregisterSearchHotkey() {
	procUnregisterHotKey.Call(r.hwnd, idHotkeySearch)
}

// openQuickSearch 响应搜索快捷键，弹出桌面搜索窗口。
func (r *WindowsRuntime) openQuickSearch() {
	if r.store == nil || strings.TrimSpace(r.SearchURL) == "" {
		r.logger.Warn("quick_search_skipped", map[string]any{"reason": "store_or_url_missing"})
		return
	}
	ShowSearchWindow(r.store, r.SearchURL, r.logger, r.license)
}

// ReregisterHotkey 从任意线程调用，通过 PostMessage 将重注册请求
// 转发到消息循环线程执行，确保 RegisterHotKey 在正确的线程上调用。
// 返回 nil 表示成功，返回 *HotkeyError 表示注册失败。
func (r *WindowsRuntime) ReregisterHotkey(cfg Config) error {
	r.hotkeyMu.Lock()
	r.pendingHotkey = &cfg.Hotkey
	r.pendingSearchHotkey = &cfg.SearchHotkey
	r.pendingHotkeyResult = make(chan error, 1)
	r.hotkeyMu.Unlock()
	procPostMessageW.Call(r.hwnd, wmReregisterHotkey, 0, 0)
	select {
	case err := <-r.pendingHotkeyResult:
		return err
	case <-time.After(3 * time.Second):
		return &HotkeyError{Msg: "快捷键注册超时"}
	}
}
