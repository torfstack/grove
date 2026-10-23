//go:build linux

package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/torfstack/grove/internal/syncengine"
)

func writeConfig(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "daemon.json")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func configText(profile, mode, interval string) string {
	v := map[string]any{"version": 1, "profile_dir": profile, "mode": mode}
	if interval != "" {
		v["interval"] = interval
	}
	data, _ := json.Marshal(v)
	return string(data)
}

func TestLoadDaemonConfig(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "profile")
	base := configText(profile, "download", "")
	for _, tc := range []struct {
		name, data string
		valid      bool
		interval   time.Duration
	}{
		{"default", base, true, 5 * time.Minute},
		{"minimum", configText(profile, "two-way", "1s"), true, time.Second},
		{"explicit", configText(profile, "download", "30s"), true, 30 * time.Second},
		{"short", configText(profile, "download", "999ms"), false, 0},
		{"overflow", configText(profile, "download", "99999999999999999999h"), false, 0},
		{"bad-duration", configText(profile, "download", "later"), false, 0},
		{"mode", configText(profile, "automatic", ""), false, 0},
		{"missing-mode", fmt.Sprintf(`{"version":1,"profile_dir":%q}`, profile), false, 0},
		{"version", strings.Replace(base, `"version":1`, `"version":2`, 1), false, 0},
		{"float-version", strings.Replace(base, `"version":1`, `"version":1.0`, 1), false, 0},
		{"missing-version", strings.Replace(base, `,"version":1`, "", 1), false, 0},
		{"unknown", strings.TrimSuffix(base, "}") + `,"private-key":"secret"}`, false, 0},
		{"duplicate", strings.TrimSuffix(base, "}") + `,"mode":"two-way"}`, false, 0},
		{"escaped-duplicate", strings.TrimSuffix(base, "}") + `,"\u006dode":"two-way"}`, false, 0},
		{"trailing", base + ` {}`, false, 0},
		{"null-interval", strings.TrimSuffix(base, "}") + `,"interval":null}`, false, 0},
		{"number-interval", strings.TrimSuffix(base, "}") + `,"interval":5}`, false, 0},
		{"empty-interval", strings.TrimSuffix(base, "}") + `,"interval":""}`, false, 0},
		{"invalid-utf8", base + string([]byte{0xff}), false, 0},
		{"relative-profile", configText("relative", "download", ""), false, 0},
		{"tilde", configText("~/profile", "download", ""), false, 0},
		{"environment", configText("$HOME/profile", "download", ""), false, 0},
		{"limit", base + strings.Repeat(" ", 64*1024-len(base)), true, 5 * time.Minute},
		{"over-limit", base + strings.Repeat(" ", 64*1024-len(base)+1), false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := LoadConfig(writeConfig(t, tc.data))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t error=%v", tc.valid, err)
			}
			if tc.valid && (cfg.ProfileDir != profile || cfg.Interval != tc.interval) {
				t.Fatalf("wrong config: %+v", cfg)
			}
			if tc.valid {
				want := ModeDownload
				if tc.name == "minimum" {
					want = ModeTwoWay
				}
				if cfg.Mode != want {
					t.Fatal("configured mode changed")
				}
			}
			if err != nil && (strings.Contains(err.Error(), profile) || strings.Contains(err.Error(), "secret")) {
				t.Fatal("config error leaked values")
			}
		})
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatal("parsing created profile")
	}
}

func TestDaemonConfigFileAndAliases(t *testing.T) {
	profile := t.TempDir()
	path := writeConfig(t, configText(profile, "download", ""))
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("public config accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "config.json")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{alias, profile, "relative.json"} {
		if _, err := LoadConfig(bad); err == nil {
			t.Fatalf("invalid file accepted: %s", bad)
		}
	}
	profileAlias := filepath.Join(t.TempDir(), "profile")
	if err := os.Symlink(profile, profileAlias); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(writeConfig(t, configText(profileAlias, "download", "")))
	if err != nil || cfg.ProfileDir != profile {
		t.Fatalf("alias not canonical: %v", err)
	}
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(writeConfig(t, configText(filepath.Join(repo, "profile"), "download", ""))); err == nil {
		t.Fatal("Git profile accepted")
	}
}

func TestValidateDaemonProfileConfig(t *testing.T) {
	profile := t.TempDir()
	local := t.TempDir()
	path := writeConfig(t, configText(profile, "download", ""))
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	summary := syncengine.ProfileSummary{Binding: syncengine.Binding{LocalDir: local}, PopulationComplete: false}
	if err := ValidateProfileConfig(cfg, summary); err != nil {
		t.Fatal(err)
	}
	cfg.Mode = ModeTwoWay
	if err := ValidateProfileConfig(cfg, summary); err == nil {
		t.Fatal("incomplete two-way accepted")
	}
	summary.PopulationComplete = true
	if err := ValidateProfileConfig(cfg, summary); err != nil {
		t.Fatal(err)
	}
	cfg.ConfigPath = filepath.Join(local, "daemon.json")
	if err := ValidateProfileConfig(cfg, summary); err == nil {
		t.Fatal("config inside destination accepted")
	}
	for _, name := range []string{"state.json", "profile.lock", "daemon.lock", "daemon-status.json"} {
		cfg.ConfigPath = filepath.Join(profile, name)
		if err := ValidateProfileConfig(cfg, summary); err == nil {
			t.Fatalf("reserved config accepted: %s", name)
		}
	}
	cfg.ConfigPath = path
	if err := os.Link(path, filepath.Join(profile, "daemon-status.json")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateProfileConfig(cfg, summary); err == nil {
		t.Fatal("reserved hardlink alias accepted")
	}
}
