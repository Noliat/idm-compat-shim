.PHONY: build install run stop logs clean test fmt lint help version native-messaging uninstall

BINARY_NAME := idm-compat-shim
INSTALL_DIR := $(HOME)/.local/bin
CONFIG_DIR  := $(HOME)/.config/idm-compat-shim

# ─── Build ──────────────────────────────────────────────────
# A versão não é fixada aqui — vem de cmd/shim/main.go (const Version),
# mesma convenção do idm-linux-bridge: uma única fonte de verdade do
# SemVer, consultada tanto por este Makefile quanto por --version.

version:
	@grep -oP 'const Version = "\K[^"]+' cmd/shim/main.go

build:
	@echo "→ Compilando $(BINARY_NAME) v$$(make -s version)..."
	@CGO_ENABLED=0 GOOS=linux go build \
		-ldflags="-s -w" \
		-o bin/$(BINARY_NAME) \
		./cmd/shim/
	@echo "✓ Binário: bin/$(BINARY_NAME)"

build-debug:
	@go build -race -o bin/$(BINARY_NAME)-debug ./cmd/shim/

# ─── Instalação ─────────────────────────────────────────────

install: build
	@mkdir -p $(INSTALL_DIR)
	@cp bin/$(BINARY_NAME) $(INSTALL_DIR)/
	@chmod +x $(INSTALL_DIR)/$(BINARY_NAME)
	@echo "✓ Instalado em: $(INSTALL_DIR)/$(BINARY_NAME)"

native-messaging: install
	@./scripts/install_native_messaging.sh $(INSTALL_DIR)/$(BINARY_NAME)

# ─── Execução local ─────────────────────────────────────────

run:
	@go run ./cmd/shim/ --verbose

run-auto-download:
	@echo "⚠ AVISO: --auto-download habilitado — ver ressalvas no README antes de usar."
	@go run ./cmd/shim/ --verbose --auto-download

# ─── Serviço ────────────────────────────────────────────────

service-start:
	systemctl --user start idm-compat-shim

service-stop:
	systemctl --user stop idm-compat-shim

service-restart:
	systemctl --user restart idm-compat-shim

service-status:
	systemctl --user status idm-compat-shim

logs:
	journalctl --user -u idm-compat-shim -f

# ─── Testes ─────────────────────────────────────────────────

test:
	@go test ./... -v

# ─── Qualidade ──────────────────────────────────────────────

fmt:
	@go fmt ./...
	@echo "✓ Código formatado"

lint:
	@go vet ./...

# ─── Limpeza ────────────────────────────────────────────────

clean:
	@rm -rf bin/
	@go clean
	@echo "✓ Limpo"

uninstall:
	@systemctl --user stop idm-compat-shim 2>/dev/null || true
	@systemctl --user disable idm-compat-shim 2>/dev/null || true
	@rm -f $(INSTALL_DIR)/$(BINARY_NAME)
	@rm -rf $(CONFIG_DIR)
	@echo "✓ Desinstalado (manifests de native messaging não removidos — rode scripts/uninstall_native_messaging.sh)"

# ─── Help ───────────────────────────────────────────────────

help:
	@echo ""
	@echo "IDM Compat Shim — Comandos disponíveis:"
	@echo ""
	@echo "  make version            Exibir versão atual"
	@echo "  make build              Compilar binário"
	@echo "  make install            Compilar e instalar em ~/.local/bin"
	@echo "  make native-messaging   Instalar + registrar Native Messaging Host"
	@echo "  make run                Rodar localmente, só logando (sem auto-download)"
	@echo "  make run-auto-download  Rodar localmente com downloads reais habilitados"
	@echo "  make test               Rodar testes unitários"
	@echo "  make logs               Ver logs do serviço systemd"
	@echo "  make service-start      Iniciar serviço"
	@echo "  make service-stop       Parar serviço"
	@echo "  make clean              Remover binários compilados"
	@echo "  make uninstall          Remover binário e configuração"
	@echo ""

.DEFAULT_GOAL := help
