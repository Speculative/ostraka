package tui

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/reviewdiff"
	"github.com/Speculative/ostraka/internal/store"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestReviewQuestionComposerFollowupsAndSubmitsOneBatch(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateItem(models.ChannelInbox, "review target", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	sup := &fakeSupervisor{}
	m := newModel(s, nil, sup)
	m.width, m.height = 110, 30
	m.projectPane = reviewProjectPane
	m.items = []models.Item{target}
	m.allItems = []models.Item{target}
	m.projectEntries = []projectEntry{{title: "change.go"}}
	m.reviewDiff = reviewdiff.Diff{Files: []reviewdiff.File{{
		Path: "change.go", Status: "modified",
		Lines: []reviewdiff.Line{{Text: "return true", Kind: '+', NewLine: 12}},
	}}}
	m.reviewAnchor, m.reviewCursor, m.reviewSide = 0, 0, "new"
	m.conv.Width, m.conv.Height = 75, 14
	m.updateConv()

	// The first question asks for a destination through the ordinary @ picker
	// and captures the selected source range before the composer opens.
	next, _ := m.beginReviewQuestion(false)
	m = next.(model)
	if !m.reviewComposer.selectingDestination || !m.mentionPicker.open {
		t.Fatalf("first question did not open destination picker: %#v", m.reviewComposer)
	}
	next, _ = m.chooseMention()
	m = next.(model)
	m.input.SetValue("Why is this branch needed?")
	next, _ = m.saveReviewComposer()
	m = next.(model)
	if m.mode != modeNav || m.reviewID == "" {
		t.Fatalf("saved draft did not return to review navigation: mode=%v review=%q", m.mode, m.reviewID)
	}

	// A follow-up stays in its thread without an edit-draft interface.
	m.openReviewThreads()
	next, _ = m.replyToReviewThread()
	m = next.(model)
	if m.mode != modeCompose || m.reviewComposer.threadNumber != 1 {
		t.Fatal("follow-up did not open on the existing thread")
	}
	m.input.SetValue("Please explain its caller too.")
	next, _ = m.saveReviewComposer()
	m = next.(model)
	m.closeReviewThreads()
	m.reviewAnchor = -1
	next, _ = m.beginReviewQuestion(false)
	m = next.(model)
	if m.reviewComposer.anchor != nil {
		t.Fatalf("second question should be item-wide, got anchor %#v", m.reviewComposer.anchor)
	}
	m.input.SetValue("Please also summarize the caller impact.")
	next, _ = m.saveReviewComposer()
	m = next.(model)

	next, _ = m.submitReviewBatch()
	m = next.(model)
	updated, err := s.GetItem(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Turns) != 1 || updated.Turns[0].Actor != models.ActorUser {
		t.Fatalf("batch created turns %#v, want exactly one user turn", updated.Turns)
	}
	for _, want := range []string{"Why is this branch needed?", "Please explain its caller too.", "Please also summarize the caller impact.", "change.go (new side, lines 12-12)", "return true"} {
		if !strings.Contains(updated.Turns[0].Content, want) {
			t.Errorf("formatted batch turn %q does not contain %q", updated.Turns[0].Content, want)
		}
	}
	if updated.Status != models.StatusPendingAgent {
		t.Fatalf("destination status = %q, want pending-agent", updated.Status)
	}
	if len(sup.enqueued) != 1 || sup.enqueued[0] != target.ID {
		t.Fatalf("queued items = %#v, want one dispatch for %s", sup.enqueued, target.ID)
	}
	reviews, err := s.ListReviews()
	if err != nil || len(reviews) != 1 {
		t.Fatalf("active reviews = %d, err=%v; want one", len(reviews), err)
	}
	for _, thread := range reviews[0].Threads {
		for _, message := range thread.Messages {
			if message.Actor == models.ActorUser && !message.Submitted {
				t.Errorf("question #%d remains an unsent draft", thread.Number)
			}
		}
	}
}

func TestReviewFooterExposesQuestionAndThreadControls(t *testing.T) {
	m := newModel(nil, nil, nil)
	m.width, m.height = 140, 30
	m.projectPane = reviewProjectPane
	plain := ansi.Strip(m.renderFooter())
	for _, key := range []string{"a ask", "@ target", "tab threads", "b batch", "F commit"} {
		if !strings.Contains(plain, key) {
			t.Errorf("diff footer %q does not show %q", plain, key)
		}
	}
	m.reviewThreadsMode = true
	plain = ansi.Strip(m.renderFooter())
	for _, key := range []string{"t reply", "x discard", "b batch", "F commit", "@ target"} {
		if !strings.Contains(plain, key) {
			t.Errorf("thread footer %q does not show %q", plain, key)
		}
	}
}

func TestReviewThreadReaderShowsLiveRepliesAndFinishPreservesTurn(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateItem(models.ChannelInbox, "review target", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.CreateReview(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	anchor := &models.ReviewAnchor{File: "src/main.go", Side: "new", StartLine: 8, EndLine: 9, Excerpt: "return result"}
	thread, err := s.CreateReviewThread(review.ID, anchor, models.ActorUser, "What happens on an empty result?")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitReviewBatch(review.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddReviewReply(review.ID, thread.Number, models.ActorAgent, "The caller handles this as no data."); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DetachReviewThread(review.ID, thread.Number); err != nil {
		t.Fatal(err)
	}

	m := newModel(s, nil, nil)
	m.width, m.height = 110, 30
	m.projectPane = reviewProjectPane
	m.reviewThreadsMode = true
	m.reviewReviews = []models.Review{mustGetReview(t, s, review.ID)}
	m.projectEntries = reviewEntries(s, m.reviewReviews)
	m.reviewSelectedID, m.reviewID, m.reviewDestinationID = review.ID, review.ID, target.ID
	m.reviewThreadCursor = 0
	m.selected = 0
	m.conv.Width, m.conv.Height = 75, 100
	m.updateConv()
	plain := ansi.Strip(m.conv.View())
	for _, want := range []string{"Thread #1", "no data", "Attachment Update", "src/main.go:8-9 (new)"} {
		if !strings.Contains(plain, want) {
			t.Errorf("live thread reader %q does not contain %q", plain, want)
		}
	}

	if err := s.FinishReview(review.ID); err == nil {
		t.Fatal("Finish review should refuse the unrecorded agent updates")
	}
	if _, err := s.AddAgentTurnWithReview(target.ID, "Reviewed the change."); err != nil {
		t.Fatal(err)
	}
	m.refreshReviewList()
	if err := s.FinishReview(review.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetReview(review.ID); err == nil {
		t.Fatal("finished review still exists")
	}
	updated, err := s.GetItem(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Turns) != 2 || !strings.Contains(updated.Turns[1].Content, "The caller handles this as no data.") {
		t.Fatalf("durable turns after finish = %#v", updated.Turns)
	}
}

func mustGetReview(t *testing.T, s *store.Store, id string) models.Review {
	t.Helper()
	review, err := s.GetReview(id)
	if err != nil {
		t.Fatal(err)
	}
	return review
}

func TestReviewFollowupStaysInThreadAndQueuesOneTurn(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateItem(models.ChannelInbox, "target", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.CreateReview(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := s.CreateReviewThread(review.ID, nil, models.ActorUser, "First question")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SubmitReviewBatch(review.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddReviewReply(review.ID, thread.Number, models.ActorAgent, "First answer"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddAgentTurnWithReview(target.ID, "Answered."); err != nil {
		t.Fatal(err)
	}
	m := newModel(s, nil, &fakeSupervisor{})
	m.width, m.height = 100, 30
	m.projectPane = reviewProjectPane
	m.reviewDestinationID = target.ID
	m.openReviewThreads()
	next, _ := m.handleReviewThreadsKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	m.input.SetValue("Follow-up question")
	next, _ = m.saveReviewComposer()
	m = next.(model)
	next, _ = m.submitReviewBatch()
	m = next.(model)
	if m.err != nil {
		t.Fatal(m.err)
	}
	updated := mustGetReview(t, s, review.ID)
	if len(updated.Threads) != 1 || updated.Threads[0].Number != thread.Number || len(updated.Threads[0].Messages) != 3 {
		t.Fatalf("follow-up created another thread: %#v", updated.Threads)
	}
	if !store.ReviewThreadNeedsReply(updated.Threads[0]) {
		t.Fatal("submitted follow-up needs a reply")
	}
	item, err := s.GetItem(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Turns) != 3 || !strings.Contains(item.Turns[2].Content, "Follow-up question") {
		t.Fatalf("turns = %#v", item.Turns)
	}
}

func TestReviewCapturePreservesOpenThreadReader(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateItem(models.ChannelInbox, "target", "body", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.CreateReview(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateReviewThread(review.ID, nil, models.ActorUser, "Question"); err != nil {
		t.Fatal(err)
	}
	m := newModel(s, nil, nil)
	m.projectPane = reviewProjectPane
	m.reviewDiffGeneration = 1
	m.openReviewThreads()
	title := m.projectEntries[0].title
	m.acceptReviewDiff(reviewDiffLoadedMsg{generation: 1, diff: reviewdiff.Diff{Files: []reviewdiff.File{{Path: "changed.go"}}}})
	if m.projectEntries[0].title != title || m.reviewSelectedID != review.ID {
		t.Fatalf("diff capture overwrote thread navigation: %#v", m.projectEntries)
	}
	m.closeReviewThreads()
	if m.projectEntries[0].title != "changed.go" {
		t.Fatalf("return to diff = %#v", m.projectEntries)
	}
}

func TestReviewAnchorValidationFlagsMovedOrMissingCode(t *testing.T) {
	anchor := &models.ReviewAnchor{File: "file.go", Side: "new", StartLine: 4, EndLine: 4, Excerpt: "return nil"}
	diff := reviewdiff.Diff{Files: []reviewdiff.File{{Path: "file.go", Lines: []reviewdiff.Line{{Kind: '+', NewLine: 4, Text: "return nil"}}}}}
	if !reviewAnchorMatchesDiff(diff, anchor) {
		t.Fatal("unchanged code should validate")
	}
	diff.Files[0].Lines[0].NewLine = 5
	if reviewAnchorMatchesDiff(diff, anchor) {
		t.Fatal("moved code must need relocation")
	}
	diff.Files[0].Lines[0].NewLine = 4
	diff.Files[0].Lines[0].Text = "return result"
	if reviewAnchorMatchesDiff(diff, anchor) {
		t.Fatal("changed code must not validate")
	}
	if reviewAnchorMatchesDiff(reviewdiff.Diff{}, anchor) {
		t.Fatal("missing file must not validate")
	}
}

func TestReviewFullScreenCommentIsPersistedBeforeDispatch(t *testing.T) {
	root := t.TempDir()
	s, err := store.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateItem(models.ChannelInbox, "destination", "", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	m := reviewDisplayModel()
	m.store = s
	m.allItems = []models.Item{target}
	m.focus = focusReadingPane
	m = m.enterCopyMode()
	if m.conv.Width != m.width {
		t.Fatal("review did not use full terminal width")
	}
	m = reviewKey(m, "s") // use the old code at the cursor in unified view
	m = reviewKey(m, "v")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = next.(model)
	if !m.copyMode || !m.reviewComposer.active || !m.mentionPicker.open {
		t.Fatal("full-screen question did not open destination picker")
	}
	if !strings.Contains(ansi.Strip(m.View()), "mention item") {
		t.Fatal("full-screen picker was not rendered")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.reviewComposer.selectingDestination || m.reviewComposer.itemID != target.ID {
		t.Fatal("destination could not be chosen full screen")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Why did this change?")})
	m = next.(model)
	plain := ansi.Strip(m.View())
	if !strings.Contains(plain, "Why did this change?") {
		t.Fatal("full-screen composer did not receive or render input")
	}
	if strings.Contains(plain, "[Review changes]") {
		t.Fatal("full-screen composer exposed the tab frame")
	}
	if lipgloss.Width(m.View()) > m.width || lipgloss.Height(m.View()) > m.height {
		t.Fatalf("full-screen composer exceeds terminal: %dx%d", lipgloss.Width(m.View()), lipgloss.Height(m.View()))
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = next.(model)
	if m.err != nil {
		t.Fatal(m.err)
	}
	if !m.copyMode || m.mode != modeNav {
		t.Fatal("saving did not return to full-screen review")
	}
	// Reopen the project exactly as a fresh TUI would, without submitting.
	reopened, err := store.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	restart := newModel(reopened, nil, nil)
	restart.width, restart.height = 110, 30
	restart.projectPane = reviewProjectPane
	restart.openReviewThreads()
	if len(restart.reviewReviews) != 1 || len(restart.reviewReviews[0].Threads) != 1 {
		t.Fatal("unsent review draft was not restored")
	}
	thread := restart.reviewReviews[0].Threads[0]
	if thread.Anchor == nil || thread.Anchor.Excerpt != "before" || thread.Messages[0].Submitted || thread.Messages[0].Content != "Why did this change?" {
		t.Fatalf("restored draft = %#v", thread)
	}
	item, err := reopened.GetItem(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Turns) != 0 || item.Status != models.StatusActive {
		t.Fatal("saving dispatched the review instead of retaining a draft")
	}
}

func TestThreadNavigationAcrossDestinationsRoutesReply(t *testing.T) {
	m := markerReviewModel(t)
	first := m.reviewReviews[0]
	target, err := m.store.CreateItem(models.ChannelInbox, "second destination", "", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.store.CreateReview(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.CreateReviewThread(second.ID, nil, models.ActorUser, "second question"); err != nil {
		t.Fatal(err)
	}
	m.reviewDestinationID = first.ItemID
	m.openReviewThreads()
	m.reviewThreadCursor = len(first.Threads) - 1
	m = reviewKey(m, "j")
	review, thread, ok := m.currentReviewThread()
	if !ok || review.ID != second.ID || thread.Number != 1 {
		t.Fatalf("next thread did not reach second destination: review=%s thread=%d", review.ID, thread.Number)
	}
	m = reviewKey(m, "t")
	if m.reviewComposer.itemID != target.ID || m.reviewComposer.reviewID != second.ID {
		t.Fatal("reply composer targets a different review than the selected thread")
	}
	m.input.SetValue("follow-up for second destination")
	next, _ := m.saveReviewComposer()
	m = next.(model)
	updated := mustGetReview(t, m.store, second.ID)
	if len(updated.Threads[0].Messages) != 2 || updated.Threads[0].Messages[1].Content != "follow-up for second destination" {
		t.Fatal("reply was not saved on the selected destination")
	}
	m = reviewKey(m, "k")
	review, thread, ok = m.currentReviewThread()
	if !ok || review.ID != first.ID || thread.Number != first.Threads[len(first.Threads)-1].Number {
		t.Fatal("previous thread did not return to the last thread in the first review")
	}
}

func TestFinishRequestsCommitAndKeepsReview(t *testing.T) {
	for _, name := range []string{"files", "threads", "fullscreen"} {
		threads := name != "files"
		t.Run(name, func(t *testing.T) {
			m := markerReviewModel(t)
			if out, err := exec.Command("git", "-C", m.store.Root, "init", "--quiet").CombinedOutput(); err != nil {
				t.Fatalf("git init: %s: %v", out, err)
			}
			review := m.reviewReviews[0]
			if _, err := m.store.SubmitReviewBatch(review.ID); err != nil {
				t.Fatal(err)
			}
			sup := &fakeSupervisor{}
			m.sup = sup
			if threads {
				m.openReviewThreads()
			} else {
				m.reviewID, m.reviewDestinationID, m.reviewSelectedID = "", "", ""
				m.focus = focusItemList
			}
			if name == "fullscreen" {
				m = m.enterCopyMode()
			}
			m.filterQuery = "does not match the destination"
			m = reviewKey(m, "F")
			if m.projectPane != 0 || m.copyMode || m.reviewThreadsMode || m.selectedID() != review.ItemID || m.focus != focusReadingPane {
				t.Fatal("Finish did not open the destination item")
			}
			if !strings.Contains(ansi.Strip(m.conv.View()), "LGTM, please commit") {
				t.Fatal("Finish did not reveal the submitted commit prompt")
			}
			if m.err != nil {
				t.Fatal(m.err)
			}
			if _, err := m.store.GetReview(review.ID); err != nil {
				t.Fatal("Finish deleted the review")
			}
			item, err := m.store.GetItem(review.ItemID)
			if err != nil {
				t.Fatal(err)
			}
			if len(item.Turns) != 2 || !strings.Contains(item.Turns[1].Content, "LGTM, please commit") || item.Status != models.StatusPendingAgent {
				t.Fatalf("Finish did not queue approval: %#v", item)
			}
			if len(sup.enqueued) != 1 || sup.enqueued[0] != item.ID {
				t.Fatal("commit request was not dispatched")
			}
		})
	}
}

func TestFinishFromFilesWithMultipleReviewsDoesNotGuessDestination(t *testing.T) {
	m := markerReviewModel(t)
	target, err := m.store.CreateItem(models.ChannelInbox, "other target", "", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.CreateReview(target.ID); err != nil {
		t.Fatal(err)
	}
	m.reviewID, m.reviewDestinationID = "", ""
	m = reviewKey(m, "F")
	if !m.reviewThreadsMode || m.err == nil || !strings.Contains(m.err.Error(), "multiple active reviews") {
		t.Fatal("Finish did not offer destination selection")
	}
	for _, review := range m.reviewReviews {
		if review.CommitRequested {
			t.Fatal("Finish approved an ambiguous destination")
		}
	}
}

func TestFinishWithoutReviewSelectsSessionAndDispatches(t *testing.T) {
	for _, name := range []string{"files", "threads", "fullscreen", "chat", "backlog"} {
		t.Run(name, func(t *testing.T) {
			m, target, sup := finishWithoutReviewModel(t)
			if name == "chat" {
				if _, err := m.store.SetMode(target.ID, models.AgentModeChat); err != nil {
					t.Fatal(err)
				}
			}
			if name == "backlog" {
				if _, err := m.store.SetStatus(target.ID, models.StatusBacklog); err != nil {
					t.Fatal(err)
				}
			}
			if name == "threads" {
				m.openReviewThreads()
			}
			if name == "fullscreen" {
				m = m.enterCopyMode()
			}
			// Even a remembered destination must be explicitly selected.
			m.reviewDestinationID = target.ID
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("F")})
			m = next.(model)
			if m.err != nil || !m.reviewComposer.commitOnSelection || !m.mentionPicker.open {
				t.Fatalf("Finish did not prompt for a session: %#v, err=%v", m.reviewComposer, m.err)
			}
			before, err := m.store.GetItem(target.ID)
			if err != nil || len(before.Turns) != 0 || len(sup.enqueued) != 0 {
				t.Fatal("Finish dispatched before a session was selected")
			}
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(model)
			if m.err != nil {
				t.Fatal(m.err)
			}
			item, err := m.store.GetItem(target.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(item.Turns) != 1 || item.Turns[0].Actor != models.ActorUser || item.Turns[0].Content != "LGTM, please commit" || item.Status != models.StatusPendingAgent || item.Mode != models.AgentModeCode {
				t.Fatalf("selection did not immediately queue LGTM: %#v", item)
			}
			if len(sup.enqueued) != 1 || sup.enqueued[0] != target.ID {
				t.Fatalf("queued items = %#v", sup.enqueued)
			}
			if m.projectPane != 0 || m.copyMode || m.reviewComposer.active || m.selectedID() != target.ID || !strings.Contains(ansi.Strip(m.conv.View()), "LGTM, please commit") {
				t.Fatal("selection did not reveal LGTM on the destination session")
			}
			reviews, err := m.store.ListReviews()
			if err != nil || len(reviews) != 0 {
				t.Fatal("LGTM created a review")
			}
		})
	}
}

func TestFinishWithoutReviewCancelAndArchivedDestination(t *testing.T) {
	for _, name := range []string{"cancel", "archived"} {
		t.Run(name, func(t *testing.T) {
			m, target, sup := finishWithoutReviewModel(t)
			m = reviewKey(m, "F")
			if name == "cancel" {
				next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
				m = next.(model)
				if m.mode != modeNav || m.reviewComposer.active || m.mentionPicker.open {
					t.Fatal("Escape did not cancel session selection")
				}
			} else {
				// Recheck durable status rather than trusting the picker snapshot.
				if _, err := m.store.SetStatus(target.ID, models.StatusArchived); err != nil {
					t.Fatal(err)
				}
				next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
				m = next.(model)
				if m.err == nil || !strings.Contains(m.err.Error(), "reopen") {
					t.Fatal("archived session was not rejected")
				}
			}
			item, err := m.store.GetItem(target.ID)
			if err != nil || len(item.Turns) != 0 || len(sup.enqueued) != 0 {
				t.Fatal("cancelled or rejected approval posted a turn or dispatched")
			}
		})
	}
}

func TestFinishWithoutReviewTabDispatchesMostRecentSession(t *testing.T) {
	m, target, sup := finishWithoutReviewModel(t)
	other, err := m.store.CreateItem(models.ChannelInbox, "A newer session", "", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.AddTurn(target.ID, models.ActorAgent, "Ready for review."); err != nil {
		t.Fatal(err)
	}
	m = reviewKey(m, "F")
	if len(m.mentionPicker.items) != 2 || m.mentionPicker.items[0].ID != target.ID || m.mentionPicker.selected != 0 {
		t.Fatal("most recently active session was not the default suggestion")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(model)
	if m.err != nil {
		t.Fatal(m.err)
	}
	updated, err := m.store.GetItem(target.ID)
	if err != nil || len(updated.Turns) != 2 || updated.Turns[1].Content != "LGTM, please commit" {
		t.Fatal("Tab did not post LGTM to the most recently active session")
	}
	if len(sup.enqueued) != 1 || sup.enqueued[0] != target.ID {
		t.Fatalf("Tab queued items = %#v", sup.enqueued)
	}
	untouched, err := m.store.GetItem(other.ID)
	if err != nil || len(untouched.Turns) != 0 {
		t.Fatal("Tab posted to the wrong session")
	}
}

func finishWithoutReviewModel(t *testing.T) (model, models.Item, *fakeSupervisor) {
	t.Helper()
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateItem(models.ChannelInbox, "commit session", "", models.TypeThread, models.StatusActive, "")
	if err != nil {
		t.Fatal(err)
	}
	sup := &fakeSupervisor{}
	m := newModel(s, nil, sup)
	m.width, m.height = 110, 30
	m.projectPane = reviewProjectPane
	m.conv.Width, m.conv.Height = 75, 14
	m = m.recalcLayout()
	return m, target, sup
}
