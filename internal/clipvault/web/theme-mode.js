// 同步设置 data-theme-mode：仅在主题未固定（system 模式）时按系统偏好补齐。
// 该脚本以普通 <script src> 同步引入，在首帧绘制前执行；固定模式下服务端已在
// <html> 上输出 data-theme-mode，此处不覆盖。
if (!document.documentElement.dataset.themeMode) {
  document.documentElement.dataset.themeMode =
    window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}
