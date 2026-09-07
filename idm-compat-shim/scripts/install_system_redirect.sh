#!/usr/bin/env bash
# install_system_redirect.sh — instala o redirect de porta 1001 -> $TARGET_PORT
# via nftables. PRECISA DE SUDO. Separado do install_user.sh de propósito:
# é a única parte de toda a instalação que roda com privilégio, e deve
# ficar bem visível/isolada.
#
# Uso:
#   sudo ./install_system_redirect.sh [porta_alvo]
#   sudo ./install_system_redirect.sh 18001   # padrão se omitido

set -euo pipefail

if [ "$EUID" -ne 0 ]; then
  echo "ERRO: precisa rodar com sudo (a regra nftables exige root)." >&2
  echo "Uso: sudo $0 [porta_alvo]" >&2
  exit 1
fi

TARGET_PORT="${1:-18001}"
if ! [[ "$TARGET_PORT" =~ ^[0-9]+$ ]] || [ "$TARGET_PORT" -lt 1024 ] || [ "$TARGET_PORT" -gt 65535 ]; then
  echo "ERRO: porta alvo inválida: '$TARGET_PORT' (precisa ser numérica, entre 1024 e 65535)." >&2
  exit 1
fi

mkdir -p /etc/idm-compat-shim

# Gera o .nft com a porta alvo real, em vez de copiar o template estático
# (que tinha 18001 fixo) -- garante que a regra bate com o SHIM_PORT que
# o usuário efetivamente configurou, mesmo se for diferente do padrão.
cat > /etc/idm-compat-shim/idm-shim-port-redirect.nft << EOF
#!/usr/sbin/nft -f
#
# Redireciona 127.0.0.1:1001 (porta que a extensao original do IDM espera,
# privilegiada, <1024) para 127.0.0.1:$TARGET_PORT (onde o idm-compat-shim
# realmente escuta, rodando sem privilegio nenhum como usuario normal).
#
# Gerado por install_system_redirect.sh -- NAO editar direto; rode o
# script de novo com a porta certa se precisar mudar.

table ip idm_shim_nat {
	chain prerouting {
		type nat hook prerouting priority dstnat; policy accept;
		tcp dport 1001 dnat to 127.0.0.1:$TARGET_PORT
	}

	chain output {
		type nat hook output priority dstnat; policy accept;
		tcp dport 1001 dnat to 127.0.0.1:$TARGET_PORT
	}
}
EOF

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cp "$PROJECT_ROOT/systemd/idm-shim-port-redirect.service" /etc/systemd/system/

systemctl daemon-reload
systemctl enable --now idm-shim-port-redirect.service

echo ""
echo "Redirect instalado (1001 -> $TARGET_PORT). Verificando..."
nft list table ip idm_shim_nat 2>/dev/null || echo "AVISO: tabela nft não apareceu -- confira 'systemctl status idm-shim-port-redirect.service'"
echo ""
echo "Lembrete: SHIM_PORT em ~/.config/idm-compat-shim/config.env deve ser"
echo "$TARGET_PORT (não 1001) para o redirect fazer sentido."
