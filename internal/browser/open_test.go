package browser

import (
	"context"
	"reflect"
	"testing"
)

func TestBrowserLaunchArgv(t *testing.T) {
	for _, tt := range []struct{ platform, command string }{{"linux", "xdg-open"}, {"darwin", "open"}} {
		raw := "https://example.test/?x=$(touch%20bad)&y=a;b"
		var name string
		var args []string
		err := openOn(context.Background(), tt.platform, raw, func(ctx context.Context, n string, a ...string) error { name = n; args = a; return nil })
		if err != nil || name != tt.command || !reflect.DeepEqual(args, []string{raw}) {
			t.Fatalf("incorrect argv %s %v %v", name, args, err)
		}
	}
	if err := openOn(context.Background(), "windows", "https://example.test", func(context.Context, string, ...string) error { t.Fatal("runner called"); return nil }); err == nil {
		t.Fatal("unsupported platform accepted")
	}
}
