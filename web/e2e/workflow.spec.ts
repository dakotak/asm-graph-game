import { expect, test, type Locator, type Page } from '@playwright/test'
import { readFile } from 'node:fs/promises'

// These tests drive the real UI against the real Go backend (see
// playwright.config.ts): every size, result and status comes from the engine.

/** Saves a solution in browser storage before the app starts. */
async function seedSolution(page: Page, puzzle: string, name: string, file: string) {
  const yaml = await readFile(new URL(`../../examples/solutions/${file}`, import.meta.url), 'utf8')
  await page.addInitScript(
    ([key, name, yaml]) => localStorage.setItem(key, JSON.stringify({ [name]: { yaml, modified: '2026-01-01T00:00:00Z' } })),
    [`asmgraph.solutions.${puzzle}`, name, yaml],
  )
}

/** Reads a JSON value from the app's browser storage. */
async function stored(page: Page, key: string) {
  return page.evaluate((k) => JSON.parse(localStorage.getItem(k) ?? 'null'), key)
}

async function openPuzzle(page: Page, id: string) {
  await page.goto('/')
  await expect(page.getByTestId('connection')).toHaveAttribute('data-state', 'open')
  // A fresh start opens the puzzle browser once the engine has summarized
  // the library; otherwise the last puzzle reopens. Wait for one of the two
  // before deciding whether to open the browser, or a click could land on
  // the toolbar just as the dialog covers it.
  const dialog = page.getByRole('dialog', { name: 'Puzzles' })
  await expect(dialog.or(page.getByTestId('puzzle-sidebar'))).toBeVisible()
  if (!(await dialog.isVisible())) await page.getByTestId('puzzle-button').click()
  await page.getByTestId(`puzzle-${id}`).click()
  await expect(page.getByTestId('puzzle-sidebar')).toBeVisible()
  await expect(page.locator('.react-flow__node').first()).toBeVisible()
  await waitForStableViewport(page)
}

/** The graph animates fit-to-view after loading; wait until it settles before dragging. */
async function waitForStableViewport(page: Page) {
  const viewport = page.locator('.react-flow__viewport')
  let last = ''
  await expect
    .poll(async () => {
      const t = (await viewport.getAttribute('style')) ?? ''
      const stable = t === last
      last = t
      return stable
    }, { intervals: [150] })
    .toBe(true)
}

/** Types a program line by line, dismissing completion so Enter inserts a newline. */
async function typeProgram(page: Page, lines: string[]) {
  await page.locator('.monaco-editor .view-lines').click()
  for (const line of lines) {
    await page.keyboard.type(line)
    await page.keyboard.press('Escape')
    await page.keyboard.press('Enter')
  }
}

async function drag(page: Page, from: Locator, to: Locator) {
  const a = (await from.boundingBox())!
  const b = (await to.boundingBox())!
  await page.mouse.move(a.x + a.width / 2, a.y + a.height / 2)
  await page.mouse.down()
  await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2, { steps: 12 })
  await page.mouse.up()
}

const port = (page: Page, cell: string, p: string) => page.locator(`[data-testid="cell-${cell}"] [data-port="${p}"]`)

test('build, compile, test and save a solution from scratch', async ({ page }) => {
  await openPuzzle(page, 'filter-positive')
  await expect(page.locator('.react-flow__node')).toHaveCount(3)

  await typeProgram(page, ['TOP:  MOV P2, ACC', 'JGZ SEND', 'JMP TOP', 'SEND: MOV ACC, P3'])
  // Exact size from the Go assembler, not an estimate.
  await expect(page.getByTestId('memory-value')).toHaveText('10 / 16 bytes')

  await drag(page, port(page, 'in', 'P0'), port(page, 'filter', 'P2'))
  await drag(page, port(page, 'filter', 'P3'), port(page, 'out', 'P0'))
  await expect(page.locator('.react-flow__edge')).toHaveCount(2)
  await expect(port(page, 'filter', 'P2')).toHaveAttribute('data-visual', 'connected')

  await page.getByTestId('run-tests').click()
  await expect(page.getByTestId('tests-summary')).toHaveText('3/3 passed')
  await expect(page.getByTestId('test-row-extremes')).toContainText('Passed')

  await expect(page.getByTestId('dirty-indicator')).toBeVisible()
  await page.keyboard.press('ControlOrMeta+s')
  await expect(page.getByTestId('modal-input')).toHaveValue('main')
  await page.getByTestId('modal-confirm').click()
  await expect(page.getByTestId('dirty-indicator')).toHaveCount(0)
  const saved = await stored(page, 'asmgraph.solutions.filter-positive')
  expect(saved.main.yaml).toContain('JGZ SEND')
  expect(saved.main.yaml).toContain('cell: filter')
})

