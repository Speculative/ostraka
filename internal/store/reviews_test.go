package store_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
)

func TestReviewThreadsPersistWithoutChangingItemScheduling(t *testing.T) {
	s := newTestStore(t)
	item, err := s.CreateItem(models.ChannelInbox, "review target", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.CreateReview(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	anchor := models.ReviewAnchor{File: "internal/work.go", Side: "new", StartLine: 8, EndLine: 9, Excerpt: "first()\nsecond()"}
	thread, err := s.CreateReviewThread(review.ID, &anchor, models.ActorUser, "Why does this branch return early?")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddReviewReply(review.ID, thread.Number, models.ActorAgent, "The guard handles the empty case."); err != nil {
		t.Fatal(err)
	}
	moved := models.ReviewAnchor{File: "internal/new.go", Side: "new", StartLine: 20, EndLine: 20, Excerpt: "return nil"}
	if _, err := s.AttachReviewThread(review.ID, thread.Number, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DetachReviewThread(review.ID, thread.Number); err != nil {
		t.Fatal(err)
	}
	gotItem, err := s.GetItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotItem.Status != models.StatusActive || len(gotItem.Turns) != 0 {
		t.Fatalf("thread operations changed item state: status=%s turns=%d", gotItem.Status, len(gotItem.Turns))
	}

	reopened, err := store.NewStore(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	gotReview, err := reopened.GetReview(review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotReview.Threads) != 1 || gotReview.Threads[0].Number != thread.Number {
		t.Fatalf("persisted threads = %#v", gotReview.Threads)
	}
	if gotReview.Threads[0].Anchor != nil {
		t.Fatalf("detached thread still has anchor: %#v", gotReview.Threads[0].Anchor)
	}
	if len(gotReview.Threads[0].Messages) != 4 {
		t.Fatalf("persisted message count = %d, want 4", len(gotReview.Threads[0].Messages))
	}
	if gotReview.Threads[0].Messages[0].Anchor == nil || gotReview.Threads[0].Messages[0].Anchor.Excerpt != anchor.Excerpt {
		t.Fatalf("original user excerpt was not retained: %#v", gotReview.Threads[0].Messages[0].Anchor)
	}
	if gotReview.NextThreadNumber != 2 {
		t.Fatalf("next thread number = %d, want 2", gotReview.NextThreadNumber)
	}
	second, err := reopened.CreateReviewThread(review.ID, nil, models.ActorUser, "A general question")
	if err != nil {
		t.Fatal(err)
	}
	if second.Number != 2 {
		t.Fatalf("next stable thread number = %d, want 2", second.Number)
	}
}

func TestSubmitReviewBatchCreatesOneFormattedUserTurn(t *testing.T) {
	s := newTestStore(t)
	item, err := s.CreateItem(models.ChannelInbox, "review target", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.CreateReview(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	anchor := models.ReviewAnchor{File: "main.go", Side: "old", StartLine: 4, EndLine: 4, Excerpt: "return false"}
	if _, err := s.CreateReviewThread(review.ID, &anchor, models.ActorUser, "Why is this removed?"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateReviewThread(review.ID, nil, models.ActorUser, "Please explain the overall approach."); err != nil {
		t.Fatal(err)
	}
	updated, err := s.SubmitReviewBatch(review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != models.StatusActive || len(updated.Turns) != 1 || updated.Turns[0].Actor != models.ActorUser {
		t.Fatalf("submitted item state: status=%s turns=%#v", updated.Status, updated.Turns)
	}
	for _, mention := range updated.Mentions {
		if mention == item.ID {
			t.Fatal("formatted review turn added a self-mention")
		}
	}
	turn := updated.Turns[0].Content
	for _, unwanted := range []string{"Inspect current threads", "ostraka review show", "ostraka review reply", "ostraka review attach", "ostraka review detach"} {
		if strings.Contains(turn, unwanted) {
			t.Errorf("submitted turn contains agent-only instructions %q:\n%s", unwanted, turn)
		}
	}
	for _, want := range []string{"## Review questions", "Thread #1", "Thread #2", "```go\nreturn false", "Why is this removed?", "Please explain the overall approach.", "ostraka-review-batch:"} {
		if !strings.Contains(turn, want) {
			t.Errorf("submitted turn missing %q:\n%s", want, turn)
		}
	}
	if _, err := s.SubmitReviewBatch(review.ID); err == nil {
		t.Fatal("second submit without new drafts unexpectedly succeeded")
	}
	again, err := s.GetItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Turns) != 1 {
		t.Fatalf("retry appended another item turn: got %d", len(again.Turns))
	}
	gotReview, err := s.GetReview(review.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, thread := range gotReview.Threads {
		if !thread.Messages[0].Submitted {
			t.Errorf("thread #%d user message not marked submitted", thread.Number)
		}
	}
}

func TestDraftMessagesCanBeEditedOrDeletedWithoutReusingThreadNumbers(t *testing.T) {
	s := newTestStore(t)
	item, err := s.CreateItem(models.ChannelInbox, "review target", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.CreateReview(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.CreateReviewThread(review.ID, nil, models.ActorUser, "Draft question")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := s.UpdateReviewDraft(review.ID, first.Number, first.Messages[0].ID, "Edited question")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Messages[0].Content != "Edited question" {
		t.Fatalf("updated draft = %q", updated.Messages[0].Content)
	}
	if _, err := s.DeleteReviewDraft(review.ID, first.Number, first.Messages[0].ID); err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateReviewThread(review.ID, nil, models.ActorUser, "Another question")
	if err != nil {
		t.Fatal(err)
	}
	if second.Number != first.Number+1 {
		t.Fatalf("thread number reused after deleting draft: first=%d second=%d", first.Number, second.Number)
	}
	got, err := s.GetItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != models.StatusActive || len(got.Turns) != 0 {
		t.Fatalf("draft edits changed item state: status=%s turns=%d", got.Status, len(got.Turns))
	}
}

func TestSubmitReviewBatchRecoversTurnWrittenBeforeReviewReceipt(t *testing.T) {
	s := newTestStore(t)
	item, err := s.CreateItem(models.ChannelInbox, "review target", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.CreateReview(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := s.CreateReviewThread(review.ID, nil, models.ActorUser, "Is this needed?")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetReview(review.ID)
	if err != nil {
		t.Fatal(err)
	}
	batchID := "batch-retry"
	loaded.Batches = []models.ReviewBatch{{ID: batchID, MessageIDs: []string{loaded.Threads[0].Messages[0].ID}, Content: "## Review questions\n\nQuestion", CreatedAt: loaded.CreatedAt}}
	if err := writeReviewFixture(s.Root, loaded); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTurn(item.ID, models.ActorUser, "Question\n\n<!-- ostraka-review-batch:"+review.ID+":"+batchID+" -->"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitReviewBatch(review.ID); err != nil {
		t.Fatal(err)
	}
	gotItem, err := s.GetItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotItem.Turns) != 1 {
		t.Fatalf("receipt recovery duplicated the item turn: %d", len(gotItem.Turns))
	}
	gotReview, err := s.GetReview(review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !gotReview.Batches[0].Submitted || !gotReview.Threads[0].Messages[0].Submitted {
		t.Fatalf("review receipt was not reconciled: %#v", gotReview)
	}
	if gotReview.Threads[0].Number != thread.Number {
		t.Fatalf("thread ID changed during retry: got %d want %d", gotReview.Threads[0].Number, thread.Number)
	}
}

func TestAgentReviewRepliesAreRecordedOnceInFinalItemTurn(t *testing.T) {
	s := newTestStore(t)
	item, err := s.CreateItem(models.ChannelInbox, "review target", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.CreateReview(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	anchor := models.ReviewAnchor{File: "main.go", Side: "new", StartLine: 9, EndLine: 10, Excerpt: "if ready {\n    return nil\n}"}
	thread, err := s.CreateReviewThread(review.ID, &anchor, models.ActorUser, "Can this handle nil?")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitReviewBatch(review.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddReviewReply(review.ID, thread.Number, models.ActorAgent, "The caller guarantees ready is non-nil."); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Status != models.StatusActive || len(before.Turns) != 1 {
		t.Fatalf("review reply changed item status or turns: %s, %d", before.Status, len(before.Turns))
	}

	// Reopening proves an interrupted dispatch's review reply survives without
	// having become an item turn.
	s, err = store.NewStore(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	item, err = s.AddAgentTurnWithReview(item.ID, "I finished the requested check.")
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Turns) != 2 || item.Turns[1].Actor != models.ActorAgent {
		t.Fatalf("final item turns = %#v", item.Turns)
	}
	turn := item.Turns[1].Content
	for _, want := range []string{"I finished the requested check.", "Review thread updates", "Thread #1", "Relevant code excerpt", "```go\n", anchor.Excerpt, "The caller guarantees ready is non-nil.", "ostraka-review-record:"} {
		if !strings.Contains(turn, want) {
			t.Errorf("final turn missing %q:\n%s", want, turn)
		}
	}
	if store.ReviewThreadNeedsReply(mustReviewThread(t, s, review.ID, thread.Number)) {
		t.Fatal("answered thread still needs a reply")
	}
	if _, err := s.AddAgentTurnWithReview(item.ID, "A later unrelated update."); err != nil {
		t.Fatal(err)
	}
	again, err := s.GetItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(again.Turns[1].Content, "The caller guarantees ready is non-nil.") != 1 || strings.Count(again.Turns[2].Content, "The caller guarantees ready is non-nil.") != 0 {
		t.Fatal("review reply was not included exactly once")
	}
}

func TestAgentReviewReceiptRecoversAfterTurnWrite(t *testing.T) {
	s := newTestStore(t)
	item, err := s.CreateItem(models.ChannelInbox, "review target", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.CreateReview(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := s.CreateReviewThread(review.ID, nil, models.ActorAgent, "The explanation requested by the user.")
	if err != nil {
		t.Fatal(err)
	}
	message := thread.Messages[0]
	if _, err := s.AddTurn(item.ID, models.ActorAgent, "Done.\n\n<!-- ostraka-review-record:"+review.ID+":"+message.ID+" -->"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddAgentTurnWithReview(item.ID, "Next normal answer."); err != nil {
		t.Fatal(err)
	}
	gotReview, err := s.GetReview(review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !gotReview.Threads[0].Messages[0].Recorded {
		t.Fatal("record marker was not reconciled to the review message")
	}
	gotItem, err := s.GetItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotItem.Turns) != 2 || strings.Contains(gotItem.Turns[1].Content, message.Content) {
		t.Fatalf("retry duplicated recorded review message: %#v", gotItem.Turns)
	}
}

func TestAttachmentMovementIsRecordedWithOldAndCurrentCode(t *testing.T) {
	s := newTestStore(t)
	item, err := s.CreateItem(models.ChannelInbox, "review target", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.CreateReview(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldAnchor := models.ReviewAnchor{File: "before.go", Side: "new", StartLine: 3, EndLine: 3, Excerpt: "old implementation"}
	thread, err := s.CreateReviewThread(review.ID, &oldAnchor, models.ActorUser, "This behavior looks wrong.")
	if err != nil {
		t.Fatal(err)
	}
	newAnchor := models.ReviewAnchor{File: "after.go", Side: "new", StartLine: 18, EndLine: 19, Excerpt: "new implementation\nreturn nil"}
	if _, err := s.AttachReviewThread(review.ID, thread.Number, newAnchor); err != nil {
		t.Fatal(err)
	}
	item, err = s.AddAgentTurnWithReview(item.ID, "I moved the comment to the updated code.")
	if err != nil {
		t.Fatal(err)
	}
	turn := item.Turns[len(item.Turns)-1].Content
	for _, want := range []string{"before.go", "old implementation", "after.go", "new implementation", "Moved attachment"} {
		if !strings.Contains(turn, want) {
			t.Errorf("attachment record missing %q:\n%s", want, turn)
		}
	}
}

func TestFinishReviewProtectsDraftsAndUnrecordedReplies(t *testing.T) {
	s := newTestStore(t)
	item, err := s.CreateItem(models.ChannelInbox, "review target", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.CreateReview(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := s.CreateReviewThread(review.ID, nil, models.ActorUser, "What is this branch for?")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishReview(review.ID); err == nil || !strings.Contains(err.Error(), "unsent user drafts") {
		t.Fatalf("finish with a draft error = %v", err)
	}
	if _, err := s.SubmitReviewBatch(review.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddReviewReply(review.ID, thread.Number, models.ActorAgent, "It explains the branch condition."); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishReview(review.ID); err == nil || !strings.Contains(err.Error(), "unrecorded agent replies") {
		t.Fatalf("finish with an unrecorded reply error = %v", err)
	}
	item, err = s.AddAgentTurnWithReview(item.ID, "Answered the review questions.")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishReview(review.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetReview(review.ID); err == nil {
		t.Fatal("finished review still exists")
	}
	gotItem, err := s.GetItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotItem.Turns[len(gotItem.Turns)-1].Content, "It explains the branch condition.") {
		t.Fatal("review discussion did not survive finish")
	}
}

func mustReviewThread(t *testing.T, s *store.Store, reviewID string, number int) models.ReviewThread {
	t.Helper()
	review, err := s.GetReview(reviewID)
	if err != nil {
		t.Fatal(err)
	}
	for _, thread := range review.Threads {
		if thread.Number == number {
			return thread
		}
	}
	t.Fatalf("review %s has no thread #%d", reviewID, number)
	return models.ReviewThread{}
}

func writeReviewFixture(root string, review models.Review) error {
	data, err := json.MarshalIndent(review, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "REVIEWS", review.ID+".json"), append(data, '\n'), 0644)
}

func TestReviewNeedsReplyUsesSubmissionTime(t *testing.T) {
	s := newTestStore(t)
	item, err := s.CreateItem(models.ChannelInbox, "target", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.CreateReview(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := s.CreateReviewThread(review.ID, nil, models.ActorUser, "First question")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitReviewBatch(review.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddReviewReply(review.ID, thread.Number, models.ActorUser, "Drafted follow-up"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddReviewReply(review.ID, thread.Number, models.ActorAgent, "Answer to first question"); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetReview(review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if store.ReviewThreadNeedsReply(current.Threads[0]) {
		t.Fatal("unsent draft must not need reply")
	}
	if _, err := s.SubmitReviewBatch(review.ID); err != nil {
		t.Fatal(err)
	}
	current, err = s.GetReview(review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !store.ReviewThreadNeedsReply(current.Threads[0]) {
		t.Fatal("newly submitted follow-up needs reply despite being drafted before the answer")
	}
	if _, err := s.AddReviewReply(review.ID, thread.Number, models.ActorAgent, "Answer to follow-up"); err != nil {
		t.Fatal(err)
	}
	current, err = s.GetReview(review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if store.ReviewThreadNeedsReply(current.Threads[0]) {
		t.Fatal("follow-up was answered")
	}
}
