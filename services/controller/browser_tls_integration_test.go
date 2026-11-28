package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBrowserHeartbeatTLSRace(t *testing.T) {
	if os.Getenv("BROWSEAI_TEST_OWNER_DSN") == "" {
		t.Skip("requires PostgreSQL integration fixture")
	}
	operator, err := sql.Open("postgres", os.Getenv("BROWSEAI_TEST_BROWSER_OPERATOR_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer operator.Close()
	node, err := sql.Open("postgres", os.Getenv("BROWSEAI_TEST_BROWSER_NODE_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const id = "00000000-0000-4000-8000-000000008021"
	const op1 = "00000000-0000-4000-8000-000000008022"
	const op2 = "00000000-0000-4000-8000-000000008023"
	const op3 = "00000000-0000-4000-8000-000000008024"
	dir := t.TempDir()
	command := func(action, generation, operation, destination string) {
		t.Helper()
		args := []string{action, id, generation, operation, destination}
		if err := browserIdentityCommand(ctx, operator, args, strings.NewReader("APPROVE "+strings.Join(args[:4], " "))); err != nil {
			t.Fatal("identity command failed", err)
		}
	}
	first := filepath.Join(dir, "first")
	command("register", "1", op1, first)
	raw, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(first)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("credential permissions rejected", err)
	}
	config, err := newBrowserNodeServer("127.0.0.1:8443", node)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(config.Handler)
	server.TLS = config.TLSConfig.Clone()
	server.StartTLS()
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	send := func(token string, generation, sequence int) int {
		body := fmt.Sprintf(`{"node_id":%q,"generation":%d,"sequence":%d,"capacity":4,"occupied":1}`, id, generation, sequence)
		r, err := http.NewRequestWithContext(ctx, "POST", server.URL+"/node/heartbeat", strings.NewReader(body))
		if err != nil {
			return 0
		}
		r.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(r)
		if err != nil {
			return 0
		}
		defer resp.Body.Close()
		if resp.TLS == nil || resp.TLS.Version != tls.VersionTLS13 {
			return 0
		}
		return resp.StatusCode
	}
	// The client must reject an untrusted certificate and the listener must
	// reject a client capped at TLS 1.2, even with the test CA installed.
	untrusted := &http.Client{Timeout: 3 * time.Second}
	if resp, err := untrusted.Get(server.URL); err == nil {
		resp.Body.Close()
		t.Fatal("untrusted certificate accepted")
	}
	transport := client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.MaxVersion = tls.VersionTLS12
	old := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	defer transport.CloseIdleConnections()
	if resp, err := old.Get(server.URL); err == nil {
		resp.Body.Close()
		t.Fatal("TLS 1.2 accepted")
	}
	var wg sync.WaitGroup
	results := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- send(string(raw), 1, 1)
		}()
	}
	wg.Wait()
	close(results)
	wins := 0
	for status := range results {
		if status == 204 {
			wins++
		} else if status != 403 {
			t.Fatal("unexpected race status", status)
		}
	}
	if wins != 1 {
		t.Fatal("heartbeat must commit exactly once", wins)
	}
	second := filepath.Join(dir, "second")
	command("rotate", "2", op2, second)
	next, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if send(string(raw), 1, 2) != 403 || send(string(next), 2, 1) != 204 {
		t.Fatal("TLS rotation not enforced")
	}
	command("revoke", "2", op3, "-")
	if send(string(next), 2, 2) != 403 {
		t.Fatal("TLS revocation not enforced")
	}
}
