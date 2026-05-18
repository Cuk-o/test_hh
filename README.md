# Lenta Parser

Go parser for Lenta catalog API. It captures fresh Lenta browser cookies at runtime, requests catalog pages with gzip compression, and exports products to CSV.

## Requirements

- Go `1.25+`
- Google Chrome installed at `/usr/bin/google-chrome`. Cookie bootstrap runs in headless Chrome; no Python runtime is required.

## Setup

```bash
cp .env.example .env
```

`.env` can stay empty for direct mode. Add proxy settings only if needed.

## Full flow

1. CLI loads `.env` and flags.
2. Categories are selected by comma-separated Lenta IDs.
3. Workers count equals selected category count.
4. Each worker gets its own HTTP client and cookie jar.
5. Worker checks if runtime session cookies (`Utk_SessionToken`, `Utk_DvcGuid`, `UserSessionId`) already exist in the cookie jar.
6. If missing, worker launches a headless Chrome, opens `https://lenta.com/catalog/`, and waits for Lenta session cookies to appear.
7. Parser copies browser cookies into the worker cookie jar and derives headers:
   - `sessiontoken` from `Utk_SessionToken`
   - `deviceid` / `x-device-id` from `Utk_DvcGuid`
   - `x-user-session-id` from `UserSessionId`
8. Worker calls `POST https://lenta.com/api-gateway/v1/catalog/items` with:
   - browser-like headers
   - `Accept-Encoding: gzip`
   - category ID, `limit`, `offset`, sort/filter body
9. Gzip responses are decoded in Go.
10. Pagination continues until one condition is true:
    - page returns zero items
    - collected rows reach API `total`
    - `--max-pages` is positive and reached
11. Products are written to CSV.

## Categories

Known categories:

| ID | Name | Title |
| --- | --- | --- |
| 144 | fruits | Овощи и фрукты |
| 183 | fish | Рыба, икра, морепродукты |
| 3 | dairy | Молочные продукты |

## Run

Two pages per selected category:

```bash
go run ./cmd/parser --categories 144,183,3 --max-pages 2
```

Run until API end:

```bash
go run ./cmd/parser --categories 144,183,3 --max-pages 0
```

Default output is timestamped:

```text
products-YYYY-MM-DD-HH-MM-SS.csv
```

Explicit output path:

```bash
go run ./cmd/parser --categories 144,183,3 --max-pages 2 --out products.csv
```

Example summary:

```text
output=products-2026-05-16-21-58-36.csv
mode=direct
proxies=direct
categories=fruits:144,fish:183,dairy:3
page_limit=40
max_pages=2
workers=3
concurrency=3
rows=240
rows_by_category=dairy:80,fish:80,fruits:80
bad_price=0
bad_url=0
```

During the run parser prints step-by-step logs:

```text
2026/05/18 15:25:12 step=start env=.env
2026/05/18 15:25:12 step=categories selected=fruits:144,fish:183,dairy:3 workers=3
2026/05/18 15:25:12 step=config mode=direct proxies=direct page_limit=40 max_pages=2 out=products.csv
2026/05/18 15:25:12 step=fetch_start workers=3
2026/05/18 15:25:12 category=fruits step=start id=144 title="Овощи и фрукты"
2026/05/18 15:25:12 category=fruits step=session_http missing=true
2026/05/18 15:25:12 category=fruits step=session_browser start
2026/05/18 15:25:12 [browser] profile=/tmp/lenta-chrome-2351144398 port=35795 cmd=chrome
2026/05/18 15:25:12 [browser:port35795] navigating to https://lenta.com/catalog/
2026/05/18 15:25:13 [browser] page load event fired
2026/05/18 15:25:13 [browser:port35795] waiting for session cookies...
2026/05/18 15:25:13 [browser] session cookies not ready yet, checking again in 100ms...
2026/05/18 15:25:14 [browser] session cookies complete: [Utk_DvcGuid UserSessionId Utk_SessionToken ...]
2026/05/18 15:25:14 category=fruits step=session_browser done cookies=16
2026/05/18 15:25:14 category=fruits step=api_page page=1 offset=0 limit=40
2026/05/18 15:25:15 category=fruits step=api_page_done page=1 offset=0 rows=40 total=673 gzip=true
2026/05/18 15:25:15 category=fruits step=delay
2026/05/18 15:25:16 category=fruits step=api_page page=2 offset=40 limit=40
2026/05/18 15:25:16 category=fruits step=api_page_done page=2 offset=40 rows=40 total=673 gzip=true
2026/05/18 15:25:16 category=fruits step=stop reason=max_pages rows=80 total=673 max_pages=2
2026/05/18 15:25:16 step=fetch_done rows=240
2026/05/18 15:25:16 step=csv_write path=products.csv rows=240
2026/05/18 15:25:16 step=csv_done path=products.csv
output=products-2026-05-18-15-25-12.csv
mode=direct
proxies=direct
categories=fruits:144,fish:183,dairy:3
page_limit=40
max_pages=2
workers=3
concurrency=3
rows=240
rows_by_category=dairy:80,fruits:80,fish:80
bad_price=0
bad_url=0
```

## Flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `--env` | `.env` | env file path |
| `--categories` | (required) | comma-separated category IDs; unknown IDs get placeholder names |
| `--max-pages` | `.env` / `0` | pages per category; `0` means until end |
| `--limit-products-per-subcategory` | `0` | cap rows per category after fetching; `0` means unlimited |
| `--out` | timestamped CSV | output CSV path |

## Env

Main vars:

| Var | Meaning |
| --- | --- |
| `LENTA_PROXY` | one proxy URL |
| `LENTA_PROXY_LIST` | comma-separated proxy URLs |
| `LENTA_PAGE_LIMIT` | API page size, default `40` |
| `LENTA_MAX_PAGES` | env default for max pages, default `0` |
| `LENTA_DELAY_MIN_MS` | minimum delay between pages |
| `LENTA_DELAY_MAX_MS` | maximum delay between pages |
| `LENTA_SCAN_CONCURRENCY` | max concurrent category scans, default `3` |

If `LENTA_PROXY` and `LENTA_PROXY_LIST` are empty, parser uses direct mode.
Proxy URLs are passed to both the API HTTP client and headless Chrome via `--proxy-server`.

Legacy fallback proxy vars are still supported for compatibility:

```text
OKEY_PROXY
OKEY_PROXY_LIST
OKEY_PROXY_HOST
OKEY_PROXY_PORT
OKEY_PROXY_PORT_START
OKEY_PROXY_PORT_COUNT
OKEY_PROXY_PORT_SKIP
OKEY_PROXY_LOGIN
OKEY_PROXY_PASSWORD
```

## CSV columns

```text
category,subcategory,name,price,url
```

## Compression

Parser sends:

```http
Accept-Encoding: gzip
```

Lenta responds with gzip for catalog API, and parser decodes it before parsing JSON. This reduces traffic through proxies.

## Notes

- Do not put Lenta cookies in `.env`.
- Generated CSV files are intentionally not part of source cleanup.
- Unknown category ID fails fast with a clear error.
- Duplicate category ID fails fast.
