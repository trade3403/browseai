package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
)

var errUnauthenticated = errors.New("unauthenticated")

type sessionDigest [32]byte

// Session storage takes only digests. Raw bearer tokens never enter the database.
type sessionStore interface {
	issue(context.Context, principal, sessionDigest) (bool, error)
	resolve(context.Context, sessionDigest) (principal, error)
	revoke(context.Context, sessionDigest) error
}

type sessionService struct {
	store sessionStore
}

func digestSession(token string) (sessionDigest, error) {
	if len(token) != 43 {
		return sessionDigest{}, errUnauthenticated
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) != 32 {
		return sessionDigest{}, errUnauthenticated
	}
	return sha256.Sum256(raw), nil
}

func validSessionPrincipal(p principal) bool {
	return p.userID != "" && p.tenantID != "" && (p.role == roleUser || p.role == rolePlatformAdmin)
}

// issueVerified must be called only AFTER a trusted credential verifier
// authenticates the user and selects an authorized membership.
// No HTTP route exposes this operation yet.
func (s sessionService) issueVerified(ctx context.Context, p principal) (string, error) {
	if !validSessionPrincipal(p) {
		return "", errUnauthenticated
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw[:])
	ok, err := s.store.issue(ctx, p, digest)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errUnauthenticated
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func (s sessionService) authenticate(ctx context.Context, token string) (principal, error) {
	digest, err := digestSession(token)
	if err != nil {
		return principal{}, err
	}
	p, err := s.store.resolve(ctx, digest)
	if err != nil {
		return principal{}, err
	}
	if !validSessionPrincipal(p) {
		return principal{}, errUnauthenticated
	}
	return p, nil
}

func (s sessionService) logout(ctx context.Context, token string) error {
	digest, err := digestSession(token)
	if err != nil {
		return err
	}
	return s.store.revoke(ctx, digest)
}

// sqlSessionStore uses a dedicated browseai_auth connection pool; the task
// pool must not have permission to call these functions. Driver/bootstrap
// and credential verification are separate work items.
type sqlSessionStore struct {
	db *sql.DB
}

func (s sqlSessionStore) issue(ctx context.Context, p principal, digest sessionDigest) (bool, error) {
	var issued bool
	err := s.db.QueryRowContext(ctx, "SELECT browseai.issue_session($1::uuid,$2::uuid,$3::bytea)", p.userID, p.tenantID, digest[:]).Scan(&issued)
	return issued, err
}

func (s sqlSessionStore) resolve(ctx context.Context, digest sessionDigest) (principal, error) {
	var p principal
	err := s.db.QueryRowContext(ctx, "SELECT user_id::text,tenant_id::text,role FROM browseai.resolve_session($1::bytea)", digest[:]).Scan(&p.userID, &p.tenantID, &p.role)
	if errors.Is(err, sql.ErrNoRows) {
		return principal{}, errUnauthenticated
	}
	if err != nil {
		return principal{}, err
	}
	return p, nil
}

func (s sqlSessionStore) revoke(ctx context.Context, digest sessionDigest) error {
	var revoked bool
	return s.db.QueryRowContext(ctx, "SELECT browseai.revoke_session($1::bytea)", digest[:]).Scan(&revoked)
}
