# skb

Daemon und TUI für die Corsair Darkstar. Der Daemon greift (EVIOCGRAB) die Tastatur-Interfaces der Maus ab, belegt die Seitentasten neu und leitet alle übrigen Ereignisse an ein virtuelles uinput-Gerät weiter. Hotplug und der Wechsel zwischen Kabel und Funk werden per inotify erkannt. Mappings werden in der TUI angelernt, bearbeitet und gelöscht; der Daemon übernimmt Änderungen ohne Neustart.

Unterstützte Geräte (USB-ID):

- Darkstar kabelgebunden: `1b1c:1bb2`
- Darkstar über Slipstream-Empfänger: `1b1c:1bdc`

Andere Corsair-Geräte (z. B. die K100, `1b1c:1b7c`) bleiben unberührt.

Der Eintrag `1b1c:1bdc` deckt den gesamten Slipstream-Empfänger ab. Weitere an denselben Dongle gekoppelte Corsair-Geräte würden ebenfalls abgegriffen (reine Durchleitung, kein Funktionsverlust); bei Bedarf in der Konfiguration einschränken.

## Voraussetzungen

- [Task](https://taskfile.dev)
- Docker Compose
- für die Installation aus der TUI: polkit (`pkexec`) mit einem Authentifizierungs-Agent (Plasma bringt einen mit)

Go läuft ausschließlich im Container; auf dem Host wird kein Go benötigt.

## Befehle

```sh
task qa          # Build, tidy, lint, Tests (-race), govulncheck
task build       # statisches Binary nach ./bin/skb
task install     # sudo ./bin/skb install --admin-uid <uid>: Binary, Unit und Config installieren
task uninstall   # Binary und Unit entfernen, /etc/skb bleibt erhalten
```

## Workflow in der TUI

`./bin/skb tui` (oder nach der Installation `skb tui`):

1. **Setup** (`s`): zeigt, ob Unit installiert und Dienst aktiv sind. `i` installiert und startet den Dienst über `pkexec`, `x` deaktiviert zusätzlich input-remapper, `u` deinstalliert. Ist der Daemon nicht erreichbar und die Unit nicht installiert, startet die TUI direkt im Setup.
2. **Hinzufügen** (`a`): Schritt 1/3 die Seitentaste an der Maus drücken, Schritt 2/3 die Zieltaste oder Kombination auf der Tastatur drücken, Schritt 3/3 mit `enter` speichern (`t` lernt das Ziel neu, `esc` verwirft).
3. **Bearbeiten** (`e`): überspringt Schritt 1 und lernt das Ziel des gewählten Mappings neu.
4. **Löschen** (`d`, dann `y`).

`esc` bricht jede Lernphase ab und kann deshalb nicht als Zieltaste gelernt werden. Beim Lernen des Ziels werden alle Tastaturen gelesen, die nicht abgegriffen sind, ohne sie zu greifen; eine Tastatur, die ein anderes Programm (z. B. input-remapper) abgreift, kann nicht angelernt werden. Die Zeitgrenze pro Lernschritt beträgt 10 s, die Anzeige der Restzeit aktualisiert sich mit dem Status-Poll.

**Hinweis zur Installation aus der TUI:** Das Setup startet `pkexec` auf dem Pfad des Binaries, aus dem die TUI gestartet wurde, und kopiert genau dieses Binary nach `/usr/local/bin/skb`. Für maximale Sicherheit die Erstinstallation daher mit `task install` (sudo) durchführen und die TUI danach als `/usr/local/bin/skb tui` starten.

## Konfiguration und Berechtigungen

`/etc/skb/config.toml` wird vom Daemon verwaltet und beim Speichern atomar neu geschrieben; handgeschriebene Kommentare gehen dabei verloren. `task install` überschreibt eine vorhandene Datei nicht, ergänzt aber `admin_uid`, falls dort keiner steht. Fehlt die Datei, startet der Daemon mit eingebauten Defaults und legt sie beim ersten Speichern an. Vorlage: `configs/config.toml`.

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

In `to` sind alle Einträge außer dem letzten Modifier, der letzte ist die Haupttaste (nur Tastatur-Keycodes, keine `BTN_*`). Die Ausgabe sind evdev-Keycodes und damit layoutunabhängig: `KEY_MINUS` ist die Taste rechts neben der 0, auf dem deutschen Layout also ß; Shift+ß ergibt dort "?".

Ändernde und lernende Operationen erlaubt der Daemon nur Prozessen mit `uid == admin_uid` oder root (geprüft per `SO_PEERCRED` bei jeder Anfrage). Der Status-Socket `/run/skb/status.sock` hat Modus 0666, damit jeder lokale Benutzer den Status lesen kann; das ist nur wegen dieser Prüfung vertretbar. Ist die TUI-uid nicht `admin_uid`, werden die Aktionen gesperrt und auf das Setup verwiesen.

Prüfen: `skb check-config --config configs/config.toml`

## Greifregel

Jedes tastaturfähige Interface (mit mindestens einer Tastatur-Taste) der konfigurierten IDs wird abgegriffen, unabhängig von den Mappings; ohne Mappings werden die Ereignisse unverändert durchgereicht. Reine Zeiger-Interfaces (nur `BTN_*`) bleiben unberührt. Die virtuellen Geräte kündigen alle Tastatur-Keycodes im Voraus an, damit neue Mappings ohne Neuanlage des Geräts wirken.

## input-remapper

input-remapper muss für die Darkstar deaktiviert sein, sonst schlägt das Abgreifen mit EBUSY fehl (sichtbar in der TUI und im Journal). Im Setup `x` wählen, in input-remapper-gtk die Injektion stoppen und Autoload für beide Corsair-Einträge abschalten, oder `sudo systemctl disable --now input-remapper`.

## Status

- `skb tui` zeigt Geräte (kabelgebunden/Funk, Status, Fehler) und Mappings.
- `journalctl -u skb` bzw. `task logs`. Gedrückte Tasten werden nie geloggt.

## Einschränkungen

- Pro abgegriffenem Interface entsteht ein eigenes virtuelles Gerät.
- LED- und Repeat-Ereignisse (EV_LED, EV_REP) werden nicht weitergeleitet.
- Das virtuelle Gerät kann keine ABS-Bereiche setzen; der angebotene `ABS_VOLUME`-Code erhält den Bereich 0..0.
- Eine beim Lernstart bereits gehaltene Seitentaste wird nicht erfasst; erneut drücken.
- Die TUI einer neuen Version gegen einen alten Daemon meldet "daemon outdated" und verweist auf das Setup.
