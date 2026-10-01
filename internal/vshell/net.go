package vshell

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
)

func netCmds() []Cmd {
	return []Cmd{
		{
			Name: "ping",
			Man: `ping — проверка доступности хоста
Синтаксис: ping [-c N] хост
  -c N  остановиться после N пакетов
Пример: ping -c 3 example.com`,
			Run: pingRun,
		},
		{
			Name: "curl",
			Man: `curl — HTTP-запрос
Синтаксис: curl [-i] [-o файл] URL
  -i     показать заголовки ответа
  -o ф   сохранить ответ в файл
В тренажёре отвечает сервер из сценария урока.`,
			Run: curlRun,
		},
		{
			Name: "ss",
			Man: `ss — статистика сокетов
Синтаксис: ss [-tulnp]
  -t  TCP
  -u  UDP
  -l  слушающие
  -p  какой процесс слушает
  -n  числовые порты
ss -tulpn — кто слушает порты.`,
			Run: ssRun,
		},
	}
}

func (s *Shell) hostKnown(host string) bool {
	if host == "localhost" || host == s.Env["HOSTNAME"] {
		return true
	}
	_, ok := s.Web[normalizeURL(host)]
	return ok
}

func normalizeURL(u string) string {
	u = strings.TrimSpace(u)
	for _, p := range []string{"https://", "http://"} {
		if strings.HasPrefix(u, p) {
			return u
		}
	}
	return "http://" + u
}

func pingRun(c *Ctx) int {
	s := c.Shell
	_, vals, operands, err := parseArgs("c:", c.Args[1:], map[string]byte{"count": 'c'})
	if err != nil {
		return errPlain(c, err.Error())
	}
	if len(operands) == 0 {
		return errPlain(c, "usage: ping [-c count] host")
	}
	host := operands[0]
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	count := 4
	if v, ok := vals['c']; ok {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 1 {
			return errPlain(c, "invalid count of packets to transmit: '"+v+"'")
		}
		count = n
	}
	if !s.hostKnown(host) {
		fmt.Fprintf(c.Stderr, "ping: %s: Name or service not known\n", host)
		return 2
	}
	fmt.Fprintf(c.Stdout, "PING %s (93.184.216.34) 56(84) bytes of data.\n", host)
	total := 0.0
	for i := 1; i <= count; i++ {
		ms := 10 + rand.Float64()*20
		total += ms
		fmt.Fprintf(c.Stdout, "64 bytes from %s (93.184.216.34): icmp_seq=%d ttl=56 time=%.1f ms\n", host, i, ms)
	}
	fmt.Fprintf(c.Stdout, "\n--- %s ping statistics ---\n", host)
	fmt.Fprintf(c.Stdout, "%d packets transmitted, %d received, 0%% packet loss, time %dms\n",
		count, count, count*20)
	fmt.Fprintf(c.Stdout, "rtt min/avg/max = %.1f/%.1f/%.1f ms\n", 10.0, total/float64(count), 30.0)
	return 0
}

func curlRun(c *Ctx) int {
	s := c.Shell
	bools, vals, operands, err := parseArgs("io:sL", c.Args[1:], map[string]byte{
		"include": 'i', "output": 'o', "location": 'L',
	})
	if err != nil {
		return errPlain(c, err.Error())
	}
	if len(operands) == 0 {
		return errPlain(c, "try 'curl --help' or 'curl URL'")
	}
	url := normalizeURL(operands[0])
	body, ok := s.Web[url]
	if !ok {
		fmt.Fprintf(c.Stderr, "curl: (6) Could not resolve host: %s\n", operands[0])
		return 6
	}
	var out strings.Builder
	if bools['i'] {
		out.WriteString("HTTP/1.1 200 OK\r\nServer: nginx/1.24.0\r\nContent-Type: text/html\r\nContent-Length: " +
			strconv.Itoa(len(body)) + "\r\n\r\n")
	}
	out.WriteString(body)
	if file, has := vals['o']; has {
		if werr := s.FS.WriteFile(s.Cwd, file, out.String(), 0o644&^s.Umask, s.User, s.User); werr != nil {
			return errNode(c, file, werr)
		}
		return 0
	}
	// byte-exact output like real curl: no newline is appended
	fmt.Fprint(c.Stdout, out.String())
	return 0
}

func ssRun(c *Ctx) int {
	fmt.Fprint(c.Stdout, `Netid State  Recv-Q Send-Q Local Address:Port  Peer Address:PortProcess
udp   UNCONN 0      0      127.0.0.53%lo:53        0.0.0.0:*          
tcp   LISTEN 0      128          0.0.0.0:22          0.0.0.0:*          users:(("sshd",pid=812,fd=3))
tcp   LISTEN 0      511        127.0.0.1:631         0.0.0.0:*          users:(("cupsd",pid=844,fd=7))
tcp   LISTEN 0      511          0.0.0.0:80          0.0.0.0:*          users:(("nginx",pid=1080,fd=6))
tcp   LISTEN 0      511             [::]:80             [::]:*          users:(("nginx",pid=1080,fd=7))
`)
	return 0
}
