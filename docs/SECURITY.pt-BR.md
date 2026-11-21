# Relatório de Segurança — Offline Package Fetcher 0.2.1

**Português (BR)** | [English](SECURITY.md)

| Campo | Valor |
|:---|:---|
| Projeto | Offline Package Fetcher (OPF) |
| Versão | **0.2.1** |
| Repositório | <https://github.com/ph7ti/Offline-Package-Fetcher> |
| Data do relatório | **2026-11-21** (America/Sao_Paulo) |
| Testes executados em | **2026-11-21** |
| Responsável pelo escopo | Engenheiro de segurança de aplicação (equipe de release) |
| Situação | Suíte de segurança executada + varreduras estáticas/dinâmicas, achados triados |

Este documento registra o trabalho de segurança realizado para a release pública v0.2.1
(2026-11-21): o modelo de ameaças, a matriz de controles/testes, as ferramentas e os comandos
realmente executados, os caminhos das evidências, os achados e o status de remediação, além dos
riscos residuais aceitos para esta release. A suíte e as varreduras foram reexecutadas na árvore
final da 0.2.1; o histórico de remediação de dependências da 0.1.0 está preservado no
[§5.3](#53-govulncheck) e no [§6](#6-achados). Nada aqui é teórico: cada controle mapeado abaixo
é respaldado por um teste ou varredura executada. Os comandos e as saídas resumidas estão
reproduzidos no [§5](#5-resultados-e-evidências-de-execução) e podem ser reexecutados com
`scripts/security/scan.ps1` (Windows) ou `scripts/security/scan.sh` (Linux/macOS).

**Nota de empacotamento da 0.2.1:** esta release altera apenas o empacotamento — CLI/TUI Windows
agora usam UPX e CLI/TUI macOS são publicados como `.tar.gz`. O código Go funcional é idêntico ao da 0.2.0 (apenas o banner de versão da TUI mudou);
a suíte e as varreduras abaixo foram reconfirmadas na árvore da 0.2.1.

---

## 1. Escopo e modelo de ameaças

### 1.1 Dentro do escopo

- Código Go em `internal/**`, `cmd/**` e `ui/**` que trata entrada não confiável, credenciais,
  construção de comandos remotos, integridade de artefatos e persistência.
- As superfícies da release que consomem valores não confiáveis: pedidos de pacotes, definições
  de repositório, definições de SSH/pontes, saída dos gerenciadores de pacotes, nomes de
  artefatos, entradas de manifesto e texto de eventos/relatórios.
- A suíte de segurança em `test/security/**` e os testes white-box estreitos listados no
  [§4.3](#43-inventário-de-testes).
- A superfície de idioma da interface adicionada na 0.2.0: o ajuste persistido `language`, a
  variável `OPF_LANG`, a flag `--lang` da CLI e os catálogos de tradução embutidos nos binários.

### 1.2 Fora do escopo

- Exploração ao vivo contra servidores SSH, ambientes Docker/WSL ou repositórios reais. A suíte
  usa dublês (fakes), diretórios temporários e segredos fictícios para ser determinística e
  rodar em CI sem infraestrutura.
- Segurança de runtime de terceiros (daemon Docker, kernel do WSL, servidor OpenSSH, chaves de
  assinatura das distribuições), exceto onde o OPF define uma política para eles.
- Internos de renderização da GUI (Fyne/OpenGL) além da varredura de dependências.
- Cadeia de suprimentos de build/release além das ferramentas listadas aqui (builds
  reproduzíveis, assinatura e atestação de proveniência são tratados à parte pelo responsável
  pela release).

### 1.3 Ativos

1. Credenciais SSH (caminho da chave privada, senha SSH, senha do sudo, senhas das pontes).
2. O host de destino remoto: o OPF executa a instalação de pacotes por SSH.
3. Artefatos baixados e sua integridade (manifesto SHA-256, três pontos de verificação).
4. Banco SQLite local, relatórios de sessão e eventos opcionais de syslog.
5. O ambiente isolado de download (contêiner Docker ou distro WSL).
6. A preferência persistida de idioma da interface (não secreta) e os catálogos de tradução
   embutidos nos binários.

### 1.4 Fronteiras de confiança e capacidade do atacante

| Fronteira | Entrada não confiável | Capacidade modelada do atacante |
|:---|:---|:---|
| Operador → CLI/TUI/GUI | nomes de pacotes, URLs de repositório, campos de SSH/pontes, caminhos | Entrada maliciosa ou descuidada do operador; argumentos errados/automatizados |
| Gerenciador de pacotes → OPF | saída de `apt-cache`, `repoquery`, `apk fetch`, `dpkg-query`, `rpm -q`, `apk info` | Metadados de repositório/pacote hostis tentando injetar nomes ou linhas extras |
| Destino remoto → OPF | `/etc/os-release`, `uname`, saída de inventário, host key SSH | Host de destino comprometido tentando quebrar o parser ou se passar por outro host |
| Arquivos de artefato → OPF | nomes/conteúdo de artefatos, entradas do `SHA256SUMS` | Árvore de artefatos adulterada, travessia de caminho, nomes duplicados, symlinks |
| OPF → shell remoto | scripts de instalação, verificação de checksums, comandos de pré-check | Metacaracteres de shell em qualquer valor interpolado |
| OPF → armazenamento local | linhas do SQLite, JSON de configuração, relatórios, syslog | Injeção de SQL, vazamento de segredos em dados persistidos/exportados |
| Idioma da interface → OPF | ajuste `language` em `config.json`, `OPF_LANG`, `--lang` | Valor hostil tentando selecionar idioma não suportado, injetar caracteres de controle/caminhos no `config.json` ou contrabandear payloads de shell pelo ajuste |

### 1.5 Objetivos de segurança

- **O1** Nenhum valor controlado pelo usuário/repositório/destino pode executar comandos de
  shell fora da operação remota pretendida.
- **O2** Segredos nunca aparecem em comandos gerados, configuração de sessão persistida,
  relatórios ou logs.
- **O3** A integridade dos artefatos é verificada e qualquer adulteração é fatal.
- **O4** A identidade do host é verificada estritamente por padrão; modos inseguros são opt-ins
  explícitos.
- **O5** Dados textuais não confiáveis não podem corromper o banco nem a máquina de estados dos
  parsers.
- **O6** Apenas `auto`, `en`, `pt-BR` e `es` são aceitos como idioma da interface; valores
  desconhecidos ou hostis caem em fallback seguro, e strings traduzidas tratam dados do usuário
  como dados.

---

## 2. Metodologia

1. **Modelagem de ameaças** de cada fronteira de confiança (acima), derivada do pipeline
   documentado (13 etapas) e da superfície pública CLI/TUI/GUI.
2. **Testes comportamentais** contra APIs exportadas (`installer`, `transfer`, `resolver`,
   `inspector`, `store`, `config`, `app`, `observability`). Os testes verificam saída observável:
   texto de shell gerado, estado do SQLite, erros retornados, valores parseados e bytes emitidos.
3. **Testes white-box estreitos** onde o controle de segurança vive em função não exportada e
   nenhuma API exportada a expõe: `shellQuote`, `wslShellQuote`/`wslShellJoin`,
   `wrapSudoCommand`, `buildSSHClientConfig`, `validateRemoteDir`, `configJSON`, internos dos
   parsers e enquadramento do syslog.
4. **Análise estática**: gosec (gate de CI + inventário completo), govulncheck
   (vulnerabilidades alcançáveis), gitleaks (segredos na árvore de trabalho), staticcheck
   (informativo) e `go vet`.
5. **Execução multiplataforma**: a suíte foi executada no Windows (plataforma principal de
   desenvolvimento, incluindo `CGO_ENABLED=1` para os testes da GUI) e em um contêiner Linux
   `golang:1.27.4` com `OPF_LANG=pt-BR CGO_ENABLED=0` na árvore final da 0.2.1, para detectar
   suposições específicas de plataforma nos testes de caminho/permissão/symlink. A árvore final
   da 0.2.1 também foi verificada com o detector de corrida
   (`CGO_ENABLED=1 OPF_LANG=pt-BR go test -race -count=1 ./test/security/...` → `ok`, exit 0).
6. **Relato honesto**: todo resultado de ferramenta abaixo foi copiado de uma execução real.
   Achados intencionais são anotados no código (`#nosec`, `gitleaks:allow`) e documentados aqui;
   nada foi silenciado sem justificativa escrita.

As limitações estão no [§8](#8-limitações).

---

## 3. Matriz de controles e testes

Legenda: **PASS** = o teste verifica o controle e passou em 2026-11-21 no Windows e no Linux
(quando o teste é multiplataforma). Os nomes dos testes são exatamente os reportados por
`go test -v`.

### 3.1 Injeção de comandos e quoting de shell

| # | Controle | Teste(s) | Resultado |
|:---|:---|:---|:---|
| C1.1 | Instaladores `apt`/`rpm`/`apk` colocam cada nome de artefato entre aspas simples POSIX; payloads de injeção (`;`, `|`, `&`, `$()`, crases, aspas, espaços, tabs) nunca aparecem fora de aspas | `TestInstallerCommandInjection` (apt/rpm/apk) | PASS |
| C1.2 | O diretório remoto da sessão é citado em scripts `cd '...'` | `TestInstallerRemoteDirInjection` | PASS |
| C1.3 | Nomes que não podem ser tratados com segurança (NUL, CR/LF, sufixo errado, duplicados, caminho vazio) são rejeitados antes de montar o comando | `TestInstallerRejectsUnsafeArtifactNames` | PASS |
| C1.4 | O diretório remoto deve ser absoluto e sem NUL/CR/LF | `TestSecurityValidateRemoteDir` (installer) | PASS |
| C1.5 | `shellQuote` fecha/escapa/reabre aspas simples corretamente | `TestSecurityShellQuote` (installer e transfer), `TestSecurityShellQuoteNeutralizes` | PASS |
| C1.6 | A junção de comandos do WSL cita cada argumento | `TestSecurityWSLShellQuote`, `TestSecurityWSLShellJoin` (build tag Windows) | PASS |
| C1.7 | A senha do sudo nunca aparece no comando; `sudo -S -p '' sh -c` lê a senha do stdin | `TestSudoPasswordNeverInCommand`, `TestSudoPasswordGoesThroughStdin` (apt/rpm/apk) | PASS |
| C1.8 | Senha com CR/LF/NUL é rejeitada (não injeta no stdin do sudo) | `TestSudoPasswordRejectsControlCharacters`, `TestSecurityWrapSudoCommand` | PASS |

### 3.2 Validação de entrada

| # | Controle | Teste(s) | Resultado |
|:---|:---|:---|:---|
| C2.1 | Resolvers rejeitam nomes hostis (`vim;id`, `$(id)`, crases, espaços, quebras de linha, `../etc/passwd`, `--help`, NUL) **antes de qualquer chamada ao executor** | `TestResolversRejectInjectedPackageNames` (apt/rpm/apk) | PASS |
| C2.2 | Arquitetura e diretório isolado são validados antes da execução | `TestResolversRejectInjectedArchitectureAndWorkDir` | PASS |
| C2.3 | Controle positivo: nomes legítimos continuam resolvendo | `TestResolversAcceptValidNames` | PASS |
| C2.4 | URLs de repositório rejeitam credenciais na URL, esquemas diferentes de HTTP(S), caracteres de controle, fragmentos, host ausente, `$basearch` em APT, opt-ins TLS/GPG conflitantes, slugs duplicados, injeção em suite/component | `TestRepositoryURLValidation`, `TestDuplicateRepositorySlugRejected` | PASS |
| C2.5 | A arquitetura detectada rejeita valores de injeção; distro desconhecida é rejeitada | `TestTargetArchValidation` | PASS |
| C2.6 | Pontes exigem host/usuário/modo de autenticação e porta numérica no intervalo; erros nunca incluem a senha | `TestSSHJumpInputValidation` | PASS |
| C2.7 | Hosts de ponte/destino com CR/LF/NUL são rejeitados antes de qualquer discagem TCP; pontes sem credenciais são rejeitadas | `TestSSHJumpControlCharactersRejectedBeforeDial`, `TestSSHJumpRequiresCredentials`, `TestSecuritySSHConfigRejectsControlChars` | PASS |
| C2.8 | O pré-check remoto valida nomes antes de montar o comando; linhas malformadas de inventário são descartadas | `TestInspectorRejectsInjectedPackageNames`, `TestInspectorCommandIsQuotedAndOutputIsParsed`, `TestSecurityParseInstalledRejectsMalformedLines` | PASS |
| C2.9 | A whitelist de tag/protocolo do syslog bloqueia injeção de cabeçalho | `TestSyslogConfigInjection`, `TestSecuritySyslogTagPattern` | PASS |

### 3.3 Injeção de SQL

| # | Controle | Teste(s) | Resultado |
|:---|:---|:---|:---|
| C3.1 | Payloads SQL em nomes de pacotes, versões, campos de sessão, texto/detalhes de eventos e `pacotes_solicitados` são armazenados literalmente; schema/tabelas permanecem intactos | `TestSQLInjectionStore` | PASS |
| C3.2 | Cláusulas `SET` dinâmicas (metadados de verificação) ligam valores como parâmetros; payloads armazenados literalmente | `TestSQLInjectionStore` (fase de atualização) | PASS |
| C3.3 | Status fora do enum é rejeitado antes de o SQL executar | `TestStoreRejectsInvalidStatusDespiteInjection` | PASS |

### 3.4 Travessia de caminho e abuso de symlink

| # | Controle | Teste(s) | Resultado |
|:---|:---|:---|:---|
| C4.1 | Entradas de manifesto com `../`, caminhos absolutos/aninhados, duplicatas ou campos extras são rejeitadas | `TestManifestRejectsTraversalAndMalformedEntries` | PASS |
| C4.2 | A verificação do manifesto rejeita symlinks para arquivos não regulares | `TestManifestRejectsSymlinkTraversal` (skip apenas onde o SO proíbe symlink) | PASS |
| C4.3 | Entradas de manifesto usam somente `filepath.Base`; basenames duplicados são rejeitados | `TestManifestEntriesUseBasename` | PASS |
| C4.4 | Caminhos remotos rejeitam NUL/CR/LF | `TestSecurityValidateRemotePath` (transfer) | PASS |
| C4.5 | Artefatos não regulares/vazios/ausentes são rejeitados antes de publicar o manifesto | `TestGerarManifestoRejectsUnsafeArtifacts` | PASS |

### 3.5 Integridade

| # | Controle | Teste(s) | Resultado |
|:---|:---|:---|:---|
| C5.1 | Qualquer mudança de conteúdo após gerar o manifesto é detectada (reescrita de mesmo tamanho e truncamento) | `TestManifestTamperDetected` | PASS |
| C5.2 | Manifestos vazios/malformados e divergências de digest são fatais | `TestManifestRejectsTraversalAndMalformedEntries`, `TestManifestTamperDetected` | PASS |
| C5.3 | A comparação de digests usa `crypto/subtle.ConstantTimeCompare` (guarda de código-fonte + teste comportamental de divergência) | `TestManifestComparisonUsesConstantTime`, `TestManifestTamperDetected` | PASS |

### 3.6 Tratamento de segredos

| # | Controle | Teste(s) | Resultado |
|:---|:---|:---|:---|
| C6.1 | `sessions.config_json` (persistido e exportado) não contém senha de SSH/ponte/sudo nem material de chave; metadados de ponte guardam somente o modo de autenticação não secreto | `TestSecurityConfigJSONExcludesSecrets`, `TestSecurityNewSessionConfigJSONExcludesSecrets` | PASS |
| C6.2 | O código de pipeline/eventos não tem superfície de credenciais (guarda de regressão) | `TestSecurityPipelineHasNoPasswordSurface` | PASS |
| C6.3 | Configurações globais não têm campos secretos; o arquivo salvo não tem marcadores de segredo e é `0600` em POSIX | `TestSettingsNeverPersistSecrets`, `TestConfigLoadAcceptsLoosePermissions` | PASS |
| C6.4 | Metadados/fingerprint de ponte excluem a senha; o salto em memória continua autenticando | `TestSSHJumpMetadataAndFingerprintExcludePasswords`, `TestSSHJumpBuildKeepsSecretsInMemoryOnly` | PASS |
| C6.5 | A senha do sudo trafega somente pelo stdin | `TestSudoPasswordGoesThroughStdin`, `TestSudoPasswordNeverInCommand` | PASS |

### 3.7 Política de host key SSH

| # | Controle | Teste(s) | Resultado |
|:---|:---|:---|:---|
| C7.1 | Estrito por padrão: `known_hosts` vazio rejeita chaves desconhecidas; entrada correspondente é aceita; hostname/porta divergentes e chave MITM são rejeitados; arquivo `known_hosts` ausente é erro | `TestSecurityHostKeyPolicyStrictByDefault` | PASS |
| C7.2 | Somente `InsecureSkipHostKey=true` desativa a verificação (opt-in explícito) | `TestSecurityInsecureSkipHostKeyIsExplicitOnly` | PASS |
| C7.3 | A validação de host rejeita caracteres de controle | `TestSecuritySSHConfigRejectsControlChars` | PASS |

### 3.8 Parsing de saídas de dependências/monitoração

| # | Controle | Teste(s) | Resultado |
|:---|:---|:---|:---|
| C8.1 | Linhas inesperadas do `apt-cache` abortam a resolução; linhas de injeção são rejeitadas | `TestSecurityAptCacheOutputRejection` | PASS |
| C8.2 | Saída do `repoquery` é filtrada pela whitelist de nomes; resultado vazio é fatal | `TestSecurityRepoqueryOutputRejection` | PASS |
| C8.3 | Saída do `apk fetch --simulate` não pode contrabandear payloads de travessia/shell | `TestSecurityApkSimulateOutputRejection` | PASS |
| C8.4 | Linhas de inventário com contagem de campos errada/estado não instalado são descartadas; valores são tratados como dados (nenhum comando é montado a partir deles) | `TestSecurityParseInstalledRejectsMalformedLines`, `TestSecurityParseInstalledTreatsNamesAsData`, `TestSecuritySplitAPKVersionStructuralValidation` | PASS |
| C8.5 | Mensagens de syslog com quebras de linha/CR/NUL/ANSI são escapadas em JSON, em uma única linha RFC 5424, e fazem round-trip inalterado | `TestSecuritySyslogFormatSanitizesMessage` | PASS |

### 3.9 Controles de ferramentas

| # | Controle | Evidência | Resultado |
|:---|:---|:---|:---|
| C9.1 | A suíte de segurança está limpa sob o detector de corrida na árvore final da 0.2.1 | `CGO_ENABLED=1 OPF_LANG=pt-BR go test -race -count=1 ./test/security/...` → `ok opf/test/security`, exit 0 (Linux) | PASS |
| C9.2 | Suíte completa de regressão | `go test -count=1 ./...` (todos os pacotes) | PASS |
| C9.3 | Gate de análise estática de severidade/confiança altas | `gosec -severity high -confidence high ./...` → 0 issues | PASS |
| C9.4 | Nenhum segredo na árvore de trabalho | `gitleaks dir . --no-banner --redact` → no leaks | PASS |
| C9.5 | Nenhuma vulnerabilidade conhecida alcançável | `govulncheck -show verbose ./...` → 0 alcançáveis | PASS |
| C9.6 | Vet/formatação/lint de CI | `go vet ./...`, `gofmt -l`, `actionlint` → limpos | PASS |

### 3.10 Idioma da interface (i18n)

| # | Controle | Teste(s) | Resultado |
|:---|:---|:---|:---|
| C10.1 | O ajuste persistido `language` aceita apenas `auto`, `en`, `pt-BR` ou `es` (vazio equivale a `auto`); travessia de caminho, CR/LF, NUL, payloads de shell e HTML são rejeitados antes de gravar o `config.json` | `TestLanguageSettingRejectsInjection` | PASS |
| C10.2 | A resolução de idioma nunca escapa do conjunto suportado e nunca entra em pânico com valores desconhecidos ou hostis (`fr`, `de-DE`, strings de 4 KiB, NUL); inglês é o fallback | `TestLanguageResolutionIsSafe` | PASS |
| C10.3 | Strings traduzidas preservam os verbos de formatação e tratam dados do usuário como dados; nenhuma string traduzida é reinterpretada como código | `TestTranslationFormattingHandlesUserData` | PASS |
| C10.4 | A falha de tradução do Fyne está corrigida: as traduções ausentes de "(Select one)" em pt-BR/es e os placeholders localizados em todos os `Select` estão presentes; o stderr da GUI fica vazio nas execuções em EN e ES | Verificação manual da GUI (EN/ES), Fyne v2.8 | PASS |

---

## 4. Ferramentas, versões e inventário de testes

### 4.1 Versões das ferramentas (verificadas em 2026-11-21)

| Ferramenta | Versão | Observações |
|:---|:---|:---|
| Go | `go1.27.4 windows/amd64` | Execução principal; execução Linux em contêiner |
| gcc / CGO | MinGW-W64 UCRT `gcc.exe 16.2.0` | Necessário para `-race` no Windows |
| Docker | `29.9.2` (cliente) | Usado para executar `golang:1.27.4 linux/amd64` |
| gitleaks | `8.30.1` (binário de release) | Um binário compilado do código-fonte também foi usado pelo Lead; ele imprime `version is set by build process` e produziu o mesmo resultado (0 vazamentos) |
| govulncheck | `v1.10.0`, DB `https://vuln.go.dev` | `govulncheck -version`; a execução da 0.2.1 usou um banco atualizado em que o item não alcançável anterior de `x/image` não é mais reportado |
| gosec | módulo `github.com/securego/gosec/v2 v2.30.0` | `gosec -version` imprime `Version: dev` (build do código-fonte); versão do módulo lida com `go version -m` |
| staticcheck | `2026.2.2 (0.8.2)` | Informativo |
| actionlint | `v1.7.12` | Lint dos workflows |
| go vet | incluído no Go 1.27.4 | — |

### 4.2 Comandos executados (exatos)

```text
go test -race -count=1 ./test/security/...
go test -race -count=1 -run 'TestSecurity' ./internal/...
go test -count=1 ./...
go vet ./...
gofmt -l .
gitleaks dir . --no-banner --redact
govulncheck -show verbose ./...
gosec -severity high -confidence high ./...
gosec -severity medium -confidence medium -fmt json -out bin/security/gosec-medium.json ./...
staticcheck ./...
actionlint
```

Docker (semântica Linux):

```text
docker run --rm -v "<repo>:/src" -w /src -e CGO_ENABLED=1 \
  -e GOCACHE=/tmp/gocache -e GOMODCACHE=/tmp/gomodcache golang:1.27.4 \
  sh -c "go test -count=1 ./test/security/... ./internal/... && \
         go test -race -count=1 ./test/security/..."
```

### 4.3 Inventário de testes

| Local | Testes de nível superior | Conteúdo |
|:---|:---:|:---|
| `test/security/**` | 33 | Suíte do modelo de ameaças (este documento), incluindo os testes de idioma da 0.2.1 |
| `internal/app/security_test.go` | 3 | Exclusão de segredos no `configJSON`, guarda do pipeline |
| `internal/transfer/security_test.go` | 5 | Rigor de host key, quoting, caminho remoto |
| `internal/installer/security_test.go` | 4 | Quoting, wrapper de sudo, diretório remoto |
| `internal/observability/security_test.go` | 2 | Enquadramento/tag do syslog |
| `internal/resolver/security_test.go` | 4 | Parsing de apt-cache/repoquery/apk |
| `internal/inspector/security_test.go` | 3 | Parsing de inventário |
| `internal/fetcher/security_windows_test.go` | 2 | `wslShellQuote`/`wslShellJoin` (somente Windows) |
| **Total** | **56** | Executados em 2026-11-21 |

### 4.4 Testes condicionais por plataforma

| Teste | Condição | Por que é seguro |
|:---|:---|:---|
| `TestSecurityWSLShellQuote`, `TestSecurityWSLShellJoin` | `//go:build windows` | Exercitam `internal/fetcher/wsl_windows.go`, que só compila no Windows. |
| `TestManifestRejectsSymlinkTraversal` | `t.Skip` se `os.Symlink` não estiver disponível | Windows sem privilégio de symlink não cria o fixture; no Linux o teste roda. |
| `TestSettingsNeverPersistSecrets` (verificação de modo) | Somente POSIX (`runtime.GOOS != "windows"`) | Windows não tem bits de permissão POSIX; as verificações de conteúdo rodam em todos. |
| `TestConfigLoadAcceptsLoosePermissions` | `chmod` é no-op no Windows | Verifica round-trip de conteúdo; o endurecimento de permissão é verificado no ramo POSIX acima. |

Todos os demais testes são multiplataforma. A execução em contêiner Linux na árvore final de
dependências passou `./test/security/...`, `./internal/...` e `-race ./test/security/...`,
incluindo o teste de symlink que é pulado no Windows sem privilégio.

---

## 5. Resultados e evidências de execução

Os artefatos brutos são gravados em `bin/security/` (ignorado pelo git) por
`scripts/security/scan.*`.

### 5.1 Suítes de teste

Execuções finais de 2026-11-21 na árvore da 0.2.1:

- **Contêiner Linux** (`golang:1.27.4 linux/amd64`, `OPF_LANG=pt-BR CGO_ENABLED=0`):
  `go test ./...` passou em todos os pacotes; `go vet ./...` saiu com 0 e `gofmt -l .` não
  produziu saída. Rodar com `CGO_ENABLED=0` também confirma que CLI/TUI/core seguem livres de
  CGO.
- **Windows** (plataforma principal): `go test ./...` passou em todos os pacotes e os testes da
  GUI com `CGO_ENABLED=1` passaram. O wrapper da execução completa local sai com 1 apenas depois
  da suíte, porque o antivírus trava um arquivo temporário que o runner tenta remover; nenhum
  teste falhou e todos os resultados de pacote são `ok`.
- **Detector de corrida**: `CGO_ENABLED=1 OPF_LANG=pt-BR go test -race -count=1
  ./test/security/...` → `ok opf/test/security` (exit 0) na árvore final da 0.2.1, além
  da execução completa sem `-race` acima.

### 5.2 gitleaks

A primeira execução contra a árvore de trabalho reportou um achado, triado como **falso
positivo**:

```text
File: internal/app/sshjumps_test.go:179
Rule: generic-api-key
Match: Password: "REDACTED"      (valor fictício "jump2-segredo")
```

Evidência: o valor é um dummy fixo usado por um teste unitário existente para provar que
senhas não são persistidas; não é credencial real e o arquivo contém apenas fixtures de teste.
O Lead corrigiu na origem com uma anotação inline naquela linha:

```go
{Host: "jump2", ..., Password: "jump2-segredo"}, // gitleaks:allow -- dummy fixture password
```

Verificação final na árvore da 0.2.1:

```text
$ gitleaks dir . --no-banner --redact
INF no leaks found
exit 0
```

Nenhum arquivo de allowlist é usado; a varredura é o comando puro da árvore de trabalho,
equivalente ao que a action do gitleaks executa na CI.

### 5.3 govulncheck

**Evidência pré-remediação** (antes das atualizações de dependência, na árvore da época):

```text
Your code is affected by 7 vulnerabilities from 4 modules.
```

| Advisory | Módulo (encontrado → corrigido) | Alcance (pré-correção) |
|:---|:---|:---|
| GO-2026-6865 | `github.com/klauspost/compress` v1.18.0 → v1.20.2 | alcançável (init de compressão / caminhos de arquivo) |
| GO-2026-6253 | `github.com/moby/go-archive` v0.1.0 → v0.3.0 | alcançável (cópia/arquivo Docker) |
| GO-2026-5506 | `go.opentelemetry.io/otel` v1.38.0 → v1.41.0 | alcançável (tratamento de baggage OTel) |
| GO-2026-5066 | `golang.org/x/image` v0.24.0 → v0.43.0 | alcançável via `ui/gui/ui.go:102` (`SetIcon` → `tiff.Decode`) |
| GO-2026-5062 | `golang.org/x/image` v0.24.0 → v0.43.0 | mesmo caminho do ícone da GUI |
| GO-2026-5032 | `golang.org/x/image` v0.24.0 → v0.41.0 | mesmo caminho do ícone da GUI |
| GO-2026-4815 | `golang.org/x/image` v0.24.0 → v0.38.0 | mesmo caminho do ícone da GUI (mais um trace reportado em `checksum.go:41`) |

**Estado final** (dependências atualizadas pelo Lead na árvore de release, re-verificadas por
esta tarefa):

```text
go.mod: github.com/klauspost/compress v1.20.2, github.com/moby/go-archive v0.3.0,
        go.opentelemetry.io/otel v1.41.0, golang.org/x/image v0.43.0

$ govulncheck -show verbose ./...
=== Symbol Results ===
No vulnerabilities found.
...
Your code is affected by 0 vulnerabilities.
This scan also found 1 vulnerability in packages you import and 1 vulnerability
in modules you require, but your code doesn't appear to call these vulnerabilities.
exit 0
```

Item não alcançável reportado pela mesma varredura (documentado como residual, sem caminho de
código):

| Advisory | Módulo | Situação |
|:---|:---|:---|
| GO-2026-5158 (limite de tamanho de header de baggage) | `go.opentelemetry.io/otel` v1.41.0, corrigido em v1.42.0 | Somente nível de pacote; não chamado pelo OPF (`Symbol Results: none`). Candidato a upgrade futuro. |

Assim, todos os sete advisories que eram alcançáveis antes da release estão **remediados na
v0.2.1**, e o único advisory restante é **não alcançável** segundo a análise de símbolos do
govulncheck. `go test ./...` passou após cada etapa de upgrade e na árvore final.

### 5.4 gosec

Gate de severidade/confiança altas (mesmo comando de `.github/workflows/ci.yml`):

```text
$ gosec -severity high -confidence high ./...
Summary:
  Gosec  : dev
  Nosec  : 1
  Issues : 0
exit 0
```

O único `#nosec` é a anotação auditável do opt-in intencional de TLS de repositório:

```go
// internal/fetcher/repositories.go:359
// #nosec G402 -- explicit, documented opt-in (--repo-insecure-tls); see docs/SECURITY.md
tlsConfig.InsecureSkipVerify = true
```

O inventário completo em severidade/confiança médias foi revisado; seus itens estão triados no
§6. O gosec sai com código 1 nessa execução de inventário por projeto; ela não é um gate de
release.

### 5.5 staticcheck, go vet, actionlint

```text
$ go vet ./...          exit 0
$ gofmt -l .            (sem saída)
$ actionlint            exit 0

$ staticcheck ./...
internal/core/pipeline.go:325:2: this value of manifestPath is never used (SA4006)
internal/core/pipeline_test.go:275:2: field progress is unused (U1000)
internal/resolver/apk.go:121:3: should replace this if statement with an unconditional strings.TrimSuffix (S1017)
```

Os três não bloqueiam a release e não são de segurança: uma atribuição não usada, um campo morto
de teste e uma sugestão de estilo. São reportados por transparência e não foram alterados para
evitar refatoração fora do escopo; o staticcheck é informativo e não é gate de release.

---

## 6. Achados

Nenhuma vulnerabilidade explorável foi encontrada no código do OPF. Os achados abaixo são os
resultados das ferramentas e suas disposições.

| ID | Severidade | Área | Evidência | Situação |
|:---|:---|:---|:---|:---|
| SEC-01 | Média (scanner: ALTA/ALTA) | Opt-in de verificação TLS (`--repo-insecure-tls`) | gosec G402 em `internal/fetcher/repositories.go:359` | **Aceito, anotado.** Opt-in intencional por repositório, registrado como warning, exige CA ou flag explícita; documentado no §7. `#nosec G402` com justificativa; gate high/high sai 0. |
| SEC-02 | Info | Varredura de segredos | gitleaks `generic-api-key` em `internal/app/sshjumps_test.go:179` | **Corrigido (falso positivo).** Senha dummy de fixture; triado e anotado `// gitleaks:allow`. Varredura final: 0 vazamentos. |
| SEC-03 | Crítica→nenhuma | Dependências | GO-2026-6865 `klauspost/compress` v1.18.0 | **Remediado**: v1.20.2; govulncheck 0 alcançáveis. |
| SEC-04 | Alta→nenhuma | Dependências | GO-2026-6253 `moby/go-archive` v0.1.0 | **Remediado**: v0.3.0; govulncheck 0 alcançáveis. |
| SEC-05 | Alta→nenhuma | Dependências | GO-2026-5506 `otel` v1.38.0 | **Remediado**: v1.41.0; govulncheck 0 alcançáveis. |
| SEC-06 | Alta→nenhuma | Dependências | GO-2026-5066 `x/image` | **Remediado**: v0.43.0; govulncheck 0 alcançáveis. |
| SEC-07 | Alta→nenhuma | Dependências | GO-2026-5062 `x/image` | **Remediado**: v0.43.0; govulncheck 0 alcançáveis. |
| SEC-08 | Média→nenhuma | Dependências | GO-2026-5032 `x/image` | **Remediado**: v0.43.0; govulncheck 0 alcançáveis. |
| SEC-09 | Alta→nenhuma | Dependências | GO-2026-4815 `x/image` | **Remediado**: v0.43.0; govulncheck 0 alcançáveis. |
| SEC-10 | Baixa | Dependências (não alcançáveis) | GO-2026-5158 (`otel` 1.41.0 → 1.42.0) | **Residual aceito**: pacote presente, mas nenhum símbolo alcançável; upgrade em release futura. |
| SEC-11 | Média (scanner) | Bypass de host key SSH (`InsecureIgnoreHostKey`) | G106 `internal/transfer/ssh.go:379` | **Aceito**: alcançável apenas por `--ssh-insecure-skip-host-key`/`InsecureSkipHostKey=true`; `known_hosts` estrito é o padrão e é coberto por C7.1. |
| SEC-12 | Média (scanner) | Subprocesso com argumentos variáveis | G204 `internal/fetcher/wsl_windows.go:364,390` | **Aceito**: `exec.CommandContext("wsl.exe", args...)` sem shell; argumentos validados/citados por `wslShellJoin`; coberto por C1.6. |
| SEC-13 | Média (scanner) | Concatenação de string SQL | G202 `internal/store/sqlite.go:810` | **Aceito**: cláusula `SET` dinâmica montada apenas com nomes fixos de colunas internas; todos os valores ligados como parâmetros; coberto por C3.2. |
| SEC-14 | Média (scanner) | Caminhos de arquivo por variável | G304 ×10 (`transfer`, `checksum`, `fetcher`, `core`, `config`, `cmd`) | **Aceito**: caminhos vêm de configuração/estado de sessão do operador, não de saída remota; nomes relevantes para travessia são validados (C4.x). |
| SEC-15 | Média (scanner) | Permissões de arquivo/diretório | G301 ×3 (diretórios `0755`), G302 ×1 (manifesto `0644`) | **Residual aceito**: artefatos/banco locais podem ser legíveis por outros usuários locais em hosts multiusuário; ver §7. |
| SEC-16 | Alta/confiança média | Conversão de inteiro | G115 ×3 `ui/gui/theme.go:210-212` | **Aceito**: aritmética de componentes de cor sobre valores internos da paleta; nenhuma entrada numérica não confiável chega a essas conversões. |

### 6.1 Observações adicionais (não são achados de segurança)

| ID | Observação | Impacto | Recomendação |
|:---|:---|:---|:---|
| OBS-01 | Verificado na revisão do Lead como **observação falsa**: em `^[A-Za-z0-9][A-Za-z0-9+._:-]*$` o `-` é o último caractere da classe, portanto é hífen literal. `ca-certificates`, `py3-pip` e `gcc-c++` casam com o padrão (confirmado com `regexp` do Go e regex .NET). O pré-check remoto não os rejeita. | Nenhum — não há defeito funcional. | Nenhuma; não é necessário alterar o código. |
| OBS-02 | Três achados informativos do staticcheck (§5.5). | Nenhum. | Limpeza opcional. |

---

## 7. Riscos residuais e aceitos

Os riscos abaixo são aceitos para a v0.2.1. Cada um é explícito, documentado e, quando possível,
mitigado por uma alternativa mais segura que o operador pode escolher.

| ID | Risco | Por que é aceito | Mitigação disponível |
|:---|:---|:---|:---|
| R1 | `--ssh-password` / `--ssh-jump-password` colocam segredos no argv do processo, visíveis em listagens de processos locais e no histórico do shell. | Compatibilidade de CLI para uso não interativo; mudar agora quebraria automações existentes. | Prefira chaves SSH; use as variáveis `OPF_SSH_PASSWORD` / `OPF_SSH_JUMP_PASSWORDS`, suportadas e recomendadas no help do CLI. |
| R2 | `--repo-insecure-tls`, `--repo-insecure-gpg`, `--ssh-insecure-skip-host-key` enfraquecem transporte ou confiança. | Necessários para repositórios isolados/laboratório e hosts com certificado próprio; bloqueá-los removeria cenários suportados. | Todos são opt-ins explícitos, registrados como warning e mutuamente exclusivos com a alternativa segura (CA/chave/host key) quando aplicável. A anotação SEC-01 registra a decisão de TLS. |
| R3 | Banco SQLite, diretório de saída e `SHA256SUMS` usam permissões `0644`/`0755`. | A ferramenta costuma ser executada por um único operador; forçar `0600`/`0700` pode quebrar diretórios de saída compartilhados. | Execute o OPF em conta/diretório dedicado com umask restritivo; endurecimento é candidato para a v0.3.0. |
| R4 | URLs HTTP de repositório são permitidas (a assinatura GPG continua obrigatória por padrão). | Espelhos air-gapped frequentemente são somente HTTP. | Use HTTPS quando possível; mantenha `--repo-insecure-gpg` desligado; opt-ins de CA/TLS são validados contra URLs HTTP. |
| R5 | A autenticidade dos pacotes depende da confiança do gerenciador no ambiente isolado (chaves GPG do repositório) mais a integridade SHA-256 do conjunto baixado. SHA-256 prova integridade, não autenticidade do publicador. | Isso acompanha o modelo de confiança das distribuições em que o OPF opera; verificação independente de assinatura está fora do escopo da v0.2.1. | Mantenha a verificação GPG habilitada (padrão), use espelhos confiáveis e verifique o `SHA256SUMS` fora de banda se o host dos artefatos não for confiável. |
| R6 | Metadados de inventário do destino autenticado (nomes/versões) são armazenados como dados opacos; nomes de pacotes não são revalidados contra whitelist. | O destino é autenticado e os valores nunca são interpolados em comandos; apenas persistidos/reportados. | Não use um destino comprometido como fonte de verdade para relatórios; a validação pode ser endurecida em release futura. |
| R7 | O comando de instalação remota executa gerenciadores de pacotes como root (ou via sudo) no destino. | Instalar pacotes exige privilégio; o OPF exige confirmação explícita antes das etapas 10/11. | Revise o comando gerado (logado/streamado), mantenha `--sudo` restrito e use uma conta de manutenção dedicada. |
| R8 | O isolamento Docker/WSL depende do runtime do host e executa comandos como root dentro do ambiente isolado. | O ambiente isolado é descartado por sessão e nunca recebe credenciais SSH. | Mantenha Docker/WSL atualizados; não monte segredos do host no ambiente isolado. |
| R9 | Um symlink dentro do diretório de artefatos pode ser verificado se o conteúdo do alvo corresponder ao hash do manifesto. | A verificação é por conteúdo; um atacante que já escreve na árvore de artefatos e conhece os hashes pode trocar conteúdo, mas não escapar do diretório por nomes de manifesto. | Mantenha os diretórios de artefatos graváveis apenas pelo operador; symlinks para arquivos não regulares são rejeitados (C4.2). |
| R10 | O advisory de dependência da GUI (SEC-10) não é alcançável, mas o módulo `otel` permanece no binário. | Atualizar mais é decisão do responsável pela release; a análise de símbolos do govulncheck não mostra caminho de chamada. | Atualize `otel` para 1.42.0 em release futura e reexecute o `govulncheck`. |
| R11 | A suíte de segurança não realiza ataques ponta a ponta ao vivo contra alvos SSH/Docker/WSL reais. | CI determinística e sem infraestrutura; a suíte de integração funcional (F0–F5) cobre os fluxos ao vivo separadamente. | Execute `go test -tags=integration ./...` em laboratório controlado antes de implantações de alta garantia. |
| R12 | A cobertura de `-race` é Linux amd64 (execução final da 0.2.1) mais as execuções Windows/Linux registradas para a 0.1.0; macOS não é exercitado nesta execução. | A release visa principalmente Linux/Windows; macOS é secundário. | Adicione macOS à matriz de CI se ele se tornar alvo suportado. |
| R13 | Os binários de release para Linux e Windows são comprimidos com UPX e têm metadados removidos (`-s -w -buildid=`), o que elimina metadados de build e dificulta inspeção estática e confiança sem assinatura; alguns produtos de proteção de endpoint sinalizam binários empacotados com UPX. | A compressão é uma decisão de tamanho de release para os ativos Linux (CLI/TUI/GUI) e Windows (CLI/TUI); macOS é publicado como `.tar.gz` porque o UPX não empacota Mach-O. Fontes e comandos de build reproduzíveis são públicos. | Verifique cada ativo contra o `SHA256SUMS` publicado, inclua os `.exe` empacotados em allow-list do EDR quando necessário e compile do código-fonte com `make build` quando transparência total for exigida. |

---

## 8. Limitações

- Ferramentas são pontuais: o gitleaks varre a árvore de trabalho (sem histórico git), o
  govulncheck usa o snapshot do banco de vulnerabilidades vigente na execução (2026-11-21) e
  gosec/staticcheck analisam o código atual. Reexecute `scripts/security/scan.*` após mudanças de
  código ou dependências.
- Os testes de injeção verificam o quoting de forma estrutural (token entre aspas simples,
  payload apenas dentro de aspas) em vez de executar um shell POSIX real no Windows; os mesmos
  testes rodam no Linux no contêiner, onde a semântica POSIX é nativa.
- Nenhum fuzzing (`go test -fuzz`) foi realizado na v0.2.1; os parsers são cobertos por testes
  de tabela com entradas hostis.
- Nenhum pentest externo ou revisão manual de código por terceiro foi realizado.
- A varredura de segredos cobre apenas a árvore de trabalho; o histórico git é varrido pela
  action do gitleaks na CI (histórico completo).

---

## 9. Como reproduzir

### 9.1 Local (Windows)

```powershell
# Requer Go 1.27.4 e gcc/MinGW disponíveis no PATH
$env:CGO_ENABLED = '1'   # necessário para -race (gcc/MinGW)

go test -race -count=1 ./test/security/...
go test -count=1 ./...
pwsh -File scripts/security/scan.ps1        # gates + relatórios em bin/security
```

### 9.2 Local (Linux/macOS)

```bash
go test -race -count=1 ./test/security/...
go test -count=1 ./...
./scripts/security/scan.sh                  # gates + relatórios em bin/security
```

`scan.sh`/`scan.ps1` são fail-fast, imprimem as versões das ferramentas, gravam evidências
JSON/texto em `bin/security/` e param no primeiro gate que falhar. `-NoRace` / `NO_RACE=1` pula
a execução com race onde CGO não está disponível.

### 9.3 CI

A CI de release já chama a suíte (`.github/workflows/ci.yml`, job de segurança):

```yaml
- name: Security test suite
  run: go test -count=1 ./test/security/...
```

e usa como gate `gosec -severity high -confidence high ./...` e o gitleaks. O conjunto completo
de comandos é:

```text
go vet ./...
gofmt -l .            # deve ser vazio
go test -count=1 ./...
go test -count=1 ./test/security/...
gitleaks (action)
gosec -severity high -confidence high ./...
govulncheck ./...     # informativo na v0.2.1 (continue-on-error)
```

Para adicionar a variante com race, use `go test -race -count=1 ./test/security/...` em um
runner Linux com gcc instalado.

---

## 10. Política de divulgação e contato

- Relate suspeitas de vulnerabilidade **em privado** pelos GitHub Security Advisories:
  <https://github.com/ph7ti/Offline-Package-Fetcher/security/advisories/new>.
- Não abra issue pública com detalhes de exploração antes de existir correção.
- Os mantenedores buscam confirmar recebimento em até **7 dias** e coordenar a divulgação em até
  **90 dias**, ou antes, assim que houver correção publicada. Crédito é dado a quem o solicitar.
- Inclua a versão do OPF (tag de release ou nome do artefato), a plataforma, o comando exato e
  uma reprodução mínima com segredos fictícios. Nunca envie credenciais reais.
- Este relatório não contém segredos reais; todo valor parecido com credencial é fixture dummy.

---

## 11. Declaração de release

- A suíte de segurança definida neste documento foi **reexecutada em 2026-11-21** para o
  **Offline Package Fetcher v0.2.1**: 56 testes de nível superior (33 em `test/security/**` e 23
  testes white-box nos pacotes internos), incluindo os novos testes de idioma da interface.
- `go test ./...` passa na árvore de release no Windows (incluindo os testes da GUI com CGO) e
  em um contêiner Linux `golang:1.27.4`; `go vet ./...` e `gofmt -l .` estão limpos.
- `CGO_ENABLED=1 OPF_LANG=pt-BR go test -race -count=1 ./test/security/...` também passa na
  árvore final (`ok opf/test/security`, exit 0).
- O gosec high/high passa com 0 issues (1 `#nosec` documentado), o gitleaks não reporta
  vazamentos, o govulncheck reporta 0 vulnerabilidades alcançáveis (1 advisory não alcançável de
  nível de pacote, GO-2026-5158, documentado no §5.3/§6), o actionlint está limpo e o staticcheck
  reporta 3 achados informativos não bloqueantes.
- Os sete advisories alcançáveis encontrados antes da release 0.1.0 permanecem remediados pelas
  atualizações de dependência registradas no §5.3.
- Os ativos de release para Linux e Windows (CLI, TUI e GUI) são comprimidos com UPX após a
  remoção de metadados (`-trimpath -ldflags "-s -w -buildid="`), verificados com `upx -t` e com
  um smoke test do `-h` da CLI comprimida; os ativos macOS são arquivos `.tar.gz` verificados com
  `tar -tzf` (ver R13).
- Os itens restantes dos scanners e os riscos residuais do §7 são aceitos para a v0.2.1 e devem
  ser revisitados na v0.3.0.

**Assinado por:** engenheiro de segurança de aplicação, equipe de release do OPF, 2026-11-21.
