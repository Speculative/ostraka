package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
	"github.com/spf13/cobra"
)

var reviewCmd = &cobra.Command{Use: "review", Short: "Manage active code review discussions"}
var reviewThreadCmd = &cobra.Command{Use: "thread", Short: "Manage review comment threads"}

var reviewCreateFlags struct{ item string }
var reviewFinishCommit string
var reviewThreadCreateFlags struct {
	content      string
	contentStdin bool
	file         string
	side         string
	start        int
	end          int
	excerpt      string
}
var reviewReplyFlags struct {
	content      string
	contentStdin bool
}
var reviewAttachFlags struct {
	file    string
	side    string
	start   int
	end     int
	excerpt string
}

func init() {
	reviewFinishCmd.Flags().StringVar(&reviewFinishCommit, "commit", "", "successful commit hash (agent completion)")
	reviewCmd.AddCommand(reviewListCmd, reviewShowCmd, reviewCreateCmd, reviewThreadCmd,
		reviewReplyCmd, reviewDetachCmd, reviewAttachCmd, reviewSubmitCmd, reviewFinishCmd)
	reviewThreadCmd.AddCommand(reviewThreadListCmd, reviewThreadShowCmd, reviewThreadCreateCmd)
	reviewCreateCmd.Flags().StringVar(&reviewCreateFlags.item, "item", "", "destination item ID; defaults to the current agent item")
	threadCreateFlags := reviewThreadCreateCmd.Flags()
	threadCreateFlags.StringVar(&reviewThreadCreateFlags.content, "content", "", "initial question or explanation")
	threadCreateFlags.BoolVar(&reviewThreadCreateFlags.contentStdin, "content-stdin", false, "read the initial message from standard input")
	threadCreateFlags.StringVar(&reviewThreadCreateFlags.file, "file", "", "anchor file path (omit for an item-wide thread)")
	threadCreateFlags.StringVar(&reviewThreadCreateFlags.side, "side", "", "diff side: old or new")
	threadCreateFlags.IntVar(&reviewThreadCreateFlags.start, "start", 0, "inclusive source line start")
	threadCreateFlags.IntVar(&reviewThreadCreateFlags.end, "end", 0, "inclusive source line end")
	threadCreateFlags.StringVar(&reviewThreadCreateFlags.excerpt, "excerpt", "", "selected code excerpt")
	reviewReplyCmd.Flags().StringVar(&reviewReplyFlags.content, "content", "", "reply text")
	reviewReplyCmd.Flags().BoolVar(&reviewReplyFlags.contentStdin, "content-stdin", false, "read reply text from standard input")
	attachFlags := reviewAttachCmd.Flags()
	attachFlags.StringVar(&reviewAttachFlags.file, "file", "", "anchor file path")
	attachFlags.StringVar(&reviewAttachFlags.side, "side", "", "diff side: old or new")
	attachFlags.IntVar(&reviewAttachFlags.start, "start", 0, "inclusive source line start")
	attachFlags.IntVar(&reviewAttachFlags.end, "end", 0, "inclusive source line end")
	attachFlags.StringVar(&reviewAttachFlags.excerpt, "excerpt", "", "selected code excerpt")
}

