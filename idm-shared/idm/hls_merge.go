package idm

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ─────────────────────────────────────────────────────────────────────────────
// Generic HLS Merge — baixar TODOS os segmentos de uma media playlist HLS e
// mergeá-los em um único arquivo local, reproduzível offline.
// ─────────────────────────────────────────────────────────────────────────────
//
// PROBLEMA DESCOBERTO: para HLS de sites genéricos (players externos, não
// YouTube), o proxy apenas reescrevia o manifest (rewriteM3U8, roteando cada
// segmento por si mesmo) e servia esse TEXTO ao IDM como se fosse o "arquivo
// baixado". O IDM, rodando via Wine, foi lançado apenas com a URL via `/d`
// (linha de comando) — isso NÃO aciona o motor de download HLS nativo do IDM
// (que normalmente só é ativado pela detecção de vídeo na página/GUI). O
// resultado: o IDM simplesmente salvava o texto do manifest reescrito como um
// arquivo `.m3u8` literal — que só "funciona" se aberto em outro player (ex:
// VLC) enquanto o bridge (proxy local) ainda está rodando, pois os URLs de
// segmento apontam para 127.0.0.1. Não é um download real, offline,
// standalone — exatamente o oposto do que o projeto se propõe a entregar.
//
// SOLUÇÃO: antes de servir o manifest como texto, o proxy agora tenta baixar
// TODOS os segmentos ele mesmo (reaproveitando doProxyFetch, que já aplica
// cookies/referrer/headers do job), descriptografa-os se necessário
// (AES-128, conforme #EXT-X-KEY), e os concatena com ffmpeg em um único
// arquivo .mp4 — servido ao IDM através do mesmo mecanismo já usado pelo
// merge DASH do YouTube (dashMergeAndServe): registra um novo job com
// URL "file://", e o IDM recebe uma URL proxy que serve esse arquivo já
// pronto, completo, e reproduzível sem depender do bridge continuar rodando.
//
// Streams AO VIVO (sem #EXT-X-ENDLIST) não têm um "fim" definido e não podem
// ser baixados por completo dessa forma — nesses casos, o proxy volta ao
// comportamento anterior (servir o manifest reescrito), já que não há uma
// alternativa completa possível para live.

// hlsSegment representa um segmento parseado da media playlist, com seu
// número de sequência (usado para derivar o IV de descriptografia quando o
// manifest não especifica um IV explícito — conforme a especificação HLS).
type hlsSegment struct {
	url string
	seq int
}

// hlsKeyInfo representa a tag #EXT-X-KEY ativa no momento em que um
// segmento aparece no manifest.
type hlsKeyInfo struct {
	method string // "AES-128" (suportado) ou outro (SAMPLE-AES etc — não suportado)
	uri    string
	ivHex  string // pode ser vazio — nesse caso, deriva do media sequence number
}

var (
	reMediaSeq = regexp.MustCompile(`EXT-X-MEDIA-SEQUENCE:(\d+)`)
	reKeyMeth  = regexp.MustCompile(`METHOD=([^,\s]+)`)
	reKeyURI   = regexp.MustCompile(`URI="([^"]+)"`)
	reKeyIV    = regexp.MustCompile(`IV=0[xX]([0-9a-fA-F]+)`)
)

// isHLSVod detecta se a media playlist é VOD (tem fim definido) através da
// presença de #EXT-X-ENDLIST — só streams VOD podem ser baixados por
// completo e mergeados num arquivo único.
func isHLSVod(body string) bool {
	return strings.Contains(body, "#EXT-X-ENDLIST")
}

// parseHLSMediaPlaylist extrai a lista de segmentos (URLs absolutas) e a
// chave de criptografia ativa (se houver) de uma media playlist HLS.
// Parser simplificado propositalmente — não trata master playlists (a
// extensão já resolve a qualidade escolhida e envia a URL da media playlist
// específica), nem tags irrelevantes para o objetivo de baixar+concatenar.
func parseHLSMediaPlaylist(body, baseURL string) ([]hlsSegment, *hlsKeyInfo) {
	lines := strings.Split(body, "\n")
	var segments []hlsSegment
	var key *hlsKeyInfo
	seq := 0

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		if m := reMediaSeq.FindStringSubmatch(line); m != nil {
			seq, _ = strconv.Atoi(m[1])
			continue
		}

		if strings.HasPrefix(line, "#EXT-X-KEY") {
			method := ""
			if m := reKeyMeth.FindStringSubmatch(line); m != nil {
				method = strings.ToUpper(strings.TrimSpace(m[1]))
			}
			if method == "" || method == "NONE" {
				key = nil
				continue
			}
			uri := ""
			if m := reKeyURI.FindStringSubmatch(line); m != nil {
				uri = m[1]
			}
			ivHex := ""
			if m := reKeyIV.FindStringSubmatch(line); m != nil {
				ivHex = m[1]
			}
			key = &hlsKeyInfo{method: method, uri: resolveSegmentURL(uri, baseURL), ivHex: ivHex}
			continue
		}

		if strings.HasPrefix(line, "#") {
			continue
		}

		// Linha de URL de segmento
		abs := resolveSegmentURL(line, baseURL)
		if abs != "" {
			segments = append(segments, hlsSegment{url: abs, seq: seq})
			seq++
		}
	}
	return segments, key
}

