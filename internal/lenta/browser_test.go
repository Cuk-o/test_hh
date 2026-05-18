package lenta

import (
	"slices"
	"testing"
)

func TestChromeArgsUseHeadlessServerMode(t *testing.T) {
	args := chromeArgs(9222, "/tmp/profile")
	want := []string{
		"--headless=new",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--enable-unsafe-swiftshader",
		"--remote-debugging-address=127.0.0.1",
		"--window-size=3440,1440",
		"--lang=en-US",
		"--disable-blink-features=AutomationControlled",
	}
	for _, flag := range want {
		if !containsString(args, flag) {
			t.Fatalf("chrome args missing %q: %#v", flag, args)
		}
	}
}

func TestChromeArgsUseVirtualDisplayMode(t *testing.T) {
	args := chromeArgsForMode(9222, "/tmp/profile", chromeVirtualDisplay, "")
	for _, flag := range []string{"--no-sandbox", "--disable-dev-shm-usage", "--remote-debugging-address=127.0.0.1"} {
		if !containsString(args, flag) {
			t.Fatalf("chrome args missing %q: %#v", flag, args)
		}
	}
	if containsString(args, "--headless=new") {
		t.Fatalf("virtual display mode must not use headless flag: %#v", args)
	}
}

func TestChromeArgsIncludeProxyServer(t *testing.T) {
	proxyURL := "http://127.0.0.1:8080"
	args := chromeArgsForMode(9222, "/tmp/profile", chromeHeadless, proxyURL)
	if !containsString(args, "--proxy-server="+proxyURL) {
		t.Fatalf("chrome args missing proxy server: %#v", args)
	}
}

func TestChromeArgsStripProxyCredentials(t *testing.T) {
	args := chromeArgsForMode(9222, "/tmp/profile", chromeHeadless, "http://user:pass@127.0.0.1:8080")
	if !containsString(args, "--proxy-server=http://127.0.0.1:8080") {
		t.Fatalf("chrome args missing sanitized proxy server: %#v", args)
	}
	if containsString(args, "--proxy-server=http://user:pass@127.0.0.1:8080") {
		t.Fatalf("chrome args leaked proxy credentials: %#v", args)
	}
}

func TestChromeProxyCredentials(t *testing.T) {
	credentials := chromeProxyCredentials("http://user:pass@127.0.0.1:8080")
	if credentials == nil || credentials.Username != "user" || credentials.Password != "pass" {
		t.Fatalf("credentials = %#v", credentials)
	}
}

func containsString(values []string, want string) bool {
	return slices.Contains(values, want)
}
