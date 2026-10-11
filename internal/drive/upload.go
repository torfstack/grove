package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const uploadChunkSize int64 = 8 << 20

func (c *Client) GenerateIDs(ctx context.Context, count int) ([]string, error) {
	if count < 1 || count > 1000 {
		return nil, errors.New("invalid Drive ID count")
	}
	resp, err := c.request(ctx, http.MethodGet, c.base+"/files/generateIds", url.Values{
		"count": {strconv.Itoa(count)},
		"space": {"drive"},
	}, nil, "", true)
	if err != nil {
		return nil, err
	}
	var result struct {
		IDs []string `json:"ids"`
	}
	if err = decode(resp, &result); err != nil {
		return nil, err
	}
	if len(result.IDs) != count {
		return nil, errors.New("drive returned an invalid ID count")
	}
	seen := make(map[string]bool, count)
	for _, id := range result.IDs {
		if id == "" || seen[id] {
			return nil, errors.New("drive returned invalid IDs")
		}
		seen[id] = true
	}
	return result.IDs, nil
}

func (c *Client) CreateWithID(ctx context.Context, id string, input Create, content io.Reader, size int64) (File, error) {
	if id == "" || input.Name == "" || input.MIMEType == "" || size < 0 || (content == nil && size != 0) {
		return File{}, errors.New("invalid sync create")
	}
	metadata := map[string]any{"id": id, "name": input.Name, "mimeType": input.MIMEType, "appProperties": input.Properties}
	if input.ParentID != "" {
		metadata["parents"] = []string{input.ParentID}
	}
	if input.MIMEType == FolderMIME {
		if content != nil || size != 0 {
			return File{}, errors.New("folders cannot contain upload data")
		}
		data, err := json.Marshal(metadata)
		if err != nil {
			return File{}, errors.New("cannot encode sync create")
		}
		resp, err := c.request(ctx, http.MethodPost, c.base+"/files", url.Values{"fields": {fields}}, data, "application/json; charset=UTF-8", false)
		if err != nil {
			return File{}, err
		}
		var wire wireFile
		if err = decode(resp, &wire); err != nil || wire.ID != id {
			return File{}, errors.New("sync create outcome is uncertain")
		}
		return wire.file(), nil
	}
	if content == nil {
		return File{}, errors.New("file content is required")
	}
	return c.resumableMedia(ctx, http.MethodPost, id, input, metadata, content, size)
}

func (c *Client) UpdateContent(ctx context.Context, id string, input Update, content io.Reader, size int64) (File, error) {
	if id == "" || content == nil || !input.ContentChanged || size < 0 {
		return File{}, errors.New("invalid sync update")
	}
	metadata := map[string]any{}
	if input.Name != "" {
		metadata["name"] = input.Name
	}
	return c.resumableMedia(ctx, http.MethodPatch, id, Create{MIMEType: input.MIMEType}, metadata, content, size)
}

func (c *Client) resumableMedia(ctx context.Context, method, id string, input Create, metadata map[string]any, content io.Reader, size int64) (File, error) {
	body, err := json.Marshal(metadata)
	if err != nil {
		return File{}, errors.New("cannot encode sync upload metadata")
	}
	endpoint := c.uploadBase() + "/files"
	if method == http.MethodPatch {
		endpoint += "/" + url.PathEscape(id)
	}
	query := url.Values{"uploadType": {"resumable"}, "fields": {fields}}
	endpoint += "?" + query.Encode()
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return File{}, errors.New("cannot start sync upload")
	}
	contentType := input.MIMEType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	request.Header.Set("Content-Type", "application/json; charset=UTF-8")
	request.Header.Set("X-Upload-Content-Type", contentType)
	request.Header.Set("X-Upload-Content-Length", strconv.FormatInt(size, 10))
	response, err := c.http.Do(request)
	if err != nil {
		return File{}, errors.New("sync upload initiation failed")
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return File{}, errors.New("sync upload initiation rejected")
	}
	sessionURL := response.Header.Get("Location")
	_ = response.Body.Close()
	if !sameOrigin(endpoint, sessionURL) {
		return File{}, errors.New("sync upload returned an invalid session")
	}
	return c.sendUpload(ctx, sessionURL, id, content, size)
}

func (c *Client) uploadBase() string {
	if strings.Contains(c.base, "/drive/v3") {
		return strings.Replace(c.base, "/drive/v3", "/upload/drive/v3", 1)
	}
	return c.base
}

func sameOrigin(endpoint, location string) bool {
	base, baseErr := url.Parse(endpoint)
	target, targetErr := url.Parse(location)
	return baseErr == nil && targetErr == nil && base.Scheme != "" && base.Scheme == target.Scheme && base.Host != "" && base.Host == target.Host && target.User == nil && target.Fragment == ""
}

