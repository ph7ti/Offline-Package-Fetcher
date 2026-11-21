# Definições para GUI e TUI — Offline Package Fetcher

**Versão:** 1.2 (SSH-first, detecção de target e host key opt-in)
**Escopo:** F6.0, F6 (Fyne) e F7 (Bubble Tea); F8 apenas como boundary.

## 0) DECISÕES CONSOLIDADAS PARA F6/F7

Esta seção prevalece sobre os wireframes abaixo em caso de conflito.

| Tema | Decisão |
|---|---|
| Sudo | Sempre a senha da conta SSH, via `sudo -S`/stdin; detectar root para dispensar sudo; **NOPASSWD removido** |
| Pacotes | Vários roots por sessão; um manifesto, uma transferência e uma instalação |
| Estado | Pré-check remoto + retomada por checkpoint seguro |
| Jump hosts | Lista ordenada de pontes, cada uma com host, porta, usuário, modo de autenticação (`senha`, `chave` ou `chave+senha`), senha em memória e `known_hosts`; sem herança de senha/chave do destino |
| Host key | Estrita por padrão; opt-in explícito e inseguro para ignorar (equivale a StrictHostKeyChecking=no) |
| GUI/TUI | Fyne em binário separado (`CGO_ENABLED=1`); TUI/CLI `CGO_ENABLED=0` |
| Histórico | Arquivamento lógico; sem deleção física |
| Visual GUI | Shell desktop compacto, tema claro/escuro alternável, ícone do app, paleta #fb9632/#2191d5/#1b222d/#c9ced3; banco SQLite em Configurações |
| Configurações | JSON em `UserConfigDir`, sem segredos |
| Relatórios/Syslog | F8; F6/F7 apenas exibem o relatório local |
| Alvos | Ubuntu 22.04/24.04, CentOS 7 preparado, Alpine 3.20; Debian 12 após teste |

### 0.0 Fluxo SSH-first

1. A tela de SSH é o primeiro passo.
2. “Detectar destino” lê `/etc/os-release` e `uname -m`, preenchendo SO, versão, arquitetura e imagem recomendada.
3. “Continuar” segue para pacotes com os dados detectados; se nada foi detectado, a própria ação dispara a detecção.
4. “Avançado” abre a configuração manual, que também é usada no modo “Apenas baixar”; nesse caminho os campos precisam ser preenchidos explicitamente.
5. Nenhuma sessão SSH usa Ubuntu 22.04 ou outra suposição padrão sem detecção/teste ou configuração manual explícita.

### 0.1 Numeração

Os wireframes antigos usam “Etapa X de 13” tanto para telas quanto para o pipeline. Na implementação:

- **Tela X de 13**: navegação da interface (Boas-vindas, Configuração, SSH, Pacotes, Pré-check, Pipeline, Integridade, Sudo, Instalação, Resultado, Relatório, Histórico, Configurações).
- **Passo Y/13**: etapas reais do pipeline definidas na seção 6 do `Project-description.md`.
- Textos da UI devem sempre dizer “Tela” ou “Passo” explicitamente; não reutilizar “Etapa” para ambos.

### 0.2 Modo “Apenas Baixar”

Sem SSH, o pipeline executa 8 passos: **1, 3, 4, 5, 6, 7, 12, 13**. Ficam ocultos 2 (pré-check remoto), 8 (upload), 9 (P3), 10 (confirmação) e 11 (instalação). A UI não deve exibir 10 passos; deve exibir a lista de 8.

### 0.3 Status padronizados

| Situação | Status |
|---|---|
| Baixado e manifesto válido | `baixado` |
| Transferido e P3 válido | `enviado` |
| Usuário recusou instalar | `enviado_nao_instalado` |
| Instalado e confirmado | `instalado` |
| Já presente no pré-check | `ja_instalado` |
| Falha de instalação | `falhou` |
| Sessão cancelada pelo usuário | `abortada` (sessão), não `falhou` |

### 0.4 Retomada por checkpoint seguro

- Passos 1–7: reutilizar artefatos se o `SHA256SUMS` local ainda verificar; caso contrário, refazer o download.
- Passos 8–9: reconectar, reenviar e revalidar; operações idempotentes.
- Passos 10–11: nunca continuar instalação automaticamente; reexibir confirmação, reconsultar o destino e exigir nova decisão.
- Passos 12–13: registrar e limpar.
- Senhas são sempre solicitadas novamente; nunca persistidas. Isso inclui a senha da conta SSH e as senhas das pontes, que voltam em branco ao retomar.
- Ambientes órfãos devem ser limpos antes de retomar.
- Se a retomada não for segura, oferecer apenas relatório/arquivamento.

### 0.5 Segurança e produtos proibidos

- **NOPASSWD** não é suportado; a UI não deve exibir detecção/checkbox de NOPASSWD.
- **Host key** é estrita por padrão; existe checkbox “Ignorar verificação” com aviso vermelho, aplicado a todos os hops e nunca habilitado por padrão.
- CLI usa `--ssh-insecure-skip-host-key` como opt-in inseguro.
- Senhas/chaves nunca vão para SQLite, JSON de configuração, logs ou relatórios.
- A senha de sudo é sempre a da conta SSH do destino, nunca a senha de uma ponte.
- Alpine mantém `--allow-untrusted` por exigência do projeto, com aviso explícito; P1 valida os `.apk` antes.

### 0.6 Progresso e logs

- Download: progresso por etapa e quantidade de artefatos; sem MB/s fictício de `apt`/`yum`/`apk`.
- Upload SFTP: bytes reais e velocidade.
- Instalação: saída remota em streaming, não apenas ao final.
- Cancelamento durante instalação é best-effort; a UI deve avisar sobre possível estado parcial no destino.
- P1/P2 por pacote; P3 por arquivo quando o parsing for confiável, com fallback agregado explícito.
- Resultado individual de instalação deve ser confirmado no gerenciador quando possível; caso contrário, rotular como agregado.

### 0.7 Configurações, histórico e F8

- Configurações globais não sensíveis em JSON; temp dir e timeout chegam ao pipeline.
- Histórico: “Arquivar” (`arquivada`) em vez de “Deletar”.
- Exportação JSON/Markdown e Syslog estão implementados na F8; eventos vão ao Syslog em background.
- Não prometer retry/backoff além do que for implementado na F6.0.

---

## A) MAPA DE FLUXO COMPLETO

O fluxo descreve a jornada do usuário do início ao fim, detalhando decisões críticas, validações de ponte SSH, sudo e caminhos de tratamento de erro.

