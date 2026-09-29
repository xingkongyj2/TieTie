import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer, request as httpRequest } from 'node:http';
import * as XLSX from 'xlsx';
import JSZip from 'jszip';
import { createQoderClient, createQoderMiddleware, publicMessages, publicSession } from './qoder.mjs';
import { extractDocumentText } from './document-text.mjs';

const session = (id = 'sess_one') => ({
  id, title: '', status: 'idle', created_at: '2026-09-28T01:02:03Z', updated_at: '2026-09-29T01:02:03Z',
  agent: { name: '贴贴助手', system: 'private system prompt', model: { api_key: 'private key' } },
  environment_variables: { SECRET: 'private environment' }, resources: [{ authorization_token: 'private resource' }],
});
const event = (id = 'evt_one', type = 'user.message', text = '你好') => ({
  id, type, content: [{ type: 'text', text }], processed_at: '2026-09-29T01:02:03.123456Z',
});
const json = value => new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
const page = (data, next = null) => ({ data, next_page: next, has_more: Boolean(next) });

async function fixture(t, options = {}) {
  const calls = [];
  const fetchImpl = options.fetchImpl || (async (url, init) => {
    calls.push({ url, init });
    if (init.method === 'POST') return json({ data: [event()] });
    if (new URL(url).pathname.endsWith('/events')) return json(page([event()]));
    if (new URL(url).pathname.endsWith('/sessions')) return json(page([session()]));
    return json(session());
  });
  const server = createServer(createQoderMiddleware({ env: { QODER_ACCESS_TOKEN: 'test-only-token', ...options.env }, ...options, fetchImpl }));
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise(resolve => { server.close(resolve); server.closeAllConnections(); }));
  return { origin: `http://127.0.0.1:${server.address().port}`, calls };
}

test('all Session pages are loaded and private fields never leave the proxy', async () => {
  const calls = [];
  const client = createQoderClient({ token: 'test-token', fetchImpl: async (url, init) => {
    calls.push({ url: new URL(url), init });
    return json(calls.length === 1 ? page([session()], 'next opaque &page') : page([session('sess_two')]));
  } });
  const result = await client.listSessions('sess_two');
  assert.equal(result.data.length, 2);
  assert.equal(result.defaultSessionId, 'sess_two');
  assert.deepEqual(Object.keys(result.data[0]).sort(), ['id', 'title', 'status', 'createdAt', 'updatedAt', 'agentName'].sort());
  assert.equal(result.data[0].title, '未命名会话');
  assert.equal(result.data[0].agentName, '贴贴助手');
  assert.equal(JSON.stringify(result).includes('private'), false);
  assert.equal(calls[1].url.searchParams.get('page'), 'next opaque &page');
  assert.equal(calls[0].url.searchParams.get('limit'), '100');
  assert.equal(calls[0].init.headers.Authorization, 'Bearer test-token');
  assert.equal(calls[0].init.redirect, 'error');
});

test('incremental pages use after_id only on page one; raw status events advance the cursor', async () => {
  const calls = [];
  const client = createQoderClient({ token: 'test-token', fetchImpl: async url => {
    const parsed = new URL(url);
    calls.push(parsed);
    if (!parsed.pathname.endsWith('/events')) return json(session());
    return json(!parsed.searchParams.has('page')
      ? page([event('evt_a'), event('evt_think', 'agent.reasoning', 'private reasoning'), event('evt_tool', 'agent.tool_use', 'secret command')], 'page-2')
      : page([event('evt_b', 'agent.message', '你好呀'), event('evt_status', 'session.status_idle', 'status')]));
  } });
  const result = await client.getMessages('sess_one', 'evt_before');
  assert.equal(calls[0].searchParams.get('after_id'), 'evt_before');
  assert.equal(calls[1].searchParams.get('page'), 'page-2');
  assert.equal(calls[1].searchParams.has('after_id'), false);
  assert.equal(calls[0].searchParams.get('order'), 'asc');
  assert.deepEqual(result.messages.map(message => [message.id, message.sender, message.text]), [['evt_a', 'self', '你好'], ['evt_b', 'ai', '你好呀']]);
  assert.equal(result.messages[0].createdAt, '2026-09-29T01:02:03.123456Z');
  assert.equal(result.cursor, 'evt_status');
  assert.equal(result.idleEventId, 'evt_status');
  assert.equal(result.turnError, '');
});

