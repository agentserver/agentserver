window.__ModuleLoader__.load({
	id: "@deepseek-ai/dsh-client-ui-settings-general",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		Object.defineProperty(exports, Symbol.toStringTag, { value: "Module" });
		let _deepseek_ai_dsh_client_ui_slots = require("@deepseek-ai/dsh-client-ui-slots");
		let _deepseek_ai_dsh_client_ui_primitives = require("@deepseek-ai/dsh-client-ui-primitives");
		let _deepseek_ai_dsh_client_store = require("@deepseek-ai/dsh-client-store");
		let react = require("react");
		let react_dom = require("react-dom");
		let react_jsx_runtime = require("react/jsx-runtime");
		//#region src/client/shell-store.ts
		/** Shared settings viewing state for its mouse and command entry points. */
		/**
		* Declare the settings dialog state and its complete mutation API.
		* @returns one root-scoped store handle for the settings shell.
		*/
		function createSettingsShellStore() {
			return (0, _deepseek_ai_dsh_client_store.defineStore)({
				init: () => ({
					open: false,
					activeId: void 0
				}),
				actions: {
					open: (d) => {
						d.open = true;
					},
					close: (d) => {
						d.open = false;
						d.activeId = void 0;
					},
					select: (d, id) => {
						d.activeId = id;
					},
					openSection: (d, id) => {
						d.activeId = id;
						d.open = true;
					}
				}
			});
		}
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
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-settings-general/src/client/SettingsRoot.module.css.mjs
		const css$6 = "._8ynAeW_triggerRow{flex:none;align-items:center;gap:8px;width:calc(100% + 4px);margin:4px -2px;display:flex;position:relative}._8ynAeW_triggerRow._8ynAeW_railRow{width:36px;margin:8px 0 10px}._8ynAeW_trigger{box-sizing:border-box;border-radius:var(--dsw-radius-md);cursor:pointer;width:auto;min-width:0;height:42px;color:var(--dsw-alias-label-primary);background:0 0;border:none;flex:1;align-items:center;gap:8px;margin:0;padding:0 10px 0 8px;font-family:inherit;font-size:14px;line-height:22px;display:flex;overflow:hidden}._8ynAeW_trigger:hover{background:var(--dsw-alias-interactive-bg-hover)}._8ynAeW_trigger._8ynAeW_rail{flex:none;justify-content:center;gap:0;width:36px;height:36px;margin:0;padding:0}._8ynAeW_triggerLabel{white-space:nowrap;overflow:hidden}._8ynAeW_overlay{z-index:1000;justify-content:center;align-items:center;display:flex;position:fixed;inset:0}._8ynAeW_mask{background:var(--dsw-alias-bg-mask-1);backdrop-filter:var(--dsw-mask-blur);position:absolute;inset:0}._8ynAeW_panel{z-index:1;width:800px;height:min(800px, calc(100vh - 2 * max(24px, var(--dsh-frame-top-clearance,24px))));border-radius:var(--dsw-radius-panel);background:var(--dsw-alias-bg-layer-2);max-width:calc(100vw - 48px);box-shadow:var(--dsw-elevation-prominent);--dsh-scrollbar-thumb:var(--dsw-alias-scrollbar-bg-l2);--dsh-scrollbar-thumb-hover:var(--dsw-alias-scrollbar-hover-l2);display:flex;position:relative;overflow:hidden}._8ynAeW_panel:focus{outline:none}._8ynAeW_nav{box-sizing:border-box;flex-direction:column;flex:none;gap:18px;width:188px;padding:22px 12px 0;display:flex}._8ynAeW_navTitle{color:var(--dsw-alias-label-primary);padding:0 12px;font-size:16px;font-weight:500;line-height:24px}._8ynAeW_navList{flex-direction:column;gap:4px;display:flex;overflow-y:auto}._8ynAeW_navCell{box-sizing:border-box;border-radius:var(--dsw-radius-md);cursor:pointer;height:40px;color:var(--dsw-alias-label-primary);text-align:left;background:0 0;border:none;align-items:center;gap:8px;padding:9px 16px 9px 12px;font-family:inherit;font-size:14px;font-weight:400;line-height:22px;display:flex}._8ynAeW_navCell:hover{background:var(--dsw-specific-sidebar-nav-item-hover)}._8ynAeW_navCell._8ynAeW_active{background:var(--dsw-specific-sidebar-nav-item-active)}._8ynAeW_navIcon{flex:none}._8ynAeW_navLabel{white-space:nowrap;text-overflow:ellipsis;flex:1;min-width:0;overflow:hidden}._8ynAeW_content{flex-direction:column;flex:1;min-width:0;display:flex}._8ynAeW_header{box-sizing:border-box;flex:none;justify-content:space-between;align-items:flex-start;gap:8px;height:54px;padding:20px 14px 8px 10px;display:flex}._8ynAeW_actions{justify-content:flex-end;align-items:center;gap:8px;min-width:0;margin-left:auto;display:flex}._8ynAeW_close{border-radius:var(--dsw-radius-sm);cursor:pointer;width:28px;height:28px;color:var(--dsw-alias-label-primary);background:0 0;border:none;justify-content:center;align-items:center;padding:0;display:inline-flex}._8ynAeW_close:hover{background:var(--dsw-alias-interactive-bg-hover)}._8ynAeW_options{flex:1;min-height:0;padding:0 24px 24px;overflow-y:auto}._8ynAeW_hiddenLabel{clip:rect(0 0 0 0);white-space:nowrap;width:1px;height:1px;position:absolute;overflow:hidden}";
		const tagId$6 = "@deepseek-ai/dsh-client-ui-settings-general/SettingsRoot.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$6) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-settings-general";
			tag.dataset.pluginCss = tagId$6;
			tag.textContent = css$6;
			document.head.appendChild(tag);
		}
		var SettingsRoot_module_css_default = {
			"actions": "_8ynAeW_actions",
			"active": "_8ynAeW_active",
			"close": "_8ynAeW_close",
			"content": "_8ynAeW_content",
			"header": "_8ynAeW_header",
			"hiddenLabel": "_8ynAeW_hiddenLabel",
			"mask": "_8ynAeW_mask",
			"nav": "_8ynAeW_nav",
			"navCell": "_8ynAeW_navCell",
			"navIcon": "_8ynAeW_navIcon",
			"navLabel": "_8ynAeW_navLabel",
			"navList": "_8ynAeW_navList",
			"navTitle": "_8ynAeW_navTitle",
			"options": "_8ynAeW_options",
			"overlay": "_8ynAeW_overlay",
			"panel": "_8ynAeW_panel",
			"rail": "_8ynAeW_rail",
			"railRow": "_8ynAeW_railRow",
			"trigger": "_8ynAeW_trigger",
			"triggerLabel": "_8ynAeW_triggerLabel",
			"triggerRow": "_8ynAeW_triggerRow"
		};
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-settings-general/src/client/DesktopUpdateIndicator.module.css.mjs
		const css$5 = ".FhZ1iG_indicator{box-sizing:border-box;border:1px solid color-mix(in srgb, var(--dsw-alias-brand-primary-new-colorprimary-new-color) 20%, transparent);border-radius:var(--dsw-radius-sm);height:28px;color:var(--dsw-alias-brand-primary-new-colorprimary-new-color);background:color-mix(in srgb, var(--dsw-alias-brand-primary-new-colorprimary-new-color) 8%, transparent);text-align:left;white-space:nowrap;cursor:pointer;flex:none;grid-template-columns:14px max-content;align-items:center;column-gap:4px;padding:0 8px;font-family:inherit;font-size:12px;font-weight:500;line-height:18px;display:inline-grid}.FhZ1iG_icon{place-items:center;width:14px;height:14px;display:grid}.FhZ1iG_badge{inset-inline-end:2px;corner-shape:round;background:var(--dsw-alias-brand-primary-new-colorprimary-new-color);border-radius:50%;width:6px;height:6px;position:absolute;top:2px}.FhZ1iG_errorDot{corner-shape:round;background:currentColor;border-radius:50%;width:6px;height:6px}.FhZ1iG_spinner{animation:1s linear infinite FhZ1iG_spin}@keyframes FhZ1iG_spin{to{transform:rotate(360deg)}}@media (prefers-reduced-motion:reduce){.FhZ1iG_spinner{animation:none}}.FhZ1iG_indicator[aria-disabled=true]{cursor:default}";
		const tagId$5 = "@deepseek-ai/dsh-client-ui-settings-general/DesktopUpdateIndicator.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$5) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-settings-general";
			tag.dataset.pluginCss = tagId$5;
			tag.textContent = css$5;
			document.head.appendChild(tag);
		}
		var DesktopUpdateIndicator_module_css_default = {
			"badge": "FhZ1iG_badge",
			"errorDot": "FhZ1iG_errorDot",
			"icon": "FhZ1iG_icon",
			"indicator": "FhZ1iG_indicator",
			"spin": "FhZ1iG_spin",
			"spinner": "FhZ1iG_spinner"
		};
		//#endregion
		//#region src/client/DesktopUpdateIndicator.tsx
		/** Optional Electron status presentation; the native shell owns actions and Web owns visible copy. */
		const BUSY_PHASES = new Set([
			"checking",
			"downloading",
			"verifying",
			"installing"
		]);
		function updateCopy(state, t) {
			const label = {
				idle: "",
				checking: t("desktop.update.checking"),
				available: t("desktop.update.available"),
				downloading: t("desktop.update.progress", { percent: state.percent ?? 0 }),
				verifying: t("desktop.update.verifying"),
				installing: t("desktop.update.installing"),
				ready: t("desktop.update.ready"),
				error: t("desktop.update.retry")
			}[state.phase];
			if (state.phase === "error") return {
				label,
				detail: {
					check: t("desktop.update.checkFailed"),
					"check-network": t("desktop.update.checkNetworkFailed"),
					download: t("desktop.update.downloadFailed"),
					"download-network": t("desktop.update.downloadNetworkFailed"),
					install: t("desktop.update.installFailed"),
					"install-network": t("desktop.update.installNetworkFailed"),
					"stop-failed": t("desktop.update.stopFailed"),
					"tasks-changed": t("desktop.update.tasksChanged"),
					"tasks-unavailable": t("desktop.update.tasksUnavailable")
				}[state.failure ?? "install"]
			};
			if (state.phase === "downloading" && state.version !== void 0) return {
				label,
				detail: t("desktop.update.downloadDetail", {
					percent: state.percent ?? 0,
					version: state.version
				})
			};
			return {
				label,
				detail: state.version === void 0 ? label : t("desktop.update.versionDetail", {
					label,
					version: state.version
				})
			};
		}
		/**
		* Render update status with connection-indicator geometry and brand-blue labels, including retries.
		* @param props - Connection priority, sidebar width, and localized bridge-failure copy.
		* @returns Desktop-only status beside the account button, or nothing in browsers.
		*/
		function DesktopUpdateIndicator({ wide, hidden, t, view, onOpen }) {
			const { presentation: state, failed, opening } = view;
			if (!wide || hidden || !failed && (state === void 0 || state.phase === "idle")) return null;
			const retryLabel = t("desktop.update.retry");
			const copy = state === void 0 ? {
				label: retryLabel,
				detail: retryLabel
			} : updateCopy(state, t);
			const label = failed ? retryLabel : copy.label;
			const error = failed || state?.phase === "error";
			const busy = opening || state !== void 0 && BUSY_PHASES.has(state.phase);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
				label: failed ? retryLabel : copy.detail,
				side: "top",
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
					type: "button",
					className: DesktopUpdateIndicator_module_css_default.indicator,
					"aria-label": label,
					"aria-disabled": busy,
					onClick: () => {
						if (!busy) onOpen();
					},
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: DesktopUpdateIndicator_module_css_default.icon,
						"aria-hidden": "true",
						children: error ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { className: DesktopUpdateIndicator_module_css_default.errorDot }) : busy ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconLoadingOutlineRegular, {
							className: DesktopUpdateIndicator_module_css_default.spinner,
							size: 14
						}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconDownloadOutlineRegular, { size: 14 })
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: label })]
				})
			});
		}
		/**
		* @param props - Framework-bound carrier and connection state.
		* @returns A non-interactive brand-blue notification on the sidebar expand button.
		*/
		function DesktopUpdateBadge({ useDesktopUpdate, useConnectionState, t }) {
			const { presentation: state, failed } = useDesktopUpdate((value) => value);
			const connection = useConnectionState((value) => value);
			if ((connection === "disconnected" || connection === "connecting") && state?.phase !== "installing" || !failed && (state === void 0 || state.phase === "idle")) return null;
			const retryLabel = t("desktop.update.retry");
			const copy = state === void 0 ? {
				label: retryLabel,
				detail: retryLabel
			} : updateCopy(state, t);
			const label = failed ? retryLabel : copy.label;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
				label: failed ? label : copy.detail,
				side: "right",
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
					role: "img",
					"aria-label": label,
					className: DesktopUpdateIndicator_module_css_default.badge
				})
			});
		}
		//#endregion
		//#region src/client/SettingsRoot.tsx
		/**
		* Settings shell root: the sidebar-foot trigger row plus the centered modal
		* panel (figma 2552:26025, 760x500) with the section nav rail. The shell is
		* a pure composition face — slot-owned text (trigger label, panel title,
		* close label, sections) arrives from registrants through slots; accessible
		* names resolve from localized content (trigger: shell locale; dialog:
		* aria-labelledby the title node; close: visually-hidden slot text). Modal
		* open state and the active section id belong to the declared owner store;
		* the onboarding coordinator mounts exactly one ordered registrant while the
		* sessions-derived empty-Hero fact is active. Visible dialog chrome belongs
		* to the step, so a mounted-but-deciding step paints nothing here.
		*/
		const RECOVERY_CONFIRMATION_MS = 2e3;
		/** Minimum visible time for the connecting pill; shorter attempts read as flicker. */
		const CONNECTING_MIN_VISIBLE_MS = 800;
		/** Nav glyph by section id; unknown ids fall back to the settings gear. */
		function navIcon(id) {
			if (id === "account") return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconUserOutlineMedium, {
				className: SettingsRoot_module_css_default.navIcon,
				size: 16
			});
			if (id === "models") return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconDataOutlineMedium, {
				className: SettingsRoot_module_css_default.navIcon,
				size: 16
			});
			if (id === "agent-presets") return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconAgentPresetOutlineMedium, {
				className: SettingsRoot_module_css_default.navIcon,
				size: 16
			});
			if (id === "plugins") return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconPersonalizationOutlineMedium, {
				className: SettingsRoot_module_css_default.navIcon,
				size: 16
			});
			if (id === "archived-sessions") return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconArchiveOutlineMedium, {
				className: SettingsRoot_module_css_default.navIcon,
				size: 16
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconSettingsOutlineMedium, {
				className: SettingsRoot_module_css_default.navIcon,
				size: 16
			});
		}
		/**
		* Body-portaled modal layer: full-viewport mask + centered panel. Close paths: the
		* header button, a mask click, and document-level Escape (mounted only while
		* open, so the listener lifetime is the panel's).
		*/
		function SettingsPanel({ rows, renderSlot, activeId, onSelect, onClose }) {
			const active = rows.find((r) => r.id === activeId)?.id ?? rows[0]?.id;
			const titleId = (0, react.useId)();
			const panel = (0, react.useRef)(null);
			(0, _deepseek_ai_dsh_client_ui_primitives.useModalLayer)(panel, true, onClose);
			return (0, react_dom.createPortal)(/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: SettingsRoot_module_css_default.overlay,
				role: "presentation",
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
					className: SettingsRoot_module_css_default.mask,
					"aria-hidden": "true",
					onClick: onClose
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					ref: panel,
					tabIndex: -1,
					"data-shortcut-modal": "settings",
					className: SettingsRoot_module_css_default.panel,
					role: "dialog",
					"aria-modal": "true",
					"aria-labelledby": titleId,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("nav", {
						className: SettingsRoot_module_css_default.nav,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: SettingsRoot_module_css_default.navTitle,
							id: titleId,
							tabIndex: -1,
							"data-modal-autofocus": active === void 0 ? "" : void 0,
							children: renderSlot("settings.header", {})
						}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: SettingsRoot_module_css_default.navList,
							children: rows.map((row) => /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
								type: "button",
								className: clsx(SettingsRoot_module_css_default.navCell, row.id === active && SettingsRoot_module_css_default.active),
								"aria-current": row.id === active ? "true" : void 0,
								"data-modal-autofocus": row.id === active ? "" : void 0,
								onClick: () => {
									onSelect(row.id);
								},
								children: [navIcon(row.id), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
									className: SettingsRoot_module_css_default.navLabel,
									children: row.label
								})]
							}, row.id))
						})]
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: SettingsRoot_module_css_default.content,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							className: SettingsRoot_module_css_default.header,
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
								className: SettingsRoot_module_css_default.actions,
								children: renderSlot("settings.action", {})
							}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
								type: "button",
								className: SettingsRoot_module_css_default.close,
								onClick: onClose,
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconCloseOutlineRegular, { size: 14 }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
									className: SettingsRoot_module_css_default.hiddenLabel,
									children: renderSlot("settings.close", {})
								})]
							})]
						}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: SettingsRoot_module_css_default.options,
							children: active !== void 0 && renderSlot("settings.section", { close: onClose }, { only: active })
						})]
					})]
				})]
			}), document.body);
		}
		/**
		* Render the settings trigger and panel.
		* @param props - composed slot props (contract/slots.ts).
		* @returns the settings shell element tree.
		*/
		function SettingsRoot(props) {
			const { wide, reconnect, useConnectionState, useSections, useOnboardingSteps, useSessions, renderSlot, t, useDesktopUpdate, openDesktopUpdate, useStore, actions, useShortcuts } = props;
			const { open, activeId } = useStore((state) => state);
			const shortcut = useShortcuts((rows) => rows.find((row) => row.id === "settings.open"));
			const { close, openSection } = actions;
			const [requestedOnboarding, setRequestedOnboarding] = (0, react.useState)();
			const [completedOnboarding, setCompletedOnboarding] = (0, react.useState)(() => /* @__PURE__ */ new Set());
			const [showRecovery, setShowRecovery] = (0, react.useState)(false);
			const [holdConnecting, setHoldConnecting] = (0, react.useState)(false);
			const connectingShownAt = (0, react.useRef)(void 0);
			const rows = useSections((s) => s);
			const desktopUpdate = useDesktopUpdate((state) => state);
			const connectionState = useConnectionState((state) => state);
			const previousConnectionState = (0, react.useRef)(connectionState);
			const onboardingSteps = useOnboardingSteps((s) => s);
			const onboardingActive = useSessions((state) => {
				const main = Object.values(state.byId).find((session) => (session.retainedBy.mainView ?? 0) > 0);
				return state.phase === "ready" && (main === void 0 || main.blank);
			});
			const onboardingStep = requestedOnboarding !== void 0 ? onboardingSteps.find((step) => step.id === requestedOnboarding) : onboardingActive ? onboardingSteps.find((step) => !completedOnboarding.has(step.id)) : void 0;
			(0, react.useEffect)(() => {
				if (onboardingActive) return;
				setCompletedOnboarding(/* @__PURE__ */ new Set());
			}, [onboardingActive]);
			const onboardingStepSeen = (0, react.useRef)(onboardingStep);
			(0, react.useEffect)(() => {
				const appeared = onboardingStepSeen.current === void 0 && onboardingStep !== void 0;
				onboardingStepSeen.current = onboardingStep;
				if (appeared && open) close();
			}, [
				onboardingStep,
				open,
				close
			]);
			(0, react.useLayoutEffect)(() => {
				const previous = previousConnectionState.current;
				previousConnectionState.current = connectionState;
				if (connectionState !== "connected") {
					setShowRecovery(false);
					return;
				}
				if (previous !== "disconnected" && previous !== "connecting") return;
				setShowRecovery(true);
			}, [connectionState]);
			(0, react.useLayoutEffect)(() => {
				if (!showRecovery || holdConnecting) return;
				const timeout = window.setTimeout(() => {
					setShowRecovery(false);
				}, RECOVERY_CONFIRMATION_MS);
				return () => {
					window.clearTimeout(timeout);
				};
			}, [showRecovery, holdConnecting]);
			(0, react.useLayoutEffect)(() => {
				if (connectionState === "connecting") {
					connectingShownAt.current = Date.now();
					return;
				}
				const shownAt = connectingShownAt.current;
				if (shownAt === void 0) return;
				connectingShownAt.current = void 0;
				const remaining = CONNECTING_MIN_VISIBLE_MS - (Date.now() - shownAt);
				if (remaining <= 0) return;
				setHoldConnecting(true);
				const timeout = window.setTimeout(() => {
					setHoldConnecting(false);
				}, remaining);
				return () => {
					window.clearTimeout(timeout);
					setHoldConnecting(false);
				};
			}, [connectionState]);
			const completeOnboardingStep = (0, react.useCallback)((id) => {
				setRequestedOnboarding(void 0);
				setCompletedOnboarding((previous) => {
					if (previous.has(id)) return previous;
					return new Set([...previous, id]);
				});
			}, []);
			let connectionIndicator;
			if (connectionState === "connecting" || holdConnecting) connectionIndicator = "connecting";
			else if (connectionState === "disconnected") connectionIndicator = "disconnected";
			else if (showRecovery) connectionIndicator = "recovered";
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [
				/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: clsx(SettingsRoot_module_css_default.triggerRow, !wide && SettingsRoot_module_css_default.railRow),
					children: [
						renderSlot("settings.launcher", {
							wide,
							settingsOpen: open,
							openSettings: actions.open,
							...shortcut?.keys.length ? { settingsShortcut: {
								keys: shortcut.keys,
								aria: shortcut.aria
							} } : {},
							openOnboarding: (id) => {
								close();
								setRequestedOnboarding(id);
							}
						}, { fallback: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
							disabled: open,
							label: t("trigger"),
							shortcutKeys: shortcut?.keys,
							children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
								type: "button",
								className: clsx(SettingsRoot_module_css_default.trigger, !wide && SettingsRoot_module_css_default.rail),
								"aria-label": t("trigger"),
								"aria-keyshortcuts": shortcut?.aria,
								"aria-haspopup": "dialog",
								"aria-expanded": open,
								onClick: () => {
									actions.open();
								},
								children: renderSlot("settings.trigger", { wide })
							})
						}) }),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.ConnectionIndicator, {
							state: wide && desktopUpdate.presentation?.phase !== "installing" ? connectionIndicator : void 0,
							disconnectedLabel: t("connection.error"),
							connectingLabel: t("connection.connecting"),
							recoveredLabel: t("connection.connected"),
							reconnectActionLabel: t("connection.reconnect"),
							restartActionLabel: t("connection.restart"),
							onReconnect: reconnect
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)(DesktopUpdateIndicator, {
							wide,
							hidden: connectionIndicator !== void 0 && desktopUpdate.presentation?.phase !== "installing",
							t,
							view: desktopUpdate,
							onOpen: openDesktopUpdate
						})
					]
				}),
				open && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(SettingsPanel, {
					rows,
					renderSlot,
					activeId,
					onSelect: actions.select,
					onClose: close
				}),
				onboardingStep !== void 0 && renderSlot("settings.onboarding", {
					stepId: onboardingStep.id,
					explicit: requestedOnboarding !== void 0,
					complete: () => {
						completeOnboardingStep(onboardingStep.id);
					},
					openSection
				}, { only: onboardingStep.id })
			] });
		}
		//#endregion
		//#region src/client/desktop-update-source.ts
		/** Client-owned observation of the optional Desktop preload. */
		/** Owns one preload subscription across both sidebar locations. */
		var DesktopUpdateSource = class {
			bridge;
			/** Framework-observed carrier status shared by both sidebar controls. */
			store = (0, _deepseek_ai_dsh_client_store.createSnapshotStore)({
				failed: false,
				opening: false
			});
			live = true;
			received = false;
			unsubscribe;
			/** @param bridge - Optional isolated Electron API, absent in ordinary browsers. */
			constructor(bridge) {
				this.bridge = bridge;
				this.unsubscribe = bridge?.subscribe((presentation) => {
					if (!this.live) return;
					this.received = true;
					this.store.set({
						...this.store.getSnapshot(),
						presentation,
						failed: false
					});
				});
				bridge?.status().then((presentation) => {
					if (this.live && !this.received) this.store.set({
						...this.store.getSnapshot(),
						presentation
					});
				}, () => {
					if (this.live && !this.received) this.store.set({
						...this.store.getSnapshot(),
						failed: true
					});
				});
			}
			/** Invoke one user action; subsequent clicks join the shell-owned operation. */
			open() {
				if (!this.live || this.bridge === void 0) return;
				const state = this.store.getSnapshot();
				if (state.opening || state.presentation !== void 0 && [
					"checking",
					"downloading",
					"verifying",
					"installing"
				].includes(state.presentation.phase)) return;
				this.store.set({
					...state,
					opening: true
				});
				this.bridge.open().catch(() => {
					if (this.live) this.store.set({
						...this.store.getSnapshot(),
						failed: true
					});
				}).finally(() => {
					if (this.live) this.store.set({
						...this.store.getSnapshot(),
						opening: false
					});
				});
			}
			/** Detach the carrier and ignore any pending status or action completion. */
			dispose() {
				this.live = false;
				this.unsubscribe?.();
			}
		};
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-settings-general/src/client/chrome.module.css.mjs
		const css$4 = ".Tb0npW_triggerLabel{white-space:nowrap;overflow:hidden}";
		const tagId$4 = "@deepseek-ai/dsh-client-ui-settings-general/chrome.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$4) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-settings-general";
			tag.dataset.pluginCss = tagId$4;
			tag.textContent = css$4;
			document.head.appendChild(tag);
		}
		var chrome_module_css_default = { "triggerLabel": "Tb0npW_triggerLabel" };
		//#endregion
		//#region src/client/chrome.tsx
		/**
		* Shell chrome content registered into the shell's trigger/header seats: the
		* trigger row icon + label (figma sidebar foot) and the panel title text.
		* The shell renders the surrounding chrome (button, nav heading row) and
		* reads each entry's `label` option for aria text.
		*/
		/**
		* Render the trigger row content (icon; label only in the wide column).
		* @param props - composed slot props.
		* @returns the trigger content fragment.
		*/
		function TriggerContent({ wide, t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconSettingsOutlineMedium, { size: wide ? 16 : 18 }), wide && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
				className: chrome_module_css_default.triggerLabel,
				children: t("trigger")
			})] });
		}
		/**
		* Render the panel title text.
		* @param props - composed slot props.
		* @returns the title text node.
		*/
		function HeaderContent({ t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(react_jsx_runtime.Fragment, { children: t("title") });
		}
		/**
		* Render the close button's visually-hidden label text.
		* @param props - composed slot props.
		* @returns the label text node.
		*/
		function CloseLabel({ t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(react_jsx_runtime.Fragment, { children: t("close") });
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-settings-general/src/client/GeneralSection.module.css.mjs
		const css$3 = "._5MhJLq_section{flex-direction:column;width:100%;display:flex}._5MhJLq_section>[data-slot=\"settings.general.item\"]>:last-child{border-bottom:none}";
		const tagId$3 = "@deepseek-ai/dsh-client-ui-settings-general/GeneralSection.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$3) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-settings-general";
			tag.dataset.pluginCss = tagId$3;
			tag.textContent = css$3;
			document.head.appendChild(tag);
		}
		var GeneralSection_module_css_default = { "section": "_5MhJLq_section" };
		//#endregion
		//#region src/client/GeneralSection.tsx
		/**
		* Render the General section content column.
		* @param props - composed slot props (contract/slots.ts).
		* @returns the section element tree.
		*/
		function GeneralSection({ renderSlot }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: GeneralSection_module_css_default.section,
				children: renderSlot("settings.general.item", {})
			});
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-settings-general/src/client/CurrentVersionRow.module.css.mjs
		const css$2 = ".SOheja_row{color:var(--dsw-alias-label-primary);overflow-wrap:anywhere;padding:16px 0;font-size:14px;line-height:22px}";
		const tagId$2 = "@deepseek-ai/dsh-client-ui-settings-general/CurrentVersionRow.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$2) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-settings-general";
			tag.dataset.pluginCss = tagId$2;
			tag.textContent = css$2;
			document.head.appendChild(tag);
		}
		var CurrentVersionRow_module_css_default = { "row": "SOheja_row" };
		//#endregion
		//#region src/client/CurrentVersionRow.tsx
		/**
		* Render the version embedded by the client build; partial builds without metadata omit the row.
		* @param props - runtime share and localized copy.
		* @returns the current release label, or nothing when build metadata is absent.
		*/
		function CurrentVersionRow({ t }) {
			const version = {}.DSH_CLIENT_VERSION;
			if (version === void 0) return null;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: CurrentVersionRow_module_css_default.row,
				children: t("general.currentVersion", { version })
			});
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-settings-general/src/client/DeveloperToolsRow.module.css.mjs
		const css$1 = ".fimqLW_row{border-bottom:.5px solid var(--dsw-alias-border-l2);justify-content:space-between;align-items:center;gap:24px;padding:16px 0;display:flex}.fimqLW_title{font-size:14px;line-height:20px}.fimqLW_description{color:var(--dsw-alias-label-secondary);margin-top:4px;font-size:12px;line-height:18px}";
		const tagId$1 = "@deepseek-ai/dsh-client-ui-settings-general/DeveloperToolsRow.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$1) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-settings-general";
			tag.dataset.pluginCss = tagId$1;
			tag.textContent = css$1;
			document.head.appendChild(tag);
		}
		var DeveloperToolsRow_module_css_default = {
			"description": "fimqLW_description",
			"row": "fimqLW_row",
			"title": "fimqLW_title"
		};
		//#endregion
		//#region src/client/DeveloperToolsRow.tsx
		/** General Settings control for shared developer-tool visibility and previews. */
		/**
		* Render the developer-tool toggle.
		* @param props - accepted preference, writer and localized copy.
		* @returns the General Settings row.
		*/
		function DeveloperToolsRow({ useDeveloperTools, setEnabled, t }) {
			const enabled = useDeveloperTools((value) => value);
			const [busy, setBusy] = (0, react.useState)(false);
			const [failed, setFailed] = (0, react.useState)(false);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: DeveloperToolsRow_module_css_default.row,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						className: DeveloperToolsRow_module_css_default.title,
						children: t("developerTools.title")
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						className: DeveloperToolsRow_module_css_default.description,
						children: t("developerTools.description")
					}),
					failed && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						role: "alert",
						children: t("developerTools.error")
					})
				] }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Switch, {
					checked: enabled,
					disabled: busy,
					label: t("developerTools.title"),
					onChange: (next) => {
						setFailed(false);
						setBusy(true);
						setEnabled(next).catch(() => {
							setFailed(true);
						}).finally(() => {
							setBusy(false);
						});
					}
				})]
			});
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-settings-general/src/client/SettingsDocumentAction.module.css.mjs
		const css = ".N6maHq_action{align-items:center;gap:8px;min-width:0;display:flex}.N6maHq_error{max-width:180px;color:var(--dsw-alias-state-error-primary);text-overflow:ellipsis;white-space:nowrap;font-size:12px;line-height:18px;overflow:hidden}";
		const tagId = "@deepseek-ai/dsh-client-ui-settings-general/SettingsDocumentAction.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-settings-general";
			tag.dataset.pluginCss = tagId;
			tag.textContent = css;
			document.head.appendChild(tag);
		}
		var SettingsDocumentAction_module_css_default = {
			"action": "N6maHq_action",
			"error": "N6maHq_error"
		};
		//#endregion
		//#region src/client/SettingsDocumentAction.tsx
		/** Optional settings-header action for opening a file-backed Host document. */
		/**
		* Render the open-document action only after Host metadata confirms document availability.
		* @param props - header owner props, localized copy, and injected document state.
		* @returns the action, or null while unavailable or unresolved.
		*/
		function SettingsDocumentAction({ controller, useSnapshot, t }) {
			const state = useSnapshot((snapshot) => snapshot);
			(0, react.useEffect)(() => {
				controller.load();
			}, [controller]);
			if (state.status !== "ready") return null;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: SettingsDocumentAction_module_css_default.action,
				children: [state.error === null ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
					className: SettingsDocumentAction_module_css_default.error,
					role: "alert",
					children: t("openDocument.error")
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
					variant: "outline",
					size: "sm",
					disabled: state.opening,
					onClick: () => {
						controller.open();
					},
					children: t("openDocument")
				})]
			});
		}
		//#endregion
		//#region src/client/settings-document-store.ts
		/** Derives local-document availability from the shared mirror and invokes the pathless Host-owned open operation. */
		var SettingsDocumentStore = class {
			ctx;
			describeFace;
			/** uSES-safe state source shared by the registered header action. */
			store = (0, _deepseek_ai_dsh_client_store.createSnapshotStore)({
				status: "idle",
				opening: false,
				error: null
			});
			following;
			/**
			* @param ctx - the plugin's context, whose loopback `remote.settings`
			* namespace opens the provider document.
			* @param describeFace - the shared mirror's describe face (`hasDocument` source).
			*/
			constructor(ctx, describeFace) {
				this.ctx = ctx;
				this.describeFace = describeFace;
			}
			/**
			* Begin following the mirror (idempotent) and reflect whether the current
			* provider owns a local document.
			* @returns settlement once the snapshot reflects the mirror.
			*/
			async load() {
				this.following ??= this.describeFace.subscribe(() => {
					this.derive();
				});
				this.store.update((state) => {
					state.status = "loading";
					state.error = null;
				});
				await this.describeFace.ensure();
				this.derive();
			}
			/**
			* Open the loaded document once; concurrent gestures collapse behind the in-flight action.
			* @returns after the native-open request settles, or immediately when unavailable/already opening.
			*/
			async open() {
				const current = this.store.getSnapshot();
				if (current.status !== "ready" || current.opening) return;
				this.store.update((state) => {
					state.opening = true;
					state.error = null;
				});
				try {
					const result = await this.ctx.remote.settings.openSettingsDocument();
					if (!result.ok) {
						const { message } = result.error;
						this.store.update((state) => {
							state.error = message;
						});
					}
				} finally {
					this.store.update((state) => {
						state.opening = false;
					});
				}
			}
			/** Stop following the mirror. */
			dispose() {
				this.following?.();
				this.following = void 0;
			}
			derive() {
				const mirrored = this.describeFace.getSnapshot();
				if (mirrored.view === void 0) {
					if (mirrored.error !== null) this.store.update((state) => {
						state.status = "unavailable";
						state.error = mirrored.error;
					});
					return;
				}
				const { hasDocument } = mirrored.view;
				this.store.update((state) => {
					state.status = hasDocument ? "ready" : "unavailable";
					state.error = null;
				});
			}
		};
		//#endregion
		//#region src/client/locales.ts
		/** Shell chrome and General-nav dictionaries; feature rows own their copy. */
		/** Simplified Chinese dictionary (the key-set source of truth). */
		const zh = {
			"trigger": "设置",
			"shortcut.open": "打开设置",
			"desktop.update.available": "新版本",
			"desktop.update.checking": "正在检查更新…",
			"desktop.update.progress": "{percent}%",
			"desktop.update.verifying": "正在校验更新文件…",
			"desktop.update.installing": "正在准备重启…",
			"desktop.update.ready": "安装并重启",
			"desktop.update.retry": "重试更新",
			"desktop.update.versionDetail": "{label} — V{version}",
			"desktop.update.downloadDetail": "正在下载更新：{percent}%\n目标版本：V{version}",
			"desktop.update.checkFailed": "检查更新失败，请稍后重试。",
			"desktop.update.downloadFailed": "下载更新失败，请重试。",
			"desktop.update.installFailed": "安装更新失败，请稍后重试。",
			"desktop.update.checkNetworkFailed": "检查更新失败，请稍后重试。网络连接异常，请检查网络后重试。",
			"desktop.update.downloadNetworkFailed": "下载更新失败，请重试。网络连接异常，请检查网络后重试。",
			"desktop.update.installNetworkFailed": "安装更新失败，请稍后重试。网络连接异常，请检查网络后重试。",
			"desktop.update.stopFailed": "未能安全停止任务，更新未安装。请稍后重试。",
			"desktop.update.tasksChanged": "有新任务开始，请重新确认更新。",
			"desktop.update.tasksUnavailable": "无法确认任务状态，请在工作区就绪后重试更新。",
			"title": "设置",
			"close": "关闭",
			"openDocument": "打开配置文件",
			"openDocument.error": "无法打开配置文件",
			"general.nav": "通用设置",
			"general.currentVersion": "当前版本：{version}",
			"developerTools.title": "代码工作工具",
			"developerTools.error": "保存失败，请重试",
			"developerTools.description": "开启后显示轨迹、本轮代码差异，新对话中的 Agent 预设切换",
			"connection.error": "连接异常，刷新重试",
			"connection.connecting": "重新连接中",
			"connection.connected": "连接成功",
			"connection.reconnect": "连接异常，点击立即重连",
			"connection.restart": "连接中断，正在重试，点击立即重连"
		};
		/** English dictionary, checked complete against the zh key set. */
		const en = {
			"trigger": "Settings",
			"shortcut.open": "Open settings",
			"desktop.update.available": "Update",
			"desktop.update.checking": "Checking for updates…",
			"desktop.update.progress": "{percent}%",
			"desktop.update.verifying": "Verifying update files…",
			"desktop.update.installing": "Preparing to restart…",
			"desktop.update.ready": "Install and Restart",
			"desktop.update.retry": "Retry update",
			"desktop.update.versionDetail": "{label} — V{version}",
			"desktop.update.downloadDetail": "Downloading update: {percent}%\nTarget version: V{version}",
			"desktop.update.checkFailed": "Could not check for updates. Please try again later.",
			"desktop.update.downloadFailed": "Could not download the update. Please try again.",
			"desktop.update.installFailed": "Could not install the update. Please try again later.",
			"desktop.update.checkNetworkFailed": "Could not check for updates. Please try again later. The connection was interrupted. Check your network and try again.",
			"desktop.update.downloadNetworkFailed": "Could not download the update. Please try again. The connection was interrupted. Check your network and try again.",
			"desktop.update.installNetworkFailed": "Could not install the update. Please try again later. The connection was interrupted. Check your network and try again.",
			"desktop.update.stopFailed": "Tasks could not be stopped safely. The update was not installed. Please try again later.",
			"desktop.update.tasksChanged": "New tasks started. Review the update confirmation again.",
			"desktop.update.tasksUnavailable": "Task status is unavailable. Try updating again when the workspace is ready.",
			"title": "Settings",
			"close": "Close",
			"openDocument": "Open configuration file",
			"openDocument.error": "Could not open configuration file",
			"general.nav": "General",
			"general.currentVersion": "Current version: {version}",
			"developerTools.title": "Coding Tools",
			"developerTools.error": "Could not save. Please try again.",
			"developerTools.description": "Shows trajectory, code diffs, and Agent preset switching in new chats",
			"connection.error": "Disconnected",
			"connection.connecting": "Reconnecting",
			"connection.connected": "Connected",
			"connection.reconnect": "Disconnected, reconnect now",
			"connection.restart": "Reconnecting, reconnect now"
		};
		//#endregion
		//#region src/client/index.ts
		/** Dictionary namespace owned by this plugin (shell chrome + General copy). */
		const NS = "settings";
		/**
		* Required services (cordis fiber inject). The target slots are declared by
		* ui-settings' apply, whose activation order relative to this one is NOT
		* constrained; registrations depend on their slots through `slots.inject()`.
		*/
		const inject = [
			"slots",
			"locale",
			"connection",
			"remote",
			"remote.settings",
			"configForms",
			"shortcuts"
		];
		/**
		* Register the `settings` dictionaries, the chrome content, and the General
		* section, each once its slot declaration is on the ledger.
		* @param ctx - client root context.
		*/
		function apply(ctx) {
			ctx.slots.inject("settings.general.item", () => ctx.slots.register({
				name: "settings.general.item",
				id: "developer-tools",
				order: 15,
				locale: NS,
				inject: () => ({
					hooks: { developerTools: ctx.configForms.developerTools.enabled },
					setEnabled: (enabled) => ctx.configForms.developerTools.setEnabled(enabled)
				})
			}, DeveloperToolsRow));
			ctx.slots.inject("settings.general.item", () => ctx.slots.register({
				name: "settings.general.item",
				id: "current-version",
				order: 100,
				locale: NS
			}, CurrentVersionRow));
			ctx.effect(() => ctx.locale.register(NS, {
				zh,
				en
			}), "ui-settings-general: dictionaries");
			const connection = ctx.get("connection");
			const carrier = globalThis.dshDesktop;
			const desktopUpdate = new DesktopUpdateSource(carrier?.protocolVersion === 1 ? carrier.updates : void 0);
			ctx.effect(() => () => {
				desktopUpdate.dispose();
			}, "ui-settings-general: desktop update carrier");
			ctx.slots.inject("sidebar.toggle.badge", () => ctx.slots.register({
				name: "sidebar.toggle.badge",
				locale: NS,
				inject: () => ({ hooks: {
					desktopUpdate: desktopUpdate.store,
					connectionState: connection.state
				} })
			}, DesktopUpdateBadge));
			const t = ctx.locale.bind(NS);
			const documentController = ctx.remote.$host.isLoopback ? new SettingsDocumentStore(ctx, ctx.configForms.describe()) : void 0;
			const documentInjected = documentController === void 0 ? void 0 : () => ({
				controller: documentController,
				hooks: { snapshot: documentController.store }
			});
			ctx.effect(() => () => {
				documentController?.dispose();
			}, "ui-settings-general: document action directory");
			let rowsVersion = -1;
			let rowsRevision = -1;
			let rows = [];
			let onboardingVersion = -1;
			let onboardingSteps = [];
			const shellInjected = () => ({
				openDesktopUpdate: () => {
					desktopUpdate.open();
				},
				reconnect: () => {
					connection.reconnect();
				},
				hooks: {
					shortcuts: ctx.shortcuts.catalog,
					desktopUpdate: desktopUpdate.store,
					connectionState: connection.state,
					sections: {
						getSnapshot: () => {
							const version = ctx.slots.getVersion("settings.section");
							const revision = ctx.locale.getSnapshot().revision;
							if (version !== rowsVersion || revision !== rowsRevision) {
								rowsVersion = version;
								rowsRevision = revision;
								rows = ctx.slots.entries("settings.section").map((e) => ({
									/* v8 ignore next -- list-slot registration requires id (SlotCore rejects an entry without one) */
									id: e.options.id ?? "",
									order: e.options.order ?? 0,
									label: (0, _deepseek_ai_dsh_client_ui_slots.resolveSlotLabel)(e.options.label) ?? ""
								})).sort((a, b) => a.order - b.order);
							}
							return rows;
						},
						subscribe: (listener) => {
							const offLedger = ctx.slots.subscribe("settings.section", listener);
							const offLocale = ctx.locale.subscribe(listener);
							return () => {
								offLedger();
								offLocale();
							};
						}
					},
					onboardingSteps: {
						getSnapshot: () => {
							const version = ctx.slots.getVersion("settings.onboarding");
							if (version !== onboardingVersion) {
								onboardingVersion = version;
								onboardingSteps = ctx.slots.entries("settings.onboarding").map((e) => ({
									/* v8 ignore next -- list-slot registration requires id */
									id: e.options.id ?? "",
									order: e.options.order ?? 0
								})).sort((a, b) => a.order - b.order);
							}
							return onboardingSteps;
						},
						subscribe: (listener) => ctx.slots.subscribe("settings.onboarding", listener)
					}
				}
			});
			ctx.slots.inject("sidebar.settings", () => {
				const shellHandle = createSettingsShellStore();
				const shellInstance = shellHandle.create();
				const shellStore = {
					...shellHandle,
					create: () => shellInstance
				};
				const disposeCommand = ctx.shortcuts.register({
					id: "settings.open",
					label: () => t("shortcut.open"),
					aliases: ["settings", "preferences"],
					defaults: {
						"desktop:macos": {
							code: "Comma",
							modifiers: ["primary"]
						},
						"desktop:windows": {
							code: "Comma",
							modifiers: ["primary"]
						},
						"desktop:linux": {
							code: "Comma",
							modifiers: ["primary"]
						},
						"web:macos": {
							code: "Comma",
							modifiers: ["primary"]
						},
						"web:windows": {
							code: "Comma",
							modifiers: ["primary"]
						}
					},
					regions: [
						"page",
						"editable",
						"terminal"
					],
					modals: ["settings"],
					resolve: ({ modal }) => {
						if (modal !== null && modal !== "settings") return {
							status: "blocked",
							reason: "modal"
						};
						return {
							status: "handled",
							run: () => {
								if (modal === "settings") (0, _deepseek_ai_dsh_client_ui_primitives.closeTopModal)(document);
								else shellInstance.actions.open();
							}
						};
					}
				});
				const disposeSlot = ctx.slots.register({
					name: "sidebar.settings",
					locale: NS,
					store: shellStore,
					children: {
						"settings.launcher": {
							kind: "single",
							scope: "root"
						},
						"settings.trigger": {
							kind: "single",
							scope: "root"
						},
						"settings.header": {
							kind: "single",
							scope: "root"
						},
						"settings.action": {
							kind: "list",
							scope: "root"
						},
						"settings.close": {
							kind: "single",
							scope: "root"
						},
						"settings.section": {
							kind: "list",
							scope: "root"
						},
						"settings.onboarding": {
							kind: "list",
							scope: "root"
						}
					},
					inject: shellInjected
				}, SettingsRoot);
				return () => {
					disposeCommand();
					disposeSlot();
				};
			});
			ctx.slots.inject("settings.trigger", () => ctx.slots.register({
				name: "settings.trigger",
				locale: NS
			}, TriggerContent));
			ctx.slots.inject("settings.header", () => ctx.slots.register({
				name: "settings.header",
				locale: NS
			}, HeaderContent));
			if (documentInjected !== void 0) ctx.slots.inject("settings.action", () => ctx.slots.register({
				name: "settings.action",
				id: "open-document",
				order: 0,
				locale: NS,
				inject: documentInjected
			}, SettingsDocumentAction));
			ctx.slots.inject("settings.close", () => ctx.slots.register({
				name: "settings.close",
				locale: NS
			}, CloseLabel));
			ctx.slots.inject("settings.section", () => ctx.slots.register({
				name: "settings.section",
				id: "general",
				order: 0,
				label: () => t("general.nav"),
				locale: NS,
				children: { "settings.general.item": {
					kind: "list",
					scope: "root"
				} }
			}, GeneralSection));
		}
		//#endregion
		exports.SettingsDocumentStore = SettingsDocumentStore;
		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map