// Command wraith runs the WRAI.TH fleet manager + Mission Control dashboard.
// This is the client binary — it connects to a running agent-relay server
// and manages Claude subprocesses on this machine.
//
// Station mode: full manager + dashboard + discord bridge
// Satellite mode: headless HTTP server that receives commands from the station
//
// Usage:
//
//	wraith                     # start with wraith.yaml in cwd
//	wraith --config path.yaml  # custom config
//	wraith init                # generate wraith.yaml scaffold
//	wraith init --project foo  # with project name
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"agent-relay/internal/client"
	"agent-relay/internal/dashboard"
	"agent-relay/internal/discord"
	"agent-relay/internal/monitor"
)

var Version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "init" {
		runInit(os.Args[2:])
		return
	}

	configPath := flag.String("config", "wraith.yaml", "path to wraith config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("wraith %s\n", Version)
		return
	}

	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// --- Load config ---
	cfg, err := client.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	log.Printf("[wraith] mode=%s machine=%s relay=%s", cfg.Mode, cfg.Machine.Name, cfg.Relay.URL)

	// --- Signal handling ---
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	tracker := monitor.NewTracker(cfg.Machine.DownloadDir)

	// --- Periodic tracker save (every 5 minutes) ---
	saveTicker := time.NewTicker(5 * time.Minute)
	go func() {
		for {
			select {
			case <-saveTicker.C:
				tracker.Save()
			case <-ctx.Done():
				return
			}
		}
	}()

	if cfg.IsSatellite() {
		runSatellite(ctx, cfg, tracker)
	} else {
		runStation(ctx, cfg, tracker)
	}

	saveTicker.Stop()
	log.Printf("[wraith] stopped")
}

// runStation runs the full station: manager + dashboard + discord.
func runStation(ctx context.Context, cfg *client.Config, tracker *monitor.Tracker) {
	mgr := client.NewManager(cfg)
	mgr.SetTracker(tracker)

	relay := mgr.Relay()

	if err := mgr.Start(ctx); err != nil {
		log.Fatalf("failed to start manager: %v", err)
	}

	// Discord bridge
	var bot *discord.Bot
	if cfg.Discord.Enabled && cfg.Discord.Token != "" {
		sseClient := mgr.SSE()
		var err error
		bot, err = discord.NewBot(cfg, relay, sseClient)
		if err != nil {
			log.Printf("[wraith] failed to create discord bot: %v", err)
		} else {
			if err := bot.Start(); err != nil {
				log.Printf("[wraith] failed to start discord bot: %v", err)
				bot = nil
			}
		}
	}

	// Dashboard
	dash := dashboard.NewServer(mgr, cfg, tracker, relay)
	dashAddr := fmt.Sprintf("%s:%d", cfg.Web.Host, cfg.Web.Port)
	dashServer := &http.Server{
		Addr:    dashAddr,
		Handler: dash.Handler(),
	}

	go func() {
		log.Printf("[wraith] Mission Control at http://%s", dashAddr)
		if err := dashServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[wraith] dashboard server error: %v", err)
		}
	}()

	// Wait for shutdown
	<-ctx.Done()
	log.Printf("[wraith] shutting down...")

	if bot != nil {
		bot.Stop()
	}

	mgr.Stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dashServer.Shutdown(shutdownCtx)
}

// runSatellite runs a headless satellite: HTTP API only, no SSE, no relay, no dashboard.
func runSatellite(ctx context.Context, cfg *client.Config, tracker *monitor.Tracker) {
	satServer := client.NewSatelliteServer(cfg, tracker, ctx)

	satAddr := fmt.Sprintf("%s:%d", cfg.Web.Host, cfg.StdoutAPI.Port)
	httpServer := &http.Server{
		Addr:    satAddr,
		Handler: satServer.Handler(),
	}

	go func() {
		log.Printf("[wraith] satellite API at http://%s", satAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[wraith] satellite server error: %v", err)
		}
	}()

	// Register with station
	if err := satServer.RegisterWithStation(); err != nil {
		log.Printf("[wraith] station registration failed: %v (will retry when station comes online)", err)
	}

	// Wait for shutdown
	<-ctx.Done()
	log.Printf("[wraith] shutting down satellite...")

	satServer.Stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpServer.Shutdown(shutdownCtx)
}

// runInit generates a wraith.yaml scaffold in the current directory.
func runInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	project := fs.String("project", "", "project name (default: directory name)")
	relay := fs.String("relay", "http://localhost:8090", "relay server URL")
	machine := fs.String("machine", "", "machine name (default: hostname)")
	fs.Parse(args)

	if *project == "" {
		cwd, _ := os.Getwd()
		*project = strings.ToLower(filepath.Base(cwd))
		*project = strings.ReplaceAll(*project, " ", "-")
	}

	if *machine == "" {
		h, _ := os.Hostname()
		if h == "" {
			h = "localhost"
		}
		*machine = strings.ToLower(h)
	}

	outPath := "wraith.yaml"
	if _, err := os.Stat(outPath); err == nil {
		fmt.Fprintf(os.Stderr, "%s already exists. Remove it first or edit manually.\n", outPath)
		os.Exit(1)
	}

	cwd, _ := os.Getwd()

	config := fmt.Sprintf(`# WRAI.TH Fleet Manager Configuration
# Generated by: wraith init --project %s

mode: station  # "station" (runs relay+dashboard+discord) or "satellite" (headless executor)

relay:
  url: %s
  project: %s
  # api_key: ${RELAY_API_KEY}  # uncomment if auth is enabled

machine:
  name: %s
  download_dir: ./data

web:
  host: 0.0.0.0
  port: 8091

# Satellite API port (satellite mode only — station sends commands here)
stdout_api:
  port: 8092

# Discord bridge (station mode only)
discord:
  enabled: false
  # token: ${DISCORD_TOKEN}
  # guild_id: "your-guild-id"
  # channels:
  #   engineering: "channel-id"

# Remote satellites (station mode only)
# satellites:
#   remote-machine:
#     host: "100.x.x.x"
#     port: 8092

# Agent definitions (station mode only — satellite receives configs from station)
agents:
  cto:
    profile_slug: cto
    role: "Technical leader. Owns the backlog, coordinates teams."
    reports_to: ""
    is_executive: true
    work_dir: %s
    machine: %s
    pool: engineering
    model: sonnet
    auto_spawn: true
    max_context_bytes: 16384
    interest_tags: [architecture, planning, coordination]

# Team pools (for team: broadcasts)
pools:
  engineering:
    lead: cto
    members: []

# SSE tuning
sse:
  reconnect_delay_seconds: 3
  fallback_poll_seconds: 10
  health_check_interval_seconds: 30
`, *project, *relay, *project, *machine, cwd, *machine)

	if err := os.WriteFile(outPath, []byte(config), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write %s: %v\n", outPath, err)
		os.Exit(1)
	}

	fmt.Printf("Created %s for project %q on machine %q\n", outPath, *project, *machine)
	fmt.Printf("  relay: %s\n", *relay)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  1. Edit %s — add your agents, set work_dir paths\n", outPath)
	fmt.Printf("  2. Run: wraith\n")
	fmt.Printf("  3. Open Mission Control: http://localhost:8091\n")
}
