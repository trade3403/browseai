package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
)

const githubBuildMaterialLimit = 512 << 20

var githubBuildBaseName = regexp.MustCompile(`^[a-z0-9]+([./_-][a-z0-9]+)*$`)

// Materials are acquired and approved independently of repository code.
// SourceTreeDigest is the SHA-256 of the exact source archive, not a Git
// tree ID. The acquisition service must bind the archive to the commit.
// The harness is a trusted executable, not a repository-provided script.
type githubBuildMaterials struct {
	source       string
	dependencies string
	harness      string
	baseName     string
}

func githubBuildDockerfile(baseName, digest string) ([]byte, error) {
	if len(baseName) > 200 || !githubBuildBaseName.MatchString(baseName) || !githubWorkflowDigestValid(digest) {
		return nil, errGitHubAuthorization
	}
	return []byte("FROM " + baseName + "@sha256:" + digest + "\n" +
		"COPY --chown=0:0 --chmod=0444 source.tar /opt/browseai/source.tar\n" +
		"COPY --chown=0:0 --chmod=0444 dependencies.tar /opt/browseai/dependencies.tar\n" +
		"COPY --chown=0:0 --chmod=0555 trusted-test /opt/browseai/trusted-test\n" +
		"ENV PATH=/usr/bin:/bin HOME=/tmp LANG=C.UTF-8\n" +
		"USER 65532:65532\nWORKDIR /\nHEALTHCHECK NONE\nSTOPSIGNAL SIGTERM\n" +
		"ENTRYPOINT [\"/opt/browseai/trusted-test\"]\nCMD []\n"), nil
}

// Stage only fixed filenames from a protected material directory. Neither
// archive is extracted during build. The harness must safely unpack in the
// isolated runtime. The build context must not be consumed until this
// function returns success: a failed hash can leave a partial output.
func githubWriteBuildContext(ctx context.Context, out io.Writer, materials githubBuildMaterials, policy githubSandboxBuildPolicy, harnessDigest string) error {
	if runtime.GOOS != "linux" || out == nil || ctx.Err() != nil {
		return errGitHubAuthorization
	}
	dockerfile, err := githubBuildDockerfile(materials.baseName, policy.baseImageDigest)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(out)
	defer tw.Close()
	if err := tw.WriteHeader(&tar.Header{Name: "Dockerfile", Mode: 0444, Size: int64(len(dockerfile))}); err != nil {
		return errGitHubAuthorization
	}
	if _, err := tw.Write(dockerfile); err != nil {
		return errGitHubAuthorization
	}
	for _, m := range []struct {
		path, name, digest string
		mode               int64
	}{
		{materials.source, "source.tar", policy.sourceTreeDigest, 0444},
		{materials.dependencies, "dependencies.tar", policy.dependencyDigest, 0444},
		{materials.harness, "trusted-test", harnessDigest, 0555},
	} {
		if err := githubWriteBuildMaterial(ctx, tw, m.path, m.name, m.digest, m.mode); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil || ctx.Err() != nil {
		return errGitHubAuthorization
	}
	return nil
}

func githubWriteBuildMaterial(ctx context.Context, tw *tar.Writer, path, name, digest string, mode int64) error {
	if ctx.Err() != nil || !githubWorkflowDigestValid(digest) || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errGitHubAuthorization
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return errGitHubAuthorization
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 ||
		info.Size() <= 0 || info.Size() > githubBuildMaterialLimit {
		return errGitHubAuthorization
	}
	f, err := os.Open(path)
	if err != nil {
		return errGitHubAuthorization
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Size() != info.Size() {
		return errGitHubAuthorization
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: info.Size()}); err != nil {
		return errGitHubAuthorization
	}
	h := sha256.New()
	reader := &githubBuildContextReader{ctx, f}
	n, err := io.CopyN(io.MultiWriter(tw, h), reader, info.Size())
	if err != nil || n != info.Size() || !bytes.Equal([]byte(hex.EncodeToString(h.Sum(nil))), []byte(digest)) {
		return errGitHubAuthorization
	}
	var extra [1]byte
	if n, err := reader.Read(extra[:]); n != 0 || err != io.EOF || ctx.Err() != nil {
		return errGitHubAuthorization
	}
	return nil
}

type githubBuildContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *githubBuildContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
