package clipvault

import (
	"fmt"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// clipWorkerDrainTimeout 是退出时等待剪贴板 worker 收敛的上限。
// 正常情况下 worker 会立即返回；此处设上限是为了避免一次卡住的
// store.Save 把整个进程退出流程无限期阻塞住。
const clipWorkerDrainTimeout = 5 * time.Second

// clipWorkerSettleDelay 是一次逻辑复制发布多个格式时的短暂收敛窗口。
const clipWorkerSettleDelay = 25 * time.Millisecond

// clipMailbox 只保留尚未读取的最新剪贴板状态。系统剪贴板是可变的全局
// 状态；新 sequence 出现后，旧通知对应的内容已经无法恢复。
type clipMailbox struct {
	mu        sync.Mutex
	pending   clipEvent
	hasValue  bool
	closed    bool
	signal    chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func newClipMailbox() *clipMailbox {
	return &clipMailbox{signal: make(chan struct{}, 1), done: make(chan struct{})}
}

// put 覆盖尚未消费的旧通知，并唤醒唯一 worker。
func (m *clipMailbox) put(ev clipEvent) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.pending = ev
	m.hasValue = true
	m.mu.Unlock()
	select {
	case m.signal <- struct{}{}:
	default:
	}
}

func (m *clipMailbox) take() (clipEvent, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.hasValue {
		return clipEvent{}, false
	}
	ev := m.pending
	m.pending = clipEvent{}
	m.hasValue = false
	return ev, true
}

func (m *clipMailbox) isClosedAndEmpty() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed && !m.hasValue
}

// close 停止接收新事件；当前最新状态仍会被 worker 消费完。
func (m *clipMailbox) close() {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.mu.Unlock()
		close(m.done)
		select {
		case m.signal <- struct{}{}:
		default:
		}
	})
}

func (m *clipMailbox) waitUntilSettled() {
	timer := time.NewTimer(clipWorkerSettleDelay)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return
		case <-m.signal:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(clipWorkerSettleDelay)
		case <-m.done:
			return
		}
	}
}

// startClipWorker 启动唯一的剪贴板处理 goroutine。连续通知先收敛到最新
// 状态，避免把早已不可读的中间 sequence 落成伪故障记录。
func (r *WindowsRuntime) startClipWorker() {
	r.clipMailbox = newClipMailbox()
	r.clipDone = make(chan struct{})
	mailbox := r.clipMailbox
	go func() {
		defer close(r.clipDone)
		for {
			select {
			case <-mailbox.signal:
			case <-mailbox.done:
			}
			mailbox.waitUntilSettled()
			if ev, ok := mailbox.take(); ok {
				r.recorder.ProcessClipboardUpdate(ev.source, ev.sequence, ev.capturedAtMS)
			}
			if mailbox.isClosedAndEmpty() {
				return
			}
		}
	}()
}

// notifyClipboardUpdate 投递一次「剪贴板已变化」事件。
// 事件时间在派发现场采集（capturedAtMS），sequence 与来源同样现场绑定；
// 被内部恢复抑制水位覆盖的事件（搜索弹窗写回剪贴板）直接跳过。
// 该函数只在消息循环线程调用，不会阻塞消息泵。
func (r *WindowsRuntime) notifyClipboardUpdate(source SourceInfo) {
	r.notifyClipboardSequence(source, clipboardSequenceNumber(), time.Now().UnixMilli())
}

// notifyClipboardSequence merges message notifications and polling detections.
// A non-zero sequence is claimed once, before suppression is checked, so an
// internally restored clipboard state is not rediscovered by the poll timer.
func (r *WindowsRuntime) notifyClipboardSequence(source SourceInfo, sequence uint32, capturedAtMS int64) {
	mailbox := r.clipMailbox
	if mailbox == nil {
		return
	}
	if !r.claimClipboardSequence(sequence) {
		return
	}
	ev := clipEvent{
		source:       source,
		sequence:     sequence,
		capturedAtMS: capturedAtMS,
	}
	if ev.sequence != 0 && r.isSequenceSuppressed(ev.sequence) {
		return
	}
	if ev.sequence == 0 {
		ev.source = SourceInfo{}
	}
	mailbox.put(ev)
}

