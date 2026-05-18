package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ProxyURL                   string
	ProxyURLs                  []string
	ProductLimitPerSubcategory int
	OutputPath                 string
	PageLimit                  int
	MaxPages                   int
	DelayMinMS                 int
	DelayMaxMS                 int
	ScanConcurrency            int
	ScanLimit                  int
}

func FromEnv() (Config, error) {
	proxy := firstEnv("LENTA_PROXY", "OKEY_PROXY")
	proxyList := parseProxyList(firstEnv("LENTA_PROXY_LIST", "OKEY_PROXY_LIST"))
	if proxy == "" && len(proxyList) == 0 {
		builtProxy, builtList, err := proxyFromSplitEnv()
		if err != nil {
			return Config{}, err
		}
		proxy = builtProxy
		proxyList = builtList
	}
	if proxy == "" && len(proxyList) == 0 {
		proxyList = []string{""}
	} else if proxy == "" {
		proxy = proxyList[0]
	}
	if proxy != "" {
		if _, err := url.ParseRequestURI(proxy); err != nil {
			return Config{}, err
		}
	}
	if len(proxyList) == 0 {
		proxyList = []string{proxy}
	}
	for _, proxyValue := range proxyList {
		if proxyValue == "" {
			continue
		}
		if _, err := url.ParseRequestURI(proxyValue); err != nil {
			return Config{}, err
		}
	}
	return Config{
		ProxyURL:                   proxy,
		ProxyURLs:                  proxyList,
		ProductLimitPerSubcategory: 0,
		OutputPath:                 "products.csv",
		PageLimit:                  envInt("LENTA_PAGE_LIMIT", 40),
		MaxPages:                   envInt("LENTA_MAX_PAGES", 0),
		DelayMinMS:                 envInt("LENTA_DELAY_MIN_MS", 1200),
		DelayMaxMS:                 envInt("LENTA_DELAY_MAX_MS", 3400),
		ScanConcurrency:            envInt("LENTA_SCAN_CONCURRENCY", 1),
		ScanLimit:                  envInt("LENTA_SCAN_LIMIT", 0),
	}, nil
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		value := strings.TrimSpace(os.Getenv(key))
		if value != "" {
			return value
		}
	}
	return ""
}

func proxyFromSplitEnv() (string, []string, error) {
	host := strings.TrimSpace(os.Getenv("OKEY_PROXY_HOST"))
	port := strings.TrimSpace(os.Getenv("OKEY_PROXY_PORT"))
	portStart := strings.TrimSpace(os.Getenv("OKEY_PROXY_PORT_START"))
	portCount := envInt("OKEY_PROXY_PORT_COUNT", 1)
	login := strings.TrimSpace(os.Getenv("OKEY_PROXY_LOGIN"))
	password := strings.TrimSpace(os.Getenv("OKEY_PROXY_PASSWORD"))
	parts := []string{host, port, portStart, login, password}
	anySet := false
	for _, part := range parts {
		if part != "" {
			anySet = true
			break
		}
	}
	if !anySet {
		return "", nil, nil
	}
	if host == "" || login == "" || password == "" {
		return "", nil, errors.New("split proxy env requires OKEY_PROXY_HOST, OKEY_PROXY_LOGIN, OKEY_PROXY_PASSWORD")
	}
	if portStart != "" {
		start, err := strconv.Atoi(portStart)
		if err != nil {
			return "", nil, err
		}
		skippedPorts := parsePortSkipList(os.Getenv("OKEY_PROXY_PORT_SKIP"))
		skippedPorts[10011] = true
		proxies := make([]string, 0, portCount)
		for i := 0; i < portCount; i++ {
			portValue := start + i
			if skippedPorts[portValue] {
				continue
			}
			proxies = append(proxies, formatProxy(host, strconv.Itoa(portValue), login, password))
		}
		if len(proxies) == 0 {
			return "", nil, errors.New("all sticky proxy ports were skipped")
		}
		return proxies[0], proxies, nil
	}
	if port == "" {
		return "", nil, errors.New("split proxy env requires OKEY_PROXY_PORT or OKEY_PROXY_PORT_START")
	}
	proxy := formatProxy(host, port, login, password)
	return proxy, []string{proxy}, nil
}

func formatProxy(host, port, login, password string) string {
	return fmt.Sprintf("http://%s:%s@%s:%s", url.QueryEscape(login), url.QueryEscape(password), host, port)
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseProxyList(raw string) []string {
	parts := strings.Split(raw, ",")
	proxies := make([]string, 0, len(parts))
	for _, part := range parts {
		proxy := strings.TrimSpace(part)
		if proxy != "" {
			proxies = append(proxies, proxy)
		}
	}
	return proxies
}

func parsePortSkipList(raw string) map[int]bool {
	skipped := map[int]bool{}
	for _, part := range strings.Split(raw, ",") {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		portValue, err := strconv.Atoi(value)
		if err == nil {
			skipped[portValue] = true
		}
	}
	return skipped
}
