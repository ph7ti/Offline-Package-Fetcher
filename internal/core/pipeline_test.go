package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opf/internal/transfer"
)

type fakeRepo struct {
	sessoes     map[int64]*Sessao
	pacotes     []Pacote
	eventos     []Evento
	etapas      map[int64]map[int]EtapaSessao
	solicitados map[int64][]PacoteSolicitado
	nextID      int64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		sessoes:     make(map[int64]*Sessao),
		etapas:      make(map[int64]map[int]EtapaSessao),
		solicitados: make(map[int64][]PacoteSolicitado),
	}
}

func (r *fakeRepo) CreateSessao(_ context.Context, s *Sessao) error {
	if err := s.Validate(); err != nil {
		return err
	}
	r.nextID++
	s.ID = r.nextID
	copySessao := *s
	r.sessoes[s.ID] = &copySessao
	return nil
}

func (r *fakeRepo) ObterSessao(_ context.Context, id int64) (*Sessao, error) {
	s, ok := r.sessoes[id]
	if !ok {
		return nil, errors.New("sessão não encontrada")
	}
	copySessao := *s
	return &copySessao, nil
}

func (r *fakeRepo) FinalizarSessao(_ context.Context, id int64, status SessaoStatus, at time.Time) error {
	s, ok := r.sessoes[id]
	if !ok {
		return errors.New("sessão não encontrada")
	}
	s.Status = status
	s.FinalizadaEm = &at
	return nil
}

func (r *fakeRepo) AdicionarPacote(_ context.Context, p *Pacote) error {
	if len(r.pacotes) > 0 {
		p.ID = int64(len(r.pacotes) + 1)
	} else {
		p.ID = 1
	}
	r.pacotes = append(r.pacotes, *p)
	return nil
}

func (r *fakeRepo) AtualizarPacotesStatus(_ context.Context, sessaoID int64, de, para PacoteStatus) error {
	for i := range r.pacotes {
		if r.pacotes[i].SessaoID == sessaoID && r.pacotes[i].Status == de {
			r.pacotes[i].Status = para
		}
	}
	return nil
}

func (r *fakeRepo) AdicionarEvento(_ context.Context, e *Evento) error {
	e.ID = int64(len(r.eventos) + 1)
	r.eventos = append(r.eventos, *e)
	return nil
}

func (r *fakeRepo) ListarPacotes(context.Context, int64) ([]Pacote, error) {
	return append([]Pacote(nil), r.pacotes...), nil
}

func (r *fakeRepo) ListarEventos(context.Context, int64) ([]Evento, error) {
	return append([]Evento(nil), r.eventos...), nil
}

func (r *fakeRepo) ReabrirSessao(_ context.Context, id int64) error {
	s, ok := r.sessoes[id]
	if !ok {
		return errors.New("sessão não encontrada")
	}
	s.Status = SessaoEmAndamento
	s.FinalizadaEm = nil
	return nil
}

func (r *fakeRepo) AtualizarSessaoExecucao(_ context.Context, id int64, outputDir, remoteDir, imagem, wslDistro, ambienteID, configJSON string) error {
	s, ok := r.sessoes[id]
	if !ok {
		return errors.New("sessão não encontrada")
	}
	s.OutputDir, s.RemoteDir, s.ImagemDocker, s.WSLDistro, s.AmbienteID, s.ConfigJSON = outputDir, remoteDir, imagem, wslDistro, ambienteID, configJSON
	return nil
}

func (r *fakeRepo) AtualizarEtapaAtual(_ context.Context, id int64, etapa int) error {
	if s, ok := r.sessoes[id]; ok {
		s.EtapaAtual = etapa
	}
	return nil
}

func (r *fakeRepo) RegistrarEtapa(_ context.Context, e *EtapaSessao) error {
	if r.etapas[e.SessaoID] == nil {
		r.etapas[e.SessaoID] = make(map[int]EtapaSessao)
	}
	copyEtapa := *e
	r.etapas[e.SessaoID][e.Etapa] = copyEtapa
	return nil
}

func (r *fakeRepo) ListarEtapas(_ context.Context, sessaoID int64) ([]EtapaSessao, error) {
	m := r.etapas[sessaoID]
	out := make([]EtapaSessao, 0, len(m))
	for i := 1; i <= TotalEtapas; i++ {
		if e, ok := m[i]; ok {
			out = append(out, e)
		}
	}
	return out, nil
}

func (r *fakeRepo) AdicionarPacoteSolicitado(_ context.Context, p *PacoteSolicitado) error {
	p.ID = int64(len(r.solicitados[p.SessaoID]) + 1)
	r.solicitados[p.SessaoID] = append(r.solicitados[p.SessaoID], *p)
	return nil
}

func (r *fakeRepo) ListarPacotesSolicitados(_ context.Context, sessaoID int64) ([]PacoteSolicitado, error) {
	return append([]PacoteSolicitado(nil), r.solicitados[sessaoID]...), nil
}

func (r *fakeRepo) AtualizarPacoteVerificacoes(_ context.Context, pacoteID int64, shaP1, shaP2 string, p3, instalado *bool) error {
	for i := range r.pacotes {
		if r.pacotes[i].ID != pacoteID {
			continue
		}
		if shaP1 != "" {
			r.pacotes[i].SHA256P1 = shaP1
		}
		if shaP2 != "" {
			r.pacotes[i].SHA256P2 = shaP2
		}
		if p3 != nil {
			r.pacotes[i].P3Verificado = *p3
		}
		if instalado != nil {
			r.pacotes[i].InstaladoVerificado = *instalado
		}
	}
	return nil
}

func (r *fakeRepo) AtualizarPacoteVersaoAnterior(_ context.Context, pacoteID int64, versao string) error {
	for i := range r.pacotes {
		if r.pacotes[i].ID == pacoteID {
			r.pacotes[i].VersaoAnterior = versao
		}
	}
	return nil
}

func (r *fakeRepo) AtualizarHostInfo(_ context.Context, sessaoID int64, infoJSON string) error {
	if s, ok := r.sessoes[sessaoID]; ok {
		s.HostInfoJSON = infoJSON
	}
	return nil
}

