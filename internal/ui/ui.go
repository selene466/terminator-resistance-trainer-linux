package ui

import (
	"fmt"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"

	"terminator-trainer/internal/trainer"
)

const (
	ksF10       = 0xffc7
	ksEscape    = 0xff1b
	ksBackSpace = 0xff08
	ksReturn    = 0xff0d
	ksLeft      = 0xff51
	ksUp        = 0xff52
	ksRight     = 0xff53
	ksDown      = 0xff54
	ksOne       = 0x31
)

type UI struct {
	t       *trainer.Trainer
	conn    *xgb.Conn
	root    xproto.Window
	win     xproto.Window
	gc      xproto.Gcontext
	keys    map[uint32]xproto.Keycode
	mu      sync.Mutex
	open    bool
	page    int // 0 main, 1 inventory
	items   []*trainer.Item
	rows    []trainer.InvEntry
	srows   []trainer.SpawnEntry
	busy    bool
	sel     int
	msg     string
	grabbed []uint32
}

var lockMods = []uint16{0, xproto.ModMaskLock, xproto.ModMask2, xproto.ModMaskLock | xproto.ModMask2}

const (
	winW    = 400
	maxRows = 16
)

func Run(t *trainer.Trainer, items []*trainer.Item) {
	u := &UI{t: t, items: items}
	var err error
	if u.conn, err = xgb.NewConn(); err != nil {
		t.Logf("ui: x11 connect: %v", err)
		return
	}
	defer u.conn.Close()
	scr := xproto.Setup(u.conn).DefaultScreen(u.conn)
	u.root = scr.Root
	if err := u.loadKeys(); err != nil {
		t.Logf("ui: keymap: %v", err)
		return
	}
	u.win, _ = xproto.NewWindowId(u.conn)
	xproto.CreateWindow(u.conn, scr.RootDepth, u.win, u.root, 40, 40, winW, u.height(), 2,
		xproto.WindowClassInputOutput, scr.RootVisual,
		xproto.CwBackPixel|xproto.CwBorderPixel|xproto.CwOverrideRedirect|xproto.CwEventMask,
		[]uint32{0x101010, 0xc02020, 1, xproto.EventMaskExposure})
	font, _ := xproto.NewFontId(u.conn)
	opened := false
	for _, n := range []string{"9x15bold", "9x15", "10x20", "fixed"} {
		if xproto.OpenFontChecked(u.conn, font, uint16(len(n)), n).Check() == nil {
			opened = true
			break
		}
	}
	if !opened {
		t.Logf("ui: no usable core font")
		return
	}
	u.gc, _ = xproto.NewGcontextId(u.conn)
	xproto.CreateGC(u.conn, u.gc, xproto.Drawable(u.win),
		xproto.GcForeground|xproto.GcBackground|xproto.GcFont,
		[]uint32{0xf0f0f0, 0x101010, uint32(font)})

	if err := u.grab(ksF10); err != nil {
		t.Logf("ui: cannot grab F10: %v", err)
		return
	}
	t.Logf("ui: ready, F10 grabbed")
	for {
		ev, err := u.conn.WaitForEvent()
		if err != nil {
			t.Logf("ui: x error: %v", err)
			continue
		}
		if ev == nil {
			return
		}
		switch e := ev.(type) {
		case xproto.ExposeEvent:
			u.draw()
		case xproto.KeyPressEvent:
			u.onKey(e.Detail)
		}
	}
}

func (u *UI) height() uint16 {
	n := len(u.items)
	if u.page == 1 || u.page == 2 {
		n = len(u.rows)
		if u.page == 2 {
			n = len(u.srows)
		}
		if n > maxRows {
			n = maxRows
		}
		n += 2 // hint + message
	}
	return uint16(46 + 22*n)
}

func (u *UI) loadKeys() error {
	setup := xproto.Setup(u.conn)
	n := byte(setup.MaxKeycode-setup.MinKeycode) + 1
	m, err := xproto.GetKeyboardMapping(u.conn, setup.MinKeycode, n).Reply()
	if err != nil {
		return err
	}
	u.keys = map[uint32]xproto.Keycode{}
	per := int(m.KeysymsPerKeycode)
	for i := 0; i < int(n); i++ {
		ks := uint32(m.Keysyms[i*per])
		if _, ok := u.keys[ks]; !ok {
			u.keys[ks] = setup.MinKeycode + xproto.Keycode(i)
		}
	}
	return nil
}

