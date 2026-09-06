package app

import (
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
