package auth

import (
	"context"
	"errors"
	"github.com/torfstack/grove/internal/privatefs"
	"path/filepath"
	"runtime"
	"time"
)

type Options struct{ ClientSecret, Access, TokenFile string }

type Service struct{ Flow Flow }

func (s Service) Authenticate(parent context.Context, opts Options) (string, error) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return "", errors.New("authentication is supported on Linux and macOS only")
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	scope, err := Scope(opts.Access)
	if err != nil {
		return "", err
	}
	if opts.ClientSecret == "" {
		return "", errors.New("--client-secret is required")
	}
	client, err := LoadClient(opts.ClientSecret)
	if err != nil {
		return "", err
	}
	path := opts.TokenFile
	if path == "" {
		path, err = DefaultTokenPath()
		if err != nil {
			return "", err
		}
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", errors.New("cannot resolve token file path")
	}
	lockPath, err := privatefs.Canonical(path)
	if err != nil {
		return "", err
	}
	lock, err := privatefs.Acquire(lockPath + ".lock")
	if err != nil {
		return "", err
	}
	defer func() { _ = lock.Close() }()
	if err = checkDestination(path); err != nil {
		return "", err
	}
	token, err := s.Flow.Authorize(ctx, client, scope)
	if err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	record := Record{Version: 1, ClientID: client.ID, ClientSecretPath: client.Path, Scopes: []string{scope}, Token: token}
	if err = SaveRecord(path, record); err != nil {
		return "", err
	}
	return path, nil
}
