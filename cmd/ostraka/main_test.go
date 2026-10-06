package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
	"github.com/spf13/cobra"
)

func TestTurnContent(t *testing.T) {
	tests := []struct {
		name         string
		stdin        string
		args         []string
		contentStdin bool
		want         string
		wantErr      bool
	}{
		{name: "positional", args: []string{"id", "short reply"}, want: "short reply"},
		{name: "stdin preserves multiline content", stdin: "first line\nsecond line\n", args: []string{"id"}, contentStdin: true, want: "first line\nsecond line\n"},
		{name: "blank positional content", args: []string{"id", " \t\n"}, wantErr: true},
		{name: "blank stdin content", stdin: "\n\t", args: []string{"id"}, contentStdin: true, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := turnContent(strings.NewReader(tt.stdin), tt.args, tt.contentStdin)
			if (err != nil) != tt.wantErr {
				t.Fatalf("turnContent() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("turnContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestItemTurnArgumentRules(t *testing.T) {
	original := turnFlags.contentStdin
	t.Cleanup(func() { turnFlags.contentStdin = original })

	turnFlags.contentStdin = false
	if err := itemTurnCmd.Args(itemTurnCmd, []string{"id", "content"}); err != nil {
		t.Fatalf("positional form rejected: %v", err)
	}
	if err := itemTurnCmd.Args(itemTurnCmd, []string{"id"}); err == nil {
		t.Fatal("missing positional content was accepted")
	}

	turnFlags.contentStdin = true
	if err := itemTurnCmd.Args(itemTurnCmd, []string{"id"}); err != nil {
		t.Fatalf("stdin form rejected: %v", err)
	}
	if err := itemTurnCmd.Args(itemTurnCmd, []string{"id", "content"}); err == nil {
		t.Fatal("stdin form accepted positional content")
	}
}

func TestItemRenameCommandRenamesItem(t *testing.T) {
	project := t.TempDir()
	s, err := store.NewStore(filepath.Join(project, ".ostraka"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(models.ChannelInbox, "old title", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}

	t.Chdir(project)
	if err := itemRenameCmd.Args(itemRenameCmd, []string{item.ID, "new title"}); err != nil {
		t.Fatal(err)
	}
	if err := itemRenameCmd.RunE(itemRenameCmd, []string{item.ID, "new title"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "new title" {
		t.Fatalf("CLI renamed title = %q, want %q", got.Title, "new title")
	}
}

func TestItemRenameCommandRequiresIDAndTitle(t *testing.T) {
	for _, args := range [][]string{{}, {"id"}, {"id", "title", "extra"}} {
		if err := itemRenameCmd.Args(itemRenameCmd, args); err == nil {
			t.Errorf("rename accepted %v", args)
		}
	}
}

func TestAgentProjectReplacementUsesDispatchItemForActivity(t *testing.T) {
	project := t.TempDir()
	s, err := store.NewStore(filepath.Join(project, ".ostraka"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(models.ChannelInbox, "thread", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OSTRAKA_AGENT_ITEM_ID", item.ID)
	if err := replaceProjectBrief(s, "agent brief"); err != nil {
		t.Fatal(err)
	}
	if err := replaceProjectInstructions(s, "agent instructions"); err != nil {
		t.Fatal(err)
	}
	activities, err := s.ListActivities(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 2 || activities[0].Type != store.ActivityProjectBriefChanged || activities[1].Type != store.ActivityProjectInstructionsChanged {
		t.Fatalf("agent project activities = %+v", activities)
	}
	for _, activity := range activities {
		if activity.ItemID != item.ID || activity.ItemTitle != item.Title || activity.Actor != models.ActorAgent || !activity.Handled {
			t.Errorf("agent project activity attribution = %+v", activity)
		}
	}

	t.Setenv("OSTRAKA_AGENT_ITEM_ID", "")
	if err := replaceProjectBrief(s, "user brief"); err != nil {
		t.Fatal(err)
	}
	activities, err = s.ListActivities(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 2 {
		t.Fatalf("user project edit created activity = %+v", activities)
	}
}

func TestItemReparentCommandMovesItemAndHonorsFlattenFlag(t *testing.T) {
	project := t.TempDir()
	rootDir := filepath.Join(project, ".ostraka")
	s, err := store.NewStore(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	oldRoot, _ := s.CreateItem(models.ChannelInbox, "old root", "old body", models.TypeThread, models.StatusActive, "")
	newRoot, _ := s.CreateItem(models.ChannelInbox, "new root", "new body", models.TypeThread, models.StatusActive, "")
	child, _ := s.CreateSubthread(oldRoot.ID, "child", "child body", models.TypeThread, models.StatusActive)

	t.Chdir(project)
	oldParent, oldFlatten := reparentFlags.parent, reparentFlags.flattenChildren
	t.Cleanup(func() {
		reparentFlags.parent = oldParent
		reparentFlags.flattenChildren = oldFlatten
	})
	reparentFlags.parent = newRoot.ID
	reparentFlags.flattenChildren = false
	if err := itemReparentCmd.Args(itemReparentCmd, []string{oldRoot.ID}); err != nil {
		t.Fatal(err)
	}
	if err := itemReparentCmd.RunE(itemReparentCmd, []string{oldRoot.ID}); err == nil || !strings.Contains(err.Error(), "--flatten-children") {
		t.Fatalf("parent move without flag error = %v", err)
	}

	reparentFlags.flattenChildren = true
	if err := itemReparentCmd.RunE(itemReparentCmd, []string{oldRoot.ID}); err != nil {
		t.Fatal(err)
	}
	moved, err := s.GetItem(oldRoot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Parent != newRoot.ID {
		t.Fatalf("CLI moved root parent = %q, want %q", moved.Parent, newRoot.ID)
	}
	movedChild, _ := s.GetItem(child.ID)
	if movedChild.Parent != newRoot.ID {
		t.Fatalf("CLI flattened child parent = %q, want %q", movedChild.Parent, newRoot.ID)
	}
}

func TestItemReparentCommandAcceptsPositionalDestination(t *testing.T) {
	oldParent, oldFlatten := reparentFlags.parent, reparentFlags.flattenChildren
	t.Cleanup(func() {
		reparentFlags.parent = oldParent
		reparentFlags.flattenChildren = oldFlatten
	})
	reparentFlags.parent = ""
	reparentFlags.flattenChildren = false
	if err := itemReparentCmd.Args(itemReparentCmd, []string{"source", "target"}); err != nil {
		t.Fatalf("positional destination rejected: %v", err)
	}
	if err := itemReparentCmd.Args(itemReparentCmd, []string{"source"}); err == nil {
		t.Fatal("missing destination accepted")
	}
	reparentFlags.parent = "target"
	if err := itemReparentCmd.Args(itemReparentCmd, []string{"source", "other-target"}); err == nil {
		t.Fatal("duplicate destinations accepted")
	}
}

func TestUserSettableStatus(t *testing.T) {
	for _, status := range models.UserSettableStatuses() {
		got, err := userSettableStatus(string(status))
		if err != nil {
			t.Errorf("userSettableStatus(%q) returned error: %v", status, err)
		}
		if got != status {
			t.Errorf("userSettableStatus(%q) = %q", status, got)
		}
	}

	for _, status := range []models.Status{
		models.StatusPendingAgent,
		models.StatusAgentAcknowledged,
		models.StatusProposed,
		models.Status("unknown"),
		models.Status("done"),
	} {
		if _, err := userSettableStatus(string(status)); err == nil {
			t.Errorf("userSettableStatus(%q) accepted non-user status", status)
		}
	}
}

func TestGroupAssignmentAndFilterValues(t *testing.T) {
	for _, value := range []string{"v1", "post-v1"} {
		group, err := groupAssignment(value)
		if err != nil || group != value {
			t.Errorf("groupAssignment(%q) = %q, %v", value, group, err)
		}
	}
	group, err := groupAssignment("none")
	if err != nil || group != "" {
		t.Fatalf("groupAssignment(none) = %q, %v", group, err)
	}
	if _, err := groupAssignment("Post V1"); err == nil {
		t.Fatal("groupAssignment accepted invalid slug")
	}
	filtered, err := groupFilter("none")
	if err != nil || filtered == nil || *filtered != "" {
		t.Fatalf("groupFilter(none) = %v, %v", filtered, err)
	}
}

func TestItemJSONConversationIncludesActivitiesAndTraces(t *testing.T) {
	started := time.Date(2026, 9, 24, 15, 0, 0, 0, time.UTC)
	item := models.Item{
		ID: "item-1", Title: "trace", Mentions: []string{"item-2", "missing"},
		Backlinks: []string{"item-3"},
		Turns: []models.Turn{
			{Actor: models.ActorUser, Timestamp: started.Add(time.Minute), Content: "question"},
			{Actor: models.ActorAgent, Timestamp: started.Add(3 * time.Minute), Content: "answer"},
		},
	}
	activities := []models.Activity{
		{ID: "activity-1", Type: store.ActivityAgentSessionStarted, Actor: models.ActorAgent,
			Timestamp: started, Result: "codex", Model: "test-model"},
		{ID: "activity-2", Type: store.ActivityAgentEndedWithoutFinalResponse, Actor: models.ActorAgent,
			Timestamp: started.Add(5 * time.Minute), Result: "failed"},
	}
	partials := []models.PartialTrace{
		{ID: "trace-1", Timestamp: started.Add(2 * time.Minute), TurnTimestamp: started.Add(3 * time.Minute), Status: "completed", Content: "provider output"},
		{ID: "trace-2", Timestamp: started.Add(4 * time.Minute), Status: "failed", Content: "unfinished work"},
	}
	got, err := itemToJSONWithConversation(item, activities, partials, map[string]string{"item-2": "Target", "item-3": "Source"})
	if err != nil {
		t.Fatal(err)
	}
	conversation := got["conversation"].([]map[string]any)
	if len(conversation) != 5 {
		t.Fatalf("conversation = %#v", conversation)
	}
	if conversation[0]["activity"].(map[string]any)["provider"] != "codex" {
		t.Fatalf("session start = %#v", conversation[0])
	}
	if conversation[1]["user_message"] != "question" {
		t.Fatalf("user entry = %#v", conversation[1])
	}
	if conversation[2]["agent_reply"] != "answer" || conversation[2]["partial_trace"].(map[string]any)["output"] != "provider output" {
		t.Fatalf("agent entry = %#v", conversation[2])
	}
	if _, posted := conversation[3]["agent_reply"]; posted {
		t.Fatalf("standalone trace has reply: %#v", conversation[3])
	}
	if conversation[3]["partial_trace"].(map[string]any)["output"] != "unfinished work" {
		t.Fatalf("standalone trace = %#v", conversation[3])
	}
	if conversation[4]["activity"].(map[string]any)["type"] != store.ActivityAgentEndedWithoutFinalResponse {
		t.Fatalf("no-output activity = %#v", conversation[4])
	}
	mentions := got["mentioned_items"].([]map[string]any)
	if len(mentions) != 2 || mentions[0]["title"] != "Target" || mentions[1]["title"] != nil {
		t.Fatalf("mentions = %#v", mentions)
	}
	if got["backlinks"].([]map[string]any)[0]["title"] != "Source" {
		t.Fatalf("backlinks = %#v", got["backlinks"])
	}
	if _, present := got["related"]; present {
		t.Fatal("legacy related key remains")
	}
	if _, present := got["turns"]; present {
		t.Fatal("turns key remains")
	}
}

func TestItemJSONDefaultOmitsTracesAndUsesEmptyArrays(t *testing.T) {
	item := models.Item{ID: "item-1", Group: "v1"}
	got, err := itemToJSONWithConversation(item, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["group"] != "v1" {
		t.Fatalf("group = %#v", got["group"])
	}
	if len(got["conversation"].([]map[string]any)) != 0 {
		t.Fatalf("conversation = %#v", got["conversation"])
	}
	if got["mentioned_items"] == nil || got["backlinks"] == nil {
		t.Fatalf("empty links = %#v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire["mentioned_items"].([]any); !ok {
		t.Fatalf("mentioned_items is not an array: %s", encoded)
	}
	if _, ok := wire["backlinks"].([]any); !ok {
		t.Fatalf("backlinks is not an array: %s", encoded)
	}
}

func TestItemJSONRejectsTraceLinkedToMissingTurn(t *testing.T) {
	_, err := itemToJSONWithConversation(models.Item{ID: "item-1"}, nil, []models.PartialTrace{{
		ID: "trace-1", TurnTimestamp: time.Date(2026, 9, 24, 15, 0, 0, 0, time.UTC), Content: "work",
	}}, nil)
	if err == nil || !strings.Contains(err.Error(), "missing agent turn") {
		t.Fatalf("missing turn error = %v", err)
	}
}

func TestItemShowHelpDocumentsConversation(t *testing.T) {
	for _, want := range []string{
		"conversation array", "user_message", "agent_reply", "activity",
		"agent session starts", "--include-partial", "partial_trace",
		"jq '.conversation[-20:]'",
	} {
		if !strings.Contains(itemShowCmd.Long, want) {
			t.Errorf("item show help missing %q", want)
		}
	}
}

func captureCommandOutput(t *testing.T, run func() error) []byte {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = writer
	t.Cleanup(func() { os.Stdout = original })
	err = run()
	os.Stdout = original
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	return data
}

func captureJSONCommand(t *testing.T, run func() error) map[string]any {
	t.Helper()
	data := captureCommandOutput(t, run)
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("invalid command JSON: %v: %s", err, data)
	}
	return result
}

func TestItemShowJSONLoadsActivitiesAndOptInTraces(t *testing.T) {
	project := t.TempDir()
	s, err := store.NewStore(filepath.Join(project, ".ostraka"))
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateItem(models.ChannelInbox, "Target title", "target", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(models.ChannelInbox, "Source title", "See @"+target.ID, models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	item, err = s.AddTurn(item.ID, models.ActorAgent, "reply")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddActivity(item.ID, models.Activity{
		Type: store.ActivityAgentSessionStarted, Actor: models.ActorAgent,
		Timestamp: item.Turns[0].Timestamp.Add(-time.Minute), Result: "codex",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendPartialTrace(item.ID, models.PartialTrace{
		Timestamp:     item.Turns[0].Timestamp.Add(-time.Second),
		TurnTimestamp: item.Turns[0].Timestamp, Status: "completed", Content: "working",
	}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	previous := showFlags
	t.Cleanup(func() { showFlags = previous })
	showFlags.asJSON = true
	showFlags.includePartial = false
	plain := captureJSONCommand(t, func() error { return itemShowCmd.RunE(itemShowCmd, []string{item.ID}) })
	conversation := plain["conversation"].([]any)
	if len(conversation) != 2 || conversation[0].(map[string]any)["activity"] == nil || conversation[1].(map[string]any)["partial_trace"] != nil {
		t.Fatalf("default conversation = %#v", conversation)
	}
	mentioned := plain["mentioned_items"].([]any)
	if len(mentioned) != 1 || mentioned[0].(map[string]any)["title"] != "Target title" {
		t.Fatalf("mentioned items = %#v", mentioned)
	}
	showFlags.includePartial = true
	withTrace := captureJSONCommand(t, func() error { return itemShowCmd.RunE(itemShowCmd, []string{item.ID}) })
	entries := withTrace["conversation"].([]any)
	if len(entries) != 2 || entries[1].(map[string]any)["partial_trace"] == nil {
		t.Fatalf("opt-in conversation = %#v", entries)
	}
}

func TestItemJSONListIncludesActivitiesAndMentionFlags(t *testing.T) {
	if itemAddCmd.Flags().Lookup("mentions") == nil || itemSuggestCmd.Flags().Lookup("mentions") == nil {
		t.Fatal("item add and suggest must register --mentions")
	}
	if itemAddCmd.Flags().Lookup("related") != nil || itemSuggestCmd.Flags().Lookup("related") != nil {
		t.Fatal("legacy --related flag remains")
	}
	project := t.TempDir()
	s, err := store.NewStore(filepath.Join(project, ".ostraka"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(models.ChannelInbox, "Source", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddActivity(item.ID, models.Activity{Type: store.ActivityAgentSessionStarted, Result: "codex"}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	previous := listFlags
	t.Cleanup(func() { listFlags = previous })
	listFlags.asJSON = true
	listFlags.channel, listFlags.status, listFlags.group = "", "", ""
	data := captureCommandOutput(t, func() error { return itemListCmd.RunE(itemListCmd, nil) })
	var out []map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("invalid list JSON: %v: %s", err, data)
	}
	if len(out) != 1 {
		t.Fatalf("list = %#v", out)
	}
	conversation := out[0]["conversation"].([]any)
	if len(conversation) != 1 || conversation[0].(map[string]any)["activity"].(map[string]any)["provider"] != "codex" {
		t.Fatalf("list conversation = %#v", conversation)
	}
}

func TestMentionFlagsCreateTextMentions(t *testing.T) {
	project := t.TempDir()
	s, err := store.NewStore(filepath.Join(project, ".ostraka"))
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateItem(models.ChannelInbox, "Target", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	previousAdd, previousSuggest := addFlags, suggestFlags
	t.Cleanup(func() { addFlags, suggestFlags = previousAdd, previousSuggest })
	addFlags.channel = string(models.ChannelInbox)
	addFlags.title, addFlags.body = "Source", "body"
	addFlags.itype, addFlags.status = string(models.TypeThread), string(models.StatusActive)
	addFlags.parent, addFlags.group = "", ""
	addFlags.mentions = []string{target.ID}
	id := strings.TrimSpace(string(captureCommandOutput(t, func() error { return itemAddCmd.RunE(itemAddCmd, nil) })))
	added, err := s.GetItem(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(added.Mentions) != 1 || added.Mentions[0] != target.ID || !strings.Contains(added.Body, "@"+target.ID) {
		t.Fatalf("item add mention = %+v", added)
	}
	suggestFlags.channel = string(models.ChannelInbox)
	suggestFlags.title, suggestFlags.body, suggestFlags.mentions = "Proposal", "body", target.ID
	id = strings.TrimSpace(string(captureCommandOutput(t, func() error { return itemSuggestCmd.RunE(itemSuggestCmd, nil) })))
	suggested, err := s.GetItem(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(suggested.Mentions) != 1 || suggested.Mentions[0] != target.ID || !strings.Contains(suggested.Body, "@"+target.ID) {
		t.Fatalf("item suggest mention = %+v", suggested)
	}
}

func TestStoreForTUIInitializesAndContinues(t *testing.T) {
	t.Chdir(t.TempDir())

	originalRunTUI := runTUI
	t.Cleanup(func() { runTUI = originalRunTUI })
	var startedAt string
	runTUI = func(s *store.Store) error {
		startedAt = s.Root
		return nil
	}

	var output bytes.Buffer
	tuiCmd.SetIn(strings.NewReader("yes\n"))
	tuiCmd.SetOut(&output)
	t.Cleanup(func() {
		tuiCmd.SetIn(nil)
		tuiCmd.SetOut(nil)
	})

	if err := tuiCmd.RunE(tuiCmd, nil); err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(mustGetwd(t), ".ostraka")
	if startedAt != wantRoot {
		t.Fatalf("TUI started at root %q, want %q", startedAt, wantRoot)
	}
	if _, err := os.Stat(filepath.Join(wantRoot, "INBOX")); err != nil {
		t.Fatalf("initialised project missing INBOX: %v", err)
	}
	for _, want := range []string{"Initialize Ostraka", "initialised " + wantRoot} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("prompt output missing %q:\n%s", want, output.String())
		}
	}
}

func TestStoreForTUICancellationDoesNotInitialize(t *testing.T) {
	t.Chdir(t.TempDir())

	originalRunTUI := runTUI
	t.Cleanup(func() { runTUI = originalRunTUI })
	runTUI = func(*store.Store) error {
		t.Fatal("TUI started after initialization was cancelled")
		return nil
	}

	var output bytes.Buffer
	cmd := newTUICommandForTest(strings.NewReader("n\n"), &output)
	s, err := storeForTUI(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if s != nil {
		t.Fatalf("cancelled initialization returned store rooted at %q", s.Root)
	}
	if _, err := os.Stat(filepath.Join(mustGetwd(t), ".ostraka")); !os.IsNotExist(err) {
		t.Fatalf("cancelled initialization created project, stat error = %v", err)
	}
	if !strings.Contains(output.String(), "initialization cancelled") {
		t.Fatalf("cancellation output missing:\n%s", output.String())
	}
}

func TestStoreForTUIUsesExistingAncestorWithoutPrompt(t *testing.T) {
	root := t.TempDir()
	projectRoot := filepath.Join(root, ".ostraka")
	if _, err := store.NewStore(projectRoot); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "nested")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(child)

	var output bytes.Buffer
	cmd := newTUICommandForTest(strings.NewReader(""), &output)
	s, err := storeForTUI(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if s == nil || s.Root != projectRoot {
		t.Fatalf("store root = %v, want %q", s, projectRoot)
	}
	if output.Len() != 0 {
		t.Fatalf("unexpected initialization prompt:\n%s", output.String())
	}
}

func TestStoreForTUIReportsInitializationFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	root := filepath.Join(mustGetwd(t), ".ostraka")
	if err := os.WriteFile(root, []byte("not a directory"), 0644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	cmd := newTUICommandForTest(strings.NewReader("y\n"), &output)
	_, err := storeForTUI(cmd)
	if err == nil || !strings.Contains(err.Error(), "initialize Ostraka") {
		t.Fatalf("initialization error = %v, want initialization failure", err)
	}
}

func newTUICommandForTest(in *strings.Reader, out *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetIn(in)
	cmd.SetOut(out)
	return cmd
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return cwd
}
