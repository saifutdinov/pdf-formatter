package cos

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// pdfDoc maps PDFDocEncoding bytes that differ from Latin-1 (Annex D).
var pdfDoc = map[byte]rune{
	0x18: 0x02D8, 0x19: 0x02C7, 0x1A: 0x02C6, 0x1B: 0x02D9, 0x1C: 0x02DD, 0x1D: 0x02DB, 0x1E: 0x02DA, 0x1F: 0x02DC,
	0x80: 0x2022, 0x81: 0x2020, 0x82: 0x2021, 0x83: 0x2026, 0x84: 0x2014, 0x85: 0x2013, 0x86: 0x0192, 0x87: 0x2044,
	0x88: 0x2039, 0x89: 0x203A, 0x8A: 0x2212, 0x8B: 0x2030, 0x8C: 0x201E, 0x8D: 0x201C, 0x8E: 0x201D, 0x8F: 0x2018,
	0x90: 0x2019, 0x91: 0x201A, 0x92: 0x2122, 0x93: 0xFB01, 0x94: 0xFB02, 0x95: 0x0141, 0x96: 0x0152, 0x97: 0x0160,
	0x98: 0x0178, 0x99: 0x017D, 0x9A: 0x0131, 0x9B: 0x0142, 0x9C: 0x0153, 0x9D: 0x0161, 0x9E: 0x017E, 0xA0: 0x20AC,
}

// Text decodes a text string (§7.9.2.2): UTF-16BE or UTF-8 with a BOM,
// otherwise PDFDocEncoding.
func Text(s String) string {
	b := []byte(s)
	switch {
	case len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF:
		u := make([]uint16, 0, len(b)/2)
		for i := 2; i+1 < len(b); i += 2 {
			u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
		}
		return string(utf16.Decode(u))
	case len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
		return string(b[3:])
	}
	var sb strings.Builder
	for _, c := range b {
		if r, ok := pdfDoc[c]; ok {
			sb.WriteRune(r)
		} else {
			sb.WriteRune(rune(c))
		}
	}
	return sb.String()
}

// TextString encodes text: ASCII as is, anything else as UTF-16BE.
func TextString(s string) String {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii && utf8.ValidString(s) {
		return String(s)
	}
	b := []byte{0xFE, 0xFF}
	for _, u := range utf16.Encode([]rune(s)) {
		b = append(b, byte(u>>8), byte(u))
	}
	return String(b)
}

// ParseDate reads "D:YYYYMMDDHHmmSSOHH'mm'" (§7.9.4); every part after the
// year is optional.
func ParseDate(s string) (time.Time, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "D:")
	num := func(from, n, def int) int {
		if len(s) < from+n {
			return def
		}
		v, err := strconv.Atoi(s[from : from+n])
		if err != nil {
			return def
		}
		return v
	}
	if len(s) < 4 {
		return time.Time{}, false
	}
	y := num(0, 4, -1)
	if y < 0 {
		return time.Time{}, false
	}
	loc := time.UTC
	if len(s) > 14 {
		switch s[14] {
		case '+', '-':
			off := num(15, 2, 0)*3600 + num(18, 2, 0)*60
			if s[14] == '-' {
				off = -off
			}
			loc = time.FixedZone("", off)
		}
	}
	return time.Date(y, time.Month(max(1, num(4, 2, 1))), max(1, num(6, 2, 1)), num(8, 2, 0), num(10, 2, 0), num(12, 2, 0), 0, loc), true
}

// FormatDate writes a PDF date with the time zone offset.
func FormatDate(t time.Time) String {
	_, off := t.Zone()
	sign := '+'
	if off < 0 {
		sign, off = '-', -off
	}
	return String(fmt.Sprintf("D:%s%c%02d'%02d'", t.Format("20060102150405"), sign, off/3600, off%3600/60))
}