```text
[ INÍCIO ]
    │
    ▼
+──────────────────────────+
│  1. Boas-Vindas / Modo   │◄─────────────────────────────┐
+──────────────────────────+                              │
    │                                                     │
    ├──► [Histórico de Sessões] ──► (Ver Relatório) ─────┤
    │                                                     │
    └──► [Nova Sessão / Continuar]                        │
              │                                           │
              ▼                                           │
+──────────────────────────────────────────────────+      │
│  2. Configuração de Sessão (Destino e Método)    │      │
│     - SO, Versão, Arch                           │      │
│     - Método: Docker, WSL ou Auto                │      │
│     - Flag Dependências Opcionais                │      │
+──────────────────────────────────────────────────+      │
    │                                                     │
    ▼                                                     │
+──────────────────────────────────────────────────+      │
│  3. Configuração de SSH & Ponte (F1)             │      │
│     - Destino (Host, User, Port, Key/Pass)       │      │
│     - Ativar Ponte? (Ponte, User, Port, Key/Pass)│      │
│     - Validar conexões                           │      │
+──────────────────────────────────────────────────+      │
    │                                                     │
    ├──► [Falha na Ponte] ────► (Exibe Erro Ponte) ──────┤ (Retorna p/ ajustar)
    ├──► [Falha no Destino] ──► (Exibe Erro Destino) ────┤
    │                                                     │
    ├──► [Apenas Baixar (Sem SSH)] ───┐                   │
    ▼                                 │                   │
+──────────────────────────────────+  │                   │
│  4. Entrada de Pacotes           │  │                   │
│     - Adicionar nome(s)          │  │                   │
│     - Listar para validação      │  │                   │
+──────────────────────────────────+  │                   │
    │                                 │                   │
    ▼ (Se SSH Ativo)                  │                   │
+──────────────────────────────────+  │                   │
│  5. Pré-Verificação SSH          │  │                   │
│     - Descobre o que já está     │  │                   │
│       instalado no destino       │  │                   │
│     - Exibe itens que serão      │  │                   │
│       pulados / atualizados      │  │                   │
+──────────────────────────────────+  │                   │
    │                                 │                   │
    └──────────────┬──────────────────┘                   │
                   │                                      │
                   ▼                                      │
+──────────────────────────────────────────────────+      │
│  6. Execução do Pipeline (13 Passos)             │      │
│     - Download, Cópia, Manifesto, Transferência  │      │
│     - Cancelar a qualquer momento (com Cleanup)  │      │
+──────────────────────────────────────────────────+      │
    │                                                     │
    ├──► [Erro em qualquer Passo] ──► (Verificar Erro) ───┤ (Ajusta e reinicia)
    │                                                     │
    ▼                                                     │
+──────────────────────────────────────────────────+      │
│  7. Verificação de Integridade (P1 -> P2 -> P3)   │      │
│     - P1: Local Docker/WSL                       │      │
│     - P2: Cópia Host (SHA256SUMS)                │      │
│     - P3: Remoto (Apenas se SSH ativo)           │      │
+──────────────────────────────────────────────────+      │
    │                                                     │
    ├──► (Falhou P1/P2) ──► Aborta (Cleanup) ─────────────┤
    ├──► (Falhou P3) ─────► Aborta SSH (Cleanup) ────────┤
    │                                                     │
    ▼ (Apenas SSH ativo; se sem SSH vai p/ etapa 11)      │
+──────────────────────────────────────────────────+      │
│  8. Confirmação de Instalação & Sudo (F2)        │      │
│     - Resumo final dos pacotes                   │      │
│     - Detecta root? Root dispensa sudo           │      │
│     - Não-root: senha da conta SSH via stdin     │      │
+──────────────────────────────────────────────────+      │
    │                                                     │
    ├──► [Recusar Instalação] ──► enviado_nao_instalado ┐│
    ▼                                                   ││
+──────────────────────────────────────────────────+    ││
│  9. Instalação Remota                            │    ││
│     - Aplica com sudo mascarado                  │    ││
│     - Monitora logs dpkg/yum/apk                 │    ││
+──────────────────────────────────────────────────+    ││
    │                                                   ││
    ├──► [Falha de Sudo/Instalação] ──► Erro e retorno ─┼┘ (Opções de retry)
    ▼                                                   │
+──────────────────────────────────────────────────+    │
│  10. Resultado Final                             │◄───┘
│      - Status de todos os pacotes                │
+──────────────────────────────────────────────────+
    │
    ▼
+──────────────────────────────────────────────────+
│  11. Relatório de Sessão                         │
│      - Exportar JSON/Markdown               │
│      - Syslog externo (F8, best-effort)          │
+──────────────────────────────────────────────────+
    │
    ▼
[ FIM (Volta para Welcome ou Fecha App) ]
```

---

## B) INVENTÁRIO DE TELAS

### 1. Boas-Vindas / Seleção de Modo
*   **Propósito:** Ponto de entrada do usuário; seleção entre criar uma nova sessão, retomar uma sessão inacabada ou acessar o histórico local de sessões.

#### GUI Wireframe (Fyne)
```text
+--------------------------------------------------------------------------+
|  Offline Package Fetcher (OPF)                                  [ - ][ x ]|
+--------------------------------------------------------------------------+
|                                                                          |
|  BEM-VINDO AO OFFLINE PACKAGE FETCHER (OPF)                              |
|  Ferramenta de automação de pacotes para ambientes isolados de rede.      |
|                                                                          |
|  +--------------------------------------------------------------------+  |
|  |  [+] INICIAR NOVA SESSÃO                                            |  |
|  |  Crie uma nova rotina de download e transferência de pacotes.      |  |
|  +--------------------------------------------------------------------+  |
|  |  [↺] CONTINUAR SESSÃO ANTERIOR                                      |  |
|  |  Retomar: ID #204 - Ubuntu 22.04 (Parado na etapa de download)       |  |
|  +--------------------------------------------------------------------+  |
|  |  [📜] HISTÓRICO DE SESSÕES                                           |  |
|  |  Ver logs, relatórios anteriores e exportar dados de sessões passadas.|  |
|  +--------------------------------------------------------------------+  |
|                                                                          |
+--------------------------------------------------------------------------+
| Versão 1.1.0 | sqlite: OK | WSL: Disponível | Docker: Ativo | [⚙️ Globais] |
+--------------------------------------------------------------------------+
```

#### TUI Wireframe (Bubble Tea)
```text
OFFLINE PACKAGE FETCHER (OPF) v1.1.0
===================================================

Escolha uma opção para iniciar:

  ▶ 1. [ Nova Sessão ]
       Criar nova rotina de download e transferência

    2. [ Continuar Última Sessão ] (ID: #204 • Ubuntu 22.04)

    3. [ Histórico de Sessões ]
       Verificar sessões anteriores, relatórios e logs

    4. [ Configurações Globais ]
       Ajustar Syslog, diretórios e timeouts


───────────────────────────────────────────────────
WSL: OK  •  Docker: OK  •  SQLite: Conectado
↑/↓: Selecionar  •  ENTER: Confirmar  •  Q: Sair
```

*   **Componentes:** Botões com ícones ("Nova Sessão", "Continuar Sessão", "Histórico de Sessões", "Configurações Globais"), barra de status inferior indicando saúde dos subsistemas (SQLite, Docker, WSL).
*   **Estados:**
    *   *Continuar Sessão Desabilitado:* Se não houver histórico de sessão inacabada no SQLite (botão/link cinza, inativo).
*   **Ações:**
    *   Primária: Iniciar Nova Sessão (`Tab` / `Enter` / Clique).
    *   Secundárias: Histórico (`H`), Configurações (`G`), Continuar (`C`), Sair (`Ctrl+C` ou `Q`).
*   **Retomada:** “Continuar” usa checkpoint seguro (seção 0.4). Se a sessão não for retomável com segurança (por exemplo, instalação já iniciada), a UI oferece apenas relatório/arquivamento e explica o motivo. Senhas são solicitadas novamente.

---

### 2. Configuração de Sessão
*   **Propósito:** Definir o SO/versão/arquitetura de destino, o método de fetch e se dependências opcionais serão consideradas.

#### GUI Wireframe (Fyne)
```text
+--------------------------------------------------------------------------+
| OPF - Nova Sessão                                                [ - ][ x ]|
+--------------------------------------------------------------------------+
|  [Tela 2 de 13] Configuração da Sessão de Destino                       |
|                                                                          |
|  SISTEMA OPERACIONAL DE DESTINO                                           |
|  SO:      [ Debian/Ubuntu       [v] ]  Arquitetura: [ amd64     [v] ]    |
|  Versão:  [ 22.04 (jammy)       [v] ]                                    |
|                                                                          |
|  AMBIENTE ISOLADO DE FETCH                                               |
|  Método:  (•) Automático (WSL compatível no Windows; senão Docker/Linux/macOS)         |
|           ( ) Apenas Docker                                              |
|           ( ) Apenas WSL (somente APT)                                                 |
|                                                                          |
|  OPÇÕES DE DEPNDÊNCIAS                                                   |
|  [x] Incluir dependências opcionais (Recommends/Suggests/Weak)           |
|                                                                          |
|  AVISO: O ambiente temporário será criado e destruído ao final do fluxo.  |
|                                                                          |
+--------------------------------------------------------------------------+
|  [⚙️ Configs]                                     [ Cancelar ] [ Próximo ]|
+--------------------------------------------------------------------------+
```

#### TUI Wireframe (Bubble Tea)
```text
NOVA SESSÃO • CONFIGURAÇÃO DO DESTINO (Tela 2/13)
===================================================

SISTEMA OPERACIONAL DO ALVO:
  SO:      [ Debian/Ubuntu       ] (Espaço para alterar)
  Versão:  [ 22.04 (jammy)       ]
  Arch:    [ amd64               ]

AMBIENTE DE EXECUÇÃO:
  (*) Automático (Recomendado: WSL)
  ( ) Docker (Forçar uso de contêiner)
  ( ) WSL (Forçar uso de subsistema)

OPÇÕES ADICIONAIS:
  [X] Incluir dependências opcionais (Recommends/Suggests)

───────────────────────────────────────────────────
TAB: Mover campo  •  ESPAÇO: Marcar/Selecionar  •  ENTER: Confirmar e Avançar
ESC: Voltar       •  CTRL+C: Sair
```

#### Repositórios adicionais (na Configuração da Sessão)

*   A lista é configurada na própria tela de Configuração da Sessão, abaixo do ambiente de download. Na GUI, cada item é um card com remover e “Procurar” para chave/CA; na TUI, `ctrl+r` abre a lista (`a` adiciona, `e`/`enter` edita, `d` remove, `esc` volta).
*   Campos comuns: nome, URL, origem da chave (arquivo, URL ou “sem chave (inseguro)”), chave, CA customizada e TLS inseguro. Em APT: suite, components e flat. Em APK, TLS inseguro fica indisponível; use CA customizada.
*   A lista é aditiva e não substitui os repositórios oficiais. Não há autenticação nesta versão; URLs com `user:password@` são rejeitadas. A chave/CA podem ser persistidas; nenhum segredo é gravado.
*   A família é a do alvo. Se o alvo mudar depois de configurar repositórios, a execução é bloqueada com aviso claro até remover/ajustar os itens incompatíveis.
*   Os repositórios são aplicados **somente no ambiente isolado de download**; o destino remoto continua recebendo apenas os artefatos locais.

