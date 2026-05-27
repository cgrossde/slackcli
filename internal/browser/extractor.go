// Package browser extracts Slack session credentials (xoxc token + xoxd cookie)
// from a live browser session using the Chrome DevTools Protocol (CDP).
// No Playwright or third-party browser-automation dependency required.
//
// Flow:
//  1. Locate a system Chrome/Chromium binary.
//  2. Launch it with --remote-debugging-port if CDP is not already reachable.
//  3. Find or open a tab at the workspace URL.
//  4. Connect to that tab's CDP WebSocket endpoint.
//  5. Enable Network events and poll Network.getCookies every second.
//  6. Concurrently scan Network.requestWillBeSent POST bodies for xoxc tokens.
//  7. Return once both xoxc token and "d" cookie are captured, or on timeout.
//
// All reads from the CDP connection go through a single multiplexer goroutine
// to avoid races on the shared buffered reader.
package browser

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	cdpPort           = 9223
	cdpStartupTimeout = 15 * time.Second
	cdpPollInterval   = 1 * time.Second
	defaultTimeout    = 5 * time.Minute
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
}

// Extract opens a real Chrome window, navigates to the given Slack workspace
// URL, waits for the user to authenticate, and returns the extracted
// credentials. The caller must cancel ctx or wait for timeout to abort.
func Extract(ctx context.Context, workspace string, opts Options) (Credentials, error) {
	if opts.Timeout == 0 {
		opts.Timeout = defaultTimeout
	}

	workspaceURL, err := normalizeWorkspaceURL(workspace)
	if err != nil {
		return Credentials{}, fmt.Errorf("invalid workspace: %w", err)
	}

	// 1. Locate Chrome.
	binary, err := findChromeBinary()
	if err != nil {
		return Credentials{}, err
	}

	// 2. Launch Chrome if CDP not already reachable, then wait for it to start.
	if !cdpReachable(cdpPort) {
		profileDir := chromeProfileDir()
		slog.Debug("launching Chrome", "binary", binary, "profile", profileDir)
		if lerr := launchChrome(binary, profileDir, workspaceURL, cdpPort); lerr != nil {
			return Credentials{}, fmt.Errorf("launching Chrome: %w", lerr)
		}
	} else {
		slog.Debug("reusing running Chrome on CDP port", "port", cdpPort)
	}

	if werr := waitForCDP(ctx, cdpPort, cdpStartupTimeout); werr != nil {
		return Credentials{}, fmt.Errorf("Chrome CDP not available: %w", werr)
	}

	// 3. Find or open a tab at the workspace URL.
	wsURL, err := cdpFindOrOpenPage(cdpPort, workspaceURL)
	if err != nil {
		return Credentials{}, err
	}
	slog.Debug("CDP page target", "ws", wsURL)

	// 4. Extract credentials.
	return cdpExtractCredentials(ctx, wsURL, workspaceURL, opts.Timeout)
}

// ── Chrome helpers ─────────────────────────────────────────────────────────

// findChromeBinary returns the first Chrome/Chromium binary found.
// CHROME_APP env var overrides the search.
func findChromeBinary() (string, error) {
	candidates := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/usr/bin/chromium-browser",
		"/usr/bin/chromium",
		"/snap/bin/chromium",
	}
	for _, c := range candidates {
		if isFile(c) {
			return c, nil
		}
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium-browser", "chromium"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf(
		"no Chrome or Chromium found; install Google Chrome or set CHROME_APP to the binary path",
	)
}

// chromeProfileDir returns a persistent profile directory for the browser
// session so users only need to log in once.
func chromeProfileDir() string {
	return "/tmp/slackcli-browser-profile"
}

// launchChrome starts Chrome with remote debugging enabled. The process is
// detached — the CLI does not wait for Chrome to exit.
func launchChrome(binary, profileDir, startURL string, port int) error {
	cmd := exec.Command(binary,
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--user-data-dir="+profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-sync",
		startURL,
	)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	return cmd.Start()
}

