package main

import (
	"context"
	"crypto/tls"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserHeartbeatTransport(t *testing.T) {
	token, err := newBrowserCredential()
	if err != nil {
		t.Fatal(err)
	}
	other, err := newBrowserCredential()
	if err != nil || token == other {
		t.Fatal("credential generation failed")
	}
	digest, err := browserCredentialDigest(token)
	if err != nil || digest == ([32]byte{}) {
		t.Fatal("credential digest failed")
	}
	const id = "00000000-0000-4000-8000-000000000001"
	const body = `{"node_id":"00000000-0000-4000-8000-000000000001","generation":1,"sequence":1,"capacity":4,"occupied":1}`
	calls := 0
	h := browserHeartbeatHandler(func(_ context.Context, got, secret string, generation, sequence int64, capacity, occupied int) error {
		calls++
		if got != id || secret != token || generation != 1 || sequence != 1 || capacity != 4 || occupied != 1 {
			return errBrowserNode
		}
		return nil
	})
	for _, tc := range []struct {
		secure bool
		token  string
		body   string
		status int
	}{
		{false, token, body, 403},
		{true, "invalid", body, 403},
		{true, token, body + "{}", 400},
		{true, token, strings.Repeat("x", 3000), 400},
		{true, token, strings.Replace(body, "\"occupied\":1", "\"occupied\":5", 1), 400},
		{true, token, body, 204},
	} {
		r := httptest.NewRequest("POST", "/node/heartbeat", strings.NewReader(tc.body))
		if tc.secure {
			r.TLS = &tls.ConnectionState{}
		}
		r.Header.Set("Authorization", "Bearer "+tc.token)
		r.Header.Set("X-Forwarded-Proto", "https")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatal("heartbeat status", w.Code, tc.status)
		}
	}
	if calls != 1 || persistBrowserHeartbeat(context.Background(), nil, id, token, 1, 1, 4, 1) == nil {
		t.Fatal("invalid heartbeat reached persistence")
	}
}
