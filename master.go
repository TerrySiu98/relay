package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	pushDebounceMu sync.Mutex
	pushDebounceT  *time.Timer
)

func requestPushConfig() {
	pushDebounceMu.Lock()
	defer pushDebounceMu.Unlock()
	if pushDebounceT != nil {
		pushDebounceT.Stop()
	}
	pushDebounceT = time.AfterFunc(300*time.Millisecond, func() {
		pushConfigToAll()
	})
}

func sendAgentMessage(agent *AgentInfo, msg Message) error {
	if agent == nil || agent.Conn == nil {
		return errors.New("agent connection is nil")
	}
	if agent.SendMu == nil {
		agent.SendMu = &sync.Mutex{}
	}
	agent.SendMu.Lock()
	defer agent.SendMu.Unlock()

	_ = agent.Conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	err := json.NewEncoder(agent.Conn).Encode(msg)
	_ = agent.Conn.SetWriteDeadline(time.Time{})
	if err != nil {
		log.Printf("⚠️ 发送控制消息失败 agent=%s type=%s err=%v", agent.Name, msg.Type, err)
	}
	return err
}

func runMaster() {

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		for range ticker.C {
			if atomic.CompareAndSwapInt32(&configDirty, 1, 0) {
				saveConfig()
			}
			cleanOldLogs()
			db.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
		}
	}()
	go broadcastLoop()
	go func() {
		// 移除自动 TLS 判断逻辑
		log.Println("⚠️ Master 已启用 TCP 模式")

		portsStr := config.AgentPorts
		if portsStr == "" {
			portsStr = "9999"
		}
		ports := strings.Split(portsStr, ",")

		for _, pStr := range ports {
			pStr = strings.TrimSpace(pStr)
			if pStr == "" {
				continue
			}
			if !strings.Contains(pStr, ":") {
				pStr = ":" + pStr
			}

			go func(p string) {
				var ln net.Listener
				var err error

				ln, err = net.Listen("tcp", p)

				if err != nil {
					log.Printf("❌ 监听端口 %s 失败: %v", p, err)
					return
				}
				log.Printf("✅ Agent 监听端口启动: %s", p)

				for {
					c, err := ln.Accept()
					if err == nil {
						go handleAgentConn(c)
					}
				}
			}(pStr)
		}
	}()

	http.HandleFunc("/legacy", authMiddleware(handleDashboard))
	http.HandleFunc("/api/v1/overview", authMiddleware(handleAPIOverview))
	http.HandleFunc("/api/v1/logs", authMiddleware(handleAPILogs))
	http.HandleFunc("/api/v1/settings", authMiddleware(handleAPISettings))
	http.HandleFunc("/api/v1/deploy/command", authMiddleware(handleAPIDeployCommand))
	http.HandleFunc("/api/v1/rules", authMiddleware(handleAPIRules))
	http.HandleFunc("/api/v1/rules/bulk", authMiddleware(handleAPIRulesBulk))
	http.HandleFunc("/api/v1/rules/", authMiddleware(handleAPIRuleByID))
	http.HandleFunc("/api/v1/agents/", authMiddleware(handleAPIAgentAction))
	http.HandleFunc("/api/v1/restart", authMiddleware(handleAPIRestart))
	http.HandleFunc("/api/v1/update_sys", authMiddleware(handleAPIUpdateSystem))
	http.HandleFunc("/ws", authMiddleware(handleWS))
	http.HandleFunc("/login", handleLogin)
	http.HandleFunc("/logout", handleLogout)
	http.HandleFunc("/setup", handleSetup)
	http.HandleFunc("/add", authMiddleware(handleAddRule))
	http.HandleFunc("/edit", authMiddleware(handleEditRule))
	http.HandleFunc("/delete", authMiddleware(handleDeleteRule))
	http.HandleFunc("/toggle", authMiddleware(handleToggleRule))
	http.HandleFunc("/reset_traffic", authMiddleware(handleResetTraffic))
	http.HandleFunc("/delete_agent", authMiddleware(handleDeleteAgent))
	http.HandleFunc("/update_settings", authMiddleware(handleUpdateSettings))
	http.HandleFunc("/download_config", authMiddleware(handleDownloadConfig))
	http.HandleFunc("/export_logs", authMiddleware(handleExportLogs))
	http.HandleFunc("/2fa/generate", authMiddleware(handle2FAGenerate))
	http.HandleFunc("/2fa/verify", authMiddleware(handle2FAVerify))
	http.HandleFunc("/2fa/disable", authMiddleware(handle2FADisable))
	http.HandleFunc("/restart", authMiddleware(handleRestart))
	http.HandleFunc("/update_sys", authMiddleware(handleUpdateSystem))
	http.HandleFunc("/update_agent", authMiddleware(handleUpdateAgent))
	http.HandleFunc("/check_update", authMiddleware(handleCheckUpdate))
	http.HandleFunc("/", authMiddleware(handleDashboard))

	log.Printf("面板启动: http://localhost%s", WebPort)
	log.Fatal(http.ListenAndServe(WebPort, nil))
}

func handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	wsMu.Lock()
	wsClients[conn] = true
	wsMu.Unlock()
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			wsMu.Lock()
			delete(wsClients, conn)
			wsMu.Unlock()
			conn.Close()
			break
		}
	}
}

func broadcastLoop() {
	ticker := time.NewTicker(1 * time.Second)
	var lastTotalTx int64
	var lastTotalRx int64

	for range ticker.C {
		mu.Lock()
		var currentTx, currentRx int64
		var agentData []AgentStatusData
		var ruleData []RuleStatusData

		for _, a := range agents {
			agentData = append(agentData, AgentStatusData{Name: a.Name, SysStatus: a.SysStatus})
		}
		for _, r := range rules {
			currentTx += r.TotalTx
			currentRx += r.TotalRx
			ruleData = append(ruleData, RuleStatusData{
				ID:        r.ID,
				Name:      r.Note,
				Total:     r.TotalTx + r.TotalRx,
				Tx:        r.TotalTx,
				Rx:        r.TotalRx,
				UserCount: r.UserCount,
				Limit:     r.TrafficLimit,
				Status:    r.TargetStatus,
				Latency:   r.TargetLatency,
			})
		}
		mu.Unlock()

		var logData []OpLog
		lRows, err := db.Query("SELECT time, ip, action, msg FROM logs ORDER BY id DESC LIMIT 15")
		if err == nil {
			for lRows.Next() {
				var l OpLog
				lRows.Scan(&l.Time, &l.IP, &l.Action, &l.Msg)
				logData = append(logData, l)
			}
			lRows.Close()
		}

		speedTx := int64(0)
		speedRx := int64(0)
		if lastTotalTx != 0 || lastTotalRx != 0 {
			speedTx = currentTx - lastTotalTx
			speedRx = currentRx - lastTotalRx
		}
		if speedTx < 0 {
			speedTx = 0
		}
		if speedRx < 0 {
			speedRx = 0
		}
		lastTotalTx = currentTx
		lastTotalRx = currentRx

		wsMu.Lock()
		if len(wsClients) == 0 {
			wsMu.Unlock()
			continue
		}
		msg := WSMessage{Type: "stats", Data: WSDashboardData{TotalTraffic: currentTx + currentRx, SpeedTx: speedTx, SpeedRx: speedRx, Agents: agentData, Rules: ruleData, Logs: logData}}
		for client := range wsClients {
			if err := client.WriteJSON(msg); err != nil {
				client.Close()
				delete(wsClients, client)
			}
		}
		wsMu.Unlock()
	}
}

func handleAgentConn(conn net.Conn) {
	defer conn.Close()
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(30 * time.Second)
	}
	dec := json.NewDecoder(conn)
	var msg Message
	if err := dec.Decode(&msg); err != nil || msg.Type != "auth" {
		return
	}

	data, ok := msg.Payload.(map[string]interface{})
	if !ok {
		return
	}
	reqToken, _ := data["token"].(string)
	name, _ := data["name"].(string)

	mu.Lock()
	tk := config.AgentToken
	mu.Unlock()
	if reqToken != tk || name == "" {
		return
	}

	remoteIP, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
	mu.Lock()
	if old, exists := agents[name]; exists {
		old.Conn.Close()
	}
	agents[name] = &AgentInfo{Name: name, RemoteIP: remoteIP, Conn: conn, SendMu: &sync.Mutex{}}
	mu.Unlock()
	log.Printf("Agent上线: %s", name)
	addSystemLog(remoteIP, "Agent 上线", fmt.Sprintf("节点 %s 已连接", name))
	sendTelegram(fmt.Sprintf("🟢 节点上线通知\n名称: %s", name))
	requestPushConfig()

	for {
		var m Message
		if err := dec.Decode(&m); err != nil {
			log.Printf("⚠️ Agent 控制连接断开 name=%s ip=%s err=%v", name, remoteIP, err)
			break
		}
		if m.Type == "stats" {
			handleStatsReport(m.Payload)
		}
		if m.Type == "health" {
			handleHealthReport(m.Payload)
		}
		if m.Type == "ping" {
			if status, ok := m.Payload.(string); ok {
				mu.Lock()
				if agent, exists := agents[name]; exists {
					agent.SysStatus = status
				}
				mu.Unlock()
			}
		}
	}
	mu.Lock()
	if curr, ok := agents[name]; ok && curr.Conn == conn {
		delete(agents, name)
		mu.Unlock()
		sendTelegram(fmt.Sprintf("🔴 节点下线通知\n名称: %s", name))
	} else {
		mu.Unlock()
	}
}

