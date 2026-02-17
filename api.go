package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func handleAPIOverview(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	agentList := make([]AgentInfo, 0, len(agents))
	for _, a := range agents {
		agentList = append(agentList, *a)
	}
	ruleList := make([]LogicalRule, len(rules))
	copy(ruleList, rules)
	conf := config
	mu.Unlock()

	if conf.AgentAliases == nil {
		conf.AgentAliases = map[string]string{}
	}

	var totalTraffic int64
	for _, rr := range ruleList {
		totalTraffic += rr.TotalTx + rr.TotalRx
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"version":       AppVersion,
		"total_traffic": totalTraffic,
		"agents":        agentList,
		"rules":         ruleList,
		"settings": map[string]string{
			"master_ip":     conf.MasterIP,
			"master_ipv6":   conf.MasterIPv6,
			"master_domain": conf.MasterDomain,
			"agent_ports":   conf.AgentPorts,
		},
		"agent_aliases": conf.AgentAliases,
	})
}

func handleAPILogs(w http.ResponseWriter, r *http.Request) {
	limit := MaxLogEntries
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	rows, err := db.Query("SELECT time, ip, action, msg FROM logs ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	logs := make([]OpLog, 0)
	for rows.Next() {
		var l OpLog
		_ = rows.Scan(&l.Time, &l.IP, &l.Action, &l.Msg)
		logs = append(logs, l)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"logs": logs})
}

func handleAPISettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		mu.Lock()
		conf := config
		mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"master_ip":     conf.MasterIP,
			"master_ipv6":   conf.MasterIPv6,
			"master_domain": conf.MasterDomain,
			"agent_ports":   conf.AgentPorts,
			"download_url":  DownloadURL,
		})
		return
	}

	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req struct {
		Password     string `json:"password"`
		Token        string `json:"token"`
		AgentPorts   string `json:"agent_ports"`
		MasterIP     string `json:"master_ip"`
		MasterIPv6   string `json:"master_ipv6"`
		MasterDomain string `json:"master_domain"`
		TgBotToken   string `json:"tg_bot_token"`
		TgChatID     string `json:"tg_chat_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	mu.Lock()
	if req.Password != "" {
		salt := generateSalt()
		config.WebPass = salt + "$" + hashPassword(req.Password, salt)
	}
	if req.Token != "" {
		config.AgentToken = req.Token
	}
	if req.AgentPorts != "" {
		config.AgentPorts = req.AgentPorts
	}
	config.MasterIP = req.MasterIP
	config.MasterIPv6 = req.MasterIPv6
	config.MasterDomain = req.MasterDomain
	config.TgBotToken = req.TgBotToken
	config.TgChatID = req.TgChatID
	saveConfigNoLock()
	mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func handleAPIDeployCommand(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		name = "node-1"
	}
	addrType := strings.TrimSpace(r.URL.Query().Get("addr_type"))
	if addrType == "" {
		addrType = "domain"
	}
	arch := strings.TrimSpace(r.URL.Query().Get("arch"))
	if arch == "" {
		arch = "amd64"
	}
	port := strings.TrimSpace(r.URL.Query().Get("port"))

	mu.Lock()
	conf := config
	mu.Unlock()

	if port == "" {
		port = "9999"
		if conf.AgentPorts != "" {
			parts := strings.Split(conf.AgentPorts, ",")
			if len(parts) > 0 {
				port = strings.TrimPrefix(strings.TrimSpace(parts[0]), ":")
			}
		}
	}

	var host string
	switch addrType {
	case "v4":
		host = conf.MasterIP
	case "v6":
		if conf.MasterIPv6 != "" {
			host = "[" + conf.MasterIPv6 + "]"
		}
	default:
		host = conf.MasterDomain
	}
	if host == "" {
		host = r.Host
	}

	finalURL := DownloadURL + "-linux-" + arch
	cmd := fmt.Sprintf("curl -L -o /root/relay %s && chmod +x /root/relay && /root/relay -service install -mode agent -name %q -connect %q -token %q", finalURL, name, host+":"+port, conf.AgentToken)

	writeJSON(w, http.StatusOK, map[string]string{"command": cmd})
}

func validateRuleConfig(protocol string) error {
	if protocol != "tcp" && protocol != "udp" && protocol != "both" {
		return fmt.Errorf("invalid protocol")
	}
	return nil
}

func handleAPIRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		mu.Lock()
		list := make([]LogicalRule, len(rules))
		copy(list, rules)
		mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]interface{}{"rules": list})
		return
	case http.MethodPost:
		var req struct {
			Group        string  `json:"group"`
			Note         string  `json:"note"`
			EntryAgent   string  `json:"entry_agent"`
			EntryPort    string  `json:"entry_port"`
			ExitAgent    string  `json:"exit_agent"`
			TargetIP     string  `json:"target_ip"`
			TargetPort   string  `json:"target_port"`
			Protocol     string  `json:"protocol"`
			TrafficLimit float64 `json:"traffic_limit_gb"`
			SpeedLimit   float64 `json:"speed_limit_mb"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if req.Note == "" || req.EntryAgent == "" || req.EntryPort == "" || req.ExitAgent == "" || req.TargetIP == "" || req.TargetPort == "" || req.Protocol == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing required fields"})
			return
		}
		if err := validateRuleConfig(req.Protocol); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		newRule := LogicalRule{
			ID:           fmt.Sprintf("%d", time.Now().UnixNano()),
			Group:        req.Group,
			Note:         req.Note,
			EntryAgent:   req.EntryAgent,
			EntryPort:    req.EntryPort,
			ExitAgent:    req.ExitAgent,
			TargetIP:     req.TargetIP,
			TargetPort:   req.TargetPort,
			Protocol:     req.Protocol,
			BridgePort:   fmt.Sprintf("%d", 20000+time.Now().UnixNano()%30000),
			TrafficLimit: int64(req.TrafficLimit * 1024 * 1024 * 1024),
			SpeedLimit:   int64(req.SpeedLimit * 1024 * 1024),
		}

		mu.Lock()
		rules = append(rules, newRule)
		saveConfigNoLock()
		mu.Unlock()
		requestPushConfig()
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "rule": newRule})
		return
	case http.MethodPut:
		var req struct {
			ID           string  `json:"id"`
			Group        string  `json:"group"`
			Note         string  `json:"note"`
			EntryAgent   string  `json:"entry_agent"`
			EntryPort    string  `json:"entry_port"`
			ExitAgent    string  `json:"exit_agent"`
			TargetIP     string  `json:"target_ip"`
			TargetPort   string  `json:"target_port"`
			Protocol     string  `json:"protocol"`
			TrafficLimit float64 `json:"traffic_limit_gb"`
			SpeedLimit   float64 `json:"speed_limit_mb"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if req.ID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing id"})
			return
		}
		if err := validateRuleConfig(req.Protocol); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		mu.Lock()
		defer mu.Unlock()
		for i := range rules {
			if rules[i].ID != req.ID {
				continue
			}
			rules[i].Group = req.Group
			rules[i].Note = req.Note
			rules[i].EntryAgent = req.EntryAgent
			rules[i].EntryPort = req.EntryPort
			rules[i].ExitAgent = req.ExitAgent
			rules[i].TargetIP = req.TargetIP
			rules[i].TargetPort = req.TargetPort
			rules[i].Protocol = req.Protocol
			rules[i].TrafficLimit = int64(req.TrafficLimit * 1024 * 1024 * 1024)
			rules[i].SpeedLimit = int64(req.SpeedLimit * 1024 * 1024)
			saveConfigNoLock()
			requestPushConfig()
			writeJSON(w, http.StatusOK, map[string]bool{"success": true})
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "rule not found"})
		return
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
}

func handleAPIRulesBulk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		IDs      []string `json:"ids"`
		Action   string   `json:"action"`
		Disabled *bool    `json:"disabled"`
		Group    string   `json:"group"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if len(req.IDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ids required"})
		return
	}

	idSet := map[string]bool{}
	for _, id := range req.IDs {
		idSet[id] = true
	}

	affected := 0
	needPush := false
	mu.Lock()
	for i := range rules {
		if !idSet[rules[i].ID] {
			continue
		}
		switch req.Action {
		case "delete":
			// handled after loop
		case "toggle":
			rules[i].Disabled = !rules[i].Disabled
			needPush = true
		case "set_disabled":
			if req.Disabled != nil {
				rules[i].Disabled = *req.Disabled
				needPush = true
			}
		case "reset_traffic":
			rules[i].TotalTx, rules[i].TotalRx = 0, 0
		case "set_group":
			rules[i].Group = req.Group
		default:
			mu.Unlock()
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown action"})
			return
		}
		affected++
	}
	if req.Action == "delete" {
		nr := make([]LogicalRule, 0, len(rules))
		for _, rr := range rules {
			if !idSet[rr.ID] {
				nr = append(nr, rr)
			}
		}
		rules = nr
		needPush = true
	}
	saveConfigNoLock()
	mu.Unlock()
	if needPush {
		requestPushConfig()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "affected": affected})
}

func handleAPIRuleByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/rules/")
	if path == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing rule id"})
		return
	}
	id := strings.Split(path, "/")[0]

	switch r.Method {
	case http.MethodDelete:
		mu.Lock()
		nr := make([]LogicalRule, 0, len(rules))
		for _, rr := range rules {
			if rr.ID != id {
				nr = append(nr, rr)
			}
		}
		rules = nr
		saveConfigNoLock()
		mu.Unlock()
		requestPushConfig()
		writeJSON(w, http.StatusOK, map[string]bool{"success": true})
		return
	case http.MethodPost:
		action := r.URL.Query().Get("action")
		mu.Lock()
		defer mu.Unlock()
		for i := range rules {
			if rules[i].ID != id {
				continue
			}
			switch action {
			case "toggle":
				rules[i].Disabled = !rules[i].Disabled
			case "reset_traffic":
				rules[i].TotalTx, rules[i].TotalRx = 0, 0
			default:
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown action"})
				return
			}
			saveConfigNoLock()
			if action == "toggle" {
				requestPushConfig()
			}
			writeJSON(w, http.StatusOK, map[string]bool{"success": true})
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "rule not found"})
		return
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
}

func handleAPIAgentAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/agents/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid path"})
		return
	}
	name := parts[0]
	action := parts[1]

	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	mu.Lock()
	agent, ok := agents[name]
	mu.Unlock()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
		return
	}

	switch action {
	case "update":
		_ = sendAgentMessage(agent, Message{Type: "upgrade"})
		writeJSON(w, http.StatusOK, map[string]bool{"success": true})
	case "uninstall":
		_ = sendAgentMessage(agent, Message{Type: "uninstall"})
		writeJSON(w, http.StatusOK, map[string]bool{"success": true})
	case "alias":
		var req struct {
			Alias string `json:"alias"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		mu.Lock()
		if config.AgentAliases == nil {
			config.AgentAliases = map[string]string{}
		}
		if strings.TrimSpace(req.Alias) == "" {
			delete(config.AgentAliases, name)
		} else {
			config.AgentAliases[name] = strings.TrimSpace(req.Alias)
		}
		saveConfigNoLock()
		mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]bool{"success": true})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown action"})
	}
}

func handleAPIRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
	go func() {
		time.Sleep(500 * time.Millisecond)
		doRestart()
	}()
}

func handleAPIUpdateSystem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if err := performSelfUpdate(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
	go func() {
		time.Sleep(1 * time.Second)
		doRestart()
	}()
}
