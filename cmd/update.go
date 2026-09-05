package cmd

import (
	"errors"

	"github.com/spf13/cobra"
)

func updateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Update the npm-managed Mnemon CLI",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return errors.New("this Mnemon installation is not managed by npm; " +
				"migrate once with: npm install --global @mnemon-dev/mnemon@latest")
		},
	}
}