func (r *fakeRepo) ListarSessoes(_ context.Context, somenteNaoArquivadas bool) ([]SessaoResumo, error) {
	out := make([]SessaoResumo, 0, len(r.sessoes))
	for _, s := range r.sessoes {
		if somenteNaoArquivadas && s.Arquivada {
			continue
		}
		out = append(out, SessaoResumo{Sessao: *s, TotalPacotes: len(r.pacotes)})
	}
	return out, nil
}

func (r *fakeRepo) ArquivarSessao(_ context.Context, id int64, arquivada bool) error {
	if s, ok := r.sessoes[id]; ok {
		s.Arquivada = arquivada
	}
	return nil
}

type fakeFetcher struct {
	sourceDir   string
	prepareErr  error
	downloadErr error
	cleanupErr  error
	artifacts   []PackageArtifact
	prepareCall int
	downloads   int
	cleanupCall int
	boundID     int64
	prepared    bool
	lastPkg     string
}

func (f *fakeFetcher) BindSession(id int64) error {
	f.boundID = id
	return nil
}

func (f *fakeFetcher) Prepare(context.Context, string, string, string) error {
	f.prepareCall++
	if f.prepareErr != nil {
		return f.prepareErr
	}
	f.prepared = true
	return nil
}

func (f *fakeFetcher) Download(_ context.Context, pkg string, _ bool) ([]PackageArtifact, error) {
	f.downloads++
	f.lastPkg = pkg
	if f.downloadErr != nil {
		return nil, f.downloadErr
	}
	artifacts := make([]PackageArtifact, 0, len(f.artifacts))
	for _, artifact := range f.artifacts {
		src := filepath.Join(f.sourceDir, artifact.Name+".deb")
		if err := os.WriteFile(src, []byte("conteudo-"+artifact.Name), 0o644); err != nil {
			return nil, err
		}
		artifact.LocalPath = src
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

func (f *fakeFetcher) Cleanup(context.Context) error {
	f.cleanupCall++
	f.prepared = false
	return f.cleanupErr
}

type fakeTransfer struct {
	connectErr  error
	uploadErr   error
	verifyErr   error
	closeErr    error
	connected   bool
	connectCall int
	uploadCall  int
	verifyCall  int
	closeCall   int
	files       []string
	remoteDir   string
	manifest    string
	sshConfig   SSHConfig
	removedDirs []string
	progress    []string
}

func (t *fakeTransfer) Connect(_ context.Context, cfg SSHConfig) error {
	t.connectCall++
	if t.connectErr != nil {
		return t.connectErr
	}
	t.connected = true
	t.sshConfig = cfg
	return nil
}

func (t *fakeTransfer) Upload(_ context.Context, files []string, remoteDir string) error {
	t.uploadCall++
	t.files = append([]string(nil), files...)
	t.remoteDir = remoteDir
	return t.uploadErr
}

func (t *fakeTransfer) RemoveRemoteDir(_ context.Context, dir string) error {
	t.removedDirs = append(t.removedDirs, dir)
	return nil
}

func (t *fakeTransfer) SetProgress(fn transfer.ProgressFunc) {}

func (t *fakeTransfer) VerifyChecksums(_ context.Context, manifestPath, remoteDir string) error {
	t.verifyCall++
	t.manifest = manifestPath
	t.remoteDir = remoteDir
	return t.verifyErr
}

func (t *fakeTransfer) RunCommand(context.Context, string) (string, error) {
	return "", nil
}

func (t *fakeTransfer) Close() error {
	t.closeCall++
	t.connected = false
	return t.closeErr
}

type fakeEventSink struct {
	events []Evento
}

func (s *fakeEventSink) Emit(_ context.Context, event Evento) error {
	s.events = append(s.events, event)
	return nil
}

type fakeInspector struct {
	installed []InstalledPackage
	hostInfo  HostInfo
	err       error
	called    int
}

func (i *fakeInspector) CollectHostInfo(context.Context) (HostInfo, error) {
	return i.hostInfo, nil
}

func (i *fakeInspector) InspectPackages(context.Context, []string) ([]InstalledPackage, error) {
	i.called++
	return i.installed, i.err
}

type multiFakeFetcher struct {
	fakeFetcher
	rootsCalled []string
}

func (f *multiFakeFetcher) DownloadPackages(ctx context.Context, roots []string, includeOptional bool) ([]PackageArtifact, error) {
	f.rootsCalled = append([]string(nil), roots...)
	if len(roots) == 0 {
		return nil, errors.New("sem roots")
	}
	return f.fakeFetcher.Download(ctx, roots[0], includeOptional)
}

type fakeReporter struct {
	started  []int
	finished []int
	failed   []int
	progress []string
}

func (r *fakeReporter) StepStarted(step int, _ string)         { r.started = append(r.started, step) }
func (r *fakeReporter) StepFinished(step int, _ string)        { r.finished = append(r.finished, step) }
func (r *fakeReporter) StepFailed(step int, _ string, _ error) { r.failed = append(r.failed, step) }
func (r *fakeReporter) Progress(stage string, current, total int64) {
	r.progress = append(r.progress, fmt.Sprintf("%s:%d/%d", stage, current, total))
}
func (r *fakeReporter) Log(string, string) {}

type fakeInstaller struct {
	buildErr   error
	execErr    error
	setErr     error
	remoteDirs []string
	commands   []string
	executed   []string
	receivedTM TransferManager
}

func (i *fakeInstaller) SetRemoteDir(dir string) error {
	i.remoteDirs = append(i.remoteDirs, dir)
	return i.setErr
}

func (i *fakeInstaller) InstallCommand(pkgs []PackageArtifact) (string, error) {
	if i.buildErr != nil {
		return "", i.buildErr
	}
	if len(pkgs) == 0 {
		return "", errors.New("nenhum pacote")
	}
	i.commands = append(i.commands, "apt-install-fake")
	return "apt-install-fake", nil
}

func (i *fakeInstaller) Execute(_ context.Context, tm TransferManager, cmd string) error {
	i.executed = append(i.executed, cmd)
	i.receivedTM = tm
	return i.execErr
}

func newTestSessao() *Sessao {
	return &Sessao{
		TargetOS:        "ubuntu",
		TargetVersion:   "22.04",
		TargetArch:      "amd64",
		FetchMethod:     "docker",
		IncludeOptional: false,
	}
}

func TestExecutarPipelineSucesso(t *testing.T) {
	base := t.TempDir()
	repo := newFakeRepo()
	fetcher := &fakeFetcher{
		sourceDir: t.TempDir(),
		artifacts: []PackageArtifact{
			{Name: "vim", Version: "9.0", Arch: "amd64", IsDependency: false},
			{Name: "libc6", Version: "2.35", Arch: "amd64", IsDependency: true},
		},
	}
	sessao := newTestSessao()

	result, err := ExecutarPipeline(context.Background(), PipelineConfig{
		Repo:          repo,
		Fetcher:       fetcher,
		Pacote:        "vim",
		OutputBaseDir: base,
	}, sessao)
	if err != nil {
		t.Fatalf("ExecutarPipeline: %v", err)
	}
	if result == nil {
		t.Fatal("resultado nulo")
	}
	if fetcher.prepareCall != 1 || fetcher.downloads != 1 || fetcher.cleanupCall != 1 {
		t.Fatalf("chamadas: prepare=%d download=%d cleanup=%d",
			fetcher.prepareCall, fetcher.downloads, fetcher.cleanupCall)
	}
	if fetcher.boundID != sessao.ID {
		t.Fatalf("sessão vinculada = %d, esperava %d", fetcher.boundID, sessao.ID)
	}
	if result.OutputDir != filepath.Join(base, "sessao-1") {
		t.Fatalf("output dir = %q", result.OutputDir)
	}
	if result.CaminhoManifesto != filepath.Join(result.OutputDir, transfer.NomeManifesto) {
		t.Fatalf("manifesto = %q", result.CaminhoManifesto)
	}

	manifest, err := transfer.VerificarManifesto(result.CaminhoManifesto, result.OutputDir)
	if err != nil {
		t.Fatalf("VerificarManifesto: %v", err)
	}
	if len(manifest.Arquivos) != 2 || len(result.Artifacts) != 2 {
		t.Fatalf("entradas do manifesto = %d, artefatos = %d", len(manifest.Arquivos), len(result.Artifacts))
	}
	for _, artifact := range result.Artifacts {
		if len(artifact.SHA256) != 64 {
			t.Fatalf("SHA-256 não preenchido para %q: %q", artifact.Name, artifact.SHA256)
		}
		if _, err := os.Stat(artifact.LocalPath); err != nil {
			t.Fatalf("artefato final ausente: %v", err)
		}
		if strings.HasPrefix(artifact.LocalPath, fetcher.sourceDir) {
			t.Fatalf("artefato permaneceu no diretório temporário: %s", artifact.LocalPath)
		}
	}

	stored, err := repo.ObterSessao(context.Background(), sessao.ID)
	if err != nil {
		t.Fatalf("ObterSessao: %v", err)
	}
	if stored.Status != SessaoConcluida || stored.FinalizadaEm == nil {
		t.Fatalf("sessão não finalizada corretamente: %+v", stored)
	}
	if len(repo.pacotes) != 2 {
		t.Fatalf("pacotes registrados = %d", len(repo.pacotes))
	}
	for _, pacote := range repo.pacotes {
		if pacote.Status != PacoteBaixado || pacote.SessaoID != sessao.ID || pacote.SHA256 == "" {
			t.Fatalf("pacote inválido no repositório: %+v", pacote)
		}
	}
	if len(repo.eventos) < 3 {
		t.Fatalf("eventos registrados = %d", len(repo.eventos))
	}
}

func TestExecutarPipelineFalhaDownloadFazCleanup(t *testing.T) {
	repo := newFakeRepo()
	fetcher := &fakeFetcher{
		sourceDir:   t.TempDir(),
		downloadErr: errors.New("falha simulada no download"),
	}
	sessao := newTestSessao()

	result, err := ExecutarPipeline(context.Background(), PipelineConfig{
		Repo:          repo,
		Fetcher:       fetcher,
		Pacote:        "vim",
		OutputBaseDir: t.TempDir(),
	}, sessao)
	if err == nil || !strings.Contains(err.Error(), "falha simulada") {
		t.Fatalf("esperava erro de download: %v", err)
	}
	if result != nil {
		t.Fatal("resultado deveria ser nulo em falha")
	}
	if fetcher.cleanupCall != 1 {
		t.Fatalf("cleanup chamado %d vezes", fetcher.cleanupCall)
	}
	stored, _ := repo.ObterSessao(context.Background(), sessao.ID)
	if stored.Status != SessaoFalhou {
		t.Fatalf("status = %q", stored.Status)
	}
	if len(repo.pacotes) != 0 {
		t.Fatalf("nenhum pacote deveria ser registrado em falha, obteve %d", len(repo.pacotes))
	}
}

func TestExecutarPipelineFalhaPrepareNaoFazCleanup(t *testing.T) {
	repo := newFakeRepo()
	fetcher := &fakeFetcher{
		sourceDir:  t.TempDir(),
		prepareErr: errors.New("docker indisponível"),
	}
	sessao := newTestSessao()

	_, err := ExecutarPipeline(context.Background(), PipelineConfig{
		Repo:          repo,
		Fetcher:       fetcher,
		Pacote:        "vim",
		OutputBaseDir: t.TempDir(),
	}, sessao)
	if err == nil || !strings.Contains(err.Error(), "docker indisponível") {
		t.Fatalf("esperava erro de prepare: %v", err)
	}
	if fetcher.cleanupCall != 0 {
		t.Fatalf("cleanup não deveria ser chamado sem ambiente preparado: %d", fetcher.cleanupCall)
	}
	stored, _ := repo.ObterSessao(context.Background(), sessao.ID)
	if stored.Status != SessaoFalhou {
		t.Fatalf("status = %q", stored.Status)
	}
}

func TestExecutarPipelineFalhaCleanup(t *testing.T) {
	repo := newFakeRepo()
	fetcher := &fakeFetcher{
		sourceDir: t.TempDir(),
		artifacts: []PackageArtifact{
			{Name: "vim", Version: "9.0", Arch: "amd64"},
		},
		cleanupErr: errors.New("falha simulada no cleanup"),
	}
	sessao := newTestSessao()

	_, err := ExecutarPipeline(context.Background(), PipelineConfig{
		Repo:          repo,
		Fetcher:       fetcher,
		Pacote:        "vim",
		OutputBaseDir: t.TempDir(),
	}, sessao)
	if err == nil || !strings.Contains(err.Error(), "cleanup") {
		t.Fatalf("esperava erro de cleanup: %v", err)
	}
	stored, _ := repo.ObterSessao(context.Background(), sessao.ID)
	if stored.Status != SessaoFalhou {
		t.Fatalf("status = %q", stored.Status)
	}
}

func TestExecutarPipelineComTransferencia(t *testing.T) {
	base := t.TempDir()
	repo := newFakeRepo()
	fetcher := &fakeFetcher{
		sourceDir: t.TempDir(),
		artifacts: []PackageArtifact{
			{Name: "vim", Version: "9.0", Arch: "amd64", IsDependency: false},
			{Name: "libc6", Version: "2.35", Arch: "amd64", IsDependency: true},
		},
	}
	transferManager := &fakeTransfer{}
	sessao := newTestSessao()

	result, err := ExecutarPipeline(context.Background(), PipelineConfig{
		Repo:          repo,
		Fetcher:       fetcher,
		Pacote:        "vim",
		OutputBaseDir: base,
		Transfer:      transferManager,
		SSH:           SSHConfig{Host: "destino", Port: 22, User: "opf"},
		RemoteDir:     "/tmp/opf-destino",
	}, sessao)
	if err != nil {
		t.Fatalf("ExecutarPipeline: %v", err)
	}
	if result.RemoteDir != "/tmp/opf-destino" {
		t.Fatalf("remote dir = %q", result.RemoteDir)
	}
	if transferManager.connectCall != 1 || transferManager.uploadCall != 1 || transferManager.verifyCall != 1 {
		t.Fatalf("chamadas: connect=%d upload=%d verify=%d",
			transferManager.connectCall, transferManager.uploadCall, transferManager.verifyCall)
	}
	if transferManager.closeCall != 1 {
		t.Fatalf("Close chamado %d vezes", transferManager.closeCall)
	}
	if len(transferManager.files) != 3 {
		t.Fatalf("arquivos enviados = %d, esperava 2 artefatos + manifesto", len(transferManager.files))
	}
	if transferManager.files[2] != result.CaminhoManifesto {
		t.Fatalf("manifesto não enviado por último: %v", transferManager.files)
	}
	if transferManager.manifest != path.Join("/tmp/opf-destino", transfer.NomeManifesto) {
		t.Fatalf("manifesto remoto = %q", transferManager.manifest)
	}
	if transferManager.remoteDir != "/tmp/opf-destino" {
		t.Fatalf("remote dir de verificação = %q", transferManager.remoteDir)
	}
	if transferManager.sshConfig.Host != "destino" {
		t.Fatalf("config SSH não repassada: %+v", transferManager.sshConfig)
	}

	for _, pacote := range repo.pacotes {
		if pacote.Status != PacoteEnviado {
			t.Fatalf("pacote %q com status %q, esperava enviado", pacote.Nome, pacote.Status)
		}
	}
	stored, _ := repo.ObterSessao(context.Background(), sessao.ID)
	if stored.Status != SessaoConcluida {
		t.Fatalf("status da sessão = %q", stored.Status)
	}
	hasTransferEvent := false
	for _, evento := range repo.eventos {
		if evento.Tipo == EventoTransferencia {
			hasTransferEvent = true
		}
	}
	if !hasTransferEvent {
		t.Fatal("nenhum evento de transferência registrado")
	}
}

func TestExecutarPipelineRemoteDirPadrao(t *testing.T) {
	repo := newFakeRepo()
	fetcher := &fakeFetcher{
		sourceDir: t.TempDir(),
		artifacts: []PackageArtifact{{Name: "vim", Version: "9.0", Arch: "amd64"}},
	}
	transferManager := &fakeTransfer{closeErr: errors.New("falha simulada no close")}
	sessao := newTestSessao()

	result, err := ExecutarPipeline(context.Background(), PipelineConfig{
		Repo:          repo,
		Fetcher:       fetcher,
		Pacote:        "vim",
		OutputBaseDir: t.TempDir(),
		Transfer:      transferManager,
		SSH:           SSHConfig{Host: "destino", User: "opf"},
	}, sessao)
	if err != nil {
		t.Fatalf("falha no Close não deve abortar a sessão: %v", err)
	}
	if result.RemoteDir != "/tmp/opf-sessao-1" {
		t.Fatalf("remote dir padrão = %q", result.RemoteDir)
	}
	if transferManager.closeCall != 1 {
		t.Fatalf("Close chamado %d vezes", transferManager.closeCall)
	}
}

func TestExecutarPipelineFalhaTransferencia(t *testing.T) {
	tests := []struct {
		name            string
		transferManager *fakeTransfer
		wantClose       int
		wantStatus      PacoteStatus
	}{
		{
			name:            "connect",
			transferManager: &fakeTransfer{connectErr: errors.New("connect falhou")},
			wantClose:       0,
			wantStatus:      PacoteBaixado,
		},
		{
			name:            "upload",
			transferManager: &fakeTransfer{uploadErr: errors.New("upload falhou")},
			wantClose:       1,
			wantStatus:      PacoteBaixado,
		},
		{
			name:            "verify",
			transferManager: &fakeTransfer{verifyErr: errors.New("verify falhou")},
			wantClose:       1,
			wantStatus:      PacoteBaixado,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeRepo()
			fetcher := &fakeFetcher{
				sourceDir: t.TempDir(),
				artifacts: []PackageArtifact{{Name: "vim", Version: "9.0", Arch: "amd64"}},
			}
			sessao := newTestSessao()

			_, err := ExecutarPipeline(context.Background(), PipelineConfig{
				Repo:          repo,
				Fetcher:       fetcher,
				Pacote:        "vim",
				OutputBaseDir: t.TempDir(),
				Transfer:      tt.transferManager,
				SSH:           SSHConfig{Host: "destino", User: "opf"},
				RemoteDir:     "/tmp/opf-destino",
			}, sessao)
			if err == nil {
				t.Fatal("esperava erro na transferência")
			}
			if tt.transferManager.closeCall != tt.wantClose {
				t.Fatalf("Close chamado %d vezes, esperava %d",
					tt.transferManager.closeCall, tt.wantClose)
			}
			wantCleanup := 1
			if strings.Contains(tt.name, "connect") {
				wantCleanup = 0
			}
			if fetcher.cleanupCall != wantCleanup {
				t.Fatalf("cleanup chamado %d vezes, esperava %d", fetcher.cleanupCall, wantCleanup)
			}
			stored, _ := repo.ObterSessao(context.Background(), sessao.ID)
			if stored.Status != SessaoFalhou {
				t.Fatalf("status da sessão = %q", stored.Status)
			}
			for _, pacote := range repo.pacotes {
				if pacote.Status != tt.wantStatus {
					t.Fatalf("status do pacote = %q, esperava %q", pacote.Status, tt.wantStatus)
				}
			}
		})
	}
}

func TestExecutarPipelineComInstalacao(t *testing.T) {
	repo := newFakeRepo()
	fetcher := &fakeFetcher{
		sourceDir: t.TempDir(),
		artifacts: []PackageArtifact{
			{Name: "vim", Version: "9.0", Arch: "amd64"},
			{Name: "libc6", Version: "2.35", Arch: "amd64", IsDependency: true},
		},
	}
	transferManager := &fakeTransfer{}
	aptInstaller := &fakeInstaller{}
	confirmed := false
	sessao := newTestSessao()

	result, err := ExecutarPipeline(context.Background(), PipelineConfig{
		Repo:          repo,
		Fetcher:       fetcher,
		Pacote:        "vim",
		OutputBaseDir: t.TempDir(),
		Transfer:      transferManager,
		SSH:           SSHConfig{Host: "destino", User: "root"},
		RemoteDir:     "/tmp/opf-install",
		Installer:     aptInstaller,
		ConfirmInstall: func(_ context.Context, artifacts []PackageArtifact) (bool, error) {
			confirmed = true
			if len(artifacts) != 2 {
				t.Errorf("artefatos na confirmação = %d", len(artifacts))
			}
			return true, nil
		},
	}, sessao)
	if err != nil {
		t.Fatalf("ExecutarPipeline: %v", err)
	}
	if !confirmed {
		t.Fatal("confirmação não foi solicitada")
	}
	if !result.Installed || result.InstallDeclined {
		t.Fatalf("resultado de instalação: %+v", result)
	}
	if len(aptInstaller.remoteDirs) != 1 || aptInstaller.remoteDirs[0] != "/tmp/opf-install" {
		t.Fatalf("remote dirs do instalador: %v", aptInstaller.remoteDirs)
	}
	if len(aptInstaller.executed) != 1 || aptInstaller.executed[0] != "apt-install-fake" {
		t.Fatalf("comandos executados: %v", aptInstaller.executed)
	}
	if aptInstaller.receivedTM != transferManager {
		t.Fatal("instalador não recebeu o TransferManager da sessão")
	}
	for _, pacote := range repo.pacotes {
		if pacote.Status != PacoteInstalado {
			t.Fatalf("status do pacote %q = %q", pacote.Nome, pacote.Status)
		}
	}
	stored, _ := repo.ObterSessao(context.Background(), sessao.ID)
	if stored.Status != SessaoConcluida {
		t.Fatalf("status da sessão = %q", stored.Status)
	}
	hasInstallEvent := false
	for _, evento := range repo.eventos {
		if evento.Tipo == EventoInstalacao {
			hasInstallEvent = true
		}
	}
	if !hasInstallEvent {
		t.Fatal("nenhum evento de instalação registrado")
	}
}

func TestExecutarPipelineInstalacaoRecusada(t *testing.T) {
	repo := newFakeRepo()
	fetcher := &fakeFetcher{
		sourceDir: t.TempDir(),
		artifacts: []PackageArtifact{{Name: "vim", Version: "9.0", Arch: "amd64"}},
	}
	transferManager := &fakeTransfer{}
	aptInstaller := &fakeInstaller{}
	sessao := newTestSessao()

	result, err := ExecutarPipeline(context.Background(), PipelineConfig{
		Repo:          repo,
		Fetcher:       fetcher,
		Pacote:        "vim",
		OutputBaseDir: t.TempDir(),
		Transfer:      transferManager,
		SSH:           SSHConfig{Host: "destino", User: "root"},
		Installer:     aptInstaller,
		ConfirmInstall: func(context.Context, []PackageArtifact) (bool, error) {
			return false, nil
		},
	}, sessao)
	if err != nil {
		t.Fatalf("ExecutarPipeline: %v", err)
	}
	if result.Installed || !result.InstallDeclined {
		t.Fatalf("resultado de instalação: %+v", result)
	}
	if len(aptInstaller.executed) != 0 {
		t.Fatalf("nenhum comando deveria ser executado: %v", aptInstaller.executed)
	}
	for _, pacote := range repo.pacotes {
		if pacote.Status != PacoteEnviadoNaoInstalado {
			t.Fatalf("status do pacote %q = %q", pacote.Nome, pacote.Status)
		}
	}
	stored, _ := repo.ObterSessao(context.Background(), sessao.ID)
	if stored.Status != SessaoConcluida {
		t.Fatalf("status da sessão = %q", stored.Status)
	}
}

func TestExecutarPipelineFalhasDeInstalacao(t *testing.T) {
	tests := []struct {
		name          string
		confirmErr    error
		confirmResult bool
		installer     *fakeInstaller
		wantStatus    PacoteStatus
	}{
		{
			name:       "confirmação com erro",
			confirmErr: errors.New("confirmação falhou"),
			installer:  &fakeInstaller{},
			wantStatus: PacoteEnviado,
		},
		{
			name:          "comando de instalação inválido",
			confirmResult: true,
			installer:     &fakeInstaller{buildErr: errors.New("build falhou")},
			wantStatus:    PacoteEnviado,
		},
		{
			name:          "binder falhou",
			confirmResult: true,
			installer:     &fakeInstaller{setErr: errors.New("binder falhou")},
			wantStatus:    PacoteEnviado,
		},
		{
			name:          "execução falhou",
			confirmResult: true,
			installer:     &fakeInstaller{execErr: errors.New("dpkg falhou")},
			wantStatus:    PacoteFalhou,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeRepo()
			fetcher := &fakeFetcher{
				sourceDir: t.TempDir(),
				artifacts: []PackageArtifact{{Name: "vim", Version: "9.0", Arch: "amd64"}},
			}
			sessao := newTestSessao()
			_, err := ExecutarPipeline(context.Background(), PipelineConfig{
				Repo:          repo,
				Fetcher:       fetcher,
				Pacote:        "vim",
				OutputBaseDir: t.TempDir(),
				Transfer:      &fakeTransfer{},
				SSH:           SSHConfig{Host: "destino", User: "root"},
				Installer:     tt.installer,
				ConfirmInstall: func(context.Context, []PackageArtifact) (bool, error) {
					return tt.confirmResult, tt.confirmErr
				},
			}, sessao)
			if err == nil {
				t.Fatal("esperava erro na instalação")
			}
			stored, _ := repo.ObterSessao(context.Background(), sessao.ID)
			if stored.Status != SessaoFalhou {
				t.Fatalf("status da sessão = %q", stored.Status)
			}
			for _, pacote := range repo.pacotes {
				if pacote.Status != tt.wantStatus {
					t.Fatalf("status do pacote = %q, esperava %q", pacote.Status, tt.wantStatus)
				}
			}
		})
	}
}

