package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

type Project struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Repository string `json:"repository"`
	Progress   int    `json:"progress"`
	State      string `json:"state"`
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Print(err)
	}
}

func routes() http.Handler {
	mux := http.NewServeMux()
	health := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"status": "ok", "mode": "mock"})
	}
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /readyz", health)
	mux.HandleFunc("GET /api/projects", func(w http.ResponseWriter, r *http.Request) {
		// V0.1 exposes only a fixed fictional demo. Real authentication and data access are V0.2.
		// Request headers, query parameters and bodies cannot choose the principal.
		p := principal{"demo-user", "demo-tenant", roleUser}
		data := []Project{
			{"localtunnel", "LocalTunnel", "trade3403/localtunnel", 75, "DEVELOPING"},
			{"globalchat", "GlobalChat", "trade3403/wechat", 72, "WAIT_CI"},
			{"p2p", "P2P 系统", "trade3403/p2p", 45, "DEVELOPING"},
			{"shop", "商城系统", "", 100, "COMPLETED"},
			{"admin", "管理后台", "", 100, "COMPLETED"},
			{"video", "视频系统", "", 0, "QUEUED"},
		}
		rows := make([]ownedProject, 0, len(data))
		for _, item := range data {
			rows = append(rows, ownedProject{p.tenantID, item})
		}
		items, err := listProjects(p, rows)
		if err != nil {
			http.Error(w, "access denied", http.StatusForbidden)
			return
		}
		writeJSON(w, map[string]any{"mock": true, "projects": items})
	})
	return mux
}

func newServer(addr string) *http.Server {
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	return &http.Server{
		Addr: addr, Handler: routes(), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
	}
}

func main() {
	server := newServer(os.Getenv("LISTEN_ADDR"))
	log.Printf("BrowseAI mock controller listening on %s", server.Addr)
	log.Fatal(server.ListenAndServe())
}
