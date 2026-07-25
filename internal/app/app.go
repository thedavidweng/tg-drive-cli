package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/thedavidweng/tg-drive-cli/internal/apperr"
	"github.com/thedavidweng/tg-drive-cli/internal/config"
	"github.com/thedavidweng/tg-drive-cli/internal/db"
	"github.com/thedavidweng/tg-drive-cli/internal/mtproto"
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
		if _, ok := apperr.As(err); !ok {
			// Cobra usage errors (unknown command/flag, wrong arg count) are
			// not rendered by command handlers; print and exit as usage error.
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			code = 2
		} else if code == 0 {
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
	cmd.PersistentFlags().BoolVar(&opts.quiet, "quiet", false, "suppress non-essential output")
	cmd.PersistentFlags().BoolVar(&opts.verbose, "verbose", false, "enable verbose diagnostics")
	cmd.PersistentFlags().StringVar(&opts.channel, "channel", "", "channel title or ID")
	cmd.PersistentFlags().BoolVar(&opts.wait, "wait", false, "wait through safe Telegram flood waits")
	cmd.PersistentFlags().BoolVar(&opts.noWait, "no-wait", false, "fail immediately on Telegram flood waits")
	cmd.PersistentPreRunE = func(c *cobra.Command, args []string) error {
		if opts.wait && opts.noWait {
			return apperr.New(apperr.ErrFlagConflict, "--wait and --no-wait are mutually exclusive")
		}
		if !c.Flags().Changed("json") && envBool("TD_JSON") {
			opts.json = true
		}
		if opts.channel == "" {
			opts.channel = os.Getenv("TD_CHANNEL")
		}
		return nil
	}

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
	quiet       bool
	verbose     bool
	configPath  string
	dbPath      string
	sessionPath string
	channel     string
	wait        bool
	noWait      bool
}

