package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"idm-compat-shim/internal/nativehost"
	"idm-compat-shim/internal/server"
)

// Version do idm-compat-shim. Independente da versão do idm-linux-bridge
// (Version em internal/server dele) — são projetos separados agora.
const Version = "0.1.0-dev"

// Códigos de saída — mesma convenção do idm-linux-bridge (ver cmd/bridge/main.go lá).
const ExitCodeRestart = 2

func main() {
	var (
		port         = flag.Int("port", 1001, "Porta do servidor WS (a extensao original do IDM espera 1001 fixo)")
		host         = flag.String("host", "127.0.0.1", "Host do servidor WS")
		winePfx      = flag.String("wine-prefix", "", "Caminho do prefixo Wine (padrao: ~/.wine)")
		idmPath      = flag.String("idm-path", "", "Caminho do IDMan.exe dentro do prefixo Wine")
		verbose      = flag.Bool("verbose", false, "Log detalhado")
		autoDownload = flag.Bool("auto-download", false, "Disparar downloads reais automaticamente ao detectar recurso (ver aviso em internal/server/server.go antes de habilitar)")
		autoDownloadTypes = flag.String("auto-download-types", "", "Lista separada por vírgula de tipos permitidos pra --auto-download (ex: ZIP,EXE). Vazio = qualquer tipo. Use pra restringir testes controlados.")
		logPath      = flag.String("log-file", "", "Caminho para log JSONL de mensagens decodificadas (vazio = nao loga em arquivo)")
		ver          = flag.Bool("version", false, "Exibir versao")
		nmHost       = flag.Bool("native-messaging-host", false, "Modo stdio do Chrome/Firefox Native Messaging — invocado pelo navegador, nao pelo usuario diretamente")
	)
	flag.Parse()

	if *ver {
		fmt.Printf("idm-compat-shim v%s\n", Version)
		os.Exit(0)
	}

	// Modo native messaging: o navegador executa o binario diretamente
	// (conforme o "path" no manifest.json registrado) e fala com ele via
	// stdin/stdout. Nao inicia o servidor WS nem o launcher do IDM neste
	// modo — so responde o handshake de deteccao de instalacao.
	// Ver aviso de status em internal/nativehost/nativehost.go.
	if *nmHost {
		nativehost.Run()
		return
	}

	if *logPath == "" {
		home, _ := os.UserHomeDir()
		*logPath = filepath.Join(home, ".config", "idm-compat-shim", "capture.jsonl")
		os.MkdirAll(filepath.Dir(*logPath), 0700)
	}

	cfg := server.Config{
		Host:         *host,
		Port:         *port,
		WinePrefix:   *winePfx,
		IDMPath:      *idmPath,
		Verbose:      *verbose,
		AutoDownload: *autoDownload,
		AutoDownloadTypes: parseCommaList(*autoDownloadTypes),
		LogPath:      *logPath,
	}

	warnIfAutoDownloadTypesLooksWrong(cfg.AutoDownloadTypes)

	srv, err := server.New(cfg)
	if err != nil {
		log.Fatalf("[ERRO] falha ao iniciar servidor: %v\n", err)
	}

	if !*autoDownload {
		log.Println("[AVISO] --auto-download esta DESLIGADO (padrao) — mensagens decodificadas so serao logadas, nenhum download real sera disparado.")
	} else if len(cfg.AutoDownloadTypes) > 0 {
		log.Printf("[AVISO] --auto-download LIGADO, restrito aos tipos: %v — qualquer outro tipo so sera logado.\n", cfg.AutoDownloadTypes)
	} else {
		log.Println("[AVISO] --auto-download LIGADO para QUALQUER tipo de recurso detectado — sem restricao.")
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		log.Println("[INFO] Encerrando idm-compat-shim...")
		srv.Shutdown()
		os.Exit(0)
	}()

	// Mesmo mecanismo de restart por mudanca de servidor grafico do
	// idm-linux-bridge (ver idm-shared/idm/display_watch.go e restart.go).
	go func() {
		newDS := <-srv.Launcher().RestartCh()
		log.Printf("[INFO] Reiniciando — servidor grafico mudou para: %s\n", newDS)
		srv.Shutdown()
		os.Exit(ExitCodeRestart)
	}()

	log.Printf("[INFO] idm-compat-shim v%s iniciado em %s:%d\n", Version, *host, *port)
	if err := srv.Start(); err != nil {
		log.Fatalf("[ERRO] servidor encerrado: %v\n", err)
	}
}

// parseCommaList divide uma lista separada por vírgula (ex: "ZIP,EXE") em
// um slice, removendo espaços em branco e ignorando itens vazios. Lista
// vazia de entrada retorna slice vazio (nil), interpretado por
// autoDownloadAllows como "permite qualquer tipo".
func parseCommaList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// warnIfAutoDownloadTypesLooksWrong avisa alto e cedo (na inicializacao,
// nao só quando um download silenciosamente nunca dispara) se
// --auto-download-types tiver algum valor que claramente não é uma
// extensão de arquivo -- ex: "true"/"false"/"1"/"0", que sugerem
// confusão com a flag --auto-download (liga/desliga) em vez da lista de
// tipos permitidos. Isso já aconteceu na prática: um prompt do
// install.sh confuso levou a AUTO_DOWNLOAD_TYPES=true no config.env,
// e o sintoma foi "nada baixa", sem nenhum erro nem aviso -- só
// descoberto depurando o journal manualmente. Esta checagem existe pra
// isso nunca mais precisar de depuração manual.
func warnIfAutoDownloadTypesLooksWrong(types []string) {
	suspicious := map[string]bool{
		"true": true, "false": true, "1": true, "0": true,
		"s": true, "n": true, "sim": true, "nao": true, "não": true,
		"yes": true, "no": true, "on": true, "off": true,
	}
	for _, t := range types {
		if suspicious[strings.ToLower(t)] {
			log.Printf("[AVISO CRITICO] --auto-download-types contém %q, que parece um valor de liga/desliga, não uma extensão de arquivo (ex: ZIP, EXE, MP4).\n", t)
			log.Println("[AVISO CRITICO] Isso vai bloquear TODO download (nenhum tipo real vai bater com esse valor) sem nenhum outro aviso.")
			log.Println("[AVISO CRITICO] Corrija AUTO_DOWNLOAD_TYPES no config.env: deixe vazio para permitir qualquer tipo, ou liste extensões reais separadas por vírgula.")
		}
	}
}
