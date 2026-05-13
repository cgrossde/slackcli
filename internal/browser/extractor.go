// Package browser extracts Slack session credentials (xoxc token + xoxd cookie)
// from a live browser session using Playwright. No Slack app required.
package browser

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/playwright-community/playwright-go"
)

const (
	slackDomain    = ".slack.com"
	defaultTimeout = 5 * time.Minute
)

// tokenRE matches a valid Slack client (xoxc) token.
var tokenRE = regexp.MustCompile(`xoxc-[0-9]+-[0-9]+-[0-9]+-[0-9a-z]{64}`)

// ErrBrowserClosed is returned when the user closes the browser before
// authentication completes.
var ErrBrowserClosed = errors.New("browser was closed before authentication completed")

// Credentials holds the extracted Slack session credentials.
type Credentials struct {
	// Token is the xoxc- client token, extracted from API request bodies.
	Token string
	// Cookie is the value of the "d" session cookie (xoxd-...).
	Cookie string
	// Workspace is the canonical workspace domain (e.g. "myorg.slack.com").
	Workspace string
}

// Options configures the extraction behaviour.
type Options struct {
	// Timeout is the maximum time to wait for the user to log in.
	// Defaults to 5 minutes.
	Timeout time.Duration
	// Browser selects chromium or firefox. Defaults to chromium.
	Browser BrowserType
}

// BrowserType selects the Playwright browser engine.
type BrowserType int

const (
	Chromium BrowserType = iota
	Firefox
)

