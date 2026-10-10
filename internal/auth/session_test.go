package auth

import (
	"context"
	"golang.org/x/oauth2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func sessionRecord(t *testing.T, scope string) (string, Record) {
	t.Helper()
	dir := t.TempDir()
	client := filepath.Join(dir, "client.json")
	if err := os.WriteFile(client, []byte(`{"installed":{"client_id":"test-client","client_secret":"synthetic-secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	r := Record{Version: 1, ClientID: "test-client", ClientSecretPath: client, Scopes: []string{scope}, Token: &oauth2.Token{AccessToken: "synthetic-access", RefreshToken: "synthetic-refresh", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)}}
	path := filepath.Join(dir, "token.json")
	if err := SaveRecord(path, r); err != nil {
		t.Fatal(err)
	}
	return path, r
}
func TestSessionScopes(t *testing.T) {
	for _, tc := range []struct {
		scope, access string
		ok            bool
	}{{"read-only", "read-only", true}, {"read-write", "read-only", true}, {"read-only", "read-write", false}, {"read-write", "read-write", true}} {
		scope, _ := Scope(tc.scope)
		path, _ := sessionRecord(t, scope)
		s, err := OpenSession(context.Background(), path, tc.access)
		if (err == nil) != tc.ok {
			t.Fatalf("scope validation: %v", err)
		}
		if s != nil {
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}
func TestSessionClientMismatch(t *testing.T) {
	scope, _ := Scope("read-only")
	path, r := sessionRecord(t, scope)
	r.ClientID = "different"
	if err := SaveRecord(path, r); err != nil {
		t.Fatal(err)
	}
	s, err := OpenSession(context.Background(), path, "read-only")
	if s != nil {
		_ = s.Close()
	}
	if err == nil {
		t.Fatal("accepted mismatched client")
	}
}
func TestRefreshPersists(t *testing.T)            { testRefresh(t, "new-refresh", false) }
func TestRefreshRetainsRefreshToken(t *testing.T) { testRefresh(t, "", false) }
func TestRefreshSaveFailure(t *testing.T)         { testRefresh(t, "", true) }
func testRefresh(t *testing.T, refresh string, fail bool) {
	t.Helper()
	scope, _ := Scope("read-only")
	path, r := sessionRecord(t, scope)
	r.Token.Expiry = time.Now().Add(-time.Hour)
	if err := SaveRecord(path, r); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","token_type":"Bearer","expires_in":3600,"refresh_token":"` + refresh + `"}`))
	}))
	defer server.Close()
	save := SaveRecord
	if fail {
		save = func(string, Record) error { return os.ErrPermission }
	}
	s, err := openSession(context.Background(), path, "read-only", oauth2.Endpoint{TokenURL: server.URL}, save)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	resp, err := s.HTTPClient.Get(server.URL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if fail {
		if err == nil {
			t.Fatal("save failure ignored")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	got, err := LoadRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	want := refresh
	if want == "" {
		want = "synthetic-refresh"
	}
	if got.Token.AccessToken != "new-access" || got.Token.RefreshToken != want {
		t.Fatal("refresh not persisted")
	}
}
func TestAuthSessionExclusion(t *testing.T) {
	scope, _ := Scope("read-only")
	path, _ := sessionRecord(t, scope)
	s, err := OpenSession(context.Background(), path, "read-only")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if _, err := OpenSession(context.Background(), path, "read-only"); err == nil {
		t.Fatal("parallel session accepted")
	}
	_, err = (Service{}).Authenticate(context.Background(), Options{TokenFile: path, ClientSecret: "absent", Access: "read-only"})
	if err == nil {
		t.Fatal("parallel auth accepted")
	}
}
