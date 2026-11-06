package fsolar

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// Realtime payload decoding.
//
// The field names, array positions, decimal magnifications and display
// precisions come from the app's own protocol table
//   assets/file_config_cache_zip/file_data_protocol_cache/
//       dataProtocol_1516001837143105536_1.0.27.zip
// which unzips to protocol_realtime_1.0.27.json; RealtimeFields in
// fields_gen.go is generated straight from it (see tools/genfields).
//
// "magnification" is a power-of-ten exponent: BattList[0][0] = 53060 with
// magnification -3 is 53.060 V, which is what FSolar displays.
//
// A captured realtime payload (real device, logcat line 49366) looks like:
//
//	"Batt":[[53100],[-178],[null]]
//	"Batsoc":[[7600,1000,500000]]
//	"BattList":[[53060,65535],[-87,-1]]
//	"BatsocList":[[7600,1000,250000]]
//	"BatcelList":[[3314,3318,...],[65535,...]]
//	"BMaxMin":[[3319,3310],[11,8]]
//	"BMSpara":[[2,6]]
//	"TempList":[[260,260],[0,0],[65535,65535],[65535,65535]]
//	"BLVolCu":[[576,480],[1500,1500]]
//	"BtemList":[[260,260,260,260,32767,32767,32767,32767]]
// ---------------------------------------------------------------------------

// Field is one mapped value inside a realtime JSON payload.
type Field struct {
	JSONKey string // key in the JSON payload, e.g. "BattList"
	Outer   int    // first array index, from the app's "position" "a,b"
	Inner   int    // second array index
	Mag     int    // magnification: decimal exponent
	Prec    int    // display precision
	Param   string // the app's parameter name
	Label   string // human-readable label
	Unit    string
}

// Value is a decoded field with its scaling applied.
type Value struct {
	Field
	Number float64
	Text   string
	Valid  bool
}

// Sentinel values the firmware uses for "not present" (0xFFFF / 0x7FFF).
func isSentinel(f float64) bool {
	return f == 65535 || f == 32767 || f == -32768
}

// ParseObject unmarshals a reassembled JSON document into a generic map.
func ParseObject(doc string) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(doc), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// DecodeRealtime extracts every mapped field from a realtime payload. Fields
// the device did not send are returned with Valid == false, so callers can see
// the whole table rather than a silently shortened one.
func DecodeRealtime(obj map[string]any) []Value {
	out := make([]Value, 0, len(RealtimeFields))
	index := keyIndex(obj)
	for _, f := range RealtimeFields {
		out = append(out, decodeField(obj, f, index))
	}
	return out
}

// keyIndex maps a case-folded payload key to the key actually present in the
// object. The app's table and the device's payload do not always agree on
// case: the table says "TempList" while this device sends "Templist". Only
// case is folded — "Batt" and "BattList" are different values and must stay
// distinct.
func keyIndex(obj map[string]any) map[string]string {
	idx := make(map[string]string, len(obj))
	for k := range obj {
		n := strings.ToLower(k)
		if _, taken := idx[n]; !taken {
			idx[n] = k
		}
	}
	return idx
}

func decodeField(obj map[string]any, f Field, index map[string]string) Value {
	v := Value{Field: f}
	raw, ok := pick(obj, f, index)
	if !ok {
		return v
	}
	switch n := raw.(type) {
	case float64:
		if isSentinel(n) {
			return v
		}
		v.Number = scale(n, f.Mag)
		v.Valid = true
	case string:
		v.Text = n
		v.Valid = true
	case bool:
		if n {
			v.Number = 1
		}
		v.Valid = true
	}
	return v
}

