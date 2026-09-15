package commands

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"

	"github.com/spf13/cobra"
	"github.com/thedavidweng/tg-drive-cli/adapters/native/sqlitestore"
	"github.com/thedavidweng/tg-drive-cli/internal/config"
	"github.com/thedavidweng/tg-drive-cli/internal/service"
)

// Doctor, doctor path-codec, and config commands.

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
