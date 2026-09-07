# Protocolo WebSocket local do IDM (extensão ↔ IDMIntegrator64)

> **Última atualização:** consolidação de toda a investigação, incluindo
> a descoberta do bootstrap (`type=4`/`type=5`/tabela de sites) a partir
> de uma captura real completa no Windows, a distinção entre detecção
> passiva e pedido explícito de download, e o patch definitivo da
> extensão pra rodar fora do Windows. Ver também `EXTENSION_PATCHING.md`
> para o passo a passo de patch, separado deste documento por foco.

## Arquitetura geral, resumida

```
extensão original do IDM (Chrome/Edge/Firefox)
        │
        │  ws://127.0.0.1:1001/?cid=N&rnd=R
        │  Sec-WebSocket-Protocol: plugin.v3.internetdownloadmanager.com
        ▼
idm-compat-shim (nosso servidor, Go)
        │
        │  logo após o handshake: manda o "bootstrap"
        │  (type=4 + type=5 + tabela de 7 sites) -- ver seção própria
        ▼
extensão decodifica o bootstrap, popula this.j (capacidades),
libera menu de contexto, injeta document.js por site
        │
        │  detecção passiva (webRequest) OU pedido explícito
        │  (clique em "Baixar com o IDM") -- ambos chegam como
        │  type=1/type=2 com id=6 (URL), mas com formato diferente
        │  o suficiente pra distinguir (ver seção própria)
        ▼
idm-compat-shim reconhece id=51 (cookies, só em pedido explícito)
        │
        │  wine IDMan.exe /d "<url>" /f "<nome>" /c "<categoria>"
        ▼
IDMan.exe real (rodando sob Wine) mostra diálogo de confirmação
e adiciona à fila de downloads
```

## Transporte

- WebSocket puro (RFC 6455) sobre TCP, porta **1001 fixa** (hardcoded
  no `background.js` da extensão, array `La = ["127.0.0.1:1001",
  "0.1.0.1:1001"]` — o segundo é uma curiosidade legada do Windows
  onde `0.x.x.x` historicamente aliasa `127.x.x.x`; no Linux não
  significa nada e sempre falha com `ERR_ADDRESS_UNREACHABLE`, pode
  ignorar).
- Handshake inclui `Sec-WebSocket-Protocol: plugin.v3.internetdownloadmanager.com`
  — **o servidor é OBRIGADO a ecoar esse header de volta** (RFC 6455
  §4.2.2), senão o Chrome aborta a conexão com `code=1006` e a mensagem
  *"Sent non-empty 'Sec-WebSocket-Protocol' header but no response was
  received"*. Esse foi o bug que impediu qualquer conexão bem-sucedida
  por um bom tempo — corrigido configurando `upgrader.Subprotocols` no
  `gorilla/websocket`.
- `Sec-WebSocket-Extensions: permessage-deflate` é oferecido pelo
  cliente mas o servidor real do IDM **não aceita** (sem esse header na
  resposta) — não precisa implementar compressão.
- 3º candidato do ciclo de retry (`Ba()` no `background.js`) é
  `browser.runtime.connectNative("com.tonec.idm")` — Native Messaging,
  usado como fallback quando os dois WS falham. Nunca foi necessário
  implementar de verdade, já que o WS sempre funciona quando a porta
  está acessível.

## Envelope da mensagem de aplicação

```
MSG#<seq>#<conn>#<type>#<campos>;
```

- `seq` — contador incremental por conexão.
- `conn` — id de "canal"/recurso. Mensagens relacionadas ao mesmo
  recurso (ex: todas as variantes de qualidade de um vídeo) chegam com
  o mesmo `conn`. Um pedido explícito de download tende a aparecer
  **sozinho** num `conn` que não se repete (ver seção sobre detecção
  explícita).
- `type` — ver tabela de tipos conhecidos abaixo.
- `campos` — lista separada por vírgula, cada item em um de dois
  formatos:
  - **posicional**: valor cru, sem `id=` (cabeçalho fixo por `type`,
    ainda não mapeado campo a campo com certeza em todos os tipos).
  - **length-prefixed**: `<id>=<tamanho_em_bytes>:<valor>` — o tamanho
    declarado é o que garante que vírgulas/dois-pontos dentro do valor
    não quebrem o parser. Implementado em `internal/wsprotocol/msg.go`.
  - Existe também um formato raro **bare** `<id>=<valor>` (sem `:`,
    valores numéricos curtos, ex: `129=364`) que o parser atual trata
    como campo posicional (não reconhece o `id`) — funciona pra extrair
    o valor, mas perde a associação com o id. Não crítico até agora.

