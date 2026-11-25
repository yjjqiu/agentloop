// AgentDeck 主界面：项目/终端/CLI 设置/主题/窗口外壳。
// 看板逻辑在 board.js，品牌头像在 avatars.js，三者在 index.html 里按顺序加载。

const shellApi = window.agentdeckShell || null;

// —— 数据接口 ——
async function req(path, options) {
  // 令牌没输入完之前先等着，别让 3 秒轮询把 401 刷满控制台。
  if (authPromptPromise) await authPromptPromise;
  const res = await fetch(path, { headers: { 'Content-Type': 'application/json' }, ...options });
  if (res.status === 401 && !path.startsWith('/api/v1/auth/')) {
    await ensureAuth();
    return req(path, options);
  }
  if (!res.ok) {
    let message = res.statusText;
    try {
      const body = await res.json();
      message = body.error || message;
    } catch { /* 非 JSON 错误体 */ }
    throw new Error(message);
  }
  if (res.status === 204) return {};
  return res.json();
}

// —— 访问令牌 ——
// 服务对外开放时，第一次请求会拿到 401，这里弹出输入框，验证后重试原请求。
let authPromptPromise = null;

function ensureAuth() {
  if (authPromptPromise) return authPromptPromise;
  authPromptPromise = new Promise((resolve) => {
    const modal = $('authModal');
    const input = $('authTokenInput');
    const errorEl = $('authError');
    modal.hidden = false;
    errorEl.hidden = true;
    input.value = '';
    setTimeout(() => input.focus(), 60);

    const submit = async () => {
      const token = input.value.trim();
      if (!token) {
        errorEl.textContent = '请输入访问令牌';
        errorEl.hidden = false;
        return;
      }
      try {
        const res = await fetch('/api/v1/auth/login', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ token }),
        });
        const body = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(body.error || '令牌不正确');
        modal.hidden = true;
        authPromptPromise = null;
        resolve();
      } catch (error) {
        errorEl.textContent = error.message || '验证失败';
        errorEl.hidden = false;
        input.select();
      }
    };

    $('authSubmit').onclick = submit;
    input.onkeydown = (event) => {
      if (event.key === 'Enter') submit();
      if (event.key === 'Escape') input.blur();
    };
  });
  return authPromptPromise;
}

