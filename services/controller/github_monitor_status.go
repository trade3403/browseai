package main

import (
	"context"
	"database/sql"
	"net/http"
	"time"
)

type githubMonitorStatusItem struct {
	WorkflowID int64         `json:"workflow_id"`
	Event      string        `json:"event"`
	State      githubCIState `json:"state"`
	ObservedAt *time.Time    `json:"observed_at,omitempty"`
	NextAt     time.Time     `json:"next_at"`
}

type githubMonitorStatusResponse struct {
	TaskID          string                    `json:"task_id"`
	Revision        uint64                    `json:"revision"`
	ObservationOnly bool                      `json:"observation_only"`
	Targets         []githubMonitorStatusItem `json:"targets"`
}

func (s taskStore) githubCIStatus(ctx context.Context, p principal, id string) (githubMonitorStatusResponse, error) {
	return tenantTx(ctx, s.db, p, func(tx *sql.Tx) (githubMonitorStatusResponse, error) {
		r, err := readTask(tx.QueryRowContext(ctx, "SELECT "+taskFields+" FROM browseai.tasks WHERE tenant_id=$1 AND id=$2 FOR SHARE", p.tenantID, id))
		if err != nil {
			return githubMonitorStatusResponse{}, err
		}
		out := githubMonitorStatusResponse{r.ID, r.Revision, true, make([]githubMonitorStatusItem, 0)}
		rows, err := tx.QueryContext(ctx, "SELECT workflow_id,event_type,state,observed_at,next_at FROM browseai.github_task_ci_status($1)", id)
		if err != nil {
			return githubMonitorStatusResponse{}, err
		}
		defer rows.Close()
		for rows.Next() {
			var item githubMonitorStatusItem
			if err := rows.Scan(&item.WorkflowID, &item.Event, &item.State, &item.ObservedAt, &item.NextAt); err != nil {
				return githubMonitorStatusResponse{}, err
			}
			out.Targets = append(out.Targets, item)
			if len(out.Targets) > 100 {
				return githubMonitorStatusResponse{}, errGitHubUnavailable
			}
		}
		return out, rows.Err()
	})
}

func (h taskHTTP) githubCIStatus(w http.ResponseWriter, r *http.Request) {
	p, ok := h.identity(w, r)
	if !ok {
		return
	}
	out, err := h.store.githubCIStatus(r.Context(), p, r.PathValue("id"))
	if err != nil {
		resourceError(w, err)
		return
	}
	writeJSON(w, out)
}
