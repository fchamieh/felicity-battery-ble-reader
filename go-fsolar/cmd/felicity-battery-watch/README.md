# Felicity Battery Watch

Cross-platform desktop monitor for Felicity Solar batteries over **Bluetooth LE**.
Scan, tap a battery, and watch pack SOC (%) and remaining energy (kWh) — polled every 60 s.

Built with [Wails v3](https://wails.io) (vanilla JS frontend) on top of the shared
`fsolar/pkg/fsolar` library — the same code as the `fsolar` CLI.

## Run (dev)

```sh
wails3 dev          # hot reload, Vite on :9245
```

## Build (release)

```sh
wails3 build        # native binary in bin/felicity-battery-watch (.app with `wails3 package` on macOS)
```

## Usage

1. Power the Felicity battery within Bluetooth range.
2. **Scan 10s** → Felicity batteries (advertising `F` + DevSN) appear, strongest first.
3. Tap a battery → it connects and shows pack SOC %, remaining kWh, voltage, current and capacity.
4. Readings repeat every 60 s; **Read now** fetches immediately, **Disconnect / Back** returns to scanning.

`Remaining kWh = SOC% ÷ 100 × Voltage(V) × Capacity(Ah) ÷ 1000`.

macOS binaries ask for Bluetooth permission (`NSBluetoothAlwaysUsageDescription`
is already in `build/darwin/Info*.plist`).
