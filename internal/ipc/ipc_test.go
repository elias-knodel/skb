package ipc_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elias-knodel/skb/internal/ipc"
)

type fakeHandler struct {
	admin *uint32

	mu          sync.Mutex
	learnCalls  int
	lastTimeout time.Duration
	lastSet     ipc.SetMappingArgs
	active      atomic.Int32

	// learnSource runs for learn_source; nil returns "KEY_2".
	learnSource func(ctx context.Context, d time.Duration) (string, error)
	err         error
}

func (h *fakeHandler) Status() ipc.Status {
	return ipc.Status{
		Version:   "test",
		StartedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Devices:   []ipc.Device{{Path: "/dev/input/event7", Name: "Keyboard", Vendor: 0x1b1c, Product: 0x1bb2, Grabbed: true}},
		Mappings:  []ipc.Mapping{{From: "KEY_4", To: []string{"KEY_LEFTSHIFT", "KEY_MINUS"}}},
	}
}

func (h *fakeHandler) AdminUID() *uint32 { return h.admin }

func (h *fakeHandler) LearnSource(ctx context.Context, d time.Duration) (string, error) {
	h.active.Add(1)
	defer h.active.Add(-1)

	h.mu.Lock()
	h.learnCalls++
	h.lastTimeout = d
	fn, err := h.learnSource, h.err
	h.mu.Unlock()

	if err != nil {
		return "", err
	}

	if fn != nil {
		return fn(ctx, d)
	}

	return "KEY_2", nil
}

func (h *fakeHandler) LearnTarget(context.Context, time.Duration) ([]string, error) {
	return []string{"KEY_LEFTSHIFT", "KEY_MINUS"}, h.err
}

func (h *fakeHandler) SetMapping(_ context.Context, a ipc.SetMappingArgs) ([]ipc.Mapping, error) {
	h.mu.Lock()
	h.lastSet = a
	h.mu.Unlock()

	return []ipc.Mapping{{From: a.From, To: a.To}}, h.err
}

func (h *fakeHandler) DeleteMapping(context.Context, string) ([]ipc.Mapping, error) {
	return nil, h.err
}

func ownUID() uint32 { return uint32(os.Getuid()) } //nolint:gosec // uid is non-negative

func serve(t *testing.T, h ipc.Handler, opts ...ipc.ServeOptions) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "s.sock")
	ctx, cancel := context.WithCancel(context.Background())

	ln, err := ipc.Listen(ctx, path)
	if err != nil {
		cancel()
		t.Fatal(err)
	}

	done := make(chan error, 1)

	go func() { done <- ipc.Serve(ctx, ln, h, opts...) }()

	t.Cleanup(func() {
		cancel()

		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve returned %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("Serve did not stop")
		}
	})

	return path
}

