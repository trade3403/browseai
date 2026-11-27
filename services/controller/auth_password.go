package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"

	"golang.org/x/crypto/argon2"
)

const passwordPrefix = "$argon2id$v=19$m=65536,t=3,p=1$"

func passwordKey(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, 3, 65536, 1, 32)
}

// Hash generation is for trusted offline provisioning, never public registration.
func hashPassword(password string) (string, error) {
	if len(password) < 12 || len(password) > 1024 {
		return "", errors.New("password must contain 12 to 1024 bytes")
	}
	var salt [16]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return passwordPrefix + enc.EncodeToString(salt[:]) + "$" + enc.EncodeToString(passwordKey(password, salt[:])), nil
}

// Only the fixed audited parameter set is accepted, so corrupted hashes cannot
// request arbitrary CPU or memory. Unknown users perform the same KDF.
func checkPassword(password, stored string) bool {
	if len(password) == 0 || len(password) > 1024 {
		return false
	}
	salt, expected := make([]byte, 16), make([]byte, 32)
	valid := false
	if len(stored) == len(passwordPrefix)+22+1+43 && strings.HasPrefix(stored, passwordPrefix) {
		parts := strings.Split(strings.TrimPrefix(stored, passwordPrefix), "$")
		if len(parts) == 2 {
			a, errA := base64.RawStdEncoding.Strict().DecodeString(parts[0])
			b, errB := base64.RawStdEncoding.Strict().DecodeString(parts[1])
			if errA == nil && errB == nil && len(a) == 16 && len(b) == 32 {
				salt, expected, valid = a, b, true
			}
		}
	}
	match := subtle.ConstantTimeCompare(passwordKey(password, salt), expected)
	return valid && match == 1
}

type sqlCredentialVerifier struct {
	db *sql.DB
}

func (v sqlCredentialVerifier) verifiedCandidate(ctx context.Context, login, password string) (principal, string, error) {
	var p principal
	var stored string
	err := v.db.QueryRowContext(ctx, "SELECT user_id::text,tenant_id::text,role,password_hash FROM browseai.login_candidate($1)", login).Scan(&p.userID, &p.tenantID, &p.role, &stored)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return principal{}, "", err
	}
	if !checkPassword(password, stored) || err != nil || !validSessionPrincipal(p) {
		return principal{}, "", errUnauthenticated
	}
	return p, stored, nil
}

func (v sqlCredentialVerifier) verify(ctx context.Context, login, password string) (principal, error) {
	p, _, err := v.verifiedCandidate(ctx, login, password)
	return p, err
}

// The real HTTP login binds token issuance to the exact verified password
// hash. A concurrent reset/disable cannot turn a stale proof into a session.
func (v sqlCredentialVerifier) verifyAndIssue(ctx context.Context, login, password string) (string, error) {
	p, stored, err := v.verifiedCandidate(ctx, login, password)
	if err != nil {
		return "", err
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw[:])
	var issued bool
	err = v.db.QueryRowContext(ctx, "SELECT browseai.issue_verified_session($1::uuid,$2::uuid,$3::bytea,$4)", p.userID, p.tenantID, digest[:], stored).Scan(&issued)
	if err != nil {
		return "", err
	}
	if !issued {
		return "", errUnauthenticated
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
