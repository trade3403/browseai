package main

import (
	"strings"
	"testing"
)

func TestGitHubSandboxInspect(t *testing.T) {
	info := `{"OSType":"linux","CgroupVersion":"2","CgroupDriver":"systemd","MemoryLimit":true,"SwapLimit":true,"CPUCfsQuota":true,"PidsLimit":true,"SecurityOptions":["name=rootless","name=seccomp,profile=builtin"]}`
	if !githubSandboxRuntimeValid([]byte(info)) {
		t.Fatal("valid runtime rejected")
	}
	for _, pair := range [][2]string{
		{"name=rootless", "name=apparmor"},
		{"name=seccomp,profile=builtin", "name=seccomp,profile=unconfined"},
		{`"MemoryLimit":true`, `"MemoryLimit":false`},
		{`"SwapLimit":true`, `"SwapLimit":false`},
		{`"CPUCfsQuota":true`, `"CPUCfsQuota":false`},
		{`"PidsLimit":true`, `"PidsLimit":false`},
		{`"CgroupVersion":"2"`, `"CgroupVersion":"1"`},
		{`"CgroupDriver":"systemd"`, `"CgroupDriver":"cgroupfs"`},
	} {
		if githubSandboxRuntimeValid([]byte(strings.Replace(info, pair[0], pair[1], 1))) {
			t.Fatal("unsafe runtime accepted", pair)
		}
	}
	image := "sha256:" + strings.Repeat("a", 64)
	raw := `[{"Id":"` + image + `","Os":"linux","Config":{"User":"65532:65532","Env":["PATH=/usr/bin:/bin","HOME=/tmp"],"Entrypoint":["/opt/browseai/trusted-test"],"Cmd":null,"WorkingDir":"/","Volumes":null,"OnBuild":null}}]`
	if !githubSandboxImageConfigValid([]byte(raw), image) {
		t.Fatal("valid image rejected")
	}
	for _, pair := range [][2]string{
		{image, "sha256:" + strings.Repeat("b", 64)},
		{`"User":"65532:65532"`, `"User":"0"`},
		{`"Volumes":null`, `"Volumes":{"/data":{}}`},
		{`"OnBuild":null`, `"OnBuild":["RUN evil"]`},
		{`"Cmd":null`, `"Cmd":["unexpected"]`},
		{`"WorkingDir":"/"`, `"WorkingDir":"/repo"`},
		{`"HOME=/tmp"`, `"HOME=/tmp","LD_PRELOAD=/bad.so"`},
		{`"HOME=/tmp"`, `"HOME=/tmp","HOME=/tmp"`},
		{`"HOME=/tmp"`, `"HOME=/root"`},
		{`"/opt/browseai/trusted-test"`, `"/bin/sh"`},
		{`"Volumes":null`, `"Volumes":null,"Healthcheck":{"Test":["CMD","evil"]}`},
		{`"Volumes":null`, `"Volumes":null,"StopSignal":"SIGKILL"`},
	} {
		if githubSandboxImageConfigValid([]byte(strings.Replace(raw, pair[0], pair[1], 1)), image) {
			t.Fatal("unsafe image accepted", pair)
		}
	}
	for _, bad := range []string{"", "null", "[]", "{}", "[", strings.Repeat("x", githubSandboxInspectLimit+1)} {
		if githubSandboxImageConfigValid([]byte(bad), image) || githubSandboxRuntimeValid([]byte(bad)) {
			t.Fatal("malformed inspection accepted")
		}
	}
	var buffer githubSandboxInspectBuffer
	if _, err := buffer.Write(make([]byte, githubSandboxInspectLimit)); err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.Write([]byte("x")); err == nil {
		t.Fatal("unbounded inspection output")
	}
}
