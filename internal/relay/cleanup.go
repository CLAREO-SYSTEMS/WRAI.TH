package relay

import (
	"log"
	"time"

	"agent-relay/internal/db"
)

const (
	// PurgeInterval is how often the cleanup runs.
	PurgeInterval = 5 * time.Minute
	// AgentMaxAge is how long an agent can be inactive before being purged.
	AgentMaxAge = 30 * time.Minute
)

// StartCleanup runs a background goroutine that purges stale agents.
// It stops when the done channel is closed.
func StartCleanup(database *db.DB, done <-chan struct{}) {
	ticker := time.NewTicker(PurgeInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				n, err := database.PurgeStaleAgents(AgentMaxAge)
				if err != nil {
					log.Printf("cleanup error: %v", err)
				} else if n > 0 {
					log.Printf("purged %d stale agent(s)", n)
				}
			}
		}
	}()
}
