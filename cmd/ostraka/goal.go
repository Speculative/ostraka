package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/Speculative/ostraka/internal/models"
	"github.com/spf13/cobra"
)

var goalCmd = &cobra.Command{Use: "goal", Short: "Arm or end the project's single goal loop"}

var goalStartCmd = &cobra.Command{
	Use: "start <item-id>", Short: "Arm goal mode; the next user turn supplies the goal", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if itemCommandActor() == models.ActorAgent {
			return fmt.Errorf("only the user can initiate goal mode")
		}
		if _, err := mustStore().SetMode(args[0], models.AgentModeGoal); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "goal mode armed for", args[0], "— send a user turn to begin")
		return nil
	},
}

var goalEndCmd = &cobra.Command{
	Use: "end <item-id>", Short: "End goal mode using the initiating item's ID", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		caller := strings.TrimSpace(os.Getenv("OSTRAKA_AGENT_ITEM_ID"))
		if caller != "" && caller != args[0] {
			return fmt.Errorf("dispatch for %s cannot end goal owned by %s", caller, args[0])
		}
		if _, err := mustStore().EndGoal(args[0]); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "goal mode ended for", args[0])
		return nil
	},
}
