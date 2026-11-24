const { contextBridge, ipcRenderer } = require('electron');

// 界面由 Go daemon 提供，这里只暴露外壳的原生能力。
contextBridge.exposeInMainWorld('agentdeckShell', {
  pickDir: () => ipcRenderer.invoke('shell:pick-dir'),
  info: () => ipcRenderer.invoke('shell:info'),
  onNewSession: (callback) => {
    const handler = () => callback();
    ipcRenderer.on('ui:new-session', handler);
    return () => ipcRenderer.removeListener('ui:new-session', handler);
  },
  onCloseSession: (callback) => {
    const handler = () => callback();
    ipcRenderer.on('ui:close-session', handler);
    return () => ipcRenderer.removeListener('ui:close-session', handler);
  },
  onDaemonExit: (callback) => {
    const handler = (_event, code) => callback(code);
    ipcRenderer.on('daemon:exit', handler);
    return () => ipcRenderer.removeListener('daemon:exit', handler);
  },
  // 无边框窗口的控制按钮
  win: {
    minimize: () => ipcRenderer.invoke('win:minimize'),
    toggleMax: () => ipcRenderer.invoke('win:toggle-max'),
    close: () => ipcRenderer.invoke('win:close'),
    isMax: () => ipcRenderer.invoke('win:is-max'),
    onMaxChange: (callback) => {
      const handler = (_event, maximized) => callback(maximized);
      ipcRenderer.on('win:maximized', handler);
      return () => ipcRenderer.removeListener('win:maximized', handler);
    },
  },
  // 自定义菜单（文件 / 视图）
  menu: (action) => ipcRenderer.invoke('app:action', action),
});