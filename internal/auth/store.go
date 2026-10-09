package auth

import (
	"encoding/json"
	"errors"
	"golang.org/x/oauth2"
	"os"
	"path/filepath"
	"runtime"
)

type Record struct {
	Version          int           `json:"version"`
	ClientID         string        `json:"client_id"`
	ClientSecretPath string        `json:"client_secret_path"`
	Scopes           []string      `json:"scopes"`
	Token            *oauth2.Token `json:"token"`
}

func DefaultTokenPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", errors.New("cannot determine config directory; use --token-file")
	}
	return filepath.Join(dir, "grove", "token.json"), nil
}

func validRecord(r Record) bool {
	return r.Version == 1 && r.ClientID != "" && filepath.IsAbs(r.ClientSecretPath) && len(r.Scopes) > 0 && r.Token != nil && r.Token.AccessToken != "" && r.Token.RefreshToken != ""
}

func checkDestination(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("token destination must be a regular file, not a symlink or directory")
	}
	return nil
}

func SaveRecord(path string, r Record) error { return saveRecord(path, r, os.Rename) }

func saveRecord(path string, r Record, rename func(string, string) error) error {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return errors.New("token storage is supported on Linux and macOS only")
	}
	if !validRecord(r) {
		return errors.New("cannot save incomplete authentication record")
	}
	if err := checkDestination(path); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return errors.New("cannot encode authentication record")
	}
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return errors.New("cannot create token directory")
	}
	file, err := os.CreateTemp(dir, ".grove-token-*")
	if err != nil {
		return errors.New("cannot create temporary token file")
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err = file.Write(append(data, '\n')); err != nil {
		return errors.New("cannot write token file")
	}
	if err = file.Sync(); err != nil {
		return errors.New("cannot sync token file")
	}
	if err = file.Close(); err != nil {
		return errors.New("cannot close token file")
	}
	if err = checkDestination(path); err != nil {
		return err
	}
	if err = rename(file.Name(), path); err != nil {
		return errors.New("cannot replace token file")
	}
	return nil
}

func LoadRecord(path string) (Record, error) {
	if err := checkDestination(path); err != nil {
		return Record{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, errors.New("cannot read token file; run grove auth")
	}
	var r Record
	if json.Unmarshal(data, &r) != nil || !validRecord(r) {
		return Record{}, errors.New("invalid or unsupported authentication record; run grove auth")
	}
	return r, nil
}
