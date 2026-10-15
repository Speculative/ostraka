package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/reviewcode"
)

const reviewDir = "REVIEWS"

var reviewIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,79}$`)

type pendingReviewRecord struct {
	reviewIndex  int
	threadIndex  int
	messageIndex int
	message      models.ReviewMessage
	thread       models.ReviewThread
	review       models.Review
}

// ListReviews returns all active review objects. Finished reviews are removed
// after their discussion has been recorded in the destination item's turns.
func (s *Store) ListReviews() ([]models.Review, error) {
	unlock, err := s.lockReviews()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return s.listReviewsLocked()
}

// GetReview loads an active review by its review-scoped ID.
func (s *Store) GetReview(id string) (models.Review, error) {
	unlock, err := s.lockReviews()
	if err != nil {
		return models.Review{}, err
	}
	defer unlock()
	return s.loadReview(id)
}

// CreateReview creates an active discussion for one existing destination
// item. Review objects do not alter item turns, status, or scheduling.
func (s *Store) CreateReview(itemID string) (models.Review, error) {
	reviewUnlock, err := s.lockReviews()
	if err != nil {
		return models.Review{}, err
	}
	defer reviewUnlock()
	itemUnlock, err := s.lockItems()
	if err != nil {
		return models.Review{}, err
	}
	defer itemUnlock()
	if _, err := s.GetItem(itemID); err != nil {
		return models.Review{}, err
	}
	id, err := newReviewID()
	if err != nil {
		return models.Review{}, err
	}
	now := time.Now().UTC()
	review := models.Review{
		ID:               id,
		ItemID:           itemID,
		CreatedAt:        now,
		UpdatedAt:        now,
		NextThreadNumber: 1,
		Threads:          []models.ReviewThread{},
	}
	if err := s.saveReview(review); err != nil {
		return models.Review{}, err
	}
	return review, nil
}

// CreateReviewThread creates a stable numbered thread and its first message.
// A user-authored message remains a draft until SubmitReviewBatch; an
// agent-authored message is copied into the next final agent item turn.
func (s *Store) CreateReviewThread(reviewID string, anchor *models.ReviewAnchor, actor models.Actor, content string) (models.ReviewThread, error) {
	if err := validateReviewMessage(actor, content); err != nil {
		return models.ReviewThread{}, err
	}
	if anchor != nil {
		if err := ValidateReviewAnchor(*anchor); err != nil {
			return models.ReviewThread{}, err
		}
	}
	unlock, err := s.lockReviews()
	if err != nil {
		return models.ReviewThread{}, err
	}
	defer unlock()
	review, err := s.loadReview(reviewID)
	if err != nil {
		return models.ReviewThread{}, err
	}
	msg, err := newReviewMessage(actor, "comment", content, anchor)
	if err != nil {
		return models.ReviewThread{}, err
	}
	thread := models.ReviewThread{
		Number:   review.NextThreadNumber,
		Anchor:   cloneReviewAnchor(anchor),
		Messages: []models.ReviewMessage{msg},
	}
	review.NextThreadNumber++
	review.Threads = append(review.Threads, thread)
	if err := s.saveReview(review); err != nil {
		return models.ReviewThread{}, err
	}
	return thread, nil
}

// AddReviewReply appends a user draft or an agent reply to an existing thread.
// It deliberately does not post an Ostraka item turn or change item status.
func (s *Store) AddReviewReply(reviewID string, threadNumber int, actor models.Actor, content string) (models.ReviewThread, error) {
	if err := validateReviewMessage(actor, content); err != nil {
		return models.ReviewThread{}, err
	}
	unlock, err := s.lockReviews()
	if err != nil {
		return models.ReviewThread{}, err
	}
	defer unlock()
	review, err := s.loadReview(reviewID)
	if err != nil {
		return models.ReviewThread{}, err
	}
	index := reviewThreadIndex(review, threadNumber)
	if index < 0 {
		return models.ReviewThread{}, fmt.Errorf("review %s has no thread #%d", reviewID, threadNumber)
	}
	// Keep the code context as it stood when either side wrote the message.
	// An agent may detach or move the thread later in the same dispatch, but
	// the final item turn still needs the excerpt the reply addressed.
	anchor := review.Threads[index].Anchor
	msg, err := newReviewMessage(actor, "comment", content, anchor)
	if err != nil {
		return models.ReviewThread{}, err
	}
	review.Threads[index].Messages = append(review.Threads[index].Messages, msg)
	if err := s.saveReview(review); err != nil {
		return models.ReviewThread{}, err
	}
	return review.Threads[index], nil
}

// UpdateReviewDraft edits one unsent user message without touching the item
// turn or scheduling state. Its saved code excerpt and thread number stay
// unchanged.
func (s *Store) UpdateReviewDraft(reviewID string, threadNumber int, messageID, content string) (models.ReviewThread, error) {
	if strings.TrimSpace(content) == "" {
		return models.ReviewThread{}, fmt.Errorf("review message must not be blank")
	}
	unlock, err := s.lockReviews()
	if err != nil {
		return models.ReviewThread{}, err
	}
	defer unlock()
	review, err := s.loadReview(reviewID)
	if err != nil {
		return models.ReviewThread{}, err
	}
	threadIndex := reviewThreadIndex(review, threadNumber)
	if threadIndex < 0 {
		return models.ReviewThread{}, fmt.Errorf("review %s has no thread #%d", reviewID, threadNumber)
	}
	thread := &review.Threads[threadIndex]
	for i := range thread.Messages {
		message := &thread.Messages[i]
		if message.ID != messageID {
			continue
		}
		if message.Actor != models.ActorUser || message.Submitted {
			return models.ReviewThread{}, fmt.Errorf("review message %s is not an editable user draft", messageID)
		}
		message.Content = strings.TrimSpace(content)
		if err := s.saveReview(review); err != nil {
			return models.ReviewThread{}, err
		}
		return *thread, nil
	}
	return models.ReviewThread{}, fmt.Errorf("review %s thread #%d has no message %s", reviewID, threadNumber, messageID)
}

// DeleteReviewDraft removes one unsent user message. An empty new thread is
// removed as well, while the review's next thread number remains monotonic.
func (s *Store) DeleteReviewDraft(reviewID string, threadNumber int, messageID string) (models.Review, error) {
	unlock, err := s.lockReviews()
	if err != nil {
		return models.Review{}, err
	}
	defer unlock()
	review, err := s.loadReview(reviewID)
	if err != nil {
		return models.Review{}, err
	}
	threadIndex := reviewThreadIndex(review, threadNumber)
	if threadIndex < 0 {
		return models.Review{}, fmt.Errorf("review %s has no thread #%d", reviewID, threadNumber)
	}
	thread := &review.Threads[threadIndex]
	messageIndex := -1
	for i := range thread.Messages {
		if thread.Messages[i].ID == messageID {
			messageIndex = i
			break
		}
	}
	if messageIndex < 0 {
		return models.Review{}, fmt.Errorf("review %s thread #%d has no message %s", reviewID, threadNumber, messageID)
	}
	message := thread.Messages[messageIndex]
	if message.Actor != models.ActorUser || message.Submitted {
		return models.Review{}, fmt.Errorf("review message %s is not a deletable user draft", messageID)
	}
	thread.Messages = append(thread.Messages[:messageIndex], thread.Messages[messageIndex+1:]...)
	if len(thread.Messages) == 0 {
		review.Threads = append(review.Threads[:threadIndex], review.Threads[threadIndex+1:]...)
	}
	if err := s.saveReview(review); err != nil {
		return models.Review{}, err
	}
	return review, nil
}

// AttachReviewThread moves an active thread to a new code range and records an
// agent-authored attachment note for inclusion in the next final item turn.
func (s *Store) AttachReviewThread(reviewID string, threadNumber int, anchor models.ReviewAnchor) (models.ReviewThread, error) {
	if err := ValidateReviewAnchor(anchor); err != nil {
		return models.ReviewThread{}, err
	}
	return s.changeReviewAttachment(reviewID, threadNumber, &anchor)
}

// DetachReviewThread turns a code-attached thread into an item-wide thread and
// records the previous anchor for the next final agent item turn.
func (s *Store) DetachReviewThread(reviewID string, threadNumber int) (models.ReviewThread, error) {
	return s.changeReviewAttachment(reviewID, threadNumber, nil)
}

func (s *Store) changeReviewAttachment(reviewID string, threadNumber int, anchor *models.ReviewAnchor) (models.ReviewThread, error) {
	unlock, err := s.lockReviews()
	if err != nil {
		return models.ReviewThread{}, err
	}
	defer unlock()
	review, err := s.loadReview(reviewID)
	if err != nil {
		return models.ReviewThread{}, err
	}
	index := reviewThreadIndex(review, threadNumber)
	if index < 0 {
		return models.ReviewThread{}, fmt.Errorf("review %s has no thread #%d", reviewID, threadNumber)
	}
	thread := &review.Threads[index]
	if equalReviewAnchors(thread.Anchor, anchor) {
		return *thread, nil
	}
	old := cloneReviewAnchor(thread.Anchor)
	thread.Anchor = cloneReviewAnchor(anchor)
	content := "Detached from " + describeReviewAnchor(old) + "."
	if anchor != nil {
		if old == nil {
			content = "Attached to " + describeReviewAnchor(anchor) + "."
		} else {
			content = "Moved attachment from " + describeReviewAnchor(old) + " to " + describeReviewAnchor(anchor) + "."
		}
	}
	msg, err := newReviewMessage(models.ActorAgent, "attachment", content, old)
	if err != nil {
		return models.ReviewThread{}, err
	}
	thread.Messages = append(thread.Messages, msg)
	if err := s.saveReview(review); err != nil {
		return models.ReviewThread{}, err
	}
	return *thread, nil
}

// SubmitReviewBatch copies all currently unsent user messages into one
// formatted user turn on the review's destination item. A persisted batch ID
// and receipt marker make an interrupted submission safe to retry.
func (s *Store) SubmitReviewBatch(reviewID string) (models.Item, error) {
	reviewUnlock, err := s.lockReviews()
	if err != nil {
		return models.Item{}, err
	}
	defer reviewUnlock()
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
		return models.Item{}, fmt.Errorf("reopen the destination item before submitting a review batch")
	}
	// Complete a batch left between its item write and review-object update.
	// This invocation reports that completed batch and leaves any newer drafts
	// for an explicit subsequent submission.
	for i := range review.Batches {
		batch := &review.Batches[i]
		if batch.Submitted {
			continue
		}
		if turnHasReviewBatch(item, review.ID, batch.ID) {
			markReviewBatchSubmitted(&review, batch, time.Now().UTC())
			if err := s.saveReview(review); err != nil {
				return models.Item{}, err
			}
			return item, nil
		}
		turnContent := batch.Content + "\n\n" + reviewBatchReceipt(review.ID, batch.ID)
		item, err = s.addTurnLocked(item.ID, models.ActorUser, turnContent)
		if err != nil {
			return models.Item{}, err
		}
		markReviewBatchSubmitted(&review, batch, time.Now().UTC())
		if err := s.saveReview(review); err != nil {
			return models.Item{}, err
		}
		return item, nil
	}

	batchMessages := unsentReviewMessages(review)
	if len(batchMessages) == 0 {
		return models.Item{}, fmt.Errorf("review %s has no unsent user comments or questions", reviewID)
	}
	batchID, err := newReviewToken("batch")
	if err != nil {
		return models.Item{}, err
	}
	content := formatReviewBatch(review, batchID, batchMessages)
	batch := models.ReviewBatch{
		ID:         batchID,
		MessageIDs: batchMessages,
		Content:    content,
		CreatedAt:  time.Now().UTC(),
	}
	review.Batches = append(review.Batches, batch)
	if err := s.saveReview(review); err != nil {
		return models.Item{}, err
	}
	item, err = s.addTurnLocked(item.ID, models.ActorUser, content+"\n\n"+reviewBatchReceipt(review.ID, batchID))
	if err != nil {
		return models.Item{}, err
	}
	markReviewBatchSubmitted(&review, &review.Batches[len(review.Batches)-1], time.Now().UTC())
	if err := s.saveReview(review); err != nil {
		return models.Item{}, err
	}
	return item, nil
}

// AddAgentTurnWithReview appends a normal final agent item turn plus all
// pending review replies and attachment updates for the item. Receipt markers
// make review content exactly-once across retries or a crash after item write.
func (s *Store) AddAgentTurnWithReview(itemID, content string) (models.Item, error) {
	reviewUnlock, err := s.lockReviews()
	if err != nil {
		return models.Item{}, err
	}
	defer reviewUnlock()
	itemUnlock, err := s.lockItems()
	if err != nil {
		return models.Item{}, err
	}
	defer itemUnlock()

	item, err := s.GetItem(itemID)
	if err != nil {
		return models.Item{}, err
	}
	reviews, err := s.listReviewsLocked()
	if err != nil {
		return models.Item{}, err
	}
	var pending []pendingReviewRecord
	for ri := range reviews {
		review := &reviews[ri]
		if review.ItemID != itemID {
			continue
		}
		reconciled := false
		for ti := range review.Threads {
			thread := &review.Threads[ti]
			for mi := range thread.Messages {
				message := &thread.Messages[mi]
				if message.Actor != models.ActorAgent {
					continue
				}
				if !message.Recorded && itemHasReviewRecord(item, review.ID, message.ID) {
					message.Recorded = true
					reconciled = true
				}
				if !message.Recorded {
					pending = append(pending, pendingReviewRecord{ri, ti, mi, *message, *thread, *review})
				}
			}
		}
		if reconciled {
			if err := s.saveReview(*review); err != nil {
				return models.Item{}, err
			}
		}
	}
	turnContent := content
	if len(pending) > 0 {
		turnContent = joinTurnContent(turnContent, formatAgentReviewUpdates(pending))
	}
	if strings.TrimSpace(turnContent) == "" {
		return models.Item{}, fmt.Errorf("agent turn content must not be blank")
	}
	item, err = s.addTurnLocked(itemID, models.ActorAgent, turnContent)
	if err != nil {
		return models.Item{}, err
	}
	changed := make(map[int]bool)
	for _, record := range pending {
		reviews[record.reviewIndex].Threads[record.threadIndex].Messages[record.messageIndex].Recorded = true
		changed[record.reviewIndex] = true
	}
	for i := range reviews {
		if changed[i] {
			if err := s.saveReview(reviews[i]); err != nil {
				return models.Item{}, err
			}
		}
	}
	// Cleanup is deferred until the final turn has durably recorded replies.
	for _, review := range reviews {
		if review.ItemID != itemID || review.CompletionCommit == "" || len(unsentReviewMessages(review)) > 0 {
			continue
		}
		head, headErr := s.reviewGit("rev-parse", "--verify", "HEAD")
		status, statusErr := s.reviewGit("status", "--porcelain")
		if headErr == nil && statusErr == nil && head == review.CompletionCommit && status == "" {
			if err := os.Remove(s.reviewPath(review.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return item, err
			}
		}
	}
	return item, nil
}

// FinishReview removes an active review only after every user draft has been
// submitted and every agent reply or attachment update is recorded in an item
// turn. It reconciles receipt markers before making that decision.
func (s *Store) FinishReview(reviewID string) error {
	reviewUnlock, err := s.lockReviews()
	if err != nil {
		return err
	}
	defer reviewUnlock()
	itemUnlock, err := s.lockItems()
	if err != nil {
		return err
	}
	defer itemUnlock()

	review, err := s.loadReview(reviewID)
	if err != nil {
		return err
	}
	item, err := s.GetItem(review.ItemID)
	if err != nil {
		return err
	}
	if review.CommitRequested {
		return fmt.Errorf("commit-requested reviews close through verified commit completion and the final agent turn")
	}
	reconcileReviewReceipts(&review, item)
	if err := s.saveReview(review); err != nil {
		return err
	}
	var drafts, unrecorded []string
	for _, thread := range review.Threads {
		for _, message := range thread.Messages {
			if message.Actor == models.ActorUser && !message.Submitted {
				drafts = append(drafts, fmt.Sprintf("#%d", thread.Number))
			}
			if message.Actor == models.ActorAgent && !message.Recorded {
				unrecorded = append(unrecorded, fmt.Sprintf("#%d", thread.Number))
			}
		}
	}
	if len(drafts) > 0 || len(unrecorded) > 0 {
		parts := []string{}
		if len(drafts) > 0 {
			parts = append(parts, "unsent user drafts in "+strings.Join(uniqueStrings(drafts), ", "))
		}
		if len(unrecorded) > 0 {
			parts = append(parts, "unrecorded agent replies in "+strings.Join(uniqueStrings(unrecorded), ", "))
		}
		return fmt.Errorf("cannot finish review %s: %s", reviewID, strings.Join(parts, "; "))
	}
	if err := os.Remove(s.reviewPath(reviewID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ReviewThreadNeedsReply reports whether the latest submitted discussion in a
// thread is a user message that has not yet been answered by an agent message.
func ReviewThreadNeedsReply(thread models.ReviewThread) bool {
	needsReply := false
	var latest time.Time
	for _, message := range thread.Messages {
		at := message.CreatedAt
		if message.Actor == models.ActorUser && !message.SubmittedAt.IsZero() {
			at = message.SubmittedAt
		}
		if at.Before(latest) {
			continue
		}
		if message.Actor == models.ActorUser && message.Submitted {
			needsReply = true
			latest = at
		} else if message.Actor == models.ActorAgent && message.Kind != "attachment" {
			needsReply = false
			latest = at
		}
	}
	return needsReply
}

// ValidateReviewAnchor validates a file/side/source-range attachment.
func ValidateReviewAnchor(anchor models.ReviewAnchor) error {
	if strings.TrimSpace(anchor.File) == "" {
		return fmt.Errorf("review anchor file must not be blank")
	}
	if anchor.Side != "old" && anchor.Side != "new" {
		return fmt.Errorf("review anchor side must be old or new")
	}
	if anchor.StartLine < 1 || anchor.EndLine < anchor.StartLine {
		return fmt.Errorf("review anchor line range must be a positive inclusive range")
	}
	if strings.TrimSpace(anchor.Excerpt) == "" {
		return fmt.Errorf("review anchor excerpt must not be blank")
	}
	return nil
}

func (s *Store) reviewPath(id string) string { return filepath.Join(s.Root, reviewDir, id+".json") }

func (s *Store) loadReview(id string) (models.Review, error) {
	if !reviewIDRE.MatchString(id) {
		return models.Review{}, fmt.Errorf("invalid review ID %q", id)
	}
	data, err := os.ReadFile(s.reviewPath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return models.Review{}, fmt.Errorf("review %q not found", id)
		}
		return models.Review{}, err
	}
	var review models.Review
	if err := json.Unmarshal(data, &review); err != nil {
		return models.Review{}, fmt.Errorf("parse review %s: %w", id, err)
	}
	if review.ID != id || strings.TrimSpace(review.ItemID) == "" {
		return models.Review{}, fmt.Errorf("review %s has invalid identity", id)
	}
	return review, nil
}

func (s *Store) saveReview(review models.Review) error {
	if !reviewIDRE.MatchString(review.ID) || strings.TrimSpace(review.ItemID) == "" {
		return fmt.Errorf("review has invalid identity")
	}
	if err := os.MkdirAll(filepath.Join(s.Root, reviewDir), 0755); err != nil {
		return err
	}
	review.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(review, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.reviewPath(review.ID), append(data, '\n'))
}

func (s *Store) listReviewsLocked() ([]models.Review, error) {
	entries, err := os.ReadDir(filepath.Join(s.Root, reviewDir))
	if err != nil {
		return nil, err
	}
	var reviews []models.Review
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		review, err := s.loadReview(id)
		if err != nil {
			return nil, err
		}
		reviews = append(reviews, review)
	}
	sort.Slice(reviews, func(i, j int) bool {
		if reviews[i].CreatedAt.Equal(reviews[j].CreatedAt) {
			return reviews[i].ID < reviews[j].ID
		}
		return reviews[i].CreatedAt.Before(reviews[j].CreatedAt)
	})
	return reviews, nil
}

func reviewThreadIndex(review models.Review, number int) int {
	for i := range review.Threads {
		if review.Threads[i].Number == number {
			return i
		}
	}
	return -1
}

func newReviewMessage(actor models.Actor, kind, content string, anchor *models.ReviewAnchor) (models.ReviewMessage, error) {
	id, err := newReviewToken("msg")
	if err != nil {
		return models.ReviewMessage{}, err
	}
	return models.ReviewMessage{
		ID:        id,
		Actor:     actor,
		Kind:      kind,
		Content:   strings.TrimSpace(content),
		Anchor:    cloneReviewAnchor(anchor),
		CreatedAt: time.Now().UTC(),
	}, nil
}

func validateReviewMessage(actor models.Actor, content string) error {
	if actor != models.ActorUser && actor != models.ActorAgent {
		return fmt.Errorf("review message actor must be user or agent")
	}
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("review message must not be blank")
	}
	return nil
}

func cloneReviewAnchor(anchor *models.ReviewAnchor) *models.ReviewAnchor {
	if anchor == nil {
		return nil
	}
	copy := *anchor
	return &copy
}

func equalReviewAnchors(a, b *models.ReviewAnchor) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func describeReviewAnchor(anchor *models.ReviewAnchor) string {
	if anchor == nil {
		return "the item-wide discussion"
	}
	return fmt.Sprintf("%s (%s side, lines %d-%d)", anchor.File, anchor.Side, anchor.StartLine, anchor.EndLine)
}

func newReviewID() (string, error) { return newReviewToken("rvw") }

func newReviewToken(prefix string) (string, error) {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(b[:]), nil
}

func unsentReviewMessages(review models.Review) []string {
	var ids []string
	for _, thread := range review.Threads {
		for _, message := range thread.Messages {
			if message.Actor == models.ActorUser && !message.Submitted {
				ids = append(ids, message.ID)
			}
		}
	}
	return ids
}

func formatReviewBatch(review models.Review, batchID string, messageIDs []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Review questions\n\nReview `%s` submitted to destination item `%s`. Batch `%s`.\n", review.ID, review.ItemID, batchID)
	for _, thread := range review.Threads {
		for _, message := range thread.Messages {
			if !containsString(messageIDs, message.ID) {
				continue
			}
			fmt.Fprintf(&b, "\n### Thread #%d", thread.Number)
			anchor := message.Anchor
			if anchor != nil {
				fmt.Fprintf(&b, " · %s (%s side, lines %d-%d)", anchor.File, anchor.Side, anchor.StartLine, anchor.EndLine)
			}
			b.WriteString("\n")
			if anchor != nil {
				b.WriteString("\nSelected code:\n")
				b.WriteString(reviewcode.Block(anchor.File, anchor.Excerpt))
			}
			b.WriteString("\nQuestion/comment:\n")
			b.WriteString(message.Content)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func markReviewBatchSubmitted(review *models.Review, batch *models.ReviewBatch, submittedAt time.Time) {
	messageIDs := make(map[string]bool, len(batch.MessageIDs))
	for _, id := range batch.MessageIDs {
		messageIDs[id] = true
	}
	for ti := range review.Threads {
		for mi := range review.Threads[ti].Messages {
			message := &review.Threads[ti].Messages[mi]
			if messageIDs[message.ID] {
				message.Submitted = true
				// Use the persisted batch's creation time so receipt recovery
				// does not make an already answered question look new again.
				message.SubmittedAt = batch.CreatedAt
			}
		}
	}
	batch.Submitted = true
	batch.SubmittedAt = submittedAt
}

func reviewBatchReceipt(reviewID, batchID string) string {
	return "<!-- ostraka-review-batch:" + reviewID + ":" + batchID + " -->"
}

func turnHasReviewBatch(item models.Item, reviewID, batchID string) bool {
	marker := reviewBatchReceipt(reviewID, batchID)
	for _, turn := range item.Turns {
		if turn.Actor == models.ActorUser && strings.Contains(turn.Content, marker) {
			return true
		}
	}
	return false
}

func agentReviewReceipt(reviewID, messageID string) string {
	return "<!-- ostraka-review-record:" + reviewID + ":" + messageID + " -->"
}

func itemHasReviewRecord(item models.Item, reviewID, messageID string) bool {
	marker := agentReviewReceipt(reviewID, messageID)
	for _, turn := range item.Turns {
		if turn.Actor == models.ActorAgent && strings.Contains(turn.Content, marker) {
			return true
		}
	}
	return false
}

func reconcileReviewReceipts(review *models.Review, item models.Item) {
	for bi := range review.Batches {
		batch := &review.Batches[bi]
		if !batch.Submitted && turnHasReviewBatch(item, review.ID, batch.ID) {
			markReviewBatchSubmitted(review, batch, time.Now().UTC())
		}
	}
	for ti := range review.Threads {
		for mi := range review.Threads[ti].Messages {
			message := &review.Threads[ti].Messages[mi]
			if message.Actor == models.ActorAgent && !message.Recorded && itemHasReviewRecord(item, review.ID, message.ID) {
				message.Recorded = true
			}
		}
	}
}

func formatAgentReviewUpdates(records []pendingReviewRecord) string {
	var b strings.Builder
	b.WriteString("## Review thread updates\n")
	for _, record := range records {
		fmt.Fprintf(&b, "\n### Review `%s` · Thread #%d", record.review.ID, record.thread.Number)
		anchor := record.message.Anchor
		if anchor == nil || (record.message.Kind == "attachment" && record.thread.Anchor != nil) {
			anchor = record.thread.Anchor
		}
		if record.message.Kind == "attachment" && record.message.Anchor != nil && record.thread.Anchor != nil && !equalReviewAnchors(record.message.Anchor, record.thread.Anchor) {
			fmt.Fprintf(&b, " · %s → %s", describeReviewAnchor(record.message.Anchor), describeReviewAnchor(record.thread.Anchor))
		} else if anchor != nil {
			fmt.Fprintf(&b, " · %s", describeReviewAnchor(anchor))
		}
		b.WriteString("\n")
		if record.message.Kind == "attachment" {
			if record.message.Anchor != nil {
				writeReviewExcerpt(&b, "Previous code", record.message.Anchor)
			}
			if record.thread.Anchor != nil && !equalReviewAnchors(record.message.Anchor, record.thread.Anchor) {
				writeReviewExcerpt(&b, "Current attachment", record.thread.Anchor)
			}
		} else if anchor != nil {
			writeReviewExcerpt(&b, "Relevant code excerpt", anchor)
		}
		if record.message.Kind == "attachment" {
			b.WriteString("\nAttachment update: ")
		} else {
			b.WriteString("\nAgent reply: ")
		}
		b.WriteString(record.message.Content)
		b.WriteString("\n\n")
		b.WriteString(agentReviewReceipt(record.review.ID, record.message.ID))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func writeReviewExcerpt(b *strings.Builder, label string, anchor *models.ReviewAnchor) {
	fmt.Fprintf(b, "\n%s (%s):\n", label, describeReviewAnchor(anchor))
	b.WriteString(reviewcode.Block(anchor.File, anchor.Excerpt))
}

func joinTurnContent(content, section string) string {
	if strings.TrimSpace(content) == "" {
		return section
	}
	return strings.TrimRight(content, "\n") + "\n\n" + section
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			unique = append(unique, value)
		}
	}
	return unique
}

// DismissReview removes a review from active routing while retaining all its
// discussion, drafts, and pending replies in the dismissed directory. It does
// not require commit completion or change the destination item's state.
func (s *Store) DismissReview(reviewID string) error {
	unlock, err := s.lockReviews()
	if err != nil {
		return err
	}
	defer unlock()
	review, err := s.loadReview(reviewID)
	if err != nil {
		return err
	}
	directory := filepath.Join(s.Root, reviewDir, "dismissed")
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	return os.Rename(s.reviewPath(review.ID), filepath.Join(directory, review.ID+".json"))
}
