// Command fsolar is a from-scratch reimplementation of the links the Felicity
// Solar ("FSolar") Android app uses to read a Felicity battery. The battery
// speaks one text/JSON protocol over two transports, and this program does
// both:
//
//	Bluetooth LE   (default)  -via ble
//	WiFi hotspot   plain TCP  -via tcp   (the app's "WiFi local" mode)
//
// Usage:
//
//	fsolar scan                        list nearby BLE devices
//	fsolar info                        base info + device date
//	fsolar real                        one realtime snapshot
//	fsolar watch                       poll realtime data (default every 8s)
//	fsolar watch -count 5              poll exactly 5 times, then exit
//	fsolar send <command>              send an arbitrary text command, print replies
//	fsolar set <json>                  write parameters (wifilocalMonitor:set cmd=...)
//
// The same commands run over the battery's WiFi hotspot:
//
//	fsolar -via tcp real               (PC joined to the AP; battery at 192.168.155.1:53970)
//	fsolar -via tcp -host 127.0.0.1 watch -count 5   (through a phone/adb bridge)
//
// Common flags:
//
//	-via ble|tcp       transport (default ble)
//	-addr  B8:3D:FB:C1:95:08   connect to this MAC instead of scanning (BLE)
//	-prefix F                  advertisement name prefix used while scanning (BLE)
//	-host 192.168.155.1        battery monitor address (TCP)
//	-port 53970                battery monitor port (TCP)
//	-interval 8s               realtime polling interval
//	-timeout 15s               per-command response timeout
//	-count 5                   stop after N samples (0 = forever)
//	-json                      print the raw JSON documents
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"fsolar/internal/fsolar"
)

type options struct {
	via      string
	addr     string
	prefix   string
	host     string
	port     int
	interval time.Duration
	timeout  time.Duration
	count    int
	rawJSON  bool
}

func main() {
	code := run()
	// WinRT keeps a background thread alive once the BLE stack has been
	// enabled, so returning from main is not enough: exit explicitly.
	os.Exit(code)
}

func run() int {
	var o options
	flag.StringVar(&o.via, "via", "ble", "transport: ble | tcp")
	flag.StringVar(&o.addr, "addr", "", "address of the battery, MAC (UUID on macOS) (BLE; skips scanning)")
	flag.StringVar(&o.prefix, "prefix", fsolar.NamePrefix, "advertised name prefix to scan for (BLE)")
	flag.StringVar(&o.host, "host", fsolar.DefaultTCPHost, "battery monitor address (TCP)")
	flag.IntVar(&o.port, "port", fsolar.DefaultTCPPort, "battery monitor port (TCP)")
	flag.DurationVar(&o.interval, "interval", 8*time.Second, "realtime polling interval")
	flag.DurationVar(&o.timeout, "timeout", 15*time.Second, "response timeout")
	flag.IntVar(&o.count, "count", 0, "stop after N samples (0 = poll forever)")
	flag.BoolVar(&o.rawJSON, "json", false, "print raw JSON documents")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: fsolar [flags] scan|info|real|watch|send|set [args]")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		return 2
	}
	cmd := flag.Arg(0)

	// --- pick the transport ------------------------------------------------
	var link fsolar.Link
	var ble *fsolar.Client
	switch o.via {
	case "ble":
		ble = fsolar.NewClient()
		link = ble
	case "tcp":
		tc := fsolar.NewTCPClient()
		tc.Host = o.host
		tc.Port = o.port
		link = tc
	default:
		return fail(fmt.Errorf("unknown -via %q (want ble or tcp)", o.via))
	}

	if err := link.Enable(); err != nil {
		return fail(err)
	}

	if cmd == "scan" {
		if ble == nil {
			return fail(fmt.Errorf("scan is BLE-only; the battery's WiFi AP is not discovered by scanning (use -via ble)"))
		}
		if err := runScan(ble); err != nil {
			return fail(err)
		}
		return 0
	}

	// Commands that are not a strict request/response pair print every
	// document as it arrives. Request() already returns the first document,
	// so the callback skips that one to avoid printing it twice.
	if cmd == "send" || cmd == "set" {
		first := true
		link.SetOnNotify(func(doc string) {
			if first {
				first = false
				return
			}
			fmt.Println(doc)
		})
	}

	// --- connect ------------------------------------------------------------
	var connectErr error
	if ble != nil {
		connectErr = connectBLE(ble, o)
	} else {
		tc := link.(*fsolar.TCPClient)
		fmt.Fprintf(os.Stderr, "connecting to %s:%d ...\n", tc.Host, tc.Port)
		connectErr = tc.Connect()
	}
	if connectErr != nil {
		return fail(connectErr)
	}
	defer func() {
		if err := link.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "close:", err)
		}
	}()

	switch cmd {
	case "info":
		if err := runInfo(link, o); err != nil {
			return fail(err)
		}
	case "real":
		if err := runRealtime(link, o); err != nil {
			return fail(err)
		}
	case "watch":
		if err := runWatch(link, o); err != nil {
			return fail(err)
		}
	case "send":
		if flag.NArg() < 2 {
			return fail(fmt.Errorf("send needs a command string"))
		}
		doc, err := link.Request(strings.Join(flag.Args()[1:], " "), o.timeout)
		if err != nil {
			return fail(err)
		}
		fmt.Println(doc)
	case "set":
		if flag.NArg() < 2 {
			return fail(fmt.Errorf("set needs a JSON object, e.g. {\"wmladd\":2}"))
		}
		doc, err := link.Request(fsolar.SetCommand(strings.Join(flag.Args()[1:], "")), o.timeout)
		if err != nil {
			return fail(err)
		}
		fmt.Println(doc)
	default:
		flag.Usage()
		return 2
	}
	return 0
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "error:", err)
	return 1
}

