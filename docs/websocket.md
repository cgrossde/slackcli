# WebSocket Real-Time Connection

How to connect to Slack's internal WebSocket gateway using the xoxc/xoxd credentials this tool already holds. This documents the private client protocol — not RTM, not Socket Mode.

## Background

The Slack web and desktop apps maintain a persistent WebSocket connection to receive messages, reactions, presence changes, and all other real-time events. This is distinct from the two public APIs:

- **RTM API** (`rtm.start` / `rtm.connect`) — deprecated, does not accept xoxc tokens.
- **Socket Mode** — requires a bot token and an installed Slack app; not usable here.

The internal gateway accepts xoxc tokens directly, identical to every other API call this tool makes. No app installation or workspace admin approval is needed.

## Gateway URL

```
wss://wss-primary.slack.com/?token=<xoxc-TOKEN>&gateway_server=<GWSERVER_ID>
```

Required query parameters:

| Parameter | Source | Notes |
|---|---|---|
| `token` | Keychain xoxc token | Same token used for all API calls |
| `gateway_server` | `client.userBoot` response | Workspace-specific gateway ID |

The `d=<xoxd>` cookie must be present on the WebSocket upgrade request. The connection is rejected if any of the three values (token, gateway_server, d cookie) are missing.

The `wss-primary.slack.com` hostname is fixed. Some enterprise workspaces append `&enterprise_id=<ENTERPRISE_ID>` and may require the `d-s` cookie in addition to `d`.

## Getting the Gateway Server ID

The `gateway_server` value is returned by `client.userBoot`, which is the first API call the Slack client makes after login. This is a workspace-scoped identifier that does not change between sessions.

`client.userBoot` is a POST to `https://<workspace>.slack.com/api/client.userBoot` with the standard token form field. The response JSON contains a `self.id` and a top-level `gateway_server` field.

```json
{
  "ok": true,
  "gateway_server": "E12345ABCDE-1",
  ...
}
```

Alternatively, the value can be scraped from the active WebSocket URL in a live browser session (visible under the Network → WS tab in DevTools as the `gateway_server` query parameter).

## Dialing the Connection

Use `gorilla/websocket` (already in `go.sum` as a transitive dependency of slack-go). The upgrade request must carry the xoxd cookie, so a `http.Header` with the `Cookie` field must be passed to `websocket.Dialer.Dial`.

The TLS connection should use the same uTLS/Chrome fingerprint as all other API calls (`internal/slack/client.go`) to avoid session revocation from fingerprint mismatch.

Sketch:

```go
dialer := websocket.Dialer{
    TLSClientConfig: tlsConfig, // same uTLS config as API calls
    HandshakeTimeout: 45 * time.Second,
}

headers := http.Header{}
headers.Set("Cookie", "d="+xoxdCookie)
headers.Set("User-Agent", chromeUA)

gwURL := fmt.Sprintf(
    "wss://wss-primary.slack.com/?token=%s&gateway_server=%s",
    xoxcToken, gatewayServer,
)

conn, _, err := dialer.Dial(gwURL, headers)
```

## Event Stream

Events arrive as JSON objects. The top-level `type` field identifies the event. These are the same type names as the slack-go `EventMapping` in `websocket_managed_conn.go`:

| Type | Meaning |
|---|---|
| `hello` | Connection established. Contains `num_connections`. |
| `message` | New message, edit, or deletion in any channel the user is a member of. |
| `reaction_added` / `reaction_removed` | Emoji reaction on a message. |
| `channel_marked` | Read cursor moved (another session read messages). |
| `im_marked` | Read cursor moved for a DM. |
| `presence_change` | User came online or went away. |
| `member_joined_channel` / `member_left_channel` | Channel membership change. |
| `user_typing` | Another user is composing a message. |
| `channel_created` / `channel_deleted` / `channel_rename` | Channel lifecycle. |
| `team_join` | New workspace member. |
| `user_change` / `user_profile_changed` | Profile update. |
| `desktop_notification` | Push notification payload (fires for unread mentions). |

Thread replies arrive as `message` events with `thread_ts` set. The `subtype` field is `message_replied` on the root message update; individual replies have no subtype.

**Mention extraction.** The `Event` struct carries a `Mentions []string` field populated by `ReadEvent`. It contains all bare user IDs found in `<@UXXXX>` patterns in the event text. This is used by `slackcli live --mention` to filter events that mention the authenticated user without requiring a text scan in the caller.

**Channel field for reaction events.** The Slack gateway does not set a top-level `channel` field on `reaction_added` / `reaction_removed` events. Instead, the channel is nested in `item.channel`. `ReadEvent` transparently promotes `item.channel` to the top-level `Event.Channel` field so all callers (filtering, display) treat reaction events identically to message events.

**Attachment field.** Message events may carry an `Attachments []Attachment` field on the `Event` struct, populated by `ReadEvent` from the `attachments` array in the wire JSON. Attachments represent link unfurls and forwarded DM previews — each carries `AuthorName`, `AuthorLink`, `Title`, `TitleLink`, `Pretext`, `Text`, `FromURL`, `ServiceName`, `ImageURL`, `ThumbURL`, and `Footer`. Callers should check `len(Event.Attachments) > 0` to detect forwarded content; this is the only way to recover the original message body when a user forwards a DM to a channel (the top-level `text` field contains only the user's annotation, not the forwarded content).

Full event reference: the `EventMapping` var in `github.com/slack-go/slack@v0.23.1/websocket_managed_conn.go` lists every handled type with its corresponding Go struct.

## Reconnection

The connection requires periodic ping/pong to stay alive. The Slack client sends a JSON ping frame on a fixed interval and expects a pong response:

```json
{"type":"ping","id":1}
```

Response:

```json
{"type":"pong","reply_to":1}
```

If no pong is received within ~4× the ping interval, treat the connection as dead and reconnect. On reconnect, call `client.counts` to reconcile any missed unread counts, then use `conversations.history` with `oldest=<last_seen_ts>` per channel to fetch missed messages.

## Implementation Notes

- The gateway_server ID should be fetched once at startup and cached for the session lifetime. It does not need to be re-fetched on reconnect.
- The `gorilla/websocket` `Conn.SetReadDeadline` and `Conn.SetPongHandler` mechanism is the correct way to implement the deadman timer; do not use a goroutine that sleeps.
- Unlike the API calls, the WebSocket connection carries the token in the URL query string, not the request body. This is Slack's design and cannot be changed.
- Only one WebSocket connection per user session is needed regardless of how many channels are being monitored. All channels the user is a member of deliver events on the single connection.
- The `flannel=3`, `batch_presence_aware=1`, `no_query_on_subscribe=1`, and `lazy_channels=1` query parameters are sent by the official client but are not required for basic functionality.
