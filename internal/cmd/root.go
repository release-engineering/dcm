package cmd

import (
	"github.com/spf13/cobra"
)

func Run() error {
	root := cobra.Command{
		Use: "dcm",
	}
	root.AddCommand(
		newAddCmd(),
		newDeprecateTruncateCmd(),
		newMigrateCmd(),
		newVersionCmd(),
	)
	return root.Execute()
}
