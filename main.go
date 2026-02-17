package main

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"

	"flag"
	"fmt"
	"html/template"
	"image/png"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/qr"
	"github.com/gorilla/websocket"
	"github.com/pquerna/otp/totp"
	_ "modernc.org/sqlite"
)

// --- 配置与常量 ---

const (
	// AppVersion 应用版本
	AppVersion = "1.0.0"
	// WebPort 面板监听端口
	WebPort         = ":8888"
	DownloadURL     = "https://github.com/TerrySiu98/relay/releases/latest/download/relay"
	GithubLatestAPI = "https://api.github.com/repos/TerrySiu98/relay/releases/latest" // GitHub API
	TCPKeepAlive    = 60 * time.Second
	UDPBufferSize   = 4 * 1024 * 1024
	CopyBufferSize  = 32 * 1024
	MaxLogEntries   = 200
	MaxLogRetention = 1000
)

var (
	DataDir    string
	DBFile     string
	ConfigFile string
)

// 支持多个 Agent 连接端口
var ControlPorts = []string{":9999", ":10086"}

var bufPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, CopyBufferSize)
		return &b
	},
}

// --- 数据结构 ---

type LogicalRule struct {
	ID           string `json:"id"`
	Group        string `json:"group"`
	Note         string `json:"note"`
	EntryAgent   string `json:"entry_agent"`
	EntryPort    string `json:"entry_port"`
	ExitAgent    string `json:"exit_agent"`
	TargetIP     string `json:"target_ip"`
	TargetPort   string `json:"target_port"`
	Protocol     string `json:"protocol"`
	BridgePort   string `json:"bridge_port"`
	TrafficLimit int64  `json:"traffic_limit"`
	Disabled     bool   `json:"disabled"`
	SpeedLimit   int64  `json:"speed_limit"`

	TotalTx   int64 `json:"total_tx"`
	TotalRx   int64 `json:"total_rx"`
	UserCount int64 `json:"user_count"`

	TargetStatus  bool  `json:"-"`
	TargetLatency int64 `json:"-"`
}

type OpLog struct {
	Time   string `json:"time"`
	IP     string `json:"ip"`
	Action string `json:"action"`
	Msg    string `json:"msg"`
}

type AppConfig struct {
	WebUser      string            `json:"web_user"`
	WebPass      string            `json:"web_pass"`
	AgentToken   string            `json:"agent_token"`
	AgentAliases map[string]string `json:"agent_aliases"`
	AgentPorts   string            `json:"agent_ports"`
	MasterIP     string            `json:"master_ip"`
	MasterIPv6   string            `json:"master_ipv6"`
	MasterDomain string            `json:"master_domain"`
	IsSetup      bool              `json:"is_setup"`
	TgBotToken   string            `json:"tg_bot_token"`
	TgChatID     string            `json:"tg_chat_id"`
	TwoFAEnabled bool              `json:"two_fa_enabled"`
	TwoFASecret  string            `json:"two_fa_secret"`
	Rules        []LogicalRule     `json:"saved_rules"`
	Logs         []OpLog           `json:"logs"`
}

type ForwardTask struct {
	ID         string `json:"id"`
	Protocol   string `json:"protocol"`
	Listen     string `json:"listen"`
	Target     string `json:"target"`
	SpeedLimit int64  `json:"speed_limit"`
}

type TrafficReport struct {
	TaskID    string `json:"task_id"`
	TxDelta   int64  `json:"tx"`
	RxDelta   int64  `json:"rx"`
	UserCount int64  `json:"uc"`
}

type HealthReport struct {
	TaskID  string `json:"task_id"`
	Latency int64  `json:"lat"`
}

type AgentInfo struct {
	Name      string      `json:"name"`
	RemoteIP  string      `json:"remote_ip"`
	Conn      net.Conn    `json:"-"`
	SysStatus string      `json:"sys_status"`
	SendMu    *sync.Mutex `json:"-"`
}

type Message struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

type TrafficCounter struct {
	Rx int64
	Tx int64
}

type udpSession struct {
	conn       *net.UDPConn
	lastActive time.Time
}

type WSMessage struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

type WSDashboardData struct {
	TotalTraffic int64             `json:"total_traffic"`
	SpeedTx      int64             `json:"speed_tx"`
	SpeedRx      int64             `json:"speed_rx"`
	Agents       []AgentStatusData `json:"agents"`
	Rules        []RuleStatusData  `json:"rules"`
	Logs         []OpLog           `json:"logs"`
}

