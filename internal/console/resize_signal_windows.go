//go:build windows

package console

import "os"

func notifyResizeSignal(ch chan<- os.Signal) {
	_ = ch
}

func isResizeSignal(sig os.Signal) bool {
	_ = sig
	return false
}
