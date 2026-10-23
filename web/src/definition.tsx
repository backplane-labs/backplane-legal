import { useEffect, useRef, useState, type ReactNode, type ComponentProps } from 'react';
import { useParams } from 'react-router-dom';
import { create, clone } from '@bufbuild/protobuf';
import { useClient } from '@backplane-labs/backplane-react';
import { definePlugin, usePluginContext, usePluginNavigate } from '@backplane-labs/backplane-plugin-sdk';
import { ConnectionNotice, usePlatformQuery, usePlatformAction } from '@backplane-labs/backplane-platform-ui';
import { CodeEditor, DiffViewer } from '@backplane-labs/backplane-editors';
import { Badge, Button, Checkbox, ConfirmAction, EmptyState, Field, FieldLabel, Input, PageHeader, Panel, SelectControl, Stack, StatusBadge, Table, TableBody, TableCell, TableHead, TableHeader, TableRow, Textarea } from '@backplane-labs/backplane-ui';
import { AdminServiceClient } from './gen/proto/legal/console/v1/admin_ws_pb';
import { CreateDocumentRequestSchema, DocumentSchema, ExportRequestSchema, FilterSchema, GetVersionsRequestSchema, JournalRequestSchema, ListDocumentsRequestSchema, PreviewRequestSchema, PublishRequestSchema, SaveDraftRequestSchema, TextSchema, VerifyRequestSchema, VersionSchema, type Document, type Version, type Text, type Filter } from './gen/proto/legal/console/v1/admin_pb';
import { canonicalLanguage, instant, localDate, positiveInteger, textEvidence, validateVersion } from './model';
import navigation from './navigation';
import './legal.css';

