// Package ipc implements the daemon's local control protocol over a Unix socket.
//
// Protocol v1: one request per connection. The client sends one JSON line
// {"v":1,"op":"...","args":{...}}; the server answers with one JSON line
// {"v":1,"ok":true,"result":{...}} or {"v":1,"ok":false,"error":{...}} and
// closes the connection.
//
// The socket is world-accessible (mode 0666) so unprivileged users can read
// the status. That is acceptable only because every operation other than
// status is authorized per request against the peer credentials
// (SO_PEERCRED): the caller must be root or the configured admin uid.
package ipc

import "time"

// Status is the daemon state served to clients.
type Status struct {
	Version    string    `json:"version"`
	StartedAt  time.Time `json:"started_at"`
	ConfigPath string    `json:"config_path"`
	Devices    []Device  `json:"devices"`
	Mappings   []Mapping `json:"mappings"`
	// AdminUID is the configured admin uid, if any.
	AdminUID *uint32 `json:"admin_uid"`
	// CallerUID and CallerIsAdmin describe the requesting peer; the server fills them.
	CallerUID     uint32 `json:"caller_uid"`
	CallerIsAdmin bool   `json:"caller_is_admin"`
	// Learning reports a running learn session.
	Learning bool `json:"learning"`
}

// Device describes one matched input device.
type Device struct {
	Path        string    `json:"path"`
	Name        string    `json:"name"`
	Vendor      uint16    `json:"vendor"`
	Product     uint16    `json:"product"`
	Matcher     string    `json:"matcher"`
	Grabbed     bool      `json:"grabbed"`
	VirtualName string    `json:"virtual_name"`
	Error       string    `json:"error"`
	Since       time.Time `json:"since"`
}

// Mapping is a configured key mapping in display form.
type Mapping struct {
	From  string   `json:"from"`
	To    []string `json:"to"`
	Label string   `json:"label,omitempty"`
}

// SetMappingArgs are the arguments of the set_mapping operation. OldFrom,
// when set, names the mapping that is replaced.
type SetMappingArgs struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	OldFrom string   `json:"old_from,omitempty"`
	Label   string   `json:"label,omitempty"`
}
