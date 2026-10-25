package store

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckpointsExcludeCredentialsAndLocks(t *testing.T) {
	dir := t.TempDir()
	for name, value := range map[string]string{"2026-10-23/input.json": "{}", ".env": "secret", "run.lock": "lock"} {
		if err := Write(filepath.Join(dir, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	data, err := Pack(dir)
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := Unpack(dest, data); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "2026-10-23/input.json")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".env", "run.lock"} {
		if _, err := os.Stat(filepath.Join(dest, name)); !os.IsNotExist(err) {
			t.Fatalf("restored %s", name)
		}
	}
	if Unpack(dest, data) == nil {
		t.Fatal("overwrote existing state")
	}
}
func TestArchiveTraversalRejected(t *testing.T) {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "../escaped.json", Mode: 0600, Size: 2})
	tw.Write([]byte("{}"))
	tw.Close()
	gz.Close()
	if Unpack(t.TempDir(), b.Bytes()) == nil {
		t.Fatal("accepted traversal")
	}
}

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestR2UsesPrivateCachePolicyAndSignedRequest(t *testing.T) {
	r := R2{AccountID: strings.Repeat("a", 32), Bucket: "daywire-state", AccessKey: "test", SecretKey: "secret", Client: &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Cache-Control") != "private, no-store" || !strings.Contains(req.Header.Get("Authorization"), "/auto/s3/aws4_request") {
			t.Fatal("incorrect private request")
		}
		if req.URL.Path != "/daywire-state/runs/test.tar.gz" {
			t.Fatal(req.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})}}
	url, err := r.Put(context.Background(), "runs/test.tar.gz", "application/gzip", []byte("test"))
	if err != nil || url != "" {
		t.Fatalf("url=%q err=%v", url, err)
	}
}
