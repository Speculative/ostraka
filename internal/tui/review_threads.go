package tui

import (
	"fmt"
	"strings"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/reviewcode"
	"github.com/Speculative/ostraka/internal/reviewdiff"
	"github.com/Speculative/ostraka/internal/store"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	tea "github.com/charmbracelet/bubbletea"
)

func (m model) handleReviewThreadsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "P":
		if controller, ok := m.sup.(dispatchPauseController); ok {
			controller.ToggleDispatchPause()
		}
		return m, nil
	case "ctrl+c":
		return m.handleInterruptKey()
	case "q":
		if _, busy := m.busyDispatch(); busy {
			m.mode = modeQuit
			return m, nil
		}
		return m, tea.Quit
	case "1":
		return m.switchView(channelView(models.ChannelInbox))
	case "2":
		return m.switchView(archiveView)
	case "3":
		m.projectPane = 1
		m.showProjectContext()
		return m, nil
	case "4", "tab", "esc":
		m.closeReviewThreads()
		return m, nil
	case "r":
		m.refreshReviewList()
		m.updateConv()
		return m.requestReviewDiff()
	case "a":
		return m.beginReviewQuestion(false)
	case "enter", "t":
		return m.replyToReviewThread()
	case "@":
		return m.beginReviewQuestion(true)
	case "b":
		return m.submitReviewBatch()
	case "F":
		return m.finishCurrentReview()
	case "x":
		return m.deleteSelectedReviewDraft()
	case "left", "shift+left", "h":
		m.returnToReviewThreadCode()
		return m, nil
	case "right", "shift+right", "l":
		m.focus = focusReadingPane
		m.reviewThreadCursor = 0
		m.updateConv()
		m.revealReviewThread()
		return m, nil
	case "j", "down", "shift+down":
		m.moveReviewThreadCursor(1)
		return m, nil
	case "k", "up", "shift+up":
		m.moveReviewThreadCursor(-1)
		return m, nil
	case "]":
		m.moveReviewThreadCursor(1)
		return m, nil
	case "[":
		m.moveReviewThreadCursor(-1)
		return m, nil
	case "pgup":
		return m, m.pageConversation(-1)
	case "pgdown":
		return m, m.pageConversation(1)
	case "f":
		return m.enterCopyMode(), nil
	default:
		return m, nil
	}
}

func (m *model) refreshReviewList() {
	if m.store == nil {
		return
	}
	reviews, err := m.store.ListReviews()
	if err != nil {
		m.err = err
		return
	}
	m.reviewReviews = reviews
	if m.reviewID != "" {
		if _, ok := reviewByID(reviews, m.reviewID); !ok {
			m.reviewID = ""
		}
	}
	if m.reviewThreadsMode {
		previous := m.reviewSelectedID
		m.projectEntries = reviewEntries(m.store, reviews)
		index := reviewIndexByID(reviews, previous)
		if index < 0 && m.reviewDestinationID != "" {
			index = reviewIndexByItemID(reviews, m.reviewDestinationID)
		}
		if index < 0 && len(reviews) > 0 {
			index = min(m.selected, len(reviews)-1)
		}
		if len(reviews) == 0 {
			m.selected = 0
			m.reviewSelectedID = ""
			m.reviewID = ""
		} else {
			m.selected = index
			m.selectReviewEntry()
		}
	}
	if !m.reviewThreadsMode {
		m.projectEntries = m.reviewFileEntries()
	}
}