var reviewListCmd = &cobra.Command{
	Use:   "list [item-id]",
	Short: "List active reviews, optionally filtered by destination item",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		itemID := ""
		if len(args) == 1 {
			itemID = args[0]
		}
		if itemCommandActor() == models.ActorAgent {
			if itemID != "" && itemID != strings.TrimSpace(osAgentItemID()) {
				return fmt.Errorf("an agent may only list reviews for its dispatch item %s", strings.TrimSpace(osAgentItemID()))
			}
			itemID = strings.TrimSpace(osAgentItemID())
		}
		reviews, err := mustStore().ListReviews()
		if err != nil {
			return err
		}
		for _, review := range reviews {
			if itemID != "" && review.ItemID != itemID {
				continue
			}
			threads, needingReply := len(review.Threads), 0
			for _, thread := range review.Threads {
				if store.ReviewThreadNeedsReply(thread) {
					needingReply++
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s  item @%s  %d threads  %d needing reply\n", review.ID, review.ItemID, threads, needingReply)
		}
		return nil
	},
}

var reviewShowCmd = &cobra.Command{
	Use:   "show <review-id>",
	Short: "Show a review and all its threads",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		review, err := mustStore().GetReview(args[0])
		if err != nil {
			return err
		}
		if err := validateAgentReviewDestination(itemCommandActor(), review.ItemID); err != nil {
			return err
		}
		printReview(cmd.OutOrStdout(), review)
		return nil
	},
}

var reviewCreateCmd = &cobra.Command{
	Use:   "create [item-id]",
	Short: "Create an active review for one destination item",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		itemID := reviewCreateFlags.item
		if len(args) == 1 {
			if itemID != "" {
				return fmt.Errorf("give the destination item either as an argument or with --item")
			}
			itemID = args[0]
		}
		if itemCommandActor() == models.ActorAgent {
			current := strings.TrimSpace(osAgentItemID())
			if itemID == "" {
				itemID = current
			}
			if itemID != current {
				return fmt.Errorf("an agent may only create a review for its dispatch item %s", current)
			}
		}
		if itemID == "" {
			return fmt.Errorf("destination item is required")
		}
		review, err := mustStore().CreateReview(itemID)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), review.ID)
		return nil
	},
}

var reviewThreadListCmd = &cobra.Command{
	Use:   "list <review-id>",
	Short: "List threads in a review",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		review, err := mustStore().GetReview(args[0])
		if err != nil {
			return err
		}
		if err := validateAgentReviewDestination(itemCommandActor(), review.ItemID); err != nil {
			return err
		}
		for _, thread := range review.Threads {
			state := "answered"
			if store.ReviewThreadNeedsReply(thread) {
				state = "needs reply"
			}
			anchor := "item-wide"
			if thread.Anchor != nil {
				anchor = describeCLIAnchor(thread.Anchor)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "#%d  %s  %s  %d messages\n", thread.Number, state, anchor, len(thread.Messages))
		}
		return nil
	},
}

var reviewThreadShowCmd = &cobra.Command{
	Use:   "show <review-id> <thread-number>",
	Short: "Show a review thread and its discussion",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		review, err := mustStore().GetReview(args[0])
		if err != nil {
			return err
		}
		if err := validateAgentReviewDestination(itemCommandActor(), review.ItemID); err != nil {
			return err
		}
		number, err := parseReviewThreadNumber(args[1])
		if err != nil {
			return err
		}
		for _, thread := range review.Threads {
			if thread.Number == number {
				printReviewThread(cmd.OutOrStdout(), thread)
				return nil
			}
		}
		return fmt.Errorf("review %s has no thread #%d", review.ID, number)
	},
}

var reviewThreadCreateCmd = &cobra.Command{
	Use:   "create <review-id>",
	Short: "Start a thread with a question or requested explanation",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		content, err := reviewCommandContent(cmd.InOrStdin(), reviewThreadCreateFlags.content, reviewThreadCreateFlags.contentStdin)
		if err != nil {
			return err
		}
		anchor, err := reviewAnchorFromFlags(reviewThreadCreateFlags.file, reviewThreadCreateFlags.side,
			reviewThreadCreateFlags.start, reviewThreadCreateFlags.end, reviewThreadCreateFlags.excerpt)
		if err != nil {
			return err
		}
		s := mustStore()
		review, err := s.GetReview(args[0])
		if err != nil {
			return err
		}
		actor := itemCommandActor()
		if err := validateAgentReviewDestination(actor, review.ItemID); err != nil {
			return err
		}
		thread, err := s.CreateReviewThread(review.ID, anchor, actor, content)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "#%d\n", thread.Number)
		return nil
	},
}

