package fetcher

import (
	"log/slog"

	"opf/internal/core"
)

type wslOptions struct {
	distro          string
	tempDir         string
	resolverFactory ResolverFactory
	logger          *slog.Logger
	repositories    []core.PackageRepository
}

type WSLFetcherOption func(*wslOptions)

func WithWSLDistro(name string) WSLFetcherOption {
	return func(o *wslOptions) { o.distro = name }
}

func WithWSLTempDir(dir string) WSLFetcherOption {
	return func(o *wslOptions) { o.tempDir = dir }
}

func WithWSLResolverFactory(factory ResolverFactory) WSLFetcherOption {
	return func(o *wslOptions) { o.resolverFactory = factory }
}

func WithWSLLogger(logger *slog.Logger) WSLFetcherOption {
	return func(o *wslOptions) { o.logger = logger }
}

func WithWSLRepositories(repos []core.PackageRepository) WSLFetcherOption {
	return func(o *wslOptions) { o.repositories = repos }
}

func applyWSLFetcherOptions(opts []WSLFetcherOption) wslOptions {
	options := wslOptions{logger: slog.Default()}
	for _, opt := range opts {
		opt(&options)
	}
	if options.logger == nil {
		options.logger = slog.Default()
	}
	return options
}

func defaultWSLDistro(targetOS, targetVersion string) string {
	switch targetOS {
	case "ubuntu":
		return "Ubuntu-" + targetVersion
	case "debian":
		return "Debian"
	default:
		return targetOS + "-" + targetVersion
	}
}
