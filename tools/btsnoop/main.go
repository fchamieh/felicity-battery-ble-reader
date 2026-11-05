// btsnoop decodes a btsnoop_hci.log capture down to the ATT layer so the
// FSolar battery GATT exchange can be inspected byte-for-byte.
//
// SUPERSEDED by tools/btsnoop_att.ps1. This version assumes a standard HCI ACL
// header (handle + PB flag + length) and never reassembles ACL fragments, but
// Android writes its log with datalink 1002, where the PB flag byte is absent
// and long ATT messages are split over several small ACL packets. It therefore
// reports a handful of ATT messages with invented CIDs. Use the PowerShell
// tool for real numbers; this file is kept only as a record of how the layout
// was worked out.
//
// Usage: go run ./tools/btsnoop capture/btsnoop_hci.log
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
)

type record struct {
	origLen  uint32
	inclLen  uint32
	flags    uint32
	drops    uint32
	ts       int64
	data     []byte
}

func readRecords(path string) ([]record, uint32, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if !bytes.HasPrefix(b, []byte("btsnoop\x00")) {
		return nil, 0, fmt.Errorf("not a btsnoop file")
	}
	version := binary.BigEndian.Uint32(b[8:12])
	dlt := binary.BigEndian.Uint32(b[12:16])
	var out []record
	p := 16
	for p+24 <= len(b) {
		r := record{
			origLen:  binary.BigEndian.Uint32(b[p : p+4]),
			inclLen:  binary.BigEndian.Uint32(b[p+4 : p+8]),
			flags:    binary.BigEndian.Uint32(b[p+8 : p+12]),
			drops:    binary.BigEndian.Uint32(b[p+12 : p+16]),
			ts:       int64(binary.BigEndian.Uint64(b[p+16 : p+24])),
		}
		p += 24
		end := p + int(r.inclLen)
		if end > len(b) {
			break
		}
		r.data = b[p:end]
		p = end
		out = append(out, r)
	}
	_ = version
	return out, dlt, nil
}

// hciType returns the HCI packet indicator (1=cmd 2=acl 4=event) for a record.
// Observed on Android 16 (Xiaomi): btsnoop datalink 1002 still carries the
// H4 indicator byte, so we keep it unless NO_PHDR is set.
func hciType(r record, dlt uint32) (byte, []byte) {
	if os.Getenv("NO_PHDR") != "" || dlt == 1001 {
		return 0, r.data
	}
	if len(r.data) < 1 {
		return 0, nil
	}
	return r.data[0], r.data[1:]
}

var attOpcodes = map[byte]string{
	0x01: "ErrorRsp",
	0x02: "ExchangeMTUReq",
	0x03: "ExchangeMTURsp",
	0x04: "FindInfoReq",
	0x05: "FindInfoRsp",
	0x06: "FindByTypeReq",
	0x07: "FindByTypeRsp",
	0x08: "ReadByTypeReq",
	0x09: "ReadByTypeRsp",
	0x0A: "ReadReq",
	0x0B: "ReadRsp",
	0x12: "WriteCmd",
	0x13: "WriteReq",
	0x15: "PrepareWriteReq",
	0x16: "PrepareWriteRsp",
	0x17: "ExecuteWriteReq",
	0x18: "ExecuteWriteRsp",
	0x1B: "HandleNotify",
	0x1D: "HandleIndicate",
	0x1E: "HandleConfirm",
}

func printable(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	ok := true
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			ok = false
			break
		}
	}
	if ok {
		return "  ascii=\"" + string(b) + "\""
	}
	return ""
}

func uuid16(b []byte) string {
	// ATT transmits 16-bit UUIDs little-endian.
	return fmt.Sprintf("%02X%02X", b[1], b[0])
}

// decode 128-bit UUID attribute value into canonical string.
func uuid128Str(b []byte) string {
	if len(b) != 16 {
		return hex.EncodeToString(b)
	}
	var parts []string
	for i := 15; i >= 0; i-- {
		parts = append(parts, fmt.Sprintf("%02x", b[i]))
	}
	s := strings.Join(parts, "")
	return fmt.Sprintf("%s-%s-%s-%s-%s", s[0:8], s[8:12], s[12:16], s[16:20], s[20:32])
}