## Tipos de mensagem conhecidos

| type | direção | conteúdo |
|------|---------|----------|
| 1    | ambos   | recurso detectado (mídia/arquivo) — campos `id=6/7/4/100/11/51/54/...` |
| 2    | ambos   | heartbeat/posição (campos posicionais, ver seção própria) e também usado pra tabela de sites (`conn=18`) |
| 3    | ?       | visto no dispatcher da extensão (`switch(b)`), nunca capturado no fio |
| 4    | servidor→extensão | **capacidades** — ver seção "Bootstrap" |
| 5    | servidor→extensão | **identificação do servidor + regras de detecção** — ver seção "Bootstrap" |

## Bootstrap do servidor (`type=4` + `type=5` + tabela de sites) — RESOLVIDO

Essa foi a peça que faltou por boa parte da investigação. Só foi
possível capturar depois de uma captura RawCap iniciada **antes** da
extensão conectar (todas as tentativas anteriores começavam a captura
tarde demais e perdiam esse handshake inicial).

### O que descobrimos

Logo após o handshake WS, o servidor real (`IDMIntegrator64.exe`,
Windows, IDM v6.43b07 Full + extensão v6.43.1) manda, nessa ordem:

1. **Uma mensagem `type=4`** — capacidades/flags. Exemplo real capturado:
   ```
   MSG#1#3#4#0:113:113:365:91:120:103483139:3:8000:108,13=13:v6.43b07 Full;
   ```
   O campo posicional é um bitmask que popula `this.j[-1..-33]` na
   extensão (flags negativas) — são essas flags que decidem, entre
   outras coisas, se os itens do menu de contexto aparecem
   (`this.j[-8]`/`this.j[-9]`, ver `Bb`/`Cb` array no `background.js`).
   Tem também um **bypass conveniente**: se um campo específico da
   mensagem (`c`, correspondente a `a.A`) for `< 2`, essas duas flags
   viram `true` **independente do bitmask** — foi assim que
   confirmamos a teoria via `__forceType4()` antes de ter a captura
   real.

2. **Uma mensagem `type=5`**, bem maior — identificação do servidor
   (`id=13`: versão do IDM) e **todas as regras de detecção**:
   - `id=1/2/3/4/20`: listas de extensões de arquivo por categoria
     (baixável em geral, mídia, arquivo compactado, etc.)
   - `id=9/11`: domínios a **ignorar** (`*.gstatic.com`, Windows
     Update, etc.)
   - `id=10`: Content-Types que indicam "isso é um arquivo de
     verdade" (`application/octet-stream`, etc.)
   - `id=17/18`: regras de extração pra **dezenas de serviços de
     hospedagem de arquivo** (Turbobit, Rapidgator, Nitroflare,
     Dropbox, etc.), num DSL próprio com prefixos tipo `G!`/`T!`/`S!`/
     `P!`/`V#MPD!`/`V#M3U8!` (GET/token/sessão/POST/vídeo DASH/HLS).
   - `id=19`: lista de padrões **conhecidos como falso-positivo**,
     a serem ignorados — inclui literalmente
     `https://www.youtube.com/s/search/audio/*.mp3`, confirmando (com
     certeza absoluta, não mais suposição) os beeps de teste de
     autoplay do YouTube que identificamos lá no início da
     investigação, na primeira leva de capturas.
   - `id=23`: lista de métodos/propriedades JS que o `document.js`
     precisa hookear (`XMLHttpRequest|open|send|...|fetch|...`) — a
     base do `interceptor.js` (gerado em runtime via `eval`+
     `sourceURL`, nunca existe como arquivo estático).

