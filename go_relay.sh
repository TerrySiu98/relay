#!/bin/bash

# =========================================================
#  Relay Manager - One-Click Installer & Management Tool
#  System: Debian/Ubuntu (Systemd) & Alpine (OpenRC)
#  Version: 2.0
# =========================================================

# --- 基础配置 ---
ARCH=$(uname -m)
case $ARCH in
    x86_64) ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    armv7l) ARCH="arm" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac
DOWNLOAD_URL="https://github.com/TerrySiu98/relay/releases/latest/download/relay_linux_${ARCH}"
BIN_PATH="/usr/local/bin/relay"
SERVICE_NAME="relay"
DATA_DIR="/usr/local/bin"  # data.db 所在目录
BACKUP_DIR="/root/relay_backup"

# --- 颜色与样式配置 ---
RED='\033[31m'
GREEN='\033[32m'
YELLOW='\033[33m'
BLUE='\033[34m'
CYAN='\033[36m'
BOLD='\033[1m'
PLAIN='\033[0m'

# 图标定义
ICON_SUCCESS="✅"
ICON_FAIL="❌"
ICON_WARN="⚠️"
ICON_INFO="ℹ️"
ICON_ROCKET="🚀"
ICON_TRASH="🗑️"
ICON_GLOBE="🌍"
ICON_UPDATE="🔄"
ICON_BACKUP="💾"
ICON_LOG="📋"

# --- UI 辅助函数 ---

clear_screen() {
    clear
}

print_line() {
    echo -e "${BLUE}————————————————————————————————————————————————————${PLAIN}"
}

print_logo() {
    clear_screen
    echo -e "${CYAN}${BOLD}"
    echo "    ____       __           "
    echo "   / __ \___  / /___ ___  __"
    echo "  / /_/ / _ \/ / __ \`/ / / /"
    echo " / _, _/  __/ / /_/ / /_/ / "
    echo "/_/ |_|\___/_/\__,_/\__, /  "
    echo "                   /____/   "
    echo -e "${PLAIN}"
    echo -e "   ${YELLOW}Relay 流量转发管理脚本 v2.0${PLAIN}"
    print_line
}

log_info() {
    echo -e "${BLUE}[${ICON_INFO}] ${PLAIN} $1"
}

log_success() {
    echo -e "${GREEN}[${ICON_SUCCESS}] ${PLAIN} $1"
}

log_error() {
    echo -e "${RED}[${ICON_FAIL}] ${PLAIN} $1"
}

log_warn() {
    echo -e "${YELLOW}[${ICON_WARN}] ${PLAIN} $1"
}

# --- 系统检查 ---

check_root() {
    if [ "$(id -u)" != "0" ]; then
        log_error "请使用 root 用户运行此脚本！"
        exit 1
    fi
}

check_dependencies() {
    if ! command -v wget >/dev/null; then
        log_info "正在安装必要组件 (wget)..."
        if [ -f /etc/alpine-release ]; then
            apk add --no-cache wget >/dev/null 2>&1
        elif [ -f /etc/debian_version ]; then
            apt-get update >/dev/null 2>&1 && apt-get install -y wget >/dev/null 2>&1
        fi
        log_success "组件安装完成"
    fi
}

# --- 获取服务状态 ---

get_service_status() {
    if [ -f /etc/alpine-release ]; then
        if rc-service $SERVICE_NAME status >/dev/null 2>&1; then
            echo "running"
        else
            echo "stopped"
        fi
    elif command -v systemctl >/dev/null; then
        if systemctl is-active --quiet $SERVICE_NAME 2>/dev/null; then
            echo "running"
        else
            echo "stopped"
        fi
    else
        echo "unknown"
    fi
}

# --- 核心功能 ---

