package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"golang.org/x/oauth2"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type flowFixture struct {
	flow    Flow
	calls   atomic.Int32
	request url.Values
	output  bytes.Buffer
	server  *httptest.Server
}

func newFlowFixture(t *testing.T, tokenBody string, stall bool) *flowFixture {
	t.Helper()
	f := &flowFixture{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		digest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if f.request == nil || base64.RawURLEncoding.EncodeToString(digest[:]) != f.request.Get("code_challenge") || r.Form.Get("redirect_uri") != f.request.Get("redirect_uri") || r.Form.Get("code") != "CODE_SENTINEL" {
			t.Error("incorrect code exchange/PKCE")
			http.Error(w, "invalid", 400)
			return
		}
		if stall {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, tokenBody)
	}))
	t.Cleanup(f.server.Close)
	f.flow = Flow{Endpoint: oauth2.Endpoint{AuthURL: f.server.URL + "/auth", TokenURL: f.server.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}, HTTPClient: f.server.Client(), Output: &f.output}
	return f
}

func (f *flowFixture) callback(t *testing.T, query url.Values, path, method string) int {
	t.Helper()
	u, _ := url.Parse(f.request.Get("redirect_uri"))
	u.Path = path
	u.RawQuery = query.Encode()
	req, _ := http.NewRequest(method, u.String(), nil)
	c := http.Client{Timeout: time.Second}
	response, err := c.Do(req)
	if err != nil {
		t.Errorf("callback: %v", err)
		return 0
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(response.Body)
	if response.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Error("missing referrer policy")
	}
	if strings.Contains(string(body), "SENTINEL") {
		t.Error("reflected sensitive callback data")
	}
	return response.StatusCode
}

func (f *flowFixture) open(t *testing.T, fn func()) OpenBrowser {
	return func(ctx context.Context, raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			t.Error(err)
			return err
		}
		f.request = u.Query()
		fn()
		return nil
	}
}

const goodToken = `{"access_token":"ACCESS_SENTINEL","refresh_token":"REFRESH_SENTINEL","token_type":"Bearer","expires_in":3600}`

