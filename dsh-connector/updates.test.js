import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { test } from 'node:test';
import { checkUpdate, downloadUpdate, selectUpdate } from './updates.js';

const bytes = Buffer.from('fixture archive');
const digest = 'sha256:' + createHash('sha256').update(bytes).digest('hex');
const name = 'todolist-agent-dsh-connector-0.4.15.tgz';
const release = { tag_name: 'v0.4.15', assets: [{ name, digest, size: bytes.length }] };

test('uses the direct manifest and constructs its own versioned download URL', async () => {
  const offer = await checkUpdate('0.4.14', async url => {
    assert.match(url.href, /github\.com\/Dusk-Hermit\/todolist-agent\/releases\/latest\/download\//);
    return Response.json(release);
  });
  assert.equal(offer.url, 'https://github.com/Dusk-Hermit/todolist-agent/releases/download/v0.4.15/' + name);
  assert.equal(selectUpdate(release, '0.4.15'), null);
  assert.throws(() => selectUpdate({ ...release, prerelease: true }, '0.4.14'));
  assert.throws(() => selectUpdate({ ...release, assets: [] }, '0.4.14'));
});

test('uses the API as fallback and verifies the downloaded bytes', async () => {
  const offer = await checkUpdate('0.4.14', async url =>
    url.hostname === 'github.com' ? new Response(null, { status: 403 }) : Response.json(release));
  assert.deepEqual(await downloadUpdate(offer, async () => new Response(bytes)), bytes);
  await assert.rejects(downloadUpdate(offer, async () => new Response(Buffer.alloc(bytes.length))), /SHA-256/);
  await assert.rejects(downloadUpdate(offer, async () => Response.redirect('https://example.org')), /Unexpected GitHub/);
});