// deriveIV retorna o IV (vetor de inicialização) para descriptografar um
// segmento AES-128-CBC. Se o manifest especifica IV explícito (mesmo IV para
// todos os segmentos sob essa chave), usa esse valor. Caso contrário, por
// especificação HLS (RFC 8216 §5.2), o IV é o media sequence number do
// segmento, como inteiro big-endian de 128 bits.
func deriveIV(ivHex string, seq int) []byte {
	if ivHex != "" {
		clean := strings.TrimPrefix(strings.TrimPrefix(ivHex, "0x"), "0X")
		if len(clean)%2 != 0 {
			clean = "0" + clean
		}
		if b, err := hex.DecodeString(clean); err == nil && len(b) == 16 {
			return b
		}
		// IV malformado — cair no fallback de sequência abaixo
	}
	iv := make([]byte, 16)
	v := seq
	for i := 15; i >= 0 && v > 0; i-- {
		iv[i] = byte(v & 0xFF)
		v >>= 8
	}
	return iv
}

// decryptAES128CBC descriptografa dados com AES-128 no modo CBC (o método
// padrão de criptografia HLS), removendo o padding PKCS7 ao final.
func decryptAES128CBC(data, key, iv []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}
	if len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("tamanho de dados inválido para AES-CBC: %d bytes (não múltiplo de %d)", len(data), aes.BlockSize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("criar cipher AES: %w", err)
	}

	out := make([]byte, len(data))
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(out, data)

	// Remover padding PKCS7
	padLen := int(out[len(out)-1])
	if padLen > 0 && padLen <= aes.BlockSize && padLen <= len(out) {
		out = out[:len(out)-padLen]
	}
	return out, nil
}

// fetchHLSKey busca os bytes da chave AES-128 (exatamente 16 bytes) usando o
// mesmo mecanismo de cookies/referrer/headers do job — a chave costuma estar
// atrás da mesma proteção de acesso que os segmentos.
func (l *Launcher) fetchHLSKey(keyURL string, job DownloadJob) ([]byte, error) {
	resp, err := l.doProxyFetch("GET", keyURL, job, true, "")
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return nil, fmt.Errorf("ler corpo: %w", err)
	}
	if len(data) != 16 {
		return nil, fmt.Errorf("tamanho de chave inesperado: %d bytes (esperado 16)", len(data))
	}
	return data, nil
}

// downloadAndDecryptSegment baixa um segmento via doProxyFetch (aplicando
// cookies/referrer/headers do job) e, se uma chave foi fornecida,
// descriptografa o conteúdo antes de salvar em disco.
func (l *Launcher) downloadAndDecryptSegment(segURL, destPath string, job DownloadJob, key, iv []byte) error {
	resp, err := l.doProxyFetch("GET", segURL, job, true, "")
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("ler corpo: %w", err)
	}

	if key != nil {
		data, err = decryptAES128CBC(data, key, iv)
		if err != nil {
			return fmt.Errorf("descriptografar: %w", err)
		}
	}

	return os.WriteFile(destPath, data, 0644)
}

