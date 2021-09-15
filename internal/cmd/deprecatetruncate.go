package cmd

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/release-engineering/dcm/internal/action"
)

func newDeprecateTruncateCmd() *cobra.Command {
	var (
		dp action.DeprecateTruncate
	)
	cmd := &cobra.Command{
		Use:   "deprecatetruncate <dcDir> <bundleImage>",
		Short: "Deprecate a bundle from a declarative config directory",
		Args:  cobra.MinimumNArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			dp.FromDir = args[0]
			dp.BundleImages = args[1:]
			dp.Log = logrus.New()

			if err := dp.Run(cmd.Context()); err != nil {
				dp.Log.Fatal(err)
			}
		},
	}
	return cmd
}
