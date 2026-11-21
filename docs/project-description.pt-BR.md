# Documento Técnico de Projeto

## Offline Package Fetcher (OPF) — Ferramenta de Download e Transferência de Pacotes Linux para Ambientes Isolados

**Versão:** 1.2 (F0–F8 implementadas; revisão UX SSH-first)
**Status:** F0–F8 implementadas
**Base:** Decisões consolidadas neste chat + alinhamento F6/F7 com `Definições para GUI e TUI.md`
**Linguagem-alvo:** Go 1.22+

> **Revisão 1.2:** SSH passa a ser o primeiro passo; o alvo pode ser detectado remotamente via `/etc/os-release` e `uname -m`, com configuração manual como opção avançada. A verificação de host key continua estrita por padrão, mas passa a existir um opt-in explícito e inseguro para ignorá-la (`StrictHostKeyChecking=no`), em todos os hops da sessão. Presets de Ubuntu, Debian, Alpine, CentOS/Stream, Rocky, AlmaLinux, Fedora e RHEL/UBI foram adicionados/testados. Em caso de conflito, este adendo e a seção 19 prevalecem.

---

## 1. Visão e Objetivos

O **Offline Package Fetcher (OPF)** é uma ferramenta multiplataforma (Linux, Windows e, como recurso secundário, macOS) destinada a apoiar ambientes isolados de rede. Ela automatiza o ciclo **download → verificação → transferência → instalação** de pacotes Linux e suas dependências completas, eliminando o retrabalho de descobrir dependências faltantes apenas no destino.

O OPF resolve um problema operacional concreto: ambientes de suporte sem acesso à internet, onde a instalação manual de pacotes exige múltiplas rodadas de tentativa e erro. A ferramenta garante que **todos os pacotes necessários** (o principal e suas dependências, opcionais ou não, conforme configuração) sejam baixados **uma única vez** e entregues íntegros ao destino.

---

## 2. Requisitos Consolidados

| ID | Requisito | Como o OPF atende |
|:---|:---|:---|
| **R1** | Executável em Linux, Windows e macOS (macOS não prioritário) | CLI e TUI em Go puro com `CGO_ENABLED=0` e cross-compilação; GUI Fyne é binário separado, compilado com `CGO_ENABLED=1` (ADR-11) |
| **R2** | GUI e TUI | GUI com **Fyne**; TUI com **Bubble Tea**; ambas consomem o mesmo núcleo |
| **R3** | Consultar repositórios originais do SO na versão definida | Ambiente isolado (Docker/WSL) criado a partir da imagem/distro exata do destino |
| **R4** | Uso de WSL e/ou Docker para o workload | Interface `PackageFetcher` com `DockerFetcher` (Linux/macOS) e `WSLFetcher` (Windows, padrão), Docker opcional no Windows |
| **R5** | Envio via SSH após/durante o download | `TransferManager` com `golang.org/x/crypto/ssh` + `pkg/sftp` |
| **R6** | Perguntar se pode instalar após o envio | Confirmação explícita na UI antes da execução remota do comando de instalação |
| **R7** | Registrar instalados, já instalados e não instalados (nome, versão, dependências) | SQLite com modelo de 3 tabelas (`sessoes`, `pacotes`, `eventos`); verificação prévia no destino via SSH |
| **R8** | Baixar todas as dependências, evitando downloads múltiplos | Resolvedor de dependências nativo por distro com resolução recursiva; manifesto `SHA256SUMS` único por sessão |

**Requisitos transversais adicionais (decorrentes do chat):**

- **R9** — Flag para incluir ou excluir dependências opcionais (Recommends/Suggests no APT; equivalentes em YUM/DNF e APK).
- **R10** — Verificação de integridade em **três pontos**: pós-download, no armazenamento local (manifesto) e no destino (antes da instalação).
- **R11** — Envio de relatório de sessão e eventos para **Syslog externo** (servidor dedicado), de forma assíncrona e best-effort.
- **R12** — Ambientes isolados (Docker e WSL) são **temporários**: diretórios de trabalho criados por sessão e removidos ao final, sem poluir o SO hospedeiro.

---

## 3. Decisões Arquiteturais Finais (ADRs)

| ADR | Decisão | Justificativa |
|:---|:---|:---|
| **ADR-01** | GUI com **Fyne** (não Wails) | Binário estático, sem WebView, sem Node.js no destino; alinhado ao princípio de “não inchar o SO” |
| **ADR-02** | TUI com **Bubble Tea** | Framework Elm-architecture maduro, componentes prontos, amplamente usado em ferramentas Go |
| **ADR-03** | SQLite com **`modernc.org/sqlite`** (não `mattn`) | Driver puro Go, sem CGO, permite `CGO_ENABLED=0` e cross-compilação limpa |
| **ADR-04** | Orquestração via interface **`PackageFetcher`** | Desacopla Docker e WSL do núcleo; permite trocar implementação sem reescrever a lógica de negócio |
| **ADR-05** | **WSL como padrão no Windows**; Docker opcional | Evita dependência não nativa no Windows; WSL é nativo do SO |
| **ADR-06** | Isolamento obrigatório de cache em WSL | `apt-get download` com `-o Dir::Cache::archives=<tmp>`; nunca tocar no cache do usuário |
| **ADR-07** | Download com comandos **nativos por distro** | APT: `apt-cache depends --recurse` (ou `apt-rdepends`) + `apt-get download`; YUM/DNF: `yumdownloader --resolve`/`repotrack`; APK: `apk fetch --recursive` |
| **ADR-08** | Checksum em **três pontos** + manifesto `SHA256SUMS` | Garante integridade ponta a ponta; verificação obrigatória antes da instalação |
| **ADR-09** | SQLite como fonte primária; **Syslog externo assíncrono** | Persistência local sempre funcional; envio remoto não bloqueia o fluxo principal |
| **ADR-10** | Ambientes e diretórios **efêmeros** | Sessão cria, usa e destrói; sem resíduos no SO hospedeiro |
| **ADR-11** | **Build separado para GUI Fyne** | GUI desktop exige CGO/bibliotecas gráficas; CLI e TUI permanecem puros com `CGO_ENABLED=0`. O binário GUI usa `CGO_ENABLED=1` e target de build próprio |

---

## 4. Stack Tecnológica Definitiva

| Camada | Componente | Biblioteca / Ferramenta |
|:---|:---|:---|
| Linguagem | Go | 1.22+ |
| GUI | Fyne | `fyne.io/fyne/v2` |
| TUI | Bubble Tea + Bubbles | `charm.land/bubbletea/v2`, `charm.land/bubbles/v2` |
| Orquestração Docker | Docker SDK (alto nível) | `github.com/docker/go-sdk/container` |
| Orquestração WSL | `gowsl` | `github.com/ubuntu/gowsl` |
| SSH / SFTP | `x/crypto/ssh` + `pkg/sftp` | `golang.org/x/crypto/ssh`, `github.com/pkg/sftp` |
| Banco local | SQLite puro Go | `modernc.org/sqlite` |
| Syslog | Stdlib / wrapper | `log/syslog` (stdlib) ou `go-slog-syslog` (RFC 5424) |
| Checksum | SHA-256 | `crypto/sha256` (stdlib) |
| Logging estruturado | `slog` | `log/slog` (stdlib) |