// cdpReachable returns true if a Chrome CDP endpoint is already listening.
func cdpReachable(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// waitForCDP polls GET /json/version until Chrome answers or timeout expires.
func waitForCDP(ctx context.Context, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("http://127.0.0.1:%d/json/version", port)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		resp, err := http.Get(addr) //nolint:noctx
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			return nil
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for Chrome on port %d", port)
}

// ── CDP page targeting ─────────────────────────────────────────────────────

type cdpTarget struct {
	Type string `json:"type"`
	URL  string `json:"url"`
	WS   string `json:"webSocketDebuggerUrl"`
}

// cdpFindOrOpenPage returns the CDP WebSocket URL for a tab at the workspace
// domain. If no matching tab exists it navigates a spare tab, or opens one.
func cdpFindOrOpenPage(port int, workspaceURL string) (string, error) {
	addr := fmt.Sprintf("http://127.0.0.1:%d/json", port)
	resp, err := http.Get(addr) //nolint:noctx
	if err != nil {
		return "", fmt.Errorf("cannot reach CDP on port %d: %w", port, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var targets []cdpTarget
	if jerr := json.Unmarshal(body, &targets); jerr != nil {
		return "", fmt.Errorf("invalid CDP /json response: %w", jerr)
	}

	wsHost := workspaceHost(workspaceURL)

	// Prefer a tab already on the workspace domain.
	for _, t := range targets {
		if t.Type == "page" && t.WS != "" && strings.Contains(t.URL, wsHost) {
			return t.WS, nil
		}
	}

	// Prefer a tab already on app.slack.com (SSO may have landed there).
	for _, t := range targets {
		if t.Type == "page" && t.WS != "" && strings.Contains(t.URL, "app.slack.com") {
			return t.WS, nil
		}
	}

	// Fall back to any page tab; navigate it to the workspace URL.
	for _, t := range targets {
		if t.Type == "page" && t.WS != "" {
			conn, cerr := cdpConnect(t.WS)
			if cerr != nil {
				continue
			}
			_, _ = conn.Send("Page.navigate", map[string]any{"url": workspaceURL})
			conn.Close()
			return t.WS, nil
		}
	}

	// Open a new tab.
	newResp, nerr := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json/new?%s", port, url.QueryEscape(workspaceURL))) //nolint:noctx
	if nerr != nil {
		return "", fmt.Errorf("could not open new Chrome tab: %w", nerr)
	}
	defer newResp.Body.Close()
	newBody, _ := io.ReadAll(newResp.Body)
	var newTarget cdpTarget
	if jerr := json.Unmarshal(newBody, &newTarget); jerr != nil || newTarget.WS == "" {
		return "", fmt.Errorf("could not determine WebSocket URL for new tab")
	}
	return newTarget.WS, nil
}

// ── Credential extraction via CDP ─────────────────────────────────────────

// cdpExtractCredentials connects to the CDP page at wsURL and waits until
// both the xoxc token and "d" session cookie are captured, or timeout/ctx.
func cdpExtractCredentials(ctx context.Context, wsURL, workspaceURL string, timeout time.Duration) (Credentials, error) {
	conn, err := cdpConnect(wsURL)
	if err != nil {
		return Credentials{}, fmt.Errorf("cannot connect to Chrome DevTools: %w", err)
	}
	defer conn.Close()

	if _, err = conn.Send("Network.enable", nil); err != nil {
		return Credentials{}, fmt.Errorf("Network.enable failed: %w", err)
	}

	var (
		mu    sync.Mutex
		token string
		xoxd  string
	)

	// Scan every Network request for an xoxc token in POST bodies or URLs.
	conn.OnEvent(func(method string, params json.RawMessage) {
		if method != "Network.requestWillBeSent" {
			return
		}
		tok := extractTokenFromCDPRequest(params)
		if tok == "" {
			return
		}
		mu.Lock()
		if token == "" {
			slog.Debug("token captured via CDP event")
			token = tok
		}
		mu.Unlock()
	})

	// Poll for token (via localStorage read) and "d" cookie every second.
	// CDP Runtime.evaluate lets us read localStorage from whichever origin
	// the tab is currently on — crucially this works across SSO redirects
	// because we're talking directly to Chrome, not through Playwright.
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(cdpPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCtx.Done():
			if ctx.Err() != nil {
				return Credentials{}, ctx.Err()
			}
			return Credentials{}, fmt.Errorf("authentication timed out after %s", timeout)

		case <-ticker.C:
			// Read localStorage for xoxc token.
			if tok := cdpReadLocalStorage(conn); tok != "" {
				mu.Lock()
				if token == "" {
					slog.Debug("token captured via localStorage poll")
					token = tok
				}
				mu.Unlock()
			}

			// Read "d" cookie from all slack.com domains.
			if d := cdpReadDCookie(conn); d != "" {
				mu.Lock()
				xoxd = d
				mu.Unlock()
			}

			mu.Lock()
			tok := token
			d := xoxd
			mu.Unlock()

			slog.Debug("poll tick", "token_found", tok != "", "cookie_found", d != "")

			if tok != "" && d != "" {
				// Close the browser now that we have everything.
				_, _ = conn.Send("Browser.close", nil)
				return Credentials{
					Token:     tok,
					Cookie:    d,
					Workspace: workspaceHost(workspaceURL),
				}, nil
			}
		}
	}
}

// cdpReadLocalStorage executes a Runtime.evaluate call to read localConfig_v2
// from the current page's localStorage and extract the first xoxc token.
func cdpReadLocalStorage(conn *cdpConn) string {
	const script = `(() => {
		try {
			const raw = localStorage.getItem('localConfig_v2');
			if (!raw) return '';
			const cfg = JSON.parse(raw);
			if (!cfg || !cfg.teams) return '';
			for (const id of Object.keys(cfg.teams)) {
				const tok = cfg.teams[id].token;
				if (tok && tok.startsWith('xoxc-')) return tok;
			}
			return '';
		} catch(e) { return ''; }
	})()`

	result, err := conn.Send("Runtime.evaluate", map[string]any{
		"expression":    script,
		"returnByValue": true,
	})
	if err != nil {
		return ""
	}

	var r struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if jerr := json.Unmarshal(result, &r); jerr != nil {
		return ""
	}
	tok := r.Result.Value
	if !tokenRE.MatchString(tok) {
		return ""
	}
	return tok
}

// cdpReadDCookie calls Network.getCookies for all slack.com domains and
// returns the value of the "d" session cookie, or empty string if not found.
func cdpReadDCookie(conn *cdpConn) string {
	result, err := conn.Send("Network.getCookies", map[string]any{
		"urls": []string{
			"https://app.slack.com",
			"https://slack.com",
		},
	})
	if err != nil {
		return ""
	}

	var r struct {
		Cookies []struct {
			Name   string `json:"name"`
			Value  string `json:"value"`
			Domain string `json:"domain"`
		} `json:"cookies"`
	}
	if jerr := json.Unmarshal(result, &r); jerr != nil {
		return ""
	}
	for _, c := range r.Cookies {
		if c.Name == "d" && strings.Contains(c.Domain, "slack.com") {
			return c.Value
		}
	}
	return ""
}

// extractTokenFromCDPRequest inspects a Network.requestWillBeSent params blob
// and extracts an xoxc token from the request URL or POST body.
func extractTokenFromCDPRequest(params json.RawMessage) string {
	var event struct {
		Request struct {
			URL      string `json:"url"`
			Method   string `json:"method"`
			PostData string `json:"postData"`
		} `json:"request"`
	}
	if err := json.Unmarshal(params, &event); err != nil {
		return ""
	}
	if !strings.Contains(event.Request.URL, ".slack.com/api/") {
		return ""
	}
	if event.Request.Method == "GET" {
		tok, _ := extractTokenFromURL(event.Request.URL)
		return tok
	}
	if event.Request.PostData != "" {
		tok, _ := extractTokenFromPostBody(event.Request.Method, event.Request.PostData)
		return tok
	}
	return ""
}

// ── Token extraction helpers ───────────────────────────────────────────────

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

// extractTokenFromPostBody extracts the token from a raw POST body string.
func extractTokenFromPostBody(method, body string) (string, error) {
	if method != "POST" || body == "" {
		return "", nil
	}

	// Try to infer content type from the body itself.
	// Slack's API uses multipart/form-data and application/x-www-form-urlencoded.
	// The CDP postData field is the raw string; we don't have headers here,
	// so try URL-encoded first (most common for Slack API calls), then scan raw.
	if vals, err := url.ParseQuery(body); err == nil {
		if tok := vals.Get("token"); tokenRE.MatchString(tok) {
			return tok, nil
		}
	}

	// Try multipart — detect by looking for boundary= marker.
	if idx := strings.Index(body, "boundary="); idx != -1 {
		boundary := strings.Fields(body[idx+9:])[0]
		boundary = strings.TrimRight(boundary, "\r\n")
		mr := multipart.NewReader(strings.NewReader(body), boundary)
		form, err := mr.ReadForm(65536)
		if err == nil {
			if vals := form.Value["token"]; len(vals) > 0 && tokenRE.MatchString(vals[0]) {
				return vals[0], nil
			}
		}
	}

	// Last resort: raw scan.
	tok := tokenRE.FindString(body)
	return tok, nil
}

// ── URL helpers ────────────────────────────────────────────────────────────

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

// workspaceHost extracts the bare hostname from a workspace URL.
func workspaceHost(workspaceURL string) string {
	if u, err := url.Parse(workspaceURL); err == nil && u.Host != "" {
		return u.Host
	}
	return workspaceURL
}

// isFile reports whether path exists as a regular file.
func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// ── Minimal RFC 6455 WebSocket client for CDP ─────────────────────────────
//
// All reads from the connection are owned by a single mux goroutine which
// dispatches responses (have an id) to waiting Send callers via per-id
// channels, and routes events (no id, have a method) to the registered
// event handler. This eliminates the reader race that would occur if Send
// and the event listener both blocked on the same bufio.Reader.

type cdpConn struct {
	conn    net.Conn
	reader  *bufio.Reader
	writeMu sync.Mutex

	idMu   sync.Mutex
	nextID int

	pendingMu sync.Mutex
	pending   map[int]chan json.RawMessage

	eventMu      sync.Mutex
	eventHandler func(method string, params json.RawMessage)

	closed int32 // atomic
}

// cdpConnect dials a CDP WebSocket URL and starts the read mux goroutine.
func cdpConnect(wsURL string) (*cdpConn, error) {
	u, err := url.Parse(wsURL)
	if err != nil {
		return nil, fmt.Errorf("invalid CDP WebSocket URL %q: %w", wsURL, err)
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":80"
	}

	conn, err := net.DialTimeout("tcp", host, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("TCP dial %s: %w", host, err)
	}

	keyBytes := make([]byte, 16)
	if _, rerr := rand.Read(keyBytes); rerr != nil {
		conn.Close()
		return nil, fmt.Errorf("rand: %w", rerr)
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	handshake := fmt.Sprintf(
		"GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		path, u.Host, key,
	)
	if _, werr := io.WriteString(conn, handshake); werr != nil {
		conn.Close()
		return nil, fmt.Errorf("WebSocket handshake write: %w", werr)
	}

	reader := bufio.NewReaderSize(conn, 128*1024)
	statusLine, rerr := reader.ReadString('\n')
	if rerr != nil {
		conn.Close()
		return nil, fmt.Errorf("WebSocket handshake read: %w", rerr)
	}
	if !strings.Contains(statusLine, "101") {
		conn.Close()
		return nil, fmt.Errorf("WebSocket upgrade rejected: %s", strings.TrimSpace(statusLine))
	}
	for {
		line, lerr := reader.ReadString('\n')
		if lerr != nil {
			conn.Close()
			return nil, fmt.Errorf("WebSocket header read: %w", lerr)
		}
		if line == "\r\n" {
			break
		}
	}

	c := &cdpConn{
		conn:    conn,
		reader:  reader,
		pending: make(map[int]chan json.RawMessage),
	}
	go c.mux()
	return c, nil
}

// OnEvent registers a handler called for every unsolicited CDP event.
func (c *cdpConn) OnEvent(fn func(method string, params json.RawMessage)) {
	c.eventMu.Lock()
	c.eventHandler = fn
	c.eventMu.Unlock()
}

// mux is the sole reader goroutine. It dispatches responses to Send callers
// and forwards events to the registered handler.
func (c *cdpConn) mux() {
	for {
		payload, err := c.readFrame()
		if err != nil {
			atomic.StoreInt32(&c.closed, 1)
			c.pendingMu.Lock()
			for _, ch := range c.pending {
				close(ch)
			}
			c.pending = make(map[int]chan json.RawMessage)
			c.pendingMu.Unlock()
			return
		}

		var envelope struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if jerr := json.Unmarshal(payload, &envelope); jerr != nil {
			continue
		}

		if envelope.ID != nil {
			c.pendingMu.Lock()
			ch, ok := c.pending[*envelope.ID]
			if ok {
				delete(c.pending, *envelope.ID)
			}
			c.pendingMu.Unlock()
			if ok {
				ch <- payload
			}
			continue
		}

		if envelope.Method != "" {
			c.eventMu.Lock()
			h := c.eventHandler
			c.eventMu.Unlock()
			if h != nil {
				h(envelope.Method, envelope.Params)
			}
		}
	}
}

// Send marshals a CDP JSON-RPC command, writes it as a masked WebSocket text
// frame, and waits for the matching response from the mux goroutine.
func (c *cdpConn) Send(method string, params map[string]any) (json.RawMessage, error) {
	if atomic.LoadInt32(&c.closed) != 0 {
		return nil, fmt.Errorf("CDP connection closed")
	}

	c.idMu.Lock()
	c.nextID++
	id := c.nextID
	c.idMu.Unlock()

	ch := make(chan json.RawMessage, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()

	msg := map[string]any{"id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	data, err := json.Marshal(msg)
	if err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, err
	}
	if werr := c.writeFrame(data); werr != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, werr
	}

	payload, ok := <-ch
	if !ok {
		return nil, fmt.Errorf("CDP connection closed while waiting for %s response", method)
	}

	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if jerr := json.Unmarshal(payload, &env); jerr != nil {
		return nil, jerr
	}
	if env.Error != nil {
		return nil, fmt.Errorf("CDP error: %s", env.Error.Message)
	}
	return env.Result, nil
}

// Close sends a WebSocket close frame and terminates the connection.
func (c *cdpConn) Close() {
	_ = c.writeFrame(nil)
	c.conn.Close()
}

// writeFrame builds a client-masked RFC 6455 frame and writes it atomically.
// data==nil sends an opcode-8 (close) frame.
func (c *cdpConn) writeFrame(data []byte) error {
	opcode := byte(0x01) // text
	if data == nil {
		opcode = 0x08 // close
		data = []byte{}
	}

	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}

	n := len(data)
	header := []byte{0x80 | opcode, 0x80} // FIN=1, MASK=1
	switch {
	case n < 126:
		header[1] |= byte(n)
	case n < 65536:
		header[1] |= 126
		header = append(header, byte(n>>8), byte(n))
	default:
		header[1] |= 127
		header = append(header,
			byte(n>>56), byte(n>>48), byte(n>>40), byte(n>>32),
			byte(n>>24), byte(n>>16), byte(n>>8), byte(n),
		)
	}
	header = append(header, mask[:]...)

	masked := make([]byte, n)
	for i, b := range data {
		masked[i] = b ^ mask[i%4]
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.conn.Write(header); err != nil {
		return err
	}
	if n > 0 {
		_, err := c.conn.Write(masked)
		return err
	}
	return nil
}

// readFrame reads a single WebSocket frame payload. Only called from mux().
// Server-to-client frames are never masked (RFC 6455 §5.1).
func (c *cdpConn) readFrame() ([]byte, error) {
	b0, err := c.reader.ReadByte()
	if err != nil {
		return nil, err
	}
	opcode := b0 & 0x0F
	if opcode == 0x08 {
		return nil, io.EOF // close frame
	}

	b1, err := c.reader.ReadByte()
	if err != nil {
		return nil, err
	}
	masked := (b1 & 0x80) != 0
	payLen := uint64(b1 & 0x7F)
	switch payLen {
	case 126:
		var buf [2]byte
		if _, err = io.ReadFull(c.reader, buf[:]); err != nil {
			return nil, err
		}
		payLen = uint64(buf[0])<<8 | uint64(buf[1])
	case 127:
		var buf [8]byte
		if _, err = io.ReadFull(c.reader, buf[:]); err != nil {
			return nil, err
		}
		payLen = uint64(buf[0])<<56 | uint64(buf[1])<<48 |
			uint64(buf[2])<<40 | uint64(buf[3])<<32 |
			uint64(buf[4])<<24 | uint64(buf[5])<<16 |
			uint64(buf[6])<<8 | uint64(buf[7])
	}

	var maskKey [4]byte
	if masked {
		if _, err = io.ReadFull(c.reader, maskKey[:]); err != nil {
			return nil, err
		}
	}

	payload := make([]byte, payLen)
	if _, err = io.ReadFull(c.reader, payload); err != nil {
		return nil, err
	}
	if masked {
		for i, b := range payload {
			payload[i] = b ^ maskKey[i%4]
		}
	}
	return payload, nil
}