type Client = InstanceType<typeof AdminServiceClient>;
const errorText = (error: unknown) => error instanceof Error ? error.message : String(error);
function ErrorNotice({ error }: { error: unknown }) { return error == null ? null : <div role="alert" className="legal-error">{errorText(error)}</div>; }
function Actions({ children }: { children: ReactNode }) { return <Stack direction="row" gap={8} style={{ flexWrap: 'wrap', alignItems: 'center' }}>{children}</Stack>; }
function FormField({ label, children }: { label: string; children: ReactNode }) { return <Field><FieldLabel>{label}</FieldLabel>{children}</Field>; }
function Hash({ value }: { value: string }) { return <code className="legal-hash">{value || '—'}</code>; }
function GuardedButton({ dirty, onConfirm, ...props }: Omit<ComponentProps<typeof Button>, 'onClick'> & { dirty: boolean; onConfirm: () => void }) {
  const button = <Button {...props} onClick={dirty ? undefined : onConfirm} />;
  return dirty ? <ConfirmAction trigger={button} title="Discard unsaved changes?" description="Your draft has unsaved edits. Discard them to continue, or cancel and save the draft first." confirmLabel="Discard changes" onConfirm={onConfirm} /> : button;
}
function useRead<T>(key: string, read: (signal: AbortSignal) => Promise<T>) {
  const controller = useRef<AbortController | undefined>(undefined);
  const query = usePlatformQuery(key, (signal) => {
    controller.current = new AbortController();
    return read(AbortSignal.any([signal, controller.current.signal]));
  });
  return { ...query, cancel: () => controller.current?.abort() };
}
function ReadState({ query }: { query: { loading: boolean; error: unknown; cancel: () => void; refresh: () => void } }) {
  return <><ErrorNotice error={query.error} />{query.loading && <Actions><span role="status">Loading…</span><Button variant="outline" size="sm" onClick={query.cancel}>Cancel loading</Button></Actions>}{query.error != null && <Button size="sm" variant="outline" onClick={query.refresh}>Retry</Button>}</>;
}
function ActionState({ action }: { action: { pending: boolean; cancelled: boolean; error: unknown; cancel: () => void } }) {
  return <><ErrorNotice error={action.error} />{action.pending && <Actions><span role="status">Working…</span><Button variant="outline" size="sm" onClick={action.cancel}>Cancel</Button></Actions>}{action.cancelled && <p role="status">Cancelled waiting for the response. The server may have completed the operation; refresh before retrying.</p>}</>;
}
function Frame({ title, description, children, actions, dirty = false }: { title: string; description: string; children: ReactNode; actions?: ReactNode; dirty?: boolean }) {
  const navigate = usePluginNavigate();
  return <Stack gap={16} className="legal-console"><ConnectionNotice /><PageHeader title={title} description={description} actions={actions} /><Actions><GuardedButton dirty={dirty} size="sm" variant="outline" onConfirm={() => navigate('')}>Documents</GuardedButton><GuardedButton dirty={dirty} size="sm" variant="outline" onConfirm={() => navigate('journal')}>Acceptance journal</GuardedButton></Actions>{children}</Stack>;
}
function Documents() {
  const client = useClient(AdminServiceClient), navigate = usePluginNavigate();
  const [audience, setAudience] = useState('');
  const query = useRead(`legal:documents:${audience}`, (signal) => client.listDocuments(create(ListDocumentsRequestSchema, { audience }), { signal }));
  return <Frame title="Legal documents" description="Manage publication and preserve the exact text people accept." actions={<Actions><Button size="sm" variant="outline" disabled={query.loading} onClick={query.refresh}>Refresh</Button><Button size="sm" onClick={() => navigate('new')}>Create document</Button></Actions>}>
    <FormField label="Audience"><SelectControl aria-label="Filter audience" value={audience} onValueChange={setAudience} options={[{ value: '', label: 'All audiences' }, { value: 'all', label: 'All users' }, { value: 'registration', label: 'Registration only' }]} /></FormField>
    <ReadState query={query} /><Panel title="Documents" count={query.value?.documents.length} flush>{query.value && (query.value.documents.length ? <div className="legal-table"><Table><TableHeader><TableRow><TableHead>Document</TableHead><TableHead>Audience</TableHead><TableHead>Policy</TableHead><TableHead>Default language</TableHead><TableHead>Versions</TableHead></TableRow></TableHeader><TableBody>{query.value.documents.map((document) => <TableRow key={document.slug}><TableCell><code>{document.slug}</code></TableCell><TableCell>{document.audience === 'all' ? 'All users' : 'Registration'}</TableCell><TableCell><Badge variant="outline">{document.required ? 'Required' : 'Optional consent'}</Badge></TableCell><TableCell>{document.defaultLang}</TableCell><TableCell><Button size="sm" variant="outline" onClick={() => navigate(`documents/${encodeURIComponent(document.slug)}`)}>Open history</Button></TableCell></TableRow>)}</TableBody></Table></div> : <EmptyState title="No documents in this audience" action={<Button size="sm" onClick={() => navigate('new')}>Create document</Button>} />)}</Panel>
  </Frame>;
}
function NewDocument() {
  const client = useClient(AdminServiceClient), navigate = usePluginNavigate(), action = usePlatformAction<string>();
  const [slug, setSlug] = useState(''), [audience, setAudience] = useState('all'), [required, setRequired] = useState(true), [language, setLanguage] = useState('en');
  const [validation, setValidation] = useState<string>();
  async function submit() {
    const lang = canonicalLanguage(language);
    if (!/^[a-z][a-z0-9-]*$/.test(slug)) { setValidation('Use a slug starting with a lowercase letter, then lowercase letters, digits or hyphens.'); return; }
    if (!lang) { setValidation('Enter a BCP 47 language tag, such as en or pt-BR.'); return; }
    setValidation(undefined);
    const result = await action.run(async (signal) => {
      await client.createDocument(create(CreateDocumentRequestSchema, { document: create(DocumentSchema, { slug, audience, required, defaultLang: lang }) }), { signal });
      return slug;
    });
    if (result) navigate(`documents/${encodeURIComponent(result)}`);
  }
  return <Frame title="Create document" description="The stable slug and acceptance policy identify this document across every version."><Panel><form onSubmit={(event) => { event.preventDefault(); void submit(); }}><Stack gap={16}>
    <fieldset disabled={action.disabled} className="legal-fields"><div className="legal-grid"><FormField label="Stable slug"><Input aria-label="Stable slug" value={slug} placeholder="privacy" onChange={(event) => setSlug(event.target.value)} required pattern="[a-z][a-z0-9-]*" /></FormField><FormField label="Default language"><Input aria-label="Default language" value={language} onChange={(event) => setLanguage(event.target.value)} placeholder="en" required /></FormField><FormField label="Audience"><SelectControl aria-label="Audience" value={audience} onValueChange={setAudience} options={[{ value: 'all', label: 'All users' }, { value: 'registration', label: 'Registration only' }]} /></FormField><FormField label="Acceptance policy"><label className="legal-check"><Checkbox checked={required} onCheckedChange={(value) => setRequired(value === true)} /> Required acceptance</label></FormField></div></fieldset>
    <ErrorNotice error={validation} /><ActionState action={action} /><Actions><Button type="submit" disabled={action.disabled}>Create document</Button><Button type="button" variant="outline" disabled={action.pending} onClick={() => navigate('')}>Back</Button></Actions>
  </Stack></form></Panel></Frame>;
}
function DocumentPage() {
  const { slug = '' } = useParams(), client = useClient(AdminServiceClient);
  const query = useRead(`legal:versions:${slug}`, (signal) => client.getVersions(create(GetVersionsRequestSchema, { document: slug }), { signal }));
  const docs = useRead(`legal:policy:${slug}`, (signal) => client.listDocuments(create(ListDocumentsRequestSchema), { signal }));
  const document = docs.value?.documents.find((item) => item.slug === slug);
  const [selected, setSelected] = useState<string>(), [newDraft, setNewDraft] = useState(false), [epoch, setEpoch] = useState(0);
  const [confirmed, setConfirmed] = useState<Version>(), [editingDirty, setEditingDirty] = useState(false);
  const [before, setBefore] = useState(''), [after, setAfter] = useState(''), [language, setLanguage] = useState('');
  const versions = [...(query.value?.versions ?? [])].sort((a, b) => a.number > b.number ? -1 : a.number < b.number ? 1 : 0);
  const active = (confirmed && String(confirmed.number) === selected ? confirmed : undefined) ?? versions.find((version) => String(version.number) === selected) ?? versions[0];
  const draft = versions.find((version) => version.status === 'draft');
  const current = versions.find((version) => version.status === 'published' && new Date(version.effectiveAt).getTime() <= Date.now());
  const from = versions.find((version) => String(version.number) === before), to = versions.find((version) => String(version.number) === after);
  const languages = [...new Set([...(from?.texts.map((text) => text.lang) ?? []), ...(to?.texts.map((text) => text.lang) ?? [])])];
  const diffLang = languages.includes(language) ? language : languages[0] ?? '';
  const context = usePluginContext();
  const reload = (version?: Version) => { if (version) { setSelected(String(version.number)); setConfirmed(version); } setNewDraft(false); setEpoch((value) => value + 1); query.refresh(); };
  return <Frame dirty={editingDirty} title={slug} description="Publishing freezes every language and its content hash. Corrections require a new version." actions={<GuardedButton dirty={editingDirty} size="sm" variant="outline" disabled={query.loading} onConfirm={() => { setConfirmed(undefined); setEditingDirty(false); setEpoch((value) => value + 1); query.refresh(); }}>Refresh history</GuardedButton>}>
    <ReadState query={query} /><ReadState query={docs} />
    {docs.value && !document && <EmptyState title="Document not found" />}
    {document && query.value && <>
      <Actions><Badge variant="outline">{document.audience === 'all' ? 'All users' : 'Registration'}</Badge><Badge variant="outline">{document.required ? 'Required' : 'Optional consent'}</Badge><span>Default language: {document.defaultLang}</span>{current ? <StatusBadge tone="success">Current: v{String(current.number)}</StatusBadge> : <StatusBadge tone="warning">No effective publication</StatusBadge>}</Actions>
      <Panel title="Version history" count={versions.length} flush><div className="legal-table"><Table><TableHeader><TableRow><TableHead>Version</TableHead><TableHead>Status</TableHead><TableHead>Effective date</TableHead><TableHead>Change note</TableHead><TableHead /></TableRow></TableHeader><TableBody>{versions.map((version) => <TableRow key={String(version.number)}><TableCell>v{String(version.number)}</TableCell><TableCell><Badge variant="outline">{version.status}</Badge>{version.status === 'published' && new Date(version.effectiveAt).getTime() > Date.now() && <span> Scheduled</span>}</TableCell><TableCell>{new Date(version.effectiveAt).toLocaleString()}</TableCell><TableCell className="legal-wrap">{version.changeNote || '—'}</TableCell><TableCell><GuardedButton dirty={editingDirty} size="sm" variant={active?.number === version.number && !newDraft ? 'default' : 'outline'} onConfirm={() => { setSelected(String(version.number)); setConfirmed(undefined); setNewDraft(false); setEpoch((value) => value + 1); }}>Open</GuardedButton></TableCell></TableRow>)}</TableBody></Table></div>{!versions.length && <EmptyState title="No versions yet" />}</Panel>
      <Actions><GuardedButton dirty={editingDirty} size="sm" disabled={newDraft || !!draft} onConfirm={() => { setNewDraft(true); setEpoch((value) => value + 1); }}>Create draft{active ? ` from v${String(active.number)}` : ''}</GuardedButton>{draft && <span>Open v{String(draft.number)} to edit the existing draft.</span>}</Actions>
      {(active || newDraft || !versions.length) && <VersionEditor key={`${newDraft || !versions.length ? 'new' : active?.number}:${epoch}`} client={client} document={document} source={active} fresh={newDraft || !versions.length} onSaved={reload} onDirty={setEditingDirty} />}
      {versions.length >= 2 && <Panel title="Compare published evidence"><Stack gap={12}><div className="legal-grid"><FormField label="Before version"><SelectControl aria-label="Before version" value={before} onValueChange={setBefore} options={[{ value: '', label: 'Select version' }, ...versions.map((version) => ({ value: String(version.number), label: `v${String(version.number)} (${version.status})` }))]} /></FormField><FormField label="After version"><SelectControl aria-label="After version" value={after} onValueChange={setAfter} options={[{ value: '', label: 'Select version' }, ...versions.map((version) => ({ value: String(version.number), label: `v${String(version.number)} (${version.status})` }))]} /></FormField>{languages.length > 0 && <FormField label="Comparison language"><SelectControl aria-label="Comparison language" value={diffLang} onValueChange={setLanguage} options={languages.map((lang) => ({ value: lang, label: lang }))} /></FormField>}</div>{from && to && <><p>Title, Markdown and external URL for {diffLang}. A missing language is shown as empty text.</p><DiffViewer key={`${before}:${after}:${diffLang}`} label={`v${before} to v${after}, ${diffLang}`} before={textEvidence(from, diffLang)} after={textEvidence(to, diffLang)} mode={context.mode} language="text" height={320} /></>}</Stack></Panel>}
    </>}
  </Frame>;
}

