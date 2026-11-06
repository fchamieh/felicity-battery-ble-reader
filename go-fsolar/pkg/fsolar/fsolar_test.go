package fsolar

import (
	"bytes"
	"strings"
	"testing"
)

// --- CRC ------------------------------------------------------------------

// Ground truth obtained by *running the app's own decompiled CRC code*
// (b/b.java method a) on the JVM, so these vectors are what the battery
// actually expects.
var crcVectors = []struct {
	in   string
	want uint16
}{
	{"", 0xFFFF},
	{"A", 0x707F},
	{"1234", 0x30BA},
	{"123456789", 0x4B37}, // the published CRC-16/MODBUS check value
}

func TestCRC16MatchesTheApp(t *testing.T) {
	for _, v := range crcVectors {
		if got := crc16([]byte(v.in)); got != v.want {
			t.Errorf("crc16(%q) = 0x%04X, want 0x%04X", v.in, got, v.want)
		}
	}
}

// The app never names its checksum; it only carries two 256-byte tables.
// This proves those tables are the standard CRC-16/MODBUS implementation
// (poly 0x8005 reflected = 0xA001, init 0xFFFF, no final XOR), which is why
// the published check value 0x4B37 for "123456789" reproduces exactly.
func TestCRC16IsStandardMODBUS(t *testing.T) {
	modbus := func(data []byte) uint16 {
		crc := uint16(0xFFFF)
		for _, b := range data {
			crc ^= uint16(b)
			for i := 0; i < 8; i++ {
				if crc&1 != 0 {
					crc = (crc >> 1) ^ 0xA001
				} else {
					crc >>= 1
				}
			}
		}
		return crc
	}

	if got := modbus([]byte("123456789")); got != 0x4B37 {
		t.Fatalf("reference CRC-16/MODBUS check value = 0x%04X, want 0x4B37", got)
	}

	// Exhaustive comparison over byte patterns that stress every table entry.
	for seed := 0; seed < 512; seed++ {
		var data []byte
		n := seed%37 + 1
		for i := 0; i < n; i++ {
			data = append(data, byte((seed*31+i*17)&0xFF))
		}
		if a, b := crc16(data), modbus(data); a != b {
			t.Fatalf("table version 0x%04X != MODBUS 0x%04X for % X", a, b, data)
		}
	}
	for i := 0; i < 256; i++ {
		data := []byte{byte(i)}
		if a, b := crc16(data), modbus(data); a != b {
			t.Fatalf("single byte 0x%02X: table 0x%04X != MODBUS 0x%04X", i, a, b)
		}
	}
}

func TestCRC16BigEndianEncoding(t *testing.T) {
	// b/b.java returns (high << 8) | low, and c/c.java appends [high, low].
	got := crc16Bytes([]byte("123456789"))
	want := []byte{0x4B, 0x37}
	if !bytes.Equal(got, want) {
		t.Fatalf("crc16Bytes = % X, want % X", got, want)
	}
}

// --- AES ------------------------------------------------------------------

func TestAesKeyAndIvRoundTrip(t *testing.T) {
	// b/a.java: SecretKeySpec("sxd_aiot_key_001", "AES") +
	//           IvParameterSpec("sxd_aiot_2022_01") + "AES/CBC/NoPadding".
	plain := make([]byte, 32)
	for i := range plain {
		plain[i] = byte(i)
	}
	enc, err := aesCrypt(plain, false)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(enc, plain) {
		t.Fatal("encryption produced identity")
	}
	dec, err := aesCrypt(enc, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dec, plain) {
		t.Fatalf("round trip mismatch:\n got % X\nwant % X", dec, plain)
	}
}

// TestAesKnownVector pins the cipher to what the app's own JVM produces
// (AESGround.java): AES/CBC/NoPadding of 16 zero bytes.
func TestAesKnownVector(t *testing.T) {
	enc, err := aesCrypt(make([]byte, 16), false)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.ToUpper(hexOf(enc))
	const want = "9B52A0261A13E81D9654A6835E374E8F"
	if got != want {
		t.Fatalf("AES vector = %s, want %s", got, want)
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 2*len(b))
	for _, v := range b {
		out = append(out, digits[v>>4], digits[v&0x0f])
	}
	return string(out)
}

