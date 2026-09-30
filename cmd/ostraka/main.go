package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/prompt"
	"github.com/Speculative/ostraka/internal/store"
	"github.com/Speculative/ostraka/internal/tui"

	"github.com/spf13/cobra"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "ostraka",
	Short: "ostraka — structured inbox for coding agent sessions",
}

func init() {
	rootCmd.AddCommand(initCmd, tuiCmd, itemCmd, preambleCmd)
	itemCmd.AddCommand(itemAddCmd, itemSuggestCmd, itemListCmd, itemShowCmd, itemTurnCmd, itemStatusCmd, itemRenameCmd, itemRmCmd, itemReparentCmd, itemUnparentCmd, itemGroupCmd)
	rootCmd.AddCommand(projectCmd)
	projectCmd.AddCommand(projectInstructionsCmd, projectBriefCmd)
	projectInstructionsCmd.AddCommand(projectInstructionsShowCmd, projectInstructionsReplaceCmd)
	projectBriefCmd.AddCommand(projectBriefShowCmd, projectBriefReplaceCmd, projectBriefHistoryCmd)
	projectInstructionsReplaceCmd.Flags().Bool("content-stdin", false, "read complete replacement from standard input")
	projectBriefReplaceCmd.Flags().Bool("content-stdin", false, "read complete replacement from standard input")
	addItemAddFlags()
	addItemListFlags()
	addItemShowFlags()
	addItemTurnFlags()
	addItemRmFlags()
	addItemReparentFlags()
}

// ── ostraka preamble ────────────────────────────────────────────────────────

var preambleCmd = &cobra.Command{
	Use:   "preamble",
	Short: "Print the static agent orientation and reply guidance",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println(prompt.AgentOrientation(prompt.ReplyCommand(preambleProjectRoot(), "<item-id>")))
		return nil
	},
}

func preambleProjectRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	if root, err := store.FindRoot(cwd); err == nil {
		return filepath.Dir(root)
	}
	return cwd
}

// ── ostraka project ──────────────────────────────────────────────────────────

var projectCmd = &cobra.Command{Use: "project", Short: "Manage project context for new agent sessions"}
var projectInstructionsCmd = &cobra.Command{Use: "instructions", Short: "User-owned project instructions"}
var projectBriefCmd = &cobra.Command{Use: "brief", Short: "Agent-curated project brief"}

func contentFromStdin(cmd *cobra.Command) (string, error) {
	b, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(b)) == "" {
		return "", fmt.Errorf("content must not be blank")
	}
	return string(b), nil
}

var projectInstructionsShowCmd = &cobra.Command{
	Use: "show", Short: "Show user-owned project instructions",
	RunE: func(cmd *cobra.Command, args []string) error {
		content, err := mustStore().ProjectInstructions()
		if err != nil {
			return err
		}
		fmt.Println(content)
		return nil
	},
}
var projectInstructionsReplaceCmd = &cobra.Command{
	Use: "replace --content-stdin", Short: "Replace user-owned instructions from stdin",
	RunE: func(cmd *cobra.Command, args []string) error {
		content, err := contentFromStdin(cmd)
		if err != nil {
			return err
		}
		return replaceProjectInstructions(mustStore(), content)
	},
}
var projectBriefShowCmd = &cobra.Command{
	Use: "show", Short: "Show the current agent-curated brief",
	RunE: func(cmd *cobra.Command, args []string) error {
		content, err := mustStore().ProjectBrief()
		if err != nil {
			return err
		}
		fmt.Println(content)
		return nil
	},
}
var projectBriefReplaceCmd = &cobra.Command{
	Use: "replace --content-stdin", Short: "Replace the complete brief from stdin (maximum 6000 characters)",
	RunE: func(cmd *cobra.Command, args []string) error {
		content, err := contentFromStdin(cmd)
		if err != nil {
			return err
		}
		return replaceProjectBrief(mustStore(), content)
	},
}

func replaceProjectInstructions(s *store.Store, content string) error {
	if itemID := strings.TrimSpace(os.Getenv("OSTRAKA_AGENT_ITEM_ID")); itemID != "" {
		return s.ReplaceProjectInstructionsForItem(itemID, content)
	}
	return s.ReplaceProjectInstructions(content)
}

