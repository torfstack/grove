package drive

import (
	"context"

	"errors"
	"io"

	"net/url"
	"strings"
)

func (c *Client) Create(ctx context.Context, input Create, content io.Reader) (File, error) {
	metadata := map[string]any{"name": input.Name, "mimeType": input.MIMEType, "appProperties": input.Properties}
	if input.ParentID != "" {
		metadata["parents"] = []string{input.ParentID}
	}
	data, ct, err := encodeFixture(metadata, content)
	if err != nil {
		return File{}, err
	}
	path := c.base + "/files"
	q := url.Values{"fields": {fields}}
	if content != nil {
		path = strings.Replace(c.base, "/drive/v3", "/upload/drive/v3", 1) + "/files"
		q.Set("uploadType", "multipart")
	}
	resp, err := c.request(ctx, "POST", path, q, data, ct, false)
	if err != nil {
		return File{}, err
	}
	var w wireFile
	if err = decode(resp, &w); err != nil {
		return File{}, err
	}
	if w.ID == "" {
		return File{}, errors.New("fixture creation outcome is uncertain")
	}
	return w.file(), nil
}
func (c *Client) Trash(ctx context.Context, id string) error {
	resp, err := c.request(ctx, "PATCH", c.base+"/files/"+url.PathEscape(id), url.Values{"fields": {"id,trashed"}}, []byte(`{"trashed":true}`), "application/json", false)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