3. **Uma mensagem `type=2` por site suportado, todas no `conn=18`** —
   7 entradas confirmadas: **YouTube, Facebook, Vimeo, Instagram,
   OK.ru, Hydrax, Udemy**. Cada uma carrega:
   - `id=101/102`: padrão de domínio + regex de URL da página
   - `id=111-135` (varia por site, YouTube é o mais completo com 26
     campos): regex de extração de API interna
     (`^/videoplayback`, `/(?:get|api)_video_info\b`, padrão de
     `/youtubei/v1/player` — exatamente o endpoint que vimos o
     `interceptor.js` interceptando), regex de decifração de
     assinatura (`nsig`) do YouTube, seletores CSS (DSL próprio com
     prefixos `$`/`$<`/`$>`/`$=`) pro elemento de vídeo em diferentes
     contextos (player normal, embed, YT Music), regex pra extrair
     `ytInitialPlayerResponse`, `PLAYER_JS_URL`, `STS`,
     `DATASYNC_ID`, `VISITOR_DATA`.
   - `id=134` no registro do YouTube: `default_kevlar_base|*.instance.networkManager|fetchFn`
     — "kevlar" é o nome interno real do framework do YouTube; esse
     campo diz ao `document.js` **em qual objeto do framework**
     hookear a rede, não só no `window.fetch` bruto.

### Implementação no shim

`internal/bootstrap/bootstrap.go` guarda essas mensagens **capturadas
byte a byte**, como constantes Go (validadas por round-trip:
reconstruir → reparsear → comparar com o original, bateu 100%).
`server.go` manda essas 9 mensagens (`Type4Template`, `Type5Template`,
7× `SiteTableTemplates`) automaticamente logo após o handshake WS, pra
toda extensão que conectar — replay literal, não reimplementação a
partir do zero. Ver `bootstrap_capture_reference.zip` (entregue à
parte) pros JSONs decodificados completos, incluindo os 26 campos da
entrada do YouTube.

**Por que replay em vez de síntese:** essas mensagens carregam uma
quantidade grande de regex/seletores muito específicos (decifração de
assinatura do YouTube, DSL de extração por hospedeiro de arquivo) que
não valia a pena tentar reconstruir campo a campo — replay é mais
seguro e imediatamente correto.

## Detecção passiva vs. pedido explícito de download — RESOLVIDO

Esse era o problema mais recente: com `--auto-download` ligado, **todo**
recurso detectado passivamente disparava um download real — inclusive
vídeos só de passagem, sem o usuário jamais ter clicado em nada.

### O sinal descoberto

Comparando 170 mensagens reais com `id=6` (URL de recurso) de uma
sessão real no Windows:

| | tem `id=11` (headers de requisição) | tem `id=51` (cookies de sessão) |
|---|---|---|
| **169 mensagens** (detecção passiva, via `webRequest`) | sempre | nunca |
| **1 mensagem** (clique real em "Baixar com o IDM", isolada no seu próprio `conn`) | nunca | sempre |

Distinção **100% limpa e mutuamente exclusiva** nos dados reais. Faz
sentido pelo próprio desenho das APIs do Chrome: o clique no menu de
contexto usa `browser.contextMenus` (dá acesso a `pageUrl`/cookies da
aba), enquanto a detecção passiva vem de `browser.webRequest` (vê
cabeçalhos de rede interceptados, não tem acesso a cookies de sessão do
jeito que um evento de UI tem). Reparo adicional: no pedido explícito,
`id=6` é a **URL da página** (`youtube.com/shorts/...`), não uma URL de
CDN/segmento de vídeo como nas detecções passivas — bate com o
`contextMenus` reportando `info.pageUrl` quando não há um
`linkUrl`/`srcUrl` específico sob o cursor.

### Implementação

`isExplicitDownloadRequest(msg)` em `server.go` reconhece **dois**
caminhos diferentes que a extensão usa pra downloads intencionais
(descobertos em momentos diferentes da investigação):

1. **Clique no menu de contexto** (`browser.contextMenus`): `id=51`
   presente e `id=11` ausente.
2. **Link de download normal clicado** (`.zip`/`.exe`/etc, capturado
   via `browser.webRequest`): esse caminho manda `id=11` (com o
   `Cookie:` embutido no texto bruto dos headers, não em `id=51`
   separado) — então o critério 1 sozinho não reconhece. O sinal usado
   aqui é o header HTTP padrão `Content-Disposition: attachment` em
   `id=13` (cabeçalhos de resposta) — o mecanismo que o **próprio
   navegador** usa pra decidir "isso é pra baixar, não pra exibir".
   Nunca aparece em segmentos de vídeo/streaming.

`handleMessage()` exige que **pelo menos um dos dois** seja verdadeiro
antes de chamar `Launch()`, mesmo com `AutoDownload=true` — qualquer
outra detecção (passiva, sniffing genérico de mídia) continua só
logando. Bônus: `id=51` também populado como header `Cookie` no
`DownloadJob` (ajuda downloads que exigem sessão autenticada —
Instagram, etc.), e `id=54` como fallback de User-Agent.