func main() {
	path := "capture/btsnoop_hci.log"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	recs, dlt, err := readRecords(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("records=%d datalink=%d\n\n", len(recs), dlt)

	if os.Getenv("PROBE") != "" {
		for i, r := range recs {
			if i >= 25 {
				break
			}
			n := len(r.data)
			if n > 16 {
				n = 16
			}
			fmt.Printf("rec%03d flags=%d incl=%d data=%s\n", i, r.flags, r.inclLen, hex.EncodeToString(r.data[:n]))
		}
		return
	}

	handles := map[uint16]string{}

	// --- census: what is actually in this capture ---
	hciCount := map[byte]int{}
	cidCount := map[uint16]int{}
	conns := map[uint16]string{}
	for _, r := range recs {
		typ, body := hciType(r, dlt)
		hciCount[typ]++
		if typ == 2 && len(body) >= 4 {
			cidCount[binary.LittleEndian.Uint16(body[2:4])]++
		}
		if typ == 4 && len(body) >= 15 && body[1] == 0x03 { // Connection Complete
			h := binary.LittleEndian.Uint16(body[5:7])
			a := body[7:13]
			conns[h] = fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", a[5], a[4], a[3], a[2], a[1], a[0])
		}
	}
	fmt.Println("HCI packet types:")
	for _, t := range []byte{1, 2, 4, 5} {
		if n := hciCount[t]; n > 0 {
			fmt.Printf("  0x%02x : %d\n", t, n)
		}
	}
	fmt.Println("L2CAP CIDs:")
	var cids []int
	for c := range cidCount {
		cids = append(cids, int(c))
	}
	sort.Ints(cids)
	for _, c := range cids {
		fmt.Printf("  0x%04x : %d\n", c, cidCount[uint16(c)])
	}
	fmt.Println("connections:")
	for h, a := range conns {
		fmt.Printf("  handle=0x%04x addr=%s\n", h, a)
	}
	fmt.Println()

	for _, r := range recs {
		dir := "=>"
		if r.flags&0x01 != 0 {
			dir = "<<"
		}
		typ, body := hciType(r, dlt)
		if typ != 2 || len(body) < 4 { // ACL only
			continue
		}
		l2len := binary.LittleEndian.Uint16(body[0:2])
		cid := binary.LittleEndian.Uint16(body[2:4])
		_ = l2len
		if cid != 0x0004 { // not ATT
			continue
		}
		pdu := body[4:]
		if len(pdu) == 0 {
			continue
		}
		op := pdu[0]
		name := attOpcodes[op]
		if name == "" {
			name = fmt.Sprintf("op%02X", op)
		}
		rest := pdu[1:]
		switch op {
		case 0x02, 0x03:
			if len(rest) >= 2 {
				fmt.Printf("%s ATT %-15s mtu=%d\n", dir, name, binary.LittleEndian.Uint16(rest[:2]))
			}
		case 0x01:
			if len(rest) >= 4 {
				fmt.Printf("%s ATT %-15s reqOp=%02x handle=0x%04x err=0x%02x\n", dir, name, rest[0], binary.LittleEndian.Uint16(rest[1:3]), rest[3])
			}
		case 0x0A, 0x12, 0x13, 0x1B, 0x1D:
			if len(rest) >= 2 {
				h := binary.LittleEndian.Uint16(rest[:2])
				val := rest[2:]
				fmt.Printf("%s ATT %-15s handle=0x%04x len=%d hex=%s%s\n", dir, name, h, len(val), hex.EncodeToString(val), printable(val))
			}
		case 0x08, 0x09, 0x04, 0x05:
			fmt.Printf("%s ATT %-15s raw=%s\n", dir, name, hex.EncodeToString(rest))
		default:
			fmt.Printf("%s ATT %-15s raw=%s\n", dir, name, hex.EncodeToString(rest))
		}
	}

	// Print discovered service/characteristic declarations to map handles.
	fmt.Println("\n--- attribute declarations (from Read By Type / Find Information) ---")
	for _, r := range recs {
		if r.flags&0x01 == 0 {
			continue
		}
		typ, body := hciType(r, dlt)
		if typ != 2 || len(body) < 4 {
			continue
		}
		cid := binary.LittleEndian.Uint16(body[2:4])
		if cid != 0x0004 {
			continue
		}
		pdu := body[4:]
		if len(pdu) == 0 || pdu[0] != 0x09 { // Read By Type Response
			continue
		}
		tokenLen := int(pdu[1])
		if tokenLen == 0 || len(pdu) < 2 {
			continue
		}
		for i := 2; i+tokenLen <= len(pdu); i += tokenLen {
			tok := pdu[i : i+tokenLen]
			h := binary.LittleEndian.Uint16(tok[:2])
			u := tok[2:]
			var s string
			switch len(u) {
			case 2:
				s = uuid16(u)
			case 16:
				s = uuid128Str(u)
			default:
				s = hex.EncodeToString(u)
			}
			fmt.Printf("  handle=0x%04x uuid=%s\n", h, s)
			_ = handles
		}
	}
}
