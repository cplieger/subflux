# Puppeteer smoke harness

Scaffolding for running ad-hoc browser-driven smoke / repro tests
against a real subflux + Chromium. Not part of the regular test
suite — `*.cjs` test files in this directory are gitignored and
exist only when someone is actively diagnosing something.

## One-time setup

```sh
# from /workspace/subflux
cd internal/server/static-src
npm install --no-save puppeteer typescript

# OS deps for the bundled chromium (Debian trixie)
apt-get install -y \
    libglib2.0-0 libnss3 libnspr4 libdbus-1-3 libatk1.0-0 \
    libatk-bridge2.0-0 libcups2 libgtk-3-0 libgbm1 libxss1 \
    libxcomposite1 libxdamage1 libxrandr2 libxkbcommon0 \
    libpango-1.0-0 libcairo2 fonts-liberation libdrm2 \
    libxshmfence1 libatspi2.0-0 libasound2t64
```

## Build the bundle the browser will load

```sh
# from /workspace/subflux
cd internal/server/static-src
node_modules/.bin/tsc --project tsconfig.json
# Concatenate the CSS bundle (mirrors what the Dockerfile does).
cd ..
> static/style.css
while IFS= read -r line; do
    case "$line" in ''|\#*) continue ;; esac
    cat "static-src/css/${line}" >> static/style.css
done < static-src/css/MANIFEST
cd /workspace/subflux
go build -o /tmp/subflux .
```

## Write a test

```js
// /workspace/subflux/internal/server/static-src/puppeteer/my_test.cjs
const puppeteer = require('../node_modules/puppeteer');
const { spawn } = require('child_process');
const PORT = 19802;

const subflux = spawn('/tmp/subflux', [], {
  env: {
    ...process.env,
    // Set whatever env vars subflux's main.go expects:
    // SUBFLUX_LISTEN_ADDR, config path, etc. Check the
    // server's startup flags / env handling.
  },
  stdio: ['ignore', 'pipe', 'pipe'],
});
subflux.stderr.pipe(process.stderr);

(async () => {
  // wait for subflux on PORT, then puppeteer.launch(),
  // page.goto('http://127.0.0.1:' + PORT + '/'), drive the page...
  subflux.kill('SIGTERM');
})();
```

Run with `node internal/server/static-src/puppeteer/my_test.cjs`.

## Notes

- subflux is a media-server companion — its UI is mostly admin
  forms (provider config, mappings, search). Unit tests in
  `*.test.ts` cover the form / wire / state logic; puppeteer is
  best for end-to-end flows the unit tests can't cover (file
  upload, multi-page navigation, OAuth callback handling,
  long-running operations with progress indicators).
- For tests that need a backing provider (Sonarr/Radarr/etc.),
  point subflux at one of the mock servers in
  `internal/testsupport/` rather than a real provider.
