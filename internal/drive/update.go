package drive

import (
	"context"
	"errors"
	"io"
	"net/url"
	"strings"
)

func (c *Client) Update(ctx context.Context, id string, input Update, content io.Reader) (File, error) {
	if id == "" || input.ContentChanged != (content != nil) {
		return File{}, errors.New("invalid fixture update")
	}
	metadata := map[string]any{}
	if input.Name != "" {
		metadata["name"] = input.Name
	}
	data, ct, err := encodeFixture(metadata, content)
	if err != nil {
		return File{}, err
	}
	q := url.Values{"fields": {fields}}
	if input.AddParent != "" {
		q.Set("addParents", input.AddParent)
	}
	if input.RemoveParent != "" {
		q.Set("removeParents", input.RemoveParent)
	}
	path := c.base + "/files/" + url.PathEscape(id)
	if content != nil {
		path = strings.Replace(c.base, "/drive/v3", "/upload/drive/v3", 1) + "/files/" + url.PathEscape(id)
		q.Set("uploadType", "multipart")
	}
	resp, err := c.request(ctx, "PATCH", path, q, data, ct, false)
	if err != nil {
		return File{}, err
	}
	var wire wireFile
	if err = decode(resp, &wire); err != nil {
		return File{}, err
	}
	if wire.ID != id {
		return File{}, errors.New("fixture update outcome is uncertain")
	}
	return wire.file(), nil
}
