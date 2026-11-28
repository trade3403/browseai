package main

import (
	"testing"
	"time"
)

func TestBrowserHeartbeatAdmission(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	now := time.Unix(1000, 0)
	initial := browserNodeSnapshot{ID: id, Generation: 2}
	n, err := acceptBrowserHeartbeat(initial, id, 2, 1, 4, 1, now)
	if err != nil || n.available(now) != 3 {
		t.Fatal("valid heartbeat rejected", err)
	}
	for _, bad := range []struct {
		id         string
		generation uint64
		sequence   uint64
		capacity   int
		occupied   int
		now        time.Time
	}{
		{"bad", 2, 2, 4, 1, now},
		{id, 1, 2, 4, 1, now},
		{id, 2, 1, 4, 1, now},
		{id, 2, 2, 257, 1, now},
		{id, 2, 2, 4, 5, now},
		{id, 2, 2, 4, -1, now},
		{id, 2, 2, 4, 1, now.Add(-time.Second)},
	} {
		got, err := acceptBrowserHeartbeat(n, bad.id, bad.generation, bad.sequence, bad.capacity, bad.occupied, bad.now)
		if err == nil || got != n {
			t.Fatal("invalid heartbeat changed node")
		}
	}
	if n.available(now.Add(browserHeartbeatTTL)) != 0 || n.available(now.Add(-time.Second)) != 0 {
		t.Fatal("stale or future heartbeat available")
	}
	n.Draining = true
	n, err = acceptBrowserHeartbeat(n, id, 2, 2, 4, 0, now.Add(time.Second))
	if err != nil || !n.Draining || n.available(now.Add(time.Second)) != 0 {
		t.Fatal("heartbeat cleared operator drain", err)
	}
}
