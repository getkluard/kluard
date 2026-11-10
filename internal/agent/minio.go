package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MinIO monitoring: the agent finds MinIO in Docker by itself and reads its Prometheus endpoint (/minio/v2/metrics/cluster).
// A MinIO started with MINIO_PROMETHEUS_AUTH_TYPE=public answers without a login. Otherwise it wants a token, which
// `mc admin prometheus generate` makes, and which is set in the Kluard UI (stored encrypted, sent only to this agent).

// MinioTarget is one MinIO to check.
type MinioTarget struct {
	Name      string `json:"name"`
	Container string `json:"container,omitempty"`
	URL       string `json:"url,omitempty"` // the address of MinIO itself, e.g. http://10.0.0.5:9000
	Token     string `json:"token,omitempty"`
}

var errMinioAuth = errors.New("minio wants a token for its metrics")

// minioMetrics are the metrics that are kept, and what each is. MinIO reports many more; these are the ones that say
// whether storage is healthy, full or failing.
var minioMetrics = map[string]string{
	"minio_cluster_capacity_usable_free_bytes":  "gauge",
	"minio_cluster_capacity_usable_total_bytes": "gauge",
	"minio_cluster_capacity_raw_free_bytes":     "gauge",
	"minio_cluster_capacity_raw_total_bytes":    "gauge",
	"minio_cluster_usage_total_bytes":           "gauge",
	"minio_cluster_usage_object_total":          "gauge",
	"minio_cluster_bucket_total":                "gauge",
	"minio_cluster_drive_total":                 "gauge",
	"minio_cluster_drive_online_total":          "gauge",
	"minio_cluster_drive_offline_total":         "gauge",
	"minio_cluster_nodes_online_total":          "gauge",
	"minio_cluster_nodes_offline_total":         "gauge",
	"minio_cluster_health_status":               "gauge",
	"minio_s3_requests_inflight_total":          "gauge",
	"minio_s3_requests_waiting_total":           "gauge",
	"minio_s3_requests_total":                   "sum",
	"minio_s3_requests_errors_total":            "sum",
	"minio_s3_requests_4xx_errors_total":        "sum",
	"minio_s3_requests_5xx_errors_total":        "sum",
	"minio_s3_traffic_received_bytes":           "sum",
	"minio_s3_traffic_sent_bytes":               "sum",
	"minio_heal_objects_errors_total":           "sum",
}

// minioImage says whether a Docker image is a MinIO server (and not the mc client, an operator or a console).
func minioImage(image string) bool {
	img := strings.ToLower(image)
	if i := strings.LastIndex(img, "/"); i >= 0 {
		img = img[i+1:]
	}
	img = strings.SplitN(img, ":", 2)[0]
	img = strings.SplitN(img, "@", 2)[0]
	for _, bad := range []string{"exporter", "operator", "console", "client", "mc", "sidekick", "warp", "gateway"} {
		if img == bad || (len(bad) > 2 && strings.Contains(img, bad)) {
			return false
		}
	}
	return strings.Contains(img, "minio")
}

// DiscoveredMinio is a MinIO found in a Docker container.
type DiscoveredMinio struct {
	Container string
	Image     string
	Bases     []string
}

func discoverMinio(ctx context.Context, socket string) ([]DiscoveredMinio, error) {
	if socket == "" {
		socket = dockerSocket()
	}
	if _, err := os.Stat(socket); err != nil {
		return nil, ErrNoDocker
	}
	cl := dockerClient(socket)
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://docker/containers/json", nil)
	resp, err := cl.Do(req)
	if err != nil {
		if errors.Is(err, os.ErrPermission) || strings.Contains(err.Error(), "permission denied") {
			return nil, fmt.Errorf("no access to the Docker socket %s: add the agent's user to the docker group (re-run the installer with --docker)", socket)
		}
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("docker API: %s", resp.Status)
	}
	var cs []dockerRedisContainer
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 8<<20)).Decode(&cs); err != nil {
		return nil, err
	}
	return minioFromContainers(cs), nil
}

func minioFromContainers(cs []dockerRedisContainer) []DiscoveredMinio {
	var out []DiscoveredMinio
	for _, c := range cs {
		if c.State != "running" || len(c.Names) == 0 || !minioImage(c.Image) || c.Labels["kluard.minio"] == "false" {
			continue
		}
		port, public := 9000, 0
		for _, p := range c.Ports {
			if p.PrivatePort == 9000 && p.PublicPort != 0 && public == 0 {
				public = p.PublicPort
			}
		}
		if v := c.Labels["kluard.minio.port"]; v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 65536 {
				port, public = n, 0
			}
		}
		d := DiscoveredMinio{Container: strings.TrimPrefix(c.Names[0], "/"), Image: c.Image}
		var nets []string
		for n := range c.NetworkSettings.Networks {
			nets = append(nets, n)
		}
		sort.Strings(nets)
		for _, n := range nets {
			if ip := c.NetworkSettings.Networks[n].IPAddress; ip != "" {
				d.Bases = append(d.Bases, "http://"+net.JoinHostPort(ip, strconv.Itoa(port)))
			}
		}
		if public != 0 {
			d.Bases = append(d.Bases, "http://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(public)))
		}
		out = append(out, d)
	}
	return out
}

