package cli

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

func runStatus() {
	// Check if relay is listening.
	port := "8090"
	if v := os.Getenv("PORT"); v != "" {
		port = v
	}

	running := isListening(port)

	if running {
		fmt.Printf("relay: %s (:%s)\n", bold("running"), port)
	} else {
		fmt.Printf("relay: %s\n", "stopped")
	}

	// Try to open DB for stats.
	d, err := openDBSafe()
	if err != nil {
		fmt.Println("db: not found")
		return
	}
	defer d.Close()

	stats, err := d.GetStats()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading stats: %v\n", err)
		return
	}

	// Agents line with names.
	agents, _ := d.ListAgents()
	if len(agents) > 0 {
		names := make([]string, len(agents))
		for i, a := range agents {
			names[i] = a.Name
		}
		fmt.Printf("agents: %d (%s)\n", stats.Agents, strings.Join(names, ", "))
	} else {
		fmt.Printf("agents: %d\n", stats.Agents)
	}

	fmt.Printf("unread: %d messages\n", stats.Unread)
}

func isListening(port string) bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