// rawRoundTrip sends line verbatim and decodes the single response.
func rawRoundTrip(t *testing.T, path, line string) (ok bool, code string) {
	t.Helper()

	conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = conn.Close() }()

	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	if _, err := conn.Write([]byte(line)); err != nil {
		t.Fatal(err)
	}

	var resp struct {
		OK    bool `json:"ok"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}

	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Error != nil {
		code = resp.Error.Code
	}

	return resp.OK, code
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()

	var e *ipc.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("err = %v, want code %q", err, code)
	}
}

func TestStatusRoundTrip(t *testing.T) {
	t.Parallel()

	h := &fakeHandler{admin: new(ownUID())}
	c := ipc.Client{Socket: serve(t, h)}

	st, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if st.CallerUID != ownUID() || !st.CallerIsAdmin || st.Version != "test" ||
		len(st.Devices) != 1 || st.Devices[0].Vendor != 0x1b1c || st.Mappings[0].To[1] != "KEY_MINUS" {
		t.Errorf("status = %+v", st)
	}

	if st.AdminUID == nil || *st.AdminUID != ownUID() {
		t.Errorf("admin = %v", st.AdminUID)
	}
}

func TestAdminOperations(t *testing.T) {
	t.Parallel()

	h := &fakeHandler{admin: new(ownUID())}
	c := ipc.Client{Socket: serve(t, h)}
	ctx := context.Background()

	key, err := c.LearnSource(ctx, 5*time.Second)
	if err != nil || key != "KEY_2" || h.lastTimeout != 5*time.Second {
		t.Fatalf("learn source: %v %v %v", key, err, h.lastTimeout)
	}

	to, err := c.LearnTarget(ctx, 0)
	if err != nil || len(to) != 2 {
		t.Fatalf("learn target: %v %v", to, err)
	}

	if _, err := c.SetMapping(ctx, ipc.SetMappingArgs{From: "KEY_1", To: []string{"KEY_6"}, OldFrom: "KEY_2"}); err != nil {
		t.Fatal(err)
	}

	if h.lastSet.OldFrom != "KEY_2" || h.lastSet.From != "KEY_1" {
		t.Errorf("args = %+v", h.lastSet)
	}

	if _, err := c.DeleteMapping(ctx, "KEY_1"); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultLearnTimeout(t *testing.T) {
	t.Parallel()

	h := &fakeHandler{admin: new(ownUID())}
	c := ipc.Client{Socket: serve(t, h)}

	if _, err := c.LearnSource(context.Background(), 0); err != nil {
		t.Fatal(err)
	}

	if h.lastTimeout != 10*time.Second {
		t.Errorf("timeout = %v", h.lastTimeout)
	}
}

func TestForbidden(t *testing.T) {
	t.Parallel()

	if os.Getuid() == 0 {
		t.Skip("root is always admin")
	}

	for name, admin := range map[string]*uint32{"other uid": new(ownUID() + 1), "nil": nil} {
		c := ipc.Client{Socket: serve(t, &fakeHandler{admin: admin})}

		_, err := c.LearnSource(context.Background(), time.Second)
		wantCode(t, err, ipc.CodeForbidden)

		_, err = c.SetMapping(context.Background(), ipc.SetMappingArgs{From: "KEY_1", To: []string{"KEY_6"}})
		wantCode(t, err, ipc.CodeForbidden)

		st, err := c.Status(context.Background())
		if err != nil || st.CallerIsAdmin {
			t.Errorf("%s: status %+v %v", name, st, err)
		}
	}
}

func TestProtocolErrors(t *testing.T) {
	t.Parallel()

	path := serve(t, &fakeHandler{admin: new(ownUID())})

	cases := map[string]struct{ line, code string }{
		"version":       {`{"v":1,"op":"status"}` + "\n", ipc.CodeUnsupportedVersion},
		"unknown op":    {`{"v":2,"op":"x"}` + "\n", ipc.CodeUnknownOp},
		"unknown field": {`{"v":2,"op":"status","extra":1}` + "\n", ipc.CodeBadRequest},
		"malformed":     {"not json\n", ipc.CodeBadRequest},
		"args field":    {`{"v":2,"op":"learn_source","args":{"bogus":1}}` + "\n", ipc.CodeBadRequest},
		"oversized":     {`{"v":2,"op":"status","pad":"` + strings.Repeat("a", 20*1024) + `"}` + "\n", ipc.CodeBadRequest},
		"timeout low":   {`{"v":2,"op":"learn_source","args":{"timeout_ms":500}}` + "\n", ipc.CodeBadRequest},
		"timeout high":  {`{"v":2,"op":"learn_source","args":{"timeout_ms":40000}}` + "\n", ipc.CodeBadRequest},
	}

	for name, tc := range cases {
		ok, code := rawRoundTrip(t, path, tc.line)
		if ok || code != tc.code {
			t.Errorf("%s: ok=%v code=%q, want %q", name, ok, code, tc.code)
		}
	}
}

func TestSilentClientIsDropped(t *testing.T) {
	t.Parallel()

	path := serve(t, &fakeHandler{}, ipc.ServeOptions{ReadTimeout: 100 * time.Millisecond})

	conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = conn.Close() }()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	start := time.Now()
	if _, err := conn.Read(make([]byte, 1)); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("expected close by server, got %v", err)
	}

	if time.Since(start) > time.Second {
		t.Error("connection closed too late")
	}
}

func TestClientCancelCancelsHandler(t *testing.T) {
	t.Parallel()

	cancelled := make(chan struct{})
	started := make(chan struct{})
	h := &fakeHandler{
		admin: new(ownUID()),
		learnSource: func(ctx context.Context, _ time.Duration) (string, error) {
			close(started)
			<-ctx.Done()
			close(cancelled)

			return "", ctx.Err()
		},
	}
	c := ipc.Client{Socket: serve(t, h)}

	ctx, cancel := context.WithCancel(context.Background())

	errc := make(chan error, 1)

	go func() {
		_, err := c.LearnSource(ctx, 10*time.Second)
		errc <- err
	}()

	<-started
	cancel()

	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("handler context not cancelled")
	}

	if err := <-errc; err == nil {
		t.Error("client returned no error")
	}
}

func TestHandlerErrors(t *testing.T) {
	t.Parallel()

	busy := &fakeHandler{admin: new(ownUID()), err: &ipc.Error{Code: ipc.CodeBusy, Message: "learn running"}}

	_, err := ipc.Client{Socket: serve(t, busy)}.LearnSource(context.Background(), time.Second)
	wantCode(t, err, ipc.CodeBusy)

	plain := &fakeHandler{admin: new(ownUID()), err: errors.New("secret /etc/path detail")}

	_, err = ipc.Client{Socket: serve(t, plain)}.LearnSource(context.Background(), time.Second)
	wantCode(t, err, ipc.CodeInternal)

	if strings.Contains(err.Error(), "secret") {
		t.Errorf("internal error leaks detail: %v", err)
	}
}

func TestConnectionLimit(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	h := &fakeHandler{
		admin: new(ownUID()),
		learnSource: func(ctx context.Context, _ time.Duration) (string, error) {
			select {
			case <-release:
			case <-ctx.Done():
			}

			return "KEY_2", nil
		},
	}
	c := ipc.Client{Socket: serve(t, h)}

	var wg sync.WaitGroup

	for range 16 {
		wg.Go(func() { _, _ = c.LearnSource(context.Background(), 10*time.Second) })
	}

	deadline := time.Now().Add(2 * time.Second)
	for h.active.Load() < 16 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}

	_, err := c.Status(context.Background())
	wantCode(t, err, ipc.CodeBusy)

	close(release)
	wg.Wait()
}

func TestJSONKeys(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(ipc.Status{Devices: []ipc.Device{{}}, Mappings: []ipc.Mapping{{}}})
	if err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{
		`"started_at"`, `"config_path"`, `"vendor"`, `"virtual_name"`,
		`"caller_uid"`, `"caller_is_admin"`, `"admin_uid"`, `"learning"`,
	} {
		if !strings.Contains(string(data), key) {
			t.Errorf("missing %s in %s", key, data)
		}
	}
}

func TestListenReplacesStaleSocket(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "s.sock")

	first, err := ipc.Listen(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	// Closing a unix listener unlinks the file; keep it to simulate a crash leftover.
	ul, ok := first.(*net.UnixListener)
	if !ok {
		t.Fatal("not a unix listener")
	}

	ul.SetUnlinkOnClose(false)
	_ = first.Close()

	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("stale socket missing: %v", err)
	}

	second, err := ipc.Listen(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	_ = second.Close()
}

func TestListenMode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "s.sock")

	ln, err := ipc.Listen(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = ln.Close() }()

	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if st.Mode().Perm() != 0o666 {
		t.Errorf("mode = %v", st.Mode().Perm())
	}
}

func TestListenRefusesRegularFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ipc.Listen(context.Background(), path); err == nil {
		t.Fatal("expected error")
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("file was removed: %v", err)
	}
}

func TestFetchMissingSocket(t *testing.T) {
	t.Parallel()

	_, err := ipc.Client{Socket: filepath.Join(t.TempDir(), "none")}.Status(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

// flakyListener fails Accept with a temporary error a fixed number of times.
type flakyListener struct {
	net.Listener
	fails atomic.Int32
}

type tempError struct{}

func (tempError) Error() string   { return "temporary accept failure" }
func (tempError) Timeout() bool   { return true }
func (tempError) Temporary() bool { return true }

func (l *flakyListener) Accept() (net.Conn, error) {
	if l.fails.Add(-1) >= 0 {
		return nil, tempError{}
	}

	return l.Listener.Accept()
}

func TestServeSurvivesTemporaryAcceptErrors(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "s.sock")
	ctx, cancel := context.WithCancel(context.Background())

	ln, err := ipc.Listen(ctx, path)
	if err != nil {
		cancel()
		t.Fatal(err)
	}

	flaky := &flakyListener{Listener: ln}
	flaky.fails.Store(4)

	done := make(chan error, 1)

	go func() { done <- ipc.Serve(ctx, flaky, &fakeHandler{}) }()

	t.Cleanup(func() {
		cancel()

		if err := <-done; err != nil {
			t.Errorf("Serve returned %v", err)
		}
	})

	if _, err := (ipc.Client{Socket: path}).Status(ctx); err != nil {
		t.Fatalf("status after accept errors: %v", err)
	}
}

func TestOverLimitConnectionsAreRejectedPromptly(t *testing.T) {
	t.Parallel()

	path := serve(t, &fakeHandler{}, ipc.ServeOptions{MaxConns: 2, ReadTimeout: 5 * time.Second})

	var held []net.Conn

	defer func() {
		for _, c := range held {
			_ = c.Close()
		}
	}()

	dial := func() net.Conn {
		conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", path)
		if err != nil {
			t.Fatal(err)
		}

		held = append(held, conn)

		return conn
	}

	// Occupy the two slots with silent clients.
	dial()
	dial()
	time.Sleep(50 * time.Millisecond)

	start := time.Now()

	for range 100 {
		conn := dial()
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))

		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil || !strings.Contains(line, ipc.CodeBusy) {
			t.Fatalf("over-limit conn: %q %v", line, err)
		}
	}

	if time.Since(start) > 3*time.Second {
		t.Error("rejection too slow")
	}
}