func (u *UI) grab(ks uint32) error {
	kc, ok := u.keys[ks]
	if !ok {
		return fmt.Errorf("keysym %#x not on keyboard", ks)
	}
	for _, m := range lockMods {
		if err := xproto.GrabKeyChecked(u.conn, true, u.root, m, kc, xproto.GrabModeAsync, xproto.GrabModeAsync).Check(); err != nil {
			return err
		}
	}
	return nil
}

func (u *UI) ungrab(ks uint32) {
	kc, ok := u.keys[ks]
	if !ok {
		return
	}
	for _, m := range lockMods {
		xproto.UngrabKey(u.conn, kc, u.root, m)
	}
}

// regrab swaps the temporary key grabs to match the current page.
func (u *UI) regrab() {
	for _, k := range u.grabbed {
		u.ungrab(k)
	}
	u.grabbed = nil
	if !u.open {
		return
	}
	var ks []uint32
	if u.page == 0 {
		ks = []uint32{ksEscape}
		for i := range u.items {
			ks = append(ks, ksOne+uint32(i))
		}
	} else {
		ks = []uint32{ksEscape, ksBackSpace, ksUp, ksDown, ksLeft, ksRight, ksReturn}
	}
	for _, k := range ks {
		if u.grab(k) == nil {
			u.grabbed = append(u.grabbed, k)
		}
	}
}

func (u *UI) resize() {
	xproto.ConfigureWindow(u.conn, u.win, xproto.ConfigWindowHeight, []uint32{uint32(u.height())})
}

func (u *UI) onKey(code xproto.Keycode) {
	u.mu.Lock()
	defer u.mu.Unlock()
	is := func(ks uint32) bool { return code == u.keys[ks] }
	switch {
	case is(ksF10):
		if u.open {
			u.hide()
		} else {
			u.show()
		}
		return
	case !u.open:
		return
	case u.page == 0:
		if is(ksEscape) {
			u.hide()
			return
		}
		for i, it := range u.items {
			if is(ksOne + uint32(i)) {
				if it.Page == 1 {
					u.openInventory()
					return
				}
				if it.Page == 2 {
					u.openSpawn()
					return
				}
				u.toggle(it)
			}
		}
	case u.page == 2:
		switch {
		case is(ksEscape), is(ksBackSpace):
			u.page, u.msg = 0, ""
			u.regrab()
			u.resize()
		case is(ksUp):
			if u.sel > 0 {
				u.sel--
			}
		case is(ksDown):
			if u.sel < len(u.srows)-1 {
				u.sel++
			}
		case is(ksReturn):
			u.startSpawn()
		}
	default:
		switch {
		case is(ksEscape), is(ksBackSpace):
			u.page, u.msg = 0, ""
			u.regrab()
			u.resize()
		case is(ksUp):
			if u.sel > 0 {
				u.sel--
			}
		case is(ksDown):
			if u.sel < len(u.rows)-1 {
				u.sel++
			}
		case is(ksLeft):
			u.adjust(-10)
		case is(ksRight):
			u.adjust(10)
		case is(ksReturn):
			u.adjust(100)
		}
	}
	u.drawLocked()
}

func (u *UI) openInventory() {
	rows, err := u.t.ListInventory()
	u.rows, u.sel, u.msg = rows, 0, ""
	if err != nil {
		u.msg = err.Error()
	}
	u.page = 1
	u.regrab()
	u.resize()
	u.drawLocked()
}

func (u *UI) openSpawn() {
	rows, err := u.t.SpawnList()
	u.srows, u.sel, u.msg = rows, 0, ""
	if err != nil {
		u.msg = err.Error()
	} else if len(rows) == 0 {
		u.msg = "no spawnable weapons found"
	}
	u.page = 2
	u.regrab()
	u.resize()
	u.drawLocked()
}