func handleStatsReport(payload interface{}) {
	d, _ := json.Marshal(payload)
	var reports []TrafficReport
	json.Unmarshal(d, &reports)

	mu.Lock()
	defer mu.Unlock()
	limitTriggered := false
	for _, rep := range reports {
		if strings.HasSuffix(rep.TaskID, "_entry") {
			rid := strings.TrimSuffix(rep.TaskID, "_entry")
			for i := range rules {
				if rules[i].ID == rid {
					rules[i].TotalTx += rep.TxDelta
					rules[i].TotalRx += rep.RxDelta
					rules[i].UserCount = rep.UserCount
					atomic.StoreInt32(&configDirty, 1)
					if rules[i].TrafficLimit > 0 && (rules[i].TotalTx+rules[i].TotalRx) >= rules[i].TrafficLimit {
						limitTriggered = true
					}
					break
				}
			}
		}
	}
	if limitTriggered {
		requestPushConfig()
	}
}

func handleHealthReport(payload interface{}) {
	d, _ := json.Marshal(payload)
	var reports []HealthReport
	json.Unmarshal(d, &reports)
	mu.Lock()
	defer mu.Unlock()
	for _, rep := range reports {
		if strings.HasSuffix(rep.TaskID, "_exit") {
			rid := strings.TrimSuffix(rep.TaskID, "_exit")
			for i := range rules {
				if rules[i].ID == rid {
					rules[i].TargetStatus = (rep.Latency >= 0)
					rules[i].TargetLatency = rep.Latency
					break
				}
			}
		}
	}
}

func pushConfigToAll() {
	mu.Lock()
	tasksMap := make(map[string][]ForwardTask)
	for _, r := range rules {
		if r.Disabled {
			continue
		}
		if r.TrafficLimit > 0 && (r.TotalTx+r.TotalRx) >= r.TrafficLimit {
			continue
		}
		rawIPs := strings.Split(r.TargetIP, ",")
		var targetList []string
		for _, ip := range rawIPs {
			ip = strings.TrimSpace(ip)
			if ip != "" {
				targetList = append(targetList, fmt.Sprintf("%s:%s", ip, r.TargetPort))
			}
		}
		finalTargetStr := strings.Join(targetList, ",")
		tasksMap[r.ExitAgent] = append(tasksMap[r.ExitAgent], ForwardTask{ID: r.ID + "_exit", Protocol: r.Protocol, Listen: ":" + r.BridgePort, Target: finalTargetStr, SpeedLimit: r.SpeedLimit})
		if exit, ok := agents[r.ExitAgent]; ok {
			rip := exit.RemoteIP
			if r.EntryAgent == r.ExitAgent {
				// 同机入口/出口时优先走回环，避免公网回环/NAT 发夹导致偶发不通。
				rip = "127.0.0.1"
			}
			if strings.Contains(rip, ":") && !strings.Contains(rip, "[") {
				rip = "[" + rip + "]"
			}
			tasksMap[r.EntryAgent] = append(tasksMap[r.EntryAgent], ForwardTask{ID: r.ID + "_entry", Protocol: r.Protocol, Listen: ":" + r.EntryPort, Target: fmt.Sprintf("%s:%s", rip, r.BridgePort), SpeedLimit: r.SpeedLimit})
		}
	}
	activeAgents := make(map[string]*AgentInfo)
	for k, v := range agents {
		activeAgents[k] = v
	}
	mu.Unlock()
	for n, a := range activeAgents {
		t := tasksMap[n]
		if t == nil {
			t = []ForwardTask{}
		}
		go func(agent *AgentInfo, tasks []ForwardTask) {
			_ = sendAgentMessage(agent, Message{Type: "update", Payload: tasks})
		}(a, t)
	}
}
