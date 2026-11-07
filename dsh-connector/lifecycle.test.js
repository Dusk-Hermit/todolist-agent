import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { Context } from '@deepseek-ai/cordis';
import * as plugin from './index.js';

async function listen(port = 0) {
  const server = createServer();
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(port, '127.0.0.1', resolve);
  });
  return server;
}

const close = server => new Promise(resolve => server.close(resolve));

async function readDiscovery(path) {
  for (let attempt = 0; attempt < 200; attempt++) {
    try { return JSON.parse(await readFile(path, 'utf8')); } catch {}
    await new Promise(resolve => setTimeout(resolve, 5));
  }
  throw new Error('Connector did not publish discovery');
}

test('Cordis unload releases listeners and recovers an occupied legacy port', { timeout: 10000 }, async t => {
  const directory = await mkdtemp(join(tmpdir(), 'todolist-lifecycle-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const discoveryFile = join(directory, 'connector.json');
  const root = new Context();
  const reservation = await listen();
  const port = reservation.address().port;
  await close(reservation);
  let fiber;
  t.after(async () => { if (fiber) await fiber.dispose(); });
  for (let cycle = 0; cycle < 5; cycle++) {
    await rm(discoveryFile, { force: true });
    fiber = root.plugin(plugin, { port, discoveryFile });
    await fiber;
    assert.equal((await readDiscovery(discoveryFile)).port, port);
    await fiber.dispose();
    await close(await listen(port));
  }
  const zombie = await listen(port);
  t.after(() => close(zombie));
  await rm(discoveryFile, { force: true });
  fiber = root.plugin(plugin, { port, discoveryFile });
  await fiber;
  const fallback = await readDiscovery(discoveryFile);
  assert.notEqual(fallback.port, port);
  const response = await fetch('http://127.0.0.1:' + fallback.port + '/todolist/v1/rpc', {
    method: 'POST', headers: { 'Content-Type': 'application/octet-stream' }, body: 'invalid',
  });
  assert.equal(response.status, 401, 'the actual port must pass Host validation');
  await fiber.dispose();
  await close(await listen(fallback.port));

  // Unload immediately after activation, while publication may still be
  // awaiting filesystem work. A following activation must acquire the port.
  await close(zombie);
  fiber = root.plugin(plugin, { port, discoveryFile });
  await fiber;
  await fiber.dispose();
  await close(await listen(port));
});
