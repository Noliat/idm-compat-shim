// Package server implementa o lado do protocolo que conversa com a
// extensão ORIGINAL do IDM (não a extensão própria do projeto) — ou seja,
// a contraparte do que o idm-linux-bridge faz para sua própria extensão.
//
// Duas responsabilidades:
//  1. Servidor WebSocket em 127.0.0.1:1001, protocolo MSG#seq#conn#type#campos;
//     (ver internal/wsprotocol e o histórico de captura de tráfego real).
//  2. Log estruturado de toda mensagem recebida, para continuar mapeando
//     o protocolo (muitos campos ainda não têm significado confirmado).
//
// STATUS: a tradução de mensagens "resource detectado" em downloads reais
// (translateToJob) está implementada só para os campos já confirmados por
// captura de tráfego passiva (grabber detectando mídia/arquivos). NÃO
// temos ainda uma captura do fluxo "usuário clica em baixar" na extensão
// original — então não sabemos com certeza qual mensagem representa uma
// ordem explícita de download vs. apenas uma notificação de recurso visto
// na página. Por segurança, o disparo automático de downloads fica atrás
// da flag --auto-download (default: desligada). Sem ela, o shim só loga.
package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"idm-shared/idm"

	"idm-compat-shim/internal/bootstrap"
	"idm-compat-shim/internal/sdactivation"
	"idm-compat-shim/internal/wsprotocol"
)

// Config contém a configuração do servidor de compatibilidade.
type Config struct {
	Host       string
	Port       int // padrão: 1001, porta fixa que a extensão original usa
	WinePrefix string
	IDMPath    string
	Verbose    bool
	// AutoDownload: quando true, mensagens reconhecidas como "recurso
	// detectado" com campos suficientes disparam idm.Launcher.Launch()
	// de verdade. Quando false (padrão), só loga — ver aviso no topo do
	// arquivo sobre a incerteza no sinal de "comando explícito de
	// download" vs. "notificação passiva de recurso visto".
	AutoDownload bool
	// AutoDownloadTypes: lista de tipos de recurso (campo id=4: "ZIP",
	// "EXE", "MP4", "TS", etc.) permitidos a disparar Launch() de
	// verdade quando AutoDownload=true. Vazia = permite qualquer tipo.
	// Usado pra restringir testes controlados (ex: só "ZIP") sem
	// desligar auto-download por completo.
	AutoDownloadTypes []string
	// LogPath: arquivo JSONL onde cada mensagem decodificada é registrada.
	// Mesmo formato usado pelo idm_ws_sniffer.py exploratório.
	LogPath string
}

var upgrader = websocket.Upgrader{
	// A extensão roda como chrome-extension://<id>, não um Origin http(s)
	// normal — sem checagem de Origin aqui achamos aceitável dado que o
	// servidor só escuta em loopback (127.0.0.1). Reavaliar se algum dia
	// o shim passar a escutar em uma interface não-loopback.
	CheckOrigin: func(r *http.Request) bool { return true },

	// CRÍTICO: a extensão original do IDM conecta com
	// new WebSocket(url, "plugin.v3.internetdownloadmanager.com")
	// -- ou seja, manda um header Sec-WebSocket-Protocol no handshake.
	// Por spec (RFC 6455 §4.2.2), se o cliente manda esse header, o
	// SERVIDOR É OBRIGADO A ECOAR de volta um dos valores oferecidos na
	// resposta do handshake -- senão o Chrome aborta a conexão com
	// "Sent non-empty 'Sec-WebSocket-Protocol' header but no response
	// was received" e fecha com code=1006, mesmo que o resto do
	// handshake estivesse correto. Sem declarar esse subprotocolo aqui,
	// o gorilla/websocket aceita a conexão mas nunca ecoa o header --
	// era essa a causa raiz de NUNCA vermos connection_open no log,
	// mesmo com porta/rede tudo certo.
	Subprotocols: []string{"plugin.v3.internetdownloadmanager.com"},
}

// Server é o servidor de compatibilidade com a extensão original do IDM.
type Server struct {
	cfg        Config
	launcher   *idm.Launcher
	httpServer *http.Server
	logFile    *os.File

	connCounter int64
}

// New cria um novo servidor de compatibilidade, reaproveitando o mesmo
// idm.Launcher (lançamento do IDM via Wine, proxy reverso, merge de
// HLS/DASH, etc.) que o idm-linux-bridge usa.
func New(cfg Config) (*Server, error) {
	if cfg.Port == 0 {
		cfg.Port = 1001
	}
	launcher, err := idm.NewLauncher(cfg.WinePrefix, cfg.IDMPath, cfg.Verbose)
	if err != nil {
		return nil, err
	}

	s := &Server{cfg: cfg, launcher: launcher}

	if cfg.LogPath != "" {
		f, err := os.OpenFile(cfg.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			log.Printf("[AVISO] não foi possível abrir log de captura %q: %v\n", cfg.LogPath, err)
		} else {
			s.logFile = f
		}
	}

	return s, nil
}