function VersionEditor({ client, document, source, fresh, onSaved, onDirty }: { client: Client; document: Document; source?: Version; fresh: boolean; onSaved: (version?: Version) => void; onDirty: (dirty: boolean) => void }) {
  const mode = usePluginContext().mode;
  const [version, setVersion] = useState<Version>(() => fresh ? create(VersionSchema, { document: document.slug, number: 0n, status: 'draft', effectiveAt: new Date().toISOString(), texts: source ? source.texts.map((text) => create(TextSchema, { lang: text.lang, title: text.title, body: text.body, url: text.url })) : [create(TextSchema, { lang: document.defaultLang })] }) : clone(VersionSchema, source!));
  const [date, setDate] = useState(localDate(version.effectiveAt)), [language, setLanguage] = useState(version.texts[0]?.lang ?? document.defaultLang), [adding, setAdding] = useState('');
  const [validation, setValidation] = useState<string>(), [dirty, setDirty] = useState(fresh), [notice, setNotice] = useState('');
  const action = usePlatformAction<Version>();
  const preview = usePlatformAction<{ html: string; body: string; lang: string }>();
  const readOnly = version.status === 'published', disabled = readOnly || action.disabled;
  useEffect(() => { onDirty(dirty); }, [dirty, onDirty]);
  const text = version.texts.find((text) => text.lang === language) ?? version.texts[0];
  useEffect(() => {
    if (!dirty) return;
    const warn = (event: BeforeUnloadEvent) => { event.preventDefault(); };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [dirty]);
  const change = (update: Partial<Version>) => { setVersion((current) => ({ ...current, ...update })); setDirty(true); setNotice(''); };
  const changeText = (update: Partial<Text>) => { if (text) change({ texts: version.texts.map((item) => item.lang === text.lang ? { ...item, ...update } : item) }); };
  const invalid = validateVersion({ ...version, effectiveAt: date }, document.defaultLang);
  async function save() {
    try {
      const candidate = { ...version, effectiveAt: instant(date) };
      const error = validateVersion(candidate, document.defaultLang);
      if (error) { setValidation(error); return; }
      setValidation(undefined);
      const result = await action.run(async (signal) => {
        const response = await client.saveDraft(create(SaveDraftRequestSchema, { version: candidate }), { signal });
        if (!response.version) throw new Error('The server did not return the saved draft. Refresh history before retrying.');
        return response.version;
      });
      if (result) { setDirty(false); onSaved(result); }
    } catch (error) { setValidation(errorText(error)); }
  }
  function addLanguage() {
    const lang = canonicalLanguage(adding);
    if (!lang) { setValidation('Enter a BCP 47 language tag, such as en or pt-BR.'); return; }
    if (version.texts.some((item) => canonicalLanguage(item.lang) === lang)) { setValidation('This language already exists.'); return; }
    change({ texts: [...version.texts, create(TextSchema, { lang })] }); setLanguage(lang); setAdding(''); setValidation(undefined);
  }
  const shownPreview = preview.value?.lang === text?.lang && preview.value?.body === text?.body ? preview.value : undefined;
  return <Panel title={fresh ? 'New draft' : `Version ${String(version.number)}`}><Stack gap={16}>
    {readOnly && <StatusBadge tone="success">Published: immutable evidence</StatusBadge>}
    {notice && <p role="status">{notice}</p>}
    <fieldset disabled={disabled} className="legal-fields"><div className="legal-grid"><FormField label="Effective date and time (your local time)"><Input type="datetime-local" aria-label="Effective date and time" value={date} onChange={(event) => { setDate(event.target.value); setDirty(true); }} required /></FormField><FormField label="Change note"><Textarea aria-label="Change note" value={version.changeNote} onChange={(event) => change({ changeNote: event.target.value })} placeholder="Explain the changes people are being asked to accept" /></FormField></div></fieldset>
    <div role="tablist" aria-label="Document languages" className="legal-tabs" onKeyDown={(event) => {
      if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key) || action.pending) return;
      event.preventDefault();
      const index = version.texts.findIndex((item) => item.lang === text?.lang);
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? version.texts.length - 1 : (index + (event.key === 'ArrowRight' ? 1 : -1) + version.texts.length) % version.texts.length;
      setLanguage(version.texts[next].lang);
      event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="tab"]')[next]?.focus();
    }}>{version.texts.map((item) => <Button key={item.lang} role="tab" tabIndex={text?.lang === item.lang ? 0 : -1} aria-selected={text?.lang === item.lang} aria-controls="legal-language-panel" id={`legal-language-${item.lang}`} size="sm" variant={text?.lang === item.lang ? 'default' : 'outline'} onClick={() => setLanguage(item.lang)} disabled={action.pending}>{item.lang}</Button>)}</div>
    {!readOnly && <Actions><Input aria-label="New language tag" value={adding} onChange={(event) => setAdding(event.target.value)} placeholder="Language tag, e.g. fr" disabled={disabled} style={{ maxWidth: 220 }} /><Button variant="outline" size="sm" onClick={addLanguage} disabled={disabled || !adding.trim()}>Add language</Button></Actions>}
    {text && <div id="legal-language-panel" role="tabpanel" aria-labelledby={`legal-language-${text.lang}`}><Stack gap={12}>
      <fieldset disabled={disabled} className="legal-fields"><div className="legal-grid"><FormField label={`Title (${text.lang})`}><Input aria-label={`Title (${text.lang})`} value={text.title} onChange={(event) => changeText({ title: event.target.value })} required /></FormField><FormField label="External URL (optional)"><Input type="url" aria-label={`External URL (${text.lang})`} value={text.url} onChange={(event) => changeText({ url: event.target.value })} placeholder="https://…" /></FormField></div></fieldset>
      <div className="legal-editor-grid"><div className="legal-editor"><CodeEditor key={text.lang} label={`Markdown (${text.lang})`} language="text" value={text.body} onChange={(body) => changeText({ body })} mode={mode} readOnly={readOnly} disabled={action.pending} height={320} complete={({ position }) => Promise.resolve({ from: position, options: [{ label: 'Heading', type: 'keyword', apply: '## Heading\n' }, { label: 'Link', type: 'keyword', apply: '[Link text](https://example.org)' }, { label: 'List', type: 'keyword', apply: '- Item\n' }, { label: 'Emphasis', type: 'keyword', apply: '**Important text**' }] })} /></div>
      <Stack gap={8}><Actions><Button variant="outline" size="sm" disabled={preview.disabled || action.pending} onClick={() => void preview.run(async (signal) => { const response = await client.preview(create(PreviewRequestSchema, { markdown: text.body }), { signal }); return { html: response.html, body: text.body, lang: text.lang }; })}>Render preview</Button><span className="legal-muted">Sanitized by the server</span></Actions><ActionState action={preview} />{shownPreview ? <iframe title={`Sanitized preview (${text.lang})`} sandbox="" referrerPolicy="no-referrer" className="legal-preview" srcDoc={`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src https: data:"><style>body{font:16px/1.6 system-ui,sans-serif;padding:16px;overflow-wrap:anywhere;color:#17202b;background:#fff}pre{white-space:pre-wrap}img{max-width:100%}table{display:block;overflow:auto}</style></head><body>${shownPreview.html}</body></html>`} /> : <p className="legal-muted">Render the current Markdown to preview it. Edits invalidate the previous preview.</p>}</Stack></div>
      {readOnly && <FormField label={`Content hash (${text.lang}, SHA-256)`}><Hash value={text.contentHash} /></FormField>}
      {!readOnly && text.lang !== document.defaultLang && <ConfirmAction trigger={<Button size="sm" variant="outline" disabled={disabled}>Remove {text.lang}</Button>} title={`Remove ${text.lang}?`} description="This removes the language from this draft only. Save the draft to persist the removal." onConfirm={() => { change({ texts: version.texts.filter((item) => item.lang !== text.lang) }); setLanguage(document.defaultLang); }} />}
    </Stack></div>}
    <ErrorNotice error={validation} /><ActionState action={action} />
    {!readOnly && <><Actions><Button disabled={action.disabled || !dirty} onClick={() => void save()}>Save draft</Button><ConfirmAction disabled={action.disabled || fresh || dirty || !!invalid} trigger={<Button variant="outline" disabled={action.disabled || fresh || dirty || !!invalid}>Publish version</Button>} title={`Publish v${String(version.number)}?`} description={`All ${version.texts.length} language texts will be frozen permanently, including their title, Markdown and URL. Effective ${version.effectiveAt}. Corrections require a new version.`} confirmLabel="Publish permanently" onConfirm={async (signal) => {
      const response = await client.publish(create(PublishRequestSchema, { document: document.slug, number: version.number }), { signal });
      if (!response.version) throw new Error('The server did not return the published version. Refresh history to confirm the outcome.');
      setVersion(response.version); setDirty(false); setNotice('Published. Each language now has an immutable content hash.'); onSaved(response.version);
    }} />{dirty && <span className="legal-muted">Save changes before publishing.</span>}</Actions>{invalid && <p className="legal-muted">{invalid}</p>}</>}
  </Stack></Panel>;
}