*   **Validações:**
    *   O SO de destino deve ser escolhido entre os **presets validados**: Ubuntu 22.04/24.04, CentOS 7 (imagem preparada), Alpine 3.20; Debian 12 após teste dedicado. Versões fora da matriz ficam em “avançado” com imagem explícita.
    *   **Automático:** no Windows, usar WSL somente se houver distro Ubuntu/Debian compatível com a versão escolhida; caso contrário Docker. Em Linux/macOS, Docker. “Apenas WSL” fica desabilitado para famílias RPM/APK ou distro incompatível.
    *   O preset CentOS 7 usa a imagem preparada `opf/centos-7:f3`; `centos:7` oficial não é suficiente por causa dos repositórios EOL.
    *   Verificação imediata se o método de fetch (Docker/WSL) selecionado está disponível no host.

---

### 3. Configuração SSH e Lista de Pontes SSH (F1)
*   **Propósito:** Coletar dados de acesso ao destino final e de uma **lista ordenada de servidores de salto (Bastion/Jump Hosts)**, permitindo validação individual prévia. A primeira ponte conecta diretamente; cada ponte seguinte passa pela anterior.

#### GUI Wireframe (Fyne)
```text
+--------------------------------------------------------------------------+
| OPF - Configurações SSH                                          [ - ][ x ]|
+--------------------------------------------------------------------------+
|  [Tela 3 de 13] Conexão SSH & Lista de Pontes (Jump Hosts)               |
|                                                                          |
|  [x] ATIVAR ENVIO VIA SSH (Se desmarcado, a sessão apenas baixará no host) |
|                                                                          |
|  +-- PONTES SSH (BASTION / JUMP HOSTS) — LISTA [Opcional] -------------+ |
|  | 1. Host: [ bastion1.transit.lan ] Port: [ 22 ] User: [ bridge1    ] | |
|  |    Auth: ( ) Senha  (•) Chave Privada: [ ~/.ssh/id_rsa          [Browse] ] |
|  |    Known Hosts: [ ~/.ssh/known_hosts                            [Browse] ] |
|  |    Validar: [ TESTAR PONTE ] -> Estado: [ CONECTADO (Sucesso!)    ] | |
|  | 2. Host: [ bastion2.transit.lan ] Port: [2222] User: [ bridge2    ] | |
|  |    ...                                                              | |
|  | [+ Adicionar ponte]  [Remover selecionada]  [Mover ↑/↓]             | |
|  +---------------------------------------------------------------------+ |
|                                                                          |
|  +-- DESTINO FINAL (TARGET REMOTE) ------------------------------------+ |
|  | Host: [ target.prod.lan     ] Port: [2222] User: [ admin_ops      ] | |
|  | Auth: (•) Senha: [ ************** ]  ( ) Chave Privada              | |
|  | [x] Conectar usando a ponte configurada acima (ProxyJump)           | |
|  | Validar: [ TESTAR DESTINO ] -> Estado: [ Aguardando teste...       ] | |
|  +---------------------------------------------------------------------+ |
|                                                                          |
|  REGRAS DE CONFIANÇA SSH                                                 |
|  Known Hosts do destino: [ /home/user/.ssh/known_hosts          [Browse] ]|
|  [ ] Ignorar verificação de host key (inseguro; todos os hops)          |
|  Host key desconhecida falha por padrão; o opt-in acima aceita MITM.    |
|                                                                          |
+--------------------------------------------------------------------------+
|  [ Voltar ]                                               [ Próximo ]    |
+--------------------------------------------------------------------------+
```

#### TUI Wireframe (Bubble Tea)
```text
CONFIGURAÇÃO SSH & LISTA DE PONTES (Tela 3/13)
===================================================

[X] ATIVAR ENVIO SSH

[X] PONTES SSH (BASTION/JUMP) [OPCIONAL] — LISTA ORDENADA:
    1. Host: [ bastion1.transit.lan ] Port: [ 22 ] User: [ bridge1 ]
       Auth: [x] Chave Privada (~/.ssh/id_rsa) [ ] Senha
       Known Hosts: [ ~/.ssh/known_hosts ]
       >> Status Ponte 1: [ OK ] Conectado
    2. Host: [ bastion2.transit.lan ] Port: [2222] User: [ bridge2 ]
       Auth: [ ] Chave Privada [x] Senha
       >> Status Ponte 2: [ OK ] Via ponte anterior
    [+ Adicionar] [Remover] [Mover ↑/↓]

DESTINO FINAL (TARGET REMOTO):
    Host:  [ target.prod.lan         ] Port: [ 2222 ]
    User:  [ admin_ops               ]
    Auth:  [ ] Chave Privada [x] Senha (Digitar abaixo)
    Senha: [ ************ ] (Nunca exposta em logs)
    >> Status Destino: [ Não detectado ] ENTER/Detectar ou use Avançado

[X] Utilizar a lista de pontes como ProxyJump
[X] Validar Host Key contra 'known_hosts' (padrão)
[ ] Ignorar Host Key (inseguro, opt-in, todos os hops)

───────────────────────────────────────────────────
TAB: Campo  •  T: Testar Conexões  •  ESPAÇO: Marcar  •  ENTER: Próximo
B: Voltar   •  CTRL+C: Sair
```

*   **Regras e Validações:**
    *   *F1 (Ponte SSH):* O estado de cada ponte é independente e validado em ordem. A ferramenta valida a primeira, depois cada hop seguinte através do anterior. Se qualquer ponte falhar, o destino final exibirá "Impossível testar (Ponte indisponível)" ou similar.
    *   *Segurança:* A host key é estrita por padrão. O checkbox de bypass é opt-in, exige aviso explícito e aplica-se a todos os hops, equivalente a `StrictHostKeyChecking=no`. A chave privada e a senha SSH nunca são exibidas em texto livre e são mascaradas por asteriscos.

    *   *Credenciais por ponte:* cada ponte exige `host`, `porta` (vazia assume 22) e `usuário`, além de modo de autenticação explícito — `senha`, `chave` ou `chave+senha` — com a credencial correspondente preenchida. Senha e chave **não são herdadas** do destino. `known_hosts` vazio herda o valor do destino; o bypass de host key continua global.
    *   *Persistência:* apenas metadados não sensíveis da ponte (`host`, `porta`, `usuário`, modo, `key_path` e `known_hosts`) entram em `config_json`. Senhas ficam somente em memória e nunca vão para SQLite, JSON, relatório ou logs. Ao retomar uma sessão, as pontes são repovoadas com senhas em branco e a execução fica bloqueada até o preenchimento (ex.: “Ponte 2 exige senha”).
    *   *Detecção:* alterar host, porta, usuário, modo, chave ou `known_hosts` de qualquer ponte invalida a detecção de SO. Digitar ou alterar apenas a senha **não** invalida.
    *   *Navegação GUI:* o card “Pontes SSH” lista um card compacto por ponte, com botões de subir, descer e remover e o botão “Adicionar ponte”; o seletor de autenticação habilita senha e/ou chave conforme o modo.
    *   *Navegação TUI:* `ctrl+j` abre a tela de pontes; `a` adiciona, `e`/`enter` edita, `d` remove e `K`/`J` reordenam. A edição só é confirmada ao salvar; `esc` descarta. A senha é digitada com máscara.

    *   *Detecção obrigatória:* a GUI/TUI não usa valores padrão como Ubuntu 22.04 silenciosamente. Sem destino detectado, “Continuar”/`Enter` executa `Detectar destino`; a detecção preenche SO, versão, arquitetura e imagem. A TUI exibe “Destino ainda não detectado” até a detecção ou configuração manual explícita em **Avançado**. Falha de detecção mantém o usuário nesta tela; testar a conexão não substitui a detecção.
    *   *Configuração manual:* em **Avançado**, o usuário pode informar o destino manualmente; ao abrir sem detecção, os campos de distro/versão/arquitetura/imagem são limpos para evitar aceitar Ubuntu 22.04 por padrão. Os campos precisam ser preenchidos explicitamente e a imagem recomendada permanece sujeita à validação do ambiente isolado.
---

### 4. Entrada de Pacotes
*   **Propósito:** Permitir a inclusão individual ou em lote de pacotes a serem buscados, validando preliminarmente a formatação do nome antes da inserção na lista.

