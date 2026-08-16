package commands

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
	"github.com/thedavidweng/tg-drive-cli/adapters/native/sqlitestore"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
	"github.com/thedavidweng/tg-drive-cli/internal/config"
	"github.com/thedavidweng/tg-drive-cli/internal/output"
	"github.com/thedavidweng/tg-drive-cli/internal/service"
	"github.com/thedavidweng/tg-drive-cli/internal/version"
)

// Runtime is the command runtime contract.
type Runtime interface {
	JSON() bool
	Channel() string
	Renderer() *output.Renderer
	LoadConfig() (config.Config, string, error)
	OpenApp(cmd *cobra.Command) (*service.App, func(), error)
}

func EnsureTelegramConfig(cfg *config.Config, configPath string, requirePhone bool) error {
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
func GroupUsage(rt Runtime, c *cobra.Command) {
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return rt.Renderer().Error(apperr.New(apperr.ErrUsage,
			fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())))
	}
}

// printKV writes a sorted key: value view of a data map for human output,
// flattening nested count maps into dotted keys.
func PrintKV(w io.Writer, data map[string]any) {
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

func NewCompletionCmd(rt Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(cmd.OutOrStdout())
			case "zsh":
				return cmd.Root().GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return cmd.Root().GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletion(cmd.OutOrStdout())
			default:
				return rt.Renderer().Error(apperr.New(apperr.ErrUsage, "valid shells: bash, zsh, fish, powershell"))
			}
		},
	}
}

func NewVersionCmd(rt Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			if rt.JSON() {
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

func NewDoctorCmd(rt Runtime) *cobra.Command {
	c := &cobra.Command{
		Use:   "doctor",
		Short: "Check local and Telegram capabilities",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			data, err := app.Doctor(context.Background())
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
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
				_, _ = fmt.Fprintln(out, line)
			}
			if maxBytes, ok := data["max_upload_bytes"].(int64); ok {
				_, _ = fmt.Fprintf(out, "%-18s %s\n", "max_upload", humanSize(maxBytes))
			}
			return nil
		},
	}
	c.AddCommand(NewDoctorPathCodecCmd(rt))
	return c
}

func NewDoctorPathCodecCmd(rt Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "path-codec",
		Short: "Run path codec self-test and verify stored slug mappings",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			cfg, _, err := rt.LoadConfig()
			if err != nil {
				return r.Error(err)
			}
			database, err := sqlitestore.Open(cfg.Storage.DBPath)
			if err != nil {
				return r.Error(err)
			}
			defer func() { _ = database.Close() }()
			app := &service.App{Cfg: cfg, DB: database}
			data, err := app.PathCodecDoctor(context.Background())
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(data)
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "fixed-vectors        %s\n", data["fixed_vectors"])
			_, _ = fmt.Fprintf(out, "db-check             %s\n", data["db_check"])
			_, _ = fmt.Fprintf(out, "db-rows              %v\n", data["db_rows"])
			_, _ = fmt.Fprintf(out, "corrupt-rows         %v\n", data["corrupt_rows"])
			return nil
		},
	}
}

func NewConfigCmd(rt Runtime) *cobra.Command {
	var showSecrets, confirm bool
	c := &cobra.Command{Use: "config", Short: "Manage configuration"}
	GroupUsage(rt, c)
	get := &cobra.Command{
		Use:   "get [key]",
		Short: "Get config value(s)",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			cfg, configPath, err := rt.LoadConfig()
			if err != nil {
				return r.Error(err)
			}
			if showSecrets && !rt.JSON() {
				fmt.Fprint(os.Stderr, "show secrets? [y/N] ")
				var ans string
				_, _ = fmt.Scanln(&ans)
				if strings.ToLower(ans) != "y" {
					showSecrets = false
				}
			}
			if showSecrets && rt.JSON() && !confirm {
				return r.Error(apperr.New(apperr.ErrUsage, "--confirm required with --json --show-secrets"))
			}
			if len(args) == 0 {
				all := config.RedactConfigMap(cfg, showSecrets)
				if rt.JSON() {
					return r.Success(all)
				}
				PrintKV(cmd.OutOrStdout(), all)
				return nil
			}
			v, err := config.GetValue(cfg, args[0])
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(map[string]any{args[0]: config.RedactValue(args[0], v, showSecrets), "config_path": configPath})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%v\n", config.RedactValue(args[0], v, showSecrets))
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
			r := rt.Renderer()
			cfg, configPath, err := rt.LoadConfig()
			if err != nil {
				return r.Error(err)
			}
			if err := config.SetValue(&cfg, args[0], args[1]); err != nil {
				return r.Error(err)
			}
			if err := config.Save(configPath, cfg); err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(map[string]string{"key": args[0], "status": "set"})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "set %s\n", args[0])
			return nil
		},
	}
	c.AddCommand(get, set)
	return c
}

