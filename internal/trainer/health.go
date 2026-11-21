package trainer

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// Unlimited HP. Player damage goes through
//
//	movss xmm1,[rbx+0xEC]  ; HP
//	xorps xmm0,xmm0
//	subss xmm1,[rdi]       ; HP -= damage      <- site (4 bytes)
//	comiss xmm1,xmm0       ;                    (3 bytes, moved into the cave)
//
// A trace showed the only victim here is the player object, vtable RVA 0x31A4C38.
// The cave skips the subtract when [rbx] (low 32 bits) equals that vtable, so other
// victims are unaffected.
const (
	healthPattern = "F3 0F 10 8B EC 00 00 00 0F 57 C0 F3 0F 5C 0F 0F 2F C8"
	healthOff     = 11
	playerVtable  = 0x31A4C38
	healthLen     = 7
)

type healthHook struct {
	site, cave uint64
	orig       []byte
	hooked     bool
}

func healthCave(cave, site uint64, vt uint32) []byte {
	c := []byte{0x81, 0x3B} // cmp dword ptr [rbx], imm32
	var v [4]byte
	binary.LittleEndian.PutUint32(v[:], vt)
	c = append(c, v[:]...)
	c = append(c, 0x74, 0x04)             // je skip
	c = append(c, 0xF3, 0x0F, 0x5C, 0x0F) // subss xmm1,[rdi]
	c = append(c, 0x0F, 0x2F, 0xC8)       // comiss xmm1,xmm0
	c = append(c, 0xE9)
	return append(c, rel32(cave+uint64(len(c))+5, site+healthLen)...)
}

func (t *Trainer) SetGodMode(on bool) error {
	g := t.game()
	if g == nil {
		return errors.New("game not attached")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	h := t.hp
	if !on {
		if h != nil && h.hooked {
			if err := g.Write(h.site+5, h.orig[5:]); err != nil {
				return err
			}
			if err := g.Write(h.site, h.orig[:5]); err != nil {
				return err
			}
			h.hooked = false
		}
		return nil
	}
	if h == nil {
		m := g.scanImage(healthPattern)
		if len(m) != 1 {
			return fmt.Errorf("health pattern matched %d times, refusing", len(m))
		}
		site := g.Base + m[0] + healthOff
		orig, err := g.Read(site, healthLen)
		if err != nil || len(orig) != healthLen {
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
		h = &healthHook{site: site, cave: cave, orig: orig}
		t.used[cave] = true
		t.hp = h
	}
	code := healthCave(h.cave, h.site, uint32(g.Base+playerVtable))
	if err := g.Write(h.cave, code); err != nil {
		return err
	}
	if err := g.Write(h.site+5, []byte{0x90, 0x90}); err != nil {
		return err
	}
	if err := g.Write(h.site, append([]byte{0xE9}, rel32(h.site+5, h.cave)...)); err != nil {
		return err
	}
	h.hooked = true
	return nil
}
