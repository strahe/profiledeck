package main

import (
	"strings"

	"github.com/strahe/profiledeck/internal/settings"
)

const (
	trayLocaleChangedEventName              = "profiledeck:locale-changed"
	trayDashboardUnavailableLabel           = "Dashboard unavailable. Open ProfileDeck for details."
	trayCodexProfilesUnavailableLabel       = "Unable to load Codex profiles. Open ProfileDeck for details."
	trayAntigravityProfilesUnavailableLabel = "Unable to load Antigravity profiles. Open ProfileDeck for details."
	trayClaudeCodeProfilesUnavailableLabel  = "Unable to load Claude Code profiles. Open ProfileDeck for details."
)

type trayLocale uint32

const (
	trayLocaleEnglish trayLocale = iota
	trayLocaleSimplifiedChinese
)

type trayMessages struct {
	profileDeckUnavailable   string
	dashboardUnavailable     string
	openProfileDeck          string
	runDoctor                string
	codexProfiles            string
	noCodexProfiles          string
	codexProfilesUnavailable string
	antigravityProfiles      string
	noAntigravityProfiles    string
	antigravityUnavailable   string
	claudeCodeProfiles       string
	noClaudeCodeProfiles     string
	claudeCodeUnavailable    string
	refreshMenu              string
	quit                     string
	missingActiveProfile     string
}

var trayEnglishMessages = trayMessages{
	profileDeckUnavailable:   "ProfileDeck: unavailable",
	dashboardUnavailable:     trayDashboardUnavailableLabel,
	openProfileDeck:          "Open ProfileDeck",
	runDoctor:                "Run Doctor",
	codexProfiles:            "Codex Profiles",
	noCodexProfiles:          "No Codex profiles",
	codexProfilesUnavailable: trayCodexProfilesUnavailableLabel,
	antigravityProfiles:      "Antigravity Profiles",
	noAntigravityProfiles:    "No Antigravity profiles",
	antigravityUnavailable:   trayAntigravityProfilesUnavailableLabel,
	claudeCodeProfiles:       "Claude Code Profiles",
	noClaudeCodeProfiles:     "No Claude Code profiles",
	claudeCodeUnavailable:    trayClaudeCodeProfilesUnavailableLabel,
	refreshMenu:              "Refresh Menu",
	quit:                     "Quit",
	missingActiveProfile:     "Missing %s profile: %s",
}

var traySimplifiedChineseMessages = trayMessages{
	profileDeckUnavailable:   "ProfileDeck：不可用",
	dashboardUnavailable:     "仪表盘不可用，请打开 ProfileDeck 查看详情。",
	openProfileDeck:          "打开 ProfileDeck",
	runDoctor:                "运行诊断",
	codexProfiles:            "Codex Profile",
	noCodexProfiles:          "没有 Codex Profile",
	codexProfilesUnavailable: "无法加载 Codex Profile，请打开 ProfileDeck 查看详情。",
	antigravityProfiles:      "Antigravity Profile",
	noAntigravityProfiles:    "没有 Antigravity Profile",
	antigravityUnavailable:   "无法加载 Antigravity Profile，请打开 ProfileDeck 查看详情。",
	claudeCodeProfiles:       "Claude Code Profile",
	noClaudeCodeProfiles:     "没有 Claude Code Profile",
	claudeCodeUnavailable:    "无法加载 Claude Code Profile，请打开 ProfileDeck 查看详情。",
	refreshMenu:              "刷新菜单",
	quit:                     "退出",
	missingActiveProfile:     "%s Profile 缺失：%s",
}

func parseTrayLocale(value string) (trayLocale, bool) {
	switch value {
	case settings.DesktopLanguageEnUS:
		return trayLocaleEnglish, true
	case settings.DesktopLanguageZhCN:
		return trayLocaleSimplifiedChinese, true
	default:
		return trayLocaleEnglish, false
	}
}

func resolveTrayLocale(preference, systemLanguage string) trayLocale {
	switch strings.TrimSpace(preference) {
	case settings.DesktopLanguageEnUS:
		return trayLocaleEnglish
	case settings.DesktopLanguageZhCN:
		return trayLocaleSimplifiedChinese
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(systemLanguage)), "zh") {
		return trayLocaleSimplifiedChinese
	}
	return trayLocaleEnglish
}

func messagesForTrayLocale(locale trayLocale) trayMessages {
	if locale == trayLocaleSimplifiedChinese {
		return traySimplifiedChineseMessages
	}
	return trayEnglishMessages
}