func NewAuthCmd(rt Runtime) *cobra.Command {
	c := &cobra.Command{Use: "auth", Short: "Authentication commands"}
	GroupUsage(rt, c)
	setup := &cobra.Command{
		Use:   "setup",
		Short: "Configure Telegram API credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			cfg, configPath, err := rt.LoadConfig()
			if err != nil {
				return r.Error(err)
			}
			if err := EnsureTelegramConfig(&cfg, configPath, false); err != nil {
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
			if rt.JSON() {
				return r.Success(map[string]any{
					"config_path":  configPath,
					"api_id":       cfg.Telegram.APIID,
					"db_path":      cfg.Storage.DBPath,
					"session_path": cfg.Storage.SessionPath,
					"status":       "configured",
				})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "saved Telegram API credentials to %s\nnext: td auth login\n", configPath)
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
			r := rt.Renderer()
			cfg, configPath, err := rt.LoadConfig()
			if err != nil {
				return r.Error(err)
			}
			if err := EnsureTelegramConfig(&cfg, configPath, true); err != nil {
				return r.Error(err)
			}
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
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
				if ae, ok := apperr.As(err); ok && ae.Code == apperr.ErrTelegramRateLimited && !rt.JSON() {
					fmt.Fprintln(os.Stderr, "Telegram rate-limits accounts after repeated login code requests.")
					fmt.Fprintln(os.Stderr, "Wait for the shown duration before retrying. Re-running `td auth login`")
					fmt.Fprintln(os.Stderr, "during the wait does not help and may extend the block.")
				}
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(data)
			}
			if data["already_authenticated"] == true {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "already logged in as %v (use `td auth logout` to switch accounts)\n", data["display_name"])
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "logged in as %v; session saved to %s\n", data["display_name"], cfg.Storage.SessionPath)
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "other td commands now reuse this session; next: td init <local-root> --create-channel")
			}
			return nil
		},
	}
	login.Flags().BoolVar(&resend, "resend", false, "request a fresh login code instead of reusing a pending one")
	status := &cobra.Command{
		Use:   "status",
		Short: "Show auth status",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			data, err := app.AuthStatus(context.Background())
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(data)
			}
			if data["authenticated"] == true {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "logged in as %v (%v)\n", data["display_name"], data["phone"])
			} else {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "not logged in; run: td auth setup, then td auth login")
			}
			return nil
		},
	}
	logout := &cobra.Command{
		Use:   "logout",
		Short: "Logout",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			if err := app.AuthLogout(context.Background()); err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(map[string]string{"status": "logged_out"})
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "logged out")
			return nil
		},
	}
	c.AddCommand(setup, login, status, logout)
	return c
}

// createChannelDefault is the NoOptDefVal for a bare --create-channel:
// derive the channel title from --channel or the local root's name.
const createChannelDefault = "auto"
const bindChannelPick = "?"

