package registry

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// S3 is a storage with an S3 interface (AWS S3, Ceph, Garage, SeaweedFS, MinIO...) that one agent watches with an access key.
// The secret key is stored encrypted and goes only to the agent on the chosen machine.
type S3 struct {
	ID        string    `json:"id"`
	Tenant    string    `json:"tenant,omitempty"`
	Name      string    `json:"name"`
	Host      string    `json:"host"` // the machine whose agent makes the requests
	URL       string    `json:"url"`
	Region    string    `json:"region,omitempty"`
	AccessKey string    `json:"access_key"`
	SecretEnc string    `json:"secret_enc,omitempty"`
	Buckets   []string  `json:"buckets,omitempty"`
	Size      bool      `json:"size,omitempty"` // measure the buckets (lists all their objects, once an hour)
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
}

// S3Out is an S3 as the UI sees it: the secret is never sent back, only whether one is set.
type S3Out struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Host      string   `json:"host"`
	URL       string   `json:"url"`
	Region    string   `json:"region"`
	AccessKey string   `json:"access_key"`
	HasSecret bool     `json:"has_secret"`
	Buckets   []string `json:"buckets"`
	Size      bool     `json:"size"`
}

func (r S3) Out() S3Out {
	b := r.Buckets
	if b == nil {
		b = []string{}
	}
	return S3Out{ID: r.ID, Name: r.Name, Host: r.Host, URL: r.URL, Region: r.Region, AccessKey: r.AccessKey, HasSecret: r.SecretEnc != "", Buckets: b, Size: r.Size}
}

type S3In struct {
	Name, Host, URL, Region, AccessKey, Secret string
	Buckets                                    []string
	Size                                       bool
}

// AgentS3 is an S3 as the agent receives it (secret decrypted).
type AgentS3 struct {
	Name      string   `json:"name"`
	URL       string   `json:"url"`
	Region    string   `json:"region,omitempty"`
	AccessKey string   `json:"access_key"`
	SecretKey string   `json:"secret_key"`
	Buckets   []string `json:"buckets,omitempty"`
	Size      bool     `json:"size,omitempty"`
}

var (
	s3BucketRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,61}[a-z0-9]$`)
	s3RegionRe = regexp.MustCompile(`^[a-z0-9-]{1,30}$`)
)

func (s *Service) ListS3(tenant string) []S3 {
	c, cancel := ctx()
	defer cancel()
	docs, _ := s.b.List(c, collS3, map[string]string{"tenant": tenant}, 1000)
	out := []S3{}
	for _, d := range docs {
		var r S3
		if d.Decode(&r) == nil {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(a, b int) bool { return strings.ToLower(out[a].Name) < strings.ToLower(out[b].Name) })
	return out
}

func (s *Service) getS3(tenant, id string) (S3, string, error) {
	c, cancel := ctx()
	defer cancel()
	d, err := s.b.Get(c, collS3, id)
	var r S3
	if err != nil || d.Decode(&r) != nil || r.Tenant != tenant {
		return S3{}, "", ErrNotFound
	}
	return r, d.Version, nil
}

// validateS3 checks and cleans the input. needSecret is true for a new record (an update may keep the old secret).
func (s *Service) validateS3(tenant, id string, in *S3In, needSecret bool) error {
	in.Name, in.Host, in.URL, in.Region = strings.TrimSpace(in.Name), strings.TrimSpace(in.Host), strings.TrimSpace(in.URL), strings.TrimSpace(in.Region)
	in.AccessKey, in.Secret = strings.TrimSpace(in.AccessKey), strings.TrimSpace(in.Secret)
	if !hostRe.MatchString(in.Host) {
		return invalid("choose which machine's agent checks this storage")
	}
	u, err := url.Parse(in.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || len(in.URL) > 300 || strings.ContainsAny(in.URL, " \r\n\x00") {
		return invalid("the address must look like https://s3.example.com (no path, no login in it)")
	}
	in.URL = strings.TrimRight(in.URL, "/")
	if in.Name == "" {
		in.Name = u.Hostname()
	}
	if len(in.Name) > 100 || strings.ContainsAny(in.Name, "\r\n\x00") {
		return invalid("the name is too long or has control characters")
	}
	if in.Region != "" && !s3RegionRe.MatchString(in.Region) {
		return invalid("the region must look like us-east-1")
	}
	if in.AccessKey == "" || len(in.AccessKey) > 200 || strings.ContainsAny(in.AccessKey, " \r\n\x00") {
		return invalid("give the access key")
	}
	if needSecret && in.Secret == "" {
		return invalid("give the secret key")
	}
	if len(in.Secret) > 500 || strings.ContainsAny(in.Secret, " \r\n\x00") {
		return invalid("the secret key is too long or has spaces or line breaks in it")
	}
	clean := []string{}
	for _, b := range in.Buckets {
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		if !s3BucketRe.MatchString(b) {
			return invalid("%q is not a bucket name (lower case letters, digits, . _ -)", b)
		}
		clean = append(clean, b)
	}
	if len(clean) > 50 {
		return invalid("name at most 50 buckets, or leave the list empty for all")
	}
	in.Buckets = clean
	for _, o := range s.ListS3(tenant) {
		if o.ID != id && strings.EqualFold(o.Name, in.Name) {
			return invalid("there is already a storage with that name")
		}
	}
	return nil
}

func (s *Service) CreateS3(tenant string, in S3In) (S3, error) {
	if err := s.validateS3(tenant, "", &in, true); err != nil {
		return S3{}, err
	}
	now := time.Now().UTC()
	r := S3{ID: "s3" + randID(), Tenant: tenant, Name: in.Name, Host: in.Host, URL: in.URL, Region: in.Region, AccessKey: in.AccessKey,
		SecretEnc: s.box.Seal(in.Secret), Buckets: in.Buckets, Size: in.Size, Created: now, Updated: now}
	c, cancel := ctx()
	defer cancel()
	return r, s.b.Create(c, collS3, r.ID, r)
}

func (s *Service) UpdateS3(tenant, id string, in S3In) (S3, error) {
	cur, ver, err := s.getS3(tenant, id)
	if err != nil {
		return S3{}, err
	}
	if err := s.validateS3(tenant, id, &in, false); err != nil {
		return S3{}, err
	}
	cur.Name, cur.Host, cur.URL, cur.Region, cur.AccessKey, cur.Buckets, cur.Size = in.Name, in.Host, in.URL, in.Region, in.AccessKey, in.Buckets, in.Size
	if in.Secret != "" {
		cur.SecretEnc = s.box.Seal(in.Secret)
	}
	cur.Updated = time.Now().UTC()
	c, cancel := ctx()
	defer cancel()
	return cur, s.b.Put(c, collS3, id, cur, ver)
}

func (s *Service) DeleteS3(tenant, id string) error {
	if _, _, err := s.getS3(tenant, id); err != nil {
		return err
	}
	c, cancel := ctx()
	defer cancel()
	return s.b.Delete(c, collS3, id)
}
