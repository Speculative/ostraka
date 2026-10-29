package webui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/supervisor"
)

type action struct {
	Op        string               `json:"op"`
	ID        string               `json:"id"`
	Title     string               `json:"title"`
	Body      string               `json:"body"`
	Status    models.Status        `json:"status"`
	Parent    string               `json:"parent"`
	Group     string               `json:"group"`
	Content   string               `json:"content"`
	Mode      models.AgentMode     `json:"mode"`
	Provider  supervisor.Provider  `json:"provider"`
	Model     string               `json:"model"`
	Effort    string               `json:"effort"`
	Delta     int                  `json:"delta"`
	IDs       []string             `json:"ids"`
	Expected  []string             `json:"expected"`
	ReviewID  string               `json:"review_id"`
	Thread    int                  `json:"thread"`
	MessageID string               `json:"message_id"`
	Anchor    *models.ReviewAnchor `json:"anchor"`
	Flatten   bool                 `json:"flatten"`
}

func (s *Server) action(w http.ResponseWriter, r *http.Request) {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		fail(w, 415, fmt.Errorf("application/json required"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	var a action
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if e := dec.Decode(&a); e != nil {
		fail(w, 400, e)
		return
	}
	if e := dec.Decode(new(any)); e != io.EOF {
		fail(w, 400, fmt.Errorf("expected one JSON object"))
		return
	}
	v, e := s.apply(a)
	respond(w, v, e)
}
func (s *Server) enqueue(id string) error {
	item, e := s.Store.GetItem(id)
	if e != nil {
		return e
	}
	if item.Status == models.StatusBacklog || item.Status == models.StatusArchived || item.Status == models.StatusProposed {
		return nil
	}
	if _, e = s.Store.SetStatus(id, models.StatusPendingAgent); e != nil {
		return e
	}
	if s.Supervisor != nil {
		s.Supervisor.Enqueue(id)
	}
	return nil
}
func (s *Server) apply(a action) (any, error) {
	st := s.Store
	switch a.Op {
	case "create":
		if a.Status == "" {
			a.Status = models.StatusActive
		}
		if !models.UserSettableStatus(a.Status) {
			return nil, fmt.Errorf("unsupported user status")
		}
		return st.CreateItemWithGroup(models.ChannelInbox, a.Title, a.Body, models.TypeThread, a.Status, a.Parent, a.Group)
	case "turn":
		if strings.TrimSpace(a.Content) == "" {
			return nil, fmt.Errorf("turn must not be blank")
		}
		item, e := st.AddTurn(a.ID, models.ActorUser, a.Content)
		if e != nil {
			return nil, e
		}
		if e = s.enqueue(a.ID); e != nil {
			return nil, e
		}
		_ = st.ClearDraft(a.ID)
		return item, nil
	case "start":
		if _, e := st.SetStatus(a.ID, models.StatusPendingAgent); e != nil {
			return nil, e
		}
		if s.Supervisor != nil {
			s.Supervisor.Enqueue(a.ID)
		}
		return st.GetItem(a.ID)
	case "rename":
		return st.RenameItem(a.ID, a.Title)
	case "status":
		if !models.UserSettableStatus(a.Status) {
			return nil, fmt.Errorf("unsupported user status")
		}
		return st.SetStatus(a.ID, a.Status)
	case "mode":
		if !models.ValidAgentMode(a.Mode) {
			return nil, fmt.Errorf("unsupported mode")
		}
		return st.SetMode(a.ID, a.Mode)
	case "end-goal":
		return st.EndGoal(a.ID)
	case "group":
		return st.SetGroup(a.ID, a.Group)
	case "delete":
		return nil, st.DeleteItem(a.ID)
	case "reparent":
		return st.ReparentItem(a.ID, a.Parent, a.Flatten)
	case "unparent":
		return st.UnparentItem(a.ID)
	case "mention":
		return st.AddMention(a.ID, a.Parent)
	case "move-backlog":
		if a.Delta != -1 && a.Delta != 1 {
			return nil, fmt.Errorf("delta must be -1 or 1")
		}
		return st.MoveBacklogRoot(a.ID, a.Delta)
	case "child-order":
		return nil, st.SetChildOrder(a.ID, a.Expected, a.IDs)
	case "draft":
		if _, e := st.GetItem(a.ID); e != nil {
			return nil, e
		}
		return nil, st.SaveDraft(a.ID, a.Content)
	case "instructions":
		return nil, st.ReplaceProjectInstructions(a.Content)
	case "brief":
		return nil, st.ReplaceProjectBrief(a.Content)
	case "review-create":
		return st.CreateReview(a.ID)
	case "review-thread":
		return st.CreateReviewThread(a.ReviewID, a.Anchor, models.ActorUser, a.Content)
	case "review-reply":
		return st.AddReviewReply(a.ReviewID, a.Thread, models.ActorUser, a.Content)
	case "review-draft":
		return st.UpdateReviewDraft(a.ReviewID, a.Thread, a.MessageID, a.Content)
	case "review-delete-draft":
		return st.DeleteReviewDraft(a.ReviewID, a.Thread, a.MessageID)
	case "review-attach":
		if a.Anchor == nil {
			return nil, fmt.Errorf("anchor required")
		}
		return st.AttachReviewThread(a.ReviewID, a.Thread, *a.Anchor)
	case "review-detach":
		return st.DetachReviewThread(a.ReviewID, a.Thread)
	case "review-dismiss":
		return nil, st.DismissReview(a.ReviewID)
	case "review-submit":
		item, e := st.SubmitReviewBatch(a.ReviewID)
		if e != nil {
			return nil, e
		}
		return item, s.enqueue(item.ID)
	case "review-commit":
		item, e := st.RequestReviewCommit(a.ReviewID)
		if e != nil {
			return nil, e
		}
		return item, s.enqueue(item.ID)
	case "session":
		if _, e := st.GetItem(a.ID); e != nil {
			return nil, e
		}
		return nil, supervisor.SelectNextSession(st.Root, a.ID, a.Provider, a.Model, a.Effort)
	case "interrupt", "pause":
		if s.Supervisor == nil {
			return nil, fmt.Errorf("this operation requires the server to own the supervisor (--supervise)")
		}
		switch a.Op {
		case "interrupt":
			return nil, s.Supervisor.InterruptItem(a.ID)
		case "pause":
			return map[string]bool{"paused": s.Supervisor.ToggleDispatchPause()}, nil

		}
	}
	return nil, fmt.Errorf("unknown operation %q", a.Op)
}
