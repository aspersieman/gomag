// Command gomag is a real-time screen magnifier for Wayland compositors that
// implement wlr-layer-shell and wlr-screencopy (Hyprland, MangoWM, sway, ...).
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/aspersieman/gomag/internal/app"
)

func main() {
	var cfg app.Config
	flag.Float64Var(&cfg.Radius, "radius", 200, "lens radius in logical pixels")
	flag.Float64Var(&cfg.Zoom, "zoom", 2, "initial zoom factor")
	flag.Float64Var(&cfg.MinZoom, "min-zoom", 1.1, "minimum zoom factor")
	flag.Float64Var(&cfg.MaxZoom, "max-zoom", 16, "maximum zoom factor")
	flag.Float64Var(&cfg.Dim, "dim", 0.4, "brightness of the area outside the lens (0 = black, 1 = unchanged)")
	flag.BoolVar(&cfg.Blur, "blur", false, "blur the area outside the lens")
	flag.Float64Var(&cfg.Border, "border", 2, "lens border width in pixels (0 = none)")
	flag.BoolVar(&cfg.Live, "live", false, "continuously refresh the magnified content (may flicker briefly on each refresh)")
	flag.IntVar(&cfg.FPS, "fps", 10, "refresh rate used with -live")
	flag.StringVar(&cfg.Output, "output", "", "output (monitor) name; default: first output")
	flag.BoolVar(&cfg.Verbose, "verbose", false, "print diagnostics")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: gomag [flags]\n\nRunning gomag while it is already running closes it, so a single key binding toggles it.\nScroll wheel: zoom. Esc, q or right click: quit.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if cfg.Zoom < cfg.MinZoom {
		cfg.Zoom = cfg.MinZoom
	}
	if cfg.Zoom > cfg.MaxZoom {
		cfg.Zoom = cfg.MaxZoom
	}

	pidfile := filepath.Join(runtimeDir(), "gomag.pid")
	if toggleOff(pidfile) {
		return
	}
	if err := os.WriteFile(pidfile, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "gomag:", err)
	}
	defer os.Remove(pidfile)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	stop := make(chan struct{})
	go func() { <-sig; close(stop) }()

	if err := app.Run(cfg, stop); err != nil {
		fmt.Fprintln(os.Stderr, "gomag:", err)
		os.Remove(pidfile)
		os.Exit(1)
	}
}

func runtimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d
	}
	return os.TempDir()
}

// toggleOff stops an already running instance and reports whether it did.
func toggleOff(pidfile string) bool {
	b, err := os.ReadFile(pidfile)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 || pid == os.Getpid() {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil && !strings.HasPrefix(string(data), "gomag") {
		return false // stale pidfile, pid reused by another program
	}
	if proc.Signal(syscall.SIGTERM) != nil {
		return false
	}
	return true
}
