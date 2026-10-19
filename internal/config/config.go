// Package config loads, validates and persists the TOML configuration.
package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/holoplot/go-evdev"

	"github.com/elias-knodel/skb/internal/keys"
)

// DefaultPath is the default location of the configuration file.
const DefaultPath = "/etc/skb/config.toml"

// maxOutputs bounds the key count of a single mapping.
const maxOutputs = 5

// maxAdminUID excludes the invalid uid (uint32)-1.
const maxAdminUID = math.MaxUint32 - 1

// header is written at the top of every saved file.
const header = "# Managed by skb. Comments are not preserved when skb saves this file.\n\n"

// ErrNotFound reports a mapping that does not exist.
var ErrNotFound = errors.New("mapping not found")

type rawConfig struct {
	AdminUID *int64       `toml:"admin_uid,omitempty"`
	Devices  []rawDevice  `toml:"devices"`
	Mappings []rawMapping `toml:"mappings,omitempty"`
}

type rawDevice struct {
	Name    string `toml:"name"`
	Vendor  int64  `toml:"vendor"`
	Product int64  `toml:"product"`
}

type rawMapping struct {
	From string   `toml:"from"`
	To   []string `toml:"to"`
}

// DeviceMatcher selects a device by USB vendor and product ID.
type DeviceMatcher struct {
	Name    string
	Vendor  uint16
	Product uint16
}

// Mapping replaces one source key with a modifier chord and a main key.
type Mapping struct {
	From      evdev.EvCode
	Modifiers []evdev.EvCode
	Key       evdev.EvCode
}

// Outputs returns the modifiers followed by the main key.
func (m Mapping) Outputs() []evdev.EvCode {
	out := make([]evdev.EvCode, 0, len(m.Modifiers)+1)
	out = append(out, m.Modifiers...)

	return append(out, m.Key)
}

// String renders the mapping as "FROM -> MOD+KEY".
func (m Mapping) String() string {
	names := make([]string, 0, len(m.Modifiers)+1)
	for _, code := range m.Outputs() {
		names = append(names, keys.Name(code))
	}

	return keys.Name(m.From) + " -> " + strings.Join(names, "+")
}

// Config is the configuration. Treat a Config as immutable once shared;
// derive changes through WithMapping and WithoutMapping.
type Config struct {
	// AdminUID is the non-root uid allowed to modify the configuration.
	AdminUID *uint32
	Devices  []DeviceMatcher
	Mappings []Mapping
}

// Default returns the built-in configuration: the Darkstar IDs, no mappings.
func Default() *Config {
	return &Config{Devices: []DeviceMatcher{
		{Name: "Darkstar wired", Vendor: 0x1b1c, Product: 0x1bb2},
		{Name: "Darkstar Slipstream receiver", Vendor: 0x1b1c, Product: 0x1bdc},
	}}
}

// Clone returns a deep copy.
func (c *Config) Clone() *Config {
	out := &Config{
		Devices:  slices.Clone(c.Devices),
		Mappings: make([]Mapping, len(c.Mappings)),
	}

	if c.AdminUID != nil {
		uid := *c.AdminUID
		out.AdminUID = &uid
	}

	for i, m := range c.Mappings {
		out.Mappings[i] = m
		out.Mappings[i].Modifiers = slices.Clone(m.Modifiers)
	}

	if len(out.Mappings) == 0 {
		out.Mappings = nil
	}

	return out
}

// SourceKeys returns the mapped source key codes in ascending order.
func (c *Config) SourceKeys() []evdev.EvCode {
	out := make([]evdev.EvCode, 0, len(c.Mappings))
	for _, m := range c.Mappings {
		out = append(out, m.From)
	}

	slices.Sort(out)

	return out
}

