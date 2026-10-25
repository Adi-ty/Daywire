package store

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Adi-ty/daywire/internal/content"
)

// R2 uses S3 Signature V4 for one immutable media object. Buckets and public
// custom domains must already exist; this code never changes account settings.
type R2 struct {
	AccountID, Bucket, AccessKey, SecretKey, PublicURL string
	Client                                             *http.Client
}

func hmacSHA(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func (r R2) Validate() error {
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(r.AccountID) || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`).MatchString(r.Bucket) || r.AccessKey == "" || r.SecretKey == "" {
		return errors.New("configure R2 account, bucket, access key, and secret key")
	}
	public, err := url.Parse(r.PublicURL)
	if err != nil || public.RawQuery != "" || public.Fragment != "" || (r.PublicURL != "" && !content.WebURL(r.PublicURL)) {
		return errors.New("invalid media public base URL")
	}
	return nil
}

func (r R2) request(ctx context.Context, method, key, mime string, data []byte) (*http.Response, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9/_.-]+$`).MatchString(key) || strings.Contains(key, "..") || strings.HasPrefix(key, "/") {
		return nil, errors.New("invalid media object key")
	}
	now := time.Now().UTC()
	date := now.Format("20060102")
	stamp := now.Format("20060102T150405Z")
	host := r.AccountID + ".r2.cloudflarestorage.com"
	path := "/" + r.Bucket + "/" + key
	payloadHash := content.Hash(data)
	canonicalHeaders := "host:" + host + "\nx-amz-content-sha256:" + payloadHash + "\nx-amz-date:" + stamp + "\n"
	signed := "host;x-amz-content-sha256;x-amz-date"
	canonical := method + "\n" + path + "\n\n" + canonicalHeaders + "\n" + signed + "\n" + payloadHash
	scope := date + "/auto/s3/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + content.Hash([]byte(canonical))
	keyDate := hmacSHA([]byte("AWS4"+r.SecretKey), date)
	keyRegion := hmacSHA(keyDate, "auto")
	keyService := hmacSHA(keyRegion, "s3")
	signature := hex.EncodeToString(hmacSHA(hmacSHA(keyService, "aws4_request"), stringToSign))
	req, err := http.NewRequestWithContext(ctx, method, "https://"+host+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mime)
	if r.PublicURL != "" {
		req.Header.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		req.Header.Set("Cache-Control", "private, no-store")
	}
	req.Header.Set("x-amz-date", stamp)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+r.AccessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+signature)
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("storage redirects disabled") }}
	}
	return client.Do(req)
}

func (r R2) Put(ctx context.Context, key, mime string, data []byte) (string, error) {
	resp, err := r.request(ctx, http.MethodPut, key, mime, data)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		return "", fmt.Errorf("R2 upload returned HTTP %d", resp.StatusCode)
	}
	if r.PublicURL == "" {
		return "", nil
	}
	return strings.TrimRight(r.PublicURL, "/") + "/" + key, nil
}

func (r R2) Get(ctx context.Context, key string, maxBytes int64) ([]byte, error) {
	resp, err := r.request(ctx, http.MethodGet, key, "application/octet-stream", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, os.ErrNotExist
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("R2 download returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("R2 object exceeds size limit")
	}
	return data, nil
}
