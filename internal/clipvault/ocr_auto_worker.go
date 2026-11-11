package clipvault

import (
	"context"
	"strings"
	"sync"
	"time"
)

type autoOCRJob struct {
	id    int64
	epoch uint64
}

// 只接受采集器提交后的事件；不扫描已有历史，不在采集线程执行网络请求。
type autoOCRWorker struct {
	server      *SearchServer
	mu          sync.Mutex
	epoch       uint64
	enabled     bool
	fingerprint string
	ctx         context.Context
	cancel      context.CancelFunc
	root        context.Context
	shutdown    context.CancelFunc
	queue       chan autoOCRJob
	done        chan struct{}
}

func newAutoOCRWorker(s *SearchServer) *autoOCRWorker {
	root, cancel := context.WithCancel(context.Background())
	w := &autoOCRWorker{server: s, root: root, shutdown: cancel, queue: make(chan autoOCRJob, 32), done: make(chan struct{})}
	w.refresh()
	go w.run()
	return w
}

func (w *autoOCRWorker) refresh() {
	ctx, cancel := context.WithTimeout(w.root, 3*time.Second)
	defer cancel()
	policy, _, err := w.server.store.readOCRAuto(ctx)
	p, profileErr := w.server.store.defaultOCR(ctx)
	enabled := err == nil && profileErr == nil && policy.Enabled && !policy.Suspended && policy.Fingerprint == p.fingerprint() && validateAutoProfile(p) == nil
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		w.cancel()
	}
	w.epoch++
	w.enabled, w.fingerprint = enabled, policy.Fingerprint
	w.ctx, w.cancel = context.WithCancel(w.root)
}

func (w *autoOCRWorker) offer(id int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.enabled || w.root.Err() != nil {
		return
	}
	select {
	case w.queue <- autoOCRJob{id, w.epoch}:
	default:
	}
}

func (w *autoOCRWorker) stop() { w.shutdown(); <-w.done }

func (w *autoOCRWorker) run() {
	defer close(w.done)
	for {
		select {
		case <-w.root.Done():
			return
		case job := <-w.queue:
			w.mu.Lock()
			ctx, fingerprint := w.ctx, w.fingerprint
			valid := w.enabled && job.epoch == w.epoch
			w.mu.Unlock()
			if valid && ctx.Err() == nil {
				w.process(ctx, job.id, fingerprint)
			}
		}
	}
}

func (w *autoOCRWorker) process(ctx context.Context, id int64, fingerprint string) {
	s := w.server
	if !s.beginOCRTask(id) {
		return
	}
	var taskErr error
	defer func() { s.endOCRTask(id, taskErr) }()
	item, err := s.store.GetByID(ctx, id)
	if err != nil || item.Sensitive {
		return
	}
	images, err := itemOCRImages(item, s.env)
	if err != nil {
		taskErr = err
		return
	}
	count := 0
	for _, img := range images {
		if strings.HasPrefix(img.ID, "file:") {
			continue
		}
		if count >= 8 || ctx.Err() != nil {
			return
		}
		count++
		// 队列等待期间可能关闭设置、修改默认连接、删除记录或标记敏感。
		policy, _, err := s.store.readOCRAuto(ctx)
		if err != nil || !policy.Enabled || policy.Suspended || policy.Fingerprint != fingerprint {
			return
		}
		p, err := s.store.defaultOCR(ctx)
		if err != nil || p.fingerprint() != fingerprint {
			return
		}
		current, err := s.store.GetByID(ctx, id)
		if err != nil || current.Sensitive {
			return
		}
		_, _, err = s.recognizeImage(ctx, id, img, p, false)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			taskErr = err
			return
		}
	}
}