func TestExecutarPipelineInstalacaoValidacoes(t *testing.T) {
	sessao := newTestSessao()
	if _, err := ExecutarPipeline(context.Background(), PipelineConfig{
		Repo:      newFakeRepo(),
		Fetcher:   &fakeFetcher{sourceDir: t.TempDir()},
		Pacote:    "vim",
		Installer: &fakeInstaller{},
	}, sessao); err == nil {
		t.Fatal("instalação sem transferência deveria falhar")
	}
	if _, err := ExecutarPipeline(context.Background(), PipelineConfig{
		Repo:      newFakeRepo(),
		Fetcher:   &fakeFetcher{sourceDir: t.TempDir()},
		Pacote:    "vim",
		Transfer:  &fakeTransfer{},
		Installer: &fakeInstaller{},
	}, sessao); err == nil {
		t.Fatal("instalação sem confirmação deveria falhar")
	}
}

func TestExecutarPipelineEventSink(t *testing.T) {
	repo := newFakeRepo()
	fetcher := &fakeFetcher{sourceDir: t.TempDir(), artifacts: []PackageArtifact{{Name: "vim", Version: "9.0", Arch: "amd64"}}}
	sink := &fakeEventSink{}
	if _, err := ExecutarPipeline(context.Background(), PipelineConfig{
		Repo: repo, Fetcher: fetcher, Pacote: "vim", OutputBaseDir: t.TempDir(), EventSink: sink,
	}, newTestSessao()); err != nil {
		t.Fatalf("ExecutarPipeline: %v", err)
	}
	if len(sink.events) == 0 {
		t.Fatal("nenhum evento enviado ao sink")
	}
	found := false
	for _, event := range sink.events {
		if strings.Contains(event.Mensagem, "sessão") || event.Tipo == EventoInfo {
			found = true
		}
	}
	if !found {
		t.Fatalf("eventos inesperados: %+v", sink.events)
	}
}

