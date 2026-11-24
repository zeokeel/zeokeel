import { theme, type ThemeConfig } from 'antd';
import { accent, dangerText, fontFamily, type Appearance } from './antd-theme';

/**
 * The whole theme: `algorithm` + `colorPrimary` + `fontFamily`
 * (docs/plans/antd-x-component-map.md §5). Nothing else is overridden — antd
 * owns its own surfaces, radii and spacing scale, and the brand only picks the
 * accent and the type face.
 *
 * `compactAlgorithm` is deliberately absent: it rescales the discrete
 * 28/32/36/40/44 control-height ladder out of existence.
 */
export function zkThemeConfig(appearance: Appearance): ThemeConfig {
  return {
    algorithm: appearance === 'dark' ? theme.darkAlgorithm : theme.defaultAlgorithm,
    token: {
      colorPrimary: accent[appearance],
      fontFamily,
    },
    components: {
      Menu: {
        // Destructive menu rows (删除会话, 删除连接器) inherit antd's error hue,
        // which is 3.9:1 on dark surfaces; the palette's AA text colour is not.
        dangerItemColor: dangerText[appearance],
        dangerItemHoverColor: dangerText[appearance],
        dangerItemSelectedColor: dangerText[appearance],
      },
    },
  };
}

export { type Appearance };