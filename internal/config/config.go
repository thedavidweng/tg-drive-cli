package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/thedavidweng/tg-drive-cli/internal/apperr"
)

// Config matches docs/contracts/config-contract.md.
type Config struct {
	Telegram TelegramConfig `toml:"telegram"`
	Storage  StorageConfig  `toml:"storage"`
	Caption  CaptionConfig  `toml:"caption"`
	Hash     HashConfig     `toml:"hash"`
	Delete   DeleteConfig   `toml:"delete"`
	Limits   LimitsConfig   `toml:"limits"`
	Locks    LocksConfig    `toml:"locks"`
	Roots    []RootConfig   `toml:"roots"`
}

type TelegramConfig struct {
	APIID   int64  `toml:"api_id"`
	APIHash string `toml:"api_hash"`
	Phone   string `toml:"phone"`
}

type StorageConfig struct {
	DBPath      string `toml:"db_path"`
	SessionPath string `toml:"session_path"`
}

type CaptionConfig struct {
	SafeMediaCaptionUTF16Units int `toml:"safe_media_caption_utf16_units"`
	SafeTextMessageUTF16Units  int `toml:"safe_text_message_utf16_units"`
	MarginUTF16Units           int `toml:"margin_utf16_units"`
	MaxHashtagsInCaption       int `toml:"max_hashtags_in_caption"`
}

type HashConfig struct {
	Enabled   bool   `toml:"enabled"`
	Algorithm string `toml:"algorithm"`
}

type DeleteConfig struct {
	Mode string `toml:"mode"`
}

type LimitsConfig struct {
	FreeUploadBytes    int64 `toml:"free_upload_bytes"`
	PremiumUploadBytes int64 `toml:"premium_upload_bytes"`
}

type LocksConfig struct {
	TTLSeconds int `toml:"ttl_seconds"`
}

type RootConfig struct {
	LocalPath    string `toml:"local_path"`
	RemotePath   string `toml:"remote_path"`
	ChannelTitle string `toml:"channel_title"`
	Strategy     string `toml:"strategy"`
}

// Overrides from CLI flags and environment.
type Overrides struct {
	ConfigPath  string
	DBPath      string
	SessionPath string
	APIID       *int64
	APIHash     string
	Phone       string
	Channel     string
	JSON        *bool
	Wait        *bool
}

// Defaults returns the default configuration.
func Defaults() Config {
	home, _ := os.UserHomeDir()
	cfgDir := defaultConfigDir(home)
	dataDir := defaultDataDir(home)
	return Config{
		Telegram: TelegramConfig{},
		Storage: StorageConfig{
			DBPath:      filepath.Join(dataDir, "local_cache.db"),
			SessionPath: filepath.Join(cfgDir, "session.json"),
		},
		Caption: CaptionConfig{
			SafeMediaCaptionUTF16Units: 1024,
			SafeTextMessageUTF16Units:  4096,
			MarginUTF16Units:           16,
			MaxHashtagsInCaption:       32,
		},
		Hash:   HashConfig{Enabled: true, Algorithm: "blake3"},
		Delete: DeleteConfig{Mode: "delete"},
		Limits: LimitsConfig{
			FreeUploadBytes:    2147483648,
			PremiumUploadBytes: 4294967296,
		},
		Locks: LocksConfig{TTLSeconds: 900},
	}
}

func defaultConfigDir(home string) string {
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "tg-drive-cli")
		}
	}
	return filepath.Join(home, ".config", "tg-drive-cli")
}

func defaultDataDir(home string) string {
	if runtime.GOOS == "windows" {
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, "tg-drive-cli")
		}
	}
	return filepath.Join(home, ".local", "share", "tg-drive-cli")
}

// DefaultConfigPath returns the default config file path.
func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(defaultConfigDir(home), "config.toml")
}

// DefaultSessionPath returns the default session path.
func DefaultSessionPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(defaultConfigDir(home), "session.json")
}

// DefaultDBPath returns the default database path.
func DefaultDBPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(defaultDataDir(home), "local_cache.db")
}

// ResolvePaths determines effective paths with precedence: CLI > env > config > defaults.
func ResolvePaths(ov Overrides) (configPath, sessionPath, dbPath string) {
	cfg := Defaults()
	configPath = firstNonEmpty(ov.ConfigPath, os.Getenv("TD_CONFIG"), DefaultConfigPath())
	if data, err := os.ReadFile(configPath); err == nil {
		_ = toml.Unmarshal(data, &cfg)
	}
	sessionPath = firstNonEmpty(ov.SessionPath, os.Getenv("TD_SESSION"), expandHome(cfg.Storage.SessionPath), DefaultSessionPath())
	dbPath = firstNonEmpty(ov.DBPath, os.Getenv("TD_DB"), expandHome(cfg.Storage.DBPath), DefaultDBPath())
	return configPath, sessionPath, dbPath
}

