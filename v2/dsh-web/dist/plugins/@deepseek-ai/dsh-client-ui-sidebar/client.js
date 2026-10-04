window.__ModuleLoader__.load({
	id: "@deepseek-ai/dsh-client-ui-sidebar",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		Object.defineProperty(exports, Symbol.toStringTag, { value: "Module" });
		let _deepseek_ai_dsh_client_store = require("@deepseek-ai/dsh-client-store");
		let _deepseek_ai_dsh_client_ui_slots = require("@deepseek-ai/dsh-client-ui-slots");
		let _deepseek_ai_dsh_client_ui_primitives = require("@deepseek-ai/dsh-client-ui-primitives");
		let react_jsx_runtime = require("react/jsx-runtime");
		let react = require("react");
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-sidebar/src/client/HeaderLeadingControls.module.css.mjs
		const css$1 = "._0WhOMG_controls{align-items:center;gap:8px;display:flex}._0WhOMG_iconButton{border-radius:var(--dsw-radius-sm);width:28px;height:28px;color:var(--dsw-alias-label-secondary);cursor:pointer;background:0 0;border:none;flex:none;justify-content:center;align-items:center;padding:0;display:inline-flex}._0WhOMG_iconButton:hover{background:var(--dsw-alias-interactive-bg-hover)}";
		const tagId$1 = "@deepseek-ai/dsh-client-ui-sidebar/HeaderLeadingControls.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$1) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-sidebar";
			tag.dataset.pluginCss = tagId$1;
			tag.textContent = css$1;
			document.head.appendChild(tag);
		}
		var HeaderLeadingControls_module_css_default = {
			"controls": "_0WhOMG_controls",
			"iconButton": "_0WhOMG_iconButton"
		};
		//#endregion
		//#region src/client/HeaderLeadingControls.tsx
		/** Window-chrome controls for the fully hidden sidebar (frame shell.leading seat). */
		/**
		* Sidebar-open and New Session controls in the frame's window-chrome seat.
		* On macOS desktop a collapsed sidebar hides entirely (no rail), taking both
		* controls off screen; this occupant puts them back beside the traffic
		* lights. The frame mounts the seat only in that state and owns its
		* placement, so the occupant renders unconditionally.
		* @param props - Injected sidebar actions plus the sidebar locale seat.
		* @returns the two window-chrome controls.
		*/
		function HeaderLeadingControls({ toggleSidebar, startSession, useShortcuts, t }) {
			const shortcut = useShortcuts((rows) => rows.find((row) => row.id === "sidebar.left.toggle"));
			const newShortcut = useShortcuts((rows) => rows.find((row) => row.id === "session.new"));
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: HeaderLeadingControls_module_css_default.controls,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
					label: t("toggle.open"),
					shortcutKeys: shortcut?.keys,
					delayMs: 500,
					children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
						type: "button",
						className: HeaderLeadingControls_module_css_default.iconButton,
						"aria-label": t("toggle.open"),
						"aria-keyshortcuts": shortcut?.aria,
						onClick: () => {
							toggleSidebar();
						},
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconPanelLeftOutlineRegular, { size: 16 })
					})
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
					label: t("session.new.label"),
					shortcutKeys: newShortcut?.keys,
					delayMs: 500,
					children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
						type: "button",
						className: HeaderLeadingControls_module_css_default.iconButton,
						"aria-label": t("session.new.label"),
						"aria-keyshortcuts": newShortcut?.aria,
						onClick: () => {
							startSession();
						},
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconNewChatOutlineRegular, { size: 16 })
					})
				})]
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
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-sidebar/src/client/SidebarRoot.module.css.mjs
		const css = ".plQ_da_root{--dsh-sidebar-inline-padding:12px;height:100%;padding:6px var(--dsh-sidebar-inline-padding);box-sizing:border-box;background:var(--dsw-specific-sidebar-fill);color:var(--dsw-alias-label-primary);--dsh-scrollbar-thumb:var(--dsw-alias-scrollbar-bg-l2);--dsh-scrollbar-thumb-hover:var(--dsw-alias-scrollbar-hover-l2);flex-direction:column;font-size:14px;display:flex}[data-platform=darwin] .plQ_da_root{background:0 0}.plQ_da_root.plQ_da_collapsed{padding:18px 10px 6px}[data-windows-titlebar] .plQ_da_root .plQ_da_logoRow{height:40px;margin:0;padding:0}[data-windows-titlebar] .plQ_da_toggle{top:calc((var(--dsh-windows-titlebar-height) - 28px) / 2);z-index:30;-webkit-app-region:no-drag;position:fixed;left:12px}[data-windows-titlebar] .plQ_da_root.plQ_da_collapsed .plQ_da_logoRow{height:0}[data-windows-titlebar] .plQ_da_root:not(.plQ_da_collapsed) .plQ_da_brand{padding-left:4px}[data-windows-titlebar] .plQ_da_brandIdentity{transform:translateY(1px)}[data-windows-titlebar] .plQ_da_brandMark{transform:translate(1px)}[data-windows-titlebar] .plQ_da_brandName{font-weight:400}[data-windows-titlebar] .plQ_da_root.plQ_da_collapsed{padding:0}[data-windows-titlebar] .plQ_da_root:not(.plQ_da_collapsed) .plQ_da_newSession{margin-top:8px;margin-left:0}[data-windows-titlebar] .plQ_da_collapsed .plQ_da_panelList,[data-windows-titlebar] .plQ_da_collapsed .plQ_da_regionArea,[data-windows-titlebar] .plQ_da_collapsed .plQ_da_footArea{display:none}html[data-windows-titlebar]:has([data-sidebar-collapsed=true]){--dsh-windows-menu-start:84px}[data-windows-titlebar] .plQ_da_collapsed .plQ_da_newSession{top:calc((var(--dsh-windows-titlebar-height) - 28px) / 2);z-index:30;-webkit-app-region:no-drag;border:none;margin:0;padding:0;position:fixed;left:48px}[data-windows-titlebar] .plQ_da_railIn .plQ_da_iconButton,[data-windows-titlebar] .plQ_da_railIn .plQ_da_newSession{animation:none}[data-windows-titlebar] .plQ_da_collapsed .plQ_da_toggle,[data-windows-titlebar] .plQ_da_collapsed .plQ_da_newSession{corner-shape:round;width:28px;height:28px;color:var(--dsw-alias-label-secondary);border-radius:50%}[data-windows-titlebar] .plQ_da_collapsed .plQ_da_toggle .plQ_da_panelIcon{display:inline}.plQ_da_root.plQ_da_quietBars{--dsh-scrollbar-thumb:transparent;--dsh-scrollbar-thumb-hover:transparent}.plQ_da_fading>*{opacity:0;transition:opacity .15s var(--ds-ease-in-out)}.plQ_da_wide{animation:plQ_da_wide-in .2s var(--ds-ease-in-out)}@keyframes plQ_da_wide-in{0%{opacity:0}}.plQ_da_railIn .plQ_da_iconButton,.plQ_da_railIn .plQ_da_newSession,.plQ_da_railIn .plQ_da_panelList,.plQ_da_railIn .plQ_da_regionArea{animation:plQ_da_rail-in .15s var(--ds-ease-in-out) backwards}.plQ_da_railIn .plQ_da_footArea{animation:plQ_da_rail-fade-in .15s var(--ds-ease-in-out) backwards}@keyframes plQ_da_rail-in{0%{opacity:0;transform:translate(49px)}}@keyframes plQ_da_rail-fade-in{0%{opacity:0}}.plQ_da_topStrip{box-sizing:border-box;height:52px;margin:-6px calc(-1 * var(--dsh-sidebar-inline-padding)) 0;flex:none;justify-content:flex-end;align-items:center;padding:0 12px 2px;display:flex}[data-fullscreen] .plQ_da_topStrip{justify-content:flex-start}.plQ_da_topStrip+.plQ_da_logoRow{margin-top:-12px}.plQ_da_logoRow{box-sizing:border-box;flex:none;justify-content:flex-end;align-items:center;gap:8px;height:60px;margin-bottom:4px;padding:8px 0 8px 4px;display:flex;overflow:hidden}.plQ_da_collapsed .plQ_da_logoRow{justify-content:flex-start;height:36px;margin-bottom:12px;padding:0;overflow:visible}.plQ_da_brand{min-width:0;color:inherit;cursor:pointer;background:0 0;border:none;flex:1;align-items:center;padding:0;display:inline-flex;overflow:hidden}[data-platform=darwin] .plQ_da_brand{cursor:default}.plQ_da_brandIdentity{align-items:center;gap:8px;min-width:0;height:24px;display:inline-flex}.plQ_da_brandMark{flex:none;justify-content:center;align-items:center;display:inline-flex}.plQ_da_brandName{letter-spacing:.04em;align-items:center;gap:6px;min-width:0;height:24px;font-size:18px;font-weight:600;line-height:24px;display:inline-flex}.plQ_da_fallbackBrandName{letter-spacing:0;white-space:nowrap;font-size:17px}.plQ_da_localBuildBrand{white-space:nowrap;flex-direction:column;flex:none;justify-content:center;align-items:flex-start;gap:1px;height:24px;display:inline-flex}.plQ_da_localBuildTitle{letter-spacing:0;font-size:12px;line-height:13px}.plQ_da_iconButton{border-radius:var(--dsw-radius-sm);cursor:pointer;width:28px;height:28px;color:var(--dsw-alias-label-secondary);background:0 0;border:none;flex:none;justify-content:center;align-items:center;padding:0;display:inline-flex;position:relative}.plQ_da_iconButton:hover{background:var(--dsw-alias-interactive-bg-hover)}.plQ_da_iconButton.plQ_da_toggle:focus-visible{outline:var(--dsw-focus-ring-width) solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:-2px}.plQ_da_collapsed .plQ_da_iconButton{border-radius:var(--dsw-radius-md);width:36px;height:36px}.plQ_da_collapsed .plQ_da_toggle .plQ_da_panelIcon{display:none}.plQ_da_collapsed .plQ_da_toggle:hover .plQ_da_panelIcon{display:inline}.plQ_da_collapsed .plQ_da_toggle:hover .plQ_da_railMark{display:none}.plQ_da_railMark{justify-content:center;align-items:center;display:inline-flex}.plQ_da_collapsed .plQ_da_iconButton{color:var(--dsw-alias-label-primary)}.plQ_da_buildVersion{height:10px;color:var(--dsw-alias-label-primary-inverted);background:var(--dsw-alias-label-primary);font-family:var(--ds-font-family-code);white-space:nowrap;border-radius:2px;flex:none;align-items:center;padding:0 3px;font-size:6px;font-weight:500;line-height:10px;display:inline-flex}.plQ_da_newSession{box-sizing:border-box;border:.5px solid var(--dsw-alias-border-l3);border-radius:var(--dsw-radius-md);background:var(--dsw-alias-button-elevated-fill);height:38px;color:var(--dsw-alias-label-primary);cursor:pointer;flex:none;justify-content:center;align-items:center;gap:0;margin:0 2px 12px;padding:8px 12px;font-size:14px;font-weight:500;line-height:22px;display:flex;position:relative;overflow:hidden;container-type:inline-size}.plQ_da_newSession:hover{background:var(--dsw-alias-button-floating-hover)}[data-platform=darwin] .plQ_da_newSession{background:#ffffff8c}[data-platform=darwin] .plQ_da_newSession:hover{background:#ffffff4d}[data-platform=darwin] [data-ds-dark-theme] .plQ_da_newSession{background:#ffffff26}[data-platform=darwin] [data-ds-dark-theme] .plQ_da_newSession:hover{background:#fff3}.plQ_da_collapsed .plQ_da_newSession{background:0 0;border-color:#0000;align-self:flex-start;gap:0;width:36px;height:36px;margin:0 0 12px;padding:0}.plQ_da_collapsed .plQ_da_newSession:hover{background:var(--dsw-alias-interactive-bg-hover)}.plQ_da_newSessionLabel{white-space:nowrap;max-width:200px;overflow:hidden}.plQ_da_collapsed .plQ_da_newSessionLabel{max-width:0}.plQ_da_newSessionLabelMask{flex:1;min-width:0}.plQ_da_newSession:is(:hover,:focus-visible) .plQ_da_newSessionLabelMask:has(+.plQ_da_newSessionShortcut){overflow:hidden;mask-image:linear-gradient(90deg,#000 calc(100% - 16px),#0000)}.plQ_da_newSessionContent{justify-content:center;align-items:center;gap:6px;width:100cqw;display:flex}.plQ_da_newSessionContent>svg{flex:none}.plQ_da_newSessionShortcut{opacity:0;pointer-events:none;flex:none;font-weight:400;display:inline-flex}.plQ_da_newSession:is(:hover,:focus-visible) .plQ_da_newSessionShortcut{opacity:1}.plQ_da_panelList{flex-direction:column;flex:none;gap:4px;margin-bottom:8px;display:flex}.plQ_da_panelRow{box-sizing:border-box;border-radius:var(--dsw-radius-md);min-height:36px;color:var(--dsw-alias-label-primary);font:inherit;text-align:left;cursor:pointer;background:0 0;border:none;align-items:center;gap:8px;margin:0 2px;padding:7px 8px;line-height:22px;display:flex}.plQ_da_panelRow:hover{background:var(--dsw-alias-interactive-bg-hover)}.plQ_da_panelRow.plQ_da_panelActive{background:var(--dsw-alias-interactive-bg-hover);color:var(--dsw-alias-label-primary)}.plQ_da_panelRow:focus-visible{outline:var(--dsw-focus-ring-width) solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:-2px}.plQ_da_panelGlyph{flex:none;justify-content:center;align-items:center;display:inline-flex}.plQ_da_panelTitle{text-overflow:ellipsis;white-space:nowrap;min-width:0;overflow:hidden}.plQ_da_collapsed .plQ_da_panelList{gap:12px;margin-bottom:12px}.plQ_da_collapsed .plQ_da_panelRow{width:36px;height:36px;color:var(--dsw-alias-label-primary);justify-content:center;margin:0;padding:0}.plQ_da_regionArea{min-height:0;margin-left:-4px;margin-right:calc(-1 * var(--dsh-sidebar-inline-padding));flex-direction:column;flex:1;padding-left:4px;display:flex;overflow:hidden}.plQ_da_collapsed .plQ_da_regionArea{margin-left:0;margin-right:0;padding-left:0}.plQ_da_footArea{flex-direction:column;flex:none;display:flex}.plQ_da_settingsArea,.plQ_da_footerActions{flex:none;width:100%;min-width:0}.plQ_da_footerActions{display:flex}.plQ_da_collapsed .plQ_da_footArea{align-items:center}.plQ_da_collapsed .plQ_da_settingsArea,.plQ_da_collapsed .plQ_da_footerActions{justify-content:center;width:auto;display:flex}@media (prefers-reduced-motion:reduce){.plQ_da_wide,.plQ_da_fading>*,.plQ_da_railIn .plQ_da_iconButton,.plQ_da_railIn .plQ_da_newSession,.plQ_da_railIn .plQ_da_panelList,.plQ_da_railIn .plQ_da_footArea,.plQ_da_railIn .plQ_da_regionArea{transition:none;animation:none}}";
		const tagId = "@deepseek-ai/dsh-client-ui-sidebar/SidebarRoot.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-sidebar";
			tag.dataset.pluginCss = tagId;
			tag.textContent = css;
			document.head.appendChild(tag);
		}
		var SidebarRoot_module_css_default = {
			"brand": "plQ_da_brand",
			"brandIdentity": "plQ_da_brandIdentity",
			"brandMark": "plQ_da_brandMark",
			"brandName": "plQ_da_brandName",
			"buildVersion": "plQ_da_buildVersion",
			"collapsed": "plQ_da_collapsed",
			"fading": "plQ_da_fading",
			"fallbackBrandName": "plQ_da_fallbackBrandName",
			"footArea": "plQ_da_footArea",
			"footerActions": "plQ_da_footerActions",
			"iconButton": "plQ_da_iconButton",
			"localBuildBrand": "plQ_da_localBuildBrand",
			"localBuildTitle": "plQ_da_localBuildTitle",
			"logoRow": "plQ_da_logoRow",
			"newSession": "plQ_da_newSession",
			"newSessionContent": "plQ_da_newSessionContent",
			"newSessionLabel": "plQ_da_newSessionLabel",
			"newSessionLabelMask": "plQ_da_newSessionLabelMask",
			"newSessionShortcut": "plQ_da_newSessionShortcut",
			"panelActive": "plQ_da_panelActive",
			"panelGlyph": "plQ_da_panelGlyph",
			"panelIcon": "plQ_da_panelIcon",
			"panelList": "plQ_da_panelList",
			"panelRow": "plQ_da_panelRow",
			"panelTitle": "plQ_da_panelTitle",
			"quietBars": "plQ_da_quietBars",
			"rail-fade-in": "plQ_da_rail-fade-in",
			"rail-in": "plQ_da_rail-in",
			"railIn": "plQ_da_railIn",
			"railMark": "plQ_da_railMark",
			"regionArea": "plQ_da_regionArea",
			"root": "plQ_da_root",
			"settingsArea": "plQ_da_settingsArea",
			"toggle": "plQ_da_toggle",
			"topStrip": "plQ_da_topStrip",
			"wide": "plQ_da_wide",
			"wide-in": "plQ_da_wide-in"
		};
		//#endregion
		//#region src/client/SidebarRoot.tsx
		/**
		* Sidebar shell: column geometry and global panel navigation.
		* Collapse is a slide plus crossfade:
		* content freezes at its expanded width (inline style) and fades out in place
		* while the sliding column (AppFrame grid tracks) clips it — nothing reflows
		* mid-slide. At settle the wide-only content unmounts and the upper
		* controls enter the 56px rail from the same horizontal offset (one icon each,
		* same top-down order) on one fade that ends with the slide. The bottom-pinned
		* settings control only fades. The workspace/session browsing region between
		* global panel rows and the foot is the `sidebar.workspaces` registrant's,
		* and the foot holds `sidebar.settings` plus `sidebar.footer.action`; the shell
		* hands them the wide flag (plus an expand request callback for the browser).
		*
		* The column also owns whether the scroll regions nested in it draw a
		* scrollbar at all: the shell tracks the pointer and rebinds ui-theme's
		* scrollbar indirection away while it is elsewhere, so a list the user is not
		* pointing at carries no bar.
		*/
		/** Wide-content unmount delay; matches the 150ms wide-content fade-out. */
		const COLLAPSE_SETTLE_MS = 150;
		/**
		* How long the column's scrollbars stay drawn after the pointer leaves it.
		* The bar is a pointer affordance here, and hiding it on the leave event
		* itself makes it blink out while the pointer is only crossing the column's
		* edge — on the way to the conversation, or around a portalled menu.
		*/
		const SCROLLBAR_LINGER_MS = 2e3;
		/** Format complete-build metadata for the local brand badge. */
		function localBuildVersion() {
			const version = {}.DSH_CLIENT_VERSION;
			if (version === void 0) return void 0;
			const commit = {}.DSH_CLIENT_COMMIT_HASH;
			return version + (commit === void 0 ? "" : `-${commit}`) + ({}.DSH_CLIENT_GIT_DIRTY === "true" ? "-dirty" : "");
		}
		/** Each panel row subscribes only to its own selection state. */
		function PanelRow({ id, label, wide, usePanelInfo, selectPanel, renderSlot }) {
			const active = usePanelInfo((info) => info.activePanelId === id);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
				label,
				delayMs: 500,
				disabled: wide,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
					type: "button",
					className: clsx(SidebarRoot_module_css_default.panelRow, active && SidebarRoot_module_css_default.panelActive),
					"aria-label": label,
					"aria-current": active ? "page" : void 0,
					onClick: () => {
						selectPanel(id);
					},
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: SidebarRoot_module_css_default.panelGlyph,
						"aria-hidden": "true",
						children: renderSlot("sidebar.panellist", {
							size: wide ? 16 : 18,
							active
						}, { only: id })
					}), wide && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: clsx(SidebarRoot_module_css_default.panelTitle, SidebarRoot_module_css_default.wide),
						children: label
					})]
				})
			});
		}
		/**
		* Render the sidebar column shell.
		* @param props - composed slot props (runtime share + injected callbacks, contract/slots.ts).
		* @returns the sidebar element tree.
		*/
		function SidebarRoot({ collapsed, width, startSession, toggleSidebar, selectPanel, usePanels, useShortcuts, usePanelInfo, t, renderSlot }) {
			const panels = usePanels((snapshot) => snapshot);
			const shortcut = useShortcuts((rows) => rows.find((row) => row.id === "sidebar.left.toggle"));
			const newShortcut = useShortcuts((rows) => rows.find((row) => row.id === "session.new"));
			const toggleLabel = collapsed ? t("toggle.open") : t("toggle.collapse");
			const [settled, setSettled] = (0, react.useState)(collapsed);
			(0, react.useEffect)(() => {
				if (!collapsed) {
					setSettled(false);
					return;
				}
				const timer = window.setTimeout(() => {
					setSettled(true);
				}, COLLAPSE_SETTLE_MS);
				return () => {
					window.clearTimeout(timer);
				};
			}, [collapsed]);
			const windowsTitlebar = document.documentElement.hasAttribute("data-windows-titlebar");
			const wide = windowsTitlebar ? !collapsed : !collapsed || !settled;
			const captionTooltipSide = windowsTitlebar ? "bottom" : "right";
			const lastWideWidth = (0, react.useRef)(width);
			if (!collapsed) lastWideWidth.current = width;
			const everWide = (0, react.useRef)(!collapsed);
			if (!collapsed) everWide.current = true;
			const column = (0, react.useRef)(null);
			const [pointerInside, setPointerInside] = (0, react.useState)(false);
			const lingerTimer = (0, react.useRef)(void 0);
			const armLinger = () => {
				if (lingerTimer.current !== void 0) return;
				lingerTimer.current = window.setTimeout(() => {
					lingerTimer.current = void 0;
					setPointerInside(false);
				}, SCROLLBAR_LINGER_MS);
			};
			const cancelLinger = () => {
				window.clearTimeout(lingerTimer.current);
				lingerTimer.current = void 0;
			};
			(0, react.useEffect)(() => {
				if (!pointerInside) return;
				const onMove = (event) => {
					const rect = column.current?.getBoundingClientRect();
					/* v8 ignore next -- the listener only exists while the column is mounted and revealed. */
					if (rect === void 0) return;
					if (event.clientX >= rect.left && event.clientX < rect.right && event.clientY >= rect.top && event.clientY < rect.bottom) cancelLinger();
					else armLinger();
				};
				document.addEventListener("pointermove", onMove);
				return () => {
					document.removeEventListener("pointermove", onMove);
					cancelLinger();
				};
			}, [pointerInside]);
			const buildVersion = localBuildVersion();
			const darwinDesktop = (0, _deepseek_ai_dsh_client_ui_primitives.isDarwinDesktop)();
			const toggle = /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
				label: toggleLabel,
				shortcutKeys: shortcut?.keys,
				delayMs: 500,
				side: captionTooltipSide,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
					type: "button",
					className: clsx(SidebarRoot_module_css_default.iconButton, SidebarRoot_module_css_default.toggle),
					"aria-label": toggleLabel,
					"aria-keyshortcuts": shortcut?.aria,
					onClick: () => {
						toggleSidebar();
					},
					children: [
						!wide && !windowsTitlebar && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: SidebarRoot_module_css_default.railMark,
							"aria-hidden": "true",
							children: renderSlot("sidebar.brand.mark", { size: 24 }, { fallback: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.FishLogo, { size: 24 }) })
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconPanelLeftOutlineRegular, {
							className: SidebarRoot_module_css_default.panelIcon,
							size: wide || windowsTitlebar ? 16 : 18
						}),
						!wide && renderSlot("sidebar.toggle.badge", {})
					]
				})
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				ref: column,
				className: clsx(SidebarRoot_module_css_default.root, !wide && SidebarRoot_module_css_default.collapsed, !wide && everWide.current && SidebarRoot_module_css_default.railIn, collapsed && wide && SidebarRoot_module_css_default.fading, !pointerInside && SidebarRoot_module_css_default.quietBars),
				style: wide ? { width: collapsed ? lastWideWidth.current : width } : void 0,
				onPointerEnter: () => {
					cancelLinger();
					setPointerInside(true);
				},
				onPointerLeave: () => {
					armLinger();
				},
				children: [
					darwinDesktop && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						className: SidebarRoot_module_css_default.topStrip,
						"data-window-drag": true,
						children: toggle
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: SidebarRoot_module_css_default.logoRow,
						"data-window-drag": true,
						children: [wide && (() => {
							const identity = /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
								className: SidebarRoot_module_css_default.brandIdentity,
								"aria-hidden": "true",
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
									className: SidebarRoot_module_css_default.brandMark,
									children: renderSlot("sidebar.brand.mark", { size: 24 }, { fallback: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.FishLogo, { size: 24 }) })
								}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
									className: SidebarRoot_module_css_default.brandName,
									children: renderSlot("sidebar.brand.name", {}, { fallback: buildVersion === void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
										className: SidebarRoot_module_css_default.fallbackBrandName,
										children: t("brand.localBuild")
									}) : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
										className: SidebarRoot_module_css_default.localBuildBrand,
										children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
											className: SidebarRoot_module_css_default.localBuildTitle,
											children: t("brand.localBuild")
										}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
											className: SidebarRoot_module_css_default.buildVersion,
											children: buildVersion
										})]
									}) })
								})]
							});
							return darwinDesktop ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
								className: clsx(SidebarRoot_module_css_default.brand, SidebarRoot_module_css_default.wide),
								children: identity
							}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
								label: t("session.new.label"),
								shortcutKeys: newShortcut?.keys,
								delayMs: 500,
								children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
									type: "button",
									className: clsx(SidebarRoot_module_css_default.brand, SidebarRoot_module_css_default.wide),
									"aria-label": t("session.new.label"),
									"aria-keyshortcuts": newShortcut?.aria,
									onClick: () => {
										startSession();
									},
									children: identity
								})
							});
						})(), !darwinDesktop && toggle]
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
						label: t("session.new.label"),
						shortcutKeys: newShortcut?.keys,
						delayMs: 500,
						side: captionTooltipSide,
						disabled: wide,
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
							type: "button",
							className: SidebarRoot_module_css_default.newSession,
							"aria-label": t("session.new.label"),
							"aria-keyshortcuts": newShortcut?.aria,
							onClick: () => {
								startSession();
							},
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
								className: SidebarRoot_module_css_default.newSessionLabelMask,
								children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
									className: SidebarRoot_module_css_default.newSessionContent,
									children: [wide ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconNewChatOutlineMedium, { size: 14 }) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconNewChatOutlineRegular, { size: windowsTitlebar ? 16 : 18 }), wide && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
										className: clsx(SidebarRoot_module_css_default.newSessionLabel, SidebarRoot_module_css_default.wide),
										children: t("session.new")
									})]
								})
							}), wide && newShortcut !== void 0 && newShortcut.keys.length > 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
								className: SidebarRoot_module_css_default.newSessionShortcut,
								"aria-hidden": "true",
								children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.ShortcutKeys, { keys: newShortcut.keys })
							})]
						})
					}),
					panels.length > 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("nav", {
						className: SidebarRoot_module_css_default.panelList,
						"aria-label": t("panels.label"),
						children: panels.map(({ id, label }) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)(PanelRow, {
							id,
							label,
							wide,
							usePanelInfo,
							selectPanel,
							renderSlot
						}, id))
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						className: SidebarRoot_module_css_default.regionArea,
						children: renderSlot("sidebar.workspaces", {
							wide,
							expandSidebar: () => {
								if (collapsed) toggleSidebar();
							}
						})
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: SidebarRoot_module_css_default.footArea,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: SidebarRoot_module_css_default.footerActions,
							children: renderSlot("sidebar.footer.action", { wide })
						}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: SidebarRoot_module_css_default.settingsArea,
							children: renderSlot("sidebar.settings", { wide })
						})]
					})
				]
			});
		}
		//#endregion
		//#region src/client/locales.ts
		/** `sidebar` namespace dictionaries for shell controls and global panels. */
		/** Simplified Chinese dictionary (the key-set source of truth). */
		const zh = {
			"session.new": "新会话",
			"session.new.label": "新建会话",
			"toggle.open": "打开侧边栏",
			"toggle.collapse": "收起侧边栏",
			"panels.label": "全局面板"
		};
		/** English dictionary, checked complete against the zh key set. */
		const en = {
			"session.new": "New Session",
			"session.new.label": "New session",
			"toggle.open": "Open sidebar",
			"toggle.collapse": "Collapse sidebar",
			"panels.label": "Global panels"
		};
		//#endregion
		//#region src/client/index.ts
		/** Dictionary namespace owned by this plugin. */
		const NS = "sidebar";
		/** Services required by the sidebar plugin. */
		const inject = [
			"slots",
			"layout",
			"uiWorkspace",
			"locale",
			"shortcuts"
		];
		/** Registers the sidebar shell and its service callbacks.
		* @param ctx - Client root context.
		*/
		function apply(ctx) {
			const workspaceNavigation = ctx.get("uiWorkspace");
			ctx.effect(() => ctx.locale.register(NS, {
				zh,
				en
			}), "ui-sidebar: dictionaries");
			const panels = (0, _deepseek_ai_dsh_client_store.createSnapshotStore)([]);
			const syncPanels = () => {
				const next = ctx.slots.entriesOfSlot("sidebar.panellist").map(({ options }) => {
					const id = options.id;
					return {
						id,
						order: options.order ?? 0,
						label: (0, _deepseek_ai_dsh_client_ui_slots.resolveSlotLabel)(options.label) ?? id
					};
				}).sort((a, b) => a.order - b.order);
				const previous = panels.getSnapshot();
				if (previous.length === next.length && previous.every((panel, index) => {
					const candidate = next[index];
					return panel.id === candidate.id && panel.order === candidate.order && panel.label === candidate.label;
				})) return;
				panels.set(next);
			};
			ctx.effect(() => ctx.slots.subscribe("sidebar.panellist", syncPanels), "ui-sidebar: panel entries");
			ctx.effect(() => ctx.locale.subscribe(syncPanels), "ui-sidebar: panel labels");
			const injectProps = () => ({
				startSession: (workspaceId) => {
					workspaceNavigation.startSession(workspaceId);
				},
				toggleSidebar: () => {
					ctx.layout.toggleSidebar();
				},
				selectPanel: (id) => {
					ctx.layout.selectPanel(id);
				},
				hooks: {
					panels,
					shortcuts: ctx.shortcuts.catalog
				}
			});
			ctx.slots.inject("sidebar", () => ctx.slots.register({
				name: "sidebar",
				locale: NS,
				children: {
					"sidebar.brand.mark": {
						kind: "single",
						scope: "root"
					},
					"sidebar.brand.name": {
						kind: "single",
						scope: "root"
					},
					"sidebar.toggle.badge": {
						kind: "single",
						scope: "root"
					},
					"sidebar.panellist": {
						kind: "list",
						scope: "root"
					},
					"sidebar.workspaces": {
						kind: "single",
						scope: "root"
					},
					"sidebar.settings": {
						kind: "single",
						scope: "root"
					},
					"sidebar.footer.action": {
						kind: "list",
						scope: "root"
					}
				},
				inject: injectProps
			}, SidebarRoot));
			ctx.slots.inject("shell.leading", () => ctx.slots.register({
				name: "shell.leading",
				locale: NS,
				inject: injectProps
			}, HeaderLeadingControls));
			syncPanels();
		}
		//#endregion
		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map