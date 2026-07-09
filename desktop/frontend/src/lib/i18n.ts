import { addMessages, init, locale } from "svelte-i18n";

export type DesktopLanguage = "auto" | "zh-CN" | "en-US";
export type DesktopLocale = "zh-CN" | "en-US";

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
			savedPlaceholder: "Saved placeholder config",
		},
		profilePages: {
			loadingTitle: "Loading profile",
			loadingDescription: "Reading Codex profile state.",
			errorTitle: "Unable to load profile",
			rawAuthWarning: "auth.json contains local credentials. Edit only when you intend to change this profile credential payload.",
			new: {
				title: "New Codex profile",
				description: "Create a full-file profile from current config.toml and auth.json, or edit the loaded files before saving.",
			},
			detail: {
				title: "{profile}",
				description: "Review and sync this profile's desired config and hidden credential payload.",
				model: "Model",
				provider: "Provider",
				baseURL: "Base URL",
				account: "Account",
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
			},
			form: {
				profile: "Profile",
				profileDescription: "Profile metadata is stored by ProfileDeck. Config/auth content below is the desired Codex state.",
				profileID: "Profile ID",
				profileIDPlaceholder: "e.g. work",
				name: "Name",
				namePlaceholder: "e.g. Work",
				description: "Description",
				descriptionPlaceholder: "Optional",
				config: "config.toml",
				auth: "auth.json",
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
			savedPlaceholder: "已保存的占位配置",
		},
		profilePages: {
			loadingTitle: "正在加载 profile",
			loadingDescription: "正在读取 Codex profile 状态。",
			errorTitle: "无法加载 profile",
			rawAuthWarning: "auth.json 包含本地凭据。只有在明确要修改该 profile 的 credential payload 时才编辑它。",
			new: {
				title: "新建 Codex profile",
				description: "从当前 config.toml 和 auth.json 创建全量 profile，也可以在保存前编辑加载出的文件。",
			},
			detail: {
				title: "{profile}",
				description: "查看并同步这个 profile 的 desired config 和隐藏 credential payload。",
				model: "模型",
				provider: "Provider",
				baseURL: "Base URL",
				account: "账号",
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
			},
			form: {
				profile: "Profile",
				profileDescription: "Profile 元数据由 ProfileDeck 存储。下面的 config/auth 内容是 Codex desired state。",
				profileID: "Profile ID",
				profileIDPlaceholder: "例如 work",
				name: "名称",
				namePlaceholder: "例如 Work",
				description: "描述",
				descriptionPlaceholder: "可选",
				config: "config.toml",
				auth: "auth.json",
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
	if (typeof navigator !== "undefined" && navigator.language.toLowerCase().startsWith("zh")) {
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
