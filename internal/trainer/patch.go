package trainer

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
)

type Section struct {
	Name       string
	VA, VSize  uint64
	Executable bool
}

func (g *Game) sections() []Section {
	h, err := g.Read(g.Base, 0x1000)
	if err != nil || len(h) < 0x100 {
		return nil
	}
	pe := binary.LittleEndian.Uint32(h[0x3c:])
	n := int(binary.LittleEndian.Uint16(h[pe+6:]))
	opt := int(binary.LittleEndian.Uint16(h[pe+20:]))
	var out []Section
	for i := 0; i < n; i++ {
		o := int(pe) + 24 + opt + i*40
		if o+40 > len(h) {
			break
		}
		ch := binary.LittleEndian.Uint32(h[o+36:])
		out = append(out, Section{
			Name:       strings.TrimRight(string(h[o:o+8]), "\x00"),
			VSize:      uint64(binary.LittleEndian.Uint32(h[o+8:])),
			VA:         uint64(binary.LittleEndian.Uint32(h[o+12:])),
			Executable: ch&0x20000000 != 0,
		})
	}
	return out
}

type pattern struct {
	b    []byte
	mask []bool
}

func parsePattern(s string) pattern {
	var p pattern
	for _, t := range strings.Fields(s) {
		if t == "?" || t == "??" {
			p.b, p.mask = append(p.b, 0), append(p.mask, false)
			continue
		}
		v, _ := strconv.ParseUint(t, 16, 8)
		p.b, p.mask = append(p.b, byte(v)), append(p.mask, true)
	}
	return p
}

func (p pattern) find(buf []byte) []int {
	var out []int
	first := -1
	for i, m := range p.mask {
		if m {
			first = i
			break
		}
	}
	if first < 0 {
		return nil
	}
	for i := 0; i+len(p.b) <= len(buf); {
		j := bytes.IndexByte(buf[i+first:], p.b[first])
		if j < 0 {
			break
		}
		i += j
		ok := true
		for k, m := range p.mask {
			if m && buf[i+k] != p.b[k] {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, i)
		}
		i++
	}
	return out
}

// scanImage returns RVAs of pattern matches in executable sections.
func (g *Game) scanImage(pat string) []uint64 {
	p := parsePattern(pat)
	var res []uint64
	for _, s := range g.sections() {
		if !s.Executable {
			continue
		}
		buf, _ := g.Read(g.Base+s.VA, int(s.VSize))
		for _, o := range p.find(buf) {
			res = append(res, s.VA+uint64(o))
		}
	}
	return res
}

// Patch is a set of byte replacements located by a unique pattern.
type Patch struct {
	Name    string
	Pattern string // must match exactly once
	Off     uint64 // offset of replacement from match start
	New     []byte
	orig    []byte
	addr    uint64
	on      bool
}

func (g *Game) apply(p *Patch) error {
	if p.on {
		return nil
	}
	m := g.scanImage(p.Pattern)
	if len(m) != 1 {
		return fmt.Errorf("%s: pattern matched %d times, refusing", p.Name, len(m))
	}
	addr := g.Base + m[0] + p.Off
	orig, err := g.Read(addr, len(p.New))
	if err != nil {
		return err
	}
	if err := g.Write(addr, p.New); err != nil {
		return err
	}
	p.orig, p.addr, p.on = orig, addr, true
	return nil
}

func (g *Game) revert(p *Patch) error {
	if !p.on {
		return nil
	}
	p.on = false
	return g.Write(p.addr, p.orig)
}