func envBool(name string) bool {
	switch strings.ToLower(os.Getenv(name)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// effectiveWait resolves flood-wait behavior: flags > TD_WAIT > config.
func (o *runtimeOpts) effectiveWait(cfg config.Config) bool {
	wait := cfg.RateLimit.DefaultWait
	if v := os.Getenv("TD_WAIT"); v != "" {
		wait = envBool("TD_WAIT")
	}
	if o.wait {
		wait = true
	}
	if o.noWait {
		wait = false
	}
	return wait
}

func (o *runtimeOpts) renderer() *output.Renderer {
	r := output.New(o.json)
	r.Quiet = o.quiet
	return r
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
	tg, err := o.telegramClient(cfg, database)
	if err != nil {
		_ = database.Close()
		return nil, func() {}, err
	}
	app := &service.App{Cfg: cfg, DB: database, TG: tg, Channel: o.channel}
	cleanup := func() {
		if closer, ok := tg.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		_ = database.Close()
	}
	return app, cleanup, nil
}

func (o *runtimeOpts) telegramClient(cfg config.Config, database *db.DB) (telegram.Client, error) {
	if os.Getenv("TD_FAKE_TELEGRAM") == "1" {
		return fake.New(), nil
	}
	if cfg.Telegram.APIID == 0 || cfg.Telegram.APIHash == "" {
		return nil, apperr.New(apperr.ErrConfigMissing, "telegram API credentials missing; run: td auth setup")
	}
	if err := config.EnsureSessionDir(cfg.Storage.SessionPath); err != nil {
		return nil, err
	}
	client := mtproto.New(cfg.Telegram.APIID, cfg.Telegram.APIHash, cfg.Storage.SessionPath,
		o.effectiveWait(cfg), time.Duration(cfg.RateLimit.MaxWaitSeconds)*time.Second)
	rows, err := database.Raw().Query(`select tg_channel_id, access_hash from channels where access_hash is not null and access_hash != ''`)
	if err == nil {
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var tgID, hash string
			if err := rows.Scan(&tgID, &hash); err != nil {
				continue
			}
			chID, err1 := strconv.ParseInt(tgID, 10, 64)
			accHash, err2 := strconv.ParseInt(hash, 10, 64)
			if err1 == nil && err2 == nil {
				client.RegisterChannelAccessHash(chID, accHash)
			}
		}
	}
	return client, nil
}

func ensureTelegramConfig(cfg *config.Config, configPath string, requirePhone bool) error {
	reader := bufio.NewReader(os.Stdin)
	if cfg.Telegram.APIID == 0 {
		fmt.Fprintln(os.Stderr, "Create a Telegram app at https://my.telegram.org/apps")
		fmt.Fprint(os.Stderr, "api_id: ")
		s, _ := reader.ReadString('\n')
		id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return apperr.New(apperr.ErrConfigInvalid, "invalid api_id")
		}
		cfg.Telegram.APIID = id
	}
	if cfg.Telegram.APIHash == "" {
		fmt.Fprint(os.Stderr, "api_hash: ")
		s, _ := reader.ReadString('\n')
		cfg.Telegram.APIHash = strings.TrimSpace(s)
	}
	if requirePhone && cfg.Telegram.Phone == "" {
		fmt.Fprint(os.Stderr, "phone (international, e.g. +1234567890): ")
		s, _ := reader.ReadString('\n')
		cfg.Telegram.Phone = strings.TrimSpace(s)
	}
	return config.Save(configPath, *cfg)
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
	setup := &cobra.Command{
		Use:   "setup",
		Short: "Configure Telegram API credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := opts.renderer()
			cfg, configPath, err := opts.loadConfig()
			if err != nil {
				return r.Error(err)
			}
			if err := ensureTelegramConfig(&cfg, configPath, false); err != nil {
				return r.Error(err)
			}
			if !opts.json {
				fmt.Fprintln(os.Stderr, "saved Telegram API credentials")
				fmt.Fprintln(os.Stderr, "next: td auth login")
			}
			return r.Success(map[string]any{
				"config_path": configPath,
				"api_id":      cfg.Telegram.APIID,
				"status":      "configured",
			})
		},
	}
	login := &cobra.Command{
		Use:   "login",
		Short: "Login to Telegram",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := opts.renderer()
			cfg, configPath, err := opts.loadConfig()
			if err != nil {
				return r.Error(err)
			}
			if err := ensureTelegramConfig(&cfg, configPath, true); err != nil {
				return r.Error(err)
			}
			database, err := db.Open(cfg.Storage.DBPath)
			if err != nil {
				return r.Error(err)
			}
			defer func() { _ = database.Close() }()
			tg, err := opts.telegramClient(cfg, database)
			if err != nil {
				return r.Error(err)
			}
			app := &service.App{Cfg: cfg, DB: database, TG: tg}
			reader := bufio.NewReader(os.Stdin)
			fmt.Fprintln(os.Stderr, "Telegram will send a login code to your phone.")
			codeFn := func() (string, error) {
				fmt.Fprint(os.Stderr, "code: ")
				s, _ := reader.ReadString('\n')
				return strings.TrimSpace(s), nil
			}
			pwFn := func() (string, error) {
				fmt.Fprint(os.Stderr, "2fa password: ")
				s, _ := reader.ReadString('\n')
				return strings.TrimSpace(s), nil
			}
			data, err := app.AuthLogin(context.Background(), codeFn, pwFn)
			if err != nil {
				return r.Error(err)
			}
			if !opts.json {
				fmt.Fprintln(os.Stderr, "login successful")
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
	c.AddCommand(setup, login, status, logout)
	return c
}

// createChannelDefault is the NoOptDefVal for a bare --create-channel:
// derive the channel title from --channel or the local root's name.
const createChannelDefault = "auto"

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
			if createCh == createChannelDefault {
				// Bare --create-channel: derive the title from --channel or
				// the local root's directory name.
				createCh = opts.channel
				if createCh == "" {
					createCh = filepath.Base(strings.TrimRight(args[0], "/"))
				}
			}
			data, err := app.InitRoot(context.Background(), args[0], opts.channel, createCh, bindCh)
			if err != nil {
				return r.Error(err)
			}
			return r.Success(data)
		},
	}
	c.Flags().StringVar(&createCh, "create-channel", "", "create a new channel; bare flag derives the title, or pass --create-channel=<title>")
	c.Flags().Lookup("create-channel").NoOptDefVal = createChannelDefault
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
	var full, strict, repair, includeDeleted bool
	c := &cobra.Command{
		Use:   "scan [remote-root]",
		Short: "Scan Telegram channel and rebuild index",
		Args:  cobra.MaximumNArgs(1),
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
			data, err := app.Scan(context.Background(), service.ScanOptions{
				Full: full, Strict: strict, Repair: repair, IncludeDeleted: includeDeleted, Root: root,
			})
			if err != nil {
				return r.Error(err)
			}
			if !opts.json {
				if warn, ok := data["full_scan_warning"].(string); ok && warn != "" {
					fmt.Fprintln(os.Stderr, "warning: "+warn)
				}
			}
			return r.Success(data)
		},
	}
	c.Flags().BoolVar(&full, "full", false, "full scan")
	c.Flags().BoolVar(&strict, "strict", false, "exit on invalid messages")
	c.Flags().BoolVar(&repair, "repair", false, "repair DB-only inconsistencies")
	c.Flags().BoolVar(&includeDeleted, "include-deleted", false, "record tombstoned files")
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
				if e.Type == "dir" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "DIR  %s/\n", e.Name)
				} else {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "FILE %s  %s\n", e.Name, humanSize(e.Size))
				}
			}
			return nil
		},
	}
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
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
			if opts.json {
				return r.Success(map[string]any{"path": p, "tree": nodes})
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintln(out, p)
			renderTree(out, nodes, "")
			return nil
		},
	}
	c.Flags().IntVar(&depth, "depth", 0, "max depth")
	return c
}

