package main

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"

	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"

	"xtpro/backend/internal/tunnel"
)

const (
	defaultServerAddr  = "103.77.246.206:8882"
	defaultLocalHost   = "localhost"
	defaultLocalPort   = 80
	heartbeatInterval  = 2 * time.Second // Faster detection
	backendIdleTimeout = 5 * time.Second
	backendIdleRetries = 3
	udpControlInterval = 2 * time.Second
	udpControlTimeout  = 6 * time.Second
)

const debugUDP = false

const (
	udpMsgHandshake byte = 1
	udpMsgData      byte = 2
	udpMsgClose     byte = 3
	udpMsgPing      byte = 4
	udpMsgPong      byte = 5
)

type client struct {
	serverAddr         string
	localAddr          string
	key                string
	clientID           string
	remotePort         int
	publicHost         string
	protocol           string
	subdomain          string // Subdomain assigned by server for HTTP mode
	baseDomain         string // Base domain assigned by server for HTTP mode
	certFingerprint    string // Optional: Server certificate fingerprint for pinning
	insecureSkipVerify bool   // Skip TLS certificate verification
	uiEnabled          bool

	// Control connection
	control        net.Conn
	enc            *jsonWriter
	dec            *jsonReader
	closeOnce      sync.Once
	done           chan struct{}
	trafficQuit    chan struct{}
	statusCh       chan trafficStats
	bytesUp        uint64
	bytesDown      uint64
	pingCh         chan time.Duration
	pingSent       int64
	pingMs         int64
	exitFlag       uint32
	activeSessions int64
	totalSessions  uint64

	udpMu       sync.Mutex
	udpSessions map[string]*udpClientSession
	udpConn     *net.UDPConn
	udpReady    bool

	udpCtrlMu        sync.Mutex
	udpPingTicker    *time.Ticker
	udpPingStop      chan struct{}
	udpLastPing      time.Time
	udpLastPong      time.Time
	udpControlWarned bool
	udpCtrlStatus    string

	dataMu           sync.Mutex
	lastServerData   time.Time
	lastBackendData  time.Time
	totalUDPSessions uint64
	udpSecret        []byte // Key for UDP encryption
}

type trafficStats struct {
	upRate    string
	downRate  string
	totalUp   string
	totalDown string
}

type udpClientSession struct {
	id         string
	conn       *net.UDPConn
	remoteAddr string
	closeOnce  sync.Once
	closed     chan struct{}
	timer      *time.Timer
	idleCount  int
}

func (s *udpClientSession) Close() {
	s.closeOnce.Do(func() {
		close(s.closed)
		if s.timer != nil {
			s.timer.Stop()
		}
		if s.conn != nil {
			s.conn.Close()
		}
	})
}

type jsonWriter struct {
	enc *json.Encoder
	mu  sync.Mutex
}

type jsonReader struct {
	dec *json.Decoder
	mu  sync.Mutex
}

func (w *jsonWriter) Encode(msg tunnel.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.enc.Encode(msg)
}

func (r *jsonReader) Decode(msg *tunnel.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dec.Decode(msg)
}

func main() {
	// Custom usage message with examples
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `
╔════════════════════════════════════════════════════════════════════════════╗
║                 XTPro v%s - Client                                   ║
║            Tunnel Localhost ra Internet - Miễn Phí 100%%                   ║
╚════════════════════════════════════════════════════════════════════════════╝

🌟 TÍNH NĂNG:
  • HTTP Tunnel:  Nhận subdomain HTTPS tự động (https://abc.domain.com)
  • TCP Tunnel:   Public bất kỳ service TCP nào (Web, SSH, RDP, Database...)
  • UDP Tunnel:   Cho game server (Minecraft PE, CS:GO, Palworld...)
  • File Sharing: Chia sẻ file/folder như Windows Network Share
  • TLS Security: Mã hóa end-to-end cho tất cả kết nối
  • Auto Reconnect: Tự động kết nối lại khi mất mạng

📖 CÚ PHÁP:
  xtpro [OPTIONS] [LOCAL_PORT]
  xtpro [OPTIONS] [SPEC...]
  xtpro [OPTIONS] --ports "SPEC[,SPEC...]"

⚙️  CÁC THAM SỐ:
`, tunnel.Version)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
💡 VÍ DỤ SỬ DỤNG:

▶ HTTP Tunnel - Nhận Subdomain HTTPS:
  xtpro --proto http 80              # Share website port 80
  xtpro --proto http 3000            # Share Node.js/React app
  xtpro --proto http 443             # Tunnel local HTTPS site
  → Kết quả: https://abc123.googleidx.click

▶ TCP Tunnel - Nhận IP:Port:
  xtpro 80                           # Public web server
  xtpro 3389                         # Remote Desktop (RDP)
  xtpro 22                           # SSH server
  → Kết quả: 103.77.246.206:10000

▶ Multi-Port (1 command):
  xtpro tcp:2222:11111 udp:53 http:3000 2222
  # tcp:2222:11111  => local 2222, remote 11111 (request)
  # 2222            => tcp local 2222, remote auto-allocate
  # 2222:11111      => tcp local 2222, remote 11111
  # udp:53          => udp local 53,  remote auto-allocate

▶ UDP Tunnel - Game Server:
  xtpro --proto udp 19132            # Minecraft Bedrock Edition
  xtpro --proto udp 25565            # Minecraft Java (UDP mode)
  xtpro --proto udp 7777             # Palworld server
  → Kết quả: 103.77.246.206:10000

▶ File Sharing - Chia Sẻ File/Folder:
  xtpro --file /home/user/Documents --pass matkhau123
  xtpro --file "C:\\Projects" --pass abc123         # Windows
  xtpro --file ~/Downloads --pass secret --permissions r  # Read-only
  → Kết quả: Mount như ổ đĩa mạng (Z:\\) hoặc truy cập qua web

▶ Kết nối tới VPS riêng:
  xtpro --server YOUR_VPS_IP:8882 --proto http 80

🔗 THÔNG TIN:
  • Website:        https://googleidx.click
  • Documentation:  https://github.com/kuratajr/xtpro
  • Issues:         https://github.com/kuratajr/xtpro/issues

© 2026 XTPro - Developed by Kuratajr
Licensed under FREE TO USE - NON-COMMERCIAL ONLY

