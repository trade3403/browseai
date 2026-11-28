package main

import (
	"context"
	"database/sql"
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
	if len(os.Args) > 1 {
		if os.Args[1] == "github-ci-acceptance" {
			if githubCIAcceptanceCommand(os.Args[2:], os.Stdin) != nil {
				log.Fatal("GitHub CI acceptance operation rejected")
			}
			log.Print("GitHub CI acceptance operation recorded")
			return
		}
		if os.Args[1] == "github-failure" {
			if githubFailureCommand(os.Args[2:], os.Stdin) != nil {
				log.Fatal("GitHub failure operation rejected")
			}
			log.Print("GitHub failure operation recorded")
			return
		}
		if os.Args[1] == "github-verifier-register" {
			dsn := os.Getenv("BROWSEAI_VERIFIER_OPERATOR_DSN")
			if !githubRoleDSNValid(dsn, "browseai_verifier_operator") {
				log.Fatal("invalid verifier operator TLS DSN")
			}
			v, err := githubVerifierRegistrationInput(os.Args[2:], os.Stdin)
			if err != nil {
				log.Fatal("verifier approval rejected")
			}
			db, err := sql.Open("postgres", dsn)
			if err != nil {
				log.Fatal("verifier database unavailable")
			}
			defer db.Close()
			if err := registerGitHubVerifier(context.Background(), db, v); err != nil {
				log.Fatal("verifier registration rejected")
			}
			log.Print("GitHub verifier registered")
			return
		}
		if os.Args[1] == "github-signer-preview" {
			if len(os.Args) != 3 || githubSignerPreviewCommand(os.Args[2]) != nil {
				log.Fatal("GitHub signer preview unavailable")
			}
			return
		}
		if os.Args[1] == "github-execution-node-preview" {
			if len(os.Args) != 3 || githubExecutionNodePreviewCommand(os.Args[2]) != nil {
				log.Fatal("GitHub execution node preview unavailable")
			}
			return
		}
		if os.Args[1] == "github-monitor" {
			if len(os.Args) != 2 || githubMonitorCommand() != nil {
				log.Fatal("GitHub monitor unavailable")
			}
			return
		}
		if os.Args[1] == "github-webhook" {
			if len(os.Args) != 2 || !githubRoleDSNValid(os.Getenv("BROWSEAI_WEBHOOK_DSN"), "browseai_webhook") {
				log.Fatal("invalid webhook database configuration")
			}
			secret, err := loadGitHubWebhookSecret(os.Getenv("BROWSEAI_WEBHOOK_SECRET_FILE"))
			if err != nil {
				log.Fatal("webhook secret unavailable")
			}
			db, err := sql.Open("postgres", os.Getenv("BROWSEAI_WEBHOOK_DSN"))
			if err != nil {
				log.Fatal("webhook database unavailable")
			}
			defer db.Close()
			db.SetMaxOpenConns(8)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err = githubWebhookReady(ctx, db)
			cancel()
			if err != nil {
				log.Fatal("webhook database role invalid")
			}
			addr := os.Getenv("BROWSEAI_WEBHOOK_ADDR")
			if addr == "" {
				addr = "127.0.0.1:8081"
			}
			server, err := newGitHubWebhookServer(addr, secret, githubDeliveryStore{db})
			if err != nil {
				log.Fatal("invalid webhook listener configuration")
			}
			log.Fatal(server.ListenAndServe())
		}
		if os.Args[1] == "github-bind" {
			dsn := os.Getenv("BROWSEAI_GITHUB_OPERATOR_DSN")
			if !githubOperatorDSNValid(dsn) {
				log.Fatal("GitHub binding requires explicit operator TLS DSN")
			}
			key, err := loadGitHubAppKey(os.Getenv("BROWSEAI_GITHUB_KEY_FILE"))
			if err != nil {
				log.Fatal("GitHub key unavailable")
			}
			app, err := newGitHubApp(os.Getenv("BROWSEAI_GITHUB_APP_ISSUER"), key, nil)
			if err != nil {
				log.Fatal("GitHub App configuration invalid")
			}
			db, err := sql.Open("postgres", dsn)
			if err != nil {
				log.Fatal("GitHub operator database unavailable")
			}
			defer db.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := githubOperatorCommand(ctx, db, app, os.Args[2:], os.Stdin, os.Stdout, time.Now()); err != nil {
				log.Fatal("GitHub binding rejected")
			}
			return
		}
		if os.Args[1] != "account" || !adminDSNValid(os.Getenv("BROWSEAI_ADMIN_DSN")) {
			log.Fatal("account command requires explicit browseai_admin TLS DSN")
		}
		db, err := sql.Open("postgres", os.Getenv("BROWSEAI_ADMIN_DSN"))
		if err != nil {
			log.Fatal("invalid operator database configuration")
		}
		defer db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := accountCommand(ctx, db, os.Args[2:], os.Stdin, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	server := newServer(os.Getenv("LISTEN_ADDR"))
	mode := os.Getenv("BROWSEAI_MODE")
	switch mode {
	case "", "mock":
		mode = "mock"
	case "authenticated":
		db, err := openAuthRuntime(context.Background(), server, os.Getenv("BROWSEAI_AUTH_DSN"), os.Getenv("BROWSEAI_ORIGIN"))
		if err != nil {
			log.Fatal("authenticated startup failed: ", err)
		}
		defer db.Close()
		appDB, err := openProjectRuntime(context.Background(), server, db, os.Getenv("BROWSEAI_APP_DSN"), os.Getenv("BROWSEAI_ORIGIN"))
		if err != nil {
			log.Fatal("project startup failed: ", err)
		}
		defer appDB.Close()
		if dsn := os.Getenv("BROWSEAI_REPAIR_HUMAN_DSN"); dsn != "" {
			reviewDB, err := openGitHubRepairReviewRuntime(context.Background(), server, db, dsn, os.Getenv("BROWSEAI_ORIGIN"))
			if err != nil {
				log.Fatal("repair review startup failed")
			}
			defer reviewDB.Close()
		}
	default:
		log.Fatal("invalid BROWSEAI_MODE")
	}
	log.Printf("BrowseAI %s controller listening on %s", mode, server.Addr)
	log.Fatal(server.ListenAndServe())
}