func replaceProjectBrief(s *store.Store, content string) error {
	if itemID := strings.TrimSpace(os.Getenv("OSTRAKA_AGENT_ITEM_ID")); itemID != "" {
		return s.ReplaceProjectBriefForItem(itemID, content)
	}
	return s.ReplaceProjectBrief(content)
}

var projectBriefHistoryCmd = &cobra.Command{
	Use: "history", Short: "List previous complete brief versions",
	RunE: func(cmd *cobra.Command, args []string) error {
		versions, err := mustStore().ProjectBriefHistory()
		if err != nil {
			return err
		}
		for _, v := range versions {
			fmt.Println(v.Name, v.Created.Format(time.RFC3339))
		}
		return nil
	},
}

// ── helpers ──────────────────────────────────────────────────────────────────

func mustStore() *store.Store {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	root, err := store.FindRoot(cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	s, err := store.NewStore(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	return s
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

// ── ostraka init ─────────────────────────────────────────────────────────────

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create .ostraka/ in the current directory",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		root := cwd + "/.ostraka"
		if _, err := os.Stat(root); err == nil {
			fmt.Fprintln(os.Stderr, "already initialised")
			os.Exit(1)
		}
		if _, err := store.NewStore(root); err != nil {
			return err
		}
		fmt.Println("initialised", root)
		return nil
	},
}

// ── ostraka tui ──────────────────────────────────────────────────────────────

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch the interactive TUI",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := storeForTUI(cmd)
		if err != nil || s == nil {
			return err
		}
		return runTUI(s)
	},
}

var runTUI = tui.Run

// storeForTUI finds the nearest existing project, or offers to initialise the
// current directory before the interactive program starts. Keeping the
// prompt outside Bubble Tea means a cancelled or failed initialisation never
// has to enter the alternate screen or create supervisor state.
func storeForTUI(cmd *cobra.Command) (*store.Store, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	root, err := store.FindRoot(cwd)
	if err == nil {
		return store.NewStore(root)
	}

	out := cmd.OutOrStdout()
	newRoot := filepath.Join(cwd, ".ostraka")
	fmt.Fprintf(out, "No .ostraka/ directory found in %s or any parent.\n", cwd)
	fmt.Fprintf(out, "Initialize Ostraka in %s and open the TUI? [y/N] ", newRoot)
	answer, readErr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	fmt.Fprintln(out)
	if readErr != nil && readErr != io.EOF {
		return nil, fmt.Errorf("read initialization choice: %w", readErr)
	}
	choice := strings.TrimSpace(answer)
	if !strings.EqualFold(choice, "y") && !strings.EqualFold(choice, "yes") {
		fmt.Fprintln(out, "initialization cancelled")
		return nil, nil
	}

	s, err := store.NewStore(newRoot)
	if err != nil {
		return nil, fmt.Errorf("initialize Ostraka in %s: %w", newRoot, err)
	}
	fmt.Fprintf(out, "initialised %s\n", newRoot)
	return s, nil
}

// ── ostraka item ─────────────────────────────────────────────────────────────

var itemCmd = &cobra.Command{
	Use:   "item",
	Short: "Create and manage items",
}

// ── item rename ─────────────────────────────────────────────────────────────

var itemRenameCmd = &cobra.Command{
	Use:   "rename <id> <new-title>",
	Short: "Rename an item",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		s := mustStore()
		if _, err := s.RenameItem(args[0], args[1]); err != nil {
			die(err)
		}
		return nil
	},
}

// ── item reparent ───────────────────────────────────────────────────────────

var reparentFlags struct {
	parent          string
	flattenChildren bool
}

var itemReparentCmd = &cobra.Command{
	Use:     "reparent <item-id> [<root-id>]",
	Aliases: []string{"move"},
	Short:   "Move an item under another root",
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 || len(args) > 2 {
			return fmt.Errorf("requires <item-id> and a destination root, either as an argument or with --parent")
		}
		if len(args) == 2 && reparentFlags.parent != "" {
			return fmt.Errorf("destination root supplied both as an argument and with --parent")
		}
		if len(args) == 1 && strings.TrimSpace(reparentFlags.parent) == "" {
			return fmt.Errorf("requires a destination root argument or --parent <root-id>")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		parent := reparentFlags.parent
		if len(args) == 2 {
			parent = args[1]
		}
		item, err := mustStore().ReparentItem(args[0], parent, reparentFlags.flattenChildren)
		if err != nil {
			return err
		}
		fmt.Println(item.ID)
		return nil
	},
}

