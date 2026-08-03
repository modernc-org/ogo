//go:build ignore

package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

// A stroke is a closed or open polyline in a 0..255 x 0..255 space, which is what
// an 8-bit DAC pair spans. The scope draws a line between consecutive points, so
// the figure is an outline: no fills, and every stroke is a path the beam walks.
type pt struct{ x, y int }

func circle(cx, cy, rx, ry float64, n int, phase float64) []pt {
	var out []pt
	for i := 0; i <= n; i++ {
		a := phase + 2*math.Pi*float64(i)/float64(n)
		out = append(out, pt{int(math.Round(cx + rx*math.Cos(a))), int(math.Round(cy + ry*math.Sin(a)))})
	}
	return out
}

func arc(cx, cy, rx, ry, from, to float64, n int) []pt {
	var out []pt
	for i := 0; i <= n; i++ {
		a := from + (to-from)*float64(i)/float64(n)
		out = append(out, pt{int(math.Round(cx + rx*math.Cos(a))), int(math.Round(cy + ry*math.Sin(a)))})
	}
	return out
}

// gopher returns the face as a list of strokes. t in [0,1) is the phase of the
// dance: the ears wag, the eyes look left and right, and the whole head bobs.
func gopher(t float64) [][]pt {
	bob := 6 * math.Sin(2*math.Pi*t)
	lean := 5 * math.Sin(2*math.Pi*t)
	look := 7 * math.Sin(2*math.Pi*t)
	ear := 8 * math.Sin(2*math.Pi*t)

	cx, cy := 128.0+lean, 118.0+bob

	// The strokes are ordered so the beam's jump from one to the next is short:
	// on a scope that jump is drawn too, and a long one is a bright wrong line.
	var out [][]pt
	// Head.
	out = append(out, circle(cx, cy, 74, 84, 44, math.Pi/2))
	// Ears, sitting on top of the head and wagging with the beat.
	out = append(out, circle(cx-52, cy+72+ear, 17, 17, 18, 0))
	out = append(out, circle(cx+52, cy+72-ear, 17, 17, 18, 0))
	// Eyes: a big round white, and a pupil that looks left and right.
	out = append(out, circle(cx-33, cy+30, 29, 29, 26, 0))
	out = append(out, circle(cx-33+look, cy+30, 10, 10, 12, 0))
	out = append(out, circle(cx+33, cy+30, 29, 29, 26, 0))
	out = append(out, circle(cx+33+look, cy+30, 10, 10, 12, 0))
	// Nose.
	out = append(out, circle(cx, cy-18, 9, 7, 14, 0))
	// The two front teeth, and the line between them.
	out = append(out, []pt{
		{int(cx - 14), int(cy - 30)}, {int(cx + 14), int(cy - 30)},
		{int(cx + 14), int(cy - 56)}, {int(cx - 14), int(cy - 56)},
		{int(cx - 14), int(cy - 30)},
	})
	out = append(out, []pt{{int(cx), int(cy - 30)}, {int(cx), int(cy - 56)}})
	// A smile either side of the teeth.
	out = append(out, arc(cx, cy-16, 40, 30, 1.15*math.Pi, 1.85*math.Pi, 14))
	return out
}

// render draws what a scope in X/Y mode shows: a line between consecutive points
// of a stroke, and a faint one across the jump between strokes.
func render(fr [][]pt, path string) {
	const S = 512
	img := image.NewRGBA(image.Rect(0, 0, S, S))
	for i := range img.Pix {
		img.Pix[i] = 0
	}
	for x := 0; x < S; x++ {
		for y := 0; y < S; y++ {
			img.Set(x, y, color.RGBA{6, 8, 20, 255})
		}
	}
	put := func(x, y float64, c color.RGBA) {
		px, py := int(x*S/256), S-1-int(y*S/256)
		if px >= 0 && px < S && py >= 0 && py < S {
			img.Set(px, py, c)
		}
	}
	line := func(a, b pt, c color.RGBA) {
		n := int(math.Hypot(float64(b.x-a.x), float64(b.y-a.y))*2) + 1
		for i := 0; i <= n; i++ {
			f := float64(i) / float64(n)
			put(float64(a.x)+f*float64(b.x-a.x), float64(a.y)+f*float64(b.y-a.y), c)
		}
	}
	// A jump between strokes is ONE dac update, so a sampling scope shows at most a
	// dot -- which is what the program does and what this preview must show, or the
	// preview flatters it.
	var prev pt
	for si, st := range fr {
		if si != 0 {
			_ = prev
			put(float64(st[0].x), float64(st[0].y), color.RGBA{40, 70, 50, 255})
		}
		for i := 0; i+1 < len(st); i++ {
			line(st[i], st[i+1], color.RGBA{120, 255, 160, 255})
		}
		prev = st[len(st)-1]
	}
	f, _ := os.Create(path)
	png.Encode(f, img)
	f.Close()
}

func main() {
	const nFrames = 8
	var fr [][][]pt
	for i := 0; i < nFrames; i++ {
		f := gopher(float64(i) / float64(nFrames))
		render(f, fmt.Sprintf("frame%d.png", i))
		fr = append(fr, f)
	}
	// Every frame has the same strokes and the same points per stroke -- only the
	// coordinates move -- so the shape table is written once and the frames are a
	// plain rectangular array.
	nStrokes := len(fr[0])
	lens := make([]int, nStrokes)
	total := 0
	for i, st := range fr[0] {
		lens[i] = len(st)
		total += len(st)
	}
	for _, f := range fr {
		if len(f) != nStrokes {
			panic("frames differ in stroke count")
		}
		for i, st := range f {
			if len(st) != lens[i] {
				panic("frames differ in stroke length")
			}
		}
	}
	fmt.Println("strokes", nStrokes, "points per frame", total)

	var b []byte
	out := func(f string, a ...any) { b = append(b, fmt.Sprintf(f, a...)...) }
	out("%s", header)
	out("const (\n\tnFrames  = %d\n\tnStrokes = %d\n\tnPoints  = %d\n)\n\n", nFrames, nStrokes, total)
	out("// strokeLen is how many points each stroke has. The beam JUMPS to the first\n")
	out("// point of a stroke in one step and walks the rest, so a jump costs at most one\n")
	out("// dot on the screen while a drawn segment costs many.\nvar strokeLen [nStrokes]int = [nStrokes]int{")
	for i, n := range lens {
		if i != 0 {
			out(", ")
		}
		out("%d", n)
	}
	out("}\n\n")
	out("// frames is the figure, once per step of the dance: the head leans, the ears\n")
	out("// wag and the eyes look left and right. Every number was produced by the\n")
	out("// generator in doc/gopher-gen.go, which draws the same picture to a PNG.\nvar frames [nFrames][nPoints]pt = [nFrames][nPoints]pt{\n")
	for _, f := range fr {
		out("\t{")
		k := 0
		for _, st := range f {
			for _, p := range st {
				if k != 0 {
					out(", ")
				}
				if k%8 == 0 {
					out("\n\t\t")
				}
				out("{%d, %d}", clamp(p.x), clamp(p.y))
				k++
			}
		}
		out(",\n\t},\n")
	}
	out("}\n%s", footer)
	if err := os.WriteFile("main.ogo", b, 0o644); err != nil {
		panic(err)
	}
}

func clamp(v int) int {
	switch {
	case v < 0:
		return 0
	case v > 255:
		return 255
	}
	return v
}
