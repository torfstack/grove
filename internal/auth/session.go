package auth

import (
	"context"
	"errors"
	"github.com/torfstack/grove/internal/privatefs"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"net/http"
)

type Session struct {
	HTTPClient *http.Client
	lock       *privatefs.Lock
}

func OpenSession(ctx context.Context, path, access string) (*Session, error) {
	return openSession(ctx, path, access, google.Endpoint, SaveRecord)
}
func openSession(ctx context.Context, path, access string, endpoint oauth2.Endpoint, save func(string, Record) error) (*Session, error) {
	if err := checkDestination(path); err != nil {
		return nil, err
	}
	path, err := privatefs.Canonical(path)
	if err != nil {
		return nil, err
	}
	lock, err := privatefs.Acquire(path + ".lock")
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = lock.Close()
		}
	}()
	record, err := LoadRecord(path)
	if err != nil {
		return nil, err
	}
	client, err := LoadClient(record.ClientSecretPath)
	if err != nil {
		return nil, err
	}
	if client.ID != record.ClientID {
		return nil, errors.New("credentials do not match saved authentication; run grove auth")
	}
	required, err := Scope(access)
	if err != nil {
		return nil, err
	}
	full, _ := Scope("read-write")
	allowed := false
	for _, scope := range record.Scopes {
		if scope == required || scope == full {
			allowed = true
		}
	}
	if !allowed {
		return nil, errors.New("authentication lacks required Drive access; run grove auth with the required access")
	}
	config := oauth2.Config{ClientID: client.ID, ClientSecret: client.Secret, Endpoint: endpoint}
	source := &persistentSource{source: config.TokenSource(ctx, record.Token), record: record, path: path, save: save}
	httpClient := oauth2.NewClient(ctx, source)
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }
	success = true
	return &Session{HTTPClient: httpClient, lock: lock}, nil
}
func (s *Session) Close() error { return s.lock.Close() }