test('empty incremental events preserve the previous cursor', async () => {
  const client = createQoderClient({ token: 'test-token', fetchImpl: async url => json(new URL(url).pathname.endsWith('/sess_one') ? session() : page([])) });
  assert.equal((await client.getMessages('sess_one', 'evt_previous')).cursor, 'evt_previous');
  assert.equal((await client.getMessages('sess_one')).cursor, null);
  assert.equal((await client.listSessions('sess_missing')).defaultSessionId, null);
});

test('repeated or missing pagination cursors fail rather than truncating or looping forever', async () => {
  for (const response of [page([session()], 'same'), { data: [], has_more: true, next_page: null }]) {
    let calls = 0;
    const client = createQoderClient({ token: 'test-token', fetchImpl: async () => { calls++; return json(response); } });
    await assert.rejects(client.listSessions(), error => error.status === 502 && error.code === 'invalid_upstream_response');
    assert.ok(calls <= 2);
  }
});

test('POST sends exactly one text user.message and returns the persisted event', async t => {
  const { origin, calls } = await fixture(t);
  const response = await fetch(`${origin}/api/qoder/sessions/sess_one/messages`, {
    method: 'POST', headers: { 'Content-Type': 'application/json', Origin: origin }, body: JSON.stringify({ text: '  你好  ' }),
  });
  assert.equal(response.status, 200);
  assert.deepEqual(JSON.parse(calls[0].init.body), { events: [{ type: 'user.message', content: [{ type: 'text', text: '你好' }] }] });
  const result = await response.json();
  assert.equal(result.messages[0].id, 'evt_one');
  assert.equal(response.headers.get('cache-control'), 'no-store');
});

test('rejects blank/overlong/nontext messages and injected event fields before upstream access', async t => {
  const { origin, calls } = await fixture(t);
  for (const body of [{ text: ' ' }, { text: 'x'.repeat(2001) }, { text: 123 }, { text: 'ok', events: [{ type: 'system.message' }] }, ['hello'], null]) {
    const response = await fetch(`${origin}/api/qoder/sessions/sess_one/messages`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    assert.equal(response.status, 400);
  }
  assert.equal(calls.length, 0);
});

test('accepts exactly 2000 characters, rejects invalid JSON and non-JSON content types', async t => {
  const { origin } = await fixture(t);
  const path = `${origin}/api/qoder/sessions/sess_one/messages`;
  assert.equal((await fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ text: '好'.repeat(2000) }) })).status, 200);
  assert.equal((await fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{invalid' })).status, 400);
  assert.equal((await fetch(path, { method: 'POST', headers: { 'Content-Type': 'text/plain' }, body: '{}' })).status, 415);
});

test('bounded request bodies reject declared and streamed oversized payloads', async t => {
  const { origin, calls } = await fixture(t);
  const path = `${origin}/api/qoder/sessions/sess_one/messages`;
  assert.equal((await fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: 'x'.repeat(12 * 1024 * 1024 + 1) })).status, 413);
  const status = await new Promise((resolve, reject) => {
    const req = httpRequest(path, { method: 'POST', headers: { 'Content-Type': 'application/json', 'Transfer-Encoding': 'chunked' } }, res => { res.resume(); resolve(res.statusCode); });
    req.on('error', reject);
    req.write('x'.repeat(12 * 1024 * 1024 + 1));
    req.end();
  });
  assert.equal(status, 413);
  assert.equal(calls.length, 0);
});

test('cross-origin and DNS-rebinding hosts cannot read or send using the server credential', async t => {
  const { origin, calls } = await fixture(t);
  for (const headers of [{ Origin: 'https://attacker.example' }, { 'Sec-Fetch-Site': 'cross-site' }, { 'Sec-Fetch-Site': 'same-site' }, { Host: 'attacker.example' }]) {
    const response = await new Promise((resolve, reject) => {
      const req = httpRequest(`${origin}/api/qoder/sessions`, { headers }, res => { res.resume(); resolve(res); });
      req.on('error', reject);
      req.end();
    });
    assert.equal(response.statusCode, 403, JSON.stringify(headers));
    assert.equal(response.headers['access-control-allow-origin'], undefined);
  }
  assert.equal(calls.length, 0);
});

