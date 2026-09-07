package runtimeui

import (
	"context"
	"errors"
	"sort"

	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-agent/session/history"
	"github.com/mattsp1290/eino-tui/internal/textsafe"
)

const maxTranscriptBytes = 2 << 20

type historyProjection struct {
	Messages     []Message
	LiveMessages []Message
}

type historyCandidate struct {
	message session.Message
	parts   []session.Part
	calls   int
}

type projectedHistoryMessage struct {
	message Message
	runID   session.RunID
}

func loadHistory(ctx context.Context, store session.Store, sessionID session.ID) ([]Message, error) {
	projection, err := loadHistoryProjection(ctx, store, sessionID, "")
	return projection.Messages, err
}

func loadHistoryProjection(ctx context.Context, store session.Store, sessionID session.ID, activeRun session.RunID) (historyProjection, error) {
	candidates, omitted, err := loadRecentCandidates(ctx, store, sessionID)
	if err != nil {
		return historyProjection{}, err
	}
	runs := make(map[session.RunID]session.Run)
	calls := make(map[session.ToolCallID]session.ToolCall)
	window := transcriptWindow{limit: maxTranscriptBytes, omitted: omitted}
	var live []Message
	for _, candidate := range candidates {
		projected, ok, projectErr := projectMessage(ctx, store, candidate.message, candidate.parts, runs, calls)
		if projectErr != nil {
			return historyProjection{}, projectErr
		}
		if !ok {
			continue
		}
		if activeRun != "" && projected.runID == activeRun && projected.message.Role == RoleAssistant {
			live = append(live, projected.message)
			continue
		}
		window.Push(projected.message)
	}
	return historyProjection{Messages: window.Items(), LiveMessages: cloneMessages(live)}, nil
}

func loadRecentCandidates(ctx context.Context, store session.Store, sessionID session.ID) ([]historyCandidate, bool, error) {
	cursor := session.ReplayCursor{Limit: 100}
	var candidates []historyCandidate
	totalCalls := 0
	omitted := false
	for {
		batch, err := store.ListMessages(ctx, sessionID, cursor)
		if err != nil {
			return nil, false, err
		}
		owners, err := session.ResolveReplayPartOwners(batch.Parts, batch.PartOwnerMessageIDs)
		if err != nil {
			return nil, false, err
		}
		partsByMessage := make(map[session.MessageID][]session.Part)
		for i, part := range batch.Parts {
			partsByMessage[owners[i]] = append(partsByMessage[owners[i]], part)
		}
		for _, durable := range batch.Messages {
			parts := partsByMessage[durable.ID]
			callCount := 0
			for _, part := range parts {
				if part.Kind == session.PartToolCall {
					callCount++
				}
			}
			if callCount > MaxTranscriptToolActivities {
				omitted = true
				continue
			}
			candidates = append(candidates, historyCandidate{message: durable, parts: append([]session.Part(nil), parts...), calls: callCount})
			totalCalls += callCount
			for len(candidates) > MaxTranscriptCandidates || totalCalls > MaxTranscriptToolActivities {
				totalCalls -= candidates[0].calls
				candidates = candidates[1:]
				omitted = true
			}
		}
		if batch.Next == (session.ReplayCursor{}) {
			break
		}
		cursor = batch.Next
	}
	return candidates, omitted, nil
}

func projectMessage(ctx context.Context, store session.Store, durable session.Message, parts []session.Part, runs map[session.RunID]session.Run, calls map[session.ToolCallID]session.ToolCall) (projectedHistoryMessage, bool, error) {
	if durable.Role != session.RoleUser && durable.Role != session.RoleAssistant {
		return projectedHistoryMessage{}, false, nil
	}
	sort.SliceStable(parts, func(i, j int) bool { return parts[i].Ordinal < parts[j].Ordinal })
	textParts := make([]session.Part, 0, len(parts))
	for _, part := range parts {
		if part.Kind != session.PartToolCall && part.Kind != session.PartToolResult {
			textParts = append(textParts, part)
		}
	}
	projected, err := history.Project(session.ReplayBatch{Messages: []session.Message{durable}, Parts: textParts}, history.Options{IncludeReasoning: false, IncludeState: false})
	if err != nil {
		return projectedHistoryMessage{}, false, err
	}
	content := ""
	if len(projected) > 0 && projected[0] != nil {
		content = textsafe.Display(projected[0].Content)
	}
	message := Message{ID: string(durable.ID), Content: content, Status: StatusComplete}
	if durable.Role == session.RoleUser {
		message.Role = RoleUser
	} else {
		message.Role = RoleAssistant
		message.Tools = projectToolActivities(ctx, store, durable, parts, calls)
		if content == "" && len(message.Tools) == 0 {
			return projectedHistoryMessage{}, false, nil
		}
	}
	if durable.RunID != "" {
		run, ok := runs[durable.RunID]
		if !ok {
			run, err = store.GetRun(ctx, durable.RunID)
			if err != nil && !errors.Is(err, session.ErrNotFound) {
				return projectedHistoryMessage{}, false, err
			}
			runs[durable.RunID] = run
		}
		switch run.Status {
		case session.RunInterrupted:
			message.Status = StatusInterrupted
		case session.RunFailed:
			message.Status = StatusFailed
		}
	}
	return projectedHistoryMessage{message: message, runID: durable.RunID}, true, nil
}

func projectToolActivities(ctx context.Context, store session.Store, durable session.Message, parts []session.Part, calls map[session.ToolCallID]session.ToolCall) []ToolActivity {
	var result []ToolActivity
	seen := make(map[session.ToolCallID]bool)
	for _, part := range parts {
		if part.Kind != session.PartToolCall {
			continue
		}
		one, err := history.Project(session.ReplayBatch{Messages: []session.Message{durable}, Parts: []session.Part{part}}, history.Options{IncludeReasoning: false, IncludeState: false})
		if err != nil || len(one) == 0 || one[0] == nil || len(one[0].ToolCalls) != 1 {
			continue
		}
		projected := one[0].ToolCalls[0]
		id := session.ToolCallID(projected.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		call, ok := calls[id]
		if !ok {
			call, err = store.GetToolCall(ctx, id)
			if err != nil {
				continue
			}
			calls[id] = call
		}
		if call.ID != id || call.SessionID != durable.SessionID || call.RunID != durable.RunID || call.MessageID != durable.ID || call.Name != projected.Function.Name {
			continue
		}
		activity, ok := summarizeToolCall(call)
		if ok {
			result = append(result, activity)
		}
	}
	return result
}

type transcriptWindow struct {
	limit   int
	used    int
	omitted bool
	items   []Message
}

func (w *transcriptWindow) Push(message Message) {
	size := messageDisplayBytes(message)
	if size > w.limit {
		w.omitted = true
		return
	}
	w.items = append(w.items, message)
	w.used += size
	for len(w.items) > 1 && w.used > w.limit {
		w.used -= messageDisplayBytes(w.items[0])
		w.items = w.items[1:]
		w.omitted = true
	}
}

func (w *transcriptWindow) Items() []Message {
	items := cloneMessages(w.items)
	if !w.omitted {
		return items
	}
	bounded := make([]Message, 0, len(items)+1)
	bounded = append(bounded, Message{Role: RoleNotice, Content: NoticeHistoryOmitted, Status: StatusComplete})
	return append(bounded, items...)
}
