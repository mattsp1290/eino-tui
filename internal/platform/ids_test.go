package platform

import (
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestIDs(t *testing.T) {
	ids := IDs{}
	values := map[string]string{
		"run-": string(ids.NewRunID()), "message-": string(ids.NewMessageID()),
		"part-": string(ids.NewPartID()), "tool-call-": string(ids.NewToolCallID()),
		"event-": string(ids.NewEventID()), "epoch-": string(ids.NewEpochID()),
	}
	for prefix, value := range values {
		if !strings.HasPrefix(value, prefix) {
			t.Fatalf("%q missing %q", value, prefix)
		}
		if _, err := uuid.Parse(strings.TrimPrefix(value, prefix)); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
	}
}

func TestIDsUniqueUnderConcurrency(t *testing.T) {
	const count = 256
	values := make(chan string, count)
	var group sync.WaitGroup
	for range count {
		group.Add(1)
		go func() { defer group.Done(); values <- string((IDs{}).NewEventID()) }()
	}
	group.Wait()
	close(values)
	seen := map[string]bool{}
	for value := range values {
		if seen[value] {
			t.Fatalf("duplicate %q", value)
		}
		seen[value] = true
	}
	if len(seen) != count {
		t.Fatalf("unique=%d", len(seen))
	}
}