install_relay() {
    print_logo
    echo -e "${BOLD}正在开始安装 Relay...${PLAIN}\n"
    
    check_dependencies

    # 1. 下载
    log_info "正在下载二进制文件..."
    wget -q -O "$BIN_PATH" "$DOWNLOAD_URL"
    if [ $? -ne 0 ]; then
        log_error "下载失败，请检查网络连接。"
        read -p "按回车键返回..."
        return
    fi
    chmod +x "$BIN_PATH"
    log_success "下载成功，已安装至: ${CYAN}$BIN_PATH${PLAIN}"

    # 2. 配置服务
    log_info "正在配置系统服务..."
    
    if [ -f /etc/alpine-release ]; then
        # Alpine OpenRC
        cat > /etc/init.d/$SERVICE_NAME <<EOF
#!/sbin/openrc-run
name="relay"
command="$BIN_PATH"
command_args="-mode master"
command_background=true
pidfile="/run/${SERVICE_NAME}.pid"

depend() {
    need net
    after firewall
}
EOF
        chmod +x /etc/init.d/$SERVICE_NAME
        rc-update add $SERVICE_NAME default >/dev/null 2>&1
        service $SERVICE_NAME restart >/dev/null 2>&1
        log_success "Alpine OpenRC 服务配置完成"

    elif command -v systemctl >/dev/null; then
        # Debian Systemd
        cat > /etc/systemd/system/${SERVICE_NAME}.service <<EOF
[Unit]
Description=Relay Master Service
After=network.target

[Service]
Type=simple
ExecStart=$BIN_PATH -mode master
Restart=always
User=root
WorkingDirectory=$DATA_DIR

[Install]
WantedBy=multi-user.target
EOF
        systemctl daemon-reload
        systemctl enable $SERVICE_NAME >/dev/null 2>&1
        systemctl restart $SERVICE_NAME
        log_success "Systemd 服务配置完成"
    else
        log_warn "未识别的初始化系统，仅完成了文件下载，未配置自启。"
    fi

    # 3. 获取 IP 地址
    log_info "正在检测服务器 IP 地址..."
    SERVER_IP=$(wget -qO- -t1 -T2 ipv4.icanhazip.com)
    if [ -z "$SERVER_IP" ]; then
        SERVER_IP=$(wget -qO- -t1 -T2 ifconfig.me)
    fi
    if [ -z "$SERVER_IP" ]; then
        SERVER_IP="[你的服务器IP]"
    fi

    echo ""
    print_line
    echo -e " ${ICON_ROCKET} ${GREEN}Relay 安装并启动成功！${PLAIN}"
    print_line
    echo -e " 运行状态: ${GREEN}Active${PLAIN}"
    echo -e " 程序路径: ${CYAN}$BIN_PATH${PLAIN}"
    echo -e " ${ICON_GLOBE} 访问地址: ${CYAN}${BOLD}http://${SERVER_IP}:8888${PLAIN}"
    print_line
    echo ""
    read -p "按回车键返回主菜单..."
}

# --- 更新功能 ---
update_relay() {
    print_logo
    echo -e "${BOLD}${ICON_UPDATE} 正在更新 Relay...${PLAIN}\n"
    
    # 检查是否已安装
    if [ ! -f "$BIN_PATH" ]; then
        log_error "Relay 尚未安装，请先安装！"
        read -p "按回车键返回..."
        return
    fi

    # 1. 自动备份数据库
    log_info "正在备份数据库..."
    if [ -f "$DATA_DIR/data.db" ]; then
        mkdir -p "$BACKUP_DIR"
        BACKUP_FILE="$BACKUP_DIR/data.db.$(date +%Y%m%d_%H%M%S).bak"
        cp "$DATA_DIR/data.db" "$BACKUP_FILE"
        log_success "数据库已备份至: ${CYAN}$BACKUP_FILE${PLAIN}"
    else
        log_warn "未找到数据库文件，跳过备份"
    fi

    # 2. 停止服务
    log_info "正在停止服务..."
    if [ -f /etc/alpine-release ]; then
        service $SERVICE_NAME stop >/dev/null 2>&1
    elif command -v systemctl >/dev/null; then
        systemctl stop $SERVICE_NAME >/dev/null 2>&1
    fi
    log_success "服务已停止"

    # 3. 下载新版本
    log_info "正在下载最新版本..."
    wget -q -O "${BIN_PATH}.new" "$DOWNLOAD_URL"
    if [ $? -ne 0 ]; then
        log_error "下载失败，正在恢复服务..."
        if [ -f /etc/alpine-release ]; then
            service $SERVICE_NAME start >/dev/null 2>&1
        elif command -v systemctl >/dev/null; then
            systemctl start $SERVICE_NAME >/dev/null 2>&1
        fi
        read -p "按回车键返回..."
        return
    fi

    # 4. 替换文件
    mv "${BIN_PATH}.new" "$BIN_PATH"
    chmod +x "$BIN_PATH"
    log_success "文件更新完成"

    # 5. 重启服务
    log_info "正在重启服务..."
    if [ -f /etc/alpine-release ]; then
        service $SERVICE_NAME start >/dev/null 2>&1
    elif command -v systemctl >/dev/null; then
        systemctl start $SERVICE_NAME >/dev/null 2>&1
    fi
    
    sleep 2
    STATUS=$(get_service_status)
    if [ "$STATUS" = "running" ]; then
        log_success "服务重启成功"
    else
        log_error "服务启动失败，请检查日志"
    fi

    echo ""
    print_line
    echo -e " ${ICON_SUCCESS} ${GREEN}Relay 更新完成！${PLAIN}"
    echo -e " 数据库: ${GREEN}已保留${PLAIN}"
    echo -e " 备份位置: ${CYAN}$BACKUP_FILE${PLAIN}"
    print_line
    echo ""
    read -p "按回车键返回主菜单..."
}

