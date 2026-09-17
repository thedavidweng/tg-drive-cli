package commands

import (
	"context"
	"fmt"
	"strconv"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"

	"github.com/spf13/cobra"
	"github.com/thedavidweng/tg-drive-cli/internal/service"
)

// Adopt (in-place claim, ADR 0019) and repair commands. The `import` name is
// reserved for external-chat ingest: `td import <source>` (ADR 0019).

// NewAdoptCmd claims existing drive-channel messages into the index without
// moving bytes.
func NewAdoptCmd(rt Runtime) *cobra.Command {
	var unmanaged, hash, confirm, dryRun, continueOnError, rewriteCaptions bool
	var into string
	c := &cobra.Command{
		Use:   "adopt [message-id] [remote-path]",
		Short: "Adopt existing Telegram messages into the virtual file tree",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			opts := service.AdoptOptions{
				Unmanaged:       unmanaged,
				NoHash:          !hash,
				DryRun:          dryRun,
				ContinueErr:     continueOnError,
				Into:            into,
				RewriteCaptions: rewriteCaptions,
			}
			if len(args) >= 1 {
				id, err := strconv.Atoi(args[0])
				if err != nil || id <= 0 {
					return r.Error(apperr.New(apperr.ErrUsage, "message-id must be a positive integer"))
				}
				opts.MessageID = id
			}
			if len(args) >= 2 {
				opts.Dest = args[1]
			}
			if opts.MessageID == 0 && !unmanaged && !rewriteCaptions {
				return r.Error(apperr.New(apperr.ErrUsage, "adopt requires a message-id, --unmanaged, or --rewrite-captions"))
			}
			if !dryRun && !confirm {
				return r.Error(apperr.New(apperr.ErrConfirmationRequired, "adopting existing messages requires --confirm (or --dry-run)"))
			}
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			data, err := app.Adopt(context.Background(), opts)
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(data)
			}
			out := cmd.OutOrStdout()
			if rewriteCaptions {
				_, _ = fmt.Fprintf(out, "adopt %s: %d captions restored, %d replies deleted, %d skipped, %d failed\n",
					map[bool]string{true: "dry-run", false: "done"}[data.DryRun],
					data.Adopted, data.Deleted, data.Skipped, data.Failed)
			} else {
				_, _ = fmt.Fprintf(out, "adopt %s: %d adopted, %d skipped, %d failed\n",
					map[bool]string{true: "dry-run", false: "done"}[data.DryRun],
					data.Adopted, data.Skipped, data.Failed)
			}
			for _, it := range data.Items {
				if it.Action == "adopt" {
					_, _ = fmt.Fprintf(out, "  %s  msg %d  %s\n", it.Kind, it.MessageID, it.Path)
					continue
				}
				_, _ = fmt.Fprintf(out, "  %-5s msg %d  %s\n", it.Action, it.MessageID, it.Reason)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&unmanaged, "unmanaged", false, "adopt every unmanaged media/text message in the channel")
	c.Flags().StringVar(&into, "into", "/", "remote directory prefix for --unmanaged")
	c.Flags().BoolVar(&hash, "hash", false, "download each adopted file to compute and store its BLAKE3 content hash")
	c.Flags().BoolVar(&confirm, "confirm", false, "confirm adopting existing messages into the local index")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print the adopt plan without editing Telegram")
	c.Flags().BoolVar(&continueOnError, "continue-on-error", false, "continue adopting after a per-message error")
	c.Flags().BoolVar(&rewriteCaptions, "rewrite-captions", false, "restore one human caption per album, delete per-file replies, and write one td-album:v1 inventory")
	return c
}

func NewRepairCmd(rt Runtime) *cobra.Command {
	var pending, orphaned, scanErrors, deleteOrphans, confirm, hash, captions, dryRun, continueOnError bool
	c := &cobra.Command{
		Use:   "repair [path]",
		Short: "Repair index inconsistencies",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			modes := 0
			for _, selected := range []bool{pending, orphaned, scanErrors, hash, captions} {
				if selected {
					modes++
				}
			}
			if modes > 1 {
				return r.Error(apperr.New(apperr.ErrUsage, "repair modes are mutually exclusive"))
			}
			if deleteOrphans && !orphaned {
				return r.Error(apperr.New(apperr.ErrUsage, "--delete-orphaned requires --orphaned"))
			}
			if deleteOrphans && !confirm {
				return r.Error(apperr.New(apperr.ErrConfirmationRequired, "deleting orphaned Telegram messages requires --confirm"))
			}
			if (dryRun || continueOnError) && !captions {
				return r.Error(apperr.New(apperr.ErrUsage, "--dry-run and --continue-on-error require --captions"))
			}
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			ctx := context.Background()
			var data map[string]any
			pathArg := ""
			if len(args) == 1 {
				pathArg = args[0]
			}
			switch {
			case captions:
				data, err = app.RepairCaptions(ctx, pathArg, dryRun, continueOnError)
			case hash:
				data, err = app.RepairHash(ctx, pathArg)
			case len(args) == 1:
				data, err = app.RepairPath(ctx, args[0])
			case pending:
				data, err = app.RepairPending(ctx)
			case orphaned:
				data, err = app.RepairOrphaned(ctx, deleteOrphans)
			case scanErrors:
				data, err = app.RepairScanErrors(ctx)
			default:
				data, err = app.RepairPending(ctx)
			}
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
	c.Flags().BoolVar(&pending, "pending", false, "repair pending uploads")
	c.Flags().BoolVar(&orphaned, "orphaned", false, "repair orphaned messages")
	c.Flags().BoolVar(&scanErrors, "scan-errors", false, "repair scan errors")
	c.Flags().BoolVar(&deleteOrphans, "delete-orphaned", false, "delete orphaned Telegram messages instead of completing them")
	c.Flags().BoolVar(&confirm, "confirm", false, "confirm deleting orphaned Telegram messages")
	c.Flags().BoolVar(&hash, "hash", false, "download files missing a content hash and backfill it into the index and machine records")
	c.Flags().BoolVar(&captions, "captions", false, "remove td's old parent-path and path-hashtag caption scaffold")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "report caption changes without editing Telegram (requires --captions)")
	c.Flags().BoolVar(&continueOnError, "continue-on-error", false, "continue caption cleanup after an individual error")
	return c
}