#### GUI Wireframe (Fyne)
```text
+--------------------------------------------------------------------------+
| OPF - Entrada de Pacotes                                         [ - ][ x ]|
+--------------------------------------------------------------------------+
|  [Tela 4 de 13] Seleção de Pacotes para Download                        |
|                                                                          |
|  Adicionar Pacote:                                                       |
|  [ nginx, htop, curl                                                   ] |
|  (Separe por vírgulas para múltiplos. Ex: vim, git, tmux)    [ ADICIONAR ]|
|                                                                          |
|  PACOTES SOLICITADOS:                                                    |
|  +--------------------------------------------------------------------+  |
|  | [x] nginx (Aguardando resolução...)                    [ Remover ] |  |
|  | [x] htop  (Aguardando resolução...)                    [ Remover ] |  |
|  | [x] curl  (Aguardando resolução...)                    [ Remover ] |  |
|  |                                                                    |  |
|  |                                                                    |  |
|  +--------------------------------------------------------------------+  |
|  Total: 3 pacote(s) adicionado(s)                                        |
|                                                                          |
+--------------------------------------------------------------------------+
|  [ Voltar ]                                               [ Próximo ]    |
+--------------------------------------------------------------------------+
```

#### TUI Wireframe (Bubble Tea)
```text
ENTRADA DE PACOTES (Tela 4/13)
===================================================

Adicionar pacote(s) (separados por vírgula):
[ nginx, htop, curl_____________________________________ ] [Adicionar]

PACOTES NA SESSÃO:
  [X] nginx (Pendente)
  [X] htop  (Pendente)
  [X] curl  (Pendente)


  (Use as setas para navegar, [ESPAÇO] para desmarcar, [D] para deletar)

───────────────────────────────────────────────────
TAB: Mudar campo  •  A: Adicionar  •  D: Remover  •  ENTER: Ir para Pré-Verificação
B: Voltar         •  CTRL+C: Sair
```

*   **Regras de Validação:**
    *   Caracteres inválidos (como `;`, `&`, `|`, `*`) são sanitizados imediatamente ou impedem a inserção, emitindo aviso.
    *   A lista contém **roots**; o núcleo resolve a união das dependências, deduplica e mantém um único manifesto/transferência/instalação.
    *   A lista de pacotes solicitada é consolidada localmente antes de avançar.

---

### 5. Pré-Verificação (Via SSH)
*   **Propósito:** Consultar o sistema remoto de destino para descobrir se algum pacote solicitado ou suas dependências já estão instalados, evitando downloads e envios redundantes.

#### GUI Wireframe (Fyne)
```text
+--------------------------------------------------------------------------+
| OPF - Pré-Verificação                                            [ - ][ x ]|
+--------------------------------------------------------------------------+
|  [Tela 5 de 13] Pré-verificação de Pacotes no Destino                  |
|                                                                          |
|  Consultando o sistema de destino remoto via SSH...                      |
|  +--------------------------------------------------------------------+  |
|  | [📥 Baixar] nginx  (Não instalado no destino - Necessita download) |  |
|  | [📥 Baixar] curl   (Versão no destino: 7.81.0, Solicitada: Recente) |  |
|  | [✔ Já Inst] htop   (Instalado: 3.0.5 - Será pulado na instalação)   |  |
|  |                                                                    |  |
|  |                                                                    |  |
|  +--------------------------------------------------------------------+  |
|  Resumo: 2 pacotes serão baixados e transferidos, 1 pacote será pulado.  |
|                                                                          |
|  [ ] Deseja forçar o download e reinstalação de pacotes já instalados?   |
|                                                                          |
+--------------------------------------------------------------------------+
|  [ Voltar ]                                        [ Iniciar Pipeline ]  |
+--------------------------------------------------------------------------+
```

#### TUI Wireframe (Bubble Tea)
```text
PRÉ-VERIFICAÇÃO REMOTA (Tela 5/13)
===================================================

Verificando ambiente remoto via SSH...

  [📥 BAIXAR ] nginx  -> Não encontrado no destino.
  [📥 BAIXAR ] curl   -> Alvo tem 7.81.0-1, mas será atualizado.
  [✔ PULADO ] htop   -> Versão 3.0.5 já está instalada e atualizada.

---------------------------------------------------
Resumo: 2 para baixar/transferir • 1 pulado (já instalado).

[ ] Forçar re-download e reinstalação de tudo

───────────────────────────────────────────────────
F: Alternar forçar tudo  •  ENTER: Iniciar Pipeline (13 passos)
B: Voltar  •  CTRL+C: Sair
```

---

### 6. Execução do Pipeline (13 Passos)
*   **Propósito:** Exibir de forma clara o progresso da orquestração dos 13 passos do ciclo de vida, com logs e opção de interrupção.

#### GUI Wireframe (Fyne)
```text
+--------------------------------------------------------------------------+
| OPF - Executando Pipeline                                        [ - ][ x ]|
+--------------------------------------------------------------------------+
| [Passo 8/13] Enviando pacotes via SFTP para o destino remoto...          |
| Progresso Geral: [=======================>------------] 62%              |
|                                                                          |
| PASSOS DA SESSÃO:                                                        |
|   [✔] 1. Abrir sessão                    [✔] 7. Gerar manifesto SHA256   |
|   [✔] 2. Pré-verificar via SSH           [▶] 8. Enviar via SFTP (68Mb/s) |
|   [✔] 3. Criar ambiente (Docker)         [ ] 9. Verificar hash remoto    |
|   [✔] 4. Resolver dependências           [ ] 10. Confirmar instalação    |
|   [✔] 5. Baixar pacotes                  [ ] 11. Instalar remotamente    |
|   [✔] 6. Copiar p/ o Host                [ ] 12. Registrar + Syslog  |
|                                          [ ] 13. Limpar ambiente (Cleanup)|
|  +-- LOG EM TEMPO REAL ------------------------------------------------+ |
|  | [INFO] 23:42:01 Iniciando upload do pacote 'nginx_1.18.0.deb'       | |
|  | [INFO] 23:42:05 'nginx_1.18.0.deb' transferido (1.2 MB)             | |
|  | [INFO] 23:42:06 Iniciando upload do pacote 'curl_7.81.0.deb'        | |
|  +---------------------------------------------------------------------+ |
| Temp Decorrido: 00:04:12                                 [ PARAR / CANCELAR ] |
+--------------------------------------------------------------------------+
```

#### TUI Wireframe (Bubble Tea)
```text
EXECUTANDO PIPELINE (Tela 6/13) • 62% Concluído
===================================================

[✔] 1. Abrir Sessão             [✔] 7. Gerar Manifesto
[✔] 2. Pré-Verificação SSH      [▶] 8. Transferir via SFTP (62% • 45/72MB)
[✔] 3. Criar Amb. Docker        [ ] 9. Verificar integridade remota (P3)
[✔] 4. Resolver Deps            [ ] 10. Aguardar confirmação de Instalação
[✔] 5. Download de Pacotes      [ ] 11. Executar Instalação Remota
[✔] 6. Copiar para Host         [ ] 12. Registrar + Syslog
                                [ ] 13. Destruir Ambiente Temporário

LOG EM TEMPO REAL:
  [23:42:01] [INFO] Transferindo arquivo nginx_1.18.0_amd64.deb...
  [23:42:05] [INFO] Sucesso ao transferir nginx_1.18.0_amd64.deb
  [23:42:06] [INFO] Iniciando upload de curl_7.81.0_amd64.deb...
  █

Tempo total decorrido: 0m28s
───────────────────────────────────────────────────
C: Cancelar Pipeline (Executa limpeza de segurança)
CTRL+C: Sair
```

*   **Estados:**
    *   *Cancelamento:* Ao pressionar "Cancelar", a UI congela o progresso, abre um pop-up de confirmação de segurança e, se aprovado, inicia o processo de "Cleanup de emergência", exibindo "Limpando diretórios..." antes de fechar.

---

### 7. Verificação de Integridade (Três Pontos)
*   **Propósito:** Mostrar os resultados das checagens de hash nos três pontos chave (P1 pós-download, P2 cópia local e P3 remoto).

#### GUI Wireframe (Fyne)
```text
+--------------------------------------------------------------------------+
| OPF - Integridade dos Arquivos                                   [ - ][ x ]|
+--------------------------------------------------------------------------+
|  [Tela 7 de 13] Verificação de Integridade Ponta a Ponta                 |
|                                                                          |
|  VALIDAÇÃO DOS CHECKSUMS (SHA-256)                                       |
|  +--------------------------------------------------------------------+  |
|  | Pacote           | P1 (Ambiente)  | P2 (Host)      | P3 (Remoto)   |  |
|  +--------------------------------------------------------------------+  |
|  | nginx_1.18       | [✔ PASSO]      | [✔ PASSO]      | [✔ PASSO]     |  |
|  | curl_7.81        | [✔ PASSO]      | [✔ PASSO]      | [✔ PASSO]     |  |
|  | libssl_1.1 (dep) | [✔ PASSO]      | [✔ PASSO]      | [✔ PASSO]     |  |
|  |                  |                |                |               |  |
|  +--------------------------------------------------------------------+  |
|                                                                          |
|  [✔] Manifest 'SHA256SUMS' gerado e verificado no destino com sucesso.   |
|                                                                          |
|  AVISO: Todos os pacotes remotos coincidem exatamente com os originais   |
|  baixados da rede. Pronto para a instalação com segurança.               |
|                                                                          |
+--------------------------------------------------------------------------+
|                                                           [ Continuar ]  |
+--------------------------------------------------------------------------+
```

