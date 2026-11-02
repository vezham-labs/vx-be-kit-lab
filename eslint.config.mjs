import nx from '@nx/eslint-plugin'
import tanstackQuery from '@tanstack/eslint-plugin-query'
import jsxA11y from 'eslint-plugin-jsx-a11y'
import unusedImports from 'eslint-plugin-unused-imports'
import * as jsoncParser from 'jsonc-eslint-parser'
import { fileURLToPath } from 'node:url'
import * as yamlParser from 'yaml-eslint-parser'

import accessibilityLabels from './vx/tools/eslint/accessibility-labels.mjs'
import arrowFunctions from './vx/tools/eslint/arrow-functions.mjs'
import buttonOnPress from './vx/tools/eslint/button-on-press.mjs'
import commentStyle from './vx/tools/eslint/comment-style.mjs'
import dotNotation from './vx/tools/eslint/dot-notation.mjs'
import namedExports from './vx/tools/eslint/named-exports.mjs'
import propsName from './vx/tools/eslint/props-name.mjs'
import wildcardBarrel from './vx/tools/eslint/wildcard-barrel.mjs'

// import react from 'eslint-plugin-react'
// import reactHooks from 'eslint-plugin-react-hooks'
const ignores = [
  '**/dist',
  '**/.output',
  '**/out-tsc',
  '**/test-output',
  '**/node_modules',
  '**/coverage',
  '**/.next',
  '**/.next/**',
  '**/.vezham',
  '**/.nx',
  '**/.tools',
  '**/.cache',
  '**/lint-staged.config.cjs',
  '**/routeTree.gen.ts',
  '**/src/generated/**',
  '**/vite.config.*.timestamp*',
  '**/vitest.config.*.timestamp*'
]

const vxLintIgnores = [
  'eslint.config.mjs',
  '**/.agents/**',
  '**/bower_components/**',
  '**/*-lock.{json,yaml}',
  '**/public/{manifest.webmanifest,browserconfig.xml,robots.txt,sw.js,offline.html,404.html}',
  '**/generated/**',
  '**/*.gen.{ts,tsx,js,jsx,mts,cts,mjs,cjs}',
  '**/*.generated.{ts,tsx,js,jsx,mts,cts,mjs,cjs}',
  '**/*.d.{ts,mts,cts}'
]

