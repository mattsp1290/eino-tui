package runtimeui

import (
	"encoding/json"
	"fmt"
	"testing"

	agentruntime "github.com/mattsp1290/eino-agent/runtime"
	"github.com/mattsp1290/eino-agent/session"
)

func TestEventAccumulatorSanitizesAcrossChunks(t *testing.T) {
	var accumulator eventAccumulator
	events := []session.EventRecord{
		{Kind: agentruntime.EventMessageDelta, SessionID: "s", RunID: "r", Payload: []byte(`{"content":"safe\u001b]0;secret"}`)},
		{Kind: agentruntime.EventMessageDelta, SessionID: "s", RunID: "r", Payload: []byte(`{"content":"\u0007text"}`)},
	}
	for _, event := range events {
		accumulator.accept(event, "s", "r")
	}
	got, ok := accumulator.accept(session.EventRecord{Kind: agentruntime.EventMessageDelta, SessionID: "s", RunID: "r", Payload: []byte(`{"content":"!"}`)}, "s", "r")
	if !ok || got != "safetext!" {
		t.Fatalf("got %q, %v", got, ok)
	}
}

func TestEventAccumulatorANSISequenceAtEveryBoundary(t *testing.T) {
	raw := "safe\x1b]8;;https://secret.example\aRED\x1b]8;;\adone"
	for boundary := 1; boundary < len(raw); boundary++ {
		t.Run(fmt.Sprintf("split-%d", boundary), func(t *testing.T) {
			var accumulator eventAccumulator
			chunks := []string{raw[:boundary], raw[boundary:]}
			got := ""
			for _, chunk := range chunks {
				payload, _ := json.Marshal(map[string]string{"content": chunk})
				if next, ok := accumulator.accept(session.EventRecord{Kind: agentruntime.EventMessageDelta, SessionID: "s", RunID: "r", Payload: payload}, "s", "r"); ok {
					got = next
				}
			}
			if got != "safeREDdone" {
				t.Fatalf("got %q", got)
			}
		})
	}
}

func TestEventAccumulatorHandlesOverflowBeforeRunFilter(t *testing.T) {
	var accumulator eventAccumulator
	accumulator.accept(session.EventRecord{Kind: agentruntime.EventTailOverflow, SessionID: "s"}, "s", "r")
	if !accumulator.resync {
		t.Fatal("overflow did not request resync")
	}
}
