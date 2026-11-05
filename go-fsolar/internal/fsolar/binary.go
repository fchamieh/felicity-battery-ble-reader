package fsolar

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
)

// ---------------------------------------------------------------------------
// The binary protocol used by the "Tianji" / MPPT family (a8.w.PMTB_BLE).
//
// Recovered from:
//   c/c.java              frame base class  -> header, AES-CBC, frame assembly
//   c/b.java              command 0x19      -> "read parameters"
//   c/a.java              command 0x18      -> "write parameters"
//   b/a.java              u16 big-endian helpers + AES/CBC/NoPadding
//   b/b.java              the two CRC-16 tables (see crc_tables.go)
//
// Wire layout (all integers are 16-bit big-endian):
//
//   off  size  meaning
//   0    2     total length field  (encLen + 4 + padding + 2)  == frame length - 2
//   2    2     constant 0x00 0x06
//   4    2     "encrypted length" field: body + 14 for 0x19, body + 16 for 0x18
//   6    1     version, always 0x01
//   7    1     command: 0x18 write params, 0x19 read params
//   8    N     AES-128-CBC(NoPadding) of the plaintext block below
//   8+N  2     CRC-16/MODBUS over bytes [0 .. 8+N)
//
// The total length field is NOT the whole frame length: c/a.java and
// c/b.java both compute it as encLen + 4 + padding + 2, which is exactly two
// bytes short of the frame because the trailing CRC is not counted.
//
// Plaintext block (before encryption), always a whole number of AES blocks:
//
//   "0000000000"  ASCII marker, 10 bytes   (c.c.f6812f)
//   u16           item count
//   u16           byte length of the body            (0x18 only)
//   ...           body
//   0x00...       zero padding so the block is 16-byte aligned
//
// A 0x19 *response* inserts one more byte: the plaintext is
// marker + count + status + params, which is why c/b.java derives the
// parameter region as (encLen - 15) starting at offset 13.
//
// The AES key and IV are hard-coded in the app (c/c.java, b/a.java):
//   key = "sxd_aiot_key_001"   IV = "sxd_aiot_2022_01"
// ---------------------------------------------------------------------------

const (
	binaryKey      = "sxd_aiot_key_001"
	binaryIV       = "sxd_aiot_2022_01"
	binaryMarker   = "0000000000"
	binaryVersion  = 0x01
	cmdWriteParams = 0x18
	cmdReadParams  = 0x19
)

// ErrBadChecksum means the trailing CRC-16 did not match the frame.
var ErrBadChecksum = errors.New("fsolar: binary frame checksum mismatch")

// ErrShortFrame means the frame is too small to even hold a header.
var ErrShortFrame = errors.New("fsolar: frame too short")

// BinaryParam is one numbered parameter of a 0x18 write request.
type BinaryParam struct {
	No    int
	Value []byte
}

// crc16 reproduces b/b.java method a() exactly.
//
// The decompiled loop is, with lo = i11 and hi = b10 (both start at 0xFF):
//
//	idx  = (lo ^ byte) & 0xFF
//	lo   = hi ^ f6356a[idx]      // crcTableA
//	hi   = f6357b[idx]           // crcTableB
//	return (hi << 8) | lo
//
// i.e. crc = (crc >> 8) ^ table16[idx] where table16[i] = f6357b[i]<<8 |
// f6356a[i]. Those are the two halves of the standard CRC-16/MODBUS table, so
// this is CRC-16/MODBUS: poly 0x8005 reflected (0xA001), init 0xFFFF, no
// final XOR. Confirmed by running the app's own Java code (see
// TestCRC16MatchesTheApp, whose vectors came from the JVM).
func crc16(data []byte) uint16 {
	lo := byte(0xFF)
	hi := byte(0xFF)
	for _, b := range data {
		idx := lo ^ b
		nextLo := hi ^ crcTableA[idx]
		nextHi := crcTableB[idx]
		lo = nextLo
		hi = nextHi
	}
	return uint16(hi)<<8 | uint16(lo)
}

func crc16Bytes(data []byte) []byte {
	c := crc16(data)
	return []byte{byte(c >> 8), byte(c)}
}

func u16be(v int) []byte { return []byte{byte(v >> 8), byte(v)} }

func be16(b []byte) int { return int(b[0])<<8 | int(b[1]) }

// padTo16 returns how many zero bytes are needed so that n + padding is a
// whole number of AES blocks; it is written in the same form the app uses.
func padTo16(n int) int { return (16 - n%16) % 16 }

func aesCrypt(data []byte, decrypt bool) ([]byte, error) {
	if len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("fsolar: payload length %d is not a multiple of 16", len(data))
	}
	block, err := aes.NewCipher([]byte(binaryKey))
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(data))
	iv := []byte(binaryIV)
	if decrypt {
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
	} else {
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, data)
	}
	return out, nil
}

