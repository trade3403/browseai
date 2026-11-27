package main

import (
	"net/http"
	"strconv"
)

type taskHTTP struct {
	auth  *authHTTP
	store taskStore
}

func (h taskHTTP) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/tasks", h.list)
	mux.HandleFunc("POST /api/tasks", h.create)
	mux.HandleFunc("GET /api/tasks/{id}", h.get)
	mux.HandleFunc("GET /api/tasks/{id}/stages", h.stages)
	mux.HandleFunc("PATCH /api/tasks/{id}", h.rename)
	mux.HandleFunc("DELETE /api/tasks/{id}", h.remove)
	mux.HandleFunc("POST /api/tasks/{id}/pause", h.command(actionPause))
	mux.HandleFunc("POST /api/tasks/{id}/stop", h.command(actionStop))
	mux.HandleFunc("POST /api/tasks/{id}/resume", h.command(actionResume))
	mux.HandleFunc("POST /api/tasks/{id}/approve", h.command(taskAction("approve")))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !h.auth.sameOrigin(r) {
			resourceError(w, errForbidden)
			return
		}
		r, ok := requireMutationKey(w, r)
		if !ok {
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (h taskHTTP) identity(w http.ResponseWriter, r *http.Request) (principal, bool) {
	return (projectHTTP{auth: h.auth}).identity(w, r)
}

func (h taskHTTP) list(w http.ResponseWriter, r *http.Request) {
	p, ok := h.identity(w, r)
	if !ok {
		return
	}
	after, projectID := r.URL.Query().Get("after"), r.URL.Query().Get("project_id")
	if (after != "" && !resourceUUID.MatchString(after)) || (projectID != "" && !resourceUUID.MatchString(projectID)) {
		resourceError(w, errResourceInvalid)
		return
	}
	items, err := h.store.list(r.Context(), p, after, projectID)
	if err != nil {
		resourceError(w, err)
		return
	}
	next := ""
	if len(items) > 100 {
		items = items[:100]
		next = items[99].ID
	}
	writeJSON(w, map[string]any{"tasks": items, "next_cursor": next})
}

func (h taskHTTP) get(w http.ResponseWriter, r *http.Request) {
	p, ok := h.identity(w, r)
	if !ok {
		return
	}
	item, err := h.store.get(r.Context(), p, r.PathValue("id"))
	if err != nil {
		resourceError(w, err)
		return
	}
	writeJSON(w, item)
}

func (h taskHTTP) stages(w http.ResponseWriter, r *http.Request) {
	p, ok := h.identity(w, r)
	if !ok {
		return
	}
	plan, err := h.store.stages(r.Context(), p, r.PathValue("id"))
	if err != nil {
		resourceError(w, err)
		return
	}
	writeJSON(w, plan)
}

func (h taskHTTP) create(w http.ResponseWriter, r *http.Request) {
	p, ok := h.identity(w, r)
	if !ok {
		return
	}
	var in taskCreate
	if !decodeResourceLimit(w, r, &in, 128*1024) {
		return
	}
	item, err := h.store.create(r.Context(), p, in)
	if err != nil {
		resourceError(w, err)
		return
	}
	w.Header().Set("Location", "/api/tasks/"+item.ID)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, item)
}

func (h taskHTTP) rename(w http.ResponseWriter, r *http.Request) {
	p, ok := h.identity(w, r)
	if !ok {
		return
	}
	var in struct {
		Name     string  `json:"name"`
		Revision *uint64 `json:"revision"`
	}
	if !decodeResource(w, r, &in) {
		return
	}
	if in.Revision == nil {
		resourceError(w, errResourceInvalid)
		return
	}
	item, err := h.store.rename(r.Context(), p, r.PathValue("id"), in.Name, *in.Revision)
	if err != nil {
		resourceError(w, err)
		return
	}
	writeJSON(w, item)
}

func (h taskHTTP) remove(w http.ResponseWriter, r *http.Request) {
	p, ok := h.identity(w, r)
	if !ok {
		return
	}
	revision, err := strconv.ParseUint(r.URL.Query().Get("revision"), 10, 63)
	if err != nil {
		resourceError(w, errResourceInvalid)
		return
	}
	if err := h.store.remove(r.Context(), p, r.PathValue("id"), revision); err != nil {
		resourceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h taskHTTP) command(action taskAction) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := h.identity(w, r)
		if !ok {
			return
		}
		var in struct {
			Revision *uint64 `json:"revision"`
		}
		if !decodeResource(w, r, &in) {
			return
		}
		if in.Revision == nil {
			resourceError(w, errResourceInvalid)
			return
		}
		var item taskRecord
		var err error
		if action == taskAction("approve") {
			item, err = h.store.approve(r.Context(), p, r.PathValue("id"), *in.Revision)
		} else {
			item, err = h.store.transition(r.Context(), p, r.PathValue("id"), *in.Revision, action, taskEvidence{LocalResume: action == actionResume})
		}
		if err != nil {
			resourceError(w, err)
			return
		}
		writeJSON(w, item)
	}
}