const api = {
  config: () => req('/api/v1/config'),
  saveConfig: (patch) => req('/api/v1/config', { method: 'POST', body: JSON.stringify(patch) }),
  projects: () => req('/api/v1/projects'),
  setActiveProject: (path) => req('/api/v1/projects/active', { method: 'POST', body: JSON.stringify({ path }) }),
  removeProject: (path) => req('/api/v1/projects/remove', { method: 'POST', body: JSON.stringify({ path }) })
    .then(() => api.projects()),
  addProject: async () => {
    const dir = await api.pickDir();
    if (!dir) return { canceled: true };
    await req('/api/v1/projects', { method: 'POST', body: JSON.stringify({ path: dir }) });
    return { canceled: false, active: dir };
  },
  addProjectPath: (path) => req('/api/v1/projects', { method: 'POST', body: JSON.stringify({ path }) })
    .then(() => ({ ok: true }))
    .catch(() => ({ ok: false })),
  workflows: (root) => req(`/api/v1/workflows?root=${encodeURIComponent(root || '')}`),
  listClis: () => req('/api/v1/clis').then((r) => r.clis || []),
  cliVersions: (refresh) => req(`/api/v1/clis/versions${refresh ? '?refresh=1' : ''}`).then((r) => r.versions || {}),
  cliRelease: (id, refresh) => req(`/api/v1/clis/release?id=${encodeURIComponent(id)}${refresh ? '&refresh=1' : ''}`).then((r) => r.info || null),
  cliInstall: (id) => req('/api/v1/clis/install', { method: 'POST', body: JSON.stringify({ id }) }),
  cliJob: () => req('/api/v1/clis/job').then((r) => r.job || null),
  cliJobCancel: () => req('/api/v1/clis/job/cancel', { method: 'POST' }),
  saveCli: (payload) => req('/api/v1/clis/config', { method: 'POST', body: JSON.stringify(payload) }),
  resetCli: (id) => req('/api/v1/clis/reset', { method: 'POST', body: JSON.stringify({ id }) }),
  installLocal: (payload) => req('/api/v1/install', { method: 'POST', body: JSON.stringify(payload) }),
  sessions: () => req('/api/v1/sessions').then((r) => r.sessions || []),
  spawn: (options) => req('/api/v1/sessions', { method: 'POST', body: JSON.stringify(options) })
    .then((r) => ({ ok: true, ...r.session }))
    .catch((error) => ({ ok: false, error: String(error.message || error) })),
  write: (id, data) => req(`/api/v1/sessions/${encodeURIComponent(id)}/input`, {
    method: 'POST',
    body: JSON.stringify({ data }),
  }).catch(() => {}),
  resize: (id, cols, rows) => req(
    `/api/v1/sessions/${encodeURIComponent(id)}/resize?cols=${cols}&rows=${rows}`,
    { method: 'POST' },
  ).catch(() => {}),
  kill: (id) => req(`/api/v1/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' }).catch(() => {}),
  listDirs: (path) => req(`/api/v1/fs/list?path=${encodeURIComponent(path || '')}`),
  mkdirDir: (parent, name) => req('/api/v1/fs/mkdir', {
    method: 'POST',
    body: JSON.stringify({ parent, name }),
  }),
  pickDir: () => (shellApi?.pickDir ? shellApi.pickDir() : openDirPicker(state.active || dirCurrent)),
};

const $ = (id) => document.getElementById(id);
const projListEl = $('projList');
const terminalHost = $('terminal');
const emptyState = $('emptyState');
const topbarHint = $('topbarHint');
const modal = $('modal');
const cliGrid = $('cliGrid');
const cwdInput = $('cwd');
const customInput = $('custom');

const panel = $('panel');
const panelPath = $('panelPath');
const wfSelect = $('wfSelect');
const wfMeta = $('wfMeta');
const wfStats = $('wfStats');
const wfGraph = $('wfGraph');
const wfEmpty = $('wfEmpty');
const wfDetail = $('wfDetail');
const wfInstalled = $('wfInstalled');

const termBar = document.querySelector('.term-bar');
const termTitle = $('termTitle');
const termAva = $('termAva');
const termDot = $('termDot');
const termCwd = $('termCwd');
const termBadge = $('termBadge');
const termRestart = $('termRestart');
const termClose = $('termClose');

const sessions = new Map();
let activeId = '';
let selectedCli = '';
let clis = [];

const state = {
  projects: [],
  active: '',
  selectedRun: '',
  selectedStage: '',
  timer: null,
  signature: '',
};

// —— 主题 ——
// 终端配色只影响终端内容区，和软件整包配色互不影响。
const TERM_THEMES = {
  midnight: {
    name: '暗夜',
    note: '默认，深色底 + 紫色强调',
    theme: {
      background: '#0a0b0f', foreground: '#e9ebf0', cursor: '#7b61ff', cursorAccent: '#0a0b0f',
      selectionBackground: 'rgba(123, 97, 255, .3)',
      black: '#171b22', red: '#ff6b6b', green: '#3ddc97', yellow: '#f3b64b',
      blue: '#7c8cff', magenta: '#a78bfa', cyan: '#5eead4', white: '#e9ebf0',
      brightBlack: '#5b6170', brightRed: '#ff8f8f', brightGreen: '#6ff0b7', brightYellow: '#ffd479',
      brightBlue: '#a3afff', brightMagenta: '#c4b5fd', brightCyan: '#8ff2e6', brightWhite: '#ffffff',
    },
  },
  graphite: {
    name: '石墨',
    note: '中性深灰，长时间阅读更稳',
    theme: {
      background: '#121316', foreground: '#e3e4e9', cursor: '#9aa0ae', cursorAccent: '#121316',
      selectionBackground: 'rgba(154, 160, 174, .28)',
      black: '#1c1d21', red: '#e06c75', green: '#98c379', yellow: '#e5c07b',
      blue: '#61afef', magenta: '#c678dd', cyan: '#56b6c2', white: '#e3e4e9',
      brightBlack: '#5c6370', brightRed: '#ff7b86', brightGreen: '#b4e08e', brightYellow: '#f2d18a',
      brightBlue: '#82c4ff', brightMagenta: '#d99bf0', brightCyan: '#78d0db', brightWhite: '#ffffff',
    },
  },
  violet: {
    name: '紫夜',
    note: '深紫底，和界面强调色呼应',
    theme: {
      background: '#14101f', foreground: '#e8e2ff', cursor: '#b9a6ff', cursorAccent: '#14101f',
      selectionBackground: 'rgba(185, 166, 255, .26)',
      black: '#221a33', red: '#ff7a90', green: '#56d6a9', yellow: '#f2c66d',
      blue: '#a48bff', magenta: '#d0a2ff', cyan: '#7fd6e8', white: '#e8e2ff',
      brightBlack: '#6b5f88', brightRed: '#ff9dae', brightGreen: '#7fe8c2', brightYellow: '#ffdd96',
      brightBlue: '#c0adff', brightMagenta: '#e3c4ff', brightCyan: '#a3e6f2', brightWhite: '#ffffff',
    },
  },
  ocean: {
    name: '深海',
    note: '蓝绿底，青蓝高亮',
    theme: {
      background: '#071218', foreground: '#d7ecf5', cursor: '#56d6e8', cursorAccent: '#071218',
      selectionBackground: 'rgba(86, 214, 232, .24)',
      black: '#0d2029', red: '#ff7b72', green: '#4fd1a5', yellow: '#e8c15a',
      blue: '#5aa9ff', magenta: '#c792ea', cyan: '#6fe3d0', white: '#d7ecf5',
      brightBlack: '#4d6b78', brightRed: '#ff9c95', brightGreen: '#74e3bd', brightYellow: '#f5d77e',
      brightBlue: '#85bfff', brightMagenta: '#dbb2f5', brightCyan: '#98f0e3', brightWhite: '#ffffff',
    },
  },
  solarized: {
    name: '暖沙',
    note: 'Solarized 深色，低对比护眼',
    theme: {
      background: '#002b36', foreground: '#93a1a1', cursor: '#93a1a1', cursorAccent: '#002b36',
      selectionBackground: 'rgba(147, 161, 161, .22)',
      black: '#073642', red: '#dc322f', green: '#859900', yellow: '#b58900',
      blue: '#268bd2', magenta: '#d33682', cyan: '#2aa198', white: '#eee8d5',
      brightBlack: '#586e75', brightRed: '#cb4b16', brightGreen: '#93a1a1', brightYellow: '#657b83',
      brightBlue: '#839496', brightMagenta: '#6c71c4', brightCyan: '#93a1a1', brightWhite: '#fdf6e3',
    },
  },
  paper: {
    name: '纸白',
    note: '浅色底，适合白天和亮屏',
    theme: {
      background: '#fbfbfd', foreground: '#24292f', cursor: '#7b61ff', cursorAccent: '#fbfbfd',
      selectionBackground: 'rgba(123, 97, 255, .18)',
      black: '#24292f', red: '#cf222e', green: '#1a7f37', yellow: '#9a6700',
      blue: '#0969da', magenta: '#8250df', cyan: '#1b7c83', white: '#6e7781',
      brightBlack: '#57606a', brightRed: '#a40e26', brightGreen: '#2da44e', brightYellow: '#bf8700',
      brightBlue: '#218bff', brightMagenta: '#a475f9', brightCyan: '#3192aa', brightWhite: '#8c959f',
    },
  },
};

let termThemeId = 'midnight';
try {
  const saved = localStorage.getItem('agentdeck.termTheme');
  if (saved && TERM_THEMES[saved]) termThemeId = saved;
} catch { /* 无 localStorage 时用默认 */ }

function termTheme() {
  return (TERM_THEMES[termThemeId] || TERM_THEMES.midnight).theme;
}

// 切换配色：更新所有已打开的终端，并记住选择。
function applyTermTheme(id) {
  if (!TERM_THEMES[id]) return;
  termThemeId = id;
  try { localStorage.setItem('agentdeck.termTheme', id); } catch { /* 忽略 */ }
  const theme = termTheme();
  document.documentElement.style.setProperty('--term-bg', theme.background);
  for (const session of sessions.values()) {
    try { session.term.options.theme = theme; } catch { /* 旧版 xterm 不支持热切换 */ }
  }
  renderTermThemes();
}

function renderTermThemes() {
  const box = $('termThemes');
  if (!box) return;
  box.innerHTML = '';
  for (const [id, item] of Object.entries(TERM_THEMES)) {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = `term-theme${id === termThemeId ? ' active' : ''}`;
    btn.dataset.theme = id;
    const preview = document.createElement('span');
    preview.className = 'tt-preview';
    preview.style.background = item.theme.background;
    preview.style.color = item.theme.foreground;
    preview.textContent = 'Aa 终端';
    const dots = document.createElement('span');
    dots.className = 'tt-dots';
    for (const color of [item.theme.red, item.theme.green, item.theme.blue, item.theme.yellow]) {
      const dot = document.createElement('i');
      dot.style.background = color;
      dots.appendChild(dot);
    }
    const meta = document.createElement('span');
    meta.className = 'tt-meta';
    const title = document.createElement('b');
    title.textContent = item.name;
    const note = document.createElement('small');
    note.textContent = item.note;
    meta.append(title, note);
    btn.append(preview, meta, dots);
    btn.addEventListener('click', () => applyTermTheme(id));
    box.appendChild(btn);
  }
}

document.documentElement.style.setProperty('--term-bg', termTheme().background);
renderTermThemes();

// 整包配色：同一套布局，浅色 / 夜间共 9 套，画布、表面、边框和强调色一起换。
const APP_THEMES = {
  violet: { name: '紫罗兰', note: '默认，紫调路线图', accent: '#7b61ff', canvas: '#f4f4f7', board: '#fbfbfd', surface: '#ffffff', dark: false },
  ocean: { name: '海盐蓝', note: '冷静的蓝调', accent: '#2f7fd1', canvas: '#eaf1f8', board: '#f4f9fd', surface: '#fbfdff', dark: false },
  forest: { name: '苔原绿', note: '低饱和绿调', accent: '#2f8a63', canvas: '#eaf2ea', board: '#f4f9f4', surface: '#fbfdfb', dark: false },
  amber: { name: '暖陶橙', note: '暖调纸面', accent: '#b56a2d', canvas: '#f4ede4', board: '#fbf5ed', surface: '#fffdfa', dark: false },
  rose: { name: '樱花粉', note: '柔和的玫红', accent: '#c04f7d', canvas: '#f5ecf1', board: '#fbf3f7', surface: '#fffbfd', dark: false },
  slate: { name: '石墨灰', note: '中性、克制', accent: '#4f5d75', canvas: '#eceff3', board: '#f5f7f9', surface: '#fcfdfe', dark: false },
  midnight: { name: '深空', note: '夜间模式，中性深色', accent: '#8f7bff', canvas: '#0e1015', board: '#12151b', surface: '#171a21', dark: true },
  navy: { name: '午夜蓝', note: '深蓝底，冷色夜间', accent: '#4c9fff', canvas: '#0a111a', board: '#0d151e', surface: '#121b26', dark: true },
  pine: { name: '松林夜', note: '墨绿底，柔和的暗色', accent: '#4fbe8b', canvas: '#0b1310', board: '#0f1713', surface: '#131d18', dark: true },
};

let appThemeId = 'violet';
try {
  const saved = localStorage.getItem('agentdeck.appTheme');
  if (saved && APP_THEMES[saved]) appThemeId = saved;
} catch { /* 无 localStorage 时用默认 */ }

function applyAppTheme(id) {
  if (!APP_THEMES[id]) return;
  appThemeId = id;
  try { localStorage.setItem('agentdeck.appTheme', id); } catch { /* 忽略 */ }
  document.documentElement.dataset.appTheme = id;
  renderAppThemes();
}

function renderAppThemes() {
  const groups = [
    { box: $('appThemesLight'), dark: false },
    { box: $('appThemesDark'), dark: true },
  ];
  for (const { box, dark } of groups) {
    if (!box) continue;
    box.innerHTML = '';
    for (const [id, item] of Object.entries(APP_THEMES)) {
      if (Boolean(item.dark) !== dark) continue;
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = `app-theme${id === appThemeId ? ' active' : ''}`;
      btn.dataset.theme = id;

      const preview = document.createElement('span');
      preview.className = 'at-preview';
      preview.style.background = item.canvas;
      const card = document.createElement('span');
      card.className = 'at-card';
      card.style.background = item.surface;
      const barColor = dark ? 'rgba(255,255,255,.26)' : 'rgba(24,24,27,.12)';
      const first = document.createElement('i');
      const second = document.createElement('i');
      first.style.background = barColor;
      second.style.background = barColor;
      card.append(first, second);
      const side = document.createElement('span');
      side.className = 'at-side';
      const blockColor = dark ? 'rgba(255,255,255,.18)' : 'rgba(24,24,27,.1)';
      for (let i = 0; i < 2; i += 1) {
        const block = document.createElement('i');
        block.style.background = blockColor;
        side.appendChild(block);
      }
      const pill = document.createElement('span');
      pill.className = 'at-pill';
      pill.style.background = item.accent;
      preview.append(card, side, pill);

      const meta = document.createElement('span');
      meta.className = 'at-meta';
      const text = document.createElement('span');
      text.className = 'at-meta-text';
      const title = document.createElement('b');
      title.textContent = item.name;
      const note = document.createElement('small');
      note.textContent = item.note;
      text.append(title, note);
      const dot = document.createElement('span');
      dot.className = 'at-dot';
      dot.style.background = item.accent;
      meta.append(text, dot);

      btn.append(preview, meta);
      btn.addEventListener('click', () => applyAppTheme(id));
      box.appendChild(btn);
    }
  }
}

document.documentElement.dataset.appTheme = appThemeId;
renderAppThemes();

// —— 通用工具 ——
// 各 CLI 的启动命令由 daemon 按「设置」生成（含默认参数），这里只做兜底。
// Claude Code 的内置参数就是 --dangerously-skip-permissions，见 agents.json。
function cliCommand(cli) {
  if (cli.command) return cli.command;
  const base = cli.path && /\s/.test(cli.path) ? `"${cli.path}"` : (cli.path || cli.bin);
  return base;
}

function shortName(command) {
  const first = splitCommand(command)[0] || 'terminal';
  const base = first.replace(/\\/g, '/').split('/').pop();
  return base.replace(/\.(exe|cmd|bat)$/i, '') || 'terminal';
}

const STAGES = [
  { key: 'TASK-01', label: '需求', agent: 'analyst' },
  { key: 'TASK-02', label: '方案', agent: 'architect' },
  { key: 'TASK-03', label: '开发', agent: 'developer' },
  { key: 'CODE-REVIEW', label: '审查', agent: 'reviewer' },
  { key: 'TASK-04', label: '测试', agent: 'tester' },
  { key: 'TASK-05', label: '沉淀', agent: 'knowledge' },
];

function relTime(iso) {
  if (!iso) return '';
  const then = new Date(iso).getTime();
  if (!Number.isFinite(then)) return '';
  const minutes = Math.max(0, Math.round((Date.now() - then) / 60000));
  if (minutes < 1) return '刚刚';
  if (minutes < 60) return `${minutes} 分钟`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours} 小时`;
  return `${Math.round(hours / 24)} 天`;
}

function avatarColor(name) {
  const palette = ['#5b8cff', '#8b5cf6', '#22d3ee', '#34d399', '#fbbf24', '#f87171'];
  let hash = 0;
  for (const ch of String(name)) hash = (hash * 31 + ch.codePointAt(0)) % 997;
  return palette[hash % palette.length];
}

// —— 左栏：项目 + 运行 ——
const ICON = {
  running: '<span class="spinner"></span>',
  failed: '<span class="run-icon failed">!</span>',
  completed: '<span class="run-icon completed" aria-hidden="true"><svg viewBox="0 0 16 16" width="13" height="13"><path d="M3 8h4a4 4 0 0 1 4 4v1M3 3v5m0 0V3" stroke="currentColor" stroke-width="1.4" fill="none" stroke-linecap="round"/><circle cx="11" cy="13" r="1.5" fill="currentColor"/><circle cx="11" cy="3" r="1.5" fill="currentColor"/></svg></span>',
  pending: '<span class="run-icon pending"></span>',
};

function renderRail() {
  projListEl.innerHTML = '';
  if (!state.projects.length) {
    const hint = document.createElement('div');
    hint.className = 'rail-empty';
    hint.textContent = '还没有项目，点右上角 + 添加';
    projListEl.appendChild(hint);
    return;
  }

  for (const project of state.projects) {
    const active = project.path === state.active;
    const row = document.createElement('div');
    row.className = `proj${active ? ' active' : ''}`;
    row.innerHTML = `
      <span class="proj-avatar"></span>
      <span class="proj-name"></span>
      <span class="proj-count"></span>
      <button class="proj-more" type="button" title="移除项目">⋮</button>`;
    const avatar = row.querySelector('.proj-avatar');
    avatar.textContent = String(project.name).slice(0, 1).toUpperCase();
    avatar.style.background = avatarColor(project.name);
    row.querySelector('.proj-name').textContent = project.name;
    row.querySelector('.proj-count').textContent = project.runs?.length ? String(project.runs.length) : '';

    row.addEventListener('click', async (event) => {
      if (event.target.classList.contains('proj-more')) return;
      if (project.path === state.active) {
        // 已选中的项目：回到它的首页（看板）。
        if (typeof switchTab === 'function') switchTab('board');
        return;
      }
      state.active = project.path;
      state.selectedRun = '';
      state.selectedStage = '';
      await api.setActiveProject(project.path);
      renderRail();
      renderPanel();
      // 项目首页就是该项目的看板。
      if (typeof switchTab === 'function') switchTab('board');
    });

    row.querySelector('.proj-more').addEventListener('click', (event) => {
      event.stopPropagation();
      openProjectMenu(project, event.currentTarget);
    });

    projListEl.appendChild(row);

    if (!active) continue;

    // 终端：按 cwd 归属到项目
    const mine = [...sessions.values()].filter((s) => isUnder(s.cwd, project.path));
    projListEl.appendChild(subHead('终端'));
    if (!mine.length) {
      projListEl.appendChild(note('新建工作流或按 Ctrl T 后，这里会出现终端'));
    }
    for (const session of mine) {
      projListEl.appendChild(termRow(session));
    }

    // 工作流：LoopForge 任务（排队 / 进行中 / 已完成都在这里）
    projListEl.appendChild(subHead('工作流'));
    if (!project.runs?.length) {
      projListEl.appendChild(note('还没有工作流 · 点右侧新建流水线'));
    }
    for (const run of project.runs || []) {
      const item = document.createElement('div');
      item.className = `run${run.slug === state.selectedRun ? ' selected' : ''}`;
      item.innerHTML = `
        <span class="run-status"></span>
        <span class="run-main">
          <span class="run-line"><span class="run-title"></span><span class="run-time"></span></span>
          <span class="run-track"></span>
        </span>`;
      item.querySelector('.run-status').innerHTML = ICON[run.status] || ICON.pending;
      const title = item.querySelector('.run-title');
      title.textContent = run.taskId || run.slug;
      title.title = `${run.taskId || run.slug} · ${run.currentStage || ''}`;
      item.querySelector('.run-time').textContent = relTime(run.updatedAt);
      const track = item.querySelector('.run-track');
      for (const stage of STAGES) {
        const status = stageStatus(run, stage.key);
        const seg = document.createElement('i');
        seg.className = status;
        seg.title = `${stage.label} · ${status}`;
        track.appendChild(seg);
      }
      item.addEventListener('click', () => {
        state.selectedRun = run.slug;
        state.selectedStage = '';
        renderRail();
        renderPanel();
        // 运行列表点开的是这个项目自己的看板。
        if (typeof openRunBoard === 'function') openRunBoard(project.path, run.slug);
      });
      projListEl.appendChild(item);
    }
  }

  // 不属于任何项目的终端
  const orphans = [...sessions.values()].filter(
    (s) => !state.projects.some((p) => isUnder(s.cwd, p.path)),
  );
  if (orphans.length) {
    projListEl.appendChild(subHead('其他终端'));
    for (const session of orphans) {
      projListEl.appendChild(termRow(session));
    }
  }
}

// —— 终端头像 ——
// 会话的 CLI 身份：流水线派发时 daemon 会写入 agent，手动新建时按启动命令里的 bin 猜。
function sessionAgentId(session) {
  if (!session) return '';
  if (session.agent) return session.agent;
  const agents = typeof boardState !== 'undefined' ? boardState.agents : [];
  if (!agents || !agents.length || typeof agentIdFromCommand !== 'function') return '';
  return agentIdFromCommand(session.command || session.label || '', agents);
}

function agentNameOf(id) {
  const agents = typeof boardState !== 'undefined' ? boardState.agents : [];
  const hit = Array.isArray(agents) ? agents.find((a) => a.id === id) : null;
  return hit ? hit.name : (id || '');
}

// 会话状态 → 头像动效状态。
function sessionAvatarState(session) {
  const state = session?.state || '';
  if (state === 'running') return 'running';
  if (state === 'error') return 'error';
  if (state === 'waiting') return 'waiting';
  return 'idle';
}

function sessionAvatarHTML(session, size) {
  const id = sessionAgentId(session);
  if (!id || typeof agentAvatarHTML !== 'function') return '';
  return agentAvatarHTML(id, {
    size,
    state: sessionAvatarState(session),
    label: agentNameOf(id),
  });
}

function termRow(session) {
  const item = document.createElement('div');
  item.className = `term${session.id === activeId ? ' selected' : ''}`;
  item.dataset.state = session.state || 'running';
  item.innerHTML = `
    <span class="term-ava"></span>
    <span class="term-name"></span>
    <button class="term-close" type="button" title="关闭">×</button>`;
  const avaEl = item.querySelector('.term-ava');
  const avatar = sessionAvatarHTML(session, 16);
  if (avatar) avaEl.innerHTML = avatar;
  const nameEl = item.querySelector('.term-name');
  nameEl.textContent = session.title;
  nameEl.title = `${session.title}\n${session.label}\n${session.cwd}`;
  item.addEventListener('click', async () => {
    // 终端可能属于另一个项目，先把它所在的项目切为当前项目。
    const owner = state.projects.find((p) => isUnder(session.cwd, p.path));
    if (owner && owner.path !== state.active) {
      state.active = owner.path;
      state.selectedRun = '';
      state.selectedStage = '';
      await api.setActiveProject(owner.path);
      renderRail();
      renderPanel();
    }
    // 点左侧终端才进入终端对话页。
    if (typeof enterTerminal === 'function') enterTerminal(session.id);
    else setActive(session.id);
  });
  item.querySelector('.term-close').addEventListener('click', (event) => {
    event.stopPropagation();
    api.kill(session.id);
    removeSession(session.id);
  });
  return item;
}

function isUnder(cwd, root) {
  const norm = (value) => String(value || '').replace(/\\/g, '/').replace(/\/+$/, '').toLowerCase();
  const child = norm(cwd);
  const parent = norm(root);
  if (!child || !parent) return false;
  return child === parent || child.startsWith(`${parent}/`);
}

function subHead(label) {
  const head = document.createElement('div');
  head.className = 'rail-sub';
  const span = document.createElement('span');
  span.textContent = label;
  head.appendChild(span);
  return head;
}

function note(text) {
  const el = document.createElement('div');
  el.className = 'rail-note';
  el.textContent = text;
  return el;
}

// —— 项目「⋮」菜单：二级入口 + 删除确认，避免误点直接移除 ——
let projectMenuEl = null;

function closeProjectMenu() {
  if (projectMenuEl) {
    projectMenuEl.remove();
    projectMenuEl = null;
  }
}

function openProjectMenu(project, anchor) {
  closeProjectMenu();
  const rect = anchor.getBoundingClientRect();
  const menu = document.createElement('div');
  menu.className = 'pop-menu';
  menu.style.top = `${rect.bottom + 4}px`;
  menu.style.left = `${Math.max(8, rect.right - 160)}px`;
  const remove = document.createElement('button');
  remove.type = 'button';
  remove.className = 'danger';
  remove.textContent = '移除项目';
  remove.addEventListener('click', async () => {
    closeProjectMenu();
    const ok = await askConfirm({
      title: `移除项目「${project.name}」？`,
      text: `只会从 AgentDeck 的项目列表里移除，${project.path} 目录和里面的文件不会被删除。`,
      okText: '移除',
      danger: true,
    });
    if (!ok) return;
    try {
      const result = await api.removeProject(project.path);
      state.projects = (result.projects || []).map((p) => ({ ...p, runs: [] }));
      state.active = result.active || '';
      await refresh(true);
    } catch (error) {
      alert(`移除失败：${error.message || error}`);
    }
  });
  menu.appendChild(remove);
  document.body.appendChild(menu);
  projectMenuEl = menu;
}
document.addEventListener('click', closeProjectMenu);

// —— 通用确认框 ——
let confirmResolve = null;

function askConfirm({ title, text, okText = '确定', danger = false }) {
  return new Promise((resolve) => {
    confirmResolve = resolve;
    $('confirmTitle').textContent = title;
    $('confirmText').textContent = text;
    const yes = $('confirmYes');
    yes.textContent = okText;
    yes.classList.toggle('danger', danger);
    $('confirmModal').hidden = false;
    setTimeout(() => yes.focus(), 30);
  });
}

function settleConfirm(ok) {
  $('confirmModal').hidden = true;
  const resolve = confirmResolve;
  confirmResolve = null;
  if (resolve) resolve(ok);
}

// —— 目录选择（浏览器模式）——
// 桌面端用 Electron 的原生选择框；服务器上只能浏览服务端目录，所以走 /api/v1/fs/list。
let dirPickerResolve = null;
let dirCurrent = '';

function openDirPicker(initial) {
  return new Promise((resolve) => {
    dirPickerResolve = resolve;
    $('dirModal').hidden = false;
    $('dirError').hidden = true;
    $('dirNewRow').hidden = true;
    $('dirNewName').value = '';
    loadDir(initial || '');
  });
}

function closeDirPicker(value) {
  $('dirModal').hidden = true;
  const resolve = dirPickerResolve;
  dirPickerResolve = null;
  if (resolve) resolve(value || '');
}

async function loadDir(path) {
  const errorEl = $('dirError');
  try {
    const listing = await api.listDirs(path);
    dirCurrent = listing.path;
    renderDirRoots(listing.roots || []);
    $('dirPath').value = listing.path;
    $('dirUp').disabled = !listing.parent;
    $('dirUp').dataset.parent = listing.parent || '';
    const list = $('dirList');
    list.textContent = '';
    if (!listing.dirs?.length) {
      const empty = document.createElement('p');
      empty.className = 'dir-empty';
      empty.textContent = '这里没有子目录，可以直接选择，或者新建一个';
      list.appendChild(empty);
    }
    for (const entry of listing.dirs || []) {
      const item = document.createElement('button');
      item.type = 'button';
      item.className = 'dir-item';
      item.innerHTML = '<span class="dir-ico">▸</span><span class="dir-name"></span>';
      item.querySelector('.dir-name').textContent = entry.name;
      item.title = entry.path;
      item.addEventListener('click', () => loadDir(entry.path));
      list.appendChild(item);
    }
    errorEl.hidden = true;
  } catch (error) {
    errorEl.textContent = String(error.message || error);
    errorEl.hidden = false;
  }
}

// renderDirRoots 渲染快捷入口：工作区和容器里挂进来的宿主机项目目录。
function renderDirRoots(roots) {
  const box = $('dirRoots');
  box.textContent = '';
  if (!roots.length) {
    box.hidden = true;
    return;
  }
  box.hidden = false;
  for (const root of roots) {
    const chip = document.createElement('button');
    chip.type = 'button';
    chip.className = 'dir-root';
    chip.textContent = root.name;
    chip.title = root.path;
    chip.addEventListener('click', () => loadDir(root.path));
    box.appendChild(chip);
  }
}

async function createDir() {
  const name = $('dirNewName').value.trim();
  if (!name) return;
  try {
    const res = await api.mkdirDir(dirCurrent, name);
    $('dirNewRow').hidden = true;
    await loadDir(res.path);
  } catch (error) {
    const errorEl = $('dirError');
    errorEl.textContent = String(error.message || error);
    errorEl.hidden = false;
  }
}

// —— 右栏：阶段图 ——
const NODE_W = 150;
const NODE_H = 66;
const GAP_X = 30;
const GAP_Y = 58;
const PAD = 16;

function currentProject() {
  return state.projects.find((p) => p.path === state.active);
}

function currentRun() {
  const project = currentProject();
  return project?.runs?.find((r) => r.slug === state.selectedRun) || project?.runs?.[0] || null;
}

function stageStatus(workflow, key) {
  const stage = workflow?.stages?.[key];
  if (!stage) return 'pending';
  const status = String(stage.status || '').toLowerCase();
  if (status === 'completed' || status === 'passed') return 'completed';
  if (status === 'in_progress' || status === 'running') return 'running';
  if (status === 'failed' || status === 'blocked') return 'failed';
  return 'pending';
}

function stat(text, tone) {
  const el = document.createElement('span');
  el.className = `stat${tone ? ` ${tone}` : ''}`;
  el.textContent = text;
  return el;
}

function renderStats(run) {
  const statuses = STAGES.map((stage) => stageStatus(run, stage.key));
  const done = statuses.filter((s) => s === 'completed').length;
  const running = statuses.filter((s) => s === 'running').length;
  const failed = statuses.filter((s) => s === 'failed').length;
  const reworks = Number(run.stages?.['TASK-03']?.rework_round || 0);

  wfStats.innerHTML = '';
  wfStats.appendChild(stat(`${done}/${STAGES.length} 阶段`));
  if (running) wfStats.appendChild(stat(`${running} 进行中`, 'live'));
  if (reworks) wfStats.appendChild(stat(`打回 ${reworks} 次`, 'warn'));
  else if (failed) wfStats.appendChild(stat(`${failed} 失败`, 'warn'));
  if (run.updatedAt) wfStats.appendChild(stat(`更新 ${relTime(run.updatedAt)}`));
  wfStats.hidden = false;
}

function renderPanel() {
  if (typeof updateProjectHeader === 'function') updateProjectHeader();
  const project = currentProject();
  panelPath.textContent = project ? project.path : '';
  wfInstalled.textContent = project?.installed
    ? `Claude ${project.installed.claude ? '✓' : '—'} · Codex ${project.installed.codex ? '✓' : '—'}`
    : '';

  const run = currentRun();
  if (!project) {
    wfSelect.hidden = true;
    wfMeta.textContent = '';
    wfEmpty.hidden = false;
    wfEmpty.querySelector('p').textContent = '还没有项目';
    wfEmpty.querySelector('small').textContent = '点左侧项目区的 + 添加一个项目';
    wfDetail.hidden = true;
    wfStats.hidden = true;
    wfStats.innerHTML = '';
    wfGraph.querySelector('svg')?.remove();
    return;
  }

  if (!run) {
    wfSelect.hidden = true;
    wfMeta.textContent = '';
    wfEmpty.hidden = false;
    wfEmpty.querySelector('p').textContent = '还没有 LoopForge 任务';
    wfEmpty.querySelector('small').textContent = '在项目里跑 /start-devflow 之后，这里会显示各阶段状态';
    wfDetail.hidden = true;
    wfStats.hidden = true;
    wfStats.innerHTML = '';
    wfGraph.querySelector('svg')?.remove();
    return;
  }

  if (state.selectedRun !== run.slug) state.selectedRun = run.slug;

  wfSelect.innerHTML = '';
  for (const item of project.runs) {
    const opt = document.createElement('option');
    opt.value = item.slug;
    opt.textContent = `${item.taskId || item.slug} · ${item.currentStage || '—'} · ${relTime(item.updatedAt)}`;
    wfSelect.appendChild(opt);
  }
  wfSelect.value = run.slug;
  wfSelect.hidden = false;
  wfMeta.textContent = `${run.sizeClass || ''}${run.runMode ? ' · ' + run.runMode : ''}${run.total ? ` · ${run.completed}/${run.total}` : ''}`;
  renderStats(run);

  wfEmpty.hidden = true;
  wfGraph.querySelector('svg')?.remove();
  drawGraph(run);
}

function drawGraph(workflow, target = wfGraph, compact = false) {
  const perRow = compact ? STAGES.length : 3;
  // 阶段 → CLI，用来在节点上画品牌头像（看板阶段图）。board.js 可能还没加载。
  const stageCfg = (() => {
    try {
      const project = typeof currentProject === 'function' ? currentProject() : null;
      return project && typeof pipelineOf === 'function' ? pipelineOf(project.path).config : null;
    } catch { return null; }
  })();
  const agentOfStage = (key) => (stageCfg && typeof stageAgentId === 'function' ? stageAgentId(stageCfg, key) : '');
  const positions = STAGES.map((stage, index) => {
    const row = compact ? 0 : Math.floor(index / perRow);
    const col = compact ? index : index % perRow;
    return { ...stage, x: PAD + col * (NODE_W + GAP_X), y: PAD + row * (NODE_H + GAP_Y) };
  });

  const rows = compact ? 1 : 2;
  const width = PAD * 2 + perRow * NODE_W + (perRow - 1) * GAP_X;
  const height = PAD * 2 + rows * NODE_H + (rows - 1) * GAP_Y + (compact ? 28 : 0);

  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('viewBox', `0 0 ${width} ${height}`);
  svg.setAttribute('width', String(width));
  svg.setAttribute('height', String(height));
  svg.style.maxWidth = '100%';

  const defs = document.createElementNS('http://www.w3.org/2000/svg', 'defs');
  defs.innerHTML = `
    <marker id="arrow" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto">
      <path d="M0,0 L8,4 L0,8 z" fill="#dcdbe3"/>
    </marker>
    <marker id="arrowRed" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto">
      <path d="M0,0 L8,4 L0,8 z" fill="#c15161"/>
    </marker>`;
  svg.appendChild(defs);

  for (let i = 0; i < positions.length - 1; i += 1) {
    const from = positions[i];
    const to = positions[i + 1];
    const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
    let d;
    if (from.y === to.y) {
      const x1 = from.x + NODE_W;
      const x2 = to.x;
      const mid = (x1 + x2) / 2;
      const y = from.y + NODE_H / 2;
      d = `M ${x1} ${y} C ${mid} ${y}, ${mid} ${y}, ${x2} ${y}`;
    } else {
      const x1 = from.x + NODE_W / 2;
      const y1 = from.y + NODE_H;
      const x2 = to.x + NODE_W / 2;
      const y2 = to.y;
      const mid = (y1 + y2) / 2;
      d = `M ${x1} ${y1} C ${x1} ${mid}, ${x2} ${mid}, ${x2} ${y2}`;
    }
    path.setAttribute('d', d);
    path.setAttribute('class', 'wf-edge');
    path.setAttribute('marker-end', 'url(#arrow)');
    svg.appendChild(path);
  }

  const review = positions.find((p) => p.key === 'CODE-REVIEW');
  const dev = positions.find((p) => p.key === 'TASK-03');
  if (review && dev) {
    const reworked = (workflow.stages?.['TASK-03']?.rework_round || 0) > 0
      || workflow.stages?.['CODE-REVIEW']?.status === 'failed';
    let d;
    let labelAt;
    if (compact) {
      // 单行时打回连线从下方绕回。
      const sx = review.x + NODE_W / 2;
      const sy = review.y + NODE_H;
      const tx = dev.x + NODE_W / 2;
      const ty = dev.y + NODE_H;
      const midY = sy + 16;
      d = `M ${sx} ${sy} C ${sx} ${midY}, ${tx} ${midY}, ${tx} ${ty}`;
      labelAt = { x: (sx + tx) / 2, y: midY + 13 };
    } else {
      const sx = review.x + NODE_W / 2;
      const sy = review.y;
      const tx = dev.x + NODE_W / 2;
      const ty = dev.y + NODE_H;
      const midY = sy - GAP_Y / 2;
      d = `M ${sx} ${sy} L ${sx} ${midY} L ${tx} ${midY} L ${tx} ${ty}`;
      labelAt = { x: (sx + tx) / 2, y: midY - 5 };
    }
    const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
    path.setAttribute('d', d);
    path.setAttribute('class', `wf-edge reject${reworked ? ' hit' : ' soft'}`);
    if (reworked) path.setAttribute('marker-end', 'url(#arrowRed)');
    svg.appendChild(path);
    if (reworked) {
      const label = document.createElementNS('http://www.w3.org/2000/svg', 'text');
      label.setAttribute('x', labelAt.x);
      label.setAttribute('y', labelAt.y);
      label.setAttribute('text-anchor', 'middle');
      label.setAttribute('class', 'wf-edge-label');
      label.textContent = '打回';
      svg.appendChild(label);
    }
  }

  for (const node of positions) {
    const status = stageStatus(workflow, node.key);
    const group = document.createElementNS('http://www.w3.org/2000/svg', 'g');
    group.setAttribute('class', `wf-node ${status}${state.selectedStage === node.key ? ' selected' : ''}`);
    group.setAttribute('transform', `translate(${node.x}, ${node.y})`);

    const rect = document.createElementNS('http://www.w3.org/2000/svg', 'rect');
    rect.setAttribute('width', NODE_W);
    rect.setAttribute('height', NODE_H);
    rect.setAttribute('rx', 10);
    group.appendChild(rect);

    const agentId = compact ? '' : agentOfStage(node.key);
    const avaState = status === 'completed' ? 'done' : status === 'running' ? 'running' : status === 'failed' ? 'error' : '';
    if (agentId && typeof agentAvatarHTML === 'function') {
      const avaSize = 20;
      const ava = document.createElementNS('http://www.w3.org/2000/svg', 'g');
      ava.setAttribute('class', 'wf-ava');
      ava.setAttribute('transform', `translate(${NODE_W - avaSize - 6}, 5)`);
      ava.innerHTML = agentAvatarHTML(agentId, {
        size: avaSize,
        state: avaState,
        label: agentNameOf(agentId),
      });
      group.appendChild(ava);
    } else {
      const dot = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
      dot.setAttribute('cx', NODE_W - 14);
      dot.setAttribute('cy', 14);
      dot.setAttribute('r', 3);
      dot.setAttribute('class', `wf-node-dot ${status}`);
      group.appendChild(dot);
    }

    const title = document.createElementNS('http://www.w3.org/2000/svg', 'text');
    title.setAttribute('x', 12);
    title.setAttribute('y', 24);
    title.setAttribute('class', 'wf-node-title');
    title.textContent = node.label;
    group.appendChild(title);

    const stage = workflow.stages?.[node.key];
    const sub = document.createElementNS('http://www.w3.org/2000/svg', 'text');
    sub.setAttribute('x', 12);
    sub.setAttribute('y', 42);
    sub.setAttribute('class', 'wf-node-sub');
    sub.textContent = String(stage?.agent_type || node.agent).slice(0, 22);
    group.appendChild(sub);

    const foot = document.createElementNS('http://www.w3.org/2000/svg', 'text');
    foot.setAttribute('x', 12);
    foot.setAttribute('y', 56);
    foot.setAttribute('class', 'wf-node-foot');
    foot.textContent = stage?.completed_at ? relTime(stage.completed_at) : (status === 'running' ? '进行中' : '待开始');
    group.appendChild(foot);

    group.addEventListener('click', () => {
      if (compact) return;
      state.selectedStage = state.selectedStage === node.key ? '' : node.key;
      drawOnly(workflow);
    });
    svg.appendChild(group);
  }

  target.appendChild(svg);
  if (!compact) renderDetail(workflow, state.selectedStage);
}

function drawOnly(workflow) {
  wfGraph.querySelector('svg')?.remove();
  drawGraph(workflow);
}

function renderDetail(workflow, key) {
  const stage = key ? workflow.stages?.[key] : null;
  if (!stage) {
    wfDetail.hidden = true;
    return;
  }
  const metas = [
    ['状态', stage.status || ''],
    ['代理', stage.agent_type || ''],
    ['产出', stage.artifact_path || ''],
    ['自检', stage.self_check || ''],
    ['结论', stage.conclusion || ''],
  ].filter(([, value]) => value);

  wfDetail.innerHTML = '';
  for (const [name, value] of metas) {
    const row = document.createElement('div');
    row.className = 'wf-detail-row';
    const tag = document.createElement('b');
    tag.textContent = name;
    const span = document.createElement('span');
    span.textContent = value;
    row.append(tag, span);
    wfDetail.appendChild(row);
  }
  wfDetail.hidden = false;
}

// —— 数据刷新 ——
async function refresh(force = false) {
  const { projects, active } = await api.projects();
  const enriched = [];
  for (const project of projects) {
    const result = await api.workflows(project.path);
    enriched.push({ ...project, runs: result.workflows || [], installed: result.installed || {} });
  }
  state.projects = enriched;
  state.active = active || projects[0]?.path || '';

  // 所有在跑的终端（包括流水线拉起来的）都要出现在左侧终端目录里。
  const list = await api.sessions().catch(() => []);
  const known = new Set(list.map((s) => s.id));
  for (const item of list) {
    if (!sessions.has(item.id)) attachSession(item, false);
  }
  for (const [id, session] of sessions) {
    if (!known.has(id) && session.state === 'running') {
      session.state = 'exited';
      renderRail();
    }
  }

  const signature = JSON.stringify(enriched.map((p) => [p.path, p.runs]));
  if (!force && signature === state.signature) return;
  state.signature = signature;

  renderRail();
  renderPanel();
  fitActive();
}

// —— 终端 ——
function makeTerminal(session) {
  const host = document.createElement('div');
  host.className = 'term-host';
  host.hidden = true;
  terminalHost.appendChild(host);

  const term = new Terminal({
    theme: termTheme(),
    fontFamily: 'Consolas, "Cascadia Mono", "Microsoft YaHei", monospace',
    fontSize: 13,
    lineHeight: 1.25,
    cursorBlink: true,
    scrollback: 8000,
  });
  const fit = new FitAddon.FitAddon();
  term.loadAddon(fit);
  term.open(host);
  term.onData((data) => api.write(session.id, data));

  session.host = host;
  session.term = term;
  session.fit = fit;
  return session;
}

function fitActive() {
  const session = sessions.get(activeId);
  if (!session) return;
  try {
    session.fit.fit();
    api.resize(session.id, session.term.cols, session.term.rows);
  } catch { /* 容器不可见时忽略 */ }
}

function projectNameOf(path) {
  const project = state.projects.find((p) => isUnder(path, p.path));
  return project ? project.name : '';
}

function renderTermBar() {
  const session = sessions.get(activeId);
  termBar.classList.toggle('running', !!session && session.state === 'running');
  termBar.classList.toggle('error', !!session && session.state === 'error');
  termTitle.textContent = session ? session.title : '没有活动终端';
  termTitle.title = session ? session.label : '';
  const barAvatar = session ? sessionAvatarHTML(session, 22) : '';
  if (termAva) termAva.innerHTML = barAvatar;
  if (termDot) termDot.hidden = Boolean(barAvatar);
  termCwd.textContent = session ? session.cwd : '';
  termRestart.hidden = !session;
  termClose.hidden = !session;
  if (!session) {
    termBadge.hidden = true;
  } else if (session.state === 'running') {
    termBadge.hidden = false;
    termBadge.className = 'pill';
    termBadge.textContent = '运行中';
  } else if (session.state === 'error') {
    termBadge.hidden = false;
    termBadge.className = 'pill error';
    termBadge.textContent = '异常退出';
  } else {
    termBadge.hidden = false;
    termBadge.className = 'pill muted';
    termBadge.textContent = '已退出';
  }

  if (!session) {
    topbarHint.textContent = '未选择会话';
    return;
  }
  const project = projectNameOf(session.cwd);
  topbarHint.textContent = project ? `${project} / ${session.title}` : session.title;
}

function setActive(id) {
  activeId = id;
  for (const [key, session] of sessions) session.host.hidden = key !== id;
  emptyState.hidden = sessions.size > 0;
  renderTermBar();
  renderRail();
  const session = sessions.get(id);
  if (session) requestAnimationFrame(() => { fitActive(); session.term.focus(); });
}

function removeSession(id) {
  const session = sessions.get(id);
  if (!session) return;
  session.source?.close();
  session.term.dispose();
  session.host.remove();
  sessions.delete(id);
  if (activeId === id) {
    const next = sessions.keys().next().value || '';
    activeId = '';
    setActive(next);
  } else {
    renderRail();
  }
  emptyState.hidden = sessions.size > 0;
}

async function createSession({ command, cwd, agent = '' }) {
  const result = await api.spawn({ command, cwd, agent });
  if (!result.ok) {
    alert(`启动失败：${result.error}`);
    return;
  }
  attachSession({ id: result.id, label: command, cwd: result.cwd, state: 'running', agent: result.agent || agent });
  // 新建终端后直接进入它的对话页。
  if (typeof switchTab === 'function') switchTab('terminal');
}

// 用同样的命令和工作目录重新开一个终端。
async function restartActive() {
  const session = sessions.get(activeId);
  if (!session) return;
  const { command, cwd } = session;
  api.kill(session.id);
  removeSession(session.id);
  await createSession({ command, cwd });
}

// attachSession 把 daemon 里已有的会话接到界面上（刷新后也能恢复）。
// focus=false 时只登记到左侧列表，不抢占当前页面。
function attachSession(info, focus = true) {
  if (sessions.has(info.id)) return;
  const session = {
    id: info.id,
    label: info.label || 'terminal',
    title: info.title || shortName(info.label || 'terminal'),
    command: info.label || '',
    cwd: info.cwd,
    state: info.state || 'running',
    agent: info.agent || '',
  };
  sessions.set(session.id, session);
  makeTerminal(session);
  streamSession(session);
  if (focus) setActive(session.id);
  else renderRail();
}

// —— 新建终端 ——
function splitCommand(text) {
  const parts = String(text || '').match(/"[^"]*"|'[^']*'|\S+/g) || [];
  return parts.map((p) => p.replace(/^["']|["']$/g, ''));
}

let modalProject = '';

async function openModal(projectPath) {
  modal.hidden = false;
  modalProject = projectPath || state.active || '';
  // 每次打开都重新读一遍，设置页里的路径 / 参数 / 停用会立即反映出来。
  clis = await api.listClis().catch(() => clis);
  const usable = (cli) => Boolean(cli.path) && cli.enabled !== false;
  cliGrid.innerHTML = '';
  for (const cli of clis) {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'cli-btn';
    btn.disabled = !usable(cli);
    btn.dataset.id = cli.id;
    btn.dataset.search = `${cli.label} ${cli.bin}`.toLowerCase();
    btn.innerHTML = '<span></span><small></small>';
    btn.querySelector('span').textContent = cli.label;
    btn.querySelector('small').textContent = cli.enabled === false ? '已停用' : (cli.path || '未安装');
    btn.addEventListener('click', () => {
      selectedCli = cli.id;
      document.querySelectorAll('.cli-btn').forEach((el) => el.classList.toggle('selected', el === btn));
      customInput.value = '';
    });
    cliGrid.appendChild(btn);
  }
  if (!clis.some((c) => c.id === selectedCli && usable(c))) {
    const preferred = clis.find((c) => c.id === 'claude-code' && usable(c)) || clis.find(usable);
    selectedCli = preferred ? preferred.id : '';
  }
  document.querySelectorAll('.cli-btn').forEach((el) => el.classList.toggle('selected', el.dataset.id === selectedCli));
  $('cliCount').textContent = `${clis.filter(usable).length}/${clis.length} 已安装`;
  const filter = $('cliFilter');
  filter.value = '';
  filter.oninput = () => {
    const q = filter.value.trim().toLowerCase();
    cliGrid.querySelectorAll('.cli-btn').forEach((btn) => {
      btn.hidden = Boolean(q) && !btn.dataset.search.includes(q);
    });
  };
  const config = await api.config();
  cwdInput.value = modalProject || config.lastCwd || config.home;
  customInput.focus();
}

function closeModal() { modal.hidden = true; }

async function createFromModal() {
  const custom = customInput.value.trim();
  let command;
  const cwd = cwdInput.value.trim();
  if (custom) {
    command = splitCommand(custom).join(' ');
  } else {
    const cli = clis.find((c) => c.id === selectedCli);
    if (!cli || !cli.path) return alert('先选一个已安装的 CLI，或填自定义命令');
    command = cliCommand(cli);
  }
  closeModal();
  await createSession({ command, cwd, agent: custom ? '' : selectedCli });
  await api.saveConfig({ lastCwd: cwd });
}

// —— 安装 LoopForge Local ——
async function install(host) {
  if (!state.active) return;
  wfInstalled.textContent = `正在安装到 ${host}…`;
  const result = await api.installLocal({ root: state.active, host });
  const tail = String(result.log || '').trim().split(String.fromCharCode(10)).filter(Boolean).slice(-1)[0] || '';
  wfInstalled.textContent = result.ok ? `${host} 安装完成` : `${host} 安装失败：${tail}`;
  await refresh(true);
}

// —— CLI 设置 ——
let cliSetQuery = '';
let cliVersions = {}; // id -> { installed, latest, outdated, package, probeError, latestError }
let cliVersionsState = 'idle'; // idle | loading | ok | error
let cliVersionsGen = 0;
let bulkUpdate = null; // { total, done, failed[], current }：一键更新进行中

// 流水线 6 个阶段：键与 pipeline.go 的 stageMeta 一致，stages 数据在 agents.json 里。
const CLI_STAGES = [
  { key: 'TASK-01', label: '需求' },
  { key: 'TASK-02', label: '方案' },
  { key: 'TASK-03', label: '开发' },
  { key: 'CODE-REVIEW', label: '审查' },
  { key: 'TASK-04', label: '测试' },
  { key: 'TASK-05', label: '沉淀' },
];
const CLI_FIT_TOP = 4; // 适配度 ≥4 星才算“强项”
let cliFitStage = ''; // 当前按哪个阶段筛选；空 = 全部

function cliStageScore(cli, key) {
  return (cli && cli.stages && cli.stages[key]) || 0;
}

function cliTopStages(cli, limit) {
  return CLI_STAGES
    .map((stage) => ({ ...stage, score: cliStageScore(cli, stage.key) }))
    .filter((stage) => stage.score >= CLI_FIT_TOP)
    .sort((a, b) => b.score - a.score)
    .slice(0, limit || 2);
}

function renderFitFilter() {
  const host = $('cliFitFilter');
  if (!host) return;
  const chips = [`<button class="fit-chip${cliFitStage ? '' : ' active'}" type="button" data-stage="">全部 <small>${clis.length}</small></button>`];
  for (const stage of CLI_STAGES) {
    const n = clis.filter((cli) => cliStageScore(cli, stage.key) >= CLI_FIT_TOP).length;
    chips.push(`<button class="fit-chip${cliFitStage === stage.key ? ' active' : ''}" type="button" data-stage="${stage.key}" title="只看「${stage.label}」阶段适配度 4 星以上的 CLI">${stage.label} <small>${n}</small></button>`);
  }
  host.innerHTML = chips.join('');
  for (const btn of host.querySelectorAll('.fit-chip')) {
    btn.addEventListener('click', () => {
      cliFitStage = btn.dataset.stage || '';
      renderCliSettings();
    });
  }
}

function cliSetUsable(cli) {
  return Boolean(cli.path) && cli.enabled !== false;
}

// 版本行：本机版本 + 是否有更新；未安装但知道包名时显示 npm 最新版。
function cliVersionHTML(cli) {
  const v = cliVersions[cli.id];
  if (!cli.path) {
    if (v && v.latest) return `<span class="ver-badge none">未安装 · npm 最新 v${v.latest}</span>`;
    return '';
  }
  if (!v) {
    if (cliVersionsState === 'loading') return '<span class="ver-checking">检查版本…</span>';
    if (cliVersionsState === 'error') return '<span class="ver-badge none">版本未知</span>';
    return '';
  }
  const bits = [];
  if (v.installed) bits.push(`<span class="ver-cur">v${v.installed}</span>`);
  if (v.latest) {
    bits.push(v.outdated
      ? `<span class="ver-badge out">可升级 v${v.latest}</span>`
      : `<span class="ver-badge ok">最新</span>`);
  } else if (v.installed) {
    bits.push(`<span class="ver-badge none" title="${v.latestError || '没有找到对应的 npm 包'}">最新版未知</span>`);
  } else {
    bits.push('<span class="ver-badge none">版本未知</span>');
  }
  return bits.join('');
}

function paintCliVersions() {
  for (const row of document.querySelectorAll('#cliSettingsList .cli-set-row')) {
    const cli = clis.find((item) => item.id === row.dataset.id);
    const slot = row.querySelector('.cli-ver');
    if (slot && cli) slot.innerHTML = cliVersionHTML(cli);
    if (cli) paintCliAction(row, cli);
  }
  const btn = $('cliSetRefresh');
  if (btn) {
    btn.disabled = cliVersionsState === 'loading' || Boolean(bulkUpdate);
    btn.textContent = cliVersionsState === 'loading' ? '刷新中…' : '刷新版本';
  }
  paintBulkButton();
}

// 可以一键更新到最新版的 CLI：npm 全局安装或自带 update 命令。
function updatableClis() {
  return clis.filter((cli) => {
    const version = cliVersions[cli.id];
    if (!version || !version.outdated) return false;
    return Boolean((cli.npmManaged && cli.packageName) || cli.updateCmd);
  });
}

function paintBulkButton() {
  const btn = $('cliUpdateAll');
  if (!btn) return;
  const list = updatableClis();
  if (bulkUpdate) {
    btn.hidden = false;
    btn.disabled = true;
    btn.textContent = `更新中 ${bulkUpdate.done}/${bulkUpdate.total}`;
    btn.title = bulkUpdate.current ? `正在更新 ${bulkUpdate.current}` : '批量更新进行中';
    return;
  }
  btn.hidden = !list.length;
  btn.disabled = cliVersionsState === 'loading';
  btn.textContent = `一键更新 ${list.length}`;
  const manual = clis.filter((cli) => {
    const version = cliVersions[cli.id];
    return version && version.outdated && !list.includes(cli);
  }).length;
  btn.title = `依次把 ${list.map((cli) => cli.label).join('、')} 更新到最新版`
    + (manual ? `；另有 ${manual} 款需手动更新` : '');
}

function setBulkNote(text, tone) {
  const note = $('cliBulkNote');
  if (!note) return;
  note.hidden = !text;
  note.textContent = text || '';
  note.className = `cli-bulk-note${tone ? ` ${tone}` : ''}`;
  clearTimeout(note._t);
  if (text && tone !== 'busy') {
    note._t = setTimeout(() => { note.hidden = true; }, 15000);
  }
}

// 等当前安装任务结束（批量更新按顺序一条条跑，避免同时改全局 npm）。
// 只看本次目标的任务：daemon 新任务还没登记时读到的可能是上一个任务的快照。
async function waitCliJobEnd(id) {
  const started = Date.now();
  for (;;) {
    await new Promise((resolve) => setTimeout(resolve, 700));
    let job = null;
    try { job = await api.cliJob(); } catch { job = null; }
    cliJobState = job;
    renderCliJob();
    paintCliVersions();
    if (!job) return null;
    if (job.target !== id) {
      if (Date.now() - started > 20000) return job;
      continue;
    }
    if (!job.running) return job;
  }
}

// 启动一款 CLI 的更新并等它结束。daemon 只允许一个任务，遇到“已有任务在跑”
// 时等前一个收尾再重试一次，避免批量更新里漏掉某一款。
async function runOneUpdate(id) {
  try {
    await api.cliInstall(id);
  } catch (error) {
    if (!/已有安装任务|正在运行/.test(String(error.message || ''))) throw error;
    await waitUntilIdle();
    await api.cliInstall(id);
  }
  return waitCliJobEnd(id);
}

async function waitUntilIdle() {
  for (let i = 0; i < 40; i += 1) {
    await new Promise((resolve) => setTimeout(resolve, 500));
    let job = null;
    try { job = await api.cliJob(); } catch { job = null; }
    cliJobState = job;
    renderCliJob();
    paintCliVersions();
    if (!job || !job.running) return;
  }
}

async function startBulkUpdate() {
  if (bulkUpdate) return;
  if (cliVersionsState === 'loading') {
    setBulkNote('版本还在刷新，等刷新完再点', 'warn');
    return;
  }
  const targets = updatableClis();
  if (!targets.length) return;
  // 同一个 daemon 只能跑一个安装任务，已有任务在跑时不要硬插队。
  let running = null;
  try { running = await api.cliJob(); } catch { running = null; }
  if (running && running.running) {
    setBulkNote('已有安装 / 更新任务在运行，等它结束再试', 'warn');
    return;
  }
  clearTimeout(cliJobTimer);
  bulkUpdate = { total: targets.length, done: 0, failed: [], current: '' };
  paintCliVersions();
  let interrupted = '';
  try {
    for (const cli of targets) {
      bulkUpdate.current = cli.label;
      setBulkNote(`正在更新 ${cli.label}（${bulkUpdate.done + 1}/${bulkUpdate.total}）`, 'busy');
      try {
        const job = await runOneUpdate(cli.id);
        if (!job || job.target !== cli.id || job.ok === false) bulkUpdate.failed.push(cli.label);
      } catch (error) {
        bulkUpdate.failed.push(cli.label);
      }
      bulkUpdate.done += 1;
      clis = await api.listClis().catch(() => clis);
      paintCliVersions();
    }
  } catch (error) {
    interrupted = String(error.message || error);
  } finally {
    const failed = bulkUpdate.failed.slice();
    // 先强制刷新版本号，再解锁按钮：否则按钮会拿着旧数据又显示「可升级」，
    // 用户以为没更新成功，只好再点一次。
    clis = await api.listClis().catch(() => clis);
    await loadVersions(true);
    bulkUpdate = null;
    renderCliSettings();
    // 更新命令返回成功、但版本号没变（npm 包和实际在用的安装不是同一份）时要点出来，
    // 否则按钮上还挂着数字，用户会以为更新丢了。
    const still = targets
      .filter((cli) => { const v = cliVersions[cli.id]; return v && v.outdated; })
      .map((cli) => cli.label);
    const parts = [];
    if (!failed.length && !still.length) {
      parts.push(`已全部更新到最新版（${targets.length} 款）`);
    } else {
      parts.push(`已更新 ${targets.length - failed.length} 款`);
      if (failed.length) parts.push(`${failed.length} 款失败：${failed.join('、')}`);
      if (still.length) parts.push(`${still.length} 款仍显示可升级：${still.join('、')}（可能不是 npm 全局安装）`);
    }
    if (interrupted) parts.push(`中断：${interrupted}`);
    setBulkNote(parts.join('；'), failed.length || still.length || interrupted ? 'warn' : 'ok');
  }
}

// 行内「安装 / 更新」按钮：未安装的都能点（没包名时先填包名）；
// 已安装的按 npm 包 / 自带更新命令二选一，任务跑在该 CLI 上时显示进度态。
function paintCliAction(row, cli) {
  const act = row.querySelector('.cli-set-act');
  if (!act) return;
  const job = cliJobState;
  const busy = Boolean(job && job.running && job.target === cli.id);
  const version = cliVersions[cli.id];
  if (busy) {
    act.hidden = false;
    act.disabled = true;
    act.classList.add('busy');
    act.textContent = job.kind === 'install' ? '安装中…' : '更新中…';
    return;
  }
  act.classList.remove('busy');
  act.disabled = false;
  if (!cli.path) {
    act.hidden = false;
    act.textContent = '安装';
    act.title = cli.packageName ? `npm install -g ${cli.packageName}` : '填写 npm 包名后安装';
    return;
  }
  if (version && version.outdated) {
    if (cli.npmManaged && cli.packageName) {
      act.hidden = false;
      act.textContent = '更新';
      act.title = `npm install -g ${cli.packageName}@latest`;
    } else if (cli.updateCmd) {
      act.hidden = false;
      act.textContent = '更新';
      act.title = `${cli.bin} ${cli.updateCmd}`;
    } else {
      // 既不是 npm 全局安装、也没有内置更新命令：只能手动更新。
      act.hidden = false;
      act.disabled = true;
      act.textContent = '更新';
      act.title = `${cli.path} 不是 npm 全局安装，也没有内置更新命令，请手动更新`;
    }
    return;
  }
  act.hidden = true;
  act.title = '';
}

async function loadVersions(force) {
  const gen = ++cliVersionsGen;
  cliVersionsState = 'loading';
  paintCliVersions();
  try {
    const versions = await api.cliVersions(force);
    if (gen !== cliVersionsGen) return;
    cliVersions = versions;
    cliVersionsState = 'ok';
  } catch (error) {
    if (gen !== cliVersionsGen) return;
    cliVersionsState = 'error';
  }
  paintCliVersions();
}

function renderCliSettings() {
  const host = $('cliSettingsList');
  const q = cliSetQuery.trim().toLowerCase();
  // 同样的筛选条件重建列表时保留滚动位置，更新完不会“跳回顶部”。
  const sig = `${q}|${cliFitStage}`;
  const keepScroll = host.dataset.sig === sig ? host.scrollTop : 0;
  host.innerHTML = '';
  let list = clis.filter((cli) => !q || `${cli.label} ${cli.id} ${cli.bin} ${cli.packageName || ''}`.toLowerCase().includes(q));
  const stage = CLI_STAGES.find((item) => item.key === cliFitStage);
  if (stage) {
    list = list
      .filter((cli) => cliStageScore(cli, stage.key) >= CLI_FIT_TOP)
      .sort((a, b) => cliStageScore(b, stage.key) - cliStageScore(a, stage.key) || a.label.localeCompare(b.label, 'zh'));
  }
  if (!list.length) {
    host.innerHTML = `<div class="cli-set-empty">${stage ? `没有在「${stage.label}」阶段适配度 4 星以上的 CLI` : '没有匹配的 CLI'}</div>`;
  } else {
    for (const cli of list) host.appendChild(cliSetRow(cli));
  }
  $('cliSetCount').textContent = stage
    ? `${list.length} 款适合「${stage.label}」`
    : `${clis.filter(cliSetUsable).length}/${clis.length} 可用`;
  renderFitFilter();
  paintCliVersions();
  host.dataset.sig = sig;
  host.scrollTop = keepScroll;
}

function cliSetRow(cli) {
  const row = document.createElement('div');
  row.className = `cli-set-row${cli.enabled === false ? ' disabled' : ''}`;
  row.dataset.id = cli.id;
  row.innerHTML = `
    <div class="cli-set-top">
      <span class="cli-set-name"></span>
      <span class="cli-set-id"></span>
      <span class="cli-set-badge"></span>
      <button class="btn mini cli-set-act" type="button" hidden></button>
      <label class="cli-set-enable"><input type="checkbox"> 启用</label>
      <button class="icon-btn cli-set-reset" type="button" title="恢复默认">↺</button>
    </div>
    <div class="cli-set-fit">
      <span class="cli-fit"></span>
      <span class="cli-fit-note"></span>
    </div>
    <div class="cli-set-grid">
      <label class="wide"><span>路径</span><input class="set-path" spellcheck="false"></label>
      <label><span>参数</span><input class="set-args" spellcheck="false"></label>
      <label><span>npm 包</span><input class="set-pkg" spellcheck="false"></label>
    </div>
    <div class="cli-set-foot"><span class="detected"></span><span class="cli-ver"></span><button class="cli-rel-link" type="button" title="查看最新版本的更新内容（中文翻译）">更新内容</button><span class="saved">已保存</span></div>`;

  row.querySelector('.cli-set-name').textContent = cli.label;
  row.querySelector('.cli-set-id').textContent = `${cli.id} · ${cli.bin}`;
  const badge = row.querySelector('.cli-set-badge');
  const detected = row.querySelector('.detected');

  const apply = (next) => {
    cli = next;
    badge.className = `cli-set-badge${next.path ? ' installed' : ''}`;
    badge.textContent = next.path ? '已安装' : '未安装';
    detected.textContent = next.path
      ? `当前使用：${next.path}`
      : next.overridePath
        ? `路径不可用：${next.overridePath}`
        : `未找到 ${next.bin}`;
    row.classList.toggle('disabled', next.enabled === false);
  };
  apply(cli);

  // 阶段适配：6 格热度条 + 最擅长的阶段 + 一句话说明。
  const fit = row.querySelector('.cli-fit');
  fit.innerHTML = CLI_STAGES.map((stage) => {
    const v = cliStageScore(cli, stage.key);
    return `<i class="fit-cell lv${v}" title="${stage.label}：${v}/5">${stage.label.slice(0, 1)}</i>`;
  }).join('');
  const fitNote = row.querySelector('.cli-fit-note');
  const tops = cliTopStages(cli, 2).map((stage) => stage.label);
  const useless = CLI_STAGES.every((stage) => cliStageScore(cli, stage.key) === 0);
  if (useless) {
    fitNote.textContent = '流水线不适用';
  } else if (tops.length) {
    fitNote.append('最擅长 ');
    const strong = document.createElement('b');
    strong.textContent = tops.join(' · ');
    fitNote.append(strong);
  }
  if (cli.fitNote) {
    const desc = document.createElement('span');
    desc.className = 'cli-fit-desc';
    desc.textContent = `${tops.length || useless ? ' — ' : ''}${cli.fitNote}`;
    desc.title = cli.fitNote;
    fitNote.append(desc);
  }

  const enable = row.querySelector('.cli-set-enable input');
  enable.checked = cli.enabled !== false;

  const pathInput = row.querySelector('.set-path');
  pathInput.value = cli.overridePath || '';
  pathInput.placeholder = cli.path || `自动探测（${cli.bin}）`;

  const argsInput = row.querySelector('.set-args');
  argsInput.value = (cli.overrideArgs || []).join(' ');
  const defaults = (cli.defaultArgs || []).join(' ');
  argsInput.placeholder = defaults ? `内置：${defaults}` : '无内置参数';

  const pkgInput = row.querySelector('.set-pkg');
  pkgInput.value = cli.overridePkg || '';
  pkgInput.placeholder = cli.detectedPkg ? `检测到：${cli.detectedPkg}` : '例如 @scope/name';

  const saved = row.querySelector('.saved');
  const flash = () => {
    saved.classList.add('show');
    clearTimeout(saved._t);
    saved._t = setTimeout(() => saved.classList.remove('show'), 1200);
  };

  const save = async () => {
    try {
      const res = await api.saveCli({
        id: cli.id,
        path: pathInput.value.trim(),
        args: splitCommand(argsInput.value),
        package: pkgInput.value.trim(),
        enabled: enable.checked,
      });
      clis = res.clis || clis;
      const next = clis.find((item) => item.id === cli.id);
      if (next) {
        apply(next);
        pathInput.placeholder = next.path || `自动探测（${next.bin}）`;
        pkgInput.placeholder = next.detectedPkg ? `检测到：${next.detectedPkg}` : '例如 @scope/name';
      }
      // 让看板/流水线的 CLI 下拉下次重新探测。
      if (typeof boardState !== 'undefined') boardState.agents = [];
      flash();
      // 路径变了，版本要重新探测。
      delete cliVersions[cli.id];
      loadVersions(false);
    } catch (error) {
      alert(`保存失败：${error.message || error}`);
    }
  };

  pathInput.addEventListener('change', save);
  argsInput.addEventListener('change', save);
  pkgInput.addEventListener('change', save);
  enable.addEventListener('change', save);
  row.querySelector('.cli-set-act').addEventListener('click', () => startCliJob(cli.id));
  row.querySelector('.cli-rel-link').addEventListener('click', () => openReleaseDrawer(cli));
  row.addEventListener('click', (event) => {
    if (event.target.closest('.ver-badge')) openReleaseDrawer(cli);
  });
  row.querySelector('.cli-set-reset').addEventListener('click', async () => {
    try {
      const res = await api.resetCli(cli.id);
      clis = res.clis || clis;
      if (typeof boardState !== 'undefined') boardState.agents = [];
      delete cliVersions[cli.id];
      renderCliSettings();
      loadVersions(false);
    } catch (error) {
      alert(`恢复失败：${error.message || error}`);
    }
  });
  return row;
}

// —— CLI 更新内容抽屉 ——
let releaseInfo = null;
let releaseMode = 'zh';
let releaseCli = null;
let releaseLoading = false;

function escapeHTML(text) {
  return String(text).replace(/[&<>"']/g, (ch) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[ch]));
}

// 轻量 Markdown：标题 / 列表 / 行内代码 / 粗体 / 链接，其余按段落。先转义再上标签，避免注入。
function renderReleaseMarkdown(text) {
  const inline = (line) => escapeHTML(line)
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<b>$1</b>')
    .replace(/\[([^\]]+)\]\((https?:[^)\s]+)\)/g, '<a href="$2" target="_blank" rel="noopener">$1</a>')
    .replace(/(^|[\s(])(https?:\/\/[^\s<)]+)/g, '$1<a href="$2" target="_blank" rel="noopener">$2</a>');
  const lines = String(text || '').replace(/\r\n/g, '\n').replace(/<!--[\s\S]*?-->/g, '').split('\n');
  const out = [];
  let inList = false;
  const closeList = () => {
    if (inList) {
      out.push('</ul>');
      inList = false;
    }
  };
  for (const raw of lines) {
    const line = raw.replace(/\s+$/, '');
    if (!line.trim()) {
      closeList();
      continue;
    }
    const heading = line.match(/^#{1,6}\s+(.*)$/);
    if (heading) {
      closeList();
      out.push(`<h4>${inline(heading[1])}</h4>`);
      continue;
    }
    const bullet = line.match(/^\s*[-*+]\s+(.*)$/);
    if (bullet) {
      if (!inList) {
        out.push('<ul>');
        inList = true;
      }
      out.push(`<li>${inline(bullet[1])}</li>`);
      continue;
    }
    closeList();
    out.push(`<p>${inline(line)}</p>`);
  }
  closeList();
  return out.join('');
}

function openReleaseDrawer(cli) {
  if (!cli) return;
  if (releaseCli === cli && releaseLoading) return;
  releaseCli = cli;
  releaseInfo = null;
  releaseMode = 'zh';
  $('relScrim').hidden = false;
  $('relDrawer').hidden = false;
  $('relTitle').textContent = `${cli.label} · 最新版本更新内容`;
  $('relMeta').textContent = '正在读取 GitHub 发布记录…';
  $('relTools').hidden = true;
  $('relLink').hidden = true;
  $('relNote').textContent = '';
  $('relBody').innerHTML = '<div class="rel-state"><span class="rel-spinner"></span><span>正在拉取更新说明并翻译成简体中文…</span></div>';
  loadRelease(cli, false);
}

async function loadRelease(cli, refresh) {
  releaseLoading = true;
  try {
    const info = await api.cliRelease(cli.id, refresh);
    if (releaseCli !== cli) return;
    releaseInfo = info;
    paintRelease();
  } catch (error) {
    if (releaseCli !== cli) return;
    $('relMeta').textContent = '';
    $('relBody').innerHTML = `<div class="rel-state"><span>读取失败：${escapeHTML(error.message || error)}</span></div>`;
  } finally {
    releaseLoading = false;
  }
}

function releaseBody() {
  if (!releaseInfo) return '';
  if (releaseMode === 'en' || !releaseInfo.zh) return releaseInfo.body || '';
  return releaseInfo.zh;
}

function paintRelease() {
  const info = releaseInfo;
  if (!info) return;
  const metaBits = [];
  if (info.version) metaBits.push(`v${info.version}`);
  if (info.publishedAt) metaBits.push(new Date(info.publishedAt).toLocaleDateString('zh-CN'));
  metaBits.push(info.repo ? `GitHub · ${info.repo}` : '');
  $('relMeta').textContent = metaBits.filter(Boolean).join(' · ');

  const link = $('relLink');
  link.hidden = !info.url;
  if (info.url) link.href = info.url;

  const tools = $('relTools');
  const hasZh = Boolean(info.zh);
  tools.hidden = !(hasZh && info.body && info.zh !== info.body);
  $('relZhTab').classList.toggle('active', releaseMode === 'zh');
  $('relEnTab').classList.toggle('active', releaseMode === 'en');

  if (info.error) {
    $('relBody').innerHTML = `<div class="rel-state"><span>${escapeHTML(info.error)}</span></div>`;
    $('relNote').textContent = info.repo ? '' : '可以在 npm / 官方文档里查看该 CLI 的更新说明。';
    return;
  }
  if (!info.body) {
    $('relBody').innerHTML = `<div class="rel-state"><span>${escapeHTML(info.note || '这个版本没有更新说明')}</span></div>`;
    $('relNote').textContent = '';
    return;
  }
  const text = releaseBody();
  $('relBody').innerHTML = text.trim()
    ? renderReleaseMarkdown(text)
    : '<div class="rel-state"><span>这条发布没有正文</span></div>';
  $('relNote').textContent = info.note || (info.translated && releaseMode === 'zh' ? '译文由公开翻译服务生成，专业术语可能有偏差。' : '');
}

function setReleaseMode(mode) {
  releaseMode = mode === 'en' ? 'en' : 'zh';
  paintRelease();
}

function closeReleaseDrawer() {
  $('relDrawer').hidden = true;
  $('relScrim').hidden = true;
  releaseCli = null;
  releaseInfo = null;
}

(function setupReleaseDrawer() {
  $('relClose').addEventListener('click', closeReleaseDrawer);
  $('relScrim').addEventListener('click', closeReleaseDrawer);
  $('relZhTab').addEventListener('click', () => setReleaseMode('zh'));
  $('relEnTab').addEventListener('click', () => setReleaseMode('en'));
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && !$('relDrawer').hidden) {
      event.stopImmediatePropagation();
      closeReleaseDrawer();
    }
  }, true);
})();

// —— 版本与更新 ——
let appHealth = null;
let updateInfo = null;

// 按当前运行平台挑发布包：Windows 取 portable exe，Linux 取对应架构的压缩包。
function pickUpdateAsset(info) {
  const assets = info?.assets || [];
  const platform = (appHealth?.platform || navigator.userAgent || '').toLowerCase();
  const isWindows = platform.includes('windows') || platform.includes('win32');
  const isLinux = platform.includes('linux');
  const isArm = platform.includes('arm64') || platform.includes('aarch64');
  const match = assets.find((asset) => {
    const name = (asset.name || '').toLowerCase();
    if (isWindows) return name.endsWith('.exe');
    if (isLinux) {
      if (!name.includes('linux')) return false;
      return isArm ? name.includes('arm64') : (name.includes('amd64') || name.includes('x86_64'));
    }
    return false;
  });
  return match?.url || info?.url || '';
}

// 发布说明是 Markdown，设置页用纯文本显示：去掉标题号、加粗和图片标记，保留可读内容。
function plainNotes(text) {
  return String(text || '')
    .replace(/^#{1,6}\s*/gm, '')
    .replace(/\*\*(.+?)\*\*/g, '$1')
    .replace(/!\[[^\]]*\]\([^)]*\)/g, '')
    .replace(/\[(.+?)\]\((https?:\/\/[^\s)]+)\)/g, '$1（$2）')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
}

