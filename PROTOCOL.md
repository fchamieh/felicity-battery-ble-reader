# Felicity Solar ("FSolar") battery protocol: BLE and WiFi (TCP)

Reverse-engineered from **FSolar 4.1.1** (`com.felicity.solar`), decompiled with
jadx 1.5.6, and confirmed against a **live battery** on this machine.

The battery speaks one text/JSON protocol over **two transports**: Bluetooth LE
(§1.1–1.3) and a WiFi hotspot with a plain TCP socket, the app's "WiFi local"
mode (§1.4). The command set (§2) is identical on both.

Every statement below is tagged:

| tag | meaning |
|-----|---------|
| ✅ | confirmed by a real exchange with the battery (`B8:3D:FB:C1:95:08`) |
| 🧪 | confirmed by executing the app's own decompiled code on the JVM |
| 📖 | read out of the decompiled source, not independently exercised |

## The battery used for this work

| | |
|---|---|
| **BLE MAC address** | **`B8:3D:FB:C1:95:08`** |
| Advertised name | `F072604825024250800` (`"F" + DevSN`) |
| DevSN | `072604825024250800` |
| Type / SubType | `112` / `7250` — a 16-cell 48 V / 250 Ah pack |
| Connect directly, skipping the scan | `fsolar -addr B8:3D:FB:C1:95:08 real` |

Everything marked ✅ below was measured against this pack.

---

## 1. Transport

A Felicity battery is a plain BLE GATT peripheral. No pairing, no bonding, no
encryption at the BLE layer — everything is carried as plaintext over the
characteristic values.

### 1.1 Advertisement ✅

* Advertised name is **`"F" + DevSN`** — e.g. `F072604825024250800`.
* The app scans by name prefix `F` (`c8/u.java`).
* The DevSN echoed back inside every response is the advertised name with the
  leading `F` removed.

### 1.2 GATT layout ✅

| Role | UUID | Notes |
|------|------|-------|
| Service | `6e6f736a-4643-4d44-8fa9-0fafd005e455` | ASCII `"nosjFCMD"` in the first 8 bytes |
| Write (host → battery) | `49535258-184d-4bd9-bc61-20c647249616` | ASCII `ISRX` prefix |
| Notify (battery → host) | `49535458-8341-43f4-a9d4-ec0e34729bb3` | ASCII `ISTX` prefix |
| CCC descriptor | `00002902-0000-1000-8000-00805f9b34fb` | write `0x0001` to enable notify |

The UUIDs are self-describing once you decode the ASCII prefix: `nosj` is
`json` reversed. 📖 The app names them `UUID_SERVICE`, `UUID_WRITE`,
`UUID_NOTIFY`.

A second pair exists for account/user traffic on some firmwares 📖:

| Role | UUID |
|------|------|
| Write (upper) | `49536108-184d-4bd9-bc61-20c647249616` |
| Notify (upper) | `49536008-8341-43f4-a9d4-ec0e34729bb3` |

Legacy MPPT sticks use a completely different, binary protocol on
`000000ff-…` / `ff03` / `ff01` (see §4).

### 1.3 Writes and MTU ✅

Measured from `capture/btsnoop_hci.log` (see `tools/btsnoop_att.ps1`, which
replays it and prints every ATT PDU):

* The app writes with **ATT Write Command (opcode `0x52`, unacknowledged)** to
  characteristic value handle **`0x11`**.
* Notifications arrive on value handle **`0x0E`**.
* The negotiated ATT MTU is **247**, so a notification carries at most
  **244** bytes of JSON.
* A whole command fits in one write — the longest seen is 37 bytes — so MTU
  negotiation is irrelevant on the TX side.

`fsolar` issues a normal `WriteCharacteristic` and falls back to
`WriteCharacteristicWithoutResponse` if the regular write is rejected; on the
battery tested here the normal write succeeds.

### 1.4 WiFi local mode: plain TCP ✅

The battery also runs a **WiFi access point**, and the same text protocol runs
over a plain TCP socket — no pairing, no auth, no handshake. The app hard-codes
the endpoint (`d8/g.java`, constants `V`/`W`):

