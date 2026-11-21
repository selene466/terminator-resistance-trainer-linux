package trainer

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Calling game functions from outside: stop a game thread at an execute breakpoint in a
// routine that already mutates the inventory (the fire routines), run the calls on that
// thread's own stack, then restore every register and resume. The return address is an
// int3 byte in .text, so the call traps back to us when it returns.

type callSpec struct {
	fn    uint64 // absolute address (0 when vcall is used)
	vcall string // "v$N+0xOFF": call [[result N]+OFF]
	args  []string
}

func fpRegs(op uintptr, tid int, buf *[512]byte) error {
	_, _, e := unix.Syscall6(unix.SYS_PTRACE, op, uintptr(tid), 0, uintptr(unsafe.Pointer(&buf[0])), 0, 0)
	if e != 0 {
		return e
	}
	return nil
}

func regByName(r *unix.PtraceRegs, n string) uint64 {
	return map[string]uint64{"rax": r.Rax, "rbx": r.Rbx, "rcx": r.Rcx, "rdx": r.Rdx, "rsi": r.Rsi, "rdi": r.Rdi,
		"rbp": r.Rbp, "r8": r.R8, "r9": r.R9, "r12": r.R12, "r13": r.R13, "r14": r.R14, "r15": r.R15}[n]
}

func parseCalls(s string, base uint64) ([]callSpec, error) {
	var out []callSpec
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		sep := "|"
		if !strings.HasPrefix(part, "v$") {
			sep = ":"
			if strings.Contains(part, "|") {
				sep = "|"
			}
		}
		fa := strings.SplitN(part, sep, 2)
		c := callSpec{}
		if strings.HasPrefix(fa[0], "v$") {
			c.vcall = fa[0]
		} else {
			rva, err := strconv.ParseUint(fa[0], 0, 64)
			if err != nil {
				return nil, err
			}
			c.fn = base + rva
		}
		if len(fa) == 2 && fa[1] != "" {
			c.args = strings.Split(fa[1], ",")
		}
		if len(c.args) > 6 {
			return nil, fmt.Errorf("max 6 args")
		}
		out = append(out, c)
	}
	return out, nil
}

// runCalls executes the calls on tid (stopped at the breakpoint). Returns rax of each call.
var errGuard = fmt.Errorf("guard mismatch")

