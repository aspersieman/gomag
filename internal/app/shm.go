package app

import (
	"fmt"
	"os"
	"syscall"

	"github.com/aspersieman/gomag/internal/render"
	"github.com/aspersieman/gomag/internal/wire"
)

const (
	fmtARGB8888 = 0
	fmtXRGB8888 = 1
	fmtABGR8888 = 0x34324241
	fmtXBGR8888 = 0x34324258
)

// shmBuffer is a wl_buffer backed by anonymous shared memory.
type shmBuffer struct {
	id           uint32
	data         []byte
	w, h, stride int
	busy         bool
}

func (a *App) newShmBuffer(w, h, stride int, format uint32) (*shmBuffer, error) {
	size := stride * h
	dir := os.Getenv("XDG_RUNTIME_DIR")
	f, err := os.CreateTemp(dir, "gomag-shm-")
	if err != nil {
		return nil, err
	}
	os.Remove(f.Name())
	defer f.Close()
	if err := f.Truncate(int64(size)); err != nil {
		return nil, err
	}
	data, err := syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("mmap: %w", err)
	}
	pool := a.conn.NewID()
	if err := a.conn.Req(a.shm, 0).U32(pool).Fd(int(f.Fd())).I32(int32(size)).Send(); err != nil {
		return nil, err
	}
	b := &shmBuffer{id: a.conn.NewID(), data: data, w: w, h: h, stride: stride}
	if err := a.conn.Req(pool, 0).U32(b.id).I32(0).I32(int32(w)).I32(int32(h)).I32(int32(stride)).U32(format).Send(); err != nil {
		return nil, err
	}
	if err := a.conn.Req(pool, 1).Send(); err != nil { // destroy pool; buffer keeps the mapping alive
		return nil, err
	}
	a.handlers[b.id] = func(m wire.Message) {
		if m.Opcode == 0 { // release
			b.busy = false
		}
	}
	return b, nil
}

func (a *App) destroyShmBuffer(b *shmBuffer) {
	a.conn.Req(b.id, 0).Send()
	delete(a.handlers, b.id)
	syscall.Munmap(b.data)
}

// toImage converts captured pixels into BGRA byte order, applying a vertical
// flip if requested.
func (b *shmBuffer) toImage(format uint32, yInvert bool) (*render.Image, error) {
	swap := false
	switch format {
	case fmtARGB8888, fmtXRGB8888:
	case fmtABGR8888, fmtXBGR8888:
		swap = true
	default:
		return nil, fmt.Errorf("unsupported screencopy pixel format 0x%x", format)
	}
	im := render.NewImage(b.w, b.h)
	for y := 0; y < b.h; y++ {
		sy := y
		if yInvert {
			sy = b.h - 1 - y
		}
		copy(im.Pix[y*im.Stride:(y+1)*im.Stride], b.data[sy*b.stride:sy*b.stride+b.w*4])
	}
	for i := 0; i < len(im.Pix); i += 4 {
		if swap {
			im.Pix[i], im.Pix[i+2] = im.Pix[i+2], im.Pix[i]
		}
		im.Pix[i+3] = 255
	}
	return im, nil
}
