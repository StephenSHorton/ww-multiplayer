package update

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Client asks a release API and downloads its assets.
type Client struct {
	// URL is the latest-release API: DefaultURL, or EnvURL's value.
	URL string
	// HTTP makes the requests; nil is a client with a 15 s timeout for
	// the check (Latest) and none for a download (its context ends it).
	HTTP *http.Client
	// UserAgent names the app to GitHub (which requires one).
	UserAgent string
	// Smoke checks the installed new version runs (Install); nil is Smoke.
	Smoke func(ctx context.Context, exe, tag string) error
}

// NewClient is the client for getenv's EnvURL, or DefaultURL.
func NewClient(getenv func(string) string, version string) *Client {
	u := DefaultURL
	if getenv != nil {
		if v := strings.TrimSpace(getenv(EnvURL)); v != "" {
			u = v
		}
	}
	return &Client{URL: u, UserAgent: "ww-multiplayer/" + version}
}

// checkTimeout bounds a Latest request without a deadline of its own.
const checkTimeout = 15 * time.Second

// maxRedirects bounds a download's redirects (GitHub sends one, to its
// asset storage).
const maxRedirects = 5

// SecureURL refuses a URL a check or download may not use: anything but
// https, except plain http to a loopback address (a test's fake server).
func SecureURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%w: %q", ErrInsecureURL, raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopback(u.Hostname()) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrInsecureURL, u.Redacted())
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c *Client) http(check bool) *http.Client {
	base := c.HTTP
	if base == nil {
		base = &http.Client{}
		if check {
			base.Timeout = checkTimeout
		}
	}
	cl := *base
	// Every redirect must be as secure as the first URL.
	cl.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return errors.New("update: too many redirects")
		}
		return SecureURL(req.URL.String())
	}
	return &cl
}

func (c *Client) get(ctx context.Context, raw string, check bool, accept string) (*http.Response, error) {
	if err := SecureURL(raw); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := c.http(check).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%w: %s answered %s", ErrServer, hostOf(raw), resp.Status)
	}
	return resp, nil
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return "the server"
}

// Latest asks the API for the newest release.
func (c *Client) Latest(ctx context.Context) (Release, error) {
	resp, err := c.get(ctx, c.URL, true, "application/vnd.github+json")
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxJSONSize+1))
	if err != nil {
		return Release{}, err
	}
	if len(b) > MaxJSONSize {
		return Release{}, fmt.Errorf("%w: the release description is over %d bytes", ErrTooLarge, MaxJSONSize)
	}
	var r Release
	if err := json.Unmarshal(b, &r); err != nil {
		return Release{}, fmt.Errorf("update: the release description isn't valid: %w", err)
	}
	if _, err := ParseTag(r.Tag); err != nil {
		return Release{}, fmt.Errorf("update: the latest release's tag %q isn't a version", r.Tag)
	}
	if SecureURL(r.HTMLURL) != nil {
		r.HTMLURL = "" // never open what a release names unless it is https
	}
	return r, nil
}

// Check asks for the latest release when st is Due at now and returns
// the answer for current with the new cached state; otherwise the cached
// answer, with no request. A dev build never asks (Result.Current is
// set, Available false). A failed request still records LastCheck (so an
// offline machine doesn't retry on every start) and keeps the old answer.
func (c *Client) Check(ctx context.Context, current string, st State, now time.Time) (Result, State, error) {
	if IsDev(current) {
		return Result{Current: current}, st, nil
	}
	if !Due(st.LastCheck, now, CheckEvery) {
		return FromState(current, st), st, nil
	}
	st.LastCheck = now
	rel, err := c.Latest(ctx)
	if err != nil {
		return FromState(current, st), st, err
	}
	if !rel.Draft && !rel.Prerelease && !IsPrerelease(rel.Tag) {
		st.Latest, st.NotesURL = rel.Tag, rel.HTMLURL
	}
	r := FromState(current, st)
	r.Cached = false
	return r, st, nil
}

// ParseSums reads a SHA256SUMS file (sha256sum's "<hex>  <name>" lines,
// "*name" in binary mode) into name → lowercase hex digest.
func ParseSums(b []byte) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sum, name, ok := strings.Cut(line, " ")
		if !ok {
			return nil, fmt.Errorf("update: SHA256SUMS line %q has no file name", line)
		}
		name = strings.TrimPrefix(strings.TrimLeft(name, " "), "*")
		if len(sum) != sha256.Size*2 {
			return nil, fmt.Errorf("update: SHA256SUMS line for %q has no SHA-256", name)
		}
		if _, err := hex.DecodeString(sum); err != nil {
			return nil, fmt.Errorf("update: SHA256SUMS line for %q has no SHA-256", name)
		}
		out[name] = strings.ToLower(sum)
	}
	return out, sc.Err()
}

