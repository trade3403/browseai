package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
)

type mutationContextKey struct{}

var idempotencyKeyPattern = regexp.MustCompile("^[A-Za-z0-9_-]{1,128}$")

// Only authenticated resource mutations use this wrapper. The header is
// scoped to the server-derived tenant/user, not to a client identity field.
func requireMutationKey(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return r, true
	}
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || !idempotencyKeyPattern.MatchString(values[0]) {
		resourceError(w, errResourceInvalid)
		return r, false
	}
	return r.WithContext(context.WithValue(r.Context(), mutationContextKey{}, values[0])), true
}

func mutationTx[T any](ctx context.Context, db *sql.DB, p principal, action, target string, revision uint64, input any, fn func(*sql.Tx) (T, error)) (T, error) {
	key, _ := ctx.Value(mutationContextKey{}).(string)
	return tenantTx(ctx, db, p, func(tx *sql.Tx) (T, error) {
		var zero T
		if key != "" {
			encoded, err := json.Marshal([]any{action, target, revision, input})
			if err != nil {
				return zero, err
			}
			hash := sha256.Sum256(encoded)
			res, err := tx.ExecContext(ctx, "INSERT INTO browseai.idempotency_keys(tenant_id,user_id,request_key,request_hash) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING", p.tenantID, p.userID, key, hash[:])
			if err != nil {
				return zero, err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return zero, err
			}
			if n == 0 {
				var saved []byte
				var same bool
				err := tx.QueryRowContext(ctx, "SELECT request_hash=$4,result FROM browseai.idempotency_keys WHERE tenant_id=$1 AND user_id=$2 AND request_key=$3", p.tenantID, p.userID, key, hash[:]).Scan(&same, &saved)
				if err != nil {
					return zero, err
				}
				if !same {
					return zero, errResourceConflict
				}
				if err := json.Unmarshal(saved, &zero); err != nil {
					return zero, err
				}
				return zero, nil
			}
		}
		value, err := fn(tx)
		if err != nil {
			return zero, err
		}
		// Store only resource IDs, action and revision in audit/events, never
		// credentials, raw request bodies, GitHub tokens or conversation data.
		switch item := any(value).(type) {
		case projectRecord:
			target, revision = item.ID, uint64(item.Revision)
		case taskRecord:
			target, revision = item.ID, item.Revision
		}
		var auditID string
		err = tx.QueryRowContext(ctx, "INSERT INTO browseai.audit_logs(tenant_id,actor_id,action,resource_id,revision) VALUES($1,$2,$3,$4,$5) RETURNING id::text", p.tenantID, p.userID, action, target, int64(revision)).Scan(&auditID)
		if err != nil {
			return zero, err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO browseai.events(tenant_id,audit_id,kind,resource_id,revision) VALUES($1,$2,$3,$4,$5)", p.tenantID, auditID, action, target, int64(revision)); err != nil {
			return zero, err
		}
		if key != "" {
			saved, err := json.Marshal(value)
			if err != nil {
				return zero, err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE browseai.idempotency_keys SET result=$4::jsonb WHERE tenant_id=$1 AND user_id=$2 AND request_key=$3", p.tenantID, p.userID, key, string(saved)); err != nil {
				return zero, err
			}
		}
		return value, nil
	})
}
