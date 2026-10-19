package ipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// DefaultSocket is the default socket path.
const DefaultSocket = "/run/skb/status.sock"

const (
	socketMode     = 0o666
	maxRequestSize = 16 * 1024
	maxDrain       = 1 << 20
	drainTimeout   = 500 * time.Millisecond
	defaultTimeout = 2 * time.Second
	defaultMaxConn = 16
	rejectTimeout  = 100 * time.Millisecond

	minAcceptDelay = 5 * time.Millisecond
	maxAcceptDelay = time.Second
)

// ServeOptions tune the server; zero values select the defaults.
type ServeOptions struct {
	// ReadTimeout bounds the wait for the request line.
	ReadTimeout time.Duration
	// WriteTimeout bounds writing the response.
	WriteTimeout time.Duration
	// MaxConns bounds concurrent connections.
	MaxConns int
}

// Listen creates the socket at path. A stale socket file is replaced; any
// other file type at path is left untouched and reported as an error.
func Listen(ctx context.Context, path string) (net.Listener, error) {
	info, err := os.Lstat(path)

	switch {
	case err == nil:
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}

		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale socket: %w", err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("stat socket path: %w", err)
	}

	var lc net.ListenConfig

	ln, err := lc.Listen(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	// World-accessible by design; operations are authorized per peer uid.
	if err := os.Chmod(path, socketMode); err != nil {
		_ = ln.Close()

		return nil, fmt.Errorf("chmod socket: %w", err)
	}

	return ln, nil
}

type server struct {
	h    Handler
	opts ServeOptions
	sem  chan struct{}
	wg   sync.WaitGroup
}

// Serve answers connections until ctx is cancelled. It closes ln and
// returns nil on cancellation.
func Serve(ctx context.Context, ln net.Listener, h Handler, opts ...ServeOptions) error {
	var o ServeOptions
	if len(opts) > 0 {
		o = opts[0]
	}

	if o.ReadTimeout <= 0 {
		o.ReadTimeout = defaultTimeout
	}

	if o.WriteTimeout <= 0 {
		o.WriteTimeout = defaultTimeout
	}

	if o.MaxConns <= 0 {
		o.MaxConns = defaultMaxConn
	}

	s := &server{h: h, opts: o, sem: make(chan struct{}, o.MaxConns)}

	go func() {
		<-ctx.Done()

		_ = ln.Close()
	}()

	defer s.wg.Wait()

	delay := minAcceptDelay

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}

			if !retryableAccept(err) {
				return fmt.Errorf("accept: %w", err)
			}

			if !sleepCtx(ctx, delay) {
				return nil
			}

			delay = min(delay*2, maxAcceptDelay)

			continue
		}

		delay = minAcceptDelay

		select {
		case s.sem <- struct{}{}:
			s.wg.Go(func() {
				defer func() { <-s.sem }()

				s.handle(ctx, conn)
			})
		default:
			s.rejectBusy(conn)
		}
	}
}

// rejectBusy answers an over-limit connection synchronously and closes it. It
// reads nothing and spawns no goroutine, so a flood cannot grow the process.
func (s *server) rejectBusy(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	_ = conn.SetWriteDeadline(time.Now().Add(rejectTimeout))

	data, _ := json.Marshal(errResponse(&Error{Code: CodeBusy, Message: "too many connections"}))
	_, _ = conn.Write(append(data, '\n'))
}

// retryableAccept reports transient accept failures such as descriptor exhaustion.
func retryableAccept(err error) bool {
	if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
		return true
	}

	return errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) || errors.Is(err, syscall.ECONNABORTED)
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (s *server) handle(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()

	uid, err := peerUID(conn)
	if err != nil {
		s.finish(conn, errResponse(&Error{Code: CodeInternal, Message: "peer credentials unavailable"}), false)

		return
	}

	_ = conn.SetReadDeadline(time.Now().Add(s.opts.ReadTimeout))

	line, oversized, err := readLine(conn)
	if err != nil {
		// Silent or stalled client: drop without a response.
		return
	}

	_ = conn.SetReadDeadline(time.Time{})

	if oversized {
		s.finish(conn, errResponse(&Error{Code: CodeBadRequest, Message: "request too large"}), true)

		return
	}

	s.finish(conn, s.dispatch(ctx, conn, uid, line), false)
}

// readLine reads one request line of at most maxRequestSize bytes.
func readLine(conn net.Conn) (line []byte, oversized bool, err error) {
	br := bufio.NewReader(io.LimitReader(conn, maxRequestSize+1))

	line, err = br.ReadBytes('\n')
	if len(line) > maxRequestSize {
		return nil, true, nil
	}

	if err != nil && (len(line) == 0 || !errors.Is(err, io.EOF)) {
		return nil, false, err
	}

	return line, false, nil
}

