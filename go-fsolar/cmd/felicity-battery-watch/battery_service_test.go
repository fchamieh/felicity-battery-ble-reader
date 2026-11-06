package main

import (
	"runtime"
	"testing"
	"time"
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
