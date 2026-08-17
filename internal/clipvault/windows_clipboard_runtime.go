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

// clipQueue 是无界 FIFO 事件队列（事件优先：绝不丢弃任何剪贴板事件）。
// 生产端只在消息循环线程调用 push；消费端是唯一的 worker goroutine。
// 事件在内存中排队等 worker 收敛，不用「满 → 丢最旧」换背压。
type clipQueue struct {
	mu        sync.Mutex
	events    []clipEvent
	closed    bool
	signal    chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func newClipQueue() *clipQueue {
	return &clipQueue{signal: make(chan struct{}, 1), done: make(chan struct{})}
}

func (q *clipQueue) push(ev clipEvent) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.events = append(q.events, ev)
	q.mu.Unlock()
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

func (q *clipQueue) pop() (clipEvent, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.events) == 0 {
		return clipEvent{}, false
	}
	ev := q.events[0]
	q.events[0] = clipEvent{}
	q.events = q.events[1:]
	return ev, true
}

func (q *clipQueue) isClosed() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.closed
}

// close 停止接收新事件；队列中已排队的事件仍会被 worker 消费完。
func (q *clipQueue) close() {
	q.closeOnce.Do(func() {
		q.mu.Lock()
		q.closed = true
		q.mu.Unlock()
		close(q.done)
		select {
		case q.signal <- struct{}{}:
		default:
		}
	})
}

// startClipWorker 启动唯一的剪贴板处理 goroutine。
// 并发度恒定为 1，处理顺序与事件顺序一致；队列为无界 FIFO，事件不会丢失。
func (r *WindowsRuntime) startClipWorker() {
	r.clipQueue = newClipQueue()
	r.clipDone = make(chan struct{})
	q := r.clipQueue
	go func() {
		defer close(r.clipDone)
		for {
			select {
			case <-q.signal:
			case <-q.done:
			}
			for {
				ev, ok := q.pop()
				if !ok {
					if q.isClosed() {
						return
					}
					break
				}
				r.recorder.ProcessClipboardUpdate(ev.source, ev.sequence, ev.capturedAtMS)
			}
		}
	}()
}

// notifyClipboardUpdate 投递一次「剪贴板已变化」事件。
// 事件时间在派发现场采集（capturedAtMS），sequence 与来源同样现场绑定；
// 被内部恢复抑制水位覆盖的事件（搜索弹窗写回剪贴板）直接跳过。
// 该函数只在消息循环线程调用，不会阻塞消息泵。
func (r *WindowsRuntime) notifyClipboardUpdate(source SourceInfo) {
	q := r.clipQueue
	if q == nil {
		return
	}
	ev := clipEvent{
		source:       source,
		sequence:     clipboardSequenceNumber(),
		capturedAtMS: time.Now().UnixMilli(),
	}
	if ev.sequence != 0 && r.isSequenceSuppressed(ev.sequence) {
		return
	}
	if ev.sequence == 0 {
		ev.source = SourceInfo{}
	}
	q.push(ev)
}

// stopClipWorker 请求 worker 退出并等待其真正结束，避免 goroutine 泄漏。
// 关闭后队列中残留的事件仍会被消费完。可重复调用，未启动时为空操作。
func (r *WindowsRuntime) stopClipWorker() {
	q := r.clipQueue
	if q == nil {
		return
	}
	r.clipCloseOnce.Do(func() {
		r.clipQueue = nil
		q.close()
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
	ShowSearchWindow(r.store, r.SearchURL, r.logger, r.license, r.cfgMgr, r.env)
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
