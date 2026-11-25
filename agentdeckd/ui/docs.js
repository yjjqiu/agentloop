// 项目资料看板：把当前项目的文档、任务产物、目录结构、依赖与 Git 状态汇总成一页。
// 只读；与流水线（看板页）和终端互相独立。

const docsState = {
  root: '',
  data: null,
  file: '',
  loading: false,
  filter: '',
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

async function renderDocs(force) {
  const root = (typeof state !== 'undefined' && state.active) || '';
  const side = $('docsSide');
  const view = $('docsView');
  if (!side || !view) return;
  if (!root) {
    side.innerHTML = '<div class="docs-empty">先选择一个项目</div>';
    view.innerHTML = '';
    return;
  }
  const nameEl = $('docsName');
  const pathEl = $('docsPath');
  const project = (typeof currentProject === 'function' ? currentProject() : null);
  if (nameEl) nameEl.textContent = project?.name || root.split(/[\\/]/).pop();
  if (pathEl) pathEl.textContent = root;

  if (force || docsState.root !== root || !docsState.data) {
    docsState.root = root;
    docsState.loading = true;
    side.innerHTML = '<div class="docs-empty">正在扫描项目资料…</div>';
    try {
      docsState.data = await docsApi.overview(root);
    } catch (error) {
      side.innerHTML = `<div class="docs-empty">读取失败：${docsEscape(error.message || error)}</div>`;
      docsState.loading = false;
      return;
    }
    docsState.loading = false;
  }

  const data = docsState.data;
  const target = docsState.file && data.docs.some((doc) => doc.path === docsState.file)
    ? docsState.file
    : (data.manifests.find((doc) => doc.name === 'README.md')?.path
      || data.docs.find((doc) => doc.rel === 'README.md')?.path
      || data.docs[0]?.path
      || '');
  // 先定下要展示的文件，左栏渲染时才能把对应条目标成选中。
  docsState.file = target;
  renderDocsSide();
  if (target) {
    await showDocFile(target);
  } else {
    renderDocsOverview();
  }
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
    side.querySelectorAll('.docs-item').forEach((el) => el.classList.remove('active'));
    overview.classList.add('active');
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
    for (const doc of focusGroup) side.appendChild(docsFileItem(doc, 'docs-doc'));
  }
  for (const [group, items] of groups) {
    if (group === '根目录') continue;
    section(`文档 · ${group}`, items.length);
    for (const doc of items) side.appendChild(docsFileItem(doc, 'docs-doc'));
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
        side.querySelectorAll('.docs-item').forEach((el) => el.classList.remove('active'));
        btn.classList.add('active');
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

function docsFileItem(doc, kind) {
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'docs-item';
  btn.dataset.path = doc.path;
  if (docsState.file === doc.path) btn.classList.add('active');
  btn.innerHTML = `<span class="docs-item-name ${kind}">${docsEscape(doc.rel || doc.name)}</span>
    <span class="docs-item-meta">${docsSize(doc.size)} · ${docsRelTime(doc.modTime)}</span>`;
  btn.addEventListener('click', () => {
    $('docsSide').querySelectorAll('.docs-item').forEach((el) => el.classList.remove('active'));
    btn.classList.add('active');
    showDocFile(doc.path).catch((error) => {
      $('docsView').innerHTML = `<div class="docs-empty">${docsEscape(error.message || error)}</div>`;
    });
  });
  return btn;
}

async function showDocFile(path) {
  const view = $('docsView');
  if (!view) return;
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
      <p class="docs-hint">阶段详情与重跑请回到「看板」页操作。</p>
    </div>`;
}

function renderDocsOverview() {
  const view = $('docsView');
  const data = docsState.data;
  if (!view || !data) return;
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
      ${git ? `<h3 class="docs-h">最近提交</h3><ul class="docs-log">${commits || '<li>还没有提交</li>'}</ul>` : '<p class="docs-hint">这个目录不是 Git 仓库，资料看板只展示文件。</p>'}
      ${changed ? `<h3 class="docs-h">未提交改动</h3><ul class="docs-log">${changed}</ul>` : ''}
    </div>`;
}

function bindDocsUI() {
  const docsBtn = $('openDocs');
  if (docsBtn) docsBtn.addEventListener('click', () => switchTab('docs'));
  const back = $('docsBack');
  if (back) back.addEventListener('click', () => switchTab('board'));
  const refresh = $('docsRefresh');
  if (refresh) refresh.addEventListener('click', () => renderDocs(true).catch(() => {}));
  const filter = $('docsFilter');
  if (filter) {
    filter.addEventListener('input', () => {
      docsState.filter = filter.value.trim();
      renderDocsSide();
    });
  }
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', bindDocsUI);
} else {
  bindDocsUI();
}