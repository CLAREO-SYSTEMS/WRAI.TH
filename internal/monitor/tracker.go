// Package monitor tracks token costs per agent turn.
// Stores cost data in-memory with periodic snapshots to JSON.
package monitor

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TurnRecord captures cost data for a single agent turn.
type TurnRecord struct {
	Agent        string    `json:"agent"`
	Model        string    `json:"model"`
	CostUSD      float64   `json:"cost_usd"`
	Duration     float64   `json:"duration_s"`
	Turn         int       `json:"turn"`
	Time         time.Time `json:"time"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
}

// AgentStats holds aggregated stats for one agent.
type AgentStats struct {
	TotalCostUSD      float64       `json:"total_cost_usd"`
	TurnCount         int           `json:"turn_count"`
	AvgCostUSD        float64       `json:"avg_cost_usd"`
	LastModel         string        `json:"last_model"`
	LastTurnAt        time.Time     `json:"last_turn_at"`
	Turns             []TurnRecord  `json:"turns"`
	TotalInputTokens  int           `json:"total_input_tokens"`
	TotalOutputTokens int           `json:"total_output_tokens"`
}

// DailyBucket holds per-day aggregation.
type DailyBucket struct {
	Date     string  `json:"date"` // YYYY-MM-DD
	CostUSD  float64 `json:"cost_usd"`
	Turns    int     `json:"turns"`
}

// Tracker accumulates cost data across all agents.
type Tracker struct {
	records  []TurnRecord
	dataDir  string
	mu       sync.RWMutex
}

// NewTracker creates a cost tracker that persists to dataDir.
func NewTracker(dataDir string) *Tracker {
	t := &Tracker{
		dataDir: dataDir,
	}
	t.load()
	return t
}

// Record adds a turn's cost data.
func (t *Tracker) Record(agent, model string, costUSD float64, duration time.Duration, turn, inputTokens, outputTokens int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.records = append(t.records, TurnRecord{
		Agent:        agent,
		Model:        model,
		CostUSD:      costUSD,
		Duration:     duration.Seconds(),
		Turn:         turn,
		Time:         time.Now(),
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	})
}

// AgentSummaries returns per-agent stats.
func (t *Tracker) AgentSummaries() map[string]*AgentStats {
	t.mu.RLock()
	defer t.mu.RUnlock()

	stats := make(map[string]*AgentStats)
	for _, r := range t.records {
		s, ok := stats[r.Agent]
		if !ok {
			s = &AgentStats{}
			stats[r.Agent] = s
		}
		s.TotalCostUSD += r.CostUSD
		s.TurnCount++
		s.LastModel = r.Model
		s.LastTurnAt = r.Time
		s.Turns = append(s.Turns, r)
		s.TotalInputTokens += r.InputTokens
		s.TotalOutputTokens += r.OutputTokens
	}

	for _, s := range stats {
		if s.TurnCount > 0 {
			s.AvgCostUSD = s.TotalCostUSD / float64(s.TurnCount)
		}
	}

	return stats
}

// TotalCost returns the sum of all recorded costs.
func (t *Tracker) TotalCost() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var total float64
	for _, r := range t.records {
		total += r.CostUSD
	}
	return total
}

// DailyBreakdown returns costs aggregated by day.
func (t *Tracker) DailyBreakdown() []DailyBucket {
	t.mu.RLock()
	defer t.mu.RUnlock()

	buckets := make(map[string]*DailyBucket)
	for _, r := range t.records {
		day := r.Time.Format("2006-01-02")
		b, ok := buckets[day]
		if !ok {
			b = &DailyBucket{Date: day}
			buckets[day] = b
		}
		b.CostUSD += r.CostUSD
		b.Turns++
	}

	var result []DailyBucket
	for _, b := range buckets {
		result = append(result, *b)
	}
	return result
}

// Save persists records to disk.
func (t *Tracker) Save() {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if len(t.records) == 0 {
		return
	}

	os.MkdirAll(t.dataDir, 0755)
	path := filepath.Join(t.dataDir, "cost-history.json")

	data, err := json.MarshalIndent(t.records, "", "  ")
	if err != nil {
		log.Printf("[monitor] failed to marshal cost data: %v", err)
		return
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Printf("[monitor] failed to save cost data: %v", err)
	}
}

// AgentTokens returns total tokens used by an agent.
func (t *Tracker) AgentTokens(agent string) (input, output int) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, r := range t.records {
		if r.Agent == agent {
			input += r.InputTokens
			output += r.OutputTokens
		}
	}
	return
}

func (t *Tracker) load() {
	path := filepath.Join(t.dataDir, "cost-history.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var records []TurnRecord
	if err := json.Unmarshal(data, &records); err != nil {
		log.Printf("[monitor] failed to load cost data: %v", err)
		return
	}

	t.records = records
	log.Printf("[monitor] loaded %d cost records", len(records))
}
