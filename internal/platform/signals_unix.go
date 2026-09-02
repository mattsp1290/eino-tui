//go:build darwin || linux

package platform

import (
	"os"
	"syscall"
)

func Signals() []os.Signal { return []os.Signal{os.Interrupt, syscall.SIGTERM} }