| | |
|---|---|
| SSID | `"F" + DevSN` (e.g. `F072604825024250772`; app strings say hotspots may also start with `S`, `Solar`, `I`, `MI`) |
| Password | factory default `12345678` |
| Security | WPA/WPA2-PSK with a mixed cipher set including WEP40/WEP104 + TKIP/CCMP |
| Battery address | `192.168.155.1` — the AP is also the DHCP server; clients get `192.168.155.2/24`, `192.168.155.3/24`, … |
| Port | **53970** |

Wire behaviour (verified live; captures in `capture/live/`):

* **No handshake, no auth.** The first bytes on the wire are the first
  command; there is no login or device-ID exchange. The connect thread in
  `e8/d.java` writes nothing.
* Commands are the identical raw UTF-8 strings from §2 — no newline, no length
  prefix. The reply is **one or more unframed JSON objects** streamed back.
* `get dev set infor` answers with **two** concatenated objects in a single
  reply: `{...,"index":1}{...,"index":2}`.
* The battery **keeps the connection open** after answering (the app reuses
  one socket for everything, `e8/d.java`); a one-shot client that connects,
  sends one command, reads the reply and closes works just as well — that is
  how `tools/fsolar-probe.ps1` probes it.
* The app re-sends every monitor command **every 1 s** on its persistent socket
  (RxJava `interval` in `a8/r.java`) and pings the link with **TCP urgent data
  `0xFF` every 5 s** (`e8/d.java`, `socket.sendUrgentData(255)`). Neither is
  needed to get replies.
* **Windows 11 will not join the AP** — it reports "Cannot connect to this
  network" (the WEP/TKIP group-cipher mix). Android joins it fine, so on this
  machine the practical bridge is: **phone on the AP + `adb` + an `nc` pipe**
  (`tools/fsolar-probe.ps1`). Any client that can join the SSID talks to the
  battery directly at `192.168.155.1:53970` — e.g. `fsolar -via tcp`.

---

## 2. The text command protocol

Commands are **plain UTF-8 text** — written to the `ISRX` characteristic over
BLE, or sent verbatim over the TCP socket in WiFi mode (§1.4). Responses come
back as **UTF-8 JSON** (BLE notifications / TCP stream). The command set is
identical on both transports. The command grammar is:

```
wifilocalMonitor:get <what>            read
wifilocalMonitor:set cmd=<json object> write
wifiGet <thing>                        misc
```

### 2.1 Commands 📖 ✅

Constant strings live in `a8/r.java` (`IModuleCommManagerImpl`).

| Command | Purpose | BLE | TCP |
|---------|---------|-----|-----|
| `wifilocalMonitor:get dev basice infor` | serial, model type, firmware versions | ✅ | ✅ |
| `wifilocalMonitor:get Date` | device clock + UTC offset | ✅ | ✅ |
| `wifilocalMonitor:get dev real infor` | realtime telemetry snapshot | ✅ | ✅ |
| `wifilocalMonitor:get dev set infor` | configurable settings | ✅ | ✅ |
| `wifilocalMonitor:get dev test infor` | self-test results | 📖 | ✅ (empty reply unless in self-test) |
| `wifiGet mode` | comm mode of the stick | 📖 | |
| `wifiGet debug` | debug key | 📖 | |
| `wifilocalMonitor:set cmd={"wmladd":2}` | write parameters | 📖 | |

Note the app's own typos (`infor`, not `info`) — they are part of the protocol.
Getting them wrong gets no reply.

The battery also emits lines that are not JSON at all, e.g. `get srv ok:` — the
app explicitly special-cases that prefix (`a8/r.java:569`, `a8/v.java:144`) — so
a reader must tolerate non-JSON frames. 📖

### 2.2 Framing: JSON split across notifications ✅

One JSON document is **not** one notification. The battery chunks a long
response across several notification packets, and the boundary can fall in the
middle of a number. `json.Unmarshal` per notification never works.

Measured from `capture/btsnoop_hci.log` (137 ACL packets → 10 notifications,
1955 bytes of JSON):

| document | size | arrived as |
|----------|------|------------|
| `dev basice infor` | 226 B | 226 |
| `Date` | 99 B | 99 |
| `dev real infor` | **807 B** | **244 + 244 + 244 + 75** |

