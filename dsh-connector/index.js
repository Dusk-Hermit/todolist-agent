import { createCipheriv, createDecipheriv, randomBytes } from 'node:crypto';
import { mkdir, rename, unlink, writeFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { homedir } from 'node:os';
import { dirname, join } from 'node:path';
import { queuedInAgent, receiptFromEvents } from './receipt.js';

export const name = 'todolist-agent-connector';
export const inject = ['agents', 'sessionController', 'sessionQuery'];

const route = '/todolist/v1/rpc';
const host = '127.0.0.1';
const maxRequest = 2 * 1024 * 1024;
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

async function dispatch(ctx, method, args, signal) {
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
      return controller.prompt({
        sessionId: args.sessionId, requestId: args.requestId, mode: 'queue',
        content: [{ type: 'text', text: args.content[0].text }],
        clientTimeZone: typeof args.clientTimeZone === 'string' ? args.clientTimeZone : undefined,
      }, signal);
    case 'todolist/status': {
      if (!validID(args?.sessionId) || !validID(args.requestId)) break;
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
  }
  throw Object.assign(new Error('Unsupported method or invalid arguments'), { code: 'connector/bad-request' });
}

export function apply(ctx, config = {}) {
  const port = config.port ?? 17892;
  const discoveryFile = config.discoveryFile ?? join(homedir(), '.dsh', 'todolist-connector.json');
  if (!Number.isInteger(port) || port < 1024 || port > 65535) throw new Error('Invalid TodoList connector port');
  const key = randomBytes(32);
  const recent = new Map();
  const logger = ctx.logger('todolist-agent-connector');
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
      const value = await dispatch(ctx, request.method, request.args?.request ?? request.args?._request, abort.signal);
      result = { ok: true, value };
    } catch (error) {
      result = { ok: false, error: { code: error.code ?? 'connector/internal', message: String(error.message) } };
    }
    if (!res.destroyed) {
      res.writeHead(200, { 'Content-Type': 'application/octet-stream', 'Cache-Control': 'no-store' });
      res.end(encrypt(key, packet.subarray(0, 12), { result }));
    }
  });
  server.on('error', error => logger.error('Could not listen on %s:%d: %s', host, port, error.message));
  server.listen(port, host, async () => {
    const temp = discoveryFile + '.' + randomBytes(8).toString('hex') + '.tmp';
    try {
      await mkdir(dirname(discoveryFile), { recursive: true, mode: 0o700 });
      await writeFile(temp, JSON.stringify({ version: 1, port, key: key.toString('base64') }), { mode: 0o600, flag: 'wx' });
      await rename(temp, discoveryFile);
      logger.info('TodoList connector listening on %s:%d', host, port);
    } catch (error) {
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