// Validate checks the structural rules of the configuration.
func (c *Config) Validate() error {
	if len(c.Devices) == 0 {
		return errors.New("config: no [[devices]] defined")
	}

	for i, d := range c.Devices {
		if d.Vendor == 0 || d.Product == 0 {
			return fmt.Errorf("devices[%d]: vendor and product must be non-zero", i)
		}
	}

	seen := make(map[evdev.EvCode]bool)

	for i, m := range c.Mappings {
		if err := validateMapping(m); err != nil {
			return fmt.Errorf("mappings[%d]: %w", i, err)
		}

		if seen[m.From] {
			return fmt.Errorf("mappings[%d]: duplicate from %q", i, keys.Name(m.From))
		}

		seen[m.From] = true
	}

	return nil
}

func validateMapping(m Mapping) error {
	if m.From == 0 {
		return errors.New("from must be set")
	}

	if keys.IsPrimaryPointerButton(m.From) {
		return fmt.Errorf("from: %q is a primary pointer button and cannot be remapped", keys.Name(m.From))
	}

	if m.Key == 0 {
		return errors.New("to must not be empty")
	}

	outputs := m.Outputs()
	if len(outputs) > maxOutputs {
		return fmt.Errorf("to has %d entries, at most %d allowed", len(outputs), maxOutputs)
	}

	for i, code := range outputs {
		if !keys.IsKeyboardKey(code) {
			return fmt.Errorf("to: %q is not a keyboard key", keys.Name(code))
		}

		if slices.Contains(outputs[:i], code) {
			return fmt.Errorf("to: duplicate key %q", keys.Name(code))
		}
	}

	for _, code := range m.Modifiers {
		if !keys.IsModifier(code) {
			return fmt.Errorf("to: %q is not a modifier but not the last entry", keys.Name(code))
		}
	}

	return nil
}

// WithMapping returns a copy with m inserted or replaced by source key. A
// non-zero oldFrom different from m.From first removes that mapping, keeping
// its position; m.From must then not exist elsewhere.
func (c *Config) WithMapping(m Mapping, oldFrom evdev.EvCode) (*Config, error) {
	out := c.Clone()
	idx := slices.IndexFunc(out.Mappings, func(x Mapping) bool { return x.From == m.From })

	switch {
	case oldFrom != 0 && oldFrom != m.From:
		oldIdx := slices.IndexFunc(out.Mappings, func(x Mapping) bool { return x.From == oldFrom })
		if oldIdx < 0 {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, keys.Name(oldFrom))
		}

		if idx >= 0 {
			return nil, fmt.Errorf("source key %s is already mapped", keys.Name(m.From))
		}

		out.Mappings[oldIdx] = m
	case idx >= 0:
		out.Mappings[idx] = m
	default:
		out.Mappings = append(out.Mappings, m)
	}

	if err := out.Validate(); err != nil {
		return nil, err
	}

	return out, nil
}

// WithoutMapping returns a copy without the mapping for from.
func (c *Config) WithoutMapping(from evdev.EvCode) (*Config, error) {
	out := c.Clone()

	idx := slices.IndexFunc(out.Mappings, func(x Mapping) bool { return x.From == from })
	if idx < 0 {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, keys.Name(from))
	}

	out.Mappings = slices.Delete(out.Mappings, idx, idx+1)
	if len(out.Mappings) == 0 {
		out.Mappings = nil
	}

	return out, nil
}

// Load reads and validates the configuration file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	return Parse(data)
}

// LoadOrDefault loads path, or returns Default and exists=false when the
// file does not exist. An invalid file is an error and is never replaced.
func LoadOrDefault(path string) (cfg *Config, exists bool, err error) {
	cfg, err = Load(path)

	switch {
	case err == nil:
		return cfg, true, nil
	case errors.Is(err, os.ErrNotExist):
		return Default(), false, nil
	default:
		return nil, false, err
	}
}