**Meta de build:** CLI/TUI em binários Go puros com `CGO_ENABLED=0` e `-ldflags="-s -w"`. A GUI Fyne é um binário separado com `CGO_ENABLED=1`, mantendo o núcleo, CLI e TUI independentes de CGO. `make build-linux` continua compilando o CLI com `CGO_ENABLED=0`; a GUI terá target próprio (ADR-11).

---

## 5. Arquitetura de Camadas

```
┌───────────────────────────────────────────────────────────────┐
│                     CAMADA DE APRESENTAÇÃO                     │
│  ┌────────────────────┐          ┌────────────────────────┐   │
│  │   GUI (Fyne)       │          │   TUI (Bubble Tea)     │   │
│  └─────────┬──────────┘          └───────────┬────────────┘   │
│            └──────────────┬──────────────────┘                 │
├───────────────────────────┼────────────────────────────────────┤
│                  CAMADA DE ORQUESTRAÇÃO (NÚCLEO)               │
│                                                                │
│   ┌──────────────────────────────────────────────────────┐    │
│   │  Sessao (coordenador do pipeline)                     │    │
│   └──────────────────────────────────────────────────────┘    │
│                                                                │
│   ┌──────────────────┐   ┌──────────────────────────────┐     │
│   │ PackageFetcher   │   │ DependencyResolver           │     │
│   │  ├ DockerFetcher │   │  ├ AptResolver               │     │
│   │  └ WSLFetcher    │   │  ├ YumDnfResolver            │     │
│   └──────────────────┘   │  └ ApkResolver               │     │
│                          └──────────────────────────────┘     │
│                                                                │
│   ┌──────────────────┐   ┌──────────────────────────────┐     │
│   │ TransferManager  │   │ InstallExecutor              │     │
│   │ (SSH/SFTP)       │   │  ├ AptInstaller              │     │
│   └──────────────────┘   │  ├ YumDnfInstaller           │     │
│                          │  └ ApkInstaller              │     │
│                          └──────────────────────────────┘     │
├────────────────────────────────────────────────────────────────┤
│             CAMADA DE PERSISTÊNCIA E OBSERVABILIDADE           │
│  ┌──────────────────────────┐   ┌────────────────────────┐    │
│  │ SQLite (modernc)         │   │ Syslog externo (slog)  │    │
│  └──────────────────────────┘   └────────────────────────┘    │
└────────────────────────────────────────────────────────────────┘
```

**Princípios arquiteturais:**

1. **Núcleo agnóstico de UI.** GUI e TUI apenas chamam métodos do núcleo. Nenhuma regra de negócio mora na camada de apresentação.
2. **Inversão de dependência.** O núcleo depende de interfaces (`PackageFetcher`, `DependencyResolver`, `TransferManager`, `InstallExecutor`), não de implementações concretas.
3. **Idempotência por sessão.** Cada execução gera uma `Sessao` com ID próprio; todos os artefatos e logs são associados a ela.
4. **Efemeridade.** Ambientes e diretórios criados por sessão são destruídos ao final (sucesso ou falha), com bloco `defer` no coordenador.

---

## 6. Fluxo de Execução (Pipeline)

```
[Usuário define: SO/versão/arch de destino + pacotes + opções (opcionais, SSH, Syslog)]
        │
        ▼
[1] Abrir sessão → criar registro em `sessoes`
        │
        ▼
[2] (Opcional) Conectar via SSH ao destino e verificar pacotes já instalados
        │  └── Se já instalado(s) → registrar `ja_instalado` e excluir do escopo
        ▼
[3] Criar ambiente isolado (Docker ou WSL) com imagem/distro exata
        │
        ▼
[4] Resolver dependências dentro do ambiente
        │  └── Com/sem opcionais conforme flag da sessão
        ▼
[5] Baixar todos os pacotes para diretório temporário isolado
        │  └── Cache isolado (nunca no cache padrão do ambiente)
        ▼
[6] Copiar pacotes do ambiente para o host (CopyFromContainer ou cp via WSL)
        │
        ▼
[7] Gerar manifesto SHA256SUMS no host
        │  └── Se algum download falhou → abortar antes da transferência
        ▼
[8] Conectar via SSH ao destino e enviar pacotes + manifesto via SFTP
        │
        ▼
[9] Verificar checksums no destino (sha256sum -c SHA256SUMS)
        │  └── Se falhar → abortar, registrar erro, notificar
        ▼
[10] Perguntar ao usuário se deseja instalar
        │  └── Se não → registrar `enviado_nao_instalado` e encerrar
        ▼
[11] Executar instalação remota com comando da distro
        │
        ▼
[12] Registrar resultado (instalado/falhou) + emitir eventos ao Syslog
        │
        ▼
[13] Cleanup: destruir ambiente isolado + remover diretórios temporários
```

**Adendo F6/F7 ao pipeline:**

- A sessão passa a receber **uma lista de pacotes principais** (`[]string`), com um único manifesto, uma transferência e uma instalação.
- O passo 2 (pré-check) é executado quando há SSH e deixa de ser apenas opcional na UI; sem SSH ele é ocultado.
- Cada etapa é registrada em `etapas_sessao`, permitindo progresso e retomada por checkpoint seguro.
- Cancelamento por contexto deve encerrar com `SessaoAbortada`, executar cleanup local e remoto best-effort e nunca marcar a sessão como `falhou` apenas por cancelamento do usuário.
- Progresso e logs são emitidos por uma interface de reporter; a saída remota deve ser transmitida em streaming, não apenas ao final do comando.
- A instalação não pode ser retomada automaticamente. Se a sessão parou nos passos 10/11, a retomada reexibe confirmação, reconsulta o estado dos pacotes no destino e exige nova decisão.
- A recusa de instalação registra os pacotes como `enviado_nao_instalado` e conclui a sessão.
- A falha de instalação registra `falhou` e permite nova tentativa apenas via nova confirmação/retomada segura.

---

## 7. Contratos de Interface (Go)

```go
// ============================================================
// Camada de orquestração — interfaces do núcleo
// ============================================================

// PackageFetcher abstrai Docker e WSL.
type PackageFetcher interface {
    // Prepare cria o ambiente isolado com SO/versão/arch exatos.
    Prepare(ctx context.Context, targetOS, targetVersion, arch string) error
    // Download executa a resolução e o download dentro do ambiente.
    Download(ctx context.Context, pkg string, includeOptional bool) ([]PackageArtifact, error)
    // Cleanup destrói o ambiente e diretórios temporários.
    Cleanup(ctx context.Context) error
}

// PackageArtifact representa um arquivo baixado e verificado.
type PackageArtifact struct {
    Name         string
    Version      string
    Arch         string
    LocalPath    string // caminho no host após cópia do ambiente
    IsDependency bool
    SHA256       string
}

// DependencyResolver é específico por família de distro.
type DependencyResolver interface {
    // ListDependencies retorna a lista recursiva de dependências.
    ListDependencies(ctx context.Context, pkg string, includeOptional bool) ([]string, error)
    // DownloadCommands retorna os comandos a executar no ambiente isolado.
    DownloadCommands(pkgs []string) [][]string
}

// TransferManager cuida do SSH/SFTP e da verificação remota.
type TransferManager interface {
    Connect(ctx context.Context, cfg SSHConfig) error
    Upload(ctx context.Context, files []string, remoteDir string) error
    VerifyChecksums(ctx context.Context, manifestPath, remoteDir string) error
    RunCommand(ctx context.Context, cmd string) (stdout string, err error)
    Close() error
}

// SSHConfig agrupa parâmetros de conexão.
type SSHConfig struct {
    Host        string
    Port        int
    User        string
    KeyPath     string
    Password    string // opcional; preferir chave; no destino também autentica o sudo via stdin
    KnownHosts  string // caminho para known_hosts (estrito por padrão)
    // InsecureSkipHostKey equivale a StrictHostKeyChecking=no e vale para o
    // destino e para todos os hops.
    InsecureSkipHostKey bool
    // JumpHosts são abertos em ordem, cada um através da conexão anterior.
    // Cada hop exige host, usuário e credencial própria (senha, chave ou
    // ambas); senha/chave nunca são herdadas do destino. KnownHosts vazio no
    // hop herda o known_hosts do destino.
    JumpHosts   []SSHConfig
}

// InstallExecutor gera e executa a instalação remota.
type InstallExecutor interface {
    // InstallCommand monta o comando de instalação para a distro.
    InstallCommand(pkgs []PackageArtifact) (string, error)
    // Execute roda o comando via TransferManager.
    Execute(ctx context.Context, tm TransferManager, cmd string) error
}
```