Existe ainda um **terceiro mecanismo na extensão**, não coberto pelos
dois critérios acima porque nunca foi confirmado se dispara de fato:
interceptação de download nativo do Chrome via
`browser.downloads.onCreated`/`onDeterminingFilename`
(`n.qb`/`n.rb`/`lc()` no `background.js`), gated pela flag de
capacidade `this.j[-6]` (bit 64 do bitmask do `type=4`). Quando
dispara, a mensagem que monta (`lc()`) já usa campos numerados
dedicados incluindo `id=51` — ou seja, já seria pego pelo critério 1
se ativar. Não confirmado se a flag `-6` realmente vem `true` com o
bootstrap capturado — ver pendências.

## Prefixo de URL customizado (`idmdwnlmfv9://`)

Visto em algumas detecções de mídia direta (ex: `.mp4` solto num site,
fora do fluxo YouTube/GitHub): `id=6` vem como
`idmdwnlmfv9://https://exemplo.com/video.mp4` em vez de só
`https://...`. `translateToJob()` já trata isso (`stripIDMURLWrapper`)
antes de repassar pro `Launch()` — sem isso, `wine IDMan.exe /d
idmdwnlmfv9://...` falharia.

## Botão flutuante — status atualizado

**Não mais "encerrado sem solução"** — avançamos bastante:

- Confirmado: as duas barreiras Windows-only (`background.js` via
  `getPlatformInfo()`, `content.js` via `navigator.platform`) foram
  identificadas e removidas — ver `EXTENSION_PATCHING.md`.
- Confirmado que **sem o bootstrap** (`type=4`/`type=5`/tabela de
  sites), o `document.js` nunca é injetado — o campo que dispara a
  injeção (`p`, 10º parâmetro de `h.Ub` no `content.js`) vinha `null`
  porque a tabela de sites nunca chegava. Depois do bootstrap
  implementado no shim, isso deve estar resolvido (pendente de
  reteste — ver próximos passos).
- Confirmado (com dado real, `rect: [1020, 162, 1386, 348, 1.35]`)
  que o cálculo de retângulo do elemento de vídeo (`content.js`,
  `h.R`/`h.P`, opcode 41) funciona corretamente no Linux quando
  `document.js` está ativo.
- Confirmado que `document.js` hookeia `window.fetch`/framework
  interno via `interceptor.js` (gerado em runtime), interceptando
  `/youtubei/v1/player` — consistente com o `id=115`/`id=134` da
  tabela de sites.
- **Ainda não confirmado**: se essas coordenadas realmente chegam a
  desenhar um botão nativo Win32 via Wine, ou se esse mecanismo requer
  um canal totalmente separado (IPC nomeado entre `IDMIntegrator64.exe`
  e `IDMan.exe`, nunca capturado) que o shim não replica. Nesse caso, a
  alternativa mais viável é desenhar um overlay próprio (DOM, via
  `content.js`, ou janela nativa Linux) usando as coordenadas já
  disponíveis — não decidido ainda.

## Arquivos de referência

- `internal/bootstrap/bootstrap.go` — mensagens de bootstrap capturadas.
- `internal/wsprotocol/msg.go` — parser do envelope `MSG#...;`.
- `internal/server/server.go` — `sendBootstrap()`, `isExplicitDownloadRequest()`, `translateToJob()`.
- `bootstrap_capture_reference.zip` — JSONs decodificados completos da captura real (handshake, type=4, type=5, tabela de 7 sites).
- `EXTENSION_PATCHING.md` — patch das duas barreiras de plataforma, com diffs pra 6.42.xx e 6.43.1.

## Pendências

- Confirmar se o bootstrap realmente desbloqueia `document.js` na
  prática (reteste, já que o bootstrap foi implementado depois da
  última rodada de testes do botão flutuante).
- Se `document.js` rodar mas o botão nativo ainda não aparecer,
  decidir entre: (a) investigar o canal `IDMIntegrator64.exe ↔
  IDMan.exe` (grande, Windows-nativo, não capturado ainda), ou (b)
  implementar overlay próprio usando as coordenadas já disponíveis.
- Campos posicionais de `type=1`/`type=2` ainda não mapeados campo a
  campo com certeza total (funciona por extração de `id=`, mas o
  cabeçalho fixo por tipo continua parcialmente hipotético).
- Formato bare `id=valor` (sem `:`) não tem parsing dedicado — cai
  como campo posicional, perdendo a associação de id.
