package agent

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// A small MySQL/MariaDB client: enough of the wire protocol to log in and run read-only queries in text mode. Nothing is
// installed and no driver is needed. Logins with mysql_native_password (the MariaDB default and MySQL 5.7) and
// caching_sha2_password (the MySQL 8 default) work, over TLS when the server offers it or demands it, and without TLS
// through the server's public key. The certificate is not checked: this connection only reads statistics.

// ErrMysqlAuth means the server refused the login: a missing or wrong password, or a user that may not log in.
var ErrMysqlAuth = errors.New("mysql requires a (correct) login")

const (
	myLongPassword   = 1
	myLongFlag       = 4
	myProtocol41     = 0x200
	mySSL            = 0x800
	myTransactions   = 0x2000
	mySecureConn     = 0x8000
	myPluginAuth     = 0x80000
	myMaxPacket      = 16 << 20
	myMaxResultBytes = 8 << 20
)

type myConn struct {
	c   net.Conn
	r   *bufio.Reader
	seq byte
	tls bool
}

func (m *myConn) close() {
	m.c.SetDeadline(time.Now().Add(time.Second))
	m.seq = 0
	m.write([]byte{0x01}) // COM_QUIT
	m.c.Close()
}

func (m *myConn) write(body []byte) error {
	for {
		n := len(body)
		if n > 0xffffff {
			n = 0xffffff
		}
		h := []byte{byte(n), byte(n >> 8), byte(n >> 16), m.seq}
		m.seq++
		if _, err := m.c.Write(append(h, body[:n]...)); err != nil {
			return err
		}
		body = body[n:]
		if n < 0xffffff {
			return nil
		}
	}
}

