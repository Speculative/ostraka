package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const tuiPanicLogName = "tui-panic.log"

// panicReporter persists a TUI panic before Bubble Tea recovers it. Keeping
// the path and write error separately lets Run report the result after Bubble
// Tea has restored the terminal, rather than printing into the alt screen.
type panicReporter struct {
	root string

	mu       sync.Mutex
	path     string
	writeErr error
}

func (r *panicReporter) report(phase string, value any, context string) {
	path := filepath.Join(r.root, "supervisor", tuiPanicLogName)
	record := fmt.Sprintf(
		"\n=== Ostraka TUI panic ===\n"+
			"time: %s\n"+
			"phase: %s\n"+
			"panic: %v\n"+
			"context: %s\n"+
			"stack:\n%s\n",
		time.Now().UTC().Format(time.RFC3339Nano), phase, value, context, debug.Stack(),
	)

	err := os.MkdirAll(filepath.Dir(path), 0755)
	if err == nil {
		var f *os.File
		f, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err == nil {
			if _, writeErr := f.WriteString(record); writeErr != nil {
				err = writeErr
			}
			if closeErr := f.Close(); err == nil {
				err = closeErr
			}
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.writeErr = err
		return
	}
	r.path = path
}

func (r *panicReporter) result() (path string, writeErr error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.path, r.writeErr
}

// panicLoggingModel records panics in model methods and commands, then
// re-panics so Bubble Tea keeps its normal terminal restoration and visible
// stack trace behavior.
type panicLoggingModel struct {
	inner    tea.Model
	reporter *panicReporter
}

func (m panicLoggingModel) Init() tea.Cmd {
	defer m.recoverPanic("Init")
	return m.wrapCommand(m.inner.Init(), "Init command")
}

func (m panicLoggingModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	defer m.recoverPanic("Update")
	next, cmd := m.inner.Update(msg)
	return panicLoggingModel{inner: next, reporter: m.reporter}, m.wrapCommand(cmd, "Update command")
}

func (m panicLoggingModel) View() string {
	defer m.recoverPanic("View")
	return m.inner.View()
}

func (m panicLoggingModel) wrapCommand(cmd tea.Cmd, phase string) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		defer m.recoverPanic(phase)
		return cmd()
	}
}

func (m panicLoggingModel) recoverPanic(phase string) {
	if value := recover(); value != nil {
		if m.reporter != nil {
			m.reporter.report(phase, value, panicContext(m.inner))
		}
		panic(value)
	}
}

func panicContext(inner tea.Model) string {
	state, ok := inner.(model)
	if !ok {
		return fmt.Sprintf("model_type=%T", inner)
	}

	selectedID, selectedStatus := "", ""
	if state.selected >= 0 && state.selected < len(state.items) {
		selectedID = state.items[state.selected].ID
		selectedStatus = string(state.items[state.selected].Status)
	}
	return fmt.Sprintf(
		"width=%d height=%d view_channel=%q view_archive=%t mode=%d focus=%d "+
			"copy_mode=%t project_pane=%d selected=%d selected_id=%q selected_status=%q "+
			"items=%d all_items=%d conv_width=%d conv_height=%d conv_offset=%d input_height=%d",
		state.width, state.height, state.view.channel, state.view.archive, state.mode, state.focus,
		state.copyMode, state.projectPane, state.selected, selectedID, selectedStatus,
		len(state.items), len(state.allItems), state.conv.Width, state.conv.Height,
		state.conv.YOffset, state.input.Height(),
	)
}
