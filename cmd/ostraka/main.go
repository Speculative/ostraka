package main

import (
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
	itemCmd.AddCommand(itemAddCmd, itemSuggestCmd, itemListCmd, itemShowCmd, itemTurnCmd, itemStatusCmd, itemRmCmd)
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
		return mustStore().ReplaceProjectInstructions(content)
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
		return mustStore().ReplaceProjectBrief(content)
	},
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
		s := mustStore()
		return tui.Run(s)
	},
}

// ── ostraka item ─────────────────────────────────────────────────────────────

var itemCmd = &cobra.Command{
	Use:   "item",
	Short: "Create and manage items",
}

// ── item add ─────────────────────────────────────────────────────────────────

var addFlags struct {
	channel string
	title   string
	body    string
	itype   string
	status  string
	parent  string
	related []string
}

func addItemAddFlags() {
	f := itemAddCmd.Flags()
	f.StringVarP(&addFlags.channel, "channel", "c", "", "inbox (required)")
	f.StringVar(&addFlags.title, "title", "", "single-line label for list views (required)")
	f.StringVar(&addFlags.body, "body", "", "opening description, any length (required)")
	f.StringVarP(&addFlags.itype, "type", "t", "thread", "thread|doc")
	f.StringVarP(&addFlags.status, "status", "s", "active", "backlog|active|pending-user|pending-agent|agent-acknowledged|proposed|done|archived")
	f.StringVarP(&addFlags.parent, "parent", "p", "", "parent item ID")
	f.StringSliceVar(&addFlags.related, "related", nil, "top-level item IDs to relate")
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
		s := mustStore()
		for _, related := range addFlags.related {
			if _, err := s.GetItem(related); err != nil {
				return err
			}
		}
		var item models.Item
		var err error
		if addFlags.parent != "" {
			item, err = s.CreateSubthread(addFlags.parent, addFlags.title, addFlags.body, models.ItemType(addFlags.itype), models.Status(addFlags.status))
		} else {
			item, err = s.CreateItem(models.Channel(addFlags.channel), addFlags.title, addFlags.body, models.ItemType(addFlags.itype), models.Status(addFlags.status), "")
		}
		if err != nil {
			return err
		}
		for _, related := range addFlags.related {
			if _, err := s.AddRelated(item.ID, related); err != nil {
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
	Short: "Create a proposed top-level item related to existing work",
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
		if _, err := s.AddRelated(item.ID, suggestFlags.related); err != nil {
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
	f.StringVar(&suggestFlags.related, "related", "", "existing top-level item ID (required)")
	itemSuggestCmd.MarkFlagRequired("title")
	itemSuggestCmd.MarkFlagRequired("body")
	itemSuggestCmd.MarkFlagRequired("related")
}

// ── item list ────────────────────────────────────────────────────────────────

var listFlags struct {
	channel string
	status  string
	asJSON  bool
}

func addItemListFlags() {
	f := itemListCmd.Flags()
	f.StringVarP(&listFlags.channel, "channel", "c", "", "filter by channel")
	f.StringVarP(&listFlags.status, "status", "s", "", "filter by status")
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
		items, err := s.ListItems(opts)
		if err != nil {
			return err
		}
		if listFlags.asJSON {
			return json.NewEncoder(os.Stdout).Encode(itemsToJSON(items))
		}
		// Plain table
		fmt.Printf("%-22s  %-8s  %-15s  %s  %5s  %s\n", "ID", "Ch", "Status", "T", "Turns", "Preview")
		fmt.Println(strings.Repeat("─", 90))
		for _, item := range items {
			title := item.Title
			if r := []rune(title); len(r) > 60 {
				title = string(r[:60]) + "…"
			}
			fmt.Printf("%-22s  %-8s  %-15s  %s  %5d  %s\n",
				item.ID, item.Channel, item.Status, string(item.Type[0]), len(item.Turns), title)
		}
		return nil
	},
}

// ── item show ────────────────────────────────────────────────────────────────

var showFlags struct{ asJSON bool }

func addItemShowFlags() {
	itemShowCmd.Flags().BoolVar(&showFlags.asJSON, "json", false, "output as JSON")
}

var itemShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show an item and its conversation",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s := mustStore()
		item, err := s.GetItem(args[0])
		if err != nil {
			die(err)
		}
		if showFlags.asJSON {
			return json.NewEncoder(os.Stdout).Encode(itemToJSON(item))
		}
		fmt.Printf("── %s ──\n", item.ID)
		fmt.Println(item.Title)
		fmt.Printf("channel: %s  type: %s  status: %s  created: %s\n",
			item.Channel, item.Type, item.Status, item.Created.Format(time.RFC3339))
		if item.Parent != "" {
			fmt.Println("parent:", item.Parent)
		}
		if len(item.Related) > 0 {
			fmt.Println("related:", strings.Join(item.Related, ", "))
		}
		fmt.Println()
		fmt.Println(item.Body)
		for _, turn := range item.Turns {
			fmt.Printf("\n── %s · %s ──\n%s\n", turn.Actor, turn.Timestamp.Format("2006-01-02 15:04:05"), turn.Content)
		}
		if activities, err := s.ListActivities(item.ID); err == nil && len(activities) > 0 {
			fmt.Println("\n── activity ──")
			for _, activity := range activities {
				fmt.Printf("%s %s %s [%s]\n", activity.Timestamp.Format("2006-01-02 15:04:05"), activity.Type, activity.ChildID, activity.Result)
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
		s := mustStore()
		if _, err := s.SetStatus(args[0], models.Status(args[1])); err != nil {
			die(err)
		}
		return nil
	},
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
		"id":      item.ID,
		"channel": item.Channel,
		"type":    item.Type,
		"status":  item.Status,
		"created": item.Created.Format(time.RFC3339Nano),
		"parent":  item.Parent,
		"related": item.Related,
		"title":   item.Title,
		"body":    item.Body,
		"turns":   turns,
	}
}

func itemsToJSON(items []models.Item) []map[string]any {
	out := make([]map[string]any, len(items))
	for i, item := range items {
		out[i] = itemToJSON(item)
	}
	return out
}
