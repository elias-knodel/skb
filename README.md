# skb

Daemon and TUI for the Corsair Darkstar. The daemon grabs (EVIOCGRAB) the mouse's keyboard interfaces, remaps the side buttons and forwards all other events to a virtual uinput device. Hotplug and switching between wired and wireless are detected via inotify. Mappings are learned, edited and deleted in the TUI; the daemon applies changes without a restart.

Supported devices (USB ID):

- Darkstar wired: `1b1c:1bb2`
- Darkstar via Slipstream receiver: `1b1c:1bdc`

Other Corsair devices (e.g. the K100, `1b1c:1b7c`) are left untouched.

The `1b1c:1bdc` entry covers the entire Slipstream receiver. Other Corsair devices paired with the same dongle would be grabbed as well (pure passthrough, no loss of functionality); restrict this in the configuration if needed.

See [ROADMAP.md](ROADMAP.md) for planned work.

## Requirements

- [Task](https://taskfile.dev)
- Docker Compose
- for installing from the TUI: polkit (`pkexec`) with an authentication agent (Plasma ships one)

Go runs exclusively in a container; Go is not required on the host.

## Commands

```sh
task qa          # build, tidy, lint, tests (-race), govulncheck
task build       # static binary at ./bin/skb
task install     # sudo ./bin/skb install --admin-uid <uid>: install binary, unit and config
task uninstall   # remove binary and unit, /etc/skb is kept
```

## TUI workflow

`./bin/skb tui` (or `skb tui` after installation):

1. **Setup** (`s`): shows whether the unit is installed and the service is active. `i` installs and starts the service via `pkexec`, `x` additionally disables input-remapper, `u` uninstalls. If the daemon is unreachable and the unit is not installed, the TUI starts directly in setup.
2. **Add** (`a`): step 1/3 press the side button on the mouse, step 2/3 press the target key or combination on the keyboard, step 3/3 save with `enter` (`t` re-learns the target, `esc` discards).
3. **Edit** (`e`): skips step 1 and re-learns the target of the selected mapping.
4. **Delete** (`d`, then `y`).

`esc` cancels every learning phase and therefore cannot be learned as a target key. While learning the target, all keyboards that are not grabbed are read without grabbing them; a keyboard grabbed by another program (e.g. input-remapper) cannot be learned. The timeout per learning step is 10 s; the remaining-time display refreshes with the status poll.

**Note on installing from the TUI:** Setup runs `pkexec` on the path of the binary the TUI was started from and copies exactly that binary to `/usr/local/bin/skb`. For maximum security, perform the initial installation with `task install` (sudo) and then start the TUI as `/usr/local/bin/skb tui`.

## Configuration and permissions

`/etc/skb/config.toml` is managed by the daemon and rewritten atomically on save; hand-written comments are lost in the process. `task install` does not overwrite an existing file, but adds `admin_uid` if none is set. If the file is missing, the daemon starts with built-in defaults and creates it on the first save. Template: `configs/config.toml`.

```toml
admin_uid = 1000

[[devices]]
name = "Darkstar wired"
vendor = 0x1b1c
product = 0x1bb2

[[devices]]
name = "Darkstar Slipstream receiver"
vendor = 0x1b1c
product = 0x1bdc

[[mappings]]
from = "KEY_4"
to = ["KEY_LEFTSHIFT", "KEY_MINUS"]
```

In `to`, all entries except the last are modifiers; the last one is the main key (keyboard keycodes only, no `BTN_*`). The output consists of evdev keycodes and is therefore layout-independent: `KEY_MINUS` is the key to the right of 0, which is ß on the German layout; Shift+ß produces "?" there.

The daemon only permits modifying and learning operations for processes with `uid == admin_uid` or root (checked via `SO_PEERCRED` on every request). The status socket `/run/skb/status.sock` has mode 0666 so that every local user can read the status; this is only acceptable because of that check. If the TUI's uid is not `admin_uid`, actions are locked and the user is referred to setup.

Validate: `skb check-config --config configs/config.toml`

## Grab rule

Every keyboard-capable interface (with at least one keyboard key) of the configured IDs is grabbed, regardless of the mappings; without mappings, events are passed through unchanged. Pure pointer interfaces (only `BTN_*`) are left untouched. The virtual devices announce all keyboard keycodes in advance so that new mappings take effect without recreating the device.

## input-remapper

input-remapper must be disabled for the Darkstar, otherwise grabbing fails with EBUSY (visible in the TUI and the journal). Choose `x` in setup, stop the injection in input-remapper-gtk and disable autoload for both Corsair entries, or run `sudo systemctl disable --now input-remapper`.

## Status

- `skb tui` shows devices (wired/wireless, status, errors) and mappings.
- `journalctl -u skb` or `task logs`. Pressed keys are never logged.

## Limitations

- Each grabbed interface gets its own virtual device.
- LED and repeat events (EV_LED, EV_REP) are not forwarded.
- The virtual device cannot set ABS ranges; the offered `ABS_VOLUME` code gets the range 0..0.
- A side button already held down when learning starts is not captured; press it again.
- A TUI of a newer version running against an older daemon reports "daemon outdated" and refers to setup.
