// Command mockbattery stands in for a Felicity battery's WiFi monitor socket,
// so the CLI and the desktop app can be exercised end to end without the pack
// in the room:
//
//	go run ./tools/mockbattery                       # listens on 127.0.0.1:53970
//	fsolar -via tcp -host 127.0.0.1 watch -count 2   # dial it with the CLI
//	felicity-battery-watch -via tcp -host 127.0.0.1  # ...or with the GUI
//	go run ./tools/mockbattery -addr :53970 -duration 30m -payload capture/live/real.json
//
// It speaks the real transport shape (PROTOCOL.md §1.4): raw UTF-8 text commands
// in, unframed JSON documents back, one long-lived socket per client. Replies are
// the documents captured from a live pack (capture/live/*.json); the settings
// reply is deliberately two concatenated documents, because that is what the pack
// sends for its two batteries and it exercises JSONStream reassembly.
//
// Like the real device it services one client at a time and closes fully.
package main

import (
	"bufio"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Captured from a live 2-pack battery (Type 112, SubType 7250, DevSN
// 072604825024250772). Fixed-point, exactly as the device sends it.
const (
	basiceReply = `{"CommVer":1,"version":"1.02","wifiSN":"F072604825024250772","COM":3,"iotType":3,"modID":1,"DevSN":"072604825024250772","Type":112,"SubType":7250,"DSwVer":65535,"M1SwVer":518,"M2SwVer":5,"DHwVer":0,"CtHwVer":0,"PwHwVer":65535}`

	dateReply = `{"CommVer":1,"wifiSN":"F072604825024250772","iotType":3,"dateTime":"20210101000616","timeZMin":120}`

	// Two documents in one write: pack 1 then pack 2.
	setReply = `{"CommVer":1,"DevSN":"072604825024250772","modID":1,"ttlPack":2,"index":1,"Type":112,"wMHMt":0,"wSlNum":0,"wMlAdd":0,"wMlRes":0,"wCVP80":3450,"wCVP20":3050,"cVolH2":3750,"cVolL2":0,"cVolHA":3600,"cVolHAB":3400,"cVolHi":3650,"cVolHB":3400,"cVolLA":2750,"cVolLAB":2950,"cVolLo":2500,"cVolLB":2950,"bCCHAB":750,"bCCHA":1500,"bCCHi":1600,"bCCHi2":2000,"bDCHAB":750,"bDCHA":1500,"bDCHi":1600,"bDCHi2":2000,"cCTHAB":45,"cCTHA":50,"cCTHi":55,"cCTHi2":57,"cCTLAB":10,"cCTLA":5,"cCTLo":0,"cCTLo2":-2,"cDTHAB":45,"cDTHA":50,"cDTHi":55,"cDTHi2":57,"cDTLAB":-10,"cDTLA":-15,"cDTLo":-20,"cDTLo2":-22,"hTHAlm":90,"hTHi":100,"balEn":1,"balVErr":65535,"balVEn":3380,"balTEn":0,"cDDep":31,"cTempC":0,"fRstCnt":10,"fClrTi":60,"iExTime":1440,"sExTime":5,"bWirMo":0}{"CommVer":1,"DevSN":"072604825024250772","modID":1,"ttlPack":2,"index":2,"Type":112,"bLigEn":0,"mRtnEn":0,"fRecEn":0}`

	realReply = `{"CommVer":1,"wifiSN":"F072604825024250772","modID":1,"date":"20210101000615","DevSN":"072604825024250772","Type":112,"SubType":7250,"Estate":5056,"Bfault":0,"Bwarn":0,"Bstate":5056,"BBfault":0,"BBwarn":0,"BTemp":[[270,270],[256,256]],"Batt":[[53000],[-317],[null]],"Batsoc":[[9500,1000,500000]],"Templist":[[270,270],[0,0],[65535,65535],[65535,65535]],"BattList":[[53010,65535],[-157,-1]],"BatsocList":[[9500,1000,250000]],"BatcelList":[[3316,3309,3318,3310,3317,3310,3317,3308,3319,3310,3317,3311,3316,3308,3317,3304],[65535,65535,65535,65535,65535,65535,65535,65535,65535,65535,65535,65535,65535,65535,65535,65535]],"EMSpara":[[2,6]],"BMaxMin":[[3319,3304],[8,15]],"LVolCur":[[576,480],[3000,3000]],"BMSpara":[[2,6]],"BLVolCu":[[576,480],[1500,1500]],"BtemList":[[270,270,270,270,32767,32767,32767,32767]]}`
)

// replyFor maps a monitor command to the document the pack would answer with.
// Unknown commands are logged and unanswered, which is also what the device
// does for "wifiGet mode" and for the self-test command outside a test run.
func replyFor(cmd, realtime string) string {
	switch {
	case strings.Contains(cmd, "real infor"):
		return realtime
	case strings.Contains(cmd, "basice infor"):
		return basiceReply
	case strings.Contains(cmd, "get Date"):
		return dateReply
	case strings.Contains(cmd, "set infor"):
		return setReply
	default:
		return ""
	}
}

func main() {
	addr := flag.String("addr", "127.0.0.1:53970", "listen address (the battery's own address is 192.168.155.1:53970)")
	duration := flag.Duration("duration", 5*time.Minute, "stop after this long (0 = until interrupted)")
	payload := flag.String("payload", "", "file holding the realtime JSON document to answer with (default: built-in capture)")
	quiet := flag.Bool("quiet", false, "log only connections, not every command")
	flag.Parse()

	realtime := realReply
	if *payload != "" {
		data, err := os.ReadFile(*payload)
		if err != nil {
			log.Fatalf("read -payload: %v", err)
		}
		realtime = strings.TrimSpace(string(data))
	}

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen %s: %v", *addr, err)
	}

	var once sync.Once
	stopped := make(chan struct{})
	stop := func(reason string) {
		once.Do(func() {
			log.Printf("stopping (%s)", reason)
			close(stopped)
		})
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signals
		stop("interrupted")
	}()
	if *duration > 0 {
		go func() {
			select {
			case <-time.After(*duration):
				stop("duration elapsed")
			case <-stopped:
			}
		}()
	}

	var connections, commands int64
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-stopped:
					return
				default:
					log.Printf("accept: %v", err)
					return
				}
			}
			go serve(conn, realtime, *quiet, &connections, &commands, stopped)
		}
	}()

	log.Printf("mock battery on %s — realtime reply %d bytes, duration %s",
		*addr, len(realtime), *duration)

	<-stopped
	_ = listener.Close()
	log.Printf("closed after %d connection(s), %d command(s)",
		atomic.LoadInt64(&connections), atomic.LoadInt64(&commands))
}

