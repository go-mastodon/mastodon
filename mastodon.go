// Package mastodon is a dependency-free read client for the Mastodon REST API.
//
// It uses only the Go standard library (CGO_ENABLED=0) and exposes a small
// surface for reading public, hashtag and per-account timelines.
package mastodon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Client is a read-only Mastodon REST API client.
type Client struct {
	// Instance is the base URL of the Mastodon instance,
	// e.g. https://mastodon.social.
	Instance string
	// Token is an optional bearer token. When set, requests are
	// authenticated with an Authorization header.
	Token string
	// HTTPClient is the underlying HTTP client. When nil, http.DefaultClient
	// is used.
	HTTPClient *http.Client
	// UserAgent is the value sent in the User-Agent header.
	UserAgent string
}

// Option configures a Client.
type Option func(*Client)

// WithToken sets the bearer token used to authenticate requests.
func WithToken(t string) Option {
	return func(c *Client) { c.Token = t }
}

// WithHTTPClient sets the HTTP client used to perform requests.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.HTTPClient = h }
}

// WithUserAgent sets the User-Agent header value.
func WithUserAgent(ua string) Option {
	return func(c *Client) { c.UserAgent = ua }
}

// New creates a Client for the given instance base URL.
func New(instance string, opts ...Option) *Client {
	c := &Client{
		Instance:  strings.TrimRight(instance, "/"),
		UserAgent: "go-mastodon/mastodon",
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Account is a Mastodon account.
type Account struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Acct        string `json:"acct"`
	DisplayName string `json:"display_name"`
	URL         string `json:"url"`
	Avatar      string `json:"avatar"`
}

// Media is a media attachment on a status.
type Media struct {
	Type        string `json:"type"` // image, video, gifv, audio
	URL         string `json:"url"`
	PreviewURL  string `json:"preview_url"`
	Description string `json:"description"`
}

// Tag is a hashtag referenced by a status.
type Tag struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Status is a Mastodon status (toot).
type Status struct {
	ID          string    `json:"id"`
	URL         string    `json:"url"`
	Content     string    `json:"content"` // HTML
	CreatedAt   time.Time `json:"created_at"`
	Account     Account   `json:"account"`
	Favourites  int       `json:"favourites_count"`
	Reblogs     int       `json:"reblogs_count"`
	Replies     int       `json:"replies_count"`
	Sensitive   bool      `json:"sensitive"`
	SpoilerText string    `json:"spoiler_text"`
	Media       []Media   `json:"media_attachments"`
	Tags        []Tag     `json:"tags"`
}

// Timeline is a page of statuses plus the pagination cursor.
type Timeline struct {
	Statuses []Status
	// MaxID is extracted from the Link header rel="next" and can be passed
	// as TimelineOptions.MaxID to fetch the following page. It is empty when
	// there is no next page.
	MaxID string
}

// TimelineOptions holds the query parameters shared by the timeline methods.
type TimelineOptions struct {
	Limit     int
	MaxID     string
	OnlyMedia bool
	Local     bool
}

// values converts the options into a url.Values query set.
func (o TimelineOptions) values() url.Values {
	v := url.Values{}
	if o.Limit > 0 {
		v.Set("limit", strconv.Itoa(o.Limit))
	}
	if o.MaxID != "" {
		v.Set("max_id", o.MaxID)
	}
	if o.OnlyMedia {
		v.Set("only_media", "true")
	}
	if o.Local {
		v.Set("local", "true")
	}
	return v
}

// PublicTimeline fetches the public timeline
// (GET /api/v1/timelines/public).
func (c *Client) PublicTimeline(ctx context.Context, opts TimelineOptions) (*Timeline, error) {
	return c.timeline(ctx, "/api/v1/timelines/public", opts)
}

// HashtagTimeline fetches the timeline for a hashtag
// (GET /api/v1/timelines/tag/:hashtag).
func (c *Client) HashtagTimeline(ctx context.Context, tag string, opts TimelineOptions) (*Timeline, error) {
	return c.timeline(ctx, "/api/v1/timelines/tag/"+url.PathEscape(tag), opts)
}

// AccountStatuses resolves acct via GET /api/v1/accounts/lookup and then
// fetches that account's statuses (GET /api/v1/accounts/:id/statuses).
func (c *Client) AccountStatuses(ctx context.Context, acct string, opts TimelineOptions) (*Timeline, error) {
	q := url.Values{}
	q.Set("acct", acct)
	var acc Account
	if err := c.getJSON(ctx, "/api/v1/accounts/lookup", q, &acc); err != nil {
		return nil, err
	}
	return c.timeline(ctx, "/api/v1/accounts/"+url.PathEscape(acc.ID)+"/statuses", opts)
}

// HomeTimeline fetches the authenticated user's home timeline — the statuses
// from the accounts they follow (GET /api/v1/timelines/home). A bearer token is
// required; without one the instance returns 401.
func (c *Client) HomeTimeline(ctx context.Context, opts TimelineOptions) (*Timeline, error) {
	return c.timeline(ctx, "/api/v1/timelines/home", opts)
}

// VerifyCredentials fetches the account associated with the configured bearer
// token (GET /api/v1/accounts/verify_credentials), returning at least its ID —
// the handle a caller needs to then page the account's own [Client.Following]
// list. A bearer token is required; without one the instance returns 401.
func (c *Client) VerifyCredentials(ctx context.Context) (*Account, error) {
	var acc Account
	if err := c.getJSON(ctx, "/api/v1/accounts/verify_credentials", nil, &acc); err != nil {
		return nil, err
	}
	return &acc, nil
}

// FollowingPage is one page of the accounts a target follows, plus the
// pagination cursor. MaxID is parsed from the Link header's rel="next" URL and
// passed back via [TimelineOptions.MaxID] to fetch the following page; it is
// empty when the list is exhausted. Mastodon keys following pagination on an
// internal relationship id it exposes only through the Link header, so the
// cursor is opaque and must be round-tripped rather than derived from an account
// id.
type FollowingPage struct {
	Accounts []Account
	MaxID    string
}

// Following fetches one page of the accounts that accountID follows
// (GET /api/v1/accounts/:id/following). Feed the returned [FollowingPage.MaxID]
// back through [TimelineOptions.MaxID] to page the rest. A bearer token
// authenticates the request (required when the target hides their follows); a
// public follows list is readable anonymously.
func (c *Client) Following(ctx context.Context, accountID string, opts TimelineOptions) (*FollowingPage, error) {
	var accts []Account
	resp, err := c.do(ctx, "/api/v1/accounts/"+url.PathEscape(accountID)+"/following", opts.values(), &accts)
	if err != nil {
		return nil, err
	}
	return &FollowingPage{Accounts: accts, MaxID: nextMaxID(resp.Header.Get("Link"))}, nil
}

// timeline performs a request that returns a list of statuses and parses the
// pagination cursor from the Link header.
func (c *Client) timeline(ctx context.Context, path string, opts TimelineOptions) (*Timeline, error) {
	var statuses []Status
	resp, err := c.do(ctx, path, opts.values(), &statuses)
	if err != nil {
		return nil, err
	}
	return &Timeline{
		Statuses: statuses,
		MaxID:    nextMaxID(resp.Header.Get("Link")),
	}, nil
}

// getJSON performs a request and decodes the JSON body into out, discarding
// the response headers.
func (c *Client) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	_, err := c.do(ctx, path, q, out)
	return err
}

// do performs an authenticated GET request against the instance and decodes
// the JSON body into out. It returns the response so callers can inspect
// headers. On non-2xx responses it returns an error including a body snippet.
func (c *Client) do(ctx context.Context, path string, q url.Values, out any) (*http.Response, error) {
	u := c.Instance + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	hc := c.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mastodon: %s %s: unexpected status %d: %s",
			req.Method, path, resp.StatusCode, snippet(body))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return nil, fmt.Errorf("mastodon: decode %s: %w", path, err)
	}
	return resp, nil
}

// snippet returns a short, single-line excerpt of a response body for error
// messages.
func snippet(b []byte) string {
	const max = 200
	s := strings.TrimSpace(string(b))
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// linkNextRe matches a rel="next" URL in a Link response header.
var linkNextRe = regexp.MustCompile(`<([^>]+)>\s*;\s*rel="next"`)

// nextMaxID extracts the max_id query parameter from the rel="next" URL of a
// Link header. It returns an empty string when there is no next link or no
// max_id in it.
func nextMaxID(link string) string {
	m := linkNextRe.FindStringSubmatch(link)
	if m == nil {
		return ""
	}
	u, err := url.Parse(m[1])
	if err != nil {
		return ""
	}
	return u.Query().Get("max_id")
}
