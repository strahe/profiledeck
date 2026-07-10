import { _, addMessages, init, locale } from "svelte-i18n";
import { get } from "svelte/store";

export type DesktopLanguage = "auto" | "zh-CN" | "en-US";
export type DesktopLocale = "zh-CN" | "en-US";
export type TranslationValues = Record<string, string | number | boolean | Date | null | undefined>;

const messages = {
	"en-US": {
		app: {
			dev: "dev",
			themeToDark: "Switch to dark mode",
			themeToLight: "Switch to light mode",
		},
		nav: {
			agents: "Agents",
			settings: "Settings",
		},
		status: {
			ready: "Ready",
			missing: "Missing",
			detected: "Detected",
			notDetected: "Not detected",
			active: "Active",
			loaded: "loaded",
			empty: "empty",
			error: "error",
			pending: "pending",
			warning: "Warning",
			enabled: "Enabled",
			disabled: "Disabled",
		},
		tabs: {
			profiles: "Profiles",
			health: "Health",
			usage: "Usage",
		},
		actions: {
			refresh: "Refresh",
			detect: "Detect",
			newProfile: "New Profile",
			createProfile: "Create Profile",
			reloadCurrent: "Reload Current",
			back: "Back",
			fork: "Fork",
			details: "Details",
			createFork: "Create Fork",
			syncProfile: "Sync Profile",
			cancel: "Cancel",
			useProfile: "Use Profile",
			saveProfile: "Save Profile",
			checkHealth: "Check Health",
			repairLock: "Repair Lock",
			sync: "Sync",
			more: "More actions",
			editDetails: "Edit details",
			updateFromCurrent: "Update from current Codex",
			saveChanges: "Save changes",
			updateProfile: "Update profile",
			continue: "Continue",
		},
		diagnostics: {
			codexService: "Codex service",
			summary: "dashboard {dashboard} · detect {detect} · profiles {profiles}",
			dashboardError: "dashboard error: {message}",
			detectError: "detect error: {message}",
			profilesError: "profiles error: {message}",
			usageError: "usage error: {message}",
			waiting: "Waiting for service data.",
			loading: "Loading service data.",
		},
		empty: {
			loadingProfilesTitle: "Loading profiles",
			loadingProfilesDescription: "Reading Codex profiles.",
			loadProfilesFailedTitle: "Unable to load profiles",
			agentNotDetectedTitle: "Agent not detected",
			agentNotDetectedDescription: "Install or configure {agent} to manage profiles.",
			noProfilesTitle: "No profiles yet",
			noProfilesDescription: 'Use "New Profile" to create a profile from current {agent} files.',
			allChecksPassedTitle: "All checks passed",
			allChecksPassedDescription: "No pending or failed operations.",
		},
		profile: {
			noDescription: "No description",
			noAccount: "No account",
			noActive: "No active profile",
			savedPlaceholder: "Saved placeholder config",
		},
		profilePages: {
			loadingTitle: "Loading profile",
			loadingDescription: "Reading Codex profile state.",
			errorTitle: "Unable to load profile",
			rawAuthWarning: "auth.json contains local credentials. Edit only when you intend to change this profile credential payload.",
			new: {
				title: "New Profile",
				description: "Create a Profile from the current Codex config.toml and auth.json files.",
			},
			list: {
				title: "Codex Profiles",
				description: "Switch between saved Codex configurations and credentials.",
				emptyDescription: "Create a Profile from the current Codex files to get started.",
				warningTitle: "Profile warning",
			},
			detail: {
				title: "{profile}",
				description: "Review this Profile's safe summary and targets.",
				name: "Name",
				model: "Model",
				provider: "Provider",
				baseURL: "Base URL",
				account: "Account",
				targetCount: "Targets",
				overview: "Overview",
				overviewDescription: "Profile metadata and detected Codex settings.",
				targets: "Targets",
				targetsDescription: "Redacted previews of the files managed by this Profile.",
				warningTitle: "Profile warning",
				syncOptions: "Sync options",
				syncDescription: "Reload Current replaces the editor with files currently on disk. Sync Profile saves the editor as this profile's latest desired state.",
				authUpdate: "Auth update",
				authUpdateDefault: "Default",
				authUpdateShared: "Update shared credential",
				authUpdateForkNew: "Fork to new credential",
				authUpdateHelp: "Choose an explicit mode when this profile shares a credential and auth.json changed.",
			},
			fork: {
				title: "Fork {profile}",
				description: "Create a new profile from the source profile.",
				authBinding: "Auth binding",
				authBindingDescription: "Choose whether the fork shares the source credential lifecycle or starts with a copied credential.",
				shareParent: "Share parent auth",
				shareParentDescription: "Future token refreshes are shared with the source profile.",
				copyNew: "Copy to new auth",
				copyNewDescription: "Start from the same auth payload, then keep future token refreshes independent.",
				sourceTitle: "Source Profile",
				sourceDescription: "A safe summary of the Profile being forked.",
				profileDescription: "Choose metadata for the new Profile.",
				copyName: "{profile} copy",
			},
			form: {
				profile: "Profile",
				profileDescription: "Profile metadata is stored by ProfileDeck; current Codex files stay outside the UI.",
				profileID: "Profile ID",
				profileIDPlaceholder: "e.g. work",
				profileIDHelp: "Up to 80 characters. Start with a lowercase letter or number; then use letters, numbers, dots, underscores, or dashes.",
				name: "Name",
				namePlaceholder: "e.g. Work",
				nameHelp: "Optional, up to 120 characters.",
				description: "Description",
				descriptionPlaceholder: "Optional",
				descriptionHelp: "Optional, up to 1,000 characters.",
				config: "config.toml",
				auth: "auth.json",
			},
			source: {
				title: "Current Codex source",
				description: "Only file paths and validation status are shown.",
				notReadyTitle: "Source is not ready",
				notReadyDescription: "Initialize ProfileDeck and provide valid config.toml and auth.json files before creating a Profile.",
				readyTitle: "Source is ready",
				readyDescription: "ProfileDeck will read both files directly when you create the Profile.",
				warningTitle: "Source warning",
			},
			validation: {
				idRequired: "Profile ID is required.",
				idTooLong: "Profile ID must be 80 characters or fewer.",
				idFormat: "Start with a lowercase letter or number and use only lowercase letters, numbers, dots, underscores, or dashes.",
				nameRequired: "Name is required.",
				nameTooLong: "Name must be 120 characters or fewer.",
				descriptionTooLong: "Description must be 1,000 characters or fewer.",
			},
			edit: {
				title: "Edit details",
				description: "Update the display name and description without changing stored Codex targets.",
			},
			sync: {
				title: "Update from current Codex",
				description: "Review the current source files before replacing this Profile's stored Codex state.",
				conflictTitle: "Choose credential behavior",
				conflictDescription: "The current auth.json differs from a credential shared by more than one Profile.",
				sourceNotReady: "Both current Codex files must be valid before this Profile can be updated.",
				replaceTitle: "Stored state will be replaced",
				replaceDescription: "ProfileDeck reads the current config.toml and auth.json directly. Raw content never enters this page.",
				authChoice: "Credential update",
				authChoiceDescription: "Choose how to handle the shared credential conflict.",
				updateShared: "Update shared credential",
				updateSharedDescription: "Apply the current auth.json to every Profile sharing this credential.",
				forkNew: "Create an independent credential",
				forkNewDescription: "Give only this Profile a new credential copied from the current auth.json.",
				errorTitle: "Unable to update Profile",
			},
		},
		useDialog: {
			title: 'Use "{profile}" for {agent}',
			description: "Replaces {agent} config targets. A backup is created first. Restart {agent} after switching.",
			planWarnings: "Plan warnings",
			unsupported: "This profile contains unsupported target changes.",
			building: "Building switch plan.",
			noChanges: "No target changes are planned.",
			before: "Before",
			after: "After",
			truncated: "Preview truncated",
			safetyTitle: "Safe switch",
			safetyDescription: "ProfileDeck creates a backup before applying atomic file updates. Restart Codex after switching.",
			reviewAgain: "Review the rebuilt plan",
			unsupportedTitle: "Unsupported operation",
			noChangesTitle: "No file changes",
			operationWarnings: "Target warnings",
		},
		planActions: {
			create: "Create",
			update: "Update",
			noop: "No change",
			unsupported: "Unsupported",
		},
		sourceStatus: {
			valid: "Valid",
			invalid: "Invalid",
			unreadable: "Unreadable",
			missing: "Missing",
		},
		health: {
			overall: "Overall",
			lock: "Lock",
			pending: "Pending",
			failed: "Failed",
			finding: "Finding",
			status: "Status",
			message: "Message",
		},
		usage: {
			events: "Events",
			inputTokens: "Input tokens",
			outputTokens: "Output tokens",
			cost: "Cost",
			configurePricing: "Configure pricing to estimate",
			importErrors: "{count} usage import errors were skipped.",
		},
		settings: {
			title: "Settings",
			description: "Desktop preferences.",
			language: {
				label: "Language",
				description: "Auto follows the operating system/browser language.",
				auto: "Auto",
				zhCN: "中文",
				enUS: "English",
			},
		},
			notice: {
			detected: {
				title: "Detected",
				codexDescription: "Codex paths verified.",
				placeholderDescription: "{agent} placeholder paths verified.",
			},
			refreshed: {
				title: "Refreshed",
				placeholderDescription: "{agent} placeholder state is up to date.",
			},
			profileSaved: {
				title: "Profile saved",
				codexDescription: "Codex config saved as {profile}.",
				placeholderDescription: "{agent} config saved as a reusable profile.",
			},
			profileCreated: {
				title: "Profile created",
				codexDescription: "Codex profile {profile} was created.",
			},
			profileForked: {
				title: "Profile forked",
				codexDescription: "Codex profile {profile} was forked.",
			},
			profileSynced: {
				title: "Profile synced",
				codexDescription: "Codex profile {profile} was synced.",
			},
			profileUpdated: {
				title: "Profile updated",
				description: "Profile details were saved.",
			},
			profileWarnings: {
				title: "Profile saved with warnings",
			},
			profileSwitched: {
				title: "Profile switched",
				codexDescription: "Codex now uses {profile}. Restart to take effect.",
				placeholderDescription: '{agent} now uses "{profile}". Restart to take effect.',
			},
			usageSynced: {
				title: "Usage synced",
				codexDescription: "Codex usage logs were parsed.",
				placeholderDescription: "{agent} placeholder usage logs were parsed.",
			},
			healthOK: {
				title: "Health OK",
				codexDescription: "Doctor check finished.",
				placeholderDescription: "No incomplete {agent} operations found.",
			},
			lockOK: {
				title: "Lock OK",
				noRepair: "No repair was necessary.",
				repaired: "Lock repair finished.",
			},
			settingsSaved: {
				title: "Settings saved",
				description: "Language preference updated.",
			},
		},
		errors: {
			desktopUnavailable: "Desktop services are unavailable.",
			profileNotReady: "The selected profile is not ready to use.",
			unsupportedTargets: "This profile cannot be used until unsupported target changes are resolved.",
			targetChanged: "Target files changed after preview. Review the updated plan before applying.",
			codexProfileNotFound: "Codex profile not found: {profile}",
		},
		time: {
			justNow: "Just now",
			minutesAgo: "{count}m ago",
			todayAt: "Today {time}",
		},
	},
	"zh-CN": {
		app: {
			dev: "开发",
			themeToDark: "切换到深色模式",
			themeToLight: "切换到浅色模式",
		},
		nav: {
			agents: "工具",
			settings: "设置",
		},
		status: {
			ready: "就绪",
			missing: "缺失",
			detected: "已检测",
			notDetected: "未检测",
			active: "当前",
			loaded: "已加载",
			empty: "为空",
			error: "错误",
			pending: "等待中",
			warning: "警告",
			enabled: "已启用",
			disabled: "已停用",
		},
		tabs: {
			profiles: "Profiles",
			health: "健康",
			usage: "用量",
		},
		actions: {
			refresh: "刷新",
			detect: "检测",
			newProfile: "新建 Profile",
			createProfile: "创建 Profile",
			reloadCurrent: "重新加载当前文件",
			back: "返回",
			fork: "Fork",
			details: "详情",
			createFork: "创建 Fork",
			syncProfile: "同步 Profile",
			cancel: "取消",
			useProfile: "使用 Profile",
			saveProfile: "保存 Profile",
			checkHealth: "检查健康状态",
			repairLock: "修复锁",
			sync: "同步",
			more: "更多操作",
			editDetails: "编辑详情",
			updateFromCurrent: "从当前 Codex 更新",
			saveChanges: "保存更改",
			updateProfile: "更新 Profile",
			continue: "继续",
		},
		diagnostics: {
			codexService: "Codex 服务",
			summary: "dashboard {dashboard} · 检测 {detect} · profiles {profiles}",
			dashboardError: "dashboard 错误：{message}",
			detectError: "detect 错误：{message}",
			profilesError: "profiles 错误：{message}",
			usageError: "usage 错误：{message}",
			waiting: "等待服务数据。",
			loading: "正在加载服务数据。",
		},
		empty: {
			loadingProfilesTitle: "正在加载 profiles",
			loadingProfilesDescription: "正在读取 Codex profiles。",
			loadProfilesFailedTitle: "无法加载 profiles",
			agentNotDetectedTitle: "未检测到 Agent",
			agentNotDetectedDescription: "安装或配置 {agent} 后才能管理 profiles。",
			noProfilesTitle: "还没有 profiles",
			noProfilesDescription: '使用"新建 Profile"从当前 {agent} 文件创建 profile。',
			allChecksPassedTitle: "全部检查通过",
			allChecksPassedDescription: "没有 pending 或 failed 操作。",
		},
		profile: {
			noDescription: "无描述",
			noAccount: "无账号",
			noActive: "没有当前 Profile",
			savedPlaceholder: "已保存的占位配置",
		},
		profilePages: {
			loadingTitle: "正在加载 profile",
			loadingDescription: "正在读取 Codex profile 状态。",
			errorTitle: "无法加载 profile",
			rawAuthWarning: "auth.json 包含本地凭据。只有在明确要修改该 profile 的 credential payload 时才编辑它。",
			new: {
				title: "新建 Profile",
				description: "从当前 Codex 的 config.toml 和 auth.json 创建 Profile。",
			},
			list: {
				title: "Codex Profiles",
				description: "在已保存的 Codex 配置与凭据之间安全切换。",
				emptyDescription: "从当前 Codex 文件创建第一个 Profile。",
				warningTitle: "Profile 警告",
			},
			detail: {
				title: "{profile}",
				description: "查看这个 Profile 的安全摘要和目标。",
				name: "名称",
				model: "模型",
				provider: "Provider",
				baseURL: "Base URL",
				account: "账号",
				targetCount: "目标数量",
				overview: "概览",
				overviewDescription: "Profile 元数据和检测到的 Codex 设置。",
				targets: "目标",
				targetsDescription: "此 Profile 管理文件的脱敏预览。",
				warningTitle: "Profile 警告",
				syncOptions: "同步选项",
				syncDescription: "重新加载当前文件会用磁盘上的文件替换编辑器内容；同步 Profile 会把编辑器内容保存为该 profile 的 latest desired state。",
				authUpdate: "Auth 更新",
				authUpdateDefault: "默认",
				authUpdateShared: "更新共享 credential",
				authUpdateForkNew: "分叉为新 credential",
				authUpdateHelp: "当该 profile 共享 credential 且 auth.json 发生变化时，需要显式选择模式。",
			},
			fork: {
				title: "Fork {profile}",
				description: "从源 profile 创建一个新的 profile。",
				authBinding: "Auth 绑定",
				authBindingDescription: "选择 fork 是否共享源 profile 的 credential 生命周期，或从复制出的 credential 开始独立维护。",
				shareParent: "共享父 auth",
				shareParentDescription: "未来 token refresh 会与源 profile 共享。",
				copyNew: "复制为新 auth",
				copyNewDescription: "从相同 auth payload 开始，但后续 token refresh 独立维护。",
				sourceTitle: "源 Profile",
				sourceDescription: "即将 Fork 的 Profile 安全摘要。",
				profileDescription: "设置新 Profile 的元数据。",
				copyName: "{profile} 副本",
			},
			form: {
				profile: "Profile",
				profileDescription: "Profile 元数据由 ProfileDeck 存储；当前 Codex 文件不会进入 UI。",
				profileID: "Profile ID",
				profileIDPlaceholder: "例如 work",
				profileIDHelp: "最多 80 个字符。首字符使用小写字母或数字，之后可使用字母、数字、点、下划线或短横线。",
				name: "名称",
				namePlaceholder: "例如 Work",
				nameHelp: "可选，最多 120 个字符。",
				description: "描述",
				descriptionPlaceholder: "可选",
				descriptionHelp: "可选，最多 1,000 个字符。",
				config: "config.toml",
				auth: "auth.json",
			},
			source: {
				title: "当前 Codex 来源",
				description: "这里只显示文件路径和校验状态。",
				notReadyTitle: "来源尚未就绪",
				notReadyDescription: "请先初始化 ProfileDeck，并确保 config.toml 和 auth.json 均有效。",
				readyTitle: "来源已就绪",
				readyDescription: "创建 Profile 时，ProfileDeck 会直接读取这两个文件。",
				warningTitle: "来源警告",
			},
			validation: {
				idRequired: "Profile ID 为必填项。",
				idTooLong: "Profile ID 不能超过 80 个字符。",
				idFormat: "首字符使用小写字母或数字，且只能包含小写字母、数字、点、下划线或短横线。",
				nameRequired: "名称为必填项。",
				nameTooLong: "名称不能超过 120 个字符。",
				descriptionTooLong: "描述不能超过 1,000 个字符。",
			},
			edit: {
				title: "编辑详情",
				description: "更新显示名称和描述，不改变已保存的 Codex 目标。",
			},
			sync: {
				title: "从当前 Codex 更新",
				description: "在替换此 Profile 保存的 Codex 状态前，先检查当前来源文件。",
				conflictTitle: "选择凭据处理方式",
				conflictDescription: "当前 auth.json 与多个 Profile 共享的凭据不同。",
				sourceNotReady: "必须确保当前两个 Codex 文件均有效，才能更新此 Profile。",
				replaceTitle: "已保存状态将被替换",
				replaceDescription: "ProfileDeck 会直接读取当前 config.toml 和 auth.json，原始内容不会进入此页面。",
				authChoice: "凭据更新",
				authChoiceDescription: "选择如何处理共享凭据冲突。",
				updateShared: "更新共享凭据",
				updateSharedDescription: "将当前 auth.json 应用到所有共享此凭据的 Profile。",
				forkNew: "创建独立凭据",
				forkNewDescription: "仅为当前 Profile 创建由当前 auth.json 复制出的新凭据。",
				errorTitle: "无法更新 Profile",
			},
		},
		useDialog: {
			title: '将 "{profile}" 用于 {agent}',
			description: "会替换 {agent} 的配置目标。切换前会先创建备份。切换后请重启 {agent}。",
			planWarnings: "Plan 警告",
			unsupported: "这个 profile 包含不支持的目标变更。",
			building: "正在构建切换 plan。",
			noChanges: "没有计划中的目标变更。",
			before: "切换前",
			after: "切换后",
			truncated: "预览已截断",
			safetyTitle: "安全切换",
			safetyDescription: "ProfileDeck 会先创建备份，再以原子方式更新文件。切换后请重启 Codex。",
			reviewAgain: "请重新审核更新后的计划",
			unsupportedTitle: "存在不支持的操作",
			noChangesTitle: "文件无需更改",
			operationWarnings: "目标警告",
		},
		planActions: {
			create: "创建",
			update: "更新",
			noop: "无变化",
			unsupported: "不支持",
		},
		sourceStatus: {
			valid: "有效",
			invalid: "无效",
			unreadable: "无法读取",
			missing: "缺失",
		},
		health: {
			overall: "总体",
			lock: "锁",
			pending: "等待中",
			failed: "失败",
			finding: "检查项",
			status: "状态",
			message: "消息",
		},
		usage: {
			events: "事件",
			inputTokens: "输入 tokens",
			outputTokens: "输出 tokens",
			cost: "成本",
			configurePricing: "配置 pricing 后可估算",
			importErrors: "已跳过 {count} 条 usage 导入错误。",
		},
		settings: {
			title: "设置",
			description: "桌面端偏好设置。",
			language: {
				label: "语言",
				description: "自动会跟随系统/浏览器语言。",
				auto: "自动",
				zhCN: "中文",
				enUS: "English",
			},
		},
			notice: {
			detected: {
				title: "已检测",
				codexDescription: "Codex 路径已验证。",
				placeholderDescription: "{agent} 占位路径已验证。",
			},
			refreshed: {
				title: "已刷新",
				placeholderDescription: "{agent} 占位状态已更新。",
			},
			profileSaved: {
				title: "Profile 已保存",
				codexDescription: "Codex 配置已保存为 {profile}。",
				placeholderDescription: "{agent} 配置已保存为可复用 profile。",
			},
			profileCreated: {
				title: "Profile 已创建",
				codexDescription: "Codex profile {profile} 已创建。",
			},
			profileForked: {
				title: "Profile 已 fork",
				codexDescription: "Codex profile {profile} 已 fork。",
			},
			profileSynced: {
				title: "Profile 已同步",
				codexDescription: "Codex profile {profile} 已同步。",
			},
			profileUpdated: {
				title: "Profile 已更新",
				description: "Profile 详情已保存。",
			},
			profileWarnings: {
				title: "Profile 已保存，但存在警告",
			},
			profileSwitched: {
				title: "Profile 已切换",
				codexDescription: "Codex 现在使用 {profile}。请重启后生效。",
				placeholderDescription: '{agent} 现在使用 "{profile}"。请重启后生效。',
			},
			usageSynced: {
				title: "Usage 已同步",
				codexDescription: "Codex usage logs 已解析。",
				placeholderDescription: "{agent} 占位 usage logs 已解析。",
			},
			healthOK: {
				title: "健康状态正常",
				codexDescription: "Doctor 检查已完成。",
				placeholderDescription: "没有发现未完成的 {agent} 操作。",
			},
			lockOK: {
				title: "锁状态正常",
				noRepair: "不需要修复。",
				repaired: "锁修复已完成。",
			},
			settingsSaved: {
				title: "设置已保存",
				description: "语言偏好已更新。",
			},
		},
		errors: {
			desktopUnavailable: "桌面服务不可用。",
			profileNotReady: "选中的 profile 还不能使用。",
			unsupportedTargets: "这个 profile 存在不支持的目标变更，解决后才能使用。",
			targetChanged: "预览后目标文件发生变化。请重新审核更新后的 plan。",
			codexProfileNotFound: "找不到 Codex profile：{profile}",
		},
		time: {
			justNow: "刚刚",
			minutesAgo: "{count} 分钟前",
			todayAt: "今天 {time}",
		},
	},
} as const;