// genericHLSMergeAndServe baixa todos os segmentos de uma media playlist HLS
// VOD, descriptografando-os se necessário, e os concatena em um único
// arquivo .mp4 local — retornando a URL proxy (http://127.0.0.1:.../token)
// que serve esse arquivo já pronto ao IDM. Retorna "" se o merge não pôde
// ser realizado (stream ao vivo, ffmpeg ausente, erro de rede, etc.) — nesse
// caso o chamador deve cair de volta ao comportamento anterior (servir o
// manifest reescrito como texto).
func (l *Launcher) genericHLSMergeAndServe(body, manifestURL string, job DownloadJob) string {
	if !isHLSVod(body) {
		log.Printf("[HLS-MERGE] Stream ao vivo (sem EXT-X-ENDLIST) — merge completo não é possível, servindo manifest reescrito\n")
		return ""
	}

	segments, keyInfo := parseHLSMediaPlaylist(body, manifestURL)
	if len(segments) == 0 {
		return ""
	}

	// Limite de segurança: um manifest VOD absurdamente longo (ou mal
	// detectado) não deve travar o proxy indefinidamente baixando segmentos.
	const maxSegments = 5000
	if len(segments) > maxSegments {
		log.Printf("[HLS-MERGE] %d segmentos excede o limite de %d — pulando merge\n", len(segments), maxSegments)
		return ""
	}

	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		log.Printf("[HLS-MERGE] ffmpeg não encontrado — instale: apt install ffmpeg\n")
		return ""
	}

	if keyInfo != nil && keyInfo.method != "AES-128" {
		log.Printf("[HLS-MERGE] Método de criptografia não suportado (%s) — pulando merge\n", keyInfo.method)
		return ""
	}

	tmpDir, err := os.MkdirTemp("", "idm-hls-*")
	if err != nil {
		return ""
	}

	var keyBytes []byte
	if keyInfo != nil {
		keyBytes, err = l.fetchHLSKey(keyInfo.uri, job)
		if err != nil {
			log.Printf("[HLS-MERGE] Falha ao buscar chave AES-128: %v\n", err)
			os.RemoveAll(tmpDir)
			return ""
		}
	}

	log.Printf("[HLS-MERGE] Baixando %d segmentos...\n", len(segments))

	segFiles := make([]string, len(segments))
	const maxConcurrent = 6 // paralelismo limitado — evita rate-limit do CDN
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	errCh := make(chan error, len(segments))

	for i, seg := range segments {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, s hlsSegment) {
			defer wg.Done()
			defer func() { <-sem }()

			destPath := filepath.Join(tmpDir, fmt.Sprintf("seg_%06d.ts", idx))
			var iv []byte
			if keyInfo != nil {
				iv = deriveIV(keyInfo.ivHex, s.seq)
			}
			if dlErr := l.downloadAndDecryptSegment(s.url, destPath, job, keyBytes, iv); dlErr != nil {
				errCh <- fmt.Errorf("segmento %d: %w", idx, dlErr)
				return
			}
			segFiles[idx] = destPath
		}(i, seg)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	// Timeout generoso — streams longos (2h+) podem ter muitos segmentos
	// e conexões lentas legitimamente demoram.
	select {
	case <-done:
	case <-time.After(90 * time.Minute):
		log.Printf("[HLS-MERGE] Timeout aguardando download dos segmentos\n")
		os.RemoveAll(tmpDir)
		return ""
	}

	close(errCh)
	if firstErr, ok := <-errCh; ok {
		log.Printf("[HLS-MERGE] Falha ao baixar segmento: %v\n", firstErr)
		os.RemoveAll(tmpDir)
		return ""
	}

	// Concatenar via ffmpeg concat demuxer — mais robusto que concatenação
	// binária crua: lida corretamente com continuidade/timestamps entre
	// segmentos vindos de arquivos MPEG-TS separados.
	listPath := filepath.Join(tmpDir, "concat_list.txt")
	listFile, err := os.Create(listPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		return ""
	}
	for _, f := range segFiles {
		fmt.Fprintf(listFile, "file '%s'\n", f)
	}
	listFile.Close()

	outFile := filepath.Join(tmpDir, "merged.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	runConcat := func(withBsf bool) error {
		args := []string{"-y", "-f", "concat", "-safe", "0", "-i", listPath, "-c", "copy"}
		if withBsf {
			// Necessário ao remuxar TS→MP4 quando o áudio é AAC (formato
			// ADTS usado em TS não é diretamente compatível com o container
			// MP4, que espera o formato ASC).
			args = append(args, "-bsf:a", "aac_adtstoasc")
		}
		args = append(args, "-movflags", "+faststart", outFile)
		cmd := exec.CommandContext(ctx, ffmpeg, args...)
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	if err := runConcat(true); err != nil {
		log.Printf("[HLS-MERGE] ffmpeg concat (com bsf AAC) falhou: %v — tentando sem bsf\n", err)
		if err2 := runConcat(false); err2 != nil {
			log.Printf("[HLS-MERGE] ffmpeg concat também falhou sem bsf: %v\n", err2)
			os.RemoveAll(tmpDir)
			return ""
		}
	}

	for _, f := range segFiles {
		os.Remove(f)
	}
	os.Remove(listPath)

	token := "hls-" + uuid.New().String()[:8]
	filename := job.Filename
	if filename == "" {
		filename = "video.mp4"
	}
	if !strings.HasSuffix(strings.ToLower(filename), ".mp4") {
		filename = strings.TrimSuffix(filename, filepath.Ext(filename)) + ".mp4"
	}

	mergeJob := DownloadJob{
		URL:      "file://" + outFile,
		Filename: filename,
		Silent:   job.Silent,
	}
	l.mu.Lock()
	l.jobs[token] = &jobEntry{job: mergeJob, createdAt: time.Now()}
	l.mu.Unlock()

	go func() {
		time.Sleep(2 * time.Hour)
		os.RemoveAll(tmpDir)
		l.mu.Lock()
		delete(l.jobs, token)
		l.mu.Unlock()
	}()

	proxyURL := fmt.Sprintf("http://127.0.0.1:%d/%s", l.proxyPort, token)
	log.Printf("[HLS-MERGE] ✓ Merge concluído (%d segmentos) → %s\n", len(segments), proxyURL)
	return proxyURL
}