test('compile errors appear in the editor and the diagnostics panel without losing edits', async ({ page }) => {
  await seedSolution(page, 'double-input', 'reference', 'double-input.yaml')
  await openPuzzle(page, 'double-input') // loads the seeded "reference" solution
  await expect(page.getByTestId('memory-value')).toHaveText('10 / 32 bytes')
  await page.locator('.monaco-editor .view-lines').click()
  await page.keyboard.press('ControlOrMeta+End')
  await page.keyboard.type('MUL 2')
  await page.keyboard.press('Escape')
  await expect(page.locator('.monaco-editor .squiggly-error')).toHaveCount(1)
  await page.getByTestId('compile').click()
  await page.getByRole('tab', { name: /Diagnostics/ }).click()
  await expect(page.getByTestId('diagnostics-panel')).toContainText('unknown instruction MUL for cell type standard')
  await expect(page.locator('.monaco-editor .view-lines')).toContainText('MUL 2')
})

test('step, run to completion and reset follow the engine', async ({ page }) => {
  await seedSolution(page, 'double-input', 'reference', 'double-input.yaml')
  await openPuzzle(page, 'double-input')
  await page.getByTestId('step').click()
  await expect(page.getByTestId('tick')).toHaveText('00001')
  await expect(page.getByTestId('cell-inspector')).toContainText('ADD ACC') // pc 1 after one tick
  await expect(page.getByTestId('cell-state')).toContainText('ACC')
  await page.keyboard.press('F10')
  await expect(page.getByTestId('tick')).toHaveText('00002')
  await expect(page.locator('.monaco-editor .asm-active-line')).toHaveCount(1)

  await page.getByLabel('Execution speed').selectOption('max')
  await page.getByTestId('run').click()
  await expect(page.getByTestId('sim-status')).toContainText('Passed')
  await expect(page.getByTestId('tick')).toHaveText('00015')
  await expect(page.locator('[data-testid="cell-output"]')).toContainText('4/4')

  await page.getByTestId('reset').click()
  await expect(page.getByTestId('tick')).toHaveText('00000')
  await expect(page.getByTestId('sim-status')).toContainText('Ready')
})

test('invalid connections are rejected in the graph', async ({ page }) => {
  await openPuzzle(page, 'double-input')
  await expect(page.getByTestId('fixed-hardware')).toBeVisible() // fixed topology: nothing to add

  // A puzzle no other test saves a solution for, so it starts unconnected.
  await openPuzzle(page, 'distributed-polynomial')
  const edges = page.locator('.react-flow__edge')
  await expect(edges).toHaveCount(0)
  // Ports the puzzle disables are not rendered: I/O cells show one port.
  await expect(page.getByTestId('cell-y').locator('[data-port]')).toHaveCount(1)
  await expect(page.getByTestId('cell-x').locator('[data-port]')).toHaveCount(1)
  await expect(page.getByTestId('cell-alpha').locator('[data-port]')).toHaveCount(4)
  await drag(page, port(page, 'alpha', 'P0'), port(page, 'alpha', 'P1')) // self connection
  await expect(edges).toHaveCount(0)
  // A valid one still works, and deleting it with the keyboard removes it.
  await drag(page, port(page, 'alpha', 'P1'), port(page, 'y', 'P0'))
  await expect(edges).toHaveCount(1)
  await edges.first().click({ force: true })
  await page.keyboard.press('Delete')
  await expect(edges).toHaveCount(0)
})

test('export and re-import solution YAML', async ({ page }) => {
  await seedSolution(page, 'double-input', 'reference', 'double-input.yaml')
  await openPuzzle(page, 'double-input')
  await page.keyboard.press('ControlOrMeta+Shift+p')
  await page.getByPlaceholder('Type a command, cell or puzzle…').fill('export')
  const download = page.waitForEvent('download')
  await page.keyboard.press('Enter')
  const file = await (await download).path()
  const yaml = await readFile(file!, 'utf8')
  expect(yaml).toContain('puzzle: double-input')
  expect(yaml).toContain('MOV P0, ACC')

  await page.keyboard.press('ControlOrMeta+Shift+p')
  await page.getByPlaceholder('Type a command, cell or puzzle…').fill('import')
  const chooser = page.waitForEvent('filechooser')
  await page.keyboard.press('Enter')
  await (await chooser).setFiles({ name: 'x.yaml', mimeType: 'text/yaml', buffer: Buffer.from(yaml.replace('ADD ACC', 'ADD ACC\n    NOP')) })
  await expect(page.locator('.monaco-editor .view-lines')).toContainText('NOP')
  await expect(page.getByTestId('dirty-indicator')).toBeVisible()
})