// pick walks obj[key][outer][inner]. Real payloads use [[a,b],[c,d]], but some
// firmwares emit flat arrays, so both shapes are accepted.
func pick(obj map[string]any, f Field, index map[string]string) (any, bool) {
	key := f.JSONKey
	if actual, ok := index[strings.ToLower(key)]; ok {
		key = actual
	}
	v, ok := obj[key]
	if !ok {
		return nil, false
	}
	list, ok := v.([]any)
	if !ok {
		return v, f.Outer == 0 && f.Inner == 0
	}
	if f.Outer >= len(list) {
		return nil, false
	}
	item := list[f.Outer]
	if sub, ok := item.([]any); ok {
		if f.Inner >= len(sub) {
			return nil, false
		}
		return sub[f.Inner], true
	}
	// Flat array: the position's outer index is the element index.
	if f.Inner != 0 {
		return nil, false
	}
	return item, true
}

func scale(raw float64, mag int) float64 {
	if mag == 0 {
		return raw
	}
	factor := 1.0
	for i := 0; i < abs(mag); i++ {
		factor *= 10
	}
	if mag < 0 {
		return raw / factor
	}
	return raw * factor
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// String renders the value. The app displays with "precisions" digits, but
// never fewer than the magnification carries, otherwise the last digits the
// device actually sent would be hidden (53060 with mag -3 must read 53.060).
func (v Value) String() string {
	if !v.Valid {
		return "-"
	}
	if v.Text != "" {
		return v.Text
	}
	prec := abs(v.Field.Prec)
	if mag := abs(v.Field.Mag); mag > prec {
		prec = mag
	}
	return fmt.Sprintf("%.*f%s", prec, v.Number, unitSuffix(v.Field.Unit))
}

func unitSuffix(u string) string {
	if u == "" {
		return ""
	}
	return " " + u
}

// Status holds the scalar bookkeeping fields that accompany the measurements.
type Status struct {
	Estate  int    `json:"Estate"`
	Bstate  int    `json:"Bstate"`
	Bfault  int    `json:"Bfault"`
	Bwarn   int    `json:"Bwarn"`
	BBfault int    `json:"BBfault"`
	BBwarn  int    `json:"BBwarn"`
	DevType int    `json:"Type"`
	SubType int    `json:"SubType"`
	DevSN   string `json:"DevSN"`
	ModAddr string `json:"ModAddr"`
	Date    string `json:"date"`
	ModID   any    `json:"modID"`
}

// ReadStatus pulls the scalar fields out of a payload.
func ReadStatus(obj map[string]any) Status {
	var s Status
	s.Estate = intOf(obj, "Estate")
	s.Bstate = intOf(obj, "Bstate")
	s.Bfault = intOf(obj, "Bfault")
	s.Bwarn = intOf(obj, "Bwarn")
	s.BBfault = intOf(obj, "BBfault")
	s.BBwarn = intOf(obj, "BBwarn")
	s.DevType = intOf(obj, "Type")
	s.SubType = intOf(obj, "SubType")
	s.DevSN = stringOf(obj, "DevSN")
	s.ModAddr = stringOf(obj, "ModAddr")
	s.Date = stringOf(obj, "date")
	s.ModID = obj["modID"]
	return s
}

func intOf(obj map[string]any, key string) int {
	if v, ok := obj[key]; ok {
		if f, ok := v.(float64); ok {
			return int(f)
		}
	}
	return 0
}

func stringOf(obj map[string]any, key string) string {
	if v, ok := obj[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// Summary renders a realtime payload as a readable block, skipping fields the
// device left empty.
func Summary(obj map[string]any) string {
	st := ReadStatus(obj)
	var b strings.Builder
	fmt.Fprintf(&b, "device   : DevSN=%s Type=%d SubType=%d ModAddr=%s\n",
		st.DevSN, st.DevType, st.SubType, st.ModAddr)
	fmt.Fprintf(&b, "flags    : Estate=0x%04X Bstate=0x%04X Bfault=%d Bwarn=%d\n",
		st.Estate, st.Bstate, st.Bfault, st.Bwarn)

	for _, v := range DecodeRealtime(obj) {
		if !v.Valid {
			continue
		}
		fmt.Fprintf(&b, "  %-20s %-22s %s\n", v.Field.Param, v.Field.Label, v.String())
	}
	return b.String()
}
