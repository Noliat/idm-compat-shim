#!/usr/bin/env bash
# ============================================================
# IDM Compat Shim — Instalador Automatizado
# Compatível com: Ubuntu 20.04+, Debian 11+, Fedora 36+, Arch
# ============================================================
#
# Diferenças em relação ao install.sh do idm-linux-bridge (irmão deste
# projeto):
#   • Não instala extensão própria — registra um Native Messaging Host
#     (com.tonec.idm) para a extensão ORIGINAL do IDM, que o usuário
#     precisa ter instalado separadamente (Chrome/Firefox Web Store).
#   • Porta do servidor WS é fixa em 1001 por padrão (a extensão
#     original tem esse valor hardcoded em background.js) — mudar só
#     faz sentido em cenário de debug/teste.
#   • Pergunta sobre --auto-download com um aviso explícito: por
#     padrão o shim só loga o que a extensão detecta, sem disparar
#     downloads de verdade (ver README para o motivo).
#   • Se detectar uma instalação existente do idm-linux-bridge
#     (~/.config/idm-bridge/config.env), oferece reaproveitar o mesmo
#     wine-prefix / caminho do IDMan.exe em vez de perguntar de novo.
#
# Este script assume que o módulo idm-shared está disponível como
# diretório irmão (../idm-shared) — ver MIGRATION_ilb.md para o layout
# de pastas esperado.
# ============================================================
set -euo pipefail

# ─── Cores ────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
BLUE='\033[0;34m'; CYAN='\033[0;36m'; NC='\033[0m'; BOLD='\033[1m'

# ─── Configurações padrão ────────────────────────────────────
INSTALL_DIR="$HOME/.local/bin"
CONFIG_DIR="$HOME/.config/idm-compat-shim"
SERVICE_DIR="$HOME/.config/systemd/user"
AUTOSTART_DIR="$HOME/.config/autostart"
SHIM_PORT=18001
WINE_PREFIX="$HOME/.wine"
IDM_DEFAULT_PATH="$WINE_PREFIX/drive_c/Program Files (x86)/Internet Download Manager/IDMan.exe"
REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
AUTO_DOWNLOAD="false"

# Config existente do idm-linux-bridge, se houver — usada pra sugerir
# defaults em vez de perguntar do zero (os dois projetos controlam a
# mesma instalação do IDM via Wine).
ILB_CONFIG="$HOME/.config/idm-bridge/config.env"

STARTUP_MODE=""

# ─── Helpers ─────────────────────────────────────────────────

log()    { echo -e "${GREEN}[✓]${NC} $*"; }
info()   { echo -e "${BLUE}[i]${NC} $*"; }
warn()   { echo -e "${YELLOW}[!]${NC} $*"; }
error()  { echo -e "${RED}[✗]${NC} $*" >&2; }
header() { echo -e "\n${BOLD}${CYAN}── $* ──${NC}\n"; }
ask()    { echo -en "${YELLOW}[?]${NC} $1 "; }

# ─── Verificar sistema operacional ───────────────────────────

detect_distro() {
  if [ -f /etc/os-release ]; then
    . /etc/os-release
    DISTRO_ID="${ID:-unknown}"
    DISTRO_LIKE="${ID_LIKE:-}"
  elif command -v lsb_release &>/dev/null; then
    DISTRO_ID=$(lsb_release -si | tr '[:upper:]' '[:lower:]')
  else
    DISTRO_ID="unknown"
  fi
}

get_pkg_manager() {
  if command -v apt-get &>/dev/null; then echo "apt"
  elif command -v dnf &>/dev/null;     then echo "dnf"
  elif command -v pacman &>/dev/null;  then echo "pacman"
  elif command -v zypper &>/dev/null;  then echo "zypper"
  else echo "unknown"; fi
}

# ─── Instalação de dependências ──────────────────────────────
# Mesmas dependências do idm-linux-bridge (Go + Wine) — o shim usa o
# mesmo idm.Launcher via idm-shared, então precisa do mesmo ambiente
# pra lançar o IDMan.exe.