// Parse decodes and validates a TOML document.
func Parse(data []byte) (*Config, error) {
	var raw rawConfig

	meta, err := toml.Decode(string(data), &raw)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return nil, fmt.Errorf("unknown config field %q", undecoded[0].String())
	}

	cfg := &Config{}

	if raw.AdminUID != nil {
		if *raw.AdminUID < 0 || *raw.AdminUID > maxAdminUID {
			return nil, fmt.Errorf("admin_uid %d out of range 0..%d", *raw.AdminUID, int64(maxAdminUID))
		}

		uid := uint32(*raw.AdminUID)
		cfg.AdminUID = &uid
	}

	for i, d := range raw.Devices {
		matcher, err := convertDevice(d)
		if err != nil {
			return nil, fmt.Errorf("devices[%d]: %w", i, err)
		}

		cfg.Devices = append(cfg.Devices, matcher)
	}

	for i, m := range raw.Mappings {
		mapping, err := convertMapping(m)
		if err != nil {
			return nil, fmt.Errorf("mappings[%d]: %w", i, err)
		}

		cfg.Mappings = append(cfg.Mappings, mapping)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func convertDevice(d rawDevice) (DeviceMatcher, error) {
	vendor, err := toID("vendor", d.Vendor)
	if err != nil {
		return DeviceMatcher{}, err
	}

	product, err := toID("product", d.Product)
	if err != nil {
		return DeviceMatcher{}, err
	}

	return DeviceMatcher{Name: d.Name, Vendor: vendor, Product: product}, nil
}

func toID(field string, v int64) (uint16, error) {
	if v < 1 || v > math.MaxUint16 {
		return 0, fmt.Errorf("%s %#x out of range 1..0xffff", field, v)
	}

	return uint16(v), nil
}

// convertMapping resolves key names; structural rules are checked by Validate.
func convertMapping(m rawMapping) (Mapping, error) {
	from, err := keys.Parse(m.From)
	if err != nil {
		return Mapping{}, fmt.Errorf("from: %w", err)
	}

	codes := make([]evdev.EvCode, 0, len(m.To))

	for _, name := range m.To {
		code, err := keys.Parse(name)
		if err != nil {
			return Mapping{}, fmt.Errorf("to: %w", err)
		}

		codes = append(codes, code)
	}

	out := Mapping{From: from}
	if len(codes) == 0 {
		return out, nil
	}

	last := len(codes) - 1
	out.Key = codes[last]

	if last > 0 {
		out.Modifiers = codes[:last]
	}

	return out, nil
}

// Marshal renders the configuration as TOML with a header comment.
func Marshal(c *Config) ([]byte, error) {
	raw := rawConfig{}

	if c.AdminUID != nil {
		uid := int64(*c.AdminUID)
		raw.AdminUID = &uid
	}

	for _, d := range c.Devices {
		raw.Devices = append(raw.Devices, rawDevice{Name: d.Name, Vendor: int64(d.Vendor), Product: int64(d.Product)})
	}

	for _, m := range c.Mappings {
		rm := rawMapping{From: keys.Name(m.From)}
		for _, code := range m.Outputs() {
			rm.To = append(rm.To, keys.Name(code))
		}

		raw.Mappings = append(raw.Mappings, rm)
	}

	var sb strings.Builder

	sb.WriteString(header)

	if err := toml.NewEncoder(&sb).Encode(raw); err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}

	return []byte(sb.String()), nil
}

// Save writes the configuration atomically: temp file in the target
// directory, fsync, rename, directory fsync.
func Save(path string, c *Config) (err error) {
	data, err := Marshal(c)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".config.toml.*")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}

	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}

	// CreateTemp uses 0600; the file must be world-readable regardless of umask.
	if err = tmp.Chmod(0o644); err != nil {
		return fmt.Errorf("chmod temp config: %w", err)
	}

	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("sync temp config: %w", err)
	}

	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}

	if err = os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}

	d, err := os.Open(filepath.Clean(dir))
	if err != nil {
		return fmt.Errorf("open config dir: %w", err)
	}

	defer func() { _ = d.Close() }()

	if err = d.Sync(); err != nil {
		return fmt.Errorf("sync config dir: %w", err)
	}

	return nil
}
