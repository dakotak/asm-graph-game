// Monaco setup: a local worker (no CDN), themes matching the app, and an
// assembly language per cell type generated from the backend's
// listCellTypes metadata. Nothing about instructions is hard-coded here.
import * as monaco from './monacoEntry'
import EditorWorker from 'monaco-editor/editor/editor.worker?worker'
import type { CellTypeCatalog, CellTypeInfo } from '../api/types'
import { definedLabels, lineContext } from './context'

declare global {
  interface Window {
    MonacoEnvironment?: { getWorker(id: string, label: string): Worker }
  }
}

self.MonacoEnvironment = { getWorker: () => new EditorWorker() }

export { monaco }

monaco.editor.defineTheme('asmgraph-dark', {
  base: 'vs-dark',
  inherit: true,
  rules: [
    { token: 'keyword', foreground: 'e0aa48', fontStyle: 'bold' },
    { token: 'port', foreground: '5cb8c9' },
    { token: 'register', foreground: 'b39ddb' },
    { token: 'label', foreground: '9ccc65' },
    { token: 'number', foreground: 'e8a07a' },
    { token: 'comment', foreground: '5f656d', fontStyle: 'italic' },
    { token: 'identifier', foreground: 'dcd8cf' },
    { token: 'invalid', foreground: 'e8695e', fontStyle: 'underline' },
  ],
  colors: {
    'editor.background': '#14171a',
    'editor.foreground': '#dcd8cf',
    'editorLineNumber.foreground': '#4a5058',
    'editorLineNumber.activeForeground': '#8f949c',
    'editor.lineHighlightBackground': '#1a1e22',
    'editor.lineHighlightBorder': '#00000000',
    'editorGutter.background': '#14171a',
    'editorWidget.background': '#1a1e22',
    'editorWidget.border': '#2a3037',
    'editorSuggestWidget.background': '#1a1e22',
    'editorSuggestWidget.border': '#2a3037',
    'editorSuggestWidget.selectedBackground': '#24323a',
    'editorHoverWidget.background': '#1a1e22',
    'editorHoverWidget.border': '#2a3037',
    'editorCursor.foreground': '#5cb8c9',
    'editor.selectionBackground': '#2b4651',
    'scrollbarSlider.background': '#2a303780',
  },
})

monaco.editor.defineTheme('asmgraph-light', {
  base: 'vs',
  inherit: true,
  rules: [
    { token: 'keyword', foreground: '8a5a00', fontStyle: 'bold' },
    { token: 'port', foreground: '1f7a8c' },
    { token: 'register', foreground: '6a4c9c' },
    { token: 'label', foreground: '3c7a1e' },
    { token: 'number', foreground: 'a3501f' },
    { token: 'comment', foreground: '8a8e95', fontStyle: 'italic' },
    { token: 'invalid', foreground: 'b3362c', fontStyle: 'underline' },
  ],
  colors: {
    'editor.background': '#f6f5f1',
    'editorGutter.background': '#f6f5f1',
    'editor.lineHighlightBackground': '#ecebe6',
    'editor.lineHighlightBorder': '#00000000',
  },
})

export function languageId(type: string): string {
  return `asm-${type}`
}

const registered = new Map<string, monaco.IDisposable[]>()

/** Registers (or re-registers) one assembly language per programmable cell type. */
export function registerLanguages(catalog: CellTypeCatalog) {
  for (const t of catalog.types) {
    if (!t.programmable) continue
    const id = languageId(t.type)
    registered.get(id)?.forEach((d) => d.dispose())
    if (!monaco.languages.getLanguages().some((l) => l.id === id)) monaco.languages.register({ id })
    registered.set(id, [
      monaco.languages.setMonarchTokensProvider(id, monarch(t, catalog)),
      monaco.languages.setLanguageConfiguration(id, {
        comments: { lineComment: catalog.syntax.comments[0] ?? '#' },
        wordPattern: /-?\d+|[A-Za-z_]\w*/,
      }),
      monaco.languages.registerCompletionItemProvider(id, completionProvider(t, catalog)),
      monaco.languages.registerHoverProvider(id, hoverProvider(t, catalog)),
    ])
  }
}