install_dependencies() {
  header "Verificando dependências"

  local pkg_mgr
  pkg_mgr=$(get_pkg_manager)

  if ! command -v go &>/dev/null; then
    warn "Go não encontrado. Instalando..."
    install_go "$pkg_mgr"
  else
    local go_version
    go_version=$(go version | grep -oP '\d+\.\d+' | head -1)
    log "Go $go_version encontrado"
  fi

  if ! command -v wine &>/dev/null && ! command -v wine64 &>/dev/null; then
    warn "Wine não encontrado. Instalando..."
    install_wine "$pkg_mgr"
  else
    log "Wine $(wine --version 2>/dev/null || echo 'encontrado')"
  fi

  if ! command -v curl &>/dev/null; then
    install_pkg "$pkg_mgr" "curl"
  fi
}

install_go() {
  local pkg_mgr=$1
  case "$pkg_mgr" in
    apt)    sudo apt-get update -q && sudo apt-get install -y golang-go ;;
    dnf)    sudo dnf install -y golang ;;
    pacman) sudo pacman -S --noconfirm go ;;
    zypper) sudo zypper install -y go ;;
    *)
      local GO_VER="1.22.0"
      local ARCH
      ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
      local GO_TAR="go${GO_VER}.linux-${ARCH}.tar.gz"
      info "Baixando Go $GO_VER..."
      curl -fsSL "https://go.dev/dl/${GO_TAR}" -o "/tmp/${GO_TAR}"
      sudo tar -C /usr/local -xzf "/tmp/${GO_TAR}"
      export PATH=$PATH:/usr/local/go/bin
      echo 'export PATH=$PATH:/usr/local/go/bin' >> "$HOME/.profile"
      rm "/tmp/${GO_TAR}"
      ;;
  esac
  log "Go instalado"
}

install_wine() {
  local pkg_mgr=$1
  info "Instalando Wine (pode demorar alguns minutos)..."
  case "$pkg_mgr" in
    apt)
      sudo dpkg --add-architecture i386
      sudo apt-get update -q
      sudo apt-get install -y wine wine32 wine64 ;;
    dnf)    sudo dnf install -y wine ;;
    pacman) sudo pacman -S --noconfirm wine wine-mono ;;
    zypper) sudo zypper install -y wine ;;
    *)
      error "Instale o Wine manualmente: https://www.winehq.org/"
      exit 1 ;;
  esac
  log "Wine instalado"
}

install_pkg() {
  local pkg_mgr=$1
  local pkg=$2
  case "$pkg_mgr" in
    apt)    sudo apt-get install -y "$pkg" ;;
    dnf)    sudo dnf install -y "$pkg" ;;
    pacman) sudo pacman -S --noconfirm "$pkg" ;;
    zypper) sudo zypper install -y "$pkg" ;;
  esac
}

# ─── Verificar módulo idm-shared ──────────────────────────────

check_idm_shared() {
  header "Verificando módulo idm-shared"

  local shared_dir="$REPO_DIR/../idm-shared"
  if [ ! -d "$shared_dir" ]; then
    error "Módulo idm-shared não encontrado em: $shared_dir"
    error "O idm-compat-shim depende dele (ver MIGRATION_ilb.md pro layout de pastas esperado)."
    error "Coloque o idm-shared como diretório irmão deste projeto e rode de novo."
    exit 1
  fi
  log "idm-shared encontrado em: $shared_dir"
}

# ─── Compilar o shim ──────────────────────────────────────────

build_shim() {
  header "Compilando IDM Compat Shim (Go)"

  cd "$REPO_DIR"

  SHIM_VERSION=$(grep -oP 'const Version = "\K[^"]+' cmd/shim/main.go 2>/dev/null || echo "desconhecida")
  info "Versão detectada no código-fonte: $SHIM_VERSION"

  info "Baixando dependências Go..."
  go mod tidy
  info "Compilando binário..."
  CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /tmp/idm-compat-shim \
    ./cmd/shim/

  mkdir -p "$INSTALL_DIR"
  mv /tmp/idm-compat-shim "$INSTALL_DIR/idm-compat-shim"
  chmod +x "$INSTALL_DIR/idm-compat-shim"

  log "Binário instalado em: $INSTALL_DIR/idm-compat-shim"
}

