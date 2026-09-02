package app

import (
	"context"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
)

const FatalDiagnostic = "eino-tui stopped after an internal application error"

type Fatal struct{ marked atomic.Bool }

func (f *Fatal) Marked() bool { return f != nil && f.marked.Load() }
func (f *Fatal) mark() {
	if f != nil {
		f.marked.Store(true)
	}
}

type safeModel struct {
	inner  tea.Model
	fatal  *Fatal
	cancel context.CancelFunc
}

func Safe(model tea.Model, cancel context.CancelFunc) (tea.Model, *Fatal) {
	fatal := &Fatal{}
	return &safeModel{inner: model, fatal: fatal, cancel: cancel}, fatal
}

func (m *safeModel) fail() {
	m.fatal.mark()
	if m.cancel != nil {
		m.cancel()
	}
}

func (m *safeModel) Init() (cmd tea.Cmd) {
	defer func() {
		if recover() != nil {
			m.fail()
			cmd = func() tea.Msg { return fatalMsg{} }
		}
	}()
	return m.wrap(m.inner.Init())
}

func (m *safeModel) Update(msg tea.Msg) (next tea.Model, cmd tea.Cmd) {
	defer func() {
		if recover() != nil {
			m.fail()
			next = m
			cmd = func() tea.Msg { return tea.Quit() }
		}
	}()
	if _, ok := msg.(fatalMsg); ok {
		m.fail()
		return m, func() tea.Msg { return tea.Quit() }
	}
	inner, command := m.inner.Update(msg)
	m.inner = inner
	return m, m.wrap(command)
}

func (m *safeModel) View() (view tea.View) {
	defer func() {
		if recover() != nil {
			m.fail()
			view = tea.NewView(FatalDiagnostic)
			view.AltScreen = true
		}
	}()
	return m.inner.View()
}

func (m *safeModel) wrap(command tea.Cmd) tea.Cmd {
	if command == nil {
		return nil
	}
	return func() (message tea.Msg) {
		defer func() {
			if recover() != nil {
				m.fail()
				message = fatalMsg{}
			}
		}()
		return command()
	}
}
