//go:build darwin

package main

import (
	"fmt"

	"tinygo.org/x/bluetooth"
)

// parseAddress turns the -addr value into a platform address. CoreBluetooth
// identifies peripherals by UUID, not MAC address, so on macOS this is the
// identifier printed by `fsolar scan`.
func parseAddress(s string) (bluetooth.Address, error) {
	u, err := bluetooth.ParseUUID(s)
	if err != nil {
		return bluetooth.Address{}, fmt.Errorf("bad -addr %q: %w (macOS uses the CoreBluetooth UUID shown by `fsolar scan`)", s, err)
	}
	return bluetooth.Address{UUID: u}, nil
}
