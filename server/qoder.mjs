import uploadTypes from '../shared/upload-types.json' with { type: 'json' };
import { extractDocumentText, OFFICE_EXTENSIONS } from './document-text.mjs';

const UPSTREAM = 'https://api.qoder.com.cn/api/v1/cloud';
const SESSION_ID = /^sess_[A-Za-z0-9_-]{1,160}$/;
const EVENT_ID = /^evt_[A-Za-z0-9_-]{1,160}$/;
const MAX_BODY_BYTES = 12 * 1024 * 1024;
const MAX_FILE_BYTES = 4 * 1024 * 1024;
const MAX_DOCUMENT_BASE64 = Math.ceil(MAX_FILE_BYTES / 3) * 4;
const MAX_IMAGE_BASE64 = 10 * 1024 * 1024;
const FILE_ID = /^file_[A-Za-z0-9_-]{1,160}$/;
const IMAGE_TYPES = new Set(['image/png', 'image/jpeg', 'image/webp', 'image/gif']);
const TEXT_APPLICATION_MIMES = new Set(uploadTypes.textApplicationMimes);
const EXTENSIONLESS_NAMES = new Set(uploadTypes.extensionlessNames);

function supportedTextName(name) {
  const lower = name.toLowerCase();
  return EXTENSIONLESS_NAMES.has(lower) || uploadTypes.textExtensions.some(extension => lower.endsWith(extension));
}

function supportedTextMime(mime) {
  const normalized = mime.toLowerCase().split(';')[0].trim();
  return normalized.startsWith('text/') || TEXT_APPLICATION_MIMES.has(normalized);
}

export class ApiError extends Error {
  constructor(status, code, message) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

function upstreamError(status) {
  const errors = {
    400: ['invalid_request', '云端无法处理这次请求，请刷新会话后重试。'],
    401: ['authentication_failed', '云端令牌无效或已过期，请更新服务端 QODER_ACCESS_TOKEN。'],
    403: ['permission_denied', '当前云端令牌没有访问此会话的权限。'],
    404: ['session_not_found', '此云端会话不存在或已被删除。'],
    409: ['session_busy', '云端会话正在处理消息或暂不可发送，请等待本轮回复完成后重试。'],
    429: ['rate_limited', '云端请求过于频繁，请稍后重试。'],
  };
  const [code, message] = errors[status] || ['upstream_unavailable', '云端服务暂时不可用，请稍后重试。'];
  return new ApiError(errors[status] ? status : 502, code, message);
}

function invalidResponse() {
  return new ApiError(502, 'invalid_upstream_response', '云端返回的数据不完整，请稍后重试。');
}

function publicTurnError(event) {
  const reason = String(event?.error?.message || '');
  if (/image length and width|height:1 or width:1|image.*(size|dimension)/i.test(reason)) {
    return '图片尺寸不符合当前模型要求。若这张图片留在会话历史中，请切换到新会话继续。';
  }
  return '云端处理这条消息时出错，请检查附件后重试。';
}

export function publicSession(session) {
  if (!session || !SESSION_ID.test(session.id)) throw invalidResponse();
  return {
    id: session.id,
    title: typeof session.title === 'string' && session.title.trim() ? session.title : '未命名会话',
    status: session.archived_at ? 'archived' : typeof session.status === 'string' ? session.status : 'unknown',
    createdAt: typeof session.created_at === 'string' ? session.created_at : '',
    updatedAt: typeof session.updated_at === 'string' ? session.updated_at : '',
    agentName: typeof session.agent?.name === 'string' ? session.agent.name : '云端助手',
  };
}

function displayUserText(value) {
  const marker = '我还附上了这些文件，请按需读取：\n';
  const at = value.lastIndexOf(marker);
  if (at === -1) return value;
  const lines = value.slice(at + marker.length).split('\n');
  if (!lines.length || !lines.every(line => /^.+：\/mnt\/session\/uploads\/[A-Za-z0-9._-]+$/.test(line))) return value;
  const prefix = value.slice(0, at).trimEnd();
  return `${prefix}${prefix ? '\n\n' : ''}${lines.map(line => `📎 ${line.split('：/mnt/session/uploads/')[0]}`).join('\n')}`;
}

export function publicMessages(events) {
  return events.flatMap(event => {
    if (!event || !['user.message', 'agent.message'].includes(event.type) || !EVENT_ID.test(event.id)) return [];
    const images = Array.isArray(event.content)
      ? event.content.filter(block => block?.type === 'image' && block.source?.type === 'base64'
        && IMAGE_TYPES.has(block.source.media_type) && typeof block.source.data === 'string'
        && block.source.data.length <= MAX_IMAGE_BASE64 && /^[A-Za-z0-9+/]+={0,2}$/.test(block.source.data))
        .slice(0, 4).map(block => `data:${block.source.media_type};base64,${block.source.data}`)
      : [];
    const text = Array.isArray(event.content)
      ? event.content.map(block => {
        if (block?.type === 'text' && typeof block.text === 'string') return event.type === 'user.message' ? displayUserText(block.text) : block.text;
        return block?.type === 'image' ? (block.source?.type === 'base64' && images.length ? '' : '[图片]') : '[暂不支持的消息内容]';
      }).filter(Boolean).join('\n')
      : '';
    if (!text.trim() && !images.length) return [];
    const createdAt = typeof event.processed_at === 'string' ? event.processed_at : '';
    const date = new Date(createdAt);
    return [{
      id: event.id,
      sender: event.type === 'user.message' ? 'self' : 'ai',
      text,
      ...(images.length ? { images } : {}),
      time: Number.isNaN(date.getTime()) ? '' : date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false }),
      createdAt,
      kind: 'text',
    }];
  });
}

