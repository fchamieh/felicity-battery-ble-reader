//go:build !darwin

package main

import (
	"fmt"

	"tinygo.org/x/bluetooth"
)

// parseAddress turns a scanned device ID into a bluetooth.Address (a MAC
// address on Linux and Windows).
func parseAddress(s string) (bluetooth.Address, error) {
	mac, err := bluetooth.ParseMAC(s)
	if err != nil {
		return bluetooth.Address{}, fmt.Errorf("bad device ID %q: %w", s, err)
	}
	return bluetooth.Address{MACAddress: bluetooth.NewMACAddress(mac, false)}, nil
}
