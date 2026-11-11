// Small, dependency-free UI primitives (in the spirit of shadcn/ui: plain
// components styled with Tailwind, owned by the app).
import clsx from 'clsx'
import { useEffect, useRef, type ButtonHTMLAttributes, type ReactNode } from 'react'
import { STATUS_STYLES, TONE_CLASSES, type UiStatus } from '../lib/status'

type Variant = 'default' | 'primary' | 'ghost' | 'danger'

export function Button({
  variant = 'default',
  className,
  children,
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant }) {
  return (
    <button
      type="button"
      className={clsx(
        'inline-flex h-7 shrink-0 items-center gap-1.5 rounded-sm border px-2.5 text-[12px] whitespace-nowrap transition-colors select-none',
        'disabled:cursor-not-allowed disabled:opacity-40',
        variant === 'default' && 'border-line bg-panel-2 text-fg hover:border-line-strong hover:bg-raised',
        variant === 'primary' && 'border-accent/60 bg-accent/15 text-fg hover:bg-accent/25',
        variant === 'ghost' && 'border-transparent text-fg-muted hover:bg-panel-2 hover:text-fg',
        variant === 'danger' && 'border-bad/60 bg-bad/15 text-fg hover:bg-bad/25',
        className,
      )}
      {...rest}
    >
      {children}
    </button>
  )
}

export function Kbd({ children }: { children: ReactNode }) {
  return <kbd className="rounded-sm border border-line bg-panel-2 px-1 font-mono text-[10px] text-fg-muted">{children}</kbd>
}

export function StatusBadge({ status, className }: { status: UiStatus; className?: string }) {
  const s = STATUS_STYLES[status]
  return (
    <span
      className={clsx('inline-flex items-center gap-1 rounded-sm border px-1.5 py-px font-mono text-[11px] whitespace-nowrap', TONE_CLASSES[s.tone], className)}
      data-status={status}
    >
      <span aria-hidden>{s.glyph}</span>
      {s.label}
    </span>
  )
}

export function Tabs<T extends string>({
  tabs,
  value,
  onChange,
  right,
}: {
  tabs: { id: T; label: ReactNode }[]
  value: T
  onChange: (t: T) => void
  right?: ReactNode
}) {
  return (
    <div className="flex h-8 shrink-0 items-stretch border-b border-line bg-panel" role="tablist">
      {tabs.map((t) => (
        <button
          key={t.id}
          role="tab"
          aria-selected={value === t.id}
          className={clsx(
            'border-r border-line px-3 font-mono text-[11px] tracking-wide uppercase',
            value === t.id ? 'bg-bg text-fg shadow-[inset_0_-2px_0_var(--c-accent)]' : 'text-fg-muted hover:text-fg',
          )}
          onClick={() => onChange(t.id)}
        >
          {t.label}
        </button>
      ))}
      <div className="ml-auto flex items-center gap-1 pr-2">{right}</div>
    </div>
  )
}

export function Modal({
  title,
  onClose,
  children,
  width = 'max-w-lg',
  footer,
  labelledBy = 'modal-title',
}: {
  title: ReactNode
  onClose: () => void
  children: ReactNode
  width?: string
  footer?: ReactNode
  labelledBy?: string
}) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null
    const root = ref.current
    const first =
      root?.querySelector<HTMLElement>('input, textarea, select') ??
      root?.querySelector<HTMLElement>('[data-testid=modal-confirm]') ??
      root?.querySelector<HTMLElement>('button:not([aria-label=Close]), [tabindex]:not([tabindex="-1"])')
    first?.focus()
    if (first instanceof HTMLInputElement) first.select()
    return () => prev?.focus?.()
  }, [])
  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center bg-black/50 pt-[10vh]"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
      onKeyDown={(e) => {
        if (e.key === 'Escape') {
          e.stopPropagation()
          onClose()
        }
      }}
    >
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-labelledby={labelledBy}
        className={clsx('w-full rounded-sm border border-line-strong bg-panel shadow-2xl', width)}
      >
        <div className="flex h-9 items-center border-b border-line px-3">
          <h2 id={labelledBy} className="label-caps !text-fg">
            {title}
          </h2>
          <button className="ml-auto text-fg-muted hover:text-fg" onClick={onClose} aria-label="Close">
            ✕
          </button>
        </div>
        <div className="max-h-[70vh] overflow-auto">{children}</div>
        {footer && <div className="flex justify-end gap-2 border-t border-line px-3 py-2">{footer}</div>}
      </div>
    </div>
  )
}

/** Renders untrusted text (puzzle descriptions, plugin metadata) safely: no HTML. */
export function PlainText({ text, className }: { text: string; className?: string }) {
  return <p className={clsx('whitespace-pre-wrap', className)}>{text}</p>
}

export function formatValue(v: unknown): string {
  if (v === null || v === undefined) return '—'
  if (typeof v === 'number' || typeof v === 'boolean') return String(v)
  if (typeof v === 'string') return v
  if (Array.isArray(v)) return `[${v.map(formatValue).join(', ')}]`
  return JSON.stringify(v)
}
