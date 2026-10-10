package store

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Speculative/ostraka/internal/models"
)

func goalStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func goalTestItem(t *testing.T, s *Store, parent string) models.Item {
	t.Helper()
	item, err := s.CreateItem(models.ChannelInbox, "work", "body", models.TypeThread, models.StatusActive, parent)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func TestGoalCapturesInitiatingTurnAndEndsByOwnerID(t *testing.T) {
	s := goalStore(t)
	item := goalTestItem(t, s, "")
	other := goalTestItem(t, s, "")
	if _, err := s.SetMode(item.ID, models.AgentModeGoal); err != nil {
		t.Fatal(err)
	}
	armed, found, err := s.GoalItem()
	if err != nil || !found || armed.Goal != nil {
		t.Fatalf("armed = %+v, %v, %v", armed, found, err)
	}
	if ok, err := s.AcknowledgeDispatch(item.ID, time.Time{}); err != nil || ok {
		t.Fatalf("armed dispatch = %v, %v", ok, err)
	}
	if _, err := s.AddTurn(item.ID, models.ActorUser, "\nFinish this exact goal.\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTurn(item.ID, models.ActorUser, "Later steering"); err != nil {
		t.Fatal(err)
	}
	item, err = s.GetItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if item.Goal == nil || item.Goal.Prompt != "\nFinish this exact goal.\n" || !item.Goal.StartedAt.Equal(item.Turns[0].Timestamp) {
		t.Fatalf("goal = %+v prompt=%q turn time=%s", item.Goal, item.Goal.Prompt, item.Turns[0].Timestamp)
	}
	if _, err := s.EndGoal(other.ID); err == nil {
		t.Fatal("wrong item ended goal")
	}
	oldGoal := item.Goal.StartedAt
	if _, err := s.EndGoal(item.ID); err != nil {
		t.Fatal(err)
	}
	if ready, err := s.PrepareGoalDispatch(item.ID, oldGoal); err != nil || ready {
		t.Fatalf("cancelled goal queued = %v, %v", ready, err)
	}
	if _, err := s.SetMode(item.ID, models.AgentModeGoal); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTurn(item.ID, models.ActorUser, "New goal"); err != nil {
		t.Fatal(err)
	}
	if ready, err := s.PrepareGoalDispatch(item.ID, oldGoal); err != nil || ready {
		t.Fatalf("old generation queued = %v, %v", ready, err)
	}
	if ok, err := s.AcknowledgeDispatch(item.ID, oldGoal); err != nil || ok {
		t.Fatalf("old generation admitted = %v, %v", ok, err)
	}
}

func TestConcurrentGoalArmingHasOneOwner(t *testing.T) {
	s := goalStore(t)
	first, second := goalTestItem(t, s, ""), goalTestItem(t, s, "")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{first.ID, second.ID} {
		wg.Go(func() {
			independent := &Store{Root: s.Root}
			_, err := independent.SetMode(id, models.AgentModeGoal)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("got %d successful goal owners", successes)
	}
	if _, found, err := s.GoalItem(); err != nil || !found {
		t.Fatalf("owner = %v, %v", found, err)
	}
}

func TestGoalDispatchMembershipAndDelegation(t *testing.T) {
	s := goalStore(t)
	root := goalTestItem(t, s, "")
	other := goalTestItem(t, s, "")
	if _, err := s.SetMode(root.ID, models.AgentModeGoal); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTurn(root.ID, models.ActorUser, "Delegate work"); err != nil {
		t.Fatal(err)
	}
	child, err := s.CreateSubthreadBy(root.ID, "delegate", "do work", models.TypeThread, models.StatusActive, models.ActorAgent)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := s.CreateSubthreadBy(root.ID, "question", "pick one", models.TypeThread, models.StatusPendingUser, models.ActorAgent)
	if err != nil {
		t.Fatal(err)
	}
	if child.Status != models.StatusPendingAgent || decision.Status != models.StatusPendingUser {
		t.Fatalf("delegate/decision = %s/%s", child.Status, decision.Status)
	}
	if _, err := s.SetStatus(other.ID, models.StatusPendingAgent); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.AcknowledgeDispatch(other.ID, time.Time{}); err != nil || ok {
		t.Fatalf("unrelated root admitted = %v, %v", ok, err)
	}
	if ok, err := s.AcknowledgeDispatch(child.ID, time.Time{}); err != nil || !ok {
		t.Fatalf("delegate admitted = %v, %v", ok, err)
	}
	for _, action := range []func() error{
		func() error { _, err := s.SetStatus(root.ID, models.StatusBacklog); return err },
		func() error { return s.DeleteItem(root.ID) },
		func() error { _, err := s.ReparentItem(root.ID, other.ID, true); return err },
	} {
		if err := action(); err == nil || !strings.Contains(err.Error(), "end goal mode") {
			t.Fatalf("goal lifecycle change = %v", err)
		}
	}
	if _, err := s.EndGoal(root.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMode(child.ID, models.AgentModeGoal); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTurn(child.ID, models.ActorUser, "Child-only work"); err != nil {
		t.Fatal(err)
	}
	owner, _, _ := s.GoalItem()
	if GoalAllows(owner, decision) || GoalAllows(owner, root) || !GoalAllows(owner, child) {
		t.Fatal("child goal escaped its item")
	}
	sibling, err := s.CreateSubthreadBy(child.ID, "sibling", "do work", models.TypeThread, models.StatusActive, models.ActorAgent)
	if err != nil {
		t.Fatal(err)
	}
	if sibling.Status != models.StatusActive || GoalAllows(owner, sibling) {
		t.Fatal("sibling joined child goal")
	}
}

func TestConcurrentGoalActivityWritesPreserveDecisions(t *testing.T) {
	s := goalStore(t)
	item := goalTestItem(t, s, "")
	if err := s.AddActivity(item.ID, models.Activity{ID: "initial", Type: ActivitySubthreadClosed, Actor: models.ActorUser}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errors := make(chan error, 25)
	for range 24 {
		wg.Go(func() {
			independent := &Store{Root: s.Root}
			errors <- independent.AddActivity(item.ID, models.Activity{Type: ActivitySubthreadClosed, Actor: models.ActorUser})
		})
	}
	wg.Go(func() { errors <- s.MarkActivitiesHandled(item.ID, []string{"initial"}) })
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	activities, err := s.ListActivities(item.ID)
	if err != nil || len(activities) != 25 {
		t.Fatalf("activities = %d, %v", len(activities), err)
	}
	if !activities[0].Handled {
		t.Fatal("marking was lost to a concurrent append")
	}
	pending, err := s.PendingActivities(item.ID)
	if err != nil || len(pending) != 24 {
		t.Fatalf("pending decisions = %d, %v", len(pending), err)
	}
}

func TestGoalChildTurnNotificationsAreScopedAndConsumedByID(t *testing.T) {
	s := goalStore(t)
	root := goalTestItem(t, s, "")
	child := goalTestItem(t, s, root.ID)
	if _, err := s.AddTurn(child.ID, models.ActorAgent, "before goal"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMode(root.ID, models.AgentModeGoal); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTurn(child.ID, models.ActorAgent, "armed only"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTurn(root.ID, models.ActorUser, "goal one"); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []models.Actor{models.ActorAgent, models.ActorUser} {
		if _, err := s.AddTurn(child.ID, actor, string(actor)+" result"); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := s.PendingActivities(root.ID)
	if err != nil || len(pending) != 3 { // user child creation plus two turns
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	var turns []models.Activity
	for _, a := range pending {
		if a.Type == ActivitySubthreadTurn {
			turns = append(turns, a)
		}
	}
	if len(turns) != 2 || turns[0].TurnContent != "agent result" || turns[1].TurnContent != "user result" {
		t.Fatalf("turns = %+v", turns)
	}
	if err := s.MarkActivitiesHandled(root.ID, []string{turns[0].ID}); err != nil {
		t.Fatal(err)
	}
	// A turn arriving after the captured batch must remain unhandled.
	if _, err := s.AddTurn(child.ID, models.ActorAgent, "late result"); err != nil {
		t.Fatal(err)
	}
	pending, err = s.PendingActivities(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, a := range pending {
		if a.Type == ActivitySubthreadTurn {
			count++
			if a.ID == turns[0].ID {
				t.Fatal("consumed turn reappeared")
			}
		}
	}
	if count != 2 {
		t.Fatalf("remaining turns = %d", count)
	}
	if _, err := s.EndGoal(root.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMode(root.ID, models.AgentModeGoal); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTurn(root.ID, models.ActorUser, "goal two"); err != nil {
		t.Fatal(err)
	}
	pending, err = s.PendingActivities(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range pending {
		if a.Type == ActivitySubthreadTurn {
			t.Fatal("previous goal turn leaked into new goal")
		}
	}
	if _, err := s.SetStatusBy(child.ID, models.StatusArchived, models.ActorAgent); err != nil {
		t.Fatal(err)
	}
	pending, err = s.PendingActivities(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range pending {
		if a.Type == ActivitySubthreadClosed {
			t.Fatal("agent closure became pending")
		}
	}
}

func TestChildOwnedGoalDoesNotNotifyItsParent(t *testing.T) {
	s := goalStore(t)
	root := goalTestItem(t, s, "")
	child := goalTestItem(t, s, root.ID)
	if _, err := s.SetMode(child.ID, models.AgentModeGoal); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTurn(child.ID, models.ActorUser, "isolated goal"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTurn(child.ID, models.ActorAgent, "result"); err != nil {
		t.Fatal(err)
	}
	activities, err := s.ListActivities(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range activities {
		if a.Type == ActivitySubthreadTurn {
			t.Fatal("isolated goal turn notified parent")
		}
	}
}
