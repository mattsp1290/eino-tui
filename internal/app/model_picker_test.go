package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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

type contextCatalog struct {
	result  catalogResult
	entered chan context.Context
	exited  chan struct{}
	block   bool
}

func (c *contextCatalog) ListModels(ctx context.Context) ([]codexmodel.CatalogEntry, error) {
	c.entered <- ctx
	if c.block {
		<-ctx.Done()
		if c.exited != nil {
			close(c.exited)
		}
		return nil, ctx.Err()
	}
	return c.result.entries, c.result.err
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
	model := newTestModel(&fakeService{load: runtimeui.Snapshot{Phase: runtimeui.PhaseIdle}}, pickerConfig(catalog))
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
	model := newTestModel(&fakeService{}, pickerConfig(catalog))
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
	model := newTestModel(&fakeService{}, pickerConfig(catalog))
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

func TestPickerReleasesCompletedAndCanceledRequestContexts(t *testing.T) {
	for _, result := range []catalogResult{{entries: pickerCatalog()}, {err: errors.New("fixed failure")}} {
		catalog := &contextCatalog{result: result, entered: make(chan context.Context, 1)}
		model := newTestModel(&fakeService{}, pickerConfig(catalog))
		_, command := model.Update(altM())
		message := command()
		requestContext := <-catalog.entered
		select {
		case <-requestContext.Done():
			t.Fatal("request context canceled before completion was applied")
		default:
		}
		model.Update(message)
		select {
		case <-requestContext.Done():
		default:
			t.Fatal("completed request context was not released")
		}
	}

	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEscape}, {Code: 'c', Text: "c", Mod: tea.ModCtrl}} {
		catalog := &contextCatalog{entered: make(chan context.Context, 1), exited: make(chan struct{}), block: true}
		model := newTestModel(&fakeService{}, pickerConfig(catalog))
		_, command := model.Update(altM())
		completion := make(chan tea.Msg, 1)
		go func() { completion <- command() }()
		requestContext := <-catalog.entered
		_, quit := model.Update(key)
		select {
		case <-requestContext.Done():
		case <-time.After(time.Second):
			t.Fatal("request context was not canceled")
		}
		select {
		case <-catalog.exited:
		case <-time.After(time.Second):
			t.Fatal("catalog call did not exit after cancellation")
		}
		model.Update(<-completion)
		if key.Mod == tea.ModCtrl {
			if quit == nil {
				t.Fatal("ctrl+c did not return quit command")
			}
		} else if model.picker.mode != pickerClosed {
			t.Fatal("escape did not close picker")
		}
	}
}

func TestSelectorFullViewRespectsShortHeightAndKeepsControls(t *testing.T) {
	model := newTestModel(&fakeService{}, testDisplayConfig())
	model.picker = pickerState{mode: pickerReady, cache: pickerCatalog(), cacheValid: true, highlightedModel: 1}
	model.reconcileEffort()
	for _, height := range []int{1, 2, 3, 4, 5} {
		model.resize(100, height)
		content := model.View().Content
		if lines := strings.Count(content, "\n") + 1; lines > height {
			t.Fatalf("height=%d rendered lines=%d: %q", height, lines, content)
		}
		if !strings.Contains(content, "Enter apply") || !strings.Contains(content, "Esc close") {
			t.Fatalf("height=%d lost controls: %q", height, content)
		}
		if height >= 2 && (!strings.Contains(content, "o4-live") || !strings.Contains(content, "[medium]")) {
			t.Fatalf("height=%d lost selection identity: %q", height, content)
		}
	}
	model.resize(0, -1)
	if lines := strings.Count(model.View().Content, "\n") + 1; lines > 1 {
		t.Fatalf("clamped short view lines=%d", lines)
	}
}

func TestPickerCompactLayoutsPreserveFeasibleSemantics(t *testing.T) {
	model := newTestModel(&fakeService{}, testDisplayConfig())
	model.picker = pickerState{mode: pickerReady, cache: pickerCatalog(), cacheValid: true, highlightedModel: 1}
	model.reconcileEffort()
	for _, width := range []int{8, 12, 18, 24, 60} {
		for _, height := range []int{1, 2, 3, 4, 5, 6} {
			view := model.pickerView(width, height)
			lines := strings.Split(view, "\n")
			if len(lines) > height {
				t.Fatalf("width=%d height=%d lines=%d view=%q", width, height, len(lines), view)
			}
			for _, line := range lines {
				if ansi.StringWidth(line) > width {
					t.Fatalf("width=%d height=%d overlong line=%q", width, height, line)
				}
			}
			if !strings.Contains(view, "Esc") || (!strings.Contains(view, "Enter") && !strings.Contains(view, "↵")) {
				t.Fatalf("width=%d height=%d lost apply/escape controls: %q", width, height, view)
			}
			if height >= 2 && !strings.Contains(view, "o4-live") {
				t.Fatalf("width=%d height=%d lost model identity: %q", width, height, view)
			}
			if (width >= 18 && height >= 2 || height >= 3) && !strings.Contains(view, "[medium]") {
				t.Fatalf("width=%d height=%d lost feasible effort identity: %q", width, height, view)
			}
		}
	}
}

func TestPickerCompactStatusLayoutsPreserveControls(t *testing.T) {
	model := newTestModel(&fakeService{}, testDisplayConfig())
	for _, mode := range []pickerMode{pickerLoading, pickerFailed, pickerEmpty} {
		model.picker.mode = mode
		for _, width := range []int{8, 12, 18, 24} {
			for _, height := range []int{1, 2, 3} {
				view := model.pickerView(width, height)
				if !strings.Contains(view, "Esc") {
					t.Fatalf("mode=%d width=%d height=%d lost escape control: %q", mode, width, height, view)
				}
				if mode != pickerLoading && !strings.Contains(view, "R") {
					t.Fatalf("mode=%d width=%d height=%d lost retry control: %q", mode, width, height, view)
				}
			}
		}
	}
}

func TestAppliedMarkerRetainsNonDefaultEffortOffHighlight(t *testing.T) {
	model := newTestModel(&fakeService{}, testDisplayConfig())
	model.selected.effort = codexmodel.ReasoningEffortLow
	model.picker = pickerState{mode: pickerReady, cache: pickerCatalog(), cacheValid: true, highlightedModel: 1}
	model.reconcileEffort()
	line := model.pickerModelLine(0)
	if !strings.Contains(line, "[applied low]") {
		t.Fatalf("applied state lost off highlight: %q", line)
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
	model := newTestModel(&fakeService{}, testDisplayConfig())
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
