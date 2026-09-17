const { mkdtempSync, readFileSync, rmSync } = require('node:fs');
const { tmpdir } = require('node:os');
const { join, resolve } = require('node:path');
const { spawnSync } = require('node:child_process');

const directory = mkdtempSync(join(tmpdir(), 'agr-vscode-host-'));
try {
  const binary = join(directory, process.platform === 'win32' ? 'gateway.exe' : 'gateway');
  const build = spawnSync('go', ['build', '-o', binary, './vscode-extension/test/gateway.go'], { cwd: resolve(__dirname, '../..'), stdio: 'inherit' });
  if (build.error) throw build.error;
  if (build.status !== 0) throw new Error('Failed to build the gateway test fixture.');
  const result = spawnSync(process.env.VSCODE_EXECUTABLE || 'code', [
    '--new-window', '--wait', '--skip-welcome', '--skip-release-notes', '--disable-workspace-trust',
    '--user-data-dir', join(directory, 'user-data'), '--extensions-dir', join(directory, 'extensions'),
    `--extensionDevelopmentPath=${resolve(__dirname, '..')}`,
    `--extensionTestsPath=${join(__dirname, 'host.cjs')}`
  ], { stdio: 'inherit', timeout: 120_000, env: { ...process.env, AGR_TEST_BINARY: binary, AGR_TEST_DIRECTORY: directory } });
  if (result.error) throw result.error;
  if (result.status === 0) {
    const report = JSON.parse(readFileSync(join(directory, 'test-result.json'), 'utf8'));
    if (report.status !== 'passed') throw new Error('VS Code host tests did not complete.');
    console.log(report.message);
  }
  process.exitCode = result.status ?? 1;
} finally { rmSync(directory, { recursive: true, force: true }); }