func TestFlowSuccessAndPKCE(t *testing.T) {
	f := newFlowFixture(t, goodToken, false)
	f.flow.OpenBrowser = f.open(t, func() {
		q := f.request
		if q.Get("scope") != "https://www.googleapis.com/auth/drive.readonly" || q.Get("access_type") != "offline" || q.Get("prompt") != "consent select_account" || q.Get("code_challenge_method") != "S256" || len(q.Get("state")) < 32 || q.Get("response_type") != "code" {
			t.Error("incorrect authorization request")
		}
		f.callback(t, url.Values{"state": {q.Get("state")}, "code": {"CODE_SENTINEL"}}, "/callback", "GET")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	token, err := f.flow.Authorize(ctx, Client{ID: "id", Secret: "secret"}, "https://www.googleapis.com/auth/drive.readonly")
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "ACCESS_SENTINEL" || token.RefreshToken != "REFRESH_SENTINEL" || f.calls.Load() != 1 {
		t.Fatal("incorrect token or exchanges")
	}
	for _, s := range []string{"ACCESS_SENTINEL", "REFRESH_SENTINEL", "CODE_SENTINEL", "secret"} {
		if strings.Contains(f.output.String(), s) {
			t.Fatal("sensitive output")
		}
	}
}

func TestInvalidCallbacksLeaveLoginPending(t *testing.T) {
	for _, kind := range []string{"wrong-state", "path", "post", "missing-state", "duplicate-state", "missing-code", "duplicate-code", "duplicate-error", "code-and-error", "malformed-query"} {
		t.Run(kind, func(t *testing.T) {
			f := newFlowFixture(t, goodToken, false)
			f.flow.OpenBrowser = f.open(t, func() {
				state := f.request.Get("state")
				q := url.Values{"state": {state}, "code": {"CODE_SENTINEL"}}
				path, method := "/callback", "GET"
				switch kind {
				case "wrong-state":
					q.Set("state", "bad")
				case "path":
					path = "/favicon.ico"
				case "post":
					method = "POST"
				case "missing-state":
					q.Del("state")
				case "duplicate-state":
					q.Add("state", state)
				case "missing-code":
					q.Del("code")
				case "duplicate-code":
					q.Add("code", "other")
				case "duplicate-error":
					q.Del("code")
					q["error"] = []string{"denied", "denied"}
				case "code-and-error":
					q.Set("error", "denied")
				}
				if kind == "malformed-query" {
					u := f.request.Get("redirect_uri") + "?state=" + state + "&code=CODE_SENTINEL&bad=%zz"
					resp, err := http.Get(u)
					if err != nil {
						t.Error(err)
					} else {
						_ = resp.Body.Close()
						if resp.StatusCode < 400 {
							t.Error("accepted malformed query")
						}
					}
				} else if status := f.callback(t, q, path, method); status < 400 {
					t.Error("invalid callback accepted")
				}
				if f.calls.Load() != 0 {
					t.Error("invalid request exchanged code")
				}
				f.callback(t, url.Values{"state": {state}, "code": {"CODE_SENTINEL"}}, "/callback", "GET")
			})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if _, err := f.flow.Authorize(ctx, Client{ID: "id", Secret: "secret"}, "scope"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestConcurrentCallbacksExchangeOnce(t *testing.T) {
	f := newFlowFixture(t, goodToken, false)
	f.flow.OpenBrowser = f.open(t, func() {
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				f.callback(t, url.Values{"state": {f.request.Get("state")}, "code": {"CODE_SENTINEL"}}, "/callback", "GET")
			}()
		}
		wg.Wait()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := f.flow.Authorize(ctx, Client{ID: "id", Secret: "secret"}, "scope"); err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 1 {
		t.Fatalf("exchanges=%d", f.calls.Load())
	}
}

func TestFlowDenialAndExchangeErrors(t *testing.T) {
	for _, kind := range []string{"denied", "missing-refresh", "missing-access", "provider-error"} {
		t.Run(kind, func(t *testing.T) {
			body := goodToken
			switch kind {
			case "missing-refresh":
				body = `{"access_token":"ACCESS_SENTINEL"}`
			case "missing-access":
				body = `{"refresh_token":"REFRESH_SENTINEL"}`
			case "provider-error":
				body = `{"error":"invalid_grant","error_description":"SECRET_SENTINEL"}`
			}
			f := newFlowFixture(t, body, false)
			f.flow.OpenBrowser = f.open(t, func() {
				q := url.Values{"state": {f.request.Get("state")}}
				if kind == "denied" {
					q.Set("error", "SECRET_SENTINEL")
				} else {
					q.Set("code", "CODE_SENTINEL")
				}
				f.callback(t, q, "/callback", "GET")
			})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, err := f.flow.Authorize(ctx, Client{ID: "id", Secret: "secret"}, "scope")
			if err == nil || strings.Contains(err.Error(), "SENTINEL") {
				t.Fatalf("unsafe/missing error: %v", err)
			}
		})
	}
}

func TestFlowCancellationClosesListener(t *testing.T) {
	for _, kind := range []string{"no-callback", "stalled-exchange", "cancel", "browser-failure"} {
		t.Run(kind, func(t *testing.T) {
			f := newFlowFixture(t, goodToken, kind == "stalled-exchange")
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			f.flow.OpenBrowser = f.open(t, func() {
				if kind == "cancel" {
					cancel()
				}
				if kind == "stalled-exchange" {
					u := f.request.Get("redirect_uri") + "?" + url.Values{"state": {f.request.Get("state")}, "code": {"CODE_SENTINEL"}}.Encode()
					req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
					resp, _ := http.DefaultClient.Do(req)
					if resp != nil {
						_ = resp.Body.Close()
					}
				}
			})
			if kind == "browser-failure" {
				f.flow.OpenBrowser = func(ctx context.Context, s string) error {
					u, _ := url.Parse(s)
					f.request = u.Query()
					return fmt.Errorf("SECRET_SENTINEL")
				}
			}
			_, err := f.flow.Authorize(ctx, Client{ID: "id", Secret: "secret"}, "scope")
			if err == nil {
				t.Fatal("missing cancellation")
			}
			if kind == "stalled-exchange" && f.calls.Load() != 1 {
				t.Fatal("did not reach token exchange")
			}
			u := f.request.Get("redirect_uri")
			c := http.Client{Timeout: time.Second}
			resp, err := c.Get(u)
			if resp != nil {
				_ = resp.Body.Close()
			}
			if err == nil {
				t.Fatal("listener still open")
			}
			if strings.Contains(f.output.String(), "SECRET_SENTINEL") {
				t.Fatal("browser error leaked")
			}
		})
	}
}

func decodeRecord(t *testing.T, data []byte) Record {
	t.Helper()
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCallbackResponseSurvivesFlowCompletion(t *testing.T) {
	// The launcher exits before the browser callback; closing early can truncate its response.
	for range 100 {
		f := newFlowFixture(t, goodToken, false)
		urls := make(chan string, 1)
		f.flow.OpenBrowser = func(ctx context.Context, raw string) error {
			u, _ := url.Parse(raw)
			f.request = u.Query()
			urls <- f.request.Get("redirect_uri") + "?" + url.Values{"state": {f.request.Get("state")}, "code": {"CODE_SENTINEL"}}.Encode()
			return nil
		}
		responseDone := make(chan error, 1)
		go func() {
			raw := <-urls
			client := http.Client{Timeout: 2 * time.Second}
			resp, err := client.Get(raw)
			if err != nil {
				responseDone <- err
				return
			}
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			if err == nil && !strings.Contains(string(body), "Return to the terminal") {
				err = fmt.Errorf("missing completion message")
			}
			responseDone <- err
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := f.flow.Authorize(ctx, Client{ID: "id", Secret: "secret"}, "scope")
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if err = <-responseDone; err != nil {
			t.Fatalf("completion response interrupted: %v", err)
		}
		f.server.Close()
	}
}