// 把检查结果画到设置 → 关于，以及标题栏的新版徽标。
function renderUpdate(info) {
  updateInfo = info;
  const title = $('updateTitle');
  const meta = $('updateMeta');
  const notes = $('updateNotes');
  const download = $('updateDownload');
  const badge = $('updateBadge');
  if (!title) return;

  if (!info) {
    title.textContent = '检查更新';
    meta.textContent = '暂时无法连接更新服务';
    notes.hidden = true;
    download.hidden = true;
    badge.hidden = true;
    return;
  }
  if (info.error) {
    title.textContent = '检查失败';
    meta.textContent = info.error;
    notes.hidden = true;
    download.hidden = true;
    badge.hidden = true;
    return;
  }
  if (info.available) {
    title.textContent = `发现新版本 v${info.latest}`;
    const date = info.publishedAt ? new Date(info.publishedAt).toLocaleDateString('zh-CN') : '';
    meta.textContent = `当前 v${info.current}${date ? ` · 发布于 ${date}` : ''}`;
    notes.textContent = plainNotes(info.notes).slice(0, 1600);
    notes.hidden = !notes.textContent;
    const url = pickUpdateAsset(info);
    download.href = url;
    download.hidden = !url;
    badge.hidden = false;
    badge.textContent = `v${info.latest} 可用`;
    return;
  }
  title.textContent = '已是最新版本';
  meta.textContent = `当前 v${info.current}`;
  notes.hidden = true;
  download.hidden = true;
  badge.hidden = true;
}