// Extract opens a visible browser window, navigates to the given Slack
// workspace URL (e.g. "myorg" or "myorg.slack.com"), waits for the user to
// authenticate, and returns the extracted credentials.
//
// The caller must cancel ctx or wait for timeout to abort the operation.
func Extract(ctx context.Context, workspace string, opts Options) (Credentials, error) {
	if opts.Timeout == 0 {
		opts.Timeout = defaultTimeout
	}

	workspaceURL, err := normalizeWorkspaceURL(workspace)
	if err != nil {
		return Credentials{}, fmt.Errorf("invalid workspace: %w", err)
	}

	// Install browser binaries on first run (idempotent). Run in a goroutine
	// so Ctrl+C (ctx cancellation) can interrupt the download wait.
	installErr := make(chan error, 1)
	go func() {
		installErr <- playwright.Install(&playwright.RunOptions{
			Browsers: []string{browserName(opts.Browser)},
			Verbose:  false,
		})
	}()
	select {
	case <-ctx.Done():
		return Credentials{}, ctx.Err()
	case err := <-installErr:
		if err != nil {
			return Credentials{}, fmt.Errorf("installing browser: %w", err)
		}
	}

	pw, err := playwright.Run()
	if err != nil {
		return Credentials{}, fmt.Errorf("starting playwright: %w", err)
	}
	defer func() { _ = pw.Stop() }()

	bt := selectBrowserType(pw, opts.Browser)

	// Use a fixed user data directory so the browser session (cookies,
	// localStorage, cached login state) persists across runs. The user only
	// needs to log in once; subsequent runs reuse the saved session.
	userDataDir := "/tmp/slackcli-browser-profile"

	bctx, err := bt.LaunchPersistentContext(userDataDir, playwright.BrowserTypeLaunchPersistentContextOptions{
		Headless: playwright.Bool(false),
	})
	if err != nil {
		return Credentials{}, fmt.Errorf("launching browser: %w", err)
	}
	defer bctx.Close()

	// Suppress the GDPR cookie consent nag screen.
	nowStr := time.Now().Add(-10 * time.Minute).Format(time.RFC3339)
	_ = bctx.AddCookies([]playwright.OptionalCookie{
		{
			Domain:  playwright.String(slackDomain),
			Path:    playwright.String("/"),
			Name:    "OptanonAlertBoxClosed",
			Value:   nowStr,
			Expires: playwright.Float(float64(time.Now().AddDate(0, 0, 30).Unix())),
		},
	})

	page, err := bctx.NewPage()
	if err != nil {
		return Credentials{}, fmt.Errorf("creating page: %w", err)
	}

	// tokenCh receives the first valid xoxc token.
	tokenCh := make(chan string, 1)

	send := func(tok string) {
		if tok == "" {
			return
		}
		select {
		case tokenCh <- tok:
		default:
		}
	}

	// Context-level request listener fires across ALL pages and ALL redirects —
	// including the MS SSO pages and the final return to app.slack.com.
	bctx.OnRequest(func(req playwright.Request) {
		send(extractTokenFromRequest(req))
	})

	// Response listener: some Slack endpoints echo the token back in the
	// JSON response body (e.g. rtm.connect). Scan responses as a secondary source.
	bctx.OnResponse(func(resp playwright.Response) {
		send(extractTokenFromResponse(resp))
	})

	// page.On("close") detects the user closing the browser window.
	pageClosed := make(chan struct{})
	var closeOnce struct{ done bool }
	page.On("close", func() {
		if !closeOnce.done {
			closeOnce.done = true
			close(pageClosed)
		}
	})

	// framenavigated fires whenever any frame in the page navigates, including
	// the top-level frame. We use this to read localStorage on each new URL —
	// critically, this catches the final navigation to app.slack.com/client/...
	// after SSO, which is where Slack actually writes the xoxc token.
	page.On("framenavigated", func(frame playwright.Frame) {
		// Only care about the main (top-level) frame.
		if frame.ParentFrame() != nil {
			return
		}
		u := frame.URL()
		if !strings.Contains(u, "slack.com") {
			return
		}
		// Give the Slack JS app a moment to initialise localStorage.
		// We do this in a goroutine so we don't block the event loop.
		go func() {
			time.Sleep(500 * time.Millisecond)
			send(readLocalStorage(page))
		}()
	})

	// Navigate. Errors during the initial goto are non-fatal — the MS SSO
	// redirect chain will trigger multiple navigations and the context-level
	// listeners will capture the token once Slack loads.
	_, _ = page.Goto(workspaceURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(30_000),
	})

	// Attempt localStorage immediately after DOMContentLoaded.
	send(readLocalStorage(page))

	// Also try after the full load event fires (JS bundles executed).
	page.Once("load", func() {
		send(readLocalStorage(page))
	})

	// Poll localStorage every second as a fallback. Slack SPA routes may not
	// fire a full load event after the initial page load, but localStorage is
	// always populated once the app is running. Stop polling once we have a token.
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-pageClosed:
				return
			case <-tokenCh:
				// Already captured — but channel is buffered so we just return.
				return
			case <-ticker.C:
				send(readLocalStorage(page))
			}
		}
	}()

	// Wait for token, SIGINT, timeout, or browser close.
	timeoutCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	var token string
	select {
	case <-timeoutCtx.Done():
		if ctx.Err() != nil {
			return Credentials{}, ctx.Err()
		}
		return Credentials{}, fmt.Errorf("authentication timed out after %s", opts.Timeout)

	case <-pageClosed:
		return Credentials{}, ErrBrowserClosed

	case token = <-tokenCh:
		// Token captured.
	}

	// Extract the "d" session cookie (xoxd) from storage state.
	state, err := bctx.StorageState()
	if err != nil {
		return Credentials{}, fmt.Errorf("reading browser storage state: %w", err)
	}

	xoxd := findDCookie(state.Cookies)
	if xoxd == "" {
		return Credentials{}, errors.New("could not find session cookie \"d\": ensure you are fully logged in to Slack")
	}

	// Derive the canonical workspace domain from the URL we navigated to.
	workspaceDomain := workspaceURL
	if u, uErr := url.Parse(workspaceURL); uErr == nil && u.Host != "" {
		workspaceDomain = u.Host
	}

	return Credentials{Token: token, Cookie: xoxd, Workspace: workspaceDomain}, nil
}

// isSlackAPI returns true if the URL is a Slack API endpoint.
func isSlackAPI(rawURL string) bool {
	return strings.Contains(rawURL, ".slack.com/api/")
}

// extractTokenFromRequest extracts a valid xoxc token from a network request,
// or returns empty string if none is found.
func extractTokenFromRequest(req playwright.Request) string {
	if req == nil || !isSlackAPI(req.URL()) {
		return ""
	}
	var tok string
	switch req.Method() {
	case "GET":
		tok, _ = extractTokenFromURL(req.URL())
	case "POST":
		tok, _ = extractTokenFromPostBody(req)
	}
	return tok
}

