package idm

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// Verificar se IDM já está rodando
//
// PROBLEMA
// ─────────
// A implementação anterior usava `pgrep -fi IDMan.exe` e `wineserver -l`.
// No Linux, o processo Wine não aparece como "IDMan.exe" no `ps` — ele
// aparece como "wine-preloader", "wine64", ou com o path completo do .exe
// como argumento. `pgrep -fi IDMan.exe` compara contra o nome do processo
// (argv[0]), não contra os argumentos, então nunca encontra.
// `wineserver -l` lista conexões do wineserver mas não lista processos.
//
// SOLUÇÃO
// ────────
// Usar `ps aux` e procurar por argumentos que contenham "idman" (case-insensitive).
// Como fallback adicional, checar a porta TCP que o IDM usa para IPC (se aplicável).
// A abordagem com `ps` é portável e funciona em todas as distros sem deps extras.
// ─────────────────────────────────────────────────────────────────────────────

// probeIDMProcessDisplay descobre qual servidor gráfico a instância Wine/IDM
// em execução está usando, lendo /proc/<pid>/environ dos processos wine que
// carregam IDMan.exe como argumento.
//
// Retorna DisplayUnknown se não encontrar nenhum processo IDM ativo.
func probeIDMProcessDisplay(idmExePath string) (DisplayServer, string, string) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return DisplayUnknown, "", ""
	}

	idmLower := strings.ToLower(filepath.Base(idmExePath))

	for _, entry := range entries {
		if !entry.IsDir() { continue }
		pid := entry.Name()
		allDigit := true
		for _, c := range pid { if c < '0' || c > '9' { allDigit = false; break } }
		if !allDigit { continue }

		// Verificar se o cmdline contém IDMan.exe
		cmdlineData, err := os.ReadFile(filepath.Join("/proc", pid, "cmdline"))
		if err != nil { continue }
		cmdline := strings.ToLower(string(cmdlineData))
		if !strings.Contains(cmdline, "wine") && !strings.Contains(cmdline, ".exe") {
			continue
		}
		if !strings.Contains(cmdline, idmLower) &&
			!strings.Contains(cmdline, "idman") {
			continue
		}

		// Encontrou processo IDM/Wine — ler seu ambiente
		envMap := readProcEnviron(pid)
		if envMap == nil { continue }

		wDisp := envMap["WAYLAND_DISPLAY"]
		xDisp := envMap["DISPLAY"]
		sType := strings.ToLower(envMap["XDG_SESSION_TYPE"])

		if sType == "wayland" || wDisp != "" {
			if wDisp == "" { wDisp = "wayland-0" }
			if xDisp == "" { xDisp = ":0" }
			log.Printf("[IDM] Processo wine/IDM (pid %s) rodando em Wayland (%s, xdisplay=%s)\n",
				pid, wDisp, xDisp)
			return DisplayWayland, wDisp, xDisp
		}
		if xDisp != "" {
			log.Printf("[IDM] Processo wine/IDM (pid %s) rodando em X11 (%s)\n", pid, xDisp)
			return DisplayX11, xDisp, xDisp
		}
	}
	return DisplayUnknown, "", ""
}

// isIDMRunning verifica se já existe um processo IDMan.exe rodando no Wine.
//
// Estratégias em ordem de confiabilidade:
//  1. ps aux — procura por "idman" nos argumentos de processos wine/wine64
//  2. pgrep com busca completa de linha de comando (-a flag)
//  3. Checar via /proc diretamente (Linux only, mais confiável)
//
// selfProcessName evita falso positivo com a própria linha de processo do
// bridge/shim: como o binário recebe --idm-path=.../IDMan.exe como
// argumento, "ps aux"/pgrep/procfs mostram "idman" na PRÓPRIA linha de
// comando do launcher, não só na do Wine. Antes isso era hardcoded como
// "idm-bridge"; generalizado para o nome real do binário em execução
// (via filepath.Base(os.Args[0])) para funcionar em qualquer consumidor
// deste pacote (idm-linux-bridge, idm-compat-shim, etc.) sem edição manual.
func isIDMRunning() bool {
	selfProcessName := strings.ToLower(filepath.Base(os.Args[0]))

	// ── Estratégia 1: ps aux grep ────────────────────────────────────────
	// ps aux mostra linha de comando completa incluindo argumentos do wine,
	// permitindo encontrar "wine ... IDMan.exe" ou "wine64 ... idman.exe"
	cmd := exec.Command("ps", "aux")
	out, err := cmd.Output()
	if err == nil {
		lower := strings.ToLower(string(out))
		lines := strings.Split(lower, "\n")
		for _, line := range lines {
			// Procurar por linhas que contenham wine E idman
			// Evitar falso positivo com "grep idman" (o próprio processo de busca)
			// e com a própria linha de comando deste binário (selfProcessName).
			if strings.Contains(line, "idman") &&
				(strings.Contains(line, "wine") || strings.Contains(line, ".exe")) &&
				!strings.Contains(line, "grep") &&
				(selfProcessName == "" || !strings.Contains(line, selfProcessName)) {
				return true
			}
		}
	}

	// ── Estratégia 2: pgrep -af (busca em linha de comando completa) ─────
	// -a: mostra linha de comando completa (não só nome)
	// -f: match contra linha de comando completa (inclui argumentos)
	// -i: case-insensitive
	pgrepCmds := [][]string{
		{"pgrep", "-af", "-i", "IDMan"},      // busca "IDMan" em toda linha de cmd
		{"pgrep", "-af", "-i", "idman.exe"},  // mais específico
	}
	for _, args := range pgrepCmds {
		cmd := exec.Command(args[0], args[1:]...)
		if out, err := cmd.Output(); err == nil {
			result := strings.ToLower(strings.TrimSpace(string(out)))
			if result != "" &&
				strings.Contains(result, "idman") &&
				(selfProcessName == "" || !strings.Contains(result, selfProcessName)) {
				return true
			}
		}
	}

	// ── Estratégia 3: /proc filesystem (Linux) ───────────────────────────
	// Ler /proc/*/cmdline para encontrar processos com "IDMan" nos argumentos.
	// Mais confiável que ps em ambientes com /proc não-padrão.
	if entries, err := os.ReadDir("/proc"); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			// Apenas entradas numéricas (PIDs)
			name := entry.Name()
			isNum := true
			for _, c := range name {
				if c < '0' || c > '9' {
					isNum = false
					break
				}
			}
			if !isNum {
				continue
			}

			cmdlineBytes, err := os.ReadFile(filepath.Join("/proc", name, "cmdline"))
			if err != nil {
				continue
			}
			// cmdline tem args separados por \0
			cmdline := strings.ToLower(strings.ReplaceAll(string(cmdlineBytes), "\x00", " "))
			if strings.Contains(cmdline, "idman") &&
				(selfProcessName == "" || !strings.Contains(cmdline, selfProcessName)) {
				return true
			}
		}
	}

	return false
}
