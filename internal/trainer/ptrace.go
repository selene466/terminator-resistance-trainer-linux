package trainer

import (
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

// All ptrace calls must come from the same OS thread.
var ptCh = make(chan func())

func init() {
	go func() {
		runtime.LockOSThread()
		for f := range ptCh {
			f()
		}
	}()
}

func onPT(f func()) {
	d := make(chan struct{})
	ptCh <- func() { f(); close(d) }
	<-d
}

const drOff = 848 // offsetof(struct user, u_debugreg) on x86_64

func tids(pid int) []int {
	ents, _ := os.ReadDir(fmt.Sprintf("/proc/%d/task", pid))
	var out []int
	for _, e := range ents {
		if n, err := strconv.Atoi(e.Name()); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func poke(tid int, off uintptr, v uint64) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	_, _ = unix.PtracePokeUser(tid, off, b[:])
}

func setWatch(tid int, addr uint64, lenBits, rwBits uint64) {
	poke(tid, drOff, addr)
	dr7 := uint64(1) | rwBits<<16 | lenBits<<18
	poke(tid, drOff+7*8, dr7)
}

// watch sets a hardware watchpoint on all game threads and returns the RIPs
// (instruction after the access) that hit it during dur. rw: "w" or "rw".
func watch(pid int, addr uint64, size int, rw string, dur time.Duration, onHit func(*unix.PtraceRegs)) (map[uint64]int, error) {
	lenBits := map[int]uint64{1: 0, 2: 1, 4: 3, 8: 2}[size]
	rwBits := uint64(1)
	exec := rw == "x"
	if rw == "rw" {
		rwBits = 3
	}
	if exec {
		rwBits, lenBits = 0, 0
	}
	res := map[uint64]int{}
	var rerr error
	onPT(func() {
		seized := map[int]bool{}
		stopAndArm := func(tid int) {
			var ws unix.WaitStatus
			_ = unix.PtraceInterrupt(tid)
			if _, err := unix.Wait4(tid, &ws, unix.WALL, nil); err != nil {
				return
			}
			setWatch(tid, addr, lenBits, rwBits)
			_ = unix.PtraceCont(tid, 0)
		}
		for _, tid := range tids(pid) {
			if err := unix.PtraceSeize(tid); err != nil {
				continue
			}
			_ = unix.PtraceSetOptions(tid, unix.PTRACE_O_TRACECLONE)
			seized[tid] = true
			stopAndArm(tid)
		}
		if len(seized) == 0 {
			rerr = fmt.Errorf("could not seize any thread (ptrace denied?)")
			return
		}
		deadline := time.Now().Add(dur)
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
				setWatch(ntid, addr, lenBits, rwBits)
				_ = unix.PtraceCont(ntid, 0)
			case sig == unix.SIGTRAP && ev == 0:
				var regs unix.PtraceRegs
				if unix.PtraceGetRegs(tid, &regs) == nil {
					res[regs.Rip]++
					if onHit != nil {
						onHit(&regs)
					}
					if exec {
						regs.Eflags |= 1 << 16 // RF: step over the breakpoint
						_ = unix.PtraceSetRegs(tid, &regs)
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

func fmtWatch(m map[uint64]int, base uint64) string {
	var ks []uint64
	for k := range m {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return m[ks[i]] > m[ks[j]] })
	s := ""
	for _, k := range ks {
		s += fmt.Sprintf("rip=%#x rva=%#x n=%d;", k, k-base, m[k])
	}
	return s
}
