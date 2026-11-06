package fsolar

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

// ErrTimeout means the battery accepted the write but never answered.
var ErrTimeout = errors.New("fsolar: timed out waiting for a response")

// ErrNotConnected means the GATT link was lost.
var ErrNotConnected = errors.New("fsolar: not connected")

// Client is a BLE central that speaks the FSolar text protocol.
type Client struct {
	adapter *bluetooth.Adapter
	device  *bluetooth.Device
	tx      *bluetooth.DeviceCharacteristic
	rx      *bluetooth.DeviceCharacteristic

	stream JSONStream

	mu        sync.Mutex
	connected bool

	// responses carries fully reassembled JSON documents (or raw text lines)
	// pushed by the battery.
	responses chan string

	// OnNotify, when set, receives every document as it arrives.
	OnNotify func(doc string)

	// OnDisconnect, when set, is called if the link drops.
	OnDisconnect func(err error)

	// Attempts is how many times Connect retries the whole connect-and-discover
	// sequence. Windows intermittently fails GATT service discovery, so the
	// default is more than one. Zero means one attempt.
	Attempts int
	// RetryDelay is the pause between attempts.
	RetryDelay time.Duration
}

// NewClient returns a client bound to the default system adapter.
func NewClient() *Client {
	return &Client{
		adapter:    bluetooth.DefaultAdapter,
		responses:  make(chan string, 32),
		Attempts:   3,
		RetryDelay: 1500 * time.Millisecond,
	}
}

// Enable turns on the local BLE stack.
func (c *Client) Enable() error {
	if err := c.adapter.Enable(); err != nil {
		return friendly(fmt.Errorf("enable BLE adapter: %w", err))
	}
	c.adapter.SetConnectionStateHandler(func(device bluetooth.Device, state bluetooth.ConnectionState) {
		if state.Connected {
			return
		}
		c.mu.Lock()
		c.connected = false
		c.mu.Unlock()
		c.stream.Reset()
		if c.OnDisconnect != nil {
			c.OnDisconnect(friendly(state.Err))
		}
	})
	return nil
}

// Discovered is an advertisement. Only what the BLE stack gives us at
// callback time is kept: tinygo warns that the advertisement payload is only
// valid until the next event arrives, so strings are copied immediately.
type Discovered struct {
	Address bluetooth.Address
	RSSI    int16
	Name    string
}

// ScanRaw runs an unfiltered scan for the given duration and reports every
// advertisement to fn.
//
// tinygo's Adapter.Scan blocks until StopScan is called (on Windows it waits
// for the WinRT watcher to report that it stopped), so the scan runs on its
// own goroutine and is stopped from here once the deadline passes.
func (c *Client) ScanRaw(d time.Duration, fn func(Discovered)) error {
	type event struct {
		res bluetooth.ScanResult
	}
	adverts := make(chan event, 64)
	failures := make(chan error, 1)
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)
		err := c.adapter.Scan(func(a *bluetooth.Adapter, res bluetooth.ScanResult) {
			select {
			case adverts <- event{res}:
			default:
			}
		})
		if err != nil {
			select {
			case failures <- err:
			default:
			}
		}
	}()

	var scanErr error
	deadline := time.After(d)
loop:
	for {
		select {
		case e := <-adverts:
			// Copy everything we need before returning: the payload is
			// recycled on the next event.
			fn(Discovered{
				Address: e.res.Address,
				RSSI:    e.res.RSSI,
				Name:    e.res.AdvertisementPayload.LocalName(),
			})
		case err := <-failures:
			scanErr = err
			break loop
		case <-deadline:
			break loop
		}
	}

	if err := c.adapter.StopScan(); err != nil && scanErr == nil {
		// Stopping a scan that already ended is not a failure worth reporting.
		_ = err
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
	}
	return friendly(scanErr)
}

// Scan looks for an advertising battery. prefix filters the advertised name
// ("F" + DevSN). It returns the first match or a timeout error.
func (c *Client) Scan(prefix string, d time.Duration) (*Discovered, error) {
	var found *Discovered
	var mu sync.Mutex

	err := c.ScanRaw(d, func(r Discovered) {
		mu.Lock()
		defer mu.Unlock()
		if found != nil {
			return
		}
		if prefix != "" && !hasPrefix(r.Name, prefix) {
			return
		}
		copied := r
		found = &copied
	})

	mu.Lock()
	defer mu.Unlock()
	if found != nil {
		return found, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("no device advertising name prefix %q seen in %s", prefix, d)
}

// Connect opens the GATT link, discovers the FSolar service and subscribes to
// notifications on the ISTX characteristic.
//
// Windows' GATT stack fails service discovery intermittently - in practice the
// first one or two attempts on this battery returned "operation failed with
// code 1" and the next one worked - so discovery is retried on a link that is
// torn down and reopened each time.
func (c *Client) Connect(address bluetooth.Address) error {
	attempts := c.Attempts
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			time.Sleep(c.RetryDelay)
		}
		err := c.connectOnce(address)
		if err == nil {
			return nil
		}
		lastErr = fmt.Errorf("attempt %d/%d: %w", attempt, attempts, err)
		c.resetDevice()
	}
	return friendly(lastErr)
}