ATT MTU is 247, so a notification carries at most **244** bytes of JSON —
exactly the size of each of the first three fragments above. The verbatim cut
points of one `real infor` response:

```
[2] 244 B | {"CommVer":1,...,"BTemp":[[260,260],[256,256]],"Batt":[[
[3] 244 B | 53100],[-189],[null]],"Batsoc":[[7550,1000,500000]],...,"BatcelList":[[3313,3318,...
[4] 244 B | 3313,3319,...,"BMSpara":[[2,6]],"BLVolCu":[[57
[5]  75 B | 6,480],[1500,1500]],"BtemList":[[260,260,260,260,32767,32767,32767,32767]]}
```

Note `[4]→[5]` cutting `576` into `57` + `6`, and `[2]→[3]` cutting `53100`.
`tools/btsnoop_att.ps1` reproduces these numbers from the capture.

The app solves this the same way: its GATT callback appends every notification
into a `StringBuffer` and only then looks for a `{` … `}` pair
(`c8/u.java:444-452`). `fsolar`'s `JSONStream` does the same but correctly
handles nested arrays, strings containing braces, and `\"` escapes.

`Request()` reassembles and blocks until one complete document arrives (or a
timeout).

---

## 3. Response payloads

The same payloads are returned on the TCP transport (§1.4); live TCP captures
of all five `get` commands are in `capture/live/`.

### 3.1 `dev basice infor` ✅

Verbatim from the capture (226 bytes, one notification):

```json
{"CommVer":1,"version":"1.03","wifiSN":"F072604825024250800","COM":3,
 "iotType":3,"modID":2,"DevSN":"072604825024250800","Type":112,
 "SubType":7250,"DSwVer":65535,"M1SwVer":518,"M2SwVer":5,"DHwVer":0,
 "CtHwVer":0,"PwHwVer":65535}
```

`Type` selects which realtime field table applies — see §5. `DSwVer` and
`PwHwVer` come back as `65535`: the "not measured" sentinel again.

### 3.2 `Date` ✅

```json
{"CommVer":1,"wifiSN":"F072604825024250800","iotType":3,
 "dateTime":"20210101004322","timeZMin":120}
```

`timeZMin` is a UTC offset in minutes. The clock itself reads January 2021 —
this battery has never been given the correct date.

### 3.3 `dev real infor` ✅

One flat object. Scalar status fields plus **nested arrays** for anything that
has one value per cell. Real capture (abridged):

```json
{"CommVer":1,"DevSN":"072604825024250800","Type":112,"SubType":7250,
 "Estate":5056,"Bfault":0,"Bwarn":0,"Bstate":5056,
 "Batt":[[53100],[-178],[null]],
 "Batsoc":[[7600,1000,500000]],
 "BattList":[[53060,65535],[-87,-1]],
 "BatsocList":[[7600,1000,250000]],
 "BatcelList":[[3314,3318,3315,3318,3314,3318,3315,3318,
               3310,3318,3314,3319,3314,3318,3314,3318],
              [65535,65535,65535,65535,65535,65535,65535,65535,
               65535,65535,65535,65535,65535,65535,65535,65535]],
 "BMaxMin":[[3319,3310],[11,8]],
 "BMSpara":[[2,6]],
 "BLVolCu":[[576,480],[1500,1500]],
 "BtemList":[[260,260,260,260,32767,32767,32767,32767]],
 "Templist":[[260,260],[0,0],[65535,65535],[65535,65535]]}
```

Three traps in this payload:

1. **Fixed-point.** There are no decimal points anywhere. `53060` means
   `53.060 V`, `7600` means `76.00 %`. The scaling factor is **not** in the
   payload — it comes from the field table (§5).
2. **Sentinels.** `65535` (`0xFFFF`) and `32767` (`0x7FFF`) mean *not measured*.
   `null` also appears. A naive decoder prints 6553.5 V cells.
3. **Near-identical key names.** `Batt` vs `BattList`, `Batsoc` vs
   `BatsocList`, `BTemp` vs `BtemList` vs `Templist` are **different** keys
   carrying different things (EMS-side vs BMS-side measurements). Only the
   *case* of `TempList`/`Templist` differs from the app's table — case is
   folded when matching, but `List` is never stripped.