func reviewEntries(s *store.Store, reviews []models.Review) []projectEntry {
	entries := make([]projectEntry, 0, len(reviews))
	for _, review := range reviews {
		item, err := s.GetItem(review.ItemID)
		title := "@" + review.ItemID
		if err == nil && item.Title != "" {
			title += " · " + item.Title
		}
		drafts, needsReply := 0, 0
		for _, thread := range review.Threads {
			if store.ReviewThreadNeedsReply(thread) {
				needsReply++
			}
			for _, message := range thread.Messages {
				if message.Actor == models.ActorUser && !message.Submitted {
					drafts++
				}
			}
		}
		meta := fmt.Sprintf("%d threads", len(review.Threads))
		if drafts > 0 {
			meta += fmt.Sprintf(" · %d drafts", drafts)
		}
		if needsReply > 0 {
			meta += fmt.Sprintf(" · %d need reply", needsReply)
		}
		entries = append(entries, projectEntry{title: title, meta: meta})
	}
	if len(entries) == 0 {
		entries = append(entries, projectEntry{title: "No active reviews", meta: "press a to start with an @destination"})
	}
	return entries
}

func reviewByID(reviews []models.Review, id string) (models.Review, bool) {
	for _, review := range reviews {
		if review.ID == id {
			return review, true
		}
	}
	return models.Review{}, false
}

func reviewIndexByID(reviews []models.Review, id string) int {
	for i, review := range reviews {
		if review.ID == id {
			return i
		}
	}
	return -1
}

func reviewIndexByItemID(reviews []models.Review, itemID string) int {
	for i, review := range reviews {
		if review.ItemID == itemID {
			return i
		}
	}
	return -1
}

func (m *model) selectReviewEntry() {
	if len(m.reviewReviews) == 0 || m.selected < 0 || m.selected >= len(m.reviewReviews) {
		return
	}
	review := m.reviewReviews[m.selected]
	if m.reviewSelectedID != review.ID {
		m.reviewThreadCursor = 0
	}
	if len(review.Threads) == 0 {
		m.reviewThreadCursor = 0
	} else {
		m.reviewThreadCursor = min(m.reviewThreadCursor, len(review.Threads)-1)
	}
	m.reviewSelectedID = review.ID
	m.reviewDestinationID = review.ItemID
	m.reviewID = review.ID
	m.updateConv()
}

func (m model) currentReview() (models.Review, bool) {
	if m.reviewID != "" {
		if review, ok := reviewByID(m.reviewReviews, m.reviewID); ok {
			return review, true
		}
	}
	if m.reviewThreadsMode && m.selected >= 0 && m.selected < len(m.reviewReviews) {
		return m.reviewReviews[m.selected], true
	}
	if m.reviewDestinationID != "" {
		index := reviewIndexByItemID(m.reviewReviews, m.reviewDestinationID)
		if index >= 0 {
			return m.reviewReviews[index], true
		}
	}
	return models.Review{}, false
}

func (m *model) openReviewThreads() {
	m.reviewSavedPath = m.selectedReviewPath()
	m.refreshReviewList()
	m.reviewThreadsMode = true
	m.projectEntries = reviewEntries(m.store, m.reviewReviews)
	index := reviewIndexByID(m.reviewReviews, m.reviewSelectedID)
	if index < 0 && m.reviewDestinationID != "" {
		index = reviewIndexByItemID(m.reviewReviews, m.reviewDestinationID)
	}
	if index < 0 {
		index = 0
	}
	m.selected = index
	if len(m.reviewReviews) > 0 {
		m.selectReviewEntry()
	}
	m.focus = focusReadingPane
	*m = m.recalcLayout()
	m.conv.GotoTop()
}

func (m *model) closeReviewThreads() {
	m.reviewThreadsMode = false
	m.reviewSelectedID = ""
	m.projectEntries = m.reviewFileEntries()
	m.selected = indexReviewPath(m.reviewDiff.Files, m.reviewSavedPath)
	m.reviewSavedPath = ""
	if file, ok := m.selectedReviewFile(); ok {
		m.reviewCursor = min(m.reviewCursor, len(file.Lines)-1)
	} else {
		m.selected = 0
	}
	*m = m.recalcLayout()
}

