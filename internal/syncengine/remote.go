package syncengine

import (
	"context"
	"errors"

	"github.com/torfstack/grove/internal/drive"
)

type RemoteChangesResult struct {
	Cursor  string
	Changed bool
}

var ErrRemoteCheckCleanup = errors.New("remote check cleanup failed")

type remoteCheckFailure struct{ cause error }

func (e remoteCheckFailure) Error() string { return "remote change check failed" }
func (e remoteCheckFailure) Unwrap() error { return e.cause }

func closeRemoteCheck(close func() error, err *error) {
	if close == nil {
		*err = errors.Join(*err, ErrRemoteCheckCleanup)
		return
	}
	if closeErr := close(); closeErr != nil {
		*err = errors.Join(*err, ErrRemoteCheckCleanup, closeErr)
	}
}

func (s Service) CheckRemoteChanges(ctx context.Context, profileDir string, expected Binding, cursor string) (result RemoteChangesResult, err error) {
	result.Cursor = cursor
	defer func() {
		if err != nil {
			result.Cursor = cursor
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				err = errors.Join(err, context.Cause(ctx))
			}
			err = remoteCheckFailure{err}
		}
	}()
	opts, state, lock, err := existingProfile(ctx, profileDir)
	if err != nil {
		return result, err
	}
	defer closeRemoteCheck(lock.Close, &err)
	if state.Binding != expected {
		return result, errors.New("profile binding changed")
	}
	open := s.OpenAPI
	if open == nil {
		open = defaultOpenAPI
	}
	api, closeAPI, err := open(ctx, opts.TokenFile, "read-only")
	if err != nil {
		return result, err
	}
	defer closeRemoteCheck(closeAPI, &err)
	feed, ok := api.(drive.ChangesAPI)
	if !ok {
		return result, errors.New("remote changes API unavailable")
	}
	if cursor == "" {
		token, readErr := feed.StartChanges(ctx)
		if readErr != nil {
			return result, readErr
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if token == "" || len(token) > 64*1024 {
			return result, errors.New("invalid remote changes cursor")
		}
		return RemoteChangesResult{Cursor: token, Changed: true}, nil
	}
	if len(cursor) > 64*1024 {
		return result, errors.New("invalid remote changes cursor")
	}
	seen := map[string]bool{cursor: true}
	current := cursor
	for pageNumber := 0; pageNumber < 100; pageNumber++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		page, readErr := feed.ListChanges(ctx, current)
		result.Changed = result.Changed || page.Changed
		if readErr != nil {
			return result, readErr
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if len(page.NextToken) > 64*1024 || len(page.StartToken) > 64*1024 {
			return result, errors.New("invalid remote changes cursor")
		}
		if page.NextToken != "" {
			if page.StartToken != "" || seen[page.NextToken] {
				return result, errors.New("invalid remote changes pagination")
			}
			seen[page.NextToken] = true
			current = page.NextToken
			continue
		}
		if page.StartToken == "" {
			return result, errors.New("remote changes cursor missing")
		}
		result.Cursor = page.StartToken
		return result, nil
	}
	return result, errors.New("remote changes pagination limit exceeded")
}
