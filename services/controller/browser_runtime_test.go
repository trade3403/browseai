package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestBrowserRuntimeConfiguration(t *testing.T) {
	db, err := sql.Open("postgres", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, addr := range []string{"", ":8443", "localhost:8443", "127.0.0.1:0"} {
		if _, err := newBrowserNodeServer(addr, db); err == nil {
			t.Fatal("invalid listener accepted")
		}
	}
	s, err := newBrowserNodeServer("127.0.0.1:8443", db)
	if err != nil || s.TLSConfig.MinVersion != 0x0304 || s.Handler == nil {
		t.Fatal("invalid TLS configuration", err)
	}
	if browserIdentityCommand(context.Background(), nil, nil, strings.NewReader("")) == nil {
		t.Fatal("invalid identity command accepted")
	}
}
