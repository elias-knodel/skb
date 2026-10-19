package ipc

import (
	"context"
	"encoding/json"
	"time"
)

// Version is the protocol version.
const Version = 1

// Operation names.
const (
	OpStatus        = "status"
	OpLearnSource   = "learn_source"
	OpLearnTarget   = "learn_target"
	OpSetMapping    = "set_mapping"
	OpDeleteMapping = "delete_mapping"
)

// Error codes.
const (
	CodeForbidden          = "forbidden"
	CodeBusy               = "busy"
	CodeTimeout            = "timeout"
	CodeCancelled          = "cancelled"
	CodeNoDevice           = "no_device"
	CodeInvalid            = "invalid"
	CodeNotFound           = "not_found"
	CodeInternal           = "internal"
	CodeBadRequest         = "bad_request"
	CodeUnsupportedVersion = "unsupported_version"
	CodeUnknownOp          = "unknown_op"
)

// Learn timeout bounds.
const (
	DefaultLearnTimeout = 10 * time.Second
	minLearnTimeout     = time.Second
	maxLearnTimeout     = 30 * time.Second
)

// Error is a protocol-level error.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error implements error.
func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Handler implements the operations behind the protocol.
type Handler interface {
	// Status returns the daemon state; the server adds the caller fields.
	Status() Status
	// AdminUID returns the configured admin uid, if any.
	AdminUID() *uint32
	// LearnSource waits for the user to press a source key.
	LearnSource(ctx context.Context, timeout time.Duration) (string, error)
	// LearnTarget waits for the user to press the target key or combination.
	LearnTarget(ctx context.Context, timeout time.Duration) ([]string, error)
	// SetMapping inserts or replaces a mapping and returns all mappings.
	SetMapping(ctx context.Context, args SetMappingArgs) ([]Mapping, error)
	// DeleteMapping removes a mapping and returns the remaining mappings.
	DeleteMapping(ctx context.Context, from string) ([]Mapping, error)
}

type request struct {
	V    int             `json:"v"`
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args,omitempty"`
}

type response struct {
	V      int    `json:"v"`
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  *Error `json:"error,omitempty"`
}

type learnArgs struct {
	TimeoutMS *int64 `json:"timeout_ms"`
}

type deleteArgs struct {
	From string `json:"from"`
}

type keyResult struct {
	Key string `json:"key"`
}

type targetResult struct {
	To []string `json:"to"`
}

type mappingsResult struct {
	Mappings []Mapping `json:"mappings"`
}