`)
	}

	serverAddr := flag.String("server", defaultServerAddr, "Địa chỉ tunnel server (mặc định: 103.77.246.206:8882)")
	hostFlag := flag.String("host", defaultLocalHost, "Host nội bộ cần tunnel (mặc định: localhost)")
	portFlag := flag.Int("port", defaultLocalPort, "Port nội bộ (bị ghi đè nếu truyền trực tiếp)")
	id := flag.String("id", "", "Client ID (optional)")
	proto := flag.String("proto", "tcp", "Protocol: tcp, udp, or http")
	portsFlag := flag.String("ports", "", "Mở nhiều port 1 lần (vd: \"tcp:2222:11111 udp:53 http:3000 2222\")")
	dryRun := flag.Bool("dry-run", false, "Chỉ parse port spec và in ra rule (không chạy tunnel)")
	UI := flag.Bool("ui", true, "Enable TUI (disable with --ui=false)")
	uiMulti := flag.Bool("ui-multi", false, "Hiển thị TUI cho multi-port (mặc định tắt)")
	certPin := flag.String("cert-pin", "", "Optional: Server certificate SHA256 fingerprint for pinning (hex format)")
	insecure := flag.Bool("insecure", false, "Skip TLS certificate verification (for testing with localhost)")

	// File sharing flags
	fileFlag := flag.String("file", "", "Đường dẫn file/folder cần chia sẻ (vd: /home/user/docs, C:\\\\Users\\\\Admin\\\\Documents)")
	userFlag := flag.String("user", "xtpro", "Username để truy cập file share (mặc định: xtpro)")
	passFlag := flag.String("pass", "", "Mật khẩu để truy cập file share (bắt buộc khi dùng --file)")
	permsFlag := flag.String("permissions", "rw", "Quyền hạn: r (chỉ đọc), rw (đọc-ghi), rwx (đầy đủ)")

	flag.Parse()

	log.SetOutput(os.Stderr)
	log.SetFlags(log.LstdFlags)

	// Check if file sharing mode
	if *fileFlag != "" {
		if *passFlag == "" {
			log.Fatal("❌ Lỗi: --pass bắt buộc khi dùng --file")
		}
		// Trim spaces to prevent auth errors
		username := strings.TrimSpace(*userFlag)
		password := strings.TrimSpace(*passFlag)
		perms := strings.TrimSpace(*permsFlag)

		if err := runFileShareMode(*fileFlag, username, password, perms, *serverAddr, *insecure); err != nil {
			log.Fatalf("❌ File sharing lỗi: %v", err)
		}
		return
	}

	clientID := strings.TrimSpace(*id)
	if clientID == "" {
		host, _ := os.Hostname()
		clientID = fmt.Sprintf("client-%s", host)
	}

	localHost := strings.TrimSpace(*hostFlag)
	if localHost == "" {
		localHost = defaultLocalHost
	}

	args := normalizedArgs(flag.Args())

	// Decide single-port legacy mode vs multi-spec mode.
	// Legacy:
	//   xtpro [PORT]
	//   xtpro [HOST PORT]
	// Multi:
	//   xtpro [SPEC...]
	//   xtpro --ports "SPEC[,SPEC...]"
	var specs []portSpec
	if strings.TrimSpace(*portsFlag) != "" {
		parsed, err := parsePortSpecsFromString(*portsFlag)
		if err != nil {
			log.Fatalf("[client] --ports không hợp lệ: %v", err)
		}
		specs = parsed
	} else if len(args) == 0 {
		// keep defaults (single-port)
		specs = []portSpec{{
			protocol:   strings.ToLower(strings.TrimSpace(*proto)),
			localPort:  *portFlag,
			remotePort: 0,
			raw:        "",
		}}
	} else if len(args) == 1 && !strings.Contains(args[0], ":") {
		// legacy: xtpro 80
		p, err := parsePort(args[0])
		if err != nil {
			log.Fatalf("[client] port không hợp lệ: %q", args[0])
		}
		specs = []portSpec{{protocol: strings.ToLower(strings.TrimSpace(*proto)), localPort: p, remotePort: 0, raw: args[0]}}
	} else if len(args) == 2 && !strings.Contains(args[0], ":") && !strings.Contains(args[1], ":") {
		// legacy: xtpro HOST PORT
		if strings.TrimSpace(args[0]) != "" {
			localHost = args[0]
		}
		p, err := parsePort(args[1])
		if err != nil {
			log.Fatalf("[client] port không hợp lệ: %q", args[1])
		}
		specs = []portSpec{{protocol: strings.ToLower(strings.TrimSpace(*proto)), localPort: p, remotePort: 0, raw: strings.Join(args, " ")}}
	} else {
		// multi-spec mode: default proto must be tcp per spec rules.
		parsed, err := parsePortSpecs(args)
		if err != nil {
			log.Fatalf("[client] port spec không hợp lệ: %v", err)
		}
		specs = parsed
	}

	for i := range specs {
		p := strings.ToLower(strings.TrimSpace(specs[i].protocol))
		if p != "udp" && p != "http" {
			p = "tcp"
		}
		specs[i].protocol = p
		if specs[i].localPort <= 0 || specs[i].localPort > 65535 {
			log.Fatalf("[client] port không hợp lệ: %d", specs[i].localPort)
		}
		if specs[i].remotePort < 0 || specs[i].remotePort > 65535 {
			log.Fatalf("[client] remote port không hợp lệ: %d", specs[i].remotePort)
		}
	}

	if *dryRun {
		fmt.Println("Parsed port rules:")
		for _, sp := range specs {
			remote := "auto"
			if sp.remotePort > 0 {
				remote = strconv.Itoa(sp.remotePort) + " (request)"
			}
			raw := strings.TrimSpace(sp.raw)
			if raw == "" {
				raw = "(default)"
			}
			fmt.Printf("- %s:%d -> %s   from=%s\n", sp.protocol, sp.localPort, remote, raw)
		}
		return
	}

	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	uiEnabled := *UI && isTTY && len(specs) == 1
	multiUIEnabled := *UI && *uiMulti && isTTY && len(specs) > 1
	if multiUIEnabled {
		// Prevent concurrent log output from tearing the TUI.
		// (Each tunnel goroutine logs during connect/reconnect.)
		log.SetOutput(io.Discard)
	}

	clients := make([]*client, 0, len(specs))
	for _, sp := range specs {
		suffix := fmt.Sprintf("%s-%d", sp.protocol, sp.localPort)
		perClientID := clientID
		if perClientID == "" {
			perClientID = suffix
		} else {
			perClientID = perClientID + "-" + suffix
		}
		cl := &client{
			serverAddr:         *serverAddr,
			localAddr:          net.JoinHostPort(localHost, strconv.Itoa(sp.localPort)),
			clientID:           perClientID,
			protocol:           sp.protocol,
			remotePort:         sp.remotePort, // <=0 => auto-allocate (old behavior). >0 => request that port.
			certFingerprint:    strings.ToLower(strings.TrimSpace(*certPin)),
			uiEnabled:          uiEnabled, // only for single-port
			insecureSkipVerify: *insecure,
		}
		clients = append(clients, cl)
	}

	// Handle Ctrl+C for clean shutdown (multi or single).
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		for _, cl := range clients {
			cl.requestExit()
		}
	}()

	if len(clients) == 1 {
		if err := clients[0].run(); err != nil {
			log.Fatalf("[client] lỗi: %v", err)
		}
		return
	}

	// Multi: run all tunnels concurrently (TUI disabled automatically).
	var wg sync.WaitGroup
	for _, cl := range clients {
		wg.Add(1)
		go func(c *client) {
			defer wg.Done()
			if err := c.run(); err != nil {
				log.Printf("[client] tunnel %s lỗi: %v", c.clientID, err)
			}
		}(cl)
	}

	if multiUIEnabled {
		runMultiUIDashboard(clients)
	}
	wg.Wait()
}

func runMultiUIDashboard(clients []*client) {
	if len(clients) == 0 {
		return
	}

	// Put terminal in raw mode for q/ESC.
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err == nil {
		defer term.Restore(fd, oldState)
	}

	fmt.Print("\033[2J\033[H\033[?25l")
	defer fmt.Print("\033[?25h\033[2J\033[H")

	stop := make(chan struct{})
	defer close(stop)

	// Key listener: q or ESC to exit.
	go func() {
		buf := make([]byte, 1)
		for {
			select {
			case <-stop:
				return
			default:
			}
			n, _ := os.Stdin.Read(buf)
			if n <= 0 {
				continue
			}
			b := buf[0]
			if b == 'q' || b == 'Q' || b == 27 {
				for _, c := range clients {
					c.requestExit()
				}
				return
			}
		}
	}()

	type snap struct {
		up   uint64
		down uint64
	}
	_ = snap{}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	stripANSI := func(s string) string {
		// Strip common ANSI escape sequences:
		// - CSI: ESC [ ... <final>
		// - OSC: ESC ] ... (BEL or ESC \)
		var out strings.Builder
		out.Grow(len(s))
		for i := 0; i < len(s); i++ {
			if s[i] != 0x1b {
				out.WriteByte(s[i])
				continue
			}
			// ESC sequence
			if i+1 >= len(s) {
				break
			}
			next := s[i+1]
			if next == '[' {
				// CSI: consume until final byte (0x40..0x7E)
				i += 2
				for i < len(s) {
					b := s[i]
					if b >= 0x40 && b <= 0x7e {
						break
					}
					i++
				}
				continue
			}
			if next == ']' {
				// OSC: consume until BEL or ST (ESC \)
				i += 2
				for i < len(s) {
					if s[i] == 0x07 { // BEL
						break
					}
					if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
						i++
						break
					}
					i++
				}
				continue
			}
			// Other single-char escapes: skip ESC + next
			i++
		}
		return out.String()
	}

	cutToWidth := func(s string, width int) string {
		if width <= 0 {
			return ""
		}
		plain := stripANSI(s)
		if runewidth.StringWidth(plain) <= width {
			return plain
		}
		var b strings.Builder
		w := 0
		for _, r := range plain {
			rw := runewidth.RuneWidth(r)
			if w+rw > width {
				break
			}
			b.WriteRune(r)
			w += rw
		}
		return b.String()
	}

	// (legacy helper removed)

	extractPublicIP := func() string {
		// Prefer any already-known public host, fall back to server address host.
		for _, c := range clients {
			if strings.TrimSpace(c.publicHost) == "" {
				continue
			}
			host, _, err := net.SplitHostPort(c.publicHost)
			if err == nil && strings.TrimSpace(host) != "" {
				return host
			}
		}
		for _, c := range clients {
			if strings.TrimSpace(c.serverAddr) == "" {
				continue
			}
			host, _, err := net.SplitHostPort(c.serverAddr)
			if err == nil && strings.TrimSpace(host) != "" {
				return host
			}
		}
		return ""
	}

	localHostForDisplay := func() string {
		// Try to extract host from first client localAddr.
		for _, c := range clients {
			if strings.TrimSpace(c.localAddr) == "" {
				continue
			}
			host, _, err := net.SplitHostPort(c.localAddr)
			if err == nil && strings.TrimSpace(host) != "" {
				return host
			}
		}
		return "localhost"
	}

	protoOrder := []string{"tcp", "udp", "http"}

	joinMappings := func(items []string, maxWidth int) string {
		// Build "a b c ..." but truncate safely if too long.
		if len(items) == 0 {
			return "-"
		}
		joined := strings.Join(items, "  ")
		if maxWidth <= 0 {
			return joined
		}
		if runewidth.StringWidth(joined) <= maxWidth {
			return joined
		}
		// keep as many items as fit
		var b strings.Builder
		for i := 0; i < len(items); i++ {
			part := items[i]
			if b.Len() > 0 {
				b.WriteString("  ")
			}
			next := b.String() + part
			if runewidth.StringWidth(next) > maxWidth {
				break
			}
			b.Reset()
			b.WriteString(next)
		}
		out := b.String()
		if out == "" {
			if maxWidth <= 3 {
				return cutToWidth(joined, maxWidth)
			}
			return cutToWidth(joined, maxWidth-3) + "..."
		}
		if out != joined {
			// ensure ellipsis fits
			if runewidth.StringWidth(out)+3 <= maxWidth {
				return out + "..."
			}
			if maxWidth <= 3 {
				return cutToWidth(out, maxWidth)
			}
			return cutToWidth(out, maxWidth-3) + "..."
		}
		return out
	}

	for {
		allExited := true
		for _, c := range clients {
			if atomic.LoadUint32(&c.exitFlag) == 0 {
				allExited = false
				break
			}
		}
		if allExited {
			return
		}

		<-ticker.C

		// Render exactly like single UI style, only replacing:
		// - 🌐 Public: show IP only
		// - add per-protocol two lines: Protocol + Ports
		lines := renderMultiFrameLikeSingle(clients, extractPublicIP(), localHostForDisplay(), protoOrder, joinMappings)

		var b strings.Builder
		b.WriteString("\033[H")
		for _, ln := range lines {
			// Clear the whole line first to avoid leftover artifacts
			// when the new frame line is shorter than the previous one.
			b.WriteString("\r\033[2K")
			b.WriteString(ln)
			b.WriteByte('\n')
		}
		b.WriteString("\033[J")
		fmt.Print(b.String())
	}
}

func renderMultiFrameLikeSingle(
	clients []*client,
	publicIP string,
	localHost string,
	protoOrder []string,
	joinMappings func(items []string, maxWidth int) string,
) []string {
	// ANSI colors (copy from renderFrame)
	const (
		reset       = "\033[0m"
		bold        = "\033[1m"
		cyan        = "\033[36m"
		green       = "\033[32m"
		yellow      = "\033[33m"
		magenta     = "\033[35m"
		blue        = "\033[34m"
		brightCyan  = "\033[96m"
		brightGreen = "\033[92m"
	)

	if strings.TrimSpace(publicIP) == "" {
		publicIP = "pending..."
	}
	if strings.TrimSpace(localHost) == "" {
		localHost = "localhost"
	}

	// aggregate stats + mappings
	var totalUp, totalDown uint64
	active := 0
	total := 0
	maxPing := int64(-1)
	mappings := map[string][]string{"tcp": {}, "udp": {}, "http": {}}

	for _, c := range clients {
		up := atomic.LoadUint64(&c.bytesUp)
		down := atomic.LoadUint64(&c.bytesDown)
		totalUp += up
		totalDown += down
		total++
		if strings.TrimSpace(c.publicHost) != "" && c.remotePort > 0 {
			active++
		}
		p := strings.ToLower(nonEmpty(c.protocol, "tcp"))
		lp := 0
		if _, port, err := net.SplitHostPort(c.localAddr); err == nil {
			if n, err2 := strconv.Atoi(port); err2 == nil {
				lp = n
			}
		}
		remote := "pending"
		if c.remotePort > 0 {
			remote = strconv.Itoa(c.remotePort)
		}
		switch p {
		case "http":
			if strings.TrimSpace(c.subdomain) != "" && strings.TrimSpace(c.baseDomain) != "" && lp > 0 {
				mappings[p] = append(mappings[p], fmt.Sprintf("%d:%s.%s", lp, c.subdomain, c.baseDomain))
			} else if lp > 0 {
				mappings[p] = append(mappings[p], fmt.Sprintf("%d:%s", lp, remote))
			} else {
				mappings[p] = append(mappings[p], fmt.Sprintf("%s:%s", c.localAddr, remote))
			}
		default:
			if lp > 0 {
				mappings[p] = append(mappings[p], fmt.Sprintf("%d:%s", lp, remote))
			} else {
				mappings[p] = append(mappings[p], fmt.Sprintf("%s:%s", c.localAddr, remote))
			}
		}
		ping := atomic.LoadInt64(&c.pingMs)
		if ping > maxPing {
			maxPing = ping
		}
		// keep last updated so rates can be added later if desired
	}

	// Status like single
	statusEmoji := "🟢"
	statusColor := green
	statusText := "ACTIVE"
	if active == 0 {
		statusEmoji = "🟡"
		statusColor = yellow
		statusText = "CONNECTING"
	}

	statusLine := func() string {
		now := time.Now().Format("15:04:05")
		return fmt.Sprintf(bold+brightCyan+"║"+reset+"  %s Status   : %s%s%s (%s)", statusEmoji, statusColor, bold, statusText, now)
	}

	// Keep the multi UI strictly within the same fixed width as single UI to avoid wrapping.
	// The single UI frame is 54 columns wide (based on the hardcoded border line).
	const frameWidth = 54

	var cutPlainToWidth func(s string, width int) string
	cutPlainToWidth = func(s string, width int) string {
		if width <= 0 {
			return ""
		}
		if runewidth.StringWidth(s) <= width {
			return s
		}
		var b strings.Builder
		w := 0
		for _, r := range s {
			rw := runewidth.RuneWidth(r)
			if w+rw > width {
				break
			}
			b.WriteRune(r)
			w += rw
		}
		// add ellipsis if we had to truncate
		out := b.String()
		if out != s && width >= 3 {
			// ensure ellipsis fits
			if runewidth.StringWidth(out)+3 <= width {
				return out + "..."
			}
			// otherwise hard cut
			return cutPlainToWidth(out, width)
		}
		return out
	}

	makeRow := func(emoji, label, val, color string) string {
		prefixVisible := 16
		currentPrefix := 2 + 2 + 1 + len(label) + 2
		padLabel := prefixVisible - currentPrefix
		if padLabel < 0 {
			padLabel = 0
		}
		labelStr := label + strings.Repeat(" ", padLabel)

		// Truncate value so the whole line never exceeds frameWidth.
		plainPrefix := "║  " + emoji + " " + labelStr + " : "
		maxValWidth := frameWidth - runewidth.StringWidth(plainPrefix)
		if maxValWidth < 0 {
			maxValWidth = 0
		}
		val = cutPlainToWidth(val, maxValWidth)

		return fmt.Sprintf(bold+brightCyan+"║"+reset+"  %s %s : %s%s%s", emoji, labelStr, color, val, reset)
	}

	// traffic summary
	totalStats := trafficStats{
		upRate:    formatRate(0, time.Second),
		downRate:  formatRate(0, time.Second),
		totalUp:   formatBytes(totalUp),
		totalDown: formatBytes(totalDown),
	}

	pingLine := func() string {
		if maxPing < 0 {
			return fmt.Sprintf(bold+brightCyan+"║"+reset+"  🏓 Ping     : %sN/A%s [----]", cyan, reset)
		}
		pingText, bars := formatPingDisplay(time.Duration(maxPing) * time.Millisecond)
		return fmt.Sprintf(bold+brightCyan+"║"+reset+"  🏓 Ping     : %s%s %s%s", green, pingText, bars, reset)
	}

	lines := []string{
		bold + brightCyan + "╔══════════════════════════════════════════════════════",
		bold + brightCyan + "║" + reset + bold + "      Kuratajr | XTPro - Tunnel Việt Nam Free",
		bold + brightCyan + "╠══════════════════════════════════════════════════════",
		statusLine(),
		makeRow("🔗", "Local", localHost+" (multi)", cyan),
		makeRow("🌐", "Public", publicIP, brightGreen+bold),
	}

	// per-protocol two lines (exactly what you asked)
	for _, p := range protoOrder {
		items := mappings[p]
		if len(items) == 0 {
			continue
		}
		lines = append(lines, makeRow("📡", "Protocol", strings.ToUpper(p), magenta))
		// Keep ports within the same UI width as single mode.
		lines = append(lines, makeRow("🔌", "Ports", joinMappings(items, 9999), cyan))
	}

	lines = append(lines,
		bold+brightCyan+"╠══════════════════════════════════════════════════════",
		func() string {
			v1 := fmt.Sprintf("⬆️  %s%s/s%s", green, totalStats.upRate, reset)
			v2 := fmt.Sprintf("⬇️  %s%s/s%s", blue, totalStats.downRate, reset)
			return fmt.Sprintf(bold+brightCyan+"║"+reset+"  📊 Traffic  : %s %s", v1, v2)
		}(),
		fmt.Sprintf(bold+brightCyan+"║"+reset+"  📈 Total    : %s%s%s ↑  %s%s%s ↓", cyan, totalStats.totalUp, reset, cyan, totalStats.totalDown, reset),
		fmt.Sprintf(bold+brightCyan+"║"+reset+"  🔌 Sessions : active %s%d%s | total %s%d%s", yellow, active, reset, cyan, total, reset),
		pingLine(),
		makeRow("⚙️", "Version", tunnel.Version, magenta),
		bold+brightCyan+"╚══════════════════════════════════════════════════════",
		"",
		cyan+"  Press 'q' or ESC to quit"+reset,
	)

	return lines
}

func (c *client) run() error {
	// ✅ FIX: Exponential backoff để tránh spam reconnect
	backoff := 3 * time.Second
	maxBackoff := 5 * time.Minute

	for {
		if atomic.LoadUint32(&c.exitFlag) == 1 {
			return nil
		}
		if err := c.connectControl(); err != nil {
			log.Printf("[client] kết nối control thất bại: %v", err)
			log.Printf("[client] retry sau %v...", backoff)
			for slept := time.Duration(0); slept < backoff; slept += 250 * time.Millisecond {
				if atomic.LoadUint32(&c.exitFlag) == 1 {
					return nil
				}
				time.Sleep(250 * time.Millisecond)
			}

			// ✅ Exponential backoff: double mỗi lần fail, max 5 phút
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		// ✅ Reset backoff khi kết nối thành công
		backoff = 3 * time.Second

		if err := c.receiveLoop(); err != nil {
			log.Printf("[client] control lỗi: %v", err)
		}
		c.closeControl()
		if atomic.LoadUint32(&c.exitFlag) == 1 {
			return nil
		}

		log.Printf("[client] thử reconnect control...")
		for slept := time.Duration(0); slept < backoff; slept += 250 * time.Millisecond {
			if atomic.LoadUint32(&c.exitFlag) == 1 {
				return nil
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
}

func (c *client) requestExit() {
	atomic.StoreUint32(&c.exitFlag, 1)
	c.closeControl()
}

func (c *client) connectControl() error {
	// Connect with TLS (certificate verification skipped by default)
	tlsConfig := c.buildTLSConfig()
	conn, err := tls.Dial("tcp", c.serverAddr, tlsConfig)
	if err != nil {
		return err
	}

	c.closeOnce = sync.Once{}
	c.done = make(chan struct{})
	c.trafficQuit = make(chan struct{})
	c.statusCh = make(chan trafficStats, 1)
	c.pingCh = make(chan time.Duration, 1)
	c.control = conn
	c.enc = &jsonWriter{enc: tunnel.NewEncoder(conn)}
	c.dec = &jsonReader{dec: tunnel.NewDecoder(bufio.NewReader(conn))}
	c.stopUDPPing()
	c.setUDPCtrlStatus("offline")
	atomic.StoreUint64(&c.bytesUp, 0)
	atomic.StoreUint64(&c.bytesDown, 0)
	atomic.StoreInt64(&c.pingSent, 0)
	atomic.StoreInt64(&c.pingMs, -1)
	select {
	case c.pingCh <- time.Duration(-1):
	default:
	}
	success := false
	defer func() {
		if !success {
			if c.control != nil {
				c.control.Close()
				c.control = nil
			}
			if c.trafficQuit != nil {
				close(c.trafficQuit)
				c.trafficQuit = nil
			}
			c.enc = nil
			c.dec = nil
			c.udpMu.Lock()
			if c.udpConn != nil {
				c.udpConn.Close()
				c.udpConn = nil
			}
			c.udpMu.Unlock()
			c.stopUDPPing()
		}
	}()

	register := tunnel.Message{
		Type:     "register",
		Key:      c.key,
		ClientID: c.clientID,
		Target:   c.localAddr,
		Protocol: c.protocol,
	}

	// If reconnecting and we had a port before, request the same port
	if c.remotePort > 0 {
		register.RequestedPort = c.remotePort
	}

	if err := c.enc.Encode(register); err != nil {
		return err
	}

	resp := tunnel.Message{}
	if err := c.dec.Decode(&resp); err != nil {
		return err
	}
	if resp.Type != "registered" {
		return fmt.Errorf("đăng ký thất bại: %+v", resp)
	}
	if strings.TrimSpace(resp.Key) != "" {
		c.key = strings.TrimSpace(resp.Key)
	}
	c.remotePort = resp.RemotePort
	if strings.TrimSpace(resp.Protocol) != "" {
		c.protocol = strings.ToLower(strings.TrimSpace(resp.Protocol))
	}

	// For HTTP mode, server assigns a subdomain
	if c.protocol == "http" && resp.Subdomain != "" {
		c.subdomain = resp.Subdomain
	}

	// Handle UDP Encryption Key
	if resp.UDPSecret != "" {
		secret, err := base64.StdEncoding.DecodeString(resp.UDPSecret)
		if err == nil && len(secret) == 32 {
			c.udpSecret = secret
		}
	}
	// Also store base domain if provided
	if resp.BaseDomain != "" {
		c.baseDomain = resp.BaseDomain
	}

	hostPart := c.serverAddr
	if host, _, err := net.SplitHostPort(c.serverAddr); err == nil {
		hostPart = host
	}
	c.publicHost = net.JoinHostPort(hostPart, strconv.Itoa(c.remotePort))
	c.setUDPCtrlStatus("n/a")

	// Log success based on protocol
	if c.protocol == "http" {
		log.Printf("[client] ✅ HTTP Tunnel Active")
		log.Printf("[client] 🌐 Public URL: https://%s.googleidx.click", c.subdomain)
		log.Printf("[client] 📍 Forwarding to: %s", c.localAddr)
	} else {
		log.Printf("[client] đăng ký thành công, public port %d", c.remotePort)
	}

	if c.protocol == "udp" {
		c.setUDPCtrlStatus("offline")
		if err := c.setupUDPChannel(); err != nil {
			log.Printf("[client] thiết lập UDP control lỗi: %v", err)
		} else if debugUDP {
			log.Printf("[client] UDP control đang chờ handshake với %s", c.serverAddr)
		}
	}
	go c.heartbeatLoop()
	go c.trafficLoop()
	go c.displayLoop()
	success = true
	return nil
}

func (c *client) receiveLoop() error {
	for {
		msg := tunnel.Message{}
		if err := c.dec.Decode(&msg); err != nil {
			if isEOF(err) {
				return io.EOF
			}
			return err
		}

		switch msg.Type {
		case "proxy":
			go c.handleProxy(msg.ID)
		case "udp_open":
			c.handleUDPOpen(msg)
		case "udp_close":
			c.handleUDPClose(msg.ID)
		case "ping":
			_ = c.enc.Encode(tunnel.Message{Type: "pong"})
		case "pong":
			c.recordPingReply()
		case "http_request":
			// Handle HTTP request
			go c.handleHTTPRequest(msg)
		case "error":
			log.Printf("[client] server báo lỗi: %s", msg.Error)
		default:
			log.Printf("[client] thông điệp không hỗ trợ: %+v", msg)
		}
	}
}

func (c *client) heartbeatLoop() {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			start := time.Now()
			if err := c.enc.Encode(tunnel.Message{Type: "ping"}); err != nil {
				return
			}
			atomic.StoreInt64(&c.pingSent, start.UnixNano())
		case <-c.done:
			return
		}
	}
}

func (c *client) trafficLoop() {
	const interval = 1 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastUp, lastDown uint64
	firstStats := trafficStats{
		upRate:    formatRate(0, interval),
		downRate:  formatRate(0, interval),
		totalUp:   formatBytes(0),
		totalDown: formatBytes(0),
	}
	select {
	case c.statusCh <- firstStats:
	default:
	}
	for {
		select {
		case <-ticker.C:
			up := atomic.LoadUint64(&c.bytesUp)
			down := atomic.LoadUint64(&c.bytesDown)
			upDelta := up - lastUp
			downDelta := down - lastDown
			lastUp = up
			lastDown = down
			stats := trafficStats{
				upRate:    formatRate(upDelta, interval),
				downRate:  formatRate(downDelta, interval),
				totalUp:   formatBytes(up),
				totalDown: formatBytes(down),
			}
			select {
			case c.statusCh <- stats:
			default:
				select {
				case <-c.statusCh:
				default:
				}
				c.statusCh <- stats
			}
		case <-c.trafficQuit:
			return
		case <-c.done:
			return
		}
	}
}

func (c *client) displayLoop() {
	if !c.uiEnabled {
		return
	}

	if c.uiEnabled {
		fmt.Print("\033[2J\033[H\033[?25l")
		defer fmt.Print("\033[?25h\033[2J\033[H")
	}

	traffic := trafficStats{
		upRate:    formatRate(0, time.Second),
		downRate:  formatRate(0, time.Second),
		totalUp:   formatBytes(0),
		totalDown: formatBytes(0),
	}
	ping := time.Duration(-1)
	hasTraffic := false

	render := func() {
		if !hasTraffic {
			return
		}
		c.renderFrame(traffic, ping)
	}

	// Force redraw every second even if no stats update
	redrawTicker := time.NewTicker(1 * time.Second)
	defer redrawTicker.Stop()

	for {
		select {
		case <-redrawTicker.C:
			render()
		case stats, ok := <-c.statusCh:
			if !ok {
				return
			}
			traffic = stats
			hasTraffic = true
			render()
		case duration, ok := <-c.pingCh:
			if !ok {
				ping = time.Duration(-1)
				continue
			}
			ping = duration
			render()
		case <-c.done:
			return
		case <-c.trafficQuit:
			return
		}
	}
}

func (c *client) handleProxy(id string) {
	if c.protocol == "udp" {
		log.Printf("[client] bỏ qua proxy TCP vì tunnel đang ở chế độ UDP")
		return
	}
	if strings.TrimSpace(id) == "" {
		return
	}

	localConn, err := net.Dial("tcp", c.localAddr)
	if err != nil {
		log.Printf("[client] không kết nối được backend %s: %v", c.localAddr, err)
		c.reportProxyError(id, err)
		return
	}

	atomic.AddInt64(&c.activeSessions, 1)
	atomic.AddUint64(&c.totalSessions, 1)

	// Connect to server with TLS
	tlsConfig := c.buildTLSConfig()
	srvConn, err := tls.Dial("tcp", c.serverAddr, tlsConfig)
	if err != nil {
		log.Printf("[client] không connect server cho proxy: %v", err)
		localConn.Close()
		c.reportProxyError(id, err)
		return
	}

	enc := tunnel.NewEncoder(srvConn)
	if err := enc.Encode(tunnel.Message{
		Type:     "proxy",
		Key:      c.key,
		ClientID: c.clientID,
		ID:       id,
	}); err != nil {
		log.Printf("[client] gửi proxy handshake lỗi: %v", err)
		localConn.Close()
		srvConn.Close()
		return
	}

	go func() {
		defer atomic.AddInt64(&c.activeSessions, -1)
		proxyCopyCount(srvConn, localConn, &c.bytesUp)
	}()
	go proxyCopyCount(localConn, srvConn, &c.bytesDown)
}

func (c *client) handleUDPOpen(msg tunnel.Message) {
	if c.protocol != "udp" {
		return
	}
	if strings.TrimSpace(msg.ID) == "" {
		return
	}
	if msg.Protocol != "" && strings.ToLower(msg.Protocol) != "udp" {
		return
	}
	backend, err := c.resolveBackendUDP()
	if err != nil {
		log.Printf("[client] resolve backend UDP lỗi: %v", err)
		c.sendUDPClose(msg.ID)
		return
	}
	conn, err := net.DialUDP("udp", nil, backend)
	if err != nil {
		log.Printf("[client] không kết nối được backend UDP %s: %v", backend, err)
		c.sendUDPClose(msg.ID)
		return
	}
	sess := &udpClientSession{
		id:         msg.ID,
		conn:       conn,
		remoteAddr: strings.TrimSpace(msg.RemoteAddr),
		closed:     make(chan struct{}),
	}
	c.udpMu.Lock()
	if c.udpSessions == nil {
		c.udpSessions = make(map[string]*udpClientSession)
	}
	if old, ok := c.udpSessions[msg.ID]; ok {
		delete(c.udpSessions, msg.ID)
		old.Close()
	}
	c.udpSessions[msg.ID] = sess
	atomic.AddUint64(&c.totalUDPSessions, 1)
	c.udpMu.Unlock()
	go c.readFromUDPLocal(sess)
}

func (c *client) handleUDPClose(id string) {
	if strings.TrimSpace(id) == "" {
		return
	}
	c.removeUDPSession(id, false)
}

func (c *client) setupUDPChannel() error {
	addr, err := net.ResolveUDPAddr("udp", c.serverAddr)
	if err != nil {
		return err
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return err
	}
	_ = conn.SetReadBuffer(4 * 1024 * 1024)
	_ = conn.SetWriteBuffer(4 * 1024 * 1024)
	c.udpMu.Lock()
	if c.udpConn != nil {
		c.udpConn.Close()
	}
	c.udpConn = conn
	c.udpReady = false
	c.udpMu.Unlock()
	c.stopUDPPing()
	c.setUDPCtrlStatus("handshake")
	go c.readUDPControl(conn)
	for i := 0; i < 3; i++ {
		if err := c.sendUDPHandshake(); err != nil {
			log.Printf("[client] gửi UDP handshake burst #%d lỗi: %v", i+1, err)
		} else if debugUDP {
			log.Printf("[client] gửi UDP handshake burst #%d tới %s", i+1, addr)
		}
		if i < 2 {
			time.Sleep(50 * time.Millisecond)
		}
	}
	go c.udpHandshakeRetry()
	return nil
}

func (c *client) readUDPControl(conn *net.UDPConn) {
	defer c.stopUDPPing()
	buf := make([]byte, 65535)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				log.Printf("[client] đọc UDP control lỗi: %v", err)
			}
			return
		}
		if n == 0 {
			continue
		}
		packet := make([]byte, n)
		copy(packet, buf[:n])
		c.handleUDPControlPacket(packet)
	}
}

func (c *client) handleUDPControlPacket(packet []byte) {
	if len(packet) < 3 {
		return
	}
	msgType := packet[0]
	key, idx, ok := decodeUDPField(packet, 1)
	if !ok || key == "" || key != c.key {
		return
	}
	switch msgType {
	case udpMsgData:
		id, next, ok := decodeUDPField(packet, idx)
		if !ok || id == "" {
			return
		}
		payload := make([]byte, len(packet)-next)
		copy(payload, packet[next:])
		c.handleUDPDataPacket(id, payload)
	case udpMsgClose:
		id, _, ok := decodeUDPField(packet, idx)
		if !ok || id == "" {
			return
		}
		c.handleUDPClose(id)
	case udpMsgHandshake:
		c.udpMu.Lock()
		if !c.udpReady && debugUDP {
			log.Printf("[client] UDP control handshake thành công từ %s", c.serverAddr)
		}
		c.udpReady = true
		c.udpMu.Unlock()
		c.startUDPPing()
	case udpMsgPong:
		_, next, ok := decodeUDPField(packet, idx)
		if !ok {
			return
		}
		payload := make([]byte, len(packet)-next)
		copy(payload, packet[next:])
		c.handleUDPPong(payload)
	case udpMsgPing:
		_, next, ok := decodeUDPField(packet, idx)
		if !ok {
			return
		}
		payload := make([]byte, len(packet)-next)
		copy(payload, packet[next:])
		c.sendUDPPong(payload)
	default:
	}
}

func (c *client) handleUDPDataPacket(id string, payload []byte) {
	if len(payload) == 0 {
		return
	}

	// Decrypt if secret is available
	if c.udpSecret != nil {
		decrypted, err := tunnel.DecryptUDP(c.udpSecret, payload)
		if err != nil {
			if debugUDP {
				log.Printf("[client] giải mã UDP thất bại: %v", err)
			}
			return
		}
		payload = decrypted
	}

	sess := c.getUDPSession(id)
	if sess == nil {
		return
	}
	c.markServerData()
	if _, err := sess.conn.Write(payload); err != nil {
		log.Printf("[client] ghi về backend UDP lỗi: %v", err)
		c.removeUDPSession(id, true)
		return
	}
	c.startBackendWait(id)
	if debugUDP {
		log.Printf("[client] nhận %d bytes UDP từ server cho phiên %s", len(payload), id)
	}
	atomic.AddUint64(&c.bytesDown, uint64(len(payload)))
}

func (c *client) readFromUDPLocal(sess *udpClientSession) {
	buf := make([]byte, 65535)
	for {
		n, err := sess.conn.Read(buf)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				log.Printf("[client] đọc UDP backend lỗi: %v", err)
			}
			break
		}
		if n == 0 {
			continue
		}
		payload := make([]byte, n)
		copy(payload, buf[:n])
		c.cancelBackendWait(sess.id)
		c.markBackendData()
		c.sendUDPData(sess.id, payload)
	}
	c.removeUDPSession(sess.id, true)
}

func (c *client) resolveBackendUDP() (*net.UDPAddr, error) {
	return net.ResolveUDPAddr("udp", c.localAddr)
}

func (c *client) getUDPSession(id string) *udpClientSession {
	c.udpMu.Lock()
	defer c.udpMu.Unlock()
	if c.udpSessions == nil {
		return nil
	}
	return c.udpSessions[id]
}

func (c *client) handleBackendTimeout(id string) {
	sess := c.getUDPSession(id)
	remote := ""
	if sess != nil {
		remote = sess.remoteAddr
	}
	if sess != nil {
		sess.idleCount++
		if sess.idleCount < backendIdleRetries {
			if debugUDP {
				log.Printf("[client] backend phiên %s (remote %s) chưa phản hồi (%d/%d)", id, remote, sess.idleCount, backendIdleRetries)
			}
			// restart timer
			c.startBackendWait(id)
			return
		}
	}
	log.Printf("[client] backend không phản hồi cho phiên %s (remote %s) - đóng phiên", id, remote)
	if c.enc != nil {
		_ = c.enc.Encode(tunnel.Message{Type: "udp_idle", ID: id, Protocol: "udp"})
	}
	c.removeUDPSession(id, true)
}

func (c *client) removeUDPSession(id string, notify bool) {
	c.udpMu.Lock()
	sess := c.udpSessions[id]
	if sess != nil {
		delete(c.udpSessions, id)
	}
	c.udpMu.Unlock()
	if sess == nil {
		return
	}
	sess.Close()
	if notify {
		c.sendUDPClose(id)
	}
}

func (c *client) startBackendWait(id string) {
	c.udpMu.Lock()
	defer c.udpMu.Unlock()
	if sess, ok := c.udpSessions[id]; ok {
		if sess.timer != nil {
			sess.timer.Stop()
		}
		sess.idleCount = 0
		sess.timer = time.AfterFunc(backendIdleTimeout, func() {
			c.handleBackendTimeout(id)
		})
	}
}

func (c *client) cancelBackendWait(id string) {
	c.udpMu.Lock()
	defer c.udpMu.Unlock()
	if sess, ok := c.udpSessions[id]; ok && sess.timer != nil {
		sess.timer.Stop()
		sess.timer = nil
		sess.idleCount = 0
	}
}

func (c *client) markServerData() {
	c.dataMu.Lock()
	c.lastServerData = time.Now()
	c.dataMu.Unlock()
}

func (c *client) markBackendData() {
	c.dataMu.Lock()
	c.lastBackendData = time.Now()
	c.dataMu.Unlock()
}

func (c *client) getLastServerData() time.Time {
	c.dataMu.Lock()
	defer c.dataMu.Unlock()
	return c.lastServerData
}

func (c *client) getLastBackendData() time.Time {
	c.dataMu.Lock()
	defer c.dataMu.Unlock()
	return c.lastBackendData
}

func (c *client) closeAllUDPSessions() {
	c.udpMu.Lock()
	sessions := make([]*udpClientSession, 0, len(c.udpSessions))
	for _, sess := range c.udpSessions {
		if sess.timer != nil {
			sess.timer.Stop()
			sess.timer = nil
		}
		sessions = append(sessions, sess)
	}
	c.udpSessions = make(map[string]*udpClientSession)
	c.udpMu.Unlock()
	for _, sess := range sessions {
		sess.Close()
	}
}

func (c *client) sendUDPData(id string, payload []byte) {
	if len(payload) == 0 {
		return
	}

	// Encrypt if secret is available
	if c.udpSecret != nil {
		encrypted, err := tunnel.EncryptUDP(c.udpSecret, payload)
		if err != nil {
			log.Printf("[client] mã hóa UDP lỗi: %v", err)
			return
		}
		payload = encrypted
	}

	if err := c.writeUDP(udpMsgData, id, payload); err != nil {
		log.Printf("[client] gửi udp_data lỗi: %v", err)
		return
	}
	atomic.AddUint64(&c.bytesUp, uint64(len(payload)))
}

func (c *client) sendUDPClose(id string) {
	if err := c.writeUDP(udpMsgClose, id, nil); err != nil {
		log.Printf("[client] gửi udp_close lỗi: %v", err)
	}
	if c.enc != nil {
		_ = c.enc.Encode(tunnel.Message{Type: "udp_close", ID: id, Protocol: "udp"})
	}
}

func (c *client) sendUDPHandshake() error {
	return c.writeUDP(udpMsgHandshake, "", nil)
}

func (c *client) sendUDPPing(payload []byte) error {
	return c.writeUDP(udpMsgPing, "", payload)
}

func (c *client) sendUDPPong(payload []byte) {
	if err := c.writeUDP(udpMsgPong, "", payload); err != nil && debugUDP {
		log.Printf("[client] gửi udp_pong lỗi: %v", err)
	}
}

func (c *client) udpHandshakeRetry() {
	const (
		retryInterval    = 500 * time.Millisecond
		handshakeTimeout = 10 * time.Second
		maxRetries       = 20
	)

	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()
	timeout := time.NewTimer(handshakeTimeout)
	defer timeout.Stop()

	attempts := 0
	for {
		c.udpMu.Lock()
		ready := c.udpReady
		connPresent := c.udpConn != nil
		c.udpMu.Unlock()
		if ready || !connPresent {
			if attempts > 0 && ready {
				log.Printf("[client] UDP handshake thành công sau %d lần thử (%d ms)", attempts+1, attempts*int(retryInterval/time.Millisecond))
			}
			return
		}
		select {
		case <-ticker.C:
			attempts++
			if attempts > maxRetries {
				log.Printf("[client] UDP handshake thất bại sau %d lần thử", maxRetries)
				c.udpMu.Lock()
				if c.udpConn != nil {
					c.udpConn.Close()
					c.udpConn = nil
				}
				c.udpMu.Unlock()
				c.setUDPCtrlStatus("offline")
				return
			}
			if err := c.sendUDPHandshake(); err != nil {
				if debugUDP {
					log.Printf("[client] retry handshake #%d lỗi: %v", attempts, err)
				}
			} else if debugUDP {
				log.Printf("[client] retry handshake #%d/%d", attempts, maxRetries)
			}
		case <-timeout.C:
			log.Printf("[client] UDP handshake timeout sau %v", handshakeTimeout)
			c.udpMu.Lock()
			if c.udpConn != nil {
				c.udpConn.Close()
				c.udpConn = nil
			}
			c.udpMu.Unlock()
			c.setUDPCtrlStatus("offline")
			return
		case <-c.done:
			return
		}
	}
}

func (c *client) writeUDP(msgType byte, id string, payload []byte) error {
	c.udpMu.Lock()
	conn := c.udpConn
	key := c.key
	ready := c.udpReady
	c.udpMu.Unlock()
	if conn == nil {
		return errors.New("udp chưa sẵn sàng")
	}
	if !ready && msgType != udpMsgHandshake && msgType != udpMsgPing {
		if debugUDP {
			log.Printf("[client] cảnh báo: gửi UDP khi handshake chưa hoàn tất (msg=%d)", msgType)
		}
	}
	buf := buildUDPMessage(msgType, key, id, payload)
	_, err := conn.Write(buf)
	if debugUDP && err == nil && msgType != udpMsgHandshake && !ready {
		log.Printf("[client] cảnh báo: gửi UDP nhưng handshake chưa được xác nhận")
	}
	return err
}

func (c *client) startUDPPing() {
	c.udpCtrlMu.Lock()
	if c.udpPingTicker != nil {
		c.udpCtrlMu.Unlock()
		return
	}
	ticker := time.NewTicker(udpControlInterval)
	stopCh := make(chan struct{})
	c.udpPingTicker = ticker
	c.udpPingStop = stopCh
	c.udpLastPong = time.Now()
	c.udpControlWarned = false
	c.udpCtrlMu.Unlock()
	c.setUDPCtrlStatus("pinging")
	go c.udpPingLoop(ticker, stopCh)
}

func (c *client) stopUDPPing() {
	c.udpCtrlMu.Lock()
	if c.udpPingTicker != nil {
		c.udpPingTicker.Stop()
		c.udpPingTicker = nil
	}
	if c.udpPingStop != nil {
		close(c.udpPingStop)
		c.udpPingStop = nil
	}
	c.udpControlWarned = false
	c.udpCtrlMu.Unlock()
}

func (c *client) udpPingLoop(ticker *time.Ticker, stopCh chan struct{}) {
	for {
		select {
		case <-ticker.C:
			ts := time.Now()
			payload := make([]byte, 8)
			binary.BigEndian.PutUint64(payload, uint64(ts.UnixNano()))
			c.udpCtrlMu.Lock()
			c.udpLastPing = ts
			c.udpCtrlMu.Unlock()
			if err := c.sendUDPPing(payload); err != nil && debugUDP {
				log.Printf("[client] gửi udp_ping lỗi: %v", err)
			}
			c.checkUDPPingTimeout()
		case <-stopCh:
			return
		case <-c.done:
			return
		}
	}
}

func (c *client) checkUDPPingTimeout() {
	c.udpCtrlMu.Lock()
	last := c.udpLastPong
	warned := c.udpControlWarned
	if time.Since(last) > udpControlTimeout {
		if !warned {
			c.udpControlWarned = true
			c.udpCtrlMu.Unlock()
			c.setUDPCtrlStatus("timeout")
			log.Printf("[client] UDP control timeout (>%v)", udpControlTimeout)
			return
		}
		c.udpCtrlMu.Unlock()
		return
	}
	if warned {
		c.udpControlWarned = false
	}
	c.udpCtrlMu.Unlock()
}

func (c *client) handleUDPPong(payload []byte) {
	if len(payload) < 8 {
		if debugUDP {
			log.Printf("[client] udp_pong payload quá ngắn")
		}
		return
	}
	sent := int64(binary.BigEndian.Uint64(payload))
	now := time.Now()
	rtt := time.Duration(now.UnixNano()-sent) * time.Nanosecond
	c.udpCtrlMu.Lock()
	c.udpLastPong = now
	c.udpControlWarned = false
	c.udpCtrlMu.Unlock()
	c.setUDPCtrlStatus(fmt.Sprintf("ok (%d ms)", rtt.Milliseconds()))
	if debugUDP {
		log.Printf("[client] nhận udp_pong, rtt %d ms", rtt.Milliseconds())
	}
}

func (c *client) setUDPCtrlStatus(status string) {
	c.udpCtrlMu.Lock()
	c.udpCtrlStatus = status
	c.udpCtrlMu.Unlock()
}

func (c *client) getUDPCtrlStatus() string {
	if strings.ToLower(c.protocol) != "udp" {
		return "n/a"
	}
	c.udpCtrlMu.Lock()
	status := c.udpCtrlStatus
	c.udpCtrlMu.Unlock()
	if status == "" {
		return "unknown"
	}
	return status
}

func (c *client) getSessionStats() (active int, total uint64) {
	activeTCP := atomic.LoadInt64(&c.activeSessions)
	totalTCP := atomic.LoadUint64(&c.totalSessions)

	c.udpMu.Lock()
	activeUDP := int64(len(c.udpSessions))
	c.udpMu.Unlock()
	totalUDP := atomic.LoadUint64(&c.totalUDPSessions)

	return int(activeTCP + activeUDP), totalTCP + totalUDP
}

func decodeUDPField(packet []byte, offset int) (string, int, bool) {
	if offset+2 > len(packet) {
		return "", offset, false
	}
	l := int(binary.BigEndian.Uint16(packet[offset : offset+2]))
	offset += 2
	if l < 0 || offset+l > len(packet) {
		return "", offset, false
	}
	return string(packet[offset : offset+l]), offset + l, true
}

func buildUDPMessage(msgType byte, key, id string, payload []byte) []byte {
	keyLen := len(key)
	idLen := len(id)
	total := 1 + 2 + keyLen
	if msgType != udpMsgHandshake {
		total += 2 + idLen
	}
	total += len(payload)
	buf := make([]byte, total)
	buf[0] = msgType
	binary.BigEndian.PutUint16(buf[1:], uint16(keyLen))
	copy(buf[3:], key)
	offset := 3 + keyLen
	if msgType != udpMsgHandshake {
		binary.BigEndian.PutUint16(buf[offset:], uint16(idLen))
		offset += 2
		copy(buf[offset:], id)
		offset += idLen
	}
	copy(buf[offset:], payload)
	return buf
}

func (c *client) reportProxyError(id string, err error) {
	if c.enc == nil {
		return
	}
	_ = c.enc.Encode(tunnel.Message{
		Type:  "proxy_error",
		ID:    id,
		Error: err.Error(),
	})
}

func (c *client) closeControl() {
	c.closeOnce.Do(func() {
		close(c.done)
	})
	c.closeAllUDPSessions()
	c.stopUDPPing()
	c.setUDPCtrlStatus("offline")
	c.udpMu.Lock()
	if c.udpConn != nil {
		c.udpConn.Close()
		c.udpConn = nil
	}
	c.udpReady = false
	c.udpMu.Unlock()
	if c.control != nil {
		c.control.Close()
	}
	c.control = nil
	c.enc = nil
	c.dec = nil
	if c.trafficQuit != nil {
		close(c.trafficQuit)
		c.trafficQuit = nil
	}
	if c.statusCh != nil {
		close(c.statusCh)
		c.statusCh = nil
	}
	if c.pingCh != nil {
		close(c.pingCh)
		c.pingCh = nil
	}
}

func normalizedArgs(input []string) []string {
	filtered := make([]string, 0, len(input))
	for _, arg := range input {
		if arg == "" {
			continue
		}
		if arg == os.Args[0] || strings.HasSuffix(arg, "/"+filepath.Base(os.Args[0])) {
			continue
		}
		if strings.Contains(arg, "/") {
			// likely a path accidentally forwarded via shell wrapper
			continue
		}
		filtered = append(filtered, arg)
	}
	return filtered
}

func formatRate(delta uint64, interval time.Duration) string {
	if interval <= 0 {
		return formatBytes(delta)
	}
	perSecond := float64(delta) / interval.Seconds()
	return formatBytesFloat(perSecond)
}

func formatSince(t time.Time) string {
	if t.IsZero() {
		return "N/A"
	}
	d := time.Since(t)
	if d < time.Millisecond {
		return "just now"
	}
	if d < time.Second {
		return fmt.Sprintf("%d ms ago", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1f s ago", d.Seconds())
	}
	if d < time.Hour {
		return fmt.Sprintf("%.1f m ago", d.Minutes())
	}
	return fmt.Sprintf("%.1f h ago", d.Hours())
}

func formatBytes(n uint64) string {
	return formatBytesFloat(float64(n))
}

func formatBytesFloat(value float64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	if value < 0 {
		value = 0
	}
	unit := 0
	for unit < len(units)-1 && value >= 1024 {
		value /= 1024
		unit++
	}
	switch {
	case value >= 100:
		return fmt.Sprintf("%.0f %s", value, units[unit])
	case value >= 10:
		return fmt.Sprintf("%.1f %s", value, units[unit])
	default:
		return fmt.Sprintf("%.2f %s", value, units[unit])
	}
}

type byteCounter struct {
	counter *uint64
}

func (b *byteCounter) Write(p []byte) (int, error) {
	if len(p) > 0 && b.counter != nil {
		atomic.AddUint64(b.counter, uint64(len(p)))
	}
	return len(p), nil
}

func proxyCopyCount(dst, src net.Conn, counter *uint64) {
	defer dst.Close()
	defer src.Close()
	reader := io.TeeReader(src, &byteCounter{counter: counter})
	_, _ = io.Copy(dst, reader)
}

func (c *client) recordPingReply() {
	sent := atomic.SwapInt64(&c.pingSent, 0)
	if sent <= 0 {
		return
	}
	ms := time.Since(time.Unix(0, sent))
	atomic.StoreInt64(&c.pingMs, ms.Milliseconds())
	if c.pingCh == nil {
		return
	}
	select {
	case c.pingCh <- ms:
	default:
		select {
		case <-c.pingCh:
		default:
		}
		select {
		case c.pingCh <- ms:
		default:
		}
	}
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func formatPingDisplay(d time.Duration) (string, string) {
	if d < 0 {
		return "N/A", "[----]"
	}
	ms := d.Milliseconds()
	var bars string
	switch {
	case ms <= 50:
		bars = "[||||]"
	case ms <= 120:
		bars = "[||| ]"
	case ms <= 250:
		bars = "[||  ]"
	case ms <= 500:
		bars = "[|   ]"
	default:
		bars = "[    ]"
	}
	return fmt.Sprintf("%d ms", ms), bars
}

func (c *client) renderFrame(stats trafficStats, ping time.Duration) {
	activeSessions, totalSessions := c.getSessionStats()

	// ANSI colors
	const (
		reset       = "\033[0m"
		bold        = "\033[1m"
		cyan        = "\033[36m"
		green       = "\033[32m"
		yellow      = "\033[33m"
		red         = "\033[31m"
		magenta     = "\033[35m"
		blue        = "\033[34m"
		brightCyan  = "\033[96m"
		brightGreen = "\033[92m"
	)

	// Status emoji and color
	statusEmoji := "🟢"
	statusColor := green
	statusText := "ACTIVE"
	if c.publicHost == "" || c.publicHost == "pending..." {
		statusEmoji = "🟡"
		statusColor = yellow
		statusText = "CONNECTING"
	}

	pingText, bars := formatPingDisplay(ping)
	pingColor := green
	// Status line special case for emoji
	// Status line special case for emoji
	statusLine := func() string {
		now := time.Now().Format("15:04:05")
		return fmt.Sprintf(bold+brightCyan+"║"+reset+"  %s Status   : %s%s%s (%s)", statusEmoji, statusColor, bold, statusText, now)
	}

	// Helper to create a row with an emoji label
	makeRow := func(emoji, label, val, color string) string {
		// "  emoji Label    : Value"
		// Align colon at specific column?
		// "  🔗 Local    : " -> 16 chars

		prefixVisible := 16
		currentPrefix := 2 + 2 + 1 + len(label) + 2
		padLabel := prefixVisible - currentPrefix
		if padLabel < 0 {
			padLabel = 0
		}

		labelStr := label + strings.Repeat(" ", padLabel)

		return fmt.Sprintf(bold+brightCyan+"║"+reset+"  %s %s : %s%s%s", emoji, labelStr, color, val, reset)
	}

	lines := []string{
		bold + brightCyan + "╔══════════════════════════════════════════════════════",
		bold + brightCyan + "║" + reset + bold + "      Kuratajr | XTPro - Tunnel Việt Nam Free",
		bold + brightCyan + "╠══════════════════════════════════════════════════════",
		statusLine(),
		makeRow("🔗", "Local", c.localAddr, cyan),
		func() string {
			displayHost := nonEmpty(c.publicHost, "pending...")
			if c.protocol == "http" && c.subdomain != "" {
				domain := c.baseDomain
				if domain == "" {
					domain = "googleidx.click" // Fallback default
				}
				displayHost = fmt.Sprintf("https://%s.%s", c.subdomain, domain)
			}
			return makeRow("🌐", "Public", displayHost, brightGreen+bold)
		}(),
		makeRow("📡", "Protocol", strings.ToUpper(nonEmpty(c.protocol, "tcp")), magenta),
		bold + brightCyan + "╠══════════════════════════════════════════════════════",
		func() string {
			v1 := fmt.Sprintf("⬆️  %s%s/s%s", green, stats.upRate, reset)
			v2 := fmt.Sprintf("⬇️  %s%s/s%s", blue, stats.downRate, reset)
			return fmt.Sprintf(bold+brightCyan+"║"+reset+"  📊 Traffic  : %s %s", v1, v2)
		}(),
		func() string {
			return fmt.Sprintf(bold+brightCyan+"║"+reset+"  📈 Total    : %s%s%s ↑  %s%s%s ↓", cyan, stats.totalUp, reset, cyan, stats.totalDown, reset)
		}(),
		func() string {
			ac := strconv.Itoa(activeSessions)
			to := strconv.FormatUint(totalSessions, 10)
			return fmt.Sprintf(bold+brightCyan+"║"+reset+"  🔌 Sessions : active %s%s%s | total %s%s%s", yellow, ac, reset, cyan, to, reset)
		}(),
		func() string {
			return fmt.Sprintf(bold+brightCyan+"║"+reset+"  🏓 Ping     : %s%s %s%s", pingColor, pingText, bars, reset)
		}(),
		makeRow("🔐", "Key", nonEmpty(c.key, "(none)"), yellow),
		makeRow("⚙️", "Version", tunnel.Version, magenta),
		bold + brightCyan + "╚══════════════════════════════════════════════════════",
		"",
		cyan + "  Press 'q' or ESC to quit" + reset,
	}

	if c.uiEnabled {
		var builder strings.Builder

		// Move cursor to top-left (Home)
		builder.WriteString("\033[H")

		// Write all lines
		for _, line := range lines {
			builder.WriteString(line)
			builder.WriteByte('\n')
		}

		// Clear from cursor to end of screen (cleans up any partial leftovers from prev frame)
		builder.WriteString("\033[J")

		// Print everything in one go to minimize tearing/scrolling artifacts
		fmt.Print(builder.String())
	}
}

func terminalSize() (int, int) {
	if term.IsTerminal(int(os.Stdout.Fd())) {
		if width, height, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
			return width, height
		}
	}
	return 80, 24
}

func isEOF(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection")
}