// finish writes the response, then half-closes and optionally drains unread
// request bytes so the peer does not lose the response to a reset.
func (s *server) finish(conn net.Conn, resp response, drain bool) {
	_ = conn.SetWriteDeadline(time.Now().Add(s.opts.WriteTimeout))

	data, err := json.Marshal(resp)
	if err != nil {
		data = []byte(`{"v":1,"ok":false,"error":{"code":"internal","message":"internal error"}}`)
	}

	if _, err := conn.Write(append(data, '\n')); err != nil {
		return
	}

	if !drain {
		return
	}

	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}

	_ = conn.SetReadDeadline(time.Now().Add(drainTimeout))
	_, _ = io.Copy(io.Discard, io.LimitReader(conn, maxDrain))
}

func errResponse(e *Error) response {
	return response{V: Version, Error: e}
}

func okResponse(result any) response {
	return response{V: Version, OK: true, Result: result}
}

// peerUID returns the uid of the connecting process (SO_PEERCRED).
func peerUID(conn net.Conn) (uint32, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, errors.New("not a unix connection")
	}

	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("syscall conn: %w", err)
	}

	var (
		cred    *unix.Ucred
		credErr error
	)

	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, fmt.Errorf("control: %w", err)
	}

	if credErr != nil {
		return 0, fmt.Errorf("SO_PEERCRED: %w", credErr)
	}

	return cred.Uid, nil
}

func isAdmin(uid uint32, admin *uint32) bool {
	return uid == 0 || (admin != nil && uid == *admin)
}

// strictDecode decodes JSON rejecting unknown fields and trailing data.
func strictDecode(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(v); err != nil {
		return err //nolint:wrapcheck // reported as bad_request
	}

	if dec.More() {
		return errors.New("trailing data")
	}

	return nil
}

func badRequest(msg string) response {
	return errResponse(&Error{Code: CodeBadRequest, Message: msg})
}

func (s *server) dispatch(ctx context.Context, conn net.Conn, uid uint32, line []byte) response {
	var req request
	if err := strictDecode(line, &req); err != nil {
		return badRequest("malformed request")
	}

	if req.V != Version {
		return errResponse(&Error{Code: CodeUnsupportedVersion, Message: fmt.Sprintf("protocol version %d is not supported", req.V)})
	}

	switch req.Op {
	case OpStatus:
		st := s.h.Status()
		st.AdminUID = s.h.AdminUID()
		st.CallerUID = uid
		st.CallerIsAdmin = isAdmin(uid, st.AdminUID)

		return okResponse(st)
	case OpLearnSource, OpLearnTarget, OpSetMapping, OpDeleteMapping:
	default:
		return errResponse(&Error{Code: CodeUnknownOp, Message: "unknown operation"})
	}

	if !isAdmin(uid, s.h.AdminUID()) {
		return errResponse(&Error{Code: CodeForbidden, Message: fmt.Sprintf("uid %d is not the admin uid", uid)})
	}

	return s.dispatchAdmin(ctx, conn, req)
}

func (s *server) dispatchAdmin(ctx context.Context, conn net.Conn, req request) response {
	switch req.Op {
	case OpLearnSource, OpLearnTarget:
		timeout, resp, ok := learnTimeout(req.Args)
		if !ok {
			return resp
		}

		// The client cancels a learn session by closing the connection.
		opCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		go func() {
			var b [1]byte

			_, _ = conn.Read(b[:])

			cancel()
		}()

		if req.Op == OpLearnSource {
			key, err := s.h.LearnSource(opCtx, timeout)

			return result(keyResult{Key: key}, err)
		}

		to, err := s.h.LearnTarget(opCtx, timeout)

		return result(targetResult{To: to}, err)
	case OpSetMapping:
		var args SetMappingArgs
		if err := strictDecode(req.Args, &args); err != nil {
			return badRequest("malformed arguments")
		}

		m, err := s.h.SetMapping(ctx, args)

		return result(mappingsResult{Mappings: m}, err)
	default: // OpDeleteMapping
		var args deleteArgs
		if err := strictDecode(req.Args, &args); err != nil {
			return badRequest("malformed arguments")
		}

		m, err := s.h.DeleteMapping(ctx, args.From)

		return result(mappingsResult{Mappings: m}, err)
	}
}

// learnTimeout parses and range-checks the learn arguments.
func learnTimeout(raw json.RawMessage) (time.Duration, response, bool) {
	var args learnArgs

	if len(raw) > 0 {
		if err := strictDecode(raw, &args); err != nil {
			return 0, badRequest("malformed arguments"), false
		}
	}

	if args.TimeoutMS == nil {
		return DefaultLearnTimeout, response{}, true
	}

	d := time.Duration(*args.TimeoutMS) * time.Millisecond
	if d < minLearnTimeout || d > maxLearnTimeout {
		return 0, badRequest("timeout_ms must be between 1000 and 30000"), false
	}

	return d, response{}, true
}

// result converts a handler outcome; unexpected errors never leak details.
func result(v any, err error) response {
	if err == nil {
		return okResponse(v)
	}

	if pe, ok := errors.AsType[*Error](err); ok {
		return errResponse(pe)
	}

	return errResponse(&Error{Code: CodeInternal, Message: "internal error"})
}
