package main

import (
	"embed"
	"flag"
	"io"
	"log"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Optional launch flags, handy for the WiFi/TCP path:
	//
	//	felicity-battery-watch -via tcp -host 127.0.0.1 -port 53970
	//
	// Parsed with a private FlagSet and errors discarded, so any flags the
	// Wails toolchain itself adds can never break the app.
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	via := fs.String("via", TransportBLE, "transport: ble | tcp")
	host := fs.String("host", "", "battery monitor address (tcp; default 192.168.155.1)")
	port := fs.Int("port", 0, "battery monitor port (tcp; default 53970)")
	_ = fs.Parse(os.Args[1:])

	service := NewBatteryService(*via, *host, *port)

	app := application.New(application.Options{
		Name:        "felicity-battery-watch",
		Description: "Felicity battery monitor — scan over Bluetooth or dial the WiFi hotspot, then poll pack SOC and remaining kWh",
		Services: []application.Service{
			application.NewService(service),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "Felicity Battery Watch",
		Width: 640,
		Height: 720,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(6, 7, 15),
		URL:              "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
