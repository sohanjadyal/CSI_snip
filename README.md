# snip

A link shortener written in Go, using only the standard library. Links are persisted to `links.json` and restored when the server restarts.

## Running it

You need Go 1.22 or newer.

```
go run .        # http://localhost:8080
go test ./...
```

Set `STORE_PATH` to use a different file than `links.json`.

## API

| Request | Does |
|---|---|
| `POST /api/links` | Creates a link. Body: `{"url": "...", "alias": "optional", "expires_in": seconds}` |
| `GET /{code}` | Redirects to the original URL |
| `GET /api/links/{code}` | Stats for a link |
| `DELETE /api/links/{code}` | Deletes a link. Needs the `X-Delete-Token` header |
| `GET /api/links?limit=N` | Newest links first, 20 by default |

```
curl -X POST localhost:8080/api/links -d '{"url": "https://go.dev", "alias": "godev"}'
curl -i localhost:8080/godev
```

## How it's supposed to work

- Only `http://` and `https://` URLs are accepted. Anything else is a `400`.
- A URL typed without a scheme, like `google.com`, gets `https://` added.
- Custom aliases are 3 to 20 letters, digits, `-` or `_`. A taken alias is a `409`.
- Reserved names (`api`, `static`, `health`, `robots`, `favicon`) cannot be used as custom aliases and return a `400`.
- A code that doesn't exist is a `404`.
- A link with an expiry works until it expires, then returns `410`.
- Each visit to `GET /{code}` counts as one click.
- Deleting needs the right token. A missing or wrong token is a `403`.
- It's safe to use from many requests at once: `go test -race ./...` reports no races.

## Code

- `main.go`: starts the server
- `handlers.go`: request handlers
- `store.go`: persistent storage with atomic writes
- `validate.go`: URL and alias checks
- `static/index.html`: the web page

## Contributing

Fork the repo, make your changes on a new branch, and open a pull request. Run `go vet ./...` and `go test ./...` first.

If you find a bug, open an issue with the steps to reproduce it, what you expected, and what happened instead.

Part of Source Start by CSI SPIT. MIT licensed.