func (u *UI) startSpawn() {
	if u.sel >= len(u.srows) {
		return
	}
	if u.busy {
		u.msg = "already waiting: fire a weapon"
		return
	}
	e := u.srows[u.sel]
	u.busy = true
	u.msg = "FIRE A WEAPON to get " + e.Name
	go func() {
		err := u.t.SpawnWeapon(e)
		u.mu.Lock()
		defer u.mu.Unlock()
		u.busy = false
		if err != nil {
			u.msg = e.Name + ": " + err.Error()
		} else {
			u.msg = e.Name + " added (+" + fmt.Sprint(trainer.SpawnAmmo) + " ammo)"
			// it is now owned: drop it from the list
			for i, r := range u.srows {
				if r.Name == e.Name {
					u.srows = append(u.srows[:i], u.srows[i+1:]...)
					break
				}
			}
			if u.sel >= len(u.srows) && u.sel > 0 {
				u.sel--
			}
		}
		u.drawLocked()
	}()
}

func (u *UI) adjust(delta int32) {
	if u.sel >= len(u.rows) {
		return
	}
	e := u.rows[u.sel]
	if e.Weapon {
		u.msg = "weapons have no count to change"
		return
	}
	n, err := u.t.AddCount(e, delta)
	if err != nil {
		u.msg = err.Error()
		return
	}
	u.rows[u.sel].Count = n
	u.msg = ""
}

func (u *UI) show() {
	u.open, u.page = true, 0
	u.resize()
	xproto.MapWindow(u.conn, u.win)
	xproto.ConfigureWindow(u.conn, u.win, xproto.ConfigWindowStackMode, []uint32{xproto.StackModeAbove})
	u.regrab()
	u.drawLocked()
}

func (u *UI) hide() {
	u.open = false
	xproto.UnmapWindow(u.conn, u.win)
	u.regrab()
}

func (u *UI) toggle(it *trainer.Item) {
	it.Err = ""
	if !it.Ready {
		it.Err = "not available yet"
		return
	}
	want := !it.On
	if want && it.Group != 0 {
		for _, o := range u.items {
			if o != it && o.Group == it.Group && o.On {
				if err := o.Toggle(false); err == nil {
					o.On = false
				}
			}
		}
	}
	if err := it.Toggle(want); err != nil {
		it.Err = err.Error()
		u.t.Logf("toggle %s: %v", it.Name, err)
		return
	}
	it.On = want
}

func (u *UI) draw() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.drawLocked()
}

func (u *UI) text(x, y int16, s string) {
	if len(s) > 255 {
		s = s[:255]
	}
	xproto.ImageText8(u.conn, byte(len(s)), xproto.Drawable(u.win), u.gc, x, y, s)
}

func (u *UI) drawLocked() {
	if !u.open {
		return
	}
	xproto.ClearArea(u.conn, false, u.win, 0, 0, 0, 0)
	if u.page == 2 {
		u.text(12, 22, "SPAWN WEAPON   [Bksp] back")
		first := 0
		if u.sel >= maxRows {
			first = u.sel - maxRows + 1
		}
		y := int16(48)
		for i := first; i < len(u.srows) && i < first+maxRows; i++ {
			mark := "  "
			if i == u.sel {
				mark = "> "
			}
			u.text(12, y, mark+u.srows[i].Name)
			y += 22
		}
		u.text(12, y, "Enter: spawn (then fire a weapon)")
		if u.msg != "" {
			u.text(12, y+22, u.msg)
		}
		return
	}
	if u.page == 1 {
		u.text(12, 22, "INVENTORY   [Bksp] back")
		first := 0
		if u.sel >= maxRows {
			first = u.sel - maxRows + 1
		}
		y := int16(48)
		for i := first; i < len(u.rows) && i < first+maxRows; i++ {
			mark := "  "
			if i == u.sel {
				mark = "> "
			}
			e := u.rows[i]
			cnt := fmt.Sprintf("%d", e.Count)
			if e.Weapon {
				cnt = "weapon"
			}
			u.text(12, y, fmt.Sprintf("%s%-26s %s", mark, e.Name, cnt))
			y += 22
		}
		u.text(12, y, "Left/Right -/+10   Enter +100")
		if u.msg != "" {
			u.text(12, y+22, u.msg)
		}
		return
	}
	u.text(12, 22, "TERMINATOR TRAINER   [F10] close")
	for i, it := range u.items {
		line := fmt.Sprintf("[%d] %-18s", i+1, it.Name)
		if it.Page != 0 {
			line += " >"
		} else if it.On {
			line += " ON"
		} else {
			line += " OFF"
		}
		if it.Err != "" {
			line += " (" + it.Err + ")"
		}
		u.text(12, int16(48+22*i), line)
	}
}
