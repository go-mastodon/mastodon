# mastodon

[![CI](https://github.com/go-mastodon/mastodon/actions/workflows/ci.yml/badge.svg)](https://github.com/go-mastodon/mastodon/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-mastodon/mastodon.svg)](https://pkg.go.dev/github.com/go-mastodon/mastodon)
[![License: BSD-3-Clause](https://img.shields.io/badge/License-BSD--3--Clause-blue.svg)](LICENSE)

A pure-Go, dependency-free **read** client for the [Mastodon](https://docs.joinmastodon.org/api/) REST API.

- **CGO_ENABLED=0** — no C, static binaries everywhere.
- **Zero third-party dependencies** — standard library only.
- Reads public, hashtag and per-account timelines, with `Link`-header pagination.
- 100% test coverage, network-free tests via `net/http/httptest`.

## Install

```sh
go get github.com/go-mastodon/mastodon
```

Requires Go 1.26.4 or newer.

## Usage

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/go-mastodon/mastodon"
)

func main() {
	c := mastodon.New("https://mastodon.social",
		mastodon.WithUserAgent("myapp/1.0"),
		// mastodon.WithToken("…"), // optional bearer token
	)

	tl, err := c.PublicTimeline(context.Background(), mastodon.TimelineOptions{
		Limit:     20,
		OnlyMedia: true,
		Local:     true,
	})
	if err != nil {
		log.Fatal(err)
	}

	for _, s := range tl.Statuses {
		fmt.Printf("@%s: %s\n", s.Account.Acct, s.URL)
	}

	// Fetch the next page using the pagination cursor.
	if tl.MaxID != "" {
		next, err := c.PublicTimeline(context.Background(), mastodon.TimelineOptions{MaxID: tl.MaxID})
		if err != nil {
			log.Fatal(err)
		}
		_ = next
	}
}
```

### Other timelines

```go
// A hashtag timeline.
tl, err := c.HashtagTimeline(ctx, "golang", mastodon.TimelineOptions{Limit: 10})

// An account's statuses (resolves the acct to an account ID first).
tl, err := c.AccountStatuses(ctx, "Gargron@mastodon.social", mastodon.TimelineOptions{})
```

## API

| Method | Endpoint |
| --- | --- |
| `PublicTimeline` | `GET /api/v1/timelines/public` |
| `HashtagTimeline` | `GET /api/v1/timelines/tag/:hashtag` |
| `AccountStatuses` | `GET /api/v1/accounts/lookup` then `GET /api/v1/accounts/:id/statuses` |

All methods take a `context.Context` and `TimelineOptions` (`Limit`, `MaxID`, `OnlyMedia`, `Local`) and return a `*Timeline` whose `MaxID` field carries the `rel="next"` pagination cursor parsed from the `Link` response header.

## License

BSD-3-Clause. See [LICENSE](LICENSE).
