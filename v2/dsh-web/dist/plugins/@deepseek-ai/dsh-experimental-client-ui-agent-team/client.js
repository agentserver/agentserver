window.__ModuleLoader__.load({
	id: "@deepseek-ai/dsh-experimental-client-ui-agent-team",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		Object.defineProperty(exports, Symbol.toStringTag, { value: "Module" });
		let react = require("react");
		let react_dom = require("react-dom");
		let _deepseek_ai_dsh_client_ui_primitives = require("@deepseek-ai/dsh-client-ui-primitives");
		let react_jsx_runtime = require("react/jsx-runtime");
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/experimental/client-ui-agent-team/src/client/TeamAction.module.css.mjs
		const css = ".ghlB2q_root{position:relative}.ghlB2q_trigger{min-height:28px;color:var(--dsw-alias-label-tertiary);cursor:pointer;background:0 0;border:0;border-radius:6px;align-items:center;gap:5px;padding:3px 7px;font-size:12px;display:inline-flex}.ghlB2q_trigger:hover,.ghlB2q_trigger:focus-visible{color:var(--dsw-alias-label-primary)}@container (width<=480px){.ghlB2q_triggerLabel{display:none}}.ghlB2q_count{color:var(--dsw-alias-label-caption);font-variant-numeric:tabular-nums;font-size:12px;font-weight:400;line-height:16px}.ghlB2q_panel{--dsh-scrollbar-thumb:var(--dsw-alias-scrollbar-bg-l2);--dsh-scrollbar-thumb-hover:var(--dsw-alias-scrollbar-hover-l2);z-index:100;box-sizing:border-box;--dsw-elevation-stroke-color:var(--dsw-alias-border-l1);width:min(500px,100vw - 32px);max-height:min(680px,100vh - 32px);box-shadow:var(--dsw-elevation-prominent);border:0;border-radius:12px;flex-direction:column;padding:8px 2px 0;display:flex;position:fixed;overflow:hidden}.ghlB2q_panel:before{content:\"\";z-index:-1;background:var(--dsw-specific-menu);backdrop-filter:var(--dsw-menu-backdrop-filter);border-radius:12px;position:absolute;inset:0}.ghlB2q_panelCompact{width:min(320px,100vw - 32px)}.ghlB2q_panelCompact .ghlB2q_roster{grid-template-columns:minmax(0,1fr)}.ghlB2q_body{--dsh-scrollbar-track-margin:8px;scrollbar-gutter:stable;flex:auto;min-height:0;padding:0 9px 16px 14px;overflow-y:auto}.ghlB2q_taskTitle{align-items:center;gap:8px;display:flex}.ghlB2q_taskTitle strong{font-size:13px;font-weight:500}.ghlB2q_panel h3{color:var(--dsw-alias-label-primary);align-items:center;gap:8px;margin:16px 0 8px 4px;font-size:13px;font-weight:500;display:flex}.ghlB2q_body section:first-of-type h3{margin-top:8px}.ghlB2q_roster{grid-template-columns:repeat(2,minmax(0,1fr));gap:8px;display:grid}.ghlB2q_member{--dsw-elevation-stroke-color:var(--dsw-alias-border-l2);min-width:0;box-shadow:var(--dsw-elevation-stroke);background:var(--dsw-alias-bg-layer-2);color:var(--dsw-alias-label-primary);text-align:left;cursor:pointer;border:0;border-radius:8px;align-items:flex-start;gap:8px;padding:10px 12px;display:flex}.ghlB2q_member:disabled{cursor:default}.ghlB2q_memberCurrent{--dsw-elevation-stroke-color:color-mix(in srgb, var(--dsw-alias-state-business-primary) 40%, transparent);box-shadow:var(--dsw-elevation-stroke), inset 0 0 0 1px color-mix(in srgb, var(--dsw-alias-state-business-primary) 40%, transparent)}.ghlB2q_member:not(:disabled):hover,.ghlB2q_member:not(:disabled):focus-visible{--dsw-elevation-stroke-color:var(--dsw-alias-border-l3);box-shadow:var(--dsw-elevation-panel)}.ghlB2q_memberDot{flex:none;align-items:center;height:1lh;display:inline-flex}.ghlB2q_inactiveIcon{color:var(--dsw-alias-label-tertiary)}.ghlB2q_memberText{flex-direction:column;min-width:0;display:flex}.ghlB2q_memberName{align-items:center;gap:5px;min-width:0;display:inline-flex}.ghlB2q_memberNameText,.ghlB2q_memberText small{white-space:nowrap;text-overflow:ellipsis;overflow:hidden}.ghlB2q_memberModel{display:none}.ghlB2q_member:hover .ghlB2q_memberModel,.ghlB2q_member:focus-visible .ghlB2q_memberModel{display:inline}.ghlB2q_currentTag{text-overflow:ellipsis;flex:0 999 auto;min-width:0;padding:0 4px;font-size:10px;line-height:15px;display:inline-block;overflow:hidden}.ghlB2q_memberText small,.ghlB2q_meta{color:var(--dsw-alias-label-tertiary);font-size:11px}.ghlB2q_diagnostic,.ghlB2q_error,.ghlB2q_warning{color:var(--dsw-alias-state-error-primary)}.ghlB2q_tasks{flex-direction:column;gap:7px;display:flex}.ghlB2q_emptyNotice{color:var(--dsw-alias-label-tertiary);margin:16px 0 0 4px;font-size:12px}.ghlB2q_task{border:.5px solid var(--dsw-alias-border-l2);background:var(--dsw-alias-bg-layer-2);border-radius:9px;padding:11px 13px}.ghlB2q_taskState{color:var(--dsw-alias-label-tertiary);align-items:center;gap:6px;margin-left:auto;font-size:11px;display:inline-flex}.ghlB2q_task p{color:var(--dsw-alias-label-secondary);white-space:pre-wrap;margin:5px 0;font-size:12px;line-height:18px}.ghlB2q_clampedDescription{-webkit-line-clamp:2;-webkit-box-orient:vertical;display:-webkit-box;overflow:hidden}.ghlB2q_expandToggle{float:right;color:var(--dsw-alias-label-tertiary);cursor:pointer;background:0 0;border:0;align-items:center;gap:2px;margin-left:10px;padding:0;font-size:11px;line-height:20px;display:inline-flex}.ghlB2q_expandToggle:hover,.ghlB2q_expandToggle:focus-visible{color:var(--dsw-alias-label-primary)}.ghlB2q_expandToggle svg{transition:transform .12s}.ghlB2q_expandToggleOpen{transform:rotate(180deg)}.ghlB2q_meta{line-height:20px}.ghlB2q_meta>span{margin-right:10px}.ghlB2q_notice,.ghlB2q_error{align-items:center;gap:6px;padding:9px;font-size:12px;display:flex}";
		const tagId = "@deepseek-ai/dsh-experimental-client-ui-agent-team/TeamAction.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-experimental-client-ui-agent-team";
			tag.dataset.pluginCss = tagId;
			tag.textContent = css;
			document.head.appendChild(tag);
		}
		var TeamAction_module_css_default = {
			"body": "ghlB2q_body",
			"clampedDescription": "ghlB2q_clampedDescription",
			"count": "ghlB2q_count",
			"currentTag": "ghlB2q_currentTag",
			"diagnostic": "ghlB2q_diagnostic",
			"emptyNotice": "ghlB2q_emptyNotice",
			"error": "ghlB2q_error",
			"expandToggle": "ghlB2q_expandToggle",
			"expandToggleOpen": "ghlB2q_expandToggleOpen",
			"inactiveIcon": "ghlB2q_inactiveIcon",
			"member": "ghlB2q_member",
			"memberCurrent": "ghlB2q_memberCurrent",
			"memberDot": "ghlB2q_memberDot",
			"memberModel": "ghlB2q_memberModel",
			"memberName": "ghlB2q_memberName",
			"memberNameText": "ghlB2q_memberNameText",
			"memberText": "ghlB2q_memberText",
			"meta": "ghlB2q_meta",
			"notice": "ghlB2q_notice",
			"panel": "ghlB2q_panel",
			"panelCompact": "ghlB2q_panelCompact",
			"root": "ghlB2q_root",
			"roster": "ghlB2q_roster",
			"task": "ghlB2q_task",
			"taskState": "ghlB2q_taskState",
			"taskTitle": "ghlB2q_taskTitle",
			"tasks": "ghlB2q_tasks",
			"trigger": "ghlB2q_trigger",
			"triggerLabel": "ghlB2q_triggerLabel",
			"warning": "ghlB2q_warning"
		};
		//#endregion
		//#region src/client/TeamAction.tsx
		function statusKey(status) {
			switch (status) {
				case "pending": return "status.pending";
				case "in_progress": return "status.in_progress";
				case "completed": return "status.completed";
				/* v8 ignore next -- Team views omit deleted task tombstones. */
				case "deleted": return "status.completed";
			}
		}
		function memberStatusKey(status) {
			switch (status) {
				case "running": return "memberStatus.running";
				case "inactive": return "memberStatus.inactive";
				case "provisioning": return "memberStatus.provisioning";
				case "failed": return "memberStatus.failed";
			}
		}
		function memberDotState(status) {
			switch (status) {
				case "running":
				case "provisioning": return "ongoing";
				case "failed": return "error";
			}
		}
		function taskDotState(task) {
			switch (task.status) {
				case "pending": return task.ready ? "idle" : "warning";
				case "in_progress": return "ongoing";
				case "completed": return "done";
				/* v8 ignore next -- Team views omit deleted task tombstones. */
				case "deleted": return "idle";
			}
		}
		function TeamMemberRow({ member, memberCount, sessionId, useSessions, useSessionStatus, openTeammate, onError, t }) {
			const model = useSessions((state) => state.projectionsBySession[member.id]?.values.modelSelection?.next?.model);
			const running = useSessionStatus((state) => state.get(member.id)?.running);
			const summaryRunning = useSessions((state) => state.byId[member.id]?.running);
			const status = member.phase === "active" ? (running ?? summaryRunning) === true ? "running" : "inactive" : member.phase;
			const isCurrent = member.id === sessionId;
			const highlightCurrent = isCurrent && memberCount > 1;
			const inert = isCurrent || status === "failed" || status === "provisioning";
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
				label: t("open"),
				side: "bottom",
				gap: 4,
				disabled: inert,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
					type: "button",
					className: highlightCurrent ? `${TeamAction_module_css_default.member} ${TeamAction_module_css_default.memberCurrent}` : TeamAction_module_css_default.member,
					disabled: inert,
					onClick: () => {
						try {
							openTeammate(sessionId, member.id);
						} catch (reason) {
							onError(String(reason));
						}
					},
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: TeamAction_module_css_default.memberDot,
						children: status === "inactive" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconUserOutlineRegular, {
							size: 14,
							className: TeamAction_module_css_default.inactiveIcon
						}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, { state: memberDotState(status) })
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
						className: TeamAction_module_css_default.memberText,
						children: [
							/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
								className: TeamAction_module_css_default.memberName,
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
									className: TeamAction_module_css_default.memberNameText,
									children: member.name
								}), isCurrent && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tag, {
									tone: "info",
									className: TeamAction_module_css_default.currentTag,
									children: t("current")
								})]
							}),
							/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("small", { children: [t(memberStatusKey(status)), model !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
								className: TeamAction_module_css_default.memberModel,
								children: ` · ${t("model")}: ${model}`
							})] }),
							member.error !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("small", {
								className: TeamAction_module_css_default.diagnostic,
								children: member.error
							})
						]
					})]
				})
			});
		}
		/** Task card with a two-line description clamp expanded from a toggle in the meta row. */
		function TaskCard({ task, t }) {
			const [expanded, setExpanded] = (0, react.useState)(false);
			const [clamped, setClamped] = (0, react.useState)(false);
			const textRef = (0, react.useRef)(null);
			(0, react.useLayoutEffect)(() => {
				if (expanded) return;
				const paragraph = textRef.current;
				/* v8 ignore next -- the paragraph mounts in the same commit as the effect. */
				if (paragraph === null) return;
				const measure = () => {
					setClamped(paragraph.scrollHeight > paragraph.clientHeight + 1);
				};
				measure();
				if (typeof ResizeObserver === "undefined") return;
				const observer = new ResizeObserver(measure);
				observer.observe(paragraph);
				return () => {
					observer.disconnect();
				};
			}, [task.description, expanded]);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("article", {
				className: TeamAction_module_css_default.task,
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: TeamAction_module_css_default.taskTitle,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("strong", { children: task.subject }), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
							className: TeamAction_module_css_default.taskState,
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, { state: taskDotState(task) }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: t(statusKey(task.status)) })]
						})]
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
						ref: textRef,
						className: expanded ? void 0 : TeamAction_module_css_default.clampedDescription,
						children: task.description
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: TeamAction_module_css_default.meta,
						children: [
							(clamped || expanded) && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
								type: "button",
								className: TeamAction_module_css_default.expandToggle,
								"aria-expanded": expanded,
								onClick: () => {
									setExpanded((current) => !current);
								},
								children: [t(expanded ? "task.collapse" : "task.expand"), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronDownOutlineRegular, {
									size: 12,
									className: expanded ? TeamAction_module_css_default.expandToggleOpen : void 0
								})]
							}),
							/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: task.id }),
							/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", { children: [
								t("owner"),
								": ",
								task.ownerName ?? t("unowned")
							] }),
							task.status === "pending" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: task.ready ? t("ready") : t("blocked") }),
							task.blockedBy.length > 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", { children: [
								t("blockedBy"),
								": ",
								task.blockedBy.join(", ")
							] }),
							task.writeScopes.length > 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", { children: [
								t("writeScopes"),
								": ",
								task.writeScopes.join(", ")
							] }),
							task.writeScopeWarnings.map((warning) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
								className: TeamAction_module_css_default.warning,
								children: warning
							}, warning))
						]
					})
				]
			});
		}
		/** Render the Team roster and read-only task board. */
		function TeamAction({ sessionId, useSession, useSessions, useSessionStatus, openTeammate, t }) {
			const [open, setOpen] = (0, react.useState)(false);
			const [error, setError] = (0, react.useState)(null);
			const rootRef = (0, react.useRef)(null);
			const triggerRef = (0, react.useRef)(null);
			const triggerLabelRef = (0, react.useRef)(null);
			const panelRef = (0, react.useRef)(null);
			const position = (0, _deepseek_ai_dsh_client_ui_primitives.useAnchoredPosition)({
				open,
				anchorRef: triggerRef,
				panelRef,
				gap: 5,
				margin: 16
			});
			const positioned = position !== null;
			const leadSessionId = useSession((snapshot) => snapshot.subagent?.address.parentSessionId) ?? sessionId;
			const team = useSessions((state) => state.projectionsBySession[leadSessionId]?.values.agentTeam);
			const opening = useSession((snapshot) => snapshot.openState === "loading");
			const listing = useSessions((state) => state.phase === "pending");
			const hoverTimer = (0, react.useRef)(void 0);
			const pinnedRef = (0, react.useRef)(false);
			const cancelHoverChange = () => {
				clearTimeout(hoverTimer.current);
				hoverTimer.current = void 0;
			};
			(0, react.useEffect)(() => {
				cancelHoverChange();
				pinnedRef.current = false;
				setOpen(false);
				setError(null);
			}, [sessionId]);
			(0, react.useEffect)(() => cancelHoverChange, []);
			(0, react.useLayoutEffect)(() => {
				if (open && positioned && pinnedRef.current) panelRef.current?.focus();
			}, [open, positioned]);
			const changeOpen = (next) => {
				cancelHoverChange();
				if (!next) pinnedRef.current = false;
				setOpen(next);
			};
			const scheduleHoverOpen = () => {
				cancelHoverChange();
				if (open) return;
				const label = triggerLabelRef.current;
				/* v8 ignore next -- the label mounts with the trigger that received the hover. */
				if (label === null) return;
				if (getComputedStyle(label).display === "none") return;
				hoverTimer.current = setTimeout(() => {
					hoverTimer.current = void 0;
					changeOpen(true);
				}, 150);
			};
			const scheduleHoverClose = () => {
				cancelHoverChange();
				if (pinnedRef.current) return;
				hoverTimer.current = setTimeout(() => {
					hoverTimer.current = void 0;
					changeOpen(false);
				}, 120);
			};
			(0, _deepseek_ai_dsh_client_ui_primitives.useDismissOnOutsidePointer)(rootRef, open, changeOpen, panelRef);
			(0, react.useEffect)(() => {
				if (!open) return;
				const dismiss = (event) => {
					if (event.key !== "Escape") return;
					event.preventDefault();
					cancelHoverChange();
					pinnedRef.current = false;
					setOpen(false);
					if (panelRef.current?.contains(document.activeElement)) triggerRef.current?.focus();
				};
				document.addEventListener("keydown", dismiss);
				return () => {
					document.removeEventListener("keydown", dismiss);
				};
			}, [open]);
			const compact = team !== void 0 && team.members.length === 1 && team.tasks.length === 0;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				ref: rootRef,
				className: TeamAction_module_css_default.root,
				"data-team-action": true,
				onMouseLeave: scheduleHoverClose,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
					type: "button",
					ref: triggerRef,
					onMouseEnter: scheduleHoverOpen,
					className: TeamAction_module_css_default.trigger,
					"aria-label": t("trigger"),
					"aria-haspopup": "dialog",
					"aria-expanded": open,
					onClick: () => {
						cancelHoverChange();
						pinnedRef.current = true;
						if (!open) changeOpen(true);
						else panelRef.current?.focus();
					},
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconUsersOutlineRegular, { size: 14 }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						ref: triggerLabelRef,
						className: TeamAction_module_css_default.triggerLabel,
						children: t("trigger")
					})]
				}), open && (0, react_dom.createPortal)(/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
					ref: panelRef,
					className: compact ? `${TeamAction_module_css_default.panel} ${TeamAction_module_css_default.panelCompact}` : TeamAction_module_css_default.panel,
					style: position ?? {
						visibility: "hidden",
						left: 0,
						top: 0
					},
					role: "dialog",
					tabIndex: -1,
					"aria-label": t("trigger"),
					"data-team-panel": true,
					onMouseEnter: cancelHoverChange,
					onMouseLeave: scheduleHoverClose,
					children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: TeamAction_module_css_default.body,
						children: [
							error !== null && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
								className: TeamAction_module_css_default.error,
								role: "alert",
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, { state: "error" }), error]
							}),
							team === void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
								className: TeamAction_module_css_default.notice,
								role: "status",
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, { state: opening || listing ? "ongoing" : "warning" }), t(opening || listing ? "loading" : "unavailable")]
							}),
							team !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [
								team.failure !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
									className: TeamAction_module_css_default.error,
									role: "alert",
									children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, { state: "error" }), t("failure", { message: team.failure })]
								}),
								/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("h3", { children: [t("roster"), team.members.length > 1 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
									className: TeamAction_module_css_default.count,
									children: team.members.length
								})] }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
									className: TeamAction_module_css_default.roster,
									children: team.members.map((member) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)(TeamMemberRow, {
										member,
										memberCount: team.members.length,
										sessionId,
										useSessions,
										useSessionStatus,
										openTeammate,
										onError: setError,
										t
									}, member.id))
								})] }),
								/* @__PURE__ */ (0, react_jsx_runtime.jsx)("section", { children: team.tasks.length === 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
									className: TeamAction_module_css_default.emptyNotice,
									children: t("empty")
								}) : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("h3", { children: [t("tasks"), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
									className: TeamAction_module_css_default.count,
									children: team.tasks.length
								})] }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
									className: TeamAction_module_css_default.tasks,
									children: team.tasks.map((task) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)(TaskCard, {
										task,
										t
									}, task.id))
								})] }) })
							] })
						]
					})
				}), document.body)]
			});
		}
		//#endregion
		//#region src/client/locales.ts
		/** Agent Teams Web dictionaries. */
		/** Locale namespace owned by the Agent Teams Web UI. */
		const NS = "agent-team";
		/** Simplified Chinese dictionary and key source. */
		const zh = {
			trigger: "智能体团队",
			loading: "正在加载团队…",
			unavailable: "Team 暂不可用",
			failure: "团队持久记录无效：{message}",
			empty: "暂无共享任务，可以通过对话创建",
			roster: "成员",
			tasks: "共享任务",
			model: "模型",
			open: "打开成员会话",
			current: "当前会话",
			owner: "Owner",
			unowned: "未分配",
			blockedBy: "依赖",
			writeScopes: "写入范围",
			ready: "可开始",
			blocked: "被依赖阻塞",
			"task.expand": "展开",
			"task.collapse": "收起",
			"memberStatus.running": "运行中",
			"memberStatus.inactive": "未运行",
			"memberStatus.provisioning": "准备中",
			"memberStatus.failed": "失败",
			"status.pending": "待处理",
			"status.in_progress": "进行中",
			"status.completed": "已完成"
		};
		/** English dictionary checked against the Chinese key set. */
		const en = {
			trigger: "Agent Team",
			loading: "Loading Team…",
			unavailable: "Team is unavailable",
			failure: "Invalid persisted Team record: {message}",
			empty: "No shared tasks yet. Create them through the conversation.",
			roster: "Members",
			tasks: "Shared tasks",
			model: "Model",
			open: "Open member conversation",
			current: "Current chat",
			owner: "Owner",
			unowned: "Unowned",
			blockedBy: "Blocked by",
			writeScopes: "Write scopes",
			ready: "Ready",
			blocked: "Blocked by dependencies",
			"task.expand": "Show more",
			"task.collapse": "Show less",
			"memberStatus.running": "Running",
			"memberStatus.inactive": "Inactive",
			"memberStatus.provisioning": "Provisioning",
			"memberStatus.failed": "Failed",
			"status.pending": "Pending",
			"status.in_progress": "In progress",
			"status.completed": "Completed"
		};
		//#endregion
		//#region src/client/mount.ts
		/** Required browser services for navigation, slots, and localized copy. */
		const inject = [
			"sessions",
			"uiWorkspace",
			"slots",
			"locale"
		];
		/**
		* Register the Team locale dictionaries and the conversation-header action.
		* The panel reads the Lead Session's `agentTeam` projection from the shared
		* Session store; this registration performs no Team RPC.
		* @param ctx - Client Context carrying the injected navigation, locale, slot, and Session services.
		*/
		function registerAgentTeamUi(ctx) {
			ctx.effect(() => ctx.locale.register(NS, {
				zh,
				en
			}), "client-ui-agent-team: dictionaries");
			const sessions = ctx.sessions;
			const leadSessionId = (sessionId) => {
				return (sessions.binding(sessionId)?.session.getSnapshot().subagent?.address)?.parentSessionId ?? sessionId;
			};
			const actions = { openTeammate(sessionId, childSessionId) {
				const parentSessionId = leadSessionId(sessionId);
				if ((sessions.retainInfo(sessionId).getSnapshot().retainedBy.mainView ?? 0) === 0) return;
				if (childSessionId === parentSessionId) {
					ctx.uiWorkspace.openSession(parentSessionId);
					return;
				}
				ctx.uiWorkspace.openSession({
					parentSessionId,
					childSessionId,
					mode: "continuable"
				});
			} };
			ctx.slots.inject("conversation.session.header.actions", () => ctx.slots.register({
				name: "conversation.session.header.actions",
				id: "agent-team",
				order: -20,
				locale: NS,
				inject: () => actions
			}, TeamAction));
		}
		//#endregion
		//#region src/client/index.ts
		/**
		* Register the Team locale dictionaries and header action on the Client Context.
		* @param ctx - Client Context with the declared `inject` services available.
		*/
		function apply(ctx) {
			registerAgentTeamUi(ctx);
		}
		//#endregion
		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map