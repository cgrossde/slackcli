// Package browser extracts Slack session credentials (xoxc token + xoxd cookie)
// using the Chrome DevTools Protocol (CDP). No third-party browser automation
// dependencies — CDP is spoken over a hand-rolled RFC 6455 WebSocket client.
//
// Flow:
//  1. Find the system Chrome/Chromium/Brave binary.
//  2. Launch it with --remote-debugging-port (reuse if already running).
//  3. Find or open a tab navigated to the workspace URL.
//  4. Poll Network.getCookies for the "d" cookie and Runtime.evaluate for the
//     xoxc token in localStorage until both are found or timeout elapses.
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
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	cdpPort           = 9235
	cdpStartupTimeout = 15 * time.Second
	defaultTimeout    = 5 * time.Minute
	pollInterval      = 2 * time.Second
)

// tokenRE matches a valid Slack client (xoxc) token.
var tokenRE = regexp.MustCompile(`xoxc-[0-9]+-[0-9]+-[0-9]+-[0-9a-z]+`)

// ErrBrowserClosed is returned when the browser is closed before authentication completes.
var ErrBrowserClosed = errors.New("browser was closed before authentication completed")

// Credentials holds the extracted Slack session credentials.
type Credentials struct {
	Token     string
	Cookie    string
	Workspace string
}

// Options configures the extraction behaviour.
type Options struct {
	// Timeout is the maximum time to wait for the user to log in.
	// Defaults to 5 minutes.
	Timeout time.Duration
}

// Extract opens a visible Chrome window, navigates to the given Slack workspace
// URL, waits for the user to authenticate, and returns the extracted credentials.
func Extract(ctx context.Context, workspace string, opts Options) (Credentials, error) {
	if opts.Timeout == 0 {
		opts.Timeout = defaultTimeout
	}

	workspaceURL, err := normalizeWorkspaceURL(workspace)
	if err != nil {
		return Credentials{}, fmt.Errorf("invalid workspace: %w", err)
	}

	binary, err := FindChromeBinary()
	if err != nil {
		return Credentials{}, err
	}

	profileDir, err := chromeProfileDir()
	if err != nil {
		return Credentials{}, fmt.Errorf("cannot determine Chrome profile directory: %w", err)
	}

	var chromCmd *exec.Cmd
	if !cdpReachable(cdpPort) {
		chromCmd, err = launchChrome(binary, profileDir, workspaceURL, cdpPort)
		if err != nil {
			return Credentials{}, fmt.Errorf("failed to launch Chrome: %w", err)
		}
	} else {
		slog.Debug("chrome already running, reusing", "port", cdpPort)
	}

	if werr := waitForCDP(ctx, cdpPort, cdpStartupTimeout); werr != nil {
		if chromCmd != nil {
			chromCmd.Process.Kill() //nolint:errcheck
		}
		return Credentials{}, fmt.Errorf("Chrome CDP not available: %w", werr)
	}

	targetID, wsURL, nerr := cdpFindOrOpenPage(cdpPort, workspaceURL)
	if nerr != nil {
		if chromCmd != nil {
			chromCmd.Process.Kill() //nolint:errcheck
		}
		return Credentials{}, nerr
	}

	creds, cerr := cdpExtractCredentials(ctx, wsURL, workspaceURL, opts.Timeout)

	cdpCloseTab(cdpPort, targetID)
	if chromCmd != nil {
		chromCmd.Process.Kill() //nolint:errcheck
	}

	return creds, cerr
}

// cdpExtractCredentials connects to the CDP WebSocket for the given tab and
// polls until both the xoxc token (from localStorage) and the "d" cookie are found.
func cdpExtractCredentials(ctx context.Context, wsURL, workspaceURL string, timeout time.Duration) (Credentials, error) {
	conn, err := cdpConnect(wsURL)
	if err != nil {
		return Credentials{}, fmt.Errorf("cannot connect to Chrome DevTools: %w", err)
	}
	defer conn.Close()

	if _, err = conn.Send("Network.enable", nil); err != nil {
		return Credentials{}, fmt.Errorf("Network.enable failed: %w", err)
	}

	deadline := time.Now().Add(timeout)
	timeoutCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	for {
		select {
		case <-timeoutCtx.Done():
			if ctx.Err() != nil {
				return Credentials{}, ctx.Err()
			}
			return Credentials{}, fmt.Errorf("authentication timed out after %s", timeout)
		case <-time.After(pollInterval):
		}

		// Poll current tab URL via Runtime.evaluate so we know where we are.
		if urlRaw, evalErr := conn.Send("Runtime.evaluate", map[string]any{
			"expression":    "location.href",
			"returnByValue": true,
		}); evalErr == nil {
			var result struct {
				Result struct{ Value string }
			}
			if json.Unmarshal(urlRaw, &result) == nil {
				slog.Debug("cdp poll", "url", result.Result.Value)
			}
		}

		// Try to read xoxc from localStorage.
		token := cdpReadLocalStorage(conn)

		// Poll for the "d" cookie on slack.com.
		cookieRaw, cerr := conn.Send("Network.getCookies", map[string]any{
			"urls": []string{"https://slack.com", workspaceURL},
		})
		if cerr != nil {
			return Credentials{}, fmt.Errorf("Chrome connection lost (browser was closed?): %w", cerr)
		}
		xoxd := parseDCookie(cookieRaw)

		slog.Debug("cdp poll result", "token_found", token != "", "cookie_found", xoxd != "")

		if token != "" && xoxd != "" {
			workspaceDomain := workspaceURL
			if u, uErr := url.Parse(workspaceURL); uErr == nil && u.Host != "" {
				workspaceDomain = u.Host
			}
			return Credentials{Token: token, Cookie: xoxd, Workspace: workspaceDomain}, nil
		}
	}
}

