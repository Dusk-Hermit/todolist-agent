import { createHash, randomUUID } from 'node:crypto';
import { execFile } from 'node:child_process';
import { mkdir, rename, unlink, writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { promisify } from 'node:util';

const repository = 'Dusk-Hermit/todolist-agent';
const releaseRoot = 'https://github.com/' + repository + '/releases';
const manifestURL = releaseRoot + '/latest/download/TodoListAgent-update.json';
const apiURL = 'https://api.github.com/repos/' + repository + '/releases/latest';
const packageName = 'todolist-agent-dsh-connector';
const trustedHosts = new Set(['github.com', 'api.github.com', 'release-assets.githubusercontent.com', 'objects.githubusercontent.com']);
const versionPattern = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;
const maxPackage = 4 * 1024 * 1024;
const runFile = promisify(execFile);

// Use Windows' system proxy for the same allowlisted GitHub URLs. The DSH
// Node runtime does not necessarily inherit that proxy.
const windowsDownloadScript = `
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Net.Http
$handler = New-Object System.Net.Http.HttpClientHandler
$handler.AllowAutoRedirect = $false
$client = New-Object System.Net.Http.HttpClient($handler)
$client.Timeout = [TimeSpan]::FromMilliseconds([int]$env:TODO_CONNECTOR_HTTP_TIMEOUT)
$target = [Uri]$env:TODO_CONNECTOR_HTTP_URL
$limit = [int]$env:TODO_CONNECTOR_HTTP_LIMIT
$allowed = @('github.com','api.github.com','release-assets.githubusercontent.com','objects.githubusercontent.com')
$buffer = New-Object byte[] 8192
$output = [Console]::OpenStandardOutput()
try {
  for ($hop = 0; $hop -lt 5; $hop++) {
    if ($target.Scheme -ne 'https' -or $target.UserInfo -or !$target.IsDefaultPort -or $target.Host -notin $allowed) {
      throw 'Unexpected GitHub download host'
    }
    $response = $client.GetAsync($target, [Net.Http.HttpCompletionOption]::ResponseHeadersRead).GetAwaiter().GetResult()
    try {
      $status = [int]$response.StatusCode
      if ($status -ge 300 -and $status -lt 400 -and $response.Headers.Location) {
        $target = [Uri]::new($target, $response.Headers.Location)
        continue
      }
      if (!$response.IsSuccessStatusCode) { throw "GitHub HTTP $status" }
      $stream = $response.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
      $total = 0
      while (($read = $stream.Read($buffer, 0, $buffer.Length)) -gt 0) {
        $total += $read
        if ($total -gt $limit) { throw 'GitHub download exceeded the size limit' }
        $output.Write($buffer, 0, $read)
      }
      return
    } finally { $response.Dispose() }
  }
  throw 'Too many GitHub redirects'
} finally { $client.Dispose() }
`;
const windowsDownloadCommand = Buffer.from(windowsDownloadScript, 'utf16le').toString('base64');

export async function hostImport(profile, specifier) {
  const require = createRequire(profile.installAnchor);
  return import(pathToFileURL(require.resolve(specifier)).href);
}

async function download(url, limit, timeout, fetcher) {
  const signal = AbortSignal.timeout(timeout);
  for (let hop = 0; hop < 5; hop++) {
    const target = new URL(url);
    if (target.protocol !== 'https:' || !trustedHosts.has(target.host) || target.username || target.password) {
      throw new Error('Unexpected GitHub download host');
    }
    const response = await fetcher(target, { redirect: 'manual', signal });
    if (response.status >= 300 && response.status < 400) {
      const next = response.headers.get('location');
      await response.body?.cancel();
      if (!next) throw new Error('Invalid GitHub redirect');
      url = new URL(next, target).href;
      continue;
    }
    if (!response.ok || !response.body) {
      await response.body?.cancel();
      throw new Error('GitHub HTTP ' + response.status);
    }
    const chunks = [];
    let bytes = 0;
    for await (const chunk of response.body) {
      bytes += chunk.length;
      if (bytes > limit) throw new Error('GitHub download exceeded the size limit');
      chunks.push(chunk);
    }
    return Buffer.concat(chunks);
  }
  throw new Error('Too many GitHub redirects');
}

async function releaseDownload(url, limit, timeout, fetcher) {
  if (process.platform === 'win32' && fetcher === fetch) {
    try {
      const { stdout } = await runFile(
        join(process.env.SystemRoot ?? 'C:\\Windows', 'System32', 'WindowsPowerShell', 'v1.0', 'powershell.exe'),
        ['-NoProfile', '-NonInteractive', '-EncodedCommand', windowsDownloadCommand],
        { windowsHide: true, encoding: 'buffer', timeout, maxBuffer: limit + 8192,
          env: { ...process.env, TODO_CONNECTOR_HTTP_URL: url, TODO_CONNECTOR_HTTP_LIMIT: String(limit),
            TODO_CONNECTOR_HTTP_TIMEOUT: String(timeout) } },
      );
      return stdout;
    } catch {
      // The DSH runtime can have its own proxy configuration.
    }
  }
  return download(url, limit, timeout, fetcher);
}

export function selectUpdate(release, current) {
  const version = release?.tag_name?.startsWith?.('v') ? release.tag_name.slice(1) : '';
  if (!versionPattern.test(version) || release.draft || release.prerelease) throw new Error('Invalid stable release');
  const installed = current.split('.').map(Number);
  const difference = version.split('.').map((value, index) => Number(value) - installed[index]).find(value => value !== 0);
  if (!(difference > 0)) return null;
  const name = packageName + '-' + version + '.tgz';
  const asset = release.assets?.find(asset => asset.name === name);
  if (!asset || !Number.isSafeInteger(asset.size) || asset.size < 1 || asset.size > maxPackage ||
      !/^sha256:[0-9a-f]{64}$/i.test(asset.digest)) throw new Error('Release is missing the verified connector package');
  return { version, name, size: asset.size, digest: asset.digest.toLowerCase(),
    url: releaseRoot + '/download/v' + version + '/' + name };
}

export async function checkUpdate(current, fetcher = fetch) {
  try {
    return selectUpdate(JSON.parse(await releaseDownload(manifestURL, 64 * 1024, 7000, fetcher)), current);
  } catch (directError) {
    try {
      return selectUpdate(JSON.parse(await releaseDownload(apiURL, 512 * 1024, 7000, fetcher)), current);
    } catch (error) {
      throw new Error('Release direct link: ' + directError.message + ' · GitHub API: ' + error.message);
    }
  }
}

export async function downloadUpdate(offer, fetcher = fetch) {
  const bytes = await releaseDownload(offer.url, offer.size, 30000, fetcher);
  if (bytes.length !== offer.size || 'sha256:' + createHash('sha256').update(bytes).digest('hex') !== offer.digest) {
    throw new Error('Connector package failed SHA-256 or size verification');
  }
  return bytes;
}

export async function installUpdate(profile, offer) {
  const { runPluginCommand } = await hostImport(profile, '@deepseek-ai/dsh-plugin-manager/operations');
  const bytes = await downloadUpdate(offer);
  const dir = join(profile.dir, 'todolist-connector-releases');
  const path = join(dir, offer.name);
  const temp = path + '.' + randomUUID() + '.tmp';
  await mkdir(dir, { recursive: true, mode: 0o700 });
  try {
    await writeFile(temp, bytes, { mode: 0o600, flag: 'wx' });
    await rename(temp, path);
  } finally {
    await unlink(temp).catch(() => {});
  }
  // Keep the archive: pnpm records its path in the profile's dependency.
  const result = await runPluginCommand({ ...profile, profile: profile.name }, ['add', path, '--ignore-scripts'], {
    ...profile.packageManager, execution: 'service', outputBytes: 8192, lockWaitMs: 30000,
    idleTimeoutMs: 45000, signal: AbortSignal.timeout(120000),
  });
  if (result.exitCode !== 0) throw new Error('DSH could not install the connector (exit ' + result.exitCode + ')');
}