func (m model) beginReviewQuestion(forceDestination bool) (tea.Model, tea.Cmd) {
	if m.store == nil {
		m.err = fmt.Errorf("project store is unavailable")
		return m, nil
	}
	var anchor *models.ReviewAnchor
	if !m.reviewThreadsMode {
		selection, marked, err := m.ReviewSelection()
		if err != nil {
			m.err = err
			return m, nil
		}
		if marked {
			anchor = &models.ReviewAnchor{
				File: selection.Path, Side: selection.Side,
				StartLine: selection.StartLine, EndLine: selection.EndLine,
				Excerpt: selection.Excerpt,
			}
		}
	}
	if !forceDestination {
		if m.reviewThreadsMode {
			if review, ok := m.currentReview(); ok {
				return m.startReviewComposer(review.ItemID, review.ID, nil, 0, "", "")
			}
		} else if m.reviewDestinationID != "" {
			index := reviewIndexByItemID(m.reviewReviews, m.reviewDestinationID)
			if index >= 0 {
				review := m.reviewReviews[index]
				return m.startReviewComposer(review.ItemID, review.ID, anchor, 0, "", "")
			}
			return m.startReviewComposer(m.reviewDestinationID, "", anchor, 0, "", "")
		}
	}
	return m.openReviewDestinationPicker(anchor)
}

func (m model) openReviewDestinationPicker(anchor *models.ReviewAnchor) (tea.Model, tea.Cmd) {
	m.refreshReviewList()
	if m.allItems == nil {
		items, err := m.store.ListItems(store.ListOpts{})
		if err != nil {
			m.err = err
			return m, nil
		}
		m.allItems = items
	}
	m.reviewComposer = reviewComposerState{active: true, selectingDestination: true, anchor: anchor}
	m.mode = modeCompose
	m.closeMentionPicker()
	m.input.Reset()
	m.input.SetValue("@")
	m.input.CursorEnd()
	m = m.recalcLayout()
	m.refreshMentionPicker()
	return m, m.input.Focus()
}

func (m model) chooseReviewDestination(item models.Item) (tea.Model, tea.Cmd) {
	if m.reviewComposer.commitOnSelection {
		m.refreshReviewList()
		if index := reviewIndexByItemID(m.reviewReviews, item.ID); index >= 0 {
			m = m.closeReviewComposerModel()
			m.reviewID, m.reviewDestinationID = m.reviewReviews[index].ID, item.ID
			return m.finishCurrentReview()
		}
		return m.requestCommitWithoutReview(item.ID)
	}
	m.closeMentionPicker()
	var reviewID string
	index := reviewIndexByItemID(m.reviewReviews, item.ID)
	if index >= 0 {
		reviewID = m.reviewReviews[index].ID
	}
	return m.startReviewComposer(item.ID, reviewID, m.reviewComposer.anchor, 0, "", "")
}

func (m model) startReviewComposer(itemID, reviewID string, anchor *models.ReviewAnchor, threadNumber int, messageID, content string) (tea.Model, tea.Cmd) {
	m.reviewDestinationID = itemID
	m.reviewID = reviewID
	m.reviewComposer = reviewComposerState{
		active: true, itemID: itemID, reviewID: reviewID,
		threadNumber: threadNumber, messageID: messageID,
		anchor: cloneReviewAnchor(anchor),
	}
	m.mode = modeCompose
	m.closeMentionPicker()
	m.input.Reset()
	m.input.SetValue(content)
	m.input.CursorEnd()
	m = m.recalcLayout()
	return m, m.input.Focus()
}

func cloneReviewAnchor(anchor *models.ReviewAnchor) *models.ReviewAnchor {
	if anchor == nil {
		return nil
	}
	copy := *anchor
	return &copy
}

