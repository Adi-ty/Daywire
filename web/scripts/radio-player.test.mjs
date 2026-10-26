import test from 'node:test';
import assert from 'node:assert/strict';
import ts from 'typescript';
import { readFileSync } from 'node:fs';

// Test the shipped controller with media events. Layout needs a real browser.
class Element extends EventTarget {
  hidden = false; textContent = ''; value = ''; max = ''; dataset = {}; attrs = new Map(); children = new Map(); classes = new Set();
  classList = { add: value => this.classes.add(value), remove: value => this.classes.delete(value) };
  setAttribute(key,value) { this.attrs.set(key,value); }
  removeAttribute(key) { this.attrs.delete(key); }
  querySelector(key) { return this.children.get(key) ?? null; }
  querySelectorAll(key) { return this.children.get(key) ?? []; }
  getBoundingClientRect() { return { top:0,bottom:500,right:500 }; }
  closest() { return this.segment; }
  scrollTo(value) { this.scrolled = value; }
}
class Audio extends Element {
  currentTime = 0; duration = 30; paused = true; ended = false; playbackRate = 1; readyState = 0;
  async play() { if (this.rejectPlayback) throw new Error('blocked'); this.paused=false; this.dispatchEvent(new Event('play')); this.dispatchEvent(new Event('playing')); }
  pause() { this.paused=true; this.dispatchEvent(new Event('pause')); }
}
const scriptURL = new URL('../src/scripts/radio-player.ts',import.meta.url);
let source = readFileSync(scriptURL,'utf8').replace('../lib/playback.mjs',new URL('../lib/playback.mjs',scriptURL).href);
const code = ts.transpileModule(source,{ compilerOptions: { module:ts.ModuleKind.ESNext,target:ts.ScriptTarget.ES2022 } }).outputText;
const { setupPlayer } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

test('player handles playback, seeking, automatic transcript following, failures, and saved progress', async () => {
  const root=new Element(), audio=new Audio(), pane=new Element();
  audio.dataset.storageKey='test-episode'; pane.clientHeight=400;pane.scrollTop=0;
  root.children.set('audio',audio); root.children.set('.transcript-scroll',pane);
  for (const key of ['[data-play]','[data-play-label]','#radio-seek','[data-playback-status]','[data-duration]','[data-elapsed]','[data-play-icon]','[data-pause-icon]','[data-speed]','[data-custom-controls]']) root.children.set(key,new Element());
  const segment = new Element(); segment.dataset.segment='chapter';
  const word = new Element();word.dataset={start:'1',end:'2'};word.segment=segment;
  const chapter = new Element();chapter.dataset.chapterLink='chapter';
  const skip = new Element();skip.dataset.skip='15';
  const jump = new Element();jump.dataset.jump='1';
  root.children.set('[data-cue]',[word]);root.children.set('[data-chapter-link]',[chapter]);root.children.set('[data-skip]',[skip]);root.children.set('[data-jump]',[jump]);
  const memory=new Map([['test-episode','4']]);
  const globals={document:{querySelector:()=>root},window:new EventTarget(),localStorage:{getItem:k=>memory.get(k),setItem:(k,v)=>memory.set(k,v),removeItem:k=>memory.delete(k)},matchMedia:()=>({matches:true}),requestAnimationFrame:()=>1,cancelAnimationFrame:()=>{}};
  const previous=new Map(Object.keys(globals).map(key=>[key,Object.getOwnPropertyDescriptor(globalThis,key)]));
  for(const [key,value] of Object.entries(globals))Object.defineProperty(globalThis,key,{value,configurable:true,writable:true});
  try {
    setupPlayer(); const get=key=>root.querySelector(key);
    assert.equal(audio.hidden,true);assert.equal(get('[data-custom-controls]').hidden,false);
    audio.dispatchEvent(new Event('loadedmetadata'));assert.equal(audio.currentTime,4);
    get('[data-play]').dispatchEvent(new Event('click'));await Promise.resolve();assert.equal(audio.paused,false);assert.equal(get('[data-play-label]').textContent,'Pause episode');
    audio.currentTime=1.5;audio.dispatchEvent(new Event('timeupdate'));assert.ok(word.classes.has('is-current'));
    // Scrolling manually must not disable following; only the transcript pane scrolls.
    pane.dispatchEvent(new Event('wheel'));
    pane.dispatchEvent(new Event('touchmove'));
    assert.match(get('[data-playback-status]').textContent,/follows/);
    word.getBoundingClientRect=()=>({top:700,bottom:724});
    audio.currentTime=3;audio.dispatchEvent(new Event('timeupdate'));
    audio.currentTime=1.5;audio.dispatchEvent(new Event('timeupdate'));
    assert.equal(pane.scrolled.top,560);
    assert.equal(pane.scrolled.behavior,'instant');
    audio.pause();
    chapter.dispatchEvent(new Event('click', {cancelable:true}));
    assert.equal(audio.currentTime,1);
    assert.equal(audio.paused,true);
    get('#radio-seek').value='3';get('#radio-seek').dispatchEvent(new Event('input'));assert.equal(audio.currentTime,3);assert.equal(word.classes.has('is-current'),false);
    skip.dispatchEvent(new Event('click'));assert.equal(audio.currentTime,18);
    get('[data-speed]').value='1.5';get('[data-speed]').dispatchEvent(new Event('change'));assert.equal(audio.playbackRate,1.5);
    audio.pause();assert.equal(memory.get('test-episode'),'18');
    audio.rejectPlayback=true;get('[data-play]').dispatchEvent(new Event('click'));await Promise.resolve();assert.match(get('[data-playback-status]').textContent,/could not start/);
    audio.dispatchEvent(new Event('error'));assert.match(get('[data-playback-status]').textContent,/could not load/);
    audio.ended=true;audio.currentTime=30;audio.dispatchEvent(new Event('ended'));assert.equal(memory.has('test-episode'),false);assert.equal(get('[data-play-label]').textContent,'Play again');
  } finally { for(const [key,descriptor] of previous){if(descriptor)Object.defineProperty(globalThis,key,descriptor);else delete globalThis[key];} }
});
