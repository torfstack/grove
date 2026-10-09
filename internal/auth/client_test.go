package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadClient(t *testing.T) {
	for _, tt := range []struct {
		name, data string
		valid      bool
	}{
		{"desktop", `{"installed":{"client_id":"id","client_secret":"secret","auth_uri":"https://evil.invalid","token_uri":"https://evil.invalid"}}`, true},
		{"web", `{"web":{"client_id":"id","client_secret":"secret"}}`, false},
		{"missing", `{"installed":{"client_id":"id"}}`, false},
		{"malformed", `{"installed":SECRET_SENTINEL`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "client.json")
			if err := os.WriteFile(p, []byte(tt.data), 0600); err != nil {
				t.Fatal(err)
			}
			c, err := LoadClient(p)
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v error=%v", tt.valid, err)
			}
			if err != nil && strings.Contains(err.Error(), "SECRET_SENTINEL") {
				t.Fatal("secret leaked")
			}
			if tt.valid && (c.ID != "id" || c.Secret != "secret" || c.Path != p) {
				t.Fatalf("unexpected client metadata")
			}
		})
	}
	t.Chdir(t.TempDir())
	os.WriteFile("client.json", []byte(`{"installed":{"client_id":"id","client_secret":"secret"}}`), 0600)
	c, err := LoadClient("client.json")
	if err != nil || !filepath.IsAbs(c.Path) {
		t.Fatal("relative path not resolved", err)
	}
}

func TestScope(t *testing.T) {
	for _, tt := range []struct{ access, want string }{{"read-only", "https://www.googleapis.com/auth/drive.readonly"}, {"read-write", "https://www.googleapis.com/auth/drive"}} {
		got, err := Scope(tt.access)
		if err != nil || got != tt.want {
			t.Fatalf("%s: %q %v", tt.access, got, err)
		}
	}
	if _, err := Scope("invalid"); err == nil {
		t.Fatal("accepted invalid access")
	}
}