// BuildReadRequest encodes a "read these parameter ids" frame (command 0x19),
// mirroring c/b.java method d(int[]).
func BuildReadRequest(ids []int) []byte {
	body := make([]byte, 0, 2*len(ids))
	for _, id := range ids {
		body = append(body, u16be(id)...)
	}
	// c/b.java: padding = (16 - (bodyLen + 12) % 16) % 16, where 12 is the
	// marker (10) plus the count field (2).
	padding := padTo16(len(body) + 12)
	plain := concat(
		[]byte(binaryMarker),
		u16be(len(ids)),
		body,
		make([]byte, padding),
	)
	// c/b.java: encrypted length field = bodyLen + 14.
	return assemble(cmdReadParams, len(body)+14, padding, plain)
}

// BuildWriteRequest encodes a "set these parameters" frame (command 0x18),
// mirroring c/a.java method d(List<Param>). Each parameter is written as
// no(u16) + valueLen(u16) + value, exactly like Param.getBytes().
func BuildWriteRequest(params []BinaryParam) []byte {
	body := make([]byte, 0, 64)
	for _, p := range params {
		body = append(body, u16be(p.No)...)
		body = append(body, u16be(len(p.Value))...)
		body = append(body, p.Value...)
	}
	// c/a.java: padding = (16 - (bodyLen + 14) % 16) % 16; 14 is marker (10)
	// + count (2) + body-length field (2).
	padding := padTo16(len(body) + 14)
	plain := concat(
		[]byte(binaryMarker),
		u16be(len(params)),
		u16be(len(body)),
		body,
		make([]byte, padding),
	)
	// c/a.java: encrypted length field = bodyLen + 16.
	return assemble(cmdWriteParams, len(body)+16, padding, plain)
}

func assemble(cmd byte, encLen, padding int, plain []byte) []byte {
	if len(plain)%16 != 0 {
		// Programming error: the builders always pad to a block boundary.
		panic(fmt.Sprintf("fsolar: plaintext is %d bytes, not block aligned", len(plain)))
	}
	enc, err := aesCrypt(plain, false)
	if err != nil {
		panic(err)
	}
	// c/a.java + c/b.java: total = encLen + 4 + padding + 2.
	total := encLen + 4 + padding + 2
	head := concat(
		u16be(total),
		[]byte{0x00, 0x06},
		u16be(encLen),
		[]byte{binaryVersion},
		[]byte{cmd},
	)
	frame := append(head, enc...)
	return append(frame, crc16Bytes(frame)...)
}

// BinaryFrame is a decoded response.
type BinaryFrame struct {
	TotalLength int
	EncLength   int
	Version     byte
	Command     byte
	Status      byte // 0 == accepted/OK (c/a.java and c/b.java both test [12]==0)
	Payload     []byte
	Params      []BinaryParam
}

// ParseBinaryFrame verifies the checksum, decrypts the body and, for a 0x19
// response, splits it into parameters.
func ParseBinaryFrame(frame []byte) (*BinaryFrame, error) {
	if len(frame) < 11 {
		return nil, ErrShortFrame
	}
	body := frame[:len(frame)-2]
	want := crc16(body)
	got := uint16(frame[len(frame)-2])<<8 | uint16(frame[len(frame)-1])
	if want != got {
		return nil, ErrBadChecksum
	}

	f := &BinaryFrame{
		TotalLength: be16(frame[0:2]),
		EncLength:   be16(frame[4:6]),
		Version:     frame[6],
		Command:     frame[7],
	}

	enc := body[8:]
	plain, err := aesCrypt(enc, true)
	if err != nil {
		return nil, err
	}
	f.Payload = plain
	if len(plain) > 12 {
		f.Status = plain[12]
	}

	if f.Command == cmdReadParams && len(plain) >= 13 {
		n := f.EncLength - 15 // c/b.java: "param region length = field - 15"
		if n < 0 {
			n = 0
		}
		if n > len(plain)-13 {
			n = len(plain) - 13
		}
		region := plain[13 : 13+n]
		for i := 0; i+4 <= len(region); {
			no := be16(region[i : i+2])
			l := be16(region[i+2 : i+4])
			i += 4
			if i+l > len(region) {
				break
			}
			val := make([]byte, l)
			copy(val, region[i:i+l])
			f.Params = append(f.Params, BinaryParam{No: no, Value: val})
			i += l
		}
	}
	return f, nil
}

// FrameHex renders a frame the way the app logs it (upper-case hex).
func FrameHex(b []byte) string {
	const digits = "0123456789ABCDEF"
	out := make([]byte, 0, 2*len(b))
	for _, v := range b {
		out = append(out, digits[v>>4], digits[v&0x0f])
	}
	return string(out)
}

func concat(parts ...[]byte) []byte {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