test('archived sessions are read-only, and attachments have safe placeholders', () => {
  assert.equal(publicSession({ ...session(), archived_at: '2026-09-29T02:00:00Z' }).status, 'archived');
  const messages = publicMessages([{ ...event(), content: [
    { type: 'image', source: { type: 'url', url: 'https://private.example/secret' } },
    { type: 'file', url: 'https://private.example/secret' },
  ] }]);
  assert.equal(messages[0].text, '[图片]\n[暂不支持的消息内容]');
  assert.equal(JSON.stringify(messages).includes('secret'), false);
  const imageOnly = publicMessages([{ ...event('evt_image'), content: [{ type: 'image', source: { type: 'base64', media_type: 'image/png', data: 'aGVsbG8=' } }] }]);
  assert.equal(imageOnly[0].text, '');
  assert.deepEqual(imageOnly[0].images, ['data:image/png;base64,aGVsbG8=']);
});

test('strict route and query validation do not allow arbitrary upstream proxying', async t => {
  const { origin, calls } = await fixture(t);
  for (const [path, status] of [
    ['/api/qoder/environments', 404], ['/api/qoder/sessions/sess_one/messages?after=bad', 400],
    ['/api/qoder/sessions/sess_one/messages?after=evt_a&after=evt_b', 400],
    ['/api/qoder/sessions/sess_one/messages?url=https://attacker.example', 400],
    ['/api/qoder/sessions?limit=100', 400], ['/api/qoder/sessions/sess_one%2Fsecrets/messages', 404],
  ]) assert.equal((await fetch(origin + path)).status, status);
  assert.equal((await fetch(`${origin}/api/qoder/sessions`, { method: 'DELETE' })).status, 405);
  assert.equal(calls.length, 0);
});

test('missing server credential returns a useful error without upstream access', async t => {
  const { origin, calls } = await fixture(t, { env: { QODER_ACCESS_TOKEN: '' } });
  const response = await fetch(`${origin}/api/qoder/sessions`);
  assert.equal(response.status, 503);
  assert.equal((await response.json()).error.code, 'not_configured');
  assert.equal(calls.length, 0);
});

test('authentication, permission, not-found, busy and rate errors are actionable without leaking upstream data', async t => {
  for (const status of [401, 403, 404, 409, 429, 500]) {
    const { origin } = await fixture(t, { fetchImpl: async () => new Response('upstream error contains private-token', { status }) });
    const response = await fetch(`${origin}/api/qoder/sessions`);
    assert.equal(response.status, status === 500 ? 502 : status);
    const body = await response.text();
    assert.equal(body.includes('private-token'), false);
    assert.equal(body.includes('test-only-token'), false);
    assert.ok(JSON.parse(body).error.message.length > 5);
  }
});

test('timeouts abort fetch and warn that timed-out sends may already have arrived', async () => {
  const client = createQoderClient({ token: 'test-token', timeoutMs: 5, fetchImpl: async (_url, init) => new Promise((_resolve, reject) => {
    init.signal.addEventListener('abort', () => reject(new DOMException('test-token', 'AbortError')));
  }) });
  await assert.rejects(client.listSessions(), error => error.status === 504 && !error.message.includes('test-token'));
  await assert.rejects(client.sendMessage('sess_one', 'hello'), error => error.status === 504 && error.message.includes('可能已送达'));
});

test('image content blocks and uploaded text resources reach the correct Qoder endpoints', async t => {
  const calls = [];
  const { origin } = await fixture(t, { fetchImpl: async (url, init) => {
    calls.push({ path: new URL(url).pathname, init });
    if (url.endsWith('/files')) return json({ id: 'file_uploaded' });
    if (url.endsWith('/resources')) return json({ mount_path: '/mnt/session/uploads/file_uploaded' });
    return json({ data: [event()] });
  } });
  const response = await fetch(`${origin}/api/qoder/sessions/sess_one/messages`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ text: '看看附件', attachments: [
      { kind: 'file', name: 'notes.md', mimeType: 'text/markdown', content: '# Hello' },
      { kind: 'image', name: 'photo.png', mimeType: 'image/png', data: 'aGVsbG8=' },
    ] }),
  });
  assert.equal(response.status, 200);
  assert.deepEqual(calls.map(call => call.path.slice('/api/v1/cloud'.length)), [
    '/files', '/sessions/sess_one/resources', '/sessions/sess_one/events',
  ]);
  assert.equal(calls[0].init.body.get('name'), 'notes.md');
  assert.equal(await calls[0].init.body.get('file').text(), '# Hello');
  assert.deepEqual(JSON.parse(calls[1].init.body), { type: 'file', file_id: 'file_uploaded' });
  const content = JSON.parse(calls[2].init.body).events[0].content;
  assert.match(content[0].text, /notes\.md.*\/mnt\/session\/uploads\/file_uploaded/s);
  assert.deepEqual(content[1], { type: 'image', source: { type: 'base64', media_type: 'image/png', data: 'aGVsbG8=' } });
  assert.equal(publicMessages([event('evt_display', 'user.message', content[0].text)])[0].text, '看看附件\n\n📎 notes.md');
});

