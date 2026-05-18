package config

import (
	"strings"
	"testing"
)

func TestFromEnvDefaultsToDirectMode(t *testing.T) {
	t.Setenv("OKEY_PROXY", "")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv returned error: %v", err)
	}
	if cfg.ProxyURL != "" || len(cfg.ProxyURLs) != 1 || cfg.ProxyURLs[0] != "" {
		t.Fatalf("direct mode expected, got %#v", cfg.ProxyURLs)
	}
}

func TestFromEnvBuildsConfigFromProxy(t *testing.T) {
	t.Setenv("OKEY_PROXY", "http://user:pass@example.test:823")
	t.Setenv("LENTA_MAX_PAGES", "3")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv returned error: %v", err)
	}
	if cfg.ProxyURL != "http://user:pass@example.test:823" {
		t.Fatalf("ProxyURL = %q", cfg.ProxyURL)
	}
	if cfg.OutputPath != "products.csv" {
		t.Fatalf("OutputPath = %q", cfg.OutputPath)
	}
	if cfg.MaxPages != 3 {
		t.Fatalf("MaxPages = %d", cfg.MaxPages)
	}
}

func TestFromEnvPrefersLentaProxyAndCrawlerOptions(t *testing.T) {
	t.Setenv("OKEY_PROXY", "http://old:proxy@example.test:823")
	t.Setenv("LENTA_PROXY", "http://user:pass@lenta-proxy.test:10000")
	t.Setenv("LENTA_PAGE_LIMIT", "20")
	t.Setenv("LENTA_MAX_PAGES", "3")
	t.Setenv("LENTA_DELAY_MIN_MS", "1500")
	t.Setenv("LENTA_DELAY_MAX_MS", "2500")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv returned error: %v", err)
	}
	if cfg.ProxyURL != "http://user:pass@lenta-proxy.test:10000" {
		t.Fatalf("ProxyURL = %q", cfg.ProxyURL)
	}
	if cfg.PageLimit != 20 || cfg.MaxPages != 3 {
		t.Fatalf("limits = %d/%d", cfg.PageLimit, cfg.MaxPages)
	}
	if cfg.DelayMinMS != 1500 || cfg.DelayMaxMS != 2500 {
		t.Fatalf("delays = %d/%d", cfg.DelayMinMS, cfg.DelayMaxMS)
	}
}

func TestFromEnvBuildsProxyFromSplitCredentials(t *testing.T) {
	t.Setenv("OKEY_PROXY", "")
	t.Setenv("OKEY_PROXY_HOST", "gw.dataimpulse.com")
	t.Setenv("OKEY_PROXY_PORT", "823")
	t.Setenv("OKEY_PROXY_LOGIN", "login__cr.ru")
	t.Setenv("OKEY_PROXY_PASSWORD", "secret")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv returned error: %v", err)
	}

	want := "http://login__cr.ru:secret@gw.dataimpulse.com:823"
	if cfg.ProxyURL != want {
		t.Fatalf("ProxyURL = %q, want %q", cfg.ProxyURL, want)
	}
	if len(cfg.ProxyURLs) != 1 || cfg.ProxyURLs[0] != want {
		t.Fatalf("ProxyURLs = %#v", cfg.ProxyURLs)
	}
}

func TestFromEnvSplitProxyRequiresAllParts(t *testing.T) {
	t.Setenv("OKEY_PROXY", "")
	t.Setenv("OKEY_PROXY_HOST", "gw.dataimpulse.com")
	t.Setenv("OKEY_PROXY_PORT", "823")
	t.Setenv("OKEY_PROXY_LOGIN", "login")
	t.Setenv("OKEY_PROXY_PASSWORD", "")

	_, err := FromEnv()
	if err == nil {
		t.Fatal("expected split proxy password error")
	}
}

func TestFromEnvBuildsStickyProxyPortRange(t *testing.T) {
	t.Setenv("OKEY_PROXY", "")
	t.Setenv("OKEY_PROXY_LIST", "")
	t.Setenv("OKEY_PROXY_HOST", "gw.dataimpulse.com")
	t.Setenv("OKEY_PROXY_LOGIN", "login__cr.ru")
	t.Setenv("OKEY_PROXY_PASSWORD", "secret")
	t.Setenv("OKEY_PROXY_PORT_START", "10000")
	t.Setenv("OKEY_PROXY_PORT_COUNT", "3")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv returned error: %v", err)
	}
	want := []string{
		"http://login__cr.ru:secret@gw.dataimpulse.com:10000",
		"http://login__cr.ru:secret@gw.dataimpulse.com:10001",
		"http://login__cr.ru:secret@gw.dataimpulse.com:10002",
	}
	for i := range want {
		if cfg.ProxyURLs[i] != want[i] {
			t.Fatalf("ProxyURLs[%d] = %q, want %q", i, cfg.ProxyURLs[i], want[i])
		}
	}
}

func TestFromEnvSkipsBadStickyProxyPorts(t *testing.T) {
	t.Setenv("OKEY_PROXY", "")
	t.Setenv("OKEY_PROXY_LIST", "")
	t.Setenv("OKEY_PROXY_HOST", "gw.dataimpulse.com")
	t.Setenv("OKEY_PROXY_LOGIN", "login__cr.ru")
	t.Setenv("OKEY_PROXY_PASSWORD", "secret")
	t.Setenv("OKEY_PROXY_PORT_START", "10000")
	t.Setenv("OKEY_PROXY_PORT_COUNT", "4")
	t.Setenv("OKEY_PROXY_PORT_SKIP", "10001, 10003")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv returned error: %v", err)
	}
	want := []string{
		"http://login__cr.ru:secret@gw.dataimpulse.com:10000",
		"http://login__cr.ru:secret@gw.dataimpulse.com:10002",
	}
	if len(cfg.ProxyURLs) != len(want) {
		t.Fatalf("ProxyURLs = %#v, want %#v", cfg.ProxyURLs, want)
	}
	for i := range want {
		if cfg.ProxyURLs[i] != want[i] {
			t.Fatalf("ProxyURLs[%d] = %q, want %q", i, cfg.ProxyURLs[i], want[i])
		}
	}
}

func TestFromEnvSkipsKnownBadStickyProxyPortByDefault(t *testing.T) {
	t.Setenv("OKEY_PROXY", "")
	t.Setenv("OKEY_PROXY_LIST", "")
	t.Setenv("OKEY_PROXY_HOST", "gw.dataimpulse.com")
	t.Setenv("OKEY_PROXY_LOGIN", "login__cr.ru")
	t.Setenv("OKEY_PROXY_PASSWORD", "secret")
	t.Setenv("OKEY_PROXY_PORT_START", "10010")
	t.Setenv("OKEY_PROXY_PORT_COUNT", "3")
	t.Setenv("OKEY_PROXY_PORT_SKIP", "")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv returned error: %v", err)
	}
	for _, proxy := range cfg.ProxyURLs {
		if strings.HasSuffix(proxy, ":10011") {
			t.Fatalf("ProxyURLs must skip known bad 10011 by default: %#v", cfg.ProxyURLs)
		}
	}
}
