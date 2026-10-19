// Package render draws the magnifier: a dimmed (optionally blurred)
// background with a zoomed circular lens around the cursor.
package render

import "math"

// Image is a 4-bytes-per-pixel image in B,G,R,A byte order (the memory layout
// of Wayland's ARGB8888/XRGB8888 on little-endian machines).
type Image struct {
	W, H, Stride int
	Pix          []byte
}

// NewImage allocates a tightly packed image.
func NewImage(w, h int) *Image {
	return &Image{W: w, H: h, Stride: w * 4, Pix: make([]byte, w*4*h)}
}

// Options configures the visual appearance.
type Options struct {
	Dim    float64 // brightness of the area outside the lens, 0 (black) .. 1 (unchanged)
	Blur   bool    // blur the area outside the lens
	Border float64 // lens border width in pixels (0 disables)
}

// Lens describes the lens for a single frame.
type Lens struct {
	X, Y   float64 // centre, in image pixels
	Radius float64 // in image pixels
	Zoom   float64 // magnification factor (>= 1)
}

// Renderer holds a prepared background for a captured source image so that
// individual frames only need to redraw the lens.
type Renderer struct {
	src, bg *Image
	opt     Options
}

// New prepares a renderer for the given source image.
func New(src *Image, opt Options) *Renderer {
	r := &Renderer{opt: opt}
	r.SetSource(src)
	return r
}

// SetSource replaces the captured source image (used for live updates).
func (r *Renderer) SetSource(src *Image) {
	r.src = src
	if r.bg == nil || r.bg.W != src.W || r.bg.H != src.H {
		r.bg = NewImage(src.W, src.H)
	}
	base := src
	if r.opt.Blur {
		base = blur(src, 8)
	}
	dim := clamp(r.opt.Dim, 0, 1)
	for y := 0; y < src.H; y++ {
		s := base.Pix[y*base.Stride : y*base.Stride+src.W*4]
		d := r.bg.Pix[y*r.bg.Stride : y*r.bg.Stride+src.W*4]
		for i := 0; i < len(s); i += 4 {
			d[i] = uint8(float64(s[i])*dim + 0.5)
			d[i+1] = uint8(float64(s[i+1])*dim + 0.5)
			d[i+2] = uint8(float64(s[i+2])*dim + 0.5)
			d[i+3] = 255
		}
	}
}

// Draw renders one frame into dst, which must have the source's dimensions.
func (r *Renderer) Draw(dst *Image, l Lens) {
	for y := 0; y < dst.H; y++ {
		copy(dst.Pix[y*dst.Stride:y*dst.Stride+dst.W*4], r.bg.Pix[y*r.bg.Stride:y*r.bg.Stride+dst.W*4])
	}
	zoom := math.Max(l.Zoom, 1)
	x0 := int(math.Max(0, math.Floor(l.X-l.Radius-1)))
	x1 := int(math.Min(float64(dst.W-1), math.Ceil(l.X+l.Radius+1)))
	y0 := int(math.Max(0, math.Floor(l.Y-l.Radius-1)))
	y1 := int(math.Min(float64(dst.H-1), math.Ceil(l.Y+l.Radius+1)))
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			dx, dy := float64(x)+0.5-l.X, float64(y)+0.5-l.Y
			d := math.Hypot(dx, dy)
			cov := clamp(l.Radius+0.5-d, 0, 1)
			if cov == 0 {
				continue
			}
			b, g, rr := sample(r.src, l.X+dx/zoom, l.Y+dy/zoom)
			if r.opt.Border > 0 {
				// ring: blend towards white over the outer Border pixels
				ring := clamp(d-(l.Radius-r.opt.Border)+0.5, 0, 1)
				b += (255 - b) * ring
				g += (255 - g) * ring
				rr += (255 - rr) * ring
			}
			o := y*dst.Stride + x*4
			dst.Pix[o] = mix(dst.Pix[o], b, cov)
			dst.Pix[o+1] = mix(dst.Pix[o+1], g, cov)
			dst.Pix[o+2] = mix(dst.Pix[o+2], rr, cov)
			dst.Pix[o+3] = 255
		}
	}
}

func mix(bg uint8, fg, a float64) uint8 {
	return uint8(float64(bg)*(1-a) + fg*a + 0.5)
}

// sample bilinearly samples the image at pixel-centre coordinates (x, y),
// clamping to the edges, and returns b, g, r.
func sample(im *Image, x, y float64) (b, g, r float64) {
	x -= 0.5
	y -= 0.5
	fx, fy := math.Floor(x), math.Floor(y)
	tx, ty := x-fx, y-fy
	ix, iy := int(fx), int(fy)
	var out [3]float64
	for j := 0; j < 2; j++ {
		wy := ty
		if j == 0 {
			wy = 1 - ty
		}
		yy := clampInt(iy+j, 0, im.H-1)
		for i := 0; i < 2; i++ {
			wx := tx
			if i == 0 {
				wx = 1 - tx
			}
			xx := clampInt(ix+i, 0, im.W-1)
			o := yy*im.Stride + xx*4
			w := wx * wy
			out[0] += float64(im.Pix[o]) * w
			out[1] += float64(im.Pix[o+1]) * w
			out[2] += float64(im.Pix[o+2]) * w
		}
	}
	return out[0], out[1], out[2]
}

// blur returns a blurred copy of src: block-average downscale by factor f
// followed by bilinear upscale.
func blur(src *Image, f int) *Image {
	sw, sh := (src.W+f-1)/f, (src.H+f-1)/f
	small := NewImage(sw, sh)
	for by := 0; by < sh; by++ {
		for bx := 0; bx < sw; bx++ {
			var sum [3]int
			n := 0
			for y := by * f; y < (by+1)*f && y < src.H; y++ {
				for x := bx * f; x < (bx+1)*f && x < src.W; x++ {
					o := y*src.Stride + x*4
					sum[0] += int(src.Pix[o])
					sum[1] += int(src.Pix[o+1])
					sum[2] += int(src.Pix[o+2])
					n++
				}
			}
			o := by*small.Stride + bx*4
			for c := 0; c < 3; c++ {
				small.Pix[o+c] = uint8(sum[c] / n)
			}
			small.Pix[o+3] = 255
		}
	}
	out := NewImage(src.W, src.H)
	inv := 1 / float64(f)
	for y := 0; y < src.H; y++ {
		for x := 0; x < src.W; x++ {
			b, g, r := sample(small, (float64(x)+0.5)*inv, (float64(y)+0.5)*inv)
			o := y*out.Stride + x*4
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = uint8(b+0.5), uint8(g+0.5), uint8(r+0.5), 255
		}
	}
	return out
}

func clamp(v, lo, hi float64) float64 { return math.Min(math.Max(v, lo), hi) }

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
