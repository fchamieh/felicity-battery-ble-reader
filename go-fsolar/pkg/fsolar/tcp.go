package fsolar

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

// WiFi local mode: the endpoint the app hard-codes (d8/g.java fields V/W).
// The battery's hotspot is a DHCP server at 192.168.155.1; clients get
// 192.168.155.2/24 and so on (PROTOCOL.md §1.4).
const (
	DefaultTCPHost = "192.168.155.1"
	DefaultTCPPort = 53970

	// dialTimeout bounds a single TCP connect attempt.
	dialTimeout = 5 * time.Second
)

// TCPClient speaks the FSolar text protocol over a plain TCP socket — the
// battery's WiFi hotspot mode. It mirrors the BLE Client one-for-one:
// Connect opens one long-lived socket (the app's pattern, e8/d.java), a
// reader goroutine reassembles JSON documents with the same JSONStream, and
// Send/Request/Drain behave exactly like their BLE counterparts.
//
// The app re-sends every monitor command every second and pings the link with
// TCP urgent data 0xFF every five seconds; live testing shows neither is
// needed to get replies, so TCPClient sends nothing until told to.
type TCPClient struct {
	// Host and Port of the battery's monitor socket. Defaults to the
	// endpoint the app hard-codes. Point them at 127.0.0.1 to use a
	// phone/adb bridge (see PROTOCOL.md §1.4) instead of joining the AP.
	Host string
	Port int

	// Attempts / RetryDelay mirror the BLE client: Windows and cheap
	// APs both enjoy dropping the first connect.
	Attempts   int
	RetryDelay time.Duration

	stream    JSONStream
	responses chan string

	mu        sync.Mutex
	conn      net.Conn
	connected bool

	onNotify     func(doc string)
	onDisconnect func(error)

	// closed is latched by Close so the reader goroutine's teardown does
	// not report a self-inflicted disconnect.
	closed bool
}

// NewTCPClient returns a client aimed at the app's default endpoint.
func NewTCPClient() *TCPClient {
	return &TCPClient{
		Host:       DefaultTCPHost,
		Port:       DefaultTCPPort,
		Attempts:   3,
		RetryDelay: 1500 * time.Millisecond,
		responses:  make(chan string, 32),
	}
}

// Enable is a no-op for TCP; it exists so Link users can treat both
// transports uniformly.
func (c *TCPClient) Enable() error { return nil }

func (c *TCPClient) SetOnNotify(fn func(doc string)) { c.onNotify = fn }
func (c *TCPClient) SetOnDisconnect(fn func(error))  { c.onDisconnect = fn }

// Target is the "host:port" endpoint the client dials, in net.JoinHostPort
// form ("[::1]:53970" for IPv6). Used as the TCP link's identity.
func (c *TCPClient) Target() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// Connected reports whether the socket is up.
func (c *TCPClient) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// Connect opens the socket, with the same retry policy as the BLE client.
// Connecting a client that is already connected closes the old link first.
func (c *TCPClient) Connect() error {
	if c.Connected() {
		_ = c.Close()
	}
	attempts := c.Attempts
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			time.Sleep(c.RetryDelay)
		}
		err := c.connectOnce()
		if err == nil {
			return nil
		}
		lastErr = fmt.Errorf("attempt %d/%d: %w", attempt, attempts, err)
	}
	return friendly(lastErr)
}

func (c *TCPClient) connectOnce() error {
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.Dial("tcp", net.JoinHostPort(c.Host, strconv.Itoa(c.Port)))
	if err != nil {
		return fmt.Errorf("connect %s:%d: %w", c.Host, c.Port, err)
	}

	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.mu.Unlock()
	c.stream.Reset()
	c.Drain()

	go c.readLoop()
	return nil
}

// readLoop pumps the socket into the JSONStream until the link ends.
func (c *TCPClient) readLoop() {
	conn := c.currentConn()
	if conn == nil {
		return
	}
	reader := bufio.NewReaderSize(conn, 64*1024)
	buf := make([]byte, 64*1024)

	var linkErr error
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			c.handleData(buf[:n])
		}
		if err != nil {
			if err != io.EOF {
				linkErr = err
			}
			break
		}
	}
	c.teardown(linkErr)
}

func (c *TCPClient) currentConn() net.Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn
}

// teardown marks the link down and reports an unexpected drop once.
func (c *TCPClient) teardown(err error) {
	c.mu.Lock()
	wasConnected := c.connected
	c.connected = false
	closed := c.closed
	c.mu.Unlock()

	c.stream.Reset()
	if closed || !wasConnected {
		return
	}
	if c.onDisconnect != nil {
		if err == nil {
			err = fmt.Errorf("fsolar: battery closed the connection")
		}
		c.onDisconnect(err)
	}
}

// handleData is the TCP twin of the BLE notification handler: feed the
// stream, fan out each complete document to OnNotify and the inbox.
func (c *TCPClient) handleData(data []byte) {
	for _, doc := range c.stream.Feed(data) {
		if c.onNotify != nil {
			c.onNotify(doc)
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

// Send writes a command to the socket. Like the BLE write, the whole command
// goes out in one call — the longest one is 37 bytes.
func (c *TCPClient) Send(cmd string) error {
	conn := c.currentConn()
	if conn == nil {
		return ErrNotConnected
	}
	if _, err := conn.Write([]byte(cmd)); err != nil {
		return fmt.Errorf("write command: %w", err)
	}
	return nil
}

// Request sends a command and returns the next JSON document, discarding
// stale documents first. Same contract as the BLE Client.
func (c *TCPClient) Request(cmd string, timeout time.Duration) (string, error) {
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
func (c *TCPClient) Drain() {
	for {
		select {
		case <-c.responses:
		default:
			return
		}
	}
}

// Close tears the link down. Safe to call twice and after a failed Connect.
func (c *TCPClient) Close() error {
	c.mu.Lock()
	c.closed = true
	conn := c.conn
	c.conn = nil
	c.connected = false
	c.mu.Unlock()

	c.stream.Reset()
	if conn == nil {
		return nil
	}
	return conn.Close()
}
