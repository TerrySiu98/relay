        // Critical UI Functions - Safe Mode
        // Defined here to ensure navigation works even if main script fails
        window.onerror = function (msg, url, line, col, error) {
            console.error("Global JS Error:", msg, url, line, col, error);
            const banner = document.getElementById('js-error-banner');
            const txt = document.getElementById('js-error-msg');
            if (banner && txt) {
                banner.style.display = 'block';
                txt.innerText = msg + " (Ln: " + line + ")";
            }
            return false; // Let default handler run too
        };

        window.showToast = function (msg, type) {
            const box = document.getElementById('toast');
            if (!box) return;
            const icon = document.getElementById('t-icon');
            const tMsg = document.getElementById('t-msg');
            if (tMsg) tMsg.innerText = msg;
            box.className = 'toast show';
            if (icon) {
                if (type === 'warn') { icon.className = 'ri-error-warning-fill'; icon.style.color = '#fbbf24'; }
                else if (type === 'success') { icon.className = 'ri-checkbox-circle-fill'; icon.style.color = '#34d399'; }
                else { icon.className = 'ri-information-fill'; icon.style.color = '#60a5fa'; }
            }
            setTimeout(() => box.className = 'toast', 2500);
        };

        window.toggleTheme = function () {
            const html = document.documentElement;
            const curr = html.getAttribute('data-theme');
            const next = curr === 'dark' ? 'light' : 'dark';
            html.setAttribute('data-theme', next);
            localStorage.setItem('theme', next);
            if (typeof updateChartTheme === 'function') updateChartTheme(next);
            const icon = document.getElementById('theme-icon');
            if (icon) icon.className = next === 'dark' ? 'ri-moon-line' : 'ri-sun-line';
        };

        window.nav = function (id, el) {
            try {
                document.querySelectorAll('.page').forEach(e => e.classList.remove('active'));
                const target = document.getElementById(id);
                if (!target) { console.error("Target page not found:", id); return; }
                target.classList.add('active');

                const titles = { 'dashboard': '仪表盘', 'deploy': '节点部署', 'rules': '转发规则', 'logs': '系统日志', 'settings': '系统配置' };
                const titleEl = document.getElementById('page-text');
                if (titleEl) titleEl.innerText = titles[id] || 'GoRelay';

                document.querySelectorAll('.sidebar .item').forEach(i => i.classList.remove('active'));
                if (el) el.classList.add('active');
                else { const t = document.querySelector('.sidebar .item[onclick*="' + id + '"]'); if (t) t.classList.add('active'); }

                document.querySelectorAll('.mobile-nav .nav-btn').forEach(b => b.classList.remove('active'));
                const mBtn = document.querySelector('.mobile-nav .nav-btn[onclick*="' + id + '"]');
                if (mBtn) mBtn.classList.add('active');

                if (history.pushState) history.pushState(null, null, '#' + id); else location.hash = '#' + id;
            } catch (e) {
                console.error("Nav Error:", e);
                window.showToast("Nav Error: " + e.message, "warn");
            }
        };

        window.initTab = function () { const hash = window.location.hash.substring(1); if (hash && document.getElementById(hash)) window.nav(hash); };

        window.showConfirm = function (title, msg, type, cb) {
            const t = document.getElementById('c_title'); if (t) t.innerText = title;
            const m = document.getElementById('c_msg'); if (m) m.innerHTML = msg;
            const btn = document.getElementById('c_btn');
            const icon = document.getElementById('c_icon');
            if (btn && icon) {
                if (type === 'danger') { btn.className = 'btn danger'; btn.innerText = '确认删除'; icon.innerText = '🗑️'; }
                else if (type === 'warning') { btn.className = 'btn warning'; btn.innerText = '确认操作'; icon.innerText = '⚡'; }
                else { btn.className = 'btn'; btn.innerText = '确认执行'; icon.innerText = '✨'; }
                btn.onclick = function () { window.closeConfirm(); cb(); };
            }
            const modal = document.getElementById('confirmModal'); if (modal) modal.style.display = 'block';
        };

        window.closeConfirm = function () { const m = document.getElementById('confirmModal'); if (m) m.style.display = 'none'; };

        window.copyText = function (txt) {
            if (navigator.clipboard && window.isSecureContext) navigator.clipboard.writeText(txt).then(() => window.showToast("已复制", "success"));
            else {
                const ta = document.createElement("textarea"); ta.value = txt; ta.style.position = "fixed"; ta.style.left = "-9999px";
                document.body.appendChild(ta); ta.focus(); ta.select();
                try { document.execCommand('copy'); window.showToast("已复制", "success"); } catch (e) { window.showToast("复制失败", "warn"); }
                document.body.removeChild(ta);
            }
        };

        document.addEventListener('DOMContentLoaded', window.initTab);
    </script>
    <script>
        // Main Logic Script
        // Errors here will be caught by the global handler in <head>

        // Explicitly define nav to global window to avoid scope issues
        // nav defined in head
        window.nav = window.nav || function () { };

        var m_domain = "{{.MasterDomain}}", m_v4 = "{{.MasterIP}}", m_v6 = "{{.MasterIPv6}}", token = "{{.Token}}", dwUrl = "{{.DownloadURL}}";
        var lastRuleStats = {};
        var ruleCharts = {};

        // Hoist chart variables to global scope
        var chart = null;
        var pieChart = null;
        var updateChartTheme = function (theme) { };

        function createMiniChartConfig(color) {
            const ctxGrad = document.createElement('canvas').getContext('2d').createLinearGradient(0, 0, 0, 32);
            ctxGrad.addColorStop(0, color.replace(')', ', 0.3)').replace('rgb', 'rgba'));
            ctxGrad.addColorStop(1, color.replace(')', ', 0)').replace('rgb', 'rgba'));

            return {
                type: 'line',
                data: {
                    labels: Array(15).fill(''),
                    datasets: [{
                        data: Array(15).fill(0),
                        borderColor: color,
                        backgroundColor: ctxGrad,
                        borderWidth: 1.5,
                        pointRadius: 0,
                        fill: true,
                        tension: 0.4
                    }]
                },
                options: {
                    responsive: true,
                    maintainAspectRatio: false,
                    animation: false,
                    plugins: { legend: { display: false }, tooltip: { enabled: false } },
                    scales: { x: { display: false }, y: { display: false, min: 0 } },
                    elements: { line: { borderJoinStyle: 'round' } }
                }
            };
        }

        /* Global nav function defined above */

        function initTab() { const hash = window.location.hash.substring(1); if (hash && document.getElementById(hash)) nav(hash); }
        initTab();

        document.addEventListener('DOMContentLoaded', () => {
            const collapsed = JSON.parse(localStorage.getItem('collapsed_groups') || '[]');
            collapsed.forEach(g => {
                const header = document.querySelector('.group-header[data-group="' + g + '"]');
                if (header) setGroupState(header, false);
            });
            checkUpdate();
        });

        function toggleGroup(header) {
            const isCurrentlyCollapsed = header.classList.contains('group-collapsed');
            setGroupState(header, isCurrentlyCollapsed);
            const group = header.getAttribute('data-group');
            let collapsed = JSON.parse(localStorage.getItem('collapsed_groups') || '[]');
            if (isCurrentlyCollapsed) { collapsed = collapsed.filter(i => i !== group); } else { if (!collapsed.includes(group)) collapsed.push(group); }
            localStorage.setItem('collapsed_groups', JSON.stringify(collapsed));
        }

        function setGroupState(header, expand) {
            const group = header.getAttribute('data-group');
            const rows = Array.from(document.querySelectorAll('.rule-row')).filter(row => row.getAttribute('data-group') === group);
            if (!expand) { header.classList.add('group-collapsed'); rows.forEach(r => r.style.display = 'none'); }
            else { header.classList.remove('group-collapsed'); rows.forEach(r => r.style.display = 'table-row'); }
        }

        // UI functions (copyText, toggleTheme, showToast, showConfirm) moved to head for safety

        function restartService() {
            showConfirm("重启服务", "确定要重启面板服务吗？连接将短暂中断。", "warning", () => {
                fetch('/restart', { method: 'POST' }).then(() => {
                    showToast("系统正在重启...", "warn");
                    setTimeout(() => location.reload(), 3000);
                }).catch(() => { showToast("请求发送失败", "warn"); });
            });
        }

        function checkUpdate(manual) {
            fetch('/check_update').then(r => r.json()).then(d => {
                const has = !!d.has_update;
                const latest = d.latest_version || '';
                const current = d.current || '';
                const badge = document.getElementById('settings-badge');
                const txt = document.getElementById('new-version-text');
                const updateBtn = document.getElementById('btn-update');

                if (has && latest) {
                    if (badge) badge.style.display = 'inline-block';
                    if (txt) { txt.style.display = 'inline'; txt.innerText = '发现新版本 ' + latest; }
                    if (updateBtn) updateBtn.style.display = 'inline-flex';
                    if (manual) showToast('发现新版本 ' + latest, 'success');
                } else {
                    if (txt) { txt.style.display = 'inline'; txt.innerText = current ? ('当前已是最新版本 ' + current) : '当前已是最新版本'; }
                    if (updateBtn) updateBtn.style.display = 'none';
                    if (manual) showToast('当前已是最新版本', 'success');
                }
            }).catch(() => {
                if (manual) showToast('检查更新失败', 'warn');
            });
        }

        function updateSystem() {
            fetch('/check_update').then(r => r.json()).then(d => {
                if (!d.has_update) {
                    showToast('当前已是最新版本', 'success');
                    return;
                }
                showConfirm("系统更新", "下载新版本并重启面板吗？", "warning", () => {
                    const btn = document.getElementById('btn-update'); btn.disabled = true; btn.innerText = '更新中...';
                    fetch('/update_sys', { method: 'POST' }).then(r => r.json()).then(d2 => {
                        if (d2.success) { showToast("更新成功，重启中...", "success"); setTimeout(() => location.reload(), 5000); }
                        else { showToast("更新失败: " + d2.error, "warn"); btn.disabled = false; btn.innerText = '立即更新'; }
                    }).catch(() => { showToast("请求失败", "warn"); btn.disabled = false; btn.innerText = '立即更新'; });
                });
            }).catch(() => {
                showToast('检查更新失败', 'warn');
            });
        }

        function updateAgent(name) {
            showConfirm("更新节点", "确定要远程更新节点 <b>" + name + "</b> 吗？", "warning", () => {
                fetch('/update_agent?name=' + name, { method: 'POST' }).then(r => {
                    if (r.ok) showToast("指令已发送", "success"); else showToast("发送失败", "warn");
                });
            });
        }

        function delAgent(name) { showConfirm("卸载节点", "节点 <b>" + name + "</b> 将自毁，确定吗？", "danger", () => location.href = "/delete_agent?name=" + name); }

        // Helper functions
        const savedTheme = localStorage.getItem('theme') || (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');
        document.documentElement.setAttribute('data-theme', savedTheme);
        const tIcon = document.getElementById('theme-icon');
        if (tIcon) tIcon.className = savedTheme === 'dark' ? 'ri-moon-line' : 'ri-sun-line';

        function genCmd() {
            const n = document.getElementById('agentName').value;
            const t = document.getElementById('addrType').value;
            const arch = document.getElementById('archType').value;
            const p = document.getElementById('connPort').value;
            const finalDwUrl = dwUrl + "-linux-" + arch;
            const host = (t === "domain") ? (m_domain || location.hostname) : (t === "v4" ? m_v4 : '[' + m_v6 + ']');
            if (!host || host === "[]") { showToast("请先在设置中配置面板地址", "warn"); return; }

            let cmd = 'curl -L -o /root/relay ' + finalDwUrl + ' && chmod +x /root/relay && /root/relay -service install -mode agent -name "' + n + '" -connect "' + host + ':' + p + '" -token "' + token + '"';

            document.getElementById('cmdText').innerText = cmd;
            document.getElementById('cmdText').style.opacity = '1';
            showToast("命令已生成", "success");
        }
        function copyCmd() { copyText(document.getElementById('cmdText').innerText); }

        function normalizeLegacyRuleForm(protoId, hintId) {
            const proto = document.getElementById(protoId);
            const hint = document.getElementById(hintId);
            if (!proto) return;
            if (proto.value === 'udp') {
                if (hint) hint.innerText = '标准 UDP 转发模式';
            } else {
                if (hint) hint.innerText = '标准 TCP 转发模式';
            }
        }

        function applyLegacyPreset(kind) {
            const proto = document.getElementById('legacy_new_proto');
            const hint = document.getElementById('legacy-rule-hint');
            if (!proto) return;
            if (kind === 'standard') {
                proto.value = 'tcp';
                if (hint) hint.innerText = '标准 TCP 转发模式';
            } else if (kind === 'udp') {
                proto.value = 'udp';
                if (hint) hint.innerText = '标准 UDP 转发模式';
            }
            normalizeLegacyRuleForm('legacy_new_proto', 'legacy-rule-hint');
        }

        function applyLegacyEditPreset(kind) {
            const proto = document.getElementById('e_proto');
            const hint = document.getElementById('legacy-edit-rule-hint');
            if (!proto) return;
            if (kind === 'standard') {
                proto.value = 'tcp';
                if (hint) hint.innerText = '标准 TCP 转发模式';
            } else if (kind === 'udp') {
                proto.value = 'udp';
                if (hint) hint.innerText = '标准 UDP 转发模式';
            }
            normalizeLegacyRuleForm('e_proto', 'legacy-edit-rule-hint');
        }

        function delRule(id) { showConfirm("删除规则", "端口将停止服务，确定删除吗？", "danger", () => location.href = "/delete?id=" + id); }
        function toggleRule(id) { location.href = "/toggle?id=" + id; }
        function resetTraffic(id) { showConfirm("重置流量", "确定要清零统计数据吗？", "warning", () => location.href = "/reset_traffic?id=" + id); }

        function getSelectedRuleIds() {
            return Array.from(document.querySelectorAll('.rule-bulk-select:checked')).map(x => x.value);
        }
        function updateBulkSelectedCount() {
            const n = getSelectedRuleIds().length;
            const el = document.getElementById('bulk_selected_count');
            if (el) el.innerText = n + ' 已选';
        }
        function toggleAllRules(checked) {
            document.querySelectorAll('.rule-bulk-select').forEach(x => x.checked = checked);
            updateBulkSelectedCount();
        }
        function bulkRequest(action, extra, confirmText) {
            const ids = getSelectedRuleIds();
            if (ids.length === 0) { showToast('请先选择规则', 'warn'); return; }
            const run = () => {
                fetch('/api/v1/rules/bulk', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(Object.assign({ ids: ids, action: action }, extra || {}))
                }).then(r => r.json()).then(d => {
                    if (d.success) { showToast('操作成功', 'success'); setTimeout(() => location.reload(), 500); }
                    else { showToast('操作失败: ' + (d.error || 'unknown'), 'warn'); }
                }).catch(() => showToast('请求失败', 'warn'));
            };
            if (confirmText) showConfirm('批量操作', confirmText, action === 'delete' ? 'danger' : 'warning', run);
            else run();
        }
        function bulkSetDisabled(disabled) {
            bulkRequest('set_disabled', { disabled: disabled }, disabled ? '确定批量暂停选中规则吗？' : '确定批量启用选中规则吗？');
        }

        function bulkResetTraffic() { bulkRequest('reset_traffic', {}, '确定批量清零流量统计吗？'); }
        function bulkDeleteRules() { bulkRequest('delete', {}, '确定删除选中规则吗？此操作不可恢复。'); }

        function editAlias(name, current) {
            const val = prompt('设置节点别名（留空则清空）', current || '');
            if (val === null) return;
            fetch('/api/v1/agents/' + encodeURIComponent(name) + '/alias', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ alias: val.trim() })
            }).then(r => r.json()).then(d => {
                if (d.success) { showToast('别名已更新', 'success'); setTimeout(() => location.reload(), 300); }
                else { showToast('更新失败: ' + (d.error || 'unknown'), 'warn'); }
            }).catch(() => showToast('请求失败', 'warn'));
        }

        function openEdit(id, group, note, entry, eport, exit, tip, tport, proto, limit, speed, entryTLS) {
            document.getElementById('e_id').value = id;
            document.getElementById('e_group').value = group;
            document.getElementById('e_note').value = note;
            document.getElementById('e_entry').value = entry;
            document.getElementById('e_eport').value = eport;
            document.getElementById('e_exit').value = exit;
            document.getElementById('e_tip').value = tip;
            document.getElementById('e_tport').value = tport;
            document.getElementById('e_proto').value = proto;
            document.getElementById('e_limit').value = (parseFloat(limit) / (1024 * 1024 * 1024)).toFixed(2);
            document.getElementById('e_speed').value = (parseFloat(speed) / (1024 * 1024)).toFixed(1);
            normalizeLegacyRuleForm('e_proto', 'legacy-edit-rule-hint');
            document.getElementById('editModal').style.display = 'block';
        }
        function closeEdit() { document.getElementById('editModal').style.display = 'none'; }
        window.onclick = function (e) { if (e.target.className === 'modal') { closeEdit(); closeConfirm(); document.getElementById('twoFAModal').style.display = 'none'; } }
        // Chart.js Configuration (Vars hoisted above)

        document.addEventListener('DOMContentLoaded', () => {
            const p1 = document.getElementById('legacy_new_proto');
            if (p1) {
                p1.addEventListener('change', () => normalizeLegacyRuleForm('legacy_new_proto', 'legacy-rule-hint'));
                applyLegacyPreset('standard');
            }
            const p2 = document.getElementById('e_proto');
            if (p2) {
                p2.addEventListener('change', () => normalizeLegacyRuleForm('e_proto', 'legacy-edit-rule-hint'));
            }
            updateBulkSelectedCount();
        });
        var tempSecret = "";
        function enable2FA() { fetch('/2fa/generate').then(r => r.json()).then(d => { tempSecret = d.secret; document.getElementById('qrImage').src = d.qr; document.getElementById('twoFAModal').style.display = 'block'; }); }
        function verify2FA() { fetch('/2fa/verify', { method: 'POST', body: JSON.stringify({ secret: tempSecret, code: document.getElementById('twoFACode').value }) }).then(r => r.json()).then(d => { if (d.success) { showToast("2FA 已开启", "success"); setTimeout(() => location.reload(), 1000); } else showToast("验证码错误", "warn"); }); }
        function disable2FA() { showConfirm("关闭 2FA", "账户安全性将降低，确定吗？", "danger", () => { fetch('/2fa/disable').then(r => r.json()).then(d => { if (d.success) location.reload(); }); }); }

        // Chart.js Configuration
        if (typeof Chart !== 'undefined') {
            Chart.defaults.font.family = "'Inter', sans-serif";
            Chart.defaults.color = '#94a3b8';

            var ctx = document.getElementById('trafficChart').getContext('2d');
            var txGrad = ctx.createLinearGradient(0, 0, 0, 300);
            txGrad.addColorStop(0, 'rgba(139, 92, 246, 0.2)');
            txGrad.addColorStop(1, 'rgba(139, 92, 246, 0)');

            var rxGrad = ctx.createLinearGradient(0, 0, 0, 300);
            rxGrad.addColorStop(0, 'rgba(6, 182, 212, 0.2)');
            rxGrad.addColorStop(1, 'rgba(6, 182, 212, 0)');

            chart = new Chart(ctx, {
                type: 'line',
                data: {
                    labels: Array(30).fill(''),
                    datasets: [
                        { label: 'Tx', data: Array(30).fill(0), borderColor: '#8b5cf6', backgroundColor: txGrad, borderWidth: 2, pointRadius: 0, fill: true, tension: 0.4 },
                        { label: 'Rx', data: Array(30).fill(0), borderColor: '#06b6d4', backgroundColor: rxGrad, borderWidth: 2, pointRadius: 0, fill: true, tension: 0.4 }
                    ]
                },
                options: {
                    responsive: true, maintainAspectRatio: false,
                    plugins: { legend: { display: false }, tooltip: { mode: 'index', intersect: false, backgroundColor: 'rgba(15, 23, 42, 0.9)', titleColor: '#f8fafc', bodyColor: '#cbd5e1', borderColor: 'rgba(255,255,255,0.1)', borderWidth: 1, padding: 10, displayColors: true } },
                    scales: {
                        x: { display: false },
                        y: { beginAtZero: true, grid: { color: 'rgba(128, 128, 128, 0.06)', borderDash: [4, 4] }, ticks: { callback: v => formatBytes(v) + '/s', font: { size: 10 }, maxTicksLimit: 5 } }
                    },
                    interaction: { mode: 'nearest', axis: 'x', intersect: false }
                }
            });

            var ctxPie = document.getElementById('pieChart').getContext('2d');
            pieChart = new Chart(ctxPie, {
                type: 'doughnut',
                data: {
                    labels: [],
                    datasets: [{ data: [], backgroundColor: ['#818cf8', '#f472b6', '#fbbf24', '#34d399', '#60a5fa'], borderWidth: 0, hoverOffset: 4 }]
                },
                options: {
                    responsive: true, maintainAspectRatio: false,
                    plugins: { legend: { position: 'bottom', labels: { boxWidth: 8, usePointStyle: true, padding: 20, font: { size: 11 } } } },
                    cutout: '75%'
                }
            });

            updateChartTheme = function (theme) {
                const gridColor = theme === 'dark' ? 'rgba(255, 255, 255, 0.05)' : 'rgba(0, 0, 0, 0.05)';
                if (chart) {
                    chart.options.scales.y.grid.color = gridColor;
                    chart.update();
                }
            };
        } else {
            console.warn("Chart.js not loaded - Graphs disabled");
            // Variables are already null/empty-function by default
        }

        function formatBytes(b) {
            if (b == 0) return "0 B";
            const u = 1024, i = Math.floor(Math.log(b) / Math.log(u));
            return parseFloat((b / Math.pow(u, i)).toFixed(2)) + " " + ["B", "KB", "MB", "GB", "TB"][i];
        }
        function formatSpeed(b) {
            if (b <= 0) return "0 B/s";
            return formatBytes(b) + "/s";
        }

        function connectWS() {
            const ws = new WebSocket((location.protocol === 'https:' ? 'wss:' : 'ws:') + '//' + location.host + '/ws');
            ws.onmessage = function (e) {
                try {
                    const msg = JSON.parse(e.data);
                    if (msg.type === 'stats' && msg.data) {
                        const d = msg.data;
                        document.getElementById('stat-total-traffic').innerText = formatBytes(d.total_traffic);
                        document.getElementById('speed-rx').innerText = formatBytes(d.speed_rx) + '/s';
                        document.getElementById('speed-tx').innerText = formatBytes(d.speed_tx) + '/s';

                        if (chart) {
                            chart.data.datasets[0].data.push(d.speed_tx); chart.data.datasets[0].data.shift();
                            chart.data.datasets[1].data.push(d.speed_rx); chart.data.datasets[1].data.shift();
                            chart.update('none');
                        }

                        if (d.rules) {
                            const sortedRules = [...d.rules].sort((a, b) => b.total - a.total).slice(0, 5);
                            if (pieChart) {
                                pieChart.data.labels = sortedRules.map(r => r.name || '未命名');
                                pieChart.data.datasets[0].data = sortedRules.map(r => r.total);
                                pieChart.update('none');
                            }

                            const tbody = document.getElementById('rule-monitor-body');
                            if (document.getElementById('dashboard').classList.contains('active')) {
                                const activeIds = new Set();
                                d.rules.forEach(r => {
                                    let stx = 0, srx = 0;
                                    if (lastRuleStats[r.id]) {
                                        stx = r.tx - lastRuleStats[r.id].tx;
                                        srx = r.rx - lastRuleStats[r.id].rx;
                                        if (stx < 0) stx = 0; if (srx < 0) srx = 0;
                                    }
                                    lastRuleStats[r.id] = { tx: r.tx, rx: r.rx };

                                    let row = document.getElementById('rule-row-mon-' + r.id);
                                    if (!row) {
                                        row = tbody.insertRow();
                                        row.id = 'rule-row-mon-' + r.id;
                                        row.innerHTML = '<td><div style="font-weight:600;font-size:13px;margin-bottom:2px">' + (r.name || '未命名') + '</div><div style="font-size:11px;color:var(--text-sub);font-family:var(--font-mono)">' + r.id.substring(0, 8) + '...</div></td>' +
                                            '<td><div class="mini-chart-container"><canvas id="chart-tx-' + r.id + '"></canvas></div><div class="speed-text" style="color:#8b5cf6" id="text-tx-' + r.id + '">0 B/s</div></td>' +
                                            '<td><div class="mini-chart-container"><canvas id="chart-rx-' + r.id + '"></canvas></div><div class="speed-text" style="color:#06b6d4" id="text-rx-' + r.id + '">0 B/s</div></td>' +
                                            '<td style="font-family:var(--font-mono);font-weight:600" id="text-total-' + r.id + '">' + formatBytes(r.total) + '</td>';

                                        const ctxTx = document.getElementById('chart-tx-' + r.id).getContext('2d');
                                        const ctxRx = document.getElementById('chart-rx-' + r.id).getContext('2d');
                                        if (typeof Chart !== 'undefined') {
                                            ruleCharts[r.id] = { tx: new Chart(ctxTx, createMiniChartConfig('#8b5cf6')), rx: new Chart(ctxRx, createMiniChartConfig('#06b6d4')) };
                                        }
                                    } else {
                                        document.getElementById('text-tx-' + r.id).innerText = formatSpeed(stx);
                                        document.getElementById('text-rx-' + r.id).innerText = formatSpeed(srx);
                                        document.getElementById('text-total-' + r.id).innerText = formatBytes(r.total);
                                    }
                                    const charts = ruleCharts[r.id];
                                    if (charts) {
                                        charts.tx.data.datasets[0].data.push(stx); charts.tx.data.datasets[0].data.shift(); charts.tx.update('none');
                                        charts.rx.data.datasets[0].data.push(srx); charts.rx.data.datasets[0].data.shift(); charts.rx.update('none');
                                    }
                                });
                                Array.from(tbody.children).forEach(tr => {
                                    const id = tr.id.replace('rule-row-mon-', '');
                                    if (id && !activeIds.has(id)) {
                                        if (ruleCharts[id]) { ruleCharts[id].tx.destroy(); ruleCharts[id].rx.destroy(); delete ruleCharts[id]; }
                                        tr.remove();
                                    }
                                });
                            }

                            d.rules.forEach(r => {
                                const traf = document.getElementById('rule-traffic-' + r.id); if (traf) traf.innerText = formatBytes(r.total);
                                const uc = document.getElementById('rule-uc-' + r.id); if (uc) uc.innerText = r.uc;
                                const lat = document.getElementById('rule-latency-' + r.id);
                                const dot = document.getElementById('rule-status-dot-' + r.id);
                                if (lat && dot) {
                                    if (r.status) {
                                        lat.innerHTML = '<span style="color:#10b981;font-weight:600">' + r.latency + ' ms</span>';
                                        dot.parentElement.className = 'badge success'; dot.parentElement.innerHTML = '<span class="status-dot pulse"></span> 运行中';
                                    } else {
                                        lat.innerHTML = '<span style="color:#ef4444">离线</span>';
                                        dot.parentElement.className = 'badge danger'; dot.parentElement.innerHTML = '<span class="status-dot"></span> 异常';
                                    }
                                }
                                if (r.limit > 0) {
                                    let pct = (r.total / r.limit) * 100; if (pct > 100) pct = 100;
                                    const bar = document.getElementById('rule-bar-' + r.id);
                                    if (bar) { bar.style.width = pct + '%'; bar.style.background = pct > 90 ? '#ef4444' : '#6366f1'; }
                                    const txt = document.getElementById('rule-limit-text-' + r.id);
                                    if (txt) txt.innerText = pct.toFixed(1) + '%';
                                }
                            });
                        }

                        if (d.agents) d.agents.forEach(a => {
                            const loadText = document.getElementById('load-text-' + a.name);
                            const loadBar = document.getElementById('load-bar-' + a.name);
                            if (loadText && loadBar) {
                                let loadStr = a.sys_status;
                                let loadVal = 0;
                                if (loadStr.includes("Load:")) { loadVal = parseFloat(loadStr.split("|")[0].replace("Load:", "").trim()) || 0; }
                                loadText.innerText = loadVal.toFixed(2);
                                let pct = loadVal * 20; if (pct > 100) pct = 100;
                                loadBar.style.width = pct + "%"; loadBar.style.background = pct > 80 ? "#ef4444" : "#10b981";
                            }
                        });

                        if (d.logs && document.getElementById('logs').classList.contains('active')) {
                            const tbody = document.getElementById('log-table-body');
                            let html = '';
                            d.logs.forEach(l => {
                                html += '<tr><td style="font-family:var(--font-mono);color:var(--text-sub)">' + l.time + '</td>' +
                                    '<td>' + l.ip + '</td>' +
                                    '<td><span class="badge" style="background:var(--input-bg);color:var(--text-main);border:1px solid var(--border)">' + l.action + '</span></td>' +
                                    '<td style="color:var(--text-sub)">' + l.msg + '</td></tr>';
                            });
                            tbody.innerHTML = html;
                        }
                    }
                } catch (err) { console.log(err); }
            };
            ws.onclose = () => setTimeout(connectWS, 3000);
        }
        connectWS();
