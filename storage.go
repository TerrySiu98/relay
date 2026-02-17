package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"strconv"
)

func initDB() {
	var err error
	db, err = sql.Open("sqlite", DBFile)
	if err != nil {
		log.Fatalf("❌ 无法打开数据库文件: %v", err)
	}

	db.SetMaxOpenConns(1)
	db.Exec("PRAGMA journal_mode=WAL;")
	db.Exec("PRAGMA journal_size_limit = 10485760;")
	db.Exec("PRAGMA wal_autocheckpoint = 100;")
	db.Exec("PRAGMA synchronous = NORMAL;")

	if _, err := db.Exec(dbSchema); err != nil {
		log.Fatalf("❌ 初始化数据库表结构失败: %v", err)
	}

	_, _ = db.Exec("ALTER TABLE rules ADD COLUMN group_name TEXT DEFAULT ''")

	if _, err := os.Stat(ConfigFile); err == nil {
		var count int
		db.QueryRow("SELECT count(*) FROM settings").Scan(&count)
		if count == 0 {
			migrateOldData()
		}
	}
}

func migrateOldData() {
	log.Println("🚚 执行旧配置迁移...")
	data, err := os.ReadFile(ConfigFile)
	if err != nil {
		return
	}
	var old AppConfig
	if err := json.Unmarshal(data, &old); err != nil {
		return
	}

	setDBSetting := func(k, v string) { _, _ = db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?,?)", k, v) }
	setDBSetting("web_user", old.WebUser)
	setDBSetting("web_pass", old.WebPass)
	setDBSetting("agent_token", old.AgentToken)
	setDBSetting("agent_aliases", "{}")
	setDBSetting("agent_ports", old.AgentPorts)
	setDBSetting("master_ip", old.MasterIP)
	setDBSetting("master_ipv6", old.MasterIPv6)
	setDBSetting("master_domain", old.MasterDomain)
	setDBSetting("is_setup", strconv.FormatBool(old.IsSetup))
	setDBSetting("tg_bot_token", old.TgBotToken)
	setDBSetting("tg_chat_id", old.TgChatID)
	setDBSetting("two_fa_enabled", strconv.FormatBool(old.TwoFAEnabled))
	setDBSetting("two_fa_secret", old.TwoFASecret)

	for _, r := range old.Rules {
		disabled := 0
		if r.Disabled {
			disabled = 1
		}
		// entry_tls 默认为 0
		_, _ = db.Exec(`INSERT INTO rules (id, group_name, note, entry_agent, entry_port, exit_agent, target_ip, target_port, protocol, bridge_port, traffic_limit, disabled, speed_limit, total_tx, total_rx) 
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			r.ID, "", r.Note, r.EntryAgent, r.EntryPort, r.ExitAgent, r.TargetIP, r.TargetPort, r.Protocol, r.BridgePort, r.TrafficLimit, disabled, r.SpeedLimit, r.TotalTx, r.TotalRx)
	}
	_ = os.Rename(ConfigFile, ConfigFile+".bak")
}

func loadConfig() {
	mu.Lock()
	defer mu.Unlock()
	rows, err := db.Query("SELECT key, value FROM settings")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var k, v string
			rows.Scan(&k, &v)
			switch k {
			case "web_user":
				config.WebUser = v
			case "web_pass":
				config.WebPass = v
			case "agent_token":
				config.AgentToken = v
			case "agent_aliases":
				if v == "" {
					config.AgentAliases = map[string]string{}
				} else {
					var aliases map[string]string
					if err := json.Unmarshal([]byte(v), &aliases); err == nil {
						config.AgentAliases = aliases
					}
					if config.AgentAliases == nil {
						config.AgentAliases = map[string]string{}
					}
				}
			case "agent_ports":
				config.AgentPorts = v
			case "master_ip":
				config.MasterIP = v
			case "master_ipv6":
				config.MasterIPv6 = v
			case "master_domain":
				config.MasterDomain = v
			case "is_setup":
				config.IsSetup = (v == "true")
			case "tg_bot_token":
				config.TgBotToken = v
			case "tg_chat_id":
				config.TgChatID = v
			case "two_fa_enabled":
				config.TwoFAEnabled = (v == "true")
			case "two_fa_secret":
				config.TwoFASecret = v
			}
		}
	}

	rules = []LogicalRule{}
	rRows, err := db.Query("SELECT id, group_name, note, entry_agent, entry_port, exit_agent, target_ip, target_port, protocol, bridge_port, traffic_limit, disabled, speed_limit, total_tx, total_rx FROM rules")
	if err == nil {
		defer rRows.Close()
		for rRows.Next() {
			var r LogicalRule
			var d int
			rRows.Scan(&r.ID, &r.Group, &r.Note, &r.EntryAgent, &r.EntryPort, &r.ExitAgent, &r.TargetIP, &r.TargetPort, &r.Protocol, &r.BridgePort, &r.TrafficLimit, &d, &r.SpeedLimit, &r.TotalTx, &r.TotalRx)
			r.Disabled = (d == 1)
			rules = append(rules, r)
		}
	}
}

func saveConfig() {
	mu.Lock()
	defer mu.Unlock()
	saveConfigNoLock()
}

func saveConfigNoLock() {
	// Agent 模式或早期启动阶段可能未初始化 DB；此时无需保存。
	if db == nil {
		return
	}

	conf := config
	if conf.AgentAliases == nil {
		conf.AgentAliases = map[string]string{}
	}
	lRules := make([]LogicalRule, len(rules))
	copy(lRules, rules)

	tx, err := db.Begin()
	if err != nil {
		log.Printf("⚠️ 保存配置失败，无法开启事务: %v", err)
		return
	}
	defer tx.Rollback()
	setS := func(k, v string) error {
		_, err := tx.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?,?)", k, v)
		return err
	}
	if err := setS("web_user", conf.WebUser); err != nil {
		log.Printf("⚠️ 保存配置失败 settings.web_user: %v", err)
		return
	}
	if err := setS("web_pass", conf.WebPass); err != nil {
		log.Printf("⚠️ 保存配置失败 settings.web_pass: %v", err)
		return
	}
	if err := setS("agent_token", conf.AgentToken); err != nil {
		log.Printf("⚠️ 保存配置失败 settings.agent_token: %v", err)
		return
	}
	aliasRaw, _ := json.Marshal(conf.AgentAliases)
	if err := setS("agent_aliases", string(aliasRaw)); err != nil {
		log.Printf("⚠️ 保存配置失败 settings.agent_aliases: %v", err)
		return
	}
	if err := setS("agent_ports", conf.AgentPorts); err != nil {
		log.Printf("⚠️ 保存配置失败 settings.agent_ports: %v", err)
		return
	}
	if err := setS("master_ip", conf.MasterIP); err != nil {
		log.Printf("⚠️ 保存配置失败 settings.master_ip: %v", err)
		return
	}
	if err := setS("master_ipv6", conf.MasterIPv6); err != nil {
		log.Printf("⚠️ 保存配置失败 settings.master_ipv6: %v", err)
		return
	}
	if err := setS("master_domain", conf.MasterDomain); err != nil {
		log.Printf("⚠️ 保存配置失败 settings.master_domain: %v", err)
		return
	}
	if err := setS("is_setup", strconv.FormatBool(conf.IsSetup)); err != nil {
		log.Printf("⚠️ 保存配置失败 settings.is_setup: %v", err)
		return
	}
	if err := setS("tg_bot_token", conf.TgBotToken); err != nil {
		log.Printf("⚠️ 保存配置失败 settings.tg_bot_token: %v", err)
		return
	}
	if err := setS("tg_chat_id", conf.TgChatID); err != nil {
		log.Printf("⚠️ 保存配置失败 settings.tg_chat_id: %v", err)
		return
	}
	if err := setS("two_fa_enabled", strconv.FormatBool(conf.TwoFAEnabled)); err != nil {
		log.Printf("⚠️ 保存配置失败 settings.two_fa_enabled: %v", err)
		return
	}
	if err := setS("two_fa_secret", conf.TwoFASecret); err != nil {
		log.Printf("⚠️ 保存配置失败 settings.two_fa_secret: %v", err)
		return
	}

	if _, err := tx.Exec("DELETE FROM rules"); err != nil {
		log.Printf("⚠️ 保存配置失败 delete rules: %v", err)
		return
	}
	for _, r := range lRules {
		d := 0
		if r.Disabled {
			d = 1
		}
		_, err := tx.Exec(`INSERT INTO rules (id, group_name, note, entry_agent, entry_port, exit_agent, target_ip, target_port, protocol, bridge_port, traffic_limit, disabled, speed_limit, total_tx, total_rx) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			r.ID, r.Group, r.Note, r.EntryAgent, r.EntryPort, r.ExitAgent, r.TargetIP, r.TargetPort, r.Protocol, r.BridgePort, r.TrafficLimit, d, r.SpeedLimit, r.TotalTx, r.TotalRx)
		if err != nil {
			log.Printf("⚠️ 保存配置失败 insert rule %s: %v", r.ID, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		log.Printf("⚠️ 保存配置失败，提交事务出错: %v", err)
		return
	}
}

func cleanOldLogs() {
	_, err := db.Exec("DELETE FROM logs WHERE id NOT IN (SELECT id FROM logs ORDER BY id DESC LIMIT ?)", MaxLogRetention)
	if err != nil {
		log.Printf("⚠️ 清理日志失败: %v", err)
	}
}
