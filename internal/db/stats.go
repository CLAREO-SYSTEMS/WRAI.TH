package db

// Stats holds aggregate relay statistics.
type Stats struct {
	Agents       int
	Messages     int
	Unread       int
	Threads      int
	OldestAgent  string // RFC3339 — earliest registered_at (proxy for uptime)
}

// GetStats returns aggregate counts from the database.
func (d *DB) GetStats() (*Stats, error) {
	s := &Stats{}

	err := d.conn.QueryRow("SELECT COUNT(*) FROM agents").Scan(&s.Agents)
	if err != nil {
		return nil, err
	}

	err = d.conn.QueryRow("SELECT COUNT(*) FROM messages").Scan(&s.Messages)
	if err != nil {
		return nil, err
	}

	err = d.conn.QueryRow("SELECT COUNT(*) FROM messages WHERE read_at IS NULL").Scan(&s.Unread)
	if err != nil {
		return nil, err
	}

	// Count distinct threads: a thread is a root message (reply_to IS NULL) that has at least one reply,
	// OR we just count distinct root IDs.
	err = d.conn.QueryRow(`
		SELECT COUNT(DISTINCT CASE WHEN reply_to IS NULL THEN id ELSE reply_to END)
		FROM messages
	`).Scan(&s.Threads)
	if err != nil {
		return nil, err
	}

	// Oldest agent registration as uptime proxy.
	var oldest *string
	err = d.conn.QueryRow("SELECT MIN(registered_at) FROM agents").Scan(&oldest)
	if err == nil && oldest != nil {
		s.OldestAgent = *oldest
	}

	return s, nil
}

// AgentCount returns just the number of agents (for lightweight status check).
func (d *DB) AgentCount() (int, error) {
	var n int
	err := d.conn.QueryRow("SELECT COUNT(*) FROM agents").Scan(&n)
	return n, err
}

// UnreadCount returns the total number of unread messages across all agents.
func (d *DB) UnreadCount() (int, error) {
	var n int
	err := d.conn.QueryRow("SELECT COUNT(*) FROM messages WHERE read_at IS NULL").Scan(&n)
	return n, err
}
