// Package client implements the WRAI.TH agent lifecycle manager.
// It spawns, wakes, and manages Claude Code subprocesses based on
// relay events (messages, tasks, activity).
package client

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration for the WRAI.TH client.
type Config struct {
	Mode       string                    `yaml:"mode"`       // "station" or "satellite"
	Relay      RelayConfig               `yaml:"relay"`
	Station    StationConfig             `yaml:"station"`    // satellite-only: URL of the station to register with
	Machine    MachineConfig             `yaml:"machine"`
	Web        WebConfig                 `yaml:"web"`
	StdoutAPI  StdoutAPIConfig           `yaml:"stdout_api"`
	Discord    DiscordConfig             `yaml:"discord"`
	Satellites map[string]SatelliteInfo  `yaml:"satellites"`
	Pools      map[string]PoolConfig     `yaml:"pools"`
	Humans     map[string]HumanConfig    `yaml:"humans"`
	Agents     map[string]AgentConfig    `yaml:"agents"`
	SSE        SSEConfig                 `yaml:"sse"`
	Tokens     TokenConfig               `yaml:"tokens"`
}

type StationConfig struct {
	URL string `yaml:"url"` // e.g. "http://100.66.244.118:8091"
}

type RelayConfig struct {
	URL     string `yaml:"url"`
	Project string `yaml:"project"`
	APIKey  string `yaml:"api_key"`
}

type MachineConfig struct {
	Name        string `yaml:"name"`
	DownloadDir string `yaml:"download_dir"`
}

type WebConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

type StdoutAPIConfig struct {
	Port int `yaml:"port"`
}

type DiscordConfig struct {
	Enabled  bool              `yaml:"enabled"`
	Token    string            `yaml:"token"`
	GuildID  string            `yaml:"guild_id"`
	Channels map[string]string `yaml:"channels"` // pool_name -> channel_id
}

type SatelliteInfo struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type PoolConfig struct {
	Channel string   `yaml:"channel"`
	Lead    string   `yaml:"lead"`
	Members []string `yaml:"members"`
}

type HumanConfig struct {
	Name        string   `yaml:"name"`
	Role        string   `yaml:"role"`
	DiscordID   string   `yaml:"discord_id"`
	IsExecutive bool     `yaml:"is_executive"`
	Pools       []string `yaml:"pools"`
}

type AgentConfig struct {
	ProfileSlug     string   `yaml:"profile_slug"`
	Role            string   `yaml:"role"`
	ReportsTo       string   `yaml:"reports_to"`
	IsExecutive     bool     `yaml:"is_executive"`
	WorkDir         string   `yaml:"work_dir"`
	Machine         string   `yaml:"machine"`
	Pool            string   `yaml:"pool"`
	IdleTimeoutSec  int      `yaml:"idle_timeout_seconds"`
	AutoSpawn       bool     `yaml:"auto_spawn"`
	InterestTags    []string `yaml:"interest_tags"`
	MaxContextBytes int      `yaml:"max_context_bytes"`
	MaxBudgetUSD    string   `yaml:"max_budget_usd"`
	Model           string   `yaml:"model"`
}

type SSEConfig struct {
	ReconnectDelaySec    float64 `yaml:"reconnect_delay_seconds"`
	FallbackPollSec      float64 `yaml:"fallback_poll_seconds"`
	HealthCheckIntervalSec float64 `yaml:"health_check_interval_seconds"`
}

type TokenConfig struct {
	DailyLimitPerAgent *int    `yaml:"daily_limit_per_agent"`
	WarningThreshold   float64 `yaml:"warning_threshold"`
}

// Defaults applies sane defaults to unset fields.
func (c *Config) Defaults() {
	if c.Mode == "" {
		c.Mode = "station"
	}
	if c.Relay.URL == "" {
		c.Relay.URL = "http://localhost:8090"
	}
	if c.Relay.Project == "" {
		c.Relay.Project = "default"
	}
	if c.Machine.Name == "" {
		c.Machine.Name = "localhost"
	}
	if c.Machine.DownloadDir == "" {
		c.Machine.DownloadDir = "./data/downloads"
	}
	if c.Web.Port == 0 {
		c.Web.Port = 8091
	}
	if c.Web.Host == "" {
		c.Web.Host = "0.0.0.0"
	}
	if c.StdoutAPI.Port == 0 {
		c.StdoutAPI.Port = 8092
	}
	if c.SSE.ReconnectDelaySec == 0 {
		c.SSE.ReconnectDelaySec = 3
	}
	if c.SSE.FallbackPollSec == 0 {
		c.SSE.FallbackPollSec = 10
	}
	if c.SSE.HealthCheckIntervalSec == 0 {
		c.SSE.HealthCheckIntervalSec = 30
	}
	if c.Tokens.WarningThreshold == 0 {
		c.Tokens.WarningThreshold = 0.8
	}

	// Agent defaults
	for name, agent := range c.Agents {
		if agent.IdleTimeoutSec == 0 {
			agent.IdleTimeoutSec = 300
		}
		if agent.MaxBudgetUSD == "" {
			agent.MaxBudgetUSD = "0.50"
		}
		if agent.Model == "" {
			agent.Model = "sonnet"
		}
		if agent.MaxContextBytes == 0 {
			agent.MaxContextBytes = 8192
		}
		c.Agents[name] = agent
	}
}

// IsStation returns true if running in station mode.
func (c *Config) IsStation() bool { return c.Mode == "station" }

// IsSatellite returns true if running in satellite mode.
func (c *Config) IsSatellite() bool { return c.Mode == "satellite" }

// LocalAgents returns only agents configured for this machine.
func (c *Config) LocalAgents() map[string]AgentConfig {
	local := make(map[string]AgentConfig)
	for name, agent := range c.Agents {
		if agent.Machine == c.Machine.Name {
			local[name] = agent
		}
	}
	return local
}

// AgentPool returns the pool config for a given agent, or nil.
func (c *Config) AgentPool(agentName string) *PoolConfig {
	agent, ok := c.Agents[agentName]
	if !ok {
		return nil
	}
	pool, ok := c.Pools[agent.Pool]
	if !ok {
		return nil
	}
	return &pool
}

// LoadConfig reads and parses a YAML config file with env var interpolation.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	// Interpolate ${ENV_VAR} patterns
	raw := string(data)
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			raw = strings.ReplaceAll(raw, "${"+parts[0]+"}", parts[1])
		}
	}

	var cfg Config
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.Defaults()
	return &cfg, nil
}
