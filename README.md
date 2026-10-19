# gomag

A circular screen magnifier for Wayland, written in pure Go (no cgo, no third-party dependencies).

It draws a zoomed lens around the mouse cursor and dims (or blurs) everything outside it.
The scroll wheel zooms in and out, and the lens follows the pointer.

## Compatibility

gomag speaks the wlroots protocols `wlr-layer-shell` and `wlr-screencopy`, so it works on
Hyprland, MangoWM, sway, river, labwc, Wayfire and other compositors that implement them.
GNOME/Mutter and KDE/KWin do not implement these protocols.

## Build, run, install

    make dev        # build and run (ARGS defaults to "-live -verbose")
    make dev ARGS="-radius 150 -blur"
    make build      # ./bin/gomag
    make install    # to ~/.local/bin; use PREFIX=/usr/local (with sudo) for system-wide
    make test

`make help` lists all targets.

## Usage

    gomag [-radius 200] [-zoom 2] [-dim 0.4] [-blur]  [-output NAME]

- Scroll wheel (or `+` / `-`): zoom in / out
- `Esc`, `q` or right click: quit
- Running `gomag` while it is already running closes it, so one key binding toggles it.

### Key bindings

Hyprland (`hyprland.conf`):

    bind = SUPER, M, exec, /path/to/gomag -live

MangoWM (`config.conf`):

    bind=SUPER,m,spawn,/path/to/gomag -live

### Multiple monitors

gomag covers every monitor with its own overlay; the lens appears on whichever monitor the
pointer is on. Use `-output NAME` (e.g. `-output eDP-1`) to limit it to a single monitor.

### Live vs. frozen

By default gomag magnifies a snapshot taken at start-up. With `-live` it keeps refreshing the
magnified area (`-fps`, default 10). The overlay would otherwise capture itself, so for each
refresh only the lens is made transparent for an instant while just the small area under it is
captured; the dimmed background stays up and is *not* refreshed (it remains the start-up
snapshot). The lens is hidden only for about one compositor frame per refresh (the capture buffer is reused and the hole is committed together with the copy request). Some flicker can remain because wlroots cannot exclude a layer from a capture; lower `-fps` to reduce it, or omit `-live` for a static, flicker-free lens.

## Test

    go test ./...