# ─── Configuração ────────────────────────────────────────────

configure() {
  header "Configuração"

  mkdir -p "$CONFIG_DIR"

  # Se o idm-linux-bridge já estiver instalado e configurado, sugerir
  # os mesmos valores em vez de perguntar do zero — os dois projetos
  # controlam a mesma instância do IDM via Wine.
  if [ -f "$ILB_CONFIG" ]; then
    info "Configuração existente do idm-linux-bridge encontrada: $ILB_CONFIG"
    ask "Reaproveitar o mesmo wine-prefix e caminho do IDMan.exe? [S/n]:"
    read -r reuse
    if [[ "${reuse,,}" != "n" ]]; then
      # shellcheck disable=SC1090
      source "$ILB_CONFIG"
      WINE_PREFIX="${WINE_PREFIX:-$HOME/.wine}"
      IDM_DEFAULT_PATH="${IDM_PATH:-$IDM_DEFAULT_PATH}"
      log "Reaproveitando configuração do idm-linux-bridge"
    fi
  fi

  ask "Caminho do prefixo Wine [$WINE_PREFIX]:"
  read -r user_prefix
  WINE_PREFIX="${user_prefix:-$WINE_PREFIX}"

  local idm_path="$IDM_DEFAULT_PATH"
  ask "Caminho do IDMan.exe [$idm_path]:"
  read -r user_idm
  idm_path="${user_idm:-$idm_path}"

  if [ ! -f "$idm_path" ]; then
    warn "IDMan.exe não encontrado em: $idm_path"
    warn "Configure o caminho correto em: $CONFIG_DIR/config.env"
  else
    log "IDMan.exe encontrado!"
  fi

  echo ""
  info "A extensão original do IDM sempre tenta ws://127.0.0.1:1001 (valor"
  info "fixo no código dela) — mas 1001 é uma porta PRIVILEGIADA no Linux"
  info "(<1024), e o shim roda como usuário comum, sem privilégio nenhum,"
  info "de propósito. Por isso o shim escuta numa porta comum (18001 por"
  info "padrão) e uma regra de firewall (nftables, instalada a seguir,"
  info "precisa de sudo) redireciona 1001 -> 18001 no nível do kernel."
  ask "Porta do servidor WS [$SHIM_PORT] (mude só se souber o que está fazendo):"
  read -r user_port
  SHIM_PORT="${user_port:-$SHIM_PORT}"

  echo ""
  warn "Sobre --auto-download:"
  echo "    Desligado (padrão): o shim decodifica e loga tudo que a extensão"
  echo "    detecta, mas NÃO dispara downloads reais no IDM."
  echo "    Ligado: qualquer recurso reconhecido (URL + tipo + nome de"
  echo "    arquivo) vira um download de verdade. Ainda não temos certeza"
  echo "    de que 'recurso detectado' sempre corresponde a uma intenção"
  echo "    real de download do usuário — ver README para detalhes."
  echo ""
  ask "Habilitar --auto-download agora? [s/N]:"
  read -r auto_dl
  AUTO_DOWNLOAD_TYPES=""
  if [[ "${auto_dl,,}" == "s" ]]; then
    AUTO_DOWNLOAD="true"
    warn "auto-download HABILITADO — monitore os logs de perto no início."
    echo ""
    echo "    Restringir a EXTENSÕES DE ARQUIVO específicas (não é sim/não)."
    echo "    Exemplo: ZIP,EXE,MP4  —  deixe em branco para permitir qualquer tipo."
    ask "Extensões (ou Enter para não restringir):"
    read -r auto_dl_types
    AUTO_DOWNLOAD_TYPES="$auto_dl_types"
  else
    AUTO_DOWNLOAD="false"
    log "auto-download desligado (recomendado por enquanto)"
  fi

  cat > "$CONFIG_DIR/config.env" << EOF
# IDM Compat Shim — Configuração
# Editado em: $(date)

SHIM_PORT=$SHIM_PORT
SHIM_HOST=127.0.0.1
WINE_PREFIX=$WINE_PREFIX
IDM_PATH=$idm_path
VERBOSE=false
AUTO_DOWNLOAD=$AUTO_DOWNLOAD
AUTO_DOWNLOAD_TYPES=$AUTO_DOWNLOAD_TYPES
EOF

  log "Configuração salva em: $CONFIG_DIR/config.env"
  info "Pra ligar/desligar auto-download depois, sem reinstalar: edite"
  info "$CONFIG_DIR/config.env e rode 'systemctl --user restart idm-compat-shim'."
}

