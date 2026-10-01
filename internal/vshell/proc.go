package vshell

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func procCmds() []Cmd {
	return []Cmd{
		{
			Name: "ps",
			Man: `ps — снимок процессов
Синтаксис: ps | ps aux
ps aux — все процессы с колонками:
USER PID %CPU %MEM VSZ RSS STAT START TIME COMMAND`,
			Run: func(c *Ctx) int {
				s := c.Shell
				_, _, operands, err := parseArgs("", c.Args[1:], nil)
				if err != nil {
					return errPlain(c, err.Error())
				}
				all := len(operands) > 0 && operands[0] == "aux" || len(c.Args) > 1 && c.Args[1] == "aux"
				if !all {
					fmt.Fprintln(c.Stdout, "    PID TTY          TIME CMD")
					fmt.Fprintf(c.Stdout, "   %d pts/0    00:00:00 bash\n", s.SelfPID())
					fmt.Fprintf(c.Stdout, "   %d pts/0    00:00:00 ps\n", s.SelfPID()+1)
					return 0
				}
				fmt.Fprintln(c.Stdout, "USER       PID %CPU %MEM    VSZ   RSS STAT START   TIME COMMAND")
				procs := append([]*Process{}, s.Procs...)
				sort.SliceStable(procs, func(i, j int) bool { return procs[i].PID < procs[j].PID })
				for _, p := range procs {
					fmt.Fprintf(c.Stdout, "%-8s %5d %4.1f %4.1f %6d %5d %-4s %7s %5s %s\n",
						p.User, p.PID, p.CPU, p.Mem, p.PID*350, int(p.Mem*40), p.State, "20:15", p.Time, p.Cmd)
				}
				fmt.Fprintf(c.Stdout, "%-8s %5d %4.1f %4.1f %6d %5d %-4s %7s %5s %s\n",
					s.User, s.SelfPID(), 0.0, 0.3, 8500, 3400, "S", "20:10", "00:00", "-bash")
				fmt.Fprintf(c.Stdout, "%-8s %5d %4.1f %4.1f %6d %5d %-4s %7s %5s %s\n",
					s.User, s.SelfPID()+1, 0.0, 0.1, 4200, 1800, "R", "20:15", "00:00", "ps aux")
				return 0
			},
		},
		{
			Name: "kill",
			Man: `kill — послать сигнал процессу
Синтаксис: kill [-9] PID...
  -9  SIGKILL — убить принудительно
kill 1234 — вежливое завершение (SIGTERM)
Найти PID: ps aux | grep имя`,
			Run: func(c *Ctx) int {
				s := c.Shell
				bools, _, operands, err := parseArgs("9l", c.Args[1:], map[string]byte{})
				if err != nil {
					return errPlain(c, err.Error())
				}
				if bools['l'] {
					fmt.Fprintln(c.Stdout, "HUP INT QUIT KILL TERM STOP CONT ...")
					return 0
				}
				if len(operands) == 0 {
					return errPlain(c, "not enough arguments")
				}
				code := 0
				for _, a := range operands {
					pid, perr := strconv.Atoi(strings.TrimPrefix(a, "-"))
					if perr != nil {
						fmt.Fprintf(c.Stderr, "kill: %s: arguments must be process or job IDs\n", a)
						code = 1
						continue
					}
					found := false
					for i, p := range s.Procs {
						if p.PID == pid {
							s.Procs = append(s.Procs[:i], s.Procs[i+1:]...)
							found = true
							break
						}
					}
					if !found {
						if pid == s.SelfPID() {
							fmt.Fprintf(c.Stderr, "kill: (%d) - Operation not permitted\n", pid)
						} else {
							fmt.Fprintf(c.Stderr, "kill: (%d) - No such process\n", pid)
						}
						code = 1
					}
				}
				return code
			},
		},
		{
			Name: "top",
			Man: `top — процессы в реальном времени
Синтаксис: top
В тренажёре показывает один статичный снимок, 
отсортированный по %CPU.`,
			Run: func(c *Ctx) int {
				s := c.Shell
				fmt.Fprint(c.Stdout, "top - 20:15:42 up  3:12,  1 user,  load average: 0.42, 0.35, 0.31\n")
				fmt.Fprint(c.Stdout, "Tasks:  87 total,   1 running,  86 sleeping,   0 stopped,   0 zombie\n")
				fmt.Fprint(c.Stdout, "%Cpu(s):  3.2 us,  1.1 sy,  0.0 ni, 95.4 id\n")
				fmt.Fprint(c.Stdout, "MiB Mem :   3809.9 total,   2047.2 free,   1204.5 used,   558.2 buff/cache\n\n")
				fmt.Fprintln(c.Stdout, "    PID USER      PR  NI    VIRT    RES  %CPU  %MEM     TIME+ COMMAND")
				procs := append([]*Process{}, s.Procs...)
				sort.SliceStable(procs, func(i, j int) bool { return procs[i].CPU > procs[j].CPU })
				rank := 20
				for _, p := range procs {
					fmt.Fprintf(c.Stdout, "  %5d %-8s %3d   0  %5.1fm %4.0fm %5.1f %5.1f %8s %s\n",
						p.PID, p.User, rank, float64(p.PID)/200, p.Mem*0.4, p.CPU, p.Mem, p.Time, p.Cmd)
					rank--
				}
				return 0
			},
		},
		{
			Name: "jobs",
			Man:  `jobs — фоновые задания текущего shell`,
			Run:  func(c *Ctx) int { return 0 },
		},
		{
			Name: "systemctl",
			Man: `systemctl — управление службами systemd
Синтаксис: systemctl КОМАНДА [юнит]
Команды: status, start, stop, restart, enable, disable, is-active
Юниты: nginx.service, ssh.service, cron.service...
sudo требуется для start/stop/restart.`,
			Run: systemctlRun,
		},
		{
			Name: "journalctl",
			Man: `journalctl — логи systemd
Синтаксис: journalctl -u ЮНИТ [-n N]
  -u  логи конкретной службы
  -n  последние N строк
journalctl -u nginx.service -n 20`,
			Run: func(c *Ctx) int {
				s := c.Shell
				_, vals, _, err := parseArgs("u:n:", c.Args[1:], map[string]byte{"unit": 'u', "lines": 'n'})
				if err != nil {
					return errPlain(c, err.Error())
				}
				unitName := vals['u']
				if unitName == "" {
					fmt.Fprint(c.Stdout, "-- Journal begins at Sat 2026-09-26 10:02:11 UTC. --\n")
					fmt.Fprint(c.Stdout, "Sep 26 10:02:11 trenbox systemd[1]: Started Daily apt download.\n")
					return 0
				}
				unit, ok := s.Units[unitName]
				if !ok {
					fmt.Fprintf(c.Stderr, "journalctl: no entries for unit: %s\n", unitName)
					return 1
				}
				limit := len(unit.Logs)
				if v, ok := vals['n']; ok {
					n, perr := strconv.Atoi(v)
					if perr != nil || n < 0 {
						return errPlain(c, "invalid number of lines: '"+v+"'")
					}
					if n < limit {
						limit = n
					}
				}
				for _, l := range unit.Logs[len(unit.Logs)-limit:] {
					fmt.Fprintln(c.Stdout, l)
				}
				return 0
			},
		},
	}
}