#### TUI Wireframe (Bubble Tea)
```text
VERIFICAÇÃO DE INTEGRIDADE PONTA A PONTA (Tela 7/13)
===================================================

Pacote              P1 (Origem)   P2 (Host)     P3 (Remoto)   Status
───────────────────────────────────────────────────────────────────────
nginx_1.18          [  OK  ]      [  OK  ]      [  OK  ]      Aprovado
curl_7.81           [  OK  ]      [  OK  ]      [  OK  ]      Aprovado
libssl_1.1 (dep)    [  OK  ]      [  OK  ]      [  OK  ]      Aprovado

Manifesto SHA256SUMS: [ VÁLIDO E REMOTO ]

>> Todos os arquivos estão íntegros e validados nos 3 pontos.

───────────────────────────────────────────────────
ENTER: Avançar para Confirmação de Instalação
CTRL+C: Sair
```

*   **Validações:**
    *   *Acessibilidade:* Em vez de usar apenas cores, os status são indicados por `[ OK ]`/`[ FALHA ]` ou `[✔ PASSO]`/`[✘ FALHOU]` para fácil leitura.
    *   Qualquer falha em P1 ou P2 bloqueia o avanço imediatamente e força o fluxo de interrupção e limpeza.

---

### 8. Confirmação de Instalação (SUDO) (F2)
*   **Propósito:** Resumir a operação, alertar sobre riscos e capturar a elevação de privilégio (Sudo) do usuário no alvo. O OPF detecta se o usuário remoto é root; caso contrário, usa a senha da própria conta SSH via `sudo -S`. **NOPASSWD não é suportado.**

#### GUI Wireframe (Fyne)
```text
+--------------------------------------------------------------------------+
| OPF - Confirmação de Instalação                                  [ - ][ x ]|
+--------------------------------------------------------------------------+
|  [Tela 8 de 13] Confirmação & Autenticação de Privilégios (Sudo)         |
|                                                                          |
|  Os pacotes foram transferidos para '/tmp/opf-488/' no destino.           |
|  Total: 2 Pacotes Principais, 3 Dependências.                            |
|                                                                          |
|  AUTORIZAÇÃO SUDO NO DESTINO:                                            |
|  (•) Usuário remoto é root — sudo não será necessário                    |
|  ( ) Usuário não-root — informe a senha da conta 'admin_ops'             |
|                                                                          |
|  Senha da conta para sudo: [ ********** ]  [x] Mostrar senha             |
|  (Com autenticação por chave, esta é a senha da conta, não a passphrase.) |
|                                                                          |
|  [Aviso] A senha é enviada somente via stdin ao sudo -S e nunca é salva  |
|  em disco, SQLite, JSON de configuração ou logs.                         |
|                                                                          |
+--------------------------------------------------------------------------+
|  [ Descartar / Sair ]                              [ INSTALAR AGORA ]    |
+--------------------------------------------------------------------------+
```

#### TUI Wireframe (Bubble Tea)
```text
CONFIRMAÇÃO DE INSTALAÇÃO & ELEVAÇÃO SUDO (Tela 8/13)
===================================================

Pacotes prontos para instalação no destino remoto:
  • nginx, curl (e 3 dependências)

AUTENTICAÇÃO SUDO:
  Detectando privilégios (id -u)...

  [ ] Usuário remoto é root — não requer sudo
  [X] Usuário não-root — senha da conta 'admin_ops' obrigatória

  Senha da conta para sudo: [ ********** ] (nunca gravada)
  (Com chave SSH, use a senha da conta, não a passphrase da chave.)

  ⚠️  AVISO: NOPASSWD não é suportado. A instalação remota
      usa sudo -S com a senha da conta via stdin.

───────────────────────────────────────────────────
S: Executar Instalação  •  TAB: Mudar de campo  •  ESPAÇO: Marcar options
Q: Cancelar e Sair      •  CTRL+C: Sair
```

*   **Política F2/F6 (Sudo):**
    *   *Detecção de root:* executar `id -u` no destino. `0` dispensa sudo; qualquer outro UID exige a senha da conta SSH.
    *   *Senha:* a mesma senha da conta usada no SSH (ou a senha da conta, quando o SSH usa chave) é enviada ao `sudo -S` apenas por stdin. Nunca vai para argv, SQLite, JSON, logs ou relatórios.
    *   *Validação prévia:* executar `sudo -S -v` antes de iniciar a instalação; senha incorreta retorna a esta tela sem descartar os artefatos baixados.
    *   *NOPASSWD:* não suportado; não há checkbox, detecção ou fallback sem senha.
    *   *Cancelamento/erro:* a instalação é best-effort; o usuário pode retomar por checkpoint seguro, reautenticando.

---

### 9. Instalação Remota
*   **Propósito:** Fornecer monitoramento ativo e visual do processo de execução dos comandos remotos de instalação de pacotes (`dpkg`, `apt`, `yum`, `apk`).

#### GUI Wireframe (Fyne)
```text
+--------------------------------------------------------------------------+
| OPF - Executando Instalação Remota                                [ - ][ x ]|
+--------------------------------------------------------------------------+
|  [Tela 9 de 13] Executando comandos no destino...                      |
|  Instalando pacotes: [=====================>------------------] 52%       |
|                                                                          |
|  Instalando: curl (7.81.0) - Configurando dependências...                |
|                                                                          |
|  +-- SAÍDA DO GERENCIADOR DE PACOTES REMOTO ---------------------------+ |
|  | Selecting previously unselected package libssl1.1:amd64.            | |
|  | Preparing to unpack .../libssl1.1_1.1.1f-1ubuntu2_amd64.deb ...     | |
|  | Unpacking libssl1.1:amd64 (1.1.1f-1ubuntu2) ...                     | |
|  | Setting up libssl1.1:amd64 (1.1.1f-1ubuntu2) ...                    | |
|  | Selecting previously unselected package curl ...                    | |
|  | Preparing to unpack .../curl_7.81.0-1ubuntu1_amd64.deb ...          | |
|  +---------------------------------------------------------------------+ |
|                                                                          |
|  [⚠️] Timeout em 09m45s                                                  |
+--------------------------------------------------------------------------+
|                                                           [ Cancelar ]   |
+--------------------------------------------------------------------------+
```

#### TUI Wireframe (Bubble Tea)
```text
INSTALAÇÃO REMOTA EM ANDAMENTO (Tela 9/13)
===================================================

Progresso: [▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓░░░░░░░░░░░░] 52%
Instalando pacote: curl (7.81.0)

SAÍDA DO TERMINAL (REMOTO):
  │ Selecting previously unselected package libssl1.1:amd64.
  │ Preparing to unpack .../libssl1.1_1.1.1f-1ubuntu2_amd64.deb ...
  │ Unpacking libssl1.1:amd64 (1.1.1f-1ubuntu2) ...
  │ Setting up libssl1.1:amd64 (1.1.1f-1ubuntu2) ...
  │ Selecting previously unselected package curl ...
  │ Preparing to unpack .../curl_7.81.0-1ubuntu1_amd64.deb ...
  _

───────────────────────────────────────────────────
Aguardando conclusão... (Timeout ativo: 10m00s)
C: Cancelar (Força encerramento seguro)  •  CTRL+C: Sair
```

*   **Cancelamento:** o encerramento do gerenciador remoto é best-effort; a UI avisa que `dpkg`/`yum`/`apk` pode deixar estado parcial. O resultado registra `abortada`/`falhou` conforme o caso.
*   **Resultado por pacote:** sempre que possível, consultar o gerenciador após a instalação para confirmar `instalado_verificado`; caso contrário, rotular explicitamente o resultado como agregado.

---

### 10. Resultado Final
*   **Propósito:** Consolidar a resposta de toda a rotina de instalação do pipeline, facilitando a identificação rápida do que obteve êxito ou problemas.

