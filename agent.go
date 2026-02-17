package main

import (
	"crypto/rand"

	"encoding/json"
	"io"
	"log"
	"math/big"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func runAgent(name, masterAddr, token string) {

	for {
		var conn net.Conn
		var err error
		dialer := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
		conn, err = dialer.Dial("tcp", masterAddr)

		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}
		if tc, ok := conn.(*net.TCPConn); ok {
			_ = tc.SetKeepAlive(true)
			_ = tc.SetKeepAlivePeriod(30 * time.Second)
		}
		var writeMu sync.Mutex
		send := func(msg Message) error {
			writeMu.Lock()
			defer writeMu.Unlock()
			_ = conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
			err := json.NewEncoder(conn).Encode(msg)
			_ = conn.SetWriteDeadline(time.Time{})
			return err
		}
		if err := send(Message{Type: "auth", Payload: map[string]string{"name": name, "token": token}}); err != nil {
			_ = conn.Close()
			time.Sleep(3 * time.Second)
			continue
		}
		stop := make(chan struct{})
		var healthRunning atomic.Bool
		go func() {
			t := time.NewTicker(1 * time.Second)
			h := time.NewTicker(30 * time.Second)
			defer t.Stop()
			defer h.Stop()
			for {
				select {
				case <-stop:
					return
				case <-t.C:
					var reps []TrafficReport
					agentTraffic.Range(func(k, v interface{}) bool {
						c := v.(*TrafficCounter)
						tx, rx := atomic.SwapInt64(&c.Tx, 0), atomic.SwapInt64(&c.Rx, 0)
						var uc int64
						if val, ok := agentUserCounts.Load(k); ok {
							uc = atomic.LoadInt64(val.(*int64))
						}
						if tx > 0 || rx > 0 || uc > 0 {
							reps = append(reps, TrafficReport{TaskID: k.(string), TxDelta: tx, RxDelta: rx, UserCount: uc})
						}
						return true
					})
					if len(reps) > 0 {
						if err := send(Message{Type: "stats", Payload: reps}); err != nil {
							log.Printf("⚠️ Agent 上报 stats 失败 addr=%s err=%v", masterAddr, err)
							_ = conn.Close()
							return
						}
					} else {
						if err := send(Message{Type: "ping", Payload: getSysStatus()}); err != nil {
							log.Printf("⚠️ Agent 上报 ping 失败 addr=%s err=%v", masterAddr, err)
							_ = conn.Close()
							return
						}
					}
				case <-h.C:
					if healthRunning.CompareAndSwap(false, true) {
						go func() {
							defer healthRunning.Store(false)
							checkTargetHealth(conn, send)
						}()
					}
				}
			}
		}()
		dec := json.NewDecoder(conn)
		for {
			var msg Message
			if err := dec.Decode(&msg); err != nil {
				log.Printf("⚠️ Agent 接收控制消息失败 addr=%s err=%v", masterAddr, err)
				close(stop)
				conn.Close()
				break
			}
			if msg.Type == "uninstall" {
				_ = send(Message{Type: "uninstalling"})
				doSelfUninstall()
				return
			}
			if msg.Type == "upgrade" {
				if err := performSelfUpdate(); err == nil {
					doRestart()
				}
			}
			if msg.Type == "update" {
				d, _ := json.Marshal(msg.Payload)
				var tasks []ForwardTask
				json.Unmarshal(d, &tasks)
				active := make(map[string]bool)
				for _, t := range tasks {
					active[t.ID] = true
					activeTargets.Store(t.ID, t.Target)

					if oldVal, loaded := activeTasks.Load(t.ID); !loaded {
						activeTasks.Store(t.ID, t)
						agentTraffic.Store(t.ID, &TrafficCounter{})
						var uz int64
						agentUserCounts.Store(t.ID, &uz)
						startProxy(t)
					} else {
						oldTask, ok := oldVal.(ForwardTask)
						needRestart := !ok || oldTask.Protocol != t.Protocol || oldTask.Listen != t.Listen || oldTask.SpeedLimit != t.SpeedLimit
						activeTasks.Store(t.ID, t)
						if needRestart {
							if closer, ok := runningListeners.Load(t.ID); ok {
								closer.(func())()
								runningListeners.Delete(t.ID)
							}
							if _, ok := agentTraffic.Load(t.ID); !ok {
								agentTraffic.Store(t.ID, &TrafficCounter{})
							}
							if _, ok := agentUserCounts.Load(t.ID); !ok {
								var uz int64
								agentUserCounts.Store(t.ID, &uz)
							}
							startProxy(t)
						}
					}
				}
				runningListeners.Range(func(k, v interface{}) bool {
					if !active[k.(string)] {
						v.(func())()
						runningListeners.Delete(k)
						agentTraffic.Delete(k)
						agentUserCounts.Delete(k)
						activeTargets.Delete(k)
						activeTasks.Delete(k)
					}
					return true
				})
			}
		}
		time.Sleep(3 * time.Second)
	}
}

