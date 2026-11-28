package main

import (
	"context"
	"database/sql"
	"encoding/hex"
	"io"
	"strconv"
	"strings"
	"time"
)

type githubVerifierRegistration struct {
	binding    githubBinding
	verifierID string
	keyID      string
	publicKey  []byte
	from       time.Time
	until      time.Time
	approvalID string
}

func (v githubVerifierRegistration) valid() bool {
	return resourceUUID.MatchString(v.binding.tenantID) && v.binding.installationID > 0 &&
		v.binding.repositoryID > 0 && resourceUUID.MatchString(v.verifierID) &&
		resourceUUID.MatchString(v.keyID) && resourceUUID.MatchString(v.approvalID) &&
		githubStrictEd25519Key(v.publicKey) && !v.from.IsZero() && v.until.After(v.from) &&
		v.until.Sub(v.from) <= 90*24*time.Hour
}

// The operator must independently verify ownership of the signing key and
// approve the exact scope. The approval reference is not a proof of ownership.
func githubVerifierRegistrationInput(args []string, input io.Reader) (githubVerifierRegistration, error) {
	var v githubVerifierRegistration
	if len(args) != 9 || input == nil {
		return v, errGitHubAuthorization
	}
	v.binding.tenantID = args[0]
	var err error
	v.binding.installationID, err = strconv.ParseInt(args[1], 10, 64)
	if err != nil || strconv.FormatInt(v.binding.installationID, 10) != args[1] {
		return v, errGitHubAuthorization
	}
	v.binding.repositoryID, err = strconv.ParseInt(args[2], 10, 64)
	if err != nil || strconv.FormatInt(v.binding.repositoryID, 10) != args[2] {
		return v, errGitHubAuthorization
	}
	v.verifierID, v.keyID, v.approvalID = args[3], args[4], args[8]
	v.publicKey, err = hex.DecodeString(args[5])
	if err != nil || hex.EncodeToString(v.publicKey) != args[5] {
		return v, errGitHubAuthorization
	}
	v.from, err = time.Parse(time.RFC3339, args[6])
	if err != nil || v.from.UTC().Format(time.RFC3339) != args[6] {
		return v, errGitHubAuthorization
	}
	v.until, err = time.Parse(time.RFC3339, args[7])
	if err != nil || v.until.UTC().Format(time.RFC3339) != args[7] || !v.valid() {
		return v, errGitHubAuthorization
	}
	raw, err := io.ReadAll(io.LimitReader(input, 1025))
	if err != nil || len(raw) > 1024 || strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r") != "APPROVE "+strings.Join(args, " ") {
		return githubVerifierRegistration{}, errGitHubAuthorization
	}
	return v, nil
}

// Apply strict key checks before the SQL registration call. Role checks and
// registration execute in the same SQL statement. The legacy SQL entry point
// remains an operator-only trust boundary; it does not perform curve math.
func registerGitHubVerifier(ctx context.Context, db *sql.DB, v githubVerifierRegistration) error {
	if db == nil || ctx.Err() != nil || !v.valid() {
		return errGitHubAuthorization
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT CASE WHEN current_user='browseai_verifier_operator'
AND session_user=current_user AND NOT r.rolsuper AND NOT r.rolbypassrls
AND NOT r.rolcreaterole AND NOT r.rolcreatedb AND NOT r.rolreplication
AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid)
AND NOT has_function_privilege(current_user,'browseai.resolve_github_verifier(uuid,bigint,bigint,uuid,uuid,timestamptz)','EXECUTE')
AND NOT has_function_privilege(current_user,'browseai.insert_github_ci_evidence(bytea,bytea,timestamptz)','EXECUTE')
AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname='browseai' AND c.relkind IN ('r','p','v','m','f')
AND has_table_privilege(current_user,c.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER'))
THEN browseai.register_github_verifier($1,$2,$3,$4,$5,$6,$7,$8,$9) ELSE false END
FROM pg_catalog.pg_roles r WHERE r.rolname=current_user`,
		v.binding.tenantID, v.binding.installationID, v.binding.repositoryID, v.verifierID, v.keyID,
		v.publicKey, v.from, v.until, v.approvalID).Scan(&ok)
	if err != nil || !ok || ctx.Err() != nil {
		return errGitHubAuthorization
	}
	return nil
}
