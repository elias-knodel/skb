// Package deploy embeds the files that are installed on the host.
package deploy

import _ "embed" // required for go:embed

// Unit is the systemd service unit.
//
//go:embed systemd/skb.service
var Unit []byte

// Desktop is the freedesktop application entry.
//
//go:embed applications/skb.desktop
var Desktop []byte