func (m model) handleReviewComposerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.reviewComposer.selectingDestination && m.mentionPicker.open {
		switch {
		case msg.String() == "esc":
			if m.reviewComposer.commitOnSelection {
				return m.closeReviewComposerModel(), nil
			}
			m.closeMentionPicker()
			return m, nil
		case msg.Type == tea.KeyUp || msg.String() == "up":
			if m.mentionPicker.selected > 0 {
				m.mentionPicker.selected--
			}
			return m, nil
		case msg.Type == tea.KeyDown || msg.String() == "down":
			if m.mentionPicker.selected < len(m.mentionPicker.items)-1 {
				m.mentionPicker.selected++
			}
			return m, nil
		case (msg.String() == "enter" || msg.String() == "tab") && len(m.mentionPicker.items) > 0:
			return m.chooseMention()
		case msg.String() == "tab":
			m.closeMentionPicker()
			return m, nil
		}
	}
	switch msg.String() {
	case "ctrl+s":
		return m.saveReviewComposer()
	case "esc", "ctrl+c":
		m = m.closeReviewComposerModel()
		return m, nil
	case "pgup":
		return m, m.pageConversation(-1)
	case "pgdown":
		return m, m.pageConversation(1)
	}
	previousHeight := m.currentInputHeight()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.reviewComposer.selectingDestination {
		m.refreshMentionPicker()
	}
	if m.currentInputHeight() != previousHeight {
		m = m.recalcLayout()
	}
	return m, cmd
}

func (m model) closeReviewComposerModel() model {
	m.closeMentionPicker()
	m.reviewComposer = reviewComposerState{}
	m.mode = modeNav
	m.input.Blur()
	m = m.recalcLayout()
	return m
}

func (m model) saveReviewComposer() (tea.Model, tea.Cmd) {
	content := strings.TrimSpace(m.input.Value())
	if content == "" {
		m.err = fmt.Errorf("review question must not be blank")
		return m, nil
	}
	composer := m.reviewComposer
	if composer.selectingDestination || composer.itemID == "" {
		m.err = fmt.Errorf("choose one @destination item before saving the question")
		return m, nil
	}
	reviewID := composer.reviewID
	if reviewID == "" {
		review, err := m.store.CreateReview(composer.itemID)
		if err != nil {
			m.err = err
			return m, nil
		}
		reviewID = review.ID
		m.reviewComposer.reviewID = reviewID
		m.reviewID = reviewID
	}
	var thread models.ReviewThread
	var err error
	if composer.messageID != "" {
		thread, err = m.store.UpdateReviewDraft(reviewID, composer.threadNumber, composer.messageID, content)
	} else if composer.threadNumber > 0 {
		thread, err = m.store.AddReviewReply(reviewID, composer.threadNumber, models.ActorUser, content)
	} else {
		thread, err = m.store.CreateReviewThread(reviewID, composer.anchor, models.ActorUser, content)
	}
	if err != nil {
		m.err = err
		return m, nil
	}
	m.reviewID = reviewID
	m.reviewDestinationID = composer.itemID
	m = m.closeReviewComposerModel()
	m.refreshReviewList()
	m.reviewThreadCursor = threadIndexByNumber(m.reviewReviews, reviewID, thread.Number)
	m.updateConv()
	return m, nil
}

func threadIndexByNumber(reviews []models.Review, reviewID string, number int) int {
	if review, ok := reviewByID(reviews, reviewID); ok {
		for i, thread := range review.Threads {
			if thread.Number == number {
				return i
			}
		}
	}
	return 0
}

func (m model) deleteSelectedReviewDraft() (tea.Model, tea.Cmd) {
	review, thread, ok := m.currentReviewThread()
	if !ok {
		m.err = fmt.Errorf("select a review thread first")
		return m, nil
	}
	message, ok := lastReviewDraft(thread)
	if !ok {
		m.err = fmt.Errorf("thread #%d has no unsent user draft to delete", thread.Number)
		return m, nil
	}
	updated, err := m.store.DeleteReviewDraft(review.ID, thread.Number, message.ID)
	if err != nil {
		m.err = err
		return m, nil
	}
	m.reviewReviews = replaceReview(m.reviewReviews, updated)
	m.refreshReviewList()
	m.updateConv()
	return m, nil
}

