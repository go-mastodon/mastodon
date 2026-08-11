package mastodon

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// rtFunc is a RoundTripper backed by a function.
type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// errReadCloser is a body whose Read always fails.
type errReadCloser struct{}

func (errReadCloser) Read([]byte) (int, error) { return 0, errors.New("read boom") }
func (errReadCloser) Close() error             { return nil }

const statusJSON = `[{
  "id":"1",
  "url":"https://mastodon.social/@a/1",
  "content":"<p>hello</p>",
  "created_at":"2026-07-10T12:00:00.000Z",
  "account":{"id":"42","username":"a","acct":"a","display_name":"A","url":"https://mastodon.social/@a","avatar":"https://img/a.png"},
  "favourites_count":3,
  "reblogs_count":2,
  "replies_count":1,
  "sensitive":true,
  "spoiler_text":"cw",
  "media_attachments":[{"type":"image","url":"https://img/1.png","preview_url":"https://img/1p.png","description":"pic"}],
  "tags":[{"name":"go","url":"https://mastodon.social/tags/go"}]
}]`

func TestNewAndOptions(t *testing.T) {
	hc := &http.Client{}
	c := New("https://example.social/",
		WithToken("tok"),
		WithHTTPClient(hc),
		WithUserAgent("ua/1"),
	)
	if c.Instance != "https://example.social" {
		t.Errorf("trailing slash not trimmed: %q", c.Instance)
	}
	if c.Token != "tok" || c.UserAgent != "ua/1" || c.HTTPClient != hc {
		t.Errorf("options not applied: %+v", c)
	}

	def := New("https://x")
	if def.UserAgent != "go-mastodon/mastodon" {
		t.Errorf("default user agent = %q", def.UserAgent)
	}
}

func TestTimelineOptionsValues(t *testing.T) {
	got := TimelineOptions{Limit: 5, MaxID: "9", OnlyMedia: true, Local: true}.values()
	want := url.Values{
		"limit":      {"5"},
		"max_id":     {"9"},
		"only_media": {"true"},
		"local":      {"true"},
	}
	if got.Encode() != want.Encode() {
		t.Errorf("values = %v, want %v", got, want)
	}
	if empty := (TimelineOptions{}).values(); len(empty) != 0 {
		t.Errorf("empty options produced %v", empty)
	}
}

func TestPublicTimeline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/timelines/public" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("auth = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != "go-mastodon/mastodon" {
			t.Errorf("ua = %q", got)
		}
		if got := r.URL.Query().Get("only_media"); got != "true" {
			t.Errorf("only_media = %q", got)
		}
		w.Header().Set("Link", `<https://x/api/v1/timelines/public?max_id=7>; rel="next", <https://x/?since_id=1>; rel="prev"`)
		w.Write([]byte(statusJSON))
	}))
	defer srv.Close()

	c := New(srv.URL, WithToken("tok"), WithHTTPClient(srv.Client()))
	tl, err := c.PublicTimeline(context.Background(), TimelineOptions{OnlyMedia: true})
	if err != nil {
		t.Fatal(err)
	}
	if tl.MaxID != "7" {
		t.Errorf("MaxID = %q", tl.MaxID)
	}
	if len(tl.Statuses) != 1 {
		t.Fatalf("statuses = %d", len(tl.Statuses))
	}
	s := tl.Statuses[0]
	if s.ID != "1" || s.Account.Username != "a" || s.Favourites != 3 ||
		!s.Sensitive || s.SpoilerText != "cw" ||
		len(s.Media) != 1 || s.Media[0].Type != "image" ||
		len(s.Tags) != 1 || s.Tags[0].Name != "go" {
		t.Errorf("status decoded wrong: %+v", s)
	}
	if s.CreatedAt.Year() != 2026 {
		t.Errorf("created_at = %v", s.CreatedAt)
	}
}

func TestHashtagTimelineNoNextLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/timelines/tag/go" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	// No token -> no Authorization header, nil HTTPClient -> default client.
	c := New(srv.URL)
	tl, err := c.HashtagTimeline(context.Background(), "go", TimelineOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if tl.MaxID != "" {
		t.Errorf("MaxID = %q, want empty", tl.MaxID)
	}
	if len(tl.Statuses) != 0 {
		t.Errorf("statuses = %d", len(tl.Statuses))
	}
}

func TestAccountStatuses(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/accounts/lookup", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("acct"); got != "a@host" {
			t.Errorf("acct = %q", got)
		}
		w.Write([]byte(`{"id":"42","username":"a","acct":"a@host"}`))
	})
	mux.HandleFunc("/api/v1/accounts/42/statuses", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(statusJSON))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, WithHTTPClient(srv.Client()))
	tl, err := c.AccountStatuses(context.Background(), "a@host", TimelineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Statuses) != 1 || tl.Statuses[0].ID != "1" {
		t.Errorf("statuses = %+v", tl.Statuses)
	}
}

func TestAccountStatusesLookupError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"Record not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(srv.URL, WithHTTPClient(srv.Client()))
	_, err := c.AccountStatuses(context.Background(), "missing", TimelineOptions{})
	if err == nil || !strings.Contains(err.Error(), "unexpected status 404") {
		t.Errorf("err = %v", err)
	}
}

