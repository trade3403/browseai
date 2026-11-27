package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestGitHubExecutionGrantStore(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux durable node journal only")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := newGitHubExecutionGrantStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	e := githubExecutionStatement{Tenant: "tenant", Repository: 12, Workflow: 34, Event: "push", Run: 56, Attempt: 1}
	var wg sync.WaitGroup
	var winners atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Independent store objects simulate multiple node instances.
			store, err := newGitHubExecutionGrantStore(dir)
			if err != nil {
				t.Error(err)
				return
			}
			if store.claim(e, []byte("authorized")) == nil {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatal("expected exactly one durable admission", winners.Load())
	}
	if err := s.claim(e, []byte("changed authorization")); err == nil {
		t.Fatal("changed authorization reopened run")
	}
	e.Attempt++
	if err := s.claim(e, []byte("new attempt")); err != nil {
		t.Fatal(err)
	}
	e.Attempt++
	id, err := githubExecutionGrantID(e)
	if err != nil {
		t.Fatal(err)
	}
	// An interrupted process may leave a zero-byte marker. It must block.
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatal("unexpected journal", err, len(entries))
	}
	path := filepath.Join(dir, fmtGrantID(id))
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.claim(e, []byte("new attempt")); err == nil {
		t.Fatal("partial marker reopened execution")
	}
	if _, err := newGitHubExecutionGrantStore("relative"); err == nil {
		t.Fatal("relative journal accepted")
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	e.Attempt++
	if err := s.claim(e, []byte("new attempt")); err == nil {
		t.Fatal("insecure directory accepted")
	}
}

func fmtGrantID(id [32]byte) string {
	return hex.EncodeToString(id[:]) + ".claimed"
}
