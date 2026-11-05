# ZeoKeel 原生视觉预览（2026-11-05）

全部截图由各屏的 headless 预览测试生成（`ui.Render(..., 1600, 1000, 1)`，live fixture，中文默认），可复现：

```sh
export PATH=/usr/local/go/bin:$PATH
# 普通测试/CI 只渲染到临时目录，不写工作树；要刷新以下快照时显式开开关：
ZEOKEEL_UPDATE_PREVIEWS=1 go test -count=1 ./internal/shell -run 'TestRender.*NativePreview'
```

| 屏 | 浅色 | 深色 |
| --- | --- | --- |
| Home | [home-native-light.png](home-native-light.png) | [home-native-dark.png](home-native-dark.png) |
| Session | [session-native-light.png](session-native-light.png) | [session-native-dark.png](session-native-dark.png) |
| Sidebar | [sidebar-native-light.png](sidebar-native-light.png) | [sidebar-native-dark.png](sidebar-native-dark.png) |
| Settings | [settings-native-light.png](settings-native-light.png) | [settings-native-dark.png](settings-native-dark.png) |
| Plugins | [plugins-native-light.png](plugins-native-light.png) | [plugins-native-dark.png](plugins-native-dark.png) |
| Automation | [automation-native-light.png](automation-native-light.png) | [automation-native-dark.png](automation-native-dark.png) |
| Trajectory | [trajectory-native-light.png](trajectory-native-light.png) | [trajectory-native-dark.png](trajectory-native-dark.png) |
| Right sidebar（Start） | [right-sidebar-native-light.png](right-sidebar-native-light.png) | [right-sidebar-native-dark.png](right-sidebar-native-dark.png) |
| Right sidebar（Files） | [right-sidebar-files-light.png](right-sidebar-files-light.png) | [right-sidebar-files-dark.png](right-sidebar-files-dark.png) |

设计口径见 [docs/design/native-ui.md](../native-ui.md)；品牌色板与主题见 [ADR 0002](../../decisions/0002-brand-theme-foundation.md)。
