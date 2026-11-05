// Command relay is a tiny bidirectional TCP forwarder, cross-compiled for
// Android and pushed to the phone with adb. It exists to let a PC that cannot
// join the battery's WiFi AP still drive fsolar's TCP transport: the PC
// connects to the relay over `adb forward`, and the relay pipes every byte to
// the battery and back.
//
// On the phone:
//
//	adb push relay /data/local/tmp/relay && adb shell /data/local/tmp/relay -listen 127.0.0.1:53970 -target 192.168.155.1:53970 &
//	adb forward tcp:53970 tcp:53970
//
// Then on the PC:
//
//	fsolar -via tcp -host 127.0.0.1 real
package main

import (
	"flag"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:53970", "local address to listen on (on the phone)")
	target := flag.String("target", "192.168.155.1:53970", "upstream battery address")
	flag.Parse()

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("listen %s: %v", *listen, err)
	}
	log.Printf("relay: %s -> %s", *listen, *target)

	// Stop on SIGTERM/SIGINT so `adb shell pkill` tears it down cleanly.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() { <-sig; ln.Close() }()

	for {
		conn, err := ln.Accept()
		if err != nil {
			return // listener closed
		}
		go pipe(conn, *target)
	}
}

func pipe(client net.Conn, target string) {
	defer client.Close()
	up, err := net.Dial("tcp", target)
	if err != nil {
		log.Printf("relay: dial %s: %v", target, err)
		return
	}
	defer up.Close()

	// The battery's simple TCP stack services one client at a time and does
	// not recover from a half-close, so when either direction ends we tear
	// the whole pair down (the deferred Closes), no FIN half-close. The
	// client always finishes reading before it exits, so nothing is lost.
	done := make(chan struct{})
	var once sync.Once
	end := func() { once.Do(func() { close(done) }) }
	go func() { io.Copy(up, client); end() }()
	go func() { io.Copy(client, up); end() }()
	<-done
}
