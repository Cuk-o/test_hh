package lenta

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseCatalogItemsResponse(t *testing.T) {
	body := []byte(`{
		"total": 1,
		"items": [{
			"id": 11230,
			"slug": "banany-ves",
			"name": "Бананы вес",
			"storeId": 123,
			"prices": {"price": 8999, "cost": 10999}
		}]
	}`)

	products, total, err := ParseCatalogItems(body, Category{Name: "fruits", Title: "Овощи и фрукты", ID: 144})
	if err != nil {
		t.Fatalf("ParseCatalogItems returned error: %v", err)
	}
	if total != 1 {
		t.Fatalf("total = %d", total)
	}
	if len(products) != 1 {
		t.Fatalf("products = %#v", products)
	}
	product := products[0]
	if product.Category != "fruits" || product.Subcategory != "Овощи и фрукты" {
		t.Fatalf("unexpected category fields: %#v", product)
	}
	if product.Name != "Бананы вес" || product.Price != "89.99" {
		t.Fatalf("unexpected product fields: %#v", product)
	}
	if product.URL != "https://lenta.com/product/banany-ves-011230/" {
		t.Fatalf("URL = %q", product.URL)
	}
}

func TestCatalogRequestUsesCategoryLimitOffsetAndHeaders(t *testing.T) {
	var gotBody catalogRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api-gateway/v1/catalog/items" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("client") != "angular_web_0.0.2" {
			t.Fatalf("missing client header: %#v", r.Header)
		}
		if r.Header.Get("x-domain") != "moscow" || r.Header.Get("x-delivery-mode") != "pickup" {
			t.Fatalf("missing Lenta headers: %#v", r.Header)
		}
		if r.Header.Get("origin") != "https://lenta.com" || r.Header.Get("referer") == "" {
			t.Fatalf("missing browser context headers: %#v", r.Header)
		}
		if r.Header.Get("x-device-os") != "Web" || r.Header.Get("x-device-web-platform") != "desktop_web" {
			t.Fatalf("missing device headers: %#v", r.Header)
		}
		if r.Header.Get("accept-encoding") != "gzip" {
			t.Fatalf("Accept-Encoding = %q", r.Header.Get("accept-encoding"))
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		_, _ = w.Write([]byte(`{"total":0,"items":[]}`))
	}))
	defer server.Close()

	client := NewClient(server.Client(), Options{BaseURL: server.URL, PageLimit: 40})
	_, _, err := client.FetchPage(context.Background(), Category{Name: "fruits", Title: "Овощи", ID: 144}, 80)
	if err != nil {
		t.Fatalf("FetchPage returned error: %v", err)
	}
	if gotBody.CategoryID != 144 || gotBody.Limit != 40 || gotBody.Offset != 80 {
		t.Fatalf("unexpected body: %#v", gotBody)
	}
	if gotBody.Sort.Type != "popular" || gotBody.Sort.Order != "desc" {
		t.Fatalf("unexpected sort: %#v", gotBody.Sort)
	}
}

