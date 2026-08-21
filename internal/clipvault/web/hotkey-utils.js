export function isSupportedVirtualKey(vk) {
    return vk === 0x20 ||
      (vk >= 0x30 && vk <= 0x39) ||
      (vk >= 0x41 && vk <= 0x5A) ||
      (vk >= 0x70 && vk <= 0x87);
  }

export function vkToDisplay(vk) {
    if (!vk) return '';
    if (vk === 0x20) return 'Space';
    if (vk >= 0x70 && vk <= 0x87) return 'F' + (vk - 0x70 + 1);
    if ((vk >= 0x30 && vk <= 0x39) || (vk >= 0x41 && vk <= 0x5A)) return String.fromCharCode(vk);
    return '';
  }

  // 0 means an intentionally empty input; null means non-empty but invalid.
export function parseVkInput(value) {
    const input = (value || '').trim().toUpperCase();
    if (!input) return 0;
    // "空格" 是中文用户对 Space 键的习惯输入，作为按键别名识别（非 UI 文案）。
    if (input === 'SPACE' || input === '\u7A7A\u683C') return 0x20;
    if (/^[A-Z0-9]$/.test(input)) return input.charCodeAt(0);
    const functionKey = /^F([1-9]|1[0-9]|2[0-4])$/.exec(input);
    if (functionKey) return 0x70 + Number(functionKey[1]) - 1;
    return null;
  }
