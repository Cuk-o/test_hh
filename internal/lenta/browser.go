package lenta

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type cdpTarget struct {
	Type                 string `json:"type"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

type cdpResponse struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *cdpError       `json:"error"`
}

type cdpError struct {
	Message string `json:"message"`
}

type cdpCookiesResult struct {
	Cookies []cdpCookie `json:"cookies"`
}

type cdpNavigateResult struct {
	FrameID string `json:"frameId"`
}

type cdpFetchAuthRequiredParams struct {
	RequestID string `json:"requestId"`
}

type cdpFetchRequestPausedParams struct {
	RequestID string `json:"requestId"`
}

type proxyCredentials struct {
	Username string
	Password string
}

type cdpCookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Secure   bool   `json:"secure"`
	HTTPOnly bool   `json:"httpOnly"`
}

type chromeMode string

const (
	chromeHeadless       chromeMode = "headless"
	chromeVirtualDisplay chromeMode = "virtual-display"
	browserUserAgent     string     = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36"
)

func BrowserCookieProvider(proxyURL string) func(context.Context, string) ([]*http.Cookie, error) {
	return func(ctx context.Context, pageURL string) ([]*http.Cookie, error) {
		cookies, err := browserCookieProviderWithMode(ctx, pageURL, chromeHeadless, proxyURL)
		if err == nil {
			return cookies, nil
		}
		virtualDisplayCookies, virtualDisplayErr := browserCookieProviderWithMode(ctx, pageURL, chromeVirtualDisplay, proxyURL)
		if virtualDisplayErr == nil {
			return virtualDisplayCookies, nil
		}
		return nil, fmt.Errorf("chrome headless cookie capture failed: %w; chrome virtual display fallback failed: %v", err, virtualDisplayErr)
	}
}

func chromeArgs(port int, profileDir string) []string {
	return chromeArgsForMode(port, profileDir, chromeHeadless, "")
}

func chromeArgsForMode(port int, profileDir string, mode chromeMode, proxyURL string) []string {
	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--remote-debugging-address=127.0.0.1",
		"--remote-allow-origins=*",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--enable-unsafe-swiftshader",
		"--window-size=3440,1440",
		"--lang=en-US",
		"--disable-blink-features=AutomationControlled",
		"--no-first-run",
		"--no-default-browser-check",
		"--user-data-dir=" + profileDir,
		"--user-agent=" + browserUserAgent,
	}
	if proxyServer := chromeProxyServer(proxyURL); proxyServer != "" {
		args = append(args, "--proxy-server="+proxyServer)
	}
	args = append(args, "about:blank")

	if mode == chromeHeadless {
		args = append([]string{"--headless=new"}, args...)
	}
	return args
}

func chromeCommand(ctx context.Context, mode chromeMode, port int, profileDir string, proxyURL string) *exec.Cmd {
	args := chromeArgsForMode(port, profileDir, mode, proxyURL)
	if mode == chromeVirtualDisplay {
		return exec.CommandContext(ctx, "/usr/bin/xvfb-run", append([]string{"-a", "/usr/bin/google-chrome"}, args...)...)
	}
	return exec.CommandContext(ctx, "/usr/bin/google-chrome", args...)
}

func browserCookieProviderWithMode(ctx context.Context, pageURL string, mode chromeMode, proxyURL string) ([]*http.Cookie, error) {
	profileDir, err := os.MkdirTemp("", "lenta-chrome-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(profileDir)

	port, err := freeLocalPort()
	if err != nil {
		return nil, err
	}
	log.Printf("[browser] profile=%s port=%d cmd=chrome", profileDir, port)

	// Hard timeout: Chrome must complete cookie fetch in 90 seconds.
	// This prevents indefinite hangs if Chrome freezes.
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	cmd := chromeCommand(ctx, mode, port, profileDir, proxyURL)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	wsURL, err := waitForPageWebSocket(ctx, port)
	log.Printf("[browser:port%d] wsURL obtained: %v", port, err)
	if err != nil {
		return nil, err
	}
	log.Printf("[browser:port%d] dialing websocket...", port)
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, http.Header{"Origin": {fmt.Sprintf("http://127.0.0.1:%d", port)}})
	log.Printf("[browser:port%d] ws dialed: %v", port, err)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	credentials := chromeProxyCredentials(proxyURL)
	log.Printf("[browser:port%d] preparing page...", port)
	if err := prepareBrowserPage(conn, credentials); err != nil {
		return nil, err
	}
	log.Printf("[browser:port%d] navigating to %s...", port, pageURL)
	if err := navigateBrowserPage(conn, pageURL, credentials); err != nil {
		return nil, err
	}
	log.Printf("[browser:port%d] waiting for session cookies...", port)
	if err := waitForSessionCookie(ctx, conn, credentials); err != nil {
		return nil, err
	}
	log.Printf("[browser:port%d] fetching cookies...", port)
	return getBrowserCookies(conn, credentials)
}

func freeLocalPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func waitForPageWebSocket(ctx context.Context, port int) (string, error) {
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/json", port)
	deadline := time.Now().Add(60 * time.Second)
	retryDelay := 100 * time.Millisecond
	client := &http.Client{Timeout: 2 * time.Second}
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return "", err
		}
		res, err := client.Do(req)
		if err == nil {
			var targets []cdpTarget
			decodeErr := json.NewDecoder(res.Body).Decode(&targets)
			bodyBytes, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if decodeErr == nil {
				for _, target := range targets {
					if target.Type == "page" && target.WebSocketDebuggerURL != "" {
						log.Printf("[ws:port%d] found target id=%s url=%s", port, target.Type, target.WebSocketDebuggerURL)
						return target.WebSocketDebuggerURL, nil
					}
				}
				log.Printf("[ws:port%d] attempt %d no page target, targets=%d body=%s", port, attempt, len(targets), string(bodyBytes))
			} else {
				log.Printf("[ws:port%d] attempt %d decode err=%v body=%s", port, attempt, decodeErr, string(bodyBytes))
			}
		} else {
			log.Printf("[ws:port%d] attempt %d err=%v", port, attempt, err)
		}
		if attempt%5 == 0 && attempt > 0 {
			log.Printf("[ws:port%d] attempt %d not ready, body len=%d", port, attempt, res.ContentLength)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(retryDelay):
			if retryDelay < 500*time.Millisecond {
				retryDelay *= 2
			}
		}
	}
	return "", fmt.Errorf("chrome devtools page target not ready (port %d)", port)
}

func prepareBrowserPage(conn *websocket.Conn, credentials *proxyCredentials) error {
	commands := []cdpCommand{
		{Method: "Network.enable"},
		{Method: "Page.enable"},
		{Method: "Runtime.enable"},
		{Method: "Network.setExtraHTTPHeaders", Params: map[string]any{"headers": map[string]string{"Accept-Language": "en-US,en;q=0.9"}}},
	}
	if credentials != nil {
		commands = append([]cdpCommand{{Method: "Fetch.enable", Params: map[string]any{"handleAuthRequests": true}}}, commands...)
	}
	for _, command := range commands {
		if _, err := sendCDPCommand(conn, command.Method, command.Params, credentials); err != nil {
			return err
		}
	}
	return nil
}

func navigateBrowserPage(conn *websocket.Conn, pageURL string, credentials *proxyCredentials) error {
	log.Printf("[browser] navigating to %s", pageURL)
	response, err := sendCDPCommand(conn, "Page.navigate", map[string]any{"url": pageURL}, credentials)
	if err != nil {
		return err
	}
	var result cdpNavigateResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return err
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var event cdpResponse
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if err := conn.ReadJSON(&event); err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return err
		}
		if handled, err := handleCDPEvent(conn, event, credentials); handled || err != nil {
			if err != nil {
				return err
			}
			continue
		}
		if event.Method == "Page.loadEventFired" {
			log.Printf("[browser] page load event fired")
			return nil
		}
		if event.Method != "" && !strings.HasPrefix(event.Method, "Fetch") && !strings.HasPrefix(event.Method, "Network") {
			log.Printf("[browser] event method=%s", event.Method)
		}
	}
	return fmt.Errorf("chrome page load timed out")
}

func waitForSessionCookie(ctx context.Context, conn *websocket.Conn, credentials *proxyCredentials) error {
	deadline := time.Now().Add(90 * time.Second)
	retryDelay := 100 * time.Millisecond
	var lastCookies []*http.Cookie
	for time.Now().Before(deadline) {
		cookies, err := getBrowserCookies(conn, credentials)
		if err != nil {
			return err
		}
		lastCookies = cookies
		if hasCompleteSession(cookies) {
			log.Printf("[browser] session cookies complete: %s", cookieNames(cookies))
			return nil
		}
		log.Printf("[browser] session cookies not ready yet, checking again in %v...", retryDelay)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelay):
			if retryDelay < 500*time.Millisecond {
				retryDelay *= 2
			}
		}
	}
	return fmt.Errorf("chrome did not receive required Lenta session cookies; seen=%s", cookieNames(lastCookies))
}

func getBrowserCookies(conn *websocket.Conn, credentials *proxyCredentials) ([]*http.Cookie, error) {
	response, err := sendCDPCommand(conn, "Storage.getCookies", nil, credentials)
	if err != nil {
		return nil, err
	}
	var result cdpCookiesResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return nil, err
	}
	return toHTTPCookies(result.Cookies), nil
}

type cdpCommand struct {
	Method string
	Params map[string]any
}

var cdpID int

func sendCDPCommand(conn *websocket.Conn, method string, params map[string]any, credentials *proxyCredentials) (cdpResponse, error) {
	cdpID++
	request := map[string]any{"id": cdpID, "method": method}
	if params != nil {
		request["params"] = params
	}
	if err := conn.WriteJSON(request); err != nil {
		return cdpResponse{}, err
	}
	for {
		var response cdpResponse
		if err := conn.ReadJSON(&response); err != nil {
			return cdpResponse{}, err
		}
		if handled, err := handleCDPEvent(conn, response, credentials); handled || err != nil {
			if err != nil {
				return cdpResponse{}, err
			}
			continue
		}
		if response.ID != cdpID {
			continue
		}
		if response.Error != nil {
			return cdpResponse{}, fmt.Errorf("chrome %s: %s", method, response.Error.Message)
		}
		return response, nil
	}
}

func handleCDPEvent(conn *websocket.Conn, event cdpResponse, credentials *proxyCredentials) (bool, error) {
	switch event.Method {
	case "Fetch.authRequired":
		if credentials == nil {
			return true, nil
		}
		var params cdpFetchAuthRequiredParams
		if err := json.Unmarshal(event.Params, &params); err != nil {
			return true, err
		}
		cdpID++
		return true, conn.WriteJSON(map[string]any{
			"id":     cdpID,
			"method": "Fetch.continueWithAuth",
			"params": map[string]any{
				"requestId": params.RequestID,
				"authChallengeResponse": map[string]string{
					"response": "ProvideCredentials",
					"username": credentials.Username,
					"password": credentials.Password,
				},
			},
		})
	case "Fetch.requestPaused":
		var params cdpFetchRequestPausedParams
		if err := json.Unmarshal(event.Params, &params); err != nil {
			return true, err
		}
		cdpID++
		return true, conn.WriteJSON(map[string]any{
			"id":     cdpID,
			"method": "Fetch.continueRequest",
			"params": map[string]string{"requestId": params.RequestID},
		})
	default:
		return false, nil
	}
}

func chromeProxyServer(proxyURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(proxyURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimSpace(proxyURL)
	}
	parsed.User = nil
	return parsed.String()
}

func chromeProxyCredentials(proxyURL string) *proxyCredentials {
	parsed, err := url.Parse(strings.TrimSpace(proxyURL))
	if err != nil || parsed.User == nil {
		return nil
	}
	password, _ := parsed.User.Password()
	return &proxyCredentials{Username: parsed.User.Username(), Password: password}
}

func hasCompleteSession(cookies []*http.Cookie) bool {
	session := sessionFromCookies(cookies)
	return session.Token != "" && session.DeviceID != "" && session.UserSessionID != ""
}

func cookieNames(cookies []*http.Cookie) string {
	names := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		names = append(names, cookie.Name)
	}
	return fmt.Sprintf("%v", names)
}

func toHTTPCookies(browserCookies []cdpCookie) []*http.Cookie {
	cookies := make([]*http.Cookie, 0, len(browserCookies))
	for _, cookie := range browserCookies {
		cookies = append(cookies, &http.Cookie{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Path:     cookie.Path,
			Domain:   cookie.Domain,
			Secure:   cookie.Secure,
			HttpOnly: cookie.HTTPOnly,
		})
	}
	return cookies
}
