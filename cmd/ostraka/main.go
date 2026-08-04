package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"ostraka/internal/models"
	"ostraka/internal/store"
	"ostraka/internal/tui"

	"github.com/spf13/cobra"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "ostraka",
	Short: "ostraka — structured inbox/asks/handoff for coding agent sessions",
}

func init() {
	rootCmd.AddCommand(initCmd, tuiCmd, itemCmd)
	itemCmd.AddCommand(itemAddCmd, itemListCmd, itemShowCmd, itemTurnCmd, itemStatusCmd, itemRmCmd)
	addItemAddFlags()
	addItemListFlags()
	addItemShowFlags()
	addItemTurnFlags()
	addItemRmFlags()
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
	itype   string
	status  string
	parent  string
}

func addItemAddFlags() {
	f := itemAddCmd.Flags()
	f.StringVarP(&addFlags.channel, "channel", "c", "", "inbox|asks|handoff (required)")
	f.StringVarP(&addFlags.itype, "type", "t", "thread", "thread|doc")
	f.StringVarP(&addFlags.status, "status", "s", "active", "backlog|active|pending-user|pending-agent|done|archived")
	f.StringVarP(&addFlags.parent, "parent", "p", "", "parent item ID")
	itemAddCmd.MarkFlagRequired("channel")
}

var itemAddCmd = &cobra.Command{
	Use:   "add <body>",
	Short: "Create a new item and print its ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s := mustStore()
		item, err := s.CreateItem(
			models.Channel(addFlags.channel),
			args[0],
			models.ItemType(addFlags.itype),
			models.Status(addFlags.status),
			addFlags.parent,
		)
		if err != nil {
			return err
		}
		fmt.Println(item.ID)
		return nil
	},
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
			preview := strings.ReplaceAll(item.Body, "\n", " ")
			if len(preview) > 60 {
				preview = preview[:60] + "…"
			}
			fmt.Printf("%-22s  %-8s  %-15s  %s  %5d  %s\n",
				item.ID, item.Channel, item.Status, string(item.Type[0]), len(item.Turns), preview)
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
		fmt.Printf("channel: %s  type: %s  status: %s  created: %s\n",
			item.Channel, item.Type, item.Status, item.Created.Format(time.RFC3339))
		if item.Parent != "" {
			fmt.Println("parent:", item.Parent)
		}
		fmt.Println()
		fmt.Println(item.Body)
		for _, turn := range item.Turns {
			fmt.Printf("\n── %s · %s ──\n%s\n", turn.Actor, turn.Timestamp.Format("2006-01-02 15:04:05"), turn.Content)
		}
		return nil
	},
}

// ── item turn ────────────────────────────────────────────────────────────────

var turnFlags struct{ actor string }

func addItemTurnFlags() {
	itemTurnCmd.Flags().StringVarP(&turnFlags.actor, "actor", "a", "", "user|agent (required)")
	itemTurnCmd.MarkFlagRequired("actor")
}

var itemTurnCmd = &cobra.Command{
	Use:   "turn <id> <content>",
	Short: "Append a turn to an item",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		s := mustStore()
		if _, err := s.AddTurn(args[0], models.Actor(turnFlags.actor), args[1]); err != nil {
			die(err)
		}
		return nil
	},
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
