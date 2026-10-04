window.__ModuleLoader__.load({
	id: "@deepseek-ai/dsh-client-ui-permission-presets",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		Object.defineProperty(exports, Symbol.toStringTag, { value: "Module" });
		let _deepseek_ai_dsh_client_store = require("@deepseek-ai/dsh-client-store");
		let react = require("react");
		let _deepseek_ai_dsh_client_ui_primitives = require("@deepseek-ai/dsh-client-ui-primitives");
		let react_jsx_runtime = require("react/jsx-runtime");
		//#region src/client/catalog.ts
		/** One latest-result-wins catalog reader for the whole browser process. */
		var PermissionCatalogDirectory = class {
			ctx;
			/** Complete snapshot consumed by both the slash popup and composer seat. */
			store = (0, _deepseek_ai_dsh_client_store.createSnapshotStore)({ value: null });
			/**
			* One tick per invalidation (a catalog notification or a connection-generation
			* change), published before the replacement read starts. Consumers that must
			* drop displayed options subscribe here instead of to {@link store}, whose
			* publications also settle a read a displayed surface is waiting for.
			*/
			invalidations = (0, _deepseek_ai_dsh_client_store.createSnapshotStore)({ count: 0 });
			connection;
			stopCatalog;
			stopGeneration;
			generationId;
			initialized = false;
			epoch = 0;
			pending;
			failure = /* @__PURE__ */ new Error("permission catalog has no complete value");
			disposed = false;
			/**
			* Subscribe to both invalidation sources before the first read, closing the
			* install/read race.
			* @param ctx - root Client context carrying Remote and Connection.
			*/
			constructor(ctx) {
				this.ctx = ctx;
				this.connection = ctx.get("connection");
				this.stopCatalog = ctx.remote.$on("permission-presets/catalog-changed", () => {
					this.invalidate();
					this.refresh();
				});
				this.stopGeneration = this.connection.generation.subscribe(() => {
					this.syncGeneration();
				});
				this.syncGeneration();
			}
			/**
			* Publish one invalidation tick for consumers holding displayed options.
			* Neither caller can run after disposal: `dispose()` unsubscribes the
			* catalog-changed listener, and `syncGeneration()` returns early when the
			* directory is disposed.
			*/
			invalidate() {
				this.invalidations.set({ count: this.invalidations.getSnapshot().count + 1 });
			}
			/** Force a fresh complete read for the active connection generation. */
			refresh() {
				if (this.disposed) return;
				const generationId = this.connection.generation.getSnapshot()?.id;
				if (generationId === void 0) return;
				if (generationId !== this.generationId) {
					this.syncGeneration();
					return;
				}
				this.startRead(generationId);
			}
			/**
			* Resolve a complete current-generation catalog for an imperative popup
			* open. An active refresh settles before a retained value can be reused.
			* @returns The active Host generation's complete permission catalog.
			*/
			async load() {
				if (this.pending === void 0 && this.store.getSnapshot().value === null) this.refresh();
				while (!this.disposed) {
					const generationId = this.connection.generation.getSnapshot()?.id;
					if (generationId === void 0) throw new Error("permission catalog has no active Host connection");
					if (generationId !== this.generationId) this.syncGeneration();
					const pending = this.pending;
					if (pending !== void 0) {
						await pending;
						continue;
					}
					const state = this.store.getSnapshot();
					if (state.value !== null) return state.value;
					throw this.failure;
				}
				throw new Error("permission catalog directory is disposed");
			}
			/** Stop subscriptions and revoke every late settlement's write access. */
			dispose() {
				if (this.disposed) return;
				this.disposed = true;
				++this.epoch;
				this.pending = void 0;
				this.stopGeneration();
				this.stopCatalog();
			}
			/** Observe generation loss/replacement and hard-clear the old Host value. */
			syncGeneration() {
				if (this.disposed) return;
				const generationId = this.connection.generation.getSnapshot()?.id;
				if (this.initialized && generationId === this.generationId) return;
				if (this.initialized) this.invalidate();
				this.initialized = true;
				this.generationId = generationId;
				++this.epoch;
				this.pending = void 0;
				this.failure = /* @__PURE__ */ new Error("permission catalog has no complete value");
				this.store.set({ value: null });
				if (generationId !== void 0) this.startRead(generationId);
			}
			/** Start one independent read; the newest epoch in the same generation wins. */
			startRead(generationId) {
				const epoch = ++this.epoch;
				this.failure = /* @__PURE__ */ new Error("permission catalog has no complete value");
				const operation = this.ctx.remote.permissionPresets.catalog().then((result) => {
					if (!result.ok) throw new Error(`${result.error.code}: ${result.error.message}`);
					if (!this.accepts(epoch, generationId)) return;
					this.store.set({ value: result.value });
				}).catch((error) => {
					if (!this.accepts(epoch, generationId)) return;
					this.failure = error instanceof Error ? error : new Error(String(error));
					this.store.set({ value: null });
				}).finally(() => {
					if (this.pending === operation) this.pending = void 0;
				});
				this.pending = operation;
			}
			/** Fence by disposal, refresh epoch, and the actual Connection generation. */
			accepts(epoch, generationId) {
				return !this.disposed && epoch === this.epoch && generationId === this.generationId && this.connection.generation.getSnapshot()?.id === generationId;
			}
		};
		//#endregion
		//#region ../../../node_modules/.pnpm/clsx@2.1.1/node_modules/clsx/dist/clsx.mjs
		function r(e) {
			var t, f, n = "";
			if ("string" == typeof e || "number" == typeof e) n += e;
			else if ("object" == typeof e) if (Array.isArray(e)) {
				var o = e.length;
				for (t = 0; t < o; t++) e[t] && (f = r(e[t])) && (n && (n += " "), n += f);
			} else for (f in e) e[f] && (n && (n += " "), n += f);
			return n;
		}
		function clsx() {
			for (var e, t, f = 0, n = "", o = arguments.length; f < o; f++) (e = arguments[f]) && (t = r(e)) && (n && (n += " "), n += t);
			return n;
		}
		//#endregion
		//#region src/client/locales.ts
		/** `settings.permission` namespace dictionaries (the Permission row's copy). */
		/** Locale namespace shared by both current-session permission pickers. */
		const PERMISSION_ACCESS_NS = "permission.access";
		/** Simplified Chinese dictionary (the key-set source of truth). */
		const zh = {
			"title": "权限",
			"description": "选择新会话的默认权限模式",
			"loading": "加载中",
			"unavailable": "不可用",
			"preset.readOnly": "仅可查看",
			"preset.workspaceWrite": "工作区内修改",
			"preset.fullAccess": "完全权限",
			"confirm.title": "确认启用完全权限？",
			"confirm.description": "启用完全权限后，新会话将减少确认步骤，并且可以直接执行更多操作，包括敏感操作、文件修改或外部命令。仅建议在你信任后续任务时使用。",
			"confirm.acknowledge": "我已了解风险，并愿意继续",
			"confirm.cancel": "取消",
			"confirm.enable": "启用完全权限"
		};
		/** English dictionary, checked complete against the zh key set. */
		const en = {
			"title": "Permission",
			"description": "Choose the default permission mode for new sessions",
			"loading": "Loading",
			"unavailable": "Unavailable",
			"preset.readOnly": "Read Only",
			"preset.workspaceWrite": "Workspace Write",
			"preset.fullAccess": "Full access",
			"confirm.title": "Enable Full access?",
			"confirm.description": "Full access lets new sessions reduce confirmation steps and perform more actions directly, including sensitive operations, file changes, or external commands. Only use it when you trust subsequent tasks.",
			"confirm.acknowledge": "I understand the risks and want to continue",
			"confirm.cancel": "Cancel",
			"confirm.enable": "Enable Full access"
		};
		/** Simplified Chinese dictionary for the current-session popup gate. */
		const accessZh = {
			"mode": "访问模式，当前：{name}",
			"close": "关闭",
			"preset.readOnly": "仅可查看",
			"preset.workspaceWrite": "工作区内修改",
			"preset.fullAccess": "完全权限",
			"confirm.title": "确认启用完全权限？",
			"confirm.description": "启用完全权限后，智能体将减少确认步骤，并且可以直接执行更多操作，包括敏感操作、文件修改或外部命令。仅建议在你信任当前任务时使用。",
			"confirm.acknowledge": "我已了解风险，并愿意继续",
			"confirm.cancel": "取消",
			"confirm.enable": "启用完全权限",
			"auto.label": "Auto review",
			"auto.badge": "EXP",
			"auto.description": "无沙箱运行；每次原生工具调用和 PTC 内层调用前由同一模型进行实验性审查。",
			"auto.confirm.title": "确认启用 Auto review（实验）？",
			"auto.confirm.description": "Auto review 不使用沙箱。每次原生工具调用和 PTC 内层调用前，都会由与当前 agent 相同的模型进行审查；审查拒绝的调用由你批准或拒绝。此功能仍属实验性，可能误放行或误拒绝，并会消耗额外 token。",
			"auto.confirm.acknowledge": "我已了解这些风险，并愿意继续",
			"auto.confirm.enable": "启用 Auto review"
		};
		/** English dictionary for the current-session popup gate. */
		const accessEn = {
			"mode": "Access mode, current: {name}",
			"close": "Close",
			"preset.readOnly": "Read Only",
			"preset.workspaceWrite": "Workspace Write",
			"preset.fullAccess": "Full access",
			"confirm.title": "Enable Full access?",
			"confirm.description": "Full access reduces confirmation steps and lets the agent perform more actions directly, including sensitive operations, file changes, or external commands. Only use it when you trust the current task.",
			"confirm.acknowledge": "I understand the risks and want to continue",
			"confirm.cancel": "Cancel",
			"confirm.enable": "Enable Full access",
			"auto.label": "Auto review",
			"auto.badge": "EXP",
			"auto.description": "Run without a sandbox after an experimental same-model review of every native tool call and PTC inner call.",
			"auto.confirm.title": "Enable Auto review (experimental)?",
			"auto.confirm.description": "Auto review runs without a sandbox. Before every native tool call and PTC inner call, the same model as the current agent reviews whether to allow it; you approve or reject each call it denies. This feature is experimental, can falsely allow or deny actions, and uses additional tokens.",
			"auto.confirm.acknowledge": "I understand these risks and want to continue",
			"auto.confirm.enable": "Enable Auto review"
		};
		//#endregion
		//#region src/client/presentation.ts
		/** Machine value of the preset that requires an explicit GUI risk gate. */
		const FULL_ACCESS_PRESET = "danger-full-access";
		const PRESET_LABEL_KEYS = new Map([
			["read-only", "preset.readOnly"],
			["workspace-write", "preset.workspaceWrite"],
			[FULL_ACCESS_PRESET, "preset.fullAccess"]
		]);
		const DEFAULT_PRESET_LABELS = {
			"preset.readOnly": en["preset.readOnly"],
			"preset.workspaceWrite": en["preset.workspaceWrite"],
			"preset.fullAccess": en["preset.fullAccess"]
		};
		/**
		* Convert conventional kebab-case preset names into user-facing title case.
		* @param name - host-supplied preset label or key.
		* @returns the title-cased conventional key, or a non-kebab label unchanged.
		*/
		function displayPresetName(name) {
			if (!/^[a-z0-9]+(-[a-z0-9]+)*$/.test(name)) return name;
			return name.split("-").map((word) => word.charAt(0).toUpperCase() + word.slice(1)).join(" ");
		}
		/**
		* Render a permission preset under its product label.
		* @param value - preset machine value.
		* @param name - host-supplied preset name.
		* @param t - optional locale dictionary lookup for built-in product labels.
		* @returns the built-in product label or the conventional display name.
		*/
		function displayPermissionPreset(value, name, t) {
			const key = PRESET_LABEL_KEYS.get(value);
			if (key !== void 0 && (name === value || name === DEFAULT_PRESET_LABELS[key])) return t?.(key) ?? DEFAULT_PRESET_LABELS[key];
			return displayPresetName(name);
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-permission-presets/src/client/PermissionSelect.module.css.mjs
		const css$1 = ".uiNSVG_trigger{border-radius:var(--dsw-radius-sm);min-width:0;max-width:220px;height:28px;color:var(--dsw-alias-label-secondary);cursor:pointer;background:0 0;border:none;outline:none;align-items:center;gap:4px;padding:0 4px 0 8px;font-size:13px;font-weight:500;line-height:20px;display:inline-flex}.uiNSVG_trigger:hover:not(:disabled){background:var(--dsw-alias-interactive-bg-hover)}.uiNSVG_trigger:focus-visible{box-shadow:0 0 0 2px var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary))}.uiNSVG_trigger:disabled{color:var(--dsw-alias-label-dimmed);cursor:default}.uiNSVG_triggerIcon{flex:none;display:inline-flex}.uiNSVG_triggerIcon svg{width:14px;height:14px}.uiNSVG_triggerLabel{text-overflow:ellipsis;white-space:nowrap;min-width:0;overflow:hidden}.uiNSVG_optionLabel{align-items:baseline;gap:4px;min-width:0;max-width:100%;display:inline-flex}.uiNSVG_optionLabelText{text-overflow:ellipsis;white-space:nowrap;min-width:0;overflow:hidden}.uiNSVG_badge{color:var(--dsw-alias-label-tertiary);letter-spacing:.2px;flex:none;align-self:flex-start;margin-top:-1px;font-size:8px;font-weight:600;line-height:10px}.uiNSVG_chevron{color:var(--dsw-alias-label-caption);flex:none;transition:transform .12s;display:inline-flex}@container (width<=460px){.uiNSVG_trigger:has(.uiNSVG_triggerIcon) .uiNSVG_triggerLabel{display:none}}.uiNSVG_chevronOpen{transform:rotate(180deg)}";
		const tagId$1 = "@deepseek-ai/dsh-client-ui-permission-presets/PermissionSelect.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$1) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-permission-presets";
			tag.dataset.pluginCss = tagId$1;
			tag.textContent = css$1;
			document.head.appendChild(tag);
		}
		var PermissionSelect_module_css_default = {
			"badge": "uiNSVG_badge",
			"chevron": "uiNSVG_chevron",
			"chevronOpen": "uiNSVG_chevronOpen",
			"optionLabel": "uiNSVG_optionLabel",
			"optionLabelText": "uiNSVG_optionLabelText",
			"trigger": "uiNSVG_trigger",
			"triggerIcon": "uiNSVG_triggerIcon",
			"triggerLabel": "uiNSVG_triggerLabel"
		};
		//#endregion
		//#region src/client/PermissionSelect.tsx
		const permissionGlyphs = new Map([
			["read-only", /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.PermissionIconReadOnlyRegular, {})],
			["workspace-write", /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.PermissionIconWorkspaceWriteRegular, {})],
			[FULL_ACCESS_PRESET, /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.PermissionIconFullAccessRegular, {})]
		]);
		/** Glyph for a permission option value; host-configured names outside the design set get none. */
		function permissionGlyph(value) {
			return permissionGlyphs.get(value);
		}
		function permissionLabel(value, name, t) {
			if (value === "auto") return t("auto.label");
			return displayPermissionPreset(value, name, (key) => t(key));
		}
		function optionBadge(value, t) {
			return value === "auto" ? t("auto.badge") : void 0;
		}
		/** Resolve locale-owned copy for the shipped Auto option; preserve host copy for other presets. */
		function optionDescription(option, t) {
			return option.value === "auto" ? t("auto.description") : option.description;
		}
		function PermissionSelect({ locked, select, usePermissionCatalog, useProjection, t }) {
			const selection = useProjection("permissions");
			const catalog = usePermissionCatalog((state) => state.value);
			const [pick, setPick] = (0, react.useState)(null);
			const [open, setOpen] = (0, react.useState)(false);
			const [confirmation, setConfirmation] = (0, react.useState)(null);
			const [acknowledged, setAcknowledged] = (0, react.useState)(false);
			(0, react.useEffect)(() => {
				if (!locked && selection !== void 0 && catalog !== null && (confirmation === null || catalog.options.some((option) => option.value === confirmation))) return;
				setOpen(false);
				setAcknowledged(false);
				setConfirmation(null);
			}, [
				catalog,
				confirmation,
				locked,
				selection
			]);
			if (selection === void 0 || catalog === null) return null;
			const currentValue = pick !== null && catalog.options.some((option) => option.value === pick) ? pick : selection.currentValue;
			const current = catalog.options.find((option) => option.value === currentValue);
			const currentLabel = current === void 0 ? permissionLabel(currentValue, currentValue, t) : permissionLabel(current.value, current.name, t);
			const busy = pick !== null || confirmation !== null;
			const items = catalog.options.map((option) => {
				const icon = permissionGlyph(option.value);
				const label = permissionLabel(option.value, option.name, t);
				const badge = optionBadge(option.value, t);
				return {
					id: option.value,
					label: badge === void 0 ? label : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
						className: PermissionSelect_module_css_default.optionLabel,
						"aria-label": `${label} ${badge}`,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: PermissionSelect_module_css_default.optionLabelText,
							children: label
						}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("sup", {
							className: PermissionSelect_module_css_default.badge,
							children: badge
						})]
					}),
					...icon === void 0 ? {} : { icon }
				};
			});
			const submit = (id) => {
				setPick(id);
				select(id).catch(() => false).then(() => {
					setPick(null);
				});
			};
			const choose = (id) => {
				setOpen(false);
				if (id === selection.currentValue) return;
				if (id === "danger-full-access" || id === "auto") {
					setAcknowledged(false);
					setConfirmation(id);
					return;
				}
				submit(id);
			};
			const closeConfirmation = () => {
				setAcknowledged(false);
				setConfirmation(null);
			};
			const confirmSelection = (id) => {
				closeConfirmation();
				submit(id);
			};
			const confirmationTitle = confirmation === "auto" ? t("auto.confirm.title") : t("confirm.title");
			const confirmationDescription = confirmation === "auto" ? t("auto.confirm.description") : t("confirm.description");
			const confirmationAcknowledge = confirmation === "auto" ? t("auto.confirm.acknowledge") : t("confirm.acknowledge");
			const confirmationEnable = confirmation === "auto" ? t("auto.confirm.enable") : t("confirm.enable");
			const currentBadge = optionBadge(currentValue, t);
			const currentAccessibleLabel = currentBadge === void 0 ? currentLabel : `${currentLabel} ${currentBadge}`;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Menu, {
				open,
				items,
				selectedId: currentValue,
				onSelect: choose,
				onClose: () => {
					setOpen(false);
				},
				side: "top",
				portal: true,
				anchor: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
					type: "button",
					className: PermissionSelect_module_css_default.trigger,
					"aria-label": t("mode", { name: currentAccessibleLabel }),
					title: current === void 0 ? void 0 : optionDescription(current, t),
					disabled: locked || busy,
					onClick: () => {
						setOpen(!open);
					},
					children: [
						permissionGlyph(currentValue) !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: PermissionSelect_module_css_default.triggerIcon,
							"aria-hidden": true,
							children: permissionGlyph(currentValue)
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: PermissionSelect_module_css_default.triggerLabel,
							children: currentLabel
						}),
						currentBadge !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("sup", {
							className: PermissionSelect_module_css_default.badge,
							children: currentBadge
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: clsx(PermissionSelect_module_css_default.chevron, open && PermissionSelect_module_css_default.chevronOpen),
							"aria-hidden": true,
							children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronDownOutlineRegular, {})
						})
					]
				})
			}), confirmation !== null && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.RiskConfirmation, {
				open: true,
				title: confirmationTitle,
				description: confirmationDescription,
				acknowledgeLabel: confirmationAcknowledge,
				cancelLabel: t("confirm.cancel"),
				closeLabel: t("close"),
				confirmLabel: confirmationEnable,
				acknowledged,
				disabled: locked,
				onAcknowledgedChange: setAcknowledged,
				onCancel: closeConfirmation,
				onConfirm: () => {
					confirmSelection(confirmation);
				}
			})] });
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-permission-presets/src/client/PermissionRow.module.css.mjs
		const css = "._7gBvmG_row{border-bottom:.5px solid var(--dsw-alias-border-l2);align-items:center;gap:8px;padding:16px 0;display:flex}._7gBvmG_rowText{flex-direction:column;flex:1;gap:4px;min-width:0;padding-right:48px;display:flex}._7gBvmG_title{color:var(--dsw-alias-label-primary);font-size:14px;font-weight:400;line-height:22px}._7gBvmG_desc{color:var(--dsw-alias-label-tertiary);font-size:12px;font-weight:400;line-height:18px}._7gBvmG_selector{border-radius:var(--dsw-radius-md);background:var(--dsw-alias-bg-module-platform);height:36px;font:inherit;color:var(--dsw-alias-label-primary);cursor:pointer;border:none;align-items:center;gap:12px;padding:0 14px;font-size:14px;line-height:22px;display:inline-flex}._7gBvmG_selector:hover:not(:disabled){background:var(--dsw-alias-interactive-bg-hover)}._7gBvmG_selector:disabled{cursor:default}._7gBvmG_chevron{flex:none}";
		const tagId = "@deepseek-ai/dsh-client-ui-permission-presets/PermissionRow.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-permission-presets";
			tag.dataset.pluginCss = tagId;
			tag.textContent = css;
			document.head.appendChild(tag);
		}
		var PermissionRow_module_css_default = {
			"chevron": "_7gBvmG_chevron",
			"desc": "_7gBvmG_desc",
			"row": "_7gBvmG_row",
			"rowText": "_7gBvmG_rowText",
			"selector": "_7gBvmG_selector",
			"title": "_7gBvmG_title"
		};
		//#endregion
		//#region src/client/PermissionRow.tsx
		/**
		* Permission preference row: the default preset for subsequently created
		* sessions. Current-session switches remain on the composer `/permission`
		* control.
		*/
		/**
		* Render the new-session Permission default selector.
		* @param props - composed slot props.
		* @returns the row, or null when the host does not expose permission settings.
		*/
		function PermissionRow({ load, select, usePermission, t }) {
			const state = usePermission((snapshot) => snapshot);
			const [open, setOpen] = (0, react.useState)(false);
			const [confirmingFullAccess, setConfirmingFullAccess] = (0, react.useState)(false);
			const [acknowledged, setAcknowledged] = (0, react.useState)(false);
			(0, react.useEffect)(() => {
				load();
			}, [load]);
			(0, react.useEffect)(() => {
				if (state.writable && state.status !== "unavailable") return;
				setOpen(false);
				setAcknowledged(false);
				setConfirmingFullAccess(false);
			}, [state.status, state.writable]);
			if (state.status === "unavailable") return null;
			const selected = state.options.find((option) => option.id === state.currentValue);
			const busy = state.status === "loading" || state.status === "saving" || confirmingFullAccess;
			const optionLabel = (option) => displayPermissionPreset(option.id, option.label, t);
			const label = selected !== void 0 ? optionLabel(selected) : busy ? t("loading") : t("unavailable");
			const description = state.error ?? t("description");
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: PermissionRow_module_css_default.row,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: PermissionRow_module_css_default.rowText,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						className: PermissionRow_module_css_default.title,
						children: t("title")
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						className: PermissionRow_module_css_default.desc,
						role: state.error === null ? void 0 : "alert",
						children: description
					})]
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Menu, {
					open,
					onClose: () => {
						setOpen(false);
					},
					items: state.options.map((option) => ({
						id: option.id,
						label: optionLabel(option)
					})),
					selectedId: state.currentValue,
					onSelect: (id) => {
						setOpen(false);
						if (id === state.currentValue) return;
						if (id === "danger-full-access") {
							setAcknowledged(false);
							setConfirmingFullAccess(true);
							return;
						}
						select(id);
					},
					align: "end",
					portal: true,
					anchor: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
						type: "button",
						className: PermissionRow_module_css_default.selector,
						"aria-haspopup": "menu",
						"aria-expanded": open,
						disabled: busy || !state.writable || state.options.length === 0,
						onClick: () => {
							setOpen((value) => !value);
						},
						children: [label, /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronDownOutlineRegular, { className: PermissionRow_module_css_default.chevron })]
					})
				})]
			}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.RiskConfirmation, {
				open: confirmingFullAccess,
				title: t("confirm.title"),
				description: t("confirm.description"),
				acknowledgeLabel: t("confirm.acknowledge"),
				cancelLabel: t("confirm.cancel"),
				closeLabel: t("close"),
				confirmLabel: t("confirm.enable"),
				acknowledged,
				disabled: !state.writable || state.status === "saving",
				onAcknowledgedChange: setAcknowledged,
				onCancel: () => {
					setAcknowledged(false);
					setConfirmingFullAccess(false);
				},
				onConfirm: () => {
					setAcknowledged(false);
					setConfirmingFullAccess(false);
					select(FULL_ACCESS_PRESET);
				}
			})] });
		}
		//#endregion
		//#region src/client/settings-store.ts
		/** Permission's settings namespace on the host wire. */
		const PERMISSION_SETTINGS_NS = "permission";
		/** Resolve the new-session choices from the permission domain's catalog.
		* @param view Live configuration descriptor.
		* @param catalog Configured permission options and effective default.
		* @returns Current choice and labels for its allowed options.
		*/
		function permissionDefaultOf(view, catalog) {
			const currentValue = view.value.defaultPreset ?? catalog.defaultPreset;
			const options = catalog.defaultOptions.map((option) => ({
				id: option.value,
				label: displayPermissionPreset(option.value, option.name)
			}));
			if (!options.some((option) => option.id === currentValue)) throw new Error("permission catalog does not advertise its current default");
			return {
				currentValue,
				options
			};
		}
		/** Controller deriving the row from the shared mirror and writing the default through it. */
		var PermissionPresetSettingsController = class {
			describeFace;
			ctx;
			catalog;
			/** Row snapshot consumed through a bound selector hook. */
			store = (0, _deepseek_ai_dsh_client_store.createSnapshotStore)({
				status: "idle",
				error: null,
				writable: false,
				currentValue: "",
				options: [],
				revision: 0
			});
			following;
			followingCatalog;
			saving = false;
			disposed = false;
			/**
			* @param describeFace - the shared mirror's read/fold face (descriptor and schema source).
			* @param ctx - the row plugin's context, whose `remote.settings` namespace
			* carries the `defaultPreset` write.
			* @param catalog - configured permission choices and their effective default.
			*/
			constructor(describeFace, ctx, catalog) {
				this.describeFace = describeFace;
				this.ctx = ctx;
				this.catalog = catalog;
			}
			/**
			* Begin following the mirror (idempotent) and reflect its current answer.
			* @returns settlement once the snapshot reflects the mirror.
			*/
			async load() {
				if (this.disposed) return;
				this.following ??= this.describeFace.subscribe(() => {
					this.derive();
				});
				this.followingCatalog ??= this.catalog.store.subscribe(() => {
					this.derive();
				});
				this.store.update((state) => {
					state.status = "loading";
					state.error = null;
				});
				await this.describeFace.ensure();
				if (this.describeFace.getSnapshot().status !== "unavailable") try {
					await this.catalog.load();
				} catch (error) {
					this.fail(error);
					return;
				}
				this.derive();
			}
			/**
			* Persist one preset as the default for subsequently created sessions.
			* A selection made while one is already saving is ignored — the row's
			* control is disabled during the save, so this only drops programmatic
			* double-submits rather than user intent.
			* @param preset - advertised preset key.
			* @returns nothing; {@link store} carries success or failure.
			*/
			async select(preset) {
				const state = this.store.getSnapshot();
				const view = this.describeFace.getSnapshot().view?.namespaces.find((entry) => entry.ns === PERMISSION_SETTINGS_NS);
				if (view === void 0 || !state.writable || this.saving) return;
				this.saving = true;
				this.store.update((draft) => {
					draft.status = "saving";
					draft.error = null;
				});
				let response;
				try {
					response = await this.ctx.remote.settings.mutate(PERMISSION_SETTINGS_NS, [{
						op: "set",
						path: ["defaultPreset"],
						value: preset
					}], view.revision);
				} finally {
					this.saving = false;
				}
				if (this.disposed) return;
				if (!response.ok) {
					this.fail(response.error);
					return;
				}
				this.describeFace.acceptView(response.value);
			}
			/** Stop following the mirror; later publishes leave the snapshot alone. */
			dispose() {
				this.disposed = true;
				this.following?.();
				this.following = void 0;
				this.followingCatalog?.();
				this.followingCatalog = void 0;
			}
			derive() {
				if (this.disposed || this.saving) return;
				const mirrored = this.describeFace.getSnapshot();
				if (mirrored.status === "unavailable") {
					this.store.update((state) => {
						state.status = "unavailable";
						state.writable = false;
						state.currentValue = "";
						state.options = [];
					});
					return;
				}
				if (mirrored.view === void 0) {
					if (mirrored.error !== null) this.fail(new Error(mirrored.error));
					return;
				}
				const view = mirrored.view.namespaces.find((entry) => entry.ns === PERMISSION_SETTINGS_NS);
				if (view === void 0) {
					this.store.update((state) => {
						state.status = "unavailable";
						state.writable = false;
						state.currentValue = "";
						state.options = [];
					});
					return;
				}
				try {
					const catalog = this.catalog.store.getSnapshot().value;
					if (catalog === null) return;
					const resolved = permissionDefaultOf(view, catalog);
					const { writable } = mirrored.view;
					this.store.update((state) => {
						state.status = "ready";
						state.error = null;
						state.writable = writable;
						state.currentValue = resolved.currentValue;
						state.options = resolved.options;
						state.revision = view.revision;
					});
				} catch (error) {
					this.fail(error);
				}
			}
			fail(error) {
				this.store.update((state) => {
					state.status = "error";
					state.error = error instanceof Error ? error.message : String(error);
				});
			}
		};
		//#endregion
		//#region src/client/index.ts
		/** Required services (cordis fiber inject). */
		const inject = [
			"commandUi",
			"connection",
			"sessions",
			"slots",
			"locale",
			"remote",
			"remote.permissionPresets",
			"remote.settings",
			"configForms",
			"settingsSchema"
		];
		/** Read one session's current permissions projection value (undefined = capability absent). */
		function selectionOf(session) {
			return session?.projections.faceOf("permissions").getSnapshot();
		}
		/** Join the process catalog with one Session's current value. */
		function optionsOf(catalog, currentValue, t) {
			return catalog.options.map((option) => ({
				id: option.value,
				label: option.value === "auto" ? t("auto.label") : displayPermissionPreset(option.value, option.name, t),
				...option.value === "auto" ? { badge: t("auto.badge") } : {},
				...option.value === "auto" ? { detail: t("auto.description") } : option.description !== void 0 ? { detail: option.description } : {},
				...option.value === currentValue ? { active: true } : {},
				...option.value === "danger-full-access" || option.value === "auto" ? { confirmation: {
					title: t(option.value === "auto" ? "auto.confirm.title" : "confirm.title"),
					description: t(option.value === "auto" ? "auto.confirm.description" : "confirm.description"),
					acknowledgeLabel: t(option.value === "auto" ? "auto.confirm.acknowledge" : "confirm.acknowledge"),
					cancelLabel: t("confirm.cancel"),
					confirmLabel: t(option.value === "auto" ? "auto.confirm.enable" : "confirm.enable")
				} } : {}
			}));
		}
		/**
		* Client plugin body: register the /permission popup picker over the
		* permissions projection.
		* @param ctx - client root context.
		*/
		function apply(ctx) {
			const command = ctx.get("commandUi");
			const sessions = ctx.sessions;
			ctx.effect(() => ctx.locale.register(PERMISSION_ACCESS_NS, {
				zh: accessZh,
				en: accessEn
			}), "ui-permission: current-session dictionaries");
			const t = ctx.locale.bind(PERMISSION_ACCESS_NS);
			const sessionFor = (session) => sessions.binding(session.sessionId)?.session;
			const submit = async (sessionId, preset) => {
				const live = sessions.binding(sessionId)?.session;
				if (live === void 0) throw new Error("this session is not materialized yet");
				const result = await live.command(`/permission ${preset}`);
				if (!result.ok) throw new Error(`permission switch failed: ${result.error.code}: ${result.error.message}`);
				if (!result.value.matched) throw new Error("the host offers no /permission command");
				return true;
			};
			const catalog = new PermissionCatalogDirectory(ctx);
			ctx.effect(() => () => {
				catalog.dispose();
			}, "ui-permission: process catalog directory");
			ctx.effect(() => catalog.invalidations.subscribe(() => {
				command.dismiss("permission");
			}), "ui-permission: dismiss stale slash choices");
			ctx.effect(() => ctx.locale.register("settings.permission", {
				zh,
				en
			}), "ui-permission: settings row dictionaries");
			const controller = new PermissionPresetSettingsController(ctx.configForms.describe(), ctx, catalog);
			const load = () => controller.load();
			const select = (preset) => controller.select(preset);
			const injected = () => ({
				hooks: { permission: controller.store },
				load,
				select
			});
			ctx.effect(() => () => {
				controller.dispose();
			}, "ui-permission: settings row directory");
			ctx.slots.inject("settings.general.item", () => ctx.slots.register({
				name: "settings.general.item",
				id: "permission",
				order: -20,
				locale: "settings.permission",
				inject: injected
			}, PermissionRow));
			ctx.slots.inject("conversation.input.permission", () => ctx.slots.register({
				name: "conversation.input.permission",
				locale: PERMISSION_ACCESS_NS,
				inject: (sessionId) => ({
					hooks: { permissionCatalog: catalog.store },
					select: (preset) => submit(sessionId, preset)
				})
			}, PermissionSelect));
			ctx.effect(() => command.decorate({
				name: "permission",
				available: (session) => selectionOf(sessionFor(session)) !== void 0,
				ui: {
					kind: "popupSelect",
					options: async (session) => {
						const selection = selectionOf(sessionFor(session));
						if (selection === void 0) throw new Error("permission presets are not available on this host");
						return optionsOf(await catalog.load(), selection.currentValue, t);
					},
					onSelect: (option, session) => submit(session.sessionId, option.id).then(() => void 0)
				}
			}), "ui-permission: /permission decoration");
		}
		//#endregion
		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map