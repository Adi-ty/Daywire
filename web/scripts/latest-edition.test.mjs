import test from 'node:test';
import assert from 'node:assert/strict';
import { latestEdition } from './latest-edition.mjs';

test('front page selects today regardless of archive order, backfills or future dates', () => {
  const editions = ['2026-10-26', '2026-10-23', '2026-10-25', '2026-10-24'].map(date => ({ date }));
  assert.equal(latestEdition(editions, '2026-10-25').date, '2026-10-25');
  assert.equal(latestEdition(editions, '2026-10-24').date, '2026-10-24');
  assert.equal(editions.length, 4); // Dated routes still receive every edition.
});

test('a missing day uses the latest published edition; no content has an empty state', () => {
  assert.equal(latestEdition([{ date: '2026-10-23' }], '2026-10-25').date, '2026-10-23');
  assert.equal(latestEdition([{ date: '2026-10-26' }], '2026-10-25'), undefined);
  assert.equal(latestEdition([], '2026-10-25'), undefined);
  assert.equal(latestEdition([{ date: '2026-10-26' }], '2026-10-25', true).date, '2026-10-26');
});
