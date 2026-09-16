package commands

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/internal/service"
)

// td import <source>: bring content in from an external Telegram chat by
// re-uploading fresh bytes (ADR 0019). The source implemented here is `saved`,
// account's Saved Messages chat, whose forwarded copies depend on origin
// channels that can delete them (ADR 0020). The in-place claim of messages
// already in the drive channel is td adopt.

// NewImportCmd builds the import command.
func NewImportCmd(rt Runtime) *cobra.Command {
	var into, photosAs string
	var mergeCaptions, deleteSource, noDedupe bool
	var replace, skipExisting, autoRename bool
	var dryRun, confirm, continueOnError, events bool
	// Kept only to reject the pre-ADR-0019 in-place claim forms.
	var unmanaged, hash, rewriteCaptions bool

	c := &cobra.Command{
		Use:   "import <source> [message-id...]",
		Short: "Import content from an external Telegram chat into the drive",
		Long: "Import content from an external Telegram chat into the drive by re-uploading fresh bytes.\n\n" +
			"The only source is `saved` (Saved Messages). Claiming messages that are already in the\n" +
			"drive channel is `td adopt`.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			pointer := "the in-place claim moved to td adopt; external sources begin with td import saved"
			oldFlag := cmd.Flags().Changed("unmanaged") || cmd.Flags().Changed("hash") ||
				cmd.Flags().Changed("rewrite-captions")
			if len(args) == 0 {
				return r.Error(apperr.New(apperr.ErrUsage, pointer))
			}
			if args[0] != "saved" {
				return r.Error(apperr.New(apperr.ErrUsage,
					fmt.Sprintf("unknown import source %q; the only source is `saved`; %s", args[0], pointer)))
			}
			if oldFlag {
				return r.Error(apperr.New(apperr.ErrFlagConflict, pointer))
			}
			ids, err := parseMessageIDs(args[1:])
			if err != nil {
				return r.Error(err)
			}
			policy, err := conflictPolicy(replace, skipExisting, autoRename)
			if err != nil {
				return r.Error(err)
			}
			switch photosAs {
			case "", service.PhotosAsDocument, service.PhotosAsPhoto:
			default:
				return r.Error(apperr.New(apperr.ErrUsage,
					fmt.Sprintf("unknown --photos-as value %q (want document or photo)", photosAs)))
			}
			if !dryRun && !confirm {
				return r.Error(apperr.New(apperr.ErrConfirmationRequired,
					"importing saved messages republishes content and requires --confirm (or --dry-run)"))
			}
			if deleteSource && !confirm {
				return r.Error(apperr.New(apperr.ErrConfirmationRequired,
					"--delete-source deletes saved originals and requires --confirm"))
			}

			opts := service.ImportSavedOptions{
				MessageIDs:    ids,
				Into:          into,
				PhotosAs:      photosAs,
				Policy:        policy,
				MergeCaptions: mergeCaptions,
				DeleteSource:  deleteSource,
				NoDedupe:      noDedupe,
				DryRun:        dryRun,
				ContinueErr:   continueOnError,
			}
			// A missing photo choice is answered interactively, and stays a
			// usage error everywhere a human cannot answer: a machine-readable
			// run must never inherit a presentation nobody picked.
			if photosAs == "" && !rt.JSON() && stdinIsInteractive() {
				opts.PhotoPrompt = func(count int) (string, error) {
					return promptPhotosAs(cmd, count)
				}
			}
			if events {
				opts.Emit = func(event string, payload any) { _ = r.Event(event, payload) }
			}

			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			data, err := app.ImportSaved(context.Background(), opts)
			if err != nil {
				return r.Error(err)
			}
			if events {
				return r.Event("import", data)
			}
			if rt.JSON() {
				return r.Success(data)
			}
			printImportSavedResult(cmd, data)
			return nil
		},
	}
	c.Flags().StringVar(&into, "into", service.DefaultImportSavedInto, "remote directory the imported content lands in")
	c.Flags().StringVar(&photosAs, "photos-as", "", "republish photo messages as document (keeps the bytes) or photo (native, recompressed)")
	c.Flags().BoolVar(&mergeCaptions, "merge-captions", false, "append a skipped duplicate's caption to the matched file's caption")
	c.Flags().BoolVar(&deleteSource, "delete-source", false, "delete the saved originals of published and duplicate items (requires --confirm)")
	c.Flags().BoolVar(&noDedupe, "no-dedupe", false, "import even when the content hash already exists in the tree")
	c.Flags().BoolVar(&replace, "replace", false, "replace an existing file at the destination")
	c.Flags().BoolVar(&skipExisting, "skip-existing", false, "skip items whose destination already exists")
	c.Flags().BoolVar(&autoRename, "auto-rename", false, "auto-rename items whose destination already exists")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print the import plan without touching Telegram")
	c.Flags().BoolVar(&confirm, "confirm", false, "confirm republishing saved content into the drive channel")
	c.Flags().BoolVar(&continueOnError, "continue-on-error", false, "continue importing after a per-item error")
	c.Flags().BoolVar(&events, "events", false, "emit NDJSON progress events during the import")
	c.Flags().BoolVar(&unmanaged, "unmanaged", false, "kept only to reject the old in-place claim form")
	c.Flags().BoolVar(&hash, "hash", false, "kept only to reject the old in-place claim form")
	c.Flags().BoolVar(&rewriteCaptions, "rewrite-captions", false, "kept only to reject the old in-place claim form")
	for _, name := range []string{"unmanaged", "hash", "rewrite-captions"} {
		_ = c.Flags().MarkHidden(name)
	}
	return c
}

