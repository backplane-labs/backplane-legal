import type { Version } from './gen/proto/legal/console/v1/admin_pb';

export function canonicalLanguage(value: string): string {
  try { return Intl.getCanonicalLocales(value.trim())[0] ?? ''; } catch { return ''; }
}
export function localDate(value: string): string {
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return '';
  return new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
}
export function instant(value: string): string {
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) throw new Error('Enter a valid effective date and time.');
  return date.toISOString();
}
export function positiveInteger(value: string, label: string): bigint {
  if (!value.trim()) return 0n;
  if (!/^[1-9]\d*$/.test(value) || BigInt(value) > 9223372036854775807n) throw new Error(`${label} must be a positive 64-bit integer.`);
  return BigInt(value);
}
export function validateVersion(version: Version, defaultLang: string): string | undefined {
  if (!version.effectiveAt || !Number.isFinite(new Date(version.effectiveAt).getTime())) return 'Enter a valid effective date and time.';
  if (!version.texts.length) return 'Add at least one language.';
  const languages = version.texts.map((text) => canonicalLanguage(text.lang));
  if (languages.some((lang) => !lang) || new Set(languages).size !== languages.length) return 'Each language must be a unique BCP 47 language tag.';
  if (!languages.includes(defaultLang)) return `Include the default language (${defaultLang}).`;
  for (const text of version.texts) {
    if (!text.title.trim()) return `Enter a title for ${text.lang}.`;
    if (!text.body.trim() && !text.url.trim()) return `Enter Markdown or an external URL for ${text.lang}.`;
    if (text.url) {
      try { const url = new URL(text.url); if (!['http:', 'https:'].includes(url.protocol)) return `Use an HTTP or HTTPS URL for ${text.lang}.`; }
      catch { return `Enter a valid external URL for ${text.lang}.`; }
    }
  }
}
export function textEvidence(version: Version | undefined, language: string): string {
  const text = version?.texts.find((text) => text.lang === language);
  return text ? `# ${text.title}\n\n${text.body}\n\nExternal URL: ${text.url}` : '';
}
