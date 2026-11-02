/** A bounded worker pool that stops claiming new work after cancellation or failure. */
export async function concurrentMap<T, R>(
  items: readonly T[],
  concurrency: number,
  run: (item: T, index: number) => Promise<R>,
  signal?: AbortSignal
): Promise<R[]> {
  if (!Number.isInteger(concurrency) || concurrency < 1) throw new Error('Concurrency must be a positive integer')
  const results = new Array<R>(items.length)
  let next = 0
  let failed = false
  let failure: unknown
  async function worker() {
    while (!failed && !signal?.aborted) {
      const index = next++
      if (index >= items.length) return
      try {
        results[index] = await run(items[index], index)
      } catch (error) {
        failed = true
        failure = error
      }
    }
  }
  await Promise.all(Array.from({ length: Math.min(concurrency, items.length) }, () => worker()))
  if (failed) throw failure
  signal?.throwIfAborted()
  return results
}