**Contrato da Sessão (coordenador):**

```go
type Sessao struct {
    ID            int64
    TargetOS      string
    TargetVersion string
    TargetArch    string
    FetchMethod   string // "docker" | "wsl"
    HostDestino   string
    IncludeOptional bool
}

// Coordenador executa o pipeline completo (passos 1–13 da seção 6).
func ExecutarPipeline(ctx context.Context, cfg Config, s *Sessao) error
```

**Adendo F6/F7 aos contratos:**

```go
// Config do pipeline ganha a lista de roots. Pacote permanece aceito para
// compatibilidade com F0–F5 e é convertido em Packages quando necessário.
type PipelineConfig struct {
    // ...campos existentes...
    Pacote   string   // legado/atalho para um único root
    Packages []string // roots solicitados; o pipeline baixa a união das dependências
}

// Reporter é opcional e usado pela GUI/TUI para progresso, logs e etapas.
type PipelineReporter interface {
    StepStarted(step int, name string)
    StepFinished(step int, name string)
    Progress(stage string, current, total int64)
    Log(level, message string)
}

// TransferManager ganha execução com stdin e streaming (usadas pelo sudo e
// pela tela de instalação). Implementações antigas podem expor interfaces
// opcionais equivalentes, mantendo o contrato base compatível.
type CommandInputRunner interface {
    RunCommandWithInput(ctx context.Context, cmd string, input []byte) (string, error)
}

type CommandStreamRunner interface {
    RunCommandStream(ctx context.Context, cmd string, stdout, stderr io.Writer) error
}
```

**Contrato multi-pacote (compatível com F0–F5):**

```go
type MultiPackageFetcher interface {
    DownloadPackages(ctx context.Context, pkgs []string, includeOptional bool) ([]PackageArtifact, error)
}

// Opcional: pré-check remoto por família.
type PackageInspector interface {
    Inspect(ctx context.Context, pkgs []string) ([]InstalledPackage, error)
}

type InstalledPackage struct {
    Name    string
    Version string
    Arch    string
}
```

- Se o fetcher implementar `MultiPackageFetcher`, o pipeline baixa a união de roots em um único ambiente/manifesto. Caso contrário, o pipeline pode chamar `Download` para cada root e deduplicar artefatos.
- `PackageArtifact.IsDependency` passa a ser calculado contra o conjunto de roots; um artefato que seja root de qualquer solicitação não é dependência.
- `InstallExecutor` recebe a senha de sudo apenas por `CommandInputRunner`, nunca por argv.
- O núcleo detecta `id -u` no destino: `0` dispensa sudo; não-root exige a senha da conta SSH.
- `TransferManager` deve permitir streaming de saída e remoção best-effort do diretório remoto em cancelamento/cleanup.
- `InsecureSkipHostKey` é opt-in por sessão e aplica-se a todos os hops; o padrão continua estrito e a UI exibe aviso explícito.
- `PipelineReporter` é opcional; sem ele, o comportamento CLI atual (logs/eventos) permanece.

---

## 8. Estratégia por Distribuição

### 8.1 Download (dentro do ambiente isolado)

| Distro | Listar dependências | Baixar | Observações |
|:---|:---|:---|:---|
| **Debian/Ubuntu** | `apt-cache depends --recurse --no-recommends --no-suggests --no-conflicts --no-breaks --no-replaces --no-enhances <pkg>` (padrão) ou `apt-rdepends --follow=Depends,PreDepends <pkg>` | `apt-get download <lista>` | Isolar com `-o Dir::Cache::archives=<tmp>`; fixar arch com `--arch=<arch>` se necessário |
| **RHEL/CentOS/Fedora** | `yumdownloader --resolve --urls <pkg>` ou `repotrack -u <pkg>` | `yumdownloader --resolve --destdir=<tmp> <pkg>` ou `repotrack -a <arch> -p <tmp> <pkg>` | Requer `yum-utils`/`dnf-plugins-core` na imagem |
| **Alpine** | `apk fetch --simulate --recursive <pkg>` | `apk fetch --recursive --output <tmp> <pkg>` | Usar `--repository` da versão exata se necessário |

**EPEL em derivados RHEL:** antes do download, o `DockerFetcher` instala `epel-release` (com fallback para o RPM oficial do Fedora no UBI), habilita CRB/PowerTools quando disponível e reimporta as chaves RPM. Isso permite pacotes comuns fora dos repositórios base, como `htop`, em Rocky/Alma/CentOS. No UBI, o catálogo reduzido ainda pode não fornecer dependências de alguns pacotes EPEL (por exemplo, `hwloc`). Fedora não recebe EPEL. Nomes de pacote seguem a distro de destino: `mtr-tiny` é Debian/Ubuntu; em Rocky/RHEL o equivalente é `mtr`.

**Flag de opcionais (`includeOptional`):**

- **APT:** quando `false`, incluir `--no-recommends --no-suggests --no-enhances`. Quando `true`, omitir essas flags.
- **YUM/DNF:** quando `false`, adicionar `--setopt=install_weak_deps=False` (DNF) e não incluir weak deps; YUM 3 não possui o conceito e ignora a flag. Quando `true`, padrão.
- **APK:** APK não tem distinção forte de opcionais; a flag é ignorada com aviso na UI.

### 8.2 Instalação (executada remotamente no destino)

| Distro | Comando de instalação | Observações |
|:---|:---|:---|
| **Debian/Ubuntu** | `dpkg -i <arquivos>`; se falhar, `apt-get install -f -y` | Sem `--force-*`; `--allow-unauthenticated` não exposto na F6/F7 |
| **RHEL/CentOS/Fedora** | `yum localinstall -y <arquivos>` ou `dnf localinstall -y <arquivos>` | `--nogpgcheck` não exposto na F6/F7; falha de assinatura aborta e orienta importar chaves |
| **Alpine** | `apk add --allow-untrusted <arquivos>` | `--allow-untrusted` obrigatório para pacotes não assinados; sempre avisar o usuário; P1 `apk verify` antes |

---

## 9. Gestão de Ambientes Isolados (Docker e WSL)

### 9.1 Docker (`DockerFetcher`)

**Disponível em:** Linux e macOS (padrão), Windows (opcional).

