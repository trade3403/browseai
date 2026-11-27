package main

import (
	"context"
	"database/sql"
	"strings"
	"unicode/utf8"

	"github.com/lib/pq"
)

// The state machine runs stages in ordinal order. Require each dependency
// to refer to a preceding stage, which also rejects cycles and self edges.
func validStagePlan(stages []stageCreate) bool {
	if len(stages) < 1 || len(stages) > 100 {
		return false
	}
	seen := make(map[string]bool)
	for _, st := range stages {
		if !stageIdentifier.MatchString(st.ID) || st.Weight < 1 || st.Weight > 10000 || seen[st.ID] ||
			!validPlanText(st.Instruction, 8192) || len(st.Dependencies) > 100 || len(st.Acceptance) < 1 || len(st.Acceptance) > 50 {
			return false
		}
		deps := make(map[string]bool)
		for _, dep := range st.Dependencies {
			if !seen[dep] || deps[dep] {
				return false
			}
			deps[dep] = true
		}
		checks := make(map[string]bool)
		for _, check := range st.Acceptance {
			if !validPlanText(check, 128) || strings.TrimSpace(check) != check || checks[check] {
				return false
			}
			checks[check] = true
		}
		seen[st.ID] = true
	}
	return true
}

func validPlanText(value string, limit int) bool {
	return utf8.ValidString(value) && len(value) <= limit && strings.TrimSpace(value) != "" && !strings.ContainsRune(value, 0)
}

func readStagePlan(ctx context.Context, tx *sql.Tx, p principal, id string) ([]stageCreate, error) {
	rows, err := tx.QueryContext(ctx, "SELECT stage_id,weight,coalesce(instruction,''),dependencies,acceptance FROM browseai.stages WHERE tenant_id=$1 AND task_id=$2 ORDER BY ordinal", p.tenantID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]stageCreate, 0)
	for rows.Next() {
		var st stageCreate
		if err := rows.Scan(&st.ID, &st.Weight, &st.Instruction, pq.Array(&st.Dependencies), pq.Array(&st.Acceptance)); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func requireStagePlan(ctx context.Context, tx *sql.Tx, p principal, r taskRecord) error {
	plan, err := readStagePlan(ctx, tx, p, r.ID)
	if err != nil {
		return err
	}
	if len(plan) != r.Count || !validStagePlan(plan) {
		return errResourceConflict
	}
	return nil
}

type stagePlanResponse struct {
	TaskID     string        `json:"task_id"`
	Revision   uint64        `json:"revision"`
	Configured bool          `json:"configured"`
	Stages     []stageCreate `json:"stages"`
}

func (s taskStore) stages(ctx context.Context, p principal, id string) (stagePlanResponse, error) {
	return tenantTx(ctx, s.db, p, func(tx *sql.Tx) (stagePlanResponse, error) {
		r, err := readTask(tx.QueryRowContext(ctx, "SELECT "+taskFields+" FROM browseai.tasks WHERE tenant_id=$1 AND id=$2 FOR SHARE", p.tenantID, id))
		if err != nil {
			return stagePlanResponse{}, err
		}
		plan, err := readStagePlan(ctx, tx, p, id)
		if err != nil {
			return stagePlanResponse{}, err
		}
		return stagePlanResponse{TaskID: id, Revision: r.Revision, Configured: len(plan) == r.Count && validStagePlan(plan), Stages: plan}, nil
	})
}
