package account

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config controls the optional QSDM Account identity service. Wallet custody
// is deliberately absent: the service stores identities, sessions, and linked
// public wallet addresses, but never a keystore, private key, or passphrase.
type Config struct {
	ListenAddress string
	PublicBaseURL string
	StorePath     string
	DataKey       []byte

	SessionTTL   time.Duration
	MagicLinkTTL time.Duration
	OIDCFlowTTL  time.Duration

	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	SMTPUseTLS   bool

	TelegramClientID     string
	TelegramClientSecret string
}

func env(name string) string { return strings.TrimSpace(os.Getenv(name)) }

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	raw := env(name)
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return d, nil
}

func envBool(name string, fallback bool) bool {
	raw := strings.ToLower(env(name))
	if raw == "" {
		return fallback
	}
	return raw == "1" || raw == "true" || raw == "yes"
}

func decodeDataKey(raw string) ([]byte, error) {
	if raw == "" {
		return nil, errors.New("QSDM_ACCOUNT_DATA_KEY is required")
	}
	for _, decode := range []func(string) ([]byte, error){
		base64.RawURLEncoding.DecodeString,
		base64.StdEncoding.DecodeString,
		hex.DecodeString,
	} {
		if key, err := decode(raw); err == nil && len(key) == 32 {
			return key, nil
		}
	}
	return nil, errors.New("QSDM_ACCOUNT_DATA_KEY must encode exactly 32 random bytes")
}

// LoadConfigFromEnv reads the account service's intentionally small,
// environment-only configuration surface. Secrets are never accepted from a
// checked-in TOML/YAML file.
func LoadConfigFromEnv() (Config, error) {
	key, err := decodeDataKey(env("QSDM_ACCOUNT_DATA_KEY"))
	if err != nil {
		return Config{}, err
	}

	sessionTTL, err := envDuration("QSDM_ACCOUNT_SESSION_TTL", 7*24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	magicTTL, err := envDuration("QSDM_ACCOUNT_MAGIC_LINK_TTL", 15*time.Minute)
	if err != nil {
		return Config{}, err
	}
	flowTTL, err := envDuration("QSDM_ACCOUNT_OIDC_FLOW_TTL", 10*time.Minute)
	if err != nil {
		return Config{}, err
	}

	baseURL := strings.TrimRight(env("QSDM_ACCOUNT_PUBLIC_BASE_URL"), "/")
	if baseURL == "" {
		baseURL = "https://qsdm.tech"
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return Config{}, errors.New("QSDM_ACCOUNT_PUBLIC_BASE_URL must be an HTTPS origin")
	}

	port := 587
	if raw := env("QSDM_ACCOUNT_SMTP_PORT"); raw != "" {
		port, err = strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			return Config{}, errors.New("QSDM_ACCOUNT_SMTP_PORT must be a valid TCP port")
		}
	}

	cfg := Config{
		ListenAddress:        env("QSDM_ACCOUNT_LISTEN"),
		PublicBaseURL:        baseURL,
		StorePath:            env("QSDM_ACCOUNT_STORE_PATH"),
		DataKey:              key,
		SessionTTL:           sessionTTL,
		MagicLinkTTL:         magicTTL,
		OIDCFlowTTL:          flowTTL,
		SMTPHost:             env("QSDM_ACCOUNT_SMTP_HOST"),
		SMTPPort:             port,
		SMTPUsername:         env("QSDM_ACCOUNT_SMTP_USERNAME"),
		SMTPPassword:         env("QSDM_ACCOUNT_SMTP_PASSWORD"),
		SMTPFrom:             env("QSDM_ACCOUNT_SMTP_FROM"),
		SMTPUseTLS:           envBool("QSDM_ACCOUNT_SMTP_TLS", true),
		TelegramClientID:     env("QSDM_ACCOUNT_TELEGRAM_CLIENT_ID"),
		TelegramClientSecret: env("QSDM_ACCOUNT_TELEGRAM_CLIENT_SECRET"),
	}
	if cfg.ListenAddress == "" {
		cfg.ListenAddress = "127.0.0.1:8092"
	}
	listenHost, listenPort, err := net.SplitHostPort(cfg.ListenAddress)
	if err != nil {
		return Config{}, errors.New("QSDM_ACCOUNT_LISTEN must be a loopback IP and TCP port")
	}
	listenIP := net.ParseIP(listenHost)
	portNumber, portErr := strconv.Atoi(listenPort)
	if listenIP == nil || !listenIP.IsLoopback() || portErr != nil || portNumber < 1 || portNumber > 65535 {
		return Config{}, errors.New("QSDM_ACCOUNT_LISTEN must be a loopback IP and TCP port")
	}
	if cfg.StorePath == "" {
		cfg.StorePath = "qsdm_accounts.json"
	}
	if cfg.SessionTTL < time.Hour || cfg.SessionTTL > 30*24*time.Hour {
		return Config{}, errors.New("QSDM_ACCOUNT_SESSION_TTL must be between 1h and 720h")
	}
	if cfg.MagicLinkTTL < time.Minute || cfg.MagicLinkTTL > time.Hour {
		return Config{}, errors.New("QSDM_ACCOUNT_MAGIC_LINK_TTL must be between 1m and 1h")
	}
	if cfg.OIDCFlowTTL < time.Minute || cfg.OIDCFlowTTL > time.Hour {
		return Config{}, errors.New("QSDM_ACCOUNT_OIDC_FLOW_TTL must be between 1m and 1h")
	}
	smtpConfigured := cfg.SMTPHost != "" || cfg.SMTPFrom != "" || cfg.SMTPUsername != "" || cfg.SMTPPassword != ""
	if smtpConfigured {
		if cfg.SMTPHost == "" || cfg.SMTPFrom == "" {
			return Config{}, errors.New("SMTP host and from mailbox must be configured together")
		}
		from, parseErr := mail.ParseAddress(cfg.SMTPFrom)
		if parseErr != nil || from.Address == "" {
			return Config{}, errors.New("QSDM_ACCOUNT_SMTP_FROM must contain a valid mailbox")
		}
		if !cfg.SMTPUseTLS {
			return Config{}, errors.New("QSDM_ACCOUNT_SMTP_TLS must remain enabled for email sign-in")
		}
	}
	if smtpConfigured && (cfg.SMTPUsername == "") != (cfg.SMTPPassword == "") {
		return Config{}, errors.New("SMTP username and password must be configured together")
	}
	if (cfg.TelegramClientID == "") != (cfg.TelegramClientSecret == "") {
		return Config{}, errors.New("Telegram client ID and secret must be configured together")
	}
	if !cfg.EmailEnabled() && !cfg.TelegramEnabled() {
		return Config{}, errors.New("configure SMTP or Telegram OIDC before starting qsdm-account")
	}
	return cfg, nil
}

func (c Config) EmailEnabled() bool {
	return c.SMTPHost != "" && c.SMTPFrom != ""
}

func (c Config) TelegramEnabled() bool {
	return c.TelegramClientID != "" && c.TelegramClientSecret != ""
}
