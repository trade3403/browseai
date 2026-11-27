package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func githubOperatorDSNValid(dsn string) bool {
	return githubRoleDSNValid(dsn, "browseai_github_operator")
}

func githubRoleDSNValid(dsn, role string) bool {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.User == nil || u.User.Username() != role {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q["sslmode"]) != 1 || q.Get("sslmode") != "verify-full" {
		return false
	}
	// Prevent query parameters from overriding connection identity or TLS.
	for k, v := range q {
		if len(v) != 1 || (k != "sslmode" && k != "sslrootcert") {
			return false
		}
	}
	return true
}

func githubOperatorReady(ctx context.Context, db *sql.DB) error {
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT current_user='browseai_github_operator'
AND session_user=current_user AND NOT r.rolsuper AND NOT r.rolbypassrls
AND NOT r.rolcreaterole AND NOT r.rolcreatedb AND NOT r.rolreplication
AND has_function_privilege(current_user,'browseai.provision_github_binding(uuid,bigint,bigint,text,bigint,bigint)','EXECUTE')
AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid)
AND NOT has_table_privilege(current_user,'browseai.github_installations','SELECT,INSERT,UPDATE,DELETE,TRUNCATE')
AND NOT has_table_privilege(current_user,'browseai.github_repositories','SELECT,INSERT,UPDATE,DELETE,TRUNCATE')
AND NOT has_table_privilege(current_user,'browseai.github_binding_audit','SELECT,INSERT,UPDATE,DELETE,TRUNCATE')
AND NOT has_table_privilege(current_user,'browseai.users','SELECT,INSERT,UPDATE,DELETE,TRUNCATE')
AND NOT has_table_privilege(current_user,'browseai.projects','SELECT,INSERT,UPDATE,DELETE,TRUNCATE')
AND NOT has_table_privilege(current_user,'browseai.auth_sessions','SELECT,INSERT,UPDATE,DELETE,TRUNCATE')
FROM pg_catalog.pg_roles r WHERE r.rolname=current_user`).Scan(&ok)
	if err != nil || !ok {
		return errGitHubAuthorization
	}
	return nil
}

// Offline operator only. The exact approval statement binds the verified
// account, repository, installation and tenant; it is an attestation by a
// trusted human, not proof of ownership supplied by GitHub or the tenant.
func githubApprovalInput(args []string, input io.Reader) (githubInstallationApproval, error) {
	var a githubInstallationApproval
	if len(args) != 7 || args[0] != "bind" || !resourceUUID.MatchString(args[1]) || !repoName.MatchString(args[4]) {
		return a, errGitHubAuthorization
	}
	ids := make([]int64, 4)
	for i, index := range []int{2, 3, 5, 6} {
		n, err := strconv.ParseInt(args[index], 10, 64)
		if err != nil || n <= 0 || strconv.FormatInt(n, 10) != args[index] {
			return a, errGitHubAuthorization
		}
		ids[i] = n
	}
	raw, err := io.ReadAll(io.LimitReader(input, 1025))
	expected := "APPROVE " + strings.Join(args[1:], " ")
	if err != nil || len(raw) > 1024 || strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r") != expected {
		return a, errGitHubAuthorization
	}
	a.binding = githubBinding{args[1], ids[0], ids[1], args[4]}
	a.appID, a.accountID = ids[2], ids[3]
	return a, nil
}

func githubOperatorCommand(ctx context.Context, db *sql.DB, app *githubApp, args []string, input io.Reader, output io.Writer, now time.Time) error {
	approval, err := githubApprovalInput(args, input)
	if err != nil || db == nil || app == nil {
		return errGitHubAuthorization
	}
	if err := githubOperatorReady(ctx, db); err != nil {
		return err
	}
	if err := (githubProvisioner{db, app}).provision(ctx, approval, now); err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, "GitHub binding approved")
	return err
}
