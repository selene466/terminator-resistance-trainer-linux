package trainer

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Weapon spawn: replicates the game's own grant sequence on a game thread, stopped at a
// weapon fire routine (so you fire once to apply it):
//
//	inv    = GetInventory(weapon)                      0xB9C220   (guarded == player's inventory)
//	pawn   = GetOwner(weapon)                          0x23013D0
//	world  = pawn->vtable[0x150](pawn)
//	params = FActorSpawnParameters ctor(scratch)       0x2717920
//	actor  = UWorld::SpawnActor(world, class, 0, 0, params)  0x23D3CB0
//	AddItem(inv, actor, 1, 0)                          0xA8DD70
//	AddItemByClass(inv, actor->ammoClass, N)           0xA8DCE0   (actor+0x5F8 = ammo class)
const SpawnAmmo = 200

var notPlayerWeapons = map[string]bool{"WP_NoWeapon": true, "WP_GrabWeapon": true}

var fireSites = []struct {
	rva  uint64
	wreg string
}{
	{0xBF0D0F, "rdi"}, // M16 / plasma / uzi family: sub [rdi+0x6F0],eax
	{0xBF14F0, "rbx"}, // shotgun: dec [rbx+0x6F0]
}

type SpawnEntry struct {
	Name  string
	Class uint64
}

func (t *Trainer) SpawnList() ([]SpawnEntry, error) {
	g, inv, err := t.inventory()
	if err != nil {
		return nil, err
	}
	data, num, ok := g.validInventory(inv)
	if !ok {
		return nil, errors.New("inventory not found")
	}
	owned := map[string]bool{}
	meta := g.u64(g.u64(data) + 0x10) // metaclass shared by item class objects
	for i := 0; i < num; i++ {
		if k := g.u64(data + uint64(i)*entrySize); k != 0 {
			owned[strings.TrimSuffix(g.fname(g.i32(k+0x18)), "_C")] = true
		}
	}
	var out []SpawnEntry
	for n, as := range g.findClasses("WP_") {
		name := strings.TrimSuffix(n, "_C")
		if owned[name] || notPlayerWeapons[name] {
			continue
		}
		for _, a := range as {
			if g.u64(a+0x10) == meta {
				out = append(out, SpawnEntry{Name: name, Class: a})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SpawnWeapon blocks until the player fires a weapon (or the wait times out).
func (t *Trainer) SpawnWeapon(e SpawnEntry) error {
	g, inv, err := t.inventory()
	if err != nil {
		return err
	}
	spec := fmt.Sprintf("0xb9c220|$w ; 0x23013d0|$w ; v$1+0x150|$1 ; 0x2717920|@0 ; "+
		"0x23d3cb0|$2,%#x,0,0,$3 ; 0xa8dd70|$0,$4,1,0 ; 0xa8dce0|$0,*$4+0x5f8,%d", e.Class, SpawnAmmo)
	calls, err := parseCalls(spec, g.Base)
	if err != nil {
		return err
	}
	var sites []bpSite
	for _, s := range fireSites {
		sites = append(sites, bpSite{g.Base + s.rva, s.wreg})
	}
	res, err := t.injectAt(sites, 120*time.Second, calls, 0, inv)
	if err != nil {
		return err
	}
	if len(res) < 6 || res[4] == 0 {
		return errors.New("game refused to spawn the weapon")
	}
	if res[5]&0xff == 0 {
		return errors.New("spawned, but the inventory did not accept it")
	}
	t.Logf("spawned %s actor=%#x", e.Name, res[4])
	return nil
}