export function createQoderClient({ token, fetchImpl = globalThis.fetch, timeoutMs = 15_000 } = {}) {
  async function request(path, { method = 'GET', body, form, timeoutMs: requestTimeout = timeoutMs } = {}) {
    if (!token?.trim()) throw new ApiError(503, 'not_configured', '尚未配置云端令牌，请在服务端设置 QODER_ACCESS_TOKEN。');
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), requestTimeout);
    try {
      const response = await fetchImpl(`${UPSTREAM}${path}`, {
        method,
        headers: {
          Authorization: `Bearer ${token.trim()}`,
          Accept: 'application/json',
          ...(body ? { 'Content-Type': 'application/json' } : {}),
        },
        ...(body ? { body: JSON.stringify(body) } : form ? { body: form } : {}),
        signal: controller.signal,
        redirect: 'error',
      });
      if (!response.ok) throw upstreamError(response.status);
      try { return await response.json(); } catch (error) {
        if (controller.signal.aborted) throw error;
        throw invalidResponse();
      }
    } catch (error) {
      if (error instanceof ApiError) throw error;
      if (controller.signal.aborted || error?.name === 'TimeoutError') {
        throw new ApiError(504, 'upstream_timeout', method === 'POST'
          ? '发送请求超时，消息可能已送达。请先刷新会话确认，避免重复发送。'
          : '读取云端会话超时，请稍后重试。');
      }
      throw new ApiError(502, 'connection_failed', method === 'POST'
        ? '发送连接中断，消息可能已送达。请先刷新会话确认，避免重复发送。'
        : '无法连接云端服务，请检查网络后重试。');
    } finally {
      clearTimeout(timeout);
    }
  }

  async function listAll(path, { after, events = false } = {}) {
    const entries = [];
    const seenPages = new Set();
    let page;
    for (let index = 0; index < 1000; index++) {
      const query = new URLSearchParams({ limit: '100' });
      if (events) query.set('order', 'asc');
      if (page) query.set('page', page);
      else if (after) query.set('after_id', after);
      const result = await request(`${path}?${query}`);
      if (!Array.isArray(result?.data)) throw invalidResponse();
      if (events && result.data.some(event => !EVENT_ID.test(event?.id))) throw invalidResponse();
      entries.push(...result.data);
      if (!result.has_more) return entries;
      if (typeof result.next_page !== 'string' || !result.next_page || seenPages.has(result.next_page)) throw invalidResponse();
      page = result.next_page;
      seenPages.add(page);
    }
    throw new ApiError(502, 'pagination_limit', '云端会话数据量过大，请稍后重试。');
  }

  return {
    async listSessions(defaultSessionId) {
      const sessions = (await listAll('/sessions')).map(publicSession);
      return { data: sessions, defaultSessionId: sessions.some(session => session.id === defaultSessionId) ? defaultSessionId : null };
    },
    async getMessages(id, after) {
      const events = await listAll(`/sessions/${id}/events`, { after, events: true });
      // Fetch the status after reading events so an in-progress turn is not
      // mistakenly reported as idle using an older Session snapshot.
      const session = publicSession(await request(`/sessions/${id}`));
      return {
        session, messages: publicMessages(events), cursor: events.at(-1)?.id || after || null,
        idleEventId: events.findLast(event => event.type === 'session.status_idle')?.id || null,
        turnError: (() => {
          const lastError = events.findLastIndex(event => event.type === 'session.error');
          const lastReply = events.findLastIndex(event => event.type === 'agent.message');
          return lastError !== -1 || lastReply !== -1 ? lastError > lastReply ? publicTurnError(events[lastError]) : '' : null;
        })(),
      };
    },
    async sendMessage(id, input) {
      const { text, attachments = [] } = typeof input === 'string' ? { text: input } : input;
      const content = [];
      const mounted = [];
      const prepared = [];
      for (const attachment of attachments) {
        if (attachment.kind !== 'document') { prepared.push(attachment); continue; }
        let extracted;
        try { extracted = await extractDocumentText(attachment.name, attachment.data); }
        catch { throw new ApiError(400, 'document_parse_failed', `${attachment.name} 无法解析，请确认文件未损坏或加密。`); }
        if (!extracted.trim()) throw new ApiError(400, 'document_empty', `${attachment.name} 没有可提取的文字内容。`);
        if (Buffer.byteLength(extracted) > MAX_FILE_BYTES) throw new ApiError(413, 'document_text_too_large', `${attachment.name} 提取后的文字超过 4 MB，请拆分文件后重试。`);
        prepared.push({ kind: 'file', name: attachment.name, uploadName: `${attachment.name.replace(/\.[^.]+$/, '').slice(0, 70)}.txt`, mimeType: 'text/plain', content: extracted, extracted: true });
      }
      for (const attachment of prepared) {
        if (attachment.kind === 'image') {
          content.push({ type: 'image', source: { type: 'base64', media_type: attachment.mimeType, data: attachment.data } });
        } else {
          const uploadName = attachment.uploadName || attachment.name;
          const form = new FormData();
          form.append('file', new Blob([attachment.content], { type: attachment.mimeType }), uploadName);
          form.append('name', uploadName);
          const file = await request('/files', { method: 'POST', form, timeoutMs: 60_000 });
          if (!FILE_ID.test(file?.id)) throw invalidResponse();
          const resource = await request(`/sessions/${id}/resources`, { method: 'POST', body: { type: 'file', file_id: file.id } });
          if (typeof resource?.mount_path !== 'string' || !resource.mount_path.startsWith('/')) throw invalidResponse();
          mounted.push(`${attachment.name}${attachment.extracted ? '（已提取为文本）' : ''}：${resource.mount_path}`);
        }
      }
      const prompt = [text, mounted.length ? `我还附上了这些文件，请按需读取：\n${mounted.join('\n')}` : ''].filter(Boolean).join('\n\n');
      if (prompt) content.unshift({ type: 'text', text: prompt });
      const result = await request(`/sessions/${id}/events`, {
        method: 'POST',
        body: { events: [{ type: 'user.message', content }] },
      });
      if (!Array.isArray(result?.data)) throw invalidResponse();
      return { messages: publicMessages(result.data) };
    },
    async openStream(id, after, signal) {
      if (!token?.trim()) throw new ApiError(503, 'not_configured', '尚未配置云端令牌，请在服务端设置 QODER_ACCESS_TOKEN。');
      const query = new URLSearchParams();
      query.append('event_deltas[]', 'agent.message');
      query.append('event_deltas[]', 'agent.thinking');
      let response;
      try {
        response = await fetchImpl(`${UPSTREAM}/sessions/${id}/events/stream?${query}`, {
          headers: { Authorization: `Bearer ${token.trim()}`, Accept: 'text/event-stream', ...(after ? { 'Last-Event-ID': after } : {}) },
          signal, redirect: 'error',
        });
      } catch (error) {
        if (signal.aborted) throw error;
        throw new ApiError(502, 'connection_failed', '实时连接云端失败，正在尝试重新连接。');
      }
      if (!response.ok) throw upstreamError(response.status);
      if (!response.body || !response.headers.get('content-type')?.includes('text/event-stream')) throw invalidResponse();
      return response.body;
    },
  };
}

