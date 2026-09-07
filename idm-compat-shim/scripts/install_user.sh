#!/usr/bin/env bash
# install_user.sh — instala TUDO que não precisa de root:
#   - binário do shim em ~/.local/bin/idm-compat-shim
#   - wrapper de native messaging em ~/.local/bin/idm-compat-shim-nmhost.sh
#   - manifests (Chromium + Firefox) apontando pro wrapper acima, com
#     path absoluto resolvido de verdade (sem ".." nem pasta de projeto)
#   - config.env em ~/.config/idm-compat-shim/ (só cria se ainda não existir,
#     não sobrescreve configuração já feita)
#   - service unit de usuário em ~/.config/systemd/user/
#
# Idempotente: pode rodar de novo a qualquer momento (ex: depois de um
# `go build` novo) que ele só atualiza o que precisa, sem duplicar nada.
#
# Uso:
#   ./install_user.sh [caminho/pro/binario/idm-compat-shim]
#
# Se nenhum caminho for passado, procura por um binário chamado
# "idm-compat-shim" já compilado na raiz do projeto (um nível acima
# desta pasta scripts/).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

SRC_BIN="${1:-$PROJECT_ROOT/idm-compat-shim}"
if [ ! -f "$SRC_BIN" ]; then
  echo "ERRO: binário não encontrado em: $SRC_BIN" >&2
  echo "Compile primeiro: cd $PROJECT_ROOT && go build -o idm-compat-shim ./cmd/shim" >&2
  echo "Ou passe o caminho como argumento: $0 /caminho/para/idm-compat-shim" >&2
  exit 1
fi

INSTALL_DIR="$HOME/.local/bin"
CONFIG_DIR="$HOME/.config/idm-compat-shim"
USER_SYSTEMD_DIR="$HOME/.config/systemd/user"

mkdir -p "$INSTALL_DIR" "$CONFIG_DIR" "$USER_SYSTEMD_DIR"

# ── 1. Binário ──────────────────────────────────────────────────────────
INSTALLED_BIN="$INSTALL_DIR/idm-compat-shim"
cp "$SRC_BIN" "$INSTALLED_BIN"
chmod +x "$INSTALLED_BIN"
INSTALLED_BIN="$(readlink -f "$INSTALLED_BIN")"   # canonical, sem ".." nem symlink
echo "[1/5] Binário instalado: $INSTALLED_BIN"

# ── 2. Wrapper de Native Messaging (fica no MESMO lugar do binário,     ─
#      NUNCA na pasta do projeto -- é isso que causou a bagunça antes)  ─
WRAPPER="$INSTALL_DIR/idm-compat-shim-nmhost.sh"
cat > "$WRAPPER" <<EOF
#!/usr/bin/env bash
exec "$INSTALLED_BIN" --native-messaging-host
EOF
chmod +x "$WRAPPER"
WRAPPER="$(readlink -f "$WRAPPER")"
echo "[2/5] Wrapper de native messaging: $WRAPPER"

# ── 3. Manifests dos navegadores ─────────────────────────────────────────
CHROMIUM_DIRS=(
  "$HOME/.config/google-chrome/NativeMessagingHosts"
  "$HOME/.config/google-chrome-beta/NativeMessagingHosts"
  "$HOME/.config/chromium/NativeMessagingHosts"
  "$HOME/.config/microsoft-edge/NativeMessagingHosts"
  "$HOME/.config/opera/NativeMessagingHosts"
  "$HOME/.config/BraveSoftware/Brave-Browser/NativeMessagingHosts"
)
installed_any_chromium=0
for dir in "${CHROMIUM_DIRS[@]}"; do
  parent="$(dirname "$dir")"
  if [ -d "$parent" ]; then
    mkdir -p "$dir"
    sed "s|__SHIM_BINARY_PATH__|$WRAPPER|" \
      "$PROJECT_ROOT/manifests/com.tonec.idm.chromium.json" \
      > "$dir/com.tonec.idm.json"
    echo "[3/5] Manifest instalado: $dir/com.tonec.idm.json"
    installed_any_chromium=1
  fi
