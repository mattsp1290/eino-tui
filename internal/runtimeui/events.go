package runtimeui

import (
	"context"
	"encoding/json"
	"time"

	agentruntime "github.com/mattsp1290/eino-agent/runtime"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/textsafe"
)

const toolLookupTimeout = 100 * time.Millisecond

type toolCallStore interface {
	GetToolCall(context.Context, session.ToolCallID) (session.ToolCall, error)
}

type accumulatorUpdate struct {
	messages []Message
	changed  bool
}

type eventAccumulator struct {
	messages      []Message
	messageIndex  map[session.MessageID]int
	callMessage   map[session.ToolCallID]session.MessageID
	rawByMessage  map[session.MessageID]string
	seededMessage map[session.MessageID]bool
	toolCount     int
	toolBytes     int
	textBytes     int
	resync        bool
}

func newEventAccumulator(seed []Message) eventAccumulator {
	a := eventAccumulator{}
	a.ensureMaps()
	for _, source := range seed {
		messageID := session.MessageID(source.ID)
		if messageID == "" || source.Role != RoleAssistant {
			continue
		}
		message := source
		message.Tools = cloneActivities(source.Tools)
		a.messageIndex[messageID] = len(a.messages)
		a.messages = append(a.messages, message)
		a.seededMessage[messageID] = true
		a.textBytes += len(message.Content)
		for _, activity := range message.Tools {
			callID := session.ToolCallID(activity.ID)
			if callID == "" || a.callMessage[callID] != "" {
				a.resync = true
				continue
			}
			a.callMessage[callID] = messageID
			a.toolCount++
			a.toolBytes += len(activity.Name) + len(activity.Subject) + len(activity.Status)
		}
	}
	if a.toolCount > MaxLiveToolActivities || a.toolBytes > MaxLiveToolDisplayBytes || a.textBytes > textsafe.MaxDisplayBytes {
		a.resync = true
	}
	return a
}

func (a *eventAccumulator) ensureMaps() {
	if a.messageIndex == nil {
		a.messageIndex = make(map[session.MessageID]int)
		a.callMessage = make(map[session.ToolCallID]session.MessageID)
		a.rawByMessage = make(map[session.MessageID]string)
		a.seededMessage = make(map[session.MessageID]bool)
	}
}

func (a *eventAccumulator) accept(ctx context.Context, store toolCallStore, event session.EventRecord, sessionID session.ID, runID session.RunID) accumulatorUpdate {
	a.ensureMaps()
	if event.Kind == agentruntime.EventTailOverflow {
		a.resync = true
		return accumulatorUpdate{}
	}
	if a.resync || event.SessionID != sessionID || event.RunID != runID {
		return accumulatorUpdate{}
	}
	switch event.Kind {
	case agentruntime.EventMessageDelta:
		return a.acceptMessageDelta(event)
	case agentruntime.EventToolCallUpdated:
		return a.acceptToolCall(ctx, store, event)
	default:
		return accumulatorUpdate{}
	}
}

func (a *eventAccumulator) acceptMessageDelta(event session.EventRecord) accumulatorUpdate {
	if event.MessageID == "" || a.seededMessage[event.MessageID] {
		return accumulatorUpdate{}
	}
	var payload struct {
		Content string `json:"content"`
	}
	if json.Unmarshal(event.Payload, &payload) != nil || payload.Content == "" {
		return accumulatorUpdate{}
	}
	raw := a.rawByMessage[event.MessageID]
	if len(raw)+len(payload.Content) > textsafe.MaxDisplayBytes {
		a.resync = true
		return accumulatorUpdate{}
	}
	oldDisplay := textsafe.Display(raw)
	raw += payload.Content
	newDisplay := textsafe.Display(raw)
	prospective := a.textBytes - len(oldDisplay) + len(newDisplay)
	if prospective > textsafe.MaxDisplayBytes {
		a.resync = true
		return accumulatorUpdate{}
	}
	a.rawByMessage[event.MessageID] = raw
	index, ok := a.messageIndex[event.MessageID]
	if !ok {
		index = len(a.messages)
		a.messageIndex[event.MessageID] = index
		a.messages = append(a.messages, Message{ID: string(event.MessageID), Role: RoleAssistant, Status: StatusComplete})
	}
	if a.messages[index].Content == newDisplay {
		return accumulatorUpdate{}
	}
	a.messages[index].Content = newDisplay
	a.textBytes = prospective
	return accumulatorUpdate{messages: cloneMessages(a.messages), changed: true}
}