- Cria contêiner a partir do preset validado: Ubuntu 22.04/24.04, Debian 12 (após teste), CentOS 7 com imagem preparada (`opf/centos-7:f3`) e Alpine 3.20. Imagens fora dos presets exigem override explícito e podem ser marcadas como “avançado”.
- Executa comandos via `container.Exec`.
- Copia artefatos com `CopyFromContainer` para diretório temporário do host.
- `Cleanup` remove o contêiner, o diretório temporário e resíduos best-effort em cancelamento.
- **Diretório de trabalho no contêiner:** `/tmp/opf-<sessao-id>/`.

### 9.2 WSL (`WSLFetcher`)

**Disponível em:** Windows (padrão).

- Usa `gowsl` para listar e executar comandos em distro WSL registrada. **Instalação automática de WSL não é suportada na F6/F7** (operação administrativa externa).
- **Isolamento obrigatório:** downloads usam `Dir::Cache::archives`, `Dir::State::lists`, `Dir::Cache::pkgcache` e `Dir::Cache::srcpkgcache` sob `/tmp/opf-<sessao-id>/`.
- Nunca executa `apt-get update` no WSL do usuário sem isolamento.
- Artefatos copiados via `wsl.exe cat` (implementação atual) ou caminho UNC equivalente.
- **Diretório de trabalho no WSL:** `/tmp/opf-<sessao-id>/`.
- `Cleanup` remove o diretório isolado; **nunca desregistra distros preexistentes**.

### 9.3 Seleção automática

| SO hospedeiro | Automático | Alternativas |
|:---|:---|:---|
| Linux | Docker | Docker explícito |
| macOS | Docker | Docker explícito |
| Windows | WSL se houver distro Ubuntu/Debian compatível; caso contrário Docker | Docker; WSL explícito apenas para APT |

A UI expõe a escolha; CLI/TUI usam `--fetch-method=docker|wsl`. “Apenas WSL” fica desabilitado para famílias RPM/APK ou distro/versão incompatível. WSL suporta apenas Ubuntu/Debian (APT) nesta fase.

---

## 10. Integridade e Checksums (Três Pontos)

| Ponto | Quando | Como | Ação em caso de falha |
|:---|:---|:---|:---|
| **P1 — Pós-download no ambiente** | Após `apt-get download` / `yumdownloader` / `apk fetch` | `debsums -c` (APT), `rpm -K` (YUM/DNF), `apk verify` (APK) | Abortar a sessão antes da cópia para o host |
| **P2 — Armazenamento local** | Após copiar do ambiente para o host | Gerar `SHA256SUMS` com todos os arquivos | Se algum arquivo faltar ou falhar, abortar antes do upload |
| **P3 — Destino remoto** | Após upload via SFTP, antes de instalar | `sha256sum -c SHA256SUMS` no destino | Abortar a instalação, registrar erro, notificar |

**Regra:** o manifesto `SHA256SUMS` é gerado **apenas** se P1 e P2 passarem. O upload só começa se o manifesto existir. A instalação só começa se P3 passar.

---

## 11. Persistência e Observabilidade

### 11.1 Esquema SQLite

```sql
PRAGMA journal_mode = WAL;

CREATE TABLE sessoes (
    id              INTEGER PRIMARY KEY,
    iniciada_em     DATETIME NOT NULL,
    finalizada_em   DATETIME,
    so_destino      TEXT NOT NULL,
    versao_destino  TEXT NOT NULL,
    arch_destino    TEXT NOT NULL,
    metodo_fetch    TEXT NOT NULL CHECK (metodo_fetch IN ('docker','wsl')),
    host_destino    TEXT,
    include_optional INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL CHECK (status IN
                      ('em_andamento','concluida','falhou','abortada'))
);

CREATE TABLE pacotes (
    id              INTEGER PRIMARY KEY,
    sessao_id       INTEGER NOT NULL REFERENCES sessoes(id) ON DELETE CASCADE,
    nome            TEXT NOT NULL,
    versao          TEXT NOT NULL,
    arquitetura     TEXT,
    status          TEXT NOT NULL CHECK (status IN
                      ('baixado','enviado','instalado','falhou','ja_instalado')),
    eh_dependencia  INTEGER NOT NULL DEFAULT 0,
    caminho_local   TEXT,
    sha256          TEXT
);

CREATE TABLE eventos (
    id          INTEGER PRIMARY KEY,
    sessao_id   INTEGER NOT NULL REFERENCES sessoes(id) ON DELETE CASCADE,
    timestamp   DATETIME NOT NULL,
    tipo        TEXT NOT NULL,   -- 'download','transferencia','instalacao','erro','info'
    mensagem    TEXT NOT NULL,
    detalhes    TEXT             -- JSON livre
);

CREATE INDEX idx_pacotes_sessao  ON pacotes(sessao_id);
CREATE INDEX idx_eventos_sessao  ON eventos(sessao_id);
```

### 11.2 Syslog externo

- Implementado com `slog` e **handler duplo**: escrita no SQLite (síncrona, fonte primária) + envio ao Syslog remoto (assíncrono, best-effort).
- Transporte: UDP ou TCP (configurável), RFC 5424.
- Se o servidor Syslog estiver indisponível, o OPF **não falha** — registra localmente e enfileira para retry em background com backoff exponencial.
- Configuração via UI/TUI: `syslog.host`, `syslog.port`, `syslog.proto`, `syslog.tag`.

### 11.3 Relatório de sessão

- Exportável em JSON e Markdown a partir das tabelas de sessão, pacotes, eventos e pacotes solicitados.
- Inclui: SO/versão/arch de destino, método de fetch, **host remoto** (`usuário SSH`, kernel, uptime, CPU, memória e disco), pacotes solicitados e dependências, papel de cada pacote (**principal** ou **dependente**), versão anterior encontrada no destino, status, hashes, timestamps, eventos relevantes, resultado final e **destinos local/remoto** dos artefatos.
- O host remoto é coletado no pipeline quando há SSH e `Inspector`, antes do envio; a versão anterior é capturada antes da instalação para cada artefato.
- Pode ser enviado ao Syslog como anexo/evento estruturado.
- Pode ser enviado ao Syslog como anexo/evento estruturado.

### 11.4 Extensões de schema para F6/F7

As extensões abaixo são obrigatórias para múltiplos pacotes, pré-check, progresso, retomada segura, histórico e relatório local. Segredos (senha SSH/sudo, conteúdo de chave privada) **nunca** são persistidos.

**`sessoes` — novas colunas:**

| Coluna | Tipo | Uso |
|---|---|---|
| `etapa_atual` | INTEGER NOT NULL DEFAULT 1 | Última etapa considerada segura para retomada |
| `output_dir` | TEXT | Diretório local dos artefatos/manifesto |
| `remote_dir` | TEXT | Diretório remoto da sessão |
| `imagem_docker` | TEXT | Imagem efetivamente usada |
| `wsl_distro` | TEXT | Distro WSL efetivamente usada |
| `ambiente_id` | TEXT | Identificador do contêiner/WSL/temporário para cleanup |
| `config_json` | TEXT | Configuração não sensível (host, usuário, portas, pacotes, opções) |
| `host_info_json` | TEXT | Host remoto não sensível: usuário SSH, kernel, uptime, CPU, memória e disco |
| `arquivada` | INTEGER NOT NULL DEFAULT 0 | Arquivamento lógico do histórico |

**`pacotes_solicitados` — nova tabela:**

