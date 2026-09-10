import { app } from './app-context.js';

const t = (key, params) => window.clipvaultI18n.t(key, params);

export async function downloadProtectedFile(url, fallbackFilename, requestInit) {
  try {
    const response = await app.protectedFetch(url, requestInit);
    if (!response.ok) {
      const failure = await app.readAPIError(response);
      app.toast(failure.message, 'err');
      if (response.status === 402) app.openLicenseSettings();
      return false;
    }
    // Finish only after receiving the entire file, not just its headers.
    const blob = await response.blob();
    const disposition = response.headers.get('Content-Disposition') || '';
    const filename = disposition.match(/filename="([^"]+)"/)?.[1] || fallbackFilename;
    const link = document.createElement('a');
    const objectURL = URL.createObjectURL(blob);
    try {
      link.href = objectURL;
      link.download = filename;
      document.body.appendChild(link);
      link.click();
    } finally {
      link.remove();
      // Give the browser time to take ownership of the download.
      window.setTimeout(() => URL.revokeObjectURL(objectURL), 60000);
    }
    return true;
  } catch (err) {
    app.toast(err.message || t('transfer.export_network_error'), 'err');
    return false;
  }
}
