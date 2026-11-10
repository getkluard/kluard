package agent

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// A small PostgreSQL client: just enough of the wire protocol (version 3) to log in and run read-only queries with the
// simple query protocol. Nothing is installed and no driver is needed. Logins with trust, a clear-text password, md5 and
// SCRAM-SHA-256 (the default since PostgreSQL 14) work; TLS is used when the server offers it (sslmode prefer) or demanded
// (require). The certificate is not checked: this connection only reads statistics.

// ErrPostgresAuth means the server wants a password and none (or a wrong one) was given.
var ErrPostgresAuth = errors.New("postgresql requires a (correct) password")

type pgConn struct {
	c net.Conn
	r *bufio.Reader
}

func (p *pgConn) close() {
	p.c.SetDeadline(time.Now().Add(time.Second))
	p.c.Write([]byte{'X', 0, 0, 0, 4})
	p.c.Close()
}

func (p *pgConn) send(typ byte, body []byte) error {
	buf := make([]byte, 0, len(body)+5)
	if typ != 0 {
		buf = append(buf, typ)
	}
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(body)+4))
	buf = append(buf, body...)
	_, err := p.c.Write(buf)
	return err
}

const pgMaxMsg = 8 << 20

func (p *pgConn) read() (byte, []byte, error) {
	var h [5]byte
	if _, err := io.ReadFull(p.r, h[:]); err != nil {
		return 0, nil, err
	}
	n := int(binary.BigEndian.Uint32(h[1:])) - 4
	if n < 0 || n > pgMaxMsg {
		return 0, nil, errors.New("not a PostgreSQL reply (is this port really PostgreSQL?)")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(p.r, body); err != nil {
		return 0, nil, err
	}
	return h[0], body, nil
}

// pgError reads the message out of an ErrorResponse body.
func pgError(body []byte) (code, msg string) {
	for len(body) > 1 {
		t := body[0]
		end := strings.IndexByte(string(body[1:]), 0)
		if end < 0 {
			break
		}
		v := string(body[1 : 1+end])
		switch t {
		case 'C':
			code = v
		case 'M':
			msg = v
		}
		body = body[2+end:]
	}
	return
}

type pgAuthErr struct{ code, msg string }

func (e *pgAuthErr) Error() string { return e.msg }

// PostgresDial connects and logs in. sslmode is disable, prefer (default) or require.
func PostgresDial(ctx context.Context, addr, user, pass, db, sslmode string) (*pgConn, error) {
	d := net.Dialer{Timeout: 3 * time.Second}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	c.SetDeadline(time.Now().Add(8 * time.Second))
	host, _, _ := net.SplitHostPort(addr)
	pc := &pgConn{c: c}
	if sslmode != "disable" {
		if _, err := c.Write([]byte{0, 0, 0, 8, 4, 210, 22, 47}); err != nil { // SSLRequest
			c.Close()
			return nil, err
		}
		var b [1]byte
		if _, err := io.ReadFull(c, b[:]); err != nil {
			c.Close()
			return nil, err
		}
		switch b[0] {
		case 'S':
			t := tls.Client(c, &tls.Config{InsecureSkipVerify: true, ServerName: host}) // statistics only; see above
			if err := t.HandshakeContext(ctx); err != nil {
				c.Close()
				return nil, err
			}
			pc.c = t
		case 'N':
			if sslmode == "require" {
				c.Close()
				return nil, errors.New("the server does not offer TLS (sslmode is require)")
			}
		default:
			c.Close()
			return nil, errors.New("not a PostgreSQL reply (is this port really PostgreSQL?)")
		}
	}
	pc.r = bufio.NewReaderSize(pc.c, 64<<10)
	if db == "" {
		db = user
	}
	var sm []byte
	for _, kv := range [][2]string{{"user", user}, {"database", db}, {"application_name", "kluard-agent"}, {"client_encoding", "UTF8"}} {
		sm = append(sm, kv[0]...)
		sm = append(sm, 0)
		sm = append(sm, kv[1]...)
		sm = append(sm, 0)
	}
	sm = append(sm, 0)
	start := binary.BigEndian.AppendUint32(nil, 196608) // protocol 3.0
	if err := pc.send(0, append(start, sm...)); err != nil {
		pc.c.Close()
		return nil, err
	}
	if err := pc.login(user, pass); err != nil {
		pc.c.Close()
		return nil, err
	}
	return pc, nil
}

func (p *pgConn) login(user, pass string) error {
	var scram *scramClient
	for {
		t, body, err := p.read()
		if err != nil {
			return err
		}
		switch t {
		case 'E':
			code, msg := pgError(body)
			if strings.Contains(msg, "no pg_hba.conf entry") {
				return fmt.Errorf("the server refuses this connection (pg_hba.conf): %s", msg)
			}
			if code == "28P01" || strings.Contains(msg, "password authentication failed") {
				return ErrPostgresAuth
			}
			return errors.New(msg)
		case 'R':
			if len(body) < 4 {
				return errors.New("bad authentication message")
			}
			switch kind := binary.BigEndian.Uint32(body); kind {
			case 0: // ok
			case 3: // clear text
				if pass == "" {
					return ErrPostgresAuth
				}
				if err := p.send('p', append([]byte(pass), 0)); err != nil {
					return err
				}
			case 5: // md5
				if pass == "" {
					return ErrPostgresAuth
				}
				if len(body) < 8 {
					return errors.New("bad md5 message")
				}
				in := md5hex(md5hex(pass+user) + string(body[4:8]))
				if err := p.send('p', append([]byte("md5"+in), 0)); err != nil {
					return err
				}
			case 10: // SASL
				if pass == "" {
					return ErrPostgresAuth
				}
				if !strings.Contains(string(body[4:]), "SCRAM-SHA-256") {
					return errors.New("the server wants a login method the agent does not support")
				}
				scram = newScram(user, pass)
				first := scram.first()
				m := append([]byte("SCRAM-SHA-256\x00"), binary.BigEndian.AppendUint32(nil, uint32(len(first)))...)
				if err := p.send('p', append(m, first...)); err != nil {
					return err
				}
			case 11: // SASL continue
				if scram == nil {
					return errors.New("unexpected SASL message")
				}
				final, err := scram.final(string(body[4:]))
				if err != nil {
					return err
				}
				if err := p.send('p', []byte(final)); err != nil {
					return err
				}
			case 12: // SASL final: check the server's proof
				if scram == nil || scram.verify(string(body[4:])) != nil {
					return errors.New("the server could not prove that it knows the password")
				}
			default:
				return fmt.Errorf("the server wants a login method the agent does not support (%d)", kind)
			}
		case 'Z': // ReadyForQuery
			return nil
		case 'S', 'K', 'N': // parameter status, key data, notice
		}
	}
}

func md5hex(s string) string { h := md5.Sum([]byte(s)); return hex.EncodeToString(h[:]) }

type scramClient struct {
	user, pass     string
	nonce          string
	firstBare      string
	serverSig      []byte
	expectedServer string
}

func newScram(user, pass string) *scramClient {
	b := make([]byte, 18)
	rand.Read(b)
	return &scramClient{user: user, pass: pass, nonce: base64.StdEncoding.EncodeToString(b)}
}

func (s *scramClient) first() string {
	s.firstBare = "n=,r=" + s.nonce // PostgreSQL ignores the user name here and uses the one from the startup message
	return "n,," + s.firstBare
}

func scramAttrs(m string) map[string]string {
	out := map[string]string{}
	for _, p := range strings.Split(m, ",") {
		if len(p) > 2 && p[1] == '=' {
			out[p[:1]] = p[2:]
		}
	}
	return out
}

func hmacSHA256(key []byte, msg string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(msg))
	return h.Sum(nil)
}

