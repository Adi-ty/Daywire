import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import Ajv from 'ajv';
import addFormats from 'ajv-formats';
import { contentDirectory, root } from '../../scripts/content-path.mjs';
import { latestEdition } from '../../scripts/latest-edition.mjs';
import type { Edition } from './types';

const validator = new Ajv({ allErrors: true });
addFormats(validator);
const validate = validator.compile<Edition>(JSON.parse(readFileSync(join(root, 'schemas/edition.schema.json'), 'utf8')));
const directory = contentDirectory();
export const editions: Edition[] = (existsSync(directory) ? readdirSync(directory) : [])
  .filter(file => file.endsWith('.json'))
  .map(file => {
    const edition = JSON.parse(readFileSync(join(directory, file), 'utf8'));
    if (!validate(edition)) throw new Error(`Invalid edition ${file}: ${validator.errorsText(validate.errors)}`);
    if (file !== `${edition.date}.json`) throw new Error(`Edition date/filename mismatch: ${file}`);
    if (edition.demo && process.env.DAYWIRE_DEMO !== '1') throw new Error('Demo edition found in production content.');
    if (!edition.demo && process.env.DAYWIRE_DEMO === '1') throw new Error('Live edition found in demo content.');
    return edition as Edition;
  })
  .sort((a, b) => b.date.localeCompare(a.date));

export const today = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Kolkata', year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date());
export const latest: Edition | undefined = latestEdition(editions, today, process.env.DAYWIRE_DEMO === '1');
export function dateLabel(date: string) { return new Intl.DateTimeFormat('en-GB', { timeZone: 'UTC', day: 'numeric', month: 'long', year: 'numeric' }).format(new Date(`${date}T12:00:00Z`)); }
export function weekday(date: string) { return new Intl.DateTimeFormat('en-GB', { timeZone: 'UTC', weekday: 'long' }).format(new Date(`${date}T12:00:00Z`)); }
export function href(path: string) { return `${import.meta.env.BASE_URL.replace(/\/$/, '')}/${path.replace(/^\//, '')}`; }
export function readingMinutes(edition: Edition) {
  const text = [edition.content.introduction, ...edition.content.articles.flatMap(a => [a.headline, a.summary, ...a.paragraphs, a.implication]), ...edition.content.lessons.flatMap(l => [l.title, ...l.paragraphs, l.example, l.takeaway])].join(' ');
  return Math.max(1, Math.ceil(text.split(/\s+/).length / 220));
}
