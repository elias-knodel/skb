package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// Client talks to the daemon socket. Each call uses its own connection.
type Client struct {
	Socket string
}

// call performs one request. Cancelling ctx closes the connection, which
// also cancels a running learn operation in the daemon.
func (c Client) call(ctx context.Context, op string, args, result any) error {
	var dialer net.Dialer

	conn, err := dialer.DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	defer func() { _ = conn.Close() }()

	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	req := request{V: Version, Op: op}

	if args != nil {
		raw, err := json.Marshal(args)
		if err != nil {
			return fmt.Errorf("encode arguments: %w", err)
		}

		req.Args = raw
	}

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	// A rejecting server may close before the request is written; its response
	// is still readable, so a write failure is only reported when nothing arrives.
	_, writeErr := conn.Write(append(data, '\n'))

	var resp struct {
		V      int             `json:"v"`
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
	}

	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		if writeErr != nil {
			err = writeErr
		}

		return wrapCtx(ctx, fmt.Errorf("exchange with daemon: %w", err))
	}

	if !resp.OK {
		if resp.Error == nil {
			return errors.New("malformed error response")
		}

		return resp.Error
	}

	if result == nil {
		return nil
	}

	if err := json.Unmarshal(resp.Result, result); err != nil {
		return fmt.Errorf("decode result: %w", err)
	}

	return nil
}

func wrapCtx(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	return err
}

func timeoutArgs(d time.Duration) learnArgs {
	if d <= 0 {
		return learnArgs{}
	}

	ms := d.Milliseconds()

	return learnArgs{TimeoutMS: &ms}
}

// Status fetches the daemon status.
func (c Client) Status(ctx context.Context) (Status, error) {
	var st Status

	err := c.call(ctx, OpStatus, nil, &st)

	return st, err
}

// LearnSource waits for a source key press; a zero timeout selects the default.
func (c Client) LearnSource(ctx context.Context, timeout time.Duration) (string, error) {
	var res keyResult

	err := c.call(ctx, OpLearnSource, timeoutArgs(timeout), &res)

	return res.Key, err
}

// LearnTarget waits for a target key or combination; a zero timeout selects the default.
func (c Client) LearnTarget(ctx context.Context, timeout time.Duration) ([]string, error) {
	var res targetResult

	err := c.call(ctx, OpLearnTarget, timeoutArgs(timeout), &res)

	return res.To, err
}

// SetMapping inserts or replaces a mapping.
func (c Client) SetMapping(ctx context.Context, args SetMappingArgs) ([]Mapping, error) {
	var res mappingsResult

	err := c.call(ctx, OpSetMapping, args, &res)

	return res.Mappings, err
}

// DeleteMapping removes the mapping for from.
func (c Client) DeleteMapping(ctx context.Context, from string) ([]Mapping, error) {
	var res mappingsResult

	err := c.call(ctx, OpDeleteMapping, deleteArgs{From: from}, &res)

	return res.Mappings, err
}
