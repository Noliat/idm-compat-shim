# Como patchear a extensão original do IDM para rodar fora do Windows

A extensão oficial do IDM tem **duas checagens independentes de
plataforma**, em dois arquivos diferentes, que bloqueiam completamente
a extensão em qualquer navegador não-Windows. As duas foram encontradas
por inspeção manual do código (minificado, mas legível o suficiente com
paciência) e confirmadas removendo cada uma e testando o resultado.

Achamos as duas nas versões **6.42.xx** e **6.43.1** — mesma lógica,
só os nomes de variável/função mudam entre builds por causa da
minificação. Este guia dá o padrão de busca pra achar em qualquer
versão, não só um diff fixo.

## Por que duas checagens, e por que as duas precisam ser removidas

| Arquivo | O que verifica | O que trava se não remover |
|---|---|---|
| `background.js` | `browser.runtime.getPlatformInfo().os != "win"` | A extensão inteira nunca inicializa — a classe principal (conexão WS, menu de contexto, tudo) nunca é instanciada. |
| `content.js` | `navigator.platform.startsWith("Win")` | O script injetado em cada página nunca roda — sem isso, mesmo com `background.js` funcionando, não há detecção de vídeo via DOM, nem cálculo de posição do botão flutuante, nem injeção do `document.js`. |

As duas são checagens **independentes**— remover só uma não é
suficiente. A extensão conecta ao WS mesmo sem a segunda (porque isso é
tratado só em `background.js`), mas fica sem qualquer funcionalidade
que dependa de rodar código na própria página.

## Patch 1 — `background.js`

### Onde procurar

Busque por `os!="win"` ou `os != "win"` (minificadores variam se
colocam espaço). Vai estar dentro de uma função assíncrona no
**final do arquivo**, algo como:

```js
(async function() {
    var a = browser.runtime.getPlatformInfo();
    let b, c;
    navigator.userAgentData ? b = navigator.userAgentData.getHighEntropyValues(["fullVersionList", "uaFullVersion"]) : b = browser.runtime.getBrowserInfo?.();
    c = browser.privacy?.websites?.firstPartyIsolate?.get({});
    var e = q && browser.management.get("...").catch( () => null);
    [a,b,c,e] = await Promise.all([a, b, c, e]);
    if (a && a.os != "win")
        return Fb(!1);          // <- nome da função varia (Fb, Gb, ...)
    ba && Nc();                 // <- nome da função varia
    ...
})();
```

### O patch

Remover só a linha do `if`:

```diff
     [a,b,c,e] = await Promise.all([a, b, c, e]);
-    if (a && a.os != "win")
-        return Fb(!1);
     ba && Nc();
```

Não precisa remover a chamada `browser.runtime.getPlatformInfo()` em si
— ela fica inofensiva, só o resultado deixa de ser usado pra bloquear
nada.

## Patch 2 — `content.js`

### Onde procurar

É a **primeira linha executável do arquivo**, envolvendo o `IIFE`
inteiro:

```js
var h;
if (!window.__idm_init__ && navigator.platform.startsWith("Win") && document.documentElement.localName == "html") {
    window.__idm_init__ = !0;
    ...
```

### O patch

Remover só a cláusula do meio, mantendo as outras duas condições
(que são legítimas — evitar reinicialização e garantir que é um
documento HTML de verdade):

```diff
 var h;
-if (!window.__idm_init__ && navigator.platform.startsWith("Win") && document.documentElement.localName == "html") {
+if (!window.__idm_init__ && document.documentElement.localName == "html") {
     window.__idm_init__ = !0;
```

## Como aplicar (sem ferramentas especiais)

Os arquivos são minificados, mas as strings de busca acima
(`os!="win"` e `navigator.platform.startsWith("Win")`) são literais
únicas no arquivo — um `grep -n` ou busca de texto simples do próprio
editor encontra o lugar certo sem precisar entender o resto do código
ao redor.

```bash
grep -n 'os!="win"\|os != "win"' background.js
grep -n 'navigator.platform.startsWith' content.js
```

Depois de editar, valide a sintaxe antes de carregar no navegador
(evita descobrir erro de digitação só na hora de testar):

```bash
node --check background.js && echo OK
node --check content.js && echo OK
```

## Onde pegar os arquivos originais

- **Chrome/Chromium/Edge**: `~/.config/<navegador>/Default/Extensions/<ID>/<versão>_0/`
  no Linux, ou `%LOCALAPPDATA%\<Navegador>\User Data\Default\Extensions\<ID>\<versão>_0\`
  no Windows (mais simples de pegar lá, já vem descompactado — copia a
  pasta inteira, zipa, e patcheia depois de qualquer lado).
- **IDs conhecidos** (achados nas strings do `IDMan.exe`, nem todos
  necessariamente ativos em toda instalação): `jeaohhlajejodfjadcponpnjgkiikocn`,
  `jmolcgpienlcieaajfkkdamlngancncm`, `llbjbkhnmlidjebalopleeepgdfgcpec`.
  Mais confiável: `chrome://extensions` → modo desenvolvedor → o ID
  real aparece embaixo do nome da extensão instalada.

## Como carregar a versão patcheada

`chrome://extensions` → ativar "Modo desenvolvedor" → **desativar** a
versão oficial (evita os dois competirem) → "Carregar sem compactação"
→ apontar pra pasta com os arquivos já editados.

## Validação de que o patch funcionou

1. `background.js`: abre o "service worker" da extensão nos detalhes
   dela em `chrome://extensions` — deve conseguir inspecionar sem erro,
   e (com o shim rodando) uma conexão WS deve aparecer no log do shim.
2. `content.js`: abre o DevTools **da própria página** (não do service
   worker) numa aba qualquer — se o patch funcionou, o resto do
   comportamento da extensão (menu de contexto, detecção de vídeo)
   deve operar normalmente.

## Isso precisa ser reaplicado a cada atualização da extensão

Iigual qualquer patch em binário/minificado de terceiro: se a Tonec
lançar uma versão nova, os nomes de função/variável provavelmente
mudam de novo (minificação não é estável entre builds), então os
literais de busca (`os!="win"`, `navigator.platform.startsWith`)
continuam válidos — só a localização exata no arquivo muda. Repita a
busca por esses literais na versão nova antes de assumir que o patch
antigo ainda se aplica linha por linha.