type AgentStatusData struct {
	Name      string `json:"name"`
	SysStatus string `json:"sys_status"`
}

type RuleStatusData struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Total     int64  `json:"total"`
	Tx        int64  `json:"tx"`
	Rx        int64  `json:"rx"`
	UserCount int64  `json:"uc"`
	Limit     int64  `json:"limit"`
	Status    bool   `json:"status"`
	Latency   int64  `json:"latency"`
}

var (
	db               *sql.DB
	config           AppConfig
	agents           = make(map[string]*AgentInfo)
	rules            = make([]LogicalRule, 0)
	mu               sync.Mutex
	runningListeners sync.Map
	activeTasks      sync.Map
	activeTargets    sync.Map // 存储最新的目标地址
	agentTraffic     sync.Map
	agentUserCounts  sync.Map
	targetHealthMap  sync.Map
	sessions         = make(map[string]time.Time)
	configDirty      int32

	loginAttempts = sync.Map{}
	blockUntil    = sync.Map{}

	wsUpgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	wsClients  = make(map[*websocket.Conn]bool)
	wsMu       sync.Mutex
)

// --- 数据库初始化与优化 ---

const dbSchema = `
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT
);
CREATE TABLE IF NOT EXISTS rules (
    id TEXT PRIMARY KEY,
    group_name TEXT, 
    note TEXT,
    entry_agent TEXT,
    entry_port TEXT,
    exit_agent TEXT,
    target_ip TEXT,
    target_port TEXT,
    protocol TEXT,
    bridge_port TEXT,
    traffic_limit INTEGER,
    disabled INTEGER,
    speed_limit INTEGER,
    disabled INTEGER,
    speed_limit INTEGER,
    total_tx INTEGER DEFAULT 0,
    total_rx INTEGER DEFAULT 0
);
CREATE TABLE IF NOT EXISTS logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    time TEXT,
    ip TEXT,
    action TEXT,
    msg TEXT
);`

// --- 基础工具函数 ---

func generateSalt() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func hashPassword(password, salt string) string {
	h := sha256.New()
	h.Write([]byte(salt + password))
	return hex.EncodeToString(h.Sum(nil))
}

func md5Hash(s string) string {
	h := md5.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

func checkLoginRateLimit(ip string) bool {
	if t, ok := blockUntil.Load(ip); ok {
		if time.Now().Before(t.(time.Time)) {
			return false
		}
		blockUntil.Delete(ip)
		loginAttempts.Delete(ip)
	}
	return true
}

func recordLoginFail(ip string) {
	v, _ := loginAttempts.LoadOrStore(ip, 0)
	count := v.(int) + 1
	loginAttempts.Store(ip, count)
	if count >= 5 {
		blockUntil.Store(ip, time.Now().Add(15*time.Minute))
	}
}

// --- 通用更新逻辑 (Master/Agent 共享) ---

func performSelfUpdate() error {
	arch := runtime.GOARCH
	osName := runtime.GOOS
	suffix := ""
	if osName == "linux" {
		suffix = "-linux-" + arch
	} else if osName == "darwin" {
		suffix = "-darwin-" + arch
	} else if osName == "windows" {
		suffix = "-windows-" + arch + ".exe"
	} else {
		return fmt.Errorf("不支持的操作系统")
	}

	targetURL := DownloadURL + suffix
	log.Printf("正在下载更新: %s", targetURL)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(targetURL)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("下载失败，状态码: %d", resp.StatusCode)
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("无法获取运行路径: %v", err)
	}

	tmpPath := exePath + ".new"
	out, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %v", err)
	}
	_, err = io.Copy(out, resp.Body)
	closeErr := out.Close()
	if err != nil {
		return fmt.Errorf("写入文件失败: %v", err)
	}
	if closeErr != nil {
		return fmt.Errorf("关闭临时文件失败: %v", closeErr)
	}

	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("设置可执行权限失败: %v", err)
	}

	oldPath := exePath + ".old"
	_ = os.Remove(oldPath)
	_ = os.Rename(exePath, oldPath) // Windows 下可能失败，后续覆盖时再处理
	if err := os.Rename(tmpPath, exePath); err != nil {
		os.Rename(oldPath, exePath) // 还原
		return fmt.Errorf("覆盖文件失败: %v", err)
	}
	return nil
}