// pbkdf2SHA256 is PBKDF2 with HMAC-SHA-256 for one output block (32 bytes), which is all SCRAM needs.
func pbkdf2SHA256(pass string, salt []byte, iter int) []byte {
	h := hmac.New(sha256.New, []byte(pass))
	h.Write(salt)
	h.Write([]byte{0, 0, 0, 1})
	u := h.Sum(nil)
	out := append([]byte(nil), u...)
	for i := 1; i < iter; i++ {
		h := hmac.New(sha256.New, []byte(pass))
		h.Write(u)
		u = h.Sum(nil)
		for j := range out {
			out[j] ^= u[j]
		}
	}
	return out
}

func (s *scramClient) final(serverFirst string) (string, error) {
	a := scramAttrs(serverFirst)
	salt, err := base64.StdEncoding.DecodeString(a["s"])
	iter, err2 := strconv.Atoi(a["i"])
	if err != nil || err2 != nil || iter < 1 || iter > 1000000 || !strings.HasPrefix(a["r"], s.nonce) {
		return "", errors.New("bad SCRAM message from the server")
	}
	salted := pbkdf2SHA256(s.pass, salt, iter)
	clientKey := hmacSHA256(salted, "Client Key")
	stored := sha256.Sum256(clientKey)
	withoutProof := "c=biws,r=" + a["r"]
	authMsg := s.firstBare + "," + serverFirst + "," + withoutProof
	sig := hmacSHA256(stored[:], authMsg)
	proof := make([]byte, len(clientKey))
	for i := range proof {
		proof[i] = clientKey[i] ^ sig[i]
	}
	serverKey := hmacSHA256(salted, "Server Key")
	s.serverSig = hmacSHA256(serverKey, authMsg)
	return withoutProof + ",p=" + base64.StdEncoding.EncodeToString(proof), nil
}

