package trainer

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// Layout (found by tracing, game build specific):
//
//	inventory object : vtable RVA 0x3195340, entries TArray at +0x40 (Data, Num, Max)
//	entry (0x18)     : key UObject* (item class) +0, count int32 +8, flag int32 +0xC
//	                   (1 = weapon with an instance), instance* +0x10
//	key object       : FName index (int32) at +0x18
//	name pool        : block table at RVA 0x42BC990, 64K entries per block,
//	                   entry = u16 header (len<<6 | wide) + chars, 2-byte stride
const (
	invVtableRVA = 0x3195340
	keyVtableRVA = 0x371ae08
	namePoolRVA  = 0x42BC990
	entrySize    = 0x18
	maxCount     = 99999
)

type InvEntry struct {
	Addr   uint64
	Key    uint64
	Name   string
	Count  int32
	Weapon bool
}

func (g *Game) u64(a uint64) uint64 {
	b, err := g.Read(a, 8)
	if err != nil || len(b) < 8 {
		return 0
	}
	return binary.LittleEndian.Uint64(b)
}

func (g *Game) i32(a uint64) int32 {
	b, err := g.Read(a, 4)
	if err != nil || len(b) < 4 {
		return 0
	}
	return int32(binary.LittleEndian.Uint32(b))
}

func (g *Game) fname(idx int32) string {
	blk := g.u64(g.Base + namePoolRVA + uint64(idx>>16)*8)
	if blk == 0 {
		return ""
	}
	off := uint64(idx&0xffff) * 2
	h, err := g.Read(blk+off, 2)
	if err != nil || len(h) < 2 {
		return ""
	}
	hd := binary.LittleEndian.Uint16(h)
	n, wide := int(hd>>6), hd&1 == 1
	if n == 0 || n > 200 || wide {
		return ""
	}
	s, _ := g.Read(blk+off+2, n)
	return string(s)
}

func (g *Game) validInventory(inv uint64) (data uint64, num int, ok bool) {
	if g.u64(inv) != g.Base+invVtableRVA {
		return 0, 0, false
	}
	data = g.u64(inv + 0x40)
	num = int(g.i32(inv + 0x48))
	mx := int(g.i32(inv + 0x4c))
	if data == 0 || num <= 0 || num > mx || mx > 4096 {
		return 0, 0, false
	}
	if g.u64(g.u64(data)) != g.Base+keyVtableRVA {
		return 0, 0, false
	}
	return data, num, true
}

// findInventory scans writable memory for inventory objects and returns the populated one.
func (g *Game) findInventory() (uint64, error) {
	rs, err := readMaps(g.PID)
	if err != nil {
		return 0, err
	}
	want := make([]byte, 8)
	binary.LittleEndian.PutUint64(want, g.Base+invVtableRVA)
	var best uint64
	bestNum := 0
	for _, r := range rs {
		if !strings.HasPrefix(r.Perms, "rw") || r.Path != "" || r.End-r.Start > 1<<30 {
			continue
		}
		buf, _ := g.Read(r.Start, int(r.End-r.Start))
		for o := 0; o+8 <= len(buf); o += 8 {
			if binary.LittleEndian.Uint64(buf[o:]) != g.Base+invVtableRVA {
				continue
			}
			if _, n, ok := g.validInventory(r.Start + uint64(o)); ok && n > bestNum {
				best, bestNum = r.Start+uint64(o), n
			}
		}
	}
	if best == 0 {
		return 0, errors.New("inventory not found (load into a level first)")
	}
	return best, nil
}

func (t *Trainer) inventory() (*Game, uint64, error) {
	g := t.game()
	if g == nil {
		return nil, 0, errors.New("game not attached")
	}
	t.mu.Lock()
	inv := t.inv
	t.mu.Unlock()
	if inv != 0 {
		if _, _, ok := g.validInventory(inv); ok {
			return g, inv, nil
		}
	}
	inv, err := g.findInventory()
	if err != nil {
		return nil, 0, err
	}
	t.mu.Lock()
	t.inv = inv
	t.mu.Unlock()
	return g, inv, nil
}

func (t *Trainer) ListInventory() ([]InvEntry, error) {
	g, inv, err := t.inventory()
	if err != nil {
		return nil, err
	}
	data, num, ok := g.validInventory(inv)
	if !ok {
		return nil, errors.New("inventory changed, reopen the menu")
	}
	var out []InvEntry
	for i := 0; i < num; i++ {
		a := data + uint64(i)*entrySize
		key := g.u64(a)
		if key == 0 {
			continue
		}
		name := strings.TrimSuffix(g.fname(g.i32(key+0x18)), "_C")
		if name == "" {
			name = fmt.Sprintf("item %#x", key)
		}
		out = append(out, InvEntry{Addr: a, Key: key, Name: name, Count: g.i32(a + 8), Weapon: g.i32(a+0xC) == 1})
	}
	return out, nil
}

// AddCount changes an entry's count (ammo/consumables only), clamped to [0, maxCount].
func (t *Trainer) AddCount(e InvEntry, delta int32) (int32, error) {
	g, inv, err := t.inventory()
	if err != nil {
		return 0, err
	}
	data, num, ok := g.validInventory(inv)
	if !ok || e.Addr < data || e.Addr >= data+uint64(num)*entrySize {
		return 0, errors.New("inventory changed, reopen the menu")
	}
	if g.u64(e.Addr) != e.Key {
		return 0, errors.New("entry moved, reopen the menu")
	}
	if g.i32(e.Addr+0xC) == 1 {
		return 0, errors.New("weapons have no count to change")
	}
	n := g.i32(e.Addr+8) + delta
	if n < 0 {
		n = 0
	}
	if n > maxCount {
		n = maxCount
	}
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(n))
	return n, g.Write(e.Addr+8, b[:])
}

// findClasses lists loaded item class objects whose name starts with prefix.
func (g *Game) findClasses(prefix string) map[string][]uint64 {
	out := map[string][]uint64{}
	rs, err := readMaps(g.PID)
	if err != nil {
		return out
	}
	vt := g.Base + keyVtableRVA
	for _, r := range rs {
		if !strings.HasPrefix(r.Perms, "rw") || r.Path != "" || r.End-r.Start > 1<<30 {
			continue
		}
		buf, _ := g.Read(r.Start, int(r.End-r.Start))
		for o := 0; o+0x20 <= len(buf); o += 8 {
			if binary.LittleEndian.Uint64(buf[o:]) != vt {
				continue
			}
			idx := int32(binary.LittleEndian.Uint32(buf[o+0x18:]))
			if idx <= 0 {
				continue
			}
			if n := g.fname(idx); strings.HasPrefix(n, prefix) {
				out[n] = append(out[n], r.Start+uint64(o))
			}
		}
	}
	return out
}
