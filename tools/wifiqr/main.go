// wifiqr renders a scan-to-join QR for an access point, using the same
// encoder the OLED panel uses.
//
// The panel is 64 pixels tall, so its symbol is at most 31 modules and the
// SSID has a 15-byte budget. This has the whole screen, so it can carry a
// longer name -- and it is the honest answer when the panel has to fall back
// to printing the key.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"

	"github.com/mame82/P4wnP1_aloa/oled"
)

func main() {
	ssid := flag.String("ssid", "", "access point name (required)")
	psk := flag.String("psk", "", "pre-shared key; empty means an OPEN network")
	out := flag.String("out", "wifi-join.png", "PNG to write")
	scale := flag.Int("scale", 10, "pixels per module")
	quiet := flag.Int("quiet", 4, "quiet zone in modules; the spec says 4")
	flag.Parse()

	if *ssid == "" {
		log.Fatal("-ssid is required")
	}
	uri := oled.WiFiURI(*ssid, *psk)
	q, err := oled.NewQR([]byte(uri))
	if err != nil {
		log.Fatalf("encoding %d bytes: %v", len(uri), err)
	}

	side := (q.Size + 2**quiet) * *scale
	img := image.NewGray(image.Rect(0, 0, side, side))
	// White paper first, then the dark modules. A QR drawn the other way round
	// is a photographic negative and decodes as nothing at all.
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	for y := 0; y < q.Size; y++ {
		for x := 0; x < q.Size; x++ {
			if !q.At(x, y) {
				continue
			}
			for dy := 0; dy < *scale; dy++ {
				for dx := 0; dx < *scale; dx++ {
					img.SetGray((x+*quiet)**scale+dx, (y+*quiet)**scale+dy, color.Gray{Y: 0})
				}
			}
		}
	}

	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("payload   : %s\n", uri)
	fmt.Printf("bytes     : %d\n", len(uri))
	fmt.Printf("symbol    : version %d, %d modules\n", q.Version, q.Size)
	fmt.Printf("wrote     : %s (%dx%d)\n", *out, side, side)
}
