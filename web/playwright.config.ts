import { defineConfig, devices } from '@playwright/test'

// End-to-end tests run the real Go server (`asmgraph web`) serving the
// production build, with a throwaway solutions directory.
const PORT = 7399

export default defineConfig({
  testDir: './e2e',
  timeout: 45_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  workers: 1,
  reporter: [['list']],
  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    viewport: { width: 1440, height: 900 },
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } } }],
  webServer: {
    command: `npm run build && rm -rf .e2e-solutions && mkdir -p .e2e-solutions/double-input && cp ../examples/solutions/double-input.yaml .e2e-solutions/double-input/reference.yaml && cd .. && go run ./cmd/asmgraph web --addr 127.0.0.1:${PORT} --solutions web/.e2e-solutions --static web/dist`,
    url: `http://127.0.0.1:${PORT}/api/health`,
    timeout: 180_000,
    reuseExistingServer: false,
    stdout: 'ignore',
    stderr: 'pipe',
  },
})