#### GUI Wireframe (Fyne)
```text
+--------------------------------------------------------------------------+
| OPF - Resultado da Sessão                                        [ - ][ x ]|
+--------------------------------------------------------------------------+
|  [Tela 10 de 13] Resumo Final dos Pacotes na Sessão                      |
|                                                                          |
|  SESSÃO CONCLUÍDA COM SUCESSO! Todos os pacotes previstos foram tratados. |
|                                                                          |
|  HISTÓRICO DE APLICAÇÃO:                                                 |
|  +--------------------------------------------------------------------+  |
|  | [✔ INSTALADO]  nginx (1.18.0) - Instalado e verificado no destino.  |  |
|  | [✔ INSTALADO]  curl (7.81.0) - Instalado e verificado no destino.   |  |
|  | [✔ INSTALADO]  libssl1.1 (dependência) - Instalado com sucesso.     |  |
|  | [✔ JÁ EXISTE]  htop (3.0.5) - Já estava presente e foi pulado.     |  |
|  |                                                                    |  |
|  +--------------------------------------------------------------------+  |
|  Eventos: 15 registrados.                                                 |
|                                                                          |
+--------------------------------------------------------------------------+
|                                                   [ Visualizar Relatório ]|
+--------------------------------------------------------------------------+
```

#### TUI Wireframe (Bubble Tea)
```text
RESULTADO FINAL DA SESSÃO (Tela 10/13)
===================================================

[✔ SUCESSO ] A sessão de instalação foi concluída.

STATUS DOS PACOTES:
  [✔ INSTALADO] nginx (1.18.0)
  [✔ INSTALADO] curl (7.81.0)
  [✔ INSTALADO] libssl1.1 (dependência)
  [✔ JÁ EXISTE ] htop (3.0.5) [Pulado]

Os ambientes temporários locais foram removidos de forma segura.

───────────────────────────────────────────────────
ENTER: Ver Relatório Final
CTRL+C: Sair
```

---

### 11. Relatório de Sessão
*   **Propósito:** Prover o desdobramento documentado da operação para fins de auditoria, permitindo download; exportação JSON/Markdown e envio ao Syslog (best-effort) estão disponíveis na F8.

#### GUI Wireframe (Fyne)
```text
+--------------------------------------------------------------------------+
| OPF - Relatório de Sessão                                         [ - ][ x ]|
+--------------------------------------------------------------------------+
|  [Tela 11 de 13] Relatório & Fechamento da Sessão                      |
|                                                                          |
|  Visualização (Markdown):                                                |
|  +--------------------------------------------------------------------+  |
|  | # RELATÓRIO DE SESSÃO OPF - ID #204                                |  |
|  | Data: 18/11/2026 00:01:22 | Status: SUCESSO                          |  |
|  | Destino: target.prod.lan (Ubuntu 22.04 amd64)                      |  |
|  |                                                                    |  |
|  | ## HOST REMOTO                                                     |  |
|  | Usuário SSH: opf | Kernel: 6.8.0 | Uptime: 1d 2h                   |  |
|  | CPU: 4 cores | Memória: 8 GB | Disco: 100 GB                       |  |
|  |                                                                    |  |
|  | ## PACOTES SOLICITADOS                                             |  |
|  | - nginx (principal)                                                |  |
|  |                                                                    |  |
|  | ## PACOTES                                                         |  |
|  | Papel       Pacote   Versão   Anterior   Status                    |  |
|  | principal   nginx    1.18.0   1.16.1     INSTALADO                 |  |
|  | dependente  curl     7.81.0   --         INSTALADO                 |  |
|  |                                                                    |  |
|  | ## DESTINO E ARMAZENAMENTO                                         |  |
|  | Arquivos locais: /tmp/opf/sessao-204 | Remotos: /tmp/opf-204       |  |
|  +--------------------------------------------------------------------+  |
|                                                                          |
|  EXPORTAR DOCUMENTO:                      |
|  [ Exportar JSON ] [ Exportar Markdown ] [ Syslog (best-effort) ]       |
|                                                                          |
|  Exportação e Syslog disponíveis com fila/reenvio best-effort; o         |
|  relatório local permanece a fonte primária.                             |
|                                                                          |
+--------------------------------------------------------------------------+
|                                                           [ FECHAR ]     |
+--------------------------------------------------------------------------+
```

#### TUI Wireframe (Bubble Tea)
```text
RELATÓRIO DE SESSÃO (Tela 11/13)
===================================================

Sessão: ID #204 • Ubuntu 22.04 (amd64)
Data: 2026-11-18 00:01:22 • Status: SUCESSO

[ Visualização do Markdown do Relatório (Use setas para rolar) ]
─────────────────────────────────────────────────────────────────
# RELATÓRIO DE SESSÃO OPF
## Host remoto
* Host: target.prod.lan
* Usuário SSH: opf
* Kernel: 6.8.0 • Uptime: 1d 2h 3m
* CPU: 4 cores • Memória: 8 GB • Disco: 100 GB
* SO: Ubuntu 22.04 (amd64)

## Pacotes solicitados
* nginx (principal)

## Pacotes
Papel       Pacote   Versão   Anterior   Status
principal   nginx    1.18.0   1.16.1     instalado
dependente  curl     7.81.0   --         instalado

## Destino e armazenamento
* Arquivos locais: /tmp/opf/sessao-204
* Arquivos remotos: /tmp/opf-204
─────────────────────────────────────────────────────────────────

Opções:
  [ J ] Exportar para JSON
  [ M ] Exportar para Markdown
  [ S ] Transmitir ao Syslog

Exportação JSON/Markdown e Syslog best-effort disponíveis; o relatório local permanece a fonte primária.

───────────────────────────────────────────────────
J/M/S: exportar/Syslog  •  ENTER: Sair e Fechar
CTRL+C: Sair
```

---

### 12. Histórico de Sessões
*   **Propósito:** Listagem de todas as rotinas registradas localmente no SQLite, facilitando consulta de auditoria.

#### GUI Wireframe (Fyne)
```text
+--------------------------------------------------------------------------+
| OPF - Histórico de Sessões                                       [ - ][ x ]|
+--------------------------------------------------------------------------+
| Filtros: SO: [ Todos       [v] ] Status: [ Todos         [v] ] [ Buscar ] |
|                                                                          |
| HISTÓRICO DE EXECUÇÕES:                                                  |
| +----------------------------------------------------------------------+ |
| | ID  | Data / Hora         | SO / Versão    | Status     | Host       | |
| +----------------------------------------------------------------------+ |
| | 204 | 18/11/26 00:01:22   | Ubuntu 22.04   | [✔ SUCESS] | target.pro | |
| | 203 | 17/11/26 14:10:05   | CentOS 7       | [✘ FALHOU] | 10.0.0.12  | |
| | 202 | 15/11/26 11:22:11   | Alpine 3.20    | [✔ SUCESS] | cluster-al | |
| | 201 | 14/11/26 09:30:15   | Ubuntu 24.04   | [📥 APENAS]| - (Local)  | |
| +----------------------------------------------------------------------+ |
|                                                                          |
| SELECIONADO: Sessão ID 204                                               |
| Data: 18/11/26 • Sucesso • Destino: target.prod.lan (Fyne/TUI Session)   |
| [ Ver Relatório ] [ Ver Logs SQLite ] [ Arquivar ] [ Exportar JSON ] |
|                                                                          |
+--------------------------------------------------------------------------+
|                                                           [ VOLTAR ]     |
+--------------------------------------------------------------------------+
```

#### TUI Wireframe (Bubble Tea)
```text
HISTÓRICO DE SESSÕES (SQLite Local)
===================================================

Filtro atual: Todos

ID   Data       SO             Status        Destino
─────────────────────────────────────────────────────────────
204  18/11/26   Ubuntu 22.04   [✔ SUCESS]    target.prod.lan
203  17/11/26   CentOS 7       [✘ FALHOU]    10.0.0.12
202  15/11/26   Alpine 3.20    [✔ SUCESS]    cluster-alpine
201  14/11/26   Ubuntu 24.04   [📥 APENAS]   - (Apenas Baixar)

(Pressione [ENTER] no item selecionado para carregar dados)

>> Sessão #204 selecionada: Ubuntu 22.04 | target.prod.lan
   Ações: [R] Visualizar Relatório | [A] Arquivar Registro

───────────────────────────────────────────────────
↑/↓: Navegar • ENTER/R: Ver Relatório • A: Arquivar • F: Filtrar
B: Voltar    • CTRL+C: Sair
```

---

### 13. Configurações Globais
*   **Propósito:** Oferecer interface para os parâmetros transversais não específicos de uma única execução.

#### GUI Wireframe (Fyne)
```text
+--------------------------------------------------------------------------+
| OPF - Configurações Globais                                      [ - ][ x ]|
+--------------------------------------------------------------------------+
|  CONFIGURAÇÃO DO SERVIDOR SYSLOG (Assíncrono / Best-effort)              |
|  [X] Habilitar Envio para Syslog Externo             |
|  Servidor:  [ 192.168.1.50                 ] Port: [ 514 ]               |
|  Protocolo: (•) UDP  ( ) TCP                Tag:  [ OPF-PROD           ] |
|  [ TESTAR SYSLOG ] -> Status: [ Sem resposta (Envios serão retidos no DB) ] |
|                                                                          |
|  DIRETÓRIOS E COMPILADORES                                               |
|  Pasta Temporária (Host): [ /tmp/opf/                            [Browse] ]|
|  Timeout padrão (Etapas): [ 600 ] segundos                               |
|                                                                          |
|  COMPATIBILIDADE DE ORQUESTRADORES                                       |
|  WSL Padrão no Windows:   [x] Tentar reinstalar automaticamente se faltar |
|  Contêineres Docker:     [x] Sempre baixar imagem oficial da distro      |
|                                                                          |
+--------------------------------------------------------------------------+
|                                                [ Cancelar ] [ SALVAR ]   |
+--------------------------------------------------------------------------+
```

