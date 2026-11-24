// 项目看板：把当前项目 artifacts/*/workflow-state.json 和流水线调度状态画成动态泳道。
// 看板嵌在项目里（终端 / 看板 两个页签），只展示当前项目自己的任务，与其他项目隔离。

const boardState = {
  tab: 'board',
  panelWasOpen: false,
  agents: [],
  pipelines: new Map(), // root -> {config, runs}
  tails: new Map(), // root|slug -> 最新终端输出
  flowSig: '',
  runnerStage: new Map(), // slug -> 上一次停在的阶段，用来做「跳到下一步」的移动动画
  timer: null,
};

const boardApi = {
  agents: () => req('/api/v1/agents').then((r) => r.agents || []),
  pipeline: (root) => req(`/api/v1/pipeline?root=${encodeURIComponent(root)}`),
  savePipeline: (root, patch) => req('/api/v1/pipeline/config', {
    method: 'POST',
    body: JSON.stringify({ root, ...patch }),
  }),
  start: (payload) => req('/api/v1/pipeline/start', { method: 'POST', body: JSON.stringify(payload) }),
  stop: (root, slug) => req('/api/v1/pipeline/stop', { method: 'POST', body: JSON.stringify({ root, slug }) }),
  resume: (root, slug) => req('/api/v1/pipeline/resume', { method: 'POST', body: JSON.stringify({ root, slug }) }),
  reject: (payload) => req('/api/v1/pipeline/reject', { method: 'POST', body: JSON.stringify(payload) }),
};

function agentLabel(id) {
  const agent = boardState.agents.find((a) => a.id === id);
  if (agent) return agent.name;
  return id || 'Claude Code';
}

function installedAgents() {
  const enabled = boardState.agents.filter((a) => a.enabled !== false);
  const list = enabled.filter((a) => a.installed);
  return list.length ? list : (enabled.length ? enabled : boardState.agents);
}

function activeProject() {
  return state.projects.find((p) => p.path === state.active) || null;
}

function pipelineOf(root) {
  return boardState.pipelines.get(root) || { config: { stageAgents: {}, auto: true }, runs: [], editions: {} };
}

// 阶段配置兼容旧格式（字符串）和新格式（{agent,args}）。
function stageAgentId(config, key) {
  const value = config?.stageAgents?.[key];
  if (!value) return 'claude-code';
  return typeof value === 'string' ? value : (value.agent || 'claude-code');
}

// 路线图卡片上的短编号，保持稳定，不暴露完整 slug。
function shortId(value) {
  let hash = 0;
  for (const ch of String(value || '')) hash = (hash * 31 + ch.charCodeAt(0)) >>> 0;
  return String((hash % 9000) + 1000);
}

function boardToast(text) {
  let node = document.getElementById('boardToast');
  if (!node) {
    node = document.createElement('div');
    node.id = 'boardToast';
    node.className = 'board-toast';
    document.body.appendChild(node);
  }
  node.textContent = text;
  node.classList.add('show');
  clearTimeout(node._timer);
  node._timer = setTimeout(() => node.classList.remove('show'), 3600);
}

function pipeRunFor(root, slug) {
  return pipelineOf(root).runs.find((r) => r.slug === slug) || null;
}

function laneOf(run, pipe) {
  if (pipe && ['paused', 'error', 'waiting'].includes(pipe.status)) return 'blocked';
  if (run.status === 'failed') return 'blocked';
  if ((pipe && pipe.status === 'done') || run.status === 'completed') return 'done';
  if (pipe && pipe.status === 'stopped') return 'queued';
  if (run.status === 'running' || (pipe && pipe.status === 'running')) return 'working';
  return 'queued';
}

async function ensureBoardAgents() {
  if (boardState.agents.length) return;
  boardState.agents = await boardApi.agents().catch(() => []);
}

// loadBoardData 只拉当前项目的流水线状态，项目之间互不影响。
async function loadBoardData() {
  boardState.pipelines.clear();
  const project = activeProject();
  if (!project) return;
  const data = await boardApi.pipeline(project.path).catch(() => null);
  if (data) boardState.pipelines.set(project.path, { config: data.config, runs: data.runs || [], editions: data.editions || {} });
}

const laneBodies = () => {
  const map = {};
  document.querySelectorAll('#lanes .lane').forEach((lane) => {
    map[lane.dataset.lane] = {
      body: lane.querySelector('.lane-body'),
      count: lane.querySelector('.lane-count'),
    };
  });
  return map;
};

const cardEls = new Map(); // key -> <article>

function cardKey(root, slug) {
  return `${root}|${slug}`;
}

function stageProgress(run) {
  return STAGES.filter((s) => ['completed', 'passed'].includes(stageStatus(run, s.key))).length;
}

