package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type ScrapeTarget struct {
	URL     string `json:"url"`
	Service string `json:"service"`
}

type LogSource struct {
	Paths   []string `json:"paths"`
	Service string   `json:"service"`
	Format  string   `json:"format"` // text | docker | nextcloud
}

// NextcloudTarget is one Nextcloud instance to monitor remotely (status.php + serverinfo API).
// Auth for the serverinfo API: either Token (occ config:app:set serverinfo token) or an
// admin Username + app Password. LogPath optionally tails that instance's nextcloud.log.
type NextcloudTarget struct {
	URL      string `json:"url"`
	Service  string `json:"service"`
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
	LogPath  string `json:"log_path"`
}

type Config struct {
	URL              string            `json:"url"`
	APIKey           string            `json:"api_key"`
	HostService      string            `json:"host_service"`
	IntervalSeconds  int               `json:"interval_seconds"`
	HostMetrics      *bool             `json:"host_metrics"` // default true
	SelfMetrics      *bool             `json:"self_metrics"` // default true
	MetricsListen    string            `json:"metrics_listen"`
	Scrape           []ScrapeTarget    `json:"scrape"`
	Logs             []LogSource       `json:"logs"`
	Nextcloud        []NextcloudTarget `json:"nextcloud"`
	Systemd          *bool             `json:"systemd"`                     // report systemd services (default true)
	Redis            []RedisTarget     `json:"redis,omitempty"`             // passwords and addresses for Redis (from the Kluard UI); Redis in Docker is found by itself
	RedisPassword    string            `json:"redis_password,omitempty"`    // one password to try on every Redis that has none of its own
	Postgres         []PostgresTarget  `json:"postgres,omitempty"`          // logins and addresses for PostgreSQL (from the Kluard UI); PostgreSQL in Docker is found by itself
	Nginx            []NginxTarget     `json:"nginx,omitempty"`             // status pages for nginx (from the Kluard UI); nginx in Docker is found by itself
	Web              []WebTarget       `json:"web,omitempty"`               // addresses to check (from the Kluard UI)
	PostgresPassword string            `json:"postgres_password,omitempty"` // one password to try on every PostgreSQL that has none of its own
	Containers       *bool             `json:"containers"`                  // report Docker containers (default true)
	WatchServices    []string          `json:"watch_services"`              // services that must be running
	NoRemote         bool              `json:"no_remote"`                   // ignore the server's configuration
	RoleSetting      string            `json:"role"`                        // "host" or "checker"; empty = decided from what the agent does
	Hostname         string            `json:"hostname"`                    // the name this agent reports under; empty = the machine's own
	// AllowedLogDirs limits which log paths the SERVER may tell this agent to read (paths in the local config file are
	// always trusted). Default: /var/log, Docker's data dirs, /var/www, /srv, /mnt, /opt. Use ["*"] to allow everything.
	AllowedLogDirs   []string `json:"allowed_log_dirs"`
	RejectedLogPaths []string `json:"-"` // remote paths that were refused, for the agent's log and the UI warning
}

func (c Config) HostOn() bool { return c.HostMetrics == nil || *c.HostMetrics }