func (s *scramClient) verify(serverFinal string) error {
	v, err := base64.StdEncoding.DecodeString(scramAttrs(serverFinal)["v"])
	if err != nil || !hmac.Equal(v, s.serverSig) {
		return errors.New("bad server signature")
	}
	return nil
}

// Query runs one statement and returns its rows as text (NULL becomes an empty string). It reads at most maxRows rows.
func (p *pgConn) Query(q string, maxRows int) ([]string, [][]string, error) {
	p.c.SetDeadline(time.Now().Add(8 * time.Second))
	if err := p.send('Q', append([]byte(q), 0)); err != nil {
		return nil, nil, err
	}
	var cols []string
	var rows [][]string
	var qerr error
	for {
		t, body, err := p.read()
		if err != nil {
			return nil, nil, err
		}
		switch t {
		case 'T':
			n := int(binary.BigEndian.Uint16(body))
			cols = cols[:0]
			rest := body[2:]
			for i := 0; i < n; i++ {
				end := strings.IndexByte(string(rest), 0)
				if end < 0 || len(rest) < end+19 {
					return nil, nil, errors.New("bad row description")
				}
				cols = append(cols, string(rest[:end]))
				rest = rest[end+1+18:]
			}
		case 'D':
			if len(rows) >= maxRows || len(body) < 2 {
				continue
			}
			n := int(binary.BigEndian.Uint16(body))
			row := make([]string, 0, n)
			rest := body[2:]
			for i := 0; i < n; i++ {
				if len(rest) < 4 {
					return nil, nil, errors.New("bad data row")
				}
				l := int(int32(binary.BigEndian.Uint32(rest)))
				rest = rest[4:]
				if l < 0 {
					row = append(row, "")
					continue
				}
				if l > len(rest) {
					return nil, nil, errors.New("bad data row")
				}
				row = append(row, string(rest[:l]))
				rest = rest[l:]
			}
			rows = append(rows, row)
		case 'E':
			code, msg := pgError(body)
			qerr = fmt.Errorf("%s (%s)", msg, code)
		case 'Z':
			if qerr != nil {
				return nil, nil, qerr
			}
			return cols, rows, nil
		}
	}
}
