/** Stable descending score selection. Keep sort semantics for non-finite legacy scores. */
export function topScored<T extends { score: number }>(values: T[], limit: number): T[] {
  const count = Math.min(values.length, Math.max(0, Math.trunc(limit) || 0))
  if (count === 0) return []
  if (values.length <= Math.max(64, count * 2) || values.some(value => !Number.isFinite(value.score))) {
    return values.sort((left, right) => right.score - left.score).slice(0, count)
  }
  // The input position breaks ties exactly as stable Array.sort does.
  const compare = (left: number, right: number) => values[right]!.score - values[left]!.score || left - right
  const heap: number[] = []
  for (let index = 0; index < values.length; index++) {
    if (heap.length < count) {
      let child = heap.length
      heap.push(index)
      while (child > 0) {
        const parent = (child - 1) >>> 1
        if (compare(heap[parent]!, index) >= 0) break
        heap[child] = heap[parent]!
        child = parent
      }
      heap[child] = index
    } else if (compare(index, heap[0]!) < 0) {
      // The root is the worst retained row; restore that invariant after replacing it.
      let parent = 0
      while (parent * 2 + 1 < count) {
        let child = parent * 2 + 1
        if (child + 1 < count && compare(heap[child + 1]!, heap[child]!) > 0) child += 1
        if (compare(index, heap[child]!) >= 0) break
        heap[parent] = heap[child]!
        parent = child
      }
      heap[parent] = index
    }
  }
  return heap.sort(compare).map(index => values[index]!)
}