// Progress reports a download: done bytes of total (0 when unknown).
type Progress func(done, total int64)

// Download fetches rel's asset name into a new temp file in dir (the
// app's own folder, so the swap is a rename on one volume), checking it
// against rel's SHA256SUMS. On any error, a hash mismatch included, the
// temp file is removed and nothing else was touched; on success the
// caller owns the returned file.
func (c *Client) Download(ctx context.Context, rel Release, name, dir string, progress Progress) (string, error) {
	asset, ok := rel.Asset(name)
	if !ok {
		return "", fmt.Errorf("%w (%s has no %s)", ErrNoAsset, rel.Tag, name)
	}
	sumsAsset, ok := rel.Asset(AssetSums)
	if !ok {
		return "", fmt.Errorf("%w (%s has no %s to check it with)", ErrNoAsset, rel.Tag, AssetSums)
	}
	limit := assetLimit
	if asset.Size > limit {
		return "", fmt.Errorf("%w: %s is %d bytes, the limit is %d", ErrTooLarge, name, asset.Size, limit)
	}
	want, err := c.sum(ctx, sumsAsset, name)
	if err != nil {
		return "", err
	}

	resp, err := c.get(ctx, asset.URL, false, "application/octet-stream")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.ContentLength > limit {
		return "", fmt.Errorf("%w: %s is %d bytes, the limit is %d", ErrTooLarge, name, resp.ContentLength, limit)
	}
	total := asset.Size
	if total <= 0 && resp.ContentLength > 0 {
		total = resp.ContentLength
	}

	f, err := os.CreateTemp(dir, TempPrefix+"*.tmp")
	if err != nil {
		return "", fmt.Errorf("update: can't write next to the app in %s: %w", dir, err)
	}
	tmp := f.Name()
	ok = false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(tmp)
		}
	}()
	h := sha256.New()
	pw := &progressWriter{total: total, fn: progress}
	n, err := io.Copy(io.MultiWriter(f, h, pw), io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", fmt.Errorf("update: downloading %s: %w", name, err)
	}
	if n > limit {
		return "", fmt.Errorf("%w: %s is over %d bytes", ErrTooLarge, name, limit)
	}
	if asset.Size > 0 && n != asset.Size {
		return "", fmt.Errorf("update: %s ended after %d of %d bytes", name, n, asset.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return "", fmt.Errorf("%w (%s: got %s, want %s)", ErrHashMismatch, name, got[:12], want[:12])
	}
	if err := f.Sync(); err != nil {
		return "", fmt.Errorf("update: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("update: %w", err)
	}
	ok = true
	return filepath.Clean(tmp), nil
}

// sum downloads the SHA256SUMS asset and returns name's digest.
func (c *Client) sum(ctx context.Context, sums Asset, name string) (string, error) {
	if sums.Size > MaxSumsSize {
		return "", fmt.Errorf("%w: %s is %d bytes", ErrTooLarge, AssetSums, sums.Size)
	}
	resp, err := c.get(ctx, sums.URL, false, "application/octet-stream")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxSumsSize+1))
	if err != nil {
		return "", fmt.Errorf("update: downloading %s: %w", AssetSums, err)
	}
	if len(b) > MaxSumsSize {
		return "", fmt.Errorf("%w: %s is over %d bytes", ErrTooLarge, AssetSums, MaxSumsSize)
	}
	sums2, err := ParseSums(b)
	if err != nil {
		return "", err
	}
	want, ok := sums2[name]
	if !ok {
		return "", fmt.Errorf("%w (%s has no line for %s)", ErrNoAsset, AssetSums, name)
	}
	return want, nil
}

// assetLimit is MaxAssetSize; a test lowers it.
var assetLimit int64 = MaxAssetSize

// progressWriter calls fn as bytes go by, at most every 64 KiB and at
// the end.
type progressWriter struct {
	done, total, last int64
	fn                Progress
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if p.fn != nil && (p.done-p.last >= 64<<10 || p.done == p.total) {
		p.last = p.done
		p.fn(p.done, p.total)
	}
	return len(b), nil
}
