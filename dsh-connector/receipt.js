function isRequest(message, id) {
  return message?.source?.kind === 'user' && message.source.rpcId === id;
}

export function queuedInAgent(agent, id) {
  return agent && [...agent.inbox.nextTurn, ...agent.inbox.nextStep].some(message => isRequest(message, id));
}

// Read the exact Session cut. A fork's inherited history belongs to its source.
export function receiptFromEvents(events, id, inheritedCount = 0) {
  const marker = '<!-- todo-msg:' + id + ' -->';
  let messageIndex = events.length - 1;
  for (; messageIndex >= inheritedCount; messageIndex--) {
    const event = events[messageIndex];
    if (event.type !== 'user/message' || (event.surfaceOp && event.surfaceOp !== 'append')) continue;
    const message = event.data;
    if (isRequest(message, id) || ((!message.source || message.source.kind === 'user') &&
        message.content.some(part => part.type === 'text' && part.text.trimEnd().endsWith(marker)))) break;
  }
  if (messageIndex < inheritedCount) return { stage: 'not-found' };

  const acceptedAt = events[messageIndex].time;
  let turn;
  for (let i = messageIndex - 1; i >= inheritedCount; i--) {
    const event = events[i];
    if (event.type === 'turn/end') break;
    if (event.type === 'turn/start') {
      turn = event.data.turn;
      break;
    }
  }
  if (turn === undefined) return { stage: 'accepted', acceptedAt };

  const calls = new Map();
  const receipt = { stage: 'running', acceptedAt, turn, tools: [] };
  for (let i = messageIndex + 1; i < events.length; i++) {
    const { type, data, time } = events[i];
    if (type === 'turn/start') break;
    if (data.turn !== turn) continue;
    if (type === 'tool/call') {
      const call = { name: data.name, status: 'running', startedAt: time };
      calls.set(data.step + ':' + data.callId, call);
      receipt.tools.push(call);
    } else if (type === 'tool/result') {
      const call = calls.get(data.step + ':' + data.message.toolCallId);
      if (call) {
        call.status = data.message.isError ? 'failed' : 'succeeded';
        call.finishedAt = time;
        if (data.error?.code) call.code = data.error.code;
      }
    } else if (type === 'assistant/message') {
      const text = data.message.content.filter(part => part.type === 'text').map(part => part.text).join('');
      if (text) receipt.response = text.slice(0, 12000);
    } else if (type === 'turn/end') {
      receipt.reason = data.reason.kind;
      receipt.stage = receipt.reason === 'completed' ? 'completed' : 'failed';
      receipt.finishedAt = time;
      for (const call of receipt.tools) {
        if (call.status === 'running') call.status = 'unconfirmed';
      }
      break;
    }
  }
  return receipt;
}
