package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tenjin/tenjin-cli/internal/buildinfo"
	"github.com/tenjin/tenjin-cli/internal/render"
)

func newVersionCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version, commit and build date",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			info := buildinfo.Get()
			if format == render.JSON {
				return render.WriteJSON(cmd.OutOrStdout(), info)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), info)
			return err
		},
	}
}
