package trainer

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Debug socket, enabled only when TRAINER_DEBUG=<socket path> is set.
// Line commands: info | rd A N | wr A HEX | scan i32|f32 V | next eq V|chg|same|inc|dec | list [N]
// | pat HEX-WITH-?  (scan exe image) | maps
type hit struct {
	addr uint64
	prev uint32
}

func (t *Trainer) ServeDebug() {
	path := os.Getenv("TRAINER_DEBUG")
	if path == "" {
		return
	}
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Logf("debug listen: %v", err)
		return
	}
	_ = os.Chmod(path, 0o600)
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go t.dbgConn(c)
	}
}

func (t *Trainer) dbgConn(c net.Conn) {
	defer c.Close()
	sc := bufio.NewScanner(c)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		g := t.game()
		if g == nil && f[0] != "quit" {
			fmt.Fprintln(c, "ERR no game")
			continue
		}
		switch f[0] {
		case "info":
			fmt.Fprintf(c, "pid=%d base=%#x\n", g.PID, g.Base)
		case "maps":
			rs, _ := readMaps(g.PID)
			n := 0
			for _, r := range rs {
				if strings.HasPrefix(r.Perms, "rw") && r.Path == "" {
					n++
				}
			}
			fmt.Fprintf(c, "regions=%d rw-anon=%d\n", len(rs), n)
		case "rd":
			a, _ := strconv.ParseUint(f[1], 0, 64)
			n, _ := strconv.Atoi(f[2])
			b, err := g.Read(a, n)
			fmt.Fprintf(c, "%x err=%v\n", b, err)
		case "wr":
			a, _ := strconv.ParseUint(f[1], 0, 64)
			b, _ := hex.DecodeString(f[2])
			fmt.Fprintf(c, "err=%v\n", g.Write(a, b))
		case "scan":
			v := parseVal(f[1], f[2])
			t.hits = t.hits[:0]
			rs, _ := readMaps(g.PID)
			for _, r := range rs {
				if !strings.HasPrefix(r.Perms, "rw") || r.Path != "" || r.End-r.Start > 1<<30 {
					continue
				}
				buf, _ := g.Read(r.Start, int(r.End-r.Start))
				for o := 0; o+4 <= len(buf); o += 4 {
					if binary.LittleEndian.Uint32(buf[o:]) == v {
						t.hits = append(t.hits, hit{r.Start + uint64(o), v})
					}
				}
			}
			fmt.Fprintf(c, "hits=%d\n", len(t.hits))
		case "next":
			out := t.hits[:0]
			for _, h := range t.hits {
				b, err := g.Read(h.addr, 4)
				if err != nil || len(b) < 4 {
					continue
				}
				cur := binary.LittleEndian.Uint32(b)
				ok := false
				switch f[1] {
				case "eq":
					ok = cur == parseVal(f[2], f[3])
				case "chg":
					ok = cur != h.prev
				case "same":
					ok = cur == h.prev
				case "inc":
					ok = int32(cur) > int32(h.prev)
				case "dec":
					ok = int32(cur) < int32(h.prev)
				}
				if ok {
					out = append(out, hit{h.addr, cur})
				}
			}
			t.hits = out
			fmt.Fprintf(c, "hits=%d\n", len(t.hits))
		case "list":
			n := 40
			if len(f) > 1 {
				n, _ = strconv.Atoi(f[1])
			}
			for i, h := range t.hits {
				if i >= n {
					break
				}
				b, _ := g.Read(h.addr, 4)
				if len(b) == 4 {
					u := binary.LittleEndian.Uint32(b)
					fmt.Fprintf(c, "%#x i32=%d f32=%g\n", h.addr, int32(u), math.Float32frombits(u))
				}
			}
			fmt.Fprintln(c, "end")
		case "pat":
			res := g.scanImage(strings.Join(f[1:], " "))
			fmt.Fprintf(c, "matches=%d %v\n", len(res), res)
		case "watch":
			// watch ADDR SIZE w|rw SECONDS
			a, _ := strconv.ParseUint(f[1], 0, 64)
			sz, _ := strconv.Atoi(f[2])
			sec, _ := strconv.Atoi(f[4])
			m, err := watch(g.PID, a, sz, f[3], time.Duration(sec)*time.Second, nil)
			fmt.Fprintf(c, "%s err=%v\n", fmtWatch(m, g.Base), err)
		case "trace":
			// trace ADDR SECONDS VICTIMREG DMGREG   (regs: r14 rbx rbp rdi rsi rcx rdx)
			a, _ := strconv.ParseUint(f[1], 0, 64)
			sec, _ := strconv.Atoi(f[2])
			vr, dr := "r14", "rbp"
			if len(f) >= 5 {
				vr, dr = f[3], f[4]
			}
			get := func(r *unix.PtraceRegs, n string) uint64 {
				return map[string]uint64{"r14": r.R14, "rbx": r.Rbx, "rbp": r.Rbp, "rdi": r.Rdi, "rsi": r.Rsi, "rcx": r.Rcx, "rdx": r.Rdx}[n]
			}
			var lines []string
			_, err := watch(g.PID, a, 1, "x", time.Duration(sec)*time.Second, func(r *unix.PtraceRegs) {
				rd := func(p uint64, n int) []byte {
					b, _ := g.Read(p, n)
					for len(b) < n {
						b = append(b, 0)
					}
					return b
				}
				v, d := get(r, vr), get(r, dr)
				vt := binary.LittleEndian.Uint64(rd(v, 8))
				hp := math.Float32frombits(binary.LittleEndian.Uint32(rd(v+0xEC, 4)))
				dm := math.Float32frombits(binary.LittleEndian.Uint32(rd(d, 4)))
				lines = append(lines, fmt.Sprintf("victim=%#x vtable_rva=%#x vteam=%d hp=%g dmg=%g", v, vt-g.Base, rd(v+0x230, 1)[0], hp, dm))
			})
			fmt.Fprintf(c, "n=%d err=%v | %s\n", len(lines), err, strings.Join(lines, " ; "))
		case "callseq":
			// callseq SITE_RVA WEAPONREG SECONDS FN_RVA:arg,arg;FN_RVA:arg...   (args: $w, $N, number)
			site, _ := strconv.ParseUint(f[1], 0, 64)
			sec, _ := strconv.Atoi(f[3])
			calls, err := parseCalls(strings.Join(f[4:], " "), g.Base)
			if err != nil {
				fmt.Fprintf(c, "ERR %v\n", err)
				break
			}
			res, err := t.injectAt([]bpSite{{g.Base + site, f[2]}}, time.Duration(sec)*time.Second, calls, -1, 0)
			var sb strings.Builder
			for _, r := range res {
				fmt.Fprintf(&sb, "%#x ", r)
			}
			fmt.Fprintf(c, "results=%s err=%v\n", sb.String(), err)
		case "inv":
			es, err := t.ListInventory()
			for _, e := range es {
				fmt.Fprintf(c, "%#x %s count=%d weapon=%v\n", e.Addr, e.Name, e.Count, e.Weapon)
			}
			fmt.Fprintf(c, "end err=%v\n", err)
		case "invadd":
			// invadd NAME DELTA
			es, err := t.ListInventory()
			d, _ := strconv.Atoi(f[2])
			done := false
			for _, e := range es {
				if e.Name == f[1] {
					n, err2 := t.AddCount(e, int32(d))
					fmt.Fprintf(c, "%s -> %d err=%v\n", e.Name, n, err2)
					done = true
				}
			}
			if !done {
				fmt.Fprintf(c, "not found err=%v\n", err)
			}
		case "classes":
			m := g.findClasses(f[1])
			for n, as := range m {
				fmt.Fprintf(c, "%s", n)
				for _, a := range as {
					fmt.Fprintf(c, " %#x", a)
				}
				fmt.Fprintln(c)
			}
			fmt.Fprintf(c, "end n=%d\n", len(m))
		case "quit":
			return
		default:
			fmt.Fprintln(c, "ERR unknown")
		}
	}
}

func parseVal(kind, s string) uint32 {
	if kind == "f32" {
		v, _ := strconv.ParseFloat(s, 32)
		return math.Float32bits(float32(v))
	}
	v, _ := strconv.ParseInt(s, 0, 64)
	return uint32(int32(v))
}
