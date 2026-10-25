package drive

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

type ChangesPage struct {
	NextToken, StartToken string
	Changed               bool
}

type ChangesAPI interface {
	StartChanges(context.Context) (string, error)
	ListChanges(context.Context, string) (ChangesPage, error)
}

const maxChangesToken = 64 * 1024
const maxChangesResponse = 1024 * 1024

func validChangesToken(token string) bool { return token != "" && len(token) <= maxChangesToken }

func decodeChanges(resp *http.Response, target any) error {
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxChangesResponse+1))
	closeErr := resp.Body.Close()
	var decodeErr error
	if readErr == nil {
		if len(data) > maxChangesResponse {
			decodeErr = errors.New("changes response exceeds limit")
		} else {
			decodeErr = json.Unmarshal(data, target)
		}
	}
	if err := errors.Join(readErr, decodeErr, closeErr); err != nil {
		return privateError{"invalid Drive changes response", err}
	}
	return nil
}

func (c *Client) StartChanges(ctx context.Context) (string, error) {
	q := url.Values{"fields": {"startPageToken"}, "supportsAllDrives": {"false"}}
	resp, err := c.request(ctx, http.MethodGet, c.base+"/changes/startPageToken", q, nil, "", true)
	if err != nil {
		return "", err
	}
	var wire struct {
		Token string `json:"startPageToken"`
	}
	if err := decodeChanges(resp, &wire); err != nil {
		return "", err
	}
	if !validChangesToken(wire.Token) {
		return "", errors.New("invalid Drive changes cursor")
	}
	return wire.Token, nil
}

func (c *Client) ListChanges(ctx context.Context, token string) (ChangesPage, error) {
	if !validChangesToken(token) {
		return ChangesPage{}, errors.New("invalid Drive changes cursor")
	}
	size := c.pageSize
	if size < 1 {
		size = 100
	}
	if size > 1000 {
		size = 1000
	}
	q := url.Values{"pageToken": {token}, "pageSize": {strconv.Itoa(size)}, "fields": {"nextPageToken,newStartPageToken,changes(changeType)"}, "spaces": {"drive"}, "restrictToMyDrive": {"true"}, "includeRemoved": {"true"}, "includeItemsFromAllDrives": {"false"}, "supportsAllDrives": {"false"}}
	resp, err := c.request(ctx, http.MethodGet, c.base+"/changes", q, nil, "", true)
	if err != nil {
		return ChangesPage{}, err
	}
	var wire struct {
		Next    string            `json:"nextPageToken"`
		Start   string            `json:"newStartPageToken"`
		Changes []json.RawMessage `json:"changes"`
	}
	if err := decodeChanges(resp, &wire); err != nil {
		return ChangesPage{}, err
	}
	if len(wire.Next) > maxChangesToken || len(wire.Start) > maxChangesToken {
		return ChangesPage{}, errors.New("invalid Drive changes cursor")
	}
	return ChangesPage{NextToken: wire.Next, StartToken: wire.Start, Changed: len(wire.Changes) > 0}, nil
}
