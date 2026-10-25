import test from 'node:test';
import assert from 'node:assert/strict';
import { cueAt, formatTime } from '../src/lib/playback.mjs';
const cues = [{start:1,end:1.5},{start:1.5,end:2},{start:3,end:4}];
test('read-along follows word boundaries and leaves pauses unhighlighted', () => {
  for (const [seconds,index] of [[0,-1],[1,0],[1.499,0],[1.5,1],[2,-1],[2.9,-1],[3,2],[4,-1],[100,-1]]) assert.equal(cueAt(cues,seconds),index);
});
test('seeking backward and invalid timestamps are safe', () => {
  assert.equal(cueAt(cues,3.5),2); assert.equal(cueAt(cues,1.2),0);
  for (const seconds of [NaN,Infinity,-1]) assert.equal(cueAt(cues,seconds),-1);
  assert.equal(cueAt([],1),-1);
});
test('media clock handles uninitialized duration', () => {
  assert.equal(formatTime(NaN),'0:00'); assert.equal(formatTime(-1),'0:00'); assert.equal(formatTime(300),'5:00');
});
