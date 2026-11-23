package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"opf/internal/core"
	"opf/internal/store"
)

func TestBuildPackagePreviewDetectsOnlyUpgrades(t *testing.T) {
	data, upgrades := buildPackagePreview(
		core.VersionReportDefault,
		[]string{"vim"},
		[]core.CandidatePackage{
			{Name: "vim", Version: "9.0"},
			{Name: "curl", Version: "8.0"},
			{Name: "libc6", Version: "2.36", IsDependency: true},
		},
		[]core.InstalledPackage{
			{Name: "vim", Version: "8.2"},
			{Name: "libc6", Version: "2.35"},
		},
	)
	if len(data.Diff) != 2 {
		t.Fatalf("diff = %+v, esperava vim e libc6", data.Diff)
	}
	if !upgrades["vim"] || upgrades["libc6"] || upgrades["curl"] {
		t.Fatalf("upgrades = %+v", upgrades)
	}
	if len(data.Inventory) != 2 {
		t.Fatalf("inventário = %+v", data.Inventory)
	}
}

func TestBuildPackagePreviewArchQualifier(t *testing.T) {
	data, upgrades := buildPackagePreview(
		core.VersionReportDefault,
		[]string{"libssl3:amd64"},
		[]core.CandidatePackage{{Name: "libssl3:amd64", Version: "3.0.2"}},
		[]core.InstalledPackage{{Name: "libssl3", Version: "3.0.1"}},
	)
	if len(data.Diff) != 1 || !upgrades["libssl3:amd64"] || !upgrades["libssl3"] {
		t.Fatalf("qualificador de arquitetura não normalizado: %+v / %+v", data.Diff, upgrades)
	}
}

func TestBuildPackagePreviewWithoutUpgrade(t *testing.T) {
	data, upgrades := buildPackagePreview(
		core.VersionReportAll,
		[]string{"curl"},
		[]core.CandidatePackage{{Name: "curl", Version: "8.0"}},
		[]core.InstalledPackage{{Name: "vim", Version: "9.0"}},
	)
	if len(data.Diff) != 0 || len(upgrades) != 0 {
		t.Fatalf("novo pacote não deve gerar diff: %+v / %+v", data.Diff, upgrades)
	}
	if data.Mode != core.VersionReportAll || len(data.Inventory) != 1 {
		t.Fatalf("inventário all inválido: %+v", data)
	}
}

type warmFakeFetcher struct {
	sourceDir    string
	prepared     bool
	prepareCalls int
	cleanupCalls int
	candidates   []core.CandidatePackage
}

func (f *warmFakeFetcher) BindSession(int64) error { return nil }
func (f *warmFakeFetcher) IsPrepared() bool        { return f.prepared }
func (f *warmFakeFetcher) Prepare(context.Context, string, string, string) error {
	f.prepareCalls++
	f.prepared = true
	return nil
}
func (f *warmFakeFetcher) ResolveCandidates(context.Context, []string, bool) ([]core.CandidatePackage, error) {
	return f.candidates, nil
}
func (f *warmFakeFetcher) Download(_ context.Context, pkg string, _ bool) ([]core.PackageArtifact, error) {
	p := filepath.Join(f.sourceDir, pkg+"_9.0_amd64.deb")
	if err := os.WriteFile(p, []byte("deb-"+pkg), 0o644); err != nil {
		return nil, err
	}
	return []core.PackageArtifact{{Name: pkg, Version: "9.0", Arch: "amd64", LocalPath: p}}, nil
}
func (f *warmFakeFetcher) Cleanup(context.Context) error {
	f.cleanupCalls++
	f.prepared = false
	return nil
}

type warmFakeTransfer struct {
	connected    bool
	connectCalls int
	closeCalls   int
}

func (t *warmFakeTransfer) IsConnected() bool { return t.connected }
func (t *warmFakeTransfer) Connect(context.Context, core.SSHConfig) error {
	t.connectCalls++
	t.connected = true
	return nil
}
func (t *warmFakeTransfer) Upload(context.Context, []string, string) error { return nil }
func (t *warmFakeTransfer) VerifyChecksums(context.Context, string, string) error {
	return nil
}
func (t *warmFakeTransfer) RunCommand(context.Context, string) (string, error) { return "", nil }
func (t *warmFakeTransfer) Close() error {
	if t.connected {
		t.closeCalls++
		t.connected = false
	}
	return nil
}

