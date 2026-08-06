package slack

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// redirectTransport rewrites every request to the given base URL, preserving
// the path. Used to redirect hardcoded production URLs (e.g. edgeapi.slack.com)
// to a local httptest.Server.
type redirectTransport struct {
	base string // e.g. "http://127.0.0.1:PORT"
}

func (rt redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	req2.URL.Scheme = "http"
	req2.URL.Host = rt.base[len("http://"):]
	return http.DefaultTransport.RoundTrip(req2)
}

// TestGetUserByHandle_cacheHit verifies that FindByName is used when the handle
// is already in the cache and no API call is made.
func TestGetUserByHandle_cacheHit(t *testing.T) {
	apiCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalled = true
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewClientWithHTTP("xoxc-test", "xoxd-test", &http.Client{
		Transport: redirectTransport{base: srv.URL},
	})
	cache := NewUserCacheFromMap("test.slack.com", map[string]CachedUser{
		"W1": {ID: "W1", Name: "u100001", DisplayName: "Alice Example"},
	})
	cache.client = client

	u, err := cache.GetUserByHandle("u100001", "E123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.DisplayName != "Alice Example" {
		t.Errorf("DisplayName = %q, want %q", u.DisplayName, "Alice Example")
	}
	if apiCalled {
		t.Error("API should not be called on a cache hit")
	}
}

// TestGetUserByHandle_apiMiss verifies that SearchUsers is called when the
// handle is not in the cache, and that a matching result is stored in-memory.
func TestGetUserByHandle_apiMiss(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := edgeSearchResponse{
			OK: true,
			Results: []edgeSearchUser{
				{ID: "W2", Name: "u100002", Profile: edgeSearchProfile{DisplayName: "Bob Example"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewClientWithHTTP("xoxc-test", "xoxd-test", &http.Client{
		Transport: redirectTransport{base: srv.URL},
	})
	cache := NewUserCacheFromMap("test.slack.com", map[string]CachedUser{})
	cache.client = client

	u, err := cache.GetUserByHandle("u100002", "E123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.DisplayName != "Bob Example" {
		t.Errorf("DisplayName = %q, want %q", u.DisplayName, "Bob Example")
	}
	// Result should be in-memory cache now.
	if _, ok := cache.users["W2"]; !ok {
		t.Error("result should be stored in users map after API hit")
	}
}

// TestGetUserByHandle_notFound verifies an error is returned when the API
// returns no result matching the requested handle.
func TestGetUserByHandle_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := edgeSearchResponse{OK: true, Results: []edgeSearchUser{}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewClientWithHTTP("xoxc-test", "xoxd-test", &http.Client{
		Transport: redirectTransport{base: srv.URL},
	})
	cache := NewUserCacheFromMap("test.slack.com", map[string]CachedUser{})
	cache.client = client

	_, err := cache.GetUserByHandle("nobody", "E123")
	if err == nil {
		t.Fatal("expected error for unknown handle, got nil")
	}
}

// TestGetUserByHandle_noEnterpriseID verifies an error is returned immediately
// when no enterpriseID is provided (no API call should be made).
func TestGetUserByHandle_noEnterpriseID(t *testing.T) {
	apiCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalled = true
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewClientWithHTTP("xoxc-test", "xoxd-test", &http.Client{
		Transport: redirectTransport{base: srv.URL},
	})
	cache := NewUserCacheFromMap("test.slack.com", map[string]CachedUser{})
	cache.client = client

	_, err := cache.GetUserByHandle("u100001", "") // empty enterpriseID
	if err == nil {
		t.Fatal("expected error when enterpriseID is empty, got nil")
	}
	if apiCalled {
		t.Error("API should not be called when enterpriseID is empty")
	}
}
