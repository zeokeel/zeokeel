#!/usr/bin/env python3
"""Render UI mockups for the open-source Grok Bot interaction design.

Output: PNG files next to this script. This is a *visual prototype*, not the
product: every colour/size below is copied from web/src/design/tokens.css
(light theme) so the review matches the real design system.

Run: python3 render_mockups.py
Deps: Pillow >= 10, Noto Sans CJK SC (/usr/share/fonts/opentype/noto).
"""

from __future__ import annotations

import os
from PIL import Image, ImageDraw, ImageFont

# ---------------------------------------------------------------- tokens ----

BG_BASE = "#ffffff"
BG_SIDEBAR = "#f7f8fa"
BG_STRONG = "#f2f4f7"
LABEL_PRIMARY = "#0f1115"
LABEL_SECONDARY = "#4a5160"
LABEL_TERTIARY = "#5a6371"
BORDER_L1 = "#f0f1f2"
BORDER_L2 = "#e9eaec"
BORDER_L3 = "#dfe0e2"
ACCENT = "#0d44f6"
ACCENT_HOVER = "#0946ef"
DANGER = "#dc2626"
DANGER_TEXT = "#b91c1c"
WARNING = "#d97706"
WARNING_TEXT = "#8a4b0f"
SUCCESS = "#16a34a"
SUCCESS_TEXT = "#15803d"

WARN_TINT = "#fdf6ec"
ACCENT_TINT = "#eef2fe"
SUCCESS_TINT = "#eef7f0"
DANGER_TINT = "#fdf0f0"

FONT_REG = "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc"
FONT_BOLD = "/usr/share/fonts/opentype/noto/NotoSansCJK-Bold.ttc"
SC_INDEX = 2  # Simplified Chinese face inside the .ttc

OUT_DIR = os.path.dirname(os.path.abspath(__file__))

# ------------------------------------------------------------------ kit -----


