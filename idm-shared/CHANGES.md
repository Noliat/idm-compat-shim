# idm-shared

Módulo Go extraído do `idm-linux-bridge` (v3.2.0), contendo tudo que é
agnóstico de qual protocolo de extensão está pedindo o download:
lançamento/controle do IDM via Wine, proxy reverso, merge de HLS/DASH,
resolução de nsig do YouTube, gerenciamento de cookies e de sessões por
site.

Consumido tanto pelo `idm-linux-bridge` (protocolo próprio da extensão
do projeto) quanto pelo `idm-compat-shim` (protocolo da extensão
original do IDM).

## Pacotes

- **`idm`** — `Launcher`, `DownloadJob`, lançamento do IDM via Wine,
  proxy reverso, merge de HLS/DASH, resolução de nsig do YouTube.
  API pública principal:
  ```go
  launcher, err := idm.NewLauncher(winePrefix, idmPath, verbose)
  jobID, err := launcher.Launch(idm.DownloadJob{...})
  launcher.IsIDMAvailable() bool
  launcher.ProxyPort() int
  launcher.RestartCh() <-chan idm.DisplayServer
  launcher.DisplayServerName() string
  ```
- **`cookies`** — `Manager`, enriquecimento de cookies por domínio
  (3 camadas: exato/subdomínio, nome de site, domínio raiz).
- **`session`** — `Manager`, persistência de dados de sessão por site
  (com handlers pré-configurados para YouTube, Google Drive, Hotmart,
  Udemy, Coursera, Dropbox, OneDrive, Mega, Twitch, Vimeo).

## O que foi alterado em relação ao código original do ILB

**Nada de lógica de negócio.** A extração foi puramente mecânica —
mover arquivos + go.mod novo. A única mudança de comportamento real:

### `idm/idm_process.go` — `isIDMRunning()`

**Antes:** excluía da busca por processos qualquer linha de `ps
aux`/`pgrep`/`/proc/*/cmdline` que contivesse o literal `"idm-bridge"`,
pra evitar que o próprio processo do launcher (que recebe
`--idm-path=.../IDMan.exe` como argumento) se autodetectasse como "IDM
já rodando".

**Depois:** usa `filepath.Base(os.Args[0])` (nome real do binário em
execução) no lugar do literal fixo.

**Por quê:** esse pacote agora é compartilhado por dois binários com
nomes diferentes (`idm-bridge` e `idm-compat-shim`, ou o que vocês
nomearem cada um). Com o literal fixo, só o `idm-bridge` continuaria
funcionando corretamente; o shim se autodetectaria como IDM já rodando
e o `Launch()` provavelmente falharia ou se comportaria de forma
inesperada (pulando o lançamento real por achar que já tem uma
instância ativa).

## Dependências externas

O `go.mod` original do `idm-linux-bridge` **não declarava**
`github.com/google/uuid` nem `github.com/dop251/goja` como `require`,
mesmo sendo usadas diretamente em `launch.go`, `hls_merge.go`,
`youtube_dash.go` e `youtube_nsig.go`. Só deve estar compilando hoje
porque alguém rodou `go mod tidy` localmente sem commitar o `go.mod`/
`go.sum` atualizados. Corrigido aqui — mas **rodem `go mod tidy` no
ambiente de vocês** depois de integrar, porque não tive acesso a rede
neste ambiente pra resolver as versões/checksums de verdade; a versão
do `goja` no `go.mod` é um placeholder razoável, não uma resolução
real.
