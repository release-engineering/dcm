package add

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/joelanford/dcm/action"
)

func NewCmd() *cobra.Command {
	var (
		add action.Add
	)
	cmd := &cobra.Command{
		Use:   "add <dcDir> <bundleImage>",
		Short: "Add a bundle to a declarative config directory",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			add.FromDir = args[0]
			add.BundleImage = args[1]
			log := logrus.New()
			add.Log = *log

			if err := add.Run(cmd.Context()); err != nil {
				add.Log.Fatal(err)
			}
		},
	}
	cmd.Flags().BoolVar(&add.OverwriteLatest, "overwrite-latest", false, "Allow bundles that are channel heads to be overwritten")
	return cmd
}