`Estate` / `Bstate` are bit fields, printed as hex (`0x13C0` here). 📖 The app
decodes them against `file_standard_cache/standard_*.zip`.

---

### 3.4 `dev set infor` ✅

The writable settings, read live from the battery. One flat object, no arrays:

```json
{"CommVer":1,"DevSN":"072604825024250800","modID":2,"ttlPack":2,"index":1,
 "Type":112,"wHMHt":0,"wSlNum":0,"wMlAdd":0,"wMlRes":0,
 "wCVP80":3450,"wCVP20":3050,
 "cVolH2":3750,"cVolL2":0,"cVolHA":3600,"cVolHAB":3400,"cVolHi":3650,
 "cVolHB":3400,"cVolLA":2750,"cVolLAB":2950,"cVolLo":2500,"cVolLB":2950,
 "bCCHAB":750,"bCCHA":1500,"bCCHi":1600,"bCCHi2":2000,
 "bDCHAB":750,"bDCHA":1500,"bDCHi":1600,"bDCHi2":2000,
 "cCTHAB":45,"cCTHA":50,"cCTHi":55,"cCTHi2":57,
 "cCTLAB":10,"cCTLA":5,"cCTLo":0,"cCTLo2":-2,
 "cDTHAB":45,"cDTHA":50,"cDTHi":55,"cDTHi2":57,
 "cDTLAB":-10,"cDTLA":-15,"cDTLo":-20,"cDTLo2":-22,
 "hTHAlm":90,"hTHi":100,
 "balEn":1,"balVErr":65535,"balVEn":3380,"balTEn":0,
 "cDDep":31,"cTempC":0,"fRstCnt":10,"fClrTi":60,
 "iExTime":1440,"sExTime":5,"bWirMo":0}
```

These names line up with the `product_setting_*` files in the APK, and they are
the keys `wifilocalMonitor:set cmd={...}` accepts. Cell limits are in mV
(`cVolHA` 3600 = 3.600 V), temperatures in °C, and `balVErr` is the `65535`
sentinel.

The reply is actually **two concatenated objects** — `ttlPack` says how many
follow. The second one (index 2) is small:

```json
{"CommVer":1,"DevSN":"072604825024250772","modID":1,"ttlPack":2,"index":2,
 "Type":112,"bLigEn":0,"mRtnEn":0,"fRecEn":0}
```

**Writing has not been tested against hardware.** Nothing in this session
changed a setting on the real pack.

---

## 4. The binary protocol (legacy MPPT / modem sticks)

Used on `000000ff-…` / `ff03` / `ff01` instead of JSON. Implemented in
`c/a.java`, `c/b.java`, `c/c.java`, `b/a.java`, `b/b.java`.

### 4.1 Frame layout 🧪

```
offset  size  field
0       2     total length, big endian  = (everything after this field) + 2
                                              i.e. excludes the trailing CRC
2       2     constant 0x00 0x06
4       2     encrypted-region length, big endian
6       1     version, 0x01
7       1     command: 0x18 = write params, 0x19 = read params
8       N     AES-CBC ciphertext, N = encrypted-region length
8+N     2     CRC-16, big endian
```

`total length` counts everything **except** the trailing CRC — the off-by-two
here is the single easiest way to get this protocol wrong. 🧪

### 4.2 Encryption 🧪

`AES/CBC/NoPadding`, static credentials from `b/a.java`:

```
key : 31 73 78 64 5F 61 69 6F 74 5F 6B 65 79 5F 30 30 31   "sxd_aiot_key_001"
iv  : 73 78 64 5F 61 69 6F 74 5F 32 30 32 32 5F 30 31      "sxd_aiot_2022_01"
```

No padding scheme: the plaintext is padded with **zero bytes** so its length is
a multiple of 16. Padding count is computed over the *pre*-encryption body:

```
read  (0x19): padding = (16 - (bodyLen + 12) % 16) % 16    ; 12 = 10 marker + 2 count
write (0x18): padding = (16 - (bodyLen + 14) % 16) % 16    ; 14 = 10 marker + 2 count + 2 bodyLen
```

### 4.3 CRC 🧪

