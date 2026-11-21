package trainer

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Damage hook. The shared damage routine does
//
//	movss xmm0,[r14+0xEC]   ; victim HP        (left untouched)
//	subss xmm0,[rbp]        ; minus damage     (5 bytes, replaced by jmp cave)
//
// Traces showed only enemy victims reach it (the player takes damage elsewhere).
// The cave sits in int3 padding inside .text (longest run there is 21 bytes) and
// repeats the same subss N times (2 or 3) before jumping back. No constants, no
// extra registers, so it fits in 20 bytes.
const damagePattern = "F3 41 0F 10 86 EC 00 00 00 F3 0F 5C 45 00"

const (
	siteOff  = 9 // subss starts 9 bytes into the pattern
	caveSize = 20
)

type damageHook struct {
	site, cave uint64 // site = address of the subss
	orig       []byte // original 5 bytes
	hooked     bool
}

// findCave returns the start of the int3 run (>= caveSize) nearest to near.
func (g *Game) findCave(near uint64, used map[uint64]bool) (uint64, error) {
	var best uint64
	bestDist := uint64(math.MaxUint64)
	for _, s := range g.sections() {
		if !s.Executable {
			continue
		}
		buf, _ := g.Read(g.Base+s.VA, int(s.VSize))
		run := 0
		for i := 0; i <= len(buf); i++ {
			if i < len(buf) && buf[i] == 0xCC {
				run++
				continue
			}
			if run >= caveSize && !used[g.Base+s.VA+uint64(i-run)] {
				a := g.Base + s.VA + uint64(i-run)
				d := a - near
				if a < near {
					d = near - a
				}
				if d < bestDist && d < 1<<30 {
					best, bestDist = a, d
				}
			}
			run = 0
		}
	}
	if best == 0 {
		return 0, errors.New("no int3 padding run large enough")
	}
	return best, nil
}

func rel32(from, to uint64) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(int32(int64(to)-int64(from))))
	return b[:]
}

func caveCode(cave, site uint64, n int) []byte {
	var c []byte
	for i := 0; i < n; i++ {
		c = append(c, 0xF3, 0x0F, 0x5C, 0x45, 0x00) // subss xmm0,[rbp]
	}
	c = append(c, 0xE9)
	c = append(c, rel32(cave+uint64(len(c))+5, site+5)...) // back to the instruction after subss
	return c
}

// SetDamage enables the damage multiplier (n = 2 or 3) or disables it (n = 0).
func (t *Trainer) SetDamage(n int) error {
	g := t.game()
	if g == nil {
		return errors.New("game not attached")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	h := t.dmg
	if h != nil && h.hooked {
		// unhook first so the cave is never rewritten while it can be entered
		if err := g.Write(h.site, h.orig); err != nil {
			return err
		}
		h.hooked = false
	}
	if n == 0 {
		return nil
	}
	if h == nil {
		m := g.scanImage(damagePattern)
		if len(m) != 1 {
			return fmt.Errorf("damage pattern matched %d times, refusing", len(m))
		}
		site := g.Base + m[0] + siteOff
		orig, err := g.Read(site, 5)
		if err != nil || len(orig) != 5 {
			return fmt.Errorf("read site: %v", err)
		}
		cave, err := g.findCave(site, t.used)
		if err != nil {
			return err
		}
		free, err := g.Read(cave, caveSize)
		if err != nil || !bytes.Equal(free, bytes.Repeat([]byte{0xCC}, caveSize)) {
			return errors.New("cave memory is not free padding")
		}
		h = &damageHook{site: site, cave: cave, orig: orig}
		t.used[cave] = true
		t.dmg = h
	}
	code := caveCode(h.cave, h.site, n)
	if err := g.Write(h.cave, code); err != nil {
		return err
	}
	if err := g.Write(h.site, append([]byte{0xE9}, rel32(h.site+5, h.cave)...)); err != nil {
		return err
	}
	h.hooked = true
	return nil
}