// --- 主程序 ---

func main() {
	setRLimit()
	initPaths()
	mode := flag.String("mode", "master", "运行模式")
	name := flag.String("name", "", "Agent名称")
	connect := flag.String("connect", "", "Master地址")
	token := flag.String("token", "", "通信Token")
	serviceOp := flag.String("service", "", "install | uninstall")
	verFlag := flag.Bool("version", false, "显示版本号")
	flag.Parse()

	if *verFlag {
		fmt.Println("GoRelay " + AppVersion)
		return
	}

	if *serviceOp != "" {
		handleServiceOp(*serviceOp, *mode, *name, *connect, *token)
		return
	}

	setupSignalHandler()

	if *mode == "master" {
		initDB()
		loadConfig()
		runMaster()
	} else if *mode == "agent" {
		if *connect == "" {
			log.Fatal("Agent模式必须指定 -connect")
		}
		runAgent(*name, *connect, *token)
	} else {
		log.Fatal("未知模式")
	}
}

func initPaths() {
	base := os.Getenv("RELAY_DATA_DIR")
	if strings.TrimSpace(base) == "" {
		base = "/var/lib/relay"
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		if os.Getenv("RELAY_DATA_DIR") == "" {
			fallback := "data"
			if mkErr := os.MkdirAll(fallback, 0755); mkErr != nil {
				log.Fatalf("无法创建数据目录 %s (%v)，回退目录 %s 也创建失败: %v", base, err, fallback, mkErr)
			}
			log.Printf("⚠️ 无法使用默认数据目录 %s，已回退到 %s", base, fallback)
			base = fallback
		} else {
			log.Fatalf("无法创建数据目录 %s: %v", base, err)
		}
	}
	DataDir = base
	DBFile = filepath.Join(DataDir, "data.db")
	ConfigFile = filepath.Join(DataDir, "config.json")

}

func setRLimit() {
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		var rLimit syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rLimit); err == nil {
			rLimit.Cur = 1000000
			rLimit.Max = 1000000
			syscall.Setrlimit(syscall.RLIMIT_NOFILE, &rLimit)
		}
	}
}

func getSysStatus() string {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	memStr := fmt.Sprintf("Mem: %dMB", m.Alloc/1024/1024)
	cpuStr := fmt.Sprintf("Go: %d", runtime.NumGoroutine())
	if runtime.GOOS == "linux" {
		if data, err := os.ReadFile("/proc/loadavg"); err == nil {
			parts := strings.Fields(string(data))
			if len(parts) > 0 {
				cpuStr = "Load: " + parts[0]
			}
		}
	}
	return fmt.Sprintf("%s | %s", cpuStr, memStr)
}

func addLog(r *http.Request, action, msg string) {
	ip := "System"
	if r != nil {
		ip, _, _ = net.SplitHostPort(r.RemoteAddr)
		if f := r.Header.Get("X-Forwarded-For"); f != "" {
			ip = f
		}
	}
	now := time.Now().Format("01-02 15:04:05")
	_, _ = db.Exec("INSERT INTO logs (time, ip, action, msg) VALUES (?,?,?,?)", now, ip, action, msg)
}

func addSystemLog(ip, action, msg string) {
	now := time.Now().Format("01-02 15:04:05")
	_, _ = db.Exec("INSERT INTO logs (time, ip, action, msg) VALUES (?,?,?,?)", now, ip, action, msg)
}

// ================= MASTER =================

