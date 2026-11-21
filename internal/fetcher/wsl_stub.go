//go:build !windows

package fetcher

import (
	"context"
	"errors"

	"opf/internal/core"
)

// WSLFetcher is a non-Windows stub; WSL is only available on Windows.
type WSLFetcher struct {
	options wslOptions
}

var _ core.PackageFetcher = (*WSLFetcher)(nil)
var _ core.SessionBinder = (*WSLFetcher)(nil)

func NewWSLFetcher(opts ...WSLFetcherOption) *WSLFetcher {
	return &WSLFetcher{options: applyWSLFetcherOptions(opts)}
}

func (f *WSLFetcher) BindSession(id int64) error {
	if id <= 0 {
		return errors.New("id de sessão inválido")
	}
	return nil
}

func (f *WSLFetcher) Prepare(context.Context, string, string, string) error {
	return errors.New("WSLFetcher disponível apenas no Windows")
}

func (f *WSLFetcher) Download(context.Context, string, bool) ([]core.PackageArtifact, error) {
	return nil, errors.New("WSLFetcher disponível apenas no Windows")
}

func (f *WSLFetcher) Cleanup(context.Context) error { return nil }
