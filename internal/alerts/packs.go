package alerts

// Pack is a set of starter rules for one kind of service, turned on with one button.
type Pack struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Desc  string   `json:"desc"`
	Rules []string `json:"rules"` // names of rules in Templates()
	// Signal is a metric whose presence shows that the service exists, so the pack can be suggested.
	Signal string `json:"signal"`
}

// Packs lists the starter packs. A pack never contains rules about Kluard itself.
func Packs() []Pack {
	return []Pack{
		{ID: "host", Signal: "system.cpu.utilization", Title: "Linux and Windows machines", Desc: "Warns when a machine stops reporting, a service fails, a disk fills up or the CPU stays busy.",
			Rules: []string{"Host down", "Service failed", "Disk almost full", "High CPU"}},
		{ID: "docker", Signal: "container_up", Title: "Docker", Desc: "Warns when a container stops, and when the machine it runs on is down or out of disk.",
			Rules: []string{"Host down", "Container down", "Disk almost full"}},
		{ID: "redis", Signal: "redis_up", Title: "Redis", Desc: "Warns when Redis does not answer, its memory is nearly full or it cannot save to disk.",
			Rules: []string{"Redis down", "Redis memory almost full", "Redis cannot save to disk"}},
		{ID: "postgres", Signal: "postgres_up", Title: "PostgreSQL", Desc: "Warns when PostgreSQL does not answer, connections run out, a transaction stays open for too long or a replica falls behind.",
			Rules: []string{"PostgreSQL down", "PostgreSQL connections almost used up", "PostgreSQL transaction open for too long", "PostgreSQL replica is behind"}},
		{ID: "minio", Signal: "minio_up", Title: "MinIO", Desc: "Warns when MinIO does not answer, its storage is nearly full, a drive is offline or the cluster is not healthy.",
			Rules: []string{"MinIO down", "MinIO storage almost full", "MinIO has a drive offline", "MinIO is not healthy"}},
		{ID: "webserver", Signal: "access_log_error_ratio", Title: "Web server errors", Desc: "Warns when a large share of the answers of a web server (nginx, Apache) are server errors. Needs the machine's Docker or web server logs to be read by the agent.",
			Rules: []string{"Many server errors on a web server"}},
		{ID: "mysql", Signal: "mysql_up", Title: "MySQL and MariaDB", Desc: "Warns when MySQL or MariaDB does not answer, connections run out, a query runs for too long or a replica stops or falls behind.",
			Rules: []string{"MySQL down", "MySQL connections almost used up", "MySQL query running for too long", "MySQL replication has stopped", "MySQL replica is behind"}},
		{ID: "web", Signal: "web_up", Title: "Websites", Desc: "Warns when a checked website does not answer, is slow or has a certificate that runs out.",
			Rules: []string{"Website down", "Website is slow", "Website certificate expires soon"}},
		{ID: "heartbeat", Signal: "heartbeat_up", Title: "Backups and jobs", Desc: "Warns when a job that calls a heartbeat address (a backup, a cron job) stops calling or says that it failed.",
			Rules: []string{"Heartbeat missed"}},
		{ID: "nginx", Signal: "nginx_up", Title: "nginx", Desc: "Warns when nginx does not answer.",
			Rules: []string{"nginx down"}},
		{ID: "nextcloud", Signal: "nextcloud_up", Title: "Nextcloud", Desc: "Warns when an instance is down or slow, background jobs stop, the login check fails, the certificate runs out or an update waits.",
			Rules: []string{"Nextcloud instance down", "Nextcloud response time", "Nextcloud background jobs not running", "Nextcloud login check failing", "TLS certificate expires soon", "TLS certificate not valid", "Nextcloud update available"}},
	}
}

// PackRules returns the rule templates of a pack, in the pack's order.
func PackRules(id string) ([]Rule, bool) {
	for _, p := range Packs() {
		if p.ID != id {
			continue
		}
		byName := map[string]Rule{}
		for _, t := range Templates() {
			byName[t.Name] = t
		}
		var out []Rule
		for _, n := range p.Rules {
			if t, ok := byName[n]; ok {
				out = append(out, t)
			}
		}
		return out, true
	}
	return nil, false
}
