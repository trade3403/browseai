package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const sessionCookie = "__Host-browseai_session"

// The verifier must authenticate credentials and choose an enabled membership.
// Client-provided user IDs, tenant headers and roles are never principals.
type credentialVerifier interface {
	verify(context.Context, string, string) (principal, error)
}

type loginWindow struct {
	start time.Time
	count int
}

// A bounded per-process limiter, with a global cap to limit password KDF work.
// A production multi-instance deployment also needs shared and proxy-aware limits.
type loginLimiter struct {
	mu     sync.Mutex
	global loginWindow
	peers  map[string]loginWindow
}

func (l *loginLimiter) allow(peer string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.peers == nil || now.Sub(l.global.start) >= time.Minute || now.Before(l.global.start) {
		l.global = loginWindow{start: now}
		l.peers = make(map[string]loginWindow)
	}
	if l.global.count >= 60 {
		return false
	}
	p := l.peers[peer]
	if p.count >= 5 {
		return false
	}
	p.count++
	l.peers[peer] = p
	l.global.count++
	return true
}

type authHTTP struct {
	verifier credentialVerifier
	sessions sessionService
	origin   string
	limiter  loginLimiter
	slots    chan struct{}
}

func newAuthHTTP(origin string, verifier credentialVerifier, sessions sessionService) (*authHTTP, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || verifier == nil || sessions.store == nil {
		return nil, errors.New("invalid authentication configuration")
	}
	return &authHTTP{verifier: verifier, sessions: sessions, origin: origin, slots: make(chan struct{}, 2)}, nil
}

func (a *authHTTP) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/login", a.login)
	mux.HandleFunc("GET /api/auth/me", a.me)
	mux.HandleFunc("POST /api/auth/logout", a.logout)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

// Exact configured Origin protects browser cookie mutations against CSRF.
// Reverse proxy headers are not trusted to choose the expected origin.
func (a *authHTTP) sameOrigin(r *http.Request) bool {
	return r.Header.Get("Origin") == a.origin && len(r.Header.Values("Origin")) == 1
}

func authError(w http.ResponseWriter, err error) {
	if errors.Is(err, errUnauthenticated) {
		http.Error(w, "invalid credentials or session", http.StatusUnauthorized)
		return
	}
	http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
}

func setSessionCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", Secure: true,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: maxAge,
	})
}

func requestToken(r *http.Request) string {
	cookies := r.CookiesNamed(sessionCookie)
	if len(cookies) != 1 {
		return ""
	}
	return cookies[0].Value
}

func (a *authHTTP) login(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(r) {
		http.Error(w, "origin denied", http.StatusForbidden)
		return
	}
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || kind != "application/json" {
		http.Error(w, "JSON required", http.StatusUnsupportedMediaType)
		return
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = "unknown"
	}
	if !a.limiter.allow(peer, time.Now()) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var body struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || dec.Decode(new(any)) != io.EOF || body.Login == "" || body.Login != strings.TrimSpace(body.Login) || len(body.Login) > 254 || len(body.Password) == 0 || len(body.Password) > 1024 {
		http.Error(w, "invalid login request", http.StatusBadRequest)
		return
	}
	select {
	case a.slots <- struct{}{}:
		defer func() { <-a.slots }()
	default:
		w.Header().Set("Retry-After", "1")
		http.Error(w, "authentication busy", http.StatusTooManyRequests)
		return
	}
	var token string
	if issuer, ok := a.verifier.(interface {
		verifyAndIssue(context.Context, string, string) (string, error)
	}); ok {
		token, err = issuer.verifyAndIssue(r.Context(), body.Login, body.Password)
	} else {
		var p principal
		p, err = a.verifier.verify(r.Context(), body.Login, body.Password)
		if err == nil {
			token, err = a.sessions.issueVerified(r.Context(), p)
		}
	}
	if err != nil {
		authError(w, err)
		return
	}
	// Rotate rather than reuse a pre-existing cookie; do not expose token in JSON.
	if old := requestToken(r); old != "" {
		if _, err := digestSession(old); err == nil {
			if err := a.sessions.logout(r.Context(), old); err != nil {
				_ = a.sessions.logout(r.Context(), token)
				authError(w, err)
				return
			}
		}
	}
	setSessionCookie(w, token, 8*60*60)
	w.WriteHeader(http.StatusNoContent)
}

func (a *authHTTP) me(w http.ResponseWriter, r *http.Request) {
	p, err := a.sessions.authenticate(r.Context(), requestToken(r))
	if err != nil {
		authError(w, err)
		return
	}
	writeJSON(w, map[string]string{"user_id": p.userID, "tenant_id": p.tenantID, "role": string(p.role)})
}

func (a *authHTTP) logout(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(r) {
		http.Error(w, "origin denied", http.StatusForbidden)
		return
	}
	token := requestToken(r)
	if _, err := digestSession(token); err == nil {
		if err := a.sessions.logout(r.Context(), token); err != nil {
			authError(w, err)
			return
		}
	}
	setSessionCookie(w, "", -1)
	w.WriteHeader(http.StatusNoContent)
}
