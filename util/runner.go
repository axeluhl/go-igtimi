package util

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"golang.org/x/exp/slog"
)

func GracefulShutdown(ctx context.Context, handleShutdown func(), timeout time.Duration) {
	// Listen for signals
	s := make(chan os.Signal, 1)
	signal.Notify(s,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM)

	// Do graceful shutdown on signal
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case sig := <-s:
			if sig != syscall.SIGHUP {
				break loop
			}
		}
	}
	done := make(chan struct{})
	go func() {
		handleShutdown()
		close(done)
	}()

	select {
	case <-done:
		slog.Info("graceful shutdown complete")
	case <-time.After(timeout):
		slog.Warn("graceful shutdown timed out")
		stacktrace := make([]byte, 8192)
		length := runtime.Stack(stacktrace, true)
		fmt.Println(string(stacktrace[:length]))
	}
}