// 私有仓库令牌状态：只显示「已配置/未配置」，不回显令牌本身。
function renderGithubToken(configured, fromEnv) {
  const state = $('ghTokenState');
  const clear = $('ghTokenClear');
  if (!state) return;
  if (fromEnv) {
    state.textContent = '由环境变量 GITHUB_TOKEN 提供';
  } else {
    state.textContent = configured ? '已配置（不显示令牌）' : '未配置（私有仓库必需）';
  }
  if (clear) clear.hidden = !configured || fromEnv;
}

async function refreshAbout(force = false) {
  const [health, shell, update, cfg] = await Promise.all([
    req('/api/v1/health').catch(() => null),
    shellApi?.info ? shellApi.info().catch(() => null) : Promise.resolve(null),
    req(`/api/v1/update/check${force ? '?force=1' : ''}`).catch(() => null),
    req('/api/v1/config').catch(() => null),
  ]);
  if (health) {
    appHealth = health;
    // 桌面版以外壳版本为准（应用版本），浏览器访问时用 daemon 版本。
    const appVersion = shell?.version || health.version;
    $('aboutDaemon').textContent = `v${health.version}`;
    $('aboutPlatform').textContent = health.platform || '—';
    $('aboutRepo').textContent = health.repo || '—';
    $('aboutVer').textContent = `版本 v${appVersion}`;
    $('aboutShell').textContent = shell ? `v${shell.version}` : '浏览器访问';
    const sideVer = $('sideVer');
    if (sideVer) sideVer.textContent = `v${appVersion}`;
    document.title = `AgentDeck v${appVersion}`;
  }
  if (cfg) renderGithubToken(Boolean(cfg.githubTokenSet), Boolean(cfg.githubTokenEnv));
  renderUpdate(update);
}