function elapsedOf(pipe) {
  if (!pipe || !pipe.startedAt) return '';
  const ms = Date.now() - new Date(pipe.startedAt).getTime();
  if (!Number.isFinite(ms) || ms < 0) return '';
  const total = Math.floor(ms / 1000);
  const m = Math.floor(total / 60);
  const h = Math.floor(m / 60);
  if (h) return `${h}h ${m % 60}m`;
  if (m) return `${m}m ${String(total % 60).padStart(2, '0')}s`;
  return `${total}s`;
}

// collectTails 取运行中会话的最新输出，让卡片有“在干活”的实感。
async function collectTails() {
  const tails = new Map();
  const project = activeProject();
  if (!project) {
    boardState.tails = tails;
    return;
  }
  const jobs = [];
  for (const run of project.runs || []) {
    const pipe = pipeRunFor(project.path, run.slug);
    if (!pipe || !['running', 'paused', 'error', 'waiting'].includes(pipe.status)) continue;
    const map = pipe.sessions || {};
    const sid = map[pipe.stage] || Object.values(map).pop() || '';
    if (!sid) continue;
    jobs.push(
      req(`/api/v1/sessions/${encodeURIComponent(sid)}/tail?max=180`)
        .then((res) => {
          if (res && res.tail) tails.set(cardKey(project.path, run.slug), res.tail);
        })
        .catch(() => {}),
    );
  }
  await Promise.all(jobs);
  boardState.tails = tails;
}

async function renderBoard(force) {
  await ensureBoardAgents();
  await loadBoardData();
  await collectTails();

  const bodies = laneBodies();
  const counts = { queued: 0, working: 0, blocked: 0, done: 0 };
  const seen = new Set();
  const before = new Map();
  for (const [key, el] of cardEls) {
    if (el.isConnected) before.set(key, el.getBoundingClientRect());
  }

  const project = activeProject();
  const laneOrder = new Map(); // 每个泳道里卡片应有的顺序
  for (const run of project?.runs || []) {
    const pipe = pipeRunFor(project.path, run.slug);
    const lane = laneOf(run, pipe);
    counts[lane] += 1;
    const key = cardKey(project.path, run.slug);
    seen.add(key);
    let card = cardEls.get(key);
    if (!card) {
      card = makeCard();
      cardEls.set(key, card);
    }
    updateCard(card, project, run, pipe, lane);
    if (!laneOrder.has(lane)) laneOrder.set(lane, []);
    laneOrder.get(lane).push(card);
  }

  // 只在卡片真的换泳道或换顺序时移动节点。对已经在位的卡片重复 appendChild，
  // 浏览器会重放入场动画，看板每 2 秒轮询一次时「已完成」的卡片就会一直跳。
  for (const [lane, cards] of laneOrder) {
    const body = bodies[lane].body;
    cards.forEach((card, index) => {
      const current = [...body.children].filter((el) => el.classList.contains('card'))[index];
      if (current !== card) body.insertBefore(card, current || null);
    });
  }

  for (const [key, el] of [...cardEls]) {
    if (!seen.has(key)) {
      el.remove();
      cardEls.delete(key);
    }
  }

  for (const [key, lane] of Object.entries(bodies)) {
    lane.count.textContent = String(counts[key]);
    const empty = lane.body.querySelector('.lane-empty');
    if (!counts[key]) {
      if (!empty) {
        const node = document.createElement('div');
        node.className = 'lane-empty';
        node.textContent = key === 'blocked' ? '没有需要处理的' : '暂无任务';
        lane.body.appendChild(node);
      }
    } else if (empty) {
      empty.remove();
    }
  }

  flipCards(before);
  updateBoardSub(counts);
  renderFlowDock(false);
}

// —— 下方：当前任务的阶段流程 ——
function currentProjectRun() {
  const project = activeProject();
  if (!project) return null;
  const run = project.runs?.find((r) => r.slug === state.selectedRun) || project.runs?.[0] || null;
  if (!run) return null;
  return { project, run, pipe: pipeRunFor(project.path, run.slug) };
}

function flowSignature(ctx) {
  if (!ctx) return 'none';
  const config = boardState.pipelines.get(ctx.project.path)?.config;
  // stageAgents 用排好序的键值对，避免对象键顺序不同导致签名误判。
  const stageAgents = config?.stageAgents || {};
  const agentSig = Object.keys(stageAgents).sort().map((key) => [key, stageAgents[key]]);
  return [
    ctx.project.path,
    ctx.run.slug,
    ctx.run.updatedAt || '',
    ctx.pipe?.status || '',
    ctx.pipe?.stage || '',
    JSON.stringify(ctx.pipe?.sessions || {}),
    // 阶段 → CLI 的映射也要参与签名，否则下拉改完会被轮询缓存吃掉。
    JSON.stringify(agentSig),
  ].join('|');
}

// 阶段行左侧的品牌头像：CLI 决定 logo，阶段状态决定动效。
function stageAvatarState(stageState) {
  if (stageState === 'completed') return 'done';
  if (stageState === 'running') return 'running';
  if (stageState === 'failed') return 'error';
  return 'idle';
}