// Start inicia o servidor WebSocket. Se o systemd tiver entregue um
// socket já aberto via socket activation (unit .socket + LISTEN_PID/
// LISTEN_FDS), ele é reaproveitado — nesse caso o processo nunca
// precisa de privilégio para bindar a porta, mesmo sendo uma porta
// privilegiada como 1001 (quem bindou foi o systemd, como root, antes
// de sequer executar este binário). Caso contrário, faz o bind normal
// via net.Listen (uso manual, fora do systemd, ou porta não-privilegiada).
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleWS)

	addr := s.cfg.Host + ":" + strconv.Itoa(s.cfg.Port)
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: mux,
		// Sem ReadTimeout/WriteTimeout fixos — mesma justificativa do
		// server.go do idm-linux-bridge: a conexão fica aberta por toda
		// a sessão de navegação, não é request-response curto.
		ReadHeaderTimeout: 30 * time.Second,
	}

	if listener, ok, err := sdactivation.Listener(); err != nil {
		log.Printf("[AVISO] falha ao usar socket do systemd, tentando bind normal: %v\n", err)
	} else if ok {
		log.Printf("[INFO] idm-compat-shim escutando em ws://%s/ (socket herdado do systemd)\n", addr)
		if err := s.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}

	log.Printf("[INFO] idm-compat-shim escutando em ws://%s/\n", addr)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	connID := atomic.AddInt64(&s.connCounter, 1)
	cid := r.URL.Query().Get("cid")
	rnd := r.URL.Query().Get("rnd")

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ERRO] upgrade WS falhou: %v\n", err)
		return
	}
	defer conn.Close()

	log.Printf("[WS #%d] conexão aberta (cid=%s rnd=%s origin=%s)\n",
		connID, cid, rnd, r.Header.Get("Origin"))
	s.writeLog(map[string]any{
		"event": "connection_open", "conn_id": connID,
		"cid": cid, "rnd": rnd, "origin": r.Header.Get("Origin"),
	})

	if err := s.sendBootstrap(conn, connID); err != nil {
		log.Printf("[WS #%d] [AVISO] falha ao enviar bootstrap: %v\n", connID, err)
		// não retorna -- a conexão continua, só sem as flags de capacidade
		// e a tabela de sites; a extensão vai ficar com funcionalidade
		// reduzida (sem menu de contexto, sem document.js) mas o resto
		// do protocolo (deteccao passiva via type=1/type=2) continua ok.
	}

	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			log.Printf("[WS #%d] conexão encerrada: %v\n", connID, err)
			s.writeLog(map[string]any{"event": "connection_closed", "conn_id": connID, "error": err.Error()})
			return
		}
		if msgType != websocket.BinaryMessage && msgType != websocket.TextMessage {
			continue
		}

		msgs := wsprotocol.ExtractMessages(data)
		for _, msg := range msgs {
			s.handleMessage(connID, msg)
		}
	}
}

// sendBootstrap replica, byte a byte, o handshake real que o
// IDMIntegrator64.exe manda pro navegador logo após a conexão WS abrir:
// uma mensagem type=4 (flags de capacidade -- é o que destrava o menu de
// contexto), uma type=5 (identificação do servidor + todas as regras de
// detecção: extensões suportadas, domínios a ignorar, hook list de
// fetch/XHR) e uma mensagem type=2/conn=18 por site suportado (YouTube,
// Facebook, Vimeo, Instagram, OK.ru, Hydrax, Udemy -- os seletores CSS e
// regex específicos de cada um, incluindo o que decide se o document.js
// é injetado naquele site).
//
// Os templates em internal/bootstrap foram capturados de uma sessão
// real no Windows (IDM v6.43b07 Full + extensão oficial v6.43.1) -- ver
// internal/bootstrap/bootstrap.go para a proveniência completa. Sem
// isso, a extensão nunca injeta document.js e o menu de contexto nunca
// aparece, mesmo com a conexão WS funcionando perfeitamente -- foi essa
// mensagem específica que ficou faltando durante toda a investigação.
func (s *Server) sendBootstrap(conn *websocket.Conn, connID int64) error {
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte(bootstrap.Type4Template)); err != nil {
		return fmt.Errorf("type4: %w", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte(bootstrap.Type5Template)); err != nil {
		return fmt.Errorf("type5: %w", err)
	}
	for i, siteMsg := range bootstrap.SiteTableTemplates {
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte(siteMsg)); err != nil {
			return fmt.Errorf("site table [%d]: %w", i, err)
		}
	}
	log.Printf("[WS #%d] bootstrap enviado (type4 + type5 + %d sites)\n", connID, len(bootstrap.SiteTableTemplates))
	return nil
}

