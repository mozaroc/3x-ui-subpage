// Package config assembles the service's runtime Config from the settings
// table in SQLite (see internal/store), overlaying it on top of built-in
// defaults so an operator only needs to set what matters. The only thing
// still read from a file is the tiny bootstrap YAML that says where the
// SQLite database itself lives — everything else is a chicken-and-egg
// problem otherwise.
package config

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// ErrSettingNotFound is returned by GetSetting for a missing key.
var ErrSettingNotFound = errors.New("config: setting not found")

type Config struct {
	Server       ServerConfig
	XUI          XUIConfig
	Subscription SubscriptionConfig
	Theme        ThemeConfig
	QR           QRConfig
	Support      SupportConfig
	Security     SecurityConfig
	Logging      LoggingConfig
}

type ServerConfig struct {
	Listen       string        `json:"listen"`
	ReadTimeout  time.Duration `json:"read_timeout"`
	WriteTimeout time.Duration `json:"write_timeout"`
	IdleTimeout  time.Duration `json:"idle_timeout"`
}

type RetryConfig struct {
	MaxAttempts int           `json:"max_attempts"`
	Backoff     time.Duration `json:"backoff"`
}

type XUIConfig struct {
	BaseURL            string        `json:"base_url"`
	Username           string        `json:"username"`
	Password           string        `json:"password"`
	BasePath           string        `json:"base_path"`
	Timeout            time.Duration `json:"timeout"`
	Retry              RetryConfig   `json:"retry"`
	InsecureSkipVerify bool          `json:"insecure_skip_verify"`
}

type SubscriptionConfig struct {
	PublicURL string `json:"public_url"`
	// ServerHost is the address clients should connect to (VPS IP or
	// domain) when an inbound's own "listen" field is empty or a wildcard
	// (0.0.0.0, ::) and therefore unusable as a connect address.
	ServerHost     string        `json:"server_host"`
	UpdateInterval time.Duration `json:"update_interval"`
	CacheTTL       time.Duration `json:"cache_ttl"`
}

// ThemeConfig only names which theme is active; the theme's own content
// (layout/partials/pages/static/colors) lives in the themes/theme_files
// tables, keyed by that slug.
type ThemeConfig struct {
	Active string `json:"active"`
}

type QRConfig struct {
	Size       int    `json:"size"`
	Margin     int    `json:"margin"`
	Foreground string `json:"foreground"`
	Background string `json:"background"`
}

type SupportConfig struct {
	Telegram string `json:"telegram"`
	Discord  string `json:"discord"`
	Email    string `json:"email"`
	Website  string `json:"website"`
	Custom   string `json:"custom"`
}

type RateLimitConfig struct {
	RequestsPerMinute int `json:"requests_per_minute"`
	Burst             int `json:"burst"`
}

type SecurityConfig struct {
	RateLimit    RateLimitConfig `json:"rate_limit"`
	CSP          string          `json:"csp"`
	TrustedHosts []string        `json:"trusted_hosts"`
}

type LoggingConfig struct {
	Level  string `json:"level"`
	Format string `json:"format"`
}