func NewInitCmd(rt Runtime) *cobra.Command {
	var createCh, bindCh string
	c := &cobra.Command{
		Use:   "init <local-root>",
		Short: "Initialize a local root",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			if createCh != "" && bindCh != "" {
				return r.Error(apperr.New(apperr.ErrFlagConflict, "--create-channel and --bind-channel are mutually exclusive"))
			}
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			if createCh == createChannelDefault {
				// Bare --create-channel: derive the title from --channel or
				// the local root's directory name ("." resolves to the real name).
				createCh = rt.Channel()
				if createCh == "" {
					if abs, err := filepath.Abs(args[0]); err == nil {
						createCh = filepath.Base(abs)
					} else {
						createCh = filepath.Base(strings.TrimRight(args[0], "/"))
					}
				}
			}
			ctx := context.Background()
			bindChannel := bindCh
			if bindCh == bindChannelPick || (createCh == "" && bindCh == "" && rt.Channel() == "") {
				chs, err := app.ListChannels(ctx, false)
				if err != nil {
					return r.Error(err)
				}
				if rt.JSON() {
					return r.Error(apperr.New(apperr.ErrChannelNotFound, "select a channel").WithDetails(map[string]any{"channels": channelsToMap(chs)}))
				}
				if len(chs) == 0 {
					_, _ = fmt.Fprintln(os.Stderr, "no existing channels; use --create-channel or --bind-channel")
					return r.Error(apperr.New(apperr.ErrChannelNotFound, "no existing channels"))
				}
				selected, err := selectChannelInteractively(bufio.NewReader(os.Stdin), chs)
				if err != nil {
					return r.Error(err)
				}
				if selected == nil {
					return r.Error(apperr.New(apperr.ErrUsage, "channel selection required"))
				}
				bindChannel = selected.Title
			} else if rt.Channel() != "" {
				bindChannel = rt.Channel()
			}
			data, err := app.InitRoot(ctx, args[0], rt.Channel(), createCh, bindChannel)
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(data)
			}
			out := cmd.OutOrStdout()
			if data["already_initialized"] == true {
				_, _ = fmt.Fprintf(out, "already initialized: %v is bound to channel %q (id %v)\n", data["local_root"], data["channel_title"], data["channel_id"])
				_, _ = fmt.Fprintln(out, "use --bind-channel to rebind, or init a different directory")
			} else {
				_, _ = fmt.Fprintf(out, "initialized %v -> channel %q (id %v)\n", data["local_root"], data["channel_title"], data["channel_id"])
				_, _ = fmt.Fprintln(out, "next: td cp <local-file> /<remote-path>")
			}
			return nil
		},
	}
	c.Flags().StringVar(&createCh, "create-channel", "", "create a new channel; bare flag derives the title, or pass --create-channel=<title>")
	c.Flags().Lookup("create-channel").NoOptDefVal = createChannelDefault
	c.Flags().StringVar(&bindCh, "bind-channel", "", "bind an existing channel; bare flag lists and prompts")
	c.Flags().Lookup("bind-channel").NoOptDefVal = bindChannelPick
	return c
}

func selectChannelInteractively(reader *bufio.Reader, chs []telegram.Channel) (*telegram.Channel, error) {
	_, _ = fmt.Fprintln(os.Stderr, "Select a channel:")
	for i, ch := range chs {
		_, _ = fmt.Fprintf(os.Stderr, "  %d. %s (id %d)\n", i+1, ch.Title, ch.ID)
	}
	_, _ = fmt.Fprint(os.Stderr, "Enter number: ")
	s, _ := reader.ReadString('\n')
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > len(chs) {
		return nil, apperr.New(apperr.ErrUsage, "invalid channel selection")
	}
	return &chs[n-1], nil
}

func channelsToMap(chs []telegram.Channel) []map[string]any {
	out := make([]map[string]any, len(chs))
	for i, ch := range chs {
		out[i] = map[string]any{"id": ch.ID, "title": ch.Title, "username": ch.Username, "invite_link": ch.InviteLink}
	}
	return out
}

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

