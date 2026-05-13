package slack

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestGetMessage_success(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.history" {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		if r.FormValue("channel") != "C123" {
			http.Error(w, "bad channel", http.StatusBadRequest)
			return
		}
		if r.FormValue("oldest") != "1752672853.184209" {
			http.Error(w, "bad oldest", http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{
			"ok": true,
			"messages": []map[string]any{
				{"type": "message", "user": "U123", "text": "hello world", "ts": "1752672853.184209"},
			},
		})
	}))

	got, err := c.GetMessage("C123", "1752672853.184209")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Text != "hello world" {
		t.Errorf("Text: got %q, want %q", got.Text, "hello world")
	}
	if got.User != "U123" {
		t.Errorf("User: got %q, want %q", got.User, "U123")
	}
}

func TestGetMessage_notFound(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "messages": []any{}})
	}))

	_, err := c.GetMessage("C123", "1111111111.000000")
	if err == nil {
		t.Fatal("expected error for empty messages, got nil")
	}
	if !errors.Is(err, ErrMessageNotFound) {
		t.Errorf("expected ErrMessageNotFound, got: %v", err)
	}
}

func TestGetMessage_apiError(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": false, "error": "channel_not_found"})
	}))

	_, err := c.GetMessage("CBAD", "1111111111.000000")
	if err == nil {
		t.Fatal("expected error for ok=false, got nil")
	}
}

// TestGetMessage_channelNotFound verifies that channel_not_found yields an
// error containing workspace guidance and does NOT wrap ErrMessageNotFound.
func TestGetMessage_channelNotFound(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": false, "error": "channel_not_found"})
	}))

	_, err := c.GetMessage("CBAD", "1111111111.000000")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, ErrMessageNotFound) {
		t.Error("channel_not_found should NOT wrap ErrMessageNotFound (it is an auth/access error, not a missing message)")
	}
	if !strings.Contains(err.Error(), "wrong workspace") {
		t.Errorf("error should mention 'wrong workspace': %v", err)
	}
}

func TestGetThread_singlePage(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.replies" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, map[string]any{
			"ok": true,
			"messages": []map[string]any{
				{"user": "U1", "text": "root", "ts": "1000000000.000001", "thread_ts": "1000000000.000001", "reply_count": 2},
				{"user": "U2", "text": "reply1", "ts": "1000000001.000001", "thread_ts": "1000000000.000001"},
				{"user": "U3", "text": "reply2", "ts": "1000000002.000001", "thread_ts": "1000000000.000001"},
			},
			"has_more": false,
		})
	}))

	got, err := c.GetThread("C123", "1000000000.000001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}
	if got[0].Text != "root" {
		t.Errorf("messages[0].Text: got %q, want %q", got[0].Text, "root")
	}
	if got[2].Text != "reply2" {
		t.Errorf("messages[2].Text: got %q, want %q", got[2].Text, "reply2")
	}
}

func TestGetThread_paginated(t *testing.T) {
	callCount := 0
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		callCount++
		if r.FormValue("cursor") == "" {
			writeJSON(w, map[string]any{
				"ok": true,
				"messages": []map[string]any{
					{"user": "U1", "text": "root", "ts": "1000000000.000001"},
					{"user": "U2", "text": "reply1", "ts": "1000000001.000001"},
				},
				"has_more":          true,
				"response_metadata": map[string]any{"next_cursor": "cursor2"},
			})
		} else {
			writeJSON(w, map[string]any{
				"ok": true,
				"messages": []map[string]any{
					{"user": "U3", "text": "reply2", "ts": "1000000002.000001"},
				},
				"has_more": false,
			})
		}
	}))

	got, err := c.GetThread("C123", "1000000000.000001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 messages across 2 pages, got %d", len(got))
	}
	if callCount != 2 {
		t.Errorf("expected 2 HTTP calls, got %d", callCount)
	}
}

// Ensure the cookie transport is used for conversation calls, not just auth.
func TestGetMessage_cookieInjected(t *testing.T) {
	var gotCookie string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		writeJSON(w, map[string]any{
			"ok": true,
			"messages": []map[string]any{
				{"user": "U1", "text": "hi", "ts": "1000000000.000001"},
			},
		})
	}))

	_, _ = c.GetMessage("C123", "1000000000.000001")
	if gotCookie != "d=xoxd-test" {
		t.Errorf("Cookie header: got %q, want %q", gotCookie, "d=xoxd-test")
	}
}

func TestGetMessage_replyUsers(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"ok": true,
			"messages": []map[string]any{
				{
					"type":         "message",
					"user":         "U1",
					"text":         "thread root",
					"ts":           "1.0",
					"reply_count":  2,
					"reply_users":  []string{"U2", "U3"},
				},
			},
		})
	}))

	got, err := c.GetMessage("C123", "1.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.ReplyUsers) != 2 {
		t.Fatalf("ReplyUsers: got %v, want [U2 U3]", got.ReplyUsers)
	}
	if got.ReplyUsers[0] != "U2" || got.ReplyUsers[1] != "U3" {
		t.Errorf("ReplyUsers: got %v, want [U2 U3]", got.ReplyUsers)
	}
}

