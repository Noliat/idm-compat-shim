#!/usr/bin/env bash
# ============================================================
# IDM Compat Shim — Remover Native Messaging Host registrado
# ============================================================
# Remove os manifests com.tonec.idm.json instalados por
# scripts/install_native_messaging.sh, e o wrapper gerado
# (idm-compat-shim-nmhost.sh). Não desinstala o binário nem o
# serviço — para isso, use scripts/install.sh (opção "Desinstalar")
# ou `make uninstall`.
# ============================================================
set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
BLUE='\033[0;34m'; NC='\033[0m'; BOLD='\033[1m'

log()  { echo -e "${GREEN}[✓]${NC} $*"; }
info() { echo -e "${BLUE}[i]${NC} $*"; }
warn() { echo -e "${YELLOW}[!]${NC} $*"; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

MANIFEST_PATHS=(
  "$HOME/.config/google-chrome/NativeMessagingHosts/com.tonec.idm.json"
  "$HOME/.config/chromium/NativeMessagingHosts/com.tonec.idm.json"
  "$HOME/.config/microsoft-edge/NativeMessagingHosts/com.tonec.idm.json"
  "$HOME/.config/opera/NativeMessagingHosts/com.tonec.idm.json"
  "$HOME/.config/BraveSoftware/Brave-Browser/NativeMessagingHosts/com.tonec.idm.json"
  "$HOME/.mozilla/native-messaging-hosts/com.tonec.idm.json"
)

removed_any=false
for path in "${MANIFEST_PATHS[@]}"; do
  if [ -f "$path" ]; then
    rm -f "$path"
    log "Removido: $path"
    removed_any=true
  fi
done

if [ -f "$REPO_DIR/idm-compat-shim-nmhost.sh" ]; then
  rm -f "$REPO_DIR/idm-compat-shim-nmhost.sh"
  log "Wrapper removido: $REPO_DIR/idm-compat-shim-nmhost.sh"
  removed_any=true
fi

if [ "$removed_any" = false ]; then
  info "Nenhum manifest de Native Messaging Host encontrado — nada a remover."
else
  warn "Reinicie o(s) navegador(es) para que a extensão original do IDM"
  warn "pare de detectar o shim como 'IDM instalado'."
fi
