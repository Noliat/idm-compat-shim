// Package nativehost implementa o lado stdio do protocolo Chrome/Firefox
// Native Messaging (https://developer.chrome.com/docs/extensions/develop/concepts/native-messaging),
// usado pela extensão original do IDM só para o navegador detectar que
// "o IDM está instalado" — o tráfego de verdade (detecção de mídia,
// downloads) acontece pelo servidor WebSocket em internal/server, não
// por aqui.
//
// STATUS: NÃO CONFIRMADO POR CAPTURA. Sabemos pelas strings do IDMan.exe
// que ele se registra como native messaging host ("com.tonec.idm") nos
// navegadores, mas não capturamos ainda o conteúdo real da troca
// stdin/stdout entre a extensão e o IDMMsgHost.exe original — só
// inferimos a existência do mecanismo. A implementação abaixo é um
// passthrough conservador: lê qualquer mensagem, responde com um "ok"
// genérico. Ajustar assim que tivermos uma captura desse handshake
// (ex: strace/ltrace no IDMMsgHost.exe rodando sob Wine, ou logging no
// lado do Chrome via chrome://net-export com native messaging habilitado).
package nativehost

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
	"os"
)

// Run inicia o loop de leitura/escrita do protocolo native messaging via
// stdin/stdout. Bloqueia até stdin fechar (o Chrome mata o processo
// quando a extensão desconecta ou o navegador fecha).
func Run() {
	for {
		msg, err := readMessage(os.Stdin)
		if err != nil {
			if err != io.EOF {
				log.Printf("[nativehost] erro lendo stdin: %v\n", err)
			}
			return
		}

		log.Printf("[nativehost] mensagem recebida: %s\n", string(msg))

		// Resposta genérica — formato e conteúdo reais ainda não
		// confirmados. "status: ok" é um chute conservador; pode
		// precisar virar algo mais específico (versão do IDM, por
		// exemplo) assim que soubermos o que a extensão realmente espera.
		reply := map[string]any{"status": "ok"}
		if err := writeMessage(os.Stdout, reply); err != nil {
			log.Printf("[nativehost] erro escrevendo stdout: %v\n", err)
			return
		}
	}
}

// readMessage lê uma mensagem no formato do protocolo: 4 bytes de
// tamanho (uint32 little-endian) seguidos de uma string JSON UTF-8.
func readMessage(r io.Reader) ([]byte, error) {
	var length uint32
	if err := binary.Read(r, binary.LittleEndian, &length); err != nil {
		return nil, err
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// writeMessage escreve uma mensagem no mesmo formato (prefixo de
// tamanho + JSON).
func writeMessage(w io.Writer, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(len(b))); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
