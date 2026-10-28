package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/torfstack/grove/internal/privatefs"
	"github.com/torfstack/grove/internal/syncengine"
)

type Mode string

const (
	ModeDownload Mode = "download"
	ModeTwoWay   Mode = "two-way"
)

type Config struct {
	RemotePollInterval time.Duration
	WatchLocal         bool
	ConfigPath         string
	ProfileDir         string
	Mode               Mode
	Interval           time.Duration
}

func configError(field string) error { return fmt.Errorf("invalid daemon configuration: %s", field) }

func LoadConfig(path string) (Config, error) {
	if !filepath.IsAbs(path) {
		return Config{}, configError("absolute config path required")
	}
	parent, err := privatefs.Canonical(filepath.Dir(path))
	if err != nil {
		return Config{}, configError("config path")
	}
	path = filepath.Join(parent, filepath.Base(path))
	if err := privatefs.OutsideGit(path); err != nil {
		return Config{}, configError("config must be outside Git")
	}
	f, err := openConfig(path)
	if err != nil {
		return Config{}, err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, 64*1024+1))
	closeErr := f.Close()
	if readErr != nil || closeErr != nil {
		return Config{}, configError("cannot read file")
	}
	if len(data) > 64*1024 || !utf8.Valid(data) {
		return Config{}, configError("file size or encoding")
	}
	fields, err := configFields(data)
	if err != nil {
		return Config{}, err
	}
	var version int
	if err := json.Unmarshal(fields["version"], &version); err != nil || version != 1 {
		return Config{}, configError("version")
	}
	readString := func(key string) (string, error) {
		raw := fields[key]
		if len(raw) == 0 || raw[0] != '"' {
			return "", configError(key)
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", configError(key)
		}
		return value, nil
	}
	profile, err := readString("profile_dir")
	if err != nil || !filepath.IsAbs(profile) {
		return Config{}, configError("profile_dir")
	}
	profile, err = privatefs.Canonical(profile)
	if err != nil {
		return Config{}, configError("profile_dir")
	}
	if err := privatefs.OutsideGit(profile); err != nil {
		return Config{}, configError("profile must be outside Git")
	}
	mode, err := readString("mode")
	if err != nil || Mode(mode) != ModeDownload && Mode(mode) != ModeTwoWay {
		return Config{}, configError("mode")
	}
	interval := 5 * time.Minute
	if _, exists := fields["interval"]; exists {
		text, err := readString("interval")
		if err != nil {
			return Config{}, err
		}
		interval, err = time.ParseDuration(text)
		if err != nil || interval < time.Second {
			return Config{}, configError("interval")
		}
	}
	watch := false
	if raw, exists := fields["watch_local"]; exists {
		if string(raw) != "true" && string(raw) != "false" {
			return Config{}, configError("watch_local")
		}
		watch = string(raw) == "true"
	}
	if watch && !localWatchSupported() {
		return Config{}, configError("watch_local is unavailable on this platform")
	}
	remoteInterval := time.Duration(0)
	if _, exists := fields["remote_poll_interval"]; exists {
		value, readErr := readString("remote_poll_interval")
		if readErr != nil {
			return Config{}, readErr
		}
		remoteInterval, err = time.ParseDuration(value)
		if err != nil || remoteInterval < 10*time.Second {
			return Config{}, configError("remote_poll_interval")
		}
	}
	return Config{RemotePollInterval: remoteInterval, WatchLocal: watch, ConfigPath: path, ProfileDir: profile, Mode: Mode(mode), Interval: interval}, nil
}

func configFields(data []byte) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, configError("JSON object required")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, configError("JSON")
		}
		key, ok := token.(string)
		if !ok {
			return nil, configError("JSON")
		}
		switch key {
		case "version", "profile_dir", "mode", "interval", "watch_local", "remote_poll_interval":
		default:
			return nil, configError("unknown field")
		}
		if _, exists := fields[key]; exists {
			return nil, configError("duplicate field")
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, configError("JSON")
		}
		fields[key] = value
	}
	if _, err := d.Token(); err != nil {
		return nil, configError("JSON")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, configError("trailing data")
	}
	return fields, nil
}

func withinPath(parent, path string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

func ValidateProfileConfig(cfg Config, summary syncengine.ProfileSummary) error {
	if withinPath(summary.Binding.LocalDir, cfg.ConfigPath) {
		return configError("config is inside destination")
	}
	info, err := os.Stat(cfg.ConfigPath)
	if err != nil {
		return configError("cannot inspect config")
	}
	for _, name := range []string{"state.json", "profile.lock", "daemon.lock", "daemon-status.json"} {
		reserved := filepath.Join(cfg.ProfileDir, name)
		canonical, err := privatefs.Canonical(reserved)
		if err != nil {
			return configError("reserved record")
		}
		if cfg.ConfigPath == canonical {
			return configError("reserved record")
		}
		other, err := os.Stat(reserved)
		if err == nil && os.SameFile(info, other) {
			return configError("reserved record alias")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return configError("cannot inspect reserved record")
		}
	}
	if cfg.Mode == ModeTwoWay && !summary.PopulationComplete {
		return configError("two-way requires populated profile")
	}
	return nil
}