func TestFetchCategoryUsesCookieProviderWhenBootstrapLacksSessionCookies(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	providerCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/catalog/ovoshchi-frukty-144/":
			_, _ = w.Write([]byte("ok"))
		case r.Method == http.MethodPost && r.URL.Path == "/api-gateway/v1/catalog/items":
			if r.Header.Get("sessiontoken") != "provider-session" {
				t.Fatalf("sessiontoken = %q", r.Header.Get("sessiontoken"))
			}
			if r.Header.Get("x-device-id") != "provider-device" {
				t.Fatalf("x-device-id = %q", r.Header.Get("x-device-id"))
			}
			if r.Header.Get("x-user-session-id") != "provider-user-session" {
				t.Fatalf("x-user-session-id = %q", r.Header.Get("x-user-session-id"))
			}
			if _, err := r.Cookie("Utk_SessionToken"); err != nil {
				t.Fatalf("provider cookies were not added to jar: %v", err)
			}
			_, _ = w.Write([]byte(`{"total":0,"items":[]}`))
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	httpClient := server.Client()
	httpClient.Jar = jar
	client := NewClient(httpClient, Options{
		BaseURL:   server.URL,
		PageLimit: 40,
		MaxPages:  1,
		CookieProvider: func(context.Context, string) ([]*http.Cookie, error) {
			providerCalled = true
			return []*http.Cookie{
				{Name: "Utk_SessionToken", Value: "provider-session", Path: "/"},
				{Name: "Utk_DvcGuid", Value: "provider-device", Path: "/"},
				{Name: "UserSessionId", Value: "provider-user-session", Path: "/"},
			}, nil
		},
	})
	_, err = client.FetchCategory(context.Background(), Category{Name: "fruits", Title: "Овощи", ID: 144})
	if err != nil {
		t.Fatalf("FetchCategory returned error: %v", err)
	}
	if !providerCalled {
		t.Fatal("cookie provider was not called")
	}
}

func TestFetchCategoryPassesCatalogRootURLToCookieProvider(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/catalog/ovoshchi-frukty-144/":
			_, _ = w.Write([]byte("ok"))
		case r.Method == http.MethodPost && r.URL.Path == "/api-gateway/v1/catalog/items":
			_ = json.NewEncoder(w).Encode(catalogResponse{Total: 0, Items: []catalogItem{}})
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	var gotURL string
	httpClient := server.Client()
	httpClient.Jar = jar
	client := NewClient(httpClient, Options{
		BaseURL:   server.URL,
		PageLimit: 40,
		MaxPages:  1,
		CookieProvider: func(_ context.Context, pageURL string) ([]*http.Cookie, error) {
			gotURL = pageURL
			return []*http.Cookie{
				{Name: "Utk_SessionToken", Value: "provider-session", Path: "/"},
				{Name: "Utk_DvcGuid", Value: "provider-device", Path: "/"},
				{Name: "UserSessionId", Value: "provider-user-session", Path: "/"},
			}, nil
		},
	})
	_, err = client.FetchCategory(context.Background(), Category{Name: "fruits", Title: "Овощи", Slug: "ovoshchi-frukty", ID: 144})
	if err != nil {
		t.Fatalf("FetchCategory returned error: %v", err)
	}
	want := server.URL + "/catalog/"
	if gotURL != want {
		t.Fatalf("cookie provider URL = %q, want %q", gotURL, want)
	}
}

func TestFetchCategoryLogsDetailedSteps(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/catalog/ovoshchi-frukty-144/":
			_, _ = w.Write([]byte("ok"))
		case r.Method == http.MethodPost && r.URL.Path == "/api-gateway/v1/catalog/items":
			_ = json.NewEncoder(w).Encode(catalogResponse{Total: 1, Items: []catalogItem{{ID: 1, Slug: "item", Name: "Item", Prices: catalogPrices{Price: 100}}}})
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	var logs bytes.Buffer
	httpClient := server.Client()
	httpClient.Jar = jar
	client := NewClient(httpClient, Options{
		BaseURL:   server.URL,
		PageLimit: 40,
		MaxPages:  1,
		Logger:    log.New(&logs, "", 0),
		CookieProvider: func(context.Context, string) ([]*http.Cookie, error) {
			return []*http.Cookie{
				{Name: "Utk_SessionToken", Value: "provider-session", Path: "/"},
				{Name: "Utk_DvcGuid", Value: "provider-device", Path: "/"},
				{Name: "UserSessionId", Value: "provider-user-session", Path: "/"},
			}, nil
		},
	})
	_, err = client.FetchCategory(context.Background(), Category{Name: "fruits", Title: "Овощи", ID: 144})
	if err != nil {
		t.Fatalf("FetchCategory returned error: %v", err)
	}

	output := logs.String()
	for _, want := range []string{
		`category=fruits step=start id=144 title="Овощи"`,
		"category=fruits step=session_http missing=true",
		"category=fruits step=session_browser start",
		"category=fruits step=session_browser done cookies=3",
		"category=fruits step=api_page page=1 offset=0",
		"category=fruits step=api_page_done page=1 offset=0 rows=1 total=1 gzip=false",
		"category=fruits step=stop reason=total_reached rows=1 total=1",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("log missing %q in:\n%s", want, output)
		}
	}
}

func TestFetchPageDecodesGzipResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("accept-encoding") != "gzip" {
			t.Fatalf("Accept-Encoding = %q", r.Header.Get("accept-encoding"))
		}
		w.Header().Set("Content-Encoding", "gzip")
		var body bytes.Buffer
		gzipWriter := gzip.NewWriter(&body)
		_, _ = gzipWriter.Write([]byte(`{"total":1,"items":[{"id":11230,"slug":"banany-ves","name":"Бананы вес","prices":{"price":8999}}]}`))
		_ = gzipWriter.Close()
		_, _ = w.Write(body.Bytes())
	}))
	defer server.Close()

	client := NewClient(server.Client(), Options{BaseURL: server.URL})
	products, total, err := client.FetchPage(context.Background(), Category{Name: "fruits", Title: "Овощи", ID: 144}, 0)
	if err != nil {
		t.Fatalf("FetchPage returned error: %v", err)
	}
	if total != 1 || len(products) != 1 || products[0].Name != "Бананы вес" {
		t.Fatalf("unexpected response: total=%d products=%#v", total, products)
	}
}

func TestFetchCategoryPagesSequentiallyWithDelay(t *testing.T) {
	var offsets []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req catalogRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		offsets = append(offsets, req.Offset)
		id := 1000 + req.Offset
		_ = json.NewEncoder(w).Encode(catalogResponse{
			Total: 3,
			Items: []catalogItem{{ID: id, Slug: "item", Name: "Item", Prices: catalogPrices{Price: 100}}},
		})
	}))
	defer server.Close()

	delays := 0
	client := NewClient(server.Client(), Options{
		BaseURL:   server.URL,
		PageLimit: 1,
		MaxPages:  3,
		Delay: func(context.Context) error {
			delays++
			return nil
		},
	})
	products, err := client.FetchCategory(context.Background(), Category{Name: "fruits", Title: "Овощи", ID: 144})
	if err != nil {
		t.Fatalf("FetchCategory returned error: %v", err)
	}
	if len(products) != 3 {
		t.Fatalf("products = %#v", products)
	}
	if len(offsets) != 3 || offsets[0] != 0 || offsets[1] != 1 || offsets[2] != 2 {
		t.Fatalf("offsets = %#v", offsets)
	}
	if delays != 2 {
		t.Fatalf("delays = %d", delays)
	}
}

func TestFetchCategoriesRunsOneWorkerPerCategory(t *testing.T) {
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	var active int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&active, 1)
		started <- struct{}{}
		<-release
		_, _ = w.Write([]byte(`{"total":1,"items":[{"id":1,"slug":"item","name":"Item","prices":{"price":100}}]}`))
	}))
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		_, err := FetchCategories(context.Background(), []Worker{
			{Client: NewClient(server.Client(), Options{BaseURL: server.URL, PageLimit: 40, MaxPages: 1}), Category: Category{Name: "fruits", Title: "Овощи", ID: 144}},
			{Client: NewClient(server.Client(), Options{BaseURL: server.URL, PageLimit: 40, MaxPages: 1}), Category: Category{Name: "fish", Title: "Рыба", ID: 183}},
			{Client: NewClient(server.Client(), Options{BaseURL: server.URL, PageLimit: 40, MaxPages: 1}), Category: Category{Name: "dairy", Title: "Молоко", ID: 3}},
		}, 3)
		done <- err
	}()

	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			t.Fatalf("expected 3 category workers, got %d", atomic.LoadInt32(&active))
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("FetchCategories returned error: %v", err)
	}
}
