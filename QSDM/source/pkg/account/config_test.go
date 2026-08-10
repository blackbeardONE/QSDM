package account

import (
	"encoding/hex"
	"strings"
	"testing"
)

func setValidConfigEnv(t *testing.T) {
	t.Helper()
	values := map[string]string{
		"QSDM_ACCOUNT_DATA_KEY":               hex.EncodeToString(testDataKey()),
		"QSDM_ACCOUNT_LISTEN":                 "127.0.0.1:8092",
		"QSDM_ACCOUNT_PUBLIC_BASE_URL":        "https://qsdm.tech",
		"QSDM_ACCOUNT_SMTP_HOST":              "smtp.example.test",
		"QSDM_ACCOUNT_SMTP_PORT":              "587",
		"QSDM_ACCOUNT_SMTP_USERNAME":          "accounts@example.test",
		"QSDM_ACCOUNT_SMTP_PASSWORD":          "test-only-secret",
		"QSDM_ACCOUNT_SMTP_FROM":              "QSDM Account <accounts@example.test>",
		"QSDM_ACCOUNT_SMTP_TLS":               "true",
		"QSDM_ACCOUNT_TELEGRAM_CLIENT_ID":     "",
		"QSDM_ACCOUNT_TELEGRAM_CLIENT_SECRET": "",
	}
	for name, value := range values {
		t.Setenv(name, value)
	}
}

func TestConfigAcceptsHardenedLoopbackEmailSetup(t *testing.T) {
	setValidConfigEnv(t)
	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddress != "127.0.0.1:8092" || !cfg.EmailEnabled() {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestConfigRejectsPublicListenAddress(t *testing.T) {
	setValidConfigEnv(t)
	t.Setenv("QSDM_ACCOUNT_LISTEN", "0.0.0.0:8092")
	_, err := LoadConfigFromEnv()
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("public listen address was accepted: %v", err)
	}
}

func TestConfigRejectsBaseURLThatIsNotAnOrigin(t *testing.T) {
	for _, value := range []string{
		"https://qsdm.tech/account",
		"https://operator:secret@qsdm.tech",
		"http://qsdm.tech",
	} {
		t.Run(value, func(t *testing.T) {
			setValidConfigEnv(t)
			t.Setenv("QSDM_ACCOUNT_PUBLIC_BASE_URL", value)
			_, err := LoadConfigFromEnv()
			if err == nil || !strings.Contains(err.Error(), "HTTPS origin") {
				t.Fatalf("invalid public base URL was accepted: %q error=%v", value, err)
			}
		})
	}
}

func TestConfigRequiresTLSForEmailSignIn(t *testing.T) {
	setValidConfigEnv(t)
	t.Setenv("QSDM_ACCOUNT_SMTP_TLS", "false")
	_, err := LoadConfigFromEnv()
	if err == nil || !strings.Contains(err.Error(), "must remain enabled") {
		t.Fatalf("plaintext SMTP was accepted: %v", err)
	}
}

func TestConfigRejectsPartialSMTPEvenWhenTelegramIsEnabled(t *testing.T) {
	setValidConfigEnv(t)
	t.Setenv("QSDM_ACCOUNT_SMTP_FROM", "")
	t.Setenv("QSDM_ACCOUNT_TELEGRAM_CLIENT_ID", "123456789")
	t.Setenv("QSDM_ACCOUNT_TELEGRAM_CLIENT_SECRET", "test-only-telegram-secret")
	_, err := LoadConfigFromEnv()
	if err == nil || !strings.Contains(err.Error(), "SMTP host and from") {
		t.Fatalf("partial SMTP configuration was accepted: %v", err)
	}
}

func TestConfigRejectsUniformDataKey(t *testing.T) {
	setValidConfigEnv(t)
	t.Setenv("QSDM_ACCOUNT_DATA_KEY", strings.Repeat("00", 32))
	_, err := LoadConfigFromEnv()
	if err == nil || !strings.Contains(err.Error(), "obvious weak value") {
		t.Fatalf("uniform account data key was accepted: %v", err)
	}
}