func renderTree(w io.Writer, nodes []service.TreeNode, prefix string) {
	for i, n := range nodes {
		connector, childPrefix := "├── ", prefix+"│   "
		if i == len(nodes)-1 {
			connector, childPrefix = "└── ", prefix+"    "
		}
		_, _ = fmt.Fprintf(w, "%s%s%s\n", prefix, connector, n.Name)
		renderTree(w, n.Children, childPrefix)
	}
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
	var recursive, replace, skip, autoRename, noHash, continueOnError, includeEmptyDirs bool
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
				data, err := app.UploadRecursive(context.Background(), args[0], args[1], policy, continueOnError, noHash, includeEmptyDirs)
				if err != nil {
					return r.Error(err)
				}
				return r.Success(data)
			}
			if includeEmptyDirs {
				return r.Error(apperr.New(apperr.ErrUsage, "--include-empty-dirs requires --recursive"))
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
	c.Flags().BoolVar(&includeEmptyDirs, "include-empty-dirs", false, "include empty directories (unsupported in V1)")
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
	var tombstone, allowStaleManifest bool
	c := &cobra.Command{
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
			data, err := app.DeleteFile(context.Background(), args[0], service.DeleteOptions{
				Tombstone:          tombstone,
				AllowStaleManifest: allowStaleManifest,
			})
			if err != nil {
				return r.Error(err)
			}
			if !opts.json {
				if data["stale_manifest"] == true {
					fmt.Fprintln(os.Stderr, "warning: manifest reply could not be redacted and remains on Telegram")
				}
				return r.SuccessLine("deleted %s", args[0])
			}
			return r.Success(data)
		},
	}
	c.Flags().BoolVar(&tombstone, "tombstone", false, "tombstone instead of deleting the Telegram message")
	c.Flags().BoolVar(&allowStaleManifest, "allow-stale-manifest", false, "do not fail when the manifest reply cannot be redacted")
	return c
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
			if opts.json {
				return r.Success(data)
			}
			out := cmd.OutOrStdout()
			if title, ok := data["channel"].(string); ok && title != "" {
				_, _ = fmt.Fprintf(out, "Channel: %s\n", title)
			}
			_, _ = fmt.Fprintf(out, "Invite: %v\n", data["invite_link"])
			if tag, ok := data["hashtag"].(string); ok && tag != "" {
				_, _ = fmt.Fprintf(out, "Filter: %s\n", tag)
			}
			_, _ = fmt.Fprintln(out, "\nOpen the channel, then search or tap the filter tag. In clients that show global hashtag results, choose the current channel/chat tab.")
			return nil
		},
	}
}

func newRepairCmd(opts *runtimeOpts) *cobra.Command {
	var pending, orphaned, scanErrors, deleteOrphans bool
	c := &cobra.Command{
		Use:   "repair [path]",
		Short: "Repair index inconsistencies",
		Args:  cobra.MaximumNArgs(1),
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
			return r.Success(data)
		},
	}
	c.Flags().BoolVar(&pending, "pending", false, "repair pending uploads")
	c.Flags().BoolVar(&orphaned, "orphaned", false, "repair orphaned messages")
	c.Flags().BoolVar(&scanErrors, "scan-errors", false, "repair scan errors")
	c.Flags().BoolVar(&deleteOrphans, "delete-orphaned", false, "delete orphaned Telegram messages instead of completing them")
	return c
}

// Ensure telegram import is used when swapping clients.
var _ telegram.Client = fake.New()