func TestAesRejectsUnpadded(t *testing.T) {
	if _, err := aesCrypt(make([]byte, 15), false); err == nil {
		t.Fatal("expected an error for a non-block-aligned payload")
	}
}

// --- binary frame layout --------------------------------------------------
//
// These frames were produced by FrameGround.java, a line-by-line port of the
// app's own builder (b/a.java + b/b.java + c/c.java + c/b.java + c/a.java)
// run on the JVM. The Go encoder must reproduce them exactly.

var goldenFrames = []struct {
	name  string
	build func() []byte
	hex   string
}{
	{
		"read ids [5]",
		func() []byte { return BuildReadRequest([]int{5}) },
		"0018000600100119963F918F231F6749A141145B757A94A48DB3",
	},
	{
		"read ids [1,2,3]",
		func() []byte { return BuildReadRequest([]int{1, 2, 3}) },
		"0028000600140119A9E222164460859315387EF0417780A1F11130E8FFE86405D76031B76AD5D353A620",
	},
	{
		"write params 1=[01 02] 7=[0A]",
		func() []byte {
			return BuildWriteRequest([]BinaryParam{
				{No: 1, Value: []byte{0x01, 0x02}},
				{No: 7, Value: []byte{0x0A}},
			})
		},
		"00280006001B01188C2C3303044566591409AEAE1BD32D56E399D2A9956825416249ECFF0C0B2ACFF082",
	},
}

func TestFramesMatchTheApp(t *testing.T) {
	for _, g := range goldenFrames {
		got := FrameHex(g.build())
		if got != g.hex {
			t.Errorf("%s:\n  got  %s\n  want %s", g.name, got, g.hex)
		}
	}
}

func TestFrameFieldLayout(t *testing.T) {
	frame := BuildReadRequest([]int{1, 2, 3})

	// The length field counts everything except the trailing CRC.
	if got, want := be16(frame[0:2]), len(frame)-2; got != want {
		t.Errorf("total length field = %d, want frame length - 2 = %d", got, want)
	}
	if frame[2] != 0x00 || frame[3] != 0x06 {
		t.Errorf("constant field = %02X %02X, want 00 06", frame[2], frame[3])
	}
	// encrypted length field = body + 14 == 6 + 14
	if got, want := be16(frame[4:6]), 20; got != want {
		t.Errorf("encrypted length field = %d, want %d", got, want)
	}
	if frame[6] != 0x01 {
		t.Errorf("version byte = 0x%02X, want 01", frame[6])
	}
	if frame[7] != 0x19 {
		t.Errorf("command byte = 0x%02X, want 19", frame[7])
	}

	enc := frame[8 : len(frame)-2]
	if len(enc)%16 != 0 {
		t.Fatalf("encrypted region is %d bytes, not a multiple of 16", len(enc))
	}
	if _, err := ParseBinaryFrame(frame); err != nil {
		t.Fatalf("own frame failed verification: %v", err)
	}

	plain, err := aesCrypt(enc, true)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain[:10]) != binaryMarker {
		t.Errorf("marker = %q, want %q", plain[:10], binaryMarker)
	}
	if n := be16(plain[10:12]); n != 3 {
		t.Errorf("param count = %d, want 3", n)
	}
	for i, id := range []int{1, 2, 3} {
		if got := be16(plain[12+2*i : 14+2*i]); got != id {
			t.Errorf("param id %d = %d, want %d", i, got, id)
		}
	}
}

