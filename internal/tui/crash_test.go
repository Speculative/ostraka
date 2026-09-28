package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

type panicViewModel struct{}

func (panicViewModel) Init() tea.Cmd { return nil }

func (panicViewModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return panicViewModel{}, nil }

func (panicViewModel) View() string { panic("test TUI panic") }

func TestPanicLoggingModelPersistsViewPanic(t *testing.T) {
	root := t.TempDir()
	reporter := &panicReporter{root: root}
	m := panicLoggingModel{inner: panicViewModel{}, reporter: reporter}

	func() {
		defer func() {
			if got := recover(); got != "test TUI panic" {
				t.Fatalf("recovered panic = %v, want test TUI panic", got)
			}
		}()
		m.View()
	}()

	path, err := reporter.result()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "supervisor", tuiPanicLogName); path != want {
		t.Fatalf("panic log path = %q, want %q", path, want)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"phase: View",
		"panic: test TUI panic",
		"model_type=tui.panicViewModel",
		"stack:",
	} {
		if !strings.Contains(string(content), want) {
			t.Errorf("panic log missing %q: %s", want, content)
		}
	}
}