// serve handles one client until it goes away, answering each write with the
// canned document for that command.
func serve(conn net.Conn, realtime string, quiet bool, connections, commands *int64, stopped chan struct{}) {
	atomic.AddInt64(connections, 1)
	remote := conn.RemoteAddr().String()
	log.Printf("connect %s", remote)
	defer func() {
		// Close fully: the real device's TCP stack does not recover from a
		// half-closed socket, and neither should the stand-in.
		_ = conn.Close()
		log.Printf("closed %s", remote)
	}()

	reader := bufio.NewReader(conn)
	buf := make([]byte, 4096)
	for {
		select {
		case <-stopped:
			return
		default:
		}

		n, err := reader.Read(buf)
		if n > 0 {
			atomic.AddInt64(commands, 1)
			cmd := strings.TrimSpace(string(buf[:n]))
			if !quiet {
				log.Printf("<- %s: %s", remote, cmd)
			}
			reply := replyFor(cmd, realtime)
			if reply == "" {
				if !quiet {
					log.Printf("-> %s: (no canned reply)", remote)
				}
				continue
			}
			if _, werr := conn.Write([]byte(reply)); werr != nil {
				log.Printf("write to %s: %v", remote, werr)
				return
			}
			if !quiet {
				log.Printf("-> %s: %d bytes", remote, len(reply))
			}
		}
		if err != nil {
			if err != io.EOF && !quiet {
				log.Printf("read %s: %v", remote, err)
			}
			return
		}
	}
}
