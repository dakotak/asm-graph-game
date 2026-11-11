// Hardware design UI: the palette of types a puzzle allows (with the
// budget), and the configuration editor for one player-built node. Every
// limit shown comes from the backend (plugin metadata narrowed by the
// puzzle's rules); every change is re-validated and re-costed by the
// backend, whose diagnostics are shown next to the controls.
import clsx from 'clsx'
import { useShallow } from 'zustand/react/shallow'
import type { ConfigField, EffectiveHardware } from '../api/types'
import { estimateCost, nodeCost, totalCost, withConfigDefaults } from '../lib/hardware'
import { controller } from '../services/controller'
import { cellType, findCell, useWorkspace } from '../state/workspace'
import { Button } from './ui'

function Meter({ label, value, max, testid }: { label: string; value: number; max?: number; testid: string }) {
  const over = max !== undefined && max > 0 && value > max
  return (
    <div className="mb-1.5" data-testid={testid}>
      <div className="flex justify-between font-mono text-[11px]">
        <span className="text-fg-muted">{label}</span>
        <span className={over ? 'text-bad' : 'text-fg'}>
          {value}
          {max ? ` / ${max}` : ''}
        </span>
      </div>
      {max ? (
        <div className="mt-0.5 h-1.5 rounded-sm bg-panel-2" aria-hidden>
          <div className={clsx('h-full rounded-sm', over ? 'bg-bad' : 'bg-accent/70')} style={{ width: `${Math.min(100, (value / max) * 100)}%` }} />
        </div>
      ) : null}
    </div>
  )
}

/** Sidebar section: budget usage and the types players may add. */
export function HardwarePalette() {
  const catalog = useWorkspace((s) => s.hardwareCatalog)
  const rules = useWorkspace((s) => s.puzzleDoc?.puzzle.hardware)
  const cells = useWorkspace((s) => s.cells)
  const report = useWorkspace((s) => s.hardwareReport)
  const issues = useWorkspace(useShallow((s) => s.hardwareIssues.filter((d) => d.path === 'hardware' || d.path === undefined || d.path === '')))
  if (!catalog || !rules) return null
  const players = cells.filter((c) => c.player)
  const cost = totalCost(cells, catalog, report)
  return (
    <section className="border-b border-line px-3 py-2" data-testid="hardware-palette">
      <h3 className="label-caps mb-1.5">Hardware</h3>
      <Meter label="cost" value={cost} max={rules.max_cost} testid="budget-cost" />
      <Meter label="nodes" value={players.length} max={rules.max_nodes} testid="budget-nodes" />
      <ul className="mt-2 space-y-1">
        {catalog.map((e) => (
          <PaletteItem key={e.type} e={e} count={players.filter((c) => c.type === e.type).length} />
        ))}
      </ul>
      {issues.length > 0 && (
        <ul className="mt-2 space-y-0.5 text-[11px] text-bad" data-testid="hardware-issues">
          {issues.map((d, i) => (
            <li key={i}>✗ {d.message}</li>
          ))}
        </ul>
      )}
    </section>
  )
}

function PaletteItem({ e, count }: { e: EffectiveHardware; count: number }) {
  const info = useWorkspace((s) => cellType(s.catalog, e.type))
  const base = estimateCost(e.cost, { ports: e.ports.default, programMemory: e.programMemory?.default ?? 0, config: withConfigDefaults(info?.config, {}) })
  const full = e.max > 0 && count >= e.max
  const short = count < e.min
  return (
    <li className="flex items-center gap-2" data-testid={`palette-${e.type}`}>
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-1.5">
          <span className="font-mono text-[12px] text-fg">{e.type}</span>
          <span className="text-[9px] tracking-widest text-fg-muted uppercase">{e.spec.category}</span>
          {e.min > 0 && <span className={clsx('text-[9px] uppercase', short ? 'text-warn' : 'text-ok')}>required</span>}
        </div>
        <div className="truncate text-[10px] text-fg-muted" title={info?.description}>
          ¤{base} default · {count}
          {e.max > 0 ? `/${e.max}` : ''} placed
        </div>
      </div>
      <Button variant="ghost" disabled={full} onClick={() => controller.addNode(e.type)} title={full ? `This puzzle allows at most ${e.max}` : `Add a ${e.type}`}>
        + Add
      </Button>
    </li>
  )
}

