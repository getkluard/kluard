package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The example "GET Bucket Lifecycle" from the AWS documentation of Signature Version 4 for Amazon S3.
func TestS3SignatureMatchesAWSExample(t *testing.T) {
	tg := S3Target{AccessKey: "AKIAIOSFODNN7EXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	auth, date := s3Sign(tg, "examplebucket.s3.amazonaws.com", "/", s3Query(map[string][]string{"lifecycle": {""}}), time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))
	if date != "20130524T000000Z" || !strings.HasSuffix(auth, "Signature=fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543") {
		t.Fatalf("%s", auth)
	}
}

func fakeS3(t *testing.T, access, secret string, objects map[string][]int) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tg := S3Target{AccessKey: access, SecretKey: secret}
		d, _ := time.Parse("20060102T150405Z", r.Header.Get("x-amz-date"))
		want, _ := s3Sign(tg, r.Host, r.URL.EscapedPath(), r.URL.RawQuery, d)
		if r.Header.Get("Authorization") != want {
			w.WriteHeader(403)
			fmt.Fprint(w, `<Error><Code>SignatureDoesNotMatch</Code></Error>`)
			return
		}
		if r.URL.Path == "/" {
			fmt.Fprint(w, `<ListAllMyBucketsResult><Buckets>`)
			for b := range objects {
				fmt.Fprintf(w, `<Bucket><Name>%s</Name></Bucket>`, b)
			}
			fmt.Fprint(w, `</Buckets></ListAllMyBucketsResult>`)
			return
		}
		sizes, ok := objects[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			w.WriteHeader(404)
			fmt.Fprint(w, `<Error><Code>NoSuchBucket</Code></Error>`)
			return
		}
		// pages of 2 objects, to exercise the continuation token
		start := 0
		if tok := r.URL.Query().Get("continuation-token"); tok != "" {
			fmt.Sscanf(tok, "t%d", &start)
		}
		end := start + 2
		trunc := end < len(sizes)
		if end > len(sizes) {
			end = len(sizes)
		}
		fmt.Fprintf(w, `<ListBucketResult><IsTruncated>%v</IsTruncated>`, trunc)
		if trunc {
			fmt.Fprintf(w, `<NextContinuationToken>t%d</NextContinuationToken>`, end)
		}
		for _, s := range sizes[start:end] {
			fmt.Fprintf(w, `<Contents><Key>k</Key><Size>%d</Size></Contents>`, s)
		}
		fmt.Fprint(w, `</ListBucketResult>`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestS3CheckAndSize(t *testing.T) {
	srv := fakeS3(t, "AK", "SK", map[string][]int{"photos": {100, 200, 300, 400, 500}, "empty": {}})
	tg := S3Target{Name: "store", URL: srv.URL, AccessKey: "AK", SecretKey: "SK", Size: true}
	res := S3Check(context.Background(), tg)
	if !res.Up || !res.Listed || len(res.Buckets) != 2 {
		t.Fatalf("%+v", res)
	}
	sz, err := s3Measure(context.Background(), tg, "photos")
	if err != nil || sz.Bytes != 1500 || sz.Objects != 5 || sz.Partial {
		t.Fatalf("%+v %v", sz, err)
	}
	sz, err = s3Measure(context.Background(), tg, "empty")
	if err != nil || sz.Objects != 0 {
		t.Fatalf("%+v %v", sz, err)
	}
	// a wrong secret is reported as such, and the secret never shows up in the text
	bad := tg
	bad.SecretKey = "nope-secret-value"
	res = S3Check(context.Background(), bad)
	if res.Up || !strings.Contains(res.Reason, "refused") || strings.Contains(res.Reason, "nope-secret-value") {
		t.Fatalf("%+v", res)
	}
	// a key limited to named buckets does not need to list them all
	lim := tg
	lim.Buckets = []string{"photos"}
	if res = S3Check(context.Background(), lim); !res.Up || res.Listed {
		t.Fatalf("%+v", res)
	}
	lim.Buckets = []string{"missing"}
	if res = S3Check(context.Background(), lim); res.Up || !strings.Contains(res.Reason, "missing") {
		t.Fatalf("%+v", res)
	}
}

func TestCollectS3Points(t *testing.T) {
	srv := fakeS3(t, "AK", "SK", map[string][]int{"photos": {100, 200, 300}})
	pts := CollectS3(context.Background(), []S3Target{{Name: "store-collect", URL: srv.URL, AccessKey: "AK", SecretKey: "SK", Size: true}}, time.Now().UnixNano())
	got := map[string]float64{}
	for _, p := range pts {
		if p.Attrs["bucket"] != "" {
			got[p.Name+"/"+p.Attrs["bucket"]] = p.Value
		} else {
			got[p.Name] = p.Value
		}
		for _, v := range p.Attrs {
			if strings.Contains(v, "SK") && v != "SK" {
				t.Fatalf("a secret in %v", p.Attrs)
			}
		}
	}
	if got["s3_up"] != 1 || got["s3_buckets"] != 1 || got["s3_bucket_size_bytes/photos"] != 600 || got["s3_bucket_objects/photos"] != 3 {
		t.Fatalf("%v", got)
	}
	// without keys nothing is sent
	if n := len(CollectS3(context.Background(), []S3Target{{Name: "x", URL: srv.URL}}, 1)); n != 0 {
		t.Fatal("a target without keys must be skipped")
	}
}