func lastReviewDraft(thread models.ReviewThread) (models.ReviewMessage, bool) {
	for i := len(thread.Messages) - 1; i >= 0; i-- {
		message := thread.Messages[i]
		if message.Actor == models.ActorUser && !message.Submitted {
			return message, true
		}
	}
	return models.ReviewMessage{}, false
}

func replaceReview(reviews []models.Review, updated models.Review) []models.Review {
	for i := range reviews {
		if reviews[i].ID == updated.ID {
			reviews[i] = updated
			return reviews
		}
	}
	return append(reviews, updated)
}

func (m model) submitReviewBatch() (tea.Model, tea.Cmd) {
	review, ok := m.currentReview()
	if !ok {
		m.err = fmt.Errorf("select or create a review before submitting a batch")
		return m, nil
	}
	item, err := m.store.SubmitReviewBatch(review.ID)
	if err != nil {
		m.err = err
		return m, nil
	}
	if dispatchable(item.Status) {
		item, err = m.store.SetStatus(item.ID, models.StatusPendingAgent)
		if err != nil {
			m.err = fmt.Errorf("review batch was recorded on @%s but could not be queued: %w", item.ID, err)
			return m, nil
		}
		if m.sup != nil {
			m.sup.Enqueue(item.ID)
		}
	}
	m.replaceItemSnapshot(item)
	m.refreshReviewList()
	m.updateConv()
	return m, nil
}

func (m model) finishCurrentReview() (tea.Model, tea.Cmd) {
	m.refreshReviewList()
	review, ok := m.currentReview()
	if !ok && len(m.reviewReviews) == 1 {
		review, ok = m.reviewReviews[0], true
		m.reviewID, m.reviewDestinationID = review.ID, review.ItemID
	}
	if !ok {
		if len(m.reviewReviews) > 1 {
			m.openReviewThreads()
			m.err = fmt.Errorf("multiple active reviews; choose the destination with j/k, then press F")
		} else {
			next, cmd := m.openReviewDestinationPicker(nil)
			m = next.(model)
			m.reviewComposer.commitOnSelection = true
			m.closeMentionPicker()
			m.refreshMentionPicker()
			return m, cmd
		}
		return m, nil
	}
	item, err := m.store.RequestReviewCommit(review.ID)
	if err != nil {
		m.err = err
		return m, nil
	}
	return m.dispatchCommitRequest(item)
}

func (m model) requestCommitWithoutReview(itemID string) (tea.Model, tea.Cmd) {
	item, err := m.store.GetItem(itemID)
	if err != nil {
		m.err = err
		return m, nil
	}
	if models.TerminalStatuses[item.Status] {
		m.err = fmt.Errorf("reopen the destination item before requesting a commit")
		return m, nil
	}
	item, err = m.store.AddTurn(itemID, models.ActorUser, "LGTM, please commit")
	if err != nil {
		m.err = err
		return m, nil
	}
	m = m.closeReviewComposerModel()
	return m.dispatchCommitRequest(item)
}

func (m model) dispatchCommitRequest(item models.Item) (tea.Model, tea.Cmd) {
	var err error
	if item.Mode == models.AgentModeChat {
		item, err = m.store.SetMode(item.ID, models.AgentModeCode)
		if err != nil {
			m.err = err
			return m, nil
		}
	}
	item, err = m.store.SetStatus(item.ID, models.StatusPendingAgent)
	if err != nil {
		m.err = err
		return m, nil
	}
	if m.sup != nil {
		m.sup.Enqueue(item.ID)
	}

	m.replaceItemSnapshot(item)
	m.refreshReviewList()
	// Show the approval immediately, rather than leaving Finish looking like
	// a no-op in the review browser while dispatch happens in the background.
	m.copyMode = false
	m.reviewThreadsMode = false
	m.projectPane = 0
	m.projectEntries = nil
	m.view = channelView(item.Channel)
	m.filterQuery = ""
	m.filterInput.SetValue("")
	m.draftSelected = false
	if m.collapsed == nil {
		m.collapsed = make(map[string]bool)
	}
	if item.Parent != "" {
		m.collapsed[item.Parent] = false
	}
	if m.selectedByView == nil {
		m.selectedByView = make(map[listView]string)
	}
	m.selectedByView[m.view] = item.ID
	m.items, m.hiddenBacklog = m.prepareVisibleItems(m.allItems)
	m.restoreSelection(item.ID)
	m.focus = focusReadingPane
	m = m.recalcLayout()
	m.showSelected(false)
	return m.requestItemsLoad(m.view, m.showBacklog)
}

