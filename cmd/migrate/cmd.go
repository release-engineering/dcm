package migrate

import (
	"fmt"

	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/joelanford/dcm/action"
)

func NewCmd() *cobra.Command {
	var (
		migrate action.Migrate
		format  = "json"
	)
	cmd := &cobra.Command{
		Use:  "migrate <indexImage>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			migrate.IndexImage = args[0]

			switch format {
			case "json":
				migrate.WriteFunc = declcfg.WriteJSON
			case "yaml":
				migrate.WriteFunc = declcfg.WriteYAML
			default:
				return fmt.Errorf("invalid output format %q, expected (json|yaml)", format)
			}

			if err := migrate.Run(cmd.Context()); err != nil {
				logrus.New().Fatal(err)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&format, "output", "o", format, "Format to use when writing DC files (json|yaml)")
	cmd.Flags().StringVarP(&migrate.OutputDir, "output-dir", "d", "index", "Directory in which to migrated index as declarative config")
	return cmd
}
