package agent

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// S3 monitoring for any S3-compatible storage (AWS S3, Ceph, Garage, SeaweedFS, MinIO, Wasabi, Backblaze...): the agent on a
// machine you choose signs requests with an access key (AWS Signature Version 4, path-style addresses) and reports whether
// the storage answers, how fast, how many buckets there are and, if asked, how much each bucket holds. The S3 API has no
// "size of this bucket" call, so the size is found by listing the objects. That costs one request per 1000 objects, so it is
// done at most once an hour per bucket and stops after a limit, in which case the size is reported as a lower bound.

// S3Target is one storage to watch.
type S3Target struct {
	Name      string   `json:"name"`
	URL       string   `json:"url"`
	Region    string   `json:"region,omitempty"` // default us-east-1; most S3-compatible storages ignore it
	AccessKey string   `json:"access_key"`
	SecretKey string   `json:"secret_key"`
	Buckets   []string `json:"buckets,omitempty"` // only these; empty means every bucket the key may list
	Size      bool     `json:"size,omitempty"`    // measure the buckets by listing their objects
}

const (
	s3Every        = 55 * time.Second
	s3SizeEvery    = time.Hour
	s3MaxPages     = 100 // 100,000 objects per bucket and round; more is reported as "at least"
	s3SizeBudget   = 90 * time.Second
	s3MaxSizedBkts = 50
	s3EmptyHash    = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

var errS3Auth = errors.New("the access key or secret was refused")

type s3BucketSize struct {
	Bucket  string
	Bytes   float64
	Objects float64
	Partial bool
}

type s3State struct {
	at     time.Time
	sizeAt time.Time
	sizes  []s3BucketSize
}

var (
	s3Mu     sync.Mutex
	s3States = map[string]*s3State{}
)

func s3Hmac(key []byte, s string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(s))
	return h.Sum(nil)
}

// s3Escape is the URI encoding of AWS Signature V4: everything except letters, digits and -_.~ is %XX (and / is kept in paths).
func s3Escape(s string, keepSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~', c == '/' && keepSlash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func s3Query(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, s3Escape(k, false)+"="+s3Escape(q.Get(k), false))
	}
	return strings.Join(parts, "&")
}

// s3Sign returns the Authorization header value for a GET without a body.
func s3Sign(t S3Target, host, path, query string, now time.Time) (auth, amzDate string) {
	region := t.Region
	if region == "" {
		region = "us-east-1"
	}
	amzDate = now.UTC().Format("20060102T150405Z")
	day := amzDate[:8]
	signed := "host;x-amz-content-sha256;x-amz-date"
	canon := "GET\n" + path + "\n" + query + "\nhost:" + host + "\nx-amz-content-sha256:" + s3EmptyHash + "\nx-amz-date:" + amzDate + "\n\n" + signed + "\n" + s3EmptyHash
	sum := sha256.Sum256([]byte(canon))
	scope := day + "/" + region + "/s3/aws4_request"
	sts := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	k := s3Hmac([]byte("AWS4"+t.SecretKey), day)
	k = s3Hmac(k, region)
	k = s3Hmac(k, "s3")
	k = s3Hmac(k, "aws4_request")
	sig := hex.EncodeToString(s3Hmac(k, sts))
	return "AWS4-HMAC-SHA256 Credential=" + t.AccessKey + "/" + scope + ", SignedHeaders=" + signed + ", Signature=" + sig, amzDate
}

type s3Error struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
}

// s3Get makes one signed GET. path is "/" or "/bucket". It never puts the keys in an error.
func s3Get(ctx context.Context, t S3Target, path string, q url.Values) ([]byte, error) {
	base, err := url.Parse(t.URL)
	if err != nil || base.Hostname() == "" {
		return nil, errors.New("bad address")
	}
	p := s3Escape(path, true)
	query := s3Query(q)
	auth, date := s3Sign(t, base.Host, p, query, time.Now())
	u := base.Scheme + "://" + base.Host + p
	if query != "" {
		u += "?" + query
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, errors.New("bad address")
	}
	req.Header.Set("x-amz-date", date)
	req.Header.Set("x-amz-content-sha256", s3EmptyHash)
	req.Header.Set("Authorization", auth)
	cl := webClient()
	cl.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse } // never carry a signature elsewhere
	resp, err := cl.Do(req)
	if err != nil {
		var ne net.Error
		switch {
		case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
			return nil, errors.New("no answer in time")
		case strings.Contains(err.Error(), "certificate"):
			return nil, errors.New("the certificate is not trusted")
		}
		return nil, errors.New("cannot connect")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == 200 {
		return body, nil
	}
	var e s3Error
	_ = xml.Unmarshal(body, &e)
	switch {
	case e.Code == "InvalidAccessKeyId" || e.Code == "SignatureDoesNotMatch" || e.Code == "AuthorizationHeaderMalformed" || e.Code == "InvalidToken":
		return nil, errS3Auth
	case e.Code == "AccessDenied" || resp.StatusCode == 403:
		return nil, errors.New("access denied: the key may not do this")
	case e.Code == "NoSuchBucket" || resp.StatusCode == 404:
		return nil, errors.New("not found")
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return nil, errors.New("the address redirects; give the exact address of the storage")
	}
	return nil, fmt.Errorf("the storage answered HTTP %d", resp.StatusCode)
}

type s3ListBuckets struct {
	Buckets []struct {
		Name string `xml:"Name"`
	} `xml:"Buckets>Bucket"`
}

