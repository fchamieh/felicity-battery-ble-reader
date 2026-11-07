package main

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"fsolar/pkg/fsolar"
)

// The battery speaks the same text/JSON protocol over both transports
// (PROTOCOL.md §1.4), so everything above the link — scanning aside — is
// shared: one link, one realtime command, one 60 s poll loop.
const (
	TransportBLE = "ble"
	TransportTCP = "tcp"
)

// BatteryDevice is one BLE advertisement discovered during a scan.
type BatteryDevice struct {
	// ID is the platform address string (CoreBluetooth UUID on macOS,
	// typically a MAC on Linux/Windows). Pass it to Connect.
	ID string `json:"id"`
	// Name is the advertised name, e.g. "F<DevSN>".
	Name string `json:"name"`
	// DevSN is the serial with the leading "F" trimmed ("" when unknown).
	DevSN string `json:"devSn"`
	// RSSI is the received signal strength in dBm (negative, higher is better).
	RSSI int16 `json:"rssi"`
	// IsFelicity reports whether the name has the Felicity "F" prefix.
	IsFelicity bool `json:"isFelicity"`
}

// BatteryReading is one decoded realtime snapshot.
type BatteryReading struct {
	SOC           float64 `json:"soc"`
	Voltage       float64 `json:"voltage"`
	Current       float64 `json:"current"`
	CapacityAh    float64 `json:"capacityAh"`
	RemainingKWh  float64 `json:"remainingKWh"`
	DevSN         string  `json:"devSn"`
	Timestamp     string  `json:"timestamp"`
}

// BatteryConnection is the link state snapshot emitted on change.
type BatteryConnection struct {
	Connected bool   `json:"connected"`
	DeviceID  string `json:"deviceId"`
	Error     string `json:"error"`
}