func (a *eventAccumulator) acceptToolCall(ctx context.Context, store toolCallStore, event session.EventRecord) accumulatorUpdate {
	if store == nil || event.ID == "" || event.ToolCallID == "" || event.MessageID == "" || event.ToolTransition == "" {
		a.resync = true
		return accumulatorUpdate{}
	}
	lookupCtx, cancel := context.WithTimeout(ctx, toolLookupTimeout)
	call, err := store.GetToolCall(lookupCtx, event.ToolCallID)
	cancel()
	if err != nil || call.ID != event.ToolCallID || call.SessionID != event.SessionID || call.RunID != event.RunID || call.MessageID != event.MessageID {
		a.resync = true
		return accumulatorUpdate{}
	}
	activity, ok := summarizeToolCall(call)
	if !ok || toolStatusRank(activity.Status) < transitionRank(event.ToolTransition) {
		a.resync = true
		return accumulatorUpdate{}
	}
	if owner := a.callMessage[event.ToolCallID]; owner != "" {
		if owner != event.MessageID {
			a.resync = true
			return accumulatorUpdate{}
		}
		message := &a.messages[a.messageIndex[owner]]
		for i := range message.Tools {
			if message.Tools[i].ID != activity.ID {
				continue
			}
			old := message.Tools[i]
			if toolStatusRank(activity.Status) < toolStatusRank(old.Status) || toolStatusRank(old.Status) == 3 && activity.Status != old.Status || activity.Name != old.Name || activity.Subject != old.Subject {
				a.resync = true
				return accumulatorUpdate{}
			}
			if activity == old {
				return accumulatorUpdate{}
			}
			prospective := a.toolBytes - len(old.Name) - len(old.Subject) - len(old.Status) + len(activity.Name) + len(activity.Subject) + len(activity.Status)
			if prospective > MaxLiveToolDisplayBytes {
				a.resync = true
				return accumulatorUpdate{}
			}
			message.Tools[i] = activity
			a.toolBytes = prospective
			return accumulatorUpdate{messages: cloneMessages(a.messages), changed: true}
		}
		a.resync = true
		return accumulatorUpdate{}
	}
	if a.toolCount >= MaxLiveToolActivities {
		a.resync = true
		return accumulatorUpdate{}
	}
	addedBytes := len(activity.Name) + len(activity.Subject) + len(activity.Status)
	if a.toolBytes+addedBytes > MaxLiveToolDisplayBytes {
		a.resync = true
		return accumulatorUpdate{}
	}
	index, exists := a.messageIndex[event.MessageID]
	if !exists {
		index = len(a.messages)
		a.messageIndex[event.MessageID] = index
		a.messages = append(a.messages, Message{ID: string(event.MessageID), Role: RoleAssistant, Status: StatusComplete})
	}
	a.messages[index].Tools = append(a.messages[index].Tools, activity)
	a.callMessage[event.ToolCallID] = event.MessageID
	a.toolCount++
	a.toolBytes += addedBytes
	return accumulatorUpdate{messages: cloneMessages(a.messages), changed: true}
}

func transitionRank(phase session.ToolTransitionPhase) int {
	switch phase {
	case session.ToolTransitionPending:
		return 1
	case session.ToolTransitionRunning:
		return 2
	case session.ToolTransitionTerminal:
		return 3
	default:
		return 4
	}
}
