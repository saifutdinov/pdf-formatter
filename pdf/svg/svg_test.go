package svg

import (
	"math"
	"strings"
	"testing"

	"pdf-formatter/pdf/content"
	"pdf-formatter/pdf/cos"
)

const sample = `<?xml version="1.0"?>
<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="200" height="100" viewBox="0 0 200 100">
  <style>.st0{fill:#2a7}.st1{fill:none;stroke:#c33;stroke-width:5}</style>
  <defs>
    <linearGradient id="g"><stop offset="0" stop-color="#ff8800"/><stop offset="1" stop-color="#0088ff"/></linearGradient>
    <circle id="dot" r="4" fill="navy"/>
  </defs>
  <rect class="st0" x="5" y="5" width="60" height="70" rx="8"/>
  <circle class="st1" cx="105" cy="40" r="30"/>
  <g transform="translate(150 10) rotate(45)" opacity="0.5">
    <path d="M0 0 h20 v20 h-20 z M5 5 l10 0 0 10 -10 0z" fill-rule="evenodd" fill="rgb(10%, 20%, 30%)"/>
  </g>
  <path d="M10 90 A 20 10 0 0 1 50 90 Q 70 70 90 90 T 130 90 C 140 80 150 80 160 90 S 180 100 190 90" fill="none" stroke="black"/>
  <polygon points="170,60 190,60 180,80" fill="url(#g)"/>
  <use xlink:href="#dot" x="20" y="20"/>
  <text x="10" y="20">Hello</text>
  <polyline points="0,0 1,1" style="stroke:hsl(120, 100%, 25%); fill:none" />
</svg>`

func TestConvert(t *testing.T) {
	res, err := Convert([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if res.ViewBox != [4]float64{0, 0, 200, 100} || res.Width != 150 || res.Height != 75 {
		t.Errorf("size %v %v×%v", res.ViewBox, res.Width, res.Height)
	}
	w := strings.Join(res.Warnings, "; ")
	if !strings.Contains(w, "текст") || !strings.Contains(w, "градиенты") {
		t.Errorf("warnings: %v", res.Warnings)
	}
	if len(res.Alphas) != 1 {
		t.Errorf("alphas: %v", res.Alphas)
	}
	// Interpret the result: paths land inside the viewBox (flipped to y up).
	doc, err := cos.Open([]byte("%PDF-1.7\n1 0 obj <</Type/Catalog/Pages 2 0 R>> endobj 2 0 obj <</Type/Pages/Kids[]/Count 0>> endobj trailer <</Root 1 0 R>>"))
	if err != nil {
		t.Fatal(err)
	}
	in := content.NewInterp(doc, content.NewFontCache())
	in.Run(res.Ops, nil, content.Identity)
	paths := 0
	for _, it := range in.Items {
		if it.Kind != content.Path {
			continue
		}
		paths++
		if it.BBox[0] < -5 || it.BBox[2] > 205 || it.BBox[1] < -5 || it.BBox[3] > 105 {
			t.Errorf("path outside viewBox: %v", it.BBox)
		}
	}
	if paths != 7 {
		t.Errorf("%d paths, want 7", paths)
	}
	// The green rect: SVG y 5..75 → PDF y 25..95.
	r := in.Items[0].BBox
	if math.Abs(r[0]-5) > 0.01 || math.Abs(r[1]-25) > 0.01 || math.Abs(r[3]-95) > 0.01 || in.Items[0].State.Fill != [3]float64{42.0 / 255, 119.0 / 255, 1} && in.Items[0].State.Fill[1] < 0.4 {
		t.Errorf("rect %v fill %v", r, in.Items[0].State.Fill)
	}
}

func TestPathParsing(t *testing.T) {
	ops := pathOps("M10-20.5.5,3e1L1e-1 2zm1 1h2v-3q1 1 2 2t3 0a5 5 0 1 0 10 0")
	names := ""
	for _, o := range ops {
		names += o.Name
	}
	if !strings.HasPrefix(names, "mllhmllcc") || !strings.HasSuffix(names, "c") {
		t.Errorf("ops %q", names)
	}
	if n := parseNums("1,2 -3.5e2.5-1"); len(n) != 5 || n[2] != -350 || n[3] != 0.5 || n[4] != -1 {
		t.Errorf("numbers %v", n)
	}
	m, ok := parseTransform("translate(10 20) scale(2)")
	if !ok {
		t.Fatal("transform")
	}
	x, y := m.Apply(1, 1)
	if x != 12 || y != 22 {
		t.Errorf("translate·scale applied to (1,1) = %v,%v, want 12,22", x, y)
	}
	if c, ok := parseColor("#abc"); !ok || math.Abs(c[0]-0xaa/255.0) > 1e-9 {
		t.Errorf("color %v", c)
	}
}
