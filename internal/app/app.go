package app

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thedavidweng/tg-drive-cli/internal/apperr"
	"github.com/thedavidweng/tg-drive-cli/internal/config"
	"github.com/thedavidweng/tg-drive-cli/internal/db"
	"github.com/thedavidweng/tg-drive-cli/internal/output"
	"github.com/thedavidweng/tg-drive-cli/internal/service"
	"github.com/thedavidweng/tg-drive-cli/internal/telegram"
	"github.com/thedavidweng/tg-drive-cli/internal/telegram/fake"
	"github.com/thedavidweng/tg-drive-cli/internal/version"
)

// Execute runs the root command.
func Execute() error {
	cmd := NewRootCommand()
	if err := cmd.Execute(); err != nil {
		code := apperr.ExitCode(err)
		if code == 0 {
			code = 1
		}
		os.Exit(code)
	}
	return nil
}

// NewRootCommand builds the CLI root.
func NewRootCommand() *cobra.Command {
	opts := &runtimeOpts{}
	cmd := &cobra.Command{
		Use:           "td",
		Short:         "Telegram-backed virtual file tree CLI",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Version,
	}
	cmd.SetVersionTemplate("td {{.Version}} (" + version.Commit + ")\n")

	cmd.PersistentFlags().BoolVar(&opts.json, "json", false, "write JSON output")
	cmd.PersistentFlags().StringVar(&opts.configPath, "config", "", "config file path")
	cmd.PersistentFlags().StringVar(&opts.dbPath, "db", "", "SQLite database path")
	cmd.PersistentFlags().StringVar(&opts.sessionPath, "session", "", "Telegram session path")
	cmd.PersistentFlags().Bool("quiet", false, "suppress non-essential output")
	cmd.PersistentFlags().Bool("verbose", false, "enable verbose diagnostics")
	cmd.PersistentFlags().StringVar(&opts.channel, "channel", "", "channel title or ID")
	cmd.PersistentFlags().Bool("wait", false, "wait through safe Telegram flood waits")

	cmd.AddCommand(newVersionCmd(opts))
	cmd.AddCommand(newDoctorCmd(opts))
	cmd.AddCommand(newConfigCmd(opts))
	cmd.AddCommand(newAuthCmd(opts))
	cmd.AddCommand(newInitCmd(opts))
	cmd.AddCommand(newStatusCmd(opts))
	cmd.AddCommand(newScanCmd(opts))
	cmd.AddCommand(newLsCmd(opts))
	cmd.AddCommand(newTreeCmd(opts))
	cmd.AddCommand(newCpCmd(opts))
	cmd.AddCommand(newGetCmd(opts))
	cmd.AddCommand(newMvCmd(opts))
	cmd.AddCommand(newRmCmd(opts))
	cmd.AddCommand(newShareCmd(opts))
	cmd.AddCommand(newRepairCmd(opts))

	return cmd
}

type runtimeOpts struct {
	json        bool
	configPath  string
	dbPath      string
	sessionPath string
	channel     string
}

func (o *runtimeOpts) renderer() *output.Renderer {
	return output.New(o.json)
}

func (o *runtimeOpts) overrides() config.Overrides {
	return config.Overrides{
		ConfigPath:  o.configPath,
		DBPath:      o.dbPath,
		SessionPath: o.sessionPath,
		Channel:     o.channel,
	}
}

func (o *runtimeOpts) loadConfig() (config.Config, string, error) {
	return config.Load(o.overrides())
}

func (o *runtimeOpts) openApp(cmd *cobra.Command) (*service.App, func(), error) {
	if isLightweight(cmd) {
		return nil, func() {}, apperr.New(apperr.ErrUsage, "command does not use app context")
	}
	cfg, _, err := o.loadConfig()
	if err != nil {
		return nil, func() {}, err
	}
	database, err := db.Open(cfg.Storage.DBPath)
	if err != nil {
		return nil, func() {}, err
	}
	tg := fake.New()
	app := &service.App{Cfg: cfg, DB: database, TG: tg}
	cleanup := func() { _ = database.Close() }
	return app, cleanup, nil
}

func isLightweight(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "version", "completion":
			return true
		}
	}
	return false
}

