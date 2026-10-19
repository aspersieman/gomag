# gomag

A circular screen magnifier for Wayland, written in pure Go (no cgo, no third-party dependencies).

It draws a zoomed lens around the mouse cursor and dims (or blurs) everything outside it.
The scroll wheel zooms in and out, and the lens follows the pointer.

## Compatibility

gomag speaks the wlroots protocols `wlr-layer-shell` and `wlr-screencopy`, so it works on
Hyprland, MangoWM, sway, river, labwc, Wayfire and other compositors that implement them.
GNOME/Mutter and KDE/KWin do not implement these protocols.

## Build

    go build -o gomag ./cmd/gomag

## Usage

    gomag [-radius 200] [-zoom 2] [-dim 0.4] [-blur] [-live] [-fps 10] [-output NAME]

- Scroll wheel (or `+` / `-`): zoom in / out
- `Esc`, `q` or right click: quit
- Running `gomag` while it is already running closes it, so one key binding toggles it.

### Key bindings

Hyprland (`hyprland.conf`):

    bind = SUPER, M, exec, /path/to/gomag -live

MangoWM (`config.conf`):

    bind=SUPER,m,spawn,/path/to/gomag -live

### Live vs. frozen

By default gomag captures the screen once at start-up and magnifies that snapshot.
With `-live` it keeps re-capturing the screen (`-fps`, default 10). Because the lens itself
is part of the composited output, it is made transparent for a moment around every capture,
which can cause a brief flicker of the lens; lower `-fps` to reduce it.

## Test

    go test ./...
