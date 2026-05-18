package lenta

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"okeyparser/internal/parser"
)

const defaultBaseURL = "https://lenta.com"

type Category struct {
	Name  string
	Title string
	Slug  string
	ID    int
}

type Options struct {
	BaseURL              string
	PageLimit            int
	MaxPages             int
	CookieProvider       func(context.Context, string) ([]*http.Cookie, error)
	DelayMin             time.Duration
	DelayMax             time.Duration
	Delay                func(context.Context) error
	Logger               *log.Logger
	runtimeSessionToken  string
	runtimeDeviceID      string
	runtimeUserSessionID string
}

type Client struct {
	httpClient *http.Client
	options    Options
}

type Worker struct {
	Client   *Client
	Category Category
}

type catalogRequest struct {
	CategoryID int            `json:"categoryId"`
	Filters    catalogFilters `json:"filters"`
	Sort       catalogSort    `json:"sort"`
	Limit      int            `json:"limit"`
	Offset     int            `json:"offset"`
}

type catalogFilters struct {
	Checkbox      []string `json:"checkbox"`
	Multicheckbox []string `json:"multicheckbox"`
	Range         []string `json:"range"`
}

type catalogSort struct {
	Type  string `json:"type"`
	Order string `json:"order"`
}

type catalogResponse struct {
	Items []catalogItem `json:"items"`
	Total int           `json:"total"`
}

type catalogItem struct {
	ID      int           `json:"id"`
	Slug    string        `json:"slug"`
	Name    string        `json:"name"`
	Prices  catalogPrices `json:"prices"`
	StoreID int           `json:"storeId"`
}

type catalogPrices struct {
	Price float64 `json:"price"`
	Cost  float64 `json:"cost"`
}

type runtimeSession struct {
	Token         string
	DeviceID      string
	UserSessionID string
}

func Categories() []Category {
	return []Category{
		{Name: "fruits", Title: "Овощи и фрукты", Slug: "ovoshchi-frukty", ID: 144},
		{Name: "fish", Title: "Рыба, икра, морепродукты", Slug: "ryba-ikra-moreprodukty", ID: 183},
		{Name: "dairy", Title: "Молочные продукты", Slug: "molochnye-produkty", ID: 3},
	}
}


func NewClient(httpClient *http.Client, options Options) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if options.BaseURL == "" {
		options.BaseURL = defaultBaseURL
	}
	if options.PageLimit <= 0 {
		options.PageLimit = 40
	}
	if options.Delay == nil {
		options.Delay = randomDelay(options.DelayMin, options.DelayMax)
	}
	return &Client{httpClient: httpClient, options: options}
}

func HTTPClientForProxy(proxyURL string) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if strings.TrimSpace(proxyURL) != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	return &http.Client{Transport: transport, Jar: jar, Timeout: 45 * time.Second}, nil
}

func ProductURL(slug string, id int) string {
	return fmt.Sprintf("https://lenta.com/product/%s-%06d/", slug, id)
}

func ParseCatalogItems(body []byte, category Category) ([]parser.Product, int, error) {
	var response catalogResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, 0, err
	}
	products := make([]parser.Product, 0, len(response.Items))
	for _, item := range response.Items {
		if item.ID == 0 || item.Slug == "" || item.Name == "" {
			continue
		}
		products = append(products, parser.Product{
			Category:    category.Name,
			Subcategory: category.Title,
			Name:        item.Name,
			Price:       formatPrice(item.Prices.Price),
			URL:         ProductURL(item.Slug, item.ID),
		})
	}
	return products, response.Total, nil
}

