#!/usr/bin/env bash
# Instala o manifest do Native Messaging Host (com.tonec.idm) apontando
# pro binário compilado do idm-compat-shim, para os navegadores baseados
# em Chromium e para o Firefox.
#
# Uso:
#   ./install_native_messaging.sh /caminho/para/binario/idm-compat-shim
#
# Se nenhum caminho for passado, assume que o binário está em
# ../idm-compat-shim relativo a este script (build local via `go build`
# na raiz do módulo).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SHIM_BIN="${1:-$SCRIPT_DIR/../idm-compat-shim}"

if [ ! -x "$SHIM_BIN" ]; then
  echo "ERRO: binário não encontrado ou não executável em: $SHIM_BIN" >&2
  echo "Compile primeiro (go build -o idm-compat-shim ./cmd/shim) ou passe o caminho como argumento." >&2
  exit 1
fi
SHIM_BIN="$(readlink -f "$SHIM_BIN")"
echo "Binário do shim: $SHIM_BIN"

# O manifest.json do Native Messaging só aceita um caminho de executável
# em "path" — o Chrome/Firefox não permite passar argumentos extras. Como
# o mesmo binário serve tanto pro servidor WS normal quanto pro modo
# native-messaging (via flag --native-messaging-host), geramos um wrapper
# script que injeta essa flag, e apontamos o manifest pro wrapper.
WRAPPER="$SCRIPT_DIR/../idm-compat-shim-nmhost.sh"
cat > "$WRAPPER" <<EOF
#!/usr/bin/env bash
exec "$SHIM_BIN" --native-messaging-host
EOF
chmod +x "$WRAPPER"
echo "Wrapper gerado: $WRAPPER"

# ── Chromium-family: Chrome, Chromium, Edge, Opera, Brave ──────────────
CHROMIUM_DIRS=(
  "$HOME/.config/google-chrome/NativeMessagingHosts"
  "$HOME/.config/chromium/NativeMessagingHosts"
  "$HOME/.config/microsoft-edge/NativeMessagingHosts"
  "$HOME/.config/opera/NativeMessagingHosts"
  "$HOME/.config/BraveSoftware/Brave-Browser/NativeMessagingHosts"
)

for dir in "${CHROMIUM_DIRS[@]}"; do
  parent="$(dirname "$dir")"
  if [ -d "$parent" ]; then
    mkdir -p "$dir"
    sed "s|__SHIM_BINARY_PATH__|$WRAPPER|" \
      "$SCRIPT_DIR/../manifests/com.tonec.idm.chromium.json" \
      > "$dir/com.tonec.idm.json"
    echo "Instalado: $dir/com.tonec.idm.json"
  fi
done

# ── Firefox ──────────────────────────────────────────────────────────
FIREFOX_DIR="$HOME/.mozilla/native-messaging-hosts"
if [ -d "$HOME/.mozilla" ]; then
  mkdir -p "$FIREFOX_DIR"
  sed "s|__SHIM_BINARY_PATH__|$WRAPPER|" \
    "$SCRIPT_DIR/../manifests/com.tonec.idm.firefox.json" \
    > "$FIREFOX_DIR/com.tonec.idm.json"
  echo "Instalado: $FIREFOX_DIR/com.tonec.idm.json"
fi

echo ""
echo "Pronto. Reinicie o(s) navegador(es) para o manifest ser carregado."