test('documented MIME categories and extensionless text files reach Files upload', async t => {
  const calls = [];
  const { origin } = await fixture(t, { fetchImpl: async (url, init) => {
    calls.push({ path: new URL(url).pathname, init });
    if (url.endsWith('/files')) return json({ id: `file_uploaded_${calls.length}` });
    if (url.endsWith('/resources')) return json({ mount_path: `/mnt/session/uploads/file_uploaded_${calls.length}` });
    return json({ data: [event()] });
  } });
  const attachments = [
    { kind: 'file', name: 'Dockerfile', mimeType: 'application/octet-stream', content: 'FROM node:22' },
    { kind: 'file', name: 'data.tsv', mimeType: 'text/tab-separated-values', content: 'a\tb' },
    { kind: 'file', name: 'notes.custom', mimeType: 'application/x-yaml', content: 'name: test' },
    { kind: 'file', name: 'vector.svg', mimeType: 'image/svg+xml', content: '<svg></svg>' },
  ];
  const response = await fetch(`${origin}/api/qoder/sessions/sess_one/messages`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ text: '', attachments }),
  });
  assert.equal(response.status, 200);
  const uploads = calls.filter(call => call.path.endsWith('/files'));
  assert.deepEqual(uploads.map(call => call.init.body.get('name')), attachments.map(item => item.name));
  assert.deepEqual(uploads.map(call => call.init.body.get('file').type), ['text/plain', 'text/tab-separated-values', 'application/x-yaml', 'text/plain']);
  assert.equal(calls.filter(call => call.path.endsWith('/resources')).length, 4);
});

test('Excel and Word content is extracted and mounted as text before sending', async t => {
  const workbook = XLSX.utils.book_new();
  XLSX.utils.book_append_sheet(workbook, XLSX.utils.aoa_to_sheet([['姓名', '金额'], ['小明', 42]]), '清单');
  const excel = XLSX.write(workbook, { type: 'buffer', bookType: 'xlsx' });
  const legacyExcel = XLSX.write(workbook, { type: 'buffer', bookType: 'biff8' });
  assert.match(await extractDocumentText('旧表.xls', legacyExcel), /小明,42/);

  const zip = new JSZip();
  zip.file('[Content_Types].xml', '<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>');
  zip.file('_rels/.rels', '<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>');
  zip.file('word/document.xml', '<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>贴贴 Word 测试</w:t></w:r></w:p></w:body></w:document>');
  const word = await zip.generateAsync({ type: 'nodebuffer' });

  const calls = [];
  const { origin } = await fixture(t, { fetchImpl: async (url, init) => {
    calls.push({ path: new URL(url).pathname, init });
    if (url.endsWith('/files')) return json({ id: `file_uploaded_${calls.length}` });
    if (url.endsWith('/resources')) return json({ mount_path: `/mnt/session/uploads/file_uploaded_${calls.length}` });
    return json({ data: [event()] });
  } });
  const response = await fetch(`${origin}/api/qoder/sessions/sess_one/messages`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ text: '请看文件', attachments: [
      { kind: 'document', name: '账本.xlsx', mimeType: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', data: excel.toString('base64') },
      { kind: 'document', name: '说明.docx', mimeType: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', data: word.toString('base64') },
    ] }),
  });
  assert.equal(response.status, 200);
  const uploads = calls.filter(call => call.path.endsWith('/files'));
  assert.deepEqual(uploads.map(call => call.init.body.get('name')), ['账本.txt', '说明.txt']);
  assert.match(await uploads[0].init.body.get('file').text(), /工作表：清单\n姓名,金额\n小明,42/);
  assert.match(await uploads[1].init.body.get('file').text(), /贴贴 Word 测试/);
  const prompt = JSON.parse(calls.at(-1).init.body).events[0].content[0].text;
  assert.match(prompt, /账本\.xlsx（已提取为文本）/);
  assert.match(prompt, /说明\.docx（已提取为文本）/);
});

