//go:build darwin

package main

import "testing"

func TestDarwinPlatformSupported(t *testing.T) {
	if !platformSupported() {
		t.Fatal("macOS daemon is unavailable")
	}
}