// MinioCheck reads the metrics of one MinIO (base is its address) and returns the kept ones.
func MinioCheck(ctx context.Context, base, token string, now int64) ([]Point, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(base, "/")+"/minio/v2/metrics/cluster", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "kluard-agent")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := nginxClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 200:
	case 401, 403:
		return nil, errMinioAuth
	case 404:
		return nil, errors.New("this does not look like MinIO (no /minio/v2/metrics/cluster)")
	default:
		return nil, fmt.Errorf("MinIO answered %s", resp.Status)
	}
	var out []Point
	for _, p := range ParseProm(io.LimitReader(resp.Body, 16<<20), "host", now) {
		typ, keep := minioMetrics[p.Name]
		if !keep {
			continue
		}
		p.Type = typ
		out = append(out, p)
		if len(out) >= 600 {
			break
		}
	}
	if len(out) == 0 {
		return nil, errors.New("MinIO answered but reported no cluster metrics")
	}
	return out, nil
}

func minioNamed(points []Point, name string) (float64, bool) {
	var v float64
	var ok bool
	for _, p := range points {
		if p.Name == name {
			v += p.Value
			ok = true
		}
	}
	return v, ok
}

// CollectMinio checks every MinIO it can find in Docker plus the ones in targets.
func CollectMinio(ctx context.Context, socket string, targets []MinioTarget, now int64) ([]Point, error) {
	found, derr := discoverMinio(ctx, socket)
	if errors.Is(derr, ErrNoDocker) {
		derr = nil
	}
	type job struct {
		t     MinioTarget
		bases []string
	}
	var jobs []job
	byContainer := map[string]MinioTarget{}
	for _, t := range targets {
		if t.Container != "" {
			byContainer[t.Container] = t
		}
	}
	seen := map[string]bool{}
	for _, d := range found {
		t := byContainer[d.Container]
		if t.Name == "" {
			t.Name = d.Container
		}
		t.Container = d.Container
		bases := d.Bases
		if t.URL != "" {
			bases = append([]string{t.URL}, bases...)
		}
		jobs = append(jobs, job{t, bases})
		seen[d.Container] = true
	}
	for _, t := range targets {
		if t.URL == "" || (t.Container != "" && seen[t.Container]) {
			continue
		}
		if t.Name == "" {
			t.Name = t.URL
		}
		jobs = append(jobs, job{t, []string{t.URL}})
	}
	if len(jobs) > 20 {
		jobs = jobs[:20]
	}
	var pts []Point
	for _, j := range jobs {
		var got []Point
		var err error = errors.New("no address to reach it on")
		url := ""
		for _, b := range j.bases {
			got, err = MinioCheck(ctx, b, j.t.Token, now)
			if err == nil || errors.Is(err, errMinioAuth) {
				url = b
				break
			}
		}
		up, auth, reason := 1.0, 0.0, ""
		if err != nil {
			up = 0
			if errors.Is(err, errMinioAuth) {
				auth = 1
				reason = "needs a token for its metrics"
				if j.t.Token != "" {
					reason = "the token is wrong"
				}
			} else {
				reason = shortErr(err)
			}
		}
		base := map[string]string{"minio": j.t.Name}
		pts = append(pts,
			Point{Service: "host", Name: "minio_up", Type: "gauge", Value: up, TimeNs: now, Attrs: base},
			Point{Service: "host", Name: "minio_auth_required", Type: "gauge", Value: auth, TimeNs: now, Attrs: base},
			Point{Service: "host", Name: "minio_info", Type: "gauge", Value: 1, TimeNs: now, Attrs: map[string]string{"minio": j.t.Name, "container": j.t.Container, "url": safeURL(url), "reason": reason}})
		if err != nil {
			continue
		}
		for _, p := range got {
			a := map[string]string{"minio": j.t.Name}
			for k, v := range p.Attrs {
				if k != "server" && k != "minio" {
					a[k] = v
				}
			}
			p.Attrs = a
			pts = append(pts, p)
		}
		free, ok1 := minioNamed(got, "minio_cluster_capacity_usable_free_bytes")
		total, ok2 := minioNamed(got, "minio_cluster_capacity_usable_total_bytes")
		if ok1 && ok2 && total > 0 {
			pts = append(pts, Point{Service: "host", Name: "minio_cluster_capacity_utilization", Type: "gauge", Value: 1 - free/total, TimeNs: now, Attrs: base})
		}
	}
	return pts, derr
}