```sql
CREATE TABLE pacotes_solicitados (
    id          INTEGER PRIMARY KEY,
    sessao_id   INTEGER NOT NULL REFERENCES sessoes(id) ON DELETE CASCADE,
    nome        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pendente',
    motivo      TEXT
);
```

**`etapas_sessao` — nova tabela:**

```sql
CREATE TABLE etapas_sessao (
    sessao_id      INTEGER NOT NULL REFERENCES sessoes(id) ON DELETE CASCADE,
    etapa          INTEGER NOT NULL,
    nome           TEXT NOT NULL,
    status         TEXT NOT NULL, -- pendente | em_andamento | concluida | falhou | pulada
    iniciada_em    DATETIME,
    finalizada_em  DATETIME,
    erro           TEXT,
    PRIMARY KEY (sessao_id, etapa)
);
```

**`pacotes` — novas colunas:**

| Coluna | Tipo | Uso |
|---|---|---|
| `sha256_p1` | TEXT | Hash verificado no ambiente isolado |
| `sha256_p2` | TEXT | Hash verificado na cópia do host |
| `p3_verificado` | INTEGER | Resultado da verificação remota por arquivo |
| `instalado_verificado` | INTEGER | Confirmação pós-instalação no gerenciador remoto |
| `versao_anterior` | TEXT | Versão encontrada no destino antes da instalação, quando aplicável |

O enum de status de `pacotes` passa a incluir `enviado_nao_instalado`. A migração F6.0 deve atualizar bancos F0–F5 de forma automática e preservar os dados existentes.

---

## 12. Segurança e Tratamento de Erros

| Tema | Diretriz |
|:---|:---|
| **Host key SSH** | Validar contra `known_hosts` por padrão em todos os hops. Bypass opt-in explícito (`InsecureSkipHostKey`, equivalente a `StrictHostKeyChecking=no`) é permitido apenas por decisão do usuário, com aviso vermelho, nunca como padrão |
| **Autenticação SSH** | Preferir chave; senha suportada mas com aviso |
| **Credenciais por jump** | Cada ponte exige host, usuário e credencial própria (`senha`, `chave` ou `chave+senha`); senha/chave nunca são herdadas do destino; `known_hosts` vazio herda o do destino; senhas de ponte só existem em memória |
| **Sudo** | Senha da conta SSH via `sudo -S`/stdin; root (`id -u`=0) dispensa sudo; **NOPASSWD não suportado**; senha nunca persiste |
| **Assinatura de pacotes** | Validar em laboratório por distro/versão. `--nogpgcheck`/`--allow-unauthenticated` ficam **desabilitados**; Alpine usa `--allow-untrusted` por exigência do projeto, com aviso explícito e P1 `apk verify` |
| **Retry de rede** | Backoff exponencial para conexões/upload e downloads; máximo configurável; implementação prevista na F6.0. Falha após esgotar tentativas registra erro e aborta |
| **Erros SSH/SFTP** | Mapear códigos comuns de `x/crypto/ssh` e `pkg/sftp` para mensagens legíveis; classificar como `retryable` ou `fatal` |
| **Timeouts** | Timeout por etapa (prepare, download, upload, verify, install) configurável; default 10 min por etapa |
| **Cleanup garantido** | `defer` no coordenador garante `Cleanup()` do fetcher e remoção de diretórios temporários mesmo em falha |
| **Logs sem segredos** | Nunca logar senha SSH, chave privada ou conteúdo de arquivos; apenas caminhos e hashes |

---

## 13. Roadmap e Critérios de Aceitação

| Fase | Entrega | Critério de Aceitação |
|:---|:---|:---|
| **F0 — Núcleo + Docker + APT** | `DockerFetcher`, `AptResolver`, download e `SHA256SUMS` | Baixar `vim` + dependências no Ubuntu 22.04 via Docker e gerar manifesto válido |
| **F1 — Transferência SSH** | `TransferManager` com SFTP + verificação remota | Enviar artefatos da F0 a host de teste e validar `sha256sum -c` |
| **F2 — Instalação remota** | `AptInstaller` + confirmação do usuário | Instalar no host de teste após confirmação e registrar no SQLite |
| **F3 — Segunda família (YUM/DNF)** | `YumDnfResolver` + `YumDnfInstaller` | Mesmo fluxo da F2 com `yumdownloader` e `yum localinstall` em CentOS/RHEL de teste |
| **F4 — Terceira família (APK)** | `ApkResolver` + `ApkInstaller` | Mesmo fluxo com `apk fetch --recursive` e `apk add --allow-untrusted` em Alpine de teste |
| **F5 — WSL** | `WSLFetcher` com `gowsl` + isolamento de cache | Fluxo completo no Windows usando WSL, sem poluir cache do usuário |
| **F6.0 — Fundação de estado/UI** | Multi-pacote, pré-check, progresso/streaming, cancelamento `abortada`, cleanup remoto, schema de retomada, histórico/configurações, camada `internal/app` | Núcleo e store suportam todos os fluxos das telas sem depender de Fyne/Bubble Tea |
| **F6 — GUI (Fyne)** | Telas 1–13, lista de pacotes, progresso, logs, confirmação/sudo, histórico e relatório local | Usuário executa o pipeline completo sem terminal; build separado com `CGO_ENABLED=1` |
| **F7 — TUI (Bubble Tea)** | Equivalente funcional à GUI | Mesmas operações via terminal, com build puro `CGO_ENABLED=0` |
| **F8 — Syslog + relatórios** | Handler duplo SQLite+Syslog, exportação JSON/Markdown | Eventos chegam ao Syslog externo; relatório é gerado a partir da sessão |

**Critério global de aceitação (F0–F5):** em ambiente de laboratório, executar o pipeline completo (download + transferência + verificação + instalação) para **um pacote com dependências não triviais** (ex.: `nginx`, `docker-ce`, `postgresql`) em **Ubuntu, CentOS e Alpine**, com sucesso e registro íntegro no SQLite (o envio ao Syslog é critério da F8).

**Critério adicional F6/F7:** executar uma sessão com **múltiplos roots**, pré-check remoto, recusa/instalação, cancelamento com status `abortada`, retomada por checkpoint seguro e histórico local na GUI e na TUI. Exportação e Syslog permanecem critério da F8.

---

## 14. Riscos e Mitigações

| Risco | Probabilidade | Impacto | Mitigação |
|:---|:---:|:---:|:---|
| `apt-rdepends` mistura arquiteturas | Média | Alto | Executar sempre dentro do ambiente isolado com arch fixada; validar com `dpkg --print-architecture` antes do download |
| WSL polui cache do usuário | Média | Alto | Isolamento obrigatório (ADR-06) com `Dir::Cache::archives` e `Dir::State::lists` em `/tmp/opf-<sessao>/` |
| Chaves GPG ausentes no destino | Alta | Médio | Alternativas explícitas de instalação desabilitadas por padrão; documentar procedimento de importação de chaves |
| Falha parcial no download | Média | Médio | Manifesto `SHA256SUMS` só é gerado se todos os downloads concluírem; qualquer falha aborta antes da transferência |
| Syslog externo indisponível | Média | Baixo | SQLite é fonte primária; envio remoto é assíncrono com fila de retry |
| Performance do `modernc.org/sqlite` | Baixa | Baixo | Volume de eventos é baixo; WAL mode habilitado; se necessário, batch de inserts |
| Incompatibilidade de imagem Docker com arch do destino | Baixa | Alto | UI exige arch explícita; imagens multi-arch são usadas por padrão; aviso se arch não suportada |
| Diferenças de flags entre versões de distro | Alta | Médio | `DependencyResolver` e `InstallExecutor` versionados por família; testes de integração por versão no roadmap |