var itemUnparentCmd = &cobra.Command{
	Use:   "unparent <item-id>",
	Short: "Promote a child to a top-level item",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		item, err := mustStore().UnparentItem(args[0])
		if err != nil {
			return err
		}
		fmt.Println(item.ID)
		return nil
	},
}

func addItemReparentFlags() {
	f := itemReparentCmd.Flags()
	f.StringVarP(&reparentFlags.parent, "parent", "p", "", "destination root item ID")
	f.BoolVar(&reparentFlags.flattenChildren, "flatten-children", false, "move the item's direct children as siblings under the destination root")
}

// ── item add ─────────────────────────────────────────────────────────────────

var addFlags struct {
	channel string
	title   string
	body    string
	itype   string
	status  string
	parent  string
	group   string
	related []string
}

func addItemAddFlags() {
	f := itemAddCmd.Flags()
	f.StringVarP(&addFlags.channel, "channel", "c", "", "inbox (required)")
	f.StringVar(&addFlags.title, "title", "", "single-line label for list views (required)")
	f.StringVar(&addFlags.body, "body", "", "opening description, any length (required)")
	f.StringVarP(&addFlags.itype, "type", "t", "thread", "thread|doc")
	f.StringVarP(&addFlags.status, "status", "s", "active", "backlog|active|pending-user|archived")
	f.StringVarP(&addFlags.parent, "parent", "p", "", "parent item ID")
	f.StringVar(&addFlags.group, "group", "", "lowercase group slug for a new root (or none)")
	f.StringSliceVar(&addFlags.related, "related", nil, "item IDs to mention (legacy flag name)")
	itemAddCmd.MarkFlagRequired("channel")
	itemAddCmd.MarkFlagRequired("title")
	itemAddCmd.MarkFlagRequired("body")
}

// Title and body are separate mandatory flags rather than one positional
// argument: when a single value served as both, callers passed whole reports
// as the label and the list view had to render them.
var itemAddCmd = &cobra.Command{
	Use:   "add --title <title> --body <body>",
	Short: "Create a new item and print its ID",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		status, err := userSettableStatus(addFlags.status)
		if err != nil {
			return err
		}
		s := mustStore()
		group, err := groupAssignment(addFlags.group)
		if err != nil {
			return err
		}
		for _, related := range addFlags.related {
			if _, err := s.GetItem(related); err != nil {
				return err
			}
		}
		var item models.Item
		if addFlags.parent != "" {
			item, err = s.CreateSubthread(addFlags.parent, addFlags.title, addFlags.body, models.ItemType(addFlags.itype), status)
		} else {
			item, err = s.CreateItemWithGroup(models.Channel(addFlags.channel), addFlags.title, addFlags.body, models.ItemType(addFlags.itype), status, "", group)
		}
		if err != nil {
			return err
		}
		for _, related := range addFlags.related {
			if _, err := s.AddMention(item.ID, related); err != nil {
				return err
			}
		}
		fmt.Println(item.ID)
		return nil
	},
}

var suggestFlags struct {
	channel string
	title   string
	body    string
	related string
}

var itemSuggestCmd = &cobra.Command{
	Use:   "suggest --title <title> --body <body> --related <id>",
	Short: "Create a proposed item related to existing work",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		s := mustStore()
		if _, err := s.GetItem(suggestFlags.related); err != nil {
			return err
		}
		item, err := s.CreateItem(models.Channel(suggestFlags.channel), suggestFlags.title, suggestFlags.body, models.TypeThread, models.StatusProposed, "")
		if err != nil {
			return err
		}
		if _, err := s.AddMention(item.ID, suggestFlags.related); err != nil {
			return err
		}
		fmt.Println(item.ID)
		return nil
	},
}

func init() {
	f := itemSuggestCmd.Flags()
	f.StringVarP(&suggestFlags.channel, "channel", "c", string(models.ChannelInbox), "inbox")
	f.StringVar(&suggestFlags.title, "title", "", "single-line label for list views (required)")
	f.StringVar(&suggestFlags.body, "body", "", "opening description (required)")
	f.StringVar(&suggestFlags.related, "related", "", "existing item ID to mention (required)")
	itemSuggestCmd.MarkFlagRequired("title")
	itemSuggestCmd.MarkFlagRequired("body")
	itemSuggestCmd.MarkFlagRequired("related")
}