# ─── Escolha do modo de inicialização ────────────────────────
# Mesmo conceito do idm-linux-bridge — ver comentário lá para detalhes
# de cada modo (boot via systemd+linger vs. login via XDG Autostart).

choose_startup_mode() {
  header "Modo de inicialização"

  echo -e "  ${BOLD}Como o IDM Compat Shim deve iniciar?${NC}"
  echo ""
  echo -e "  ${CYAN}1)${NC} ${BOLD}Com o sistema${NC} (antes do login)"
  echo -e "     Usa systemd --user + loginctl enable-linger"
  echo ""
  echo -e "  ${CYAN}2)${NC} ${BOLD}Após o login${NC} (recomendado)"
  echo -e "     Usa XDG Autostart (~/.config/autostart/)"
  echo ""

  local choice=""
  while [[ "$choice" != "1" && "$choice" != "2" ]]; do
    ask "Opção [2]:"
    read -r choice
    choice="${choice:-2}"
    if [[ "$choice" != "1" && "$choice" != "2" ]]; then
      warn "Digite 1 ou 2"
    fi
  done

  if [ "$choice" = "1" ]; then
    STARTUP_MODE="boot"
    log "Modo selecionado: iniciar COM O SISTEMA"
  else
    STARTUP_MODE="login"
    log "Modo selecionado: iniciar APÓS O LOGIN"
  fi
}

_build_exec_start() {
  # IMPORTANTE: usa \${AUTO_DOWNLOAD} (substituição em RUNTIME pelo
  # systemd, via EnvironmentFile) em vez de decidir aqui na hora de
  # instalar. Antes, esta função gravava "--auto-download" como texto
  # fixo dentro do .service quando AUTO_DOWNLOAD=true estava setado
  # durante a instalação -- e como não usava a variável, editar o
  # config.env depois NÃO tinha efeito nenhum: o .service já gerado
  # continuava com o valor antigo cravado, e só reinstalar (rodando
  # este script de novo) corrigia. Usando --auto-download=${AUTO_DOWNLOAD}
  # (sintaxe --flag=valor, que o pacote "flag" do Go aceita como
  # true/false explícito), o config.env passa a ser a única fonte da
  # verdade, editável a qualquer momento sem precisar reinstalar --
  # só reiniciar o serviço (systemctl --user restart idm-compat-shim).
  echo "$INSTALL_DIR/idm-compat-shim \\
  --host \${SHIM_HOST} \\
  --port \${SHIM_PORT} \\
  --wine-prefix \${WINE_PREFIX} \\
  --idm-path \${IDM_PATH} \\
  --auto-download=\${AUTO_DOWNLOAD} \\
  --auto-download-types=\${AUTO_DOWNLOAD_TYPES} \\
  --verbose=\${VERBOSE}"
}

