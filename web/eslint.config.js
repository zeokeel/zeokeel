import js from '@eslint/js';
import reactHooks from 'eslint-plugin-react-hooks';
import reactRefresh from 'eslint-plugin-react-refresh';
import tseslint from 'typescript-eslint';

/**
 * Color literals are banned outside the generated token files
 * (docs/WEB-CLIENT.md §9.2: only `src/design/tokens.css` may carry them; the
 * sibling `antd-theme.ts` is generated from the same Go sources).
 */
const COLOR_LITERAL = String.raw`/(^|[^\w-])(#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\()/`;

export default tseslint.config(
  // `.cache` holds local artefacts (the generation-chain baseline, and any
  // throwaway tree used to test the checker) — never lint or ship them.
  { ignores: ['dist', 'node_modules', 'dev', 'scripts', '.cache'] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ['**/*.{ts,tsx}'],
    languageOptions: {
      ecmaVersion: 2022,
      sourceType: 'module',
    },
    plugins: {
      'react-hooks': reactHooks,
      'react-refresh': reactRefresh,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],
      'no-console': ['error', { allow: ['warn', 'error'] }],
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_' },
      ],
      'no-restricted-syntax': [
        'error',
        {
          selector: `Literal[value=${COLOR_LITERAL}]`,
          message:
            'Color literals are banned outside src/design/tokens.css. Use a var(--zk-*) token.',
        },
      ],
    },
  },
  {
    // Generated from internal/brand + internal/design by scripts/gen-tokens.mjs.
    files: ['src/design/antd-theme.ts'],
    rules: { 'no-restricted-syntax': 'off' },
  },
  {
    // Generated from internal/icons by scripts/gen-icons.mjs.
    files: ['src/design/icons.tsx'],
    rules: { 'react-refresh/only-export-components': 'off' },
  },
  {
    // @tanstack/react-virtual is an external store the React compiler cannot
    // model; the warning is expected, the virtualizer is the point (§7.1).
    files: ['src/chat/transcript.tsx'],
    rules: { 'react-hooks/incompatible-library': 'off' },
  },
  {
    // Context modules legitimately export a provider plus its hook.
    files: [
      'src/features/sessions/sessions-context.tsx',
      'src/features/approvals/approvals-context.tsx',
      'src/keyboard/keyboard-provider.tsx',
      'src/chat/tools/registry.tsx',
      'src/chat/tools/sections.tsx',
      'src/chat/composer.tsx',
      'src/chat/transcript.tsx',
      'src/features/inspector/inspector-panel.tsx',
    ],
    rules: { 'react-refresh/only-export-components': 'off' },
  },
  {
    files: ['**/*.test.{ts,tsx}', 'vitest.setup.ts'],
    rules: { 'no-restricted-syntax': 'off' },
  },
);