func (client *Client) FetchPage(ctx context.Context, category Category, offset int) ([]parser.Product, int, error) {
	page := offset/client.options.PageLimit + 1
	client.logf("category=%s step=api_page page=%d offset=%d limit=%d", category.Name, page, offset, client.options.PageLimit)
	body, err := json.Marshal(catalogRequest{
		CategoryID: category.ID,
		Filters:    catalogFilters{Checkbox: []string{}, Multicheckbox: []string{}, Range: []string{}},
		Sort:       catalogSort{Type: "popular", Order: "desc"},
		Limit:      client.options.PageLimit,
		Offset:     offset,
	})
	if err != nil {
		return nil, 0, err
	}
	endpoint := strings.TrimRight(client.options.BaseURL, "/") + "/api-gateway/v1/catalog/items"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	client.setHeaders(req)
	res, err := client.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, 0, fmt.Errorf("fetch category %d offset %d: %s", category.ID, offset, res.Status)
	}
	gzipped := res.Header.Get("Content-Encoding") == "gzip"
	responseBody, err := readResponseBody(res)
	if err != nil {
		return nil, 0, err
	}
	products, total, err := ParseCatalogItems(responseBody, category)
	if err != nil {
		return nil, 0, err
	}
	client.logf("category=%s step=api_page_done page=%d offset=%d rows=%d total=%d gzip=%t", category.Name, page, offset, len(products), total, gzipped)
	return products, total, nil
}

func (client *Client) FetchCategory(ctx context.Context, category Category) ([]parser.Product, error) {
	client.logf("category=%s step=start id=%d title=%q", category.Name, category.ID, category.Title)
	if err := client.ensureRuntimeSession(ctx, category); err != nil {
		return nil, err
	}
	products := make([]parser.Product, 0)
	for page := 0; ; page++ {
		offset := page * client.options.PageLimit
		pageProducts, total, err := client.FetchPage(ctx, category, offset)
		if err != nil {
			return nil, err
		}
		products = append(products, pageProducts...)
		if len(pageProducts) == 0 {
			client.logf("category=%s step=stop reason=empty_page rows=%d total=%d", category.Name, len(products), total)
			return products, nil
		}
		if len(products) >= total {
			client.logf("category=%s step=stop reason=total_reached rows=%d total=%d", category.Name, len(products), total)
			return products, nil
		}
		if client.options.MaxPages > 0 && page+1 >= client.options.MaxPages {
			client.logf("category=%s step=stop reason=max_pages rows=%d total=%d max_pages=%d", category.Name, len(products), total, client.options.MaxPages)
			return products, nil
		}
		client.logf("category=%s step=delay", category.Name)
		if err := client.options.Delay(ctx); err != nil {
			return nil, err
		}
	}
}

func (client *Client) ensureRuntimeSession(ctx context.Context, category Category) error {
	client.setRuntimeSessionOptions()
	if client.hasRuntimeSession() || client.options.CookieProvider == nil || client.httpClient.Jar == nil {
		client.logf("category=%s step=session_http missing=%t", category.Name, !client.hasRuntimeSession())
		return nil
	}
	client.logf("category=%s step=session_http missing=true", category.Name)
	client.logf("category=%s step=session_browser start", category.Name)
	cookies, err := client.options.CookieProvider(ctx, client.catalogRootURL())
	if err != nil {
		return err
	}
	client.logf("category=%s step=session_browser done cookies=%d", category.Name, len(cookies))
	client.httpClient.Jar.SetCookies(client.baseURL(), cookies)
	client.setRuntimeSessionOptions()
	return nil
}

func (client *Client) hasRuntimeSession() bool {
	return client.options.runtimeSessionToken != "" && client.options.runtimeDeviceID != "" && client.options.runtimeUserSessionID != ""
}