// ================= WEB HANDLERS =================

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	al := make([]AgentInfo, 0)
	for _, a := range agents {
		al = append(al, *a)
	}
	var totalTraffic int64
	for _, r := range rules {
		totalTraffic += (r.TotalTx + r.TotalRx)
	}
	displayRules := make([]LogicalRule, len(rules))
	copy(displayRules, rules)
	mu.Unlock()

	// 排序规则
	sort.Slice(displayRules, func(i, j int) bool {
		if displayRules[i].Group == displayRules[j].Group {
			return displayRules[i].ID < displayRules[j].ID
		}
		return displayRules[i].Group < displayRules[j].Group
	})

	var displayLogs []OpLog
	rows, err := db.Query("SELECT time, ip, action, msg FROM logs ORDER BY id DESC LIMIT ?", MaxLogEntries)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var l OpLog
			rows.Scan(&l.Time, &l.IP, &l.Action, &l.Msg)
			displayLogs = append(displayLogs, l)
		}
	}

	mu.Lock()
	conf := config
	mu.Unlock()

	// 准备端口列表给前端 (从配置中读取)
	pStr := conf.AgentPorts
	if pStr == "" {
		pStr = "9999"
	}
	cleanPorts := make([]string, 0)
	for _, p := range strings.Split(pStr, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			cleanPorts = append(cleanPorts, strings.TrimPrefix(p, ":"))
		}
	}

	data := struct {
		Agents       []AgentInfo
		Rules        []LogicalRule
		Logs         []OpLog
		Token        string
		User         string
		DownloadURL  string
		TotalTraffic int64
		MasterIP     string
		MasterIPv6   string
		MasterDomain string
		Config       AppConfig
		TwoFA        bool

		Ports   []string // 新增: 传递端口列表
		Version string
	}{al, displayRules, displayLogs, conf.AgentToken, conf.WebUser, DownloadURL, totalTraffic, conf.MasterIP, conf.MasterIPv6, conf.MasterDomain, conf, conf.TwoFAEnabled, cleanPorts, AppVersion}

	t := template.New("dash").Funcs(template.FuncMap{
		"formatBytes": formatBytes,
		"add":         func(a, b int64) int64 { return a + b },
		"percent": func(currTx, currRx, limit int64) float64 {
			if limit <= 0 {
				return 0
			}
			p := (float64(currTx+currRx) / float64(limit)) * 100
			if p > 100 {
				p = 100
			}
			return p
		},
		"formatSpeed": func(bytesPerSec int64) string {
			if bytesPerSec <= 0 {
				return "无限制"
			}
			return formatBytes(bytesPerSec) + "/s"
		},
	})
	// 增加 Cache-Control 防止浏览器缓存导致更新后 UI 不一致
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	t, parseErr := t.Parse(dashboardHtml)
	if parseErr != nil {
		http.Error(w, "Template Parse Error: "+parseErr.Error(), http.StatusInternalServerError)
		log.Printf("Template Parse Error: %v", parseErr)
		return
	}
	t.Execute(w, data)
}

func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		setup := config.IsSetup
		mu.Unlock()
		if !setup {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
		c, err := r.Cookie("sid")
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		mu.Lock()
		exp, ok := sessions[c.Value]
		mu.Unlock()
		if !ok || time.Now().After(exp) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		mu.Lock()
		config.WebUser = r.FormValue("username")
		salt := generateSalt()
		pwdHash := hashPassword(r.FormValue("password"), salt)
		config.WebPass = salt + "$" + pwdHash
		config.AgentToken = r.FormValue("token")
		config.IsSetup = true
		saveConfigNoLock()
		mu.Unlock()
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	t, _ := template.New("s").Parse(setupHtml)
	t.Execute(w, nil)
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		mu.Lock()
		isEnabled := config.TwoFAEnabled
		mu.Unlock()
		t, _ := template.New("l").Parse(loginHtml)
		t.Execute(w, map[string]interface{}{"TwoFA": isEnabled})
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !checkLoginRateLimit(ip) {
		http.Error(w, "尝试次数过多", 429)
		return
	}
	mu.Lock()
	u, storedVal := config.WebUser, config.WebPass
	twoFAEnabled := config.TwoFAEnabled
	twoFASecret := config.TwoFASecret
	mu.Unlock()

	passMatch := false
	parts := strings.Split(storedVal, "$")
	if len(parts) == 2 {
		if r.FormValue("username") == u && hashPassword(r.FormValue("password"), parts[0]) == parts[1] {
			passMatch = true
		}
	} else if r.FormValue("username") == u && md5Hash(r.FormValue("password")) == storedVal {
		passMatch = true
	}

	if !passMatch {
		recordLoginFail(ip)
		http.Redirect(w, r, "/login?err=1", http.StatusSeeOther)
		return
	}

	if twoFAEnabled {
		if !totp.Validate(r.FormValue("code"), twoFASecret) {
			recordLoginFail(ip)
			http.Redirect(w, r, "/login?err=2", http.StatusSeeOther)
			return
		}
	}

	sid := make([]byte, 16)
	rand.Read(sid)
	sidStr := hex.EncodeToString(sid)
	mu.Lock()
	sessions[sidStr] = time.Now().Add(12 * time.Hour)
	mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "sid", Value: sidStr, Path: "/", HttpOnly: true})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "sid", Value: "", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func handle2FAGenerate(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	u := config.WebUser
	mu.Unlock()
	key, _ := totp.Generate(totp.GenerateOpts{Issuer: "GoRelay-Pro", AccountName: u})
	var buf bytes.Buffer
	img, _ := qr.Encode(key.URL(), qr.M, qr.Auto)
	img, _ = barcode.Scale(img, 200, 200)
	png.Encode(&buf, img)
	resp := map[string]string{"secret": key.Secret(), "qr": "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())}
	json.NewEncoder(w).Encode(resp)
}

func handle2FAVerify(w http.ResponseWriter, r *http.Request) {
	var req struct{ Secret, Code string }
	json.NewDecoder(r.Body).Decode(&req)
	if totp.Validate(req.Code, req.Secret) {
		mu.Lock()
		config.TwoFASecret = req.Secret
		config.TwoFAEnabled = true
		saveConfigNoLock()
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]bool{"success": true})
	} else {
		json.NewEncoder(w).Encode(map[string]bool{"success": false})
	}
}

