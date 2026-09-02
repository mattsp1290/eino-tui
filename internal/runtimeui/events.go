package runtimeui

import (
	"encoding/json"

	agentruntime "github.com/mattsp1290/eino-agent/runtime"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/textsafe"
)

type eventAccumulator struct {
	raw    string
	resync bool
}

func (a *eventAccumulator) accept(event session.EventRecord, sessionID session.ID, runID session.RunID) (string, bool) {
	if event.Kind == agentruntime.EventTailOverflow {
		a.raw = ""
		a.resync = true
		return "", false
	}
	if event.SessionID != sessionID || event.RunID != runID || event.Kind != agentruntime.EventMessageDelta || a.resync {
		return "", false
	}
	var payload struct {
		Content string `json:"content"`
	}
	if json.Unmarshal(event.Payload, &payload) != nil || payload.Content == "" {
		return "", false
	}
	if len(a.raw)+len(payload.Content) > textsafe.MaxDisplayBytes {
		a.raw = ""
		a.resync = true
		return "", false
	}
	a.raw += payload.Content
	return textsafe.Display(a.raw), true
}
