const fs = require('node:fs');

fs.mkdirSync('dist', { recursive: true });
fs.closeSync(fs.openSync('dist/.gitkeep', 'a'));