/** Configuration editor for one player-built node. */
export function NodeEditor({ id }: { id: string }) {
  const node = useWorkspace((s) => s.solution?.hardware.find((n) => n.id === id))
  const cell = useWorkspace((s) => findCell(s.cells, id))
  const eff = useWorkspace((s) => (node ? s.hardwareCatalog?.find((e) => e.type === node.type) : undefined))
  const info = useWorkspace((s) => (node ? cellType(s.catalog, node.type) : undefined))
  const report = useWorkspace((s) => s.hardwareReport)
  const issues = useWorkspace(
    useShallow((s) => {
      const i = s.solution?.hardware.findIndex((n) => n.id === id) ?? -1
      return i < 0 ? [] : s.hardwareIssues.filter((d) => d.path === `hardware[${i}]` || d.path?.startsWith(`hardware[${i}].`))
    }),
  )
  if (!node || !cell) return null
  if (!eff) {
    return (
      <div className="text-[12px] text-bad" data-testid="node-editor">
        This puzzle does not allow {node.type} hardware.{' '}
        <Button variant="danger" onClick={() => controller.removeNode(id)}>
          Remove {id}
        </Button>
      </div>
    )
  }
  const spec = eff.spec
  const pf = spec.portFunctions
  const cost = nodeCost(cell, eff, report)
  const fieldIssue = (f: string) => issues.filter((d) => d.path?.endsWith(`.${f}`) || d.path?.includes(`.${f}[`)).map((d) => d.message)
  return (
    <div className="space-y-2 text-[12px]" data-testid="node-editor">
      <div className="flex items-baseline gap-2">
        <span className="label-caps">Hardware</span>
        <span className="font-mono text-fg-muted">{node.type}</span>
        <span className="ml-auto font-mono text-accent" data-testid="node-cost">
          ¤{cost}
        </span>
      </div>
      <CostBreakdown eff={eff} ports={cell.portCount} memory={cell.programMemory} config={cell.config} />

      <Field label="Ports" hint={`${eff.ports.min}–${eff.ports.max}`} errors={fieldIssue('port_count')}>
        <Stepper
          value={cell.portCount}
          min={eff.ports.min}
          max={eff.ports.max}
          testid="node-ports"
          onChange={(v) => controller.updateNode(id, { port_count: v, port_functions: node.port_functions ? resizeFunctions(node.port_functions, v, cell.portFunctions) : undefined })}
        />
      </Field>

      {eff.programMemory && (
        <Field label="Program memory" hint={`${eff.programMemory.min}–${eff.programMemory.max} bytes`} errors={fieldIssue('program_memory')}>
          <Stepper
            value={cell.programMemory}
            min={eff.programMemory.min}
            max={eff.programMemory.max}
            step={memoryStep(eff)}
            testid="node-memory"
            onChange={(v) => controller.updateNode(id, { program_memory: v })}
            suffix="bytes"
          />
        </Field>
      )}

      {pf && (
        <Field label="Port functions" hint={pf.rule} errors={[...fieldIssue('port_functions')]}>
          <div className="grid grid-cols-[auto_1fr] items-center gap-x-2 gap-y-1">
            {Array.from({ length: cell.portCount }, (_, i) => (
              <PortFunctionSelect key={i} id={id} index={i} eff={eff} value={cell.portFunctions?.[i] ?? ''} all={cell.portFunctions ?? []} disabled={!pf.configurable} />
            ))}
          </div>
        </Field>
      )}

      {(info?.config ?? []).map((f) => (
        <ConfigInput key={f.name} id={id} field={f} eff={eff} value={node.config?.[f.name]} errors={fieldIssue(`config.${f.name}`)} />
      ))}

      {issues.length > 0 && (
        <ul className="space-y-0.5 text-[11px] text-bad" data-testid="node-issues">
          {issues.map((d, i) => (
            <li key={i}>✗ {d.message.replace(new RegExp(`^${id}: `), '')}</li>
          ))}
        </ul>
      )}
      <div className="pt-1">
        <Button variant="danger" onClick={() => controller.removeNode(id)} title="Remove this node and its connections (Del in the graph)">
          Remove {id}
        </Button>
      </div>
    </div>
  )
}

/** Keeps assigned functions when the port count changes (new ports get defaults). */
function resizeFunctions(current: string[], n: number, resolved?: string[]): string[] {
  return Array.from({ length: n }, (_, i) => current[i] ?? resolved?.[i] ?? current[current.length - 1] ?? '')
}

function memoryStep(eff: EffectiveHardware): number {
  const p = eff.cost.properties?.find((x) => x.property === 'program_memory')
  return p && p.per > 1 ? p.per : 1
}

function CostBreakdown({ eff, ports, memory, config }: { eff: EffectiveHardware; ports: number; memory: number; config?: Record<string, unknown> }) {
  const parts = [`${eff.cost.base} base`, `${ports} × ${eff.cost.perPort} per port`]
  for (const p of eff.cost.properties ?? []) {
    const raw = p.property === 'program_memory' ? memory : config?.[p.property]
    const v = typeof raw === 'number' ? raw : 0
    if (p.cost > 0) parts.push(`⌈${v} / ${p.per}⌉ × ${p.cost} ${p.property.replace('_', ' ')}`)
  }
  return <p className="font-mono text-[10px] text-fg-muted">{parts.join(' + ')}</p>
}

