export function clampHistoryPage(currentPage, totalCount, pageSize) {
  const page = Number.isInteger(currentPage) && currentPage > 0 ? currentPage : 0;
  const total = Number.isFinite(totalCount) && totalCount > 0 ? totalCount : 0;
  const size = Number.isFinite(pageSize) && pageSize > 0 ? pageSize : 50;
  const lastPage = Math.max(0, Math.ceil(total / size) - 1);
  return Math.min(page, lastPage);
}