**CRC-16/MODBUS** (poly `0x8005` reflected → `0xA001`, init `0xFFFF`), emitted
big-endian. The app implements it with two hard-coded 256-byte tables
(`b/b.java` fields `f6356a`, `f6357b`) rather than a bit loop.

Proof: the app's tables were loaded out of the decompiled source, run on the
JVM, and matched the standard table exactly; `crc("123456789") = 4B37`, which
is the published CRC-16/MODBUS check value. Go reproduces the same bytes.

### 4.4 Encrypted body 🧪

```
read  0x19: "0000000000" | count(u16) | id[0](u16) id[1](u16) ... | 0x00 padding
write 0x18: "0000000000" | count(u16) | bodyLen(u16) | params | 0x00 padding
          param = no(u16) | valueLen(u16) | value bytes
```

The literal ten ASCII `0` characters are a magic marker the receiver checks.

### 4.5 Golden frames 🧪

Produced by `FrameGround.java`, a line-by-line port of the app's builder run on
the JVM, and reproduced byte-for-byte by the Go code:

```
read  ids [5]                 0018 0006 0010 01 19 963F918F231F6749A141145B757A94A48DB3
read  ids [1,2,3]             0028 0006 0014 01 19 A9E222164460859315387EF0417780A1
                              F11130E8FFE86405D76031B76AD5D353A620
write params 1=[01 02] 7=[0A] 0028 0006 001B 01 18 8C2C3303044566591409AEAE1BD32D56
                              E399D2A9956825416249ECFF0C0B2ACFF082
```

---

## 5. Field tables: how raw numbers become engineering units

The payload carries no scaling information. It lives in
`apk/extract/json/protocol_realtime_1.0.27.json`, keyed by device `Type`
(112 = this battery), with one row per parameter:

```json
{"baseParamName":"BattList","position":"0,0","magnification":-3,
 "precisions":2,"paramName":"battVolt"}
```

meaning: read `payload["BattList"][0][0]` and multiply by
`10^magnification`. `precisions` is how many decimals the app displays.

`position` is `"outer,inner"` — comma separated, **not** an underscore, and
always exactly two numbers (all 2750 non-empty positions in the shipped table
have this shape). An empty `position` means the key holds a scalar.

`tools/genfields` turns that file into `pkg/fsolar/fields_gen.go` — 46
fields for type 112. It is regenerated with:

```
go run ./tools/genfields apk/extract/json/protocol_realtime_1.0.27.json 112 \
    go-fsolar/pkg/fsolar/fields_gen.go
```

A firmware that sends `[53060]` instead of `[[53060]]` is accepted too.

### 5.1 Worked examples ✅

| key | position | raw | mag | displayed |
|-----|----------|-----|-----|-----------|
| `BattList` | `0,0` | 53060 | −3 | 53.060 V |
| `BattList` | `1,0` | −87 | −1 | −8.7 A |
| `BatsocList` | `0,0` | 7600 | −2 | 76.0 % |
| `BatsocList` | `0,2` | 250000 | −3 | 250.0 Ah |
| `BatcelList` | `0,11` | 3319 | −3 | 3.319 V |
| `BMaxMin` | `1,0` | 11 | 0 | 11 (cell index, **0-based**) |
| `BLVolCu` | `0,0` | 576 | −1 | 57.6 V |
| `BtemList` | `0,0` | 260 | −1 | 26.0 °C |
| `Batt` | `0,0` | 53100 | −3 | 53.100 V (EMS side) |

---

## 6. Gotchas found the hard way

1. **`infor` not `info`.** Copy the app's strings exactly.
2. **Notification reassembly.** Never parse one notification as one document.
3. **`Batt` ≠ `BattList`.** EMS-side vs BMS-side. Do not strip `List` when
   matching keys — only fold case.
4. **65535 / 32767 / `null` are "not measured"**, not values.
5. **Display precision must be at least `|magnification|`.** Printing
   `precisions=0` fields with 0 decimals hid `53.060` as `53`.
6. **CRC is big-endian**, and the length field excludes it.
7. **`Type` is mandatory** before decoding: the same key scales differently in
   another device family.
