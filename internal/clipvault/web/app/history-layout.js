// ── 历史页布局应用 ────────────────────────────────────────────
// 职责边界（布局重构后的约定）：
//   1. 读取 HistoryPageSettings；
//   2. 解析当前模式；
//   3. 把模式写到 html 的 data 属性与 CSS 变量上。
// 列数阶梯、内容宽度、边距、间距全部由 CSS 消费（见 history.css 的
// PageContainer 与 List 断点体系）；本模块不监听窗口尺寸、不计算
// 「当前宽度应该几列」，也不设置任何卡片最小宽度。
import { app } from './app-context.js';

const DEFAULTS = {
  layout: 'top-bottom',
  columns: 0,
  forceColumns: false,
  contentWidthMode: 'auto',
  contentWidth: 1600,
  horizontalPadding: 24,
  cardGap: 12,
  previewLines: 3,
  fixedPreview: false,
};

function clampInt(value, min, max, fallback) {
  const raw = Number(value);
  if (!Number.isFinite(raw)) return fallback;
  return Math.max(min, Math.min(max, Math.round(raw)));
}

function parseHistoryPageSettings(page) {
  const settings = {
    layout: page?.layout === 'left-right' ? 'left-right' : 'top-bottom',
    columns: clampInt(page?.columns, 0, 4, DEFAULTS.columns),
    forceColumns: page?.forceColumns === true,
    contentWidthMode: page?.contentWidthMode === 'custom' ? 'custom' : 'auto',
    contentWidth: clampInt(page?.contentWidth, 720, 3200, DEFAULTS.contentWidth),
    horizontalPadding: clampInt(page?.horizontalPadding, 0, 80, DEFAULTS.horizontalPadding),
    cardGap: clampInt(page?.cardGap, 0, 32, DEFAULTS.cardGap),
    previewLines: clampInt(page?.previewLines, 0, 20, DEFAULTS.previewLines),
    fixedPreview: page?.fixedPreview === true,
  };
  // 自动列数下强制固定语义不适用（与后端 normalize 规则一致）。
  if (settings.columns === 0) settings.forceColumns = false;
  // 不限制行数时固定占位语义不适用（与后端 normalize 规则一致）。
  if (settings.previewLines === 0) settings.fixedPreview = false;
  return settings;
}

// applyHistoryPageLayout(page) 把历史页设置翻译为页面状态：
//   html[data-history-layout]        top-bottom | left-right
//   html[data-history-columns]       0（自动）| 1-4（最大 N 列）
//   html[data-history-columns-force] 存在时强制固定列数（CSS 无断点、不降级）
//   --history-content-max-width      auto → none；custom → Npx
//   --history-horizontal-padding     页面左右边距
//   --history-card-gap               卡片横向/纵向间距
//   --preview-max-h                  文本预览行数对应的最大高度
//   --preview-min-h                  固定占位高度（开启时同 --preview-max-h，否则 0）
function applyHistoryPageLayout(page) {
  const settings = parseHistoryPageSettings(page);
  const root = document.documentElement;
  root.dataset.historyLayout = settings.layout;
  root.dataset.historyColumns = String(settings.columns);
  if (settings.forceColumns && settings.columns > 0) {
    root.dataset.historyColumnsForce = String(settings.columns);
  } else {
    delete root.dataset.historyColumnsForce;
  }
  root.style.setProperty('--history-content-max-width',
    settings.contentWidthMode === 'custom' ? `${settings.contentWidth}px` : 'none');
  root.style.setProperty('--history-horizontal-padding', `${settings.horizontalPadding}px`);
  root.style.setProperty('--history-card-gap', `${settings.cardGap}px`);
  const previewMaxH = settings.previewLines > 0 ? `${settings.previewLines * 1.7}em` : 'none';
  const previewMinH = settings.fixedPreview ? previewMaxH : '0em';
  root.style.setProperty('--preview-max-h', previewMaxH);
  root.style.setProperty('--preview-min-h', previewMinH);
  // 文本截断工具在渲染卡片时读取行数（0 为不限制）。
  app.previewLines = settings.previewLines;
  app.historyPageSettings = settings;
  return settings;
}

export function registerHistoryLayout() {
  app.applyHistoryPageLayout = applyHistoryPageLayout;
}
