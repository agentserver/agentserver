window.__ModuleLoader__.load({
	id: "@deepseek-ai/dsh-client-ui-subagent",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		Object.defineProperty(exports, Symbol.toStringTag, { value: "Module" });
		let react = require("react");
		let react_dom = require("react-dom");
		let _deepseek_ai_dsh_client_ui_primitives = require("@deepseek-ai/dsh-client-ui-primitives");
		let react_jsx_runtime = require("react/jsx-runtime");
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-subagent/src/client/SubagentHeaderLineage.module.css.mjs
		const css$2 = ".CAvkGW_root{align-items:center;gap:10px;min-width:0;display:inline-flex;position:relative}.CAvkGW_switcherRoot{min-width:0;margin-left:6px}.CAvkGW_trigger,.CAvkGW_switcherTrigger{border-radius:var(--dsw-radius-sm);min-height:28px;color:var(--dsw-alias-label-tertiary);cursor:pointer;background:0 0;border:0;align-items:center;padding:3px 2px;font-size:12px;line-height:18px;display:inline-flex}.CAvkGW_trigger{gap:4px}.CAvkGW_switcherTrigger{min-width:0;max-width:244px;color:var(--dsw-alias-label-primary);gap:4px;font-weight:500}.CAvkGW_ancestorSwitcherTrigger{color:var(--dsw-alias-label-tertiary);font-weight:400}.CAvkGW_switcherTitle{text-overflow:ellipsis;white-space:nowrap;flex:1;min-width:0;overflow:hidden}.CAvkGW_switcherTrigger svg{flex:none}.CAvkGW_activitySlot{flex:none;justify-content:center;align-items:center;width:14px;height:14px;display:inline-flex}.CAvkGW_trigger:hover,.CAvkGW_trigger:focus-visible,.CAvkGW_switcherTrigger:hover,.CAvkGW_switcherTrigger:focus-visible{color:var(--dsw-alias-label-primary)}.CAvkGW_ancestorSwitcherTrigger:hover,.CAvkGW_ancestorSwitcherTrigger:focus-visible{color:var(--dsw-alias-label-tertiary)}.CAvkGW_trigger svg,.CAvkGW_switcherTrigger svg{transition:transform .12s}.CAvkGW_triggerOpen{transform:rotate(180deg)}.CAvkGW_menu{z-index:100;box-sizing:border-box;border-radius:var(--dsw-radius-lg);--dsh-scrollbar-thumb:var(--dsw-alias-scrollbar-bg-l2);--dsh-scrollbar-thumb-hover:var(--dsw-alias-scrollbar-hover-l2);--dsw-elevation-stroke-color:var(--dsw-alias-border-l1);width:336px;max-width:min(400px,100vw - 32px);max-height:min(560px,100vh - 140px);box-shadow:var(--dsw-elevation-prominent);flex-direction:column;padding:3px;display:flex;position:fixed;overflow:hidden}.CAvkGW_menu:before{content:\"\";z-index:-1;border-radius:inherit;background:var(--dsw-specific-menu);backdrop-filter:var(--dsw-menu-backdrop-filter);position:absolute;inset:0}.CAvkGW_menuBody{flex-direction:column;flex:auto;min-height:0;display:flex;overflow:auto}.CAvkGW_node{min-width:0;position:relative}.CAvkGW_menuBody>.CAvkGW_node{margin-left:-2px}.CAvkGW_row{box-sizing:border-box;border-radius:var(--dsw-radius-lg);width:100%;min-height:44px;color:var(--dsw-alias-label-primary);text-align:left;cursor:pointer;background:0 0;border:0;outline:none;align-items:flex-start;gap:6px;padding:6px 7px 6px 9px;font-size:12px;line-height:17px;display:flex;position:relative}.CAvkGW_row:hover>.CAvkGW_clickarea,.CAvkGW_row:focus-visible>.CAvkGW_clickarea{background:var(--dsw-alias-interactive-bg-hover)}.CAvkGW_clickarea{box-sizing:border-box;border-radius:var(--dsw-radius-lg);flex:1;align-self:stretch;align-items:flex-start;gap:6px;min-width:0;margin:-6px -7px;padding:6px 7px;display:flex}.CAvkGW_rowActivitySlot{flex:none;justify-content:center;align-items:center;width:14px;height:17px;display:inline-flex}.CAvkGW_disclosure,.CAvkGW_disclosureSpace{flex:none;width:14px;height:17px}.CAvkGW_disclosure{color:var(--dsw-alias-label-tertiary);cursor:pointer;background:0 0;border:0;justify-content:center;align-items:center;padding:0;transition:transform .12s;display:inline-flex}.CAvkGW_disclosure svg{width:12px;height:12px}.CAvkGW_disclosure:hover{color:var(--dsw-alias-label-primary)}.CAvkGW_disclosureOpen{transform:rotate(90deg)}.CAvkGW_content{flex-direction:column;flex:1;min-width:0;display:flex}.CAvkGW_label,.CAvkGW_summary{text-overflow:ellipsis;white-space:nowrap;overflow:hidden}.CAvkGW_label{color:inherit;font-weight:400}.CAvkGW_currentLabel{font-weight:600}.CAvkGW_summary,.CAvkGW_metrics{color:var(--dsw-alias-label-tertiary);font-size:10px;line-height:15px}.CAvkGW_metrics{font-variant-numeric:tabular-nums;text-align:right;white-space:nowrap;flex:none;grid-template-rows:17px 15px;display:grid}.CAvkGW_metricToken{grid-row:1;line-height:17px}.CAvkGW_metricDuration{grid-row:2}.CAvkGW_sidebarButton{border-radius:var(--dsw-radius-sm);width:28px;height:28px;color:var(--dsw-alias-label-tertiary);cursor:pointer;background:0 0;border:0;flex:none;justify-content:center;align-items:center;margin:4px 0;padding:6px;display:inline-flex}.CAvkGW_sidebarButton:hover,.CAvkGW_sidebarButton:focus-visible{background:var(--dsw-alias-interactive-bg-hover);color:var(--dsw-alias-label-primary)}.CAvkGW_children{margin-left:16px;padding-left:3px;position:relative}.CAvkGW_children:before,.CAvkGW_children>.CAvkGW_node:before{content:\"\";border-left:.5px solid var(--dsw-alias-border-l2);position:absolute;left:0}.CAvkGW_children:before{height:23px;top:-23px}.CAvkGW_children[aria-busy=true]:before{content:none}.CAvkGW_children>.CAvkGW_node:before{top:0;bottom:0;left:-3px}.CAvkGW_children>.CAvkGW_node:last-child:before{height:15px;bottom:auto}.CAvkGW_children>.CAvkGW_node>.CAvkGW_row:before{content:\"\";border-top:.5px solid var(--dsw-alias-border-l2);width:12px;position:absolute;top:14px;left:-3px}.CAvkGW_notice,.CAvkGW_error{color:var(--dsw-alias-label-tertiary);padding:8px 10px;font-size:11px;line-height:16px}.CAvkGW_error{color:var(--dsw-alias-state-error-primary);justify-content:space-between;align-items:center;gap:10px;display:flex}.CAvkGW_refresh{border-radius:var(--dsw-radius-sm);color:inherit;cursor:pointer;background:0 0;border:0;flex:none;align-items:center;gap:3px;padding:3px 5px;display:inline-flex}.CAvkGW_refresh svg{width:12px;height:12px}.CAvkGW_refresh:hover{background:var(--dsw-alias-interactive-bg-hover)}";
		const tagId$2 = "@deepseek-ai/dsh-client-ui-subagent/SubagentHeaderLineage.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$2) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-subagent";
			tag.dataset.pluginCss = tagId$2;
			tag.textContent = css$2;
			document.head.appendChild(tag);
		}
		var SubagentHeaderLineage_module_css_default = {
			"activitySlot": "CAvkGW_activitySlot",
			"ancestorSwitcherTrigger": "CAvkGW_ancestorSwitcherTrigger",
			"children": "CAvkGW_children",
			"clickarea": "CAvkGW_clickarea",
			"content": "CAvkGW_content",
			"currentLabel": "CAvkGW_currentLabel",
			"disclosure": "CAvkGW_disclosure",
			"disclosureOpen": "CAvkGW_disclosureOpen",
			"disclosureSpace": "CAvkGW_disclosureSpace",
			"error": "CAvkGW_error",
			"label": "CAvkGW_label",
			"menu": "CAvkGW_menu",
			"menuBody": "CAvkGW_menuBody",
			"metricDuration": "CAvkGW_metricDuration",
			"metricToken": "CAvkGW_metricToken",
			"metrics": "CAvkGW_metrics",
			"node": "CAvkGW_node",
			"notice": "CAvkGW_notice",
			"refresh": "CAvkGW_refresh",
			"root": "CAvkGW_root",
			"row": "CAvkGW_row",
			"rowActivitySlot": "CAvkGW_rowActivitySlot",
			"sidebarButton": "CAvkGW_sidebarButton",
			"summary": "CAvkGW_summary",
			"switcherRoot": "CAvkGW_switcherRoot",
			"switcherTitle": "CAvkGW_switcherTitle",
			"switcherTrigger": "CAvkGW_switcherTrigger",
			"trigger": "CAvkGW_trigger",
			"triggerOpen": "CAvkGW_triggerOpen"
		};
		//#endregion
		//#region src/client/SubagentHeaderLineage.tsx
		function treeItems(root) {
			return root === null ? [] : Array.from(root.querySelectorAll("[role=\"treeitem\"]:not([aria-disabled=\"true\"])"));
		}
		/** Compact token count shared in shape with the conversation stats strip. */
		function formatTokens(value, t) {
			const scaled = (next) => next >= 100 ? String(Math.round(next)) : String(Math.round(next * 10) / 10);
			if (value < 1e3) return String(value);
			if (value < 1e6) return t("tokens.thousand", { value: scaled(value / 1e3) });
			return t("tokens.million", { value: scaled(value / 1e6) });
		}
		/** Sum the four disjoint durable provider-usage buckets. */
		function tokenTotal(usage) {
			return usage === void 0 ? void 0 : usage.uncachedInputTokens + usage.outputTokens + usage.cacheReadTokens + usage.cacheWriteTokens;
		}
		/** Exact whole-second active-turn duration for one catalog row. */
		function activityDuration(summary, activity, now) {
			if (summary === void 0) return void 0;
			const timing = summary.projectionValues?.subagentTiming;
			if (timing === void 0) return void 0;
			if (timing.active === void 0) return timing.settledMs;
			const end = activity === "running" ? now : timing.active.through;
			return timing.settledMs + Math.max(0, end - timing.active.since);
		}
		function splitDuration(ms) {
			const totalSeconds = Math.floor(Math.max(0, ms) / 1e3);
			const totalMinutes = Math.floor(totalSeconds / 60);
			const totalHours = Math.floor(totalMinutes / 60);
			return {
				seconds: totalSeconds % 60,
				minutes: totalMinutes % 60,
				hours: totalHours % 24,
				days: Math.floor(totalHours / 24),
				totalMinutes,
				totalHours
			};
		}
		/** Format a duration with decreasing visual precision at larger scales. */
		function formatDuration(ms, t) {
			const { seconds, minutes, hours, days, totalMinutes, totalHours } = splitDuration(ms);
			if (days >= 365) {
				const years = Math.floor(days / 365);
				const months = Math.floor(days % 365 / 30);
				return months === 0 ? t("duration.years", { years }) : t("duration.yearsMonths", {
					years,
					months
				});
			}
			if (days >= 30) {
				const months = Math.floor(days / 30);
				const remainingDays = days % 30;
				return remainingDays === 0 ? t("duration.months", { months }) : t("duration.monthsDays", {
					months,
					days: remainingDays
				});
			}
			if (days > 0) return hours === 0 ? t("duration.days", { days }) : t("duration.daysHours", {
				days,
				hours
			});
			if (totalHours > 0) return t("duration.hours", {
				hours: totalHours,
				minutes: String(minutes).padStart(2, "0"),
				seconds: String(seconds).padStart(2, "0")
			});
			if (totalMinutes > 0) return t("duration.minutes", {
				minutes: totalMinutes,
				seconds: String(seconds).padStart(2, "0")
			});
			return t("duration.seconds", { seconds });
		}
		/** Preserve exact whole seconds for hover and accessible naming. */
		function formatExactDuration(ms, t) {
			const { seconds, minutes, hours, days } = splitDuration(ms);
			return days === 0 ? formatDuration(ms, t) : t("duration.exactDays", {
				days,
				hours: String(hours).padStart(2, "0"),
				minutes: String(minutes).padStart(2, "0"),
				seconds: String(seconds).padStart(2, "0")
			});
		}
		function SubagentSwitcherIcon() {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("svg", {
				width: "16",
				height: "16",
				viewBox: "0 0 20 20",
				fill: "none",
				"aria-hidden": "true",
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
					d: "M5.99951 12.7L8.95546 14.9478C9.40011 15.2859 9.62244 15.455 9.87526 15.488C9.95774 15.4988 10.0413 15.4988 10.1238 15.488C10.3766 15.455 10.5989 15.2859 11.0436 14.9478L13.9995 12.7",
					stroke: "currentColor",
					strokeWidth: "1.5"
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
					d: "M13.9995 7.7417L11.0436 5.49387C10.5989 5.15574 10.3766 4.98668 10.1238 4.95362C10.0413 4.94283 9.95775 4.94283 9.87527 4.95362C9.62245 4.98668 9.40012 5.15574 8.95547 5.49387L5.99952 7.7417",
					stroke: "currentColor",
					strokeWidth: "1.5"
				})]
			});
		}
		/** Render catalog loading without inventing child membership. */
		function CatalogLoadingRows({ t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: SubagentHeaderLineage_module_css_default.notice,
				children: t("loading.label")
			});
		}
		/** A child becomes a known leaf only after its own authoritative catalog loads empty. */
		function isKnownLeaf(catalog) {
			return catalog?.state === "ready" && catalog.entries.length === 0;
		}
		/** Render one catalog level and recurse only through explicitly expanded rows. */
		function CatalogRows({ parentSessionId, currentSessionId, catalog, catalogs, summaries, expanded, level, openChild, openChildAside, refreshProjection, toggleBranch, closeCatalog, t }) {
			const [now, setNow] = (0, react.useState)(() => Date.now());
			const running = catalog.entries.some((entry) => entry.activity === "running");
			(0, react.useEffect)(() => {
				if (!running) return;
				const timer = setInterval(() => {
					setNow(Date.now());
				}, 1e3);
				return () => {
					clearInterval(timer);
				};
			}, [running]);
			const emptyLoading = catalog.state === "loading" && catalog.entries.length === 0;
			const reserveDisclosure = catalog.entries.some((entry) => !isKnownLeaf(catalogs[entry.id]));
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [
				emptyLoading && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(CatalogLoadingRows, { t }),
				catalog.state === "error" && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: SubagentHeaderLineage_module_css_default.error,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: catalog.error?.message ?? t("load.error") }), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
						type: "button",
						className: SubagentHeaderLineage_module_css_default.refresh,
						onClick: () => {
							refreshProjection(parentSessionId);
						},
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconRefreshOutlineRegular, { size: 14 }), t("retry")]
					})]
				}),
				catalog.entries.map((entry) => {
					const childCatalog = catalogs[entry.id];
					const isCurrent = entry.id === currentSessionId;
					const isExpanded = expanded.has(entry.id);
					const knownLeaf = isKnownLeaf(childCatalog);
					const childLoading = childCatalog === void 0 || childCatalog.state === "loading" && childCatalog.entries.length === 0;
					const summary = summaries[entry.id];
					const label = entry.label ?? entry.id;
					const mode = entry.mode === "unknown" ? t("mode.unknown") : entry.mode === "one-shot" ? t("mode.oneShot") : t("mode.continuable");
					const completed = entry.activity === "inactive" && summary?.projectionValues?.subagentTiming?.lastTurnCompleted === true;
					const activity = entry.activity === "running" ? t("activity.running") : completed ? t("activity.completed") : t("activity.inactive");
					const secondary = [
						summary?.title,
						mode,
						activity
					].filter((value) => value !== void 0).join(" · ");
					const totalTokens = tokenTotal(summary?.projectionValues?.tokenUsage);
					const durationMs = activityDuration(summary, entry.activity, now);
					const tokenMetric = totalTokens === void 0 ? void 0 : t("tokens.total", { value: formatTokens(totalTokens, t) });
					const durationMetric = durationMs === void 0 ? void 0 : {
						compact: formatDuration(durationMs, t),
						exact: formatExactDuration(durationMs, t)
					};
					const metrics = [tokenMetric, durationMetric?.exact].filter((value) => value !== void 0).join(" · ");
					const open = () => {
						openChild({
							parentSessionId,
							childSessionId: entry.id,
							mode: entry.mode
						});
						closeCatalog();
					};
					const openAside = (event) => {
						event.preventDefault();
						event.stopPropagation();
						openChildAside({
							parentSessionId,
							childSessionId: entry.id,
							mode: entry.mode
						});
						closeCatalog();
					};
					const handleKey = (event) => {
						if (event.key === "Enter" || event.key === " ") {
							event.preventDefault();
							event.stopPropagation();
							open();
						} else if (event.key === "ArrowRight" && !knownLeaf && !isExpanded || event.key === "ArrowLeft" && isExpanded) {
							event.preventDefault();
							event.stopPropagation();
							toggleBranch(entry.id);
						}
					};
					const toggle = (event) => {
						event.preventDefault();
						event.stopPropagation();
						toggleBranch(entry.id);
					};
					return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: SubagentHeaderLineage_module_css_default.node,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							role: "treeitem",
							tabIndex: 0,
							"aria-level": level,
							"aria-current": isCurrent || void 0,
							"aria-label": [
								label,
								secondary,
								metrics
							].filter((value) => value !== "").join(" "),
							...knownLeaf ? {} : { "aria-expanded": isExpanded },
							className: SubagentHeaderLineage_module_css_default.row,
							onClick: open,
							onKeyDown: handleKey,
							children: [knownLeaf ? reserveDisclosure && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { className: SubagentHeaderLineage_module_css_default.disclosureSpace }) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
								type: "button",
								tabIndex: -1,
								className: `${SubagentHeaderLineage_module_css_default.disclosure} ${isExpanded ? SubagentHeaderLineage_module_css_default.disclosureOpen : ""}`,
								"aria-label": t(isExpanded ? "branch.collapse" : "branch.expand", { label }),
								onClick: toggle,
								children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronRightOutlineRegular, {})
							}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
								className: SubagentHeaderLineage_module_css_default.clickarea,
								children: [
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
										className: SubagentHeaderLineage_module_css_default.rowActivitySlot,
										children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, { state: entry.activity === "running" ? "ongoing" : completed ? "done" : "idle" })
									}),
									/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
										className: SubagentHeaderLineage_module_css_default.content,
										children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
											className: `${SubagentHeaderLineage_module_css_default.label} ${isCurrent ? SubagentHeaderLineage_module_css_default.currentLabel : ""}`,
											children: label
										}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
											className: SubagentHeaderLineage_module_css_default.summary,
											children: secondary
										})]
									}),
									metrics !== "" && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
										className: SubagentHeaderLineage_module_css_default.metrics,
										children: [tokenMetric !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
											className: SubagentHeaderLineage_module_css_default.metricToken,
											children: tokenMetric
										}), durationMetric !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
											className: SubagentHeaderLineage_module_css_default.metricDuration,
											title: t("duration.exactTitle", { duration: durationMetric.exact }),
											children: durationMetric.compact
										})]
									}),
									!isCurrent && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
										label: t("open.sidebar"),
										side: "bottom",
										align: "end",
										children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
											type: "button",
											className: SubagentHeaderLineage_module_css_default.sidebarButton,
											"aria-label": t("open.sidebar.aria", { label }),
											onClick: openAside,
											onKeyDown: (event) => {
												event.stopPropagation();
											},
											children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronRightOutlineRegular, {})
										})
									})
								]
							})]
						}), isExpanded && !knownLeaf && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							role: "group",
							className: SubagentHeaderLineage_module_css_default.children,
							"aria-busy": childLoading || void 0,
							children: childCatalog === void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(CatalogLoadingRows, { t }) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(CatalogRows, {
								parentSessionId: entry.id,
								currentSessionId,
								catalog: childCatalog,
								catalogs,
								summaries,
								expanded,
								level: level + 1,
								openChild,
								openChildAside,
								refreshProjection,
								toggleBranch,
								closeCatalog,
								t
							})
						})]
					}, entry.id);
				})
			] });
		}
		const MENU_VIEWPORT_MARGIN = 16;
		/** Place a portaled catalog below its trigger without crossing the viewport edge. */
		function catalogMenuPosition(trigger) {
			const rect = trigger.getBoundingClientRect();
			const width = Math.min(336, window.innerWidth - MENU_VIEWPORT_MARGIN * 2);
			return {
				top: rect.bottom + 5,
				left: Math.min(Math.max(MENU_VIEWPORT_MARGIN, rect.left), window.innerWidth - width - MENU_VIEWPORT_MARGIN)
			};
		}
		/** One trigger-plus-tree dropdown over the catalog rooted at `rootSessionId`. */
		function CatalogDropdown({ rootSessionId, currentSessionId, displayTitle, openTitle, variant, useSessions, useSessionStatus, openChild, openChildAside, refreshProjection, t }) {
			const ancestorSwitcher = variant === "switcher" && openTitle !== void 0;
			const projections = useSessions((state) => state.projectionsBySession);
			const summaries = useSessions((state) => state.byId);
			const statuses = useSessionStatus((value) => value);
			const catalogs = (0, react.useMemo)(() => Object.fromEntries(Object.entries(projections).map(([id, snapshot]) => [id, {
				state: snapshot.state === "idle" ? snapshot.values.subagentCatalog === void 0 ? "loading" : "ready" : snapshot.state,
				error: snapshot.error,
				entries: (snapshot.values.subagentCatalog ?? []).map((entry) => ({
					...entry,
					activity: (statuses.get(entry.id)?.running ?? summaries[entry.id]?.running) === true ? "running" : "inactive"
				}))
			}])), [
				projections,
				summaries,
				statuses
			]);
			const catalog = catalogs[rootSessionId];
			const [open, setOpen] = (0, react.useState)(false);
			const [menuPosition, setMenuPosition] = (0, react.useState)();
			const [expanded, setExpanded] = (0, react.useState)(() => /* @__PURE__ */ new Set());
			const rootRef = (0, react.useRef)(null);
			const triggerRef = (0, react.useRef)(null);
			const menuRef = (0, react.useRef)(null);
			const hoverOpenTimer = (0, react.useRef)(void 0);
			const hoverCloseTimer = (0, react.useRef)(void 0);
			const pinnedRef = (0, react.useRef)(false);
			const currentEntry = currentSessionId === void 0 ? void 0 : catalog?.entries.find((entry) => entry.id === currentSessionId);
			const switcherDisplayTitle = currentEntry !== void 0 ? currentEntry.label ?? currentEntry.id : displayTitle;
			const directChildren = catalog?.entries ?? [];
			const directCount = directChildren.length;
			const runningCount = directChildren.filter((entry) => entry.activity === "running").length;
			const totalCountKey = directCount === 1 ? "count.total.one" : "count.total.other";
			const runningCountKey = runningCount === 1 ? "count.running.one" : "count.running.other";
			const presentedCatalog = catalog ?? (variant === "switcher" ? {
				entries: [],
				state: "loading",
				error: null
			} : void 0);
			const cancelHoverClose = () => {
				if (hoverCloseTimer.current === void 0) return;
				clearTimeout(hoverCloseTimer.current);
				hoverCloseTimer.current = void 0;
			};
			const cancelHoverOpen = () => {
				if (hoverOpenTimer.current === void 0) return;
				clearTimeout(hoverOpenTimer.current);
				hoverOpenTimer.current = void 0;
			};
			const changeOpen = (next, restoreFocus = false) => {
				cancelHoverOpen();
				cancelHoverClose();
				if (next) {
					const trigger = triggerRef.current;
					/* v8 ignore next -- a queued callback can outlive the trigger */
					if (trigger === null) return;
					setOpen(true);
					setMenuPosition(catalogMenuPosition(trigger));
				} else {
					pinnedRef.current = false;
					setOpen(false);
					setMenuPosition(void 0);
					setExpanded(/* @__PURE__ */ new Set());
				}
				if (restoreFocus) queueMicrotask(() => {
					triggerRef.current?.focus();
				});
			};
			const scheduleHoverOpen = () => {
				cancelHoverOpen();
				cancelHoverClose();
				if (open) return;
				hoverOpenTimer.current = setTimeout(() => {
					hoverOpenTimer.current = void 0;
					changeOpen(true);
				}, 150);
			};
			const scheduleHoverClose = () => {
				cancelHoverOpen();
				cancelHoverClose();
				if (pinnedRef.current) return;
				hoverCloseTimer.current = setTimeout(() => {
					hoverCloseTimer.current = void 0;
					changeOpen(false);
				}, 120);
			};
			const closeBranch = (root) => {
				const closing = /* @__PURE__ */ new Set();
				const visit = (parentSessionId) => {
					if (closing.has(parentSessionId) || !expanded.has(parentSessionId)) return;
					closing.add(parentSessionId);
					const branch = catalogs[parentSessionId];
					for (const entry of branch?.entries ?? []) visit(entry.id);
				};
				visit(root);
				setExpanded((current) => new Set([...current].filter((id) => !closing.has(id))));
			};
			const toggleBranch = (childSessionId) => {
				if (expanded.has(childSessionId)) {
					closeBranch(childSessionId);
					return;
				}
				setExpanded((current) => new Set(current).add(childSessionId));
				refreshProjection(childSessionId);
			};
			(0, react.useEffect)(() => {
				if (!open) return;
				const closeOutside = (event) => {
					if (event.target instanceof Node && !rootRef.current?.contains(event.target) && !menuRef.current?.contains(event.target)) changeOpen(false);
				};
				document.addEventListener("pointerdown", closeOutside);
				return () => {
					document.removeEventListener("pointerdown", closeOutside);
				};
			}, [open]);
			(0, react.useEffect)(() => {
				if (!open) return;
				const placeMenu = () => {
					const trigger = triggerRef.current;
					/* v8 ignore next -- native resize or scroll can outlive the trigger */
					if (trigger === null) return;
					setMenuPosition(catalogMenuPosition(trigger));
				};
				window.addEventListener("resize", placeMenu);
				document.addEventListener("scroll", placeMenu, true);
				return () => {
					window.removeEventListener("resize", placeMenu);
					document.removeEventListener("scroll", placeMenu, true);
				};
			}, [open]);
			(0, react.useEffect)(() => () => {
				cancelHoverOpen();
				cancelHoverClose();
			}, []);
			const visible = presentedCatalog !== void 0 && (variant === "switcher" || presentedCatalog.state === "error" || presentedCatalog.entries.length > 0);
			(0, react.useEffect)(() => {
				if (visible) return;
				cancelHoverOpen();
				cancelHoverClose();
				if (!open) return;
				pinnedRef.current = false;
				setOpen(false);
				setExpanded(/* @__PURE__ */ new Set());
			}, [visible, open]);
			if (!visible) return null;
			const focusAt = (index) => {
				const items = treeItems(menuRef.current);
				if (items.length === 0) return;
				items[(index + items.length) % items.length]?.focus();
			};
			const navigate = (event) => {
				const items = treeItems(menuRef.current);
				const index = items.indexOf(document.activeElement);
				if (event.key === "Escape") {
					event.preventDefault();
					changeOpen(false, true);
				} else if (event.key === "Home") {
					event.preventDefault();
					focusAt(0);
				} else if (event.key === "End") {
					event.preventDefault();
					focusAt(items.length - 1);
				} else if (event.key === "ArrowDown") {
					event.preventDefault();
					focusAt(index + 1);
				} else if (event.key === "ArrowUp") {
					event.preventDefault();
					focusAt(index < 0 ? items.length - 1 : index - 1);
				}
			};
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: `${SubagentHeaderLineage_module_css_default.root} ${variant === "switcher" ? SubagentHeaderLineage_module_css_default.switcherRoot : ""}`,
				ref: rootRef,
				onKeyDown: navigate,
				onMouseLeave: scheduleHoverClose,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
					ref: triggerRef,
					onMouseEnter: scheduleHoverOpen,
					type: "button",
					className: variant === "switcher" ? `${SubagentHeaderLineage_module_css_default.switcherTrigger} ${ancestorSwitcher ? SubagentHeaderLineage_module_css_default.ancestorSwitcherTrigger : ""}` : SubagentHeaderLineage_module_css_default.trigger,
					"aria-haspopup": "tree",
					"aria-expanded": open,
					"aria-label": variant === "switcher" ? t("switcher.aria", { title: switcherDisplayTitle }) : t(runningCount > 0 ? runningCountKey : totalCountKey, { count: runningCount > 0 ? runningCount : directCount }),
					onClick: openTitle === void 0 ? () => {
						cancelHoverOpen();
						cancelHoverClose();
						pinnedRef.current = true;
						if (!open) changeOpen(true);
					} : () => {
						cancelHoverOpen();
						if (open) changeOpen(false);
						openTitle();
					},
					onKeyDown: (event) => {
						if (event.key !== "ArrowDown") return;
						event.preventDefault();
						if (!open) changeOpen(true);
						queueMicrotask(() => {
							focusAt(0);
						});
					},
					children: [variant === "switcher" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: SubagentHeaderLineage_module_css_default.switcherTitle,
						children: switcherDisplayTitle
					}) : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [runningCount > 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: SubagentHeaderLineage_module_css_default.activitySlot,
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, { state: "ongoing" })
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: SubagentHeaderLineage_module_css_default.count,
						children: t(totalCountKey, { count: directCount })
					})] }), variant === "switcher" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(SubagentSwitcherIcon, {}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronDownOutlineRegular, { className: open ? SubagentHeaderLineage_module_css_default.triggerOpen : void 0 })]
				}), open && (0, react_dom.createPortal)(/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
					ref: menuRef,
					className: SubagentHeaderLineage_module_css_default.menu,
					style: menuPosition,
					onMouseEnter: cancelHoverClose,
					onMouseLeave: scheduleHoverClose,
					children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						className: SubagentHeaderLineage_module_css_default.menuBody,
						role: "tree",
						"aria-label": t("tree.aria"),
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(CatalogRows, {
							parentSessionId: rootSessionId,
							currentSessionId,
							catalog: presentedCatalog,
							catalogs,
							summaries,
							expanded,
							level: 1,
							openChild,
							openChildAside,
							refreshProjection,
							toggleBranch,
							closeCatalog: () => {
								changeOpen(false);
							},
							t
						})
					})
				}), document.body)]
			});
		}
		/**
		* Session-header catalog action for root sessions: the descendant count and
		* its dropdown at the start of the header actions band. Child sessions render nothing
		* here — their breadcrumb switcher in the lineage slot owns the same
		* navigation.
		* @param props - Session standard props plus the catalog actions and translator.
		* @returns The count dropdown, or null on a child session.
		*/
		function SubagentCatalogAction({ sessionId, useSessions, useSessionStatus, openChild, openChildAside, refreshProjection, t }) {
			if (useSessions((state) => state.byId[sessionId]?.origin === "subagent")) return null;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(CatalogDropdown, {
				rootSessionId: sessionId,
				variant: "count",
				useSessions,
				useSessionStatus,
				openChild,
				openChildAside,
				refreshProjection,
				t
			}, sessionId);
		}
		/**
		* Render one breadcrumb title together with its subagent navigation.
		* @param props - Breadcrumb title, session standard props, and catalog actions.
		* @returns A title-and-chevron sibling switcher, or nothing on a root session.
		*/
		function SubagentHeaderLineage({ lineageSessionId, displayTitle, openTitle, useSessions, useSession, useSessionStatus, openChild, openChildAside, refreshProjection, t }) {
			const address = useSession((session) => session.subagent?.address);
			const parentId = useSessions((state) => {
				if (address?.childSessionId === lineageSessionId) return address.parentSessionId;
				for (const [parentId, snapshot] of Object.entries(state.projectionsBySession)) if (snapshot.values.subagentCatalog?.some((entry) => entry.id === lineageSessionId)) return parentId;
			});
			const shared = {
				useSessions,
				useSessionStatus,
				openChild,
				openChildAside,
				refreshProjection,
				t
			};
			if (parentId === void 0) return null;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(CatalogDropdown, {
				rootSessionId: parentId,
				currentSessionId: lineageSessionId,
				variant: "switcher",
				displayTitle,
				...openTitle === void 0 ? {} : { openTitle },
				...shared
			}, lineageSessionId), openTitle === void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(CatalogDropdown, {
				rootSessionId: lineageSessionId,
				variant: "count",
				...shared
			}, lineageSessionId)] });
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-subagent/src/client/SubagentReadOnlyComposer.module.css.mjs
		const css$1 = ".WpS-lW_frame{border:.5px solid var(--dsw-alias-border-l4);border-radius:var(--dsw-radius-lg);background:var(--dsw-alias-bg-layer-1);min-height:54px;color:var(--dsw-alias-label-tertiary);justify-content:center;align-items:center;gap:8px;margin:0 24px 20px;padding:10px 16px;font-size:13px;line-height:20px;display:flex}.WpS-lW_frame strong{color:var(--dsw-alias-label-primary);font-weight:510}";
		const tagId$1 = "@deepseek-ai/dsh-client-ui-subagent/SubagentReadOnlyComposer.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$1) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-subagent";
			tag.dataset.pluginCss = tagId$1;
			tag.textContent = css$1;
			document.head.appendChild(tag);
		}
		var SubagentReadOnlyComposer_module_css_default = { "frame": "WpS-lW_frame" };
		//#endregion
		//#region src/client/SubagentReadOnlyComposer.tsx
		/**
		* Explain why the normal composer is unavailable for an addressed child.
		* @param props - selector-owned read-only reason plus standard slot props.
		* @returns A read-only composer replacement.
		*/
		function SubagentReadOnlyComposer({ matched, t }) {
			const oneShot = matched.reason === "one-shot";
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: SubagentReadOnlyComposer_module_css_default.frame,
				role: "status",
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("strong", { children: t(oneShot ? "readonly.oneShot.title" : "readonly.title") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: t(matched.reason === "unknown" ? "readonly.unknown.body" : oneShot ? "readonly.oneShot.body" : "readonly.body") })]
			});
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-subagent/src/client/sidebar-chat/SidebarChat.module.css.mjs
		const css = ".oXd0za_root{width:100%;min-width:0;height:100%;min-height:0;display:flex}";
		const tagId = "@deepseek-ai/dsh-client-ui-subagent/SidebarChat.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-subagent";
			tag.dataset.pluginCss = tagId;
			tag.textContent = css;
			document.head.appendChild(tag);
		}
		var SidebarChat_module_css_default = { "root": "oXd0za_root" };
		//#endregion
		//#region src/client/sidebar-chat/index.tsx
		/** Stable implementation identity for the Sidebar tab body. */
		const SUBAGENT_CHAT_ID = "@deepseek-ai/dsh-client-ui-subagent";
		/** Resource-address prefix for an embedded Session chat. */
		const SUBAGENT_CHAT_ADDRESS = "dsh-resource://subagentchat/session/";
		/**
		* Address one subagent Session together with the routing facts needed to restore it.
		* @param address - durable direct-parent subagent address.
		* @returns canonical Sidebar resource address.
		*/
		function subagentChatAddress(address) {
			const query = new URLSearchParams({
				parent: address.parentSessionId,
				mode: address.mode
			});
			return `${SUBAGENT_CHAT_ADDRESS}${encodeURIComponent(address.childSessionId)}?${query}`;
		}
		/**
		* Parse one canonical Sidebar chat resource address.
		* @param value - possible chat resource address.
		* @returns the encoded direct-parent address, or undefined for another or malformed resource.
		*/
		function parseSubagentChatAddress(value) {
			let url;
			try {
				url = new URL(value);
			} catch (_invalidUrl) {
				return;
			}
			if (url.protocol !== "dsh-resource:" || url.hostname.toLowerCase() !== "subagentchat") return void 0;
			const parts = url.pathname.split("/").filter(Boolean);
			if (parts.length !== 2 || parts[0] !== "session") return void 0;
			const parentSessionId = url.searchParams.get("parent");
			const mode = url.searchParams.get("mode");
			if (parentSessionId === null || parentSessionId === "" || mode !== "one-shot" && mode !== "continuable" && mode !== "unknown") return;
			try {
				return {
					parentSessionId,
					childSessionId: decodeURIComponent(parts[1]),
					mode
				};
			} catch (_invalidEncoding) {
				return;
			}
		}
		function waitForAbort(signal) {
			if (signal.aborted) return Promise.resolve();
			return new Promise((resolve) => {
				signal.addEventListener("abort", () => {
					resolve();
				}, { once: true });
			});
		}
		function isAbortRequested(signal) {
			return signal.aborted;
		}
		function subagentChatResourceProvider(sessions) {
			return {
				protocol: "subagentchat",
				async *open(resourceAddress, { signal }) {
					const address = parseSubagentChatAddress(resourceAddress);
					if (address === void 0) throw new Error(`ui-subagent: invalid chat resource address "${resourceAddress}"`);
					if (isAbortRequested(signal)) return;
					const reference = sessions.retain(address, {
						source: "sidebarChat",
						signal
					});
					try {
						yield {
							ok: true,
							value: {
								address,
								reference
							}
						};
						await waitForAbort(signal);
					} finally {
						reference.release();
					}
				}
			};
		}
		/** Fixed Chat selection used by an embedded Conversation occurrence. */
		function FixedChatConversationView(props) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(react_jsx_runtime.Fragment, { children: props.renderSlot("conversation.session", { view: "chat" }) });
		}
		/** Render the shared Conversation content for one explicitly provided child Session. */
		function ConversationSlotPanel({ sessionId, useSession, useConversation, useSessions, renderFactorySlot }) {
			const session = useSession((value) => value);
			const shellPhase = useConversation((value) => value).activeTargets.size > 0 || !session.blank && !session.awaitingFirstTurn || session.running ? "active" : session.promptAttempted ? "engaging" : "blank";
			const summaryBlank = useSessions((state) => state.byId[sessionId]?.blank);
			const parentAvailabilityPending = session.subagent?.address.mode === "continuable" && session.subagent.parentAvailable === void 0;
			const settling = shellPhase === "blank" && session.openState === "loading" && summaryBlank !== true || parentAvailabilityPending;
			const hero = shellPhase === "blank" && (session.openState === "open" || summaryBlank === true);
			return renderFactorySlot("conversation.content", {
				variant: "embedded",
				phase: settling ? "settling" : hero ? "hero" : "active",
				hero
			}, { slots: { views: FixedChatConversationView } });
		}
		/** Bind a chat resource's child reference around its Conversation slot. */
		function SidebarChatTab({ useResource, useTabInfo, SessionProvider, renderSlot }) {
			const { tab } = useTabInfo();
			const resource = useResource(tab.contentId);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: SidebarChat_module_css_default.root,
				"data-sidebar-chat": "",
				children: resource.value === void 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(SessionProvider, {
					session: resource.value.reference,
					children: renderSlot("sidebar.chat.conversation", {})
				})
			});
		}
		/**
		* Register the chat resource owner and its right-Sidebar presentation.
		* @param ctx - Client root carrying Sessions, resources, Slots, and Sidebar registries.
		* @param t - Chat namespace translator used for fallback tab titles.
		*/
		function registerSidebarChat(ctx, t) {
			ctx.effect(() => ctx.resources.register(subagentChatResourceProvider(ctx.sessions)), "ui-subagent: Sidebar chat resources");
			ctx.effect(() => ctx.sidebarRightTabs.register({
				id: SUBAGENT_CHAT_ID,
				kind: "subagentchat",
				patterns: [`${SUBAGENT_CHAT_ADDRESS}**`],
				priority: "builtin",
				canOpen: (address) => parseSubagentChatAddress(address) !== void 0,
				title: (address) => {
					const child = parseSubagentChatAddress(address)?.childSessionId;
					return child === void 0 ? t("sidebar.chat") : ctx.sessions.list.getSnapshot().byId[child]?.projectionValues?.subagent?.label ?? child;
				}
			}), "ui-subagent: Sidebar chat type");
			ctx.effect(() => ctx.slots.inject("sidebar.right.pane.tab", () => ctx.slots.register({
				name: "sidebar.right.pane.tab",
				key: SUBAGENT_CHAT_ID,
				children: { "sidebar.chat.conversation": {
					kind: "single",
					scope: "session"
				} }
			}, SidebarChatTab)), "ui-subagent: Sidebar chat body");
			ctx.effect(() => ctx.slots.inject("sidebar.chat.conversation", () => ctx.slots.register({ name: "sidebar.chat.conversation" }, ConversationSlotPanel)), "ui-subagent: Sidebar Conversation");
		}
		//#endregion
		//#region src/client/locales.ts
		/** `subagent` namespace dictionaries. */
		/** Dictionary namespace owned by this plugin. */
		const NS = "subagent";
		/** Simplified Chinese dictionary (the key-set source of truth). */
		const zh = {
			"duration.seconds": "{seconds}秒",
			"duration.minutes": "{minutes}分{seconds}秒",
			"duration.hours": "{hours}小时{minutes}分{seconds}秒",
			"duration.days": "{days}天",
			"duration.daysHours": "{days}天{hours}小时",
			"duration.months": "约{months}个月",
			"duration.monthsDays": "约{months}个月{days}天",
			"duration.years": "约{years}年",
			"duration.yearsMonths": "约{years}年{months}个月",
			"duration.exactDays": "{days}天{hours}小时{minutes}分{seconds}秒",
			"duration.exactTitle": "总活跃耗时：{duration}",
			"tokens.thousand": "{value}K",
			"tokens.million": "{value}M",
			"tokens.total": "{value} tok",
			"loading.label": "正在加载子智能体…",
			"load.error": "无法加载子智能体",
			"retry": "重试",
			"mode.oneShot": "一次性",
			"mode.continuable": "可继续",
			"mode.unknown": "模式未知",
			"readonly.unknown.body": "读取子会话后才能确定是否可继续。",
			"activity.running": "正在运行",
			"activity.completed": "已完成",
			"activity.inactive": "当前未运行",
			"branch.collapse": "收起 {label} 的下级子智能体",
			"branch.expand": "展开 {label} 的下级子智能体",
			"count.total.one": "{count} 个子智能体",
			"count.total.other": "{count} 个子智能体",
			"count.running.one": "{count} 个子智能体，正在运行",
			"count.running.other": "{count} 个子智能体，正在运行",
			"switcher.aria": "切换子智能体：{title}",
			"tree.aria": "子智能体会话",
			"open.sidebar": "在侧边栏打开",
			"open.sidebar.aria": "在侧边栏打开 {label}",
			"sidebar.chat": "聊天",
			"readonly.oneShot.title": "一次性子智能体记录",
			"readonly.title": "此子智能体暂时只读",
			"readonly.oneShot.body": "一次性任务不支持后续消息，可在这里查看完整执行记录。",
			"readonly.body": "父会话当前不在线，重新打开父会话后即可继续发送消息。"
		};
		/** English dictionary, key-identical to the Chinese source of truth. */
		const en = {
			"duration.seconds": "{seconds}s",
			"duration.minutes": "{minutes}m {seconds}s",
			"duration.hours": "{hours}h {minutes}m {seconds}s",
			"duration.days": "{days}d",
			"duration.daysHours": "{days}d {hours}h",
			"duration.months": "~{months}mo",
			"duration.monthsDays": "~{months}mo {days}d",
			"duration.years": "~{years}y",
			"duration.yearsMonths": "~{years}y {months}mo",
			"duration.exactDays": "{days}d {hours}h {minutes}m {seconds}s",
			"duration.exactTitle": "Total active duration: {duration}",
			"tokens.thousand": "{value}K",
			"tokens.million": "{value}M",
			"tokens.total": "{value} tok",
			"loading.label": "Loading subagents…",
			"load.error": "Unable to load subagents",
			"retry": "Retry",
			"mode.oneShot": "one-shot",
			"mode.continuable": "continuable",
			"mode.unknown": "unknown mode",
			"readonly.unknown.body": "Read the child session to determine whether it can be continued.",
			"activity.running": "running",
			"activity.completed": "completed",
			"activity.inactive": "not running",
			"branch.collapse": "Collapse {label} descendants",
			"branch.expand": "Expand {label} descendants",
			"count.total.one": "{count} subagent",
			"count.total.other": "{count} subagents",
			"count.running.one": "{count} subagent running",
			"count.running.other": "{count} subagents running",
			"switcher.aria": "Switch subagent: {title}",
			"tree.aria": "Subagent sessions",
			"open.sidebar": "Open in sidebar",
			"open.sidebar.aria": "Open {label} in sidebar",
			"sidebar.chat": "Chat",
			"readonly.oneShot.title": "One-shot subagent record",
			"readonly.title": "This subagent is read-only for now",
			"readonly.oneShot.body": "One-shot tasks do not accept follow-ups; review the full execution record here.",
			"readonly.body": "The parent session is offline; reopen it to continue sending messages."
		};
		//#endregion
		//#region src/client/index.ts
		/** Required services for subagent presentation and navigation. */
		const inject = [
			"sessions",
			"uiWorkspace",
			"slots",
			"locale",
			"sidebarRight"
		];
		/** Claim the composer for one-shot history or an unavailable continuation owner. */
		function selectReadOnlySubagent(owner) {
			const subagent = owner.session?.subagent;
			if (subagent === void 0 || subagent === null) return null;
			if (subagent.address.mode === "unknown") return { reason: "unknown" };
			if (subagent.address.mode === "one-shot") return { reason: "one-shot" };
			if (subagent.parentAvailable !== false) return null;
			return owner.session?.running === true ? null : { reason: "parent-unavailable" };
		}
		/**
		* Client plugin body: register the subagent catalog and read-only composer seats.
		* @param ctx - client root context.
		*/
		function apply(ctx) {
			ctx.effect(() => ctx.locale.register(NS, {
				zh,
				en
			}), "ui-subagent: dictionaries");
			ctx.inject(["resources", "sidebarRightTabs"], (scope) => {
				registerSidebarChat(scope, ctx.locale.bind(NS));
			});
			const catalogActions = (_parentSessionId) => ({
				openChild(address) {
					ctx.uiWorkspace.openSession(address);
				},
				openChildAside(address) {
					ctx.sidebarRight.openResource(subagentChatAddress(address), {
						kind: "subagentchat",
						preferNewPane: true
					});
				},
				refreshProjection(parentSessionId) {
					ctx.sessions.refreshProjections(parentSessionId);
				}
			});
			ctx.slots.inject("conversation.session.header.lineage", () => ctx.slots.register({
				name: "conversation.session.header.lineage",
				locale: NS,
				inject: catalogActions
			}, SubagentHeaderLineage));
			ctx.slots.inject("conversation.session.header.actions", () => ctx.slots.register({
				name: "conversation.session.header.actions",
				id: "subagent-catalog",
				order: -30,
				locale: NS,
				inject: catalogActions
			}, SubagentCatalogAction));
			ctx.slots.inject("conversation.composer", () => ctx.slots.register({
				name: "conversation.composer",
				priority: -10,
				locale: NS,
				select: selectReadOnlySubagent
			}, SubagentReadOnlyComposer));
		}
		//#endregion
		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map