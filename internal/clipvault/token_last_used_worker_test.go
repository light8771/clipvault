package clipvault

import (
	"context"
	"testing"
	"time"
)

func TestTokenLastUsedWorkerWritesAndStops(t *testing.T) {
	type call struct {
		id   int64
		tsMS int64
	}
	calls := make(chan call, 2)
	worker := newTokenLastUsedWorker(func(_ context.Context, id, tsMS int64) error {
		calls <- call{id: id, tsMS: tsMS}
		return nil
	})
	worker.Submit(7, 1234)
	select {
	case got := <-calls:
		if got != (call{id: 7, tsMS: 1234}) {
			t.Fatalf("touch = %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for last-used update")
	}

	worker.Close()
	worker.Close()
	worker.Submit(8, 5678)
	select {
	case got := <-calls:
		t.Fatalf("touch after close = %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}
