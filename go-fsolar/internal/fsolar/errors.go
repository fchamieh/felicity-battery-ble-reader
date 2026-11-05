package fsolar

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The BLE stack reports failures as bare numeric codes that mean nothing
// without looking up the right enum, e.g.
//
//	failed to stop scanning (error code 9)
//	operation failed with code 2
//	Bluetooth ATT error (code 0x06)
//	HRESULT 0x80070490: The operation was canceled.
//
// friendly() recognizes those strings and appends a short, actionable hint.
// Errors that match no known code are returned unchanged, so the function is
// a no-op on platforms that use different error formats.

const hintPrefix = "\n  hint: "

// codePattern matches one numeric code embedded in an error string.
type codePattern struct {
	re   *regexp.Regexp // capture group 1 is the code
	base int            // number base of the captured code
	// key maps the captured value to a hints key (identity by default), e.g.
	// an HRESULT to its Win32 code.
	key   func(code int) int
	hints map[int]string
}

// bluetoothErrorHints: Windows.Devices.Bluetooth.BluetoothError, reported by
// the advertisement watcher when a scan cannot run or stop.
var bluetoothErrorHints = map[int]string{
	1: "the Bluetooth radio is not available. Turn Bluetooth on (Settings > Bluetooth & devices), or unplug/replug the USB adapter.",
	2: "another application is currently using the Bluetooth radio. Close it (phone/headset pairing apps, Windows Settings) and retry.",
	3: "the Bluetooth device is not connected.",
	4: "the Bluetooth stack reported a failure. Toggle Bluetooth off/on and retry.",
	5: "Bluetooth is disabled by a policy (common on work computers). Ask your IT admin to allow it.",
	6: "this Bluetooth operation is not supported by the adapter.",
	7: "Bluetooth is switched off. Turn it on (Settings > Bluetooth & devices) and retry.",
	8: "Windows is asking for consent for this Bluetooth operation. Look for a system prompt and approve it.",
	9: "the Bluetooth adapter does not support Bluetooth Low Energy (BLE). fsolar needs a Bluetooth 4.0+ radio: if the built-in adapter is BT 3.0/3.1, plug in a USB BT 4.0/5.0 dongle and disable the internal one (Device Manager).",
}

// gattStatusHints: Windows GattCommunicationStatus.
var gattStatusHints = map[int]string{
	1: "the device could not be reached. Is the battery powered on and within a few meters? Retry.",
	2: "the device rejected a GATT request (ProtocolError). This is intermittent on Windows; retry, or move closer.",
	3: "Windows denied GATT access to the device. Make sure no other app holds the connection.",
}

// attErrorHints: Bluetooth Core Spec vol 3, part F, table 3.4 (ATT_ERROR_RSP).
var attErrorHints = map[int]string{
	0x01: "the attribute handle is not valid on the battery (stale discovery; reconnect).",
	0x02: "the battery does not allow reading that attribute.",
	0x03: "the battery does not allow writing that attribute.",
	0x04: "the request packet was malformed.",
	0x05: "the battery requires authentication before this read/write.",
	0x06: "the battery does not support the requested GATT operation.",
	0x07: "the read offset was past the end of the attribute value.",
	0x08: "the battery requires authorization before this read/write.",
	0x09: "too many prepared writes are queued on the battery.",
	0x0A: "no attribute was found in the given handle range (stale discovery; reconnect).",
	0x0B: "the attribute cannot be read with a blob read.",
	0x0C: "the link encryption key is too short for this attribute.",
	0x0D: "the attribute value length is invalid for this operation (payload too big for the negotiated MTU?).",
	0x0E: "the battery hit an unexpected error while handling the request; retry.",
	0x0F: "the battery requires link encryption, which fsolar does not use.",
	0x10: "the attribute group type is not supported.",
	0x11: "the battery has insufficient resources; retry.",
	0x12: "the battery database changed; reconnect and rediscover.",
	0x13: "the value written was not allowed by the battery.",
}

// win32ErrorHints: a small set of Win32 codes that surface inside BLE
// HRESULTs (facility 0x7, lower 16 bits).
var win32ErrorHints = map[int]string{
	5:     "Windows denied Bluetooth access. Check Settings > Privacy > Bluetooth, and close any other app using the radio.",
	9:     "the Bluetooth stack rejected the operation with invalid data. Toggle Bluetooth off/on and retry.",
	1168:  "the Bluetooth network is no longer available (the radio was just turned off, or the adapter changed). Toggle Bluetooth off/on and retry.",
	1170:  "the operation was cancelled, usually because the radio switched off or another app took over. Retry.",
	12002: "the Bluetooth operation timed out. Move closer to the battery and retry.",
	1225:  "the Bluetooth device is not present. Check that the battery is powered on and within range.",
	10060: "the Bluetooth operation timed out. Move closer to the battery and retry.",
}

var codePatterns = []codePattern{
	{
		re:    regexp.MustCompile(`failed to (?:start|stop) scanning \(error code (\d+)\)`),
		base:  10,
		hints: bluetoothErrorHints,
	},
	{
		re:    regexp.MustCompile(`operation failed with code (\d+)`),
		base:  10,
		hints: gattStatusHints,
	},
	{
		re:    regexp.MustCompile(`Bluetooth ATT error \(code 0x([0-9A-Fa-f]+)\)`),
		base:  16,
		hints: attErrorHints,
	},
	{
		re:   regexp.MustCompile(`HRESULT 0x([0-9A-Fa-f]{8})`),
		base: 16,
		key: func(hr int) int {
			if (hr>>16)&0x1FFF == 0x7 { // FACILITY_WIN32
				return hr & 0xFFFF
			}
			return -1
		},
		hints: win32ErrorHints,
	},
}

// friendly annotates err with a hint when it embeds a recognized numeric BLE
// code. It returns err unchanged otherwise, including when a hint is already
// present (so wrapping twice is harmless).
func friendly(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, hintPrefix) {
		return err
	}
	for _, p := range codePatterns {
		m := p.re.FindStringSubmatch(msg)
		if m == nil {
			continue
		}
		code, err := strconv.ParseInt(m[1], p.base, 64)
		if err != nil {
			continue
		}
		key := int(code)
		if p.key != nil {
			key = p.key(key)
		}
		if hint, ok := p.hints[key]; ok {
			return fmt.Errorf("%w%s%s", err, hintPrefix, hint)
		}
	}
	return err
}
