package main

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func handleServiceOp(op, mode, name, connect, token string) {
	if os.Geteuid() != 0 {
		log.Fatal("需 root 权限")
	}
	exe, _ := os.Executable()
	exe, _ = filepath.Abs(exe)

	svcName := "relay" // 默认为 Master 服务名
	if mode == "agent" {
		svcName = "gorelay" // Agent 服务名
	}

	args := fmt.Sprintf("-mode %s -name \"%s\" -connect \"%s\" -token \"%s\"", mode, name, connect, token)
	dataDir := os.Getenv("RELAY_DATA_DIR")
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "/var/lib/relay"
	}
	_ = os.MkdirAll(dataDir, 0755)
	isSys := false
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		isSys = true
	}
	isAlpine := false
	if _, err := os.Stat("/etc/alpine-release"); err == nil {
		isAlpine = true
	}

	if op == "install" {
		if isSys {
			c := fmt.Sprintf("[Unit]\nDescription=GoRelay Service (%s)\nAfter=network.target\n[Service]\nType=simple\nExecStart=%s %s\nEnvironment=RELAY_DATA_DIR=%s\nRestart=always\nUser=root\nLimitNOFILE=1000000\n[Install]\nWantedBy=multi-user.target", svcName, exe, args, dataDir)
			os.WriteFile(fmt.Sprintf("/etc/systemd/system/%s.service", svcName), []byte(c), 0644)
			exec.Command("systemctl", "enable", svcName).Run()
			exec.Command("systemctl", "restart", svcName).Run()
			log.Printf("Systemd 服务 %s 已安装", svcName)
		} else if isAlpine {
			c := fmt.Sprintf("#!/sbin/openrc-run\nname=\"%s\"\ncommand=\"/usr/bin/env\"\ncommand_args=\"RELAY_DATA_DIR=%s %s %s\"\ncommand_background=true\npidfile=\"/run/%s.pid\"\nrc_ulimit=\"-n 1000000\"\ndepend(){ need net; }", svcName, dataDir, exe, args, svcName)
			os.WriteFile(fmt.Sprintf("/etc/init.d/%s", svcName), []byte(c), 0755)
			exec.Command("rc-update", "add", svcName, "default").Run()
			exec.Command("rc-service", svcName, "restart").Run()
			log.Printf("OpenRC 服务 %s 已安装", svcName)
		} else {
			exec.Command("nohup", exe, args, "&").Start()
			log.Println("已通过 nohup 启动")
		}
	} else {
		if isSys {
			exec.Command("systemctl", "disable", svcName).Run()
			exec.Command("systemctl", "stop", svcName).Run()
			os.Remove(fmt.Sprintf("/etc/systemd/system/%s.service", svcName))
			exec.Command("systemctl", "daemon-reload").Run()
		}
		if isAlpine {
			exec.Command("rc-update", "del", svcName, "default").Run()
			exec.Command("rc-service", svcName, "stop").Run()
			os.Remove(fmt.Sprintf("/etc/init.d/%s", svcName))
		}
		log.Printf("服务 %s 已卸载", svcName)
	}
}

func doSelfUninstall() {
	log.Println("执行自毁程序...")

	services := []string{"relay", "gorelay"}

	if _, err := os.Stat("/run/systemd/system"); err == nil {
		for _, s := range services {
			if _, err := os.Stat(fmt.Sprintf("/etc/systemd/system/%s.service", s)); err == nil {
				exec.Command("systemctl", "disable", s).Run()
				exec.Command("systemctl", "stop", s).Run()
				os.Remove(fmt.Sprintf("/etc/systemd/system/%s.service", s))
			}
		}
		exec.Command("systemctl", "daemon-reload").Run()
	} else if _, err := os.Stat("/etc/alpine-release"); err == nil {
		for _, s := range services {
			if _, err := os.Stat(fmt.Sprintf("/etc/init.d/%s", s)); err == nil {
				exec.Command("rc-update", "del", s, "default").Run()
				exec.Command("rc-service", s, "stop").Run()
				os.Remove(fmt.Sprintf("/etc/init.d/%s", s))
			}
		}
	}

	exe, err := os.Executable()
	if err == nil {
		realPath, err := filepath.EvalSymlinks(exe)
		if err != nil {
			realPath = exe
		}
		absPath, _ := filepath.Abs(realPath)
		os.Remove(absPath)
	}
	os.Exit(0)
}

func sendTelegram(text string) {
	mu.Lock()
	token := config.TgBotToken
	chatID := config.TgChatID
	mu.Unlock()
	if token == "" || chatID == "" {
		return
	}
	api := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	data := url.Values{}
	data.Set("chat_id", chatID)
	data.Set("text", text)
	go func() { http.PostForm(api, data) }()
}