#### TUI Wireframe (Bubble Tea)
```text
CONFIGURAÇÕES GLOBAIS do Sistema
===================================================

[X] ATIVAR ENVIO SYSLOG EXTERNO
    Servidor:  [ 192.168.1.50                 ]  Port: [ 514 ]
    Protocolo: (*) UDP  ( ) TCP
    Tag:       [ OPF-PROD           ]
    >> Status Syslog: [ pronto para enviar (best-effort) ]

PARÂMETROS DE EXECUÇÃO:
    Dir Temporário:  [ /tmp/opf/                                ]
    Timeout Etapas:  [ 600  ] segundos

CONFIGURAÇÕES DE SUBSISTEMA:
    [ ] Instalar WSL automaticamente (não suportado nesta fase; requer admin)
    [X] Baixar imagens Docker originais de forma automatizada

───────────────────────────────────────────────────
TAB: Mudar de campo  •  ESPAÇO: Alternar  •  T: Testar Syslog
ENTER: Salvar e Voltar  •  ESC: Cancelar  •  CTRL+C: Sair
```

*   **Persistência:** configurações não sensíveis são salvas em `os.UserConfigDir()/opf/config.json`; senhas/chaves nunca são gravadas.
*   **Syslog:** campos e teste ficam desabilitados até a F8.
*   **Instalação automática de WSL:** não suportada nesta fase; exige operação administrativa externa.
*   **Diretório temporário e timeout:** valores globais aplicados ao pipeline; diretórios de sessão continuam isolados por ID.

---

## C) ESPECIFICAÇÃO DE COMPONENTES REUTILIZÁVEIS

### 1. Card de Pacote
*   **Nome:** Card de Pacote (`PackageCard`).
*   **Descrição:** Representa o estado individual de um pacote ou dependência, informando nome, versão, arquitetura e seu status atual.
*   **Acessibilidade:** Além das cores, deve sempre usar símbolos e textos legíveis (`[✔]`, `[📥]`, `[✘]`) para daltônicos ou leitores de tela.
*   **Estados Visuais:**
    *   *Pendente/Padrão:* Borda cinza suave. Texto `[Pendente]`.
    *   *Sendo Baixado / Transferido:* Indicador com animação ou reticências, texto `[▶ Processando...]`.
    *   *Sucesso (Instalado / Enviado):* Cor verde. Ícone `[✔ Sucesso]` ou `[✔ Instalado]`.
    *   *Erro / Falha:* Cor vermelha e borda contrastante. Ícone `[✘ Falhou]`.
    *   *Pulado (Já Instalado):* Cor azul / cinza escuro. Ícone `[✔ Já Existia]`.

```text
Visualização ASCII (GUI Card / TUI Row):
+-----------------------------------------------------------+
| [📥] nginx  - amd64 (v1.18.0)                  [ Principal ]|
| Status: [ Pendente de Download ]                          |
+-----------------------------------------------------------+
| [✔] libssl1.1 - amd64 (v1.1.1)                 [Dependência]|
| Status: [ Baixado e Verificado (P1/P2) ]                  |
+-----------------------------------------------------------+
```

### 2. Indicador de Status
Símbolos e cores universais para visualização de integridade e feedback:
*   **`[✔ PASSO] / [  OK  ]`** (Verde / Símbolo de "Visto"): Representa integridade garantida, etapas concluídas com sucesso.
*   **`[▶ EM PROGRESSO]`** (Azul ou Ciano): Atividade sendo processada.
*   **`[✘ FALHOU] / [ ERRO ]`** (Vermelho / Símbolo de Erro): Indica que um arquivo falhou no checksum ou conexões caíram.
*   **`[✔ JÁ EXISTE] / [ PULA ]`** (Cinza / Azul escuro): O sistema identificou no alvo, prevenindo download.

### 3. Barra de Progresso
*   Uma barra dupla, onde a superior reflete a etapa isolada e a inferior o consolidado geral (13 Passos).
*   *GUI:* `fyne.widget.ProgressBar`.
*   *TUI:* Usando o pacote `bubbles/progress`, desenhando os caracteres correspondentes.

```text
Baixando pacotes [=========>----------------] 38% (38/100 MB)
```

### 4. Painel de Logs em Tempo Real
*   **Descrição:** Caixa de texto com suporte a scroll, mostrando output capturado do Go (`slog`) e scripts remotos.
*   **Funcionalidades:** Permitir filtragem em texto e níveis (INFO, WARN, ERROR).
*   **Estados:**
    *   *Vazio:* "Aguardando inicialização da rotina..."
    *   *Ativo:* Mostra os timestamps e texto com scroll automático.

### 5. Campo de Senha Seguro (SSH/Sudo)
*   Sempre com máscara de caracteres (`*`).
*   Inclusão de um checkbox/botão para "Mostrar Senha" (apenas útil na GUI ou na TUI por tecla rápida).
*   Inclusão de uma nota de rodapé fixa ou tooltip: "🔒 Esta senha é utilizada temporariamente e nunca é persistida no SQLite ou nos arquivos de Log."

### 6. Diálogo de Confirmação de Ação Destrutiva / Cancelamento
*   Pop-up de segurança ao acionar o cancelamento do pipeline.
*   **Texto principal:** "Você deseja mesmo interromper a operação de fetch/instalação?"
*   **Aviso:** "Todos os contêineres e pastas temporárias no host e no alvo remoto serão limpos."
*   **Opções:** `[ NÃO, CONTINUAR PIPELINE ]` (Destaque principal) ou `[ SIM, CANCELAR E LIMPAR ]` (Borda vermelha/destacada).

---

## D) CONSISTÊNCIA GUI ↔ TUI

Para garantir que a mesma rotina seja compreendida, as telas e os controles são mapeados 1:1.

| Tela # | Nome da Tela | Equivalente na GUI (Fyne) | Equivalente na TUI (Bubble Tea) | Atalhos Propostos (TUI) |
|:---:|---|---|---|---|
| **1** | Boas-vindas | Layout Central, Cards de Opções | Menu lista com cursor ativo | `Enter`, `1`, `2`, `3`, `4` |
| **2** | Configuração Sessão | Form layout, select boxes, flags | Inputs textuais, list boxes custom | `Tab` (mover), `Espaço` (marcar) |
| **3** | Configurações SSH | Form segmentado com abas, Bridge toggle | Seções de input, teste e flags | `T` (Testar), `Tab`, `Espaço` |
| **4** | Entrada de Pacotes | Campo texto, Lista de pacotes na tela | Input de texto, viewport de lista | `A` (Add), `D` (Del), `Espaço` (marcar) |
| **5** | Pré-verificação | Grid com status, botão forçar | Tabela de status e resumo | `F` (Forçar tudo), `ENTER` (Avançar) |
| **6** | Execução Pipeline | ProgressBar, check de passos, Logview | ProgressBar, Checklist e Log box | `C` (Cancelar / Parar) |
| **7** | Integridade Checklist| Tabela com checks visuais | Tabela de status (P1, P2, P3) | `ENTER` (Continuar) |
| **8** | Confirmação Sudo | Resumo de pacotes, Form de senha | Inputs de privilégios e senha | `S` (Executar Sudo), `Tab` |
| **9** | Instalação Remota | ProgressBar de andamento, Terminal box| Viewport de saída, Progresso | `C` (Cancelar) |
| **10** | Resultado Final | Cards grandes com status de fechamento| Layout de status final, Lista | `ENTER` (Ir p/ Relatório) |
| **11** | Relatório Sessão | Texto + botões de exportação habilitados | Viewport Markdown; exportação local e Syslog | `J`/`M`/`S`, `ENTER` |
| **12** | Histórico | Tabela de sessões, view de detalhes | Tabela navegável, viewport inferior | `↑/↓` (navegar), `R` (Relatório), `A` (Arquivar) |
| **13** | Configs Globais | Tabs e inputs gerais | Seções divididas por divisórias textuais | `T` (Testar Syslog), `ENTER` (Salvar) |

### Como a TUI gerencia grandes massas de dados (ex: muitos pacotes)
*   **Viewport Limitado:** A TUI utiliza o componente `viewport` e `list` do Charm, implementando paginação natural `[ Página 1 de X ]` e busca por barra `/` para filtrar por nome de pacote no meio de grandes dependências.
*   **Rendimento:** Ao carregar múltiplos pacotes, a TUI exibe o progresso de forma compactada, transformando os cards em linhas simples.