// SelfPID returns a stable fake PID for the shell itself.
func (s *Shell) SelfPID() int { return 3141 }

func systemctlRun(c *Ctx) int {
	s := c.Shell
	if len(c.Args) < 2 {
		return errPlain(c, "try: systemctl status nginx.service")
	}
	action := c.Args[1]
	unitName := ""
	if len(c.Args) > 2 {
		unitName = c.Args[2]
	}
	if action == "list-units" || action == "list" {
		fmt.Fprintln(c.Stdout, "UNIT                    LOAD   ACTIVE SUB     DESCRIPTION")
		names := make([]string, 0, len(s.Units))
		for n := range s.Units {
			names = append(names, n)
		}
		sortStrings(names)
		for _, n := range names {
			u := s.Units[n]
			sub := map[string]string{"active": "running", "inactive": "dead", "failed": "failed"}[u.Active]
			fmt.Fprintf(c.Stdout, "%-24s loaded %s %s %s\n", n, u.Active, sub, u.Desc)
		}
		return 0
	}
	if unitName == "" {
		return errPlain(c, "unit name required")
	}
	if !strings.Contains(unitName, ".") {
		unitName += ".service"
	}
	unit, ok := s.Units[unitName]
	if !ok {
		// is-active reports a missing unit as inactive (exit 3), while
		// status and the mutating actions report the error
		if action == "is-active" {
			fmt.Fprintln(c.Stdout, "inactive")
			return 3
		}
		fmt.Fprintf(c.Stderr, "Unit %s could not be found.\n", unitName)
		return 1
	}
	switch action {
	case "status":
		enabled := map[bool]string{true: "enabled", false: "disabled"}[unit.Enabled]
		fmt.Fprintf(c.Stdout, "● %s - %s\n", unitName, unit.Desc)
		fmt.Fprintf(c.Stdout, "     Loaded: loaded (/lib/systemd/system/%s; %s)\n", unitName, enabled)
		fmt.Fprintf(c.Stdout, "     Active: %s\n", statusLine(unit.Active))
		fmt.Fprintln(c.Stdout, "   Main PID: 1080 (nginx)")
		if len(unit.Logs) > 0 {
			fmt.Fprintln(c.Stdout)
			for _, l := range unit.Logs {
				fmt.Fprintln(c.Stdout, l)
			}
		}
		return 0
	case "is-active":
		fmt.Fprintln(c.Stdout, unit.Active)
		if unit.Active == "active" {
			return 0
		}
		return 3
	case "start", "stop", "restart":
		if s.User != "root" {
			fmt.Fprintf(c.Stderr, "Failed to %s %s.service: Access denied\n", action, unitName)
			return 1
		}
		switch action {
		case "start":
			unit.Active = "active"
		case "stop":
			unit.Active = "inactive"
		case "restart":
			unit.Active = "active"
		}
		return 0
	case "enable":
		if s.User != "root" {
			fmt.Fprintln(c.Stderr, "Failed to enable unit: Access denied")
			return 1
		}
		unit.Enabled = true
		fmt.Fprintf(c.Stdout, "Created symlink /etc/systemd/system/multi-user.target.wants/%s → /lib/systemd/system/%s\n", unitName, unitName)
		return 0
	case "disable":
		if s.User != "root" {
			fmt.Fprintln(c.Stderr, "Failed to disable unit: Access denied")
			return 1
		}
		unit.Enabled = false
		fmt.Fprintf(c.Stdout, "Removed %s\n", unitName)
		return 0
	}
	return errPlain(c, "unknown action '"+action+"'")
}

func statusLine(active string) string {
	switch active {
	case "active":
		return "active (running) since Tue 2026-09-29 09:00:12 UTC; 1 day ago"
	case "inactive":
		return "inactive (dead)"
	default:
		return "failed (Result: exit-code)"
	}
}
