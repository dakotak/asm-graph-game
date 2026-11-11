import '@testing-library/jest-dom/vitest'

// Node >= 22 defines its own (file-backed, here unusable) localStorage
// global that shadows jsdom's. Use a simple in-memory Storage in tests.
class MemoryStorage implements Storage {
  private m = new Map<string, string>()
  get length() {
    return this.m.size
  }
  clear() {
    this.m.clear()
  }
  getItem(k: string) {
    return this.m.has(k) ? this.m.get(k)! : null
  }
  key(i: number) {
    return [...this.m.keys()][i] ?? null
  }
  removeItem(k: string) {
    this.m.delete(k)
  }
  setItem(k: string, v: string) {
    this.m.set(k, String(v))
  }
}
for (const name of ['localStorage', 'sessionStorage'] as const) {
  Object.defineProperty(globalThis, name, { value: new MemoryStorage(), configurable: true, writable: true })
  if (typeof window !== 'undefined') Object.defineProperty(window, name, { value: globalThis[name], configurable: true, writable: true })
}
