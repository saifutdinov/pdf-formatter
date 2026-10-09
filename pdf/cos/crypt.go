package cos

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/rc4"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
)

// ErrPassword means the document needs a (different) password.
var ErrPassword = errors.New("pdf: password required")

// Permission bits of /P (§7.6.4.2, Table 22), 1-based bit positions.
const (
	PermPrint    = 1 << 2
	PermModify   = 1 << 3
	PermCopy     = 1 << 4
	PermAnnotate = 1 << 5
	PermFillForm = 1 << 8
	PermAssemble = 1 << 10
)

// Security describes how an encrypted document was opened.
type Security struct {
	Method string // RC4-40, RC4-128, AES-128, AES-256
	Owner  bool   // opened with the owner password: no restrictions apply
	Perm   uint32 // /P
	User   bool   // a non-empty user password is needed to open the file
}

// Allows reports whether the permission bits (any of them) are granted.
func (s *Security) Allows(bits uint32) bool { return s == nil || s.Owner || s.Perm&bits != 0 }

// crypt is the standard security handler (§7.6.4) for one document.
type crypt struct {
	key      []byte
	v, r     int
	stm, str string // method: none, rc4, aesv2, aesv3
	encMeta  bool
	encNum   int // the /Encrypt dictionary itself is not encrypted
}

var padding = []byte("\x28\xBF\x4E\x5E\x4E\x75\x8A\x41\x64\x00\x4E\x56\xFF\xFA\x01\x08\x2E\x2E\x00\xB6\xD0\x68\x3E\x80\x2F\x0C\xA9\xFE\x64\x53\x69\x7A")

// padPassword is step (a) of Algorithm 2: the password in PDFDocEncoding
// (Latin-1 is close enough here), cut or padded to 32 bytes.
func padPassword(pw string) []byte {
	b := make([]byte, 0, 32)
	for _, r := range pw {
		if len(b) == 32 {
			break
		}
		if r > 255 {
			r = '?'
		}
		b = append(b, byte(r))
	}
	return append(b, padding[:32-len(b)]...)
}

func rc4Crypt(key, data []byte) []byte {
	c, _ := rc4.NewCipher(key)
	out := make([]byte, len(data))
	c.XORKeyStream(out, data)
	return out
}

func xorKey(key []byte, i byte) []byte {
	k := make([]byte, len(key))
	for j := range key {
		k[j] = key[j] ^ i
	}
	return k
}