func newVersionCmd(opts *runtimeOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := opts.renderer()
			if opts.json {
				return r.Success(map[string]string{
					"version":  version.Version,
					"commit":   version.Commit,
					"date":     version.Date,
					"built_by": version.BuiltBy,
				})
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "td %s (%s)\n", version.Version, version.Commit)
			return err
		},
	}
}

func newDoctorCmd(opts *runtimeOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check local and Telegram capabilities",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := opts.renderer()
			app, cleanup, err := opts.openApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			data, err := app.Doctor(context.Background())
			if err != nil {
				return r.Error(err)
			}
			return r.Success(data)
		},
	}
}

func newConfigCmd(opts *runtimeOpts) *cobra.Command {
	var showSecrets, confirm bool
	c := &cobra.Command{Use: "config", Short: "Manage configuration"}
	get := &cobra.Command{
		Use:   "get [key]",
		Short: "Get config value(s)",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := opts.renderer()
			cfg, configPath, err := opts.loadConfig()
			if err != nil {
				return r.Error(err)
			}
			if showSecrets && !opts.json {
				fmt.Fprint(os.Stderr, "show secrets? [y/N] ")
				var ans string
				_, _ = fmt.Scanln(&ans)
				if strings.ToLower(ans) != "y" {
					showSecrets = false
				}
			}
			if showSecrets && opts.json && !confirm {
				return r.Error(apperr.New(apperr.ErrUsage, "--confirm required with --json --show-secrets"))
			}
			if len(args) == 0 {
				return r.Success(config.RedactConfigMap(cfg, showSecrets))
			}
			v, err := config.GetValue(cfg, args[0])
			if err != nil {
				return r.Error(err)
			}
			return r.Success(map[string]any{args[0]: config.RedactValue(args[0], v, showSecrets), "config_path": configPath})
		},
	}
	get.Flags().BoolVar(&showSecrets, "show-secrets", false, "show secret values")
	get.Flags().BoolVar(&confirm, "confirm", false, "confirm showing secrets in JSON mode")
	set := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set config value",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := opts.renderer()
			cfg, configPath, err := opts.loadConfig()
			if err != nil {
				return r.Error(err)
			}
			if err := config.SetValue(&cfg, args[0], args[1]); err != nil {
				return r.Error(err)
			}
			if err := config.Save(configPath, cfg); err != nil {
				return r.Error(err)
			}
			return r.Success(map[string]string{"key": args[0], "status": "set"})
		},
	}
	c.AddCommand(get, set)
	return c
}

func newAuthCmd(opts *runtimeOpts) *cobra.Command {
	c := &cobra.Command{Use: "auth", Short: "Authentication commands"}
	login := &cobra.Command{
		Use:   "login",
		Short: "Login to Telegram",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := opts.renderer()
			app, cleanup, err := opts.openApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			reader := bufio.NewReader(os.Stdin)
			codeFn := func() (string, error) {
				fmt.Fprint(os.Stderr, "code: ")
				s, _ := reader.ReadString('\n')
				return strings.TrimSpace(s), nil
			}
			pwFn := func() (string, error) {
				fmt.Fprint(os.Stderr, "password: ")
				s, _ := reader.ReadString('\n')
				return strings.TrimSpace(s), nil
			}
			data, err := app.AuthLogin(context.Background(), codeFn, pwFn)
			if err != nil {
				return r.Error(err)
			}
			return r.Success(data)
		},
	}
	status := &cobra.Command{
		Use:   "status",
		Short: "Show auth status",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := opts.renderer()
			app, cleanup, err := opts.openApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			data, err := app.AuthStatus(context.Background())
			if err != nil {
				return r.Error(err)
			}
			return r.Success(data)
		},
	}
	logout := &cobra.Command{
		Use:   "logout",
		Short: "Logout",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := opts.renderer()
			app, cleanup, err := opts.openApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			if err := app.AuthLogout(context.Background()); err != nil {
				return r.Error(err)
			}
			return r.Success(map[string]string{"status": "logged_out"})
		},
	}
	c.AddCommand(login, status, logout)
	return c
}

