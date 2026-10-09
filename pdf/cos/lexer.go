package cos

import (
	"errors"
	"strconv"
)

// TokKind is the kind of a lexical token (§7.2).
type TokKind uint8

const (
	TEOF TokKind = iota
	TInt
	TReal
	TName
	TString
	TKeyword // also content stream operators
	TArrayOpen
	TArrayClose
	TDictOpen
	TDictClose
	TBraceOpen // PostScript calculator functions only
	TBraceClose
)

type Token struct {
	Kind TokKind
	S    string // name (without '/'), keyword, string bytes
	I    int64
	F    float64
}

// Lexer tokenizes PDF syntax. It is shared by the file parser and the
// content stream parser.
type Lexer struct {
	B   []byte
	Pos int
	// Refs enables "num gen R" recognition in ReadObject (off for content streams).
	Refs bool
}

var errSyntax = errors.New("pdf: syntax error")

func IsWhite(c byte) bool {
	return c == ' ' || c == '\n' || c == '\r' || c == '\t' || c == '\f' || c == 0
}

func IsDelim(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func isRegular(c byte) bool { return !IsWhite(c) && !IsDelim(c) }

// SkipSpace skips whitespace and comments.
func (l *Lexer) SkipSpace() {
	for l.Pos < len(l.B) {
		c := l.B[l.Pos]
		if IsWhite(c) {
			l.Pos++
		} else if c == '%' {
			for l.Pos < len(l.B) && l.B[l.Pos] != '\n' && l.B[l.Pos] != '\r' {
				l.Pos++
			}
		} else {
			return
		}
	}
}

func (l *Lexer) Next() Token {
	for {
		if t, ok := l.next(); ok {
			return t
		}
	}
}

// next returns ok=false after skipping a stray ')' or '>'.
func (l *Lexer) next() (Token, bool) {
	l.SkipSpace()
	if l.Pos >= len(l.B) {
		return Token{Kind: TEOF}, true
	}
	c := l.B[l.Pos]
	switch c {
	case '[':
		l.Pos++
		return Token{Kind: TArrayOpen}, true
	case ']':
		l.Pos++
		return Token{Kind: TArrayClose}, true
	case '{':
		l.Pos++
		return Token{Kind: TBraceOpen}, true
	case '}':
		l.Pos++
		return Token{Kind: TBraceClose}, true
	case '<':
		if l.Pos+1 < len(l.B) && l.B[l.Pos+1] == '<' {
			l.Pos += 2
			return Token{Kind: TDictOpen}, true
		}
		return l.hexString(), true
	case '>':
		l.Pos++
		if l.Pos < len(l.B) && l.B[l.Pos] == '>' {
			l.Pos++
			return Token{Kind: TDictClose}, true
		}
		return Token{}, false
	case '(':
		return l.literalString(), true
	case ')':
		l.Pos++
		return Token{}, false
	case '/':
		return l.name(), true
	}
	start := l.Pos
	for l.Pos < len(l.B) && isRegular(l.B[l.Pos]) {
		l.Pos++
	}
	word := l.B[start:l.Pos]
	if t, ok := number(word); ok {
		return t, true
	}
	return Token{Kind: TKeyword, S: string(word)}, true
}

// number parses PDF numbers leniently: "+1", "-.5", "4.", also junk like "--5".
func number(w []byte) (Token, bool) {
	if len(w) == 0 {
		return Token{}, false
	}
	c := w[0]
	if !(c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.') {
		return Token{}, false
	}
	i, neg := 0, false
	for i < len(w) && (w[i] == '+' || w[i] == '-') {
		neg = neg != (w[i] == '-')
		i++
	}
	var ip int64
	digits, dot := 0, false
	var frac float64
	scale := 1.0
	for ; i < len(w); i++ {
		c := w[i]
		switch {
		case c >= '0' && c <= '9':
			digits++
			if dot {
				scale /= 10
				frac += float64(c-'0') * scale
			} else {
				ip = ip*10 + int64(c-'0')
			}
		case c == '.' && !dot:
			dot = true
		default:
			if digits == 0 {
				return Token{}, false
			}
			i = len(w) // trailing junk like "1.2.3": keep the prefix
		}
	}
	if digits == 0 {
		return Token{}, false
	}
	if dot {
		f := float64(ip) + frac
		if v, err := strconv.ParseFloat(string(w), 64); err == nil {
			f = v // exact for well-formed numbers
		} else if neg {
			f = -f
		}
		if neg && f > 0 {
			f = -f
		}
		return Token{Kind: TReal, F: f}, true
	}
	if neg {
		ip = -ip
	}
	return Token{Kind: TInt, I: ip}, true
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

func (l *Lexer) name() Token {
	l.Pos++ // '/'
	start := l.Pos
	plain := true
	for l.Pos < len(l.B) && isRegular(l.B[l.Pos]) {
		if l.B[l.Pos] == '#' {
			plain = false
		}
		l.Pos++
	}
	raw := l.B[start:l.Pos]
	if plain {
		return Token{Kind: TName, S: string(raw)}
	}
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] == '#' && i+2 < len(raw) {
			h, ok1 := unhex(raw[i+1])
			lo, ok2 := unhex(raw[i+2])
			if ok1 && ok2 {
				out = append(out, h<<4|lo)
				i += 2
				continue
			}
		}
		out = append(out, raw[i])
	}
	return Token{Kind: TName, S: string(out)}
}