var reviewReplyCmd = &cobra.Command{
	Use:   "reply <review-id> <thread-number>",
	Short: "Add a reply or user draft to a review thread",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		content, err := reviewCommandContent(cmd.InOrStdin(), reviewReplyFlags.content, reviewReplyFlags.contentStdin)
		if err != nil {
			return err
		}
		number, err := parseReviewThreadNumber(args[1])
		if err != nil {
			return err
		}
		s := mustStore()
		review, err := s.GetReview(args[0])
		if err != nil {
			return err
		}
		actor := itemCommandActor()
		if err := validateAgentReviewDestination(actor, review.ItemID); err != nil {
			return err
		}
		if _, err := s.AddReviewReply(review.ID, number, actor, content); err != nil {
			return err
		}
		return nil
	},
}

var reviewDetachCmd = &cobra.Command{
	Use:   "detach <review-id> <thread-number>",
	Short: "Detach a thread from its current code range",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		number, err := parseReviewThreadNumber(args[1])
		if err != nil {
			return err
		}
		s := mustStore()
		review, err := s.GetReview(args[0])
		if err != nil {
			return err
		}
		if itemCommandActor() != models.ActorAgent {
			return fmt.Errorf("only an agent dispatch can move review thread attachments")
		}
		if err := validateAgentReviewDestination(itemCommandActor(), review.ItemID); err != nil {
			return err
		}
		_, err = s.DetachReviewThread(review.ID, number)
		return err
	},
}

var reviewAttachCmd = &cobra.Command{
	Use:   "attach <review-id> <thread-number>",
	Short: "Attach or move a thread to a code range",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		number, err := parseReviewThreadNumber(args[1])
		if err != nil {
			return err
		}
		anchor, err := reviewAnchorFromFlags(reviewAttachFlags.file, reviewAttachFlags.side,
			reviewAttachFlags.start, reviewAttachFlags.end, reviewAttachFlags.excerpt)
		if err != nil {
			return err
		}
		if anchor == nil {
			return fmt.Errorf("file, side, start, end, and excerpt are required")
		}
		s := mustStore()
		review, err := s.GetReview(args[0])
		if err != nil {
			return err
		}
		if itemCommandActor() != models.ActorAgent {
			return fmt.Errorf("only an agent dispatch can move review thread attachments")
		}
		if err := validateAgentReviewDestination(itemCommandActor(), review.ItemID); err != nil {
			return err
		}
		_, err = s.AttachReviewThread(review.ID, number, *anchor)
		return err
	},
}

var reviewSubmitCmd = &cobra.Command{
	Use:   "submit <review-id>",
	Short: "Submit all current draft questions as one destination item turn",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if itemCommandActor() == models.ActorAgent {
			return fmt.Errorf("agents cannot submit user review batches")
		}
		item, err := mustStore().SubmitReviewBatch(args[0])
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "submitted review batch to @%s\n", item.ID)
		return nil
	},
}

var reviewFinishCmd = &cobra.Command{
	Use:   "finish <review-id>",
	Short: "Request a commit, or confirm successful agent commit completion",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s := mustStore()
		if itemCommandActor() == models.ActorAgent {
			review, err := s.GetReview(args[0])
			if err != nil {
				return err
			}
			if err := validateAgentReviewDestination(models.ActorAgent, review.ItemID); err != nil {
				return err
			}
			if reviewFinishCommit == "" {
				return fmt.Errorf("agent completion requires --commit <successful-commit-hash>")
			}
			if err := s.CompleteReviewCommit(args[0], reviewFinishCommit); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "commit verified; review closes with the final agent item turn")
			return nil
		}
		if reviewFinishCommit != "" {
			return fmt.Errorf("only an agent dispatch can confirm commit completion")
		}
		item, err := s.RequestReviewCommit(args[0])
		if err != nil {
			return err
		}
		if item.Mode == models.AgentModeChat {
			item, err = s.SetMode(item.ID, models.AgentModeCode)
			if err != nil {
				return err
			}
		}
		if !models.TerminalStatuses[item.Status] {
			if _, err := s.SetStatus(item.ID, models.StatusPendingAgent); err != nil {
				return err
			}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "commit requested on @%s; review remains open\n", item.ID)
		return nil
	},
}