export default [
  {
    files: ['**/*.{yaml,yml}'],
    ignores: ['**/*-lock.yaml', '**/.verdaccio/**', '**/.github/workflows/**'],
    languageOptions: { parser: yamlParser },
    plugins: { '@vx-lint': { rules: { 'comment-style': commentStyle } } },
    rules: { '@vx-lint/comment-style': 'error' }
  },
  {
    files: ['**/*.json'],
    // Override or add rules here
    rules: {},
    languageOptions: {
      parser: jsoncParser
    }
  },
  ...nx.configs['flat/base'],
  ...nx.configs['flat/typescript'],
  ...nx.configs['flat/javascript'],
  ...tanstackQuery.configs['flat/recommended'],
  {
    ignores
  },
  {
    files: ['**/*.{js,jsx,mjs,cjs,ts,tsx,mts,cts}'],
    plugins: { 'unused-imports': unusedImports },
    rules: {
      'no-unused-vars': 'off',
      '@typescript-eslint/no-unused-vars': 'off',
      'unused-imports/no-unused-imports': 'error',
      'unused-imports/no-unused-vars': 'error'
    }
  },
  {
    files: ['**/*.{jsx,tsx}'],
    plugins: { 'jsx-a11y': jsxA11y },
    rules: jsxA11y.configs.recommended.rules
  },
  {
    files: ['**/*.{js,jsx,mjs,cjs,ts,tsx,mts,cts}'],
    ignores: vxLintIgnores,
    plugins: {
      '@vx-lint': {
        rules: {
          'arrow-functions': arrowFunctions,
          'accessibility-labels': accessibilityLabels,
          'button-on-press': buttonOnPress,
          'comment-style': commentStyle,
          'dot-notation': dotNotation,
          'named-exports': namedExports,
          'props-name': propsName,
          'wildcard-barrel': wildcardBarrel
        }
      }
    },
    rules: {
      '@vx-lint/arrow-functions': 'error',
      '@vx-lint/accessibility-labels': 'error',
      '@vx-lint/button-on-press': 'error',
      '@vx-lint/comment-style': 'error',
      '@vx-lint/dot-notation': 'error',
      'prefer-arrow-callback': ['error', { allowNamedFunctions: true }]
    }
  },
  {
    // vx-bot/NOTE: Resolve app exceptions from the workspace even when Nx changes cwd.
    basePath: fileURLToPath(new URL('.', import.meta.url)),
    files: ['**/src/**/*.{ts,tsx,js,jsx,mts,cts,mjs,cjs}'],
    ignores: [
      ...vxLintIgnores,
      '**/*.config.*',
      '**/*.stories.*',
      '**/routes/**',
      '{apps,apps-*}/**/src/{pages,vx-pages}/**',
      '**/app/**/page.{ts,tsx,js,jsx}',
      '**/app/**/{layout,template,loading,error,global-error,not-found,default,global-not-found}.{ts,tsx,js,jsx}',
      '**/app/**/{sitemap,robots,manifest,opengraph-image,twitter-image,icon,apple-icon}.{ts,tsx,js,jsx}',
      '**/middleware.{ts,js}',
      '**/proxy.{ts,js}'
    ],
    rules: { '@vx-lint/named-exports': 'error' }
  },
  {
    files: ['**/src/**/*.{ts,tsx,js,jsx,mts,cts}'],
    ignores: vxLintIgnores,
    rules: { '@vx-lint/props-name': 'error' }
  },
  {
    files: [
      '**/src/lib/**/types.{ts,mts,cts}',
      '**/src/{components,pages,vx-pages}/**/types.{ts,mts,cts}'
    ],
    ignores: vxLintIgnores,
    rules: {
      '@vx-lint/props-name': ['error', { allowExportedProps: true }]
    }
  },
  {
    files: [
      '**/src/lib/**/index.{ts,js}',
      '**/src/{components,pages,store,hooks}/**/index.{ts,js}'
    ],
    ignores: vxLintIgnores,
    rules: { '@vx-lint/wildcard-barrel': 'error' }
  },
  {
    // wjdlz/NOTE: Keep this rule in sync with @vx-cli project generators.
    files: ['**/*-mock/**/*.{ts,tsx,cts,mts}'],
    rules: {
      '@typescript-eslint/no-explicit-any': 'off',
      'unused-imports/no-unused-vars': 'off',
      '@typescript-eslint/no-non-null-assertion': 'off'
    }
  },
  {
    files: ['**/*.ts', '**/*.tsx', '**/*.js', '**/*.jsx'],
    rules: {
      '@nx/enforce-module-boundaries': [
        'error',
        {
          enforceBuildableLibDependency: true,
          allow: ['^.*/eslint(\\.base)?\\.config\\.[cm]?[jt]s$'],
          depConstraints: [
            {
              sourceTag: '*',
              onlyDependOnLibsWithTags: ['*']
            }
          ]
        }
      ]
    }
  },
  {
    files: [
      '**/*.ts',
      '**/*.tsx',
      '**/*.cts',
      '**/*.mts',
      '**/*.js',
      '**/*.jsx',
      '**/*.cjs',
      '**/*.mjs'
    ],
    // Override or add rules here
    rules: {}
  }
  // --- wjdlz/TODO: review based on WS
  // ----------
  // --- wjdlz/NOTE(vx): skipped for internal tools
  // {
  //   files: ['vx/scripts/**/*.tsx', 'vx/scripts/**/*.ts'],
  //   rules: {
  //     '@typescript-eslint/no-unused-vars': 'off'
  //   }
  // },
  // {
  //   files: ['vx/scripts/helpers.ts'],
  //   rules: {
  //     '@typescript-eslint/no-explicit-any': 'off'
  //   }
  // }
  // --- * ---
  // {
  //   files: ['**/**/vite.config.ts'],
  //   rules: {
  //     '@typescript-eslint/no-unused-vars': 'off',
  //     'no-unused-vars': 'off'
  //   }
  // },
  // --- * ---
  // @components-store / @hooks-store
  // {
  //   // wjdlz/NOTE: ref - apps-internals/storybook/src/store/blogs/usePosts/index.ts
  //   files: ["**/**/src/store/**/**/*.ts"],
  //   rules: {
  //     "@tanstack/query/exhaustive-deps": "off",
  //     // 'react-hooks/rules-of-hooks': 'off',
  //     // '@nx/enforce-module-boundaries': 'off'
  //   },
  // },
  // {
  //   files: ["**/*.ts"],
  //   rules: {
  //     "react-hooks/rules-of-hooks": "off",
  //     "@tanstack/query/exhaustive-deps": "off",
  //   },
  // },
  // {
  //   plugins: {
  //     // prettier-ignore
  //     'react': react,
  //     'react-hooks': reactHooks
  //   },
  //   files: ['**/*.ts', '**/*.tsx', '**/*.js', '**/*.jsx'],
  //   // Override or add rules here
  //   rules: {
  //     'no-empty-function': 'off',
  //     '@typescript-eslint/no-empty-function': 'off',
  //     'react/jsx-no-useless-fragment': 'off',
  //     '@typescript-eslint/no-empty-object-type': 'off',
  //     'no-empty-object-type': 'off',
  //     // @wjdlz/ESFIX
  //     '@typescript-eslint/no-explicit-any': 'off',
  //     'no-unused-vars': 'off',
  //     '@typescript-eslint/no-unused-vars': 'off',
  //     '@typescript-eslint/no-non-null-assertion': 'off',
  //     // @wjdlz/ESFIX - testing
  //     'react-hooks/exhaustive-deps': 'off',
  //     '@typescript-eslint/no-unsafe-function-type': 'off',
  //     '@typescript-eslint/no-unused-expressions': 'off',
  //     '@typescript-eslint/ban-ts-comment': 'off',
  //     'prefer-const': 'off',
  //     '@typescript-eslint/no-empty-interface': 'off'
  //   }
  // }
]
