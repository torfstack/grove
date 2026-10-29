//go:build darwin

package tray

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/png"
	"sync"
)

//go:embed icons/grove-idle-32.png
var iconIdle []byte

//go:embed icons/grove-syncing-32.png
var iconSyncing []byte

//go:embed icons/grove-attention-32.png
var iconAttention []byte

//go:embed icons/grove-stopped-32.png
var iconStopped []byte

var templateIcons map[string][]byte
var templateIconsOnce sync.Once

func iconFor(name string) []byte {
	templateIconsOnce.Do(func() {
		templateIcons = make(map[string][]byte)
		for key, raw := range map[string][]byte{
			"grove-idle": iconIdle, "grove-syncing": iconSyncing,
			"grove-attention": iconAttention, "grove-stopped": iconStopped,
		} {
			templateIcons[key] = templateIcon(raw)
		}
	})
	if icon := templateIcons[name]; len(icon) > 0 {
		return icon
	}
	return templateIcons["grove-attention"]
}

func templateIcon(data []byte) []byte {
	source, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	bounds := source.Bounds()
	mask := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := color.NRGBAModel.Convert(source.At(x, y)).(color.NRGBA)
			light := min(c.R, c.G, c.B)
			if light <= 190 {
				continue
			}
			alpha := uint8((uint32(light-190) * uint32(c.A)) / 65)
			mask.SetNRGBA(x, y, color.NRGBA{A: alpha})
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, mask); err != nil {
		return nil
	}
	return output.Bytes()
}
