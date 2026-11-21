package core

import (
	"context"
	"io"

	"opf/internal/transfer"
)

// PackageFetcher abstracts the isolated environment used to resolve and
// download packages (Docker or WSL).
type PackageFetcher interface {
	// Prepare creates the isolated environment with the exact target
	// distro/version/architecture.
	Prepare(ctx context.Context, targetOS, targetVersion, arch string) error
	// Download resolves and downloads the package and its dependencies inside
	// the isolated environment, copying the artifacts to the host.
	Download(ctx context.Context, pkg string, includeOptional bool) ([]PackageArtifact, error)
	// Cleanup destroys the environment and its temporary directories.
	Cleanup(ctx context.Context) error
}

// MultiPackageFetcher is the preferred fetcher contract for sessions with
// multiple roots. Implementations resolve the union of dependencies and
// download everything in one isolated environment.
type MultiPackageFetcher interface {
	DownloadPackages(ctx context.Context, pkgs []string, includeOptional bool) ([]PackageArtifact, error)
}

// EventSink receives persisted events for secondary observability (Syslog).
// It must be best-effort and never block the SQLite source of truth.
type EventSink interface {
	Emit(ctx context.Context, event Evento) error
}

// EventSinkCloser flushes and closes an EventSink.
type EventSinkCloser interface {
	Close(ctx context.Context) error
}

// PipelineReporter receives progress, step and log events. It is optional and
// used by GUI/TUI; CLI can leave it nil.
type PipelineReporter interface {
	StepStarted(step int, name string)
	StepFinished(step int, name string)
	StepFailed(step int, name string, err error)
	Progress(stage string, current, total int64)
	Log(level, message string)
}

// ReporterSetter is implemented by fetchers/transfers that want to emit
// progress to the active reporter.
type ReporterSetter interface {
	SetReporter(reporter PipelineReporter)
}

// PackageInspector performs the remote pre-check (step 2).
type PackageInspector interface {
	InspectPackages(ctx context.Context, pkgs []string) ([]InstalledPackage, error)
}

type HostInfoCollector interface {
	CollectHostInfo(ctx context.Context) (HostInfo, error)
}

type InstalledPackage struct {
	Name    string
	Version string
	Arch    string
}

// PackageArtifact represents a downloaded and verified package file.
type PackageArtifact struct {
	Name         string
	Version      string
	Arch         string
	LocalPath    string
	IsDependency bool
	SHA256       string
	SHA256P1     string
	SHA256P2     string
	P3Verified   bool
	Installed    bool
}

// DependencyResolver is specific to a distro family and must execute its
// listing commands inside the isolated environment.
type DependencyResolver interface {
	ListDependencies(ctx context.Context, pkg string, includeOptional bool) ([]string, error)
	DownloadCommands(pkgs []string) [][]string
}

// CommandExecutor runs commands inside the isolated environment.
type CommandExecutor interface {
	Exec(ctx context.Context, cmd []string) (stdout string, err error)
}

// SessionBinder is implemented by fetchers that need the session ID to build
// the isolated working directory (/tmp/opf-<sessao-id>/).
type SessionBinder interface {
	BindSession(id int64) error
}

// TransferManager handles SSH/SFTP transfer and remote verification.
// The concrete implementation lives in internal/transfer.
type TransferManager = transfer.TransferManager

// SSHConfig groups SSH connection parameters, including jump hosts.
type SSHConfig = transfer.SSHConfig

// InstallExecutor builds and executes the remote installation command.
type InstallExecutor interface {
	InstallCommand(pkgs []PackageArtifact) (string, error)
	Execute(ctx context.Context, tm TransferManager, cmd string) error
}

// RemoteDirBinder is implemented by installers that need the remote session
// directory to build the installation command.
type RemoteDirBinder interface {
	SetRemoteDir(dir string) error
}

// CommandInputRunner is implemented by transfer managers that can send stdin
// to a remote command (used by sudo -S without exposing the password in argv).
type CommandInputRunner interface {
	RunCommandWithInput(ctx context.Context, cmd string, input []byte) (stdout string, err error)
}

// CommandStreamRunner streams remote command output to writers.
type CommandStreamRunner interface {
	RunCommandStream(ctx context.Context, cmd string, stdout, stderr io.Writer) error
}

// RemoteCleaner removes the remote session directory on cancellation/failure.
type RemoteCleaner interface {
	RemoveRemoteDir(ctx context.Context, dir string) error
}

// InstallConfirmation is the explicit user decision required before any
// remote installation command is executed.
type InstallConfirmation func(ctx context.Context, artifacts []PackageArtifact) (bool, error)