function ensureSameOrigin(req, allowedOrigin) {
  let origin;
  try {
    origin = new URL(`${req.socket.encrypted ? 'https' : 'http'}://${req.headers.host}`).origin;
    const hostname = new URL(origin).hostname;
    if (allowedOrigin) {
      // An explicit canonical origin also supports HTTPS reverse proxies.
      if (new URL(allowedOrigin).host !== req.headers.host) throw new Error();
      origin = new URL(allowedOrigin).origin;
    } else if (!['127.0.0.1', 'localhost', '[::1]'].includes(hostname)) throw new Error();
  } catch {
    throw new ApiError(403, 'invalid_host', '此主机不允许访问云端会话。');
  }
  if ((req.headers.origin && req.headers.origin !== origin)
    || (req.headers['sec-fetch-site'] && !['same-origin', 'none'].includes(req.headers['sec-fetch-site']))) {
    throw new ApiError(403, 'cross_origin_denied', '仅允许从当前应用访问云端会话。');
  }
}

async function readMessage(req) {
  if (req.headers['content-type']?.split(';')[0].trim().toLowerCase() !== 'application/json') {
    throw new ApiError(415, 'unsupported_media_type', '消息请求必须使用 JSON 格式。');
  }
  if (Number(req.headers['content-length']) > MAX_BODY_BYTES) {
    req.resume();
    throw new ApiError(413, 'body_too_large', '附件总大小超过限制。');
  }
  const data = await new Promise((resolve, reject) => {
    const chunks = [];
    let bytes = 0;
    const onData = chunk => {
      bytes += chunk.length;
      if (bytes > MAX_BODY_BYTES) {
        req.removeListener('data', onData);
        req.resume();
        reject(new ApiError(413, 'body_too_large', '附件总大小超过限制。'));
      } else chunks.push(chunk);
    };
    req.on('data', onData);
    req.once('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
    req.once('error', reject);
    req.once('aborted', () => reject(new ApiError(400, 'request_aborted', '消息请求已中断。')));
  });
  let body;
  try { body = JSON.parse(data); } catch {
    throw new ApiError(400, 'invalid_json', '消息格式不正确，请重新发送。');
  }
  if (!body || Array.isArray(body) || Object.keys(body).some(key => !['text', 'attachments'].includes(key))
    || typeof body.text !== 'string' || body.text.length > 2000 || !Array.isArray(body.attachments ?? [])
    || body.attachments?.length > 4 || (!body.text.trim() && !body.attachments?.length)) {
    throw new ApiError(400, 'invalid_message', '请输入消息或添加最多 4 个附件，文字不超过 2000 字。');
  }
  const attachments = (body.attachments ?? []).map(item => {
    if (!item || typeof item !== 'object' || Array.isArray(item)
      || Object.keys(item).some(key => !['kind', 'name', 'mimeType', 'data', 'content'].includes(key))
      || typeof item.name !== 'string' || !item.name || item.name.length > 255
      || /[/\\\0\r\n]/.test(item.name) || typeof item.mimeType !== 'string') {
      throw new ApiError(400, 'invalid_attachment', '附件名称或格式无效。');
    }
    if (item.kind === 'image') {
      if (!IMAGE_TYPES.has(item.mimeType) || typeof item.data !== 'string' || !item.data
        || item.data.length > MAX_IMAGE_BASE64 || !/^[A-Za-z0-9+/]+={0,2}$/.test(item.data)
        || item.content !== undefined) throw new ApiError(400, 'invalid_attachment', '图片格式或大小不支持。');
      return { kind: 'image', name: item.name, mimeType: item.mimeType, data: item.data };
    }
    if (item.kind === 'file') {
      if (!(supportedTextName(item.name) || supportedTextMime(item.mimeType)) || typeof item.content !== 'string' || !item.content
        || Buffer.byteLength(item.content) > MAX_FILE_BYTES || item.data !== undefined
        || item.content.includes('\0')) {
        throw new ApiError(400, 'invalid_attachment', '仅支持 4 MB 以内的文本类文件。');
      }
      return { kind: 'file', name: item.name, mimeType: supportedTextMime(item.mimeType) ? item.mimeType.toLowerCase().split(';')[0].trim() : 'text/plain', content: item.content };
    }
    if (item.kind === 'document') {
      if (!OFFICE_EXTENSIONS.test(item.name) || typeof item.data !== 'string' || !item.data
        || item.data.length > MAX_DOCUMENT_BASE64 || !/^[A-Za-z0-9+/]+={0,2}$/.test(item.data)
        || item.content !== undefined) throw new ApiError(400, 'invalid_attachment', 'Office 文件格式或大小不支持。');
      const data = Buffer.from(item.data, 'base64');
      if (!data.length || data.length > MAX_FILE_BYTES) throw new ApiError(400, 'invalid_attachment', 'Office 文件超过 4 MB。');
      return { kind: 'document', name: item.name, mimeType: item.mimeType, data };
    }
    throw new ApiError(400, 'invalid_attachment', '附件类型不支持。');
  });
  return { text: body.text.trim(), attachments };
}

function publicStreamEvent(payload) {
  if (!payload || typeof payload !== 'object') return null;
  if (payload.type === 'event_start' && EVENT_ID.test(payload.event?.id)
    && ['agent.message', 'agent.thinking'].includes(payload.event?.type)) {
    return { type: 'start', id: payload.event.id, kind: payload.event.type === 'agent.thinking' ? 'thinking' : 'message' };
  }
  if (payload.type === 'event_delta' && EVENT_ID.test(payload.event_id)
    && payload.delta?.type === 'content_delta' && payload.delta.content?.type === 'text'
    && typeof payload.delta.content.text === 'string') {
    return { type: 'delta', id: payload.event_id, text: payload.delta.content.text.slice(0, 16_384) };
  }
  if (!EVENT_ID.test(payload.id)) return null;
  if (['user.message', 'agent.message'].includes(payload.type)) {
    return { type: 'message', id: payload.id, message: publicMessages([payload])[0] ?? null };
  }
  if (payload.type === 'agent.thinking') return { type: 'thinking_end', id: payload.id };
  if (payload.type.startsWith('session.status_')) return { type: 'status', id: payload.id, status: payload.type.slice('session.status_'.length) };
  if (payload.type === 'session.error') return { type: 'session_error', id: payload.id, message: publicTurnError(payload) };
  if (payload.type === 'session.deleted') return { type: 'status', id: payload.id, status: 'terminated' };
  return null;
}

async function relayStream(body, res) {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  let frame = { id: '', data: [] };
  function flush() {
    if (!frame.data.length) { frame = { id: '', data: [] }; return; }
    try {
      const item = publicStreamEvent(JSON.parse(frame.data.join('\n')));
      if (item) {
        if (EVENT_ID.test(frame.id)) res.write(`id: ${frame.id}\n`);
        res.write(`data: ${JSON.stringify(item)}\n\n`);
      }
    } catch { /* Malformed upstream frame: wait for the next one. */ }
    frame = { id: '', data: [] };
  }
  try {
    while (!res.destroyed) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      let end;
      while ((end = buffer.indexOf('\n')) !== -1) {
        const line = buffer.slice(0, end).replace(/\r$/, '');
        buffer = buffer.slice(end + 1);
        if (!line) flush();
        else if (line.startsWith(':')) res.write(': heartbeat\n\n');
        else if (line.startsWith('id:')) frame.id = line.slice(3).trim();
        else if (line.startsWith('data:')) frame.data.push(line.slice(5).trimStart());
      }
      if (buffer.length > 256 * 1024) { buffer = ''; frame = { id: '', data: [] }; }
    }
  } finally {
    await reader.cancel().catch(() => {});
    if (!res.destroyed) res.end();
  }
}

