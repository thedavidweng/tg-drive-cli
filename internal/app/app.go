package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/thedavidweng/tg-drive-cli/adapters/native/localfs"
	"github.com/thedavidweng/tg-drive-cli/adapters/native/sqlitestore"
	"github.com/thedavidweng/tg-drive-cli/adapters/native/telegramgotd"
	"github.com/thedavidweng/tg-drive-cli/core/drive"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
	"github.com/thedavidweng/tg-drive-cli/core/telegram/fake"
	"github.com/thedavidweng/tg-drive-cli/internal/config"
	"github.com/thedavidweng/tg-drive-cli/internal/output"
	"github.com/thedavidweng/tg-drive-cli/internal/service"
	"github.com/thedavidweng/tg-drive-cli/internal/version"
)

// Execute runs the root command.
func Execute() error {
	cmd := NewRootCommand()
	if err := cmd.Execute(); err != nil {
		code := apperr.ExitCode(err)
		if _, ok := apperr.As(err); !ok {
			// Cobra usage errors (unknown command/flag, wrong arg count) are
			// not rendered by command handlers; render as a usage error so
			// --json consumers still get an envelope.
			_ = output.New(argvWantsJSON()).Error(apperr.New(apperr.ErrUsage, err.Error()))
			code = 2
		} else if code == 0 {
			code = 1
		}
		os.Exit(code)
	}
	return nil
}

// argvWantsJSON detects --json for errors that occur before flag parsing
// completes (e.g. unknown root command).
func argvWantsJSON() bool {
	for _, a := range os.Args[1:] {
		if a == "--" {
			break
		}
		if a == "--json" || a == "--json=true" {
			return true
		}
	}
	return envBool("TD_JSON")
}

// NewRootCommand builds the CLI root.
func NewRootCommand() *cobra.Command {
	opts := &runtimeOpts{}
	cmd := &cobra.Command{
		Use:   "td",
		Short: "Telegram-backed virtual file tree CLI",
		Long: `Telegram-backed virtual file tree CLI.

Environment:
  TD_API_ID, TD_API_HASH  Telegram API credentials (https://my.telegram.org/apps)
  TD_PHONE                account phone number (international format)
  TD_CONFIG               config file path        (default ~/.config/tg-drive-cli/config.toml)
  TD_SESSION              session file path       (default ~/.config/tg-drive-cli/session.json)
  TD_DB                   local cache DB path     (default ~/.local/share/tg-drive-cli/local_cache.db)
  TD_CHANNEL              channel title or ID (same as --channel)
  TD_JSON                 set to 1 for JSON output (same as --json)
  TD_WAIT                 set to 1 to wait through safe flood waits (same as --wait)`,
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
		if !c.Flags().Changed("json") && envBool("TD_JSON") {
			opts.json = true
		}
		if opts.wait && opts.noWait {
			// Render here: errors returned from PersistentPreRunE bypass the
			// command handlers and would otherwise exit silently.
			return opts.renderer().Error(apperr.New(apperr.ErrFlagConflict, "--wait and --no-wait are mutually exclusive"))
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
	database, err := sqlitestore.Open(cfg.Storage.DBPath)
	if err != nil {
		return nil, func() {}, err
	}
	tg, err := o.telegramClient(cfg, database)
	if err != nil {
		_ = database.Close()
		return nil, func() {}, err
	}
	app := &service.App{
		Cfg:     cfg,
		DB:      database,
		TG:      tg,
		Channel: o.channel,
		Runtime: drive.NewRuntime(database, localfs.FS{}, tg),
	}
	cleanup := func() {
		if closer, ok := tg.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		_ = database.Close()
	}
	return app, cleanup, nil
}

func (o *runtimeOpts) telegramClient(cfg config.Config, database *sqlitestore.DB) (telegram.Client, error) {
	if os.Getenv("TD_FAKE_TELEGRAM") == "1" {
		if p := os.Getenv("TD_FAKE_TELEGRAM_STATE"); p != "" {
			return fake.NewPersistent(p), nil
		}
		return fake.New(), nil
	}
	if cfg.Telegram.APIID == 0 || cfg.Telegram.APIHash == "" {
		return nil, apperr.New(apperr.ErrConfigMissing, "telegram API credentials missing; run: td auth setup")
	}
	if err := config.EnsureSessionDir(cfg.Storage.SessionPath); err != nil {
		return nil, err
	}
	client := telegramgotd.New(cfg.Telegram.APIID, cfg.Telegram.APIHash, cfg.Storage.SessionPath,
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

// groupUsage makes a command group reject unknown subcommands as usage
// errors (exit 2, rendered envelope) instead of printing help with exit 0.
func groupUsage(opts *runtimeOpts, c *cobra.Command) {
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return opts.renderer().Error(apperr.New(apperr.ErrUsage,
			fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())))
	}
}

// printKV writes a sorted key: value view of a data map for human output,
// flattening nested count maps into dotted keys.
func printKV(w io.Writer, data map[string]any) {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch v := data[k].(type) {
		case map[string]int:
			if len(v) == 0 {
				_, _ = fmt.Fprintf(w, "%s: none\n", k)
				continue
			}
			subs := make([]string, 0, len(v))
			for sk := range v {
				subs = append(subs, sk)
			}
			sort.Strings(subs)
			for _, sk := range subs {
				_, _ = fmt.Fprintf(w, "%s.%s: %d\n", k, sk, v[sk])
			}
		default:
			_, _ = fmt.Fprintf(w, "%s: %v\n", k, data[k])
		}
	}
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
			if opts.json {
				return r.Success(data)
			}
			out := cmd.OutOrStdout()
			checks, _ := data["checks"].(map[string]string)
			hints, _ := data["hints"].(map[string]string)
			names := make([]string, 0, len(checks))
			for k := range checks {
				names = append(names, k)
			}
			sort.Strings(names)
			for _, name := range names {
				line := fmt.Sprintf("%-18s %s", name, checks[name])
				if hint := hints[name]; hint != "" {
					line += " — " + hint
				}
				fmt.Fprintln(out, line)
			}
			if maxBytes, ok := data["max_upload_bytes"].(int64); ok {
				fmt.Fprintf(out, "%-18s %s\n", "max_upload", humanSize(maxBytes))
			}
			return nil
		},
	}
}