func newInitCmd(opts *runtimeOpts) *cobra.Command {
	var createCh, bindCh string
	c := &cobra.Command{
		Use:   "init <local-root>",
		Short: "Initialize a local root",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := opts.renderer()
			app, cleanup, err := opts.openApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			data, err := app.InitRoot(context.Background(), args[0], opts.channel, createCh, bindCh)
			if err != nil {
				return r.Error(err)
			}
			return r.Success(data)
		},
	}
	c.Flags().StringVar(&createCh, "create-channel", "", "create a new channel")
	c.Flags().StringVar(&bindCh, "bind-channel", "", "bind an existing channel")
	return c
}

func newStatusCmd(opts *runtimeOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show index status",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := opts.renderer()
			app, cleanup, err := opts.openApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			data, err := app.Status(context.Background())
			if err != nil {
				return r.Error(err)
			}
			return r.Success(data)
		},
	}
}

func newScanCmd(opts *runtimeOpts) *cobra.Command {
	var full, strict bool
	c := &cobra.Command{
		Use:   "scan [remote-root]",
		Short: "Scan Telegram channel and rebuild index",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := opts.renderer()
			app, cleanup, err := opts.openApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			root := "/"
			if len(args) > 0 {
				root = args[0]
			}
			data, err := app.Scan(context.Background(), service.ScanOptions{Full: full, Strict: strict, Root: root})
			if err != nil {
				return r.Error(err)
			}
			return r.Success(data)
		},
	}
	c.Flags().BoolVar(&full, "full", false, "full scan")
	c.Flags().BoolVar(&strict, "strict", false, "exit on invalid messages")
	return c
}

func newLsCmd(opts *runtimeOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "ls [remote-path]",
		Short: "List remote directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := opts.renderer()
			app, cleanup, err := opts.openApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			p := "/"
			if len(args) > 0 {
				p = args[0]
			}
			entries, err := app.ListDir(context.Background(), p)
			if err != nil {
				return r.Error(err)
			}
			if opts.json {
				return r.Success(map[string]any{"path": p, "entries": entries})
			}
			for _, e := range entries {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", e.Type, e.Name)
			}
			return nil
		},
	}
}

func newTreeCmd(opts *runtimeOpts) *cobra.Command {
	var depth int
	c := &cobra.Command{
		Use:   "tree [remote-path]",
		Short: "Show remote tree",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := opts.renderer()
			app, cleanup, err := opts.openApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			p := "/"
			if len(args) > 0 {
				p = args[0]
			}
			nodes, err := app.Tree(context.Background(), p, depth)
			if err != nil {
				return r.Error(err)
			}
			return r.Success(map[string]any{"path": p, "tree": nodes})
		},
	}
	c.Flags().IntVar(&depth, "depth", 0, "max depth")
	return c
}

func conflictPolicy(replace, skip, autoRename bool) (service.ConflictPolicy, error) {
	n := 0
	if replace {
		n++
	}
	if skip {
		n++
	}
	if autoRename {
		n++
	}
	if n > 1 {
		return "", apperr.New(apperr.ErrFlagConflict, "only one conflict flag allowed")
	}
	if replace {
		return service.ConflictReplace, nil
	}
	if skip {
		return service.ConflictSkip, nil
	}
	if autoRename {
		return service.ConflictRename, nil
	}
	return service.ConflictFail, nil
}

func newCpCmd(opts *runtimeOpts) *cobra.Command {
	var recursive, replace, skip, autoRename, noHash, continueOnError bool
	c := &cobra.Command{
		Use:   "cp <local> <remote-path>",
		Short: "Upload local file or directory",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := opts.renderer()
			app, cleanup, err := opts.openApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			policy, err := conflictPolicy(replace, skip, autoRename)
			if err != nil {
				return r.Error(err)
			}
			if recursive {
				data, err := app.UploadRecursive(context.Background(), args[0], args[1], policy, continueOnError, noHash)
				if err != nil {
					return r.Error(err)
				}
				return r.Success(data)
			}
			data, err := app.UploadFile(context.Background(), args[0], args[1], policy, noHash)
			if err != nil {
				return r.Error(err)
			}
			if !opts.json {
				return r.SuccessLine("uploaded %s", data["path"])
			}
			return r.Success(data)
		},
	}
	c.Flags().BoolVar(&recursive, "recursive", false, "upload directory recursively")
	c.Flags().BoolVar(&replace, "replace", false, "replace existing remote file")
	c.Flags().BoolVar(&skip, "skip-existing", false, "skip existing remote file")
	c.Flags().BoolVar(&autoRename, "auto-rename", false, "auto rename on conflict")
	c.Flags().BoolVar(&noHash, "no-hash", false, "skip content hash")
	c.Flags().BoolVar(&continueOnError, "continue-on-error", false, "continue on upload errors")
	return c
}

