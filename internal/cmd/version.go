package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/release-engineering/dcm/internal/version"
)

func newVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Printf("%#v\n", version.Version)
		},
	}
	return cmd
}
