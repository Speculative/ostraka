package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Speculative/ostraka/internal/models"
)

func TestBootstrapIntroducesItemWithoutReinitializingHarness(t *testing.T) {
	command := "ostraka item turn item-1 --actor agent --content-stdin"
	got := Bootstrap("item-1", "instructions", "brief", "full item context", command, nil)
	normalized := normalizeWhitespace(got)
	for _, want := range []string{
		"Begin work on Ostraka item item-1",
		"Ostraka's initial prompt for this item",
		"item context available to this dispatch",
		"full item context",
		"instructions",
		"brief",
		command,
		"Starting a new Ostraka turn does not by itself require revalidation",
	} {
		if !strings.Contains(normalized, want) {
			t.Errorf("bootstrap prompt missing %q: %q", want, got)
		}
	}
	for _, unwanted := range []string{"starting a new agent session", "Apply any required startup"} {
		if strings.Contains(normalized, unwanted) {
			t.Errorf("bootstrap prompt unexpectedly contains %q: %q", unwanted, got)
		}
	}
}

func TestNudgeExplicitlyContinuesTheConversation(t *testing.T) {
	got := Nudge("20260808-054612", []string{"Please implement this."}, nil)
	normalized := normalizeWhitespace(got)
	for _, want := range []string{
		"continues the conversation on Ostraka item 20260808-054612",
		"Please implement this.",
		"Do not repeat session-start orientation",
		"re-read or re-announce startup skills already applied",
		"Later turns supersede earlier turns where they conflict",
		"Starting a new Ostraka turn does not by itself require revalidation",
		"Do not rerun a successful check against unchanged inputs merely to reorient",
		"ordinary harness progress updates remain visible to the user",
	} {
		if !strings.Contains(normalized, want) {
			t.Errorf("continuation prompt missing %q: %q", want, got)
		}
	}
	for _, unwanted := range []string{
		"starting a new agent session",
		"only after work and validation are complete",
		"item show",
	} {
		if strings.Contains(normalized, unwanted) {
			t.Errorf("continuation prompt unexpectedly contains %q: %q", unwanted, got)
		}
	}
}

func TestNudgeIncludesAllUnseenUserTurnsInOrder(t *testing.T) {
	got := Nudge("item-1", []string{"first unseen request", "second unseen request"}, nil)
	first := strings.Index(got, "first unseen request")
	second := strings.Index(got, "second unseen request")
	if first < 0 || second < 0 || first >= second {
		t.Fatalf("batched user turns are missing or out of order: %q", got)
	}
	for _, want := range []string{"--- user turn 1 ---", "--- user turn 2 ---"} {
		if !strings.Contains(got, want) {
			t.Errorf("batched continuation prompt missing %q: %q", want, got)
		}
	}
}

func TestNudgeWithoutUserTurnRecoversRequestInCurrentConversation(t *testing.T) {
	got := Nudge("20260808-054612", nil, nil)
	normalized := normalizeWhitespace(got)
	for _, want := range []string{
		"continues the conversation",
		"ostraka item show 20260808-054612 --json",
		"Continue any outstanding work already present in this conversation",
		"only if you cannot determine the outstanding request or necessary context",
	} {
		if !strings.Contains(normalized, want) {
			t.Errorf("fallback continuation prompt missing %q: %q", want, got)
		}
	}
	if strings.Contains(normalized, "--status pending-agent") {
		t.Errorf("fallback continuation prompt searches for a status that excludes the dispatched item: %q", got)
	}
}

func TestActivityOnlyNudgeDoesNotReloadRootItem(t *testing.T) {
	activity := models.Activity{Type: "subthread.closed", ChildID: "child-1"}
	got := Nudge("root-1", nil, []models.Activity{activity})
	if !strings.Contains(got, "No unseen user turns") {
		t.Errorf("activity-only prompt does not identify its input: %q", got)
	}
	if strings.Contains(got, "ostraka item show root-1 --json") {
		t.Errorf("activity-only prompt reloads the root item: %q", got)
	}
	if !strings.Contains(got, "ostraka item show <child-id> --json") {
		t.Errorf("activity-only prompt omitted child review guidance: %q", got)
	}
}

