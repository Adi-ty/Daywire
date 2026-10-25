package fetch

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPublicIP(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.2.3.4", "169.254.169.254", "100.100.100.200", "::1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "192.0.2.1", "64:ff9b::a00:1"} {
		if PublicIP(netip.MustParseAddr(ip)) {
			t.Errorf("allowed %s", ip)
		}
	}
	if !PublicIP(netip.MustParseAddr("1.1.1.1")) {
		t.Fatal("blocked public IP")
	}
}
func TestURLPolicyAndSizeLimit(t *testing.T) {
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("12345")), Header: http.Header{}}, nil
	})}
	for _, raw := range []string{"http://example.com", "https://user:password@example.com", "https://example.com:8080/"} {
		if _, err := Get(context.Background(), client, raw, 10); err == nil {
			t.Errorf("allowed %s", raw)
		}
	}
	if _, err := Get(context.Background(), client, "https://example.com", 4); err == nil {
		t.Fatal("accepted oversized body")
	}
}
func TestCanonicalPreservesMeaningfulQuery(t *testing.T) {
	got := Canonical("https://EXAMPLE.com/story?v=2&utm_source=hn#intro")
	if got != "https://example.com/story?v=2" {
		t.Fatal(got)
	}
}