install_service_boot() {
  header "Configurando serviço systemd (modo: com o sistema)"

  mkdir -p "$SERVICE_DIR"

  cat > "$SERVICE_DIR/idm-compat-shim.service" << EOF
[Unit]
Description=IDM Compat Shim — ponte entre a extensão original do IDM e o IDM via Wine
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=$CONFIG_DIR/config.env
ExecStart=$(_build_exec_start)
Restart=on-failure
RestartSec=3
StartLimitIntervalSec=60
StartLimitBurst=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=idm-compat-shim

NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=default.target
EOF

  _disable_autostart_entry 2>/dev/null || true

  info "Habilitando linger para o usuário '$(whoami)'..."
  if loginctl enable-linger "$(whoami)" 2>/dev/null; then
    log "Linger habilitado com sucesso"
  else
    warn "Não foi possível habilitar linger automaticamente."
    warn "Execute manualmente: sudo loginctl enable-linger $(whoami)"
  fi

  systemctl --user daemon-reload
  systemctl --user enable --now idm-compat-shim.service

  _save_startup_mode "boot"

  log "Serviço idm-compat-shim habilitado (modo: boot)"
  info "Para ver logs:    journalctl --user -u idm-compat-shim -f"
  info "Para parar:       systemctl --user stop idm-compat-shim"
}

install_service_login() {
  header "Configurando autostart pós-login (modo: após o login)"

  mkdir -p "$AUTOSTART_DIR" "$SERVICE_DIR"

  cat > "$SERVICE_DIR/idm-compat-shim.service" << EOF
[Unit]
Description=IDM Compat Shim — ponte entre a extensão original do IDM e o IDM via Wine
After=graphical-session.target network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=$CONFIG_DIR/config.env
ExecStart=$(_build_exec_start)
Restart=on-failure
RestartSec=3
StartLimitIntervalSec=60
StartLimitBurst=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=idm-compat-shim

NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=graphical-session.target
EOF

  if systemctl --user is-enabled idm-compat-shim 2>/dev/null | grep -q "enabled"; then
    info "Desabilitando serviço boot anterior..."
    systemctl --user disable idm-compat-shim 2>/dev/null || true
  fi
  if loginctl show-user "$(whoami)" 2>/dev/null | grep -q "Linger=yes"; then
    info "Removendo linger (não necessário para modo login)..."
    loginctl disable-linger "$(whoami)" 2>/dev/null || \
      warn "Não foi possível desativar linger. Execute: sudo loginctl disable-linger $(whoami)"
  fi

  systemctl --user daemon-reload

  cat > "$AUTOSTART_DIR/idm-compat-shim.desktop" << EOF
[Desktop Entry]
Type=Application
Name=IDM Compat Shim
Comment=Ponte entre a extensão original do IDM e o IDM via Wine
Exec=systemctl --user start idm-compat-shim
Icon=network-server
Terminal=false
Hidden=false
X-GNOME-Autostart-enabled=true
X-GNOME-Autostart-Delay=3
X-KDE-autostart-after=panel
X-Mate-Autostart-enabled=true
EOF

  chmod +x "$AUTOSTART_DIR/idm-compat-shim.desktop"

  _save_startup_mode "login"

  log "Autostart pós-login configurado"
  info "Arquivo: $AUTOSTART_DIR/idm-compat-shim.desktop"

  echo ""
  ask "Iniciar o shim agora? [S/n]:"
  read -r start_now
  if [[ "${start_now,,}" != "n" ]]; then
    systemctl --user start idm-compat-shim && log "Shim iniciado!" || \
      warn "Não foi possível iniciar. Tente: systemctl --user start idm-compat-shim"
  fi
}

install_service() {
  if [ -z "$STARTUP_MODE" ]; then
    choose_startup_mode
  fi

  case "$STARTUP_MODE" in
    boot)  install_service_boot  ;;
    login) install_service_login ;;
    *)
      error "Modo de startup inválido: '$STARTUP_MODE'"
      exit 1 ;;
  esac
}

change_startup_mode() {
  header "Alterar modo de inicialização"

  local current_mode
  current_mode=$(_load_startup_mode)

  if [ -n "$current_mode" ]; then
    echo -e "  Modo atual: ${CYAN}${BOLD}$current_mode${NC}"
  else
    echo -e "  ${YELLOW}Nenhum modo configurado ainda.${NC}"
  fi
  echo ""

  choose_startup_mode

  systemctl --user stop idm-compat-shim 2>/dev/null || true

  install_service
}