type warmFakeInspector struct {
	installed []core.InstalledPackage
}

func (i *warmFakeInspector) InspectPackages(context.Context, []string) ([]core.InstalledPackage, error) {
	return i.installed, nil
}

func newWarmPreviewService(t *testing.T) (*Service, *warmFakeFetcher, *warmFakeTransfer) {
	t.Helper()
	repo, err := store.Open(filepath.Join(t.TempDir(), "opf.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	f := &warmFakeFetcher{
		sourceDir:  t.TempDir(),
		candidates: []core.CandidatePackage{{Name: "vim", Version: "9.0"}},
	}
	tm := &warmFakeTransfer{}
	ins := &warmFakeInspector{installed: []core.InstalledPackage{{Name: "vim", Version: "8.2"}}}
	svc := &Service{
		cfg: Config{
			OutputBaseDir: t.TempDir(),
			TargetOS:      "ubuntu",
			TargetVersion: "22.04",
			TargetArch:    "amd64",
			Packages:      []string{"vim"},
			FetchMethod:   "docker",
			SSH:           core.SSHConfig{Host: "10.0.0.1", User: "opf"},
			VersionReport: core.VersionReportDefault,
		},
		repo:     repo,
		fetcher:  f,
		transfer: tm,
		inspect:  ins,
	}
	return svc, f, tm
}

func TestPreviewReusesWarmEnvironmentAndSSH(t *testing.T) {
	svc, f, tm := newWarmPreviewService(t)
	defer svc.Close()

	preview, err := svc.PreviewPackageDiff(context.Background())
	if err != nil {
		t.Fatalf("PreviewPackageDiff: %v", err)
	}
	if !f.prepared || !tm.connected {
		t.Fatalf("ambiente e SSH deveriam permanecer aquecidos: prepared=%v connected=%v", f.prepared, tm.connected)
	}
	if f.prepareCalls != 1 || tm.connectCalls != 1 {
		t.Fatalf("chamadas após preview: prepare=%d connect=%d", f.prepareCalls, tm.connectCalls)
	}

	result, err := svc.RunPreviewed(context.Background(), preview, nil)
	if err != nil {
		t.Fatalf("RunPreviewed: %v", err)
	}
	if result == nil || result.SessaoID != preview.SessionID {
		t.Fatalf("resultado inesperado: %+v", result)
	}
	if f.prepareCalls != 1 || tm.connectCalls != 1 {
		t.Fatalf("ambiente/SSH não foram reutilizados: prepare=%d connect=%d", f.prepareCalls, tm.connectCalls)
	}
	if f.cleanupCalls != 1 || tm.closeCalls != 1 {
		t.Fatalf("cleanup/close após pipeline: cleanup=%d close=%d", f.cleanupCalls, tm.closeCalls)
	}
}

func TestAbortPreviewCleansUpWarmResources(t *testing.T) {
	svc, f, tm := newWarmPreviewService(t)
	defer svc.Close()

	preview, err := svc.PreviewPackageDiff(context.Background())
	if err != nil {
		t.Fatalf("PreviewPackageDiff: %v", err)
	}
	if err := svc.AbortPreview(context.Background(), preview); err != nil {
		t.Fatalf("AbortPreview: %v", err)
	}
	if f.prepared || tm.connected || f.cleanupCalls != 1 || tm.closeCalls != 1 {
		t.Fatalf("AbortPreview não limpou recursos: prepared=%v connected=%v cleanup=%d close=%d",
			f.prepared, tm.connected, f.cleanupCalls, tm.closeCalls)
	}
}

func TestCloseCleansUpWarmPreviewResources(t *testing.T) {
	svc, f, tm := newWarmPreviewService(t)

	if _, err := svc.PreviewPackageDiff(context.Background()); err != nil {
		t.Fatalf("PreviewPackageDiff: %v", err)
	}
	if err := svc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if f.prepared || tm.connected || f.cleanupCalls != 1 || tm.closeCalls != 1 {
		t.Fatalf("Close não limpou recursos do preview: prepared=%v connected=%v cleanup=%d close=%d",
			f.prepared, tm.connected, f.cleanupCalls, tm.closeCalls)
	}
}