// ── item list ────────────────────────────────────────────────────────────────

var listFlags struct {
	channel string
	status  string
	group   string
	asJSON  bool
}

func addItemListFlags() {
	f := itemListCmd.Flags()
	f.StringVarP(&listFlags.channel, "channel", "c", "", "filter by channel")
	f.StringVarP(&listFlags.status, "status", "s", "", "filter by status")
	f.StringVar(&listFlags.group, "group", "", "filter by group slug or none")
	f.BoolVar(&listFlags.asJSON, "json", false, "output as JSON")
}

var itemListCmd = &cobra.Command{
	Use:   "list",
	Short: "List items",
	RunE: func(cmd *cobra.Command, args []string) error {
		s := mustStore()
		opts := store.ListOpts{}
		if listFlags.channel != "" {
			ch := models.Channel(listFlags.channel)
			opts.Channel = &ch
		}
		if listFlags.status != "" {
			st := models.Status(listFlags.status)
			opts.Status = &st
		}
		if listFlags.group != "" {
			group, err := groupFilter(listFlags.group)
			if err != nil {
				return err
			}
			opts.Group = group
		}
		items, err := s.ListItems(opts)
		if err != nil {
			return err
		}
		if listFlags.asJSON {
			return json.NewEncoder(os.Stdout).Encode(itemsToJSON(items))
		}
		// Plain table
		fmt.Printf("%-22s  %-8s  %-15s  %-16s  %s  %5s  %s\n", "ID", "Ch", "Status", "Group", "T", "Turns", "Preview")
		fmt.Println(strings.Repeat("─", 112))
		for _, item := range items {
			title := item.Title
			if r := []rune(title); len(r) > 60 {
				title = string(r[:60]) + "…"
			}
			fmt.Printf("%-22s  %-8s  %-15s  %-16s  %s  %5d  %s\n",
				item.ID, item.Channel, item.Status, item.Group, string(item.Type[0]), len(item.Turns), title)
		}
		return nil
	},
}

// ── item show ────────────────────────────────────────────────────────────────

var showFlags struct {
	asJSON         bool
	includePartial bool
}

func addItemShowFlags() {
	itemShowCmd.Flags().BoolVar(&showFlags.asJSON, "json", false, "output as JSON")
	itemShowCmd.Flags().BoolVar(&showFlags.includePartial, "include-partial", false, "include retained provider partial traces")
}

var itemShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show an item and its conversation",
	Long: `Show an item and its complete conversation. JSON output includes the full
turns array. Retained provider partial traces are excluded unless
--include-partial is supplied. For long items, use jq to select only the
context you need; turn indexes are zero-based. A retained provider trace
(called a partial trace in the partial_traces JSON field) is provider progress
output captured during a dispatch, such as reasoning or tool activity; it is
not a posted conversation turn. It may be linked to a posted agent turn or be
standalone when a run ends before a final response.
In JSON, a non-zero turn_timestamp identifies the linked turn; a zero
turn_timestamp means the trace is standalone and no final agent turn was posted.

Examples:
  ostraka item show <item-id> --json | jq '.turns[-20:]'
  ostraka item show <item-id> --json | jq '[.turns[] | select(.actor == "user")]'
  ostraka item show <item-id> --include-partial --json | jq --argjson turn 42 '. as $item | ($item.turns[$turn].timestamp) as $ts | $item | .partial_traces = [.partial_traces[] | select(.turn_timestamp == $ts)]'`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s := mustStore()
		item, err := s.GetItem(args[0])
		if err != nil {
			die(err)
		}
		if backlinks, err := s.BacklinkItems(item.ID); err == nil {
			item.Backlinks = make([]string, len(backlinks))
			for i, backlink := range backlinks {
				item.Backlinks[i] = backlink.ID
			}
		}
		var partials []models.PartialTrace
		var partialErr error
		if showFlags.includePartial {
			partials, partialErr = s.ListPartialTraces(item.ID)
		}
		if showFlags.asJSON {
			if partialErr != nil {
				return partialErr
			}
			if showFlags.includePartial {
				return json.NewEncoder(os.Stdout).Encode(itemToJSONWithPartialTraces(item, partials))
			}
			return json.NewEncoder(os.Stdout).Encode(itemToJSON(item))
		}
		fmt.Printf("── %s ──\n", item.ID)
		fmt.Println(item.Title)
		fmt.Printf("channel: %s  type: %s  status: %s  created: %s\n",
			item.Channel, item.Type, item.Status, item.Created.Format(time.RFC3339))
		if item.Group != "" {
			fmt.Println("group:", item.Group)
		}
		if item.Parent != "" {
			fmt.Println("parent:", item.Parent)
		}
		if len(item.Related) > 0 {
			fmt.Println("related (legacy):", strings.Join(item.Related, ", "))
		}
		if len(item.Mentions) > 0 {
			fmt.Println("mentions:", strings.Join(item.Mentions, ", "))
		}
		if backlinks, err := s.BacklinkItems(item.ID); err == nil && len(backlinks) > 0 {
			ids := make([]string, len(backlinks))
			for i, backlink := range backlinks {
				ids[i] = backlink.ID
			}
			fmt.Println("backlinks:", strings.Join(ids, ", "))
		}
		fmt.Println()
		fmt.Println(item.Body)
		for _, turn := range item.Turns {
			fmt.Printf("\n── %s · %s ──\n%s\n", turn.Actor, turn.Timestamp.Format("2006-01-02 15:04:05"), turn.Content)
		}
		if partialErr == nil {
			for _, partial := range partials {
				status := partial.Status
				if status == "" {
					status = "retained"
				}
				fmt.Printf("\n── agent partial trace · %s · %s ──\n%s\n", status, partial.Timestamp.Format("2006-01-02 15:04:05"), partial.Content)
			}
		}
		if activities, err := s.ListActivities(item.ID); err == nil && len(activities) > 0 {
			fmt.Println("\n── activity ──")
			for _, activity := range activities {
				if activity.Type == store.ActivityAgentSessionStarted {
					fmt.Printf("%s new %s session started\n", activity.Timestamp.Format("2006-01-02 15:04:05"), store.AgentSessionLabel(activity))
					continue
				}
				detail := activity.ChildID
				if activity.ItemID != "" {
					detail = activity.ItemID
				}
				if activity.ItemTitle != "" {
					detail += " · " + activity.ItemTitle
				}
				if detail == "" && strings.HasPrefix(activity.Type, "project.") {
					detail = "this item"
				}
				if activity.Type == store.ActivitySubthreadMoved && activity.FromRootID != "" && activity.ToRootID != "" {
					detail += " (" + activity.FromRootID + " → " + activity.ToRootID + ")"
				}
				if activity.Type == store.ActivityItemRenamed && activity.PreviousTitle != "" && activity.ItemTitle != "" {
					detail += " (" + activity.PreviousTitle + " → " + activity.ItemTitle + ")"
				}
				fmt.Printf("%s %s %s [%s]\n", activity.Timestamp.Format("2006-01-02 15:04:05"), activity.Type, detail, activity.Result)
			}
		}
		return nil
	},
}

// ── item turn ────────────────────────────────────────────────────────────────

var turnFlags struct {
	actor        string
	contentStdin bool
}

func addItemTurnFlags() {
	itemTurnCmd.Flags().StringVarP(&turnFlags.actor, "actor", "a", "", "user|agent (required)")
	itemTurnCmd.Flags().BoolVar(&turnFlags.contentStdin, "content-stdin", false, "read turn content from standard input")
	itemTurnCmd.MarkFlagRequired("actor")
}

var itemTurnCmd = &cobra.Command{
	Use:   "turn <id> <content> | turn <id> --content-stdin",
	Short: "Append a turn to an item",
	Args: func(cmd *cobra.Command, args []string) error {
		if turnFlags.contentStdin {
			if len(args) != 1 {
				return fmt.Errorf("--content-stdin requires exactly one argument: <id>")
			}
			return nil
		}
		if len(args) != 2 {
			return fmt.Errorf("requires <id> and <content>, or <id> with --content-stdin")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		content, err := turnContent(cmd.InOrStdin(), args, turnFlags.contentStdin)
		if err != nil {
			return err
		}
		s := mustStore()
		if _, err := s.AddTurn(args[0], models.Actor(turnFlags.actor), content); err != nil {
			die(err)
		}
		return nil
	},
}

func turnContent(stdin io.Reader, args []string, contentStdin bool) (string, error) {
	var content string
	if contentStdin {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read turn content from stdin: %w", err)
		}
		content = string(data)
	} else {
		content = args[1]
	}
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("turn content must not be blank")
	}
	return content, nil
}

