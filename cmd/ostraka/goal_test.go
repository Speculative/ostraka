package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/Speculative/ostraka/internal/store"
	"github.com/spf13/cobra"
)

func TestGoalCLIRequiresOwnerIDAndRejectsChildCaller(t *testing.T) {
	project := t.TempDir()
	st, err := store.NewStore(filepath.Join(project, ".ostraka"))
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := st.CreateItem(models.ChannelInbox, "goal", "work", models.TypeThread, models.StatusActive, "")
	child, _ := st.CreateSubthread(owner.ID, "child", "work", models.TypeThread, models.StatusActive)
	t.Chdir(project)
	t.Setenv("OSTRAKA_AGENT_ITEM_ID", "")
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	if err := goalStartCmd.RunE(cmd, []string{owner.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTurn(owner.ID, models.ActorUser, "Finish work"); err != nil {
		t.Fatal(err)
	}
	if err := goalEndCmd.Args(cmd, nil); err == nil {
		t.Fatal("end accepted no owner ID")
	}
	if err := goalEndCmd.RunE(cmd, []string{child.ID}); err == nil {
		t.Fatal("wrong ID cancelled goal")
	}
	t.Setenv("OSTRAKA_AGENT_ITEM_ID", child.ID)
	if err := goalEndCmd.RunE(cmd, []string{owner.ID}); err == nil || !strings.Contains(err.Error(), "cannot end") {
		t.Fatalf("child caller result = %v", err)
	}
	if err := goalStartCmd.RunE(cmd, []string{child.ID}); err == nil {
		t.Fatal("agent initiated goal mode")
	}
	t.Setenv("OSTRAKA_AGENT_ITEM_ID", owner.ID)
	if err := goalEndCmd.RunE(cmd, []string{owner.ID}); err != nil {
		t.Fatal(err)
	}
	item, err := st.GetItem(owner.ID)
	if err != nil || item.Mode != models.AgentModeCode || item.Goal != nil {
		t.Fatalf("end result = %+v, %v", item, err)
	}
}
