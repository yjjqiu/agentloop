// —— 内置 agentgateway：托管进程 + 指标面板 ——
// 负责「装好、拉起、看指标、看日志、按 CLI 接管终端」，关掉设置页就停止轮询；
// 不启用也完全不影响 AgentDeck 其它功能。

let gatewayData = null;
let gatewayTimer = null;

// fmtCompact 把大数字压成人能读的形式（token 计数很容易上百万）。
function fmtCompact(n) {
  const v = Number(n) || 0;
  if (v >= 1e9) return `${(v / 1e9).toFixed(1)}B`;
  if (v >= 1e6) return `${(v / 1e6).toFixed(1)}M`;
  if (v >= 1e4) return `${(v / 1e3).toFixed(1)}K`;
  return String(Math.round(v));
}

// fmtBytes 用于下载进度。
function fmtBytes(n) {
  const v = Number(n) || 0;
  if (v >= 1 << 30) return `${(v / (1 << 30)).toFixed(1)} GB`;
  if (v >= 1 << 20) return `${(v / (1 << 20)).toFixed(1)} MB`;
  if (v >= 1 << 10) return `${(v / (1 << 10)).toFixed(0)} KB`;
  return `${v} B`;
}

// gatewayVisible 判断网关页签是否还在前台，决定要不要继续轮询。
function gatewayVisible() {
  const modal = $('settingsModal');
  const pane = $('paneGateway');
  return Boolean(modal && !modal.hidden && pane && !pane.hidden);
}

async function loadGateway() {
  // 第一次打开还不知道状态，先要一份指标；之后只在运行中才抓。
  const wantMetrics = gatewayData ? Boolean(gatewayData.status?.running) : true;
  gatewayData = await api.gateway(wantMetrics);
  paintGateway();
  // 运行中也要刷（指标在动），只在安装中刷得更勤一点。
  if (gatewayData.job?.running || gatewayData.status?.running) startGatewayPoll();
  else stopGatewayPoll();
}

function startGatewayPoll() {
  if (gatewayTimer) return;
  gatewayTimer = setInterval(async () => {
    if (!gatewayVisible()) {
      stopGatewayPoll();
      return;
    }
    try {
      gatewayData = await api.gateway(Boolean(gatewayData?.status?.running));
      paintGateway();
      if (!gatewayData.job?.running && !gatewayData.status?.running) stopGatewayPoll();
    } catch {
      stopGatewayPoll();
    }
  }, 2500);
}

function stopGatewayPoll() {
  if (!gatewayTimer) return;
  clearInterval(gatewayTimer);
  gatewayTimer = null;
}

// fillGatewayField 只在用户没在编辑时回填，避免轮询把正在输的字冲掉。
function fillGatewayField(id, value) {
  const el = $(id);
  if (el && document.activeElement !== el) el.value = value;
}