func TestWriteRequestFieldLayout(t *testing.T) {
	params := []BinaryParam{
		{No: 1, Value: []byte{0x01, 0x02}},
		{No: 7, Value: []byte{0x0A}},
	}
	frame := BuildWriteRequest(params)

	if got, want := be16(frame[0:2]), len(frame)-2; got != want {
		t.Errorf("total length field = %d, actual - 2 = %d", got, want)
	}
	if frame[7] != 0x18 {
		t.Errorf("command byte = 0x%02X, want 18", frame[7])
	}
	// body = (2+2+2) + (2+1+1) = 11 -> encrypted length field = 11 + 16 = 27.
	if got, want := be16(frame[4:6]), 27; got != want {
		t.Errorf("encrypted length field = %d, want %d", got, want)
	}

	plain, err := aesCrypt(frame[8:len(frame)-2], true)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := be16(plain[10:12]), 2; got != want {
		t.Errorf("param count = %d, want %d", got, want)
	}
	if got, want := be16(plain[12:14]), 11; got != want {
		t.Errorf("body length field = %d, want %d", got, want)
	}
}

func TestParseBinaryFrameChecksum(t *testing.T) {
	frame := BuildReadRequest([]int{5})
	bad := make([]byte, len(frame))
	copy(bad, frame)
	bad[9] ^= 0xFF
	if _, err := ParseBinaryFrame(bad); err != ErrBadChecksum {
		t.Fatalf("corrupted frame error = %v, want %v", err, ErrBadChecksum)
	}
}

func TestParseBinaryFrameShort(t *testing.T) {
	if _, err := ParseBinaryFrame([]byte{0x00, 0x01, 0x02}); err != ErrShortFrame {
		t.Fatalf("error = %v, want %v", err, ErrShortFrame)
	}
}

// --- JSON notification reassembly ----------------------------------------

func TestJSONStreamSplitsAcrossNotifications(t *testing.T) {
	docs := []string{
		`{"DevSN":"072604825024250800","Type":112,"SubType":7250,"modID":2}`,
		`{"dateTime":"2026-10-30 19:24:11","timeZMin":120}`,
	}

	var s JSONStream
	var got []string

	feed := strings.Join(docs, "")
	for i := 0; i < len(feed); i += 17 {
		end := i + 17
		if end > len(feed) {
			end = len(feed)
		}
		got = append(got, s.Feed([]byte(feed[i:end]))...)
	}

	if len(got) != 2 {
		t.Fatalf("reassembled %d documents, want 2 (pending=%q)", len(got), s.Pending())
	}
	for i, want := range docs {
		if got[i] != want {
			t.Errorf("doc %d = %s, want %s", i, got[i], want)
		}
	}
}

func TestJSONStreamBracesInsideStrings(t *testing.T) {
	// A naive '{' counter (which the app uses) breaks on this payload.
	payload := `{"cmd":"ok","note":"set {wmladd} done","DevSN":"a{b}c"}`
	var s JSONStream
	got := s.Feed([]byte(payload))
	if len(got) != 1 {
		t.Fatalf("got %d documents, want 1 (pending=%q)", len(got), s.Pending())
	}
	obj, err := ParseObject(got[0])
	if err != nil {
		t.Fatalf("reassembled text is not valid JSON: %v", err)
	}
	if obj["note"] != "set {wmladd} done" {
		t.Errorf("note = %v", obj["note"])
	}
}

func TestJSONStreamDropsPrefixNoise(t *testing.T) {
	var s JSONStream
	got := s.Feed([]byte("get srv ok:{\"DevSN\":\"X\"}"))
	if len(got) != 1 || got[0] != `{"DevSN":"X"}` {
		t.Fatalf("got %v, want one document with the prefix stripped", got)
	}
}

func TestJSONStreamKeepsPartialDocument(t *testing.T) {
	var s JSONStream
	if got := s.Feed([]byte(`{"DevSN":"12`)); len(got) != 0 {
		t.Fatalf("expected no complete document, got %v", got)
	}
	got := s.Feed([]byte(`34","Type":112}`))
	if len(got) != 1 {
		t.Fatalf("expected the document to complete, got %v", got)
	}
	obj, err := ParseObject(got[0])
	if err != nil {
		t.Fatal(err)
	}
	if obj["DevSN"] != "1234" {
		t.Errorf("DevSN = %v, want 1234", obj["DevSN"])
	}
}

func TestJSONStreamTwoDocumentsInOnePacket(t *testing.T) {
	var s JSONStream
	got := s.Feed([]byte(`{"a":1}{"b":2}`))
	if len(got) != 2 {
		t.Fatalf("got %d documents, want 2", len(got))
	}
}