# --- 查看状态 ---
show_status() {
    print_logo
    echo -e "${BOLD}${ICON_INFO} Relay 服务状态${PLAIN}\n"
    
    # 检查是否已安装
    if [ ! -f "$BIN_PATH" ]; then
        log_error "Relay 尚未安装"
        read -p "按回车键返回..."
        return
    fi

    # 获取状态
    STATUS=$(get_service_status)
    
    # 获取 IP
    SERVER_IP=$(wget -qO- -t1 -T2 ipv4.icanhazip.com 2>/dev/null)
    if [ -z "$SERVER_IP" ]; then
        SERVER_IP="获取失败"
    fi

    # 检查数据库
    if [ -f "$DATA_DIR/data.db" ]; then
        DB_SIZE=$(du -h "$DATA_DIR/data.db" | cut -f1)
        DB_STATUS="${GREEN}存在 ($DB_SIZE)${PLAIN}"
    else
        DB_STATUS="${YELLOW}不存在${PLAIN}"
    fi

    print_line
    if [ "$STATUS" = "running" ]; then
        echo -e " 服务状态: ${GREEN}● 运行中${PLAIN}"
    else
        echo -e " 服务状态: ${RED}○ 已停止${PLAIN}"
    fi
    echo -e " 程序路径: ${CYAN}$BIN_PATH${PLAIN}"
    echo -e " 数据库:   $DB_STATUS"
    echo -e " 服务器IP: ${CYAN}$SERVER_IP${PLAIN}"
    if [ "$STATUS" = "running" ]; then
        echo -e " ${ICON_GLOBE} 访问地址: ${CYAN}${BOLD}http://${SERVER_IP}:8888${PLAIN}"
    fi
    print_line
    echo ""
    read -p "按回车键返回主菜单..."
}

# --- 重启服务 ---
restart_service() {
    print_logo
    echo -e "${BOLD}${ICON_UPDATE} 正在重启服务...${PLAIN}\n"
    
    if [ ! -f "$BIN_PATH" ]; then
        log_error "Relay 尚未安装"
        read -p "按回车键返回..."
        return
    fi

    if [ -f /etc/alpine-release ]; then
        service $SERVICE_NAME restart >/dev/null 2>&1
    elif command -v systemctl >/dev/null; then
        systemctl restart $SERVICE_NAME >/dev/null 2>&1
    fi
    
    sleep 2
    STATUS=$(get_service_status)
    if [ "$STATUS" = "running" ]; then
        log_success "服务重启成功"
    else
        log_error "服务启动失败"
    fi
    
    read -p "按回车键返回主菜单..."
}

# --- 查看日志 ---
view_logs() {
    print_logo
    echo -e "${BOLD}${ICON_LOG} 服务日志 (按 Ctrl+C 退出)${PLAIN}\n"
    print_line
    
    if [ -f /etc/alpine-release ]; then
        # Alpine 使用 tail 查看日志
        if [ -f /var/log/messages ]; then
            tail -f /var/log/messages | grep -i relay
        else
            log_warn "未找到日志文件"
            read -p "按回车键返回..."
        fi
    elif command -v systemctl >/dev/null; then
        journalctl -u $SERVICE_NAME -f --no-pager
    else
        log_warn "无法查看日志"
        read -p "按回车键返回..."
    fi
}