func (m *model) moveReviewThreadCursor(delta int) {
	review, ok := m.currentReview()
	if !ok {
		return
	}
	cursor := m.reviewThreadCursor + delta
	if cursor < 0 || cursor >= len(review.Threads) {
		// Without a review list, adjacent review groups are reached by the
		// same next/previous-thread controls. Skip groups with no threads.
		index := reviewIndexByID(m.reviewReviews, review.ID)
		for index += delta; index >= 0 && index < len(m.reviewReviews); index += delta {
			if len(m.reviewReviews[index].Threads) == 0 {
				continue
			}
			m.selected = index
			m.selectReviewEntry()
			cursor = 0
			if delta < 0 {
				cursor = len(m.reviewReviews[index].Threads) - 1
			}
			m.reviewThreadCursor = cursor
			m.updateConv()
			m.revealReviewThread()
			return
		}
	}
	m.reviewThreadCursor = max(0, min(len(review.Threads)-1, cursor))
	m.updateConv()
	m.revealReviewThread()
}

func (m model) currentReviewThread() (models.Review, models.ReviewThread, bool) {
	review, ok := m.currentReview()
	if !ok || m.reviewThreadCursor < 0 || m.reviewThreadCursor >= len(review.Threads) {
		return review, models.ReviewThread{}, false
	}
	return review, review.Threads[m.reviewThreadCursor], true
}

func (m *model) updateReviewThreadsConv() {
	review, ok := m.currentReview()
	m.reviewThreadOffsets, m.reviewThreadEnds = nil, nil
	if !ok || len(review.Threads) == 0 {
		m.conv.SetContent("No review threads. Press a to add a comment, or Tab to select code.")
		m.resetProjectConvSelection()
		return
	}
	width := max(1, m.conv.Width)
	itemTitle := review.ItemID
	if m.store != nil {
		if item, err := m.store.GetItem(review.ItemID); err == nil && item.Title != "" {
			itemTitle = item.Title
		}
	}
	parts := []string{renderMarkdown(fmt.Sprintf("# Review @%s · %s", review.ItemID, itemTitle), width)}
	offset := strings.Count(parts[0], "\n") + 2
	for i, thread := range review.Threads {
		rendered := renderMarkdown(m.reviewThreadMarkdown(thread), width)
		selected := i == m.reviewThreadCursor
		if selected {
			rendered = selectedBackground(rendered, width)
		}
		// A dedicated title band separates threads from the Markdown message
		// headings. Keep its colour even when the whole thread is selected.
		headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81")).
			Background(lipgloss.Color("24")).Width(width)
		if selected {
			headerStyle = headerStyle.Background(selectedBg)
		}
		header := headerStyle.Render(ansi.Wrap(fmt.Sprintf("▌ Thread #%d of %d", thread.Number, len(review.Threads)), width, " "))
		spacer := ""
		if selected {
			spacer = lipgloss.NewStyle().Width(width).Background(selectedBg).Render("")
		}
		rendered = header + "\n" + spacer + "\n" + rendered
		m.reviewThreadOffsets = append(m.reviewThreadOffsets, offset)
		m.reviewThreadEnds = append(m.reviewThreadEnds, offset+strings.Count(rendered, "\n")+1)
		parts = append(parts, rendered)
		offset += strings.Count(rendered, "\n") + 2
	}
	m.conv.SetContent(strings.Join(parts, "\n\n"))
	m.resetProjectConvSelection()
}