// cdpReadLocalStorage reads the xoxc token from Slack's localStorage via CDP Runtime.evaluate.
func cdpReadLocalStorage(conn *cdpConn) string {
	raw, err := conn.Send("Runtime.evaluate", map[string]any{
		"expression": `(() => {
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
		})()`,
		"returnByValue": true,
	})
	if err != nil {
		return ""
	}
	var result struct {
		Result struct{ Value string }
	}
	if json.Unmarshal(raw, &result) != nil {
		return ""
	}
	tok := result.Result.Value
	if tok == "" {
		return ""
	}
	if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		preview := tok
		if len(preview) > 40 {
			preview = preview[:40] + "..."
		}
		slog.Debug("localStorage token", "value", preview, "matches_re", tokenRE.MatchString(tok))
	}
	if !tokenRE.MatchString(tok) {
		return ""
	}
	return tok
}

// parseDCookie extracts the "d" session cookie value from a Network.getCookies result.
func parseDCookie(result json.RawMessage) string {
	var r struct {
		Cookies []struct {
			Name   string `json:"name"`
			Value  string `json:"value"`
			Domain string `json:"domain"`
		} `json:"cookies"`
	}
	if json.Unmarshal(result, &r) != nil {
		return ""
	}
	for _, c := range r.Cookies {
		if c.Name == "d" && strings.Contains(c.Domain, "slack.com") {
			return c.Value
		}
	}
	return ""
}

// ── Chrome launching ─────────────────────────────────────────────────────────

// FindChromeBinary returns the path to the first Chrome/Chromium/Brave binary
// found on the system. CHROME_APP env var overrides the search.
func FindChromeBinary() (string, error) {
	if override := os.Getenv("CHROME_APP"); override != "" {
		if _, err := os.Stat(override); err == nil {
			return override, nil
		}
		return "", fmt.Errorf("CHROME_APP=%q not found", override)
	}
	candidates := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		"/usr/bin/chromium-browser",
		"/usr/bin/chromium",
		"/usr/bin/google-chrome",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("no Chrome/Chromium/Brave binary found; set CHROME_APP to override")
}

// chromeProfileDir returns (and creates) the Chrome profile directory.
// SLACKCLI_CHROME_PROFILE env var overrides the default (~/.config/slackcli/chrome-profile).
func chromeProfileDir() (string, error) {
	if override := os.Getenv("SLACKCLI_CHROME_PROFILE"); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "slackcli", "chrome-profile")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("cannot create Chrome profile dir %s: %w", dir, err)
	}
	return dir, nil
}

