package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func newBrowserNodeServer(addr string, db *sql.DB) (*http.Server, error) {
	host, port, err := net.SplitHostPort(addr)
	n, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || n < 1 || n > 65535 || net.ParseIP(host) == nil || db == nil {
		return nil, errBrowserNode
	}
	return &http.Server{
		Addr: addr, Handler: browserDatabaseHeartbeatHandler(db),
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13},
		ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second,
		WriteTimeout: 8 * time.Second, IdleTimeout: 15 * time.Second,
		MaxHeaderBytes: 8192,
	}, nil
}

// The operator supplies a protected destination for newly generated secrets.
// Never print credentials or overwrite a previous credential file.
func browserIdentityCommand(ctx context.Context, db *sql.DB, args []string, input io.Reader) error {
	if len(args) != 5 || input == nil || !resourceUUID.MatchString(args[1]) || !resourceUUID.MatchString(args[3]) {
		return errBrowserNode
	}
	action, id, operation, destination := args[0], args[1], args[3], args[4]
	generation, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil || generation <= 0 || strconv.FormatInt(generation, 10) != args[2] ||
		(action != "register" && action != "rotate" && action != "revoke") ||
		(action == "register" && generation != 1) || (action == "revoke" && destination != "-") ||
		(action != "revoke" && (destination == "" || destination == "-")) {
		return errBrowserNode
	}
	raw, err := io.ReadAll(io.LimitReader(input, 1025))
	if err != nil || len(raw) > 1024 || strings.TrimSuffix(string(raw), "\n") != "APPROVE "+strings.Join(args[:4], " ") {
		return errBrowserNode
	}
	if browserOperatorReady(ctx, db) != nil {
		return errBrowserNode
	}
	var credential any
	if action != "revoke" {
		token, err := newBrowserCredential()
		if err != nil {
			return err
		}
		digest, err := browserCredentialDigest(token)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return errBrowserNode
		}
		_, writeErr := io.WriteString(f, token)
		syncErr := f.Sync()
		closeErr := f.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			return errBrowserNode
		}
		credential = digest[:]
	}
	// Retain the protected file on ambiguous DB failure for reconciliation.
	// An uncommitted credential cannot authenticate.
	var ok bool
	err = db.QueryRowContext(ctx, "SELECT browseai.manage_browser_identity($1,$2,$3,$4,$5)", id, generation, credential, operation, action).Scan(&ok)
	if err != nil || !ok {
		return errBrowserNode
	}
	return nil
}
