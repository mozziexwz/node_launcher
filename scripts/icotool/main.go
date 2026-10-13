// Command icotool is used by make-icons.ps1. It downsamples large renders
// with a box filter and writes a multi-size .ico (32-bit DIB frames, PNG for
// 256 px).
//
//	icotool out.ico 16=src-256.png 32=src-512.png ...
//	icotool -png out.png 48=src-768.png
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strconv"
	"strings"
)

func main() {
	args := os.Args[1:]
	asPNG := false
	if len(args) > 0 && args[0] == "-png" {
		asPNG = true
		args = args[1:]
	}
	out := args[0]
	type frame struct {
		size int
		data []byte
	}
	var frames []frame
	for _, a := range args[1:] {
		size, src, _ := strings.Cut(a, "=")
		n, err := strconv.Atoi(size)
		check(err)
		img := load(src)
		small := downscale(img, n)
		if asPNG {
			var buf bytes.Buffer
			check(png.Encode(&buf, small))
			check(os.WriteFile(out, buf.Bytes(), 0o644))
			return
		}
		var data []byte
		if n >= 256 {
			var buf bytes.Buffer
			check(png.Encode(&buf, small))
			data = buf.Bytes()
		} else {
			data = dib(small)
		}
		frames = append(frames, frame{n, data})
	}
	var buf bytes.Buffer
	w := func(v any) { check(binary.Write(&buf, binary.LittleEndian, v)) }
	w(uint16(0))
	w(uint16(1))
	w(uint16(len(frames)))
	offset := 6 + 16*len(frames)
	for _, f := range frames {
		dim := uint8(f.size)
		if f.size >= 256 {
			dim = 0
		}
		w(dim)
		w(dim)
		w(uint8(0))
		w(uint8(0))
		w(uint16(1))
		w(uint16(32))
		w(uint32(len(f.data)))
		w(uint32(offset))
		offset += len(f.data)
	}
	for _, f := range frames {
		buf.Write(f.data)
	}
	check(os.WriteFile(out, buf.Bytes(), 0o644))
	fmt.Printf("%s: %d frames, %d bytes\n", out, len(frames), buf.Len())
}

func load(path string) image.Image {
	f, err := os.Open(path)
	check(err)
	defer f.Close()
	img, err := png.Decode(f)
	check(err)
	return img
}

// downscale averages premultiplied colour over each factor x factor block.
func downscale(img image.Image, size int) *image.NRGBA {
	b := img.Bounds()
	if b.Dx() != b.Dy() || b.Dx()%size != 0 {
		panic(fmt.Sprintf("source %dx%d is not a multiple of %d", b.Dx(), b.Dy(), size))
	}
	k := b.Dx() / size
	out := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, bl, a uint64
			for dy := 0; dy < k; dy++ {
				for dx := 0; dx < k; dx++ {
					pr, pg, pb, pa := img.At(b.Min.X+x*k+dx, b.Min.Y+y*k+dy).RGBA()
					r, g, bl, a = r+uint64(pr), g+uint64(pg), bl+uint64(pb), a+uint64(pa)
				}
			}
			if a == 0 {
				continue
			}
			n := uint64(k * k)
			out.SetNRGBA(x, y, color.NRGBA{
				R: uint8(r * 255 / a), G: uint8(g * 255 / a), B: uint8(bl * 255 / a),
				A: uint8((a/n*255 + 32767) / 65535),
			})
		}
	}
	return out
}

func dib(img *image.NRGBA) []byte {
	n := img.Bounds().Dx()
	var buf bytes.Buffer
	w := func(v any) { check(binary.Write(&buf, binary.LittleEndian, v)) }
	maskStride := ((n + 31) / 32) * 4
	w(uint32(40))
	w(int32(n))
	w(int32(2 * n))
	w(uint16(1))
	w(uint16(32))
	w(uint32(0))
	w(uint32(n*n*4 + maskStride*n))
	w(int32(0))
	w(int32(0))
	w(uint32(0))
	w(uint32(0))
	for y := n - 1; y >= 0; y-- {
		for x := 0; x < n; x++ {
			c := img.NRGBAAt(x, y)
			buf.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}
	for y := n - 1; y >= 0; y-- {
		row := make([]byte, maskStride)
		for x := 0; x < n; x++ {
			if img.NRGBAAt(x, y).A == 0 {
				row[x/8] |= 0x80 >> (x % 8)
			}
		}
		buf.Write(row)
	}
	return buf.Bytes()
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
