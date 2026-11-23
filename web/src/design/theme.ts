import { theme, type ThemeConfig } from 'antd';
import { accent, fontFamily, type Appearance } from './antd-theme';

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
  };
}

export { type Appearance };