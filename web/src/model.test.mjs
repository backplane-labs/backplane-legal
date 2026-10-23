import test from 'node:test';
import assert from 'node:assert/strict';
import { canonicalLanguage, instant, localDate, positiveInteger, textEvidence, validateVersion } from './model.ts';

const version = () => ({ document: 'privacy', number: 1n, effectiveAt: '2026-10-23T12:00:00Z', status: 'draft', changeNote: '', texts: [{ lang: 'en', title: 'Privacy', body: 'Our policy', url: '', contentHash: '' }] });
test('canonical BCP 47 tags reject invalid languages', () => {
  assert.equal(canonicalLanguage(' pt-br '), 'pt-BR');
  assert.equal(canonicalLanguage('en_US'), '');
  assert.equal(canonicalLanguage(''), '');
});
test('sequence and version filters accept only positive int64 or an empty bound', () => {
  assert.equal(positiveInteger('', 'Version'), 0n);
  assert.equal(positiveInteger('9223372036854775807', 'Version'), 9223372036854775807n);
  for (const value of ['0', '-1', '1.5', '1e2', '9223372036854775808']) assert.throws(() => positiveInteger(value, 'Version'));
});
test('draft validation requires default text, title, and body or safe external URL', () => {
  const draft = version();
  assert.equal(validateVersion(draft, 'en'), undefined);
  assert.match(validateVersion(draft, 'fr'), /default language/);
  draft.texts[0].title = '';
  assert.match(validateVersion(draft, 'en'), /title/);
  draft.texts[0].title = 'Privacy'; draft.texts[0].body = '';
  assert.match(validateVersion(draft, 'en'), /Markdown or an external URL/);
  draft.texts[0].url = 'javascript:alert(1)';
  assert.match(validateVersion(draft, 'en'), /HTTP or HTTPS/);
  draft.texts[0].url = 'https://example.org/privacy';
  assert.equal(validateVersion(draft, 'en'), undefined);
});
test('duplicate canonical language variants and invalid dates are rejected', () => {
  const draft = version(); draft.texts.push({ ...draft.texts[0], lang: 'EN' });
  assert.match(validateVersion(draft, 'en'), /unique BCP 47/);
  draft.texts.pop(); draft.effectiveAt = 'invalid';
  assert.match(validateVersion(draft, 'en'), /effective date/);
});
test('diff covers title, Markdown and URL, including a removed language', () => {
  assert.equal(textEvidence(version(), 'en'), '# Privacy\n\nOur policy\n\nExternal URL: ');
  assert.equal(textEvidence(version(), 'fr'), '');
  assert.equal(textEvidence(undefined, 'en'), '');
});
test('local date conversions preserve the selected instant to minute precision', () => {
  const date = '2026-10-23T12:30:00Z';
  assert.equal(instant(localDate(date)), new Date(date).toISOString());
  assert.equal(localDate('bad'), '');
  assert.throws(() => instant('bad'));
});
