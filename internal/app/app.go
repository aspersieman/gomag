// Package app implements the Wayland client for gomag. It targets compositors
// that provide wlr-layer-shell and wlr-screencopy (Hyprland, MangoWM, sway,
// river, labwc, wayfire, ...).
package app

import (
	"errors"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/aspersieman/gomag/internal/render"
	"github.com/aspersieman/gomag/internal/wire"
)

// Config holds user-visible settings.
type Config struct {
	Radius  float64 // lens radius in logical pixels
	Zoom    float64 // initial zoom
	MinZoom float64
	MaxZoom float64
	Dim     float64 // brightness outside lens (0..1)
	Blur    bool
	Border  float64 // lens border width, logical pixels
	Live    bool    // continuously re-capture the screen
	FPS     int     // live capture rate
	Output  string  // output name; empty = first output
	Verbose bool
}

type output struct {
	id   uint32
	name string
}

// App is the magnifier client.
type App struct {
	cfg      Config
	conn     *wire.Conn
	handlers map[uint32]func(wire.Message)

	registry, compositor, shm, seat, layerShell, screencopy uint32
	screencopyVersion                                       uint32
	outputs                                                 []*output
	globals                                                 map[string]struct{ name, version uint32 }

	surface, layer uint32
	lw, lh         int // logical size
	scale          int
	configured     bool
	pending        bool // frame needs redraw

	src      *render.Image
	renderer *render.Renderer
	bufs     []*shmBuffer

	px, py   float64 // pointer, logical
	havePtr  bool
	zoom     float64
	quit     bool
	capture  bool // capture in flight
	hidden   bool // transparent buffer shown for capture
	pointer  uint32
	keyboard uint32
}

// Run starts the magnifier and blocks until the user quits.
func Run(cfg Config, stop <-chan struct{}) error {
	conn, err := wire.Connect()
	if err != nil {
		return fmt.Errorf("connecting to Wayland: %w", err)
	}
	defer conn.Close()
	a := &App{cfg: cfg, conn: conn, handlers: map[uint32]func(wire.Message){}, zoom: cfg.Zoom,
		globals: map[string]struct{ name, version uint32 }{}}
	return a.run(stop)
}

func (a *App) logf(format string, args ...any) {
	if a.cfg.Verbose {
		fmt.Fprintf(os.Stderr, "gomag: "+format+"\n", args...)
	}
}

