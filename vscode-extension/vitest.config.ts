import { defineConfig } from 'vitest/config';
import { resolve } from 'node:path';

export default defineConfig({
  resolve: { alias: { vscode: resolve(__dirname, 'test/vscode.ts') } },
  test: {
    include: ['test/**/*.test.ts'],
    coverage: {
      provider: 'v8', include: ['src/**/*.ts'], reporter: ['text', 'json-summary'],
      thresholds: { lines: 80, functions: 80, statements: 80, branches: 80 }
    }
  }
});