func handle2FADisable(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	config.TwoFAEnabled = false
	config.TwoFASecret = ""
	saveConfigNoLock()
	mu.Unlock()
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func handleAddRule(w http.ResponseWriter, r *http.Request) {
	limitGB, _ := strconv.ParseFloat(r.FormValue("traffic_limit"), 64)
	speedMB, _ := strconv.ParseFloat(r.FormValue("speed_limit"), 64)
	protocol := r.FormValue("protocol")

	// [还原] 移除手动指定 bridge_port，恢复随机生成
	finalBridgePort := fmt.Sprintf("%d", 20000+time.Now().UnixNano()%30000)

	mu.Lock()
	rules = append(rules, LogicalRule{
		ID:           fmt.Sprintf("%d", time.Now().UnixNano()),
		Group:        r.FormValue("group"),
		Note:         r.FormValue("note"),
		EntryAgent:   r.FormValue("entry_agent"),
		EntryPort:    r.FormValue("entry_port"),
		ExitAgent:    r.FormValue("exit_agent"),
		TargetIP:     r.FormValue("target_ip"),
		TargetPort:   r.FormValue("target_port"),
		Protocol:     protocol,
		TrafficLimit: int64(limitGB * 1024 * 1024 * 1024),
		SpeedLimit:   int64(speedMB * 1024 * 1024),
		BridgePort:   finalBridgePort,
	})
	saveConfigNoLock()
	mu.Unlock()
	requestPushConfig()
	http.Redirect(w, r, "/#rules", http.StatusSeeOther)
}

func handleEditRule(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	limitGB, _ := strconv.ParseFloat(r.FormValue("traffic_limit"), 64)
	speedMB, _ := strconv.ParseFloat(r.FormValue("speed_limit"), 64)
	protocol := r.FormValue("protocol")

	mu.Lock()
	for i := range rules {
		if rules[i].ID == id {
			rules[i].Group = r.FormValue("group")
			rules[i].Note = r.FormValue("note")
			rules[i].EntryAgent = r.FormValue("entry_agent")
			rules[i].EntryPort = r.FormValue("entry_port")
			rules[i].ExitAgent = r.FormValue("exit_agent")
			rules[i].TargetIP = r.FormValue("target_ip")
			rules[i].TargetPort = r.FormValue("target_port")
			rules[i].Protocol = protocol
			rules[i].TrafficLimit = int64(limitGB * 1024 * 1024 * 1024)
			rules[i].SpeedLimit = int64(speedMB * 1024 * 1024)
			break
		}
	}
	saveConfigNoLock()
	mu.Unlock()
	requestPushConfig()
	http.Redirect(w, r, "/#rules", http.StatusSeeOther)
}

func handleToggleRule(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	mu.Lock()
	for i := range rules {
		if rules[i].ID == id {
			rules[i].Disabled = !rules[i].Disabled
			break
		}
	}
	saveConfigNoLock()
	mu.Unlock()
	requestPushConfig()
	http.Redirect(w, r, "/#rules", http.StatusSeeOther)
}

func handleResetTraffic(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	mu.Lock()
	for i := range rules {
		if rules[i].ID == id {
			rules[i].TotalTx, rules[i].TotalRx = 0, 0
			break
		}
	}
	saveConfigNoLock()
	mu.Unlock()
	http.Redirect(w, r, "/#rules", http.StatusSeeOther)
}

func handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	mu.Lock()
	var nr []LogicalRule
	for _, x := range rules {
		if x.ID != id {
			nr = append(nr, x)
		}
	}
	rules = nr
	saveConfigNoLock()
	mu.Unlock()
	requestPushConfig()
	http.Redirect(w, r, "/#rules", http.StatusSeeOther)
}

func handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	mu.Lock()
	if a, ok := agents[name]; ok {
		_ = sendAgentMessage(a, Message{Type: "uninstall"})
	}
	mu.Unlock()
	http.Redirect(w, r, "/#dashboard", http.StatusSeeOther)
}

func handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	if p := r.FormValue("password"); p != "" {
		salt := generateSalt()
		config.WebPass = salt + "$" + hashPassword(p, salt)
	}
	config.AgentToken = r.FormValue("token")
	config.AgentPorts = r.FormValue("agent_ports") // 保存新添加的端口配置
	config.MasterIP = r.FormValue("master_ip")
	config.MasterIPv6 = r.FormValue("master_ipv6")
	config.MasterDomain = r.FormValue("master_domain")
	config.TgBotToken = r.FormValue("tg_bot_token")
	config.TgChatID = r.FormValue("tg_chat_id")
	saveConfigNoLock()
	mu.Unlock()
	http.Redirect(w, r, "/#settings", http.StatusSeeOther)
}

func handleDownloadConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Disposition", "attachment; filename=data.db")
	http.ServeFile(w, r, DBFile)
}

func handleExportLogs(w http.ResponseWriter, r *http.Request) {
	var logs []OpLog
	rows, err := db.Query("SELECT time, ip, action, msg FROM logs ORDER BY id DESC")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var l OpLog
			rows.Scan(&l.Time, &l.IP, &l.Action, &l.Msg)
			logs = append(logs, l)
		}
	}
	b, _ := json.MarshalIndent(logs, "", "  ")
	w.Header().Set("Content-Disposition", "attachment; filename=logs.json")
	w.Write(b)
}

func handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		return
	}
	w.Write([]byte("ok"))
	go func() {
		time.Sleep(500 * time.Millisecond)
		doRestart()
	}()
}

// Master自我更新处理
func handleUpdateSystem(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		return
	}
	if err := performSelfUpdate(); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	go func() { time.Sleep(1 * time.Second); doRestart() }()
}

// Master远程通知Agent更新
func handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	mu.Lock()
	agent, ok := agents[name]
	mu.Unlock()
	if !ok {
		http.Error(w, "Agent not found", 404)
		return
	}
	// 发送更新指令给Agent
	_ = sendAgentMessage(agent, Message{Type: "upgrade"})
	w.Write([]byte("ok"))
}

// [新增] 检查更新接口
func handleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(GithubLatestAPI)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"has_update": false, "error": err.Error()})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		json.NewEncoder(w).Encode(map[string]interface{}{"has_update": false})
		return
	}

	var data struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"has_update": false})
		return
	}
	if strings.TrimSpace(data.TagName) == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{"has_update": false, "current": AppVersion})
		return
	}

	// 简单对比版本号
	remoteVer := strings.TrimPrefix(data.TagName, "v")
	currentVer := strings.TrimPrefix(AppVersion, "v")

	hasUpdate := remoteVer != currentVer

	json.NewEncoder(w).Encode(map[string]interface{}{
		"has_update":     hasUpdate,
		"latest_version": data.TagName,
		"current":        AppVersion,
	})
}

func doRestart() {
	log.Println("🔄 接收到重启指令...")

	// [修改] 自动检测存在的服务名进行重启 (relay 或 gorelay)
	services := []string{"relay", "gorelay"}

	// 1. 尝试 Systemd
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		for _, s := range services {
			if _, err := os.Stat(fmt.Sprintf("/etc/systemd/system/%s.service", s)); err == nil {
				exec.Command("systemctl", "restart", s).Start()
				time.Sleep(1 * time.Second)
				os.Exit(0)
				return
			}
		}
	}

	// 2. 尝试 OpenRC
	if _, err := os.Stat("/etc/init.d"); err == nil {
		for _, s := range services {
			if _, err := os.Stat(fmt.Sprintf("/etc/init.d/%s", s)); err == nil {
				exec.Command("rc-service", s, "restart").Start()
				time.Sleep(1 * time.Second)
				os.Exit(0)
				return
			}
		}
	}

	// 3. 直接二进制重启 (Standalone/Manual)
	argv0, err := os.Executable()
	if err != nil {
		argv0 = os.Args[0]
	}
	os.Stdin = nil
	os.Stdout = nil
	os.Stderr = nil
	if runtime.GOOS == "windows" {
		os.Exit(0)
	} else {
		syscall.Exec(argv0, os.Args, os.Environ())
	}
}

