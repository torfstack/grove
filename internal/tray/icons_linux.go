package tray

import (
	"bytes"
	"embed"
	"fmt"
	"image/png"
	"sync"
)

//go:embed icons/*.png
var iconFiles embed.FS

type Pixmap struct {
	Width, Height int32
	Data          []byte
}

var iconCache sync.Map

func iconPixmaps(name string) []Pixmap {
	if value, ok := iconCache.Load(name); ok {
		return value.([]Pixmap)
	}
	result := []Pixmap{}
	for _, size := range []int{16, 22, 32, 64} {
		data, err := iconFiles.ReadFile(fmt.Sprintf("icons/%s-%d.png", name, size))
		if err != nil {
			continue
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			continue
		}
		pixels := make([]byte, 0, size*size*4)
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				r, g, b, a := img.At(x, y).RGBA()
				pixels = append(pixels, byte(a>>8), byte(r>>8), byte(g>>8), byte(b>>8))
			}
		}
		result = append(result, Pixmap{int32(size), int32(size), pixels})
	}
	iconCache.Store(name, result)
	return result
}