class Canvas:
    def __init__(self, w: int, h: int, bg: str = BG_BASE):
        self.w, self.h = w, h
        self.img = Image.new("RGB", (w, h), bg)
        self.d = ImageDraw.Draw(self.img)
        self._fonts: dict[tuple[int, bool], ImageFont.FreeTypeFont] = {}

    # fonts ---------------------------------------------------------------
    def font(self, size: int, bold: bool = False):
        key = (size, bold)
        if key not in self._fonts:
            path = FONT_BOLD if bold else FONT_REG
            self._fonts[key] = ImageFont.truetype(path, size, index=SC_INDEX)
        return self._fonts[key]

    # primitives ----------------------------------------------------------
    def rect(self, x, y, w, h, fill=None, border=None, r=0):
        if r:
            self.d.rounded_rectangle([x, y, x + w, y + h], radius=r,
                                    fill=fill, outline=border, width=1)
        else:
            self.d.rectangle([x, y, x + w, y + h], fill=fill,
                             outline=border, width=1)

    def hline(self, x, y, w, color=BORDER_L2):
        self.d.line([x, y, x + w, y], fill=color, width=1)

    def vline(self, x, y, h, color=BORDER_L2):
        self.d.line([x, y, x, y + h], fill=color, width=1)

    def dot(self, cx, cy, r, color):
        self.d.ellipse([cx - r, cy - r, cx + r, cy + r], fill=color)

    def text(self, x, y, s, size=14, color=LABEL_PRIMARY, bold=False,
             anchor="la", max_w=None):
        f = self.font(size, bold)
        if max_w is not None:
            s = self._ellipsize(s, f, max_w)
        self.d.text((x, y), s, font=f, fill=color, anchor=anchor)
        return self.text_w(s, size, bold)

    def text_w(self, s, size=14, bold=False):
        return self.d.textlength(s, font=self.font(size, bold))

    def text_block(self, x, y, s, size=13, color=LABEL_PRIMARY, max_w=260,
                   line_h=20, bold=False):
        f = self.font(size, bold)
        line = ""
        for ch in s:
            if ch == "\n":
                self.d.text((x, y), line, font=f, fill=color)
                y += line_h
                line = ""
                continue
            if self.d.textlength(line + ch, font=f) > max_w and line:
                self.d.text((x, y), line, font=f, fill=color)
                y += line_h
                line = ch
            else:
                line += ch
        if line:
            self.d.text((x, y), line, font=f, fill=color)
            y += line_h
        return y

    def _ellipsize(self, s, f, max_w):
        if self.d.textlength(s, font=f) <= max_w:
            return s
        while s and self.d.textlength(s + "…", font=f) > max_w:
            s = s[:-1]
        return s + "…"

    # components ----------------------------------------------------------
    def pill(self, x, y, label, size=12, fill=BG_STRONG, color=LABEL_SECONDARY,
             h=28, icon=None, pad=12):
        tw = self.text_w(label, size)
        w = tw + pad * 2 + (14 if icon else 0)
        self.rect(x, y, w, h, fill=fill, r=h // 2)
        tx = x + pad + (14 if icon else 0)
        if icon:
            self.dot(x + pad + 3, y + h // 2, 3, color)
        self.text(tx, y + h // 2, label, size, color, anchor="lm")
        return w

    def button(self, x, y, w, h, label, kind="outline", size=13):
        if kind == "primary":
            self.rect(x, y, w, h, fill=ACCENT, r=8)
            self.text(x + w / 2, y + h / 2, label, size, "#ffffff", True, "mm")
        elif kind == "outline":
            self.rect(x, y, w, h, fill="#ffffff", border=BORDER_L3, r=8)
            self.text(x + w / 2, y + h / 2, label, size, LABEL_PRIMARY, False, "mm")
        elif kind == "ghost":
            self.text(x + w / 2, y + h / 2, label, size, LABEL_SECONDARY, False, "mm")
        elif kind == "danger-ghost":
            self.text(x + w / 2, y + h / 2, label, size, DANGER_TEXT, False, "mm")
        elif kind == "accent-text":
            self.text(x + w / 2, y + h / 2, label, size, ACCENT, False, "mm")

    def status(self, x, y, label, color, size=12, dot=True):
        if dot:
            self.dot(x + 3, y + 8, 3.5, color)
            self.text(x + 12, y, label, size, color)
            return 12 + self.text_w(label, size)
        return self.text(x, y, label, size, color)


# ------------------------------------------------------------- sidebar ------


def sidebar(c: Canvas, active: str, height: int):
    c.rect(0, 0, 240, height, fill=BG_SIDEBAR)
    c.vline(240, 0, height, BORDER_L2)

    # brand
    c.rect(20, 20, 22, 22, fill=LABEL_PRIMARY, r=6)
    c.text(31, 31, "Z", 13, "#ffffff", True, "mm")
    c.text(52, 22, "ZeoKeel", 15, LABEL_PRIMARY, True, "la")
    c.text(52, 42, "Grok Bot 开源实现", 11, LABEL_TERTIARY, False, "la")

    nav = [("现在", ""), ("等我", "2"), ("在跑", "3"), ("会话", ""), ("设置", "")]
    y = 84
    for label, badge in nav:
        selected = label == active
        if selected:
            c.rect(12, y, 216, 36, fill=ACCENT_TINT, r=8)
        c.text(28, y + 18, label, 14,
               LABEL_PRIMARY if selected else LABEL_SECONDARY,
               selected, "lm")
        if badge:
            bx = 200
            c.text(bx, y + 18, badge, 12,
                   ACCENT if label == "等我" else LABEL_TERTIARY, False, "mm")
        y += 40

    # roster
    c.hline(20, 316, 200)
    c.text(24, 336, "岗位", 11, LABEL_TERTIARY, False, "la")
    bots = [("助理", "空闲", LABEL_TERTIARY),
            ("研究 Bot", "进行中", ACCENT),
            ("数据 Bot", "进行中", ACCENT),
            ("报价 Bot", "掉线", DANGER)]
    y = 360
    for name, state, color in bots:
        c.dot(30, y + 9, 3.5, color)
        c.text(44, y + 9, name, 13, LABEL_PRIMARY, False, "lm")
        c.text(204, y + 9, state, 11, color, False, "rm")
        y += 30

    # connectors
    c.hline(20, height - 128, 200)
    c.text(24, height - 108, "连接", 11, LABEL_TERTIARY, False, "la")
    conns = [("微信", "已连接", SUCCESS), ("钉钉", "已连接", SUCCESS),
             ("飞书", "未绑定", LABEL_TERTIARY)]
    y = height - 84
    for name, state, color in conns:
        c.dot(30, y + 8, 3.5, color)
        c.text(44, y + 8, name, 12, LABEL_SECONDARY, False, "lm")
        c.text(204, y + 8, state, 11, color, False, "rm")
        y += 26


def card(c: Canvas, x, y, w, h, status_color=None, strong=False):
    c.rect(x, y, w, h, fill="#ffffff",
           border=BORDER_L2 if strong else BORDER_L1, r=12)
    if status_color:
        c.rect(x + 1, y + 14, 3, h - 28, fill=status_color, r=2)


# ------------------------------------------------------------ screen 1 ------


def screen_now(path: str):
    W, H = 1440, 900
    c = Canvas(W, H)
    sidebar(c, "现在", H)

    x0, cw = 312, 760
    c.text(x0, 40, "现在", 24, LABEL_PRIMARY, True, "la")
    c.text(x0 + cw, 48, "3 件在跑 · 2 件等你", 13, LABEL_SECONDARY, False, "ra")

    # return banner
    c.rect(x0, 96, cw, 64, fill=BG_STRONG, r=12)
    c.text(x0 + 20, 112, "你不在的 8 小时", 14, LABEL_PRIMARY, True, "la")
    c.text(x0 + 20, 134, "完成 6 件 · 失败 1 件 · 有 2 件等你", 13,
           LABEL_SECONDARY, False, "la")
    c.button(x0 + cw - 208, 112, 88, 32, "看时间线", "accent-text", 13)
    c.button(x0 + cw - 112, 112, 92, 32, "只处理等我的", "ghost", 13)

    # composer
    c.rect(x0, 184, cw, 132, fill="#ffffff", border=BORDER_L2, r=12)
    c.text(x0 + 20, 204, "让谁做什么，什么时候要…", 15, LABEL_TERTIARY)
    chips = ["＠ 岗位", "＋ 附件", "◎ 语音"]
    cx = x0 + 20
    for ch in chips:
        cx += c.pill(cx, 258, ch, 12) + 8
    c.button(x0 + cw - 108, 254, 88, 36, "发送", "primary", 13)
    c.text(x0 + 20, 330, "Enter 发送 · Shift+Enter 换行 · 关掉页面也会继续跑",
           12, LABEL_TERTIARY)

    # recent
    c.text(x0, 372, "最近", 12, LABEL_TERTIARY)
    items = [
        ("研究竞品定价", "进行中 · 数据 Bot · 12 分钟", ACCENT,
         "正在抓 3 个站点，已完成 2 个", "不需要你做什么", LABEL_TERTIARY),
        ("给张三发报价单", "等你 · 助理 · 3 分钟", WARNING_TEXT,
         "草稿已经写好，附件 3 个", "等你批准", WARNING_TEXT),
        ("周报汇总", "完成 · 研究 Bot · 1 小时", SUCCESS_TEXT,
         "生成 6 页，含 3 个来源链接", "不需要你做什么", LABEL_TERTIARY),
    ]
    y = 400
    for title, meta, meta_color, line2, line3, line3_color in items:
        card(c, x0, y, cw, 104, status_color=meta_color)
        c.text(x0 + 20, y + 18, title, 15, LABEL_PRIMARY, True)
        c.text(x0 + cw - 20, y + 20, meta, 12, meta_color, False, "ra")
        c.text(x0 + 20, y + 46, line2, 13, LABEL_SECONDARY)
        c.text(x0 + 20, y + 72, line3, 12, line3_color)
        if line3_color == WARNING_TEXT:
            c.button(x0 + cw - 92, y + 66, 72, 28, "去处理", "accent-text", 12)
        y += 116

    c.img.save(path)


# ------------------------------------------------------------ screen 2 ------


def screen_inbox(path: str):
    W, H = 1440, 900
    c = Canvas(W, H)
    sidebar(c, "等我", H)

    x0, cw = 312, 800
    c.text(x0, 40, "等我", 24, LABEL_PRIMARY, True)
    c.text(x0 + 72, 48, "3 件", 13, LABEL_SECONDARY)

    # tabs
    tabs = [("现在必须做 2", True), ("等我决定 1", False), ("外部消息 4", False)]
    tx = x0
    for label, sel in tabs:
        w = c.text_w(label, 13) + 8
        c.text(tx, 96, label, 13,
               LABEL_PRIMARY if sel else LABEL_TERTIARY, sel)
        if sel:
            c.rect(tx, 118, w, 2, fill=ACCENT, r=1)
        tx += w + 28
    c.hline(x0, 120, cw, BORDER_L2)

    # approval card
    cy = 148
    c.rect(x0, cy, cw, 232, fill="#ffffff", border=BORDER_L2, r=12)
    c.rect(x0 + 1, cy + 16, 3, 200, fill=WARNING, r=2)
    c.text(x0 + 24, cy + 22, "需要你批准", 11, WARNING_TEXT, True)
    c.text(x0 + 24 + 70, cy + 21, "· 2 分钟前", 12, LABEL_TERTIARY)
    c.text(x0 + 24, cy + 46, "给「张三」发报价单邮件（附件 3 个）", 17,
           LABEL_PRIMARY, True)
    c.text(x0 + 24, cy + 78, "收件人 zhang@acme.com · 主题 报价单-v2 · 附件会一起发出",
           13, LABEL_SECONDARY)

    # consequence strip
    c.rect(x0 + 24, cy + 108, 420, 36, fill=WARN_TINT, r=8)
    c.dot(x0 + 40, cy + 126, 3.5, WARNING)
    c.text(x0 + 52, cy + 118, "发出后无法撤回", 13, WARNING_TEXT)

    c.button(x0 + 24, cy + 168, 88, 40, "拒绝", "outline", 13)
    c.button(x0 + 120, cy + 168, 104, 40, "始终允许", "ghost", 13)
    c.button(x0 + cw - 160, cy + 168, 136, 40, "允许并发送", "primary", 13)
    c.text(x0 + 24, cy + 216, "Esc = 拒绝 · Enter = 允许 · 手机上回复 #A3 也行",
           11, LABEL_TERTIARY)

    # decision card
    cy = 400
    c.rect(x0, cy, cw, 148, fill="#ffffff", border=BORDER_L1, r=12)
    c.rect(x0 + 1, cy + 16, 3, 116, fill=WARNING, r=2)
    c.text(x0 + 24, cy + 20, "研究 Bot 卡住了 · 需要 GitHub 只读令牌", 15,
           LABEL_PRIMARY, True)
    c.text(x0 + 24, cy + 48, "给我之后我继续；不给的话我可以改用公开搜索（会慢一些）。",
           13, LABEL_SECONDARY)
    c.button(x0 + 24, cy + 88, 96, 36, "去填令牌", "outline", 13)
    c.button(x0 + 132, cy + 88, 120, 36, "改用公开搜索", "ghost", 13)
    c.button(x0 + cw - 140, cy + 88, 116, 36, "停掉这个任务", "danger-ghost", 13)

    # external messages
    cy = 568
    c.text(x0, cy, "外部消息", 12, LABEL_TERTIARY)
    rows = [("钉钉 · #运营", "查一下上周的退款率", "转成任务"),
            ("微信 · 私聊", "把报销单整理一下，周五前给我", "转成任务"),
            ("邮件 · 财务组", "客户投诉跟进（3 封未读）", "看一遍")]
    y = cy + 24
    for src, text, action in rows:
        c.rect(x0, y, cw, 64, fill="#ffffff", border=BORDER_L1, r=10)
        c.text(x0 + 20, y + 14, src, 12, LABEL_TERTIARY)
        c.text(x0 + 20, y + 34, text, 13, LABEL_PRIMARY)
        c.button(x0 + cw - 112, y + 18, 88, 28, action, "accent-text", 12)
        y += 76

    c.img.save(path)


# ------------------------------------------------------------ screen 3 ------


def screen_team(path: str):
    W, H = 1440, 900
    c = Canvas(W, H)
    sidebar(c, "在跑", H)

    x0, cw = 312, 860
    c.text(x0, 40, "在跑", 24, LABEL_PRIMARY, True)
    c.text(x0 + 72, 48, "3 件进行中 · 1 件卡住", 13, LABEL_SECONDARY)

    filters = [("全部", True), ("进行中", False), ("等你", False),
               ("卡住", False), ("完成", False)]
    fx = x0
    for label, sel in filters:
        w = c.text_w(label, 12) + 24
        c.rect(fx, 88, w, 28, fill=BG_STRONG if sel else "#ffffff",
               border=None if sel else BORDER_L2, r=14)
        c.text(fx + w / 2, 102, label, 12,
               LABEL_PRIMARY if sel else LABEL_SECONDARY, sel, "mm")
        fx += w + 8

    tasks = [
        ("研究竞品定价", "进行中 · 数据 Bot · 12 分钟", LABEL_SECONDARY,
         "正在抓 3 个站点，已完成 2 个", "不需要你做什么", LABEL_TERTIARY, False),
        ("抓取站点 B", "卡住 · 数据 Bot · 9 分钟", WARNING_TEXT,
         "站点返回 403，重试 3 次都被拦", "需要你选一条路", WARNING_TEXT, True),
        ("竞品定价表", "排队中 · 助理 · —", LABEL_TERTIARY,
         "等「抓取站点 B」完成后自动开始", "不需要你做什么", LABEL_TERTIARY, False),
        ("上周周报", "完成 · 研究 Bot · 1 小时", SUCCESS_TEXT,
         "生成 6 页，含 3 个来源链接", "不需要你做什么", LABEL_TERTIARY, False),
    ]

    y = 144
    for title, meta, meta_color, line2, line3, line3_color, expanded in tasks:
        h = 168 if expanded else 104
        card(c, x0, y, cw, h, status_color=meta_color)
        c.text(x0 + 24, y + 18, title, 15, LABEL_PRIMARY, True)
        c.status(x0 + cw - 24 - 210, y + 20, meta, meta_color, 12)
        c.text(x0 + 24, y + 46, line2, 13, LABEL_SECONDARY)
        c.text(x0 + 24, y + 72, line3, 12, line3_color)
        if expanded:
            c.hline(x0 + 24, y + 100, cw - 48, BORDER_L1)
            c.text(x0 + 24, y + 120, "你可以：", 12, LABEL_TERTIARY, anchor="lm")
            c.button(x0 + 84, y + 104, 132, 32, "换浏览器再试", "outline", 12)
            c.button(x0 + 228, y + 104, 132, 32, "跳过 B 先出表", "ghost", 12)
            c.button(x0 + 372, y + 104, 64, 32, "停掉", "danger-ghost", 12)
        y += h + 12

    c.img.save(path)


# ------------------------------------------------------------ screen 4 ------


def screen_task(path: str):
    W, H = 1440, 940
    c = Canvas(W, H)
    sidebar(c, "在跑", H)

    x0, cw = 312, 760
    c.text(x0, 36, "在跑 / 研究竞品定价", 12, LABEL_TERTIARY)
    c.text(x0, 60, "研究竞品定价", 24, LABEL_PRIMARY, True)
    c.status(x0 + cw - 120, 64, "进行中 · 12 分钟", LABEL_SECONDARY, 13)

    # 1
    c.text(x0, 120, "1 · 在做什么", 11, LABEL_TERTIARY, True)
    c.text(x0, 140, "抓 3 个站点的公开定价，清洗成一张表，并标注每一条的来源链接。",
           14, LABEL_PRIMARY)

    # 2
    c.text(x0, 190, "2 · 到哪一步了", 11, LABEL_TERTIARY, True)
    steps = [("9:12", "派给「数据 Bot」", ACCENT),
             ("9:14", "开始抓取：站点 A 完成", ACCENT),
             ("9:26", "站点 B 返回 403，正在重试（第 3 次）", WARNING)]
    y = 212
    for t, s, color in steps:
        c.dot(x0 + 4, y + 8, 3.5, color)
        c.text(x0 + 18, y, t, 12, LABEL_TERTIARY)
        c.text(x0 + 58, y, s, 13, LABEL_PRIMARY if color == ACCENT else WARNING_TEXT)
        y += 28
    c.text(x0 + 18, y + 2, "展开全部 12 条", 12, ACCENT)

    # 3
    c.text(x0, 340, "3 · 证据", 11, LABEL_TERTIARY, True)
    ev = [("来源", "acme.com/pricing（2026-11-25 抓取）"),
          ("截图", "站点A-价格页.png")]
    y = 362
    for kind, val in ev:
        c.rect(x0, y, cw, 44, fill="#ffffff", border=BORDER_L1, r=10)
        c.text(x0 + 16, y + 14, kind, 11, LABEL_TERTIARY)
        c.text(x0 + 64, y + 13, val, 13, LABEL_PRIMARY)
        c.button(x0 + cw - 84, y + 8, 64, 28, "打开", "accent-text", 12)
        y += 52

    # 4
    c.text(x0, 484, "4 · 产物", 11, LABEL_TERTIARY, True)
    c.rect(x0, 506, cw, 72, fill="#ffffff", border=BORDER_L1, r=10)
    c.rect(x0 + 16, 522, 32, 40, fill=BG_STRONG, r=6)
    c.text(x0 + 32, 542, "XLS", 10, LABEL_TERTIARY, True, "mm")
    c.text(x0 + 60, 520, "定价表.xlsx", 14, LABEL_PRIMARY, True)
    c.text(x0 + 60, 542, "42 条 · 12 KB · 2026-11-25 09:31", 12, LABEL_TERTIARY)
    c.button(x0 + cw - 168, 526, 68, 32, "预览", "outline", 12)
    c.button(x0 + cw - 88, 526, 68, 32, "下载", "ghost", 12)

    # 5
    c.text(x0, 608, "5 · 要不要你", 11, LABEL_TERTIARY, True)
    c.rect(x0, 630, cw, 64, fill=SUCCESS_TINT, r=10)
    c.dot(x0 + 24, 662, 4, SUCCESS)
    c.text(x0 + 40, 646, "不需要你做什么", 14, SUCCESS_TEXT, True)
    c.text(x0 + 40, 668, "站点 B 失败后会自动走备用方案；出表前不会再问你。",
           12, LABEL_SECONDARY)
    c.button(x0 + cw - 96, 646, 80, 32, "催一下", "ghost", 12)

    # footer actions
    c.hline(x0, 728, cw, BORDER_L1)
    c.button(x0, 748, 96, 36, "停掉任务", "danger-ghost", 13)
    c.button(x0 + cw - 128, 748, 128, 36, "在会话里问一句", "outline", 13)

    c.img.save(path)


# ------------------------------------------------------------ screen 5 ------


def _phone_chrome(c: Canvas, title: str, h: int):
    c.text(20, 14, "9:41", 13, "#000000", True)
    for i, hh in enumerate((3, 5, 7, 9)):
        c.rect(c.w - 96 + i * 6, 26 - hh, 4, hh, fill="#000000", r=1)
    c.text(c.w - 66, 14, "5G", 11, "#000000", True)
    c.rect(c.w - 44, 16, 22, 11, border="#000000", r=2)
    c.rect(c.w - 42, 18, 14, 7, fill="#000000", r=1)
    c.rect(c.w - 21, 19, 2, 5, fill="#000000", r=1)

    c.rect(0, 44, c.w, 44, fill="#f7f7f7")
    c.text(20, 66, "‹", 22, "#000000", False, "lm")
    c.text(c.w / 2, 66, title, 16, "#000000", True, "mm")
    c.text(c.w - 20, 66, "⋯", 16, "#000000", False, "rm")
    c.hline(0, 88, c.w, "#dcdcdc")

    c.rect(0, h - 64, c.w, 64, fill="#f7f7f7")
    c.hline(0, h - 64, c.w, "#dcdcdc")
    c.rect(12, h - 52, c.w - 80, 40, fill="#ffffff", border=BORDER_L2, r=8)
    c.text(24, h - 32, "回复…", 13, LABEL_TERTIARY, False, "lm")
    c.rect(c.w - 60, h - 52, 48, 40, fill="#ffffff", border=BORDER_L2, r=8)
    c.text(c.w - 36, h - 32, "◎", 16, LABEL_SECONDARY, False, "mm")


def phone_wechat_notice() -> Canvas:
    W, H = 390, 844
    c = Canvas(W, H, bg="#ededed")
    _phone_chrome(c, "助理", H)

    c.rect(W - 20 - 250, 104, 250, 40, fill="#95ec69", r=10)
    c.text(W - 32, 124, "把报销单整理一下，周五前给我", 13, "#000000", False, "rm")

    # approval as one sentence
    c.rect(12, 156, 336, 110, fill="#ffffff", r=10)
    c.text_block(24, 172, "需要你批准：给「张三」发报价单邮件\n"
                          "（附件 3 个）。发出后无法撤回。\n回复「同意」就发。",
                 13, LABEL_PRIMARY, 312, 22)

    c.text(W / 2, 286, "单聊里只有这一条待批，直接回就行", 11, "#8a8f98", False, "ma")

    # user reply
    c.rect(W - 20 - 110, 312, 110, 40, fill="#95ec69", r=10)
    c.text(W - 32, 332, "同意", 13, "#000000", False, "rm")

    # receipt
    c.rect(12, 368, 250, 44, fill="#ffffff", r=10)
    c.text(24, 380, "已允许，正在发送…", 13, LABEL_PRIMARY)

    # follow-up
    c.rect(12, 424, 336, 76, fill="#ffffff", r=10)
    c.text_block(24, 438, "有多条待批时它会先问：\n现在有 3 条，回 1 / 2 / 3 选一条。",
                 12, LABEL_TERTIARY, 312, 20)

    # second example: reject path
    c.rect(12, 512, 336, 64, fill="#ffffff", r=10)
    c.text_block(24, 526, "回「不同意」就取消，草稿留着不删。", 12, LABEL_TERTIARY, 312, 20)
    return c


def phone_full_web() -> Canvas:
    W, H = 390, 844
    c = Canvas(W, H, bg="#ffffff")

    # status bar
    c.text(20, 14, "9:41", 13, "#000000", True)
    for i, hh in enumerate((3, 5, 7, 9)):
        c.rect(W - 96 + i * 6, 26 - hh, 4, hh, fill="#000000", r=1)
    c.text(W - 66, 14, "5G", 11, "#000000", True)
    c.rect(W - 44, 16, 22, 11, border="#000000", r=2)
    c.rect(W - 42, 18, 14, 7, fill="#000000", r=1)

    # app bar
    c.text(20, 62, "等我", 22, LABEL_PRIMARY, True)
    c.text(76, 68, "3 件", 12, LABEL_TERTIARY)
    c.text(W - 20, 66, "设置", 13, LABEL_TERTIARY, False, "ra")

    # tabs
    tabs = [("现在必须做 2", True), ("等我决定 1", False), ("外部消息 4", False)]
    tx = 20
    for label, sel in tabs:
        w = c.text_w(label, 12) + 4
        c.text(tx, 110, label, 12, LABEL_PRIMARY if sel else LABEL_TERTIARY, sel)
        if sel:
            c.rect(tx, 130, w, 2, fill=ACCENT, r=1)
        tx += w + 20
    c.hline(20, 132, W - 40, BORDER_L2)

    # approval card
    bx, by, bw = 12, 150, W - 24
    c.rect(bx, by, bw, 292, fill="#ffffff", border=BORDER_L2, r=14)
    c.rect(bx + 1, by + 16, 3, 260, fill=WARNING, r=2)
    c.text(bx + 20, by + 20, "需要你批准", 11, WARNING_TEXT, True)
    c.text(bx + 88, by + 19, "· 2 分钟前", 11, LABEL_TERTIARY)
    c.text(bx + 20, by + 44, "给「张三」发报价单邮件", 16, LABEL_PRIMARY, True,
           max_w=bw - 40)
    c.text(bx + 20, by + 72, "附件 3 个 · zhang@acme.com", 12, LABEL_SECONDARY,
           max_w=bw - 40)

    c.rect(bx + 20, by + 100, bw - 40, 34, fill=WARN_TINT, r=8)
    c.text(bx + 34, by + 109, "发出后无法撤回", 12, WARNING_TEXT)

    half = (bw - 48) / 2
    c.rect(bx + 20, by + 148, half, 42, fill="#ffffff", border=BORDER_L3, r=8)
    c.text(bx + 20 + half / 2, by + 169, "拒绝", 13, LABEL_PRIMARY, False, "mm")
    c.rect(bx + 28 + half, by + 148, half, 42, fill="#ffffff", border=BORDER_L3, r=8)
    c.text(bx + 28 + half + half / 2, by + 169, "始终允许", 12, LABEL_SECONDARY, False, "mm")
    c.rect(bx + 20, by + 198, bw - 40, 48, fill=ACCENT, r=10)
    c.text(bx + bw / 2, by + 222, "允许并发送", 14, "#ffffff", True, "mm")
    c.text(bx + 20, by + 256, "也可以回一句「同意」", 11, LABEL_TERTIARY)

    # second card (decision)
    c.rect(bx, by + 306, bw, 96, fill="#ffffff", border=BORDER_L1, r=12)
    c.text(bx + 20, by + 322, "研究 Bot 卡住了 · 需要令牌", 13, LABEL_PRIMARY, True)
    c.text(bx + 20, by + 344, "给我就能继续；不给可改用公开搜索。", 11, LABEL_TERTIARY)
    c.text(bx + 20, by + 368, "去填令牌", 12, ACCENT)

    # bottom tab bar
    c.hline(0, H - 76, W, BORDER_L2)
    tabs2 = [("现在", False), ("等我", True), ("在跑", False), ("会话", False), ("设置", False)]
    tw = W / len(tabs2)
    for i, (label, sel) in enumerate(tabs2):
        cx = tw * i + tw / 2
        c.dot(cx, H - 50, 4, ACCENT if sel else BORDER_L3)
        c.text(cx, H - 36, label, 10, ACCENT if sel else LABEL_TERTIARY, sel, "ma")
    return c


def screen_mobile(path: str):
    left = phone_wechat_notice().img
    right = phone_full_web().img
    W, H = 820, 900
    out = Image.new("RGB", (W, H), "#ffffff")
    d = ImageDraw.Draw(out)

    f15 = ImageFont.truetype(FONT_BOLD, 15, index=SC_INDEX)
    f12 = ImageFont.truetype(FONT_REG, 12, index=SC_INDEX)
    d.text((0, 20), "微信：回一句「同意」就够了", font=f15, fill="#0f1115")
    d.text((0, 44), "单聊里只有一条待批，直接回同意；多条会先问编号",
           font=f12, fill="#5a6371")
    d.text((430, 20), "手机端：同一套全功能 Web", font=f15, fill="#0f1115")
    d.text((430, 44), "内网直连，模块一个不少，布局自适应",
           font=f12, fill="#5a6371")

    out.paste(left, (0, 76))
    out.paste(right, (430, 76))
    out.save(path)


# ------------------------------------------------------------ screen 6 ------


def switch(c: Canvas, x, y, on: bool):
    c.rect(x, y, 40, 24, fill=ACCENT if on else BORDER_L3, r=12)
    cx = x + 20 if on else x + 4
    c.d.ellipse([cx, y + 4, cx + 16, y + 20], fill="#ffffff")


def settings_nav(c: Canvas, active: str, height: int):
    c.rect(240, 0, 200, height, fill="#ffffff")
    c.vline(440, 0, height, BORDER_L1)
    c.text(264, 40, "设置", 16, LABEL_PRIMARY, True)
    items = ["模型", "行为与限额", "外观", "通知", "连接器", "岗位", "数据"]
    y = 84
    for it in items:
        sel = it == active
        if sel:
            c.rect(252, y - 6, 176, 34, fill=ACCENT_TINT, r=8)
        c.text(268, y + 11, it, 13, LABEL_PRIMARY if sel else LABEL_SECONDARY,
               sel, "lm")
        y += 40


def screen_settings(path: str):
    W, H = 1440, 1060
    c = Canvas(W, H)
    sidebar(c, "设置", H)
    settings_nav(c, "通知", H)

    x0, cw = 488, 720
    c.text(x0, 36, "通知", 24, LABEL_PRIMARY, True)
    c.text(x0, 80, "只有「需要你」的事才会打断你；其余按下面的规则汇总。",
           13, LABEL_SECONDARY)

    y = 108
    c.rect(x0, y, cw, 132, fill="#ffffff", border=BORDER_L1, r=12)
    c.text(x0 + 20, y + 18, "每天最多主动打断", 14, LABEL_PRIMARY, True)
    c.text(x0 + 20, y + 44, "超过之后自动降级为汇总，不会丢消息。", 12, LABEL_TERTIARY)
    c.rect(x0 + cw - 148, y + 18, 128, 36, fill=BG_STRONG, r=8)
    c.text(x0 + cw - 126, y + 36, "5", 15, LABEL_PRIMARY, True, "mm")
    c.text(x0 + cw - 84, y + 36, "次 / 天", 12, LABEL_SECONDARY, False, "mm")
    c.text(x0 + 20, y + 76, "静音时段", 12, LABEL_TERTIARY)
    c.rect(x0 + 96, y + 70, 176, 30, fill="#ffffff", border=BORDER_L2, r=6)
    c.text(x0 + 112, y + 85, "22:30 — 08:00", 12, LABEL_PRIMARY, False, "lm")
    c.text(x0 + 292, y + 76, "审批也会被压到早上，一起汇总。", 11, LABEL_TERTIARY)
    c.button(x0 + cw - 264, y + 84, 116, 32, "我在忙 2 小时", "outline", 12)
    c.button(x0 + cw - 136, y + 84, 116, 32, "今晚别找我", "ghost", 12)

    y = 268
    c.text(x0, y, "推到哪个渠道", 11, LABEL_TERTIARY, True)
    ty = y + 24
    c.rect(x0, ty, cw, 300, fill="#ffffff", border=BORDER_L1, r=12)
    headers = ["类别", "主渠道", "备用", "免打扰"]
    xs = [x0 + 20, x0 + 210, x0 + 400, x0 + 540]
    for hx, h in zip(xs, headers):
        c.text(hx, ty + 14, h, 11, LABEL_TERTIARY, True)
    c.hline(x0 + 1, ty + 40, cw - 2, BORDER_L1)
    rows = [
        ("审批（阻塞）", "微信通知+链接", "钉钉卡片", "否"),
        ("需要你决定", "钉钉", "微信", "是"),
        ("任务完成 / 失败", "微信", "邮件", "是"),
        ("外部消息汇总", "微信 · 2 次/天", "邮件", "是"),
        ("每日总结", "邮件", "—", "—"),
    ]
    ry = ty + 52
    for name, ch, quiet, esc in rows:
        c.text(x0 + 20, ry + 10, name, 13, LABEL_PRIMARY)
        c.pill(x0 + 205, ry + 1, ch, 11, h=26)
        c.pill(x0 + 395, ry + 1, quiet, 11, h=26)
        c.text(x0 + 540, ry + 10, esc, 12, LABEL_SECONDARY)
        c.hline(x0 + 1, ry + 40, cw - 2, BORDER_L1)
        ry += 48

    y = 612
    c.text(x0, y, "审批入口（都落到同一个 approval.action）", 11, LABEL_TERTIARY, True)
    c.rect(x0, y + 24, cw, 152, fill="#ffffff", border=BORDER_L1, r=12)
    c.text(x0 + 20, y + 44, "点按钮就批，不用回文字", 14, LABEL_PRIMARY, True)
    c.text(x0 + 20, y + 68, "桌面按钮 · 手机审批页 · 钉钉/飞书卡片按钮", 12, LABEL_TERTIARY)
    switch(c, x0 + cw - 60, y + 40, True)
    c.hline(x0 + 20, y + 92, cw - 40, BORDER_L1)
    c.text(x0 + 20, y + 106, "允许微信回「同意」当审批（默认关）", 13, LABEL_PRIMARY)
    c.text(x0 + 20, y + 128, "靠自然语言解析，歧义大；只在你明确开启时生效。", 12, LABEL_TERTIARY)
    c.switch = None
    c.rect(x0 + cw - 60, y + 96, 40, 24, fill=BORDER_L3, r=12)
    c.d.ellipse([x0 + cw - 56, y + 100, x0 + cw - 40, y + 116], fill="#ffffff")

    y = 800
    c.text(x0, y, "连接器", 11, LABEL_TERTIARY, True)
    conns = [("微信 ClawBot", "已连接 · 单聊 · 可接活", SUCCESS, "重新连接"),
             ("钉钉", "已连接 · 长连接 · 可批", SUCCESS, "测试"),
             ("飞书", "未绑定", LABEL_TERTIARY, "去绑定")]
    cy = y + 24
    for name, state, color, action in conns:
        c.rect(x0, cy, cw, 56, fill="#ffffff", border=BORDER_L1, r=10)
        c.dot(x0 + 24, cy + 28, 4, color)
        c.text(x0 + 40, cy + 18, name, 13, LABEL_PRIMARY, True)
        c.text(x0 + 40, cy + 36, state, 11, color)
        c.button(x0 + cw - 108, cy + 14, 88, 28, action, "accent-text", 12)
        cy += 64

    c.img.save(path)


def main():
    outs = [
        ("01-now.png", screen_now),
        ("02-inbox.png", screen_inbox),
        ("03-team.png", screen_team),
        ("04-task.png", screen_task),
        ("05-mobile-approval.png", screen_mobile),
        ("06-settings-notification.png", screen_settings),
    ]
    for name, fn in outs:
        path = os.path.join(OUT_DIR, name)
        fn(path)
        print("wrote", path)


if __name__ == "__main__":
    main()