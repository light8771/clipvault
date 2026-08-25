import { app } from "./app-context.js";

// 历史列表列数运行时（clipvault-history-list-columns-force-ai-spec）。
// preferredColumns（auto/1..4）与 forceColumns 持久化于配置 theme；
// capacityColumns / actualColumns 为运行时派生状态，不持久化。
const MAX_COLUMNS = 4;
// 卡片「舒适宽度」仅用于 capacity 判断，不得写成卡片 CSS min-width：
// 强制固定列数模式下卡片允许被持续压缩（spec §6.2 / §7）。
const CARD_COMFORT_WIDTH = 480;
const LIST_GAP = 10;
// 内容区最大宽度随实际列数联动，1440px 不再是 3/4 列的硬上限（spec §14.1）
const MAIN_MAX_WIDTH = { 1: 900, 2: 1440, 3: 1900, 4: 2400 };

function normalizeColumns(value) {
  return ["auto", "1", "2", "3", "4"].includes(value) ? value : "auto";
}

function normalizeForce(value, columns) {
  // auto 模式由系统自适应列数，强制固定语义无效（spec §2.2）
  return columns !== "auto" && value === true;
}

// 当前可用宽度最多能「舒适」容纳几列（1..4）
function capacityColumns(availableWidth) {
  for (let columns = MAX_COLUMNS; columns >= 2; columns--) {
    if (availableWidth >= columns * CARD_COMFORT_WIDTH + (columns - 1) * LIST_GAP) {
      return columns;
    }
  }
  return 1;
}

function resolveActualColumns(preferred, force, capacity) {
  if (preferred === "auto") return capacity;
  const configured = Number(preferred);
  // 强制模式严格等于用户设置；默认模式为「最大列数」，随容量升降（spec §2）
  return force ? configured : Math.min(configured, capacity);
}

// 外层真实可用内容宽度：视口宽 −（sidebar 布局侧栏让位 270px）− main 水平内边距。
// 刻意不测量 main / .list 自身宽度——它们受 max-width 联动影响，会形成
// actualColumns → max-width → 测量宽度 → capacityColumns 的循环（spec §15）。
function outerAvailableWidth(main) {
  const root = document.documentElement;
  let width = root.clientWidth;
  if (
    root.dataset.historyLayout === "sidebar" &&
    window.matchMedia("(min-width: 769px)").matches
  ) {
    width -= 270;
  }
  const styles = window.getComputedStyle(main);
  width -= (parseFloat(styles.paddingLeft) || 0) + (parseFloat(styles.paddingRight) || 0);
  return width;
}

export function registerHistoryLayout() {
  let lastTheme = null;

  app.applyHistoryLayout = function applyHistoryLayout(theme) {
    const main = document.getElementById("viewHistory");
    if (!main) return; // 非历史页
    lastTheme = theme || null;

    const preferred = normalizeColumns(theme?.historyColumns);
    const force = normalizeForce(theme?.historyForceColumns, preferred);
    let actual;
    if (force) {
      actual = Number(preferred); // 强制模式：不判断宽度，空间不足时压缩卡片
    } else {
      actual = resolveActualColumns(preferred, false, capacityColumns(outerAvailableWidth(main)));
    }
    actual = Math.min(Math.max(actual, 1), MAX_COLUMNS);

    const root = document.documentElement;
    root.style.setProperty("--history-columns", String(actual));
    root.style.setProperty("--history-main-max", `${MAIN_MAX_WIDTH[actual]}px`);
  };

  // 视口尺寸变化（resize / 最大化 / 还原 / 分屏 / 开发者工具）后重算。
  // 监听 documentElement 而不是 .list，避免 max-width 联动的自循环。
  let timer = 0;
  const schedule = () => {
    clearTimeout(timer);
    timer = setTimeout(() => app.applyHistoryLayout(lastTheme), 80);
  };
  if (typeof ResizeObserver === "function") {
    new ResizeObserver(schedule).observe(document.documentElement);
  } else {
    window.addEventListener("resize", schedule);
  }
}