func NewScanCmd(rt Runtime) *cobra.Command {
	var full, strict, repair, includeDeleted bool
	c := &cobra.Command{
		Use:   "scan [remote-root]",
		Short: "Scan Telegram channel and rebuild index",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
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
			if rt.JSON() {
				return r.Success(data)
			}
			if warn, ok := data["full_scan_warning"].(string); ok && warn != "" {
				fmt.Fprintln(os.Stderr, "warning: "+warn)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "scan complete (%v): %v active, %v deleted, %v invalid, %v missing\n",
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

func NewLsCmd(rt Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "ls [remote-path]",
		Short: "List remote directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
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
			if rt.JSON() {
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

func NewTreeCmd(rt Runtime) *cobra.Command {
	var depth int
	c := &cobra.Command{
		Use:   "tree [remote-path]",
		Short: "Show remote tree",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
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
			if rt.JSON() {
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

func NewCpCmd(rt Runtime) *cobra.Command {
	var recursive, replace, skip, autoRename, noHash, continueOnError, includeEmptyDirs bool
	var uploadThreads, uploadPartSizeKB int
	var confirm, dryRun, events bool
	c := &cobra.Command{
		Use:   "cp <local> <remote-path>",
		Short: "Upload local file or directory",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			policy, err := conflictPolicy(replace, skip, autoRename)
			if err != nil {
				return r.Error(err)
			}
			if dryRun {
				plan := map[string]any{"local": args[0], "remote": args[1], "policy": string(policy)}
				if replace {
					plan["would_replace"] = args[1]
				}
				if events {
					return r.Event("cp.dry-run", plan)
				}
				return r.Success(plan)
			}
			if replace && !confirm {
				return r.Error(apperr.New(apperr.ErrConfirmationRequired, "replacing an existing remote file requires --confirm"))
			}
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			if cmd.Flags().Changed("upload-threads") && uploadThreads > 0 {
				app.Cfg.Upload.Threads = uploadThreads
			}
			if cmd.Flags().Changed("upload-part-size-kb") && uploadPartSizeKB > 0 {
				app.Cfg.Upload.PartSizeKB = uploadPartSizeKB
			}
			if events {
				app.Progress = func(ctx context.Context, state telegram.UploadProgressState) error {
					return r.Event("cp.progress", state)
				}
			}
			if recursive {
				data, err := app.UploadRecursive(context.Background(), args[0], args[1], policy, continueOnError, noHash, includeEmptyDirs)
				if err != nil {
					return r.Error(err)
				}
				if events {
					return r.Event("cp", data)
				}
				if !rt.JSON() {
					_ = r.SuccessLine("uploaded %v files (%v skipped, %v failed)", data["uploaded"], data["skipped"], data["failed"])
					if link, ok := data["invite_link"].(string); ok && link != "" {
						return r.SuccessLine("invite: %s", link)
					}
					return nil
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
			if events {
				return r.Event("cp", data)
			}
			if !rt.JSON() {
				if data["skipped"] == true {
					return r.SuccessLine("skipped %s (already exists; use --replace to overwrite)", data["path"])
				}
				verb := "uploaded"
				if data["resumed"] == true {
					verb = "resumed upload of"
				}
				if size, ok := data["size"].(int64); ok {
					_ = r.SuccessLine("%s %s (%s)", verb, data["path"], humanSize(size))
				} else {
					_ = r.SuccessLine("%s %s", verb, data["path"])
				}
				if link, ok := data["invite_link"].(string); ok && link != "" {
					return r.SuccessLine("invite: %s", link)
				}
				return nil
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
	c.Flags().IntVar(&uploadThreads, "upload-threads", 0, "parallel upload goroutines (0 uses config)")
	c.Flags().IntVar(&uploadPartSizeKB, "upload-part-size-kb", 0, "upload part size in KB (0 uses config)")
	c.Flags().BoolVar(&confirm, "confirm", false, "confirm destructive operations such as --replace")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "preview the upload plan without executing")
	c.Flags().BoolVar(&events, "events", false, "emit NDJSON progress events during upload")
	return c
}

func NewGetCmd(rt Runtime) *cobra.Command {
	var recursive, replace, skip, autoRename, continueOnError bool
	c := &cobra.Command{
		Use:   "get <remote-path> <local-dest>",
		Short: "Download remote file or directory",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			policy, err := conflictPolicy(replace, skip, autoRename)
			if err != nil {
				return r.Error(err)
			}
			if recursive {
				data, err := app.DownloadRecursive(context.Background(), args[0], args[1], policy, continueOnError)
				if err != nil {
					return r.Error(err)
				}
				if !rt.JSON() {
					_ = r.SuccessLine("downloaded %s -> %s (%v files, %v skipped, %v failed)", args[0], args[1], data["downloaded"], data["skipped"], data["failed"])
					return nil
				}
				return r.Success(data)
			}
			res, err := app.DownloadFile(context.Background(), args[0], args[1], policy)
			if err != nil {
				return r.Error(err)
			}
			if !rt.JSON() {
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

func NewMvCmd(rt Runtime) *cobra.Command {
	var confirm, dryRun bool
	c := &cobra.Command{
		Use:   "mv <remote-from> <remote-to>",
		Short: "Move or rename remote file",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			if dryRun {
				return r.Success(map[string]any{"would_move": args[0], "to": args[1]})
			}
			if !confirm {
				return r.Error(apperr.New(apperr.ErrConfirmationRequired, "moving a remote file requires --confirm"))
			}
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			if err := app.MoveFile(context.Background(), args[0], args[1]); err != nil {
				return r.Error(err)
			}
			if !rt.JSON() {
				return r.SuccessLine("moved %s -> %s", args[0], args[1])
			}
			return r.Success(map[string]string{"from": args[0], "to": args[1]})
		},
	}
	c.Flags().BoolVar(&confirm, "confirm", false, "confirm the move")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "preview what would be moved")
	return c
}

func NewRmCmd(rt Runtime) *cobra.Command {
	var tombstone, allowStaleManifest, confirm, dryRun bool
	c := &cobra.Command{
		Use:   "rm <remote-path>",
		Short: "Delete remote file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			if dryRun {
				return r.Success(map[string]any{"would_delete": args[0], "tombstone": tombstone})
			}
			if !confirm {
				return r.Error(apperr.New(apperr.ErrConfirmationRequired, "deleting a remote file requires --confirm"))
			}
			app, cleanup, err := rt.OpenApp(cmd)
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
			if !rt.JSON() {
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
	c.Flags().BoolVar(&confirm, "confirm", false, "confirm the deletion")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "preview what would be deleted")
	return c
}

func NewShareCmd(rt Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "share [remote-path]",
		Short: "Share invite link and hashtag",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
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
			if rt.JSON() {
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

func NewImportCmd(rt Runtime) *cobra.Command {
	var unmanaged, hash, confirm, dryRun, continueOnError, rewriteCaptions bool
	var into string
	c := &cobra.Command{
		Use:   "import [message-id] [remote-path]",
		Short: "Adopt existing Telegram messages into the virtual file tree",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			opts := service.ImportOptions{
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
				return r.Error(apperr.New(apperr.ErrUsage, "import requires a message-id, --unmanaged, or --rewrite-captions"))
			}
			if !dryRun && !confirm {
				return r.Error(apperr.New(apperr.ErrConfirmationRequired, "adopting existing messages requires --confirm (or --dry-run)"))
			}
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			data, err := app.Import(context.Background(), opts)
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(data)
			}
			out := cmd.OutOrStdout()
			if rewriteCaptions {
				_, _ = fmt.Fprintf(out, "import %s: %d captions restored, %d replies deleted, %d skipped, %d failed\n",
					map[bool]string{true: "dry-run", false: "done"}[data.DryRun],
					data.Imported, data.Deleted, data.Skipped, data.Failed)
			} else {
				_, _ = fmt.Fprintf(out, "import %s: %d adopted, %d skipped, %d failed\n",
					map[bool]string{true: "dry-run", false: "done"}[data.DryRun],
					data.Imported, data.Skipped, data.Failed)
			}
			for _, it := range data.Items {
				if it.Action == "import" {
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
	var pending, orphaned, scanErrors, deleteOrphans, confirm bool
	c := &cobra.Command{
		Use:   "repair [path]",
		Short: "Repair index inconsistencies",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			if deleteOrphans && !orphaned {
				return r.Error(apperr.New(apperr.ErrUsage, "--delete-orphaned requires --orphaned"))
			}
			if deleteOrphans && !confirm {
				return r.Error(apperr.New(apperr.ErrConfirmationRequired, "deleting orphaned Telegram messages requires --confirm"))
			}
			app, cleanup, err := rt.OpenApp(cmd)
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
	return c
}
