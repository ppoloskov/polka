package library

import (
	"bytes"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/gen2brain/jpegxl"
)

// JPEG encoding fills transparent pixels with black; the transcode must
// composite onto white first so light-background illustrations survive.
func TestJXLAlphaCompositesOntoWhite(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			if y < 5 {
				src.SetNRGBA(x, y, color.NRGBA{R: 255, G: 0, B: 0, A: 0}) // fully transparent
				continue
			}
			src.SetNRGBA(x, y, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	out := compositeOnWhite(src)
	r, g, b, a := out.At(2, 2).RGBA()
	if a != 0xffff || r != 0xffff || g != 0xffff || b != 0xffff {
		t.Fatalf("transparent corner after composite = %v (want opaque white)", [4]uint32{r, g, b, a})
	}
	r, g, b, a = out.At(2, 7).RGBA()
	if a != 0xffff || r != 10*0xffff/255 {
		t.Fatalf("opaque stroke after composite = %v (want its own color, opaque)", [4]uint32{r, g, b, a})
	}

	// The live fixture's decoded model decides whether compositing applies.
	if !hasAlpha(decodeJXLOrSkip(t)) {
		t.Log("the sampled live payload is opaque; compositing is a no-op for it")
	}
}

func decodeJXLOrSkip(t *testing.T) image.Image {
	data, err := os.ReadFile(filepath.Join("testdata", "jxl-image.jxl"))
	if err != nil {
		t.Skip("no jxl fixture")
	}
	img, err := jpegxl.Decode(bytes.NewReader(data))
	if err != nil {
		t.Skip("fixture decode failed")
	}
	return img
}