// --- realtime value decoding ---------------------------------------------

// capturedPayload is a realtime response recorded verbatim from the battery
// (logcat line 49366, "u: 接收消息线程 => {...}"). It is the ground truth the
// decoder is written against.
const capturedPayload = `{"CommVer":1,"wifiSN":"F072604825024250800","modID":2,"date":"20210101004041","DevSN":"072604825024250800","Type":112,"SubType":7250,"Estate":5056,"Bfault":0,"Bwarn":0,"Bstate":5056,"BBfault":0,"BBwarn":0,"BTemp":[[260,260],[256,256]],"Batt":[[53100],[-178],[null]],"Batsoc":[[7600,1000,500000]],"Templist":[[260,260],[0,0],[65535,65535],[65535,65535]],"BattList":[[53060,65535],[-87,-1]],"BatsocList":[[7600,1000,250000]],"BatcelList":[[3314,3318,3315,3318,3314,3318,3315,3318,3310,3318,3314,3319,3314,3318,3314,3318],[65535,65535,65535,65535,65535,65535,65535,65535,65535,65535,65535,65535,65535,65535,65535,65535]],"EMSpara":[[2,6]],"BMaxMin":[[3319,3310],[11,8]],"LVolCur":[[576,480],[3000,3000]],"BMSpara":[[2,6]],"BLVolCu":[[576,480],[1500,1500]],"BtemList":[[260,260,260,260,32767,32767,32767,32767]]}`

// expected holds what FSolar itself computes for this payload, using its own
// magnifications from protocol_realtime_1.0.27.json.
var expected = map[string]float64{
	"battVolt":             53.060,
	"battCurr":             -8.7,
	"battSoc":              76.00,
	"battSoh":              100.0,
	"battCapacity":         250.000,
	"emsVoltage":           53.100,
	"emsCurrent":           -17.8,
	"emsSoc":               76.00,
	"emsSoh":               100.0,
	"emsCapacity":          500.000,
	"tempMax":              26.0,
	"tempMin":              26.0,
	"maxVoltage2bms":       3.319,
	"minVoltage2bms":       3.310,
	"maxVoltageNum2bms":    11,
	"minVoltageNum2bms":    8,
	"cellNumber":           2,
	"seriesParallelStatus": 6,
	"cellVolt1":            3.314,
	"cellVolt12":           3.319, // the pack maximum, matching BMaxMin[1][0] = 11
	"cellVolt16":           3.318,
	"cellTemp1":            26.0,
	"BMSLCVolt":            57.6,
	"BMSLDVolt":            48.0,
	"BMSLCCurr":            150.0,
	"BMSLDCurr":            150.0,
}

func TestDecodeCapturedPayload(t *testing.T) {
	obj, err := ParseObject(capturedPayload)
	if err != nil {
		t.Fatalf("captured payload is not valid JSON: %v", err)
	}

	vals := DecodeRealtime(obj)
	got := map[string]Value{}
	for _, v := range vals {
		got[v.Field.Param] = v
	}

	for param, want := range expected {
		v, ok := got[param]
		if !ok {
			t.Errorf("%s: not in the decoded table", param)
			continue
		}
		if !v.Valid {
			t.Errorf("%s: not decoded from the captured payload", param)
			continue
		}
		if diff := v.Number - want; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("%s = %v, want %v", param, v.Number, want)
		}
	}

	// 65535 / 32767 are "absent" markers and must never surface as numbers.
	for _, param := range []string{"cellVolt17", "cellTemp5", "maxCellTempNum"} {
		if v, ok := got[param]; ok && v.Valid && isSentinel(v.Number) {
			t.Errorf("%s leaked a sentinel value: %v", param, v)
		}
	}
	if v := got["cellVolt1"]; v.Valid && v.Number > 10 {
		t.Errorf("cellVolt1 = %v looks unscaled (cell voltage should be ~3.3 V)", v.Number)
	}

	st := ReadStatus(obj)
	if st.DevSN != "072604825024250800" {
		t.Errorf("DevSN = %q", st.DevSN)
	}
	if st.DevType != 112 || st.SubType != 7250 {
		t.Errorf("Type/SubType = %d/%d, want 112/7250", st.DevType, st.SubType)
	}
	if st.Estate != 5056 || st.Bstate != 5056 {
		t.Errorf("Estate/Bstate = %d/%d, want 5056/5056", st.Estate, st.Bstate)
	}

	s := Summary(obj)
	for _, want := range []string{"53.060 V", "3.319 V", "76.00 %", "250.000 Ah"} {
		if !strings.Contains(s, want) {
			t.Errorf("Summary is missing %q:\n%s", want, s)
		}
	}
}

