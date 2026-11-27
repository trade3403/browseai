package main

import (
	"bytes"
	"encoding/json"
	"strings"
)

const githubSandboxInspectLimit = 262144

// The local daemon is trusted infrastructure. These checks reject unexpected
// runtime/image configuration, not malicious daemon responses or kernel bugs.
func githubSandboxRuntimeValid(raw []byte) bool {
	if len(raw) == 0 || len(raw) > githubSandboxInspectLimit {
		return false
	}
	var info struct {
		OSType          string
		CgroupVersion   string
		CgroupDriver    string
		SecurityOptions []string
		MemoryLimit     bool
		SwapLimit       bool
		CPUCfsQuota     bool
		PidsLimit       bool
	}
	if json.Unmarshal(raw, &info) != nil || info.OSType != "linux" ||
		info.CgroupVersion != "2" || info.CgroupDriver != "systemd" ||
		!info.MemoryLimit || !info.SwapLimit || !info.CPUCfsQuota || !info.PidsLimit {
		return false
	}
	rootless, seccomp := false, false
	for _, option := range info.SecurityOptions {
		if option == "name=rootless" {
			rootless = true
		}
		if option == "name=seccomp,profile=builtin" {
			seccomp = true
		}
	}
	return rootless && seccomp
}

func githubSandboxImageConfigValid(raw []byte, image string) bool {
	if len(raw) == 0 || len(raw) > githubSandboxInspectLimit || !strings.HasPrefix(image, "sha256:") ||
		!githubWorkflowDigestValid(strings.TrimPrefix(image, "sha256:")) {
		return false
	}
	var images []struct {
		ID     string `json:"Id"`
		OS     string
		Config *struct {
			User        string
			Env         []string
			Entrypoint  []string
			Cmd         []string
			WorkingDir  string
			Volumes     map[string]json.RawMessage
			Healthcheck *struct {
				Test []string
			}
			OnBuild    []string
			StopSignal string
		}
	}
	if json.Unmarshal(raw, &images) != nil || len(images) != 1 || images[0].ID != image ||
		images[0].OS != "linux" || images[0].Config == nil {
		return false
	}
	c := images[0].Config
	if c.User != "65532:65532" || len(c.Volumes) != 0 || len(c.OnBuild) != 0 ||
		len(c.Cmd) != 0 || len(c.Entrypoint) != 1 || c.Entrypoint[0] != "/opt/browseai/trusted-test" ||
		c.WorkingDir != "/" || (c.StopSignal != "" && c.StopSignal != "SIGTERM") ||
		(c.Healthcheck != nil && (len(c.Healthcheck.Test) != 1 || c.Healthcheck.Test[0] != "NONE")) {
		return false
	}
	// Fixed, minimal image environment. Do not allow arbitrary loader,
	// proxy, interpreter or credential settings baked into the image.
	allowed := map[string]string{
		"PATH": "/usr/bin:/bin",
		"HOME": "/tmp",
		"LANG": "C.UTF-8",
	}
	seen := make(map[string]bool)
	for _, entry := range c.Env {
		name, value, ok := strings.Cut(entry, "=")
		if expected, exists := allowed[name]; !ok || !exists || expected != value || seen[name] {
			return false
		}
		seen[name] = true
	}
	return seen["PATH"] && seen["HOME"]
}

// Bound daemon output without retaining unbounded logs or sensitive text.
type githubSandboxInspectBuffer struct {
	bytes.Buffer
}

func (b *githubSandboxInspectBuffer) Write(p []byte) (int, error) {
	if len(p) > githubSandboxInspectLimit-b.Len() {
		return 0, errGitHubAuthorization
	}
	return b.Buffer.Write(p)
}
