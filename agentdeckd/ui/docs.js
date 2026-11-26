// 项目资料：挂在「项目对话」页右侧的只读面板。
// 一页两层——先看分组列表，点条目进入文档预览，返回按钮回到列表。
// 和 SOP 开发（右侧工作流面板）完全分开，这里不碰流水线状态。

const docsState = {
  root: '',
  data: null,
  file: '',
  loading: false,
  filter: '',
  mode: 'list', // list | file
  expanded: new Set(), // 已展开全部条目的分组
};

const docsApi = {
  overview: (root) => req(`/api/v1/projects/overview?root=${encodeURIComponent(root)}`),
  file: (path) => req(`/api/v1/projects/file?path=${encodeURIComponent(path)}`),
};

function docsEscape(text) {
  return String(text == null ? '' : text).replace(/[&<>"']/g, (ch) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[ch]));
}

function docsRelTime(ms) {
  if (!ms) return '';
  const diff = Date.now() - ms;
  const min = Math.round(diff / 60000);
  if (min < 1) return '刚刚';
  if (min < 60) return `${min} 分钟前`;
  const hour = Math.round(min / 60);
  if (hour < 24) return `${hour} 小时前`;
  const day = Math.round(hour / 24);
  if (day < 30) return `${day} 天前`;
  return new Date(ms).toLocaleDateString('zh-CN');
}

function docsSize(bytes) {
  if (!bytes && bytes !== 0) return '';
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

// setDocsMode 在「列表」和「文档预览」之间切换，同一个面板里翻页。
function setDocsMode(mode) {
  docsState.mode = mode === 'file' ? 'file' : 'list';
  const side = $('docsSide');
  const view = $('docsView');
  const back = $('docsBack');
  if (side) side.hidden = docsState.mode === 'file';
  if (view) view.hidden = docsState.mode !== 'file';
  // 返回按钮常驻，只切禁用态，避免标题左右跳。
  if (back) {
    back.disabled = docsState.mode !== 'file';
    back.classList.toggle('off', docsState.mode !== 'file');
  }
}

async function renderDocs(force) {
  const root = (typeof state !== 'undefined' && state.active) || '';
  const side = $('docsSide');
  const view = $('docsView');
  if (!side || !view) return;
  const pathEl = $('docsPath');
  if (pathEl) {
    pathEl.textContent = root;
    pathEl.title = root;
  }
  if (!root) {
    side.innerHTML = '<div class="docs-empty">先选择一个项目</div>';
    docsState.data = null;
    docsState.root = '';
    setDocsMode('list');
    return;
  }

  const switched = docsState.root !== root;
  if (switched) {
    docsState.data = null;
    docsState.file = '';
    docsState.expanded = new Set();
    setDocsMode('list');
  }
  if (force || switched || !docsState.data) {
    docsState.root = root;
    docsState.loading = true;
    if (!docsState.data) side.innerHTML = '<div class="docs-empty">正在扫描项目资料…</div>';
    try {
      docsState.data = await docsApi.overview(root);
    } catch (error) {
      docsState.loading = false;
      docsState.data = null;
      side.innerHTML = `<div class="docs-empty">读取失败：${docsEscape(error.message || error)}</div>`;
      setDocsMode('list');
      return;
    }
    docsState.loading = false;
  }

  // 已经翻到某份文档时不打断阅读，等用户点返回再回列表。
  if (docsState.mode === 'file' && docsState.file) {
    renderDocsSide();
    return;
  }
  setDocsMode('list');
  renderDocsSide();
}

function docsMatches(item, query) {
  if (!query) return true;
  return `${item.rel || item.name || ''}`.toLowerCase().includes(query);
}

function renderDocsSide() {
  const side = $('docsSide');
  const data = docsState.data;
  if (!side || !data) return;
  const query = docsState.filter.toLowerCase();
  side.innerHTML = '';

  const section = (title, count) => {
    const head = document.createElement('div');
    head.className = 'docs-group-head';
    head.innerHTML = `<span>${docsEscape(title)}</span><b>${count}</b>`;
    side.appendChild(head);
  };

  // 1) 概览：Git + 任务数
  const git = data.git;
  section('概览', '');
  const overview = document.createElement('button');
  overview.type = 'button';
  overview.className = 'docs-item docs-item-overview';
  overview.innerHTML = git
    ? `<span class="docs-item-name">分支 <b>${docsEscape(git.branch || '—')}</b></span>
       <span class="docs-item-meta">${git.dirty ? `${git.dirty} 个改动` : '工作区干净'} · ${data.artifacts.length} 个任务</span>`
    : `<span class="docs-item-name">非 Git 仓库</span>
       <span class="docs-item-meta">${data.artifacts.length} 个任务 · ${data.tree.length} 项顶层内容</span>`;
  overview.addEventListener('click', () => {
    docsState.file = '';
    renderDocsOverview();
  });
  side.appendChild(overview);

  // 2) 清单
  const manifests = data.manifests.filter((item) => docsMatches(item, query));
  if (manifests.length) {
    section('清单', manifests.length);
    for (const item of manifests) side.appendChild(docsFileItem(item, 'docs-manifest'));
  }

  // 3) 文档：按一级目录分组；根目录的清单已经单独列出，这里不重复
  const manifestPaths = new Set(data.manifests.map((item) => item.path));
  const groups = new Map();
  for (const doc of data.docs) {
    if (manifestPaths.has(doc.path)) continue;
    if (!docsMatches(doc, query)) continue;
    if (!groups.has(doc.group)) groups.set(doc.group, []);
    groups.get(doc.group).push(doc);
  }
  const focusGroup = groups.get('根目录') || [];
  if (focusGroup.length) {
    section('文档 · 根目录', focusGroup.length);
    appendDocsGroup(side, '根目录', focusGroup);
  }
  for (const [group, items] of groups) {
    if (group === '根目录') continue;
    section(`文档 · ${group}`, items.length);
    appendDocsGroup(side, group, items);
  }

  // 4) 任务产物
  if (data.artifacts.length) {
    section('任务产物', data.artifacts.length);
    for (const artifact of data.artifacts) {
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'docs-item';
      const stageCount = Object.keys(artifact.stages || {}).length;
      btn.innerHTML = `<span class="docs-item-name">${docsEscape(artifact.slug)}</span>
        <span class="docs-item-meta">${docsEscape(artifact.currentStage || '—')} · ${stageCount} 阶段 · ${docsRelTime(artifact.updatedAt)}</span>`;
      btn.addEventListener('click', () => {
        docsState.file = '';
        renderArtifactDetail(artifact);
      });
      side.appendChild(btn);
    }
  }

  // 5) 目录结构
  if (data.tree.length) {
    section('顶层结构', data.tree.length);
    const tree = document.createElement('div');
    tree.className = 'docs-tree';
    tree.innerHTML = data.tree.map((entry) => (entry.dir
      ? `<span class="docs-tree-dir">▸ ${docsEscape(entry.name)}<em>${entry.count ?? 0}</em></span>`
      : `<span class="docs-tree-file">· ${docsEscape(entry.name)}<em>${docsSize(entry.size)}</em></span>`)).join('');
    side.appendChild(tree);
  }
}

function docsClearActive() {
  $('docsSide')?.querySelectorAll('.docs-item').forEach((el) => el.classList.remove('active'));
}

// 单个分组默认只列前 8 项，右侧面板本来就窄，长列表留给「展开」。
const DOCS_GROUP_LIMIT = 8;

function appendDocsGroup(side, group, items) {
  const expanded = docsState.expanded.has(group);
  const limit = expanded ? items.length : Math.min(items.length, DOCS_GROUP_LIMIT);
  for (let i = 0; i < limit; i += 1) side.appendChild(docsFileItem(items[i], 'docs-doc'));
  if (expanded || items.length <= DOCS_GROUP_LIMIT) return;
  const more = document.createElement('button');
  more.type = 'button';
  more.className = 'docs-more';
  more.textContent = `展开其余 ${items.length - DOCS_GROUP_LIMIT} 项`;
  more.addEventListener('click', () => {
    docsState.expanded.add(group);
    renderDocsSide();
  });
  side.appendChild(more);
}

function docsFileItem(doc, kind) {
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'docs-item';
  btn.dataset.path = doc.path;
  if (docsState.file === doc.path) btn.classList.add('active');
  btn.innerHTML = `<span class="docs-item-name ${kind}">${docsEscape(doc.rel || doc.name)}</span>
    <span class="docs-item-meta">${docsSize(doc.size)} · ${docsRelTime(doc.modTime)}</span>`;
  btn.addEventListener('click', () => {
    docsClearActive();
    btn.classList.add('active');
    showDocFile(doc.path).catch((error) => {
      const view = $('docsView');
      if (view) view.innerHTML = `<div class="docs-empty">${docsEscape(error.message || error)}</div>`;
      setDocsMode('file');
    });
  });
  return btn;
}

async function showDocFile(path) {
  const view = $('docsView');
  if (!view) return;
  setDocsMode('file');
  view.innerHTML = '<div class="docs-empty">读取中…</div>';
  const file = await docsApi.file(path);
  docsState.file = path;
  const rel = docsState.data
    ? (docsState.data.docs.find((d) => d.path === path)?.rel
      || docsState.data.manifests.find((d) => d.path === path)?.rel
      || path)
    : path;
  const body = typeof renderReleaseMarkdown === 'function'
    ? renderReleaseMarkdown(file.content || '')
    : `<pre>${docsEscape(file.content || '')}</pre>`;
  view.innerHTML = `
    <header class="docs-view-head">
      <div>
        <div class="docs-view-name">${docsEscape(rel)}</div>
        <div class="docs-view-meta">${docsSize(file.size)} · ${docsRelTime(file.modTime)}</div>
      </div>
      <button class="btn mini ghost docs-view-copy" type="button">复制路径</button>
    </header>
    <div class="docs-view-body">${body}</div>`;
  view.querySelector('.docs-view-copy')?.addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText(file.path);
      boardToast?.('路径已复制');
    } catch {
      boardToast?.('复制失败');
    }
  });
  view.scrollTop = 0;
}

