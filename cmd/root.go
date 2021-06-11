package cmd

import (
	"github.com/spf13/cobra"

	"github.com/joelanford/dcm/cmd/add"
	"github.com/joelanford/dcm/cmd/deprecatetruncate"
	"github.com/joelanford/dcm/cmd/migrate"
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
