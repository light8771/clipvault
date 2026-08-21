// i18n.js — 前端翻译模块。
// 字典由服务端按回退链合成后经 /i18n-dict.js 注入（页面 head 中先于
// bootstrap 模块加载），t() 为纯查表：缺失词条回退 Key 原文并打 Debug
// 日志；{name} 参数缺失时保留占位符。渲染一律 textContent（纯文本契约）。

(function () {
  'use strict';

  var state = (typeof window !== 'undefined' && window.__clipvaultI18n) || { lang: 'zh-CN', dict: {} };

  function t(key, params) {
    var text = state.dict[key];
    if (text === undefined) {
      if (typeof console !== 'undefined' && console.debug) console.debug('i18n key missing: ' + key);
      return key;
    }
    if (params && text.indexOf('{') >= 0) {
      text = text.replace(/\{([^{}]+)\}/g, function (match, name) {
        return Object.prototype.hasOwnProperty.call(params, name) ? String(params[name]) : match;
      });
    }
    return text;
  }

  function locale() {
    return state.lang || 'zh-CN';
  }

  // localeNumber 数字本地化：当前语言映射 Intl locale（zh-CN → zh，其余 → en）。
  function localeNumber(value) {
    try {
      var tag = locale().indexOf('zh') === 0 ? 'zh' : 'en';
      return Number(value).toLocaleString(tag);
    } catch (err) {
      return String(value);
    }
  }

  window.clipvaultI18n = { t: t, locale: locale, localeNumber: localeNumber };
})();
