package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
)

var accountLoginPattern = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9_.@+-]{0,253}$")

func adminDSNValid(dsn string) bool {
	u, err := url.Parse(dsn)
	return err == nil && (u.Scheme == "postgres" || u.Scheme == "postgresql") && u.Hostname() != "" && u.User != nil && u.User.Username() == "browseai_admin" && u.Query().Get("sslmode") == "verify-full"
}

func accountCommand(ctx context.Context, db *sql.DB, args []string, input io.Reader, output io.Writer) error {
	if len(args) < 2 || !accountLoginPattern.MatchString(args[1]) {
		return errors.New("usage: account provision LOGIN WORKSPACE | account enable|disable|reset-password LOGIN")
	}
	action, login := args[0], args[1]
	switch action {
	case "provision":
		if len(args) != 3 || !validProjectName(args[2]) {
			return errResourceInvalid
		}
	case "enable", "disable", "reset-password":
		if len(args) != 2 {
			return errResourceInvalid
		}
	default:
		return errResourceInvalid
	}
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT current_user='browseai_admin' AND NOT r.rolsuper AND NOT r.rolbypassrls
AND NOT has_table_privilege(current_user,'browseai.users','SELECT')
AND NOT has_table_privilege(current_user,'browseai.projects','SELECT')
AND NOT has_table_privilege(current_user,'browseai.auth_sessions','SELECT')
FROM pg_catalog.pg_roles r WHERE r.rolname=current_user`).Scan(&ok)
	if err != nil || !ok {
		return errors.New("invalid account operator role")
	}
	if action == "provision" || action == "reset-password" {
		raw, err := io.ReadAll(io.LimitReader(input, 1027))
		if err != nil || len(raw) > 1026 {
			return errors.New("invalid password input")
		}
		password := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
		if strings.ContainsAny(password, "\r\n") {
			return errors.New("one password line required")
		}
		hash, err := hashPassword(password)
		if err != nil {
			return err
		}
		if action == "provision" {
			var id string
			if err := db.QueryRowContext(ctx, "SELECT browseai.provision_account($1,$2,$3)::text", login, args[2], hash).Scan(&id); err != nil {
				return errors.New("account provisioning rejected")
			}
			_, err = fmt.Fprintln(output, "account created:", id)
			return err
		}
		err = db.QueryRowContext(ctx, "SELECT browseai.reset_account_password($1,$2)", login, hash).Scan(&ok)
		if err != nil || !ok {
			return errors.New("password reset rejected")
		}
	} else {
		err = db.QueryRowContext(ctx, "SELECT browseai.set_account_enabled($1,$2)", login, action == "enable").Scan(&ok)
		if err != nil || !ok {
			return errors.New("account update rejected or already in requested state")
		}
	}
	_, err = fmt.Fprintln(output, "account updated")
	return err
}
