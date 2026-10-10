package live

import (
	"bytes"
	"context"
	"fmt"
	"io"
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

func TestConditionalUploadConflictLive(t *testing.T) {
	cfg := testConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := os.MkdirAll(cfg.Runs, 0700); err != nil {
		t.Fatal("cannot create live runs directory")
	}
	base, err := os.MkdirTemp(cfg.Runs, "conditional-upload-")
	if err != nil {
		t.Fatal("cannot create private live run")
	}
	runDir := filepath.Join(base, "fixture")
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Failed run retained at %s", filepath.Join(runDir, "run.json"))
		}
	})
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
		t.Fatal("cannot seed conditional upload fixture")
	}
	remoteID := ""
	for _, object := range run.Objects {
		if object.LogicalID == "hello.txt" {
			remoteID = object.RemoteID
			break
		}
	}
	if remoteID == "" {
		t.Fatal("conditional upload fixture file is missing")
	}
	hasETag, err := fileETagAvailable(ctx, cfg.Token, remoteID)
	if err != nil {
		t.Fatal("cannot inspect fixture response headers")
	}
	if !hasETag {
		t.Log("Drive v3 metadata response omitted HTTP ETag")
	}
	baselineVersion, err := fixtureFileVersion(ctx, cfg.Token, runDir, remoteID)
	if err != nil {
		t.Fatal("cannot read fixture version")
	}
	matchingVersion := fmt.Sprintf("%q", baselineVersion)
	probe := []byte("version condition probe")
	status, err := tryResumableUpdate(ctx, cfg.Token, remoteID, matchingVersion, probe)
	if err != nil {
		t.Fatal("matching-version upload request failed")
	}
	if status < 200 || status >= 300 {
		cleanupConditionalFixture(t, ctx, cfg.Token, runDir)
		t.Logf("Drive rejected the version-based If-Match control with HTTP %d; conditional-write feasibility is not established", status)
		return
	}
	currentVersion, err := fixtureFileVersion(ctx, cfg.Token, runDir, remoteID)
	if err != nil {
		t.Fatal("cannot read post-control fixture version")
	}
	if currentVersion == baselineVersion {
		cleanupConditionalFixture(t, ctx, cfg.Token, runDir)
		t.Log("Drive version did not advance after media update; conditional-write feasibility is not established")
		return
	}
	competing := []byte("competing remote edit")
	if err = fixtureAPI(ctx, cfg.Token, runDir, "read-write", func(api drive.API) error {
		mutation, ok := api.(drive.MutationAPI)
		if !ok {
			return fmt.Errorf("fixture mutation unavailable")
		}
		_, updateErr := mutation.Update(ctx, remoteID, drive.Update{ContentChanged: true}, bytes.NewReader(competing))
		return updateErr
	}); err != nil {
		t.Fatal("cannot create competing fixture edit")
	}
	staleCondition := fmt.Sprintf("%q", currentVersion)
	status, err = tryResumableUpdate(ctx, cfg.Token, remoteID, staleCondition, []byte("stale local edit"))
	if err != nil {
		t.Fatal("conditional upload request failed")
	}
	var actual []byte
	if err = fixtureAPI(ctx, cfg.Token, runDir, "read-only", func(api drive.API) error {
		var downloadErr error
		var content bytes.Buffer
		downloadErr = api.Download(ctx, remoteID, &content)
		actual = content.Bytes()
		return downloadErr
	}); err != nil {
		t.Fatal("cannot independently read fixture content")
	}
	if !bytes.Equal(actual, competing) {
		if err = restoreFixtureContent(ctx, cfg.Token, runDir, remoteID, competing); err != nil {
			t.Fatal("stale write changed fixture content and restoration failed")
		}
		cleanupConditionalFixture(t, ctx, cfg.Token, runDir)
		t.Logf("Drive accepted a stale version condition with HTTP %d; competing content was restored and conditional-write feasibility is not established", status)
		return
	}
	if status != http.StatusPreconditionFailed {
		cleanupConditionalFixture(t, ctx, cfg.Token, runDir)
		t.Logf("stale version condition returned HTTP %d without changing content; conditional-write feasibility is not established", status)
		return
	}
	cleanupConditionalFixture(t, ctx, cfg.Token, runDir)
	t.Log("stale version condition returned HTTP 412; competing remote bytes were preserved")
}

