package main

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the installed Ostraka version",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		info, _ := debug.ReadBuildInfo()
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "ostraka", buildVersion(info))
		return err
	},
}

// Go embeds the module version for versioned installs and derives it from Git
// for checkout builds. Read the binary's metadata, independent of the current
// directory or whether Git is installed on the user's machine.
func buildVersion(info *debug.BuildInfo) string {
	if info == nil || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "(devel)"
	}
	return info.Main.Version
}
