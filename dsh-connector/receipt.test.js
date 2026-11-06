import assert from 'node:assert/strict';
import { test } from 'node:test';
import { receiptFromEvents } from './receipt.js';

function event(type, data, time = 1) {
  return { type, data, time };
}

test('recovers a legacy message and the exact turn after restart', () => {
  const history = [
    event('turn/start', { turn: 7 }),
    event('user/message', { content: [{ type: 'text', text: 'Legacy\n<!-- todo-msg:old -->' }] }, 2),
    event('tool/call', { turn: 7, step: 0, callId: 'call', name: 'read', arguments: 'private' }, 3),
    event('tool/result', { turn: 7, step: 0, message: { toolCallId: 'call', isError: true }, error: { code: 'NOT_FOUND' } }, 4),
    event('turn/end', { turn: 7, reason: { kind: 'completed' } }, 5),
    event('turn/start', { turn: 8 }),
    event('user/message', { source: { kind: 'user', rpcId: 'different' }, content: [] }),
    event('turn/end', { turn: 8, reason: { kind: 'error' } }),
  ];
  assert.deepEqual(receiptFromEvents(history, 'old'), {
    stage: 'completed', turn: 7, acceptedAt: 2, finishedAt: 5, reason: 'completed',
    tools: [{ name: 'read', status: 'failed', code: 'NOT_FOUND', startedAt: 3, finishedAt: 4 }],
  });
  assert.deepEqual(receiptFromEvents(history, 'old', 5), { stage: 'not-found' });
});

test('matches request identity, reports running and handles a crash closure', () => {
  const history = [
    event('turn/start', { turn: 1 }),
    event('user/message', { source: { kind: 'user', rpcId: 'one' }, content: [] }),
    event('user/message', { source: { kind: 'inject' }, content: [{ type: 'text', text: '<!-- todo-msg:fake -->' }] }),
    event('tool/call', { turn: 1, step: 0, callId: 'unfinished', name: 'write' }, 3),
  ];
  assert.equal(receiptFromEvents(history, 'one').stage, 'running');
  assert.equal(receiptFromEvents(history, 'fake').stage, 'not-found');
  history.push(event('turn/end', { turn: 1, reason: { kind: 'interrupted' } }, 4));
  const receipt = receiptFromEvents(history, 'one');
  assert.equal(receipt.stage, 'failed');
  assert.equal(receipt.reason, 'interrupted');
  assert.equal(receipt.tools[0].status, 'unconfirmed');
});