---

## 15. Diretrizes para Implementação com IA

### 15.1 Onde a IA agrega mais valor

1. **Geração de comandos por distro/versão.** As flags de `apt-cache depends`, `yumdownloader`, `repotrack`, `apk fetch` mudam entre versões. Pedir à IA que gere os comandos com testes unitários por versão reduz retrabalho.
2. **Parsers de saída.** As saídas de `apt-cache depends --recurse`, `yumdownloader --urls` e `apk fetch --simulate` têm formatos distintos. A IA gera parsers robustos com fixtures de exemplo.
3. **Dockerfiles de teste.** Presets validados inicialmente: Ubuntu 22.04/24.04, CentOS 7 (imagem preparada com repositórios EOL) e Alpine 3.20; Debian 12 entra após teste dedicado. Ubuntu 20.04, CentOS Stream 9 e Alpine 3.18/3.19 só serão expostos após imagem e testes próprios.
4. **Mapeamento de erros SSH/SFTP.** A IA gera um mapa de códigos comuns de `x/crypto/ssh` e `pkg/sftp` → classificação `retryable`/`fatal` → ação.
5. **Testes de checksum.** Cenários com arquivo corrompido, arquivo faltante, manifesto inválido, falha no destino — a IA gera a matriz de testes.

### 15.2 Ordem de geração de código sugerida

```
1. Modelos e contratos (interfaces da seção 7)
2. Camada SQLite (schema + DAOs + testes)
3. DependencyResolver (APT primeiro; YUM/DNF e APK depois)
4. DockerFetcher (com testes contra imagens reais)
5. TransferManager (SSH/SFTP + verificação + testes)
6. InstallExecutor (APT primeiro; demais depois)
7. Coordenador Sessao (pipeline da seção 6)
8. WSLFetcher
9. Fundação de estado/UI (F6.0: multi-pacote, pré-check, progresso/streaming, retomada, store/histórico, `internal/app`)
10. GUI (Fyne, binário separado com CGO=1)
11. TUI (Bubble Tea, CGO=0)
12. Syslog + relatórios (F8)
```

### 15.3 Restrições a serem respeitadas pela IA

- **Não** introduzir CGO no núcleo, CLI ou TUI (`CGO_ENABLED=0` deve compilar). A GUI Fyne é a única exceção: binário separado com `CGO_ENABLED=1` (ADR-11).
- **Não** usar `mattn/go-sqlite3`.
- **Não** usar Wails ou qualquer dependência de WebView.
- **Não** habilitar bypass de host key por padrão; o opt-in inseguro deve ser explícito e avisado.
- **Não** implementar NOPASSWD; sudo usa a senha da conta SSH via stdin e root é detectado por `id -u`.
- **Não** prometer progresso/MB/s de gerenciadores que não fornecem métricas estruturadas; usar etapa/quantidade e bytes apenas onde controlados.
- **Não** ignorar host key SSH sem opt-in explícito do usuário.
- **Não** logar segredos.
- **Não** executar comandos de download no SO hospedeiro fora do ambiente isolado.
- **Não** remover o isolamento de cache no WSL.

### 15.4 Estrutura de diretórios sugerida

```
opf/
├── cmd/
│   ├── opf-gui/main.go
│   └── opf-tui/main.go
├── internal/
│   ├── core/
│   │   ├── sessao.go
│   │   ├── contratos.go
│   │   └── pipeline.go
│   ├── fetcher/
│   │   ├── docker.go
│   │   └── wsl.go
│   ├── resolver/
│   │   ├── apt.go
│   │   ├── yumdnf.go
│   │   └── apk.go
│   ├── transfer/
│   │   ├── ssh.go
│   │   └── checksum.go
│   ├── installer/
│   │   ├── apt.go
│   │   ├── yumdnf.go
│   │   └── apk.go
│   ├── store/
│   │   ├── sqlite.go
│   │   └── models.go
│   └── observability/
│       ├── syslog.go
│       └── relatorio.go
├── ui/
│   ├── gui/
│   └── tui/
├── test/
│   ├── fixtures/
│   └── integration/
├── docker/
│   ├── ubuntu-22.04.Dockerfile
│   ├── centos-7.Dockerfile
│   ├── alpine-3.20.Dockerfile
│   └── (demais presets validados na F6.0)
└── go.mod
```

---

## 16. Glossário

| Termo | Definição |
|:---|:---|
| **Ambiente isolado** | Contêiner Docker ou distro WSL criado por sessão para executar downloads na distro/versão exata do destino |
| **Artefato** | Arquivo de pacote baixado (`.deb`, `.rpm`, `.apk`) com metadados (nome, versão, arch, hash) |
| **Manifesto `SHA256SUMS`** | Arquivo com hashes SHA-256 de todos os artefatos da sessão; base da verificação P2 e P3 |
| **Pipeline** | Sequência de 13 passos da seção 6 |
| **Sessão** | Execução completa do pipeline, com ID próprio e registro em `sessoes` |
| **Fetch method** | Método de orquestração: `docker` ou `wsl` |
| **Dependência opcional** | Recommends/Suggests (APT), weak deps (YUM/DNF); controlada pela flag `includeOptional` |
| **Checkpoint seguro** | Etapa persistida a partir da qual a sessão pode ser retomada sem repetir efeitos perigosos; instalação nunca é retomada automaticamente |
| **Preset validado** | Combinação distro/versão/imagem com Dockerfile e teste de integração próprios, exposta pela UI |
| **F6.0** | Fundação de estado/UI: multi-pacote, pré-check, progresso/streaming, retomada, histórico e store antes da GUI/TUI |

---

## 17. Encerramento

Este documento consolida todas as decisões tomadas no chat e está pronto para servir como **fonte única de verdade** para a implementação com IA. As pendências arquiteturais levantadas foram resolvidas e refletidas nas ADRs (seção 3). O roadmap (seção 13) define a ordem de execução; as diretrizes para IA (seção 15) definem como acelerar cada fase sem violar as restrições do projeto.

**Status final:** todas as fases F0–F8 estão implementadas. A validação de aceitação global e a matriz de distribuições podem ser ampliadas conforme a seção 13.

---

## 18. Adendo F6/F7 — Decisões consolidadas de interface e estado

Este adendo prevalece sobre qualquer descrição anterior em conflito, especialmente no documento `Definições para GUI e TUI.md`.

### 18.1 Decisões aprovadas

| Tema | Decisão |
|---|---|
| Sudo | Usar a senha da conta SSH, enviada ao `sudo -S` apenas por stdin; detectar root (`id -u` = 0) para dispensar sudo; **NOPASSWD não é suportado** |
| Pacotes | Vários roots por sessão, com um manifesto, uma transferência e uma instalação |
| Estado | Pré-check remoto obrigatório quando há SSH + retomada por checkpoint seguro |
| Jump hosts | Lista ordenada, com credenciais e `known_hosts` por hop; cada hop exige credencial própria, sem herança de senha/chave do destino; sem limite conceitual de um único bastion |
| Host key | Estrita por padrão; opt-in explícito de bypass (`StrictHostKeyChecking=no`) por sessão, aplicado a todos os hops |
| GUI | Fyne em binário separado, `CGO_ENABLED=1`; CLI/TUI permanecem `CGO_ENABLED=0` |
| Histórico | Arquivamento lógico (`arquivada`), sem deleção física |
| Configurações globais | JSON em `os.UserConfigDir()/opf/config.json`, sem segredos |
| Relatórios/Syslog | Exportação JSON/Markdown e Syslog são F8; F6/F7 apenas exibem dados locais |
| Alvos iniciais | Ubuntu 22.04/24.04, CentOS 7 preparado, Alpine 3.20; Debian 12 após teste dedicado |

