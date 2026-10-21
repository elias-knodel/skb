package update_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elias-knodel/skb/internal/update"
)

var fixedNow = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

const releaseURL = "https://github.com/elias-knodel/skb/releases/tag/"

type server struct {
	*httptest.Server

	hits   atomic.Int32
	header atomic.Pointer[http.Header]
}

// newServer answers every request with status and body.
func newServer(t *testing.T, status int, body string) *server {
	t.Helper()

	s := &server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)

		h := r.Header.Clone()
		s.header.Store(&h)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))

	t.Cleanup(s.Close)

	return s
}

func releaseBody(tag string) string {
	return fmt.Sprintf(`{"tag_name":%q,"html_url":%q}`, tag, releaseURL+tag)
}

type cacheFile struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
	URL       string    `json:"url"`
}

func writeCache(t *testing.T, path string, c cacheFile) {
	t.Helper()

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readCache(t *testing.T, path string) cacheFile {
	t.Helper()

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}

	var c cacheFile
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}

	return c
}

func newChecker(s *server, cache string) update.Checker {
	return update.Checker{
		URL:       s.URL,
		CachePath: cache,
		UserAgent: "skb/test",
		Now:       func() time.Time { return fixedNow },
	}
}

func cachePath(t *testing.T) string { return filepath.Join(t.TempDir(), "skb", "update.json") }

