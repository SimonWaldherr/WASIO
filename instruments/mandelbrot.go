// instruments/mandelbrot.go
package main

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"strconv"
)

// Payload entspricht dem WASIO‑Standard (stdin → JSON).
type Payload struct {
	Params map[string]string `json:"params"`
}

func main() {
	// 1) JSON‑Payload von stdin lesen
	var pl Payload
	if err := json.NewDecoder(os.Stdin).Decode(&pl); err != nil {
		// ungültiges JSON → leeres PNG (oder Fehlerbild)
		return
	}

	// 2) Parameter mit Defaults
	cx := parseFloat(pl.Params["cx"], -0.5)
	cy := parseFloat(pl.Params["cy"], 0.0)
	zoom := parseFloat(pl.Params["zoom"], 1.0)
	width := parseInt(pl.Params["width"], 800)
	height := parseInt(pl.Params["height"], 600)
	maxIter := parseInt(pl.Params["max_iter"], 1000)

	// 3) Fraktal berechnen
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	// Bildbereich im komplexen Raum: Breite = 3.5/zoom, Höhe entsprechend
	aspect := float64(width) / float64(height)
	xmin := cx - (3.5/zoom)/2
	xmax := cx + (3.5/zoom)/2
	ymin := cy - (3.5/zoom)/(2*aspect)
	ymax := cy + (3.5/zoom)/(2*aspect)

	for py := 0; py < height; py++ {
		y := ymin + (ymax-ymin)*float64(py)/float64(height-1)
		for px := 0; px < width; px++ {
			x := xmin + (xmax-xmin)*float64(px)/float64(width-1)
			// Mandelbrot-Iteration
			var zx, zy, zx2, zy2 float64
			var iter int
			for ; iter < maxIter && zx2+zy2 <= 4; iter++ {
				zy = 2*zx*zy + y
				zx = zx2 - zy2 + x
				zx2 = zx*zx
				zy2 = zy*zy
			}
			col := mapColor(iter, maxIter)
			img.Set(px, py, col)
		}
	}

	// 4) PNG an stdout schreiben
	png.Encode(os.Stdout, img)
}

// parseFloat konvertiert s → float64, oder liefert def bei Fehler.
func parseFloat(s string, def float64) float64 {
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return v
	}
	return def
}

// parseInt konvertiert s → int, oder liefert def bei Fehler.
func parseInt(s string, def int) int {
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return def
}

// mapColor wandelt Iterationszahl in Farbe um (smooth grayscale).
func mapColor(iter, maxIter int) color.RGBA {
	if iter == maxIter {
		return color.RGBA{0, 0, 0, 255} // im Set geblieben → schwarz
	}
	// smooth: 0 → 255
	t := uint8(255 - int(255*math.Sqrt(float64(iter)/float64(maxIter))))
	return color.RGBA{t, t, 255, 255}
}
