package main

import (
	"errors"
	"time"
)

var errBrowserNode = errors.New("browser node admission rejected")

// Browser node status is derived from controller receipt time, never from
// a remote clock. This model does not authenticate a node or launch Chrome.
type browserNodeSnapshot struct {
	ID         string
	Generation uint64
	Sequence   uint64
	Capacity   int
	Occupied   int
	Draining   bool
	ReceivedAt time.Time
}

const browserHeartbeatTTL = 30 * time.Second

func (n browserNodeSnapshot) available(now time.Time) int {
	if n.Generation == 0 || n.Sequence == 0 || n.Draining || n.ReceivedAt.IsZero() ||
		now.Before(n.ReceivedAt) || now.Sub(n.ReceivedAt) >= browserHeartbeatTTL ||
		n.Capacity < 1 || n.Capacity > 256 || n.Occupied < 0 || n.Occupied > n.Capacity {
		return 0
	}
	return n.Capacity - n.Occupied
}

// A heartbeat can only update a previously provisioned identity and its
// current generation. Rotation is a distinct trusted operator operation.
func acceptBrowserHeartbeat(previous browserNodeSnapshot, id string, generation, sequence uint64, capacity, occupied int, now time.Time) (browserNodeSnapshot, error) {
	if !resourceUUID.MatchString(id) || previous.ID != id || generation == 0 || generation != previous.Generation ||
		sequence == 0 || sequence <= previous.Sequence || now.IsZero() ||
		(!previous.ReceivedAt.IsZero() && now.Before(previous.ReceivedAt)) ||
		capacity < 1 || capacity > 256 || occupied < 0 || occupied > capacity {
		return previous, errBrowserNode
	}
	previous.Sequence = sequence
	previous.Capacity = capacity
	previous.Occupied = occupied
	previous.ReceivedAt = now
	return previous, nil
}