// 统一设置入口：左下角「设置」打开，左侧竖向页签分「终端 CLI」「工作流」「外观」「关于」。
async function openSettings(tab) {
  closeReleaseDrawer();
  $('settingsModal').hidden = false;
  setSettingsTab(tab || 'cli');
  $('cliSetFilter').value = '';
  cliSetQuery = '';
  cliFitStage = '';
  syncWfToggle();
  clis = await api.listClis().catch(() => clis);
  renderCliSettings();
  loadVersions(false);
  loadCliJob();
}

function setSettingsTab(tab) {
  for (const btn of document.querySelectorAll('.settings-tab')) {
    btn.classList.toggle('active', btn.dataset.tab === tab);
  }
  $('paneCli').hidden = tab !== 'cli';
  $('paneWf').hidden = tab !== 'wf';
  const look = $('paneLook');
  if (look) look.hidden = tab !== 'look';
  const about = $('paneAbout');
  if (about) about.hidden = tab !== 'about';
  if (tab === 'look') { renderAppThemes(); renderTermThemes(); }
  if (tab === 'about') refreshAbout(false).catch(() => {});
}

function setPanelVisible(visible) {
  panel.hidden = !visible;
  const box = $('wfPanelToggle');
  if (box) box.checked = visible;
  requestAnimationFrame(fitActive);
}