func (g *Game) runCalls(tid int, wreg string, calls []callSpec, ret uint64, guardIdx int, guardVal uint64) ([]uint64, error) {
	var saved unix.PtraceRegs
	if err := unix.PtraceGetRegs(tid, &saved); err != nil {
		return nil, err
	}
	var fp [512]byte
	if err := fpRegs(14, tid, &fp); err != nil {
		return nil, fmt.Errorf("getfpregs: %w", err)
	}
	w := regByName(&saved, wreg)
	var results []uint64
	var rerr error
	for ci, c := range calls {
		r := saved
		r.Rsp = ((saved.Rsp - 0x400) &^ 0xF) - 8
		var rb [8]byte
		for i := 0; i < 8; i++ {
			rb[i] = byte(ret >> (8 * i))
		}
		if err := g.Write(r.Rsp, rb[:]); err != nil {
			rerr = err
			break
		}
		scratch := r.Rsp + 8 + 0x100
		argv := [6]uint64{}
		fnAddr := c.fn
		if c.vcall != "" {
			p := strings.SplitN(c.vcall[1:], "+", 2) // "$N+0xOFF"
			n, _ := strconv.Atoi(p[0][1:])
			off, _ := strconv.ParseUint(p[1], 0, 64)
			if n >= len(results) {
				rerr = fmt.Errorf("bad vcall ref %s", c.vcall)
				break
			}
			fnAddr = g.u64(g.u64(results[n]) + off)
			if fnAddr == 0 {
				rerr = fmt.Errorf("vcall %s resolved to null", c.vcall)
				break
			}
		}
		resolve := func(tok string) uint64 {
			if tok == "$w" {
				return w
			}
			n, _ := strconv.Atoi(tok[1:])
			if n < len(results) {
				return results[n]
			}
			rerr = fmt.Errorf("bad result ref %s", tok)
			return 0
		}
		for i, a := range c.args {
			a = strings.TrimSpace(a)
			switch {
			case strings.HasPrefix(a, "@"): // scratch memory on the injected stack
				off, _ := strconv.ParseUint(a[1:], 0, 64)
				argv[i] = scratch + off
			case strings.HasPrefix(a, "*"): // *$w+0x5f8 : qword read from base+off
				p := strings.SplitN(a[1:], "+", 2)
				off := uint64(0)
				if len(p) == 2 {
					off, _ = strconv.ParseUint(p[1], 0, 64)
				}
				b, err := g.Read(resolve(p[0])+off, 8)
				if err != nil || len(b) < 8 {
					rerr = fmt.Errorf("deref %s: %v", a, err)
				} else {
					for k := 0; k < 8; k++ {
						argv[i] |= uint64(b[k]) << (8 * k)
					}
				}
			case a == "$w":
				argv[i] = w
			case strings.HasPrefix(a, "$"):
				n, _ := strconv.Atoi(a[1:])
				if n >= len(results) {
					rerr = fmt.Errorf("bad result ref %s", a)
				} else {
					argv[i] = results[n]
				}
			default:
				v, err := strconv.ParseUint(a, 0, 64)
				if err != nil {
					rerr = err
				}
				argv[i] = v
			}
		}
		if rerr != nil {
			break
		}
		r.Rcx, r.Rdx, r.R8, r.R9 = argv[0], argv[1], argv[2], argv[3]
		for k := 4; k < 6; k++ { // stack arguments sit above the return address and shadow space
			var sb [8]byte
			for q := 0; q < 8; q++ {
				sb[q] = byte(argv[k] >> (8 * q))
			}
			if err := g.Write(r.Rsp+0x28+uint64(k-4)*8, sb[:]); err != nil {
				rerr = err
			}
		}
		if rerr != nil {
			break
		}
		r.Rip = fnAddr
		r.Orig_rax = ^uint64(0)
		r.Eflags &^= 1 << 8
		r.Eflags |= 1 << 16
		if err := unix.PtraceSetRegs(tid, &r); err != nil {
			rerr = err
			break
		}
		if err := unix.PtraceCont(tid, 0); err != nil {
			rerr = err
			break
		}
		done := false
		deadline := time.Now().Add(5 * time.Second)
		for !done && time.Now().Before(deadline) {
			var ws unix.WaitStatus
			p, err := unix.Wait4(tid, &ws, unix.WALL|unix.WNOHANG, nil)
			if err != nil || p == 0 {
				time.Sleep(time.Millisecond)
				continue
			}
			if !ws.Stopped() {
				rerr = fmt.Errorf("thread exited during call %d", ci)
				done = true
				break
			}
			var cur unix.PtraceRegs
			_ = unix.PtraceGetRegs(tid, &cur)
			if ws.StopSignal() == unix.SIGTRAP && cur.Rip == ret+1 {
				results = append(results, cur.Rax)
				done = true
				if ci == guardIdx && cur.Rax != guardVal {
					rerr = errGuard
				}
				break
			}
			if ws.StopSignal() == unix.SIGTRAP || ws.StopSignal() == unix.SIGSTOP {
				_ = unix.PtraceCont(tid, 0)
				continue
			}
			rerr = fmt.Errorf("call %d: signal %v at rip=%#x", ci, ws.StopSignal(), cur.Rip)
			break
		}
		if rerr != nil {
			break
		}
		if !done {
			rerr = fmt.Errorf("call %d timed out", ci)
			break
		}
	}
	// restore the interrupted state no matter what
	saved.Eflags |= 1 << 16
	_ = unix.PtraceSetRegs(tid, &saved)
	_ = fpRegs(15, tid, &fp)
	return results, rerr
}

type bpSite struct {
	addr uint64
	wreg string
}