func TestExecutarPipelineMultiPacotes(t *testing.T) {
	repo := newFakeRepo()
	fetcher := &multiFakeFetcher{fakeFetcher: fakeFetcher{
		sourceDir: t.TempDir(),
		artifacts: []PackageArtifact{
			{Name: "vim", Version: "9.0", Arch: "amd64"},
			{Name: "curl", Version: "8.0", Arch: "amd64"},
		},
	}}
	reporter := &fakeReporter{}
	result, err := ExecutarPipeline(context.Background(), PipelineConfig{
		Repo: repo, Fetcher: fetcher, Packages: []string{"vim", "curl"},
		OutputBaseDir: t.TempDir(), Reporter: reporter,
	}, newTestSessao())
	if err != nil {
		t.Fatalf("ExecutarPipeline: %v", err)
	}
	if strings.Join(fetcher.rootsCalled, ",") != "vim,curl" {
		t.Fatalf("roots chamados = %v", fetcher.rootsCalled)
	}
	if len(result.Artifacts) != 2 {
		t.Fatalf("artefatos = %d", len(result.Artifacts))
	}
	if len(reporter.started) == 0 || reporter.started[0] != 1 {
		t.Fatalf("reporter não recebeu etapas: %+v", reporter)
	}
	for _, step := range []int{1, 3, 4, 5, 6, 7, 12} {
		found := false
		for _, got := range reporter.finished {
			if got == step {
				found = true
			}
		}
		if !found {
			t.Fatalf("etapa %d não finalizada no reporter: %+v", step, reporter.finished)
		}
	}
}

