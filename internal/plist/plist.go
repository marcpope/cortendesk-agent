// Package plist reads the string values of a property list's top-level
// dictionary, in XML or binary (bplist00) form. That is all the agent needs
// from an app's Info.plist.
package plist

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf16"
)

var errFormat = errors.New("plist: unsupported or corrupt file")

// ReadFile returns the top-level string values of the plist at path.
func ReadFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// Info.plist files are small. Refuse anything silly.
	b, err := io.ReadAll(io.LimitReader(f, 8<<20))
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

func Parse(b []byte) (map[string]string, error) {
	if bytes.HasPrefix(b, []byte("bplist00")) {
		return parseBinary(b)
	}
	return parseXML(b)
}

func parseXML(b []byte) (map[string]string, error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false
	// Find the first <dict>.
	for {
		t, err := d.Token()
		if err != nil {
			return nil, errFormat
		}
		if se, ok := t.(xml.StartElement); ok && se.Name.Local == "dict" {
			break
		}
	}
	out := map[string]string{}
	var key string
	haveKey := false
	for {
		t, err := d.Token()
		if err != nil {
			return out, nil
		}
		switch t := t.(type) {
		case xml.StartElement:
			switch {
			case t.Name.Local == "key":
				var s string
				if err := d.DecodeElement(&s, &t); err != nil {
					return nil, errFormat
				}
				key, haveKey = s, true
			case haveKey && t.Name.Local == "string":
				var s string
				if err := d.DecodeElement(&s, &t); err != nil {
					return nil, errFormat
				}
				out[key] = s
				haveKey = false
			default:
				if err := d.Skip(); err != nil {
					return nil, errFormat
				}
				haveKey = false
			}
		case xml.EndElement:
			if t.Name.Local == "dict" {
				return out, nil
			}
		}
	}
}

type bplist struct {
	b       []byte
	offsets []uint64
	refSize int
}

func parseBinary(b []byte) (map[string]string, error) {
	if len(b) < 8+32 {
		return nil, errFormat
	}
	tr := b[len(b)-32:]
	offSize := int(tr[6])
	refSize := int(tr[7])
	numObj := binary.BigEndian.Uint64(tr[8:16])
	top := binary.BigEndian.Uint64(tr[16:24])
	tableOff := binary.BigEndian.Uint64(tr[24:32])
	if offSize < 1 || offSize > 8 || refSize < 1 || refSize > 8 || numObj == 0 || top >= numObj {
		return nil, errFormat
	}
	end := uint64(len(b) - 32)
	if tableOff >= end || numObj > (end-tableOff)/uint64(offSize) {
		return nil, errFormat
	}
	p := &bplist{b: b, refSize: refSize}
	p.offsets = make([]uint64, numObj)
	for i := range p.offsets {
		pos := tableOff + uint64(i*offSize)
		p.offsets[i] = readUint(b[pos : pos+uint64(offSize)])
		if p.offsets[i] >= tableOff {
			return nil, errFormat
		}
	}
	off := p.offsets[top]
	marker := b[off]
	if marker>>4 != 0xD {
		return nil, errFormat
	}
	n, pos, err := p.count(off)
	if err != nil {
		return nil, err
	}
	if pos+2*n*uint64(refSize) > end {
		return nil, errFormat
	}
	out := map[string]string{}
	for i := uint64(0); i < n; i++ {
		kref := readUint(b[pos+i*uint64(refSize) : pos+(i+1)*uint64(refSize)])
		vref := readUint(b[pos+(n+i)*uint64(refSize) : pos+(n+i+1)*uint64(refSize)])
		k, ok := p.str(kref)
		if !ok {
			continue
		}
		if v, ok := p.str(vref); ok {
			out[k] = v
		}
	}
	return out, nil
}

// count reads the length nibble of the object at off, following the 0xF
// escape to an int object. It returns the count and the offset of the data.
func (p *bplist) count(off uint64) (uint64, uint64, error) {
	n := uint64(p.b[off] & 0x0F)
	pos := off + 1
	if n != 0x0F {
		return n, pos, nil
	}
	if pos >= uint64(len(p.b)) || p.b[pos]>>4 != 0x1 {
		return 0, 0, errFormat
	}
	size := uint64(1) << (p.b[pos] & 0x0F)
	if size > 8 || pos+1+size > uint64(len(p.b)) {
		return 0, 0, errFormat
	}
	n = readUint(p.b[pos+1 : pos+1+size])
	return n, pos + 1 + size, nil
}

func (p *bplist) str(ref uint64) (string, bool) {
	if ref >= uint64(len(p.offsets)) {
		return "", false
	}
	off := p.offsets[ref]
	kind := p.b[off] >> 4
	if kind != 0x5 && kind != 0x6 {
		return "", false
	}
	n, pos, err := p.count(off)
	if err != nil {
		return "", false
	}
	if kind == 0x5 {
		if pos+n > uint64(len(p.b)) {
			return "", false
		}
		return string(p.b[pos : pos+n]), true
	}
	if pos+2*n > uint64(len(p.b)) {
		return "", false
	}
	u := make([]uint16, n)
	for i := range u {
		u[i] = binary.BigEndian.Uint16(p.b[pos+uint64(2*i):])
	}
	return string(utf16.Decode(u)), true
}

func readUint(b []byte) uint64 {
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v
}

// BundleName picks the display name of an app bundle from its Info.plist.
func BundleName(m map[string]string, fallback string) string {
	for _, k := range []string{"CFBundleDisplayName", "CFBundleName"} {
		if v := strings.TrimSpace(m[k]); v != "" {
			return v
		}
	}
	return fallback
}

// BundleVersion prefers the marketing version over the build number.
func BundleVersion(m map[string]string) string {
	for _, k := range []string{"CFBundleShortVersionString", "CFBundleVersion"} {
		if v := strings.TrimSpace(m[k]); v != "" {
			return v
		}
	}
	return ""
}