// ================= DATA PERSISTENCE =================

func setupSignalHandler() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Println("📢 正在安全关闭服务...")
		mu.Lock()
		for _, a := range agents {
			a.Conn.Close()
		}
		// Agent 模式下 db 可能为 nil；saveConfigNoLock 内部会自动跳过。
		saveConfigNoLock()
		mu.Unlock()
		if db != nil {
			db.Close()
		}
		os.Exit(0)
	}()
}

func formatBytes(b int64) string {
	const u = 1024
	if b < u {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(u), 0
	for n := b / u; n >= u; n /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

const setupHtml = `<!DOCTYPE html>
<html lang="zh">
<head>
<title>初始化配置 - GoRelay Pro</title>
<meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no">
<link href="https://cdn.jsdelivr.net/npm/remixicon@3.5.0/fonts/remixicon.css" rel="stylesheet">
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet">
<style>
:root { --primary: #6366f1; --bg: #09090b; --card-bg: #18181b; --text: #fafafa; --text-sub: #a1a1aa; --border: #27272a; --input-bg: #27272a; }
body { background: var(--bg); color: var(--text); font-family: 'Inter', system-ui, sans-serif; display: flex; align-items: center; justify-content: center; min-height: 100vh; margin: 0; background-image: radial-gradient(circle at 50% -20%, #2e1065, transparent 40%); }
.card { background: var(--card-bg); padding: 40px; border-radius: 20px; box-shadow: 0 0 0 1px var(--border), 0 20px 40px -10px rgba(0,0,0,0.5); width: 100%; max-width: 380px; position: relative; overflow: hidden; }
.card::before { content: ""; position: absolute; top: 0; left: 0; right: 0; height: 1px; background: linear-gradient(90deg, transparent, rgba(99,102,241,0.5), transparent); }
h2 { text-align: center; margin: 0 0 8px 0; font-size: 24px; font-weight: 700; background: linear-gradient(135deg, #fff 0%, #a5b4fc 100%); -webkit-background-clip: text; -webkit-text-fill-color: transparent; }
p { text-align: center; color: var(--text-sub); margin-bottom: 32px; font-size: 13px; line-height: 1.6; }
.input-group { margin-bottom: 16px; position: relative; }
.input-group i { position: absolute; left: 14px; top: 50%; transform: translateY(-50%); color: var(--text-sub); transition: .2s; font-size: 18px; }
input { width: 100%; padding: 12px 14px 12px 42px; border: 1px solid var(--border); border-radius: 10px; background: var(--input-bg); color: var(--text); outline: none; transition: .2s; box-sizing: border-box; font-size: 14px; }
input:focus { border-color: var(--primary); background: #000; box-shadow: 0 0 0 2px rgba(99,102,241,0.2); }
input:focus + i { color: var(--primary); }
button { width: 100%; padding: 12px; background: var(--primary); color: #fff; border: none; border-radius: 10px; font-size: 14px; font-weight: 600; cursor: pointer; transition: .2s; margin-top: 10px; display: flex; align-items: center; justify-content: center; gap: 8px; }
button:hover { background: #4f46e5; }
</style>
</head>
<body>
<form class="card" method="POST">
    <h2>GoRelay Pro</h2>
    <p>欢迎使用，请配置初始管理员账户<br>并设置通信 Token 密钥</p>
    <div class="input-group"><input name="username" placeholder="管理员用户名" required autocomplete="off"><i class="ri-user-line"></i></div>
    <div class="input-group"><input type="password" name="password" placeholder="登录密码" required><i class="ri-lock-password-line"></i></div>
    <div class="input-group"><input name="token" placeholder="通信 Token (Agent 连接密钥)" required><i class="ri-key-2-line"></i></div>
    <button>完成初始化 <i class="ri-arrow-right-line"></i></button>
</form>
</body>
</html>`

const loginHtml = `<!DOCTYPE html>
<html lang="zh" data-theme="dark">
<head>
<title>登录 - GoRelay Pro</title>
<meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no">
<link href="https://cdn.jsdelivr.net/npm/remixicon@3.5.0/fonts/remixicon.css" rel="stylesheet">
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet">
<style>
:root { --primary: #6366f1; --bg: #09090b; --card-bg: #18181b; --text: #fafafa; --text-sub: #a1a1aa; --border: #27272a; --input-bg: #27272a; }
body { background: var(--bg); color: var(--text); font-family: 'Inter', system-ui, sans-serif; display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; overflow: hidden; position: relative; }
.bg-glow { position: absolute; width: 600px; height: 600px; background: radial-gradient(circle, rgba(99,102,241,0.15) 0%, transparent 70%); top: -20%; left: 50%; transform: translateX(-50%); opacity: 0.6; pointer-events: none; }

.card { background: rgba(24, 24, 27, 0.6); backdrop-filter: blur(20px); -webkit-backdrop-filter: blur(20px); padding: 48px 40px; border-radius: 24px; width: 100%; max-width: 340px; border: 1px solid rgba(255,255,255,0.08); box-shadow: 0 20px 50px -10px rgba(0, 0, 0, 0.5); position: relative; z-index: 10; }
.header { text-align: center; margin-bottom: 36px; }
.logo-icon { width: 56px; height: 56px; background: linear-gradient(135deg, #6366f1, #a855f7); border-radius: 16px; display: inline-flex; align-items: center; justify-content: center; font-size: 32px; color: white; box-shadow: 0 10px 20px -5px rgba(99,102,241,0.4); margin-bottom: 20px; }
.header h2 { margin: 0; font-size: 20px; font-weight: 600; color: var(--text); letter-spacing: -0.5px; }
.header p { margin: 6px 0 0; color: var(--text-sub); font-size: 13px; }

.input-box { margin-bottom: 16px; position: relative; }
.input-box i { position: absolute; left: 14px; top: 13px; color: var(--text-sub); font-size: 18px; transition: .2s; }
input { width: 100%; padding: 12px 14px 12px 44px; background: rgba(0, 0, 0, 0.2); border: 1px solid var(--border); border-radius: 12px; color: var(--text); font-size: 14px; outline: none; transition: .2s; box-sizing: border-box; }
input:focus { border-color: var(--primary); background: rgba(0,0,0,0.4); box-shadow: 0 0 0 2px rgba(99, 102, 241, 0.2); }
input:focus + i { color: var(--primary); }

button { width: 100%; padding: 12px; background: var(--primary); color: #fff; border: none; border-radius: 12px; font-size: 14px; font-weight: 500; cursor: pointer; transition: .2s; margin-top: 12px; display: flex; align-items: center; justify-content: center; gap: 8px; }
button:hover { background: #4f46e5; transform: translateY(-1px); }
.error-msg { background: rgba(239, 68, 68, 0.1); color: #ef4444; padding: 10px; border-radius: 8px; font-size: 12px; margin-bottom: 20px; text-align: center; border: 1px solid rgba(239, 68, 68, 0.2); display: flex; align-items: center; justify-content: center; gap: 6px; }
</style>
</head>
<body>
<div class="bg-glow"></div>
<form class="card" method="POST">
    <div class="header">
        <div class="logo-icon"><i class="ri-globe-line"></i></div>
        <h2>GoRelay Pro</h2>
        <p>安全内网穿透控制台</p>
    </div>
    {{if .Error}}<div class="error-msg"><i class="ri-error-warning-fill"></i> {{.Error}}</div>{{end}}
    
    <div class="input-box"><input name="username" placeholder="管理员账号" required autocomplete="off"><i class="ri-user-3-line"></i></div>
    <div class="input-box"><input type="password" name="password" placeholder="登录密码" required><i class="ri-lock-2-line"></i></div>
    {{if .TwoFA}}
    <div class="input-box"><input name="code" placeholder="2FA 动态验证码" required pattern="[0-9]{6}" maxlength="6" style="letter-spacing: 4px; text-align: center; padding-left: 14px; font-weight: 600; font-family: monospace"><i class="ri-shield-keyhole-line" style="left: auto; right: 14px;"></i></div>
    {{end}}
    <button>立即登录 <i class="ri-arrow-right-line"></i></button>
</form>
</body>
</html>`

//go:embed dashboard.html
var dashboardHtml string
