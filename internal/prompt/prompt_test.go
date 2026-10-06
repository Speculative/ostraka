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
		"The item context is below",
		"full item context",
		"instructions",
		"brief",
		command,
		"starting a new Ostraka turn alone does not require revalidation",
		"finish a coherent slice",
		"self-scheduling commands",
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
	if count := strings.Count(got, "For work likely to exceed one dispatch"); count != 1 {
		t.Errorf("checkpoint guidance appears %d times, want once", count)
	}
	for _, want := range []string{
		"continues the conversation on Ostraka item 20260808-054612",
		"Please implement this.",
		"Do not repeat session-start orientation",
		"re-read or re-announce startup skills already applied",
		"Later turns supersede earlier turns where they conflict",
		"Starting a new Ostraka turn does not by itself require revalidation",
		"Do not rerun a successful check against unchanged inputs merely to reorient",
		"ordinary harness progress updates remain visible to the user",
		"bounded checkpoints",
		"self-scheduling commands",
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
		"bootstrap":     Bootstrap("item-1", "", "", "context", "reply", []models.Activity{activity}),
		"activity-only": Nudge("item-1", nil, []models.Activity{activity}),
		"mixed nudge":   Nudge("item-1", []string{"continue"}, []models.Activity{activity}),
	} {
		normalized := normalizeWhitespace(got)
		lower := strings.ToLower(normalized)
		if !strings.Contains(got, "subthread.closed: Decision (child-1, result=archived, actor=user)") {
			t.Errorf("%s prompt omitted activity context: %q", name, got)
		}
		read := strings.Index(lower, "read every distinct affected child")
		conclusion := -1
		for _, marker := range []string{"then assess", "then consolidate", "reconcile the batch"} {
			if i := strings.Index(lower, marker); i >= 0 {
				conclusion = i
				break
			}
		}
		if !strings.Contains(normalized, "ostraka item show <child-id> --json") ||
			read < 0 || conclusion < 0 || read >= conclusion {
			t.Errorf("%s prompt must read affected children before consolidation: %q", name, got)
		}
		if !strings.Contains(got, activityReviewGuidance) || !strings.Contains(got, activityFollowupGuidance) {
			t.Errorf("%s prompt does not include the shared guidance verbatim: %q", name, got)
		}
		if name != "mixed nudge" && strings.Contains(got, mixedActivityGuidance) {
			t.Errorf("%s prompt refers to unseen user turns: %q", name, got)
		}
	}
}

func TestActivityGuidanceSeparatesDispatchPaths(t *testing.T) {
	activity := models.Activity{Type: "subthread.deleted", ChildID: "child-1"}
	bootstrap := normalizeWhitespace(Bootstrap("root-1", "", "", "context", "reply", []models.Activity{activity}))
	only := normalizeWhitespace(Nudge("root-1", nil, []models.Activity{activity}))
	mixed := normalizeWhitespace(Nudge("root-1", []string{"Implement the change."}, []models.Activity{activity}))

	for name, got := range map[string]string{"bootstrap": bootstrap, "activity-only": only, "mixed": mixed} {
		if !strings.Contains(got, "whether its work is still needed elsewhere") ||
			!strings.Contains(got, "clarif") {
			t.Errorf("%s prompt omits uncertainty about a deleted child: %q", name, got)
		}
	}
	for _, want := range []string{
		"If it unblocks work already authorized by the root goal and conversation, continue that work here, including implementation",
		"If the next step needs a user decision or new authorization, ask here",
	} {
		if !strings.Contains(only, want) {
			t.Errorf("activity-only prompt omitted root continuation guidance %q", want)
		}
	}
	for _, want := range []string{"Reconcile the child outcomes with the unseen user turns", "details about children", "Point out a conflict"} {
		if !strings.Contains(mixed, want) {
			t.Errorf("mixed prompt omitted %q: %q", want, mixed)
		}
	}
	if strings.Contains(bootstrap, "Reconcile the child outcomes with the unseen user turns") ||
		strings.Contains(only, "Reconcile the child outcomes with the unseen user turns") {
		t.Errorf("non-mixed activity guidance includes mixed-turn instruction")
	}
	review := strings.Index(mixed, normalizeWhitespace(activityReviewGuidance))
	reconcile := strings.Index(mixed, normalizeWhitespace(mixedActivityGuidance))
	followup := strings.Index(mixed, normalizeWhitespace(activityFollowupGuidance))
	if review < 0 || reconcile <= review || followup <= reconcile {
		t.Errorf("mixed prompt should review children, reconcile user turns, then act: %q", mixed)
	}
}

func TestPromptIncludesReparentActivityRoots(t *testing.T) {
	activity := models.Activity{
		Type:       "subthread.moved",
		ChildID:    "child-1",
		ChildTitle: "Moved branch",
		FromRootID: "old-root",
		ToRootID:   "new-root",
		Result:     "moved",
		Actor:      models.ActorUser,
	}
	got := Nudge("new-root", nil, []models.Activity{activity})
	if !strings.Contains(got, "subthread.moved: Moved branch (child-1, from=old-root, to=new-root, result=moved, actor=user)") {
		t.Fatalf("reparent activity roots missing from prompt: %q", got)
	}
}

func TestAgentOrientationIncludesExactReplyCommand(t *testing.T) {
	command := "go run ./cmd/ostraka item turn <item-id> --actor agent --content-stdin"
	got := normalizeWhitespace(AgentOrientation(command))
	for _, want := range []string{
		command,
		"ends this dispatch",
		"project brief replace --content-stdin",
		"within 6000 characters",
		"ostraka item show <item-id> --include-partial --json",
		"standalone partial_trace entry",
		"agent reply",
		"starting a new Ostraka turn alone does not require revalidation",
		"New items have independent sessions",
		"ostraka item suggest --mentions <item-id>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("orientation missing %q", want)
		}
	}
	for _, unwanted := range []string{"turn_timestamp", "--argjson turn 42", "partial_traces", "jq '.turns[-20:]'"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("orientation still includes %q", unwanted)
		}
	}
}

func TestThreadingGuidanceKeepsSequentialConversationInOneItem(t *testing.T) {
	got := normalizeWhitespace(AgentOrientation("reply"))
	for _, want := range []string{
		"it and this item will have distinct active discussions",
		"blocking questions on this item",
		"when the user asks for a split",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("threading guidance missing %q", want)
		}
	}
	for _, unwanted := range []string{
		"likely to need multiple exchanges",
		"independently discussable",
		"two or more distinct conversations need to exist in the root family",
	} {
		if strings.Contains(got, unwanted) {
			t.Errorf("threading guidance still contains %q", unwanted)
		}
	}

	activityPrompt := normalizeWhitespace(Nudge("root-1", nil, []models.Activity{{Type: "subthread.closed", ChildID: "child-1"}}))
	if !strings.Contains(activityPrompt, "the root and proposed child both have distinct discussions") {
		t.Error("activity follow-up guidance does not compare the root with the proposed child")
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