function paintStageAvatar(row, agentId, stageState) {
  if (typeof agentAvatarHTML !== 'function') return;
  const slot = row.querySelector('.fava');
  if (!slot) return;
  slot.innerHTML = agentAvatarHTML(agentId, {
    size: 18,
    state: stageAvatarState(stageState),
    label: agentLabel(agentId),
  });
}

// 流程图上的「跑者」头像跟着当前阶段的 CLI 走：下拉换终端后立即换品牌。
function refreshRunnerAvatar(canvas, run, pipe, config) {
  if (typeof agentAvatarHTML !== 'function') return;
  const runner = canvas.querySelector('.wf-runner');
  if (!runner) return;
  const stageKey = pipe?.stage || run.currentStage || nextStageOf(run) || 'TASK-01';
  let index = STAGES.findIndex((s) => s.key === stageKey);
  if (index < 0) index = run.status === 'completed' ? STAGES.length - 1 : 0;
  const agentId = stageAgentId(config, STAGES[index].key);
  const state = !pipe
    ? (run.status === 'completed' ? 'done' : 'idle')
    : { running: 'running', waiting: 'waiting', paused: 'waiting', error: 'error', done: 'done' }[pipe.status] || 'idle';
  runner.innerHTML = agentAvatarHTML(agentId, {
    size: 30,
    state,
    label: `${agentLabel(agentId)} · ${STAGES[index].label}阶段`,
  });
}

function renderFlowDock(force) {
  const ctx = currentProjectRun();
  const sig = flowSignature(ctx);
  if (!force && sig === boardState.flowSig) return;
  boardState.flowSig = sig;

  const canvas = $('flowCanvas');
  const agents = $('flowAgents');
  const toggle = $('flowToggle');
  const term = $('flowTerm');

  if (!ctx) {
    $('flowTitle').textContent = '当前任务';
    $('flowMeta').textContent = '';
    const empty = $('flowStatus');
    empty.className = 'flow-status';
    empty.textContent = '—';
    toggle.disabled = true;
    toggle.onclick = null;
    term.disabled = true;
    term.onclick = null;
    $('flowNext').hidden = true;
    $('flowReject').hidden = true;
    canvas.innerHTML = '<div class="flow-empty">点上方卡片选择一个任务，这里会显示它的阶段流程</div>';
    agents.innerHTML = '';
    return;
  }

  const { project, run, pipe } = ctx;
  $('flowTitle').textContent = run.taskId || run.slug;
  const editions = [...new Set(Object.values(pipe?.editions || {}))];
  const reworks = Object.values(pipe?.reworks || {}).reduce((sum, n) => sum + n, 0);
  $('flowMeta').textContent = [
    run.sizeClass || 'medium',
    '独立终端驱动',
    editions.length ? `LoopForge ${editions.join('/')}` : '',
    reworks ? `返工 ${reworks} 轮` : '',
    `更新于 ${relTime(run.updatedAt) || '刚刚'}`,
  ].filter(Boolean).join(' · ');

  const derived = pipe?.status || (run.status === 'completed' ? 'done' : 'idle');
  const statusEl = $('flowStatus');
  statusEl.className = `flow-status ${derived}`;
  statusEl.textContent = pipe
    ? { running: '流水线运行中', waiting: '等待你输入', paused: '等待确认', error: '出错', done: '已完成', stopped: '已停止' }[pipe.status] || pipe.status
    : run.status === 'completed' ? '工作流已完成' : '未启动流水线';

  canvas.innerHTML = '';
  drawGraph(run, canvas, true);

  const config = pipelineOf(project.path).config;
  mountFlowRunner(canvas, project, run, pipe, config);
  agents.innerHTML = '';
  for (const stage of STAGES) {
    const stageState = pipe?.status === 'running' && pipe.stage === stage.key
      ? 'running'
      : stageStatus(run, stage.key);
    const row = document.createElement('div');
    row.className = `fagent ${stageState}`;
    row.dataset.stage = stage.key;
    row.innerHTML = '<span class="fava"></span><span class="sdot"></span><b></b><span class="frework" hidden></span><select></select>';
    row.querySelector('b').textContent = stage.label;
    const rowAgent = stageAgentId(config, stage.key);
    paintStageAvatar(row, rowAgent, stageState);
    row.title = `${run.stages?.[stage.key]?.artifact_path || stage.key} · 点击打开该阶段终端`;
    const rework = pipe?.reworks?.[stage.key] || 0;
    const badge = row.querySelector('.frework');
    badge.hidden = !rework;
    badge.textContent = `返工 ${rework}`;
    const select = row.querySelector('select');
    for (const agent of installedAgents()) {
      const opt = document.createElement('option');
      opt.value = agent.id;
      opt.textContent = agent.name;
      select.appendChild(opt);
    }
    const mapped = stageAgentId(config, stage.key);
    select.value = mapped;
    if (![...select.options].some((o) => o.value === mapped)) {
      const opt = document.createElement('option');
      opt.value = mapped;
      opt.textContent = agentLabel(mapped);
      select.appendChild(opt);
      select.value = mapped;
    }
    let selected = mapped;
    select.addEventListener('click', (event) => event.stopPropagation());
    select.addEventListener('change', async () => {
      const nextAgent = select.value;
      if (!nextAgent || nextAgent === selected) return;
      const cfg = pipelineOf(project.path).config;
      // 本地也存成 {agent} 结构，和 daemon 读回来的配置保持一致，避免签名对不上导致重建。
      const stageAgents = { ...(cfg.stageAgents || {}), [stage.key]: { agent: nextAgent } };
      select.disabled = true;
      try {
        // daemon 返回规范化后的配置（字符串 → {agent}），直接用它的结果，避免本地和读回不一致。
        const saved = await boardApi.savePipeline(project.path, { stageAgents });
        const latest = saved?.config || { ...cfg, stageAgents };
        const cached = boardState.pipelines.get(project.path);
        if (cached) cached.config = latest;
        else cfg.stageAgents = latest.stageAgents;
        selected = nextAgent;
        // 就地换头像，不用等下一轮轮询；流程图上的跑者也一起换。
        paintStageAvatar(row, nextAgent, stageState);
        refreshRunnerAvatar(canvas, run, pipe, latest);
        // 同步刷新签名，避免 2 秒轮询把整排节点重建、丢掉焦点。
        boardState.flowSig = flowSignature(ctx);
        boardToast(`${stage.label}阶段改用 ${agentLabel(nextAgent)}`);
      } catch (error) {
        select.value = selected;
        boardToast(`保存阶段 CLI 失败：${error.message || error}`);
      } finally {
        select.disabled = false;
      }
    });
    row.addEventListener('click', () => openStageTerminal(project, run, pipe, stage.key));
    agents.appendChild(row);
  }

  toggle.disabled = false;
  toggle.textContent = { running: '暂停', paused: '继续', waiting: '继续', done: '重新启动' }[derived] || '启动流水线';
  toggle.onclick = async () => {
    toggle.disabled = true;
    try {
      if (derived === 'running') {
        await boardApi.stop(project.path, run.slug);
      } else if (derived === 'paused' || derived === 'waiting') {
        await boardApi.resume(project.path, run.slug);
      } else {
        await startPipeline({ root: project.path, slug: run.slug });
      }
      await refresh(true);
      await renderBoard(true);
    } catch (error) {
      alert(`操作失败：${error.message || error}`);
    } finally {
      toggle.disabled = false;
    }
  };

  term.disabled = false;
  term.onclick = () => openStageTerminal(project, run, pipe, pipe?.stage);

  // 人工打回：只有停在某个阶段（暂停 / 等你 / 出错 / 已停止）时才出现。
  const reject = $('flowReject');
  const stageKey = pipe?.stage || run.currentStage || '';
  const stagePos = STAGES.findIndex((s) => s.key === stageKey);
  const rejectable = Boolean(pipe)
    && ['paused', 'waiting', 'error', 'stopped'].includes(pipe.status)
    && stagePos > 0;
  reject.hidden = !rejectable;
  reject.disabled = !rejectable;
  reject.onclick = rejectable ? () => openReject(project, run, pipe) : null;

  // 任务结束后给一个直接开下一个任务的入口。
  const next = $('flowNext');
  next.hidden = derived !== 'done';
  next.onclick = () => $('newPipeline').click();
}