test('broken Office files do not partially upload other attachments or send a message', async t => {
  const { origin, calls } = await fixture(t);
  const response = await fetch(`${origin}/api/qoder/sessions/sess_one/messages`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ text: '', attachments: [
      { kind: 'file', name: 'notes.txt', mimeType: 'text/plain', content: 'keep' },
      { kind: 'document', name: 'broken.docx', mimeType: 'application/octet-stream', data: Buffer.from('invalid').toString('base64') },
    ] }),
  });
  assert.equal(response.status, 400);
  assert.equal((await response.json()).error.code, 'document_parse_failed');
  assert.equal(calls.length, 0);
});

test('invalid attachments are rejected before any upstream request', async t => {
  const { origin, calls } = await fixture(t);
  const path = `${origin}/api/qoder/sessions/sess_one/messages`;
  for (const attachments of [
    [{ kind: 'file', name: 'script.exe', mimeType: 'application/octet-stream', content: 'oops' }],
    [{ kind: 'file', name: 'report.pdf', mimeType: 'application/pdf', content: '%PDF-1.7' }],
    [{ kind: 'file', name: 'fake.txt', mimeType: 'text/plain', content: 'binary\0data' }],
    [{ kind: 'image', name: 'photo.png', mimeType: 'image/png', data: 'invalid!' }],
    [{ kind: 'file', name: '../secret.md', mimeType: 'text/plain', content: 'oops' }],
  ]) {
    const result = await fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ text: '', attachments }) });
    assert.equal(result.status, 400);
  }
  assert.equal(calls.length, 0);
});

test('SSE forwards safe message deltas, thinking state, and final events with resume cursor', async t => {
  const calls = [];
  const frames = [
    ['evt_think', { type: 'event_start', event: { id: 'evt_think', type: 'agent.thinking' } }],
    ['evt_private', { id: 'evt_private', type: 'agent.tool_use', content: 'secret tool data' }],
    ['evt_msg', { type: 'event_start', event: { id: 'evt_msg', type: 'agent.message' } }],
    ['evt_msg', { type: 'event_delta', event_id: 'evt_msg', delta: { type: 'content_delta', index: 0, content: { type: 'text', text: '你好' } } }],
    ['evt_msg', event('evt_msg', 'agent.message', '你好')],
    ['evt_idle', { id: 'evt_idle', type: 'session.status_idle', processed_at: '2026-09-29T01:02:04Z' }],
  ];
  const body = frames.map(([id, data]) => `id: ${id}\nevent: ${data.type}\ndata: ${JSON.stringify(data)}\n\n`).join('');
  const { origin } = await fixture(t, { fetchImpl: async (url, init) => {
    calls.push({ url: new URL(url), init });
    return new Response(body, { headers: { 'Content-Type': 'text/event-stream' } });
  } });
  const result = await fetch(`${origin}/api/qoder/sessions/sess_one/stream?after=evt_previous`);
  assert.equal(result.status, 200);
  const stream = await result.text();
  assert.equal(calls[0].init.headers['Last-Event-ID'], 'evt_previous');
  assert.deepEqual(calls[0].url.searchParams.getAll('event_deltas[]'), ['agent.message', 'agent.thinking']);
  assert.match(stream, /"type":"delta".*"text":"你好"/);
  assert.match(stream, /"kind":"thinking"/);
  assert.match(stream, /"status":"idle"/);
  assert.equal(stream.includes('secret tool data'), false);
  assert.equal((await fetch(`${origin}/api/qoder/sessions/sess_one/stream?after=invalid`)).status, 400);
});

test('session execution errors are surfaced without leaking raw upstream diagnostics', async () => {
  const client = createQoderClient({ token: 'test-token', fetchImpl: async url => new URL(url).pathname.endsWith('/events')
    ? json(page([event('evt_user'), { id: 'evt_error', type: 'session.error', error: { message: 'private prompt: image length and width do not meet model restrictions' } }]))
    : json(session()) });
  const result = await client.getMessages('sess_one');
  assert.match(result.turnError, /图片尺寸/);
  assert.equal(JSON.stringify(result).includes('private prompt'), false);
});
