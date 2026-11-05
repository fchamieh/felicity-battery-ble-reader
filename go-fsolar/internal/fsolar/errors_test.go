package fsolar

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestFriendly(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantSub string // must appear in the result
		// unchanged reports that the exact same error value must come back.
		unchanged bool
	}{
		{"nil", nil, "", true},
		{
			"scan: adapter without BLE (the HP-laptop case)",
			errors.New("failed to stop scanning (error code 9)"),
			"does not support Bluetooth Low Energy",
			false,
		},
		{
			"scan: bluetooth switched off",
			errors.New("failed to stop scanning (error code 7)"),
			"switched off",
			false,
		},
		{
			"scan: radio busy",
			errors.New("failed to stop scanning (error code 2)"),
			"another application",
			false,
		},
		{
			"scan: unknown code passes through",
			errors.New("failed to stop scanning (error code 42)"),
			"",
			true,
		},
		{
			"gatt: unreachable",
			errors.New("discover services: operation failed with code 1"),
			"could not be reached",
			false,
		},
		{
			"gatt: protocol error",
			errors.New("operation failed with code 2"),
			"rejected a GATT request",
			false,
		},
		{
			"att: request not supported",
			errors.New("write characteristic: Bluetooth ATT error (code 0x06)"),
			"does not support the requested GATT operation",
			false,
		},
		{
			"att: insufficient encryption",
			errors.New("Bluetooth ATT error (code 0x0F)"),
			"requires link encryption",
			false,
		},
		{
			"win32: no network (0x80070490 = 1168)",
			errors.New("error connecting to device: HRESULT 0x80070490: The specified network name is no longer available."),
			"no longer available",
			false,
		},
		{
			"win32: cancelled (0x80070492 = 1170)",
			errors.New("error connecting to device: HRESULT 0x80070492: The operation was canceled."),
			"cancelled",
			false,
		},
		{
			"win32: no such device (0x800704C9 = 1225)",
			errors.New("HRESULT 0x800704C9"),
			"not present",
			false,
		},
		{
			"non-win32 hresult passes through",
			errors.New("HRESULT 0x80065006"),
			"",
			true,
		},
		{
			"unrelated error passes through",
			errors.New("no device advertising name prefix \"F\" seen in 20s"),
			"",
			true,
		},
		{
			"wrapped chain still matches",
			fmt.Errorf("attempt 1/3: connect B8:3D:FB:C1:95:08: failed to stop scanning (error code 9)"),
			"does not support Bluetooth Low Energy",
			false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := friendly(tc.err)
			if tc.err == nil {
				if got != nil {
					t.Fatalf("friendly(nil) = %v, want nil", got)
				}
				return
			}
			if tc.unchanged && got != tc.err {
				t.Fatalf("friendly(%q) changed the error: %v", tc.err, got)
			}
			if !strings.Contains(got.Error(), tc.wantSub) {
				t.Fatalf("friendly(%q) = %q, want it to contain %q", tc.err, got, tc.wantSub)
			}
			if strings.Count(got.Error(), hintPrefix) > 1 {
				t.Fatalf("friendly(%q) added the hint more than once: %q", tc.err, got)
			}
		})
	}
}

func TestFriendlyIdempotent(t *testing.T) {
	err := errors.New("failed to stop scanning (error code 9)")
	once := friendly(err)
	twice := friendly(once)
	if twice != once {
		t.Fatalf("second friendly() call changed the error:\nfirst:  %v\nsecond: %v", once, twice)
	}
}
