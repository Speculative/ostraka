package supervisor

import (
	"strings"
	"testing"
	"time"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
)

func TestDispatchReminderForMissingReply(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		for _, tc := range []struct {
			name       string
			traces     []models.PartialTrace
			activities []models.Activity
			postReply  bool
			want       bool
		}{
			{name: "never dispatched"},
			{name: "completed trace", traces: []models.PartialTrace{{Status: "completed"}}, want: true},
			{name: "silent completion", activities: []models.Activity{{Type: store.ActivityAgentEndedWithoutFinalResponse, Result: "completed"}}, want: true},
			{name: "linked reply", traces: []models.PartialTrace{{Status: "completed", TurnTimestamp: time.Now()}}},
			{name: "later reply", traces: []models.PartialTrace{{Status: "completed"}}, postReply: true},
			{name: "later reply after silent completion", activities: []models.Activity{{Type: store.ActivityAgentEndedWithoutFinalResponse, Result: "completed"}}, postReply: true},
			{name: "failed trace", traces: []models.PartialTrace{{Status: "failed"}}},
			{name: "interrupted trace", traces: []models.PartialTrace{{Status: "interrupted"}}},
			{name: "silent failure", activities: []models.Activity{{Type: store.ActivityAgentEndedWithoutFinalResponse, Result: "failed"}}},
			{name: "latest run posted", traces: []models.PartialTrace{{Status: "completed"}, {Status: "completed", TurnTimestamp: time.Now()}}},
			{name: "latest run failed", traces: []models.PartialTrace{{Status: "completed"}, {Status: "failed"}}},
			{name: "latest run silent failed", traces: []models.PartialTrace{{Status: "completed"}}, activities: []models.Activity{{Type: store.ActivityAgentEndedWithoutFinalResponse, Result: "failed"}}},
			{name: "latest run interrupted", traces: []models.PartialTrace{{Status: "completed"}}, activities: []models.Activity{{Type: store.ActivityAgentInterrupted}}},
			{name: "latest run silent completed", traces: []models.PartialTrace{{Status: "failed"}}, activities: []models.Activity{{Type: store.ActivityAgentEndedWithoutFinalResponse, Result: "completed"}}, want: true},
		} {
			name := "resumed/" + tc.name
			if fresh {
				name = "fresh/" + tc.name
			}
			t.Run(name, func(t *testing.T) {
				h := &fakeHarness{}
				s, st := newStoreBackedSupervisor(t, h)
				item, err := st.CreateItem(models.ChannelInbox, "reminder", "body", models.TypeThread, models.StatusPendingAgent, "")
				if err != nil {
					t.Fatal(err)
				}
				if !fresh {
					if err := saveItemSession(s.root, item.ID, sessionFile{Provider: ProviderClaude, SessionID: "existing-session"}); err != nil {
						t.Fatal(err)
					}
				}
				// Records precede the current user turn. User follow-ups must not hide
				// a missing reply, whereas a posted agent reply must clear it.
				stamp := time.Now().Add(-time.Hour)
				for _, trace := range tc.traces {
					trace.Timestamp, trace.Content = stamp, "retained progress"
					if err := st.AppendPartialTrace(item.ID, trace); err != nil {
						t.Fatal(err)
					}
					stamp = stamp.Add(time.Minute)
				}
				for _, activity := range tc.activities {
					activity.Timestamp, activity.Actor = stamp, models.ActorAgent
					if err := st.AddActivity(item.ID, activity); err != nil {
						t.Fatal(err)
					}
					stamp = stamp.Add(time.Minute)
				}
				if tc.postReply {
					if _, err := st.AddTurn(item.ID, models.ActorAgent, "posted reply"); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := st.AddTurn(item.ID, models.ActorUser, "continue"); err != nil {
					t.Fatal(err)
				}
				if _, err := st.SetStatus(item.ID, models.StatusPendingAgent); err != nil {
					t.Fatal(err)
				}
				s.dispatch(enqueueMsg{itemID: item.ID})
				if len(h.prompts) != 1 {
					t.Fatalf("prompts = %v", h.prompts)
				}
				got := strings.Contains(h.prompts[0], "Your previous dispatch completed without posting an `ostraka item turn`")
				if got != tc.want {
					t.Fatalf("reminder = %v, want %v; prompt: %s", got, tc.want, h.prompts[0])
				}
				if tc.want && !strings.Contains(h.prompts[0], replyCommand(s.root, item.ID)) {
					t.Fatal("reminder omitted reply command")
				}
			})
		}
	}
}