func TestPromptIncludesActivityContext(t *testing.T) {
	activity := models.Activity{
		Type:       "subthread.closed",
		ChildID:    "child-1",
		ChildTitle: "Decision",
		Result:     "archived",
		Actor:      models.ActorUser,
	}
	for name, got := range map[string]string{
		"bootstrap": Bootstrap("item-1", "", "", "context", "reply", []models.Activity{activity}),
		"nudge":     Nudge("item-1", []string{"continue"}, []models.Activity{activity}),
	} {
		if !strings.Contains(got, "subthread.closed: Decision (child-1, result=archived, actor=user)") {
			t.Errorf("%s prompt omitted activity context: %q", name, got)
		}
		if !strings.Contains(got, "Review each distinct affected subthread once") {
			t.Errorf("%s prompt omitted activity guidance: %q", name, got)
		}
	}
}

func TestAgentOrientationIncludesExactReplyCommand(t *testing.T) {
	command := "go run ./cmd/ostraka item turn <item-id> --actor agent --content-stdin"
	got := AgentOrientation(command)
	normalized := normalizeWhitespace(got)
	for _, want := range []string{
		command,
		"real multiline content",
		"ends this dispatch",
		"project brief replace --content-stdin",
		"complete replacement",
		"both long-lived and broadly relevant to most items",
		"supervisor includes the brief when initiating every item",
		"project description, goals, and norms",
		"not a log of recent changes",
		"medium-duration state",
		"recently fixed decision",
		"item-specific findings",
		"only matters to a subset of work",
		"linking dependent items to the item that established it",
		"do not duplicate it in the brief",
		"threaded work interface",
		"durable work card, roughly like a Kanban card",
		"normal harness progress and partial responses are streamed into Ostraka",
		"Starting a new Ostraka turn does not by itself require revalidation",
		"Do not rerun a successful check against unchanged inputs merely to reorient",
	} {
		if !strings.Contains(normalized, want) {
			t.Fatalf("orientation missing %q: %q", want, got)
		}
	}
	if !strings.Contains(got, "Final reply command:") {
		t.Fatalf("orientation omitted guidance: %q", got)
	}
	for _, unwanted := range []string{"complete item context", "only agent currently working"} {
		if strings.Contains(normalized, unwanted) {
			t.Fatalf("orientation unexpectedly contains %q: %q", unwanted, got)
		}
	}
}

func TestRenderPromptPanicsOnUnknownTemplateField(t *testing.T) {
	tmpl := mustPromptTemplate("invalid-test-template", "{{.Missing}}")
	assertPanicContains(t, "can't evaluate field Missing", func() {
		renderPrompt(tmpl, continuationWithoutTurnData{ItemID: "item-1"})
	})
}

func TestPromptBuildersRejectEmptyRequiredFields(t *testing.T) {
	tests := map[string]func(){
		"bootstrap item ID": func() {
			Bootstrap("", "", "", "context", "reply", nil)
		},
		"bootstrap item context": func() {
			Bootstrap("item-1", "", "", "", "reply", nil)
		},
		"bootstrap reply command": func() {
			Bootstrap("item-1", "", "", "context", "", nil)
		},
		"continuation item ID": func() {
			Nudge("", []string{"turn"}, nil)
		},
		"orientation reply command": func() {
			AgentOrientation("")
		},
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			assertPanicContains(t, "empty required field", run)
		})
	}
}

func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func assertPanicContains(t *testing.T, want string, run func()) {
	t.Helper()
	defer func() {
		got := recover()
		if got == nil {
			t.Fatalf("expected panic containing %q", want)
		}
		if message := fmt.Sprint(got); !strings.Contains(message, want) {
			t.Fatalf("panic = %q, want it to contain %q", message, want)
		}
	}()
	run()
}

func TestReplyCommandPrefersRepositoryLocalCommand(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "ostraka"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cmd", "ostraka", "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got := ReplyCommand(root, "<item-id>")
	want := "go run ./cmd/ostraka item turn <item-id> --actor agent --content-stdin"
	if got != want {
		t.Errorf("ReplyCommand() = %q, want %q", got, want)
	}
}

func TestReplyCommandFallsBackToInstalledCLI(t *testing.T) {
	got := ReplyCommand(t.TempDir(), "<item-id>")
	want := "ostraka item turn <item-id> --actor agent --content-stdin"
	if got != want {
		t.Errorf("ReplyCommand() = %q, want %q", got, want)
	}
}
