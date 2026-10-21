# skb

> [!NOTE]
> Most of the code in this repository was written by AI. Code quality is assured through test-driven development and human review.

skb (simple key binds) is a daemon and TUI for the Corsair Darkstar. The daemon grabs (EVIOCGRAB) the mouse's keyboard interfaces, remaps the side buttons and forwards all other events to a virtual uinput device. Hotplug and switching between wired and wireless are detected via inotify. Mappings are learned, edited and deleted in the TUI; the daemon applies changes without a restart.

## Features

| Feature | Description |
| --- | --- |
| Side button remapping | Map Darkstar side buttons to any keyboard key or key combination |
| Interactive learning | Learn source button and target key directly in the TUI, no keycode lookup needed |
| Typed labels | Mappings show the character actually typed on the active layout (e.g. `ß`) |
| Live reload | The daemon applies mapping changes without a restart |
| Hotplug | Wired and wireless (Slipstream) connections are detected automatically via inotify |
| Passthrough | All non-mapped events are forwarded unchanged to a virtual uinput device |
| Full-screen TUI | Alt-screen interface with device cards, mapping table, progress bar and help |
| Guided setup | Install, start and uninstall the service from the TUI via `pkexec`, optionally disabling input-remapper |
| Access control | Changes are restricted to `admin_uid` or root, checked via `SO_PEERCRED` |
| Privacy | Pressed keys are never logged |

## Supported devices

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
task dist        # release binary and SHA256SUMS at ./dist (amd64)
task install     # sudo ./bin/skb install --admin-uid <uid>: install binary, unit, config and desktop entry
task uninstall   # remove binary, unit and desktop entry, /etc/skb is kept
```

## Releases

Publishing a GitHub release with a `vX.Y.Z` tag runs `task qa`, builds `skb-linux-amd64` with the tag as version, and attaches it together with `SHA256SUMS` and a build provenance attestation. To install a release:

```sh
sha256sum -c SHA256SUMS
gh attestation verify skb-linux-amd64 --repo elias-knodel/skb
chmod +x skb-linux-amd64
sudo ./skb-linux-amd64 install --admin-uid "$(id -u)"
```

## Updates

When the TUI starts, it asks the GitHub API (`api.github.com/repos/elias-knodel/skb/releases/latest`) at most once per day whether a newer release exists and caches the answer in `$XDG_CACHE_HOME/skb/update.json` (default `~/.cache/skb/update.json`). A newer version is shown in the TUI header with the release URL; nothing is downloaded or installed. Network failures are silent. Development builds (`dev`, `git describe` output with a commit suffix or `-dirty`) skip the check. `skb check-update` queries GitHub immediately, ignoring and refreshing the cache, prints the current and latest version, and exits with 1 if the request fails. Pre-releases are never offered; a running pre-release is notified when the corresponding stable release exists.

## TUI workflow

`./bin/skb` (or `skb` after installation; TUI flags such as `--interval 1s` can be passed directly):

1. **Setup** (`s`): shows whether the unit is installed and the service is active. `i` installs and starts the service via `pkexec`, `x` additionally disables input-remapper, `u` uninstalls. If the daemon is unreachable and the unit is not installed, the TUI starts directly in setup.
2. **Add** (`a`): step 1/3 press the side button on the mouse, step 2/3 press the target key or combination on the keyboard, step 3/3 save with `enter` (`t` re-learns the target, `esc` discards).
3. **Edit** (`e`): skips step 1 and re-learns the target of the selected mapping.
4. **Delete** (`d`, then `y`).

`esc` cancels every learning phase and therefore cannot be learned as a target key. While learning the target, all keyboards that are not grabbed are read without grabbing them; a keyboard grabbed by another program (e.g. input-remapper) cannot be learned. The timeout per learning step is 10 s; the remaining-time display refreshes with the status poll.

After installation, skb appears in the application launcher (search "skb" or "mouse", category System Settings / Hardware) and opens in the default terminal; the entry is `/usr/local/share/applications/skb.desktop`.

**Note on installing from the TUI:** Setup runs `pkexec` on the path of the binary the TUI was started from and copies exactly that binary to `/usr/local/bin/skb`. For maximum security, perform the initial installation with `task install` (sudo) and then start the TUI as `/usr/local/bin/skb`.

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

In `to`, all entries except the last are modifiers; the last one is the main key (keyboard keycodes only, no `BTN_*`). The output consists of evdev keycodes and is therefore layout-independent: `KEY_MINUS` is the physical position to the right of 0 on a US layout, which is ß on the German layout; Shift+ß produces "?" there.

The optional `label` field (at most 16 printable characters) is display-only and never affects remapping. The TUI sets it automatically while learning a target: it stores the character the active layout produced for the pressed key (for example `ß` or `?`) and shows it instead of the evdev name. Without a label, the TUI falls back to names derived from the evdev codes.

```toml
[[mappings]]
from = "KEY_4"
to = ["KEY_LEFTSHIFT", "KEY_MINUS"]
label = "?"
```

The daemon only permits modifying and learning operations for processes with `uid == admin_uid` or root (checked via `SO_PEERCRED` on every request). The status socket `/run/skb/status.sock` has mode 0666 so that every local user can read the status; this is only acceptable because of that check. If the TUI's uid is not `admin_uid`, actions are locked and the user is referred to setup.

Validate: `skb check-config --config configs/config.toml`

## Grab rule

Every keyboard-capable interface (with at least one keyboard key) of the configured IDs is grabbed, regardless of the mappings; without mappings, events are passed through unchanged. Pure pointer interfaces (only `BTN_*`) are left untouched. The virtual devices announce all keyboard keycodes in advance so that new mappings take effect without recreating the device.

## input-remapper

input-remapper must be disabled for the Darkstar, otherwise grabbing fails with EBUSY (visible in the TUI and the journal). Choose `x` in setup, stop the injection in input-remapper-gtk and disable autoload for both Corsair entries, or run `sudo systemctl disable --now input-remapper`.

## Status

- `skb` (without a command) shows devices (wired/wireless, status, errors) and mappings.
- `journalctl -u skb` or `task logs`. Pressed keys are never logged.

## Limitations

- Each grabbed interface gets its own virtual device.
- LED and repeat events (EV_LED, EV_REP) are not forwarded.
- The virtual device cannot set ABS ranges; the offered `ABS_VOLUME` code gets the range 0..0.
- A side button already held down when learning starts is not captured; press it again.
- A TUI of a newer version running against an older daemon reports "daemon outdated" and refers to setup.

## Contributing

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org) (`commitlint.config.mjs`). Branch names start with a type and a slash, e.g. `feat/…`, `fix/…`, `docs/…` or `ci/…`. Pull requests check both and run `task qa`; `govulncheck` additionally runs weekly.