// capturedPayload2 is a second, independent realtime document taken from a
// different source: reassembled from the raw HCI capture
// (capture/btsnoop_hci.log -> capture/reassembled.jsonl) rather than from
// logcat. Decoding must work identically for both.
const capturedPayload2 = `{"CommVer":1,"wifiSN":"F072604825024250800","modID":2,"date":"20210101004323","DevSN":"072604825024250800","Type":112,"SubType":7250,"Estate":5056,"Bfault":0,"Bwarn":0,"Bstate":5056,"BBfault":0,"BBwarn":0,"BTemp":[[260,260],[256,256]],"Batt":[[53100],[-189],[null]],"Batsoc":[[7550,1000,500000]],"Templist":[[260,260],[0,0],[65535,65535],[65535,65535]],"BattList":[[53050,65535],[-95,-1]],"BatsocList":[[7500,1000,250000]],"BatcelList":[[3313,3318,3314,3318,3313,3319,3314,3318,3308,3318,3313,3319,3313,3318,3313,3318],[65535,65535,65535,65535,65535,65535,65535,65535,65535,65535,65535,65535,65535,65535,65535,65535]],"EMSpara":[[2,6]],"BMaxMin":[[3319,3308],[5,8]],"LVolCur":[[576,480],[3000,3000]],"BMSpara":[[2,6]],"BLVolCu":[[576,480],[1500,1500]],"BtemList":[[260,260,260,260,32767,32767,32767,32767]]}`

// expected2 is derived by hand from the field table's magnifications, not from
// the decoder.
var expected2 = map[string]float64{
	"battVolt":             53.050,
	"battCurr":             -9.5,
	"battSoc":              75.0,
	"battSoh":              100.0,
	"battCapacity":         250.0,
	"emsVoltage":           53.1,
	"emsCurrent":           -18.9,
	"emsSoc":               75.5,
	"emsSoh":               100.0,
	"emsCapacity":          500.0,
	"maxVoltage2bms":       3.319,
	"minVoltage2bms":       3.308,
	"maxVoltageNum2bms":    5,
	"minVoltageNum2bms":    8,
	"cellNumber":           2,
	"seriesParallelStatus": 6,
	"cellVolt12":           3.319,
	"cellTemp1":            26.0,
	"BMSLCVolt":            57.6,
	"BMSLDVolt":            48.0,
	"BMSLCCurr":            150.0,
	"BMSLDCurr":            150.0,
}