func TestGetMessage_filesAndReactions(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"ok": true,
			"messages": []map[string]any{
				{
					"type": "message",
					"user": "U1",
					"text": "look at this",
					"ts":   "1.0",
					"files": []map[string]any{
						{
							"id":          "F001",
							"name":        "image.png",
							"title":       "Image",
							"pretty_type": "PNG",
							"mimetype":    "image/png",
							"permalink":   "https://files.slack.com/files/image.png",
							"url_private": "https://files.slack.com/files-pri/image.png",
						},
					},
					"reactions": []map[string]any{
						{"name": "thumbsup", "count": 3, "users": []string{"U2", "U3", "U4"}},
						{"name": "ok_hand",  "count": 1, "users": []string{"U2"}},
					},
				},
			},
		})
	}))

	got, err := c.GetMessage("C123", "1.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Files
	if len(got.Files) != 1 {
		t.Fatalf("Files: got %d, want 1", len(got.Files))
	}
	f := got.Files[0]
	if f.ID != "F001" {
		t.Errorf("Files[0].ID: got %q, want %q", f.ID, "F001")
	}
	if f.Name != "image.png" {
		t.Errorf("Files[0].Name: got %q, want %q", f.Name, "image.png")
	}
	if f.PrettyType != "PNG" {
		t.Errorf("Files[0].PrettyType: got %q, want %q", f.PrettyType, "PNG")
	}
	if f.Permalink != "https://files.slack.com/files/image.png" {
		t.Errorf("Files[0].Permalink: got %q", f.Permalink)
	}

	// Reactions
	if len(got.Reactions) != 2 {
		t.Fatalf("Reactions: got %d, want 2", len(got.Reactions))
	}
	if got.Reactions[0].Name != "thumbsup" {
		t.Errorf("Reactions[0].Name: got %q, want %q", got.Reactions[0].Name, "thumbsup")
	}
	if got.Reactions[0].Count != 3 {
		t.Errorf("Reactions[0].Count: got %d, want 3", got.Reactions[0].Count)
	}
	if len(got.Reactions[0].Users) != 3 {
		t.Errorf("Reactions[0].Users: got %v, want [U2 U3 U4]", got.Reactions[0].Users)
	}
	if got.Reactions[1].Name != "ok_hand" {
		t.Errorf("Reactions[1].Name: got %q, want %q", got.Reactions[1].Name, "ok_hand")
	}
}

func TestGetMessage_noFilesNoReactions(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"ok": true,
			"messages": []map[string]any{
				{"type": "message", "user": "U1", "text": "plain", "ts": "1.0"},
			},
		})
	}))

	got, err := c.GetMessage("C123", "1.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Files) != 0 {
		t.Errorf("Files: expected empty, got %v", got.Files)
	}
	if len(got.Reactions) != 0 {
		t.Errorf("Reactions: expected empty, got %v", got.Reactions)
	}
}

func TestGetFileInfo_success(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/files.info" {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		if r.FormValue("file") != "F0B3HRU6ZA7" {
			http.Error(w, "bad file id", http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{
			"ok": true,
			"file": map[string]any{
				"id":          "F0B3HRU6ZA7",
				"name":        "image.png",
				"title":       "Image",
				"pretty_type": "PNG",
				"mimetype":    "image/png",
				"permalink":   "https://myorg.slack.com/files/WUSER/F0B3HRU6ZA7/image.png",
				"url_private": "https://files.slack.com/files-pri/T0/F0B3HRU6ZA7/image.png",
			},
		})
	}))

	got, err := c.GetFileInfo("F0B3HRU6ZA7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != "F0B3HRU6ZA7" {
		t.Errorf("ID: got %q, want %q", got.ID, "F0B3HRU6ZA7")
	}
	if got.Name != "image.png" {
		t.Errorf("Name: got %q, want %q", got.Name, "image.png")
	}
	if got.URLPrivate != "https://files.slack.com/files-pri/T0/F0B3HRU6ZA7/image.png" {
		t.Errorf("URLPrivate: got %q", got.URLPrivate)
	}
	if got.Mimetype != "image/png" {
		t.Errorf("Mimetype: got %q, want %q", got.Mimetype, "image/png")
	}
}

func TestGetFileInfo_apiError(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": false, "error": "file_not_found"})
	}))
	_, err := c.GetFileInfo("FBAD")
	if err == nil {
		t.Fatal("expected error for ok=false, got nil")
	}
}

func TestGetChannelName_success(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.info" {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		if r.FormValue("channel") != "C012ABC" {
			http.Error(w, "bad channel", http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{
			"ok": true,
			"channel": map[string]any{
				"id":   "C012ABC",
				"name": "general",
			},
		})
	}))

	name, err := c.GetChannelName(t.Context(), "C012ABC")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "general" {
		t.Errorf("Name: got %q, want %q", name, "general")
	}
}

func TestGetChannelName_apiError(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": false, "error": "channel_not_found"})
	}))

	_, err := c.GetChannelName(t.Context(), "CBAD")
	if err == nil {
		t.Fatal("expected error for ok=false, got nil")
	}
}

