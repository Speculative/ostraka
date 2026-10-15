package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
)

func TestReviewCLIBatchReplyFinalTurnAndFinish(t *testing.T) {
	project := t.TempDir()
	s, err := store.NewStore(project + "/.ostraka")
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(models.ChannelInbox, "review target", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.CreateReview(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	t.Setenv("OSTRAKA_AGENT_ITEM_ID", "")

	oldCreateFlags := reviewThreadCreateFlags
	oldReplyFlags := reviewReplyFlags
	oldTurnFlags := turnFlags
	t.Cleanup(func() {
		reviewThreadCreateFlags = oldCreateFlags
		reviewReplyFlags = oldReplyFlags
		turnFlags = oldTurnFlags
	})
	reviewThreadCreateFlags = struct {
		content      string
		contentStdin bool
		file         string
		side         string
		start        int
		end          int
		excerpt      string
	}{content: "Can this branch handle nil?", file: "main.go", side: "new", start: 12, end: 12, excerpt: "return value"}
	var created bytes.Buffer
	reviewThreadCreateCmd.SetOut(&created)
	if err := reviewThreadCreateCmd.RunE(reviewThreadCreateCmd, []string{review.ID}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(created.String()) != "#1" {
		t.Fatalf("created thread output = %q", created.String())
	}
	beforeSubmit, err := s.GetItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(beforeSubmit.Turns) != 0 || beforeSubmit.Status != models.StatusActive {
		t.Fatalf("draft thread changed item state: status=%s turns=%d", beforeSubmit.Status, len(beforeSubmit.Turns))
	}
	if err := reviewSubmitCmd.RunE(reviewSubmitCmd, []string{review.ID}); err != nil {
		t.Fatal(err)
	}

	t.Setenv("OSTRAKA_AGENT_ITEM_ID", item.ID)
	reviewReplyFlags = struct {
		content      string
		contentStdin bool
	}{content: "The caller checks this before entering the branch."}
	if err := reviewReplyCmd.RunE(reviewReplyCmd, []string{review.ID, "1"}); err != nil {
		t.Fatal(err)
	}
	afterReply, err := s.GetItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterReply.Turns) != 1 {
		t.Fatalf("thread reply posted an item turn: %d", len(afterReply.Turns))
	}
	turnFlags.actor = "agent"
	turnFlags.contentStdin = false
	if err := itemTurnCmd.RunE(itemTurnCmd, []string{item.ID, "Review response done."}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OSTRAKA_AGENT_ITEM_ID", "")
	if err := s.FinishReview(review.ID); err != nil {
		t.Fatal(err)
	}
	finishedItem, err := s.GetItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(finishedItem.Turns) != 2 || finishedItem.Turns[1].Actor != models.ActorAgent {
		t.Fatalf("durable item turns = %#v", finishedItem.Turns)
	}
	for _, want := range []string{"Review response done.", "The caller checks this before entering the branch.", "return value"} {
		if !strings.Contains(finishedItem.Turns[1].Content, want) {
			t.Errorf("final item turn missing %q:\n%s", want, finishedItem.Turns[1].Content)
		}
	}
	if out, err := exec.Command("git", "-C", project, "init", "--quiet").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	nextReview, err := s.CreateReview(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldFinish := reviewFinishCommit
	t.Cleanup(func() { reviewFinishCommit = oldFinish })
	reviewFinishCommit = ""
	if err := reviewFinishCmd.RunE(reviewFinishCmd, []string{nextReview.ID}); err != nil {
		t.Fatal(err)
	}
	requested, err := s.GetReview(nextReview.ID)
	if err != nil || !requested.CommitRequested {
		t.Fatal("CLI finish deleted review instead of requesting commit")
	}
	t.Setenv("OSTRAKA_AGENT_ITEM_ID", item.ID)
	if err := reviewFinishCmd.RunE(reviewFinishCmd, []string{nextReview.ID}); err == nil {
		t.Fatal("agent completion accepted without commit hash")
	}
	reviewFinishCommit = "HEAD"
	if err := reviewFinishCmd.RunE(reviewFinishCmd, []string{nextReview.ID}); err == nil {
		t.Fatal("unborn HEAD accepted as completed commit")
	}

}

func TestReviewCLIDismissIsUserOnly(t *testing.T) {
	project := t.TempDir()
	s, err := store.NewStore(project + "/.ostraka")
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(models.ChannelInbox, "target", "", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.CreateReview(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	t.Setenv("OSTRAKA_AGENT_ITEM_ID", item.ID)
	if err := reviewDismissCmd.RunE(reviewDismissCmd, []string{review.ID}); err == nil {
		t.Fatal("agent dismissed review")
	}
	if _, err := s.GetReview(review.ID); err != nil {
		t.Fatal("rejected dismissal removed review")
	}
	t.Setenv("OSTRAKA_AGENT_ITEM_ID", "")
	if err := reviewDismissCmd.RunE(reviewDismissCmd, []string{review.ID}); err != nil {
		t.Fatal(err)
	}
	if reviews, err := s.ListReviews(); err != nil || len(reviews) != 0 {
		t.Fatal("dismissed review still active")
	}
}