func (l *Lexer) hexString() Token {
	l.Pos++ // '<'
	var out []byte
	var hi byte
	half := false
	for l.Pos < len(l.B) {
		c := l.B[l.Pos]
		l.Pos++
		if c == '>' {
			break
		}
		v, ok := unhex(c)
		if !ok {
			continue
		}
		if half {
			out = append(out, hi<<4|v)
		} else {
			hi = v
		}
		half = !half
	}
	if half {
		out = append(out, hi<<4)
	}
	return Token{Kind: TString, S: string(out)}
}

func (l *Lexer) literalString() Token {
	l.Pos++ // '('
	var out []byte
	depth := 1
	for l.Pos < len(l.B) {
		c := l.B[l.Pos]
		l.Pos++
		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return Token{Kind: TString, S: string(out)}
			}
		case '\r':
			if l.Pos < len(l.B) && l.B[l.Pos] == '\n' {
				l.Pos++
			}
			c = '\n'
		case '\\':
			if l.Pos >= len(l.B) {
				break
			}
			e := l.B[l.Pos]
			l.Pos++
			switch e {
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			case 'b':
				c = '\b'
			case 'f':
				c = '\f'
			case '\r':
				if l.Pos < len(l.B) && l.B[l.Pos] == '\n' {
					l.Pos++
				}
				continue
			case '\n':
				continue
			default:
				if e >= '0' && e <= '7' {
					v := int(e - '0')
					for k := 0; k < 2 && l.Pos < len(l.B) && l.B[l.Pos] >= '0' && l.B[l.Pos] <= '7'; k++ {
						v = v*8 + int(l.B[l.Pos]-'0')
						l.Pos++
					}
					c = byte(v)
				} else {
					c = e // \( \) \\ and unknown escapes
				}
			}
		}
		out = append(out, c)
	}
	return Token{Kind: TString, S: string(out)}
}

// ReadObject builds an object whose first token is t. Keywords other than
// true/false/null yield a *KeywordError.
func (l *Lexer) ReadObject(t Token) (Object, error) { return l.readObject(t, 0) }

func (l *Lexer) readObject(t Token, depth int) (Object, error) {
	if depth > 256 {
		return nil, errSyntax
	}
	switch t.Kind {
	case TInt:
		if l.Refs {
			save := l.Pos
			if t2 := l.Next(); t2.Kind == TInt {
				if t3 := l.Next(); t3.Kind == TKeyword && t3.S == "R" {
					return Ref{int(t.I), int(t2.I)}, nil
				}
			}
			l.Pos = save
		}
		return Int(t.I), nil
	case TReal:
		return Real(t.F), nil
	case TName:
		return Name(t.S), nil
	case TString:
		return String(t.S), nil
	case TArrayOpen:
		a := Array{}
		for {
			t := l.Next()
			if t.Kind == TArrayClose {
				return a, nil
			}
			if t.Kind == TEOF {
				return a, errSyntax
			}
			o, err := l.readObject(t, depth+1)
			if err != nil {
				return a, err
			}
			a = append(a, o)
		}
	case TDictOpen:
		d := Dict{}
		for {
			t := l.Next()
			if t.Kind == TDictClose {
				return d, nil
			}
			if t.Kind == TEOF {
				return d, errSyntax
			}
			if t.Kind != TName {
				continue // tolerate junk keys
			}
			vt := l.Next()
			if vt.Kind == TDictClose {
				return d, nil
			}
			v, err := l.readObject(vt, depth+1)
			if err != nil {
				return d, err
			}
			if v != nil {
				d[Name(t.S)] = v
			}
		}
	case TKeyword:
		switch t.S {
		case "true":
			return Bool(true), nil
		case "false":
			return Bool(false), nil
		case "null":
			return nil, nil
		}
	}
	return nil, &KeywordError{t.S}
}

// KeywordError reports an unexpected keyword where an object was expected.
type KeywordError struct{ Keyword string }

func (e *KeywordError) Error() string { return "pdf: unexpected keyword " + strconv.Quote(e.Keyword) }
