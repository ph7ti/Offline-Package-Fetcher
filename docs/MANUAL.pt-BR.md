[English](MANUAL.md) | **Português (BR)**

# Manual do Usuário — Offline Package Fetcher (OPF) v0.2.1

Este manual é a referência operacional do OPF: descreve cada tela, cada flag, o pipeline de 13 etapas, o banco SQLite, os relatórios, o Syslog e os procedimentos de diagnóstico. Para uma visão geral do projeto, comece pelo [README em português](README.pt-BR.md).

## Sumário

1. [Público e escopo](#1-público-e-escopo)
2. [Conceitos essenciais](#2-conceitos-essenciais)
3. [Pré-requisitos e matriz de plataformas](#3-pré-requisitos-e-matriz-de-plataformas)
4. [Instalação, build e verificação](#4-instalação-build-e-verificação)
5. [Início rápido](#5-início-rápido)
6. [GUI passo a passo](#6-gui-passo-a-passo)
7. [TUI passo a passo](#7-tui-passo-a-passo)
8. [Referência da CLI](#8-referência-da-cli)
9. [Detecção remota do alvo](#9-detecção-remota-do-alvo)
10. [Resolução de dependências e pacotes opcionais](#10-resolução-de-dependências-e-pacotes-opcionais)
11. [Repositórios adicionais](#11-repositórios-adicionais)
12. [SSH: autenticação, known_hosts, pontes e sudo](#12-ssh-autenticação-known_hosts-pontes-e-sudo)
13. [Pipeline de 13 etapas, cancelamento e retomada](#13-pipeline-de-13-etapas-cancelamento-e-retomada)
14. [Banco de dados SQLite](#14-banco-de-dados-sqlite)
15. [Relatórios e Syslog](#15-relatórios-e-syslog)
16. [Solução de problemas](#16-solução-de-problemas)
17. [Checklist de hardening](#17-checklist-de-hardening)
18. [Perguntas frequentes](#18-perguntas-frequentes)
19. [Glossário](#19-glossário)
20. [Versão e licença](#20-versão-e-licença)

## 1. Público e escopo

Este manual é destinado a profissionais de infraestrutura que preparam pacotes Linux para redes isoladas: administradores de sistemas, engenheiros de plataforma, times de suporte de campo e analistas de segurança que precisam levar software até servidores sem internet.

O OPF **faz**:

- baixar pacotes e dependências completas para Ubuntu/Debian (APT), CentOS/RHEL/Rocky/AlmaLinux/Fedora (RPM) e Alpine (APK);
- executar o download em um ambiente isolado com a distro/versão/arquitetura do destino;
- validar a integridade dos arquivos em três pontos;
- transferir os arquivos por SSH/SFTP, inclusive através de pontes;
- instalar no destino após confirmação explícita;
- registrar tudo em SQLite e exportar relatórios JSON/Markdown, além de enviar eventos a um Syslog externo.

O OPF **não faz** (v0.2.1):

- configurar repositórios no servidor de destino;
- autenticar em repositórios privados com usuário/senha;
- instalar automaticamente sem confirmação humana (exceto com `--yes`, que é uma decisão explícita);
- usar NOPASSWD para `sudo`;
- operar WSL fora da família APT;
- retomar sessões pela CLI (retomada existe apenas na GUI e na TUI).

## 2. Conceitos essenciais

| Conceito | Definição |
|:---|:---|
| **Sessão** | Uma execução completa do pipeline. Ganha um `ID` e um registro em `sessoes`; todos os pacotes, eventos e etapas apontam para ela. |
| **Alvo (target)** | Par `distro:versão` do destino (ex.: `ubuntu:22.04`), mais a arquitetura (ex.: `amd64`). |
| **Família** | Conjunto de ferramentas do gerenciador de pacotes: `apt`, `rpm` ou `apk`. |
| **Ambiente isolado** | Contêiner Docker ou distro WSL criado por sessão, na versão exata do destino, com diretório de trabalho `/tmp/opf-<id>`. |
| **Root** | Pacote explicitamente solicitado pelo operador. Um artefato que seja root não é marcado como dependência. |
| **Dependência** | Pacote resolvido recursivamente a partir dos roots. Pode ser obrigatória ou opcional (Recommends/Suggests/Enhances ou weak deps). |
| **Artefato** | Arquivo `.deb`, `.rpm` ou `.apk` baixado, com nome, versão, arquitetura e hash SHA-256. |
| **Manifesto** | Arquivo `SHA256SUMS` gerado no diretório de saída, com o hash de todos os artefatos da sessão. |
| **P1 / P2 / P3** | Pontos de verificação de integridade: P1 no ambiente isolado, P2 no host (manifesto) e P3 no destino remoto. |
| **Pré-check** | Consulta ao gerenciador remoto para descobrir pacotes já instalados antes de baixar. |
| **Checkpoint** | Registro de cada etapa em `etapas_sessao`, usado para progresso e retomada segura. |
| **Fetch method** | `docker` ou `wsl` — como o ambiente isolado é criado. |
| **Ponte (jump host)** | Salto SSH intermediário usado para alcançar o destino. |
| **Relatório** | Documento JSON/Markdown com sessão, host remoto, pacotes, eventos e destinos. |

## 3. Pré-requisitos e matriz de plataformas

### 3.1 Requisitos por recurso

| Recurso | Necessário para | Detalhe |
|:---|:---|:---|
| Go 1.27.4 | compilar | declarado em `go.mod`; também é a versão usada pela CI |
| Docker Engine | fetch `docker` | o daemon precisa estar acessível ao usuário que executa o OPF |
| WSL2 + distro registrada | fetch `wsl` | somente Ubuntu/Debian; execução como `root` |
| Servidor SSH + `known_hosts` | transferência, pré-check, instalação e SSH estrito | o destino precisa aceitar `sftp` e comandos remotos; a host key deve constar em `known_hosts` (padrão `~/.ssh/known_hosts`) |
| Rede para os repositórios | download | o host que executa o OPF precisa de internet/intranet; o destino não |
| `gcc` + libs gráficas | GUI | Linux: `libgl1-mesa-dev`, `xorg-dev`; Windows: `mingw-w64`; macOS: Xcode CLT |
| `sqlite3` (opcional) | consultas manuais | não é distribuído pelo OPF |

### 3.2 Matriz de plataformas

| Plataforma | CLI | TUI | GUI | Fetch Docker | Fetch WSL | SSH/SFTP |
|:---|:---:|:---:|:---:|:---:|:---:|:---:|
| Linux | sim | sim | sim (CGO) | sim | não | sim |
| Windows | sim | sim | sim (CGO) | sim | sim (APT) | sim |
| macOS | sim | sim | sim (CGO) | sim | não | sim |

Observações:

- **macOS** é plataforma secundária: use Docker para o download; a GUI exige Xcode Command Line Tools.
- **Windows** sem Docker pode operar apenas a família APT via WSL2.
- Ambientes com `CGO_ENABLED=0` compilam CLI, TUI e núcleo; a GUI vira um stub que encerra com mensagem de erro.

## 4. Instalação, build e verificação

### 4.1 Binários da release

1. Acesse a página da release `v0.2.1`.
2. Baixe o binário da sua plataforma e o arquivo de checksums publicado junto.
3. Verifique o hash:

```bash
sha256sum -c SHA256SUMS
```

```powershell
Get-FileHash .\opf-cli.exe -Algorithm SHA256
```

4. Torne o binário executável (Linux/macOS) e mova para um diretório do `PATH`:

```bash
chmod +x opf-cli
sudo install -m 0755 opf-cli /usr/local/bin/opf-cli
```

Cada release publica 12 ativos (11 binários e `SHA256SUMS`): CLI e TUI em linux/amd64, linux/arm64, windows/amd64 (`.exe` com UPX), darwin/amd64 e darwin/arm64 (`.tar.gz`), além de `opf-gui-linux-amd64`. Extraia os ativos macOS com `tar -xzf <ativo>.tar.gz`.

#### Tamanho dos binários e compressão

Builds de produção removem metadados com `-trimpath -ldflags "-s -w -buildid="`. Os ativos Linux e Windows são então comprimidos com UPX `--best --lzma` e verificados com `upx -t`; o workflow de release também executa `opf-cli -h` no binário Linux comprimido como smoke test. Tamanhos medidos em 2026-11-21:

| Ativo | Sem compressão (bytes) | UPX (bytes) | Redução |
|:---|--:|--:|--:|
| `opf-cli-linux-amd64` | 14.540.924 | 4.548.248 | -68,72% |
| `opf-tui-linux-amd64` | 15.396.988 | 4.946.584 | -67,87% |
| `opf-gui-linux-amd64` | 33.762.840 | 12.305.644 | -63,55% |
| `opf-cli-linux-arm64` | 13.697.148 | 3.941.072 | -71,23% |
| `opf-tui-linux-arm64` | 14.483.580 | 4.107.204 | -71,64% |
| `opf-cli-windows-amd64.exe` | 14.781.440 | 4.758.528 | -67,81% |
| `opf-tui-windows-amd64.exe` | 15.679.488 | 4.964.352 | -68,34% |

Os valores são medições locais (UPX 4.2.4); os ativos publicados podem variar alguns KB conforme a versão do UPX no runner. Executáveis Windows empacotados podem exigir allow-list em produtos de proteção de endpoint (Cortex XDR e similares). Compile do código com `make build` quando precisar de um executável não empacotado.

Executáveis Windows empacotados podem exigir allow-list em produtos de proteção de endpoint (Cortex XDR e similares). Compile do código com `make build` quando precisar de um executável não empacotado.

macOS não pode ser empacotado pelo UPX; por isso é publicado em `.tar.gz`. Extraia com `tar -xzf <ativo>.tar.gz`; tamanhos medidos:

| Ativo | Sem compressão (bytes) | `.tar.gz` (bytes) | Redução |
|:---|--:|--:|--:|
| `opf-cli-darwin-amd64.tar.gz` | 14.627.072 | 6.060.500 | -58,57% |
| `opf-cli-darwin-arm64.tar.gz` | 13.807.426 | 5.646.939 | -59,10% |
| `opf-tui-darwin-amd64.tar.gz` | 15.492.608 | 6.339.584 | -59,08% |
| `opf-tui-darwin-arm64.tar.gz` | 14.634.338 | 5.905.253 | -59,65% |

O `opf-gui` é publicado apenas para linux/amd64; o build Windows da GUI (34.168.832 bytes) é produzido pela CI, mas não é ativo de release.

### 4.2 Build a partir do código

```bash
git clone https://github.com/ph7ti/Offline-Package-Fetcher.git
cd Offline-Package-Fetcher
mkdir -p bin

make build        # bin/opf-cli   (CGO_ENABLED=0)
make build-tui    # bin/opf-tui   (CGO_ENABLED=0)
make build-gui    # bin/opf-gui   (CGO_ENABLED=1)
make build-linux  # bin/opf-cli-linux-amd64 (cross-compile)
make compress     # opcional (Linux/Windows): UPX --best --lzma + upx -t
```

Os alvos do Makefile usam `-trimpath -ldflags "-s -w -buildid="`, as mesmas flags do workflow de release. No Windows, sem `make`:

```powershell
# Go 1.27.4 instalado e disponível no PATH (go version => go1.27.4)
New-Item -ItemType Directory -Force bin | Out-Null
go build -trimpath -ldflags "-s -w -buildid=" -o bin/opf-cli.exe ./cmd/opf-cli
go build -trimpath -ldflags "-s -w -buildid=" -o bin/opf-tui.exe ./cmd/opf-tui
```

### 4.3 Verificação pós-instalação

```bash
opf-cli -h        # lista todas as flags e defaults
go version        # deve reportar a toolchain usada no build
opf-tui           # abre o menu principal (Ctrl+C encerra)
opf-gui           # abre a janela; sem CGO, encerra com mensagem orientativa
```

O `opf-cli -h` imprime a referência das flags no idioma da interface e sai com código `0`.

### 4.4 Configurações globais (GUI/TUI)

A GUI e a TUI gravam preferências não sensíveis em JSON:

| Plataforma | Caminho |
|:---|:---|
| Linux | `~/.config/opf/config.json` |
| Windows | `%AppData%\opf\config.json` |
| macOS | `~/Library/Application Support/opf/config.json` |

O diretório é criado com `0700` e o arquivo é publicado de forma atômica com `0600`.

| Campo | Tipo | Default | Uso |
|:---|:---|:---|:---|
| `fetch_method` | string | `docker` | `docker`, `wsl` ou `auto` |
| `temp_dir` | string | vazio | base de temporários |
| `database_path` | string | vazio | caminho do SQLite |
| `stage_timeout_seconds` | int | `600` | timeout por etapa |
| `docker_image` | string | vazio | imagem padrão |
| `wsl_distro` | string | vazio | distro WSL padrão |
| `include_optional` | bool | `false` | dependências opcionais |
| `force_reinstall` | bool | `false` | forçar reinstalação |
| `language` | string | `auto` | idioma da interface: `auto`, `en`, `pt-BR` ou `es` |
| `syslog_enabled` | bool | `false` | habilita Syslog |
| `syslog_host` | string | vazio | coletor |
| `syslog_port` | int | `514` | porta |
| `syslog_proto` | string | `udp` | `udp`/`tcp` |
| `syslog_tag` | string | `opf` | tag RFC 5424 |

A CLI **não** lê esse arquivo: todos os parâmetros vêm de flags e variáveis de ambiente.

## 5. Início rápido

### 5.1 Apenas baixar

```bash
opf-cli --target ubuntu:22.04 --arch amd64 --pkg vim --pkg git --out ./opf-output
```

Resultado: os arquivos ficam em `./opf-output/sessao-<id>/` junto do manifesto, e o registro em `./opf.db`.

### 5.2 Transferir por SSH com detecção automática do alvo

```bash
opf-cli --pkg postgresql \
  --ssh-host db01.interno --ssh-user deploy \
  --ssh-key ~/.ssh/id_ed25519
```

Sem `--target`/`--arch`, o OPF conecta, lê `/etc/os-release` e `uname -m`, deriva `distro:versão`, arquitetura e imagem recomendada e segue o pipeline.

### 5.3 Transferir e instalar com sudo

```bash
export OPF_SSH_PASSWORD='senha-da-conta'

opf-cli --pkg nginx --ssh-host web01.interno --ssh-user deploy \
  --ssh-password "$OPF_SSH_PASSWORD" \
  --install --sudo --yes \
  --export-json ./opf-output/relatorio.json \
  --export-markdown ./opf-output/relatorio.md
```

Sem `--yes`, o prompt é interativo:

```
Instalar 12 pacotes em deploy@web01.interno? [y/N]
```

Responder qualquer coisa diferente de `y`/`yes` registra os pacotes como `enviado_nao_instalado` e conclui a sessão sem instalar.

### 5.4 Windows com WSL

```powershell
.\bin\opf-cli.exe --target ubuntu:22.04 --arch amd64 --pkg htop `
  --fetch-method wsl --wsl-distro Ubuntu-22.04
```

A distro informada precisa estar registrada (`wsl --list --verbose`), ser Ubuntu/Debian da mesma versão do alvo e executar como `root`.

## 6. GUI passo a passo

A GUI (`opf-gui`) é um binário Fyne separado, compilado com `CGO_ENABLED=1`. O fluxo é **SSH-first**: uma nova sessão começa pela conexão e detecção do destino; a configuração manual fica em **Avançado**. Os rótulos de tela e de campos seguem o idioma da interface; este manual cita os rótulos-fonte em português do Brasil.

### 6.1 Início

Tela inicial com quatro indicadores (Sessões, Pacotes, Instalados, Banco de dados) e as **Ações rápidas**:

| Ação | Efeito |
|:---|:---|
| **Nova sessão** | inicia o fluxo em **Conexão SSH e Jump Hosts** com os campos limpos |
| **Continuar** | abre a sessão não concluída mais recente (checkpoint seguro) |
| **Histórico** | abre a lista de sessões registradas |
| **Configurações** | abre **Configurações Globais** |
| **Atualizar** | recarrega indicadores, banco e sessões recentes |

Abaixo, **Sessões recentes** mostra até quatro cartões com `#id`, distro/versão, host (ou “download local”), data e total de pacotes, cada um com os botões **Relatório**, **Retomar** (desabilitado em sessões concluídas) e **Arquivar**.

### 6.2 Conexão SSH e Jump Hosts

1. Marque **Enviar e instalar via SSH** para habilitar transferência e instalação. Sem essa opção, a sessão apenas baixa e valida os pacotes localmente.
2. Preencha **Conexão**: Host do destino, Porta (padrão `22`), Usuário, Chave privada, Senha da conta e Known hosts (vazio = `~/.ssh/known_hosts`).
3. Opcionalmente, adicione **Pontes SSH** em **Adicionar ponte**. As pontes são abertas em ordem, cada uma através da anterior, com credenciais próprias (senha, chave ou chave+senha).
4. Ajuste **Diretório remoto** se não quiser o padrão `/tmp/opf-sessao-<id>`.
5. Em **Detecção e segurança**, use **Testar conexão** (apenas valida o handshake) e **Detectar destino** (lê `/etc/os-release` e `uname -m`, preenchendo distro, versão, arquitetura e imagem recomendada).
6. Clique **Continuar**. Se o destino ainda não foi detectado e a configuração não mudou desde a última detecção, a GUI dispara a detecção automaticamente.

Regras e avisos:

- **Ignorar verificação de host key (inseguro)** exibe um banner vermelho de MITM e equivale a `StrictHostKeyChecking=no`, aplicado ao destino e a todas as pontes.
- Alterar host, usuário, porta ou a configuração das pontes invalida a detecção anterior (`Configuração SSH alterada; detecte novamente.`).
- **Configurar manualmente** abre **Configuração da Sessão** e limpa o alvo detectado se a detecção estiver desatualizada.

### 6.3 Configuração da Sessão (avançado)

Campos:

| Grupo | Campos |
|:---|:---|
| Sistema de destino | Distro de destino (Ubuntu, Debian, CentOS, Rocky Linux, AlmaLinux, Fedora, RHEL, Alpine), Versão, Arquitetura |
| Ambiente de download | Método de fetch (Docker, WSL, Automático), Imagem Docker (opcional), Distro WSL (opcional) |
| Repositórios adicionais | lista aditiva usada apenas no ambiente isolado + **Adicionar repositório** |
| Saída e opções | Diretório de saída, **Incluir dependências opcionais** |

Ao trocar a distro, a GUI sugere versão e arquitetura padrão (`ubuntu`/`22.04`/`amd64`, `debian`/`12`/`amd64`, `centos`/`7`/`x86_64`, `rockylinux` e `almalinux`/`9`/`x86_64`, `fedora`/`41`/`x86_64`, `rhel`/`9`/`x86_64`, `alpine`/`3.20`/`x86_64`).

**Continuar** valida distro, versão e arquitetura antes de abrir **Pacotes e Pré-check**.

### 6.4 Pacotes e Pré-check

| Campo | Comportamento |
|:---|:---|
| **Pacotes solicitados** | um pacote por linha ou separados por vírgula; duplicatas são removidas |
| **Instalar após transferir e validar** | habilita a etapa 11 (somente com SSH) |
| **Usar sudo com a senha da conta** | habilita `sudo -S`; exige senha preenchida |
| **Senha da conta para sudo** | usada apenas em memória e enviada por `stdin` |
| **Incluir dependências opcionais** | equivalentes a Recommends/Suggests/Enhances e weak deps |
| **Forçar reinstalação (ignorar pré-check)** | baixa mesmo que o pacote já exista no destino |

Sem SSH, os controles de instalação ficam desabilitados e um banner informa que os pacotes serão apenas baixados e validados. **Iniciar** executa o pipeline.

### 6.5 Pipeline

- **Etapas do pipeline**: grade com as 13 etapas e marcadores `[ ]`, `[▶]`, `[✔]` e `[✘]`.
- **Progresso**: texto da etapa atual e barra (download/upload reportam contagem e bytes reais; não há MB/s inventado).
- **Log em tempo real**: últimas linhas do `slog` e da execução remota.
- **Cancelar execução**: solicita cancelamento; a sessão termina como `abortada` com cleanup local e remoto best-effort.

Durante a instalação, aparece o diálogo **Confirmar instalação** com **Instalar** / **Recusar**.

### 6.6 Resultado

Cartões com Sessão, Artefatos, Destino e Instalação (`instalado`, `recusado`, `já presente`, `transferido`) e a seção **Rastreabilidade** com caminho local, manifesto, destino remoto e a lista de pacotes já instalados. Ações: **Ver relatório**, **Histórico** e **Nova sessão**.

Casos exibidos: `Instalação concluída`, `Transferência concluída` (instalação recusada), `Nada a instalar` (todos já presentes), `Sessão concluída` (somente download) e `Sessão encerrada com erro`.

### 6.7 Relatório da Sessão

Renderiza o Markdown gerado a partir do SQLite (host remoto, destino/armazenamento, pacotes solicitados, tabela de pacotes com papel e versão anterior, eventos).

| Botão | Resultado |
|:---|:---|
| **Exportar JSON** | grava `opf-relatorio-<id>.json` no diretório de saída |
| **Exportar Markdown** | grava `opf-relatorio-<id>.md` no diretório de saída |
| **Enviar ao Syslog** | envia o relatório como evento estruturado (exige Syslog habilitado) |
| **Voltar** | retorna à tela anterior |

Arquivos de relatório são gravados com permissão `0600`.

### 6.8 Histórico

Lista de sessões com cartões (`Relatório`, `Retomar`, `Arquivar`). Na GUI, **Retomar** fica desabilitado para sessões concluídas; o arquivamento é lógico (`arquivada = 1`) e remove a sessão das listagens padrão.

### 6.9 Configurações Globais

| Item | Efeito |
|:---|:---|
| **Arquivo SQLite** | caminho do banco usado por histórico, checkpoints e relatórios |
| **Diretório temporário** | base para temporários do host durante o prepare |
| **Timeout de etapa (segundos)** | padrão `600` (10 minutos) |
| **Syslog: Habilitar, Host, Porta, Protocolo, Tag** | envio assíncrono RFC 5424 (UDP/TCP) |
| **Idioma da interface** | idioma dos textos: **Automático (sistema)**, **English**, **Português (BR)** ou **Español**; `auto` segue `OPF_LANG` e depois o sistema operacional |

**Salvar configurações** valida timeout (`> 0`), porta (`1..65535`), host obrigatório quando o Syslog está habilitado e o idioma selecionado, grava o `config.json` com permissão `0600` e reabre o banco. A troca de idioma é aplicada imediatamente e redesenha a tela atual e os textos de ajuda.

A barra superior traz atalhos **Início**, **Histórico**, **Configurações** (desabilitados durante a execução) e o botão **Tema** (claro/escuro, preferência salva); a barra inferior mostra o estado (`pronto para operar`, `pipeline em execução`, `sessão #N`, `SQLite indisponível`), o nome do banco e o tema atual.

## 7. TUI passo a passo

A TUI (`opf-tui`) é Bubble Tea puro (`CGO_ENABLED=0`), com as mesmas operações da GUI.

### 7.1 Menu principal

```
Offline Package Fetcher

> Nova sessão
  Continuar sessão
  Histórico
  Configurações globais
  Sair
```

Navegação: `↑`/`↓` ou `k`/`j`; `Enter` seleciona; `q` sai. O rodapé confirma `SQLite: OK` e a política `Sem NOPASSWD • host key obrigatória • segredos nunca são logados`.

### 7.2 Telas e atalhos

| Tela | Teclas | Observações |
|:---|:---|:---|
| **Configuração da Sessão** | `tab`/`↓`, `shift+tab`/`↑`, `espaço` (toggles), `enter` avança, `ctrl+r` repositórios, `esc` volta | campos Distro, Versão, Arquitetura, Fetch (`docker`/`wsl`/`auto`), Imagem, Distro WSL, Saída, SQLite, Incluir opcionais |
| **Conexão SSH e Jump Hosts** | `ctrl+j` pontes, `ctrl+d` detectar, `ctrl+t` testar, `ctrl+a` avançado, `enter` detectar/continuar | `espaço` alterna Habilitar SSH e Ignorar host key |
| **Ponte SSH** (edição) | `tab`/`↑↓`, `espaço` alterna autenticação (`password`, `key`, `key+password`), `ctrl+s`/`enter` salva, `esc` descarta | `known_hosts` vazio herda o do destino |
| **Pontes SSH** (lista) | `a` adicionar, `e`/`enter` editar, `d` remover, `K`/`J` mover, `esc` voltar | ordem = ordem dos saltos |
| **Repositórios adicionais** | `a`, `e`/`enter`, `d`, `esc` | lista aditiva; aviso quando a família não corresponde ao alvo |
| **Repositório adicional** (edição) | `tab`, `espaço` alterna fonte da chave (`arquivo`, `URL`, `nenhuma`), `ctrl+s`/`enter` salva | campos APT aparecem apenas em família APT |
| **Pacotes e Pré-check** | `espaço` alterna, `enter` inicia, `esc` volta | pacotes separados por vírgula |
| **Executando pipeline** | `c` cancela, `y`/`s` confirma instalação, `n`/`esc` recusa, `ctrl+c` sai | progresso, 13 etapas e últimas 25 linhas de log |
| **Resultado** | `r` relatório, `h` histórico, `n` nova sessão, `esc` início | mostra sessão, artefatos, saída, manifesto, destino remoto e já instalados |
| **Relatório da sessão** | `↑`/`↓`/`k`/`j` rolar, `pgup`/`pgdown`, `J` JSON, `M` Markdown, `S` Syslog, `esc` volta | exporta `opf-relatorio-<id>.json/.md` no diretório de saída |
| **Histórico** | `↑`/`↓`, `enter` abrir relatório, `r` retomar, `a` arquivar, `esc` voltar | sessões arquivadas marcadas com `(arquivada)` |
| **Configurações Globais** | `tab`/`↑↓`, `ctrl+s` salva, `esc` volta | temporário, timeout, idioma da interface, Syslog (host, porta, protocolo, tag) |

O campo **Idioma da interface** aceita `auto`, `en`, `pt-BR` ou `es` (os apelidos `pt` e `pt_BR` são normalizados). `auto` segue `OPF_LANG` quando definida e, caso contrário, o sistema operacional. Ao salvar, o valor é validado, persistido em `config.json` e a tela de configurações, os rótulos dos formulários e os textos de ajuda são redesenhados imediatamente no novo idioma.

### 7.3 Fluxo recomendado na TUI

1. **Nova sessão** → habilite SSH e informe host/usuário/credencial.
2. `ctrl+d` detecta o destino; confirme a linha `Destino detectado: ...`.
3. `ctrl+j` adiciona pontes, se necessário; `ctrl+t` testa a conexão.
4. `enter` em **Continuar** → **Pacotes e Pré-check**: informe pacotes, marque instalação/sudo se aplicável.
5. `enter` inicia; acompanhe as etapas; confirme a instalação com `y` quando solicitado.
6. Em **Resultado**, `r` abre o relatório; `J`/`M`/`S` exportam ou enviam ao Syslog.

Erros de digitação/validação aparecem em vermelho no rodapé da tela; `esc` sempre retorna sem executar.

## 8. Referência da CLI

A CLI usa o pacote `flag` da biblioteca padrão: aceita `-flag` e `--flag`. Flags repetíveis estão marcadas como “repetível”. Ajuda, mensagens e erros seguem o idioma da interface (ver [8.11 Idioma da interface](#811-idioma-da-interface)); por padrão, segue o idioma do sistema operacional. As mensagens citadas neste manual são as strings-fonte em português do Brasil.

### 8.1 Alvo e saída

| Flag | Tipo | Default | Observações |
|:---|:---|:---|:---|
| `--target` | string | vazio | `distro:versao`; obrigatório sem `--ssh-host`; aceita `ubuntu`, `debian`, `centos`, `rhel`, `rockylinux` (alias `rocky`), `almalinux`, `fedora`, `alpine` |
| `--arch` | string | vazio | ex.: `amd64`, `x86_64`; obrigatório sem `--ssh-host` |
| `--out` | string | `./opf-output` | não pode ser vazio; os artefatos vão para `<out>/sessao-<id>` |
| `--db` | string | `./opf.db` | não pode ser vazio; criado com schema e migrações automáticas |
| `--export-json` | string | vazio | caminho do relatório JSON |
| `--export-markdown` | string | vazio | caminho do relatório Markdown |

### 8.2 Pacotes e seleção

| Flag | Tipo | Default | Observações |
|:---|:---|:---|:---|
| `--pkg` | repetível / lista | — | obrigatório ao menos um; aceita repetição ou valores separados por vírgula |
| `--with-optional` | bool | `false` | inclui Recommends/Suggests/Enhances (APT) e weak deps (DNF) |
| `--force-reinstall` | bool | `false` | ignora o resultado do pré-check e baixa mesmo assim |

### 8.3 Fetch e ambiente

| Flag | Tipo | Default | Observações |
|:---|:---|:---|:---|
| `--fetch-method` | string | `docker` | `docker` ou `wsl`; `wsl` só aceita famílias APT |
| `--image` | string | vazio | sobrescreve a imagem Docker; não se aplica a `wsl` |
| `--wsl-distro` | string | vazio | distro registrada; exigida apenas com `--fetch-method wsl` |
| `--temp-dir` | string | vazio (SO) | base dos temporários do host; na CLI aplica-se apenas ao fetcher WSL (no Docker vale o padrão do SO) |

### 8.4 SSH (destino)

| Flag | Tipo | Default | Observações |
|:---|:---|:---|:---|
| `--ssh-host` | string | vazio | habilita SSH/SFTP, pré-check e instalação |
| `--ssh-port` | int | `22` | validado em `1..65535` |
| `--ssh-user` | string | vazio | obrigatório com `--ssh-host` |
| `--ssh-key` | string | vazio | chave privada; aceita `~/...` |
| `--ssh-password` | string | vazio | senha SSH (prefira `OPF_SSH_PASSWORD`); também autentica o `sudo -S` |
| `--ssh-known-hosts` | string | vazio → `~/.ssh/known_hosts` | arquivo precisa existir e ser regular |
| `--ssh-insecure-skip-host-key` | bool | `false` | inseguro; equivale a `StrictHostKeyChecking=no` em todos os saltos |
| `--remote-dir` | string | `/tmp/opf-sessao-<id>` | diretório remoto da sessão |

Todas as flags de SSH (incluindo as de ponte) exigem `--ssh-host`; usá-las sem ele gera erro de validação.

### 8.5 Pontes SSH (jump hosts)

| Flag | Tipo | Default | Observações |
|:---|:---|:---|:---|
| `--ssh-jump` | repetível | — | `[usuario@]host[:porta]`; a ordem é a ordem dos saltos |
| `--ssh-jump-auth` | repetível | — | obrigatória por ponte: `password`, `key` ou `key+password` |
| `--ssh-jump-password` | repetível | — | senha da ponte; conflita com `OPF_SSH_JUMP_PASSWORDS` |
| `--ssh-jump-key` | repetível | — | chave privada da ponte |
| `--ssh-jump-known-hosts` | repetível | herda `--ssh-known-hosts` | `known_hosts` específico da ponte |

As listas são alinhadas por índice com `--ssh-jump`; contagens divergentes geram erro. Variáveis de ambiente:

| Variável | Conteúdo |
|:---|:---|
| `OPF_SSH_PASSWORD` | senha da conta SSH do destino |
| `OPF_SSH_JUMP_PASSWORDS` | array JSON de strings, uma senha por ponte (use `""` quando a ponte usa chave) |

### 8.6 Sudo e instalação

| Flag | Tipo | Default | Observações |
|:---|:---|:---|:---|
| `--install` | bool | `false` | instala após enviar e validar; exige `--ssh-host` |
| `--yes` | bool | `false` | dispensa o prompt; exige `--install` |
| `--sudo` | bool | `false` | executa via `sudo -S`; exige `--install` e senha (`--ssh-password` ou `OPF_SSH_PASSWORD`) |

### 8.7 Repositórios adicionais

| Flag | Tipo | Default | Observações |
|:---|:---|:---|:---|
| `--repo-config` | string | vazio | arquivo JSON com a lista; mutuamente exclusivo com as demais `--repo-*` |
| `--repo-url` | string | vazio | obrigatória no modo simples |
| `--repo-name` | string | `private` | nome usado no arquivo de repositório do ambiente |
| `--repo-family` | string | família do alvo | `apt`, `rpm` ou `apk`; precisa coincidir com o alvo |
| `--repo-key` | string | vazio | chave local (arquivo) |
| `--repo-key-url` | string | vazio | chave remota (http/https) |
| `--repo-ca` | string | vazio | CA customizada |
| `--repo-apt-suite` | string | vazio | obrigatório em APT não-flat |
| `--repo-apt-components` | string | `main` | lista separada por vírgula |
| `--repo-apt-flat` | bool | `false` | repositório flat (sem suite/components) |
| `--repo-insecure-tls` | bool | `false` | inseguro; não suportado em APK |
| `--repo-insecure-gpg` | bool | `false` | inseguro; conflita com chave informada |

### 8.8 Syslog

| Flag | Tipo | Default | Observações |
|:---|:---|:---|:---|
| `--syslog` | bool | `false` | habilita o envio; exige `--syslog-host` |
| `--syslog-host` | string | vazio | host do coletor |
| `--syslog-port` | int | `514` | validado em `1..65535` |
| `--syslog-proto` | string | `udp` | `udp` ou `tcp` |
| `--syslog-tag` | string | `opf` | `[A-Za-z0-9][A-Za-z0-9._-]*` |

### 8.9 Erros de validação da CLI

| Mensagem | Causa | Correção |
|:---|:---|:---|
| `--pkg é obrigatório` | nenhum pacote informado | use `--pkg pacote` |
| `--out não pode ser vazio` | `--out ""` | informe um diretório |
| `--db não pode ser vazio` | `--db ""` | informe um arquivo |
| `--yes exige --install` | `--yes` isolado | adicione `--install` |
| `--sudo exige --install` | `--sudo` isolado | adicione `--install` |
| `--install exige --ssh-host` | instalação sem destino | informe `--ssh-host`/`--ssh-user` |
| `--syslog exige --syslog-host` | Syslog sem coletor | informe `--syslog-host` |
| `--<flag> exige --ssh-host` | flag de SSH sem destino | remova a flag ou informe `--ssh-host` |
| `--ssh-user é obrigatório com --ssh-host` | usuário ausente | informe `--ssh-user` |
| `--ssh-port inválida: N` | porta fora de `1..65535` | corrija a porta |
| `informe --ssh-key ou --ssh-password/OPF_SSH_PASSWORD` | sem credencial | informe chave ou senha |
| `--target e --arch são obrigatórios sem --ssh-host` | download sem alvo nem SSH | informe os dois |
| `--target inválido: "x" (esperado distro:versao)` | formato errado | use `distro:versao` |
| `--target não suportado: "x"` | distro desconhecida | veja os alvos suportados |
| `--fetch-method inválido: "x" (use docker ou wsl)` | método inválido | use `docker`/`wsl` |
| `--fetch-method wsl suporta apenas Ubuntu/Debian (APT)` | WSL com RPM/APK | use Docker |
| `--image não se aplica a --fetch-method wsl` | imagem com WSL | remova `--image` |
| `--wsl-distro exige --fetch-method wsl` | distro WSL sem método | adicione `--fetch-method wsl` |
| `--repo-config é mutuamente exclusivo com as flags --repo-*` | duas formas juntas | escolha uma |
| `--repo-url é obrigatório com as flags --repo-*` | modo simples sem URL | informe `--repo-url` |
| `--repo-apt-* aplica-se apenas a repositórios APT` | flag APT em RPM/APK | remova a flag |
| `--repo-apt-components não se aplica a --repo-apt-flat` | combinação inválida | remova uma das duas |
| `--ssh-jump exige --ssh-jump-auth ...` | ponte sem autenticação | informe `--ssh-jump-auth` |
| `--ssh-jump-auth tem N valores para M pontes` | contagem divergente | alinhe as listas |
| `OPF_SSH_JUMP_PASSWORDS deve ser um array JSON de strings` | env inválida | corrija o JSON |
| `use --ssh-jump-password ou OPF_SSH_JUMP_PASSWORDS, não ambos` | duas fontes | escolha uma |
| `--sudo exige --ssh-password/OPF_SSH_PASSWORD ...` | sudo sem senha | informe a senha |
| `argumentos inesperados: ...` | argumento posicional | remova o argumento |
| `--lang inválido: "..." (use auto, en, pt-BR ou es)` | valor de idioma não suportado | use `auto`, `en`, `pt-BR` ou `es` |
| `--lang exige um valor` | `--lang` sem valor | informe um dos valores suportados |

Erros são impressos como `opf-cli: <mensagem>` em `stderr`; código de saída `1` para erro de validação ou falha de pipeline. A ajuda com `-h`/`--help` sai com código `0`.

### 8.10 Saída padrão da CLI

Ao final de uma sessão bem-sucedida:

```
sessão <id> concluída
artefatos: <n>
saída: <diretório local>
manifesto: <caminho do SHA256SUMS>
destino remoto: <diretório remoto>   # apenas com SSH
relatório JSON: <caminho>            # apenas com --export-json
relatório Markdown: <caminho>        # apenas com --export-markdown
instalação: concluída                # apenas com --install aceito
instalação: recusada; pacotes permanecem enviados   # quando recusada
```

### 8.11 Idioma da interface

O OPF segue o idioma do sistema operacional por padrão. São suportados inglês, português do Brasil (pt-BR) e espanhol (es); se o idioma do SO não for um deles, a interface inicia em inglês. A flag da CLI tem precedência sobre tudo, e `OPF_LANG` sobrepõe a detecção do SO quando a flag está em `auto` ou é omitida:

| Flag | Tipo | Default | Observações |
|:---|:---|:---|:---|
| `--lang` | string | `auto` | `auto`, `en`, `pt-BR` ou `es`; `-lang` e `--lang` são aceitos |

| Variável | Conteúdo |
|:---|:---|
| `OPF_LANG` | `auto`, `en`, `pt-BR` ou `es`; consultada quando `--lang` está em `auto`/omitida (também afeta a inicialização da GUI e da TUI) |

Precedência: `--lang` > `OPF_LANG` > sistema operacional. Os apelidos `pt` e `pt_br` são aceitos sem diferenciar maiúsculas; valor não suportado em `--lang` gera erro de validação (`--lang inválido: ...`).

O idioma se aplica à ajuda/uso das flags, mensagens de erro, progresso no console, nomes e mensagens das etapas do pipeline e rótulos do relatório Markdown; a GUI e a TUI também traduzem suas strings visíveis (ver [6. GUI passo a passo](#6-gui-passo-a-passo) e [7. TUI passo a passo](#7-tui-passo-a-passo)). `-h`/`--help` imprime a referência no idioma ativo e sai com código `0`.

Limitação na 0.2.1: os diagnósticos de baixo nível permanecem em português do Brasil independentemente do idioma selecionado. Isso inclui a saída dos gerenciadores de pacotes e do ambiente isolado (apt/dpkg, dnf/yum/rpm, apk, ferramentas Docker/WSL), as causas de erro emitidas pelas camadas de fetch, transferência, instalação, resolução, inspeção, armazenamento, configuração e aplicação, e parte das linhas de log interna (slog).

## 9. Detecção remota do alvo

Quando `--target` e/ou `--arch` são omitidos com `--ssh-host`, ou quando a GUI/TUI clicam em **Detectar destino**:

1. abre-se uma conexão SSH transitória (timeout de 45 s no probe; 60 s no CLI);
2. executam-se `cat /etc/os-release` e `uname -m`;
3. o OPF normaliza `ID`/`ID_LIKE` para uma distro suportada;
4. `VERSION_ID` (ou `VERSION`) define a versão; `uname -m` define a arquitetura;
5. um preset sugere a imagem Docker recomendada.

Normalização de distro (`ID` → alvo interno):

| `ID` / `ID_LIKE` | Alvo |
|:---|:---|
| `ubuntu` | `ubuntu` |
| `debian` | `debian` |
| `alpine` | `alpine` |
| `centos` | `centos` |
| `rhel`, `ol`, `ID_LIKE` com `rhel`/`fedora` | `rhel` |
| `rocky` | `rockylinux` |
| `almalinux` | `almalinux` |
| `fedora` | `fedora` |

Normalização de arquitetura:

| `uname -m` | APT | RPM | APK |
|:---|:---|:---|:---|
| `x86_64`/`amd64` | `amd64` | `x86_64` | `x86_64` |
| `aarch64`/`arm64` | `arm64` | `aarch64` | `aarch64` |
| `armv7l`/`armhf` | `armhf` | `armv7hl` | `armv7` |
| `i686`/`i386` | `i386` | `i686` | `i686` |

Presets de imagem:

| Alvo | Imagem sugerida | Preset validado |
|:---|:---|:---|
| Ubuntu 22.04 / 24.04 | `ubuntu:<versão>` | sim |
| Debian 12 | `debian:12` | sim |
| Alpine 3.20 | `alpine:<versão>` | sim |
| CentOS 7 | `centos:7` | sim |
| CentOS 8/9+ | `quay.io/centos/centos:stream<major>` | 9 e 10 |
| Rocky Linux | `rockylinux:<versão>` | 9.x |
| AlmaLinux | `almalinux:<versão>` | 9.x |
| Fedora | `fedora:<versão>` | 41 |
| RHEL | `registry.access.redhat.com/ubi<major>/ubi` | 8, 9 |

Se o preset detectado não for validado, o OPF registra um aviso (`preset do target detectado ainda não foi validado em integração`) e a GUI/TUI exibem a marca “preset não validado”. Flags explícitas sempre têm precedência sobre a detecção.

## 10. Resolução de dependências e pacotes opcionais

### 10.1 APT (Ubuntu/Debian)

- Índices e cache ficam isolados na sessão: `Dir::State::lists`, `Dir::Cache::archives`, `Dir::Cache::pkgcache` e `Dir::Cache::srcpkgcache` sob `/tmp/opf-<id>`.
- Resolução: `apt-cache depends --recurse --no-conflicts --no-breaks --no-replaces`, com `--no-recommends --no-suggests --no-enhances` quando as opcionais estão desabilitadas.
- Download: `apt-get -o APT::Architecture=<arch> download <pacotes>` com `Acquire::Retries=3`.

### 10.2 RPM (CentOS, RHEL, Rocky, AlmaLinux, Fedora)

- O fetcher detecta `dnf` ou `yum` e instala `dnf-plugins-core`/`yum-utils` quando necessário.
- **DNF**: `dnf download --resolve --alldeps --arch=<arch> --destdir=<dir>` (também no DNF5, que removeu `repoquery --resolve`). Com opcionais desabilitadas, aplica `--setopt=install_weak_deps=False`.
- **YUM**: `repoquery --requires --resolve --recursive` para listar e `yumdownloader --resolve --archlist=<arch>` para baixar.
- **CentOS 7** é EOL: o fetcher reescreve os `.repo` para o mirror `centos-store`, fixa `$releasever` em `7.9.2009` e importa as chaves em `/etc/pki/rpm-gpg`.
- **Derivados RHEL** (Rocky, AlmaLinux, CentOS, RHEL): o OPF tenta instalar `epel-release` (com fallback para o RPM oficial do Fedora no UBI) e habilita CRB/PowerTools quando existir. Fedora não recebe EPEL.
- Nomes de pacote seguem a distro de destino: `mtr-tiny` é Debian/Ubuntu; em Rocky/RHEL o equivalente é `mtr`.

### 10.3 APK (Alpine)

- Resolução: `apk fetch --simulate --recursive --arch=<arch>`, com cache isolado em `<workdir>/apk-cache`.
- Download: `apk fetch --recursive --arch=<arch> --output=<workdir>`.
- APK não tem distinção forte de dependências opcionais: a flag é efetivamente ignorada.
- Com `--repo-insecure-gpg` em repositório adicional, o fetcher habilita `--allow-untrusted` no download.

### 10.4 Verificação no ambiente (P1)

Antes de copiar qualquer arquivo, o ambiente valida os artefatos com ferramentas nativas (`.deb`: `dpkg-deb --info`; `.rpm`: `rpm -K`; `.apk`: `apk verify`). O inventário coleta nome, versão, arquitetura e SHA-256 de cada arquivo; nada é copiado antes dessa etapa.

## 11. Repositórios adicionais

Repositórios adicionais existem **apenas no ambiente isolado**. O destino recebe somente artefatos; nenhuma configuração de repositório é escrita nele.

### 11.1 Schema JSON (`--repo-config`)

O arquivo deve conter um array JSON. Campos desconhecidos são rejeitados; o limite é 1 MiB; caminhos relativos de `key_file`/`ca_file` são resolvidos contra o diretório do arquivo.

| Campo | Tipo | Obrigatório | Regras |
|:---|:---|:---|:---|
| `name` | string | sim | `[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}`; duplicatas (mesmo slug) são rejeitadas |
| `family` | string | não | `apt`, `rpm` ou `apk`; default = família do alvo; precisa coincidir com o alvo |
| `url` | string | sim | `http`/`https`, com host, sem usuário/senha, sem fragmento, sem caracteres de controle; `$basearch` só em RPM |
| `key_file` | string | conforme | chave local; exclusiva com `key_url` |
| `key_url` | string | conforme | chave remota; exclusiva com `key_file` |
| `ca_file` | string | não | CA customizada; conflita com TLS inseguro e não se aplica a URL `http://` |
| `suite` | string | APT não-flat | ex.: `jammy`, `bookworm`; `[A-Za-z0-9][A-Za-z0-9._/-]*` |
| `components` | []string | APT não-flat | default `["main"]` |
| `flat` | bool | não | APT flat; não aceita `suite`/`components` |
| `insecure_skip_tls_verify` | bool | não | inseguro; **não suportado em APK** |
| `insecure_skip_gpg_check` | bool | não | inseguro; conflita com chave informada |

Sem `key_file`/`key_url`, é obrigatório habilitar `insecure_skip_gpg_check`. A chave deve existir mesmo quando a URL é HTTP (assinatura continua obrigatória por padrão).

### 11.2 Exemplos

**APT com chave remota e CA interna**

```json
[
  {
    "name": "Interno APT",
    "family": "apt",
    "url": "https://repo.interno/apt",
    "suite": "jammy",
    "components": ["main", "updates"],
    "key_url": "https://repo.interno/apt/key.asc",
    "ca_file": "/etc/ssl/certs/interno-ca.pem"
  }
]
```

**RPM com chave local e variável de arquitetura**

```json
[
  {
    "name": "Interno RPM",
    "family": "rpm",
    "url": "https://repo.interno/el9/$basearch",
    "key_file": "/etc/pki/rpm-gpg/RPM-GPG-KEY-interno"
  }
]
```

**APK com CA customizada (sem TLS inseguro)**

```json
[
  {
    "name": "Interno APK",
    "family": "apk",
    "url": "https://repo.interno/alpine/v3.20/main",
    "key_file": "./interno.rsa.pub",
    "ca_file": "./interno-ca.pem"
  }
]
```

**Modo simples (uma única entrada)**

```bash
opf-cli --target ubuntu:22.04 --arch amd64 --pkg mytool \
  --repo-url https://repo.interno/apt \
  --repo-apt-suite jammy --repo-apt-components main \
  --repo-key-url https://repo.interno/apt/key.asc
```

### 11.3 Como cada família é configurada no ambiente

| Família | Arquivos criados (prefixo `opf-<sessão>-<slug>`) | Detalhes |
|:---|:---|:---|
| APT | `/etc/apt/sources.list.d/opf-<prefixo>.list`, `/usr/share/keyrings/opf-<prefixo>.gpg`, `/etc/apt/apt.conf.d/99opf-<prefixo>`, CA em `/etc/apt/opf-<prefixo>-ca.pem` | chave armored convertida com `gpg --dearmor`; `signed-by=` por padrão, `trusted=yes` no opt-in inseguro |
| RPM | `/etc/yum.repos.d/opf-<prefixo>.repo`, chave em `/etc/pki/rpm-gpg/RPM-GPG-KEY-opf-<prefixo>`, CA em `/etc/pki/opf/<prefixo>-ca.pem` | `rpm --import` da chave; `gpgcheck=1`/`0`, `sslverify=1`/`0`, `repo_gpgcheck=0` |
| APK | chave em `/etc/apk/keys/opf-<prefixo>.rsa.pub`; URL acrescentada a `/etc/apk/repositories`; CA concatenada em `/etc/ssl/certs/ca-certificates.crt` | sem suporte a TLS inseguro por repositório |

### 11.4 Cuidados

- A lista é **aditiva**: repositórios oficiais continuam habilitados; não há pin/priority.
- No WSL (ambiente persistente), os arquivos usam prefixo de sessão e são removidos no cleanup; no Docker o contêiner é efêmero.
- Nenhuma credencial de repositório é aceita ou armazenada na v0.2.1.
- Chave e CA são material público e podem aparecer em `config_json` (metadados), mas nunca em relatórios como segredo.
- Em retomadas, a lista é repovoada; se a chave/CA local sumiu, o erro cita o repositório afetado.

## 12. SSH: autenticação, known_hosts, pontes e sudo

### 12.1 Autenticação

- **Chave** (`--ssh-key`/campo Chave privada): aceita `~`; chave é lida e interpretada localmente.
- **Senha** (`--ssh-password`, `OPF_SSH_PASSWORD` ou campo Senha da conta): suportada, mas gera aviso no log (`autenticação SSH por senha habilitada; prefira chave privada`).
- Informar chave **e** senha faz o cliente tentar ambas (chave primeiro).
- Sem chave e sem senha: erro `informe --ssh-key ou --ssh-password/OPF_SSH_PASSWORD`.

### 12.2 Verificação de host key

- Padrão: **estrita**, contra `--ssh-known-hosts` ou `~/.ssh/known_hosts`, em todos os saltos.
- Arquivo ausente/ilegível: `known_hosts "..." indisponível` — resolva com `ssh-keyscan -H <host> >> ~/.ssh/known_hosts` ou informe um arquivo próprio.
- Algoritmos de host key preferidos: `ssh-ed25519`, ECDSA (256/384/521), `rsa-sha2-256/512` e `ssh-rsa`.
- Opt-in inseguro: `--ssh-insecure-skip-host-key`, checkbox **Ignorar verificação de host key** (GUI) ou toggle na TUI. Aplica-se ao destino e a todas as pontes e **não** é persistido na sessão.
- Timeout de handshake: 15 s.

### 12.3 Cadeia de pontes

- As pontes são abertas na ordem informada, cada uma discando através da conexão anterior; o destino é alcançado pela última.
- Cada ponte exige host, usuário e credencial própria; **senha/chave nunca são herdadas** do destino.
- `known_hosts` vazio na ponte herda o `known_hosts` do destino; `InsecureSkipHostKey` é herdado do destino.
- Saltos aninhados (ponte com `JumpHosts`) são rejeitados.
- A lista tem limite prático apenas pelo tempo de conexão; não há limite conceitual de pontes.
- Senhas de ponte vivem somente em memória e não são restauradas na retomada.

Exemplo com duas pontes (a primeira por chave, a segunda por senha):

```bash
OPF_SSH_JUMP_PASSWORDS='["","senha-da-ponte-2"]' \
opf-cli --pkg vim --ssh-host alvo.interno --ssh-user admin \
  --ssh-key ~/.ssh/id_ed25519 \
  --ssh-jump ponte1.interno:2200 \
    --ssh-jump-auth key --ssh-jump-key ~/.ssh/id_ed25519 \
  --ssh-jump admin@ponte2.interno \
    --ssh-jump-auth password
```

### 12.4 Sudo e privilégios

- A senha do `sudo` é **sempre** a senha da conta SSH do destino, enviada por `stdin` (`sudo -S -p '' sh -c '...'`).
- NOPASSWD não é suportado.
- A GUI e a TUI executam `id -u` no destino antes de instalar: se o retorno for `0`, o sudo é dispensado automaticamente; se não for `root` e o sudo não estiver habilitado, a execução falha com `usuário remoto não é root; habilite “Usar sudo” e informe a senha da conta`.
- A CLI **não** faz esse probe: se você já é `root` no destino, não use `--sudo`; se não é, use `--sudo` com senha.
- Senhas com `\r`, `\n` ou NUL são rejeitadas.
- A conexão inicial tenta até **3 vezes** com espera crescente (1 s, 2 s) antes de abortar.
- Diretório remoto padrão: `/tmp/opf-sessao-<id>` (alterável com `--remote-dir`). Em cancelamento, ele só é removido se começar com `/tmp/opf-` ou `/var/tmp/opf-`; fora dessa área o cleanup remoto é recusado por segurança.

## 13. Pipeline de 13 etapas, cancelamento e retomada

### 13.1 As etapas

Os nomes das etapas acompanham o idioma da interface; a tabela abaixo usa os nomes-fonte em português do Brasil. O rótulo gravado em `etapas_sessao` é o que estava ativo quando a etapa rodou, enquanto a GUI e a TUI redesenham os rótulos ao vivo no idioma atual.

| # | Nome | O que acontece | Observações |
|:---:|:---|:---|:---|
| 1 | abrir sessão | cria o registro em `sessoes`, grava `config_json` e os roots em `pacotes_solicitados` | etapa sempre executada no início |
| 2 | pré-check remoto | consulta o gerenciador remoto; pacotes presentes viram `ja_instalado` e saem dos roots | pulada sem SSH ou sem inspector; `--force-reinstall` mantém os roots |
| 3 | criar ambiente isolado | Docker/WSL com a distro/versão exata; valida `uname -m`; configura repositórios adicionais; prepara ferramentas RPM/EPEL | grava `ambiente_id`, `imagem_docker`/`wsl_distro`, `output_dir` e `remote_dir` |
| 4 | resolver dependências | resolve a união das dependências dos roots | usa o resolver da família |
| 5 | baixar pacotes | executa os comandos de download no ambiente | etapas 4 e 5 são concluídas em sequência após resolução+download |
| 6 | copiar artefatos para o host | move os arquivos para `<out>/sessao-<id>`, deduplica nomes e recalcula o hash | divergência gera `cópia corrompida do artefato` |
| 7 | gerar e verificar manifesto | grava `SHA256SUMS` de forma atômica, verifica e registra os pacotes | falha aborta antes de qualquer envio |
| 8 | enviar via SFTP | cria o diretório remoto e envia artefatos + manifesto, com progresso em bytes | pulada sem SSH |
| 9 | verificar checksums no destino | `sha256sum -c SHA256SUMS`; marca `baixado` → `enviado` e `p3_verificado` | pulada sem SSH |
| 10 | confirmar instalação | prompt/diálogo; recusa marca `enviado_nao_instalado` e pula a etapa 11 | exige `--install` (CLI) |
| 11 | instalar remotamente | `dpkg -i` + `apt-get install -f -y`; `dnf`/`yum localinstall -y`; `apk add --allow-untrusted` | com `--sudo`, executa via `sudo -S` |
| 12 | registrar resultado | grava evento e status final | — |
| 13 | cleanup | destrói o contêiner/distro e remove temporários locais | diretório remoto só é removido em cancelamento |

Os nomes das etapas são exibidos na GUI, na TUI e no rodapé de `etapas_sessao`.

### 13.2 Cancelamento

- **CLI**: `Ctrl+C`/`SIGTERM` cancelam o contexto.
- **GUI**: botão **Cancelar execução**.
- **TUI**: tecla `c` durante o pipeline (ou `Ctrl+C` para sair).

Efeitos:

- a sessão termina como **`abortada`** (não `falhou`);
- a etapa corrente é marcada como `falhou` com o motivo do cancelamento;
- o ambiente isolado e os temporários locais são limpos no `defer`;
- se havia SSH, o diretório remoto é removido best-effort (dentro de `/tmp/opf-*` ou `/var/tmp/opf-*`);
- cancelar durante `dpkg`/`yum`/`apk` é best-effort e pode deixar estado parcial no destino.

### 13.3 Retomada por checkpoint

A retomada existe na **GUI** (botão **Retomar**/ação **Continuar**) e na **TUI** (`r` no histórico / **Continuar sessão**). A CLI da v0.2.1 não expõe retomada.

Etapa segura calculada a partir de `etapas_sessao`:

| Estado observado | Retoma em |
|:---|:---|
| etapa 12 concluída | 12 |
| etapa 11 falhou | 10 (nova confirmação obrigatória) |
| etapa 11 concluída | 12 |
| etapa 10 concluída e 11 pulada | 12 |
| etapa 10 concluída | 10 |
| etapa 9 concluída | 10 |
| etapa 8 concluída | 9 |
| etapa 7 concluída | 8 |
| etapas 3–6 concluídas | 3 |
| nenhuma etapa registrada | 1 |
| sem `output_dir` ou manifesto inválido | 3 |

Regras adicionais:

- retomar em etapa ≥ 8 exige transferência SSH configurada; em 10–11 exige instalador e **nova** confirmação;
- senhas (SSH, sudo e pontes) são solicitadas novamente e nunca vêm do banco;
- o manifesto local é revalidado antes de reutilizar artefatos;
- sessões `concluida` não podem ser retomadas (o botão fica desabilitado).

## 14. Banco de dados SQLite

O banco padrão é `./opf.db` (`--db` para alterar; a GUI/TUI permitem configurar em **Configurações Globais**). O driver é `modernc.org/sqlite` (Go puro), com `journal_mode=WAL`, `foreign_keys=ON`, `busy_timeout=5000` e uma única conexão aberta.

### 14.1 Tabelas

| Tabela | Conteúdo |
|:---|:---|
| `sessoes` | alvo, versão, arquitetura, método de fetch, host, `include_optional`, status, `etapa_atual`, `output_dir`, `remote_dir`, `imagem_docker`, `wsl_distro`, `ambiente_id`, `config_json`, `host_info_json`, `arquivada`, timestamps |
| `pacotes` | nome, versão, arquitetura, status, `eh_dependencia`, `caminho_local`, `sha256`, `sha256_p1`, `sha256_p2`, `versao_anterior`, `p3_verificado`, `instalado_verificado` |
| `eventos` | timestamp, tipo (`download`, `transferencia`, `instalacao`, `erro`, `info`, `precheck`), mensagem, `detalhes` (JSON livre) |
| `etapas_sessao` | etapa (1–13), nome, status (`pendente`, `em_andamento`, `concluida`, `falhou`, `pulada`), início/fim e erro; PK `(sessao_id, etapa)` |
| `pacotes_solicitados` | roots informados pelo operador e `motivo` opcional |

Status de sessão: `em_andamento`, `concluida`, `falhou`, `abortada`.
Status de pacote: `baixado`, `enviado`, `enviado_nao_instalado`, `instalado`, `falhou`, `ja_instalado`.

Migrações: o `store.Open` cria o schema quando necessário, adiciona colunas ausentes (`ensureColumns`) e migra bancos antigos que ainda não conheciam `enviado_nao_instalado`.

### 14.2 Consultas úteis

```sql
-- últimas sessões não arquivadas
SELECT id, so_destino, versao_destino, arch_destino, metodo_fetch,
       status, etapa_atual, host_destino
FROM sessoes
WHERE arquivada = 0
ORDER BY id DESC;

-- pacotes de uma sessão
SELECT nome, versao, arquitetura, status, eh_dependencia, sha256
FROM pacotes
WHERE sessao_id = 12
ORDER BY nome;

-- etapas e falhas
SELECT etapa, nome, status, iniciada_em, finalizada_em, erro
FROM etapas_sessao
WHERE sessao_id = 12
ORDER BY etapa;

-- eventos em ordem cronológica
SELECT timestamp, tipo, mensagem, detalhes
FROM eventos
WHERE sessao_id = 12
ORDER BY id;
```

Use o cliente `sqlite3` (não distribuído pelo OPF):

```bash
sqlite3 opf.db "SELECT id, status FROM sessoes ORDER BY id DESC LIMIT 5;"
```

Como o banco fica em modo WAL, os arquivos `opf.db-wal` e `opf.db-shm` aparecem ao lado do banco; eles são normais e devem ser preservados junto do `.db` ao mover o histórico.

## 15. Relatórios e Syslog

### 15.1 Relatório JSON

Gerado por `--export-json` (CLI) ou **Exportar JSON** (GUI/TUI). Estrutura:

| Campo | Conteúdo |
|:---|:---|
| `sessao` | registro completo da sessão (alvo, status, timestamps, caminhos, config) |
| `host_info` | host, usuário SSH, kernel, uptime, CPU, memória e disco coletados no destino |
| `pacotes_solicitados` | roots informados |
| `pacotes` | lista de pacotes com `papel` (`principal` ou `dependente`), versão anterior, hashes e status |
| `eventos` | eventos persistidos |
| `local_destino` / `remote_destino` | diretórios usados |
| `gerado_em` | timestamp UTC da geração |

### 15.2 Relatório Markdown

Seções: **Host remoto**, **Destino e armazenamento**, **Pacotes solicitados**, **Pacotes** (tabela com papel, versão, versão anterior, arquitetura, status e SHA-256) e **Eventos**. O rodapé informa que a exportação foi gerada localmente, sem segredos. Os rótulos e o rodapé acompanham o idioma da interface; os nomes acima são as strings-fonte em português do Brasil. Os relatórios são renderizados no idioma ativo no momento da exportação; as mensagens de evento gravadas no SQLite mantêm o idioma em que foram registradas, e chaves JSON, valores de papel (`principal`/`dependente`) e códigos de status nunca são traduzidos.

Arquivos são gravados com permissão `0600`; diretórios intermediários são criados com `0700` quando necessário.

### 15.3 Syslog externo

- Transporte RFC 5424 sobre **UDP ou TCP**; o sink é assíncrono (fila de 512 eventos) e nunca bloqueia o pipeline.
- Formato: `<PRI>1 <timestamp RFC3339Nano> <hostname> <tag> - - - <JSON>`, com `JSON` contendo `sessao_id`, `tipo`, `mensagem` e, quando houver, `detalhes`.
- PRI `14` (user.info) para eventos informativos e `11` (user.err) para erros.
- Retentativas: 3 tentativas com backoff exponencial (200 ms, 400 ms, 800 ms) e timeout de 2 s por envio; falha é registrada como aviso e o evento permanece no SQLite.
- Tag padrão `opf`; validação `[A-Za-z0-9][A-Za-z0-9._-]*`.
- O relatório também pode ser enviado como evento (`relatorio`) pelos botões/atalhos da GUI e da TUI, no formato `<14>1 2026-11-21T12:00:00Z host opf - - - {"sessao_id":12,...}`.

## 16. Solução de problemas

| Sintoma | Causa provável | Ação |
|:---|:---|:---|
| `known_hosts "..." indisponível` | arquivo ausente ou caminho errado | `ssh-keyscan -H host >> ~/.ssh/known_hosts` ou `--ssh-known-hosts <arquivo>` |
| `host key de ... não confere` | host key mudou ou `known_hosts` desatualizado | valide com `ssh host` e atualize a entrada; evite o opt-in inseguro |
| `conectar ao destino ...: connection refused` | SSH indisponível/porta errada | teste `nc -vz host 22`, confira `--ssh-port` |
| `--ssh-jump 1: ...` | formato de ponte inválido | use `[usuario@]host[:porta]`; IPv6 exige colchetes |
| `criar contêiner Docker: ...` | daemon parado/sem permissão | `docker info`; inicie o daemon ou ajuste o grupo do usuário |
| `arquitetura do contêiner ("...") difere da solicitada` | imagem não é multi-arch | ajuste `--arch` ou use `--image` compatível |
| `imagem ... not found` / pull lento | registry inacessível | valide a rede/proxy do Docker; autentique no registry se necessário |
| `distro WSL "..." não registrada (registradas: ...)` | nome diferente do registrado | `wsl --list --verbose` e ajuste `--wsl-distro` |
| `distro WSL "..." tem versão "...", esperada "..."` | alvo não corresponde à distro | alinhe `--target`/`--wsl-distro` |
| `execução WSL não está como root` | distro configurada com usuário comum | execute com `-u root` ou ajuste o `/etc/wsl.conf` |
| `WSLFetcher disponível apenas no Windows` | binário Linux/macOS com `--fetch-method wsl` | use `docker` |
| `pacote indisponível para <distro>:<versão>` | nome incorreto para a distro ou repositório ausente | confira o nome da distro (ex.: `mtr` vs `mtr-tiny`) e adicione `--repo-config` |
| `No matching Packages to list` | índices do ambiente sem o repositório | verifique URL/suite/components e conectividade |
| `checksum divergente para "..."` | arquivo corrompido no host | remova o diretório de saída e repita; verifique disco/rede |
| `cópia corrompida do artefato "..."` | corrupção entre ambiente e host | repita a sessão; investigue armazenamento |
| `verificar checksums no destino: comando remoto saiu com status 1` | upload truncado ou `sha256sum` ausente | verifique espaço em `/tmp`, reenvie, instale `coreutils` no destino |
| `opf-gui requer CGO_ENABLED=1 ...` | GUI compilada sem CGO | `make build-gui` com toolchain gráfica |
| `usuário remoto não é root; habilite “Usar sudo” ...` | GUI/TUI sem sudo para conta comum | marque **Usar sudo** e informe a senha |
| `--yes exige --install` / `--sudo exige --install` / `--install exige --ssh-host` | combinação de flags inconsistente | inclua as flags exigidas e informe `--ssh-host`/`--ssh-user` |
| `nenhum artefato retornado` | resolução vazia | valide o nome do pacote e a conectividade do ambiente |
| `SQLite indisponível` | caminho inválido/sem permissão | ajuste o caminho em **Configurações Globais** ou `--db` |

## 17. Checklist de hardening

Antes de operar em produção:

- [ ] Prefira **chave SSH** com passphrase e remova autenticação por senha quando possível.
- [ ] Mantenha o `known_hosts` versionado/gerenciado e **nunca** use `--ssh-insecure-skip-host-key` fora de laboratório.
- [ ] Não use NOPASSWD: o OPF usa `sudo -S` com a senha da conta e root é detectado por `id -u`.
- [ ] Restrinja o usuário SSH ao mínimo necessário (SFTP + `dpkg`/`dnf`/`apk` via sudo).
- [ ] Valide os repositórios adicionais com **GPG e TLS**; evite `insecure_skip_gpg_check`/`insecure_skip_tls_verify`.
- [ ] Fixe imagens Docker (`--image`) quando precisar de reprodutibilidade.
- [ ] Proteja o banco: `chmod 600 opf.db` e cuidado com `opf.db-wal`/`opf.db-shm`.
- [ ] Valide o `SHA256SUMS` recebido no destino antes de instalar (o pipeline já faz P3; confira em auditorias).
- [ ] Mantenha o Syslog em rede gerencial; o transporte é UDP/TCP sem TLS na v0.2.1.
- [ ] Revise os relatórios exportados antes de compartilhar (contêm host, usuário e kernel).
- [ ] Rode o OPF com um usuário local sem privilégios; o acesso remoto é o único privilegiado.
- [ ] Atualize Docker/WSL e o próprio OPF; acompanhe o changelog e avisos de segurança.

## 18. Perguntas frequentes

| Pergunta | Resposta |
|:---|:---|
| Preciso de internet no servidor de destino? | Não. Apenas a máquina que executa o OPF precisa alcançar os repositórios; o destino só recebe arquivos e executa a instalação local. |
| O OPF instala sem eu confirmar? | Não. A instalação exige confirmação explícita; `--yes` é opt-in e continua exigindo `--install`. |
| E se eu recusar a instalação? | Os pacotes ficam `enviado_nao_instalado`, o diretório remoto é preservado e a sessão conclui com `instalação: recusada; pacotes permanecem enviados`. |
| Posso informar vários pacotes? | Sim: repita `--pkg` ou separe por vírgula. Cada sessão gera um manifesto, uma transferência e uma instalação. |
| Como incluir dependências opcionais? | `--with-optional` (APT: Recommends/Suggests/Enhances; DNF: weak deps). APK não distingue opcionais. |
| O OPF configura os repositórios no destino? | Não. Repositórios adicionais existem apenas no ambiente isolado de download. |
| Como retomar uma sessão que falhou? | Use a GUI (**Continuar**/**Retomar**) ou a TUI (`r` no histórico). A CLI da v0.2.1 não retoma, e etapas de instalação nunca são retomadas sem nova confirmação. |
| Minhas senhas ficam salvas? | Não. Senhas de SSH, sudo e pontes ficam apenas em memória; `config_json` guarda somente metadados. |
| Por que o download não mostra MB/s? | Porque `apt`, `dnf` e `apk` não fornecem métricas estruturadas confiáveis; bytes reais aparecem apenas no upload SFTP, controlado pelo OPF. |
| Funciona no macOS? | Sim, como plataforma secundária, com Docker. A GUI requer Xcode Command Line Tools. |
| E no Windows sem Docker? | Use WSL2 com uma distro Ubuntu/Debian da mesma versão do alvo; apenas a família APT é suportada. |
| Onde ficam os arquivos baixados? | Em `<out>/sessao-<id>/` com o manifesto `SHA256SUMS`. O padrão é `./opf-output`. |
| Posso usar um repositório privado com usuário e senha? | Não na v0.2.1. URLs com credenciais são rejeitadas; use chave/CA e, se necessário, um proxy autenticado na rede. |
| Posso mudar o idioma da interface? | Sim. A GUI e a TUI oferecem **Idioma da interface** em **Configurações Globais** (`auto`, `en`, `pt-BR`, `es`), a CLI aceita `--lang` e a variável `OPF_LANG` define o padrão quando a opção é `auto`. O padrão segue o idioma do sistema, com inglês como fallback. |
| Como auditar o que foi instalado? | Consulte o relatório JSON/Markdown da sessão (papel principal/dependente, versão anterior, status, hashes) e as tabelas `pacotes`, `eventos` e `etapas_sessao`. |

## 19. Glossário

| Termo | Definição |
|:---|:---|
| **APT** | Gerenciador de pacotes de Ubuntu/Debian; artefatos `.deb`. |
| **APK** | Gerenciador de pacotes do Alpine; artefatos `.apk`. |
| **Artefato** | Arquivo de pacote baixado e validado. |
| **Checkpoint** | Etapa persistida que permite retomada segura. |
| **Cleanup** | Remoção do ambiente isolado e dos temporários ao final da sessão. |
| **DNF/YUM** | Gerenciadores RPM; artefatos `.rpm`. |
| **EPEL** | Repositório extra para derivados RHEL, habilitado automaticamente no ambiente isolado. |
| **Fetch method** | Forma de criar o ambiente isolado: `docker` ou `wsl`. |
| **Host key** | Chave pública do servidor SSH, validada via `known_hosts`. |
| **Manifesto** | Arquivo `SHA256SUMS` com os hashes dos artefatos da sessão. |
| **P1/P2/P3** | Verificações de integridade no ambiente, no host e no destino. |
| **Ponte (jump host)** | Salto SSH intermediário até o destino. |
| **Pré-check** | Consulta ao gerenciador remoto para evitar baixar pacotes já instalados. |
| **Preset** | Combinação distro/versão/imagem validada por testes de integração. |
| **Root** | Pacote solicitado explicitamente pelo operador. |
| **Sessão** | Execução completa do pipeline, identificada por `ID`. |
| **Sudo** | Elevação de privilégio no destino usando a senha da conta SSH via `stdin`. |
| **Syslog** | Envio assíncrono de eventos em RFC 5424 (UDP/TCP). |

## 20. Versão e licença

- **Produto:** Offline Package Fetcher (OPF)
- **Versão deste manual:** 0.2.1
- **Licença:** GNU General Public License v3.0 — consulte [LICENSE](../LICENSE)
- **Repositório:** <https://github.com/ph7ti/Offline-Package-Fetcher>
- **Changelog:** [CHANGELOG.md](../CHANGELOG.md)
- **Documentação relacionada:** [README PT-BR](README.pt-BR.md) · [README EN](../README.md) · [Manual EN](MANUAL.md) · [Segurança PT-BR](SECURITY.pt-BR.md) · [Documento técnico](project-description.pt-BR.md) · [Especificação GUI/TUI](specs/gui-tui.pt-BR.md)

O OPF foi desenvolvido com apoio de ferramentas de IA sob revisão humana. O software é fornecido “como está”, sem garantias além das previstas na GPL-3.0; valide em laboratório antes de usar em ambientes críticos.