func (a *App) run(stop <-chan struct{}) error {
	a.handlers[1] = a.onDisplay
	a.registry = a.conn.NewID()
	a.conn.Req(1, 1).U32(a.registry).Send()
	a.handlers[a.registry] = a.onRegistry
	if err := a.roundtrip(stop); err != nil {
		return err
	}
	if err := a.bindGlobals(); err != nil {
		return err
	}
	if err := a.roundtrip(stop); err != nil { // output names etc.
		return err
	}
	out, err := a.pickOutput()
	if err != nil {
		return err
	}

	// Initial capture happens before our overlay exists, so it is clean.
	if err := a.captureSync(out, stop); err != nil {
		return err
	}
	a.renderer = render.New(a.src, render.Options{Dim: a.cfg.Dim, Blur: a.cfg.Blur, Border: a.cfg.Border})

	if err := a.createSurface(out); err != nil {
		return err
	}

	interval := time.Hour
	if a.cfg.Live {
		fps := a.cfg.FPS
		if fps < 1 {
			fps = 10
		}
		interval = time.Second / time.Duration(fps)
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for !a.quit {
		select {
		case m, ok := <-a.conn.Events:
			if !ok {
				if e := a.conn.Err(); e != nil {
					return e
				}
				return errors.New("compositor closed the connection")
			}
			a.dispatch(m)
		case <-tick.C:
			if a.cfg.Live {
				a.startLiveCapture(out)
			}
		case <-stop:
			a.quit = true
		}
		a.redraw()
	}
	return nil
}

func (a *App) dispatch(m wire.Message) {
	if h := a.handlers[m.Object]; h != nil {
		h(m)
	}
	for _, fd := range m.Fds {
		closeFd(fd)
	}
}

// roundtrip waits until the compositor has processed all prior requests.
func (a *App) roundtrip(stop <-chan struct{}) error {
	return a.waitFor(stop, func(done func()) uint32 {
		cb := a.conn.NewID()
		a.conn.Req(1, 0).U32(cb).Send()
		a.handlers[cb] = func(m wire.Message) { delete(a.handlers, cb); done() }
		return cb
	})
}

func (a *App) waitFor(stop <-chan struct{}, start func(done func()) uint32) error {
	finished := false
	start(func() { finished = true })
	for !finished {
		select {
		case m, ok := <-a.conn.Events:
			if !ok {
				if e := a.conn.Err(); e != nil {
					return e
				}
				return errors.New("compositor closed the connection")
			}
			a.dispatch(m)
		case <-stop:
			return errors.New("interrupted")
		}
		if a.quit {
			return errors.New("aborted")
		}
	}
	return nil
}

func (a *App) onDisplay(m wire.Message) {
	switch m.Opcode {
	case 0:
		ar := wire.NewArgs(m.Args)
		obj, code, msg := ar.U32(), ar.U32(), ar.Str()
		fmt.Fprintf(os.Stderr, "gomag: wayland protocol error on object %d (code %d): %s\n", obj, code, msg)
		a.quit = true
	case 1:
		ar := wire.NewArgs(m.Args)
		delete(a.handlers, ar.U32())
	}
}

func (a *App) onRegistry(m wire.Message) {
	if m.Opcode != 0 {
		return
	}
	ar := wire.NewArgs(m.Args)
	name, iface, ver := ar.U32(), ar.Str(), ar.U32()
	if iface == "wl_output" {
		// outputs may be several; keep every one
		a.globals[fmt.Sprintf("wl_output/%d", name)] = struct{ name, version uint32 }{name, ver}
		return
	}
	a.globals[iface] = struct{ name, version uint32 }{name, ver}
}

func (a *App) bind(iface string, maxVer uint32) (uint32, uint32, bool) {
	g, ok := a.globals[iface]
	if !ok {
		return 0, 0, false
	}
	v := g.version
	if v > maxVer {
		v = maxVer
	}
	id := a.conn.NewID()
	a.conn.Req(a.registry, 0).U32(g.name).Str(iface).U32(v).U32(id).Send()
	return id, v, true
}

func (a *App) bindGlobals() error {
	var ok bool
	if a.compositor, _, ok = a.bind("wl_compositor", 4); !ok {
		return errors.New("compositor does not provide wl_compositor")
	}
	if a.shm, _, ok = a.bind("wl_shm", 1); !ok {
		return errors.New("compositor does not provide wl_shm")
	}
	if a.layerShell, _, ok = a.bind("zwlr_layer_shell_v1", 1); !ok {
		return errors.New("compositor does not support wlr-layer-shell (zwlr_layer_shell_v1)")
	}
	if a.screencopy, a.screencopyVersion, ok = a.bind("zwlr_screencopy_manager_v1", 3); !ok {
		return errors.New("compositor does not support wlr-screencopy (zwlr_screencopy_manager_v1)")
	}
	if a.seat, _, ok = a.bind("wl_seat", 5); !ok {
		return errors.New("compositor does not provide wl_seat")
	}
	a.handlers[a.seat] = a.onSeat
	for key, g := range a.globals {
		if len(key) > 10 && key[:10] == "wl_output/" {
			v := g.version
			if v > 4 {
				v = 4
			}
			o := &output{id: a.conn.NewID()}
			a.conn.Req(a.registry, 0).U32(g.name).Str("wl_output").U32(v).U32(o.id).Send()
			a.outputs = append(a.outputs, o)
			a.handlers[o.id] = func(m wire.Message) {
				if m.Opcode == 4 { // name
					o.name = wire.NewArgs(m.Args).Str()
				}
			}
		}
	}
	return nil
}

func (a *App) pickOutput() (*output, error) {
	if len(a.outputs) == 0 {
		return nil, errors.New("no outputs found")
	}
	if a.cfg.Output == "" {
		return a.outputs[0], nil
	}
	for _, o := range a.outputs {
		if o.name == a.cfg.Output {
			return o, nil
		}
	}
	return nil, fmt.Errorf("output %q not found", a.cfg.Output)
}

func (a *App) onSeat(m wire.Message) {
	if m.Opcode != 0 {
		return
	}
	caps := wire.NewArgs(m.Args).U32()
	if caps&1 != 0 && a.pointer == 0 {
		a.pointer = a.conn.NewID()
		a.conn.Req(a.seat, 0).U32(a.pointer).Send()
		a.handlers[a.pointer] = a.onPointer
	}
	if caps&2 != 0 && a.keyboard == 0 {
		a.keyboard = a.conn.NewID()
		a.conn.Req(a.seat, 1).U32(a.keyboard).Send()
		a.handlers[a.keyboard] = a.onKeyboard
	}
}

func (a *App) onPointer(m wire.Message) {
	ar := wire.NewArgs(m.Args)
	switch m.Opcode {
	case 0: // enter
		serial := ar.U32()
		ar.U32()
		a.px, a.py = ar.Fixed(), ar.Fixed()
		a.havePtr = true
		a.pending = true
		a.conn.Req(a.pointer, 0).U32(serial).U32(0).I32(0).I32(0).Send() // hide cursor
	case 2: // motion
		ar.U32()
		a.px, a.py = ar.Fixed(), ar.Fixed()
		a.havePtr = true
		a.pending = true
	case 3: // button
		ar.U32()
		ar.U32()
		button, state := ar.U32(), ar.U32()
		if state == 1 && button == 0x111 { // right click quits
			a.quit = true
		}
	case 4: // axis
		ar.U32()
		axis, v := ar.U32(), ar.Fixed()
		if axis == 0 && v != 0 {
			a.zoom = math.Min(math.Max(a.zoom*math.Pow(1.1, -v/10), a.cfg.MinZoom), a.cfg.MaxZoom)
			a.pending = true
		}
	}
}

func (a *App) onKeyboard(m wire.Message) {
	if m.Opcode != 3 {
		return
	}
	ar := wire.NewArgs(m.Args)
	ar.U32()
	ar.U32()
	key, state := ar.U32(), ar.U32()
	if state == 1 {
		switch key {
		case 1, 16: // Escape, q
			a.quit = true
		case 13, 78: // = / keypad +
			a.zoom = math.Min(a.zoom*1.1, a.cfg.MaxZoom)
			a.pending = true
		case 12, 74: // - / keypad -
			a.zoom = math.Max(a.zoom/1.1, a.cfg.MinZoom)
			a.pending = true
		}
	}
}

func (a *App) createSurface(out *output) error {
	a.surface = a.conn.NewID()
	a.conn.Req(a.compositor, 0).U32(a.surface).Send()
	a.layer = a.conn.NewID()
	a.conn.Req(a.layerShell, 0).U32(a.layer).U32(a.surface).U32(out.id).U32(3).Str("gomag").Send()
	a.conn.Req(a.layer, 0).U32(0).U32(0).Send() // set_size 0,0 = fill
	a.conn.Req(a.layer, 1).U32(15).Send()       // anchor all edges
	a.conn.Req(a.layer, 2).I32(-1).Send()       // exclusive zone -1: ignore other surfaces
	a.conn.Req(a.layer, 4).U32(1).Send()        // exclusive keyboard
	a.handlers[a.layer] = func(m wire.Message) {
		ar := wire.NewArgs(m.Args)
		switch m.Opcode {
		case 0:
			serial, w, h := ar.U32(), int(ar.U32()), int(ar.U32())
			a.conn.Req(a.layer, 6).U32(serial).Send()
			a.lw, a.lh = w, h
			a.scale = int(math.Round(float64(a.src.W) / float64(w)))
			if a.scale < 1 {
				a.scale = 1
			}
			if a.scale > 1 {
				a.conn.Req(a.surface, 8).I32(int32(a.scale)).Send()
			}
			a.configured = true
			a.pending = true
		case 1:
			a.quit = true
		}
	}
	a.conn.Req(a.surface, 6).Send() // commit to get configure
	return nil
}

// acquire returns an idle buffer matching the source size.
func (a *App) acquire() (*shmBuffer, error) {
	for _, b := range a.bufs {
		if !b.busy && b.w == a.src.W && b.h == a.src.H {
			return b, nil
		}
	}
	if len(a.bufs) >= 4 {
		return nil, nil
	}
	b, err := a.newShmBuffer(a.src.W, a.src.H, a.src.W*4, fmtARGB8888)
	if err != nil {
		return nil, err
	}
	a.bufs = append(a.bufs, b)
	return b, nil
}

func (a *App) present(b *shmBuffer) {
	b.busy = true
	a.conn.Req(a.surface, 1).U32(b.id).I32(0).I32(0).Send()
	a.conn.Req(a.surface, 9).I32(0).I32(0).I32(math.MaxInt32).I32(math.MaxInt32).Send()
	a.conn.Req(a.surface, 6).Send()
}

func (a *App) redraw() {
	if !a.configured || !a.pending || a.quit || a.capture || a.hidden {
		return
	}
	b, err := a.acquire()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gomag:", err)
		a.quit = true
		return
	}
	if b == nil {
		return // all buffers busy; retry on next event
	}
	a.pending = false
	cx, cy := float64(a.src.W)/2, float64(a.src.H)/2
	if a.havePtr {
		cx, cy = a.px*float64(a.scale), a.py*float64(a.scale)
	}
	dst := &render.Image{W: b.w, H: b.h, Stride: b.stride, Pix: b.data}
	a.renderer.Draw(dst, render.Lens{X: cx, Y: cy, Radius: a.cfg.Radius * float64(a.scale), Zoom: a.zoom})
	a.present(b)
}