func (m model) reviewThreadMarkdown(thread models.ReviewThread) string {
	var content strings.Builder
	if thread.Anchor != nil {
		fmt.Fprintf(&content, "**%s:%d-%d (%s)**\n\n", thread.Anchor.File, thread.Anchor.StartLine, thread.Anchor.EndLine, thread.Anchor.Side)
		if !reviewAnchorMatchesDiff(m.reviewDiff, thread.Anchor) {
			content.WriteString("_Attachment not verified in the loaded diff. Refresh with r; ask the agent to relocate or detach it if the code has changed._\n\n")
		}
		if thread.Anchor.Excerpt != "" {
			content.WriteString(reviewcode.Block(thread.Anchor.File, thread.Anchor.Excerpt))
			content.WriteByte('\n')
		}
	}
	if store.ReviewThreadNeedsReply(thread) {
		content.WriteString("_Awaiting agent reply._\n\n")
	}
	for _, message := range thread.Messages {
		actor := string(message.Actor)
		if message.Kind == "attachment" {
			actor += " · attachment update"
		}
		indicator := ""
		if message.Actor == models.ActorUser && !message.Submitted {
			indicator = " · [unsent]"
		}
		fmt.Fprintf(&content, "### %s · %s%s\n\n", strings.Title(actor), message.CreatedAt.Local().Format("2006-01-02 15:04"), indicator)
		if message.Anchor != nil && (thread.Anchor == nil || !sameReviewAnchor(message.Anchor, thread.Anchor)) {
			fmt.Fprintf(&content, "Context: %s:%d-%d (%s)\n\n", message.Anchor.File, message.Anchor.StartLine, message.Anchor.EndLine, message.Anchor.Side)
		}
		content.WriteString(message.Content)
		content.WriteString("\n\n")
	}
	return content.String()
}

func (m *model) revealReviewThread() {
	i := m.reviewThreadCursor
	if i < 0 || i >= len(m.reviewThreadOffsets) {
		return
	}
	top, bottom := m.reviewThreadOffsets[i], m.reviewThreadEnds[i]
	if top < m.conv.YOffset || bottom > m.conv.YOffset+m.conv.Height {
		m.conv.SetYOffset(top)
	}
}

func (m model) replyToReviewThread() (tea.Model, tea.Cmd) {
	review, thread, ok := m.currentReviewThread()
	if !ok {
		m.err = fmt.Errorf("select a review thread to reply to")
		return m, nil
	}
	return m.startReviewComposer(review.ItemID, review.ID, thread.Anchor, thread.Number, "", "")
}

func sameReviewAnchor(a, b *models.ReviewAnchor) bool {
	return a != nil && b != nil && a.File == b.File && a.Side == b.Side &&
		a.StartLine == b.StartLine && a.EndLine == b.EndLine && a.Excerpt == b.Excerpt
}

// A stored excerpt is context, not proof that its line numbers still refer to
// the current code. Flag attachments we cannot verify rather than displaying
// the saved range as a confirmed location after later edits.
func reviewAnchorMatchesDiff(diff reviewdiff.Diff, anchor *models.ReviewAnchor) bool {
	if anchor == nil {
		return true
	}
	for _, file := range diff.Files {
		path := file.Path
		if anchor.Side == "old" && file.OldPath != "" {
			path = file.OldPath
		}
		if path != anchor.File {
			continue
		}
		if file.FullLines != nil {
			file.Lines = file.FullLines
		}
		from, to := -1, -1
		for i, line := range file.Lines {
			n := line.NewLine
			if anchor.Side == "old" {
				n = line.OldLine
			}
			if n == anchor.StartLine {
				from = i
			}
			if n == anchor.EndLine {
				to = i
			}
		}
		if from < 0 || to < from {
			return false
		}
		selection, err := reviewdiff.Select(file, from, to, anchor.Side)
		return err == nil && selection.Excerpt == anchor.Excerpt
	}
	return false
}