test('design hardware: add a processor and a passive stack, configure, wire, pass and get scored', async ({ page }) => {
  await openPuzzle(page, 'stack-reverse')
  const palette = page.getByTestId('hardware-palette')
  await expect(palette).toContainText('requires at least 1 stack') // backend constraint, before anything runs
  // Add from the sidebar palette and from the graph toolbar's menu.
  await page.getByTestId('palette-stack').getByRole('button', { name: '+ Add' }).click()
  await page.getByTestId('add-hardware').click()
  await expect(page.getByTestId('add-stack')).toBeDisabled() // the puzzle allows one stack
  await page.getByTestId('add-standard').click()
  await expect(page.getByTestId('cell-stack1')).toBeVisible()
  await expect(page.getByTestId('cell-cpu1')).toBeVisible()
  await expect(page.getByTestId('budget-cost')).toContainText('33 / 50')
  await expect(palette).not.toContainText('requires at least')

  // Configure the stack in the editor pane (it has no code): two ports, capacity 9.
  await page.getByTestId('cell-stack1').click()
  const config = page.getByTestId('hardware-config')
  await config.getByTestId('node-ports').getByRole('button', { name: 'decrease' }).click()
  await config.getByTestId('node-ports').getByRole('button', { name: 'decrease' }).click()
  await expect(page.getByTestId('cell-stack1').locator('[data-port]')).toHaveCount(2)
  await expect(port(page, 'stack1', 'P1')).toHaveAttribute('data-function', 'POP')
  await config.getByTestId('node-config-capacity').getByRole('button', { name: 'increase' }).click()
  await expect(page.getByTestId('cost-stack1')).toHaveText('¤10')

  await page.getByRole('button', { name: 'Fit', exact: true }).click()
  await waitForStableViewport(page)
  const edges = page.locator('.react-flow__edge')
  // An input stream and a POP port both only send: refused.
  await drag(page, port(page, 'in', 'P0'), port(page, 'stack1', 'P1'))
  await expect(edges).toHaveCount(0)
  await drag(page, port(page, 'in', 'P0'), port(page, 'cpu1', 'P0'))
  await drag(page, port(page, 'cpu1', 'P1'), port(page, 'stack1', 'P0'))
  await drag(page, port(page, 'stack1', 'P1'), port(page, 'cpu1', 'P2'))
  await drag(page, port(page, 'cpu1', 'P3'), port(page, 'out', 'P0'))
  await expect(edges).toHaveCount(4)

  await page.getByTestId('cell-cpu1').click()
  await typeProgram(page, ['MOV 0, P1', 'read: MOV P0, ACC', 'JEZ flush', 'MOV ACC, P1', 'JMP read', 'flush: MOV P2, ACC', 'JEZ done', 'MOV ACC, P3', 'JMP flush', 'done: MOV 0, P3'])
  await expect(page.getByTestId('memory-value')).toHaveText('26 / 32 bytes')

  await page.getByTestId('run-tests').click()
  await expect(page.getByTestId('tests-summary')).toHaveText('3/3 passed')
  // The server re-ran the solution and recorded independent personal bests.
  await expect(page.getByTestId('score-cost')).toContainText('¤32')
  await expect(page.getByTestId('score-ticks')).toContainText('92')
  await expect(page.getByTestId('score-bytes')).toContainText('26')
  await expect(page.getByTestId('score-cost')).toContainText('new best')
  const bests = await stored(page, 'asmgraph.bests.stack-reverse')
  expect(bests.bests.cost.score).toEqual({ cost: 32, ticks: 92, bytes: 26 })

  // Removing a node removes its wires too.
  await page.getByTestId('cell-stack1').click()
  await page.getByTestId('hardware-config').getByRole('button', { name: 'Remove stack1' }).click()
  await expect(page.getByTestId('cell-stack1')).toHaveCount(0)
  await expect(edges).toHaveCount(2)
})

test('import a puzzle file into the browser library', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByRole('dialog', { name: 'Puzzles' })).toBeVisible() // a fresh start opens the browser
  const yaml = (await readFile(new URL('../../examples/puzzles/double-input.yaml', import.meta.url), 'utf8')).replace('id: double-input', 'id: my-double').replace('name: Double the Input', 'name: My Double')
  const chooser = page.waitForEvent('filechooser')
  await page.getByTestId('import-puzzle').click()
  await (await chooser).setFiles({ name: 'my-double.yaml', mimeType: 'text/yaml', buffer: Buffer.from(yaml) })
  // Validated by the engine, stored in the browser, and opened.
  await expect(page.getByTestId('puzzle-sidebar')).toContainText('My Double')
  expect(Object.keys(await stored(page, 'asmgraph.puzzles'))).toEqual(['my-double'])
  await page.reload()
  await page.getByTestId('puzzle-button').click()
  await expect(page.getByTestId('puzzle-my-double')).toContainText('imported')
})
