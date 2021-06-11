package migrate

import (
	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/joelanford/dcm/action"
)

func NewCmd() *cobra.Command {
	var (
		migrate action.Migrate
	)
	cmd := &cobra.Command{
		Use:   "migrate <indexImage>",
		Short: "Migrate an index image to a declarative config directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			migrate.IndexImage = args[0]
			migrate.WriteFunc = declcfg.WriteYAML

			if err := migrate.Run(cmd.Context()); err != nil {
				logrus.New().Fatal(err)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&migrate.OutputDir, "output-dir", "d", "index", "Directory in which to migrated index as declarative config")
	return cmd
}
