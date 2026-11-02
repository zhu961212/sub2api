import { describe, expect, it } from 'vitest'
import { concurrentMap } from '../concurrentMap'

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>(done => { resolve = done })
  return { promise, resolve }
}

describe('concurrentMap', () => {
  it('caps in-flight work while returning results in input order', async () => {
    const gates = Array.from({ length: 5 }, deferred)
    const started: number[] = []
    let active = 0
    let peak = 0
    const pending = concurrentMap([0, 1, 2, 3, 4], 2, async item => {
      started.push(item)
      peak = Math.max(peak, ++active)
      await gates[item].promise
      active--
      return item * 2
    })
    expect(started).toEqual([0, 1])
    gates[1].resolve()
    await Promise.resolve(); await Promise.resolve()
    expect(started).toEqual([0, 1, 2])
    gates.forEach(gate => gate.resolve())
    expect(await pending).toEqual([0, 2, 4, 6, 8])
    expect(peak).toBe(2)
  })

  it('does not claim queued work after abort', async () => {
    const gate = deferred()
    const controller = new AbortController()
    const started: number[] = []
    const pending = concurrentMap([1, 2, 3, 4], 2, async item => {
      started.push(item)
      await gate.promise
    }, controller.signal)
    const rejected = expect(pending).rejects.toMatchObject({ name: 'AbortError' })
    controller.abort()
    gate.resolve()
    await rejected
    expect(started).toEqual([1, 2])
  })

  it('joins in-flight workers and stops the queue after failure', async () => {
    const gate = deferred()
    const started: number[] = []
    let joined = false
    const pending = concurrentMap([1, 2, 3], 2, async item => {
      started.push(item)
      if (item === 1) throw new Error('Failed')
      await gate.promise
      joined = true
    })
    const rejected = expect(pending).rejects.toThrow('Failed')
    await Promise.resolve()
    expect(joined).toBe(false)
    gate.resolve()
    await rejected
    expect(joined).toBe(true)
    expect(started).toEqual([1, 2])
  })
})
