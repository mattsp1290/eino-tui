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
	window := transcriptWindow{limit: maxTranscriptBytes}
	runs := make(map[session.RunID]session.Run)
	for {
		batch, err := store.ListMessages(ctx, sessionID, cursor)
		if err != nil {
			return nil, err
		}
		partsByMessage := make(map[session.MessageID][]session.Part)
		for _, part := range batch.Parts {
			partsByMessage[part.MessageID] = append(partsByMessage[part.MessageID], part)
		}
		for _, durable := range batch.Messages {
			message, ok, err := projectMessage(ctx, store, durable, partsByMessage[durable.ID], runs)
			if err != nil {
				return nil, err
			}
			if ok {
				window.Push(message)
			}
		}
		if batch.Next == (session.ReplayCursor{}) {
			break
		}
		cursor = batch.Next
	}
	return window.Items(), nil
}

func projectMessage(ctx context.Context, store session.Store, durable session.Message, parts []session.Part, runs map[session.RunID]session.Run) (Message, bool, error) {
	if durable.Role != session.RoleUser && durable.Role != session.RoleAssistant {
		return Message{}, false, nil
	}
	projected, err := history.Project(session.ReplayBatch{Messages: []session.Message{durable}, Parts: parts}, history.Options{IncludeReasoning: false, IncludeState: false})
	if err != nil {
		return Message{}, false, err
	}
	content := ""
	if len(projected) > 0 && projected[0] != nil {
		content = textsafe.Display(projected[0].Content)
	}
	if durable.Role == session.RoleAssistant && content == "" {
		return Message{}, false, nil
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
				return Message{}, false, err
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
	return message, true, nil
}

type transcriptWindow struct {
	limit   int
	used    int
	omitted bool
	items   []Message
}

func (w *transcriptWindow) Push(message Message) {
	w.items = append(w.items, message)
	w.used += len(message.Content)
	for len(w.items) > 1 && w.used > w.limit {
		w.used -= len(w.items[0].Content)
		w.items = w.items[1:]
		w.omitted = true
	}
}

func (w *transcriptWindow) Items() []Message {
	if !w.omitted {
		return w.items
	}
	bounded := make([]Message, 0, len(w.items)+1)
	bounded = append(bounded, Message{Role: RoleNotice, Content: NoticeHistoryOmitted, Status: StatusComplete})
	return append(bounded, w.items...)
}
