package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type ClientOptions struct {
	BaseURL  string
	PageSize int
}
type Client struct {
	http     *http.Client
	base     string
	pageSize int
}

func NewClient(client *http.Client, opts ClientOptions) *Client {
	if opts.BaseURL == "" {
		opts.BaseURL = "https://www.googleapis.com/drive/v3"
	}
	if opts.PageSize == 0 {
		opts.PageSize = 100
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }
	return &Client{http: &copy, base: strings.TrimRight(opts.BaseURL, "/"), pageSize: opts.PageSize}
}

const fields = "id,name,mimeType,parents,size,md5Checksum,version,driveId,ownedByMe,trashed,appProperties,capabilities(canDownload)"

var ErrNotFound = errors.New("drive object is missing")

func (c *Client) request(ctx context.Context, method, path string, q url.Values, data []byte, contentType string, readOnly bool) (*http.Response, error) {
	attempts := 1
	if readOnly {
		attempts = 3
	}
	for n := 0; n < attempts; n++ {
		req, err := http.NewRequestWithContext(ctx, method, path+"?"+q.Encode(), bytes.NewReader(data))
		if err != nil {
			return nil, errors.New("cannot build Drive request")
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, errors.New("drive request failed or cancelled")
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}
		_ = resp.Body.Close()
		if resp.StatusCode == 404 {
			return nil, ErrNotFound
		}
		transient := resp.StatusCode == 429 || resp.StatusCode >= 500
		if !readOnly || !transient || n+1 == attempts {
			return nil, errors.New("drive request rejected")
		}
		delay := time.Duration(n+1) * 100 * time.Millisecond
		if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 {
			if seconds > 2 {
				seconds = 2
			}
			delay = time.Duration(seconds) * time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, errors.New("drive request cancelled")
		case <-timer.C:
		}
	}
	return nil, errors.New("drive request failed")
}
func decode(resp *http.Response, target any) error {
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(target); err != nil {
		return errors.New("invalid Drive response")
	}
	return nil
}
func (c *Client) Get(ctx context.Context, id string) (File, error) {
	resp, err := c.request(ctx, "GET", c.base+"/files/"+url.PathEscape(id), url.Values{"fields": {fields}}, nil, "", true)
	if err != nil {
		return File{}, err
	}
	var w wireFile
	if err = decode(resp, &w); err != nil {
		return File{}, err
	}
	if w.ID == "" {
		return File{}, errors.New("missing Drive identity")
	}
	return w.file(), nil
}
func (c *Client) List(ctx context.Context, query, token string) (Page, error) {
	resp, err := c.request(ctx, "GET", c.base+"/files", url.Values{"q": {query}, "pageToken": {token}, "pageSize": {strconv.Itoa(c.pageSize)}, "spaces": {"drive"}, "corpora": {"user"}, "fields": {"nextPageToken,incompleteSearch,files(" + fields + ")"}}, nil, "", true)
	if err != nil {
		return Page{}, err
	}
	var w struct {
		Files      []wireFile `json:"files"`
		Next       string     `json:"nextPageToken"`
		Incomplete bool       `json:"incompleteSearch"`
	}
	if err = decode(resp, &w); err != nil {
		return Page{}, err
	}
	p := Page{NextToken: w.Next, Incomplete: w.Incomplete}
	for _, f := range w.Files {
		if f.ID == "" {
			return Page{}, errors.New("missing Drive identity")
		}
		p.Files = append(p.Files, f.file())
	}
	return p, nil
}
func ListAll(ctx context.Context, api API, query string) ([]File, error) {
	var result []File
	token := ""
	seen := map[string]bool{}
	for {
		page, err := api.List(ctx, query, token)
		if err != nil {
			return nil, err
		}
		if page.Incomplete {
			return nil, errors.New("drive listing is incomplete")
		}
		result = append(result, page.Files...)
		if page.NextToken == "" {
			return result, nil
		}
		if seen[page.NextToken] || len(page.NextToken) > 4096 || strings.ContainsAny(page.NextToken, "\r\n\x00") {
			return nil, errors.New("invalid Drive pagination")
		}
		seen[page.NextToken] = true
		token = page.NextToken
	}
}
func ChildrenQuery(id string) string {
	return "'" + EscapeQuery(id) + "' in parents and trashed = false"
}
func EscapeQuery(value string) string {
	return strings.NewReplacer("\\", "\\\\", "'", "\\'").Replace(value)
}