const DOCS_STAGE_LABEL = {
  completed: '已完成',
  passed: '已通过',
  running: '进行中',
  in_progress: '进行中',
  failed: '失败',
  blocked: '阻塞',
  skipped: '跳过',
  pending: '待开始',
};

// docsStageRank 让 TASK-01…05 排在前面，再是审查，其余按名字。
function docsStageRank(key) {
  const task = /^TASK-(\d+)$/i.exec(key);
  if (task) return 100 + Number(task[1]);
  if (/^CODE-REVIEW$/i.test(key)) return 200;
  if (/^SOLO$/i.test(key)) return 300;
  return 400;
}

function renderArtifactDetail(artifact) {
  const view = $('docsView');
  if (!view) return;
  setDocsMode('file');
  docsState.file = '';
  const rows = Object.entries(artifact.stages || {})
    .sort((a, b) => docsStageRank(a[0]) - docsStageRank(b[0]) || a[0].localeCompare(b[0]))
    .map(([key, status]) => `<div class="docs-stage"><span>${docsEscape(key)}</span><b class="docs-stage-${docsEscape(status)}">${docsEscape(DOCS_STAGE_LABEL[status] || status)}</b></div>`)
    .join('');
  view.innerHTML = `
    <header class="docs-view-head">
      <div>
        <div class="docs-view-name">${docsEscape(artifact.slug)}</div>
        <div class="docs-view-meta">当前阶段 ${docsEscape(artifact.currentStage || '—')} · 更新于 ${docsRelTime(artifact.updatedAt)}</div>
      </div>
    </header>
    <div class="docs-view-body">
      <div class="docs-kv"><span>运行模式</span><b>${docsEscape(artifact.runtimeMode || '—')}</b></div>
      <div class="docs-kv"><span>最近事件</span><b>${docsEscape(artifact.lastEvent || '—')}</b></div>
      <div class="docs-stages">${rows || '<div class="docs-empty">还没有阶段记录</div>'}</div>
      <p class="docs-hint">阶段详情与重跑请回到「SOP 开发 → 工作流」操作。</p>
    </div>`;
  view.scrollTop = 0;
}

