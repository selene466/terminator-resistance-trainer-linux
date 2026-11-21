# Terminator: Resistance Trainer (Linux)

Trainer cheat menu for Terminator: Resistance on Linux (Proton, X11).

|                                                   |                                                 |                                             |
| ------------------------------------------------- | ----------------------------------------------- | ------------------------------------------- |
| ![menu](screenshots/01-menu-cheats-on.jpg)        | ![inventory](screenshots/02-menu-inventory.jpg) | ![spawn](screenshots/03-menu-spawn.jpg)     |
| F10 menu, cheats on                               | Inventory page                                  | Spawn weapon page                           |
| ![aim](screenshots/04-pose-aim.jpg)               | ![weapon](screenshots/05-pose-weapon-2.jpg)     | ![weapon](screenshots/06-pose-weapon-3.jpg) |
| Aiming show off                                   | Weapon                                          | Weapon                                      |
| ![items](screenshots/08-game-inventory-items.png) | ![skills](screenshots/10-game-skills-1.png)     | ![skills](screenshots/11-game-skills-2.png) |
| In-game inventory                                 | Skills                                          | Skills                                      |

Only for Linux, tested in Linux: Devuan GNU/Linux 6 (excalibur) x86_64

## Features

| Key   | What                                     |
| ----- | ---------------------------------------- |
| 1     | Unlimited ammo                           |
| 2 / 3 | Damage x2 / x3 (enemies only)            |
| 4     | Unlimited HP                             |
| 5     | EXP 270 per level (needs a game restart) |
| 6     | Inventory item spawn                     |
| 7     | Spawn weapon                             |

## Requirements

- Linux, Terminator: Resistance installed (Proton)
- X11 session (the menu does not work on pure Wayland)
- [mise](https://mise.jdx.dev)
- `git`
- `~/.local/bin` in your PATH (or use the full path in the launch option)

## Install

With [mise](https://mise.jdx.dev):

```sh
mise use -g github:selene466/terminator-resistance-trainer-linux@latest
```

Or install from source (Go 1.27+):

```sh
mise install-local
```

Writes `./dist/terminator-resistance-trainer`, auto install to `~/.local/bin/terminator-resistance-trainer`.

## Setup

Steam > right click Terminator: Resistance > Properties > General > Launch Options:

```
terminator-resistance-trainer %command%
```

![Steam launch options](screenshots/13-steam-launch-options.png)

"command not found"? Use the full path: `/home/USERNAME/.local/bin/terminator-resistance-trainer %command%`

Start the game, load into a level, press **F10**.

Launch option, mise may need to puts shims:

```
~/.local/share/mise/shims/terminator-resistance-trainer %command%
```

The trainer should work in others launcher (Heroic Launcher, Bottles, Lutris).

## Use

- **F10** opens and closes the menu. Press the number key of a line to switch it ON or OFF.
- Cheats only work inside a level, not in the main menu.
- **EXP (5):** press 5 to turn it ON, then fully quit and restart the game. It is a game file change, it cannot be applied live.
- **Inventory (6):** Up/Down select, Left/Right change amount by 10, Enter adds 100, Backspace goes back. Ammo and items only, weapons have no amount.
- **Spawn weapon (7):** Up/Down select, Enter, then fire any weapon once. The new weapon appears in your inventory with 200 ammo.

## If something is wrong

- **F10 does nothing:** the launch option is wrong. Check `~/.cache/terminator-trainer.log`.
- **A cheat shows an error:** load into a level first, then try again.
- **Remove EXP mod:** press 5 to turn it OFF, restart the game.

## Develop

    mise run build      # -> dist/terminator-resistance-trainer
    mise run vet
    python3 -I tools/mkexp.py "<game>/Terminator/Content/Paks" 270   # rebuild the EXP pak from the game's own asset

Layout: `cmd/` entry point, `internal/trainer` memory patches, `internal/ui` menu, `internal/mod` EXP pak (`exp.pak` and unpacked `exp/`).

## License

[MIT](LICENSE). This covers the trainer code and tools. `internal/mod/exp.pak`, `internal/mod/exp/` and the screenshots contain material from Terminator: Resistance and belong to its owners, not covered by the MIT license.
