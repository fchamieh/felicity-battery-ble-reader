package fsolar

import "time"

// Link is the transport-agnostic view of a connection to the battery.
// Both the BLE Client and the TCPClient implement it, so the CLI drives
// identical commands (info, real, watch, send, set) over either transport.
//
// The battery speaks the same text/JSON protocol on both links (PROTOCOL.md
// §1.4): raw UTF-8 commands out, unframed JSON documents back. What differs
// is only the pipe underneath.
type Link interface {
	// Enable prepares the local side of the transport. A no-op for TCP.
	Enable() error

	// Connected reports whether the link to the battery is up.
	Connected() bool

	// Send writes a raw command. It does not wait for a reply.
	Send(cmd string) error

	// Request sends a command and returns the next complete JSON document.
	// Stale documents from earlier commands are discarded first.
	Request(cmd string, timeout time.Duration) (string, error)

	// Drain discards buffered replies.
	Drain()

	// Close releases the link.
	Close() error

	// SetOnNotify installs the callback that receives every reassembled
	// document as it arrives, including the one Request() returns.
	SetOnNotify(fn func(doc string))

	// SetOnDisconnect installs the callback invoked when the link drops.
	SetOnDisconnect(fn func(err error))
}

// compile-time checks: both transports must satisfy Link.
var (
	_ Link = (*Client)(nil)
	_ Link = (*TCPClient)(nil)
)
