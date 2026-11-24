const { app, BrowserWindow, Menu, dialog, ipcMain, shell } = require('electron');
const path = require('node:path');
const fs = require('node:fs');
const { spawn } = require('node:child_process');

// 外壳只做三件事：拉起 daemon、开窗口、提供原生对话框。
const PORT = Number(process.env.AGENTDECK_PORT || 3210);
const BASE = `http://127.0.0.1:${PORT}`;
const SMOKE = process.argv.includes('--smoke');
// 调试用：--shot=<png> 载入后截图退出
const SHOT = (process.argv.find((a) => a.startsWith('--shot=')) || '').slice('--shot='.length);

// Windows 任务栏分组 / 通知标识，和 electron-builder 的 appId 保持一致。
app.setAppUserModelId('app.agentdeck.desktop');

let mainWindow = null;
let daemon = null;
let daemonOwned = false;

function daemonPath() {
  if (process.env.AGENTDECK_DAEMON) return process.env.AGENTDECK_DAEMON;
  if (app.isPackaged) return path.join(process.resourcesPath, 'agentdeckd.exe');
  return path.join(__dirname, '..', 'agentdeckd', 'agentdeckd.exe');
}

async function health() {
  try {
    const res = await fetch(`${BASE}/api/v1/health`, { signal: AbortSignal.timeout(1500) });
    return res.ok;
  } catch {
    return false;
  }
}

async function waitHealthy(totalMs) {
  const deadline = Date.now() + totalMs;
  while (Date.now() < deadline) {
    if (await health()) return true;
    await new Promise((resolve) => setTimeout(resolve, 300));
  }
  return false;
}

