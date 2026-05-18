package lenta

import (
	"context"
	"os"
	"testing"
)

func TestHeadlessBrowserCookie(t *testing.T) {
	if os.Getenv("LENTA_LIVE_TESTS") != "1" {
		t.Skip("set LENTA_LIVE_TESTS=1 to run live Chrome cookie capture")
	}
	ctx := context.Background()
	provider := BrowserCookieProvider("")

	cookies, err := provider(ctx, "https://lenta.com")
	if err != nil {
		t.Fatalf("failed to get cookies: %v", err)
	}

	for _, c := range cookies {
		t.Logf("cookie: %s=%s", c.Name, c.Value)
	}
}

func TestBrowserCookieHeadlessMode(t *testing.T) {
	if os.Getenv("LENTA_LIVE_TESTS") != "1" {
		t.Skip("set LENTA_LIVE_TESTS=1 to run live Chrome cookie capture")
	}
	ctx := context.Background()
	cookies, err := browserCookieProviderWithMode(ctx, "https://lenta.com", chromeHeadless, "")
	t.Logf("got %d cookies", len(cookies))
	for _, c := range cookies {
		t.Logf("  %s = %s", c.Name, c.Value)
	}
	if err != nil {
		t.Logf("error: %v", err)
	}
}