done
if [ "$installed_any_chromium" -eq 0 ]; then
  echo "[3/5] AVISO: nenhum diretório de navegador Chromium encontrado — nenhum manifest Chromium instalado."
  echo "       Se o seu navegador usa um perfil não-padrão (ex: instalado via yay/AUR em /opt,"
  echo "       ou perfil customizado), rode manualmente:"
  echo "         mkdir -p <seu-perfil>/NativeMessagingHosts"
  echo "         sed 's|__SHIM_BINARY_PATH__|$WRAPPER|' '$PROJECT_ROOT/manifests/com.tonec.idm.chromium.json' > <seu-perfil>/NativeMessagingHosts/com.tonec.idm.json"
fi

if [ -d "$HOME/.mozilla" ]; then
  FIREFOX_DIR="$HOME/.mozilla/native-messaging-hosts"
  mkdir -p "$FIREFOX_DIR"
  sed "s|__SHIM_BINARY_PATH__|$WRAPPER|" \
    "$PROJECT_ROOT/manifests/com.tonec.idm.firefox.json" \
    > "$FIREFOX_DIR/com.tonec.idm.json"
  echo "[3/5] Manifest instalado: $FIREFOX_DIR/com.tonec.idm.json"
fi

# ── 4. config.env (não sobrescreve se já existir) ───────────────────────
CONFIG_FILE="$CONFIG_DIR/config.env"
if [ -f "$CONFIG_FILE" ]; then
  echo "[4/5] config.env já existe, mantendo: $CONFIG_FILE"
  current_port="$(grep -oP '^SHIM_PORT=\K.*' "$CONFIG_FILE" 2>/dev/null || true)"
  if [ "$current_port" = "1001" ]; then
    echo "       ⚠️  AVISO: SHIM_PORT está em 1001 (porta privilegiada)."
    echo "       Se estiverem usando o redirect nftables (install_system_redirect.sh),"
    echo "       SHIM_PORT deveria ser 18001. Edite manualmente: $CONFIG_FILE"
  fi
else
  cat > "$CONFIG_FILE" <<EOF
# IDM Compat Shim — Configuração
# Gerado por install_user.sh em $(date)

SHIM_HOST=127.0.0.1
SHIM_PORT=18001
WINE_PREFIX=$HOME/.wine
IDM_PATH=$HOME/.wine/drive_c/Program Files (x86)/Internet Download Manager/IDMan.exe
VERBOSE=false
AUTO_DOWNLOAD=false
AUTO_DOWNLOAD_TYPES=
EOF
  echo "[4/5] config.env criado: $CONFIG_FILE (edite WINE_PREFIX/IDM_PATH se necessário)"
fi

# ── 5. Service unit de usuário ───────────────────────────────────────────
cp "$PROJECT_ROOT/systemd/idm-compat-shim.service" "$USER_SYSTEMD_DIR/idm-compat-shim.service"
echo "[5/5] Service unit instalado: $USER_SYSTEMD_DIR/idm-compat-shim.service"

systemctl --user daemon-reload
echo ""
echo "Instalação de usuário concluída. Para (re)iniciar o serviço:"
echo "  systemctl --user enable --now idm-compat-shim.service"
echo "  systemctl --user restart idm-compat-shim.service   # se já estava rodando"
echo ""
echo "Se a extensão precisar da porta 1001 (privilegiada) e SHIM_PORT acima"
echo "estiver em 18001, rode também (com sudo) o install_system_redirect.sh"
echo "pra criar o redirect 1001 -> 18001."
echo ""
echo "Teste isolado do wrapper de native messaging (sem depender do navegador):"
echo "  printf '\\x0d\\x00\\x00\\x00{\"test\":true}' | $WRAPPER | xxd | head -3"