function syncWfToggle() {
  const box = $('wfPanelToggle');
  if (box) box.checked = !panel.hidden;
}

// —— 安装前补 npm 包名 ——
let pkgPromptCli = null;

function openPkgPrompt(cli) {
  pkgPromptCli = cli;
  $('pkgTitle').textContent = cli.label;
  const input = $('pkgInput');
  input.value = cli.overridePkg || '';
  input.placeholder = cli.detectedPkg || '例如 @scope/name';
  $('pkgModal').hidden = false;
  setTimeout(() => input.focus(), 30);
}

function closePkgPrompt() {
  $('pkgModal').hidden = true;
  pkgPromptCli = null;
}

async function confirmPkgPrompt() {
  const cli = pkgPromptCli;
  if (!cli) return;
  const pkg = $('pkgInput').value.trim();
  if (!pkg) {
    $('pkgInput').focus();
    return;
  }
  try {
    const res = await api.saveCli({
      id: cli.id,
      path: cli.overridePath || '',
      args: cli.overrideArgs || [],
      package: pkg,
      enabled: cli.enabled !== false,
    });
    clis = res.clis || clis;
  } catch (error) {
    alert(`保存包名失败：${error.message || error}`);
    return;
  }
  closePkgPrompt();
  renderCliSettings();
  await startCliJob(cli.id);
}