// newCrypt authenticates password against the /Encrypt dictionary, as owner
// first, then as user.
func newCrypt(g Getter, enc Dict, encNum int, id []byte, pw string) (*crypt, *Security, error) {
	if g.Name(enc["Filter"]) != "Standard" {
		return nil, nil, fmt.Errorf("%w: handler %s", ErrEncrypted, g.Name(enc["Filter"]))
	}
	v, _ := g.Int(enc["V"])
	r, _ := g.Int(enc["R"])
	p, _ := g.Int(enc["P"])
	str := func(o Object) []byte { s, _ := g.Resolve(o).(String); return []byte(s) }
	o, u := str(enc["O"]), str(enc["U"])
	c := &crypt{v: v, r: r, encMeta: true, encNum: encNum}
	if b, ok := enc["EncryptMetadata"].(Bool); ok {
		c.encMeta = bool(b)
	}
	sec := &Security{Perm: uint32(int32(p))}
	n := 5
	if l, ok := g.Int(enc["Length"]); ok && v >= 2 {
		n = l / 8
	}
	switch v {
	case 1, 2:
		c.stm, c.str = "rc4", "rc4"
	case 4, 5:
		cf := g.Dict(enc["CF"])
		method := func(name Name) string {
			if name == "" || name == "Identity" {
				return "none"
			}
			f := g.Dict(cf[name])
			switch g.Name(f["CFM"]) {
			case "V2":
				if l, ok := g.Int(f["Length"]); ok && v == 4 {
					if l > 16 {
						l /= 8 // some writers give bits
					}
					n = l
				}
				return "rc4"
			case "AESV2":
				n = 16
				return "aesv2"
			case "AESV3":
				return "aesv3"
			}
			return "none"
		}
		c.stm, c.str = method(g.Name(enc["StmF"])), method(g.Name(enc["StrF"]))
	default:
		return nil, nil, fmt.Errorf("%w: V %d", ErrEncrypted, v)
	}
	n = max(5, min(16, n))
	sec.Method = map[string]string{"rc4": fmt.Sprintf("RC4-%d", n*8), "aesv2": "AES-128", "aesv3": "AES-256", "none": "—"}[c.stm]

	if r >= 5 {
		if len(o) < 48 || len(u) < 48 {
			return nil, nil, fmt.Errorf("%w: short O or U", ErrEncrypted)
		}
		oe, ue := str(enc["OE"]), str(enc["UE"])
		pwb := []byte(pw)
		if len(pwb) > 127 {
			pwb = pwb[:127]
		}
		h := func(salt, udata []byte) []byte { return hash2B(r, pwb, salt, udata) }
		var inter, wrapped []byte
		switch {
		case bytes.Equal(h(o[32:40], u[:48]), o[:32]):
			sec.Owner, inter, wrapped = true, h(o[40:48], u[:48]), oe
		case bytes.Equal(h(u[32:40], nil), u[:32]):
			inter, wrapped = h(u[40:48], nil), ue
		default:
			return nil, nil, ErrPassword
		}
		if len(wrapped) != 32 {
			return nil, nil, fmt.Errorf("%w: bad OE/UE", ErrEncrypted)
		}
		blk, _ := aes.NewCipher(inter)
		c.key = make([]byte, 32)
		cipher.NewCBCDecrypter(blk, make([]byte, 16)).CryptBlocks(c.key, wrapped)
		sec.User = !userOK5(r, u)
		return c, sec, nil
	}

	if len(o) < 32 {
		return nil, nil, fmt.Errorf("%w: short O", ErrEncrypted)
	}
	fileKey := func(upw []byte) []byte { // Algorithm 2
		h := md5.New()
		h.Write(upw)
		h.Write(o[:32])
		binary.Write(h, binary.LittleEndian, uint32(int32(p)))
		h.Write(id)
		if r >= 4 && !c.encMeta {
			h.Write([]byte{0xff, 0xff, 0xff, 0xff})
		}
		k := h.Sum(nil)
		if r >= 3 {
			for range 50 {
				s := md5.Sum(k[:n])
				k = s[:]
			}
		}
		return k[:n]
	}
	userOK := func(key []byte) bool { // Algorithms 4 and 5
		if r == 2 {
			return bytes.Equal(rc4Crypt(key, padding), u[:min(32, len(u))])
		}
		h := md5.New()
		h.Write(padding)
		h.Write(id)
		x := rc4Crypt(key, h.Sum(nil))
		for i := 1; i <= 19; i++ {
			x = rc4Crypt(xorKey(key, byte(i)), x)
		}
		return len(u) >= 16 && bytes.Equal(x, u[:16])
	}
	// Owner (Algorithm 7): the owner password unlocks the user password.
	ok := md5.Sum(padPassword(pw))
	ownerKey := ok[:]
	if r >= 3 {
		for range 50 {
			s := md5.Sum(ownerKey)
			ownerKey = s[:]
		}
	}
	ownerKey = ownerKey[:n]
	upw := o[:32]
	if r == 2 {
		upw = rc4Crypt(ownerKey, upw)
	} else {
		for i := 19; i >= 0; i-- {
			upw = rc4Crypt(xorKey(ownerKey, byte(i)), upw)
		}
	}
	if k := fileKey(upw); userOK(k) {
		c.key, sec.Owner = k, true
	} else if k := fileKey(padPassword(pw)); userOK(k) {
		c.key = k
	} else {
		return nil, nil, ErrPassword
	}
	sec.User = !userOK(fileKey(padding))
	return c, sec, nil
}

// userOK5 reports whether the empty password opens an AES-256 file.
func userOK5(r int, u []byte) bool {
	return bytes.Equal(hash2B(r, nil, u[32:40], nil), u[:32])
}

// hash2B is the password hash of AES-256 encryption: SHA-256 for R5,
// Algorithm 2.B of ISO 32000-2 for R6.
func hash2B(r int, pw, salt, udata []byte) []byte {
	h := sha256.New()
	h.Write(pw)
	h.Write(salt)
	h.Write(udata)
	k := h.Sum(nil)
	if r == 5 {
		return k
	}
	for round := 0; ; round++ {
		k1 := make([]byte, 0, 64*(len(pw)+len(k)+len(udata)))
		for range 64 {
			k1 = append(k1, pw...)
			k1 = append(k1, k...)
			k1 = append(k1, udata...)
		}
		blk, _ := aes.NewCipher(k[:16])
		e := make([]byte, len(k1))
		cipher.NewCBCEncrypter(blk, k[16:32]).CryptBlocks(e, k1)
		sum := 0
		for _, b := range e[:16] {
			sum += int(b)
		}
		var hf hash.Hash
		switch sum % 3 {
		case 0:
			hf = sha256.New()
		case 1:
			hf = sha512.New384()
		default:
			hf = sha512.New()
		}
		hf.Write(e)
		k = hf.Sum(nil)
		if round >= 63 && int(e[len(e)-1]) <= round-31 {
			break
		}
	}
	return k[:32]
}