8. **Cell indices are 0-based.** `BMaxMin[1][0]` is the array index of the
   highest cell, not a 1-based cell number. Proven twice: in one sample `3319`
   sits at array index `5` and `BMaxMin[1][0]` is `5`; in the other `3319` is at
   index `11` and the field reports `11`. Subtract one before printing
   "cell #N" to a human.
9. The battery advertises with a **non-standard, non-resolvable** public
   address on some units — connect by observed MAC, not by name alone.
10. **TCP replies are unframed and can hold several objects.** `get dev set
    infor` answers with two concatenated JSON objects; split on balanced
    braces, never on newlines (there are none).
11. **Windows 11 will not join the battery's AP** (WEP/TKIP group ciphers).
    Bridge through a phone on the AP, or use a client whose Wi-Fi stack
    accepts the cipher mix.
12. **Android treats the AP as "no internet"** and silently drops the
    connection; when reconnecting, accept the "continue to use" prompt or
    Android permanently disables the network (`NETWORK_SELECTION_DISABLED_NO_INTERNET_PERMANENT`).
13. **`adb exec-out` does not forward stdin** to the remote command — a
    `echo ... | nc` pipeline must be composed *on the phone* (`adb shell`),
    not on the PC.

---

## 7. Source map

| Behaviour | Decompiled location |
|-----------|---------------------|
| UUIDs, GATT setup, notification buffering | `c8/u.java` |
| Command strings | `a8/r.java` (`IModuleCommManagerImpl`) |
| Binary frame assembly | `c/c.java` (`d()` at line 62), `c/a.java`, `c/b.java` |
| AES key / IV / concat / u16 | `b/a.java` (`d()`, `c()`, `b()`) |
| CRC tables and loop | `b/b.java` (`a(byte[])`) |
| Field tables per device type | `apk/extract/json/protocol_realtime_1.0.27.json` |

Reproduce the decompile with:

```
java -jar jadx-1.5.6.zip -d decompiled apk/FSolar-arm64_v8a.apk apk/FSolar-base.apk
```

---

## 8. Artifacts in this tree

| path | what it is |
|------|------------|
| `apk/FSolar-base.apk`, `apk/FSolar-arm64_v8a.apk` | the app pulled off the phone |
| `apk/extract/json/protocol_realtime_1.0.27.json` | the app's per-device-type field tables |
| `decompiled/sources/` | jadx output, 20 512 files |
| `capture/btsnoop_hci.log` | the phone's HCI snoop log: real ATT traffic |
| `capture/logcat.txt` | the app's own log lines, including the commands it sent |
| `capture/reassembled.jsonl` | JSON documents rebuilt from the snoop log |
| `tools/btsnoop_att.ps1` | replays the snoop log, prints every ATT PDU |
| `tools/btsnoop_extract.ps1` | replays it and writes `reassembled.jsonl` |
| `tools/btsnoop/` | earlier Go attempt at the same thing — **superseded**: it assumes a standard HCI ACL header and never reassembles fragments, so it finds 2 ATT messages instead of 58 |
| `tools/gencrc/` | lifts the CRC tables out of the decompiled source into Go |
| `tools/genfields/` | turns the field-table JSON into Go |
| `tools/groundtruth/CRCGround.java` | the app's CRC tables, executed on the JVM |
| `tools/groundtruth/AESGround.java` | the app's AES setup, executed on the JVM |
| `tools/groundtruth/FrameGround.java` | the app's frame builder, executed on the JVM |
| `capture/live/*.json` | live **TCP** captures of all five `get` commands (phone bridge) |
| `tools/fsolar-probe.ps1` | queries the battery over TCP through the phone (`adb` + phone-side `nc` pipe) |
| `tools/wifi-connect.ps1` | Windows `WlanConnect` join attempt — **parked**: this wlanapi build rejects it (0x57) |
| `go-fsolar/` | the reimplementation (BLE + TCP) |

The snoop log is worth replaying before trusting any protocol claim:

```
pwsh -NoProfile -File tools/btsnoop_att.ps1
```

It shows the app writing `wifilocalMonitor:get dev basice infor` (37 bytes, ATT
`Write Command`, characteristic handle `0x11`) and the battery answering with
four 244-byte notifications that only make sense once concatenated.
