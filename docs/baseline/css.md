# 全元素 CSS 提取（基线）

> 来源：在隔离实例上对 17 个屏/状态执行 `scripts/cdp.mjs cssscan`，逐元素读取 computed style（43 个属性），原始数据在 `baseline/css/*.json`，机器汇总在 [summary.json](../../baseline/css/summary.json)。
> 用途：实现时样式函数（§5.4）按此表逐项对齐；像素门禁的“令牌一致”检查以此为输入。

## 覆盖的屏

| 屏 | 元素数 |
| --- | --- |
| 00-home | 236 |
| 01-more-menu | 260 |
| 02-plugins | 267 |
| 03-automation | 167 |
| 04-settings-general | 380 |
| 05-settings-models | 294 |
| 06-settings-builtin | 559 |
| 07-settings-presets | 349 |
| 08-session-chat | 3141 |
| 09-session-trajectory | 838 |
| 10-session-menu | 3142 |
| 11-right-sidebar | 3149 |
| 12-session-permission | 3163 |
| 13-session-model | 3147 |
| 14-session-commands | 3147 |
| 15-session-dark | 3147 |
| 16-session-dark-narrow | 3138 |

## 颜色

### 文字色
| color | 出现次数 |
| --- | --- |
| `rgb(129, 133, 140)` | 12851 |
| `rgb(15, 17, 21)` | 7866 |
| `rgb(173, 178, 184)` | 4300 |
| `rgb(249, 250, 251)` | 1905 |
| `rgb(97, 102, 107)` | 748 |
| `rgb(65, 118, 230)` | 270 |
| `rgb(207, 211, 214)` | 113 |
| `rgb(255, 255, 255)` | 102 |
| `color(srgb 0.532549 0.418824 0.682353)` | 80 |
| `rgb(0, 0, 0)` | 73 |
| `rgb(122, 170, 255)` | 52 |
| `rgb(221, 134, 41)` | 45 |
| `rgb(236, 19, 19)` | 38 |
| `rgb(53, 54, 56)` | 24 |
| `rgb(229, 229, 229)` | 16 |

### 背景色
| background-color | 出现次数 |
| --- | --- |
| `rgba(0, 0, 0, 0)` | 26928 |
| `rgb(173, 178, 184)` | 780 |
| `rgb(129, 133, 140)` | 260 |
| `rgb(255, 255, 255)` | 141 |
| `rgb(250, 250, 250)` | 72 |
| `rgb(245, 246, 247)` | 36 |
| `rgb(237, 243, 254)` | 36 |
| `rgb(249, 250, 251)` | 30 |
| `rgb(221, 134, 41)` | 22 |
| `rgba(38, 49, 72, 0.06)` | 21 |
| `rgb(41, 41, 41)` | 20 |
| `color(srgb 0.254902 0.462745 0.901961 / 0.1)` | 18 |
| `rgb(44, 44, 46)` | 16 |
| `color(srgb 0.935088 0.912 0.948235)` | 16 |
| `rgb(65, 118, 230)` | 15 |

### 边框色
| border-*-color | 出现次数 |
| --- | --- |
| `rgb(129, 133, 140)` | 25676 |
| `rgb(15, 17, 21)` | 15352 |
| `rgb(173, 178, 184)` | 8596 |
| `rgb(249, 250, 251)` | 3748 |
| `rgb(97, 102, 107)` | 1480 |
| `rgb(65, 118, 230)` | 464 |
| `rgba(0, 0, 0, 0.04)` | 241 |
| `rgb(207, 211, 214)` | 226 |
| `rgb(255, 255, 255)` | 204 |
| `rgb(0, 0, 0)` | 146 |

## 字体

### 字体栈
| font-family | 出现次数 |
| --- | --- |
| `-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microso` | 28268 |
| `"SF Mono", "JetBrains Mono", "Fira Code", Consolas, "Liberation Mono", Menlo, Courier, "Pi` | 237 |
| `Menlo, Consolas, "Liberation Mono", "PingFang SC", "Microsoft YaHei"` | 15 |
| `ui-monospace, SFMono-Regular, Menlo, monospace` | 4 |

### 字号
| font-size | 出现次数 |
| --- | --- |
| `14px` | 11315 |
| `16px` | 10262 |
| `13px` | 4359 |
| `13.3333px` | 1010 |
| `12px` | 779 |
| `18px` | 376 |
| `10px` | 202 |
| `12.25px` | 80 |
| `11px` | 71 |
| `26px` | 36 |
| `19px` | 24 |
| `15px` | 4 |
| `8px` | 4 |
| `20px` | 2 |

### 字重
| font-weight | 出现次数 |
| --- | --- |
| `400` | 27221 |
| `500` | 648 |
| `600` | 486 |
| `650` | 145 |
| `700` | 24 |

