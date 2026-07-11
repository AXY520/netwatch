package probe

import (
	"context"
	"sync"
)

// taskRuntime isolates long-running user tasks (broadband speedtest + traceroute)
// so Service lifecycle code does not interleave with observation state.
type taskRuntime struct {
	broadbandMu     sync.Mutex
	broadbandTask   BroadbandTaskStatus
	broadbandCancel context.CancelFunc

	traceMu     sync.Mutex
	traceTask   TraceResult
	traceCancel context.CancelFunc
}

func newTaskRuntime() *taskRuntime {
	return &taskRuntime{}
}

func (t *taskRuntime) cancelAll() {
	t.broadbandMu.Lock()
	cancelBB := t.broadbandCancel
	t.broadbandMu.Unlock()
	if cancelBB != nil {
		cancelBB()
	}
	t.traceMu.Lock()
	cancelTrace := t.traceCancel
	t.traceMu.Unlock()
	if cancelTrace != nil {
		cancelTrace()
	}
}
