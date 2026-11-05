# fsolar — read a Felicity battery without the app

A from-scratch Go reimplementation of the links used by the Felicity Solar
**FSolar** Android app. The battery speaks one text/JSON protocol over two
transports, and `fsolar` does both:

* **Bluetooth LE** (default, `-via ble`) — scans for the battery, connects,
  sends the app's commands, reassembles the split JSON responses.
* **WiFi hotspot** (`-via tcp`) — the app's "WiFi local" mode: a plain TCP
  socket to the battery at `192.168.155.1:53970` while on its AP.

Both convert the raw fixed-point numbers into engineering units and share the
same reassembly, field table and commands.

Protocol details and how they were verified: **[`../PROTOCOL.md`](../PROTOCOL.md)**
(§1.4 covers the WiFi/TCP transport).

## Build

Go 1.27. Bluetooth is provided by
[`tinygo.org/x/bluetooth`](https://tinygo.org), which talks to the native stack
of each OS — no TinyGo toolchain is required.

```
cd go-fsolar
go build -o ../bin/fsolar ./cmd/fsolar     # Linux / macOS
go build -o ../bin/fsolar.exe ./cmd/fsolar # Windows
```

Windows needs the Bluetooth stack enabled (Settings → Bluetooth). The first
call also warms up WinRT, so the initial connection can take a few seconds.

The machine needs a **Bluetooth 4.0+ (BLE) adapter**. The protocol is GATT
over BLE; a classic-only radio (Bluetooth 3.0, common in pre-2015 laptops)
cannot do it — `fsolar scan` then fails with
`failed to stop scanning (error code 9)`, and the tool prints a hint saying
exactly this. A ~$10 USB BT 4.0/5.0 dongle fixes it (disable the internal
3.0 radio afterwards).

## Use

```
fsolar scan                                  # find the battery, shows its MAC
fsolar info                                  # serial, model, firmware versions
fsolar real                                  # one telemetry snapshot
fsolar watch -interval 5s                    # keep polling
fsolar watch -count 1 -json                  # one raw JSON document, for cron
fsolar send "wifilocalMonitor:get dev set infor"     # the pack's settings
fsolar set '{"wmladd":2}'                    # write a parameter
```

`send` prints the raw JSON document, which is the quickest way to inspect
things the decoded view does not cover yet — the settings read, for instance,
returns cell voltage limits (`cVolHA`, `cVolLA` …), temperature thresholds
(`cCTHi`, `cDTLo` …) and balancer settings (`balEn`, `balVEn` …).

| flag | default | meaning |
|------|---------|---------|
| `-via` | `ble` | transport: `ble` or `tcp` |
| `-addr` | *(scan)* | connect to this MAC directly (BLE) |
| `-prefix` | `F` | advertised-name filter while scanning (BLE) |
| `-host` | `192.168.155.1` | battery monitor address (TCP) |
| `-port` | `53970` | battery monitor port (TCP) |
| `-interval` | `8s` | `watch` polling interval |
| `-timeout` | `15s` | per-command response timeout |
| `-count` | `0` | stop after N samples (0 = forever) |
| `-json` | off | print raw JSON instead of decoded values |

`fsolar set` **writes to the battery**. Nothing else in this tool changes
anything on the device.

## WiFi (TCP) mode

The battery also runs a WiFi access point, and the same commands work over a
plain TCP socket. Join its SSID (`"F" + DevSN`, factory password `12345678`)
and it is at `192.168.155.1:53970`:

```
fsolar -via tcp real
fsolar -via tcp -interval 5s watch
```

Two caveats from the field:

* **Windows 11 will not join the AP** — the battery's WPA/WPA2 mix includes
  WEP/TKIP group ciphers and Windows reports "Cannot connect to this network".
  Android (and most Linux stacks) join it fine.
* If your machine can't join the AP, bridge it with a phone: the phone sits on
  the AP and a tiny relay (cross-compiled for Android, source in
  `tools/relay/`) forwards the socket. Then point `fsolar` at localhost:

  ```
  GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build -o relay ./tools/relay
  adb push relay /data/local/tmp/ && adb shell 'chmod 755 /data/local/tmp/relay; /data/local/tmp/relay &'
  adb forward tcp:53970 tcp:53970
  fsolar -via tcp -host 127.0.0.1 real
  ```

  `tools/fsolar-probe.ps1` (outside this module) does the same thing without
  the relay, one short-lived `nc` connection per command.

The relay and the one-shot probe both exist because the battery's simple TCP
stack services **one client at a time** and does not recover from a TCP
half-close — close connections fully, and expect the first connect after a
teardown to be the flaky one (`fsolar` already retries 3×).

Sample session against a real pack:

```
$ fsolar scan
scanning for 10s ...
B8:3D:FB:C1:95:08  rssi= -85  name=F072604825024250800   <-- Felicity battery

$ fsolar -addr B8:3D:FB:C1:95:08 real
device   : DevSN=072604825024250800 Type=112 SubType=7250
flags    : Estate=0x13C0 Bstate=0x13C0 Bfault=0 Bwarn=0
  battVolt     pack voltage          52.940 V
  battCurr     pack current          -9.00 A
  battSoc      pack SOC              71.00 %
  cellVolt12   cell voltage 12       3.312 V
  ...
```

## Troubleshooting

Errors from the OS Bluetooth stack arrive as bare numeric codes; `fsolar`
appends a `hint:` line for the common ones. The most frequent cases:

| you see | it means | fix |
|---------|----------|-----|
| `failed to stop scanning (error code 9)` | the adapter has no BLE (Bluetooth < 4.0) | use a USB BT 4.0/5.0 dongle, disable the internal radio |
| `failed to stop scanning (error code 7)` | Bluetooth is switched off | turn it on in Settings → Bluetooth & devices |
| `failed to stop scanning (error code 2)` | another app holds the radio | close phone/headset pairing apps, retry |
| `no device advertising name prefix "F" seen in 20s` | nothing in range | battery powered on? within ~10 m? try `-prefix ""` with `fsolar scan` |
| `operation failed with code 1` during connect | the device was momentarily unreachable | usually self-heals — `fsolar` retries 3× |
| `Bluetooth ATT error (code 0x…)` | the battery's GATT server rejected a request | the hint names the ATT error; often fixed by reconnecting |

## Layout

| file | role |
|------|------|
| `internal/fsolar/protocol.go` | UUIDs, command strings, JSON notification reassembly |
| `internal/fsolar/link.go`     | `Link` interface shared by both transports |
| `internal/fsolar/client.go`   | BLE: adapter, scan, connect, subscribe, request/response |
| `internal/fsolar/tcp.go`      | TCP: the battery's WiFi-hotspot transport |
| `internal/fsolar/errors.go`   | translates numeric BLE-stack error codes into hints |
| `internal/fsolar/model.go`    | raw JSON → decoded values via the field table |
| `internal/fsolar/fields_gen.go` | generated field table for device type 112 |
| `internal/fsolar/binary.go`   | AES-CBC + CRC binary frames (legacy sticks) |
| `internal/fsolar/crc_tables.go` | the app's hard-coded CRC tables |
| `cmd/fsolar/main.go`          | CLI |
| `tools/relay/`                | Android TCP relay for bridging the battery to a PC |
| `../tools/genfields`          | regenerates `fields_gen.go` from the app's JSON |

## Tests

```
cd go-fsolar && go test ./...
```

25 tests. The crypto and framing tests are **not** self-consistency checks:
their expected bytes come from `../tools/groundtruth/FrameGround.java` and
`CRCGround.java`, which run the app's own decompiled algorithm on a real JVM,
so a passing suite means this Go code is byte-identical to the Android app.

Two decode tests run against `real infor` payloads captured from the battery by
two different means — a logcat capture, and a document reassembled from the raw
HCI snoop log (`capture/reassembled.jsonl`).

## Regenerating the field table

`fields_gen.go` is derived from the protocol file shipped inside the APK:

```
go run ./tools/genfields apk/extract/json/protocol_realtime_1.0.27.json 112 \
    go-fsolar/internal/fsolar/fields_gen.go
```

Change `112` to another device `Type` to support a different product family.

## Known limits

* The **TCP read path** (`real`/`info`/`watch`/`send`) is verified against a
  live battery; the TCP transport otherwise mirrors the app's persistent-socket
  behaviour. `set` over TCP is unverified, as over BLE.
* Only device **type 112** has a generated field table; other types decode
  against the same JSON but need their own table generated first.
* The AES-CBC binary frame path is implemented and verified against the app's
  Java, but has not been exercised against hardware — the battery tested here
  speaks the text protocol.
* `wifilocalMonitor:set cmd=...` is unverified against hardware. Start with a
  harmless parameter.
* Reconnecting after the battery drops is left to the caller.
