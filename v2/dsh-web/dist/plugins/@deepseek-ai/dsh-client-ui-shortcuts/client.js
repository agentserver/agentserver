window.__ModuleLoader__.load({
	id: "@deepseek-ai/dsh-client-ui-shortcuts",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		Object.defineProperty(exports, Symbol.toStringTag, { value: "Module" });
		let _deepseek_ai_dsh_client_ui_primitives = require("@deepseek-ai/dsh-client-ui-primitives");
		let _deepseek_ai_dsh_client_store = require("@deepseek-ai/dsh-client-store");
		let react = require("react");
		let react_jsx_runtime = require("react/jsx-runtime");
		//#region src/client/store.ts
		/** Shortcut reference visibility and search state, shared by its entry points. */
		/**
		* Declare the reference dialog store.
		* @returns root-scoped visibility, focus request, and search actions.
		*/
		function createShortcutsStore() {
			return (0, _deepseek_ai_dsh_client_store.defineStore)({
				init: () => ({
					open: false,
					query: "",
					focusRequest: 0
				}),
				actions: {
					open: (d) => {
						d.open = true;
						d.focusRequest++;
					},
					close: (d) => {
						d.open = false;
						d.query = "";
					},
					search: (d, query) => {
						d.query = query;
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
		//#region src/client/feedback.ts
		/**
		* Identify the unreadable preferences, their recovery path, and the bindings still in use.
		* @param config - failed read and last accepted preferences.
		* @param runtime - storage owner whose location and reload action to show.
		* @param t - shortcut dictionary.
		* @returns localized recovery guidance without replacing the stored document.
		*/
		function shortcutReadFailure(config, runtime, t) {
			const location = t(runtime === "desktop" ? "desktop-document" : "web-document");
			const reload = t(runtime === "desktop" ? "desktop-reload" : "web-reload");
			return `${t(config.error ?? "read", {
				location,
				reload
			})} ${t(config.usingDefaults ? "using-defaults" : "using-accepted")}`;
		}
		/**
		* Describe an unsuccessful preference write without losing command names.
		* @param result - rejected operation result.
		* @param catalog - current command labels.
		* @param t - shortcut dictionary.
		* @param runtime - storage owner for unreadable-document recovery guidance.
		* @returns localized diagnosis for a system toast and accessible field description.
		*/
		function shortcutFailure(result, catalog, t, runtime) {
			if (result.status === "unreadable") return shortcutReadFailure(result.snapshot, runtime, t);
			return result.issue ? t(result.issue) : result.status === "conflict" ? t("conflict", { commands: result.conflicts?.map((id) => catalog.find((row) => row.id === id)?.label ?? id).join(", ") ?? "" }) : t(result.status);
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-shortcuts/src/client/Reference.module.css.mjs
		const css = ".Lx7vRG_setting,.Lx7vRG_row,.Lx7vRG_binding{justify-content:space-between;align-items:center;gap:8px;display:flex}.Lx7vRG_setting{box-sizing:border-box;border-bottom:.5px solid var(--dsw-alias-border-l2);min-height:60px;padding:16px 0}.Lx7vRG_settingText{flex-direction:column;flex:1;gap:4px;min-width:0;padding-right:48px;display:flex}.Lx7vRG_settingTitle{color:var(--dsw-alias-label-primary);font-size:14px;line-height:22px}.Lx7vRG_settingDescription{color:var(--dsw-alias-label-tertiary);margin:0;font-size:12px;line-height:18px}.Lx7vRG_button{border-radius:var(--dsw-radius-md);background:var(--dsw-alias-bg-module-platform);min-width:90px;max-width:280px;height:36px;color:var(--dsw-alias-label-primary);font:inherit;cursor:pointer;white-space:nowrap;border:none;flex-shrink:0;justify-content:center;align-items:center;padding:0 14px;font-size:14px;line-height:22px;display:inline-flex}.Lx7vRG_button:hover{background:var(--dsw-alias-interactive-bg-hover)}.Lx7vRG_dialog{box-sizing:border-box;border-radius:24px;gap:0;width:min(480px,100%);height:600px;max-height:calc(100dvh - 108px);padding:0;transform:translateY(30px);box-shadow:0 0 1px #0003,0 0 4px #00000005,0 12px 32px #00000014}.Lx7vRG_dialog:focus{outline:none}.Lx7vRG_contents{flex-direction:column;flex:1;min-height:0;display:flex}.Lx7vRG_header{box-sizing:border-box;flex:0 0 46px;padding:22px 54px 0 24px;position:relative}.Lx7vRG_title{color:var(--dsw-alias-label-primary);margin:0;font-size:16px;font-weight:500;line-height:24px}.Lx7vRG_close{width:28px;height:28px;color:var(--dsw-alias-label-primary);cursor:pointer;background:0 0;border:0;border-radius:28px;justify-content:center;align-items:center;padding:6px;display:inline-flex;position:absolute;top:20px;right:14px}.Lx7vRG_close:hover{background:var(--dsw-alias-interactive-bg-hover)}.Lx7vRG_searchRow{box-sizing:border-box;flex:0 0 60px;align-items:center;padding:14px 20px 10px;display:flex}.Lx7vRG_searchField{box-sizing:border-box;border:.5px solid var(--dsw-alias-border-l2);background:var(--dsw-alias-markdown-code-block);border-radius:12px;align-items:center;gap:8px;width:100%;height:36px;padding:8px 14px;display:flex}.Lx7vRG_searchIcon{color:var(--dsw-alias-label-caption);flex:none}.Lx7vRG_search{width:100%;min-width:0;color:var(--dsw-alias-label-primary);font:inherit;background:0 0;border:0;outline:none;padding:0;font-size:13px;line-height:18px}.Lx7vRG_search::placeholder{color:var(--dsw-alias-label-tertiary);opacity:1}.Lx7vRG_list{--dsh-scrollbar-thumb:var(--dsw-alias-scrollbar-bg-l2);--dsh-scrollbar-thumb-hover:var(--dsw-alias-scrollbar-hover-l2);overscroll-behavior:contain;flex:1;min-height:0;padding:0 16px 18px;overflow-y:auto}.Lx7vRG_footer{border-top:.5px solid var(--dsw-alias-border-l2);flex:none;justify-content:space-between;align-items:center;gap:12px;min-height:52px;padding:0 24px;font-size:12px;line-height:18px;display:flex}.Lx7vRG_resetAll{color:var(--dsw-alias-label-secondary);font:inherit;cursor:pointer;background:0 0;border:0;border-radius:4px;align-items:center;gap:8px;padding:6px 0;display:inline-flex}.Lx7vRG_resetAll:enabled:hover{color:var(--dsw-alias-label-secondary)}.Lx7vRG_resetAll:focus-visible{outline:1px solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:2px}.Lx7vRG_resetAll:disabled{cursor:default;opacity:.45}.Lx7vRG_modifiedCount{color:var(--dsw-alias-label-tertiary)}.Lx7vRG_group{color:var(--dsw-alias-label-tertiary);margin:18px 8px 4px;font-size:12px;font-weight:400;line-height:18px}.Lx7vRG_rows{margin:0;padding:0;list-style:none}.Lx7vRG_row{border-radius:var(--dsw-radius-md);box-sizing:border-box;min-height:42px;color:var(--dsw-alias-label-primary);padding:8px;font-size:13px;line-height:18px;position:relative}.Lx7vRG_binding{pointer-events:none;flex-shrink:0;position:relative}.Lx7vRG_binding:has(.Lx7vRG_inlineEditor){pointer-events:auto}.Lx7vRG_commandLabel{pointer-events:none;text-overflow:ellipsis;white-space:nowrap;min-width:0;position:relative;overflow:hidden}.Lx7vRG_hint{color:var(--dsw-alias-label-tertiary);font-size:12px;line-height:18px}p.Lx7vRG_hint{margin:6px 0}.Lx7vRG_rowActions{width:20px;height:26px;color:var(--dsw-alias-label-tertiary);opacity:0;justify-content:center;align-items:center;display:inline-flex}.Lx7vRG_row:hover .Lx7vRG_rowActions,.Lx7vRG_row:has(:focus-visible) .Lx7vRG_rowActions{opacity:1}.Lx7vRG_inlineAction:focus-visible{outline:1px solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:2px;border-radius:4px}.Lx7vRG_rowButton{border-radius:inherit;cursor:pointer;background:0 0;border:0;width:100%;height:100%;position:absolute;inset:0}.Lx7vRG_row:has(.Lx7vRG_rowButton:enabled):hover{background:var(--dsw-alias-interactive-bg-hover)}.Lx7vRG_rowButton:focus-visible{outline:1px solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:-1px}.Lx7vRG_binding .Lx7vRG_keyBadge{color:var(--dsw-alias-label-secondary)}.Lx7vRG_binding .Lx7vRG_fixedKeyBadge{height:26px;color:var(--dsw-alias-label-tertiary)}.Lx7vRG_keyBadge{box-sizing:border-box;background:var(--dsw-alias-bg-module-platform);border-radius:10px;justify-content:center;height:26px;padding:0 12px}.Lx7vRG_unbound{box-sizing:border-box;background:var(--dsw-alias-bg-module-platform);height:26px;color:var(--dsw-alias-label-tertiary);white-space:nowrap;border-radius:10px;justify-content:center;align-items:center;padding:6px 16px;font-size:12px;line-height:14px;display:inline-flex}.Lx7vRG_inlineEditor{flex-direction:column;align-items:flex-end;gap:6px;display:flex}.Lx7vRG_binding:has(.Lx7vRG_inlineEditor){max-width:100%}.Lx7vRG_inlineControls{justify-content:flex-end;align-items:center;gap:8px;display:flex}.Lx7vRG_inlineAction{color:var(--dsw-alias-label-tertiary);font:inherit;white-space:nowrap;cursor:pointer;background:0 0;border:0;padding:0;font-size:12px;line-height:18px}.Lx7vRG_recorder{box-sizing:border-box;border:.5px solid var(--dsw-alias-border-l4);background:var(--dsw-alias-bg-module-platform);min-width:96px;height:26px;color:var(--dsw-alias-label-secondary);font:inherit;white-space:nowrap;cursor:pointer;border-radius:10px;justify-content:center;align-items:center;padding:3px 14px;font-size:12px;line-height:18px;display:inline-flex}.Lx7vRG_recorder:focus-visible{outline:1px solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:2px}.Lx7vRG_recorder.Lx7vRG_invalid{border:1px solid var(--dsw-alias-state-error-primary);box-shadow:none}.Lx7vRG_recorded{height:auto;color:inherit;background:0 0;padding:0}.Lx7vRG_review{color:var(--dsw-alias-label-tertiary);flex-direction:column;align-items:flex-end;gap:4px;font-size:12px;line-height:18px;display:flex}.Lx7vRG_toastSuccess{color:var(--dsw-alias-state-success-secondary)}.Lx7vRG_toastError{color:var(--dsw-alias-state-warn-secondary)}.Lx7vRG_srOnly{clip-path:inset(50%);white-space:nowrap;border:0;width:1px;height:1px;margin:-1px;padding:0;position:absolute;overflow:hidden}@media (hover:none){.Lx7vRG_rowActions{opacity:1}}.Lx7vRG_button:disabled,.Lx7vRG_rowButton:disabled,.Lx7vRG_inlineAction:disabled,.Lx7vRG_recorder[aria-disabled=true]{cursor:default;opacity:.45}.Lx7vRG_search::-webkit-search-cancel-button{-webkit-appearance:none}.Lx7vRG_clearSearch{box-sizing:border-box;border:.5px solid var(--dsw-alias-label-tertiary);corner-shape:round;width:16px;height:16px;color:var(--dsw-alias-label-secondary);cursor:pointer;background:0 0;border-radius:50%;flex:none;justify-content:center;align-items:center;padding:0;display:inline-flex}.Lx7vRG_clearSearch:hover{background:var(--dsw-alias-interactive-bg-hover)}.Lx7vRG_clearSearch:focus-visible{outline:1px solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:2px}";
		const tagId = "@deepseek-ai/dsh-client-ui-shortcuts/Reference.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-shortcuts";
			tag.dataset.pluginCss = tagId;
			tag.textContent = css;
			document.head.appendChild(tag);
		}
		var Reference_module_css_default = {
			"binding": "Lx7vRG_binding",
			"button": "Lx7vRG_button",
			"clearSearch": "Lx7vRG_clearSearch",
			"close": "Lx7vRG_close",
			"commandLabel": "Lx7vRG_commandLabel",
			"contents": "Lx7vRG_contents",
			"dialog": "Lx7vRG_dialog",
			"fixedKeyBadge": "Lx7vRG_fixedKeyBadge",
			"footer": "Lx7vRG_footer",
			"group": "Lx7vRG_group",
			"header": "Lx7vRG_header",
			"hint": "Lx7vRG_hint",
			"inlineAction": "Lx7vRG_inlineAction",
			"inlineControls": "Lx7vRG_inlineControls",
			"inlineEditor": "Lx7vRG_inlineEditor",
			"invalid": "Lx7vRG_invalid",
			"keyBadge": "Lx7vRG_keyBadge",
			"list": "Lx7vRG_list",
			"modifiedCount": "Lx7vRG_modifiedCount",
			"recorded": "Lx7vRG_recorded",
			"recorder": "Lx7vRG_recorder",
			"resetAll": "Lx7vRG_resetAll",
			"review": "Lx7vRG_review",
			"row": "Lx7vRG_row",
			"rowActions": "Lx7vRG_rowActions",
			"rowButton": "Lx7vRG_rowButton",
			"rows": "Lx7vRG_rows",
			"search": "Lx7vRG_search",
			"searchField": "Lx7vRG_searchField",
			"searchIcon": "Lx7vRG_searchIcon",
			"searchRow": "Lx7vRG_searchRow",
			"setting": "Lx7vRG_setting",
			"settingDescription": "Lx7vRG_settingDescription",
			"settingText": "Lx7vRG_settingText",
			"settingTitle": "Lx7vRG_settingTitle",
			"srOnly": "Lx7vRG_srOnly",
			"title": "Lx7vRG_title",
			"toastError": "Lx7vRG_toastError",
			"toastSuccess": "Lx7vRG_toastSuccess",
			"unbound": "Lx7vRG_unbound"
		};
		//#endregion
		//#region src/client/Editor.tsx
		/** Inline physical-key recording and revision-aware command editing. */
		/**
		* Save a released physical combination against the configuration the user reviewed.
		* @param props - command, accepted snapshots, and storage/feedback callbacks.
		* @returns inline command controls; failures retain the draft and allow another recording.
		*/
		function ShortcutEditor({ target, onClose, onSaved, onError, useCatalog, useConfig, useFixedCatalog, edit, recording, describeBinding, runtime, platform, t }) {
			const config = useConfig((value) => value);
			const catalog = useCatalog((value) => value);
			const fixed = useFixedCatalog((value) => value);
			const [revision, setRevision] = (0, react.useState)(config.revision);
			const [candidate, setCandidate] = (0, react.useState)(null);
			const [captured, setCaptured] = (0, react.useState)(false);
			const [busy, setBusy] = (0, react.useState)(false);
			const [nativeReady, setNativeReady] = (0, react.useState)(runtime === "web");
			const [message, setMessage] = (0, react.useState)("");
			const [retry, setRetry] = (0, react.useState)(null);
			const descriptionId = (0, react.useId)();
			const recorder = (0, react.useRef)(null);
			const mounted = (0, react.useRef)(true);
			const writing = (0, react.useRef)(false);
			/* v8 ignore next -- The effect installs reset before the recorder is enabled. */
			const restart = (0, react.useRef)(() => {});
			const desktopChords = runtime === "desktop" && (platform === "macos" || platform === "windows");
			const targetId = target.id;
			const stale = revision !== config.revision;
			const report = (text) => {
				setMessage(text);
				onError(text);
			};
			const save = async (operation, reviewedRevision = revision) => {
				if (writing.current) return;
				writing.current = true;
				setBusy(true);
				const result = await edit(operation, reviewedRevision);
				if (!mounted.current) return;
				writing.current = false;
				setBusy(false);
				if (result.status === "saved") onSaved();
				else {
					setRetry(operation.type === "set" ? operation.binding : null);
					report(shortcutFailure(result, [...catalog, ...fixed], t, runtime));
				}
			};
			const capture = (binding, id) => {
				setCandidate(binding);
				setCaptured(true);
				setRetry(null);
				const described = describeBinding(binding);
				const conflicts = described.conflicts.filter((value) => value !== id).map((value) => [...catalog, ...fixed].find((row) => row.id === value)?.label ?? value);
				if (described.issue !== null) {
					report(t(described.issue));
					return;
				}
				if (conflicts.length > 0) {
					report(t("conflict", { commands: conflicts.join(", ") }));
					return;
				}
				if (stale) {
					setRetry(binding);
					report(t("stale"));
					return;
				}
				if (config.status !== "ready") {
					report(config.status === "loading" ? t("not-ready") : shortcutReadFailure(config, runtime, t));
					return;
				}
				save({
					type: "set",
					id,
					binding
				});
			};
			const handlers = (0, react.useRef)({
				capture,
				report,
				onClose
			});
			(0, react.useLayoutEffect)(() => {
				handlers.current = {
					capture,
					report,
					onClose
				};
			});
			(0, react.useEffect)(() => {
				mounted.current = true;
				return () => {
					mounted.current = false;
				};
			}, []);
			(0, react.useEffect)(() => {
				let disposed = false;
				recording(true).then(() => {
					if (!disposed) setNativeReady(true);
				}, () => {
					if (!disposed) handlers.current.report(t("native-failed"));
				});
				const composition = (0, _deepseek_ai_dsh_client_ui_primitives.observeComposition)(document);
				let pending = null;
				let dead = false;
				let blocked = false;
				const held = /* @__PURE__ */ new Set();
				const reset = () => {
					pending = null;
					held.clear();
					blocked = false;
					dead = false;
				};
				restart.current = reset;
				const down = (event) => {
					if (composition.guards(event) || event.getModifierState("AltGraph")) {
						if (desktopChords) reset();
						return;
					}
					if ((!desktopChords || document.activeElement !== recorder.current) && event.key === "Escape" && !event.ctrlKey && !event.altKey && !event.metaKey && !event.shiftKey) {
						event.preventDefault();
						event.stopPropagation();
						if (!event.repeat && !writing.current) handlers.current.onClose();
						return;
					}
					const commandDeadKey = runtime === "web" && platform === "macos" && document.activeElement === recorder.current && event.code === "KeyN" && event.metaKey && event.altKey && !event.ctrlKey && !event.shiftKey;
					if (event.key === "Dead" && !commandDeadKey) {
						if (desktopChords) reset();
						dead = true;
						return;
					}
					if (dead) {
						dead = false;
						return;
					}
					const modifiers = [
						"control",
						"alt",
						"shift",
						"meta"
					].filter((value) => ({
						control: event.ctrlKey,
						alt: event.altKey,
						shift: event.shiftKey,
						meta: event.metaKey
					})[value]);
					const recordTab = desktopChords || (platform === "macos" || platform === "windows") && modifiers.length >= 3;
					if (document.activeElement !== recorder.current || event.key === "Tab" && !recordTab) return;
					event.preventDefault();
					event.stopPropagation();
					if (writing.current || event.repeat) return;
					if (/^(Control|Alt|Shift|Meta)(Left|Right)$/u.test(event.code)) {
						if (desktopChords && pending !== null) {
							reset();
							setCandidate(null);
							setCaptured(false);
						}
						return;
					}
					if (desktopChords) {
						held.add(event.code);
						if (blocked) return;
						if (held.size > 2) {
							blocked = true;
							pending = null;
							handlers.current.report(t("too-many-keys"));
							return;
						}
					}
					try {
						const codes = [event.code, ...desktopChords ? [...held].filter((value) => value !== event.code) : []];
						pending = describeBinding({
							code: codes[0],
							...codes[1] === void 0 ? {} : { secondCode: codes[1] },
							modifiers
						}).binding;
						setCandidate(pending);
						setCaptured(desktopChords);
						setMessage("");
						setRetry(null);
					} catch (_error) {
						blocked = desktopChords;
						pending = null;
						handlers.current.report(t("unsupported-key"));
					}
				};
				const up = (event) => {
					held.delete(event.code);
					if (desktopChords && document.activeElement === recorder.current) {
						event.preventDefault();
						event.stopPropagation();
					}
					const binding = pending !== null && (pending.code === event.code || pending.secondCode === event.code || pending.modifiers.some((modifier) => modifier === event.code.replace(/(Left|Right)$/u, "").toLowerCase())) ? pending : null;
					if (binding !== null) {
						pending = null;
						blocked = desktopChords;
					}
					if (desktopChords && (held.size === 0 || platform === "macos" && /^Meta(Left|Right)$/u.test(event.code))) {
						held.clear();
						blocked = false;
					}
					if (binding === null) return;
					handlers.current.capture(binding, targetId);
				};
				const blur = () => {
					reset();
					if (desktopChords) {
						setCandidate(null);
						setCaptured(false);
					}
				};
				const focus = () => {
					if (document.activeElement !== recorder.current) blur();
				};
				document.addEventListener("keydown", down, true);
				document.addEventListener("keyup", up, true);
				window.addEventListener("blur", blur);
				if (desktopChords) {
					document.addEventListener("focusin", focus, true);
					document.addEventListener("compositionstart", blur, true);
				}
				return () => {
					disposed = true;
					composition.dispose();
					document.removeEventListener("keydown", down, true);
					document.removeEventListener("keyup", up, true);
					window.removeEventListener("blur", blur);
					document.removeEventListener("focusin", focus, true);
					document.removeEventListener("compositionstart", blur, true);
					recording(false).catch(() => {});
				};
			}, [
				describeBinding,
				recording,
				t,
				targetId,
				runtime,
				platform,
				desktopChords
			]);
			(0, react.useEffect)(() => {
				const element = recorder.current;
				if (nativeReady && element !== null) (0, _deepseek_ai_dsh_client_ui_primitives.focusWithoutRing)(element);
			}, [nativeReady]);
			const readonly = busy || stale || config.status !== "ready";
			const review = stale && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: Reference_module_css_default.review,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: t("stale") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
					type: "button",
					className: Reference_module_css_default.inlineAction,
					disabled: busy,
					onClick: () => {
						setRevision(config.revision);
						setMessage("");
					},
					children: t("review")
				})]
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: Reference_module_css_default.inlineEditor,
				role: "group",
				"aria-label": target.label,
				"data-shortcut-modal": "shortcut-edit",
				"aria-busy": busy,
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: Reference_module_css_default.inlineControls,
						children: [
							/* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
								type: "button",
								className: Reference_module_css_default.inlineAction,
								disabled: readonly,
								onClick: () => {
									save({
										type: "reset",
										id: target.id
									});
								},
								children: t("reset")
							}),
							target.binding !== null && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
								type: "button",
								className: Reference_module_css_default.inlineAction,
								disabled: readonly,
								onClick: () => {
									save({
										type: "set",
										id: target.id,
										binding: null
									});
								},
								children: t("clear")
							}),
							/* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
								ref: recorder,
								type: "button",
								className: clsx(Reference_module_css_default.recorder, message && Reference_module_css_default.invalid),
								disabled: !nativeReady,
								"aria-disabled": busy || !nativeReady,
								onBlur: () => {
									restart.current();
								},
								"aria-label": t("record"),
								"aria-invalid": message !== "",
								"aria-describedby": descriptionId,
								onClick: () => {
									if (!writing.current) {
										restart.current();
										setCaptured(false);
										setMessage("");
										setRetry(null);
									}
								},
								children: captured && message === "" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.ShortcutKeys, {
									keys: describeBinding(candidate).keys,
									className: Reference_module_css_default.recorded
								}) : t("record")
							})
						]
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						id: descriptionId,
						className: Reference_module_css_default.srOnly,
						children: message || (desktopChords ? "" : t(runtime === "web" ? platform === "windows" ? "windows-web-help" : platform === "macos" ? "macos-web-help" : "web-help" : "record-help"))
					}),
					review,
					retry !== null && !stale && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
						type: "button",
						className: Reference_module_css_default.inlineAction,
						disabled: readonly,
						onClick: () => {
							capture(retry, target.id);
						},
						children: t("retry-save")
					})
				]
			});
		}
		//#endregion
		//#region src/client/Icons.tsx
		/** Supplied shortcut-control and feedback artwork, colored by the owning UI. */
		const artwork = {
			edit: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(react_jsx_runtime.Fragment, { children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", { d: "M7.4553 1.01221C8.02829 0.676881 8.73746 0.676959 9.31048 1.01221C9.52928 1.14026 9.72631 1.34353 9.98255 1.59978C10.2388 1.85601 10.4421 2.05305 10.5701 2.27185C10.9053 2.84485 10.9054 3.55407 10.5701 4.12703C10.4421 4.34576 10.2387 4.54293 9.98255 4.7991L4.99422 9.78744C4.71024 10.0714 4.50494 10.2832 4.2488 10.4324C3.99266 10.5815 3.70709 10.6557 3.32001 10.7625L2.43268 11.0072C2.08118 11.1042 1.75876 11.1948 1.50071 11.2265C1.2387 11.2585 0.884455 11.2512 0.607798 10.9745C0.331166 10.6979 0.323805 10.3436 0.35587 10.0816C0.387501 9.82358 0.47815 9.50112 0.575111 9.14964L0.819864 8.26232C0.926638 7.87525 1.00083 7.58966 1.14992 7.33353C1.2991 7.07739 1.5109 6.87209 1.79489 6.58811L6.78322 1.59978C7.03941 1.34359 7.23656 1.14027 7.4553 1.01221ZM11.6568 11.13H5.6639L6.72503 10.0689H11.6568V11.13ZM2.54509 7.33831C2.22348 7.65993 2.13155 7.75784 2.06755 7.86768C2.0036 7.97757 1.96369 8.10606 1.84272 8.54454L1.59797 9.43187C1.5011 9.78304 1.44118 10.0065 1.4162 10.1653C1.57504 10.1404 1.79876 10.0814 2.15046 9.98436L3.03779 9.7396C3.47631 9.61863 3.60475 9.57874 3.71464 9.51478C3.82448 9.45077 3.92239 9.35885 4.24401 9.03723L8.04047 5.23998L6.34155 3.54106L2.54509 7.33831ZM8.77473 1.92824C8.53277 1.78664 8.233 1.78664 7.99104 1.92824C7.91987 1.97 7.83628 2.04713 7.53343 2.34998L7.09176 2.79085L8.79068 4.48977L9.23235 4.0489C9.53517 3.74608 9.61232 3.66245 9.65409 3.59128C9.79567 3.34935 9.79564 3.04954 9.65409 2.8076C9.61237 2.73635 9.53565 2.65329 9.23235 2.34998C8.92899 2.04662 8.84598 1.96995 8.77473 1.92824Z" }) }),
			error: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [
				/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", { d: "M8.90039 3.97328V9.07973H7.09961V3.97328H8.90039Z" }),
				/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", { d: "M8.90039 10.341V12.3654H7.09961V10.341H8.90039Z" }),
				/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", { d: "M14.5996 8C14.5996 4.35492 11.6451 1.40039 8 1.40039C4.35492 1.40039 1.40039 4.35492 1.40039 8C1.40039 11.6451 4.35492 14.5996 8 14.5996C11.6451 14.5996 14.5996 11.6451 14.5996 8ZM15.9004 8C15.9004 12.363 12.363 15.9004 8 15.9004C3.63695 15.9004 0.0996094 12.363 0.0996094 8C0.0996094 3.63695 3.63695 0.0996094 8 0.0996094C12.363 0.0996094 15.9004 3.63695 15.9004 8Z" })
			] }),
			success: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", { d: "M12.5303 6.53027L8.80273 10.2578C8.54967 10.5109 8.31796 10.7439 8.10645 10.9141C7.88375 11.0932 7.616 11.2602 7.27344 11.3145C7.09229 11.3431 6.90771 11.3431 6.72656 11.3145C6.384 11.2602 6.11625 11.0932 5.89355 10.9141C5.68204 10.7439 5.45033 10.5109 5.19727 10.2578L3.46973 8.53027L4.53027 7.46973L6.25781 9.19727C6.53457 9.47402 6.70036 9.63859 6.83398 9.74609C6.95637 9.84453 6.98241 9.83644 6.96094 9.83301C6.98679 9.83709 7.01321 9.83709 7.03906 9.83301C7.01759 9.83644 7.04363 9.84453 7.16602 9.74609C7.29964 9.63859 7.46543 9.47402 7.74219 9.19727L11.4697 5.46973L12.5303 6.53027Z" }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", { d: "M14.5996 8C14.5996 4.35492 11.6451 1.40039 8 1.40039C4.35492 1.40039 1.40039 4.35492 1.40039 8C1.40039 11.6451 4.35492 14.5996 8 14.5996C11.6451 14.5996 14.5996 11.6451 14.5996 8ZM15.9004 8C15.9004 12.363 12.363 15.9004 8 15.9004C3.63695 15.9004 0.0996094 12.363 0.0996094 8C0.0996094 3.63695 3.63695 0.0996094 8 0.0996094C12.363 0.0996094 15.9004 3.63695 15.9004 8Z" })] })
		};
		/**
		* Render the shortcut design artwork at its supplied size.
		* @param props - glyph kind and semantic color class.
		* @returns an ornamental SVG hidden from assistive technology.
		*/
		function ShortcutIcon({ kind, className }) {
			const size = kind === "edit" ? 12 : 16;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("svg", {
				width: size,
				height: size,
				viewBox: `0 0 ${size} ${size}`,
				fill: "currentColor",
				className,
				"aria-hidden": "true",
				children: artwork[kind]
			});
		}
		//#endregion
		//#region src/client/Reference.tsx
		/** Searchable editable shortcut reference and its General Settings row. */
		/** Core reference positions are independent of labels and plugin registration order. */
		const coreActionOrder = new Map([
			"shortcuts.open",
			"session.new",
			"sidebar.left.toggle",
			"session.search",
			"workspace.add",
			"session.rename",
			"session.fork",
			"session.archive",
			"settings.open",
			"workspace.openLocal",
			"sidebar.right.toggle",
			"workspace.files",
			"browser.new",
			"terminal.new",
			"pane.split",
			"pane.fullscreen.toggle",
			"page.refresh",
			"page.close"
		].map((id, index) => [id, index]));
		/**
		* Render the General Settings action that opens the shortcut reference.
		* @param props - shared dialog action and localized labels.
		* @returns the settings row.
		*/
		function ShortcutsRow({ actions, t, useCatalog }) {
			const shortcut = useCatalog((rows) => rows.find((row) => row.id === "shortcuts.open"));
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: Reference_module_css_default.setting,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: Reference_module_css_default.settingText,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						className: Reference_module_css_default.settingTitle,
						children: t("settings")
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
						className: Reference_module_css_default.settingDescription,
						children: t("description")
					})]
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
					disabled: !shortcut?.keys.length,
					label: t("global-hint"),
					shortcutKeys: shortcut?.keys,
					children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
						className: Reference_module_css_default.button,
						type: "button",
						"aria-label": t("view"),
						"aria-keyshortcuts": shortcut?.aria,
						onClick: () => {
							actions.open();
						},
						children: t("view")
					})
				})]
			});
		}
		/**
		* Render core actions in product order, then other commands by ID within each group. Search relevance takes precedence.
		* @param props - root store, effective catalog, and localized copy.
		* @returns the single reference dialog when open.
		*/
		function ShortcutReference({ useStore, actions, useCatalog, useConfig, useFixedCatalog, platform, runtime, edit, recording, describeBinding, t }) {
			const { open, query, focusRequest } = useStore((state) => state);
			const catalog = useCatalog((value) => value);
			const config = useConfig((value) => value);
			const fixedCatalog = useFixedCatalog((value) => value);
			const [target, setTarget] = (0, react.useState)(null);
			const [resetRevision, setResetRevision] = (0, react.useState)(null);
			const [busy, setBusy] = (0, react.useState)(false);
			const [toast, setToast] = (0, react.useState)(null);
			const mounted = (0, react.useRef)(true);
			const search = (0, react.useRef)(null);
			(0, react.useEffect)(() => {
				mounted.current = true;
				return () => {
					mounted.current = false;
				};
			}, []);
			const dismissToast = (0, react.useCallback)(() => {
				setToast(null);
			}, []);
			const notify = (0, react.useCallback)((text, error = false) => {
				setToast((previous) => {
					if (error && previous?.error && previous.text === text) return previous;
					return {
						text,
						error,
						seq: (previous?.seq ?? 0) + 1
					};
				});
			}, []);
			(0, react.useEffect)(() => {
				if (open && config.status === "unreadable") notify(shortcutReadFailure(config, runtime, t), true);
			}, [
				open,
				config,
				runtime,
				notify,
				t
			]);
			const closeEditor = () => {
				setTarget(null);
				const dialog = search.current?.closest("[role=\"dialog\"]");
				/* v8 ignore next -- Editor callbacks run while its reference dialog and search input are mounted. */
				if (dialog != null) (0, _deepseek_ai_dsh_client_ui_primitives.focusWithoutRing)(dialog, { preventScroll: true });
			};
			const closeReference = () => {
				if (busy) return;
				/* v8 ignore next -- The confirmation modal blocks the reference's close control. */
				if (resetRevision !== null) setResetRevision(null);
				else if (target !== null) closeEditor();
				else actions.close();
			};
			(0, react.useEffect)(() => {
				if (!open) setResetRevision(null);
			}, [open]);
			const persist = async (...args) => {
				setBusy(true);
				const result = await edit(...args);
				if (mounted.current) setBusy(false);
				return result;
			};
			const resetAll = async () => {
				/* v8 ignore next -- The reset control is accessible only while its confirmation is open. */
				if (resetRevision === null) return;
				const result = await persist({ type: "reset-all" }, resetRevision);
				if (!mounted.current) return;
				if (result.status === "saved" || result.status === "stale") setResetRevision(null);
				notify(result.status === "saved" ? t("reset-saved") : result.status === "write-failed" ? t("reset-failed") : shortcutFailure(result, catalog, t, runtime), result.status !== "saved");
			};
			const editorProps = {
				useCatalog,
				useConfig,
				useFixedCatalog,
				platform,
				runtime,
				edit: persist,
				recording,
				describeBinding,
				t,
				onClose: closeEditor,
				onSaved: () => {
					notify(t("saved"));
					closeEditor();
				},
				onError: (message) => {
					notify(message, true);
				}
			};
			(0, react.useLayoutEffect)(() => {
				const input = search.current;
				if (open && input !== null && !(0, _deepseek_ai_dsh_client_ui_primitives.isBehindModal)(input)) (0, _deepseek_ai_dsh_client_ui_primitives.focusWithoutRing)(input);
			}, [open, focusRequest]);
			const ranked = (0, _deepseek_ai_dsh_client_ui_primitives.rankByName)([...catalog.map((row) => ({
				...row,
				names: [
					...row.aliases,
					row.keys.filter((key) => key !== "+").join("+"),
					row.keys.filter((key) => key !== "+").join(""),
					row.aria ?? "",
					row.aria?.replace("Meta", "Cmd") ?? ""
				],
				group: "application"
			})), ...fixedCatalog.map((row) => ({
				...row,
				names: [row.id, row.keys.join(" ")]
			}))].sort((left, right) => Number(left.id === "response.stop") - Number(right.id === "response.stop") || (coreActionOrder.get(left.id) ?? coreActionOrder.size) - (coreActionOrder.get(right.id) ?? coreActionOrder.size) || Number(left.id > right.id) - Number(left.id < right.id)).flatMap((row) => row.names.map((name) => ({
				name,
				label: row.label,
				row
			}))), query.trim());
			const matches = [...new Set(ranked.map((match) => match.row))];
			const modifiedCount = Object.keys(config.document.profiles[`${runtime}:${platform}`] ?? {}).length;
			(0, react.useLayoutEffect)(() => {
				const input = search.current;
				if (open && resetRevision === null && modifiedCount === 0 && document.activeElement === document.body && input !== null) (0, _deepseek_ai_dsh_client_ui_primitives.focusWithoutRing)(input);
			}, [
				open,
				resetRevision,
				modifiedCount
			]);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [
				/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Modal, {
					open,
					onClose: closeReference,
					title: t("title"),
					headless: true,
					shortcutModal: "shortcuts",
					className: Reference_module_css_default.dialog,
					children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: Reference_module_css_default.contents,
						onPointerDownCapture: (event) => {
							if (target === null || busy || !(event.target instanceof Element) || event.target.closest("button, input, a, [contenteditable=\"true\"], [role=\"group\"]") !== null) return;
							event.preventDefault();
							closeEditor();
						},
						children: [
							/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("header", {
								className: Reference_module_css_default.header,
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("h2", {
									className: Reference_module_css_default.title,
									children: t("title")
								}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
									type: "button",
									className: Reference_module_css_default.close,
									"aria-label": t("close"),
									disabled: busy,
									onClick: closeReference,
									children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconCloseOutlineRegular, { size: 14 })
								})]
							}),
							/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
								className: Reference_module_css_default.searchRow,
								children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
									className: Reference_module_css_default.searchField,
									role: "search",
									"aria-label": t("search"),
									children: [
										/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("svg", {
											className: Reference_module_css_default.searchIcon,
											width: "14",
											height: "14",
											viewBox: "0 0 14 14",
											fill: "none",
											"aria-hidden": "true",
											children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
												d: "M10.28 5.86536C10.28 3.41381 8.29245 1.42644 5.84094 1.42627C3.38929 1.42627 1.40186 3.4137 1.40186 5.86536C1.40203 8.31686 3.38939 10.3044 5.84094 10.3044C8.29234 10.3043 10.2799 8.31676 10.28 5.86536ZM11.4174 5.86536C11.4172 8.94498 8.92057 11.4416 5.84094 11.4418C2.76117 11.4418 0.263843 8.94509 0.263672 5.86536C0.263672 2.78548 2.76106 0.288086 5.84094 0.288086C8.92067 0.288258 11.4174 2.78559 11.4174 5.86536Z",
												fill: "currentColor"
											}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
												d: "M13.7372 12.9078L12.9323 13.7127L9.9732 10.7536L10.7781 9.94867L13.7372 12.9078Z",
												fill: "currentColor"
											})]
										}),
										/* @__PURE__ */ (0, react_jsx_runtime.jsx)("input", {
											ref: search,
											"data-modal-autofocus": true,
											className: Reference_module_css_default.search,
											type: "search",
											"aria-label": t("search"),
											placeholder: t("search"),
											value: query,
											onChange: (event) => {
												actions.search(event.target.value);
											}
										}),
										query !== "" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
											type: "button",
											className: Reference_module_css_default.clearSearch,
											"aria-label": t("clear-search"),
											onClick: () => {
												actions.search("");
												search.current?.focus();
											},
											children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconCloseOutlineRegular, { size: 10 })
										})
									]
								})
							}),
							/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
								className: Reference_module_css_default.list,
								"aria-busy": config.status === "loading",
								children: [[
									"application",
									"input",
									"menus",
									"approval"
								].map((group) => {
									const rows = matches.filter((row) => row.group === group);
									if (rows.length === 0) return null;
									return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
										"aria-label": t(group),
										children: [group !== "application" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("h3", {
											className: Reference_module_css_default.group,
											children: t(group)
										}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("ul", {
											className: Reference_module_css_default.rows,
											children: rows.map((row) => /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("li", {
												className: Reference_module_css_default.row,
												children: [
													"modified" in row && target?.id !== row.id && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
														type: "button",
														className: Reference_module_css_default.rowButton,
														"aria-label": t("edit-label", { command: row.label }),
														disabled: busy || config.status !== "ready",
														onClick: () => {
															setTarget(row);
														}
													}),
													/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
														className: Reference_module_css_default.commandLabel,
														children: row.label
													}),
													/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
														className: Reference_module_css_default.binding,
														children: "modified" in row ? target?.id === row.id ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ShortcutEditor, {
															target: row,
															...editorProps
														}, row.id) : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
															className: Reference_module_css_default.rowActions,
															children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ShortcutIcon, { kind: "edit" })
														}), row.keys.length === 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
															className: Reference_module_css_default.unbound,
															children: t("unbound")
														}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.ShortcutKeys, {
															keys: row.keys,
															className: Reference_module_css_default.keyBadge
														})] }) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.ShortcutKeys, {
															keys: row.keys,
															className: Reference_module_css_default.fixedKeyBadge
														})
													})
												]
											}, row.id))
										})]
									}, group);
								}), matches.length === 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
									className: Reference_module_css_default.hint,
									role: "status",
									children: t("empty")
								})]
							}),
							/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("footer", {
								className: Reference_module_css_default.footer,
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
									type: "button",
									className: Reference_module_css_default.resetAll,
									disabled: busy || config.status !== "ready" || modifiedCount === 0,
									onClick: (event) => {
										event.currentTarget.focus();
										setTarget(null);
										setResetRevision(config.revision);
									},
									children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconRefreshOutlineRegular, { size: 12 }), t("reset-all")]
								}), modifiedCount > 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
									className: Reference_module_css_default.modifiedCount,
									children: t("modified-count", { count: modifiedCount })
								})]
							})
						]
					})
				}),
				/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Modal, {
					open: open && resetRevision !== null,
					title: t("reset-title"),
					description: t("reset-description"),
					closeLabel: t("close-confirmation"),
					onClose: () => {
						if (!busy) setResetRevision(null);
					},
					footer: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
						"data-modal-autofocus": true,
						disabled: busy,
						onClick: () => {
							setResetRevision(null);
						},
						children: t("cancel")
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
						variant: "primary",
						disabled: busy || config.status !== "ready",
						onClick: () => {
							resetAll();
						},
						children: t("reset")
					})] })
				}),
				toast !== null && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Toast, {
					text: toast.text,
					onDone: dismissToast,
					icon: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ShortcutIcon, {
						kind: toast.error ? "error" : "success",
						className: toast.error ? Reference_module_css_default.toastError : Reference_module_css_default.toastSuccess
					})
				}, toast.seq)
			] });
		}
		//#endregion
		//#region src/client/locales.ts
		/** Shortcut reference copy; only implemented operations appear in the catalog. */
		const zh = {
			"edit-label": "修改{command}快捷键",
			record: "按下快捷键",
			"record-help": "松开组合键即保存。Tab 切换操作，Esc 退出录键。",
			"web-help": "浏览器可用组合：Mod+/、Mod+Shift+,、Mod+Shift+.。Mod 在 Mac 上为 Command，其他系统为 Ctrl。",
			"unsupported-key": "暂不支持这个按键。",
			reserved: "此组合由系统或文本编辑操作保留。",
			"modifier-required": "请同时按下 Command、Ctrl 或 Alt 修饰键。",
			"too-many-keys": "最多同时按下两个非修饰键。请松开后重试。",
			"macos-web-help": "可设置 Command+/、Command+,、Command+反斜杠、Control+反引号、Command+Option+按键或 Command+Shift+按键。也支持三个或四个不同修饰键的组合。浏览器或系统占用的组合可能无法送达页面。",
			"windows-web-help": "可设置 Ctrl+/、Ctrl+,、Ctrl+Alt+按键或 Ctrl+Shift+按键。也支持三个或四个不同修饰键的组合。浏览器或系统占用的组合可能无法送达页面。",
			"unsupported-browser": "此浏览器暂不支持该组合。",
			conflict: "已被「{commands}」占用",
			saved: "已修改",
			clear: "移除",
			"retry-save": "重试保存",
			reset: "恢复默认",
			"reset-all": "恢复全部默认",
			"modified-count": "{count} 项已自定义",
			"reset-title": "恢复全部默认快捷键？",
			"reset-description": "将恢复当前平台的默认快捷键。你修改或移除的快捷键都会恢复，其他平台不受影响。",
			cancel: "取消",
			"reset-saved": "已恢复默认快捷键",
			"close-confirmation": "关闭确认",
			"reset-failed": "恢复默认失败。原快捷键已保留，请重试。",
			review: "已核对最新配置",
			stale: "快捷键配置或可用命令已更新。请核对最新键位后再保存。",
			"write-failed": "保存失败。原快捷键和当前草稿已保留，请重试。",
			"not-ready": "快捷键尚未准备好，请稍后重试。",
			read: "无法读取 {location}。请检查访问权限后{reload}。",
			invalid: "{location} 的快捷键配置内容损坏。请先备份并修复该配置，然后{reload}。",
			future: "{location} 的快捷键配置由更新版本创建。请升级 Harness 后重试。",
			"web-document": "当前网站本地存储中的 dsh.keybindings.v1",
			"desktop-document": "userData/keybindings.json",
			"web-reload": "刷新页面",
			"desktop-reload": "重新启动 Harness",
			"using-defaults": "当前使用默认键位。",
			"using-accepted": "当前继续使用上次读取成功的键位。",
			"native-failed": "无法启用桌面录键保护，请关闭后重试。",
			"global-hint": "全局唤起查看",
			"clear-search": "清空搜索",
			title: "快捷键",
			open: "快捷键速查",
			settings: "快捷键",
			view: "编辑快捷键",
			description: "查看和编辑当前可用的快捷键和输入操作",
			search: "搜索快捷键",
			close: "关闭快捷键",
			application: "应用操作",
			input: "消息输入",
			menus: "菜单与弹层",
			approval: "审批区域",
			unbound: "暂无快捷键",
			empty: "没有匹配的快捷键",
			move: "移动菜单选择",
			select: "选择菜单项",
			dismiss: "关闭菜单或顶层弹窗"
		};
		/** Typed English counterpart. */
		const en = {
			"edit-label": "Edit shortcut for {command}",
			record: "Press a shortcut",
			"record-help": "Release the keys to save. Tab moves between actions; Esc cancels.",
			"web-help": "Browser combinations: Mod+/, Mod+Shift+,, Mod+Shift+.. Mod is Command on Mac and Ctrl elsewhere.",
			"unsupported-key": "This key is not supported.",
			reserved: "This combination is reserved for system or text editing actions.",
			"modifier-required": "Include Command, Ctrl, or Alt in the combination.",
			"too-many-keys": "Hold at most two non-modifier keys. Release the keys to try again.",
			"macos-web-help": "Use Command+/, Command+,, Command+Backslash, Control+Backquote, Command+Option+key, or Command+Shift+key. Combinations with three or four distinct modifiers are also supported. Browser or system shortcuts may not reach the page.",
			"windows-web-help": "Use Ctrl+/, Ctrl+,, Ctrl+Alt+key, or Ctrl+Shift+key. Combinations with three or four distinct modifiers are also supported. Browser or system shortcuts may not reach the page.",
			"unsupported-browser": "This browser does not support this combination yet.",
			conflict: "Already used by “{commands}”",
			saved: "Modified",
			clear: "Remove",
			"retry-save": "Retry save",
			reset: "Restore default",
			"reset-all": "Restore all defaults",
			"modified-count": "{count} customized",
			"reset-title": "Restore all default shortcuts?",
			"reset-description": "Restore the default shortcuts for this platform. All modified or removed shortcuts will be restored. Other platforms are unaffected.",
			cancel: "Cancel",
			"reset-saved": "Default shortcuts restored",
			"close-confirmation": "Close confirmation",
			"reset-failed": "Could not restore defaults. Your shortcuts are unchanged. Please retry.",
			review: "I have reviewed the latest configuration",
			stale: "Shortcut configuration or available commands changed. Review the latest bindings before saving.",
			"write-failed": "Could not save. Your previous shortcuts and current draft are preserved. Please retry.",
			"not-ready": "Shortcuts are not ready. Please try again.",
			read: "Could not read {location}. Check access permissions, then {reload}.",
			invalid: "Shortcut configuration in {location} is damaged. Back up and repair this configuration, then {reload}.",
			future: "Shortcut configuration in {location} was created by a newer version. Upgrade Harness and try again.",
			"web-document": "this site’s localStorage entry dsh.keybindings.v1",
			"desktop-document": "userData/keybindings.json",
			"web-reload": "reload the page",
			"desktop-reload": "restart Harness",
			"using-defaults": "Default bindings are active.",
			"using-accepted": "The last successfully read bindings remain active.",
			"native-failed": "Could not protect desktop key recording. Exit recording and try again.",
			"global-hint": "Open from anywhere",
			"clear-search": "Clear search",
			title: "Keyboard shortcuts",
			open: "Open keyboard shortcuts",
			settings: "Keyboard shortcuts",
			view: "Edit shortcuts",
			description: "View and edit available shortcuts and input actions",
			search: "Search shortcuts",
			close: "Close keyboard shortcuts",
			application: "Application",
			input: "Message input",
			menus: "Menus and dialogs",
			approval: "Approval area",
			unbound: "No shortcut",
			empty: "No matching shortcuts",
			move: "Move menu selection",
			select: "Select menu item",
			dismiss: "Close menu or top dialog"
		};
		//#endregion
		//#region src/client/fixed.ts
		/**
		* Describe shared menu actions for display and conflict checking.
		* @param t - shortcut dictionary.
		* @returns read-only actions and each physical combination they occupy.
		*/
		function fixedCommands(t) {
			return [
				{
					id: "move",
					keys: ["↑", "↓"],
					bindings: [{
						code: "ArrowUp",
						modifiers: []
					}, {
						code: "ArrowDown",
						modifiers: []
					}],
					group: "menus"
				},
				{
					id: "select",
					keys: ["Enter"],
					bindings: [{
						code: "Enter",
						modifiers: []
					}],
					group: "menus"
				},
				{
					id: "dismiss",
					keys: ["Esc"],
					bindings: [{
						code: "Escape",
						modifiers: []
					}],
					group: "menus"
				}
			].map((row) => ({
				...row,
				id: `fixed.${row.id}`,
				label: () => t(row.id)
			}));
		}
		//#endregion
		//#region src/client/index.ts
		/** Required command, locale, and slot services. */
		const inject = [
			"shortcuts",
			"locale",
			"slots"
		];
		/**
		* Register the reference command, settings row, and single shell overlay.
		* @param ctx - plugin-owned client context.
		*/
		function apply(ctx) {
			ctx.effect(() => ctx.locale.register("shortcuts", {
				zh,
				en
			}), "shortcuts: dictionaries");
			const t = ctx.locale.bind("shortcuts");
			const handle = createShortcutsStore();
			const instance = handle.create();
			const store = {
				...handle,
				create: () => instance
			};
			const edit = (...args) => ctx.shortcuts.edit(...args);
			const recording = (active) => ctx.shortcuts.recording(active);
			const describeBinding = (binding) => ctx.shortcuts.describeBinding(binding);
			for (const command of fixedCommands(t)) ctx.effect(() => ctx.shortcuts.registerFixed(command), `shortcuts: ${command.id}`);
			const injected = () => ({
				platform: ctx.shortcuts.platform,
				runtime: ctx.shortcuts.runtime,
				edit,
				recording,
				describeBinding,
				hooks: {
					catalog: ctx.shortcuts.catalog,
					config: ctx.shortcuts.config,
					fixedCatalog: ctx.shortcuts.fixedCatalog
				}
			});
			ctx.slots.inject("settings.general.item", () => ctx.slots.register({
				name: "settings.general.item",
				id: "shortcuts",
				order: 20,
				locale: "shortcuts",
				store,
				inject: injected
			}, ShortcutsRow));
			ctx.slots.inject("shell.overlay", () => {
				const disposeCommand = ctx.shortcuts.register({
					id: "shortcuts.open",
					label: () => t("open"),
					aliases: ["shortcuts", "keyboard shortcuts"],
					defaults: {
						"desktop:macos": {
							code: "Slash",
							modifiers: ["primary"]
						},
						"desktop:windows": {
							code: "Slash",
							modifiers: ["primary"]
						},
						"desktop:linux": {
							code: "Slash",
							modifiers: ["primary"]
						},
						"web:macos": {
							code: "Slash",
							modifiers: ["primary"]
						},
						"web:windows": {
							code: "Slash",
							modifiers: ["primary"]
						},
						"web:linux": {
							code: "Slash",
							modifiers: ["primary"]
						}
					},
					regions: [
						"page",
						"editable",
						"terminal"
					],
					modals: ["settings", "shortcuts"],
					resolve: ({ modal }) => {
						if (modal !== null && modal !== "settings" && modal !== "shortcuts") return {
							status: "blocked",
							reason: "modal"
						};
						return {
							status: "handled",
							run: () => {
								if (modal === "shortcuts") (0, _deepseek_ai_dsh_client_ui_primitives.closeTopModal)(document);
								else instance.actions.open();
							}
						};
					}
				});
				const disposeSlot = ctx.slots.register({
					name: "shell.overlay",
					id: "shortcuts",
					locale: "shortcuts",
					store,
					inject: injected
				}, ShortcutReference);
				return () => {
					disposeCommand();
					disposeSlot();
				};
			});
		}
		//#endregion
		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map