func doPing(address string) (int64, bool) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		if strings.Contains(err.Error(), "missing port") {
			host = address
		} else {
			return -1, false
		}
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("ping", "-n", "1", "-w", "1000", host)
	} else {
		cmd = exec.Command("ping", "-c", "1", "-W", "1", host)
	}

	start := time.Now()
	err = cmd.Run()
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return -1, false
	}
	return latency, true
}

func checkTargetHealth(conn net.Conn, send func(Message) error) {
	var results []HealthReport
	activeTargets.Range(func(key, value interface{}) bool {
		checkMode := "tcp"
		if tVal, ok := activeTasks.Load(key); ok {
			if t, ok := tVal.(ForwardTask); ok {
				if t.Protocol == "udp" {
					checkMode = "ping"
				} else if t.Protocol == "both" {
					checkMode = "mixed"
				}
			}
		}

		targets := strings.Split(value.(string), ",")
		var bestLat int64 = -1
		for _, target := range targets {
			target = strings.TrimSpace(target)
			if target == "" {
				continue
			}
			var success bool
			var lat int64
			if checkMode == "ping" {
				lat, success = doPing(target)
			} else if checkMode == "mixed" {
				start := time.Now()
				c, err := net.DialTimeout("tcp", target, 1*time.Second)
				if err == nil {
					c.Close()
					lat = time.Since(start).Milliseconds()
					success = true
				} else {
					lat, success = doPing(target)
				}
			} else {
				start := time.Now()
				c, err := net.DialTimeout("tcp", target, 1*time.Second)
				if err == nil {
					c.Close()
					lat = time.Since(start).Milliseconds()
					success = true
				}
			}
			if success {
				if bestLat == -1 || lat < bestLat {
					bestLat = lat
				}
				targetHealthMap.Store(target, true)
			} else {
				targetHealthMap.Store(target, false)
			}
		}
		results = append(results, HealthReport{TaskID: key.(string), Latency: bestLat})
		return true
	})
	if len(results) > 0 {
		_ = send(Message{Type: "health", Payload: results})
	}
}

type IpTracker struct {
	mu    sync.Mutex
	refs  map[string]int
	count *int64
}

func (t *IpTracker) Add(addr string) {
	host, _, _ := net.SplitHostPort(addr)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refs[host]++
	if t.refs[host] == 1 {
		atomic.AddInt64(t.count, 1)
	}
}

func (t *IpTracker) Remove(addr string) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if count, exists := t.refs[host]; !exists || count <= 0 {
		return
	}
	if t.refs[host] <= 0 {
		delete(t.refs, host)
		if atomic.LoadInt64(t.count) > 0 {
			atomic.AddInt64(t.count, -1)
		}
	}
}

func startProxy(t ForwardTask) {
	var closers []func()
	var l sync.Mutex
	activeConns := make(map[net.Conn]struct{})
	closed := false
	closeAll := func() {
		l.Lock()
		defer l.Unlock()
		if closed {
			return
		}
		closed = true
		for _, f := range closers {
			f()
		}
		for c := range activeConns {
			c.Close()
		}
	}
	runningListeners.Store(t.ID, closeAll)
	v, _ := agentUserCounts.Load(t.ID)
	userCountPtr := v.(*int64)
	ipTracker := &IpTracker{refs: make(map[string]int), count: userCountPtr}

	if t.Protocol == "tcp" || t.Protocol == "both" {
		go func() {
			ln, err := net.Listen("tcp", t.Listen)
			if err != nil {
				return
			}
			l.Lock()
			closers = append(closers, func() { ln.Close() })
			l.Unlock()
			for {
				c, e := ln.Accept()
				if e != nil {
					break
				}
				l.Lock()
				if closed {
					c.Close()
					l.Unlock()
					continue
				}
				activeConns[c] = struct{}{}
				l.Unlock()
				ipTracker.Add(c.RemoteAddr().String())
				go func(conn net.Conn) {
					pipeTCP(conn, t.ID, t.SpeedLimit)
					l.Lock()
					delete(activeConns, conn)
					l.Unlock()
					ipTracker.Remove(conn.RemoteAddr().String())
				}(c)
			}
		}()
	}
	if t.Protocol == "udp" || t.Protocol == "both" {
		go func() {
			addr, _ := net.ResolveUDPAddr("udp", t.Listen)
			ln, err := net.ListenUDP("udp", addr)
			if err != nil {
				return
			}
			l.Lock()
			closers = append(closers, func() { ln.Close() })
			l.Unlock()
			handleUDP(ln, t.ID, ipTracker, t.SpeedLimit)
		}()
	}
}

