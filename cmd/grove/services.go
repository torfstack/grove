package main

import (
	"context"
	"errors"
	"github.com/torfstack/grove/internal/auth"
	"github.com/torfstack/grove/internal/cli"
	"github.com/torfstack/grove/internal/drive"
	"github.com/torfstack/grove/internal/fixture"
	"github.com/torfstack/grove/internal/privatefs"
	"github.com/torfstack/grove/internal/syncengine"
	"path/filepath"
)

func services(authenticate cli.AuthenticateFunc, driveOptions drive.ClientOptions) cli.Services {
	return cli.Services{Authenticate: authenticate, Seed: func(ctx context.Context, opts cli.FixtureOptions) (path string, err error) {
		v, err := fixture.LoadManifest(opts.Manifest)
		if err != nil {
			return "", err
		}
		err = withFixture(ctx, opts, "read-write", driveOptions, func(api drive.API) error { _, err := fixture.Seed(ctx, api, v, opts.RunDir); return err })
		if err != nil {
			return "", err
		}
		return filepath.Join(opts.RunDir, "run.json"), nil
	}, Inspect: func(ctx context.Context, opts cli.FixtureOptions) (report fixture.Report, err error) {
		err = withFixture(ctx, opts, "read-only", driveOptions, func(api drive.API) error {
			var err error
			report, err = fixture.Inspect(ctx, api, opts.RunDir)
			return err
		})
		return report, err
	}, Verify: func(ctx context.Context, opts cli.FixtureOptions) (fixture.Report, error) {
		v, err := fixture.LoadManifest(opts.Manifest)
		if err != nil {
			return fixture.Report{}, err
		}
		return fixture.VerifyLocal(ctx, v, opts.LocalDir)
	}, Cleanup: func(ctx context.Context, opts cli.FixtureOptions) error {
		return withFixture(ctx, opts, "read-write", driveOptions, func(api drive.API) error { return fixture.Cleanup(ctx, api, opts.RunDir) })
	}, Sync: (syncengine.Service{OpenAPI: func(ctx context.Context, token string) (drive.API, func() error, error) {
		session, err := auth.OpenSession(ctx, token, "read-only")
		if err != nil {
			return nil, nil, err
		}
		return drive.NewClient(session.HTTPClient, driveOptions), session.Close, nil
	}}).Run}
}
func withFixture(ctx context.Context, opts cli.FixtureOptions, access string, driveOptions drive.ClientOptions, work func(drive.API) error) (err error) {
	dir, err := privatefs.Canonical(opts.RunDir)
	if err != nil {
		return err
	}
	if err := privatefs.OutsideGit(dir); err != nil {
		return err
	}
	lock, err := privatefs.Acquire(dir + ".lock")
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := lock.Close(); err == nil && closeErr != nil {
			err = errors.New("cannot release fixture lock")
		}
	}()
	session, err := auth.OpenSession(ctx, opts.TokenFile, access)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := session.Close(); err == nil && closeErr != nil {
			err = errors.New("cannot close fixture authentication")
		}
	}()
	return work(drive.NewClient(session.HTTPClient, driveOptions))
}