function json(res, status, payload) {
  res.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store', 'X-Content-Type-Options': 'nosniff' });
  res.end(JSON.stringify(payload));
}

export function createQoderMiddleware({ env = process.env, fetchImpl, timeoutMs } = {}) {
  const client = createQoderClient({ token: env.QODER_ACCESS_TOKEN, fetchImpl, timeoutMs });
  return async (req, res, next = () => json(res, 404, { error: { code: 'not_found', message: '接口不存在。' } })) => {
    const path = (req.url || '').split('?')[0];
    if (path !== '/api/qoder' && !path.startsWith('/api/qoder/')) return next();
    try {
      ensureSameOrigin(req, env.QODER_ALLOWED_ORIGIN);
      const url = new URL(req.url, 'http://localhost');
      if (url.pathname === '/api/qoder/sessions' && req.method === 'GET') {
        if (url.search) throw new ApiError(400, 'invalid_query', '不支持的查询参数。');
        return json(res, 200, await client.listSessions(env.QODER_DEFAULT_SESSION_ID));
      }
      const streamMatch = url.pathname.match(/^\/api\/qoder\/sessions\/(sess_[A-Za-z0-9_-]{1,160})\/stream$/);
      if (streamMatch && req.method === 'GET') {
        const afterValues = url.searchParams.getAll('after');
        const lastEventId = req.headers['last-event-id'];
        if ([...url.searchParams.keys()].some(key => key !== 'after') || afterValues.length > 1
          || (afterValues.length && !EVENT_ID.test(afterValues[0]))
          || (lastEventId && (typeof lastEventId !== 'string' || !EVENT_ID.test(lastEventId)))) {
          throw new ApiError(400, 'invalid_query', '实时会话游标无效。');
        }
        const abort = new AbortController();
        const close = () => abort.abort();
        res.once('close', close);
        try {
          const body = await client.openStream(streamMatch[1], lastEventId || afterValues[0], abort.signal);
          if (abort.signal.aborted) return;
          res.writeHead(200, {
            'Content-Type': 'text/event-stream; charset=utf-8', 'Cache-Control': 'no-cache, no-transform',
            Connection: 'keep-alive', 'X-Content-Type-Options': 'nosniff', 'X-Accel-Buffering': 'no',
          });
          res.flushHeaders?.();
          res.write(': connected\n\n');
          res.setTimeout?.(0);
          await relayStream(body, res);
        } finally {
          res.off('close', close);
          abort.abort();
        }
        return;
      }
      const match = url.pathname.match(/^\/api\/qoder\/sessions\/(sess_[A-Za-z0-9_-]{1,160})\/messages$/);
      if (match && ['GET', 'POST'].includes(req.method)) {
        const afterValues = url.searchParams.getAll('after');
        if ([...url.searchParams.keys()].some(key => key !== 'after') || afterValues.length > 1
          || (afterValues.length && (!EVENT_ID.test(afterValues[0]) || req.method !== 'GET'))) {
          throw new ApiError(400, 'invalid_query', '会话游标无效，请刷新会话。');
        }
        const result = req.method === 'GET'
          ? await client.getMessages(match[1], afterValues[0])
          : await client.sendMessage(match[1], await readMessage(req));
        return json(res, 200, result);
      }
      throw new ApiError(match || streamMatch || url.pathname === '/api/qoder/sessions' ? 405 : 404, 'unsupported_route', '不支持此会话操作。');
    } catch (error) {
      const safe = error instanceof ApiError ? error : new ApiError(500, 'internal_error', '会话服务发生错误，请稍后重试。');
      if (!res.headersSent && !res.destroyed) json(res, safe.status, { error: { code: safe.code, message: safe.message } });
    }
  };
}