func TestExecutarPipelinePreCheck(t *testing.T) {
	repo := newFakeRepo()
	fetcher := &fakeFetcher{
		sourceDir: t.TempDir(),
		artifacts: []PackageArtifact{{Name: "curl", Version: "8.0", Arch: "amd64"}},
	}
	inspector := &fakeInspector{installed: []InstalledPackage{{Name: "vim", Version: "9.0", Arch: "amd64"}}}
	result, err := ExecutarPipeline(context.Background(), PipelineConfig{
		Repo: repo, Fetcher: fetcher, Packages: []string{"vim", "curl"},
		OutputBaseDir: t.TempDir(), Transfer: &fakeTransfer{}, SSH: SSHConfig{Host: "host", User: "root"},
		Inspector: inspector,
	}, newTestSessao())
	if err != nil {
		t.Fatalf("ExecutarPipeline: %v", err)
	}
	if inspector.called < 1 {
		t.Fatalf("pré-check não chamado")
	}
	if fetcher.lastPkg != "curl" {
		t.Fatalf("pacote baixado = %q, esperava curl", fetcher.lastPkg)
	}
	hasJaInstalado := false
	for _, pacote := range repo.pacotes {
		if pacote.Nome == "vim" && pacote.Status == PacoteJaInstalado {
			hasJaInstalado = true
		}
	}
	if !hasJaInstalado {
		t.Fatalf("vim não registrado como ja_instalado: %+v", repo.pacotes)
	}
	if len(result.Artifacts) != 1 {
		t.Fatalf("artefatos = %d", len(result.Artifacts))
	}
}

