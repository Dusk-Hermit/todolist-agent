import { createCipheriv, createDecipheriv, randomBytes } from 'node:crypto';
import { mkdir, rename, unlink, writeFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { homedir } from 'node:os';
import { dirname, join } from 'node:path';
import z from '@deepseek-ai/schemastery';
import { queuedInAgent, receiptFromEvents } from './receipt.js';
import { registerManagement } from './management.js';

export const name = 'todolist-agent-connector';
export const Config = z.object({
  port: z.number().min(1024).max(65535).default(17892),
  discoveryFile: z.string(),
  provider: z.string().default('').volatile(),
  model: z.string().default('').volatile(),
  reasoningEffort: z.string().default('').volatile(),
});

const route = '/todolist/v1/rpc';
const host = '127.0.0.1';
const maxRequest = 2 * 1024 * 1024;
const watchTimeout = 9000;
const requestAAD = Buffer.from('todolist/v1/request');
const responseAAD = Buffer.from('todolist/v1/response:');

function decrypt(key, packet) {
  const decipher = createDecipheriv('aes-256-gcm', key, packet.subarray(0, 12));
  decipher.setAAD(requestAAD);
  decipher.setAuthTag(packet.subarray(-16));
  return Buffer.concat([decipher.update(packet.subarray(12, -16)), decipher.final()]);
}

function encrypt(key, requestNonce, value) {
  const nonce = randomBytes(12);
  const cipher = createCipheriv('aes-256-gcm', key, nonce);
  cipher.setAAD(Buffer.concat([responseAAD, requestNonce]));
  return Buffer.concat([nonce, cipher.update(JSON.stringify(value), 'utf8'), cipher.final(), cipher.getAuthTag()]);
}

function validID(value) {
  return typeof value === 'string' && /^[\w.-]{1,160}$/.test(value);
}

async function status(ctx, args, signal) {
  let observation;
  try {
    observation = await ctx.sessionQuery.observeSession(args.sessionId, { signal, projectionMode: 'none' });
    const receipt = receiptFromEvents(observation.events, args.requestId, observation.inheritedEventCount);
    if (receipt.stage !== 'not-found') return receipt;
    return queuedInAgent(ctx.agents.get(args.sessionId), args.requestId) ? { stage: 'queued' } : receipt;
  } catch (error) {
    if (error.code === 'SESSION_QUERY_SESSION_NOT_FOUND') return { stage: 'session-not-found' };
    throw error;
  } finally {
    observation?.[Symbol.dispose]();
  }
}

async function waitForStatus(ctx, args, signal) {
  // Subscribe before reading, so a turn ending during the read cannot be missed.
  let notify;
  let changed = new Promise(resolve => { notify = resolve; });
  const dispose = ctx.on('session/event', (session, event) => {
    if (session.id === args.sessionId && event.type === 'turn/end') notify();
  }, { global: true });
  const timeout = AbortSignal.any([signal, AbortSignal.timeout(watchTimeout)]);
  let stop;
  const expired = new Promise(resolve => { stop = resolve; });
  timeout.addEventListener('abort', stop, { once: true });
  try {
    for (;;) {
      const receipt = await status(ctx, args, signal);
      // An admitted prompt can leave the inbox before its user event is
      // appended. Keep waiting across that gap as well.
      if (timeout.aborted || ['completed', 'failed', 'session-not-found'].includes(receipt.stage)) return receipt;
      await Promise.race([changed, expired]);
      changed = new Promise(resolve => { notify = resolve; });
    }
  } finally {
    timeout.removeEventListener('abort', stop);
    dispose();
  }
}

async function dispatch(ctx, config, method, args, signal) {
  const controller = ctx.sessionController;
  switch (method) {
    case 'session/list':
      return controller.list({}, signal);
    case 'session/create':
      if (args?.cwd !== undefined && typeof args.cwd !== 'string') break;
      return controller.create({ cwd: args?.cwd });
    case 'session/rename':
      if (!validID(args?.sessionId) || typeof args.title !== 'string') break;
      return controller.rename({ sessionId: args.sessionId, title: args.title });
    case 'session/prompt':
      if (!validID(args?.sessionId) || !validID(args.requestId) || args.mode !== 'queue' ||
          !Array.isArray(args.content) || args.content.length !== 1 ||
          args.content[0]?.type !== 'text' || typeof args.content[0].text !== 'string') break;
      {
        const provider = config.provider.get();
        const model = config.model.get();
        const reasoningEffort = config.reasoningEffort.get();
        if (provider && model) {
          const { agent, error } = await controller.resolveAgent(args.sessionId);
          if (error) throw error;
          const current = agent.model.current();
          const effort = reasoningEffort || undefined;
          if (current?.provider !== provider || current.model !== model || current.reasoningEffort !== effort) {
            const selection = { provider, model, ...(effort ? { reasoningEffort: effort } : {}) };
            try {
              await ctx.llm.resolveCallConfig(selection);
            } catch (error) {
              throw Object.assign(new Error(error.message), { code: 'connector/model-unavailable' });
            }
            signal.throwIfAborted();
            // Use the linked Session model without changing the global DSH default.
            agent.model.select(selection);
          }
        }
      }
      return controller.prompt({
        sessionId: args.sessionId, requestId: args.requestId, mode: 'queue',
        content: [{ type: 'text', text: args.content[0].text }],
        clientTimeZone: typeof args.clientTimeZone === 'string' ? args.clientTimeZone : undefined,
      }, signal);
    case 'todolist/status':
    case 'todolist/wait':
      if (!validID(args?.sessionId) || !validID(args.requestId)) break;
      return method === 'todolist/wait' ? waitForStatus(ctx, args, signal) : status(ctx, args, signal);
  }
  throw Object.assign(new Error('Unsupported method or invalid arguments'), { code: 'connector/bad-request' });
}

export function apply(ctx, config = {}) {
  const port = config.port ?? 17892;
  const discoveryFile = config.discoveryFile ?? join(homedir(), '.dsh', 'todolist-connector.json');
  const key = randomBytes(32);
  const recent = new Map();
  const logger = ctx.logger('todolist-agent-connector');
  const diagnostics = [];
  let sessionContext;
  let listening = false;
  function record(level, code) {
    const now = Date.now();
    const last = diagnostics.at(-1);
    if (last?.code === code && now - last.at < 10000) return;
    diagnostics.push({ at: now, level, code });
    if (diagnostics.length > 24) diagnostics.shift();
  }
  ctx.inject(['webServer', 'profileContext'], child => registerManagement(child, () => ({
    host, port, route, watchTimeout, maxRequest, listening, sessionAPI: !!sessionContext,
    diagnostics,
  }), record));
  ctx.inject(['agents', 'llm', 'sessionController', 'sessionQuery'], child => {
    const required = ['list', 'create', 'rename', 'prompt', 'resolveAgent'];
    if (required.some(method => typeof child.sessionController[method] !== 'function') ||
        typeof child.sessionQuery.observeSession !== 'function' || typeof child.agents.get !== 'function' ||
        typeof child.llm.resolveCallConfig !== 'function') {
      record('error', 'DSH_SESSION_API_UNAVAILABLE');
      return;
    }
    sessionContext = child;
    child.on('dispose', () => { sessionContext = undefined; });
  });
  if (!Number.isInteger(port) || port < 1024 || port > 65535) {
    record('error', 'CONNECTOR_PORT_INVALID');
    return;
  }
  const server = createServer({ requestTimeout: 10000 }, async (req, res) => {
    if (req.method !== 'POST' || req.url !== route || req.headers.host !== host + ':' + port ||
        req.headers['content-type'] !== 'application/octet-stream') {
      res.writeHead(404).end();
      return;
    }
    let packet, request;
    try {
      const chunks = [];
      let length = 0;
      for await (const chunk of req) {
        length += chunk.length;
        if (length > maxRequest) throw new Error('Request too large');
        chunks.push(chunk);
      }
      packet = Buffer.concat(chunks);
      request = JSON.parse(decrypt(key, packet));
      const now = Date.now();
      const nonce = packet.subarray(0, 12).toString('hex');
      for (const [seen, expires] of recent) {
        if (expires <= now) recent.delete(seen);
        else break;
      }
      if (!Number.isSafeInteger(request.at) || Math.abs(request.at - now) > 60000 ||
          recent.has(nonce) || recent.size >= 4096) throw new Error('Invalid request timestamp or nonce');
      recent.set(nonce, now + 120000);
    } catch {
      res.writeHead(401).end();
      return;
    }

    const abort = new AbortController();
    res.once('close', () => abort.abort());
    let result;
    try {
      if (!sessionContext) throw Object.assign(new Error('DSH session services are unavailable'), { code: 'connector/session-api-unavailable' });
      const value = await dispatch(sessionContext, config, request.method, request.args?.request ?? request.args?._request, abort.signal);
      result = { ok: true, value };
    } catch (error) {
      if (!abort.signal.aborted) {
        const code = typeof error?.code === 'string' && /^[\w./-]{1,80}$/.test(error.code) ? error.code : 'CONNECTOR_RPC_ERROR';
        record('error', code);
      }
      result = { ok: false, error: { code: error.code ?? 'connector/internal', message: String(error.message) } };
    }
    if (!res.destroyed) {
      res.writeHead(200, { 'Content-Type': 'application/octet-stream', 'Cache-Control': 'no-store' });
      res.end(encrypt(key, packet.subarray(0, 12), { result }));
    }
  });
  server.on('error', error => {
    record('error', error.code === 'EADDRINUSE' ? 'CONNECTOR_PORT_IN_USE' : 'CONNECTOR_LISTEN_FAILED');
    logger.error('Could not listen on %s:%d: %s', host, port, error.message);
  });
  server.listen(port, host, async () => {
    const temp = discoveryFile + '.' + randomBytes(8).toString('hex') + '.tmp';
    try {
      await mkdir(dirname(discoveryFile), { recursive: true, mode: 0o700 });
      await writeFile(temp, JSON.stringify({ version: 1, port, key: key.toString('base64') }), { mode: 0o600, flag: 'wx' });
      await rename(temp, discoveryFile);
      listening = true;
      record('info', 'CONNECTOR_READY');
      logger.info('TodoList connector listening on %s:%d', host, port);
    } catch (error) {
      record('error', 'CONNECTOR_DISCOVERY_WRITE_FAILED');
      await unlink(temp).catch(() => {});
      logger.error('Could not publish TodoList connector endpoint: %s', error.message);
      server.close();
    }
  });
  ctx.on('dispose', () => {
    server.close();
    server.closeAllConnections();
  });
}
