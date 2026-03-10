package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags.
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:   "agentserver",
	Short: "Self-hosted coding agent server",
	Long:  `agentserver provides a web-based interface to opencode, similar to code-server for VS Code.`,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the server version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("agentserver %s\n", Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}
