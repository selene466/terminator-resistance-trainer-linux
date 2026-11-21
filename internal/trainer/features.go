package trainer

import (
	"errors"

	"terminator-trainer/internal/mod"
)

type Item struct {
	Name   string
	Group  int // items with the same non-zero group are mutually exclusive
	Ready  bool
	On     bool
	Page   int // 1 = inventory page, 2 = spawn page (instead of toggling)
	Toggle func(on bool) error
	Err    string
}

func (t *Trainer) Items() []*Item {
	// Per shot the fire routine does two things: magazine -= perShot (sub [rdi+0x6F0],eax)
	// and removes the same amount from the inventory ammo total (call a972e0). The HUD
	// reserve is derived from the total, so both must be skipped. Patterns verified unique.
	ammoPatches := []*Patch{
		{
			Name:    "ammo-mag",
			Pattern: "8B 87 F0 05 00 00 48 8B CF 29 87 F0 06 00 00 E8",
			Off:     9,
			New:     []byte{0x90, 0x90, 0x90, 0x90, 0x90, 0x90},
		},
		{
			Name:    "ammo-inventory",
			Pattern: "45 8B C7 49 8B D6 48 8B CE E8 ? ? ? ? 48 8B CF E8",
			Off:     9,
			New:     []byte{0x90, 0x90, 0x90, 0x90, 0x90},
		},
	}
	// pump shotgun: its fire routine decrements the same field with dec [rbx+0x6F0]
	ammoPatches = append(ammoPatches, &Patch{
		Name:    "ammo-shotgun",
		Pattern: "FF 8B F0 06 00 00 48 8B CB E8 22 AD FA FF",
		Off:     0,
		New:     []byte{0x90, 0x90, 0x90, 0x90, 0x90, 0x90},
	})
	// the shotgun routine also removes the shot from the inventory total (amount 1);
	// without this the total drains, goes negative and the game clamps the magazine to it
	ammoPatches = append(ammoPatches, &Patch{
		Name:    "ammo-shotgun-inventory",
		Pattern: "41 B8 01 00 00 00 49 8B CE E8 98 5D EA FF",
		Off:     9,
		New:     []byte{0x90, 0x90, 0x90, 0x90, 0x90},
	})
	patchToggle := func(ps []*Patch) func(bool) error {
		return func(on bool) error {
			g := t.game()
			if g == nil {
				return errors.New("game not attached")
			}
			if !on {
				var first error
				for _, p := range ps {
					if err := g.revert(p); err != nil && first == nil {
						first = err
					}
				}
				return first
			}
			for i, p := range ps {
				if err := g.apply(p); err != nil {
					for _, q := range ps[:i] {
						_ = g.revert(q)
					}
					return err
				}
			}
			return nil
		}
	}
	return []*Item{
		{Name: "Unlimited ammo", Ready: true, Toggle: patchToggle(ammoPatches)},
		{Name: "Damage x2", Group: 1, Ready: true, Toggle: func(on bool) error { return t.SetDamage(map[bool]int{true: 2, false: 0}[on]) }},
		{Name: "Damage x3", Group: 1, Ready: true, Toggle: func(on bool) error { return t.SetDamage(map[bool]int{true: 3, false: 0}[on]) }},
		{Name: "Unlimited HP", Ready: true, Toggle: func(on bool) error { return t.SetGodMode(on) }},
		{Name: "EXP 270/level (restart)", Ready: true, On: mod.Installed(), Toggle: mod.Set},
		{Name: "Inventory", Ready: true, Page: 1},
		{Name: "Spawn weapon", Ready: true, Page: 2},
	}
}