// —— CLI 安装 / 更新（走 npm 全局包，和 magpie 更新自己一样：下载新版、替换、重新探测） ——
let cliJobState = null;
let cliJobTimer = 0;

async function loadCliJob() {
  try {
    cliJobState = await api.cliJob();
  } catch {
    cliJobState = null;
  }
  renderCliJob();
  paintCliVersions();
}

function renderCliJob() {
  const panel = $('cliJob');
  const job = cliJobState;
  if (!job || !job.startedAt) {
    panel.hidden = true;
    return;
  }
  panel.hidden = false;
  const verb = job.kind === 'install' ? '安装' : '更新';
  $('cliJobTitle').textContent = job.running ? `${job.label} ${verb}中…` : `${job.label} ${verb}${job.ok ? '完成' : '失败'}`;
  $('cliJobCancel').hidden = !job.running;
  const log = $('cliJobLog');
  log.innerHTML = (job.lines || []).map((line) => {
    const cls = /失败|ERR!|error/i.test(line) ? 'err' : /完成/.test(line) ? 'ok' : '';
    return cls ? `<span class="${cls}">${escapeHtml(line)}</span>` : escapeHtml(line);
  }).join('\n');
  log.scrollTop = log.scrollHeight;
}

async function pollCliJob() {
  const wasRunning = Boolean(cliJobState && cliJobState.running);
  await loadCliJob();
  const job = cliJobState;
  if (job && job.running && !$('settingsModal').hidden) {
    clearTimeout(cliJobTimer);
    cliJobTimer = setTimeout(() => { pollCliJob(); }, 1000);
  } else if (wasRunning && job && !job.running) {
    // 安装 / 更新结束：重新拉列表和版本，新装的 CLI 会直接出现在设置页里。
    clis = await api.listClis().catch(() => clis);
    renderCliSettings();
    loadVersions(true);
  }
}

