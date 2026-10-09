package auth

import (
	"bytes"
	"golang.org/x/oauth2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func testRecord() Record {
	return Record{Version: 1, ClientID: "id", ClientSecretPath: "/client.json", Scopes: []string{"https://www.googleapis.com/auth/drive.readonly"}, Token: &oauth2.Token{AccessToken: "ACCESS_SENTINEL", RefreshToken: "REFRESH_SENTINEL", TokenType: "Bearer", Expiry: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}}
}

func TestRecordRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "grove", "token.json")
	want := testRecord()
	if err := SaveRecord(p, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRecord(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.ClientID != "id" || got.ClientSecretPath != "/client.json" || len(got.Scopes) != 1 || got.Scopes[0] != "https://www.googleapis.com/auth/drive.readonly" || got.Token.AccessToken != "ACCESS_SENTINEL" || got.Token.RefreshToken != "REFRESH_SENTINEL" || !got.Token.Expiry.Equal(want.Token.Expiry) {
		t.Fatal("record did not roundtrip")
	}
	for path, mode := range map[string]os.FileMode{p: 0600, filepath.Dir(p): 0700} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("permissions: %v %v", info, err)
		}
	}
}

func TestDefaultTokenPath(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	p, err := DefaultTokenPath()
	if err != nil || p != filepath.Join(base, "grove", "token.json") {
		t.Fatal("wrong default", p, err)
	}
	if runtime.GOOS == "linux" && p != filepath.Join(xdg, "grove", "token.json") {
		t.Fatal("XDG ignored")
	}
}

func TestAtomicSaveFailurePreservesToken(t *testing.T) {
	p := filepath.Join(t.TempDir(), "token.json")
	if err := SaveRecord(p, testRecord()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(p)
	for _, token := range []*oauth2.Token{nil, {AccessToken: "new"}, {RefreshToken: "new"}} {
		r := testRecord()
		r.Token = token
		if err := SaveRecord(p, r); err == nil {
			t.Fatal("invalid token saved")
		}
		after, _ := os.ReadFile(p)
		if !bytes.Equal(before, after) {
			t.Fatal("existing token changed")
		}
	}
	// Force failure after the temporary file is written, before replacement.
	r := testRecord()
	r.Token.AccessToken = "new"
	err := saveRecord(p, r, func(string, string) error { return os.ErrPermission })
	if err == nil {
		t.Fatal("rename failure ignored")
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) {
		t.Fatal("rename failure changed token")
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatal("temporary files left behind")
	}
}

func TestRejectSymlinkDestination(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "token.json")
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
	if err := SaveRecord(p, testRecord()); err == nil {
		t.Fatal("accepted symlink")
	}
	if _, err := LoadRecord(p); err == nil {
		t.Fatal("loaded symlink")
	}
	b, _ := os.ReadFile(target)
	if string(b) != "unchanged" {
		t.Fatal("symlink target modified")
	}
}

func TestLoadUnsupportedVersion(t *testing.T) {
	p := filepath.Join(t.TempDir(), "token.json")
	if err := os.WriteFile(p, []byte(`{"version":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRecord(p); err == nil {
		t.Fatal("unsupported version accepted")
	}
}

func TestRecordErrorsRedacted(t *testing.T) {
	p := filepath.Join(t.TempDir(), "token.json")
	if err := os.WriteFile(p, []byte(`{"token":ACCESS_SENTINEL`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadRecord(p)
	if err == nil || strings.Contains(err.Error(), "ACCESS_SENTINEL") {
		t.Fatal("invalid error", err)
	}
}

func TestReplacementPermissions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "token.json")
	if err := os.WriteFile(p, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0644); err != nil {
		t.Fatal(err)
	}
	if err := SaveRecord(p, testRecord()); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0600 {
		t.Fatal("replacement permissions", info.Mode())
	}
}