func TestNon2xxError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom body", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, WithHTTPClient(srv.Client()))
	_, err := c.PublicTimeline(context.Background(), TimelineOptions{})
	if err == nil || !strings.Contains(err.Error(), "unexpected status 500") ||
		!strings.Contains(err.Error(), "boom body") {
		t.Errorf("err = %v", err)
	}
}

func TestBadJSONError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c := New(srv.URL, WithHTTPClient(srv.Client()))
	_, err := c.PublicTimeline(context.Background(), TimelineOptions{})
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Errorf("err = %v", err)
	}
}

func TestNewRequestError(t *testing.T) {
	// A control character in the instance makes url.Parse (inside
	// http.NewRequestWithContext) fail.
	c := New("http://\x7f")
	_, err := c.PublicTimeline(context.Background(), TimelineOptions{})
	if err == nil {
		t.Fatal("expected request-construction error")
	}
}

func TestTransportError(t *testing.T) {
	c := New("https://example.social", WithHTTPClient(&http.Client{
		Transport: rtFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("transport down")
		}),
	}))
	_, err := c.PublicTimeline(context.Background(), TimelineOptions{})
	if err == nil || !strings.Contains(err.Error(), "transport down") {
		t.Errorf("err = %v", err)
	}
}

func TestBodyReadError(t *testing.T) {
	c := New("https://example.social", WithHTTPClient(&http.Client{
		Transport: rtFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Header:     make(http.Header),
				Body:       errReadCloser{},
			}, nil
		}),
	}))
	_, err := c.PublicTimeline(context.Background(), TimelineOptions{})
	if err == nil || !strings.Contains(err.Error(), "read boom") {
		t.Errorf("err = %v", err)
	}
}

func TestHomeTimeline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/timelines/home" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("auth = %q, want Bearer tok", got)
		}
		w.Header().Set("Link", `<https://x/api/v1/timelines/home?max_id=7>; rel="next"`)
		w.Write([]byte(statusJSON))
	}))
	defer srv.Close()

	c := New(srv.URL, WithToken("tok"), WithHTTPClient(srv.Client()))
	tl, err := c.HomeTimeline(context.Background(), TimelineOptions{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if tl.MaxID != "7" {
		t.Errorf("MaxID = %q", tl.MaxID)
	}
	if len(tl.Statuses) != 1 || tl.Statuses[0].ID != "1" {
		t.Errorf("statuses = %+v", tl.Statuses)
	}
}

func TestVerifyCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/accounts/verify_credentials" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("auth = %q, want Bearer tok", got)
		}
		w.Write([]byte(`{"id":"42","username":"me","acct":"me","display_name":"Me"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, WithToken("tok"), WithHTTPClient(srv.Client()))
	acc, err := c.VerifyCredentials(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if acc.ID != "42" || acc.Username != "me" {
		t.Errorf("account = %+v", acc)
	}
}

func TestVerifyCredentialsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(srv.URL, WithHTTPClient(srv.Client()))
	_, err := c.VerifyCredentials(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unexpected status 401") {
		t.Errorf("err = %v", err)
	}
}

func TestFollowing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/accounts/42/following" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("max_id"); got != "5" {
			t.Errorf("max_id = %q", got)
		}
		w.Header().Set("Link", `<https://x/api/v1/accounts/42/following?max_id=99>; rel="next"`)
		w.Write([]byte(`[{"id":"7","username":"a","acct":"a@host","display_name":"A"},{"id":"8","username":"b","acct":"b"}]`))
	}))
	defer srv.Close()

	c := New(srv.URL, WithToken("tok"), WithHTTPClient(srv.Client()))
	pg, err := c.Following(context.Background(), "42", TimelineOptions{MaxID: "5"})
	if err != nil {
		t.Fatal(err)
	}
	if pg.MaxID != "99" {
		t.Errorf("MaxID = %q", pg.MaxID)
	}
	if len(pg.Accounts) != 2 || pg.Accounts[0].Acct != "a@host" || pg.Accounts[1].Acct != "b" {
		t.Errorf("accounts = %+v", pg.Accounts)
	}
}

func TestFollowingError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, WithHTTPClient(srv.Client()))
	_, err := c.Following(context.Background(), "42", TimelineOptions{})
	if err == nil || !strings.Contains(err.Error(), "unexpected status 500") {
		t.Errorf("err = %v", err)
	}
}

func TestNextMaxID(t *testing.T) {
	cases := []struct {
		link string
		want string
	}{
		{"", ""},
		{`<https://x/api?max_id=100>; rel="next"`, "100"},
		{`<https://x/api?since_id=1>; rel="prev"`, ""}, // no next
		{`<https://x/api>; rel="next"`, ""},            // next but no max_id
		{"<http://x/\x7f>; rel=\"next\"", ""},          // matches regexp, url.Parse fails
	}
	for _, tc := range cases {
		if got := nextMaxID(tc.link); got != tc.want {
			t.Errorf("nextMaxID(%q) = %q, want %q", tc.link, got, tc.want)
		}
	}
}

func TestSnippetTruncation(t *testing.T) {
	long := strings.Repeat("x", 500)
	s := snippet([]byte("  " + long + "\n"))
	if !strings.HasSuffix(s, "…") || len(s) > 210 {
		t.Errorf("snippet not truncated: len=%d", len(s))
	}
	if got := snippet([]byte("short")); got != "short" {
		t.Errorf("snippet(short) = %q", got)
	}
	// ensure io is referenced for the errReadCloser interface satisfaction check
	var _ io.ReadCloser = errReadCloser{}
}
