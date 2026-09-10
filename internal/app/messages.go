package app

import (
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
)

type loadedMsg struct {
	snapshot runtimeui.Snapshot
	err      error
}
type startedMsg struct {
	draft  string
	result runtimeui.ActionResult
	err    error
}
type interruptedMsg struct{ err error }
type snapshotMsg struct {
	run      runtimeui.Run
	snapshot runtimeui.Snapshot
	ok       bool
}
type recoveredMsg struct {
	result runtimeui.ActionResult
	err    error
}
type recoveryDueMsg struct{}
type fatalMsg struct{}

type catalogLoadedMsg struct {
	generation uint64
	entries    []codexmodel.CatalogEntry
	err        error
}

// Conversation results carry the operation ID that issued them and the
// selection generation they were issued from, so a late result can never
// update a newer screen, draft, title, or run.
type directoryLoadedMsg struct {
	op         uint64
	generation uint64
	cursors    []string
	page       runtimeui.ConversationPage
	err        error
}
type conversationCreatedMsg struct {
	op         uint64
	generation uint64
	result     runtimeui.SelectionResult
	err        error
}
type conversationSelectedMsg struct {
	op         uint64
	generation uint64
	id         session.ID
	result     runtimeui.SelectionResult
	err        error
}
type conversationRenamedMsg struct {
	op         uint64
	generation uint64
	info       runtimeui.ConversationInfo
	err        error
}
