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

## Architecture

gomag is a single static binary with no cgo and no third-party dependencies. It talks to the
compositor directly over the Wayland wire protocol.

    cmd/gomag          CLI: flags, toggle-on-rerun (pidfile + SIGTERM), signal handling
    internal/app       Wayland client: globals, outputs, input, overlays, capture, event loop
    internal/wire      Minimal Wayland wire protocol: framing, fd passing, arg encode/decode
    internal/render    Pure-Go pixel code: lens, zoom, dim/blur, border (no Wayland knowledge)

**How it works**

1. **Connect and bind** (`wire`, `app`): connect to `$WAYLAND_DISPLAY`, read the registry and
   bind `wl_compositor`, `wl_shm`, `wl_seat`, every `wl_output`, `zwlr_layer_shell_v1` and
   `zwlr_screencopy_manager_v1`.
2. **Snapshot**: before any overlay exists, each monitor is captured once with screencopy
   into a shared-memory buffer. This is the clean source image.
3. **Overlay**: for each monitor a full-screen layer-shell surface is created on the overlay
   layer with exclusive keyboard focus, so it gets pointer and key events.
4. **Render** (`render`): the renderer keeps a dimmed (optionally blurred) copy of the
   snapshot as background. For each frame it copies the background and, inside a circle
   around the pointer, bilinearly samples the source at the zoom factor with an anti-aliased
   edge and border. Output is written straight into a shared-memory buffer that the
   compositor displays. Buffers are pooled and recycled on `wl_buffer.release`.
5. **Input**: pointer motion moves the lens, the scroll wheel changes the zoom, and the
   lens lives on whichever monitor currently has the pointer. Esc, `q` or right click quit.
6. **Live mode** (`-live`): a ticker periodically refreshes just the area under the lens.
   Because the compositor would capture gomag's own overlay, the lens is briefly replaced by
   a transparent hole, a small screencopy region is taken, and the pixels are patched into
   the source image. The dimmed background is not refreshed.

**Concurrency model**: one goroutine reads the socket and decodes events into a channel; the
main goroutine owns all state and runs a single `select` loop over events, the live-refresh
ticker and the stop signal, so no locking is needed. Request handlers are plain callbacks
registered per Wayland object id.

**Constraints**: it requires wlroots-style protocols, so it targets Hyprland, MangoWM, sway,
river, labwc and similar, and not GNOME or KDE. Rendering is on the CPU, which is simple and
portable but limits very large zoomed lenses at high refresh rates.

## Test

    go test ./...
