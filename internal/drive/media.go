package drive

import (
	"context"
	"errors"
	"io"
	"net/url"
)

func (c *Client) Download(ctx context.Context, id string, dst io.Writer) error {
	resp, err := c.request(ctx, "GET", c.base+"/files/"+url.PathEscape(id), url.Values{"alt": {"media"}}, nil, "", true)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err = io.Copy(dst, resp.Body); err != nil {
		return errors.New("drive download failed")
	}
	return nil
}
