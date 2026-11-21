package trainer

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const exeName = "Terminator-Win64-Shipping.exe"

type Region struct {
	Start, End uint64
	Perms      string
	Path       string
}

type Game struct {
	PID  int
	mem  *os.File
	Base uint64
}

// findPID returns the wine process that has the game exe mapped.
func findPID() (int, error) {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return 0, err
	}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		f, err := os.Open(fmt.Sprintf("/proc/%d/maps", pid))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		found := false
		for sc.Scan() {
			if strings.HasSuffix(sc.Text(), exeName) {
				found = true
				break
			}
		}
		f.Close()
		if found {
			return pid, nil
		}
	}
	return 0, errors.New("game process not found")
}

func readMaps(pid int) ([]Region, error) {
	f, err := os.Open(fmt.Sprintf("/proc/%d/maps", pid))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Region
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		fl := strings.Fields(sc.Text())
		if len(fl) < 5 {
			continue
		}
		var r Region
		a := strings.SplitN(fl[0], "-", 2)
		r.Start, _ = strconv.ParseUint(a[0], 16, 64)
		r.End, _ = strconv.ParseUint(a[1], 16, 64)
		r.Perms = fl[1]
		if len(fl) >= 6 {
			r.Path = strings.Join(fl[5:], " ")
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

func attach() (*Game, error) {
	pid, err := findPID()
	if err != nil {
		return nil, err
	}
	m, err := os.OpenFile(fmt.Sprintf("/proc/%d/mem", pid), os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open mem: %w", err)
	}
	regs, err := readMaps(pid)
	if err != nil {
		m.Close()
		return nil, err
	}
	g := &Game{PID: pid, mem: m}
	for _, r := range regs {
		if strings.HasSuffix(r.Path, exeName) && (g.Base == 0 || r.Start < g.Base) {
			g.Base = r.Start
		}
	}
	if g.Base == 0 {
		m.Close()
		return nil, errors.New("exe base not found")
	}
	// the PE header must start at the base
	h := make([]byte, 2)
	if _, err := m.ReadAt(h, int64(g.Base)); err != nil || !bytes.Equal(h, []byte("MZ")) {
		m.Close()
		return nil, fmt.Errorf("no MZ at base %#x (err=%v)", g.Base, err)
	}
	return g, nil
}

func (g *Game) Read(addr uint64, n int) ([]byte, error) {
	b := make([]byte, n)
	k, err := g.mem.ReadAt(b, int64(addr))
	return b[:k], err
}

func (g *Game) Write(addr uint64, b []byte) error {
	_, err := g.mem.WriteAt(b, int64(addr))
	return err
}

func (g *Game) Close() { g.mem.Close() }