func readResponseBody(res *http.Response) ([]byte, error) {
	if res.Header.Get("Content-Encoding") != "gzip" {
		return io.ReadAll(res.Body)
	}
	reader, err := gzip.NewReader(res.Body)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func (client *Client) setRuntimeSessionOptions() {
	if client.httpClient.Jar == nil {
		return
	}
	session := sessionFromCookies(client.httpClient.Jar.Cookies(client.baseURL()))
	client.options.runtimeSessionToken = session.Token
	client.options.runtimeDeviceID = session.DeviceID
	client.options.runtimeUserSessionID = session.UserSessionID
}

func sessionFromCookies(cookies []*http.Cookie) runtimeSession {
	session := runtimeSession{}
	for _, cookie := range cookies {
		switch cookie.Name {
		case "Utk_SessionToken":
			session.Token = cookie.Value
		case "Utk_DvcGuid":
			session.DeviceID = cookie.Value
		case "UserSessionId":
			session.UserSessionID = cookie.Value
		}
	}
	return session
}

func (client *Client) baseURL() *url.URL {
	parsed, err := url.Parse(strings.TrimRight(client.options.BaseURL, "/"))
	if err != nil {
		return &url.URL{Scheme: "https", Host: "lenta.com"}
	}
	return parsed
}

func (client *Client) bootstrapCatalogPage(ctx context.Context, category Category) error {
	if client.httpClient.Jar == nil {
		return nil
	}
	endpoint := client.catalogPageURL(category)
	client.logf("category=%s step=bootstrap_catalog url=%s", category.Name, endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	client.setBrowserHeaders(req)
	res, err := client.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		client.logf("category=%s step=bootstrap_catalog status=%d continue=true", category.Name, res.StatusCode)
		return nil
	}
	if res.StatusCode < 200 || res.StatusCode > 399 {
		return fmt.Errorf("bootstrap category %d: %s", category.ID, res.Status)
	}
	client.logf("category=%s step=bootstrap_catalog status=%d", category.Name, res.StatusCode)
	return nil
}

func (category Category) catalogSlug() string {
	if category.Slug != "" {
		return category.Slug
	}
	return category.Name
}

func (client *Client) catalogPageURL(category Category) string {
	return fmt.Sprintf("%s/catalog/%s-%d/", strings.TrimRight(client.options.BaseURL, "/"), category.catalogSlug(), category.ID)
}

func (client *Client) catalogRootURL() string {
	return strings.TrimRight(client.options.BaseURL, "/") + "/catalog/"
}

func FetchCategories(ctx context.Context, workers []Worker, concurrency int) ([]parser.Product, error) {
	if concurrency <= 0 {
		concurrency = len(workers)
	}
	sem := make(chan struct{}, concurrency)
	productsByWorker := make([][]parser.Product, len(workers))
	errs := make(chan error, len(workers))
	var wg sync.WaitGroup
	for i, worker := range workers {
		i, worker := i, worker
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if i > 0 {
				time.Sleep(time.Duration(i) * 2000 * time.Millisecond)
			}
			products, err := worker.Client.FetchCategory(ctx, worker.Category)
			if err != nil {
				errs <- err
				return
			}
			productsByWorker[i] = products
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return nil, err
		}
	}
	products := make([]parser.Product, 0)
	for _, workerProducts := range productsByWorker {
		products = append(products, workerProducts...)
	}
	return products, nil
}

func (client *Client) logf(format string, args ...any) {
	if client.options.Logger == nil {
		return
	}
	client.options.Logger.Printf(format, args...)
}

func (client *Client) setHeaders(req *http.Request) {
	client.setBrowserHeaders(req)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json, text/plain, */*")
	req.Header.Set("accept-encoding", "gzip")
	req.Header.Set("client", "angular_web_0.0.2")
	req.Header.Set("x-domain", "moscow")
	req.Header.Set("x-delivery-mode", "pickup")
	req.Header.Set("x-platform", "omniweb")
	req.Header.Set("x-retail-brand", "lo")
	req.Header.Set("x-device-os", "Web")
	req.Header.Set("x-device-web-platform", "desktop_web")
	req.Header.Set("origin", defaultBaseURL)
	req.Header.Set("referer", strings.TrimRight(client.options.BaseURL, "/")+"/")
	if client.options.runtimeSessionToken != "" {
		req.Header.Set("sessiontoken", client.options.runtimeSessionToken)
	}
	if client.options.runtimeDeviceID != "" {
		req.Header.Set("deviceid", client.options.runtimeDeviceID)
		req.Header.Set("x-device-id", client.options.runtimeDeviceID)
	}
	if client.options.runtimeUserSessionID != "" {
		req.Header.Set("x-user-session-id", client.options.runtimeUserSessionID)
	}
}

func (client *Client) setBrowserHeaders(req *http.Request) {
	req.Header.Set("user-agent", browserUserAgent)
	req.Header.Set("accept-encoding", "gzip")
}

func formatPrice(price float64) string {
	return fmt.Sprintf("%.2f", price/100)
}

func randomDelay(minDelay, maxDelay time.Duration) func(context.Context) error {
	if minDelay <= 0 && maxDelay <= 0 {
		return func(context.Context) error { return nil }
	}
	if maxDelay < minDelay {
		maxDelay = minDelay
	}
	return func(ctx context.Context) error {
		delay := minDelay
		if maxDelay > minDelay {
			delay += time.Duration(rand.Int63n(int64(maxDelay - minDelay)))
		}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}
}