_save_startup_mode() {
  local mode=$1
  if grep -q "^STARTUP_MODE=" "$CONFIG_DIR/config.env" 2>/dev/null; then
    sed -i "s/^STARTUP_MODE=.*/STARTUP_MODE=$mode/" "$CONFIG_DIR/config.env"
  else
    echo "" >> "$CONFIG_DIR/config.env"
    echo "# Modo de inicialização: boot (com o sistema) ou login (após login gráfico)" >> "$CONFIG_DIR/config.env"
    echo "STARTUP_MODE=$mode" >> "$CONFIG_DIR/config.env"
  fi
}

_load_startup_mode() {
  if [ -f "$CONFIG_DIR/config.env" ]; then
    local mode
    mode=$(grep "^STARTUP_MODE=" "$CONFIG_DIR/config.env" 2>/dev/null | cut -d= -f2 | tr -d ' ')
    echo "${mode:-}"
  fi
}

_disable_autostart_entry() {
  local desktop="$AUTOSTART_DIR/idm-compat-shim.desktop"
  if [ -f "$desktop" ]; then
    info "Removendo entrada de autostart anterior..."
    rm -f "$desktop"
  fi
}

# ─── PATH do usuário ─────────────────────────────────────────

setup_path() {
  local shell_rc=""
  case "${SHELL:-}" in
    */bash) shell_rc="$HOME/.bashrc" ;;
    */zsh)  shell_rc="$HOME/.zshrc"  ;;
    */fish) shell_rc="$HOME/.config/fish/config.fish" ;;
    *)      shell_rc="$HOME/.profile" ;;
  esac

  if ! grep -q "$INSTALL_DIR" "$shell_rc" 2>/dev/null; then
    echo "export PATH=\"\$PATH:$INSTALL_DIR\"" >> "$shell_rc"
    info "PATH atualizado em: $shell_rc"
    info "Rode: source $shell_rc  (ou abra um novo terminal)"
  fi
}

# ─── Native Messaging Host ────────────────────────────────────

# ─── Redirect de porta privilegiada (nftables, precisa sudo) ─
# A extensão original SEMPRE tenta ws://127.0.0.1:1001 (valor fixo no
# código dela, não configurável). Se SHIM_PORT for diferente de 1001
# (o padrão recomendado, ex: 18001), precisa desse redirect pra ligar
# os dois. Só pula se o usuário deliberadamente configurou SHIM_PORT=1001
# (nesse caso o shim tentaria bindar a porta privilegiada direto, o que
# tem seus próprios problemas — não é o caminho recomendado).

install_port_redirect() {
  header "Redirect de porta privilegiada"

  if [ "$SHIM_PORT" -eq 1001 ] 2>/dev/null; then
    warn "SHIM_PORT=1001 — sem redirect, o shim vai tentar bindar essa"
    warn "porta privilegiada diretamente, o que provavelmente falha sem"
    warn "privilégio (bind: permission denied). Considere usar 18001."
    return
  fi

  echo "  A extensão original do IDM sempre tenta ws://127.0.0.1:1001 (fixo"
  echo "  no código dela). O shim está configurado pra escutar em"
  echo "  $SHIM_PORT (porta comum, sem privilégio) — precisa de uma regra"
  echo "  de firewall redirecionando 1001 -> $SHIM_PORT pra ligar os dois."
  echo ""
  echo "  Isso instala essa regra (nftables) como serviço de SISTEMA (root)"
  echo "  -- é a ÚNICA parte de toda a instalação que pede privilégio"
  echo "  elevado. Sem isso, a extensão nunca vai conseguir alcançar o"
  echo "  shim (a conexão dela sempre bate em 1001, nunca em $SHIM_PORT"
  echo "  diretamente)."
  echo ""
  warn "Isso vai pedir sua senha de sudo agora."
  ask "Continuar? [S/n]:"
  read -r do_redirect
  if [[ "${do_redirect,,}" == "n" ]]; then
    warn "Pulado. Rode manualmente depois: sudo ./scripts/install_system_redirect.sh $SHIM_PORT"
    warn "Sem isso a extensão não vai conseguir alcançar o shim."
    return
  fi

  if sudo "$REPO_DIR/scripts/install_system_redirect.sh" "$SHIM_PORT"; then
    log "Redirect de porta instalado (1001 -> $SHIM_PORT)"
  else
    error "Falha ao instalar o redirect. Rode manualmente:"
    error "  sudo $REPO_DIR/scripts/install_system_redirect.sh $SHIM_PORT"
  fi
}

