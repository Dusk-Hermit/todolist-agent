import { createHash, randomUUID } from 'node:crypto';
import { mkdir, readFile, rename, unlink, writeFile } from 'node:fs/promises';
import { dirname } from 'node:path';
import { queuedInAgent, receiptFromEvents } from './receipt.js';

const hash = value => createHash('sha256').update(value).digest('hex');
const failure = message => Object.assign(new Error(message), { code: 'connector/backend-not-ready' });

export class Backends {
  constructor(path, template, context, applyModel) {
    this.path = path;
    this.template = template.replaceAll('\r\n', '\n').trimEnd() + '\n';
    this.promptHash = hash(this.template);
    this.context = context;
    this.applyModel = applyModel;
    this.pending = Promise.resolve();
    this.state = { version: 1, clients: {}, assignments: {} };
    this.loaded = false;
  }

  serial(operation) {
    const task = this.pending.then(async () => {
      if (!this.loaded) {
        try {
          const stored = JSON.parse(await readFile(this.path, 'utf8'));
          if (stored.version !== 1 || !stored.clients || !stored.assignments) throw new Error('Invalid backend state');
          this.state = stored;
        } catch (error) {
          if (error.code !== 'ENOENT') throw error;
        }
        this.loaded = true;
      }
      return operation();
    });
    this.pending = task.catch(() => {});
    return task;
  }

  async save(next) {
    const temp = this.path + '.' + randomUUID() + '.tmp';
    await mkdir(dirname(this.path), { recursive: true, mode: 0o700 });
    try {
      await writeFile(temp, JSON.stringify(next), { mode: 0o600, flag: 'wx' });
      await rename(temp, this.path);
      this.state = next;
    } finally {
      await unlink(temp).catch(() => {});
    }
  }

  async create(clientID) {
    const next = structuredClone(this.state);
    const client = next.clients[clientID];
    const backend = {
      sessionId: 'session-' + randomUUID(), initID: randomUUID(), phase: 'created',
      promptHash: this.promptHash, bindingHash: hash(JSON.stringify(client.binding)), createdAt: Date.now(),
    };
    client.backends.push(backend);
    client.active = backend.sessionId;
    await this.save(next);
    return backend;
  }

  async setPhase(clientID, sessionID, phase, reason = '') {
    const next = structuredClone(this.state);
    const backend = next.clients[clientID].backends.find(item => item.sessionId === sessionID);
    backend.phase = phase;
    backend.reason = reason;
    await this.save(next);
    return backend;
  }

  async ensure(clientID, signal) {
    let client = this.state.clients[clientID];
    if (!client) throw failure('TodoList bridge is not registered');
    let backend = client.backends.find(item => item.sessionId === client.active);
    if (!backend || backend.promptHash !== this.promptHash || backend.bindingHash !== hash(JSON.stringify(client.binding))) {
      backend = await this.create(clientID);
      client = this.state.clients[clientID];
    }
    const ctx = this.context();
    if (!ctx) throw failure('DSH session services are unavailable');
    let startInitialization = false;
    if (backend.phase === 'created') {
      const response = await fetch(client.binding.url + '/api/todos', {
        headers: { Authorization: 'Bearer ' + client.binding.token },
        redirect: 'error', signal: AbortSignal.any([signal, AbortSignal.timeout(4000)]),
      });
      await response.body?.cancel();
      if (!response.ok) throw failure('TodoList bridge authentication or task endpoint failed');
      const created = await ctx.sessionController.create({
        sessionId: backend.sessionId, ...(client.binding.workspace ? { cwd: client.binding.workspace } : {}),
      });
      if (created.sessionId !== backend.sessionId) throw failure('DSH returned a different backend session');
      await ctx.sessionController.rename({ sessionId: backend.sessionId, title: 'TodoList · ' + new Date(backend.createdAt).toLocaleString() });
      await this.applyModel(ctx, backend.sessionId, signal);
      backend = await this.setPhase(clientID, backend.sessionId, 'initializing');
      startInitialization = true;
    }
    if (backend.phase === 'initializing') {
      const observation = await ctx.sessionQuery.observeSession(backend.sessionId, { signal, projectionMode: 'none' });
      let receipt;
      try {
        receipt = receiptFromEvents(observation.events, backend.initID, observation.inheritedEventCount);
      } finally {
        observation[Symbol.dispose]();
      }
      if (receipt.stage === 'completed') {
        backend = await this.setPhase(clientID, backend.sessionId, 'ready');
      } else if (receipt.stage === 'failed') {
        backend = await this.setPhase(clientID, backend.sessionId, 'failed', receipt.reason);
      } else if (receipt.stage === 'not-found') {
        if (startInitialization) {
          const binding = client.binding;
          const text = this.template.replaceAll('{{BRIDGE_URL}}', binding.url).replaceAll('{{BRIDGE_TOKEN}}', binding.token);
          await ctx.sessionController.prompt({
            sessionId: backend.sessionId, requestId: backend.initID, mode: 'queue',
            content: [{ type: 'text', text }],
          }, signal);
        } else {
          const agent = ctx.agents?.get(backend.sessionId);
          // Claimed input can briefly leave the inbox before its event is
          // appended. A cold idle session with no evidence needs review.
          if (agent?.status !== 'running' && !queuedInAgent(agent, backend.initID)) {
            backend = await this.setPhase(clientID, backend.sessionId, 'failed', 'initialization-record-unavailable');
          }
        }
      }
    }
    return backend;
  }