func (r *WindowsRuntime) claimClipboardSequence(sequence uint32) bool {
	if sequence == 0 {
		return true
	}
	if r.lastClipboardSequence == sequence {
		return false
	}
	r.lastClipboardSequence = sequence
	return true
}

// pollClipboardUpdate detects producers that mutate the clipboard sequence but
// do not deliver WM_CLIPBOARDUPDATE. The clipboard owner is a better attribution
// source than the foreground window at this delayed observation point.
func (r *WindowsRuntime) pollClipboardUpdate() {
	sequence := clipboardSequenceNumber()
	if sequence == 0 || sequence == r.lastClipboardSequence {
		return
	}
	var source SourceInfo
	if r.cfgMgr.Get().RecordSourceInfo {
		source = captureClipboardOwnerSourceFast()
	}
	r.notifyClipboardSequence(source, sequence, time.Now().UnixMilli())
}

// stopClipWorker 请求 worker 退出并等待其真正结束，避免 goroutine 泄漏。
// 关闭后队列中残留的事件仍会被消费完。可重复调用，未启动时为空操作。
func (r *WindowsRuntime) stopClipWorker() {
	mailbox := r.clipMailbox
	if mailbox == nil {
		return
	}
	r.clipCloseOnce.Do(func() {
		r.clipMailbox = nil
		mailbox.close()
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
	procClipboardKillTimer.Call(r.hwnd, idClipboardPoll)
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
		return &HotkeyError{Msg: "invalid hotkey config: " + err.Error()}
	}
	if !hk.Enabled {
		r.logger.Info("hotkey_disabled", map[string]any{"id": id})
		return nil
	}
	ok, _, err := procRegisterHotKey.Call(r.hwnd, uintptr(id), uintptr(hk.Modifiers), uintptr(hk.Key))
	if ok == 0 {
		r.logger.Warn("hotkey_register_failed", map[string]any{"id": id, "error": err.Error()})
		return &HotkeyError{Msg: fmt.Sprintf("hotkey registration failed (possibly conflicting with another app): %v", err)}
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
	if r.store == nil || strings.TrimSpace(r.SearchURL()) == "" {
		r.logger.Warn("quick_search_skipped", map[string]any{"reason": "store_or_url_missing"})
		return
	}
	ShowSearchWindow(r.store, r.SearchURL(), r.logger, r.license, r.cfgMgr, r.env)
}

// ReregisterHotkey 从任意线程调用，通过 PostMessage 将重注册请求
// 转发到消息循环线程执行，确保 RegisterHotKey 在正确的线程上调用。
// 调用方已处于消息循环线程时（如托盘菜单触发的配置更新）直接内联
// 执行，避免向自己的消息队列发消息后阻塞等待造成消息泵冻结。
// 返回 nil 表示成功，返回 *HotkeyError 表示注册失败。
func (r *WindowsRuntime) ReregisterHotkey(cfg Config) error {
	if r.hwnd != 0 && r.onMessageThread() {
		return r.applyHotkeyUpdate(&cfg.Hotkey, &cfg.SearchHotkey)
	}
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
		return &HotkeyError{Msg: "hotkey registration timed out"}
	}
}

// onMessageThread 报告当前线程是否为创建消息窗口的线程（即消息循环线程）。
func (r *WindowsRuntime) onMessageThread() bool {
	ownerTid, _, _ := procGetWindowThreadProcessId.Call(r.hwnd, 0)
	if ownerTid == 0 {
		return false
	}
	currentTid, _, _ := procGetCurrentThreadId.Call()
	return ownerTid == currentTid
}

// applyHotkeyUpdate 在消息循环线程上执行快捷键重注册。
func (r *WindowsRuntime) applyHotkeyUpdate(hk, shk *HotkeyConfig) error {
	r.unregisterHotkey()
	r.unregisterSearchHotkey()
	var err error
	if hk != nil {
		err = r.doRegisterHotkey(idHotkeyToggle, *hk)
	}
	if shk != nil && err == nil {
		err = r.doRegisterHotkey(idHotkeySearch, *shk)
	}
	return err
}
