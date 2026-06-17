// Package slack — websocket.go implements a real-time event stream via
// Slack's internal WebSocket gateway.
//
// Flow:
//  1. Call GatewayServer to obtain the wss:// endpoint from client.userBoot.
//  2. Call DialWebSocket to establish the connection with uTLS + Chrome UA.
//  3. Call WSConn.ReadEvent in a loop to receive parsed Events.
//  4. The caller sends context cancellation to stop the loop.
//
// Ping/pong: a goroutine sends {"type":"ping","id":N} every 30 s.  The read
// deadline is reset on every ReadEvent call (not only on pong) to handle
// high-traffic workspaces.
package slack

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	gorilla "github.com/gorilla/websocket"
	utls "github.com/refraction-networking/utls"
)

const (
	wsPingInterval = 30 * time.Second
	wsReadDeadline = 120 * time.Second
)

// Event is a normalised Slack WebSocket event.  Only fields present in the
// raw JSON are populated; missing fields are the zero value for their type.
type Event struct {
	Type        string          // "message", "reaction_added", …
	SubType     string          // message subtype ("message_changed", "message_deleted", …)
	Channel     string          // channel ID (C…); for reactions, the channel of the reacted-to message
	User        string          // user who triggered the event
	Text        string          // message body (message events)
	Ts          string          // event timestamp
	ThreadTs    string          // thread root timestamp (thread replies)
	Reaction    string          // emoji name without colons (reaction events)
	ItemUser    string          // user whose item was reacted to
	ItemTs      string          // timestamp of the reacted-to item
	Mentions    []string        // user IDs mentioned in the text (<@UXXXX> patterns)
	Attachments []Attachment    // link unfurls and forwarded message previews
	Raw         json.RawMessage // verbatim JSON of the full frame
}

// wsRaw is the wire shape used for unmarshalling.
type wsRaw struct {
	Type        string `json:"type"`
	SubType     string `json:"subtype"`
	Channel     string `json:"channel"`
	User        string `json:"user"`
	Text        string `json:"text"`
	Ts          string `json:"ts"`
	ThreadTs    string `json:"thread_ts"`
	Reaction    string `json:"reaction"`
	ItemUser    string `json:"item_user"`
	Attachments []struct {
		AuthorName  string `json:"author_name"`
		AuthorLink  string `json:"author_link"`
		Title       string `json:"title"`
		TitleLink   string `json:"title_link"`
		Pretext     string `json:"pretext"`
		Text        string `json:"text"`
		FromURL     string `json:"from_url"`
		ServiceName string `json:"service_name"`
		ImageURL    string `json:"image_url"`
		ThumbURL    string `json:"thumb_url"`
		Footer      string `json:"footer"`
	} `json:"attachments"`
	Item struct {
		Channel string `json:"channel"`
		Ts      string `json:"ts"`
	} `json:"item"`
}

// userBootWorkspace is one entry in the workspaces array of client.userBoot.
type userBootWorkspace struct {
	ID     string `json:"id"`
	Domain string `json:"domain"`
}

// userBootResponse is the minimal subset of client.userBoot we need.
type userBootResponse struct {
	OK         bool                  `json:"ok"`
	Error      string                `json:"error"`
	Workspaces []userBootWorkspace   `json:"workspaces"`
}

// wsGatewayBase is the fixed WebSocket entry point for Slack's RTM gateway.
const wsGatewayBase = "wss://wss-primary.slack.com/"

// GatewayServer calls client.userBoot for the given workspace and returns a
// fully-formed wss:// URL ready to dial.  workspace must be a bare domain
// (e.g. "myorg.slack.com").
//
// The gateway URL shape is:
//
//	wss://wss-primary.slack.com/?token=<xoxc>&gateway_server=<team_id>
//
// where team_id is the T… or E… ID of the workspace.
func (c *Client) GatewayServer(ctx context.Context, workspace string) (string, error) {
	teamID, err := c.TeamID(ctx, workspace)
	if err != nil {
		return "", err
	}
	wsURL := wsGatewayBase + "?token=" + url.QueryEscape(c.token) +
		"&gateway_server=" + url.QueryEscape(teamID)
	return wsURL, nil
}