// resetDevice drops a half-open link so the next attempt starts clean.
func (c *Client) resetDevice() {
	if c.device != nil {
		_ = c.device.Disconnect()
		c.device = nil
	}
	c.tx = nil
	c.rx = nil
	c.stream.Reset()
	c.Drain()
	c.mu.Lock()
	c.connected = false
	c.mu.Unlock()
}

func (c *Client) connectOnce(address bluetooth.Address) error {
	device, err := c.adapter.Connect(address, bluetooth.ConnectionParams{})
	if err != nil {
		return fmt.Errorf("connect %s: %w", address.String(), err)
	}
	c.device = &device

	services, err := device.DiscoverServices(nil)
	if err != nil {
		return fmt.Errorf("discover services: %w", err)
	}

	wantService := MustUUID(ServiceFCMD)
	wantTx := MustUUID(CharWrite)
	wantRx := MustUUID(CharNotify)

	var foundService *bluetooth.DeviceService
	for i := range services {
		if services[i].UUID() == wantService {
			foundService = &services[i]
			break
		}
	}
	if foundService == nil {
		var seen []string
		for _, s := range services {
			seen = append(seen, hexUUID(s.UUID()))
		}
		return fmt.Errorf("service %s not found; device offered: %v", ServiceFCMD, seen)
	}

	chars, err := foundService.DiscoverCharacteristics(nil)
	if err != nil {
		return fmt.Errorf("discover characteristics: %w", err)
	}
	for _, ch := range chars {
		switch ch.UUID() {
		case wantTx:
			c.tx = &ch
		case wantRx:
			c.rx = &ch
		}
	}
	if c.tx == nil {
		return fmt.Errorf("write characteristic %s not found", CharWrite)
	}
	if c.rx == nil {
		return fmt.Errorf("notify characteristic %s not found", CharNotify)
	}

	// The app writes the CCC descriptor with ENABLE_NOTIFICATION_VALUE (0x0001)
	// and also calls setCharacteristicNotification(true). EnableNotifications
	// performs both.
	if err := c.rx.EnableNotifications(c.handleNotification); err != nil {
		return fmt.Errorf("enable notifications on %s: %w", CharNotify, err)
	}

	c.mu.Lock()
	c.connected = true
	c.mu.Unlock()
	return nil
}

func (c *Client) handleNotification(buf []byte) {
	for _, doc := range c.stream.Feed(buf) {
		if c.OnNotify != nil {
			c.OnNotify(doc)
		}
		select {
		case c.responses <- doc:
		default:
			// Nobody draining the inbox: drop the oldest so new data gets in.
			select {
			case <-c.responses:
			default:
			}
			select {
			case c.responses <- doc:
			default:
			}
		}
	}
}

// Connected reports whether the GATT link is up.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// Send writes a command to the battery. It mirrors the app, which uses a
// single writeCharacteristic call with the whole command (MTU 247 is plenty:
// the longest command is well under 100 bytes).
func (c *Client) Send(cmd string) error {
	if !c.Connected() {
		return ErrNotConnected
	}
	data := []byte(cmd)
	if _, err := c.tx.Write(data); err != nil {
		// Fall back to a write-without-response, which some firmwares prefer.
		if _, err2 := c.tx.WriteWithoutResponse(data); err2 != nil {
			return friendly(fmt.Errorf("write command: %v (fallback: %v)", err, err2))
		}
	}
	return nil
}

// Request sends a command and returns the next JSON document the battery sends
// back. Stale documents left over from an earlier command are discarded first.
func (c *Client) Request(cmd string, timeout time.Duration) (string, error) {
	c.Drain()
	if err := c.Send(cmd); err != nil {
		return "", err
	}
	select {
	case doc := <-c.responses:
		return doc, nil
	case <-time.After(timeout):
		return "", ErrTimeout
	}
}

// Drain discards any buffered responses.
func (c *Client) Drain() {
	for {
		select {
		case <-c.responses:
		default:
			return
		}
	}
}

// SetOnNotify installs the callback that receives every reassembled document
// as it arrives (Link method; the OnNotify field remains settable directly).
func (c *Client) SetOnNotify(fn func(doc string)) { c.OnNotify = fn }

// SetOnDisconnect installs the link-loss callback (Link method).
func (c *Client) SetOnDisconnect(fn func(err error)) { c.OnDisconnect = fn }

// Close ends the connection.
func (c *Client) Close() error {
	if c.device != nil {
		if err := c.device.Disconnect(); err != nil {
			return friendly(err)
		}
	}
	c.mu.Lock()
	c.connected = false
	c.mu.Unlock()
	return nil
}

// --- helpers ---------------------------------------------------------------

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func hexUUID(u bluetooth.UUID) string {
	b := u.BytesBigEndian()
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, v := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out = append(out, '-')
		}
		out = append(out, hexDigits[v>>4], hexDigits[v&0x0f])
	}
	return string(out)
}