### 行高
| line-height | 出现次数 |
| --- | --- |
| `24px` | 11599 |
| `20px` | 2370 |
| `22px` | 704 |
| `18px` | 552 |
| `16px` | 222 |
| `13.3333px` | 153 |
| `19px` | 80 |
| `32px` | 36 |
| `28px` | 26 |
| `18.2px` | 24 |
| `17px` | 23 |
| `22.4px` | 20 |

## 圆角
| border-radius | 出现次数 |
| --- | --- |
| `0px` | 25803 |
| `1px` | 1091 |
| `8px` | 1045 |
| `12px` | 246 |
| `20px` | 105 |
| `999px` | 69 |
| `4px` | 62 |
| `16px` | 50 |
| `28px` | 19 |
| `10px` | 16 |
| `50%` | 7 |
| `20px 20px 0px 0px` | 4 |

## 阴影
| box-shadow | 出现次数 |
| --- | --- |
| `rgba(0, 0, 0, 0.1) 0px 0px 0px 0.5px, rgba(0, 0, 0, 0.03) 0px 4px 16px 0px, rgba(0, 0, 0, ` | 13 |
| `rgba(0, 0, 0, 0.16) 0px 0px 0px 0.5px, rgba(0, 0, 0, 0.04) 0px 3px 8px 0px, rgba(0, 0, 0, ` | 4 |
| `rgba(0, 0, 0, 0.04) 0px 0px 0px 0.5px, rgba(0, 0, 0, 0.04) 0px 3px 8px 0px, rgba(0, 0, 0, ` | 3 |
| `rgba(255, 255, 255, 0.12) 0px 0px 0px 0.5px, rgba(0, 0, 0, 0.03) 0px 4px 16px 0px, rgba(0,` | 2 |
| `rgba(0, 0, 0, 0.2) 0px 0px 1px 0px, rgba(0, 0, 0, 0.02) 0px 0px 4px 0px, rgba(0, 0, 0, 0.0` | 1 |

## 间距
| padding | 出现次数 |
| --- | --- |
| `0px` | 110446 |
| `6px` | 763 |
| `8px` | 721 |
| `12px` | 287 |
| `5px` | 273 |
| `4px` | 270 |
| `1px` | 228 |
| `10px` | 222 |
| `16px` | 215 |
| `14px` | 156 |
| `2px` | 149 |
| `7px` | 92 |
| `20px` | 57 |
| `9px` | 50 |
| `36px` | 37 |

### margin
| margin | 出现次数 |
| --- | --- |
| `0px` | 104373 |
| `1px` | 4192 |
| `6px` | 2269 |
| `8px` | 2132 |
| `16px` | 264 |
| `4px` | 207 |
| `2px` | 182 |
| `-4px` | 91 |
| `-3px` | 64 |
| `5px` | 48 |
| `12px` | 35 |
| `-12px` | 34 |

### gap
| gap | 出现次数 |
| --- | --- |
| `16px` | 1452 |
| `8px` | 1238 |
| `4px` | 746 |
| `6px` | 741 |
| `12px` | 470 |
| `2px` | 405 |
| `0px` | 228 |
| `10px` | 126 |
| `14px` | 81 |
| `3px` | 36 |

## 组件样式样本（从真实 DOM 抽取）

| 组件 | 屏 | 元素 | 字号/行高 | 字重 | 颜色/背景 | 圆角 | padding | 关键属性 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| plugin_row_title | 02-plugins | `div` | 16px / - | 400 | rgb(15, 17, 21) / rgba(0, 0, 0, 0) | 0px | ['0px', '0px', '0px', '0px'] | border-top-width=0px; border-top-color=rgb(15, 17, 21); display=flex; gap=8px |
| setting_row_label | 04-settings-general | `div` | 16px / - | 400 | rgb(15, 17, 21) / rgba(0, 0, 0, 0) | 0px | ['0px', '48px', '0px', '0px'] | border-top-width=0px; border-top-color=rgb(15, 17, 21); display=flex; gap=4px |
| tab | 08-session-chat | `button` | 13px / 16px | 500 | rgb(65, 118, 230) / rgba(0, 0, 0, 0) | 0px | ['0px', '0px', '9px', '0px'] | border-top-width=0px; border-top-color=rgb(65, 118, 230); display=block |

## 用法

- 实现时：`internal/design` 的样式函数按本表的值实现；新增/变更一律先改 summary 与令牌生成。
- 复跑：`DSH_BASE=... DSH_AUTHORITY=... node scripts/cdp.mjs seq --file scripts/capture/css-scan.json`。
- 逐元素原始表：`baseline/css/<screen>.json`（`elements[]` 含 tag/role/class/text/rect/computed style）。