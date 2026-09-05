package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	agentmodel "github.com/mattsp1290/eino-agent/model"
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
)

type catalogResult struct {
	entries []codexmodel.CatalogEntry
	err     error
}

type scriptedCatalog struct {
	calls   int
	results []catalogResult
}

func (c *scriptedCatalog) ListModels(context.Context) ([]codexmodel.CatalogEntry, error) {
	c.calls++
	if len(c.results) == 0 {
		return nil, nil
	}
	result := c.results[0]
	c.results = c.results[1:]
	return result.entries, result.err
}

func pickerCatalog() []codexmodel.CatalogEntry {
	return []codexmodel.CatalogEntry{
		{
			ModelID: "gpt-5.5", DisplayName: "GPT-5.5", DefaultEffort: "medium",
			SupportedEfforts: []codexmodel.ReasoningEffort{{ID: "low"}, {ID: "medium"}, {ID: "high"}},
		},
		{
			ModelID: "o4-live", DisplayName: "O4 Live", DefaultEffort: "high",
			SupportedEfforts: []codexmodel.ReasoningEffort{{ID: "medium"}, {ID: "high"}},
		},
	}
}

func pickerConfig(catalog codexmodel.Catalog) Config {
	cfg := testDisplayConfig()
	cfg.Catalog = catalog
	return cfg
}

func altM() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'm', Text: "m", Mod: tea.ModAlt} }

func TestPickerIsLazyCachedAndExplicitlyRefreshable(t *testing.T) {
	catalog := &scriptedCatalog{results: []catalogResult{{entries: pickerCatalog()}, {entries: nil}}}
	model := New(context.Background(), &fakeService{load: runtimeui.Snapshot{Phase: runtimeui.PhaseIdle}}, pickerConfig(catalog))
	if catalog.calls != 0 {
		t.Fatal("New fetched catalog")
	}
	initCmd := model.Init()
	if catalog.calls != 0 {
		t.Fatal("Init fetched catalog")
	}
	model.Update(initCmd())
	if catalog.calls != 0 {
		t.Fatal("load command fetched catalog")
	}
	_, command := model.Update(altM())
	if command == nil || catalog.calls != 0 || model.picker.mode != pickerLoading {
		t.Fatalf("first open command=%v calls=%d mode=%v", command != nil, catalog.calls, model.picker.mode)
	}
	model.Update(command())
	if catalog.calls != 1 || model.picker.mode != pickerReady {
		t.Fatalf("first result calls=%d mode=%v", catalog.calls, model.picker.mode)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	_, command = model.Update(altM())
	if command != nil || catalog.calls != 1 || model.picker.mode != pickerReady {
		t.Fatalf("cached reopen command=%v calls=%d mode=%v", command != nil, catalog.calls, model.picker.mode)
	}
	_, command = model.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if command == nil || model.picker.mode != pickerLoading {
		t.Fatal("refresh did not start")
	}
	if _, duplicate := model.Update(tea.KeyPressMsg{Code: 'r', Text: "r"}); duplicate != nil {
		t.Fatal("loading refresh duplicated request")
	}
	model.Update(command())
	if catalog.calls != 2 || model.picker.mode != pickerEmpty || !model.picker.cacheValid {
		t.Fatalf("empty refresh calls=%d mode=%v cached=%v", catalog.calls, model.picker.mode, model.picker.cacheValid)
	}
}

func TestPickerGenerationFailureAndCancelPreserveDraft(t *testing.T) {
	catalog := &scriptedCatalog{results: []catalogResult{{err: errors.New("TOKEN /secret")}, {entries: pickerCatalog()}}}
	model := New(context.Background(), &fakeService{}, pickerConfig(catalog))
	model.textarea.SetValue("first\nsecond")
	_, firstCommand := model.Update(altM())
	oldGeneration := model.picker.generation
	model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.textarea.Value() != "first\nsecond" || model.picker.mode != pickerClosed {
		t.Fatalf("cancel changed draft/mode: %q %v", model.textarea.Value(), model.picker.mode)
	}
	_, secondCommand := model.Update(altM())
	model.Update(firstCommand())
	if model.picker.generation == oldGeneration || model.picker.mode != pickerLoading {
		t.Fatal("stale completion replaced active request")
	}
	model.Update(secondCommand())
	if model.picker.mode != pickerReady {
		t.Fatalf("fresh result mode=%v", model.picker.mode)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.textarea.Value() != "first\nsecond" || model.selected.selection.ModelID != "o4-live" || model.selected.effort != "high" {
		t.Fatalf("apply draft=%q selected=%#v", model.textarea.Value(), model.selected)
	}
	model.resize(100, 20)
	view := model.View().Content
	if !strings.Contains(view, "O4 Live (o4-live) · high") || strings.Contains(view, "TOKEN") || strings.Contains(view, "/secret") {
		t.Fatalf("applied view=%q", view)
	}
}

func TestPickerFailureHasNoStaleCacheAndBusyPhasesIgnoreOpen(t *testing.T) {
	catalog := &scriptedCatalog{results: []catalogResult{{entries: pickerCatalog()}, {err: errors.New("CANARY")}, {entries: pickerCatalog()}}}
	model := New(context.Background(), &fakeService{}, pickerConfig(catalog))
	_, cmd := model.Update(altM())
	model.Update(cmd())
	_, refresh := model.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	model.Update(refresh())
	if model.picker.mode != pickerFailed || model.picker.cacheValid || strings.Contains(model.View().Content, "CANARY") {
		t.Fatalf("failed state mode=%v cache=%v view=%q", model.picker.mode, model.picker.cacheValid, model.View().Content)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	_, retry := model.Update(altM())
	if retry == nil {
		t.Fatal("failed reopen reused stale cache")
	}
	model.Update(retry())
	model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	for _, phase := range []runtimeui.Phase{runtimeui.PhaseStarting, runtimeui.PhaseRunning, runtimeui.PhaseRecoveryWaiting, runtimeui.PhaseRecovering} {
		model.snapshot.Phase = phase
		if _, command := model.Update(altM()); command != nil || model.picker.mode != pickerClosed {
			t.Fatalf("phase %s opened picker", phase)
		}
	}
}

func TestPickerRendersOnlyBoundedVisibleWindow(t *testing.T) {
	entries := make([]codexmodel.CatalogEntry, codexmodel.MaxCatalogEntries)
	for i := range entries {
		entries[i] = codexmodel.CatalogEntry{
			ModelID: agentID(fmt.Sprintf("model-%03d", i)), DisplayName: strings.Repeat("界", 100), DefaultEffort: "medium",
			SupportedEfforts: []codexmodel.ReasoningEffort{{ID: "medium"}},
		}
	}
	model := New(context.Background(), &fakeService{}, testDisplayConfig())
	model.picker = pickerState{mode: pickerReady, cache: entries, cacheValid: true, highlightedModel: 128}
	view := model.pickerView(24, 6)
	if strings.Count(view, "\n") >= 6 || len(strings.Split(view, "\n")) > 6 || strings.Contains(view, "model-000") || !strings.Contains(view, "model-128") {
		t.Fatalf("unbounded/wrong picker view=%q", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if len([]rune(line)) > 24 {
			t.Fatalf("line wider than bound: %q", line)
		}
	}
}

func agentID(value string) agentmodel.ID { return agentmodel.ID(value) }
