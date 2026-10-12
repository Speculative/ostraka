package store_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Speculative/ostraka/internal/models"
)

func TestReviewCommitFailureKeepsDiscussionAndSuccessClosesAfterFinalTurn(t *testing.T) {
	s := newTestStore(t)
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", s.Root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "--quiet")
	git("config", "user.name", "Review Test")
	git("config", "user.email", "review@example.test")
	git("config", "commit.gpgsign", "false")
	git("config", "core.hooksPath", filepath.Join(s.Root, ".git", "hooks"))
	if err := os.WriteFile(filepath.Join(s.Root, ".gitignore"), []byte("*\n"), 0644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Root, "code.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git("add", "-f", ".gitignore", "code.txt")
	git("commit", "--quiet", "-m", "initial")
	base := git("rev-parse", "HEAD")
	item, err := s.CreateItem(models.ChannelInbox, "commit target", "", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.CreateReview(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := s.CreateReviewThread(review.ID, nil, models.ActorUser, "question")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestReviewCommit(review.ID); err == nil {
		t.Fatal("unsent drafts were approved")
	}
	if _, err := s.SubmitReviewBatch(review.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddReviewReply(review.ID, thread.Number, models.ActorAgent, "answer retained through commit"); err != nil {
		t.Fatal(err)
	}
	approval, err := s.RequestReviewCommit(review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(approval.Turns[len(approval.Turns)-1].Content, "LGTM, please commit") {
		t.Fatal("missing commit approval")
	}
	if err := s.CompleteReviewCommit(review.ID, base); err == nil {
		t.Fatal("old HEAD accepted as successful completion")
	}
	if err := os.WriteFile(path, []byte("after\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git("add", "code.txt")
	hook := filepath.Join(s.Root, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", s.Root, "commit", "-m", "blocked").Run(); err == nil {
		t.Fatal("hook did not block commit")
	}
	if err := s.CompleteReviewCommit(review.ID, "HEAD"); err == nil {
		t.Fatal("failed commit accepted")
	}
	if _, err := s.AddAgentTurnWithReview(item.ID, "Commit hook failed; need intervention."); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetReview(review.ID); err != nil {
		t.Fatal("failed commit removed review")
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	git("commit", "--quiet", "-m", "complete")
	commit := git("rev-parse", "HEAD")
	if err := os.WriteFile(path, []byte("later change\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteReviewCommit(review.ID, commit); err == nil {
		t.Fatal("dirty working tree accepted")
	}
	git("restore", "code.txt")
	if err := s.CompleteReviewCommit(review.ID, commit); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetReview(review.ID); err != nil {
		t.Fatal("review disappeared before final turn")
	}
	if _, err := s.AddReviewReply(review.ID, thread.Number, models.ActorAgent, "commit succeeded"); err != nil {
		t.Fatal(err)
	}
	final, err := s.AddAgentTurnWithReview(item.ID, "Committed.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetReview(review.ID); err == nil {
		t.Fatal("successful final turn did not close review")
	}
	if !strings.Contains(final.Turns[len(final.Turns)-1].Content, "commit succeeded") {
		t.Fatal("cleanup lost pending reply")
	}
}
