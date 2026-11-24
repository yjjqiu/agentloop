// 把 xterm 的 dist 复制到 renderer/vendor，避免打包时要带 node_modules。
const fs = require('node:fs');
const path = require('node:path');

const root = path.join(__dirname, '..');
const out = path.join(root, 'renderer', 'vendor');

const copies = [
  ['node_modules/@xterm/xterm/lib/xterm.js', 'xterm.js'],
  ['node_modules/@xterm/xterm/css/xterm.css', 'xterm.css'],
  ['node_modules/@xterm/addon-fit/lib/addon-fit.js', 'addon-fit.js'],
];

fs.mkdirSync(out, { recursive: true });
for (const [from, to] of copies) {
  const src = path.join(root, from);
  if (!fs.existsSync(src)) {
    console.error(`缺少 ${from}，先 npm install`);
    process.exit(1);
  }
  fs.copyFileSync(src, path.join(out, to));
  console.log(`vendor: ${to}`);
}