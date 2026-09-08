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

type toolPosition struct {
	message  int
	activity int
}

type eventAccumulator struct {
	messages      []Message
	messageIndex  map[session.MessageID]int
	toolIndex     map[session.ToolCallID]toolPosition
	rawByMessage  map[session.MessageID]string
	seededMessage map[session.MessageID]bool
	budget        liveMessageBudget
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
		if !a.budget.admit(source) {
			a.resync = true
			break
		}
		message := source
		message.Tools = cloneActivities(source.Tools)
		a.messageIndex[messageID] = len(a.messages)
		a.messages = append(a.messages, message)
		a.seededMessage[messageID] = true
		for i, activity := range message.Tools {
			callID := session.ToolCallID(activity.ID)
			_, exists := a.toolIndex[callID]
			if callID == "" || exists {
				a.resync = true
				continue
			}
			a.toolIndex[callID] = toolPosition{message: a.messageIndex[messageID], activity: i}
		}
	}
	return a
}

func (a *eventAccumulator) ensureMaps() {
	if a.messageIndex == nil {
		a.messageIndex = make(map[session.MessageID]int)
		a.toolIndex = make(map[session.ToolCallID]toolPosition)
		a.rawByMessage = make(map[session.MessageID]string)
		a.seededMessage = make(map[session.MessageID]bool)
	}
}

func (a *eventAccumulator) messageFor(id session.MessageID) *Message {
	index, exists := a.messageIndex[id]
	if !exists {
		index = len(a.messages)
		a.messageIndex[id] = index
		a.messages = append(a.messages, Message{ID: string(id), Role: RoleAssistant, Status: StatusComplete})
	}
	return &a.messages[index]
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
	if !a.budget.reserve(len(newDisplay)-len(oldDisplay), 0, 0) {
		a.resync = true
		return accumulatorUpdate{}
	}
	a.rawByMessage[event.MessageID] = raw
	message := a.messageFor(event.MessageID)
	if message.Content == newDisplay {
		return accumulatorUpdate{}
	}
	message.Content = newDisplay
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
	if position, exists := a.toolIndex[event.ToolCallID]; exists {
		message := &a.messages[position.message]
		old := message.Tools[position.activity]
		if message.ID != string(event.MessageID) ||
			toolStatusRank(activity.Status) < toolStatusRank(old.Status) ||
			toolStatusRank(old.Status) == 3 && activity.Status != old.Status ||
			activity.Name != old.Name || activity.Subject != old.Subject {
			a.resync = true
			return accumulatorUpdate{}
		}
		if activity == old {
			return accumulatorUpdate{}
		}
		// Name and subject are immutable, so only status bytes can change.
		if !a.budget.reserve(0, 0, len(activity.Status)-len(old.Status)) {
			a.resync = true
			return accumulatorUpdate{}
		}
		message.Tools[position.activity] = activity
	} else {
		if !a.budget.reserve(0, 1, len(activity.Name)+len(activity.Subject)+len(activity.Status)) {
			a.resync = true
			return accumulatorUpdate{}
		}
		message := a.messageFor(event.MessageID)
		a.toolIndex[event.ToolCallID] = toolPosition{message: a.messageIndex[event.MessageID], activity: len(message.Tools)}
		message.Tools = append(message.Tools, activity)
	}
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
