package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type Client struct{ ID, Secret, Path string }

func LoadClient(path string) (Client, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Client{}, errors.New("cannot resolve client credentials path")
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return Client{}, errors.New("cannot read client credentials; check --client-secret path")
	}
	var document struct {
		Installed *struct {
			ID     string `json:"client_id"`
			Secret string `json:"client_secret"`
		} `json:"installed"`
	}
	if json.Unmarshal(data, &document) != nil || document.Installed == nil || strings.TrimSpace(document.Installed.ID) == "" || strings.TrimSpace(document.Installed.Secret) == "" {
		return Client{}, errors.New("invalid credentials: supply a Google Desktop app client JSON")
	}
	return Client{document.Installed.ID, document.Installed.Secret, absolute}, nil
}

func Scope(access string) (string, error) {
	switch access {
	case "read-only":
		return "https://www.googleapis.com/auth/drive.readonly", nil
	case "read-write":
		return "https://www.googleapis.com/auth/drive", nil
	default:
		return "", errors.New("access must be read-only or read-write")
	}
}