---

## E) ESTADOS TRANSVERSAIS

### 1. Sessão Sem SSH Configurado (Modo "Apenas Baixar")
*   Se o usuário desmarcar "Ativar envio via SSH" na Tela 3:
    *   O pipeline oculta os passos `2 (Pré-check remoto)`, `8 (Enviar via SFTP)`, `9 (Verificar Remoto)`, `10 (Confirmação de instalação)` e `11 (Instalar)`.
    *   A interface exibe o fluxo em **8 passos**: 1, 3, 4, 5, 6, 7, 12 e 13. O passo final será a geração do manifesto/download. O relatório informará: "Sessão local (Apenas baixar) concluída".

### 2. Sessão com Ponte SSH que Falha antes do Destino
*   Durante a Tela 3 (Configurações) ou Passo 8 do Pipeline:
    *   A validação da ponte falha.
    *   *Feedback:* O indicador exibe `[✘ PONTE SSH FALHOU]` em vermelho, acompanhado do erro exato (`i/o timeout`, `auth failed`). O teste do destino é bloqueado com a mensagem: "Inalcançável (Sem acesso pela ponte)".

### 3. Autenticação Sudo (F2/F6)

#### Usuário Remoto Root
*   Se `id -u` retornar `0`, o campo de senha desaparece e a instalação ocorre sem sudo.

#### Usuário Não-Root
*   A senha da conta SSH (ou a senha da conta, quando o SSH usa chave) é obrigatória e enviada ao `sudo -S` apenas por stdin.
*   NOPASSWD não é suportado; não há detecção, checkbox ou fallback sem senha.

#### Sudo com Senha Errada
*   Validar `sudo -S -v` antes de iniciar a instalação. Se falhar, a UI exibe `[✘ FALHA DE AUTENTICAÇÃO SUDO]` e retorna à tela 8 sem descartar os artefatos baixados. O usuário pode corrigir a senha ou retomar por checkpoint seguro.

#### Sudo com Timeout ou Falha de Permissão
*   O sistema aborta com erro, informando a falta de permissão apropriada no alvo, sem travar a interface e procedendo para o cleanup. A retomada exige nova confirmação.

### 4. Syslog Indisponível
*   O aplicativo continua a execução, registra localmente no SQLite e enfileira o envio best-effort com retry/backoff.
*   SQLite e o relatório local são a fonte primária; falhas no Syslog não interrompem o pipeline.

### 5. Cancelamento no Meio do Pipeline
*   O usuário clica em "Cancelar" e confirma no diálogo de segurança.
*   O pipeline interrompe as atividades concorrentes e assume o estado `[▶ Cancelamento solicitado. Limpando...]`.
*   O OPF remove os diretórios temporários do host, tenta remover a pasta `/tmp/opf-...` remota via SSH e desliga contêiner/WSL gerados.
*   O status da sessão no SQLite é marcado como `abortada` (não `falhou`).
*   Durante `dpkg`/`yum`/`apk`, o encerramento é best-effort: fechar o canal SSH não garante parada imediata do gerenciador. A UI deve avisar sobre possível estado parcial no destino.

---

## F) DIRETRIZES DE UX

### Princípios de Design Aplicados
1. **Segurança por Padrão (Secure by Default):**
    *   *Host key:* Estrita por padrão; o bypass é opt-in por sessão, com aviso vermelho e aplicado a todos os hops.
    *   *Assinaturas:* Alpine usa `--allow-untrusted` por exigência do projeto, sempre com aviso explícito e após P1 `apk verify`; `--nogpgcheck`/`--allow-unauthenticated` não são expostos na F6/F7.
    *   *Não vazamento:* Senhas e chaves privadas nunca são expostas nos painéis de log, SQLite, JSON de configuração ou logs de depuração.
2. **Visibilidade (Feedback Imediato):**
    *   Toda transição entre os passos do pipeline é identificada visualmente para que o usuário não se sinta perdido ou assuma que a aplicação travou.
    *   Upload SFTP mostra bytes/velocidade reais; download mostra etapa e quantidade de artefatos. Não exibir MB/s fictício de gerenciadores que não fornecem progresso estruturado.
3. **Prevenção de Erros (Recuperabilidade):**
    *   Cancelamentos e encerramentos exigem confirmação explícita; o histórico é arquivado, não deletado fisicamente.
    *   Retry com backoff para conexão/upload e falhas transitórias de rede, com máximo configurável. Erros fatais não são repetidos.
    *   Retomada usa checkpoint seguro e nunca continua instalação sem nova confirmação.

---

## G) ENTREGÁVEIS FORMAIS

### Glossário de Termos de Interface
*   **Pontes SSH (Jump Hosts):** Lista ordenada de servidores intermediários; cada hop usa a conexão anterior até o destino isolado.
*   **Túnel / ProxyJump:** Método para encadear conexões SSH a fim de alcançar um servidor em rede restrita.
*   **NOPASSWD:** Configuração de sudo sem senha; **não suportada** por esta interface. O OPF usa a senha da conta SSH via `sudo -S`.
*   **Pipeline (13 Passos):** O encadeamento fechado de etapas do download até a verificação e instalação de um ou mais pacotes principais.
*   **TUI Scrollback Viewport:** Componente do terminal para navegação em logs ou relatórios longos usando o teclado.
*   **Cleanup de Sessão:** Processo automatizado e irrevogável de destruição de contêineres, pastas temporárias e recursos alocados ao término ou interrupção de uma rotina.

### Checklist de Implementação de Interfaces
- [x] Mapa de Fluxo Completo em ASCII
- [x] Configuração SSH com lista de jump hosts (F1)
- [x] SSH-first + detecção de target via /etc/os-release/uname
- [x] Presets multi-distro (Ubuntu/Debian/Alpine/CentOS/Stream/Rocky/Alma/Fedora/RHEL)
- [x] Sudo com a senha da conta SSH, sem NOPASSWD (F2/F6)
- [x] Host key estrita por padrão; bypass opt-in avisado
- [x] Múltiplos pacotes por sessão
- [x] Pré-check remoto e retomada por checkpoint seguro
- [x] Telas GUI (Fyne) para todos os passos principais
- [x] Telas TUI (Bubble Tea) equivalentes funcionais
- [x] Componentes reutilizáveis detalhados
- [x] Tabela de equivalência GUI-TUI e atalhos
- [x] Tratamento de estados transversais e erros de rede
- [x] Princípios de UX, Segurança e Acessibilidade explícitos
- [x] F6.0 — fundação de estado/UI (implementada; testes unitários e regressões de integração)
- [x] F6 — GUI Fyne em build separado CGO=1 (implementada; smoke test de inicialização)
- [x] F7 — TUI Bubble Tea CGO=0 (implementada; build e testes de parser)
- [x] F8 — exportação e Syslog (implementada; RFC 5424, JSON/Markdown e testes)

---

## H) ESCOPO E ORDEM DE IMPLEMENTAÇÃO

### F6.0 — Fundação de estado/UI (pré-requisito)

- Núcleo multi-pacote (`Packages []string`), deduplicação e roots.
- Pré-check remoto por família + “forçar reinstalação”.
- Eventos/progresso/logs em streaming, retry limitado e cancelamento `abortada`.
- Cleanup local/remoto em cancelamento.
- Schema/migração: `etapas_sessao`, `pacotes_solicitados`, campos de verificação e arquivamento.
- Retomada por checkpoint seguro.
- Store/histórico/configurações e camada `internal/app` compartilhada.

### F6 — GUI Fyne

- Telas 1–13 sobre `internal/app`.
- Build separado com `CGO_ENABLED=1`; dependências gráficas documentadas.
- Paridade com a TUI e testes manuais/headless possíveis.

### F7 — TUI Bubble Tea

- Paridade funcional 1:1 com a GUI.
- Build puro `CGO_ENABLED=0`.

### F8 — Syslog + relatórios

- Exportação JSON/Markdown, handler duplo SQLite+Syslog, fila/retry best-effort.
- As telas 11 e 13 já existem, mas os controles ficam desabilitados até aqui.

### Critérios de aceitação F6/F7

- Sessão com múltiplos roots, pré-check, recusa/instalação e falha registradas corretamente.
- Cancelamento em qualquer etapa resulta em `abortada` e cleanup verificável (best-effort na instalação).
- Retomada nunca reinstala sem nova confirmação.
- Nenhum segredo em SQLite, JSON, logs ou relatórios.
- GUI e TUI executam o mesmo fluxo e respeitam os mesmos atalhos/estados.
- `make build-linux` do CLI/TUI permanece `CGO_ENABLED=0`; GUI tem target próprio com CGO=1.