// --- commands ------------------------------------------------------------

func runScan(c *fsolar.Client) error {
	fmt.Println("scanning for 10s ...")
	seen := map[string]bool{}
	return c.ScanRaw(10*time.Second, func(r fsolar.Discovered) {
		key := r.Address.String() + "|" + r.Name
		if seen[key] {
			return
		}
		seen[key] = true
		tag := ""
		if strings.HasPrefix(r.Name, fsolar.NamePrefix) {
			tag = "   <-- Felicity battery (DevSN " + strings.TrimPrefix(r.Name, fsolar.NamePrefix) + ")"
		}
		fmt.Printf("%-20s  rssi=%4d  name=%-24s%s\n", r.Address.String(), r.RSSI, r.Name, tag)
	})
}

func connectBLE(c *fsolar.Client, o options) error {
	if o.addr != "" {
		target, err := parseAddress(o.addr)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "connecting to %s ...\n", target.String())
		return c.Connect(target)
	}

	fmt.Fprintf(os.Stderr, "scanning for name prefix %q ...\n", o.prefix)
	res, err := c.Scan(o.prefix, 20*time.Second)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "found %s name=%q rssi=%d\n", res.Address.String(), res.Name, res.RSSI)
	return c.Connect(res.Address)
}

func runInfo(c fsolar.Link, o options) error {
	doc, err := c.Request(fsolar.CmdBaseInfo, o.timeout)
	if err != nil {
		return err
	}
	obj, err := fsolar.ParseObject(doc)
	if err != nil {
		fmt.Println(doc)
	} else {
		for _, k := range []string{"DevSN", "wifiSN", "Type", "SubType", "version", "M1SwVer", "M2SwVer", "DHwVer", "COM", "iotType"} {
			if v, ok := obj[k]; ok {
				fmt.Printf("  %-10s %v\n", k, v)
			}
		}
	}

	dateDoc, err := c.Request(fsolar.CmdDate, o.timeout)
	if err != nil {
		return nil // date is optional
	}
	if dObj, err := fsolar.ParseObject(dateDoc); err == nil {
		fmt.Printf("  %-10s %v (utc offset %v min)\n", "date", dObj["dateTime"], dObj["timeZMin"])
	}
	return nil
}

func runRealtime(c fsolar.Link, o options) error {
	doc, err := c.Request(fsolar.CmdRealtime, o.timeout)
	if err != nil {
		return err
	}
	printReading(doc, o.rawJSON)
	return nil
}

func runWatch(c fsolar.Link, o options) error {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)

	ticker := time.NewTicker(o.interval)
	defer ticker.Stop()

	// An immediate first sample, then one per interval: a cron job or a
	// Home Assistant sensor should not have to wait for the tick.
	samples := 0
	for {
		doc, err := c.Request(fsolar.CmdRealtime, o.timeout)
		if err != nil {
			fmt.Fprintln(os.Stderr, "query failed:", err)
		} else {
			printReading(doc, o.rawJSON)
		}
		samples++
		if o.count > 0 && samples >= o.count {
			return nil
		}
		select {
		case <-stop:
			return nil
		case <-ticker.C:
		}
	}
}

func printReading(doc string, rawJSON bool) {
	if rawJSON {
		fmt.Println(doc)
		return
	}
	obj, err := fsolar.ParseObject(doc)
	if err != nil {
		fmt.Println(doc)
		return
	}
	fmt.Printf("%s\n%s\n", time.Now().Format("2006-01-02 15:04:05"), fsolar.Summary(obj))
}
