package runtimeui

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tools/fileops"
	"github.com/mattsp1290/eino-tools/glob"
	"github.com/mattsp1290/eino-tools/search"
	"github.com/mattsp1290/eino-tui/internal/conversationtools"
	"github.com/mattsp1290/eino-tui/internal/textsafe"
)

const (
	MaxToolSubjectBytes         = 256
	MaxLiveToolActivities       = 64
	MaxLiveToolDisplayBytes     = 32 * 1024
	MaxTranscriptCandidates     = 500
	MaxTranscriptToolActivities = 1000
)

const unavailableToolSubject = "details unavailable"

func summarizeToolCall(call session.ToolCall) (ToolActivity, bool) {
	status, ok := mapToolStatus(call.Status)
	if !ok || call.ID == "" {
		return ToolActivity{}, false
	}
	subject, ok := toolSubject(call.Name, call.Input)
	if !ok {
		subject = unavailableToolSubject
	}
	if !knownToolName(call.Name) {
		return ToolActivity{}, false
	}
	return ToolActivity{ID: string(call.ID), Name: call.Name, Subject: subject, Status: status}, true
}

func knownToolName(name string) bool {
	switch name {
	case fileops.NameRead, fileops.NameList, glob.Name, search.Name, conversationtools.Name:
		return true
	default:
		return false
	}
}

func mapToolStatus(status session.ToolCallStatus) (ToolStatus, bool) {
	switch status {
	case session.ToolCallPending:
		return ToolPending, true
	case session.ToolCallRunning:
		return ToolRunning, true
	case session.ToolCallCompleted:
		return ToolCompleted, true
	case session.ToolCallFailed:
		return ToolFailed, true
	case session.ToolCallInterrupted:
		return ToolInterrupted, true
	default:
		return "", false
	}
}

func toolSubject(name string, input json.RawMessage) (string, bool) {
	if name == conversationtools.Name {
		// The title argument is user/model text, never a path; it is not shown.
		return conversationtools.Subject, true
	}
	if !utf8.Valid(input) {
		return "", false
	}
	var fields struct {
		Path    *string `json:"path"`
		Pattern *string `json:"pattern"`
		Offset  *int    `json:"offset"`
		Limit   *int    `json:"limit"`
	}
	if len(input) == 0 || json.Unmarshal(input, &fields) != nil {
		return "", false
	}
	var subject string
	switch name {
	case fileops.NameRead:
		if fields.Path == nil || *fields.Path == "" {
			return "", false
		}
		subject = *fields.Path
		if fields.Offset != nil && fields.Limit != nil && *fields.Offset > 0 && *fields.Limit > 0 {
			subject += fmt.Sprintf(" · line %d, %d lines", *fields.Offset, *fields.Limit)
		} else if fields.Offset != nil && *fields.Offset > 0 {
			subject += fmt.Sprintf(" · from line %d", *fields.Offset)
		} else if fields.Limit != nil && *fields.Limit > 0 {
			subject += fmt.Sprintf(" · first %d lines", *fields.Limit)
		}
	case fileops.NameList:
		if fields.Path == nil || *fields.Path == "" {
			subject = "."
		} else {
			subject = *fields.Path
		}
	case glob.Name, search.Name:
		if fields.Pattern == nil || *fields.Pattern == "" {
			return "", false
		}
		root := "."
		if fields.Path != nil && *fields.Path != "" {
			root = *fields.Path
		}
		subject = *fields.Pattern + " · in " + root
	default:
		return "", false
	}
	return boundedToolSubject(subject), true
}

func boundedToolSubject(value string) string {
	value = textsafe.Display(value)
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return unavailableToolSubject
	}
	if len(value) <= MaxToolSubjectBytes {
		return value
	}
	const marker = "…"
	cut := MaxToolSubjectBytes - len(marker)
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	if cut <= 0 {
		return unavailableToolSubject
	}
	return value[:cut] + marker
}

func toolStatusRank(status ToolStatus) int {
	switch status {
	case ToolPending:
		return 1
	case ToolRunning:
		return 2
	case ToolCompleted, ToolFailed, ToolInterrupted:
		return 3
	default:
		return 0
	}
}

func cloneActivities(values []ToolActivity) []ToolActivity {
	return append([]ToolActivity(nil), values...)
}

func cloneMessages(values []Message) []Message {
	result := make([]Message, len(values))
	for i := range values {
		result[i] = values[i]
		result[i].Tools = cloneActivities(values[i].Tools)
	}
	return result
}

func cloneSnapshot(value Snapshot) Snapshot {
	value.Messages = cloneMessages(value.Messages)
	value.LiveMessages = cloneMessages(value.LiveMessages)
	return value
}

func messageDisplayBytes(message Message) int {
	used := len(message.Content)
	for _, activity := range message.Tools {
		used += len(activity.Name) + len(activity.Subject) + len(activity.Status)
	}
	return used
}