// startLiveCapture makes the overlay transparent, grabs a fresh frame, then
// restores the overlay. The overlay must be invisible while capturing,
// otherwise the screencopy would contain the magnifier itself.
func (a *App) startLiveCapture(out *output) {
	if !a.configured || a.capture || a.hidden || a.quit {
		return
	}
	b, err := a.acquire()
	if err != nil || b == nil {
		return
	}
	for i := range b.data {
		b.data[i] = 0
	}
	a.present(b)
	a.hidden = true
	a.capture = true
	a.requestCapture(out, func(img *render.Image, err error) {
		a.capture = false
		a.hidden = false
		a.pending = true
		if err != nil {
			a.logf("live capture failed: %v", err)
			return
		}
		if img.W == a.src.W && img.H == a.src.H {
			a.src = img
			a.renderer.SetSource(img)
		}
	})
}

func closeFd(fd int) { syscallClose(fd) }

// requestCapture starts a screencopy of the output and calls done on completion.
func (a *App) requestCapture(out *output, done func(*render.Image, error)) {
	frame := a.conn.NewID()
	a.conn.Req(a.screencopy, 0).U32(frame).I32(0).U32(out.id).Send()
	var (
		format       uint32
		w, h, stride int
		yInvert      bool
		buf          *shmBuffer
		copied       bool
		finish       = func(img *render.Image, err error) {
			a.conn.Req(frame, 1).Send()
			delete(a.handlers, frame)
			if buf != nil {
				a.destroyShmBuffer(buf)
			}
			done(img, err)
		}
		doCopy = func() {
			if copied {
				return
			}
			copied = true
			var err error
			buf, err = a.newShmBuffer(w, h, stride, format)
			if err != nil {
				finish(nil, err)
				return
			}
			a.conn.Req(frame, 0).U32(buf.id).Send()
		}
	)
	a.handlers[frame] = func(m wire.Message) {
		ar := wire.NewArgs(m.Args)
		switch m.Opcode {
		case 0: // buffer
			format, w, h, stride = ar.U32(), int(ar.U32()), int(ar.U32()), int(ar.U32())
			if a.screencopyVersion < 3 {
				doCopy()
			}
		case 1: // flags
			yInvert = ar.U32()&1 != 0
		case 2: // ready
			img, err := buf.toImage(format, yInvert)
			finish(img, err)
		case 3: // failed
			finish(nil, errors.New("screencopy failed"))
		case 6: // buffer_done
			doCopy()
		}
	}
}

func (a *App) captureSync(out *output, stop <-chan struct{}) error {
	var img *render.Image
	var cerr error
	err := a.waitFor(stop, func(done func()) uint32 {
		a.requestCapture(out, func(i *render.Image, e error) { img, cerr = i, e; done() })
		return 0
	})
	if err != nil {
		return err
	}
	if cerr != nil {
		return cerr
	}
	a.src = img
	return nil
}
