package clipvault

import (
	"context"
	"sync"
	"time"
)

const (
	tokenLastUsedBufferSize    = 256
	tokenLastUsedFlushInterval = 250 * time.Millisecond
	tokenLastUsedWriteTimeout  = 2 * time.Second
)

type tokenLastUsedUpdate struct {
	id   int64
	tsMS int64
}

type tokenLastUsedTouchFunc func(context.Context, int64, int64) error

// tokenLastUsedWorker 把请求热路径上的最后使用时间更新收敛为一个有界后台任务。
// 同一 Token 在一次刷新周期内只写入最新时间；队列或待写集合满时允许丢弃，后续
// 请求会再次提交。Close 通过取消上下文终止数据库操作，不会拖住服务退出。
type tokenLastUsedWorker struct {
	ctx       context.Context
	cancel    context.CancelFunc
	touch     tokenLastUsedTouchFunc
	updates   chan tokenLastUsedUpdate
	done      chan struct{}
	closeOnce sync.Once
}

func newTokenLastUsedWorker(touch tokenLastUsedTouchFunc) *tokenLastUsedWorker {
	ctx, cancel := context.WithCancel(context.Background())
	w := &tokenLastUsedWorker{
		ctx:     ctx,
		cancel:  cancel,
		touch:   touch,
		updates: make(chan tokenLastUsedUpdate, tokenLastUsedBufferSize),
		done:    make(chan struct{}),
	}
	go w.run()
	return w
}

func (w *tokenLastUsedWorker) Submit(id, tsMS int64) {
	if w == nil || id <= 0 || tsMS <= 0 {
		return
	}
	update := tokenLastUsedUpdate{id: id, tsMS: tsMS}
	select {
	case <-w.ctx.Done():
		return
	default:
	}
	select {
	case <-w.ctx.Done():
	case w.updates <- update:
	default:
		// 最后使用时间是提示性元数据。过载时保持请求路径非阻塞，后续请求会重试。
	}
}

func (w *tokenLastUsedWorker) Close() {
	if w == nil {
		return
	}
	w.closeOnce.Do(func() {
		w.cancel()
		<-w.done
	})
}

func (w *tokenLastUsedWorker) run() {
	defer close(w.done)
	ticker := time.NewTicker(tokenLastUsedFlushInterval)
	defer ticker.Stop()
	pending := make(map[int64]int64, tokenLastUsedBufferSize)
	flush := func() bool {
		for id, tsMS := range pending {
			ctx, cancel := context.WithTimeout(w.ctx, tokenLastUsedWriteTimeout)
			_ = w.touch(ctx, id, tsMS)
			cancel()
			delete(pending, id)
			if w.ctx.Err() != nil {
				return false
			}
		}
		return true
	}
	for {
		select {
		case <-w.ctx.Done():
			return
		case update := <-w.updates:
			if current, ok := pending[update.id]; ok {
				if update.tsMS > current {
					pending[update.id] = update.tsMS
				}
			} else if len(pending) < tokenLastUsedBufferSize {
				pending[update.id] = update.tsMS
			}
		case <-ticker.C:
			if !flush() {
				return
			}
		}
	}
}
