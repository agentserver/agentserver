window.__ModuleLoader__.load({
	id: "@deepseek-ai/dsh-client-ui-sidebar-terminal",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		Object.defineProperty(exports, Symbol.toStringTag, { value: "Module" });
		let _deepseek_ai_dsh_client_ui_primitives = require("@deepseek-ai/dsh-client-ui-primitives");
		let react = require("react");
		let react_jsx_runtime = require("react/jsx-runtime");
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-sidebar-terminal/src/client/TerminalGuide.module.css.mjs
		const css$1 = ".Mu6A4q_entry{box-sizing:border-box;border:.5px solid var(--dsw-alias-border-l3);border-radius:var(--dsl-guide-entry-radius);background:var(--dsw-alias-bg-layer-1);align-items:center;gap:14px;width:100%;min-height:56px;padding:14px 20px;display:flex;position:relative;overflow:hidden}.Mu6A4q_main{border-radius:0;width:100%;height:100%;position:absolute;inset:0}.Mu6A4q_main:focus-visible{outline-offset:-2px}.Mu6A4q_icon{width:26px;height:26px;color:var(--dsw-alias-label-secondary);pointer-events:none;flex:none;justify-content:center;align-items:center;display:flex;position:relative}.Mu6A4q_text{pointer-events:none;flex-direction:column;flex:1;gap:3px;min-width:0;display:flex;position:relative}.Mu6A4q_titleRow{align-items:center;gap:4px;min-width:0;display:flex}.Mu6A4q_title{color:var(--dsw-alias-label-primary);white-space:nowrap;text-overflow:ellipsis;font-size:14px;line-height:1.4;overflow:hidden}.Mu6A4q_description{color:var(--dsw-alias-label-tertiary);white-space:nowrap;text-overflow:ellipsis;font-size:11px;line-height:1.4;overflow:hidden}.Mu6A4q_trigger{border-radius:4px;flex:none;width:20px;height:20px;padding:0}.Mu6A4q_trigger svg{width:14px;height:14px;color:var(--dsw-alias-label-tertiary)}.Mu6A4q_menu{pointer-events:auto;flex:none;display:flex}.Mu6A4q_shortcut{pointer-events:none;position:relative}";
		const tagId$1 = "@deepseek-ai/dsh-client-ui-sidebar-terminal/TerminalGuide.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$1) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-sidebar-terminal";
			tag.dataset.pluginCss = tagId$1;
			tag.textContent = css$1;
			document.head.appendChild(tag);
		}
		var TerminalGuide_module_css_default = {
			"description": "Mu6A4q_description",
			"entry": "Mu6A4q_entry",
			"icon": "Mu6A4q_icon",
			"main": "Mu6A4q_main",
			"menu": "Mu6A4q_menu",
			"shortcut": "Mu6A4q_shortcut",
			"text": "Mu6A4q_text",
			"title": "Mu6A4q_title",
			"titleRow": "Mu6A4q_titleRow",
			"trigger": "Mu6A4q_trigger"
		};
		//#endregion
		//#region src/client/TerminalGuide.tsx
		/** Shell launch menu owned by the terminal provider's guide entry. */
		/**
		* Open the remembered shell from the card or choose another shell from its menu.
		* The card displays its effective shortcut; the shell menu sits beside the title.
		* @param props - guide copy, enclosing tab actions and cancellable discovery.
		* @returns separate launch and menu buttons within one guide card.
		*/
		function TerminalGuide({ title, description, kind, useTabInfo, loadShells, selectShell, t, useShortcuts }) {
			const shortcut = useShortcuts((entries) => entries.find((entry) => entry.id === "terminal.new"));
			const { tab } = useTabInfo();
			const [open, setOpen] = (0, react.useState)(false);
			const [attempt, setAttempt] = (0, react.useState)(0);
			const [state, setState] = (0, react.useState)({ phase: "loading" });
			(0, react.useEffect)(() => {
				if (!open) return;
				const lifetime = new AbortController();
				loadShells(lifetime.signal).then((choices) => {
					if (!lifetime.signal.aborted) setState({
						phase: "ready",
						choices
					});
				}, (error) => {
					if (!lifetime.signal.aborted) setState({
						phase: "failed",
						message: error instanceof Error ? error.message : String(error)
					});
				});
				return () => {
					lifetime.abort();
				};
			}, [
				open,
				attempt,
				loadShells
			]);
			const items = state.phase === "ready" ? state.choices.shells.map((shell) => ({
				id: shell.path,
				label: shell.name
			})) : state.phase === "loading" ? [{
				id: "loading",
				label: t("shellLoading"),
				disabled: true
			}] : [{
				id: "error",
				label: t("failed", { message: state.message }),
				disabled: true
			}, {
				id: "retry",
				label: t("retry")
			}];
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: TerminalGuide_module_css_default.entry,
				"data-sidebar-right-guide-entry": kind,
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
						variant: "ghost",
						className: TerminalGuide_module_css_default.main,
						"aria-label": description === void 0 ? title : `${title} ${description}`,
						"aria-keyshortcuts": shortcut?.aria,
						onClick: () => {
							tab.actions.openTab("terminal", { replaceTab: true });
						}
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: TerminalGuide_module_css_default.icon,
						"aria-hidden": "true",
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.PluginArtworkTerminal, { size: description === void 0 ? 22 : 26 })
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
						className: TerminalGuide_module_css_default.text,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
							className: TerminalGuide_module_css_default.titleRow,
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
								className: TerminalGuide_module_css_default.title,
								"aria-hidden": "true",
								children: title
							}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Menu, {
								open,
								portal: true,
								autoFocus: true,
								align: "end",
								className: TerminalGuide_module_css_default.menu,
								items: items.length === 0 ? [{
									id: "empty",
									label: t("shellEmpty"),
									disabled: true
								}] : items,
								selectedId: state.phase === "ready" ? state.choices.selectedShell : void 0,
								onClose: () => {
									setOpen(false);
								},
								onSelect: (path) => {
									if (state.phase === "failed") {
										setState({ phase: "loading" });
										setAttempt((value) => value + 1);
										return;
									}
									selectShell(path);
									setOpen(false);
									tab.actions.openTab("terminal", {
										replaceTab: true,
										params: { shellPath: path }
									});
								},
								anchor: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
									variant: "ghost",
									className: TerminalGuide_module_css_default.trigger,
									"aria-label": t("shell"),
									"aria-haspopup": "menu",
									"aria-expanded": open,
									onClick: () => {
										setState({ phase: "loading" });
										setOpen((value) => !value);
									},
									children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronDownOutlineRegular, {})
								})
							})]
						}), description !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: TerminalGuide_module_css_default.description,
							"aria-hidden": "true",
							children: description
						})]
					}),
					shortcut !== void 0 && shortcut.keys.length > 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.ShortcutKeys, {
						keys: shortcut.keys,
						className: TerminalGuide_module_css_default.shortcut
					})
				]
			});
		}
		//#endregion
		//#region src/client/LazyTerminalBody.tsx
		/** Load xterm only after a terminal body is mounted. */
		const LoadedTerminalBody = (0, react.lazy)(async () => ({ default: (await require.async("./client.terminal.js")).TerminalBody }));
		/**
		* Suspend while the package-local terminal chunk arrives.
		* @param props - Terminal body props supplied by the sidebar slot.
		* @returns the deferred terminal renderer.
		*/
		function LazyTerminalBody(props) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(react.Suspense, {
				fallback: null,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(LoadedTerminalBody, { ...props })
			});
		}
		//#endregion
		//#region src/client/TerminalIcon.tsx
		/**
		* Render the tab title's terminal prompt in the surrounding text color.
		* @returns a decorative sixteen-pixel line glyph.
		*/
		function TerminalIcon() {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("svg", {
				width: "16",
				height: "16",
				viewBox: "0 0 16 16",
				fill: "none",
				"aria-hidden": "true",
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
					d: "M3 4L7 8L3 12",
					stroke: "currentColor"
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
					d: "M9 12H13",
					stroke: "currentColor"
				})]
			});
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-sidebar-terminal/src/client/TerminalTitle.module.css.mjs
		const css = ".w_BTSa_title{text-overflow:ellipsis;white-space:nowrap;min-width:0;overflow:hidden}.w_BTSa_name{width:120px;min-width:48px;max-width:100%;color:inherit;font:inherit;background:var(--dsw-alias-bg-l1);border:.5px solid var(--dsw-alias-border-l3);border-radius:var(--dsw-radius-sm);outline:none;padding:0 4px}";
		const tagId = "@deepseek-ai/dsh-client-ui-sidebar-terminal/TerminalTitle.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-sidebar-terminal";
			tag.dataset.pluginCss = tagId;
			tag.textContent = css;
			document.head.appendChild(tag);
		}
		var TerminalTitle_module_css_default = {
			"name": "w_BTSa_name",
			"title": "w_BTSa_title"
		};
		//#endregion
		//#region src/client/TerminalTitle.tsx
		/** Live terminal names in docked and floating tab chrome. */
		/**
		* Render the terminal name, editable in place on double-click.
		* @param props - sidebar occurrence, terminal model and localized copy.
		* @returns the terminal icon and current name or its editor.
		*/
		function TerminalTitle({ useTabInfo, useTerminal, view, t }) {
			const { tab } = useTabInfo();
			const title = useTerminal(tab.id, (state) => state?.info?.title ?? state?.title) ?? tab.title;
			const [editing, setEditing] = (0, react.useState)(false);
			const input = (0, react.useRef)(null);
			const label = (0, react.useRef)(null);
			const cancelled = (0, react.useRef)(false);
			(0, react.useLayoutEffect)(() => {
				if (editing) return;
				const chip = label.current?.closest("[data-dockkit-tab], [data-dockkit-float-grip]");
				const rename = (event) => {
					event.stopPropagation();
					cancelled.current = false;
					setEditing(true);
				};
				chip?.addEventListener("dblclick", rename);
				return () => {
					chip?.removeEventListener("dblclick", rename);
				};
			}, [editing]);
			(0, react.useLayoutEffect)(() => {
				if (!editing) return;
				input.current?.focus();
				input.current?.select();
			}, [editing]);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(TerminalIcon, {}), editing ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("input", {
				ref: input,
				className: TerminalTitle_module_css_default.name,
				defaultValue: title,
				maxLength: 120,
				"aria-label": t("rename"),
				onPointerDown: (event) => {
					event.stopPropagation();
				},
				onClick: (event) => {
					event.stopPropagation();
				},
				onDoubleClick: (event) => {
					event.stopPropagation();
				},
				onBlur: (event) => {
					setEditing(false);
					const next = event.currentTarget.value.trim();
					if (!cancelled.current && next !== "" && next !== title) view(tab.id).rename(next);
				},
				onKeyDown: (event) => {
					event.stopPropagation();
					if (event.nativeEvent.isComposing || event.nativeEvent.keyCode === 229) return;
					if (event.key === "Escape") {
						cancelled.current = true;
						event.currentTarget.blur();
					} else if (event.key === "Enter") event.currentTarget.blur();
				}
			}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
				ref: label,
				className: TerminalTitle_module_css_default.title,
				children: title
			})] });
		}
		//#endregion
		//#region src/client/locales.ts
		/** Simplified Chinese terminal copy. */
		const zh = {
			"shortcut.noSession": "请先选择会话",
			recoveryFailed: "恢复终端失败：{message}",
			retryRecovery: "重试恢复终端",
			shell: "选择 Shell",
			shellLoading: "正在读取 Shell…",
			shellEmpty: "没有可用的 Shell",
			description: "在会话工作区运行命令",
			title: "终端",
			new: "新建终端",
			loading: "正在读取终端环境…",
			creating: "正在启动…",
			connecting: "正在连接…",
			disconnected: "连接已断开。",
			reconnect: "重新连接",
			readonly: "此页面当前只读。",
			control: "接管输入",
			closed: "终端已关闭。",
			exited: "进程已退出（{code}）",
			failed: "终端错误：{message}",
			rename: "终端名称",
			unavailable: "不可用",
			retry: "重试",
			cleanupFailed: "终端「{title}」未能结束：{message}",
			missingTerminal: "此终端已不存在，请新建终端。",
			inputFull: "输入缓冲区已满，请重新连接后重试。",
			attachmentEnded: "终端连接已结束，请重新连接。",
			invalidOutput: "终端画面传输异常，请重新连接。",
			terminalLimit: "终端数量已达上限，请关闭不用的终端后重试。已退出的终端也计入数量。"
		};
		/** English terminal copy. */
		const en = {
			"shortcut.noSession": "Select a session first",
			recoveryFailed: "Terminal recovery failed: {message}",
			retryRecovery: "Retry terminal recovery",
			shell: "Choose shell",
			shellLoading: "Loading shells…",
			shellEmpty: "No shells available",
			description: "Run commands in the Session workspace",
			title: "Terminal",
			new: "New terminal",
			loading: "Reading terminal environment…",
			creating: "Starting…",
			connecting: "Connecting…",
			disconnected: "Disconnected.",
			reconnect: "Reconnect",
			readonly: "This view is read-only.",
			control: "Take control",
			closed: "Terminal closed.",
			exited: "Process exited ({code})",
			failed: "Terminal error: {message}",
			rename: "Terminal name",
			unavailable: "Unavailable",
			retry: "Retry",
			cleanupFailed: "Terminal “{title}” could not be ended: {message}",
			missingTerminal: "This terminal no longer exists. Open a new terminal.",
			inputFull: "The input buffer is full. Reconnect and try again.",
			attachmentEnded: "The terminal connection ended. Reconnect to continue.",
			invalidOutput: "The terminal screen could not be received. Reconnect to recover it.",
			terminalLimit: "The terminal limit has been reached. Close unused terminals and try again. Exited terminals also count toward the limit."
		};
		//#endregion
		//#region src/client/index.ts
		/** Services needed by the terminal's two sidebar seats. */
		const inject = [
			"slots",
			"locale",
			"sidebarRight",
			"sidebarRightTabs",
			"webTerminals",
			"theme",
			"shortcuts"
		];
		/**
		* Register the terminal type, observable views and background process cleanup.
		* @param ctx - Client root Context with sidebar and terminal services.
		*/
		function apply(ctx) {
			ctx.effect(() => {
				const sync = () => {
					ctx.webTerminals.retainTabs(ctx.sidebarRight.openTabs.getSnapshot().filter((tab) => tab.kind === "terminal"));
				};
				const unsubscribe = ctx.sidebarRight.openTabs.subscribe(sync);
				sync();
				return () => {
					unsubscribe();
					ctx.webTerminals.retainTabs([]);
				};
			}, "ui-sidebar-terminal.window-holds");
			const target = (sessionId, key) => ctx.sidebarRight.tabDomain.occurrence(sessionId, { id: key }).navigation.getSnapshot().params;
			const terminalId = (sessionId, key) => {
				const params = target(sessionId, key);
				return params !== void 0 && "terminalId" in params ? params.terminalId : void 0;
			};
			const view = (sessionId, key) => {
				const params = target(sessionId, key);
				const contentId = ctx.sidebarRight.tabDomain.occurrence(sessionId, { id: key }).navigation.getSnapshot().address;
				return ctx.webTerminals.view(sessionId, key, contentId, terminalId(sessionId, key), params !== void 0 && "shellPath" in params ? params.shellPath : void 0);
			};
			const namespace = "sidebarTerminal";
			const id = "@deepseek-ai/dsh-client-ui-sidebar-terminal";
			const t = ctx.locale.bind(namespace);
			ctx.effect(() => ctx.shortcuts.register({
				id: "terminal.new",
				label: () => t("new"),
				aliases: ["new terminal", "shell"],
				defaults: {
					"desktop:macos": {
						code: "Backquote",
						modifiers: ["control"]
					},
					"desktop:windows": {
						code: "Backquote",
						modifiers: ["control"]
					},
					"desktop:linux": {
						code: "Backquote",
						modifiers: ["control"]
					},
					"web:macos": {
						code: "Backquote",
						modifiers: ["control"]
					},
					"web:windows": {
						code: "Backquote",
						modifiers: ["control"]
					}
				},
				regions: [
					"page",
					"editable",
					"terminal"
				],
				modals: [],
				resolve: ({ target: element }) => {
					const target = ctx.sidebarRight.commandTarget(element);
					if (target === void 0) return {
						status: "blocked",
						reason: t("shortcut.noSession")
					};
					return {
						status: "handled",
						run: () => {
							ctx.sidebarRight.openTabFromTarget("terminal", target);
						}
					};
				}
			}), "ui-sidebar-terminal: shortcut");
			ctx.effect(() => ctx.locale.register(namespace, {
				zh,
				en
			}), "ui-sidebar-terminal.copy");
			ctx.effect(() => ctx.sidebarRightTabs.register({
				id,
				kind: "terminal",
				multiple: true,
				priority: "builtin",
				title: () => t("title"),
				guide: [{
					id: "new",
					order: 20,
					title: () => t("new"),
					description: () => t("description"),
					icon: _deepseek_ai_dsh_client_ui_primitives.PluginArtworkTerminal
				}]
			}), "ui-sidebar-terminal.type");
			ctx.effect(() => ctx.sidebarRight.registerCloseHandler("terminal", (sessionId, tab) => {
				ctx.webTerminals.close(sessionId, tab.id, tab.contentId, terminalId(sessionId, tab.id));
			}), "ui-sidebar-terminal.close");
			const inject = (sessionId) => ({
				view: (key) => view(sessionId, key),
				keyedHooks: { terminal: (key) => view(sessionId, key).state }
			});
			const theme = {
				getSnapshot: () => ctx.theme.getTheme(),
				subscribe: (listener) => ctx.on("theme/change", listener)
			};
			ctx.effect(() => ctx.slots.inject("sidebar.right.tab.guide.entry", () => ctx.slots.register({
				name: "sidebar.right.tab.guide.entry",
				key: id,
				locale: namespace,
				inject: (sessionId) => ({
					hooks: { shortcuts: ctx.shortcuts.catalog },
					loadShells: (signal) => ctx.webTerminals.launchShells(sessionId, signal),
					selectShell: (path) => {
						ctx.webTerminals.selectShell(path);
					}
				})
			}, TerminalGuide)), "ui-sidebar-terminal.guide");
			ctx.effect(() => ctx.slots.inject("sidebar.right.pane.tab", () => ctx.slots.register({
				name: "sidebar.right.pane.tab",
				key: id,
				locale: namespace,
				inject: (sessionId) => ({
					...inject(sessionId),
					hooks: { theme }
				})
			}, LazyTerminalBody)), "ui-sidebar-terminal.body");
			ctx.effect(() => ctx.slots.inject("sidebar.right.pane.tab.title", () => ctx.slots.register({
				name: "sidebar.right.pane.tab.title",
				key: id,
				locale: namespace,
				inject
			}, TerminalTitle)), "ui-sidebar-terminal.title");
		}
		//#endregion
		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map