func TestExecutarPipelineTodosJaInstalados(t *testing.T) {
	repo := newFakeRepo()
	fetcher := &fakeFetcher{sourceDir: t.TempDir(), artifacts: []PackageArtifact{{Name: "vim", Version: "9.0", Arch: "amd64"}}}
	inspector := &fakeInspector{installed: []InstalledPackage{{Name: "vim", Version: "9.0", Arch: "amd64"}}}
	result, err := ExecutarPipeline(context.Background(), PipelineConfig{
		Repo: repo, Fetcher: fetcher, Packages: []string{"vim"},
		OutputBaseDir: t.TempDir(), Transfer: &fakeTransfer{},
		SSH: SSHConfig{Host: "host", User: "root"}, Inspector: inspector,
	}, newTestSessao())
	if err != nil {
		t.Fatalf("ExecutarPipeline: %v", err)
	}
	if !result.AllAlreadyInstalled {
		t.Fatalf("resultado = %+v", result)
	}
	if fetcher.prepareCall != 0 || fetcher.downloads != 0 {
		t.Fatalf("não deveria preparar/baixar: %d/%d", fetcher.prepareCall, fetcher.downloads)
	}
	if len(repo.pacotes) != 1 || repo.pacotes[0].Status != PacoteJaInstalado {
		t.Fatalf("pacotes = %+v", repo.pacotes)
	}
}

