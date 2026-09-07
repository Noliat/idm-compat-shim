// Package wsprotocol implementa o parser do protocolo de aplicação usado
// pela extensão original do IDM sobre a conexão WebSocket local
// (extensão <-> IDMIntegrator64), reverso a partir de capturas de tráfego
// reais (ver idm_ws_protocol_notes.md no histórico do projeto).
//
// Formato de cada mensagem, já com a fragmentação WS removida:
//
//	MSG#<seq>#<conn>#<type>#<campos>;
//
// Onde <campos> é uma lista separada por vírgula de itens em um dos dois
// formatos:
//
//   - posicional: um valor cru, sem "id=" explícito (tipicamente os
//     primeiros campos de cada mensagem, cabeçalho fixo por <type>).
//   - length-prefixed: "<id>=<tamanho_em_bytes>:<valor>" — o tamanho
//     declarado garante que vírgulas/dois-pontos DENTRO do valor (URLs,
//     JSON, cabeçalhos HTTP) não quebrem o parsing.
//
// IMPORTANTE — status do mapeamento de campos: nem todo <id> tem
// significado confirmado. Ver a tabela de campos no protocolo notes;
// isso é engenharia reversa em andamento, não uma spec oficial.
package wsprotocol

import (
	"regexp"
	"strconv"
	"strings"
)

// Field é um único campo dentro de uma mensagem MSG#...;
// ID vazio ("") significa campo posicional (sem "id=" explícito).
type Field struct {
	ID    string
	Value string
}

// Message é uma mensagem de aplicação já decodificada.
type Message struct {
	Seq    int
	Conn   int
	Type   int
	Fields []Field
}

// GetField retorna o valor do primeiro campo com o id informado.
// Retorna "" e false se não encontrado.
func (m Message) GetField(id string) (string, bool) {
	for _, f := range m.Fields {
		if f.ID == id {
			return f.Value, true
		}
	}
	return "", false
}

// PositionalFields retorna, em ordem, os valores de todos os campos
// posicionais (sem id explícito) da mensagem.
func (m Message) PositionalFields() []string {
	var out []string
	for _, f := range m.Fields {
		if f.ID == "" {
			out = append(out, f.Value)
		}
	}
	return out
}

var msgRe = regexp.MustCompile(`(?s)^MSG#(\d+)#(\d+)#(\d+)#(.*);\s*$`)
var lenPrefixRe = regexp.MustCompile(`^(\d+)=(\d+):`)

// ParseMessage faz o parse de uma única mensagem "MSG#seq#conn#type#campos;".
// Retorna false se a string não bater com o formato esperado.
func ParseMessage(raw string) (Message, bool) {
	m := msgRe.FindStringSubmatch(raw)
	if m == nil {
		return Message{}, false
	}
	seq, _ := strconv.Atoi(m[1])
	conn, _ := strconv.Atoi(m[2])
	typ, _ := strconv.Atoi(m[3])
	return Message{
		Seq:    seq,
		Conn:   conn,
		Type:   typ,
		Fields: splitFields(m[4]),
	}, true
}

// splitFields separa a lista de campos respeitando o comprimento
// declarado nos campos length-prefixed ("id=len:valor"), para que
// vírgulas/dois-pontos dentro do valor não quebrem o split.
func splitFields(body string) []Field {
	var fields []Field
	i, n := 0, len(body)

	for i < n {
		if body[i] == ',' {
			i++
			continue
		}

		if loc := lenPrefixRe.FindStringSubmatchIndex(body[i:]); loc != nil {
			// loc[2:4] = grupo id, loc[4:6] = grupo tamanho, loc[1] = fim do match ("id=len:")
			idStr := body[i+loc[2] : i+loc[3]]
			lenStr := body[i+loc[4] : i+loc[5]]
			declaredLen, err := strconv.Atoi(lenStr)
			start := i + loc[1]

			if err == nil && start+declaredLen <= n {
				value := body[start : start+declaredLen]
				fields = append(fields, Field{ID: idStr, Value: value})
				i = start + declaredLen
				continue
			}
			// tamanho declarado inconsistente com o resto do buffer:
			// cai no fallback posicional abaixo em vez de travar o parser.
		}

		j := strings.IndexByte(body[i:], ',')
		if j == -1 {
			fields = append(fields, Field{ID: "", Value: body[i:]})
			i = n
		} else {
			fields = append(fields, Field{ID: "", Value: body[i : i+j]})
			i += j
		}
	}

	return fields
}

// ExtractMessages recebe um blob de bytes já reassemblado (mensagens de
// aplicação completas, sem fragmentação WS) e retorna todas as mensagens
// MSG#...; encontradas, em ordem.
func ExtractMessages(data []byte) []Message {
	text := string(data)
	var out []Message

	// Mensagens são terminadas por ';' e a próxima começa com "MSG#".
	// Exigimos que o caractere imediatamente anterior a "MSG#" seja ';'
	// (ou início da string) — mesma heurística do parser de referência em
	// Python, que reduz (sem eliminar 100%) falso positivo de split caso
	// o texto literal "MSG#" apareça dentro do conteúdo de um campo grande
	// (ex: HTML/JS de página capturada inteira em um único campo).
	starts := messageStartIndexes(text)
	for idx, start := range starts {
		end := len(text)
		if idx+1 < len(starts) {
			end = starts[idx+1]
		}
		chunk := strings.TrimSpace(text[start:end])
		if chunk == "" {
			continue
		}
		if msg, ok := ParseMessage(chunk); ok {
			out = append(out, msg)
		}
	}
	return out
}

// messageStartIndexes localiza os índices onde uma nova mensagem "MSG#"
// começa, exigindo que o caractere anterior seja ';' ou que seja o
// início absoluto da string.
func messageStartIndexes(s string) []int {
	var idxs []int
	offset := 0
	for {
		i := strings.Index(s[offset:], "MSG#")
		if i == -1 {
			break
		}
		abs := offset + i
		if abs == 0 || s[abs-1] == ';' {
			idxs = append(idxs, abs)
		}
		offset = abs + len("MSG#")
	}
	return idxs
}