type FilterFields = { subject: string; document: string; version: string; from: string; to: string };
const emptyFilters: FilterFields = { subject: '', document: '', version: '', from: '', to: '' };
function Journal() {
  const client = useClient(AdminServiceClient);
  const [fields, setFields] = useState<FilterFields>(emptyFilters), [filter, setFilter] = useState<Filter>(() => create(FilterSchema)), [validation, setValidation] = useState<string>();
  const [rangeFrom, setRangeFrom] = useState(''), [rangeTo, setRangeTo] = useState(''), [verifyValidation, setVerifyValidation] = useState<string>();
  const query = useRead(`legal:journal:${JSON.stringify(filter, (_, value) => typeof value === 'bigint' ? String(value) : value)}`, (signal) => client.journal(create(JournalRequestSchema, { filter }), { signal }));
  const exporting = usePlatformAction<string>(), verifying = usePlatformAction<{ ok: boolean; checked: bigint; brokenSeq: bigint; from: bigint; to: bigint }>();
  const apply = () => {
    try {
      const from = fields.from ? instant(fields.from) : '', to = fields.to ? instant(fields.to) : '';
      if (from && to && new Date(from) > new Date(to)) throw new Error('The start time must not be after the end time.');
      setFilter(create(FilterSchema, { subject: fields.subject, document: fields.document, version: positiveInteger(fields.version, 'Version'), from, to })); setValidation(undefined);
    } catch (error) { setValidation(errorText(error)); }
  };
  async function download(format: 'csv' | 'json') {
    await exporting.run(async (signal) => {
      const response = await client.export(create(ExportRequestSchema, { filter, format }), { signal });
      const blob = new Blob([new Uint8Array(response.data)], { type: response.contentType || (format === 'csv' ? 'text/csv' : 'application/json') });
      const url = URL.createObjectURL(blob), anchor = window.document.createElement('a');
      anchor.href = url; anchor.download = response.filename.replace(/[\\/]/g, '_') || `legal-journal.${format}`;
      window.document.body.append(anchor); anchor.click(); anchor.remove(); window.setTimeout(() => URL.revokeObjectURL(url), 1000);
      return `Downloaded ${anchor.download}`;
    });
  }
  async function verify() {
    try {
      const from = positiveInteger(rangeFrom, 'First sequence'), to = positiveInteger(rangeTo, 'Last sequence');
      if (from && to && from > to) throw new Error('The first sequence must not exceed the last sequence.');
      setVerifyValidation(undefined);
      await verifying.run(async (signal) => ({ ...await client.verify(create(VerifyRequestSchema, { from, to }), { signal }), from, to }));
    } catch (error) { setVerifyValidation(errorText(error)); }
  }
  return <Frame title="Acceptance journal" description="Append-only acceptance and withdrawal evidence, with exact text hashes and a verifiable chain." actions={<Button variant="outline" size="sm" disabled={query.loading} onClick={query.refresh}>Refresh journal</Button>}>
    <Panel title="Filter evidence"><form onSubmit={(event) => { event.preventDefault(); apply(); }}><Stack gap={12}><div className="legal-grid">{(['subject', 'document', 'version', 'from', 'to'] as const).map((name) => <FormField key={name} label={{ subject: 'Subject', document: 'Document slug', version: 'Version', from: 'Accepted from (local time)', to: 'Accepted until (local time)' }[name]}><Input aria-label={`Filter ${name}`} type={name === 'from' || name === 'to' ? 'datetime-local' : 'text'} inputMode={name === 'version' ? 'numeric' : undefined} value={fields[name]} onChange={(event) => setFields((current) => ({ ...current, [name]: event.target.value }))} /></FormField>)}</div><Actions><Button type="submit" size="sm" disabled={query.loading}>Apply filters</Button><Button type="button" size="sm" variant="outline" onClick={() => { setFields(emptyFilters); setFilter(create(FilterSchema)); setValidation(undefined); }}>Reset filters</Button></Actions><ErrorNotice error={validation} /></Stack></form></Panel>
    <Panel title="Export matching evidence"><Stack gap={8}><p>Downloads use the applied filters, not unapplied field edits. CSV and JSON include content and chain hashes.</p><Actions><Button size="sm" variant="outline" disabled={exporting.disabled} onClick={() => void download('csv')}>Download CSV</Button><Button size="sm" variant="outline" disabled={exporting.disabled} onClick={() => void download('json')}>Download JSON</Button></Actions><ActionState action={exporting} />{exporting.value && <p role="status">{exporting.value}</p>}</Stack></Panel>
    <ReadState query={query} /><Panel title="Journal records" count={query.value?.records.length} flush>{query.value && (query.value.records.length ? <div className="legal-table"><Table><TableHeader><TableRow><TableHead>Sequence</TableHead><TableHead>Subject</TableHead><TableHead>Document</TableHead><TableHead>Version / language</TableHead><TableHead>Kind</TableHead><TableHead>Server time</TableHead><TableHead>Evidence</TableHead></TableRow></TableHeader><TableBody>{query.value.records.map((record) => <TableRow key={record.id}><TableCell>{String(record.seq)}</TableCell><TableCell className="legal-wrap">{record.subject}</TableCell><TableCell className="legal-wrap">{record.document}</TableCell><TableCell>v{String(record.version)} / {record.lang}</TableCell><TableCell><Badge variant="outline">{record.kind}</Badge></TableCell><TableCell>{new Date(record.acceptedAt).toLocaleString()}</TableCell><TableCell><details><summary>Hashes and context</summary><dl className="legal-evidence"><dt>Record ID</dt><dd>{record.id}</dd><dt>Accepted at (server)</dt><dd>{record.acceptedAt}</dd><dt>Content SHA-256</dt><dd><Hash value={record.contentHash} /></dd><dt>Previous record hash</dt><dd><Hash value={record.prevHash} /></dd><dt>Record hash</dt><dd><Hash value={record.hash} /></dd><dt>Context</dt><dd><pre>{JSON.stringify(record.context, null, 2)}</pre></dd></dl></details></TableCell></TableRow>)}</TableBody></Table></div> : <EmptyState title="No records match the applied filters" />)}</Panel>
    <Panel title="Verify hash chain"><Stack gap={12}><p>Verification uses sequence bounds, independently of the journal filters. Leave both empty to verify the entire chain.</p><div className="legal-grid"><FormField label="First sequence (inclusive)"><Input aria-label="First sequence" inputMode="numeric" value={rangeFrom} disabled={verifying.pending} onChange={(event) => setRangeFrom(event.target.value)} /></FormField><FormField label="Last sequence (inclusive)"><Input aria-label="Last sequence" inputMode="numeric" value={rangeTo} disabled={verifying.pending} onChange={(event) => setRangeTo(event.target.value)} /></FormField></div><Actions><Button size="sm" disabled={verifying.disabled} onClick={() => void verify()}>Verify chain</Button></Actions><ErrorNotice error={verifyValidation} /><ActionState action={verifying} />{verifying.value && <div role="status"><p>Verified range: {String(verifying.value.from || 'start')} to {String(verifying.value.to || 'end')}.</p><StatusBadge tone={verifying.value.ok ? 'success' : 'danger'}>{verifying.value.ok ? `Chain verified: ${String(verifying.value.checked)} records checked` : `Broken chain at sequence ${String(verifying.value.brokenSeq)} (${String(verifying.value.checked)} records checked)`}</StatusBadge></div>}</Stack></Panel>
  </Frame>;
}
function Missing() { return <Frame title="Page not found" description="Choose Documents or Acceptance journal to continue."><EmptyState title="This legal console page does not exist" /></Frame>; }
export const definition = definePlugin({ ...navigation, routes: [{ index: true, element: <Documents /> }, { path: 'new', element: <NewDocument /> }, { path: 'documents/:slug', element: <DocumentPage /> }, { path: 'journal', element: <Journal /> }, { path: '*', element: <Missing /> }] });