func launchChrome(binary, profileDir, targetURL string, port int) (*exec.Cmd, error) {
	cmd := exec.Command(binary,
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--user-data-dir="+profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-sync",
		targetURL,
	)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func cdpReachable(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func waitForCDP(ctx context.Context, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("http://127.0.0.1:%d/json/version", port)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		resp, err := http.Get(addr) //nolint:noctx
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for Chrome on port %d (waited %s)", port, timeout)
}

// ── CDP page targeting ────────────────────────────────────────────────────────

type cdpTarget struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	URL  string `json:"url"`
	WS   string `json:"webSocketDebuggerUrl"`
}

// cdpFindOrOpenPage returns the target ID and WebSocket URL for a page on the
// workspace host. If none exists it navigates a spare tab or opens a new one.
func cdpFindOrOpenPage(port int, workspaceURL string) (targetID, wsURL string, err error) {
	addr := fmt.Sprintf("http://127.0.0.1:%d/json", port)
	resp, err := http.Get(addr) //nolint:noctx
	if err != nil {
		return "", "", fmt.Errorf("cannot reach CDP on port %d: %w", port, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var targets []cdpTarget
	if jerr := json.Unmarshal(body, &targets); jerr != nil {
		return "", "", fmt.Errorf("invalid CDP /json response: %w", jerr)
	}

	u, _ := url.Parse(workspaceURL)
	host := ""
	if u != nil {
		host = u.Host
	}

	// Prefer an existing tab already on the workspace host.
	for _, t := range targets {
		if t.Type == "page" && t.WS != "" && host != "" && strings.Contains(t.URL, host) {
			return t.ID, t.WS, nil
		}
	}

	// Navigate a spare tab.
	for _, t := range targets {
		if t.Type == "page" && t.WS != "" {
			conn, cerr := cdpConnect(t.WS)
			if cerr != nil {
				continue
			}
			_, _ = conn.Send("Page.navigate", map[string]any{"url": workspaceURL})
			conn.Close()
			return t.ID, t.WS, nil
		}
	}

	// Open a new tab.
	newResp, nerr := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json/new?%s", port, url.QueryEscape(workspaceURL))) //nolint:noctx
	if nerr != nil {
		return "", "", fmt.Errorf("could not open new Chrome tab: %w", nerr)
	}
	defer newResp.Body.Close()
	newBody, _ := io.ReadAll(newResp.Body)
	var newTarget cdpTarget
	if jerr := json.Unmarshal(newBody, &newTarget); jerr != nil || newTarget.WS == "" {
		return "", "", fmt.Errorf("could not determine WebSocket URL for new tab")
	}
	return newTarget.ID, newTarget.WS, nil
}

func cdpCloseTab(port int, targetID string) {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json/close/%s", port, targetID)) //nolint:noctx
	if err != nil {
		return
	}
	resp.Body.Close()
}

// ── Minimal RFC 6455 WebSocket client for CDP ─────────────────────────────────

type cdpConn struct {
	conn    net.Conn
	reader  *bufio.Reader
	writeMu sync.Mutex

	nextID int
	idMu   sync.Mutex

	pendingMu sync.Mutex
	pending   map[int]chan json.RawMessage

	eventMu      sync.Mutex
	eventHandler func(method string, params json.RawMessage)

	closed int32
}

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

func (c *cdpConn) OnEvent(fn func(method string, params json.RawMessage)) {
	c.eventMu.Lock()
	c.eventHandler = fn
	c.eventMu.Unlock()
}

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
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
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

func (c *cdpConn) Close() {
	c.writeFrame(nil) //nolint:errcheck
	c.conn.Close()
}

func (c *cdpConn) writeFrame(data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	var header [10]byte
	headerLen := 2

	if data == nil {
		// Close frame.
		header[0] = 0x88
		header[1] = 0x80
		mask := header[2:6]
		if _, err := rand.Read(mask); err != nil {
			return err
		}
		headerLen = 6
		_, err := c.conn.Write(header[:headerLen])
		return err
	}

	header[0] = 0x81 // text frame, FIN
	l := len(data)
	switch {
	case l <= 125:
		header[1] = byte(l) | 0x80
		headerLen = 2
	case l <= 65535:
		header[1] = 126 | 0x80
		header[2] = byte(l >> 8)
		header[3] = byte(l)
		headerLen = 4
	default:
		header[1] = 127 | 0x80
		for i := 0; i < 8; i++ {
			header[2+i] = byte(l >> (56 - 8*i))
		}
		headerLen = 10
	}

	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}

	masked := make([]byte, l)
	for i, b := range data {
		masked[i] = b ^ mask[i%4]
	}

	buf := make([]byte, 0, headerLen+4+l)
	buf = append(buf, header[:headerLen]...)
	buf = append(buf, mask...)
	buf = append(buf, masked...)
	_, err := c.conn.Write(buf)
	return err
}

func (c *cdpConn) readFrame() ([]byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(c.reader, header); err != nil {
		return nil, err
	}

	// opcode := header[0] & 0x0f  // we don't need it
	masked := (header[1] & 0x80) != 0
	payloadLen := int(header[1] & 0x7f)

	switch payloadLen {
	case 126:
		ext := make([]byte, 2)
		if _, err := io.ReadFull(c.reader, ext); err != nil {
			return nil, err
		}
		payloadLen = int(ext[0])<<8 | int(ext[1])
	case 127:
		ext := make([]byte, 8)
		if _, err := io.ReadFull(c.reader, ext); err != nil {
			return nil, err
		}
		payloadLen = 0
		for _, b := range ext {
			payloadLen = (payloadLen << 8) | int(b)
		}
	}

	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(c.reader, mask[:]); err != nil {
			return nil, err
		}
	}

	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(c.reader, payload); err != nil {
		return nil, err
	}
	if masked {
		for i, b := range payload {
			payload[i] = b ^ mask[i%4]
		}
	}
	return payload, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

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