func (c *Client) sendUpload(ctx context.Context, sessionURL, id string, content io.Reader, size int64) (File, error) {
	if size == 0 {
		return c.sendChunk(ctx, sessionURL, id, content, 0, 0, 0, true)
	}
	for start := int64(0); start < size; {
		length := min(uploadChunkSize, size-start)
		end := start + length
		file, err := c.sendChunk(ctx, sessionURL, id, io.LimitReader(content, length), start, end, size, end == size)
		if err != nil {
			return File{}, err
		}
		if end == size {
			return file, nil
		}
		start = end
	}
	return File{}, errors.New("sync upload did not complete")
}

func (c *Client) sendChunk(ctx context.Context, sessionURL, id string, content io.Reader, start, end, total int64, final bool) (File, error) {
	length := end - start
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, sessionURL, content)
	if err != nil {
		return File{}, errors.New("cannot create sync upload request")
	}
	request.ContentLength = length
	request.Header.Set("Content-Type", "application/octet-stream")
	if length == 0 {
		request.Header.Set("Content-Range", "bytes */0")
	} else {
		request.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end-1, total))
	}
	response, err := c.http.Do(request)
	if err != nil {
		return File{}, errors.New("sync upload transfer failed")
	}
	defer func() { _ = response.Body.Close() }()
	if !final {
		if response.StatusCode != 308 || response.Header.Get("Range") != fmt.Sprintf("bytes=0-%d", end-1) {
			return File{}, errors.New("sync upload chunk was not confirmed")
		}
		return File{}, nil
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return File{}, errors.New("sync upload completion was not confirmed")
	}
	var wire wireFile
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&wire); err != nil || wire.ID != id {
		return File{}, errors.New("sync upload completion was uncertain")
	}
	return wire.file(), nil
}

func (c *Client) ListRevisions(ctx context.Context, id string) ([]Revision, error) {
	if id == "" {
		return nil, errors.New("file ID is required")
	}
	token := ""
	seenTokens := map[string]bool{}
	var revisions []Revision
	for range 10000 {
		query := url.Values{
			"fields":   {"nextPageToken,revisions(id,md5Checksum,modifiedTime,size,keepForever)"},
			"pageSize": {"100"},
		}
		if token != "" {
			query.Set("pageToken", token)
		}
		resp, err := c.request(ctx, http.MethodGet, c.base+"/files/"+url.PathEscape(id)+"/revisions", query, nil, "", true)
		if err != nil {
			return nil, err
		}
		var page struct {
			Next      string `json:"nextPageToken"`
			Revisions []struct {
				ID           string `json:"id"`
				MD5          string `json:"md5Checksum"`
				ModifiedTime string `json:"modifiedTime"`
				Size         *int64 `json:"size,string"`
				KeepForever  bool   `json:"keepForever"`
			} `json:"revisions"`
		}
		if err = decode(resp, &page); err != nil {
			return nil, err
		}
		for _, item := range page.Revisions {
			if item.ID == "" {
				return nil, errors.New("drive returned an invalid revision")
			}
			revision := Revision{ID: item.ID, MD5: item.MD5, ModifiedTime: item.ModifiedTime, KeepForever: item.KeepForever}
			if item.Size != nil {
				revision.Size, revision.HasSize = *item.Size, true
			}
			revisions = append(revisions, revision)
		}
		if page.Next == "" {
			return revisions, nil
		}
		if seenTokens[page.Next] {
			return nil, errors.New("drive repeated a revision page token")
		}
		seenTokens[page.Next] = true
		token = page.Next
	}
	return nil, errors.New("drive returned too many revision pages")
}

func (c *Client) SetRevisionKeepForever(ctx context.Context, id, revisionID string, keep bool) error {
	if id == "" || revisionID == "" {
		return errors.New("file and revision IDs are required")
	}
	data, err := json.Marshal(map[string]bool{"keepForever": keep})
	if err != nil {
		return errors.New("cannot encode revision update")
	}
	path := c.base + "/files/" + url.PathEscape(id) + "/revisions/" + url.PathEscape(revisionID)
	resp, err := c.request(ctx, http.MethodPatch, path, url.Values{"fields": {"id,keepForever"}}, data, "application/json; charset=UTF-8", false)
	if err != nil {
		return err
	}
	var result struct {
		ID          string `json:"id"`
		KeepForever bool   `json:"keepForever"`
	}
	if err = decode(resp, &result); err != nil || result.ID != revisionID || result.KeepForever != keep {
		return errors.New("drive revision state was not confirmed")
	}
	return nil
}

func (c *Client) DownloadRevision(ctx context.Context, id, revisionID string, dst io.Writer) error {
	if id == "" || revisionID == "" || dst == nil {
		return errors.New("file, revision, and destination are required")
	}
	resp, err := c.request(ctx, http.MethodGet, c.base+"/files/"+url.PathEscape(id)+"/revisions/"+url.PathEscape(revisionID), url.Values{"alt": {"media"}}, nil, "", true)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err = io.Copy(dst, resp.Body); err != nil {
		return errors.New("drive revision download failed")
	}
	return nil
}
