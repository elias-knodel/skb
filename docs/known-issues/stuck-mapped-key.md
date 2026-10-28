# Stuck mapped key (unconfirmed)

Status: unconfirmed, observed once on 2026-10-26.

## Symptom

While playing Guild Wars 2 (Proton, KDE Plasma on Wayland, Darkstar via Slipstream receiver), the output key `KEY_7` behaved as permanently held: an open chat window filled with `7777…`. Stopping skb resolved it immediately. No logs were available (skb was not running as a systemd unit).

Active mapping at the time: `KEY_2` (side button 2) → `KEY_7`.

## What is known

- Stopping skb destroys the uinput device; the kernel and the compositor then release every key still held on it. The fix-by-exit therefore indicates that skb's virtual device reported `KEY_7` as pressed and no release followed.
- The repetition itself is client-side key repeat (Wayland/Wine), which continues until a release arrives.

## Hypotheses

1. **Press and release on different interfaces (most likely).** The Slipstream receiver exposes two keyboard interfaces (e.g. `event8` and `event10`). skb creates one worker, engine and virtual device per interface (`internal/daemon/daemon.go`, `activate`). If the press of `KEY_2` arrives on one interface and the release on the other, the second engine drops the release because the key is not in its `active` list (`internal/remap/engine.go`, `processMapped`, `valueRelease` branch). The first engine keeps `KEY_7` held indefinitely. Pressing the side button again does not help if it keeps arriving on the other interface.
2. **Release lost on the radio link.** A short wireless dropout or the mouse entering sleep while the button is held, without the device node disappearing and without `SYN_DROPPED`. skb cannot notice this. Pressing the side button again would release the key in this case.
3. **Interference from `inputplumber`.** Bazzite runs `inputplumber`, which also watches the Corsair devices. Considered unlikely because the grab is exclusive.

## How to diagnose on recurrence

1. Press side button 2 again. If that ends the repetition, hypothesis 2 is more likely; if not, hypothesis 1.
2. Run skb with logging enabled (systemd unit or terminal) to capture `device stopped` / reconnect events around the incident.
3. Stop skb and watch both receiver interfaces at once with `sudo evtest` (`event8` and `event10`, verify the numbers via `/proc/bus/input/devices`). Check whether `KEY_2` press and release can appear on different interfaces.

## Possible mitigations

- Share mapped-key state across all interfaces of one physical device, or route a release to whichever engine holds the key.
- Release held mapped outputs after a timeout without repeat events from the source.
- Add an IPC command (e.g. `skb release-all`) that releases all held keys without stopping the daemon.