function monarch(t: CellTypeInfo, catalog: CellTypeCatalog): monaco.languages.IMonarchLanguage {
  const comment = catalog.syntax.comments.map((c) => c.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|') || '#'
  return {
    ignoreCase: true,
    defaultToken: 'invalid',
    mnemonics: (t.instructions ?? []).map((i) => i.mnemonic),
    ports: catalog.ports.map((p) => p.name),
    registers: (t.registers ?? []).map((r) => r.name),
    tokenizer: {
      root: [
        [new RegExp(`(${comment}).*$`), 'comment'],
        [/[A-Za-z_]\w*(?=\s*:)/, 'label'],
        [/:/, 'delimiter'],
        [/[-+]?(0[xX][0-9a-fA-F]+|0[bB][01]+|0[oO][0-7]+|\d+)\b/, 'number'],
        [/[A-Za-z_]\w*/, { cases: { '@mnemonics': 'keyword', '@ports': 'port', '@registers': 'register', '@default': 'identifier' } }],
        [/,/, 'delimiter'],
        [/\s+/, 'white'],
      ],
    },
  } as monaco.languages.IMonarchLanguage
}

function completionProvider(t: CellTypeInfo, catalog: CellTypeCatalog): monaco.languages.CompletionItemProvider {
  return {
    triggerCharacters: [' ', ','],
    provideCompletionItems(model, position) {
      const word = model.getWordUntilPosition(position)
      const range = new monaco.Range(position.lineNumber, word.startColumn, position.lineNumber, word.endColumn)
      const ctx = lineContext(model.getLineContent(position.lineNumber), position.column, catalog.syntax.comments)
      if (ctx.inComment) return { suggestions: [] }
      const K = monaco.languages.CompletionItemKind
      if (!ctx.mnemonic) {
        return {
          suggestions: (t.instructions ?? []).map((i) => ({
            label: { label: i.mnemonic, detail: i.operands.length ? ` ${i.operands.join(', ')}` : '', description: i.opcode },
            kind: K.Keyword,
            insertText: i.mnemonic + (i.operands.length ? ' ' : ''),
            documentation: { value: instructionDoc(i, catalog) },
            range,
            command: i.operands.length ? { id: 'editor.action.triggerSuggest', title: '' } : undefined,
          })),
        }
      }
      const ins = t.instructions?.find((i) => i.mnemonic === ctx.mnemonic)
      const slot = ins?.operands[ctx.operandIndex]
      if (!ins || !slot) return { suggestions: [] }
      const items: monaco.languages.CompletionItem[] = []
      if (slot === 'target') {
        for (const l of definedLabels(model.getValue(), catalog.syntax.comments)) {
          items.push({ label: l, kind: K.Reference, insertText: l, detail: 'label', range })
        }
      } else {
        for (const r of t.registers ?? []) {
          items.push({ label: r.name, kind: K.Variable, insertText: r.name, detail: 'register', documentation: r.summary, range })
        }
        for (const p of catalog.ports) {
          items.push({ label: p.name, kind: K.Interface, insertText: p.name, detail: 'port', documentation: p.description, range })
        }
      }
      return { suggestions: items }
    },
  }
}

export function instructionDoc(i: { mnemonic: string; operands: string[]; summary: string; opcode: string }, catalog: CellTypeCatalog): string {
  const sig = `${i.mnemonic}${i.operands.length ? ' ' + i.operands.map((o) => `<${o}>`).join(', ') : ''}`
  const ops = i.operands.map((o) => `- \`<${o}>\`: ${catalog.operandKinds[o] ?? o}`).join('\n')
  return `\`${sig}\` — opcode \`${i.opcode}\`\n\n${i.summary}${ops ? '\n\n' + ops : ''}`
}

function hoverProvider(t: CellTypeInfo, catalog: CellTypeCatalog): monaco.languages.HoverProvider {
  return {
    provideHover(model, position) {
      const w = model.getWordAtPosition(position)
      if (!w) return null
      const u = w.word.toUpperCase()
      const range = new monaco.Range(position.lineNumber, w.startColumn, position.lineNumber, w.endColumn)
      const ins = t.instructions?.find((i) => i.mnemonic === u)
      if (ins) return { range, contents: [{ value: instructionDoc(ins, catalog) }] }
      const port = catalog.ports.find((p) => p.name === u)
      if (port) return { range, contents: [{ value: `**${port.name}** — ${port.description}` }] }
      const reg = t.registers?.find((r) => r.name === u)
      if (reg) return { range, contents: [{ value: `**${reg.name}** (register) — ${reg.summary}` }] }
      return null
    },
  }
}