// mountFlowRunner 把「当前阶段的 CLI 头像」放到流程图的阶段节点上：
// 阶段推进时它从上一个节点跳到下一个节点，让看板跟着进度动起来。
function mountFlowRunner(canvas, project, run, pipe, config) {
  if (typeof agentAvatarHTML !== 'function') return;
  const svg = canvas.querySelector('svg');
  if (!svg) return;
  const stageKey = pipe?.stage || run.currentStage || nextStageOf(run) || 'TASK-01';
  let index = STAGES.findIndex((s) => s.key === stageKey);
  if (index < 0) index = run.status === 'completed' ? STAGES.length - 1 : 0;
  const runnerStage = STAGES[index].key;
  const agentId = stageAgentId(config, runnerStage);
  const state = !pipe
    ? (run.status === 'completed' ? 'done' : 'idle')
    : { running: 'running', waiting: 'waiting', paused: 'waiting', error: 'error', done: 'done' }[pipe.status] || 'idle';

  const size = 30;
  const x = PAD + index * (NODE_W + GAP_X) + NODE_W - size - 6;
  const y = PAD - 9;
  const runner = document.createElementNS('http://www.w3.org/2000/svg', 'g');
  runner.setAttribute('class', 'wf-runner');
  runner.style.transform = `translate(${x}px, ${y}px)`;
  runner.innerHTML = agentAvatarHTML(agentId, {
    size,
    state,
    label: `${agentLabel(agentId)} · ${STAGES[index].label}阶段`,
  });
  svg.appendChild(runner);

  const key = `${project.path}|${run.slug}`;
  const prevStage = boardState.runnerStage.get(key);
  boardState.runnerStage.set(key, runnerStage);
  if (!prevStage || prevStage === runnerStage) return;
  if (window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches) return;
  const prevIndex = STAGES.findIndex((s) => s.key === prevStage);
  if (prevIndex < 0) return;

  // 从上一个阶段的位置跳过来：中间抬高一点，像跨过节点。
  const fromX = PAD + prevIndex * (NODE_W + GAP_X) + NODE_W - size - 6;
  runner.animate([
    { transform: `translate(${fromX}px, ${y}px)` },
    { transform: `translate(${(fromX + x) / 2}px, ${y - 20}px)`, offset: .45 },
    { transform: `translate(${x}px, ${y}px)` },
  ], { duration: 620, easing: 'cubic-bezier(.3,.9,.35,1)' });
}

