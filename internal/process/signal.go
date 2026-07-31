package process

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
)

type signalContextKey struct{}

// NotifyContext cancels parent on SIGINT or SIGTERM and records which signal arrived.
func NotifyContext(parent context.Context) (context.Context, func()) {
	state := &atomic.Int32{}
	ctx, cancel := context.WithCancel(context.WithValue(parent, signalContextKey{}, state))
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})

	go func() {
		defer close(done)
		select {
		case received := <-signals:
			signal.Stop(signals)
			if value, ok := received.(syscall.Signal); ok {
				state.Store(int32(value))
			}
			cancel()
		case <-ctx.Done():
		}
	}()

	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			signal.Stop(signals)
			cancel()
			<-done
		})
	}
}

func cancellationSignal(ctx context.Context) syscall.Signal {
	state, _ := ctx.Value(signalContextKey{}).(*atomic.Int32)
	if state == nil || state.Load() == 0 {
		return syscall.SIGTERM
	}
	return syscall.Signal(state.Load())
}
