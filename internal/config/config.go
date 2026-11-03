// Package config loads runtime configuration from environment variables.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr           string // listen address, default :4318 (OTLP/HTTP standard port)
	ClickHouseURL  string // e.g. http://localhost:8123
	ClickHouseDB   string // default "lumen"
	ClickHouseUser string
	ClickHousePass string
	RetentionDays  int               // TTL applied to all tables
	APIKeys        map[string]string // api key -> tenant. Empty = dev mode (single tenant, no auth)
	PublicURL      string            // externally reachable base URL, baked into agent install scripts
	DistDir        string            // directory holding agent binaries served at /download
	DataDir        string            // where users, API keys and the session secret are stored (auth.json)
	DevMode        bool              // KLUARD_DEV_MODE=true: no authentication at all, single tenant "default"
	AdminUser      string            // optional bootstrap admin, created at startup if it does not exist
	AdminPassword  string
	AdminTenant    string
	// Elasticsearch (or OpenSearch) holds users, API keys and dashboards. Empty URL = JSON file in DataDir.
	SecretKey                                     string // encrypts stored instance credentials; falls back to the session secret when empty
	BackupDir                                     string // where daily exports of expiring data go ("" disables backups)
	BackupRetentionDays                           int    // how long backups are kept; 0 = forever
	ESURL, ESUser, ESPassword, ESAPIKey, ESPrefix string
	Alerts                                        bool          // KLUARD_ALERTS: alert rules and notifications (default on)
	AlertAllowPrivate                             bool          // KLUARD_ALERT_ALLOW_PRIVATE: let channels reach internal addresses (an internal mail relay)
	AlertGroupWait                                time.Duration // KLUARD_ALERT_GROUP_WAIT: how long related alerts are collected into one message
	AlertRepeat                                   time.Duration // KLUARD_ALERT_REPEAT: how often a firing alert is announced again
	OIDCAllowHTTP                                 bool          // KLUARD_OIDC_ALLOW_HTTP: accept a sign-in provider that is reached over plain http (one inside a closed network)
	AuditDays                                     int           // KLUARD_AUDIT_DAYS: how long the audit log is kept (default 365)
	BackupRoots                                   []string      // KLUARD_BACKUP_ROOTS: where a backup folder may be chosen (default /backup, /mnt, /media, /srv, /var/backups)
	DiskWarn, DiskCrit                            float64       // KLUARD_DISK_WARN / KLUARD_DISK_CRIT: percent full at which Kluard warns about its own disk (default 80 and 90)
	LicenseFile                                   string        // KLUARD_LICENSE_FILE: a licence file to use (a renewal can be dropped in as a file)
}

// getenv reads a KLUARD_ setting. The product used to be called Lumen and its settings LUMEN_*: an existing .env file or
// service unit with the old names keeps working, and the new name wins when both are set.
func getenv(k string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	if rest, ok := strings.CutPrefix(k, "KLUARD_"); ok {
		return os.Getenv("LUMEN_" + rest)
	}
	return ""
}

func env(k, def string) string {
	if v := getenv(k); v != "" {
		return v
	}
	return def
}

func Load() Config {
	days, err := strconv.Atoi(env("KLUARD_RETENTION_DAYS", "30"))
	if err != nil || days < 1 {
		days = 30
	}
	bdays, err := strconv.Atoi(env("KLUARD_BACKUP_RETENTION_DAYS", "365"))
	if err != nil || bdays < 0 {
		bdays = 365
	}
	dur := func(k string, def time.Duration) time.Duration {
		if d, err := time.ParseDuration(getenv(k)); err == nil && d > 0 {
			return d
		}
		return def
	}
	c := Config{
		LicenseFile:         getenv("KLUARD_LICENSE_FILE"),
		OIDCAllowHTTP:       getenv("KLUARD_OIDC_ALLOW_HTTP") == "1",
		AuditDays:           int(num("KLUARD_AUDIT_DAYS", 365)),
		BackupRoots:         list(getenv("KLUARD_BACKUP_ROOTS")),
		DiskWarn:            num("KLUARD_DISK_WARN", 80),
		DiskCrit:            num("KLUARD_DISK_CRIT", 90),
		Alerts:              getenv("KLUARD_ALERTS") != "false",
		AlertAllowPrivate:   getenv("KLUARD_ALERT_ALLOW_PRIVATE") == "true",
		AlertGroupWait:      dur("KLUARD_ALERT_GROUP_WAIT", 30*time.Second),
		AlertRepeat:         dur("KLUARD_ALERT_REPEAT", 4*time.Hour),
		BackupDir:           env("KLUARD_BACKUP_DIR", "/backup"),
		BackupRetentionDays: bdays,
		Addr:                env("KLUARD_ADDR", ":4318"),
		ClickHouseURL:       env("KLUARD_CLICKHOUSE_URL", "http://localhost:8123"),
		ClickHouseDB:        env("KLUARD_CLICKHOUSE_DB", "lumen"),
		ClickHouseUser:      env("KLUARD_CLICKHOUSE_USER", "default"),
		ClickHousePass:      getenv("KLUARD_CLICKHOUSE_PASSWORD"),
		RetentionDays:       days,
		APIKeys:             map[string]string{},
		PublicURL:           strings.TrimRight(getenv("KLUARD_PUBLIC_URL"), "/"),
		DistDir:             env("KLUARD_DIST_DIR", "/dist"),
		DataDir:             env("KLUARD_DATA_DIR", "/data"),
		DevMode:             getenv("KLUARD_DEV_MODE") == "true",
		AdminUser:           getenv("KLUARD_ADMIN_USER"),
		AdminPassword:       getenv("KLUARD_ADMIN_PASSWORD"),
		AdminTenant:         env("KLUARD_ADMIN_TENANT", "main"),
		SecretKey:           getenv("KLUARD_SECRET_KEY"),
		ESURL:               getenv("KLUARD_ELASTICSEARCH_URL"),
		ESUser:              getenv("KLUARD_ELASTICSEARCH_USER"),
		ESPassword:          getenv("KLUARD_ELASTICSEARCH_PASSWORD"),
		ESAPIKey:            getenv("KLUARD_ELASTICSEARCH_API_KEY"),
		ESPrefix:            env("KLUARD_ELASTICSEARCH_PREFIX", "lumen"),
	}
	// KLUARD_API_KEYS="key1:tenantA,key2:tenantB"
	for _, pair := range strings.Split(getenv("KLUARD_API_KEYS"), ",") {
		k, t, ok := strings.Cut(strings.TrimSpace(pair), ":")
		if ok && k != "" && t != "" {
			c.APIKeys[k] = t
		}
	}
	return c
}

// num reads a number from the environment, or uses the default.
func num(key string, def float64) float64 {
	if v := getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// list reads a comma separated list from the environment; empty gives nil.
func list(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