# --- 备份数据 ---
backup_data() {
    print_logo
    echo -e "${BOLD}${ICON_BACKUP} 备份数据库${PLAIN}\n"
    
    if [ ! -f "$DATA_DIR/data.db" ]; then
        log_error "未找到数据库文件"
        read -p "按回车键返回..."
        return
    fi

    mkdir -p "$BACKUP_DIR"
    BACKUP_FILE="$BACKUP_DIR/data.db.$(date +%Y%m%d_%H%M%S).bak"
    
    cp "$DATA_DIR/data.db" "$BACKUP_FILE"
    if [ $? -eq 0 ]; then
        log_success "备份成功！"
        echo ""
        print_line
        echo -e " 备份文件: ${CYAN}$BACKUP_FILE${PLAIN}"
        echo -e " 文件大小: ${CYAN}$(du -h $BACKUP_FILE | cut -f1)${PLAIN}"
        print_line
        
        # 显示所有备份
        echo ""
        echo -e "${BOLD}现有备份列表:${PLAIN}"
        ls -lh "$BACKUP_DIR"/*.bak 2>/dev/null | awk '{print "  " $9 " (" $5 ")"}'
    else
        log_error "备份失败"
    fi
    
    echo ""
    read -p "按回车键返回主菜单..."
}

# --- 恢复数据 ---
restore_data() {
    print_logo
    echo -e "${BOLD}${ICON_BACKUP} 恢复数据库${PLAIN}\n"
    
    # 检查备份目录
    if [ ! -d "$BACKUP_DIR" ] || [ -z "$(ls -A $BACKUP_DIR/*.bak 2>/dev/null)" ]; then
        log_error "未找到任何备份文件"
        read -p "按回车键返回..."
        return
    fi

    echo -e "${BOLD}可用的备份文件:${PLAIN}"
    echo ""
    
    # 列出备份文件
    i=1
    declare -a backups
    for f in $(ls -t "$BACKUP_DIR"/*.bak 2>/dev/null); do
        backups[$i]="$f"
        SIZE=$(du -h "$f" | cut -f1)
        DATE=$(basename "$f" | sed 's/data.db.\(.*\).bak/\1/' | sed 's/_/ /')
        echo -e " ${GREEN}$i.${PLAIN} $DATE ($SIZE)"
        ((i++))
    done
    
    echo ""
    echo -e " ${GREEN}0.${PLAIN} 返回主菜单"
    echo ""
    read -p " 请选择要恢复的备份 [0-$((i-1))]: " choice
    
    if [ "$choice" = "0" ] || [ -z "$choice" ]; then
        return
    fi
    
    if [ -z "${backups[$choice]}" ]; then
        log_error "无效选择"
        read -p "按回车键返回..."
        return
    fi

    RESTORE_FILE="${backups[$choice]}"
    
    echo ""
    log_warn "恢复将覆盖当前数据库，此操作不可逆！"
    read -p "确认恢复? (y/n): " confirm
    
    if [ "$confirm" != "y" ] && [ "$confirm" != "Y" ]; then
        log_info "已取消恢复"
        read -p "按回车键返回..."
        return
    fi

    # 停止服务
    log_info "正在停止服务..."
    if [ -f /etc/alpine-release ]; then
        service $SERVICE_NAME stop >/dev/null 2>&1
    elif command -v systemctl >/dev/null; then
        systemctl stop $SERVICE_NAME >/dev/null 2>&1
    fi

    # 恢复数据库
    cp "$RESTORE_FILE" "$DATA_DIR/data.db"
    if [ $? -eq 0 ]; then
        log_success "数据库恢复成功"
    else
        log_error "恢复失败"
    fi

    # 重启服务
    log_info "正在重启服务..."
    if [ -f /etc/alpine-release ]; then
        service $SERVICE_NAME start >/dev/null 2>&1
    elif command -v systemctl >/dev/null; then
        systemctl start $SERVICE_NAME >/dev/null 2>&1
    fi
    
    log_success "服务已重启"
    read -p "按回车键返回主菜单..."
}

# --- 卸载 ---
uninstall_relay() {
    print_logo
    echo -e "${BOLD}正在卸载 Relay...${PLAIN}\n"

    # 询问是否保留数据
    read -p "是否保留数据库备份? (y/n): " keep_data
    
    if [ "$keep_data" = "y" ] || [ "$keep_data" = "Y" ]; then
        if [ -f "$DATA_DIR/data.db" ]; then
            mkdir -p "$BACKUP_DIR"
            cp "$DATA_DIR/data.db" "$BACKUP_DIR/data.db.uninstall.bak"
            log_success "数据库已备份至: ${CYAN}$BACKUP_DIR/data.db.uninstall.bak${PLAIN}"
        fi
    fi

    # 停止并删除服务
    if [ -f /etc/alpine-release ]; then
        if [ -f /etc/init.d/$SERVICE_NAME ]; then
            service $SERVICE_NAME stop >/dev/null 2>&1
            rc-update del $SERVICE_NAME default >/dev/null 2>&1
            rm -f /etc/init.d/$SERVICE_NAME
            log_success "服务已停止并移除 (OpenRC)"
        fi
    elif command -v systemctl >/dev/null; then
        if [ -f /etc/systemd/system/${SERVICE_NAME}.service ]; then
            systemctl stop $SERVICE_NAME >/dev/null 2>&1
            systemctl disable $SERVICE_NAME >/dev/null 2>&1
            rm -f /etc/systemd/system/${SERVICE_NAME}.service
            systemctl daemon-reload
            log_success "服务已停止并移除 (Systemd)"
        fi
    fi

    # 删除文件
    if [ -f "$BIN_PATH" ]; then
        rm -f "$BIN_PATH"
        log_success "二进制文件已删除"
    else
        log_warn "未找到二进制文件 (可能已被删除)"
    fi
    
    # 删除数据库
    if [ -f "$DATA_DIR/data.db" ]; then
        rm -f "$DATA_DIR/data.db"
        rm -f "$DATA_DIR/data.db-wal" 2>/dev/null
        rm -f "$DATA_DIR/data.db-shm" 2>/dev/null
        log_success "数据库文件已删除"
    fi

    echo ""
    print_line
    echo -e " ${ICON_TRASH} ${GREEN}Relay 已彻底卸载。${PLAIN}"
    if [ "$keep_data" = "y" ] || [ "$keep_data" = "Y" ]; then
        echo -e " 备份位置: ${CYAN}$BACKUP_DIR${PLAIN}"
    fi
    print_line
    echo ""
    read -p "按回车键返回主菜单..."
}

# --- 菜单系统 ---

show_menu() {
    check_root
    while true; do
        print_logo
        
        # 显示当前状态
        STATUS=$(get_service_status)
        if [ "$STATUS" = "running" ]; then
            echo -e " 当前状态: ${GREEN}● 运行中${PLAIN}"
        elif [ -f "$BIN_PATH" ]; then
            echo -e " 当前状态: ${RED}○ 已停止${PLAIN}"
        else
            echo -e " 当前状态: ${YELLOW}○ 未安装${PLAIN}"
        fi
        echo ""
        
        echo -e " ${GREEN}1.${PLAIN} 安装 Relay     ${YELLOW}(Install)${PLAIN}"
        echo -e " ${GREEN}2.${PLAIN} 更新 Relay     ${YELLOW}(Update)${PLAIN}"
        echo -e " ${GREEN}3.${PLAIN} 查看状态       ${YELLOW}(Status)${PLAIN}"
        echo -e " ${GREEN}4.${PLAIN} 重启服务       ${YELLOW}(Restart)${PLAIN}"
        echo -e " ${GREEN}5.${PLAIN} 查看日志       ${YELLOW}(Logs)${PLAIN}"
        echo -e " ${GREEN}6.${PLAIN} 备份数据       ${YELLOW}(Backup)${PLAIN}"
        echo -e " ${GREEN}7.${PLAIN} 恢复数据       ${YELLOW}(Restore)${PLAIN}"
        echo -e " ${GREEN}8.${PLAIN} 卸载 Relay     ${YELLOW}(Uninstall)${PLAIN}"
        echo -e " ${GREEN}0.${PLAIN} 退出脚本       ${YELLOW}(Exit)${PLAIN}"
        echo ""
        print_line
        echo -e "${CYAN}提示: 根据系统自动识别 Systemd 或 OpenRC${PLAIN}"
        echo ""
        read -p " 请输入选项 [0-8]: " choice
        
        case "$choice" in
            1) install_relay ;;
            2) update_relay ;;
            3) show_status ;;
            4) restart_service ;;
            5) view_logs ;;
            6) backup_data ;;
            7) restore_data ;;
            8) uninstall_relay ;;
            0) exit 0 ;;
            *) echo -e "\n${RED}输入无效，请重新输入...${PLAIN}"; sleep 1 ;;
        esac
    done
}

# --- 入口处理 ---

if [ "$1" == "install" ]; then
    check_root
    install_relay
    exit 0
elif [ "$1" == "uninstall" ]; then
    check_root
    uninstall_relay
    exit 0
elif [ "$1" == "update" ]; then
    check_root
    update_relay
    exit 0
elif [ "$1" == "status" ]; then
    check_root
    show_status
    exit 0
elif [ "$1" == "restart" ]; then
    check_root
    restart_service
    exit 0
elif [ "$1" == "backup" ]; then
    check_root
    backup_data
    exit 0
elif [ "$1" == "restore" ]; then
    check_root
    restore_data
    exit 0
else
    show_menu
fi
