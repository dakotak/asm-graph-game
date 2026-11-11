# asmgraph web IDE

The browser frontend for asmgraph: a graph editor (React Flow), an assembly
editor (Monaco) and a debugger, all driven by the Go engine over
`asmgraph web`.

To develop, run each command in its own terminal from the repository root:

```bash
go run ./cmd/asmgraph web --static ''
```

```bash
npm --prefix web run dev
```

Then open http://127.0.0.1:5173.

Other scripts:

| Script | What it does |
|---|---|
| `npm run build` | Production build into `dist/` (served by `asmgraph web`) |
| `npm test` | Unit and component tests (Vitest) |
| `npm run e2e` | End-to-end tests against the real Go server (Playwright) |
| `npm run typecheck` | Type checking |

Before the first e2e run, install the browser:

```bash
npx playwright install chromium
```

Full documentation: [../docs/web.md](../docs/web.md).
