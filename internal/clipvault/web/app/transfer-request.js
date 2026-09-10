// Headers only accept byte strings. Encode the same UTF-8 password bytes that
// encrypted export receives in JSON, including spaces and non-ASCII characters.
export function importRequestHeaders(format, password) {
  const headers = { 'X-ClipVault-Import-Format': format };
  if (format === 'cvexport') {
    const bytes = new TextEncoder().encode(password);
    headers['X-ClipVault-Import-Password-Base64'] = btoa(Array.from(bytes, byte => String.fromCharCode(byte)).join(''));
  }
  return headers;
}