func (s *Server) handleMessage(connID int64, msg wsprotocol.Message) {
	if s.cfg.Verbose {
		log.Printf("[WS #%d] MSG seq=%d conn=%d type=%d (%d campos)\n",
			connID, msg.Seq, msg.Conn, msg.Type, len(msg.Fields))
	}
	s.writeLog(map[string]any{
		"event": "message", "conn_id": connID,
		"seq": msg.Seq, "conn": msg.Conn, "type": msg.Type,
		"fields": msg.Fields,
	})

	job, ok := translateToJob(msg)
	if !ok {
		return // mensagem sem campos suficientes para virar um download (heartbeat, tracking, etc.)
	}

	if !s.cfg.AutoDownload {
		log.Printf("[WS #%d] recurso detectado (auto-download desligado, só logando): %s\n", connID, job.URL)
		return
	}

	if !isExplicitDownloadRequest(msg) {
		log.Printf("[WS #%d] recurso detectado passivamente (sem id=51/cookies -- provável sniffing de rede, não clique do usuário), só logando: %s\n", connID, job.URL)
		return
	}

	resourceType, _ := msg.GetField("4")
	if !s.autoDownloadAllows(resourceType) {
		log.Printf("[WS #%d] recurso detectado mas tipo %q fora da lista permitida (--auto-download-types), só logando: %s\n",
			connID, resourceType, job.URL)
		return
	}

	jobID, err := s.launcher.Launch(job)
	if err != nil {
		log.Printf("[ERRO] falha ao lançar IDM para %s: %v\n", job.URL, err)
		return
	}
	log.Printf("[OK] download enviado ao IDM: %s (job: %s)\n", job.URL, jobID)
}

// isExplicitDownloadRequest distingue um pedido EXPLÍCITO de download
// de uma detecção PASSIVA de mídia (a extensão farejando tráfego de
// rede enquanto o usuário só está navegando/assistindo). Duas vias
// diferentes de pedido explícito, cobrindo os dois mecanismos que a
// extensão usa:
//
//  1. Clique em "Baixar com o IDM" no menu de contexto (via
//     browser.contextMenus): id=51 (cookies de sessão) presente e
//     id=11 (cabeçalhos de requisição brutos) ausente. Descoberto
//     comparando 170 mensagens reais com id=6 de uma captura no
//     Windows -- só 1 batia nesse padrão, e era exatamente o clique
//     real capturado. Ver idm_ws_protocol_notes.md para os detalhes.
//
//  2. Download de arquivo real via link normal (ex: clicar num link
//     ".zip"), capturado pela extensão via webRequest -- ESSE caminho
//     também manda id=11 (com o Cookie: embutido dentro do texto bruto
//     dos headers, não em id=51 separado), então o critério 1 sozinho
//     não reconhece esse caso -- foi exatamente esse o buraco que
//     causou "downloads de arquivo comuns não disparam mesmo com
//     auto-download ligado". O sinal usado aqui é o header HTTP padrão
//     Content-Disposition: attachment (id=13, cabeçalhos de resposta)
//     -- é o mecanismo que o PRÓPRIO NAVEGADOR usa pra decidir "isso é
//     pra baixar, não pra exibir". Nunca aparece em segmentos de
//     vídeo/streaming (players nunca mandam esse header), então é um
//     sinal confiável de intenção real de download sem depender de
//     nenhum comportamento específico da extensão.
func isExplicitDownloadRequest(msg wsprotocol.Message) bool {
	_, hasCookies := msg.GetField("51")
	_, hasRequestHeaders := msg.GetField("11")
	if hasCookies && !hasRequestHeaders {
		return true
	}

	if responseHeaders, ok := msg.GetField("13"); ok {
		lower := strings.ToLower(responseHeaders)
		if strings.Contains(lower, "content-disposition:") && strings.Contains(lower, "attachment") {
			return true
		}
	}

	return false
}

// autoDownloadAllows checa se o tipo de recurso (campo id=4: "MP4", "ZIP",
// "EXE", "TS", etc.) está na lista permitida em AutoDownloadTypes. Lista
// vazia = permite qualquer tipo (comportamento antigo). Usado pra
// restringir testes controlados a cenários de baixo risco (ex: só "ZIP",
// evitando disparar downloads de vídeo via HLS/DASH sem querer).
func (s *Server) autoDownloadAllows(resourceType string) bool {
	if len(s.cfg.AutoDownloadTypes) == 0 {
		return true
	}
	for _, allowed := range s.cfg.AutoDownloadTypes {
		if strings.EqualFold(allowed, resourceType) {
			return true
		}
	}
	return false
}

