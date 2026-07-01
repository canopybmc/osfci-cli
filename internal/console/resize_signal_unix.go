//go:build !windows

package console

import (
	"os"
	"os/signal"
	"syscall"
)

func notifyResizeSignal(ch chan<- os.Signal) {
	signal.Notify(ch, syscall.SIGWINCH)
}

func isResizeSignal(sig os.Signal) bool {
	return sig == syscall.SIGWINCH
}