type s3ListObjects struct {
	IsTruncated bool   `xml:"IsTruncated"`
	NextToken   string `xml:"NextContinuationToken"`
	Contents    []struct {
		Size float64 `xml:"Size"`
	} `xml:"Contents"`
}

// S3Result is what one check found.
type S3Result struct {
	Up      bool
	Reason  string
	Seconds float64
	Buckets []string // the buckets in scope
	Listed  bool     // the number of buckets is known (ListBuckets worked)
}

// S3Check asks the storage for its buckets (or, for a key limited to named buckets, looks into each of them).
func S3Check(ctx context.Context, t S3Target) S3Result {
	var r S3Result
	start := time.Now()
	finish := func(err error) S3Result {
		r.Seconds = time.Since(start).Seconds()
		if err != nil {
			r.Reason = err.Error()
		} else {
			r.Up = true
		}
		return r
	}
	if len(t.Buckets) == 0 {
		body, err := s3Get(ctx, t, "/", nil)
		if err != nil {
			return finish(err)
		}
		var l s3ListBuckets
		if err := xml.Unmarshal(body, &l); err != nil {
			return finish(errors.New("the address does not answer like S3"))
		}
		for _, b := range l.Buckets {
			r.Buckets = append(r.Buckets, b.Name)
		}
		r.Listed = true
		return finish(nil)
	}
	r.Buckets = t.Buckets
	var firstErr error
	for _, b := range t.Buckets {
		if _, err := s3Get(ctx, t, "/"+b, url.Values{"list-type": {"2"}, "max-keys": {"1"}}); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("bucket %s: %w", b, err)
		}
	}
	return finish(firstErr)
}

// s3Measure sums the sizes of the objects in one bucket by listing them.
func s3Measure(ctx context.Context, t S3Target, bucket string) (s3BucketSize, error) {
	out := s3BucketSize{Bucket: bucket}
	token := ""
	for page := 0; ; page++ {
		if page >= s3MaxPages {
			out.Partial = true
			return out, nil
		}
		q := url.Values{"list-type": {"2"}, "max-keys": {"1000"}}
		if token != "" {
			q.Set("continuation-token", token)
		}
		body, err := s3Get(ctx, t, "/"+bucket, q)
		if err != nil {
			return out, err
		}
		var l s3ListObjects
		if err := xml.Unmarshal(body, &l); err != nil {
			return out, errors.New("the answer is not a list of objects")
		}
		for _, c := range l.Contents {
			out.Bytes += c.Size
			out.Objects++
		}
		if !l.IsTruncated || l.NextToken == "" {
			return out, nil
		}
		token = l.NextToken
	}
}

func validS3Target(t S3Target) (*url.URL, bool) {
	u, err := url.Parse(t.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return nil, false
	}
	return u, t.AccessKey != "" && t.SecretKey != ""
}

// CollectS3 checks every storage that is due (once a minute) and measures buckets that are due (once an hour).
func CollectS3(ctx context.Context, targets []S3Target, now int64) []Point {
	var (
		mu  sync.Mutex
		pts []Point
		wg  sync.WaitGroup
		sem = make(chan struct{}, 4)
	)
	for _, t := range targets {
		if _, ok := validS3Target(t); !ok {
			continue
		}
		key := t.Name + "\x00" + t.URL + "\x00" + t.AccessKey
		s3Mu.Lock()
		st := s3States[key]
		if st == nil {
			st = &s3State{}
			s3States[key] = st
		}
		due := time.Since(st.at) >= s3Every
		if due {
			st.at = time.Now()
		}
		s3Mu.Unlock()
		if !due {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(t S3Target, st *s3State) {
			defer wg.Done()
			defer func() { <-sem }()
			res := S3Check(ctx, t)
			base := map[string]string{"s3": t.Name}
			g := func(name string, v float64, a map[string]string) Point {
				return Point{Service: "host", Name: name, Type: "gauge", Value: v, TimeNs: now, Attrs: a}
			}
			up := 0.0
			if res.Up {
				up = 1
			}
			out := []Point{g("s3_up", up, base), g("s3_response_seconds", res.Seconds, base),
				g("s3_info", 1, map[string]string{"s3": t.Name, "url": safeURL(t.URL), "reason": res.Reason})}
			if res.Listed {
				out = append(out, g("s3_buckets", float64(len(res.Buckets)), base))
			}
			if res.Up && t.Size {
				s3Mu.Lock()
				measure := time.Since(st.sizeAt) >= s3SizeEvery
				if measure {
					st.sizeAt = time.Now()
				}
				cached := st.sizes
				s3Mu.Unlock()
				if measure {
					bs := res.Buckets
					if len(bs) > s3MaxSizedBkts {
						bs = bs[:s3MaxSizedBkts]
					}
					mctx, cancel := context.WithTimeout(ctx, s3SizeBudget)
					var sizes []s3BucketSize
					for _, b := range bs {
						sz, err := s3Measure(mctx, t, b)
						if err != nil {
							continue // a bucket the key may not list is left out
						}
						sizes = append(sizes, sz)
					}
					cancel()
					s3Mu.Lock()
					st.sizes = sizes
					s3Mu.Unlock()
					cached = sizes
				}
				for _, sz := range cached {
					a := map[string]string{"s3": t.Name, "bucket": sz.Bucket}
					out = append(out, g("s3_bucket_size_bytes", sz.Bytes, a), g("s3_bucket_objects", sz.Objects, a))
					if sz.Partial {
						out = append(out, g("s3_bucket_size_partial", 1, a))
					}
				}
			}
			mu.Lock()
			pts = append(pts, out...)
			mu.Unlock()
		}(t, st)
	}
	wg.Wait()
	return pts
}