func (m *myConn) read() ([]byte, error) {
	var out []byte
	for {
		var h [4]byte
		if _, err := io.ReadFull(m.r, h[:]); err != nil {
			return nil, err
		}
		n := int(h[0]) | int(h[1])<<8 | int(h[2])<<16
		m.seq = h[3] + 1
		if len(out)+n > myMaxPacket {
			return nil, errors.New("not a MySQL reply (is this port really MySQL or MariaDB?)")
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(m.r, b); err != nil {
			return nil, err
		}
		out = append(out, b...)
		if n < 0xffffff {
			return out, nil
		}
	}
}

// myErr turns an error packet into an error. 1045 and 1698 are login refusals.
func myErr(p []byte) error {
	if len(p) < 3 {
		return errors.New("mysql error")
	}
	code := int(binary.LittleEndian.Uint16(p[1:3]))
	msg := p[3:]
	if len(msg) > 6 && msg[0] == '#' {
		msg = msg[6:]
	}
	if code == 1045 || code == 1698 {
		return fmt.Errorf("%w: %s", ErrMysqlAuth, string(msg))
	}
	return fmt.Errorf("mysql error %d: %s", code, string(msg))
}

func myNativeScramble(pass string, nonce []byte) []byte {
	if pass == "" {
		return nil
	}
	h1 := sha1.Sum([]byte(pass))
	h2 := sha1.Sum(h1[:])
	h3 := sha1.New()
	h3.Write(nonce)
	h3.Write(h2[:])
	x := h3.Sum(nil)
	for i := range x {
		x[i] ^= h1[i]
	}
	return x
}

func myCachingScramble(pass string, nonce []byte) []byte {
	if pass == "" {
		return nil
	}
	h1 := sha256.Sum256([]byte(pass))
	h2 := sha256.Sum256(h1[:])
	h3 := sha256.New()
	h3.Write(h2[:])
	h3.Write(nonce)
	x := h3.Sum(nil)
	for i := range x {
		x[i] ^= h1[i]
	}
	return x
}

func myScramble(plugin, pass string, nonce []byte) ([]byte, error) {
	switch plugin {
	case "mysql_native_password", "":
		return myNativeScramble(pass, nonce), nil
	case "caching_sha2_password":
		return myCachingScramble(pass, nonce), nil
	}
	return nil, fmt.Errorf("the login method %q is not supported: give the user IDENTIFIED WITH mysql_native_password or caching_sha2_password", plugin)
}

// MysqlDial connects and logs in. sslmode is disable, prefer (default) or require.
func MysqlDial(ctx context.Context, addr, user, pass, sslmode string) (*myConn, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	m := &myConn{c: c, r: bufio.NewReader(c)}
	dl := time.Now().Add(15 * time.Second)
	if t, ok := ctx.Deadline(); ok && t.Before(dl) {
		dl = t
	}
	c.SetDeadline(dl)
	if err := m.login(user, pass, sslmode); err != nil {
		c.Close()
		return nil, err
	}
	return m, nil
}

func (m *myConn) login(user, pass, sslmode string) error {
	p, err := m.read()
	if err != nil {
		return err
	}
	if len(p) > 0 && p[0] == 0xff {
		return myErr(p)
	}
	if len(p) < 20 || p[0] != 10 {
		return errors.New("not a MySQL reply (is this port really MySQL or MariaDB?)")
	}
	i := 1
	for i < len(p) && p[i] != 0 {
		i++
	}
	i++ // server version, NUL
	if i+13 > len(p) {
		return errors.New("short MySQL handshake")
	}
	i += 4 // thread id
	nonce := append([]byte(nil), p[i:i+8]...)
	i += 8 + 1 // part 1, filler
	caps := uint32(binary.LittleEndian.Uint16(p[i:]))
	i += 2
	plugin := "mysql_native_password"
	if len(p) > i+2+2+1+10 {
		i += 1 + 2 // charset, status
		caps |= uint32(binary.LittleEndian.Uint16(p[i:])) << 16
		i += 2
		alen := int(p[i])
		i += 1 + 10
		n2 := alen - 8
		if n2 < 13 {
			n2 = 13
		}
		if i+n2 > len(p) {
			n2 = len(p) - i
		}
		part2 := p[i : i+n2]
		part2 = []byte(strings.TrimRight(string(part2), "\x00"))
		nonce = append(nonce, part2...)
		i += n2
		if i < len(p) {
			plugin = strings.TrimRight(string(p[i:]), "\x00")
		}
	}
	useTLS := sslmode != "disable" && caps&mySSL != 0
	if sslmode == "require" && caps&mySSL == 0 {
		return errors.New("the server does not offer TLS but TLS is required")
	}
	flags := uint32(myLongPassword | myLongFlag | myProtocol41 | myTransactions | mySecureConn | myPluginAuth)
	if useTLS {
		flags |= mySSL
		req := make([]byte, 32)
		binary.LittleEndian.PutUint32(req[0:], flags)
		binary.LittleEndian.PutUint32(req[4:], myMaxPacket)
		req[8] = 45 // utf8mb4_general_ci
		if err := m.write(req); err != nil {
			return err
		}
		tc := tls.Client(m.c, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // statistics only
		if err := tc.Handshake(); err != nil {
			return fmt.Errorf("TLS handshake: %w", err)
		}
		m.c, m.r, m.tls = tc, bufio.NewReader(tc), true
	}
	auth, err := myScramble(plugin, pass, nonce)
	if err != nil {
		return err
	}
	resp := make([]byte, 32, 64+len(user)+len(auth))
	binary.LittleEndian.PutUint32(resp[0:], flags)
	binary.LittleEndian.PutUint32(resp[4:], myMaxPacket)
	resp[8] = 45
	resp = append(resp, user...)
	resp = append(resp, 0, byte(len(auth)))
	resp = append(resp, auth...)
	resp = append(resp, plugin...)
	resp = append(resp, 0)
	if err := m.write(resp); err != nil {
		return err
	}
	for {
		p, err := m.read()
		if err != nil {
			return err
		}
		if len(p) == 0 {
			return errors.New("empty MySQL reply")
		}
		switch p[0] {
		case 0x00:
			return nil
		case 0xff:
			return myErr(p)
		case 0xfe: // the server asks for another login method
			j := 1
			for j < len(p) && p[j] != 0 {
				j++
			}
			plugin = string(p[1:j])
			nonce = []byte(strings.TrimRight(string(p[j+1:]), "\x00"))
			a, err := myScramble(plugin, pass, nonce)
			if err != nil {
				return err
			}
			if err := m.write(a); err != nil {
				return err
			}
		case 0x01: // more data for caching_sha2_password
			if len(p) < 2 {
				return errors.New("short MySQL reply")
			}
			switch p[1] {
			case 3: // fast path accepted, the OK packet follows
			case 4: // the full login: the password must travel safely
				if m.tls {
					if err := m.write(append([]byte(pass), 0)); err != nil {
						return err
					}
					continue
				}
				if err := m.write([]byte{2}); err != nil { // ask for the server's public key
					return err
				}
				kp, err := m.read()
				if err != nil {
					return err
				}
				if len(kp) < 2 || kp[0] != 0x01 {
					return errors.New("the server sent no public key")
				}
				blk, _ := pem.Decode(kp[1:])
				if blk == nil {
					return errors.New("the server's public key could not be read")
				}
				pk, err := x509.ParsePKIXPublicKey(blk.Bytes)
				if err != nil {
					return err
				}
				rk, ok := pk.(*rsa.PublicKey)
				if !ok {
					return errors.New("the server's public key is not RSA")
				}
				pw := append([]byte(pass), 0)
				for k := range pw {
					pw[k] ^= nonce[k%len(nonce)]
				}
				enc, err := rsa.EncryptOAEP(sha1.New(), rand.Reader, rk, pw, nil)
				if err != nil {
					return err
				}
				if err := m.write(enc); err != nil {
					return err
				}
			}
		default:
			return errors.New("unexpected MySQL reply during login")
		}
	}
}

func lenenc(b []byte) (v uint64, n int, null bool) {
	if len(b) == 0 {
		return 0, 0, false
	}
	switch c := b[0]; {
	case c < 0xfb:
		return uint64(c), 1, false
	case c == 0xfb:
		return 0, 1, true
	case c == 0xfc && len(b) >= 3:
		return uint64(binary.LittleEndian.Uint16(b[1:])), 3, false
	case c == 0xfd && len(b) >= 4:
		return uint64(b[1]) | uint64(b[2])<<8 | uint64(b[3])<<16, 4, false
	case c == 0xfe && len(b) >= 9:
		return binary.LittleEndian.Uint64(b[1:]), 9, false
	}
	return 0, 0, false
}

// Query runs one statement in text mode and returns the column names and at most max rows (NULL becomes "").
func (m *myConn) Query(sql string, max int) ([]string, [][]string, error) {
	m.seq = 0
	if err := m.write(append([]byte{0x03}, sql...)); err != nil {
		return nil, nil, err
	}
	p, err := m.read()
	if err != nil {
		return nil, nil, err
	}
	if len(p) == 0 {
		return nil, nil, errors.New("empty MySQL reply")
	}
	if p[0] == 0xff {
		return nil, nil, myErr(p)
	}
	if p[0] == 0x00 {
		return nil, nil, nil
	}
	ncols, _, _ := lenenc(p)
	if ncols == 0 || ncols > 64 {
		return nil, nil, errors.New("unexpected MySQL reply")
	}
	cols := make([]string, 0, ncols)
	for i := uint64(0); i < ncols; i++ {
		cp, err := m.read()
		if err != nil {
			return nil, nil, err
		}
		// catalog, schema, table, org_table, name, ...: all length-encoded strings; the fifth is the name
		off := 0
		name := ""
		for f := 0; f < 5; f++ {
			l, n, _ := lenenc(cp[off:])
			if n == 0 || off+n+int(l) > len(cp) {
				break
			}
			if f == 4 {
				name = string(cp[off+n : off+n+int(l)])
			}
			off += n + int(l)
		}
		cols = append(cols, name)
	}
	if p, err = m.read(); err != nil { // EOF after the column definitions
		return nil, nil, err
	}
	var rows [][]string
	size := 0
	for {
		p, err := m.read()
		if err != nil {
			return cols, rows, err
		}
		if len(p) > 0 && p[0] == 0xff {
			return cols, rows, myErr(p)
		}
		if len(p) > 0 && p[0] == 0xfe && len(p) < 9 {
			return cols, rows, nil
		}
		if len(rows) >= max || size > myMaxResultBytes {
			continue // read on to the end of the result, keep what fits
		}
		row := make([]string, 0, len(cols))
		off := 0
		for range cols {
			l, n, null := lenenc(p[off:])
			if n == 0 {
				break
			}
			off += n
			if null {
				row = append(row, "")
				continue
			}
			if off+int(l) > len(p) {
				break
			}
			row = append(row, string(p[off:off+int(l)]))
			off += int(l)
		}
		size += len(p)
		rows = append(rows, row)
	}
}
