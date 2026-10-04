window.__ModuleLoader__.load({
	id: "@deepseek-ai/dsh-client-ui-settings-plugin-inventory",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		Object.defineProperty(exports, Symbol.toStringTag, { value: "Module" });
		let react = require("react");
		let _deepseek_ai_dsh_client_ui_primitives = require("@deepseek-ai/dsh-client-ui-primitives");
		let react_jsx_runtime = require("react/jsx-runtime");
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
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-settings-plugin-inventory/src/client/PluginInventorySettingsTab.module.css.mjs
		const css = ".hiNSXq_section{width:100%;max-width:760px;color:var(--dsw-alias-label-primary);flex-direction:column;gap:14px;display:flex}.hiNSXq_catalogHeading h3,.hiNSXq_status,.hiNSXq_failure p{margin:0}.hiNSXq_status,.hiNSXq_failure{color:var(--dsw-alias-label-tertiary);font-size:13px;line-height:20px}.hiNSXq_failure{color:var(--dsw-alias-state-error-primary);align-items:center;gap:10px;display:flex}.hiNSXq_statusWithDot{align-items:center;gap:6px;display:inline-flex}.hiNSXq_failure button{border:.5px solid var(--dsw-alias-border-l3);border-radius:var(--dsw-radius-sm);color:var(--dsw-alias-label-primary);font:inherit;cursor:pointer;background:0 0;padding:4px 10px}.hiNSXq_catalog{flex-direction:column;gap:12px;display:flex}.hiNSXq_search{width:100%;color:var(--dsw-alias-label-tertiary);align-items:center;display:flex;position:relative}.hiNSXq_search>svg{pointer-events:none;position:absolute;left:12px}.hiNSXq_search input{border:.5px solid var(--dsw-alias-border-l4);border-radius:var(--dsw-radius-md);background:var(--dsw-alias-bg-layer-1);width:100%;height:36px;color:var(--dsw-alias-label-primary);font:inherit;outline:none;padding:0 34px 0 36px;font-size:13px}.hiNSXq_search input::placeholder{color:var(--dsw-alias-label-tertiary)}.hiNSXq_search input:focus-visible{border-color:var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));box-shadow:0 0 0 2px color-mix(in srgb, var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary)) 18%, transparent)}.hiNSXq_catalogHeading{align-items:baseline;gap:7px;padding:0 2px;display:flex}.hiNSXq_catalogHeading h3{font-size:13px;font-weight:600;line-height:20px}.hiNSXq_catalogHeading span{color:var(--dsw-alias-label-tertiary);font-variant-numeric:tabular-nums;font-size:12px;line-height:18px}.hiNSXq_cards{grid-template-columns:repeat(2,minmax(0,1fr));align-items:start;gap:10px;margin:0;padding:0;list-style:none;display:grid}.hiNSXq_card{border:.5px solid var(--dsw-alias-settings-card-stroke);border-radius:var(--dsw-radius-xl);background:var(--dsw-alias-settings-card-fill);min-width:0;overflow:hidden}.hiNSXq_card[data-open=true]{border-color:var(--dsw-alias-border-l3)}.hiNSXq_cardContent{box-sizing:border-box;width:100%;min-height:52px;color:inherit;font:inherit;text-align:left;cursor:pointer;background:0 0;border:0;flex-direction:column;align-items:stretch;padding:12px 14px;display:flex}.hiNSXq_cardContent:hover,.hiNSXq_card[data-open=true]>.hiNSXq_cardContent{background:var(--dsw-alias-interactive-bg-hover)}.hiNSXq_cardContent:focus-visible{outline:var(--dsw-focus-ring-width) solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:-2px}.hiNSXq_cardMainRow{justify-content:space-between;align-items:center;gap:12px;min-width:0;display:flex}.hiNSXq_cardTitle,.hiNSXq_cardIdentity{text-overflow:ellipsis;white-space:nowrap;overflow:hidden}.hiNSXq_cardTitle{flex:1;min-width:0;font-size:14px;font-weight:600;line-height:20px}.hiNSXq_cardIdentity,.hiNSXq_entryValue{font-family:var(--ds-font-family-code);font-size:12px;line-height:18px}.hiNSXq_cardIdentity{width:100%;color:var(--dsw-alias-label-tertiary);display:block}.hiNSXq_cardTrailing{color:var(--dsw-alias-label-tertiary);flex:none;align-items:center;gap:7px;display:inline-flex}.hiNSXq_phaseDot{flex:none;display:inline-flex}.hiNSXq_group{flex-direction:column;gap:10px;display:flex}.hiNSXq_groupTitleRow{align-items:center;gap:8px;min-height:32px;display:flex}.hiNSXq_group+.hiNSXq_group{border-top:.5px solid var(--dsw-alias-border-l2);padding-top:14px}.hiNSXq_headerEnd{margin-left:auto}.hiNSXq_groupToggle{color:inherit;font:inherit;text-align:left;cursor:pointer;background:0 0;border:0;flex:none;align-items:center;gap:8px;padding:0;display:flex}.hiNSXq_groupToggle:focus-visible{outline:var(--dsw-focus-ring-width) solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:2px}.hiNSXq_groupToggle>.hiNSXq_chevron{transform:rotate(-90deg)}.hiNSXq_groupToggle[aria-expanded=true]>.hiNSXq_chevron{transform:none}.hiNSXq_groupTitle{color:var(--dsw-alias-label-primary);font-size:14px;font-weight:400;line-height:22px}.hiNSXq_groupSub{color:var(--dsw-alias-label-tertiary);font-variant-numeric:tabular-nums;flex-wrap:wrap;gap:4px 8px;margin:-6px 0 0 20px;font-size:12px;line-height:18px;display:flex}.hiNSXq_failedCount{color:var(--dsw-alias-state-error-primary)}.hiNSXq_switcher{white-space:nowrap;border-radius:var(--dsw-radius-md);background:var(--dsw-alias-bg-module-platform);height:36px;color:var(--dsw-alias-label-primary);font:inherit;cursor:pointer;border:none;flex:none;align-items:center;gap:12px;padding:0 14px;font-size:14px;line-height:22px;display:inline-flex}.hiNSXq_switcher:hover{background:var(--dsw-alias-interactive-bg-hover)}.hiNSXq_switcher:focus-visible{outline:var(--dsw-focus-ring-width) solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:2px}.hiNSXq_switcher>.hiNSXq_chevron{flex:none}.hiNSXq_switcherLabel{text-overflow:ellipsis;max-width:240px;overflow:hidden}.hiNSXq_groupBody{flex-direction:column;gap:10px;display:flex}.hiNSXq_brokenNote{border-radius:var(--dsw-radius-lg);background:color-mix(in srgb, var(--dsw-alias-state-error-primary) 8%, transparent);color:var(--dsw-alias-state-error-primary);overflow-wrap:anywhere;white-space:pre-line;margin:0;padding:8px 10px;font-size:12.5px;line-height:18px}.hiNSXq_hint{color:var(--dsw-alias-label-tertiary);flex-wrap:wrap;align-items:baseline;gap:4px 8px;margin:0;font-size:12.5px;line-height:18px;display:flex}.hiNSXq_jumpLink{color:var(--dsw-alias-state-business-primary);font:inherit;cursor:pointer;background:0 0;border:0;padding:0;font-size:12.5px}.hiNSXq_enabledIn{flex-wrap:wrap;align-items:baseline;gap:4px 10px;display:flex}.hiNSXq_card[data-failed=true]{border-color:color-mix(in srgb, var(--dsw-alias-state-error-primary) 45%, transparent)}.hiNSXq_chevron{color:var(--dsw-alias-label-tertiary);flex:none}.hiNSXq_card[data-open=true] .hiNSXq_chevron{transform:rotate(180deg)}.hiNSXq_cardDetails{border-top:.5px solid var(--dsw-alias-border-l2);background:var(--dsw-alias-bg-module-platform);padding:10px 14px 12px}.hiNSXq_entryValue{overflow-wrap:anywhere;color:var(--dsw-alias-label-primary);display:block}.hiNSXq_details{grid-template-columns:76px minmax(0,1fr);gap:6px 10px;margin:8px 0 0;display:grid}.hiNSXq_details div{display:contents}.hiNSXq_details dt{color:var(--dsw-alias-label-tertiary);font-size:11px;line-height:17px}.hiNSXq_details dd{overflow-wrap:anywhere;min-width:0;color:var(--dsw-alias-label-secondary);margin:0;font-size:12px;line-height:17px}.hiNSXq_visuallyHidden{clip:rect(0 0 0 0);clip-path:inset(50%);white-space:nowrap;width:1px;height:1px;position:absolute;overflow:hidden}@media (prefers-reduced-motion:no-preference){.hiNSXq_chevron{transition:transform .14s var(--ds-ease-in-out)}}@media (width<=680px){.hiNSXq_cards{grid-template-columns:minmax(0,1fr)}}";
		const tagId = "@deepseek-ai/dsh-client-ui-settings-plugin-inventory/PluginInventorySettingsTab.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-settings-plugin-inventory";
			tag.dataset.pluginCss = tagId;
			tag.textContent = css;
			document.head.appendChild(tag);
		}
		var PluginInventorySettingsTab_module_css_default = {
			"brokenNote": "hiNSXq_brokenNote",
			"card": "hiNSXq_card",
			"cardContent": "hiNSXq_cardContent",
			"cardDetails": "hiNSXq_cardDetails",
			"cardIdentity": "hiNSXq_cardIdentity",
			"cardMainRow": "hiNSXq_cardMainRow",
			"cardTitle": "hiNSXq_cardTitle",
			"cardTrailing": "hiNSXq_cardTrailing",
			"cards": "hiNSXq_cards",
			"catalog": "hiNSXq_catalog",
			"catalogHeading": "hiNSXq_catalogHeading",
			"chevron": "hiNSXq_chevron",
			"details": "hiNSXq_details",
			"enabledIn": "hiNSXq_enabledIn",
			"entryValue": "hiNSXq_entryValue",
			"failedCount": "hiNSXq_failedCount",
			"failure": "hiNSXq_failure",
			"group": "hiNSXq_group",
			"groupBody": "hiNSXq_groupBody",
			"groupSub": "hiNSXq_groupSub",
			"groupTitle": "hiNSXq_groupTitle",
			"groupTitleRow": "hiNSXq_groupTitleRow",
			"groupToggle": "hiNSXq_groupToggle",
			"headerEnd": "hiNSXq_headerEnd",
			"hint": "hiNSXq_hint",
			"jumpLink": "hiNSXq_jumpLink",
			"phaseDot": "hiNSXq_phaseDot",
			"search": "hiNSXq_search",
			"section": "hiNSXq_section",
			"status": "hiNSXq_status",
			"statusWithDot": "hiNSXq_statusWithDot",
			"switcher": "hiNSXq_switcher",
			"switcherLabel": "hiNSXq_switcherLabel",
			"visuallyHidden": "hiNSXq_visuallyHidden"
		};
		//#endregion
		//#region src/client/PluginInventorySettingsTab.tsx
		const PHASE_KEYS = {
			pending: "pending",
			loading: "loadingPhase",
			active: "active",
			failed: "failed",
			unloading: "unloading"
		};
		/** Localized accessible label for one root Fiber phase. */
		function phaseLabel(phase, t) {
			return phase === null ? t("unobserved") : t(PHASE_KEYS[phase]);
		}
		/** Compact technical names for Settings without changing their module identity. */
		function moduleShortName(moduleName) {
			return (moduleName.startsWith("@") ? moduleName.slice(moduleName.indexOf("/") + 1) : moduleName).replace(/^cordis:/, "").replace(/^cordis-plugin-/, "").replace(/^dsh-(?:host-|client-)?/, "");
		}
		/** Display an entry identity without the composition-only `include:` marker. */
		function entrySubtitle(entryId) {
			return entryId.replace(/^include:/, "");
		}
		/** Preserve translated titles and shorten literal package or module name fallbacks in Settings. */
		function pluginText(row, resolveText) {
			const title = row.meta?.title;
			return {
				title: typeof title === "object" ? resolveText(title) : moduleShortName(title ?? row.moduleName),
				description: row.meta?.description === void 0 ? void 0 : resolveText(row.meta.description) || void 0
			};
		}
		/** Match translated text alongside the row's technical module and entry identities. */
		function matches(row, normalizedQuery, resolveText) {
			if (normalizedQuery.length === 0) return true;
			const { title, description } = pluginText(row, resolveText);
			return [
				row.moduleName,
				row.entryId,
				title,
				description
			].some((value) => value?.toLocaleLowerCase().includes(normalizedQuery));
		}
		/** The roster row shown when the preset switcher has no explicit choice. */
		function fallbackPreset(presets) {
			return presets.find((preset) => preset.isDefault) ?? presets[0];
		}
		/** The switcher's display label for one preset. */
		function presetLabel(preset, t, presetName) {
			const name = presetName(preset);
			if (preset.broken !== void 0) return t("presetOptionBroken", { name });
			if (preset.isDefault) return t("presetOptionDefault", { name });
			return name;
		}
		/** One expandable plugin card; the caller owns the trailing status content. */
		function PluginCard({ rowKey, moduleName, title, description, metadataError, entryId, trailing, ariaLabel, failed, expanded, onToggle, children }) {
			const open = expanded === rowKey;
			const detailId = `plugin-details-${encodeURIComponent(rowKey)}`;
			const descriptionId = (0, react.useId)();
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("li", {
				className: PluginInventorySettingsTab_module_css_default.card,
				"data-plugin-entry": entryId ?? void 0,
				"data-plugin-module": moduleName,
				"data-failed": failed ? "true" : void 0,
				"data-open": open ? "true" : void 0,
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
						className: PluginInventorySettingsTab_module_css_default.cardContent,
						type: "button",
						"aria-expanded": open,
						"aria-controls": detailId,
						"aria-label": ariaLabel,
						"aria-describedby": description === void 0 ? void 0 : descriptionId,
						onClick: () => {
							onToggle(rowKey);
						},
						children: [
							/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
								className: PluginInventorySettingsTab_module_css_default.cardMainRow,
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("strong", {
									className: PluginInventorySettingsTab_module_css_default.cardTitle,
									title: moduleName,
									children: title
								}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
									className: PluginInventorySettingsTab_module_css_default.cardTrailing,
									children: [trailing, /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronDownOutlineRegular, {
										className: PluginInventorySettingsTab_module_css_default.chevron,
										size: 12,
										"aria-hidden": "true"
									})]
								})]
							}),
							description === void 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
								className: PluginInventorySettingsTab_module_css_default.hint,
								id: descriptionId,
								children: description
							}),
							entryId === null ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("code", {
								className: PluginInventorySettingsTab_module_css_default.cardIdentity,
								title: entryId,
								children: entrySubtitle(entryId)
							})
						]
					}),
					metadataError === void 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
						className: PluginInventorySettingsTab_module_css_default.brokenNote,
						role: "status",
						"data-package-meta-error": true,
						children: metadataError
					}),
					open ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						className: PluginInventorySettingsTab_module_css_default.cardDetails,
						id: detailId,
						children
					}) : null
				]
			});
		}
		/** Detail rows shared by every card: the Loader identity, then labeled facts. */
		function CardFacts({ moduleName, moduleLabel, entryId, facts }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [entryId === null ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("code", {
				className: PluginInventorySettingsTab_module_css_default.entryValue,
				"data-loader-entry": true,
				children: entryId
			}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("dl", {
				className: PluginInventorySettingsTab_module_css_default.details,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: moduleLabel }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: moduleName })] }), facts.map(([label, value]) => /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: label }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: value })] }, label))]
			})] });
		}
		const PHASE_DOT_STATES = {
			pending: "idle",
			loading: "ongoing",
			unloading: "ongoing"
		};
		/** Whether a live root-fiber phase carries a dot of its own. */
		function showsPhaseDot(phase) {
			return phase === "pending" || phase === "loading" || phase === "unloading";
		}
		/** Status dot naming a live root-fiber phase; rows without a dotted phase show none. */
		function PhaseDot({ phase, t }) {
			const status = phaseLabel(phase, t);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
				className: PluginInventorySettingsTab_module_css_default.phaseDot,
				role: "img",
				"aria-label": status,
				title: status,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, { state: PHASE_DOT_STATES[phase] })
			});
		}
		const TAG_TONES = {
			enabled: "success",
			disabled: "neutral",
			conditional: "warning",
			preset: "info",
			failed: "danger"
		};
		/** Enablement tag; `kind` selects the palette. */
		function StateTag({ kind, label }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tag, {
				tone: TAG_TONES[kind],
				children: label
			});
		}
		/** Render the read-only plugin inventory: agent presets first, then the global plane. */
		function PluginInventorySettingsTab({ list, presetName, resolveText, t, useClientSync, retryClient }) {
			const clientSync = useClientSync((snapshot) => snapshot);
			const sectionId = (0, react.useId)();
			const [request, setRequest] = (0, react.useState)(0);
			const [query, setQuery] = (0, react.useState)("");
			const [expanded, setExpanded] = (0, react.useState)(null);
			const [chosenPreset, setChosenPreset] = (0, react.useState)(null);
			const [switcherOpen, setSwitcherOpen] = (0, react.useState)(false);
			const [presetOpen, setPresetOpen] = (0, react.useState)(null);
			const [globalOpen, setGlobalOpen] = (0, react.useState)(null);
			const [state, setState] = (0, react.useState)({ status: "loading" });
			(0, react.useEffect)(() => {
				let current = true;
				Promise.resolve().then(() => list()).then((snapshot) => {
					if (current) setState({
						status: "ready",
						snapshot
					});
				}, () => {
					if (current) setState({ status: "error" });
				});
				return () => {
					current = false;
				};
			}, [list, request]);
			const normalizedQuery = query.trim().toLocaleLowerCase();
			const searching = normalizedQuery.length > 0;
			const snapshot = state.status === "ready" ? state.snapshot : void 0;
			const presets = snapshot?.agentPresets ?? [];
			const selected = presets.find((preset) => preset.id === chosenPreset) ?? fallbackPreset(presets);
			/** Presets that actually enable a module, keyed by module name. */
			const enabledIn = (0, react.useMemo)(() => {
				const found = /* @__PURE__ */ new Map();
				for (const preset of presets) for (const row of preset.rows) {
					if (row.enabled !== true) continue;
					const groups = found.get(row.moduleName);
					if (groups === void 0) found.set(row.moduleName, [preset]);
					else if (!groups.includes(preset)) groups.push(preset);
				}
				return found;
			}, [presets]);
			const entries = snapshot?.entries ?? [];
			const failedEntries = [];
			const regularEntries = [];
			for (const entry of entries) if (entry.fiberPhase === "failed") failedEntries.push(entry);
			else regularEntries.push(entry);
			const entryMatch = (entry) => matches(entry, normalizedQuery, resolveText);
			const rowMatch = (row) => matches(row, normalizedQuery, resolveText);
			const filteredFailed = failedEntries.filter(entryMatch);
			const filteredRegular = regularEntries.filter(entryMatch);
			const globalCount = filteredFailed.length + filteredRegular.length;
			const selectedRows = selected === void 0 ? [] : selected.rows.filter(rowMatch);
			const otherPresetMatches = searching ? presets.filter((preset) => preset !== selected && preset.rows.some(rowMatch)) : [];
			const otherMatchCount = otherPresetMatches.reduce((total, preset) => total + preset.rows.filter(rowMatch).length, 0);
			const presetEffectiveOpen = searching || (presetOpen ?? false);
			const globalEffectiveOpen = searching || (globalOpen ?? false);
			const nothingMatches = searching && globalCount === 0 && selectedRows.length === 0 && otherPresetMatches.length === 0;
			const retry = () => {
				setState({ status: "loading" });
				setRequest((value) => value + 1);
			};
			const toggleRow = (key) => {
				setExpanded((current) => current === key ? null : key);
			};
			/** Trailing status and detail facts for one row of the selected preset. */
			const presetRowCard = (preset, row, index) => {
				const key = `preset:${preset.id}:${String(index)}`;
				const { title, description } = pluginText(row, resolveText);
				const failed = row.fiberPhase === "failed";
				const stateText = failed ? t("failedTag") : row.enabled === true ? t("enabledTag") : row.enabled === false ? t("disabledTag") : t("conditionalTag");
				const kind = failed ? "failed" : row.enabled === true ? "enabled" : row.enabled === false ? "disabled" : "conditional";
				return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(PluginCard, {
					rowKey: key,
					moduleName: row.moduleName,
					title,
					description,
					metadataError: row.meta?.error === void 0 ? void 0 : t("metadataError", { error: row.meta.error }),
					entryId: row.entryId,
					failed,
					expanded,
					onToggle: toggleRow,
					ariaLabel: `${title}${row.entryId === null ? "" : `, ${row.entryId}`}, ${stateText}`,
					trailing: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [row.enabled === true && showsPhaseDot(row.fiberPhase) ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(PhaseDot, {
						phase: row.fiberPhase,
						t
					}) : null, /* @__PURE__ */ (0, react_jsx_runtime.jsx)(StateTag, {
						kind,
						label: stateText
					})] }),
					children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(CardFacts, {
						moduleName: row.moduleName,
						moduleLabel: t("moduleLabel"),
						entryId: row.entryId,
						facts: [
							[t("fromPreset"), presetName(preset)],
							[t("configuration"), stateText],
							...row.fiberPhase === null ? [] : [[t("runtime"), phaseLabel(row.fiberPhase, t)]],
							...row.condition === void 0 ? [] : [[t("condition"), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("code", { children: row.condition }, "condition")]]
						]
					})
				}, key);
			};
			/** One global-plane row; a preset-provided row carries the presets that enable it. */
			const globalRowCard = (entry, providers) => {
				const key = `global:${entry.entryId}`;
				const { title, description } = pluginText(entry, resolveText);
				const failed = entry.fiberPhase === "failed";
				const stateText = failed ? t("failedTag") : providers !== void 0 ? t("presetEnabledTag") : t(entry.enabled ? "enabledTag" : "disabledTag");
				const kind = failed ? "failed" : providers !== void 0 ? "preset" : entry.enabled ? "enabled" : "disabled";
				return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(PluginCard, {
					rowKey: key,
					moduleName: entry.moduleName,
					title,
					description,
					metadataError: entry.meta?.error === void 0 ? void 0 : t("metadataError", { error: entry.meta.error }),
					entryId: entry.entryId,
					failed,
					expanded,
					onToggle: toggleRow,
					ariaLabel: `${title}, ${entry.entryId}, ${stateText}`,
					trailing: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [entry.enabled && showsPhaseDot(entry.fiberPhase) ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(PhaseDot, {
						phase: entry.fiberPhase,
						t
					}) : null, /* @__PURE__ */ (0, react_jsx_runtime.jsx)(StateTag, {
						kind,
						label: stateText
					})] }),
					children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(CardFacts, {
						moduleName: entry.moduleName,
						moduleLabel: t("moduleLabel"),
						entryId: entry.entryId,
						facts: providers !== void 0 ? [[t("configuration"), t("presetProvidedDetail")], [t("enabledIn"), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
							className: PluginInventorySettingsTab_module_css_default.enabledIn,
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: providers.map((preset) => presetName(preset)).join(" · ") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
								type: "button",
								className: PluginInventorySettingsTab_module_css_default.jumpLink,
								onClick: () => {
									setChosenPreset(providers[0].id);
								},
								children: t("viewInPreset")
							})]
						})]] : [[t("configuration"), t(entry.enabled ? "enabledTag" : "disabledTag")], ...entry.enabled ? [[t("runtime"), phaseLabel(entry.fiberPhase, t)]] : []]
					})
				}, key);
			};
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: PluginInventorySettingsTab_module_css_default.section,
				"aria-busy": state.status === "loading",
				children: [
					clientSync.syncing ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("p", {
						className: `${PluginInventorySettingsTab_module_css_default.status} ${PluginInventorySettingsTab_module_css_default.statusWithDot}`,
						role: "status",
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, { state: "ongoing" }), t("clientSyncing")]
					}) : null,
					clientSync.failures.length === 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: PluginInventorySettingsTab_module_css_default.failure,
						"data-client-sync-failure": true,
						children: [
							/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("p", {
								className: PluginInventorySettingsTab_module_css_default.statusWithDot,
								role: "alert",
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, { state: "error" }), t("clientSyncFailed")]
							}),
							/* @__PURE__ */ (0, react_jsx_runtime.jsx)("ul", { children: clientSync.failures.map((failure) => /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("li", { children: [
								failure.id,
								": ",
								failure.message
							] }, failure.id)) }),
							/* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
								type: "button",
								disabled: clientSync.syncing,
								onClick: retryClient,
								children: t("clientSyncRetry")
							})
						]
					}),
					state.status === "loading" ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("p", {
						className: `${PluginInventorySettingsTab_module_css_default.status} ${PluginInventorySettingsTab_module_css_default.statusWithDot}`,
						role: "status",
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, { state: "ongoing" }), t("loading")]
					}) : null,
					state.status === "error" ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: PluginInventorySettingsTab_module_css_default.failure,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("p", {
							className: PluginInventorySettingsTab_module_css_default.statusWithDot,
							role: "alert",
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, { state: "error" }), t("error")]
						}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
							type: "button",
							onClick: retry,
							children: t("retry")
						})]
					}) : null,
					snapshot !== void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: PluginInventorySettingsTab_module_css_default.catalog,
						children: [
							/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("label", {
								className: PluginInventorySettingsTab_module_css_default.search,
								children: [
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconSearchOutlineRegular, { "aria-hidden": "true" }),
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
										className: PluginInventorySettingsTab_module_css_default.visuallyHidden,
										children: t("search")
									}),
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)("input", {
										type: "search",
										value: query,
										placeholder: t("search"),
										"aria-label": t("search"),
										onChange: (event) => {
											setQuery(event.currentTarget.value);
										}
									})
								]
							}),
							entries.length === 0 && presets.length === 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
								className: PluginInventorySettingsTab_module_css_default.status,
								children: t("empty")
							}) : null,
							nothingMatches ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
								className: PluginInventorySettingsTab_module_css_default.status,
								children: t("emptySearch")
							}) : null,
							selected !== void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
								className: PluginInventorySettingsTab_module_css_default.group,
								"data-plugin-scope": "preset",
								"data-preset-id": selected.id,
								children: [
									/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
										className: PluginInventorySettingsTab_module_css_default.groupTitleRow,
										children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
											type: "button",
											className: PluginInventorySettingsTab_module_css_default.groupToggle,
											"aria-expanded": presetEffectiveOpen,
											"aria-controls": `${sectionId}-preset`,
											onClick: () => {
												setPresetOpen(!presetEffectiveOpen);
											},
											children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronDownOutlineRegular, {
												className: PluginInventorySettingsTab_module_css_default.chevron,
												size: 12,
												"aria-hidden": "true"
											}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
												className: PluginInventorySettingsTab_module_css_default.groupTitle,
												children: t("presetTitle")
											})]
										}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
											className: PluginInventorySettingsTab_module_css_default.headerEnd,
											children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Menu, {
												open: switcherOpen,
												onClose: () => {
													setSwitcherOpen(false);
												},
												items: presets.map((preset) => ({
													id: preset.id,
													label: presetLabel(preset, t, presetName)
												})),
												selectedId: selected.id,
												onSelect: (id) => {
													setSwitcherOpen(false);
													setChosenPreset(id);
												},
												align: "end",
												portal: true,
												anchor: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
													type: "button",
													className: PluginInventorySettingsTab_module_css_default.switcher,
													"aria-haspopup": "menu",
													"aria-expanded": switcherOpen,
													"aria-label": t("switcherLabel"),
													onClick: () => {
														setSwitcherOpen((value) => !value);
													},
													children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
														className: PluginInventorySettingsTab_module_css_default.switcherLabel,
														children: presetLabel(selected, t, presetName)
													}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronDownOutlineRegular, {
														className: PluginInventorySettingsTab_module_css_default.chevron,
														"aria-hidden": "true"
													})]
												})
											})
										})]
									}),
									/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("p", {
										className: PluginInventorySettingsTab_module_css_default.groupSub,
										children: [t("presetSubtitle"), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
											"data-preset-plugin-count": selectedRows.length,
											children: ` · ${String(selectedRows.length)} ${t("countUnit")}`
										})]
									}),
									presetEffectiveOpen ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
										id: `${sectionId}-preset`,
										className: PluginInventorySettingsTab_module_css_default.groupBody,
										children: [
											selected.broken !== void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
												className: PluginInventorySettingsTab_module_css_default.brokenNote,
												role: "alert",
												children: selected.broken
											}) : null,
											selectedRows.length > 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("ul", {
												className: PluginInventorySettingsTab_module_css_default.cards,
												children: selectedRows.map((row, index) => presetRowCard(selected, row, index))
											}) : null,
											otherMatchCount > 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("p", {
												className: PluginInventorySettingsTab_module_css_default.hint,
												children: [t("matchesInOtherPresets", { count: String(otherMatchCount) }), otherPresetMatches.map((preset) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
													type: "button",
													className: PluginInventorySettingsTab_module_css_default.jumpLink,
													onClick: () => {
														setChosenPreset(preset.id);
													},
													children: presetName(preset)
												}, preset.id))]
											}) : null
										]
									}) : null
								]
							}) : null,
							entries.length > 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
								className: PluginInventorySettingsTab_module_css_default.group,
								"data-plugin-scope": "global",
								children: [
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
										className: PluginInventorySettingsTab_module_css_default.groupTitleRow,
										children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
											type: "button",
											className: PluginInventorySettingsTab_module_css_default.groupToggle,
											"aria-expanded": globalEffectiveOpen,
											"aria-controls": `${sectionId}-global`,
											onClick: () => {
												setGlobalOpen(!globalEffectiveOpen);
											},
											children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronDownOutlineRegular, {
												className: PluginInventorySettingsTab_module_css_default.chevron,
												size: 12,
												"aria-hidden": "true"
											}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
												className: PluginInventorySettingsTab_module_css_default.groupTitle,
												children: t("globalTitle")
											})]
										})
									}),
									/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("p", {
										className: PluginInventorySettingsTab_module_css_default.groupSub,
										children: [
											t("globalSubtitle"),
											/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
												"data-plugin-count": globalCount,
												children: ` · ${String(globalCount)} ${t("countUnit")}`
											}),
											filteredFailed.length > 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
												className: PluginInventorySettingsTab_module_css_default.failedCount,
												children: [
													filteredFailed.length,
													" ",
													t("failedCountLabel")
												]
											}) : null
										]
									}),
									globalEffectiveOpen && globalCount > 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("ul", {
										className: PluginInventorySettingsTab_module_css_default.cards,
										id: `${sectionId}-global`,
										children: [filteredFailed.map((entry) => globalRowCard(entry)), filteredRegular.map((entry) => globalRowCard(entry, entry.enabled ? void 0 : enabledIn.get(entry.moduleName)))]
									}) : null
								]
							}) : null
						]
					}) : null
				]
			});
		}
		//#endregion
		//#region src/client/locales.ts
		/** Copy dictionaries for the plugin inventory Settings section. */
		/** Simplified Chinese dictionary and key source of truth. */
		const zh = {
			tab: "插件列表",
			loading: "正在读取插件…",
			clientSyncing: "正在同步本页面的插件…",
			clientSyncFailed: "本页面的插件未能完成同步；服务端的启用状态保持不变。",
			clientSyncRetry: "重试本页面同步",
			error: "暂时无法读取插件。",
			metadataError: "包元信息错误：{error}",
			retry: "重试",
			search: "搜索插件",
			empty: "暂无插件。",
			emptySearch: "没有匹配的插件。",
			presetTitle: "会话插件",
			presetSubtitle: "由 Agent 预设按会话组成",
			countUnit: "个",
			switcherLabel: "选择要查看的 Agent 预设",
			presetOptionDefault: "{name}（默认）",
			presetOptionBroken: "{name}（加载失败）",
			globalTitle: "全局插件",
			globalSubtitle: "系统与所有会话共用",
			presetProvidedDetail: "全局已停用，由 Agent 预设按会话提供",
			enabledIn: "启用于",
			viewInPreset: "去预设分组查看",
			matchesInOtherPresets: "其他预设中还有 {count} 个匹配：",
			failedCountLabel: "个失败",
			enabledTag: "已启用",
			disabledTag: "已停用",
			conditionalTag: "条件启用",
			presetEnabledTag: "预设中启用",
			failedTag: "启动失败",
			moduleLabel: "完整名称",
			fromPreset: "来自",
			condition: "禁用条件",
			configuration: "配置状态",
			runtime: "运行状态",
			unobserved: "未运行",
			pending: "等待依赖",
			loadingPhase: "加载中",
			active: "运行中",
			failed: "启动失败",
			unloading: "卸载中"
		};
		/** English dictionary checked against the Chinese key set. */
		const en = {
			tab: "Plugin list",
			loading: "Reading plugins…",
			clientSyncing: "Syncing plugins on this page…",
			clientSyncFailed: "Some plugins could not sync on this page. Host enablement is unchanged.",
			clientSyncRetry: "Retry this page",
			error: "Plugins are temporarily unavailable.",
			metadataError: "Package metadata error: {error}",
			retry: "Retry",
			search: "Search plugins",
			empty: "No plugins are available.",
			emptySearch: "No matching plugins.",
			presetTitle: "Session plugins",
			presetSubtitle: "Composed per session by agent presets",
			countUnit: "plugins",
			switcherLabel: "Choose the agent preset to inspect",
			presetOptionDefault: "{name} (default)",
			presetOptionBroken: "{name} (failed to load)",
			globalTitle: "Global plugins",
			globalSubtitle: "Shared by the system and every session",
			presetProvidedDetail: "Disabled globally; agent presets provide it per session",
			enabledIn: "Enabled in",
			viewInPreset: "View in the preset group",
			matchesInOtherPresets: "{count} more matches in other presets: ",
			failedCountLabel: "failed",
			enabledTag: "Enabled",
			disabledTag: "Disabled",
			conditionalTag: "Conditional",
			presetEnabledTag: "Enabled via presets",
			failedTag: "Failed",
			moduleLabel: "Module",
			fromPreset: "From",
			condition: "Disabled when",
			configuration: "Configuration",
			runtime: "Status",
			unobserved: "Not running",
			pending: "Waiting for dependencies",
			loadingPhase: "Loading",
			active: "Running",
			failed: "Failed to start",
			unloading: "Unloading"
		};
		//#endregion
		//#region src/client/index.ts
		/** Dictionary namespace owned by this plugin. */
		const NS = "settings.pluginInventory";
		/** Services required by the Settings registration and generated Remote face. */
		const inject = [
			"slots",
			"locale",
			"remote",
			"remote.pluginInventory",
			"modules"
		];
		/** Contribute the lazy inventory tab to the Plugins settings section. */
		function apply(ctx) {
			ctx.effect(() => ctx.locale.register(NS, {
				zh,
				en
			}), "ui-settings-plugin-inventory: dictionaries");
			const t = ctx.locale.bind(NS);
			const list = async () => {
				const result = await ctx.remote.pluginInventory.list();
				if (!result.ok) throw new Error(`pluginInventory.list failed: ${result.error.code}: ${result.error.message}`);
				return result.value;
			};
			const agentPresetCopy = ctx.locale.bind("settings.agentPreset");
			const presetName = (preset) => presetDisplayText(preset, agentPresetCopy).name;
			const injected = () => ({
				list,
				presetName,
				resolveText: (text) => ctx.locale.resolveText(text),
				hooks: { clientSync: ctx.modules.entries.state },
				retryClient: () => {
					ctx.modules.entries.retry().catch((error) => {
						ctx.logger.error(error);
					});
				}
			});
			ctx.slots.inject("settings.plugins.tab", () => ctx.slots.register({
				name: "settings.plugins.tab",
				id: "all",
				order: 10,
				label: () => t("tab"),
				locale: NS,
				inject: injected
			}, PluginInventorySettingsTab));
		}
		//#endregion
		exports.NS = NS;
		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map