// TeamID resolves the per-workspace team ID (T…) for workspace by calling
// client.userBoot. workspace must be a bare domain (e.g. "myorg.slack.com").
//
// On Enterprise Grid this returns the *member workspace* team ID, not the
// enterprise ID — the form required by the slack:// deep link scheme.
func (c *Client) TeamID(ctx context.Context, workspace string) (string, error) {
	apiURL := fmt.Sprintf("https://%s/api/client.userBoot", workspace)

	form := strings.NewReader("token=" + url.QueryEscape(c.token))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, form)
	if err != nil {
		return "", fmt.Errorf("client.userBoot: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("client.userBoot: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("client.userBoot: read body: %w", err)
	}

	var boot userBootResponse
	if err := json.Unmarshal(body, &boot); err != nil {
		return "", fmt.Errorf("client.userBoot: parse response: %w", err)
	}
	if !boot.OK {
		return "", fmt.Errorf("client.userBoot: %s", boot.Error)
	}

	// Find the team ID for the requested workspace domain.
	// workspace is e.g. "myorg.slack.com"; domain in the response is "myorg".
	wantDomain := strings.TrimSuffix(workspace, ".slack.com")
	for _, ws := range boot.Workspaces {
		if ws.Domain == wantDomain || ws.ID == workspace {
			return ws.ID, nil
		}
	}
	// Fallback: use the first workspace if there is exactly one.
	if len(boot.Workspaces) == 1 {
		return boot.Workspaces[0].ID, nil
	}
	return "", fmt.Errorf("client.userBoot: workspace %q not found in response (domains: %s)",
		workspace, workspaceDomains(boot.Workspaces))
}

// workspaceDomains returns a comma-separated list of domains for error messages.
func workspaceDomains(ws []userBootWorkspace) string {
	parts := make([]string, len(ws))
	for i, w := range ws {
		parts[i] = w.Domain
	}
	return strings.Join(parts, ", ")
}

// WSConn holds an active WebSocket connection to the Slack gateway.
type WSConn struct {
	conn     *gorilla.Conn
	pingID   atomic.Int64
	pingStop chan struct{}
}

// DialWebSocket connects to gatewayServer (a wss:// URL) using the same uTLS
// Chrome fingerprint as the HTTP client.  Returns a ready-to-use *WSConn
// with the ping goroutine already running.
func (c *Client) DialWebSocket(ctx context.Context, gatewayServer string) (*WSConn, error) {
	dialer := gorilla.Dialer{
		HandshakeTimeout:  15 * time.Second,
		NetDialTLSContext: utlsDialTLS(),
	}

	header := http.Header{}
	header.Set("User-Agent", chromeUA)
	header.Set("Origin", "https://app.slack.com")
	if c.cookie != "" {
		header.Set("Cookie", "d="+c.cookie)
	}
	conn, _, err := dialer.DialContext(ctx, gatewayServer, header)
	if err != nil {
		return nil, fmt.Errorf("dial websocket %s: %w", gatewayServer, err)
	}

	ws := &WSConn{
		conn:     conn,
		pingStop: make(chan struct{}),
	}
	ws.resetDeadline()

	// When the context is cancelled, expire the read deadline immediately so
	// any blocked ReadMessage call returns promptly rather than waiting up to
	// wsReadDeadline.
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetReadDeadline(time.Now())
		case <-ws.pingStop:
		}
	}()

	go ws.pingLoop()
	return ws, nil
}

