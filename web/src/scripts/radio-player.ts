import { cueAt, formatTime } from '../lib/playback.mjs';

export function setupPlayer() {
  const root = document.querySelector<HTMLElement>('[data-radio-player]');
  const audio = root?.querySelector<HTMLAudioElement>('audio');
  if (!root || !audio) return;
  const play = root.querySelector<HTMLButtonElement>('[data-play]')!;
  const label = root.querySelector<HTMLElement>('[data-play-label]')!;
  const seek = root.querySelector<HTMLInputElement>('#radio-seek')!;
  const status = root.querySelector<HTMLElement>('[data-playback-status]')!;
  const pane = root.querySelector<HTMLElement>('.transcript-scroll')!;
  const words = Array.from(root.querySelectorAll<HTMLElement>('[data-cue]'));
  const cues = words.map(word => ({ start: Number(word.dataset.start), end: Number(word.dataset.end) }));
  const reducedMotion = matchMedia('(prefers-reduced-motion: reduce)');
  const key = audio.dataset.storageKey!;
  let active = -1, activeSegment: HTMLElement | null = null, frame = 0, lastSave = 0;

  function savePosition() {
    try { if (audio!.ended) localStorage.removeItem(key); else localStorage.setItem(key, String(audio!.currentTime)); } catch { /* Private browsing can deny storage. */ }
  }
  function playingStatus() {
    status.textContent = words.length ? 'Playing · The transcript follows the narration.' : 'Playing · Read along in the transcript.';
  }
  function revealWord(word: HTMLElement) {
    const bounds = pane.getBoundingClientRect(), target = word.getBoundingClientRect();
    if (target.top < bounds.top + 32 || target.bottom > bounds.bottom - 56) {
      pane.scrollTo({ top: pane.scrollTop + target.top - bounds.top - pane.clientHeight * .35, behavior: reducedMotion.matches ? 'instant' : 'smooth' });
    }
  }
  function update() {
    const duration = audio!.duration;
    if (Number.isFinite(duration) && duration > 0) {
      seek.max = String(duration);
      root!.querySelector('[data-duration]')!.textContent = formatTime(duration);
    }
    seek.value = String(audio!.currentTime);
    seek.setAttribute('aria-valuetext', `${formatTime(audio!.currentTime)} of ${formatTime(duration)}`);
    root!.querySelector('[data-elapsed]')!.textContent = formatTime(audio!.currentTime);
    const next = audio!.ended ? -1 : cueAt(cues, audio!.currentTime);
    if (next !== active) {
      if (active >= 0) words[active].classList.remove('is-current');
      active = next;
      if (active >= 0) {
        const word = words[active];
        word.classList.add('is-current');
        const segment = word.closest<HTMLElement>('[data-segment]')!;
        if (segment !== activeSegment) {
          activeSegment?.classList.remove('is-current');
          activeSegment = segment;
          segment.classList.add('is-current');
          root!.querySelectorAll<HTMLElement>('[data-chapter-link]').forEach(link => {
            if (link.dataset.chapterLink === segment.dataset.segment) link.setAttribute('aria-current', 'true');
            else link.removeAttribute('aria-current');
          });
        }
        if (!audio!.paused) revealWord(word);
      }
    }
  }
  function animate() { update(); if (!audio!.paused && !audio!.ended) frame = requestAnimationFrame(animate); }
  function playbackState() {
    const playing = !audio!.paused && !audio!.ended;
    root!.querySelector<HTMLElement>('[data-play-icon]')!.hidden = playing;
    root!.querySelector<HTMLElement>('[data-pause-icon]')!.hidden = !playing;
    label.textContent = playing ? 'Pause episode' : audio!.ended ? 'Play again' : 'Play episode';
    play.setAttribute('aria-label', label.textContent);
    cancelAnimationFrame(frame);
    if (playing) {
      if (active >= 0) revealWord(words[active]);
      frame = requestAnimationFrame(animate);
    }
  }
  async function start() {
    status.textContent = 'Loading audio…';
    try { await audio!.play(); }
    catch { status.textContent = 'Playback could not start. Try Play again or read the transcript.'; playbackState(); }
  }
  play.addEventListener('click', () => { if (audio.paused) void start(); else audio.pause(); });
  seek.addEventListener('input', () => { audio.currentTime = Number(seek.value); update(); });
  root.querySelectorAll<HTMLButtonElement>('[data-skip]').forEach(button => button.addEventListener('click', () => {
    if (Number.isFinite(audio.duration)) audio.currentTime = Math.max(0, Math.min(audio.duration, audio.currentTime + Number(button.dataset.skip)));
    update();
  }));
  root.querySelector<HTMLSelectElement>('[data-speed]')!.addEventListener('change', event => { audio.playbackRate = Number((event.target as HTMLSelectElement).value); });
  root.querySelectorAll<HTMLButtonElement>('[data-jump]').forEach(button => {
    button.hidden = false;
    button.addEventListener('click', () => { audio.currentTime = Number(button.dataset.jump); update(); void start(); });
  });
  // A chapter selects the matching audio position when cues are available.
  // Without cues its native fragment link still scrolls to the transcript.
  root.querySelectorAll<HTMLAnchorElement>('[data-chapter-link]').forEach(link => link.addEventListener('click', event => {
    const index = words.findIndex(word => word.closest<HTMLElement>('[data-segment]')?.dataset.segment === link.dataset.chapterLink);
    if (index < 0) return;
    event.preventDefault();
    audio.currentTime = cues[index].start;
    update();
    revealWord(words[index]);
  }));
  function restorePosition() {
    try { const saved = Number(localStorage.getItem(key)); if (saved > 0 && saved < audio!.duration - 2) audio!.currentTime = saved; } catch {}
    update();
  }
  if (audio.readyState >= 1) restorePosition();
  else audio.addEventListener('loadedmetadata', restorePosition, { once: true });
  audio.addEventListener('timeupdate', () => { update(); if (Date.now() - lastSave > 5000) { savePosition(); lastSave = Date.now(); } });
  audio.addEventListener('seeked', update);
  audio.addEventListener('play', playbackState);
  audio.addEventListener('playing', playingStatus);
  audio.addEventListener('waiting', () => { status.textContent = 'Buffering audio…'; });
  audio.addEventListener('pause', () => { playbackState(); savePosition(); status.textContent = 'Paused. Pick up whenever you’re ready.'; });
  audio.addEventListener('ended', () => { playbackState(); update(); savePosition(); status.textContent = 'You’re all caught up. Thanks for listening.'; });
  audio.addEventListener('error', () => { playbackState(); status.textContent = 'The audio could not load. Try Play again or read the transcript.'; });
  window.addEventListener('pagehide', () => { savePosition(); cancelAnimationFrame(frame); audio.pause(); });
  root.querySelector<HTMLElement>('[data-custom-controls]')!.hidden = false;
  audio.hidden = true; // Native controls remain available when JavaScript is disabled.
  update();
}