// parseMessageIDs reads the optional source message ids.
func parseMessageIDs(args []string) ([]int, error) {
	var out []int
	for _, a := range args {
		id, err := strconv.Atoi(a)
		if err != nil || id <= 0 {
			return nil, apperr.New(apperr.ErrUsage,
				fmt.Sprintf("message id %q must be a positive integer", a))
		}
		out = append(out, id)
	}
	return out, nil
}

// stdinIsInteractive reports whether a human can answer a prompt.
func stdinIsInteractive() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// promptPhotosAs asks how photo messages should be republished. Prompts go to
// stderr so stdout stays machine-readable.
func promptPhotosAs(cmd *cobra.Command, count int) (string, error) {
	w := cmd.ErrOrStderr()
	_, _ = fmt.Fprintf(w, "%d photo message(s) to import. Choose how to republish them:\n", count)
	_, _ = fmt.Fprintln(w, "  document  keep the original bytes, hash-verifiable")
	_, _ = fmt.Fprintln(w, "  photo     native photo card, recompressed by Telegram")
	_, _ = fmt.Fprint(w, "photos-as [document/photo]: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return "", apperr.New(apperr.ErrUsage, "no photo presentation chosen; pass --photos-as document or --photos-as photo")
	}
	return strings.TrimSpace(line), nil
}

// printImportSavedResult renders the human summary.
func printImportSavedResult(cmd *cobra.Command, data *service.ImportSavedResult) {
	out := cmd.OutOrStdout()
	mode := "done"
	if data.DryRun {
		mode = "dry-run"
	}
	_, _ = fmt.Fprintf(out, "import saved %s: %d imported, %d skipped (%d duplicate), %d failed\n",
		mode, data.Imported, data.Skipped, data.Duplicates, data.Failed)
	if data.CaptionsMerged > 0 || data.SourcesDeleted > 0 {
		_, _ = fmt.Fprintf(out, "  %d caption(s) merged, %d source(s) deleted\n",
			data.CaptionsMerged, data.SourcesDeleted)
	}
	if !data.HistoryComplete {
		_, _ = fmt.Fprintln(out, "  note: the saved-chat read stopped early; rerun to import the rest")
	}
	for _, it := range data.Items {
		switch it.Action {
		case "import":
			_, _ = fmt.Fprintf(out, "  %-8s msg %d  %s\n", it.Kind, it.MessageID, it.Path)
		case "skip":
			_, _ = fmt.Fprintf(out, "  %-8s msg %d  %s\n", "skip", it.MessageID, it.Reason)
		default:
			_, _ = fmt.Fprintf(out, "  %-8s msg %d  %s\n", "fail", it.MessageID, it.Error)
		}
	}
}
