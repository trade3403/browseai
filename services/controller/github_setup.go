package main

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Only operator-controlled files are accepted. The containing directory must
// be trusted and inaccessible to untrusted writers. Windows ACL provisioning
// is not implemented; fail closed rather than interpreting POSIX mode bits.
func loadGitHubAppKey(path string) (*rsa.PrivateKey, error) {
	if !filepath.IsAbs(path) {
		return nil, errGitHubAuthorization
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0077 != 0 {
		return nil, errGitHubAuthorization
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errGitHubAuthorization
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !os.SameFile(before, info) || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errGitHubAuthorization
	}
	const limit = 64 * 1024
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || len(raw) > limit {
		return nil, errGitHubAuthorization
	}
	block, rest := pem.Decode(raw)
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 || len(block.Headers) != 0 {
		return nil, errGitHubAuthorization
	}
	var key *rsa.PrivateKey
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		var parsed any
		parsed, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		key, _ = parsed.(*rsa.PrivateKey)
	default:
		return nil, errGitHubAuthorization
	}
	if err != nil || key == nil || key.N.BitLen() < 2048 || key.Validate() != nil {
		return nil, errGitHubAuthorization
	}
	return key, nil
}

// These values must come from independently verified operator records,
// never from a tenant's untrusted request. Remote checks alone do not prove
// the tenant's ownership.
type githubInstallationApproval struct {
	binding   githubBinding
	appID     int64
	accountID int64
}

func (a *githubApp) appJSON(ctx context.Context, path string, now time.Time, dst any) error {
	jwt, err := a.jwt(now)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com"+path, nil)
	if err != nil {
		return errGitHubAuthorization
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	res, err := a.client.Do(req)
	if err != nil {
		return errGitHubAuthorization
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return githubAppResponseError(res, now)
	}
	const limit = 2 * 1024 * 1024
	raw, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil || len(raw) > limit || json.Unmarshal(raw, dst) != nil {
		return errGitHubAuthorization
	}
	return nil
}

func githubAppResponseError(res *http.Response, now time.Time) error {
	if res.StatusCode == 429 || res.StatusCode >= 500 || (res.StatusCode == 403 && (res.Header.Get("Retry-After") != "" || res.Header.Get("X-RateLimit-Remaining") == "0")) {
		return githubReadError{retryAt: githubRetryAt(res.Header, now)}
	}
	return errGitHubAuthorization
}

// Verify the App, installation account and exact scoped repository before
// any trusted operator provisions the mapping. This does not persist it.
func (a *githubApp) verifyInstallation(ctx context.Context, approval githubInstallationApproval, now time.Time) error {
	b := approval.binding
	if approval.appID <= 0 || approval.accountID <= 0 || b.installationID <= 0 || b.repositoryID <= 0 || !resourceUUID.MatchString(b.tenantID) || !repoName.MatchString(b.fullName) {
		return errGitHubAuthorization
	}
	var app struct {
		ID int64 `json:"id"`
	}
	if err := a.appJSON(ctx, "/app", now, &app); err != nil {
		return err
	}
	if app.ID != approval.appID {
		return errGitHubAuthorization
	}
	var installation struct {
		ID      int64 `json:"id"`
		AppID   int64 `json:"app_id"`
		Account struct {
			ID int64 `json:"id"`
		} `json:"account"`
		TargetID    int64      `json:"target_id"`
		SuspendedAt *time.Time `json:"suspended_at"`
	}
	if err := a.appJSON(ctx, fmt.Sprintf("/app/installations/%d", b.installationID), now, &installation); err != nil {
		return err
	}
	if installation.ID != b.installationID || installation.AppID != approval.appID || installation.Account.ID != approval.accountID || installation.TargetID != approval.accountID || installation.SuspendedAt != nil {
		return errGitHubAuthorization
	}
	token, err := a.mint(ctx, b, now)
	if err != nil {
		return err
	}
	var repository struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
		Archived bool   `json:"archived"`
		Disabled bool   `json:"disabled"`
	}
	if err := a.readJSON(ctx, token, "/repos/"+b.fullName, nil, now, &repository); err != nil {
		return err
	}
	if repository.ID != b.repositoryID || !strings.EqualFold(repository.FullName, b.fullName) || repository.Archived || repository.Disabled {
		return errGitHubAuthorization
	}
	return nil
}