func newGetCmd(opts *runtimeOpts) *cobra.Command {
	var recursive, replace, skip, autoRename, continueOnError bool
	c := &cobra.Command{
		Use:   "get <remote-path> <local-dest>",
		Short: "Download remote file or directory",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := opts.renderer()
			app, cleanup, err := opts.openApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			policy, err := conflictPolicy(replace, skip, autoRename)
			if err != nil {
				return r.Error(err)
			}
			if recursive {
				if err := app.DownloadRecursive(context.Background(), args[0], args[1], policy, continueOnError); err != nil {
					return r.Error(err)
				}
				return r.Success(map[string]string{"path": args[0], "local": args[1]})
			}
			if err := app.DownloadFile(context.Background(), args[0], args[1], policy); err != nil {
				return r.Error(err)
			}
			if !opts.json {
				return r.SuccessLine("downloaded %s", args[0])
			}
			return r.Success(map[string]string{"path": args[0], "local": args[1]})
		},
	}
	c.Flags().BoolVar(&recursive, "recursive", false, "download directory recursively")
	c.Flags().BoolVar(&replace, "replace", false, "replace existing local file")
	c.Flags().BoolVar(&skip, "skip-existing", false, "skip existing local file")
	c.Flags().BoolVar(&autoRename, "auto-rename", false, "auto rename on conflict")
	c.Flags().BoolVar(&continueOnError, "continue-on-error", false, "continue on download errors")
	return c
}

func newMvCmd(opts *runtimeOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "mv <remote-from> <remote-to>",
		Short: "Move or rename remote file",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := opts.renderer()
			app, cleanup, err := opts.openApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			if err := app.MoveFile(context.Background(), args[0], args[1]); err != nil {
				return r.Error(err)
			}
			if !opts.json {
				return r.SuccessLine("moved %s -> %s", args[0], args[1])
			}
			return r.Success(map[string]string{"from": args[0], "to": args[1]})
		},
	}
}

func newRmCmd(opts *runtimeOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <remote-path>",
		Short: "Delete remote file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := opts.renderer()
			app, cleanup, err := opts.openApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			if err := app.DeleteFile(context.Background(), args[0]); err != nil {
				return r.Error(err)
			}
			if !opts.json {
				return r.SuccessLine("deleted %s", args[0])
			}
			return r.Success(map[string]string{"path": args[0]})
		},
	}
}

func newShareCmd(opts *runtimeOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "share [remote-path]",
		Short: "Share invite link and hashtag",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := opts.renderer()
			app, cleanup, err := opts.openApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			p := "/"
			if len(args) > 0 {
				p = args[0]
			}
			data, err := app.Share(context.Background(), p)
			if err != nil {
				return r.Error(err)
			}
			return r.Success(data)
		},
	}
}

func newRepairCmd(opts *runtimeOpts) *cobra.Command {
	var pending, orphaned, scanErrors bool
	c := &cobra.Command{
		Use:   "repair [path]",
		Short: "Repair index inconsistencies",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := opts.renderer()
			app, cleanup, err := opts.openApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			ctx := context.Background()
			var data map[string]any
			switch {
			case pending:
				data, err = app.RepairPending(ctx)
			case orphaned:
				data, err = app.RepairOrphaned(ctx)
			case scanErrors:
				data, err = app.RepairScanErrors(ctx)
			default:
				data, err = app.RepairPending(ctx)
			}
			if err != nil {
				return r.Error(err)
			}
			return r.Success(data)
		},
	}
	c.Flags().BoolVar(&pending, "pending", false, "repair pending uploads")
	c.Flags().BoolVar(&orphaned, "orphaned", false, "repair orphaned messages")
	c.Flags().BoolVar(&scanErrors, "scan-errors", false, "repair scan errors")
	return c
}

// Ensure telegram import is used when swapping clients.
var _ telegram.Client = fake.New()
