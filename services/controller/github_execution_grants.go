package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

// This journal is for a dedicated Linux node on a persistent local filesystem.
// The operator must protect the directory, its parents and the mount from
// workload access, deletion, rollback and replacement. It is not a cluster
// consensus service. Never use a temporary directory in production.
type githubExecutionGrantStore struct {
	dir string
}

func newGitHubExecutionGrantStore(dir string) (*githubExecutionGrantStore, error) {
	if runtime.GOOS != "linux" || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return nil, errGitHubAuthorization
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil || resolved != dir {
		return nil, errGitHubAuthorization
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, errGitHubAuthorization
	}
	return &githubExecutionGrantStore{dir: dir}, nil
}

// Identity deliberately excludes nonce, task revision, verifier and digests:
// changing those values cannot reopen a previously admitted run/attempt.
func githubExecutionGrantID(e githubExecutionStatement) ([32]byte, error) {
	raw, err := json.Marshal(struct {
		Domain       string
		Tenant       string
		Repository   int64
		Workflow     int64
		Event        string
		Run, Attempt int64
	}{"browseai-node-admission-v1", e.Tenant, e.Repository, e.Workflow, e.Event, e.Run, e.Attempt})
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(raw), nil
}

// A successful return requires both the marker and directory entry to be
// synced before executing anything. Existing, partial or ambiguous markers
// are never removed automatically, including on errors.
func (s *githubExecutionGrantStore) claim(e githubExecutionStatement, authorization []byte) error {
	if s == nil || len(authorization) == 0 {
		return errGitHubAuthorization
	}
	if _, err := newGitHubExecutionGrantStore(s.dir); err != nil {
		return err
	}
	id, err := githubExecutionGrantID(e)
	if err != nil {
		return errGitHubAuthorization
	}
	dir, err := os.Open(s.dir)
	if err != nil {
		return errGitHubAuthorization
	}
	defer dir.Close()
	f, err := os.OpenFile(filepath.Join(s.dir, hex.EncodeToString(id[:])+".claimed"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errGitHubAuthorization
	}
	digest := sha256.Sum256(authorization)
	_, writeErr := f.Write([]byte(hex.EncodeToString(digest[:]) + "\n"))
	syncErr := f.Sync()
	closeErr := f.Close()
	dirErr := dir.Sync()
	if writeErr != nil || syncErr != nil || closeErr != nil || dirErr != nil {
		return errGitHubAuthorization
	}
	return nil
}