func TestGetReply_success(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.replies" {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		writeJSON(w, map[string]any{
			"ok": true,
			"messages": []map[string]any{
				{"user": "U_ROOT", "text": "root msg", "ts": "1000000000.000001"},
				{"user": "U_REPLIER", "text": "the reply", "ts": "1000000002.000001"},
			},
			"has_more": false,
		})
	}))

	got, err := c.GetReply("C123", "1000000000.000001", "1000000002.000001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.User != "U_REPLIER" {
		t.Errorf("User: got %q, want U_REPLIER", got.User)
	}
	if got.Text != "the reply" {
		t.Errorf("Text: got %q, want 'the reply'", got.Text)
	}
}

func TestGetReply_notFound(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"ok": true,
			// only root, no matching reply ts
			"messages": []map[string]any{
				{"user": "U_ROOT", "text": "root", "ts": "1000000000.000001"},
			},
			"has_more": false,
		})
	}))

	_, err := c.GetReply("C123", "1000000000.000001", "9999999999.000001")
	if err == nil {
		t.Fatal("expected error when reply ts not found")
	}
}

func TestGetChannelName_dm(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.info" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, map[string]any{
			"ok": true,
			"channel": map[string]any{
				"id":    "D012ABC",
				"name":  "",   // DMs have no name
				"is_im": true,
				"user":  "W4UDRQJNR", // peer user ID
			},
		})
	}))

	name, err := c.GetChannelName(t.Context(), "D012ABC")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "W4UDRQJNR" {
		t.Errorf("Name: got %q, want W4UDRQJNR (peer user ID for DM)", name)
	}
}

func TestGetHistory_basicSuccess(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.history" {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		if r.FormValue("channel") != "C999" {
			http.Error(w, "bad channel", http.StatusBadRequest)
			return
		}
		if r.FormValue("limit") != "10" {
			http.Error(w, "bad limit", http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{
			"ok": true,
			"messages": []map[string]any{
				{"type": "message", "user": "U1", "text": "newest", "ts": "1000000002.000001"},
				{"type": "message", "user": "U2", "text": "older", "ts": "1000000001.000001"},
			},
			"has_more": false,
		})
	}))

	result, err := c.GetHistory("C999", HistoryParams{Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result.Messages))
	}
	if result.Messages[0].Text != "newest" {
		t.Errorf("messages[0].Text: got %q, want %q", result.Messages[0].Text, "newest")
	}
	if result.HasMore {
		t.Error("HasMore: expected false")
	}
	if result.Cursor != "" {
		t.Errorf("Cursor: expected empty, got %q", result.Cursor)
	}
}

func TestGetHistory_hasMoreAndCursor(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"ok": true,
			"messages": []map[string]any{
				{"type": "message", "user": "U1", "text": "msg1", "ts": "1000000002.000001"},
			},
			"has_more":          true,
			"response_metadata": map[string]any{"next_cursor": "dXNlcjpVMDYx"},
		})
	}))

	result, err := c.GetHistory("C999", HistoryParams{Limit: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.HasMore {
		t.Error("HasMore: expected true")
	}
	if result.Cursor != "dXNlcjpVMDYx" {
		t.Errorf("Cursor: got %q, want %q", result.Cursor, "dXNlcjpVMDYx")
	}
}

func TestGetHistory_limitsClampedTo200(t *testing.T) {
	var gotLimit string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotLimit = r.FormValue("limit")
		writeJSON(w, map[string]any{"ok": true, "messages": []any{}, "has_more": false})
	}))

	_, _ = c.GetHistory("C999", HistoryParams{Limit: 500})
	if gotLimit != "200" {
		t.Errorf("limit sent to API: got %q, want %q", gotLimit, "200")
	}
}

func TestGetHistory_oldestLatestCursorPassthrough(t *testing.T) {
	var gotOldest, gotLatest, gotCursor string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotOldest = r.FormValue("oldest")
		gotLatest = r.FormValue("latest")
		gotCursor = r.FormValue("cursor")
		writeJSON(w, map[string]any{"ok": true, "messages": []any{}, "has_more": false})
	}))

	_, _ = c.GetHistory("C999", HistoryParams{
		Limit:  5,
		Oldest: "1000000000.000000",
		Latest: "1999999999.000000",
		Cursor: "testcursor",
	})
	if gotOldest != "1000000000.000000" {
		t.Errorf("oldest: got %q, want %q", gotOldest, "1000000000.000000")
	}
	if gotLatest != "1999999999.000000" {
		t.Errorf("latest: got %q, want %q", gotLatest, "1999999999.000000")
	}
	if gotCursor != "testcursor" {
		t.Errorf("cursor: got %q, want %q", gotCursor, "testcursor")
	}
}

func TestGetHistory_apiError(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": false, "error": "channel_not_found"})
	}))

	_, err := c.GetHistory("CBAD", HistoryParams{Limit: 5})
	if err == nil {
		t.Fatal("expected error for ok=false, got nil")
	}
}