// Role is what this agent is for. A "host" reports on its machine. A "checker" only checks Nextcloud instances from
// somewhere (no host metrics, nothing of its own to report): it is not a machine and is not listed or counted as one.
func (c Config) Role() string {
	switch c.RoleSetting {
	case "host", "checker":
		return c.RoleSetting
	}
	if !c.HostOn() && len(c.Nextcloud) > 0 && len(c.WatchServices) == 0 {
		return "checker"
	}
	return "host"
}
func (c Config) SelfOn() bool       { return c.SelfMetrics == nil || *c.SelfMetrics }
func (c Config) SystemdOn() bool    { return c.Systemd == nil || *c.Systemd }
func (c Config) ContainersOn() bool { return c.Containers == nil || *c.Containers }

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// LoadConfig reads an optional JSON file, then applies KLUARD_AGENT_* environment variables
// (used by the Docker agent, which has no config file), then defaults.
func LoadConfig(path string, getenv func(string) string) (Config, error) {
	getenv = LegacyEnv(getenv)
	var c Config
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &c); err != nil {
			return c, fmt.Errorf("invalid config %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return c, err
	}
	if v := getenv("KLUARD_AGENT_URL"); v != "" {
		c.URL = v
	}
	if v := getenv("KLUARD_AGENT_API_KEY"); v != "" {
		c.APIKey = v
	}
	if truthy(getenv("KLUARD_AGENT_NO_REMOTE")) {
		c.NoRemote = true
	}
	if v := getenv("KLUARD_AGENT_SYSTEMD"); v != "" {
		b := truthy(v)
		c.Systemd = &b
	}
	if v := getenv("KLUARD_AGENT_CONTAINERS"); v != "" {
		b := truthy(v)
		c.Containers = &b
	}
	if v := getenv("KLUARD_AGENT_REDIS_PASSWORD"); v != "" {
		c.RedisPassword = v
	}
	if v := getenv("KLUARD_AGENT_POSTGRES_PASSWORD"); v != "" {
		c.PostgresPassword = v
	}
	if v := getenv("KLUARD_AGENT_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.IntervalSeconds = n
		}
	}
	if v := getenv("KLUARD_AGENT_METRICS_LISTEN"); v != "" {
		c.MetricsListen = v
	}
	if v := getenv("KLUARD_AGENT_ROLE"); v == "host" || v == "checker" {
		c.RoleSetting = v
	}
	if v := strings.TrimSpace(getenv("KLUARD_AGENT_HOSTNAME")); v != "" && len(v) <= 128 {
		c.Hostname = v
	}
	if v := getenv("KLUARD_AGENT_HOST_METRICS"); v != "" {
		b := truthy(v)
		c.HostMetrics = &b
	}
	if v := getenv("KLUARD_AGENT_LOG_PATHS"); v != "" {
		var paths []string
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				paths = append(paths, p)
			}
		}
		c.Logs = append(c.Logs, LogSource{Paths: paths, Service: "system-logs", Format: "text"})
	}
	if truthy(getenv("KLUARD_AGENT_DOCKER_LOGS")) {
		c.Logs = append(c.Logs, LogSource{Paths: []string{"/var/lib/docker/containers/*/*-json.log"}, Service: "docker", Format: "docker"})
	}
	if u := getenv("KLUARD_AGENT_NEXTCLOUD_URL"); u != "" {
		c.Nextcloud = append(c.Nextcloud, NextcloudTarget{
			URL: u, Service: getenv("KLUARD_AGENT_NEXTCLOUD_SERVICE"), Token: getenv("KLUARD_AGENT_NEXTCLOUD_TOKEN"),
			Username: getenv("KLUARD_AGENT_NEXTCLOUD_USER"), Password: getenv("KLUARD_AGENT_NEXTCLOUD_PASSWORD"),
			LogPath: getenv("KLUARD_AGENT_NEXTCLOUD_LOG"),
		})
	}
	if c.URL == "" {
		c.URL = "http://localhost:4318"
	}
	if c.HostService == "" {
		c.HostService = "host"
	}
	if c.IntervalSeconds < 1 {
		c.IntervalSeconds = 15
	}
	for i := range c.Nextcloud {
		if c.Nextcloud[i].Service == "" {
			c.Nextcloud[i].Service = "nextcloud"
		}
	}
	return c, nil
}

// LegacyEnv makes the old LUMEN_AGENT_* settings keep working next to the KLUARD_AGENT_* ones (the product used to be
// called Lumen): an agent installed before the rename, or a Docker agent started with the old command, is not broken
// by an update. The new name wins when both are set.
func LegacyEnv(get func(string) string) func(string) string {
	return func(k string) string {
		if v := get(k); v != "" {
			return v
		}
		if rest, ok := strings.CutPrefix(k, "KLUARD_"); ok {
			return get("LUMEN_" + rest)
		}
		return ""
	}
}