func TestExecutarPipelineForceReinstall(t *testing.T) {
	repo := newFakeRepo()
	fetcher := &fakeFetcher{sourceDir: t.TempDir(), artifacts: []PackageArtifact{{Name: "vim", Version: "9.0", Arch: "amd64"}}}
	inspector := &fakeInspector{installed: []InstalledPackage{{Name: "vim", Version: "9.0", Arch: "amd64"}}}
	result, err := ExecutarPipeline(context.Background(), PipelineConfig{
		Repo: repo, Fetcher: fetcher, Packages: []string{"vim"}, ForceReinstall: true,
		OutputBaseDir: t.TempDir(), Transfer: &fakeTransfer{},
		SSH: SSHConfig{Host: "host", User: "root"}, Inspector: inspector,
	}, newTestSessao())
	if err != nil {
		t.Fatalf("ExecutarPipeline: %v", err)
	}
	if len(result.Artifacts) != 1 || fetcher.downloads != 1 {
		t.Fatalf("force reinstall não baixou: %+v downloads=%d", result.Artifacts, fetcher.downloads)
	}
}

func TestExecutarPipelineHostInfoEVersaoAnterior(t *testing.T) {
	repo := newFakeRepo()
	fetcher := &fakeFetcher{sourceDir: t.TempDir(), artifacts: []PackageArtifact{{Name: "vim", Version: "9.0", Arch: "amd64"}}}
	inspector := &fakeInspector{
		installed: []InstalledPackage{{Name: "vim", Version: "8.0", Arch: "amd64"}},
		hostInfo:  HostInfo{Kernel: "6.8.0", Uptime: "1d 2h 3m", CPU: "4 cores", Memoria: "8 GB", Disco: "100 GB"},
	}
	if _, err := ExecutarPipeline(context.Background(), PipelineConfig{
		Repo: repo, Fetcher: fetcher, Pacote: "vim", OutputBaseDir: t.TempDir(),
		Transfer: &fakeTransfer{}, SSH: SSHConfig{Host: "host", User: "opf"},
		Inspector: inspector, ForceReinstall: true,
	}, newTestSessao()); err != nil {
		t.Fatalf("ExecutarPipeline: %v", err)
	}
	stored, _ := repo.ObterSessao(context.Background(), 1)
	if stored.HostInfoJSON == "" || !strings.Contains(stored.HostInfoJSON, "6.8.0") {
		t.Fatalf("host info não persistida: %q", stored.HostInfoJSON)
	}
	if len(repo.pacotes) != 1 || repo.pacotes[0].VersaoAnterior != "8.0" {
		t.Fatalf("versão anterior não registrada: %+v", repo.pacotes)
	}
}

func TestExecutarPipelineAbortada(t *testing.T) {
	repo := newFakeRepo()
	fetcher := &fakeFetcher{sourceDir: t.TempDir(), downloadErr: context.Canceled}
	transferManager := &fakeTransfer{}
	_, err := ExecutarPipeline(context.Background(), PipelineConfig{
		Repo: repo, Fetcher: fetcher, Pacote: "vim",
		OutputBaseDir: t.TempDir(), Transfer: transferManager,
		SSH: SSHConfig{Host: "host", User: "root"}, RemoteDir: "/tmp/opf-sessao-1",
	}, newTestSessao())
	if err == nil {
		t.Fatal("esperava erro de cancelamento")
	}
	stored, _ := repo.ObterSessao(context.Background(), 1)
	if stored.Status != SessaoAbortada {
		t.Fatalf("status = %q, esperava abortada", stored.Status)
	}
	if fetcher.cleanupCall != 1 {
		t.Fatalf("cleanup = %d", fetcher.cleanupCall)
	}
	if len(transferManager.removedDirs) != 1 || transferManager.removedDirs[0] != "/tmp/opf-sessao-1" {
		t.Fatalf("cleanup remoto = %v", transferManager.removedDirs)
	}
}