func TestDecodeSecondCapturedPayload(t *testing.T) {
	obj, err := ParseObject(capturedPayload2)
	if err != nil {
		t.Fatalf("captured payload 2 is not valid JSON: %v", err)
	}

	got := map[string]Value{}
	for _, v := range DecodeRealtime(obj) {
		got[v.Field.Param] = v
	}
	for param, want := range expected2 {
		v, ok := got[param]
		if !ok {
			t.Errorf("%s: not in the decoded table", param)
			continue
		}
		if !v.Valid {
			t.Errorf("%s: not decoded", param)
			continue
		}
		if diff := v.Number - want; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("%s = %v, want %v", param, v.Number, want)
		}
	}

	// BMaxMin is the pack's own summary of the per-cell array. If the key and
	// position mapping were wrong these would disagree, so this is an
	// independent check that does not rely on the field table at all.
	//
	// The index the battery reports is 0-based: here 3319 first appears at
	// array index 5 and BMaxMin[1][0] is 5, and in the first captured payload
	// 3319 sits at index 11 which is exactly what BMaxMin[1][0] reports.
	cells := []float64{3313, 3318, 3314, 3318, 3313, 3319, 3314, 3318,
		3308, 3318, 3313, 3319, 3313, 3318, 3313, 3318}
	highest, lowest := cells[0], cells[0]
	highestIdx, lowestIdx := 0, 0
	for i, c := range cells {
		if c > highest {
			highest, highestIdx = c, i
		}
		if c < lowest {
			lowest, lowestIdx = c, i
		}
	}
	if got["maxVoltage2bms"].Number != highest/1000 {
		t.Errorf("maxVoltage2bms = %v, but the cell array max is %v", got["maxVoltage2bms"].Number, highest/1000)
	}
	if got["minVoltage2bms"].Number != lowest/1000 {
		t.Errorf("minVoltage2bms = %v, but the cell array min is %v", got["minVoltage2bms"].Number, lowest/1000)
	}
	if int(got["maxVoltageNum2bms"].Number) != highestIdx {
		t.Errorf("maxVoltageNum2bms = %v, but the max cell is at 0-based index %d",
			got["maxVoltageNum2bms"].Number, highestIdx)
	}
	if int(got["minVoltageNum2bms"].Number) != lowestIdx {
		t.Errorf("minVoltageNum2bms = %v, but the min cell is at 0-based index %d",
			got["minVoltageNum2bms"].Number, lowestIdx)
	}
}

func TestDecodeRealtimeFlatArrays(t *testing.T) {
	// Some firmwares send [53060] instead of [[53060]]; both must decode.
	doc := `{"BattList":[53060],"BatsocList":[7600]}`
	obj, err := ParseObject(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range DecodeRealtime(obj) {
		if v.Field.Param == "battVolt" {
			if !v.Valid || v.Number != 53.06 {
				t.Errorf("flat-array battVolt = %v (valid=%v), want 53.06", v.Number, v.Valid)
			}
			return
		}
	}
	t.Error("battVolt not found in decoded output")
}

func TestDecodeRealtimeMissingKeys(t *testing.T) {
	// A partial payload must not panic and must mark the rest invalid.
	doc := `{"DevSN":"X","BattList":[[50000]]}`
	obj, err := ParseObject(doc)
	if err != nil {
		t.Fatal(err)
	}
	vals := DecodeRealtime(obj)
	if len(vals) != len(RealtimeFields) {
		t.Fatalf("got %d values, want one per mapped field (%d)", len(vals), len(RealtimeFields))
	}
	var valid int
	for _, v := range vals {
		if v.Valid {
			valid++
		}
	}
	if valid == 0 {
		t.Error("nothing decoded from a payload that does contain BattList")
	}
	if valid == len(vals) {
		t.Error("every field decoded from a payload that only contains BattList")
	}
}

func TestIsJSONObject(t *testing.T) {
	if !IsJSONObject(` {"a":1}`) {
		t.Error("object not detected")
	}
	if IsJSONObject("get srv ok:") {
		t.Error("plain text misdetected as object")
	}
	if IsJSONObject(`[1,2]`) {
		t.Error("array misdetected as object")
	}
}

// --- command strings -----------------------------------------------------

func TestCommandsMatchTheApp(t *testing.T) {
	// Captured verbatim from a live FSolar session.
	want := []string{
		"wifilocalMonitor:get dev basice infor",
		"wifilocalMonitor:get Date",
		"wifilocalMonitor:get dev real infor",
		"wifilocalMonitor:get dev set infor",
		"wifilocalMonitor:get dev test infor",
		"wifiGet mode",
		"wifiGet debug",
	}
	got := []string{CmdBaseInfo, CmdDate, CmdRealtime, CmdSettings, CmdSelfTest, CmdNetMode, CmdDebugKey}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("command %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAdvertisedName(t *testing.T) {
	// B8:3D:FB:C1:95:08 advertised "F072604825024250800".
	if got := AdvertisedName("072604825024250800"); got != "F072604825024250800" {
		t.Errorf("AdvertisedName = %q", got)
	}
}