func newConfigCmd(opts *runtimeOpts) *cobra.Command {
	var showSecrets, confirm bool
	c := &cobra.Command{Use: "config", Short: "Manage configuration"}
	groupUsage(opts, c)
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
				all := config.RedactConfigMap(cfg, showSecrets)
				if opts.json {
					return r.Success(all)
				}
				printKV(cmd.OutOrStdout(), all)
				return nil
			}
			v, err := config.GetValue(cfg, args[0])
			if err != nil {
				return r.Error(err)
			}
			if opts.json {
				return r.Success(map[string]any{args[0]: config.RedactValue(args[0], v, showSecrets), "config_path": configPath})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%v\n", config.RedactValue(args[0], v, showSecrets))
			return nil
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
			if opts.json {
				return r.Success(map[string]string{"key": args[0], "status": "set"})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "set %s\n", args[0])
			return nil
		},
	}
	c.AddCommand(get, set)
	return c
}

func newAuthCmd(opts *runtimeOpts) *cobra.Command {
	c := &cobra.Command{Use: "auth", Short: "Authentication commands"}
	groupUsage(opts, c)
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
			// Pre-create the session and database directories so the first
			// `td auth login` does not fail on a missing data dir.
			if err := config.EnsureSessionDir(cfg.Storage.SessionPath); err != nil {
				return r.Error(err)
			}
			database, err := sqlitestore.Open(cfg.Storage.DBPath)
			if err != nil {
				return r.Error(err)
			}
			_ = database.Close()
			if opts.json {
				return r.Success(map[string]any{
					"config_path":  configPath,
					"api_id":       cfg.Telegram.APIID,
					"db_path":      cfg.Storage.DBPath,
					"session_path": cfg.Storage.SessionPath,
					"status":       "configured",
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "saved Telegram API credentials to %s\nnext: td auth login\n", configPath)
			return nil
		},
	}
	var resend bool
	login := &cobra.Command{
		Use:   "login",
		Short: "Login to Telegram",
		Long: "Login to Telegram interactively.\n\n" +
			"Telegram sends a login code to your phone. If you re-run this command\n" +
			"while a code is still pending, the pending code is reused instead of\n" +
			"requesting a new one (repeated code requests get the account\n" +
			"rate-limited for up to 24 hours). Use --resend to request a fresh code.\n" +
			"After a successful login the session is saved and other commands do not\n" +
			"require logging in again.",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := opts.renderer()
			cfg, configPath, err := opts.loadConfig()
			if err != nil {
				return r.Error(err)
			}
			if err := ensureTelegramConfig(&cfg, configPath, true); err != nil {
				return r.Error(err)
			}
			database, err := sqlitestore.Open(cfg.Storage.DBPath)
			if err != nil {
				return r.Error(err)
			}
			defer func() { _ = database.Close() }()
			tg, err := opts.telegramClient(cfg, database)
			if err != nil {
				return r.Error(err)
			}
			app := &service.App{
				Cfg:     cfg,
				DB:      database,
				TG:      tg,
				Runtime: drive.NewRuntime(database, localfs.FS{}, tg),
			}
			reader := bufio.NewReader(os.Stdin)
			codeFn := func(p telegram.CodePrompt) (string, error) {
				switch {
				case p.Attempt > 1:
					fmt.Fprintf(os.Stderr, "invalid code, try again (%d/%d): ", p.Attempt, p.MaxAttempts)
				case p.Reused:
					age := time.Since(p.SentAt).Round(time.Second)
					fmt.Fprintf(os.Stderr, "Reusing the code Telegram sent %s ago (run with --resend for a new one).\ncode: ", age)
				case p.Resent:
					fmt.Fprint(os.Stderr, "The previous code expired; Telegram sent a new code to your phone.\ncode: ")
				default:
					fmt.Fprint(os.Stderr, "Telegram sent a login code to your phone.\ncode: ")
				}
				s, _ := reader.ReadString('\n')
				return strings.TrimSpace(s), nil
			}
			pwAttempt := 0
			pwFn := func() (string, error) {
				pwAttempt++
				if pwAttempt > 1 {
					fmt.Fprint(os.Stderr, "invalid password, try again: ")
				} else {
					fmt.Fprint(os.Stderr, "2fa password: ")
				}
				s, _ := reader.ReadString('\n')
				return strings.TrimSpace(s), nil
			}
			data, err := app.AuthLogin(context.Background(), codeFn, pwFn, telegram.LoginOptions{ForceNewCode: resend})
			if err != nil {
				if ae, ok := apperr.As(err); ok && ae.Code == apperr.ErrTelegramRateLimited && !opts.json {
					fmt.Fprintln(os.Stderr, "Telegram rate-limits accounts after repeated login code requests.")
					fmt.Fprintln(os.Stderr, "Wait for the shown duration before retrying. Re-running `td auth login`")
					fmt.Fprintln(os.Stderr, "during the wait does not help and may extend the block.")
				}
				return r.Error(err)
			}
			if opts.json {
				return r.Success(data)
			}
			if data["already_authenticated"] == true {
				fmt.Fprintf(cmd.OutOrStdout(), "already logged in as %v (use `td auth logout` to switch accounts)\n", data["display_name"])
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "logged in as %v; session saved to %s\n", data["display_name"], cfg.Storage.SessionPath)
				fmt.Fprintln(cmd.OutOrStdout(), "other td commands now reuse this session; next: td init <local-root> --create-channel")
			}
			return nil
		},
	}
	login.Flags().BoolVar(&resend, "resend", false, "request a fresh login code instead of reusing a pending one")
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
			if opts.json {
				return r.Success(data)
			}
			if data["authenticated"] == true {
				fmt.Fprintf(cmd.OutOrStdout(), "logged in as %v (%v)\n", data["display_name"], data["phone"])
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "not logged in; run: td auth setup, then td auth login")
			}
			return nil
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
			if opts.json {
				return r.Success(map[string]string{"status": "logged_out"})
			}
			fmt.Fprintln(cmd.OutOrStdout(), "logged out")
			return nil
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
			if createCh != "" && bindCh != "" {
				return r.Error(apperr.New(apperr.ErrFlagConflict, "--create-channel and --bind-channel are mutually exclusive"))
			}
			app, cleanup, err := opts.openApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			if createCh == createChannelDefault {
				// Bare --create-channel: derive the title from --channel or
				// the local root's directory name ("." resolves to the real name).
				createCh = opts.channel
				if createCh == "" {
					if abs, err := filepath.Abs(args[0]); err == nil {
						createCh = filepath.Base(abs)
					} else {
						createCh = filepath.Base(strings.TrimRight(args[0], "/"))
					}
				}
			}
			data, err := app.InitRoot(context.Background(), args[0], opts.channel, createCh, bindCh)
			if err != nil {
				return r.Error(err)
			}
			if opts.json {
				return r.Success(data)
			}
			out := cmd.OutOrStdout()
			if data["already_initialized"] == true {
				fmt.Fprintf(out, "already initialized: %v is bound to channel %q (id %v)\n", data["local_root"], data["channel_title"], data["channel_id"])
				fmt.Fprintln(out, "use --bind-channel to rebind, or init a different directory")
			} else {
				fmt.Fprintf(out, "initialized %v -> channel %q (id %v)\n", data["local_root"], data["channel_title"], data["channel_id"])
				fmt.Fprintln(out, "next: td cp <local-file> /<remote-path>")
			}
			return nil
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
			if opts.json {
				return r.Success(data)
			}
			printKV(cmd.OutOrStdout(), data)
			return nil
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
			if opts.json {
				return r.Success(data)
			}
			if warn, ok := data["full_scan_warning"].(string); ok && warn != "" {
				fmt.Fprintln(os.Stderr, "warning: "+warn)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "scan complete (%v): %v active, %v deleted, %v invalid, %v missing\n",
				data["mode"], data["active"], data["deleted"], data["invalid"], data["missing"])
			return nil
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
			width := 0
			for _, e := range entries {
				n := displayWidth(e.Name)
				if e.Type == "dir" {
					n++
				}
				if n > width {
					width = n
				}
			}
			for _, e := range entries {
				if e.Type == "dir" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-4s %s/\n", "DIR", e.Name)
				} else {
					pad := width - displayWidth(e.Name)
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-4s %s%s  %8s\n", "FILE", e.Name, strings.Repeat(" ", pad), humanSize(e.Size))
				}
			}
			return nil
		},
	}
}