// flipCards 让卡片跨泳道移动时平滑过渡，而不是瞬移。
function flipCards(before) {
  for (const [key, el] of cardEls) {
    const prev = before.get(key);
    if (!prev || !el.isConnected) continue;
    const now = el.getBoundingClientRect();
    const dx = prev.left - now.left;
    const dy = prev.top - now.top;
    if (Math.abs(dx) < 2 && Math.abs(dy) < 2) continue;
    el.animate(
      [{ transform: `translate(${dx}px, ${dy}px)` }, { transform: 'none' }],
      { duration: 280, easing: 'cubic-bezier(.2,.7,.3,1)' },
    );
  }
}

function updateBoardSub(counts) {
  const project = activeProject();
  const projectEl = $('boardProject');
  const metaEl = $('boardMeta');
  const modeEl = $('boardMode');
  if (!project) {
    $('boardSub').textContent = '先在左侧选择一个项目';
    if (projectEl) projectEl.textContent = '—';
    if (metaEl) metaEl.textContent = '';
    if (modeEl) modeEl.textContent = 'AgentDeck 驱动 · 每阶段独立终端';
    return;
  }
  const total = project.runs?.length || 0;
  $('boardSub').textContent = `${total} 个任务 · ${counts.working} 个进行中 · ${counts.blocked} 个等你处理 · 每 2 秒刷新`;
  if (projectEl) projectEl.textContent = project.name;
  if (metaEl) {
    metaEl.textContent = project.path;
    metaEl.title = project.path;
  }
  if (modeEl) {
    const editions = Object.values(pipelineOf(project.path).editions || {});
    const unique = [...new Set(editions)];
    modeEl.textContent = unique.length
      ? `AgentDeck 驱动 · LoopForge ${unique.join(' / ')}`
      : 'AgentDeck 驱动 · 每阶段独立终端';
  }
}

function makeCard() {
  const card = document.createElement('article');
  card.innerHTML = `
    <div class="card-meta">
      <span class="card-tag"></span>
      <span class="card-id"></span>
    </div>
    <h3 class="card-task"></h3>
    <p class="card-desc"></p>
    <div class="card-bar"></div>
    <div class="card-tail" hidden></div>
    <div class="card-agents"></div>
    <div class="card-foot">
      <span class="card-state" data-tone="neutral"></span>
      <span class="card-time"></span>
      <span class="card-open">打开 →</span>
    </div>`;

  const bar = card.querySelector('.card-bar');
  for (const item of STAGES) {
    const seg = document.createElement('i');
    seg.dataset.stage = item.key;
    bar.appendChild(seg);
  }

  // 点卡片 = 选中任务，下方阶段流程跟着切换。
  card.addEventListener('click', () => {
    if (card.dataset.root !== state.active) return;
    state.selectedRun = card.dataset.slug;
    for (const el of cardEls.values()) {
      el.classList.toggle('selected', el.dataset.slug === state.selectedRun);
    }
    renderRail();
    renderPanel();
    renderFlowDock(true);
  });
  return card;
}