// Default returns a Config populated with sane defaults; LoadFromDB overlays
// whatever settings rows exist on top of it, so an operator only needs to
// store the sections that matter (at minimum: xui, subscription).
func Default() Config {
	return Config{
		Server: ServerConfig{
			Listen:       "0.0.0.0:8080",
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
		XUI: XUIConfig{
			BasePath: "/",
			Timeout:  10 * time.Second,
			Retry:    RetryConfig{MaxAttempts: 3, Backoff: 500 * time.Millisecond},
		},
		Subscription: SubscriptionConfig{
			UpdateInterval: 12 * time.Hour,
			CacheTTL:       30 * time.Second,
		},
		Theme: ThemeConfig{
			Active: "default",
		},
		QR: QRConfig{
			Size:       256,
			Margin:     4,
			Foreground: "#000000",
			Background: "#FFFFFF",
		},
		Security: SecurityConfig{
			RateLimit: RateLimitConfig{RequestsPerMinute: 60, Burst: 20},
			CSP:       "default-src 'self'",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
}

// bootstrapFile is the tiny on-disk YAML that only says where the SQLite
// database lives — read before the database exists to open, so it can't
// live in the database itself.
type bootstrapFile struct {
	Database struct {
		Path string `yaml:"path"`
	} `yaml:"database"`
}

// LoadBootstrap reads the bootstrap YAML at path and returns the configured
// SQLite database path.
func LoadBootstrap(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("config: read bootstrap %s: %w", path, err)
	}

	var b bootstrapFile
	if err := yaml.Unmarshal(data, &b); err != nil {
		return "", fmt.Errorf("config: parse bootstrap %s: %w", path, err)
	}
	if b.Database.Path == "" {
		return "", fmt.Errorf("config: bootstrap %s: database.path is required", path)
	}
	return b.Database.Path, nil
}

// LoadFromDB assembles a Config from the settings table, overlaying each
// section's JSON value onto Default(), then validates the result. Rows for
// sections the operator hasn't configured are simply absent — their default
// stays in effect.
func LoadFromDB(db *sql.DB) (Config, error) {
	cfg := Default()
	sections := sectionTargets(&cfg)

	rows, err := db.Query("SELECT key, value FROM settings")
	if err != nil {
		return Config{}, fmt.Errorf("config: query settings: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return Config{}, fmt.Errorf("config: scan settings row: %w", err)
		}

		target, ok := sections[key]
		if !ok {
			continue // unknown/forward-compat key: ignore
		}
		if err := json.Unmarshal([]byte(value), target); err != nil {
			return Config{}, fmt.Errorf("config: decode settings[%s]: %w", key, err)
		}
	}
	if err := rows.Err(); err != nil {
		return Config{}, fmt.Errorf("config: iterate settings: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("config: invalid: %w", err)
	}

	return cfg, nil
}

// sectionTargets maps each known settings key to the field it decodes onto.
func sectionTargets(cfg *Config) map[string]any {
	return map[string]any{
		"server":       &cfg.Server,
		"xui":          &cfg.XUI,
		"subscription": &cfg.Subscription,
		"theme":        &cfg.Theme,
		"qr":           &cfg.QR,
		"support":      &cfg.Support,
		"security":     &cfg.Security,
		"logging":      &cfg.Logging,
	}
}

// KnownSettingsKeys returns every settings key LoadFromDB understands, in a
// stable order — for the admin UI to offer as an editable list.
func KnownSettingsKeys() []string {
	return []string{"server", "xui", "subscription", "theme", "qr", "support", "security", "logging"}
}

// SaveSetting validates value (must be well-formed JSON, and — for a known
// key — must decode onto that section's struct) and upserts it into the
// settings table.
func SaveSetting(db *sql.DB, key string, value []byte) error {
	if !json.Valid(value) {
		return fmt.Errorf("config: value for settings[%s] is not valid JSON", key)
	}

	cfg := Default()
	if target, ok := sectionTargets(&cfg)[key]; ok {
		if err := json.Unmarshal(value, target); err != nil {
			return fmt.Errorf("config: value for settings[%s] doesn't match its schema: %w", key, err)
		}
	}

	_, err := db.Exec(`
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, string(value), time.Now().UnixNano())
	if err != nil {
		return fmt.Errorf("config: save settings[%s]: %w", key, err)
	}
	return nil
}

// GetSetting returns one key's raw JSON value, or ErrSettingNotFound.
func GetSetting(db *sql.DB, key string) (string, error) {
	var value string
	err := db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSettingNotFound
	}
	if err != nil {
		return "", fmt.Errorf("config: query settings[%s]: %w", key, err)
	}
	return value, nil
}

// ListSettings returns every stored settings key -> raw JSON value.
func ListSettings(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("config: query settings: %w", err)
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("config: scan settings row: %w", err)
		}
		out[key] = value
	}
	return out, rows.Err()
}

// Validate checks that required fields are present and internally
// consistent.
func (c Config) Validate() error {
	if c.XUI.BaseURL == "" {
		return fmt.Errorf("xui.base_url is required")
	}
	if c.XUI.Username == "" || c.XUI.Password == "" {
		return fmt.Errorf("xui.username and xui.password are required")
	}
	if c.Subscription.PublicURL == "" {
		return fmt.Errorf("subscription.public_url is required")
	}
	if c.Subscription.ServerHost == "" {
		return fmt.Errorf("subscription.server_host is required")
	}
	if c.XUI.Retry.MaxAttempts < 1 {
		return fmt.Errorf("xui.retry.max_attempts must be >= 1")
	}
	switch c.Logging.Format {
	case "json", "console":
	default:
		return fmt.Errorf("logging.format must be 'json' or 'console', got %q", c.Logging.Format)
	}
	return nil
}