// Load reads configuration applying overrides.
func Load(ov Overrides) (Config, string, error) {
	cfg := Defaults()
	configPath, sessionPath, dbPath := ResolvePaths(ov)
	if data, err := os.ReadFile(configPath); err != nil {
		if !os.IsNotExist(err) {
			return cfg, configPath, apperr.Wrap(apperr.ErrConfigInvalid, "read config", err)
		}
	} else if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, configPath, apperr.Wrap(apperr.ErrConfigInvalid, "parse config", err)
	}
	cfg.Storage.SessionPath = sessionPath
	cfg.Storage.DBPath = dbPath

	if v := os.Getenv("TD_API_ID"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return cfg, configPath, apperr.Wrap(apperr.ErrConfigInvalid, "TD_API_ID", err)
		}
		cfg.Telegram.APIID = id
	}
	if v := os.Getenv("TD_API_HASH"); v != "" {
		cfg.Telegram.APIHash = v
	}
	if v := os.Getenv("TD_PHONE"); v != "" {
		cfg.Telegram.Phone = v
	}
	if ov.APIID != nil {
		cfg.Telegram.APIID = *ov.APIID
	}
	if ov.APIHash != "" {
		cfg.Telegram.APIHash = ov.APIHash
	}
	if ov.Phone != "" {
		cfg.Telegram.Phone = ov.Phone
	}
	return cfg, configPath, nil
}

// Save writes config to path, creating parent dir with secure permissions.
func Save(path string, cfg Config) error {
	if err := ensureDir(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return apperr.Wrap(apperr.ErrConfigInvalid, "marshal config", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return apperr.Wrap(apperr.ErrConfigInvalid, "write config", err)
	}
	return nil
}

// GetValue returns a config value by dotted key.
func GetValue(cfg Config, key string) (any, error) {
	switch key {
	case "telegram.api_id":
		return cfg.Telegram.APIID, nil
	case "telegram.api_hash":
		return cfg.Telegram.APIHash, nil
	case "telegram.phone":
		return cfg.Telegram.Phone, nil
	case "storage.db_path":
		return cfg.Storage.DBPath, nil
	case "storage.session_path":
		return cfg.Storage.SessionPath, nil
	case "delete.mode":
		return cfg.Delete.Mode, nil
	case "hash.enabled":
		return cfg.Hash.Enabled, nil
	case "hash.algorithm":
		return cfg.Hash.Algorithm, nil
	default:
		return nil, apperr.New(apperr.ErrUsage, fmt.Sprintf("unknown config key: %s", key))
	}
}

// SetValue sets a config value by dotted key.
func SetValue(cfg *Config, key, value string) error {
	switch key {
	case "telegram.api_id":
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return apperr.Wrap(apperr.ErrConfigInvalid, "api_id", err)
		}
		cfg.Telegram.APIID = id
	case "telegram.api_hash":
		cfg.Telegram.APIHash = value
	case "telegram.phone":
		cfg.Telegram.Phone = value
	case "storage.db_path":
		cfg.Storage.DBPath = value
	case "storage.session_path":
		cfg.Storage.SessionPath = value
	case "delete.mode":
		if value != "delete" && value != "tombstone" {
			return apperr.New(apperr.ErrConfigInvalid, "delete.mode must be delete or tombstone")
		}
		cfg.Delete.Mode = value
	case "hash.enabled":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return apperr.Wrap(apperr.ErrConfigInvalid, "hash.enabled", err)
		}
		cfg.Hash.Enabled = b
	default:
		return apperr.New(apperr.ErrUsage, fmt.Sprintf("unknown config key: %s", key))
	}
	return nil
}

// RedactValue returns a display-safe config value.
func RedactValue(key string, value any, showSecrets bool) any {
	if showSecrets {
		return value
	}
	switch key {
	case "telegram.api_hash":
		return "redacted"
	case "telegram.phone":
		if s, ok := value.(string); ok && len(s) > 4 {
			return s[:2] + strings.Repeat("*", len(s)-4) + s[len(s)-2:]
		}
		return "redacted"
	}
	return value
}

// RedactConfigMap returns all config values with secrets redacted.
func RedactConfigMap(cfg Config, showSecrets bool) map[string]any {
	keys := []string{
		"telegram.api_id", "telegram.api_hash", "telegram.phone",
		"storage.db_path", "storage.session_path", "delete.mode",
		"hash.enabled", "hash.algorithm",
	}
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		v, err := GetValue(cfg, k)
		if err != nil {
			continue
		}
		out[k] = RedactValue(k, v, showSecrets)
	}
	return out
}

// EnsureSessionDir creates parent dirs for session with secure permissions.
func EnsureSessionDir(sessionPath string) error {
	return ensureDir(filepath.Dir(sessionPath), 0o700)
}

// EnsureSessionFile creates an empty session file with 0600 if missing.
func EnsureSessionFile(sessionPath string) error {
	if err := EnsureSessionDir(sessionPath); err != nil {
		return err
	}
	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		return os.WriteFile(sessionPath, []byte("{}"), 0o600)
	}
	return nil
}

func ensureDir(path string, perm os.FileMode) error {
	if path == "" || path == "." {
		return nil
	}
	if err := os.MkdirAll(path, perm); err != nil {
		return apperr.Wrap(apperr.ErrConfigInvalid, "create directory", err)
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(path, perm)
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return expandHome(v)
		}
	}
	return ""
}

func expandHome(path string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}
