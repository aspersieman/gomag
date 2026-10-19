package render

import "testing"

func checker(w, h int) *Image {
	im := NewImage(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := y*im.Stride + x*4
			v := byte(0)
			if (x/10+y/10)%2 == 0 {
				v = 200
			}
			im.Pix[o], im.Pix[o+1], im.Pix[o+2], im.Pix[o+3] = v, v, v, 255
		}
	}
	return im
}

func px(im *Image, x, y int) byte { return im.Pix[y*im.Stride+x*4] }

func TestOutsideIsDimmedAndInsideZoomed(t *testing.T) {
	src := checker(200, 200)
	r := New(src, Options{Dim: 0.5})
	dst := NewImage(200, 200)
	r.Draw(dst, Lens{X: 105, Y: 105, Radius: 40, Zoom: 4})

	// outside lens: half brightness of source
	if got, want := px(dst, 5, 5), byte(100); got != want {
		t.Fatalf("outside pixel = %d, want %d", got, want)
	}
	// inside lens, 4x zoom: a 10px cell becomes 40px, so the cell containing
	// the centre must extend ~±35px from it with full source brightness.
	if got := px(dst, 105+30, 105); got != 200 && got != 0 {
		t.Fatalf("unexpected lens pixel %d", got)
	}
	// source (95..104,95..104) cell is one colour; at zoom 4 pixels at offset
	// ±20 from the centre should still show the centre cell's value.
	c := px(dst, 105, 105)
	if px(dst, 105+10, 105) != c || px(dst, 105-10, 105) != c {
		t.Fatal("lens is not magnified")
	}
	// without magnification the same offset would cross into another cell
	if px(src, 105+10, 105) == px(src, 105, 105) {
		t.Fatal("test image assumption broken")
	}
}

func TestBlurAndAlpha(t *testing.T) {
	src := checker(64, 64)
	r := New(src, Options{Dim: 1, Blur: true, Border: 2})
	dst := NewImage(64, 64)
	r.Draw(dst, Lens{X: 32, Y: 32, Radius: 10, Zoom: 2})
	for i := 3; i < len(dst.Pix); i += 4 {
		if dst.Pix[i] != 255 {
			t.Fatal("alpha not opaque")
		}
	}
}

func TestLensAtEdgeDoesNotPanic(t *testing.T) {
	src := checker(50, 50)
	r := New(src, Options{Dim: 0.3})
	dst := NewImage(50, 50)
	for _, p := range [][2]float64{{0, 0}, {49, 49}, {-20, 70}} {
		r.Draw(dst, Lens{X: p[0], Y: p[1], Radius: 30, Zoom: 8})
	}
}
