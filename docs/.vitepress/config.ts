import { defineConfig } from 'vitepress'

function normalizeBase(value: string | undefined): string {
  const trimmed = value?.trim()
  if (!trimmed || trimmed === '/') {
    return '/'
  }
  return `/${trimmed.replace(/^\/+|\/+$/g, '')}/`
}

const enStart = [
  { text: 'Getting Started', link: '/guide/getting-started' },
  { text: 'Profiles, Logins, and Settings', link: '/guide/concepts' },
  { text: 'Desktop Updates', link: '/guide/updates' }
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

const enSafety = [
  { text: 'Review and Switch', link: '/operations/switching' },
  { text: 'Recover or Undo', link: '/operations/recovery' },
  { text: 'Data and Security', link: '/reference/data-security' }
]

const enCLI = [
  { text: 'CLI Reference', link: '/reference/cli' },
  { text: 'Other Configuration Files', link: '/guide/generic-targets' }
]

const zhStart = [
  { text: '快速开始', link: '/zh/guide/getting-started' },
  { text: 'Profile、登录与设置', link: '/zh/guide/concepts' },
  { text: '桌面端更新', link: '/zh/guide/updates' }
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

const zhSafety = [
  { text: '审核并切换', link: '/zh/operations/switching' },
  { text: '恢复或撤销', link: '/zh/operations/recovery' },
  { text: '数据与安全', link: '/zh/reference/data-security' }
]

const zhCLI = [
  { text: 'CLI 参考', link: '/zh/reference/cli' },
  { text: '切换其他配置文件', link: '/zh/guide/generic-targets' }
]

export default defineConfig({
  base: normalizeBase(process.env.VITEPRESS_BASE),
  title: 'ProfileDeck',
  description: 'Review and switch local AI coding tool Profiles.',
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
      { text: 'Get Started', link: '/guide/getting-started' },
      { text: 'Codex', link: '/codex/profiles' },
      { text: 'Claude Code', link: '/claude-code/profiles' },
      { text: 'Antigravity', link: '/antigravity/profiles' },
      { text: 'Safety & Recovery', link: '/operations/switching' },
      { text: 'CLI & Advanced', link: '/reference/cli' }
    ],
    sidebar: [
      { text: 'Get Started', items: enStart },
      { text: 'Codex', items: enCodex },
      { text: 'Claude Code', items: enClaudeCode },
      { text: 'Antigravity', items: enAntigravity },
      { text: 'Safety & Recovery', items: enSafety },
      { text: 'CLI & Advanced', items: enCLI }
    ],
    footer: {
      message: 'Released under the Apache License 2.0.'
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
      description: '审核并切换本地 AI 编程工具 Profile。',
      themeConfig: {
        nav: [
          { text: '开始使用', link: '/zh/guide/getting-started' },
          { text: 'Codex', link: '/zh/codex/profiles' },
          { text: 'Claude Code', link: '/zh/claude-code/profiles' },
          { text: 'Antigravity', link: '/zh/antigravity/profiles' },
          { text: '安全与恢复', link: '/zh/operations/switching' },
          { text: 'CLI 与高级用法', link: '/zh/reference/cli' }
        ],
        sidebar: [
          { text: '开始使用', items: zhStart },
          { text: 'Codex', items: zhCodex },
          { text: 'Claude Code', items: zhClaudeCode },
          { text: 'Antigravity', items: zhAntigravity },
          { text: '安全与恢复', items: zhSafety },
          { text: 'CLI 与高级用法', items: zhCLI }
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
        },
        footer: {
          message: '基于 Apache License 2.0 发布。'
        }
      }
    }
  }
})
