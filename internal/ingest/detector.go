package ingest

import (
	"context"
	"sync"
	"time"
)

const (
	waitingThreshold = 10 * time.Second
	idleThreshold    = 30 * time.Second
	exitThreshold    = 5 * time.Minute
	tickInterval     = 5 * time.Second
)

type SessionState struct {
	SessionID string    `json:"session_id"`
	Activity  Activity  `json:"activity"`
	Tool      string    `json:"tool"`
	File      string    `json:"file"`
	LastEvent time.Time `json:"last_event"`
	State     string    `json:"state"` // "active", "idle", "waiting", "exited"
}

type sessionEntry struct {
	lastEvent time.Time
	lastType  EventType
	tool      string
	file      string
	activity  Activity
	state     string
	idleSent  bool
	waitSent  bool
}

type Detector struct {
	mu       sync.RWMutex
	sessions map[string]*sessionEntry
	out      chan<- AgentEvent
}

func newDetector(out chan<- AgentEvent) *Detector {
	return &Detector{
		sessions: make(map[string]*sessionEntry),
		out:      out,
	}
}

func (d *Detector) RecordEvent(evt AgentEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()

	s, ok := d.sessions[evt.SessionID]
	if !ok {
		s = &sessionEntry{}
		d.sessions[evt.SessionID] = s
	}

	s.lastEvent = evt.Timestamp
	s.lastType = evt.Type
	s.tool = evt.Tool
	s.file = evt.File
	s.activity = evt.Activity
	s.state = "active"
	s.idleSent = false
	s.waitSent = false
}

func (d *Detector) GetSessions() []SessionState {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make([]SessionState, 0, len(d.sessions))
	for sid, s := range d.sessions {
		result = append(result, SessionState{
			SessionID: sid,
			Activity:  s.activity,
			Tool:      s.tool,
			File:      s.file,
			LastEvent: s.lastEvent,
			State:     s.state,
		})
	}
	return result
}

func (d *Detector) run(ctx context.Context) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			d.tick(now)
		}
	}
}

func (d *Detector) tick(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for sid, s := range d.sessions {
		elapsed := now.Sub(s.lastEvent)

		if elapsed > exitThreshold {
			if s.state != "exited" {
				s.state = "exited"
				s.activity = ActivityIdle
				d.out <- AgentEvent{
					Type:      EventAgentExit,
					SessionID: sid,
					Activity:  ActivityIdle,
					Timestamp: now,
				}
			}
			delete(d.sessions, sid)
			continue
		}

		if elapsed > idleThreshold && !s.idleSent {
			s.idleSent = true
			s.state = "idle"
			s.activity = ActivityIdle
			d.out <- AgentEvent{
				Type:      EventIdle,
				SessionID: sid,
				Activity:  ActivityIdle,
				Timestamp: now,
			}
			continue
		}

		if elapsed > waitingThreshold && s.lastType == EventToolEnd && !s.waitSent {
			s.waitSent = true
			s.state = "waiting"
			s.activity = ActivityWaiting
			d.out <- AgentEvent{
				Type:      EventWaiting,
				SessionID: sid,
				Activity:  ActivityWaiting,
				Timestamp: now,
			}
		}
	}
}