function Field({ label, hint, errors, children }: { label: string; hint?: string; errors?: string[]; children: React.ReactNode }) {
  return (
    <div>
      <div className="mb-0.5 flex items-baseline gap-2">
        <span className="text-fg-muted">{label}</span>
        {hint && <span className="text-[10px] text-fg-faint">{hint}</span>}
      </div>
      {children}
      {errors?.map((e, i) => (
        <p key={i} className="mt-0.5 text-[11px] text-bad">
          {e}
        </p>
      ))}
    </div>
  )
}

function Stepper({
  value,
  min,
  max,
  step = 1,
  onChange,
  testid,
  suffix,
}: {
  value: number
  min: number
  max: number
  step?: number
  onChange: (v: number) => void
  testid: string
  suffix?: string
}) {
  const clamp = (v: number) => Math.max(min, Math.min(max, v))
  return (
    <div className="flex items-center gap-1" data-testid={testid}>
      <Button variant="ghost" disabled={value <= min} onClick={() => onChange(clamp(value - step))} aria-label="decrease">
        −
      </Button>
      <input
        type="number"
        className="w-16 rounded-sm border border-line bg-bg px-1 py-0.5 text-center font-mono text-[12px]"
        value={value}
        min={min}
        max={max}
        onChange={(e) => {
          const v = Number(e.target.value)
          if (Number.isInteger(v) && v >= min && v <= max) onChange(v)
        }}
        aria-label={testid}
      />
      <Button variant="ghost" disabled={value >= max} onClick={() => onChange(clamp(value + step))} aria-label="increase">
        +
      </Button>
      {suffix && <span className="text-[11px] text-fg-muted">{suffix}</span>}
    </div>
  )
}

function PortFunctionSelect({ id, index, eff, value, all, disabled }: { id: string; index: number; eff: EffectiveHardware; value: string; all: string[]; disabled: boolean }) {
  const fns = eff.spec.portFunctions?.functions ?? []
  const allowed = new Set(eff.functions ?? fns.map((f) => f.name))
  const def = fns.find((f) => f.name === value)
  return (
    <>
      <span className="font-mono text-[11px] text-fg-muted">P{index}</span>
      <select
        className="rounded-sm border border-line bg-bg px-1 py-0.5 font-mono text-[12px]"
        value={value}
        disabled={disabled}
        data-testid={`port-function-${index}`}
        title={def?.summary}
        onChange={(e) => {
          const next = [...all]
          next[index] = e.target.value
          controller.updateNode(id, { port_functions: next })
        }}
      >
        {fns
          .filter((f) => allowed.has(f.name) || f.name === value)
          .map((f) => (
            <option key={f.name} value={f.name} title={f.summary}>
              {f.name} ({f.direction === 'in' ? 'neighbours write' : 'neighbours read'})
            </option>
          ))}
      </select>
    </>
  )
}

function ConfigInput({ id, field, eff, value, errors }: { id: string; field: ConfigField; eff: EffectiveHardware; value: unknown; errors: string[] }) {
  const rule = eff.config?.[field.name]
  const min = rule?.min ?? field.min
  const max = rule?.max ?? field.max
  const current = value ?? field.default
  const set = (v: unknown) => {
    const node = useWorkspace.getState().solution?.hardware.find((n) => n.id === id)
    const config = { ...(node?.config ?? {}) }
    if (v === undefined || v === '') delete config[field.name]
    else config[field.name] = v
    controller.updateNode(id, { config: Object.keys(config).length ? config : undefined })
  }
  const hint = [min !== undefined && max !== undefined ? `${min}–${max}` : '', field.description ?? ''].filter(Boolean).join(' · ')
  return (
    <Field label={field.name} hint={hint} errors={errors}>
      {rule?.values?.length ? (
        <select className="rounded-sm border border-line bg-bg px-1 py-0.5 font-mono text-[12px]" value={String(current ?? '')} onChange={(e) => set(rule.values!.find((v) => String(v) === e.target.value))}>
          {rule.values.map((v) => (
            <option key={String(v)}>{String(v)}</option>
          ))}
        </select>
      ) : field.type === 'int' ? (
        <Stepper value={typeof current === 'number' ? current : (min ?? 0)} min={min ?? -2147483648} max={max ?? 2147483647} onChange={set} testid={`node-config-${field.name}`} />
      ) : field.type === 'bool' ? (
        <input type="checkbox" checked={current === true} onChange={(e) => set(e.target.checked)} />
      ) : (
        <input className="w-full rounded-sm border border-line bg-bg px-1 py-0.5 font-mono text-[12px]" value={String(current ?? '')} onChange={(e) => set(e.target.value)} />
      )}
    </Field>
  )
}