// ReadEvent reads the next event from the WebSocket connection.  It blocks
// until an event arrives, the context is cancelled, or the connection closes.
// Internal event types (hello, pong, typing, etc.) are silently skipped.
func (ws *WSConn) ReadEvent(ctx context.Context) (Event, error) {
	for {
		select {
		case <-ctx.Done():
			return Event{}, ctx.Err()
		default:
		}

		ws.resetDeadline()
		_, msg, err := ws.conn.ReadMessage()
		if err != nil {
			// If the context fired and we expired the deadline to unblock this
			// read, return the context error rather than the deadline error.
			if ctxErr := ctx.Err(); ctxErr != nil {
				return Event{}, ctxErr
			}
			return Event{}, fmt.Errorf("websocket read: %w", err)
		}

		var raw wsRaw
		if err := json.Unmarshal(msg, &raw); err != nil {
			// Malformed frame — skip, do not crash the stream.
			continue
		}

		if skipEventType(raw.Type) {
			continue
		}

		// For reaction events, the top-level channel field is absent;
		// the channel of the reacted-to message lives in item.channel.
		channel := raw.Channel
		if channel == "" {
			channel = raw.Item.Channel
		}

		// Extract mentioned user IDs from the message text.
		var mentions []string
		if raw.Text != "" {
			for _, m := range mentionRe.FindAllString(raw.Text, -1) {
				mentions = append(mentions, m[2:len(m)-1]) // strip <@ and >
			}
		}

		attachments := make([]Attachment, 0, len(raw.Attachments))
		for _, a := range raw.Attachments {
			attachments = append(attachments, Attachment{
				AuthorName:  a.AuthorName,
				AuthorLink:  a.AuthorLink,
				Title:       a.Title,
				TitleLink:   a.TitleLink,
				Pretext:     a.Pretext,
				Text:        a.Text,
				FromURL:     a.FromURL,
				ServiceName: a.ServiceName,
				ImageURL:    a.ImageURL,
				ThumbURL:    a.ThumbURL,
				Footer:      a.Footer,
			})
		}
		return Event{
			Type:        raw.Type,
			SubType:     raw.SubType,
			Channel:     channel,
			User:        raw.User,
			Text:        raw.Text,
			Ts:          raw.Ts,
			ThreadTs:    raw.ThreadTs,
			Reaction:    raw.Reaction,
			ItemUser:    raw.ItemUser,
			ItemTs:      raw.Item.Ts,
			Mentions:    mentions,
			Attachments: attachments,
			Raw:         json.RawMessage(msg),
		}, nil
	}
}

// Close stops the ping goroutine and closes the underlying connection.
func (ws *WSConn) Close() error {
	close(ws.pingStop)
	return ws.conn.Close()
}

// resetDeadline extends the read deadline by wsReadDeadline from now.
func (ws *WSConn) resetDeadline() {
	_ = ws.conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
}

// pingLoop sends a JSON ping frame every wsPingInterval until stopped.
func (ws *WSConn) pingLoop() {
	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()

	ws.conn.SetPongHandler(func(_ string) error {
		ws.resetDeadline()
		return nil
	})

	for {
		select {
		case <-ws.pingStop:
			return
		case <-ticker.C:
			id := ws.pingID.Add(1)
			ping := fmt.Sprintf(`{"type":"ping","id":%d}`, id)
			if err := ws.conn.WriteMessage(gorilla.TextMessage, []byte(ping)); err != nil {
				return
			}
		}
	}
}

// AllowedEventTypes is the set of real-time event types the live stream
// surfaces to callers. Any type not in this set is silently dropped.
var AllowedEventTypes = []string{
	"message",
	"reaction_added",
	"reaction_removed",
	"member_joined_channel",
	"member_left_channel",
	"channel_created",
	"channel_deleted",
	"channel_rename",
	"team_join",
	"desktop_notification",
}

// allowedEventSet is a fast lookup set built from AllowedEventTypes.
var allowedEventSet = func() map[string]bool {
	m := make(map[string]bool, len(AllowedEventTypes))
	for _, t := range AllowedEventTypes {
		m[t] = true
	}
	return m
}()

// skipEventType reports whether the event type should be dropped from the
// live stream. Uses an allowlist: only types in AllowedEventTypes pass through.
func skipEventType(t string) bool {
	return !allowedEventSet[t]
}

// utlsDialTLS returns a gorilla NetDialTLSContext function that performs the
// TLS handshake using a Chrome uTLS fingerprint.  Falls back to the standard
// TLS dialer if uTLS handshake fails.
func utlsDialTLS() func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}

		tcpConn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}

		uconn := utls.UClient(tcpConn, &utls.Config{
			ServerName: host,
		}, utls.HelloChrome_Auto)

		if err := uconn.HandshakeContext(ctx); err != nil {
			_ = tcpConn.Close()
			// Fallback: plain TLS on a fresh TCP connection.
			tcpConn2, dialErr := (&net.Dialer{}).DialContext(ctx, network, addr)
			if dialErr != nil {
				return nil, dialErr
			}
			tlsConn := tls.Client(tcpConn2, &tls.Config{ServerName: host})
			if err2 := tlsConn.HandshakeContext(ctx); err2 != nil {
				_ = tcpConn2.Close()
				return nil, err2
			}
			return tlsConn, nil
		}
		return uconn, nil
	}
}