// displayWidth approximates the terminal cell width of s: East Asian wide
// glyphs occupy two cells, everything else one.
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if isWideRune(r) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

func isWideRune(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E, // CJK radicals, symbols, punctuation
		r >= 0x3041 && r <= 0x33FF, // Hiragana..CJK compatibility
		r >= 0x3400 && r <= 0x4DBF, // CJK ext A
		r >= 0x4E00 && r <= 0x9FFF, // CJK unified
		r >= 0xA000 && r <= 0xA4CF, // Yi
		r >= 0xAC00 && r <= 0xD7A3, // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK compatibility ideographs
		r >= 0xFE30 && r <= 0xFE4F, // CJK compatibility forms
		r >= 0xFF00 && r <= 0xFF60, // fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F, // emoji
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x20000 && r <= 0x2FFFD, // CJK ext B+
		r >= 0x30000 && r <= 0x3FFFD:
		return true
	}
	return false
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
				if !opts.json {
					return r.SuccessLine("uploaded %v files (%v skipped, %v failed)", data["uploaded"], data["skipped"], data["failed"])
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
				if data["skipped"] == true {
					return r.SuccessLine("skipped %s (already exists; use --replace to overwrite)", data["path"])
				}
				if size, ok := data["size"].(int64); ok {
					return r.SuccessLine("uploaded %s (%s)", data["path"], humanSize(size))
				}
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
				if !opts.json {
					return r.SuccessLine("downloaded %s -> %s", args[0], args[1])
				}
				return r.Success(map[string]string{"path": args[0], "local": args[1]})
			}
			res, err := app.DownloadFile(context.Background(), args[0], args[1], policy)
			if err != nil {
				return r.Error(err)
			}
			if !opts.json {
				if res.Skipped {
					return r.SuccessLine("skipped %s (already exists; use --replace to overwrite)", res.Dest)
				}
				return r.SuccessLine("downloaded %s -> %s (%s)", res.Path, res.Dest, humanSize(res.Size))
			}
			return r.Success(res)
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
				_, _ = fmt.Fprintln(out, "\nOpen the channel, then search or tap the filter tag. In clients that show global hashtag results, choose the current channel/chat tab.")
			} else {
				_, _ = fmt.Fprintln(out, "\nOpen the channel from the invite link; it contains everything shared here.")
			}
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
			if opts.json {
				return r.Success(data)
			}
			printKV(cmd.OutOrStdout(), data)
			return nil
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