let configured = false;

export function setupI18n() {
	if (configured) return;
	addMessages("en-US", messages["en-US"]);
	addMessages("zh-CN", messages["zh-CN"]);
	init({
		fallbackLocale: "en-US",
		initialLocale: resolveDesktopLocale("auto"),
	});
	configured = true;
}

export function normalizeDesktopLanguage(value: string | undefined | null): DesktopLanguage {
	if (value === "zh-CN" || value === "en-US" || value === "auto") return value;
	return "auto";
}

export function resolveDesktopLocale(value: DesktopLanguage): DesktopLocale {
	if (value === "zh-CN" || value === "en-US") return value;
	if (typeof navigator !== "undefined" && navigator.language?.toLowerCase().startsWith("zh")) {
		return "zh-CN";
	}
	return "en-US";
}

export function applyDesktopLanguagePreference(value: string | undefined | null): DesktopLanguage {
	const language = normalizeDesktopLanguage(value);
	const resolved = resolveDesktopLocale(language);
	locale.set(resolved);
	if (typeof document !== "undefined") {
		document.documentElement.lang = resolved;
	}
	return language;
}

export function translate(id: string, values?: TranslationValues): string {
	return String(get(_)(id, values ? { values } : undefined));
}

export function currentDesktopLocale(): DesktopLocale {
	const current = get(locale);
	return current === "zh-CN" ? "zh-CN" : "en-US";
}
