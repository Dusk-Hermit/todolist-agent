import { randomBytes, timingSafeEqual } from 'node:crypto';
import { createRequire } from 'node:module';
import { checkUpdate, hostImport, installUpdate } from './updates.js';

const { version } = createRequire(import.meta.url)('./package.json');
const testedDSH = '0.2.2-rc.1';
const endpoint = '/todolist-connector/manage';

export function registerManagement(ctx, info, record) {
  const token = randomBytes(32).toString('hex');
  const auth = Buffer.from('Bearer ' + token);
  let runtime = '';
  let offer;
  let busy = false;
  let staged = '';
  const profile = ctx.profileContext;
  void hostImport(profile, '@deepseek-ai/dsh-app-boot').then(({ getDshRuntimeVersion }) => {
    runtime = getDshRuntimeVersion();
  }).catch(() => record('warning', 'DSH_VERSION_UNAVAILABLE'));
  ctx.on('webserver/index-inject', (table, peer) => {
    if (peer?.principal?.kind === 'owner') table.push({
      kind: 'global', name: '__TodoListConnectorManagement', value: { endpoint, token },
    });
  }, { global: true });
  ctx.effect(() => ctx.webServer.register({
    kind: 'prefix', path: endpoint,
    async handler(req, res) {
      const supplied = Buffer.from(req.headers.authorization ?? '');
      if (supplied.length !== auth.length || !timingSafeEqual(supplied, auth)) {
        res.writeHead(401).end();
        return;
      }
      function send(code, value) {
        res.writeHead(code, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' });
        res.end(JSON.stringify(value));
      }
      if (req.method === 'GET' && req.url === endpoint) {
        send(200, { ...info(), version, runtime, testedDSH, available: offer?.version, staged });
        return;
      }
      if (req.method !== 'POST' || ![endpoint + '/check', endpoint + '/install'].includes(req.url)) {
        send(404, { error: 'Unknown action' });
        return;
      }
      if (busy) {
        send(409, { error: 'Another update action is in progress' });
        return;
      }
      busy = true;
      const checking = req.url.endsWith('/check');
      try {
        if (checking) {
          offer = undefined;
          offer = await checkUpdate(version);
          send(200, { available: offer?.version });
        } else {
          if (!offer) { send(409, { error: 'Check for an update first' }); return; }
          if (staged !== offer.version) await installUpdate(profile, offer);
          staged = offer.version;
          send(200, { staged });
        }
      } catch (error) {
        record('error', checking ? 'UPDATE_CHECK_FAILED' : 'UPDATE_INSTALL_FAILED');
        send(502, { error: error.message });
      } finally {
        busy = false;
      }
    },
  }));
}
