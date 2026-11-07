package main

import (
	"runtime"
	"testing"
	"time"

	"fsolar/pkg/fsolar"
)

func TestParseReadingPayload(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	// Captured shape (model.go): BatsocList [[7600 → 76.00% SOC, 250000 → 250.000 Ah]],
	// BattList [[53060 → 53.060 V, ...], [-87 → -8.7 A, ...]].
	doc := `{"DevSN":"072604825024250800","BatsocList":[[7600,1000,250000]],"BattList":[[53060,65535],[-87,-1]]}`
	r, err := parseReadingPayload(doc, now)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r.SOC != 76.00 {
		t.Errorf("SOC = %v, want 76.00", r.SOC)
	}
	if r.Voltage != 53.060 {
		t.Errorf("Voltage = %v, want 53.060", r.Voltage)
	}
	if r.CapacityAh != 250.000 {
		t.Errorf("CapacityAh = %v, want 250.000", r.CapacityAh)
	}
	want := 76.00 / 100 * 53.060 * 250.000 / 1000
	if r.RemainingKWh != want {
		t.Errorf("RemainingKWh = %v, want %v", r.RemainingKWh, want)
	}
	if r.Current != -8.7 {
		t.Errorf("Current = %v, want -8.7", r.Current)
	}
	if r.DevSN != "072604825024250800" {
		t.Errorf("DevSN = %q", r.DevSN)
	}
	if r.Timestamp != now.Format(time.RFC3339) {
		t.Errorf("Timestamp = %q", r.Timestamp)
	}
}

func TestParseAddress(t *testing.T) {
	if runtime.GOOS == "darwin" {
		if _, err := parseAddress("00000000-0000-0000-0000-000000000000"); err != nil {
			t.Errorf("UUID: %v", err)
		}
	} else {
		if _, err := parseAddress("B8:3D:FB:C1:95:08"); err != nil {
			t.Errorf("MAC: %v", err)
		}
	}
	if _, err := parseAddress("not-an-address"); err == nil {
		t.Error("expected error for garbage")
	}
}

func TestNewBatteryServiceDefaults(t *testing.T) {
	s := NewBatteryService("", "", 0)
	info := s.GetTransport()
	if info.Mode != TransportBLE {
		t.Errorf("Mode = %q, want %q", info.Mode, TransportBLE)
	}
	if info.Host != fsolar.DefaultTCPHost || info.Port != fsolar.DefaultTCPPort {
		t.Errorf("endpoint = %s:%d, want %s:%d",
			info.Host, info.Port, fsolar.DefaultTCPHost, fsolar.DefaultTCPPort)
	}
	if info.Connected {
		t.Error("a fresh service must not report a connection")
	}
}

func TestNewBatteryServiceTCP(t *testing.T) {
	s := NewBatteryService("TCP", "127.0.0.1", 53970)
	info := s.GetTransport()
	if info.Mode != TransportTCP || info.Host != "127.0.0.1" || info.Port != 53970 {
		t.Errorf("GetTransport() = %+v, want tcp 127.0.0.1:53970", info)
	}
}

func TestSetTransportRemembersEndpoint(t *testing.T) {
	s := NewBatteryService("tcp", "127.0.0.1", 53971)
	if err := s.SetTransport("ble", "", 0); err != nil {
		t.Fatalf("SetTransport(ble): %v", err)
	}
	info := s.GetTransport()
	if info.Mode != TransportBLE {
		t.Errorf("Mode = %q, want %q", info.Mode, TransportBLE)
	}
	// Switching to Bluetooth must not forget the TCP endpoint.
	if info.Host != "127.0.0.1" || info.Port != 53971 {
		t.Errorf("endpoint = %s:%d, want 127.0.0.1:53971", info.Host, info.Port)
	}
	if err := s.SetTransport("tcp", "", 0); err != nil {
		t.Fatalf("SetTransport(tcp): %v", err)
	}
	info = s.GetTransport()
	if info.Mode != TransportTCP || info.Host != "127.0.0.1" || info.Port != 53971 {
		t.Errorf("GetTransport() = %+v, want tcp 127.0.0.1:53971", info)
	}
}

func TestSetTransportRejectsBadInput(t *testing.T) {
	s := &BatteryService{}
	if err := s.SetTransport("carrier-pigeon", "", 0); err == nil {
		t.Error("expected error for an unknown transport")
	}
	if err := s.SetTransport("tcp", "127.0.0.1", 70000); err == nil {
		t.Error("expected error for an out-of-range port")
	}
	// A rejected change must leave the previous transport in place.
	if info := s.GetTransport(); info.Mode != TransportBLE {
		t.Errorf("Mode = %q after a rejected change, want %q", info.Mode, TransportBLE)
	}
}

func TestScanIsBluetoothOnly(t *testing.T) {
	s := NewBatteryService("tcp", "127.0.0.1", 53970)
	if _, err := s.Scan(1); err == nil {
		t.Error("expected Scan to refuse in TCP mode")
	}
}

func TestSplitHostPort(t *testing.T) {
	cases := []struct {
		in     string
		wantH  string
		wantP  int
		wantOK bool
	}{
		{"192.168.155.1", "192.168.155.1", 0, true},
		{"127.0.0.1:53970", "127.0.0.1", 53970, true},
		{"[::1]:53970", "::1", 53970, true},
		{"host:not-a-port", "host", 0, false},
		{"", "", 0, false},
	}
	for _, c := range cases {
		host, port, err := splitHostPort(c.in)
		if (err == nil) != c.wantOK {
			t.Errorf("splitHostPort(%q): err = %v, wantOK = %v", c.in, err, c.wantOK)
			continue
		}
		if host != c.wantH || port != c.wantP {
			t.Errorf("splitHostPort(%q) = %q/%d, want %q/%d", c.in, host, port, c.wantH, c.wantP)
		}
	}
}
