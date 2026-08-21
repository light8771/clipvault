const nativeFetch = window.fetch.bind(window);
let csrfTokenPromise = null;

const t = window.clipvaultI18n.t;

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
  if (envelope?.error && typeof envelope.error.code === 'string' && typeof envelope.error.message === 'string') {
    return { ok: false, code: envelope.error.code, error: envelope.error.message };
  }
  if (Object.prototype.hasOwnProperty.call(envelope || {}, 'data')) return envelope.data;
  throw new APIError('invalid_response', t('apijs.invalid_response'), 500);
}

export async function readAPIError(response, fallback) {
  let payload = {};
  try {
    payload = unwrapAPIEnvelope(await response.clone().json());
  } catch (_) {}
  return {
    code: payload?.code || 'request_failed',
    message: payload?.error || fallback || t('apijs.request_failed'),
    status: response.status,
  };
}

export async function requestWithSecondaryAuth(send, authenticate, onExpired, options = {}) {
  const fresh = options.fresh === true;
  for (let attempt = 0; attempt < 2; attempt++) {
    if (!await authenticate(fresh || attempt > 0)) {
      throw new APIError('secondary_cancelled', t('apijs.secondary_cancelled'), 403);
    }
    const response = await send();
    if (response.status !== 403) return response;
    const failure = await readAPIError(response, t('apijs.secondary_required'));
    const expired = failure.code === 'secondary_required' || (fresh && failure.code === 'fresh_secondary_required');
    if (!expired) return response;
    onExpired();
  }
  throw new APIError('secondary_required', t('apijs.secondary_required'), 403);
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