// ── item status ──────────────────────────────────────────────────────────────

var itemStatusCmd = &cobra.Command{
	Use:   "status <id> <status>",
	Short: "Change the status of an item",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		status, err := userSettableStatus(args[1])
		if err != nil {
			return err
		}
		s := mustStore()
		if _, err := s.SetStatus(args[0], status); err != nil {
			die(err)
		}
		return nil
	},
}

var itemGroupCmd = &cobra.Command{
	Use:   "group <item-id> <group|none>",
	Short: "Assign or clear the root group for an item family",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		group, err := groupAssignment(args[1])
		if err != nil {
			return err
		}
		item, err := mustStore().SetGroup(args[0], group)
		if err != nil {
			return err
		}
		fmt.Println(item.ID)
		return nil
	},
}

func groupAssignment(value string) (string, error) {
	if value == "none" {
		return "", nil
	}
	if err := store.ValidateGroup(value); err != nil {
		return "", err
	}
	return value, nil
}

func groupFilter(value string) (*string, error) {
	group, err := groupAssignment(value)
	if err != nil {
		return nil, err
	}
	return &group, nil
}

func userSettableStatus(value string) (models.Status, error) {
	status := models.Status(value)
	if !models.UserSettableStatus(status) {
		allowed := models.UserSettableStatuses()
		values := make([]string, len(allowed))
		for i, candidate := range allowed {
			values[i] = string(candidate)
		}
		return "", fmt.Errorf("status %q cannot be set by a user (choose %s)", value, strings.Join(values, ", "))
	}
	return status, nil
}

// ── item rm ──────────────────────────────────────────────────────────────────

var rmFlags struct{ yes bool }

func addItemRmFlags() {
	itemRmCmd.Flags().BoolVarP(&rmFlags.yes, "yes", "y", false, "skip confirmation")
}

var itemRmCmd = &cobra.Command{
	Use:   "rm <id>",
	Short: "Permanently delete an item",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s := mustStore()
		if _, err := s.GetItem(args[0]); err != nil {
			die(err)
		}
		if !rmFlags.yes {
			fmt.Printf("Permanently delete %s? [y/N] ", args[0])
			var ans string
			fmt.Scanln(&ans)
			if strings.ToLower(ans) != "y" {
				fmt.Println("aborted")
				return nil
			}
		}
		return s.DeleteItem(args[0])
	},
}

// ── JSON helpers ─────────────────────────────────────────────────────────────

func itemToJSON(item models.Item) map[string]any {
	turns := make([]map[string]any, len(item.Turns))
	for i, t := range item.Turns {
		turns[i] = map[string]any{
			"actor":     t.Actor,
			"timestamp": t.Timestamp.Format(time.RFC3339Nano),
			"content":   t.Content,
		}
	}
	return map[string]any{
		"id":        item.ID,
		"channel":   item.Channel,
		"type":      item.Type,
		"status":    item.Status,
		"created":   item.Created.Format(time.RFC3339Nano),
		"parent":    item.Parent,
		"group":     item.Group,
		"related":   item.Related,
		"mentions":  item.Mentions,
		"backlinks": item.Backlinks,
		"title":     item.Title,
		"body":      item.Body,
		"turns":     turns,
	}
}

func itemToJSONWithPartialTraces(item models.Item, partials []models.PartialTrace) map[string]any {
	out := itemToJSON(item)
	traces := make([]map[string]any, len(partials))
	for i, partial := range partials {
		traces[i] = map[string]any{
			"id":             partial.ID,
			"timestamp":      partial.Timestamp.Format(time.RFC3339Nano),
			"turn_timestamp": partial.TurnTimestamp.Format(time.RFC3339Nano),
			"status":         partial.Status,
			"content":        partial.Content,
		}
	}
	out["partial_traces"] = traces
	return out
}

func itemsToJSON(items []models.Item) []map[string]any {
	out := make([]map[string]any, len(items))
	for i, item := range items {
		out[i] = itemToJSON(item)
	}
	return out
}