function paintGateway() {
  const data = gatewayData;
  if (!data) return;
  const st = data.status || {};
  const job = data.job || {};

  const dot = $('gwDot');
  if (dot) dot.className = `gw-dot ${st.running ? 'on' : st.installed ? 'idle' : 'off'}`;
  $('gwState').textContent = st.running ? '运行中' : st.installed ? '已停止' : '未安装';
  const meta = [];
  if (st.version) meta.push(st.version);
  else if (st.installed) meta.push(`agentgateway ${data.pinnedVersion}（托管）`);
  meta.push(`业务 ${st.port}`);
  if (st.proxyPort) meta.push(`代理 ${st.proxyPort}`);
  meta.push(`管理 ${st.adminPort}`);
  if (st.running && st.pid) meta.push(`PID ${st.pid}`);
  if (st.upstream) meta.push(`→ ${st.upstream}`);
  $('gwMeta').textContent = meta.join(' · ');

  // 操作按钮：按状态给出下一步，避免一屏按钮让人猜。
  const actions = $('gwActions');
  actions.innerHTML = '';
  const addBtn = (label, cls, onClick) => {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = `btn mini${cls ? ` ${cls}` : ''}`;
    btn.textContent = label;
    btn.addEventListener('click', onClick);
    actions.appendChild(btn);
    return btn;
  };
  if (!st.installed) {
    const btn = addBtn(job.running ? '安装中…' : '安装 agentgateway', 'primary', installGateway);
    if (job.running) btn.disabled = true;
  } else if (st.running) {
    addBtn('停止', '', () => gatewayAction('stop'));
    addBtn('重启', 'ghost', () => gatewayAction('restart'));
  } else {
    addBtn('启动', 'primary', () => gatewayAction('start'));
  }

  // 下载进度
  const jobBox = $('gwJob');
  if (job.running) {
    jobBox.hidden = false;
    $('gwJobPhase').textContent = job.phase || '处理中';
    const pct = job.total > 0 ? Math.min(100, (job.done / job.total) * 100) : 0;
    $('gwJobPct').textContent = job.total > 0
      ? `${pct.toFixed(1)}% · ${fmtBytes(job.done)} / ${fmtBytes(job.total)}`
      : fmtBytes(job.done);
    $('gwBar').style.width = `${pct}%`;
  } else if (job.error) {
    jobBox.hidden = false;
    $('gwJobPhase').textContent = `安装失败：${job.error}`;
    $('gwJobPct').textContent = '';
    $('gwBar').style.width = '0%';
  } else {
    jobBox.hidden = true;
  }

  // 指标卡
  const m = data.metrics;
  const tiles = $('gwTiles');
  const ok = Boolean(m && m.ok);
  const items = [
    ['请求', ok ? fmtCompact(m.requests) : '—'],
    ['输入 token', ok ? fmtCompact(m.inputTokens) : '—'],
    ['输出 token', ok ? fmtCompact(m.outputTokens) : '—'],
    ['错误', ok ? fmtCompact(m.errors) : '—'],
  ];
  const hint = !st.installed ? '装好之后开始统计' : st.running ? (m?.error || '正在采集…') : '启动后开始统计';
  tiles.innerHTML = items
    .map(([label, value]) => `<div class="gw-tile"><span>${escapeHTML(label)}</span><b class="${ok ? '' : 'muted'}">${escapeHTML(value)}</b></div>`)
    .join('') + (ok ? '' : `<p class="gw-tiles-hint">${escapeHTML(hint)}</p>`);
  tiles.classList.toggle('dim', !ok);

  // 表单回填
  const s = data.settings || {};
  fillGatewayField('gwUpstream', s.upstream || '');
  fillGatewayField('gwKey', s.upstreamKey || '');
  fillGatewayField('gwPort', s.port || '');
  fillGatewayField('gwProxyPort', s.proxyPort || '');
  fillGatewayField('gwAdminPort', s.adminPort || '');
  fillGatewayField('gwBin', s.binPath || '');
  const auto = $('gwAuto');
  if (auto && document.activeElement !== auto) auto.checked = Boolean(s.autoStart);

  const adminBtn = $('gwOpenAdmin');
  if (adminBtn) adminBtn.disabled = !st.running;

  // 终端接管：勾选框只在开关或安装状态变化时重建，避免轮询把点击打断。
  const routes = data.routes || [];
  const routeBox = $('gwRoutes');
  if (routeBox) {
    const sig = routes.map((r) => `${r.agent}:${r.enabled ? 1 : 0}`).join('|') + `:${st.installed ? 1 : 0}`;
    if (routeBox.dataset.sig !== sig) {
      routeBox.dataset.sig = sig;
      routeBox.innerHTML = '';
      for (const r of routes) {
        const row = document.createElement('label');
        row.className = 'gw-route';
        const box = document.createElement('input');
        box.type = 'checkbox';
        box.checked = Boolean(r.enabled);
        box.disabled = !st.installed;
        box.dataset.agent = r.agent;
        const name = document.createElement('span');
        name.textContent = r.label;
        if (r.verified === false) {
          const tag = document.createElement('em');
          tag.className = 'gw-unverified';
          tag.textContent = '未验证';
          name.appendChild(tag);
        }
        const env = document.createElement('small');
        if (!st.installed) {
          env.textContent = '装好网关后才能接管';
        } else {
          const via = r.via === 'proxy' ? '代理口，只统计请求数' : '业务口，token 也统计';
          env.textContent = `${r.note ? `${r.note} · ` : ''}${via}`;
        }
        row.append(box, name, env);
        box.addEventListener('change', saveGatewaySettings);
        routeBox.appendChild(row);
      }
    }
    const warn = $('gwRouteWarn');
    if (warn) warn.hidden = !routes.some((r) => r.enabled);
  }

  $('gwConfig').textContent = data.config || '—';
  const logs = data.logs || [];
  $('gwLog').textContent = logs.length ? logs.slice(-40).join('\n') : '—';
  const inline = $('gwUrlInline');
  if (inline) inline.textContent = st.url || 'http://127.0.0.1:4000';
}

async function installGateway() {
  try {
    await api.gatewayInstall();
    gatewayData = await api.gateway(false);
    paintGateway();
    startGatewayPoll();
  } catch (error) {
    alert(`安装失败：${error.message || error}`);
  }
}

async function gatewayAction(action) {
  // 停网关会把已接管的终端晾在半路，先说清楚再动手。
  if (action === 'stop' && (gatewayData?.settings?.takeover || []).length) {
    if (!confirm('停掉网关会让已接管的终端立即断线（要重开终端才能恢复）。继续？')) return;
  }
  try {
    await api.gatewayAction(action);
    // 端口释放/监听有一两秒延迟，等一小会儿再读状态，指标才有东西。
    await new Promise((resolve) => setTimeout(resolve, action === 'stop' ? 400 : 1200));
    gatewayData = await api.gateway(action !== 'stop');
    paintGateway();
  } catch (error) {
    alert(`操作失败：${error.message || error}`);
  }
}

async function saveGatewaySettings() {
  const payload = {
    autoStart: $('gwAuto').checked,
    upstream: $('gwUpstream').value.trim(),
    upstreamKey: $('gwKey').value,
    port: Number($('gwPort').value) || 0,
    proxyPort: Number($('gwProxyPort').value) || 0,
    adminPort: Number($('gwAdminPort').value) || 0,
    binPath: $('gwBin').value.trim(),
    takeover: [...document.querySelectorAll('#gwRoutes input[type="checkbox"]:checked')]
      .map((box) => box.dataset.agent),
  };
  try {
    await api.saveGateway(payload);
    gatewayData = await api.gateway(Boolean(gatewayData?.status?.running));
    paintGateway();
    const saved = $('gwSaved');
    if (saved) {
      saved.classList.add('show');
      clearTimeout(saved._t);
      saved._t = setTimeout(() => saved.classList.remove('show'), 1400);
    }
  } catch (error) {
    alert(`保存失败：${error.message || error}`);
  }
}

function openGatewayAdmin() {
  const base = gatewayData?.status?.adminUrl || 'http://127.0.0.1:15000';
  window.open(`${base}/ui/`, '_blank', 'noopener');
}

// 一次性绑定控件；页面脚本在 body 末尾加载，DOM 已就绪。
$('gwSave')?.addEventListener('click', saveGatewaySettings);
$('gwAuto')?.addEventListener('change', saveGatewaySettings);
$('gwRestart')?.addEventListener('click', () => gatewayAction('restart'));
$('gwOpenAdmin')?.addEventListener('click', openGatewayAdmin);