// translateToJob tenta construir um idm.DownloadJob a partir dos campos
// confirmados de uma mensagem MSG#...; Retorna ok=false se a mensagem não
// tiver campos mínimos suficientes (URL) — cobre heartbeats (conn=100),
// blobs de tracking, mensagens de página/cabeçalho sem recurso associado.
//
// Campos usados (ver tabela em idm_ws_protocol_notes.md):
//
//	id=6   URL do recurso (mídia ou arquivo)
//	id=7   URL da página / referrer
//	id=4   tipo do recurso (MP4, MP3, TS, ZIP, EXE, ...)
//	id=8   tamanho em bytes (não usado ainda — DownloadJob não tem campo pra isso)
//	id=11  cabeçalhos de requisição brutos ("Header: valor\r\n...") -- SÓ aparece em detecção passiva
//	id=51  cookies de sessão -- SÓ aparece em pedido explícito (ver isExplicitDownloadRequest)
//	id=54  user-agent (fallback quando id=11 não está presente)
//	id=100 nome de arquivo sugerido
//
// CONHECIDO INCOMPLETO: filtro de falso-positivo (beeps de teste de
// autoplay do YouTube em .../s/search/audio/*.mp3) ainda não implementado
// aqui — replicar antes de habilitar --auto-download em produção.
func translateToJob(msg wsprotocol.Message) (idm.DownloadJob, bool) {
	rawURL, hasURL := msg.GetField("6")
	if !hasURL || rawURL == "" {
		return idm.DownloadJob{}, false
	}
	rawURL = stripIDMURLWrapper(rawURL)

	job := idm.DownloadJob{URL: rawURL}

	if referrer, ok := msg.GetField("7"); ok {
		job.Referrer = referrer
	}
	if filename, ok := msg.GetField("100"); ok {
		job.Filename = filename
	}
	if headersRaw, ok := msg.GetField("11"); ok {
		job.Headers = parseHeaderBlob(headersRaw)
		if ua, ok := job.Headers["user-agent"]; ok {
			job.UserAgent = ua
		}
	}
	if cookies, ok := msg.GetField("51"); ok && cookies != "" {
		if job.Headers == nil {
			job.Headers = make(map[string]string)
		}
		job.Headers["cookie"] = cookies
	}
	if ua, ok := msg.GetField("54"); ok && ua != "" && job.UserAgent == "" {
		job.UserAgent = ua
	}

	// Silent=false: abre a janela do IDM pra confirmar, mesmo comportamento
	// padrão do idm-linux-bridge — trocar depois de decidir a UX do shim.
	job.Silent = false

	return job, true
}

// stripIDMURLWrapper remove um "wrapper scheme" customizado que o IDM às
// vezes usa pra envolver a URL real do recurso (ex: visto em captura real:
// "idmdwnlmfv9://https://site.com/video.mp4" -- o esquema antes de
// "http(s)://" muda entre sessões/tipos de recurso, então não fixamos o
// nome exato, só detectamos o padrão "algumacoisa://http(s)://resto").
//
// Sem isso, "wine IDMan.exe /d idmdwnlmfv9://https://..." falharia --
// o IDM não entende esse scheme customizado, só a URL http(s) real por
// dentro dele.
//
// NÃO confundir com "idmrs://youtube/video/<id>" (visto no campo id=133
// em capturas antigas) -- esse é um identificador de recurso interno do
// IDM, não uma URL envolvida, e não bate com o padrão abaixo (não tem
// "http(s)://" logo depois do "://").
var idmURLWrapperRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://(https?://.+)$`)

func stripIDMURLWrapper(raw string) string {
	if m := idmURLWrapperRe.FindStringSubmatch(raw); m != nil {
		return m[1]
	}
	return raw
}

// parseHeaderBlob converte um bloco "Header: valor\r\nHeader2: valor2"
// (como capturado no campo id=11) em um map[string]string com chaves em
// minúsculo.
func parseHeaderBlob(blob string) map[string]string {
	headers := make(map[string]string)
	for _, line := range strings.Split(blob, "\r\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.IndexByte(line, ':')
		if idx <= 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:idx]))
		val := strings.TrimSpace(line[idx+1:])
		headers[key] = val
	}
	return headers
}

func (s *Server) writeLog(event map[string]any) {
	if s.logFile == nil {
		return
	}
	event["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	b, err := json.Marshal(event)
	if err != nil {
		return
	}
	s.logFile.Write(b)
	s.logFile.Write([]byte("\n"))
}

// Shutdown encerra o servidor e o arquivo de log.
func (s *Server) Shutdown() {
	if s.httpServer != nil {
		s.httpServer.Close()
	}
	if s.logFile != nil {
		s.logFile.Close()
	}
}

// Launcher expõe o launcher interno (para monitorar RestartCh no main.go,
// mesmo padrão do idm-linux-bridge).
func (s *Server) Launcher() *idm.Launcher {
	return s.launcher
}
