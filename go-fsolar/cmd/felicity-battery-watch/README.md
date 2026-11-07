# Felicity Battery Watch

Cross-platform desktop monitor for Felicity Solar batteries. Scan over **Bluetooth
LE**, tap a battery, and watch pack SOC (%) and remaining energy (kWh) — polled
every 60 s. Or dial the battery's **WiFi hotspot over TCP** and watch the same
numbers.

Built with [Wails v3](https://wails.io) (vanilla JS + Vite frontend) on top of the
shared `fsolar/pkg/fsolar` library — the same code the `fsolar` CLI uses, over the
same `Link` interface.

## Prerequisites

| Tool | Version | Notes |
|---|---|---|
| Go | 1.27+ | `go version` |
| Node + npm | 20+ | the frontend is Vite 8 |
| [Task](https://taskfile.dev) | 3.x | Wails v3 drives builds through `Taskfile.yml` |
| `wails3` CLI | v3.0.0 | `go install github.com/wailsapp/wails/v3/cmd/wails3@latest` |
| WebView2 runtime | any recent | preinstalled on Windows 11; `wails3 doctor` reports it |

No C compiler is needed on Windows: the build runs `CGO_ENABLED=0` and Bluetooth
comes from [`tinygo.org/x/bluetooth`](https://tinygo.org) (WinRT / CoreBluetooth /
BlueZ in pure Go). macOS needs Xcode (CGO); Linux needs WebKit2GTK dev headers
(`libwebkit2gtk-4.1-dev`) or the Docker cross-compile image.

`wails3 doctor` prints a toolchain report if something is missing.

## Build

```sh
cd go-fsolar/cmd/felicity-battery-watch

wails3 build          # production → bin/felicity-battery-watch(.exe)
wails3 dev            # hot reload: Vite on :9245 + rebuild on .go changes
wails3 package        # installer: NSIS on Windows, .app/.dmg on macOS
```

`wails3 build` does everything in order: `go mod tidy` → `wails3 generate bindings`
→ `npm install` → `vite build` (→ `frontend/dist`, embedded with `go:embed`) →
`wails3 generate syso` (Windows icon/version info) → `go build -tags production
-ldflags "-w -s -H windowsgui"`.

Run it with `wails3 task run`, or just launch the binary:

```sh
bin/felicity-battery-watch.exe                 # Windows
./bin/felicity-battery-watch                   # Linux / macOS
```

### Other targets

```sh
wails3 build ARCH=arm64                    # Windows ARM64
wails3 build GOOS=linux                    # build on Linux, or:
wails3 task setup:docker && wails3 build GOOS=linux CGO_ENABLED=1
wails3 build GOOS=darwin                   # run this on a Mac (Xcode)
wails3 package FORMAT=msix                 # Windows MSIX (wails3 task install:msix:tools)
```

## Usage — Bluetooth (default)

1. Windows Settings → **Bluetooth on**; power the battery within range.
2. **Scan 10s** → Felicity batteries advertise as `F` + DevSN (e.g. `F072604825024250800`),
   strongest RSSI first.
3. Tap one → it connects and shows SOC %, remaining kWh, voltage, current and capacity.
4. Readings repeat every 60 s; **Read now** fetches at once, **Disconnect / Back**
   returns to scanning. Failed scans retry automatically after 3 s.

The service the app uses is `6e6f736a-4643-4d44-8fa9-0fafd005e455`, write
characteristic `ISRX` (handle `0x11`), notifications from `ISTX` (handle `0x0E`) —
see [PROTOCOL.md](../../PROTOCOL.md).

## Usage — WiFi hotspot (TCP)

Pick **WiFi · TCP** in the transport switch, set host and port, press **Connect**
(Enter in either field works too). Everything else — polling, SOC, kWh — is identical.

| Situation | Host | Port |
|---|---|---|
| PC joined to the battery's own AP | `192.168.155.1` | `53970` |
| PC reaching it through the phone/adb relay | `127.0.0.1` | `53970` |

The battery's hotspot is a DHCP server at `192.168.155.1` and its monitor socket
listens on `53970`. If Windows refuses that AP (it does: the battery's WPA2 group
ciphers include WEP40/WEP104, which Windows 11 rejects), bridge it through the
phone — see [go-fsolar/README.md](../README.md#wifi-tcp-mode):

```sh
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build -o relay ../tools/relay
adb push relay /data/local/tmp/relay
adb shell 'chmod 755 /data/local/tmp/relay; /data/local/tmp/relay -listen 127.0.0.1:53970 -target 192.168.155.1:53970 &'
adb forward tcp:53970 tcp:53970
```

then set host `127.0.0.1`, port `53970` in the app.

### Launch flags

The transport can be fixed at launch, which also dials immediately — handy for the
bridge workflow:

```sh
bin/felicity-battery-watch.exe -via tcp -host 127.0.0.1 -port 53970
bin/felicity-battery-watch.exe -via tcp              # 192.168.155.1:53970
bin/felicity-battery-watch.exe                       # Bluetooth
```

The battery serves **one TCP client at a time** and does not recover from a
half-closed socket, so the app fully closes the link on Disconnect / transport
switch, and does *not* auto-retry a failed dial — press **Connect** again.

## Formula

`Remaining kWh = SOC% ÷ 100 × Voltage(V) × Capacity(Ah) ÷ 1000`

Pack (EMS) values are preferred when the device reports them; the per-battery
(BMS) values are the fallback. `65535` / `32767` / `null` mean "not measured".

## Notes

- macOS builds ask for Bluetooth permission — `NSBluetoothAlwaysUsageDescription`
  is already in `build/darwin/Info.plist` / `Info.dev.plist`.
- `frontend/dist`, `frontend/bindings` and `frontend/node_modules` are generated
  and gitignored. A plain `go build` in this directory therefore fails with
  `pattern all:frontend/dist: no matching files found` until `wails3 build` (or
  `wails3 task common:build:frontend`) has run once.
- `frontend/bindings/` is regenerated with `-clean=true` on every build: change
  `battery_service.go`, never the generated JS.
- Tests: `go test ./...` covers payload decoding, address parsing and the
  transport switch.

## Wails service surface

| Method | Purpose |
|---|---|
| `Scan(seconds)` | BLE scan (refused in TCP mode) → `[]BatteryDevice` |
| `Connect(deviceID)` | connect a scanned BLE device |
| `ConnectTCP(host, port)` | dial the WiFi-hotspot socket |
| `SetTransport(mode, host, port)` | `ble` \| `tcp`; closes any live link first |
| `GetTransport()` | active mode, endpoint, connected flag |
| `GetReading()` | one realtime snapshot |
| `Disconnect()` | close the link, stop polling |

Push events: `battery:devices`, `battery:reading`, `battery:connection`,
`battery:transport`.