// extractTokenFromResponse scans a Slack API response body for a token.
// Handles the case where the token appears in a JSON response field.
func extractTokenFromResponse(resp playwright.Response) string {
	if resp == nil || !isSlackAPI(resp.URL()) {
		return ""
	}
	body, err := resp.Text()
	if err != nil {
		return ""
	}
	return tokenRE.FindString(body)
}

// extractTokenFromURL extracts a token from the query string of a URL.
func extractTokenFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	tok := u.Query().Get("token")
	if !tokenRE.MatchString(tok) {
		return "", nil
	}
	return tok, nil
}

// extractTokenFromPostBody extracts the token from a POST request body.
func extractTokenFromPostBody(req playwright.Request) (string, error) {
	ct, err := req.HeaderValue("content-type")
	if err != nil || ct == "" {
		body, _ := req.PostData()
		return tokenRE.FindString(body), nil
	}

	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil {
		return "", nil
	}

	switch mediaType {
	case "multipart/form-data":
		boundary, ok := params["boundary"]
		if !ok {
			return "", nil
		}
		body, err := req.PostData()
		if err != nil || body == "" {
			return "", nil
		}
		mr := multipart.NewReader(strings.NewReader(body), boundary)
		form, err := mr.ReadForm(65536)
		if err != nil {
			return "", nil
		}
		vals := form.Value["token"]
		if len(vals) == 0 || !tokenRE.MatchString(vals[0]) {
			return "", nil
		}
		return vals[0], nil

	case "application/x-www-form-urlencoded":
		body, err := req.PostData()
		if err != nil || body == "" {
			return "", nil
		}
		vals, err := url.ParseQuery(body)
		if err != nil {
			return "", nil
		}
		tok := vals.Get("token")
		if !tokenRE.MatchString(tok) {
			return "", nil
		}
		return tok, nil
	}

	// Last resort: raw body scan.
	body, _ := req.PostData()
	return tokenRE.FindString(body), nil
}

// readLocalStorage reads the xoxc token directly from Slack's localStorage.
// It tries all teams in localConfig_v2 and returns the first valid xoxc token
// found. Returns empty string if not present or if the page is not a Slack
// page yet.
func readLocalStorage(page playwright.Page) string {
	val, err := page.Evaluate(`() => {
		try {
			const raw = localStorage.getItem('localConfig_v2');
			if (!raw) return '';
			const cfg = JSON.parse(raw);
			if (!cfg || !cfg.teams) return '';
			for (const teamID of Object.keys(cfg.teams)) {
				const tok = cfg.teams[teamID].token;
				if (tok && tok.startsWith('xoxc-')) return tok;
			}
			return '';
		} catch(e) {
			return '';
		}
	}`)
	if err != nil {
		return ""
	}
	tok, ok := val.(string)
	if !ok || !tokenRE.MatchString(tok) {
		return ""
	}
	return tok
}

// findDCookie returns the value of the "d" session cookie.
func findDCookie(cookies []playwright.Cookie) string {
	for _, c := range cookies {
		if c.Name == "d" && strings.Contains(c.Domain, "slack.com") {
			return c.Value
		}
	}
	return ""
}

// normalizeWorkspaceURL converts a workspace name or URL to a full HTTPS URL.
func normalizeWorkspaceURL(workspace string) (string, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return "", errors.New("workspace must not be empty")
	}
	if strings.HasPrefix(workspace, "https://") || strings.HasPrefix(workspace, "http://") {
		return workspace, nil
	}
	workspace = strings.TrimSuffix(workspace, ".slack.com")
	return "https://" + workspace + ".slack.com", nil
}

func browserName(bt BrowserType) string {
	if bt == Firefox {
		return "firefox"
	}
	return "chromium"
}

func selectBrowserType(pw *playwright.Playwright, bt BrowserType) playwright.BrowserType {
	if bt == Firefox {
		return pw.Firefox
	}
	return pw.Chromium
}
