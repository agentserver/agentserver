window.__ModuleLoader__.load({
	id: "@deepseek-ai/dsh-client-ui-settings-subagent",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		Object.defineProperty(exports, Symbol.toStringTag, { value: "Module" });
		let react = require("react");
		let _deepseek_ai_dsh_client_ui_primitives = require("@deepseek-ai/dsh-client-ui-primitives");
		let react_jsx_runtime = require("react/jsx-runtime");
		let _deepseek_ai_dsh_client_store = require("@deepseek-ai/dsh-client-store");
		//#region src/client/locales.ts
		/** English copy. */
		const en = {
			overridden: "Overridden",
			reset: "Reset to default",
			readOnly: "This deployment stores settings read-only.",
			unavailable: "This plugin is not loaded, so it cannot be configured right now.",
			save: "Save",
			saving: "Saving…",
			saveFailed: "The deployment did not accept these values; they were left for you to correct.",
			subagentTitle: "Subagent",
			subagentDescription: "Set Subagent recursion depth, count, and models.",
			subagentLimitsTitle: "Limits",
			subagentMaxDepth: "Maximum recursion depth",
			subagentDepthHelpLabel: "About maximum recursion depth",
			subagentDepthHelp: "Limits how many levels of Subagents an Agent can create.",
			subagentDepthZero: "Disable Subagents",
			subagentDepthOne: "Only the main Agent can create Subagents",
			subagentDepthOverride: "If a tool defines its own maximum recursion depth, that setting takes precedence.",
			subagentMaxActive: "Subagent parallelism limit",
			subagentCapacityHelpLabel: "About the Subagent parallelism limit",
			subagentCapacityHelp: "Total live Subagents under the same main Agent, across all recursion levels. The main Agent is excluded. New start requests are rejected when the limit is reached.",
			subagentDepthInvalid: "Enter a whole number of 0 or more.",
			subagentCapacityInvalid: "Enter a whole number of 1 or more.",
			subagentModelSelectionTitle: "Model selection",
			subagentModelSelectionToggle: "Allow agents to choose models for Subagents",
			subagentModelSelectionChoose: "When enabled, agents can choose a provider, model, and reasoning effort for each Subagent from the authorized models below. Applies only to new sessions.",
			subagentModelSelectionAllowed: "Models agents may choose",
			subagentModelSelectionLoading: "Loading models…",
			subagentModelSelectionLoadFailed: "Models could not be loaded.",
			subagentModelSelectionRetry: "Retry",
			subagentModelSelectionPartial: "Some model providers could not be loaded; saved choices remain removable.",
			subagentModelSelectionUnavailable: "Currently unavailable",
			subagentModelSelectionUnavailableGroup: "Saved but currently unavailable",
			subagentModelSelectionEmpty: "No model provider currently advertises a model.",
			subagentModelSelectionRequired: "Select at least one model before saving.",
			subagentModelSelectionConflict: "Settings changed elsewhere. Discard your draft and try again.",
			subagentModelSelectionOff: "Subagents use configured defaults or inherit the parent agent's model. Saved model choices are retained."
		};
		/** Simplified Chinese copy. */
		const zh = {
			overridden: "已覆盖",
			reset: "恢复默认",
			readOnly: "本部署的设置为只读。",
			unavailable: "该插件当前未加载，暂时无法配置。",
			save: "保存",
			saving: "保存中…",
			saveFailed: "本部署没有接受这些值，已保留供你修改。",
			subagentTitle: "子智能体",
			subagentDescription: "设置子智能体的递归层级、数量和模型。",
			subagentLimitsTitle: "运行限制",
			subagentMaxDepth: "最大递归深度",
			subagentDepthHelpLabel: "最大递归深度说明",
			subagentDepthHelp: "限制 Agent 创建子智能体的递归层级。",
			subagentDepthZero: "禁用子智能体",
			subagentDepthOne: "仅允许主 Agent 创建子智能体",
			subagentDepthOverride: "如果某个工具单独设置了最大递归深度，以该工具的设置为准。",
			subagentMaxActive: "子智能体并行数量上限",
			subagentCapacityHelpLabel: "子智能体并行数量上限说明",
			subagentCapacityHelp: "同一主 Agent 下，所有递归层级同时存活的子智能体总数，主 Agent 不计入。达到上限时，新的启动请求会被拒绝。",
			subagentDepthInvalid: "请输入不小于 0 的整数。",
			subagentCapacityInvalid: "请输入不小于 1 的整数。",
			subagentModelSelectionTitle: "模型选择",
			subagentModelSelectionToggle: "允许 Agent 为子智能体选择模型",
			subagentModelSelectionChoose: "开启后，Agent 可以从下方授权模型中，为每个子智能体选择提供方、模型和推理强度。仅影响新会话。",
			subagentModelSelectionAllowed: "Agent 可选择的模型",
			subagentModelSelectionLoading: "正在加载模型…",
			subagentModelSelectionLoadFailed: "无法加载模型。",
			subagentModelSelectionRetry: "重试",
			subagentModelSelectionPartial: "部分模型提供方暂时无法加载；已保存的选择仍可移除。",
			subagentModelSelectionUnavailable: "当前不可用",
			subagentModelSelectionUnavailableGroup: "已保存但当前不可用",
			subagentModelSelectionEmpty: "当前没有模型提供方公布模型。",
			subagentModelSelectionRequired: "保存前请至少选择一个模型。",
			subagentModelSelectionConflict: "设置已在其他位置更新。请放弃修改后重试。",
			subagentModelSelectionOff: "关闭后，子智能体使用配置的默认模型或继承父 Agent 的模型；已选模型会保留。"
		};
		/**
		* The form frame's copy, read from this page's dictionary.
		* @param t - the page's locale reader.
		* @returns the labels the shared settings form renders.
		*/
		function formLabels(t) {
			return {
				unavailable: t("unavailable"),
				readOnly: t("readOnly"),
				saveFailed: t("saveFailed"),
				save: t("save"),
				saving: t("saving")
			};
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-settings-subagent/src/client/SubagentLimitsFields.module.css.mjs
		const css$2 = "._238S0G_limits{grid-template-columns:repeat(auto-fit,minmax(min(220px,100%),1fr));gap:16px;display:grid}._238S0G_limit{min-width:0}._238S0G_limit input{font-variant-numeric:tabular-nums;min-width:0}._238S0G_depthTable{border-collapse:collapse;border-block:.5px solid var(--dsw-alias-border-l2);width:100%;font:inherit;text-align:left;margin:8px 0}._238S0G_depthTable th,._238S0G_depthTable td{vertical-align:top;padding:6px 0}._238S0G_depthTable th{font-variant-numeric:tabular-nums;width:24px;color:var(--dsw-alias-label-primary);padding-right:8px;font-weight:500}._238S0G_depthTable tr+tr{border-top:.5px solid var(--dsw-alias-border-l2)}";
		const tagId$2 = "@deepseek-ai/dsh-client-ui-settings-subagent/SubagentLimitsFields.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$2) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-settings-subagent";
			tag.dataset.pluginCss = tagId$2;
			tag.textContent = css$2;
			document.head.appendChild(tag);
		}
		var SubagentLimitsFields_module_css_default = {
			"depthTable": "_238S0G_depthTable",
			"limit": "_238S0G_limit",
			"limits": "_238S0G_limits"
		};
		//#endregion
		//#region src/client/SubagentLimitsFields.tsx
		/**
		* Render the depth and capacity fields with their original validation and reset behavior.
		* @param props - Locale, staged fields, and edit callbacks.
		* @returns Two responsive fields and their application rules.
		*/
		function SubagentLimitsFields(props) {
			const { t, state } = props;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(react_jsx_runtime.Fragment, { children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: SubagentLimitsFields_module_css_default.limits,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
					className: SubagentLimitsFields_module_css_default.limit,
					children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.SettingsValueField, {
						id: "plugin-config-subagent-depth",
						label: t("subagentMaxDepth"),
						help: {
							label: t("subagentDepthHelpLabel"),
							content: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [
								/* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", { children: t("subagentDepthHelp") }),
								/* @__PURE__ */ (0, react_jsx_runtime.jsx)("table", {
									className: SubagentLimitsFields_module_css_default.depthTable,
									"aria-label": t("subagentDepthHelpLabel"),
									children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("tbody", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("tr", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("th", {
										scope: "row",
										children: 0
									}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("td", { children: t("subagentDepthZero") })] }), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("tr", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("th", {
										scope: "row",
										children: 1
									}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("td", { children: t("subagentDepthOne") })] })] })
								}),
								/* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", { children: t("subagentDepthOverride") })
							] })
						},
						overriddenLabel: t("overridden"),
						resetLabel: t("reset"),
						invalidLabel: t("subagentDepthInvalid"),
						numeric: true,
						disabled: !state.writable || state.saving,
						...state.maxDepth,
						onEdit: (text) => {
							props.edit("maxDepth", text);
						},
						onReset: () => {
							props.resetField("maxDepth");
						}
					})
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
					className: SubagentLimitsFields_module_css_default.limit,
					children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.SettingsValueField, {
						id: "plugin-config-subagent-capacity",
						label: t("subagentMaxActive"),
						help: {
							label: t("subagentCapacityHelpLabel"),
							content: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", { children: t("subagentCapacityHelp") })
						},
						overriddenLabel: t("overridden"),
						resetLabel: t("reset"),
						invalidLabel: t("subagentCapacityInvalid"),
						numeric: true,
						disabled: !state.writable || state.saving,
						...state.maxActiveSubagents,
						onEdit: (text) => {
							props.edit("maxActiveSubagents", text);
						},
						onReset: () => {
							props.resetField("maxActiveSubagents");
						}
					})
				})]
			}) });
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-settings-subagent/src/client/SubagentModelSelectionFields.module.css.mjs
		const css$1 = ".ed54cq_permission{gap:6px;padding:12px 0;display:grid}.ed54cq_toggleRow{color:var(--dsw-alias-label-primary);justify-content:space-between;align-items:flex-start;gap:16px;font-size:13px;line-height:1.5;display:flex}.ed54cq_toggleLabel{flex:1;min-width:0}.ed54cq_selection{gap:10px;display:grid}.ed54cq_hint,.ed54cq_notice,.ed54cq_invalid,.ed54cq_conflict{margin:0;font-size:12px;line-height:1.5}.ed54cq_hint,.ed54cq_notice{color:var(--dsw-alias-label-tertiary)}.ed54cq_invalid,.ed54cq_conflict{color:var(--dsw-alias-state-error-primary)}.ed54cq_catalogError{color:var(--dsw-alias-state-error-primary);justify-content:space-between;align-items:center;gap:12px;font-size:12px;display:flex}.ed54cq_catalogError button{color:var(--dsw-alias-brand-primary);cursor:pointer;background:0 0;border:0;padding:0}.ed54cq_models{border:.5px solid var(--dsw-alias-border-l4);border-radius:var(--dsw-radius-lg);gap:6px;min-width:0;max-height:280px;margin:0;padding:10px;display:grid;overflow:auto}.ed54cq_models legend{color:var(--dsw-alias-label-secondary);padding:0 4px;font-size:12px}.ed54cq_modelGroup{gap:6px;display:grid}.ed54cq_modelGroup+.ed54cq_modelGroup{border-top:.5px solid var(--dsw-alias-border-l3);margin-top:4px;padding-top:10px}.ed54cq_providerName{color:var(--dsw-alias-label-tertiary);padding:0 6px;font-size:11px;font-weight:500}.ed54cq_model{border-radius:var(--dsw-radius-md);cursor:pointer;grid-template-columns:auto minmax(0,1fr) auto;align-items:center;gap:8px;min-width:0;padding:6px;display:grid}.ed54cq_model:hover{background:var(--dsw-alias-bg-layer-4)}.ed54cq_modelName,.ed54cq_route{text-overflow:ellipsis;white-space:nowrap;display:block;overflow:hidden}.ed54cq_modelName{color:var(--dsw-alias-label-primary);font-size:13px}.ed54cq_route{color:var(--dsw-alias-label-tertiary);margin-top:2px;font-size:11px}.ed54cq_unavailable{color:var(--dsw-alias-label-tertiary);font-size:11px}";
		const tagId$1 = "@deepseek-ai/dsh-client-ui-settings-subagent/SubagentModelSelectionFields.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$1) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-settings-subagent";
			tag.dataset.pluginCss = tagId$1;
			tag.textContent = css$1;
			document.head.appendChild(tag);
		}
		var SubagentModelSelectionFields_module_css_default = {
			"catalogError": "ed54cq_catalogError",
			"conflict": "ed54cq_conflict",
			"hint": "ed54cq_hint",
			"invalid": "ed54cq_invalid",
			"model": "ed54cq_model",
			"modelGroup": "ed54cq_modelGroup",
			"modelName": "ed54cq_modelName",
			"models": "ed54cq_models",
			"notice": "ed54cq_notice",
			"permission": "ed54cq_permission",
			"providerName": "ed54cq_providerName",
			"route": "ed54cq_route",
			"selection": "ed54cq_selection",
			"toggleLabel": "ed54cq_toggleLabel",
			"toggleRow": "ed54cq_toggleRow",
			"unavailable": "ed54cq_unavailable"
		};
		//#endregion
		//#region src/client/SubagentModelSelectionFields.tsx
		/** User control for model-selectable subagent delegation in new sessions. */
		/**
		* Render the default-off preference and its exact adapter-route choices.
		* @param props - locale copy, the card snapshot, and its toggle action.
		* @returns the model permission and route choices inside the shared card.
		*/
		function SubagentModelSelectionFields(props) {
			const { t, state } = props;
			const availableGroups = /* @__PURE__ */ new Map();
			const unavailable = [];
			for (const candidate of state.candidates) {
				if (!candidate.available) {
					unavailable.push(candidate);
					continue;
				}
				const group = availableGroups.get(candidate.provider);
				if (group === void 0) availableGroups.set(candidate.provider, {
					providerName: candidate.providerName,
					candidates: [candidate]
				});
				else group.candidates.push(candidate);
			}
			const renderCandidate = (candidate) => /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("label", {
				className: SubagentModelSelectionFields_module_css_default.model,
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("input", {
						type: "checkbox",
						checked: candidate.selected,
						disabled: !state.writable || state.saving,
						onChange: () => {
							props.toggleModel(candidate.key);
						}
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: SubagentModelSelectionFields_module_css_default.modelName,
						children: candidate.modelName
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: SubagentModelSelectionFields_module_css_default.route,
						children: `${candidate.providerName} · ${candidate.provider}/${candidate.model}`
					})] }),
					!candidate.available ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: SubagentModelSelectionFields_module_css_default.unavailable,
						children: t("subagentModelSelectionUnavailable")
					}) : null
				]
			}, candidate.key);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [
				/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: SubagentModelSelectionFields_module_css_default.permission,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: SubagentModelSelectionFields_module_css_default.toggleRow,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: SubagentModelSelectionFields_module_css_default.toggleLabel,
							children: t("subagentModelSelectionToggle")
						}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Switch, {
							checked: state.enabled,
							label: t("subagentModelSelectionToggle"),
							disabled: !state.writable || state.saving,
							onChange: props.toggleEnabled
						})]
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
						className: SubagentModelSelectionFields_module_css_default.hint,
						children: t(state.enabled ? "subagentModelSelectionChoose" : "subagentModelSelectionOff")
					})]
				}),
				state.enabled ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: SubagentModelSelectionFields_module_css_default.selection,
					children: [
						state.catalogStatus === "loading" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
							className: SubagentModelSelectionFields_module_css_default.notice,
							role: "status",
							children: t("subagentModelSelectionLoading")
						}) : null,
						state.catalogStatus === "error" ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							className: SubagentModelSelectionFields_module_css_default.catalogError,
							role: "alert",
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: t("subagentModelSelectionLoadFailed") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
								type: "button",
								disabled: state.saving,
								onClick: props.retryCatalog,
								children: t("subagentModelSelectionRetry")
							})]
						}) : null,
						state.catalogPartial ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
							className: SubagentModelSelectionFields_module_css_default.notice,
							children: t("subagentModelSelectionPartial")
						}) : null,
						state.candidates.length > 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("fieldset", {
							className: SubagentModelSelectionFields_module_css_default.models,
							children: [
								/* @__PURE__ */ (0, react_jsx_runtime.jsx)("legend", { children: t("subagentModelSelectionAllowed") }),
								[...availableGroups].map(([provider, group]) => /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
									className: SubagentModelSelectionFields_module_css_default.modelGroup,
									children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
										className: SubagentModelSelectionFields_module_css_default.providerName,
										children: group.providerName
									}), group.candidates.map(renderCandidate)]
								}, provider)),
								unavailable.length > 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
									className: SubagentModelSelectionFields_module_css_default.modelGroup,
									children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
										className: SubagentModelSelectionFields_module_css_default.providerName,
										children: t("subagentModelSelectionUnavailableGroup")
									}), unavailable.map(renderCandidate)]
								}) : null
							]
						}) : state.catalogStatus === "ready" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
							className: SubagentModelSelectionFields_module_css_default.notice,
							children: t("subagentModelSelectionEmpty")
						}) : null,
						state.invalid ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
							className: SubagentModelSelectionFields_module_css_default.invalid,
							children: t("subagentModelSelectionRequired")
						}) : null
					]
				}) : null,
				state.conflicted ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
					className: SubagentModelSelectionFields_module_css_default.conflict,
					role: "status",
					children: t("subagentModelSelectionConflict")
				}) : null
			] });
		}
		//#endregion
		//#region src/client/subagent-card-controller.ts
		/**
		* Derive the shared card state without duplicating either form's subscriptions.
		* @param limits - Current delegation-limit form.
		* @param models - Current model-authorization form.
		* @returns Availability and settlement across the sections this Host serves.
		*/
		function subagentCardShell(limits, models) {
			const sections = [limits, models].filter((section) => section.available);
			return {
				available: sections.length > 0,
				writable: sections.every((section) => section.writable),
				dirty: sections.some((section) => section.dirty),
				invalid: sections.some((section) => section.invalid) || models.available && models.dirty && models.conflicted,
				saving: sections.some((section) => section.saving),
				failed: sections.some((section) => section.failed)
			};
		}
		/**
		* Compose one card from the existing forms; each write retains its namespace revision fence.
		* @param limits - Limit form source and actions.
		* @param models - Model form source and actions.
		* @returns Framework-bound sources and shared save/discard actions.
		*/
		function subagentCardFace(limits, models) {
			return {
				hooks: {
					...limits.hooks,
					...models.hooks
				},
				editLimit: limits.edit,
				resetLimit: limits.resetField,
				toggleEnabled: models.toggleEnabled,
				toggleModel: models.toggleModel,
				retryCatalog: models.retryCatalog,
				save: () => {
					const limitState = limits.hooks.subagentLimitsCard.getSnapshot();
					const modelState = models.hooks.subagentModelSelectionCard.getSnapshot();
					const state = subagentCardShell(limitState, modelState);
					if (!state.available || !state.writable || !state.dirty || state.invalid || state.saving) return;
					if (modelState.available && modelState.dirty) models.save();
					if (limitState.available && limitState.dirty) limits.save();
				},
				discard: () => {
					if (subagentCardShell(limits.hooks.subagentLimitsCard.getSnapshot(), models.hooks.subagentModelSelectionCard.getSnapshot()).saving) return;
					limits.discard();
					models.discard();
				}
			};
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-settings-subagent/src/client/SubagentCard.module.css.mjs
		const css = ".k9d1OG_section{min-width:0;padding:16px 0}.k9d1OG_heading{color:var(--dsw-alias-label-primary);margin:0;font-size:13px;font-weight:600;line-height:1.5}";
		const tagId = "@deepseek-ai/dsh-client-ui-settings-subagent/SubagentCard.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-settings-subagent";
			tag.dataset.pluginCss = tagId;
			tag.textContent = css;
			document.head.appendChild(tag);
		}
		var SubagentCard_module_css_default = {
			"heading": "k9d1OG_heading",
			"section": "k9d1OG_section"
		};
		//#endregion
		//#region src/client/SubagentCard.tsx
		/** One settings card for Subagent delegation limits and model authorization. */
		/**
		* Render the available Subagent settings with one configuration page and save footer.
		* @param props - Locale, both form snapshots, and their shared actions.
		* @returns The summary or the available settings form.
		*/
		function SubagentCard(props) {
			const { t } = props;
			const limits = props.useSubagentLimitsCard((snapshot) => snapshot);
			const models = props.useSubagentModelSelectionCard((snapshot) => snapshot);
			const headingId = (0, react.useId)();
			if (props.view === "summary") return t("subagentDescription");
			const state = subagentCardShell(limits, models);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(_deepseek_ai_dsh_client_ui_primitives.SettingsForm, {
				labels: formLabels(t),
				state,
				onSave: props.save,
				onDiscard: props.discard,
				children: [limits.available ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
					className: SubagentCard_module_css_default.section,
					"aria-labelledby": `${headingId}-limits`,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("h3", {
						className: SubagentCard_module_css_default.heading,
						id: `${headingId}-limits`,
						children: t("subagentLimitsTitle")
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(SubagentLimitsFields, {
						t,
						state: {
							...limits,
							saving: state.saving
						},
						edit: props.editLimit,
						resetField: props.resetLimit
					})]
				}) : null, models.available ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
					className: SubagentCard_module_css_default.section,
					"aria-labelledby": `${headingId}-models`,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("h3", {
						className: SubagentCard_module_css_default.heading,
						id: `${headingId}-models`,
						children: t("subagentModelSelectionTitle")
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(SubagentModelSelectionFields, {
						t,
						state: {
							...models,
							saving: state.saving
						},
						toggleEnabled: props.toggleEnabled,
						toggleModel: props.toggleModel,
						retryCatalog: props.retryCatalog
					})]
				}) : null]
			});
		}
		//#endregion
		//#region src/client/subagent-limits-card-controller.ts
		function limitField(field, minimum) {
			const numeric = (0, _deepseek_ai_dsh_client_ui_primitives.settingsNumberField)(field);
			return {
				...numeric,
				parse: (text) => {
					const write = numeric.parse(text);
					if (write?.kind !== "set") return write;
					const value = write.value;
					return Number.isSafeInteger(value) && value >= minimum && !Object.is(value, -0) ? write : void 0;
				}
			};
		}
		/** Bind two independently resettable limits to one staged settings form. */
		var SubagentLimitsCardController = class {
			form;
			store;
			/** @param scope - The Host's `subagent` settings section. */
			constructor(scope) {
				this.form = new _deepseek_ai_dsh_client_ui_primitives.SettingsFormModel(scope, [limitField("maxDepth", 0), limitField("maxActiveSubagents", 1)]);
				this.store = this.form.bind(() => ({
					...this.form.shell(),
					maxDepth: this.form.field("maxDepth"),
					maxActiveSubagents: this.form.field("maxActiveSubagents")
				}));
			}
			/**
			* Bind the limits editor to the slot renderer.
			* @returns The limits snapshot and staged write actions.
			*/
			inject() {
				return {
					hooks: { subagentLimitsCard: this.store },
					...this.form.actions()
				};
			}
			/** Release accepted-value subscriptions. */
			dispose() {
				this.form.dispose();
			}
		};
		//#endregion
		//#region src/client/subagent-model-selection-card-controller.ts
		/** Namespace of the Host-owned subagent model-selection preference. */
		const SUBAGENT_MODEL_SELECTION_NS = "subagent-model-selection-settings";
		/**
		* Stable identity for one exact route; callers resolve it by lookup and never parse it.
		* @param route - Provider/model route to identify.
		* @returns Opaque key for lookup within the card.
		*/
		function subagentModelKey(route) {
			return `${route.provider}\0${route.model}`;
		}
		/**
		* Join live adapter metadata with stored routes that remain removable after disappearance.
		* @param groups - Current model directory grouped by provider.
		* @param stored - Routes in the effective settings value.
		* @param selected - Opaque route keys selected in the current draft.
		* @returns Candidate rows for the card.
		*/
		function subagentModelCandidates(groups, stored, selected) {
			const storedByKey = new Map(stored.map((route) => [subagentModelKey(route), route]));
			const candidates = groups.flatMap((group) => group.models.map((model) => {
				const route = {
					provider: group.id,
					model: model.id
				};
				const key = subagentModelKey(route);
				storedByKey.delete(key);
				return {
					...route,
					key,
					providerName: group.name,
					modelName: model.name,
					available: true,
					selected: selected.has(key)
				};
			}));
			for (const route of storedByKey.values()) {
				const key = subagentModelKey(route);
				candidates.push({
					...route,
					key,
					providerName: route.provider,
					modelName: route.model,
					available: false,
					selected: selected.has(key)
				});
			}
			return candidates;
		}
		function sameRoutes(left, right) {
			if (left.length !== right.length) return false;
			const rightKeys = new Set(right.map(subagentModelKey));
			return left.every((route) => rightKeys.has(subagentModelKey(route)));
		}
		/** Bridges one configuration form and the live adapter directory onto a staged card. */
		var SubagentModelSelectionCardController = class {
			scope;
			ctx;
			catalogGroups = [];
			catalogPartial = false;
			catalogStatus = "idle";
			draftEnabled;
			draftRoutes;
			draftRevision;
			saving = false;
			failed = false;
			conflicted = false;
			disposed = false;
			saveGeneration = 0;
			catalogGeneration = 0;
			store;
			unsubscribe;
			/**
			* @param scope - bound `subagent-model-selection` configuration form.
			* @param ctx - the card plugin's context, whose `remote.session` namespace
			* answers the Host model catalog.
			*/
			constructor(scope, ctx) {
				this.scope = scope;
				this.ctx = ctx;
				this.store = (0, _deepseek_ai_dsh_client_store.createSnapshotStore)(this.projection());
				this.unsubscribe = scope.subscribe(() => {
					if (!this.saving && this.draftRoutes !== void 0 && this.scope.getSnapshot().revision !== this.draftRevision) if (this.currentEnabled() === this.enabled() && sameRoutes(this.currentRoutes(), this.desiredRoutes())) this.clearDraft();
					else this.conflicted = true;
					if (this.enabled() && this.catalogStatus === "idle") this.loadCatalog();
					this.publish();
				});
				if (this.enabled() && this.catalogStatus === "idle") this.loadCatalog();
			}
			/** Stop observing settings and suppress late directory/write settlements. */
			dispose() {
				this.disposed = true;
				this.saveGeneration += 1;
				this.catalogGeneration += 1;
				this.unsubscribe();
			}
			/**
			* Build the renderer face for this card.
			* @returns The snapshot and staged card actions injected into the renderer.
			*/
			inject() {
				return {
					hooks: { subagentModelSelectionCard: this.store },
					toggleEnabled: () => {
						this.toggleEnabled();
					},
					toggleModel: (key) => {
						this.toggleModel(key);
					},
					retryCatalog: () => {
						this.loadCatalog();
					},
					save: () => {
						this.save();
					},
					discard: () => {
						this.discard();
					}
				};
			}
			currentRoutes() {
				return this.scope.getSnapshot().value?.allowedModels.map((route) => ({ ...route })) ?? [];
			}
			currentEnabled() {
				return this.scope.getSnapshot().value?.enabled ?? false;
			}
			selected() {
				return new Set(this.draftRoutes?.keys() ?? this.currentRoutes().map(subagentModelKey));
			}
			enabled() {
				return this.draftEnabled ?? this.currentEnabled();
			}
			beginDraft() {
				if (this.draftRoutes === void 0) {
					const snapshot = this.scope.getSnapshot();
					this.draftEnabled = snapshot.value?.enabled ?? false;
					this.draftRoutes = new Map(snapshot.value?.allowedModels.map((route) => [subagentModelKey(route), { ...route }]) ?? []);
					this.draftRevision = snapshot.revision;
				}
				return this.draftRoutes;
			}
			toggleEnabled() {
				const snapshot = this.scope.getSnapshot();
				if (this.disposed || snapshot.status !== "ready" || !snapshot.writable || this.saving) return;
				this.beginDraft();
				this.draftEnabled = !this.draftEnabled;
				this.failed = false;
				if (this.draftEnabled && this.catalogStatus === "idle") this.loadCatalog();
				this.publish();
			}
			toggleModel(key) {
				if (!this.enabled() || this.saving || !this.scope.getSnapshot().writable) return;
				const candidate = this.candidates().find((candidate) => candidate.key === key);
				if (candidate === void 0) return;
				const routes = this.beginDraft();
				if (routes.has(key)) routes.delete(key);
				else routes.set(key, {
					provider: candidate.provider,
					model: candidate.model
				});
				this.failed = false;
				this.publish();
			}
			clearDraft() {
				this.draftEnabled = void 0;
				this.draftRoutes = void 0;
				this.draftRevision = void 0;
				this.failed = false;
				this.conflicted = false;
			}
			discard() {
				if (this.saving) return;
				this.clearDraft();
				this.publish();
			}
			candidates() {
				const retained = new Map(this.currentRoutes().map((route) => [subagentModelKey(route), route]));
				for (const [key, route] of this.draftRoutes ?? []) retained.set(key, route);
				return subagentModelCandidates(this.catalogGroups, [...retained.values()], this.selected());
			}
			desiredRoutes() {
				return [...this.draftRoutes?.values() ?? this.currentRoutes()].map((route) => ({ ...route }));
			}
			async save() {
				const snapshot = this.scope.getSnapshot();
				const desiredEnabled = this.enabled();
				const desired = this.desiredRoutes();
				if (this.disposed || snapshot.status !== "ready" || !snapshot.writable || this.saving || this.currentEnabled() === desiredEnabled && sameRoutes(this.currentRoutes(), desired) || desiredEnabled && desired.length === 0) return;
				if (this.draftRoutes !== void 0 && snapshot.revision !== this.draftRevision) {
					this.conflicted = true;
					this.publish();
					return;
				}
				const generation = this.saveGeneration;
				this.saving = true;
				this.failed = false;
				this.conflicted = false;
				this.publish();
				await this.scope.mutate([{
					op: "set",
					path: ["enabled"],
					value: desiredEnabled
				}, {
					op: "set",
					path: ["allowedModels"],
					value: desired.map((route) => ({
						provider: route.provider,
						model: route.model
					}))
				}], this.draftRevision);
				if (generation !== this.saveGeneration) return;
				const landed = this.currentEnabled() === desiredEnabled && sameRoutes(this.currentRoutes(), desired);
				this.saving = false;
				this.failed = !landed;
				if (landed) this.clearDraft();
				this.publish();
			}
			/** Invalidate and reload model candidates after a Host model input changes. */
			refreshCatalog() {
				if (this.disposed) return;
				this.catalogGeneration += 1;
				this.catalogStatus = "idle";
				this.catalogPartial = false;
				if (this.enabled()) this.loadCatalog();
				else this.publish();
			}
			/** Drop Host-specific candidates and drafts, then reload after reconnecting. */
			resetConnection() {
				if (this.disposed) return;
				this.saveGeneration += 1;
				this.saving = false;
				this.clearDraft();
				this.catalogGroups = [];
				this.refreshCatalog();
			}
			async loadCatalog() {
				if (this.disposed || this.catalogStatus === "loading") return;
				const generation = this.catalogGeneration;
				this.catalogStatus = "loading";
				this.catalogPartial = false;
				this.publish();
				const response = await this.ctx.remote.session.modelCatalog();
				if (generation !== this.catalogGeneration) return;
				if (response.ok) {
					this.catalogGroups = response.value.groups;
					this.catalogPartial = response.value.failures.length > 0;
					this.catalogStatus = "ready";
				} else this.catalogStatus = "error";
				this.publish();
			}
			projection() {
				const snapshot = this.scope.getSnapshot();
				const current = this.currentRoutes();
				const desired = this.desiredRoutes();
				const enabled = this.enabled();
				return {
					available: snapshot.status === "ready",
					writable: snapshot.writable,
					dirty: this.currentEnabled() !== enabled || !sameRoutes(current, desired),
					invalid: enabled && desired.length === 0,
					saving: this.saving,
					failed: this.failed,
					enabled,
					candidates: this.candidates(),
					catalogStatus: this.catalogStatus,
					catalogPartial: this.catalogPartial,
					conflicted: this.conflicted
				};
			}
			publish() {
				this.store.set(this.projection());
			}
		};
		//#endregion
		//#region src/client/index.ts
		/** Dictionary namespace owned by this plugin. */
		const NS = "settings.subagent";
		/**
		* Namespace of the delegation limits. Spelled here rather than imported: a
		* client package must not depend on a Host package.
		*/
		const SUBAGENT_NS = "subagent";
		/** Required services (cordis fiber inject). */
		const inject = [
			"slots",
			"locale",
			"remote",
			"remote.session",
			"configForms"
		];
		/**
		* Mount the Subagent settings page while the Host serves either of its namespaces.
		* @param ctx - the browser plugin context.
		*/
		function apply(ctx) {
			const t = ctx.locale.bind(NS);
			ctx.effect(() => ctx.locale.register(NS, {
				zh,
				en
			}), "ui-settings-subagent: dictionaries");
			const limits = new SubagentLimitsCardController(ctx.configForms.get(SUBAGENT_NS));
			ctx.effect(() => () => {
				limits.dispose();
			}, "ui-settings-subagent: limits form subscription");
			const models = new SubagentModelSelectionCardController(ctx.configForms.get(SUBAGENT_MODEL_SELECTION_NS), ctx);
			const limitsFace = limits.inject();
			const modelsFace = models.inject();
			ctx.effect(() => ctx.remote.$on("llm/adapters-updated", () => {
				models.refreshCatalog();
			}), "ui-settings-subagent: adapter invalidations");
			ctx.effect(() => ctx.remote.$on("settings/document-updated", () => {
				models.refreshCatalog();
			}), "ui-settings-subagent: settings invalidations");
			ctx.effect(() => ctx.on("connection/reset", () => {
				models.resetConnection();
			}), "ui-settings-subagent: connection generation");
			ctx.effect(() => () => {
				models.dispose();
			}, "ui-settings-subagent: model preference");
			ctx.effect(() => ctx.configForms.whileServed([SUBAGENT_NS, SUBAGENT_MODEL_SELECTION_NS], () => ctx.slots.inject("plugins.item", () => ctx.slots.register({
				name: "plugins.item",
				id: "subagent",
				order: 30,
				label: () => t("subagentTitle"),
				locale: NS,
				inject: () => subagentCardFace(limitsFace, modelsFace)
			}, SubagentCard))), "ui-settings-subagent: page");
		}
		//#endregion
		exports.NS = NS;
		exports.SUBAGENT_NS = SUBAGENT_NS;
		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map