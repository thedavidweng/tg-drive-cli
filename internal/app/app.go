package app

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/thedavidweng/tg-drive-cli/adapters/native/localfs"
	"github.com/thedavidweng/tg-drive-cli/adapters/native/sqlitestore"
	"github.com/thedavidweng/tg-drive-cli/adapters/native/telegramgotd"
	"github.com/thedavidweng/tg-drive-cli/core/drive"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
	"github.com/thedavidweng/tg-drive-cli/core/telegram/fake"
	"github.com/thedavidweng/tg-drive-cli/internal/app/commands"
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
		opts.start = time.Now()
		opts.requestID = uuid.NewString()
		opts.command = c.CommandPath()
		if !c.Flags().Changed("json") && envBool("TD_JSON") {
			opts.json = true
		}
		if opts.wait && opts.noWait {
			// Render here: errors returned from PersistentPreRunE bypass the
			// command handlers and would otherwise exit silently.
			return opts.Renderer().Error(apperr.New(apperr.ErrFlagConflict, "--wait and --no-wait are mutually exclusive"))
		}
		if opts.channel == "" {
			opts.channel = os.Getenv("TD_CHANNEL")
		}
		return nil
	}

	cmd.AddGroup(&cobra.Group{ID: "core", Title: "Core"})
	cmd.AddGroup(&cobra.Group{ID: "auth", Title: "Authentication"})
	cmd.AddGroup(&cobra.Group{ID: "channels", Title: "Channels"})
	cmd.AddGroup(&cobra.Group{ID: "files", Title: "Files"})
	cmd.AddGroup(&cobra.Group{ID: "maintenance", Title: "Maintenance"})

	add := func(c *cobra.Command, group string) *cobra.Command {
		c.GroupID = group
		cmd.AddCommand(c)
		return c
	}

	add(commands.NewVersionCmd(opts), "core")
	add(commands.NewDoctorCmd(opts), "core")
	add(commands.NewConfigCmd(opts), "core")
	add(commands.NewAuthCmd(opts), "auth")
	add(commands.NewChannelsCmd(opts), "channels")
	add(commands.NewInitCmd(opts), "files")
	add(commands.NewStatusCmd(opts), "core")
	add(commands.NewScanCmd(opts), "maintenance")
	add(commands.NewLsCmd(opts), "files")
	add(commands.NewTreeCmd(opts), "files")
	add(commands.NewCpCmd(opts), "files")
	add(commands.NewGetCmd(opts), "files")
	add(commands.NewMvCmd(opts), "files")
	add(commands.NewRmCmd(opts), "files")
	add(commands.NewShareCmd(opts), "maintenance")
	add(commands.NewImportCmd(opts), "files")
	add(commands.NewRepairCmd(opts), "maintenance")
	add(commands.NewCompletionCmd(opts), "core")

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
	command     string
	requestID   string
	start       time.Time
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

func (o *runtimeOpts) Renderer() *output.Renderer {
	r := output.New(o.json)
	r.Quiet = o.quiet
	r.Command = o.command
	r.RequestID = o.requestID
	r.Start = o.start
	return r
}

func (o *runtimeOpts) JSON() bool { return o.json }

func (o *runtimeOpts) Channel() string { return o.channel }
func (o *runtimeOpts) overrides() config.Overrides {
	return config.Overrides{
		ConfigPath:  o.configPath,
		DBPath:      o.dbPath,
		SessionPath: o.sessionPath,
		Channel:     o.channel,
	}
}

func (o *runtimeOpts) LoadConfig() (config.Config, string, error) {
	return config.Load(o.overrides())
}

func (o *runtimeOpts) OpenApp(cmd *cobra.Command) (*service.App, func(), error) {
	if isLightweight(cmd) {
		return nil, func() {}, apperr.New(apperr.ErrUsage, "command does not use app context")
	}
	cfg, _, err := o.LoadConfig()
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
	rows, err := database.Raw().Query(`select tg_channel_id, access_hash, title from channels where access_hash is not null and access_hash != ''`)
	if err == nil {
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var tgID, hash, title string
			if err := rows.Scan(&tgID, &hash, &title); err != nil {
				continue
			}
			chID, err1 := strconv.ParseInt(tgID, 10, 64)
			accHash, err2 := strconv.ParseInt(hash, 10, 64)
			if err1 == nil && err2 == nil {
				client.RegisterChannelInfo(chID, accHash, title)
			}
		}
	}
	return client, nil
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

// Compile-time check: the in-memory fake implements telegram.Client.
var _ telegram.Client = fake.New()
