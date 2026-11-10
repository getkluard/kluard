package registry

import (
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Postgres is what Kluard knows about one PostgreSQL server beyond what its agent finds by itself: the password (encrypted), an
// ACL user, and, for a PostgreSQL that is not in Docker, its address. A PostgreSQL in Docker is matched by Container on Host.
type Postgres struct {
	ID          string    `json:"id"`
	Tenant      string    `json:"tenant,omitempty"`
	Name        string    `json:"name"`      // what it is called in Kluard
	Host        string    `json:"host"`      // the machine whose agent connects to it
	Container   string    `json:"container"` // Docker container name (empty for one given by address)
	Addr        string    `json:"addr"`      // host:port, optional for a container
	Username    string    `json:"username,omitempty"`
	Database    string    `json:"database,omitempty"`
	SSLMode     string    `json:"sslmode,omitempty"`
	PasswordEnc string    `json:"password_enc,omitempty"`
	Created     time.Time `json:"created"`
	Updated     time.Time `json:"updated"`
}

// PostgresOut is a PostgreSQL as the UI sees it: the password is never sent back, only whether one is set.
type PostgresOut struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Host        string `json:"host"`
	Container   string `json:"container"`
	Addr        string `json:"addr"`
	Username    string `json:"username"`
	Database    string `json:"database"`
	SSLMode     string `json:"sslmode"`
	HasPassword bool   `json:"has_password"`
}

func (r Postgres) Out() PostgresOut {
	return PostgresOut{ID: r.ID, Name: r.Name, Host: r.Host, Container: r.Container, Addr: r.Addr, Username: r.Username, Database: r.Database, SSLMode: r.SSLMode, HasPassword: r.PasswordEnc != ""}
}

type PostgresIn struct {
	Name, Host, Container, Addr, Username string
	Database, SSLMode                     string
	Password                              string
	ClearPassword                         bool
}

// AgentPostgres is a PostgreSQL as the agent receives it (password decrypted).
type AgentPostgres struct {
	Name      string `json:"name"`
	Container string `json:"container,omitempty"`
	Addr      string `json:"addr,omitempty"`
	Username  string `json:"username,omitempty"`
	Password  string `json:"password,omitempty"`
	Database  string `json:"database,omitempty"`
	SSLMode   string `json:"sslmode,omitempty"`
}

func (s *Service) ListPostgres(tenant string) []Postgres {
	c, cancel := ctx()
	defer cancel()
	docs, _ := s.b.List(c, collPostgres, map[string]string{"tenant": tenant}, 1000)
	out := []Postgres{}
	for _, d := range docs {
		var r Postgres
		if d.Decode(&r) == nil {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(a, b int) bool {
		return strings.ToLower(out[a].Host+"/"+out[a].Name) < strings.ToLower(out[b].Host+"/"+out[b].Name)
	})
	return out
}

func (s *Service) getPostgres(tenant, id string) (Postgres, string, error) {
	c, cancel := ctx()
	defer cancel()
	d, err := s.b.Get(c, collPostgres, id)
	var r Postgres
	if err != nil || d.Decode(&r) != nil || r.Tenant != tenant {
		return Postgres{}, "", ErrNotFound
	}
	return r, d.Version, nil
}

func (s *Service) validatePostgres(tenant, id string, in *PostgresIn) error {
	in.Name, in.Host, in.Container = strings.TrimSpace(in.Name), strings.TrimSpace(in.Host), strings.TrimSpace(in.Container)
	in.Addr, in.Username = strings.TrimSpace(in.Addr), strings.TrimSpace(in.Username)
	in.Database, in.SSLMode = strings.TrimSpace(in.Database), strings.TrimSpace(in.SSLMode)
	if in.SSLMode != "" && in.SSLMode != "disable" && in.SSLMode != "prefer" && in.SSLMode != "require" {
		return invalid("the TLS setting must be disable, prefer or require")
	}
	if len(in.Database) > 63 || strings.ContainsAny(in.Database, "\r\n\x00") {
		return invalid("the database name is too long or has control characters")
	}
	if in.Name == "" {
		in.Name = in.Container
	}
	if in.Name == "" {
		in.Name = in.Addr
	}
	if !hostRe.MatchString(in.Host) {
		return invalid("choose which machine's agent connects to this PostgreSQL")
	}
	if in.Container == "" && in.Addr == "" {
		return invalid("give the Docker container name or an address (host:port)")
	}
	if in.Name == "" || len(in.Name) > 100 || strings.ContainsAny(in.Name+in.Container+in.Username, "\r\n\x00") {
		return invalid("the name is empty, too long or has control characters")
	}
	if in.Container != "" && !nameRe.MatchString(in.Container) {
		return invalid("the container name must be letters, digits, . _ - (as Docker allows)")
	}
	if in.Addr != "" {
		h, p, err := net.SplitHostPort(in.Addr)
		n, _ := strconv.Atoi(p)
		if err != nil || h == "" || n < 1 || n > 65535 || strings.ContainsAny(in.Addr, " \r\n\x00/") {
			return invalid("the address must look like 10.0.0.5:5432 or db.internal:5432")
		}
	}
	if len(in.Password) > 500 || len(in.Username) > 100 || strings.ContainsAny(in.Password, "\r\n\x00") {
		return invalid("the password or user is too long or has control characters")
	}
	for _, o := range s.ListPostgres(tenant) {
		if o.ID != id && o.Host == in.Host && (strings.EqualFold(o.Name, in.Name) || (in.Container != "" && o.Container == in.Container)) {
			return invalid("this PostgreSQL is already set up on that machine")
		}
	}
	return nil
}

func (s *Service) CreatePostgres(tenant string, in PostgresIn) (Postgres, error) {
	if err := s.validatePostgres(tenant, "", &in); err != nil {
		return Postgres{}, err
	}
	now := time.Now().UTC()
	r := Postgres{ID: "pg" + randID(), Tenant: tenant, Name: in.Name, Host: in.Host, Container: in.Container, Addr: in.Addr, Username: in.Username, Database: in.Database, SSLMode: in.SSLMode,
		PasswordEnc: s.box.Seal(in.Password), Created: now, Updated: now}
	c, cancel := ctx()
	defer cancel()
	return r, s.b.Create(c, collPostgres, r.ID, r)
}

func (s *Service) UpdatePostgres(tenant, id string, in PostgresIn) (Postgres, error) {
	cur, ver, err := s.getPostgres(tenant, id)
	if err != nil {
		return Postgres{}, err
	}
	if err := s.validatePostgres(tenant, id, &in); err != nil {
		return Postgres{}, err
	}
	cur.Name, cur.Host, cur.Container, cur.Addr, cur.Username = in.Name, in.Host, in.Container, in.Addr, in.Username
	cur.Database, cur.SSLMode = in.Database, in.SSLMode
	switch {
	case in.ClearPassword:
		cur.PasswordEnc = ""
	case in.Password != "":
		cur.PasswordEnc = s.box.Seal(in.Password)
	}
	cur.Updated = time.Now().UTC()
	c, cancel := ctx()
	defer cancel()
	return cur, s.b.Put(c, collPostgres, id, cur, ver)
}

func (s *Service) DeletePostgres(tenant, id string) error {
	if _, _, err := s.getPostgres(tenant, id); err != nil {
		return err
	}
	c, cancel := ctx()
	defer cancel()
	return s.b.Delete(c, collPostgres, id)
}
