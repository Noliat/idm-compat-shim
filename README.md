# idm-compat-shim

Ponte de compatibilidade entre a **extensão original do IDM** (a da
Tonec, instalada da Chrome/Firefox Web Store — `com.tonec.idm`) e o
motor de download real, rodando o `IDMan.exe` via Wine.

Projeto irmão do [`idm-linux-bridge`](../idm-linux-bridge), que faz a
mesma coisa só que para a extensão própria do projeto. Os dois
compartilham o motor (lançamento do IDM, proxy reverso, merge de
HLS/DASH, cookies, sessões) via o módulo [`idm-shared`](../idm-shared).

## Arquitetura

```
extensão original do IDM (Chrome/Firefox)
      │
      ├── Native Messaging (stdio) ──────► internal/nativehost
      │   só usado pra detectar "IDM instalado"    (STATUS: não confirmado)
      │
      └── WebSocket ws://127.0.0.1:1001 ──► internal/server
          protocolo MSG#seq#conn#type#campos;        (STATUS: confirmado
                                                        por captura real)
                          │
                          ▼
                   internal/wsprotocol
                (parser do protocolo, testado
                 contra 2 capturas de tráfego real)
                          │
                          ▼
                    idm-shared/idm
              (Launcher.Launch — mesmo motor do ILB)
```

## Status por componente

| Componente             | Status                                                          |
|-------------------------|-------------------------------------------------------------------|
| `internal/wsprotocol`   | ✅ Parser validado contra 2 capturas reais de tráfego (YouTube, Instagram, TikTok, GitHub, HLS) |
| `internal/server` — decodificar/logar mensagens | ✅ Funcional |
| `internal/server` — `translateToJob` | ⚠️ Implementado só pros campos já confirmados (`6`,`7`,`4`,`100`,`11`). Sem filtro do falso-positivo dos beeps de teste do YouTube ainda. |
| `internal/server` — disparo real de download | ⚠️ Atrás da flag `--auto-download` (desligada por padrão) — ainda não temos captura do fluxo "usuário clica em baixar" pra confirmar qual mensagem é comando explícito vs. notificação passiva |
| `internal/nativehost`   | ❓ Não confirmado por captura nenhuma — implementação passthrough conservadora, ver aviso no arquivo |
| Manifests native messaging | ⚠️ IDs de extensão vieram das strings do `IDMan.exe`/`uninstall.txt`, não testados na prática |

## Instalação (recomendado)

Mesmo padrão do `idm-linux-bridge`: um instalador interativo que
detecta a distro, resolve dependências (Go + Wine), compila, configura
e registra o serviço.

```bash
./scripts/install.sh
```

Ou, sem interação (modo autostart pós-login, valores padrão):

```bash
./scripts/install.sh --auto
```

Requer o módulo `idm-shared` disponível como diretório irmão
(`../idm-shared`) — ver `MIGRATION_ilb.md` no histórico do projeto para
o layout de pastas esperado. O instalador verifica isso antes de
compilar e para com uma mensagem clara se não encontrar.

Se o `idm-linux-bridge` já estiver instalado e configurado, o
instalador detecta `~/.config/idm-bridge/config.env` e oferece
reaproveitar o mesmo `wine-prefix`/caminho do `IDMan.exe` em vez de
perguntar de novo.

Atalhos via `Makefile` (equivalentes, pra quem prefere `make`):

```bash
make build              # só compila (bin/idm-compat-shim)
make install            # compila + instala em ~/.local/bin
make native-messaging   # instala + registra o Native Messaging Host
make run                # roda localmente, sem auto-download
make logs                # acompanha logs do serviço systemd
make uninstall           # remove binário + configuração
```

## Build manual (alternativa ao instalador)

```bash
# a partir da raiz deste diretório (idm-compat-shim/)
go mod tidy   # necessário — não roda no ambiente onde isso foi gerado, sem rede
go build -o idm-compat-shim ./cmd/shim
```

## Uso (modo servidor WS — o principal)

```bash
./idm-compat-shim --wine-prefix ~/.wine --verbose
```

Por padrão escuta em `127.0.0.1:1001` (porta fixa que o `background.js`
da extensão original usa) e só loga mensagens decodificadas em
`~/.config/idm-compat-shim/capture.jsonl` — nenhum download real
dispara até habilitar `--auto-download`.

Pra habilitar downloads reais (depois de revisar o filtro de
falso-positivo mencionado acima):

```bash
./idm-compat-shim --wine-prefix ~/.wine --auto-download
```

## Instalar o Native Messaging Host

```bash
./scripts/install_native_messaging.sh ./idm-compat-shim
```

Registra o manifest nos navegadores instalados e gera um wrapper
(`idm-compat-shim-nmhost.sh`) que injeta a flag `--native-messaging-host`
— necessário porque o `manifest.json` do Chrome não permite passar
argumentos ao executável.

## Próximos passos (pendências reais, não só polimento)

1. **Capturar o fluxo de clique explícito de download** na extensão
   original (não só detecção passiva) — decide o design de
   `translateToJob`/`--auto-download`.
2. **Capturar o handshake real do Native Messaging** (ex: rodando o
   `IDMMsgHost.exe` original sob Wine com log/strace, ou testando a
   extensão contra este shim e vendo se ela aceita o `{"status":"ok"}`
   genérico ou espera outra coisa).
3. Implementar o filtro de falso-positivo dos beeps de autoplay do
   YouTube antes de habilitar `--auto-download` por padrão.
4. Rodar `go mod tidy` de verdade (rede indisponível no ambiente onde
   isso foi gerado) e confirmar que o build passa.
