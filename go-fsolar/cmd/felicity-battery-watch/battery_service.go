package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"fsolar/pkg/fsolar"
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

func init() {
	application.RegisterEvent[[]BatteryDevice]("battery:devices")
	application.RegisterEvent[BatteryReading]("battery:reading")
	application.RegisterEvent[BatteryConnection]("battery:connection")
}

// BatteryService wraps the BLE client and exposes it to the frontend.
type BatteryService struct {
	mu        sync.Mutex
	client    *fsolar.Client
	enabled   bool
	deviceID  string
	scanning  bool
	pollStop  chan struct{}
	pollDone  chan struct{}
	userClose bool
}

func (s *BatteryService) emit(event string, data any) {
	if app := application.Get(); app != nil {
		app.Event.Emit(event, data)
	}
}

func (s *BatteryService) ensureClient() (*fsolar.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		s.client = fsolar.NewClient()
	}
	if s.enabled {
		return s.client, nil
	}
	if err := s.client.Enable(); err != nil {
		return nil, err
	}
	s.client.SetOnDisconnect(func(err error) {
		s.mu.Lock()
		wasConnected := s.client != nil && s.client.Connected() == false
		// client.Connected() is already false when OnDisconnect fires;
		// treat any firing while we hold a deviceID as an unexpected drop
		// unless the user asked to disconnect.
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
		// Only surface the drop if we thought we were connected.
		if id != "" || !wasConnected {
			s.emit("battery:connection", BatteryConnection{
				Connected: false,
				DeviceID:  id,
				Error:     err.Error(),
			})
		}
	})
	s.enabled = true
	return s.client, nil
}

// Scan discovers BLE devices for durationSec seconds (clamped to 1..30s)
// and returns them sorted Felicity-first, then by RSSI. Incremental
// "battery:devices" events are emitted as new devices appear.
func (s *BatteryService) Scan(durationSec int) ([]BatteryDevice, error) {
	client, err := s.ensureClient()
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

// Connect links to the device with the given ID and starts the 60 s
// polling loop; the first reading is emitted immediately.
func (s *BatteryService) Connect(deviceID string) error {
	client, err := s.ensureClient()
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
	s.mu.Lock()
	s.deviceID = deviceID
	s.userClose = false
	s.mu.Unlock()

	s.startPolling(60)

	s.emit("battery:connection", BatteryConnection{Connected: true, DeviceID: deviceID})

	// Best-effort first sample; polling will retry if this fails.
	if reading, err := s.fetchReading(); err == nil {
		s.emit("battery:reading", reading)
	} else {
		s.emit("battery:connection", BatteryConnection{
			Connected: true,
			DeviceID:  deviceID,
			Error:     err.Error(),
		})
	}
	return nil
}

// Disconnect ends the link and stops polling.
func (s *BatteryService) Disconnect() error {
	s.mu.Lock()
	hasDevice := s.deviceID != ""
	client := s.client
	s.userClose = true
	s.mu.Unlock()

	s.stopPolling()

	var err error
	if client != nil {
		// Close returns an error only if Disconnect fails; the link is
		// usually going down anyway. Keep the deviceID so a deliberate
		// close is distinguishable from an unexpected drop, then clear it.
		if cerr := client.Close(); cerr != nil {
			err = cerr
		}
	}
	s.mu.Lock()
	s.deviceID = ""
	s.mu.Unlock()
	if hasDevice || err == nil {
		s.emit("battery:connection", BatteryConnection{Connected: false})
	}
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
	connected := s.client != nil && s.client.Connected()
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
	client := s.client
	s.mu.Unlock()
	if client == nil || !client.Connected() {
		return BatteryReading{}, fmt.Errorf("not connected")
	}
	doc, err := client.Request(fsolar.CmdRealtime, 15*time.Second)
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
					client := s.client
					connected := client != nil && client.Connected()
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
