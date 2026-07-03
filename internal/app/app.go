package app

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/thedavidweng/tg-drive-cli/internal/version"
)

func Execute() error {
	cmd := NewRootCommand()
	return cmd.Execute()
}

func NewRootCommand() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "td",
		Short: "Telegram-backed virtual file tree CLI",
	}
	cmd.PersistentFlags().BoolVar(&jsonOut, "json", false, "write JSON output")
	cmd.PersistentFlags().String("config", "", "config file path")
	cmd.PersistentFlags().String("db", "", "SQLite database path")
	cmd.PersistentFlags().String("session", "", "Telegram session path")
	cmd.PersistentFlags().Bool("quiet", false, "suppress non-essential output")
	cmd.PersistentFlags().Bool("verbose", false, "enable verbose diagnostics")
	cmd.PersistentFlags().String("channel", "", "channel title or ID")
	cmd.PersistentFlags().Bool("wait", false, "wait through safe Telegram flood waits")

	cmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOut {
				fmt.Fprintf(cmd.OutOrStdout(), "{"ok":true,"data":{"version":%q,"commit":%q,"date":%q,"built_by":%q}}
", version.Version, version.Commit, version.Date, version.BuiltBy)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "td %s (%s)
", version.Version, version.Commit)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "doctor",
		Short: "Check local and Telegram capabilities",
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOut {
				fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true,"data":{"status":"not_implemented"}}`)
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), "doctor: not implemented")
			return nil
		},
	})

	return cmd
}
