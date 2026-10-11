package live

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/torfstack/grove/internal/auth"
	"github.com/torfstack/grove/internal/drive"
	"github.com/torfstack/grove/internal/fixture"
)

func TestPinCurrentHeadRevisionLive(t *testing.T) {
	cfg := testConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := os.MkdirAll(cfg.Runs, 0700); err != nil {
		t.Fatal("cannot create live runs directory")
	}
	base, err := os.MkdirTemp(cfg.Runs, "pin-head-revision-")
	if err != nil {
		t.Fatal("cannot create private live run")
	}
	runDir := filepath.Join(base, "fixture")
	manifest, err := fixture.LoadManifest("../../testdata/fixtures/baseline/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var run fixture.Run
	if err = fixtureAPI(ctx, cfg.Token, runDir, "read-write", func(api drive.API) error {
		var seedErr error
		run, seedErr = fixture.Seed(ctx, api, manifest, runDir)
		return seedErr
	}); err != nil {
		t.Fatalf("cannot seed revision fixture: %v", err)
	}
	defer func() {
		if err := fixtureAPI(ctx, cfg.Token, runDir, "read-write", func(api drive.API) error {
			return fixture.Cleanup(ctx, api, runDir)
		}); err != nil {
			t.Error("cannot clean revision fixture")
		}
	}()
	fileID := ""
	for _, object := range run.Objects {
		if object.LogicalID == "hello.txt" {
			fileID = object.RemoteID
			break
		}
	}
	if fileID == "" {
		t.Fatal("revision fixture file is missing")
	}
	session, err := auth.OpenSession(ctx, cfg.Token, "read-write")
	if err != nil {
		t.Fatal("cannot open authorized Drive session")
	}
	defer func() { _ = session.Close() }()
	metadataURL := "https://www.googleapis.com/drive/v3/files/" + url.PathEscape(fileID) + "?fields=id,headRevisionId"
	metadataReq, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		t.Fatal("cannot create metadata request")
	}
	metadataResp, err := session.HTTPClient.Do(metadataReq)
	if err != nil {
		t.Fatal("cannot read head revision")
	}
	var metadata struct {
		HeadRevisionID string `json:"headRevisionId"`
	}
	decodeErr := json.NewDecoder(metadataResp.Body).Decode(&metadata)
	_ = metadataResp.Body.Close()
	if metadataResp.StatusCode != http.StatusOK || decodeErr != nil || metadata.HeadRevisionID == "" {
		t.Fatal("Drive did not return a head revision")
	}
	patchURL := "https://www.googleapis.com/drive/v3/files/" + url.PathEscape(fileID) + "/revisions/" + url.PathEscape(metadata.HeadRevisionID)
	body := bytes.NewBufferString(`{"keepForever":true}`)
	patchReq, err := http.NewRequestWithContext(ctx, http.MethodPatch, patchURL+"?fields=id,keepForever", body)
	if err != nil {
		t.Fatal("cannot create pin request")
	}
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, err := session.HTTPClient.Do(patchReq)
	if err != nil {
		t.Fatal("cannot pin head revision")
	}
	var result struct {
		KeepForever bool `json:"keepForever"`
	}
	decodeErr = json.NewDecoder(patchResp.Body).Decode(&result)
	_ = patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK || decodeErr != nil || !result.KeepForever {
		t.Fatalf("Drive rejected pinning the current head revision with HTTP %d", patchResp.StatusCode)
	}
	patchReq, err = http.NewRequestWithContext(ctx, http.MethodPatch, patchURL+"?fields=id,keepForever", bytes.NewBufferString(`{"keepForever":false}`))
	if err != nil {
		t.Fatal("cannot create unpin request")
	}
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, err = session.HTTPClient.Do(patchReq)
	if err != nil {
		t.Fatal("cannot unpin head revision")
	}
	_ = patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("Drive rejected unpinning the revision with HTTP %d", patchResp.StatusCode)
	}
	t.Log("current head revision can be pinned and unpinned")
}
