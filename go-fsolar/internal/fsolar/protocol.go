// Package fsolar replicates the Bluetooth LE link used by the Felicity Solar
// ("FSolar") Android app to talk to a Felicity battery/BMS.
//
// Everything here was recovered from the app itself (jadx decompilation of
// FSolar 4.1.1, package com.felicity.solar) plus a live capture of the
// commands and responses it exchanged with a real battery. See PROTOCOL.md.
package fsolar

import (
	"fmt"
	"strings"

	"tinygo.org/x/bluetooth"
)

// ---------------------------------------------------------------------------
// GATT layout (c8/u.java, field names UUID_SERVICE / UUID_WRITE / ...)
// ---------------------------------------------------------------------------

const (
	// ServiceFCMD = "nosj"+"FCMD". The service every Felicity battery exposes.
	ServiceFCMD = "6e6f736a-4643-4d44-8fa9-0fafd005e455"

	// ISRX: host -> battery. Plain UTF-8 command text.
	CharWrite = "49535258-184d-4bd9-bc61-20c647249616"

	// ISTX: battery -> host. Notifications carrying UTF-8 JSON.
	CharNotify = "49535458-8341-43f4-a9d4-ec0e34729bb3"

	// "Upper" channel used for user/account traffic on some firmwares.
	CharWriteUpper  = "49536108-184d-4bd9-bc61-20c647249616"
	CharNotifyUpper = "49536008-8341-43f4-a9d4-ec0e34729bb3"

	// Client Characteristic Configuration descriptor (written as 0x0001).
	DescriptorCCC = "00002902-0000-1000-8000-00805f9b34fb"

	// Legacy MPPT stick ("Tianji" devices): binary 0x18/0x19 protocol instead
	// of JSON text (a8.w.PMTB_BLE in the app).
	ServiceMPPT    = "000000ff-0000-1000-8000-00805f9b34fb"
	CharMPPTWrite  = "0000ff03-0000-1000-8000-00805f9b34fb"
	CharMPPTNotify = "0000ff01-0000-1000-8000-00805f9b34fb"
)

// MustUUID parses a UUID string and panics on failure (all callers use
// compile-time constants, so a failure is a programming error).
func MustUUID(s string) bluetooth.UUID {
	u, err := bluetooth.ParseUUID(s)
	if err != nil {
		panic(fmt.Sprintf("bad UUID %q: %v", s, err))
	}
	return u
}

// ---------------------------------------------------------------------------
// Text protocol commands (a8/r.java "IModuleCommManagerImpl")
// ---------------------------------------------------------------------------

const (
	CmdBaseInfo = "wifilocalMonitor:get dev basice infor"
	CmdDate     = "wifilocalMonitor:get Date"
	CmdRealtime = "wifilocalMonitor:get dev real infor"
	CmdSettings = "wifilocalMonitor:get dev set infor"
	CmdSelfTest = "wifilocalMonitor:get dev test infor"
	CmdNetMode  = "wifiGet mode"
	CmdDebugKey = "wifiGet debug"
)

// SetCommand builds a parameter-write command: wifilocalMonitor:set cmd={...}
func SetCommand(paramJSON string) string {
	return "wifilocalMonitor:set cmd=" + paramJSON
}

// AdvertisedName is how the battery advertises itself: "F" + <DevSN>.
// Observed: MAC B8:3D:FB:C1:95:08 advertised "F072604825024250800".
func AdvertisedName(devSN string) string {
	return "F" + devSN
}

// NamePrefix is the advertisement-name prefix used to find batteries.
const NamePrefix = "F"

// ---------------------------------------------------------------------------
// JSON notification reassembly
// ---------------------------------------------------------------------------

// JSONStream reassembles complete JSON documents out of a stream of BLE
// notifications. The battery splits one JSON payload over several
// notification packets, so a per-packet json.Unmarshal never works.
//
// The Android app solves this the same way (c8/u.java keeps a StringBuffer,
// waits for a matching '{' ... '}'), but its brace handling is naive; this
// version also understands arrays, strings and escapes.
type JSONStream struct {
	buf []byte
}

// maxBuffer guards against an endless pile-up if the peer never closes a brace.
const maxBuffer = 256 * 1024

// Feed appends one notification and returns every complete JSON document that
// is now available. Anything incomplete stays buffered for the next Feed.
func (s *JSONStream) Feed(data []byte) []string {
	s.buf = append(s.buf, data...)

	var docs []string
	for {
		start := indexFirstOpen(s.buf)
		if start < 0 {
			// No JSON started yet: drop leading noise, keep the tail.
			s.buf = s.buf[:0]
			break
		}
		end, ok := findDocumentEnd(s.buf, start)
		if !ok {
			// Incomplete: discard garbage before the opening brace and wait.
			s.buf = s.buf[start:]
			break
		}
		docs = append(docs, string(s.buf[start:end+1]))
		s.buf = s.buf[end+1:]
	}

	if len(s.buf) > maxBuffer {
		s.buf = s.buf[:0]
	}
	return docs
}

// Pending reports bytes buffered so far that have not formed a full document.
func (s *JSONStream) Pending() string { return string(s.buf) }

// Reset drops any partial document (call it after a reconnect).
func (s *JSONStream) Reset() { s.buf = s.buf[:0] }

func indexFirstOpen(b []byte) int {
	for i, c := range b {
		if c == '{' || c == '[' {
			return i
		}
	}
	return -1
}

// findDocumentEnd returns the index of the closing bracket that balances
// b[start], or ok=false when the document is still incomplete.
func findDocumentEnd(b []byte, start int) (int, bool) {
	depth := 0
	inString := false
	escaped := false

	for i := start; i < len(b); i++ {
		c := b[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			if depth == 0 {
				return i, false // unbalanced stray closer
			}
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

// IsJSONObject reports whether a reassembled document is an object (the app
// also receives bare arrays and plain text such as "get srv ok:").
func IsJSONObject(doc string) bool {
	t := strings.TrimSpace(doc)
	return strings.HasPrefix(t, "{")
}
