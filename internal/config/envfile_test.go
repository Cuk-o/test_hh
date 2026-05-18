package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnvSetsMissingValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "OKEY_PROXY_HOST=gw.dataimpulse.com\n" +
		"OKEY_PROXY_PORT=823\n" +
		"OKEY_PROXY_LOGIN='login__cr.ru'\n" +
		"OKEY_PROXY_PASSWORD=\"secret\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OKEY_PROXY_HOST", "")
	t.Setenv("OKEY_PROXY_PORT", "")
	t.Setenv("OKEY_PROXY_LOGIN", "")
	t.Setenv("OKEY_PROXY_PASSWORD", "")

	if err := LoadDotEnv(path); err != nil {
		t.Fatalf("LoadDotEnv returned error: %v", err)
	}

	if got := os.Getenv("OKEY_PROXY_LOGIN"); got != "login__cr.ru" {
		t.Fatalf("OKEY_PROXY_LOGIN = %q", got)
	}
	if got := os.Getenv("OKEY_PROXY_PASSWORD"); got != "secret" {
		t.Fatalf("OKEY_PROXY_PASSWORD = %q", got)
	}
}

func TestLoadDotEnvDoesNotOverrideExistingEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("OKEY_PROXY_HOST=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OKEY_PROXY_HOST", "from-env")

	if err := LoadDotEnv(path); err != nil {
		t.Fatalf("LoadDotEnv returned error: %v", err)
	}

	if got := os.Getenv("OKEY_PROXY_HOST"); got != "from-env" {
		t.Fatalf("env was overridden: %q", got)
	}
}

func TestLoadDotEnvIgnoresMissingFile(t *testing.T) {
	if err := LoadDotEnv(filepath.Join(t.TempDir(), ".env")); err != nil {
		t.Fatalf("missing .env should be ignored, got %v", err)
	}
}
