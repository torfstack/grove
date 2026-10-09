package auth

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeClient(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "client.json")
	if err := os.WriteFile(p, []byte(`{"installed":{"client_id":"id","client_secret":"CLIENT_SECRET_SENTINEL","auth_uri":"https://evil.invalid","token_uri":"https://evil.invalid"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAuthenticatePersistsRecord(t *testing.T) {
	f := newFlowFixture(t, goodToken, false)
	f.flow.OpenBrowser = f.open(t, func() {
		f.callback(t, url.Values{"state": {f.request.Get("state")}, "code": {"CODE_SENTINEL"}}, "/callback", "GET")
	})
	path := filepath.Join(t.TempDir(), "grove", "token.json")
	client := writeClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := (Service{Flow: f.flow}).Authenticate(ctx, Options{ClientSecret: client, Access: "read-write", TokenFile: path})
	if err != nil || got != path {
		t.Fatal(got, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r := decodeRecord(t, data)
	if r.Version != 1 || r.ClientID != "id" || r.ClientSecretPath != client || len(r.Scopes) != 1 || r.Scopes[0] != "https://www.googleapis.com/auth/drive" || r.Token.AccessToken != "ACCESS_SENTINEL" || r.Token.RefreshToken != "REFRESH_SENTINEL" {
		t.Fatal("incorrect stored auth")
	}
	for _, sentinel := range []string{"ACCESS_SENTINEL", "REFRESH_SENTINEL", "CLIENT_SECRET_SENTINEL", "CODE_SENTINEL"} {
		if strings.Contains(f.output.String(), sentinel) {
			t.Fatal("secret leaked")
		}
	}
}

func TestAuthenticateFailurePreservesToken(t *testing.T) {
	for _, kind := range []string{"denied", "cancel", "missing-refresh", "exchange-failure", "save-failure"} {
		t.Run(kind, func(t *testing.T) {
			body := goodToken
			switch kind {
			case "missing-refresh":
				body = `{"access_token":"ACCESS_SENTINEL"}`
			case "exchange-failure":
				body = `{"error":"invalid_grant","error_description":"SECRET_SENTINEL"}`
			}
			f := newFlowFixture(t, body, false)
			p := filepath.Join(t.TempDir(), "token.json")
			if err := SaveRecord(p, testRecord()); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(p)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			f.flow.OpenBrowser = f.open(t, func() {
				if kind == "cancel" {
					cancel()
					return
				}
				if kind == "save-failure" {
					os.Remove(p)
					os.Symlink(filepath.Join(filepath.Dir(p), "original.json"), p)
					os.WriteFile(filepath.Join(filepath.Dir(p), "original.json"), before, 0600)
				}
				q := url.Values{"state": {f.request.Get("state")}, "code": {"CODE_SENTINEL"}}
				if kind == "denied" {
					q.Del("code")
					q.Set("error", "SECRET_SENTINEL")
				}
				f.callback(t, q, "/callback", "GET")
			})
			got, err := (Service{Flow: f.flow}).Authenticate(ctx, Options{ClientSecret: writeClient(t), Access: "read-only", TokenFile: p})
			if err == nil || got != "" || strings.Contains(err.Error(), "SENTINEL") {
				t.Fatal("incorrect failure", got, err)
			}
			after, _ := os.ReadFile(p)
			if !bytes.Equal(before, after) {
				t.Fatal("existing record changed")
			}
		})
	}
}

func TestAuthenticateValidatesBeforeBrowser(t *testing.T) {
	for _, opts := range []Options{{Access: "invalid"}, {ClientSecret: "missing.json", Access: "read-only"}} {
		s := Service{Flow: Flow{OpenBrowser: func(context.Context, string) error { t.Error("browser opened before validation"); return nil }}}
		if _, err := s.Authenticate(context.Background(), opts); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
}
