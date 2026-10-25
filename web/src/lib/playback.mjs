/** Find the word actually being spoken; pauses have no active word. */
export function cueAt(cues, seconds) {
  if (!Number.isFinite(seconds) || seconds < 0) return -1;
  let low = 0, high = cues.length - 1, candidate = -1;
  while (low <= high) {
    const mid = (low + high) >>> 1;
    if (cues[mid].start <= seconds) { candidate = mid; low = mid + 1; }
    else high = mid - 1;
  }
  return candidate >= 0 && seconds < cues[candidate].end ? candidate : -1;
}
export function formatTime(seconds) {
  const safe = Number.isFinite(seconds) ? Math.max(0, Math.floor(seconds)) : 0;
  return `${Math.floor(safe / 60)}:${String(safe % 60).padStart(2, '0')}`;
}
