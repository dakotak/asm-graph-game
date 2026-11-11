// Pure helpers for editor features (no Monaco import, so they are unit-testable).

/** Splits a line into mnemonic + operand tokens before the cursor, ignoring labels and comments. */
export function lineContext(lineText: string, column: number, commentChars: string[]) {
  let text = lineText.slice(0, column - 1)
  for (const c of commentChars) {
    const i = text.indexOf(c)
    if (i >= 0) return { inComment: true, mnemonic: null as string | null, operandIndex: -1 }
  }
  text = text.replace(/^(\s*[A-Za-z_]\w*\s*:)+/, '')
  const m = /^\s*([A-Za-z_]\w*)(\s+)?(.*)$/.exec(text)
  if (!m || m[2] === undefined) return { inComment: false, mnemonic: null as string | null, operandIndex: -1 }
  const rest = m[3]
  // Operands are separated by commas and/or whitespace.
  const parts = rest.split(/\s*,\s*|\s+/)
  return { inComment: false, mnemonic: m[1].toUpperCase(), operandIndex: Math.max(0, parts.length - 1) }
}

/** Labels defined in a program (for target completion). */
export function definedLabels(source: string, commentChars: string[]): string[] {
  const labels = new Set<string>()
  for (let line of source.split('\n')) {
    for (const c of commentChars) {
      const i = line.indexOf(c)
      if (i >= 0) line = line.slice(0, i)
    }
    const re = /([A-Za-z_]\w*)\s*:/g
    let m: RegExpExecArray | null
    const prefix = /^(\s*[A-Za-z_]\w*\s*:)+/.exec(line)?.[0] ?? ''
    while ((m = re.exec(prefix))) labels.add(m[1])
  }
  return [...labels]
}

