const nativeFetch = window.fetch.bind(window);
let csrfTokenPromise = null;

export class APIError extends Error {
  constructor(code, message, status) {
    super(message);
    this.name = 'APIError';
    this.code = code;
    this.status = status;
  }
}

async function csrfToken(forceRefresh = false) {
  if (forceRefresh) csrfTokenPromise = null;
  if (!csrfTokenPromise) {
    csrfTokenPromise = nativeFetch('/api/v1/session/csrf', { cache: 'no-store' })
      .then(async response => {
        if (!response.ok) throw new APIError('csrf_bootstrap_failed', 'CSRF bootstrap failed', response.status);
        const envelope = await response.json();
        return envelope?.data?.token || '';
      });
  }
  return csrfTokenPromise;
}

export function unwrapAPIEnvelope(envelope) {
  if (envelope?.error) {
    return { ok: false, code: envelope.error.code, error: envelope.error.message };
  }
  return Object.prototype.hasOwnProperty.call(envelope || {}, 'data') ? envelope.data : envelope;
}

export async function handleSecondaryRequired(response, requestSecondaryAuth, onExpired) {
  const failure = await response.json().catch(() => ({}));
  if (response.status !== 403 || failure.code !== 'secondary_required') {
    return { required: false, retry: false, error: failure.error || `HTTP ${response.status}` };
  }
  onExpired();
  return { required: true, retry: await requestSecondaryAuth(), error: failure.error };
}

function decorateJSON(response) {
  const decode = response.json.bind(response);
  response.json = async () => unwrapAPIEnvelope(await decode());
  return response;
}

export async function apiFetch(input, init = {}) {
  const options = { ...init };
  const method = String(options.method || 'GET').toUpperCase();
  const target = typeof input === 'string' ? input : input?.url || '';
  const local = target.startsWith('/') || target.startsWith(window.location.origin);
  if (local && ['POST', 'PUT', 'PATCH', 'DELETE'].includes(method)) {
    const headers = new Headers(options.headers || (typeof input !== 'string' ? input?.headers : undefined));
    headers.set('X-ClipVault-CSRF', await csrfToken());
    options.headers = headers;
  }
  let response = await nativeFetch(input, options);
  if (local && response.status === 403 && response.headers.get('X-ClipVault-CSRF-Invalid') === '1') {
    const headers = new Headers(options.headers || {});
    headers.set('X-ClipVault-CSRF', await csrfToken(true));
    options.headers = headers;
    response = await nativeFetch(input, options);
  }
  return decorateJSON(response);
}

export async function apiJSON(input, init) {
  const response = await apiFetch(input, init);
  const payload = await response.json();
  if (!response.ok || payload?.ok === false) {
    throw new APIError(payload?.code || 'request_failed', payload?.error || response.statusText, response.status);
  }
  return payload;
}

export async function apiDownload(input, init) {
  const response = await apiFetch(input, init);
  if (!response.ok) {
    let payload = null;
    try { payload = await response.json(); } catch (_) {}
    throw new APIError(payload?.code || 'download_failed', payload?.error || response.statusText, response.status);
  }
  return response;
}