### 18.2 Pipeline e retomada

- `PipelineConfig` aceita `Packages []string`; `Pacote` permanece como atalho compatível.
- O pré-check consulta o gerenciador remoto, registra `ja_instalado` e permite “forçar reinstalação”.
- Checkpoints seguros:
  - 1–7: reutilizar artefatos se o manifesto local ainda verificar; senão refazer.
  - 8–9: reconectar, reenviar e revalidar (idempotente).
  - 10–11: nunca continuar automaticamente; nova confirmação, consulta ao destino e nova decisão.
  - 12–13: registrar e limpar.
- Segredos são solicitados novamente na retomada e nunca são persistidos.
- Ambientes órfãos (contêiner/WSL/temporários/remoto) devem ser limpos ao retomar.
- Cancelamento do usuário encerra como `abortada`, com cleanup local e remoto best-effort.
- A UI deve deixar claro que cancelar durante `dpkg`/`yum`/`apk` é best-effort e pode deixar estado parcial no destino.

### 18.3 Progresso, logs e erros

- `PipelineReporter` emite eventos de etapa, progresso e log para GUI/TUI.
- O download reporta etapa e quantidade de artefatos; o upload SFTP reporta bytes reais. Não haverá MB/s inventado para `apt`/`yum`/`apk`.
- A saída remota de instalação deve ser transmitida em streaming (`CommandStreamRunner`), não bufferizada até o fim.
- Retry com backoff para conexão SSH, com máximo configurável; erros fatais não são repetidos. Retry fino de upload/download fica para a integração da UI.
- P1/P2 podem ser exibidos por pacote; P3 será por arquivo quando o gerenciador permitir parsing confiável, com fallback agregado explícito.
- Após instalar, consultar o gerenciador para registrar `instalado_verificado` por pacote quando possível; caso contrário, o resultado agregado deve ser rotulado como tal.

### 18.4 Interface e builds

- Camada compartilhada `internal/app` (ou equivalente) expõe casos de uso, estado, eventos e callbacks; Fyne e Bubble Tea não duplicam regras.
- GUI Fyne: binário separado, `CGO_ENABLED=1`, com target próprio de build e dependências gráficas documentadas.
- CLI/TUI: `CGO_ENABLED=0`, `make build-linux` continua obrigatório e inalterado.
- Configurações globais não sensíveis em JSON; nenhuma senha/chave é gravada.
- Histórico usa `ListarSessoes`, filtros, agregações e `arquivada`; host de destino deve ser preenchido no início da sessão.
- Tela de Syslog e exportação estão habilitadas; configurações são não sensíveis e a falha remota é best-effort.

### 18.5 Ordem de implementação

1. **F6.0:** contratos multi-pacote, schema/migração, pré-check, reporter/streaming, cancelamento `abortada`, cleanup remoto, store/histórico/configurações, `internal/app`.
2. **F6:** GUI Fyne sobre `internal/app`, build CGO=1, testes headless/manuais.
3. **F7:** TUI Bubble Tea com paridade funcional e `CGO_ENABLED=0`.
4. **F8:** exportação e Syslog, reutilizando o relatório local.

### 18.6 Critérios de aceitação F6/F7

- Sessão com múltiplos roots e dependências não triviais.
- Pré-check, recusa, instalação e falha registrados corretamente no SQLite.
- Cancelamento em etapas diferentes resulta em `abortada` e cleanup verificável.
- Retomada segura não reexecuta instalação sem nova confirmação.
- GUI e TUI executam o mesmo fluxo com paridade de telas/atalhos.
- `make build-linux` do CLI/TUI permanece `CGO_ENABLED=0`; GUI é validada no target CGO=1.
- Nenhum segredo em SQLite, JSON de configuração, logs ou relatórios.

### 18.7 Status da implementação

- **F6.0 concluída:** multi-pacote, pré-check, progresso/streaming, cancelamento `abortada`, cleanup remoto, schema/migração, retomada, histórico, configurações e `internal/app`.
- **F6 concluída:** GUI Fyne em `cmd/opf-gui` + `ui/gui`, com configuração, SSH/jumps, pacotes, progresso, confirmação/sudo, resultado, relatório local, histórico e configurações. Build separado com `CGO_ENABLED=1`; smoke test de inicialização aprovado.
- **F7 concluída:** TUI Bubble Tea em `cmd/opf-tui` + `ui/tui`, pura Go (`CGO_ENABLED=0`), com configuração, SSH/jumps, pacotes, progresso/logs, confirmação/sudo, resultado, relatório local, histórico e configurações.
- **F8 concluída:** `internal/observability/syslog.go` com fila assíncrona, retry e RFC 5424 UDP/TCP; `relatorio.go` com geração/exportação JSON e Markdown; CLI, GUI e TUI habilitados para exportação e configuração do Syslog.
- **Revisão pós-F8:** GUI/TUI exigem detecção ou configuração manual explícita do destino antes de executar; o pipeline coleta host remoto e versões anteriores, e o relatório unificado destaca pacote principal, dependentes e destinos. Persistência em `sessoes.host_info_json` e `pacotes.versao_anterior`.
- **Revisão visual da GUI:** shell desktop compacto com app bar, status bar, painéis 2x2, tema claro/escuro alternável, ícone do aplicativo embutido e paleta laranja/azul/grafite/cinza. O caminho do SQLite foi movido para Configurações Globais.

## 19. Revisão UX — SSH-first, detecção de target e host key opcional

### 19.1 Fluxo

1. O usuário configura e testa a conexão SSH (destino, jumps, autenticação, known_hosts ou opt-in inseguro).
2. O OPF lê `/etc/os-release` e `uname -m` no destino e normaliza SO, versão, arquitetura e família.
3. O preset recomendado preenche a imagem Docker e a configuração da sessão.
4. A configuração manual permanece disponível como **Avançado**, inclusive para o modo “Apenas baixar” sem SSH.
5. O pipeline usa os campos finais de target; a detecção não substitui a validação do ambiente isolado.

### 19.2 Presets e validação

| Alvo | Imagem | Observação |
|---|---|---|
| Ubuntu 22.04/24.04 | `ubuntu:<versao>` | Validado |
| Debian 12 | `debian:12` | Validado |
| Alpine 3.20 | `alpine:3.20` | Validado |
| CentOS 7 | `centos:7` | Validado; mirror `centos-store` e chaves RPM configurados em runtime |
| CentOS Stream 9 | `quay.io/centos/centos:stream9` | Validado |
| Rocky Linux 9 | `rockylinux:9` | Validado |
| AlmaLinux 9 | `almalinux:9` | Validado |
| Fedora 41 | `fedora:41` | Validado com DNF; `repoquery --resolve` não é usado no DNF/DNF5 |
| RHEL 9 | `registry.access.redhat.com/ubi9/ubi` | Validado via UBI |

