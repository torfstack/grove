package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"golang.org/x/oauth2"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"
)

type OpenBrowser func(context.Context, string) error
type Flow struct {
	Endpoint    oauth2.Endpoint
	HTTPClient  *http.Client
	OpenBrowser OpenBrowser
	Output      io.Writer
}
type flowResult struct {
	token *oauth2.Token
	err   error
}

func (f Flow) Authorize(parent context.Context, client Client, scope string) (*oauth2.Token, error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("cannot start localhost authentication listener")
	}
	defer func() { _ = listener.Close() }()
	random := make([]byte, 32)
	if _, err = rand.Read(random); err != nil {
		return nil, errors.New("cannot generate authentication state")
	}
	state := base64.RawURLEncoding.EncodeToString(random)
	verifier := oauth2.GenerateVerifier()
	config := oauth2.Config{ClientID: client.ID, ClientSecret: client.Secret, Endpoint: f.Endpoint, RedirectURL: "http://" + listener.Addr().String() + "/callback", Scopes: []string{scope}}
	if f.HTTPClient != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, f.HTTPClient)
	}
	results := make(chan flowResult, 1)
	var consumed atomic.Bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if r.URL.Path != "/callback" {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		query, parseErr := url.ParseQuery(r.URL.RawQuery)
		if parseErr != nil || len(query["state"]) != 1 || subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid authentication callback", 400)
			return
		}
		codes, codePresent := query["code"]
		failures, errorPresent := query["error"]
		if codePresent == errorPresent || (codePresent && (len(codes) != 1 || codes[0] == "")) || (errorPresent && (len(failures) != 1 || failures[0] == "")) {
			http.Error(w, "Invalid authentication callback", 400)
			return
		}
		if !consumed.CompareAndSwap(false, true) {
			http.Error(w, "Authentication callback already received", http.StatusConflict)
			return
		}
		if errorPresent {
			http.Error(w, "Authorization was declined. Return to the terminal.", 400)
			results <- flowResult{err: errors.New("authorization with Google was declined")}
			return
		}
		token, exchangeErr := config.Exchange(ctx, codes[0], oauth2.VerifierOption(verifier))
		if exchangeErr != nil {
			http.Error(w, "Authorization failed. Return to the terminal.", 400)
			if ctx.Err() != nil {
				results <- flowResult{err: ctx.Err()}
			} else {
				results <- flowResult{err: errors.New("token exchange with Google failed; retry grove auth")}
			}
			return
		}
		if token.AccessToken == "" || token.RefreshToken == "" {
			http.Error(w, "Authorization did not provide complete credentials. Return to the terminal.", 400)
			results <- flowResult{err: errors.New("missing access or refresh token from Google; retry grove auth and grant consent")}
			return
		}
		_, _ = fmt.Fprintln(w, "Google authorization received. Return to the terminal to confirm token storage.")
		results <- flowResult{token: token}
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case results <- flowResult{err: errors.New("authentication listener failed")}:
			default:
			}
		}
	}()
	defer func() {
		if parent.Err() == nil {
			// Let the callback handler return and flush its completion response.
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
			defer shutdownCancel()
			_ = server.Shutdown(shutdownCtx)
		}
		_ = server.Close()
		<-serveDone
	}()
	authorizationURL := config.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent select_account"), oauth2.S256ChallengeOption(verifier))
	output := f.Output
	if output == nil {
		output = io.Discard
	}
	if _, err = fmt.Fprintf(output, "Open this URL in your browser to authorize Google Drive:\n%s\n", authorizationURL); err != nil {
		return nil, errors.New("cannot display authorization URL")
	}
	openerDone := make(chan struct{})
	go func() {
		defer close(openerDone)
		if f.OpenBrowser != nil {
			launchCtx, launchCancel := context.WithTimeout(ctx, 10*time.Second)
			defer launchCancel()
			_ = f.OpenBrowser(launchCtx, authorizationURL)
		}
	}()
	defer func() { cancel(); <-openerDone }()
	select {
	case result := <-results:
		return result.token, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
