// Package slack provides a Slack Web API client that authenticates with
// browser-extracted credentials (xoxc token + xoxd cookie) rather than a bot
// token. It wraps github.com/slack-go/slack and injects the required xoxd
// cookie on every request via a custom http.Client.
package slack

import (
	"fmt"
	"io"
	"net/http"
	"time"

	utls "github.com/refraction-networking/utls"
	"github.com/rusq/chttp/v2"
	"github.com/rusq/chttp/v2/transport"
	slackgo "github.com/slack-go/slack"
)

// chromeUA matches the Chromium version bundled with playwright-go v0.5700.1
// (Chrome/143) so the TLS fingerprint and User-Agent are consistent across
// the login browser session and subsequent API calls.
const chromeUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.7499.4 Safari/537.36"

// Client calls the Slack Web API using browser session credentials.
type Client struct {
	api        *slackgo.Client
	token      string       // xoxc token, retained for edge API calls
	cookie     string       // xoxd cookie value, retained for WebSocket dial
	httpClient *http.Client // shared transport used for both slack-go and edge API
}

// NewClient creates a client for the given xoxc token and xoxd cookie value.
// The client uses a uTLS transport that emulates Chrome's TLS ClientHello and
// HTTP/2 framing to avoid session revocation from TLS/HTTP fingerprinting.
// opts are passed through to slack.New and are intended for testing
// (e.g. slack.OptionAPIURL to redirect to a test server).
func NewClient(token, cookie string, opts ...slackgo.Option) *Client {
	cookies := []*http.Cookie{{
		Name:   "d",
		Value:  cookie,
		Domain: ".slack.com",
		Path:   "/",
	}}

	httpClient, err := chttp.New(
		"https://slack.com",
		cookies,
		chttp.WithUTLS(&utls.Config{}),
		chttp.WithUserAgent(chromeUA),
	)
	if err != nil {
		// Fallback: uTLS setup failed (unsupported platform). Use a plain
		// cookieTransport so cookie injection still works.
		httpClient = newPlainCookieClient(cookie)
	}

	// Wrap the transport to inject Chrome's sec-ch-ua / sec-fetch-* headers on
	// every request. These are sent by real Chrome but omitted by Go's http
	// package; their absence is a signal for bot-detection heuristics.
	httpClient.Transport = newChromeHeaderTransport(httpClient.Transport)
	httpClient.Timeout = 15 * time.Second

	allOpts := append([]slackgo.Option{slackgo.OptionHTTPClient(httpClient)}, opts...)
	return &Client{
		api:        slackgo.New(token, allOpts...),
		token:      token,
		cookie:     cookie,
		httpClient: httpClient,
	}
}

// NewClientWithHTTP creates a Client using a caller-supplied http.Client.
// Intended for tests that need to inject an httptest TLS client.
func NewClientWithHTTP(token, cookie string, httpClient *http.Client) *Client {
	allOpts := []slackgo.Option{slackgo.OptionHTTPClient(httpClient)}
	return &Client{
		api:        slackgo.New(token, allOpts...),
		token:      token,
		cookie:     cookie,
		httpClient: httpClient,
	}
}

// newChromeHeaderTransport wraps rt and injects browser-specific request
// headers that Chrome sends on every XHR/fetch request to the Slack API.
func newChromeHeaderTransport(rt http.RoundTripper) http.RoundTripper {
	ft := transport.NewFuncTransport(rt)
	ft.BeforeReq = func(req *http.Request) {
		req.Header.Set("sec-ch-ua", `"Chromium";v="143", "Not_A Brand";v="24"`)
		req.Header.Set("sec-ch-ua-mobile", "?0")
		req.Header.Set("sec-ch-ua-platform", `"macOS"`)
		req.Header.Set("sec-fetch-dest", "empty")
		req.Header.Set("sec-fetch-mode", "cors")
		req.Header.Set("sec-fetch-site", "same-origin")
		req.Header.Set("Origin", "https://app.slack.com")
	}
	return ft
}

// cookieTransport wraps an http.RoundTripper and injects the xoxd session
// cookie on every outbound request.
type cookieTransport struct {
	base   http.RoundTripper
	cookie string // bare xoxd value, e.g. "xoxd-abc123"
}

func (t *cookieTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("Cookie", "d="+t.cookie)
	return t.base.RoundTrip(r)
}

// newPlainCookieClient returns a plain http.Client with cookie injection via
// cookieTransport. Used as a fallback when uTLS initialisation fails.
func newPlainCookieClient(cookie string) *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &cookieTransport{
			base:   &http.Transport{},
			cookie: cookie,
		},
	}
}

// slackAPI exposes the underlying slack-go client for methods not wrapped here.
// Internal use only.
func (c *Client) slackAPI() *slackgo.Client {
	return c.api
}

// slackError converts a slack-go API error into a well-formed Go error.
// slack-go returns errors as plain strings from SlackErrorResponse; this
// wraps them so callers always get a non-nil error on failure.
func slackError(method, code string) error {
	return fmt.Errorf("%s: %s", method, code)
}

// FetchFileBytes downloads a Slack private file URL using the authenticated
// httpClient (cookie injected). Returns the raw bytes and the Content-Type
// header value. Callers should check the content-type before rendering.
func (c *Client) FetchFileBytes(url string) ([]byte, string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("building file request: %w", err)
	}
	// Slack requires the xoxc token as an Authorization header for url_private.
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetching file: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("fetching file: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("reading file body: %w", err)
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// GetFileInfo returns the File metadata for the given file ID, including
// URLPrivate which is needed to download the file content.
func (c *Client) GetFileInfo(fileID string) (File, error) {
	f, _, _, err := c.api.GetFileInfo(fileID, 0, 0)
	if err != nil {
		return File{}, fmt.Errorf("files.info %s: %w", fileID, err)
	}
	return File{
		ID:         f.ID,
		Name:       f.Name,
		Title:      f.Title,
		PrettyType: f.PrettyType,
		Mimetype:   f.Mimetype,
		Permalink:  f.Permalink,
		URLPrivate: f.URLPrivate,
	}, nil
}
