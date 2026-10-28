// Package update looks up the latest skb release on GitHub.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	// DefaultURL is the GitHub API endpoint of the latest release.
	DefaultURL = "https://api.github.com/repos/elias-knodel/skb/releases/latest"
	// ReleasesURL is the fallback link when the API returns no usable URL.
	ReleasesURL = "https://github.com/elias-knodel/skb/releases/latest"
	// Interval is the minimum time between two requests of CheckDaily.
	Interval = 24 * time.Hour

	requestTimeout = 5 * time.Second
	maxBody        = 1 << 20
)

// describeSuffix matches the commit suffix of git describe output.
var describeSuffix = regexp.MustCompile(`-[0-9]+-g[0-9a-f]+$`)

// Checker queries the latest release and caches the answer.
type Checker struct {
	// Client defaults to a client with a 5 s timeout.
	Client *http.Client
	// URL defaults to DefaultURL.
	URL string
	// CachePath is the cache file; empty disables caching.
	CachePath string
	// UserAgent defaults to "skb".
	UserAgent string
	// Now defaults to time.Now.
	Now func() time.Time
}

// Result compares the running version with the latest release.
type Result struct {
	Current, Latest, URL string
	// Comparable reports whether Current is a release version.
	Comparable bool
	// Available reports whether Latest is newer than Current.
	Available bool
}

type release struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
	URL       string    `json:"url"`
}

// DefaultCachePath returns the cache file below the user cache directory.
func DefaultCachePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate cache directory: %w", err)
	}

	return filepath.Join(dir, "skb", "update.json"), nil
}

// Releasable reports whether v is the version of a tagged release build.
func Releasable(v string) bool {
	if !semver.IsValid(v) || strings.HasSuffix(v, "-dirty") {
		return false
	}

	return !describeSuffix.MatchString(v)
}

// Newer reports whether latest is a newer release than current.
func Newer(current, latest string) bool {
	return Releasable(current) && semver.IsValid(latest) && semver.Compare(latest, current) > 0
}

func (c Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}

	return time.Now()
}

// Check always queries GitHub and refreshes the cache.
func (c Checker) Check(ctx context.Context, current string) (Result, error) {
	rel, err := c.fetch(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("check for updates: %w", err)
	}

	rel.CheckedAt = c.now()
	c.writeCache(rel)

	return result(current, rel), nil
}

// CheckDaily answers from the cache while it is younger than Interval and
// queries GitHub otherwise. Non-release builds and a missing CachePath skip
// the check entirely. A failed request is recorded so that it is not retried
// before the interval has passed.
func (c Checker) CheckDaily(ctx context.Context, current string) (Result, error) {
	if !Releasable(current) || c.CachePath == "" {
		return Result{}, nil
	}

	now := c.now()
	cached, ok := c.readCache()

	if ok && !cached.CheckedAt.After(now) && now.Sub(cached.CheckedAt) < Interval {
		return result(current, cached), nil
	}

	rel, err := c.fetch(ctx)
	if err != nil {
		prev := cached
		if !ok {
			prev = release{}
		}

		prev.CheckedAt = now
		c.writeCache(prev)

		return Result{}, fmt.Errorf("check for updates: %w", err)
	}

	rel.CheckedAt = now
	c.writeCache(rel)

	return result(current, rel), nil
}

func result(current string, rel release) Result {
	return Result{
		Current:    current,
		Latest:     rel.Latest,
		URL:        safeURL(rel.URL),
		Comparable: Releasable(current),
		Available:  Newer(current, rel.Latest),
	}
}

func (c Checker) fetch(ctx context.Context) (release, error) {
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}

	target := c.URL
	if target == "" {
		target = DefaultURL
	}

	agent := c.UserAgent
	if agent == "" {
		agent = "skb"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return release{}, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", agent)

	resp, err := client.Do(req)
	if err != nil {
		return release{}, fmt.Errorf("request: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return release{}, errors.New("no published release")
	case resp.StatusCode != http.StatusOK:
		return release{}, fmt.Errorf("unexpected status %s", resp.Status)
	}

	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&body); err != nil {
		return release{}, fmt.Errorf("decode response: %w", err)
	}

	if !semver.IsValid(body.TagName) {
		return release{}, fmt.Errorf("invalid release tag %q", body.TagName)
	}

	return release{Latest: body.TagName, URL: safeURL(body.HTMLURL)}, nil
}

// safeURL returns link if it points to github.com over https, else ReleasesURL.
func safeURL(link string) string {
	if u, err := url.Parse(link); err != nil || u.Scheme != "https" || u.Host != "github.com" {
		return ReleasesURL
	}

	return link
}

// readCache returns the cached answer; a missing or corrupt file or invalid one counts as stale.
func (c Checker) readCache() (release, bool) {
	if c.CachePath == "" {
		return release{}, false
	}

	data, err := os.ReadFile(c.CachePath)
	if err != nil {
		return release{}, false
	}

	var rel release
	if err := json.Unmarshal(data, &rel); err != nil || !semver.IsValid(rel.Latest) {
		return release{}, false
	}

	return rel, true
}

// writeCache stores rel atomically; failures are ignored.
func (c Checker) writeCache(rel release) {
	if c.CachePath == "" {
		return
	}

	data, err := json.Marshal(rel)
	if err != nil {
		return
	}

	dir := filepath.Dir(c.CachePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}

	tmp, err := os.CreateTemp(dir, ".update.json.*")
	if err != nil {
		return
	}

	_, werr := tmp.Write(data)
	cherr := tmp.Chmod(0o600)
	clerr := tmp.Close()

	if werr != nil || cherr != nil || clerr != nil || os.Rename(tmp.Name(), c.CachePath) != nil {
		_ = os.Remove(tmp.Name())
	}
}

// Report prints the comparison for the check-update command.
func Report(w io.Writer, r Result) {
	_, _ = fmt.Fprintf(w, "current: %s\nlatest:  %s\n", r.Current, r.Latest)

	switch {
	case !r.Comparable:
		_, _ = fmt.Fprintln(w, "not a release build, versions not compared")
	case r.Available:
		_, _ = fmt.Fprintf(w, "update available: %s\n", r.URL)
	default:
		_, _ = fmt.Fprintln(w, "up to date")
	}
}