function updateCard(card, project, run, pipe, lane) {
  const current = currentProjectRun();
  card.className = `card ${lane}${pipe?.status === 'waiting' ? ' waiting' : ''}${current?.run.slug === run.slug ? ' selected' : ''}`;
  card.dataset.slug = run.slug;
  card.dataset.root = project.path;

  const done = run.status === 'completed';
  const stage = done ? 'DONE' : (pipe?.stage || run.currentStage || nextStageOf(run) || '');
  const stageInfo = STAGES.find((s) => s.key === stage);
  const cfg = pipelineOf(project.path).config;
  const agentId = stageInfo ? stageAgentId(cfg, stageInfo.key) : '';

  // 路线图卡片：分类 + 编号 → 标题 → 描述 → 状态。
  const tagEl = card.querySelector('.card-tag');
  const tagText = done ? '已交付' : (stageInfo ? `${stageInfo.label}阶段` : '待启动');
  const laneState = lane === 'working' ? 'running' : lane === 'blocked' ? 'waiting' : 'idle';
  // 只有内容变了才重建：2 秒轮询里重复 innerHTML 会让品牌头像的动效从头重播。
  const tagSig = `${done ? '' : agentId}|${laneState}|${tagText}`;
  if (tagEl.dataset.sig !== tagSig) {
    tagEl.dataset.sig = tagSig;
    if (!done && stageInfo && agentId && typeof agentAvatarHTML === 'function') {
      tagEl.innerHTML = agentAvatarHTML(agentId, { size: 14, state: laneState, label: agentLabel(agentId) });
      tagEl.appendChild(document.createTextNode(tagText));
      tagEl.classList.add('has-ava');
    } else {
      tagEl.textContent = tagText;
      tagEl.classList.remove('has-ava');
    }
  }
  card.querySelector('.card-id').textContent = `#${shortId(run.slug)}`;
  card.querySelector('.card-task').textContent = run.taskId || run.slug;

  const desc = card.querySelector('.card-desc');
  if (done) {
    desc.textContent = `6 个阶段已完成，产物和交接记录都在 artifacts/${run.slug} 里。`;
  } else if (stageInfo) {
    const progress = `${stageProgress(run)}/${STAGES.length}`;
    desc.textContent = `${stageInfo.label}阶段由 ${agentLabel(agentId)} 执行，进度 ${progress}${pipe?.message ? ` · ${pipe.message}` : ''}`;
  } else {
    desc.textContent = pipe?.message || '等待启动流水线';
  }

  card.querySelectorAll('.card-bar i').forEach((seg) => {
    seg.className = stageStatus(run, seg.dataset.stage);
  });

  const key = cardKey(project.path, run.slug);
  const tail = card.querySelector('.card-tail');
  const text = boardState.tails.get(key) || '';
  tail.hidden = !text || (lane !== 'working' && lane !== 'blocked');
  if (!tail.hidden) tail.textContent = text;

  const agents = card.querySelector('.card-agents');
  const unique = [];
  for (const item of STAGES) {
    const id = stageAgentId(cfg, item.key);
    if (!unique.some((x) => x.id === id)) unique.push({ id, stages: [] });
    unique.find((x) => x.id === id).stages.push(item.label);
  }
  const agentsSig = unique.map((x) => `${x.id}:${x.stages.join('/')}`).join(',')
    + `|${done ? 'done' : agentId}|${laneState}`;
  if (agents.dataset.sig !== agentsSig) {
    agents.dataset.sig = agentsSig;
    agents.innerHTML = '';
    for (const entry of unique.slice(0, 3)) {
      const chip = document.createElement('span');
      chip.className = 'achip';
      const isCurrent = !done && stageInfo && entry.id === agentId;
      const state = isCurrent ? laneState : 'idle';
      if (typeof agentAvatarHTML === 'function') {
        chip.innerHTML = agentAvatarHTML(entry.id, { size: 13, state, label: agentLabel(entry.id) }) + '<span></span>';
        chip.querySelector('span').textContent = agentLabel(entry.id);
      } else {
        chip.textContent = agentLabel(entry.id);
      }
      if (isCurrent) chip.classList.add('current');
      chip.title = `负责：${entry.stages.join('、')}`;
      agents.appendChild(chip);
    }
  }

  const stateEl = card.querySelector('.card-state');
  const badge = {
    queued: ['排队中', 'neutral'],
    working: ['进行中', 'info'],
    blocked: ['待你处理', 'warn'],
    done: ['已完成', 'success'],
  }[lane] || ['—', 'neutral'];
  stateEl.textContent = badge[0];
  stateEl.dataset.tone = badge[1];

  const time = card.querySelector('.card-time');
  time.dataset.since = pipe?.startedAt || '';
  time.dataset.progress = `${stageProgress(run)}/${STAGES.length}`;
  time.textContent = cardTimeText(time, done);
}

function cardTimeText(node, done) {
  const progress = node.dataset.progress || '';
  if (done) return `${progress} 阶段 · 已收工`;
  const elapsed = elapsedOf({ startedAt: node.dataset.since });
  return elapsed ? `${progress} 阶段 · ${elapsed}` : progress;
}

function nextStageOf(run) {
  for (const item of STAGES) {
    const st = run.stages?.[item.key]?.status || '';
    if (!['completed', 'passed'].includes(String(st).toLowerCase())) return item.key;
  }
  return '';
}

async function openStageTerminal(project, run, pipe, stageKey) {
  let id = '';
  if (pipe?.sessions) {
    id = stageKey ? (pipe.sessions[stageKey] || '') : (pipe.sessions[pipe.stage] || Object.values(pipe.sessions).pop() || '');
  }
  if (!id && stageKey) {
    const stage = STAGES.find((s) => s.key === stageKey);
    boardToast(`${stage ? stage.label : stageKey} 阶段终端已归档：artifacts/${run.slug}/.agentdeck/stages/${stageKey}.log`);
    return;
  }
  if (!id) {
    switchTab('terminal');
    openModal(project.path);
    return;
  }
  if (!sessions.has(id)) {
    const list = await api.sessions();
    const info = list.find((s) => s.id === id);
    if (info) attachSession(info);
  }
  if (sessions.has(id)) {
    switchTab('terminal');
    setActive(id);
  }
}

