package main

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
)

type projectHTTP struct {
	auth  *authHTTP
	store projectStore
}

func resourceError(w http.ResponseWriter, err error) {
	code, message := http.StatusServiceUnavailable, "service unavailable"
	switch {
	case errors.Is(err, errUnauthenticated):
		code, message = 401, "authentication required"
	case errors.Is(err, errForbidden):
		code, message = 403, "access denied"
	case errors.Is(err, errResourceInvalid):
		code, message = 400, "invalid request"
	case errors.Is(err, errResourceMissing):
		code, message = 404, "resource not found"
	case errors.Is(err, errResourceConflict):
		code, message = 409, "resource conflict"
	}
	http.Error(w, message, code)
}

func decodeResource(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeResourceLimit(w, r, dst, 8192)
}

func decodeResourceLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || kind != "application/json" {
		http.Error(w, "JSON required", http.StatusUnsupportedMediaType)
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()
	if dec.Decode(dst) != nil || dec.Decode(new(any)) != io.EOF {
		resourceError(w, errResourceInvalid)
		return false
	}
	return true
}

func (h projectHTTP) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects", h.list)
	mux.HandleFunc("POST /api/projects", h.create)
	mux.HandleFunc("GET /api/projects/{id}", h.get)
	mux.HandleFunc("PATCH /api/projects/{id}", h.rename)
	mux.HandleFunc("DELETE /api/projects/{id}", h.remove)
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

func (h projectHTTP) identity(w http.ResponseWriter, r *http.Request) (principal, bool) {
	p, err := h.auth.sessions.authenticate(r.Context(), requestToken(r))
	if err == nil && p.role != roleUser {
		err = errForbidden
	}
	if err == nil && r.PathValue("id") != "" && !resourceUUID.MatchString(r.PathValue("id")) {
		err = errResourceMissing
	}
	if err != nil {
		resourceError(w, err)
		return principal{}, false
	}
	return p, true
}

func (h projectHTTP) list(w http.ResponseWriter, r *http.Request) {
	p, ok := h.identity(w, r)
	if !ok {
		return
	}
	after := r.URL.Query().Get("after")
	if after != "" && !resourceUUID.MatchString(after) {
		resourceError(w, errResourceInvalid)
		return
	}
	items, err := h.store.list(r.Context(), p, after)
	if err != nil {
		resourceError(w, err)
		return
	}
	next := ""
	if len(items) > 100 {
		items = items[:100]
		next = items[99].ID
	}
	writeJSON(w, map[string]any{"mock": false, "projects": items, "next_cursor": next})
}

func (h projectHTTP) get(w http.ResponseWriter, r *http.Request) {
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

func (h projectHTTP) create(w http.ResponseWriter, r *http.Request) {
	p, ok := h.identity(w, r)
	if !ok {
		return
	}
	var in projectCreate
	if !decodeResource(w, r, &in) {
		return
	}
	item, err := h.store.create(r.Context(), p, in)
	if err != nil {
		resourceError(w, err)
		return
	}
	w.Header().Set("Location", "/api/projects/"+item.ID)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, item)
}

func (h projectHTTP) rename(w http.ResponseWriter, r *http.Request) {
	p, ok := h.identity(w, r)
	if !ok {
		return
	}
	var in struct {
		Name     string `json:"name"`
		Revision *int64 `json:"revision"`
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

func (h projectHTTP) remove(w http.ResponseWriter, r *http.Request) {
	p, ok := h.identity(w, r)
	if !ok {
		return
	}
	revision, err := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
	if err != nil || revision < 0 {
		resourceError(w, errResourceInvalid)
		return
	}
	if err := h.store.remove(r.Context(), p, r.PathValue("id"), revision); err != nil {
		resourceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