  register(request, signal) {
    return this.serial(async () => {
      const next = structuredClone(this.state);
      const binding = { url: request.url, token: request.token, workspace: request.workspace ?? '' };
      next.clients[request.clientID] ??= { backends: [], active: '' };
      const client = next.clients[request.clientID];
      if (JSON.stringify(client.binding) !== JSON.stringify(binding)) {
        client.binding = binding;
        await this.save(next);
      }
      const backend = await this.ensure(request.clientID, signal);
      const list = await this.context().sessionController.list({}, signal);
      const row = list.items.find(item => item.sessionId === backend.sessionId);
      if (!row) throw failure('Managed DSH session is unavailable');
      return { items: [{ ...row, running: row.running || backend.phase !== 'ready' }], backend: this.describe(backend) };
    });
  }

  prepare(clientID, requestID, signal) {
    return this.serial(async () => {
      const key = clientID + '/' + requestID;
      const existing = this.state.assignments[key];
      if (existing) return { sessionId: existing };
      const backend = await this.ensure(clientID, signal);
      if (backend.phase !== 'ready') throw failure('Backend initialization has not completed');
      const next = structuredClone(this.state);
      next.assignments[key] = backend.sessionId;
      await this.save(next);
      return { sessionId: backend.sessionId };
    });
  }

  assigned(clientID, requestID) {
    return this.serial(() => {
      const sessionId = this.state.assignments[clientID + '/' + requestID];
      if (!sessionId) throw failure('Message has not been assigned to a backend');
      return sessionId;
    });
  }

  change(clientID, sessionId, signal) {
    return this.serial(async () => {
      const client = this.state.clients[clientID];
      if (!client) throw failure('TodoList bridge is not registered');
      if (sessionId) {
        const target = client.backends.find(item => item.sessionId === sessionId);
        if (!target || target.promptHash !== this.promptHash || target.bindingHash !== hash(JSON.stringify(client.binding))) {
          throw failure('The selected backend uses an older initialization prompt or bridge');
        }
        const next = structuredClone(this.state);
        next.clients[clientID].active = sessionId;
        await this.save(next);
      } else {
        await this.create(clientID);
      }
      return this.describe(await this.ensure(clientID, signal));
    });
  }

  describe(backend) {
    return { sessionId: backend.sessionId, phase: backend.phase, reason: backend.reason,
      promptHash: backend.promptHash, createdAt: backend.createdAt };
  }

  view() {
    return { promptHash: this.promptHash, clients: Object.entries(this.state.clients).map(([id, client]) => ({
      id, active: client.active, backends: client.backends.map(backend => ({
        ...this.describe(backend), compatible: backend.promptHash === this.promptHash &&
          backend.bindingHash === hash(JSON.stringify(client.binding)),
      })),
    })) };
  }
}