install_native_messaging() {
  header "Native Messaging Host"

  echo "  Isso registra o idm-compat-shim como 'com.tonec.idm' nos"
  echo "  navegadores instalados, para que a extensão ORIGINAL do IDM"
  echo "  o detecte como se fosse o IDM de verdade."
  echo ""
  echo -e "  ${YELLOW}Pré-requisito:${NC} a extensão original do IDM precisa já"
  echo "  estar instalada no navegador (Chrome/Firefox Web Store) — este"
  echo "  script não a instala."
  echo ""

  "$REPO_DIR/scripts/install_native_messaging.sh" "$INSTALL_DIR/idm-compat-shim"
}

# ─── Verificação final ───────────────────────────────────────

verify_installation() {
  header "Verificando instalação"

  local ok=true

  if [ -x "$INSTALL_DIR/idm-compat-shim" ]; then
    log "Binário: $INSTALL_DIR/idm-compat-shim"
  else
    error "Binário não encontrado"
    ok=false
  fi

  if [ -f "$CONFIG_DIR/config.env" ]; then
    log "Configuração: $CONFIG_DIR/config.env"
  else
    error "Arquivo de configuração não encontrado"
    ok=false
  fi

  local current_mode
  current_mode=$(_load_startup_mode)

  case "$current_mode" in
    boot|login)
      log "Modo de startup: $current_mode"
      if systemctl --user is-active --quiet idm-compat-shim 2>/dev/null; then
        log "Serviço: idm-compat-shim rodando ✓"
      else
        info "Serviço não rodando ainda (normal dependendo do modo escolhido)"
      fi
      ;;
    *)
      warn "Modo de startup não configurado"
      ok=false
      ;;
  esac

  echo ""
  if [ "$ok" = true ]; then
    echo -e "${GREEN}${BOLD}✓ IDM Compat Shim instalado com sucesso!${NC}"
    echo ""
    echo -e "  ${CYAN}journalctl --user -u idm-compat-shim -f${NC}  — acompanhar logs"
    echo -e "  ${CYAN}~/.config/idm-compat-shim/capture.jsonl${NC}   — mensagens decodificadas"
    echo -e "  Para alterar modo de inicialização: ${CYAN}$0 --change-startup${NC}"
  else
    echo -e "${YELLOW}${BOLD}Instalação concluída com avisos. Verifique os erros acima.${NC}"
  fi
}

# ─── Desinstalar ─────────────────────────────────────────────