func printReview(out io.Writer, review models.Review) {
	fmt.Fprintf(out, "Review %s → @%s\nCreated: %s\nThreads: %d\n", review.ID, review.ItemID, review.CreatedAt.Format("2006-01-02 15:04:05"), len(review.Threads))
	for _, thread := range review.Threads {
		printReviewThread(out, thread)
	}
}

func printReviewThread(out io.Writer, thread models.ReviewThread) {
	state := "answered"
	if store.ReviewThreadNeedsReply(thread) {
		state = "needs reply"
	}
	anchor := "item-wide"
	if thread.Anchor != nil {
		anchor = describeCLIAnchor(thread.Anchor)
	}
	fmt.Fprintf(out, "\nThread #%d (%s; %s)\n", thread.Number, state, anchor)
	for _, message := range thread.Messages {
		status := ""
		if message.Actor == models.ActorUser && !message.Submitted {
			status = " [draft]"
		}
		if message.Actor == models.ActorAgent && !message.Recorded {
			status = " [not in item turn yet]"
		}
		label := string(message.Actor)
		if message.Kind == "attachment" {
			label = "attachment update"
		}
		fmt.Fprintf(out, "\n%s%s:\n%s\n", label, status, message.Content)
		if message.Anchor != nil {
			fmt.Fprintf(out, "\nExcerpt (%s):\n%s\n", describeCLIAnchor(message.Anchor), message.Anchor.Excerpt)
		}
	}
}

func describeCLIAnchor(anchor *models.ReviewAnchor) string {
	if anchor == nil {
		return "item-wide"
	}
	return fmt.Sprintf("%s:%d-%d (%s)", anchor.File, anchor.StartLine, anchor.EndLine, anchor.Side)
}

func parseReviewThreadNumber(value string) (int, error) {
	number, err := strconv.Atoi(strings.TrimPrefix(value, "#"))
	if err != nil || number < 1 {
		return 0, fmt.Errorf("thread number must be a positive integer")
	}
	return number, nil
}

func reviewCommandContent(stdin io.Reader, content string, fromStdin bool) (string, error) {
	if fromStdin && strings.TrimSpace(content) != "" {
		return "", fmt.Errorf("use either --content or --content-stdin")
	}
	if fromStdin {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read review content from stdin: %w", err)
		}
		content = string(data)
	}
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("review content must not be blank")
	}
	return content, nil
}

func reviewAnchorFromFlags(file, side string, start, end int, excerpt string) (*models.ReviewAnchor, error) {
	if strings.TrimSpace(file) == "" {
		if side != "" || start != 0 || end != 0 || excerpt != "" {
			return nil, fmt.Errorf("--file is required when supplying an anchor side, range, or excerpt")
		}
		return nil, nil
	}
	anchor := &models.ReviewAnchor{File: file, Side: side, StartLine: start, EndLine: end, Excerpt: excerpt}
	if err := store.ValidateReviewAnchor(*anchor); err != nil {
		return nil, err
	}
	return anchor, nil
}

func validateAgentReviewDestination(actor models.Actor, itemID string) error {
	if actor == models.ActorAgent && strings.TrimSpace(osAgentItemID()) != itemID {
		return fmt.Errorf("agent review operations are limited to dispatch item %s", strings.TrimSpace(osAgentItemID()))
	}
	return nil
}

func osAgentItemID() string { return os.Getenv("OSTRAKA_AGENT_ITEM_ID") }
