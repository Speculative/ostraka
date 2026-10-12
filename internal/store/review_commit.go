package store

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/Speculative/ostraka/internal/models"
)

func (s *Store) reviewGit(args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", s.Root}, args...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// RequestReviewCommit posts user approval without deleting the review.
func (s *Store) RequestReviewCommit(reviewID string) (models.Item, error) {
	unlock, err := s.lockReviews()
	if err != nil {
		return models.Item{}, err
	}
	defer unlock()
	itemUnlock, err := s.lockItems()
	if err != nil {
		return models.Item{}, err
	}
	defer itemUnlock()
	review, err := s.loadReview(reviewID)
	if err != nil {
		return models.Item{}, err
	}
	item, err := s.GetItem(review.ItemID)
	if err != nil {
		return models.Item{}, err
	}
	if models.TerminalStatuses[item.Status] {
		return models.Item{}, fmt.Errorf("reopen the destination item before requesting a commit")
	}
	if len(unsentReviewMessages(review)) > 0 {
		return models.Item{}, fmt.Errorf("submit or discard unsent review drafts before requesting a commit")
	}
	if _, err := s.reviewGit("rev-parse", "--show-toplevel"); err != nil {
		return models.Item{}, err
	}
	if !review.CommitRequested {
		// An unborn repository has no HEAD yet; its first commit is valid.
		review.CommitBaseHead, _ = s.reviewGit("rev-parse", "--verify", "HEAD")
	}
	review.CommitRequested = true
	review.CompletionCommit = ""
	if err := s.saveReview(review); err != nil {
		return models.Item{}, err
	}
	content := fmt.Sprintf("LGTM, please commit the reviewed changes for review `%s`. Keep the review open if the commit fails or you need my intervention.\n\n<!-- ostraka-review-finish:%s -->", review.ID, review.ID)
	return s.addTurnLocked(review.ItemID, models.ActorUser, content)
}

// CompleteReviewCommit verifies a new commit and defers cleanup to the final
// agent turn, so pending replies are preserved before the review disappears.
func (s *Store) CompleteReviewCommit(reviewID, commit string) error {
	unlock, err := s.lockReviews()
	if err != nil {
		return err
	}
	defer unlock()
	review, err := s.loadReview(reviewID)
	if err != nil {
		return err
	}
	if !review.CommitRequested {
		return fmt.Errorf("review has no user commit request")
	}
	if len(unsentReviewMessages(review)) > 0 {
		return fmt.Errorf("review has unsent user drafts")
	}
	head, err := s.reviewGit("rev-parse", "--verify", "HEAD")
	if err != nil {
		return err
	}
	resolved, err := s.reviewGit("rev-parse", "--verify", "--end-of-options", commit+"^{commit}")
	if err != nil {
		return err
	}
	if resolved != head || head == review.CommitBaseHead {
		return fmt.Errorf("completion requires a new commit at HEAD")
	}
	if review.CommitBaseHead != "" {
		if _, err := s.reviewGit("merge-base", "--is-ancestor", review.CommitBaseHead, head); err != nil {
			return fmt.Errorf("completion commit must descend from the reviewed HEAD: %w", err)
		}
	}
	status, err := s.reviewGit("status", "--porcelain")
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("reviewed working tree still has uncommitted changes")
	}
	review.CompletionCommit = head
	return s.saveReview(review)
}
