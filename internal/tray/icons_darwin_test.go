//go:build darwin

package tray

import (
	"bytes"
	"image/png"
	"testing"
)

func TestMenuBarTemplateIconsHaveDistinctMasks(t *testing.T) {
	masks := make(map[string]string)
	for _, name := range []string{"grove-idle", "grove-syncing", "grove-attention", "grove-stopped"} {
		icon, err := png.Decode(bytes.NewReader(iconFor(name)))
		if err != nil {
			t.Fatal(err)
		}
		var alpha []byte
		bounds := icon.Bounds()
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				_, _, _, a := icon.At(x, y).RGBA()
				alpha = append(alpha, byte(a>>8))
			}
		}
		if bytes.Count(alpha, []byte{0}) == len(alpha) {
			t.Fatalf("%s has empty mask", name)
		}
		for previous, mask := range masks {
			if mask == string(alpha) {
				t.Fatalf("%s and %s have the same template mask", previous, name)
			}
		}
		masks[name] = string(alpha)
	}
}
