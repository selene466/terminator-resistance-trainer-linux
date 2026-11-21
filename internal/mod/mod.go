package mod

import (
	"bytes"
	_ "embed"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Pak mods are mounted by the engine at startup, so install/remove only
// takes effect on the next launch.
//
//go:embed exp.pak
var expPak []byte

const modName = "Easy EXP 270_P.pak"

// paksDir finds <install>/Terminator/Content/Paks from Steam's env or the launch args.
func paksDir() (string, error) {
	var roots []string
	if p := os.Getenv("STEAM_COMPAT_INSTALL_PATH"); p != "" {
		roots = append(roots, p)
	}
	for _, a := range os.Args[1:] {
		if strings.HasSuffix(strings.ToLower(a), ".exe") {
			for d := filepath.Dir(a); d != "/" && d != "."; d = filepath.Dir(d) {
				roots = append(roots, d)
			}
		}
	}
	for _, r := range roots {
		d := filepath.Join(r, "Terminator", "Content", "Paks")
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d, nil
		}
	}
	return "", errors.New("Paks folder not found")
}

func Installed() bool {
	d, err := paksDir()
	if err != nil {
		return false
	}
	b, err := os.ReadFile(filepath.Join(d, "~mods", modName))
	return err == nil && bytes.Equal(b, expPak)
}

func Set(on bool) error {
	d, err := paksDir()
	if err != nil {
		return err
	}
	p := filepath.Join(d, "~mods", modName)
	if !on {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, expPak, 0o644)
}
