// Command icon draws the application icon into packaging/icon.png.
//
//	go run ./tools/icon   (scripts/release.sh turns it into the macOS .icns)
package main

import (
	"image"
	"image/color"
	"image/png"
	"log"
	"os"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

const size = 1024

func fill(dst *image.RGBA, c color.Color, path func(r *vector.Rasterizer)) {
	r := vector.NewRasterizer(size, size)
	path(r)
	r.Draw(dst, dst.Bounds(), image.NewUniform(c), image.Point{})
}

// roundRect adds a rectangle with circular corners (cubic approximation).
func roundRect(r *vector.Rasterizer, x0, y0, x1, y1, rad float32) {
	const k = 0.5523
	r.MoveTo(x0+rad, y0)
	r.LineTo(x1-rad, y0)
	r.CubeTo(x1-rad+k*rad, y0, x1, y0+rad-k*rad, x1, y0+rad)
	r.LineTo(x1, y1-rad)
	r.CubeTo(x1, y1-rad+k*rad, x1-rad+k*rad, y1, x1-rad, y1)
	r.LineTo(x0+rad, y1)
	r.CubeTo(x0+rad-k*rad, y1, x0, y1-rad+k*rad, x0, y1-rad)
	r.LineTo(x0, y0+rad)
	r.CubeTo(x0, y0+rad-k*rad, x0+rad-k*rad, y0, x0+rad, y0)
	r.ClosePath()
}

func main() {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	blue := color.RGBA{0x24, 0x63, 0xeb, 0xff}
	// Background tile on the macOS icon grid (824 px of 1024).
	fill(img, blue, func(r *vector.Rasterizer) { roundRect(r, 100, 100, 924, 924, 185) })
	// A sheet with a folded corner.
	const x0, y0, x1, y1, fold = 312, 220, 712, 780, 120
	fill(img, color.White, func(r *vector.Rasterizer) {
		r.MoveTo(x0, y0)
		r.LineTo(x1-fold, y0)
		r.LineTo(x1, y0+fold)
		r.LineTo(x1, y1)
		r.LineTo(x0, y1)
		r.ClosePath()
	})
	fill(img, color.RGBA{0xb9, 0xcd, 0xf8, 0xff}, func(r *vector.Rasterizer) {
		r.MoveTo(x1-fold, y0)
		r.LineTo(x1-fold, y0+fold)
		r.LineTo(x1, y0+fold)
		r.ClosePath()
	})
	// Text lines and the label.
	for i, w := range []float32{280, 230, 260} {
		y := float32(600 + i*48)
		fill(img, color.RGBA{0xc8, 0xd4, 0xe8, 0xff}, func(r *vector.Rasterizer) { roundRect(r, 372, y, 372+w, y+18, 9) })
	}
	data, err := os.ReadFile("fonts/free/PTSans.ttc")
	if err != nil {
		log.Fatal(err)
	}
	coll, err := opentype.ParseCollection(data)
	if err != nil {
		log.Fatal(err)
	}
	var face font.Face
	for i := range coll.NumFonts() {
		f, _ := coll.Font(i)
		if name, _ := f.Name(nil, 6); name == "PTSans-Bold" {
			face, _ = opentype.NewFace(f, &opentype.FaceOptions{Size: 190, DPI: 72, Hinting: font.HintingNone})
		}
	}
	if face == nil {
		log.Fatal("PTSans-Bold not found")
	}
	d := &font.Drawer{Dst: img, Src: image.NewUniform(blue), Face: face}
	w := d.MeasureString("PDF")
	d.Dot = fixed.Point26_6{X: fixed.I(512) - w/2, Y: fixed.I(530)}
	d.DrawString("PDF")

	out, err := os.Create("packaging/icon.png")
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()
	if err := png.Encode(out, img); err != nil {
		log.Fatal(err)
	}
}