// startPipeline 在任务由 LoopForge 宿主驱动时，先确认再由 AgentDeck 接管。
async function startPipeline(payload) {
  try {
    return await boardApi.start(payload);
  } catch (error) {
    const message = String(error?.message || error);
    if (message.includes('驱动')) {
      const ok = await askConfirm({
        title: '接管这个任务？',
        text: '这个任务目前由 LoopForge 宿主驱动。AgentDeck 接管后会与宿主同时推进同一个任务，可能互相覆盖状态。确认接管吗？',
        okText: '强制接管',
        danger: true,
      });
      if (!ok) return null;
      return boardApi.start({ ...payload, force: true });
    }
    throw error;
  }
}

// —— 新建流水线（固定属于当前项目） ——
async function fillPipelineModal() {
  await ensureBoardAgents();
  const project = activeProject();
  $('pipeProjectLabel').textContent = project ? `${project.name} · ${project.path}` : '先在左侧选择一个项目';

  const map = $('stageMap');
  map.innerHTML = '';
  for (const stage of STAGES) {
    const row = document.createElement('label');
    row.className = 'smap-row';
    row.innerHTML = '<span></span><select></select>';
    row.querySelector('span').textContent = `${stage.label} ${stage.key}`;
    const select = row.querySelector('select');
    for (const agent of installedAgents()) {
      const opt = document.createElement('option');
      opt.value = agent.id;
      opt.textContent = agent.name;
      select.appendChild(opt);
    }
    select.value = stageAgentId(pipelineOf(project.path).config, stage.key);
    if (![...select.options].some((o) => o.value === select.value)) {
      const opt = document.createElement('option');
      opt.value = select.value;
      opt.textContent = select.value;
      select.appendChild(opt);
    }
    row.dataset.stage = stage.key;
    map.appendChild(row);
  }
}

async function createPipeline() {
  const root = state.active;
  const requirement = $('pipeRequirement').value.trim();
  if (!root) return alert('先在左侧选择一个项目');
  if (!requirement) return alert('写一下需求');

  const stageAgents = {};
  document.querySelectorAll('#stageMap .smap-row').forEach((row) => {
    stageAgents[row.dataset.stage] = row.querySelector('select').value;
  });
  await boardApi.savePipeline(root, { stageAgents });
  try {
    const result = await startPipeline({ root, requirement });
    if (!result) return;
    $('pipeModal').hidden = true;
    $('pipeRequirement').value = '';
    state.selectedRun = result.run.slug;
    await refresh(true);
    await renderBoard(true);
    // 新工作流的第一阶段终端由 daemon 在下一个调度周期创建，等它出现后直接打开。
    focusRunTerminal(root, result.run.slug);
  } catch (error) {
    alert(`启动失败：${error.message || error}`);
  }
}

// focusRunTerminal 轮询新任务的会话，出现就切到终端页，让用户立刻看到 CLI 在干活。
async function focusRunTerminal(root, slug) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    const data = await boardApi.pipeline(root).catch(() => null);
    const run = data?.runs?.find((r) => r.slug === slug);
    const ids = run?.sessions ? Object.values(run.sessions) : [];
    const id = ids.find((sid) => sessions.has(sid)) || ids[0] || '';
    if (id) {
      if (!sessions.has(id)) {
        const list = await api.sessions().catch(() => []);
        const info = list.find((s) => s.id === id);
        if (info) attachSession(info);
      }
      if (sessions.has(id)) {
        switchTab('terminal');
        setActive(id);
      }
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
}

// —— 项目内的两种页面：看板（项目首页）/ 终端会话 ——
function switchTab(tab) {
  boardState.tab = tab;
  document.querySelector('.term-card').hidden = tab !== 'terminal';
  $('projectBoard').hidden = tab !== 'board';
  $('backToBoard').hidden = tab !== 'terminal';
  // 看板需要横向空间，切到看板时自动收起右侧工作流面板，切回终端再恢复。
  const panelEl = document.getElementById('panel');
  if (tab === 'board') {
    if (panelEl && !panelEl.hidden) {
      boardState.panelWasOpen = true;
      panelEl.hidden = true;
    }
    renderBoard(true).catch(() => {});
  } else {
    if (panelEl && boardState.panelWasOpen) {
      panelEl.hidden = false;
      boardState.panelWasOpen = false;
    }
    requestAnimationFrame(() => fitActive());
  }
}

// enterTerminal 从左侧终端列表进入某个终端会话页。
function enterTerminal(id) {
  setActive(id);
  switchTab('terminal');
}