func TestPlanejarRetomada(t *testing.T) {
	repo := newFakeRepo()
	sessao := newTestSessao()
	if err := repo.CreateSessao(context.Background(), sessao); err != nil {
		t.Fatalf("CreateSessao: %v", err)
	}
	outDir := t.TempDir()
	artifactPath := filepath.Join(outDir, "vim.deb")
	if err := os.WriteFile(artifactPath, []byte("vim"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	manifest, err := transfer.GerarManifesto(outDir, []string{artifactPath})
	if err != nil {
		t.Fatalf("GerarManifesto: %v", err)
	}
	sessao.OutputDir = outDir
	if err := repo.AtualizarSessaoExecucao(context.Background(), sessao.ID, outDir, "/tmp/opf-sessao-1", "", "", "", ""); err != nil {
		t.Fatalf("AtualizarSessaoExecucao: %v", err)
	}
	for step := 1; step <= 7; step++ {
		_ = repo.RegistrarEtapa(context.Background(), &EtapaSessao{SessaoID: sessao.ID, Etapa: step, Status: EtapaConcluida})
	}
	start, err := PlanejarRetomada(context.Background(), repo, sessao.ID)
	if err != nil || start != 8 {
		t.Fatalf("start = %d, err = %v, esperava 8", start, err)
	}
	_ = repo.RegistrarEtapa(context.Background(), &EtapaSessao{SessaoID: sessao.ID, Etapa: 8, Status: EtapaConcluida})
	start, _ = PlanejarRetomada(context.Background(), repo, sessao.ID)
	if start != 9 {
		t.Fatalf("start = %d, esperava 9", start)
	}
	_ = repo.RegistrarEtapa(context.Background(), &EtapaSessao{SessaoID: sessao.ID, Etapa: 9, Status: EtapaConcluida})
	start, _ = PlanejarRetomada(context.Background(), repo, sessao.ID)
	if start != 10 {
		t.Fatalf("start = %d, esperava 10", start)
	}
	_ = repo.RegistrarEtapa(context.Background(), &EtapaSessao{SessaoID: sessao.ID, Etapa: 10, Status: EtapaConcluida})
	_ = repo.RegistrarEtapa(context.Background(), &EtapaSessao{SessaoID: sessao.ID, Etapa: 11, Status: EtapaFalhou})
	start, _ = PlanejarRetomada(context.Background(), repo, sessao.ID)
	if start != 10 {
		t.Fatalf("start = %d, esperava 10 após falha", start)
	}
	_ = repo.RegistrarEtapa(context.Background(), &EtapaSessao{SessaoID: sessao.ID, Etapa: 11, Status: EtapaConcluida})
	start, _ = PlanejarRetomada(context.Background(), repo, sessao.ID)
	if start != 12 {
		t.Fatalf("start = %d, esperava 12", start)
	}
	if _, err := os.Stat(manifest.Caminho); err != nil {
		t.Fatalf("manifesto: %v", err)
	}
}

func TestRetomadaAPartirDoPasso8(t *testing.T) {
	repo := newFakeRepo()
	sessao := newTestSessao()
	if err := repo.CreateSessao(context.Background(), sessao); err != nil {
		t.Fatalf("CreateSessao: %v", err)
	}
	outDir := t.TempDir()
	artifactPath := filepath.Join(outDir, "vim.deb")
	if err := os.WriteFile(artifactPath, []byte("vim"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := transfer.GerarManifesto(outDir, []string{artifactPath}); err != nil {
		t.Fatalf("manifesto: %v", err)
	}
	if err := repo.AdicionarPacote(context.Background(), &Pacote{
		SessaoID: sessao.ID, Nome: "vim", Versao: "9.0", Arquitetura: "amd64",
		Status: PacoteBaixado, CaminhoLocal: artifactPath, SHA256: "abc",
	}); err != nil {
		t.Fatalf("AdicionarPacote: %v", err)
	}
	for step := 1; step <= 7; step++ {
		_ = repo.RegistrarEtapa(context.Background(), &EtapaSessao{SessaoID: sessao.ID, Etapa: step, Status: EtapaConcluida})
	}
	sessao.OutputDir = outDir
	sessao.RemoteDir = "/tmp/opf-sessao-1"
	if err := repo.AtualizarSessaoExecucao(context.Background(), sessao.ID, outDir, sessao.RemoteDir, "", "", "", ""); err != nil {
		t.Fatalf("exec: %v", err)
	}
	fetcher := &fakeFetcher{sourceDir: t.TempDir()}
	transferManager := &fakeTransfer{}
	result, err := ExecutarPipeline(context.Background(), PipelineConfig{
		Repo: repo, Fetcher: fetcher, Packages: []string{"vim"},
		OutputBaseDir: t.TempDir(), ResumeFrom: 8,
		Transfer: transferManager, SSH: SSHConfig{Host: "host", User: "root"},
	}, sessao)
	if err != nil {
		t.Fatalf("retomada: %v", err)
	}
	if fetcher.prepareCall != 0 || fetcher.downloads != 0 {
		t.Fatalf("retomada não deveria baixar novamente: prepare=%d downloads=%d", fetcher.prepareCall, fetcher.downloads)
	}
	if result.StartStep != 8 || transferManager.uploadCall != 1 {
		t.Fatalf("resultado: %+v uploads=%d", result, transferManager.uploadCall)
	}
	for _, pacote := range repo.pacotes {
		if pacote.Status != PacoteEnviado {
			t.Fatalf("status = %q, esperava enviado", pacote.Status)
		}
	}
}

func TestExecutarPipelineValidacoes(t *testing.T) {
	sessao := newTestSessao()
	if _, err := ExecutarPipeline(context.Background(), PipelineConfig{
		Fetcher: &fakeFetcher{sourceDir: t.TempDir()},
		Pacote:  "vim",
	}, sessao); err == nil {
		t.Fatal("esperava erro sem repositório")
	}
	if _, err := ExecutarPipeline(context.Background(), PipelineConfig{
		Repo:   newFakeRepo(),
		Pacote: "vim",
	}, sessao); err == nil {
		t.Fatal("esperava erro sem fetcher")
	}
	if _, err := ExecutarPipeline(context.Background(), PipelineConfig{
		Repo:    newFakeRepo(),
		Fetcher: &fakeFetcher{sourceDir: t.TempDir()},
	}, sessao); err == nil {
		t.Fatal("esperava erro sem pacote")
	}
}
