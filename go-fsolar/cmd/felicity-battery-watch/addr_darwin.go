//go:build darwin

package main

import (
	"fmt"

	"tinygo.org/x/bluetooth"
)

// parseAddress turns a scanned device ID into a bluetooth.Address. On macOS
// CoreBluetooth identifies peripherals by UUID, not MAC.
func parseAddress(s string) (bluetooth.Address, error) {
	u, err := bluetooth.ParseUUID(s)
	if err != nil {
		return bluetooth.Address{}, fmt.Errorf("bad device ID %q: %w (macOS uses the CoreBluetooth UUID shown by scan)", s, err)
	}
	return bluetooth.Address{UUID: u}, nil
}
