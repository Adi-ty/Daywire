// Package fetch handles untrusted publisher URLs, without access to provider credentials.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var excluded = []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "64:ff9b:1::/48", "2002::/16"}

func PublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, raw := range excluded {
		if netip.MustParsePrefix(raw).Contains(ip) {
			return false
		}
	}
	return true
}

func allowedURL(u *url.URL) error {
	if u.Scheme != "https" || u.User != nil || u.Hostname() == "" || (u.Port() != "" && u.Port() != "443") {
		return errors.New("only public HTTPS URLs on port 443 are allowed")
	}
	return nil
}

func NewClient() *http.Client {
	transport := &http.Transport{
		TLSHandshakeTimeout: 8 * time.Second, ResponseHeaderTimeout: 12 * time.Second,
		MaxIdleConns: 20, MaxIdleConnsPerHost: 2, MaxConnsPerHost: 2, IdleConnTimeout: 30 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, errors.New("empty DNS answer")
			}
			// Reject the whole DNS answer if any address is private. Dial the
			// validated IP directly, preventing a second DNS lookup/rebinding.
			for _, ip := range ips {
				if !PublicIP(ip) {
					return nil, errors.New("non-public destination blocked")
				}
			}
			var last error
			for _, ip := range ips {
				conn, err := (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
				last = err
			}
			return nil, last
		},
	}
	return &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return allowedURL(req.URL)
	}}
}

func Get(ctx context.Context, client *http.Client, raw string, maxBytes int64) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if err := allowedURL(u); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "Daywire/0.1 (+https://github.com/Adi-ty/daywire)")
		req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/json, text/html;q=0.9, */*;q=0.5")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if (resp.StatusCode == 429 || resp.StatusCode >= 500) && attempt < 2 {
			resp.Body.Close()
			timer := time.NewTimer(time.Duration(attempt+1) * time.Second)
			select {
			case <-timer.C:
				continue
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			}
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("source returned HTTP %d", resp.StatusCode)
		}
		if readErr != nil {
			return nil, readErr
		}
		if int64(len(data)) > maxBytes {
			return nil, errors.New("source exceeds response size limit")
		}
		return data, nil
	}
	return nil, errors.New("source retry budget exhausted")
}

func Canonical(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	q := u.Query()
	for key := range q {
		if strings.HasPrefix(strings.ToLower(key), "utm_") || key == "fbclid" || key == "gclid" {
			q.Del(key)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}
