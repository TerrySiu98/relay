# 🚀 GoRelay Pro - 高性能分布式流量转发管理系统

GoRelay Pro 是一个基于 Go 语言开发的轻量级、高性能多节点流量转发与中转管理平台。采用 **Master-Agent（主控-被控）** 分布式架构，允许用户通过一个中心化的 Web 仪表盘，轻松管理分散在全球各地的服务器节点，构建灵活的 TCP/UDP 转发链路。

---

## ✨ 核心功能特性

### 🖥️ 现代化实时仪表盘
- **毫秒级流量监控**：集成 WebSocket + Chart.js，提供动态波形图实时展示全网传输速率
- **资源状态透视**：直接查看所有 Agent 节点的 CPU 负载和内存使用率
- **数据可视化**：直观展示在线节点数、规则总数、累计流量以及各规则流量进度条

### 🚀 强大的转发能力
- **全协议支持**：完美支持 TCP、UDP 以及 TCP+UDP 双协议转发
- **灵活的拓扑结构**：支持入口节点与出口节点分离，轻松构建中转链路
- **IPv4/IPv6 双栈**：完全兼容 IPv6 环境

### 🛡️ 智能流控与管理
- **流量配额限制**：为每条规则设置流量上限，超额自动暂停
- **带宽限速**：支持为规则设置 MB/s 级别的带宽限制
- **一键启停**：通过 Web 界面快速暂停/恢复指定端口转发
- **Telegram 通知**：节点上线/下线等关键事件实时推送

### 📋 规则管理增强 (v2.0 新增)
- 🔍 **搜索与筛选**：按名称搜索，按入口/出口节点、状态筛选
- 📊 **排序**：按流量、用户数、名称排序
- ☑️ **批量操作**：批量启用/禁用/删除多条规则
- 📋 **规则克隆**：一键复制现有规则
- 📥📤 **导入/导出**：JSON 格式规则备份与迁移

### ⚡ 极简部署体验
- **单文件架构**：无任何系统依赖，一个二进制文件即可运行
- **自动安装脚本**：面板内置节点部署向导，复制粘贴即可部署
- **服务自托管**：支持 systemd (Linux) 和 OpenRC (Alpine) 开机自启

---

## 📚 部署教程

### 准备工作
- **中转机 (Master)**：用于部署控制面板的公网服务器
- **节点机 (Agent)**：用于实际转发流量的服务器（可以是同一台机器）

### 方式一：一键安装脚本（推荐）

```bash
curl -o go_relay.sh https://raw.githubusercontent.com/TerrySiu98/relay/main/go_relay.sh && chmod +x go_relay.sh && ./go_relay.sh
```

脚本提供以下功能：
| 选项 | 功能 |
|------|------|
| 1 | 安装 Relay |
| 2 | 更新 Relay（自动备份数据）|
| 3 | 查看状态 |
| 4 | 重启服务 |
| 5 | 查看日志 |
| 6 | 备份数据 |
| 7 | 恢复数据 |
| 8 | 卸载 Relay |

**命令行快捷操作：**
```bash
./go_relay.sh update   # 一键更新
./go_relay.sh status   # 查看状态
./go_relay.sh backup   # 备份数据
```

### 方式二：Docker 部署

```bash
# 创建目录
mkdir gorelay && cd gorelay

# 运行容器
docker run -d --name relay-master \
  --restart=always \
  --net=host \
  -v $(pwd):/data \
  terrysiu/relay -mode master
```

### 方式三：手动编译

```bash
# 下载并编译
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o relay main.go

# 首次运行
chmod +x relay
./relay -mode master

# 注册为系统服务
./relay -service install -mode master
```

---

## 🔧 部署节点 (Agent)

1. **登录面板**：访问 `http://<服务器IP>:8888`
2. **设置面板 IP**：进入【系统设置】填写 Master 服务器的公网 IP
3. **获取安装命令**：进入【部署节点】生成一键安装命令
4. **在节点执行**：复制命令到节点服务器运行

---

## 📦 添加转发规则

1. 进入【转发管理】页面
2. 选择**入口节点**和端口
3. 选择**出口节点**
4. 填写**目标 IP 和端口**
5. 选择协议并保存

✅ 完成！流量会自动经过：`入口节点 → 出口节点 → 目标服务器`

---

## 🔄 更新与数据持久化

### 更新项目
```bash
./go_relay.sh update
```
脚本会自动：备份数据库 → 下载新版 → 重启服务

### 数据存储
所有数据保存在 `data.db` (SQLite)，包括：
- ✅ 所有规则配置
- ✅ 流量统计数据
- ✅ 系统设置
- ✅ 操作日志

**备份位置**：`/root/relay_backup/`

---

## 🛠️ 常用维护命令

| 操作 | 命令 |
|------|------|
| 停止服务 | `systemctl stop relay` |
| 重启服务 | `systemctl restart relay` |
| 查看日志 | `journalctl -u relay -f` |
| 手动备份 | `./go_relay.sh backup` |
| 恢复数据 | `./go_relay.sh restore` |

---

## ⚠️ 注意事项

1. **防火墙**：确保放行以下端口
   - `8888`：Web 控制面板
   - `9999`：Agent 通信端口
   - 转发规则使用的端口

2. **安全性**：请妥善保管通信 Token

3. **架构支持**：
   - ✅ x86_64 (amd64)
   - ✅ ARM64 (aarch64)
   - ✅ ARMv7

---

## 📄 许可证

MIT License
