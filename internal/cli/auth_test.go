package cli

import (
	"bytes"
	"context"
	"github.com/torfstack/grove/internal/auth"
	"strings"
	"testing"
)

func TestAuthFlags(t *testing.T) {
	for _, tt := range []struct {
		name          string
		args          []string
		valid, called bool
	}{
		{"missing", []string{"auth"}, false, false},
		{"empty", []string{"auth", "--client-secret", ""}, false, false},
		{"invalid", []string{"auth", "--client-secret", "client.json", "--access", "bad"}, false, false},
		{"unknown", []string{"auth", "--unknown"}, false, false},
		{"positional", []string{"auth", "--client-secret", "client.json", "extra"}, false, false},
		{"help", []string{"auth", "--help"}, true, false},
		{"default", []string{"auth", "--client-secret", "client.json"}, true, true},
		{"write", []string{"auth", "--client-secret", "client.json", "--access", "read-write", "--token-file", "test.json"}, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			var opts auth.Options
			root := NewRoot(Services{Authenticate: func(ctx context.Context, o auth.Options) (string, error) {
				called = true
				opts = o
				return "saved.json", nil
			}})
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(tt.args)
			err := root.ExecuteContext(context.Background())
			if (err == nil) != tt.valid || called != tt.called {
				t.Fatalf("err=%v called=%v", err, called)
			}
			if called && (opts.ClientSecret != "client.json" || !strings.Contains(out.String(), "saved.json")) {
				t.Fatal("incorrect invocation/output")
			}
			if tt.name == "default" && opts.Access != "read-only" {
				t.Fatal("wrong default")
			}
			if tt.name == "write" && (opts.Access != "read-write" || opts.TokenFile != "test.json") {
				t.Fatal("wrong explicit flags")
			}
		})
	}
}