Derivados RHEL habilitam EPEL automaticamente no ambiente isolado (incluindo fallback de `epel-release` no UBI) e reimportam as chaves RPM antes do P1. Pacotes como `htop` passam a resolver em Rocky/Alma/CentOS; no UBI, dependências ausentes do catálogo reduzido (como `hwloc`) ainda podem bloquear alguns pacotes EPEL. Nomes devem seguir a distro de destino (`mtr` em Rocky/RHEL, não `mtr-tiny`).

Para imagens RPM oficiais, o `DockerFetcher` instala `dnf-plugins-core`/`yum-utils` em runtime e importa as chaves RPM disponíveis. Alvos sem preset validado continuam permitidos pela configuração avançada, com aviso.

### 19.3 Host key

- Padrão: `known_hosts` obrigatório e estrito.
- Opt-in por sessão: `InsecureSkipHostKey` (equivalente a `StrictHostKeyChecking=no`), aplicado ao destino e a todos os jumps.
- GUI/TUI exibem checkbox com aviso vermelho; CLI usa `--ssh-insecure-skip-host-key`.
- A opção não é persistida por padrão e nunca deve ser ativada silenciosamente.

### 19.4 CLI

Com `--ssh-host` presente e `--target`/`--arch` omitidos, o CLI detecta o target, arquitetura e imagem recomendada. Flags explícitas continuam tendo precedência. `--target` e `--arch` permanecem obrigatórios sem SSH.

Cada ponte tem credencial própria. Flags repetíveis são alinhadas por índice com `--ssh-jump`:

| Flag | Obrigatória | Regra |
|:---|:---|:---|
| `--ssh-jump [usuario@]host[:porta]` | quando há ponte | Ordem dos saltos; porta omitida assume 22; usuário omitido usa o usuário do destino |
| `--ssh-jump-auth password\|key\|key+password` | sim, uma por ponte | Define a credencial exigida pelo hop |
| `--ssh-jump-password` | conforme o modo | Lista vazia ou com exatamente uma entrada por ponte; string vazia é placeholder |
| `--ssh-jump-key` | conforme o modo | Idem; caminho da chave privada do hop |
| `--ssh-jump-known-hosts` | não | Vazio herda `--ssh-known-hosts` |
| `OPF_SSH_JUMP_PASSWORDS` | não | Array JSON de strings, uma por ponte; conflita com `--ssh-jump-password` |

Exemplo com uma ponte por chave e outra por senha:

```bash
OPF_SSH_JUMP_PASSWORDS='["","senha-da-ponte-2"]' \
opf-cli --target ubuntu:22.04 --arch amd64 --pkg vim \
  --ssh-host alvo.interno --ssh-user admin --ssh-key ~/.ssh/id_ed25519 \
  --ssh-jump ponte1.interno:2200 \
  --ssh-jump-auth key --ssh-jump-key ~/.ssh/id_ed25519 \
  --ssh-jump admin@ponte2.interno \
  --ssh-jump-auth password --ssh-jump-key ""
```

A mudança é **breaking**: scripts que usam `--ssh-jump` sem `--ssh-jump-auth` passam a falhar com mensagem que cita a nova flag. Senhas de ponte nunca são gravadas em `config_json`, SQLite, relatório, logs ou mensagens de erro. A retomada repovoa apenas host, porta, usuário, modo, chave e `known_hosts`; as senhas precisam ser reinformadas. A senha de sudo continua sendo exclusivamente a senha da conta SSH do destino, nunca a senha de uma ponte.


### 19.5 Detecção obrigatória na GUI/TUI e relatório enriquecido

- GUI e TUI não iniciam uma sessão SSH usando valores padrão silenciosos. “Continuar” sem destino detectado dispara a detecção automaticamente; a configuração manual permanece disponível em **Avançado** e é aceita apenas após preenchimento explícito.
- A TUI mostra “Destino ainda não detectado” e usa `Enter`/`ctrl+d` para detectar; a GUI mostra o status de detecção e o mesmo fluxo de continuar.
- Após a execução, GUI e TUI renderizam o mesmo Markdown de `internal/observability`: seções de host remoto, destino/armazenamento, pacotes solicitados, tabela com papel (principal/dependente) e versão anterior, além de eventos e resultado.

### 19.6 Repositórios adicionais (somente download)

- A sessão aceita uma lista de repositórios adicionais usados **somente no ambiente isolado** (Docker/WSL). O destino recebe apenas os artefatos locais; nenhum repositório ou credencial é configurado no servidor remoto.
- A lista é **aditiva**: os repositórios oficiais continuam habilitados e não há pin/priority nem substituição de repositório.
- Famílias suportadas: APT (`sources.list.d`), DNF/YUM (`.repo`) e APK (`/etc/apk/repositories`). Cada item tem família, nome, URL, chave (arquivo local ou URL), CA opcional, opt-in inseguro de GPG e, em APT, suite/components ou modo flat.
- Sem autenticação nesta versão; URLs com `user:password@` são rejeitadas. Chave e CA são material público e podem ser persistidas; nenhum segredo de repositório é gravado.
- Verificação de assinatura é obrigatória por padrão. O opt-in inseguro de GPG usa `trusted=yes`, `gpgcheck=0` ou `apk fetch --allow-untrusted` e gera aviso. No APK, insecure TLS não é suportado; use CA customizada.
- Chaves RPM são importadas com `rpm --import`; chaves APT armored são convertidas com `gpg --dearmor`; chaves APK são copiadas para `/etc/apk/keys`. A CA é aplicada por repositório no APT/DNF e no trust store do APK.
- WSL é persistente: os arquivos de repositório/chave usam prefixo de sessão e são removidos no cleanup. Docker é efêmero.
- Configuração por sessão, persistida apenas como metadados em `config_json` (`repositories`). A retomada repovoa a lista; chave/CA ausentes falham com mensagem que cita o repositório.
- CLI: `--repo-config arquivo.json` para múltiplos repositórios ou `--repo-url` + `--repo-key`/`--repo-key-url`, `--repo-ca`, `--repo-family`, `--repo-apt-suite`, `--repo-apt-components`, `--repo-apt-flat`, `--repo-insecure-tls`, `--repo-insecure-gpg` para um único repositório. As duas formas são mutuamente exclusivas. Caminhos relativos no JSON são resolvidos contra o diretório do arquivo.
- Exemplo JSON:

```json
[
  {
    "name": "Interno RPM",
    "family": "rpm",
    "url": "https://repo.interno/el9/$basearch",
    "key_file": "/etc/pki/rpm-gpg/RPM-GPG-KEY-interno"
  },
  {
    "name": "Interno APT",
    "family": "apt",
    "url": "https://repo.interno/apt",
    "suite": "bookworm",
    "components": ["main"],
    "key_url": "https://repo.interno/apt/key.asc",
    "ca_file": "/etc/ssl/certs/interno-ca.pem"
  }
]
```

- Nomes de pacote seguem a distro de destino (`mtr` em Rocky/RHEL, não `mtr-tiny`). O repositório adicional não substitui o EPEL automático nem configura o destino para instalações futuras.


> **Precedência documental:** este adendo e a seção 0 de `Definições para GUI e TUI.md` substituem, em caso de conflito, os wireframes antigos que ainda mencionem NOPASSWD, bypass de host key, uma única ponte SSH, deleção física de histórico, Syslog na F6/F7 ou métricas fictícias de download.
