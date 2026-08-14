import { APIError, apiFetch, readAPIError, requestWithSecondaryAuth } from '../api.js';
import { appStore } from '../store.js';
import * as theme from '../theme-utils.js';
import * as hotkey from '../hotkey-utils.js';

export const app = {
  APIError,
  apiFetch,
  readAPIError,
  requestWithSecondaryAuth,
  appStore,
  scrollBehavior: () => window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth',
  ...theme,
  ...hotkey
};
