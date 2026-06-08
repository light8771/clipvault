"""生成与托盘图标一致的 ClipVault 应用图标"""
from PIL import Image, ImageDraw


def draw_clipboard_icon(size: int) -> Image.Image:
    """绘制剪贴板图标，返回 RGBA Image"""
    img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    s = size / 32.0  # 缩放因子

    def r(x, y):
        """将 32x32 坐标缩放到目标尺寸，返回 (x, y) 元组"""
        return (round(x * s), round(y * s))

    def rect(x0, y0, x1, y1, fill):
        d.rectangle([r(x0, y0), r(x1, y1)], fill=fill)

    def hline(x0, x1, y, fill, width=1):
        p0 = r(x0, y)
        p1 = r(x1, y)
        d.rectangle([p0, (p1[0], p1[1] + max(1, round(width * s) - 1))], fill=fill)

    def vline(y0, y1, x, fill, width=1):
        p0 = r(x, y0)
        p1 = r(x, y1)
        d.rectangle([p0, (p0[0] + max(1, round(width * s) - 1), p1[1])], fill=fill)

    # 颜色
    board_fill = (245, 245, 245, 255)
    board_border = (85, 85, 85, 255)
    clip_fill = (85, 153, 238, 255)
    clip_border = (34, 102, 187, 255)
    hole_fill = (221, 238, 255, 255)
    hole_border = (34, 102, 187, 255)
    line1_color = (136, 136, 136, 255)
    line2_color = (170, 170, 170, 255)
    shadow_color = (0, 0, 0, 51)

    # ── 板体 ──
    rect(4, 7, 27, 29, board_fill)
    # 阴影
    for y in range(8, 31):
        px = r(28, y)
        d.point(px, fill=shadow_color)
    for x in range(4, 29):
        px = r(x, 30)
        d.point(px, fill=shadow_color)
    # 边框
    hline(3, 27, 6, board_border)
    hline(3, 27, 29, board_border)
    vline(6, 29, 3, board_border)
    vline(6, 29, 27, board_border)

    # ── 蓝色夹子 ──
    rect(10, 1, 21, 9, clip_fill)
    hline(10, 21, 1, clip_border)
    hline(10, 21, 9, clip_border)
    vline(1, 9, 10, clip_border)
    vline(1, 9, 21, clip_border)

    # 夹子孔
    rect(13, 3, 18, 7, hole_fill)
    hline(13, 18, 3, hole_border)
    hline(13, 18, 7, hole_border)
    vline(3, 7, 13, hole_border)
    vline(3, 7, 18, hole_border)

    # ── 板面线条 ──
    hline(4, 27, 7, board_border)
    hline(5, 26, 8, board_fill)

    # 文字线条
    hline(7, 23, 14, line1_color)
    hline(7, 23, 15, line1_color)
    hline(7, 23, 18, line1_color)
    hline(7, 23, 19, line1_color)
    hline(7, 17, 22, line2_color)
    hline(7, 17, 23, line2_color)

    return img


if __name__ == "__main__":
    import os

    base = os.path.dirname(__file__)
    winres_dir = os.path.join(base, "winres")
    cmd_dir = os.path.join(base, "cmd", "clipvault")
    os.makedirs(winres_dir, exist_ok=True)

    # 主图标 256x256，存放在 cmd/clipvault/ 供 go:embed（托盘图标）和 go-winres（exe 图标）共用
    icon256 = draw_clipboard_icon(256)
    icon256.save(os.path.join(cmd_dir, "icon.png"))

    # 16x16 小图标，仅供 go-winres 使用
    icon16 = draw_clipboard_icon(16)
    icon16.save(os.path.join(winres_dir, "icon16.png"))

    print(f"图标已生成:")
    print(f"  {cmd_dir}/icon.png      = 256x256 (exe+托盘图标)")
    print(f"  {winres_dir}/icon16.png  = 16x16   (exe小图标)")
    print(f"\n修改后运行: go-winres make && go build")
