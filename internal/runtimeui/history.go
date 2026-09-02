package runtimeui

import (
	"context"
	"errors"

	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-agent/session/history"
	"github.com/mattsp1290/eino-tui/internal/textsafe"
)

const maxTranscriptBytes = 2 << 20

func loadHistory(ctx context.Context, store session.Store, sessionID session.ID) ([]Message, error) {
	cursor := session.ReplayCursor{Limit: 100}
	var messages []session.Message
	var parts []session.Part
	for {
		batch, err := store.ListMessages(ctx, sessionID, cursor)
		if err != nil {
			return nil, err
		}
		messages = append(messages, batch.Messages...)
		parts = append(parts, batch.Parts...)
		if batch.Next == (session.ReplayCursor{}) {
			break
		}
		cursor = batch.Next
	}
	partsByMessage := make(map[session.MessageID][]session.Part)
	for _, part := range parts {
		partsByMessage[part.MessageID] = append(partsByMessage[part.MessageID], part)
	}
	runs := make(map[session.RunID]session.Run)
	result := make([]Message, 0, len(messages))
	for _, durable := range messages {
		if durable.Role != session.RoleUser && durable.Role != session.RoleAssistant {
			continue
		}
		projected, err := history.Project(session.ReplayBatch{Messages: []session.Message{durable}, Parts: partsByMessage[durable.ID]}, history.Options{IncludeReasoning: false, IncludeState: false})
		if err != nil {
			return nil, err
		}
		content := ""
		if len(projected) > 0 && projected[0] != nil {
			content = textsafe.Display(projected[0].Content)
		}
		if durable.Role == session.RoleAssistant && content == "" {
			continue
		}
		message := Message{ID: string(durable.ID), Content: content, Status: StatusComplete}
		if durable.Role == session.RoleUser {
			message.Role = RoleUser
		} else {
			message.Role = RoleAssistant
		}
		if durable.RunID != "" {
			run, ok := runs[durable.RunID]
			if !ok {
				run, err = store.GetRun(ctx, durable.RunID)
				if err != nil && !errors.Is(err, session.ErrNotFound) {
					return nil, err
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
		result = append(result, message)
	}
	return retainNewestWithinBudget(result, maxTranscriptBytes), nil
}

func retainNewestWithinBudget(messages []Message, limit int) []Message {
	if limit < 0 {
		limit = 0
	}
	start := len(messages)
	used := 0
	for start > 0 {
		size := len(messages[start-1].Content)
		if start < len(messages) && used+size > limit {
			break
		}
		used += size
		start--
	}
	if start == 0 {
		return messages
	}
	bounded := make([]Message, 0, len(messages)-start+1)
	bounded = append(bounded, Message{Role: RoleNotice, Content: NoticeHistoryOmitted, Status: StatusComplete})
	bounded = append(bounded, messages[start:]...)
	return bounded
}