uninstall() {
  header "Desinstalando IDM Compat Shim"

  systemctl --user stop    idm-compat-shim 2>/dev/null || true
  systemctl --user disable idm-compat-shim 2>/dev/null || true
  rm -f "$SERVICE_DIR/idm-compat-shim.service"
  systemctl --user daemon-reload 2>/dev/null || true

  rm -f "$AUTOSTART_DIR/idm-compat-shim.desktop"
  info "Entrada de autostart removida"

  if loginctl show-user "$(whoami)" 2>/dev/null | grep -q "Linger=yes"; then
    loginctl disable-linger "$(whoami)" 2>/dev/null && \
      info "Linger desabilitado" || \
      warn "Execute: sudo loginctl disable-linger $(whoami)"
  fi

  rm -f "$INSTALL_DIR/idm-compat-shim"
  rm -rf "$CONFIG_DIR"

  ask "Também remover os manifests de Native Messaging Host registrados? [s/N]:"
  read -r rm_nm
  if [[ "${rm_nm,,}" == "s" ]]; then
    rm -f "$HOME/.config/google-chrome/NativeMessagingHosts/com.tonec.idm.json" \
          "$HOME/.config/chromium/NativeMessagingHosts/com.tonec.idm.json" \
          "$HOME/.config/microsoft-edge/NativeMessagingHosts/com.tonec.idm.json" \
          "$HOME/.config/opera/NativeMessagingHosts/com.tonec.idm.json" \
          "$HOME/.config/BraveSoftware/Brave-Browser/NativeMessagingHosts/com.tonec.idm.json" \
          "$HOME/.mozilla/native-messaging-hosts/com.tonec.idm.json"
    rm -f "$REPO_DIR/idm-compat-shim-nmhost.sh"
    log "Manifests de Native Messaging removidos"
  fi

  if systemctl is-enabled idm-shim-port-redirect.service &>/dev/null; then
    echo ""
    ask "Também remover o redirect de porta de sistema (precisa sudo)? [s/N]:"
    read -r rm_redirect
    if [[ "${rm_redirect,,}" == "s" ]]; then
      if sudo systemctl disable --now idm-shim-port-redirect.service 2>/dev/null; then
        sudo rm -f /etc/systemd/system/idm-shim-port-redirect.service
        sudo rm -rf /etc/idm-compat-shim
        sudo systemctl daemon-reload
        log "Redirect de porta de sistema removido"
      else
        warn "Não foi possível remover automaticamente. Rode manualmente:"
        warn "  sudo systemctl disable --now idm-shim-port-redirect.service"
        warn "  sudo rm -f /etc/systemd/system/idm-shim-port-redirect.service /etc/idm-compat-shim -r"
      fi
    fi
  fi

  log "IDM Compat Shim removido"
}

# ─── Menu principal ──────────────────────────────────────────

show_banner() {
  echo -e "${RED}${BOLD}"
  cat << 'BANNER'
 _____ ____ __  __      ____                            _
|_   _|  _ \  \/  |    / ___|___  _ __ ___  _ __   __ _| |_
  | | | | | | |\/| |  | |   / _ \| '_ ` _ \| '_ \ / _` | __|
  | | | |_| | |  | |  | |__| (_) | | | | | | |_) | (_| | |_
  |_| |____/|_|  |_|   \____\___/|_| |_| |_| .__/ \__,_|\__|
                        Shim                |_|
BANNER
  echo -e "${NC}"
}

show_menu() {
  echo -e "${BOLD}O que deseja fazer?${NC}"
  echo "  1) Instalação completa (recomendado)"
  echo "  2) Apenas compilar"
  echo "  3) Apenas configurar"
  echo "  4) Apenas instalar serviço"
  echo "  5) Apenas registrar Native Messaging Host"
  echo "  6) Alterar modo de inicialização"
  echo "  7) Desinstalar"
  echo "  8) Apenas instalar redirect de porta (precisa sudo)"
  echo "  9) Sair"
  echo ""
  ask "Opção [1]:"
  read -r choice
  choice="${choice:-1}"
}

main() {
  show_banner

  case "${1:-}" in
    --auto)
      STARTUP_MODE="login"
      detect_distro
      install_dependencies
      check_idm_shared
      build_shim
      configure
      install_port_redirect
      install_service
      setup_path
      verify_installation
      return ;;
    --change-startup)
      change_startup_mode
      return ;;
  esac

  show_menu

  case "$choice" in
    1)
      detect_distro
      install_dependencies
      check_idm_shared
      build_shim
      configure
      install_port_redirect
      choose_startup_mode
      install_service
      setup_path
      install_native_messaging
      verify_installation
      ;;
    2) check_idm_shared; build_shim ;;
    3) configure ;;
    4) choose_startup_mode; install_service ;;
    5) install_native_messaging ;;
    6) change_startup_mode ;;
    7) uninstall ;;
    8) install_port_redirect ;;
    9) exit 0 ;;
    *) error "Opção inválida"; exit 1 ;;
  esac
}

if [ "${EUID:-$(id -u)}" -eq 0 ]; then
  warn "Não execute como root — o shim usa instalação em espaço do usuário."
  ask "Continuar mesmo assim? [s/N]:"
  read -r ans
  [[ "${ans,,}" != "s" ]] && exit 1
fi

main "$@"
