package commands

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

// Channel selection, listing, and bound-status commands.

func NewChannelsCmd(rt Runtime) *cobra.Command {
	var onlyDrive bool
	c := &cobra.Command{Use: "channels", Short: "List Telegram channels"}
	GroupUsage(rt, c)
	list := &cobra.Command{
		Use:   "list",
		Short: "List channels",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			chs, err := app.ListChannels(context.Background(), onlyDrive)
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(map[string]any{"channels": channelsToMap(chs)})
			}
			out := cmd.OutOrStdout()
			if len(chs) == 0 {
				_, _ = fmt.Fprintln(out, "no channels")
				return nil
			}
			for i, ch := range chs {
				_, _ = fmt.Fprintf(out, "%3d. %s (id %d)\n", i+1, ch.Title, ch.ID)
			}
			return nil
		},
	}
	list.Flags().BoolVar(&onlyDrive, "only-drive", false, "show only [TD] channels")
	c.AddCommand(list)
	link := &cobra.Command{
		Use:   "link-discussion",
		Short: "Create and link a discussion group for the bound channel (carries machine records, ADR 0018)",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			res, err := app.LinkDiscussionGroup(context.Background())
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(res)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "linked discussion group %q (id %d)\n", res["discussion_title"], res["discussion_channel_id"])
			return nil
		},
	}
	c.AddCommand(link)
	return c
}

func NewStatusCmd(rt Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show index status",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			data, err := app.Status(context.Background())
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(data)
			}
			PrintKV(cmd.OutOrStdout(), data)
			return nil
		},
	}
}
