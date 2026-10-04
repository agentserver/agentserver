window.__ModuleLoader__.load({
	id: "@deepseek-ai/dsh-client-ui-agent-preset",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		Object.defineProperty(exports, Symbol.toStringTag, { value: "Module" });
		let react = require("react");
		let _deepseek_ai_dsh_client_ui_primitives = require("@deepseek-ai/dsh-client-ui-primitives");
		let react_jsx_runtime = require("react/jsx-runtime");
		let _deepseek_ai_dsh_client_store = require("@deepseek-ai/dsh-client-store");
		//#region ../../util/values/src/index.ts
		/**
		* Weak-key lookup with a strongly retained iterable set of associated values.
		*
		* Each value must belong to only one key. The container performs no automatic
		* cleanup; owners delete associations or clear the container at lifecycle end.
		*/
		var WeakMapWithValues = class {
			keys = /* @__PURE__ */ new WeakMap();
			valueSet = /* @__PURE__ */ new Set();
			/** Live strongly retained values in insertion order. */
			values = this.valueSet;
			/**
			* Read the value associated with a key.
			* @param key - weakly held lookup key.
			* @returns the associated value, or absence.
			*/
			get(key) {
				return this.keys.get(key);
			}
			/**
			* Test whether a key has an association.
			* @param key - weakly held lookup key.
			* @returns whether the key is present.
			*/
			has(key) {
				return this.keys.has(key);
			}
			/**
			* Associate one key with one caller-unique value.
			* @param key - weakly held lookup key.
			* @param value - strongly retained value that belongs to no other key.
			* @returns this container.
			*/
			set(key, value) {
				if (this.keys.has(key)) {
					const previous = this.keys.get(key);
					if (previous === value) return this;
					this.valueSet.delete(previous);
				}
				this.keys.set(key, value);
				this.valueSet.add(value);
				return this;
			}
			/**
			* Remove one association and its strongly retained value.
			* @param key - weakly held lookup key.
			* @returns whether an association was removed.
			*/
			delete(key) {
				if (!this.keys.has(key)) return false;
				const value = this.keys.get(key);
				const deleted = this.keys.delete(key);
				this.valueSet.delete(value);
				return deleted;
			}
			/** Remove every association and strongly retained value. */
			clear() {
				this.keys = /* @__PURE__ */ new WeakMap();
				this.valueSet.clear();
			}
		};
		//#endregion
		//#region src/client/guide-locales.ts
		/** Curated help for shipped presets, kept separate from their short picker copy. */
		const guideEn = {
			modeExplanation: "Mode details",
			howToUse: "How to use",
			guideSections: "Guide sections",
			guideExampleTask: "Example task",
			guideCopy: "Copy",
			guideCopied: "Copied",
			guideFootnotes: "Footnotes",
			guideStandardIntro: "Choose Standard mode when starting a new task. Describe what you want to accomplish, point to the relevant files, and explain how to check the result.",
			guideStandardExplanation: [
				"### How it works",
				"The agent calls tools directly to read and edit files, search, and run terminal commands. It includes Skills, planning, goals, subagents, workflows, and context compaction.",
				"### When to choose it",
				"Start here for everyday coding, file work, and research. Standard mode can also write scripts and process files in batches. PTC changes how tool calls are organized; it is not required for batch tasks."
			].join("\n\n"),
			guideStandardUsage: [
				"### Fix a bug",
				"> Find out why submitting the search form twice makes the results disappear. Fix it and run the relevant tests. Explain the cause and what changed.",
				"Expected output: a code change, the relevant test results, and an explanation of the cause.",
				"### Organize project notes",
				"> Read the Markdown notes in this project. Summarize the agreed decisions and open questions, with links to the source files.",
				"Expected output: a summary with references that you can check against the original notes."
			].join("\n\n"),
			guidePtcIntro: "Choose PTC mode when starting a new task. Specify the input files, processing rules, and output format. The agent writes the code.",
			guidePtcExplanation: [
				"### How tools are called",
				"PTC means Programmatic Tool Calling. In this built-in preset, the agent uses run_code to write a TypeScript program that calls tools through a generated SDK. The program can use loops, conditions, error handling, and concurrent calls where appropriate.",
				"### What reaches the model",
				"Tool results first reach the program, which can filter and combine them. The model receives what the program prints or returns; image results are attached separately. Nested tool calls are still recorded and remain subject to tool permissions.",
				"### Compared with Standard mode",
				"Both modes can handle coding and batch tasks. Standard mode exposes individual tools directly; PTC organizes tool calls in code. The current PTC preset leaves the workflow tool disabled. Speed and token use depend on the task and how the program handles its results."
			].join("\n\n"),
			guidePtcUsage: [
				"### Check a set of configuration files",
				"> Check all JSON files in configs/. List missing required fields and invalid values against schema.json. Save a CSV with one row per issue. Include unreadable files in the report and keep checking the rest. Leave the original files unchanged.",
				"Expected output: an issue summary and a CSV report. The program can repeat the same checks, handle individual failures, and collect the results.",
				"### Summarize error logs",
				"> Analyze the log files in logs/. Group errors by service and error type. Show the ten most frequent groups and one example from each. Save the full counts to a CSV.",
				"Expected output: the top error groups and a complete count table. Intermediate data can be aggregated in the program before the summary reaches the model."
			].join("\n\n"),
			guideMinimalIntro: "Choose Minimal mode for a new task. For a comparison, hold the model, permissions, input, and starting workspace state constant across runs.",
			guideMinimalExplanation: [
				"### What is included",
				"One persistent shell tool and a fixed system prompt. The built-in preset does not load Skills, planning, context compaction, or the standard runtime context.",
				"### When to choose it",
				"Use it as a baseline for experiments and comparisons. It can still read files and execute scripts through shell commands, but offers fewer built-in ways to manage a long task. Fewer tools does not necessarily make it easier for a beginner."
			].join("\n\n"),
			guideMinimalUsage: [
				"### Compare performance on a small bug fix",
				"> Run the tests for this project, find the cause of the failure, and make the smallest fix. Run the relevant tests again and report the result.",
				"Run the same task separately in Standard and Minimal modes from the same starting state. Compare task completion, tool calls, and the resulting changes. Minimal mode performs the work through terminal commands."
			].join("\n\n"),
			guideCordisIntro: "Choose Creator mode for a new task. Describe the capability you want, where it should appear, and how you will verify it.",
			guideCordisExplanation: [
				"### What you can create",
				"Creator mode includes the standard task tools plus runtime inspection, persistent plugin management, and guidance for authoring Cordis plugins and agent presets. It can create a plugin that adds a capability or UI, or a preset that combines tools and prompts for a particular job.",
				"### Plugins and modes",
				"A plugin adds capabilities to DSH, such as a tool, a service connection, or a UI entry. A mode is an agent preset that selects tools and defines how the agent works in a task. A plugin can be included in a custom preset.",
				"### How the result takes effect",
				"Ask the agent to install and verify the result, not just generate source code. A plugin may load immediately or require a restart, depending on what it changes. A newly created preset is selected when starting a new task."
			].join("\n\n"),
			guideCordisUsage: [
				"### Add a UI",
				"> Create a DSH plugin that adds a project notes entry to the sidebar. Let me browse the Markdown files in this workspace and preview a selected note. Install it and verify that the page opens.",
				"Expected output: an installed plugin with a working entry and preview page, plus any remaining activation steps.",
				"### Add a tool",
				"> Create a plugin with a tool that reads this project’s test report and summarizes the failed tests. Register it and verify it with a sample report.",
				"Expected output: a plugin with a callable tool and a verified sample call.",
				"### Create my own mode",
				"> Create a “Code review” mode based on Standard mode. Have it prioritize potential bugs and test gaps, cite file paths and lines, and ask before modifying files. Save it as a selectable preset.",
				"Expected output: a custom preset for new tasks. These review instructions guide the agent; permission settings determine which actions it can execute."
			].join("\n\n")
		};
		/** Simplified Chinese help. Examples describe suggested tasks, not recorded runs. */
		const guideZh = {
			modeExplanation: "模式说明",
			howToUse: "如何使用",
			guideSections: "帮助内容",
			guideExampleTask: "示例任务",
			guideCopy: "复制",
			guideCopied: "已复制",
			guideFootnotes: "脚注",
			guideStandardIntro: "新建任务时选择「标准模式」，说明要完成什么、相关文件在哪里，以及怎样判断任务完成。",
			guideStandardExplanation: [
				"### 工作方式",
				"Agent 直接调用工具来读写文件、检索资料和执行终端命令。包含 Skills、计划、目标、子 Agent、工作流和上下文压缩等能力。",
				"### 什么时候选",
				"日常编程、文件处理和资料整理可以从这里开始。标准模式也能编写脚本、批量处理文件；PTC 改变的是工具调用方式，批量任务并不必须使用 PTC。"
			].join("\n\n"),
			guideStandardUsage: [
				"### 修复一个问题",
				"> 搜索表单连续提交两次后，结果会消失。请定位原因、修复问题并运行相关测试，最后说明原因和修改内容。",
				"预期产出：代码修改、相关测试结果，以及问题原因说明。",
				"### 整理项目资料",
				"> 阅读项目中的 Markdown 记录，整理已经达成的结论和仍待确认的问题，并附上对应文件链接。",
				"预期产出：一份带来源引用的总结，方便回到原文核对。"
			].join("\n\n"),
			guidePtcIntro: "新建任务时选择「PTC 模式」，说明输入文件、处理规则和输出格式。代码由 Agent 编写。",
			guidePtcExplanation: [
				"### 怎样调用工具",
				"PTC 是 Programmatic Tool Calling，即通过程序调用工具。当前内置预设让 Agent 通过 run_code 编写 TypeScript 程序，使用生成的工具 SDK 发起调用。程序可以组织循环、条件判断、错误处理，以及适合并发执行的调用。",
				"### 哪些结果交给模型",
				"工具返回的数据先交给程序，经过筛选、计算或合并，再通过输出或返回值交给模型；图片结果会另行附加。程序中的工具调用仍会被记录，也仍受工具权限约束。",
				"### 与标准模式的区别",
				"两种模式都能编程、批量处理文件。标准模式直接向模型提供各个工具；PTC 让模型用代码组织工具调用。当前 PTC 预设未启用 workflow 工具。速度和 token 用量取决于具体任务与结果处理方式。"
			].join("\n\n"),
			guidePtcUsage: [
				"### 批量检查配置文件",
				"> 检查 configs/ 下所有 JSON 文件，按照 schema.json 找出缺失字段和不合法的值。每个问题写成 CSV 中的一行；读取失败的文件也记入报告，继续检查其余文件。保留原文件。",
				"预期产出：问题汇总和一份 CSV 报告。程序可以对多份文件执行相同检查，处理单个文件的失败，再汇总结果。",
				"### 汇总错误日志",
				"> 分析 logs/ 下的日志，按服务和错误类型统计次数，列出出现最多的十类错误，每类保留一条示例。完整统计另存为 CSV。",
				"预期产出：高频错误摘要和完整统计表。中间数据可以先在程序中聚合，再把汇总交给模型。"
			].join("\n\n"),
			guideMinimalIntro: "新建任务时选择「极简模式」。做对照测试时，保持模型、权限、任务输入和工作区起始状态一致。",
			guideMinimalExplanation: [
				"### 保留哪些能力",
				"仅提供一个持久 Shell 工具，并使用固定系统提示词。内置预设不加载 Skills、计划、上下文压缩，也不注入标准运行时上下文。",
				"### 什么时候选",
				"适合作为实验和对照测试的基线。Agent 仍能通过终端命令读写文件、运行脚本，但缺少管理长任务的内置辅助能力。工具少，不代表对新手更容易。"
			].join("\n\n"),
			guideMinimalUsage: [
				"### 对比基础修复表现",
				"> 运行这个项目的测试，找出失败原因，做最小修复，再运行相关测试并报告结果。",
				"分别用标准模式和极简模式，从相同的工作区状态执行这条任务，对比完成情况、工具调用和最终修改。极简模式会通过终端命令完成这些操作。"
			].join("\n\n"),
			guideCordisIntro: "新建任务时选择「创造模式」，说明希望增加什么能力、从哪里使用，以及怎样验证效果。",
			guideCordisExplanation: [
				"### 可以创造什么",
				"创造模式具备标准任务工具，并增加运行时检查、持久化插件管理，以及 Cordis 插件和 Agent 预设的开发指引。可以编写插件来添加功能或界面，也可以组合工具和提示词，创建适合特定任务的模式。",
				"### 插件与模式的关系",
				"插件为 DSH 增加能力，例如工具、服务连接或界面入口。模式是一份 Agent 预设，用来选择任务可用的工具，并约定 Agent 的工作方式。自定义模式中也可以使用自己开发的插件。",
				"### 怎样让成果生效",
				"可以要求 Agent 完成安装并验证实际效果。插件可能即时加载，也可能需要重启，取决于修改内容；新建的模式在创建新任务时选择。"
			].join("\n\n"),
			guideCordisUsage: [
				"### 添加一个界面",
				"> 帮我写一个 DSH 插件，在侧栏增加「项目笔记」入口，列出当前工作区的 Markdown 文件，点击后能预览内容。完成安装并验证页面能打开。",
				"预期产出：带侧栏入口和预览页的插件，以及仍需完成的生效步骤。",
				"### 添加一个工具",
				"> 写一个插件，提供读取项目测试报告、汇总失败用例的工具。注册工具，并用一份示例报告验证调用结果。",
				"预期产出：可调用的新工具，以及一次示例调用的验证结果。",
				"### 创建自己的模式",
				"> 基于标准模式创建「代码审查」模式，优先检查潜在错误和测试缺口，指出文件与行号，修改文件前先询问我。保存成可选择的预设。",
				"预期产出：可在新任务中选择的自定义模式。审查要求用于指导 Agent，实际可执行的操作仍由权限设置决定。"
			].join("\n\n")
		};
		//#endregion
		//#region ../../preset/agent-preset-registry/src/display.ts
		const BUILT_IN_PRESET_KEYS = {
			standard: {
				name: "presetStandardName",
				description: "presetStandardDescription"
			},
			ptc: {
				name: "presetPtcName",
				description: "presetPtcDescription"
			},
			minimal: {
				name: "presetMinimalName",
				description: "presetMinimalDescription"
			},
			cordis: {
				name: "presetCordisName",
				description: "presetCordisDescription"
			}
		};
		/**
		* Whether a roster row is one of the shipped presets whose copy the dictionaries carry.
		* A shipped preset publishes no `name`; a declaration that names itself owns its copy.
		* @param preset - roster row.
		* @returns true for a shipped preset id without a published name.
		*/
		function isBuiltInPreset(preset) {
			return preset.name === void 0 && BUILT_IN_PRESET_KEYS[preset.id] !== void 0;
		}
		/**
		* Resolve preset display copy without making user-authored metadata translatable.
		* @param preset - roster row whose copy is being rendered.
		* @param t - active locale lookup covering {@link BuiltInPresetCopyKey}.
		* @returns localized copy for a known shipped preset, otherwise declaration metadata.
		*/
		function presetDisplayText(preset, t) {
			const keys = isBuiltInPreset(preset) ? BUILT_IN_PRESET_KEYS[preset.id] : void 0;
			if (keys !== void 0) return {
				name: t(keys.name),
				description: t(keys.description)
			};
			return {
				name: preset.name ?? preset.id,
				...preset.description === void 0 ? {} : { description: preset.description }
			};
		}
		//#endregion
		//#region src/client/locales.ts
		/** Locale bundles for the agent-preset hero chip, header label, and management section. */
		/** English copy. */
		const en = {
			...guideEn,
			builtInGroup: "Built-in",
			customGroup: "Custom",
			sectionIntro: "Choose the agent’s tools and how it works. Use Standard mode for everyday tasks, or Creator mode to add capabilities to DSH.",
			seatHint: "Choose the agent preset for your new task",
			headerHint: "The agent preset chosen when this task started",
			nav: "Agent presets",
			setDefault: "Set as new task default",
			view: "View configuration",
			presetStandardName: "Standard mode",
			presetStandardDescription: "Work with code, files, and information. Suitable for most tasks, with search, editing, terminal commands, and other tools available as needed.",
			presetPtcName: "PTC mode",
			presetPtcDescription: "Includes all Standard mode capabilities. Better suited to tasks that call tools in batches and then filter, organize, deduplicate, count, or summarize the results.",
			presetMinimalName: "Minimal mode",
			presetMinimalDescription: "The agent works using only a terminal tool. Useful for testing and comparing its basic performance.",
			presetCordisName: "Creator mode",
			presetCordisDescription: "Customize DSH through conversation. Let the agent write plugins that add features or UI, or combine tools and prompts to create your own mode.",
			inUse: "New task default",
			noDescription: "No description.",
			brokenBadge: "Failed to load",
			switchRefused: "Could not switch to {name}: {reason}",
			close: "Close",
			creatorDraft: "Let the agent help me create a preset",
			enableDevToolsToSetDefault: "Turn on Coding Tools in General settings to choose a default",
			enableDevToolsToCreate: "Turn on Coding Tools in General settings to start Creator mode"
		};
		/** Simplified Chinese copy. */
		const zh = {
			...guideZh,
			builtInGroup: "内置",
			customGroup: "自定义",
			sectionIntro: "选择 Agent 的工具和工作方式。日常任务用「标准模式」，扩展 DSH 的能力用「创造模式」。",
			seatHint: "选择新任务使用的 Agent 预设",
			headerHint: "本任务的 Agent 预设，在任务开始时确定",
			nav: "Agent 预设",
			setDefault: "设为新任务默认",
			view: "查看配置",
			presetStandardName: "标准模式",
			presetStandardDescription: "处理代码、文件和资料，适合大多数任务。Agent 会按需使用检索、编辑和终端等工具。",
			presetPtcName: "PTC 模式",
			presetPtcDescription: "包含标准模式的所有能力，更适合批量调用工具，并对结果进行筛选、整理、去重、统计或汇总的任务。",
			presetMinimalName: "极简模式",
			presetMinimalDescription: "Agent 仅使用终端工具完成任务，适合测试和对比其基础表现。",
			presetCordisName: "创造模式",
			presetCordisDescription: "用对话定制 DSH：让 Agent 编写插件，添加新功能或界面；也能组合工具和提示词，创建自己的模式。",
			inUse: "新任务默认",
			noDescription: "暂无描述。",
			brokenBadge: "加载失败",
			switchRefused: "无法切换到「{name}」：{reason}",
			close: "关闭",
			creatorDraft: "让 Agent 帮我创建预设模式",
			enableDevToolsToSetDefault: "请先在通用设置中开启代码工作工具，再设置默认值",
			enableDevToolsToCreate: "请先在通用设置中开启代码工作工具，再启动创造模式"
		};
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-agent-preset/src/client/AgentPresetLabel.module.css.mjs
		const css$3 = "._4I0PHW_label{border-radius:var(--dsw-radius-xs);background:var(--dsw-alias-fill-tsp-secondary);max-width:180px;height:22px;color:var(--dsw-alias-label-tertiary);white-space:nowrap;text-overflow:ellipsis;align-items:center;gap:4px;padding:0 2px 0 0;font-size:12px;line-height:22px;display:inline-flex;overflow:hidden}._4I0PHW_icon{opacity:.7;flex:none}@container (width<=540px){._4I0PHW_label{display:none}}";
		const tagId$3 = "@deepseek-ai/dsh-client-ui-agent-preset/AgentPresetLabel.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$3) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-agent-preset";
			tag.dataset.pluginCss = tagId$3;
			tag.textContent = css$3;
			document.head.appendChild(tag);
		}
		var AgentPresetLabel_module_css_default = {
			"icon": "_4I0PHW_icon",
			"label": "_4I0PHW_label"
		};
		//#endregion
		//#region src/client/AgentPresetLabel.tsx
		/**
		* The session header's agent-preset label.
		*
		* Read-only by construction: a session's composition is fixed once its
		* conversation starts, and a header is only worth reading after that. Offering
		* a control here would promise a switch the host refuses; naming what the
		* session runs is the honest affordance, and the choice itself lives on the
		* new-session screen ({@link AgentPresetSeat}).
		*/
		/**
		* Render this session's agent-preset name beside its title.
		* @param props - composed slot props.
		* @returns the label, or null when the session records no preset.
		*/
		function AgentPresetLabel({ sessionId, useSessions, useAgentPresets, load, t }) {
			const preset = useSessions((state) => {
				const value = state.byId[sessionId]?.projectionValues?.agentPreset;
				return typeof value === "string" ? value : void 0;
			});
			const options = useAgentPresets((state) => state.options);
			(0, react.useEffect)(() => {
				if (preset !== void 0) load();
			}, [preset, load]);
			if (preset === void 0) return null;
			const option = options.find((entry) => entry.id === preset);
			const text = option === void 0 ? void 0 : presetDisplayText(option, t);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
				className: AgentPresetLabel_module_css_default.label,
				title: text?.description ?? t("headerHint"),
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconAgentPresetOutlineRegular, {
					size: 14,
					className: AgentPresetLabel_module_css_default.icon
				}), text?.name ?? preset]
			});
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-agent-preset/src/client/AgentPresetSeat.module.css.mjs
		const css$2 = "._8bs5-G_menuAnchor{min-width:54px;max-width:100%}._8bs5-G_seat{border-radius:var(--dsw-radius-sm);min-width:0;max-width:min(100%,240px);min-height:28px;color:var(--dsw-alias-label-primary);white-space:nowrap;cursor:pointer;background:0 0;border:none;align-items:center;gap:4px;padding:0 8px;font-size:13px;font-weight:500;line-height:20px;display:inline-flex}._8bs5-G_seat:not(:disabled):hover,._8bs5-G_seat[aria-expanded=true]{background:var(--dsw-alias-interactive-bg-hover)}._8bs5-G_seat:disabled{cursor:default;color:var(--dsw-alias-label-quaternary)}._8bs5-G_seatIcon{color:var(--dsw-alias-label-primary);flex:none}._8bs5-G_seatLabel{text-overflow:ellipsis;white-space:nowrap;min-width:0;overflow:hidden}._8bs5-G_introIcon{animation:.15s cubic-bezier(.16,1,.3,1) both _8bs5-G_seat-icon-in}@keyframes _8bs5-G_seat-icon-in{0%{opacity:0;transform:scale(.5)}to{opacity:1;transform:scale(1)}}._8bs5-G_introText{white-space:pre;display:inline-block}._8bs5-G_introChar{white-space:pre;opacity:0;animation:.4s ease-out forwards _8bs5-G_seat-char-in;display:inline-block}@keyframes _8bs5-G_seat-char-in{0%{opacity:0;transform:translateY(4px)}to{opacity:1;transform:none}}@media (prefers-reduced-motion:reduce){._8bs5-G_introIcon,._8bs5-G_introChar{opacity:1;animation:none}}._8bs5-G_chevron{color:var(--dsw-alias-label-caption);flex:none}._8bs5-G_item{flex-direction:column;gap:2px;max-width:280px;display:flex}._8bs5-G_itemName{color:var(--dsw-alias-label-primary);font-size:13px;line-height:20px}._8bs5-G_itemDesc{color:var(--dsw-alias-label-caption);white-space:normal;font-size:12px;line-height:16px}";
		const tagId$2 = "@deepseek-ai/dsh-client-ui-agent-preset/AgentPresetSeat.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$2) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-agent-preset";
			tag.dataset.pluginCss = tagId$2;
			tag.textContent = css$2;
			document.head.appendChild(tag);
		}
		var AgentPresetSeat_module_css_default = {
			"chevron": "_8bs5-G_chevron",
			"introChar": "_8bs5-G_introChar",
			"introIcon": "_8bs5-G_introIcon",
			"introText": "_8bs5-G_introText",
			"item": "_8bs5-G_item",
			"itemDesc": "_8bs5-G_itemDesc",
			"itemName": "_8bs5-G_itemName",
			"menuAnchor": "_8bs5-G_menuAnchor",
			"seat": "_8bs5-G_seat",
			"seat-char-in": "_8bs5-G_seat-char-in",
			"seat-icon-in": "_8bs5-G_seat-icon-in",
			"seatIcon": "_8bs5-G_seatIcon",
			"seatLabel": "_8bs5-G_seatLabel"
		};
		//#endregion
		//#region src/client/AgentPresetSeat.tsx
		/**
		* The agent-preset chip on the new-session screen, beside the workspace
		* picker.
		*
		* It lives here rather than in the composer because the choice is only
		* available before a conversation starts: once a turn has run, the session's
		* history was produced under that preset's tools and the host refuses to swap
		* them. A control that spends most of its life disabled belongs on the screen
		* where it still works.
		*
		* The menu opens on the staged choice, which starts as the deployment default.
		* Picking stages; the choice reaches a session when one becomes current.
		*/
		const INTRO_TEXT_DELAY_MS = 150;
		const INTRO_CHAR_STAGGER_MS = 40;
		const INTRO_TEXT_REVEAL_MS = 200;
		const INTRO_CHAR_FADE_MS = 400;
		/** Duration of a selection-refusal banner, including a revision becoming unavailable during a pick. */
		const REFUSAL_HOLD_MS = 8e3;
		/**
		* Per-character start offset for the introduce reveal.
		* @param count - character count of the shown preset name.
		* @returns milliseconds between successive character starts.
		*/
		function introStaggerMs(count) {
			if (count <= 1) return 0;
			return Math.min(INTRO_CHAR_STAGGER_MS, INTRO_TEXT_REVEAL_MS / (count - 1));
		}
		/**
		* Render the new-session agent-preset chip.
		* @param props - composed slot props.
		* @returns The chip, or null outside the main view, with Developer tools off,
		* or before the roster provides a preset choice.
		*/
		function AgentPresetSeat({ sessionId, useSessionRetainInfo, load, select, introduced, useAgentPresetSeat, useDeveloperTools, t }) {
			const developerTools = useDeveloperTools((value) => value);
			const state = useAgentPresetSeat((snapshot) => snapshot);
			const main = useSessionRetainInfo((info) => sessionId === void 0 || (info?.retainedBy.mainView ?? 0) > 0);
			const [open, setOpen] = (0, react.useState)(false);
			const toastSeq = (0, react.useRef)(0);
			const [toast, setToast] = (0, react.useState)(null);
			const pickerVisible = (0, react.useRef)(developerTools);
			pickerVisible.current = developerTools;
			(0, react.useEffect)(() => {
				load();
			}, [load]);
			(0, react.useEffect)(() => {
				if (developerTools) return;
				setOpen(false);
				setToast(null);
			}, [developerTools]);
			const chosen = state.options.find((option) => option.id === state.current);
			const label = (chosen === void 0 ? void 0 : presetDisplayText(chosen, t))?.name ?? state.current;
			const ready = state.options.length > 0 && state.current !== "";
			const [introducing, setIntroducing] = (0, react.useState)(false);
			(0, react.useEffect)(() => {
				if (!state.introduce || !ready) return;
				const characters = Array.from(label);
				if (characters.length === 0 || window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
					introduced();
					return;
				}
				setIntroducing(true);
				const done = window.setTimeout(() => {
					setIntroducing(false);
					introduced();
				}, INTRO_TEXT_DELAY_MS + (characters.length - 1) * introStaggerMs(characters.length) + INTRO_CHAR_FADE_MS);
				return () => {
					window.clearTimeout(done);
				};
			}, [
				state.introduce,
				ready,
				label,
				introduced
			]);
			if (!main || !developerTools || !ready) return null;
			const characters = Array.from(label);
			const stagger = introStaggerMs(characters.length);
			const shownLabel = introducing ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
				className: AgentPresetSeat_module_css_default.introText,
				children: characters.map((character, index) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
					className: AgentPresetSeat_module_css_default.introChar,
					style: { animationDelay: `${INTRO_TEXT_DELAY_MS + index * stagger}ms` },
					children: character
				}, index))
			}) : label;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Menu, {
				open,
				onClose: () => {
					setOpen(false);
				},
				items: state.options.map((option) => {
					const text = presetDisplayText(option, t);
					return {
						id: option.id,
						label: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
							className: AgentPresetSeat_module_css_default.item,
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
								className: AgentPresetSeat_module_css_default.itemName,
								children: text.name
							}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
								className: AgentPresetSeat_module_css_default.itemDesc,
								children: text.description ?? t("noDescription")
							})]
						})
					};
				}),
				selectedId: state.current,
				onSelect: (id) => {
					setOpen(false);
					const picked = state.options.find((option) => option.id === id);
					/* v8 ignore next */
					const name = picked === void 0 ? id : presetDisplayText(picked, t).name;
					select(id).then((refusal) => {
						if (refusal === void 0 || !pickerVisible.current) return;
						toastSeq.current += 1;
						setToast({
							seq: toastSeq.current,
							text: t("switchRefused", {
								name,
								reason: refusal
							})
						});
					});
				},
				align: "start",
				portal: true,
				className: AgentPresetSeat_module_css_default.menuAnchor,
				anchor: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
					type: "button",
					className: AgentPresetSeat_module_css_default.seat,
					"aria-haspopup": "menu",
					"aria-expanded": open,
					title: state.error ?? t("seatHint"),
					disabled: state.busy,
					onClick: () => {
						setOpen((value) => !value);
					},
					children: [
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconAgentPresetOutlineRegular, { className: introducing ? `${AgentPresetSeat_module_css_default.seatIcon} ${AgentPresetSeat_module_css_default.introIcon}` : AgentPresetSeat_module_css_default.seatIcon }),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: AgentPresetSeat_module_css_default.seatLabel,
							children: shownLabel
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronDownOutlineRegular, { className: AgentPresetSeat_module_css_default.chevron })
					]
				})
			}), toast !== null && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Toast, {
				text: toast.text,
				icon: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconWarningOutlineRegular, {}),
				holdMs: REFUSAL_HOLD_MS,
				anchor: document.querySelector("[data-composer-card]"),
				onDone: () => {
					setToast(null);
				}
			}, toast.seq)] });
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-agent-preset/src/client/PresetGuideDialog.module.css.mjs
		const css$1 = ".OqhVfW_guideDialog{box-sizing:border-box;gap:0;width:min(640px,100%);height:min(600px,100%);max-height:100%;padding:0}.OqhVfW_guideLayout{flex-direction:column;height:100%;min-height:0;display:flex}.OqhVfW_guideHeader{flex:none;padding:24px 24px 20px 28px}.OqhVfW_guideTitleRow{justify-content:space-between;align-items:center;gap:16px;display:flex}.OqhVfW_guideTitle{margin:0;font-size:20px;font-weight:600;line-height:28px}.OqhVfW_guideIntro{color:var(--dsw-alias-label-secondary);margin:8px 0 0;font-size:14px;line-height:22px}.OqhVfW_guideClose{border-radius:var(--dsw-radius-md);width:32px;height:32px;min-height:32px;color:var(--dsw-alias-label-tertiary);padding:0}.OqhVfW_guideTabs{flex:none;margin:0 28px 22px}.OqhVfW_guidePanel{--dsh-scrollbar-thumb:var(--dsw-alias-scrollbar-bg-l2);--dsh-scrollbar-thumb-hover:var(--dsw-alias-scrollbar-hover-l2);overscroll-behavior:contain;scrollbar-gutter:stable;min-height:0;color:var(--dsw-alias-label-primary);overflow-wrap:anywhere;flex:1;padding:0 24px 24px 28px;font-size:16px;overflow-y:auto}.OqhVfW_guidePanel:focus-visible{outline:var(--dsw-focus-ring-width) solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:-2px}.OqhVfW_guidePanel h3{margin:28px 0 10px;font-size:16px;font-weight:600;line-height:24px}.OqhVfW_guidePanel h3:first-child{margin-top:0}.OqhVfW_guidePanel p{color:var(--dsw-alias-label-primary);margin:0 0 16px;font-size:16px;line-height:1.625}.OqhVfW_guidePanel blockquote{border-radius:var(--dsw-radius-lg);background:var(--dsw-alias-bg-module-platform);border:0;margin:10px 0 14px;padding:14px 16px}.OqhVfW_guidePanel blockquote p{color:var(--dsw-alias-label-primary);margin:0}.OqhVfW_guidePanel[data-guide-page=usage] p{color:var(--dsw-alias-label-secondary);margin-bottom:12px;font-size:14px;line-height:22px}.OqhVfW_guidePanel[data-guide-page=usage] h3{margin:0}.OqhVfW_guideExample+.OqhVfW_guideExample{margin-top:28px}.OqhVfW_guideExampleHeader{flex-wrap:wrap;align-items:center;gap:8px 10px;margin-bottom:8px;display:flex}.OqhVfW_guideExampleTag{flex-shrink:0;gap:4px}.OqhVfW_guidePanel[data-guide-page=usage] blockquote{margin:8px 0;padding:12px 16px}.OqhVfW_guidePanel[data-guide-page=usage] blockquote p{color:var(--dsw-alias-label-primary);margin:0;font-size:16px;line-height:26px}";
		const tagId$1 = "@deepseek-ai/dsh-client-ui-agent-preset/PresetGuideDialog.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$1) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-agent-preset";
			tag.dataset.pluginCss = tagId$1;
			tag.textContent = css$1;
			document.head.appendChild(tag);
		}
		var PresetGuideDialog_module_css_default = {
			"guideClose": "OqhVfW_guideClose",
			"guideDialog": "OqhVfW_guideDialog",
			"guideExample": "OqhVfW_guideExample",
			"guideExampleHeader": "OqhVfW_guideExampleHeader",
			"guideExampleTag": "OqhVfW_guideExampleTag",
			"guideHeader": "OqhVfW_guideHeader",
			"guideIntro": "OqhVfW_guideIntro",
			"guideLayout": "OqhVfW_guideLayout",
			"guidePanel": "OqhVfW_guidePanel",
			"guideTabs": "OqhVfW_guideTabs",
			"guideTitle": "OqhVfW_guideTitle",
			"guideTitleRow": "OqhVfW_guideTitleRow"
		};
		//#endregion
		//#region src/client/PresetGuideDialog.tsx
		/** Read-only help stays local to Settings and never changes the selected preset. */
		const guides = new Map([
			["standard", {
				name: "presetStandardName",
				intro: "guideStandardIntro",
				explanation: "guideStandardExplanation",
				usage: "guideStandardUsage"
			}],
			["ptc", {
				name: "presetPtcName",
				intro: "guidePtcIntro",
				explanation: "guidePtcExplanation",
				usage: "guidePtcUsage"
			}],
			["minimal", {
				name: "presetMinimalName",
				intro: "guideMinimalIntro",
				explanation: "guideMinimalExplanation",
				usage: "guideMinimalUsage"
			}],
			["cordis", {
				name: "presetCordisName",
				intro: "guideCordisIntro",
				explanation: "guideCordisExplanation",
				usage: "guideCordisUsage"
			}]
		]);
		/**
		* Look up help only for known, shipped presets.
		* @param id - preset identifier from the roster.
		* @param trust - roster source; custom presets own their capability claims.
		* @returns the shipped guide, or undefined for unknown and custom presets.
		*/
		function presetGuide(id, trust) {
			return trust === "system" ? guides.get(id) : void 0;
		}
		/** Keep keyboard focus inside a preset reader while Tab moves through its controls.
		* @param event Keyboard event from the active reader.
		*/
		function trapPresetReaderTab(event) {
			if (event.key !== "Tab") return;
			const targets = Array.from(event.currentTarget.querySelectorAll("button:not([disabled]):not([tabindex=\"-1\"]), [tabindex=\"0\"]")).filter((element) => !element.closest("[hidden]"));
			const first = targets[0];
			const last = targets[targets.length - 1];
			if (event.shiftKey && document.activeElement === first) {
				event.preventDefault();
				last?.focus();
			} else if (!event.shiftKey && document.activeElement === last) {
				event.preventDefault();
				first?.focus();
			}
		}
		/** Curated usage dictionaries contain only level-three example sections. */
		function GuideUsage({ text, t }) {
			const labels = {
				code: {
					copyLabel: t("guideCopy"),
					copiedLabel: t("guideCopied"),
					toolbarLabels: {
						codeLabel: t("codeBlock.title"),
						wrapLabel: t("codeBlock.wrap"),
						unwrapLabel: t("codeBlock.unwrap")
					}
				},
				footnotes: t("guideFootnotes")
			};
			return text.split(/(?=^### )/m).map((section) => {
				const headingEnd = section.indexOf("\n");
				const title = section.slice(4, headingEnd);
				return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
					className: PresetGuideDialog_module_css_default.guideExample,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: PresetGuideDialog_module_css_default.guideExampleHeader,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("h3", { children: title }), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(_deepseek_ai_dsh_client_ui_primitives.Tag, {
							tone: "neutral",
							className: PresetGuideDialog_module_css_default.guideExampleTag,
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconListPenOutlineRegular, { size: 12 }), t("guideExampleTask")]
						})]
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.MarkdownText, {
						text: section.slice(headingEnd + 1),
						labels
					})]
				}, title);
			});
		}
		/**
		* Open read-only help without changing the selected preset.
		* @param props - localized guide, initial page, and close callback.
		* @returns the modal reader with independent scroll positions for each page.
		*/
		function PresetGuideDialog({ guide, initialPage, t, onClose }) {
			const [page, setPage] = (0, react.useState)(initialPage);
			const guideId = (0, react.useId)();
			const content = (0, react.useRef)(null);
			(0, react.useLayoutEffect)(() => {
				content.current?.querySelector("[role=\"tab\"][aria-selected=\"true\"]")?.focus();
			}, []);
			const onKeyDown = (event) => {
				if (event.key === "Escape") {
					event.preventDefault();
					event.stopPropagation();
					onClose();
				} else trapPresetReaderTab(event);
			};
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Modal, {
				open: true,
				headless: true,
				onClose,
				title: t(guide.name),
				className: PresetGuideDialog_module_css_default.guideDialog,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					ref: content,
					className: PresetGuideDialog_module_css_default.guideLayout,
					role: "presentation",
					onKeyDownCapture: onKeyDown,
					children: [
						/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							className: PresetGuideDialog_module_css_default.guideHeader,
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
								className: PresetGuideDialog_module_css_default.guideTitleRow,
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("h2", {
									className: PresetGuideDialog_module_css_default.guideTitle,
									children: t(guide.name)
								}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
									variant: "ghost",
									className: PresetGuideDialog_module_css_default.guideClose,
									"aria-label": t("close"),
									onClick: onClose,
									children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconCloseOutlineRegular, { size: 18 })
								})]
							}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
								className: PresetGuideDialog_module_css_default.guideIntro,
								children: t(guide.intro)
							})]
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.SegmentedTabs, {
							className: PresetGuideDialog_module_css_default.guideTabs,
							label: t("guideSections"),
							value: page,
							onChange: setPage,
							items: [{
								value: "explanation",
								label: t("modeExplanation"),
								id: `${guideId}-explanation-tab`,
								panelId: `${guideId}-explanation-panel`
							}, {
								value: "usage",
								label: t("howToUse"),
								id: `${guideId}-usage-tab`,
								panelId: `${guideId}-usage-panel`
							}]
						}),
						["explanation", "usage"].map((section) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							id: `${guideId}-${section}-panel`,
							role: "tabpanel",
							"aria-labelledby": `${guideId}-${section}-tab`,
							className: PresetGuideDialog_module_css_default.guidePanel,
							"data-guide-page": section,
							hidden: page !== section,
							tabIndex: 0,
							children: section === "usage" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(GuideUsage, {
								text: t(guide.usage),
								t
							}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.MarkdownText, {
								text: t(guide.explanation),
								labels: {
									code: {
										copyLabel: t("guideCopy"),
										copiedLabel: t("guideCopied"),
										toolbarLabels: {
											codeLabel: t("codeBlock.title"),
											wrapLabel: t("codeBlock.wrap"),
											unwrapLabel: t("codeBlock.unwrap")
										}
									},
									footnotes: t("guideFootnotes")
								}
							})
						}, section))
					]
				})
			});
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-agent-preset/src/client/AgentPresetSection.module.css.mjs
		const css = ".s_Tn4W_section{max-width:720px;color:var(--dsw-alias-label-primary);flex-direction:column;gap:12px;display:flex}.s_Tn4W_title{margin:0;font-size:18px;font-weight:600}.s_Tn4W_intro{color:var(--dsw-alias-label-tertiary);margin:0;font-size:13px}.s_Tn4W_group{flex-direction:column;gap:10px;display:flex}.s_Tn4W_group+.s_Tn4W_group{margin-top:20px}.s_Tn4W_groupHead{letter-spacing:.06em;text-transform:uppercase;color:var(--dsw-alias-label-tertiary);margin:0;font-size:12px;font-weight:600}.s_Tn4W_cards{grid-template-columns:repeat(auto-fill,minmax(268px,1fr));gap:12px;margin:0;padding:0;list-style:none;display:grid}.s_Tn4W_card{border:.5px solid var(--dsw-alias-settings-card-stroke);border-radius:var(--dsw-radius-xl);background:var(--dsw-alias-settings-card-fill);flex-direction:column;transition:border-color .16s,background .16s;display:flex}.s_Tn4W_card:hover:not(.s_Tn4W_cardActive):not(.s_Tn4W_cardSelectionDisabled){background:var(--dsw-alias-interactive-bg-hover)}.s_Tn4W_cardActive{background:var(--dsw-alias-bg-module-platform);border-color:var(--dsw-static-neutral-bluish-400)}.s_Tn4W_cardSelectionDisabled{background:var(--dsw-alias-bg-module-platform)}.s_Tn4W_cardSelectionDisabled .s_Tn4W_cardMain{opacity:.48}.s_Tn4W_cardBroken,.s_Tn4W_cardBroken:hover{border-color:var(--dsw-alias-state-error-primary)}.s_Tn4W_brokenBadge{corner-shape:round;white-space:nowrap;background:var(--dsw-alias-state-error-primary);color:var(--dsw-alias-bg-layer-3);border-radius:999px;padding:1px 8px;font-size:11px;font-weight:500;line-height:17px}.s_Tn4W_brokenTip{z-index:1;border-radius:var(--dsw-radius-sm);background:var(--dsw-alias-label-primary);width:max-content;max-width:100%;color:var(--dsw-alias-bg-layer-3);text-align:left;white-space:pre-line;overflow-wrap:anywhere;opacity:0;pointer-events:none;padding:6px 8px;font-size:11px;font-weight:400;line-height:1.5;transition:opacity .12s;position:absolute;top:calc(100% + 6px);left:0}.s_Tn4W_brokenBadge:hover .s_Tn4W_brokenTip,.s_Tn4W_cardMain:focus-visible .s_Tn4W_brokenTip{opacity:1}.s_Tn4W_cardMain[aria-disabled=true]{cursor:default}.s_Tn4W_cardBrokenReason{clip:rect(0 0 0 0);white-space:nowrap;width:1px;height:1px;position:absolute;overflow:hidden}.s_Tn4W_cardMain{appearance:none;font:inherit;color:inherit;text-align:left;cursor:pointer;border-radius:var(--dsw-radius-xl) var(--dsw-radius-xl) 0 0;background:0 0;border:0;flex-direction:column;flex:1;gap:12px;padding:14px 16px 12px;display:flex}.s_Tn4W_cardMain:disabled{cursor:default}.s_Tn4W_cardMain:focus-visible{outline:var(--dsw-focus-ring-width) solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:-2px}.s_Tn4W_cardHead{align-items:flex-start;gap:12px;display:flex;position:relative}.s_Tn4W_cardIdentity{flex:1;align-items:center;gap:6px;min-width:0;display:flex}.s_Tn4W_cardName{text-overflow:ellipsis;white-space:nowrap;min-width:0;font-size:15px;font-weight:600;line-height:1.4;overflow:hidden}.s_Tn4W_cardDesc{color:var(--dsw-alias-label-secondary);-webkit-line-clamp:4;overflow-wrap:anywhere;-webkit-box-orient:vertical;margin-block:auto;font-size:13px;line-height:1.55;display:-webkit-box;overflow:hidden}.s_Tn4W_cardId{max-width:35%;font-family:var(--dsw-font-mono,ui-monospace, SFMono-Regular, Menlo, monospace);color:var(--dsw-alias-label-tertiary);text-overflow:ellipsis;white-space:nowrap;flex-shrink:0;font-size:11px;line-height:21px;overflow:hidden}.s_Tn4W_cardFoot{border-top:.5px solid var(--dsw-alias-border-l2);flex-wrap:wrap;justify-content:flex-end;align-items:center;gap:2px;padding:6px 10px;display:flex}.s_Tn4W_cardHelp{align-items:center;gap:4px;margin-right:auto;display:flex}.s_Tn4W_helpButton{min-height:28px;color:var(--dsw-alias-label-tertiary);padding:5px 6px;font-size:12px;font-weight:400}.s_Tn4W_helpButton:hover,.s_Tn4W_helpButton:focus-visible{color:var(--dsw-alias-label-primary)}.s_Tn4W_iconButton{appearance:none;border-radius:var(--dsw-radius-sm);color:var(--dsw-alias-label-tertiary);cursor:pointer;background:0 0;border:0;align-items:center;padding:6px;display:inline-flex;position:relative}.s_Tn4W_iconButton:hover{background:var(--dsw-alias-bg-layer-1);color:var(--dsw-alias-label-primary)}.s_Tn4W_iconButton:focus-visible{outline:var(--dsw-focus-ring-width) solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:-1px}.s_Tn4W_iconButton:after{content:attr(data-tip);border-radius:var(--dsw-radius-sm);background:var(--dsw-alias-label-primary);color:var(--dsw-alias-bg-layer-3);white-space:nowrap;opacity:0;pointer-events:none;padding:3px 8px;font-size:11px;line-height:17px;transition:opacity .12s;position:absolute;bottom:calc(100% + 6px);left:50%;transform:translate(-50%)}.s_Tn4W_iconButton:hover:after,.s_Tn4W_iconButton:focus-visible:after{opacity:1}.s_Tn4W_dialog{width:min(720px,100%)}.s_Tn4W_viewerCode{border:.5px solid var(--dsw-alias-border-l4);border-radius:var(--dsw-radius-lg);background:var(--dsw-alias-bg-layer-2);max-height:min(60vh,560px);color:var(--dsw-alias-label-secondary);font-family:var(--dsw-font-mono,ui-monospace, SFMono-Regular, Menlo, monospace);white-space:pre;tab-size:2;--dsh-scrollbar-thumb:var(--dsw-alias-scrollbar-bg-l2);--dsh-scrollbar-thumb-hover:var(--dsw-alias-scrollbar-hover-l2);margin:0;padding:12px;font-size:12.5px;line-height:1.5;overflow:auto}.s_Tn4W_error{color:var(--dsw-alias-state-error-primary);margin:0;font-size:12px}.s_Tn4W_creatorButton{box-sizing:border-box;border:1px dashed var(--dsw-alias-border-l3);border-radius:var(--dsw-radius-lg);height:44px;font:inherit;color:var(--dsw-alias-label-primary);cursor:pointer;background:0 0;justify-content:center;align-self:stretch;align-items:center;gap:6px;font-size:14px;line-height:22px;display:flex}.s_Tn4W_creatorButton:hover:not(:disabled){background:var(--dsw-alias-interactive-bg-hover)}.s_Tn4W_creatorButton:disabled{opacity:.4;cursor:default}";
		const tagId = "@deepseek-ai/dsh-client-ui-agent-preset/AgentPresetSection.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-agent-preset";
			tag.dataset.pluginCss = tagId;
			tag.textContent = css;
			document.head.appendChild(tag);
		}
		var AgentPresetSection_module_css_default = {
			"brokenBadge": "s_Tn4W_brokenBadge",
			"brokenTip": "s_Tn4W_brokenTip",
			"card": "s_Tn4W_card",
			"cardActive": "s_Tn4W_cardActive",
			"cardBroken": "s_Tn4W_cardBroken",
			"cardBrokenReason": "s_Tn4W_cardBrokenReason",
			"cardDesc": "s_Tn4W_cardDesc",
			"cardFoot": "s_Tn4W_cardFoot",
			"cardHead": "s_Tn4W_cardHead",
			"cardHelp": "s_Tn4W_cardHelp",
			"cardId": "s_Tn4W_cardId",
			"cardIdentity": "s_Tn4W_cardIdentity",
			"cardMain": "s_Tn4W_cardMain",
			"cardName": "s_Tn4W_cardName",
			"cardSelectionDisabled": "s_Tn4W_cardSelectionDisabled",
			"cards": "s_Tn4W_cards",
			"creatorButton": "s_Tn4W_creatorButton",
			"dialog": "s_Tn4W_dialog",
			"error": "s_Tn4W_error",
			"group": "s_Tn4W_group",
			"groupHead": "s_Tn4W_groupHead",
			"helpButton": "s_Tn4W_helpButton",
			"iconButton": "s_Tn4W_iconButton",
			"intro": "s_Tn4W_intro",
			"section": "s_Tn4W_section",
			"title": "s_Tn4W_title",
			"viewerCode": "s_Tn4W_viewerCode"
		};
		//#endregion
		//#region src/client/AgentPresetSection.tsx
		function CardDescription({ text }) {
			const ref = (0, react.useRef)(null);
			const [truncated, setTruncated] = (0, react.useState)(false);
			(0, react.useLayoutEffect)(() => {
				const el = ref.current;
				/* v8 ignore next -- the ref is attached before layout effects run. */
				if (el === null) return;
				const measure = () => {
					setTruncated(el.scrollHeight > el.clientHeight);
				};
				measure();
				if (typeof ResizeObserver === "undefined") return;
				const observer = new ResizeObserver(measure);
				observer.observe(el);
				return () => {
					observer.disconnect();
				};
			}, [text]);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
				label: text,
				side: "bottom",
				delayMs: 400,
				disabled: !truncated,
				maxWidth: 360,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
					ref,
					className: AgentPresetSection_module_css_default.cardDesc,
					title: "",
					children: text
				})
			});
		}
		/** Render the roster with its default, mode help, composition viewer, and the guidance to Creator mode.
		* @param props Settings actions, snapshot hooks and localized text.
		* @returns The preset settings section.
		*/
		function AgentPresetSection({ useAgentPresetSection, load, view, closeView, makeDefault, startCreatorDraft, close: closeSettings, useDeveloperTools, t }) {
			const state = useAgentPresetSection((value) => value);
			const developerTools = useDeveloperTools((enabled) => enabled);
			const [guide, setGuide] = (0, react.useState)(null);
			const viewTrigger = (0, react.useRef)(null);
			const closeViewOnUnmount = (0, react.useRef)(closeView);
			(0, react.useEffect)(() => {
				load();
			}, [load]);
			(0, react.useLayoutEffect)(() => {
				closeViewOnUnmount.current = closeView;
			}, [closeView]);
			(0, react.useEffect)(() => () => {
				closeViewOnUnmount.current();
			}, []);
			const closeViewer = () => {
				closeView();
				viewTrigger.current?.focus();
			};
			const viewed = state.view;
			const viewedRow = viewed === null ? void 0 : state.rows.find((row) => row.id === viewed.id);
			const viewedTitle = viewed === null ? "" : viewedRow === void 0 ? viewed.title : presetDisplayText(viewedRow, t).name;
			const creator = startCreatorDraft !== void 0 && state.rows.some((row) => row.id === "cordis") ? startCreatorDraft : void 0;
			const creatorButton = creator === void 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
				type: "button",
				className: AgentPresetSection_module_css_default.creatorButton,
				disabled: !developerTools || state.saving,
				title: developerTools ? void 0 : t("enableDevToolsToCreate"),
				onClick: () => {
					creator();
					closeSettings();
				},
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconPlusOutlineRegular, { size: 14 }), t("creatorDraft")]
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
				className: AgentPresetSection_module_css_default.section,
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("h2", {
						className: AgentPresetSection_module_css_default.title,
						children: t("nav")
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
						className: AgentPresetSection_module_css_default.intro,
						children: t("sectionIntro")
					}),
					state.error === null ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
						className: AgentPresetSection_module_css_default.error,
						role: "alert",
						children: state.error
					}),
					[true, false].map((builtIn) => {
						const rows = state.rows.filter((row) => isBuiltInPreset(row) === builtIn);
						const entry = builtIn ? null : creatorButton;
						if (rows.length === 0 && entry === null) return null;
						return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
							className: AgentPresetSection_module_css_default.group,
							children: [
								/* @__PURE__ */ (0, react_jsx_runtime.jsx)("h3", {
									className: AgentPresetSection_module_css_default.groupHead,
									children: t(builtIn ? "builtInGroup" : "customGroup")
								}),
								rows.length === 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("ul", {
									className: AgentPresetSection_module_css_default.cards,
									children: rows.map((row) => {
										const display = presetDisplayText(row, t);
										const help = presetGuide(row.id, builtIn ? "system" : "user");
										const selectionAction = row.broken !== void 0 ? t("brokenBadge") : row.isDefault ? t("inUse") : t(developerTools ? "setDefault" : "enableDevToolsToSetDefault");
										return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("li", {
											"data-agent-preset-id": row.id,
											className: [
												AgentPresetSection_module_css_default.card,
												row.broken === void 0 ? void 0 : AgentPresetSection_module_css_default.cardBroken,
												row.isDefault ? AgentPresetSection_module_css_default.cardActive : void 0,
												!developerTools && row.broken === void 0 && !row.isDefault ? AgentPresetSection_module_css_default.cardSelectionDisabled : void 0
											].filter(Boolean).join(" "),
											children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
												type: "button",
												className: AgentPresetSection_module_css_default.cardMain,
												"aria-pressed": row.isDefault,
												disabled: row.isDefault || row.broken === void 0 && (!developerTools || state.saving),
												"aria-disabled": row.broken !== void 0,
												"aria-label": `${selectionAction}: ${display.name}`,
												title: selectionAction,
												onClick: () => {
													if (row.broken === void 0) makeDefault(row.id);
												},
												children: [
													/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
														className: AgentPresetSection_module_css_default.cardHead,
														children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
															className: AgentPresetSection_module_css_default.cardIdentity,
															children: [
																/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
																	className: AgentPresetSection_module_css_default.cardName,
																	title: display.name,
																	children: display.name
																}),
																row.broken === void 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
																	className: AgentPresetSection_module_css_default.brokenBadge,
																	children: [t("brokenBadge"), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
																		className: AgentPresetSection_module_css_default.brokenTip,
																		"aria-hidden": "true",
																		children: row.broken
																	})]
																}),
																/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tag, {
																	tone: row.isDefault ? "solid" : "outline",
																	children: row.isDefault ? t("inUse") : t(builtIn ? "builtInGroup" : "customGroup")
																})
															]
														}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("code", {
															className: AgentPresetSection_module_css_default.cardId,
															title: row.id,
															children: row.id
														})]
													}),
													/* @__PURE__ */ (0, react_jsx_runtime.jsx)(CardDescription, { text: display.description ?? t("noDescription") }),
													row.broken === void 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
														className: AgentPresetSection_module_css_default.cardBrokenReason,
														role: "alert",
														children: row.broken
													})
												]
											}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
												className: AgentPresetSection_module_css_default.cardFoot,
												children: [help === void 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
													className: AgentPresetSection_module_css_default.cardHelp,
													children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
														variant: "ghost",
														className: AgentPresetSection_module_css_default.helpButton,
														"aria-label": `${t("modeExplanation")}: ${display.name}`,
														onClick: () => {
															setGuide({
																content: help,
																page: "explanation"
															});
														},
														children: t("modeExplanation")
													}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
														variant: "ghost",
														className: AgentPresetSection_module_css_default.helpButton,
														"aria-label": `${t("howToUse")}: ${display.name}`,
														onClick: () => {
															setGuide({
																content: help,
																page: "usage"
															});
														},
														children: t("howToUse")
													})]
												}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
													type: "button",
													className: AgentPresetSection_module_css_default.iconButton,
													"data-tip": t("view"),
													"aria-label": `${t("view")}: ${display.name}`,
													onClick: (event) => {
														viewTrigger.current = event.currentTarget;
														view(row.id);
													},
													children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconBrowseOutlineRegular, {})
												})]
											})]
										}, row.id);
									})
								}),
								entry
							]
						}, String(builtIn));
					}),
					guide === null ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(PresetGuideDialog, {
						guide: guide.content,
						initialPage: guide.page,
						t,
						onClose: () => {
							setGuide(null);
						}
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Modal, {
						open: viewed !== null,
						onClose: closeViewer,
						closeLabel: t("close"),
						onKeyDownCapture: (event) => {
							if (event.key === "Escape") {
								event.preventDefault();
								event.stopPropagation();
								closeViewer();
							} else trapPresetReaderTab(event);
						},
						title: viewed === null ? "" : `${t("view")} · ${viewedTitle}`,
						className: AgentPresetSection_module_css_default.dialog,
						footer: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
							variant: "outline",
							autoFocus: true,
							onClick: closeViewer,
							children: t("close")
						}),
						children: viewed === null ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("pre", {
							className: AgentPresetSection_module_css_default.viewerCode,
							children: viewed.content
						})
					})
				]
			});
		}
		//#endregion
		//#region src/client/settings-store.ts
		/** The agent-preset settings namespace on the host wire. */
		const AGENT_PRESET_SETTINGS_NS = "agent-preset-registry";
		/**
		* Persist one preset as the default for sessions created later.
		*
		* The default is a settings field rather than a preset property; the
		* management section writes it here — one home for which namespace and field
		* the host resolves at session creation.
		* @param ctx - the browser plugin context carrying the Remote namespaces.
		* @param id - the preset to make default.
		* @returns the failure message, or undefined once the write landed.
		*/
		async function writeDefaultPreset(ctx, id) {
			const response = await ctx.remote.settings.update(AGENT_PRESET_SETTINGS_NS, { selectedDefault: id }, void 0);
			return response.ok ? void 0 : response.error.message;
		}
		const EMPTY_ROSTER = { presets: [] };
		/**
		* Read the roster, turning a refusal into the message every surface shows.
		* @param ctx - the browser plugin context carrying the Remote namespaces.
		* @returns the roster, or the message to show in its place.
		*/
		async function readRoster(ctx) {
			const result = await ctx.remote.agentPresets.list();
			if (result.ok) return {
				ok: true,
				value: result.value
			};
			if (result.error.code === "gateway/invocation-unavailable") return {
				ok: true,
				value: EMPTY_ROSTER
			};
			return {
				ok: false,
				error: result.error.message
			};
		}
		/**
		* The opening move every roster-backed surface makes: refuse a read that is
		* already in flight, mark the store loading, then read.
		*
		* A surface that gets `undefined` returns without touching its snapshot
		* further — either another read owns it, or this one already wrote the
		* failure. What differs between surfaces starts after this.
		* @param ctx - the browser plugin context carrying the Remote namespaces.
		* @param store - the surface's own snapshot store.
		* @returns the roster, or undefined when the caller should return.
		*/
		async function beginRosterRead(ctx, store) {
			const before = store.getSnapshot();
			if (before.status === "loading") return void 0;
			store.set({
				...before,
				status: "loading",
				error: null
			});
			const roster = await readRoster(ctx);
			if (roster.ok) return roster.value;
			store.set({
				...store.getSnapshot(),
				status: "error",
				error: roster.error
			});
		}
		/**
		* The roster entries as the pickers render them: healthy presets only.
		*
		* The chip exists to choose the NEXT session's composition, and a broken
		* preset cannot compose one — offering it would defer the discovery of that
		* fact to a failed session start. The management section renders the full
		* roster (broken rows included) from its own store instead.
		*
		* The chip, the header label, and the management section all show the same
		* facts, and `exactOptionalPropertyTypes` makes "absent" and "present as
		* undefined" different shapes — so the spread dance belongs in one place rather than
		* once per store.
		* @param presets - the roster the host answered with.
		* @returns one option per selectable preset, in roster order.
		*/
		function presetOptions(presets) {
			return presets.filter((preset) => preset.broken === void 0).map((preset) => ({
				id: preset.id,
				...preset.name === void 0 ? {} : { name: preset.name },
				...preset.description === void 0 ? {} : { description: preset.description }
			}));
		}
		const INITIAL$2 = {
			status: "idle",
			error: null,
			options: []
		};
		/** Reads the roster for the surfaces that only display it. */
		var AgentPresetSettingsController = class {
			ctx;
			/** Roster snapshot the renderer subscribes to. */
			store = (0, _deepseek_ai_dsh_client_store.createSnapshotStore)(INITIAL$2);
			/**
			* @param ctx - the browser plugin context (the roster read).
			*/
			constructor(ctx) {
				this.ctx = ctx;
			}
			set(patch) {
				this.store.set({
					...this.store.getSnapshot(),
					...patch
				});
			}
			/**
			* Load the roster. An empty roster means the deployment composes no
			* presets, which is a valid deployment rather than a failure — the
			* surfaces report `unavailable` and render nothing.
			* @returns once the snapshot reflects the host.
			*/
			async load() {
				const roster = await beginRosterRead(this.ctx, this.store);
				if (roster === void 0) return;
				const { presets } = roster;
				if (presets.length === 0) {
					this.set({
						status: "unavailable",
						options: []
					});
					return;
				}
				this.set({
					status: "ready",
					error: null,
					options: presetOptions(presets)
				});
			}
		};
		//#endregion
		//#region src/client/seat-store.ts
		const INITIAL$1 = {
			options: [],
			current: "",
			error: null,
			busy: false,
			introduce: false
		};
		/** Stages the next session's preset and applies it when one appears. */
		var AgentPresetSeatController = class {
			ctx;
			currentSession;
			staged;
			/** Chip snapshot the renderer subscribes to. */
			store = (0, _deepseek_ai_dsh_client_store.createSnapshotStore)(INITIAL$1);
			/**
			* The Host-effective default, so a consumed stage can fall back to it without
			* re-reading the roster.
			*/
			fallback = "";
			/** Only the newest roster read may publish after overlapping refreshes. */
			loadGeneration = 0;
			/** Completion of the active Host selection; Settings choices wait before staging. */
			pendingSelection;
			constructor(ctx, currentSession, staged = {
				id: void 0,
				introduce: false
			}) {
				this.ctx = ctx;
				this.currentSession = currentSession;
				this.staged = staged;
			}
			set(patch) {
				this.store.set({
					...this.store.getSnapshot(),
					...patch
				});
			}
			clearStage() {
				this.staged.id = void 0;
				this.staged.introduce = false;
			}
			/** Developer tools are the single gate over preset selection. */
			selectionAvailable() {
				return this.ctx.configForms.developerTools.enabled.getSnapshot();
			}
			/**
			* Read the roster and open the chip on the Host-effective default.
			* @returns once the snapshot reflects the host.
			*/
			async load() {
				const generation = ++this.loadGeneration;
				const roster = await readRoster(this.ctx);
				if (generation !== this.loadGeneration) return;
				if (!roster.ok) {
					this.set({ error: roster.error });
					return;
				}
				const { presets } = roster.value;
				if (!this.selectionAvailable()) this.clearStage();
				this.fallback = presets.find((preset) => preset.isDefault)?.id ?? presets[0]?.id ?? "";
				const session = this.currentSession();
				this.set({
					options: presetOptions(presets),
					current: this.staged.id ?? (session === void 0 ? this.fallback : presetOf(session) ?? ""),
					error: null,
					introduce: this.staged.introduce
				});
				await this.apply();
			}
			/**
			* Stage one preset for the next session, applying it immediately when a
			* blank session is already current.
			*
			* The refusal is returned as well as stored, because the two readers need
			* different things from it: the chip's own label carries the standing state,
			* while the caller that made this pick is the one that has to say why the
			* label came back — and only it knows the pick was a person's, not the
			* applier catching up with a session that just became current.
			* @param id - the preset to stage.
			* @returns the refusal text, or undefined once the pick settled.
			*/
			async select(id) {
				if (this.store.getSnapshot().busy) return void 0;
				this.stage(id);
				return await this.apply();
			}
			/**
			* Stage a pick WITHOUT the immediate apply, for a flow that starts the
			* receiving session after the pick (the settings section's creator entry).
			* `select()`'s immediate apply would meet the still-current running session
			* and drop the stage as unservable; staging alone leaves it for the
			* list-change applier, which fires when the started session becomes
			* current.
			* @param id - the preset to stage.
			* @param introduce - true when the stage came from another screen and the
			* chip should announce itself on the session it lands on.
			*/
			stage(id, introduce = false) {
				this.staged.id = id;
				this.staged.introduce = introduce;
				this.set({
					current: id,
					error: null,
					introduce
				});
			}
			/**
			* Capture the exact blank Session a Settings action may bring along.
			* @returns its id, or undefined outside a blank Session.
			*/
			blankSessionId() {
				const session = this.currentSession();
				return session?.blank === true ? session.id : void 0;
			}
			/**
			* Apply a Settings choice only if its captured Session is still current and
			* blank after any pending selection settles. The selection uses the existing stage/apply path.
			* @param expectedSessionId - blank Session captured before the Settings write.
			* @param id - the effective default that the write persisted.
			* @returns the Host refusal text, or undefined when applied or no longer relevant.
			*/
			async syncBlankSession(expectedSessionId, id) {
				while (this.pendingSelection !== void 0) await this.pendingSelection;
				const session = this.currentSession();
				if (session === void 0 || !session.blank || session.id !== expectedSessionId) return void 0;
				this.stage(id);
				return await this.apply();
			}
			/** Acknowledge the introduction cue once the chip has played it. */
			introduced() {
				if (!this.store.getSnapshot().introduce) return;
				this.staged.introduce = false;
				this.set({ introduce: false });
			}
			/**
			* Hand the staged choice to the current session, if there is one to take it.
			*
			* Called both by `select()` and by whoever observes the current session
			* changing, because the session may appear either before or after the pick.
			* List updates do not repeat a selection while its response is pending.
			* @returns this attempt's Host refusal, or undefined when successful or no switch starts.
			*/
			async apply() {
				if (this.store.getSnapshot().busy) return;
				const available = this.selectionAvailable();
				if (!available) this.clearStage();
				const staged = this.staged.id;
				const session = this.currentSession();
				if (staged === void 0) {
					const current = session === void 0 ? this.fallback : presetOf(session) ?? "";
					const shown = this.store.getSnapshot();
					if (!available && shown.introduce) this.set({
						current,
						introduce: false
					});
					else if (current !== shown.current) this.set({ current });
					return;
				}
				if (session === void 0) return;
				if (!session.blank || presetOf(session) === staged) {
					this.clearStage();
					return;
				}
				const completion = Promise.withResolvers();
				this.pendingSelection = completion.promise;
				this.clearStage();
				try {
					this.set({
						busy: true,
						error: null
					});
					const result = await this.ctx.remote.agentPresets.select(session.id, staged);
					if (!result.ok) {
						const { error } = result;
						const refusal = "reason" in error.details && typeof error.details.reason === "string" ? error.details.reason : error.message;
						this.set({
							error: refusal,
							current: this.staged.id ?? presetOf(session) ?? ""
						});
						return refusal;
					}
					this.set({ current: this.staged.id ?? result.value });
				} finally {
					this.pendingSelection = void 0;
					this.set({ busy: false });
					completion.resolve(void 0);
				}
			}
		};
		function presetOf(session) {
			const value = session?.projectionValues?.agentPreset;
			return typeof value === "string" ? value : void 0;
		}
		//#endregion
		//#region src/client/section-store.ts
		const INITIAL = {
			status: "idle",
			error: null,
			saving: false,
			rows: [],
			view: null
		};
		const message = (error) => error instanceof Error ? error.message : String(error);
		/** Loads the roster, writes the default, and reads one composition at a time. */
		var AgentPresetSectionController = class {
			ctx;
			/** Observable roster, selection and viewer state. */
			store = (0, _deepseek_ai_dsh_client_store.createSnapshotStore)(INITIAL);
			loading;
			viewRequest = 0;
			constructor(ctx) {
				this.ctx = ctx;
			}
			set(patch) {
				this.store.set({
					...this.store.getSnapshot(),
					...patch
				});
			}
			/** Refresh the roster; concurrent calls share one read.
			* @returns Once the roster read settles.
			*/
			load() {
				return this.loading ??= this.readRoster().finally(() => {
					this.loading = void 0;
				});
			}
			async readRoster() {
				try {
					const result = await this.ctx.remote.agentPresets.list();
					if (!result.ok) throw new Error(result.error.message);
					this.set({
						status: "ready",
						error: null,
						rows: result.value.presets
					});
				} catch (error) {
					this.set({
						status: "error",
						error: message(error)
					});
				}
			}
			/** Open one preset's declared composition in the viewer.
			* @param id Preset to read.
			* @returns Once the read settles; a current failure lands in `error`, while a read superseded by close or another read is ignored.
			*/
			async view(id) {
				const request = ++this.viewRequest;
				this.set({
					error: null,
					view: null
				});
				try {
					const result = await this.ctx.remote.agentPresets.read(id);
					if (request !== this.viewRequest) return;
					if (!result.ok) throw new Error(result.error.message);
					const { name, content } = result.value;
					this.set({ view: {
						id,
						title: name ?? id,
						content
					} });
				} catch (error) {
					if (request === this.viewRequest) this.set({ error: message(error) });
				}
			}
			/** Close the viewer. */
			closeView() {
				this.viewRequest++;
				this.set({ view: null });
			}
			/** Set the default and synchronize the current blank task when supplied.
			* @param id Selected default.
			* @param sync Blank-session synchronization callback.
			* @returns Once saved and refreshed.
			*/
			async makeDefault(id, sync) {
				await this.save(() => writeDefaultPreset(this.ctx, id), sync);
			}
			async save(write, sync) {
				if (this.store.getSnapshot().saving) return;
				this.set({
					saving: true,
					error: null
				});
				try {
					const error = await write();
					await this.load();
					if (error !== void 0) throw new Error(error);
					const selected = this.store.getSnapshot().rows.find((row) => row.isDefault);
					if (selected !== void 0) {
						const error = await sync?.(selected.id);
						if (error !== void 0) throw new Error(error);
					}
				} catch (error) {
					this.set({ error: message(error) });
				} finally {
					this.set({ saving: false });
				}
			}
		};
		//#endregion
		//#region src/client/index.ts
		/** Required services (cordis fiber inject). */
		const inject = [
			"slots",
			"sessions",
			"locale",
			"remote",
			"remote.agentPresets",
			"remote.settings",
			"configForms"
		];
		/**
		* Mount the roster surfaces: hero chip, session-header label, settings section.
		* @param ctx - the browser plugin context.
		*/
		function apply(ctx) {
			const controller = new AgentPresetSettingsController(ctx);
			const staged = {
				id: void 0,
				introduce: false
			};
			const seats = new WeakMapWithValues();
			const boundSeatDisposers = /* @__PURE__ */ new Set();
			ctx.effect(() => async () => {
				await Promise.all([...boundSeatDisposers].map((dispose) => dispose()));
			}, "ui-agent-preset: bound selections");
			const unboundSeat = new AgentPresetSeatController(ctx, () => void 0, staged);
			const seatFor = (binding) => {
				const existing = seats.get(binding);
				if (existing !== void 0) return existing;
				const seat = new AgentPresetSeatController(ctx, () => {
					if (ctx.sessions.binding(binding.sessionId) !== binding) return void 0;
					const summary = ctx.sessions.list.getSnapshot().byId[binding.sessionId];
					return summary !== void 0 && (ctx.sessions.retainInfo(binding.sessionId).getSnapshot().retainedBy.mainView ?? 0) > 0 ? summary : void 0;
				}, staged);
				seats.set(binding, seat);
				const dispose = binding.ctx.effect(() => {
					const stop = ctx.sessions.list.subscribe(() => {
						seat.apply();
					});
					return () => {
						stop();
						seats.delete(binding);
						boundSeatDisposers.delete(dispose);
					};
				}, "ui-agent-preset: Provider binding");
				boundSeatDisposers.add(dispose);
				return seat;
			};
			const section = new AgentPresetSectionController(ctx);
			const developerTools = ctx.configForms.developerTools.enabled;
			ctx.effect(() => developerTools.subscribe(() => {
				if (developerTools.getSnapshot()) return;
				staged.id = void 0;
				staged.introduce = false;
				unboundSeat.apply();
				for (const seat of seats.values) seat.apply();
			}), "ui-agent-preset: Developer tools gate");
			const mainBlankSeat = () => {
				const summary = Object.values(ctx.sessions.list.getSnapshot().byId).find((session) => {
					/* v8 ignore next -- retained source counts omit zero-valued entries. */
					return session.blank && (session.retainedBy.mainView ?? 0) > 0;
				});
				const binding = summary === void 0 ? void 0 : ctx.sessions.binding(summary.id);
				return binding === void 0 ? void 0 : seatFor(binding);
			};
			ctx.effect(() => ctx.locale.register("settings.agentPreset", {
				zh,
				en
			}), "ui-agent-preset: settings row dictionaries");
			ctx.effect(() => {
				const refresh = () => {
					controller.load();
					if (section.store.getSnapshot().status !== "idle") section.load();
					unboundSeat.load();
					for (const seat of seats.values) seat.load();
				};
				const disposers = [ctx.remote.$on("settings/document-updated", (ns) => {
					if (ns !== "agent-preset-registry") return;
					refresh();
				}), ctx.on("connection/reset", () => {
					refresh();
				})];
				return () => {
					for (const dispose of disposers) dispose();
				};
			}, "ui-agent-preset: settings refresh");
			let creatorDraft;
			ctx.inject([
				"slots",
				"conversation",
				"sessions",
				"uiWorkspace"
			], (scope) => {
				const seatInjected = (sessionId) => {
					const binding = sessionId === void 0 ? void 0 : ctx.sessions.binding(sessionId);
					const seat = binding === void 0 ? unboundSeat : seatFor(binding);
					return {
						hooks: {
							agentPresetSeat: seat.store,
							developerTools: ctx.configForms.developerTools.enabled
						},
						load: () => seat.load(),
						select: (id) => seat.select(id),
						introduced: () => {
							seat.introduced();
						}
					};
				};
				const labelInjected = () => ({
					hooks: { agentPresets: controller.store },
					load: () => controller.load()
				});
				scope.effect(() => {
					creatorDraft = () => {
						const seat = mainBlankSeat() ?? unboundSeat;
						seat.stage("cordis", true);
						scope.uiWorkspace.startSession();
						seat.apply();
					};
					const chip = scope.slots.register({
						name: "conversation.hero.agentPreset",
						locale: "settings.agentPreset",
						inject: seatInjected
					}, AgentPresetSeat);
					const label = scope.slots.register({
						name: "conversation.session.header.actions",
						id: "agent-preset",
						order: -10,
						locale: "settings.agentPreset",
						inject: labelInjected
					}, AgentPresetLabel);
					return () => {
						creatorDraft = void 0;
						chip();
						label();
					};
				}, "ui-agent-preset: new-session chip and header label");
			});
			/** Capture the exact blank Session one Settings action may update. */
			const captureBlankSessionSync = () => {
				const summary = Object.values(ctx.sessions.list.getSnapshot().byId).find((session) => session.blank && (session.retainedBy.mainView ?? 0) > 0);
				const binding = summary === void 0 ? void 0 : ctx.sessions.binding(summary.id);
				const seat = binding === void 0 ? void 0 : seatFor(binding);
				const sessionId = seat?.blankSessionId();
				return async (id) => {
					if (seat === void 0 || sessionId === void 0 || binding === void 0 || seats.get(binding) !== seat) return void 0;
					return await seat.syncBlankSession(sessionId, id);
				};
			};
			const sectionInjected = () => ({
				hooks: {
					agentPresetSection: section.store,
					developerTools: ctx.configForms.developerTools.enabled
				},
				load: () => section.load(),
				view: (id) => section.view(id),
				closeView: () => {
					section.closeView();
				},
				...creatorDraft === void 0 ? {} : { startCreatorDraft: creatorDraft },
				makeDefault: (id) => section.makeDefault(id, captureBlankSessionSync())
			});
			ctx.slots.inject("settings.section", () => ctx.slots.register({
				name: "settings.section",
				id: "agent-presets",
				order: 20,
				label: () => ctx.locale.bind("settings.agentPreset")("nav"),
				locale: "settings.agentPreset",
				inject: sectionInjected
			}, AgentPresetSection));
		}
		//#endregion
		exports.AGENT_PRESET_SETTINGS_NS = AGENT_PRESET_SETTINGS_NS;
		exports.apply = apply;
		exports.inject = inject;
		exports.writeDefaultPreset = writeDefaultPreset;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map