import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { Backends } from './backends.js';

async function fixture(t) {
  const dir = await mkdtemp(join(tmpdir(), 'todolist-backends-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const server = createServer((req, res) => {
    assert.equal(req.url, '/api/todos');
    assert.equal(req.headers.authorization, 'Bearer secret');
    res.end('[]');
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => server.close());
  const sessions = new Map();
  const requests = [];
  const ctx = {
    sessionController: {
      async create({ sessionId }) { if (!sessions.has(sessionId)) sessions.set(sessionId, []); return { sessionId }; },
      async rename() {},
      async list() { return { items: [...sessions.keys()].map(sessionId => ({ sessionId, running: false })) }; },
      async prompt(request) {
        requests.push(request);
        sessions.get(request.sessionId).push(
          { type: 'turn/start', data: { turn: 1 } },
          { type: 'user/message', data: { source: { kind: 'user', rpcId: request.requestId }, content: request.content } },
        );
      },
    },
    sessionQuery: {
      async observeSession(id) {
        return { events: sessions.get(id), inheritedEventCount: 0, [Symbol.dispose]() {} };
      },
    },
  };
  const path = join(dir, 'backends.json');
  const construct = template => new Backends(path, template, () => ctx, async () => {});
  const binding = { clientID: 'desktop', url: 'http://127.0.0.1:' + server.address().port, token: 'secret' };
  const signal = t.signal;
  const finish = (id, kind = 'completed') => sessions.get(id).push({ type: 'turn/end', data: { turn: 1, reason: { kind } } });
  return { construct, binding, signal, sessions, requests, finish, ctx };
}

test('initializes before admission, pins messages and preserves assignments after a manual switch and restart', async t => {
  const f = await fixture(t);
  const store = f.construct('Init {{BRIDGE_URL}} {{BRIDGE_TOKEN}}');
  const first = await store.register(f.binding, f.signal);
  assert.equal(first.backend.phase, 'initializing');
  await assert.rejects(store.prepare('desktop', 'one', f.signal), /initialization/);
  assert.equal(f.requests.length, 1);
  assert.match(f.requests[0].content[0].text, /Bearer|secret/);
  f.finish(first.backend.sessionId);
  assert.equal((await store.register(f.binding, f.signal)).backend.phase, 'ready');
  assert.equal((await store.prepare('desktop', 'one', f.signal)).sessionId, first.backend.sessionId);
  const next = await store.change('desktop', undefined, f.signal);
  assert.notEqual(next.sessionId, first.backend.sessionId);
  assert.equal((await store.prepare('desktop', 'one', f.signal)).sessionId, first.backend.sessionId);
  f.finish(next.sessionId);
  assert.equal((await store.prepare('desktop', 'two', f.signal)).sessionId, next.sessionId);
  const restored = f.construct('Init {{BRIDGE_URL}} {{BRIDGE_TOKEN}}');
  assert.equal((await restored.register(f.binding, f.signal)).backend.sessionId, next.sessionId);
  assert.equal(await restored.assigned('desktop', 'one'), first.backend.sessionId);
  assert.equal(f.requests.length, 2);
  await restored.change('desktop', first.backend.sessionId, f.signal);
  assert.equal((await restored.prepare('desktop', 'three', f.signal)).sessionId, first.backend.sessionId);
  assert.equal(await restored.assigned('desktop', 'two'), next.sessionId);
  const lineEndings = f.construct('Init {{BRIDGE_URL}} {{BRIDGE_TOKEN}}\r\n');
  assert.equal((await lineEndings.register(f.binding, f.signal)).backend.sessionId, first.backend.sessionId);
});

test('prompt changes create one new backend while old message receipts retain their original route', async t => {
  const f = await fixture(t);
  const old = f.construct('Old instructions');
  const initial = await old.register(f.binding, f.signal);
  f.finish(initial.backend.sessionId);
  await old.prepare('desktop', 'pending', f.signal);
  const updated = f.construct('New instructions');
  const fresh = await updated.register(f.binding, f.signal);
  assert.notEqual(fresh.backend.sessionId, initial.backend.sessionId);
  assert.equal((await updated.register(f.binding, f.signal)).backend.sessionId, fresh.backend.sessionId);
  assert.equal(await updated.assigned('desktop', 'pending'), initial.backend.sessionId);
  await assert.rejects(updated.change('desktop', initial.backend.sessionId, f.signal), /older/);
  f.finish(fresh.backend.sessionId, 'interrupted');
  assert.equal((await updated.register(f.binding, f.signal)).backend.phase, 'failed');
  await assert.rejects(updated.prepare('desktop', 'new', f.signal), /initialization/);
  assert.equal(f.requests.length, 2);
});

test('recovers initialization after admission succeeds but its acknowledgement is lost', async t => {
  const f = await fixture(t);
  const original = f.ctx.sessionController.prompt;
  f.ctx.sessionController.prompt = async request => {
    await original(request);
    throw new Error('lost acknowledgement');
  };
  const store = f.construct('Instructions');
  await assert.rejects(store.register(f.binding, f.signal), /lost acknowledgement/);
  f.ctx.sessionController.prompt = original;
  const restored = f.construct('Instructions');
  const pending = await restored.register(f.binding, f.signal);
  assert.equal(pending.backend.phase, 'initializing');
  assert.equal(f.requests.length, 1);
  f.finish(pending.backend.sessionId);
  assert.equal((await restored.register(f.binding, f.signal)).backend.phase, 'ready');
  assert.equal(f.requests.length, 1);
});

test('an initialization with no durable or live evidence fails visibly instead of replaying', async t => {
  const f = await fixture(t);
  f.ctx.sessionController.prompt = async () => { throw new Error('admission interrupted'); };
  const initial = f.construct('Instructions');
  await assert.rejects(initial.register(f.binding, f.signal), /interrupted/);
  const restored = f.construct('Instructions');
  const result = await restored.register(f.binding, f.signal);
  assert.equal(result.backend.phase, 'failed');
  assert.equal(result.backend.reason, 'initialization-record-unavailable');
  await assert.rejects(restored.prepare('desktop', 'one', f.signal), /initialization/);
});