function renderDocsOverview() {
  const view = $('docsView');
  const data = docsState.data;
  if (!view || !data) return;
  setDocsMode('file');
  const git = data.git;
  const commits = (git?.commits || [])
    .map((c) => `<li><code>${docsEscape(c.sha)}</code><span>${docsEscape(c.subject)}</span><em>${docsEscape(c.when)}</em></li>`)
    .join('');
  const changed = (git?.changed || [])
    .slice(0, 24)
    .map((line) => `<li><code>${docsEscape(line.slice(0, 2).trim() || '·')}</code><span>${docsEscape(line.slice(2).trim())}</span></li>`)
    .join('');
  view.innerHTML = `
    <header class="docs-view-head">
      <div>
        <div class="docs-view-name">${docsEscape(data.name)}</div>
        <div class="docs-view-meta">${docsEscape(data.root)}</div>
      </div>
    </header>
    <div class="docs-view-body">
      <div class="docs-kpis">
        <div class="docs-kpi"><b>${data.docs.length}</b><span>份文档</span></div>
        <div class="docs-kpi"><b>${data.artifacts.length}</b><span>个任务</span></div>
        <div class="docs-kpi"><b>${git ? git.dirty : '—'}</b><span>处改动</span></div>
        <div class="docs-kpi"><b>${data.tree.length}</b><span>项顶层内容</span></div>
      </div>
      ${git ? `<h3 class="docs-h">最近提交</h3><ul class="docs-log">${commits || '<li>还没有提交</li>'}</ul>` : '<p class="docs-hint">这个目录不是 Git 仓库，资料面板只展示文件。</p>'}
      ${changed ? `<h3 class="docs-h">未提交改动</h3><ul class="docs-log">${changed}</ul>` : ''}
    </div>`;
  view.scrollTop = 0;
}

function bindDocsUI() {
  const back = $('docsBack');
  if (back) {
    back.addEventListener('click', () => {
      docsState.file = '';
      setDocsMode('list');
      renderDocsSide();
    });
  }
  const refresh = $('docsRefresh');
  if (refresh) refresh.addEventListener('click', () => { renderDocs(true).catch(() => {}); });
  const filter = $('docsFilter');
  if (filter) {
    filter.addEventListener('input', () => {
      docsState.filter = filter.value.trim();
      if (docsState.mode === 'list') renderDocsSide();
    });
  }
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', bindDocsUI);
} else {
  bindDocsUI();
}