func TestReleasable(t *testing.T) {
	t.Parallel()

	for v, want := range map[string]bool{
		"v1.2.3":                  true,
		"v1.1.1-alpha":            true,
		"v1.2.3-rc.1":             true,
		"dev":                     false,
		"":                        false,
		"1.2.3":                   false,
		"abc1234":                 false,
		"v1.2.3-dirty":            false,
		"v1.2.3-4-gabc1234":       false,
		"v1.2.3-4-gabc1234-dirty": false,
	} {
		if got := update.Releasable(v); got != want {
			t.Errorf("Releasable(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestNewer(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		current, latest string
		want            bool
	}{
		{"v1.1.0", "v1.2.0", true},
		{"v1.1.1-alpha", "v1.1.1", true},
		{"v1.2.0", "v1.2.0", false},
		{"v1.3.0", "v1.2.0", false},
		{"v1.10.0", "v1.9.0", false},
		{"v1.1.1-alpha", "v1.1.0", false},
		{"v1.1.0", "garbage", false},
		{"dev", "v9.0.0", false},
	} {
		if got := update.Newer(tc.current, tc.latest); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestCheckFetchesAndCaches(t *testing.T) {
	t.Parallel()

	s := newServer(t, http.StatusOK, releaseBody("v1.2.0"))
	path := cachePath(t)

	res, err := newChecker(s, path).Check(context.Background(), "v1.1.0")
	if err != nil {
		t.Fatal(err)
	}

	if !res.Available || res.Latest != "v1.2.0" || res.URL != releaseURL+"v1.2.0" || !res.Comparable {
		t.Errorf("result = %+v", res)
	}

	h := s.header.Load()
	if h.Get("Accept") != "application/vnd.github+json" || h.Get("User-Agent") != "skb/test" {
		t.Errorf("headers = %v", *h)
	}

	c := readCache(t, path)
	if !c.CheckedAt.Equal(fixedNow) || c.Latest != "v1.2.0" || c.URL != releaseURL+"v1.2.0" {
		t.Errorf("cache = %+v", c)
	}

	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("file: %v %v", st, err)
	}

	if st, err := os.Stat(filepath.Dir(path)); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("dir: %v %v", st, err)
	}
}

func TestCheckBypassesCache(t *testing.T) {
	t.Parallel()

	s := newServer(t, http.StatusOK, releaseBody("v1.3.0"))
	path := cachePath(t)

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	writeCache(t, path, cacheFile{CheckedAt: fixedNow.Add(-time.Hour), Latest: "v1.2.0", URL: releaseURL + "v1.2.0"})

	res, err := newChecker(s, path).Check(context.Background(), "v1.1.0")
	if err != nil {
		t.Fatal(err)
	}

	if s.hits.Load() != 1 || res.Latest != "v1.3.0" {
		t.Errorf("hits=%d result=%+v", s.hits.Load(), res)
	}

	if c := readCache(t, path); c.Latest != "v1.3.0" || !c.CheckedAt.Equal(fixedNow) {
		t.Errorf("cache = %+v", c)
	}
}

func seeded(t *testing.T, age time.Duration) (*server, string) {
	t.Helper()

	s := newServer(t, http.StatusOK, releaseBody("v1.3.0"))
	path := cachePath(t)

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	writeCache(t, path, cacheFile{CheckedAt: fixedNow.Add(-age), Latest: "v1.2.0", URL: releaseURL + "v1.2.0"})

	return s, path
}

func TestCheckDailyFreshCache(t *testing.T) {
	t.Parallel()

	s, path := seeded(t, time.Hour)

	res, err := newChecker(s, path).CheckDaily(context.Background(), "v1.1.0")
	if err != nil || !res.Available || res.Latest != "v1.2.0" || s.hits.Load() != 0 {
		t.Errorf("res=%+v err=%v hits=%d", res, err, s.hits.Load())
	}
}

func TestCheckDailyStaleCache(t *testing.T) {
	t.Parallel()

	s, path := seeded(t, 25*time.Hour)

	res, err := newChecker(s, path).CheckDaily(context.Background(), "v1.1.0")
	if err != nil || res.Latest != "v1.3.0" || s.hits.Load() != 1 {
		t.Errorf("res=%+v err=%v hits=%d", res, err, s.hits.Load())
	}

	if c := readCache(t, path); c.Latest != "v1.3.0" || !c.CheckedAt.Equal(fixedNow) {
		t.Errorf("cache = %+v", c)
	}
}

func TestCheckDailyFutureTimestamp(t *testing.T) {
	t.Parallel()

	s, path := seeded(t, -48*time.Hour)

	if _, err := newChecker(s, path).CheckDaily(context.Background(), "v1.1.0"); err != nil || s.hits.Load() != 1 {
		t.Errorf("err=%v hits=%d", err, s.hits.Load())
	}
}

func TestCheckDailyCorruptCache(t *testing.T) {
	t.Parallel()

	s, path := seeded(t, time.Hour)

	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := newChecker(s, path).CheckDaily(context.Background(), "v1.1.0"); err != nil || s.hits.Load() != 1 {
		t.Errorf("err=%v hits=%d", err, s.hits.Load())
	}
}

func TestCheckDailyAfterUpgrade(t *testing.T) {
	t.Parallel()

	s, path := seeded(t, time.Hour)

	res, err := newChecker(s, path).CheckDaily(context.Background(), "v1.2.0")
	if err != nil || res.Available || s.hits.Load() != 0 {
		t.Errorf("res=%+v err=%v hits=%d", res, err, s.hits.Load())
	}
}

func TestCheckDailySkipsNonRelease(t *testing.T) {
	t.Parallel()

	s := newServer(t, http.StatusOK, releaseBody("v1.3.0"))
	path := cachePath(t)

	for _, v := range []string{"dev", "v1.2.3-4-gabc1234"} {
		res, err := newChecker(s, path).CheckDaily(context.Background(), v)
		if err != nil || res != (update.Result{}) {
			t.Errorf("%s: res=%+v err=%v", v, res, err)
		}
	}

	if _, err := os.Stat(path); err == nil {
		t.Error("cache file written")
	}

	if s.hits.Load() != 0 {
		t.Errorf("hits = %d", s.hits.Load())
	}
}

func TestCheckDailyFailureRecordsAttempt(t *testing.T) {
	t.Parallel()

	s, path := seeded(t, 25*time.Hour)
	s.Close()

	failing := newServer(t, http.StatusInternalServerError, "")
	c := newChecker(failing, path)

	if _, err := c.CheckDaily(context.Background(), "v1.1.0"); err == nil {
		t.Fatal("expected error")
	}

	if got := readCache(t, path); !got.CheckedAt.Equal(fixedNow) || got.Latest != "v1.2.0" {
		t.Errorf("cache = %+v", got)
	}

	res, err := c.CheckDaily(context.Background(), "v1.1.0")
	if err != nil || !res.Available || failing.hits.Load() != 1 {
		t.Errorf("res=%+v err=%v hits=%d", res, err, failing.hits.Load())
	}
}

func TestCheckErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"not found", http.StatusNotFound, "", "no published release"},
		{"forbidden", http.StatusForbidden, "", "unexpected status"},
		{"bad json", http.StatusOK, "{", "decode response"},
		{"bad tag", http.StatusOK, `{"tag_name":"latest"}`, "invalid release tag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newServer(t, tc.status, tc.body)
			path := cachePath(t)

			_, err := newChecker(s, path).Check(context.Background(), "v1.1.0")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}

			if _, err := os.Stat(path); err == nil {
				t.Error("cache written")
			}
		})
	}
}

