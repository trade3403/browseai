package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"strings"
	"time"
)

const browserCredentialPrefix = "banode_"

func newBrowserCredential() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", errBrowserNode
	}
	return browserCredentialPrefix + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func browserCredentialDigest(token string) ([32]byte, error) {
	if !strings.HasPrefix(token, browserCredentialPrefix) || len(token) != len(browserCredentialPrefix)+43 {
		return [32]byte{}, errBrowserNode
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token[len(browserCredentialPrefix):])
	if err != nil || len(raw) != 32 {
		return [32]byte{}, errBrowserNode
	}
	return sha256.Sum256([]byte(token)), nil
}

func persistBrowserHeartbeat(ctx context.Context, db *sql.DB, id, token string, generation, sequence int64, capacity, occupied int) error {
	digest, err := browserCredentialDigest(token)
	if err != nil || db == nil || !resourceUUID.MatchString(id) || generation <= 0 || sequence <= 0 ||
		capacity < 1 || capacity > 256 || occupied < 0 || occupied > capacity {
		return errBrowserNode
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var ok bool
	if err := db.QueryRowContext(ctx, "SELECT browseai.record_browser_heartbeat($1,$2,$3,$4,$5,$6)", id, generation, digest[:], sequence, capacity, occupied).Scan(&ok); err != nil || !ok {
		return errBrowserNode
	}
	return nil
}
