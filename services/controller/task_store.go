package main

import (
	"context"
	"database/sql"
	"errors"
	"regexp"

	"github.com/lib/pq"
)

var stageIdentifier = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$")

type taskRecord struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	State     taskState `json:"state"`
	Suspended taskState `json:"suspended_state,omitempty"`
	Revision  uint64    `json:"revision"`
	Stage     int       `json:"stage_index"`
	Count     int       `json:"stage_count"`
	Repairs   int       `json:"repairs"`
	Max       int       `json:"max_repairs"`
	CodeSHA   string    `json:"code_sha,omitempty"`
}

type stageCreate struct {
	ID     string `json:"id"`
	Weight int    `json:"weight"`
}

type taskCreate struct {
	ProjectID  string        `json:"project_id"`
	Name       string        `json:"name"`
	Stages     []stageCreate `json:"stages"`
	MaxRepairs *int          `json:"max_repairs"`
}

func (in taskCreate) valid() bool {
	if !resourceUUID.MatchString(in.ProjectID) || !validProjectName(in.Name) || len(in.Stages) < 1 || len(in.Stages) > 100 || (in.MaxRepairs != nil && (*in.MaxRepairs < 0 || *in.MaxRepairs > 20)) {
		return false
	}
	seen := make(map[string]bool)
	for _, st := range in.Stages {
		if !stageIdentifier.MatchString(st.ID) || st.Weight < 1 || st.Weight > 10000 || seen[st.ID] {
			return false
		}
		seen[st.ID] = true
	}
	return true
}

func (r taskRecord) snapshot() taskSnapshot {
	return taskSnapshot{State: r.State, Suspended: r.Suspended, Revision: r.Revision, Stage: r.Stage, StageCount: r.Count, Repairs: r.Repairs, MaxRepairs: r.Max, CodeSHA: r.CodeSHA}
}

const taskFields = "id::text,project_id::text,name,state,coalesce(suspended_state,''),revision,stage_index,stage_count,repairs,max_repairs,coalesce(code_sha,'')"

func readTask(row rowScanner) (taskRecord, error) {
	var r taskRecord
	err := row.Scan(&r.ID, &r.ProjectID, &r.Name, &r.State, &r.Suspended, &r.Revision, &r.Stage, &r.Count, &r.Repairs, &r.Max, &r.CodeSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return taskRecord{}, errResourceMissing
	}
	return r, err
}

type taskStore struct {
	db *sql.DB
}

