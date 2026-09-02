//go:build darwin || linux

package platform

import (
	"os"
	"syscall"
	"testing"
)

func TestSignalsHaveOneSupportedOwnerList(t *testing.T) {
	signals := Signals()
	if len(signals) != 2 || signals[0] != os.Interrupt || signals[1] != syscall.SIGTERM {
		t.Fatalf("signals = %#v", signals)
	}
}
