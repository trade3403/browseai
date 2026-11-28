package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

type browserHeartbeatRequest struct {
	ID         string `json:"node_id"`
	Generation int64  `json:"generation"`
	Sequence   int64  `json:"sequence"`
	Capacity   int    `json:"capacity"`
	Occupied   int    `json:"occupied"`
}

type browserHeartbeatRecorder func(context.Context, string, string, int64, int64, int, int) error

// Direct TLS only. A forwarded header cannot establish transport security.
func browserHeartbeatHandler(record browserHeartbeatRecorder) http.Handler {
	slots := make(chan struct{}, 8)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path != "/node/heartbeat" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.TLS == nil || r.URL.RawQuery != "" || record == nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if _, err := browserCredentialDigest(token); !ok || err != nil || len(r.Header.Values("Authorization")) != 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		defer r.Body.Close()
		var in browserHeartbeatRequest
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF || !resourceUUID.MatchString(in.ID) ||
			in.Generation <= 0 || in.Sequence <= 0 || in.Capacity < 1 || in.Capacity > 256 ||
			in.Occupied < 0 || in.Occupied > in.Capacity {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if record(r.Context(), in.ID, token, in.Generation, in.Sequence, in.Capacity, in.Occupied) != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func browserDatabaseHeartbeatHandler(db *sql.DB) http.Handler {
	return browserHeartbeatHandler(func(ctx context.Context, id, token string, generation, sequence int64, capacity, occupied int) error {
		return persistBrowserHeartbeat(ctx, db, id, token, generation, sequence, capacity, occupied)
	})
}
