package exeres

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
)

// The icon is drawn here, by code (no third-party art): a keyboard seen from above, a dark body with two rows of
// light keys, one blue key and a space bar. Sizes 16, 32 and 48 are 32-bit DIBs, 256 is a PNG image; both forms
// are what an RT_ICON resource may hold (https://learn.microsoft.com/en-us/windows/win32/menurc/about-icons).

// IconSizes are the sizes of the icon's images, in the order of the group.
var IconSizes = []int{16, 32, 48, 256}

// IconImage is one image of the icon.
type IconImage struct {
	Size int
	Data []byte
}

var (
	colBody   = [3]float64{0x2E, 0x3A, 0x48}
	colKey    = [3]float64{0xEE, 0xF1, 0xF5}
	colAccent = [3]float64{0x3D, 0x8B, 0xD9}
)

type rrect struct{ x0, y0, x1, y1, r float64 }

func (q rrect) has(x, y float64) bool {
	if x < q.x0 || x > q.x1 || y < q.y0 || y > q.y1 {
		return false
	}
	cx := min(max(x, q.x0+q.r), q.x1-q.r)
	cy := min(max(y, q.y0+q.r), q.y1-q.r)
	return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= q.r*q.r
}

type shape struct {
	r   rrect
	col [3]float64
}

// shapes in a unit square, painted in order.
func shapes() []shape {
	s := []shape{{rrect{0.04, 0.20, 0.96, 0.82, 0.10}, colBody}}
	const cols, gap = 5, 0.035
	kw := (0.84 - gap*(cols-1)) / cols
	for row := 0; row < 2; row++ {
		y0 := 0.29 + float64(row)*0.17
		for c := 0; c < cols; c++ {
			x0 := 0.10 + float64(c)*(kw+gap)
			col := colKey
			if row == 0 && c == cols-1 {
				col = colAccent
			}
			s = append(s, shape{rrect{x0, y0, x0 + kw, y0 + 0.13, 0.03}, col})
		}
	}
	s = append(s, shape{rrect{0.24, 0.63, 0.76, 0.74, 0.03}, colKey})
	return s
}

// render draws the icon at n×n pixels, 8×8 samples per pixel, as straight-alpha RGBA rows from the top.
func render(n int) []color.NRGBA {
	const ss = 8
	sh := shapes()
	out := make([]color.NRGBA, n*n)
	for py := 0; py < n; py++ {
		for px := 0; px < n; px++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/ss) / float64(n)
					y := (float64(py) + (float64(sy)+0.5)/ss) / float64(n)
					for i := len(sh) - 1; i >= 0; i-- {
						if sh[i].r.has(x, y) {
							r, g, b, a = r+sh[i].col[0], g+sh[i].col[1], b+sh[i].col[2], a+1
							break
						}
					}
				}
			}
			if a == 0 {
				continue
			}
			out[py*n+px] = color.NRGBA{uint8(r/a + 0.5), uint8(g/a + 0.5), uint8(b/a + 0.5), uint8(a*255/(ss*ss) + 0.5)}
		}
	}
	return out
}

// IconImages draws every image of the icon.
func IconImages() ([]IconImage, error) {
	var out []IconImage
	for _, n := range IconSizes {
		px := render(n)
		if n >= 256 {
			img := image.NewNRGBA(image.Rect(0, 0, n, n))
			for i, c := range px {
				img.SetNRGBA(i%n, i/n, c)
			}
			var b bytes.Buffer
			if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&b, img); err != nil {
				return nil, err
			}
			out = append(out, IconImage{n, b.Bytes()})
			continue
		}
		out = append(out, IconImage{n, dib(n, px)})
	}
	return out, nil
}

// dib writes a 32-bit icon image: BITMAPINFOHEADER with twice the height, BGRA rows from the bottom, then an
// all-zero AND mask (the alpha channel decides).
func dib(n int, px []color.NRGBA) []byte {
	maskRow := (n + 31) / 32 * 4
	b := make([]byte, 40, 40+4*n*n+maskRow*n)
	le := binary.LittleEndian
	le.PutUint32(b[0:], 40)
	le.PutUint32(b[4:], uint32(n))
	le.PutUint32(b[8:], uint32(2*n))
	le.PutUint16(b[12:], 1)
	le.PutUint16(b[14:], 32)
	le.PutUint32(b[20:], uint32(4*n*n+maskRow*n))
	for y := n - 1; y >= 0; y-- {
		for x := 0; x < n; x++ {
			c := px[y*n+x]
			b = append(b, c.B, c.G, c.R, c.A)
		}
	}
	return append(b, make([]byte, maskRow*n)...)
}