// objectKey is Algorithm 1: the key for one object (AES-256 uses the file
// key directly).
func (c *crypt) objectKey(num, gen int, method string) []byte {
	if method == "aesv3" {
		return c.key
	}
	b := append([]byte{}, c.key...)
	b = append(b, byte(num), byte(num>>8), byte(num>>16), byte(gen), byte(gen>>8))
	if method == "aesv2" {
		b = append(b, "sAlT"...)
	}
	s := md5.Sum(b)
	return s[:min(len(c.key)+5, 16)]
}

func (c *crypt) decrypt(data []byte, num, gen int, method string) []byte {
	switch method {
	case "rc4":
		return rc4Crypt(c.objectKey(num, gen, method), data)
	case "aesv2", "aesv3":
		if len(data) < 32 || len(data)%16 != 0 {
			if len(data) < 16 {
				return nil
			}
			data = data[:len(data)/16*16] // truncated: keep what decrypts
		}
		blk, _ := aes.NewCipher(c.objectKey(num, gen, method))
		out := make([]byte, len(data)-16)
		cipher.NewCBCDecrypter(blk, data[:16]).CryptBlocks(out, data[16:])
		if p := int(out[len(out)-1]); p >= 1 && p <= 16 && p <= len(out) {
			out = out[:len(out)-p]
		}
		return out
	}
	return data
}

// sealed returns o encrypted as object num, gen (unchanged without c).
func (c *crypt) sealed(o Object, num, gen int) Object {
	if c == nil {
		return o
	}
	return c.apply(o, func(b []byte, method string) []byte { return c.encrypt(b, num, gen, method) })
}

func (c *crypt) encrypt(data []byte, num, gen int, method string) []byte {
	switch method {
	case "rc4":
		return rc4Crypt(c.objectKey(num, gen, method), data)
	case "aesv2", "aesv3":
		p := 16 - len(data)%16
		in := append(append([]byte{}, data...), bytes.Repeat([]byte{byte(p)}, p)...)
		out := make([]byte, 16+len(in))
		rand.Read(out[:16])
		blk, _ := aes.NewCipher(c.objectKey(num, gen, method))
		cipher.NewCBCEncrypter(blk, out[:16]).CryptBlocks(out[16:], in)
		return out
	}
	return data
}

// apply runs f over the strings and stream data of an indirect object
// (§7.6.2): returns a new object, the input is left alone. The /Encrypt
// dictionary itself is never passed here.
func (c *crypt) apply(o Object, f func(data []byte, method string) []byte) Object {
	var walk func(o Object) Object
	walk = func(o Object) Object {
		switch v := o.(type) {
		case String:
			return String(f([]byte(v), c.str))
		case Array:
			out := make(Array, len(v))
			for i, e := range v {
				out[i] = walk(e)
			}
			return out
		case Dict:
			out := make(Dict, len(v))
			for k, e := range v {
				out[k] = walk(e)
			}
			return out
		case *Stream:
			d := walk(v.Dict).(Dict)
			switch {
			case v.Dict["Type"] == Name("XRef"),
				v.Dict["Type"] == Name("Metadata") && !c.encMeta,
				identityCrypt(v.Dict):
				return &Stream{Dict: d, Data: v.Data}
			}
			return &Stream{Dict: d, Data: f(v.Data, c.stm)}
		}
		return o
	}
	return walk(o)
}

// identityCrypt reports a stream that opts out with an Identity crypt
// filter (§7.4.10).
func identityCrypt(d Dict) bool {
	names := []Object{d["Filter"]}
	if a, ok := d["Filter"].(Array); ok {
		names = a
	}
	for _, n := range names {
		if n == Name("Crypt") {
			p, _ := d["DecodeParms"].(Dict)
			if a, ok := d["DecodeParms"].(Array); ok && len(a) > 0 {
				p, _ = a[0].(Dict)
			}
			return p == nil || p["Name"] == nil || p["Name"] == Name("Identity")
		}
	}
	return false
}