func fixtureFileVersion(ctx context.Context, token, runDir, id string) (string, error) {
	var version string
	err := fixtureAPI(ctx, token, runDir, "read-only", func(api drive.API) error {
		file, err := api.Get(ctx, id)
		version = file.Version
		return err
	})
	if err != nil || version == "" {
		return "", fmt.Errorf("fixture version unavailable")
	}
	return version, nil
}

func fileETagAvailable(ctx context.Context, token, id string) (bool, error) {
	session, err := auth.OpenSession(ctx, token, "read-only")
	if err != nil {
		return false, err
	}
	defer func() { _ = session.Close() }()
	u := "https://www.googleapis.com/drive/v3/files/" + url.PathEscape(id) + "?fields=id,version"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	resp, err := session.HTTPClient.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("metadata request returned HTTP %d", resp.StatusCode)
	}
	return resp.Header.Get("ETag") != "", nil
}

func tryResumableUpdate(ctx context.Context, token, id, condition string, content []byte) (int, error) {
	session, err := auth.OpenSession(ctx, token, "read-write")
	if err != nil {
		return 0, err
	}
	defer func() { _ = session.Close() }()
	initURL := "https://www.googleapis.com/upload/drive/v3/files/" + url.PathEscape(id) + "?uploadType=resumable&fields=id,version"
	initReq, err := http.NewRequestWithContext(ctx, http.MethodPatch, initURL, nil)
	if err != nil {
		return 0, err
	}
	initReq.Header.Set("If-Match", condition)
	initReq.Header.Set("X-Upload-Content-Type", "application/octet-stream")
	initReq.Header.Set("X-Upload-Content-Length", fmt.Sprint(len(content)))
	initResp, err := session.HTTPClient.Do(initReq)
	if err != nil {
		return 0, err
	}
	defer func() { _ = initResp.Body.Close() }()
	if initResp.StatusCode == http.StatusPreconditionFailed {
		_ = initResp.Body.Close()
		return initResp.StatusCode, nil
	}
	if initResp.StatusCode < 200 || initResp.StatusCode >= 300 {
		_ = initResp.Body.Close()
		return initResp.StatusCode, nil
	}
	location := initResp.Header.Get("Location")
	_, _ = io.Copy(io.Discard, io.LimitReader(initResp.Body, 4096))
	_ = initResp.Body.Close()
	if location == "" {
		return initResp.StatusCode, fmt.Errorf("resumable initiation omitted session location")
	}
	putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, location, bytes.NewReader(content))
	if err != nil {
		return 0, err
	}
	putReq.Header.Set("If-Match", condition)
	putReq.Header.Set("Content-Type", "application/octet-stream")
	putReq.Header.Set("Content-Length", fmt.Sprint(len(content)))
	putResp, err := session.HTTPClient.Do(putReq)
	if err != nil {
		return 0, err
	}
	defer func() { _ = putResp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(putResp.Body, 4096))
	return putResp.StatusCode, nil
}

func cleanupConditionalFixture(t *testing.T, ctx context.Context, token, runDir string) {
	t.Helper()
	if err := fixtureAPI(ctx, token, runDir, "read-write", func(api drive.API) error {
		return fixture.Cleanup(ctx, api, runDir)
	}); err != nil {
		t.Fatalf("cannot clean up conditional upload fixture: %v", err)
	}
}

func restoreFixtureContent(ctx context.Context, token, runDir, id string, content []byte) error {
	return fixtureAPI(ctx, token, runDir, "read-write", func(api drive.API) error {
		mutation, ok := api.(drive.MutationAPI)
		if !ok {
			return fmt.Errorf("fixture mutation unavailable")
		}
		_, err := mutation.Update(ctx, id, drive.Update{ContentChanged: true}, bytes.NewReader(content))
		return err
	})
}
