package cmd

import (
	"github.com/spf13/cobra"

	"github.com/release-engineering/dcm/cmd/add"
	"github.com/release-engineering/dcm/cmd/deprecatetruncate"
	"github.com/release-engineering/dcm/cmd/migrate"
)

func Run() error {
	root := cobra.Command{
		Use: "dcm",
	}
	root.AddCommand(
		add.NewCmd(),
		deprecatetruncate.NewCmd(),
		migrate.NewCmd(),
	)
	return root.Execute()
}
