// Package sdactivation implementa o subconjunto mínimo do protocolo de
// "socket activation" do systemd necessário para o shim: checar se o
// systemd já entregou um socket aberto via variáveis de ambiente
// (LISTEN_PID / LISTEN_FDS) e, se sim, devolver um net.Listener pronto
// pra uso, sem o processo nunca precisar de privilégio para bindar a
// porta (quem bindou foi o systemd, rodando como root via a unit
// .socket, antes mesmo de executar o binário do shim).
//
// Protocolo (https://www.freedesktop.org/software/systemd/man/latest/sd_listen_fds.html):
//   - LISTEN_PID: deve ser igual ao PID do processo atual (systemd seta
//     isso especificamente pra esse processo; se não bater, os fds não
//     são "nossos" — situação rara, mas checada por segurança).
//   - LISTEN_FDS: quantidade de file descriptors entregues, começando
//     sempre no fd 3 (0,1,2 são stdin/stdout/stderr).
package sdactivation

import (
	"fmt"
	"net"
	"os"
	"strconv"
)

// listenFDsStart é o primeiro file descriptor usado pelo systemd para
// socket activation — fixo pelo protocolo, sempre 3.
const listenFDsStart = 3

// Listener retorna um net.Listener herdado do systemd via socket
// activation, se as variáveis de ambiente indicarem que há um disponível
// para este processo. O segundo valor de retorno é false se não houver
// nenhum socket herdado (caso em que o chamador deve fazer o bind normal
// via net.Listen).
func Listener() (net.Listener, bool, error) {
	pidStr := os.Getenv("LISTEN_PID")
	fdsStr := os.Getenv("LISTEN_FDS")
	if pidStr == "" || fdsStr == "" {
		return nil, false, nil
	}

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return nil, false, fmt.Errorf("LISTEN_PID inválido: %w", err)
	}
	if pid != os.Getpid() {
		// Os fds foram destinados a outro processo (ex: uma cadeia de
		// exec intermediária) — não são nossos, não usa.
		return nil, false, nil
	}

	nfds, err := strconv.Atoi(fdsStr)
	if err != nil {
		return nil, false, fmt.Errorf("LISTEN_FDS inválido: %w", err)
	}
	if nfds < 1 {
		return nil, false, nil
	}

	// Usamos sempre o primeiro fd entregue (fd 3). O shim só declara um
	// ListenStream= na unit .socket, então nfds deveria ser sempre 1;
	// se for mais, os demais são ignorados por ora.
	fd := uintptr(listenFDsStart)
	file := os.NewFile(fd, "systemd-socket-activation")
	if file == nil {
		return nil, false, fmt.Errorf("não foi possível abrir fd %d entregue pelo systemd", fd)
	}

	listener, err := net.FileListener(file)
	if err != nil {
		file.Close()
		return nil, false, fmt.Errorf("net.FileListener falhou para fd %d: %w", fd, err)
	}

	return listener, true, nil
}
