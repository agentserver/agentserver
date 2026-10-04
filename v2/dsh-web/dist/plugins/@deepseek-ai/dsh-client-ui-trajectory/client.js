window.__ModuleLoader__.load({
	id: "@deepseek-ai/dsh-client-ui-trajectory",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		Object.defineProperty(exports, Symbol.toStringTag, { value: "Module" });
		//#region \0rolldown/runtime.js
		var __create = Object.create;
		var __defProp = Object.defineProperty;
		var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
		var __getOwnPropNames = Object.getOwnPropertyNames;
		var __getProtoOf = Object.getPrototypeOf;
		var __hasOwnProp = Object.prototype.hasOwnProperty;
		var __copyProps = (to, from, except, desc) => {
			if (from && typeof from === "object" || typeof from === "function") for (var keys = __getOwnPropNames(from), i = 0, n = keys.length, key; i < n; i++) {
				key = keys[i];
				if (!__hasOwnProp.call(to, key) && key !== except) __defProp(to, key, {
					get: ((k) => from[k]).bind(null, key),
					enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable
				});
			}
			return to;
		};
		var __toESM = (mod, isNodeMode, target) => (target = mod != null ? __create(__getProtoOf(mod)) : {}, __copyProps(isNodeMode || !mod || !mod.__esModule ? __defProp(target, "default", {
			value: mod,
			enumerable: true
		}) : target, mod));
		//#endregion
		let _deepseek_ai_dsh_client_store = require("@deepseek-ai/dsh-client-store");
		let react = require("react");
		react = __toESM(react, 1);
		let react_dom = require("react-dom");
		let _deepseek_ai_dsh_client_ui_primitives = require("@deepseek-ai/dsh-client-ui-primitives");
		let react_jsx_runtime = require("react/jsx-runtime");
		//#region src/client/duration-store.ts
		/**
		* Create the browser-wide trajectory duration preference source.
		* @returns a persisted source shared by every session view in one plugin lifecycle.
		*/
		function createTrajectoryDurationStore() {
			return (0, _deepseek_ai_dsh_client_store.createSnapshotStore)(false, { persist: { name: "dsh.trajectory.duration" } });
		}
		//#endregion
		//#region src/client/string-wrapping-store.ts
		/**
		* Create the browser-wide default for expanded JSON strings.
		* @returns A persisted preference sampled only when a string is expanded.
		*/
		function createTrajectoryStringWrappingStore() {
			return (0, _deepseek_ai_dsh_client_store.createSnapshotStore)(false, { persist: { name: "dsh.trajectory.jsonStringWrapping" } });
		}
		//#endregion
		//#region src/client/locales.ts
		/** `trajectory` namespace dictionaries for the complete trajectory surface. */
		/** Dictionary namespace owned by this plugin. */
		const NS = "trajectory";
		/** Simplified Chinese dictionary (the key-set source of truth). */
		const zh = {
			"view.trajectory": "轨迹",
			"toolbar.aria": "轨迹工具栏",
			"toolbar.duration": "时长",
			"toolbar.useActualDuration": "使用实际时长",
			"toolbar.useEqualWidth": "使用等宽操作",
			"toolbar.actualTime": "实际时间",
			"toolbar.turns": "轮次",
			"toolbar.expandTurns": "展开所有轮次",
			"toolbar.collapseTurns": "收起所有轮次",
			"toolbar.calls": "调用",
			"toolbar.expandCalls": "展开所有调用",
			"toolbar.collapseCalls": "收起所有调用",
			"toolbar.search": "搜索轨迹",
			"toolbar.searchPlaceholder": "搜索",
			"kind.system": "系统",
			"kind.user": "用户",
			"kind.context": "上下文",
			"kind.compacted": "已压缩",
			"kind.message": "消息",
			"kind.assistant": "助手",
			"kind.tool": "工具",
			"kind.subtool": "子工具",
			"kind.sub": "子项",
			"column.input": "输入",
			"column.output": "输出",
			"column.think": "思考",
			"column.time": "时间",
			"column.model": "模型",
			"column.tools": "工具",
			"turn.label": "第 {turn} 轮",
			"section.betweenTurns": "轮次之间",
			"group.message": "消息",
			"group.step": "第 {step} 步",
			"group.compaction": "压缩 {seq}",
			"status.failed": "失败",
			"status.pending": "等待中",
			"status.completed": "已完成",
			"timing.notAvailable": "不可用",
			"timing.notRecorded": "未记录",
			"timing.stepStartUnavailable": "步骤开始时间不可用",
			"timing.firstTokenUnavailable": "首 token 时间不可用",
			"timing.usageUnavailable": "用量不可用",
			"timing.outputTokensUnavailable": "输出 token 数不可用",
			"timing.durationTooShort": "时长过短",
			"timing.showLocalTime": "显示本地时间",
			"timing.showUnixTimestamp": "显示 Unix 时间戳",
			"timing.started": "开始时间",
			"timing.totalDuration": "总时长",
			"timing.ttft": "首 token 延迟",
			"timing.generation": "生成",
			"timing.throughput": "吞吐量",
			"timing.duration": "时长",
			"timing.source": "计时来源",
			"timing.sessionTimestamps": "会话时间戳",
			"timing.sessionTimestampsRunning": "会话时间戳（运行中）",
			"timing.request": "请求计时",
			"unit.milliseconds": "{value} 毫秒",
			"unit.seconds": "{value} 秒",
			"unit.tokens": "{value} tok",
			"unit.tokensPerSecond": "{value} tok/s",
			"usage.tokens": "Token",
			"usage.reasoning": "推理",
			"usage.content": "内容",
			"usage.notReported": "未报告用量",
			"usage.input": "输入",
			"usage.cached": "缓存读取",
			"usage.cacheCreated": "缓存写入",
			"usage.other": "其他",
			"usage.output": "输出",
			"usage.thisRequest": "本次请求",
			"usage.sessionCumulative": "会话累计",
			"options.notRecorded": "未记录选项",
			"options.json": "请求选项 JSON",
			"source.unknown": "未知",
			"source.user": "用户",
			"source.plugin": "插件",
			"source.pluginNamed": "插件 · {plugin}",
			"source.goal": "目标",
			"source.goalRound": "目标 · Round {round}",
			"source.notRecorded": "未记录来源",
			"source.messageJson": "消息来源 JSON",
			"tab.summary": "概述",
			"tab.rawOutput": "原始输出",
			"tab.preview": "预览",
			"tab.raw": "原始内容",
			"tab.source": "来源",
			"tab.payload": "参数",
			"tab.result": "结果",
			"tab.schema": "Schema",
			"tab.timing": "计时",
			"tab.diff": "差异",
			"tab.systemPrompt": "系统提示词",
			"tab.tools": "工具",
			"tab.options": "选项",
			"tab.usage": "用量",
			"record.toolCallOnly": "（仅工具调用）",
			"record.noContent": "无内容",
			"record.noPayload": "未捕获参数",
			"record.noResult": "未捕获结果",
			"record.noOutput": "无输出",
			"record.schemaUnavailable": "Schema 不可用",
			"record.parameters": "参数",
			"record.resultJson": "结果 JSON",
			"record.json": "JSON",
			"record.parametersJson": "参数 JSON",
			"record.namedParametersJson": "{name} 参数 JSON",
			"record.payloadJson": "参数 JSON",
			"record.outputJson": "结果 JSON",
			"record.thinking": "思考",
			"record.wrapLines": "自动换行",
			"code.source": "代码",
			"code.output": "输出",
			"code.copySource": "复制代码",
			"code.copyOutput": "复制输出",
			"code.originalJson": "原始 JSON",
			"code.running": "执行中…",
			"record.systemPromptMissing": "本次请求没有系统提示词",
			"record.toolsMissing": "本次请求没有工具",
			"record.systemPrompt": "系统提示词",
			"record.tools": "工具",
			"block.openSummary": "打开第 {index} 个块的工具调用概述",
			"block.openSummaryTitle": "打开工具调用概述",
			"block.label": "块 #{index} {type}",
			"history.loadingTrajectory": "正在加载轨迹…",
			"history.loadingEarlier": "正在加载更早的历史…",
			"history.loadingEarlierAria": "正在加载更早的历史…",
			"history.loadEarlier": "加载更早的历史",
			"history.clickToLoadEarlier": "点击加载更早的历史",
			"request.label": "请求 #{request}",
			"request.labelCompaction": "请求 #{request} · 压缩",
			"request.compaction": "压缩 · {section}",
			"request.compactionPurpose": "压缩",
			"request.retryProgress": "{retry}/{maximum}",
			"request.collapsedSummary": "已收起的{kind}概述，{summary}",
			"request.collapsedTurn": "轮次",
			"request.collapsedAssistant": "助手",
			"request.rowAria": "{request}{kind}，{content}",
			"request.rowPrefix": "请求 {request}，",
			"request.rowAriaCompaction": "请求 {request}，压缩",
			"request.noContent": "无内容",
			"summary.toolCalls.one": "{count} 个工具调用",
			"summary.toolCalls.other": "{count} 个工具调用",
			"summary.steps.one": "{count} 个步骤",
			"summary.steps.other": "{count} 个步骤",
			"details.event": "事件详情",
			"details.resize": "调整事件详情宽度",
			"details.resizeTitle": "拖动调整大小；双击恢复默认值。",
			"details.close": "关闭详情",
			"details.status": "状态",
			"details.purpose": "用途",
			"details.provider": "提供方",
			"details.model": "模型",
			"details.toolCalls": "工具调用",
			"details.subtoolCalls": "子工具调用",
			"details.error": "错误",
			"details.failure.auth": "API 密钥无效",
			"details.retry": "重试",
			"details.scheduled": "已计划",
			"details.retryDelay": "重试延迟",
			"details.result": "结果",
			"details.compacted": "已压缩",
			"details.assistantMessage": "助手消息",
			"details.source": "来源",
			"details.hierarchy": "层级",
			"details.toolCall": "工具调用",
			"timeline.aria": "轨迹时间线",
			"timeline.overviewAria": "时间线概览；水平拖动可聚焦事件",
			"timeline.noTimingData": "无计时数据",
			"timeline.total": "总计 {duration}",
			"timeline.started": "开始于 {time}",
			"timeline.ttftDecoding": "首 token {ttft} · 解码 {decoding}",
			"layout.compacting": "正在压缩上下文…",
			"layout.compactionFailed": "上下文压缩失败",
			"layout.compacted": "上下文已压缩",
			"layout.toolCallOnly": "仅工具调用",
			"attachment.list": "附件",
			"attachment.imageName": "图片 {index}",
			"layout.imageCount": "图片 ×{count}",
			"layout.fileAttachments": "文件 ×{count}",
			"layout.initialSystemPrompt": "初始系统提示词",
			"layout.systemPromptUpdated": "系统提示词已更新",
			"layout.toolsUpdated": "工具已更新",
			"layout.toolAdded": "已添加工具：{name}",
			"layout.toolRemoved": "已移除工具：{name}",
			"layout.toolUpdateNotice": "工具已更新",
			"layout.toolsAdded": "新增：{names}",
			"layout.toolsAddedCount": "新增 {count} 个",
			"layout.toolsChanged": "新增 {added} 个，移除 {removed} 个",
			"layout.toolsRemoved": "移除：{names}",
			"layout.toolsRemovedCount": "移除 {count} 个",
			"layout.systemPromptAndToolsUpdated": "系统提示词和工具已更新",
			"layout.compactionInterrupted": "上下文压缩在完成前被中断。"
		};
		/** English dictionary, checked complete against the Chinese source of truth. */
		const en = {
			"view.trajectory": "Trajectory",
			"toolbar.aria": "Trajectory toolbar",
			"toolbar.duration": "Duration",
			"toolbar.useActualDuration": "Use actual duration",
			"toolbar.useEqualWidth": "Use equal-width operations",
			"toolbar.actualTime": "Actual time",
			"toolbar.turns": "Turns",
			"toolbar.expandTurns": "Expand turns",
			"toolbar.collapseTurns": "Collapse turns",
			"toolbar.calls": "Calls",
			"toolbar.expandCalls": "Expand calls",
			"toolbar.collapseCalls": "Collapse calls",
			"toolbar.search": "Search trajectory",
			"toolbar.searchPlaceholder": "Search",
			"kind.system": "SYSTEM",
			"kind.user": "USER",
			"kind.context": "CONTEXT",
			"kind.compacted": "COMPACTED",
			"kind.message": "Message",
			"kind.assistant": "ASSISTANT",
			"kind.tool": "TOOL",
			"kind.subtool": "SUBTOOL",
			"kind.sub": "Sub",
			"column.input": "Input",
			"column.output": "Output",
			"column.think": "Think",
			"column.time": "Time",
			"column.model": "Model",
			"column.tools": "Tools",
			"turn.label": "Turn {turn}",
			"section.betweenTurns": "Between turns",
			"group.message": "Message",
			"group.step": "Step {step}",
			"group.compaction": "Compaction {seq}",
			"status.failed": "Failed",
			"status.pending": "Pending",
			"status.completed": "Completed",
			"timing.notAvailable": "Not available",
			"timing.notRecorded": "Not recorded",
			"timing.stepStartUnavailable": "Step start unavailable",
			"timing.firstTokenUnavailable": "First token unavailable",
			"timing.usageUnavailable": "Usage unavailable",
			"timing.outputTokensUnavailable": "Output tokens unavailable",
			"timing.durationTooShort": "Duration too short",
			"timing.showLocalTime": "Show local time",
			"timing.showUnixTimestamp": "Show Unix timestamp",
			"timing.started": "Started",
			"timing.totalDuration": "Total duration",
			"timing.ttft": "TTFT",
			"timing.generation": "Generation",
			"timing.throughput": "Throughput",
			"timing.duration": "Duration",
			"timing.source": "Timing source",
			"timing.sessionTimestamps": "Session timestamps",
			"timing.sessionTimestampsRunning": "Session timestamps (running)",
			"timing.request": "Request Timing",
			"unit.milliseconds": "{value} ms",
			"unit.seconds": "{value} s",
			"unit.tokens": "{value} tok",
			"unit.tokensPerSecond": "{value} tok/s",
			"usage.tokens": "Tokens",
			"usage.reasoning": "Reasoning",
			"usage.content": "Content",
			"usage.notReported": "Usage not reported",
			"usage.input": "Input",
			"usage.cached": "Cached",
			"usage.cacheCreated": "Cache created",
			"usage.other": "Other",
			"usage.output": "Output",
			"usage.thisRequest": "This request",
			"usage.sessionCumulative": "Session cumulative",
			"options.notRecorded": "Options not recorded",
			"options.json": "Request options JSON",
			"source.unknown": "Unknown",
			"source.user": "User",
			"source.plugin": "Plugin",
			"source.pluginNamed": "Plugin · {plugin}",
			"source.goal": "Goal",
			"source.goalRound": "Goal · Round {round}",
			"source.notRecorded": "Source not recorded",
			"source.messageJson": "Message source JSON",
			"tab.summary": "Summary",
			"tab.rawOutput": "Raw Output",
			"tab.preview": "Preview",
			"tab.raw": "Raw",
			"tab.source": "Source",
			"tab.payload": "Payload",
			"tab.result": "Result",
			"tab.schema": "Schema",
			"tab.timing": "Timing",
			"tab.diff": "Diff",
			"tab.systemPrompt": "System Prompt",
			"tab.tools": "Tools",
			"tab.options": "Options",
			"tab.usage": "Usage",
			"record.toolCallOnly": "(tool call only)",
			"record.noContent": "No content",
			"record.noPayload": "No payload captured",
			"record.noResult": "No result captured",
			"record.noOutput": "No output",
			"record.schemaUnavailable": "Schema unavailable",
			"record.parameters": "Parameters",
			"record.resultJson": "Result JSON",
			"record.json": "JSON",
			"record.parametersJson": "parameters JSON",
			"record.namedParametersJson": "{name} parameters JSON",
			"record.payloadJson": "Payload JSON",
			"record.outputJson": "Result JSON",
			"record.thinking": "Thinking",
			"record.wrapLines": "Wrap lines",
			"code.source": "Code",
			"code.output": "Output",
			"code.copySource": "Copy code",
			"code.copyOutput": "Copy output",
			"code.originalJson": "Original JSON",
			"code.running": "Running…",
			"record.systemPromptMissing": "No system prompt in this request",
			"record.toolsMissing": "No tools in this request",
			"record.systemPrompt": "System Prompt",
			"record.tools": "Tools",
			"block.openSummary": "Open Block #{index} tool call summary",
			"block.openSummaryTitle": "Open tool call summary",
			"block.label": "Block #{index} {type}",
			"history.loadingTrajectory": "Loading trajectory…",
			"history.loadingEarlier": "Loading earlier history…",
			"history.loadingEarlierAria": "Loading earlier history…",
			"history.loadEarlier": "Load earlier history",
			"history.clickToLoadEarlier": "Click to load earlier history",
			"request.label": "Request #{request}",
			"request.labelCompaction": "Request #{request} · Compaction",
			"request.compaction": "Compaction · {section}",
			"request.compactionPurpose": "Compaction",
			"request.retryProgress": "{retry} of {maximum}",
			"request.collapsedSummary": "Collapsed {kind} summary, {summary}",
			"request.collapsedTurn": "turn",
			"request.collapsedAssistant": "assistant",
			"request.rowAria": "{request}{kind}, {content}",
			"request.rowPrefix": "Request {request}, ",
			"request.rowAriaCompaction": "Request {request}, compaction",
			"request.noContent": "no content",
			"summary.toolCalls.one": "{count} tool call",
			"summary.toolCalls.other": "{count} tool calls",
			"summary.steps.one": "{count} step",
			"summary.steps.other": "{count} steps",
			"details.event": "Event details",
			"details.resize": "Resize event details",
			"details.resizeTitle": "Drag to resize. Double-click to reset.",
			"details.close": "Close details",
			"details.status": "Status",
			"details.purpose": "Purpose",
			"details.provider": "Provider",
			"details.model": "Model",
			"details.toolCalls": "Tool calls",
			"details.subtoolCalls": "Subtool calls",
			"details.error": "Error",
			"details.failure.auth": "API key is invalid",
			"details.retry": "Retry",
			"details.scheduled": "Scheduled",
			"details.retryDelay": "Retry delay",
			"details.result": "Result",
			"details.compacted": "Compacted",
			"details.assistantMessage": "Assistant Message",
			"details.source": "Source",
			"details.hierarchy": "Hierarchy",
			"details.toolCall": "Tool Call",
			"timeline.aria": "Trajectory timeline",
			"timeline.overviewAria": "Timeline overview; drag horizontally to focus events",
			"timeline.noTimingData": "No timing data",
			"timeline.total": "Total {duration}",
			"timeline.started": "Started {time}",
			"timeline.ttftDecoding": "TTFT {ttft} · Decoding {decoding}",
			"layout.compacting": "Compacting context…",
			"layout.compactionFailed": "Compaction failed",
			"layout.compacted": "Context compacted",
			"layout.toolCallOnly": "Tool call only",
			"attachment.list": "Attachments",
			"attachment.imageName": "Image {index}",
			"layout.imageCount": "Images ×{count}",
			"layout.fileAttachments": "Files ×{count}",
			"layout.initialSystemPrompt": "Initial System Prompt",
			"layout.systemPromptUpdated": "System Prompt Updated",
			"layout.toolsUpdated": "Tools Updated",
			"layout.toolAdded": "Tool added: {name}",
			"layout.toolRemoved": "Tool removed: {name}",
			"layout.toolUpdateNotice": "Tools updated",
			"layout.toolsAdded": "Added: {names}",
			"layout.toolsAddedCount": "{count} added",
			"layout.toolsChanged": "{added} added, {removed} removed",
			"layout.toolsRemoved": "Removed: {names}",
			"layout.toolsRemovedCount": "{count} removed",
			"layout.systemPromptAndToolsUpdated": "System Prompt and Tools Updated",
			"layout.compactionInterrupted": "Compaction was interrupted before completion."
		};
		//#endregion
		//#region ../../llm/llm/src/assistant-stream.ts
		/**
		* Whether one chunk carries the model's first output token for latency measurement.
		* @param chunk - any stream chunk.
		* @returns true for a non-empty text, reasoning, or Tool-call arguments fragment and for
		*   every name-bearing Tool-call delta; false for block, usage, and finish chunks.
		*/
		function isTokenDelta$1(chunk) {
			switch (chunk.type) {
				case "text-delta":
				case "reasoning-delta": return chunk.text !== "";
				case "tool-call-delta": return chunk.argumentsDelta !== "" || chunk.name !== void 0;
				default: return false;
			}
		}
		function firstRunMemberTime(run, predicate) {
			const fragments = run.type === "tool-call-chunks" ? run.args : run.texts;
			let time = run.time0;
			for (let index = 0; index < fragments.length; index += 1) {
				if (index > 0) time += run.dt[index - 1];
				if (predicate(fragments[index])) return time;
			}
		}
		/**
		* Time of the first member of one packed run that {@link isTokenDelta} accepts: a
		* name-bearing Tool-call run starts at its first member, otherwise the first non-empty fragment.
		* Stops scanning at that member.
		* @param run - one packed delta run.
		* @returns the member's reconstructed time, or undefined when no member qualifies.
		*/
		function runFirstTokenTime(run) {
			if (run.type === "tool-call-chunks" && run.name !== void 0) return run.time0;
			return firstRunMemberTime(run, (fragment) => fragment !== "");
		}
		/**
		* Time of the first token in one compact stream per {@link isTokenDelta}, read from the
		* records themselves and stopping at the first qualifying member.
		* @param stream - compact records from one durable Assistant settlement.
		* @returns the first token's time, or undefined when the stream carries no token.
		*/
		function assistantStreamFirstTokenTime(stream) {
			for (const record of stream) {
				const time = record.type === "chunk" ? isTokenDelta$1(record.chunk) ? record.time : void 0 : runFirstTokenTime(record);
				if (time !== void 0) return time;
			}
		}
		//#endregion
		//#region src/client/trajectory-definition-common.ts
		/**
		* Wrap one contribution in the Engine-owned target envelope.
		*
		* @param context - Context that owns the contribution identity.
		* @param anchorSeq - Sequence used to order the contribution.
		* @param data - Trajectory-specific contribution payload.
		* @returns The contribution wrapped as a Trajectory view node.
		*/
		function trajectoryNode(context, anchorSeq, data) {
			return {
				key: context.key,
				kind: context.kind,
				id: context.id,
				target: "trajectory",
				anchorSeq,
				location: context.start?.location ?? { kind: "unresolved" },
				data
			};
		}
		//#endregion
		//#region src/client/trajectory-event-projection.ts
		function asRecord(value) {
			return typeof value === "object" && value !== null && !Array.isArray(value) ? value : null;
		}
		function readString(record, key) {
			const value = record[key];
			return typeof value === "string" && value.length > 0 ? value : null;
		}
		function collect(source, member, field) {
			const list = source[member];
			if (!Array.isArray(list)) return [];
			const seen = [];
			for (const entry of list) {
				const record = asRecord(entry);
				const value = record === null ? null : readString(record, field);
				if (value !== null && !seen.includes(value)) seen.push(value);
			}
			return seen;
		}
		function joined(names) {
			return names.length > 0 ? names.join(", ") : null;
		}
		/** Forms Trajectory presents structurally; unknown merge-extensible values remain opaque. */
		const KNOWN_FORMS = [
			"instructions",
			"catalog",
			"snapshot",
			"notice",
			"relay",
			"recall"
		];
		/**
		* Read the target-supported presentation form from a durable message source.
		* @param source - Logged `user/message` source.
		* @returns Supported form, or null for the opaque presentation.
		*/
		function contextForm(source) {
			const record = asRecord(source);
			const form = record === null ? null : readString(record, "form");
			return form !== null && KNOWN_FORMS.includes(form) ? form : null;
		}
		/**
		* Project a durable message source to the Trajectory row's role and producer label.
		* @param source - Logged `user/message` source.
		* @returns Role and label rendered by Trajectory.
		*/
		function contextProducer(source) {
			const record = asRecord(source);
			const kind = record === null ? null : readString(record, "kind");
			if (record === null || kind === null) return {
				role: "inject",
				label: null
			};
			switch (kind) {
				case "session-reference": return {
					role: "recall",
					label: joined(collect(record, "references", "label")) ?? kind
				};
				case "agent-instructions": return {
					role: "inject",
					label: joined(collect(record, "changes", "path")) ?? kind
				};
				case "skill-invocation": return {
					role: "inject",
					label: readString(record, "name") ?? kind
				};
				default: return {
					role: "inject",
					label: kind
				};
			}
		}
		/**
		* Classify finalized Assistant content for Trajectory rendering.
		* @param content - Core content blocks.
		* @returns Trajectory blocks in source order.
		*/
		function toAssistantBlocks(content) {
			return content.map(toAssistantBlock);
		}
		/**
		* Classify one finalized Assistant block for Trajectory rendering.
		* @param block - Core content block.
		* @returns Trajectory block.
		*/
		function toAssistantBlock(block) {
			switch (block.type) {
				case "text": return {
					kind: "text",
					text: block.text
				};
				case "reasoning": return {
					kind: "reasoning",
					text: block.text
				};
				case "image": return {
					kind: "image",
					attachment: block.attachment
				};
				case "tool-call": return {
					kind: "tool-call",
					callId: String(block.id),
					name: block.name,
					argsRaw: block.arguments
				};
				default: return {
					kind: "other",
					block
				};
			}
		}
		/**
		* Create the initial Trajectory block for one streamed Assistant block kind.
		* @param blockType - Wire block kind.
		* @returns Empty block ready to receive deltas.
		*/
		function emptyAssistantBlock(blockType) {
			switch (blockType) {
				case "text": return {
					kind: "text",
					text: ""
				};
				case "reasoning": return {
					kind: "reasoning",
					text: ""
				};
				case "tool-call": return {
					kind: "tool-call",
					callId: "",
					name: "",
					argsRaw: ""
				};
				default: return {
					kind: "other",
					block: null
				};
			}
		}
		/**
		* Convert a durable failure to locale-independent fields safe for Trajectory.
		* @param failure - Failure preserved by a Session event.
		* @returns Sanitized message and optional stable provider code.
		*/
		function displayFailure(failure) {
			if (failure === null || typeof failure !== "object") return { message: String(failure) };
			const record = failure;
			const code = typeof record.code === "string" ? record.code : void 0;
			if (code === "AUTH") return {
				code,
				message: ""
			};
			return {
				...code === void 0 ? {} : { code },
				message: typeof record.message === "string" ? record.message : JSON.stringify(failure)
			};
		}
		/**
		* Whether a stream chunk carries visible model output for Trajectory timing.
		* @param chunk - Stream chunk to inspect.
		* @returns true for a non-empty text, reasoning, or Tool-call delta.
		*/
		function isTokenDelta(chunk) {
			switch (chunk.type) {
				case "text-delta":
				case "reasoning-delta": return chunk.text !== "";
				case "tool-call-delta": return chunk.argumentsDelta !== "" || chunk.name !== void 0;
				default: return false;
			}
		}
		//#endregion
		//#region src/client/trajectory-assistant-definition.ts
		function initialState(turn, step, startSeq, startTime, started) {
			return {
				turn,
				step,
				startSeq,
				startTime,
				started,
				sawChunk: false,
				blocks: [],
				visibleBlocks: 0,
				firstVisibleSeq: void 0,
				firstVisibleTime: void 0,
				firstTokenTime: void 0,
				final: void 0,
				usage: void 0,
				retry: void 0,
				stepEnd: void 0
			};
		}
		function compactBlocks(blocks) {
			return blocks.filter((block) => block !== void 0);
		}
		function blockIsVisible(block) {
			if (block === void 0 || block.kind === "tool-call") return false;
			if (block.kind === "text" || block.kind === "reasoning") return block.text.trim() !== "";
			return true;
		}
		function countVisibleBlocks(blocks) {
			let count = 0;
			for (const block of blocks) if (blockIsVisible(block)) count++;
			return count;
		}
		function hasInterruptionEvidence(blocks) {
			return blocks.some((block) => {
				if (block.kind === "text" || block.kind === "reasoning") return block.text.trim() !== "";
				return true;
			});
		}
		function addUsage$1(current, next) {
			return {
				inputTokens: (current?.inputTokens ?? 0) + next.inputTokens,
				outputTokens: (current?.outputTokens ?? 0) + next.outputTokens,
				...current?.cacheReadTokens === void 0 && next.cacheReadTokens === void 0 ? {} : { cacheReadTokens: (current?.cacheReadTokens ?? 0) + (next.cacheReadTokens ?? 0) },
				...current?.cacheWriteTokens === void 0 && next.cacheWriteTokens === void 0 ? {} : { cacheWriteTokens: (current?.cacheWriteTokens ?? 0) + (next.cacheWriteTokens ?? 0) },
				...current?.reasoningTokens === void 0 && next.reasoningTokens === void 0 ? {} : { reasoningTokens: (current?.reasoningTokens ?? 0) + (next.reasoningTokens ?? 0) }
			};
		}
		function updateChunk(state, chunk, seq, time) {
			if (chunk.type === "usage") return {
				...state,
				sawChunk: true,
				usage: addUsage$1(state.usage, chunk.usage)
			};
			const blocks = [...state.blocks];
			let changedIndex = -1;
			let previousVisible = false;
			switch (chunk.type) {
				case "block-start":
					changedIndex = chunk.index;
					previousVisible = blockIsVisible(blocks[chunk.index]);
					blocks[chunk.index] = emptyAssistantBlock(chunk.blockType);
					break;
				case "text-delta": {
					const previous = blocks[chunk.index];
					changedIndex = chunk.index;
					previousVisible = blockIsVisible(previous);
					blocks[chunk.index] = {
						kind: "text",
						text: (previous?.kind === "text" ? previous.text : "") + chunk.text
					};
					break;
				}
				case "reasoning-delta": {
					const previous = blocks[chunk.index];
					changedIndex = chunk.index;
					previousVisible = blockIsVisible(previous);
					blocks[chunk.index] = {
						kind: "reasoning",
						text: (previous?.kind === "reasoning" ? previous.text : "") + chunk.text
					};
					break;
				}
				case "tool-call-delta": {
					const previous = blocks[chunk.index];
					changedIndex = chunk.index;
					previousVisible = blockIsVisible(previous);
					const base = previous?.kind === "tool-call" ? previous : {
						kind: "tool-call",
						callId: "",
						name: "",
						argsRaw: ""
					};
					blocks[chunk.index] = {
						kind: "tool-call",
						callId: base.callId || String(chunk.id),
						name: chunk.name ?? base.name,
						argsRaw: base.argsRaw + chunk.argumentsDelta
					};
					break;
				}
				case "block-end":
					changedIndex = chunk.index;
					previousVisible = blockIsVisible(blocks[chunk.index]);
					blocks[chunk.index] = toAssistantBlock(chunk.block);
					break;
				default: return {
					...state,
					sawChunk: true
				};
			}
			const visibleBlocks = state.visibleBlocks - Number(previousVisible) + Number(blockIsVisible(blocks[changedIndex]));
			return {
				...state,
				sawChunk: true,
				blocks,
				visibleBlocks,
				...visibleBlocks > 0 && state.firstVisibleSeq === void 0 ? {
					firstVisibleSeq: seq,
					firstVisibleTime: time
				} : {},
				...isTokenDelta(chunk) && state.firstTokenTime === void 0 ? { firstTokenTime: time } : {}
			};
		}
		/** The Step retains its first token across live chunks and settled retry attempts. */
		function settleTiming(state, event) {
			return {
				...state,
				firstTokenTime: state.firstTokenTime ?? assistantStreamFirstTokenTime(event.data.stream)
			};
		}
		function settleMessage(state, match, event) {
			const blocks = toAssistantBlocks(event.data.message.content);
			return {
				...settleTiming(state, event),
				sawChunk: false,
				blocks,
				visibleBlocks: countVisibleBlocks(blocks),
				final: match,
				usage: event.data.usage
			};
		}
		function closedBoundary(context) {
			if (context.state?.stepEnd?.event.type === "step/end") return context.state.stepEnd.event;
			const location = context.start?.location ?? context.matches.at(-1)?.location;
			if (location?.kind === "step" && location.step.status === "closed") return location.step.end;
			if ((location?.kind === "step" || location?.kind === "turn") && location.turn.status === "closed") return location.turn.end;
		}
		function fallbackState$1(context) {
			let state;
			for (const match of context.matches) {
				const event = match.event;
				if (event.type === "assistant/live-chunk") {
					state ??= initialState(event.data.turn, event.data.step, event.seq, event.time, false);
					state = updateChunk(state, event.data.chunk, event.seq, event.time);
				} else if (event.type === "assistant/attempt") {
					state ??= initialState(event.data.turn, event.data.step, event.seq, event.time, false);
					state = settleTiming(state, event);
				} else if (event.type === "assistant/message") {
					state ??= initialState(event.data.turn, event.data.step, event.seq, event.time, false);
					state = settleMessage(state, match, event);
				} else if (event.type === "step/end" && state !== void 0) state = {
					...state,
					stepEnd: match
				};
			}
			return state;
		}
		function finalNode(state, context) {
			const final = state.final;
			if (final?.event.type === "assistant/message") {
				const event = final.event;
				return {
					kind: "assistant",
					seq: event.seq,
					messageId: event.data.message.id,
					time: event.time,
					turn: state.turn,
					step: state.step,
					blocks: toAssistantBlocks(event.data.message.content),
					usage: event.data.usage,
					providerMetadata: {
						provider: event.data.message.source.provider,
						model: event.data.message.source.model
					},
					timing: {
						stepStartTime: state.started ? state.startTime : null,
						firstTokenTime: state.firstTokenTime ?? null,
						completedTime: event.time
					},
					...event.data.interrupted === true ? { interrupted: true } : {}
				};
			}
			const boundary = closedBoundary(context);
			if (boundary === void 0) return void 0;
			const blocks = compactBlocks(state.blocks);
			if (!hasInterruptionEvidence(blocks)) return void 0;
			return {
				kind: "assistant",
				seq: boundary.seq - .9,
				time: boundary.time,
				turn: state.turn,
				step: state.step,
				blocks,
				interrupted: true
			};
		}
		function assistantRequest(state, node, boundary) {
			if (!state.started) return void 0;
			const status = node !== void 0 && node.interrupted !== true ? "complete" : state.retry !== void 0 || boundary !== void 0 ? "error" : "running";
			return {
				purpose: "assistant",
				startSeq: state.startSeq,
				turn: state.turn,
				step: state.step,
				startedAt: state.startTime,
				completedAt: node?.time ?? boundary?.time ?? null,
				status,
				...state.retry === void 0 ? {} : {
					error: state.retry.message,
					...state.retry.code === void 0 ? {} : { errorCode: state.retry.code },
					retry: state.retry.retry,
					...state.retry.maxRetries === void 0 ? {} : { maxRetries: state.retry.maxRetries },
					retryDelayMs: state.retry.delayMs
				},
				...node?.messageId === void 0 ? {} : {
					resultSeq: node.seq,
					...node.providerMetadata === void 0 ? {} : { providerMetadata: node.providerMetadata }
				},
				...state.usage === void 0 ? {} : { usage: state.usage }
			};
		}
		/** Trajectory-owned Assistant streaming, settlement, and request lifecycle. */
		const trajectoryAssistantDefinition = {
			kind: "trajectory-assistant-step",
			target: "trajectory",
			match: (event) => {
				if (event.type === "step/start") return {
					id: `${event.data.turn}:${event.data.step}`,
					role: "start"
				};
				if (event.type === "assistant/live-chunk" || event.type === "assistant/message" || event.type === "assistant/attempt" || event.type === "llm/retry" || event.type === "step/end") return {
					id: `${event.data.turn}:${event.data.step}`,
					role: "update"
				};
				return null;
			},
			start: (_context, match) => {
				if (match.event.type !== "step/start") throw new Error("trajectory-assistant-step start requires step/start");
				return initialState(match.event.data.turn, match.event.data.step, match.event.seq, match.event.time, true);
			},
			update: (context, match) => {
				if (match.event.type === "assistant/live-chunk") return updateChunk(context.state, match.event.data.chunk, match.event.seq, match.event.time);
				if (match.event.type === "assistant/message") return settleMessage(context.state, match, match.event);
				if (match.event.type === "assistant/attempt") return settleTiming(context.state, match.event);
				if (match.event.type === "step/end") return {
					...context.state,
					stepEnd: match
				};
				if (match.event.type !== "llm/retry") return context.state;
				const data = match.event.data;
				const failure = displayFailure(data.failure);
				return {
					...initialState(context.state.turn, context.state.step, context.state.startSeq, context.state.startTime, true),
					firstTokenTime: context.state.firstTokenTime,
					usage: context.state.usage,
					retry: {
						message: failure.message,
						...failure.code === void 0 ? {} : { code: failure.code },
						retry: data.retry,
						...data.mode === "normal" ? { maxRetries: data.maxRetries } : {},
						delayMs: data.delayMs
					}
				};
			},
			publication: (match) => {
				if (match.event.type === "step/start" || match.event.type === "assistant/attempt") return "none";
				if (match.event.type !== "assistant/live-chunk") return "immediate";
				const type = match.event.data.chunk.type;
				return type === "usage" || type === "finish" ? "none" : "animation-frame";
			},
			buildViewNode: (context) => {
				const state = context.state ?? fallbackState$1(context);
				if (state === void 0) return null;
				const node = finalNode(state, context);
				const boundary = closedBoundary(context);
				const partial = node === void 0 && boundary === void 0 && state.sawChunk ? {
					turn: state.turn,
					step: state.step,
					blocks: compactBlocks(state.blocks)
				} : null;
				const request = assistantRequest(state, node, boundary);
				if (node === void 0 && partial === null && request === void 0) return null;
				return trajectoryNode(context, state.startSeq, {
					kind: "assistant",
					...node === void 0 ? {} : { node },
					partial,
					...request === void 0 ? {} : { request }
				});
			}
		};
		const trajectoryTurnEndDefinition = {
			kind: "trajectory-turn-end",
			target: "trajectory",
			match: (event) => event.type === "turn/end" ? {
				id: String(event.seq),
				role: "start"
			} : null,
			start: (_context, match) => {
				if (match.event.type !== "turn/end") throw new Error("trajectory-turn-end start requires turn/end");
				const reason = match.event.data.reason;
				const failure = reason.kind === "error" ? displayFailure(reason.error) : void 0;
				return {
					turn: match.event.data.turn,
					seq: match.event.seq,
					time: match.event.time,
					...failure === void 0 ? {} : {
						error: failure.message,
						...failure.code === void 0 ? {} : { errorCode: failure.code }
					}
				};
			},
			update: (context) => context.state,
			buildViewNode: (context) => context.state === void 0 ? null : trajectoryNode(context, context.state.seq, {
				kind: "turn-end",
				turn: context.state.turn,
				time: context.state.time,
				...context.state.error === void 0 ? {} : { error: context.state.error },
				...context.state.errorCode === void 0 ? {} : { errorCode: context.state.errorCode }
			})
		};
		/**
		* Register the Trajectory Assistant lifecycle.
		*
		* @param ctx - Plugin context receiving the Definitions.
		*/
		function registerTrajectoryAssistantDefinition(ctx) {
			ctx.uiConversation.events.register(trajectoryAssistantDefinition);
			ctx.uiConversation.events.register(trajectoryTurnEndDefinition);
		}
		//#endregion
		//#region src/client/trajectory-compaction-definition.ts
		function checkpointId(event) {
			if (event.type !== "user/message") return void 0;
			const source = event.data.source;
			return source.kind === "compact-checkpoint" && typeof source.compactionId === "string" && source.compactionId !== "" ? source.compactionId : void 0;
		}
		function eventCompactionId(event) {
			if (event.type !== "compaction/start" && event.type !== "compaction/summary" && event.type !== "compaction/end") return void 0;
			const value = event.data.compactionId;
			return typeof value === "string" && value !== "" ? value : void 0;
		}
		function requestFromState(state) {
			const start = state.start.event;
			if (start.type !== "compaction/start") return void 0;
			const summary = state.summary?.event;
			const end = state.end?.event;
			const checkpoint = state.checkpoint?.event;
			return {
				purpose: "compaction",
				startSeq: start.seq,
				turn: start.data.turn,
				step: 0,
				startedAt: start.time,
				completedAt: end?.type === "compaction/end" ? end.time : null,
				status: end?.type !== "compaction/end" ? "running" : end.data.error === void 0 ? "complete" : "error",
				...end?.type === "compaction/end" && end.data.error !== void 0 ? { error: end.data.error } : {},
				...summary?.type !== "compaction/summary" ? {} : {
					resultSeq: summary.seq,
					summary: summary.data.summary,
					...summary.data.rawOutput === void 0 ? {} : { rawOutput: summary.data.rawOutput },
					providerMetadata: {
						provider: summary.data.provider,
						model: summary.data.model
					},
					requestConfig: {
						provider: summary.data.provider,
						model: summary.data.model,
						purpose: "compaction",
						...summary.data.maxTokens === void 0 ? {} : { maxTokens: summary.data.maxTokens }
					},
					...summary.data.usage === void 0 ? {} : { usage: summary.data.usage }
				},
				...checkpoint?.type === "user/message" ? { replacementSeq: checkpoint.seq } : {}
			};
		}
		const trajectoryCompactionDefinition = {
			kind: "trajectory-compaction",
			target: "trajectory",
			match: (event) => {
				const compactId = eventCompactionId(event);
				if (compactId !== void 0) return {
					id: compactId,
					role: event.type === "compaction/start" ? "start" : "update"
				};
				const checkpoint = checkpointId(event);
				return checkpoint === void 0 ? null : {
					id: checkpoint,
					role: "update"
				};
			},
			start: (_context, match) => {
				if (match.event.type !== "compaction/start") throw new Error("trajectory-compaction start requires compaction/start");
				return { start: match };
			},
			update: (context, match) => {
				if (match.event.type === "compaction/summary") return {
					...context.state,
					summary: match
				};
				if (match.event.type === "compaction/end") return {
					...context.state,
					end: match
				};
				return checkpointId(match.event) === void 0 ? context.state : {
					...context.state,
					checkpoint: match
				};
			},
			buildViewNode: (context) => {
				if (context.state === void 0) return null;
				const request = requestFromState(context.state);
				return request === void 0 ? null : trajectoryNode(context, request.startSeq, {
					kind: "compaction",
					request
				});
			}
		};
		const trajectorySessionEndDefinition = {
			kind: "trajectory-session-end",
			target: "trajectory",
			match: (event) => event.type === "session/end-seed" ? {
				id: String(event.seq),
				role: "start"
			} : null,
			start: (_context, match) => ({
				seq: match.event.seq,
				time: match.event.time
			}),
			update: (context) => context.state,
			buildViewNode: (context) => context.state === void 0 ? null : trajectoryNode(context, context.state.seq, {
				kind: "session-end",
				seq: context.state.seq,
				time: context.state.time
			})
		};
		/**
		* Register Trajectory compaction requests and session boundaries.
		*
		* @param ctx - Plugin context receiving the Definitions.
		*/
		function registerTrajectoryCompactionDefinitions(ctx) {
			ctx.uiConversation.events.register(trajectoryCompactionDefinition);
			ctx.uiConversation.events.register(trajectorySessionEndDefinition);
		}
		//#endregion
		//#region src/client/trajectory-message-definitions.ts
		const EMPTY_PENDING = {
			kind: "snapshot",
			ids: []
		};
		const EMPTY_CURRENT_CLAIMED = /* @__PURE__ */ new Set();
		function materializePending(state) {
			const splices = [];
			let current = state;
			while (current.kind === "splice") {
				splices.push(current);
				current = current.previous;
			}
			const pending = [...current.ids];
			for (const splice of splices.reverse()) pending.splice(splice.start, splice.removedCount, ...splice.inserted);
			return pending;
		}
		function withoutInserted(claimed, inserted) {
			let next;
			for (const id of inserted) {
				if (!claimed.has(id)) continue;
				next ??= new Set(claimed);
				next.delete(id);
			}
			return next ?? claimed;
		}
		/**
		* Apply one next-step splice under the AgentLoop's durable event ordering.
		* An entered claim logs its complete message batch before another claim; a
		* rejected claim logs no messages, so only the current claim can classify a
		* later `user/message`.
		*/
		function applySplice(previous, splice) {
			const priorPending = previous?.state.pending ?? EMPTY_PENDING;
			const inserted = splice.inserted.map((identity) => identity.id);
			const removedCount = splice.removedCount ?? 0;
			if (removedCount > 0 && splice.outcome !== "canceled") {
				const pending = materializePending(priorPending);
				const removed = pending.splice(splice.start, removedCount, ...inserted);
				return {
					pending: {
						kind: "snapshot",
						ids: pending
					},
					currentClaimed: new Set(removed)
				};
			}
			const currentClaimed = withoutInserted(previous?.state.currentClaimed ?? EMPTY_CURRENT_CLAIMED, inserted);
			return {
				pending: {
					kind: "splice",
					previous: priorPending,
					start: splice.start,
					removedCount,
					inserted
				},
				currentClaimed
			};
		}
		const trajectoryInboxDefinition = {
			kind: "trajectory-inbox-next-step",
			match: (event) => {
				if (event.type === "agent/inbox/spliced" && event.data.target === "next-step") return {
					id: String(event.seq),
					role: "start"
				};
				return null;
			},
			start: (_context, match, reader) => {
				if (match.event.type !== "agent/inbox/spliced") throw new Error("trajectory-inbox-next-step start requires agent/inbox/spliced");
				return applySplice(reader.previous("trajectory-inbox-next-step"), match.event.data);
			},
			update: (context) => context.state,
			publication: () => "none"
		};
		const trajectoryMessageDefinition = {
			kind: "trajectory-input-message",
			target: "trajectory",
			match: (event) => {
				return event.type === "user/message" || event.type === "developer/message" ? {
					id: String(event.seq),
					role: "start"
				} : null;
			},
			start: (_context, match, reader) => {
				if (match.event.type === "developer/message") {
					const { seq, time, data: { message } } = match.event;
					return {
						kind: "context",
						seq,
						time,
						content: message.content,
						source: message.source,
						producer: contextProducer(message.source),
						form: contextForm(message.source)
					};
				}
				if (match.event.type !== "user/message") throw new Error("trajectory-input-message start requires user/message");
				const event = match.event;
				if (event.data.source.kind !== "user") return {
					kind: "context",
					seq: event.seq,
					time: event.time,
					content: event.data.content,
					source: event.data.source,
					producer: contextProducer(event.data.source),
					form: contextForm(event.data.source)
				};
				return reader.previous("trajectory-inbox-next-step")?.state.currentClaimed.has(String(event.data.id)) === true ? {
					kind: "steering",
					messageId: event.data.id,
					seq: event.seq,
					time: event.time,
					content: event.data.content,
					source: event.data.source
				} : {
					kind: "user",
					seq: event.seq,
					time: event.time,
					content: event.data.content,
					source: event.data.source
				};
			},
			update: (context) => context.state,
			buildViewNode: (context) => context.state === void 0 ? null : trajectoryNode(context, context.state.seq, {
				kind: "node",
				node: context.state
			})
		};
		/**
		* Register Trajectory-owned inbox classification and message records.
		*
		* @param ctx - Plugin context receiving the Definitions.
		*/
		function registerTrajectoryMessageDefinitions(ctx) {
			ctx.uiConversation.events.register(trajectoryInboxDefinition);
			ctx.uiConversation.events.register(trajectoryMessageDefinition);
		}
		//#endregion
		//#region src/client/trajectory-request-header-definition.ts
		/**
		* Definition retaining each `system/message` surface node for the Trajectory
		* request-header Definition, which reads it through `reader.previous` and
		* presents the prompt through the request's `system` cell. A node that
		* lacks a loaded header contributes known text without request config or tools; an in-history
		* update contributes a request-header fact at its own position, since no
		* `request/header` follows a prompt change that keeps the cached history.
		* Surface replacements also contribute prompt changes when they remove the
		* effective node; earlier request facts remain historical.
		* @param inspect - Pure surface interpretation supplied by uiConversation.
		* @returns The Trajectory system-prompt Definition.
		*/
		function trajectorySystemMessageDefinition(inspect) {
			return {
				kind: "trajectory-system-message",
				target: "trajectory",
				match: (event) => event.type === "system/message" || "surfaceOp" in event && event.surfaceOp !== "append" ? {
					id: String(event.seq),
					role: "start"
				} : null,
				start: (_context, match, reader) => {
					if (match.event.type === "assistant/live-chunk") throw new Error("system-message requires a durable event");
					const prior = reader.previous("trajectory-system-message")?.state;
					const state = inspect(prior, match.event);
					const node = state.effective;
					if (state.uncertain) {
						const header = reader.previous("trajectory-request-header")?.state;
						if (header === void 0) return state;
						return {
							...state,
							header: {
								seq: match.event.seq,
								time: match.event.time,
								location: match.location,
								prompt: {
									...header.prompt,
									system: ""
								}
							}
						};
					}
					if (node === void 0 || node.text === prior?.effective?.text || state.introduced !== void 0 && !state.introduced.update) return {
						...state,
						...prior?.header === void 0 ? {} : { header: prior.header }
					};
					const header = reader.previous("trajectory-request-header")?.state;
					const systemHeader = prior?.header;
					const previous = systemHeader !== void 0 && (header === void 0 || systemHeader.seq > header.seq) ? systemHeader : header;
					if (previous === void 0) return state;
					return {
						...state,
						header: {
							seq: node.seq,
							time: node.time,
							prompt: {
								...previous.prompt,
								system: node.text
							},
							change: {
								seq: node.seq,
								time: node.time,
								kind: "system",
								previous: previous.prompt
							},
							location: match.location
						}
					};
				},
				update: (context) => context.state,
				buildViewNode: (context) => {
					const state = context.state;
					if (state?.header !== void 0 && state.header.seq === context.start?.event.seq) return trajectoryNode(context, state.header.seq, {
						kind: "request-header",
						header: state.header
					});
					const prompt = state?.introduced;
					return prompt !== void 0 && prompt.text !== "" && context.start?.event.type === "system/message" && context.start.event.surfaceOp === "append" ? trajectoryNode(context, prompt.seq, {
						kind: "system-prompt",
						prompt
					}) : null;
				}
			};
		}
		/**
		* Request-header fact Definition for the Trajectory target.
		* @param inspect - the shared prompt interpretation, supplied by the
		* uiConversation service (a client bundle cannot value-import it).
		* @returns the Trajectory request-header Definition.
		*/
		function trajectoryRequestHeaderDefinition(inspect) {
			return {
				kind: "trajectory-request-header",
				target: "trajectory",
				match: (event) => event.type === "request/header" ? {
					id: String(event.seq),
					role: "start"
				} : null,
				start: (_context, match, reader) => {
					if (match.event.type !== "request/header") throw new Error("trajectory-request-header start requires request/header");
					const header = reader.previous("trajectory-request-header")?.state;
					const state = reader.previous("trajectory-system-message")?.state;
					const systemHeader = state?.header;
					const previous = systemHeader !== void 0 && (header === void 0 || systemHeader.seq > header.seq) ? systemHeader.prompt : header?.prompt;
					const system = state?.effective;
					const inspection = inspect(previous, match.event, system);
					const { prompt } = inspection;
					const change = inspection.change ?? (systemHeader !== void 0 && (header === void 0 || systemHeader.seq > header.seq) ? systemHeader.change : void 0);
					return {
						seq: match.event.seq,
						time: match.event.time,
						prompt,
						location: match.location,
						...change === void 0 ? {} : { change }
					};
				},
				update: (context) => context.state,
				buildViewNode: (context) => context.state === void 0 ? null : trajectoryNode(context, context.state.seq, {
					kind: "request-header",
					header: context.state
				})
			};
		}
		/**
		* Register Trajectory system-prompt node and request-header facts.
		*
		* @param ctx - Plugin context receiving the Definitions.
		*/
		function registerTrajectoryRequestHeaderDefinition(ctx) {
			ctx.uiConversation.events.register(trajectorySystemMessageDefinition((previous, event) => ctx.uiConversation.inspectSystemPrompt(previous, event)));
			ctx.uiConversation.events.register(trajectoryRequestHeaderDefinition((previous, event, system) => ctx.uiConversation.inspectRequestPrompt(previous, event, system)));
		}
		//#endregion
		//#region src/client/copy-codes.ts
		/** Stable non-display markers for client-synthesized trajectory copy. */
		/** Error marker localized only when the trajectory renders it. */
		const COMPACTION_INTERRUPTED_ERROR = "trajectory.compaction-interrupted";
		//#endregion
		//#region src/client/trajectory-snapshot-builder.ts
		const EMPTY_LIST = [];
		/** Stable empty target used until a Session has assembled Trajectory records. */
		const EMPTY_TRAJECTORY_SNAPSHOT = {
			eventNodes: EMPTY_LIST,
			eventLocations: /* @__PURE__ */ new Map(),
			requests: EMPTY_LIST,
			callSchemas: /* @__PURE__ */ new Map(),
			partial: null,
			runningCalls: EMPTY_LIST
		};
		function stepKey(turn, step) {
			return `${turn}\u0000${step}`;
		}
		function headerStepKey(header) {
			const location = header.location;
			return location.kind === "step" ? stepKey(location.turn.turn, location.step.step) : void 0;
		}
		function headerFor(request, headersByStep, previous) {
			return headersByStep.get(stepKey(request.turn, request.step)) ?? (previous !== void 0 && previous.seq < request.startSeq ? {
				latest: previous,
				...previous.change === void 0 ? {} : { change: previous.change }
			} : void 0);
		}
		function applyHeader(request, header, includeChange) {
			return header === void 0 ? request : {
				...request,
				prompt: header.latest.prompt,
				requestConfig: header.latest.prompt.config,
				...includeChange && header.change !== void 0 ? { promptChange: header.change } : {}
			};
		}
		function withRequestConfig(node, prompt) {
			return prompt === void 0 ? node : {
				...node,
				requestConfig: prompt.config
			};
		}
		function captureSchemas(block, toolsByName, output) {
			const name = "kind" in block ? block.call?.name : block.name;
			const schema = name === void 0 ? void 0 : toolsByName.get(name);
			if (schema !== void 0) output.set(block.callId, schema);
			for (const child of block.subCalls) captureSchemas(child, toolsByName, output);
		}
		function indexTools(tools) {
			return new Map(tools.map((tool) => [tool.name, tool]));
		}
		function interruptCompactions(requests, boundaries) {
			let nextRequest = 0;
			const runningCompactions = [];
			for (const boundary of boundaries) {
				while (nextRequest < requests.length) {
					const request = requests[nextRequest];
					if (request === void 0 || request.startSeq >= boundary.seq) break;
					if (request.purpose === "compaction" && request.status === "running") runningCompactions.push(nextRequest);
					nextRequest++;
				}
				let index = runningCompactions.pop();
				while (index !== void 0 && requests[index]?.status !== "running") index = runningCompactions.pop();
				if (index === void 0) continue;
				const request = requests[index];
				if (request?.purpose !== "compaction") continue;
				requests[index] = {
					...request,
					completedAt: boundary.time,
					status: "error",
					error: COMPACTION_INTERRUPTED_ERROR
				};
			}
		}
		function applyTurnErrors(requests, endings) {
			const lastAssistantByTurn = /* @__PURE__ */ new Map();
			for (const [index, request] of requests.entries()) if (request.purpose === "assistant") lastAssistantByTurn.set(request.turn, index);
			for (const ending of endings) {
				if (ending.error === void 0) continue;
				const index = lastAssistantByTurn.get(ending.turn);
				if (index === void 0) continue;
				const request = requests[index];
				if (request?.purpose !== "assistant") continue;
				requests[index] = {
					...request,
					completedAt: request.completedAt ?? ending.time,
					status: "error",
					error: ending.error,
					...ending.errorCode === void 0 ? {} : { errorCode: ending.errorCode }
				};
			}
		}
		/** Simple keyed adapter retaining the old Trajectory snapshot and stage layout. */
		var TrajectorySnapshotBuilder = class {
			nodes = /* @__PURE__ */ new Map();
			positions = /* @__PURE__ */ new Map();
			contributions = [];
			empty = EMPTY_TRAJECTORY_SNAPSHOT;
			replace(input) {
				this.nodes.clear();
				for (const node of input.nodes) this.nodes.set(node.key, node);
				this.rebuildContributions();
				return this.snapshot();
			}
			apply(input) {
				let structural = false;
				for (const node of input.upserts) {
					const previous = this.nodes.get(node.key);
					this.nodes.set(node.key, node);
					if (previous === void 0 || previous.anchorSeq !== node.anchorSeq) {
						structural = true;
						continue;
					}
					const position = this.positions.get(node.key);
					if (position === void 0) structural = true;
					else this.contributions[position] = node;
				}
				if (structural) this.rebuildContributions();
				return this.snapshot();
			}
			snapshot() {
				const headersByStep = /* @__PURE__ */ new Map();
				for (const contribution of this.contributions) {
					if (contribution.data.kind !== "request-header") continue;
					const key = headerStepKey(contribution.data.header);
					if (key === void 0) continue;
					const previous = headersByStep.get(key);
					headersByStep.set(key, {
						latest: contribution.data.header,
						...contribution.data.header.change !== void 0 ? { change: contribution.data.header.change } : previous?.change === void 0 ? {} : { change: previous.change }
					});
				}
				const representedPrompts = new Set(this.contributions.flatMap((node) => node.data.kind === "request-header" && node.data.header.change !== void 0 ? [node.data.header.change.seq] : []));
				const systemPrompts = [];
				const finalized = [];
				const eventLocations = /* @__PURE__ */ new Map();
				const requests = [];
				const boundaries = [];
				const turnEndings = [];
				const callSchemas = /* @__PURE__ */ new Map();
				const consumedPromptChanges = /* @__PURE__ */ new Set();
				let previousHeader;
				let previousTools = /* @__PURE__ */ new Map();
				let partial = null;
				const runningCalls = [];
				for (const contribution of this.contributions) {
					const data = contribution.data;
					if (data.kind === "system-prompt") {
						if (!representedPrompts.has(data.prompt.seq)) systemPrompts.push(data.prompt);
						continue;
					}
					if (data.kind === "request-header") {
						previousHeader = data.header;
						previousTools = indexTools(data.header.prompt.tools);
						continue;
					}
					if (data.kind === "node") {
						finalized.push(data.node);
						eventLocations.set(data.node.seq, contribution.location);
						continue;
					}
					if (data.kind === "assistant") {
						const header = data.request === void 0 ? void 0 : headerFor(data.request, headersByStep, previousHeader);
						if (data.node !== void 0) finalized.push(withRequestConfig(data.node, header?.latest.prompt));
						if (data.partial !== null) partial = data.partial;
						if (data.request !== void 0) {
							const change = header?.change;
							const includeChange = change !== void 0 && !consumedPromptChanges.has(change.seq);
							requests.push(applyHeader(data.request, header, includeChange));
							if (includeChange) consumedPromptChanges.add(change.seq);
						}
						continue;
					}
					if (data.kind === "tool") {
						if ("kind" in data.root) finalized.push(data.root);
						else runningCalls.push(data.root);
						if (previousHeader !== void 0 && previousHeader.seq < contribution.anchorSeq) captureSchemas(data.root, previousTools, callSchemas);
						continue;
					}
					if (data.kind === "compaction") {
						requests.push(data.request);
						continue;
					}
					if (data.kind === "session-end") {
						boundaries.push({
							seq: data.seq,
							time: data.time
						});
						continue;
					}
					turnEndings.push({
						turn: data.turn,
						time: data.time,
						...data.error === void 0 ? {} : { error: data.error },
						...data.errorCode === void 0 ? {} : { errorCode: data.errorCode }
					});
				}
				requests.sort((left, right) => left.startSeq - right.startSeq);
				interruptCompactions(requests, boundaries);
				applyTurnErrors(requests, turnEndings);
				finalized.sort((left, right) => left.seq - right.seq);
				return {
					eventNodes: finalized,
					...systemPrompts.length > 0 ? { systemPrompts } : {},
					eventLocations,
					requests,
					callSchemas,
					partial,
					runningCalls
				};
			}
			rebuildContributions() {
				this.contributions = [...this.nodes.values()].sort((left, right) => left.anchorSeq - right.anchorSeq || left.key.localeCompare(right.key));
				this.positions.clear();
				for (const [index, contribution] of this.contributions.entries()) this.positions.set(contribution.key, index);
			}
		};
		/** Trajectory target factory preserving the existing stage-oriented view model. */
		const trajectoryViewDefinition = {
			target: "trajectory",
			toolCallFocus: (callId) => callId,
			create: () => new TrajectorySnapshotBuilder()
		};
		/**
		* Register the stage-oriented Trajectory target builder.
		*
		* @param ctx - Plugin context receiving the view Definition.
		*/
		function registerTrajectoryConversationView(ctx) {
			ctx.uiConversation.views.register(trajectoryViewDefinition);
		}
		//#endregion
		//#region src/client/trajectory-tool-definition.ts
		const MAX_DEPTH = 256;
		function rootCall(match) {
			if (match.event.type !== "tool/call") throw new Error("trajectory-tool-call start requires tool/call");
			return {
				phase: "start",
				callId: String(match.event.data.callId),
				name: match.event.data.name,
				argsRaw: match.event.data.arguments,
				turn: match.event.data.turn,
				step: match.event.data.step,
				time: match.event.time,
				subCalls: []
			};
		}
		function rootResult(match, previous) {
			if (match.event.type !== "tool/result") return void 0;
			const message = match.event.data.message;
			return {
				kind: "tool-result",
				seq: match.event.seq,
				time: match.event.time,
				callId: String(message.source.callId),
				call: previous === void 0 ? null : {
					name: previous.name,
					argsRaw: previous.argsRaw
				},
				callTime: previous?.time ?? null,
				content: message.content,
				isError: message.isError === true,
				...match.event.data.error === void 0 ? {} : { error: match.event.data.error },
				meta: match.event.data.meta,
				subCalls: []
			};
		}
		function locationTurn(match) {
			return match.location.kind === "step" || match.location.kind === "turn" ? match.location.turn.turn : 0;
		}
		function locationStep(match) {
			return match.location.kind === "step" ? match.location.step.step : 0;
		}
		function childCall(match, data) {
			return {
				phase: "start",
				callId: data.subCallId,
				parentCallId: data.parentCallId,
				name: data.name,
				argsRaw: JSON.stringify(data.arguments),
				turn: locationTurn(match),
				step: locationStep(match),
				time: match.event.time,
				subCalls: []
			};
		}
		function childResult(match, data, previous) {
			return {
				kind: "tool-result",
				seq: match.event.seq,
				time: match.event.time,
				callId: data.subCallId,
				parentCallId: data.parentCallId,
				call: {
					name: data.name,
					argsRaw: JSON.stringify(data.arguments)
				},
				callTime: previous === void 0 || "kind" in previous ? null : previous.time,
				content: data.content ?? [],
				isError: data.isError === true,
				...data.error === void 0 ? {} : { error: data.error },
				subCalls: []
			};
		}
		function acceptsEdge(state, parent, child) {
			if (parent === child || state.parents.has(child)) return false;
			let cursor = parent;
			let parentDepth = 0;
			const ancestors = /* @__PURE__ */ new Set();
			while (cursor !== void 0) {
				if (cursor === child || ancestors.has(cursor)) return false;
				ancestors.add(cursor);
				parentDepth++;
				cursor = state.parents.get(cursor);
			}
			const pending = [{
				callId: child,
				depth: 1
			}];
			const descendants = /* @__PURE__ */ new Set();
			let subtreeDepth = 0;
			for (const candidate of pending) {
				if (descendants.has(candidate.callId)) return false;
				descendants.add(candidate.callId);
				subtreeDepth = Math.max(subtreeDepth, candidate.depth);
				for (const nested of state.children.get(candidate.callId) ?? []) pending.push({
					callId: nested,
					depth: candidate.depth + 1
				});
			}
			return parentDepth + subtreeDepth <= MAX_DEPTH;
		}
		function updateDispatch(state, match) {
			const event = match.event;
			if (event.type !== "tool/ptc-dispatch-start" && event.type !== "tool/ptc-dispatch") return state;
			const data = event.data;
			const parentId = String(data.parentCallId);
			const childId = String(data.subCallId);
			const siblings = state.children.get(parentId) ?? [];
			const index = siblings.indexOf(childId);
			if (index < 0 && !acceptsEdge(state, parentId, childId)) return state;
			if (event.type === "tool/ptc-dispatch-start" && index >= 0) return state;
			const calls = new Map(state.calls);
			calls.set(childId, event.type === "tool/ptc-dispatch-start" ? childCall(match, data) : childResult(match, data, calls.get(childId)));
			if (index >= 0) return {
				...state,
				calls
			};
			const children = new Map(state.children);
			children.set(parentId, [...siblings, childId]);
			const parents = new Map(state.parents);
			parents.set(childId, parentId);
			return {
				...state,
				calls,
				children,
				parents
			};
		}
		function interruption(context) {
			const location = context.start?.location;
			if (location?.kind === "step" && location.step.status === "closed") return location.step.end;
			if ((location?.kind === "step" || location?.kind === "turn") && location.turn.status === "closed") return location.turn.end;
		}
		function projectCall(state, callId, interruptedAt, visited = /* @__PURE__ */ new Set(), depth = 1) {
			const block = state.calls.get(callId);
			if (block === void 0) return void 0;
			if (visited.has(callId) || depth > MAX_DEPTH) return {
				...block,
				subCalls: []
			};
			const nextVisited = new Set(visited);
			nextVisited.add(callId);
			const subCalls = (state.children.get(callId) ?? []).flatMap((childId) => {
				const child = projectCall(state, childId, interruptedAt, nextVisited, depth + 1);
				return child === void 0 ? [] : [child];
			});
			if ("kind" in block || interruptedAt === void 0) return {
				...block,
				subCalls
			};
			return {
				kind: "tool-result",
				seq: interruptedAt.seq - .8,
				time: interruptedAt.time,
				callId: block.callId,
				...block.parentCallId === void 0 ? {} : { parentCallId: block.parentCallId },
				call: {
					name: block.name,
					argsRaw: block.argsRaw
				},
				callTime: block.time,
				content: [],
				isError: true,
				error: {
					name: "Interrupted",
					code: "interrupted"
				},
				subCalls
			};
		}
		function fallbackState(context) {
			const resultMatch = context.matches.find((match) => match.event.type === "tool/result");
			const root = resultMatch === void 0 ? void 0 : rootResult(resultMatch);
			if (root === void 0) return void 0;
			let state = {
				rootId: root.callId,
				calls: new Map([[root.callId, root]]),
				children: /* @__PURE__ */ new Map(),
				parents: /* @__PURE__ */ new Map()
			};
			for (const match of context.matches) state = updateDispatch(state, match);
			return state;
		}
		/** Trajectory-owned root Tool lifecycle with nested PTC dispatch calls. */
		const trajectoryToolDefinition = {
			kind: "trajectory-tool-call",
			target: "trajectory",
			match: (event) => {
				if (event.type === "tool/call") return {
					id: String(event.data.callId),
					role: "start"
				};
				if (event.type === "tool/result") return {
					id: String(event.data.message.source.callId),
					role: "update"
				};
				if (event.type === "tool/ptc-dispatch-start" || event.type === "tool/ptc-dispatch") {
					const rootCallId = event.data.rootCallId;
					return typeof rootCallId === "string" && rootCallId !== "" ? {
						id: rootCallId,
						role: "update"
					} : null;
				}
				return null;
			},
			start: (_context, match) => {
				const root = rootCall(match);
				return {
					rootId: root.callId,
					calls: new Map([[root.callId, root]]),
					children: /* @__PURE__ */ new Map(),
					parents: /* @__PURE__ */ new Map()
				};
			},
			update: (context, match) => {
				if (match.event.type !== "tool/result") return updateDispatch(context.state, match);
				const previous = context.state.calls.get(context.state.rootId);
				const result = rootResult(match, previous !== void 0 && !("kind" in previous) ? previous : void 0);
				if (result === void 0) return context.state;
				const calls = new Map(context.state.calls);
				calls.set(context.state.rootId, result);
				return {
					...context.state,
					calls
				};
			},
			buildViewNode: (context) => {
				const state = context.state ?? fallbackState(context);
				if (state === void 0) return null;
				const root = projectCall(state, state.rootId, interruption(context));
				if (root === void 0) return null;
				return trajectoryNode(context, context.start?.event.seq ?? ("kind" in root ? root.seq : context.matches[0]?.event.seq ?? 0), {
					kind: "tool",
					root
				});
			}
		};
		/**
		* Register the Trajectory Tool lifecycle with raw native and PTC error details.
		*
		* @param ctx - Plugin context receiving the Definition.
		*/
		function registerTrajectoryToolDefinition(ctx) {
			ctx.uiConversation.events.register(trajectoryToolDefinition);
		}
		//#endregion
		//#region ../../../node_modules/.pnpm/@tanstack+virtual-core@3.17.7/node_modules/@tanstack/virtual-core/dist/esm/lazy-measurements.js
		function createLazyMeasurementsView(count, flat, getItemKey) {
			const cache = new Array(count);
			return new Proxy(cache, { get(target, prop, receiver) {
				if (typeof prop === "string") {
					const c = prop.charCodeAt(0);
					if (c >= 48 && c <= 57) {
						const i = +prop;
						if (Number.isInteger(i) && i >= 0 && i < count) {
							let v = target[i];
							if (!v) {
								const s = flat[i * 2];
								v = target[i] = {
									index: i,
									key: getItemKey(i),
									start: s,
									size: flat[i * 2 + 1],
									end: s + flat[i * 2 + 1],
									lane: 0
								};
							}
							return v;
						}
					}
					if (prop === "length") return count;
				}
				return Reflect.get(target, prop, receiver);
			} });
		}
		//#endregion
		//#region ../../../node_modules/.pnpm/@tanstack+virtual-core@3.17.7/node_modules/@tanstack/virtual-core/dist/esm/utils.js
		function memo$1(getDeps, fn, opts) {
			let deps = opts.initialDeps ?? [];
			let result;
			let isInitial = true;
			function memoizedFunction() {
				const newDeps = getDeps();
				if (!(newDeps.length !== deps.length || newDeps.some((dep, index) => deps[index] !== dep))) return result;
				deps = newDeps;
				result = fn(...newDeps);
				if ((opts == null ? void 0 : opts.onChange) && !(isInitial && opts.skipInitialOnChange)) opts.onChange(result);
				isInitial = false;
				return result;
			}
			memoizedFunction.updateDeps = (newDeps) => {
				deps = newDeps;
			};
			return memoizedFunction;
		}
		function notUndefined(value, msg) {
			if (value === void 0) throw new Error(`Unexpected undefined${msg ? `: ${msg}` : ""}`);
			else return value;
		}
		const approxEqual = (a, b) => Math.abs(a - b) < 1.01;
		const debounce = (targetWindow, fn, ms) => {
			let timeoutId;
			return function(...args) {
				targetWindow.clearTimeout(timeoutId);
				timeoutId = targetWindow.setTimeout(() => fn.apply(this, args), ms);
			};
		};
		//#endregion
		//#region ../../../node_modules/.pnpm/@tanstack+virtual-core@3.17.7/node_modules/@tanstack/virtual-core/dist/esm/index.js
		let _isIOSResult;
		const isIOSWebKit = () => {
			if (_isIOSResult !== void 0) return _isIOSResult;
			if (typeof navigator === "undefined") return _isIOSResult = false;
			if (/iP(hone|od|ad)/.test(navigator.userAgent)) return _isIOSResult = true;
			const mtp = navigator.maxTouchPoints;
			return _isIOSResult = navigator.platform === "MacIntel" && mtp !== void 0 && mtp > 0;
		};
		const getRect = (element) => {
			const { offsetWidth, offsetHeight } = element;
			return {
				width: offsetWidth,
				height: offsetHeight
			};
		};
		const defaultKeyExtractor = (index) => index;
		const defaultRangeExtractor = (range) => {
			const start = Math.max(range.startIndex - range.overscan, 0);
			const len = Math.min(range.endIndex + range.overscan, range.count - 1) - start + 1;
			const arr = new Array(len);
			for (let i = 0; i < len; i++) arr[i] = start + i;
			return arr;
		};
		const observeElementRect = (instance, cb) => {
			const element = instance.scrollElement;
			if (!element) return;
			const targetWindow = instance.targetWindow;
			if (!targetWindow) return;
			const handler = (rect) => {
				const { width, height } = rect;
				cb({
					width: Math.round(width),
					height: Math.round(height)
				});
			};
			handler(getRect(element));
			if (!targetWindow.ResizeObserver) return () => {};
			const observer = new targetWindow.ResizeObserver((entries) => {
				const run = () => {
					const entry = entries[0];
					if (entry == null ? void 0 : entry.borderBoxSize) {
						const box = entry.borderBoxSize[0];
						if (box) {
							handler({
								width: box.inlineSize,
								height: box.blockSize
							});
							return;
						}
					}
					handler(getRect(element));
				};
				instance.options.useAnimationFrameWithResizeObserver ? requestAnimationFrame(run) : run();
			});
			observer.observe(element, { box: "border-box" });
			return () => {
				observer.unobserve(element);
			};
		};
		const addEventListenerOptions = { passive: true };
		const supportsScrollend = typeof window == "undefined" ? true : "onscrollend" in window;
		const observeOffset = (instance, cb, readOffset) => {
			const element = instance.scrollElement;
			if (!element) return;
			const targetWindow = instance.targetWindow;
			if (!targetWindow) return;
			const registerScrollendEvent = instance.options.useScrollendEvent && supportsScrollend;
			let offset = 0;
			const fallback = registerScrollendEvent ? null : debounce(targetWindow, () => cb(offset, false), instance.options.isScrollingResetDelay);
			const createHandler = (isScrolling) => () => {
				offset = readOffset(element);
				fallback?.();
				cb(offset, isScrolling);
			};
			const handler = createHandler(true);
			const endHandler = createHandler(false);
			element.addEventListener("scroll", handler, addEventListenerOptions);
			if (registerScrollendEvent) element.addEventListener("scrollend", endHandler, addEventListenerOptions);
			return () => {
				element.removeEventListener("scroll", handler);
				if (registerScrollendEvent) element.removeEventListener("scrollend", endHandler);
			};
		};
		const observeElementOffset = (instance, cb) => observeOffset(instance, cb, (el) => {
			const { horizontal, isRtl } = instance.options;
			return horizontal ? el.scrollLeft * (isRtl && -1 || 1) : el.scrollTop;
		});
		const measureElement = (element, entry, instance) => {
			if (instance.options.useCachedMeasurements) {
				const index = instance.indexFromElement(element);
				const key = instance.options.getItemKey(index);
				return instance.itemSizeCache.get(key) ?? instance.options.estimateSize(index);
			}
			if (entry == null ? void 0 : entry.borderBoxSize) {
				const box = entry.borderBoxSize[0];
				if (box) return Math.round(box[instance.options.horizontal ? "inlineSize" : "blockSize"]);
			}
			if (!entry) {
				const index = instance.indexFromElement(element);
				const key = instance.options.getItemKey(index);
				const cachedSize = instance.itemSizeCache.get(key);
				if (cachedSize !== void 0) return cachedSize;
			}
			return element[instance.options.horizontal ? "offsetWidth" : "offsetHeight"];
		};
		const scrollWithAdjustments = (offset, { adjustments = 0, behavior }, instance) => {
			var _a, _b;
			(_b = (_a = instance.scrollElement) == null ? void 0 : _a.scrollTo) == null || _b.call(_a, {
				[instance.options.horizontal ? "left" : "top"]: offset + adjustments,
				behavior
			});
		};
		const elementScroll = scrollWithAdjustments;
		var Virtualizer = class {
			constructor(opts) {
				this.unsubs = [];
				this.scrollElement = null;
				this.targetWindow = null;
				this.isScrolling = false;
				this.scrollState = null;
				this.measurementsCache = [];
				this._flatMeasurements = null;
				this.itemSizeCache = /* @__PURE__ */ new Map();
				this.itemSizeCacheVersion = 0;
				this.laneAssignments = /* @__PURE__ */ new Map();
				this.pendingMin = null;
				this.prevLanes = void 0;
				this.lanesChangedFlag = false;
				this.lanesSettling = false;
				this.pendingScrollAnchor = null;
				this.scrollRect = null;
				this.scrollOffset = null;
				this.scrollDirection = null;
				this.scrollAdjustments = 0;
				this._iosDeferredAdjustment = 0;
				this._iosTouching = false;
				this._iosJustTouchEnded = false;
				this._iosTouchEndTimerId = null;
				this._intendedScrollOffset = null;
				this.elementsCache = /* @__PURE__ */ new Map();
				this.now = () => {
					var _a, _b, _c;
					return ((_c = (_b = (_a = this.targetWindow) == null ? void 0 : _a.performance) == null ? void 0 : _b.now) == null ? void 0 : _c.call(_b)) ?? Date.now();
				};
				this.observer = /* @__PURE__ */ (() => {
					let _ro = null;
					const get = () => {
						if (_ro) return _ro;
						if (!this.targetWindow || !this.targetWindow.ResizeObserver) return null;
						return _ro = new this.targetWindow.ResizeObserver((entries) => {
							entries.forEach((entry) => {
								const run = () => {
									const node = entry.target;
									const index = this.indexFromElement(node);
									if (!node.isConnected) {
										this.observer.unobserve(node);
										for (const [cacheKey, cachedNode] of this.elementsCache) if (cachedNode === node) {
											this.elementsCache.delete(cacheKey);
											break;
										}
										return;
									}
									if (this.shouldMeasureDuringScroll(index)) this.resizeItem(index, this.options.measureElement(node, entry, this));
								};
								this.options.useAnimationFrameWithResizeObserver ? requestAnimationFrame(run) : run();
							});
						});
					};
					return {
						disconnect: () => {
							var _a;
							(_a = get()) == null || _a.disconnect();
							_ro = null;
						},
						observe: (target) => {
							var _a;
							return (_a = get()) == null ? void 0 : _a.observe(target, { box: "border-box" });
						},
						unobserve: (target) => {
							var _a;
							return (_a = get()) == null ? void 0 : _a.unobserve(target);
						}
					};
				})();
				this.range = null;
				this.setOptions = (opts2) => {
					var _a, _b;
					const merged = {
						debug: false,
						initialOffset: 0,
						overscan: 1,
						paddingStart: 0,
						paddingEnd: 0,
						scrollPaddingStart: 0,
						scrollPaddingEnd: 0,
						horizontal: false,
						getItemKey: defaultKeyExtractor,
						rangeExtractor: defaultRangeExtractor,
						onChange: () => {},
						measureElement,
						initialRect: {
							width: 0,
							height: 0
						},
						scrollMargin: 0,
						gap: 0,
						indexAttribute: "data-index",
						initialMeasurementsCache: [],
						lanes: 1,
						anchorTo: "start",
						followOnAppend: false,
						scrollEndThreshold: 1,
						isScrollingResetDelay: 150,
						enabled: true,
						isRtl: false,
						useScrollendEvent: false,
						useAnimationFrameWithResizeObserver: false,
						laneAssignmentMode: "estimate",
						useCachedMeasurements: false
					};
					for (const key in opts2) {
						const v = opts2[key];
						if (v !== void 0) merged[key] = v;
					}
					const prevOptions = this.options;
					let anchor = null;
					let followOnAppend = null;
					let edgeKeysChanged = false;
					if (prevOptions !== void 0 && prevOptions.enabled && merged.enabled && merged.anchorTo === "end" && this.scrollElement !== null) {
						const prevCount = prevOptions.count;
						const nextCount = merged.count;
						const measurements = this.getMeasurements();
						const prevFirstKey = prevCount > 0 ? ((_a = measurements[0]) == null ? void 0 : _a.key) ?? prevOptions.getItemKey(0) : null;
						const prevLastKey = prevCount > 0 ? ((_b = measurements[prevCount - 1]) == null ? void 0 : _b.key) ?? prevOptions.getItemKey(prevCount - 1) : null;
						if (nextCount !== prevCount || prevCount > 0 && nextCount > 0 && (merged.getItemKey(0) !== prevFirstKey || merged.getItemKey(nextCount - 1) !== prevLastKey)) {
							edgeKeysChanged = true;
							const item = prevCount > 0 ? this.getVirtualItemForOffset(this.getScrollOffset()) ?? measurements[0] : null;
							if (item) anchor = [item.key, this.getScrollOffset() - item.start];
							const behavior = merged.followOnAppend === true ? "auto" : merged.followOnAppend || null;
							if (behavior && nextCount > prevCount && this.isAtEnd(prevOptions.scrollEndThreshold) && (prevCount === 0 || merged.getItemKey(nextCount - 1) !== prevLastKey)) followOnAppend = behavior;
						}
					}
					this.options = merged;
					if (edgeKeysChanged) {
						this.pendingMin = 0;
						this.itemSizeCacheVersion++;
					}
					let anchorResolved = false;
					let anchorDelta = 0;
					if (anchor && this.scrollOffset !== null) {
						const [anchorKey, anchorOffset] = anchor;
						const newMeasurements = this.getMeasurements();
						const { count, getItemKey } = this.options;
						let idx = 0;
						while (idx < count && getItemKey(idx) !== anchorKey) idx++;
						if (idx < count) {
							const anchorItem = newMeasurements[idx];
							if (anchorItem) {
								const newOffset = Math.max(0, anchorItem.start + anchorOffset);
								if (newOffset !== this.scrollOffset) {
									anchorDelta = newOffset - this.scrollOffset;
									this.scrollOffset = newOffset;
									anchorResolved = true;
								}
							}
						}
					}
					if (anchorResolved || followOnAppend) this.pendingScrollAnchor = [
						anchorResolved ? anchor[0] : null,
						anchorResolved ? anchor[1] : 0,
						followOnAppend,
						anchorDelta
					];
				};
				this.notify = (sync) => {
					var _a, _b;
					(_b = (_a = this.options).onChange) == null || _b.call(_a, this, sync);
				};
				this.maybeNotify = memo$1(() => {
					this.calculateRange();
					return [
						this.isScrolling,
						this.range ? this.range.startIndex : null,
						this.range ? this.range.endIndex : null
					];
				}, (isScrolling) => {
					this.notify(isScrolling);
				}, {
					key: false,
					debug: () => this.options.debug,
					initialDeps: [
						this.isScrolling,
						this.range ? this.range.startIndex : null,
						this.range ? this.range.endIndex : null
					]
				});
				this.cleanup = () => {
					this.unsubs.filter(Boolean).forEach((d) => d());
					this.unsubs = [];
					this.observer.disconnect();
					if (this.rafId != null && this.targetWindow) {
						this.targetWindow.cancelAnimationFrame(this.rafId);
						this.rafId = null;
					}
					this.scrollState = null;
					this._iosDeferredAdjustment = 0;
					this._iosTouching = false;
					this._iosJustTouchEnded = false;
					this.scrollElement = null;
					this.targetWindow = null;
				};
				this._didMount = () => {
					return () => {
						this.cleanup();
					};
				};
				this._willUpdate = () => {
					var _a;
					const scrollElement = this.options.enabled ? this.options.getScrollElement() : null;
					if (this.scrollElement !== scrollElement) {
						this.cleanup();
						if (!scrollElement) {
							this.maybeNotify();
							return;
						}
						this.scrollElement = scrollElement;
						if (this.scrollElement && "ownerDocument" in this.scrollElement) this.targetWindow = this.scrollElement.ownerDocument.defaultView;
						else this.targetWindow = ((_a = this.scrollElement) == null ? void 0 : _a.window) ?? null;
						this.elementsCache.forEach((cached) => {
							this.observer.observe(cached);
						});
						this.unsubs.push(this.options.observeElementRect(this, (rect) => {
							this.scrollRect = rect;
							this.maybeNotify();
						}));
						this.unsubs.push(this.options.observeElementOffset(this, (offset, isScrolling) => {
							if (isScrolling && this._intendedScrollOffset === null && offset === this.scrollOffset) return;
							if (this._intendedScrollOffset !== null && Math.abs(offset - this._intendedScrollOffset) < 1.5) offset = this._intendedScrollOffset;
							this._intendedScrollOffset = null;
							this.scrollAdjustments = 0;
							const prevOffset = this.getScrollOffset();
							this.scrollDirection = isScrolling ? prevOffset === offset ? this.scrollDirection : prevOffset < offset ? "forward" : "backward" : null;
							this.scrollOffset = offset;
							this.isScrolling = isScrolling;
							this._flushIosDeferredIfReady();
							if (this.scrollState) this.scheduleScrollReconcile();
							this.maybeNotify();
						}));
						if ("addEventListener" in this.scrollElement) {
							const scrollEl = this.scrollElement;
							const onTouchStart = () => {
								this._iosTouching = true;
								this._iosJustTouchEnded = false;
								if (this._iosTouchEndTimerId !== null && this.targetWindow != null) {
									this.targetWindow.clearTimeout(this._iosTouchEndTimerId);
									this._iosTouchEndTimerId = null;
								}
							};
							const onTouchEnd = () => {
								this._iosTouching = false;
								if (!isIOSWebKit() || this.targetWindow == null) return;
								this._iosJustTouchEnded = true;
								this._iosTouchEndTimerId = this.targetWindow.setTimeout(() => {
									this._iosJustTouchEnded = false;
									this._iosTouchEndTimerId = null;
									this._flushIosDeferredIfReady();
								}, 150);
							};
							scrollEl.addEventListener("touchstart", onTouchStart, addEventListenerOptions);
							scrollEl.addEventListener("touchend", onTouchEnd, addEventListenerOptions);
							this.unsubs.push(() => {
								scrollEl.removeEventListener("touchstart", onTouchStart);
								scrollEl.removeEventListener("touchend", onTouchEnd);
								if (this._iosTouchEndTimerId !== null && this.targetWindow != null) {
									this.targetWindow.clearTimeout(this._iosTouchEndTimerId);
									this._iosTouchEndTimerId = null;
								}
							});
						}
						this._scrollToOffset(this.getScrollOffset(), {
							adjustments: void 0,
							behavior: void 0
						});
					}
					const anchor = this.pendingScrollAnchor;
					this.pendingScrollAnchor = null;
					if (anchor && this.scrollElement && this.options.enabled) {
						const [key, _offset, followOnAppend, anchorDelta] = anchor;
						if (key !== null && !followOnAppend) if (isIOSWebKit() && (this.isScrolling || this._iosTouching || this._iosJustTouchEnded)) {
							if (anchorDelta !== 0) this._iosDeferredAdjustment += anchorDelta;
						} else this._scrollToOffset(this.getScrollOffset(), {
							adjustments: void 0,
							behavior: void 0
						});
						if (followOnAppend) this.scrollToEnd({ behavior: followOnAppend });
					}
				};
				this._flushIosDeferredIfReady = () => {
					if (this._iosDeferredAdjustment === 0) return;
					if (this.isScrolling) return;
					if (this._iosTouching) return;
					if (this._iosJustTouchEnded) return;
					const cur = this.getScrollOffset();
					const max = this.getMaxScrollOffset();
					if (cur < 0 || cur > max) return;
					if (this._iosDeferredAdjustment < 0 && cur >= max - 1) {
						this._iosDeferredAdjustment = 0;
						return;
					}
					const delta = this._iosDeferredAdjustment;
					this._iosDeferredAdjustment = 0;
					this._scrollToOffset(cur, {
						adjustments: this.scrollAdjustments += delta,
						behavior: void 0
					});
				};
				this.rafId = null;
				this.getSize = () => {
					if (!this.options.enabled) {
						this.scrollRect = null;
						return 0;
					}
					this.scrollRect = this.scrollRect ?? this.options.initialRect;
					return this.scrollRect[this.options.horizontal ? "width" : "height"];
				};
				this.getScrollOffset = () => {
					if (!this.options.enabled) {
						this.scrollOffset = null;
						return 0;
					}
					this.scrollOffset = this.scrollOffset ?? (typeof this.options.initialOffset === "function" ? this.options.initialOffset() : this.options.initialOffset);
					return this.scrollOffset;
				};
				this.getMeasurementOptions = memo$1(() => [
					this.options.count,
					this.options.paddingStart,
					this.options.scrollMargin,
					this.options.getItemKey,
					this.options.enabled,
					this.options.lanes,
					this.options.laneAssignmentMode,
					this.options.gap
				], (count, paddingStart, scrollMargin, getItemKey, enabled, lanes, laneAssignmentMode, gap) => {
					if (this.prevLanes !== void 0 && this.prevLanes !== lanes) this.lanesChangedFlag = true;
					this.prevLanes = lanes;
					this.pendingMin = null;
					return {
						count,
						paddingStart,
						scrollMargin,
						getItemKey,
						enabled,
						lanes,
						laneAssignmentMode,
						gap
					};
				}, { key: false });
				this.getMeasurements = memo$1(() => [this.getMeasurementOptions(), this.itemSizeCacheVersion], ({ count, paddingStart, scrollMargin, getItemKey, enabled, lanes, laneAssignmentMode, gap }, _itemSizeCacheVersion) => {
					const itemSizeCache = this.itemSizeCache;
					if (!enabled) {
						this.measurementsCache = [];
						this.itemSizeCache.clear();
						this.laneAssignments.clear();
						return [];
					}
					if (this.laneAssignments.size > count) {
						for (const index of this.laneAssignments.keys()) if (index >= count) this.laneAssignments.delete(index);
					}
					if (this.lanesChangedFlag) {
						this.lanesChangedFlag = false;
						this.lanesSettling = true;
						this.measurementsCache = [];
						this.itemSizeCache.clear();
						this.laneAssignments.clear();
						this.pendingMin = null;
					}
					if (this.measurementsCache.length === 0 && !this.lanesSettling) {
						this.measurementsCache = this.options.initialMeasurementsCache;
						this.measurementsCache.forEach((item) => {
							this.itemSizeCache.set(item.key, item.size);
						});
					}
					const min = this.lanesSettling ? 0 : this.pendingMin ?? 0;
					this.pendingMin = null;
					if (this.lanesSettling && this.measurementsCache.length === count) this.lanesSettling = false;
					if (lanes === 1) {
						const need = count * 2;
						let flat = this._flatMeasurements;
						if (!flat || flat.length < need) {
							const next = new Float64Array(need);
							if (flat && min > 0) next.set(flat.subarray(0, min * 2));
							flat = next;
							this._flatMeasurements = flat;
						}
						let runningStart;
						if (min === 0) runningStart = paddingStart + scrollMargin;
						else {
							const prevIdx = min - 1;
							runningStart = flat[prevIdx * 2] + flat[prevIdx * 2 + 1] + gap;
						}
						for (let i = min; i < count; i++) {
							const key = getItemKey(i);
							const measuredSize = itemSizeCache.get(key);
							const size = typeof measuredSize === "number" ? measuredSize : this.options.estimateSize(i);
							flat[i * 2] = runningStart;
							flat[i * 2 + 1] = size;
							runningStart += size + gap;
						}
						const view = createLazyMeasurementsView(count, flat, getItemKey);
						this.measurementsCache = view;
						return view;
					}
					const measurements = this.measurementsCache.slice(0, min);
					const laneLastIndex = new Array(lanes).fill(void 0);
					const laneEnds = new Float64Array(lanes);
					let filledLanes = 0;
					for (let m = 0; m < min; m++) {
						const item = measurements[m];
						if (item) {
							if (laneLastIndex[item.lane] === void 0) filledLanes++;
							laneLastIndex[item.lane] = m;
							laneEnds[item.lane] = item.end;
						}
					}
					for (let i = min; i < count; i++) {
						const key = getItemKey(i);
						const cachedLane = this.laneAssignments.get(i);
						let lane;
						let start;
						const shouldCacheLane = laneAssignmentMode === "estimate" || itemSizeCache.has(key);
						if (cachedLane !== void 0 && this.options.lanes > 1) {
							lane = cachedLane;
							const prevIndex = laneLastIndex[lane];
							const prevInLane = prevIndex !== void 0 ? measurements[prevIndex] : void 0;
							start = prevInLane ? prevInLane.end + gap : paddingStart + scrollMargin;
						} else if (filledLanes === lanes) {
							let bestLane = 0;
							let bestEnd = laneEnds[0];
							let bestIdx = laneLastIndex[0];
							for (let l = 1; l < lanes; l++) {
								const e = laneEnds[l];
								if (e < bestEnd || e === bestEnd && laneLastIndex[l] < bestIdx) {
									bestLane = l;
									bestEnd = e;
									bestIdx = laneLastIndex[l];
								}
							}
							lane = bestLane;
							start = bestEnd + gap;
							if (shouldCacheLane) this.laneAssignments.set(i, lane);
						} else {
							lane = i % this.options.lanes;
							start = paddingStart + scrollMargin;
							if (shouldCacheLane) this.laneAssignments.set(i, lane);
						}
						const measuredSize = itemSizeCache.get(key);
						const size = typeof measuredSize === "number" ? measuredSize : this.options.estimateSize(i);
						const end = start + size;
						measurements[i] = {
							index: i,
							start,
							size,
							end,
							key,
							lane
						};
						if (laneLastIndex[lane] === void 0) filledLanes++;
						laneLastIndex[lane] = i;
						laneEnds[lane] = end;
					}
					this.measurementsCache = measurements;
					return measurements;
				}, {
					key: false,
					debug: () => this.options.debug
				});
				this.calculateRange = memo$1(() => [
					this.getMeasurements(),
					this.getSize(),
					this.getScrollOffset(),
					this.options.lanes
				], (measurements, outerSize, scrollOffset, lanes) => {
					if (measurements.length === 0 || outerSize === 0) {
						this.range = null;
						return null;
					}
					this.range = calculateRangeImpl(measurements, outerSize, scrollOffset, lanes, lanes === 1 && this._flatMeasurements != null ? this._flatMeasurements : null);
					return this.range;
				}, {
					key: false,
					debug: () => this.options.debug
				});
				this.getVirtualIndexes = memo$1(() => {
					let startIndex = null;
					let endIndex = null;
					const range = this.calculateRange();
					if (range) {
						startIndex = range.startIndex;
						endIndex = range.endIndex;
					}
					this.maybeNotify.updateDeps([
						this.isScrolling,
						startIndex,
						endIndex
					]);
					return [
						this.options.rangeExtractor,
						this.options.overscan,
						this.options.count,
						startIndex,
						endIndex
					];
				}, (rangeExtractor, overscan, count, startIndex, endIndex) => {
					return startIndex === null || endIndex === null ? [] : rangeExtractor({
						startIndex,
						endIndex,
						overscan,
						count
					});
				}, {
					key: false,
					debug: () => this.options.debug
				});
				this.indexFromElement = (node) => {
					const attributeName = this.options.indexAttribute;
					const indexStr = node.getAttribute(attributeName);
					if (!indexStr) {
						console.warn(`Missing attribute name '${attributeName}={index}' on measured element.`);
						return -1;
					}
					return parseInt(indexStr, 10);
				};
				this.shouldMeasureDuringScroll = (index) => {
					var _a;
					if (!this.scrollState || this.scrollState.behavior !== "smooth") return true;
					const scrollIndex = this.scrollState.index ?? ((_a = this.getVirtualItemForOffset(this.scrollState.lastTargetOffset)) == null ? void 0 : _a.index);
					if (scrollIndex !== void 0 && this.range) {
						const bufferSize = Math.max(this.options.overscan, Math.ceil((this.range.endIndex - this.range.startIndex) / 2));
						const minIndex = Math.max(0, scrollIndex - bufferSize);
						const maxIndex = Math.min(this.options.count - 1, scrollIndex + bufferSize);
						return index >= minIndex && index <= maxIndex;
					}
					return true;
				};
				this.measureElement = (node) => {
					if (!node) {
						this.elementsCache.forEach((cached, key2) => {
							if (!cached.isConnected) {
								this.observer.unobserve(cached);
								this.elementsCache.delete(key2);
							}
						});
						return;
					}
					const index = this.indexFromElement(node);
					const key = this.options.getItemKey(index);
					const prevNode = this.elementsCache.get(key);
					if (prevNode !== node) {
						if (prevNode) this.observer.unobserve(prevNode);
						this.observer.observe(node);
						this.elementsCache.set(key, node);
					}
					if ((!this.isScrolling || this.scrollState) && this.shouldMeasureDuringScroll(index)) this.resizeItem(index, this.options.measureElement(node, void 0, this));
				};
				this.resizeItem = (index, size) => {
					var _a, _b;
					if (index < 0 || index >= this.options.count) return;
					let cachedSize;
					let itemStart;
					let key;
					const flat = this._flatMeasurements;
					if (this.options.lanes === 1 && flat !== null) {
						key = this.options.getItemKey(index);
						itemStart = flat[index * 2];
						cachedSize = flat[index * 2 + 1];
					} else {
						const item = this.measurementsCache[index];
						if (!item) return;
						key = item.key;
						itemStart = item.start;
						cachedSize = item.size;
					}
					const itemSize = this.itemSizeCache.get(key) ?? cachedSize;
					const delta = size - itemSize;
					if (delta !== 0) {
						const wasAtEnd = this.options.anchorTo === "end" && ((_a = this.scrollState) == null ? void 0 : _a.behavior) !== "smooth" && this.getVirtualDistanceFromEnd() <= this.options.scrollEndThreshold;
						const prevTotalSize = wasAtEnd ? this.getTotalSize() : 0;
						const scrollOffsetWithAdj = this.getScrollOffset() + this.scrollAdjustments;
						const defaultShouldAdjust = !this.itemSizeCache.has(key) ? itemStart < scrollOffsetWithAdj : itemStart + itemSize <= scrollOffsetWithAdj && this.scrollDirection !== "backward";
						const shouldAdjustScroll = ((_b = this.scrollState) == null ? void 0 : _b.behavior) !== "smooth" && (this.shouldAdjustScrollPositionOnItemSizeChange !== void 0 ? this.shouldAdjustScrollPositionOnItemSizeChange(this.measurementsCache[index] ?? {
							index,
							key,
							start: itemStart,
							size: cachedSize,
							end: itemStart + cachedSize,
							lane: 0
						}, delta, this) : defaultShouldAdjust);
						if (this.pendingMin === null || index < this.pendingMin) this.pendingMin = index;
						this.itemSizeCache.set(key, size);
						this.itemSizeCacheVersion++;
						let adjustedSync = false;
						if (wasAtEnd) adjustedSync = this.applyScrollAdjustment(this.getTotalSize() - prevTotalSize);
						else if (shouldAdjustScroll) adjustedSync = this.applyScrollAdjustment(delta);
						this.notify(adjustedSync);
					}
				};
				this.getVirtualItems = memo$1(() => [this.getVirtualIndexes(), this.getMeasurements()], (indexes, measurements) => {
					const virtualItems = [];
					for (let k = 0, len = indexes.length; k < len; k++) {
						const measurement = measurements[indexes[k]];
						virtualItems.push(measurement);
					}
					return virtualItems;
				}, {
					key: false,
					debug: () => this.options.debug
				});
				this.getVirtualItemForOffset = (offset) => {
					const measurements = this.getMeasurements();
					if (measurements.length === 0) return;
					const flat = this._flatMeasurements;
					const useFlat = this.options.lanes === 1 && flat != null;
					return notUndefined(measurements[findNearestBinarySearch(0, measurements.length - 1, useFlat ? (i) => flat[i * 2] : (i) => notUndefined(measurements[i]).start, offset)]);
				};
				this.getMaxScrollOffset = () => {
					if (!this.scrollElement) return 0;
					if ("scrollHeight" in this.scrollElement) return this.options.horizontal ? this.scrollElement.scrollWidth - this.scrollElement.clientWidth : this.scrollElement.scrollHeight - this.scrollElement.clientHeight;
					else {
						const doc = this.scrollElement.document.documentElement;
						return this.options.horizontal ? doc.scrollWidth - this.scrollElement.innerWidth : doc.scrollHeight - this.scrollElement.innerHeight;
					}
				};
				this.getVirtualDistanceFromEnd = () => {
					return Math.max(this.getTotalSize() - this.getSize() - this.getScrollOffset(), 0);
				};
				this.getDistanceFromEnd = () => {
					return Math.max(this.getMaxScrollOffset() - this.getScrollOffset(), 0);
				};
				this.isAtEnd = (threshold = this.options.scrollEndThreshold) => {
					return this.getDistanceFromEnd() <= threshold;
				};
				this.getOffsetForAlignment = (toOffset, align, itemSize = 0) => {
					if (!this.scrollElement) return 0;
					const size = this.getSize();
					const scrollOffset = this.getScrollOffset();
					if (align === "auto") align = toOffset >= scrollOffset + size ? "end" : "start";
					if (align === "center") toOffset += (itemSize - size) / 2;
					else if (align === "end") toOffset -= size;
					const maxOffset = this.getMaxScrollOffset();
					return Math.max(Math.min(maxOffset, toOffset), 0);
				};
				this.getOffsetForIndex = (index, align = "auto") => {
					index = Math.max(0, Math.min(index, this.options.count - 1));
					const size = this.getSize();
					const scrollOffset = this.getScrollOffset();
					const item = this.measurementsCache[index];
					if (!item) return;
					if (align === "auto") if (item.end >= scrollOffset + size - this.options.scrollPaddingEnd) align = "end";
					else if (item.start <= scrollOffset + this.options.scrollPaddingStart) align = "start";
					else return [scrollOffset, align];
					if (align === "end" && index === this.options.count - 1) return [this.getMaxScrollOffset(), align];
					const toOffset = align === "end" ? item.end + this.options.scrollPaddingEnd : item.start - this.options.scrollPaddingStart;
					return [this.getOffsetForAlignment(toOffset, align, item.size), align];
				};
				this.scrollToOffset = (toOffset, { align = "start", behavior = "auto" } = {}) => {
					this._iosDeferredAdjustment = 0;
					const offset = this.getOffsetForAlignment(toOffset, align);
					const now = this.now();
					this.scrollState = {
						index: null,
						align,
						behavior,
						startedAt: now,
						lastTargetOffset: offset,
						stableFrames: 0
					};
					this._scrollToOffset(offset, {
						adjustments: void 0,
						behavior
					});
					this.scheduleScrollReconcile();
				};
				this.scrollToIndex = (index, { align: initialAlign = "auto", behavior = "auto" } = {}) => {
					this._iosDeferredAdjustment = 0;
					index = Math.max(0, Math.min(index, this.options.count - 1));
					const offsetInfo = this.getOffsetForIndex(index, initialAlign);
					if (!offsetInfo) return;
					const [offset, align] = offsetInfo;
					const now = this.now();
					this.scrollState = {
						index,
						align,
						behavior,
						startedAt: now,
						lastTargetOffset: offset,
						stableFrames: 0
					};
					this._scrollToOffset(offset, {
						adjustments: void 0,
						behavior
					});
					this.scheduleScrollReconcile();
				};
				this.scrollBy = (delta, { behavior = "auto" } = {}) => {
					const offset = this.getScrollOffset() + delta;
					const now = this.now();
					this.scrollState = {
						index: null,
						align: "start",
						behavior,
						startedAt: now,
						lastTargetOffset: offset,
						stableFrames: 0
					};
					this._scrollToOffset(offset, {
						adjustments: void 0,
						behavior
					});
					this.scheduleScrollReconcile();
				};
				this.scrollToEnd = ({ behavior = "auto" } = {}) => {
					if (this.options.count > 0) {
						this.scrollToIndex(this.options.count - 1, {
							align: "end",
							behavior
						});
						return;
					}
					this.scrollToOffset(Math.max(this.getTotalSize() - this.getSize(), 0), { behavior });
				};
				this.getTotalSize = () => {
					var _a;
					const measurements = this.getMeasurements();
					let end;
					if (measurements.length === 0) end = this.options.paddingStart;
					else if (this.options.lanes === 1) {
						const lastIdx = measurements.length - 1;
						const flat = this._flatMeasurements;
						if (flat != null) end = flat[lastIdx * 2] + flat[lastIdx * 2 + 1];
						else end = ((_a = measurements[lastIdx]) == null ? void 0 : _a.end) ?? 0;
					} else {
						const endByLane = Array(this.options.lanes).fill(null);
						let endIndex = measurements.length - 1;
						while (endIndex >= 0 && endByLane.some((val) => val === null)) {
							const item = measurements[endIndex];
							if (endByLane[item.lane] === null) endByLane[item.lane] = item.end;
							endIndex--;
						}
						end = Math.max(...endByLane.filter((val) => val !== null));
					}
					return Math.max(end - this.options.scrollMargin + this.options.paddingEnd, 0);
				};
				this.takeSnapshot = () => {
					const snapshot = [];
					if (this.itemSizeCache.size === 0) return snapshot;
					const m = this.getMeasurements();
					for (const item of m) if (item && this.itemSizeCache.has(item.key)) snapshot.push({
						index: item.index,
						key: item.key,
						start: item.start,
						size: item.size,
						end: item.end,
						lane: item.lane
					});
					return snapshot;
				};
				this._scrollToOffset = (offset, { adjustments, behavior }) => {
					this._intendedScrollOffset = offset + (adjustments ?? 0);
					this.options.scrollToFn(offset, {
						behavior,
						adjustments
					}, this);
				};
				this.measure = () => {
					this.pendingMin = null;
					this.itemSizeCache.clear();
					this.laneAssignments.clear();
					this.itemSizeCacheVersion++;
					this.notify(false);
				};
				this.setOptions(opts);
			}
			applyScrollAdjustment(delta, behavior) {
				if (delta === 0) return false;
				if (isIOSWebKit() && (this.isScrolling || this._iosTouching || this._iosJustTouchEnded)) {
					this._iosDeferredAdjustment += delta;
					return false;
				} else {
					this._scrollToOffset(this.getScrollOffset(), {
						adjustments: this.scrollAdjustments += delta,
						behavior
					});
					if (this.scrollOffset !== null) {
						this.scrollOffset += this.scrollAdjustments;
						if (this.scrollOffset < 0) this.scrollOffset = 0;
						this.scrollAdjustments = 0;
					}
					return true;
				}
			}
			scheduleScrollReconcile() {
				if (!this.targetWindow) {
					this.scrollState = null;
					return;
				}
				if (this.rafId != null) return;
				this.rafId = this.targetWindow.requestAnimationFrame(() => {
					this.rafId = null;
					this.reconcileScroll();
				});
			}
			reconcileScroll() {
				if (!this.scrollState) return;
				if (!this.scrollElement) return;
				if (this.now() - this.scrollState.startedAt > 5e3) {
					this.scrollState = null;
					return;
				}
				const offsetInfo = this.scrollState.index != null ? this.getOffsetForIndex(this.scrollState.index, this.scrollState.align) : void 0;
				const targetOffset = offsetInfo ? offsetInfo[0] : this.scrollState.lastTargetOffset;
				const STABLE_FRAMES = 1;
				const targetChanged = targetOffset !== this.scrollState.lastTargetOffset;
				if (!targetChanged && approxEqual(targetOffset, this.getScrollOffset())) {
					this.scrollState.stableFrames++;
					if (this.scrollState.stableFrames >= STABLE_FRAMES) {
						if (this.getScrollOffset() !== targetOffset) this._scrollToOffset(targetOffset, {
							adjustments: void 0,
							behavior: "auto"
						});
						this.scrollState = null;
						return;
					}
				} else {
					this.scrollState.stableFrames = 0;
					if (targetChanged) {
						const viewport = this.getSize() || 600;
						const distance = Math.abs(targetOffset - this.getScrollOffset());
						const keepSmooth = this.scrollState.behavior === "smooth" && distance > viewport;
						this.scrollState.lastTargetOffset = targetOffset;
						if (!keepSmooth) this.scrollState.behavior = "auto";
						this._scrollToOffset(targetOffset, {
							adjustments: void 0,
							behavior: keepSmooth ? "smooth" : "auto"
						});
					}
				}
				this.scheduleScrollReconcile();
			}
		};
		const findNearestBinarySearch = (low, high, getCurrentValue, value) => {
			while (low <= high) {
				const middle = (low + high) / 2 | 0;
				const currentValue = getCurrentValue(middle);
				if (currentValue < value) low = middle + 1;
				else if (currentValue > value) high = middle - 1;
				else return middle;
			}
			if (low > 0) return low - 1;
			else return 0;
		};
		function findNearestBinarySearchFlat(flat, high, value) {
			let low = 0;
			while (low <= high) {
				const middle = (low + high) / 2 | 0;
				const currentValue = flat[middle * 2];
				if (currentValue < value) low = middle + 1;
				else if (currentValue > value) high = middle - 1;
				else return middle;
			}
			return low > 0 ? low - 1 : 0;
		}
		function calculateRangeImpl(measurements, outerSize, scrollOffset, lanes, flat) {
			const lastIndex = measurements.length - 1;
			if (measurements.length <= lanes) return {
				startIndex: 0,
				endIndex: lastIndex
			};
			if (lanes === 1 && flat !== null) {
				const startIndex2 = findNearestBinarySearchFlat(flat, lastIndex, scrollOffset);
				let endIndex2 = startIndex2;
				const limit = scrollOffset + outerSize;
				while (endIndex2 < lastIndex && flat[endIndex2 * 2] + flat[endIndex2 * 2 + 1] < limit) endIndex2++;
				return {
					startIndex: startIndex2,
					endIndex: endIndex2
				};
			}
			const getStart = (index) => measurements[index].start;
			let startIndex = findNearestBinarySearch(0, lastIndex, getStart, scrollOffset);
			let endIndex = startIndex;
			if (lanes === 1) while (endIndex < lastIndex && measurements[endIndex].end < scrollOffset + outerSize) endIndex++;
			else if (lanes > 1) {
				const endPerLane = Array(lanes).fill(0);
				while (endIndex < lastIndex && endPerLane.some((pos) => pos < scrollOffset + outerSize)) {
					const item = measurements[endIndex];
					endPerLane[item.lane] = item.end;
					endIndex++;
				}
				const startPerLane = Array(lanes).fill(scrollOffset + outerSize);
				while (startIndex >= 0 && startPerLane.some((pos) => pos >= scrollOffset)) {
					const item = measurements[startIndex];
					startPerLane[item.lane] = item.start;
					startIndex--;
				}
				startIndex = Math.max(0, startIndex - startIndex % lanes);
				endIndex = Math.min(lastIndex, endIndex + (lanes - 1 - endIndex % lanes));
			}
			return {
				startIndex,
				endIndex
			};
		}
		//#endregion
		//#region ../../../node_modules/.pnpm/@tanstack+react-virtual@3.14.9_react-dom@18.3.1_react@18.3.1__react@18.3.1/node_modules/@tanstack/react-virtual/dist/esm/index.js
		const useIsomorphicLayoutEffect = typeof document !== "undefined" ? react.useLayoutEffect : react.useEffect;
		function useVirtualizerBase({ useFlushSync = true, directDomUpdates = false, directDomUpdatesMode = "transform", ...options }) {
			const rerender = react.useReducer((x) => x + 1, 0)[1];
			const directRef = react.useRef({
				enabled: directDomUpdates,
				mode: directDomUpdatesMode,
				container: null,
				lastSize: null,
				lastPositions: /* @__PURE__ */ new WeakMap(),
				prevRange: null
			});
			directRef.current.enabled = directDomUpdates;
			directRef.current.mode = directDomUpdatesMode;
			const applyContainerSize = (instance2) => {
				const state = directRef.current;
				if (!state.enabled || !state.container) return;
				const totalSize = instance2.getTotalSize();
				if (totalSize !== state.lastSize) {
					state.lastSize = totalSize;
					const sizeAxis = instance2.options.horizontal ? "width" : "height";
					state.container.style[sizeAxis] = `${totalSize}px`;
				}
			};
			const applyDirectStyles = (instance2) => {
				const state = directRef.current;
				if (!state.enabled || !state.container) return;
				applyContainerSize(instance2);
				const horizontal = !!instance2.options.horizontal;
				const useTransform = state.mode === "transform";
				const posAxis = horizontal ? "left" : "top";
				const scrollMargin = instance2.options.scrollMargin;
				const items = instance2.getVirtualItems();
				for (const item of items) {
					const next = item.start - scrollMargin;
					const el = instance2.elementsCache.get(item.key);
					if (!el) continue;
					if (state.lastPositions.get(el) === next) continue;
					state.lastPositions.set(el, next);
					if (useTransform) el.style.transform = horizontal ? `translate3d(${next}px, 0, 0)` : `translate3d(0, ${next}px, 0)`;
					else el.style[posAxis] = `${next}px`;
				}
			};
			const resolvedOptions = {
				...options,
				onChange: (instance2, sync) => {
					var _a;
					const state = directRef.current;
					let shouldRerender = true;
					if (state.enabled) {
						applyDirectStyles(instance2);
						const range = instance2.range;
						const prev = state.prevRange;
						shouldRerender = !prev || prev.isScrolling !== instance2.isScrolling || prev.startIndex !== (range == null ? void 0 : range.startIndex) || prev.endIndex !== (range == null ? void 0 : range.endIndex);
						if (shouldRerender) state.prevRange = range ? {
							startIndex: range.startIndex,
							endIndex: range.endIndex,
							isScrolling: instance2.isScrolling
						} : null;
					}
					if (shouldRerender) if (useFlushSync && sync) (0, react_dom.flushSync)(rerender);
					else rerender();
					(_a = options.onChange) == null || _a.call(options, instance2, sync);
				}
			};
			const [instance] = react.useState(() => {
				const v = new Virtualizer(resolvedOptions);
				return Object.assign(v, { containerRef: (node) => {
					const state = directRef.current;
					state.container = node;
					state.lastSize = null;
					if (node && state.enabled) {
						const total = v.getTotalSize();
						state.lastSize = total;
						const axis = v.options.horizontal ? "width" : "height";
						node.style[axis] = `${total}px`;
					}
				} });
			});
			instance.setOptions(resolvedOptions);
			useIsomorphicLayoutEffect(() => {
				return instance._didMount();
			}, []);
			useIsomorphicLayoutEffect(() => {
				applyContainerSize(instance);
				return instance._willUpdate();
			});
			useIsomorphicLayoutEffect(() => {
				applyDirectStyles(instance);
			});
			return instance;
		}
		function useVirtualizer(options) {
			return useVirtualizerBase({
				observeElementRect,
				observeElementOffset,
				scrollToFn: elementScroll,
				...options
			});
		}
		//#endregion
		//#region ../../../node_modules/.pnpm/diff@9.0.0/node_modules/diff/libesm/diff/base.js
		var Diff = class {
			diff(oldStr, newStr, options = {}) {
				let callback;
				if (typeof options === "function") {
					callback = options;
					options = {};
				} else if ("callback" in options) callback = options.callback;
				const oldString = this.castInput(oldStr, options);
				const newString = this.castInput(newStr, options);
				const oldTokens = this.removeEmpty(this.tokenize(oldString, options));
				const newTokens = this.removeEmpty(this.tokenize(newString, options));
				return this.diffWithOptionsObj(oldTokens, newTokens, options, callback);
			}
			diffWithOptionsObj(oldTokens, newTokens, options, callback) {
				var _a;
				const done = (value) => {
					value = this.postProcess(value, options);
					if (callback) {
						setTimeout(function() {
							callback(value);
						}, 0);
						return;
					} else return value;
				};
				const newLen = newTokens.length, oldLen = oldTokens.length;
				let editLength = 1;
				let maxEditLength = newLen + oldLen;
				if (options.maxEditLength != null) maxEditLength = Math.min(maxEditLength, options.maxEditLength);
				const maxExecutionTime = (_a = options.timeout) !== null && _a !== void 0 ? _a : Infinity;
				const abortAfterTimestamp = Date.now() + maxExecutionTime;
				const bestPath = [{
					oldPos: -1,
					lastComponent: void 0
				}];
				let newPos = this.extractCommon(bestPath[0], newTokens, oldTokens, 0, options);
				if (bestPath[0].oldPos + 1 >= oldLen && newPos + 1 >= newLen) return done(this.buildValues(bestPath[0].lastComponent, newTokens, oldTokens));
				let minDiagonalToConsider = -Infinity, maxDiagonalToConsider = Infinity;
				const execEditLength = () => {
					for (let diagonalPath = Math.max(minDiagonalToConsider, -editLength); diagonalPath <= Math.min(maxDiagonalToConsider, editLength); diagonalPath += 2) {
						let basePath;
						const removePath = bestPath[diagonalPath - 1], addPath = bestPath[diagonalPath + 1];
						if (removePath) bestPath[diagonalPath - 1] = void 0;
						let canAdd = false;
						if (addPath) {
							const addPathNewPos = addPath.oldPos - diagonalPath;
							canAdd = addPath && 0 <= addPathNewPos && addPathNewPos < newLen;
						}
						const canRemove = removePath && removePath.oldPos + 1 < oldLen;
						if (!canAdd && !canRemove) {
							bestPath[diagonalPath] = void 0;
							continue;
						}
						if (!canRemove || canAdd && removePath.oldPos < addPath.oldPos) basePath = this.addToPath(addPath, true, false, 0, options);
						else basePath = this.addToPath(removePath, false, true, 1, options);
						newPos = this.extractCommon(basePath, newTokens, oldTokens, diagonalPath, options);
						if (basePath.oldPos + 1 >= oldLen && newPos + 1 >= newLen) return done(this.buildValues(basePath.lastComponent, newTokens, oldTokens)) || true;
						else {
							bestPath[diagonalPath] = basePath;
							if (basePath.oldPos + 1 >= oldLen) maxDiagonalToConsider = Math.min(maxDiagonalToConsider, diagonalPath - 1);
							if (newPos + 1 >= newLen) minDiagonalToConsider = Math.max(minDiagonalToConsider, diagonalPath + 1);
						}
					}
					editLength++;
				};
				if (callback) (function exec() {
					setTimeout(function() {
						if (editLength > maxEditLength || Date.now() > abortAfterTimestamp) return callback(void 0);
						if (!execEditLength()) exec();
					}, 0);
				})();
				else while (editLength <= maxEditLength && Date.now() <= abortAfterTimestamp) {
					const ret = execEditLength();
					if (ret) return ret;
				}
			}
			addToPath(path, added, removed, oldPosInc, options) {
				const last = path.lastComponent;
				if (last && !options.oneChangePerToken && last.added === added && last.removed === removed) return {
					oldPos: path.oldPos + oldPosInc,
					lastComponent: {
						count: last.count + 1,
						added,
						removed,
						previousComponent: last.previousComponent
					}
				};
				else return {
					oldPos: path.oldPos + oldPosInc,
					lastComponent: {
						count: 1,
						added,
						removed,
						previousComponent: last
					}
				};
			}
			extractCommon(basePath, newTokens, oldTokens, diagonalPath, options) {
				const newLen = newTokens.length, oldLen = oldTokens.length;
				let oldPos = basePath.oldPos, newPos = oldPos - diagonalPath, commonCount = 0;
				while (newPos + 1 < newLen && oldPos + 1 < oldLen && this.equals(oldTokens[oldPos + 1], newTokens[newPos + 1], options)) {
					newPos++;
					oldPos++;
					commonCount++;
					if (options.oneChangePerToken) basePath.lastComponent = {
						count: 1,
						previousComponent: basePath.lastComponent,
						added: false,
						removed: false
					};
				}
				if (commonCount && !options.oneChangePerToken) basePath.lastComponent = {
					count: commonCount,
					previousComponent: basePath.lastComponent,
					added: false,
					removed: false
				};
				basePath.oldPos = oldPos;
				return newPos;
			}
			equals(left, right, options) {
				if (options.comparator) return options.comparator(left, right);
				else return left === right || !!options.ignoreCase && left.toLowerCase() === right.toLowerCase();
			}
			removeEmpty(array) {
				const ret = [];
				for (let i = 0; i < array.length; i++) if (array[i]) ret.push(array[i]);
				return ret;
			}
			castInput(value, options) {
				return value;
			}
			tokenize(value, options) {
				return Array.from(value);
			}
			join(chars) {
				return chars.join("");
			}
			postProcess(changeObjects, options) {
				return changeObjects;
			}
			get useLongestToken() {
				return false;
			}
			buildValues(lastComponent, newTokens, oldTokens) {
				const components = [];
				let nextComponent;
				while (lastComponent) {
					components.push(lastComponent);
					nextComponent = lastComponent.previousComponent;
					delete lastComponent.previousComponent;
					lastComponent = nextComponent;
				}
				components.reverse();
				const componentLen = components.length;
				let componentPos = 0, newPos = 0, oldPos = 0;
				for (; componentPos < componentLen; componentPos++) {
					const component = components[componentPos];
					if (!component.removed) {
						if (!component.added && this.useLongestToken) {
							let value = newTokens.slice(newPos, newPos + component.count);
							value = value.map(function(value, i) {
								const oldValue = oldTokens[oldPos + i];
								return oldValue.length > value.length ? oldValue : value;
							});
							component.value = this.join(value);
						} else component.value = this.join(newTokens.slice(newPos, newPos + component.count));
						newPos += component.count;
						if (!component.added) oldPos += component.count;
					} else {
						component.value = this.join(oldTokens.slice(oldPos, oldPos + component.count));
						oldPos += component.count;
					}
				}
				return components;
			}
		};
		//#endregion
		//#region ../../../node_modules/.pnpm/diff@9.0.0/node_modules/diff/libesm/diff/character.js
		var CharacterDiff = class extends Diff {};
		new CharacterDiff();
		//#endregion
		//#region ../../../node_modules/.pnpm/diff@9.0.0/node_modules/diff/libesm/util/string.js
		function longestCommonPrefix(str1, str2) {
			let i;
			for (i = 0; i < str1.length && i < str2.length; i++) if (str1[i] != str2[i]) return str1.slice(0, i);
			return str1.slice(0, i);
		}
		function longestCommonSuffix(str1, str2) {
			let i;
			if (!str1 || !str2 || str1[str1.length - 1] != str2[str2.length - 1]) return "";
			for (i = 0; i < str1.length && i < str2.length; i++) if (str1[str1.length - (i + 1)] != str2[str2.length - (i + 1)]) return str1.slice(-i);
			return str1.slice(-i);
		}
		function replacePrefix(string, oldPrefix, newPrefix) {
			if (string.slice(0, oldPrefix.length) != oldPrefix) throw Error(`string ${JSON.stringify(string)} doesn't start with prefix ${JSON.stringify(oldPrefix)}; this is a bug`);
			return newPrefix + string.slice(oldPrefix.length);
		}
		function replaceSuffix(string, oldSuffix, newSuffix) {
			if (!oldSuffix) return string + newSuffix;
			if (string.slice(-oldSuffix.length) != oldSuffix) throw Error(`string ${JSON.stringify(string)} doesn't end with suffix ${JSON.stringify(oldSuffix)}; this is a bug`);
			return string.slice(0, -oldSuffix.length) + newSuffix;
		}
		function removePrefix(string, oldPrefix) {
			return replacePrefix(string, oldPrefix, "");
		}
		function removeSuffix(string, oldSuffix) {
			return replaceSuffix(string, oldSuffix, "");
		}
		function maximumOverlap(string1, string2) {
			return string2.slice(0, overlapCount(string1, string2));
		}
		function overlapCount(a, b) {
			let startA = 0;
			if (a.length > b.length) startA = a.length - b.length;
			let endB = b.length;
			if (a.length < b.length) endB = a.length;
			const map = Array(endB);
			let k = 0;
			map[0] = 0;
			for (let j = 1; j < endB; j++) {
				if (b[j] == b[k]) map[j] = map[k];
				else map[j] = k;
				while (k > 0 && b[j] != b[k]) k = map[k];
				if (b[j] == b[k]) k++;
			}
			k = 0;
			for (let i = startA; i < a.length; i++) {
				while (k > 0 && a[i] != b[k]) k = map[k];
				if (a[i] == b[k]) k++;
			}
			return k;
		}
		/**
		* Split a string into segments using a word segmenter, merging consecutive
		* segments if they are both whitespace segments. Whitespace segments can
		* appear adjacent to one another for two reasons:
		* - newlines always get their own segment
		* - where a diacritic is attached to a whitespace character in the text, the
		*   segment ends after the diacritic, so e.g. " \u0300 " becomes two segments.
		* This function therefore runs the segmenter's .segment() method and then
		* merges consecutive segments of whitespace into a single part.
		*/
		function segment(string, segmenter) {
			const parts = [];
			for (const segmentObj of Array.from(segmenter.segment(string))) {
				const segment = segmentObj.segment;
				if (parts.length && /\s/.test(parts[parts.length - 1]) && /\s/.test(segment)) parts[parts.length - 1] += segment;
				else parts.push(segment);
			}
			return parts;
		}
		function trailingWs(string, segmenter) {
			if (segmenter) return leadingAndTrailingWs(string, segmenter)[1];
			let i;
			for (i = string.length - 1; i >= 0; i--) if (!string[i].match(/\s/)) break;
			return string.substring(i + 1);
		}
		function leadingWs(string, segmenter) {
			if (segmenter) return leadingAndTrailingWs(string, segmenter)[0];
			const match = string.match(/^\s*/);
			return match ? match[0] : "";
		}
		function leadingAndTrailingWs(string, segmenter) {
			if (!segmenter) return [leadingWs(string), trailingWs(string)];
			if (segmenter.resolvedOptions().granularity != "word") throw new Error("The segmenter passed must have a granularity of \"word\"");
			const segments = segment(string, segmenter);
			const firstSeg = segments[0];
			const lastSeg = segments[segments.length - 1];
			return [/\s/.test(firstSeg) ? firstSeg : "", /\s/.test(lastSeg) ? lastSeg : ""];
		}
		//#endregion
		//#region ../../../node_modules/.pnpm/diff@9.0.0/node_modules/diff/libesm/diff/word.js
		const extendedWordChars = "a-zA-Z0-9_\\u{AD}\\u{C0}-\\u{D6}\\u{D8}-\\u{F6}\\u{F8}-\\u{2C6}\\u{2C8}-\\u{2D7}\\u{2DE}-\\u{2FF}\\u{1E00}-\\u{1EFF}";
		const tokenizeIncludingWhitespace = new RegExp(`[${extendedWordChars}]+|\\s+|[^${extendedWordChars}]`, "ug");
		var WordDiff = class extends Diff {
			equals(left, right, options) {
				if (options.ignoreCase) {
					left = left.toLowerCase();
					right = right.toLowerCase();
				}
				return left.trim() === right.trim();
			}
			tokenize(value, options = {}) {
				let parts;
				if (options.intlSegmenter) {
					const segmenter = options.intlSegmenter;
					if (segmenter.resolvedOptions().granularity != "word") throw new Error("The segmenter passed must have a granularity of \"word\"");
					parts = segment(value, segmenter);
				} else parts = value.match(tokenizeIncludingWhitespace) || [];
				const tokens = [];
				let prevPart = null;
				parts.forEach((part) => {
					if (/\s/.test(part)) if (prevPart == null) tokens.push(part);
					else tokens.push(tokens.pop() + part);
					else if (prevPart != null && /\s/.test(prevPart)) if (tokens[tokens.length - 1] == prevPart) tokens.push(tokens.pop() + part);
					else tokens.push(prevPart + part);
					else tokens.push(part);
					prevPart = part;
				});
				return tokens;
			}
			join(tokens) {
				return tokens.map((token, i) => {
					if (i == 0) return token;
					else return token.replace(/^\s+/, "");
				}).join("");
			}
			postProcess(changes, options) {
				if (!changes || options.oneChangePerToken) return changes;
				let lastKeep = null;
				let insertion = null;
				let deletion = null;
				changes.forEach((change) => {
					if (change.added) insertion = change;
					else if (change.removed) deletion = change;
					else {
						if (insertion || deletion) dedupeWhitespaceInChangeObjects(lastKeep, deletion, insertion, change, options.intlSegmenter);
						lastKeep = change;
						insertion = null;
						deletion = null;
					}
				});
				if (insertion || deletion) dedupeWhitespaceInChangeObjects(lastKeep, deletion, insertion, null, options.intlSegmenter);
				return changes;
			}
		};
		new WordDiff();
		function dedupeWhitespaceInChangeObjects(startKeep, deletion, insertion, endKeep, segmenter) {
			if (deletion && insertion) {
				const [oldWsPrefix, oldWsSuffix] = leadingAndTrailingWs(deletion.value, segmenter);
				const [newWsPrefix, newWsSuffix] = leadingAndTrailingWs(insertion.value, segmenter);
				if (startKeep) {
					const commonWsPrefix = longestCommonPrefix(oldWsPrefix, newWsPrefix);
					startKeep.value = replaceSuffix(startKeep.value, newWsPrefix, commonWsPrefix);
					deletion.value = removePrefix(deletion.value, commonWsPrefix);
					insertion.value = removePrefix(insertion.value, commonWsPrefix);
				}
				if (endKeep) {
					const commonWsSuffix = longestCommonSuffix(oldWsSuffix, newWsSuffix);
					endKeep.value = replacePrefix(endKeep.value, newWsSuffix, commonWsSuffix);
					deletion.value = removeSuffix(deletion.value, commonWsSuffix);
					insertion.value = removeSuffix(insertion.value, commonWsSuffix);
				}
			} else if (insertion) {
				if (startKeep) {
					const ws = leadingWs(insertion.value, segmenter);
					insertion.value = insertion.value.substring(ws.length);
				}
				if (endKeep) {
					const ws = leadingWs(endKeep.value, segmenter);
					endKeep.value = endKeep.value.substring(ws.length);
				}
			} else if (startKeep && endKeep) {
				const newWsFull = leadingWs(endKeep.value, segmenter), [delWsStart, delWsEnd] = leadingAndTrailingWs(deletion.value, segmenter);
				const newWsStart = longestCommonPrefix(newWsFull, delWsStart);
				deletion.value = removePrefix(deletion.value, newWsStart);
				const newWsEnd = longestCommonSuffix(removePrefix(newWsFull, newWsStart), delWsEnd);
				deletion.value = removeSuffix(deletion.value, newWsEnd);
				endKeep.value = replacePrefix(endKeep.value, newWsFull, newWsEnd);
				startKeep.value = replaceSuffix(startKeep.value, newWsFull, newWsFull.slice(0, newWsFull.length - newWsEnd.length));
			} else if (endKeep) {
				const endKeepWsPrefix = leadingWs(endKeep.value, segmenter);
				const overlap = maximumOverlap(trailingWs(deletion.value, segmenter), endKeepWsPrefix);
				deletion.value = removeSuffix(deletion.value, overlap);
			} else if (startKeep) {
				const overlap = maximumOverlap(trailingWs(startKeep.value, segmenter), leadingWs(deletion.value, segmenter));
				deletion.value = removePrefix(deletion.value, overlap);
			}
		}
		var WordsWithSpaceDiff = class extends Diff {
			tokenize(value) {
				const regex = new RegExp(`(\\r?\\n)|[${extendedWordChars}]+|[^\\S\\n\\r]+|[^${extendedWordChars}]`, "ug");
				return value.match(regex) || [];
			}
		};
		new WordsWithSpaceDiff();
		//#endregion
		//#region ../../../node_modules/.pnpm/diff@9.0.0/node_modules/diff/libesm/diff/line.js
		var LineDiff = class extends Diff {
			constructor() {
				super(...arguments);
				this.tokenize = tokenize;
			}
			equals(left, right, options) {
				if (options.ignoreWhitespace) {
					if (!options.newlineIsToken || !left.includes("\n")) left = left.trim();
					if (!options.newlineIsToken || !right.includes("\n")) right = right.trim();
				} else if (options.ignoreNewlineAtEof && !options.newlineIsToken) {
					if (left.endsWith("\n")) left = left.slice(0, -1);
					if (right.endsWith("\n")) right = right.slice(0, -1);
				}
				return super.equals(left, right, options);
			}
		};
		const lineDiff = new LineDiff();
		function diffLines(oldStr, newStr, options) {
			return lineDiff.diff(oldStr, newStr, options);
		}
		function tokenize(value, options) {
			if (options.stripTrailingCr) value = value.replace(/\r\n/g, "\n");
			const retLines = [], linesAndNewlines = value.split(/(\n|\r\n)/);
			if (!linesAndNewlines[linesAndNewlines.length - 1]) linesAndNewlines.pop();
			for (let i = 0; i < linesAndNewlines.length; i++) {
				const line = linesAndNewlines[i];
				if (i % 2 && !options.newlineIsToken) retLines[retLines.length - 1] += line;
				else retLines.push(line);
			}
			return retLines;
		}
		//#endregion
		//#region ../../../node_modules/.pnpm/diff@9.0.0/node_modules/diff/libesm/diff/sentence.js
		function isSentenceEndPunct(char) {
			return char == "." || char == "!" || char == "?";
		}
		var SentenceDiff = class extends Diff {
			tokenize(value) {
				var _a;
				const result = [];
				let tokenStartI = 0;
				for (let i = 0; i < value.length; i++) {
					if (i == value.length - 1) {
						result.push(value.slice(tokenStartI));
						break;
					}
					if (isSentenceEndPunct(value[i]) && value[i + 1].match(/\s/)) {
						result.push(value.slice(tokenStartI, i + 1));
						i = tokenStartI = i + 1;
						while ((_a = value[i + 1]) === null || _a === void 0 ? void 0 : _a.match(/\s/)) i++;
						result.push(value.slice(tokenStartI, i + 1));
						tokenStartI = i + 1;
					}
				}
				return result;
			}
		};
		new SentenceDiff();
		//#endregion
		//#region ../../../node_modules/.pnpm/diff@9.0.0/node_modules/diff/libesm/diff/css.js
		var CssDiff = class extends Diff {
			tokenize(value) {
				return value.split(/([{}:;,]|\s+)/);
			}
		};
		new CssDiff();
		//#endregion
		//#region ../../../node_modules/.pnpm/diff@9.0.0/node_modules/diff/libesm/diff/json.js
		var JsonDiff = class extends Diff {
			constructor() {
				super(...arguments);
				this.tokenize = tokenize;
			}
			get useLongestToken() {
				return true;
			}
			castInput(value, options) {
				const { undefinedReplacement, stringifyReplacer = (k, v) => typeof v === "undefined" ? undefinedReplacement : v } = options;
				return typeof value === "string" ? value : JSON.stringify(canonicalize(value, null, null, stringifyReplacer), null, "  ");
			}
			equals(left, right, options) {
				return super.equals(left.replace(/,([\r\n])/g, "$1"), right.replace(/,([\r\n])/g, "$1"), options);
			}
		};
		new JsonDiff();
		function canonicalize(obj, stack, replacementStack, replacer, key) {
			stack = stack || [];
			replacementStack = replacementStack || [];
			if (replacer) obj = replacer(key === void 0 ? "" : key, obj);
			let i;
			for (i = 0; i < stack.length; i += 1) if (stack[i] === obj) return replacementStack[i];
			let canonicalizedObj;
			if ("[object Array]" === Object.prototype.toString.call(obj)) {
				stack.push(obj);
				canonicalizedObj = new Array(obj.length);
				replacementStack.push(canonicalizedObj);
				for (i = 0; i < obj.length; i += 1) canonicalizedObj[i] = canonicalize(obj[i], stack, replacementStack, replacer, String(i));
				stack.pop();
				replacementStack.pop();
				return canonicalizedObj;
			}
			if (obj && obj.toJSON) obj = obj.toJSON();
			if (typeof obj === "object" && obj !== null) {
				stack.push(obj);
				canonicalizedObj = {};
				replacementStack.push(canonicalizedObj);
				const sortedKeys = [];
				let key;
				for (key in obj)
 /* istanbul ignore else */
				if (Object.prototype.hasOwnProperty.call(obj, key)) sortedKeys.push(key);
				sortedKeys.sort();
				for (i = 0; i < sortedKeys.length; i += 1) {
					key = sortedKeys[i];
					canonicalizedObj[key] = canonicalize(obj[key], stack, replacementStack, replacer, key);
				}
				stack.pop();
				replacementStack.pop();
			} else canonicalizedObj = obj;
			return canonicalizedObj;
		}
		//#endregion
		//#region ../../../node_modules/.pnpm/diff@9.0.0/node_modules/diff/libesm/diff/array.js
		var ArrayDiff = class extends Diff {
			tokenize(value) {
				return value.slice();
			}
			join(value) {
				return value;
			}
			removeEmpty(value) {
				return value;
			}
		};
		new ArrayDiff();
		//#endregion
		//#region ../../../node_modules/.pnpm/diff@9.0.0/node_modules/diff/libesm/patch/create.js
		function structuredPatch(oldFileName, newFileName, oldStr, newStr, oldHeader, newHeader, options) {
			let optionsObj;
			if (!options) optionsObj = {};
			else if (typeof options === "function") optionsObj = { callback: options };
			else optionsObj = options;
			if (typeof optionsObj.context === "undefined") optionsObj.context = 4;
			const context = optionsObj.context;
			if (optionsObj.newlineIsToken) throw new Error("newlineIsToken may not be used with patch-generation functions, only with diffing functions");
			if (!optionsObj.callback) return diffLinesResultToPatch(diffLines(oldStr, newStr, optionsObj));
			else {
				const { callback } = optionsObj;
				diffLines(oldStr, newStr, Object.assign(Object.assign({}, optionsObj), { callback: (diff) => {
					callback(diffLinesResultToPatch(diff));
				} }));
			}
			function diffLinesResultToPatch(diff) {
				if (!diff) return;
				diff.push({
					value: "",
					lines: []
				});
				function contextLines(lines) {
					return lines.map(function(entry) {
						return " " + entry;
					});
				}
				const hunks = [];
				let oldRangeStart = 0, newRangeStart = 0, curRange = [], oldLine = 1, newLine = 1;
				for (let i = 0; i < diff.length; i++) {
					const current = diff[i], lines = current.lines || splitLines(current.value);
					current.lines = lines;
					if (current.added || current.removed) {
						if (!oldRangeStart) {
							const prev = diff[i - 1];
							oldRangeStart = oldLine;
							newRangeStart = newLine;
							if (prev) {
								curRange = context > 0 ? contextLines(prev.lines.slice(-context)) : [];
								oldRangeStart -= curRange.length;
								newRangeStart -= curRange.length;
							}
						}
						for (const line of lines) curRange.push((current.added ? "+" : "-") + line);
						if (current.added) newLine += lines.length;
						else oldLine += lines.length;
					} else {
						if (oldRangeStart) if (lines.length <= context * 2 && i < diff.length - 2) for (const line of contextLines(lines)) curRange.push(line);
						else {
							const contextSize = Math.min(lines.length, context);
							for (const line of contextLines(lines.slice(0, contextSize))) curRange.push(line);
							const hunk = {
								oldStart: oldRangeStart,
								oldLines: oldLine - oldRangeStart + contextSize,
								newStart: newRangeStart,
								newLines: newLine - newRangeStart + contextSize,
								lines: curRange
							};
							hunks.push(hunk);
							oldRangeStart = 0;
							newRangeStart = 0;
							curRange = [];
						}
						oldLine += lines.length;
						newLine += lines.length;
					}
				}
				for (const hunk of hunks) for (let i = 0; i < hunk.lines.length; i++) if (hunk.lines[i].endsWith("\n")) hunk.lines[i] = hunk.lines[i].slice(0, -1);
				else {
					hunk.lines.splice(i + 1, 0, "\\ No newline at end of file");
					i++;
				}
				return {
					oldFileName,
					newFileName,
					oldHeader,
					newHeader,
					hunks
				};
			}
		}
		/**
		* Split `text` into an array of lines, including the trailing newline character (where present)
		*/
		function splitLines(text) {
			const hasTrailingNl = text.endsWith("\n");
			const result = text.split("\n").map((line) => line + "\n");
			if (hasTrailingNl) result.pop();
			else result.push(result.pop().slice(0, -1));
			return result;
		}
		//#endregion
		//#region src/client/trajectory-record.ts
		/**
		* Resolve the identity that survives prepending older projected records.
		* @param cell - Projected trajectory record.
		* @returns Stable identity from the owning event or tool call, with a fixture fallback.
		*/
		function trajectoryRecordId(cell) {
			if (cell.recordId !== void 0) return cell.recordId;
			if (cell.callId !== void 0) return `${cell.kind}\u0000call\u0000${cell.callId}`;
			if (cell.sourceSeq !== void 0) return `${cell.kind}\u0000seq\u0000${cell.sourceSeq}`;
			return `${cell.kind}\u0000index\u0000${cell.index}`;
		}
		/**
		* Format a duration in milliseconds with thousands separators.
		* @param milliseconds - Duration in milliseconds, or `null` when absent.
		* @param t - Trajectory locale translator.
		* @returns `—` when unknown, otherwise an integer-millisecond label.
		*/
		function formatDurationMillis(milliseconds, t) {
			if (milliseconds === null || !Number.isFinite(milliseconds)) return "—";
			return t("unit.milliseconds", { value: String(Math.round(milliseconds)).replace(/\B(?=(\d{3})+(?!\d))/g, ",") });
		}
		/**
		* Format an elapsed duration given in seconds as a millisecond label.
		* @param seconds - Duration seconds, or `null` when absent.
		* @param t - Trajectory locale translator.
		* @returns `—` when unknown, otherwise an integer-millisecond label.
		*/
		function formatElapsedSeconds(seconds, t) {
			return formatDurationMillis(seconds === null ? null : seconds * 1e3, t);
		}
		//#endregion
		//#region src/client/trajectory-virtual-rows.ts
		const CONTENT_ROW_HEIGHT = 30;
		const COLLAPSED_SUMMARY_HEIGHT = 20;
		const TERMINAL_BOUNDARY_HEIGHT = 9;
		/**
		* Derive the DOM-safe row identity shared by React, the virtualizer, and
		* browser scroll contracts.
		* @param record - Display record whose identity is required.
		* @returns Stable record identity with a suffix for synthetic fold summaries.
		*/
		function trajectoryVirtualRecordKey(record) {
			const identity = encodeURIComponent(trajectoryRecordId(record.cell));
			return record.collapsedSummaryKind === void 0 ? identity : `${identity}\u0000summary\u0000${record.collapsedSummaryKind}`;
		}
		/**
		* Attach separator-only records to the next content row so the virtualizer
		* never owns a zero-height item. A terminal separator retains its CSS-owned
		* lower-marker clearance as a standalone item.
		* @param records - Final search/fold projection in ledger order.
		* @returns Measurable virtual rows with original logical positions retained.
		*/
		function groupTrajectoryVirtualRows(records) {
			const rows = [];
			let pending = [];
			for (const [logicalIndex, record] of records.entries()) {
				const entry = {
					logicalIndex,
					record
				};
				if (record.cell.requestOnly === true) {
					pending.push(entry);
					continue;
				}
				const entries = [...pending, entry];
				pending = [];
				rows.push({
					entries,
					height: record.collapsedSummaryKind === void 0 ? CONTENT_ROW_HEIGHT : COLLAPSED_SUMMARY_HEIGHT,
					key: trajectoryVirtualRecordKey(record)
				});
			}
			if (pending.length > 0) rows.push({
				entries: pending,
				height: TERMINAL_BOUNDARY_HEIGHT,
				key: pending.map((candidate) => trajectoryVirtualRecordKey(candidate.record)).join("|")
			});
			return rows;
		}
		//#endregion
		//#region src/client/trajectory-preview.ts
		/** Bounded Markdown-to-text projection shared by trajectory consumers. */
		const PREVIEW_SOURCE_CHARACTERS = 2048;
		const PREVIEW_OUTPUT_CHARACTERS = 512;
		/**
		* Build a bounded one-line preview without parsing the complete Markdown document.
		* @param text - Untrusted message, reasoning, payload, or result text.
		* @returns A compact preview capped independently from the retained source.
		*/
		function trajectoryPreviewText(text) {
			const source = text.slice(0, PREVIEW_SOURCE_CHARACTERS);
			const compact = (0, _deepseek_ai_dsh_client_ui_primitives.extractMarkdownPlainText)(source).replace(/\s+/g, " ").trim();
			const preview = compact.slice(0, PREVIEW_OUTPUT_CHARACTERS).trimEnd();
			return source.length < text.length || preview.length < compact.length ? `${preview}…` : preview;
		}
		//#endregion
		//#region src/client/code-program.ts
		/** Recorded name of the programmatic tool-calling entry point. */
		const PTC_TOOL_NAME = "run_code";
		function record(value) {
			return typeof value === "object" && value !== null && !Array.isArray(value) ? value : void 0;
		}
		function parseRecord(value) {
			if (value === void 0) return void 0;
			try {
				return record(JSON.parse(value));
			} catch {
				return;
			}
		}
		function recordedLanguage(schemaRaw) {
			const description = record(record(record(parseRecord(schemaRaw)?.parameters)?.properties)?.code)?.description;
			if (typeof description !== "string") return void 0;
			const typescript = /\bTypeScript\b/i.test(description);
			if (typescript === /\bPython\b/i.test(description)) return void 0;
			return typescript ? "typescript" : "python";
		}
		/**
		* Resolve a PTC program without guessing its language from source or current runtime settings.
		* @param cell - Recorded tool arguments and the schema visible at call time.
		* @returns The program, or undefined for another tool or unsupported arguments.
		*/
		function codeProgram(cell) {
			if (cell.kind !== "tool" && cell.kind !== "subtool") return void 0;
			if (cell.toolName !== "run_code" || cell.inputDetail === void 0) return void 0;
			const args = parseRecord(cell.inputDetail);
			if (typeof args?.code !== "string") return void 0;
			if (args.description !== void 0 && typeof args.description !== "string") return void 0;
			return {
				rawInput: cell.inputDetail,
				source: args.code,
				description: typeof args.description === "string" && args.description.trim() !== "" ? args.description : args.code.split(/\r?\n/).find((line) => line.trim() !== "")?.trim() ?? "",
				arguments: args,
				language: recordedLanguage(cell.schemaDetail)
			};
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-trajectory/src/client/TrajectoryTable.module.css.mjs
		const css$3 = ".yymjNa_split{--dsh-scrollbar-thumb:var(--dsw-alias-scrollbar-bg-l2);--dsh-scrollbar-thumb-hover:var(--dsw-alias-scrollbar-hover-l2);background:var(--dsw-alias-bg-layer-1);flex:1;width:100%;min-height:0;display:flex;position:relative;overflow:hidden;container-type:inline-size}.yymjNa_tablePane{min-width:0;padding-bottom:var(--dsh-trajectory-bottom-clearance,0px);flex:1;position:relative;overflow:hidden auto;container:yymjNa_trajectory-table/inline-size}.yymjNa_historyLoading{z-index:5;pointer-events:none;height:0;position:sticky;top:0;overflow:visible}.yymjNa_historyLoadingBar{box-sizing:border-box;border-bottom:.5px solid var(--dsw-alias-border-l2);background:var(--dsw-alias-bg-layer-1);width:100%;height:30px;color:var(--dsw-alias-label-secondary);font:var(--dsw-font-xxs-12);justify-content:center;align-items:center;gap:6px;display:flex}.yymjNa_table tbody .yymjNa_historyLoadRow td{height:30px;padding:0}.yymjNa_table tbody .yymjNa_historyLoadRow+tr[data-turn-start=true] td:before{content:none}.yymjNa_historyLoadButton{background:var(--dsw-alias-bg-layer-1);width:100%;height:29px;color:var(--dsw-alias-label-secondary);cursor:pointer;font:var(--dsw-font-xxs-12);border:0;justify-content:center;align-items:center;gap:6px;display:flex}.yymjNa_historyLoadButton:hover:not(:disabled){background:var(--dsw-alias-interactive-bg-hover);color:var(--dsw-alias-label-primary)}.yymjNa_historyLoadButton:focus-visible{outline:var(--dsw-focus-ring-width) solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:-2px}.yymjNa_historyLoadButton:disabled{cursor:default}.yymjNa_visuallyHidden{clip:rect(0 0 0 0);white-space:nowrap;width:1px;height:1px;position:absolute;overflow:hidden}.yymjNa_table:not([data-scroll-ready=true]){visibility:hidden}.yymjNa_table{--trajectory-turn-accent:color-mix(in srgb, var(--dsw-static-blue-500) 22%, var(--dsw-alias-bg-layer-1));border-spacing:0;table-layout:fixed;width:100%;min-width:0;color:var(--dsw-alias-label-primary);background:var(--dsw-alias-bg-layer-1);font:var(--dsw-font-xxs-12)}.yymjNa_eventColumn{width:122px}.yymjNa_eventColumn:where(:lang(zh)){width:84px}.yymjNa_contentColumn{width:auto}.yymjNa_table th{z-index:3;box-sizing:border-box;border-bottom:.5px solid var(--dsw-alias-border-l2);height:30px;color:var(--dsw-alias-label-tertiary);background:var(--dsw-specific-sidebar-fill);font:var(--dsw-font-xxs-12);text-align:left;text-overflow:ellipsis;user-select:none;white-space:nowrap;padding:0 8px;font-weight:500;position:sticky;top:0;overflow:hidden}.yymjNa_eventHeader{text-align:right!important;padding-right:4px!important}.yymjNa_table td{box-sizing:border-box;border-bottom:.5px solid var(--dsw-alias-border-l1);text-overflow:ellipsis;white-space:nowrap;height:30px;padding:0 8px;overflow:hidden}.yymjNa_table tbody .yymjNa_virtualSpacer{pointer-events:none}.yymjNa_table tbody .yymjNa_virtualSpacer td{height:var(--trajectory-virtual-spacer-height);border:0;padding:0}.yymjNa_table tbody tr:not([data-collapsed-summary]):not([data-virtual-spacer]):not([data-history-load]){cursor:default;transition:background-color .12s var(--ds-ease-in-out), opacity .12s var(--ds-ease-in-out);outline:none}.yymjNa_table tbody tr[data-timeline-focus=outside]{opacity:.24}.yymjNa_table tbody tr:not([data-collapsed-summary]):not([data-virtual-spacer]):not([data-history-load]):not([data-selected=true]):hover{background:var(--dsw-alias-interactive-bg-hover)}.yymjNa_table tbody tr[data-request-only=true]:hover{background:0 0}.yymjNa_table tbody tr[data-request-only=true] td{border-bottom:0;height:0;padding-top:0;padding-bottom:0}.yymjNa_table tbody tr[data-terminal-request-boundary=true] td{height:9px}.yymjNa_table tbody tr[data-request-only=true] .yymjNa_turnRail{top:-15px;bottom:0}.yymjNa_table tbody tr:not([data-collapsed-summary]):focus-visible{box-shadow:inset 0 0 0 1px var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary))}.yymjNa_table tbody tr[data-selected=true]{background:var(--dsw-alias-interactive-bg-active)}.yymjNa_requestBoundaryControl{--request-boundary-base-left:12px;z-index:6;top:-8px;left:calc(var(--request-boundary-base-left) + var(--request-boundary-offset,0px));cursor:pointer;background:0 0;border:0;width:16px;height:16px;padding:0;position:absolute}.yymjNa_requestBoundaryControl:before{corner-shape:round;background:var(--dsw-alias-label-caption);width:5px;height:5px;box-shadow:0 0 0 2px var(--dsw-alias-bg-layer-1), 0 0 0 3px transparent;content:\"\";transition:background .12s var(--ds-ease-in-out), box-shadow .12s var(--ds-ease-in-out);border-radius:50%;position:absolute;top:5.5px;left:5.5px}.yymjNa_requestBoundaryControl:after{border:.5px solid var(--dsw-alias-border-l1);width:max-content;color:var(--dsw-alias-label-secondary);background:var(--dsw-alias-bg-layer-1);content:attr(data-label);font:9px/12px var(--ds-font-family-code);opacity:0;pointer-events:none;transition:opacity .12s var(--ds-ease-in-out), transform .12s var(--ds-ease-in-out);user-select:none;white-space:nowrap;border-radius:2px;padding:0 4px;position:absolute;top:2px;left:17px;transform:translate(-2px);box-shadow:0 2px 6px #0000001f}.yymjNa_requestBoundaryControl:hover:before,.yymjNa_requestBoundaryControl:focus-visible:before{background:var(--dsw-alias-brand-primary-new-colorprimary-new-color)}.yymjNa_requestBoundaryControlActive:before,.yymjNa_requestBoundaryControlActive:hover:before,.yymjNa_requestBoundaryControlActive:focus-visible:before{background:color-mix(in srgb, var(--dsw-alias-brand-primary-new-colorprimary-new-color) 18%, var(--dsw-alias-bg-layer-1));box-shadow:0 0 0 1.5px var(--dsw-alias-state-business-primary)}.yymjNa_requestBoundaryControl[data-request-status=error]:before,.yymjNa_requestBoundaryControl[data-request-status=error]:hover:before,.yymjNa_requestBoundaryControl[data-request-status=error]:focus-visible:before{background:var(--dsw-alias-state-error-primary)}.yymjNa_requestBoundaryControl:hover:after,.yymjNa_requestBoundaryControl:focus-visible:after{opacity:1;transform:translate(0)}.yymjNa_requestBoundaryControl:focus-visible{outline:none}.yymjNa_table tbody tr:has(.yymjNa_requestBoundaryControl:hover):not([data-selected=true]){background:0 0}.yymjNa_event{position:relative}.yymjNa_turnRail,.yymjNa_selectionRail{background:var(--dsw-alias-brand-primary-new-colorprimary-new-color);pointer-events:none;position:absolute;left:0}.yymjNa_turnRail{z-index:4;background:var(--trajectory-turn-accent);width:2px;top:-1px;bottom:-1px}.yymjNa_table tbody tr[data-turn-end=true] .yymjNa_turnRail{bottom:0}.yymjNa_selectionRail{z-index:5;width:3px;top:0;bottom:0}.yymjNa_table tbody tr[data-error=true] .yymjNa_turnRail{background:color-mix(in srgb, var(--dsw-alias-state-error-primary) 22%, var(--dsw-alias-bg-layer-1))}.yymjNa_table tbody tr[data-error=true] .yymjNa_selectionRail{background:var(--dsw-alias-state-error-primary)}.yymjNa_table tbody tr[data-turn-start=true] td{position:relative;overflow:visible}.yymjNa_table tbody tr[data-turn-start=true]:not(:first-child) td:before{z-index:1;background:var(--dsw-alias-border-l1);content:\"\";pointer-events:none;height:2px;position:absolute;top:0;left:0;right:0;transform:translateY(-50%)}.yymjNa_event{padding-left:36px!important;padding-right:4px!important;overflow:visible!important}.yymjNa_turnLabel{z-index:3;box-sizing:border-box;width:max-content;color:var(--dsw-alias-label-tertiary);background:var(--dsw-alias-bg-module-platform);font:8px/10px var(--ds-font-family-code);font-variant-numeric:tabular-nums;user-select:none;white-space:nowrap;border-radius:0 0 2px;flex:none;align-items:center;padding:1px 5px;display:inline-grid;position:absolute;top:0;left:0}.yymjNa_turnLabelFull,.yymjNa_turnLabelCompact{opacity:1;white-space:nowrap;grid-area:1/1;max-width:64px;overflow:hidden}.yymjNa_turnLabelCompact{opacity:0;max-width:0}.yymjNa_turnLabelActive{color:color-mix(in srgb, var(--dsw-static-blue-500) 55%, var(--dsw-alias-label-tertiary));background:var(--trajectory-turn-accent)}.yymjNa_eventInner{justify-content:flex-start;align-items:center;min-width:0;height:100%;display:flex}.yymjNa_kindSlot{flex:none;justify-content:flex-end;align-items:flex-end;width:76px;display:flex}.yymjNa_kindSlot:where(:lang(zh)){width:44px}.yymjNa_kindSlot [role=tooltip]{background:var(--dsw-alias-bg-layer-2);box-shadow:var(--dsw-elevation-panel);color:var(--dsw-alias-label-primary);border:0}.yymjNa_content{color:var(--dsw-alias-label-primary);padding-left:4px!important}.yymjNa_kindTag{box-sizing:border-box;border-radius:var(--dsw-radius-xs);letter-spacing:.035em;user-select:none;border:1px solid #0000;flex:none;align-items:center;height:19px;padding:0 5px;font-size:10px;font-weight:650;line-height:16px;display:inline-flex}.yymjNa_kindTagIcon{opacity:0;flex:none;justify-content:center;align-items:center;width:0;height:13px;display:inline-flex;overflow:hidden;transform:scale(.8)}.yymjNa_kindTagLabel{opacity:1;white-space:nowrap;max-width:72px;display:inline-block;overflow:hidden}.yymjNa_table .yymjNa_kindSlot .yymjNa_message{justify-content:center;width:100%}@container yymjNa_trajectory-table (width<=620px){.yymjNa_eventColumn{width:50px}.yymjNa_event{padding-left:28px!important;padding-right:3px!important}.yymjNa_requestBoundaryControl{--request-boundary-base-left:6px}.yymjNa_kindSlot{width:19px}.yymjNa_kindTag,.yymjNa_table .yymjNa_kindSlot .yymjNa_message{justify-content:center;width:19px;padding-left:0;padding-right:0}.yymjNa_kindTagIcon{opacity:1;width:13px;transform:scale(1)}.yymjNa_kindTagLabel,.yymjNa_turnLabelFull{opacity:0;max-width:0}.yymjNa_turnLabelCompact{opacity:1;max-width:64px}}@media (prefers-reduced-motion:no-preference){.yymjNa_eventColumn,.yymjNa_event,.yymjNa_requestBoundaryControl,.yymjNa_kindSlot,.yymjNa_kindTag,.yymjNa_kindTagIcon,.yymjNa_kindTagLabel,.yymjNa_turnLabelFull,.yymjNa_turnLabelCompact{transition-duration:.18s;transition-timing-function:var(--ds-ease-in-out)}.yymjNa_eventColumn,.yymjNa_kindSlot{transition-property:width}.yymjNa_event{transition-property:padding-right,padding-left}.yymjNa_requestBoundaryControl{transition-property:left}.yymjNa_kindTag{transition-property:padding-right,padding-left}.yymjNa_kindTagIcon{transition-property:width,opacity,transform}.yymjNa_kindTagLabel,.yymjNa_turnLabelFull,.yymjNa_turnLabelCompact{transition-property:max-width,opacity}}.yymjNa_user{color:var(--dsw-alias-state-business-primary);background:var(--dsw-alias-state-business-tertiary)}.yymjNa_systemNeutral{color:var(--dsw-alias-label-secondary);background:var(--dsw-alias-bg-module-platform)}.yymjNa_contextGreen{color:color-mix(in srgb, var(--dsw-alias-state-success-primary) 68%, var(--dsw-alias-label-secondary));background:var(--dsw-alias-state-success-tertiary)}.yymjNa_compacted{color:var(--dsw-alias-label-secondary);background:var(--dsw-alias-bg-module-platform)}.yymjNa_compactedSummary{font:var(--dsw-font-xs-13);margin-top:12px;padding:0 0 14px}.yymjNa_promptDiffSections{flex-direction:column;gap:14px;max-height:100%;padding:10px 14px 14px;display:flex;overflow:auto}.yymjNa_promptDiffSection{min-width:0}.yymjNa_promptDiffTitle{color:var(--dsw-alias-label-secondary);font:var(--dsw-font-xs-strong-13);user-select:none;margin:0 0 6px}.yymjNa_promptDiff{color:var(--dsw-alias-label-primary);background:var(--dsw-alias-bg-layer-1);font:11px/17px var(--ds-font-family-code);white-space:pre;margin:0;overflow:auto}.yymjNa_promptDiff span{min-width:max-content;padding:0 6px;display:block}.yymjNa_promptDiffLinemeta{color:var(--dsw-alias-label-caption);background:var(--dsw-alias-bg-module-platform);user-select:none}.yymjNa_promptDiffLinecontext{color:var(--dsw-alias-label-secondary)}.yymjNa_promptDiffLineadded{color:color-mix(in srgb, var(--dsw-alias-state-success-primary) 72%, var(--dsw-alias-label-primary));background:var(--dsw-alias-state-success-tertiary)}.yymjNa_promptDiffLineremoved{color:var(--dsw-alias-state-error-primary);background:color-mix(in srgb, var(--dsw-alias-state-error-primary) 12%, var(--dsw-alias-bg-layer-1))}.yymjNa_assistantVioletBright{color:color-mix(in srgb, var(--dsw-alias-brand-primary-new-colorprimary-new-color) 60%, var(--dsw-alias-state-error-secondary));background:color-mix(in srgb, color-mix(in srgb, var(--dsw-alias-brand-primary-new-colorprimary-new-color) 55%, var(--dsw-alias-state-error-secondary)) 15%, var(--dsw-alias-bg-layer-1))}.yymjNa_toolAmber{color:var(--dsw-alias-state-warn-label);background:var(--dsw-alias-state-warn-tertiary)}.yymjNa_subtoolAmber{color:color-mix(in srgb, var(--dsw-alias-state-warn-label) 62%, var(--dsw-alias-label-tertiary));background:color-mix(in srgb, var(--dsw-alias-state-warn-tertiary) 58%, var(--dsw-alias-bg-layer-1))}.yymjNa_contentText{text-overflow:ellipsis;white-space:nowrap;min-width:0;display:block;overflow:hidden}.yymjNa_toolCallOnly{color:var(--dsw-alias-label-tertiary)}.yymjNa_table tbody tr[data-collapsed-summary=turn] td,.yymjNa_table tbody tr[data-collapsed-summary=assistant] td{height:20px}.yymjNa_table tbody tr[data-collapsed-summary]{cursor:pointer;transition:background-color .12s var(--ds-ease-in-out);outline:none}.yymjNa_table tbody tr[data-collapsed-summary]:hover{background:var(--dsw-alias-interactive-bg-hover)}.yymjNa_table tbody tr[data-collapsed-summary]:focus-visible{box-shadow:inset 0 0 0 1px var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary))}.yymjNa_collapsedTurnContent{min-width:0;color:var(--dsw-alias-label-secondary);align-items:center;font-size:12px;line-height:16px;display:flex}.yymjNa_collapsedTurnEllipsis{color:var(--dsw-alias-label-tertiary);user-select:none;flex:none;margin-right:6px;font-weight:600}.yymjNa_collapsedTurnText{text-overflow:ellipsis;white-space:nowrap;min-width:0;overflow:hidden}.yymjNa_resultPreview{grid-template-columns:clamp(180px, var(--trajectory-tool-request-width,calc(36cqw - 56px)), 480px) minmax(0, 1fr);align-items:center;gap:8px;min-width:0;display:grid}.yymjNa_resultRequest,.yymjNa_inlineResultText{text-overflow:ellipsis;white-space:nowrap;min-width:0;overflow:hidden}.yymjNa_toolCallNameTypeface{color:var(--dsw-alias-label-primary);font:400 12px/18px Menlo,Consolas,Liberation Mono,PingFang SC,Microsoft YaHei}.yymjNa_toolCallPayload{color:var(--dsw-alias-label-secondary);font:400 12px/18px var(--ds-font-family-code);margin-left:7px}.yymjNa_table tbody tr[data-kind=tool] .yymjNa_contentText,.yymjNa_table tbody tr[data-kind=subtool] .yymjNa_contentText,.yymjNa_table tbody tr[data-kind=tool] .yymjNa_resultPreview,.yymjNa_table tbody tr[data-kind=subtool] .yymjNa_resultPreview{font-family:var(--ds-font-family-code);font-size:12px}.yymjNa_table tbody tr[data-kind=subtool] .yymjNa_content{padding-left:26px}.yymjNa_inlineResult{min-width:0;color:var(--dsw-alias-label-secondary);align-items:center;display:flex}.yymjNa_noOutputText{color:var(--dsw-alias-label-caption)}.yymjNa_arrow{color:var(--dsw-alias-label-caption);flex:none;margin-right:8px}.yymjNa_error,.yymjNa_overview dd.yymjNa_error,.yymjNa_details .yymjNa_errorPayload{color:var(--dsw-alias-state-error-primary)}.yymjNa_details .yymjNa_errorPayload .yymjNa_resultBlockText{color:inherit}.yymjNa_details .yymjNa_jsonPayload.yymjNa_errorPayload,.yymjNa_details .yymjNa_jsonPreview.yymjNa_errorPayload{--json-tree-property:var(--dsw-alias-state-error-primary);--json-tree-string:var(--dsw-alias-state-error-primary);--json-tree-number:var(--dsw-alias-state-error-primary);--json-tree-keyword:var(--dsw-alias-state-error-primary);--json-tree-punctuation:var(--dsw-alias-state-error-primary);--json-tree-icon:var(--dsw-alias-state-error-primary)}.yymjNa_details{border-left:.5px solid var(--dsw-alias-border-l2);background:var(--dsw-alias-bg-layer-1);flex-direction:column;flex:none;width:clamp(320px,38%,440px);min-width:0;max-width:calc(100% - 280px);min-height:0;display:flex;position:relative}.yymjNa_detailsResizeHandle{z-index:6;cursor:col-resize;touch-action:none;user-select:none;background:0 0;border:0;width:8px;padding:0;position:absolute;top:0;bottom:0;left:-4px}.yymjNa_detailsResizeHandle:focus-visible{outline:none}.yymjNa_detailsHeader{box-sizing:border-box;border-bottom:.5px solid var(--dsw-alias-border-l2);flex:none;justify-content:space-between;align-items:center;height:42px;padding:0 8px 0 12px;display:flex}.yymjNa_detailsTitle{min-width:0;color:var(--dsw-alias-label-primary);align-items:center;gap:8px;display:flex}.yymjNa_requestDetailsDot{corner-shape:round;background:var(--dsw-alias-label-secondary);border-radius:50%;flex:none;width:5px;height:5px}.yymjNa_requestDetailsName{font:500 12px/16px var(--ds-font-family-code);flex:none}.yymjNa_detailsLocation{min-width:0;color:var(--dsw-alias-label-tertiary);font:11px/16px var(--ds-font-family-code);text-overflow:ellipsis;white-space:nowrap;overflow:hidden}.yymjNa_close{border-radius:var(--dsw-radius-sm);width:28px;height:28px;color:var(--dsw-alias-label-secondary);cursor:pointer;background:0 0;border:0;flex:none;justify-content:center;align-items:center;padding:0;font-size:18px;line-height:18px;display:inline-flex}.yymjNa_close:hover{color:var(--dsw-alias-label-primary);background:var(--dsw-alias-interactive-bg-hover)}.yymjNa_detailTabs{box-sizing:border-box;border-bottom:.5px solid var(--dsw-alias-border-l2);overscroll-behavior-x:contain;scrollbar-width:none;white-space:nowrap;flex:none;gap:1px;width:100%;min-width:0;max-width:100%;height:34px;padding:0 8px;display:flex;overflow:auto hidden}.yymjNa_detailTabs::-webkit-scrollbar{display:none}.yymjNa_detailTab{color:var(--dsw-alias-label-tertiary);cursor:pointer;font:var(--dsw-font-xs-13);background:0 0;border:0;flex:none;padding:0 9px;position:relative}.yymjNa_detailTab:hover{color:var(--dsw-alias-label-primary);background:var(--dsw-alias-interactive-bg-hover)}.yymjNa_detailTabActive{color:var(--dsw-alias-state-business-primary)}.yymjNa_detailTabActive:after{background:var(--dsw-alias-state-business-primary);content:\"\";border-radius:1px 1px 0 0;height:2px;position:absolute;bottom:0;left:9px;right:9px}.yymjNa_close:focus-visible,.yymjNa_detailTab:focus-visible{outline:1px solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:-1px}.yymjNa_detailBody{min-height:0;padding-bottom:var(--dsh-trajectory-bottom-clearance,0px);scrollbar-gutter:stable;flex:1;overflow:hidden auto}.yymjNa_detailBodySummary{box-sizing:border-box;padding-bottom:calc(12px + var(--dsh-trajectory-bottom-clearance,0px));flex-direction:column;display:flex;overflow:hidden}.yymjNa_detailBodySummary>.yymjNa_overview{overscroll-behavior:contain;flex:0 auto;min-height:0;padding-bottom:0;overflow:auto}.yymjNa_detailBodySummary>.yymjNa_compactedSummary{flex:1;min-height:0;overflow:auto}.yymjNa_summaryScrollRegion{--dsh-scrollbar-thumb:transparent;--dsh-scrollbar-thumb-hover:transparent}.yymjNa_summaryScrollRegion:hover,.yymjNa_summaryScrollRegion:focus-within{--dsh-scrollbar-thumb:var(--dsw-alias-scrollbar-bg-l2);--dsh-scrollbar-thumb-hover:var(--dsw-alias-scrollbar-hover-l2)}.yymjNa_compactedSummary .yymjNa_markdownPayload{padding-right:18px}.yymjNa_overview{font:var(--dsw-font-xs-13);margin:0;padding:8px 0}.yymjNa_overviewParentLinks{gap:14px;display:flex}.yymjNa_overviewHierarchyNavLink{all:unset;color:var(--dsw-alias-label-secondary);cursor:pointer;font:inherit;opacity:1;align-items:center;gap:1px;display:inline-flex}.yymjNa_overviewHierarchyNavLink:hover{color:var(--dsw-alias-label-primary)}.yymjNa_overviewHierarchyJumpIconTight{color:var(--dsw-alias-label-caption);flex:none}.yymjNa_overviewHierarchyNavLink:hover .yymjNa_overviewHierarchyJumpIconTight,.yymjNa_overviewHierarchyNavLink:focus-visible .yymjNa_overviewHierarchyJumpIconTight{color:var(--dsw-alias-label-primary)}.yymjNa_overviewHierarchyNavLink:focus-visible{color:var(--dsw-alias-label-primary);outline:1px solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:2px}.yymjNa_overview>div{grid-template-columns:94px minmax(0,1fr);align-items:center;min-height:22px;padding:0 14px;display:grid}.yymjNa_overview>.yymjNa_requestTokenDetail dt{padding-left:12px}.yymjNa_usagePanel{padding:4px 0 10px}.yymjNa_usageGroup+.yymjNa_usageGroup{margin-top:8px}.yymjNa_usageHeading{color:var(--dsw-alias-label-secondary);font:var(--dsw-font-xs-strong-13);user-select:none;margin:0;padding:4px 14px 1px}.yymjNa_usageGroup .yymjNa_overview{padding:0}.yymjNa_overview dt{color:var(--dsw-alias-label-tertiary)}.yymjNa_overview dd{min-width:0;color:var(--dsw-alias-label-primary);text-overflow:ellipsis;white-space:nowrap;margin:0;overflow:hidden}.yymjNa_timestampToggle{all:unset;color:inherit;cursor:pointer;font:inherit;user-select:text}.yymjNa_timestampToggle:focus-visible{outline:1px solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:2px}.yymjNa_overviewSections{border-top:0;flex-direction:column;flex:1;min-height:min-content;display:flex;overflow:hidden}.yymjNa_overviewSection{flex-direction:column;flex:1 1 0;min-height:24px;max-height:max-content;display:flex;overflow:hidden}.yymjNa_overviewSection+.yymjNa_overviewSection{padding-top:2px}.yymjNa_overviewHeading{box-sizing:border-box;width:100%;height:24px;color:var(--dsw-alias-label-secondary);background:var(--dsw-alias-bg-layer-1);font:var(--dsw-font-xs-strong-13);user-select:none;flex:none;align-items:flex-end;margin:0;padding:0 0 3px 14px;display:flex}.yymjNa_overviewTitle{width:max-content;height:auto;color:inherit;cursor:pointer;font:inherit;user-select:none;background:0 0;border:0;flex:none;align-items:center;gap:3px;padding:0;line-height:1;display:inline-flex}.yymjNa_overviewTitleIcon{color:var(--dsw-alias-label-caption);flex:none}.yymjNa_overviewTitle:hover .yymjNa_overviewTitleIcon{color:var(--dsw-alias-label-primary)}.yymjNa_overviewTitle:focus-visible{outline:1px solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:2px}.yymjNa_overviewTitle:focus-visible .yymjNa_overviewTitleIcon{color:var(--dsw-alias-state-business-primary)}.yymjNa_overviewPreview{overscroll-behavior:contain;background:var(--dsw-alias-bg-layer-1);flex:1;min-height:0;overflow:auto}.yymjNa_overviewPreview[data-scroll-more]{mask-image:linear-gradient(#000 calc(100% - 12px),#0000)}.yymjNa_overviewPreview>:last-child{margin-bottom:0;padding-bottom:0}.yymjNa_programDescription{color:var(--dsw-alias-label-primary);font:var(--dsw-font-xs-13);white-space:pre-wrap;overflow-wrap:anywhere;flex:none;margin:0;padding:8px 14px 2px}.yymjNa_programIcon{vertical-align:-2px;margin-right:4px}.yymjNa_programSummary{font-family:var(--dsw-font-family);margin-left:5px}.yymjNa_detailBodyProgram,.yymjNa_programPanel{flex-direction:column;min-height:0;display:flex;overflow:hidden}.yymjNa_programPanel{background:var(--dsw-alias-bg-layer-1);flex:1}.yymjNa_programActions{flex:none;align-items:center;gap:4px;height:16px;margin-left:auto;padding-right:14px;display:inline-flex}.yymjNa_programLanguage{color:var(--dsw-alias-label-tertiary);font:11px/16px var(--ds-font-family-code);padding-right:4px}.yymjNa_programAction{box-sizing:border-box;width:20px;height:16px;color:var(--dsw-alias-label-secondary);cursor:pointer;background:0 0;border:0;justify-content:center;align-items:center;padding:0;display:inline-flex}.yymjNa_programAction:hover{color:var(--dsw-alias-label-primary)}.yymjNa_programAction[aria-pressed=true]{color:var(--dsw-alias-state-business-primary)}.yymjNa_programAction[data-state=failed]{color:var(--dsw-alias-state-error-primary)}.yymjNa_programAction:focus-visible{outline:1px solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:-1px}.yymjNa_programContent{min-width:0;min-height:0}.yymjNa_programPanel>.yymjNa_programContent{overscroll-behavior:contain;flex:1;overflow:auto}.yymjNa_programSource{background:var(--dsw-alias-bg-layer-1);--dsl-code-block-line-white-space:pre;border-radius:0;margin:0!important}.yymjNa_programSource :where(pre){font:12px/16px var(--ds-font-family-code);overflow-wrap:normal;word-break:normal;tab-size:2;border-radius:0;padding:2px 14px 8px;overflow:visible;background:var(--dsw-alias-bg-layer-1)!important}.yymjNa_programSource pre code>.line{box-sizing:border-box;width:max-content;min-width:100%}.yymjNa_programContent[data-wrap=true] .yymjNa_programSource{--dsl-code-block-line-white-space:pre-wrap}.yymjNa_programContent[data-wrap=true] .yymjNa_programSource pre code>.line{overflow-wrap:anywhere;word-break:break-all;width:auto}.yymjNa_programOutput{color:var(--dsw-alias-label-primary);font:12px/16px var(--ds-font-family-code);white-space:pre-wrap;overflow-wrap:anywhere;margin:0;padding:2px 14px 8px}.yymjNa_programError:first-line{color:var(--dsw-alias-state-error-primary);font-weight:600}.yymjNa_overviewPreview .yymjNa_jsonPreview>:first-child,.yymjNa_overviewPreview .yymjNa_schemaTree>:first-child{padding-top:2px;padding-bottom:0}.yymjNa_overviewPreview .yymjNa_schemaTree{margin-bottom:0}.yymjNa_overviewPreview .yymjNa_overview{padding:2px 0 0}.yymjNa_overviewPreview .yymjNa_overview>div{min-height:22px}.yymjNa_markdownPreview,.yymjNa_markdownPayload{color:var(--dsw-alias-label-primary)}.yymjNa_markdownPreview>div:not([data-markdown-variant=compact]),.yymjNa_markdownPayload>div:not([data-markdown-variant=compact]){font:var(--dsw-font-xs-13);gap:8px}.yymjNa_markdownPreview>div:not([data-markdown-variant=compact]) h1,.yymjNa_markdownPayload>div:not([data-markdown-variant=compact]) h1{font:600 16px/22px var(--dsw-font-family)}.yymjNa_markdownPreview>div:not([data-markdown-variant=compact]) h2,.yymjNa_markdownPayload>div:not([data-markdown-variant=compact]) h2{font:600 15px/22px var(--dsw-font-family)}.yymjNa_markdownPreview>div:not([data-markdown-variant=compact]) :where(h3,h4,h5,h6),.yymjNa_markdownPayload>div:not([data-markdown-variant=compact]) :where(h3,h4,h5,h6){font:600 14px/20px var(--dsw-font-family)}.yymjNa_markdownPreview>div:not([data-markdown-variant=compact]) :not(pre)>code,.yymjNa_markdownPayload>div:not([data-markdown-variant=compact]) :not(pre)>code{font:12px/18px var(--ds-font-family-code)}.yymjNa_markdownPreview>div:not([data-markdown-variant=compact]) table,.yymjNa_markdownPayload>div:not([data-markdown-variant=compact]) table{font:var(--dsw-font-xs-13)}.yymjNa_markdownPreview>div:not([data-markdown-variant=compact])>:first-child{margin-top:0}.yymjNa_markdownPreview>div:not([data-markdown-variant=compact])>:last-child{margin-bottom:0}.yymjNa_markdownPreview>div:not([data-markdown-variant=compact]) :where(h1,h2,h3){margin:12px 0 6px}.yymjNa_markdownPreview>div:not([data-markdown-variant=compact]) :where(h4,h5,h6){margin:10px 0 5px}.yymjNa_markdownPreview>div:not([data-markdown-variant=compact]) :where(p,ul,ol){margin:8px 0}.yymjNa_markdownPreview>div:not([data-markdown-variant=compact]) li:not(:first-child){margin-top:3px}.yymjNa_markdownPreview>div:not([data-markdown-variant=compact]) blockquote{margin-top:8px}.yymjNa_markdownPreview>div:not([data-markdown-variant=compact]) hr{margin:14px 0}.yymjNa_markdownPreview>div:not([data-markdown-variant=compact])>.md-code-block{margin:10px 0}.yymjNa_assistantContent .yymjNa_markdownPayload,.yymjNa_assistantContent .yymjNa_payload{min-height:0}.yymjNa_thinkingQuote{--dsh-content-font-size-secondary:13px;--dsh-content-font-delta-secondary:0px;border-left:2px solid var(--dsw-alias-markdown-citation);color:var(--dsw-alias-label-secondary);background:0 0;margin:6px 14px 0 12px;padding-left:6px}.yymjNa_thinkingQuoteOnlyPreview{margin-bottom:8px}.yymjNa_detailBody>.yymjNa_assistantContentRendered .yymjNa_thinkingQuote{margin-top:14px}.yymjNa_thinkingToggle{width:max-content;height:18px;color:var(--dsw-alias-label-tertiary);cursor:pointer;font:600 12px/18px var(--dsw-font-family);user-select:none;background:0 0;border:0;align-items:center;gap:2px;padding:0;display:flex}.yymjNa_thinkingChevron{transition:transform .12s var(--ds-ease-in-out);flex:none}.yymjNa_thinkingToggle[aria-expanded=true] .yymjNa_thinkingChevron{transform:rotate(90deg)}.yymjNa_thinkingToggle:hover{color:var(--dsw-alias-label-secondary)}.yymjNa_thinkingToggle:focus-visible{outline:1px solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:2px}.yymjNa_thinkingQuote .yymjNa_markdownPreview,.yymjNa_thinkingQuote .yymjNa_markdownPayload,.yymjNa_thinkingQuote .yymjNa_payload{min-height:0;color:var(--dsw-alias-label-secondary);background:0 0;padding:2px 0}.yymjNa_assistantOutput{background:var(--dsw-alias-bg-layer-1)}.yymjNa_markdownPreview{padding:2px 14px 8px}.yymjNa_markdownPayload{min-height:100%;padding:14px}.yymjNa_jsonPayload{min-height:100%}.yymjNa_jsonPreview{min-height:0}.yymjNa_schema{background:var(--dsw-alias-bg-layer-1);min-height:100%}.yymjNa_schemaPreview{min-height:0}.yymjNa_schemaIntro{padding:12px 14px 6px}.yymjNa_schemaPreview .yymjNa_schemaIntro{padding-top:2px}.yymjNa_schemaName{color:var(--dsw-alias-label-primary);font:600 12px/18px var(--ds-font-family-code);margin:0}.yymjNa_schemaDescription{color:var(--dsw-alias-label-secondary);font:var(--dsw-font-xs-13);white-space:pre-wrap;margin:2px 0 0}.yymjNa_schemaParameters{min-width:0}.yymjNa_schemaParametersTitle{color:var(--dsw-alias-label-tertiary);font:600 11px/16px var(--dsw-font-family);user-select:none;margin:0;padding:4px 14px 2px}.yymjNa_schemaTree{margin:0 6px 8px 0}.yymjNa_payload{box-sizing:border-box;overflow-wrap:anywhere;min-height:100%;color:var(--dsw-alias-label-primary);background:var(--dsw-alias-markdown-code-block);font:12px/19px var(--ds-font-family-code);tab-size:2;white-space:pre-wrap;margin:0;padding:14px}.yymjNa_payloadPreview{min-height:0;padding:2px 14px 8px}.yymjNa_sourceBlocks{box-sizing:border-box;min-height:100%;padding:12px 14px}.yymjNa_sourceBlock+.yymjNa_sourceBlock{margin-top:14px}.yymjNa_sourceBlockHeader,.yymjNa_sourceBlockJumpTarget{user-select:none;align-items:center;gap:3px;width:max-content;margin-bottom:3px;display:flex}.yymjNa_sourceBlockJumpTarget{all:unset;cursor:pointer;user-select:none;align-items:center;gap:3px;width:max-content;margin-bottom:3px;display:flex}.yymjNa_sourceBlockLabel{color:var(--dsw-alias-label-tertiary);font:11px/16px var(--ds-font-family-code)}.yymjNa_sourceBlockJumpIcon{color:var(--dsw-alias-label-caption);flex:none}.yymjNa_sourceBlockJumpTarget:hover .yymjNa_sourceBlockJumpIcon{color:var(--dsw-alias-label-primary)}.yymjNa_sourceBlockJumpTarget:focus-visible{outline:none}.yymjNa_sourceBlockJumpTarget:focus-visible .yymjNa_sourceBlockJumpIcon{color:var(--dsw-alias-state-business-primary)}.yymjNa_sourceBlockContent{overflow-wrap:anywhere;color:var(--dsw-alias-label-primary);font:12px/19px var(--ds-font-family-code);tab-size:2;white-space:pre-wrap;background:0 0;margin:0}.yymjNa_messageImages{flex-direction:column;gap:8px;margin:8px 14px 14px;display:flex}.yymjNa_attachments{flex-direction:column;gap:8px;margin:8px 14px 14px;padding:0;list-style:none;display:flex}.yymjNa_attachmentsPreview{gap:6px;margin-block:6px 8px}.yymjNa_attachmentRow{border:.5px solid var(--dsw-alias-border-l2-darkmode-thin);border-radius:var(--dsw-radius-lg);align-items:center;gap:10px;min-width:0;padding:8px;display:flex}.yymjNa_attachmentIcon{flex:0 0 48px;place-items:center;width:48px;height:48px;display:grid}.yymjNa_attachmentInfo{flex-direction:column;flex:1;gap:3px;min-width:0;display:flex}.yymjNa_attachmentName{color:var(--dsw-alias-label-primary);text-overflow:ellipsis;white-space:nowrap;font-size:12px;line-height:18px;overflow:hidden}.yymjNa_attachmentMetadata{color:var(--dsw-alias-label-tertiary);text-overflow:ellipsis;white-space:nowrap;font-size:11px;line-height:16px;overflow:hidden}.yymjNa_attachmentDisclosure{border:.5px solid var(--dsw-alias-border-l2-darkmode-thin);border-radius:var(--dsw-radius-md);margin-block:8px}.yymjNa_attachmentDisclosure>summary{cursor:pointer;padding:8px}.yymjNa_attachmentDisclosure>summary .yymjNa_attachmentName{display:block}.yymjNa_attachmentDisclosure>summary:focus-visible{outline:var(--dsw-focus-ring-width) solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:2px}.yymjNa_attachmentDisclosure>pre{padding:0 8px 8px}.yymjNa_assistantToolCalls{box-sizing:border-box;max-width:100%;color:var(--dsw-alias-label-secondary);font:11px/17px var(--ds-font-family-code);margin:2px 14px 12px;padding:0;list-style:none}.yymjNa_assistantToolCallsPreview{margin-top:2px;margin-bottom:8px}.yymjNa_assistantToolCalls li{min-width:0}.yymjNa_assistantToolCallButton{all:unset;box-sizing:border-box;border-radius:var(--dsw-radius-xs);cursor:pointer;align-items:center;width:calc(100% + 4px);min-width:0;height:19px;margin-left:-4px;padding:0 4px;display:flex}.yymjNa_assistantToolCallButton:hover{background:var(--dsw-alias-interactive-bg-hover)}.yymjNa_assistantToolCallButton:focus-visible{background:var(--dsw-alias-interactive-bg-hover);outline:1px solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:-1px}.yymjNa_assistantToolCallIcon{color:var(--dsw-alias-label-caption);flex:none;margin-right:5px}.yymjNa_assistantToolCallButton:hover .yymjNa_assistantToolCallIcon,.yymjNa_assistantToolCallButton:focus-visible .yymjNa_assistantToolCallIcon{color:var(--dsw-alias-label-secondary)}.yymjNa_assistantToolCallText{white-space:nowrap;flex:1;min-width:0;display:flex;overflow:hidden}.yymjNa_assistantToolCallName{color:var(--dsw-alias-label-secondary);flex:none;margin-right:5px;font-weight:500}.yymjNa_assistantToolCallArgs{min-width:0;color:var(--dsw-alias-label-tertiary);text-overflow:ellipsis;white-space:nowrap;overflow:hidden}.yymjNa_systemPrompt{box-sizing:border-box}.yymjNa_toolCatalog{min-height:100%;padding:8px 0 16px}.yymjNa_toolCatalogItem{border-bottom:.5px solid var(--dsw-alias-border-l1)}.yymjNa_toolCatalogSummary{box-sizing:border-box;cursor:pointer;user-select:none;grid-template-columns:12px 12px max-content minmax(0,1fr);align-items:center;gap:5px;min-height:30px;padding:4px 12px;list-style:none;display:grid}.yymjNa_toolCatalogSummary::-webkit-details-marker{display:none}.yymjNa_toolCatalogSummary:hover{background:var(--dsw-alias-interactive-bg-hover)}.yymjNa_toolCatalogSummary:focus-visible{background:var(--dsw-alias-interactive-bg-hover);outline:1px solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:-1px}.yymjNa_toolCatalogChevron,.yymjNa_toolCatalogIcon{color:var(--dsw-alias-label-caption)}.yymjNa_toolCatalogChevron{transition:transform .1s var(--ds-ease-in-out)}.yymjNa_toolCatalogItem[open] .yymjNa_toolCatalogChevron{transform:rotate(90deg)}.yymjNa_toolCatalogName{color:var(--dsw-alias-label-primary);font:500 12px/18px var(--ds-font-family-code)}.yymjNa_toolCatalogDescription{min-width:0;color:var(--dsw-alias-label-tertiary);font:var(--dsw-font-xs-13);text-overflow:ellipsis;white-space:nowrap;overflow:hidden}.yymjNa_toolCatalogDefinition{background:var(--dsw-alias-bg-base);padding:0 0 8px 29px}.yymjNa_toolCatalogFullDescription{color:var(--dsw-alias-label-secondary);font:var(--dsw-font-xs-13);white-space:pre-wrap;margin:0;padding:7px 14px 4px 0}.yymjNa_toolCatalogTree{margin-left:-14px;margin-right:6px}.yymjNa_resultBlocks{box-sizing:border-box;flex-direction:column;gap:10px;min-height:100%;padding:14px;display:flex}.yymjNa_resultBlocksPreview{gap:6px;min-height:0;padding:2px 14px 8px}.yymjNa_resultBlockText{overflow-wrap:anywhere;color:var(--dsw-alias-label-primary);font:12px/19px var(--ds-font-family-code);tab-size:2;white-space:pre-wrap;background:0 0;margin:0}.yymjNa_overviewPreview .yymjNa_noPayload{padding:2px 14px 0}.yymjNa_noPayload{color:var(--dsw-alias-label-tertiary);font:var(--dsw-font-xs-13);margin:0;padding:18px 14px}@media (width<=760px){.yymjNa_details{z-index:5;border-left-color:var(--dsw-alias-border-l3);width:min(92%,420px);max-width:92%;position:absolute;top:0;bottom:0;right:0;box-shadow:-12px 0 32px #00000024}}.yymjNa_toolUpdatePayload{white-space:pre;overflow-x:auto}";
		const tagId$3 = "@deepseek-ai/dsh-client-ui-trajectory/TrajectoryTable.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$3) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-trajectory";
			tag.dataset.pluginCss = tagId$3;
			tag.textContent = css$3;
			document.head.appendChild(tag);
		}
		var TrajectoryTable_module_css_default = {
			"arrow": "yymjNa_arrow",
			"assistantContent": "yymjNa_assistantContent",
			"assistantContentRendered": "yymjNa_assistantContentRendered",
			"assistantOutput": "yymjNa_assistantOutput",
			"assistantToolCallArgs": "yymjNa_assistantToolCallArgs",
			"assistantToolCallButton": "yymjNa_assistantToolCallButton",
			"assistantToolCallIcon": "yymjNa_assistantToolCallIcon",
			"assistantToolCallName": "yymjNa_assistantToolCallName",
			"assistantToolCallText": "yymjNa_assistantToolCallText",
			"assistantToolCalls": "yymjNa_assistantToolCalls",
			"assistantToolCallsPreview": "yymjNa_assistantToolCallsPreview",
			"assistantVioletBright": "yymjNa_assistantVioletBright",
			"attachmentDisclosure": "yymjNa_attachmentDisclosure",
			"attachmentIcon": "yymjNa_attachmentIcon",
			"attachmentInfo": "yymjNa_attachmentInfo",
			"attachmentMetadata": "yymjNa_attachmentMetadata",
			"attachmentName": "yymjNa_attachmentName",
			"attachmentRow": "yymjNa_attachmentRow",
			"attachments": "yymjNa_attachments",
			"attachmentsPreview": "yymjNa_attachmentsPreview",
			"close": "yymjNa_close",
			"collapsedTurnContent": "yymjNa_collapsedTurnContent",
			"collapsedTurnEllipsis": "yymjNa_collapsedTurnEllipsis",
			"collapsedTurnText": "yymjNa_collapsedTurnText",
			"compacted": "yymjNa_compacted",
			"compactedSummary": "yymjNa_compactedSummary",
			"content": "yymjNa_content",
			"contentColumn": "yymjNa_contentColumn",
			"contentText": "yymjNa_contentText",
			"contextGreen": "yymjNa_contextGreen",
			"detailBody": "yymjNa_detailBody",
			"detailBodyProgram": "yymjNa_detailBodyProgram",
			"detailBodySummary": "yymjNa_detailBodySummary",
			"detailTab": "yymjNa_detailTab",
			"detailTabActive": "yymjNa_detailTabActive",
			"detailTabs": "yymjNa_detailTabs",
			"details": "yymjNa_details",
			"detailsHeader": "yymjNa_detailsHeader",
			"detailsLocation": "yymjNa_detailsLocation",
			"detailsResizeHandle": "yymjNa_detailsResizeHandle",
			"detailsTitle": "yymjNa_detailsTitle",
			"error": "yymjNa_error",
			"errorPayload": "yymjNa_errorPayload",
			"event": "yymjNa_event",
			"eventColumn": "yymjNa_eventColumn",
			"eventHeader": "yymjNa_eventHeader",
			"eventInner": "yymjNa_eventInner",
			"historyLoadButton": "yymjNa_historyLoadButton",
			"historyLoadRow": "yymjNa_historyLoadRow",
			"historyLoading": "yymjNa_historyLoading",
			"historyLoadingBar": "yymjNa_historyLoadingBar",
			"inlineResult": "yymjNa_inlineResult",
			"inlineResultText": "yymjNa_inlineResultText",
			"jsonPayload": "yymjNa_jsonPayload",
			"jsonPreview": "yymjNa_jsonPreview",
			"kindSlot": "yymjNa_kindSlot",
			"kindTag": "yymjNa_kindTag",
			"kindTagIcon": "yymjNa_kindTagIcon",
			"kindTagLabel": "yymjNa_kindTagLabel",
			"markdownPayload": "yymjNa_markdownPayload",
			"markdownPreview": "yymjNa_markdownPreview",
			"message": "yymjNa_message",
			"messageImages": "yymjNa_messageImages",
			"noOutputText": "yymjNa_noOutputText",
			"noPayload": "yymjNa_noPayload",
			"overview": "yymjNa_overview",
			"overviewHeading": "yymjNa_overviewHeading",
			"overviewHierarchyJumpIconTight": "yymjNa_overviewHierarchyJumpIconTight",
			"overviewHierarchyNavLink": "yymjNa_overviewHierarchyNavLink",
			"overviewParentLinks": "yymjNa_overviewParentLinks",
			"overviewPreview": "yymjNa_overviewPreview",
			"overviewSection": "yymjNa_overviewSection",
			"overviewSections": "yymjNa_overviewSections",
			"overviewTitle": "yymjNa_overviewTitle",
			"overviewTitleIcon": "yymjNa_overviewTitleIcon",
			"payload": "yymjNa_payload",
			"payloadPreview": "yymjNa_payloadPreview",
			"programAction": "yymjNa_programAction",
			"programActions": "yymjNa_programActions",
			"programContent": "yymjNa_programContent",
			"programDescription": "yymjNa_programDescription",
			"programError": "yymjNa_programError",
			"programIcon": "yymjNa_programIcon",
			"programLanguage": "yymjNa_programLanguage",
			"programOutput": "yymjNa_programOutput",
			"programPanel": "yymjNa_programPanel",
			"programSource": "yymjNa_programSource",
			"programSummary": "yymjNa_programSummary",
			"promptDiff": "yymjNa_promptDiff",
			"promptDiffLineadded": "yymjNa_promptDiffLineadded",
			"promptDiffLinecontext": "yymjNa_promptDiffLinecontext",
			"promptDiffLinemeta": "yymjNa_promptDiffLinemeta",
			"promptDiffLineremoved": "yymjNa_promptDiffLineremoved",
			"promptDiffSection": "yymjNa_promptDiffSection",
			"promptDiffSections": "yymjNa_promptDiffSections",
			"promptDiffTitle": "yymjNa_promptDiffTitle",
			"requestBoundaryControl": "yymjNa_requestBoundaryControl",
			"requestBoundaryControlActive": "yymjNa_requestBoundaryControlActive",
			"requestDetailsDot": "yymjNa_requestDetailsDot",
			"requestDetailsName": "yymjNa_requestDetailsName",
			"requestTokenDetail": "yymjNa_requestTokenDetail",
			"resultBlockText": "yymjNa_resultBlockText",
			"resultBlocks": "yymjNa_resultBlocks",
			"resultBlocksPreview": "yymjNa_resultBlocksPreview",
			"resultPreview": "yymjNa_resultPreview",
			"resultRequest": "yymjNa_resultRequest",
			"schema": "yymjNa_schema",
			"schemaDescription": "yymjNa_schemaDescription",
			"schemaIntro": "yymjNa_schemaIntro",
			"schemaName": "yymjNa_schemaName",
			"schemaParameters": "yymjNa_schemaParameters",
			"schemaParametersTitle": "yymjNa_schemaParametersTitle",
			"schemaPreview": "yymjNa_schemaPreview",
			"schemaTree": "yymjNa_schemaTree",
			"selectionRail": "yymjNa_selectionRail",
			"sourceBlock": "yymjNa_sourceBlock",
			"sourceBlockContent": "yymjNa_sourceBlockContent",
			"sourceBlockHeader": "yymjNa_sourceBlockHeader",
			"sourceBlockJumpIcon": "yymjNa_sourceBlockJumpIcon",
			"sourceBlockJumpTarget": "yymjNa_sourceBlockJumpTarget",
			"sourceBlockLabel": "yymjNa_sourceBlockLabel",
			"sourceBlocks": "yymjNa_sourceBlocks",
			"split": "yymjNa_split",
			"subtoolAmber": "yymjNa_subtoolAmber",
			"summaryScrollRegion": "yymjNa_summaryScrollRegion",
			"systemNeutral": "yymjNa_systemNeutral",
			"systemPrompt": "yymjNa_systemPrompt",
			"table": "yymjNa_table",
			"tablePane": "yymjNa_tablePane",
			"thinkingChevron": "yymjNa_thinkingChevron",
			"thinkingQuote": "yymjNa_thinkingQuote",
			"thinkingQuoteOnlyPreview": "yymjNa_thinkingQuoteOnlyPreview",
			"thinkingToggle": "yymjNa_thinkingToggle",
			"timestampToggle": "yymjNa_timestampToggle",
			"toolAmber": "yymjNa_toolAmber",
			"toolCallNameTypeface": "yymjNa_toolCallNameTypeface",
			"toolCallOnly": "yymjNa_toolCallOnly",
			"toolCallPayload": "yymjNa_toolCallPayload",
			"toolCatalog": "yymjNa_toolCatalog",
			"toolCatalogChevron": "yymjNa_toolCatalogChevron",
			"toolCatalogDefinition": "yymjNa_toolCatalogDefinition",
			"toolCatalogDescription": "yymjNa_toolCatalogDescription",
			"toolCatalogFullDescription": "yymjNa_toolCatalogFullDescription",
			"toolCatalogIcon": "yymjNa_toolCatalogIcon",
			"toolCatalogItem": "yymjNa_toolCatalogItem",
			"toolCatalogName": "yymjNa_toolCatalogName",
			"toolCatalogSummary": "yymjNa_toolCatalogSummary",
			"toolCatalogTree": "yymjNa_toolCatalogTree",
			"toolUpdatePayload": "yymjNa_toolUpdatePayload",
			"trajectory-table": "yymjNa_trajectory-table",
			"turnLabel": "yymjNa_turnLabel",
			"turnLabelActive": "yymjNa_turnLabelActive",
			"turnLabelCompact": "yymjNa_turnLabelCompact",
			"turnLabelFull": "yymjNa_turnLabelFull",
			"turnRail": "yymjNa_turnRail",
			"usageGroup": "yymjNa_usageGroup",
			"usageHeading": "yymjNa_usageHeading",
			"usagePanel": "yymjNa_usagePanel",
			"user": "yymjNa_user",
			"virtualSpacer": "yymjNa_virtualSpacer",
			"visuallyHidden": "yymjNa_visuallyHidden"
		};
		//#endregion
		//#region src/client/TrajectoryTable.tsx
		/** Turn-aware trajectory event ledger with a local record inspector. */
		const BOTTOM_FOLLOW_THRESHOLD_PX = 2;
		const OLDER_LOAD_THRESHOLD_PX = 48;
		const HISTORY_LOAD_ROW_HEIGHT_PX = 30;
		const VIRTUALIZATION_THRESHOLD = 100;
		const VIRTUAL_OVERSCAN_ROWS = 12;
		const VIRTUAL_INITIAL_VIEWPORT_HEIGHT_PX = 600;
		const KIND_LABEL_KEY = {
			system: "kind.system",
			user: "kind.user",
			context: "kind.context",
			compacted: "kind.compacted",
			message: "kind.assistant",
			tool: "kind.tool",
			subtool: "kind.subtool"
		};
		function ToolWrenchIcon() {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("svg", {
				width: "13",
				height: "13",
				viewBox: "0 0 16 16",
				fill: "none",
				stroke: "currentColor",
				strokeWidth: "1.5",
				strokeLinecap: "round",
				strokeLinejoin: "round",
				"data-role-icon": "wrench",
				"aria-hidden": "true",
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", { d: "M14 3.3a3.8 3.8 0 0 1-4.8 4.8l-5.1 5.1a1.6 1.6 0 1 1-2.3-2.3l5.1-5.1A3.8 3.8 0 0 1 11.7 1l-2.3 2.3 2.3 2.3L14 3.3Z" })
			});
		}
		function InformationIcon() {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("svg", {
				width: "14",
				height: "14",
				viewBox: "0 0 16 16",
				fill: "none",
				stroke: "currentColor",
				strokeWidth: "1.4",
				strokeLinecap: "round",
				"data-role-icon": "information",
				"aria-hidden": "true",
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("circle", {
						cx: "8",
						cy: "8",
						r: "6.7"
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("circle", {
						cx: "8",
						cy: "5.5",
						r: ".85",
						fill: "currentColor",
						stroke: "none"
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
						d: "M8 7.75v3.4",
						strokeWidth: "1.8"
					})
				]
			});
		}
		function CompactedIcon() {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("svg", {
				width: "13",
				height: "13",
				viewBox: "0 0 16 16",
				fill: "none",
				stroke: "currentColor",
				strokeWidth: "1.5",
				strokeLinecap: "round",
				strokeLinejoin: "round",
				"data-role-icon": "compacted",
				"aria-hidden": "true",
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", { d: "m2.5 2.5 3.75 3.75M3 6.25h3.25V3" }),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", { d: "m13.5 2.5-3.75 3.75M13 6.25H9.75V3" }),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", { d: "m2.5 13.5 3.75-3.75M3 9.75h3.25V13" }),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", { d: "m13.5 13.5-3.75-3.75M13 9.75H9.75V13" })
				]
			});
		}
		const KIND_ICON = {
			system: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconSettingsOutlineRegular, { size: 13 }),
			user: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconUserOutlineRegular, { size: 13 }),
			context: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(InformationIcon, {}),
			compacted: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(CompactedIcon, {}),
			message: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconSparkleRegular, { size: 13 }),
			tool: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ToolWrenchIcon, {}),
			subtool: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ToolWrenchIcon, {})
		};
		function useStableVirtualRowStructure(rows) {
			const cache = (0, react.useRef)({
				rows: [],
				structure: []
			});
			if (cache.current.rows === rows) return cache.current.structure;
			const structure = cache.current.structure.length === rows.length && rows.every((row, index) => {
				const previous = cache.current.structure[index];
				return previous?.key === row.key && previous.height === row.height;
			}) ? cache.current.structure : rows.map((row) => ({
				key: row.key,
				height: row.height
			}));
			cache.current = {
				rows,
				structure
			};
			return structure;
		}
		const DETAILS_MIN_WIDTH = 320;
		const DETAILS_MAX_WIDTH = 720;
		const TABLE_MIN_WIDTH = 280;
		const DETAILS_RESIZE_STEP = 16;
		const TOOL_REQUEST_SHARE = .58;
		const TOOL_REQUEST_MIN_WIDTH = 180;
		const TOOL_REQUEST_MAX_WIDTH = 480;
		const DEFAULT_TOOL_REQUEST_SHARE = .36;
		const DEFAULT_TOOL_REQUEST_OFFSET = 56;
		const SYSTEM_PROMPT_TABS = [{
			id: "system-prompt",
			labelKey: "tab.systemPrompt"
		}, {
			id: "tools",
			labelKey: "tab.tools"
		}];
		const SYSTEM_UPDATE_TABS = [{
			id: "diff",
			labelKey: "tab.diff"
		}, ...SYSTEM_PROMPT_TABS];
		const REQUEST_TABS = [
			{
				id: "overview",
				labelKey: "tab.summary"
			},
			{
				id: "options",
				labelKey: "tab.options"
			},
			{
				id: "usage",
				labelKey: "tab.usage"
			},
			{
				id: "timing",
				labelKey: "tab.timing"
			}
		];
		function jsonTreeLabels(t) {
			return {
				copyValue: t("copy.value"),
				copyJson: t("copy.json"),
				copyPath: t("copy.path"),
				copyPrettyJson: t("copy.prettyJson"),
				copyCompactJson: t("copy.compactJson"),
				copied: t("copied"),
				copyFailed: t("copy.failed"),
				collapseNode: t("collapse"),
				expandNode: t("expand"),
				copyButtonTitle: (action) => t("copy.optionsHint", { action })
			};
		}
		function markdownLabels(t) {
			return {
				code: {
					copyLabel: t("copy"),
					copiedLabel: t("copied")
				},
				footnotes: t("markdown.footnotes")
			};
		}
		function clampDetailsWidth(width, splitWidth) {
			const maxWidth = Math.max(DETAILS_MIN_WIDTH, Math.min(DETAILS_MAX_WIDTH, splitWidth - TABLE_MIN_WIDTH));
			return Math.round(Math.min(Math.max(width, DETAILS_MIN_WIDTH), maxWidth));
		}
		function defaultToolRequestWidth(splitWidth) {
			return Math.min(Math.max(splitWidth * DEFAULT_TOOL_REQUEST_SHARE - DEFAULT_TOOL_REQUEST_OFFSET, TOOL_REQUEST_MIN_WIDTH), TOOL_REQUEST_MAX_WIDTH);
		}
		function formatDurationMs(milliseconds, t) {
			if (milliseconds < 1e3) return t("unit.milliseconds", { value: Math.round(milliseconds) });
			return t("unit.seconds", { value: (milliseconds / 1e3).toFixed(milliseconds < 1e4 ? 2 : 1) });
		}
		function formatStartedAt(timestamp, t) {
			if (timestamp === null || !Number.isFinite(timestamp)) return t("timing.notAvailable");
			const date = new Date(timestamp);
			const two = (value) => String(value).padStart(2, "0");
			const three = (value) => String(value).padStart(3, "0");
			const time = `${two(date.getHours())}:${two(date.getMinutes())}:${two(date.getSeconds())}.${three(date.getMilliseconds())}`;
			return `${`${date.getFullYear()}-${two(date.getMonth() + 1)}-${two(date.getDate())}`} ${time}`;
		}
		/** Whether a click lands on an active text selection and should keep it. */
		function clickSelectsText(target) {
			const selection = window.getSelection();
			return selection !== null && !selection.isCollapsed && selection.rangeCount > 0 && selection.getRangeAt(0).intersectsNode(target);
		}
		function StartedAtValue({ timestamp, t }) {
			const [showUnix, setShowUnix] = (0, react.useState)(false);
			if (timestamp === null || !Number.isFinite(timestamp)) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: t("timing.notAvailable") });
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
				type: "button",
				className: TrajectoryTable_module_css_default.timestampToggle,
				title: showUnix ? t("timing.showLocalTime") : t("timing.showUnixTimestamp"),
				onClick: (event) => {
					if (clickSelectsText(event.currentTarget)) return;
					setShowUnix((current) => !current);
				},
				children: showUnix ? (timestamp / 1e3).toFixed(3) : formatStartedAt(timestamp, t)
			}) });
		}
		function totalTime(metrics, t) {
			if (!metrics.timingRecorded) return t("timing.notRecorded");
			if (metrics.stepStartTime === null) return t("timing.stepStartUnavailable");
			if (metrics.completedTime === null) return t("status.pending");
			return formatDurationMs(Math.max(0, metrics.completedTime - metrics.stepStartTime), t);
		}
		function ttft(metrics, t) {
			if (!metrics.timingRecorded) return t("timing.notRecorded");
			if (metrics.stepStartTime === null) return t("timing.stepStartUnavailable");
			if (metrics.firstTokenTime === null) return t("timing.firstTokenUnavailable");
			return formatDurationMs(Math.max(0, metrics.firstTokenTime - metrics.stepStartTime), t);
		}
		function generationTime(metrics, t) {
			if (!metrics.timingRecorded || metrics.firstTokenTime === null) return t("timing.firstTokenUnavailable");
			if (metrics.completedTime === null) return t("status.pending");
			return formatDurationMs(Math.max(0, metrics.completedTime - metrics.firstTokenTime), t);
		}
		function throughput(metrics, t) {
			if (!metrics.usageProvided) return t("timing.usageUnavailable");
			if (metrics.outputTokens === null) return t("timing.outputTokensUnavailable");
			if (!metrics.timingRecorded || metrics.firstTokenTime === null) return t("timing.firstTokenUnavailable");
			if (metrics.completedTime === null) return t("status.pending");
			const generationSeconds = (metrics.completedTime - metrics.firstTokenTime) / 1e3;
			if (generationSeconds <= 0) return t("timing.durationTooShort");
			return t("unit.tokensPerSecond", { value: (metrics.outputTokens / generationSeconds).toFixed(1) });
		}
		function AssistantTimingPanel({ metrics, t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("dl", {
				className: TrajectoryTable_module_css_default.overview,
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("timing.started") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(StartedAtValue, {
						timestamp: metrics.stepStartTime,
						t
					})] }),
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("timing.totalDuration") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: totalTime(metrics, t) })] }),
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("timing.ttft") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: ttft(metrics, t) })] }),
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("timing.generation") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: generationTime(metrics, t) })] }),
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("timing.throughput") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: throughput(metrics, t) })] })
				]
			});
		}
		function flattenRecords(turns) {
			return turns.flatMap((turn, section) => {
				let firstInSection = true;
				const records = turn.groups.flatMap((group) => {
					return group.cells.map((cell, index) => {
						const turnStart = firstInSection && cell.requestOnly !== true && cell.kind !== "system" && (cell.kind !== "compacted" || turn.turn === null);
						if (turnStart) firstInSection = false;
						return {
							turn: turn.turn,
							section,
							group: group.title,
							groupStart: index === 0,
							turnStart,
							cell,
							turnEnd: false
						};
					});
				});
				const last = records.at(-1);
				if (last !== void 0) last.turnEnd = true;
				return records;
			});
		}
		function filterRecords(records, matches) {
			const filtered = records.filter((record) => record.cell.requestOnly !== true && matches.has(record.cell.index)).map((record) => ({
				...record,
				groupStart: false,
				turnStart: false,
				turnEnd: false
			}));
			const startedSections = /* @__PURE__ */ new Set();
			for (const [index, record] of filtered.entries()) {
				const previous = filtered[index - 1];
				const next = filtered[index + 1];
				record.groupStart = previous === void 0 || previous.section !== record.section || previous.group !== record.group;
				record.turnStart = !startedSections.has(record.section) && record.cell.kind !== "system" && (record.cell.kind !== "compacted" || record.turn === null);
				if (record.turnStart) startedSections.add(record.section);
				record.turnEnd = next === void 0 || next.section !== record.section;
			}
			return filtered;
		}
		function requestKey(turn, group) {
			return `${turn}\u0000${group}`;
		}
		function requestIdentity(request) {
			return request.purpose === "compaction" ? `compaction\u0000${request.seq}` : `assistant\u0000${request.turn}\u0000${request.step}`;
		}
		function indexRequestBoundaries(records, requestGroups) {
			const boundaries = /* @__PURE__ */ new Map();
			for (const record of records) {
				const key = requestKey(record.turn, record.group);
				if (!requestGroups.has(key)) continue;
				if (boundaries.has(key)) continue;
				if (record.cell.kind === "user" || record.cell.kind === "context") continue;
				boundaries.set(key, record.cell.index);
			}
			return boundaries;
		}
		function sectionLabel(turn, t) {
			return turn === null ? t("section.betweenTurns") : t("turn.label", { turn });
		}
		function indexRequestNumbers(sessionNumbers) {
			const numbers = /* @__PURE__ */ new Map();
			for (const request of sessionNumbers ?? []) numbers.set(requestKey(request.turn, request.group), request.number);
			return numbers;
		}
		function indexRequestBoundaryRuns(records, requestGroups) {
			const indexes = /* @__PURE__ */ new Map();
			let runLength = 0;
			for (const record of records) {
				if (record.cell.requestOnly === true) {
					indexes.set(record.cell.index, runLength++);
					continue;
				}
				if (runLength > 0 && record.groupStart && requestGroups.has(requestKey(record.turn, record.group))) indexes.set(record.cell.index, runLength);
				runLength = 0;
			}
			return indexes;
		}
		function summarizeTurn(records, requestGroups, t) {
			const steps = new Set(records.map((record) => requestKey(record.turn, record.group)).filter((key) => requestGroups.has(key))).size;
			const toolCalls = records.filter((record) => record.cell.kind === "tool" || record.cell.kind === "subtool").length;
			return [t(steps === 1 ? "summary.steps.one" : "summary.steps.other", { count: steps }), t(toolCalls === 1 ? "summary.toolCalls.one" : "summary.toolCalls.other", { count: toolCalls })].join(" · ");
		}
		function collapseTurnRecords(records, collapsedTurns, requestGroups, t) {
			const recordsByTurn = /* @__PURE__ */ new Map();
			for (const record of records) {
				if (record.turn === null) continue;
				const turnRecords = recordsByTurn.get(record.turn) ?? [];
				turnRecords.push(record);
				recordsByTurn.set(record.turn, turnRecords);
			}
			return records.flatMap((record) => {
				if (record.turn === null || !collapsedTurns.has(record.turn)) return [record];
				const turnRecords = recordsByTurn.get(record.turn) ?? [record];
				if (record.cell.requestOnly === true || record.cell.kind === "system") return [record];
				const contentRecords = turnRecords.filter((candidate) => candidate.cell.requestOnly !== true && candidate.cell.kind !== "system");
				if (contentRecords.length <= 1) return [record];
				if (record.cell.index !== contentRecords[0]?.cell.index) return [];
				return [{
					...record,
					turnEnd: false
				}, {
					...record,
					groupStart: false,
					turnStart: false,
					turnEnd: true,
					collapsedSummary: summarizeTurn(contentRecords.slice(1), requestGroups, t),
					collapsedSummaryKind: "turn"
				}];
			});
		}
		function assistantToolCalls(records, assistantIndex) {
			const at = records.findIndex((record) => record.cell.index === assistantIndex);
			if (at === -1 || records[at]?.cell.kind !== "message") return [];
			const calls = [];
			for (let i = at + 1; i < records.length; i++) {
				const record = records[i];
				if (record === void 0) break;
				if (record.cell.kind !== "tool" && record.cell.kind !== "subtool") break;
				calls.push(record);
			}
			return calls;
		}
		function summarizeAssistantTools(records, t) {
			const names = [...new Set(records.map((record) => {
				const separator = record.cell.text.indexOf(" · ");
				return separator === -1 ? record.cell.text : record.cell.text.slice(0, separator);
			}).filter((name) => name !== ""))];
			const count = records.length;
			const summary = t(count === 1 ? "summary.toolCalls.one" : "summary.toolCalls.other", { count });
			return names.length > 0 ? `${summary} · ${names.join(", ")}` : summary;
		}
		function collapseAssistantRecords(records, collapsedAssistants, t) {
			const out = [];
			for (let i = 0; i < records.length; i++) {
				const record = records[i];
				if (record === void 0) continue;
				out.push(record);
				if (record.cell.kind !== "message" || !collapsedAssistants.has(trajectoryRecordId(record.cell))) continue;
				const calls = [];
				for (let j = i + 1; j < records.length; j++) {
					const candidate = records[j];
					if (candidate === void 0 || candidate.collapsedSummary !== void 0 || candidate.cell.kind !== "tool" && candidate.cell.kind !== "subtool") break;
					calls.push(candidate);
				}
				if (calls.length === 0) continue;
				const last = calls.at(-1);
				out[out.length - 1] = {
					...record,
					turnEnd: false
				};
				out.push({
					...record,
					groupStart: false,
					turnStart: false,
					turnEnd: last?.turnEnd ?? false,
					collapsedSummary: summarizeAssistantTools(calls, t),
					collapsedSummaryKind: "assistant"
				});
				i += calls.length;
			}
			return out;
		}
		function stateOf(record) {
			if (record.cell.isError) return "error";
			if (record.cell.kind === "compacted" && record.cell.timeSeconds === null) return "running";
			if ((record.cell.kind === "tool" || record.cell.kind === "subtool") && record.cell.outputDetail === void 0) return "running";
			return "complete";
		}
		function statusLabel(state, t) {
			if (state === "error") return t("status.failed");
			if (state === "running") return t("status.pending");
			return t("status.completed");
		}
		function requestErrorMessage(request, t) {
			if (request.errorCode === "AUTH") return t("details.failure.auth");
			if (request.error === "trajectory.compaction-interrupted") return t("layout.compactionInterrupted");
			return request.error;
		}
		function TokenRows({ cell, t }) {
			const content = cell.output !== void 0 && cell.think !== void 0 ? Math.max(0, cell.output - cell.think) : void 0;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [
				/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("usage.tokens") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: cell.output === void 0 ? "—" : t("unit.tokens", { value: cell.output }) })] }),
				cell.think !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: TrajectoryTable_module_css_default.requestTokenDetail,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("usage.reasoning") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: t("unit.tokens", { value: cell.think }) })]
				}),
				content !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: TrajectoryTable_module_css_default.requestTokenDetail,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("usage.content") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: t("unit.tokens", { value: content }) })]
				})
			] });
		}
		function inputTotal(usage) {
			if (usage.input === void 0 && usage.cacheRead === void 0 && usage.cacheWrite === void 0) return void 0;
			return (usage.input ?? 0) + (usage.cacheRead ?? 0) + (usage.cacheWrite ?? 0);
		}
		function UsageRows({ usage, t }) {
			if (usage === void 0) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
				className: TrajectoryTable_module_css_default.noPayload,
				children: t("usage.notReported")
			});
			const totalInput = inputTotal(usage);
			const otherOutput = usage.output !== void 0 && usage.reasoning !== void 0 ? usage.output - usage.reasoning : void 0;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("dl", {
				className: TrajectoryTable_module_css_default.overview,
				children: [
					totalInput !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("usage.input") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: t("unit.tokens", { value: totalInput }) })] }),
					usage.cacheRead !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: TrajectoryTable_module_css_default.requestTokenDetail,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("usage.cached") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: t("unit.tokens", { value: usage.cacheRead }) })]
					}),
					usage.cacheWrite !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: TrajectoryTable_module_css_default.requestTokenDetail,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("usage.cacheCreated") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: t("unit.tokens", { value: usage.cacheWrite }) })]
					}),
					usage.input !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: TrajectoryTable_module_css_default.requestTokenDetail,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("usage.other") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: t("unit.tokens", { value: usage.input }) })]
					}),
					usage.output !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("usage.output") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: t("unit.tokens", { value: usage.output }) })] }),
					usage.reasoning !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: TrajectoryTable_module_css_default.requestTokenDetail,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("usage.reasoning") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: t("unit.tokens", { value: usage.reasoning }) })]
					}),
					otherOutput !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: TrajectoryTable_module_css_default.requestTokenDetail,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("usage.content") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: t("unit.tokens", { value: otherOutput }) })]
					})
				]
			});
		}
		function RequestUsagePanel({ usage, cumulative, t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: TrajectoryTable_module_css_default.usagePanel,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
					className: TrajectoryTable_module_css_default.usageGroup,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("h4", {
						className: TrajectoryTable_module_css_default.usageHeading,
						children: t("usage.thisRequest")
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(UsageRows, {
						usage,
						t
					})]
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
					className: TrajectoryTable_module_css_default.usageGroup,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("h4", {
						className: TrajectoryTable_module_css_default.usageHeading,
						children: t("usage.sessionCumulative")
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(UsageRows, {
						usage: cumulative,
						t
					})]
				})]
			});
		}
		function RequestOptions({ options, preview = false, stringWrapping, t }) {
			if (options === void 0) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
				className: TrajectoryTable_module_css_default.noPayload,
				children: t("options.notRecorded")
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.JsonTree, {
				data: options,
				stringWrapping,
				collapsedStringLines: preview ? 3 : 12,
				label: t("options.json"),
				labels: jsonTreeLabels(t),
				className: preview ? TrajectoryTable_module_css_default.jsonPreview : TrajectoryTable_module_css_default.jsonPayload
			});
		}
		function messageSourceLabel(source, t) {
			if (typeof source !== "object" || source === null || Array.isArray(source)) return t("source.unknown");
			const properties = source;
			const kind = properties.kind;
			if (kind === "user") return t("source.user");
			if (kind === "goal") {
				const round = properties.round;
				return typeof round === "number" && round > 0 ? t("source.goalRound", { round }) : t("source.goal");
			}
			if (typeof kind !== "string" || kind === "") return t("source.unknown");
			return `${kind[0]?.toUpperCase() ?? ""}${kind.slice(1)}`;
		}
		function MessageSource({ record, stringWrapping, t }) {
			const source = record.cell.messageSource;
			if (source === void 0) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
				className: TrajectoryTable_module_css_default.noPayload,
				children: t("source.notRecorded")
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.JsonTree, {
				data: typeof source === "object" && source !== null ? source : { value: source },
				stringWrapping,
				collapsedStringLines: 12,
				label: t("source.messageJson"),
				labels: jsonTreeLabels(t),
				className: TrajectoryTable_module_css_default.jsonPayload
			});
		}
		function isMarkdownRecord(record) {
			return record.cell.kind === "user" || record.cell.kind === "context" || record.cell.kind === "message";
		}
		function parentRecords(records, record) {
			if (record.cell.kind !== "tool" && record.cell.kind !== "subtool") return {};
			const at = records.findIndex((candidate) => candidate.cell.index === record.cell.index);
			if (at === -1) return {};
			let tool;
			if (record.cell.kind === "subtool") for (let i = at - 1; i >= 0; i--) {
				const candidate = records[i];
				if (candidate === void 0 || candidate.turn !== record.turn || candidate.group !== record.group) break;
				if (candidate.cell.kind === "tool") {
					tool = candidate;
					break;
				}
			}
			const parentCallId = tool?.cell.callId ?? record.cell.callId;
			let message;
			if (parentCallId !== void 0) message = records.find((candidate) => candidate.turn === record.turn && candidate.cell.kind === "message" && candidate.cell.sourceBlocks?.some((block) => block.callId === parentCallId) === true);
			return {
				...message === void 0 ? {} : { message },
				...tool === void 0 ? {} : { tool }
			};
		}
		function markdownSource(record) {
			if (record.cell.kind === "user" || record.cell.kind === "context") return record.cell.inputDetail;
			if (record.cell.kind === "message" || record.cell.kind === "compacted") return record.cell.outputDetail;
		}
		function detailTabs(record) {
			if (record.cell.kind === "system") {
				if (record.cell.promptDetail === void 0 && record.cell.systemPromptDetail !== void 0) return SYSTEM_PROMPT_TABS.filter((tab) => tab.id === "system-prompt");
				return record.cell.previousPromptDetail === void 0 ? SYSTEM_PROMPT_TABS : SYSTEM_UPDATE_TABS;
			}
			if (record.cell.kind === "compacted") return [{
				id: "overview",
				labelKey: "tab.summary"
			}, {
				id: "raw",
				labelKey: "tab.rawOutput"
			}];
			if (isMarkdownRecord(record)) return [
				{
					id: "overview",
					labelKey: "tab.summary"
				},
				{
					id: "rendered",
					labelKey: "tab.preview"
				},
				{
					id: "raw",
					labelKey: "tab.raw"
				},
				...record.cell.messageSource === void 0 ? [] : [{
					id: "source",
					labelKey: "tab.source"
				}]
			];
			return [
				{
					id: "overview",
					labelKey: "tab.summary"
				},
				...record.cell.inputDetail ? [{
					id: "input",
					labelKey: codeProgram(record.cell) === void 0 ? "tab.payload" : "code.source"
				}] : [],
				...record.cell.outputDetail || codeProgram(record.cell) !== void 0 ? [{
					id: "output",
					labelKey: "tab.result"
				}] : [],
				{
					id: "schema",
					labelKey: "tab.schema"
				},
				{
					id: "timing",
					labelKey: "tab.timing"
				}
			];
		}
		function recordDisplayText(cell, t) {
			if (isToolCallOnly(cell, t)) return "";
			const program = codeProgram(cell);
			if (program !== void 0) return `${PTC_TOOL_NAME} · ${program.description.replace(/\s+/g, " ")}`;
			if (cell.previewMarkdown !== void 0) {
				const preview = trajectoryPreviewText(cell.previewMarkdown);
				if (cell.text === "") return preview;
				return preview === "" ? cell.text : `${cell.text} · ${preview}`;
			}
			if (cell.text !== "") return cell.text;
			const markdown = cell.kind === "user" || cell.kind === "context" ? cell.inputDetail : cell.kind === "message" ? cell.outputDetail ?? cell.thinkingDetail : void 0;
			return markdown === void 0 ? "" : trajectoryPreviewText(markdown);
		}
		function recordResultText(cell) {
			return cell.resultPreviewMarkdown === void 0 ? cell.result : trajectoryPreviewText(cell.resultPreviewMarkdown);
		}
		function toolCallTextParts(cell, text) {
			if (cell.kind !== "tool" && cell.kind !== "subtool") return void 0;
			const program = cell.toolName === PTC_TOOL_NAME;
			const separator = text.indexOf(" · ");
			if (separator === -1) return {
				name: text,
				program
			};
			return {
				program,
				name: text.slice(0, separator),
				args: text.slice(separator + 3)
			};
		}
		function isToolCallOnly(cell, t) {
			return cell.kind === "message" && !cell.outputDetail && !cell.thinkingDetail && cell.text === t("layout.toolCallOnly");
		}
		function RecordPresentation({ cell, children, t }) {
			const displayText = (0, react.useMemo)(() => recordDisplayText(cell, t), [
				cell.kind,
				cell.text,
				cell.toolName,
				cell.previewMarkdown,
				cell.inputDetail,
				cell.outputDetail,
				cell.thinkingDetail,
				t
			]);
			const resultText = (0, react.useMemo)(() => recordResultText(cell), [cell.result, cell.resultPreviewMarkdown]);
			const toolCallOnly = isToolCallOnly(cell, t);
			const toolCallText = toolCallTextParts(cell, displayText);
			return children({
				displayText,
				listDisplayText: toolCallOnly ? t("record.toolCallOnly") : toolCallText === void 0 ? displayText : [toolCallText.name, toolCallText.args].filter(Boolean).join(" "),
				resultText,
				toolCallOnly,
				toolCallText
			});
		}
		function RecordListText({ displayText, toolCallOnly, toolCallText, t }) {
			if (toolCallOnly) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
				className: TrajectoryTable_module_css_default.toolCallOnly,
				children: t("record.toolCallOnly")
			});
			if (toolCallText === void 0) return displayText || "—";
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
				className: TrajectoryTable_module_css_default.toolCallNameTypeface,
				children: [toolCallText.program && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconCodeOutlineRegular, {
					className: TrajectoryTable_module_css_default.programIcon,
					size: 12
				}), toolCallText.name || "—"]
			}), toolCallText.args !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
				className: toolCallText.program ? TrajectoryTable_module_css_default.programSummary : TrajectoryTable_module_css_default.toolCallPayload,
				children: toolCallText.args
			})] });
		}
		function MarkdownFragment({ text, rendered, preview, variant = "body", t }) {
			const labels = (0, react.useMemo)(() => markdownLabels(t), [t]);
			if (rendered) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: preview ? TrajectoryTable_module_css_default.markdownPreview : TrajectoryTable_module_css_default.markdownPayload,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.MarkdownText, {
					text,
					labels,
					variant
				})
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("pre", {
				className: `${TrajectoryTable_module_css_default.payload} ${preview ? TrajectoryTable_module_css_default.payloadPreview : ""}`,
				children: text
			});
		}
		function SourceBlocks({ blocks, onOpenCall, t }) {
			const attachments = new Map(recordAttachments(blocks, t).map((entry) => [entry.index, entry]));
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: TrajectoryTable_module_css_default.sourceBlocks,
				children: blocks.map((block, index) => {
					const attachment = attachments.get(index);
					return attachment !== void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("details", {
						className: TrajectoryTable_module_css_default.attachmentDisclosure,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("summary", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: TrajectoryTable_module_css_default.sourceBlockLabel,
							children: t("block.label", {
								index: index + 1,
								type: block.type
							})
						}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: TrajectoryTable_module_css_default.attachmentName,
							title: attachment.name,
							children: attachment.name
						})] }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("pre", {
							className: TrajectoryTable_module_css_default.sourceBlockContent,
							children: block.content
						})]
					}, index) : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
						className: TrajectoryTable_module_css_default.sourceBlock,
						children: [block.callId !== void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
							type: "button",
							className: TrajectoryTable_module_css_default.sourceBlockJumpTarget,
							"aria-label": t("block.openSummary", { index: index + 1 }),
							title: t("block.openSummaryTitle"),
							onClick: () => {
								if (block.callId !== void 0) onOpenCall(block.callId);
							},
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
								className: TrajectoryTable_module_css_default.sourceBlockLabel,
								children: t("block.label", {
									index: index + 1,
									type: block.type
								})
							}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronRightOutlineRegular, {
								className: TrajectoryTable_module_css_default.sourceBlockJumpIcon,
								size: 12
							})]
						}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: TrajectoryTable_module_css_default.sourceBlockHeader,
							children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
								className: TrajectoryTable_module_css_default.sourceBlockLabel,
								children: t("block.label", {
									index: index + 1,
									type: block.type
								})
							})
						}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("pre", {
							className: TrajectoryTable_module_css_default.sourceBlockContent,
							children: block.content
						})]
					}, index);
				})
			});
		}
		function recordAttachments(blocks, t) {
			let imageIndex = 0;
			return (blocks ?? []).flatMap((block, index) => {
				const ref = block.attachment ?? block.file;
				if (ref === void 0) return [];
				if (block.attachment !== void 0) imageIndex += 1;
				return [{
					block,
					index,
					name: ref.name ?? t("attachment.imageName", { index: imageIndex }),
					metadata: [
						block.attachment?.mediaType ?? (0, _deepseek_ai_dsh_client_ui_primitives.fileExtension)(ref.name ?? "").toUpperCase(),
						(0, _deepseek_ai_dsh_client_ui_primitives.fileSizeText)(ref.bytes),
						...block.attachment === void 0 ? [] : [`${block.attachment.width} × ${block.attachment.height}`]
					].filter(Boolean).join(" · ")
				}];
			});
		}
		function RecordAttachments({ blocks, preview, renderImages, t }) {
			const attachments = recordAttachments(blocks, t);
			if (attachments.length === 0) return null;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("ul", {
				className: preview ? `${TrajectoryTable_module_css_default.attachments} ${TrajectoryTable_module_css_default.attachmentsPreview}` : TrajectoryTable_module_css_default.attachments,
				"aria-label": t("attachment.list"),
				children: attachments.map(({ block, index, name, metadata }) => /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("li", {
					className: TrajectoryTable_module_css_default.attachmentRow,
					children: [block.attachment !== void 0 ? renderImages({
						images: [{
							attachment: block.attachment,
							label: name
						}],
						align: "start",
						thumbnail: true
					}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: TrajectoryTable_module_css_default.attachmentIcon,
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.FileTypeIcon, { path: name })
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: TrajectoryTable_module_css_default.attachmentInfo,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: TrajectoryTable_module_css_default.attachmentName,
							title: name,
							children: name
						}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: TrajectoryTable_module_css_default.attachmentMetadata,
							children: metadata
						})]
					})]
				}, index))
			});
		}
		function AssistantToolCalls({ blocks, preview, onOpenCall, t }) {
			const calls = blocks?.filter((block) => block.type === "tool-call") ?? [];
			if (calls.length === 0) return null;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("ul", {
				className: preview ? `${TrajectoryTable_module_css_default.assistantToolCalls} ${TrajectoryTable_module_css_default.assistantToolCallsPreview}` : TrajectoryTable_module_css_default.assistantToolCalls,
				children: calls.map((call, index) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)("li", { children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
					type: "button",
					className: TrajectoryTable_module_css_default.assistantToolCallButton,
					title: t("block.openSummaryTitle"),
					onClick: () => {
						if (call.callId !== void 0) onOpenCall(call.callId);
					},
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("svg", {
						className: TrajectoryTable_module_css_default.assistantToolCallIcon,
						width: "12",
						height: "12",
						viewBox: "0 0 24 24",
						fill: "none",
						"aria-hidden": "true",
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
							d: "M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94z",
							stroke: "currentColor",
							strokeWidth: "1.8",
							strokeLinecap: "round",
							strokeLinejoin: "round"
						})
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
						className: TrajectoryTable_module_css_default.assistantToolCallText,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: TrajectoryTable_module_css_default.assistantToolCallName,
							children: call.toolName ?? t("details.toolCall")
						}), call.content !== "" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: TrajectoryTable_module_css_default.assistantToolCallArgs,
							children: call.content
						})]
					})]
				}) }, call.callId ?? index))
			});
		}
		function ToolGlyph() {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("svg", {
				className: TrajectoryTable_module_css_default.toolCatalogIcon,
				width: "12",
				height: "12",
				viewBox: "0 0 24 24",
				fill: "none",
				"aria-hidden": "true",
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
					d: "M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94z",
					stroke: "currentColor",
					strokeWidth: "1.8",
					strokeLinecap: "round",
					strokeLinejoin: "round"
				})
			});
		}
		function ToolCatalog({ tools, stringWrapping, t }) {
			if (tools.length === 0) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
				className: TrajectoryTable_module_css_default.noPayload,
				children: t("record.toolsMissing")
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: TrajectoryTable_module_css_default.toolCatalog,
				children: tools.map((tool, index) => /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("details", {
					className: TrajectoryTable_module_css_default.toolCatalogItem,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("summary", {
						className: TrajectoryTable_module_css_default.toolCatalogSummary,
						children: [
							/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronRightOutlineRegular, {
								className: TrajectoryTable_module_css_default.toolCatalogChevron,
								size: 12
							}),
							/* @__PURE__ */ (0, react_jsx_runtime.jsx)(ToolGlyph, {}),
							/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
								className: TrajectoryTable_module_css_default.toolCatalogName,
								children: tool.name
							}),
							/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
								className: TrajectoryTable_module_css_default.toolCatalogDescription,
								children: tool.description
							})
						]
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: TrajectoryTable_module_css_default.toolCatalogDefinition,
						children: [tool.description !== "" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
							className: TrajectoryTable_module_css_default.toolCatalogFullDescription,
							children: tool.description
						}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.JsonTree, {
							data: tool.parameters,
							stringWrapping,
							label: t("record.namedParametersJson", { name: tool.name }),
							labels: jsonTreeLabels(t),
							className: TrajectoryTable_module_css_default.toolCatalogTree
						})]
					})]
				}, `${tool.name}:${index}`))
			});
		}
		function promptDiffLines(before, after) {
			return structuredPatch("", "", before, after, void 0, void 0, { context: 3 }).hunks.flatMap((hunk, hunkIndex) => [
				...hunkIndex === 0 ? [] : [{
					kind: "meta",
					text: ""
				}],
				{
					kind: "meta",
					text: `@@ -${hunk.oldStart},${hunk.oldLines} +${hunk.newStart},${hunk.newLines} @@`
				},
				...hunk.lines.flatMap((line) => {
					if (line.startsWith("\\")) return [];
					if (line.startsWith("+")) return [{
						kind: "added",
						text: line
					}];
					if (line.startsWith("-")) return [{
						kind: "removed",
						text: line
					}];
					return [{
						kind: "context",
						text: line
					}];
				})
			]);
		}
		function PromptDiffSection({ title, before, after }) {
			const lines = promptDiffLines(before, after);
			if (lines.length === 0) return null;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
				className: TrajectoryTable_module_css_default.promptDiffSection,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("h3", {
					className: TrajectoryTable_module_css_default.promptDiffTitle,
					children: title
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("pre", {
					className: TrajectoryTable_module_css_default.promptDiff,
					children: lines.map((line, index) => /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
						className: TrajectoryTable_module_css_default[`promptDiffLine${line.kind}`],
						children: [line.text || " ", "\n"]
					}, index))
				})]
			});
		}
		function SystemPromptDiff({ before, after, t }) {
			const toolsBefore = JSON.stringify(before.tools, null, 2);
			const toolsAfter = JSON.stringify(after.tools, null, 2);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: TrajectoryTable_module_css_default.promptDiffSections,
				children: [before.system !== after.system && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(PromptDiffSection, {
					title: t("record.systemPrompt"),
					before: before.system,
					after: after.system
				}), toolsBefore !== toolsAfter && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(PromptDiffSection, {
					title: t("record.tools"),
					before: toolsBefore,
					after: toolsAfter
				})]
			});
		}
		function ToolOutputBlocks({ blocks, error, errorDetail, preview, renderImages }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: [
					TrajectoryTable_module_css_default.resultBlocks,
					preview ? TrajectoryTable_module_css_default.resultBlocksPreview : void 0,
					error ? TrajectoryTable_module_css_default.errorPayload : void 0
				].filter((value) => value !== void 0).join(" "),
				children: [error && errorDetail !== void 0 && errorDetail !== "" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("pre", {
					className: TrajectoryTable_module_css_default.resultBlockText,
					children: errorDetail
				}), blocks.map((block, index) => block.attachment !== void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
					className: TrajectoryTable_module_css_default.messageImages,
					children: renderImages({
						images: [{ attachment: block.attachment }],
						align: "start"
					})
				}, index) : block.content !== "" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("pre", {
					className: TrajectoryTable_module_css_default.resultBlockText,
					children: block.content
				}, index) : null)]
			});
		}
		function MarkdownRecordContent({ record, rendered, preview = false, thinkingExpanded, onThinkingExpandedChange, onOpenCall, renderImages, t }) {
			if (record.cell.sourceBlocks?.length && record.cell.sourceBlocks.every((block) => block.type === "tool-addition" || block.type === "tool-removal")) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("pre", {
				className: `${TrajectoryTable_module_css_default.payload} ${TrajectoryTable_module_css_default.toolUpdatePayload}`,
				children: record.cell.inputDetail
			});
			if (!rendered && record.cell.sourceBlocks && record.cell.sourceBlocks.length > 0) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(SourceBlocks, {
				blocks: record.cell.sourceBlocks,
				onOpenCall,
				t
			});
			if (record.cell.thinkingDetail) {
				if (!rendered) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(MarkdownFragment, {
					text: [record.cell.thinkingDetail, record.cell.outputDetail].filter((value) => value !== void 0 && value !== "").join("\n\n"),
					rendered: false,
					preview,
					t
				});
				return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: `${TrajectoryTable_module_css_default.assistantContent} ${TrajectoryTable_module_css_default.assistantContentRendered}`,
					children: [
						/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							className: preview && !record.cell.outputDetail ? `${TrajectoryTable_module_css_default.thinkingQuote} ${TrajectoryTable_module_css_default.thinkingQuoteOnlyPreview}` : TrajectoryTable_module_css_default.thinkingQuote,
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
								type: "button",
								className: TrajectoryTable_module_css_default.thinkingToggle,
								"aria-expanded": thinkingExpanded,
								onClick: () => {
									onThinkingExpandedChange(!thinkingExpanded);
								},
								children: [t("record.thinking"), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronRightOutlineRegular, {
									className: TrajectoryTable_module_css_default.thinkingChevron,
									size: 12
								})]
							}), thinkingExpanded && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(MarkdownFragment, {
								text: record.cell.thinkingDetail,
								rendered,
								preview,
								variant: "compact",
								t
							})]
						}),
						record.cell.outputDetail && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: TrajectoryTable_module_css_default.assistantOutput,
							children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(MarkdownFragment, {
								text: record.cell.outputDetail,
								rendered,
								preview,
								t
							})
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)(AssistantToolCalls, {
							blocks: record.cell.sourceBlocks,
							preview,
							onOpenCall,
							t
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)(RecordAttachments, {
							blocks: record.cell.sourceBlocks,
							preview,
							renderImages,
							t
						})
					]
				});
			}
			const source = markdownSource(record);
			const hasAttachments = record.cell.sourceBlocks?.some((block) => block.attachment !== void 0 || block.file !== void 0) === true;
			const hasToolCalls = record.cell.kind === "message" && record.cell.sourceBlocks?.some((block) => block.type === "tool-call") === true;
			if (!source && !hasAttachments && !hasToolCalls) {
				const emptyLabel = isToolCallOnly(record.cell, t) ? t("record.toolCallOnly") : record.cell.text || t("record.noContent");
				return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
					className: TrajectoryTable_module_css_default.noPayload,
					children: emptyLabel
				});
			}
			if (!rendered || !hasAttachments && !hasToolCalls) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(MarkdownFragment, {
				text: source ?? "",
				rendered,
				preview,
				t
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [
				source && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(MarkdownFragment, {
					text: source,
					rendered: true,
					preview,
					t
				}),
				record.cell.kind === "message" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(AssistantToolCalls, {
					blocks: record.cell.sourceBlocks,
					preview,
					onOpenCall,
					t
				}),
				/* @__PURE__ */ (0, react_jsx_runtime.jsx)(RecordAttachments, {
					blocks: record.cell.sourceBlocks,
					preview,
					renderImages,
					t
				})
			] });
		}
		function RecordTiming({ record, preview = false, t }) {
			return record.cell.kind === "message" && record.cell.assistantMetrics !== void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(AssistantTimingPanel, {
				metrics: record.cell.assistantMetrics,
				t
			}) : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("dl", {
				className: TrajectoryTable_module_css_default.overview,
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("timing.started") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(StartedAtValue, {
						timestamp: record.cell.startedAt ?? null,
						t
					})] }),
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("timing.duration") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: formatElapsedSeconds(record.cell.timeSeconds, t) })] }),
					!preview && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("timing.source") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: record.cell.timeSeconds === null ? t("timing.notAvailable") : t("timing.sessionTimestamps") })] })
				]
			});
		}
		function RequestTiming({ assistant, anchor, request, preview = false, t }) {
			if (assistant !== void 0) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(RecordTiming, {
				record: assistant,
				preview,
				t
			});
			if (request?.startedAt !== void 0) {
				const duration = request.completedAt === null || request.completedAt === void 0 ? null : Math.max(0, (request.completedAt - request.startedAt) / 1e3);
				return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("dl", {
					className: TrajectoryTable_module_css_default.overview,
					children: [
						/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("timing.started") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(StartedAtValue, {
							timestamp: request.startedAt,
							t
						})] }),
						/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("timing.duration") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: formatElapsedSeconds(duration, t) })] }),
						!preview && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("timing.source") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: duration === null ? t("timing.sessionTimestampsRunning") : t("timing.sessionTimestamps") })] })
					]
				});
			}
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("dl", {
				className: TrajectoryTable_module_css_default.overview,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("timing.started") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(StartedAtValue, {
					timestamp: anchor?.cell.startedAt ?? null,
					t
				})] }), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("timing.duration") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: formatElapsedSeconds(null, t) })] })]
			});
		}
		function RecordPayload({ record, direction, preview = false, renderImages, stringWrapping, t }) {
			const value = direction === "input" ? record.cell.inputDetail : record.cell.outputDetail;
			const missing = direction === "input" ? t("record.noPayload") : t("record.noResult");
			if (!value) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
				className: TrajectoryTable_module_css_default.noPayload,
				children: missing
			});
			const error = direction === "output" && record.cell.isError === true;
			const payloadClass = preview ? TrajectoryTable_module_css_default.jsonPreview : TrajectoryTable_module_css_default.jsonPayload;
			const payloadClassName = error ? `${payloadClass} ${TrajectoryTable_module_css_default.errorPayload}` : payloadClass;
			const json = parseJsonContainer(value);
			if (direction === "output" && record.cell.outputBlocks?.length === 1 && record.cell.outputBlocks[0]?.type === "text" && json !== void 0) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.JsonTree, {
				data: json,
				stringWrapping,
				collapsedStringLines: preview ? 3 : 12,
				label: t("record.resultJson"),
				labels: jsonTreeLabels(t),
				className: payloadClassName
			});
			if (direction === "output" && record.cell.outputBlocks?.some((block) => block.attachment !== void 0 || block.content !== "") === true) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ToolOutputBlocks, {
				blocks: record.cell.outputBlocks,
				error,
				errorDetail: error ? value : void 0,
				preview,
				renderImages
			});
			if (direction === "input" && (record.cell.kind === "user" || record.cell.kind === "context") || direction === "output" && record.cell.kind === "message") return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: [preview ? TrajectoryTable_module_css_default.markdownPreview : TrajectoryTable_module_css_default.markdownPayload, error ? TrajectoryTable_module_css_default.errorPayload : void 0].filter((className) => className !== void 0).join(" "),
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.MarkdownText, {
					text: value,
					labels: markdownLabels(t)
				})
			});
			if (json !== void 0) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.JsonTree, {
				data: json,
				stringWrapping,
				collapsedStringLines: preview ? 3 : 12,
				label: t(direction === "input" ? "record.payloadJson" : "record.outputJson"),
				labels: jsonTreeLabels(t),
				className: payloadClassName
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("pre", {
				className: [
					TrajectoryTable_module_css_default.payload,
					preview ? TrajectoryTable_module_css_default.payloadPreview : void 0,
					error ? TrajectoryTable_module_css_default.errorPayload : void 0,
					value === t("record.noOutput") ? TrajectoryTable_module_css_default.noOutputText : void 0
				].filter((value) => value !== void 0).join(" "),
				children: value
			});
		}
		function RecordSchema({ record, preview = false, stringWrapping, t }) {
			if (!record.cell.schemaDetail) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
				className: TrajectoryTable_module_css_default.noPayload,
				children: t("record.schemaUnavailable")
			});
			const schema = parseToolSchema(record.cell.schemaDetail);
			if (schema !== void 0) return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: preview ? `${TrajectoryTable_module_css_default.schema} ${TrajectoryTable_module_css_default.schemaPreview}` : TrajectoryTable_module_css_default.schema,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("header", {
					className: TrajectoryTable_module_css_default.schemaIntro,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("h3", {
						className: TrajectoryTable_module_css_default.schemaName,
						children: schema.name
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
						className: TrajectoryTable_module_css_default.schemaDescription,
						children: schema.description
					})]
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
					className: TrajectoryTable_module_css_default.schemaParameters,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("h4", {
						className: TrajectoryTable_module_css_default.schemaParametersTitle,
						children: t("record.parameters")
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.JsonTree, {
						data: schema.parameters,
						stringWrapping,
						collapsedStringLines: preview ? 3 : 12,
						label: t("record.namedParametersJson", { name: schema.name }),
						labels: jsonTreeLabels(t),
						className: TrajectoryTable_module_css_default.schemaTree
					})]
				})]
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("pre", {
				className: `${TrajectoryTable_module_css_default.payload} ${preview ? TrajectoryTable_module_css_default.payloadPreview : ""}`,
				children: record.cell.schemaDetail
			});
		}
		function parseToolSchema(value) {
			try {
				const parsed = JSON.parse(value);
				if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return void 0;
				const schema = parsed;
				if (typeof schema.name !== "string" || typeof schema.description !== "string" || typeof schema.parameters !== "object" || schema.parameters === null || Array.isArray(schema.parameters)) return void 0;
				return {
					name: schema.name,
					description: schema.description,
					parameters: schema.parameters
				};
			} catch {
				return;
			}
		}
		function parseJsonContainer(value) {
			try {
				const parsed = JSON.parse(value);
				return typeof parsed === "object" && parsed !== null ? parsed : void 0;
			} catch {
				return;
			}
		}
		function InspectorCopyButton({ text, label, t }) {
			const [state, setState] = (0, react.useState)("idle");
			(0, react.useEffect)(() => {
				if (state === "idle") return;
				const timer = setTimeout(() => {
					setState("idle");
				}, 1500);
				return () => {
					clearTimeout(timer);
				};
			}, [state]);
			const title = state === "idle" ? label : t(state === "copied" ? "copied" : "copy.failed");
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
				type: "button",
				className: TrajectoryTable_module_css_default.programAction,
				"data-state": state,
				"aria-label": title,
				title,
				onClick: () => {
					(0, _deepseek_ai_dsh_client_ui_primitives.writeClipboard)(text).then((ok) => {
						setState(ok ? "copied" : "failed");
					});
				},
				children: state === "copied" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconCheckOutlineRegular, { size: 12 }) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconCopyOutlineRegular, { size: 12 })
			});
		}
		function ProgramInput({ program, initialWrapped, stringWrapping, onOpen, t }) {
			const contentsId = (0, react.useId)();
			const [wrapped, setWrapped] = (0, react.useState)(initialWrapped);
			const [showJson, setShowJson] = (0, react.useState)(false);
			const actions = /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
				className: TrajectoryTable_module_css_default.programActions,
				children: [
					!showJson && program.language !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: TrajectoryTable_module_css_default.programLanguage,
						children: program.language
					}),
					!showJson && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
						type: "button",
						className: TrajectoryTable_module_css_default.programAction,
						"aria-label": t("record.wrapLines"),
						title: t("record.wrapLines"),
						"aria-pressed": wrapped,
						"aria-controls": contentsId,
						onClick: () => {
							const next = !wrapped;
							setWrapped(next);
							stringWrapping?.setDefault(next);
						},
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconWrapLinesOutlineRegular, { size: 12 })
					}),
					onOpen === void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
						type: "button",
						className: TrajectoryTable_module_css_default.programAction,
						"aria-label": t("code.originalJson"),
						title: t("code.originalJson"),
						"aria-pressed": showJson,
						"aria-controls": contentsId,
						onClick: () => {
							setShowJson((value) => !value);
						},
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("svg", {
							width: "12",
							height: "12",
							viewBox: "0 0 16 16",
							fill: "none",
							stroke: "currentColor",
							strokeWidth: "1.5",
							strokeLinecap: "round",
							strokeLinejoin: "round",
							"aria-hidden": "true",
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", { d: "M6 2H5a2 2 0 0 0-2 2v2a2 2 0 0 1-2 2 2 2 0 0 1 2 2v2a2 2 0 0 0 2 2h1" }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", { d: "M10 2h1a2 2 0 0 1 2 2v2a2 2 0 0 0 2 2 2 2 0 0 0-2 2v2a2 2 0 0 1-2 2h-1" })]
						})
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)(InspectorCopyButton, {
						text: showJson ? program.rawInput : program.source,
						label: t(showJson ? "copy.json" : "code.copySource"),
						t
					})
				]
			});
			const body = /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				id: contentsId,
				className: TrajectoryTable_module_css_default.programContent,
				"data-wrap": wrapped,
				children: showJson ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.JsonTree, {
					data: program.arguments,
					label: t("record.parametersJson"),
					labels: jsonTreeLabels(t),
					stringWrapping
				}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.CodeBlock, {
					code: program.source,
					lang: program.language,
					lineNumbers: true,
					showHeader: false,
					className: TrajectoryTable_module_css_default.programSource,
					copyLabel: t("code.copySource"),
					copiedLabel: t("copied")
				})
			});
			return onOpen === void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
				className: TrajectoryTable_module_css_default.programPanel,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("header", {
					className: TrajectoryTable_module_css_default.overviewHeading,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: t("code.source") }), actions]
				}), body]
			}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(OverviewSection, {
				label: t("code.source"),
				onOpen,
				actions,
				children: body
			});
		}
		function ProgramOutput({ record, stringWrapping, onOpen, t }) {
			const output = record.cell.outputDetail;
			const json = output === void 0 || record.cell.isError === true ? void 0 : parseJsonContainer(output);
			const actions = output === void 0 ? void 0 : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
				className: TrajectoryTable_module_css_default.programActions,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(InspectorCopyButton, {
					text: output,
					label: t("code.copyOutput"),
					t
				})
			});
			const body = /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: TrajectoryTable_module_css_default.programContent,
				children: output === void 0 || output === "" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
					className: TrajectoryTable_module_css_default.noPayload,
					children: t(stateOf(record) === "running" ? "code.running" : "record.noOutput")
				}) : json !== void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.JsonTree, {
					data: json,
					label: t("record.outputJson"),
					labels: jsonTreeLabels(t),
					stringWrapping,
					collapsedStringLines: onOpen === void 0 ? 12 : 3
				}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("pre", {
					className: `${TrajectoryTable_module_css_default.programOutput} ${record.cell.isError === true ? TrajectoryTable_module_css_default.programError : ""}`,
					children: output
				})
			});
			return onOpen === void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
				className: TrajectoryTable_module_css_default.programPanel,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("header", {
					className: TrajectoryTable_module_css_default.overviewHeading,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: t("code.output") }), actions]
				}), body]
			}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(OverviewSection, {
				label: t("code.output"),
				onOpen,
				actions,
				children: body
			});
		}
		function OverviewSection({ label, onOpen, actions, children }) {
			const previewRef = (0, react.useRef)(null);
			const [hasMore, setHasMore] = (0, react.useState)(false);
			const measureOverflow = (0, react.useCallback)((preview) => {
				setHasMore(preview.scrollHeight - preview.clientHeight - preview.scrollTop > 1);
			}, []);
			(0, react.useLayoutEffect)(() => {
				const preview = previewRef.current;
				const measure = () => {
					measureOverflow(preview);
				};
				measure();
				if (typeof ResizeObserver === "undefined") return;
				const observer = new ResizeObserver(measure);
				observer.observe(preview);
				for (const child of preview.children) observer.observe(child);
				return () => {
					observer.disconnect();
				};
			}, [children, measureOverflow]);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
				className: TrajectoryTable_module_css_default.overviewSection,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("h3", {
					className: TrajectoryTable_module_css_default.overviewHeading,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
						type: "button",
						className: TrajectoryTable_module_css_default.overviewTitle,
						onClick: onOpen,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: label }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronRightOutlineRegular, {
							className: TrajectoryTable_module_css_default.overviewTitleIcon,
							size: 12
						})]
					}), actions]
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
					ref: previewRef,
					className: `${TrajectoryTable_module_css_default.overviewPreview} ${TrajectoryTable_module_css_default.summaryScrollRegion}`,
					"data-summary-scroll-region": "",
					"data-scroll-more": hasMore || void 0,
					onScroll: (event) => {
						measureOverflow(event.currentTarget);
					},
					children
				})]
			});
		}
		/**
		* Render trajectory events as a dense ledger with turn and step separators.
		* Clicking ledger whitespace clears the active record or request selection.
		* @param props - Grouped trajectory data and whole-ledger fold state.
		* @returns The ledger and an optional local record inspector.
		*/
		function TrajectoryTable({ t, stringWrapping, renderImages, requestNumbers: sessionRequestNumbers, turns, streamingCells = [], timelineFocusIndexes = null, searchMatchIndexes = null, onSelectedIndexChange, onRecordSelect, recordSelection = null, recordFocus = null, historyLoading = false, olderHistoryLoading = false, historyStartSeq, hasOlderRecords = false, onLoadOlder, onClearSelection, collapsedTurns, onToggleTurn, collapsedAssistants, onToggleAssistant, inspectCallId = null, onInspectApplied }) {
			const [selectedRecordId, setSelectedRecordId] = (0, react.useState)(null);
			const [selectedRequest, setSelectedRequest] = (0, react.useState)(null);
			const [activeTab, setActiveTab] = (0, react.useState)("overview");
			const [codeWrappingOnOpen, setCodeWrappingOnOpen] = (0, react.useState)(false);
			const [thinkingDisclosure, setThinkingDisclosure] = (0, react.useState)();
			const [detailsWidth, setDetailsWidth] = (0, react.useState)(null);
			const [toolRequestOffset, setToolRequestOffset] = (0, react.useState)(null);
			const detailsResizeDrag = (0, react.useRef)(null);
			const appliedRecordSelection = (0, react.useRef)(null);
			const appliedRecordFocus = (0, react.useRef)(null);
			const tabHistory = (0, react.useRef)(new Set(["overview"]));
			const rootRef = (0, react.useRef)(null);
			const tablePaneRef = (0, react.useRef)(null);
			const followsTableTail = (0, react.useRef)(false);
			const tableScrollInitialized = (0, react.useRef)(false);
			const [tableScrollReady, setTableScrollReady] = (0, react.useState)(false);
			const pendingScrollRecordId = (0, react.useRef)(null);
			const loadingOlder = (0, react.useRef)(false);
			const [olderLoading, setOlderLoading] = (0, react.useState)(false);
			const olderLoadAnchor = (0, react.useRef)(null);
			const allRecords = (0, react.useMemo)(() => flattenRecords(turns), [turns]);
			const streamingCellsByIndex = (0, react.useMemo)(() => new Map(streamingCells.map((cell) => [cell.index, cell])), [streamingCells]);
			const currentRecord = (0, react.useCallback)((record) => {
				const cell = streamingCellsByIndex.get(record.cell.index);
				return cell === void 0 ? record : {
					...record,
					cell
				};
			}, [streamingCellsByIndex]);
			const selectedTemplate = (0, react.useMemo)(() => selectedRecordId === null ? void 0 : allRecords.find((record) => trajectoryRecordId(record.cell) === selectedRecordId), [allRecords, selectedRecordId]);
			const selected = selectedTemplate === void 0 ? void 0 : currentRecord(selectedTemplate);
			const selectedProgram = selected === void 0 ? void 0 : codeProgram(selected.cell);
			const thinkingExpanded = thinkingDisclosure?.recordId === selectedRecordId ? thinkingDisclosure.expanded : selected?.cell.kind === "message" && Boolean(selected.cell.thinkingDetail?.trim());
			const setThinkingExpanded = (expanded) => {
				if (selectedRecordId !== null) setThinkingDisclosure({
					recordId: selectedRecordId,
					expanded
				});
			};
			const selectedIndex = selected?.cell.index ?? null;
			(0, react.useEffect)(() => {
				onSelectedIndexChange?.(selectedIndex);
			}, [onSelectedIndexChange, selectedIndex]);
			const requestGroups = (0, react.useMemo)(() => new Set((sessionRequestNumbers ?? []).map((request) => requestKey(request.turn, request.group))), [sessionRequestNumbers]);
			const requestBoundaries = (0, react.useMemo)(() => indexRequestBoundaries(allRecords, requestGroups), [allRecords, requestGroups]);
			const requestNumbers = (0, react.useMemo)(() => indexRequestNumbers(sessionRequestNumbers), [sessionRequestNumbers]);
			const records = (0, react.useMemo)(() => {
				if (searchMatchIndexes !== null) return filterRecords(allRecords, searchMatchIndexes);
				const turnRecords = collapsedTurns.size === 0 ? allRecords : collapseTurnRecords(allRecords, collapsedTurns, requestGroups, t);
				return collapsedAssistants.size === 0 ? turnRecords : collapseAssistantRecords(turnRecords, collapsedAssistants, t);
			}, [
				allRecords,
				collapsedAssistants,
				collapsedTurns,
				requestGroups,
				searchMatchIndexes,
				t
			]);
			const projectedVirtualRows = (0, react.useMemo)(() => groupTrajectoryVirtualRows(records), [records]);
			const virtualRowStructure = useStableVirtualRowStructure(projectedVirtualRows);
			const virtualizationEnabled = hasOlderRecords || records.length > VIRTUALIZATION_THRESHOLD;
			const virtualScrollMargin = hasOlderRecords ? HISTORY_LOAD_ROW_HEIGHT_PX : 0;
			const estimateVirtualRowSize = (0, react.useCallback)((index) => virtualRowStructure[index]?.height ?? 30, [virtualRowStructure]);
			const getVirtualRowKey = (0, react.useCallback)((index) => virtualRowStructure[index]?.key ?? index, [virtualRowStructure]);
			const getTableScrollElement = (0, react.useCallback)(() => tablePaneRef.current, []);
			const rowVirtualizer = useVirtualizer({
				count: virtualizationEnabled ? virtualRowStructure.length : 0,
				enabled: virtualizationEnabled,
				estimateSize: estimateVirtualRowSize,
				getItemKey: getVirtualRowKey,
				getScrollElement: getTableScrollElement,
				initialRect: {
					width: 0,
					height: VIRTUAL_INITIAL_VIEWPORT_HEIGHT_PX
				},
				anchorTo: "end",
				overscan: VIRTUAL_OVERSCAN_ROWS,
				scrollMargin: virtualScrollMargin,
				scrollEndThreshold: BOTTOM_FOLLOW_THRESHOLD_PX,
				followOnAppend: "auto"
			});
			const virtualIndexByRecordId = (0, react.useMemo)(() => {
				const indexes = /* @__PURE__ */ new Map();
				for (const [virtualIndex, row] of projectedVirtualRows.entries()) for (const entry of row.entries) if (entry.record.collapsedSummary === void 0) indexes.set(trajectoryRecordId(entry.record.cell), virtualIndex);
				return indexes;
			}, [projectedVirtualRows]);
			const virtualItems = virtualizationEnabled ? rowVirtualizer.getVirtualItems() : [];
			const virtualTop = Math.max(0, (virtualItems[0]?.start ?? 0) - virtualScrollMargin);
			const virtualBottom = virtualItems.length === 0 ? 0 : Math.max(0, rowVirtualizer.getTotalSize() + virtualScrollMargin - (virtualItems.at(-1)?.end ?? 0));
			const renderedRecords = virtualizationEnabled ? virtualItems.flatMap((item) => {
				const row = projectedVirtualRows[item.index];
				if (row === void 0) return [];
				return row.entries.map((entry, entryIndex) => ({
					record: currentRecord(entry.record),
					position: entry.logicalIndex,
					terminalRequestBoundary: entry.record.cell.requestOnly === true && row.entries.at(-1)?.record.cell.requestOnly === true && entryIndex === row.entries.length - 1
				}));
			}) : records.map((record, position) => ({
				record: currentRecord(record),
				position,
				terminalRequestBoundary: record.cell.requestOnly === true && position === records.length - 1
			}));
			const requestBoundaryRuns = (0, react.useMemo)(() => indexRequestBoundaryRuns(records, requestGroups), [records, requestGroups]);
			const selectedPrompt = selected?.cell.kind === "system" ? selected.cell.promptDetail : void 0;
			const selectedPreviousPrompt = selected?.cell.kind === "system" ? selected.cell.previousPromptDetail : void 0;
			const selectedSystemPrompt = selectedPrompt?.system ?? selected?.cell.systemPromptDetail;
			const promptSelected = selectedSystemPrompt !== void 0;
			const selectedState = selected === void 0 ? void 0 : stateOf(selected);
			const selectedRequestInfo = selectedRequest === null ? void 0 : sessionRequestNumbers?.find((request) => requestIdentity(request) === selectedRequest.identity);
			const selectedRequestRecords = (0, react.useMemo)(() => selectedRequestInfo === void 0 ? [] : allRecords.filter((record) => record.turn === selectedRequestInfo.turn && record.group === selectedRequestInfo.group), [allRecords, selectedRequestInfo]).map(currentRecord);
			const selectedRequestAssistant = selectedRequestRecords.find((record) => record.cell.kind === "message");
			const selectedRequestAnchor = selectedRequestAssistant ?? selectedRequestRecords[0];
			const selectedRequestNumber = selectedRequestInfo?.number;
			const selectedRequestState = selectedRequestInfo === void 0 ? void 0 : selectedRequestInfo.status ?? (selectedRequestAssistant?.cell.assistantMetrics?.completedTime === null ? "running" : selectedRequestAssistant === void 0 && selectedRequestRecords.some((record) => stateOf(record) === "running") ? "running" : "complete");
			const selectedRequestToolCalls = selectedRequestRecords.filter((record) => record.cell.kind === "tool").length;
			const selectedRequestSubtoolCalls = selectedRequestRecords.filter((record) => record.cell.kind === "subtool").length;
			const selectedRequestResultTemplate = selectedRequestInfo?.resultSeq === void 0 ? selectedRequestAssistant : allRecords.find((record) => record.cell.sourceSeq === selectedRequestInfo.resultSeq);
			const selectedRequestResult = selectedRequestResultTemplate === void 0 ? void 0 : currentRecord(selectedRequestResultTemplate);
			const selectedRequestUsage = selectedRequestInfo?.usage ?? (selectedRequestAssistant === void 0 ? void 0 : {
				...selectedRequestAssistant.cell.input === void 0 ? {} : { input: selectedRequestAssistant.cell.input },
				...selectedRequestAssistant.cell.cacheRead === void 0 ? {} : { cacheRead: selectedRequestAssistant.cell.cacheRead },
				...selectedRequestAssistant.cell.cacheWrite === void 0 ? {} : { cacheWrite: selectedRequestAssistant.cell.cacheWrite },
				...selectedRequestAssistant.cell.output === void 0 ? {} : { output: selectedRequestAssistant.cell.output },
				...selectedRequestAssistant.cell.think === void 0 ? {} : { reasoning: selectedRequestAssistant.cell.think }
			});
			const selectedRequestCumulativeUsage = selectedRequestInfo?.cumulativeUsage ?? selectedRequestUsage;
			const selectedRequestOptions = selectedRequestInfo?.requestConfig;
			const activeTurn = selectedRequestInfo === void 0 ? selected?.turn : selectedRequestInfo.turn;
			const activeSection = selectedRequestInfo === void 0 ? selected?.section : selectedRequestRecords[0]?.section;
			const selectedTabs = selectedRequestInfo !== void 0 ? REQUEST_TABS.filter((tab) => tab.id !== "options" || selectedRequestOptions !== void 0) : selected === void 0 ? [] : detailTabs(selected);
			const selectedParents = selected === void 0 ? {} : parentRecords(allRecords, selected);
			const selectedParentMessage = selectedParents.message;
			const selectedParentTool = selectedParents.tool;
			const selectedAssistantRequest = selected?.cell.kind === "message" ? requestNumbers.get(requestKey(selected.turn, selected.group)) : void 0;
			const selectedAssistantRequestInfo = selectedAssistantRequest === void 0 ? void 0 : sessionRequestNumbers?.find((request) => request.number === selectedAssistantRequest);
			const selectedAssistantRequestTarget = selectedAssistantRequestInfo === void 0 ? void 0 : { identity: requestIdentity(selectedAssistantRequestInfo) };
			const hasSelectedHierarchy = selectedAssistantRequestTarget !== void 0 || selectedParents.message !== void 0 || selectedParents.tool !== void 0;
			const splitStyle = toolRequestOffset === null ? void 0 : { "--trajectory-tool-request-width": `calc(58cqw - ${toolRequestOffset}px)` };
			const activateTab = (tab) => {
				setCodeWrappingOnOpen(stringWrapping?.getDefault() ?? false);
				tabHistory.current.delete(tab);
				tabHistory.current.add(tab);
				setActiveTab(tab);
			};
			const clearInspectorSelection = () => {
				setSelectedRecordId(null);
				setSelectedRequest(null);
			};
			const clearAllSelections = () => {
				clearInspectorSelection();
				onClearSelection?.();
			};
			const selectRecord = (0, react.useCallback)((index) => {
				setCodeWrappingOnOpen(stringWrapping?.getDefault() ?? false);
				const record = allRecords.find((candidate) => candidate.cell.index === index);
				onRecordSelect?.(index);
				setSelectedRequest(null);
				setSelectedRecordId(record === void 0 ? null : trajectoryRecordId(record.cell));
				if (record === void 0) return;
				const tabs = detailTabs(record);
				const available = new Set(tabs.map((tab) => tab.id));
				setActiveTab([...tabHistory.current].reverse().find((tab) => available.has(tab)) ?? tabs[0]?.id ?? "overview");
			}, [
				allRecords,
				onRecordSelect,
				stringWrapping
			]);
			(0, react.useEffect)(() => {
				if (recordSelection === null || appliedRecordSelection.current === recordSelection) return;
				appliedRecordSelection.current = recordSelection;
				selectRecord(recordSelection.index);
				const record = allRecords.find((candidate) => candidate.cell.index === recordSelection.index);
				pendingScrollRecordId.current = record === void 0 ? null : trajectoryRecordId(record.cell);
			}, [
				allRecords,
				recordSelection,
				selectRecord
			]);
			(0, react.useEffect)(() => {
				if (recordFocus === null || appliedRecordFocus.current === recordFocus) return;
				appliedRecordFocus.current = recordFocus;
				const record = allRecords.find((candidate) => candidate.cell.index === recordFocus.index);
				pendingScrollRecordId.current = record === void 0 ? null : trajectoryRecordId(record.cell);
			}, [allRecords, recordFocus]);
			const selectRequest = (request, tab = "overview") => {
				setSelectedRecordId(null);
				setSelectedRequest(request);
				activateTab(tab);
			};
			const openRecordSummary = (target) => {
				const targetAt = allRecords.findIndex((record) => record.cell.index === target.cell.index);
				if (target.turn !== null && collapsedTurns.has(target.turn)) onToggleTurn(target.turn);
				if (target.cell.kind === "tool" || target.cell.kind === "subtool") for (let i = targetAt - 1; i >= 0; i--) {
					const candidate = allRecords[i];
					if (candidate === void 0 || candidate.turn !== target.turn) break;
					if (candidate.cell.kind !== "message") continue;
					const assistantId = trajectoryRecordId(candidate.cell);
					if (collapsedAssistants.has(assistantId)) onToggleAssistant(assistantId);
					break;
				}
				setSelectedRequest(null);
				setSelectedRecordId(trajectoryRecordId(target.cell));
				activateTab("overview");
			};
			const openCallSummary = (callId) => {
				const target = allRecords.find((record) => record.cell.callId === callId);
				if (target !== void 0) openRecordSummary(target);
			};
			const openRecordSummaryRef = (0, react.useRef)(openRecordSummary);
			openRecordSummaryRef.current = openRecordSummary;
			(0, react.useEffect)(() => {
				if (inspectCallId === null) return;
				const target = flattenRecords(turns).find((record) => record.cell.callId === inspectCallId);
				if (target === void 0) return;
				openRecordSummaryRef.current(target);
				pendingScrollRecordId.current = trajectoryRecordId(target.cell);
				onInspectApplied?.();
			}, [
				inspectCallId,
				turns,
				onInspectApplied
			]);
			(0, react.useEffect)(() => {
				const id = pendingScrollRecordId.current;
				if (id === null) return;
				const position = records.findIndex((record) => trajectoryRecordId(record.cell) === id && record.collapsedSummary === void 0);
				if (position === -1) return;
				if (virtualizationEnabled) {
					const virtualIndex = virtualIndexByRecordId.get(id);
					if (virtualIndex === void 0) return;
					pendingScrollRecordId.current = null;
					followsTableTail.current = false;
					rowVirtualizer.scrollToIndex(virtualIndex, {
						behavior: "smooth",
						align: "center"
					});
					return;
				}
				pendingScrollRecordId.current = null;
				followsTableTail.current = false;
				const recordIndex = records[position]?.cell.index;
				const row = recordIndex === void 0 ? null : rootRef.current?.querySelector(`tr[data-record-index="${recordIndex}"]`);
				/* v8 ignore next -- jsdom lacks scrollIntoView; browsers always have it. */
				if (row !== void 0 && row !== null && typeof row.scrollIntoView === "function") row.scrollIntoView({
					behavior: "smooth",
					block: "center"
				});
			}, [
				records,
				rowVirtualizer,
				virtualIndexByRecordId,
				virtualizationEnabled
			]);
			(0, react.useEffect)(() => {
				if (timelineFocusIndexes === null || timelineFocusIndexes.size === 0) return;
				const focusedPositions = records.flatMap((record, position) => record.collapsedSummary === void 0 && record.cell.requestOnly !== true && timelineFocusIndexes.has(record.cell.index) ? [position] : []);
				const first = focusedPositions.at(0);
				const last = focusedPositions.at(-1);
				if (first === void 0 || last === void 0) return;
				if (!virtualizationEnabled) {
					const ledger = rootRef.current;
					if (ledger === null) return;
					const focusedRows = [...ledger.querySelectorAll("tr[data-timeline-focus=\"inside\"]")];
					const firstRow = focusedRows.at(0);
					const lastRow = focusedRows.at(-1);
					if (firstRow === void 0 || lastRow === void 0) return;
					const focusHeight = lastRow.getBoundingClientRect().bottom - firstRow.getBoundingClientRect().top;
					const target = focusHeight > ledger.clientHeight ? firstRow : focusedRows[Math.floor((focusedRows.length - 1) / 2)];
					/* v8 ignore next -- jsdom lacks scrollIntoView; browsers always have it. */
					if (target !== void 0 && typeof target.scrollIntoView === "function") {
						followsTableTail.current = false;
						target.scrollIntoView({
							behavior: "smooth",
							block: focusHeight > ledger.clientHeight ? "start" : "center"
						});
					}
					return;
				}
				const focusedVirtualIndexes = [...new Set(focusedPositions.flatMap((position) => {
					const record = records[position];
					if (record === void 0) return [];
					const virtualIndex = virtualIndexByRecordId.get(trajectoryRecordId(record.cell));
					return virtualIndex === void 0 ? [] : [virtualIndex];
				}))].sort((left, right) => left - right);
				const firstVirtual = focusedVirtualIndexes.at(0);
				const lastVirtual = focusedVirtualIndexes.at(-1);
				if (firstVirtual === void 0 || lastVirtual === void 0) return;
				const paneHeight = tablePaneRef.current?.clientHeight ?? 0;
				const focusHeight = projectedVirtualRows.slice(firstVirtual, lastVirtual + 1).reduce((height, row) => height + row.height, 0);
				followsTableTail.current = false;
				rowVirtualizer.scrollToIndex(focusHeight > paneHeight ? firstVirtual : focusedVirtualIndexes[Math.floor((focusedVirtualIndexes.length - 1) / 2)] ?? firstVirtual, {
					behavior: "smooth",
					align: focusHeight > paneHeight ? "start" : "center"
				});
			}, [
				projectedVirtualRows,
				records,
				rowVirtualizer,
				timelineFocusIndexes,
				virtualIndexByRecordId,
				virtualizationEnabled
			]);
			const requestOlder = (0, react.useCallback)((pane, requireTop) => {
				if (!hasOlderRecords || onLoadOlder === void 0 || loadingOlder.current || olderHistoryLoading || requireTop && pane.scrollTop > OLDER_LOAD_THRESHOLD_PX) return;
				loadingOlder.current = true;
				setOlderLoading(true);
				olderLoadAnchor.current = {
					historyStartSeq,
					scrollHeight: pane.scrollHeight,
					scrollTop: pane.scrollTop
				};
				onLoadOlder().then((advanced) => {
					if (!advanced) olderLoadAnchor.current = null;
				}).finally(() => {
					loadingOlder.current = false;
					setOlderLoading(false);
				});
			}, [
				hasOlderRecords,
				historyStartSeq,
				olderHistoryLoading,
				onLoadOlder
			]);
			(0, react.useLayoutEffect)(() => {
				const pane = tablePaneRef.current;
				if (pane === null) return;
				const anchor = olderLoadAnchor.current;
				if (anchor !== null && anchor.historyStartSeq !== historyStartSeq) {
					if (!virtualizationEnabled) pane.scrollTop = anchor.scrollTop + pane.scrollHeight - anchor.scrollHeight;
					olderLoadAnchor.current = null;
					followsTableTail.current = false;
					return;
				}
				if (!tableScrollInitialized.current) {
					if (historyLoading) return;
					tableScrollInitialized.current = true;
					followsTableTail.current = true;
					if (virtualizationEnabled) rowVirtualizer.scrollToEnd({ behavior: "auto" });
					else pane.scrollTop = pane.scrollHeight;
					setTableScrollReady(true);
					return;
				}
				if (!followsTableTail.current) return;
				if (!virtualizationEnabled) pane.scrollTop = pane.scrollHeight;
			}, [
				historyLoading,
				historyStartSeq,
				rowVirtualizer,
				virtualRowStructure,
				virtualizationEnabled
			]);
			const olderBusy = olderHistoryLoading || olderLoading;
			const showInitialLoading = historyLoading || !tableScrollReady;
			const historyRowOffset = hasOlderRecords ? 1 : 0;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				ref: rootRef,
				className: TrajectoryTable_module_css_default.split,
				style: splitStyle,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					ref: tablePaneRef,
					className: TrajectoryTable_module_css_default.tablePane,
					"data-trajectory-scroll": "",
					onScroll: (event) => {
						const pane = event.currentTarget;
						followsTableTail.current = pane.scrollHeight - pane.clientHeight - pane.scrollTop <= BOTTOM_FOLLOW_THRESHOLD_PX;
						requestOlder(pane, true);
					},
					onClick: (event) => {
						if (event.target === event.currentTarget) clearAllSelections();
					},
					children: [showInitialLoading && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						className: TrajectoryTable_module_css_default.historyLoading,
						role: "status",
						"aria-live": "polite",
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
							className: TrajectoryTable_module_css_default.historyLoadingBar,
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, { state: "ongoing" }), t("history.loadingTrajectory")]
						})
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("table", {
						className: TrajectoryTable_module_css_default.table,
						"data-scroll-ready": tableScrollReady || void 0,
						"aria-rowcount": records.length + historyRowOffset,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("colgroup", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("col", { className: TrajectoryTable_module_css_default.eventColumn }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("col", { className: TrajectoryTable_module_css_default.contentColumn })] }), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("tbody", { children: [
							hasOlderRecords && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("tr", {
								className: TrajectoryTable_module_css_default.historyLoadRow,
								"data-history-load": "",
								"aria-rowindex": 1,
								children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("td", {
									colSpan: 2,
									children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
										type: "button",
										className: TrajectoryTable_module_css_default.historyLoadButton,
										disabled: olderBusy || onLoadOlder === void 0,
										"aria-label": olderBusy ? t("history.loadingEarlierAria") : t("history.loadEarlier"),
										onClick: () => {
											const pane = tablePaneRef.current;
											if (pane !== null) requestOlder(pane, false);
										},
										children: [
											olderBusy && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, { state: "ongoing" }),
											/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
												"aria-hidden": "true",
												children: olderBusy ? t("history.loadingEarlier") : t("history.loadEarlier")
											}),
											/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
												className: TrajectoryTable_module_css_default.visuallyHidden,
												role: "status",
												"aria-live": "polite",
												children: olderBusy ? t("history.loadingEarlier") : ""
											})
										]
									})
								})
							}),
							virtualTop > 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("tr", {
								className: TrajectoryTable_module_css_default.virtualSpacer,
								"data-virtual-spacer": "top",
								"aria-hidden": "true",
								children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("td", {
									colSpan: 2,
									style: { "--trajectory-virtual-spacer-height": `${virtualTop}px` }
								})
							}),
							renderedRecords.map(({ record, position, terminalRequestBoundary }) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)(RecordPresentation, {
								cell: record.cell,
								t,
								children: ({ displayText, listDisplayText, resultText, toolCallOnly, toolCallText }) => {
									const isCollapsedSummary = record.collapsedSummary !== void 0;
									const isRequestOnly = record.cell.requestOnly === true;
									const isInitialSystem = record.cell.kind === "system" && record.cell.index === allRecords[0]?.cell.index;
									const key = requestKey(record.turn, record.group);
									const request = requestBoundaries.get(key) === record.cell.index && !isCollapsedSummary && (record.turn === null || !collapsedTurns.has(record.turn)) ? requestNumbers.get(key) : void 0;
									const requestInfo = request === void 0 ? void 0 : sessionRequestNumbers?.find((candidate) => candidate.number === request);
									const requestStatus = requestInfo?.status ?? (record.cell.isError === true ? "error" : void 0);
									const requestRunIndex = requestBoundaryRuns.get(record.cell.index) ?? 0;
									const requestBoundaryStyle = { "--request-boundary-offset": `${requestRunIndex * 8}px` };
									const requestLabel = request === void 0 ? void 0 : t(requestInfo?.purpose === "compaction" ? "request.labelCompaction" : "request.label", { request });
									const requestSelected = requestInfo !== void 0 && selectedRequest?.identity === requestIdentity(requestInfo);
									const sectionActive = record.turn === null ? activeSection === record.section : activeTurn === record.turn;
									const singleToolNotice = record.cell.kind === "context" && record.cell.sourceBlocks?.length === 1 && record.cell.sourceBlocks.every((block) => block.type === "tool-addition" || block.type === "tool-removal");
									return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("tr", {
										tabIndex: isRequestOnly || singleToolNotice ? -1 : 0,
										"aria-rowindex": position + 1 + historyRowOffset,
										"aria-label": isCollapsedSummary ? t("request.collapsedSummary", {
											kind: t(record.collapsedSummaryKind === "turn" ? "request.collapsedTurn" : "request.collapsedAssistant"),
											summary: record.collapsedSummary
										}) : isRequestOnly ? t("request.rowAriaCompaction", { request: request ?? "" }) : t("request.rowAria", {
											request: request === void 0 ? "" : t("request.rowPrefix", { request }),
											kind: t(KIND_LABEL_KEY[record.cell.kind]),
											content: listDisplayText || t("request.noContent")
										}),
										"aria-selected": !isCollapsedSummary && !isRequestOnly && selectedIndex === record.cell.index,
										"data-kind": record.cell.kind,
										"data-trajectory-row-key": trajectoryVirtualRecordKey(record),
										"data-virtual-position": virtualizationEnabled ? position : void 0,
										"data-record-index": !isCollapsedSummary && !isRequestOnly ? record.cell.index : void 0,
										"data-request-only": isRequestOnly || void 0,
										"data-terminal-request-boundary": terminalRequestBoundary || void 0,
										"data-group-start": record.groupStart || void 0,
										"data-turn-start": record.turnStart || void 0,
										"data-error": record.cell.isError || void 0,
										"data-running": stateOf(record) === "running" || void 0,
										"data-turn-end": record.turnEnd || void 0,
										"data-collapsed-summary": record.collapsedSummaryKind,
										"data-selected": !isCollapsedSummary && selectedIndex === record.cell.index || void 0,
										"data-timeline-focus": isCollapsedSummary || timelineFocusIndexes === null ? void 0 : timelineFocusIndexes.has(record.cell.index) ? "inside" : "outside",
										onClick: isRequestOnly || singleToolNotice ? void 0 : isCollapsedSummary ? () => {
											if (record.collapsedSummaryKind === "turn" && record.turn !== null) onToggleTurn(record.turn);
											else onToggleAssistant(trajectoryRecordId(record.cell));
										} : () => {
											selectRecord(record.cell.index);
										},
										onDoubleClick: (event) => {
											if (isCollapsedSummary || isRequestOnly || singleToolNotice) return;
											if (record.turn !== null && collapsedTurns.has(record.turn)) {
												event.preventDefault();
												onToggleTurn(record.turn);
												return;
											}
											if (record.cell.kind === "message" && assistantToolCalls(allRecords, record.cell.index).length > 0) {
												event.preventDefault();
												onToggleAssistant(trajectoryRecordId(record.cell));
												return;
											}
											if (!record.turnStart) return;
											if (record.turn === null) return;
											if (allRecords.filter((candidate) => candidate.turn === record.turn && candidate.cell.requestOnly !== true && candidate.cell.kind !== "system").length <= 1) return;
											event.preventDefault();
											onToggleTurn(record.turn);
										},
										onKeyDown: (event) => {
											if (isRequestOnly || singleToolNotice) return;
											if (event.key !== "Enter" && event.key !== " ") return;
											event.preventDefault();
											if (isCollapsedSummary) {
												if (record.collapsedSummaryKind === "turn" && record.turn !== null) onToggleTurn(record.turn);
												else onToggleAssistant(trajectoryRecordId(record.cell));
												return;
											}
											selectRecord(record.cell.index);
										},
										children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("td", {
											className: TrajectoryTable_module_css_default.event,
											children: [
												request !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
													type: "button",
													className: requestSelected ? `${TrajectoryTable_module_css_default.requestBoundaryControl} ${TrajectoryTable_module_css_default.requestBoundaryControlActive}` : TrajectoryTable_module_css_default.requestBoundaryControl,
													"aria-label": requestLabel,
													"aria-pressed": requestSelected,
													"data-label": requestLabel,
													"data-request-run-index": requestRunIndex,
													"data-request-status": requestStatus,
													style: requestBoundaryStyle,
													onClick: (event) => {
														event.stopPropagation();
														if (requestInfo !== void 0) selectRequest({ identity: requestIdentity(requestInfo) });
													},
													onDoubleClick: (event) => {
														event.stopPropagation();
													}
												}),
												record.turn !== null && activeTurn === record.turn && !isInitialSystem && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
													className: TrajectoryTable_module_css_default.turnRail,
													"aria-hidden": "true"
												}),
												!isCollapsedSummary && selectedIndex === record.cell.index && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
													className: TrajectoryTable_module_css_default.selectionRail,
													"aria-hidden": "true"
												}),
												!isCollapsedSummary && !isRequestOnly && record.turnStart && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
													className: sectionActive ? `${TrajectoryTable_module_css_default.turnLabel} ${TrajectoryTable_module_css_default.turnLabelActive}` : TrajectoryTable_module_css_default.turnLabel,
													"aria-label": sectionLabel(record.turn, t),
													children: record.turn === null ? sectionLabel(record.turn, t) : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
														className: TrajectoryTable_module_css_default.turnLabelFull,
														"aria-hidden": "true",
														children: sectionLabel(record.turn, t)
													}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
														className: TrajectoryTable_module_css_default.turnLabelCompact,
														"aria-hidden": "true",
														children: ["#", record.turn]
													})] })
												}),
												/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
													className: TrajectoryTable_module_css_default.eventInner,
													children: !isCollapsedSummary && !isRequestOnly && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
														className: TrajectoryTable_module_css_default.kindSlot,
														children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
															className: `${TrajectoryTable_module_css_default.kindTag} ${record.cell.kind === "system" ? TrajectoryTable_module_css_default.systemNeutral : record.cell.kind === "context" ? TrajectoryTable_module_css_default.contextGreen : record.cell.kind === "compacted" ? TrajectoryTable_module_css_default.compacted : record.cell.kind === "tool" ? TrajectoryTable_module_css_default.toolAmber : record.cell.kind === "message" ? TrajectoryTable_module_css_default.assistantVioletBright : record.cell.kind === "subtool" ? TrajectoryTable_module_css_default.subtoolAmber : TrajectoryTable_module_css_default[record.cell.kind]}`,
															"data-role-kind": record.cell.kind,
															children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
																label: t(KIND_LABEL_KEY[record.cell.kind]),
																side: "right",
																children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
																	className: TrajectoryTable_module_css_default.kindTagIcon,
																	"aria-hidden": "true",
																	children: KIND_ICON[record.cell.kind]
																})
															}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
																className: TrajectoryTable_module_css_default.kindTagLabel,
																children: t(KIND_LABEL_KEY[record.cell.kind])
															})]
														})
													})
												})
											]
										}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("td", {
											className: TrajectoryTable_module_css_default.content,
											children: isRequestOnly ? null : record.collapsedSummary !== void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
												className: TrajectoryTable_module_css_default.collapsedTurnContent,
												title: record.collapsedSummary,
												children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
													className: TrajectoryTable_module_css_default.collapsedTurnEllipsis,
													children: "…"
												}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
													className: TrajectoryTable_module_css_default.collapsedTurnText,
													children: record.collapsedSummary
												})]
											}) : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
												className: resultText === void 0 ? TrajectoryTable_module_css_default.contentText : TrajectoryTable_module_css_default.resultPreview,
												title: resultText === void 0 ? listDisplayText : `${listDisplayText} → ${resultText}`,
												children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
													className: resultText === void 0 ? void 0 : TrajectoryTable_module_css_default.resultRequest,
													children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(RecordListText, {
														displayText,
														toolCallOnly,
														toolCallText,
														t
													})
												}), resultText !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
													className: record.cell.isError ? `${TrajectoryTable_module_css_default.inlineResult} ${TrajectoryTable_module_css_default.error}` : TrajectoryTable_module_css_default.inlineResult,
													children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
														className: TrajectoryTable_module_css_default.arrow,
														children: "→"
													}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
														className: resultText === t("record.noOutput") ? `${TrajectoryTable_module_css_default.inlineResultText} ${TrajectoryTable_module_css_default.noOutputText}` : TrajectoryTable_module_css_default.inlineResultText,
														children: resultText
													})]
												})]
											})
										})]
									});
								}
							}, trajectoryVirtualRecordKey(record))),
							virtualBottom > 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("tr", {
								className: TrajectoryTable_module_css_default.virtualSpacer,
								"data-virtual-spacer": "bottom",
								"aria-hidden": "true",
								children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("td", {
									colSpan: 2,
									style: { "--trajectory-virtual-spacer-height": `${virtualBottom}px` }
								})
							})
						] })]
					})]
				}), (selectedRequestInfo !== void 0 || promptSelected || selected !== void 0 && selectedState !== void 0) && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("aside", {
					className: TrajectoryTable_module_css_default.details,
					"aria-label": t("details.event"),
					style: detailsWidth === null ? void 0 : { width: detailsWidth },
					children: [
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: TrajectoryTable_module_css_default.detailsResizeHandle,
							role: "separator",
							"aria-label": t("details.resize"),
							"aria-controls": "trajectory-detail-panel",
							"aria-orientation": "vertical",
							tabIndex: 0,
							title: t("details.resizeTitle"),
							onDoubleClick: () => {
								setDetailsWidth(null);
								setToolRequestOffset(null);
							},
							onPointerDown: (event) => {
								if (event.button !== 0) return;
								const details = event.currentTarget.parentElement;
								if (details === null) return;
								const split = details.parentElement;
								if (split === null) return;
								const splitWidth = split.getBoundingClientRect().width;
								detailsResizeDrag.current = {
									pointerId: event.pointerId,
									startX: event.clientX,
									startWidth: details.getBoundingClientRect().width,
									splitWidth,
									startToolRequestOffset: toolRequestOffset ?? splitWidth * TOOL_REQUEST_SHARE - defaultToolRequestWidth(splitWidth)
								};
								event.currentTarget.setPointerCapture(event.pointerId);
								event.preventDefault();
							},
							onPointerMove: (event) => {
								const drag = detailsResizeDrag.current;
								if (drag === null || drag.pointerId !== event.pointerId) return;
								const nextDetailsWidth = clampDetailsWidth(drag.startWidth + drag.startX - event.clientX, drag.splitWidth);
								setDetailsWidth(nextDetailsWidth);
								setToolRequestOffset(drag.startToolRequestOffset + (nextDetailsWidth - drag.startWidth) * TOOL_REQUEST_SHARE);
							},
							onPointerUp: (event) => {
								if (detailsResizeDrag.current?.pointerId !== event.pointerId) return;
								detailsResizeDrag.current = null;
								event.currentTarget.releasePointerCapture(event.pointerId);
							},
							onPointerCancel: () => {
								detailsResizeDrag.current = null;
							},
							onKeyDown: (event) => {
								if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
								const details = event.currentTarget.parentElement;
								if (details === null) return;
								const split = details.parentElement;
								if (split === null) return;
								const direction = event.key === "ArrowLeft" ? 1 : -1;
								const currentDetailsWidth = details.getBoundingClientRect().width;
								const splitWidth = split.getBoundingClientRect().width;
								const nextDetailsWidth = clampDetailsWidth(currentDetailsWidth + direction * DETAILS_RESIZE_STEP, splitWidth);
								const currentToolRequestOffset = toolRequestOffset ?? splitWidth * TOOL_REQUEST_SHARE - defaultToolRequestWidth(splitWidth);
								setDetailsWidth(nextDetailsWidth);
								setToolRequestOffset(currentToolRequestOffset + (nextDetailsWidth - currentDetailsWidth) * TOOL_REQUEST_SHARE);
								event.preventDefault();
							}
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							className: TrajectoryTable_module_css_default.detailsHeader,
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
								className: TrajectoryTable_module_css_default.detailsTitle,
								children: selectedRequestInfo !== void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
										className: TrajectoryTable_module_css_default.requestDetailsDot,
										"aria-hidden": "true"
									}),
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
										className: TrajectoryTable_module_css_default.requestDetailsName,
										children: t("request.label", { request: selectedRequestNumber ?? "—" })
									}),
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
										className: TrajectoryTable_module_css_default.detailsLocation,
										children: selectedRequestInfo.purpose === "compaction" ? t("request.compaction", { section: sectionLabel(selectedRequestInfo.turn, t) }) : sectionLabel(selectedRequestInfo.turn, t)
									})
								] }) : promptSelected ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
									className: `${TrajectoryTable_module_css_default.kindTag} ${TrajectoryTable_module_css_default.systemNeutral}`,
									children: t("kind.system")
								}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
									className: TrajectoryTable_module_css_default.detailsLocation,
									children: selected?.cell.text
								})] }) : selected !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
									className: `${TrajectoryTable_module_css_default.kindTag} ${selected.cell.kind === "context" ? TrajectoryTable_module_css_default.contextGreen : selected.cell.kind === "compacted" ? TrajectoryTable_module_css_default.compacted : selected.cell.kind === "tool" ? TrajectoryTable_module_css_default.toolAmber : selected.cell.kind === "message" ? TrajectoryTable_module_css_default.assistantVioletBright : selected.cell.kind === "subtool" ? TrajectoryTable_module_css_default.subtoolAmber : TrajectoryTable_module_css_default[selected.cell.kind]}`,
									children: t(KIND_LABEL_KEY[selected.cell.kind])
								}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
									className: TrajectoryTable_module_css_default.detailsLocation,
									children: selected.cell.kind === "compacted" ? sectionLabel(selected.turn, t) : `${sectionLabel(selected.turn, t)} · ${selected.group}`
								})] })
							}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
								type: "button",
								className: TrajectoryTable_module_css_default.close,
								"aria-label": t("details.close"),
								onClick: clearInspectorSelection,
								children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
									"aria-hidden": "true",
									children: "×"
								})
							})]
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: TrajectoryTable_module_css_default.detailTabs,
							role: "tablist",
							"aria-label": t("details.event"),
							children: selectedTabs.map((tab) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
								id: `trajectory-detail-${tab.id}`,
								type: "button",
								role: "tab",
								"aria-controls": "trajectory-detail-panel",
								"aria-selected": activeTab === tab.id,
								className: activeTab === tab.id ? `${TrajectoryTable_module_css_default.detailTab} ${TrajectoryTable_module_css_default.detailTabActive}` : TrajectoryTable_module_css_default.detailTab,
								onClick: () => {
									activateTab(tab.id);
								},
								children: t(tab.labelKey)
							}, tab.id))
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							id: "trajectory-detail-panel",
							className: activeTab === "overview" ? `${TrajectoryTable_module_css_default.detailBody} ${TrajectoryTable_module_css_default.detailBodySummary}` : selectedProgram !== void 0 && (activeTab === "input" || activeTab === "output") ? `${TrajectoryTable_module_css_default.detailBody} ${TrajectoryTable_module_css_default.detailBodyProgram}` : TrajectoryTable_module_css_default.detailBody,
							role: "tabpanel",
							"aria-labelledby": `trajectory-detail-${activeTab}`,
							children: [
								selectedRequestInfo !== void 0 && selectedRequestState !== void 0 && activeTab === "overview" && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("dl", {
									className: `${TrajectoryTable_module_css_default.overview} ${TrajectoryTable_module_css_default.summaryScrollRegion}`,
									"data-summary-scroll-region": "",
									children: [
										/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("details.status") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", {
											className: selectedRequestState === "error" ? TrajectoryTable_module_css_default.error : void 0,
											children: statusLabel(selectedRequestState, t)
										})] }),
										selectedRequestInfo.purpose === "compaction" && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("details.purpose") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: t("request.compactionPurpose") })] }),
										(selectedRequestInfo.provider ?? selectedRequestInfo.requestConfig?.provider) !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("details.provider") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: selectedRequestInfo.provider ?? selectedRequestInfo.requestConfig?.provider })] }),
										(selectedRequestInfo.model ?? selectedRequestInfo.requestConfig?.model) !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("details.model") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: selectedRequestInfo.model ?? selectedRequestInfo.requestConfig?.model })] }),
										/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("details.toolCalls") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: selectedRequestToolCalls })] }),
										selectedRequestSubtoolCalls > 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("details.subtoolCalls") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: selectedRequestSubtoolCalls })] }),
										selectedRequestInfo.error !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("details.error") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", {
											className: TrajectoryTable_module_css_default.error,
											children: requestErrorMessage(selectedRequestInfo, t)
										})] }),
										selectedRequestInfo.retry !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("details.retry") }), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("dd", { children: [
											t("details.scheduled"),
											" ",
											selectedRequestInfo.maxRetries === void 0 ? selectedRequestInfo.retry : t("request.retryProgress", {
												retry: selectedRequestInfo.retry,
												maximum: selectedRequestInfo.maxRetries
											})
										] })] }),
										selectedRequestInfo.retryDelayMs !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("details.retryDelay") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: formatDurationMs(selectedRequestInfo.retryDelayMs, t) })] }),
										selectedRequestResult !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("details.result") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", {
											className: TrajectoryTable_module_css_default.overviewParentLinks,
											children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
												type: "button",
												className: TrajectoryTable_module_css_default.overviewHierarchyNavLink,
												onClick: () => {
													openRecordSummary(selectedRequestResult);
												},
												children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: selectedRequestInfo.purpose === "compaction" ? t("details.compacted") : t("details.assistantMessage") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronRightOutlineRegular, {
													className: TrajectoryTable_module_css_default.overviewHierarchyJumpIconTight,
													size: 11
												})]
											})
										})] })
									]
								}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
									className: TrajectoryTable_module_css_default.overviewSections,
									children: [
										selectedRequestOptions !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(OverviewSection, {
											label: t("tab.options"),
											onOpen: () => {
												activateTab("options");
											},
											children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(RequestOptions, {
												options: selectedRequestOptions,
												preview: true,
												stringWrapping,
												t
											})
										}),
										/* @__PURE__ */ (0, react_jsx_runtime.jsx)(OverviewSection, {
											label: t("tab.usage"),
											onOpen: () => {
												activateTab("usage");
											},
											children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(UsageRows, {
												usage: selectedRequestUsage,
												t
											})
										}),
										/* @__PURE__ */ (0, react_jsx_runtime.jsx)(OverviewSection, {
											label: t("tab.timing"),
											onOpen: () => {
												activateTab("timing");
											},
											children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(RequestTiming, {
												assistant: selectedRequestAssistant,
												anchor: selectedRequestAnchor,
												request: selectedRequestInfo,
												preview: true,
												t
											})
										})
									]
								})] }),
								selectedRequestInfo !== void 0 && activeTab === "options" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(RequestOptions, {
									options: selectedRequestOptions,
									stringWrapping,
									t
								}),
								selectedRequestInfo !== void 0 && activeTab === "usage" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(RequestUsagePanel, {
									usage: selectedRequestUsage,
									cumulative: selectedRequestCumulativeUsage,
									t
								}),
								selectedRequestInfo !== void 0 && activeTab === "timing" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(RequestTiming, {
									assistant: selectedRequestAssistant,
									anchor: selectedRequestAnchor,
									request: selectedRequestInfo,
									t
								}),
								selectedPrompt !== void 0 && selectedPreviousPrompt !== void 0 && activeTab === "diff" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(SystemPromptDiff, {
									before: selectedPreviousPrompt,
									after: selectedPrompt,
									t
								}),
								promptSelected && activeTab === "system-prompt" && (selectedSystemPrompt === "" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
									className: TrajectoryTable_module_css_default.noPayload,
									children: t("record.systemPromptMissing")
								}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
									className: `${TrajectoryTable_module_css_default.markdownPayload} ${TrajectoryTable_module_css_default.systemPrompt}`,
									children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.MarkdownText, {
										text: selectedSystemPrompt,
										labels: markdownLabels(t)
									})
								})),
								selectedPrompt !== void 0 && activeTab === "tools" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ToolCatalog, {
									tools: selectedPrompt.tools,
									stringWrapping,
									t
								}),
								!promptSelected && selected?.cell.kind === "compacted" && selectedState !== void 0 && activeTab === "overview" && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("dl", {
									className: `${TrajectoryTable_module_css_default.overview} ${TrajectoryTable_module_css_default.summaryScrollRegion}`,
									"data-summary-scroll-region": "",
									children: [
										/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("details.status") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", {
											className: selectedState === "error" ? TrajectoryTable_module_css_default.error : void 0,
											children: statusLabel(selectedState, t)
										})] }),
										/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("timing.duration") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: formatElapsedSeconds(selected.cell.timeSeconds, t) })] }),
										/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("usage.tokens") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: "—" })] })
									]
								}), selected.cell.outputDetail !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
									className: `${TrajectoryTable_module_css_default.compactedSummary} ${TrajectoryTable_module_css_default.summaryScrollRegion}`,
									"data-summary-scroll-region": "",
									children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(MarkdownRecordContent, {
										record: selected,
										renderImages,
										rendered: true,
										thinkingExpanded,
										onThinkingExpandedChange: setThinkingExpanded,
										onOpenCall: openCallSummary,
										t
									})
								})] }),
								!promptSelected && selected !== void 0 && selected.cell.kind !== "compacted" && selectedState !== void 0 && activeTab === "overview" && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [
									selectedProgram !== void 0 && selectedProgram.description !== "" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
										className: TrajectoryTable_module_css_default.programDescription,
										children: selectedProgram.description
									}),
									/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("dl", {
										className: `${TrajectoryTable_module_css_default.overview} ${TrajectoryTable_module_css_default.summaryScrollRegion}`,
										"data-summary-scroll-region": "",
										children: [
											selected.cell.messageSource !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("details.source") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", {
												className: TrajectoryTable_module_css_default.overviewParentLinks,
												children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
													type: "button",
													className: TrajectoryTable_module_css_default.overviewHierarchyNavLink,
													onClick: () => {
														activateTab("source");
													},
													children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: messageSourceLabel(selected.cell.messageSource, t) }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronRightOutlineRegular, {
														className: TrajectoryTable_module_css_default.overviewHierarchyJumpIconTight,
														size: 11
													})]
												})
											})] }),
											hasSelectedHierarchy && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: selectedAssistantRequestTarget !== void 0 ? t("details.source") : t("details.hierarchy") }), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("dd", {
												className: TrajectoryTable_module_css_default.overviewParentLinks,
												children: [
													selectedAssistantRequestTarget !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
														type: "button",
														className: TrajectoryTable_module_css_default.overviewHierarchyNavLink,
														onClick: () => {
															selectRequest(selectedAssistantRequestTarget);
														},
														children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: t("request.label", { request: selectedAssistantRequest ?? "—" }) }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronRightOutlineRegular, {
															className: TrajectoryTable_module_css_default.overviewHierarchyJumpIconTight,
															size: 11
														})]
													}),
													selectedParentMessage !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
														type: "button",
														className: TrajectoryTable_module_css_default.overviewHierarchyNavLink,
														onClick: () => {
															openRecordSummary(selectedParentMessage);
														},
														children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: t("details.assistantMessage") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronRightOutlineRegular, {
															className: TrajectoryTable_module_css_default.overviewHierarchyJumpIconTight,
															size: 11
														})]
													}),
													selectedParentTool !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
														type: "button",
														className: TrajectoryTable_module_css_default.overviewHierarchyNavLink,
														onClick: () => {
															openRecordSummary(selectedParentTool);
														},
														children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: t("details.toolCall") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronRightOutlineRegular, {
															className: TrajectoryTable_module_css_default.overviewHierarchyJumpIconTight,
															size: 11
														})]
													})
												]
											})] }),
											/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("details.status") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", {
												className: selectedState === "error" ? TrajectoryTable_module_css_default.error : void 0,
												children: statusLabel(selectedState, t)
											})] }),
											selected.cell.kind === "message" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(TokenRows, {
												cell: selected.cell,
												t
											}),
											(selected.cell.kind === "user" || selected.cell.kind === "context") && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("timing.duration") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: formatElapsedSeconds(selected.cell.timeSeconds, t) })] })
										]
									}),
									/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
										className: TrajectoryTable_module_css_default.overviewSections,
										children: [
											selectedProgram !== void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(ProgramInput, {
												program: selectedProgram,
												initialWrapped: codeWrappingOnOpen,
												stringWrapping,
												onOpen: () => {
													activateTab("input");
												},
												t
											}, `preview:${selectedRecordId}`), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ProgramOutput, {
												record: selected,
												stringWrapping,
												onOpen: () => {
													activateTab("output");
												},
												t
											})] }) : isMarkdownRecord(selected) ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(react_jsx_runtime.Fragment, { children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(OverviewSection, {
												label: t("tab.preview"),
												onOpen: () => {
													activateTab("rendered");
												},
												children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(MarkdownRecordContent, {
													record: selected,
													renderImages,
													rendered: true,
													preview: true,
													thinkingExpanded,
													onThinkingExpandedChange: setThinkingExpanded,
													onOpenCall: openCallSummary,
													t
												})
											}) }) : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [
												selected.cell.inputDetail && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(OverviewSection, {
													label: t("tab.payload"),
													onOpen: () => {
														activateTab("input");
													},
													children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(RecordPayload, {
														record: selected,
														direction: "input",
														preview: true,
														renderImages,
														stringWrapping,
														t
													})
												}),
												selected.cell.outputDetail && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(OverviewSection, {
													label: t("tab.result"),
													onOpen: () => {
														activateTab("output");
													},
													children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(RecordPayload, {
														record: selected,
														direction: "output",
														preview: true,
														renderImages,
														stringWrapping,
														t
													})
												}),
												/* @__PURE__ */ (0, react_jsx_runtime.jsx)(OverviewSection, {
													label: t("tab.schema"),
													onOpen: () => {
														activateTab("schema");
													},
													children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(RecordSchema, {
														record: selected,
														preview: true,
														stringWrapping,
														t
													})
												})
											] }),
											selectedAssistantRequestTarget !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(OverviewSection, {
												label: t("timing.request"),
												onOpen: () => {
													selectRequest(selectedAssistantRequestTarget, "timing");
												},
												children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(RecordTiming, {
													record: selected,
													preview: true,
													t
												})
											}),
											(selected.cell.kind === "tool" || selected.cell.kind === "subtool") && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(OverviewSection, {
												label: t("tab.timing"),
												onOpen: () => {
													activateTab("timing");
												},
												children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(RecordTiming, {
													record: selected,
													preview: true,
													t
												})
											})
										]
									})
								] }),
								!promptSelected && selected !== void 0 && activeTab === "rendered" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(MarkdownRecordContent, {
									record: selected,
									renderImages,
									rendered: true,
									thinkingExpanded,
									onThinkingExpandedChange: setThinkingExpanded,
									onOpenCall: openCallSummary,
									t
								}),
								!promptSelected && selected !== void 0 && activeTab === "raw" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(MarkdownRecordContent, {
									record: selected,
									renderImages,
									rendered: false,
									thinkingExpanded,
									onThinkingExpandedChange: setThinkingExpanded,
									onOpenCall: openCallSummary,
									t
								}),
								!promptSelected && selected !== void 0 && activeTab === "source" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(MessageSource, {
									record: selected,
									stringWrapping,
									t
								}),
								!promptSelected && selected !== void 0 && activeTab === "input" && (selectedProgram === void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(RecordPayload, {
									record: selected,
									direction: "input",
									renderImages,
									stringWrapping,
									t
								}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ProgramInput, {
									program: selectedProgram,
									initialWrapped: codeWrappingOnOpen,
									stringWrapping,
									t
								}, `input:${selectedRecordId}`)),
								!promptSelected && selected !== void 0 && activeTab === "output" && (selectedProgram === void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(RecordPayload, {
									record: selected,
									direction: "output",
									renderImages,
									stringWrapping,
									t
								}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ProgramOutput, {
									record: selected,
									stringWrapping,
									t
								})),
								!promptSelected && selected !== void 0 && activeTab === "schema" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(RecordSchema, {
									record: selected,
									stringWrapping,
									t
								}),
								!promptSelected && selected !== void 0 && activeTab === "timing" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(RecordTiming, {
									record: selected,
									t
								})
							]
						})
					]
				})]
			});
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-trajectory/src/client/TrajectoryToolbar.module.css.mjs
		const css$2 = ".CIvZpa_root{z-index:4;box-sizing:border-box;width:100%;height:var(--dsh-trajectory-toolbar-height);border-bottom:.5px solid var(--dsw-alias-border-l2);background:var(--dsw-alias-bg-layer-1);position:sticky;top:0}.CIvZpa_inner{box-sizing:border-box;align-items:center;gap:8px;width:100%;height:100%;padding:0 6px;display:flex}.CIvZpa_actions{flex:none;align-items:center;gap:2px;display:flex}.CIvZpa_toggle{border-radius:var(--dsw-radius-sm);height:20px;color:var(--dsw-alias-label-tertiary);cursor:pointer;font:var(--dsw-font-xxs-12);background:0 0;border:0;flex:none;align-items:center;gap:4px;padding:0 7px;display:inline-flex}.CIvZpa_toggle:hover,.CIvZpa_toggle[aria-pressed=true]{color:var(--dsw-alias-label-primary);background:var(--dsw-alias-interactive-bg-hover)}.CIvZpa_toggle:focus-visible{outline:1px solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:1px}.CIvZpa_toggleIcon{stroke:currentColor;stroke-width:1.25px;stroke-linecap:round;stroke-linejoin:round;flex:none;width:12px;height:12px}.CIvZpa_control{box-sizing:border-box;width:88px;height:20px;color:var(--dsw-alias-label-tertiary);cursor:pointer;font:var(--dsw-font-xxs-12);background:0 0;border:0;border-radius:0;flex:none;justify-content:center;align-items:center;gap:4px;padding:0 5px;display:inline-flex}.CIvZpa_control[hidden]{display:none}.CIvZpa_control:hover:not(:disabled),.CIvZpa_control[aria-checked=true],.CIvZpa_control[aria-pressed=true]{color:var(--dsw-alias-label-primary)}.CIvZpa_control:focus-visible{outline:1px solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:1px}.CIvZpa_control:disabled{color:var(--dsw-alias-label-dimmed);cursor:not-allowed}.CIvZpa_controlTrack{background:var(--dsw-alias-border-l2);width:20px;height:10px;transition:background-color .12s var(--ds-ease-in-out);border-radius:5px;flex:none;display:inline-block;position:relative}.CIvZpa_controlThumb{corner-shape:round;background:var(--dsw-alias-bg-layer-1);width:6px;height:6px;transition:transform .12s var(--ds-ease-in-out);border-radius:50%;position:absolute;top:2px;left:2px}.CIvZpa_controlTrack[data-on=true]{background:var(--dsw-alias-state-business-primary)}.CIvZpa_controlTrack[data-on=true] .CIvZpa_controlThumb{transform:translate(10px)}.CIvZpa_action{border-radius:var(--dsw-radius-sm);height:20px;color:var(--dsw-alias-label-tertiary);cursor:pointer;font:var(--dsw-font-xxs-12);background:0 0;border:0;flex:none;align-items:center;gap:4px;padding:0 5px;display:inline-flex}.CIvZpa_action:hover{color:var(--dsw-alias-label-primary);background:var(--dsw-alias-interactive-bg-hover)}.CIvZpa_action:focus-visible{outline:1px solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:1px}.CIvZpa_actionIcon{color:var(--dsw-alias-label-tertiary);font:14px/14px var(--ds-font-family-code)}.CIvZpa_search{border:.5px solid var(--dsw-alias-border-l4);border-radius:var(--dsw-radius-sm);min-width:84px;height:22px;color:var(--dsw-alias-label-caption);background:var(--dsw-alias-bg-layer-2);flex:0 164px;align-items:center;gap:4px;margin-left:auto;padding:0 6px;display:flex}.CIvZpa_search:hover{border-color:var(--dsw-alias-label-caption)}.CIvZpa_search:focus-within{border-color:var(--dsw-alias-state-business-primary);background:var(--dsw-alias-bg-layer-1)}.CIvZpa_searchIcon{flex:none}.CIvZpa_searchInput{width:100%;min-width:0;color:var(--dsw-alias-label-primary);font:var(--dsw-font-xxs-12);background:0 0;border:0;outline:0;padding:0}.CIvZpa_searchInput::placeholder{color:var(--dsw-alias-label-caption)}.CIvZpa_searchInput::-webkit-search-cancel-button{cursor:pointer;width:12px;height:12px}";
		const tagId$2 = "@deepseek-ai/dsh-client-ui-trajectory/TrajectoryToolbar.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$2) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-trajectory";
			tag.dataset.pluginCss = tagId$2;
			tag.textContent = css$2;
			document.head.appendChild(tag);
		}
		var TrajectoryToolbar_module_css_default = {
			"action": "CIvZpa_action",
			"actionIcon": "CIvZpa_actionIcon",
			"actions": "CIvZpa_actions",
			"control": "CIvZpa_control",
			"controlThumb": "CIvZpa_controlThumb",
			"controlTrack": "CIvZpa_controlTrack",
			"inner": "CIvZpa_inner",
			"root": "CIvZpa_root",
			"search": "CIvZpa_search",
			"searchIcon": "CIvZpa_searchIcon",
			"searchInput": "CIvZpa_searchInput",
			"toggle": "CIvZpa_toggle",
			"toggleIcon": "CIvZpa_toggleIcon"
		};
		//#endregion
		//#region src/client/TrajectoryToolbar.tsx
		/**
		* Render the sticky trajectory toolbar.
		* @param props - rendered counts and whole-list fold state.
		* @returns the toolbar element.
		*/
		function TrajectoryToolbar({ actualDuration, onActualDurationChange, actualTime, onActualTimeChange, allTurnsCollapsed, onToggleAllTurns, allAssistantsCollapsed, onToggleAllAssistants, searchQuery, onSearchQueryChange, t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: TrajectoryToolbar_module_css_default.root,
				role: "toolbar",
				"aria-label": t("toolbar.aria"),
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: TrajectoryToolbar_module_css_default.inner,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: TrajectoryToolbar_module_css_default.actions,
						children: [
							/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
								type: "button",
								className: TrajectoryToolbar_module_css_default.toggle,
								"aria-label": t("toolbar.useActualDuration"),
								"aria-pressed": actualDuration,
								title: actualDuration ? t("toolbar.useEqualWidth") : t("toolbar.useActualDuration"),
								onClick: () => {
									onActualDurationChange(!actualDuration);
								},
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("svg", {
									className: TrajectoryToolbar_module_css_default.toggleIcon,
									viewBox: "0 0 16 16",
									fill: "none",
									"aria-hidden": "true",
									children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("circle", {
										cx: "8",
										cy: "8",
										r: "5.25"
									}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", { d: "M8 4.75V8l2.25 1.5" })]
								}), t("toolbar.duration")]
							}),
							/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
								type: "button",
								className: TrajectoryToolbar_module_css_default.control,
								role: "switch",
								"aria-checked": actualTime,
								hidden: true,
								onClick: () => {
									onActualTimeChange(!actualTime);
								},
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: t("toolbar.actualTime") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
									className: TrajectoryToolbar_module_css_default.controlTrack,
									"data-on": actualTime || void 0,
									"aria-hidden": "true",
									children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { className: TrajectoryToolbar_module_css_default.controlThumb })
								})]
							}),
							/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
								type: "button",
								className: TrajectoryToolbar_module_css_default.action,
								"aria-label": allTurnsCollapsed ? t("toolbar.expandTurns") : t("toolbar.collapseTurns"),
								"aria-pressed": allTurnsCollapsed,
								title: allTurnsCollapsed ? t("toolbar.expandTurns") : t("toolbar.collapseTurns"),
								onClick: onToggleAllTurns,
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
									className: TrajectoryToolbar_module_css_default.actionIcon,
									"aria-hidden": "true",
									children: allTurnsCollapsed ? "⊞" : "⊟"
								}), t("toolbar.turns")]
							}),
							/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
								type: "button",
								className: TrajectoryToolbar_module_css_default.action,
								"aria-label": allAssistantsCollapsed ? t("toolbar.expandCalls") : t("toolbar.collapseCalls"),
								"aria-pressed": allAssistantsCollapsed,
								title: allAssistantsCollapsed ? t("toolbar.expandCalls") : t("toolbar.collapseCalls"),
								onClick: onToggleAllAssistants,
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
									className: TrajectoryToolbar_module_css_default.actionIcon,
									"aria-hidden": "true",
									children: allAssistantsCollapsed ? "⊞" : "⊟"
								}), t("toolbar.calls")]
							})
						]
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: TrajectoryToolbar_module_css_default.search,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconSearchOutlineRegular, {
							size: 11,
							className: TrajectoryToolbar_module_css_default.searchIcon
						}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("input", {
							type: "search",
							className: TrajectoryToolbar_module_css_default.searchInput,
							"aria-label": t("toolbar.search"),
							placeholder: t("toolbar.searchPlaceholder"),
							value: searchQuery,
							onChange: (event) => {
								onSearchQueryChange(event.currentTarget.value);
							}
						})]
					})]
				})
			});
		}
		//#endregion
		//#region src/client/timeline.ts
		/**
		* Format a timeline duration as an integer-millisecond label.
		* @param milliseconds - Non-negative duration in milliseconds.
		* @param t - Trajectory locale translator.
		* @returns Millisecond label with thousands separators.
		*/
		function formatTimelineOffset(milliseconds, t) {
			return formatDurationMillis(milliseconds, t);
		}
		function laneFor(kind) {
			if (kind === "tool" || kind === "subtool") return 2;
			if (kind === "message" || kind === "compacted") return 1;
			return 0;
		}
		function finite(value) {
			return value !== null && value !== void 0 && Number.isFinite(value);
		}
		function cellRange(cell) {
			if (!finite(cell.startedAt)) return null;
			const durationMs = finite(cell.timeSeconds) ? Math.max(0, cell.timeSeconds * 1e3) : 0;
			return {
				start: cell.startedAt,
				end: cell.startedAt + durationMs
			};
		}
		/**
		* Project every visible record into a stable three-lane timeline.
		* @param turns - Unfiltered trajectory layout.
		* @param mode - Independent equal/recorded duration and compressed/complete time projection.
		* @returns Timeline model, or `null` when no record is visible.
		*/
		function deriveTrajectoryTimeline(turns, mode = "sequence") {
			if (mode !== "sequence") return deriveTimedTimeline(turns, mode === "duration" || mode === "actual", mode === "duration");
			const spans = [];
			const turnBoundaries = [];
			for (const turn of turns) {
				const cells = turn.groups.flatMap((group) => group.cells.filter((cell) => cell.requestOnly !== true));
				if (cells.length === 0) continue;
				if (turn.turn !== null) turnBoundaries.push({
					turn: turn.turn,
					time: spans.length
				});
				spans.push(...cells.map((cell, offset) => ({
					start: spans.length + offset,
					end: spans.length + offset + 1,
					index: cell.index,
					isError: cell.isError === true,
					kind: cell.kind,
					label: cell.text,
					lane: laneFor(cell.kind)
				})));
			}
			if (spans.length === 0) return null;
			return {
				start: 0,
				end: spans.length,
				spans,
				turnBoundaries
			};
		}
		function deriveTimedTimeline(turns, actualDuration, compressIdle) {
			const timedTurns = turns.flatMap((turn) => {
				const rawSpans = turn.groups.flatMap((group) => group.cells.flatMap((cell) => {
					if (cell.requestOnly === true) return [];
					const range = cellRange(cell);
					return range === null ? [] : [{
						...range,
						index: cell.index,
						isError: cell.isError === true,
						kind: cell.kind,
						label: cell.text,
						lane: laneFor(cell.kind)
					}];
				}));
				return rawSpans.length === 0 ? [] : [{
					turn: turn.turn,
					rawSpans
				}];
			});
			const rawSpans = timedTurns.flatMap((turn) => turn.rawSpans);
			if (rawSpans.length === 0) return null;
			const removedIdleBySpan = /* @__PURE__ */ new Map();
			let removedIdle = 0;
			let coveredUntil = null;
			for (const span of [...rawSpans].sort((left, right) => left.start - right.start || left.end - right.end)) {
				if (compressIdle && coveredUntil !== null && span.start > coveredUntil) removedIdle += span.start - coveredUntil;
				removedIdleBySpan.set(span, removedIdle);
				coveredUntil = coveredUntil === null ? span.end : Math.max(coveredUntil, span.end);
			}
			const spans = [];
			const turnBoundaries = [];
			for (const turn of timedTurns) {
				const projected = turn.rawSpans.map((span) => {
					const offset = removedIdleBySpan.get(span) ?? 0;
					return {
						...span,
						start: span.start - offset,
						end: (actualDuration ? span.end : span.start) - offset
					};
				});
				spans.push(...projected);
				if (turn.turn !== null) turnBoundaries.push({
					turn: turn.turn,
					time: Math.min(...projected.map((span) => span.start))
				});
			}
			return {
				start: Math.min(...spans.map((span) => span.start)),
				end: Math.max(...spans.map((span) => span.end)),
				spans,
				turnBoundaries
			};
		}
		/**
		* Identify records active at any point inside an inclusive selected interval.
		* @param turns - Unfiltered trajectory layout.
		* @param range - Selected interval in the active projection.
		* @param mode - Independent equal/recorded duration and compressed/complete time projection.
		* @returns Record indexes inside the focus interval.
		*/
		function trajectoryTimelineFocusIndexes(turns, range, mode = "sequence") {
			const model = deriveTrajectoryTimeline(turns, mode);
			return new Set(model?.spans.filter((span) => span.start <= range.end && span.end >= range.start).map((span) => span.index));
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-trajectory/src/client/TrajectoryTimeline.module.css.mjs
		const css$1 = ".pIWxHq_root{z-index:1;isolation:isolate;border-bottom:.5px solid var(--dsw-alias-border-l2);user-select:none;flex:none;position:relative}.pIWxHq_root [role=tooltip]{font:var(--dsw-font-xxxs-11)}.pIWxHq_plot{background:var(--dsw-alias-bg-layer-2);grid-template-columns:44px minmax(0,1fr);height:50px;display:grid;overflow:hidden}.pIWxHq_labels{border-right:.5px solid var(--dsw-alias-border-l1);color:var(--dsw-alias-label-caption);font:var(--dsw-font-xs-13);font-size:10px;line-height:1;position:relative}.pIWxHq_labels span{text-align:right;justify-content:flex-end;align-items:center;height:8px;display:flex;position:absolute;right:3px}.pIWxHq_labels span:first-child{top:7px}.pIWxHq_labels span:nth-child(2){top:21px}.pIWxHq_labels span:nth-child(3){top:35px}.pIWxHq_track{cursor:crosshair;touch-action:none;position:relative;overflow:hidden}.pIWxHq_track[data-panning=true]{cursor:grabbing}.pIWxHq_earlierHistory{z-index:5;appearance:none;box-sizing:border-box;background:linear-gradient(to right, var(--dsw-alias-bg-layer-2) 0, var(--dsw-alias-bg-layer-2) 38%, transparent 100%);width:28px;color:var(--dsw-alias-label-secondary);font:var(--dsw-font-xs-13);opacity:.72;cursor:pointer;border:0;outline:none;justify-content:flex-start;align-items:center;padding-left:3px;line-height:1;display:flex;position:absolute;top:0;bottom:0;left:0}.pIWxHq_earlierHistory:hover{opacity:1}.pIWxHq_earlierHistory[aria-disabled=true]{cursor:default}.pIWxHq_earlierHistory:focus-visible{box-shadow:inset 0 0 0 1px var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary))}.pIWxHq_empty{color:var(--dsw-alias-label-caption);font:var(--dsw-font-xs-13);position:absolute;top:50%;left:50%;transform:translate(-50%,-50%)}.pIWxHq_track:focus-visible{outline:1px solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:-1px}.pIWxHq_lanes{z-index:2;top:7px;bottom:7px;left:var(--trajectory-domain-left);width:var(--trajectory-domain-width);position:absolute}.pIWxHq_turnBoundaries{z-index:3;top:0;bottom:0;left:var(--trajectory-domain-left);width:var(--trajectory-domain-width);pointer-events:none;position:absolute}@media (prefers-reduced-motion:no-preference){.pIWxHq_lanes[data-animate-viewport=true],.pIWxHq_turnBoundaries[data-animate-viewport=true]{transition:left .18s ease-out}}.pIWxHq_turnBoundary{top:0;bottom:0;left:var(--trajectory-turn-left);background:var(--dsw-alias-border-l2);width:.5px;position:absolute}.pIWxHq_span{top:calc(var(--trajectory-span-lane) * 14px);left:calc(var(--trajectory-span-left) + var(--trajectory-span-gap));width:max(2px, calc(var(--trajectory-span-width) - var(--trajectory-span-gap) - var(--trajectory-span-gap)));background:var(--dsw-alias-label-secondary);opacity:.78;border-radius:1px;min-width:2px;height:8px;position:absolute}.pIWxHq_span[data-timeline-span=user]{background:var(--dsw-alias-state-business-primary)}.pIWxHq_span[data-timeline-span=context]{background:color-mix(in srgb, var(--dsw-alias-state-success-primary) 68%, var(--dsw-alias-label-secondary))}.pIWxHq_span[data-timeline-span=message]{--trajectory-assistant-decoding-color:color-mix(in srgb, var(--dsw-alias-brand-primary-new-colorprimary-new-color) 60%, var(--dsw-alias-state-error-secondary));--trajectory-assistant-ttft-color:color-mix(in srgb, var(--trajectory-assistant-decoding-color) 54%, var(--dsw-alias-bg-layer-2));background:var(--trajectory-assistant-decoding-color);opacity:1}.pIWxHq_span[data-timeline-span=message][data-assistant-timing=true]{background:linear-gradient(to right, var(--trajectory-assistant-ttft-color) 0, var(--trajectory-assistant-ttft-color) var(--trajectory-assistant-ttft), var(--trajectory-assistant-decoding-color) var(--trajectory-assistant-ttft), var(--trajectory-assistant-decoding-color) 100%)}.pIWxHq_span[data-timeline-span=tool],.pIWxHq_span[data-timeline-span=subtool]{background:var(--dsw-alias-state-warn-label);opacity:1}.pIWxHq_span[data-error=true]{background:var(--dsw-alias-state-error-primary)}.pIWxHq_span[data-equal-duration=true]{width:8px;min-width:8px}.pIWxHq_span[data-selected=false]{opacity:.2}.pIWxHq_span[data-hovered=true]:not([data-current=true]){z-index:1;opacity:1;box-shadow:0 0 0 1px var(--dsw-alias-bg-layer-2), 0 0 0 2px color-mix(in srgb, var(--dsw-alias-state-business-primary) 80%, transparent)}.pIWxHq_span[data-current=true]{z-index:1;opacity:1;box-shadow:0 0 0 1px var(--dsw-alias-bg-layer-2), 0 0 0 2px var(--dsw-alias-state-business-primary)}.pIWxHq_span[data-search-match=false]{opacity:.14}.pIWxHq_selection{z-index:1;top:0;bottom:0;left:var(--trajectory-selection-left);width:var(--trajectory-selection-width);background:color-mix(in srgb, var(--dsw-alias-state-business-primary) 12%, transparent);min-width:1px;box-shadow:-100vw 0 0 100vw color-mix(in srgb, var(--dsw-alias-bg-layer-1) 58%, transparent), 100vw 0 0 100vw color-mix(in srgb, var(--dsw-alias-bg-layer-1) 58%, transparent);pointer-events:none;position:absolute}.pIWxHq_selectionEdges{z-index:4;top:0;bottom:0;left:var(--trajectory-selection-left);width:var(--trajectory-selection-width);pointer-events:none;min-width:1px;position:absolute}.pIWxHq_hoverLine{z-index:4;top:0;bottom:0;left:clamp(0px, calc(var(--trajectory-hover-left) - 1px), calc(100% - 2px));background:var(--dsw-alias-state-business-primary);pointer-events:none;width:2px;position:absolute}.pIWxHq_selectionEdges:before,.pIWxHq_selectionEdges:after{background:var(--dsw-alias-state-business-primary);content:\"\";width:3px;position:absolute;top:0;bottom:0}.pIWxHq_selectionEdges:before{left:0}.pIWxHq_selectionEdges:after{right:0}.pIWxHq_selectionEdges[data-dragging=true]:before,.pIWxHq_selectionEdges[data-dragging=true]:after{width:2px}.pIWxHq_selection[data-dragging=true]{background:color-mix(in srgb, var(--dsw-alias-state-business-primary) 18%, transparent)}";
		const tagId$1 = "@deepseek-ai/dsh-client-ui-trajectory/TrajectoryTimeline.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$1) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-trajectory";
			tag.dataset.pluginCss = tagId$1;
			tag.textContent = css$1;
			document.head.appendChild(tag);
		}
		var TrajectoryTimeline_module_css_default = {
			"earlierHistory": "pIWxHq_earlierHistory",
			"empty": "pIWxHq_empty",
			"hoverLine": "pIWxHq_hoverLine",
			"labels": "pIWxHq_labels",
			"lanes": "pIWxHq_lanes",
			"plot": "pIWxHq_plot",
			"root": "pIWxHq_root",
			"selection": "pIWxHq_selection",
			"selectionEdges": "pIWxHq_selectionEdges",
			"span": "pIWxHq_span",
			"track": "pIWxHq_track",
			"turnBoundaries": "pIWxHq_turnBoundaries",
			"turnBoundary": "pIWxHq_turnBoundary"
		};
		//#endregion
		//#region src/client/TrajectoryTimeline.tsx
		/** Chrome-Network-style overview timeline for focusing the trajectory ledger. */
		const MINIMUM_DRAG_PX = 3;
		const MINIMUM_ZOOM_OPERATIONS = 4;
		const EDGE_PAN_ZONE_FRACTION = .08;
		const EDGE_PAN_STEP_FRACTION = .025;
		const MAXIMUM_EDGE_PAN_PX = 32;
		const TIMELINE_TOOLTIP_DELAY_MS = 500;
		function assistantTimingDetail(metrics) {
			const start = metrics?.stepStartTime;
			const first = metrics?.firstTokenTime;
			const completed = metrics?.completedTime;
			if (metrics?.timingRecorded !== true || typeof start !== "number" || typeof first !== "number" || typeof completed !== "number" || !Number.isFinite(start) || !Number.isFinite(first) || !Number.isFinite(completed) || first < start || completed < first) return {};
			return {
				ttftMs: first - start,
				decodingMs: completed - first
			};
		}
		function timelineRecordDetail(cell) {
			const durationMs = cell.timeSeconds === null || !Number.isFinite(cell.timeSeconds) ? void 0 : Math.max(0, cell.timeSeconds * 1e3);
			const startedAt = cell.startedAt === null || !Number.isFinite(cell.startedAt) ? void 0 : cell.startedAt;
			return {
				...durationMs === void 0 ? {} : { durationMs },
				...startedAt === void 0 ? {} : { startedAt },
				...assistantTimingDetail(cell.assistantMetrics)
			};
		}
		function timelineKindLabel(kind, t) {
			switch (kind) {
				case "system": return t("kind.system");
				case "user": return t("kind.user");
				case "context": return t("kind.context");
				case "compacted": return t("kind.compacted");
				case "message": return t("kind.assistant");
				case "tool": return t("kind.tool");
				case "subtool": return t("kind.subtool");
			}
		}
		function formatRecordedTime(timestamp) {
			return new Date(timestamp).toLocaleTimeString(void 0, {
				hour: "2-digit",
				minute: "2-digit",
				second: "2-digit",
				fractionalSecondDigits: 3
			});
		}
		function timelineTooltipLabel(kind, detail, t) {
			const heading = timelineKindLabel(kind, t);
			if (detail === void 0) return heading;
			const duration = detail.durationMs === void 0 ? null : t("timeline.total", { duration: formatTimelineOffset(detail.durationMs, t) });
			return [
				heading,
				detail.startedAt === void 0 ? null : detail.durationMs === void 0 ? t("timeline.started", { time: formatRecordedTime(detail.startedAt) }) : `${formatRecordedTime(detail.startedAt)} → ${formatRecordedTime(detail.startedAt + detail.durationMs)}`,
				[duration, detail.ttftMs === void 0 || detail.decodingMs === void 0 ? null : t("timeline.ttftDecoding", {
					ttft: formatTimelineOffset(detail.ttftMs, t),
					decoding: formatTimelineOffset(detail.decodingMs, t)
				})].filter((value) => value !== null).join(" · ")
			].filter((value) => value !== null && value !== "").join("\n");
		}
		function orderedRange(left, right) {
			return left <= right ? {
				start: left,
				end: right
			} : {
				start: right,
				end: left
			};
		}
		function clampFraction(value) {
			return Math.min(1, Math.max(0, value));
		}
		function centeredRange(center, width, minimum, maximum) {
			const clampedWidth = Math.min(maximum - minimum, Math.max(0, width));
			const start = Math.min(Math.max(center - clampedWidth / 2, minimum), maximum - clampedWidth);
			return {
				start,
				end: start + clampedWidth
			};
		}
		function rangeFraction(range, start, duration, minimum, maximum) {
			const bounded = orderedRange(Math.min(maximum, Math.max(minimum, range.start)), Math.min(maximum, Math.max(minimum, range.end)));
			return {
				start: (bounded.start - start) / duration,
				end: (bounded.end - start) / duration
			};
		}
		function LaneLabels({ t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: TrajectoryTimeline_module_css_default.labels,
				"aria-hidden": "true",
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: t("column.input") }),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: t("column.model") }),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: t("column.tools") })
				]
			});
		}
		function EarlierHistoryBoundary({ loading, onHover, onLoad, t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
				label: loading ? t("history.loadingEarlier") : t("history.clickToLoadEarlier"),
				side: "right",
				delayMs: TIMELINE_TOOLTIP_DELAY_MS,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
					type: "button",
					className: TrajectoryTimeline_module_css_default.earlierHistory,
					"data-earlier-history": true,
					"data-loading": loading || void 0,
					"aria-label": loading ? t("history.loadingEarlierAria") : t("history.loadEarlier"),
					"aria-disabled": loading || onLoad === void 0,
					onClick: onLoad,
					onPointerEnter: (event) => {
						event.stopPropagation();
						onHover();
					},
					onPointerMove: (event) => {
						event.stopPropagation();
					},
					onPointerDown: (event) => {
						event.stopPropagation();
					},
					children: "…"
				})
			});
		}
		/** Overview renderer with drag ranges, click-sized focus, and Escape reset. */
		const TrajectoryTimeline = (0, react.memo)(function TrajectoryTimeline({ t, turns, mode, range, hasEarlierRecords = false, onLoadEarlier, selectedIndex = null, searchMatchIndexes = null, onRangeChange, onRecordSelect, onRecordFocus }) {
			const model = (0, react.useMemo)(() => deriveTrajectoryTimeline(turns, mode), [mode, turns]);
			const detailByIndex = (0, react.useMemo)(() => new Map(turns.flatMap((turn) => turn.groups.flatMap((group) => group.cells.map((cell) => [cell.index, timelineRecordDetail(cell)])))), [turns]);
			const dragRef = (0, react.useRef)(null);
			const panRef = (0, react.useRef)(null);
			const rootRef = (0, react.useRef)(null);
			const trackRef = (0, react.useRef)(null);
			const [draft, setDraft] = (0, react.useState)(null);
			const [hover, setHover] = (0, react.useState)(null);
			const [loadingEarlier, setLoadingEarlier] = (0, react.useState)(false);
			const [panning, setPanning] = (0, react.useState)(false);
			const [viewport, setViewport] = (0, react.useState)(null);
			const [animateViewport, setAnimateViewport] = (0, react.useState)(false);
			(0, react.useEffect)(() => {
				if (model !== null && range !== null && (range.end < model.start || range.start > model.end)) onRangeChange(null);
			}, [
				model,
				onRangeChange,
				range
			]);
			(0, react.useEffect)(() => {
				if (model === null) return;
				setAnimateViewport(false);
				setViewport((current) => current !== null && (current.end < model.start || current.start > model.end) ? null : current);
			}, [model]);
			(0, react.useEffect)(() => {
				if (model === null || selectedIndex === null) return;
				const selectedSpan = model.spans.find((span) => span.index === selectedIndex);
				if (selectedSpan === void 0) return;
				setAnimateViewport(true);
				setViewport((current) => {
					if (current === null) return current;
					if (selectedSpan.end > current.start && selectedSpan.start < current.end) return current;
					const duration = Math.max(1, current.end - current.start);
					const desiredStart = selectedSpan.end <= current.start ? selectedSpan.start : selectedSpan.end - duration;
					const nextStart = Math.min(Math.max(desiredStart, model.start), Math.max(model.start, model.end - duration));
					if (nextStart === current.start) return current;
					return {
						start: nextStart,
						end: nextStart + duration
					};
				});
			}, [model, selectedIndex]);
			const fullDuration = Math.max(1, (model?.end ?? 0) - (model?.start ?? 0));
			const viewportDuration = Math.min(fullDuration, Math.max(1, (viewport?.end ?? 0) - (viewport?.start ?? 0)));
			const viewportStart = model === null || viewport === null ? model?.start ?? 0 : Math.min(Math.max(viewport.start, model.start), model.end - viewportDuration);
			const domainDuration = viewport === null ? fullDuration : viewportDuration;
			const domainStart = viewport === null ? model?.start ?? 0 : viewportStart;
			const showsEarlierBoundary = hasEarlierRecords && model !== null && domainStart === model.start;
			const loadEarlier = onLoadEarlier === void 0 || loadingEarlier ? void 0 : () => {
				setLoadingEarlier(true);
				onLoadEarlier().finally(() => {
					setLoadingEarlier(false);
				});
			};
			const projectedDomainStyle = model === null ? void 0 : {
				"--trajectory-domain-left": `${-(domainStart - model.start) / domainDuration * 100}%`,
				"--trajectory-domain-width": `${fullDuration / domainDuration * 100}%`
			};
			const committed = model === null || range === null ? null : rangeFraction(range, domainStart, domainDuration, model.start, model.end);
			const visibleRange = (model === null || draft === null ? null : rangeFraction(draft, domainStart, domainDuration, model.start, model.end)) ?? committed;
			const activeRange = draft ?? range;
			(0, react.useEffect)(() => {
				const root = rootRef.current;
				if (root === null) return;
				const onWheel = (event) => {
					event.preventDefault();
					const track = trackRef.current;
					if (track === null || model === null) return;
					setAnimateViewport(false);
					const rect = track.getBoundingClientRect();
					const anchorFraction = clampFraction((event.clientX - rect.left) / Math.max(1, rect.width));
					const nextDuration = Math.min(fullDuration, Math.max(Math.min(mode === "sequence" ? MINIMUM_ZOOM_OPERATIONS : 20, fullDuration), domainDuration * Math.exp(event.deltaY * .0015)));
					if (nextDuration >= fullDuration * .999) {
						setViewport(null);
						return;
					}
					const anchorTime = domainStart + anchorFraction * domainDuration;
					const nextStart = Math.min(Math.max(anchorTime - anchorFraction * nextDuration, model.start), model.end - nextDuration);
					setViewport({
						start: nextStart,
						end: nextStart + nextDuration
					});
				};
				root.addEventListener("wheel", onWheel, { passive: false });
				return () => {
					root.removeEventListener("wheel", onWheel);
				};
			}, [
				domainDuration,
				domainStart,
				fullDuration,
				mode,
				model
			]);
			if (model === null) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("section", {
				ref: rootRef,
				className: TrajectoryTimeline_module_css_default.root,
				"aria-label": t("timeline.aria"),
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: TrajectoryTimeline_module_css_default.plot,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(LaneLabels, { t }), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: TrajectoryTimeline_module_css_default.track,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: TrajectoryTimeline_module_css_default.empty,
							children: t("timeline.noTimingData")
						}), hasEarlierRecords && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(EarlierHistoryBoundary, {
							loading: loadingEarlier,
							onHover: () => {
								setHover(null);
							},
							onLoad: loadEarlier,
							t
						})]
					})]
				})
			});
			const minimumSelectionDuration = Math.min(domainDuration, fullDuration / model.spans.length);
			const fractionAt = (event) => {
				const rect = event.currentTarget.getBoundingClientRect();
				return clampFraction((event.clientX - rect.left) / Math.max(1, rect.width));
			};
			const recordIndexAt = (event) => {
				const value = (event.target instanceof HTMLElement ? event.target : null)?.closest("[data-timeline-record-index]")?.dataset.timelineRecordIndex;
				if (value === void 0) return null;
				const index = Number(value);
				return Number.isFinite(index) ? index : null;
			};
			const commit = (nextRange) => {
				onRangeChange(nextRange);
			};
			const onPointerDown = (event) => {
				if (event.button === 2) {
					panRef.current = {
						anchorClientX: event.clientX,
						anchorStart: domainStart,
						moved: false,
						pannable: viewport !== null,
						pointerId: event.pointerId
					};
					if (viewport !== null) setAnimateViewport(false);
					setPanning(true);
					if (typeof event.currentTarget.setPointerCapture === "function") event.currentTarget.setPointerCapture(event.pointerId);
					return;
				}
				if (event.button !== 0) return;
				const anchor = fractionAt(event);
				const anchorTime = domainStart + anchor * domainDuration;
				const recordIndex = recordIndexAt(event);
				setHover({
					fraction: anchor,
					recordIndex
				});
				dragRef.current = {
					pointerId: event.pointerId,
					anchorTime,
					anchorClientX: event.clientX,
					recordIndex
				};
				if (typeof event.currentTarget.setPointerCapture === "function") event.currentTarget.setPointerCapture(event.pointerId);
				setDraft({
					start: anchorTime,
					end: anchorTime
				});
			};
			const onPointerMove = (event) => {
				const rect = event.currentTarget.getBoundingClientRect();
				const fraction = fractionAt(event);
				setHover({
					fraction,
					recordIndex: recordIndexAt(event)
				});
				const pan = panRef.current;
				if (pan !== null && pan.pointerId === event.pointerId) {
					if (Math.abs(event.clientX - pan.anchorClientX) >= MINIMUM_DRAG_PX) pan.moved = true;
					if (!pan.pannable) return;
					const delta = (event.clientX - pan.anchorClientX) / Math.max(1, rect.width);
					const nextStart = Math.min(Math.max(pan.anchorStart - delta * domainDuration, model.start), model.end - domainDuration);
					setViewport({
						start: nextStart,
						end: nextStart + domainDuration
					});
					return;
				}
				const drag = dragRef.current;
				if (drag === null || drag.pointerId !== event.pointerId) return;
				let nextDomainStart = domainStart;
				if (viewport !== null) {
					const localX = event.clientX - rect.left;
					const edgeWidth = Math.min(MAXIMUM_EDGE_PAN_PX, Math.max(1, rect.width * EDGE_PAN_ZONE_FRACTION));
					const direction = localX < edgeWidth ? -1 : localX > rect.width - edgeWidth ? 1 : 0;
					if (direction !== 0) {
						const strength = clampFraction((direction < 0 ? edgeWidth - localX : localX - (rect.width - edgeWidth)) / edgeWidth);
						const desiredStart = domainStart + direction * domainDuration * EDGE_PAN_STEP_FRACTION * Math.max(.2, strength);
						nextDomainStart = Math.min(Math.max(desiredStart, model.start), model.end - domainDuration);
						if (nextDomainStart !== domainStart) {
							setAnimateViewport(false);
							setViewport({
								start: nextDomainStart,
								end: nextDomainStart + domainDuration
							});
						}
					}
				}
				const pointTime = nextDomainStart + fraction * domainDuration;
				setDraft(orderedRange(drag.anchorTime, pointTime));
			};
			const onPointerEnd = (event) => {
				const pan = panRef.current;
				if (pan !== null && pan.pointerId === event.pointerId) {
					const moved = pan.moved || Math.abs(event.clientX - pan.anchorClientX) >= MINIMUM_DRAG_PX;
					panRef.current = null;
					setPanning(false);
					if (!moved) onRangeChange(null);
					return;
				}
				const drag = dragRef.current;
				if (drag === null || drag.pointerId !== event.pointerId) return;
				const pointFraction = fractionAt(event);
				const pointTime = domainStart + pointFraction * domainDuration;
				const selected = orderedRange(drag.anchorTime, pointTime);
				setHover({
					fraction: pointFraction,
					recordIndex: recordIndexAt(event)
				});
				dragRef.current = null;
				setDraft(null);
				const click = Math.abs(event.clientX - drag.anchorClientX) < MINIMUM_DRAG_PX;
				const clickedSpan = click && drag.recordIndex !== null ? model.spans.find((span) => span.index === drag.recordIndex) : void 0;
				if (clickedSpan !== void 0) {
					onRangeChange(null);
					onRecordSelect?.(clickedSpan.index);
					return;
				}
				commit(selected.end - selected.start < minimumSelectionDuration ? centeredRange(click ? selected.start : (selected.start + selected.end) / 2, minimumSelectionDuration, model.start, model.end) : selected);
				if (click) {
					const timelinePoint = selected.start;
					const nearest = model.spans.reduce((candidate, span) => {
						const candidateDistance = timelinePoint < candidate.start ? candidate.start - timelinePoint : timelinePoint > candidate.end ? timelinePoint - candidate.end : 0;
						return (timelinePoint < span.start ? span.start - timelinePoint : timelinePoint > span.end ? timelinePoint - span.end : 0) < candidateDistance ? span : candidate;
					});
					onRecordFocus?.(nearest.index);
				}
			};
			const onKeyDown = (event) => {
				if (event.key !== "Escape" || range === null) return;
				event.preventDefault();
				onRangeChange(null);
			};
			const onPointerCancel = () => {
				dragRef.current = null;
				panRef.current = null;
				setDraft(null);
				setHover(null);
				setPanning(false);
			};
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("section", {
				ref: rootRef,
				className: TrajectoryTimeline_module_css_default.root,
				"aria-label": t("timeline.aria"),
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: TrajectoryTimeline_module_css_default.plot,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(LaneLabels, { t }), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						ref: trackRef,
						className: TrajectoryTimeline_module_css_default.track,
						"data-panning": panning || void 0,
						"aria-label": t("timeline.overviewAria"),
						tabIndex: 0,
						onKeyDown,
						onPointerDown,
						onPointerMove,
						onPointerUp: onPointerEnd,
						onPointerCancel,
						onPointerLeave: () => {
							if (dragRef.current === null && panRef.current === null) setHover(null);
						},
						onDoubleClick: (event) => {
							event.preventDefault();
							onRangeChange(null);
						},
						onContextMenu: (event) => {
							event.preventDefault();
						},
						children: [
							showsEarlierBoundary && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(EarlierHistoryBoundary, {
								loading: loadingEarlier,
								onHover: () => {
									setHover(null);
								},
								onLoad: loadEarlier,
								t
							}),
							hover !== null && hover.recordIndex === null && draft === null && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
								className: TrajectoryTimeline_module_css_default.hoverLine,
								"data-timeline-hover-line": true,
								"aria-hidden": "true",
								style: { "--trajectory-hover-left": `${hover.fraction * 100}%` }
							}),
							visibleRange !== null && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
								className: TrajectoryTimeline_module_css_default.selection,
								"data-dragging": draft === null ? void 0 : "true",
								"aria-hidden": "true",
								style: {
									"--trajectory-selection-left": `${visibleRange.start * 100}%`,
									"--trajectory-selection-width": `${(visibleRange.end - visibleRange.start) * 100}%`
								}
							}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
								className: TrajectoryTimeline_module_css_default.selectionEdges,
								"data-dragging": draft === null ? void 0 : "true",
								"aria-hidden": "true",
								style: {
									"--trajectory-selection-left": `${visibleRange.start * 100}%`,
									"--trajectory-selection-width": `${(visibleRange.end - visibleRange.start) * 100}%`
								}
							})] }),
							/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
								className: TrajectoryTimeline_module_css_default.turnBoundaries,
								"data-animate-viewport": animateViewport || void 0,
								"aria-hidden": "true",
								style: projectedDomainStyle,
								children: model.turnBoundaries.filter((boundary) => boundary.time > model.start && boundary.time >= domainStart && boundary.time <= domainStart + domainDuration).map((boundary) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
									className: TrajectoryTimeline_module_css_default.turnBoundary,
									"data-turn": boundary.turn,
									style: { "--trajectory-turn-left": `${(boundary.time - model.start) / fullDuration * 100}%` }
								}, boundary.turn))
							}),
							/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
								className: TrajectoryTimeline_module_css_default.lanes,
								"data-animate-viewport": animateViewport || void 0,
								"data-timeline-domain": true,
								style: projectedDomainStyle,
								children: model.spans.filter((span) => span.index === selectedIndex || span.end >= domainStart && span.start <= domainStart + domainDuration).map((span) => {
									const left = (span.start - model.start) / fullDuration;
									const widthPercent = (span.end - span.start) / fullDuration * 100;
									const detail = detailByIndex.get(span.index);
									const ttftMs = detail?.ttftMs;
									const decodingMs = detail?.decodingMs;
									const ttftFraction = ttftMs === void 0 || decodingMs === void 0 || ttftMs + decodingMs <= 0 ? null : ttftMs / (ttftMs + decodingMs);
									return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
										label: () => timelineTooltipLabel(span.kind, detail, t),
										side: "bottom",
										delayMs: TIMELINE_TOOLTIP_DELAY_MS,
										children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
											"aria-hidden": "true",
											className: TrajectoryTimeline_module_css_default.span,
											"data-timeline-span": span.kind,
											"data-timeline-record-index": span.index,
											"data-assistant-timing": ttftFraction === null ? void 0 : "true",
											"data-error": span.isError || void 0,
											"data-equal-duration": mode === "time" || void 0,
											"data-current": span.index === selectedIndex || void 0,
											"data-hovered": hover?.recordIndex === span.index || void 0,
											"data-search-match": searchMatchIndexes === null ? void 0 : searchMatchIndexes.has(span.index) ? "true" : "false",
											"data-selected": activeRange === null ? void 0 : span.start <= activeRange.end && span.end >= activeRange.start ? "true" : "false",
											style: {
												"--trajectory-span-left": `${left * 100}%`,
												"--trajectory-span-width": `${widthPercent}%`,
												"--trajectory-span-gap": `min(${widthPercent * .08}%, 1px)`,
												"--trajectory-span-lane": span.lane,
												...ttftFraction === null ? {} : { "--trajectory-assistant-ttft": `${ttftFraction * 100}%` }
											}
										})
									}, span.index);
								})
							})
						]
					})]
				})
			});
		});
		//#endregion
		//#region src/client/layout.ts
		function layoutEntryOrder(entry) {
			return entry.kind === "system" && entry.change.kind === "initial" ? Number.NEGATIVE_INFINITY : entry.seq;
		}
		function inputCellDetail(node, t) {
			if (node.kind === "context" && node.content.length > 0 && node.content.every((block) => block.type === "tool-addition" || block.type === "tool-removal")) {
				const added = node.content.flatMap((block) => block.type === "tool-addition" ? [block.toolName] : []);
				const removed = node.content.flatMap((block) => block.type === "tool-removal" ? [block.toolName] : []);
				const single = node.content.length === 1 ? node.content[0] : void 0;
				const summary = added.length > 0 && removed.length > 0 ? t("layout.toolsChanged", {
					added: added.length,
					removed: removed.length
				}) : added.length > 0 ? t("layout.toolsAddedCount", { count: added.length }) : t("layout.toolsRemovedCount", { count: removed.length });
				return {
					text: single !== void 0 ? t(single.type === "tool-addition" ? "layout.toolAdded" : "layout.toolRemoved", { name: single.toolName }) : `${t("layout.toolUpdateNotice")} · ${summary}`,
					sourceSeq: node.seq,
					sourceBlocks: node.content.map((block) => ({
						type: block.type,
						content: block.toolName
					})),
					...single !== void 0 ? {} : { inputDetail: [...added.length > 0 ? [t("layout.toolsAdded", { names: added.join(", ") })] : [], ...removed.length > 0 ? [t("layout.toolsRemoved", { names: removed.join(", ") })] : []].join("\n") },
					timeSeconds: 0,
					startedAt: finiteTime(node.time)
				};
			}
			const preview = previewContent(node.content);
			const previewMarkdown = preview === "" ? void 0 : preview;
			const images = imageBlockCount(node.content);
			const files = fileBlockCount(node.content);
			return {
				text: [images > 0 ? t("layout.imageCount", { count: images }) : void 0, files > 0 ? t("layout.fileAttachments", { count: files }) : void 0].filter((value) => value !== void 0).join(" · "),
				...previewMarkdown === void 0 ? {} : { previewMarkdown },
				sourceSeq: node.seq,
				messageSource: node.source,
				inputDetail: detailContent(node.content),
				sourceBlocks: node.content.map((block) => sourceBlock(block)),
				timeSeconds: 0,
				startedAt: finiteTime(node.time)
			};
		}
		/**
		* Fold a snapshot into turn → Message/Step groups with expanded cells.
		* @param input - nodes plus in-flight partial/runningCalls.
		* @param t - Trajectory locale translator.
		* @returns turns ordered by first appearance.
		*/
		function deriveTrajectoryLayout(input, t) {
			const { nodes, eventLocations, partial, runningCalls, requests = [], callSchemas } = input;
			const resultByCall = indexResults(nodes);
			const callById = new Map(resultByCall);
			for (const call of runningCalls) callById.set(call.callId, call);
			const emittedCallIds = indexAssistantCallIds(nodes);
			const followingAssistants = indexFollowingAssistants(nodes);
			const callStartById = /* @__PURE__ */ new Map();
			for (const result of resultByCall.values()) {
				const startedAt = finiteTime(result.callTime);
				if (startedAt !== null) callStartById.set(result.callId, startedAt);
			}
			for (const call of runningCalls) {
				if (call.phase === "preparing") continue;
				const startedAt = finiteTime(call.time);
				if (startedAt !== null) callStartById.set(call.callId, startedAt);
			}
			const turns = /* @__PURE__ */ new Map();
			const standaloneCompactions = [];
			let index = 0;
			let prevAbsTime = null;
			let lastAssistantTurn = null;
			const bucket = (turn) => {
				let entry = turns.get(turn);
				if (entry === void 0) {
					entry = { groups: [] };
					turns.set(turn, entry);
				}
				return entry;
			};
			const pushMessage = (turn, laid) => {
				const groups = bucket(turn).groups;
				const last = groups.at(-1);
				if (last?.title === t("group.message")) {
					last.laid.push(laid);
					return;
				}
				groups.push({
					title: t("group.message"),
					laid: [laid]
				});
			};
			const pushStep = (turn, step, laid) => {
				if (laid.length === 0) return;
				const groups = bucket(turn).groups;
				const title = t("group.step", { step });
				const existing = groups.find((group) => group.title === title);
				if (existing !== void 0) {
					existing.laid.push(...laid);
					return;
				}
				groups.push({
					title,
					laid: [...laid]
				});
			};
			const pushStepInput = (turn, step, laid) => {
				if (laid.length === 0) return;
				const groups = bucket(turn).groups;
				const title = t("group.step", { step });
				const existing = groups.find((group) => group.title === title);
				if (existing === void 0) {
					groups.push({
						title,
						laid: [...laid]
					});
					return;
				}
				const request = existing.laid.findIndex((entry) => entry.cell.requestOnly === true);
				if (request === -1) existing.laid.push(...laid);
				else existing.laid.splice(request, 0, ...laid);
			};
			const representedRequests = /* @__PURE__ */ new Set();
			for (const node of nodes) if (node.kind === "assistant" && node.step > 0) representedRequests.add(`${node.turn}\u0000${node.step}`);
			if (partial !== null && partial.step > 0) representedRequests.add(`${partial.turn}\u0000${partial.step}`);
			for (const call of runningCalls) if (call.step > 0) representedRequests.add(`${call.turn}\u0000${call.step}`);
			const entries = [
				...(input.systemPrompts ?? []).map((prompt) => ({
					kind: "system",
					seq: prompt.seq,
					systemPrompt: prompt.text,
					change: {
						seq: prompt.seq,
						time: prompt.time,
						kind: prompt.update ? "system" : "initial"
					}
				})),
				...nodes.map((node, nodeIndex) => ({
					kind: "node",
					seq: node.seq,
					node,
					nodeIndex
				})),
				...requests.filter((request) => request.purpose === "compaction").map((request) => ({
					kind: "compaction",
					seq: request.startSeq,
					request
				})),
				...requests.flatMap((request) => request.purpose !== "assistant" || request.promptChange === void 0 || request.prompt === void 0 ? [] : [{
					kind: "system",
					seq: request.promptChange.seq,
					request,
					change: request.promptChange
				}]),
				...requests.filter((request) => request.purpose === "assistant").filter((request) => !representedRequests.has(`${request.turn}\u0000${request.step}`)).map((request) => ({
					kind: "request",
					seq: request.startSeq,
					request
				}))
			].sort((left, right) => layoutEntryOrder(left) - layoutEntryOrder(right));
			for (const entry of entries) {
				if (entry.kind === "request") {
					const { request } = entry;
					pushStep(request.turn, request.step, [{
						absTime: finiteTime(request.startedAt),
						cell: {
							index: ++index,
							kind: "message",
							text: "",
							sourceSeq: request.startSeq,
							requestOnly: true,
							timeSeconds: request.completedAt === null ? null : durationSeconds(request.completedAt, request.startedAt),
							startedAt: finiteTime(request.startedAt),
							...request.status === "error" ? { isError: true } : {}
						}
					}]);
					prevAbsTime = finiteTime(request.completedAt) ?? finiteTime(request.startedAt) ?? prevAbsTime;
					continue;
				}
				if (entry.kind === "system") {
					const { change, request } = entry;
					pushMessage(change.kind === "initial" ? firstVisibleTurn(nodes, partial) : enclosingPromptTurn(nodes, change.seq, partial), {
						absTime: finiteTime(change.time),
						cell: {
							index: ++index,
							kind: "system",
							text: promptChangeLabel(change, t),
							sourceSeq: change.seq,
							...request?.prompt === void 0 ? {} : { promptDetail: request.prompt },
							...entry.systemPrompt === void 0 ? {} : { systemPromptDetail: entry.systemPrompt },
							...change.previous === void 0 ? {} : { previousPromptDetail: change.previous },
							timeSeconds: 0,
							startedAt: finiteTime(change.time)
						}
					});
					prevAbsTime = finiteTime(change.time) ?? prevAbsTime;
					continue;
				}
				if (entry.kind === "compaction") {
					const request = entry.request;
					const rawOutput = request.rawOutput ?? request.summary;
					const thinkingDetail = rawOutput === void 0 ? "" : detailReasoning(rawOutput);
					const cell = {
						index: ++index,
						kind: "compacted",
						text: request.status === "running" ? t("layout.compacting") : request.status === "error" ? request.error === "trajectory.compaction-interrupted" ? t("layout.compactionInterrupted") : request.error ?? t("layout.compactionFailed") : request.summary === void 0 ? t("layout.compacted") : "",
						...request.status === "complete" && request.summary !== void 0 ? previewContentProperty(request.summary) : {},
						sourceSeq: request.startSeq,
						...request.summary === void 0 ? {} : {
							outputDetail: detailContent(request.summary),
							outputBlocks: request.summary.map((block) => sourceBlock(block))
						},
						...thinkingDetail === "" ? {} : { thinkingDetail },
						...rawOutput === void 0 ? {} : { sourceBlocks: rawOutput.map((block) => sourceBlock(block)) },
						...request.status === "error" ? { isError: true } : {},
						timeSeconds: request.completedAt === null ? null : durationSeconds(request.completedAt, request.startedAt),
						startedAt: finiteTime(request.startedAt)
					};
					attachUsage(cell, request.usage);
					const compaction = { groups: [{
						title: t("group.compaction", { seq: request.startSeq }),
						laid: [{
							absTime: finiteTime(request.startedAt),
							cell
						}]
					}] };
					if (request.turn === null) standaloneCompactions.push(compaction);
					else bucket(request.turn).groups.push(...compaction.groups);
					prevAbsTime = finiteTime(request.completedAt) ?? finiteTime(request.startedAt) ?? prevAbsTime;
					continue;
				}
				const { node, nodeIndex: i } = entry;
				if (node.kind === "user") {
					pushMessage(enclosingUserTurn(followingAssistants[i], partial, lastAssistantTurn), {
						absTime: finiteTime(node.time),
						cell: {
							index: ++index,
							kind: "user",
							...inputCellDetail(node, t),
							opensTurn: true
						}
					});
					prevAbsTime = finiteTime(node.time) ?? prevAbsTime;
					continue;
				}
				if (node.kind === "steering") {
					const placement = steeringPlacement(followingAssistants[i], partial, lastAssistantTurn, eventLocations?.get(node.seq));
					const laid = {
						absTime: finiteTime(node.time),
						cell: {
							index: ++index,
							kind: "user",
							...inputCellDetail(node, t)
						}
					};
					if (placement.step === void 0) pushMessage(placement.turn, laid);
					else pushStepInput(placement.turn, placement.step, [laid]);
					prevAbsTime = finiteTime(node.time) ?? prevAbsTime;
					continue;
				}
				if (node.kind === "assistant") {
					const laidList = withSubCalls(expandAssistant(node, index + 1, prevAbsTime, resultByCall, callStartById, callById, t), t);
					if (node.step > 0) pushStep(node.turn, node.step, laidList);
					else for (const laid of laidList) pushMessage(node.turn, laid);
					const last = laidList[laidList.length - 1];
					if (last !== void 0) index = last.cell.index;
					prevAbsTime = finiteTime(node.time) ?? prevAbsTime;
					lastAssistantTurn = node.turn;
					continue;
				}
				if (node.kind === "context") {
					pushMessage(enclosingUserTurn(followingAssistants[i], partial, lastAssistantTurn), {
						absTime: finiteTime(node.time),
						cell: {
							index: ++index,
							kind: "context",
							...inputCellDetail(node, t)
						}
					});
					prevAbsTime = finiteTime(node.time) ?? prevAbsTime;
					continue;
				}
				if (node.kind === "compaction") {
					prevAbsTime = finiteTime(node.time) ?? prevAbsTime;
					continue;
				}
				if (node.kind === "tool-result") {
					if (!emittedCallIds.has(node.callId)) {
						const toolName = node.call?.name;
						const resultPreview = summarizeResult(node, t);
						const laidList = [{
							absTime: finiteTime(node.callTime ?? node.time),
							...toolName !== void 0 ? { toolName } : {},
							callId: node.callId,
							subCalls: node.subCalls,
							cell: {
								index: ++index,
								kind: "tool",
								sourceSeq: node.seq,
								...node.call !== null ? summarizeCall(node.call.name, node.call.argsRaw) : resultAsText(resultPreview),
								...node.call !== null ? { inputDetail: node.call.argsRaw } : {},
								outputDetail: detailResult(node, t),
								outputBlocks: node.content.map((block) => sourceBlock(block)),
								...resultPreview,
								callId: node.callId,
								isError: node.isError,
								timeSeconds: durationSeconds(node.time, node.callTime),
								startedAt: finiteTime(node.callTime)
							}
						}];
						for (const laid of expandSubCalls(node.subCalls, index, t)) {
							laidList.push(laid);
							index = laid.cell.index;
						}
						pushStep(0, 1, laidList);
					}
					prevAbsTime = finiteTime(node.time) ?? prevAbsTime;
				}
			}
			if (partial !== null) {
				const laidList = withSubCalls(expandAssistant({
					kind: "assistant",
					seq: Number.MAX_SAFE_INTEGER,
					time: 0,
					turn: partial.turn,
					step: partial.step,
					blocks: partial.blocks
				}, index + 1, prevAbsTime, resultByCall, callStartById, callById, t, { streaming: true }), t);
				if (partial.step > 0) pushStep(partial.turn, partial.step, laidList);
				else for (const laid of laidList) pushMessage(partial.turn, laid);
				const last = laidList[laidList.length - 1];
				if (last !== void 0) index = last.cell.index;
			}
			const seenCalls = collectCallIds(turns);
			for (const call of runningCalls) {
				if (call.phase === "preparing" || seenCalls.has(call.callId)) continue;
				const laidList = [{
					absTime: null,
					toolName: call.name,
					callId: call.callId,
					subCalls: call.subCalls,
					cell: {
						index: ++index,
						kind: "tool",
						...summarizeCall(call.name, call.argsRaw),
						inputDetail: call.argsRaw,
						callId: call.callId,
						timeSeconds: null,
						startedAt: finiteTime(call.time)
					}
				}];
				for (const laid of expandSubCalls(call.subCalls, index, t)) {
					laidList.push(laid);
					index = laid.cell.index;
				}
				if (call.step > 0) pushStep(call.turn, call.step, laidList);
				else for (const laid of laidList) pushMessage(call.turn, laid);
			}
			const prologue = turns.get(0);
			if (prologue !== void 0) {
				turns.delete(0);
				const emptyTurn = () => ({ groups: [] });
				const first = turns.get(1) ?? emptyTurn();
				first.groups = [...prologue.groups, ...first.groups];
				turns.set(1, first);
			}
			for (const entry of [...turns.values(), ...standaloneCompactions]) for (const group of entry.groups) for (const laid of group.laid) attachToolSchema(laid, callSchemas);
			return [...[...turns.entries()].map(([turn, entry]) => toTurnModel(turn, entry, t)), ...standaloneCompactions.map((entry) => toTurnModel(null, entry, t))].sort((left, right) => firstCellIndex(left) - firstCellIndex(right));
		}
		/**
		* Append the changing in-flight assistant cells to a stable finalized layout.
		* @param turns - Finalized layout derived with an empty-block partial anchor.
		* @param partial - Current in-flight assistant projection.
		* @param lastIndex - Highest cell index in the finalized layout.
		* @param t - Trajectory locale translator.
		* @returns The original layout without a partial, otherwise a layout sharing every unaffected turn.
		*/
		function appendTrajectoryPartialLayout(turns, partial, lastIndex, t) {
			if (partial === null) return turns;
			const partialTurn = deriveTrajectoryLayout({
				nodes: [],
				partial,
				runningCalls: []
			}, t).at(0);
			if (partialTurn === void 0) return turns;
			const streamed = {
				...partialTurn,
				groups: partialTurn.groups.map((group) => ({
					...group,
					cells: group.cells.map((cell) => ({
						...cell,
						index: cell.index + lastIndex
					}))
				}))
			};
			const turnIndex = turns.findIndex((turn) => turn.turn === streamed.turn);
			if (turnIndex === -1) return [...turns, streamed];
			const current = turns[turnIndex];
			/* v8 ignore next -- findIndex proved the dense array position exists. */
			if (current === void 0) return turns;
			const groups = [...current.groups];
			for (const streamedGroup of streamed.groups) {
				const groupIndex = groups.findIndex((group) => group.title === streamedGroup.title);
				if (groupIndex === -1) {
					groups.push(streamedGroup);
					continue;
				}
				const group = groups[groupIndex];
				/* v8 ignore next -- findIndex proved the dense array position exists. */
				if (group === void 0) continue;
				const streamedCallIds = new Set(streamedGroup.cells.flatMap((cell) => cell.callId === void 0 ? [] : [cell.callId]));
				groups[groupIndex] = {
					...streamedGroup,
					cells: [...group.cells.filter((cell) => cell.requestOnly !== true && (cell.callId === void 0 || !streamedCallIds.has(cell.callId))), ...streamedGroup.cells]
				};
			}
			const updated = [...turns];
			updated[turnIndex] = {
				...current,
				groups
			};
			return updated;
		}
		function attachToolSchema(laid, callSchemas) {
			if (laid.callId === void 0 || callSchemas === void 0) return;
			const schema = callSchemas.get(laid.callId);
			if (schema === void 0) return;
			laid.cell.schemaDetail = JSON.stringify(schema, null, 2);
		}
		function toTurnModel(turn, entry, t) {
			return {
				turn,
				groups: entry.groups.map(({ title, laid }) => {
					const description = groupDescription(laid, t);
					return {
						title,
						...description !== void 0 ? { description } : {},
						cells: laid.map((l) => l.cell)
					};
				})
			};
		}
		/** Chronological section position from the fold's monotonically assigned cell indexes. */
		function firstCellIndex(turn) {
			return Math.min(...turn.groups.flatMap((group) => group.cells.map((cell) => cell.index)), Number.POSITIVE_INFINITY);
		}
		/** Wall-span duration + tool histogram, e.g. `1.5 s bash×6`. */
		function groupDescription(laid, t) {
			const parts = [];
			const times = [];
			for (const l of laid) {
				if (l.absTime === null || !Number.isFinite(l.absTime)) continue;
				times.push(l.absTime);
				if (l.cell.kind === "tool" && l.cell.timeSeconds !== null && Number.isFinite(l.cell.timeSeconds)) times.push(l.absTime + l.cell.timeSeconds * 1e3);
			}
			if (times.length >= 2) {
				const span = formatGroupDuration((Math.max(...times) - Math.min(...times)) / 1e3, t);
				if (span !== void 0) parts.push(span);
			} else if (times.length === 1) {
				const own = laid.find((l) => l.absTime === times[0])?.cell.timeSeconds;
				const span = own !== null && own !== void 0 ? formatGroupDuration(own, t) : void 0;
				if (span !== void 0) parts.push(span);
			}
			const tools = /* @__PURE__ */ new Map();
			for (const l of laid) {
				if (l.toolName === void 0 || l.cell.kind !== "tool") continue;
				tools.set(l.toolName, (tools.get(l.toolName) ?? 0) + 1);
			}
			for (const [name, count] of tools) parts.push(count > 1 ? `${name}×${count}` : name);
			return parts.length === 0 ? void 0 : parts.join(" ");
		}
		function formatGroupDuration(seconds, t) {
			if (!Number.isFinite(seconds)) return void 0;
			return formatElapsedSeconds(seconds, t);
		}
		/** Own-duration seconds from two epoch-ms stamps; null when either is unusable. */
		function durationSeconds(later, earlier) {
			if (earlier === null || !Number.isFinite(later) || !Number.isFinite(earlier)) return null;
			return Math.max(0, (later - earlier) / 1e3);
		}
		/** Epoch-ms usable as an absolute time, else null. */
		function finiteTime(time) {
			return typeof time === "number" && Number.isFinite(time) ? time : null;
		}
		function expandAssistant(node, startIndex, prevAbsTime, results, callStarts, calls, t, opts) {
			if (opts?.streaming === true && node.blocks.length === 0) return [];
			const out = [];
			let index = startIndex - 1;
			const usage = node.usage;
			const streaming = opts?.streaming === true;
			const recordedStart = finiteTime(node.timing?.stepStartTime);
			const messageDuration = streaming ? null : durationSeconds(node.time, recordedStart ?? prevAbsTime);
			const nodeAbs = streaming ? null : finiteTime(node.time);
			const messageText = node.blocks.filter((block) => block.kind === "text" && (!streaming || block.text !== "")).map((block) => block.kind === "text" ? block.text : "").join("\n\n");
			const thinkingText = node.blocks.filter((block) => block.kind === "reasoning" && (!streaming || block.text !== "")).map((block) => block.kind === "reasoning" ? block.text : "").join("\n\n");
			const message = {
				index: ++index,
				recordId: `assistant\u0000${node.turn}\u0000${node.step}`,
				kind: "message",
				sourceSeq: node.seq,
				text: messageText !== "" || thinkingText !== "" ? "" : summarizeAssistantActivity(node.blocks, t),
				...messageText !== "" ? { previewMarkdown: messageText } : thinkingText !== "" ? { previewMarkdown: thinkingText } : {},
				...messageText !== "" ? { outputDetail: messageText } : {},
				...thinkingText !== "" ? { thinkingDetail: thinkingText } : {},
				sourceBlocks: node.blocks.map((block) => assistantSourceBlock(block)),
				timeSeconds: messageDuration,
				startedAt: recordedStart
			};
			attachUsage(message, usage);
			message.assistantMetrics = {
				timingRecorded: node.timing !== void 0,
				stepStartTime: node.timing?.stepStartTime ?? null,
				firstTokenTime: node.timing?.firstTokenTime ?? null,
				completedTime: streaming ? null : finiteTime(node.time),
				usageProvided: usage !== void 0,
				outputTokens: Number.isFinite(usage?.outputTokens) ? usage?.outputTokens ?? null : null
			};
			out.push({
				absTime: nodeAbs,
				cell: message
			});
			for (const block of node.blocks) {
				if (block.kind !== "tool-call") continue;
				const result = results.get(block.callId);
				const toolDuration = streaming || result === void 0 ? null : durationSeconds(result.time, result.callTime);
				const callAbs = finiteTime(callStarts.get(block.callId));
				const call = calls.get(block.callId);
				const resultPreview = result === void 0 ? void 0 : summarizeResult(result, t);
				out.push({
					absTime: callAbs,
					toolName: block.name,
					callId: block.callId,
					...call === void 0 ? {} : { subCalls: call.subCalls },
					cell: {
						index: ++index,
						kind: "tool",
						...summarizeCall(block.name, block.argsRaw),
						inputDetail: block.argsRaw,
						callId: block.callId,
						...result !== void 0 ? {
							outputDetail: detailResult(result, t),
							outputBlocks: result.content.map((block) => sourceBlock(block)),
							...resultPreview,
							isError: result.isError
						} : {},
						timeSeconds: toolDuration,
						startedAt: callAbs
					}
				});
			}
			return out;
		}
		function summarizeAssistantActivity(blocks, t) {
			const tools = /* @__PURE__ */ new Map();
			for (const block of blocks) {
				if (block.kind !== "tool-call") continue;
				tools.set(block.name, (tools.get(block.name) ?? 0) + 1);
			}
			if (tools.size > 0) return t("layout.toolCallOnly");
			const images = blocks.filter((block) => block.kind === "image").length;
			if (images > 0) return t("layout.imageCount", { count: images });
			return "";
		}
		function promptChangeLabel(change, t) {
			if (change.kind === "initial") return t("layout.initialSystemPrompt");
			if (change.kind === "system") return t("layout.systemPromptUpdated");
			if (change.kind === "tools") return t("layout.toolsUpdated");
			return t("layout.systemPromptAndToolsUpdated");
		}
		function assistantSourceBlock(block) {
			switch (block.kind) {
				case "text": return {
					type: "text",
					content: block.text
				};
				case "reasoning": return {
					type: "thinking",
					content: block.text
				};
				case "tool-call": return {
					type: "tool-call",
					content: block.argsRaw,
					callId: block.callId,
					toolName: block.name
				};
				case "image": return sourceBlock({
					type: "image",
					attachment: block.attachment
				});
				case "other": return sourceBlock(block.block);
			}
		}
		function sourceBlock(value) {
			if (typeof value !== "object" || value === null) return {
				type: "unknown",
				content: stringifySourceValue(value)
			};
			const block = value;
			const type = typeof block.type === "string" ? block.type : "unknown";
			if (typeof block.text === "string") return {
				type: type === "reasoning" ? "thinking" : type,
				content: block.text
			};
			if ((type === "image" || type === "file") && typeof block.attachment === "object" && block.attachment !== null && typeof block.attachment.attachmentId === "string") return {
				type,
				content: stringifySourceValue(value),
				...type === "image" ? { attachment: block.attachment } : { file: block.attachment }
			};
			return {
				type,
				content: stringifySourceValue(value)
			};
		}
		function imageBlockCount(content) {
			return content.filter((block) => block.type === "image").length;
		}
		function fileBlockCount(content) {
			return content.filter((block) => block.type === "file").length;
		}
		function stringifySourceValue(value) {
			return JSON.stringify(value, null, 2) || String(value);
		}
		/**
		* Turn that encloses a user/message: next assistant turn, else the
		* in-flight partial, else the turn after the last finalized assistant (or 1).
		*/
		function enclosingUserTurn(followingAssistant, partial, lastAssistantTurn) {
			if (followingAssistant !== void 0) return followingAssistant.turn;
			if (partial !== null) return partial.turn;
			if (lastAssistantTurn !== null) return lastAssistantTurn + 1;
			return 1;
		}
		function steeringPlacement(followingAssistant, partial, lastAssistantTurn, location) {
			if (location?.kind === "step") return {
				turn: location.turn.turn,
				step: location.step.step
			};
			const locatedTurn = location?.kind === "turn" ? location.turn.turn : void 0;
			if (followingAssistant !== void 0 && (locatedTurn === void 0 || followingAssistant.turn === locatedTurn)) return {
				turn: followingAssistant.turn,
				...followingAssistant.step > 0 ? { step: followingAssistant.step } : {}
			};
			if (partial !== null && (locatedTurn === void 0 || partial.turn === locatedTurn)) return {
				turn: partial.turn,
				...partial.step > 0 ? { step: partial.step } : {}
			};
			if (locatedTurn !== void 0) return { turn: locatedTurn };
			return { turn: lastAssistantTurn ?? 1 };
		}
		function indexFollowingAssistants(nodes) {
			const following = new Array(nodes.length);
			let assistant;
			for (let index = nodes.length - 1; index >= 0; index--) {
				following[index] = assistant;
				const node = nodes[index];
				if (node?.kind === "assistant") assistant = node;
			}
			return following;
		}
		function enclosingPromptTurn(nodes, seq, partial) {
			const next = nodes.find((node) => node.seq > seq && node.kind === "assistant" && node.step > 0);
			if (next?.kind === "assistant") return next.turn;
			return partial?.turn ?? 1;
		}
		/** Earliest raw turn represented by the selected trajectory branch. */
		function firstVisibleTurn(nodes, partial) {
			const turns = nodes.flatMap((node) => node.kind === "assistant" && node.turn > 0 ? [node.turn] : []);
			if (partial !== null && partial.turn > 0) turns.push(partial.turn);
			return turns.length === 0 ? 1 : Math.min(...turns);
		}
		/** Copy provider usage onto a Message cell when present. */
		function attachUsage(cell, usage) {
			if (usage === void 0) return;
			if (usage.inputTokens !== void 0) cell.input = usage.inputTokens;
			if (usage.cacheReadTokens !== void 0) cell.cacheRead = usage.cacheReadTokens;
			if (usage.cacheWriteTokens !== void 0) cell.cacheWrite = usage.cacheWriteTokens;
			if (usage.outputTokens !== void 0) cell.output = usage.outputTokens;
			if (usage.reasoningTokens !== void 0) cell.think = usage.reasoningTokens;
		}
		function indexResults(nodes) {
			const map = /* @__PURE__ */ new Map();
			for (const node of nodes) if (node.kind === "tool-result") map.set(node.callId, node);
			return map;
		}
		function indexAssistantCallIds(nodes) {
			const ids = /* @__PURE__ */ new Set();
			for (const node of nodes) {
				if (node.kind !== "assistant") continue;
				for (const block of node.blocks) if (block.kind === "tool-call") ids.add(block.callId);
			}
			return ids;
		}
		function collectCallIds(turns) {
			const ids = /* @__PURE__ */ new Set();
			for (const entry of turns.values()) for (const group of entry.groups) for (const laid of group.laid) if (laid.callId !== void 0) ids.add(laid.callId);
			return ids;
		}
		/** Interleave each tool cell's nested child calls right after it, reindexing followers. */
		function withSubCalls(laidList, t) {
			if (!laidList.some((laid) => laid.subCalls !== void 0 && laid.subCalls.length > 0)) return laidList;
			const out = [];
			let index = laidList[0] !== void 0 ? laidList[0].cell.index - 1 : 0;
			for (const laid of laidList) {
				out.push({
					...laid,
					cell: {
						...laid.cell,
						index: ++index
					}
				});
				for (const sub of expandSubCalls(laid.subCalls, index, t)) {
					out.push(sub);
					index = sub.cell.index;
				}
			}
			return out;
		}
		/** Sub-dispatch cells for one run_code parent, in start order (running = null duration). */
		function expandSubCalls(subs, startIndex, t) {
			if (subs === void 0 || subs.length === 0) return [];
			const out = [];
			let index = startIndex;
			for (const sub of subs) {
				if (!("kind" in sub) && sub.phase === "preparing") continue;
				const settled = "kind" in sub;
				const resultPreview = settled ? summarizeResult(sub, t) : void 0;
				const laid = {
					absTime: settled ? finiteTime(sub.callTime ?? sub.time) : finiteTime(sub.time),
					toolName: settled ? sub.call?.name ?? sub.callId : sub.name,
					callId: sub.callId,
					cell: {
						index: ++index,
						kind: "subtool",
						callId: sub.callId,
						...settled ? sub.call !== null ? summarizeCall(sub.call.name, sub.call.argsRaw) : resultAsText(resultPreview) : summarizeCall(sub.name, sub.argsRaw),
						...settled ? sub.call !== null ? { inputDetail: sub.call.argsRaw } : {} : { inputDetail: sub.argsRaw },
						...settled ? {
							outputDetail: detailResult(sub, t),
							outputBlocks: sub.content.map((block) => sourceBlock(block)),
							...resultPreview,
							isError: sub.isError
						} : {},
						timeSeconds: settled ? durationSeconds(sub.time, sub.callTime) : null,
						startedAt: settled ? finiteTime(sub.callTime) : finiteTime(sub.time)
					}
				};
				out.push(laid);
				for (const child of expandSubCalls(sub.subCalls, index, t)) {
					out.push(child);
					index = child.cell.index;
				}
			}
			return out;
		}
		function summarizeCall(name, argsRaw) {
			return {
				toolName: name,
				text: name,
				...argsRaw === "" ? {} : { previewMarkdown: argsRaw }
			};
		}
		function summarizeResult(node, t) {
			if (node.isError) return { result: node.error?.code ?? "error" };
			for (const block of node.content) if (block.type === "text" && typeof block.text === "string" && block.text !== "") return {
				result: "",
				resultPreviewMarkdown: block.text
			};
			const images = imageBlockCount(node.content);
			if (images > 0) return { result: t("layout.imageCount", { count: images }) };
			return { result: t("record.noOutput") };
		}
		function resultAsText(result) {
			return {
				text: result?.result ?? "",
				...result?.resultPreviewMarkdown === void 0 ? {} : { previewMarkdown: result.resultPreviewMarkdown }
			};
		}
		function detailResult(node, t) {
			if (node.isError) return node.error === void 0 ? "error" : `${node.error.name}: ${node.error.code}`;
			const text = node.content.filter((block) => block.type === "text" && typeof block.text === "string").map((block) => block.type === "text" ? block.text : "").join("\n");
			if (text !== "") return text;
			const images = imageBlockCount(node.content);
			if (images > 0) return t("layout.imageCount", { count: images });
			if (node.content.length === 0 || node.content.every((block) => block.type === "text" && (typeof block.text !== "string" || block.text === ""))) return t("record.noOutput");
			return JSON.stringify(node.content, null, 2);
		}
		function detailContent(content) {
			return content.filter((block) => block.type === "text" && typeof block.text === "string").map((block) => block.text ?? "").join("\n");
		}
		function detailReasoning(content) {
			return content.filter((block) => block.type === "reasoning" && typeof block.text === "string").map((block) => block.text ?? "").join("\n");
		}
		function previewContent(content) {
			for (const block of content) if (block.type === "text" && typeof block.text === "string") return block.text;
		}
		function previewContentProperty(content) {
			const previewMarkdown = previewContent(content);
			return previewMarkdown === void 0 ? {} : { previewMarkdown };
		}
		//#endregion
		//#region src/client/trajectory-search-index.ts
		function searchableJson(value) {
			if (value === void 0) return "";
			try {
				return JSON.stringify(value);
			} catch {
				return "";
			}
		}
		function sameSources(left, right) {
			return left.length === right.length && left.every((value, index) => value === right[index]);
		}
		function markdownPreview(cell) {
			if (cell.previewMarkdown === void 0) return "";
			const preview = trajectoryPreviewText(cell.previewMarkdown);
			if (cell.text === "") return preview;
			return preview === "" ? cell.text : `${cell.text} · ${preview}`;
		}
		function resultPreview(cell) {
			return cell.resultPreviewMarkdown === void 0 ? cell.result ?? "" : trajectoryPreviewText(cell.resultPreviewMarkdown);
		}
		function recordSources(turn, group, cell) {
			const blocks = [...cell.sourceBlocks ?? [], ...cell.outputBlocks ?? []];
			return [
				turn === null ? "between turns" : `turn ${turn}`,
				group,
				cell.kind,
				cell.kind === "message" ? "assistant" : "",
				cell.text,
				cell.previewMarkdown ?? "",
				cell.inputDetail ?? "",
				cell.outputDetail ?? "",
				cell.thinkingDetail ?? "",
				cell.schemaDetail ?? "",
				cell.result ?? "",
				cell.resultPreviewMarkdown ?? "",
				cell.callId ?? "",
				...blocks.flatMap((block) => [
					block.type,
					block.content,
					block.callId ?? "",
					block.toolName ?? "",
					block.attachment?.name ?? ""
				]),
				searchableJson(cell.messageSource),
				searchableJson(cell.promptDetail),
				searchableJson(cell.previousPromptDetail)
			];
		}
		/** Session-view-local index that reparses Markdown only when one record's source changes. */
		var TrajectorySearchIndex = class {
			entries = /* @__PURE__ */ new Map();
			layouts;
			/**
			* Incrementally synchronize one or more current trajectory layout slices.
			* @param layouts - Finalized and optional streaming layouts from the same view.
			* @returns Whether the indexed layout version changed.
			*/
			update(layouts) {
				if (this.layouts === layouts) return false;
				this.layouts = layouts;
				const seen = /* @__PURE__ */ new Set();
				for (const turns of layouts) for (const turn of turns) for (const group of turn.groups) for (const cell of group.cells) {
					if (cell.requestOnly === true) continue;
					const id = trajectoryRecordId(cell);
					const sources = recordSources(turn.turn, group.title, cell);
					const previous = this.entries.get(id);
					const entry = previous !== void 0 && sameSources(previous.sources, sources) ? previous : {
						sources,
						text: [
							...sources,
							markdownPreview(cell),
							resultPreview(cell)
						].join("\n").toLocaleLowerCase()
					};
					this.entries.set(id, entry);
					seen.add(id);
				}
				for (const id of this.entries.keys()) if (!seen.has(id)) this.entries.delete(id);
				return true;
			}
			/**
			* Match a query against the latest committed index version.
			* @param query - Space-separated case-insensitive search terms.
			* @returns Matching stable record identities, or `null` without a query.
			*/
			search(query) {
				const terms = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
				if (terms.length === 0) return null;
				const matches = /* @__PURE__ */ new Set();
				for (const [id, entry] of this.entries) if (terms.every((term) => entry.text.includes(term))) matches.add(id);
				return matches;
			}
		};
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-trajectory/src/client/views.module.css.mjs
		const css = ".ND_Nrq_root{--dsh-trajectory-toolbar-height:32px;box-sizing:border-box;width:100%;height:100%;min-height:0;color:var(--dsw-alias-label-primary);background:var(--dsw-alias-bg-layer-1);flex-direction:column;display:flex;overflow:hidden}.ND_Nrq_ledger{z-index:0;isolation:isolate;--dsh-trajectory-bottom-clearance:calc(var(--dsh-composer-height,152px) + 16px);flex:1;min-width:0;min-height:0;display:flex;position:relative;overflow:hidden}";
		const tagId = "@deepseek-ai/dsh-client-ui-trajectory/views.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-trajectory";
			tag.dataset.pluginCss = tagId;
			tag.textContent = css;
			document.head.appendChild(tag);
		}
		var views_module_css_default = {
			"ledger": "ND_Nrq_ledger",
			"root": "ND_Nrq_root"
		};
		//#endregion
		//#region src/client/TrajectoryView.tsx
		/** Trajectory view: compact summary over a turn-aware event ledger. */
		const EMPTY_TURN_IDS = /* @__PURE__ */ new Set();
		const EMPTY_RECORD_IDS = /* @__PURE__ */ new Set();
		const SEARCH_INDEX_THROTTLE_MS = 3e3;
		const HISTORY_PAGE_NODES = 50;
		function containsCall(calls, callId) {
			for (const call of calls) if (call.callId === callId || containsCall(call.subCalls, callId)) return true;
			return false;
		}
		function lastCellIndex(turns) {
			let last = 0;
			for (const turn of turns) for (const group of turn.groups) for (const cell of group.cells) last = Math.max(last, cell.index);
			return last;
		}
		function timelineBlock(block) {
			switch (block.kind) {
				case "text": return {
					kind: "text",
					text: ""
				};
				case "reasoning": return {
					kind: "reasoning",
					text: ""
				};
				case "image": return block;
				case "tool-call": return {
					kind: "tool-call",
					callId: block.callId,
					name: block.name,
					argsRaw: ""
				};
				case "other": return {
					kind: "other",
					block: null
				};
			}
		}
		function partialStructureSignature(partial) {
			if (partial === null) return "";
			return partial.blocks.map((block) => block.kind === "tool-call" ? `${block.kind}:${block.callId}:${block.name}` : block.kind).join("\0");
		}
		function requestUsage(value) {
			const usage = value;
			if (usage === void 0) return void 0;
			return {
				...usage.inputTokens === void 0 ? {} : { input: usage.inputTokens },
				...usage.cacheReadTokens === void 0 ? {} : { cacheRead: usage.cacheReadTokens },
				...usage.cacheWriteTokens === void 0 ? {} : { cacheWrite: usage.cacheWriteTokens },
				...usage.outputTokens === void 0 ? {} : { output: usage.outputTokens },
				...usage.reasoningTokens === void 0 ? {} : { reasoning: usage.reasoningTokens }
			};
		}
		function addUsage(total, usage) {
			if (usage === void 0) return total;
			return {
				...total?.input === void 0 && usage.input === void 0 ? {} : { input: (total?.input ?? 0) + (usage.input ?? 0) },
				...total?.cacheRead === void 0 && usage.cacheRead === void 0 ? {} : { cacheRead: (total?.cacheRead ?? 0) + (usage.cacheRead ?? 0) },
				...total?.cacheWrite === void 0 && usage.cacheWrite === void 0 ? {} : { cacheWrite: (total?.cacheWrite ?? 0) + (usage.cacheWrite ?? 0) },
				...total?.output === void 0 && usage.output === void 0 ? {} : { output: (total?.output ?? 0) + (usage.output ?? 0) },
				...total?.reasoning === void 0 && usage.reasoning === void 0 ? {} : { reasoning: (total?.reasoning ?? 0) + (usage.reasoning ?? 0) }
			};
		}
		function TrajectoryView({ useSession, useTrajectory, useDuration, loadOlder, loadImage, setActualDuration, viewRequest, completeViewRequest, renderSlot, t, jsonStringWrapping }) {
			const [collapsedTurns, setCollapsedTurns] = (0, react.useState)(EMPTY_TURN_IDS);
			const renderImages = (0, react.useCallback)((owner) => renderSlot("conversation.trajectory.images", {
				...owner,
				loadImage
			}), [loadImage, renderSlot]);
			const [collapsedAssistants, setCollapsedAssistants] = (0, react.useState)(EMPTY_RECORD_IDS);
			const [timelineSelection, setTimelineSelection] = (0, react.useState)(null);
			const actualDuration = useDuration((value) => value);
			const [actualTime, setActualTime] = (0, react.useState)(false);
			const [searchQuery, setSearchQuery] = (0, react.useState)("");
			const [searchIndex] = (0, react.useState)(() => new TrajectorySearchIndex());
			const [searchIndexRevision, setSearchIndexRevision] = (0, react.useState)(0);
			const searchIndexTimer = (0, react.useRef)(null);
			const searchIndexInitialized = (0, react.useRef)(false);
			const [selectedTimelineIndex, setSelectedTimelineIndex] = (0, react.useState)(null);
			const [timelineRecordSelection, setTimelineRecordSelection] = (0, react.useState)(null);
			const [timelineRecordFocus, setTimelineRecordFocus] = (0, react.useState)(null);
			const completeInspection = useTrajectory((snapshot) => snapshot);
			const latestNodeSeq = completeInspection.eventNodes.at(-1)?.seq;
			const [historyTailSeq, setHistoryTailSeq] = (0, react.useState)(latestNodeSeq);
			const [historyNodeLimit, setHistoryNodeLimit] = (0, react.useState)(HISTORY_PAGE_NODES);
			const fixedTailSeq = historyTailSeq ?? latestNodeSeq;
			const historyTailIndex = fixedTailSeq === void 0 ? -1 : completeInspection.eventNodes.findLastIndex((node) => node.seq <= fixedTailSeq);
			const historyEndIndex = historyTailIndex < 0 && latestNodeSeq !== void 0 ? completeInspection.eventNodes.length : historyTailIndex + 1;
			const historyStartIndex = Math.max(0, historyEndIndex - historyNodeLimit);
			(0, react.useEffect)(() => {
				if (latestNodeSeq !== void 0 && (historyTailSeq === void 0 || historyTailIndex < 0)) setHistoryTailSeq(latestNodeSeq);
			}, [
				historyTailIndex,
				historyTailSeq,
				latestNodeSeq
			]);
			const inspection = (0, react.useMemo)(() => {
				if (historyStartIndex === 0) return completeInspection;
				const eventNodes = completeInspection.eventNodes.slice(historyStartIndex);
				const firstSeq = eventNodes[0]?.seq ?? 0;
				return {
					...completeInspection,
					eventNodes,
					requests: completeInspection.requests.filter((request) => request.startSeq >= firstSeq || (request.resultSeq ?? -1) >= firstSeq)
				};
			}, [completeInspection, historyStartIndex]);
			const historyLoading = useSession((snapshot) => snapshot.openState === "loading");
			const olderHistoryLoading = useSession((snapshot) => snapshot.loadingOlder);
			const sessionHasOlderHistory = useSession((snapshot) => snapshot.hasMore);
			const hasResidentOlderHistory = historyStartIndex > 0;
			const hasOlderHistory = hasResidentOlderHistory || sessionHasOlderHistory;
			const nodes = inspection.eventNodes;
			const eventLocations = inspection.eventLocations;
			const historyBaseSeq = nodes[0]?.seq ?? 0;
			const partial = inspection.partial;
			const runningCalls = inspection.runningCalls;
			const requests = inspection.requests;
			const callSchemas = inspection.callSchemas;
			const inspectCallId = viewRequest?.view === "trajectory" ? viewRequest.focus : null;
			const inspectNodeIndex = (0, react.useMemo)(() => inspectCallId === null ? -1 : completeInspection.eventNodes.findIndex((node) => node.kind === "assistant" ? node.blocks.some((block) => block.kind === "tool-call" && block.callId === inspectCallId) : node.kind === "tool-result" && containsCall([node], inspectCallId)), [completeInspection.eventNodes, inspectCallId]);
			(0, react.useEffect)(() => {
				if (inspectNodeIndex < 0 || inspectNodeIndex >= historyStartIndex) return;
				setHistoryNodeLimit((limit) => limit + historyStartIndex - inspectNodeIndex);
			}, [historyStartIndex, inspectNodeIndex]);
			const requestNumbers = (0, react.useMemo)(() => {
				const assistantsByStep = /* @__PURE__ */ new Map();
				for (const node of completeInspection.eventNodes) {
					if (node.kind !== "assistant" || node.step <= 0) continue;
					assistantsByStep.set(`${node.turn}\u0000${node.step}`, node);
				}
				const requestsByStep = new Map(completeInspection.requests.filter((request) => request.purpose === "assistant").map((request) => [`${request.turn}\u0000${request.step}`, request]));
				const orderedRequests = [...completeInspection.requests.map((request) => ({
					seq: request.startSeq,
					request,
					node: request.purpose === "assistant" ? assistantsByStep.get(`${request.turn}\u0000${request.step}`) : void 0
				})), ...[...assistantsByStep.entries()].flatMap(([key, node]) => requestsByStep.has(key) ? [] : [{
					seq: node.seq,
					request: void 0,
					node
				}])].sort((left, right) => left.seq - right.seq);
				const numbered = [];
				let cumulativeUsage;
				for (const [index, entry] of orderedRequests.entries()) {
					const usage = requestUsage(entry.request?.usage ?? entry.node?.usage);
					cumulativeUsage = addUsage(cumulativeUsage, usage);
					if (entry.request?.purpose !== "compaction") {
						const request = entry.request;
						const node = entry.node;
						const turn = request?.turn ?? node?.turn;
						const step = request?.step ?? node?.step;
						if (turn === void 0 || step === void 0) continue;
						const provider = request?.providerMetadata?.provider ?? node?.providerMetadata?.provider;
						const model = request?.providerMetadata?.model ?? node?.providerMetadata?.model;
						const requestConfig = request?.requestConfig ?? node?.requestConfig;
						numbered.push({
							seq: entry.seq,
							turn,
							step,
							group: t("group.step", { step }),
							number: index + 1,
							...request?.status === void 0 ? {} : { status: request.status },
							...request?.startedAt === void 0 ? {} : { startedAt: request.startedAt },
							...request?.completedAt === void 0 ? {} : { completedAt: request.completedAt },
							...request?.error === void 0 ? {} : { error: request.error },
							...request?.errorCode === void 0 ? {} : { errorCode: request.errorCode },
							...request?.resultSeq === void 0 ? {} : { resultSeq: request.resultSeq },
							...request?.retry === void 0 ? {} : { retry: request.retry },
							...request?.maxRetries === void 0 ? {} : { maxRetries: request.maxRetries },
							...request?.retryDelayMs === void 0 ? {} : { retryDelayMs: request.retryDelayMs },
							...provider === void 0 ? {} : { provider },
							...model === void 0 ? {} : { model },
							...requestConfig === void 0 ? {} : { requestConfig },
							...usage === void 0 ? {} : { usage },
							...cumulativeUsage === void 0 ? {} : { cumulativeUsage }
						});
						continue;
					}
					const request = entry.request;
					numbered.push({
						seq: request.startSeq,
						turn: request.turn,
						step: 0,
						group: t("group.compaction", { seq: request.startSeq }),
						number: index + 1,
						purpose: "compaction",
						status: request.status,
						startedAt: request.startedAt,
						completedAt: request.completedAt,
						...request.error === void 0 ? {} : { error: request.error },
						...request.errorCode === void 0 ? {} : { errorCode: request.errorCode },
						resultSeq: request.startSeq,
						...request.providerMetadata?.provider === void 0 ? {} : { provider: request.providerMetadata.provider },
						...request.providerMetadata?.model === void 0 ? {} : { model: request.providerMetadata.model },
						...request.requestConfig === void 0 ? {} : { requestConfig: request.requestConfig },
						...usage === void 0 ? {} : { usage },
						...cumulativeUsage === void 0 ? {} : { cumulativeUsage }
					});
				}
				return numbered;
			}, [
				completeInspection.eventNodes,
				completeInspection.requests,
				t
			]);
			const partialTurn = partial?.turn ?? null;
			const partialStep = partial?.step ?? null;
			const finalized = (0, react.useMemo)(() => {
				const turns = deriveTrajectoryLayout({
					nodes,
					eventLocations,
					partial: partialTurn === null || partialStep === null ? null : {
						turn: partialTurn,
						step: partialStep,
						blocks: []
					},
					runningCalls,
					requests,
					systemPrompts: inspection.systemPrompts,
					callSchemas
				}, t);
				return {
					turns,
					lastIndex: lastCellIndex(turns)
				};
			}, [
				nodes,
				eventLocations,
				partialTurn,
				partialStep,
				runningCalls,
				requests,
				inspection.systemPrompts,
				callSchemas,
				t
			]);
			const timelinePartial = (0, react.useMemo)(() => partial === null ? null : {
				turn: partial.turn,
				step: partial.step,
				blocks: partial.blocks.map((block) => timelineBlock(block))
			}, [
				partialStep,
				partialTurn,
				partialStructureSignature(partial)
			]);
			const timelineTurns = (0, react.useMemo)(() => appendTrajectoryPartialLayout(finalized.turns, timelinePartial, finalized.lastIndex, t), [
				finalized,
				timelinePartial,
				t
			]);
			const timelineMode = actualDuration ? actualTime ? "actual" : "duration" : actualTime ? "time" : "sequence";
			const partialSearchTurns = (0, react.useMemo)(() => appendTrajectoryPartialLayout([], partial, finalized.lastIndex, t), [
				finalized.lastIndex,
				partial,
				t
			]);
			const searchLayouts = (0, react.useMemo)(() => [finalized.turns, partialSearchTurns], [finalized, partialSearchTurns]);
			const latestSearchLayouts = (0, react.useRef)(searchLayouts);
			latestSearchLayouts.current = searchLayouts;
			(0, react.useEffect)(() => {
				if (!searchIndexInitialized.current) {
					searchIndexInitialized.current = true;
					if (searchIndex.update(searchLayouts)) setSearchIndexRevision((revision) => revision + 1);
					return;
				}
				if (searchIndexTimer.current !== null) return;
				searchIndexTimer.current = setTimeout(() => {
					searchIndexTimer.current = null;
					if (searchIndex.update(latestSearchLayouts.current)) setSearchIndexRevision((revision) => revision + 1);
				}, SEARCH_INDEX_THROTTLE_MS);
			}, [searchIndex, searchLayouts]);
			(0, react.useEffect)(() => () => {
				if (searchIndexTimer.current !== null) clearTimeout(searchIndexTimer.current);
			}, []);
			const streamingCells = (0, react.useMemo)(() => partialSearchTurns.flatMap((turn) => turn.groups.flatMap((group) => group.cells)), [partialSearchTurns]);
			const searchMatchRecordIds = (0, react.useMemo)(() => searchIndex.search(searchQuery), [
				searchIndex,
				searchIndexRevision,
				searchQuery
			]);
			const searchMatchIndexes = (0, react.useMemo)(() => {
				if (searchMatchRecordIds === null) return null;
				const indexes = /* @__PURE__ */ new Set();
				for (const turns of searchLayouts) for (const turn of turns) for (const group of turn.groups) for (const cell of group.cells) if (searchMatchRecordIds.has(trajectoryRecordId(cell))) indexes.add(cell.index);
				return indexes;
			}, [searchLayouts, searchMatchRecordIds]);
			const timelineRange = timelineSelection;
			const timelineFocusIndexes = (0, react.useMemo)(() => timelineRange === null ? null : trajectoryTimelineFocusIndexes(timelineTurns, timelineRange, timelineMode), [
				timelineMode,
				timelineRange,
				timelineTurns
			]);
			const handleRecordSelect = (0, react.useCallback)((index) => {
				if (timelineFocusIndexes !== null && !timelineFocusIndexes.has(index)) setTimelineSelection(null);
			}, [timelineFocusIndexes]);
			const handleTimelineRangeChange = (0, react.useCallback)((range) => {
				setTimelineSelection(range);
			}, []);
			const handleTimelineRecordSelect = (0, react.useCallback)((index) => {
				setTimelineSelection(null);
				setTimelineRecordSelection({ index });
				setSelectedTimelineIndex(index);
			}, []);
			const handleTimelineRecordFocus = (0, react.useCallback)((index) => {
				setTimelineRecordFocus({ index });
			}, []);
			const collapsibleTurnIds = (0, react.useMemo)(() => timelineTurns.filter((turn) => turn.turn !== null && turn.groups.reduce((count, group) => count + group.cells.filter((cell) => cell.requestOnly !== true && cell.kind !== "system").length, 0) > 1).flatMap((turn) => turn.turn === null ? [] : [turn.turn]), [timelineTurns]);
			const allTurnsCollapsed = collapsibleTurnIds.length > 0 && collapsibleTurnIds.every((turn) => collapsedTurns.has(turn));
			const collapsibleAssistantIds = (0, react.useMemo)(() => {
				const ids = [];
				for (const turn of timelineTurns) {
					const cells = turn.groups.flatMap((group) => group.cells);
					for (let i = 0; i < cells.length; i++) {
						const cell = cells[i];
						if (cell?.kind !== "message") continue;
						const next = cells[i + 1];
						if (next?.kind === "tool" || next?.kind === "subtool") ids.push(trajectoryRecordId(cell));
					}
				}
				return ids;
			}, [timelineTurns]);
			const allAssistantsCollapsed = collapsibleAssistantIds.length > 0 && collapsibleAssistantIds.every((index) => collapsedAssistants.has(index));
			const toggleTurn = (turn) => {
				setCollapsedTurns((current) => {
					const collapsed = new Set(current);
					if (collapsed.has(turn)) collapsed.delete(turn);
					else collapsed.add(turn);
					return collapsed;
				});
			};
			const toggleAllTurns = () => {
				setCollapsedTurns((current) => {
					const collapsed = new Set(current);
					if (allTurnsCollapsed) for (const turn of collapsibleTurnIds) collapsed.delete(turn);
					else for (const turn of collapsibleTurnIds) collapsed.add(turn);
					return collapsed;
				});
			};
			const toggleAssistant = (id) => {
				setCollapsedAssistants((current) => {
					const collapsed = new Set(current);
					if (collapsed.has(id)) collapsed.delete(id);
					else collapsed.add(id);
					return collapsed;
				});
			};
			const toggleAllAssistants = () => {
				setCollapsedAssistants((current) => {
					const collapsed = new Set(current);
					if (allAssistantsCollapsed) for (const index of collapsibleAssistantIds) collapsed.delete(index);
					else for (const index of collapsibleAssistantIds) collapsed.add(index);
					return collapsed;
				});
			};
			const loadEarlierHistory = (0, react.useCallback)(async () => {
				if (!hasResidentOlderHistory && !await loadOlder()) return false;
				setHistoryNodeLimit((limit) => limit + HISTORY_PAGE_NODES);
				return true;
			}, [hasResidentOlderHistory, loadOlder]);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: views_module_css_default.root,
				"data-conversation-composer-overlay": "",
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)(TrajectoryToolbar, {
						actualDuration,
						onActualDurationChange: (nextActualDuration) => {
							setActualDuration(nextActualDuration);
							setTimelineSelection(null);
						},
						actualTime,
						onActualTimeChange: (nextActualTime) => {
							setActualTime(nextActualTime);
							setTimelineSelection(null);
						},
						allTurnsCollapsed,
						onToggleAllTurns: toggleAllTurns,
						allAssistantsCollapsed,
						onToggleAllAssistants: toggleAllAssistants,
						searchQuery,
						onSearchQueryChange: setSearchQuery,
						t
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)(TrajectoryTimeline, {
						t,
						turns: timelineTurns,
						mode: timelineMode,
						range: timelineRange,
						hasEarlierRecords: hasOlderHistory,
						onLoadEarlier: loadEarlierHistory,
						selectedIndex: selectedTimelineIndex,
						searchMatchIndexes,
						onRangeChange: handleTimelineRangeChange,
						onRecordSelect: handleTimelineRecordSelect,
						onRecordFocus: handleTimelineRecordFocus
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						className: views_module_css_default.ledger,
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(TrajectoryTable, {
							t,
							stringWrapping: jsonStringWrapping === void 0 ? void 0 : {
								...jsonStringWrapping,
								label: t("record.wrapLines")
							},
							renderImages,
							requestNumbers,
							turns: timelineTurns,
							streamingCells,
							timelineFocusIndexes,
							searchMatchIndexes,
							onSelectedIndexChange: setSelectedTimelineIndex,
							onRecordSelect: handleRecordSelect,
							recordSelection: timelineRecordSelection,
							recordFocus: timelineRecordFocus,
							historyLoading,
							olderHistoryLoading,
							historyStartSeq: historyBaseSeq,
							hasOlderRecords: hasOlderHistory,
							onLoadOlder: loadEarlierHistory,
							onClearSelection: () => {
								setTimelineSelection(null);
							},
							collapsedTurns,
							onToggleTurn: toggleTurn,
							collapsedAssistants,
							onToggleAssistant: toggleAssistant,
							inspectCallId,
							onInspectApplied: completeViewRequest
						})
					})
				]
			});
		}
		//#endregion
		//#region src/client/index.ts
		/** Required services: the conversation slot, registries, ordinary Session paging, and the locale service. */
		const inject = [
			"slots",
			"sessions",
			"uiSession",
			"uiConversation",
			"locale"
		];
		/**
		* Client plugin body: register the trajectory view tab. The registration
		* rides the slot service's effect wrapper, so plugin unload removes the tab.
		* @param ctx - client root context.
		*/
		function apply(ctx) {
			const trajectorySources = /* @__PURE__ */ new WeakMap();
			const trajectorySource = (binding) => {
				let source = trajectorySources.get(binding);
				if (source === void 0) {
					const target = ctx.uiConversation.binding(binding).target("trajectory");
					source = {
						getSnapshot: () => target.getSnapshot() ?? EMPTY_TRAJECTORY_SNAPSHOT,
						subscribe: (listener) => target.subscribe(listener)
					};
					trajectorySources.set(binding, source);
				}
				return source;
			};
			ctx.effect(() => ctx.locale.register(NS, {
				zh,
				en
			}), "ui-trajectory: dictionaries");
			const t = ctx.locale.bind(NS);
			const duration = createTrajectoryDurationStore();
			const stringWrapping = createTrajectoryStringWrappingStore();
			registerTrajectoryMessageDefinitions(ctx);
			registerTrajectoryRequestHeaderDefinition(ctx);
			registerTrajectoryAssistantDefinition(ctx);
			registerTrajectoryToolDefinition(ctx);
			registerTrajectoryCompactionDefinitions(ctx);
			registerTrajectoryConversationView(ctx);
			ctx.uiSession.provide({
				hooks: ["trajectory"],
				resolve: (binding) => ({ hooks: { trajectory: trajectorySource(binding) } })
			});
			ctx.slots.inject("conversation.view", () => ctx.slots.register({
				name: "conversation.view",
				id: "trajectory",
				order: 10,
				locale: NS,
				label: () => t("view.trajectory"),
				children: { "conversation.trajectory.images": {
					kind: "single",
					scope: "session"
				} },
				inject: (sessionId) => {
					const session = ctx.sessions.binding(sessionId)?.session;
					if (session === void 0) throw new Error(`ui-trajectory: session "${sessionId}" is unavailable`);
					const trajectory = ctx.uiConversation.binding(sessionId).target("trajectory");
					return {
						hooks: { duration },
						jsonStringWrapping: {
							getDefault: () => stringWrapping.getSnapshot(),
							setDefault: (value) => {
								stringWrapping.set(value);
							}
						},
						loadOlder: async () => {
							const before = trajectory.getSnapshot();
							await session.loadOlder();
							return trajectory.getSnapshot() !== before;
						},
						loadImage: Object.assign((attachment) => ctx.uiConversation.imageUrl(sessionId, attachment), { peek: (attachment) => ctx.uiConversation.peekImageUrl(sessionId, attachment) }),
						setActualDuration: (value) => {
							duration.set(value);
						}
					};
				}
			}, TrajectoryView));
		}
		//#endregion
		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map