func (s taskStore) list(ctx context.Context, p principal, after, projectID string) ([]taskRecord, error) {
	return tenantTx(ctx, s.db, p, func(tx *sql.Tx) ([]taskRecord, error) {
		rows, err := tx.QueryContext(ctx, "SELECT "+taskFields+" FROM browseai.tasks WHERE tenant_id=$1 AND (NULLIF($2,'')::uuid IS NULL OR id>NULLIF($2,'')::uuid) AND (NULLIF($3,'')::uuid IS NULL OR project_id=NULLIF($3,'')::uuid) ORDER BY id LIMIT 101", p.tenantID, after, projectID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := make([]taskRecord, 0)
		for rows.Next() {
			r, err := readTask(rows)
			if err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, rows.Err()
	})
}

func (s taskStore) get(ctx context.Context, p principal, id string) (taskRecord, error) {
	return tenantTx(ctx, s.db, p, func(tx *sql.Tx) (taskRecord, error) {
		return readTask(tx.QueryRowContext(ctx, "SELECT "+taskFields+" FROM browseai.tasks WHERE tenant_id=$1 AND id=$2", p.tenantID, id))
	})
}

func (s taskStore) create(ctx context.Context, p principal, in taskCreate) (taskRecord, error) {
	if !in.valid() {
		return taskRecord{}, errResourceInvalid
	}
	max := 5
	if in.MaxRepairs != nil {
		max = *in.MaxRepairs
	}
	return tenantTx(ctx, s.db, p, func(tx *sql.Tx) (taskRecord, error) {
		var projectID string
		err := tx.QueryRowContext(ctx, "SELECT id::text FROM browseai.projects WHERE tenant_id=$1 AND id=$2 FOR KEY SHARE", p.tenantID, in.ProjectID).Scan(&projectID)
		if errors.Is(err, sql.ErrNoRows) {
			return taskRecord{}, errResourceMissing
		}
		if err != nil {
			return taskRecord{}, err
		}
		r, err := readTask(tx.QueryRowContext(ctx, "INSERT INTO browseai.tasks(id,tenant_id,project_id,name,stage_count,max_repairs) VALUES(gen_random_uuid(),$1,$2,$3,$4,$5) RETURNING "+taskFields, p.tenantID, projectID, in.Name, len(in.Stages), max))
		if err != nil {
			return taskRecord{}, err
		}
		for ordinal, st := range in.Stages {
			if _, err := tx.ExecContext(ctx, "INSERT INTO browseai.stages(tenant_id,task_id,stage_id,ordinal,weight) VALUES($1,$2,$3,$4,$5)", p.tenantID, r.ID, st.ID, ordinal, st.Weight); err != nil {
				return taskRecord{}, err
			}
		}
		return r, nil
	})
}

func lockedTask(ctx context.Context, tx *sql.Tx, p principal, id string, revision uint64) (taskRecord, error) {
	r, err := readTask(tx.QueryRowContext(ctx, "SELECT "+taskFields+" FROM browseai.tasks WHERE tenant_id=$1 AND id=$2 FOR UPDATE", p.tenantID, id))
	if err != nil {
		return taskRecord{}, err
	}
	if r.Revision != revision || r.Revision >= uint64(1<<63-1) {
		return taskRecord{}, errResourceConflict
	}
	return r, nil
}

func (s taskStore) rename(ctx context.Context, p principal, id, name string, revision uint64) (taskRecord, error) {
	if !validProjectName(name) {
		return taskRecord{}, errResourceInvalid
	}
	return tenantTx(ctx, s.db, p, func(tx *sql.Tx) (taskRecord, error) {
		r, err := lockedTask(ctx, tx, p, id, revision)
		if err != nil {
			return taskRecord{}, err
		}
		if r.snapshot().terminal() {
			return taskRecord{}, errResourceConflict
		}
		return readTask(tx.QueryRowContext(ctx, "UPDATE browseai.tasks SET name=$3,revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND id=$2 AND revision=$4 RETURNING "+taskFields, p.tenantID, id, name, int64(revision)))
	})
}

// Only trusted service code may supply evidence. Public HTTP handlers expose
// pause and stop only; CI, dispatch, reconciliation and leases are not public flags.
func (s taskStore) transition(ctx context.Context, p principal, id string, revision uint64, action taskAction, evidence taskEvidence) (taskRecord, error) {
	return tenantTx(ctx, s.db, p, func(tx *sql.Tx) (taskRecord, error) {
		r, err := lockedTask(ctx, tx, p, id, revision)
		if err != nil {
			return taskRecord{}, err
		}
		next, err := advanceTask(r.snapshot(), revision, action, evidence)
		if errors.Is(err, errTaskRevision) || errors.Is(err, errTaskTransition) {
			return taskRecord{}, errResourceConflict
		}
		if err != nil {
			return taskRecord{}, err
		}
		return readTask(tx.QueryRowContext(ctx, "UPDATE browseai.tasks SET state=$3,suspended_state=NULLIF($4,''),revision=$5,stage_index=$6,repairs=$7,code_sha=NULLIF($8,''),updated_at=now() WHERE tenant_id=$1 AND id=$2 AND revision=$9 RETURNING "+taskFields, p.tenantID, id, next.State, next.Suspended, int64(next.Revision), next.Stage, next.Repairs, next.CodeSHA, int64(revision)))
	})
}

func (s taskStore) remove(ctx context.Context, p principal, id string, revision uint64) error {
	_, err := tenantTx(ctx, s.db, p, func(tx *sql.Tx) (bool, error) {
		r, err := lockedTask(ctx, tx, p, id, revision)
		if err != nil {
			return false, err
		}
		// Preserve the history of any changed/dispatched task until audit and
		// explicit archival are implemented. Only pristine drafts are deletable.
		if r.State != taskQueued || r.Revision != 0 {
			return false, errResourceConflict
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM browseai.stages WHERE tenant_id=$1 AND task_id=$2", p.tenantID, id); err != nil {
			return false, err
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM browseai.tasks WHERE tenant_id=$1 AND id=$2 AND revision=$3", p.tenantID, id, int64(revision))
		var pg *pq.Error
		if errors.As(err, &pg) && pg.Code == "23503" {
			return false, errResourceConflict
		}
		return err == nil, err
	})
	return err
}
