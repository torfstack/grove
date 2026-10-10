package drive

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUpdateHTTP(t *testing.T) {
	for _, media := range []bool{false, true} {
		t.Run(map[bool]string{false: "metadata", true: "content"}[media], func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "PATCH" || r.URL.Query().Get("addParents") != "new" || r.URL.Query().Get("removeParents") != "old" {
					t.Error("wrong metadata patch")
				}
				if media {
					if r.URL.Path != "/upload/drive/v3/files/id" || r.URL.Query().Get("uploadType") != "multipart" {
						t.Error("wrong upload path")
					}
					_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
					reader := multipart.NewReader(r.Body, params["boundary"])
					part, err := reader.NextPart()
					if err != nil {
						t.Fatal(err)
					}
					metadata, _ := io.ReadAll(part)
					if !strings.Contains(string(metadata), `"name":"renamed"`) {
						t.Error("missing metadata")
					}
					part, err = reader.NextPart()
					if err != nil {
						t.Fatal(err)
					}
					b, _ := io.ReadAll(part)
					if string(b) != "new content" {
						t.Error("wrong bytes")
					}
				} else {
					if r.URL.Path != "/drive/v3/files/id" {
						t.Error("wrong metadata path")
					}
				}
				_, _ = io.WriteString(w, `{"id":"id","name":"renamed"}`)
			}))
			defer server.Close()
			client := NewClient(server.Client(), ClientOptions{BaseURL: server.URL + "/drive/v3"})
			var data io.Reader
			if media {
				data = strings.NewReader("new content")
			}
			got, err := client.Update(context.Background(), "id", Update{Name: "renamed", AddParent: "new", RemoveParent: "old", ContentChanged: media}, data)
			if err != nil || got.ID != "id" || calls != 1 {
				t.Fatal("update failed", err)
			}
		})
	}
}
func TestUpdateNoRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "private response", 500) }))
	defer server.Close()
	client := NewClient(server.Client(), ClientOptions{BaseURL: server.URL})
	_, err := client.Update(context.Background(), "id", Update{Name: "x"}, nil)
	if err == nil || strings.Contains(err.Error(), "private") || calls != 1 {
		t.Fatal("unsafe retry or error", err)
	}
}
