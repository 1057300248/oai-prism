// Read-only metadata observer for a dedicated, already-authorized browser.
// It does not log in, copy cookies, invoke a model, or bypass access controls.
import {parseArgs} from 'node:util';
import {writeFile, rename, mkdir, unlink, readFile, stat} from 'node:fs/promises';
import {resolve, dirname} from 'node:path';
import {pathToFileURL} from 'node:url';
import {randomUUID} from 'node:crypto';
import {findPrismModels, atPointer, normalizeModels} from './normalize.mjs';

const {values} = parseArgs({options: {
  cdp: {type:'string'}, 'page-origin': {type:'string',default:'https://prism.openai.com'},
  'response-prefix': {type:'string'}, pointer: {type:'string'}, output: {type:'string'},
  'playwright-module': {type:'string'}, 'from-json': {type:'string'},
}});
if (!values.output) throw new Error('--output is required');
const output = resolve(values.output);
const MAX = 2 * 1024 * 1024;
let saving = Promise.resolve();
function select(value) {
  if (values.pointer !== undefined) return atPointer(value, values.pointer);
  const found = findPrismModels(value);
  if (found.length !== 1) throw new Error('Exactly one prism_codex_models configuration is required; otherwise set --pointer explicitly');
  return found[0];
}
async function save(value, observedAt) {
  const normalized = normalizeModels(select(value), observedAt);
  const body = JSON.stringify(normalized, null, 2) + '\n';
  if (Buffer.byteLength(body) > MAX) throw new Error('Normalized model catalog too large');
  saving = saving.catch(() => {}).then(async () => {
    await mkdir(dirname(output), {recursive:true, mode:0o700});
    const temporary = output + '.' + randomUUID() + '.tmp';
    try { await writeFile(temporary, body, {mode:0o600, flag:'wx'}); await rename(temporary, output); }
    finally { await unlink(temporary).catch(() => {}); }
  });
  await saving;
  console.log(JSON.stringify({event:'model_metadata_updated', count:normalized.models.length, observed_at:normalized.observed_at}));
}
if (values['from-json']) {
  const file = resolve(values['from-json']);
  const info = await stat(file);
  if (!info.isFile() || info.size > MAX) throw new Error('Input metadata must be a bounded regular file');
  // Re-importing an old capture MUST NOT make its evidence appear fresh.
  await save(JSON.parse(await readFile(file, 'utf8')), info.mtime);
} else {
  if (!values.cdp || !values['response-prefix']) throw new Error('Live observation requires --cdp and an explicitly selected --response-prefix');
  const endpoint = new URL(values.cdp);
  if (!['http:', 'ws:'].includes(endpoint.protocol) || !['127.0.0.1', '[::1]'].includes(endpoint.hostname) || endpoint.username || endpoint.password) throw new Error('Only literal loopback CDP endpoints are accepted');
  const prefix = new URL(values['response-prefix']);
  if (prefix.protocol !== 'https:' || prefix.username || prefix.password || prefix.hash) throw new Error('Metadata response prefix must be HTTPS without embedded credentials');
  const origin = new URL(values['page-origin']).origin;
  const module = values['playwright-module'] ? await import(pathToFileURL(resolve(values['playwright-module'])).href) : await import('playwright-core');
  const browser = await module.chromium.connectOverCDP(endpoint.href, {timeout:10000});
  const pages = browser.contexts().flatMap(context => context.pages()).filter(page => {
    try { return new URL(page.url()).origin === origin; } catch { return false; }
  });
  if (pages.length !== 1) throw new Error('Use a dedicated per-account browser with exactly one matching page; ambiguous sessions are not auto-selected');
  let inflight = 0;
  const page = pages[0];
  page.on('response', async response => {
    if (inflight >= 2) return;
    let url;
    try { url = new URL(response.url()); } catch { return; }
    if (url.origin !== prefix.origin || !url.pathname.startsWith(prefix.pathname) || response.status() !== 200) return;
    const headers = response.headers();
    if (!headers['content-type']?.includes('json')) return;
    const length = Number(headers['content-length']);
    // Playwright body() is buffered. Skip unknown/oversized lengths rather than
    // allow an arbitrary browser response to consume unbounded observer memory.
    if (!Number.isSafeInteger(length) || length <= 0 || length > MAX) return;
    inflight++;
    try {
      const body = await response.body();
      if (body.length > MAX) throw new Error('metadata body exceeds limit');
      await save(JSON.parse(body.toString('utf8')), new Date());
    } catch {
      // No raw response, URL query, error body, cookie or credential is logged.
      console.error(JSON.stringify({event:'model_metadata_rejected', last_good_preserved:true}));
    } finally { inflight--; }
  });
  console.log(JSON.stringify({event:'observer_ready', behavior:'read-only; reload the authorized page normally to observe its next metadata response'}));
  // SIGINT exits this observer connection only. It must not close the user's browser.
  await new Promise(resolve => {process.once('SIGINT',resolve);process.once('SIGTERM',resolve);browser.once('disconnected',resolve);page.once('close',resolve);});
  await saving.catch(() => {});
  process.exit(0);
}