func TestCheckFallbackURL(t *testing.T) {
	t.Parallel()

	for _, link := range []string{"https://evil.example/x", "http://github.com/x"} {
		s := newServer(t, http.StatusOK, fmt.Sprintf(`{"tag_name":"v1.2.0","html_url":%q}`, link))

		res, err := newChecker(s, "").Check(context.Background(), "v1.1.0")
		if err != nil || res.URL != update.ReleasesURL {
			t.Errorf("%s: res=%+v err=%v", link, res, err)
		}
	}
}

func TestCheckTimeout(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	c := update.Checker{URL: srv.URL, Client: &http.Client{Timeout: 50 * time.Millisecond}}
	start := time.Now()

	if _, err := c.Check(context.Background(), "v1.1.0"); err == nil {
		t.Fatal("expected error")
	}

	if time.Since(start) > time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

func TestReport(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		res  update.Result
		want string
	}{
		{update.Result{Current: "v1.1.0", Latest: "v1.2.0", URL: "https://x", Comparable: true, Available: true},
			"current: v1.1.0\nlatest:  v1.2.0\nupdate available: https://x\n"},
		{update.Result{Current: "v1.2.0", Latest: "v1.2.0", Comparable: true},
			"current: v1.2.0\nlatest:  v1.2.0\nup to date\n"},
		{update.Result{Current: "dev", Latest: "v1.2.0"},
			"current: dev\nlatest:  v1.2.0\nnot a release build, versions not compared\n"},
	} {
		var buf bytes.Buffer

		update.Report(&buf, tc.res)

		if buf.String() != tc.want {
			t.Errorf("got %q, want %q", buf.String(), tc.want)
		}
	}
}

func TestCheckDailyCachedURLValidated(t *testing.T) {
	t.Parallel()

	s := newServer(t, http.StatusOK, releaseBody("v1.3.0"))
	path := cachePath(t)

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	writeCache(t, path, cacheFile{CheckedAt: fixedNow.Add(-time.Hour), Latest: "v1.2.0", URL: "https://evil.example/x"})

	res, err := newChecker(s, path).CheckDaily(context.Background(), "v1.1.0")
	if err != nil || res.URL != update.ReleasesURL || s.hits.Load() != 0 {
		t.Errorf("res=%+v err=%v hits=%d", res, err, s.hits.Load())
	}
}

func TestCheckDailyCachedInvalidLatest(t *testing.T) {
	t.Parallel()

	s := newServer(t, http.StatusOK, releaseBody("v1.3.0"))
	path := cachePath(t)

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	writeCache(t, path, cacheFile{CheckedAt: fixedNow.Add(-time.Hour), Latest: "garbage", URL: releaseURL + "v1.2.0"})

	res, err := newChecker(s, path).CheckDaily(context.Background(), "v1.1.0")
	if err != nil || res.Latest != "v1.3.0" || s.hits.Load() != 1 {
		t.Errorf("res=%+v err=%v hits=%d", res, err, s.hits.Load())
	}
}
