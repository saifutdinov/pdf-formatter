package cos

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"encoding/ascii85"
	"errors"
	"io"
	"sync"
)

// FilterError reports a filter Decode does not handle. Image codecs
// (DCT, JPX, CCITTFax, JBIG2) land here on purpose: image data stays encoded.
type FilterError struct{ Filter Name }

func (e *FilterError) Error() string { return "pdf: unsupported filter " + string(e.Filter) }

// Filters lists a stream's filters with their parameters (§7.4).
func Filters(g Getter, d Dict) ([]Name, []Dict) {
	var names []Name
	var parms []Dict
	switch f := g.Resolve(d["Filter"]).(type) {
	case Name:
		names = []Name{f}
	case Array:
		for _, e := range f {
			names = append(names, g.Name(e))
		}
	}
	dp := d["DecodeParms"]
	if dp == nil {
		dp = d["DP"]
	}
	switch p := g.Resolve(dp).(type) {
	case Dict:
		parms = []Dict{p}
	case Array:
		for _, e := range p {
			parms = append(parms, g.Dict(e))
		}
	}
	return names, parms
}

// Decode applies the stream's filters and returns the decoded bytes.
func (r resolver) Decode(s *Stream) ([]byte, error) {
	data := s.Data
	names, parms := Filters(r, s.Dict)
	for i, f := range names {
		var p Dict
		if i < len(parms) {
			p = parms[i]
		}
		var err error
		switch f {
		case "FlateDecode", "Fl":
			if data, err = inflate(data); err == nil {
				data, err = predict(r, data, p)
			}
		case "LZWDecode", "LZW":
			early := 1
			if v, ok := r.Int(p["EarlyChange"]); ok {
				early = v
			}
			data, err = predict(r, lzwDecode(data, early), p)
		case "ASCIIHexDecode", "AHx":
			l := Lexer{B: append([]byte{'<'}, data...)}
			data = []byte(l.hexString().S)
		case "ASCII85Decode", "A85":
			data, err = decode85(data)
		case "RunLengthDecode", "RL":
			data = runLength(data)
		default:
			return data, &FilterError{f}
		}
		if err != nil {
			return data, err
		}
	}
	return data, nil
}

// maxDecoded caps a single decoded stream (zip bombs).
const maxDecoded = 1 << 30

// inflate is lenient: real files have bad headers, truncated data and bad
// checksums. Whatever decodes is kept.
func inflate(b []byte) ([]byte, error) {
	if zr, err := zlib.NewReader(bytes.NewReader(b)); err == nil {
		out, err := io.ReadAll(io.LimitReader(zr, maxDecoded))
		if len(out) > 0 || err == nil {
			return out, nil
		}
	}
	for _, skip := range []int{0, 2} {
		if len(b) > skip {
			out, _ := io.ReadAll(io.LimitReader(flate.NewReader(bytes.NewReader(b[skip:])), maxDecoded))
			if len(out) > 0 {
				return out, nil
			}
		}
	}
	if len(b) == 0 {
		return nil, nil
	}
	return nil, errors.New("pdf: corrupt flate stream")
}