async function startCliJob(id) {
  const cli = clis.find((item) => item.id === id);
  // 未安装、也没有已知包名：先让用户填一个再装。
  if (cli && !cli.path && !cli.packageName) {
    openPkgPrompt(cli);
    return;
  }
  try {
    await api.cliInstall(id);
  } catch (error) {
    alert(`启动失败：${error.message || error}`);
    return;
  }
  clearTimeout(cliJobTimer);
  await pollCliJob();
}

function escapeHtml(text) {
  return String(text).replace(/[&<>"]/g, (ch) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[ch]));
}

// —— 事件 ——
$('addProject').addEventListener('click', async () => {
  const result = await api.addProject();
  if (result.canceled) return;
  await refresh(true);
});
$('emptyNew').addEventListener('click', () => openModal());
$('termNew').addEventListener('click', () => openModal(state.active));
$('cancel').addEventListener('click', closeModal);
$('create').addEventListener('click', createFromModal);
$('pickDir').addEventListener('click', async () => {
  const dir = await api.pickDir();
  if (dir) cwdInput.value = dir;
});
modal.addEventListener('click', (event) => { if (event.target === modal) closeModal(); });
document.addEventListener('keydown', (event) => {
  if (modal.hidden) return;
  if (event.key === 'Escape') {
    event.preventDefault();
    closeModal();
    return;
  }
  if (event.key === 'Enter' && event.target.tagName !== 'BUTTON') {
    event.preventDefault();
    createFromModal();
  }
});

termClose.addEventListener('click', () => {
  if (!activeId) return;
  api.kill(activeId);
  removeSession(activeId);
});
termRestart.addEventListener('click', restartActive);
$('installClaude').addEventListener('click', () => install('claude'));
$('installCodex').addEventListener('click', () => install('codex'));

$('openSettings').addEventListener('click', () => {
  openSettings('cli').catch((error) => alert(`加载设置失败：${error.message || error}`));
});
for (const tab of document.querySelectorAll('.settings-tab')) {
  tab.addEventListener('click', () => setSettingsTab(tab.dataset.tab));
}
$('updateCheck').addEventListener('click', async () => {
  const btn = $('updateCheck');
  btn.disabled = true;
  btn.textContent = '检查中…';
  try {
    await refreshAbout(true);
  } finally {
    btn.disabled = false;
    btn.textContent = '检查更新';
  }
});
$('updateBadge').addEventListener('click', () => {
  openSettings('about').catch((error) => alert(`打开设置失败：${error.message || error}`));
});
// 私有仓库只读 Token：保存后立即清缓存重查一次，状态只显示是否配置。
async function saveGithubToken(value) {
  const button = value ? $('ghTokenSave') : $('ghTokenClear');
  const label = button.textContent;
  button.disabled = true;
  button.textContent = '保存中…';
  try {
    const res = await req('/api/v1/config', { method: 'POST', body: JSON.stringify({ githubToken: value }) });
    $('ghTokenInput').value = '';
    renderGithubToken(Boolean(res?.githubTokenSet), false);
    await refreshAbout(true);
  } catch (error) {
    alert(`保存失败：${error.message || error}`);
  } finally {
    button.disabled = false;
    button.textContent = label;
  }
}
$('ghTokenSave').addEventListener('click', () => {
  const value = $('ghTokenInput').value.trim();
  if (!value) {
    $('ghTokenInput').focus();
    return;
  }
  saveGithubToken(value);
});
$('ghTokenClear').addEventListener('click', () => saveGithubToken(''));
$('cliSetFilter').addEventListener('input', (event) => {
  cliSetQuery = event.target.value;
  renderCliSettings();
});
$('cliSetRefresh').addEventListener('click', () => { loadVersions(true); });
$('cliUpdateAll').addEventListener('click', () => { startBulkUpdate(); });
$('cliJobCancel').addEventListener('click', async () => {
  try { await api.cliJobCancel(); } catch (error) { alert(`取消失败：${error.message || error}`); }
  loadCliJob();
});
$('settingsClose').addEventListener('click', () => { $('settingsModal').hidden = true; });
$('settingsModal').addEventListener('click', (event) => {
  if (event.target === $('settingsModal')) $('settingsModal').hidden = true;
});
$('wfPanelToggle').addEventListener('change', (event) => setPanelVisible(event.target.checked));
$('wfPanelOpen').addEventListener('click', () => {
  setPanelVisible(true);
  $('settingsModal').hidden = true;
});
$('pkgCancel').addEventListener('click', closePkgPrompt);
$('pkgOk').addEventListener('click', () => { confirmPkgPrompt(); });
$('pkgInput').addEventListener('keydown', (event) => {
  if (event.key === 'Enter') {
    event.preventDefault();
    confirmPkgPrompt();
  }
});
$('pkgModal').addEventListener('click', (event) => {
  if (event.target === $('pkgModal')) closePkgPrompt();
});
$('confirmYes').addEventListener('click', () => settleConfirm(true));
$('confirmNo').addEventListener('click', () => settleConfirm(false));
$('confirmModal').addEventListener('click', (event) => {
  if (event.target === $('confirmModal')) settleConfirm(false);
});
// —— 目录选择弹窗 ——
$('dirCancel').addEventListener('click', () => closeDirPicker(''));
$('dirChoose').addEventListener('click', () => closeDirPicker(dirCurrent));
$('dirUp').addEventListener('click', () => {
  const parent = $('dirUp').dataset.parent;
  if (parent) loadDir(parent);
});
$('dirGo').addEventListener('click', () => loadDir($('dirPath').value.trim()));
$('dirPath').addEventListener('keydown', (event) => {
  if (event.key !== 'Enter') return;
  event.preventDefault();
  loadDir($('dirPath').value.trim());
});
$('dirMkdir').addEventListener('click', () => {
  $('dirNewRow').hidden = false;
  $('dirNewName').value = '';
  $('dirNewName').focus();
});
$('dirNewCancel').addEventListener('click', () => { $('dirNewRow').hidden = true; });
$('dirNewCreate').addEventListener('click', createDir);
$('dirNewName').addEventListener('keydown', (event) => {
  if (event.key === 'Enter') {
    event.preventDefault();
    createDir();
  }
  if (event.key === 'Escape') {
    event.stopPropagation();
    $('dirNewRow').hidden = true;
  }
});
$('dirModal').addEventListener('click', (event) => {
  if (event.target === $('dirModal')) closeDirPicker('');
});
document.addEventListener('keydown', (event) => {
  if (event.key !== 'Escape') return;
  closeProjectMenu();
  if (!$('dirModal').hidden) {
    closeDirPicker('');
    return;
  }
  if (!$('confirmModal').hidden) {
    settleConfirm(false);
    return;
  }
  if (!$('pkgModal').hidden) {
    closePkgPrompt();
    return;
  }
  if (!$('settingsModal').hidden) $('settingsModal').hidden = true;
});
$('refreshWf').addEventListener('click', () => refresh(true));
wfSelect.addEventListener('change', () => {
  state.selectedRun = wfSelect.value;
  state.selectedStage = '';
  renderRail();
  renderPanel();
});
$('panelClose').addEventListener('click', () => setPanelVisible(false));

// —— 窗口外壳：无边框窗口的按钮与自定义菜单（浏览器里自动隐藏） ——
// Ctrl+T / Ctrl+W 在页面里处理；原生菜单栏隐藏后不再依赖菜单快捷键。
document.addEventListener('keydown', (event) => {
  if (!(event.ctrlKey || event.metaKey) || event.altKey || event.shiftKey) return;
  const key = event.key.toLowerCase();
  if (key === 't') {
    event.preventDefault();
    if (modal.hidden) openModal(state.active);
  } else if (key === 'w') {
    event.preventDefault();
    if (activeId) {
      api.kill(activeId);
      removeSession(activeId);
    }
  }
});

const hasShell = Boolean(shellApi && shellApi.win);

if (hasShell) {
  document.body.classList.add('with-shell');
  $('winControls').hidden = false;

  const maxSvg = '<svg viewBox="0 0 12 12" aria-hidden="true"><rect x="2.4" y="2.4" width="7.2" height="7.2" rx="1.2" fill="none" stroke="currentColor" stroke-width="1.1"/></svg>';
  const restoreSvg = '<svg viewBox="0 0 12 12" aria-hidden="true"><rect x="4" y="2.2" width="5.8" height="5.8" rx="1" fill="none" stroke="currentColor" stroke-width="1.1"/><path d="M2.4 4.4v4.2c0 .7.5 1.2 1.2 1.2h4.2" fill="none" stroke="currentColor" stroke-width="1.1" stroke-linecap="round"/></svg>';
  const setMaxIcon = (maximized) => {
    const btn = $('winMax');
    btn.innerHTML = maximized ? restoreSvg : maxSvg;
    btn.title = maximized ? '还原' : '最大化';
    btn.classList.toggle('restored', maximized);
  };

  $('winMin').addEventListener('click', () => shellApi.win.minimize());
  $('winMax').addEventListener('click', () => shellApi.win.toggleMax());
  $('winClose').addEventListener('click', () => shellApi.win.close());
  shellApi.win.onMaxChange(setMaxIcon);
  shellApi.win.isMax().then(setMaxIcon).catch(() => setMaxIcon(false));

  const menuPops = { file: $('menuFile'), view: $('menuView') };
  const closeMenus = () => { for (const pop of Object.values(menuPops)) pop.hidden = true; };
  for (const btn of document.querySelectorAll('.menu-btn')) {
    btn.addEventListener('click', (event) => {
      event.stopPropagation();
      const pop = menuPops[btn.dataset.menu];
      const willOpen = pop.hidden;
      closeMenus();
      pop.hidden = !willOpen;
    });
  }
  document.addEventListener('click', closeMenus);
  for (const item of document.querySelectorAll('.menu-pop button[data-action]')) {
    item.addEventListener('click', () => {
      const action = item.dataset.action;
      closeMenus();
      closeModal();
      if (action === 'new-session') openModal(state.active);
      else shellApi.menu(action);
    });
  }
}

function streamSession(session) {
  const source = new EventSource(`/api/v1/sessions/${encodeURIComponent(session.id)}/stream`);
  source.onmessage = (event) => {
    let payload;
    try { payload = JSON.parse(event.data); } catch { return; }
    if (payload.type === 'data') {
      const binary = atob(payload.data || '');
      const bytes = new Uint8Array(binary.length);
      for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i);
      session.term.write(bytes);
      return;
    }
    if (payload.type === 'exit') {
      session.state = payload.exitCode === 0 ? 'exited' : 'error';
      session.term.write(`\r\n\x1b[2m[进程已退出，code=${payload.exitCode}]\x1b[0m\r\n`);
      source.close();
      renderRail();
    }
  };
  source.onerror = () => source.close();
  session.source = source;
}

shellApi?.onNewSession?.(() => openModal());
shellApi?.onCloseSession?.(() => { if (activeId) { api.kill(activeId); removeSession(activeId); } });

window.addEventListener('resize', () => requestAnimationFrame(fitActive));

// —— 启动 ——
(async function init() {
  const params = new URLSearchParams(location.search);
  const config = await api.config();
  cwdInput.value = params.get('cwd') || state.active || config.lastCwd || config.home;

  if (params.get('root')) {
    await api.addProjectPath(params.get('root')).catch(() => {});
  }

  await refresh(true);
  refreshAbout(false).catch(() => {});
  cwdInput.value = params.get('cwd') || state.active || config.lastCwd || config.home;
  // 启动后停在项目首页（看板），终端只在点左侧终端列表时进入。
  if (typeof switchTab === 'function') switchTab('board');

  const existing = await api.sessions();
  for (const item of existing) attachSession(item);

  state.timer = setInterval(() => {
    refresh().catch(() => {});
  }, 3000);

  const demo = params.get('demo');
  if (demo) createSession({ command: demo, cwd: cwdInput.value });
})();