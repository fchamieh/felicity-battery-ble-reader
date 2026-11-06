# Felicity battery BLE reader

Read a **Felicity Solar** battery directly — no phone, no FSolar app. A
from-scratch Go CLI that speaks the battery's text/JSON protocol over
**Bluetooth LE** or its **WiFi hotspot (TCP)**, decoding the raw fixed-point
values into engineering units.

The protocol was reverse-engineered from the **FSolar 4.1.1** Android app
(decompiled with jadx) and verified against a live pack. Every statement in
[PROTOCOL.md](PROTOCOL.md) carries a tag:

| tag | meaning |
|-----|---------|
| ✅ | confirmed by a real exchange with the battery |
| 🧪 | confirmed by executing the app's own decompiled code on a JVM |
| 📖 | read out of the decompiled source, not independently exercised |

## Repository layout

| Path | What it is |
|------|------------|
| `go-fsolar/cmd/fsolar` | **The CLI** — build, usage and troubleshooting: [go-fsolar/README.md](go-fsolar/README.md) |
| `go-fsolar/cmd/felicity-battery-watch` | **Desktop app (Wails v3, vanilla JS)** — scan, tap a Felicity battery, watch SOC % and remaining kWh (60 s poll): [go-fsolar/cmd/felicity-battery-watch/README.md](go-fsolar/cmd/felicity-battery-watch/README.md) |
| `go-fsolar/pkg/fsolar` | Shared library: GATT/protocol, BLE + TCP transports, realtime decoding (used by the CLI and the desktop app) |
| `PROTOCOL.md` | The full protocol: GATT layout, commands, JSON shapes, fixed-point scaling, TCP transport |
| `tools/` | Research tooling: btsnoop→ATT extractor, CRC/field-table generators, ground-truth Java (the app's decompiled code, run on a JVM) |
| `capture/` | Sample captures: live JSON responses (`capture/live/`), an HCI snoop log, probe scripts |
| `packages.txt`, `apk_base_listing.txt` | APK inventory notes (the APK and decompiled sources are third-party and git-ignored) |

## Quick start

Build (Go 1.27+). Bluetooth comes from
[tinygo.org/x/bluetooth](https://tinygo.org), which talks to the OS's native
stack — no TinyGo toolchain required.

```sh
cd go-fsolar
go build -o bin/fsolar ./cmd/fsolar      # Linux / macOS (macOS: sh build-mac.sh — cgo, must build on a Mac)
go build -o bin/fsolar.exe ./cmd/fsolar  # Windows
```

Then:

```sh
fsolar scan                 # find the battery (advertises "F" + DevSN)
fsolar real                 # one telemetry snapshot
fsolar watch -interval 5s   # keep polling
fsolar -via tcp real        # over the battery's WiFi AP instead of BLE
```

Full flag table, WiFi/phone-relay setup, troubleshooting and sample output
live in [go-fsolar/README.md](go-fsolar/README.md).

## How decoding works

The battery reports raw fixed-point integers (`53060` = 53.060 V). The scaling
table — position, magnification, precision per field — ships inside the APK as
`protocol_realtime_1.0.27.json`; `tools/genfields` turns it into
`go-fsolar/pkg/fsolar/fields_gen.go` (device type **112**, a 16-cell 48 V
pack). Generate a table for another `Type` to support another product family.

## Verification

The Go test suite is anchored to ground truth, not self-consistency:
`tools/groundtruth/*.java` run the app's own decompiled AES/CRC/framing code on
a real JVM and emit the expected bytes, so a green suite means the Go
implementation is byte-identical to the Android app. Two decode tests replay
real captures, one reassembled from raw HCI
(`capture/reassembled.jsonl`).

```sh
cd go-fsolar && go test ./...
```

## License

MIT — see [LICENSE](LICENSE).
