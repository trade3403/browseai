package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"
)

var (
	errGitHubAuthorization = errors.New("GitHub authorization unavailable")
	githubAppIssuer        = regexp.MustCompile("^[A-Za-z0-9_-]{1,128}$")
)

type githubApp struct {
	issuer string
	key    *rsa.PrivateKey
	client *http.Client
}

func newGitHubApp(issuer string, key *rsa.PrivateKey, transport http.RoundTripper) (*githubApp, error) {
	if !githubAppIssuer.MatchString(issuer) || key == nil || key.N == nil || key.N.BitLen() < 2048 || key.Validate() != nil {
		return nil, errGitHubAuthorization
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &githubApp{issuer: issuer, key: key, client: &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}, nil
}

func (a *githubApp) jwt(now time.Time) (string, error) {
	claims, err := json.Marshal(struct {
		Issuer string `json:"iss"`
		IAT    int64  `json:"iat"`
		EXP    int64  `json:"exp"`
	}{a.issuer, now.Add(-time.Minute).Unix(), now.Add(5 * time.Minute).Unix()})
	if err != nil {
		return "", errGitHubAuthorization
	}
	enc := base64.RawURLEncoding
	input := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + enc.EncodeToString(claims)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", errGitHubAuthorization
	}
	return input + "." + enc.EncodeToString(sig), nil
}

// Repository ID, installation ID and tenant come only from the internal
// database binding lookup. They must never be constructed from HTTP input.
type githubBinding struct {
	tenantID       string
	installationID int64
	repositoryID   int64
	fullName       string
}

type githubToken struct {
	value     string
	expiresAt time.Time
}

// Tokens are opaque and kept in memory only. No response body or token is
// logged. The requested permissions are read-only and scoped to one repo.
func (a *githubApp) mint(ctx context.Context, binding githubBinding, now time.Time) (githubToken, error) {
	if !resourceUUID.MatchString(binding.tenantID) || binding.installationID <= 0 || binding.repositoryID <= 0 || !repoName.MatchString(binding.fullName) {
		return githubToken{}, errGitHubAuthorization
	}
	permissions := map[string]string{"actions": "read", "checks": "read", "contents": "read", "pull_requests": "read", "metadata": "read"}
	body, err := json.Marshal(struct {
		Repositories []int64           `json:"repository_ids"`
		Permissions  map[string]string `json:"permissions"`
	}{[]int64{binding.repositoryID}, permissions})
	if err != nil {
		return githubToken{}, errGitHubAuthorization
	}
	jwt, err := a.jwt(now)
	if err != nil {
		return githubToken{}, err
	}
	endpoint := fmt.Sprintf("https://api.github.com/app/installations/%d/access_tokens", binding.installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return githubToken{}, errGitHubAuthorization
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	req.Header.Set("Content-Type", "application/json")
	res, err := a.client.Do(req)
	if err != nil {
		return githubToken{}, errGitHubAuthorization
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		return githubToken{}, githubAppResponseError(res, now)
	}
	const maxBody = 256 * 1024
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil || len(raw) > maxBody {
		return githubToken{}, errGitHubAuthorization
	}
	var result struct {
		Token        string            `json:"token"`
		ExpiresAt    time.Time         `json:"expires_at"`
		Permissions  map[string]string `json:"permissions"`
		Repositories []struct {
			ID int64 `json:"id"`
		} `json:"repositories"`
	}
	if json.Unmarshal(raw, &result) != nil || len(result.Token) == 0 || len(result.Token) > 16384 ||
		!result.ExpiresAt.After(now.Add(time.Minute)) || result.ExpiresAt.After(now.Add(65*time.Minute)) ||
		len(result.Repositories) != 1 || result.Repositories[0].ID != binding.repositoryID {
		return githubToken{}, errGitHubAuthorization
	}
	if len(result.Permissions) != len(permissions) {
		return githubToken{}, errGitHubAuthorization
	}
	for name, level := range permissions {
		if result.Permissions[name] != level {
			return githubToken{}, errGitHubAuthorization
		}
	}
	for _, ch := range result.Token {
		if ch <= 32 || ch >= 127 {
			return githubToken{}, errGitHubAuthorization
		}
	}
	return githubToken{value: result.Token, expiresAt: result.ExpiresAt}, nil
}
