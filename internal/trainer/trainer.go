package trainer

import (
	"fmt"
	"os"
	"sync"
	"time"
)

type Trainer struct {
	mu   sync.Mutex
	g    *Game
	quit chan struct{}
	log  *os.File
	hits []hit
	dmg  *damageHook
	hp   *healthHook
	used map[uint64]bool
	inv  uint64
}

func New() *Trainer {
	lf, _ := os.OpenFile(os.Getenv("HOME")+"/.cache/terminator-trainer.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	return &Trainer{quit: make(chan struct{}), log: lf, used: map[uint64]bool{}}
}

func (t *Trainer) Logf(f string, a ...any) {
	if t.log != nil {
		fmt.Fprintf(t.log, time.Now().Format("15:04:05 ")+f+"\n", a...)
	}
}

func (t *Trainer) Stop() {
	close(t.quit)
	t.mu.Lock()
	if t.g != nil {
		t.g.Close()
		t.g = nil
	}
	t.mu.Unlock()
}

// game returns the attached game, attaching lazily once the exe is mapped.
func (t *Trainer) game() *Game {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.g != nil {
		return t.g
	}
	g, err := attach()
	if err != nil {
		return nil
	}
	t.g = g
	t.Logf("attached pid=%d base=%#x", g.PID, g.Base)
	return g
}

func (t *Trainer) Run() {
	for {
		select {
		case <-t.quit:
			return
		case <-time.After(time.Second):
		}
		t.game()
	}
}
