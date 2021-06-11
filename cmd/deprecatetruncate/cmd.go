package deprecatetruncate

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/joelanford/dcm/action"
)

func NewCmd() *cobra.Command {
	var (
		dp action.DeprecateTruncate
	)
	cmd := &cobra.Command{
		Use:   "deprecatetruncate <dcDir> <bundleImage>",
		Short: "Deprecate a bundle from a declarative config directory",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			dp.FromDir = args[0]
			dp.BundleImage = args[1]
			log := logrus.New()
			dp.Log = *log

			if err := dp.Run(cmd.Context()); err != nil {
				dp.Log.Fatal(err)
			}
		},
	}
	return cmd
}
