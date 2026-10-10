package auth

import (
	"errors"
	"golang.org/x/oauth2"
	"sync"
)

type persistentSource struct {
	mu     sync.Mutex
	source oauth2.TokenSource
	record Record
	path   string
	save   func(string, Record) error
}

func (s *persistentSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, err := s.source.Token()
	if err != nil {
		return nil, errors.New("cannot refresh authentication; run grove auth if authorization expired")
	}
	copy := *token
	if copy.RefreshToken == "" {
		copy.RefreshToken = s.record.Token.RefreshToken
	}
	previous := s.record.Token
	if copy.AccessToken != previous.AccessToken || copy.RefreshToken != previous.RefreshToken || !copy.Expiry.Equal(previous.Expiry) {
		next := s.record
		next.Token = &copy
		if err := s.save(s.path, next); err != nil {
			return nil, errors.New("cannot persist refreshed authentication")
		}
		s.record = next
	}
	return &copy, nil
}
