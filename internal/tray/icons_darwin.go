//go:build darwin

package tray

import _ "embed"

//go:embed icons/grove-idle-32.png
var iconIdle []byte

//go:embed icons/grove-syncing-32.png
var iconSyncing []byte

//go:embed icons/grove-attention-32.png
var iconAttention []byte

//go:embed icons/grove-stopped-32.png
var iconStopped []byte

func iconFor(name string) []byte {
	switch name {
	case "grove-idle":
		return iconIdle
	case "grove-syncing":
		return iconSyncing
	case "grove-stopped":
		return iconStopped
	default:
		return iconAttention
	}
}