async function ensureDaemon() {
  if (await health()) return { ok: true, owned: false };
  const exe = daemonPath();
  if (!fs.existsSync(exe)) {
    return { ok: false, error: `找不到 daemon：${exe}` };
  }
  try {
    daemon = spawn(exe, ['-addr', `127.0.0.1:${PORT}`], { windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
  } catch (error) {
    return { ok: false, error: `启动 daemon 失败：${error?.message || error}` };
  }
  daemonOwned = true;
  daemon.stdout?.on('data', (d) => process.stdout.write(String(d)));
  daemon.stderr?.on('data', (d) => process.stderr.write(String(d)));
  daemon.on('exit', (code) => {
    daemon = null;
    if (code !== 0 && mainWindow && !mainWindow.isDestroyed()) {
      mainWindow.webContents.send('daemon:exit', code);
    }
  });

  if (await waitHealthy(20000)) return { ok: true, owned: true };
  return { ok: false, error: 'daemon 启动超时，请看控制台日志' };
}

function showError(message) {
  const html = `<!doctype html><meta charset="utf-8">
  <style>
    html,body{height:100%;margin:0;background:#0b0f18;color:#e8edf6;font-family:"Segoe UI","Microsoft YaHei",sans-serif}
    .w{height:100%;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:10px;padding:40px;text-align:center}
    h1{font-size:18px;margin:0}
    p{margin:0;color:#8fa0ba;font-size:13px;max-width:560px;line-height:1.6;font-family:Consolas,monospace}
    button{margin-top:12px;height:34px;padding:0 16px;border:none;border-radius:8px;background:linear-gradient(135deg,#5b8cff,#8b5cf6);color:#fff;cursor:pointer}
  </style>
  <div class="w"><h1>本地服务没起来</h1><p>${message.replace(/[<>&]/g, '')}</p>
  <button onclick="location.reload()">重试</button></div>`;
  mainWindow.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent(html)}`);
}

function createWindow(url) {
  mainWindow = new BrowserWindow({
    width: 1500,
    height: 920,
    minWidth: 1000,
    minHeight: 620,
    backgroundColor: '#0b0f18',
    title: `AgentDeck v${app.getVersion()}`,
    frame: false, // 标题栏和窗口键都画在页面里，和界面融为一体
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
    },
  });

  const sendMax = () => {
    if (mainWindow && !mainWindow.isDestroyed()) {
      mainWindow.webContents.send('win:maximized', mainWindow.isMaximized());
    }
  };
  mainWindow.on('maximize', sendMax);
  mainWindow.on('unmaximize', sendMax);

  mainWindow.on('closed', () => { mainWindow = null; });
  mainWindow.webContents.setWindowOpenHandler(({ url: target }) => {
    if (/^https?:/i.test(target)) shell.openExternal(target);
    return { action: 'deny' };
  });
  mainWindow.loadURL(url);
  if (SHOT) {
    mainWindow.webContents.once('did-finish-load', () => {
      setTimeout(async () => {
        try {
          const image = await mainWindow.webContents.capturePage();
          fs.writeFileSync(SHOT, image.toPNG());
          console.log(`shot saved: ${SHOT}`);
        } catch (error) {
          console.error(error);
        }
        app.exit(0);
      }, 6000);
    });
  }
  return mainWindow;
}

function buildMenu() {
  Menu.setApplicationMenu(Menu.buildFromTemplate([
    {
      label: '文件',
      submenu: [
        { label: '新建终端', click: () => mainWindow?.webContents.send('ui:new-session') },
        { label: '关闭当前终端', click: () => mainWindow?.webContents.send('ui:close-session') },
        { type: 'separator' },
        { label: '重启本地服务', click: restartDaemon },
        { type: 'separator' },
        { label: '退出', role: 'quit' },
      ],
    },
    {
      label: '视图',
      submenu: [
        { role: 'reload' },
        { role: 'toggleDevTools' },
        { type: 'separator' },
        { role: 'resetZoom' },
        { role: 'zoomIn' },
        { role: 'zoomOut' },
        { role: 'togglefullscreen' },
      ],
    },
  ]));
}

async function restartDaemon() {
  if (daemon) {
    daemon.kill();
    daemon = null;
  }
  const result = await ensureDaemon();
  if (result.ok) mainWindow?.loadURL(BASE);
  else showError(result.error);
}

ipcMain.handle('shell:pick-dir', async () => {
  const result = await dialog.showOpenDialog(mainWindow, { properties: ['openDirectory'] });
  return result.canceled ? '' : result.filePaths[0];
});

ipcMain.handle('shell:info', () => ({
  daemon: daemonPath(),
  port: PORT,
  owned: daemonOwned,
  version: app.getVersion(),
}));

// 无边框窗口的控制按钮
ipcMain.handle('win:minimize', () => { mainWindow?.minimize(); });
ipcMain.handle('win:close', () => { mainWindow?.close(); });
ipcMain.handle('win:is-max', () => Boolean(mainWindow?.isMaximized()));
ipcMain.handle('win:toggle-max', () => {
  if (!mainWindow) return false;
  if (mainWindow.isMaximized()) mainWindow.unmaximize();
  else mainWindow.maximize();
  return mainWindow.isMaximized();
});

// 页面里的「文件 / 视图」菜单
ipcMain.handle('app:action', (_event, action) => {
  if (!mainWindow) return;
  const wc = mainWindow.webContents;
  switch (action) {
    case 'new-session':
      wc.send('ui:new-session');
      break;
    case 'close-session':
      wc.send('ui:close-session');
      break;
    case 'restart-daemon':
      restartDaemon();
      break;
    case 'reload':
      wc.reload();
      break;
    case 'devtools':
      wc.toggleDevTools();
      break;
    case 'zoom-reset':
      wc.setZoomLevel(0);
      break;
    case 'zoom-in':
      wc.setZoomLevel(wc.getZoomLevel() + 0.5);
      break;
    case 'zoom-out':
      wc.setZoomLevel(wc.getZoomLevel() - 0.5);
      break;
    case 'fullscreen':
      mainWindow.setFullScreen(!mainWindow.isFullScreen());
      break;
    case 'quit':
      app.quit();
      break;
    default:
      break;
  }
});

if (!app.requestSingleInstanceLock()) {
  app.quit();
} else {
  app.on('second-instance', () => {
    if (!mainWindow) return;
    if (mainWindow.isMinimized()) mainWindow.restore();
    mainWindow.focus();
  });

  app.whenReady().then(async () => {
    const result = await ensureDaemon();
    if (SMOKE) {
      console.log(JSON.stringify({ ...result, target: `${BASE}/api/v1/health` }));
      app.exit(result.ok ? 0 : 2);
      return;
    }
    buildMenu();
    createWindow(result.ok ? BASE : 'about:blank');
    if (!result.ok) showError(result.error);
  });

  app.on('window-all-closed', () => app.quit());
  app.on('will-quit', () => {
    if (daemon && daemonOwned) {
      try { daemon.kill(); } catch { /* 已退出 */ }
    }
  });
}