func predict(r resolver, data []byte, p Dict) ([]byte, error) {
	pred, _ := r.Int(p["Predictor"])
	if pred < 2 {
		return data, nil
	}
	colors, bpc, cols := 1, 8, 1
	if v, ok := r.Int(p["Colors"]); ok && v > 0 {
		colors = v
	}
	if v, ok := r.Int(p["BitsPerComponent"]); ok && v > 0 {
		bpc = v
	}
	if v, ok := r.Int(p["Columns"]); ok && v > 0 {
		cols = v
	}
	bpp := max(1, colors*bpc/8)
	rowLen := (colors*bpc*cols + 7) / 8
	if rowLen <= 0 || rowLen > len(data) {
		rowLen = max(1, len(data)-1) // bogus parameters: treat as one row
	}
	if pred == 2 { // TIFF, 8-bit components only
		for i := 0; i < len(data); i += rowLen {
			row := data[i:min(i+rowLen, len(data))]
			for j := bpp; j < len(row); j++ {
				row[j] += row[j-bpp]
			}
		}
		return data, nil
	}
	out := make([]byte, 0, len(data)/(rowLen+1)*rowLen)
	prev := make([]byte, rowLen)
	for i := 0; i+1 < len(data); i += rowLen + 1 {
		ft := data[i]
		row := make([]byte, rowLen)
		copy(row, data[i+1:min(i+1+rowLen, len(data))])
		for j := range row {
			var a, c byte
			if j >= bpp {
				a, c = row[j-bpp], prev[j-bpp]
			}
			b := prev[j]
			switch ft {
			case 1:
				row[j] += a
			case 2:
				row[j] += b
			case 3:
				row[j] += byte((int(a) + int(b)) / 2)
			case 4:
				row[j] += paeth(a, b, c)
			}
		}
		out = append(out, row...)
		prev = row
	}
	return out, nil
}

func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
	if pa <= pb && pa <= pc {
		return a
	}
	if pb <= pc {
		return b
	}
	return c
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func decode85(b []byte) ([]byte, error) {
	b = bytes.TrimSpace(b)
	b = bytes.TrimPrefix(b, []byte("<~"))
	if i := bytes.Index(b, []byte("~>")); i >= 0 {
		b = b[:i]
	}
	dst := make([]byte, 4*len(b)+4)
	n, _, err := ascii85.Decode(dst, b, true)
	return dst[:n], err
}

func runLength(b []byte) []byte {
	var out []byte
	for i := 0; i < len(b); {
		n := int(b[i])
		i++
		switch {
		case n < 128:
			end := min(i+n+1, len(b))
			out = append(out, b[i:end]...)
			i = end
		case n > 128 && i < len(b):
			for range 257 - n {
				out = append(out, b[i])
			}
			i++
		default:
			return out
		}
	}
	return out
}

// lzwDecode implements the PDF LZW variant (§7.4.4) with EarlyChange.
func lzwDecode(b []byte, early int) []byte {
	var out []byte
	dict := make([][]byte, 258, 4096)
	reset := func() {
		dict = dict[:258]
		for i := range 256 {
			dict[i] = []byte{byte(i)}
		}
	}
	reset()
	width := 9
	var prev []byte
	var buf uint32
	nbits, pos := 0, 0
	for {
		for nbits < width {
			if pos >= len(b) {
				return out
			}
			buf = buf<<8 | uint32(b[pos])
			pos++
			nbits += 8
		}
		code := int(buf>>(nbits-width)) & (1<<width - 1)
		nbits -= width
		if code == 256 {
			reset()
			width, prev = 9, nil
			continue
		}
		if code == 257 {
			return out
		}
		var entry []byte
		switch {
		case code < len(dict) && dict[code] != nil:
			entry = dict[code]
		case code == len(dict) && prev != nil:
			entry = append(append([]byte{}, prev...), prev[0])
		default:
			return out
		}
		out = append(out, entry...)
		if prev != nil && len(dict) < 4096 {
			ne := make([]byte, len(prev)+1)
			copy(ne, prev)
			ne[len(prev)] = entry[0]
			dict = append(dict, ne)
		}
		prev = entry
		if len(dict)+early >= 1<<width && width < 12 {
			width++
		}
	}
}

// zlib writers carry large buffers: reuse them across streams.
var zwPool = sync.Pool{New: func() any {
	zw, _ := zlib.NewWriterLevel(nil, zlib.DefaultCompression)
	return zw
}}

// Deflate compresses data for FlateDecode.
func Deflate(data []byte) []byte {
	var buf bytes.Buffer
	zw := zwPool.Get().(*zlib.Writer)
	zw.Reset(&buf)
	zw.Write(data)
	zw.Close()
	zwPool.Put(zw)
	return buf.Bytes()
}
