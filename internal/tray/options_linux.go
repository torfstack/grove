package tray

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"unicode"
	"unicode/utf8"

	"github.com/torfstack/grove/internal/privatefs"
)

type Options struct{ ProfileDir, Name, SystemdInstance string }

func validName(name string) bool {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > 80 || name == "" {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return false
		}
	}
	return true
}
func validInstance(instance string) bool {
	if instance == "" || len(instance) > 128 {
		return false
	}
	for _, r := range instance {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
func ValidateArguments(opts Options) (Options, error) {
	if opts.Name == "" {
		opts.Name = "Grove"
	}
	if !validName(opts.Name) {
		return Options{}, errors.New("invalid tray display name")
	}
	if opts.SystemdInstance != "" && !validInstance(opts.SystemdInstance) {
		return Options{}, errors.New("invalid service instance")
	}
	if !filepath.IsAbs(opts.ProfileDir) {
		return Options{}, errors.New("absolute profile directory required")
	}
	return opts, nil
}
func NormalizeOptions(opts Options) (Options, error) {
	opts, err := ValidateArguments(opts)
	if err != nil {
		return Options{}, err
	}
	profile, err := privatefs.Canonical(opts.ProfileDir)
	if err != nil || privatefs.OutsideGit(profile) != nil {
		return Options{}, errors.New("invalid profile directory")
	}
	info, err := os.Stat(profile)
	if err != nil || !info.IsDir() {
		return Options{}, errors.New("existing profile directory required")
	}
	info, err = os.Lstat(filepath.Join(profile, "state.json"))
	if err != nil || !info.Mode().IsRegular() {
		return Options{}, errors.New("existing profile state required")
	}
	opts.ProfileDir = profile
	return opts, nil
}
func Identity(profile string) string {
	sum := sha256.Sum256([]byte(profile))
	return hex.EncodeToString(sum[:])
}