func pipeTCP(src net.Conn, tid string, limit int64) {
	defer src.Close()
	var targetStr string
	if v, ok := activeTargets.Load(tid); ok {
		targetStr = v.(string)
	} else {
		return
	}

	allTargets := strings.Split(targetStr, ",")
	var candidates []string
	for _, t := range allTargets {
		t = strings.TrimSpace(t)
		if status, ok := targetHealthMap.Load(t); ok && status.(bool) {
			candidates = append(candidates, t)
		}
	}
	if len(candidates) == 0 {
		candidates = allTargets
	}
	randIdx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(candidates))))
	selectedTarget := strings.TrimSpace(candidates[randIdx.Int64()])

	// 移除强制 TLS 逻辑，始终使用 TCP 直连
	// 这样 XrayR (Trojan) 和 SS 都能收到正确的原始流量
	var dst net.Conn
	var err error
	dst, err = net.DialTimeout("tcp", selectedTarget, 8*time.Second)

	if err != nil {
		return
	}
	defer dst.Close()
	v, _ := agentTraffic.Load(tid)
	cnt := v.(*TrafficCounter)
	go copyCount(dst, src, &cnt.Tx, limit)
	copyCount(src, dst, &cnt.Rx, limit)
}

func handleUDP(ln *net.UDPConn, tid string, tracker *IpTracker, limit int64) {
	udpSessions := &sync.Map{}
	v, _ := agentTraffic.Load(tid)
	cnt := v.(*TrafficCounter)

	go func() {
		for {
			time.Sleep(30 * time.Second)
			now := time.Now()
			udpSessions.Range(func(key, value interface{}) bool {
				s := value.(*udpSession)
				if now.Sub(s.lastActive) > 180*time.Second {
					s.conn.Close()
					udpSessions.Delete(key)
					tracker.Remove(key.(string))
				}
				return true
			})
		}
	}()

	bufPtr := bufPool.Get().(*[]byte)
	defer bufPool.Put(bufPtr)
	buf := *bufPtr
	for {
		n, srcAddr, err := ln.ReadFromUDP(buf)
		if err != nil {
			break
		}
		atomic.AddInt64(&cnt.Tx, int64(n))
		sAddr := srcAddr.String()
		val, ok := udpSessions.Load(sAddr)
		if ok {
			s := val.(*udpSession)
			s.lastActive = time.Now()
			s.conn.Write(buf[:n])
		} else {
			var currentTargetStr string
			if v, ok := activeTargets.Load(tid); ok {
				currentTargetStr = v.(string)
			} else {
				continue
			}
			targets := strings.Split(currentTargetStr, ",")
			randIdx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(targets))))
			dstAddr, _ := net.ResolveUDPAddr("udp", strings.TrimSpace(targets[randIdx.Int64()]))
			newConn, err := net.DialUDP("udp", nil, dstAddr)
			if err != nil {
				continue
			}
			s := &udpSession{conn: newConn, lastActive: time.Now()}
			udpSessions.Store(sAddr, s)
			tracker.Add(sAddr)
			newConn.Write(buf[:n])
			go func(c *net.UDPConn, sa *net.UDPAddr, k string) {
				bPtr := bufPool.Get().(*[]byte)
				defer bufPool.Put(bPtr)
				b := *bPtr
				for {
					c.SetReadDeadline(time.Now().Add(190 * time.Second))
					m, _, e := c.ReadFromUDP(b)
					if e != nil {
						c.Close()
						udpSessions.Delete(k)
						tracker.Remove(k)
						break
					}
					ln.WriteToUDP(b[:m], sa)
					atomic.AddInt64(&cnt.Rx, int64(m))
				}
			}(newConn, srcAddr, sAddr)
		}
	}
}

func copyCount(dst io.Writer, src io.Reader, c *int64, limit int64) {
	bufPtr := bufPool.Get().(*[]byte)
	defer bufPool.Put(bufPtr)
	buf := *bufPtr
	for {
		nr, err := src.Read(buf)
		if nr > 0 {
			start := time.Now()
			nw, _ := dst.Write(buf[0:nr])
			if nw > 0 {
				atomic.AddInt64(c, int64(nw))
			}
			if limit > 0 {
				exp := time.Duration(1e9 * int64(nr) / limit)
				if act := time.Since(start); exp > act {
					time.Sleep(exp - act)
				}
			}
		}
		if err != nil {
			break
		}
	}
}
