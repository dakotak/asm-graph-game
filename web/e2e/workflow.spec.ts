import { expect, test, type Locator, type Page } from '@playwright/test'
import { readFile } from 'node:fs/promises'

// These tests drive the real UI against the real Go backend (see
// playwright.config.ts): every size, result and status comes from the engine.

async function openPuzzle(page: Page, id: string) {
  await page.goto('/')
  await expect(page.getByTestId('connection')).toHaveAttribute('data-state', 'open')
  const item = page.getByTestId(`puzzle-${id}`)
  if (!(await item.isVisible())) await page.getByTestId('puzzle-button').click()
  await item.click()
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

test('build, compile, test and save a solution from scratch', async ({ page, request }) => {
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
  const saved = await (await request.get('/api/puzzles/filter-positive/solutions/main')).json()
  expect(saved.yaml).toContain('JGZ SEND')
  expect(saved.yaml).toContain('cell: filter')
})

test('compile errors appear in the editor and the diagnostics panel without losing edits', async ({ page }) => {
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
  // A puzzle no other test saves a solution for, so it starts unconnected.
  await openPuzzle(page, 'distributed-polynomial')
  const edges = page.locator('.react-flow__edge')
  await expect(edges).toHaveCount(0)
  await drag(page, port(page, 'alpha', 'P0'), port(page, 'y', 'P1')) // disabled by the puzzle
  await drag(page, port(page, 'alpha', 'P0'), port(page, 'alpha', 'P1')) // self connection
  await expect(edges).toHaveCount(0)
  await expect(port(page, 'y', 'P1')).toHaveAttribute('data-visual', 'disabled')
  // A valid one still works, and deleting it with the keyboard removes it.
  await drag(page, port(page, 'alpha', 'P1'), port(page, 'y', 'P0'))
  await expect(edges).toHaveCount(1)
  await edges.first().click({ force: true })
  await page.keyboard.press('Delete')
  await expect(edges).toHaveCount(0)
})

test('export and re-import solution YAML', async ({ page }) => {
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
