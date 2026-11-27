package main

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"

	"github.com/lib/pq"
)

var (
	errResourceMissing  = errors.New("resource not found")
	errResourceConflict = errors.New("resource conflict")
	errResourceInvalid  = errors.New("invalid resource")
	resourceUUID        = regexp.MustCompile("^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")
	repoName            = regexp.MustCompile("^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")
	branchName          = regexp.MustCompile("^[A-Za-z0-9_./-]+$")
)

type projectRecord struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	RepositoryID int64  `json:"repository_id"`
	Repository   string `json:"repository"`
	CodeBranch   string `json:"code_branch"`
	Revision     int64  `json:"revision"`
}

type projectCreate struct {
	Name         string `json:"name"`
	RepositoryID int64  `json:"repository_id"`
	Repository   string `json:"repository"`
	CodeBranch   string `json:"code_branch"`
}

func validProjectName(name string) bool {
	return name != "" && len(name) <= 128 && strings.TrimSpace(name) == name
}

func (p projectCreate) valid() bool {
	if !validProjectName(p.Name) || p.RepositoryID <= 0 || len(p.Repository) > 200 || !repoName.MatchString(p.Repository) || len(p.CodeBranch) > 255 || !branchName.MatchString(p.CodeBranch) || strings.Contains(p.CodeBranch, "..") || strings.HasPrefix(p.CodeBranch, "-") || strings.HasPrefix(p.CodeBranch, "ai-control/") || p.CodeBranch == "ai-control" || strings.HasSuffix(p.CodeBranch, ".") {
		return false
	}
	for _, part := range strings.Split(p.Repository, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	for _, part := range strings.Split(p.CodeBranch, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

type projectStore struct {
	db *sql.DB
}

// A transaction-local context derived only from the authenticated principal.
// All queries also explicitly filter the tenant ID. Never use a pooled
// connection's session-wide SET for identity.
func tenantTx[T any](ctx context.Context, db *sql.DB, p principal, fn func(*sql.Tx) (T, error)) (T, error) {
	var zero T
	if p.role != roleUser || !resourceUUID.MatchString(p.tenantID) || !resourceUUID.MatchString(p.userID) {
		return zero, errForbidden
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SELECT set_config('browseai.tenant_id',$1,true),set_config('browseai.actor_id',$2,true)", p.tenantID, p.userID); err != nil {
		return zero, err
	}
	value, err := fn(tx)
	if err != nil {
		return zero, err
	}
	if err := tx.Commit(); err != nil {
		return zero, err
	}
	return value, nil
}

const projectFields = "id::text,name,repository_id,repository,code_branch,revision"

type rowScanner interface {
	Scan(...any) error
}

func readProject(row rowScanner) (projectRecord, error) {
	var p projectRecord
	err := row.Scan(&p.ID, &p.Name, &p.RepositoryID, &p.Repository, &p.CodeBranch, &p.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return projectRecord{}, errResourceMissing
	}
	return p, err
}

func (s projectStore) list(ctx context.Context, p principal, after string) ([]projectRecord, error) {
	return tenantTx(ctx, s.db, p, func(tx *sql.Tx) ([]projectRecord, error) {
		rows, err := tx.QueryContext(ctx, "SELECT "+projectFields+" FROM browseai.projects WHERE tenant_id=$1 AND (NULLIF($2,'')::uuid IS NULL OR id>NULLIF($2,'')::uuid) ORDER BY id LIMIT 101", p.tenantID, after)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := make([]projectRecord, 0)
		for rows.Next() {
			item, err := readProject(rows)
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		return out, rows.Err()
	})
}

func (s projectStore) get(ctx context.Context, p principal, id string) (projectRecord, error) {
	return tenantTx(ctx, s.db, p, func(tx *sql.Tx) (projectRecord, error) {
		return readProject(tx.QueryRowContext(ctx, "SELECT "+projectFields+" FROM browseai.projects WHERE tenant_id=$1 AND id=$2", p.tenantID, id))
	})
}

func (s projectStore) create(ctx context.Context, p principal, in projectCreate) (projectRecord, error) {
	if !in.valid() {
		return projectRecord{}, errResourceInvalid
	}
	return mutationTx(ctx, s.db, p, "project.create", "", 0, in, func(tx *sql.Tx) (projectRecord, error) {
		return readProject(tx.QueryRowContext(ctx, "INSERT INTO browseai.projects(id,tenant_id,name,repository_id,repository,code_branch) VALUES(gen_random_uuid(),$1,$2,$3,$4,$5) RETURNING "+projectFields, p.tenantID, in.Name, in.RepositoryID, in.Repository, in.CodeBranch))
	})
}

func (s projectStore) rename(ctx context.Context, p principal, id, name string, revision int64) (projectRecord, error) {
	if !validProjectName(name) || revision < 0 {
		return projectRecord{}, errResourceInvalid
	}
	return mutationTx(ctx, s.db, p, "project.rename", id, uint64(revision), name, func(tx *sql.Tx) (projectRecord, error) {
		row, err := readProject(tx.QueryRowContext(ctx, "SELECT "+projectFields+" FROM browseai.projects WHERE tenant_id=$1 AND id=$2 FOR UPDATE", p.tenantID, id))
		if err != nil {
			return projectRecord{}, err
		}
		if row.Revision != revision {
			return projectRecord{}, errResourceConflict
		}
		return readProject(tx.QueryRowContext(ctx, "UPDATE browseai.projects SET name=$3,revision=revision+1 WHERE tenant_id=$1 AND id=$2 AND revision=$4 RETURNING "+projectFields, p.tenantID, id, name, revision))
	})
}

func (s projectStore) remove(ctx context.Context, p principal, id string, revision int64) error {
	if revision < 0 {
		return errResourceInvalid
	}
	_, err := mutationTx(ctx, s.db, p, "project.delete", id, uint64(revision), nil, func(tx *sql.Tx) (bool, error) {
		row, err := readProject(tx.QueryRowContext(ctx, "SELECT "+projectFields+" FROM browseai.projects WHERE tenant_id=$1 AND id=$2 FOR UPDATE", p.tenantID, id))
		if err != nil {
			return false, err
		}
		if row.Revision != revision {
			return false, errResourceConflict
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM browseai.projects WHERE tenant_id=$1 AND id=$2 AND revision=$3", p.tenantID, id, revision)
		var pg *pq.Error
		if errors.As(err, &pg) && pg.Code == "23503" {
			return false, errResourceConflict
		}
		return err == nil, err
	})
	return err
}