function updateProjectHeader() {
  const project = activeProject();
  const badge = $('pbBadge');
  if (!project) {
    badge.textContent = '';
    badge.style.background = 'var(--surface-3)';
    $('pbName').textContent = '未选择项目';
    $('pbPath').textContent = '';
    return;
  }
  badge.textContent = String(project.name).slice(0, 1).toUpperCase();
  badge.style.background = avatarColor(project.name);
  $('pbName').textContent = project.name;
  $('pbPath').textContent = project.path;
}

// openRunBoard 让左侧“运行”列表直接选中该任务并打开项目看板。
function openRunBoard(root, slug) {
  if (root !== state.active) return;
  state.selectedRun = slug;
  switchTab('board');
  renderBoard(true).catch(() => {});
}

$('backToBoard').addEventListener('click', () => switchTab('board'));

$('newPipeline').addEventListener('click', async () => {
  $('pipeModal').hidden = false;
  $('pipeRequirement').focus();
  try {
    await fillPipelineModal();
  } catch (error) {
    alert(`加载 CLI 列表失败：${error.message || error}`);
  }
});
$('pipeCancel').addEventListener('click', () => { $('pipeModal').hidden = true; });
$('pipeCreate').addEventListener('click', createPipeline);
$('pipeModal').addEventListener('click', (event) => {
  if (event.target === $('pipeModal')) $('pipeModal').hidden = true;
});

// —— 人工打回 ——
// 默认把审查 / 测试 / 沉淀退回开发，其余阶段退回上一阶段。
const REJECT_DEFAULTS = {
  'CODE-REVIEW': 'TASK-03',
  'TASK-04': 'TASK-03',
  'TASK-05': 'TASK-03',
  'TASK-03': 'TASK-02',
  'TASK-02': 'TASK-01',
};

let rejectCtx = null;

function openReject(project, run, pipe) {
  const stage = pipe?.stage || run.currentStage || '';
  const pos = STAGES.findIndex((s) => s.key === stage);
  if (pos <= 0) {
    boardToast('当前阶段没有可退回的上游阶段');
    return;
  }
  const current = STAGES[pos];
  rejectCtx = { project, run, stage };
  const select = $('rejectTarget');
  select.innerHTML = '';
  for (const item of STAGES.slice(0, pos)) {
    const opt = document.createElement('option');
    opt.value = item.key;
    opt.textContent = `${item.label} · ${item.key}`;
    select.appendChild(opt);
  }
  const preferred = REJECT_DEFAULTS[stage];
  const preferredPos = STAGES.findIndex((s) => s.key === preferred);
  select.value = preferred && preferredPos >= 0 && preferredPos < pos ? preferred : STAGES[pos - 1].key;
  $('rejectSub').textContent = `把「${current.label}」退回到上游阶段，原因会写进下一轮的返工提示。`;
  $('rejectReason').value = '';
  $('rejectModal').hidden = false;
  setTimeout(() => $('rejectReason').focus(), 30);
}

function closeReject() {
  $('rejectModal').hidden = true;
  rejectCtx = null;
}

async function confirmReject() {
  if (!rejectCtx) return;
  const reason = $('rejectReason').value.trim();
  if (!reason) {
    boardToast('写一句打回原因，下一轮返工才知道要修什么');
    $('rejectReason').focus();
    return;
  }
  const { project, run, stage } = rejectCtx;
  const target = $('rejectTarget').value;
  $('rejectOk').disabled = true;
  try {
    await boardApi.reject({ root: project.path, slug: run.slug, stage, target, reason });
    const fromLabel = STAGES.find((s) => s.key === stage)?.label || stage;
    const toLabel = STAGES.find((s) => s.key === target)?.label || target;
    boardToast(`已打回：${fromLabel} → ${toLabel}`);
    closeReject();
    await refresh(true);
    await renderBoard(true);
  } catch (error) {
    boardToast(`打回失败：${error.message || error}`);
  } finally {
    $('rejectOk').disabled = false;
  }
}

$('rejectCancel').addEventListener('click', closeReject);
$('rejectOk').addEventListener('click', confirmReject);
$('rejectModal').addEventListener('click', (event) => {
  if (event.target === $('rejectModal')) closeReject();
});
document.addEventListener('keydown', (event) => {
  if (event.key === 'Escape' && !$('rejectModal').hidden) closeReject();
});

// 看板页签可见时每 2 秒刷新。
setInterval(() => {
  if (boardState.tab === 'board' && !$('projectBoard').hidden) {
    renderBoard(false).catch(() => {});
  }
}, 2000);

// 每秒只更新耗时，不重绘整块看板。
setInterval(() => {
  if (boardState.tab !== 'board' || $('projectBoard').hidden) return;
  document.querySelectorAll('#lanes .card').forEach((card) => {
    const time = card.querySelector('.card-time');
    if (!time || !time.dataset.since) return;
    time.textContent = cardTimeText(time, card.classList.contains('done'));
  });
}, 1000);