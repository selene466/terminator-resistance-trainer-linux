package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"terminator-trainer/internal/trainer"
	"terminator-trainer/internal/ui"
)

// Usage as Steam launch option:  /path/terminator-resistance-trainer %command%
// The trainer starts the game as its child, which is what lets it write to
// the game's memory under kernel.yama.ptrace_scope=1.
func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: terminator-resistance-trainer GAME_COMMAND [ARGS]  (as Steam launch option: put it before the command placeholder)")
		os.Exit(2)
	}
	cmd := exec.Command(os.Args[1], os.Args[2:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "trainer: cannot start game:", err)
		os.Exit(1)
	}
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for s := range sig {
			_ = cmd.Process.Signal(s)
		}
	}()

	tr := trainer.New()
	go tr.Run()
	go tr.ServeDebug()
	go ui.Run(tr, tr.Items())

	err := cmd.Wait()
	tr.Stop()
	time.Sleep(100 * time.Millisecond)
	if ee, ok := err.(*exec.ExitError); ok {
		os.Exit(ee.ExitCode())
	}
}
