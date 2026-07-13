import { defineConfig } from 'vitepress'

function normalizeBase(value: string | undefined): string {
  const trimmed = value?.trim()
  if (!trimmed || trimmed === '/') {
    return '/'
  }
  return `/${trimmed.replace(/^\/+|\/+$/g, '')}/`
}

const enGuide = [
  { text: 'Getting Started', link: '/guide/getting-started' },
  { text: 'Concepts', link: '/guide/concepts' },
  { text: 'Generic Targets', link: '/guide/generic-targets' }
]

const enCodex = [
  { text: 'Profiles', link: '/codex/profiles' },
  { text: 'Usage and Cost', link: '/codex/usage-cost' }
]

const enClaudeCode = [
  { text: 'Profiles', link: '/claude-code/profiles' }
]

const enAntigravity = [
  { text: 'Profiles', link: '/antigravity/profiles' }
]

const enOperations = [
  { text: 'Switching', link: '/operations/switching' },
  { text: 'Recovery', link: '/operations/recovery' }
]

const enReference = [
  { text: 'CLI Reference', link: '/reference/cli' },
  { text: 'Data and Security', link: '/reference/data-security' }
]

const zhGuide = [
  { text: '快速开始', link: '/zh/guide/getting-started' },
  { text: '核心概念', link: '/zh/guide/concepts' },
  { text: '通用目标文件', link: '/zh/guide/generic-targets' }
]

const zhCodex = [
  { text: 'Profile', link: '/zh/codex/profiles' },
  { text: '用量与成本', link: '/zh/codex/usage-cost' }
]

const zhClaudeCode = [
  { text: 'Profile', link: '/zh/claude-code/profiles' }
]

const zhAntigravity = [
  { text: 'Profile', link: '/zh/antigravity/profiles' }
]

const zhOperations = [
  { text: '切换流程', link: '/zh/operations/switching' },
  { text: '恢复操作', link: '/zh/operations/recovery' }
]

const zhReference = [
  { text: 'CLI 参考', link: '/zh/reference/cli' },
  { text: '数据与安全', link: '/zh/reference/data-security' }
]

export default defineConfig({
  base: normalizeBase(process.env.VITEPRESS_BASE),
  title: 'ProfileDeck',
  description: 'Safe profile switching for AI coding tools.',
  cleanUrls: true,
  lastUpdated: true,
  themeConfig: {
    search: {
      provider: 'local',
      options: {
        locales: {
          zh: {
            translations: {
              button: {
                buttonText: '搜索文档',
                buttonAriaLabel: '搜索文档'
              },
              modal: {
                noResultsText: '无法找到相关结果',
                resetButtonTitle: '清除查询条件',
                footer: {
                  selectText: '选择',
                  navigateText: '切换',
                  closeText: '关闭'
                }
              }
            }
          }
        }
      }
    },
    nav: [
      { text: 'Guide', link: '/guide/getting-started' },
      { text: 'Codex', link: '/codex/profiles' },
      { text: 'Claude Code', link: '/claude-code/profiles' },
      { text: 'Antigravity', link: '/antigravity/profiles' },
      { text: 'Operations', link: '/operations/switching' },
      { text: 'Reference', link: '/reference/cli' }
    ],
    sidebar: [
      { text: 'Guide', items: enGuide },
      { text: 'Codex', items: enCodex },
      { text: 'Claude Code', items: enClaudeCode },
      { text: 'Antigravity', items: enAntigravity },
      { text: 'Operations', items: enOperations },
      { text: 'Reference', items: enReference }
    ],
    footer: {
      message: 'Released under the MIT License.'
    }
  },
  locales: {
    root: {
      label: 'English',
      lang: 'en-US'
    },
    zh: {
      label: '简体中文',
      lang: 'zh-CN',
      link: '/zh/',
      description: '安全切换本地 AI 编程工具配置。',
      themeConfig: {
        nav: [
          { text: '指南', link: '/zh/guide/getting-started' },
          { text: 'Codex', link: '/zh/codex/profiles' },
          { text: 'Claude Code', link: '/zh/claude-code/profiles' },
          { text: 'Antigravity', link: '/zh/antigravity/profiles' },
          { text: '操作', link: '/zh/operations/switching' },
          { text: '参考', link: '/zh/reference/cli' }
        ],
        sidebar: [
          { text: '指南', items: zhGuide },
          { text: 'Codex', items: zhCodex },
          { text: 'Claude Code', items: zhClaudeCode },
          { text: 'Antigravity', items: zhAntigravity },
          { text: '操作', items: zhOperations },
          { text: '参考', items: zhReference }
        ],
        outline: {
          label: '页面导航'
        },
        docFooter: {
          prev: '上一页',
          next: '下一页'
        },
        lastUpdated: {
          text: '最后更新'
        }
      }
    }
  }
})