func setWatches(tid int, addrs []uint64) {
	var dr7 uint64
	for i, a := range addrs {
		poke(tid, drOff+uintptr(i)*8, a)
		dr7 |= 1 << (2 * uint(i))
	}
	poke(tid, drOff+7*8, dr7)
}

// injectAt waits for any game thread to hit an execute breakpoint at one of the sites,
// runs the calls there, and resumes. If guardIdx >= 0 and that call's result differs from
// guardVal, the hit is skipped and waiting continues. dur bounds the wait.
func (t *Trainer) injectAt(sites []bpSite, dur time.Duration, calls []callSpec, guardIdx int, guardVal uint64) ([]uint64, error) {
	addrs := make([]uint64, len(sites))
	for i, st := range sites {
		addrs[i] = st.addr
	}
	site0 := addrs[0]
	g := t.game()
	if g == nil {
		return nil, fmt.Errorf("no game")
	}
	t.mu.Lock()
	retPad, err := g.findCave(site0, t.used)
	t.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var res []uint64
	var rerr error = fmt.Errorf("timed out waiting for breakpoint (fire a weapon)")
	onPT(func() {
		seized := map[int]bool{}
		arm := func(tid int) {
			var ws unix.WaitStatus
			_ = unix.PtraceInterrupt(tid)
			if _, err := unix.Wait4(tid, &ws, unix.WALL, nil); err != nil {
				return
			}
			setWatches(tid, addrs)
			_ = unix.PtraceCont(tid, 0)
		}
		for _, tid := range tids(g.PID) {
			if unix.PtraceSeize(tid) != nil {
				continue
			}
			_ = unix.PtraceSetOptions(tid, unix.PTRACE_O_TRACECLONE)
			seized[tid] = true
			arm(tid)
		}
		deadline := time.Now().Add(dur)
	loop:
		for time.Now().Before(deadline) {
			var ws unix.WaitStatus
			tid, err := unix.Wait4(-1, &ws, unix.WALL|unix.WNOHANG, nil)
			if err != nil || tid == 0 {
				time.Sleep(2 * time.Millisecond)
				continue
			}
			if !ws.Stopped() {
				delete(seized, tid)
				continue
			}
			sig := ws.StopSignal()
			ev := int(ws>>16) & 0xff
			switch {
			case sig == unix.SIGTRAP && ev == unix.PTRACE_EVENT_CLONE:
				nt, _ := unix.PtraceGetEventMsg(tid)
				_ = unix.PtraceCont(tid, 0)
				ntid := int(nt)
				seized[ntid] = true
				var ws2 unix.WaitStatus
				_, _ = unix.Wait4(ntid, &ws2, unix.WALL, nil)
				setWatches(ntid, addrs)
				_ = unix.PtraceCont(ntid, 0)
			case sig == unix.SIGTRAP && ev == 0:
				var cur unix.PtraceRegs
				if unix.PtraceGetRegs(tid, &cur) == nil {
					hit := -1
					for i, a := range addrs {
						if cur.Rip == a {
							hit = i
						}
					}
					if hit >= 0 {
						poke(tid, drOff+7*8, 0) // disarm this thread before running calls
						r, err := g.runCalls(tid, sites[hit].wreg, calls, retPad, guardIdx, guardVal)
						if err == errGuard {
							setWatches(tid, addrs) // not ours: keep waiting
							_ = unix.PtraceCont(tid, 0)
							continue
						}
						res, rerr = r, err
						_ = unix.PtraceCont(tid, 0)
						break loop
					}
				}
				poke(tid, drOff+6*8, 0)
				_ = unix.PtraceCont(tid, 0)
			case ev != 0:
				_ = unix.PtraceCont(tid, 0)
			default:
				_ = unix.PtraceCont(tid, int(sig))
			}
		}
		for tid := range seized {
			var ws unix.WaitStatus
			_ = unix.PtraceInterrupt(tid)
			_, _ = unix.Wait4(tid, &ws, unix.WALL, nil)
			poke(tid, drOff+7*8, 0)
			_ = unix.PtraceDetach(tid)
		}
	})
	return res, rerr
}