// TransportInfo is the active transport plus its TCP endpoint and link state.
// For Bluetooth the host/port are the remembered TCP settings, kept so the
// frontend can round-trip between transports without losing them.
type TransportInfo struct {
	Mode      string `json:"mode"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Connected bool   `json:"connected"`
	DeviceID  string `json:"deviceId"`
}

func init() {
	application.RegisterEvent[[]BatteryDevice]("battery:devices")
	application.RegisterEvent[BatteryReading]("battery:reading")
	application.RegisterEvent[BatteryConnection]("battery:connection")
	application.RegisterEvent[TransportInfo]("battery:transport")
}

// BatteryService drives one link to the battery — Bluetooth LE or the WiFi
// hotspot's plain TCP socket — and exposes it to the frontend.
type BatteryService struct {
	mu        sync.Mutex
	mode      string // TransportBLE (default) or TransportTCP
	ble       *fsolar.Client
	tcp       *fsolar.TCPClient
	bleReady  bool
	host      string
	port      int
	deviceID  string
	scanning  bool
	pollStop  chan struct{}
	pollDone  chan struct{}
	userClose bool
}

// NewBatteryService returns a service preconfigured for a transport, so the
// app can be launched straight into TCP mode:
//
//	felicity-battery-watch -via tcp -host 127.0.0.1 -port 53970
//
// Bad or empty arguments fall back to the defaults rather than failing.
func NewBatteryService(mode, host string, port int) *BatteryService {
	s := &BatteryService{}
	newMode, newHost, newPort, err := s.planTransport(mode, host, port)
	if err != nil {
		newMode, newHost, newPort = TransportBLE, fsolar.DefaultTCPHost, fsolar.DefaultTCPPort
	}
	s.mode, s.host, s.port = newMode, newHost, newPort
	return s
}

func (s *BatteryService) emit(event string, data any) {
	if app := application.Get(); app != nil {
		app.Event.Emit(event, data)
	}
}

// defaultsLocked normalises unset values. s.mu must be held.
func (s *BatteryService) defaultsLocked() {
	if s.mode != TransportTCP {
		s.mode = TransportBLE
	}
	if s.host == "" {
		s.host = fsolar.DefaultTCPHost
	}
	if s.port == 0 {
		s.port = fsolar.DefaultTCPPort
	}
}

// planTCP validates and fills in a TCP endpoint. Empty host/port keep the
// currently remembered one, which defaults to the app's hard-coded
// 192.168.155.1:53970 (fsolar.DefaultTCPHost / DefaultTCPPort).
func (s *BatteryService) planTCP(host string, port int) (string, int, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		host = s.rememberedHost()
	}
	if port == 0 {
		port = s.rememberedPort()
	}
	if port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("invalid TCP port %d", port)
	}
	return host, port, nil
}

// planTransport validates and fills in a requested transport without mutating
// service state.
func (s *BatteryService) planTransport(mode, host string, port int) (string, string, int, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = TransportBLE
	}
	switch mode {
	case TransportBLE:
		return TransportBLE, strings.TrimSpace(host), port, nil
	case TransportTCP:
		newHost, newPort, err := s.planTCP(host, port)
		if err != nil {
			return "", "", 0, err
		}
		return TransportTCP, newHost, newPort, nil
	default:
		return "", "", 0, fmt.Errorf("unknown transport %q (want ble or tcp)", mode)
	}
}

func (s *BatteryService) rememberedHost() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.host == "" {
		return fsolar.DefaultTCPHost
	}
	return s.host
}

func (s *BatteryService) rememberedPort() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.port == 0 {
		return fsolar.DefaultTCPPort
	}
	return s.port
}

// linkLocked returns the client for the active transport, or nil if that
// transport has never been used. s.mu must be held.
func (s *BatteryService) linkLocked() fsolar.Link {
	switch {
	case s.mode == TransportTCP && s.tcp != nil:
		return s.tcp
	case s.mode != TransportTCP && s.ble != nil:
		return s.ble
	default:
		return nil
	}
}

// GetTransport reports the active transport, its TCP endpoint and link state.
// The frontend calls it on startup to restore the last-used configuration.
func (s *BatteryService) GetTransport() TransportInfo {
	s.mu.Lock()
	s.defaultsLocked()
	info := TransportInfo{
		Mode:     s.mode,
		Host:     s.host,
		Port:     s.port,
		DeviceID: s.deviceID,
	}
	link := s.linkLocked()
	info.Connected = link != nil && link.Connected()
	s.mu.Unlock()
	return info
}

// SetTransport switches between Bluetooth and TCP. host/port are only
// meaningful for TCP; pass "" / 0 to keep the remembered endpoint. Any live
// link is closed first — the battery's TCP stack serves one client at a time,
// and the BLE adapter is happier when the old GATT link is properly torn down.
func (s *BatteryService) SetTransport(mode, host string, port int) error {
	s.mu.Lock()
	if s.scanning {
		s.mu.Unlock()
		return fmt.Errorf("scan in progress: wait for it to finish before switching transport")
	}
	oldLink := s.linkLocked()
	oldID := s.deviceID
	rememberedHost, rememberedPort := s.host, s.port
	live := oldID != "" || (oldLink != nil && oldLink.Connected())
	s.mu.Unlock()

	// Empty arguments keep the remembered endpoint rather than resetting it.
	if strings.TrimSpace(host) == "" {
		host = rememberedHost
	}
	if port == 0 {
		port = rememberedPort
	}

	newMode, newHost, newPort, err := s.planTransport(mode, host, port)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.mode, s.host, s.port = newMode, newHost, newPort
	s.defaultsLocked()
	if live {
		s.userClose = true
		s.deviceID = ""
	}
	s.mu.Unlock()

	if live {
		s.stopPolling()
		var closeErr string
		if oldLink != nil {
			if cerr := oldLink.Close(); cerr != nil {
				closeErr = cerr.Error()
			}
		}
		s.emit("battery:connection", BatteryConnection{
			Connected: false,
			DeviceID:  oldID,
			Error:     closeErr,
		})
	}
	s.emit("battery:transport", s.GetTransport())
	return nil
}

// ensureBLE lazily creates the BLE client and enables the local adapter.
func (s *BatteryService) ensureBLE() (*fsolar.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ble == nil {
		s.ble = fsolar.NewClient()
		s.ble.SetOnDisconnect(s.onLinkClosed)
	}
	if !s.bleReady {
		if err := s.ble.Enable(); err != nil {
			return nil, err
		}
		s.bleReady = true
	}
	s.mode = TransportBLE
	return s.ble, nil
}

// ensureTCP lazily creates the TCP client and points it at host:port.
// It does not dial — that is ConnectTCP's job.
func (s *BatteryService) ensureTCP(host string, port int) (*fsolar.TCPClient, error) {
	newHost, newPort, err := s.planTCP(host, port)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tcp == nil {
		s.tcp = fsolar.NewTCPClient()
		s.tcp.SetOnDisconnect(s.onLinkClosed)
	}
	s.tcp.Host = newHost
	s.tcp.Port = newPort
	s.mode, s.host, s.port = TransportTCP, newHost, newPort
	return s.tcp, nil
}

// onLinkClosed is the disconnect handler both transports report through.
func (s *BatteryService) onLinkClosed(err error) {
	s.mu.Lock()
	// userClose is set by Disconnect/SetTransport so a deliberate close is
	// not reported as a drop.
	userClose := s.userClose
	s.userClose = false
	id := s.deviceID
	s.mu.Unlock()

	s.stopPolling()
	if userClose {
		return
	}
	if err == nil {
		err = fmt.Errorf("connection closed")
	}
	if id != "" {
		s.emit("battery:connection", BatteryConnection{
			Connected: false,
			DeviceID:  id,
			Error:     err.Error(),
		})
	}
}

// Scan discovers BLE devices for durationSec seconds (clamped to 1..30s)
// and returns them sorted Felicity-first, then by RSSI. Incremental
// "battery:devices" events are emitted as new devices appear.
//
// Bluetooth-only: there is nothing to discover on a TCP socket, so in TCP
// mode this explains itself rather than silently doing nothing.
func (s *BatteryService) Scan(durationSec int) ([]BatteryDevice, error) {
	if info := s.GetTransport(); info.Mode == TransportTCP {
		return nil, fmt.Errorf(
			"scanning is Bluetooth-only — over WiFi use Connect (%s:%d)", info.Host, info.Port)
	}
	client, err := s.ensureBLE()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.scanning {
		s.mu.Unlock()
		return nil, fmt.Errorf("scan already in progress")
	}
	if s.deviceID != "" && client.Connected() {
		s.mu.Unlock()
		return nil, fmt.Errorf("connected: disconnect before scanning")
	}
	s.scanning = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.scanning = false
		s.mu.Unlock()
	}()

	if durationSec <= 0 {
		durationSec = 10
	}
	if durationSec > 30 {
		durationSec = 30
	}

	seen := map[string]BatteryDevice{}
	var scanMu sync.Mutex
	emit := func() {
		scanMu.Lock()
		devices := make([]BatteryDevice, 0, len(seen))
		for _, d := range seen {
			devices = append(devices, d)
		}
		scanMu.Unlock()
		sortDevices(devices)
		s.emit("battery:devices", devices)
	}

	err = client.ScanRaw(time.Duration(durationSec)*time.Second, func(d fsolar.Discovered) {
		id := d.Address.String()
		name := d.Name
		devSN := strings.TrimPrefix(name, fsolar.NamePrefix)
		if devSN == name {
			// No "F" prefix — not a recognisable Felicity advertisement.
			devSN = ""
		}
		dev := BatteryDevice{
			ID:         id,
			Name:       name,
			DevSN:      devSN,
			RSSI:       d.RSSI,
			IsFelicity: strings.HasPrefix(name, fsolar.NamePrefix),
		}
		scanMu.Lock()
		existing, ok := seen[id]
		seen[id] = dev
		scanMu.Unlock()
		if !ok || existing.RSSI != dev.RSSI || existing.Name != dev.Name {
			emit()
		}
	})
	if err != nil {
		return nil, err
	}
	scanMu.Lock()
	devices := make([]BatteryDevice, 0, len(seen))
	for _, d := range seen {
		devices = append(devices, d)
	}
	scanMu.Unlock()
	sortDevices(devices)
	s.emit("battery:devices", devices)
	return devices, nil
}

func sortDevices(devices []BatteryDevice) {
	sort.Slice(devices, func(i, j int) bool {
		if devices[i].IsFelicity != devices[j].IsFelicity {
			return devices[i].IsFelicity
		}
		if devices[i].RSSI != devices[j].RSSI {
			return devices[i].RSSI > devices[j].RSSI
		}
		return devices[i].ID < devices[j].ID
	})
}

// Connect links to the Bluetooth device with the given ID and starts the
// 60 s polling loop; the first reading is emitted immediately.
func (s *BatteryService) Connect(deviceID string) error {
	client, err := s.ensureBLE()
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.deviceID == deviceID && client.Connected() {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	addr, err := parseAddress(deviceID)
	if err != nil {
		return err
	}
	if err := client.Connect(addr); err != nil {
		return err
	}
	s.afterConnected(deviceID)
	return nil
}

// ConnectTCP opens the battery's WiFi-hotspot socket (the app's "WiFi local"
// mode) and starts the same 60 s polling loop. host/port default to the
// remembered endpoint — 192.168.155.1:53970 when the PC has joined the
// battery's AP, or 127.0.0.1 when reaching it through a phone/adb bridge
// (see PROTOCOL.md §1.4 and go-fsolar/README.md).
//
// host may be given as "192.168.155.1" or "192.168.155.1:53970"; the port
// argument wins when it is non-zero.
func (s *BatteryService) ConnectTCP(host string, port int) error {
	// Accept the "host:port" form the device list and CLI both use.
	if h, p, err := splitHostPort(host); err == nil {
		if h != "" {
			host = h
		}
		if port == 0 {
			port = p
		}
	}

	client, err := s.ensureTCP(host, port)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.deviceID == client.Target() && client.Connected() {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	if err := client.Connect(); err != nil {
		return err
	}
	s.afterConnected(client.Target())
	return nil
}

// afterConnected records the link, starts the poll loop and pulls the first
// sample. A failed first sample is reported but not fatal: polling retries.
func (s *BatteryService) afterConnected(deviceID string) {
	s.mu.Lock()
	s.deviceID = deviceID
	s.userClose = false
	mode, host, port := s.mode, s.host, s.port
	s.mu.Unlock()

	s.startPolling(60)

	s.emit("battery:connection", BatteryConnection{Connected: true, DeviceID: deviceID})
	s.emit("battery:transport", TransportInfo{
		Mode: mode, Host: host, Port: port, Connected: true, DeviceID: deviceID,
	})

	if reading, err := s.fetchReading(); err == nil {
		s.emit("battery:reading", reading)
	} else {
		s.emit("battery:connection", BatteryConnection{
			Connected: true,
			DeviceID:  deviceID,
			Error:     err.Error(),
		})
	}
}

// Disconnect ends the link and stops polling.
func (s *BatteryService) Disconnect() error {
	s.mu.Lock()
	hasDevice := s.deviceID != ""
	link := s.linkLocked()
	s.userClose = true
	s.mu.Unlock()

	s.stopPolling()

	var err error
	if link != nil {
		// Close returns an error only if the transport's own teardown fails;
		// the link is usually going down anyway. Keep the deviceID so a
		// deliberate close is distinguishable from an unexpected drop, then
		// clear it.
		if cerr := link.Close(); cerr != nil {
			err = cerr
		}
	}
	s.mu.Lock()
	s.deviceID = ""
	s.mu.Unlock()
	if hasDevice || err == nil {
		s.emit("battery:connection", BatteryConnection{Connected: false})
	}
	s.emit("battery:transport", s.GetTransport())
	return err
}

// GetReading fetches one realtime snapshot from the connected battery.
func (s *BatteryService) GetReading() (BatteryReading, error) {
	return s.fetchReading()
}

// GetConnectionStatus reports the current link state.
func (s *BatteryService) GetConnectionStatus() BatteryConnection {
	s.mu.Lock()
	defer s.mu.Unlock()
	link := s.linkLocked()
	connected := link != nil && link.Connected()
	return BatteryConnection{Connected: connected, DeviceID: s.deviceID}
}

// StartPolling starts the periodic read loop if not already running.
func (s *BatteryService) StartPolling(intervalSec int) error {
	if intervalSec <= 0 {
		intervalSec = 60
	}
	s.startPolling(intervalSec)
	return nil
}

// StopPolling stops the periodic read loop.
func (s *BatteryService) StopPolling() {
	s.stopPolling()
}

func (s *BatteryService) fetchReading() (BatteryReading, error) {
	s.mu.Lock()
	link := s.linkLocked()
	s.mu.Unlock()
	if link == nil || !link.Connected() {
		return BatteryReading{}, fmt.Errorf("not connected")
	}
	doc, err := link.Request(fsolar.CmdRealtime, 15*time.Second)
	if err != nil {
		return BatteryReading{}, err
	}
	return parseReadingPayload(doc, time.Now())
}

func (s *BatteryService) startPolling(intervalSec int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopPollingLocked()
	stop := make(chan struct{})
	done := make(chan struct{})
	s.pollStop = stop
	s.pollDone = done
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				reading, err := s.fetchReading()
				if err != nil {
					s.mu.Lock()
					id := s.deviceID
					link := s.linkLocked()
					connected := link != nil && link.Connected()
					s.mu.Unlock()
					s.emit("battery:connection", BatteryConnection{
						Connected: connected,
						DeviceID:  id,
						Error:     err.Error(),
					})
					continue
				}
				s.emit("battery:reading", reading)
			}
		}
	}()
}

func (s *BatteryService) stopPolling() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopPollingLocked()
}

func (s *BatteryService) stopPollingLocked() {
	if s.pollStop != nil {
		select {
		case <-s.pollStop:
			// already closed
		default:
			close(s.pollStop)
		}
		s.pollStop = nil
		s.pollDone = nil
	}
}

// splitHostPort accepts "host", "host:port" and "[::1]:port". A bare host
// yields port 0, meaning "use the remembered/default port".
func splitHostPort(addr string) (string, int, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", 0, fmt.Errorf("empty address")
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		// No port separator — treat the whole thing as a host.
		return addr, 0, nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return host, 0, fmt.Errorf("invalid port %q", portStr)
	}
	return host, port, nil
}

// parseReadingPayload decodes one realtime JSON document into a reading.
// Pack (EMS) values are preferred when the device sends them; otherwise the
// per-battery (BMS) values are used. RemainingKWh = SOC% ÷ 100 × Voltage ×
// Capacity(Ah) ÷ 1000.
func parseReadingPayload(doc string, now time.Time) (BatteryReading, error) {
	obj, err := fsolar.ParseObject(doc)
	if err != nil {
		return BatteryReading{}, fmt.Errorf("decode realtime payload: %w", err)
	}
	values := fsolar.DecodeRealtime(obj)
	byParam := map[string]float64{}
	for _, v := range values {
		if v.Valid && v.Text == "" {
			byParam[v.Field.Param] = v.Number
		}
	}
	pick := func(keys ...string) (float64, bool) {
		for _, k := range keys {
			if v, ok := byParam[k]; ok {
				return v, true
			}
		}
		return 0, false
	}
	soc, socOK := pick("emsSoc", "battSoc")
	voltage, voltOK := pick("emsVoltage", "battVolt")
	capacityAh, capOK := pick("emsCapacity", "battCapacity")
	if !socOK || !voltOK || !capOK {
		return BatteryReading{}, fmt.Errorf("realtime payload missing SOC, voltage or capacity")
	}
	current, _ := pick("emsCurrent", "battCurr")
	status := fsolar.ReadStatus(obj)
	remaining := soc / 100 * voltage * capacityAh / 1000
	return BatteryReading{
		SOC:           soc,
		Voltage:       voltage,
		Current:       current,
		CapacityAh:    capacityAh,
		RemainingKWh:  remaining,
		DevSN:         status.DevSN,
		Timestamp:     now.Format(time.RFC3339),
	}, nil
}

// parseAddress lives in addr_darwin.go / addr_other.go for the platform-correct
// bluetooth.Address construction (UUID on macOS, MAC elsewhere).
