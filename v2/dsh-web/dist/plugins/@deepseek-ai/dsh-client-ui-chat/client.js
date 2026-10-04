window.__ModuleLoader__.load({
	id: "@deepseek-ai/dsh-client-ui-chat",
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
		let _deepseek_ai_dsh_client_ui_primitives = require("@deepseek-ai/dsh-client-ui-primitives");
		let react_jsx_runtime = require("react/jsx-runtime");
		let react_dom = require("react-dom");
		//#region ../../util/workspace-path/src/file-address.ts
		/** The scheme and type every file address opens with. */
		const FILE_ADDRESS_PREFIX = "dsh-resource://file/";
		/** Component-encode one id or path segment, keeping `:` literal for drive letters. */
		function encodeSegment(segment) {
			return encodeURIComponent(segment).replace(/%3A/gi, ":");
		}
		/** Encode a `/`-separated path segment by segment. */
		function encodePath(path) {
			return path.split("/").map(encodeSegment).join("/");
		}
		/**
		* Build the address of a file read through one Session.
		* @param sessionId - the Session whose Host workspace resolves the path.
		* @param path - absolute or workspace-relative path; backslashes are normalized to `/`, and leading `./` prefixes are dropped.
		* @returns the `dsh-resource://file/session/<sessionId>/<path>` address.
		*/
		function sessionFileAddress(sessionId, path) {
			const normalized = path.replace(/\\/g, "/").replace(/^(?:\.\/)+/, "");
			return `${FILE_ADDRESS_PREFIX}session/${encodeSegment(sessionId)}/${encodePath(normalized)}`;
		}
		//#endregion
		//#region ../../util/workspace-path/src/index.ts
		/**
		* Browser-safe Workspace path and display helpers.
		* @module @deepseek-ai/dsh-util-workspace-path
		*/
		/** Whether a path uses a Windows drive or UNC prefix. */
		function isWindowsStylePath(value) {
			return /^[A-Za-z]:[/\\]/.test(value) || value.startsWith("\\\\");
		}
		/**
		* Whether a path is absolute in either spelling the Host accepts: POSIX (`/a/b`) or Windows drive or UNC.
		* @param path - the path to classify.
		* @returns `true` for an absolute path; `false` for a Workspace-relative one.
		*/
		function isAbsoluteWorkspacePath(path) {
			return path.startsWith("/") || isWindowsStylePath(path);
		}
		/**
		* Resolve a Workspace-relative path into the Host-facing spelling used by path operations.
		* @param cwd - Session Workspace root, when known.
		* @param path - Absolute or Workspace-relative path.
		* @returns an absolute path when a Workspace root is available, otherwise the original path.
		*/
		function resolveWorkspacePath(cwd, path) {
			if (isAbsoluteWorkspacePath(path)) return path;
			if (cwd === void 0 || cwd === "") return path;
			const separator = isWindowsStylePath(cwd) && cwd.includes("\\") ? "\\" : "/";
			return `${cwd.replace(/[/\\]+$/, "")}${separator}${path.replace(/^[/\\]+/, "")}`;
		}
		/**
		* The address for a path as a caller holds it: a relative path, or an absolute
		* path inside the Session's workspace, becomes a `session`-scoped address; an
		* absolute path outside it, or one whose workspace root is unknown, keeps its
		* absolute path in that Session's address.
		* @param sessionId - the Session the path is read in.
		* @param cwd - that Session's workspace root, when known.
		* @param path - absolute or workspace-relative path, in either separator spelling.
		* @returns the `dsh-resource://file/…` address.
		*/
		function fileAddressFor(sessionId, cwd, path) {
			const normalized = path.replace(/\\/g, "/");
			if (!isAbsoluteWorkspacePath(normalized)) return sessionFileAddress(sessionId, normalized);
			const root = cwd === void 0 ? "" : cwd.replace(/\\/g, "/").replace(/\/+$/, "");
			if (root !== "" && normalized === root) return sessionFileAddress(sessionId, "");
			if (root !== "" && normalized.startsWith(`${root}/`)) return sessionFileAddress(sessionId, normalized.slice(root.length + 1));
			return sessionFileAddress(sessionId, normalized);
		}
		/**
		* Address a decoded absolute file path through the authenticated file route.
		* @param base - HTTP(S) application base, including its deployment prefix, or `dsh-app://app/`.
		* @param path - Native file path; URL escapes in authored Markdown must already be decoded.
		* @returns File URL, or undefined for unsupported transports and non-absolute paths.
		*/
		function fileMediaUrl(base, path) {
			if (!/^https?:/u.test(base) && !base.startsWith("dsh-app://app/") || !isAbsoluteWorkspacePath(path) || /^[/\\]{2}/u.test(path) || /[\u0000-\u001f\u007f]/u.test(path)) return void 0;
			return new URL(`api/file?path=${encodeURIComponent(path)}`, base).href;
		}
		//#endregion
		//#region src/client/contract/snapshot.ts
		const EMPTY_LIST$1 = [];
		const EMPTY_TIMELINE = {
			turnOrder: EMPTY_LIST$1,
			turns: /* @__PURE__ */ new Map()
		};
		const EMPTY_NODE_SOURCE = {
			getSnapshot: () => void 0,
			subscribe: () => () => {}
		};
		const EMPTY_NODE_PROCESS_SOURCE = {
			getSnapshot: () => void 0,
			subscribe: () => () => {}
		};
		const EMPTY_TURN_NODE_SOURCE = {
			getSnapshot: () => EMPTY_LIST$1,
			subscribe: () => () => {}
		};
		/** Empty Chat target used before a view builder is registered. */
		const EMPTY_CHAT_SNAPSHOT = {
			order: EMPTY_LIST$1,
			nodes: {
				get: () => void 0,
				source: () => EMPTY_NODE_SOURCE,
				turnDataSource: () => EMPTY_TURN_NODE_SOURCE,
				processSource: () => EMPTY_NODE_PROCESS_SOURCE,
				values: () => EMPTY_LIST$1
			},
			locations: {
				getTurn: () => EMPTY_LIST$1,
				getStep: () => EMPTY_LIST$1
			},
			navigation: { items: () => EMPTY_LIST$1 },
			timeline: EMPTY_TIMELINE,
			legacy: {
				nodes: EMPTY_LIST$1,
				turnTimings: /* @__PURE__ */ new Map(),
				turnEnds: /* @__PURE__ */ new Map(),
				partial: null,
				runningCalls: EMPTY_LIST$1
			}
		};
		//#endregion
		//#region src/client/chat/ApprovalCommand.tsx
		/**
		* Extract a shell command from a correlated Tool call when its arguments carry one.
		* @param call - Tool call arguments, when a correlated call exists.
		* @returns command text, or undefined for absent, malformed, or unrelated arguments.
		*/
		function commandOf(call) {
			if (call === void 0) return void 0;
			try {
				const args = JSON.parse(call.argsRaw);
				return typeof args.command === "string" ? args.command : void 0;
			} catch {
				return;
			}
		}
		/**
		* Render the command of the Chat Tool node correlated with an approval.
		* @param props - Approval identity and Session-standard Chat selector hook.
		* @returns command text when the correlated call carries one.
		*/
		function ApprovalCommand({ callId, useChat }) {
			return useChat((snapshot) => {
				for (const node of snapshot.nodes.values()) {
					const root = node.kind === "tool-call" ? node.data.root : void 0;
					if (root !== void 0 && root.callId === callId && !("kind" in root) && root.phase === "start") return commandOf(root);
				}
			}) ?? null;
		}
		//#endregion
		//#region src/client/markdown-labels.ts
		/**
		* Build the complete Markdown chrome copy for one locale revision.
		* @param t - Chat locale seat.
		* @returns Labels for code fences and footnotes.
		*/
		function markdownLabels(t) {
			return {
				code: {
					copyLabel: t("copy"),
					copiedLabel: t("copied"),
					toolbarLabels: {
						codeLabel: t("codeBlock.title"),
						wrapLabel: t("codeBlock.wrap"),
						unwrapLabel: t("codeBlock.unwrap")
					}
				},
				footnotes: t("markdown.footnotes")
			};
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-chat/src/client/chat/MessageItem.module.css.mjs
		const css$17 = ".GW-x-W_userRow{flex-direction:column;align-items:flex-end;gap:6px;display:flex}.GW-x-W_userStack{min-width:0;max-width:min(calc(var(--dsh-chat-content-width,748px) * .702), 82%);flex-direction:column;align-items:flex-end;gap:8px;display:flex}.GW-x-W_bubble{background:var(--dsw-specific-bubble);border-radius:var(--dsw-radius-xl);max-width:100%;font-size:var(--dsh-content-font-size,14px);line-height:calc(22px + var(--dsh-content-font-delta,0px));color:var(--dsw-alias-label-primary);white-space:pre-wrap;word-break:break-word;padding:10px 16px}.GW-x-W_referenceSummary{color:var(--dsw-alias-label-tertiary);font-size:var(--dsh-content-font-size-secondary,13px);line-height:calc(18px + var(--dsh-content-font-delta-secondary,0px))}.GW-x-W_contextRow{padding:2px 0}.GW-x-W_compactionRow{--dsh-compaction-header-height:calc(24px + var(--dsh-content-font-delta,0px));padding:2px 0}.GW-x-W_compactionButton{width:100%;height:var(--dsh-compaction-header-height);border-radius:var(--dsw-radius-sm);min-width:0;color:inherit;font:inherit;text-align:left;background:0 0;border:none;align-items:center;padding:0;display:flex}.GW-x-W_compactionRow:has(.GW-x-W_compactionBody) .GW-x-W_compactionButton{z-index:7;background:var(--dsw-alias-bg-base);border-radius:0;position:sticky;top:0}.GW-x-W_compactionBody :has(>[data-code-block-banner]){top:var(--dsh-compaction-header-height)}.GW-x-W_compactionRow:has(.GW-x-W_compactionBody) .GW-x-W_compactionButton:hover{background:var(--dsw-alias-interactive-bg-hover-solid)}.GW-x-W_compactionButton:not(:disabled){cursor:pointer}.GW-x-W_compactionButton:not(:disabled):hover{background:var(--dsw-alias-interactive-bg-hover)}.GW-x-W_compactionLeading{width:calc(16px + var(--dsh-content-font-delta,0px));height:calc(16px + var(--dsh-content-font-delta,0px));color:var(--dsw-alias-label-secondary);flex:none;place-items:center;margin-right:6px;display:inline-grid}.GW-x-W_compactionLeading svg{width:calc(14px + var(--dsh-content-font-delta,0px));height:calc(14px + var(--dsh-content-font-delta,0px))}.GW-x-W_compactionContextIcon,.GW-x-W_compactionDisclosureIcon{grid-area:1/1;justify-content:center;align-items:center;display:inline-flex}.GW-x-W_compactionDisclosureIcon,.GW-x-W_compactionButton:not(:disabled):hover .GW-x-W_compactionContextIcon,.GW-x-W_compactionButton:not(:disabled):focus-visible .GW-x-W_compactionContextIcon{opacity:0}.GW-x-W_compactionButton:not(:disabled):hover .GW-x-W_compactionDisclosureIcon,.GW-x-W_compactionButton:not(:disabled):focus-visible .GW-x-W_compactionDisclosureIcon{opacity:1}.GW-x-W_compactionTitle{font-size:var(--dsh-content-font-size-secondary,13px);line-height:calc(24px + var(--dsh-content-font-delta,0px));color:var(--dsw-alias-label-primary-dimmed);flex:none}.GW-x-W_compactionSep{background:var(--dsw-alias-label-caption);border-radius:1px;flex:none;width:2px;height:2px;margin:0 8px}.GW-x-W_compactionSummary{min-width:0;color:var(--dsw-alias-label-tertiary);font-size:var(--dsh-content-font-size-secondary,13px);line-height:calc(24px + var(--dsh-content-font-delta,0px));text-overflow:ellipsis;white-space:nowrap;flex:auto;overflow:hidden}.GW-x-W_compactionBody{padding:4px 0 4px calc(22px + var(--dsh-content-font-delta,0px));color:var(--dsw-alias-label-tertiary);font-size:var(--dsh-content-font-size-secondary,13px);line-height:calc(24px + var(--dsh-content-font-delta,0px))}.GW-x-W_retryRow{color:var(--dsw-alias-label-tertiary);font-size:var(--dsh-content-font-size-secondary,13px);line-height:calc(20px + var(--dsh-content-font-delta-secondary,0px))}.GW-x-W_retrySummary{border-radius:var(--dsw-radius-sm);width:fit-content;color:inherit;cursor:pointer;user-select:none;align-items:center;gap:7px;padding:2px 0;list-style:none;display:inline-flex}.GW-x-W_retrySummary::-webkit-details-marker{display:none}.GW-x-W_retrySummary:after{content:\"\";opacity:.8;border-bottom:1.5px solid;border-right:1.5px solid;width:6px;height:6px;transition:transform .12s;transform:rotate(-45deg)}.GW-x-W_retrySummary:hover{color:var(--dsw-alias-label-secondary)}.GW-x-W_retrySummary:focus-visible{outline:1.5px solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:2px}.GW-x-W_retryText{color:inherit}.GW-x-W_retryRow[data-active] .GW-x-W_retryText{background:linear-gradient(90deg, var(--dsw-alias-label-tertiary) 0%, var(--dsw-alias-label-tertiary) 40%, var(--dsw-alias-label-secondary) 50%, var(--dsw-alias-label-tertiary) 60%, var(--dsw-alias-label-tertiary) 100%);color:#0000;background-position:100%;background-size:200% 100%;background-clip:text;animation:1.6s ease-in-out infinite GW-x-W_retry-shimmer}.GW-x-W_retryRow[open] .GW-x-W_retrySummary:after{transform:rotate(45deg)}.GW-x-W_retryDetails{overflow-wrap:anywhere;font-size:var(--dsh-content-font-size-secondary,13px);line-height:calc(18px + var(--dsh-content-font-delta-secondary,0px));gap:2px;margin-top:3px;padding-left:14px;display:grid}.GW-x-W_retryDetailLabel{color:var(--dsw-alias-label-secondary)}.GW-x-W_turnErrorRow{font-size:var(--dsh-content-font-size-secondary,13px);line-height:calc(20px + var(--dsh-content-font-delta-secondary,0px));grid-template-columns:10px minmax(0,1fr) auto;align-items:start;gap:8px;padding:2px 0;display:grid}.GW-x-W_turnErrorDot{margin-top:5px}.GW-x-W_turnErrorCopy{overflow-wrap:anywhere;min-width:0}.GW-x-W_turnErrorTitle{color:var(--dsw-alias-state-error-primary);margin-right:6px;font-weight:600}.GW-x-W_turnErrorMessage{color:var(--dsw-alias-label-secondary)}.GW-x-W_turnErrorCode{color:var(--dsw-alias-label-tertiary);font:var(--dsw-font-markdown-code-block-small)}.GW-x-W_maxTokensTitle{color:var(--dsw-alias-state-warn-primary);margin-right:6px;font-weight:600}@keyframes GW-x-W_retry-shimmer{0%{background-position:100%}to{background-position:0}}@media (prefers-reduced-motion:reduce){.GW-x-W_retryRow[data-active] .GW-x-W_retryText{color:inherit;background:0 0;animation:none}}.GW-x-W_attachmentRow{flex-wrap:wrap;justify-content:flex-end;gap:8px;max-width:100%;display:flex}.GW-x-W_fileCard{border:.5px solid var(--dsw-alias-border-l2,#0000001f);border-radius:var(--dsw-radius-xl);background:var(--dsw-specific-input-major,transparent);box-sizing:border-box;flex:0 0 240px;align-items:center;gap:10px;width:240px;min-height:64px;padding:8px 12px;display:inline-flex}.GW-x-W_fileIcon{flex:none;width:28px;height:28px}.GW-x-W_fileContent{flex-direction:column;flex:1;min-width:0;display:flex}.GW-x-W_fileName{white-space:nowrap;text-overflow:ellipsis;color:var(--dsw-alias-label-primary);font-size:14px;font-weight:500;line-height:22px;overflow:hidden}.GW-x-W_fileMeta{white-space:nowrap;text-overflow:ellipsis;color:var(--dsw-alias-label-tertiary,#00000073);font-size:12px;line-height:15px;overflow:hidden}";
		const tagId$17 = "@deepseek-ai/dsh-client-ui-chat/MessageItem.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$17) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-chat";
			tag.dataset.pluginCss = tagId$17;
			tag.textContent = css$17;
			document.head.appendChild(tag);
		}
		var MessageItem_module_css_default = {
			"attachmentRow": "GW-x-W_attachmentRow",
			"bubble": "GW-x-W_bubble",
			"compactionBody": "GW-x-W_compactionBody",
			"compactionButton": "GW-x-W_compactionButton",
			"compactionContextIcon": "GW-x-W_compactionContextIcon",
			"compactionDisclosureIcon": "GW-x-W_compactionDisclosureIcon",
			"compactionLeading": "GW-x-W_compactionLeading",
			"compactionRow": "GW-x-W_compactionRow",
			"compactionSep": "GW-x-W_compactionSep",
			"compactionSummary": "GW-x-W_compactionSummary",
			"compactionTitle": "GW-x-W_compactionTitle",
			"contextRow": "GW-x-W_contextRow",
			"fileCard": "GW-x-W_fileCard",
			"fileContent": "GW-x-W_fileContent",
			"fileIcon": "GW-x-W_fileIcon",
			"fileMeta": "GW-x-W_fileMeta",
			"fileName": "GW-x-W_fileName",
			"maxTokensTitle": "GW-x-W_maxTokensTitle",
			"referenceSummary": "GW-x-W_referenceSummary",
			"retry-shimmer": "GW-x-W_retry-shimmer",
			"retryDetailLabel": "GW-x-W_retryDetailLabel",
			"retryDetails": "GW-x-W_retryDetails",
			"retryRow": "GW-x-W_retryRow",
			"retrySummary": "GW-x-W_retrySummary",
			"retryText": "GW-x-W_retryText",
			"turnErrorCode": "GW-x-W_turnErrorCode",
			"turnErrorCopy": "GW-x-W_turnErrorCopy",
			"turnErrorDot": "GW-x-W_turnErrorDot",
			"turnErrorMessage": "GW-x-W_turnErrorMessage",
			"turnErrorRow": "GW-x-W_turnErrorRow",
			"turnErrorTitle": "GW-x-W_turnErrorTitle",
			"userRow": "GW-x-W_userRow",
			"userStack": "GW-x-W_userStack"
		};
		//#endregion
		//#region src/client/chat/CompactionItem.tsx
		/**
		* Renders the model-history compaction marker.
		* @param props - the marker node off the snapshot cache.
		* @returns the marker row, with the summary disclosure when one is available.
		*/
		const CompactionItem = (0, react.memo)(function CompactionItem({ node, title, fallbackSummary, t }) {
			const [expanded, setExpanded] = (0, react.useState)(false);
			const labels = (0, react.useMemo)(() => markdownLabels(t), [t]);
			const expandable = node.summary !== null;
			const open = expandable && expanded;
			const summary = node.shadowedItemCount !== null && node.shadowedTokenCount !== null ? t("message.compaction.completed", {
				items: node.shadowedItemCount,
				tokens: node.shadowedTokenCount
			}) : fallbackSummary ?? (expandable ? t("message.compaction.expand") : t("message.compaction.unavailable"));
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: MessageItem_module_css_default.compactionRow,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
					type: "button",
					className: MessageItem_module_css_default.compactionButton,
					disabled: !expandable,
					"aria-expanded": expandable ? open : void 0,
					onClick: () => {
						setExpanded((value) => !value);
					},
					children: [
						/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
							className: MessageItem_module_css_default.compactionLeading,
							"aria-hidden": true,
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
								className: MessageItem_module_css_default.compactionContextIcon,
								"data-compaction-icon": "context",
								children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconApiOutlineRegular, {})
							}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
								className: MessageItem_module_css_default.compactionDisclosureIcon,
								"data-compaction-disclosure": open ? "expanded" : "collapsed",
								children: open ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronDownOutlineRegular, {}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronRightOutlineRegular, {})
							})]
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: MessageItem_module_css_default.compactionTitle,
							children: title ?? t("message.compaction")
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: MessageItem_module_css_default.compactionSep,
							"aria-hidden": true
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: MessageItem_module_css_default.compactionSummary,
							children: summary
						})
					]
				}), open && node.summary !== null && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
					className: MessageItem_module_css_default.compactionBody,
					children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.MarkdownText, {
						text: node.summary,
						labels
					})
				})]
			});
		});
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-chat/src/client/chat/ContextBody.module.css.mjs
		const css$16 = ".YoCQKq_text{color:var(--dsw-alias-label-secondary);font:inherit;white-space:pre-wrap;overflow-wrap:anywhere;margin:0}.YoCQKq_fields{border-top:.5px solid var(--dsw-alias-border-l2);flex-direction:column;gap:2px;margin:8px 0 0;padding-top:8px;display:flex}.YoCQKq_field{gap:8px;min-width:0;display:flex}.YoCQKq_fieldKey{min-width:96px;color:var(--dsw-alias-label-caption);flex:none}.YoCQKq_fieldValue{min-width:0;color:var(--dsw-alias-label-tertiary);overflow-wrap:anywhere;flex:auto;margin:0}.YoCQKq_files{flex-wrap:wrap;gap:4px 12px;margin:0 0 8px;padding:0;list-style:none;display:flex}.YoCQKq_file{align-items:baseline;gap:6px;min-width:0;display:flex}.YoCQKq_filePath{color:var(--dsw-alias-label-secondary);overflow-wrap:anywhere}.YoCQKq_fileAction{color:var(--dsw-alias-label-caption)}.YoCQKq_catalogNotice{color:var(--dsw-alias-label-caption);margin:0 0 6px}.YoCQKq_entries{flex-direction:column;gap:4px;margin:0;padding:0;list-style:none;display:flex}.YoCQKq_entry{gap:8px;min-width:0;display:flex}.YoCQKq_entryName{color:var(--dsw-alias-label-secondary);flex:none}.YoCQKq_entryDescription{min-width:0;color:var(--dsw-alias-label-tertiary);text-overflow:ellipsis;white-space:nowrap;flex:auto;overflow:hidden}.YoCQKq_sections{flex-direction:column;gap:8px;margin:0;display:flex}.YoCQKq_section{flex-direction:column;gap:2px;min-width:0;display:flex}.YoCQKq_sectionName{color:var(--dsw-alias-label-caption)}.YoCQKq_sectionText{color:var(--dsw-alias-label-secondary);white-space:pre-wrap;overflow-wrap:anywhere;margin:0}.YoCQKq_relaySender{color:var(--dsw-alias-label-caption);overflow-wrap:anywhere;margin:0 0 6px}.YoCQKq_recalls{flex-direction:column;gap:2px;margin:0 0 8px;padding:0;list-style:none;display:flex}.YoCQKq_recall{gap:8px;min-width:0;display:flex}.YoCQKq_recallLabel{color:var(--dsw-alias-label-secondary);overflow-wrap:anywhere}.YoCQKq_recallCounts{color:var(--dsw-alias-label-caption);flex:none}";
		const tagId$16 = "@deepseek-ai/dsh-client-ui-chat/ContextBody.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$16) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-chat";
			tag.dataset.pluginCss = tagId$16;
			tag.textContent = css$16;
			document.head.appendChild(tag);
		}
		var ContextBody_module_css_default = {
			"catalogNotice": "YoCQKq_catalogNotice",
			"entries": "YoCQKq_entries",
			"entry": "YoCQKq_entry",
			"entryDescription": "YoCQKq_entryDescription",
			"entryName": "YoCQKq_entryName",
			"field": "YoCQKq_field",
			"fieldKey": "YoCQKq_fieldKey",
			"fieldValue": "YoCQKq_fieldValue",
			"fields": "YoCQKq_fields",
			"file": "YoCQKq_file",
			"fileAction": "YoCQKq_fileAction",
			"filePath": "YoCQKq_filePath",
			"files": "YoCQKq_files",
			"recall": "YoCQKq_recall",
			"recallCounts": "YoCQKq_recallCounts",
			"recallLabel": "YoCQKq_recallLabel",
			"recalls": "YoCQKq_recalls",
			"relaySender": "YoCQKq_relaySender",
			"section": "YoCQKq_section",
			"sectionName": "YoCQKq_sectionName",
			"sectionText": "YoCQKq_sectionText",
			"sections": "YoCQKq_sections",
			"text": "YoCQKq_text"
		};
		//#endregion
		//#region src/client/chat/ContextBody.tsx
		/** Model-facing text stays bounded at the disclosure, not at the producer. */
		const MAX_CHARS = 2e4;
		/** Rows a list body materializes before summarizing the remainder. */
		const MAX_ENTRIES = 200;
		/** One durable source narrowed to the readable-record shape; null for anything else. */
		function asRecord$1(value) {
			return typeof value === "object" && value !== null && !Array.isArray(value) ? value : null;
		}
		/**
		* The content blocks as runs, IN THE ORDER the model received them.
		*
		* Adjacent text blocks join with no separator, matching how provider adapters
		* flatten them — inserting a line break would show the reader a line the model
		* never saw. An unknown block breaks the run and keeps its own fallback rather
		* than being hoisted past the text around it or vanishing; the block union is
		* merge-extensible, so a foreign log may interleave shapes this build does not
		* know.
		*/
		function contentRuns(content) {
			const runs = [];
			for (const block of content) {
				if (block.type !== "text") {
					runs.push({ block });
					continue;
				}
				const last = runs[runs.length - 1];
				if (last !== void 0 && "text" in last) last.text += block.text;
				else runs.push({ text: block.text });
			}
			return runs;
		}
		/** Only the blocks this UI version does not know, for bodies that replace the text. */
		function unknownBlocks(content) {
			return contentRuns(content).flatMap((run) => "block" in run ? [run.block] : []);
		}
		/** The model-facing text, truncated to the display bound. */
		function boundedText(text, t) {
			return text.length > MAX_CHARS ? `${text.slice(0, MAX_CHARS)}\n${t("json.truncated", { total: text.length })}` : text;
		}
		/**
		* One source field rendered as a value row; nested shapes stay compact JSON.
		* Bounded on its own, because source fields are as unbounded as the text: an unknown
		* producer may record an arbitrarily large string or array.
		*/
		function fieldValue(value, t) {
			return boundedText(typeof value === "string" ? value : typeof value === "number" || typeof value === "boolean" ? String(value) : JSON.stringify(value), t);
		}
		/**
		* Source fields as a key/value list. `kind` is always omitted because the
		* row header already names the producer. `form` is omitted only when a
		* dedicated body rendered for it — then the presentation the reader is looking
		* at IS that value. On the opaque fallback the declaration is kept, because
		* that is the one place a form this version cannot present would otherwise
		* disappear from the UI entirely.
		*/
		function SourceFields({ source, formRendered, t }) {
			const record = asRecord$1(source);
			if (record === null) return null;
			const hidden = formRendered ? ["kind", "form"] : ["kind"];
			const rows = Object.entries(record).filter(([key]) => !hidden.includes(key));
			if (rows.length === 0) return null;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dl", {
				className: ContextBody_module_css_default.fields,
				"data-context-fields": true,
				children: rows.map(([key, value]) => /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: ContextBody_module_css_default.field,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", {
						className: ContextBody_module_css_default.fieldKey,
						children: key
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", {
						className: ContextBody_module_css_default.fieldValue,
						children: fieldValue(value, t)
					})]
				}, key))
			});
		}
		/**
		* Content blocks this UI version does not know, kept visible rather than
		* dropped: the block union is merge-extensible, so a newer or foreign log may
		* carry a shape this build has no presentation for.
		* @param props - The unrecognized blocks and the locale seat.
		* @returns One generic JSON block per unknown entry.
		*/
		function UnknownBlocks({ blocks, t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(react_jsx_runtime.Fragment, { children: blocks.map((block, index) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.JsonBlock, {
				label: t("message.unknownBlock"),
				payload: block,
				truncatedLabel: (total) => t("json.truncated", { total })
			}, index)) });
		}
		/**
		* The model-facing content of one context, shared by every form that shows it:
		* the text with its real line breaks, then any block this UI version does not
		* know, which keeps its own fallback rather than vanishing.
		* @param props - Durable content and the locale seat.
		* @returns The content blocks as the model received them.
		*/
		function ModelFacingContent({ content, t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(react_jsx_runtime.Fragment, { children: contentRuns(content).map((run, index) => "text" in run ? run.text !== "" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("pre", {
				className: ContextBody_module_css_default.text,
				"data-context-text": true,
				children: boundedText(run.text, t)
			}, index) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.JsonBlock, {
				label: t("message.unknownBlock"),
				payload: run.block,
				truncatedLabel: (total) => t("json.truncated", { total })
			}, index)) });
		}
		/**
		* Default presentation: the model-facing text as text, with its real line
		* breaks, and the remaining source fields beneath it. This is what every form
		* this UI version does not recognize renders as.
		* @param props - Durable content, its source, and the locale seat.
		* @returns The opaque context body.
		*/
		function OpaqueBody({ content, source, t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(ModelFacingContent, {
				content,
				t
			}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(SourceFields, {
				source,
				formRendered: false,
				t
			})] });
		}
		/**
		* Instruction changes read off the source, or null when the record is not a
		* usable instruction list.
		*
		* The read is all-or-nothing: silently dropping one unreadable entry would show
		* a confident, incomplete file list for a log this version cannot fully read.
		* Paths are deduplicated in first-seen order, matching how the header label is
		* derived from the same array.
		*/
		function instructionChanges(source) {
			const record = asRecord$1(source);
			const list = record === null ? void 0 : record["changes"];
			if (!Array.isArray(list)) return null;
			const changes = [];
			const seen = /* @__PURE__ */ new Set();
			for (const entry of list) {
				const change = asRecord$1(entry);
				if (change === null) return null;
				const path = change["path"];
				if (typeof path !== "string" || path === "") return null;
				const action = change["action"];
				if (action !== "set" && action !== "replace" && action !== "remove") return null;
				const digest = change["digest"];
				if (seen.has(path)) continue;
				seen.add(path);
				changes.push({
					action,
					path,
					...typeof digest === "string" ? { digest } : {}
				});
			}
			return changes.length === 0 ? null : changes;
		}
		/**
		* Locale key for one reconciled file. The baseline loads a file; a later delta
		* distinguishes a newly reconciled path from a rewritten one, which `set` and
		* `replace` already separate at the producer.
		* @param action - the durable change action.
		* @param baseline - whether this context is the startup/resume baseline.
		* @returns the key naming what happened to that file.
		*/
		function instructionAction(action, baseline) {
			if (action === "remove") return "message.context.instructions.removed";
			if (baseline) return "message.context.instructions.loaded";
			return action === "set" ? "message.context.instructions.added" : "message.context.instructions.updated";
		}
		/**
		* `instructions` form: the files this context reconciled, then their text.
		*
		* The text keeps its `<system-reminder>` framing verbatim — the framing is part
		* of what the model read, so hiding it would misreport the request.
		* @param props - Durable content, its source, and the locale seat.
		* @returns The instructions context body, or the opaque body when the change
		* list is unreadable.
		*/
		function InstructionsBody({ content, source, t }) {
			const changes = instructionChanges(source);
			if (changes === null) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(OpaqueBody, {
				content,
				source,
				t
			});
			const baseline = asRecord$1(source)?.["baseline"] === true;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("ul", {
				className: ContextBody_module_css_default.files,
				"data-context-files": true,
				children: changes.map((change) => /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("li", {
					className: ContextBody_module_css_default.file,
					title: change.digest,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: ContextBody_module_css_default.filePath,
						children: change.path
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: ContextBody_module_css_default.fileAction,
						children: t(instructionAction(change.action, baseline))
					})]
				}, change.path))
			}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ModelFacingContent, {
				content,
				t
			})] });
		}
		/**
		* Catalog entries read off the source, or null when the record is not a usable
		* catalog. All-or-nothing for the same reason as the instruction list: this body
		* replaces the model-facing text, so a partial list would hide the only complete
		* account of what the model read.
		*/
		function catalogEntries(source) {
			const record = asRecord$1(source);
			const list = record === null ? void 0 : record["entries"];
			if (!Array.isArray(list)) return null;
			const entries = [];
			for (const item of list) {
				const entry = asRecord$1(item);
				if (entry === null) return null;
				const name = entry["name"];
				const description = entry["description"];
				if (typeof name !== "string" || name === "" || typeof description !== "string") return null;
				entries.push({
					name,
					description
				});
			}
			return entries;
		}
		/**
		* `catalog` form: the published entries as a list, read from the source rather
		* than re-parsed out of the model-facing prose.
		*
		* A catalog whose source carries no usable entries falls through to the opaque
		* body, so an older or hand-edited log still shows its text.
		* @param props - Durable content, its source, and the locale seat.
		* @returns The catalog context body, or the opaque body when the entry list is
		* unreadable.
		*/
		function CatalogBody({ content, source, t }) {
			const entries = catalogEntries(source);
			if (entries === null) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(OpaqueBody, {
				content,
				source,
				t
			});
			const update = asRecord$1(source)?.["update"] === true;
			const shown = entries.slice(0, MAX_ENTRIES);
			const rest = unknownBlocks(content);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [
				update && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
					className: ContextBody_module_css_default.catalogNotice,
					"data-context-catalog-update": true,
					children: t("message.context.catalog.replaced")
				}),
				/* @__PURE__ */ (0, react_jsx_runtime.jsx)("ul", {
					className: ContextBody_module_css_default.entries,
					"data-context-entries": true,
					children: shown.map((entry, index) => /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("li", {
						className: ContextBody_module_css_default.entry,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("code", {
							className: ContextBody_module_css_default.entryName,
							children: entry.name
						}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: ContextBody_module_css_default.entryDescription,
							children: entry.description
						})]
					}, index))
				}),
				shown.length < entries.length && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
					className: ContextBody_module_css_default.catalogNotice,
					"data-context-entries-truncated": true,
					children: t("message.context.catalog.more", { count: entries.length - shown.length })
				}),
				/* @__PURE__ */ (0, react_jsx_runtime.jsx)(UnknownBlocks, {
					blocks: rest,
					t
				})
			] });
		}
		/** Snapshot sections read off the source, or null when the record is unusable. */
		function snapshotSections(source) {
			const record = asRecord$1(source);
			const list = record === null ? void 0 : record["sections"];
			if (!Array.isArray(list)) return null;
			const sections = [];
			for (const item of list) {
				const section = asRecord$1(item);
				if (section === null) return null;
				const name = section["name"];
				const text = section["text"];
				if (typeof name !== "string" || name === "" || typeof text !== "string") return null;
				sections.push({
					name,
					text
				});
			}
			return sections.length === 0 ? null : sections;
		}
		/**
		* `snapshot` form: the named contributions this snapshot assembled, in order.
		*
		* The sections are the same bytes the model read, split at the boundaries the
		* producer assembled them on, so a reader sees which subsystem contributed
		* which state instead of one undifferentiated wall.
		*
		* One sentence of the model-facing text is NOT in any section: the producer's
		* framing line declaring that this snapshot supersedes earlier ones. Unlike the
		* `<system-reminder>` wrapper an instruction context carries — which wraps
		* content and cannot be separated from it — that line states the form's own
		* semantics, so the body states them as a caption instead of reprinting the
		* joined prose beside the sections it was split from.
		* @param props - Durable content, its source, and the locale seat.
		* @returns The snapshot context body, or the opaque body when unreadable.
		*/
		function SnapshotBody({ content, source, t }) {
			const sections = snapshotSections(source);
			/* v8 ignore next -- contextBody reads the sections before choosing this body. */
			if (sections === null) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(OpaqueBody, {
				content,
				source,
				t
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
				className: ContextBody_module_css_default.catalogNotice,
				"data-context-snapshot-supersedes": true,
				children: t("message.context.snapshot.supersedes")
			}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dl", {
				className: ContextBody_module_css_default.sections,
				"data-context-sections": true,
				children: sections.map((section, index) => /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: ContextBody_module_css_default.section,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", {
						className: ContextBody_module_css_default.sectionName,
						children: section.name
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", {
						className: ContextBody_module_css_default.sectionText,
						children: boundedText(section.text, t)
					})]
				}, index))
			})] });
		}
		/**
		* `notice` form: what just happened, with the model-facing text beneath it.
		*
		* The one-line account also rides the collapsed row ({@link contextBody}), so a
		* notice is usually readable without expanding at all.
		* @param props - Durable content, its source, and the locale seat.
		* @returns The notice context body.
		*/
		function NoticeBody({ content, t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ModelFacingContent, {
				content,
				t
			});
		}
		/**
		* `relay` form: which agent sent this, then what it said.
		*
		* The sender is an opaque session id; it is shown as a field rather than a
		* label, because this client cannot resolve it to a title.
		* @param props - Durable content, its source, and the locale seat.
		* @returns The relay context body.
		*/
		function RelayBody({ content, source, t }) {
			const sender = relaySender(source);
			/* v8 ignore next -- contextBody resolves the sender before choosing this body. */
			if (sender === null) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(OpaqueBody, {
				content,
				source,
				t
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
				className: ContextBody_module_css_default.relaySender,
				"data-context-relay-sender": true,
				children: t("message.context.relay.from", { session: sender })
			}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ModelFacingContent, {
				content,
				t
			})] });
		}
		/** The sending agent's session id, or null when the record does not name one. */
		function relaySender(source) {
			const sender = asRecord$1(source)?.["senderSessionId"];
			return typeof sender === "string" && sender !== "" ? sender : null;
		}
		/** Recalled sessions read off the source, or null when the record is unusable. */
		function recalledSessions(source) {
			const record = asRecord$1(source);
			const list = record === null ? void 0 : record["references"];
			if (!Array.isArray(list)) return null;
			const sessions = [];
			for (const item of list) {
				const reference = asRecord$1(item);
				if (reference === null) return null;
				const label = reference["label"];
				const retained = reference["retainedMessages"];
				const omitted = reference["omittedMessages"];
				const truncated = reference["truncated"];
				if (typeof label !== "string" || label === "" || typeof retained !== "number" || typeof omitted !== "number" || typeof truncated !== "boolean") return null;
				sessions.push({
					label,
					retained,
					omitted,
					truncated
				});
			}
			return sessions.length === 0 ? null : sessions;
		}
		/**
		* `recall` form: which sessions this material came from and how much of each
		* survived the read, then the material itself.
		*
		* Completeness is the fact a reader needs first: recalled context is bounded on
		* the way in, so a card that hid the omitted count would overstate what the
		* model received.
		* @param props - Durable content, its source, and the locale seat.
		* @returns The recall context body, or the opaque body when unreadable.
		*/
		function RecallBody({ content, source, t }) {
			const sessions = recalledSessions(source);
			if (sessions === null) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(OpaqueBody, {
				content,
				source,
				t
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("ul", {
				className: ContextBody_module_css_default.recalls,
				"data-context-recalls": true,
				children: sessions.map((session, index) => /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("li", {
					className: ContextBody_module_css_default.recall,
					children: [
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: ContextBody_module_css_default.recallLabel,
							children: session.label
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: ContextBody_module_css_default.recallCounts,
							children: t("message.context.recall.counts", {
								retained: session.retained,
								omitted: session.omitted
							})
						}),
						session.truncated && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: ContextBody_module_css_default.recallCounts,
							children: t("message.context.recall.truncated")
						})
					]
				}, index))
			}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ModelFacingContent, {
				content,
				t
			})] });
		}
		/** The one-line account a `notice` puts on its collapsed row, when it records one. */
		function noticeSummary(source) {
			const summary = asRecord$1(source)?.["summary"];
			return typeof summary === "string" && summary !== "" ? summary : null;
		}
		/**
		* Choose the body for one context node.
		*
		* Returns the form the body actually rendered as, which is not always the
		* declared one: a declared form whose fields are unreadable falls back to
		* opaque, and the caller labels the row with what it really shows.
		* `summary` is the collapsed row's one-line account, which only a `notice`
		* records: its whole point is being readable without expanding.
		* @param form - the producer-declared form projected onto the node.
		* @param props - durable content, its source, and the locale seat.
		* @returns the rendered form (null for opaque), its collapsed summary, and its body.
		*/
		function contextBody(form, props) {
			const opaque = {
				rendered: null,
				summary: null,
				body: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(OpaqueBody, { ...props })
			};
			switch (form) {
				case "instructions": return instructionChanges(props.source) === null ? opaque : {
					rendered: "instructions",
					summary: null,
					body: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(InstructionsBody, { ...props })
				};
				case "catalog": return catalogEntries(props.source) === null ? opaque : {
					rendered: "catalog",
					summary: null,
					body: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(CatalogBody, { ...props })
				};
				case "snapshot": return snapshotSections(props.source) === null ? opaque : {
					rendered: "snapshot",
					summary: null,
					body: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(SnapshotBody, { ...props })
				};
				case "notice": {
					const summary = noticeSummary(props.source);
					return summary === null ? opaque : {
						rendered: "notice",
						summary,
						body: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(NoticeBody, { ...props })
					};
				}
				case "relay": return relaySender(props.source) === null ? opaque : {
					rendered: "relay",
					summary: null,
					body: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(RelayBody, { ...props })
				};
				case "recall": return recalledSessions(props.source) === null ? opaque : {
					rendered: "recall",
					summary: null,
					body: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(RecallBody, { ...props })
				};
				case null: return opaque;
				/* v8 ignore next 4 -- closed-union backstop; the compiler rejects a new
				KnownContextForm here rather than letting it degrade to opaque silently. */
				default: throw new Error(`unreachable context form: ${String(form)}`);
			}
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-chat/src/client/chat/ContextInjectionRow.module.css.mjs
		const css$15 = ".Rf-PHW_root{min-width:0}.Rf-PHW_root[data-open]{padding-bottom:4px}.Rf-PHW_chevron{color:var(--dsw-alias-label-secondary)}.Rf-PHW_sep{background:var(--dsw-alias-label-caption);border-radius:1px;flex:none;width:2px;height:2px;margin:0 8px}.Rf-PHW_source{min-width:0;color:var(--dsw-alias-label-tertiary);font-size:var(--dsh-content-font-size-secondary,13px);line-height:calc(24px + var(--dsh-content-font-delta,0px));text-overflow:ellipsis;white-space:nowrap;flex:none;overflow:hidden}.Rf-PHW_summary{min-width:0;color:var(--dsw-alias-label-tertiary);font-size:var(--dsh-content-font-size-secondary,13px);line-height:calc(24px + var(--dsh-content-font-delta,0px));text-overflow:ellipsis;white-space:nowrap;flex:auto;overflow:hidden}.Rf-PHW_body{box-sizing:border-box;width:calc(100% - 22px - var(--dsh-content-font-delta,0px));max-height:141px;margin:4px 0 0 calc(22px + var(--dsh-content-font-delta,0px));border-radius:var(--dsw-radius-md);background:var(--dsw-alias-markdown-code-block);color:var(--dsw-alias-label-tertiary);font:400 11px/16px var(--ds-font-family-code);border:none;padding:10px 16px 12px 12px;overflow:auto}.Rf-PHW_toolChanges{white-space:nowrap}";
		const tagId$15 = "@deepseek-ai/dsh-client-ui-chat/ContextInjectionRow.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$15) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-chat";
			tag.dataset.pluginCss = tagId$15;
			tag.textContent = css$15;
			document.head.appendChild(tag);
		}
		var ContextInjectionRow_module_css_default = {
			"body": "Rf-PHW_body",
			"chevron": "Rf-PHW_chevron",
			"root": "Rf-PHW_root",
			"sep": "Rf-PHW_sep",
			"source": "Rf-PHW_source",
			"summary": "Rf-PHW_summary",
			"toolChanges": "Rf-PHW_toolChanges"
		};
		//#endregion
		//#region src/client/chat/ContextInjectionRow.tsx
		/**
		* Render logged context with the Tool calls disclosure chrome from Figma.
		*
		* The header names the role the context plays and, beside it, the producer the
		* durable source identifies, so a reader can tell an injected skill catalog
		* from a workspace instruction file or a recalled session without expanding.
		* The expanded body follows the producer-declared form; an absent or unknown
		* form renders the opaque body.
		* @param props - Durable content, its projected producer role/name and form, and the locale seat.
		* @returns A collapsed context row with a bounded, form-specific body.
		*/
		function ContextInjectionRow({ content, source, producer, form, t }) {
			const [open, setOpen] = (0, react.useState)(false);
			const { rendered, summary, body } = contextBody(form, {
				content,
				source,
				t
			});
			const toolBlocks = content.length > 0 && content.every((block) => block.type === "tool-addition" || block.type === "tool-removal") ? content : void 0;
			const added = toolBlocks?.flatMap((block) => block.type === "tool-addition" ? [block.toolName] : []) ?? [];
			const removed = toolBlocks?.flatMap((block) => block.type === "tool-removal" ? [block.toolName] : []) ?? [];
			const single = toolBlocks?.length === 1 ? toolBlocks[0] : void 0;
			const toolSummary = toolBlocks === void 0 || single !== void 0 ? null : added.length > 0 && removed.length > 0 ? t("message.toolsChanged", {
				added: added.length,
				removed: removed.length
			}) : added.length > 0 ? t("message.toolsAddedCount", { count: added.length }) : t("message.toolsRemovedCount", { count: removed.length });
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.DisclosureRow, {
				className: ContextInjectionRow_module_css_default.root,
				icon: toolBlocks !== void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconBrowseOutlineRegular, { size: 14 }) : producer.role === "recall" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
					"data-context-recall-icon": true,
					children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.ReferenceIconRegular, { kind: "session" })
				}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconContextInjectionOutlineRegular, { size: 14 }),
				chevronClassName: ContextInjectionRow_module_css_default.chevron,
				title: single !== void 0 ? t(single.type === "tool-addition" ? "message.toolAdded" : "message.toolRemoved", { name: single.toolName }) : t(toolBlocks !== void 0 ? "message.toolsUpdated" : producer.role === "recall" ? "message.contextRecall" : "message.contextInjection"),
				collapsedContent: toolSummary !== null ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
					className: ContextInjectionRow_module_css_default.sep,
					"aria-hidden": true
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
					className: ContextInjectionRow_module_css_default.summary,
					children: toolSummary
				})] }) : toolBlocks !== void 0 || producer.label === null ? void 0 : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: ContextInjectionRow_module_css_default.sep,
						"aria-hidden": true
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: ContextInjectionRow_module_css_default.source,
						"data-context-source": true,
						children: producer.label
					}),
					summary !== null && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: ContextInjectionRow_module_css_default.sep,
						"aria-hidden": true
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: ContextInjectionRow_module_css_default.summary,
						"data-context-summary": true,
						children: summary
					})] })
				] }),
				keepContentWhenOpen: true,
				open: open && single === void 0,
				expandable: single === void 0,
				expandOnRowClick: true,
				onToggle: () => {
					setOpen((value) => !value);
				},
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
					className: ContextInjectionRow_module_css_default.body,
					"data-context-injection-body": true,
					"data-context-form": rendered ?? void 0,
					children: toolBlocks === void 0 ? body : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: ContextInjectionRow_module_css_default.toolChanges,
						children: [added.length > 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", { children: t("message.toolsAdded", { names: added.join(", ") }) }), removed.length > 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", { children: t("message.toolsRemoved", { names: removed.join(", ") }) })]
					})
				})
			});
		}
		//#endregion
		//#region src/client/chat/message-chrome.ts
		/** Refresh interval for whole-second live run clocks. */
		const LIVE_RUN_CLOCK_INTERVAL_MS = 1e3;
		function pad2(n) {
			return String(n).padStart(2, "0");
		}
		/**
		* Local calendar-day epoch (ms at local midnight) for an instant.
		* @param ms - Unix epoch ms.
		* @returns Midnight of that local calendar day.
		*/
		function startOfLocalDay(ms) {
			const d = new Date(ms);
			d.setHours(0, 0, 0, 0);
			return d.getTime();
		}
		/**
		* Delay until the next local midnight after `ms` (at least 1ms).
		* @param ms - Unix epoch ms.
		* @returns Milliseconds until the following local midnight.
		*/
		function msUntilNextLocalMidnight(ms) {
			const next = new Date(ms);
			next.setHours(24, 0, 0, 0);
			return Math.max(next.getTime() - ms, 1);
		}
		/**
		* Localized elapsed-time label for the running conversation clock.
		* @param ms - Elapsed duration in milliseconds (negatives clamp to zero).
		* @param t - Translate seat supplying the duration templates.
		* @returns Display string in whole seconds; minutes and seconds once the
		* duration reaches a minute; hours, minutes, and seconds once it reaches an
		* hour, with the smaller units zero-padded.
		*/
		function formatRunDuration(ms, t) {
			const total = Math.max(0, Math.floor(ms / 1e3));
			const hours = Math.floor(total / 3600);
			const minutes = Math.floor(total / 60) % 60;
			const seconds = total % 60;
			if (hours > 0) return t("duration.hours", {
				hours,
				minutes: pad2(minutes),
				seconds: pad2(seconds)
			});
			return minutes > 0 ? t("duration.minutes", {
				minutes,
				seconds: pad2(seconds)
			}) : t("duration.seconds", { seconds });
		}
		/**
		* Localized live elapsed time without padded seconds or early rollover.
		* @param ms - Elapsed duration in milliseconds (negatives clamp to zero).
		* @param t - Translate seat supplying the duration templates.
		* @returns Whole seconds without a leading zero; minutes start at 60 seconds
		* and hours start at exactly 60 minutes.
		*/
		function formatLiveRunDuration(ms, t) {
			const totalSeconds = Math.max(0, Math.floor(ms / 1e3));
			const hours = Math.floor(totalSeconds / 3600);
			const minutes = Math.floor(totalSeconds / 60) % 60;
			const seconds = String(totalSeconds % 60);
			if (hours > 0) return t("duration.hours", {
				hours,
				minutes: pad2(minutes),
				seconds
			});
			return minutes > 0 ? t("duration.minutes", {
				minutes,
				seconds
			}) : t("duration.seconds", { seconds });
		}
		/**
		* Decode-throughput figure: whole tokens from ten up, one decimal below.
		* @param tps - Tokens per second.
		* @returns Display number without unit.
		*/
		function formatTokensPerSecond(tps) {
			const clamped = Math.max(0, tps);
			return clamped >= 10 ? String(Math.round(clamped)) : String(Math.round(clamped * 10) / 10);
		}
		/**
		* Compact local timestamp for message IconActions. Same calendar day →
		* `HH:mm`; earlier this year → the `clock.md` date template + clock; other
		* years → the `clock.ymd` template + clock. Pure: the date templates arrive
		* through the caller's locale seat.
		* @param time - Unix epoch ms from the source session event.
		* @param t - translate seat supplying the `clock.md` / `clock.ymd` templates.
		* @param now - Reference instant for the day/year cut (defaults to wall clock).
		* @returns Date-aware clock string (24-hour, zero-padded time).
		*/
		function formatMessageClock(time, t, now = Date.now()) {
			const d = new Date(time);
			const n = new Date(now);
			const clock = `${pad2(d.getHours())}:${pad2(d.getMinutes())}`;
			if (d.getFullYear() === n.getFullYear() && d.getMonth() === n.getMonth() && d.getDate() === n.getDate()) return clock;
			const params = {
				y: d.getFullYear(),
				m: d.getMonth() + 1,
				d: d.getDate()
			};
			return `${d.getFullYear() === n.getFullYear() ? t("clock.md", params) : t("clock.ymd", params)} ${clock}`;
		}
		//#endregion
		//#region src/client/chat/use-calendar-day.ts
		/**
		* Local calendar-day epoch that advances at each local midnight.
		* @returns Midnight ms for the current local day; updates after the boundary.
		*/
		function useCalendarDay() {
			const [day, setDay] = (0, react.useState)(() => startOfLocalDay(Date.now()));
			(0, react.useEffect)(() => {
				let timer;
				const arm = () => {
					const now = Date.now();
					setDay(startOfLocalDay(now));
					timer = setTimeout(arm, msUntilNextLocalMidnight(now));
				};
				timer = setTimeout(arm, msUntilNextLocalMidnight(Date.now()));
				return () => {
					clearTimeout(timer);
				};
			}, []);
			return day;
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-chat/src/client/chat/MessageIconActions.module.css.mjs
		const css$14 = ".s8xVLq_actions{height:calc(28px + var(--dsh-content-font-delta,0px));align-items:center;gap:8px;display:flex}.s8xVLq_timeStart{font-size:var(--dsh-content-font-size-secondary,13px);line-height:calc(24px + var(--dsh-content-font-delta,0px));color:var(--dsw-alias-label-tertiary);white-space:nowrap;padding-right:12px}.s8xVLq_timeEnd{font-size:calc(var(--dsh-content-font-size-secondary,13px) - 1px);line-height:calc(24px + var(--dsh-content-font-delta,0px));color:inherit;white-space:nowrap}.s8xVLq_endInfo{min-width:0;color:var(--dsw-alias-label-tertiary);align-items:center;gap:8px;margin-left:8px;display:inline-flex}@media (hover:hover){[data-actions-reveal=hover] .s8xVLq_actions,:is([data-chat-flow-kind=user],[data-chat-flow-kind=steering]):has(~:is([data-chat-flow-kind=user],[data-chat-flow-kind=steering])) .s8xVLq_actions{opacity:0;transition:opacity 80ms}[data-actions-reveal=hover]:hover .s8xVLq_actions,[data-actions-reveal=hover]:focus-within .s8xVLq_actions,:is([data-chat-flow-kind=user],[data-chat-flow-kind=steering]):has(~:is([data-chat-flow-kind=user],[data-chat-flow-kind=steering])):hover .s8xVLq_actions,:is([data-chat-flow-kind=user],[data-chat-flow-kind=steering]):has(~:is([data-chat-flow-kind=user],[data-chat-flow-kind=steering])):focus-within .s8xVLq_actions{opacity:1}}.s8xVLq_action{width:calc(28px + var(--dsh-content-font-delta,0px));height:calc(28px + var(--dsh-content-font-delta,0px));border-radius:var(--dsw-radius-sm);color:var(--dsw-alias-label-tertiary);cursor:pointer;background:0 0;border:none;justify-content:center;align-items:center;padding:6px;display:inline-flex}.s8xVLq_action svg{width:calc(15px + var(--dsh-content-font-delta,0px));height:calc(15px + var(--dsh-content-font-delta,0px))}.s8xVLq_actions[data-clock=end] .s8xVLq_action svg{width:calc(17px + var(--dsh-content-font-delta,0px));height:calc(17px + var(--dsh-content-font-delta,0px))}.s8xVLq_action:hover{background:var(--dsw-alias-interactive-bg-hover);color:var(--dsw-alias-label-secondary)}.s8xVLq_action[data-unavailable]{cursor:default;opacity:.4}.s8xVLq_action[data-unavailable]:hover{color:var(--dsw-alias-label-tertiary);background:0 0}.s8xVLq_visuallyHidden{clip:rect(0 0 0 0);white-space:nowrap;width:1px;height:1px;position:absolute;overflow:hidden}";
		const tagId$14 = "@deepseek-ai/dsh-client-ui-chat/MessageIconActions.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$14) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-chat";
			tag.dataset.pluginCss = tagId$14;
			tag.textContent = css$14;
			document.head.appendChild(tag);
		}
		var MessageIconActions_module_css_default = {
			"action": "s8xVLq_action",
			"actions": "s8xVLq_actions",
			"endInfo": "s8xVLq_endInfo",
			"timeEnd": "s8xVLq_timeEnd",
			"timeStart": "s8xVLq_timeStart",
			"visuallyHidden": "s8xVLq_visuallyHidden"
		};
		//#endregion
		//#region src/client/chat/MessageIconActions.tsx
		/**
		* Copy / branch (/ clock) IconActions row shared by user and assistant chrome.
		* @param props - Copy text, event time, clock side, branch callback, className.
		* @returns The actions row element.
		*/
		function MessageIconActions({ text, time, clock, onBranch, branchUnavailable = false, className, extraActions, usageAction, t }) {
			const day = useCalendarDay();
			const reasonId = (0, react.useId)();
			const [copied, setCopied] = (0, react.useState)(false);
			const copyPending = (0, react.useRef)(false);
			const copyTimer = (0, react.useRef)(null);
			const copyEpoch = (0, react.useRef)(0);
			(0, react.useEffect)(() => () => {
				copyEpoch.current += 1;
				copyPending.current = false;
				if (copyTimer.current !== null) clearTimeout(copyTimer.current);
			}, []);
			const onCopy = (0, react.useCallback)(() => {
				if (copied || copyPending.current) return;
				const epoch = copyEpoch.current;
				copyPending.current = true;
				(0, _deepseek_ai_dsh_client_ui_primitives.writeClipboard)(text).then((ok) => {
					if (epoch !== copyEpoch.current) return;
					copyPending.current = false;
					if (!ok) return;
					setCopied(true);
					copyTimer.current = window.setTimeout(() => {
						copyTimer.current = null;
						setCopied(false);
					}, 1e3);
				});
			}, [copied, text]);
			const clockEl = time === void 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
				className: clock === "start" ? MessageIconActions_module_css_default.timeStart : MessageIconActions_module_css_default.timeEnd,
				children: formatMessageClock(time, t, day)
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: className === void 0 ? MessageIconActions_module_css_default.actions : `${MessageIconActions_module_css_default.actions} ${className}`,
				"data-clock": clock,
				children: [
					clock === "start" ? clockEl : null,
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
						label: copied ? t("copied") : t("copy"),
						side: "bottom",
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
							type: "button",
							className: MessageIconActions_module_css_default.action,
							"aria-label": copied ? t("copied") : t("copy"),
							onClick: onCopy,
							children: copied ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconCheckOutlineRegular, {}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconCopyOutlineRegular, {})
						})
					}),
					extraActions,
					onBranch !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
						label: branchUnavailable ? t("message.branchUnavailable") : t("message.branch"),
						side: "bottom",
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
							type: "button",
							className: MessageIconActions_module_css_default.action,
							"aria-label": t("message.branch"),
							"aria-disabled": branchUnavailable || void 0,
							"aria-describedby": branchUnavailable ? reasonId : void 0,
							"data-unavailable": branchUnavailable || void 0,
							onClick: branchUnavailable ? void 0 : onBranch,
							children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconBranchOutlineRegular, {})
						})
					}),
					onBranch !== void 0 && branchUnavailable && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						id: reasonId,
						className: MessageIconActions_module_css_default.visuallyHidden,
						children: t("message.branchUnavailable")
					}),
					clock === "end" ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
						className: MessageIconActions_module_css_default.endInfo,
						children: [usageAction, clockEl]
					}) : usageAction
				]
			});
		}
		//#endregion
		//#region src/client/chat/MessageItem.tsx
		function contentParts(content) {
			const texts = [];
			const attachments = [];
			const rest = [];
			for (const block of content) {
				const b = block;
				if (b.type === "text" && typeof b.text === "string") texts.push(b.text);
				else if (b.type === "image" && b.attachment !== void 0) attachments.push({
					type: "image",
					image: { attachment: b.attachment }
				});
				else if (b.type === "file" && b.attachment !== void 0) attachments.push({
					type: "file",
					file: b.attachment
				});
				else rest.push(block);
			}
			return {
				text: texts.join(""),
				attachments,
				rest
			};
		}
		function retrySeconds(milliseconds) {
			return Math.max(1, Math.ceil(milliseconds / 1e3));
		}
		function failureMessage(message, code, t) {
			if (code === "ACCOUNT_SIGNED_OUT") return t("message.failure.accountSignedOut");
			if (code === "ACCOUNT_SIGN_IN_REQUIRED") return t("message.failure.accountSignInRequired");
			if (code === "QUOTA" || code === "ACCOUNT_QUOTA") return t("message.failure.quota");
			return code === "AUTH" ? t("message.failure.auth") : message;
		}
		function ModelRetryItem({ node, active, t }) {
			const deadline = (0, react.useMemo)(() => Date.now() + node.delayMs, [node.delayMs, node.seq]);
			const scheduledSeconds = retrySeconds(node.delayMs);
			const maximum = node.mode === "normal" ? node.maxRetries : "∞";
			const [countdown, setCountdown] = (0, react.useState)(() => ({
				deadline,
				seconds: retrySeconds(deadline - Date.now())
			}));
			const remainingSeconds = countdown.deadline === deadline ? countdown.seconds : retrySeconds(deadline - Date.now());
			(0, react.useEffect)(() => {
				if (!active) return;
				const updateCountdown = () => {
					const next = retrySeconds(deadline - Date.now());
					setCountdown((current) => current.deadline === deadline && current.seconds === next ? current : {
						deadline,
						seconds: next
					});
					return next;
				};
				if (updateCountdown() === 1) return;
				const timer = window.setInterval(() => {
					if (updateCountdown() === 1) window.clearInterval(timer);
				}, 250);
				return () => {
					window.clearInterval(timer);
				};
			}, [active, deadline]);
			const label = active ? t("message.retry.active") : node.retryState === "cancelled" ? t("message.retry.cancelled") : node.retryState === "started" ? t("message.retry.started") : t("message.retry.scheduled");
			const seconds = active ? remainingSeconds : scheduledSeconds;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("details", {
				className: MessageItem_module_css_default.retryRow,
				"data-active": active || void 0,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("summary", {
					className: MessageItem_module_css_default.retrySummary,
					children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: MessageItem_module_css_default.retryText,
						role: "status",
						children: t("message.retry.status", {
							label,
							retry: node.retry,
							maximum,
							seconds
						})
					})
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: MessageItem_module_css_default.retryDetails,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: MessageItem_module_css_default.retryDetailLabel,
						children: t("message.retry.delay")
					}), t("duration.milliseconds", { milliseconds: Math.round(node.delayMs) })] }), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: MessageItem_module_css_default.retryDetailLabel,
						children: t("message.retry.failure")
					}), failureMessage(node.failure.message, node.failure.code, t)] })]
				})]
			});
		}
		/** Persistent, turn-positioned feedback for a terminal failure. */
		function TurnErrorItem({ node, t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: MessageItem_module_css_default.turnErrorRow,
				role: "status",
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, {
						state: "error",
						className: MessageItem_module_css_default.turnErrorDot
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: MessageItem_module_css_default.turnErrorCopy,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: MessageItem_module_css_default.turnErrorTitle,
							children: node.code === "ACCOUNT_SIGNED_OUT" ? t("message.accountStopped") : t("message.turnError")
						}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: MessageItem_module_css_default.turnErrorMessage,
							children: failureMessage(node.message, node.code, t)
						})]
					}),
					node.code !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("code", {
						className: MessageItem_module_css_default.turnErrorCode,
						children: node.code
					})
				]
			});
		}
		/** Persistent, turn-positioned notice for a turn ended at the output-token cap. */
		function TurnMaxTokensItem({ t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: MessageItem_module_css_default.turnErrorRow,
				role: "status",
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, {
					state: "warning",
					className: MessageItem_module_css_default.turnErrorDot
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: MessageItem_module_css_default.turnErrorCopy,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: MessageItem_module_css_default.maxTokensTitle,
						children: t("message.maxTokens")
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: MessageItem_module_css_default.turnErrorMessage,
						children: t("message.maxTokens.hint")
					})]
				})]
			});
		}
		/** Right-aligned bubble shared by user and steering rows. */
		function UserStyleBubble({ content, renderMessageImages, actions, pending = false, echo = false, referenceLabels = [], skillNames = [], previewAttachments, references, t }) {
			const { text, attachments: contentAttachments, rest } = contentParts(content);
			const attachments = previewAttachments ?? contentAttachments;
			const compactImages = attachments.length > 1;
			const truncated = (total) => t("json.truncated", { total });
			const showBubble = text !== "" || rest.length > 0;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: MessageItem_module_css_default.userRow,
				"data-pending-steering": pending || void 0,
				"data-submission-echo": echo || void 0,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: MessageItem_module_css_default.userStack,
					children: [
						attachments.length > 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: MessageItem_module_css_default.attachmentRow,
							"data-message-attachments": true,
							children: attachments.map((attachment, index) => attachment.type === "image" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(react.Fragment, { children: renderMessageImages({
								images: [attachment.image],
								align: "end",
								compact: compactImages
							}) }, `image:${index}`) : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
								className: MessageItem_module_css_default.fileCard,
								title: attachment.file.name,
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.FileTypeIcon, {
									path: attachment.file.name,
									className: MessageItem_module_css_default.fileIcon
								}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
									className: MessageItem_module_css_default.fileContent,
									children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
										className: MessageItem_module_css_default.fileName,
										children: attachment.file.name
									}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
										className: MessageItem_module_css_default.fileMeta,
										children: [(0, _deepseek_ai_dsh_client_ui_primitives.fileExtension)(attachment.file.name).toUpperCase().slice(0, 8), (0, _deepseek_ai_dsh_client_ui_primitives.fileSizeText)(attachment.file.bytes)].filter(Boolean).join(" ")
									})]
								})]
							}, `file:${index}`))
						}),
						showBubble && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							className: MessageItem_module_css_default.bubble,
							children: [(0, _deepseek_ai_dsh_client_ui_primitives.projectUserText)(text, referenceLabels, skillNames, "skill", references), rest.map((block, i) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.JsonBlock, {
								label: t("message.extraBlock"),
								payload: block,
								truncatedLabel: truncated
							}, i))]
						}),
						referenceLabels.length > 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: MessageItem_module_css_default.referenceSummary,
							children: t("message.referenceSummary", { labels: referenceLabels.join(t("message.referenceSeparator")) })
						})
					]
				}), actions?.(text)]
			});
		}
		/**
		* Render one Host-authoritative pending steering item with the same visual
		* language as its eventual durable transcript node.
		* @param props - Pending message content and conversation translator.
		* @returns the pending steering bubble.
		*/
		function PendingSteeringBubble({ content, renderMessageImages, t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(UserStyleBubble, {
				content,
				renderMessageImages,
				pending: true,
				t,
				actions: (text) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)(MessageIconActions, {
					text,
					clock: "start",
					className: MessageItem_module_css_default.actions,
					t
				})
			});
		}
		/**
		* Render one local transcript or steering submission echo with the same
		* visual language and surface marker as the Host occurrence that replaces
		* it: draft text plus object-URL previews, visible from the submit click
		* until the durable `user/message` or steering occurrence renders.
		* @param props - the session snapshot's pending submission and render seats.
		* @returns the echoed user bubble.
		*/
		function PendingSubmissionBubble({ submission, renderMessageImages, t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(UserStyleBubble, {
				content: (0, react.useMemo)(() => submission.text === "" ? [] : [{
					type: "text",
					text: submission.text
				}], [submission.text]),
				previewAttachments: (0, react.useMemo)(() => submission.attachments.map((attachment) => attachment.type === "image" ? {
					type: "image",
					image: { preview: {
						url: attachment.value.previewUrl,
						...attachment.value.name === void 0 ? {} : { name: attachment.value.name },
						...attachment.value.width === void 0 ? {} : { width: attachment.value.width },
						...attachment.value.height === void 0 ? {} : { height: attachment.value.height }
					} }
				} : {
					type: "file",
					file: attachment.value
				}), [submission.attachments]),
				renderMessageImages,
				pending: submission.placement === "steering",
				echo: true,
				t,
				actions: (text) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)(MessageIconActions, {
					text,
					time: submission.time,
					clock: "start",
					className: MessageItem_module_css_default.actions,
					t
				})
			});
		}
		/** User and admitted-steering keyed Chat renderer. */
		const UserMessageNodeView = (0, react.memo)(function UserMessageNodeView({ node, renderMessageImages, openFile, openSkill, t }) {
			const data = node.data;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(UserStyleBubble, {
				content: data.content,
				references: {
					openFile,
					openSkill
				},
				renderMessageImages,
				...data.referenceLabels === void 0 ? {} : { referenceLabels: data.referenceLabels },
				...data.skillNames === void 0 ? {} : { skillNames: data.skillNames },
				t,
				actions: (text) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)(MessageIconActions, {
					text,
					time: data.time,
					clock: "start",
					className: MessageItem_module_css_default.actions,
					t
				})
			});
		});
		/** Injected-context keyed Chat renderer. */
		const ContextMessageNodeView = (0, react.memo)(function ContextMessageNodeView({ node, t }) {
			const data = node.data;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ContextInjectionRow, {
				content: data.content,
				source: data.source,
				producer: data.producer,
				form: data.form,
				t
			});
		});
		/** Automatic compaction keyed Chat renderer. */
		const CompactionNodeView = (0, react.memo)(function CompactionNodeView({ node, t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(CompactionItem, {
				node: node.data,
				t
			});
		});
		/** Correlated retry-chain keyed Chat renderer. */
		const RetryNodeView = (0, react.memo)(function RetryNodeView({ node, t }) {
			const data = node.data;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ModelRetryItem, {
				node: data.current,
				active: data.current.retryState === "scheduled",
				t
			});
		});
		/** Terminal turn-error keyed Chat renderer. */
		const TurnErrorNodeView = (0, react.memo)(function TurnErrorNodeView({ node, t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(TurnErrorItem, {
				node: node.data,
				t
			});
		});
		/** Max-tokens turn-end notice keyed Chat renderer. */
		const TurnMaxTokensNodeView = (0, react.memo)(function TurnMaxTokensNodeView({ t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(TurnMaxTokensItem, { t });
		});
		/** Explicit unknown-surface keyed Chat renderer. */
		const UnknownNodeView = (0, react.memo)(function UnknownNodeView({ node, t }) {
			const data = node.data;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: MessageItem_module_css_default.contextRow,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.JsonBlock, {
					label: t("message.unknownSurface", { type: data.type }),
					payload: data.data,
					truncatedLabel: (total) => t("json.truncated", { total })
				})
			});
		});
		/** Chat Node kinds that remain independent of a Turn's process disclosure. */
		const TURN_PROCESS_INDEPENDENT_KINDS = new Set([
			"system-prompt",
			"user",
			"steering",
			"turn-trigger",
			"turn-process",
			"turn-error",
			"turn-max-tokens",
			"turn-tail"
		]);
		/**
		* Compare immutable Turn-process specifications by their published fields.
		* @param left - previous specification.
		* @param right - next specification.
		* @returns whether both values describe the same process presentation.
		*/
		function sameTurnProcessSpec(left, right) {
			return left.turn === right.turn && left.controlAnchorSeq === right.controlAnchorSeq && left.processStartSeq === right.processStartSeq && left.answerAnchorSeq === right.answerAnchorSeq && left.answerStep === right.answerStep && left.inlineReasoning === right.inlineReasoning && left.messageCount === right.messageCount && left.toolCallCount === right.toolCallCount && left.subagentCount === right.subagentCount;
		}
		/**
		* Recognize the shipped subagent delegation name and its configured variants.
		* Control tools use distinct names such as `send_message` and `list_agents`.
		* @param name - durable Tool-call name.
		* @returns whether the call creates or forks a subagent.
		*/
		function isSubagentDelegationTool(name) {
			return name === "subagent" || name.startsWith("subagent_");
		}
		/**
		* Keep live, stopped, and failed Turns open.
		* @param node - Node carrying the owning Turn.
		* @returns whether whole-Turn collapse is unavailable.
		*/
		function turnProcessAlwaysOpen(node) {
			const location = node?.location;
			if (location?.kind !== "turn" && location?.kind !== "step") return false;
			const reason = location.turn.end?.data.reason.kind;
			return location.turn.status === "open" || reason === "aborted" || reason === "error";
		}
		//#endregion
		//#region src/client/stores.ts
		/** Per-Session Chat view store. */
		/**
		* Resolve the manually expanded answer for one Turn.
		* @param state - Chat store snapshot.
		* @param turn - owning Turn.
		* @returns the Turn's stored entry, when present.
		*/
		function storedTurnProcessEntry(state, turn) {
			return state.turnProcesses.find((entry) => entry.turn === turn);
		}
		/**
		* Create the Chat view store handle.
		* @returns a handle instantiated once per rendered Session scope.
		*/
		function createChatStore() {
			return (0, _deepseek_ai_dsh_client_store.defineStore)({
				init: () => ({ turnProcesses: [] }),
				actions: { setTurnProcessOpen: (draft, turn, answerStep, open) => {
					const index = draft.turnProcesses.findIndex((entry) => entry.turn === turn);
					if (!open) {
						if (index >= 0) draft.turnProcesses.splice(index, 1);
						return;
					}
					const next = {
						turn,
						answerStep
					};
					if (index < 0) draft.turnProcesses.push(next);
					else draft.turnProcesses[index] = next;
				} }
			});
		}
		//#endregion
		//#region src/client/chat/searchable-hidden.ts
		/**
		* Apply searchable hidden state without unmounting a stable subtree.
		* @param hidden - whether the subtree is currently hidden.
		* @param reveal - callback for browser find's `beforematch` reveal.
		* @returns ref for the stable subtree root.
		*/
		function useSearchableHidden(hidden, reveal) {
			const ref = (0, react.useRef)(null);
			(0, react.useLayoutEffect)(() => {
				const element = ref.current;
				if (element === null) return;
				if (hidden && element.contains(element.ownerDocument.activeElement)) {
					reveal();
					return;
				}
				if (hidden) element.setAttribute("hidden", "until-found");
				else element.removeAttribute("hidden");
			}, [hidden, reveal]);
			(0, react.useEffect)(() => {
				const element = ref.current;
				if (element === null) return;
				element.addEventListener("beforematch", reveal);
				return () => {
					element.removeEventListener("beforematch", reveal);
				};
			}, [reveal]);
			return ref;
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-chat/src/client/chat/ChatView.module.css.mjs
		const css$13 = "._9vZXdq_frame{flex-direction:column;flex:auto;min-height:0;display:flex;position:relative;container-type:inline-size}._9vZXdq_root{flex-direction:column;flex:auto;min-height:0;display:flex;position:relative;overflow:visible clip}._9vZXdq_scroll{min-height:0;padding:16px calc(var(--dsh-composer-side-clearance) + 16px);flex:auto;overflow-y:auto;container-type:inline-size}._9vZXdq_root[data-chat-following-tail] ._9vZXdq_scroll,[data-conversation-scroll]:has(._9vZXdq_root[data-chat-following-tail]){overflow-anchor:none}[data-conversation-scroll] ._9vZXdq_frame,[data-conversation-scroll] ._9vZXdq_root{flex:none;height:auto;min-height:auto}[data-conversation-scroll] ._9vZXdq_scroll{flex:none;min-height:auto;overflow:visible}._9vZXdq_column{max-width:var(--dsh-chat-content-width);flex-direction:column;width:100%;margin:0 auto;display:flex}._9vZXdq_column>:not([hidden]):not(._9vZXdq_flowItem:empty)~:not([hidden]):not(._9vZXdq_flowItem:empty){margin-top:var(--dsh-chat-flow-gap,16px)}._9vZXdq_flowItem{min-width:0}._9vZXdq_flowItem[data-turn-process-answer]{--dsh-chat-flow-gap:8px}._9vZXdq_flowItem:empty{height:0}._9vZXdq_callRow{border-radius:var(--dsw-radius-sm)}._9vZXdq_hint{color:var(--dsw-alias-label-tertiary);font-size:var(--dsh-content-font-size-secondary,13px);line-height:calc(18px + var(--dsh-content-font-delta-secondary,0px))}._9vZXdq_openError{color:var(--dsw-alias-state-error-primary);font-size:var(--dsh-content-font-size-secondary,13px);line-height:calc(18px + var(--dsh-content-font-delta-secondary,0px))}._9vZXdq_older{justify-content:center;display:flex}._9vZXdq_older button{border-radius:var(--dsw-radius-sm);color:var(--dsw-alias-label-secondary);background:var(--dsw-alias-interactive-bg-hover-solid);cursor:pointer;border:none;padding:4px 12px;font-size:12px}._9vZXdq_older button:disabled{cursor:default;opacity:.6}._9vZXdq_toBottomSlot{z-index:8;height:0;padding-right:max(calc(var(--dsh-composer-side-clearance) + 16px), calc((100% - var(--dsh-chat-content-width)) / 2));pointer-events:none;justify-content:flex-end;display:flex;position:absolute;bottom:16px;left:0;right:0}[data-conversation-scroll] ._9vZXdq_toBottomSlot{bottom:calc(var(--dsh-composer-height,152px) + 16px);position:sticky}._9vZXdq_toBottom{--dsw-elevation-stroke-color:var(--dsw-alias-border-l3);corner-shape:round;width:34px;height:34px;color:var(--dsw-alias-label-primary);background:var(--dsw-alias-button-floating-fill);box-shadow:var(--dsw-elevation-panel);cursor:pointer;pointer-events:auto;border:0;border-radius:100px;justify-content:center;align-items:center;margin-top:-34px;padding:0;display:flex}._9vZXdq_toBottom:hover{background:var(--dsw-alias-button-floating-hover)}._9vZXdq_modalAction{min-width:72px}";
		const tagId$13 = "@deepseek-ai/dsh-client-ui-chat/ChatView.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$13) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-chat";
			tag.dataset.pluginCss = tagId$13;
			tag.textContent = css$13;
			document.head.appendChild(tag);
		}
		var ChatView_module_css_default = {
			"callRow": "_9vZXdq_callRow",
			"column": "_9vZXdq_column",
			"flowItem": "_9vZXdq_flowItem",
			"frame": "_9vZXdq_frame",
			"hint": "_9vZXdq_hint",
			"modalAction": "_9vZXdq_modalAction",
			"older": "_9vZXdq_older",
			"openError": "_9vZXdq_openError",
			"root": "_9vZXdq_root",
			"scroll": "_9vZXdq_scroll",
			"toBottom": "_9vZXdq_toBottom",
			"toBottomSlot": "_9vZXdq_toBottomSlot"
		};
		//#endregion
		//#region src/client/chat/ChatNodeSeat.tsx
		function turnDataOf(node) {
			const location = node?.location;
			return location?.kind === "turn" || location?.kind === "step" ? location.turn.data : void 0;
		}
		function turnOf$1(node) {
			const location = node?.location;
			return location?.kind === "turn" || location?.kind === "step" ? location.turn.turn : void 0;
		}
		/**
		* Subscribe, apply Turn-process visibility, and dispatch one stable Context key.
		* Policy reads select this seat's own conclusion, so a mode change re-renders
		* only seats whose visibility actually changes.
		*/
		const ChatNodeSeat = (0, react.memo)(function ChatNodeSeat({ nodeKey, groupPart, useChatNode, useChatNodeProcess, usePresentation, cwd, openFile, openSkill, inspectCall, forkAt, loadImage, renderMessageImages, fileMentions, useStore, actions, renderSlot, t }) {
			const node = useChatNode(nodeKey);
			const routedNode = node;
			const turn = turnOf$1(routedNode);
			const processPresentation = useChatNodeProcess(nodeKey);
			const processSpec = processPresentation?.spec;
			const storedEntry = useStore((state) => processSpec === void 0 ? void 0 : storedTurnProcessEntry(state, processSpec.turn));
			const processEntry = processSpec !== void 0 && storedEntry?.answerStep === (processSpec.answerStep ?? 0) ? storedEntry : void 0;
			const liveProcess = processPresentation !== void 0 && !processPresentation.turnClosed;
			const interleavedInput = processPresentation?.hasInterleavedInput === true;
			const alwaysOpen = liveProcess || interleavedInput || turnProcessAlwaysOpen(routedNode);
			const processOpen = alwaysOpen || processEntry !== void 0;
			const setOpen = (0, react.useCallback)((open) => {
				if (processSpec !== void 0 && !alwaysOpen) actions.setTurnProcessOpen(processSpec.turn, processSpec.answerStep ?? 0, open);
			}, [
				actions,
				processSpec,
				alwaysOpen
			]);
			const foldCompleted = usePresentation((policy) => policy.foldCompletedTurns);
			const processWindowReady = processSpec !== void 0 && processPresentation !== void 0 && foldCompleted && processPresentation.turn === processSpec.turn && (processPresentation.turnStarted || processPresentation.turnClosed);
			const processMember = routedNode !== void 0 && processWindowReady && !TURN_PROCESS_INDEPENDENT_KINDS.has(routedNode.kind) && routedNode.anchorSeq >= processSpec.processStartSeq && (liveProcess || processSpec.answerAnchorSeq === null || routedNode.anchorSeq < processSpec.answerAnchorSeq || groupPart === "reasoning" && routedNode.kind === "assistant-step" && routedNode.data.step === processSpec.answerStep);
			const processAnswer = routedNode !== void 0 && processWindowReady && !liveProcess && groupPart !== "reasoning" && routedNode.kind === "assistant-step" && routedNode.data.step === processSpec.answerStep;
			const ownsDisclosure = routedNode?.kind === "turn-process" || processAnswer;
			const foldable = processWindowReady && (liveProcess || processMember || ownsDisclosure);
			const turnProcess = (0, react.useMemo)(() => processSpec === void 0 ? void 0 : {
				spec: processSpec,
				foldable,
				hasContent: !interleavedInput && (processPresentation?.hasExternalProcess === true || processSpec.inlineReasoning),
				open: processOpen,
				setOpen
			}, [
				foldable,
				interleavedInput,
				processOpen,
				processSpec,
				processPresentation?.hasExternalProcess,
				setOpen
			]);
			const controllerInactive = routedNode?.kind === "turn-process" && foldCompleted && !foldable;
			const compactAnswer = processAnswer && foldable && processPresentation.compactAnswer && !processOpen;
			const processHidden = controllerInactive || foldable && processMember && !processOpen;
			const wrapperRef = useSearchableHidden(processHidden, (0, react.useCallback)(() => {
				if (processMember) setOpen(true);
			}, [processMember, setOpen]));
			const [disclosureReset] = (0, react.useState)(() => (0, _deepseek_ai_dsh_client_store.createSnapshotStore)(0));
			const turnData = turnDataOf(routedNode);
			const hookContext = (0, react.useMemo)(() => ({
				turnData,
				disclosureReset
			}), [turnData, disclosureReset]);
			(0, react.useEffect)(() => {
				if (processMember && processHidden && wrapperRef.current?.hasAttribute("hidden")) disclosureReset.set(disclosureReset.getSnapshot() + 1);
			}, [
				processMember,
				processHidden,
				wrapperRef,
				disclosureReset
			]);
			const owner = (0, react.useMemo)(() => node === void 0 ? null : {
				...groupPart === void 0 ? {} : { groupPart },
				cwd,
				openFile,
				openSkill,
				inspectCall,
				forkAt,
				loadImage,
				renderMessageImages,
				fileMentions,
				turnProcess
			}, [
				node,
				groupPart,
				cwd,
				openFile,
				openSkill,
				inspectCall,
				forkAt,
				loadImage,
				renderMessageImages,
				fileMentions,
				turnProcess
			]);
			if (routedNode === void 0 || owner === null) return null;
			const routedOwner = {
				...owner,
				node: routedNode
			};
			const flowKey = groupPart === void 0 || groupPart === "response" ? routedNode.key : JSON.stringify([routedNode.key, groupPart]);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				ref: wrapperRef,
				className: ChatView_module_css_default.flowItem,
				"data-chat-anchor-key": flowKey,
				"data-chat-flow-key": flowKey,
				"data-chat-paging-anchor": routedNode.kind !== "turn-process" || void 0,
				"data-chat-node-key": routedNode.key,
				"data-chat-group-part": groupPart,
				"data-chat-flow-kind": routedNode.kind,
				"data-chat-turn": turn,
				"data-turn-process-member": processMember || void 0,
				"data-turn-process-hidden": processHidden || void 0,
				"data-turn-process-answer": compactAnswer || void 0,
				children: renderSlot("conversation.chat.node", routedOwner, {
					entryKey: routedNode.kind,
					hookContext,
					fallback: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.JsonBlock, {
						label: t("message.unknownSurface", { type: routedNode.kind }),
						payload: routedNode.data,
						truncatedLabel: (total) => t("json.truncated", { total })
					})
				})
			});
		});
		//#endregion
		//#region ../../util/values/src/index.ts
		/**
		* Mark an unreachable closed-union branch.
		* @param value - impossible value; an unhandled typed variant fails at the call site.
		* @param context - optional switch-site label included in the failure message.
		* @returns never; a runtime value that escaped its type always throws.
		*/
		function assertNever(value, context) {
			const rendered = JSON.stringify(value) ?? String(value);
			throw new Error(`unreachable variant${context ? ` in ${context}` : ""}: ${rendered}`);
		}
		//#endregion
		//#region src/client/chat/render-entry.ts
		/**
		* Identify a rendering position independently of presentation mode.
		* @param entry - mode-independent rendering reference.
		* @returns its collision-free React key.
		*/
		function chatRenderKey(entry) {
			switch (entry.kind) {
				case "node": return JSON.stringify([
					"node",
					entry.key,
					entry.groupPart ?? null
				]);
				case "group": return JSON.stringify(["group", entry.key]);
				default: return assertNever(entry);
			}
		}
		//#endregion
		//#region src/client/chat/step-process.ts
		/**
		* Compose a closed group's localized title from its top three categories without counts.
		* @param summary - ranked work and phase evidence for this range.
		* @param t - Chat namespace translator.
		* @returns the secondary disclosure title.
		*/
		function processTitle(summary, t) {
			const labels = summary.counts.slice(0, 3).map(({ kind }) => t(`message.stepProcess.done.${kind}`));
			const first = labels[0];
			if (first === void 0) return t("message.stepProcess.done.thinking");
			const continuation = (label) => label.charAt(0).toLowerCase() + label.slice(1);
			const second = labels[1];
			if (second === void 0) return first;
			if (labels.length === 2) {
				const prefix = t("message.stepProcess.sharedPrefix");
				return t("message.stepProcess.joinTwo", {
					first,
					second: continuation(prefix !== "" && first.startsWith(prefix) && second.startsWith(prefix) ? second.slice(prefix.length) : second)
				});
			}
			const title = [first, ...labels.slice(1).map(continuation)].join(t("message.stepProcess.comma"));
			return summary.counts.length > 3 ? t("message.stepProcess.more", { title }) : title;
		}
		//#endregion
		//#region src/client/chat/use-disclosure.ts
		/** Bind independent disclosure state to a Chat seat's reset source. */
		/**
		* Own one initially collapsed disclosure without an external subscription.
		* @param version - reset generation; unchanged generations retain local open state.
		* @returns the open state, an explicit setter, and a toggle action.
		*/
		function useDisclosure(version = 0) {
			const [expandedVersion, setExpandedVersion] = (0, react.useState)(null);
			return {
				expanded: expandedVersion === version,
				setExpanded: (0, react.useCallback)((open) => {
					setExpandedVersion(open ? version : null);
				}, [version]),
				toggle: (0, react.useCallback)(() => {
					setExpandedVersion((previous) => previous === version ? null : version);
				}, [version])
			};
		}
		/**
		* Bind a Hook without subscribing until a component calls it.
		* @param reset - stable source whose version advances when the seat is hidden by its Turn.
		* @returns a Hook with independent open state for each invocation.
		*/
		function bindDisclosure(reset) {
			const subscribe = (listener) => reset.subscribe(listener);
			const getSnapshot = () => reset.getSnapshot();
			return function useBoundDisclosure() {
				return useDisclosure((0, react.useSyncExternalStore)(subscribe, getSnapshot));
			};
		}
		//#endregion
		//#region src/client/chat/use-scroll-follow.ts
		/** Independent bottom-follow intent and native scrolling, without paging or DOM observers. */
		/**
		* Read one scrollport without measuring its children.
		* @param element - scrolling element.
		* @returns current position and range.
		*/
		function scrollMetrics(element) {
			const height = element.clientHeight;
			return {
				top: element.scrollTop,
				height,
				floor: Math.max(0, element.scrollHeight - height)
			};
		}
		/** One scrollport's follow intent; native animation progress does not count as reader movement. */
		var ScrollFollow = class ScrollFollow {
			following;
			threshold;
			static owners = /* @__PURE__ */ new WeakMap();
			target = null;
			sampledTop;
			/**
			* @param following - initial follow intent.
			* @param threshold - accepted distance from the floor, in pixels.
			*/
			constructor(following, threshold) {
				this.following = following;
				this.threshold = threshold;
			}
			/**
			* Find the mounted controller for reading-position compensation.
			* @param element - scrollport with an optional follow owner.
			* @returns its controller, when bound.
			*/
			static forElement(element) {
				return this.owners.get(element);
			}
			/**
			* Share this controller with reading-position compensation for the same scrollport.
			* @param element - owned scrollport.
			* @returns release the association on unmount or close.
			*/
			bind(element) {
				ScrollFollow.owners.set(element, this);
				return () => {
					if (ScrollFollow.owners.get(element) === this) ScrollFollow.owners.delete(element);
				};
			}
			/**
			* Expose follow intent independently of the current offset.
			* @returns whether content growth should follow the floor.
			*/
			get active() {
				return this.following;
			}
			/**
			* Expose outstanding native motion before accepting reader input.
			* @returns whether a native follow animation has an outstanding target.
			*/
			get animating() {
				return this.target !== null;
			}
			/**
			* Classify bottom arrivals using this scrollport's own tolerance.
			* @param metrics - current scroll geometry.
			* @returns whether the position is within the follow threshold.
			*/
			nearBottom(metrics) {
				return metrics.floor - metrics.top <= this.threshold;
			}
			/**
			* Commit caller-owned follow decisions without moving the scrollport.
			* @param active - externally committed follow intent.
			*/
			setFollowing(active) {
				this.following = active;
				if (!active) this.target = null;
			}
			/** Adopt the next visible layout as a fresh reader position. */
			reset() {
				this.setFollowing(false);
				this.sampledTop = void 0;
			}
			/**
			* Adopt delivered scrolling while retaining intent during native animation.
			* @param metrics - current geometry.
			* @param movedByReader - caller attribution; omitted callers compare the last sampled position.
			* @returns updated follow intent.
			*/
			sample(metrics, movedByReader = this.sampledTop === void 0 || Math.abs(metrics.top - this.sampledTop) > .5) {
				this.sampledTop = metrics.top;
				if (!this.animating && movedByReader) this.following = this.nearBottom(metrics);
				return this.active;
			}
			/**
			* Settle native scrolling; an off-target stop releases follow intent.
			* @param metrics - actual geometry delivered at scrollend.
			* @returns follow intent after completing or interrupting native motion.
			*/
			settle(metrics) {
				const target = this.target;
				this.target = null;
				return this.sample(metrics, target === null ? void 0 : Math.abs(metrics.top - Math.min(target, metrics.floor)) > this.threshold);
			}
			/**
			* Position immediately and adopt the resulting follow intent.
			* @param element - scrolling element.
			* @param metrics - geometry before positioning.
			* @param top - requested offset, clamped to the measured range.
			* @returns geometry after positioning.
			*/
			jump(element, metrics, top) {
				const animated = this.animating;
				this.target = null;
				const target = Math.max(0, Math.min(metrics.floor, top));
				if (animated) element.scrollTo({
					top: target,
					behavior: "instant"
				});
				else if (target !== metrics.top) element.scrollTop = target;
				const landed = {
					...metrics,
					top: element.scrollTop
				};
				this.sampledTop = landed.top;
				this.following = this.nearBottom(landed);
				return landed;
			}
			/**
			* Follow the measured floor, respecting reduced motion for smooth requests.
			* An outstanding smooth target finishes before another is issued.
			* Within-tolerance positioning is immediate while no animation is outstanding.
			* @param element - scrolling element.
			* @param metrics - current geometry.
			* @param behavior - native animation for growth, or immediate positioning.
			* @returns current geometry; smooth requests retain their starting position until native scroll delivery.
			*/
			toBottom(element, metrics, behavior) {
				this.following = true;
				if (behavior === "instant" || metrics.top >= metrics.floor || !this.animating && this.nearBottom(metrics)) return this.jump(element, metrics, metrics.floor);
				if (typeof matchMedia === "function" && matchMedia("(prefers-reduced-motion: reduce)").matches) return this.jump(element, metrics, metrics.floor);
				if (this.target === null) {
					this.target = metrics.floor;
					element.scrollTo({
						top: metrics.floor,
						behavior: "smooth"
					});
				}
				return metrics;
			}
			/**
			* Cancel native motion before a reader gesture; only subsequent actual movement changes follow intent.
			* @param element - scrolling element.
			* @param metrics - position at interruption.
			*/
			interrupt(element, metrics) {
				if (!this.animating) return;
				this.target = null;
				this.sampledTop = metrics.top;
				element.scrollTo({
					top: metrics.top,
					behavior: "instant"
				});
			}
		};
		/**
		* Retain one independent follow controller without React updates for scroll samples.
		* @param initial - initial follow intent.
		* @param threshold - accepted distance from the floor, in pixels.
		* @returns the stable controller shared by the caller's scroll and resize handlers.
		*/
		function useScrollFollow(initial, threshold) {
			const [follow] = (0, react.useState)(() => new ScrollFollow(initial, threshold));
			return follow;
		}
		//#endregion
		//#region src/client/chat/use-process-scroll.ts
		/** Capped process-group scrolling and fades over the shared follow controller. */
		const AT_REST = {
			canScrollUp: false,
			canScrollDown: false
		};
		const SCROLL_KEYS$1 = new Set([
			"ArrowUp",
			"ArrowDown",
			"PageUp",
			"PageDown",
			"Home",
			"End",
			" "
		]);
		/**
		* Observe one group's body and content without coupling its follow intent to the outer transcript.
		* Wheel, touchstart, any pointerdown, and unprevented scroll keys interrupt active animations,
		* including events from editable controls; subsequent position sampling determines follow intent.
		* @param bodyRef - capped scrolling body.
		* @param contentRef - uncapped content whose size reports growth.
		* @param open - local disclosure state.
		* @param grouped - whether the display mode retains the group's height cap.
		* @returns edge fades, DOM event bindings, and one-shot positioning for manual opening.
		*/
		function useProcessScroll(bodyRef, contentRef, open, grouped) {
			const follow = useScrollFollow(false, 1);
			const initialPosition = (0, react.useRef)(null);
			const [edges, setEdges] = (0, react.useState)(AT_REST);
			const initialize = (0, react.useCallback)((position) => {
				initialPosition.current = position;
			}, []);
			const sync = (0, react.useCallback)((cause) => {
				const body = bodyRef.current;
				let next = AT_REST;
				if (body !== null && body.closest("[hidden], [data-group-expanded-mode]") === null) {
					let metrics = scrollMetrics(body);
					const initial = cause === "resize" ? initialPosition.current : null;
					if (initial !== null) {
						metrics = follow.jump(body, metrics, initial === "bottom" ? metrics.floor : 0);
						if (initial === "top") follow.setFollowing(false);
						initialPosition.current = null;
					} else {
						const wasAnimating = follow.animating;
						if (cause === "scrollend") follow.settle(metrics);
						else follow.sample(metrics);
						if (follow.active && (cause === "resize" || cause === "scrollend" && wasAnimating)) metrics = follow.toBottom(body, metrics, "smooth");
					}
					next = {
						canScrollUp: metrics.top > 1,
						canScrollDown: metrics.top < metrics.floor - 1
					};
				} else follow.reset();
				setEdges((previous) => previous.canScrollUp === next.canScrollUp && previous.canScrollDown === next.canScrollDown ? previous : next);
			}, [bodyRef, follow]);
			const interrupt = (0, react.useCallback)(() => {
				const body = bodyRef.current;
				if (body !== null && follow.animating) follow.interrupt(body, scrollMetrics(body));
			}, [bodyRef, follow]);
			const events = (0, react.useMemo)(() => ({
				onScroll: () => {
					sync("scroll");
				},
				onWheel: interrupt,
				onTouchStart: interrupt,
				onPointerDown: interrupt,
				onKeyDown: (event) => {
					if (!event.defaultPrevented && SCROLL_KEYS$1.has(event.key)) interrupt();
				}
			}), [interrupt, sync]);
			(0, react.useLayoutEffect)(() => {
				interrupt();
				follow.reset();
				if (!grouped || !open) initialPosition.current = null;
			}, [
				follow,
				grouped,
				interrupt,
				open
			]);
			(0, react.useLayoutEffect)(() => {
				const body = bodyRef.current;
				if (body === null || !open || typeof ResizeObserver === "undefined") return;
				const unbind = follow.bind(body);
				const observer = new ResizeObserver(() => {
					sync("resize");
				});
				const onScrollEnd = (event) => {
					if (event.target === body) sync("scrollend");
				};
				body.addEventListener("scrollend", onScrollEnd);
				observer.observe(body);
				if (contentRef.current !== null) observer.observe(contentRef.current);
				return () => {
					unbind();
					observer.disconnect();
					body.removeEventListener("scrollend", onScrollEnd);
				};
			}, [
				bodyRef,
				contentRef,
				follow,
				open,
				sync
			]);
			return {
				edges,
				events,
				initialize
			};
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-chat/src/client/chat/ChatGroupSeat.module.css.mjs
		const css$12 = ".Al2fMG_root{min-width:0}.Al2fMG_title{max-width:100%;color:var(--dsw-alias-label-secondary);font:inherit;font-size:var(--dsh-content-font-size,14px);text-align:left;cursor:pointer;background:0 0;border:0;align-items:center;gap:6px;padding:0;transition:color .1s;display:flex}.Al2fMG_title:hover{color:var(--dsw-alias-label-primary)}.Al2fMG_leading{width:16px;height:16px;color:var(--dsw-alias-label-tertiary);flex:none;justify-content:center;align-items:center;display:inline-flex;position:relative}.Al2fMG_activityIcon,.Al2fMG_chevron{justify-content:center;align-items:center;transition:opacity .1s;display:inline-flex;position:absolute;inset:0}.Al2fMG_activityIcon{opacity:1}.Al2fMG_chevron,.Al2fMG_title:is(:hover,:focus-visible) .Al2fMG_activityIcon{opacity:0}.Al2fMG_title:is(:hover,:focus-visible) .Al2fMG_chevron{opacity:1}.Al2fMG_title[aria-expanded=true] .Al2fMG_activityIcon{opacity:0}.Al2fMG_title[aria-expanded=true] .Al2fMG_chevron{opacity:1}.Al2fMG_title[aria-expanded=true]{padding-bottom:16px}.Al2fMG_body{--dsh-chat-flow-gap:8px;overscroll-behavior-y:auto;scrollbar-gutter:stable;max-height:min(400px,50vh);overflow-y:auto}.Al2fMG_label{text-overflow:ellipsis;white-space:nowrap;min-width:0;overflow:hidden}.Al2fMG_fadeTop{mask-image:linear-gradient(#0000 0,#000 24px 100%)}.Al2fMG_fadeBottom{mask-image:linear-gradient(#000 0 calc(100% - 24px),#0000 100%)}.Al2fMG_fadeTop.Al2fMG_fadeBottom{mask-image:linear-gradient(#0000 0,#000 24px calc(100% - 24px),#0000 100%)}@media (prefers-reduced-motion:reduce){.Al2fMG_title,.Al2fMG_activityIcon,.Al2fMG_chevron{transition:none}}.Al2fMG_content{flex-direction:column;display:flex}.Al2fMG_content>*{flex-shrink:0}.Al2fMG_content>:not([hidden]):not(:empty)~:not([hidden]):not(:empty){margin-top:var(--dsh-chat-flow-gap,8px)}.Al2fMG_expandedBody{--dsh-chat-flow-gap:16px;scrollbar-gutter:auto;max-height:none;overflow:visible}";
		const tagId$12 = "@deepseek-ai/dsh-client-ui-chat/ChatGroupSeat.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$12) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-chat";
			tag.dataset.pluginCss = tagId$12;
			tag.textContent = css$12;
			document.head.appendChild(tag);
		}
		var ChatGroupSeat_module_css_default = {
			"activityIcon": "Al2fMG_activityIcon",
			"body": "Al2fMG_body",
			"chevron": "Al2fMG_chevron",
			"content": "Al2fMG_content",
			"expandedBody": "Al2fMG_expandedBody",
			"fadeBottom": "Al2fMG_fadeBottom",
			"fadeTop": "Al2fMG_fadeTop",
			"label": "Al2fMG_label",
			"leading": "Al2fMG_leading",
			"root": "Al2fMG_root",
			"title": "Al2fMG_title"
		};
		//#endregion
		//#region src/client/chat/ChatGroupSeat.tsx
		/** Stable process container; display policy changes visibility, never member parents. */
		const PROCESS_TITLE_MINIMUM_MS = 150;
		const PROCESS_ICONS = {
			thinking: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconThinkOutlineRegular, {}),
			read: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconBrowseOutlineRegular, { size: 14 }),
			readImage: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconBrowseOutlineRegular, { size: 14 }),
			search: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconSearchOutlineRegular, { size: 14 }),
			edit: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconEditOutlineRegular, { size: 14 }),
			write: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconEditOutlineRegular, { size: 14 }),
			commands: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconApiOutlineRegular, {}),
			code: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconCodeOutlineRegular, { size: 14 }),
			webSearch: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconGlobeOutlineRegular, {}),
			webFetch: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconBrowseOutlineRegular, { size: 14 }),
			subagents: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconAgentPresetOutlineRegular, { size: 14 }),
			plan: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconPlanOutlineRegular, {}),
			questions: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconQuestionOutlineRegular, {}),
			tools: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconSparkleRegular, { size: 14 })
		};
		function sameLiveProcessTitle(left, right) {
			return left.activity === right.activity && left.detail === right.detail && left.preparing === right.preparing;
		}
		function useStableLiveProcessTitle(desired, active) {
			const [displayed, setDisplayed] = (0, react.useState)(desired);
			const displayedRef = (0, react.useRef)(displayed);
			const desiredRef = (0, react.useRef)(desired);
			const displayedAtRef = (0, react.useRef)(Date.now());
			(0, react.useEffect)(() => {
				desiredRef.current = desired;
				if (!active || sameLiveProcessTitle(displayedRef.current, desired)) return;
				const remaining = PROCESS_TITLE_MINIMUM_MS - (Date.now() - displayedAtRef.current);
				const commit = () => {
					const next = desiredRef.current;
					displayedRef.current = next;
					displayedAtRef.current = Date.now();
					setDisplayed(next);
				};
				if (remaining <= 0) {
					commit();
					return;
				}
				const timer = setTimeout(commit, remaining);
				return () => {
					clearTimeout(timer);
				};
			}, [
				active,
				desired.activity,
				desired.detail,
				desired.preparing
			]);
			return active ? displayed : desired;
		}
		const GroupMembers = (0, react.memo)(function GroupMembers({ members, ...props }) {
			return members.map((member) => /* @__PURE__ */ (0, react.createElement)(ChatNodeSeat, {
				...props,
				key: chatRenderKey(member),
				nodeKey: member.key,
				...member.groupPart === void 0 ? {} : { groupPart: member.groupPart }
			}));
		});
		const ProcessGroupHeader = (0, react.memo)(function ProcessGroupHeader({ groupKey, useChatGroup, usePresentation, t, open, bodyId, toggle }) {
			const data = useChatGroup(groupKey, (group) => group?.data);
			const detailed = usePresentation((policy) => data?.closed === false && policy.liveProcessDetail);
			const live = useStableLiveProcessTitle({
				activity: data?.summary.running ?? "thinking",
				detail: data?.summary.runningDetail ?? "",
				preparing: data?.summary.preparing === true
			}, data !== void 0 && !data.closed);
			if (data === void 0) return null;
			const label = data.closed ? processTitle(data.summary, t) : live.preparing ? t(`message.stepProcess.prepare.${live.activity === "thinking" ? "tools" : live.activity}`) : t(`message.stepProcess.${live.activity}`);
			const detail = detailed && !data.closed ? live.detail : "";
			const title = detail === "" ? label : `${label}${t("message.turnProcess.separator")}${detail}`;
			const activity = data.closed ? data.summary.counts[0]?.kind ?? "thinking" : live.activity;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
				type: "button",
				className: ChatGroupSeat_module_css_default.title,
				"aria-expanded": open,
				"aria-controls": bodyId,
				"data-process-activity": activity,
				onClick: (event) => {
					event.currentTarget.focus();
					toggle();
				},
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
					className: ChatGroupSeat_module_css_default.leading,
					"aria-hidden": "true",
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: ChatGroupSeat_module_css_default.activityIcon,
						"data-step-process-icon": true,
						children: PROCESS_ICONS[activity]
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: ChatGroupSeat_module_css_default.chevron,
						"data-step-process-chevron": true,
						children: open ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronUpOutlineRegular, {}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronDownOutlineRegular, {})
					})]
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.TextShimmer, {
					active: !data.closed,
					className: ChatGroupSeat_module_css_default.label,
					children: title
				})]
			});
		});
		/** Render a process group with local disclosure and the existing outer-Turn visibility. */
		const ChatGroupSeat = (0, react.memo)(function ChatGroupSeat({ groupKey, useChatGroup, ...props }) {
			const members = useChatGroup(groupKey, (group) => group?.members);
			const turn = useChatGroup(groupKey, (group) => group?.data.turn);
			const closed = useChatGroup(groupKey, (group) => group?.data.closed);
			const foldCompleted = props.usePresentation((policy) => policy.foldCompletedTurns);
			const { expanded: open, setExpanded: setOpen } = useDisclosure();
			const firstKey = members?.[0]?.key ?? "";
			const presentation = props.useChatNodeProcess(firstKey);
			const turnLocation = props.useChatNode(firstKey, (node) => {
				const location = node?.location;
				return location?.kind === "turn" || location?.kind === "step" ? location.turn : void 0;
			});
			const grouped = props.usePresentation((policy) => policy.stepGrouping === "collapsed" || policy.stepGrouping === "history" && turnLocation?.status !== "open");
			const reason = turnLocation?.end?.data.reason.kind;
			const alwaysOpen = presentation?.turnClosed === false || presentation?.hasInterleavedInput === true || reason === "aborted" || reason === "error";
			const spec = presentation?.spec;
			const selectStored = (0, react.useCallback)((state) => turn === void 0 ? void 0 : storedTurnProcessEntry(state, turn), [turn]);
			const stored = props.useStore(selectStored);
			const outerHidden = foldCompleted && presentation?.turnClosed === true && spec !== void 0 && !alwaysOpen && stored?.answerStep !== (spec.answerStep ?? 0);
			const rootRef = useSearchableHidden(outerHidden, (0, react.useCallback)(() => {
				if (spec !== void 0 && !alwaysOpen) props.actions.setTurnProcessOpen(spec.turn, spec.answerStep ?? 0, true);
			}, [
				props.actions,
				spec,
				alwaysOpen
			]));
			(0, react.useEffect)(() => {
				if (outerHidden && rootRef.current?.hasAttribute("hidden")) setOpen(false);
			}, [
				outerHidden,
				rootRef,
				setOpen
			]);
			const reveal = (0, react.useCallback)(() => {
				setOpen(true);
			}, [setOpen]);
			const bodyRef = useSearchableHidden(grouped && !open, reveal);
			const contentRef = (0, react.useRef)(null);
			const bodyId = (0, react.useId)();
			const { edges, events, initialize } = useProcessScroll(bodyRef, contentRef, open, grouped);
			const toggle = (0, react.useCallback)(() => {
				if (!open) initialize(closed === false ? "bottom" : "top");
				setOpen(!open);
			}, [
				closed,
				initialize,
				open,
				setOpen
			]);
			if (members === void 0) return null;
			const classes = [
				ChatGroupSeat_module_css_default.body,
				!grouped ? ChatGroupSeat_module_css_default.expandedBody : "",
				grouped && edges.canScrollUp ? ChatGroupSeat_module_css_default.fadeTop : "",
				grouped && edges.canScrollDown ? ChatGroupSeat_module_css_default.fadeBottom : ""
			];
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				ref: rootRef,
				className: ChatGroupSeat_module_css_default.root,
				"data-chat-group-key": groupKey,
				"data-chat-flow-key": groupKey,
				"data-chat-anchor-key": `group:${groupKey}`,
				"data-chat-turn": turn,
				"data-chat-paging-anchor": grouped && !open || void 0,
				"data-step-process": true,
				"data-group-expanded-mode": !grouped || void 0,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
					hidden: !grouped,
					children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ProcessGroupHeader, {
						groupKey,
						useChatGroup,
						usePresentation: props.usePresentation,
						t: props.t,
						open,
						bodyId,
						toggle
					})
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
					ref: bodyRef,
					id: bodyId,
					className: classes.join(" "),
					"data-step-process-body": true,
					"data-scroll-up": edges.canScrollUp || void 0,
					"data-scroll-down": edges.canScrollDown || void 0,
					...events,
					children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						ref: contentRef,
						className: ChatGroupSeat_module_css_default.content,
						"data-step-process-content": true,
						"data-chat-flow": "",
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(GroupMembers, {
							...props,
							members
						})
					})
				})]
			});
		});
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
		function memo$11(getDeps, fn, opts) {
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
				this.maybeNotify = memo$11(() => {
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
				this.getMeasurementOptions = memo$11(() => [
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
				this.getMeasurements = memo$11(() => [this.getMeasurementOptions(), this.itemSizeCacheVersion], ({ count, paddingStart, scrollMargin, getItemKey, enabled, lanes, laneAssignmentMode, gap }, _itemSizeCacheVersion) => {
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
				this.calculateRange = memo$11(() => [
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
				this.getVirtualIndexes = memo$11(() => {
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
				this.getVirtualItems = memo$11(() => [this.getVirtualIndexes(), this.getMeasurements()], (indexes, measurements) => {
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
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-chat/src/client/chat/TurnNavigator.module.css.mjs
		const css$11 = ".qNphhG_slot{z-index:7;height:0;padding-inline:calc(var(--dsh-composer-side-clearance) + 16px);pointer-events:none;position:absolute;top:0;left:0;right:0;container-type:inline-size}[data-conversation-scroll] .qNphhG_slot{position:sticky}.qNphhG_frame{--turn-rail-band:calc(var(--dsh-conversation-viewport-height,100dvh) - var(--dsh-composer-height,152px));--turn-preview-height:100px;top:calc(var(--turn-rail-band) / 2);width:28px;max-height:min(max(0px, calc(var(--turn-rail-band) - 64px)), 420px);contain:layout;cursor:pointer;pointer-events:auto;position:absolute;right:12px;transform:translateY(-50%)}.qNphhG_scroller{max-height:inherit;overscroll-behavior:contain;scrollbar-width:none;position:relative;overflow-y:auto}.qNphhG_scroller::-webkit-scrollbar{display:none}.qNphhG_fadeTop{mask-image:linear-gradient(#0000 0,#000 24px 100%)}.qNphhG_fadeBottom{mask-image:linear-gradient(#000 0 calc(100% - 24px),#0000 100%)}.qNphhG_fadeTop.qNphhG_fadeBottom{mask-image:linear-gradient(#0000 0,#000 24px calc(100% - 24px),#0000 100%)}.qNphhG_marks{position:relative}.qNphhG_mark{cursor:pointer;background:0 0;border:0;border-radius:8px;height:10px;padding:0;position:absolute;top:0;left:0;right:0}.qNphhG_mark:before{background:var(--dsw-alias-border-l4);content:\"\";transform-origin:100%;border-radius:2px;width:20px;height:2px;transition:transform .14s,background-color .14s;position:absolute;top:50%;right:0;transform:translateY(-50%)scaleX(.6)}.qNphhG_markUnloaded:before{opacity:.6;transform:translateY(-50%)scaleX(.4)}.qNphhG_markPreview:before{background:var(--dsw-alias-label-tertiary);transform:translateY(-50%)scaleX(.9)}.qNphhG_markBusy:before{animation:1s ease-in-out infinite qNphhG_dsh-turn-mark-busy}.qNphhG_markActive:before{background:var(--dsw-alias-label-primary);transform:translateY(-50%)scaleX(1)}.qNphhG_mark:focus-visible:before{background:var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));transform:translateY(-50%)scaleX(1)}.qNphhG_mark:focus-visible{outline:none}.qNphhG_mark:focus-visible:after{border-radius:inherit;outline:1px solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:-1px;content:\"\";width:20px;position:absolute;inset:0 0 0 auto}.qNphhG_preview{top:clamp(0px, calc(var(--turn-preview-center) - var(--turn-preview-height) / 2), calc(100% - var(--turn-preview-height)));box-sizing:border-box;width:min(300px,100cqw - 120px);max-height:var(--turn-preview-height);border-radius:var(--dsw-radius-lg);color:var(--dsw-alias-label-primary);background:var(--dsw-alias-bg-layer-1);box-shadow:var(--dsw-elevation-panel);pointer-events:none;border:0;padding:10px 12px;transition:top .14s cubic-bezier(.2,.8,.2,1);animation:.12s ease-out qNphhG_dsh-turn-preview-enter;position:absolute;right:calc(100% + 10px);overflow:hidden}.qNphhG_previewPrompt,.qNphhG_previewResponse{-webkit-box-orient:vertical;display:-webkit-box;overflow:hidden}.qNphhG_previewPrompt{font:var(--dsw-font-xs-strong-13);-webkit-line-clamp:1}.qNphhG_previewResponse{color:var(--dsw-alias-label-caption);font:var(--dsw-font-xxs-12);-webkit-line-clamp:3;margin-top:4px}@keyframes qNphhG_dsh-turn-preview-enter{0%{opacity:0;transform:translate(4px)}to{opacity:1;transform:translate(0)}}@keyframes qNphhG_dsh-turn-mark-busy{0%,to{opacity:1}50%{opacity:.35}}@container (width<=900px){.qNphhG_frame{display:none}}@media (prefers-reduced-motion:reduce){.qNphhG_frame,.qNphhG_scroller,.qNphhG_mark:before,.qNphhG_markBusy:before,.qNphhG_preview{scroll-behavior:auto;transition:none;animation:none}}";
		const tagId$11 = "@deepseek-ai/dsh-client-ui-chat/TurnNavigator.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$11) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-chat";
			tag.dataset.pluginCss = tagId$11;
			tag.textContent = css$11;
			document.head.appendChild(tag);
		}
		var TurnNavigator_module_css_default = {
			"dsh-turn-mark-busy": "qNphhG_dsh-turn-mark-busy",
			"dsh-turn-preview-enter": "qNphhG_dsh-turn-preview-enter",
			"fadeBottom": "qNphhG_fadeBottom",
			"fadeTop": "qNphhG_fadeTop",
			"frame": "qNphhG_frame",
			"mark": "qNphhG_mark",
			"markActive": "qNphhG_markActive",
			"markBusy": "qNphhG_markBusy",
			"markPreview": "qNphhG_markPreview",
			"markUnloaded": "qNphhG_markUnloaded",
			"marks": "qNphhG_marks",
			"preview": "qNphhG_preview",
			"previewPrompt": "qNphhG_previewPrompt",
			"previewResponse": "qNphhG_previewResponse",
			"scroller": "qNphhG_scroller",
			"slot": "qNphhG_slot"
		};
		//#endregion
		//#region src/client/chat/TurnNavigator.tsx
		/** Fixed-pitch virtual turn rail with independent activation and scroll controls. */
		/** Fixed pitch between neighbouring marks; overflow scrolls inside the frame. */
		const TURN_SPACING_PX = 10;
		/** Rail padding above the first mark and below the last one, per end. */
		const RAIL_INSET_PX = 6;
		/** Fade band the mask reserves at a scrollable end. */
		const FADE_PX = 24;
		function preferredScrollBehavior() {
			return typeof matchMedia === "function" && matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth";
		}
		const TurnMark = (0, react.memo)(function TurnMark({ item, index, active, busy, previewId, registerElement, onNavigate, onPreview, onFocusChange, t }) {
			const classes = [TurnNavigator_module_css_default.mark];
			if (item.anchor.kind === "unloaded") classes.push(TurnNavigator_module_css_default.markUnloaded);
			if (active) classes.push(TurnNavigator_module_css_default.markActive);
			else if (previewId !== void 0) classes.push(TurnNavigator_module_css_default.markPreview);
			if (busy) classes.push(TurnNavigator_module_css_default.markBusy);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
				ref: registerElement,
				"data-index": index,
				type: "button",
				className: classes.join(" "),
				"aria-label": t(item.anchor.kind === "loaded" ? "chat.turnNavigation.jump" : "chat.turnNavigation.jumpLoad", { turn: item.turn }),
				"aria-current": active ? "true" : void 0,
				"aria-busy": busy ? "true" : void 0,
				"aria-describedby": previewId,
				onPointerMove: () => {
					onPreview(item.turn);
				},
				onClick: () => {
					onNavigate(item);
				},
				onFocus: () => {
					onFocusChange(item.turn);
				},
				onBlur: () => {
					onFocusChange(null);
				}
			});
		});
		function TurnNavigatorRail({ items, activeTurn, busyTurn, onNavigate, t }, ref) {
			const [previewTurn, setPreviewTurn] = (0, react.useState)(null);
			const [focusedTurn, setFocusedTurn] = (0, react.useState)(null);
			const scrollerRef = (0, react.useRef)(null);
			const initialization = (0, react.useRef)({
				placed: false,
				index: 0,
				follow: null,
				publishOffset: null
			});
			/** While the pointer works the rail, follow must not move it under the hand. */
			const pointerInsideRef = (0, react.useRef)(false);
			const previewId = (0, react.useId)();
			const turnIndexes = (0, react.useMemo)(() => {
				const indexes = /* @__PURE__ */ new Map();
				items.forEach((item, index) => {
					indexes.set(item.turn, index);
				});
				return indexes;
			}, [items]);
			const activeIndex = activeTurn === null ? void 0 : turnIndexes.get(activeTurn);
			(0, react.useLayoutEffect)(() => {
				initialization.current.index = activeIndex ?? 0;
			}, [activeIndex]);
			const focusedIndex = focusedTurn === null ? void 0 : turnIndexes.get(focusedTurn);
			const previewIndex = previewTurn === null ? void 0 : turnIndexes.get(previewTurn);
			const onFocusChange = (0, react.useCallback)((turn) => {
				setFocusedTurn(turn);
				setPreviewTurn(turn);
			}, []);
			const virtualizer = useVirtualizer({
				count: items.length,
				enabled: items.length >= 2,
				directDomUpdates: true,
				directDomUpdatesMode: "transform",
				useScrollendEvent: true,
				getScrollElement: (0, react.useCallback)(() => scrollerRef.current, []),
				getItemKey: (0, react.useCallback)((index) => items[index]?.turn ?? index, [items]),
				estimateSize: () => TURN_SPACING_PX,
				measureElement: () => TURN_SPACING_PX,
				initialRect: {
					width: 0,
					height: 0
				},
				initialOffset: 0,
				scrollToFn: (offset, options, instance) => {
					if (initialization.current.placed) elementScroll(offset, options, instance);
				},
				observeElementOffset: (instance, notify) => {
					initialization.current.publishOffset = notify;
					const dispose = observeElementOffset(instance, notify);
					return () => {
						dispose?.();
						initialization.current.placed = false;
						initialization.current.follow = null;
						initialization.current.publishOffset = null;
					};
				},
				observeElementRect: (instance, notify) => {
					const element = instance.scrollElement;
					const Observer = instance.targetWindow?.ResizeObserver;
					if (element === null || Observer === void 0) return;
					const observer = new Observer(([entry]) => {
						if (entry === void 0) return;
						const box = entry.borderBoxSize[0];
						const rect = {
							width: Math.round(box?.inlineSize ?? entry.contentRect.width),
							height: Math.round(box?.blockSize ?? entry.contentRect.height)
						};
						const initial = initialization.current;
						if (!initial.placed && rect.height > 0) {
							const max = Math.max(0, instance.getTotalSize() - rect.height);
							const center = initial.index * TURN_SPACING_PX + RAIL_INSET_PX;
							const target = Math.max(0, Math.min(max, center - rect.height / 2));
							initial.placed = true;
							initial.follow = {
								index: initial.index,
								count: instance.options.count,
								height: rect.height
							};
							element.scrollTop = target;
							initial.publishOffset?.(target, false);
						}
						notify(rect);
					});
					observer.observe(element, { box: "border-box" });
					return () => {
						observer.disconnect();
					};
				},
				paddingStart: RAIL_INSET_PX - TURN_SPACING_PX / 2,
				paddingEnd: RAIL_INSET_PX - TURN_SPACING_PX / 2,
				scrollPaddingStart: FADE_PX,
				scrollPaddingEnd: FADE_PX,
				overscan: 3,
				rangeExtractor: (0, react.useCallback)((range) => {
					const indexes = defaultRangeExtractor(range);
					if (focusedIndex !== void 0) {
						const last = Math.min(range.count - 1, focusedIndex + 1);
						for (let index = Math.max(0, focusedIndex - 1); index <= last; index++) if (!indexes.includes(index)) indexes.push(index);
						indexes.sort((left, right) => left - right);
					}
					return indexes;
				}, [focusedIndex])
			});
			const scrollTop = virtualizer.scrollOffset ?? 0;
			const viewHeight = virtualizer.scrollRect?.height ?? 0;
			const virtualItems = virtualizer.getVirtualItems();
			const scrollToIndex = (0, react.useCallback)((index, reveal, behavior = preferredScrollBehavior()) => {
				const item = virtualizer.measurementsCache[index];
				const height = virtualizer.scrollRect?.height ?? 0;
				if (item === void 0 || height <= 0) return;
				const current = virtualizer.scrollOffset ?? 0;
				const center = item.start + item.size / 2;
				if (reveal === "if-needed") {
					const { scrollPaddingStart, scrollPaddingEnd } = virtualizer.options;
					if (center >= current + scrollPaddingStart && center <= current + height - scrollPaddingEnd) return;
				}
				const target = center - height / 2;
				const max = Math.max(0, virtualizer.getTotalSize() - height);
				const delta = Math.max(0, Math.min(max, target)) - current;
				if (delta !== 0) virtualizer.scrollBy(delta, { behavior });
			}, [virtualizer]);
			(0, react.useImperativeHandle)(ref, () => ({
				activateTurn(turn) {
					const index = turnIndexes.get(turn);
					const item = index === void 0 ? void 0 : items[index];
					if (item !== void 0) onNavigate(item);
				},
				scrollToTurn(turn) {
					const index = turnIndexes.get(turn);
					if (index !== void 0) scrollToIndex(index, "always");
				}
			}), [
				items,
				turnIndexes,
				onNavigate,
				scrollToIndex
			]);
			(0, react.useEffect)(() => {
				if (viewHeight <= 0) {
					initialization.current.follow = null;
					return;
				}
				if (activeIndex === void 0 || pointerInsideRef.current) return;
				const previous = initialization.current.follow;
				if (previous?.index === activeIndex && previous.count === items.length && previous.height === viewHeight) return;
				initialization.current.follow = {
					index: activeIndex,
					count: items.length,
					height: viewHeight
				};
				scrollToIndex(activeIndex, "if-needed", previous?.count === items.length && previous.height === viewHeight ? preferredScrollBehavior() : "instant");
			}, [
				activeIndex,
				items.length,
				viewHeight,
				scrollToIndex
			]);
			if (items.length < 2) return null;
			const preview = previewIndex === void 0 ? void 0 : items[previewIndex];
			const previewPosition = virtualItems.find((item) => item.index === previewIndex);
			const fadeClasses = [TurnNavigator_module_css_default.scroller];
			if (scrollTop > 1) fadeClasses.push(TurnNavigator_module_css_default.fadeTop);
			if (scrollTop < virtualizer.getTotalSize() - viewHeight - 1) fadeClasses.push(TurnNavigator_module_css_default.fadeBottom);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: TurnNavigator_module_css_default.slot,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("nav", {
					className: TurnNavigator_module_css_default.frame,
					"aria-label": t("chat.turnNavigation.label"),
					onPointerEnter: () => {
						pointerInsideRef.current = true;
					},
					onPointerLeave: () => {
						pointerInsideRef.current = false;
						setPreviewTurn(null);
					},
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						ref: scrollerRef,
						className: fadeClasses.join(" "),
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							ref: virtualizer.containerRef,
							className: TurnNavigator_module_css_default.marks,
							children: virtualItems.map(({ index, key }) => {
								const item = items[index];
								if (item === void 0) return null;
								return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(TurnMark, {
									item,
									index,
									active: item.turn === activeTurn,
									busy: item.turn === busyTurn,
									previewId: item.turn === previewTurn ? previewId : void 0,
									registerElement: virtualizer.measureElement,
									onNavigate,
									onPreview: setPreviewTurn,
									onFocusChange,
									t
								}, key);
							})
						})
					}), preview !== void 0 && previewPosition !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						id: previewId,
						role: "tooltip",
						className: TurnNavigator_module_css_default.preview,
						style: { "--turn-preview-center": `${String(previewPosition.start + previewPosition.size / 2 - scrollTop)}px` },
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: TurnNavigator_module_css_default.previewPrompt,
							children: preview.prompt || t("chat.turnNavigation.turn", { turn: preview.turn })
						}), preview.response !== "" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: TurnNavigator_module_css_default.previewResponse,
							children: preview.response
						})]
					})]
				})
			});
		}
		/**
		* Fixed-pitch rail of every known Turn — loaded marks scroll, unloaded marks
		* page history in first — with hover and focus previews. Overflow scrolls
		* inside the frame, gradient fades marking each scrollable end, and the
		* active mark centers only outside the fade-free band while the pointer is
		* elsewhere. Previews follow pointer movement or focus, not scrolling under
		* a stationary pointer.
		*/
		const TurnNavigator = (0, react.memo)((0, react.forwardRef)(TurnNavigatorRail));
		//#endregion
		//#region ../../util/brand/lib/index.js
		/**
		* Apply a compile-time number brand without changing the value.
		* @param value - number admitted by the domain that owns the target brand.
		* @returns the same number with the requested compile-time brand.
		*/
		function brandNumber(value) {
			return value;
		}
		//#endregion
		//#region ../../core/session/src/types.ts
		/**
		* Admit a numeric value as an existing Session event position.
		* @param value - non-negative safe integer admitted by the owning log operation.
		* @returns the same number with the Session-sequence brand.
		*/
		function SessionSeq(value) {
			if (!Number.isSafeInteger(value) || value < 0 || Object.is(value, -0)) throw new TypeError(`SessionSeq must be a non-negative safe integer, got ${String(value)}`);
			return brandNumber(value);
		}
		//#endregion
		//#region src/client/chat/turn-rail-items.ts
		const EMPTY_ITEMS$1 = [];
		/**
		* Structurally narrow one wire outline entry (projection values cross the
		* wire). `turn` and `seq` are the load-bearing fields — a mark cannot exist
		* or jump without them — so their damage drops the entry; the previews are
		* decorative, so a malformed one degrades to `''` and the turn stays
		* navigable by number.
		*/
		function outlineEntry(value) {
			if (typeof value !== "object" || value === null) return void 0;
			const entry = value;
			if (typeof entry.turn !== "number" || !Number.isSafeInteger(entry.turn) || entry.turn < 0) return void 0;
			if (typeof entry.seq !== "number" || !Number.isSafeInteger(entry.seq) || entry.seq < 0 || Object.is(entry.seq, -0)) return void 0;
			return {
				turn: entry.turn,
				seq: SessionSeq(entry.seq),
				prompt: typeof entry.prompt === "string" ? entry.prompt : "",
				response: typeof entry.response === "string" ? entry.response : ""
			};
		}
		/** Wire outline entries, or none when the projection is absent or malformed. */
		function outlineEntries(outline) {
			return Array.isArray(outline) ? outline : EMPTY_ITEMS$1;
		}
		/**
		* Merge the host outline with the loaded rail items into the full ladder.
		* A turn present in both sides keeps the loaded anchor, taking an outline
		* preview only where the window's own is empty (a mid-Turn window head, or a
		* turn whose loaded nodes carry no text); turns on one side only pass
		* through. Result ascends by turn.
		* @param loaded - loaded-window rail items (timeline order).
		* @param outline - `turnOutline` projection value, treated as wire data.
		* @returns every known turn, ascending; a stable empty array when none.
		*/
		function mergeTurnRailItems(loaded, outline) {
			const byTurn = /* @__PURE__ */ new Map();
			for (const raw of outlineEntries(outline)) {
				const entry = outlineEntry(raw);
				if (entry === void 0) continue;
				byTurn.set(entry.turn, {
					turn: entry.turn,
					prompt: entry.prompt,
					response: entry.response,
					anchor: {
						kind: "unloaded",
						seq: entry.seq
					}
				});
			}
			for (const item of loaded) {
				const preview = byTurn.get(item.turn);
				byTurn.set(item.turn, {
					turn: item.turn,
					prompt: item.prompt !== "" ? item.prompt : preview?.prompt ?? "",
					response: item.response !== "" ? item.response : preview?.response ?? "",
					anchor: {
						kind: "loaded",
						key: item.anchorKey
					}
				});
			}
			if (byTurn.size === 0) return EMPTY_ITEMS$1;
			return [...byTurn.values()].sort((left, right) => left.turn - right.turn);
		}
		//#endregion
		//#region src/client/chat/use-chat-navigation.ts
		/** Turn jumps and history-prepend anchoring, independent of DOM measurement. */
		/** Owns one replaceable turn jump and the anchor retained while history loads. */
		var ChatNavigation = class {
			viewport;
			reading;
			input;
			onBusyTurn;
			jump = null;
			settleFrame = null;
			constructor(viewport, reading, input, onBusyTurn) {
				this.viewport = viewport;
				this.reading = reading;
				this.input = input;
				this.onBusyTurn = onBusyTurn;
			}
			/**
			* Adopt committed history availability without starting a request.
			* @param input - history state from the latest committed render.
			*/
			setInput(input) {
				this.input = input;
			}
			/** Cancel navigation when opening a Chat view. */
			reset() {
				this.cancel();
			}
			/** Cancel local callbacks; late history completions cannot revive a task. */
			dispose() {
				this.clearTask();
			}
			/** Release the jump, paging anchor, and busy indicator without cancelling shared history I/O. */
			cancel() {
				this.clearTask();
				this.onBusyTurn(null);
			}
			clearTask() {
				this.cancelFrame();
				this.jump = null;
				this.viewport.stopPreserving();
			}
			/**
			* Replace the current jump with an explicit turn selection.
			* @param item - loaded anchor or unloaded turn to fetch before landing.
			*/
			navigateToTurn = (item) => {
				if (item.anchor.kind === "loaded") {
					this.cancel();
					const landing = this.viewport.scrollToTurn(item.turn);
					if (landing === null) return;
					this.reading.acceptNavigation(landing);
					if (this.input.loadingOlder) this.viewport.beginPreserving(landing.position);
					return;
				}
				this.cancel();
				this.viewport.beginPreserving();
				this.reading.pauseFollowing();
				const jump = {
					turn: item.turn,
					seq: item.anchor.seq,
					phase: "loading",
					landing: "pending",
					repageHead: null
				};
				this.jump = jump;
				this.onBusyTurn(jump.turn);
				this.request(jump);
			};
			/** Request one older page while retaining the current semantic position. */
			loadEarlier = () => {
				this.cancel();
				this.viewport.beginPaging();
				this.reading.pauseFollowing();
				this.input.loadOlder();
			};
			/**
			* Preserve reader ownership across pending history work.
			* @param sample - settled reader movement that can update or interrupt an anchor.
			*/
			readerSampled(sample) {
				if (sample.movedByReader && this.jump?.landing === "landed") this.jump.landing = "interrupted";
				if (sample.followingTail || sample.movedByReader) this.viewport.stopPreserving();
			}
			/**
			* Preserve one paging anchor after a commit or a later size change, regardless of head identity.
			* @returns whether the retained anchor handled the layout change.
			*/
			contentCommitted() {
				if (!this.viewport.preserving || this.reading.pending) return false;
				if (this.landJump(false)) return true;
				const landing = this.viewport.preserve();
				if (landing === null) return false;
				this.reading.preservePosition(landing);
				return true;
			}
			/** Retarget a still-loading page only after inner or outer reader scrolling ends. */
			readerSettled() {
				if (this.input.loadingOlder && this.jump === null && !this.viewport.preserving && !this.reading.followingTail) this.viewport.beginPreserving();
			}
			/** Land, retry, or complete the current jump against the committed window. */
			reconcile() {
				const jump = this.jump;
				if (jump === null || this.reading.pending) return;
				if (jump.phase === "loading") {
					if (jump.landing === "pending") this.landJump(false);
					return;
				}
				if (this.input.loadingOlder) return;
				if (this.landJump(true)) return;
				if ((this.input.firstSeq === null || this.input.firstSeq > jump.seq) && this.input.hasMore && jump.repageHead !== this.input.firstSeq) {
					jump.repageHead = this.input.firstSeq;
					this.viewport.beginPreserving();
					this.request(jump);
					return;
				}
				const fallback = this.viewport.scrollToTurnAtOrAfter(jump.turn);
				this.cancel();
				if (fallback !== null) this.reading.acceptNavigation(fallback);
			}
			landJump(settle) {
				const jump = this.jump;
				if (jump === null) return false;
				if (jump.landing === "interrupted") {
					if (settle) {
						this.cancel();
						return true;
					}
					return false;
				}
				const landing = this.viewport.scrollToTurn(jump.turn);
				if (landing === null) return false;
				this.reading.acceptNavigation(landing);
				if (settle) this.cancel();
				else {
					this.viewport.beginPreserving(landing.position);
					jump.landing = "landed";
				}
				return true;
			}
			request(jump) {
				jump.phase = "loading";
				const settled = () => {
					if (this.jump !== jump) return;
					jump.phase = "settled";
					this.cancelFrame();
					if (typeof requestAnimationFrame !== "function") this.reconcile();
					else this.settleFrame = requestAnimationFrame(() => {
						this.settleFrame = null;
						if (this.jump === jump) this.reconcile();
					});
				};
				this.input.loadThrough(jump.seq).then(settled, settled);
			}
			cancelFrame() {
				if (this.settleFrame !== null && typeof cancelAnimationFrame === "function") cancelAnimationFrame(this.settleFrame);
				this.settleFrame = null;
			}
		};
		/**
		* Retain one navigation owner for the component's lifetime.
		* @param viewport - turn-aware DOM operations.
		* @param reading - reading and follow policy receiving navigation landings.
		* @param input - committed history state and load operations.
		* @returns the navigation owner and its visible busy turn.
		*/
		function useChatNavigation(viewport, reading, input) {
			const [busyTurn, setBusyTurn] = (0, react.useState)(null);
			const [navigation] = (0, react.useState)(() => new ChatNavigation(viewport, reading, input, setBusyTurn));
			(0, react.useLayoutEffect)(() => {
				navigation.setInput(input);
			}, [navigation, input]);
			(0, react.useLayoutEffect)(() => () => {
				navigation.dispose();
			}, [navigation]);
			return {
				navigation,
				busyTurn
			};
		}
		//#endregion
		//#region src/client/chat/use-chat-reading.ts
		/** Follow-tail ownership, saved-position restoration, and sampled reader movement. */
		const SCROLL_SAMPLE_INTERVAL_MS = 500;
		/** Owns reading policy and its cancellable sampling work, without DOM access. */
		var ChatReading = class {
			viewport;
			store;
			state;
			onChange;
			follow;
			sampleTimer = null;
			probeFrame = null;
			sampled = null;
			constructor(viewport, store, state, onChange, follow) {
				this.viewport = viewport;
				this.store = store;
				this.state = state;
				this.onChange = onChange;
				this.follow = follow;
			}
			/**
			* Expose pending reader ownership to navigation and resize handlers.
			* @returns whether reader input still awaits interval or scrollend sampling.
			*/
			get pending() {
				return this.sampleTimer !== null;
			}
			/**
			* Expose the active follow policy.
			* @returns whether content growth retains bottom-follow ownership.
			*/
			get followingTail() {
				return this.state.followingTail;
			}
			/**
			* Adopt the committed Session's scroll memory.
			* @param store - scroll memory for the current Session.
			*/
			setStore(store) {
				this.store = store;
			}
			/**
			* Connect history policy to settled reading observations.
			* @param sampled - receives settled reader positions.
			* @returns a disposer that disconnects only this listener.
			*/
			connect(sampled) {
				this.sampled = sampled;
				return () => {
					if (this.sampled === sampled) this.sampled = null;
				};
			}
			/** Cancel timers and animation frames and detach the sample listener. */
			dispose() {
				this.cancelPending();
				this.sampled = null;
			}
			/** Release bottom follow and pending sampling for an explicit navigation. */
			pauseFollowing() {
				this.cancelPending();
				this.publish({
					...this.state,
					followingTail: false
				});
			}
			/** Land at the current floor and clear saved reader position. */
			followTail() {
				const landing = this.viewport.scrollToBottom(this.follow);
				if (landing === null) return;
				this.cancelPending();
				this.commit(landing, true, this.viewport.latestTurn);
			}
			/** Restore the Session's semantic position, or follow the tail when none is saved. */
			restore() {
				const saved = this.store.read();
				if (saved === null) {
					this.followTail();
					return;
				}
				const landing = this.viewport.restore(saved);
				if (landing === null) return;
				this.cancelPending();
				const following = this.follow.nearBottom(landing.metrics);
				this.commit(landing, following, following ? this.viewport.latestTurn : this.state.activeTurn, following);
				if (!this.state.followingTail && landing.position === null) {
					const position = this.viewport.capturePosition();
					if (position !== null) this.store.save(position);
				}
				this.refreshActiveTurn();
			}
			/**
			* Adopt a known landing without rediscovering its anchor.
			* @param landing - measured navigation result that replaces pending reader input.
			*/
			acceptNavigation(landing) {
				this.cancelPending();
				const following = this.follow.nearBottom(landing.metrics);
				this.commit(landing, following, landing.turn ?? (following ? this.viewport.latestTurn : this.state.activeTurn));
			}
			/**
			* Retain reading policy while history changes the anchor's geometry.
			* @param landing - compensated position that retains the current reading policy.
			*/
			preservePosition(landing) {
				this.cancelPending();
				this.commit(landing, this.state.followingTail, this.state.activeTurn);
			}
			/**
			* Handle pinned layout movement and reader arrivals at the floor immediately.
			* @param scroll - attributed scroll delivery; other reader movement remains pending until sampled.
			*/
			onScroll = (scroll) => {
				if (!scroll.movedByReader && this.state.followingTail || scroll.movedByReader && scroll.metrics.top >= scroll.metrics.floor) {
					this.followTail();
					this.sampled?.({
						position: null,
						movedByReader: scroll.movedByReader,
						followingTail: true
					});
					return;
				}
				this.sampleTimer ??= window.setTimeout(this.flushSample, SCROLL_SAMPLE_INTERVAL_MS);
			};
			/** Settle pending reader movement at the browser's scrollend. */
			onScrollEnd = () => {
				this.flushSample();
			};
			/** Reconcile a layout change without overriding unsampled reader input. */
			onResize() {
				if (this.pending) return;
				if (this.state.followingTail) this.followTail();
				else this.refreshActiveTurn();
			}
			/** Resolve the active turn from tail ownership or a coalesced reading-line probe. */
			refreshActiveTurn() {
				if (this.pending) return;
				if (this.state.followingTail) {
					this.publish({
						...this.state,
						initialized: true,
						activeTurn: this.viewport.latestTurn
					});
					return;
				}
				if (this.probeFrame !== null) return;
				if (typeof requestAnimationFrame !== "function") this.probe();
				else this.probeFrame = requestAnimationFrame(this.probe);
			}
			commit(landing, followingTail, activeTurn, initialized = true) {
				if (followingTail) this.store.save(null);
				else if (landing.position !== null) this.store.save(landing.position);
				this.publish({
					initialized,
					followingTail,
					activeTurn
				});
			}
			publish(state) {
				this.follow.setFollowing(state.followingTail);
				if (state.initialized === this.state.initialized && state.followingTail === this.state.followingTail && state.activeTurn === this.state.activeTurn) return;
				this.state = state;
				this.onChange(state);
			}
			cancelPending() {
				if (this.sampleTimer !== null) window.clearTimeout(this.sampleTimer);
				if (this.probeFrame !== null && typeof cancelAnimationFrame === "function") cancelAnimationFrame(this.probeFrame);
				this.sampleTimer = null;
				this.probeFrame = null;
			}
			probe = () => {
				this.probeFrame = null;
				if (this.pending) return;
				const scroll = this.viewport.readScroll();
				if (scroll === null) return;
				const activeTurn = this.follow.nearBottom(scroll.metrics) ? this.viewport.latestTurn : this.viewport.readVisibleTurn(scroll.metrics);
				this.publish({
					...this.state,
					initialized: true,
					activeTurn
				});
			};
			flushSample = () => {
				if (!this.pending) return;
				this.cancelPending();
				const scroll = this.viewport.readScroll();
				if (scroll === null) return;
				const followingTail = this.follow.sample(scroll.metrics, scroll.movedByReader);
				let position = null;
				if (!scroll.movedByReader && followingTail) this.followTail();
				else {
					position = followingTail ? null : this.viewport.capturePosition();
					this.viewport.acknowledge(scroll.metrics);
					if (followingTail || position !== null) this.store.save(position);
					const activeTurn = this.follow.nearBottom(scroll.metrics) ? this.viewport.latestTurn : this.viewport.readVisibleTurn(scroll.metrics);
					this.publish({
						initialized: true,
						followingTail,
						activeTurn
					});
				}
				this.sampled?.({
					position,
					movedByReader: scroll.movedByReader,
					followingTail
				});
			};
		};
		/**
		* Retain reading policy and expose only changes in visible reading state.
		* @param viewport - turn-aware DOM operations.
		* @param store - Session-owned semantic scroll memory.
		* @param initialTurn - latest loaded turn before the first landing.
		* @returns the reading owner and its React-visible state.
		*/
		function useChatReading(viewport, store, initialTurn) {
			const [state, setState] = (0, react.useState)(() => ({
				initialized: false,
				followingTail: store.read() === null,
				activeTurn: initialTurn
			}));
			const follow = useScrollFollow(state.followingTail, 25);
			const [reading] = (0, react.useState)(() => new ChatReading(viewport, store, state, setState, follow));
			(0, react.useLayoutEffect)(() => {
				reading.setStore(store);
			}, [reading, store]);
			(0, react.useLayoutEffect)(() => () => {
				reading.dispose();
			}, [reading]);
			return {
				reading,
				state
			};
		}
		//#endregion
		//#region src/client/chat/use-chat-viewport.ts
		/** Turn-aware DOM scrolling and geometry, without history-loading or follow policy. */
		const READING_INTENTS = [
			"wheel",
			"touchstart",
			"pointerdown",
			"keydown",
			"beforematch"
		];
		const SCROLL_KEYS = new Set([
			"ArrowUp",
			"ArrowDown",
			"PageUp",
			"PageDown",
			"Home",
			"End",
			" "
		]);
		/** Owns one Chat scrollport's DOM operations, event listeners, and size observer. */
		var ChatViewport = class {
			elements = null;
			observer = null;
			events = null;
			turns = [];
			observation = {
				top: 0,
				landing: null
			};
			paging = null;
			/**
			* Bind to the containing scrollport and observe content and viewport sizes.
			* @param list - Chat root inside an optional shared conversation scrollport.
			* @param column - ordered outer Node/Group boxes; its size changes invalidate cached landings.
			*/
			attach(list, column) {
				this.detach();
				const scroller = list.closest("[data-conversation-scroll]") ?? list;
				const composer = scroller.querySelector("[data-composer-seat]");
				const elements = {
					list,
					column,
					scroller,
					composer
				};
				this.elements = elements;
				scroller.addEventListener("scroll", this.onScroll, { passive: true });
				scroller.addEventListener("scrollend", this.onScrollEnd, {
					passive: true,
					capture: true
				});
				for (const type of READING_INTENTS) scroller.addEventListener(type, this.onIntent, {
					passive: true,
					capture: true
				});
				if (typeof ResizeObserver !== "undefined") {
					this.observer = new ResizeObserver(() => {
						if (this.elements !== elements) return;
						this.invalidate();
						this.events?.resize();
					});
					this.observer.observe(column);
					this.observer.observe(scroller);
					if (composer !== null) this.observer.observe(composer);
				}
			}
			/** Disconnect DOM resources and clear observations for the detached view. */
			detach() {
				this.stopPreserving();
				this.elements?.scroller.removeEventListener("scroll", this.onScroll);
				this.elements?.scroller.removeEventListener("scrollend", this.onScrollEnd, true);
				for (const type of READING_INTENTS) this.elements?.scroller.removeEventListener(type, this.onIntent, true);
				this.observer?.disconnect();
				this.observer = null;
				this.elements = null;
				this.events = null;
				this.turns = [];
				this.observation = {
					top: 0,
					landing: null
				};
			}
			/**
			* Connect business policy without changing DOM listener ownership.
			* @param events - business handlers for scroll and layout changes.
			* @returns a disposer that disconnects only these handlers.
			*/
			connect(events) {
				this.events = events;
				return () => {
					if (this.events === events) this.events = null;
				};
			}
			/**
			* Adopt the loaded turn anchors without querying the DOM.
			* @param turns - ordered loaded turns from the committed Chat snapshot.
			*/
			updateTurns(turns) {
				this.turns = turns;
			}
			/**
			* Resolve the tail from the committed turn index.
			* @returns the latest loaded turn, or null for an empty window.
			*/
			get latestTurn() {
				return this.turns.at(-1)?.turn ?? null;
			}
			/** Discard geometry-dependent landing knowledge while retaining scroll attribution. */
			invalidate() {
				this.observation.landing = null;
			}
			/**
			* Accept a sampled reader position without retaining a known landing.
			* @param metrics - settled reader position used as the next attribution baseline.
			*/
			acknowledge(metrics) {
				this.observation = {
					top: metrics.top,
					landing: null
				};
			}
			/**
			* Compare the current scroll geometry with the last acknowledged position.
			* @returns current metrics and movement attribution, or null while detached.
			*/
			readScroll() {
				const metrics = this.metrics();
				if (metrics === null) return null;
				return {
					metrics,
					movedByReader: Math.abs(metrics.top - Math.min(this.observation.top, metrics.floor)) > .5
				};
			}
			metrics() {
				const scroller = this.elements?.scroller;
				if (scroller === void 0) return null;
				return scrollMetrics(scroller);
			}
			anchor(key, identity = "position") {
				if (this.elements === null) return null;
				let nodePart = null;
				for (const row of this.elements.list.querySelectorAll("[data-chat-anchor-key]:not([hidden]):not([hidden] *)")) {
					if (row.dataset.chatAnchorKey === key || identity === "node" && row.dataset.chatNodeKey === key) return row;
					if (nodePart === null && row.dataset.chatNodeKey === key) nodePart = row;
				}
				return nodePart;
			}
			/**
			* Capture visible transcript content, excluding Turn controls that relocate when history expands.
			* @returns a visible semantic anchor, or null when no anchor can be resolved.
			*/
			capturePosition() {
				const elements = this.elements;
				if (elements === null) return null;
				const { list, scroller, composer } = elements;
				const viewport = scroller.getBoundingClientRect();
				const bottom = composer?.getBoundingClientRect().top ?? viewport.bottom;
				let anchor = null;
				if (typeof document.elementsFromPoint === "function" && bottom > viewport.top) {
					const content = list.getBoundingClientRect();
					const left = Math.max(viewport.left, content.left);
					const right = Math.min(viewport.right, content.right);
					for (const element of document.elementsFromPoint(left + Math.max(0, right - left) / 2, viewport.top + 1)) {
						const row = element instanceof HTMLElement ? element.closest("[data-chat-anchor-key]") : null;
						if (row !== null && row.dataset.chatFlowKind !== "turn-process" && list.contains(row)) {
							anchor = row.dataset.chatGroupKey === void 0 ? row : row.querySelector("[data-step-process-content] > [data-chat-anchor-key]:not(:empty):not([hidden]):not([hidden] *)") ?? row;
							break;
						}
					}
				}
				if (anchor === null) {
					const rows = list.querySelectorAll("[data-chat-flow-key]:not([data-chat-group-key]):not([data-chat-flow-kind=\"turn-process\"]):not(:empty):not([hidden]):not([hidden] *)");
					let low = 0;
					let high = rows.length;
					while (low < high) {
						const middle = low + high >>> 1;
						if (rows.item(middle).getBoundingClientRect().bottom > viewport.top) high = middle;
						else low = middle + 1;
					}
					const row = rows[low];
					anchor = row !== void 0 && row.getBoundingClientRect().top < bottom ? row : rows[0] ?? null;
				}
				const key = anchor?.dataset.chatAnchorKey;
				return anchor === null || key === void 0 ? null : {
					anchorKey: key,
					anchorTop: anchor.getBoundingClientRect().top - viewport.top,
					scrollTop: scroller.scrollTop
				};
			}
			/**
			* Approximate the active Turn by binary-searching outer Node/Group boxes.
			* Gaps retain the last visited Turn candidate, not necessarily the immediate predecessor.
			* A known landing bypasses measurement while its position is unchanged.
			* @param metrics - reusable scroll metrics; omitted callers request a fresh read.
			* @returns the Turn near the reading line, or null while detached or empty.
			*/
			readVisibleTurn(metrics = this.metrics()) {
				const knownTurn = this.observation.landing?.turn;
				if (knownTurn != null && metrics?.top === this.observation.top) return knownTurn;
				const elements = this.elements;
				const first = this.turns[0];
				if (elements === null || metrics === null || first === void 0) return null;
				const line = elements.scroller.getBoundingClientRect().top + Math.min(96, metrics.height * .2);
				const rows = elements.column.children;
				let low = 0;
				let high = rows.length;
				let reading = first.turn;
				while (low < high) {
					const middle = low + high >>> 1;
					const row = rows[middle];
					if (row.getBoundingClientRect().top > line) high = middle;
					else {
						const value = row.getAttribute("data-chat-turn");
						const turn = value === null ? NaN : Number(value);
						if (Number.isSafeInteger(turn)) reading = turn;
						low = middle + 1;
					}
				}
				return reading;
			}
			/**
			* Align a known loaded turn and return its actual clamped position.
			* A split Node anchor selects its first visible part.
			* @param turn - loaded turn to align below the scrollport's top edge.
			* @returns the actual landing, or null when its anchor is unavailable.
			*/
			scrollToTurn(turn) {
				const item = this.turns.find((candidate) => candidate.turn === turn);
				if (item === void 0) return null;
				const row = this.anchor(item.anchorKey, "node");
				return row === null ? null : this.align(row, 24, turn);
			}
			/**
			* Align the nearest available fallback for an unavailable turn anchor.
			* @param turn - minimum turn number for a mounted fallback row.
			* @returns the fallback landing, or null when no eligible row exists.
			*/
			scrollToTurnAtOrAfter(turn) {
				if (this.elements === null) return null;
				for (const row of this.elements.list.querySelectorAll("[data-chat-turn]:not([hidden]):not([hidden] *)")) {
					const candidate = Number(row.dataset.chatTurn);
					if (Number.isSafeInteger(candidate) && candidate >= turn) return this.align(row, 24, candidate);
				}
				return null;
			}
			/**
			* Restore a semantic anchor with a raw-position fallback.
			* @param position - semantic scroll memory; raw top is used only if its row is absent.
			* @returns the actual landing, or null while detached.
			*/
			restore(position) {
				const row = this.anchor(position.anchorKey);
				if (row !== null) return this.align(row, position.anchorTop, null);
				const metrics = this.metrics();
				return metrics === null ? null : this.write(position.scrollTop, metrics, null);
			}
			/** Retain the first eligible transcript seat in DOM order; selection reads no geometry. */
			beginPaging() {
				this.stopPreserving();
				const row = this.elements?.list.querySelector("[data-chat-paging-anchor]:not(:empty):not([hidden]):not([hidden] *)");
				if (row != null) this.retain(row);
			}
			/**
			* Retain one old row and its inner/outer offsets for paging and later content growth.
			* @param position - an explicit landing to retain; omitted callers capture the current reading position.
			*/
			beginPreserving(position = this.capturePosition()) {
				this.stopPreserving();
				if (position === null) return;
				const row = this.anchor(position.anchorKey);
				if (row === null) return;
				this.retain(row, position);
			}
			retain(row, position, groupTop) {
				const elements = this.elements;
				const key = row.dataset.chatAnchorKey;
				if (elements === null || key === void 0) return null;
				const previous = this.paging?.group;
				if (previous != null) this.observer?.unobserve(previous.content);
				const top = row.getBoundingClientRect().top;
				const body = row.closest("[data-step-process-body]");
				const content = body?.querySelector("[data-step-process-content]");
				const group = body === null || content == null ? null : {
					body,
					content,
					top: groupTop ?? top - body.getBoundingClientRect().top
				};
				this.paging = {
					row,
					group,
					position: position ?? {
						anchorKey: key,
						anchorTop: top - elements.scroller.getBoundingClientRect().top,
						scrollTop: elements.scroller.scrollTop
					}
				};
				if (group !== null) this.observer?.observe(group.content);
				return this.paging;
			}
			/** Release paging ownership and its content-size observation. */
			stopPreserving() {
				const group = this.paging?.group;
				if (group != null) this.observer?.unobserve(group.content);
				this.paging = null;
			}
			/**
			* Expose retained paging ownership to navigation and resize policy.
			* @returns whether a paging row is retained for subsequent layout changes.
			*/
			get preserving() {
				return this.paging !== null;
			}
			/**
			* Compensate inner scrolling first, then the outer scrollport, within their actual scroll ranges.
			* An inner write pauses its bound follow controller so the reading anchor takes priority.
			* @returns the actual landing, or null when no visible retained row remains.
			*/
			preserve() {
				let paging = this.paging;
				const elements = this.elements;
				if (paging === null || elements === null) return null;
				if (!elements.list.contains(paging.row)) {
					const replacement = this.anchor(paging.position.anchorKey);
					if (replacement === null) {
						this.stopPreserving();
						return null;
					}
					paging = this.retain(replacement, paging.position, paging.group?.top);
					if (paging === null) return null;
				}
				const { row, group, position } = paging;
				if (row.closest("[hidden]") !== null || row.matches(":empty")) {
					this.stopPreserving();
					return null;
				}
				if (group !== null && group.body.contains(row)) {
					const top = row.getBoundingClientRect().top - group.body.getBoundingClientRect().top;
					const metrics = scrollMetrics(group.body);
					const target = Math.max(0, Math.min(metrics.floor, metrics.top + top - group.top));
					if (metrics.top !== target) {
						const follow = ScrollFollow.forElement(group.body);
						if (follow === void 0) group.body.scrollTop = target;
						else {
							follow.jump(group.body, metrics, target);
							follow.setFollowing(false);
						}
					}
				}
				const metrics = this.metrics();
				if (metrics === null) return null;
				const top = row.getBoundingClientRect().top - elements.scroller.getBoundingClientRect().top;
				const target = metrics.top + top - position.anchorTop;
				return this.write(target, metrics, null, {
					key: position.anchorKey,
					top
				});
			}
			/**
			* Align the scrollport with its current floor.
			* @param follow - independent follow intent and scrolling controller.
			* @returns the actual floor landing, or null while detached.
			*/
			scrollToBottom(follow) {
				const metrics = this.metrics();
				if (metrics === null || this.elements === null) return null;
				const landing = {
					metrics: follow.toBottom(this.elements.scroller, metrics, "instant"),
					position: null,
					turn: this.latestTurn
				};
				this.observation = {
					top: landing.metrics.top,
					landing
				};
				return landing;
			}
			align(row, offset, turn) {
				const metrics = this.metrics();
				if (metrics === null || this.elements === null) return null;
				const top = row.getBoundingClientRect().top - this.elements.scroller.getBoundingClientRect().top;
				return this.write(metrics.top + top - offset, metrics, turn, {
					key: row.dataset.chatAnchorKey,
					top
				});
			}
			write(target, metrics, turn, anchor) {
				if (this.elements === null) return null;
				const top = Math.max(0, Math.min(metrics.floor, target));
				if (top !== metrics.top) this.elements.scroller.scrollTop = top;
				const actual = this.elements.scroller.scrollTop;
				const landing = {
					metrics: {
						...metrics,
						top: actual
					},
					turn,
					position: anchor?.key === void 0 ? null : {
						anchorKey: anchor.key,
						anchorTop: anchor.top - (actual - metrics.top),
						scrollTop: actual
					}
				};
				this.observation = {
					top: actual,
					landing
				};
				return landing;
			}
			onScroll = (event) => {
				if (this.elements === null || event.target !== this.elements.scroller) return;
				if (this.observation.landing !== null && this.elements.scroller.scrollTop === this.observation.top) return;
				this.invalidate();
				if (this.paging !== null) {
					this.events?.resize();
					return;
				}
				const scroll = this.readScroll();
				if (scroll !== null) this.events?.scroll(scroll);
			};
			onScrollEnd = (event) => {
				if (event.target === this.elements?.scroller || event.target instanceof HTMLElement && event.target.hasAttribute("data-step-process-body")) this.events?.scrollEnd();
			};
			onIntent = (event) => {
				if (event.type === "keydown" || event.type === "pointerdown") {
					if (event.target instanceof Element && event.target.closest("[data-composer-seat]") !== null) return;
					if (event.type === "keydown" && (!(event instanceof KeyboardEvent) || !SCROLL_KEYS.has(event.key))) return;
				}
				if (this.paging === null) return;
				this.stopPreserving();
				this.events?.interact();
			};
		};
		/**
		* Bind viewport resource ownership to the component's layout lifetime.
		* @returns one viewport owner and the element refs attached for this mount.
		*/
		function useChatViewport() {
			const listRef = (0, react.useRef)(null);
			const columnRef = (0, react.useRef)(null);
			const [viewport] = (0, react.useState)(() => new ChatViewport());
			(0, react.useLayoutEffect)(() => {
				if (listRef.current === null || columnRef.current === null) return;
				viewport.attach(listRef.current, columnRef.current);
				return () => {
					viewport.detach();
				};
			}, [viewport]);
			return {
				viewport,
				listRef,
				columnRef
			};
		}
		//#endregion
		//#region src/client/chat/use-chat-scroll.ts
		/** Composes viewport operations, reading policy, and history navigation for Chat. */
		/**
		* Coordinate scroll policy after Chat content commits.
		* New submitted input supersedes pending reader sampling.
		* @param input - current Chat content, scroll memory, and history operations.
		* @returns element refs, visible reading state, and navigation callbacks.
		*/
		function useChatScroll(input) {
			const { ready, order, firstSeq, lastKey, lastIsUser, steeringId, submissionId, running, loadedTurns, chatScroll, hasMore, loadingOlder, loadOlder, loadThrough } = input;
			const { viewport, listRef, columnRef } = useChatViewport();
			const { reading, state } = useChatReading(viewport, chatScroll, loadedTurns.at(-1)?.turn ?? null);
			const navigationInput = (0, react.useMemo)(() => ({
				firstSeq,
				loadingOlder,
				hasMore,
				loadOlder,
				loadThrough
			}), [
				firstSeq,
				loadingOlder,
				hasMore,
				loadOlder,
				loadThrough
			]);
			const { navigation, busyTurn } = useChatNavigation(viewport, reading, navigationInput);
			const content = (0, react.useRef)({
				input,
				applied: null,
				opened: false
			});
			const processContent = (0, react.useCallback)(() => {
				const current = content.current.input;
				const previous = content.current.applied;
				const ownInput = current.lastIsUser && current.lastKey !== previous?.lastKey || current.steeringId !== null && current.steeringId !== previous?.steeringId && current.steeringId !== previous?.submissionId || current.submissionId !== null && current.submissionId !== previous?.submissionId && current.submissionId !== previous?.steeringId;
				if (reading.pending && !ownInput) return;
				content.current.applied = current;
				if (current.ready && !content.current.opened) {
					content.current.opened = true;
					navigation.reset();
					reading.restore();
					return;
				}
				if (ownInput) {
					navigation.cancel();
					reading.followTail();
					return;
				}
				if (navigation.contentCommitted()) {
					navigation.reconcile();
					return;
				}
				if ((previous === null || current.ready !== previous.ready || current.firstSeq !== previous.firstSeq || current.lastKey !== previous.lastKey || current.order.length !== previous.order.length || current.running !== previous.running || current.steeringId !== previous.steeringId || current.submissionId !== previous.submissionId) && reading.followingTail) {
					navigation.cancel();
					reading.followTail();
				} else navigation.reconcile();
			}, [reading, navigation]);
			(0, react.useLayoutEffect)(() => {
				const disconnectViewport = viewport.connect({
					scroll: reading.onScroll,
					scrollEnd: () => {
						reading.onScrollEnd();
						navigation.readerSettled();
					},
					interact: () => {
						navigation.cancel();
					},
					resize: () => {
						if (!navigation.contentCommitted()) reading.onResize();
						navigation.reconcile();
					}
				});
				const disconnectReading = reading.connect((sample) => {
					navigation.readerSampled(sample);
					processContent();
				});
				return () => {
					disconnectViewport();
					disconnectReading();
					content.current.opened = false;
					content.current.applied = null;
				};
			}, [
				viewport,
				reading,
				navigation,
				processContent
			]);
			(0, react.useLayoutEffect)(() => {
				const previous = content.current.input;
				content.current.input = {
					ready,
					order,
					lastKey,
					lastIsUser,
					steeringId,
					submissionId,
					running,
					loadedTurns,
					chatScroll,
					...navigationInput
				};
				viewport.updateTurns(loadedTurns);
				const layoutChanged = previous.order !== order || previous.ready !== ready;
				if (layoutChanged) viewport.invalidate();
				processContent();
				if (layoutChanged) reading.refreshActiveTurn();
			}, [
				viewport,
				reading,
				processContent,
				navigationInput,
				ready,
				order,
				lastKey,
				lastIsUser,
				steeringId,
				submissionId,
				running,
				loadedTurns,
				chatScroll
			]);
			const returnToBottom = (0, react.useCallback)(() => {
				navigation.cancel();
				reading.followTail();
			}, [navigation, reading]);
			return {
				listRef,
				columnRef,
				...state,
				busyTurn,
				navigateToTurn: navigation.navigateToTurn,
				loadEarlier: navigation.loadEarlier,
				returnToBottom
			};
		}
		//#endregion
		//#region src/client/chat/ChatView.tsx
		/** Host/OS refusal text for the file-open dialog; empty throws keep a locale fallback. */
		function openFailureMessage(error, fallback) {
			const message = error instanceof Error ? error.message : String(error);
			return message === "" ? fallback : message;
		}
		/**
		* Durable input identities suppress matching echoes in the same render.
		* The last input's Turn also distinguishes an empty opening control from
		* one whose human input or trigger notice is already present.
		*/
		function observedInputs(order, nodes) {
			const observed = /* @__PURE__ */ new Set();
			let lastInputTurn;
			for (const key of order) {
				const node = nodes.get(key);
				if (node === void 0 || node.kind !== "user" && node.kind !== "steering" && node.kind !== "turn-trigger") continue;
				if (node.location.kind === "turn" || node.location.kind === "step") lastInputTurn = node.location.turn.turn;
				if (node.kind === "turn-trigger") continue;
				const source = node.data.source;
				if (source?.kind === "user" && typeof source.rpcId === "string") observed.add(source.rpcId);
			}
			return {
				rpcIds: observed,
				lastInputTurn
			};
		}
		const ChatNodeList = (0, react.memo)(function ChatNodeList({ entries, useChatGroup, pendingInputs, lastInputTurn, ...seatProps }) {
			const rows = entries.map((entry) => {
				switch (entry.kind) {
					case "node": return /* @__PURE__ */ (0, react.createElement)(ChatNodeSeat, {
						...seatProps,
						key: chatRenderKey(entry),
						nodeKey: entry.key,
						...entry.groupPart === void 0 ? {} : { groupPart: entry.groupPart }
					});
					case "group": return /* @__PURE__ */ (0, react.createElement)(ChatGroupSeat, {
						...seatProps,
						key: chatRenderKey(entry),
						groupKey: entry.key,
						useChatGroup
					});
					default: return assertNever(entry);
				}
			});
			const pendingRows = pendingInputs.map((item) => "requestId" in item ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(PendingSubmissionBubble, {
				submission: item,
				renderMessageImages: seatProps.renderMessageImages,
				t: seatProps.t
			}, item.requestId) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(PendingSteeringBubble, {
				content: item.content,
				renderMessageImages: seatProps.renderMessageImages,
				t: seatProps.t
			}, item.id));
			const tail = entries.at(-1);
			const node = tail?.kind === "node" ? seatProps.nodeStore.get(tail.key) : void 0;
			if (node?.kind === "turn-process" && node.location.kind === "turn" && node.location.turn.status === "open" && node.location.turn.turn !== lastInputTurn) {
				const index = pendingInputs.findIndex((item) => "requestId" in item && item.placement === "transcript");
				if (index !== -1) rows.splice(rows.length - 1, 0, ...pendingRows.splice(index, 1));
			}
			return [...rows, ...pendingRows];
		});
		/**
		* The chat view slot entry: pure component over the composed props; each
		* ordered business Node crosses the keyed renderer seat.
		*/
		function ChatView({ useSession, useChat, useChatNode, useChatNodeProcess, useChatGroup, useConversation, useSessions, useStore, actions, renderSlot, sessionId, openFile, openSkill, openExternalLink, loadOlder, loadThrough, loadImage, inspectCall, chatScroll, forkAt, fileMentions, usePresentation, useProjection, t }) {
			const order = useChat((s) => s.order);
			const groupedEntries = useConversation((snapshot) => snapshot.views.grouped("chat")?.entries);
			const entries = (0, react.useMemo)(() => groupedEntries ?? order.map((key) => ({
				kind: "node",
				key
			})), [groupedEntries, order]);
			const nodeStore = useChat((s) => s.nodes);
			const turnNavigationItems = useChat((s) => s.navigation.items());
			const turnOutline = useProjection("turnOutline");
			const railItems = (0, react.useMemo)(() => mergeTurnRailItems(turnNavigationItems, turnOutline), [turnNavigationItems, turnOutline]);
			const inbox = useProjection("inbox");
			const cwd = useSessions((s) => s.byId[sessionId]?.cwd);
			const fileImages = (0, react.useMemo)(() => ({
				resolve: (path) => fileMediaUrl(document.baseURI, resolveWorkspacePath(cwd, path)),
				labels: {
					open: t("image.open"),
					loading: t("image.loading"),
					failed: t("image.failed"),
					dialog: t("image.dialog"),
					close: t("image.close")
				}
			}), [cwd, t]);
			const running = useSession((s) => s.running);
			const openState = useSession((s) => s.openState);
			const openError = useSession((s) => s.openError);
			const hasMore = useSession((s) => s.hasMore);
			const loadingOlder = useSession((s) => s.loadingOlder);
			const [fileOpenError, setFileOpenError] = (0, react.useState)(null);
			const [fileOpenBusy, setFileOpenBusy] = (0, react.useState)(false);
			const fileOpenRequest = (0, react.useRef)(0);
			const requestOpenFile = (0, react.useCallback)((path, options) => {
				const id = ++fileOpenRequest.current;
				setFileOpenBusy(true);
				(options === void 0 ? openFile(path) : openFile(path, options)).then(() => {
					if (id !== fileOpenRequest.current) return;
					setFileOpenError(null);
					setFileOpenBusy(false);
				}, (error) => {
					if (id !== fileOpenRequest.current) return;
					setFileOpenError({
						path,
						message: openFailureMessage(error, t("fileOpen.unknown"))
					});
					setFileOpenBusy(false);
				});
			}, [openFile, t]);
			const closeFileOpenError = (0, react.useCallback)(() => {
				fileOpenRequest.current += 1;
				setFileOpenError(null);
				setFileOpenBusy(false);
			}, []);
			const inboxSteering = (0, react.useMemo)(() => inbox?.["next-step"].filter((message) => message.source.kind === "user") ?? [], [inbox]);
			const pendingSubmissions = useSession((s) => s.pendingSubmissions);
			const [visibleSubmissions, lastInputTurn] = (0, react.useMemo)(() => {
				if (pendingSubmissions.length === 0) return [pendingSubmissions, void 0];
				const observed = observedInputs(order, nodeStore);
				return [pendingSubmissions.filter((submission) => submission.placement !== "queued" && !observed.rpcIds.has(submission.requestId)), observed.lastInputTurn];
			}, [
				pendingSubmissions,
				order,
				nodeStore
			]);
			const pendingInputs = (0, react.useMemo)(() => {
				const local = new Map(visibleSubmissions.map((submission) => [submission.requestId, submission]));
				const localIds = new Set(pendingSubmissions.filter((submission) => submission.placement !== "queued").map((submission) => submission.requestId));
				return [...inboxSteering.flatMap((item) => {
					const source = item.source;
					if (source.kind !== "user" || !("rpcId" in source)) return [item];
					const submission = local.get(source.rpcId);
					if (submission === void 0) return localIds.has(source.rpcId) ? [] : [item];
					local.delete(source.rpcId);
					return [submission];
				}), ...local.values()];
			}, [
				inboxSteering,
				pendingSubmissions,
				visibleSubmissions
			]);
			const renderMessageImages = (0, react.useCallback)((owner) => renderSlot("conversation.message.images", {
				...owner,
				loadImage
			}), [loadImage, renderSlot]);
			const firstKey = order[0];
			const firstSeq = firstKey === void 0 ? null : nodeStore.get(firstKey)?.anchorSeq ?? null;
			const lastKey = order.at(-1) ?? null;
			const latestSteering = pendingInputs.findLast((item) => "source" in item);
			const steeringId = latestSteering?.source.kind === "user" && "rpcId" in latestSteering.source ? latestSteering.source.rpcId : latestSteering?.id ?? null;
			const scroll = useChatScroll({
				ready: openState === "open",
				order,
				firstSeq,
				lastKey,
				running,
				loadingOlder,
				hasMore,
				chatScroll,
				loadOlder,
				loadThrough,
				lastIsUser: lastKey !== null && nodeStore.get(lastKey)?.kind === "user",
				steeringId,
				submissionId: visibleSubmissions.at(-1)?.requestId ?? null,
				loadedTurns: turnNavigationItems
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: ChatView_module_css_default.frame,
				children: [
					scroll.initialized && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(TurnNavigator, {
						items: railItems,
						activeTurn: scroll.activeTurn,
						busyTurn: scroll.busyTurn,
						onNavigate: scroll.navigateToTurn,
						t
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						className: ChatView_module_css_default.root,
						"data-chat-following-tail": scroll.followingTail ? "" : void 0,
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							ref: scroll.listRef,
							className: ChatView_module_css_default.scroll,
							children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
								ref: scroll.columnRef,
								className: ChatView_module_css_default.column,
								"data-chat-flow": "",
								children: [
									openState === "loading" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
										className: ChatView_module_css_default.hint,
										children: t("chat.loadingHistory")
									}),
									openState === "error" && openError !== null && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
										className: ChatView_module_css_default.openError,
										children: t("chat.loadError", {
											message: openError.message,
											code: openError.code
										})
									}),
									hasMore && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
										className: ChatView_module_css_default.older,
										children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
											type: "button",
											disabled: loadingOlder,
											onClick: scroll.loadEarlier,
											children: loadingOlder ? t("loading") : t("chat.loadOlder")
										})
									}),
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.MarkdownDelegateProvider, {
										openExternalLink,
										openFile: requestOpenFile,
										fileImages,
										children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ChatNodeList, {
											entries,
											pendingInputs,
											lastInputTurn,
											nodeStore,
											useChatGroup,
											useChatNode,
											useChatNodeProcess,
											usePresentation,
											useStore,
											actions,
											cwd,
											openFile: requestOpenFile,
											openSkill,
											inspectCall,
											forkAt,
											loadImage,
											renderMessageImages,
											fileMentions,
											renderSlot,
											t
										})
									})
								]
							})
						})
					}),
					!scroll.followingTail && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						className: ChatView_module_css_default.toBottomSlot,
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
							type: "button",
							className: ChatView_module_css_default.toBottom,
							"aria-label": t("chat.toBottom"),
							onClick: scroll.returnToBottom,
							children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronDownOutlineRegular, {})
						})
					}),
					fileOpenError !== null && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(FileOpenErrorDialog, {
						message: fileOpenError.message,
						busy: fileOpenBusy,
						onClose: closeFileOpenError,
						onRetry: () => {
							requestOpenFile(fileOpenError.path);
						},
						t
					})
				]
			});
		}
		/** In-page Host open-path refusal: the wire reason plus a retry of the same path. */
		function FileOpenErrorDialog({ message, busy, onClose, onRetry, t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Modal, {
				open: true,
				onClose,
				closeLabel: t("close"),
				title: t("fileOpen.title"),
				description: message,
				footer: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
					variant: "outline",
					className: ChatView_module_css_default.modalAction,
					onClick: onClose,
					children: t("cancel")
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
					variant: "primary",
					className: ChatView_module_css_default.modalAction,
					disabled: busy,
					onClick: onRetry,
					children: t("retry")
				})] })
			});
		}
		//#endregion
		//#region src/client/locale.ts
		/** Chat-owned locale namespace and dictionaries. */
		/** Namespace for Chat target, node, statistics, and details copy. */
		const NS = "chat";
		/** Simplified Chinese dictionary and key-set source of truth. */
		const zh = {
			"message.stepProcess.thinking": "正在分析请求",
			"message.stepProcess.read": "正在读取文件",
			"message.stepProcess.readImage": "正在读取图片",
			"message.stepProcess.write": "正在写入文件",
			"message.stepProcess.search": "正在搜索代码",
			"message.stepProcess.edit": "正在编辑文件",
			"message.stepProcess.commands": "正在运行命令",
			"message.stepProcess.code": "正在运行代码",
			"message.stepProcess.webSearch": "正在搜索网页",
			"message.stepProcess.webFetch": "正在访问网页",
			"message.stepProcess.subagents": "正在协调子智能体",
			"message.stepProcess.plan": "正在更新计划",
			"message.stepProcess.questions": "等待你的操作",
			"message.stepProcess.tools": "正在调用工具",
			"message.stepProcess.prepare.read": "准备读取文件",
			"message.stepProcess.prepare.readImage": "准备读取图片",
			"message.stepProcess.prepare.write": "准备写入文件",
			"message.stepProcess.prepare.search": "准备搜索代码",
			"message.stepProcess.prepare.edit": "准备编辑文件",
			"message.stepProcess.prepare.commands": "准备运行命令",
			"message.stepProcess.prepare.code": "准备运行代码",
			"message.stepProcess.prepare.webSearch": "准备搜索网页",
			"message.stepProcess.prepare.webFetch": "准备访问网页",
			"message.stepProcess.prepare.subagents": "准备协调子智能体",
			"message.stepProcess.prepare.plan": "准备更新计划",
			"message.stepProcess.prepare.questions": "准备提问",
			"message.stepProcess.prepare.tools": "准备调用工具",
			"message.stepProcess.done.thinking": "已完成分析",
			"message.stepProcess.done.read": "已读取文件",
			"message.stepProcess.done.readImage": "已读取图片",
			"message.stepProcess.done.write": "已写入文件",
			"message.stepProcess.done.search": "已搜索代码",
			"message.stepProcess.done.edit": "修改了文件",
			"message.stepProcess.done.commands": "执行了命令",
			"message.stepProcess.done.code": "运行了代码",
			"message.stepProcess.done.webSearch": "已搜索网页",
			"message.stepProcess.done.webFetch": "已访问网页",
			"message.stepProcess.done.subagents": "已协调子智能体",
			"message.stepProcess.done.plan": "更新了计划",
			"message.stepProcess.done.questions": "向用户提出了问题",
			"message.stepProcess.done.tools": "已调用工具",
			"message.stepProcess.joinTwo": "{first}并{second}",
			"message.stepProcess.comma": "，",
			"message.stepProcess.sharedPrefix": "已",
			"message.stepProcess.more": "{title}等",
			"message.trigger.request": "收到执行请求",
			"message.trigger.goal": "继续执行目标",
			"message.trigger.agent": "收到任务消息",
			"message.trigger.team": "收到团队消息",
			"message.trigger.subagent": "子任务状态更新",
			"message.trigger.github": "收到 GitHub 事件",
			"message.trigger.webhook": "收到外部事件",
			"message.trigger.schedule": "定时任务",
			"message.trigger.job": "后台任务状态更新",
			"message.trigger.plugin": "插件状态更新",
			"message.trigger.explanation": "这条通知触发了本轮回复。",
			"message.turnProcess.worked": "已完成工作",
			"message.turnProcess.deepDivingFor": "深度求索中，用时{duration}",
			"message.turnProcess.took": "用时 {duration}",
			"message.turnProcess.failed": "处理失败",
			"view.chat": "对话",
			"image.open": "查看大图",
			"image.loading": "加载图片…",
			"image.failed": "图片无法预览",
			"image.dialog": "图片预览",
			"image.close": "关闭图片预览",
			"number.groupSeparator": ",",
			"duration.compactSeconds": "{seconds}秒",
			"duration.compactMinutes": "{minutes}分{seconds}秒",
			"duration.milliseconds": "{milliseconds}毫秒",
			"stats.counts": "{turns} 轮 {steps} 步",
			"stats.cacheHit": "缓存命中 {percent}%",
			"stats.dialog.title": "会话统计",
			"stats.dialog.usageTitle": "Token 用量",
			"stats.dialog.llmTime": "模型用时",
			"stats.dialog.toolTime": "工具调用用时",
			"stats.dialog.ttft": "首 token 平均（TTFT）",
			"stats.dialog.speed": "输出速度（TPS）",
			"chat.loadingHistory": "载入历史…",
			"chat.loadError": "历史加载失败：{message}（{code}）",
			"chat.loadOlder": "加载更早",
			"chat.toBottom": "回到底部",
			"chat.deepDiving": "深度求索中",
			"chat.turnNavigation.label": "轮次导航",
			"chat.turnNavigation.jump": "跳转到第 {turn} 轮",
			"chat.turnNavigation.jumpLoad": "加载并跳转到第 {turn} 轮",
			"chat.turnNavigation.turn": "第 {turn} 轮",
			"settings.performance.title": "性能与用量",
			"settings.performance.description": "选择性能与用量信息展示的详细程度",
			"settings.performance.compact": "简洁",
			"settings.performance.detailed": "详细",
			"settings.links.title": "网页链接默认打开方式",
			"settings.links.description": "对话中网页链接的打开位置",
			"settings.links.sidebar": "应用内侧边栏",
			"settings.links.newTab": "默认浏览器",
			"settings.transcript.title": "工作步骤展示",
			"settings.transcript.description": "选择希望看到多少工具调用细节",
			"settings.transcript.compact": "简洁",
			"settings.transcript.standard": "标准",
			"settings.transcript.detailed": "详细",
			"settings.transcript.verbose": "完全展开",
			"fileOpen.title": "无法打开文件",
			"fileOpen.unknown": "无法打开此文件",
			"message.extraBlock": "附加内容块",
			"message.systemPrompt": "系统提示词",
			"message.systemPromptUpdate": "系统提示词更新",
			"message.toolAdded": "已添加工具：{name}",
			"message.toolRemoved": "已移除工具：{name}",
			"message.toolsAdded": "新增：{names}",
			"message.toolsAddedCount": "新增 {count} 个",
			"message.toolsChanged": "新增 {added} 个，移除 {removed} 个",
			"message.toolsRemoved": "移除：{names}",
			"message.toolsRemovedCount": "移除 {count} 个",
			"message.toolsUpdated": "工具已更新",
			"message.contextInjection": "上下文注入",
			"message.contextRecall": "跨会话召回",
			"message.referenceSummary": "引用会话 · {labels}",
			"message.referenceSeparator": "、",
			"message.context.instructions.loaded": "已载入",
			"message.context.instructions.added": "已新增",
			"message.context.instructions.updated": "已更新",
			"message.context.instructions.removed": "已移除",
			"message.context.catalog.replaced": "替换目录",
			"message.context.catalog.more": "…还有 {count} 条",
			"message.context.snapshot.supersedes": "取代先前的快照",
			"message.context.relay.from": "来自会话 {session}",
			"message.context.recall.counts": "保留 {retained} 条 · 省略 {omitted} 条",
			"message.context.recall.truncated": "已截断",
			"message.compaction": "上下文已压缩",
			"message.compaction.running": "正在压缩…",
			"message.compaction.completed": "已压缩 {items} 条历史记录（约 {tokens} tokens）",
			"message.compaction.expand": "点击查看压缩摘要",
			"message.compaction.unavailable": "压缩摘要不可用",
			"message.compaction.commandTitle": "compact",
			"message.think": "思考",
			"message.unknownSurface": "未知 surface 事件：{type}",
			"message.unknownBlock": "未知内容块",
			"message.turnProcess.toolCalls.one": "{count} 次工具调用",
			"message.turnProcess.toolCalls.other": "{count} 次工具调用",
			"message.turnProcess.messages.one": "{count} 条消息",
			"message.turnProcess.messages.other": "{count} 条消息",
			"message.turnProcess.subagents.one": "{count} 个 subagent",
			"message.turnProcess.subagents.other": "{count} 个 subagent",
			"message.turnProcess.thoughtForAWhile": "已思考",
			"message.turnProcess.separator": " · ",
			"message.stopped": "已停止",
			"message.branch": "在新对话中分支",
			"message.branchUnavailable": "仅可从已完成轮次的最后一条消息分支",
			"message.retry.active": "正在重试模型请求",
			"message.retry.cancelled": "模型请求重试已取消",
			"message.retry.started": "已重试模型请求",
			"message.retry.scheduled": "等待重试模型请求",
			"message.retry.status": "{label}（{retry}/{maximum}） · {seconds}s",
			"message.retry.delay": "重试延迟：",
			"message.retry.failure": "失败原因：",
			"message.failure.auth": "API 密钥无效",
			"message.accountStopped": "任务已停止",
			"message.failure.accountSignedOut": "任务已因退出 DeepSeek 登录而停止。",
			"message.failure.accountSignInRequired": "请先登录 DeepSeek，并确认请求地址支持账号认证。",
			"message.failure.quota": "当前请求的额度已用尽",
			"message.turnError": "本轮运行失败",
			"message.maxTokens": "已达到输出 token 上限",
			"message.maxTokens.hint": "回答被截断，已有输出保留在对话中。发送“继续”可让模型接着输出。",
			"message.tokensPerSecond": "{tps} tok/s",
			"message.turnUsage.title": "本轮用量",
			"message.turnUsage.consumed": "用量 {total}",
			"message.turnUsage.model": "提供方 / 模型",
			"message.turnUsage.cacheHit": "缓存命中",
			"message.turnUsage.input": "未缓存输入",
			"message.turnUsage.cacheRead": "缓存读取",
			"message.turnUsage.cacheWrite": "缓存写入",
			"message.turnUsage.output": "输出",
			"message.turnUsage.reasoning": "（其中推理 {tokens}）",
			"message.turnUsage.count": "{count} tok",
			"duration.seconds": "{seconds}秒",
			"duration.minutes": "{minutes}分{seconds}秒",
			"duration.hours": "{hours}小时{minutes}分{seconds}秒",
			"command.running": "执行中…",
			"command.failed": "指令失败",
			"command.done": "已完成",
			"command.title": "指令",
			"row.running": "运行中",
			"row.failed": "失败",
			"json.truncated": "… 已截断，共 {total} 字符",
			"clock.md": "{m}月{d}日",
			"clock.ymd": "{y}年{m}月{d}日"
		};
		/** English dictionary, checked against the Chinese key set. */
		const en = {
			"message.stepProcess.thinking": "Analyzing the request",
			"message.stepProcess.read": "Reading files",
			"message.stepProcess.readImage": "Reading images",
			"message.stepProcess.write": "Writing files",
			"message.stepProcess.search": "Searching code",
			"message.stepProcess.edit": "Editing files",
			"message.stepProcess.commands": "Running commands",
			"message.stepProcess.code": "Running code",
			"message.stepProcess.webSearch": "Searching the web",
			"message.stepProcess.webFetch": "Visiting web pages",
			"message.stepProcess.subagents": "Coordinating subagents",
			"message.stepProcess.plan": "Updating the plan",
			"message.stepProcess.questions": "Waiting for your action",
			"message.stepProcess.tools": "Calling tools",
			"message.stepProcess.prepare.read": "Preparing to read files",
			"message.stepProcess.prepare.readImage": "Preparing to read images",
			"message.stepProcess.prepare.write": "Preparing to write files",
			"message.stepProcess.prepare.search": "Preparing to search code",
			"message.stepProcess.prepare.edit": "Preparing to edit files",
			"message.stepProcess.prepare.commands": "Preparing to run commands",
			"message.stepProcess.prepare.code": "Preparing to run code",
			"message.stepProcess.prepare.webSearch": "Preparing to search the web",
			"message.stepProcess.prepare.webFetch": "Preparing to visit web pages",
			"message.stepProcess.prepare.subagents": "Preparing to coordinate subagents",
			"message.stepProcess.prepare.plan": "Preparing to update the plan",
			"message.stepProcess.prepare.questions": "Preparing questions",
			"message.stepProcess.prepare.tools": "Preparing tool calls",
			"message.stepProcess.done.thinking": "Analysis completed",
			"message.stepProcess.done.read": "Read files",
			"message.stepProcess.done.readImage": "Read images",
			"message.stepProcess.done.write": "Wrote files",
			"message.stepProcess.done.search": "Searched code",
			"message.stepProcess.done.edit": "Edited files",
			"message.stepProcess.done.commands": "Ran commands",
			"message.stepProcess.done.code": "Ran code",
			"message.stepProcess.done.webSearch": "Searched the web",
			"message.stepProcess.done.webFetch": "Visited web pages",
			"message.stepProcess.done.subagents": "Coordinated subagents",
			"message.stepProcess.done.plan": "Updated the plan",
			"message.stepProcess.done.questions": "Asked questions",
			"message.stepProcess.done.tools": "Called tools",
			"message.stepProcess.joinTwo": "{first} and {second}",
			"message.stepProcess.comma": ", ",
			"message.stepProcess.sharedPrefix": "",
			"message.stepProcess.more": "{title}, etc.",
			"message.trigger.request": "Execution requested",
			"message.trigger.goal": "Continuing goal",
			"message.trigger.agent": "Task message received",
			"message.trigger.team": "Team message received",
			"message.trigger.subagent": "Subtask status updated",
			"message.trigger.github": "GitHub event received",
			"message.trigger.webhook": "External event received",
			"message.trigger.schedule": "Scheduled task",
			"message.trigger.job": "Background task updated",
			"message.trigger.plugin": "Plugin status updated",
			"message.trigger.explanation": "This notification triggered this response.",
			"message.turnProcess.worked": "Worked",
			"message.turnProcess.deepDivingFor": "Deep diving for {duration}",
			"message.turnProcess.took": "Took {duration}",
			"message.turnProcess.failed": "Failed",
			"view.chat": "Chat",
			"image.open": "View full image",
			"image.loading": "Loading image…",
			"image.failed": "Image preview unavailable",
			"image.dialog": "Image preview",
			"image.close": "Close image preview",
			"number.groupSeparator": ",",
			"duration.compactSeconds": "{seconds}s",
			"duration.compactMinutes": "{minutes}m{seconds}s",
			"duration.milliseconds": "{milliseconds}ms",
			"stats.counts": "{turns} turns {steps} steps",
			"stats.cacheHit": "Cache hit {percent}%",
			"stats.dialog.title": "Session statistics",
			"stats.dialog.usageTitle": "Token usage",
			"stats.dialog.llmTime": "LLM time",
			"stats.dialog.toolTime": "Tool time",
			"stats.dialog.ttft": "Avg time to first token (TTFT)",
			"stats.dialog.speed": "Tokens per second (TPS)",
			"chat.loadingHistory": "Loading history…",
			"chat.loadError": "Failed to load history: {message} ({code})",
			"chat.loadOlder": "Load earlier",
			"chat.toBottom": "Back to bottom",
			"chat.deepDiving": "Deep diving...",
			"chat.turnNavigation.label": "Turn navigation",
			"chat.turnNavigation.jump": "Jump to turn {turn}",
			"chat.turnNavigation.jumpLoad": "Load and jump to turn {turn}",
			"chat.turnNavigation.turn": "Turn {turn}",
			"settings.performance.title": "Performance & usage",
			"settings.performance.description": "Choose how much performance and usage information to show",
			"settings.performance.compact": "Compact",
			"settings.performance.detailed": "Detailed",
			"settings.links.title": "Open chat links in",
			"settings.links.description": "Choose where to open web links",
			"settings.links.sidebar": "In-App Sidebar",
			"settings.links.newTab": "Default Browser",
			"settings.transcript.title": "Work details",
			"settings.transcript.description": "Choose how much detail to show for tool calls",
			"settings.transcript.compact": "Compact",
			"settings.transcript.standard": "Standard",
			"settings.transcript.detailed": "Detailed",
			"settings.transcript.verbose": "Verbose",
			"fileOpen.title": "Couldn’t open file",
			"fileOpen.unknown": "Couldn’t open this file",
			"message.extraBlock": "Extra content block",
			"message.systemPrompt": "System prompt",
			"message.systemPromptUpdate": "System prompt update",
			"message.toolAdded": "Tool added: {name}",
			"message.toolRemoved": "Tool removed: {name}",
			"message.toolsAdded": "Added: {names}",
			"message.toolsAddedCount": "{count} added",
			"message.toolsChanged": "{added} added, {removed} removed",
			"message.toolsRemoved": "Removed: {names}",
			"message.toolsRemovedCount": "{count} removed",
			"message.toolsUpdated": "Tools updated",
			"message.contextInjection": "Context injection",
			"message.contextRecall": "Session recall",
			"message.referenceSummary": "Referenced session · {labels}",
			"message.referenceSeparator": ", ",
			"message.context.instructions.loaded": "loaded",
			"message.context.instructions.added": "added",
			"message.context.instructions.updated": "updated",
			"message.context.instructions.removed": "removed",
			"message.context.catalog.replaced": "Replacement catalog",
			"message.context.catalog.more": "… {count} more",
			"message.context.snapshot.supersedes": "Supersedes earlier snapshots",
			"message.context.relay.from": "From session {session}",
			"message.context.recall.counts": "{retained} kept · {omitted} omitted",
			"message.context.recall.truncated": "truncated",
			"message.compaction": "Context compacted",
			"message.compaction.running": "Compacting context…",
			"message.compaction.completed": "Compacted {items} history items (~{tokens} tokens)",
			"message.compaction.expand": "View compaction summary",
			"message.compaction.unavailable": "Compaction summary unavailable",
			"message.compaction.commandTitle": "compact",
			"message.think": "Think",
			"message.unknownSurface": "Unknown surface event: {type}",
			"message.unknownBlock": "Unknown content block",
			"message.turnProcess.toolCalls.one": "{count} tool call",
			"message.turnProcess.toolCalls.other": "{count} tool calls",
			"message.turnProcess.messages.one": "{count} message",
			"message.turnProcess.messages.other": "{count} messages",
			"message.turnProcess.subagents.one": "{count} subagent",
			"message.turnProcess.subagents.other": "{count} subagents",
			"message.turnProcess.thoughtForAWhile": "Thought for a while",
			"message.turnProcess.separator": " · ",
			"message.stopped": "Stopped",
			"message.branch": "Branch into a new conversation",
			"message.branchUnavailable": "Available only on the last message of a completed turn",
			"message.retry.active": "Retrying model request",
			"message.retry.cancelled": "Model request retry cancelled",
			"message.retry.started": "Retried model request",
			"message.retry.scheduled": "Waiting to retry model request",
			"message.retry.status": "{label} ({retry}/{maximum}) · {seconds}s",
			"message.retry.delay": "Retry delay: ",
			"message.retry.failure": "Failure reason: ",
			"message.failure.auth": "API key is invalid",
			"message.accountStopped": "Task stopped",
			"message.failure.accountSignedOut": "Stopped because you signed out of DeepSeek.",
			"message.failure.accountSignInRequired": "Sign in to DeepSeek and ensure the request destination supports account authentication.",
			"message.failure.quota": "Request quota exhausted.",
			"message.turnError": "This turn failed",
			"message.maxTokens": "Output token limit reached",
			"message.maxTokens.hint": "The reply was cut off; earlier output is preserved in the conversation. Send \"continue\" to let the model resume.",
			"message.tokensPerSecond": "{tps} tok/s",
			"message.turnUsage.title": "Turn usage",
			"message.turnUsage.consumed": "Usage {total}",
			"message.turnUsage.model": "Provider / model",
			"message.turnUsage.cacheHit": "Cache hit",
			"message.turnUsage.input": "Uncached input",
			"message.turnUsage.cacheRead": "Cached input",
			"message.turnUsage.cacheWrite": "Cache write",
			"message.turnUsage.output": "Output",
			"message.turnUsage.reasoning": " ({tokens} reasoning)",
			"message.turnUsage.count": "{count} tok",
			"duration.seconds": "{seconds}s",
			"duration.minutes": "{minutes}m {seconds}s",
			"duration.hours": "{hours}h {minutes}m {seconds}s",
			"command.running": "Running…",
			"command.failed": "Command failed",
			"command.done": "Completed",
			"command.title": "Command",
			"row.running": "Running",
			"row.failed": "Failed",
			"json.truncated": "… truncated, {total} characters total",
			"clock.md": "{m}/{d}",
			"clock.ymd": "{y}-{m}-{d}"
		};
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-chat/src/client/chat/accessibility.module.css.mjs
		const css$10 = ".goHRka_visuallyHidden{clip:rect(0 0 0 0);white-space:nowrap;width:1px;height:1px;position:absolute;overflow:hidden}";
		const tagId$10 = "@deepseek-ai/dsh-client-ui-chat/accessibility.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$10) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-chat";
			tag.dataset.pluginCss = tagId$10;
			tag.textContent = css$10;
			document.head.appendChild(tag);
		}
		var accessibility_module_css_default = { "visuallyHidden": "goHRka_visuallyHidden" };
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-chat/src/client/chat/ReasoningRow.module.css.mjs
		const css$9 = ".lnsRsa_root{flex-direction:column;display:flex}.lnsRsa_root:not([data-expanded]){contain:size layout;height:calc(24px + var(--dsh-content-font-delta,0px))}.lnsRsa_row{position:relative;overflow:hidden}.lnsRsa_root[data-expanded] [data-open] [data-disclosure-row]{z-index:1;background:var(--dsw-alias-bg-base);position:sticky;top:0}.lnsRsa_root[data-state=running] .lnsRsa_row:after{content:\"\";inset-block:0;background:linear-gradient(90deg, transparent 0%, color-mix(in srgb, var(--dsw-alias-bg-base) 60%, transparent) 55%, transparent 100%);pointer-events:none;width:300px;animation:2.6s ease-out infinite lnsRsa_dsh-reasoning-row-sweep;position:absolute;left:0}@keyframes lnsRsa_dsh-reasoning-row-sweep{0%{left:-300px}90%,to{left:100%}}.lnsRsa_leading{flex-shrink:0}.lnsRsa_chevron{color:var(--dsw-alias-label-secondary)}.lnsRsa_title{font-weight:400}.lnsRsa_separator{background:var(--dsw-alias-label-caption);border-radius:1px;flex:none;width:2px;height:2px;margin:0 8px}.lnsRsa_summary{min-width:0;color:var(--dsw-alias-label-tertiary);font-size:var(--dsh-content-font-size-secondary,13px);line-height:calc(20px + var(--dsh-content-font-delta-secondary,0px));white-space:nowrap;flex:auto;overflow:hidden}.lnsRsa_summaryText{text-overflow:ellipsis;display:block;overflow:hidden}.lnsRsa_summary[data-streaming]{mask-image:linear-gradient(90deg,#000 calc(100% - 48px),#0000)}.lnsRsa_summary[data-streaming] .lnsRsa_summaryText{text-overflow:clip;overflow:visible}.lnsRsa_root:not([data-preview]) .lnsRsa_separator,.lnsRsa_root:not([data-preview]) .lnsRsa_summary{display:none}.lnsRsa_thinkBody{padding:4px 0 4px calc(22px + var(--dsh-content-font-delta,0px));min-width:0}@media (prefers-reduced-motion:reduce){.lnsRsa_root[data-state=running] .lnsRsa_row:after{animation:none}}";
		const tagId$9 = "@deepseek-ai/dsh-client-ui-chat/ReasoningRow.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$9) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-chat";
			tag.dataset.pluginCss = tagId$9;
			tag.textContent = css$9;
			document.head.appendChild(tag);
		}
		var ReasoningRow_module_css_default = {
			"chevron": "lnsRsa_chevron",
			"dsh-reasoning-row-sweep": "lnsRsa_dsh-reasoning-row-sweep",
			"leading": "lnsRsa_leading",
			"root": "lnsRsa_root",
			"row": "lnsRsa_row",
			"separator": "lnsRsa_separator",
			"summary": "lnsRsa_summary",
			"summaryText": "lnsRsa_summaryText",
			"thinkBody": "lnsRsa_thinkBody",
			"title": "lnsRsa_title"
		};
		//#endregion
		//#region src/client/chat/ReasoningRow.tsx
		/** Assistant reasoning disclosure, independent of Tool-call presentation. */
		const THINK_ICON = /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconThinkOutlineRegular, { size: 14 });
		function firstLine(text) {
			const newline = text.indexOf("\n");
			return newline === -1 ? text : text.slice(0, newline);
		}
		function latestCompletedParagraphFirstLine(text) {
			let summary = "";
			let paragraphStart = 0;
			const separator = /\r?\n(?:[\t ]*\r?\n)+/g;
			while (true) {
				const nextParagraph = separator.exec(text);
				const paragraphEnd = nextParagraph === null ? text.length : nextParagraph.index + nextParagraph[0].indexOf("\n");
				const newline = text.indexOf("\n", paragraphStart);
				if (newline !== -1 && newline <= paragraphEnd) {
					const candidate = text.slice(paragraphStart, newline).trim();
					if (candidate !== "") summary = candidate;
				}
				if (nextParagraph === null) return summary;
				paragraphStart = nextParagraph.index + nextParagraph[0].length;
			}
		}
		/**
		* Render one assistant reasoning block collapsed until the reader opens it. The
		* collapsed summary omits double-asterisk markers; expanded content renders
		* the complete Markdown with secondary typography. A streaming preview advances
		* when a paragraph's first line completes. Mode changes toggle CSS display without unmounting
		* collapsed summaries.
		* @param props.text - complete or streaming reasoning text.
		* @param props.running - whether this block is the streaming tail.
		* @param props.usePresentation - live display-policy selector for this reasoning row.
		* @param props.useDisclosure - independent open state with enclosing-Turn resets.
		* @param props.t - conversation locale seat for status and Markdown actions.
		* @returns the reasoning disclosure.
		*/
		const ReasoningRow = (0, react.memo)(function ReasoningRow({ text, running, usePresentation, useDisclosure, t }) {
			const { expanded, toggle } = useDisclosure();
			const labels = (0, react.useMemo)(() => markdownLabels(t), [t]);
			const summaryText = running ? latestCompletedParagraphFirstLine(text) : firstLine(text);
			const summary = (0, react.useMemo)(() => summaryText.replaceAll("**", ""), [summaryText]);
			const preview = usePresentation((policy) => !expanded && summary !== "" && (running || policy.settledReasoningPreview));
			const collapsedContent = (0, react.useMemo)(() => /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
				className: ReasoningRow_module_css_default.separator,
				"aria-hidden": true
			}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
				className: ReasoningRow_module_css_default.summary,
				"data-streaming": running || void 0,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
					className: ReasoningRow_module_css_default.summaryText,
					children: summary
				})
			})] }), [running, summary]);
			const content = (0, react.useMemo)(() => expanded ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: ReasoningRow_module_css_default.thinkBody,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.MarkdownText, {
					text,
					streaming: running,
					labels,
					variant: "compact"
				})
			}) : void 0, [
				expanded,
				labels,
				running,
				text
			]);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: ReasoningRow_module_css_default.root,
				"data-variant": "think",
				"data-state": running ? "running" : "ok",
				"data-expanded": expanded || void 0,
				"data-preview": preview || void 0,
				children: [running && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
					className: accessibility_module_css_default.visuallyHidden,
					children: t("row.running")
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.DisclosureRow, {
					rowClassName: ReasoningRow_module_css_default.row,
					leadingClassName: ReasoningRow_module_css_default.leading,
					titleClassName: ReasoningRow_module_css_default.title,
					chevronClassName: ReasoningRow_module_css_default.chevron,
					icon: THINK_ICON,
					title: t("message.think"),
					open: expanded,
					expandable: true,
					expandOnRowClick: true,
					onToggle: toggle,
					collapsedContent,
					children: content
				})]
			});
		});
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-chat/src/client/chat/AssistantMarkdown.module.css.mjs
		const css$8 = ".vFayGa_root{font-size:var(--dsh-content-font-size,14px);line-height:calc(24px + var(--dsh-content-font-delta,0px));color:var(--dsw-alias-label-primary);flex-direction:column;display:flex}.vFayGa_body{flex-direction:column;gap:16px;display:flex}.vFayGa_body .md-table-wide{--dsh-table-spare:max(0px, calc((100cqw - var(--dsh-chat-content-width)) / 2));--dsh-table-lead:calc(var(--dsh-table-spare) + min(var(--dsh-chat-content-width), 100cqw) - 100%);box-sizing:border-box;width:calc(100% + var(--dsh-table-lead) + var(--dsh-table-spare));max-width:none;margin-left:calc(-1 * var(--dsh-table-lead));padding-left:var(--dsh-table-lead)}.vFayGa_body .md-table-wide>table{z-index:1;position:relative}.vFayGa_body>[data-turn-process-inline][hidden]{margin-bottom:-16px}.vFayGa_stopped{border-radius:var(--dsw-radius-sm);background:var(--dsw-alias-interactive-bg-hover);color:var(--dsw-alias-label-tertiary);align-self:flex-start;padding:0 6px;font-size:11px;line-height:18px}.vFayGa_actions{margin-top:16px;margin-left:-6px}";
		const tagId$8 = "@deepseek-ai/dsh-client-ui-chat/AssistantMarkdown.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$8) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-chat";
			tag.dataset.pluginCss = tagId$8;
			tag.textContent = css$8;
			document.head.appendChild(tag);
		}
		var AssistantMarkdown_module_css_default = {
			"actions": "vFayGa_actions",
			"body": "vFayGa_body",
			"root": "vFayGa_root",
			"stopped": "vFayGa_stopped"
		};
		//#endregion
		//#region src/client/chat/AssistantMarkdown.tsx
		/**
		* Standalone fallback for image destinations (query/fragment suffixes are ignored).
		* Chat fileImages resolves decoded file references against cwd; pathImages also
		* serves this component outside that provider and accepts legacy image URL suffixes.
		* Resolve an authored absolute image path against the document's file API.
		* @param base - canonical `document.baseURI` at render time.
		* @param value - authored Markdown destination; URL escapes are decoded once.
		* @returns an absolute Web or Desktop file-API URL, or undefined for unsupported
		* protocols and non-local paths.
		*/
		function localPathMediaUrl(base, value) {
			let path;
			try {
				path = decodeURIComponent(value.split(/[?#]/u)[0] ?? "");
			} catch {
				return;
			}
			return fileMediaUrl(base, path);
		}
		/** Reasoning block as the Think variant summary row (figma 39:28304). */
		const AssistantMarkdown = (0, react.memo)(function AssistantMarkdown({ blocks, streaming, interrupted, renderMessageImages, groupPart, useDisclosure, reasoningHidden = false, usePresentation, revealProcess, mentions, t }) {
			const labels = (0, react.useMemo)(() => markdownLabels(t), [t]);
			const pathImages = (0, react.useMemo)(() => {
				return { resolve: (value) => localPathMediaUrl(document.baseURI, value) };
			}, []);
			const last = blocks.length - 1;
			if (!(streaming || interrupted === true || blocks.some((block) => block.kind !== "tool-call"))) return null;
			const rendered = [];
			for (let i = 0; i < blocks.length; i++) {
				const block = blocks[i];
				if (block === void 0) continue;
				if (groupPart === "reasoning" && block.kind !== "reasoning") continue;
				if (groupPart === "response" && block.kind === "reasoning") continue;
				switch (block.kind) {
					case "text":
						rendered.push(/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.MarkdownText, {
							text: block.text,
							streaming,
							labels,
							fileMentions: mentions,
							pathImages
						}, i));
						break;
					case "reasoning":
						rendered.push(/* @__PURE__ */ (0, react_jsx_runtime.jsx)(ProcessReasoning, {
							hidden: reasoningHidden,
							reveal: revealProcess,
							children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ReasoningRow, {
								text: block.text,
								running: streaming && i === last,
								usePresentation,
								useDisclosure,
								t
							})
						}, i));
						break;
					case "image": {
						const start = i;
						const group = [block];
						while (i + 1 < blocks.length) {
							const next = blocks[i + 1];
							if (next === void 0 || next.kind !== "image") break;
							group.push(next);
							i += 1;
						}
						rendered.push(/* @__PURE__ */ (0, react_jsx_runtime.jsx)(react.Fragment, { children: renderMessageImages({
							images: group.map(({ attachment }) => ({ attachment })),
							align: "start"
						}) }, start));
						break;
					}
					case "tool-call": break;
					default: rendered.push(/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.JsonBlock, {
						label: t("message.unknownBlock"),
						payload: block.block,
						truncatedLabel: (total) => t("json.truncated", { total })
					}, i));
				}
			}
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: AssistantMarkdown_module_css_default.root,
				"data-streaming": streaming || void 0,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: AssistantMarkdown_module_css_default.body,
					children: [rendered, interrupted && (groupPart === void 0 || groupPart === "response" || !blocks.some((block) => block.kind !== "reasoning" && block.kind !== "tool-call")) && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: AssistantMarkdown_module_css_default.stopped,
						children: t("message.stopped")
					})]
				})
			});
		});
		function ProcessReasoning({ hidden, reveal, children }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				ref: useSearchableHidden(hidden, reveal ?? NOOP),
				"data-turn-process-inline": hidden || void 0,
				children
			});
		}
		const NOOP = () => {};
		//#endregion
		//#region src/client/chat/AssistantNodeView.tsx
		/** Streaming, settled, and interrupted Assistant states share one keyed renderer instance. */
		const AssistantNodeView = (0, react.memo)(function AssistantNodeView({ node, groupPart, useDisclosure, useTurnData, turnProcess, openFile, renderMessageImages, fileMentions, usePresentation, t }) {
			const data = node.data;
			const turn = node.location.kind === "turn" || node.location.kind === "step" ? node.location.turn : void 0;
			const tail = useTurnData("turn-tail");
			const owner = (0, react.useMemo)(() => {
				if (turn?.status !== "closed" || data.finalNode === void 0) return void 0;
				if (tail?.closing?.finalNode.seq !== data.finalNode.seq) return void 0;
				return {
					turn,
					seq: data.finalNode.seq,
					openFile
				};
			}, [
				data.finalNode,
				openFile,
				tail,
				turn
			]);
			const mentions = (0, react.useMemo)(() => owner === void 0 ? void 0 : fileMentions(owner), [fileMentions, owner]);
			const reasoningHidden = turnProcess !== void 0 && turnProcess.foldable && turnProcess.spec.answerStep === data.step && turnProcess.spec.inlineReasoning && !turnProcess.open;
			const revealProcess = (0, react.useCallback)(() => {
				turnProcess?.setOpen(true);
			}, [turnProcess]);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(AssistantMarkdown, {
				blocks: data.blocks,
				groupPart,
				useDisclosure,
				streaming: data.status === "running",
				interrupted: data.status === "interrupted",
				renderMessageImages,
				reasoningHidden,
				usePresentation,
				revealProcess,
				mentions,
				t
			});
		});
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-chat/src/client/chat/GenericCommandCard.module.css.mjs
		const css$7 = ".zV-GFa_root{flex-direction:column;display:flex}.zV-GFa_leading{flex-shrink:0}.zV-GFa_chevron{color:var(--dsw-alias-label-secondary)}.zV-GFa_title{font-weight:400;transition:color .1s}.zV-GFa_separator{background:var(--dsw-alias-label-caption);border-radius:1px;flex:none;width:2px;height:2px;margin:0 8px}.zV-GFa_summary{min-width:0;color:var(--dsw-alias-label-tertiary);font-size:var(--dsh-content-font-size-secondary,13px);line-height:calc(24px + var(--dsh-content-font-delta,0px));text-overflow:ellipsis;white-space:nowrap;flex:auto;transition:color .1s;overflow:hidden}.zV-GFa_row:hover .zV-GFa_title,.zV-GFa_row:hover .zV-GFa_summary:not([data-error]){color:var(--dsw-alias-label-primary)}.zV-GFa_summary[data-error],.zV-GFa_body[data-error]{color:var(--dsw-alias-state-error-primary)}.zV-GFa_body{border:.5px solid var(--dsw-alias-border-l1);border-radius:var(--dsw-radius-lg);background:var(--dsw-alias-markdown-code-block);max-height:260px;color:var(--dsw-alias-label-primary);font:var(--dsw-font-markdown-code-block-small);white-space:pre-wrap;margin:4px 0 4px 4px;padding:12px 16px;overflow:auto}";
		const tagId$7 = "@deepseek-ai/dsh-client-ui-chat/GenericCommandCard.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$7) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-chat";
			tag.dataset.pluginCss = tagId$7;
			tag.textContent = css$7;
			document.head.appendChild(tag);
		}
		var GenericCommandCard_module_css_default = {
			"body": "zV-GFa_body",
			"chevron": "zV-GFa_chevron",
			"leading": "zV-GFa_leading",
			"root": "zV-GFa_root",
			"row": "zV-GFa_row",
			"separator": "zV-GFa_separator",
			"summary": "zV-GFa_summary",
			"title": "zV-GFa_title"
		};
		//#endregion
		//#region src/client/chat/GenericCommandCard.tsx
		const COMMAND_ICON = /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconApiOutlineRegular, { size: 14 });
		/** Node state → row state semantic (running while unsettled; outcome kind after). */
		function stateOf(outcome) {
			if (outcome === null) return "running";
			return outcome.kind === "error" ? "error" : "ok";
		}
		/**
		* Render a command summary and its lazily mounted multiline output.
		* @param props - command, locale, and optional running label.
		* @returns the command disclosure.
		*/
		const GenericCommandCard = (0, react.memo)(function GenericCommandCard({ node, t, runningSummary }) {
			const [expanded, setExpanded] = (0, react.useState)(false);
			const text = node.outcome?.text;
			const summary = node.outcome === null ? runningSummary ?? t("command.running") : text ?? (node.outcome.kind === "error" ? t("command.failed") : t("command.done"));
			const title = node.name ?? t("command.title");
			const state = stateOf(node.outcome);
			const running = state === "running";
			const body = text !== void 0 && text.includes("\n") ? text : null;
			const open = expanded && body !== null;
			const toggle = (0, react.useCallback)(() => {
				setExpanded((value) => !value);
			}, []);
			const collapsedContent = (0, react.useMemo)(() => /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
				className: GenericCommandCard_module_css_default.separator,
				"aria-hidden": true
			}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
				className: GenericCommandCard_module_css_default.summary,
				"data-error": state === "error" || void 0,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.TextShimmer, {
					active: running,
					children: summary
				})
			})] }), [
				running,
				state,
				summary
			]);
			const content = (0, react.useMemo)(() => open ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("pre", {
				className: GenericCommandCard_module_css_default.body,
				"data-error": state === "error" || void 0,
				children: body
			}) : void 0, [
				body,
				open,
				state
			]);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: GenericCommandCard_module_css_default.root,
				"data-variant": "others",
				"data-state": state,
				children: [
					state === "running" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: accessibility_module_css_default.visuallyHidden,
						children: t("row.running")
					}),
					state === "error" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: accessibility_module_css_default.visuallyHidden,
						children: t("row.failed")
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.DisclosureRow, {
						rowClassName: GenericCommandCard_module_css_default.row,
						leadingClassName: GenericCommandCard_module_css_default.leading,
						titleClassName: GenericCommandCard_module_css_default.title,
						chevronClassName: GenericCommandCard_module_css_default.chevron,
						icon: COMMAND_ICON,
						title,
						running,
						open,
						expandable: body !== null,
						expandOnRowClick: true,
						keepContentWhenOpen: true,
						onToggle: toggle,
						collapsedContent,
						children: content
					})
				]
			});
		});
		//#endregion
		//#region src/client/chat/CompactionCommandCard.tsx
		/** Render one manual compaction lifecycle without duplicating its checkpoint marker. */
		function CompactionCommandCard({ node, compaction, t }) {
			if (compaction !== void 0) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(CompactionItem, {
				node: compaction,
				title: t("message.compaction.commandTitle"),
				fallbackSummary: node.outcome?.text ?? null,
				t
			});
			if (node.outcome !== null) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(GenericCommandCard, {
				node,
				t
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(GenericCommandCard, {
				node,
				t,
				runningSummary: t("message.compaction.running")
			});
		}
		//#endregion
		//#region src/client/chat/CommandNodeView.tsx
		/** Ordinary command lifecycle renderer with command-name keyed specialization. */
		const CommandNodeView = (0, react.memo)(function CommandNodeView({ node, renderSlot, t }) {
			const command = node.data;
			const owner = (0, react.useMemo)(() => ({ node: command }), [command]);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: ChatView_module_css_default.callRow,
				children: renderSlot("conversation.chat.commandview", owner, {
					entryKey: command.name ?? "",
					fallback: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(GenericCommandCard, {
						...owner,
						t
					})
				})
			});
		});
		/** One integrated `/compact` command and compaction transaction renderer. */
		const ManualCompactionNodeView = (0, react.memo)(function ManualCompactionNodeView({ node, t }) {
			const data = node.data;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: ChatView_module_css_default.callRow,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(CompactionCommandCard, {
					node: data.command,
					...data.compaction === null ? {} : { compaction: data.compaction },
					t
				})
			});
		});
		//#endregion
		//#region src/client/chat/SystemPromptRow.tsx
		/**
		* Render one complete system prompt as a collapsed disclosure whose expanded
		* body is the same opaque context chrome: 141px code-block scrollport and
		* model-facing text with its real line breaks. An in-history update uses the
		* same row under its own title.
		* @param props - Complete prompt text, whether it is an update, and the locale seat.
		* @returns The system-prompt disclosure row.
		*/
		function SystemPromptRow({ text, update = false, t }) {
			const [open, setOpen] = (0, react.useState)(false);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.DisclosureRow, {
				className: ContextInjectionRow_module_css_default.root,
				icon: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconBrowseOutlineRegular, { size: 14 }),
				chevronClassName: ContextInjectionRow_module_css_default.chevron,
				title: t(update ? "message.systemPromptUpdate" : "message.systemPrompt"),
				open,
				expandable: true,
				expandOnRowClick: true,
				onToggle: () => {
					setOpen((value) => !value);
				},
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
					className: ContextInjectionRow_module_css_default.body,
					"data-system-prompt-body": true,
					children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(OpaqueBody, {
						content: [{
							type: "text",
							text
						}],
						source: null,
						t
					})
				})
			});
		}
		/** System-prompt keyed Chat renderer. */
		const SystemPromptNodeView = (0, react.memo)(function SystemPromptNodeView({ node, t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(SystemPromptRow, {
				text: node.data.text,
				update: node.data.update === true,
				t
			});
		});
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-chat/src/client/chat/TurnProcessNodeView.module.css.mjs
		const css$6 = "._7GLJja_root{box-sizing:border-box;width:100%;min-width:0;height:calc(33px + var(--dsh-content-font-delta,0px));border:none;border-bottom:.5px solid var(--dsw-alias-border-l2);color:var(--dsw-alias-label-tertiary);cursor:pointer;text-align:left;background:0 0;align-items:center;padding:0 0 8px;transition:color .1s;display:flex}._7GLJja_root:disabled{cursor:default}._7GLJja_root:not(:disabled):hover{color:var(--dsw-alias-label-primary)}._7GLJja_root:not([data-open]){margin-bottom:8px}._7GLJja_chevron{width:14px;height:14px;color:var(--dsw-alias-label-caption);flex:none;margin-left:4px;transition:transform .1s}._7GLJja_root[data-open] ._7GLJja_chevron{transform:rotate(180deg)}._7GLJja_label{min-width:0;font-size:var(--dsh-content-font-size-secondary,13px);line-height:calc(24px + var(--dsh-content-font-delta,0px));text-overflow:ellipsis;white-space:nowrap;overflow:hidden}@media (prefers-reduced-motion:reduce){._7GLJja_root,._7GLJja_chevron{transition:none}}";
		const tagId$6 = "@deepseek-ai/dsh-client-ui-chat/TurnProcessNodeView.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$6) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-chat";
			tag.dataset.pluginCss = tagId$6;
			tag.textContent = css$6;
			document.head.appendChild(tag);
		}
		var TurnProcessNodeView_module_css_default = {
			"chevron": "_7GLJja_chevron",
			"label": "_7GLJja_label",
			"root": "_7GLJja_root"
		};
		//#endregion
		//#region src/client/chat/TurnProcessNodeView.tsx
		/** Turn-level process disclosure controller. */
		const TurnProcessNodeView = (0, react.memo)(function TurnProcessNodeView({ node, turnProcess, t }) {
			if (turnProcess === void 0) throw new Error("turn-process node requires Turn process owner state");
			const open = !turnProcess.foldable || turnProcess.open;
			const turn = node.location.kind === "turn" || node.location.kind === "step" ? node.location.turn : void 0;
			const [now, setNow] = (0, react.useState)(Date.now);
			const ticking = turn?.status === "open" && turn.start !== void 0;
			(0, react.useEffect)(() => {
				if (!ticking) return;
				setNow(Date.now());
				const timer = setInterval(() => {
					setNow(Date.now());
				}, LIVE_RUN_CLOCK_INTERVAL_MS);
				return () => {
					clearInterval(timer);
				};
			}, [ticking]);
			if (turn?.start === void 0 && turn?.status !== "closed") return null;
			const canCollapse = turnProcess.foldable && turnProcess.hasContent && !turnProcessAlwaysOpen(node);
			const running = turn.status === "open";
			const reason = turn.end?.data.reason.kind;
			const elapsedMs = turn.start === void 0 ? void 0 : Math.max(1e3, (turn.end?.time ?? now) - turn.start.time);
			const duration = elapsedMs === void 0 ? void 0 : running ? formatLiveRunDuration(elapsedMs, t) : formatRunDuration(elapsedMs, t);
			const label = running ? duration === void 0 ? t("chat.deepDiving") : t("message.turnProcess.deepDivingFor", { duration }) : reason === "aborted" ? t("message.stopped") : reason === "error" ? t("message.turnProcess.failed") : duration === void 0 ? t("message.turnProcess.worked") : t("message.turnProcess.took", { duration });
			const announcement = running ? t("chat.deepDiving") : reason === "aborted" ? t("message.stopped") : reason === "error" ? t("message.turnProcess.failed") : t("message.turnProcess.worked");
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
				className: accessibility_module_css_default.visuallyHidden,
				role: "status",
				"aria-live": "polite",
				"aria-atomic": "true",
				children: announcement
			}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
				type: "button",
				className: TurnProcessNodeView_module_css_default.root,
				"data-open": open || void 0,
				"data-turn-process": node.data.turn,
				"data-turn-process-messages": node.data.messageCount,
				"data-turn-process-tool-calls": node.data.toolCallCount,
				"data-turn-process-subagents": node.data.subagentCount,
				disabled: !canCollapse,
				"aria-expanded": turnProcess.hasContent ? open : void 0,
				onClick: (event) => {
					event.currentTarget.focus();
					turnProcess.setOpen(!open);
				},
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
					className: TurnProcessNodeView_module_css_default.label,
					children: label
				}), canCollapse && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronDownOutlineRegular, { className: TurnProcessNodeView_module_css_default.chevron })]
			})] });
		});
		//#endregion
		//#region src/client/chat/token-format.ts
		/**
		* Compact token count: 517 / 12.2K / 517K / 1.2M.
		* @param value - non-negative token count.
		* @param t - Chat locale seat.
		* @returns locale-owned compact display string.
		*/
		function formatTokens(value, t) {
			const scaled = (candidate) => candidate >= 100 ? String(Math.round(candidate)) : String(Math.round(candidate * 10) / 10);
			if (value < 1e3) return String(value);
			if (value < 1e6) return t("number.thousand", { value: scaled(value / 1e3) });
			return t("number.million", { value: scaled(value / 1e6) });
		}
		/**
		* Exact integer token count with locale-owned digit grouping.
		* @param value - non-negative safe integer token count.
		* @param t - Chat locale seat.
		* @returns an unrounded display string.
		*/
		function formatExactTokens(value, t) {
			const digits = String(value);
			const groups = [];
			for (let end = digits.length; end > 0; end -= 3) groups.unshift(digits.slice(Math.max(0, end - 3), end));
			return groups.join(t("number.groupSeparator"));
		}
		/** Round a cache-read ratio to exact percentage units, with positive ties rounded up. */
		function roundedPercentUnits(cacheReadTokens, denominator, decimalPlaces) {
			const scale = (decimalPlaces === 0 ? 1 : 10) * 100;
			const doubledScale = scale * 2;
			const denominatorQuotient = Math.floor(denominator / doubledScale);
			const denominatorRemainder = denominator % doubledScale;
			let lower = 0;
			let upper = scale;
			while (lower < upper) {
				const candidate = Math.floor((lower + upper + 1) / 2);
				const factor = candidate * 2 - 1;
				if (cacheReadTokens >= factor * denominatorQuotient + Math.ceil(factor * denominatorRemainder / doubledScale)) lower = candidate;
				else upper = candidate - 1;
			}
			return lower;
		}
		function displayPercentUnits(units, decimalPlaces) {
			if (decimalPlaces === 0) return String(units);
			const whole = Math.floor(units / 10);
			const tenths = units % 10;
			return tenths === 0 ? String(whole) : `${whole}.${tenths}`;
		}
		/**
		* Display-ready cache-hit share without rounding a partial hit to 100%.
		* @param cacheReadTokens - exact prompt tokens served from cache.
		* @param promptTokens - exact aggregate prompt tokens.
		* @param decimalPlaces - ordinary-ratio precision; partial hits that would
		* round to 100 automatically use enough additional precision to stay honest.
		* @returns percentage text, or null when there was no prompt input.
		*/
		function formatCacheHitPercent(cacheReadTokens, promptTokens, decimalPlaces = 0) {
			if (promptTokens === 0) return null;
			const missedInputTokens = promptTokens - cacheReadTokens;
			if (missedInputTokens === 0) return "100";
			const roundedUnits = roundedPercentUnits(cacheReadTokens, promptTokens, decimalPlaces);
			if (roundedUnits < (decimalPlaces === 0 ? 100 : 1e3)) return displayPercentUnits(roundedUnits, decimalPlaces);
			let distinguishingPlaces = 1;
			let scaledDoubleGap = missedInputTokens * 200;
			const denominatorTens = Math.floor(promptTokens / 10);
			while (scaledDoubleGap <= denominatorTens) {
				scaledDoubleGap *= 10;
				distinguishingPlaces += 1;
			}
			const denominatorOnes = promptTokens % 10;
			let roundedLoss = 5;
			for (let loss = 1; loss < 5; loss += 1) {
				const factor = loss * 2 + 1;
				const threshold = factor * denominatorTens + Math.floor(factor * denominatorOnes / 10);
				if (scaledDoubleGap <= threshold) {
					roundedLoss = loss;
					break;
				}
			}
			return `99.${"9".repeat(distinguishingPlaces - 1)}${10 - roundedLoss}`;
		}
		//#endregion
		//#region src/client/chat/stat-dialog.ts
		/** Viewport margin the placement clamp keeps (the Menu portal margin). */
		const PANEL_MARGIN = 12;
		/** Distance between the trigger's top edge and the panel's bottom. */
		const PANEL_GAP = 8;
		/**
		* Unplaced portal panel: hidden but laid out so the clamp measures real
		* dimensions (the `useAnchoredPosition` measure pass).
		*/
		const MEASURE_STYLE = {
			visibility: "hidden",
			left: 0,
			top: 0
		};
		/**
		* One trigger-anchored dialog seat: open state, viewport-clamped placement, outside-close.
		* @param controlled - external open state; when given the seat reads and writes
		* it instead of owning its own, letting sibling dialogs share one exclusive slot.
		* @returns the seat; spread `pos ?? MEASURE_STYLE` onto the portaled panel.
		*/
		function useStatDialog(controlled) {
			const [ownOpen, setOwnOpen] = (0, react.useState)(false);
			const open = controlled?.open ?? ownOpen;
			const setOpen = controlled?.setOpen ?? setOwnOpen;
			const rootRef = (0, react.useRef)(null);
			const panelRef = (0, react.useRef)(null);
			const pos = (0, _deepseek_ai_dsh_client_ui_primitives.useAnchoredPosition)({
				open,
				anchorRef: rootRef,
				panelRef,
				side: "top",
				gap: PANEL_GAP,
				margin: PANEL_MARGIN
			});
			(0, _deepseek_ai_dsh_client_ui_primitives.useDismissOnOutsidePointer)(rootRef, open, setOpen, panelRef);
			(0, react.useEffect)(() => {
				if (!open) return;
				const onKeyDown = (e) => {
					if (e.key === "Escape") setOpen(false);
				};
				document.addEventListener("keydown", onKeyDown);
				return () => {
					document.removeEventListener("keydown", onKeyDown);
				};
			}, [open, setOpen]);
			return {
				open,
				setOpen,
				rootRef,
				panelRef,
				pos
			};
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-chat/src/client/chat/TurnUsagePanel.module.css.mjs
		const css$5 = ".j0UAAG_root{min-width:0;display:inline-flex}.j0UAAG_root+.j0UAAG_root{margin-left:-6px}.j0UAAG_trigger{min-width:0;height:calc(28px + var(--dsh-content-font-delta,0px));border-radius:var(--dsw-radius-sm);color:var(--dsw-alias-label-tertiary);font-size:calc(var(--dsh-content-font-size-secondary,13px) - 1px);font-variant-numeric:tabular-nums;line-height:calc(24px + var(--dsh-content-font-delta,0px));white-space:nowrap;cursor:pointer;background:0 0;border:none;align-items:center;gap:4px;padding:6px 8px;display:inline-flex}.j0UAAG_label{text-overflow:ellipsis;min-width:0;overflow:hidden}.j0UAAG_trigger svg{width:calc(15px + var(--dsh-content-font-delta,0px));height:calc(15px + var(--dsh-content-font-delta,0px));flex:none}.j0UAAG_trigger:hover,.j0UAAG_trigger[aria-expanded=true]{background:var(--dsw-alias-interactive-bg-hover);color:var(--dsw-alias-label-tertiary)}@media (width<=480px){.j0UAAG_trigger{width:calc(28px + var(--dsh-content-font-delta,0px));justify-content:center;padding:6px}.j0UAAG_trigger .j0UAAG_label{display:none}.j0UAAG_root+.j0UAAG_root{margin-left:0}}";
		const tagId$5 = "@deepseek-ai/dsh-client-ui-chat/TurnUsagePanel.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$5) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-chat";
			tag.dataset.pluginCss = tagId$5;
			tag.textContent = css$5;
			document.head.appendChild(tag);
		}
		var TurnUsagePanel_module_css_default = {
			"label": "j0UAAG_label",
			"root": "j0UAAG_root",
			"trigger": "j0UAAG_trigger"
		};
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-chat/src/client/chat/stat-dialog.module.css.mjs
		const css$4 = ".MFf55a_panel{z-index:1100;box-sizing:border-box;border-radius:var(--dsw-radius-lg);background:var(--dsw-specific-menu);width:max-content;min-width:min(300px,100vw - 24px);max-width:min(440px,100vw - 24px);backdrop-filter:var(--dsw-menu-backdrop-filter);--dsw-elevation-stroke-color:var(--dsw-alias-border-l1);box-shadow:var(--dsw-elevation-prominent);color:var(--dsw-alias-label-secondary);cursor:default;border:0;padding:16px;font-size:12px;line-height:18px;position:fixed}.MFf55a_title{color:var(--dsw-alias-label-primary);justify-content:space-between;gap:16px;margin-bottom:8px;font-weight:500;display:flex}.MFf55a_titleRule{border-top:.5px solid var(--dsw-alias-border-l2);margin-bottom:10px}.MFf55a_titleValue{font-variant-numeric:tabular-nums}.MFf55a_titleLabel{align-items:center;gap:6px;min-width:0;display:inline-flex}.MFf55a_titleLabel svg{flex:none;width:14px;height:14px}.MFf55a_details{color:var(--dsw-alias-label-tertiary);grid-template-columns:minmax(76px,auto) minmax(0,1fr);gap:6px 16px;margin:0;display:grid}.MFf55a_details dt,.MFf55a_details dd{min-width:0;margin:0}.MFf55a_details dd{color:var(--dsw-alias-label-secondary);font-variant-numeric:tabular-nums;text-align:right}.MFf55a_details .MFf55a_route{overflow-wrap:anywhere}.MFf55a_reasoning{color:var(--dsw-alias-label-tertiary);white-space:nowrap}";
		const tagId$4 = "@deepseek-ai/dsh-client-ui-chat/stat-dialog.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$4) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-chat";
			tag.dataset.pluginCss = tagId$4;
			tag.textContent = css$4;
			document.head.appendChild(tag);
		}
		var stat_dialog_module_css_default = {
			"details": "MFf55a_details",
			"panel": "MFf55a_panel",
			"reasoning": "MFf55a_reasoning",
			"route": "MFf55a_route",
			"title": "MFf55a_title",
			"titleLabel": "MFf55a_titleLabel",
			"titleRule": "MFf55a_titleRule",
			"titleValue": "MFf55a_titleValue"
		};
		//#endregion
		//#region src/client/chat/TurnUsagePanel.tsx
		/** Completed-Turn token usage action and its accounting details dialog. */
		function formatCompactCount(value, t) {
			return t("message.turnUsage.count", { count: formatTokens(value, t) });
		}
		function formatExactCount(value, t) {
			return t("message.turnUsage.count", { count: formatExactTokens(value, t) });
		}
		/**
		* Turn-usage IconActions pill with a click-open Turn-usage details dialog.
		* @param props - Turn usage buckets and locale seat.
		* @returns The trigger and, while open, its portaled dialog anchored above the trigger.
		*/
		function TurnUsagePanel({ usage, t }) {
			const { open, setOpen, rootRef, panelRef, pos } = useStatDialog();
			const cacheHit = usage.cacheReadTokens === void 0 ? null : formatCacheHitPercent(usage.cacheReadTokens, usage.totalTokens - usage.outputTokens, 1);
			const total = formatCompactCount(usage.totalTokens, t);
			const routes = usage.routes?.map((route) => `${route.provider}/${route.model}`).join(", ") ?? "";
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
				ref: rootRef,
				className: TurnUsagePanel_module_css_default.root,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
					type: "button",
					className: TurnUsagePanel_module_css_default.trigger,
					"aria-haspopup": "dialog",
					"aria-expanded": open,
					onClick: () => {
						setOpen(!open);
					},
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconDatabaseOutlineRegular, {}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: TurnUsagePanel_module_css_default.label,
						children: t("message.turnUsage.consumed", { total })
					})]
				}), open && (0, react_dom.createPortal)(/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					ref: panelRef,
					className: stat_dialog_module_css_default.panel,
					role: "dialog",
					"aria-label": t("message.turnUsage.title"),
					style: pos ?? MEASURE_STYLE,
					children: [
						/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							className: stat_dialog_module_css_default.title,
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
								className: stat_dialog_module_css_default.titleLabel,
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconDatabaseOutlineRegular, {}), t("message.turnUsage.title")]
							}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
								className: stat_dialog_module_css_default.titleValue,
								children: formatExactCount(usage.totalTokens, t)
							})]
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: stat_dialog_module_css_default.titleRule,
							"aria-hidden": true
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("dl", {
							className: stat_dialog_module_css_default.details,
							"data-turn-usage-details": true,
							children: [
								routes !== "" && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("message.turnUsage.model") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", {
									className: stat_dialog_module_css_default.route,
									children: routes
								})] }),
								cacheHit !== null && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("message.turnUsage.cacheHit") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: `${cacheHit}%` })] }),
								/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("message.turnUsage.input") }),
								/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: formatExactCount(usage.uncachedInputTokens, t) }),
								usage.cacheReadTokens !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("message.turnUsage.cacheRead") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: formatExactCount(usage.cacheReadTokens, t) })] }),
								usage.cacheWriteTokens !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("message.turnUsage.cacheWrite") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: formatExactCount(usage.cacheWriteTokens, t) })] }),
								/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("message.turnUsage.output") }),
								/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("dd", { children: [formatExactCount(usage.outputTokens, t), usage.reasoningTokens !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
									className: stat_dialog_module_css_default.reasoning,
									children: t("message.turnUsage.reasoning", { tokens: formatExactCount(usage.reasoningTokens, t) })
								})] })
							]
						})
					]
				}), document.body)]
			});
		}
		//#endregion
		//#region src/client/chat/turn-assistant.ts
		/**
		* Collect visible prose from one Assistant lifecycle.
		* @param blocks - Assistant content blocks.
		* @returns concatenated text blocks.
		*/
		function assistantText(blocks) {
			return blocks.flatMap((block) => block.kind === "text" ? [block.text] : []).join("");
		}
		//#endregion
		//#region src/client/contract/assistant-content.ts
		/**
		* Test whether Assistant blocks contain a user-facing reply rather than only
		* reasoning or Tool-call protocol material.
		* @param blocks - Assistant content blocks.
		* @returns whether the blocks contain visible reply content.
		*/
		function hasAssistantReplyContent(blocks) {
			return blocks.some((block) => {
				if (block.kind === "reasoning" || block.kind === "tool-call") return false;
				if (block.kind === "text") return block.text.trim() !== "";
				return true;
			});
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-chat/src/client/chat/TurnTailNodeView.module.css.mjs
		const css$3 = ".VorB5a_root{flex-direction:column;gap:16px;display:flex}.VorB5a_actions{margin-top:4px;margin-left:-6px}";
		const tagId$3 = "@deepseek-ai/dsh-client-ui-chat/TurnTailNodeView.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$3) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-chat";
			tag.dataset.pluginCss = tagId$3;
			tag.textContent = css$3;
			document.head.appendChild(tag);
		}
		var TurnTailNodeView_module_css_default = {
			"actions": "VorB5a_actions",
			"root": "VorB5a_root"
		};
		//#endregion
		//#region src/client/chat/TurnTailNodeView.tsx
		function lastContent(snapshot, turn, skipWarning) {
			const keys = snapshot.locations.getTurn(turn);
			for (let index = keys.length - 1; index >= 0; index--) {
				const node = snapshot.nodes.get(keys[index]);
				if (node === void 0 || node.kind === "turn-tail" || node.kind === "turn-process" || skipWarning && node.kind === "turn-max-tokens") continue;
				return node;
			}
		}
		/** Turn-local actions and feature tail over the Location index, independent of Assistant placement. */
		const TurnTailNodeView = (0, react.memo)(function TurnTailNodeView({ node, openFile, forkAt, renderSlot, t, useChat, usePerformanceUsage }) {
			const detailed = usePerformanceUsage((mode) => mode) === "detailed";
			const data = node.data;
			const hasLaterChatNode = useChat((snapshot) => (lastContent(snapshot, data.turn, true)?.anchorSeq ?? -1) > (data.closing?.finalNode.seq ?? data.seq));
			const endsWithResponse = useChat((snapshot) => {
				if (snapshot.timeline.turnOrder.at(-1) !== data.turn) return false;
				const last = lastContent(snapshot, data.turn, false);
				const block = last?.kind === "assistant-step" ? last.data.blocks.findLast((candidate) => candidate.kind !== "text" && candidate.kind !== "reasoning" || candidate.text.trim() !== "") : void 0;
				return block !== void 0 && hasAssistantReplyContent([block]);
			});
			const turn = node.location.kind === "turn" || node.location.kind === "step" ? node.location.turn : void 0;
			if (turn === void 0) return null;
			const closing = data.closing;
			const tail = renderSlot("conversation.chat.turnTail", {
				turn,
				seq: closing?.finalNode.seq ?? data.seq,
				openFile
			});
			if (closing === null) return tail === null ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: TurnTailNodeView_module_css_default.root,
				"data-turn-tail": data.turn,
				children: tail
			});
			const messageId = closing.finalNode.messageId;
			const assistantActions = messageId === void 0 ? null : renderSlot("conversation.chat.assistant-actions", { messageId });
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: TurnTailNodeView_module_css_default.root,
				"data-turn-tail": data.turn,
				"data-actions-reveal": endsWithResponse ? "always" : "hover",
				children: [tail, /* @__PURE__ */ (0, react_jsx_runtime.jsx)(MessageIconActions, {
					text: assistantText(closing.blocks),
					time: closing.time,
					clock: "end",
					onBranch: () => {
						forkAt(data.seq);
					},
					branchUnavailable: data.branchUnavailable || hasLaterChatNode,
					className: TurnTailNodeView_module_css_default.actions,
					extraActions: assistantActions,
					usageAction: detailed && data.tokenUsage !== void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(TurnUsagePanel, {
						usage: data.tokenUsage,
						t
					}) : null,
					t
				})]
			});
		});
		//#endregion
		//#region src/client/chat/turn-trigger.ts
		function record(value) {
			return typeof value === "object" && value !== null && !Array.isArray(value) ? value : {};
		}
		function field(source, key) {
			return typeof source[key] === "string" ? source[key] : "";
		}
		/**
		* Describe a waking message using its source and recognized producer framing.
		* @param node - durable context, including the original notification body.
		* @returns localized title key and source-family icon.
		*/
		function turnTriggerDetails(node) {
			const source = record(node.source);
			const kind = field(source, "kind");
			let title = "message.trigger.request";
			let icon = "request";
			switch (kind) {
				case "goal":
					title = "message.trigger.goal";
					icon = "goal";
					break;
				case "agent-message":
					title = "message.trigger.agent";
					icon = "agent";
					break;
				case "team-message":
					title = "message.trigger.team";
					icon = "team";
					break;
				case "subagent-settled":
					title = "message.trigger.subagent";
					icon = "subagent";
					break;
				case "webhook": {
					const github = field(source, "provider") === "github";
					title = github ? "message.trigger.github" : "message.trigger.webhook";
					icon = github ? "github" : "webhook";
					break;
				}
				case "schedule":
					title = "message.trigger.schedule";
					icon = "schedule";
					break;
				case "tool-jobs":
					title = "message.trigger.job";
					icon = "job";
					break;
				case "cordis-host-runner":
					title = "message.trigger.plugin";
					icon = "plugin";
					break;
				default: break;
			}
			return {
				title,
				icon
			};
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-chat/src/client/chat/TurnTriggerNodeView.module.css.mjs
		const css$2 = ".mZFzQq_root{border:.5px solid var(--dsw-alias-border-l1);border-radius:var(--dsw-radius-xl);background:var(--dsw-alias-markdown-code-block);min-width:0;transition:background-color .1s}.mZFzQq_root:hover{background:var(--dsw-alias-interactive-bg-hover)}.mZFzQq_header{width:100%;color:var(--dsw-alias-label-primary);font:inherit;text-align:left;cursor:pointer;background:0 0;border:0;align-items:center;gap:10px;padding:12px 16px;display:flex}.mZFzQq_icon{color:var(--dsw-alias-label-tertiary);flex:none;display:inline-flex}.mZFzQq_title{font:var(--dsw-font-xs-13);flex:none}.mZFzQq_time{color:var(--dsw-alias-label-caption);font:var(--dsw-font-xxs-12);flex:none;margin-left:auto}.mZFzQq_chevron,.mZFzQq_openChevron{color:var(--dsw-alias-label-tertiary);flex:none}.mZFzQq_openChevron{transform:rotate(180deg)}.mZFzQq_body{padding:0 16px 12px 40px}.mZFzQq_explanation{color:var(--dsw-alias-label-secondary);font:var(--dsw-font-xxs-12);margin:8px 0}.mZFzQq_content{white-space:pre-wrap;overflow-wrap:anywhere;max-height:240px;font:var(--dsw-font-xxs-12);overflow:auto}@media (prefers-reduced-motion:reduce){.mZFzQq_root{transition:none}}";
		const tagId$2 = "@deepseek-ai/dsh-client-ui-chat/TurnTriggerNodeView.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$2) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-chat";
			tag.dataset.pluginCss = tagId$2;
			tag.textContent = css$2;
			document.head.appendChild(tag);
		}
		var TurnTriggerNodeView_module_css_default = {
			"body": "mZFzQq_body",
			"chevron": "mZFzQq_chevron",
			"content": "mZFzQq_content",
			"explanation": "mZFzQq_explanation",
			"header": "mZFzQq_header",
			"icon": "mZFzQq_icon",
			"openChevron": "mZFzQq_openChevron",
			"root": "mZFzQq_root",
			"time": "mZFzQq_time",
			"title": "mZFzQq_title"
		};
		//#endregion
		//#region src/client/chat/TurnTriggerNodeView.tsx
		/** An independent, expandable notice explaining a non-human Turn trigger. */
		const TRIGGER_ICONS = {
			request: _deepseek_ai_dsh_client_ui_primitives.IconContextInjectionOutlineRegular,
			goal: _deepseek_ai_dsh_client_ui_primitives.IconGoalOutlineRegular,
			agent: _deepseek_ai_dsh_client_ui_primitives.IconPaperPlaneOutlineRegular,
			team: _deepseek_ai_dsh_client_ui_primitives.IconAgentPresetOutlineRegular,
			subagent: _deepseek_ai_dsh_client_ui_primitives.IconAgentPresetOutlineRegular,
			github: _deepseek_ai_dsh_client_ui_primitives.IconBranchOutlineRegular,
			webhook: _deepseek_ai_dsh_client_ui_primitives.IconGlobeOutlineRegular,
			schedule: _deepseek_ai_dsh_client_ui_primitives.IconAlarmClockOutlineRegular,
			job: _deepseek_ai_dsh_client_ui_primitives.IconQueueOutlineRegular,
			plugin: _deepseek_ai_dsh_client_ui_primitives.IconCordisPluginOutlineRegular
		};
		/** Render recorded trigger attribution above the whole-Turn disclosure. */
		function TurnTriggerNodeView({ node, t }) {
			const [open, setOpen] = (0, react.useState)(false);
			const bodyId = (0, react.useId)();
			const details = turnTriggerDetails(node.data);
			const TriggerIcon = TRIGGER_ICONS[details.icon];
			const date = new Date(node.data.time);
			const time = formatMessageClock(node.data.time, t);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
				className: TurnTriggerNodeView_module_css_default.root,
				"data-turn-trigger": true,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
					className: TurnTriggerNodeView_module_css_default.header,
					type: "button",
					"aria-expanded": open,
					"aria-controls": bodyId,
					onClick: () => {
						setOpen(!open);
					},
					children: [
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: TurnTriggerNodeView_module_css_default.icon,
							"aria-hidden": true,
							children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(TriggerIcon, { size: 14 })
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: TurnTriggerNodeView_module_css_default.title,
							children: t(details.title)
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)("time", {
							className: TurnTriggerNodeView_module_css_default.time,
							dateTime: date.toISOString(),
							children: time
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronDownOutlineRegular, {
							size: 12,
							className: open ? TurnTriggerNodeView_module_css_default.openChevron : TurnTriggerNodeView_module_css_default.chevron
						})
					]
				}), open && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					id: bodyId,
					className: TurnTriggerNodeView_module_css_default.body,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
						className: TurnTriggerNodeView_module_css_default.explanation,
						children: t("message.trigger.explanation")
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						className: TurnTriggerNodeView_module_css_default.content,
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(NoticeBody, {
							content: node.data.content,
							source: node.data.source,
							t
						})
					})]
				})]
			});
		}
		//#endregion
		//#region src/client/chat/register-node-renderers.ts
		/**
		* Register this package's business renderers behind the keyed Chat Node seat.
		* Renderers whose output depends on the work-details mode receive the policy
		* through their own registration; the seat and the other renderers do not.
		* @param ctx - owning UI Conversation context.
		* @param performanceUsage - live statistics detail preference.
		* @param presentation - live presentation policy.
		*/
		function registerChatNodeRenderers(ctx, performanceUsage, presentation) {
			ctx.slots.inject("conversation.chat.node", () => ctx.slots.register({
				name: "conversation.chat.node",
				key: "user",
				locale: NS
			}, UserMessageNodeView));
			ctx.slots.inject("conversation.chat.node", () => ctx.slots.register({
				name: "conversation.chat.node",
				key: "steering",
				locale: NS
			}, UserMessageNodeView));
			ctx.slots.inject("conversation.chat.node", () => ctx.slots.register({
				name: "conversation.chat.node",
				key: "context",
				locale: NS
			}, ContextMessageNodeView));
			ctx.slots.inject("conversation.chat.node", () => ctx.slots.register({
				name: "conversation.chat.node",
				key: "turn-trigger",
				locale: NS
			}, TurnTriggerNodeView));
			ctx.slots.inject("conversation.chat.node", () => ctx.slots.register({
				name: "conversation.chat.node",
				key: "system-prompt",
				locale: NS
			}, SystemPromptNodeView));
			ctx.slots.inject("conversation.chat.node", () => ctx.slots.register({
				name: "conversation.chat.node",
				key: "assistant-step",
				locale: NS,
				inject: () => ({ hooks: { presentation } })
			}, AssistantNodeView));
			ctx.slots.inject("conversation.chat.node", () => ctx.slots.register({
				name: "conversation.chat.node",
				key: "command",
				locale: NS,
				children: { "conversation.chat.commandview": {
					kind: "keyed",
					scope: "session"
				} }
			}, CommandNodeView));
			ctx.slots.inject("conversation.chat.node", () => ctx.slots.register({
				name: "conversation.chat.node",
				key: "manual-compaction",
				locale: NS
			}, ManualCompactionNodeView));
			ctx.slots.inject("conversation.chat.node", () => ctx.slots.register({
				name: "conversation.chat.node",
				key: "compaction",
				locale: NS
			}, CompactionNodeView));
			ctx.slots.inject("conversation.chat.node", () => ctx.slots.register({
				name: "conversation.chat.node",
				key: "model-retry",
				locale: NS
			}, RetryNodeView));
			ctx.slots.inject("conversation.chat.node", () => ctx.slots.register({
				name: "conversation.chat.node",
				key: "turn-error",
				locale: NS
			}, TurnErrorNodeView));
			ctx.slots.inject("conversation.chat.node", () => ctx.slots.register({
				name: "conversation.chat.node",
				key: "turn-max-tokens",
				locale: NS
			}, TurnMaxTokensNodeView));
			ctx.slots.inject("conversation.chat.node", () => ctx.slots.register({
				name: "conversation.chat.node",
				key: "turn-process",
				locale: NS
			}, TurnProcessNodeView));
			ctx.slots.inject("conversation.chat.node", () => ctx.slots.register({
				name: "conversation.chat.node",
				key: "turn-tail",
				locale: NS,
				inject: () => ({ hooks: { performanceUsage } }),
				children: {
					"conversation.chat.turnTail": {
						kind: "list",
						scope: "session"
					},
					"conversation.chat.assistant-actions": {
						kind: "list",
						scope: "session"
					}
				}
			}, TurnTailNodeView));
			ctx.slots.inject("conversation.chat.node", () => ctx.slots.register({
				name: "conversation.chat.node",
				key: "unknown",
				locale: NS
			}, UnknownNodeView));
		}
		//#endregion
		//#region src/client/contract/turn-metrics.ts
		function usageOutputTokens(usage) {
			if (typeof usage !== "object" || usage === null) return null;
			const value = usage.outputTokens;
			return typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : null;
		}
		/**
		* Read one assistant node's TTFT, decode wall time, and output tokens.
		* @param node - A settled assistant node.
		* @returns Per-part readings with `null` for unrecorded values.
		*/
		function assistantStepReading(node) {
			const timing = node.timing;
			return {
				ttftMs: timing !== void 0 && timing.stepStartTime !== null && timing.firstTokenTime !== null ? Math.max(0, timing.firstTokenTime - timing.stepStartTime) : null,
				decodeMs: timing !== void 0 && timing.firstTokenTime !== null ? Math.max(0, timing.completedTime - timing.firstTokenTime) : null,
				outputTokens: usageOutputTokens(node.usage)
			};
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-chat/src/client/chat/StatsPills.module.css.mjs
		const css$1 = ".EeO94q_root{box-sizing:border-box;min-width:0;max-width:100%;font-size:calc(var(--dsh-content-font-size-secondary,13px) - 1px);line-height:calc(20px + var(--dsh-content-font-delta-secondary,0px));justify-content:center;gap:12px;display:flex}.EeO94q_anchor{min-width:0;display:inline-flex}.EeO94q_pill{box-sizing:border-box;corner-shape:round;max-width:100%;color:var(--dsw-alias-label-tertiary);font:inherit;font-variant-numeric:tabular-nums;line-height:inherit;white-space:nowrap;background:0 0;border:none;border-radius:999px;align-items:center;gap:6px;padding:1px 8px;display:inline-flex}.EeO94q_pill svg{flex:none;width:14px;height:14px}button.EeO94q_pill{cursor:pointer}button.EeO94q_pill:hover,button.EeO94q_pill[aria-expanded=true]{background:var(--dsw-alias-interactive-bg-hover);color:var(--dsw-alias-label-secondary)}.EeO94q_label{text-overflow:ellipsis;min-width:0;overflow:hidden}.EeO94q_sep{color:var(--dsw-alias-separator-primary);margin:0 6px}";
		const tagId$1 = "@deepseek-ai/dsh-client-ui-chat/StatsPills.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$1) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-chat";
			tag.dataset.pluginCss = tagId$1;
			tag.textContent = css$1;
			document.head.appendChild(tag);
		}
		var StatsPills_module_css_default = {
			"anchor": "EeO94q_anchor",
			"label": "EeO94q_label",
			"pill": "EeO94q_pill",
			"root": "EeO94q_root",
			"sep": "EeO94q_sep"
		};
		//#endregion
		//#region src/client/chat/StatsPills.tsx
		/**
		* Fold assistant and tool-result nodes into window-scoped display totals —
		* the FALLBACK for assemblies without the `sessionStats` projection.
		*
		* Every displayed figure rides that durable whole-log projection (and token
		* accounting rides `tokenUsage`) because the window is paged and compaction
		* rewrites it; this fold answers "what is on screen" only when no projection
		* value is served. Its field names deliberately mirror the projection's so
		* the two swap wholesale.
		* @param nodes - snapshot nodes.
		* @returns fallback counts and summed wall times.
		*/
		function deriveStats(nodes) {
			const turns = /* @__PURE__ */ new Set();
			let steps = 0;
			let llmMs = 0;
			let toolMs = 0;
			let ttftMs = 0;
			let ttftSteps = 0;
			let decodeMs = 0;
			let decodeTokens = 0;
			for (const node of nodes) {
				if (node.kind === "tool-result") {
					if (node.callTime !== null) toolMs += Math.max(0, node.time - node.callTime);
					continue;
				}
				if (node.kind !== "assistant") continue;
				turns.add(node.turn);
				steps += 1;
				if (node.timing !== void 0 && node.timing.stepStartTime !== null) llmMs += Math.max(0, node.timing.completedTime - node.timing.stepStartTime);
				const reading = assistantStepReading(node);
				if (reading.ttftMs !== null) {
					ttftMs += reading.ttftMs;
					ttftSteps += 1;
				}
				if (reading.decodeMs !== null && reading.outputTokens !== null) {
					decodeMs += reading.decodeMs;
					decodeTokens += reading.outputTokens;
				}
			}
			return {
				turns: turns.size,
				steps,
				llmMs,
				toolMs,
				ttftMs,
				ttftSteps,
				decodeMs,
				decodeTokens
			};
		}
		/**
		* Compact duration: 45.2s under a minute, 2m42s from there on.
		* @param ms - duration in milliseconds.
		* @returns display string.
		*/
		function formatDuration(ms, t) {
			const s = ms / 1e3;
			if (s < 60) return t("duration.compactSeconds", { seconds: Math.round(s * 10) / 10 });
			const whole = Math.round(s);
			return t("duration.compactMinutes", {
				minutes: Math.floor(whole / 60),
				seconds: whole % 60
			});
		}
		/**
		* Display-ready cache-hit share of prompt-side input over the whole durable log.
		* @param usage - the session's token-usage projection value.
		* @returns integer text when integer rounding stays below 100, otherwise the
		* minimum decimal precision that still rounds below 100; a full hit returns
		* 100, and no billed input returns null.
		*/
		function cacheHitPercent(usage) {
			const denominator = billedInputTokens(usage);
			return formatCacheHitPercent(usage.cacheReadTokens, denominator);
		}
		/**
		* Sum the three disjoint prompt-side billing buckets.
		* @param usage - the session's token-usage projection value.
		* @returns billed input tokens.
		*/
		function billedInputTokens(usage) {
			return usage.uncachedInputTokens + usage.cacheReadTokens + usage.cacheWriteTokens;
		}
		function exactCount(value, t) {
			return t("message.turnUsage.count", { count: formatExactTokens(value, t) });
		}
		function TimePill({ stats, t, dialog }) {
			const { open, setOpen, rootRef, panelRef, pos } = useStatDialog(dialog);
			const counts = t("stats.counts", {
				turns: stats.turns,
				steps: stats.steps
			});
			const tps = stats.decodeMs > 0 ? t("message.tokensPerSecond", { tps: formatTokensPerSecond(stats.decodeTokens / (stats.decodeMs / 1e3)) }) : null;
			const label = /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
				className: StatsPills_module_css_default.label,
				children: [counts, tps !== null && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
					className: StatsPills_module_css_default.sep,
					"aria-hidden": true,
					children: "·"
				}), tps] })]
			});
			if (stats.llmMs <= 0 && stats.toolMs <= 0 && stats.ttftSteps <= 0 && stats.decodeMs <= 0) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
				className: StatsPills_module_css_default.anchor,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
					className: StatsPills_module_css_default.pill,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconGaugeOutlineRegular, {}), label]
				})
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
				ref: rootRef,
				className: StatsPills_module_css_default.anchor,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
					type: "button",
					className: StatsPills_module_css_default.pill,
					"aria-haspopup": "dialog",
					"aria-expanded": open,
					"aria-label": tps === null ? counts : `${counts} · ${tps}`,
					onClick: () => {
						setOpen(!open);
					},
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconGaugeOutlineRegular, {}), label]
				}), open && (0, react_dom.createPortal)(/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					ref: panelRef,
					className: stat_dialog_module_css_default.panel,
					role: "dialog",
					"aria-label": t("stats.dialog.title"),
					style: pos ?? MEASURE_STYLE,
					children: [
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: stat_dialog_module_css_default.title,
							children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
								className: stat_dialog_module_css_default.titleLabel,
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconGaugeOutlineRegular, {}), t("stats.dialog.title")]
							})
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: stat_dialog_module_css_default.titleRule,
							"aria-hidden": true
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("dl", {
							className: stat_dialog_module_css_default.details,
							"data-session-stats-details": true,
							children: [
								stats.llmMs > 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("stats.dialog.llmTime") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: formatDuration(stats.llmMs, t) })] }),
								stats.toolMs > 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("stats.dialog.toolTime") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: formatDuration(stats.toolMs, t) })] }),
								stats.ttftSteps > 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("stats.dialog.ttft") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: formatDuration(stats.ttftMs / stats.ttftSteps, t) })] }),
								stats.decodeMs > 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("stats.dialog.speed") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: t("message.tokensPerSecond", { tps: formatTokensPerSecond(stats.decodeTokens / (stats.decodeMs / 1e3)) }) })] })
							]
						})
					]
				}), document.body)]
			});
		}
		function UsagePill({ usage, t, dialog }) {
			const { open, setOpen, rootRef, panelRef, pos } = useStatDialog(dialog);
			const total = billedInputTokens(usage) + usage.outputTokens;
			const totalText = t("message.turnUsage.count", { count: formatTokens(total, t) });
			const cacheHit = cacheHitPercent(usage);
			const cacheHitText = cacheHit !== null ? t("stats.cacheHit", { percent: cacheHit }) : null;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
				ref: rootRef,
				className: StatsPills_module_css_default.anchor,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
					type: "button",
					className: StatsPills_module_css_default.pill,
					"aria-haspopup": "dialog",
					"aria-expanded": open,
					"aria-label": cacheHitText === null ? totalText : `${totalText} · ${cacheHitText}`,
					onClick: () => {
						setOpen(!open);
					},
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconDatabaseOutlineRegular, {}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
						className: StatsPills_module_css_default.label,
						children: [totalText, cacheHitText !== null && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: StatsPills_module_css_default.sep,
							"aria-hidden": true,
							children: "·"
						}), cacheHitText] })]
					})]
				}), open && (0, react_dom.createPortal)(/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					ref: panelRef,
					className: stat_dialog_module_css_default.panel,
					role: "dialog",
					"aria-label": t("stats.dialog.usageTitle"),
					style: pos ?? MEASURE_STYLE,
					children: [
						/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							className: stat_dialog_module_css_default.title,
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
								className: stat_dialog_module_css_default.titleLabel,
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconDatabaseOutlineRegular, {}), t("stats.dialog.usageTitle")]
							}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
								className: stat_dialog_module_css_default.titleValue,
								children: exactCount(total, t)
							})]
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: stat_dialog_module_css_default.titleRule,
							"aria-hidden": true
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("dl", {
							className: stat_dialog_module_css_default.details,
							"data-session-stats-usage": true,
							children: [
								cacheHit !== null && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("message.turnUsage.cacheHit") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: `${cacheHit}%` })] }),
								/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("message.turnUsage.input") }),
								/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: exactCount(usage.uncachedInputTokens, t) }),
								/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("message.turnUsage.cacheRead") }),
								/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: exactCount(usage.cacheReadTokens, t) }),
								usage.cacheWriteTokens !== 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("message.turnUsage.cacheWrite") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: exactCount(usage.cacheWriteTokens, t) })] }),
								/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dt", { children: t("message.turnUsage.output") }),
								/* @__PURE__ */ (0, react_jsx_runtime.jsx)("dd", { children: exactCount(usage.outputTokens, t) })
							]
						})
					]
				}), document.body)]
			});
		}
		const StatsPills = (0, react.memo)(function StatsPills({ useChat, useProjection, usePerformanceUsage, t }) {
			const mode = usePerformanceUsage((value) => value);
			const settledNodes = useChat((s) => s.legacy.nodes);
			const usage = useProjection("tokenUsage");
			const [openPill, setOpenPill] = (0, react.useState)(null);
			const projected = useProjection("sessionStats");
			const stats = (0, react.useMemo)(() => projected ?? deriveStats(settledNodes), [projected, settledNodes]);
			const hasTokens = usage !== void 0 && (billedInputTokens(usage) > 0 || usage.outputTokens > 0);
			if (mode === "compact") {
				const speed = stats.decodeMs > 0 ? t("message.tokensPerSecond", { tps: formatTokensPerSecond(stats.decodeTokens / (stats.decodeMs / 1e3)) }) : null;
				const cacheHit = hasTokens ? cacheHitPercent(usage) : null;
				if (speed === null && cacheHit === null) return null;
				return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: StatsPills_module_css_default.root,
					"data-composer-stats": true,
					children: [speed !== null && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
						className: StatsPills_module_css_default.pill,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconGaugeOutlineRegular, {}), speed]
					}), cacheHit !== null && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
						className: StatsPills_module_css_default.pill,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconDatabaseOutlineRegular, {}), t("stats.cacheHit", { percent: cacheHit })]
					})]
				});
			}
			if (stats.steps === 0 && !hasTokens) return null;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: StatsPills_module_css_default.root,
				"data-composer-stats": true,
				children: [stats.steps > 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(TimePill, {
					stats,
					t,
					dialog: {
						open: openPill === "time",
						setOpen: (open) => {
							setOpenPill(open ? "time" : null);
						}
					}
				}), hasTokens && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(UsagePill, {
					usage,
					t,
					dialog: {
						open: openPill === "usage",
						setOpen: (open) => {
							setOpenPill(open ? "usage" : null);
						}
					}
				})]
			});
		});
		//#endregion
		//#region src/client/conversation-nodes/common.ts
		/**
		* Relative positions in one durable event's seq neighborhood: interrupted
		* Assistant, its follow-up Nodes, then follow-ups to an ordinary final. The
		* max-tokens notice sits between a closing Assistant and the turn-tail so the
		* tail stays the turn's last node and keeps its branch action enabled.
		*/
		const CHAT_SYNTHETIC_SEQ_OFFSETS = {
			interruptedAssistant: -.9,
			interruptedFollowup: -.8,
			processControl: -.1,
			maxTokensNotice: .05,
			finalizedFollowup: .1
		};
		/**
		* Resolve one Context's best currently loaded event Location.
		* @param context - assembled business Context.
		* @returns start or first-match Location, otherwise unresolved.
		*/
		function contextLocation(context) {
			return context.start?.location ?? context.matches[0]?.location ?? { kind: "unresolved" };
		}
		/**
		* Build one final Chat target Node with the engine-owned stable key.
		* @param context - assembled business Context.
		* @param kind - Chat renderer dispatch key.
		* @param anchorSeq - sortable render position.
		* @param data - renderer-owned payload.
		* @param options - optional Location and visibility overrides.
		* @returns final Chat view Node.
		*/
		function chatNode(context, kind, anchorSeq, data, options = {}) {
			return {
				key: context.key,
				kind,
				id: context.id,
				target: "chat",
				anchorSeq,
				location: options.location ?? contextLocation(context),
				visibility: options.visibility ?? "visible",
				data
			};
		}
		//#endregion
		//#region src/client/conversation-nodes/event-projection.ts
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
		/** Forms Chat presents structurally; unknown merge-extensible values remain opaque. */
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
		* Project a durable message source to the Chat row's role and producer label.
		* @param source - Logged `user/message` source.
		* @returns Role and label rendered by Chat.
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
		* Read distinct labels cited by a durable cross-session recall source.
		* @param source - Logged `user/message` source.
		* @returns Labels in first-seen order.
		*/
		function sessionRecallLabels(source) {
			const record = asRecord(source);
			if (record === null || readString(record, "kind") !== "session-reference") return [];
			return collect(record, "references", "label");
		}
		/**
		* Read the skill name a durable skill-invocation injection loaded.
		* @param source - Logged `user/message` source.
		* @returns The skill name, or null for every other source.
		*/
		function skillInvocationName(source) {
			const record = asRecord(source);
			if (record === null || readString(record, "kind") !== "skill-invocation") return null;
			return readString(record, "name");
		}
		/**
		* Classify finalized Assistant content for Chat rendering.
		* @param content - Core content blocks.
		* @returns Chat blocks in source order.
		*/
		function toAssistantBlocks(content) {
			return content.map(toAssistantBlock);
		}
		/**
		* Classify one finalized Assistant block for Chat rendering.
		* @param block - Core content block.
		* @returns Chat block.
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
		* Create the initial Chat block for one streamed Assistant block kind.
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
		* Convert a durable failure to locale-independent fields safe for Chat.
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
		* Whether a stream chunk carries visible model output for Chat timing.
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
		//#region src/client/conversation-nodes/assistant.ts
		function initialState(turn, step) {
			return {
				turn,
				step,
				blocks: [],
				visibleBlocks: 0,
				firstVisibleSeq: void 0,
				firstVisibleTime: void 0,
				firstTokenTime: void 0,
				final: void 0,
				usage: void 0
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
		function hasVisibleContent(blocks) {
			return blocks.some(blockIsVisible);
		}
		function hasInterruptionEvidence(blocks) {
			return blocks.some((block) => {
				if (block.kind === "text" || block.kind === "reasoning") return block.text.trim() !== "";
				return true;
			});
		}
		function resetForRetry(state) {
			return {
				...initialState(state.turn, state.step),
				firstTokenTime: state.firstTokenTime
			};
		}
		function updateChunk(state, chunk, seq, time) {
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
				case "usage": return {
					...state,
					usage: chunk.usage
				};
				default: return state;
			}
			const visibleBlocks = state.visibleBlocks - Number(previousVisible) + Number(blockIsVisible(blocks[changedIndex]));
			const firstToken = isTokenDelta(chunk);
			return {
				...state,
				blocks,
				visibleBlocks,
				...visibleBlocks > 0 && state.firstVisibleSeq === void 0 ? {
					firstVisibleSeq: seq,
					firstVisibleTime: time
				} : {},
				...firstToken && state.firstTokenTime === void 0 ? { firstTokenTime: time } : {}
			};
		}
		function settleMessage(state, match, event) {
			const blocks = toAssistantBlocks(event.data.message.content);
			return {
				...state,
				blocks,
				visibleBlocks: countVisibleBlocks(blocks),
				final: match,
				usage: event.data.usage
			};
		}
		function closedBoundary(location) {
			if (location.kind === "step" && location.step.status === "closed" && location.step.end !== void 0) return location.step.end;
			if ((location.kind === "step" || location.kind === "turn") && location.turn.status === "closed" && location.turn.end !== void 0) return location.turn.end;
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
					timing: {
						stepStartTime: context.start?.event.time ?? null,
						firstTokenTime: state.firstTokenTime ?? null,
						completedTime: event.time
					},
					...event.data.interrupted === true ? { interrupted: true } : {}
				};
			}
			const location = context.start?.location ?? context.matches.at(-1)?.location;
			const boundary = location === void 0 ? void 0 : closedBoundary(location);
			if (boundary === void 0) return void 0;
			const blocks = compactBlocks(state.blocks);
			if (!hasInterruptionEvidence(blocks)) return void 0;
			return {
				kind: "assistant",
				seq: boundary.seq + CHAT_SYNTHETIC_SEQ_OFFSETS.interruptedAssistant,
				time: boundary.time,
				turn: state.turn,
				step: state.step,
				blocks,
				interrupted: true
			};
		}
		function fallbackState$5(context) {
			let state;
			for (const match of context.matches) {
				if (match.event.type === "assistant/live-chunk") {
					state ??= initialState(match.event.data.turn, match.event.data.step);
					state = updateChunk(state, match.event.data.chunk, match.event.seq, match.event.time);
					continue;
				}
				if (match.event.type === "assistant/message") {
					state ??= initialState(match.event.data.turn, match.event.data.step);
					state = settleMessage(state, match, match.event);
					continue;
				}
				if (match.event.type === "llm/retry" && state !== void 0) state = resetForRetry(state);
			}
			return state;
		}
		function projectAssistant(context) {
			const state = context.state ?? fallbackState$5(context);
			if (state === void 0) return void 0;
			const settled = finalNode(state, context);
			const blocks = settled?.blocks ?? compactBlocks(state.blocks);
			const visible = settled === void 0 ? state.visibleBlocks > 0 : hasVisibleContent(blocks);
			const status = settled?.interrupted === true ? "interrupted" : settled === void 0 ? "running" : "settled";
			const anchorSeq = (settled?.interrupted === true ? settled.seq : state.firstVisibleSeq ?? settled?.seq) ?? context.matches[0]?.event.seq ?? 0;
			const time = settled?.time ?? state.firstVisibleTime ?? context.matches[0]?.event.time ?? 0;
			return {
				anchorSeq,
				visible,
				settled,
				data: {
					status,
					turn: state.turn,
					step: state.step,
					blocks,
					time,
					...state.usage === void 0 ? {} : { usage: state.usage },
					...settled === void 0 ? {} : { finalNode: settled }
				}
			};
		}
		function publishedAssistantData(context) {
			const location = context.start?.location ?? context.matches.at(-1)?.location;
			return location?.kind === "step" ? location.step.data.get("assistant-step") : void 0;
		}
		/** Per-step Assistant lifecycle; materialized keys survive cleared stream content as hidden Nodes. */
		const assistantDefinition = {
			kind: "assistant-step",
			target: "chat",
			match: (event) => {
				if (event.type === "step/start") return {
					id: `${event.data.turn}:${event.data.step}`,
					role: "start"
				};
				if (event.type === "assistant/live-chunk" || event.type === "assistant/message" && event.surfaceOp === "append") return {
					id: `${event.data.turn}:${event.data.step}`,
					role: "update"
				};
				if (event.type === "llm/retry") return {
					id: `${event.data.turn}:${event.data.step}`,
					role: "update"
				};
				return null;
			},
			start: (_context, match) => {
				if (match.event.type !== "step/start") throw new Error("assistant-step start requires step/start");
				return initialState(match.event.data.turn, match.event.data.step);
			},
			update: (context, match) => {
				if (match.event.type === "assistant/live-chunk") return updateChunk(context.state, match.event.data.chunk, match.event.seq, match.event.time);
				if (match.event.type === "assistant/message") return settleMessage(context.state, match, match.event);
				if (match.event.type === "llm/retry") return resetForRetry(context.state);
				return context.state;
			},
			publication: (match) => {
				if (match.event.type === "step/start") return "none";
				if (match.event.type !== "assistant/live-chunk") return "immediate";
				const type = match.event.data.chunk.type;
				return type === "usage" || type === "finish" ? "none" : "animation-frame";
			},
			buildLocationData: (context, scope) => {
				if (scope !== "step") return null;
				const projected = projectAssistant(context);
				if (projected === void 0) return null;
				return {
					kind: "step",
					turn: projected.data.turn,
					step: projected.data.step,
					key: "assistant-step",
					value: projected.data
				};
			},
			buildViewNode: (context) => {
				const current = context.current.get("chat");
				const state = context.state ?? fallbackState$5(context);
				const data = publishedAssistantData(context);
				if (state === void 0 || data === void 0) return current == null ? null : {
					...current,
					visibility: "hidden"
				};
				const settled = data.finalNode;
				const visible = settled === void 0 ? state.visibleBlocks > 0 : hasVisibleContent(data.blocks);
				if (settled === void 0 && !visible && current == null) return null;
				return chatNode(context, "assistant-step", (settled?.interrupted === true ? settled.seq : state.firstVisibleSeq ?? settled?.seq) ?? context.matches[0]?.event.seq ?? 0, data, { visibility: settled?.interrupted === true || visible ? "visible" : "hidden" });
			}
		};
		/**
		* Register the Assistant lifecycle business contribution.
		* @param ctx - owning UI Conversation context.
		*/
		function registerAssistantConversationNode(ctx) {
			ctx.uiConversation.events.register(assistantDefinition);
		}
		//#endregion
		//#region src/client/contract/chat-nodes.ts
		/**
		* Test whether a Tool root has settled.
		* @param block - Tool root lifecycle value.
		* @returns whether the root carries its final result.
		*/
		function isSettledTool(block) {
			return "kind" in block;
		}
		/**
		* Test whether a Tool root is still running.
		* @param block - Tool root lifecycle value.
		* @returns whether the root lacks a final result.
		*/
		function isRunningTool(block) {
			return !isSettledTool(block);
		}
		//#endregion
		//#region src/client/contract/chat-visibility.ts
		/**
		* Exclude system prompts, ordinary Context, and permission commands from visible Chat rows.
		* Context containing tool changes retains its notice row.
		* @param node - projected Chat node.
		* @returns whether the node contributes a visible Chat row.
		*/
		function isVisibleChatNode(node) {
			return node.visibility === "visible" && node.kind !== "system-prompt" && (node.kind !== "context" || node.data.content.some((block) => block.type === "tool-addition" || block.type === "tool-removal")) && !(node.kind === "command" && node.data.name === "permission");
		}
		//#endregion
		//#region src/client/conversation-nodes/turn-navigation.ts
		/**
		* Preview budgets, sized to the rail card's clamps (one prompt line, up to
		* three response lines) and mirrored by the turnOutline projection so a turn
		* shows the same words before and after its events load. Anything past a
		* budget is invisible; copying whole transcripts into navigation state would
		* otherwise grow with the loaded window on every structural update.
		*/
		const PROMPT_PREVIEW_LIMIT = 50;
		const RESPONSE_PREVIEW_LIMIT = 120;
		/** Join rendered text, collapse whitespace, and cap at `limit` with a trailing ellipsis when clipped. */
		function preview(parts, limit) {
			let text = "";
			let unread = false;
			for (const part of parts) {
				if (text.length >= limit * 2) {
					unread = true;
					break;
				}
				const clipped = part.length > limit * 2;
				const chunk = clipped ? part.slice(0, limit * 2) : part;
				text += text === "" ? chunk : ` ${chunk}`;
				if (clipped) {
					unread = true;
					break;
				}
			}
			const normalized = text.replace(/\s+/g, " ").trim();
			if (normalized.length > limit - 1) return `${normalized.slice(0, limit - 1).trimEnd()}…`;
			return unread ? `${normalized}…` : normalized;
		}
		function promptText(node) {
			if (node.kind !== "user") return "";
			return preview(node.data.content.flatMap((block) => block.type === "text" ? [block.text] : []), PROMPT_PREVIEW_LIMIT);
		}
		function responseText(node) {
			if (node.kind !== "assistant-step") return "";
			return preview(node.data.blocks.flatMap((block) => block.kind === "text" ? [block.text] : []), RESPONSE_PREVIEW_LIMIT);
		}
		/**
		* Whether two items carry the same rail state, so the reader can keep its array.
		* @param left - previously published item, when the Turn had one.
		* @param right - freshly derived item, when the Turn still has one.
		* @returns whether both sides describe the same mark.
		*/
		function sameTurnNavigationItem(left, right) {
			if (left === void 0 || right === void 0) return left === right;
			return left.turn === right.turn && left.anchorKey === right.anchorKey && left.prompt === right.prompt && left.response === right.response;
		}
		/**
		* Project one loaded Turn into its rail item.
		* @param turn - Turn number the item addresses.
		* @param locations - live Location index supplying the Turn's node keys.
		* @param nodes - live Chat node store.
		* @returns the item, or undefined when the Turn has no visible loaded node.
		*/
		function turnNavigationItem(turn, locations, nodes) {
			const loaded = locations.getTurn(turn).map((key) => nodes.get(key)).filter((node) => node !== void 0 && isVisibleChatNode(node));
			const user = loaded.find((node) => node.kind === "user");
			const anchor = user ?? loaded[0];
			if (anchor === void 0) return void 0;
			const response = loaded.findLast((node) => responseText(node) !== "");
			return {
				turn,
				anchorKey: anchor.key,
				prompt: user === void 0 ? "" : promptText(user),
				response: response === void 0 ? "" : responseText(response)
			};
		}
		//#endregion
		//#region src/client/conversation-nodes/turn-process-presentation.ts
		function nodeTurn(node) {
			const location = node?.location;
			return location?.kind === "turn" || location?.kind === "step" ? location.turn.turn : void 0;
		}
		function samePresentation(left, right) {
			return left === right || left !== void 0 && right !== void 0 && left.spec === right.spec && left.turn === right.turn && left.turnStarted === right.turnStarted && left.turnClosed === right.turnClosed && left.hasExternalProcess === right.hasExternalProcess && left.hasInterleavedInput === right.hasInterleavedInput && left.compactAnswer === right.compactAnswer;
		}
		function derivePresentation(turn, locations, nodes) {
			const keys = locations.getTurn(turn);
			const control = keys.map((key) => nodes.get(key)).find((node) => node?.kind === "turn-process");
			if (control === void 0) return void 0;
			const spec = control.data;
			const location = control.location;
			if (location.kind !== "turn" && location.kind !== "step") return void 0;
			let openingHumanAnchor;
			for (const key of keys) {
				const node = nodes.get(key);
				if ((node?.kind === "user" || node?.kind === "steering" || node?.kind === "turn-trigger") && (spec.controlAnchorSeq === location.turn.start?.seq || node.anchorSeq < spec.controlAnchorSeq)) openingHumanAnchor = Math.max(openingHumanAnchor ?? node.anchorSeq, node.anchorSeq);
			}
			let hasExternalProcess = false;
			let hasInterleavedInput = false;
			let compactAnswer = true;
			for (const key of keys) {
				const node = nodes.get(key);
				if (node === void 0 || !isVisibleChatNode(node) || node.kind === "turn-process") continue;
				if ((node.kind === "user" || node.kind === "steering" || node.kind === "turn-trigger") && (openingHumanAnchor === void 0 || node.anchorSeq > openingHumanAnchor)) {
					hasInterleavedInput = true;
					if (spec.answerAnchorSeq === null || node.anchorSeq < spec.answerAnchorSeq) compactAnswer = false;
				}
				if (TURN_PROCESS_INDEPENDENT_KINDS.has(node.kind) || node.anchorSeq < spec.processStartSeq || spec.answerAnchorSeq !== null && node.anchorSeq >= spec.answerAnchorSeq) continue;
				if (node.kind !== "assistant-step" || spec.answerStep === null || node.data.step !== spec.answerStep) hasExternalProcess = true;
			}
			return {
				turn,
				spec,
				turnStarted: location.turn.start !== void 0,
				turnClosed: location.turn.status === "closed",
				hasExternalProcess,
				hasInterleavedInput,
				compactAnswer
			};
		}
		/** Mutable projection of cross-Node process layout facts by Turn. */
		var ChatTurnProcessProjector = class {
			presentations = /* @__PURE__ */ new Map();
			/**
			* Read the retained process presentation for a Node's Turn.
			* @param node - Current Chat Node.
			* @returns The Turn's process presentation, when present.
			*/
			get(node) {
				const turn = nodeTurn(node);
				return turn === void 0 ? void 0 : this.presentations.get(turn);
			}
			/**
			* Replace every projected Turn.
			* @param order - visible Chat Node order.
			* @param locations - current Chat Location index.
			* @param nodes - current Chat Node store.
			* @returns Turns whose process presentation changed.
			*/
			replace(order, locations, nodes) {
				const turns = /* @__PURE__ */ new Set();
				for (const key of order) {
					const turn = nodeTurn(nodes.get(key));
					if (turn !== void 0) turns.add(turn);
				}
				const changed = /* @__PURE__ */ new Set();
				for (const turn of new Set([...this.presentations.keys(), ...turns])) if (this.set(turn, turns.has(turn) ? derivePresentation(turn, locations, nodes) : void 0)) changed.add(turn);
				return changed;
			}
			/**
			* Recompute selected Turns after incremental Node changes.
			* @param turns - affected Turn numbers.
			* @param locations - current Chat Location index.
			* @param nodes - current Chat Node store.
			* @returns Turns whose process presentation changed.
			*/
			update(turns, locations, nodes) {
				const changed = /* @__PURE__ */ new Set();
				for (const turn of turns) if (this.set(turn, derivePresentation(turn, locations, nodes))) changed.add(turn);
				return changed;
			}
			set(turn, next) {
				if (samePresentation(this.presentations.get(turn), next)) return false;
				if (next === void 0) this.presentations.delete(turn);
				else this.presentations.set(turn, next);
				return true;
			}
		};
		//#endregion
		//#region src/client/conversation-nodes/chat-snapshot-builder.ts
		const EMPTY_KEYS = [];
		const EMPTY_TURNS = [];
		const EMPTY_ITEMS = [];
		const EMPTY_LIST = [];
		function sameReferences$1(left, right) {
			return left.length === right.length && left.every((value, index) => value === right[index]);
		}
		function cachedSource(sources, key, create) {
			let source = sources.get(key);
			if (source === void 0) {
				source = create();
				sources.set(key, source);
			}
			return source;
		}
		var MutableChatSource = class {
			read;
			label;
			listeners = /* @__PURE__ */ new Set();
			published;
			constructor(read, label) {
				this.read = read;
				this.label = label;
				this.published = read();
			}
			getSnapshot = () => this.read();
			subscribe = (listener) => {
				this.listeners.add(listener);
				return () => {
					this.listeners.delete(listener);
				};
			};
			publish() {
				const next = this.getSnapshot();
				if (this.published === next) return;
				this.published = next;
				(0, _deepseek_ai_dsh_client_store.notifySubscribers)(this.listeners, this.label);
			}
		};
		/** Membership is indexed on write; ordered arrays are materialized only for observed collections. */
		var TurnKindNodes = class {
			nodes = /* @__PURE__ */ new Map();
			current = EMPTY_LIST;
			dirty = false;
			observable;
			source() {
				return this.observable ??= new MutableChatSource(() => this.read(), "[ui-chat] turn kind nodes");
			}
			set(node) {
				const previous = this.nodes.get(node.key);
				this.nodes.set(node.key, node);
				if (previous !== void 0 && previous.data === node.data && previous.anchorSeq === node.anchorSeq) return;
				this.dirty = true;
			}
			delete(key) {
				this.nodes.delete(key);
				this.dirty = true;
			}
			publish() {
				this.observable?.publish();
			}
			read() {
				if (this.dirty) {
					this.current = [...this.nodes.values()].sort((a, b) => a.anchorSeq - b.anchorSeq).map((node) => node.data);
					this.dirty = false;
				}
				return this.current;
			}
		};
		var MutableChatNodeStore = class {
			byKey = /* @__PURE__ */ new Map();
			turnProcesses = new ChatTurnProcessProjector();
			sources = /* @__PURE__ */ new Map();
			processSources = /* @__PURE__ */ new Map();
			dirtyKeys = /* @__PURE__ */ new Set();
			dirtyProcessKeys = /* @__PURE__ */ new Set();
			turnKinds = /* @__PURE__ */ new Map();
			dirtyTurnKinds = /* @__PURE__ */ new Set();
			valuesCache = EMPTY_LIST;
			valuesDirty = false;
			get(key) {
				return this.byKey.get(key);
			}
			source(key) {
				return cachedSource(this.sources, key, () => new MutableChatSource(() => this.get(key), `[ui-chat] node source ${key}`));
			}
			turnDataSource(turn, kind) {
				return this.turnKind(turn, kind).source();
			}
			turnKind(turn, kind) {
				return cachedSource(cachedSource(this.turnKinds, turn, () => /* @__PURE__ */ new Map()), kind, () => new TurnKindNodes());
			}
			updateTurnKind(previous, next) {
				const before = previous === void 0 ? void 0 : locationCoordinates(previous.location).turn;
				const after = next === void 0 ? void 0 : locationCoordinates(next.location).turn;
				if (previous !== void 0 && before !== void 0 && (before !== after || previous.kind !== next?.kind)) {
					const collection = this.turnKind(before, previous.kind);
					collection.delete(previous.key);
					this.dirtyTurnKinds.add(collection);
				}
				if (next !== void 0 && after !== void 0) {
					const collection = this.turnKind(after, next.kind);
					collection.set(next);
					this.dirtyTurnKinds.add(collection);
				}
			}
			processSource(key) {
				return cachedSource(this.processSources, key, () => new MutableChatSource(() => this.process(key), `[ui-chat] node process source ${key}`));
			}
			process(key) {
				return this.turnProcesses.get(this.get(key));
			}
			values() {
				if (this.valuesDirty) {
					this.valuesCache = [...this.byKey.values()];
					this.valuesDirty = false;
				}
				return this.valuesCache;
			}
			replace(nodes) {
				const previous = new Map(this.byKey);
				this.byKey.clear();
				for (const node of nodes) {
					this.byKey.set(node.key, node);
					if (previous.get(node.key) !== node) {
						this.updateTurnKind(previous.get(node.key), node);
						this.dirtyKeys.add(node.key);
						this.dirtyProcessKeys.add(node.key);
					}
					previous.delete(node.key);
				}
				for (const key of previous.keys()) {
					this.updateTurnKind(previous.get(key), void 0);
					this.dirtyKeys.add(key);
					this.dirtyProcessKeys.add(key);
				}
				this.valuesCache = [...this.byKey.values()];
				this.valuesDirty = false;
			}
			upsert(nodes) {
				let changed = false;
				for (const node of nodes) {
					if (this.byKey.get(node.key) === node) continue;
					this.updateTurnKind(this.byKey.get(node.key), node);
					this.byKey.set(node.key, node);
					this.dirtyKeys.add(node.key);
					this.dirtyProcessKeys.add(node.key);
					changed = true;
				}
				if (changed) this.valuesDirty = true;
			}
			touchProcesses(turns, locations) {
				for (const turn of turns) for (const key of locations.getTurn(turn)) this.dirtyProcessKeys.add(key);
			}
			replaceProcesses(order, locations) {
				this.touchProcesses(this.turnProcesses.replace(order, locations, this), locations);
			}
			updateProcesses(turns, locations) {
				this.touchProcesses(this.turnProcesses.update(turns, locations, this), locations);
			}
			publish() {
				const dirty = [...this.dirtyKeys];
				const dirtyProcesses = [...this.dirtyProcessKeys];
				const dirtyTurnKinds = [...this.dirtyTurnKinds];
				this.dirtyKeys.clear();
				this.dirtyProcessKeys.clear();
				this.dirtyTurnKinds.clear();
				for (const key of dirty) this.sources.get(key)?.publish();
				for (const key of dirtyProcesses) this.processSources.get(key)?.publish();
				for (const collection of dirtyTurnKinds) collection.publish();
			}
		};
		var MutableChatLocationIndex = class {
			turns = /* @__PURE__ */ new Map();
			steps = /* @__PURE__ */ new Map();
			positions = /* @__PURE__ */ new Map();
			getTurn(turn) {
				return this.turns.get(turn) ?? EMPTY_KEYS;
			}
			getStep(turn, step) {
				return this.steps.get(stepKey(turn, step)) ?? EMPTY_KEYS;
			}
			getPosition(key) {
				return this.positions.get(key);
			}
			rebuild(order, store) {
				const turns = /* @__PURE__ */ new Map();
				const steps = /* @__PURE__ */ new Map();
				const positions = /* @__PURE__ */ new Map();
				const changedTurns = /* @__PURE__ */ new Set();
				for (const [index, key] of order.entries()) {
					const location = store.get(key)?.location;
					if (location === void 0) continue;
					const coordinates = locationCoordinates(location);
					const previous = this.positions.get(key);
					const position = {
						turn: coordinates.turn,
						previous: order[index - 1],
						next: order[index + 1]
					};
					const unchanged = previous !== void 0 && previous.turn === position.turn && previous.previous === position.previous && previous.next === position.next;
					positions.set(key, unchanged ? previous : position);
					if (!unchanged) {
						if (previous?.turn !== void 0) changedTurns.add(previous.turn);
						if (position.turn !== void 0) changedTurns.add(position.turn);
					}
					if (coordinates.turn === void 0) continue;
					const turnKeys = turns.get(coordinates.turn) ?? [];
					turnKeys.push(key);
					turns.set(coordinates.turn, turnKeys);
					if (coordinates.step === void 0) continue;
					const step = stepKey(coordinates.turn, coordinates.step);
					const stepKeys = steps.get(step) ?? [];
					stepKeys.push(key);
					steps.set(step, stepKeys);
				}
				for (const [key, position] of this.positions) if (!positions.has(key) && position.turn !== void 0) changedTurns.add(position.turn);
				this.positions = positions;
				this.turns = updateIndex(this.turns, turns);
				this.steps = updateIndex(this.steps, steps);
				return [...changedTurns];
			}
			/** Invalidate aggregate readers when member data changes without moving. */
			touch(nodes) {
				const turns = /* @__PURE__ */ new Set();
				const steps = /* @__PURE__ */ new Set();
				for (const node of nodes) {
					const coordinates = locationCoordinates(node.location);
					if (coordinates.turn === void 0 || !this.turns.get(coordinates.turn)?.includes(node.key)) continue;
					turns.add(coordinates.turn);
					if (coordinates.step !== void 0) steps.add(stepKey(coordinates.turn, coordinates.step));
				}
				for (const turn of turns) {
					const keys = this.turns.get(turn);
					if (keys === void 0) continue;
					this.turns.set(turn, [...keys]);
				}
				for (const step of steps) {
					const keys = this.steps.get(step);
					if (keys === void 0) continue;
					this.steps.set(step, [...keys]);
				}
			}
		};
		function updateIndex(previous, nextMutable) {
			const next = /* @__PURE__ */ new Map();
			const keys = new Set([...previous.keys(), ...nextMutable.keys()]);
			for (const key of keys) {
				const before = previous.get(key) ?? EMPTY_KEYS;
				const candidate = nextMutable.get(key) ?? EMPTY_KEYS;
				const value = sameReferences$1(before, candidate) ? before : candidate;
				if (candidate.length > 0) next.set(key, value);
			}
			return next;
		}
		/**
		* Loaded-Turn rail projection accumulated alongside the node store: a
		* structural change re-derives the Turn set, a content-only upsert re-derives
		* only the Turns whose nodes moved, and the published array keeps its identity
		* until an item actually changes. Renderers therefore consume final Turn data
		* instead of scanning the loaded window per frame.
		*/
		var MutableTurnNavigationIndex = class {
			current = EMPTY_ITEMS;
			byTurn = /* @__PURE__ */ new Map();
			items() {
				return this.current;
			}
			/** Re-derive the whole Turn set; runs only when the loaded structure moves. */
			rebuild(timeline, locations, nodes) {
				const next = [];
				const byTurn = /* @__PURE__ */ new Map();
				for (const turn of timeline.turnOrder) {
					const derived = turnNavigationItem(turn, locations, nodes);
					if (derived === void 0) continue;
					const previous = this.byTurn.get(turn);
					const item = previous !== void 0 && sameTurnNavigationItem(previous, derived) ? previous : derived;
					next.push(item);
					byTurn.set(turn, item);
				}
				this.byTurn = byTurn;
				if (!(next.length === this.current.length && next.every((item, index) => item === this.current[index]))) this.current = next;
			}
			/** Re-derive only the Turns a content-only upsert touched. */
			touch(turns, locations, nodes) {
				if (turns.size === 0) return;
				const next = this.current.map((item) => {
					if (!turns.has(item.turn)) return item;
					const derived = turnNavigationItem(item.turn, locations, nodes);
					if (derived === void 0 || sameTurnNavigationItem(item, derived)) return item;
					this.byTurn.set(item.turn, derived);
					return derived;
				});
				if (next.some((item, index) => item !== this.current[index])) this.current = next;
			}
		};
		function stepKey(turn, step) {
			return `${turn}:${step}`;
		}
		function locationCoordinates(location) {
			if (location.kind === "step") return {
				turn: location.turn.turn,
				step: location.step.step
			};
			if (location.kind === "turn") return { turn: location.turn.turn };
			return {};
		}
		function locationTurnStatus(location) {
			return location.kind === "turn" || location.kind === "step" ? location.turn.status : void 0;
		}
		function processPresentationInputChanged(previous, next, structural) {
			if (structural || previous === void 0) return true;
			if (locationTurnStatus(previous.location) !== locationTurnStatus(next.location)) return true;
			if (previous.kind === "turn-process" && next.kind === "turn-process") return previous.data !== next.data;
			return previous.kind === "assistant-step" && next.kind === "assistant-step" && previous.data.step !== next.data.step;
		}
		function turnProcessPresentations(nodes) {
			const presentations = /* @__PURE__ */ new Map();
			for (const raw of nodes) {
				const node = raw;
				if (node.kind === "turn-process") presentations.set(node.data.turn, {
					...presentations.get(node.data.turn),
					control: node
				});
			}
			for (const raw of nodes) {
				const node = raw;
				const location = node.location;
				if (location.kind !== "turn" && location.kind !== "step") continue;
				const current = presentations.get(location.turn.turn) ?? {};
				const controlAnchor = current.control?.data.controlAnchorSeq;
				if ((node.kind === "user" || node.kind === "turn-trigger" || node.kind === "steering") && controlAnchor !== void 0 && (controlAnchor === location.turn.start?.seq || node.anchorSeq < controlAnchor)) {
					presentations.set(location.turn.turn, {
						...current,
						openingInputAnchor: Math.max(current.openingInputAnchor ?? node.anchorSeq, node.anchorSeq)
					});
					continue;
				}
				if (TURN_PROCESS_INDEPENDENT_KINDS.has(node.kind)) continue;
				presentations.set(location.turn.turn, {
					...current,
					earliestProcessAnchor: Math.min(current.earliestProcessAnchor ?? node.anchorSeq, node.anchorSeq)
				});
			}
			return presentations;
		}
		function presentationPosition(raw, presentations) {
			const node = raw;
			const location = node.location;
			if (location.kind !== "turn" && location.kind !== "step") return {
				anchor: node.anchorSeq,
				rank: 0,
				originalAnchor: node.anchorSeq
			};
			const presentation = presentations.get(location.turn.turn);
			if (presentation === void 0) return {
				anchor: node.anchorSeq,
				rank: 0,
				originalAnchor: node.anchorSeq
			};
			const openingInputAnchor = presentation.openingInputAnchor;
			if (openingInputAnchor !== void 0 && node.anchorSeq < openingInputAnchor && !TURN_PROCESS_INDEPENDENT_KINDS.has(node.kind)) return {
				anchor: openingInputAnchor,
				rank: 2,
				originalAnchor: node.anchorSeq
			};
			if (presentation.control !== void 0 && node.key === presentation.control.key) return openingInputAnchor === void 0 ? {
				anchor: presentation.earliestProcessAnchor ?? node.anchorSeq,
				rank: -1,
				originalAnchor: node.anchorSeq
			} : {
				anchor: openingInputAnchor,
				rank: 1,
				originalAnchor: node.anchorSeq
			};
			return {
				anchor: node.anchorSeq,
				rank: 0,
				originalAnchor: node.anchorSeq
			};
		}
		/**
		* Order visible Chat Nodes without changing existing relative order as process
		* eligibility changes. Opening input precedes process candidates, while
		* each synthetic process control sits between them.
		* @param nodes - currently materialized Chat Nodes.
		* @returns visible Nodes in presentation order.
		*/
		function orderedVisibleChatNodes(nodes) {
			const visible = nodes.filter((node) => isVisibleChatNode(node));
			const presentations = turnProcessPresentations(visible);
			return visible.sort((left, right) => {
				const leftPosition = presentationPosition(left, presentations);
				const rightPosition = presentationPosition(right, presentations);
				return leftPosition.anchor - rightPosition.anchor || leftPosition.rank - rightPosition.rank || leftPosition.originalAnchor - rightPosition.originalAnchor || left.key.localeCompare(right.key);
			});
		}
		function referenceMessageSeq(node) {
			const candidate = node;
			return candidate.kind === "user" || candidate.kind === "steering" ? candidate.data.seq : void 0;
		}
		function followingRecall(node) {
			const candidate = node;
			if (candidate.kind !== "context") return void 0;
			return {
				messageSeq: candidate.data.seq - 1,
				labels: sessionRecallLabels(candidate.data.source)
			};
		}
		function withReferenceLabels(node, labels) {
			const candidate = node;
			if (candidate.kind !== "user" && candidate.kind !== "steering") return node;
			const current = candidate.data.referenceLabels ?? EMPTY_KEYS;
			const hasLabels = Object.hasOwn(candidate.data, "referenceLabels");
			if (sameReferences$1(current, labels) && hasLabels === labels.length > 0) return node;
			const data = { ...candidate.data };
			if (labels.length === 0) delete data.referenceLabels;
			else data.referenceLabels = labels;
			return {
				...candidate,
				data
			};
		}
		/** Associates a direct message with the sourced recall event that immediately follows it. */
		var ReferenceLabelProjector = class {
			messagesBySeq = /* @__PURE__ */ new Map();
			labelsByMessageSeq = /* @__PURE__ */ new Map();
			replace(nodes) {
				this.messagesBySeq.clear();
				this.labelsByMessageSeq.clear();
				for (const node of nodes) {
					const messageSeq = referenceMessageSeq(node);
					if (messageSeq !== void 0) this.messagesBySeq.set(messageSeq, node.key);
					const recall = followingRecall(node);
					if (recall !== void 0 && recall.labels.length > 0) this.labelsByMessageSeq.set(recall.messageSeq, recall.labels);
				}
				return nodes.map((node) => {
					const messageSeq = referenceMessageSeq(node);
					return messageSeq === void 0 ? node : withReferenceLabels(node, this.labelsByMessageSeq.get(messageSeq) ?? EMPTY_KEYS);
				});
			}
			apply(upserts, store) {
				const byKey = new Map(upserts.map((node) => [node.key, node]));
				const affected = /* @__PURE__ */ new Set();
				for (const node of upserts) {
					const messageSeq = referenceMessageSeq(node);
					if (messageSeq !== void 0) {
						this.messagesBySeq.set(messageSeq, node.key);
						affected.add(messageSeq);
					}
					const recall = followingRecall(node);
					if (recall === void 0) continue;
					const current = this.labelsByMessageSeq.get(recall.messageSeq);
					if (recall.labels.length === 0) this.labelsByMessageSeq.delete(recall.messageSeq);
					else this.labelsByMessageSeq.set(recall.messageSeq, current !== void 0 && sameReferences$1(current, recall.labels) ? current : recall.labels);
					affected.add(recall.messageSeq);
				}
				for (const messageSeq of affected) {
					const key = this.messagesBySeq.get(messageSeq);
					if (key === void 0) continue;
					const node = byKey.get(key) ?? store.get(key);
					if (node === void 0) continue;
					byKey.set(key, withReferenceLabels(node, this.labelsByMessageSeq.get(messageSeq) ?? EMPTY_KEYS));
				}
				return [...byKey.values()];
			}
		};
		function withSkillNames(node, names) {
			const candidate = node;
			if (candidate.kind !== "user" && candidate.kind !== "steering") return node;
			const current = candidate.data.skillNames ?? EMPTY_KEYS;
			const hasNames = Object.hasOwn(candidate.data, "skillNames");
			if (sameReferences$1(current, names) && hasNames === names.length > 0) return node;
			const data = { ...candidate.data };
			if (names.length === 0) delete data.skillNames;
			else data.skillNames = names;
			return {
				...candidate,
				data
			};
		}
		/**
		* Classify one Node for batching: a direct message, a `skill-invocation`
		* context, or a boundary of any other kind. A context that injects no skill
		* (workspace rules, the catalog, a recall) is transparent and yields null.
		*/
		function slashEntryOf(node) {
			const candidate = node;
			if (candidate.kind === "user" || candidate.kind === "steering") return {
				key: node.key,
				seq: node.anchorSeq,
				kind: "message",
				name: null
			};
			if (candidate.kind === "context") {
				const name = skillInvocationName(candidate.data.source);
				return name === null ? null : {
					key: node.key,
					seq: node.anchorSeq,
					kind: "skill",
					name
				};
			}
			return {
				key: node.key,
				seq: node.anchorSeq,
				kind: "boundary",
				name: null
			};
		}
		function sameSlashEntry(left, right) {
			return left.seq === right.seq && left.kind === right.kind && left.name === right.name;
		}
		/**
		* Attaches each direct message's step-loaded skill names to its Node.
		*
		* A step's `skill-invocation` injections follow the direct messages the host
		* scanned for `/name` gestures and precede the step's first Node of any other
		* kind, so every non-message, non-context Node closes a batch. Every ended
		* Turn publishes its `turn-tail` Node on `turn/end` whatever the reason, so a
		* batch never spans Turns, and `step/start` precedes the direct message in
		* the log, so no boundary separates a message from its injections. Names
		* attach to every direct message of the batch: the bubble decorates only the
		* tokens its own text carries.
		*
		* The index holds only messages, skill injections, and boundaries, ordered by
		* `anchorSeq`. An apply re-reads just the batches around the Nodes whose
		* classification changed and never scans the store, so an assistant
		* streaming frame costs nothing here (the append hot path never scans the
		* Chat Nodes).
		*/
		var SkillNameProjector = class {
			entries = /* @__PURE__ */ new Map();
			/** Every indexed entry in `anchorSeq` order. */
			sorted = [];
			/**
			* Rebuild the index from a whole Node set and attach names to its messages.
			* @param nodes - every materialized Chat Node, in any order.
			* @returns the same Nodes, direct messages carrying their batch's names.
			*/
			replace(nodes) {
				this.entries.clear();
				this.sorted = [];
				for (const node of nodes) {
					const entry = slashEntryOf(node);
					if (entry === null) continue;
					this.entries.set(entry.key, entry);
					this.sorted.push(entry);
				}
				this.sorted.sort((left, right) => left.seq - right.seq);
				const names = /* @__PURE__ */ new Map();
				for (let index = 0; index < this.sorted.length; index++) {
					if (this.sorted[index]?.kind === "boundary") continue;
					const end = this.runEnd(index);
					this.assignRun(index, end, names);
					index = end;
				}
				return nodes.map((node) => withSkillNames(node, names.get(node.key) ?? EMPTY_KEYS));
			}
			/**
			* Fold one incremental upsert set: re-read only the batches around the
			* Nodes whose classification changed.
			* @param upserts - the changed Nodes.
			* @param store - the resident Nodes, read by key for the messages of an affected batch.
			* @returns the upserts plus any resident message whose names changed.
			*/
			apply(upserts, store) {
				const dirty = [];
				for (const node of upserts) {
					const next = slashEntryOf(node);
					const previous = this.entries.get(node.key);
					if (previous !== void 0) {
						if (next !== null && sameSlashEntry(previous, next)) {
							if (next.kind === "message") dirty.push(next.seq);
							continue;
						}
						this.remove(previous);
						dirty.push(previous.seq);
					}
					if (next === null) continue;
					this.insert(next);
					dirty.push(next.seq);
				}
				if (dirty.length === 0) return upserts;
				const names = /* @__PURE__ */ new Map();
				for (const seq of dirty) this.collectAround(seq, names);
				const byKey = new Map(upserts.map((node) => [node.key, node]));
				for (const [key, list] of names) {
					const node = byKey.get(key) ?? store.get(key);
					if (node === void 0) continue;
					const next = withSkillNames(node, list);
					if (next !== node || byKey.has(key)) byKey.set(key, next);
				}
				return [...byKey.values()];
			}
			insert(entry) {
				this.sorted.splice(this.lowerBound(entry.seq), 0, entry);
				this.entries.set(entry.key, entry);
			}
			remove(entry) {
				this.sorted.splice(this.sorted.indexOf(entry), 1);
				this.entries.delete(entry.key);
			}
			/** First index whose seq is at least `seq`. */
			lowerBound(seq) {
				let low = 0;
				let high = this.sorted.length;
				while (low < high) {
					const middle = low + high >>> 1;
					if ((this.sorted[middle]?.seq ?? Number.POSITIVE_INFINITY) < seq) low = middle + 1;
					else high = middle;
				}
				return low;
			}
			/** Last index of the boundary-free run containing `index`. */
			runEnd(index) {
				let end = index;
				while (end + 1 < this.sorted.length && this.sorted[end + 1]?.kind !== "boundary") end++;
				return end;
			}
			/** First index of the boundary-free run containing `index`. */
			runStart(index) {
				let start = index;
				while (start - 1 >= 0 && this.sorted[start - 1]?.kind !== "boundary") start--;
				return start;
			}
			/** Record the names every message of the run `[start, end]` carries. */
			assignRun(start, end, names) {
				const list = [];
				for (let index = start; index <= end; index++) {
					const entry = this.sorted[index];
					if (entry?.kind === "skill" && entry.name !== null && !list.includes(entry.name)) list.push(entry.name);
				}
				for (let index = start; index <= end; index++) {
					const entry = this.sorted[index];
					if (entry?.kind === "message") names.set(entry.key, list);
				}
			}
			/**
			* Re-read the run(s) around one changed seq: the run holding a message or
			* skill entry, or — for a boundary, or a seq that left the index — the runs
			* on both sides of that position.
			*/
			collectAround(seq, names) {
				const at = this.lowerBound(seq);
				const here = this.sorted[at];
				if (here !== void 0 && here.seq === seq && here.kind !== "boundary") {
					this.assignRun(this.runStart(at), this.runEnd(at), names);
					return;
				}
				if (at - 1 >= 0 && this.sorted[at - 1]?.kind !== "boundary") this.assignRun(this.runStart(at - 1), at - 1, names);
				const right = here !== void 0 && here.seq === seq ? at + 1 : at;
				if (right < this.sorted.length && this.sorted[right]?.kind !== "boundary") this.assignRun(right, this.runEnd(right), names);
			}
		};
		const EMPTY_CONTRIBUTION = {
			anchorSeq: 0,
			nodes: EMPTY_LIST,
			partial: null,
			running: null
		};
		function legacyContribution(raw) {
			const node = raw;
			if (raw.visibility !== "visible" && node.kind !== "assistant-step") return EMPTY_CONTRIBUTION;
			switch (node.kind) {
				case "user":
				case "steering":
				case "context":
				case "command":
				case "compaction":
				case "turn-error":
				case "turn-max-tokens":
				case "unknown": return {
					anchorSeq: node.anchorSeq,
					nodes: [node.data],
					partial: null,
					running: null
				};
				case "assistant-step": {
					const data = node.data;
					if (data.status === "running") {
						if (raw.visibility !== "visible") return EMPTY_CONTRIBUTION;
						return {
							anchorSeq: node.anchorSeq,
							nodes: EMPTY_LIST,
							partial: {
								turn: data.turn,
								step: data.step,
								blocks: data.blocks
							},
							running: null
						};
					}
					return {
						anchorSeq: node.anchorSeq,
						nodes: data.finalNode === void 0 ? EMPTY_LIST : [data.finalNode],
						partial: null,
						running: null
					};
				}
				case "tool-call": {
					const root = node.data.root;
					return isRunningTool(root) ? {
						anchorSeq: node.anchorSeq,
						nodes: EMPTY_LIST,
						partial: null,
						running: root
					} : {
						anchorSeq: node.anchorSeq,
						nodes: [root],
						partial: null,
						running: null
					};
				}
				case "manual-compaction": {
					const data = node.data;
					return {
						anchorSeq: node.anchorSeq,
						nodes: data.compaction === null ? [data.command] : [data.command, data.compaction],
						partial: null,
						running: null
					};
				}
				case "model-retry": return {
					anchorSeq: node.anchorSeq,
					nodes: node.data.attempts,
					partial: null,
					running: null
				};
				case "turn-tail":
				case "system-prompt": return EMPTY_CONTRIBUTION;
				default: return EMPTY_CONTRIBUTION;
			}
		}
		function sameContribution(left, right) {
			return left !== void 0 && left.anchorSeq === right.anchorSeq && left.partial?.blocks === right.partial?.blocks && left.partial?.turn === right.partial?.turn && left.partial?.step === right.partial?.step && left.running === right.running && sameReferences$1(left.nodes, right.nodes);
		}
		/** Incremental compatibility projection for StatsPills and legacy top-level snapshot fields. */
		var LegacySliceBuilder = class {
			contributions = /* @__PURE__ */ new Map();
			finalizedContributions = /* @__PURE__ */ new Map();
			runningContributions = /* @__PURE__ */ new Map();
			partialContributions = /* @__PURE__ */ new Map();
			finalized = EMPTY_LIST;
			runningCalls = EMPTY_LIST;
			partial = null;
			timeline;
			turnTimings = /* @__PURE__ */ new Map();
			turnEnds = /* @__PURE__ */ new Map();
			replace(nodes, timeline) {
				this.contributions.clear();
				this.finalizedContributions.clear();
				this.runningContributions.clear();
				this.partialContributions.clear();
				for (const node of nodes) {
					const contribution = legacyContribution(node);
					this.contributions.set(node.key, contribution);
					this.indexContribution(node.key, contribution);
				}
				this.rebuildFinalized();
				this.rebuildRunning();
				this.rebuildPartial();
				this.updateTimeline(timeline);
				return this.snapshot();
			}
			apply(upserts, timeline) {
				let finalizedChanged = false;
				let runningChanged = false;
				let partialChanged = false;
				for (const node of upserts) {
					const contribution = legacyContribution(node);
					const previous = this.contributions.get(node.key);
					if (sameContribution(previous, contribution)) continue;
					finalizedChanged ||= finalizedContributionChanged(previous, contribution);
					runningChanged ||= runningContributionChanged(previous, contribution);
					partialChanged ||= partialContributionChanged(previous, contribution);
					this.contributions.set(node.key, contribution);
					this.indexContribution(node.key, contribution);
				}
				if (finalizedChanged) this.rebuildFinalized();
				if (runningChanged) this.rebuildRunning();
				if (partialChanged) this.rebuildPartial();
				this.updateTimeline(timeline);
				return this.snapshot();
			}
			indexContribution(key, contribution) {
				updateContributionIndex(this.finalizedContributions, key, contribution, contribution.nodes.length > 0);
				updateContributionIndex(this.runningContributions, key, contribution, contribution.running !== null);
				updateContributionIndex(this.partialContributions, key, contribution, contribution.partial !== null);
			}
			rebuildFinalized() {
				const finalized = [...this.finalizedContributions.values()].flatMap((value) => value.nodes).sort((left, right) => left.seq - right.seq);
				if (!sameReferences$1(this.finalized, finalized)) this.finalized = finalized;
			}
			rebuildRunning() {
				const runningCalls = [...this.runningContributions.values()].sort((left, right) => left.anchorSeq - right.anchorSeq).flatMap((value) => value.running === null ? [] : [value.running]);
				if (!sameReferences$1(this.runningCalls, runningCalls)) this.runningCalls = runningCalls;
			}
			rebuildPartial() {
				const partial = [...this.partialContributions.values()].sort((left, right) => left.anchorSeq - right.anchorSeq).findLast((value) => value.partial !== null)?.partial ?? null;
				if (this.partial?.blocks !== partial?.blocks || this.partial?.turn !== partial?.turn || this.partial?.step !== partial?.step) this.partial = partial;
			}
			updateTimeline(timeline) {
				if (this.timeline === timeline) return;
				this.timeline = timeline;
				const turnTimings = /* @__PURE__ */ new Map();
				const turnEnds = /* @__PURE__ */ new Map();
				for (const turn of timeline.turns.values()) {
					if (turn.start !== void 0) turnTimings.set(turn.turn, {
						startTime: turn.start.time,
						...turn.end === void 0 ? {} : { endTime: turn.end.time }
					});
					if (turn.end !== void 0) turnEnds.set(turn.turn, turn.end.seq);
				}
				this.turnTimings = turnTimings;
				this.turnEnds = turnEnds;
			}
			snapshot() {
				return {
					nodes: this.finalized,
					turnTimings: this.turnTimings,
					turnEnds: this.turnEnds,
					partial: this.partial,
					runningCalls: this.runningCalls
				};
			}
		};
		function updateContributionIndex(index, key, contribution, present) {
			if (present) index.set(key, contribution);
			else index.delete(key);
		}
		function finalizedContributionChanged(previous, next) {
			const previousNodes = previous?.nodes ?? EMPTY_LIST;
			return !sameReferences$1(previousNodes, next.nodes) || (previousNodes.length > 0 || next.nodes.length > 0) && previous?.anchorSeq !== next.anchorSeq;
		}
		function runningContributionChanged(previous, next) {
			return previous?.running !== next.running || (previous.running !== null || next.running !== null) && previous.anchorSeq !== next.anchorSeq;
		}
		function partialContributionChanged(previous, next) {
			return previous?.partial?.blocks !== next.partial?.blocks || previous?.partial?.turn !== next.partial?.turn || previous?.partial?.step !== next.partial?.step || ((previous?.partial ?? null) !== null || next.partial !== null) && previous?.anchorSeq !== next.anchorSeq;
		}
		/** Incremental keyed Chat builder registered under the `chat` target. */
		var ChatSnapshotBuilder = class {
			store = new MutableChatNodeStore();
			locations = new MutableChatLocationIndex();
			navigation = new MutableTurnNavigationIndex();
			legacy = new LegacySliceBuilder();
			referenceLabels = new ReferenceLabelProjector();
			skillNames = new SkillNameProjector();
			order = EMPTY_KEYS;
			latestGroupInput;
			readGroupNode = (key) => this.store.get(key);
			readGroupTurn = (turn) => this.locations.getTurn(turn);
			readGroupPosition = (key) => this.locations.getPosition(key);
			/** Last published timeline: a Turn boundary can land without a new node. */
			timeline = null;
			empty;
			constructor() {
				this.empty = this.snapshot({
					turnOrder: EMPTY_TURNS,
					turns: /* @__PURE__ */ new Map()
				});
				this.latestGroupInput = {
					kind: "replace",
					order: this.order,
					readNode: this.readGroupNode,
					readTurn: this.readGroupTurn,
					readPosition: this.readGroupPosition,
					timeline: this.empty.timeline
				};
			}
			replace(input) {
				const nodes = this.skillNames.replace(this.referenceLabels.replace(input.nodes));
				this.store.replace(nodes);
				this.order = orderedVisibleChatNodes(nodes).map((node) => node.key);
				this.locations.rebuild(this.order, this.store);
				this.store.replaceProcesses(this.order, this.locations);
				this.navigation.rebuild(input.timeline, this.locations, this.store);
				this.timeline = input.timeline;
				this.latestGroupInput = {
					kind: "replace",
					order: this.order,
					readNode: this.readGroupNode,
					readTurn: this.readGroupTurn,
					readPosition: this.readGroupPosition,
					timeline: input.timeline
				};
				return this.snapshot(input.timeline, this.legacy.replace(nodes, input.timeline));
			}
			apply(input) {
				const upserts = this.skillNames.apply(this.referenceLabels.apply(input.upserts, this.store), this.store);
				const processTurns = /* @__PURE__ */ new Set();
				let structural = false;
				const contentOnly = [];
				const changes = [];
				for (const node of upserts) {
					const previous = this.store.get(node.key);
					if (previous !== node) changes.push({
						previous,
						current: node
					});
					const nodeStructural = previous === void 0 || previous.kind !== node.kind || previous.anchorSeq !== node.anchorSeq || previous.visibility !== node.visibility || locationIdentity(previous.location) !== locationIdentity(node.location);
					structural ||= nodeStructural;
					if (!nodeStructural) contentOnly.push(node);
					if (processPresentationInputChanged(previous, node, nodeStructural)) {
						const previousTurn = previous === void 0 ? void 0 : locationCoordinates(previous.location).turn;
						const nextTurn = locationCoordinates(node.location).turn;
						if (previousTurn !== void 0) processTurns.add(previousTurn);
						if (nextTurn !== void 0) processTurns.add(nextTurn);
					}
				}
				this.store.upsert(upserts);
				let changedTurnOrders = EMPTY_TURNS;
				if (structural) {
					const next = orderedVisibleChatNodes(this.store.values()).map((node) => node.key);
					this.order = sameReferences$1(this.order, next) ? this.order : next;
					changedTurnOrders = this.locations.rebuild(this.order, this.store);
				}
				this.locations.touch(contentOnly);
				this.store.updateProcesses(processTurns, this.locations);
				if (structural || input.timeline !== this.timeline) this.navigation.rebuild(input.timeline, this.locations, this.store);
				else this.navigation.touch(turnsOf(contentOnly), this.locations, this.store);
				this.timeline = input.timeline;
				this.latestGroupInput = {
					kind: "apply",
					changes,
					order: this.order,
					readNode: this.readGroupNode,
					readTurn: this.readGroupTurn,
					readPosition: this.readGroupPosition,
					timeline: input.timeline,
					changedTurns: input.changedTurns ?? EMPTY_TURNS,
					changedTurnOrders
				};
				return this.snapshot(input.timeline, this.legacy.apply(upserts, input.timeline));
			}
			groupInput() {
				return this.latestGroupInput;
			}
			publish() {
				this.store.publish();
			}
			snapshot(timeline, legacy = this.legacy.replace(EMPTY_LIST, timeline)) {
				return {
					order: this.order,
					nodes: this.store,
					locations: this.locations,
					navigation: this.navigation,
					timeline,
					legacy
				};
			}
		};
		/** Turns owning the given nodes, for the content-only navigation update. */
		function turnsOf(nodes) {
			const turns = /* @__PURE__ */ new Set();
			for (const node of nodes) {
				const turn = locationCoordinates(node.location).turn;
				if (turn !== void 0) turns.add(turn);
			}
			return turns;
		}
		function locationIdentity(location) {
			const coordinates = locationCoordinates(location);
			return `${location.kind}:${coordinates.turn ?? ""}:${coordinates.step ?? ""}`;
		}
		/** Chat target factory contributed to the Conversation view registry. */
		const chatViewDefinition = {
			target: "chat",
			create: () => new ChatSnapshotBuilder(),
			isActive: (snapshot) => snapshot.order.some((key) => snapshot.nodes.get(key)?.kind !== "command")
		};
		/**
		* Register the incremental Chat target builder.
		* @param ctx - owning UI Conversation context.
		*/
		function registerChatConversationView(ctx) {
			ctx.uiConversation.views.register(chatViewDefinition);
		}
		//#endregion
		//#region ../../core/session/src/surface.ts
		/** Runtime counterpart of the message-producing event union. */
		const SURFACE_EVENT_TYPES = new Set([
			"system/message",
			"developer/message",
			"user/message",
			"assistant/message",
			"tool/result"
		]);
		/**
		* Narrow an event to a surface-eligible event carrying its required marker.
		* @param event - event to test.
		* @returns true when both the type and marker identify a surface event.
		*/
		function isSurfaceEvent(event) {
			if (!SURFACE_EVENT_TYPES.has(event.type)) return false;
			return event.surfaceOp !== void 0;
		}
		/**
		* Narrow an event to an append-origin surface event: one that entered the
		* surface at its own log position and was never itself a replacement copy.
		*
		* The model-visible surface deliberately shadows replaced ranges, so it is the
		* wrong source for a human transcript — a landed replacement would erase
		* conversation the user already saw. Append-origin events are that transcript's
		* durable source material; replacement copies stay model-only.
		* @param event - event to test.
		* @returns true when the event appended to the surface tail.
		*/
		function isAppendSurfaceEvent(event) {
			return isSurfaceEvent(event) && event.surfaceOp === "append";
		}
		/**
		* Narrow an event to a surface replacement: a node that shadowed an existing
		* surface range instead of appending to the tail. The counterpart of
		* {@link isAppendSurfaceEvent} over the two {@link SurfaceOp} variants.
		* @param event - event to test.
		* @returns true when the event replaced a surface range.
		*/
		function isReplacementSurfaceEvent(event) {
			return isSurfaceEvent(event) && event.surfaceOp !== "append";
		}
		//#endregion
		//#region src/client/conversation-nodes/command.ts
		const COMPACT_KIND = "compact-checkpoint";
		function commandFromRun(match) {
			if (match.event.type !== "command/run") throw new Error("command start requires command/run");
			const data = match.event.data;
			return {
				kind: "command",
				seq: match.event.seq,
				time: match.event.time,
				commandId: data.commandId,
				name: data.name,
				args: data.args ?? null,
				outcome: null
			};
		}
		function commandFromDone(match, previous) {
			if (match.event.type !== "command/done") throw new Error("command update requires command/done");
			const data = match.event.data;
			const sourceEventSeq = data.kind === "success" && data.sourceEventSeq !== void 0 && Number.isSafeInteger(data.sourceEventSeq) && data.sourceEventSeq >= 0 ? data.sourceEventSeq : void 0;
			return {
				kind: "command",
				seq: previous?.seq ?? match.event.seq,
				time: previous?.time ?? match.event.time,
				commandId: data.commandId,
				name: previous?.name ?? null,
				args: previous?.args ?? null,
				outcome: {
					kind: data.kind,
					...data.text === void 0 ? {} : { text: data.text },
					...sourceEventSeq === void 0 ? {} : { sourceEventSeq }
				}
			};
		}
		/**
		* Read correlation identity from a compaction replacement checkpoint.
		* @param event - candidate Session event.
		* @returns correlated compaction and optional command identity.
		*/
		function compactSource(event) {
			if (event.type !== "user/message" || !isReplacementSurfaceEvent(event)) return void 0;
			const source = event.data.source;
			if (source.kind !== COMPACT_KIND || typeof source.compactionId !== "string") return void 0;
			return {
				compactionId: source.compactionId,
				...source.sourceCommandId === void 0 ? {} : { sourceCommandId: source.sourceCommandId }
			};
		}
		/**
		* Build the visible summary marker from optional lifecycle evidence.
		* @param match - compaction/summary Match, when loaded.
		* @param checkpoint - replacement checkpoint Match.
		* @returns final compaction summary Node data.
		*/
		function compactSummary(match, checkpoint) {
			let summary = null;
			let shadowedItemCount = null;
			let shadowedTokenCount = null;
			if (match?.event.type === "compaction/summary") {
				const data = match.event.data;
				if (Array.isArray(data.summary)) {
					const text = data.summary.map((block) => block.type === "text" ? block.text : "").join("");
					summary = text.trim() === "" ? null : text;
				}
				shadowedItemCount = Array.isArray(data.shadowedSeqs) && data.shadowedSeqs.every((seq) => Number.isSafeInteger(seq) && seq >= 0) ? data.shadowedSeqs.length : null;
				shadowedTokenCount = Number.isSafeInteger(data.shadowedTokenCount) && data.shadowedTokenCount >= 0 ? data.shadowedTokenCount : null;
			}
			return {
				kind: "compaction",
				seq: checkpoint.event.seq,
				time: checkpoint.event.time,
				summary,
				summaryEventSeq: match?.event.seq ?? null,
				shadowedItemCount,
				shadowedTokenCount
			};
		}
		function fallbackState$4(context) {
			const done = context.matches.find((match) => match.event.type === "command/done");
			const checkpoint = context.matches.find((match) => compactSource(match.event) !== void 0);
			const summary = context.matches.find((match) => match.event.type === "compaction/summary");
			if (checkpoint === void 0) return done === void 0 ? void 0 : { command: commandFromDone(done) };
			const source = compactSource(checkpoint.event);
			if (source?.sourceCommandId === void 0) return done === void 0 ? void 0 : { command: commandFromDone(done) };
			return {
				command: done === void 0 ? {
					kind: "command",
					seq: checkpoint.event.seq,
					time: checkpoint.event.time,
					commandId: source.sourceCommandId,
					name: "compact",
					args: null,
					outcome: null
				} : {
					...commandFromDone(done),
					name: "compact"
				},
				checkpoint,
				...summary === void 0 ? {} : { summary }
			};
		}
		/**
		* Fold shared compaction evidence into a Definition-owned State.
		* @param state - current business State carrying optional compaction evidence.
		* @param match - next compaction lifecycle Match.
		* @returns adopted State, preserving reference identity when the Match adds no evidence.
		*/
		function updateCompactionState(state, match) {
			if (match.event.type === "compaction/summary") return {
				...state,
				summary: match
			};
			if (compactSource(match.event) !== void 0) return {
				...state,
				checkpoint: match
			};
			return state;
		}
		/** Slash-command lifecycle, including integrated manual compaction, Definition. */
		const commandDefinition = {
			kind: "command",
			target: "chat",
			match: (event) => {
				if (event.type === "command/run") return {
					id: String(event.data.commandId),
					role: "start"
				};
				if (event.type === "command/done") return {
					id: String(event.data.commandId),
					role: "update"
				};
				const checkpoint = compactSource(event);
				if (checkpoint?.sourceCommandId !== void 0) return {
					id: String(checkpoint.sourceCommandId),
					role: "update"
				};
				if (event.type === "compaction/start" || event.type === "compaction/summary" || event.type === "compaction/end") {
					if (event.data.sourceCommandId !== void 0) return {
						id: String(event.data.sourceCommandId),
						role: "update"
					};
				}
				return null;
			},
			start: (_context, match) => ({ command: commandFromRun(match) }),
			update: (context, match) => {
				if (match.event.type === "command/done") return {
					...context.state,
					command: commandFromDone(match, context.state.command)
				};
				return updateCompactionState(context.state, match);
			},
			buildViewNode: (context) => {
				const state = context.state ?? fallbackState$4(context);
				if (state === void 0) return null;
				if (state.command.name !== "compact") return chatNode(context, "command", state.command.seq, state.command);
				const compaction = state.checkpoint === void 0 ? null : compactSummary(state.summary, state.checkpoint);
				const data = {
					command: state.command,
					compaction
				};
				return chatNode(context, "manual-compaction", compaction?.seq ?? state.command.seq, data);
			}
		};
		/**
		* Register the command lifecycle business contribution.
		* @param ctx - owning UI Conversation context.
		*/
		function registerCommandConversationNode(ctx) {
			ctx.uiConversation.events.register(commandDefinition);
		}
		//#endregion
		//#region src/client/conversation-nodes/compaction.ts
		function fallbackState$3(context) {
			const summary = context.matches.find((match) => match.event.type === "compaction/summary");
			const checkpoint = context.matches.find((match) => compactSource(match.event) !== void 0);
			return {
				...summary === void 0 ? {} : { summary },
				...checkpoint === void 0 ? {} : { checkpoint }
			};
		}
		/** Automatic compaction lifecycle and landed checkpoint Definition. */
		const compactionDefinition = {
			kind: "compaction",
			target: "chat",
			match: (event) => {
				const checkpoint = compactSource(event);
				if (checkpoint !== void 0 && checkpoint.sourceCommandId === void 0) return {
					id: checkpoint.compactionId,
					role: "update"
				};
				if (event.type === "compaction/start" || event.type === "compaction/summary" || event.type === "compaction/end") {
					if (event.data.sourceCommandId !== void 0) return null;
					const compactionId = event.data.compactionId;
					if (typeof compactionId !== "string" || compactionId === "") return null;
					return {
						id: compactionId,
						role: event.type === "compaction/start" ? "start" : "update"
					};
				}
				return null;
			},
			start: () => ({}),
			update: (context, match) => updateCompactionState(context.state, match),
			buildViewNode: (context) => {
				const state = context.state ?? fallbackState$3(context);
				if (state.checkpoint === void 0) return null;
				const marker = compactSummary(state.summary, state.checkpoint);
				return chatNode(context, "compaction", marker.seq, marker);
			}
		};
		/**
		* Register the automatic-compaction business contribution.
		* @param ctx - owning UI Conversation context.
		*/
		function registerCompactionConversationNode(ctx) {
			ctx.uiConversation.events.register(compactionDefinition);
		}
		//#endregion
		//#region src/client/conversation-nodes/fallback.ts
		/** Unclaimed append-surface fallback Definition. */
		const unknownFallbackDefinition = {
			kind: "unknown-surface",
			target: "chat",
			match: (event) => event.type !== "assistant/live-chunk" && isAppendSurfaceEvent(event) ? {
				id: String(event.seq),
				role: "start"
			} : null,
			start: (_context, match) => ({
				kind: "unknown",
				seq: match.event.seq,
				time: match.event.time,
				type: match.event.type,
				data: match.event.data
			}),
			update: (context) => context.state,
			buildViewNode: (context) => context.state === void 0 ? null : chatNode(context, "unknown", context.state.seq, context.state)
		};
		/**
		* Register the unmatched append-surface fallback contribution.
		* @param ctx - owning UI Conversation context.
		*/
		function registerUnknownConversationFallback(ctx) {
			ctx.uiConversation.events.registerFallback(unknownFallbackDefinition);
		}
		//#endregion
		//#region src/client/conversation-nodes/inbox.ts
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
			for (const { id } of inserted) {
				if (!claimed.has(id)) continue;
				next ??= new Set(claimed);
				next.delete(id);
			}
			return next ?? claimed;
		}
		/**
		* Apply one Inbox splice under the AgentLoop's durable event ordering.
		* An entered claim logs its complete message batch before another claim; a
		* rejected claim logs no messages, so only the current claim can classify a
		* later `user/message`.
		*/
		function applySplice(previous, splice, seq) {
			const priorPending = previous?.state.pending ?? EMPTY_PENDING;
			const inserted = splice.inserted;
			const removedCount = splice.removedCount ?? 0;
			if (removedCount > 0 && splice.outcome !== "canceled") {
				const pending = materializePending(priorPending);
				const removed = pending.splice(splice.start, removedCount, ...inserted);
				return {
					pending: {
						kind: "snapshot",
						ids: pending
					},
					currentClaimed: new Set(removed.map((message) => message.id)),
					claimSeq: seq,
					claimedHuman: removed.some((message) => message.source.kind === "user")
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
				currentClaimed,
				claimSeq: previous?.state.claimSeq ?? -1,
				claimedHuman: previous?.state.claimedHuman ?? false
			};
		}
		function inboxDefinition(target) {
			const kind = `inbox-${target}`;
			return {
				kind,
				match: (event) => event.type === "agent/inbox/spliced" && event.data.target === target ? {
					id: String(event.seq),
					role: "start"
				} : null,
				start: (_context, match, reader) => {
					if (match.event.type !== "agent/inbox/spliced") throw new Error("inbox start requires agent/inbox/spliced");
					return applySplice(reader.previous(kind), match.event.data, match.event.seq);
				},
				update: (context) => context.state,
				publication: () => "none"
			};
		}
		/** Persistent next-step claims identify messages admitted into a running Turn. */
		const nextStepInboxDefinition = inboxDefinition("next-step");
		/** Persistent next-turn claims identify messages that wake a new Turn. */
		const nextTurnInboxDefinition = inboxDefinition("next-turn");
		/**
		* Register the Inbox state used by Chat message classification.
		* @param ctx - owning UI Conversation context.
		*/
		function registerInboxConversationNodes(ctx) {
			ctx.uiConversation.events.register(nextStepInboxDefinition);
			ctx.uiConversation.events.register(nextTurnInboxDefinition);
		}
		//#endregion
		//#region src/client/conversation-nodes/message.ts
		function isCompactionCheckpoint(event) {
			if (event.type !== "user/message" || !isReplacementSurfaceEvent(event)) return false;
			return event.data.source.kind === "compact-checkpoint";
		}
		/** Context presentation shared by user-role injections and developer messages. */
		function contextMessage(event, message) {
			return {
				kind: "context",
				seq: event.seq,
				time: event.time,
				content: message.content,
				source: message.source,
				producer: contextProducer(message.source),
				form: contextForm(message.source)
			};
		}
		/** User, steering, and injected-context message classification Definition. */
		const messageDefinition = {
			kind: "input-message",
			target: "chat",
			match: (event) => {
				if (event.type === "user/message") return isAppendSurfaceEvent(event) && !isCompactionCheckpoint(event) ? {
					id: String(event.data.id),
					role: "start"
				} : null;
				return null;
			},
			start: (_context, match, reader) => {
				const event = match.event;
				if (event.type !== "user/message") throw new Error("input-message start requires user/message");
				if (event.data.source.kind !== "user") {
					const nextTurn = reader.previous("inbox-next-turn")?.state;
					const nextStep = reader.previous("inbox-next-step")?.state;
					const location = match.location;
					const turnStart = location.kind === "step" ? location.turn.start?.seq : void 0;
					const idleSteer = location.kind === "step" && location.step.step === 1 && turnStart !== void 0 && (nextStep?.claimSeq ?? -1) > turnStart && (nextTurn?.claimSeq ?? -1) < turnStart && nextStep?.claimedHuman === false && nextStep.currentClaimed.has(String(event.data.id));
					return {
						...contextMessage(event, event.data),
						waking: nextTurn?.currentClaimed.has(String(event.data.id)) === true || idleSteer
					};
				}
				return reader.previous("inbox-next-step")?.state.currentClaimed.has(String(event.data.id)) === true ? {
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
			buildViewNode: (context) => {
				if (context.state === void 0) return null;
				return chatNode(context, context.state.kind === "context" && context.start?.event.type === "user/message" && context.state.waking === true ? "turn-trigger" : context.state.kind, context.state.seq, context.state);
			}
		};
		/** Developer history uses the input-message lifecycle and context presentation. */
		const developerMessageDefinition = {
			...messageDefinition,
			kind: "developer-message",
			match: (event) => event.type === "developer/message" ? {
				id: String(event.data.message.id),
				role: "start"
			} : null,
			start: (_context, match) => {
				const event = match.event;
				if (event.type !== "developer/message") throw new Error("developer-message start requires developer/message");
				return contextMessage(event, event.data.message);
			}
		};
		/**
		* Register user, steering, injected-context, and developer message contributions.
		* @param ctx - owning UI Conversation context.
		*/
		function registerMessageConversationNode(ctx) {
			ctx.uiConversation.events.register(messageDefinition);
			ctx.uiConversation.events.register(developerMessageDefinition);
		}
		//#endregion
		//#region src/client/conversation-nodes/request-prompt.ts
		/** Place a request's system prompt at the start of its visible message series. */
		function requestPromptAnchor(match, previous, isInitial) {
			if (match.location.kind !== "step") return match.event.seq;
			if (previous === void 0 && !isInitial) return match.event.seq;
			if (previous?.turn === match.location.turn.turn && previous.step === match.location.step.step) return match.event.seq;
			return match.location.step.step === 1 ? match.location.turn.start?.seq ?? match.location.step.start?.seq ?? match.event.seq : match.location.step.start?.seq ?? match.event.seq;
		}
		/** Keep an already rendered prompt at its page-lifetime presentation anchor. */
		function stableRequestPromptAnchor(context, match, previous, isInitial) {
			const current = context.current.get("chat");
			return current?.kind === "system-prompt" ? current.anchorSeq : requestPromptAnchor(match, previous, isInitial);
		}
		/**
		* System-prompt surface node Definition for the Chat target. It owns every
		* `system/message` event on the Chat target so the unknown-surface fallback
		* never renders the prompt as a transcript row. Each nonempty append owns a
		* prompt card, even without a loaded request header. Initial cards precede
		* their step's input; in-history updates stay at their own positions. The
		* request-prompt Definition owns replacement and later-series cards. Positional
		* replacements advance the effective prompt without changing historical cards.
		* @param inspect - Pure surface interpretation supplied by uiConversation.
		* @returns The Chat system-prompt Definition.
		*/
		function systemMessageDefinition(inspect) {
			return {
				kind: "system-message",
				target: "chat",
				match: (event) => event.type === "system/message" || "surfaceOp" in event && event.surfaceOp !== "append" ? {
					id: String(event.seq),
					role: "start"
				} : null,
				start: (_context, match, reader) => {
					if (match.event.type === "assistant/live-chunk") throw new Error("system-message requires a durable event");
					return inspect(reader.previous("system-message")?.state, match.event);
				},
				update: (context) => context.state,
				buildViewNode: (context) => {
					const state = context.state?.introduced;
					if (state === void 0 || state.text === "" || context.start?.event.type !== "system/message" || context.start.event.surfaceOp !== "append") return null;
					return chatNode(context, "system-prompt", state.update ? state.seq : requestPromptAnchor(context.start, void 0, true), {
						text: state.text,
						...state.update ? { update: true } : {}
					});
				}
			};
		}
		/**
		* Request-header prompt Definition for the Chat target. Resume and explicit
		* series starts retain a prompt card even when the system text is unchanged.
		* @param inspect - the shared prompt interpretation, supplied by the
		* uiConversation service (a client bundle cannot value-import it).
		* @returns the Chat request-prompt Definition.
		*/
		function requestPromptDefinition(inspect) {
			return {
				kind: "request-prompt",
				target: "chat",
				match: (event) => event.type === "request/header" ? {
					id: String(event.seq),
					role: "start"
				} : null,
				start: (context, match, reader) => {
					if (match.event.type !== "request/header") throw new Error("request-prompt start requires request/header");
					const previous = reader.previous("request-prompt")?.state;
					const systemContext = reader.previous("system-message");
					const system = systemContext?.state.effective;
					const location = match.location.kind === "step" ? {
						turn: match.location.turn.turn,
						step: match.location.step.step
					} : {};
					const inspection = inspect(previous?.prompt, match.event, system);
					const change = inspection.change?.kind;
					const systemEvent = systemContext?.matches[0]?.event;
					const shownByUpdate = system !== void 0 && systemEvent?.type === "system/message" && systemEvent.surfaceOp === "append" && (system.update || previous === void 0) && system.turn === location.turn && system.step === location.step;
					return {
						anchorSeq: stableRequestPromptAnchor(context, match, previous, match.event.data.reason === "initial"),
						showsPrompt: !shownByUpdate && (previous === void 0 || match.event.data.reason !== "change" || match.event.data.startsSeries === true || change === "system" || change === "system-and-tools"),
						...location,
						...inspection
					};
				},
				update: (context) => context.state,
				buildViewNode: (context) => {
					const state = context.state;
					if (state === void 0) return null;
					const current = context.current.get("chat");
					const visible = state.showsPrompt && state.prompt.system !== "";
					if (!visible && current?.kind !== "system-prompt") return null;
					return chatNode(context, "system-prompt", state.anchorSeq, { text: state.prompt.system }, { visibility: visible ? "visible" : "hidden" });
				}
			};
		}
		/**
		* Register the system-prompt surface node and the model-request prompt card in the Chat flow.
		* @param ctx - Owning UI Conversation context.
		*/
		function registerRequestPromptConversationNode(ctx) {
			ctx.uiConversation.events.register(systemMessageDefinition((previous, event) => ctx.uiConversation.inspectSystemPrompt(previous, event)));
			ctx.uiConversation.events.register(requestPromptDefinition((previous, event, system) => ctx.uiConversation.inspectRequestPrompt(previous, event, system)));
		}
		//#endregion
		//#region src/client/conversation-nodes/retry.ts
		function scheduledNode(match) {
			if (match.event.type !== "llm/retry") return void 0;
			return {
				kind: "model-retry",
				seq: match.event.seq,
				time: match.event.time,
				retryState: "scheduled",
				...match.event.data
			};
		}
		/** A scheduled attempt is cancelled once either owning boundary closes. */
		function isClosed(location) {
			return location.kind === "step" && location.step.status === "closed" || (location.kind === "step" || location.kind === "turn") && location.turn.status === "closed";
		}
		/** Producer-correlated model retry chain Definition. */
		const retryDefinition = {
			kind: "model-retry",
			target: "chat",
			match: (event) => {
				if (event.type === "llm/retry") {
					const retryId = event.data.retryId;
					if (typeof retryId !== "string" || retryId === "") return null;
					return {
						id: retryId,
						role: event.data.retry === 1 ? "start" : "update"
					};
				}
				if (event.type === "llm/retry-started") {
					const retryId = event.data.retryId;
					return typeof retryId === "string" && retryId !== "" ? {
						id: retryId,
						role: "update"
					} : null;
				}
				return null;
			},
			start: (_context, match) => {
				const node = scheduledNode(match);
				if (node === void 0) throw new Error("model-retry start requires a valid llm/retry event");
				return {
					turn: node.turn,
					step: node.step,
					attempts: [node]
				};
			},
			update: (context, match) => {
				if (match.event.type === "llm/retry") {
					const node = scheduledNode(match);
					return node === void 0 ? context.state : {
						...context.state,
						attempts: [...context.state.attempts, node]
					};
				}
				if (match.event.type !== "llm/retry-started") return context.state;
				const retry = match.event.data.retry;
				return {
					...context.state,
					attempts: context.state.attempts.map((attempt) => attempt.retry === retry ? {
						...attempt,
						retryState: "started"
					} : attempt)
				};
			},
			buildViewNode: (context) => {
				if (context.state === void 0 || context.state.attempts.length === 0) return null;
				const location = context.start?.location ?? context.matches[0]?.location ?? { kind: "unresolved" };
				const stateAttempts = context.state.attempts;
				const attempts = stateAttempts.map((attempt, index) => index === stateAttempts.length - 1 && attempt.retryState === "scheduled" && isClosed(location) ? {
					...attempt,
					retryState: "cancelled"
				} : attempt);
				const current = attempts.at(-1);
				if (current === void 0) return null;
				const data = {
					attempts,
					current
				};
				return chatNode(context, "model-retry", attempts[0]?.seq ?? current.seq, data);
			}
		};
		/**
		* Register the correlated model-retry business contribution.
		* @param ctx - owning UI Conversation context.
		*/
		function registerRetryConversationNode(ctx) {
			ctx.uiConversation.events.register(retryDefinition);
		}
		//#endregion
		//#region src/client/conversation-nodes/tool.ts
		const MAX_DEPTH = 256;
		const projectedBlocks = /* @__PURE__ */ new WeakMap();
		function jsonArguments(value) {
			return JSON.stringify(value);
		}
		function rootCall(match) {
			const event = match.event;
			if (event.type === "assistant/live-chunk") {
				const chunk = event.data.chunk;
				if (chunk.type !== "tool-call-delta" || !chunk.name) throw new Error("tool preparation requires a named call delta");
				return {
					phase: "preparing",
					callId: String(chunk.id),
					name: chunk.name,
					turn: event.data.turn,
					step: event.data.step,
					time: event.time,
					subCalls: []
				};
			}
			if (match.event.type !== "tool/call") throw new Error("tool-call start requires tool/call");
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
		function childCall(match, data) {
			return {
				phase: "start",
				callId: data.subCallId,
				parentCallId: data.parentCallId,
				name: data.name,
				argsRaw: jsonArguments(data.arguments),
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
					argsRaw: jsonArguments(data.arguments)
				},
				callTime: previous?.time ?? null,
				content: data.content ?? [],
				isError: data.isError === true,
				...data.error === void 0 ? {} : { error: data.error },
				subCalls: []
			};
		}
		function locationTurn(match) {
			return match.location.kind === "step" || match.location.kind === "turn" ? match.location.turn.turn : 0;
		}
		function locationStep(match) {
			return match.location.kind === "step" ? match.location.step.step : 0;
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
					callId: nested.callId,
					depth: candidate.depth + 1
				});
			}
			return parentDepth + subtreeDepth <= MAX_DEPTH;
		}
		function updateDispatch(state, match) {
			const event = match.event;
			if (event.type !== "tool/ptc-dispatch-start" && event.type !== "tool/ptc-dispatch") return state;
			const data = event.data;
			const parentCallId = String(data.parentCallId);
			const subCallId = String(data.subCallId);
			const siblings = state.children.get(parentCallId) ?? [];
			const index = siblings.findIndex((candidate) => candidate.callId === subCallId);
			if (event.type === "tool/ptc-dispatch-start") {
				if (index >= 0 || !acceptsEdge(state, parentCallId, subCallId)) return state;
				const children = new Map(state.children);
				children.set(parentCallId, [...siblings, childCall(match, data)]);
				const parents = new Map(state.parents);
				parents.set(subCallId, parentCallId);
				return {
					...state,
					children,
					parents
				};
			}
			if (index < 0 && !acceptsEdge(state, parentCallId, subCallId)) return state;
			const settled = childResult(match, data, index < 0 ? void 0 : siblings[index]);
			const children = new Map(state.children);
			children.set(parentCallId, index < 0 ? [...siblings, settled] : siblings.map((child, at) => at === index ? settled : child));
			const parents = new Map(state.parents);
			if (index < 0) parents.set(subCallId, parentCallId);
			return {
				...state,
				children,
				parents
			};
		}
		function projectBlock(block, state, interruptedAt, visited = /* @__PURE__ */ new Set(), depth = 1) {
			if (!("kind" in block) && block.phase === "preparing") return block;
			if (visited.has(block.callId) || depth > MAX_DEPTH) return {
				...block,
				subCalls: []
			};
			const nextVisited = new Set(visited);
			nextVisited.add(block.callId);
			const children = (state.children.get(block.callId) ?? block.subCalls).map((child) => projectBlock(child, state, interruptedAt, nextVisited, depth + 1));
			const interruptionSeq = "kind" in block ? void 0 : interruptedAt?.seq;
			const interruptionTime = "kind" in block ? void 0 : interruptedAt?.time;
			const cached = projectedBlocks.get(block);
			if (cached !== void 0 && cached.interruptionSeq === interruptionSeq && cached.interruptionTime === interruptionTime && sameReferences(cached.children, children)) return cached.value;
			const projected = "kind" in block || interruptedAt === void 0 ? sameReferences(block.subCalls, children) ? block : {
				...block,
				subCalls: children
			} : {
				kind: "tool-result",
				seq: interruptedAt.seq + CHAT_SYNTHETIC_SEQ_OFFSETS.interruptedFollowup,
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
				subCalls: children
			};
			projectedBlocks.set(block, {
				children,
				interruptionSeq,
				interruptionTime,
				value: projected
			});
			return projected;
		}
		function sameReferences(left, right) {
			return left.length === right.length && left.every((value, index) => value === right[index]);
		}
		function interruption(context) {
			const location = context.start?.location;
			if (location?.kind === "step" && location.step.status === "closed") return location.step.end;
			if ((location?.kind === "step" || location?.kind === "turn") && location.turn.status === "closed") return location.turn.end;
		}
		function fallbackState$2(context) {
			const match = context.matches.find((candidate) => candidate.event.type === "tool/result");
			const root = match === void 0 ? void 0 : rootResult(match);
			if (root === void 0) return void 0;
			let state = {
				root,
				children: /* @__PURE__ */ new Map(),
				parents: /* @__PURE__ */ new Map()
			};
			for (const candidate of context.matches) state = updateDispatch(state, candidate);
			return state;
		}
		/** Root Tool preparation, dispatch, result, and nested PTC calls. */
		const toolDefinition = {
			kind: "tool-call",
			target: "chat",
			match: (event) => {
				if (event.type === "assistant/live-chunk") {
					const chunk = event.data.chunk;
					return chunk.type === "tool-call-delta" && chunk.name ? {
						id: String(chunk.id),
						role: "start"
					} : null;
				}
				if (event.type === "tool/call") return {
					id: String(event.data.callId),
					role: "start"
				};
				if (event.type === "tool/result" && isAppendSurfaceEvent(event)) return {
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
			start: (_context, match) => ({
				root: rootCall(match),
				children: /* @__PURE__ */ new Map(),
				parents: /* @__PURE__ */ new Map()
			}),
			update: (context, match) => {
				if (match.event.type === "tool/call") return {
					...context.state,
					root: rootCall(match)
				};
				if (match.event.type === "tool/result") {
					const root = context.state.root;
					const result = rootResult(match, !("kind" in root) && root.phase === "start" ? root : void 0);
					return result === void 0 ? context.state : {
						...context.state,
						root: result
					};
				}
				return updateDispatch(context.state, match);
			},
			publication: (match) => match.event.type === "assistant/live-chunk" ? "animation-frame" : "immediate",
			buildViewNode: (context) => {
				const current = context.current.get("chat");
				const state = context.state ?? fallbackState$2(context);
				if (state === void 0) return current == null ? null : current.visibility === "hidden" ? current : {
					...current,
					visibility: "hidden"
				};
				const interruptedAt = interruption(context);
				const projected = projectBlock(state.root, state, interruptedAt);
				const anchor = context.start?.event.seq ?? ("kind" in state.root ? state.root.seq : context.matches[0]?.event.seq ?? 0);
				const visibility = !("kind" in projected) && projected.phase === "preparing" && interruptedAt !== void 0 ? "hidden" : "visible";
				const location = contextLocation(context);
				const data = current?.data.root === projected ? current.data : { root: projected };
				if (current?.data === data && current.anchorSeq === anchor && current.visibility === visibility && current.location === location) return current;
				return chatNode(context, "tool-call", anchor, data, {
					visibility,
					location
				});
			}
		};
		/**
		* Register the root Tool lifecycle and nested-subcall contribution.
		* @param ctx - owning UI Conversation context.
		*/
		function registerToolConversationNode(ctx) {
			ctx.uiConversation.events.register(toolDefinition);
		}
		//#endregion
		//#region src/client/conversation-nodes/turn-error.ts
		function lastStep$1(context) {
			const location = context.start?.location ?? context.matches[0]?.location;
			if (location?.kind !== "turn" && location?.kind !== "step") return 0;
			return location.turn.steps.at(-1)?.step ?? 0;
		}
		function failureFrom(match) {
			if (match.event.type !== "turn/end") return void 0;
			const reason = match.event.data.reason;
			const failure = reason.kind === "error" ? reason.error : reason.kind === "aborted" && reason.reason.kind === "hook" && reason.reason.reason === "deepseek-account/signed-out" ? {
				message: "Stopped because you signed out of DeepSeek.",
				code: "ACCOUNT_SIGNED_OUT"
			} : void 0;
			if (failure === void 0) return void 0;
			const display = displayFailure(failure);
			return {
				seq: match.event.seq,
				time: match.event.time,
				message: display.message,
				...display.code === void 0 ? {} : { code: display.code }
			};
		}
		function fallbackState$1(context) {
			const end = context.matches.find((match) => failureFrom(match) !== void 0);
			if (end?.event.type !== "turn/end") return void 0;
			const failure = failureFrom(end);
			if (failure === void 0) return void 0;
			return {
				turn: end.event.data.turn,
				failure
			};
		}
		/**
		* Terminal turn failure Definition. Retries run inside the failing turn, so the
		* turn's `llm/retry` history never suppresses this terminal row; the model-retry
		* node renders that history separately.
		*/
		const turnErrorDefinition = {
			kind: "turn-error",
			target: "chat",
			match: (event) => {
				if (event.type === "turn/start") return {
					id: String(event.data.turn),
					role: "start"
				};
				if (event.type === "turn/end" && (event.data.reason.kind === "error" || event.data.reason.kind === "aborted" && event.data.reason.reason.kind === "hook" && event.data.reason.reason.reason === "deepseek-account/signed-out")) return {
					id: String(event.data.turn),
					role: "update"
				};
				return null;
			},
			start: (_context, match) => {
				if (match.event.type !== "turn/start") throw new Error("turn-error start requires turn/start");
				return { turn: match.event.data.turn };
			},
			update: (context, match) => {
				const failure = failureFrom(match);
				return failure === void 0 ? context.state : {
					...context.state,
					failure
				};
			},
			buildViewNode: (context) => {
				const state = context.state ?? fallbackState$1(context);
				if (state?.failure === void 0) return null;
				const failure = state.failure;
				const node = {
					kind: "turn-error",
					seq: failure.seq,
					time: failure.time,
					turn: state.turn,
					step: lastStep$1(context),
					message: failure.message,
					...failure.code === void 0 ? {} : { code: failure.code }
				};
				return chatNode(context, "turn-error", node.seq, node);
			}
		};
		/**
		* Register the terminal Turn-error business contribution.
		* @param ctx - owning UI Conversation context.
		*/
		function registerTurnErrorConversationNode(ctx) {
			ctx.uiConversation.events.register(turnErrorDefinition);
		}
		//#endregion
		//#region src/client/conversation-nodes/turn-max-tokens.ts
		function lastStep(context) {
			const location = context.start?.location ?? context.matches[0]?.location;
			if (location?.kind !== "turn" && location?.kind !== "step") return 0;
			return location.turn.steps.at(-1)?.step ?? 0;
		}
		/**
		* Anchor the notice between the closing Assistant and the turn-tail so the
		* tail stays the turn's last Chat node and keeps its branch action enabled.
		* Without a closing text Assistant there is no branch action to protect, and
		* the turn/end seq keeps the notice at the truncation point.
		*/
		function noticeAnchor(context, seq) {
			const location = context.start?.location ?? context.matches[0]?.location;
			if (location?.kind !== "turn" && location?.kind !== "step") return seq;
			const closing = location.turn.data.get("turn-tail")?.closing;
			return closing === null || closing === void 0 ? seq : closing.finalNode.seq + CHAT_SYNTHETIC_SEQ_OFFSETS.maxTokensNotice;
		}
		function stateFrom(match) {
			if (match.event.type !== "turn/end" || match.event.data.reason.kind !== "max-tokens") return void 0;
			return {
				turn: match.event.data.turn,
				seq: match.event.seq,
				time: match.event.time
			};
		}
		/** Notice Definition for a turn the provider ended at its output-token cap. */
		const turnMaxTokensDefinition = {
			kind: "turn-max-tokens",
			target: "chat",
			match: (event) => {
				if (event.type === "turn/end" && event.data.reason.kind === "max-tokens") return {
					id: String(event.data.turn),
					role: "start"
				};
				return null;
			},
			start: (_context, match) => {
				const state = stateFrom(match);
				if (state === void 0) throw new Error("turn-max-tokens start requires a max-tokens turn/end");
				return state;
			},
			update: (context) => context.state,
			buildViewNode: (context) => {
				const state = context.state;
				if (state === void 0) return null;
				const node = {
					kind: "turn-max-tokens",
					seq: state.seq,
					time: state.time,
					turn: state.turn,
					step: lastStep(context)
				};
				return chatNode(context, "turn-max-tokens", noticeAnchor(context, state.seq), node);
			}
		};
		/**
		* Register the max-tokens turn-end notice contribution.
		* @param ctx - owning UI Conversation context.
		*/
		function registerTurnMaxTokensConversationNode(ctx) {
			ctx.uiConversation.events.register(turnMaxTokensDefinition);
		}
		//#endregion
		//#region src/client/conversation-nodes/turn-process.ts
		function eventTurn(event) {
			const data = event.data;
			return typeof data.turn === "number" ? data.turn : void 0;
		}
		function visibleChunk(chunk) {
			if (chunk.type === "text-delta" || chunk.type === "reasoning-delta") return chunk.text.trim() !== "";
			if (chunk.type === "block-start") return chunk.blockType !== "text" && chunk.blockType !== "reasoning" && chunk.blockType !== "tool-call";
			if (chunk.type !== "block-end") return false;
			const block = chunk.block;
			if (block.type === "tool-call") return false;
			if (block.type === "text" || block.type === "reasoning") return block.text.trim() !== "";
			return true;
		}
		function visibleAssistantEvent(event) {
			if (event.type === "assistant/live-chunk") return visibleChunk(event.data.chunk);
			if (event.type === "assistant/attempt") return false;
			return event.type === "assistant/message" && event.surfaceOp === "append" && toAssistantBlocks(event.data.message.content).some((block) => {
				if (block.kind === "tool-call") return false;
				if (block.kind === "text" || block.kind === "reasoning") return block.text.trim() !== "";
				return true;
			});
		}
		function processEvidence(event) {
			if (visibleAssistantEvent(event)) {
				if (event.type !== "assistant/live-chunk" && event.type !== "assistant/message" && event.type !== "assistant/attempt") return void 0;
				return {
					kind: "assistant",
					seq: event.seq,
					step: event.data.step
				};
			}
			if (event.type === "tool/call" || event.type === "tool/result" && event.surfaceOp === "append" || event.type === "llm/retry") return {
				kind: "other",
				seq: event.seq
			};
		}
		function turnLocation$1(context) {
			const location = context.start?.location ?? context.matches.at(-1)?.location;
			return location?.kind === "turn" || location?.kind === "step" ? location.turn : void 0;
		}
		function fallbackState(context) {
			const turn = context.matches.map((match) => eventTurn(match.event)).find((candidate) => candidate !== void 0);
			if (turn === void 0) return void 0;
			let state = {
				turn,
				assistantStartByStep: /* @__PURE__ */ new Map(),
				messageCountByStep: /* @__PURE__ */ new Map(),
				messageCount: 0,
				toolCallCount: 0,
				subagentCount: 0
			};
			for (const match of context.matches) state = updateProcessState(state, match.event);
			return state;
		}
		function isFinalAssistant(data) {
			return data?.finalNode !== void 0;
		}
		function latestAnswer(turn) {
			const data = turn.steps.at(-1)?.data.get("assistant-step");
			if (!isFinalAssistant(data) || !hasAssistantReplyContent(data.blocks)) return null;
			return data.blocks.some((block) => block.kind === "tool-call") ? null : data;
		}
		function processSpec(state, turn) {
			const controlAnchorSeq = state.controlAnchorSeq ?? turn.start?.seq;
			if (controlAnchorSeq === void 0) return null;
			const answer = latestAnswer(turn);
			const counts = {
				messageCount: answer === null ? state.messageCount : [...state.messageCountByStep].filter(([step]) => step < answer.step).reduce((total, [, count]) => total + count, 0),
				toolCallCount: state.toolCallCount,
				subagentCount: state.subagentCount
			};
			if (answer === null) return {
				turn: turn.turn,
				controlAnchorSeq,
				processStartSeq: controlAnchorSeq,
				answerAnchorSeq: null,
				answerStep: null,
				inlineReasoning: false,
				...counts
			};
			const inlineReasoning = answer.blocks.some((block) => block.kind === "reasoning" && block.text.trim() !== "");
			const earlierAssistantSeq = Math.min(...[...state.assistantStartByStep].filter(([step]) => step < answer.step).map(([, seq]) => seq));
			const externalProcessSeq = Math.min(state.otherStartSeq ?? Number.POSITIVE_INFINITY, earlierAssistantSeq);
			return {
				turn: turn.turn,
				controlAnchorSeq,
				processStartSeq: turn.start?.seq ?? (Number.isFinite(externalProcessSeq) ? externalProcessSeq : answer.finalNode.seq),
				answerAnchorSeq: answer.finalNode.seq,
				answerStep: answer.step,
				inlineReasoning,
				...counts
			};
		}
		function updateProcessState(state, event) {
			let current = state;
			if (event.type === "assistant/message" && event.surfaceOp === "append" && hasAssistantReplyContent(toAssistantBlocks(event.data.message.content))) {
				const messageCountByStep = new Map(current.messageCountByStep);
				messageCountByStep.set(event.data.step, (messageCountByStep.get(event.data.step) ?? 0) + 1);
				current = {
					...current,
					messageCountByStep,
					messageCount: current.messageCount + 1
				};
			}
			if (event.type === "tool/call") {
				const subagent = isSubagentDelegationTool(event.data.name);
				current = {
					...current,
					toolCallCount: current.toolCallCount + (subagent ? 0 : 1),
					subagentCount: current.subagentCount + (subagent ? 1 : 0)
				};
			}
			const evidence = processEvidence(event);
			if (evidence === void 0) return current;
			if (evidence.kind === "other") return current.otherStartSeq === void 0 ? {
				...current,
				otherStartSeq: evidence.seq,
				controlAnchorSeq: Math.min(current.controlAnchorSeq ?? Number.POSITIVE_INFINITY, evidence.seq)
			} : current;
			if (current.assistantStartByStep.has(evidence.step)) return current;
			const assistantStartByStep = new Map(current.assistantStartByStep);
			assistantStartByStep.set(evidence.step, evidence.seq);
			return {
				...current,
				assistantStartByStep,
				controlAnchorSeq: Math.min(current.controlAnchorSeq ?? Number.POSITIVE_INFINITY, evidence.seq)
			};
		}
		/** Turn-scoped process range and answer-boundary Definition. */
		const turnProcessDefinition = {
			kind: "turn-process",
			target: "chat",
			match: (event) => {
				if (event.type === "turn/start") return {
					id: String(event.data.turn),
					role: "start"
				};
				const turn = eventTurn(event);
				if (turn === void 0) return null;
				if (event.type === "assistant/live-chunk" || event.type === "assistant/message" || event.type === "tool/call" || event.type === "tool/result" || event.type === "llm/retry" || event.type === "step/start" || event.type === "step/end" || event.type === "turn/end") return {
					id: String(turn),
					role: "update"
				};
				return null;
			},
			start: (_context, match) => {
				if (match.event.type !== "turn/start") throw new Error("turn-process start requires turn/start");
				return {
					turn: match.event.data.turn,
					assistantStartByStep: /* @__PURE__ */ new Map(),
					messageCountByStep: /* @__PURE__ */ new Map(),
					messageCount: 0,
					toolCallCount: 0,
					subagentCount: 0
				};
			},
			update: (context, match) => updateProcessState(context.state, match.event),
			publication: (match) => {
				if (match.event.type === "assistant/live-chunk") {
					const type = match.event.data.chunk.type;
					return type === "usage" || type === "finish" ? "none" : "animation-frame";
				}
				return "immediate";
			},
			buildLocationData: (context, scope, previous) => {
				if (scope !== "turn") return null;
				const state = context.state ?? fallbackState(context);
				if (state === void 0) return null;
				const turn = turnLocation$1(context);
				if (turn === void 0) return null;
				const current = context.current.get("chat");
				const latestStep = turn.steps.at(-1);
				if (previous?.kind === "turn" && previous.key === "turn-process" && current?.kind === "turn-process" && current.data.answerAnchorSeq === null && current.data.controlAnchorSeq === state.controlAnchorSeq && current.data.messageCount === state.messageCount && current.data.toolCallCount === state.toolCallCount && current.data.subagentCount === state.subagentCount && turn.status !== "closed" && latestStep?.status !== "closed") return previous;
				const spec = processSpec(state, turn);
				if (spec === null) return null;
				if (previous?.kind === "turn" && previous.turn === spec.turn && previous.key === "turn-process" && sameTurnProcessSpec(previous.value, spec)) return previous;
				return {
					kind: "turn",
					turn: turn.turn,
					key: "turn-process",
					value: spec
				};
			},
			buildViewNode: (context) => {
				const turn = turnLocation$1(context);
				const data = turn?.data.get("turn-process");
				if (turn === void 0 || data === void 0) return null;
				const current = context.current.get("chat");
				const state = context.state;
				if (current?.kind === "turn-process" && state !== void 0 && current.data.answerAnchorSeq === null && current.data.controlAnchorSeq === state.controlAnchorSeq && current.data.messageCount === state.messageCount && current.data.toolCallCount === state.toolCallCount && current.data.subagentCount === state.subagentCount && turn.status !== "closed" && turn.steps.at(-1)?.status !== "closed" && current.location === (context.start?.location ?? context.matches[0]?.location)) return current;
				return chatNode(context, "turn-process", data.controlAnchorSeq + CHAT_SYNTHETIC_SEQ_OFFSETS.processControl, data);
			}
		};
		/**
		* Register the Turn-scoped process disclosure projection.
		* @param ctx - owning UI Conversation context.
		*/
		function registerTurnProcess(ctx) {
			ctx.uiConversation.events.register(turnProcessDefinition);
		}
		//#endregion
		//#region ../../llm/llm/lib/types/assistant-stream.js
		/**
		* The last raw chunk of one never-packed type, scanning backwards and stopping at the first hit.
		* @param stream - compact records from one durable Assistant settlement.
		* @param type - chunk type that only appears as a raw record.
		* @returns the stream's final chunk of that type, or undefined when it has none.
		*/
		function lastAssistantStreamChunk(stream, type) {
			for (let index = stream.length - 1; index >= 0; index -= 1) {
				const record = stream[index];
				if (record.type === "chunk" && record.chunk.type === type) return record.chunk;
			}
		}
		//#endregion
		//#region ../../llm/token-meter/src/turn-usage.ts
		function isCount(value) {
			return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
		}
		function safeSum(values) {
			let total = 0;
			for (const value of values) {
				total += value;
				if (!Number.isSafeInteger(total)) return void 0;
			}
			return total;
		}
		function messageRoute(message) {
			const { provider, model } = message.source;
			return provider.length > 0 && model.length > 0 ? {
				provider,
				model
			} : void 0;
		}
		function streamUsage(stream) {
			return lastAssistantStreamChunk(stream, "usage")?.usage;
		}
		function normalizeUsage(usage, route) {
			const { inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, reasoningTokens, totalTokens } = usage;
			if (!isCount(inputTokens) || !isCount(outputTokens)) return void 0;
			if (cacheReadTokens !== void 0 && !isCount(cacheReadTokens)) return void 0;
			if (cacheWriteTokens !== void 0 && !isCount(cacheWriteTokens)) return void 0;
			if (reasoningTokens !== void 0 && (!isCount(reasoningTokens) || reasoningTokens > outputTokens)) return;
			const knownPrompt = safeSum([
				inputTokens,
				...cacheReadTokens === void 0 ? [] : [cacheReadTokens],
				...cacheWriteTokens === void 0 ? [] : [cacheWriteTokens]
			]);
			if (knownPrompt === void 0) return void 0;
			let exactTotal;
			if (totalTokens !== void 0) {
				if (!isCount(totalTokens)) return void 0;
				const exactPrompt = totalTokens - outputTokens;
				if (!isCount(exactPrompt) || exactPrompt < knownPrompt) return void 0;
				if (cacheReadTokens !== void 0 && cacheWriteTokens !== void 0 && exactPrompt !== knownPrompt) return;
				exactTotal = totalTokens;
			} else {
				if (cacheReadTokens === void 0 || cacheWriteTokens === void 0) return void 0;
				const derivedTotal = safeSum([knownPrompt, outputTokens]);
				if (derivedTotal === void 0) return void 0;
				exactTotal = derivedTotal;
			}
			return {
				inputTokens,
				outputTokens,
				totalTokens: exactTotal,
				...cacheReadTokens === void 0 ? {} : { cacheReadTokens },
				...cacheWriteTokens === void 0 ? {} : { cacheWriteTokens },
				...reasoningTokens === void 0 ? {} : { reasoningTokens },
				...route === void 0 ? {} : { route }
			};
		}
		function aggregateAttempts(attempts) {
			if (attempts.length === 0) return void 0;
			const inputTokens = safeSum(attempts.map((attempt) => attempt.inputTokens));
			const outputTokens = safeSum(attempts.map((attempt) => attempt.outputTokens));
			const totalTokens = safeSum(attempts.map((attempt) => attempt.totalTokens));
			if (inputTokens === void 0 || outputTokens === void 0 || totalTokens === void 0) return void 0;
			const cacheRead = attempts.map((attempt) => attempt.cacheReadTokens);
			const cacheWrite = attempts.map((attempt) => attempt.cacheWriteTokens);
			const reasoning = attempts.map((attempt) => attempt.reasoningTokens);
			const cacheReadTokens = cacheRead.every(isCount) ? safeSum(cacheRead) : void 0;
			const cacheWriteTokens = cacheWrite.every(isCount) ? safeSum(cacheWrite) : void 0;
			const reasoningTokens = reasoning.every(isCount) ? safeSum(reasoning) : void 0;
			let routes;
			const attributed = attempts.map((attempt) => attempt.route);
			if (attributed.every((route) => route !== void 0)) {
				const unique = /* @__PURE__ */ new Map();
				for (const route of attributed) unique.set(`${route.provider}\0${route.model}`, route);
				routes = [...unique.values()];
			}
			return {
				uncachedInputTokens: inputTokens,
				outputTokens,
				totalTokens,
				...cacheReadTokens === void 0 ? {} : { cacheReadTokens },
				...cacheWriteTokens === void 0 ? {} : { cacheWriteTokens },
				...reasoningTokens === void 0 ? {} : { reasoningTokens },
				...routes === void 0 ? {} : { routes }
			};
		}
		function sameAttempt(state, turn, step) {
			return state.turn === turn && state.step === step;
		}
		/**
		* Fold one complete Turn's durable attempt lifecycle into exact token accounting.
		*
		* No attempt is inferred from a usage sample. Any missing lifecycle boundary,
		* incomplete attempt usage, unsafe count, or contradictory exact total makes
		* the whole disclosure unavailable.
		* @param events - Turn-local durable events from `turn/start` through `turn/end`.
		* @returns exact aggregate usage, or undefined when it cannot be proven.
		*/
		function deriveTurnTokenUsage(events) {
			let state = { kind: "idle" };
			const attempts = [];
			let turn;
			let sawEnd = false;
			let invalid = false;
			const closeOpen = (route) => {
				if (state.kind !== "open" || state.sample === void 0) return false;
				const normalized = normalizeUsage(state.sample, route);
				if (normalized === void 0) return false;
				attempts.push(normalized);
				return true;
			};
			for (const event of events) {
				if (invalid) break;
				if (event.type === "turn/start") {
					if (turn !== void 0 || state.kind !== "idle") invalid = true;
					else turn = event.data.turn;
					continue;
				}
				if (turn === void 0) {
					invalid = true;
					break;
				}
				if (event.type === "turn/end") {
					if (event.data.turn !== turn || state.kind !== "idle" || sawEnd) invalid = true;
					else sawEnd = true;
					continue;
				}
				if (sawEnd) {
					invalid = true;
					break;
				}
				if (event.type === "step/start") {
					if (event.data.turn !== turn || state.kind !== "idle") invalid = true;
					else state = {
						kind: "open",
						turn,
						step: event.data.step
					};
					continue;
				}
				if (event.type === "llm/retry-started") {
					if (event.data.turn !== turn || state.kind !== "settled" || state.by !== "retry" || !sameAttempt(state, event.data.turn, event.data.step)) invalid = true;
					else state = {
						kind: "open",
						turn,
						step: event.data.step
					};
					continue;
				}
				if (event.type === "assistant/attempt") {
					if (event.data.turn !== turn || state.kind !== "open" || !sameAttempt(state, event.data.turn, event.data.step)) {
						invalid = true;
						continue;
					}
					const sample = streamUsage(event.data.stream) ?? state.sample;
					state = {
						kind: "open",
						turn,
						step: event.data.step,
						...sample === void 0 ? {} : { sample }
					};
					if (!closeOpen()) invalid = true;
					else state = {
						kind: "finishClosed",
						turn,
						step: event.data.step
					};
					continue;
				}
				if (event.type === "assistant/message") {
					if (event.data.turn !== turn || state.kind !== "open" || !sameAttempt(state, event.data.turn, event.data.step)) {
						invalid = true;
						continue;
					}
					const sample = event.data.usage ?? streamUsage(event.data.stream);
					if (sample !== void 0) state = {
						...state,
						sample
					};
					if (!closeOpen(messageRoute(event.data.message))) invalid = true;
					else state = {
						kind: "settled",
						turn,
						step: event.data.step,
						by: "message"
					};
					continue;
				}
				if (event.type === "llm/retry") {
					if (event.data.turn !== turn || state.kind === "idle" || !sameAttempt(state, event.data.turn, event.data.step)) {
						invalid = true;
						continue;
					}
					if (state.kind === "settled" || state.kind === "open" && !closeOpen()) invalid = true;
					if (!invalid) state = {
						kind: "settled",
						turn,
						step: event.data.step,
						by: "retry"
					};
					continue;
				}
				if (event.type === "step/end") {
					if (event.data.turn !== turn || state.kind === "idle" || !sameAttempt(state, event.data.turn, event.data.step)) {
						invalid = true;
						continue;
					}
					if (state.kind === "open" && !closeOpen()) invalid = true;
					if (!invalid) state = { kind: "idle" };
				}
			}
			return invalid || !sawEnd || state.kind !== "idle" ? void 0 : aggregateAttempts(attempts);
		}
		//#endregion
		//#region src/client/conversation-nodes/turn-tail.ts
		function isSessionEvent(event) {
			return event.type !== "assistant/live-chunk";
		}
		function turnCoordinates(event) {
			if (event.type === "assistant/message" || event.type === "assistant/attempt" || event.type === "assistant/live-chunk" || event.type === "step/start" || event.type === "step/end") return {
				turn: event.data.turn,
				step: event.data.step
			};
			if (event.type === "llm/retry" || event.type === "llm/retry-started") return {
				turn: event.data.turn,
				step: event.data.step
			};
		}
		function turnLocation(context) {
			const location = context.start?.location ?? context.matches[0]?.location;
			return location?.kind === "turn" || location?.kind === "step" ? location.turn : void 0;
		}
		function hasText(data) {
			return data.finalNode !== void 0 && data.blocks.some((block) => block.kind === "text" && block.text.trim() !== "");
		}
		function tailData(context) {
			const end = context.state === void 0 ? context.matches.find((match) => match.event.type === "turn/end") : context.state.end;
			if (end?.event.type !== "turn/end") return null;
			const turn = turnLocation(context);
			if (turn === void 0) return null;
			const finalized = turn.steps.map((step) => step.data.get("assistant-step")).filter((candidate) => candidate !== void 0).filter((candidate) => candidate.finalNode !== void 0).sort((left, right) => left.finalNode.seq - right.finalNode.seq);
			const closing = finalized.findLast(hasText) ?? null;
			let latestTranscriptSeq = finalized.at(-1)?.finalNode.seq;
			for (const match of context.matches) {
				const event = match.event;
				const candidate = event.type === "tool/call" || event.type === "tool/result" && event.surfaceOp === "append" || event.type === "turn/end" && event.data.reason.kind === "error" || event.type === "llm/retry" ? event.seq : void 0;
				if (candidate !== void 0 && (latestTranscriptSeq === void 0 || candidate > latestTranscriptSeq)) latestTranscriptSeq = candidate;
			}
			const tokenUsage = context.start?.event.type === "turn/start" ? deriveTurnTokenUsage(context.matches.map((match) => match.event).filter(isSessionEvent)) : void 0;
			return {
				turn: end.event.data.turn,
				seq: end.event.seq,
				time: end.event.time,
				closing,
				branchUnavailable: closing === null || latestTranscriptSeq !== closing.finalNode.seq,
				...tokenUsage === void 0 ? {} : { tokenUsage }
			};
		}
		/** Completed-turn footer Definition independent of any Assistant row. */
		const turnTailDefinition = {
			kind: "turn-tail",
			target: "chat",
			match: (event) => {
				if (event.type === "turn/start") return {
					id: String(event.data.turn),
					role: "start"
				};
				if (event.type === "turn/end") return {
					id: String(event.data.turn),
					role: "update"
				};
				if (event.type === "tool/call" || event.type === "tool/result") return {
					id: String(event.data.turn),
					role: "update"
				};
				const coordinates = turnCoordinates(event);
				if (coordinates !== void 0) return {
					id: String(coordinates.turn),
					role: "update"
				};
				return null;
			},
			start: (_context, match) => {
				if (match.event.type !== "turn/start") throw new Error("turn-tail start requires turn/start");
				return { turn: match.event.data.turn };
			},
			update: (context, match) => match.event.type === "turn/end" ? {
				...context.state,
				end: match
			} : context.state,
			publication: (match) => match.event.type === "turn/end" ? "immediate" : "none",
			buildLocationData: (context, scope) => {
				if (scope !== "turn") return null;
				const value = tailData(context);
				return value === null ? null : {
					kind: "turn",
					turn: value.turn,
					key: "turn-tail",
					value
				};
			},
			buildViewNode: (context) => {
				const data = turnLocation(context)?.data.get("turn-tail");
				return data === void 0 ? null : chatNode(context, "turn-tail", data.seq + CHAT_SYNTHETIC_SEQ_OFFSETS.finalizedFollowup, data);
			}
		};
		/**
		* Register completed-Turn footer data and its Chat node contribution.
		* @param ctx - owning UI Conversation context.
		*/
		function registerTurnTailConversationNode(ctx) {
			ctx.uiConversation.events.register(turnTailDefinition);
		}
		//#endregion
		//#region ../../util/brand/src/index.ts
		/**
		* Apply a compile-time string brand without changing the value.
		* @param value - string admitted by the domain that owns the target brand.
		* @returns the same string with the requested compile-time brand.
		*/
		function brandString(value) {
			return value;
		}
		//#endregion
		//#region src/client/conversation-nodes/process-activity.ts
		function activity(name) {
			if (name === "read") return "read";
			if (name === "read_image") return "readImage";
			if (name === "grep" || name === "glob" || name.endsWith("_inspect")) return "search";
			if (name === "write") return "write";
			if (name === "edit" || name === "apply_patch") return "edit";
			if ([
				"bash",
				"pwsh",
				"exec_command",
				"write_stdin"
			].includes(name) || name.startsWith("terminal_")) return "commands";
			if (name === "run_code") return "code";
			if (name === "web_search") return "webSearch";
			if (name === "web_fetch") return "webFetch";
			if (name === "subagent" || name.startsWith("subagent_")) return "subagents";
			if ([
				"todo_write",
				"create_goal",
				"update_goal",
				"get_goal"
			].includes(name)) return "plan";
			if (name === "ask_user_question" || name === "request_user_input") return "questions";
			return "tools";
		}
		const LIVE_TOOL_DETAIL_MAX_CHARS = 160;
		const LIVE_TOOL_DETAIL_SEGMENTER = new Intl.Segmenter(void 0, { granularity: "grapheme" });
		const LIVE_TOOL_DETAIL_KEYS = [
			"title",
			"description",
			"objective",
			"task",
			"task_name",
			"name",
			"question",
			"questions",
			"prompt",
			"message",
			"command",
			"cmd",
			"queries",
			"query",
			"pattern",
			"url",
			"uri",
			"file_path",
			"path",
			"target",
			"action",
			"status"
		];
		function normalizeLiveToolDetail(value) {
			const normalized = (typeof value === "string" ? value : Array.isArray(value) && value.every((item) => typeof item === "string") ? value.join(", ") : "").replace(/\s+/g, " ").trim();
			const chars = Array.from(LIVE_TOOL_DETAIL_SEGMENTER.segment(normalized), (part) => part.segment);
			return chars.length <= LIVE_TOOL_DETAIL_MAX_CHARS ? normalized : `${chars.slice(0, LIVE_TOOL_DETAIL_MAX_CHARS - 1).join("").trimEnd()}…`;
		}
		function questionDetail(value) {
			if (!Array.isArray(value)) return "";
			for (const item of value) {
				if (item === null || typeof item !== "object") continue;
				const detail = normalizeLiveToolDetail(Reflect.get(item, "question"));
				if (detail !== "") return detail;
			}
			return "";
		}
		function liveReasoningDetail(nodes) {
			for (let nodeIndex = nodes.length - 1; nodeIndex >= 0; nodeIndex--) {
				const node = nodes[nodeIndex];
				if (node?.kind !== "assistant-step" || node.data.status !== "running") continue;
				for (let blockIndex = node.data.blocks.length - 1; blockIndex >= 0; blockIndex--) {
					const block = node.data.blocks[blockIndex];
					if (block?.kind !== "reasoning") continue;
					const paragraphs = block.text.split(/\r?\n[\t ]*\r?\n/);
					for (let paragraphIndex = paragraphs.length - 1; paragraphIndex >= 0; paragraphIndex--) {
						const detail = normalizeLiveToolDetail(paragraphs[paragraphIndex]?.replaceAll("**", ""));
						if (detail !== "") return detail;
					}
				}
			}
			return "";
		}
		function liveToolDetail(name, argsRaw) {
			let args;
			try {
				args = JSON.parse(argsRaw);
			} catch (_error) {
				return normalizeLiveToolDetail(name);
			}
			if (args === null || typeof args !== "object") return normalizeLiveToolDetail(name);
			for (const key of LIVE_TOOL_DETAIL_KEYS) if (key in args) {
				const value = Reflect.get(args, key);
				const detail = key === "questions" ? questionDetail(value) : normalizeLiveToolDetail(value);
				if (detail !== "") return detail;
			}
			return normalizeLiveToolDetail(name);
		}
		/**
		* Rank categories by distinct call count, breaking ties by first appearance.
		* @param nodes - process members, including recursive tools.
		* @returns all ranked categories and the latest running tool category and bounded task detail.
		*/
		function processActivity(nodes) {
			const counts = /* @__PURE__ */ new Map();
			const seen = /* @__PURE__ */ new Set();
			let running;
			let runningDetail = "";
			let runningTime = -Infinity;
			let preparing;
			const visit = (tool) => {
				if (seen.has(tool.callId)) return;
				seen.add(tool.callId);
				const call = isRunningTool(tool) ? tool : tool.call;
				if (call !== null) {
					const kind = activity(call.name);
					if (isRunningTool(tool) && tool.time >= runningTime) {
						running = kind;
						preparing = tool.phase === "preparing";
						runningDetail = tool.phase === "preparing" ? kind === "tools" ? tool.name : "" : liveToolDetail(tool.name, tool.argsRaw);
						runningTime = tool.time;
					}
					counts.set(kind, (counts.get(kind) ?? 0) + 1);
				}
				for (const child of tool.subCalls) visit(child);
			};
			for (const node of nodes) if (node.kind === "tool-call") visit(node.data.root);
			if (running === void 0) runningDetail = liveReasoningDetail(nodes);
			return {
				counts: [...counts].map(([kind, count]) => ({
					kind,
					count
				})).sort((a, b) => b.count - a.count),
				running,
				runningDetail,
				...preparing ? { preparing: true } : {}
			};
		}
		//#endregion
		//#region src/client/conversation-nodes/process-groups.ts
		/** Chat-owned segmentation and incremental summaries over materialized Node inputs. */
		const INDEPENDENT = new Set([
			"user",
			"steering",
			"turn-trigger",
			"model-retry",
			"turn-error",
			"turn-max-tokens",
			"turn-tail"
		]);
		function turnOf(node) {
			const location = node.location;
			return location.kind === "turn" || location.kind === "step" ? location.turn.turn : void 0;
		}
		function reasoning(node) {
			return node.kind === "assistant-step" && node.data.blocks.some((block) => block.kind === "reasoning" && block.text.trim() !== "");
		}
		function reply(node) {
			return node.kind === "assistant-step" && hasAssistantReplyContent(node.data.blocks);
		}
		function sameSummary(left, right) {
			return left.running === right.running && left.runningDetail === right.runningDetail && left.preparing === right.preparing && left.counts.length === right.counts.length && left.counts.every((value, index) => value.kind === right.counts[index]?.kind && value.count === right.counts[index].count);
		}
		function sameMembers(left, right) {
			return left.length === right.length && left.every((value, index) => value.key === right[index]?.key && value.groupPart === right[index].groupPart);
		}
		function structureChanged(previous, current) {
			if (!isVisibleChatNode(current) && (previous === void 0 || !isVisibleChatNode(previous))) return false;
			return previous === void 0 || previous.kind !== current.kind || turnOf(previous) !== turnOf(current) || isVisibleChatNode(previous) !== isVisibleChatNode(current) || reasoning(previous) !== reasoning(current) || reply(previous) !== reply(current);
		}
		function readNode(input, key) {
			const node = input.readNode(key);
			if (node === void 0) throw new Error(`Chat grouping input is missing Node ${key}`);
			return node;
		}
		/** One group's members and cached summary, refreshed together when its content changes. */
		var ProcessGroup = class {
			key;
			turn;
			members;
			nodes = [];
			snapshot;
			constructor(key, turn, members) {
				this.key = key;
				this.turn = turn;
				this.members = members;
				this.snapshot = {
					key,
					members,
					data: {
						turn,
						closed: false,
						summary: {
							counts: [],
							running: void 0,
							runningDetail: ""
						}
					}
				};
			}
			refresh(input, closed) {
				const nodes = this.members.map((member) => readNode(input, member.key));
				const unchanged = nodes.length === this.nodes.length && nodes.every((node, index) => node === this.nodes[index]);
				const previous = this.snapshot.data;
				const activity = unchanged && previous.closed === closed ? previous.summary : processActivity(nodes);
				const summary = closed ? {
					counts: activity.counts,
					running: void 0,
					runningDetail: ""
				} : activity;
				this.nodes = nodes;
				if (previous.closed !== closed || !sameSummary(previous.summary, summary)) this.snapshot = {
					key: this.key,
					members: this.members,
					data: {
						turn: this.turn,
						closed,
						summary
					}
				};
			}
		};
		/** One Turn's grouping result and member lookup; summaries stay with their groups. */
		var TurnGroups = class {
			turn;
			groups = /* @__PURE__ */ new Map();
			membership = /* @__PURE__ */ new Map();
			roots = /* @__PURE__ */ new Map();
			constructor(turn) {
				this.turn = turn;
			}
			references(key) {
				return this.roots.get(key) ?? [];
			}
			snapshots() {
				return [...this.groups.values()].map((group) => group.snapshot);
			}
			refresh(input, changed) {
				const dirty = /* @__PURE__ */ new Set();
				for (const node of changed) {
					const group = this.membership.get(node);
					if (group !== void 0) dirty.add(group);
				}
				const ended = input.timeline.turns.get(this.turn)?.status === "closed";
				if (ended) {
					for (const group of this.groups.values()) if (!group.snapshot.data.closed) dirty.add(group.key);
				}
				const upserts = [];
				for (const key of dirty) {
					const group = this.groups.get(key);
					const previous = group.snapshot;
					group.refresh(input, previous.data.closed || ended);
					if (group.snapshot !== previous) upserts.push(group.snapshot);
				}
				return upserts;
			}
			rebuild(input, added) {
				const roots = /* @__PURE__ */ new Map();
				const groups = /* @__PURE__ */ new Map();
				const membership = /* @__PURE__ */ new Map();
				let pending = [];
				const upserts = [];
				const emit = (key, entry) => {
					roots.set(key, [...roots.get(key) ?? [], entry]);
				};
				const flush = (closed) => {
					const first = pending[0];
					if (first === void 0) return;
					const key = this.extendedGroup(pending, added)?.key ?? brandString(JSON.stringify([
						"process",
						first.key,
						first.groupPart ?? null
					]));
					const previous = this.groups.get(key);
					const before = previous?.snapshot;
					const group = previous !== void 0 && sameMembers(previous.members, pending) ? previous : new ProcessGroup(key, this.turn, pending);
					group.refresh(input, closed || input.timeline.turns.get(this.turn)?.status === "closed");
					groups.set(group.key, group);
					emit(first.key, {
						kind: "group",
						key: group.key
					});
					for (const member of pending) membership.set(member.key, group.key);
					if (group.snapshot !== before) upserts.push(group.snapshot);
					pending = [];
				};
				let previous;
				let followed = false;
				for (const key of input.readTurn(this.turn)) {
					const position = readPosition(input, key);
					if (previous !== void 0 && position.previous !== previous) flush(true);
					previous = key;
					followed = position.next !== void 0;
					const node = readNode(input, key);
					if (INDEPENDENT.has(node.kind)) {
						flush(true);
						emit(key, {
							kind: "node",
							key
						});
					} else if (node.kind === "turn-process") emit(key, {
						kind: "node",
						key
					});
					else if (node.kind === "assistant-step") {
						if (reasoning(node)) pending.push({
							kind: "node",
							key,
							groupPart: "reasoning"
						});
						if (reply(node)) {
							flush(true);
							emit(key, {
								kind: "node",
								key,
								groupPart: "response"
							});
						}
					} else pending.push({
						kind: "node",
						key
					});
				}
				flush(followed);
				const removes = [...this.groups.keys()].filter((key) => !groups.has(key));
				this.groups = groups;
				this.membership = membership;
				this.roots = roots;
				return {
					upserts,
					removes
				};
			}
			extendedGroup(members, added) {
				const offset = members.findIndex((member) => !added.has(member.key));
				const first = members[offset];
				if (first === void 0) return void 0;
				const key = this.membership.get(first.key);
				const previous = key === void 0 ? void 0 : this.groups.get(key);
				if (previous === void 0 || offset + previous.members.length > members.length) return void 0;
				for (let index = 0; index < previous.members.length; index++) {
					const before = previous.members[index];
					const after = members[offset + index];
					if (before.key !== after.key || before.groupPart !== after.groupPart) return void 0;
				}
				for (let index = offset + previous.members.length; index < members.length; index++) if (!added.has(members[index].key)) return void 0;
				return previous;
			}
		};
		function readPosition(input, key) {
			const position = input.readPosition(key);
			if (position === void 0) throw new Error(`Chat grouping order is missing position for Node ${key}`);
			return position;
		}
		/** Session-local Turn results; ordinary updates never read other Turns' Node contents. */
		var ProcessState = class {
			turns = /* @__PURE__ */ new Map();
			order = [];
			pending = null;
			/**
			* Consume one synchronous Builder input without retaining its readers.
			* @param input - projected Node changes, indexed positions, and Turn lifecycle.
			*/
			accept(input) {
				if (input.kind === "replace") {
					const previousKeys = new Set(this.order);
					const added = new Set(input.order.filter((key) => !previousKeys.has(key)));
					const turns = /* @__PURE__ */ new Map();
					for (const key of input.order) {
						const turn = readPosition(input, key).turn;
						if (turn === void 0 || turns.has(turn)) continue;
						const groups = this.turns.get(turn) ?? new TurnGroups(turn);
						groups.rebuild(input, added);
						turns.set(turn, groups);
					}
					this.turns = turns;
					this.order = input.order;
					this.pending = {
						entries: this.rootEntries(input),
						groups: {
							kind: "replace",
							snapshots: [...turns.values()].flatMap((turn) => turn.snapshots())
						}
					};
					return;
				}
				const regroup = new Set(input.changedTurnOrders);
				const added = /* @__PURE__ */ new Set();
				const changed = /* @__PURE__ */ new Map();
				const touch = (turn) => {
					let keys = changed.get(turn);
					if (keys === void 0) {
						keys = /* @__PURE__ */ new Set();
						changed.set(turn, keys);
					}
					return keys;
				};
				for (const change of input.changes) {
					const before = change.previous;
					const after = change.current;
					const turn = turnOf(after);
					if (before === void 0 || !isVisibleChatNode(before)) added.add(after.key);
					if (structureChanged(before, after)) {
						const previousTurn = before === void 0 ? void 0 : turnOf(before);
						if (previousTurn !== void 0) regroup.add(previousTurn);
						if (turn !== void 0) regroup.add(turn);
					}
					if (turn !== void 0) touch(turn).add(after.key);
				}
				for (const turn of input.changedTurns) touch(turn);
				const upserts = [];
				const removes = [];
				for (const turn of regroup) {
					const groups = this.turns.get(turn) ?? new TurnGroups(turn);
					const update = groups.rebuild(input, added);
					upserts.push(...update.upserts);
					removes.push(...update.removes);
					if (input.readTurn(turn).length === 0) this.turns.delete(turn);
					else this.turns.set(turn, groups);
				}
				for (const [turn, keys] of changed) if (!regroup.has(turn)) upserts.push(...this.turns.get(turn)?.refresh(input, keys) ?? []);
				const reordered = input.order !== this.order || regroup.size > 0;
				this.order = input.order;
				const installed = new Set(upserts.map((group) => group.key));
				this.pending = reordered || upserts.length > 0 || removes.length > 0 ? {
					...reordered ? { entries: this.rootEntries(input) } : {},
					groups: {
						kind: "apply",
						upserts,
						removes: removes.filter((key) => !installed.has(key))
					}
				} : null;
			}
			rootEntries(input) {
				return input.order.flatMap((key) => {
					const turn = readPosition(input, key).turn;
					if (turn === void 0) return [{
						kind: "node",
						key
					}];
					const groups = this.turns.get(turn);
					if (groups === void 0) throw new Error(`Chat grouping order is missing Turn ${turn}`);
					return groups.references(key);
				});
			}
			/**
			* Read pending output without advancing State.
			* @returns the repeatable update for the last input batch.
			*/
			output() {
				return this.pending;
			}
		};
		/** Chat's registered business grouping; presentation modes never enter its State. */
		const processGroupDefinition = {
			kind: "process-groups",
			target: "chat",
			create: () => new ProcessState(),
			update: (context, input) => {
				context.state.accept(input);
				return context.state;
			},
			buildGroups: (context) => context.state.output()
		};
		//#endregion
		//#region src/client/conversation-nodes/register.ts
		/**
		* Register the Chat business Definitions and target builder contributed by this package.
		* @param ctx - owning UI Conversation context.
		*/
		function registerConversationNodes(ctx) {
			registerInboxConversationNodes(ctx);
			registerMessageConversationNode(ctx);
			registerRequestPromptConversationNode(ctx);
			registerAssistantConversationNode(ctx);
			registerTurnProcess(ctx);
			registerToolConversationNode(ctx);
			registerCommandConversationNode(ctx);
			registerCompactionConversationNode(ctx);
			registerRetryConversationNode(ctx);
			registerTurnErrorConversationNode(ctx);
			registerTurnMaxTokensConversationNode(ctx);
			registerTurnTailConversationNode(ctx);
			registerUnknownConversationFallback(ctx);
			registerChatConversationView(ctx);
			ctx.uiConversation.groups.register(processGroupDefinition);
		}
		//#endregion
		//#region src/client/chat/QuotaNoticeHost.tsx
		/**
		* The frame-wide `shell.overlay` host for quota notices. It holds the one live
		* notice for the whole app, so leaving the Chat panel does not drop it. A
		* `shell.quota-notice` entry may replace the generic Toast for codes it claims.
		*/
		/**
		* @param props - the live notice, its dismissal, the chain outlet, and the locale seat.
		* @returns the notice on display, or null while none is live.
		*/
		function QuotaNoticeHost({ useNotice, dismissNotice, keepNoticeOpen, renderSlotChain, t }) {
			const notice = useNotice((current) => current);
			if (notice === null) return null;
			const owner = {
				code: notice.code,
				message: t("message.failure.quota"),
				dismiss: dismissNotice,
				keepOpen: keepNoticeOpen
			};
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(react.Fragment, { children: renderSlotChain("shell.quota-notice", owner, { fallback: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Toast, {
				text: owner.message,
				icon: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconWarningOutlineRegular, { size: 18 }),
				onDone: dismissNotice
			}) }) }, `quota-notice-${String(notice.seq)}`);
		}
		//#endregion
		//#region ../../../vendor/cosmokit/lib/index.js
		/** Return true when a value is `null` or `undefined`. */
		function isNullable(value) {
			return value === null || value === void 0;
		}
		/** Return true for non-array object values. */
		function isPlainObject(data) {
			return data && typeof data === "object" && !Array.isArray(data);
		}
		/** Filter object entries and return a new object. */
		function filterKeys(object, filter) {
			return Object.fromEntries(Object.entries(object).filter(([key, value]) => filter(key, value)));
		}
		/** Map object values while preserving the original key set. */
		function mapValues(object, transform) {
			return Object.fromEntries(Object.entries(object).map(([key, value]) => [key, transform(value, key)]));
		}
		/** Pick selected keys from an object, optionally including `undefined` values. */
		function pick(source, keys, forced) {
			if (!keys) return { ...source };
			const result = {};
			for (const key of keys) if (forced || source[key] !== void 0) result[key] = source[key];
			return result;
		}
		/** Shared config references used by schema validators and plugin runtimes. */
		const write = Symbol.for("cosmokit.volatile.write");
		function snapshot(value, ancestors = /* @__PURE__ */ new Set()) {
			if (typeof value === "function") throw new TypeError("volatile config cannot contain functions");
			if (value === null || typeof value !== "object") return value;
			if (ancestors.has(value)) throw new TypeError("volatile config cannot contain cycles");
			ancestors.add(value);
			try {
				if (Array.isArray(value)) return Object.freeze(value.map((item) => snapshot(item, ancestors)));
				if (Object.getPrototypeOf(value) !== Object.prototype && Object.getPrototypeOf(value) !== null) throw new TypeError("volatile config objects must be plain objects or arrays");
				return Object.freeze(Object.fromEntries(Object.entries(value).map(([key, item]) => [key, snapshot(item, ancestors)])));
			} finally {
				ancestors.delete(value);
			}
		}
		/**
		* Create a detached reference containing an immutable copy of the supplied data.
		* @param value - validated config data; class instances and functions are unsupported.
		* @returns a reference whose value is updated only by its owning runtime.
		*/
		function createVolatile(value) {
			let current = snapshot(value);
			return Object.freeze({
				get: () => current,
				[write]: (value) => {
					current = value;
				}
			});
		}
		/**
		* Identify references across ESM/CJS copies of the shared library.
		* @param value - a parsed config value.
		* @returns whether the value implements the shared reference protocol.
		*/
		function isVolatile(value) {
			return typeof value === "object" && value !== null && write in value;
		}
		/** Test values using `instanceof` with a `toStringTag` fallback. */
		function is(type, value) {
			if (arguments.length === 1) return (value) => is(type, value);
			return type in globalThis && value instanceof globalThis[type] || Object.prototype.toString.call(value).slice(8, -1) === type;
		}
		function isArrayBufferLike(value) {
			return is("ArrayBuffer", value) || is("SharedArrayBuffer", value);
		}
		function isArrayBufferSource(value) {
			return isArrayBufferLike(value) || ArrayBuffer.isView(value);
		}
		/** Binary source detection and base64/hex conversion helpers. */
		var Binary;
		(function(Binary) {
			Binary.is = isArrayBufferLike;
			Binary.isSource = isArrayBufferSource;
			function fromSource(source) {
				if (ArrayBuffer.isView(source)) return source.buffer.slice(source.byteOffset, source.byteOffset + source.byteLength);
				else return source;
			}
			Binary.fromSource = fromSource;
			function toBase64(source) {
				source = fromSource(source);
				if (typeof Buffer !== "undefined") return Buffer.from(source).toString("base64");
				let binary = "";
				const bytes = new Uint8Array(source);
				for (let i = 0; i < bytes.byteLength; i++) binary += String.fromCharCode(bytes[i]);
				return btoa(binary);
			}
			Binary.toBase64 = toBase64;
			function fromBase64(source) {
				if (typeof Buffer !== "undefined") return fromSource(Buffer.from(source, "base64"));
				return Uint8Array.from(atob(source), (c) => c.charCodeAt(0));
			}
			Binary.fromBase64 = fromBase64;
			function toHex(source) {
				source = fromSource(source);
				if (typeof Buffer !== "undefined") return Buffer.from(source).toString("hex");
				return Array.from(new Uint8Array(source), (byte) => byte.toString(16).padStart(2, "0")).join("");
			}
			Binary.toHex = toHex;
			function fromHex(source) {
				if (typeof Buffer !== "undefined") return fromSource(Buffer.from(source, "hex"));
				const hex = source.length % 2 === 0 ? source : source.slice(0, source.length - 1);
				const buffer = [];
				for (let i = 0; i < hex.length; i += 2) buffer.push(parseInt(`${hex[i]}${hex[i + 1]}`, 16));
				return Uint8Array.from(buffer).buffer;
			}
			Binary.fromHex = fromHex;
		})(Binary || (Binary = {}));
		Binary.fromBase64;
		Binary.toBase64;
		Binary.fromHex;
		Binary.toHex;
		/** Deep-clone common JavaScript values while preserving prototypes and cycles. */
		function clone(source, refs = /* @__PURE__ */ new Map()) {
			if (!source || typeof source !== "object") return source;
			if (is("Date", source)) return new Date(source.valueOf());
			if (is("RegExp", source)) return new RegExp(source.source, source.flags);
			if (isArrayBufferLike(source)) return source.slice(0);
			if (ArrayBuffer.isView(source)) return source.buffer.slice(source.byteOffset, source.byteOffset + source.byteLength);
			const cached = refs.get(source);
			if (cached) return cached;
			if (Array.isArray(source)) {
				const result = [];
				refs.set(source, result);
				source.forEach((value, index) => {
					result[index] = Reflect.apply(clone, null, [value, refs]);
				});
				return result;
			}
			const result = Object.create(Object.getPrototypeOf(source));
			refs.set(source, result);
			for (const key of Reflect.ownKeys(source)) {
				const descriptor = { ...Reflect.getOwnPropertyDescriptor(source, key) };
				if ("value" in descriptor) descriptor.value = Reflect.apply(clone, null, [descriptor.value, refs]);
				Reflect.defineProperty(result, key, descriptor);
			}
			return result;
		}
		/**
		* Compare values recursively, treating two volatile references as equal regardless of value.
		* Strict comparison distinguishes null/undefined, treats opaque objects by identity,
		* compares URLs by normalized href, treats array holes as undefined, and considers distinct cyclic structures unequal.
		* @param a - first value.
		* @param b - second value.
		* @param strict - whether to require strict data equality outside volatile references.
		* @returns whether the values compare equal.
		*/
		function deepEqual(a, b, strict) {
			const ancestors = /* @__PURE__ */ new Set();
			function compare(a, b) {
				if (a === b) return true;
				if (isVolatile(a) || isVolatile(b)) return isVolatile(a) && isVolatile(b);
				if (!strict && isNullable(a) && isNullable(b)) return true;
				if (typeof a !== typeof b || typeof a !== "object" || !a || !b) return false;
				if (ancestors.has(a)) return false;
				function check(test, then) {
					return test(a) ? test(b) ? then(a, b) : false : test(b) ? false : void 0;
				}
				ancestors.add(a);
				try {
					return check(Array.isArray, (a, b) => {
						if (a.length !== b.length) return false;
						for (let index = 0; index < a.length; index++) if (!compare(a[index], b[index])) return false;
						return true;
					}) ?? check(is("Date"), (a, b) => a.valueOf() === b.valueOf()) ?? check(is("URL"), (a, b) => a.href === b.href) ?? check(is("RegExp"), (a, b) => a.source === b.source && a.flags === b.flags) ?? check(isArrayBufferLike, (a, b) => {
						if (a.byteLength !== b.byteLength) return false;
						const viewA = new Uint8Array(a);
						const viewB = new Uint8Array(b);
						for (let i = 0; i < viewA.length; i++) if (viewA[i] !== viewB[i]) return false;
						return true;
					}) ?? ((!strict || [a, b].every((value) => Object.getPrototypeOf(value) === Object.prototype || Object.getPrototypeOf(value) === null)) && Object.keys({
						...a,
						...b
					}).every((key) => compare(a[key], b[key])));
				} finally {
					ancestors.delete(a);
				}
			}
			return compare(a, b);
		}
		/** Time constants plus parsing and formatting helpers. */
		var Time;
		(function(Time) {
			Time.millisecond = 1;
			Time.second = 1e3;
			Time.minute = Time.second * 60;
			Time.hour = Time.minute * 60;
			Time.day = Time.hour * 24;
			Time.week = Time.day * 7;
			let timezoneOffset = (/* @__PURE__ */ new Date()).getTimezoneOffset();
			function setTimezoneOffset(offset) {
				timezoneOffset = offset;
			}
			Time.setTimezoneOffset = setTimezoneOffset;
			function getTimezoneOffset() {
				return timezoneOffset;
			}
			Time.getTimezoneOffset = getTimezoneOffset;
			function getDateNumber(date = /* @__PURE__ */ new Date(), offset) {
				if (typeof date === "number") date = new Date(date);
				if (offset === void 0) offset = timezoneOffset;
				return Math.floor((date.valueOf() / Time.minute - offset) / 1440);
			}
			Time.getDateNumber = getDateNumber;
			function fromDateNumber(value, offset) {
				const date = new Date(value * Time.day);
				if (offset === void 0) offset = timezoneOffset;
				return new Date(+date + offset * Time.minute);
			}
			Time.fromDateNumber = fromDateNumber;
			const numeric = /\d+(?:\.\d+)?/.source;
			const timeRegExp = new RegExp(`^${[
				"w(?:eek(?:s)?)?",
				"d(?:ay(?:s)?)?",
				"h(?:our(?:s)?)?",
				"m(?:in(?:ute)?(?:s)?)?",
				"s(?:ec(?:ond)?(?:s)?)?"
			].map((unit) => `(${numeric}${unit})?`).join("")}$`);
			function parseTime(source) {
				const capture = timeRegExp.exec(source);
				if (!capture) return 0;
				return (parseFloat(capture[1]) * Time.week || 0) + (parseFloat(capture[2]) * Time.day || 0) + (parseFloat(capture[3]) * Time.hour || 0) + (parseFloat(capture[4]) * Time.minute || 0) + (parseFloat(capture[5]) * Time.second || 0);
			}
			Time.parseTime = parseTime;
			function parseDate(date) {
				const parsed = parseTime(date);
				if (parsed) date = Date.now() + parsed;
				else if (/^\d{1,2}(:\d{1,2}){1,2}$/.test(date)) date = `${(/* @__PURE__ */ new Date()).toLocaleDateString()}-${date}`;
				else if (/^\d{1,2}-\d{1,2}-\d{1,2}(:\d{1,2}){1,2}$/.test(date)) date = `${(/* @__PURE__ */ new Date()).getFullYear()}-${date}`;
				return date ? new Date(date) : /* @__PURE__ */ new Date();
			}
			Time.parseDate = parseDate;
			function format(ms) {
				const abs = Math.abs(ms);
				if (abs >= Time.day - Time.hour / 2) return Math.round(ms / Time.day) + "d";
				else if (abs >= Time.hour - Time.minute / 2) return Math.round(ms / Time.hour) + "h";
				else if (abs >= Time.minute - Time.second / 2) return Math.round(ms / Time.minute) + "m";
				else if (abs >= Time.second) return Math.round(ms / Time.second) + "s";
				return ms + "ms";
			}
			Time.format = format;
			function toDigits(source, length = 2) {
				return source.toString().padStart(length, "0");
			}
			Time.toDigits = toDigits;
			function template(template, time = /* @__PURE__ */ new Date()) {
				return template.replace("yyyy", time.getFullYear().toString()).replace("yy", time.getFullYear().toString().slice(2)).replace("MM", toDigits(time.getMonth() + 1)).replace("dd", toDigits(time.getDate())).replace("hh", toDigits(time.getHours())).replace("mm", toDigits(time.getMinutes())).replace("ss", toDigits(time.getSeconds())).replace("SSS", toDigits(time.getMilliseconds(), 3));
			}
			Time.template = template;
		})(Time || (Time = {}));
		//#endregion
		//#region ../../../vendor/schemastery/src/index.ts
		const kSchema = Symbol.for("schemastery");
		const kValidationError = Symbol.for("ValidationError");
		globalThis.__schemastery_index__ ??= 0;
		globalThis.__schemastery_refs__ = void 0;
		var ValidationError = class extends TypeError {
			constructor(message, options) {
				let prefix = "$";
				for (const segment of options.path || []) if (typeof segment === "string") prefix += "." + segment;
				else if (typeof segment === "number") prefix += "[" + segment + "]";
				else if (typeof segment === "symbol") prefix += `[Symbol(${segment.toString()})]`;
				if (prefix.startsWith(".")) prefix = prefix.slice(1);
				super((prefix === "$" ? "" : `${prefix} `) + message);
				this.options = options;
				this.name = "ValidationError";
			}
			static is(error) {
				return !!error?.[kValidationError];
			}
		};
		Object.defineProperty(ValidationError.prototype, kValidationError, { value: true });
		const Schema = function(options) {
			const schema = function(data, options = {}) {
				return Schema.resolve(data, schema, options)[0];
			};
			if (options.refs) {
				const refs = mapValues(options.refs, (options) => new Schema(options));
				const getRef = (uid) => refs[uid];
				for (const key in refs) {
					const options = refs[key];
					options.sKey = getRef(options.sKey);
					options.inner = getRef(options.inner);
					options.list = options.list && options.list.map(getRef);
					options.dict = options.dict && mapValues(options.dict, getRef);
				}
				return refs[options.uid];
			}
			Object.assign(schema, options);
			if (typeof schema.callback === "string") try {
				schema.callback = new Function("return " + schema.callback)();
			} catch {}
			Object.defineProperty(schema, "uid", { value: globalThis.__schemastery_index__++ });
			Object.setPrototypeOf(schema, Schema.prototype);
			schema.meta ||= {};
			schema.toString = schema.toString.bind(schema);
			return schema;
		};
		Schema.prototype = Object.create(Function.prototype);
		Schema.prototype[kSchema] = true;
		Object.defineProperty(Schema.prototype, "~standard", { get() {
			return {
				version: 1,
				vendor: "schemastery",
				validate: (value) => {
					try {
						return { value: Schema.resolve(value, this, {})[0] };
					} catch (error) {
						if (ValidationError.is(error)) return { issues: [{
							message: error.message,
							path: error.options.path
						}] };
						throw error;
					}
				}
			};
		} });
		Schema.ValidationError = ValidationError;
		Schema.prototype.toJSON = function toJSON() {
			if (globalThis.__schemastery_refs__) {
				globalThis.__schemastery_refs__[this.uid] ??= JSON.parse(JSON.stringify({ ...this }));
				return this.uid;
			}
			globalThis.__schemastery_refs__ = { [this.uid]: { ...this } };
			globalThis.__schemastery_refs__[this.uid] = JSON.parse(JSON.stringify({ ...this }));
			const result = {
				uid: this.uid,
				refs: globalThis.__schemastery_refs__
			};
			globalThis.__schemastery_refs__ = void 0;
			return result;
		};
		Schema.prototype.set = function set(key, value) {
			this.dict[key] = value;
			return this;
		};
		Schema.prototype.push = function push(value) {
			this.list.push(value);
			return this;
		};
		function mergeDesc(original, messages) {
			const result = typeof original === "string" ? { "": original } : { ...original };
			for (const locale in messages) {
				const value = messages[locale];
				if (value?.$description || value?.$desc) result[locale] = value.$description || value.$desc;
				else if (typeof value === "string") result[locale] = value;
			}
			return result;
		}
		function getInner(value) {
			return value?.$value ?? value?.$inner;
		}
		function extractKeys(data) {
			return filterKeys(data ?? {}, (key) => !key.startsWith("$"));
		}
		Schema.prototype.i18n = function i18n(messages) {
			const schema = Schema(this);
			const desc = mergeDesc(schema.meta.description, messages);
			if (Object.keys(desc).length) schema.meta.description = desc;
			if (schema.dict) schema.dict = mapValues(schema.dict, (inner, key) => {
				return inner.i18n(mapValues(messages, (data) => getInner(data)?.[key] ?? data?.[key]));
			});
			if (schema.list) schema.list = schema.list.map((inner, index) => {
				return inner.i18n(mapValues(messages, (data = {}) => {
					if (Array.isArray(getInner(data))) return getInner(data)[index];
					if (Array.isArray(data)) return data[index];
					return extractKeys(data);
				}));
			});
			if (schema.inner) schema.inner = schema.inner.i18n(mapValues(messages, (data) => {
				if (getInner(data)) return getInner(data);
				return extractKeys(data);
			}));
			if (schema.sKey) schema.sKey = schema.sKey.i18n(mapValues(messages, (data) => data?.$key));
			return schema;
		};
		Schema.prototype.extra = function extra(key, value) {
			const schema = Schema(this);
			schema.meta = {
				...schema.meta,
				[key]: value
			};
			return schema;
		};
		for (const key of [
			"required",
			"disabled",
			"collapse",
			"hidden",
			"loose"
		]) Object.assign(Schema.prototype, { [key](value = true) {
			const schema = Schema(this);
			schema.meta = {
				...schema.meta,
				[key]: value
			};
			return schema;
		} });
		Schema.prototype.deprecated = function deprecated() {
			const schema = Schema(this);
			schema.meta.badges ||= [];
			schema.meta.badges.push({
				text: "deprecated",
				type: "danger"
			});
			return schema;
		};
		Schema.prototype.experimental = function experimental() {
			const schema = Schema(this);
			schema.meta.badges ||= [];
			schema.meta.badges.push({
				text: "experimental",
				type: "warning"
			});
			return schema;
		};
		Schema.prototype.pattern = function pattern(regexp) {
			const schema = Schema(this);
			const pattern = pick(regexp, ["source", "flags"]);
			schema.meta = {
				...schema.meta,
				pattern
			};
			return schema;
		};
		Schema.prototype.simplify = function simplify(value) {
			if (isVolatile(value)) value = value.get();
			if (deepEqual(value, this.meta.default, this.type === "dict")) return null;
			if (isNullable(value)) return value;
			if (this.type === "object" || this.type === "dict") {
				const result = {};
				for (const key in value) {
					const item = (this.type === "object" ? this.dict[key] : this.inner)?.simplify(value[key]);
					if (this.type === "dict" || !isNullable(item)) result[key] = item;
				}
				if (deepEqual(result, this.meta.default, this.type === "dict")) return null;
				return result;
			} else if (this.type === "array" || this.type === "tuple") {
				const result = [];
				value.forEach((value, index) => {
					const schema = this.type === "array" ? this.inner : this.list[index];
					const item = schema ? schema.simplify(value) : value;
					result.push(item);
				});
				return result;
			} else if (this.type === "intersect") {
				const result = {};
				for (const item of this.list) Object.assign(result, item.simplify(value));
				return result;
			} else if (this.type === "union") for (const schema of this.list) try {
				Schema.resolve(value, schema, {});
				return schema.simplify(value);
			} catch {}
			return value;
		};
		Schema.prototype.toString = function toString(inline) {
			return formatters[this.type]?.(this, inline) ?? `Schema<${this.type}>`;
		};
		Schema.prototype.role = function role(role, extra) {
			const schema = Schema(this);
			schema.meta = {
				...schema.meta,
				role,
				extra
			};
			return schema;
		};
		for (const key of [
			"default",
			"link",
			"comment",
			"description",
			"max",
			"min",
			"step"
		]) Object.assign(Schema.prototype, { [key](value) {
			const schema = Schema(this);
			schema.meta = {
				...schema.meta,
				[key]: value
			};
			return schema;
		} });
		Schema.prototype.volatile = function volatile() {
			if (this.meta.volatile) throw new TypeError("volatile schema is already wrapped");
			return this.extra("volatile", true);
		};
		const resolvers = {};
		const checkedVolatile = Symbol("checked-volatile-schema");
		function validateVolatileSchema(schema, path = [], blocked = false, seen = /* @__PURE__ */ new Map()) {
			const states = seen.get(schema) ?? /* @__PURE__ */ new Set();
			if (states.has(blocked)) return;
			states.add(blocked);
			seen.set(schema, states);
			if (schema.meta?.volatile && blocked) throw new ValidationError("volatile fields require a fixed object path without an enclosing volatile field", { path });
			const nested = blocked || !!schema.meta?.volatile;
			if (schema.dict) for (const [key, child] of Object.entries(schema.dict)) validateVolatileSchema(child, [...path, key], nested, seen);
			if (schema.sKey) validateVolatileSchema(schema.sKey, [...path, "<key>"], true, seen);
			if (schema.inner && (schema.type !== "lazy" || schema.inner[kSchema])) validateVolatileSchema(schema.inner, [...path, "*"], true, seen);
			if (schema.list) for (let index = 0; index < schema.list.length; index++) validateVolatileSchema(schema.list[index], [...path, String(index)], true, seen);
		}
		Schema.extend = function extend(type, resolve) {
			resolvers[type] = resolve;
		};
		Schema.resolve = function resolve(data, schema, options = {}, strict = false) {
			if (!schema) return [data];
			if (!options[checkedVolatile]) {
				validateVolatileSchema(schema, options.path);
				options = {
					...options,
					[checkedVolatile]: true
				};
			}
			if (schema.meta?.volatile) {
				const inner = Schema(schema);
				inner.meta = {
					...schema.meta,
					volatile: false
				};
				const [value, adapted] = Schema.resolve(data, inner, options, strict);
				try {
					return [createVolatile(value), adapted];
				} catch (error) {
					throw new ValidationError(error instanceof Error ? error.message : String(error), options);
				}
			}
			if (options.ignore?.(data, schema)) return [data];
			if (isNullable(data) && schema.type !== "lazy") {
				if (schema.meta.required) throw new ValidationError(`missing required value`, options);
				let current = schema;
				let fallback = schema.meta.default;
				while (current?.type === "intersect" && isNullable(fallback)) {
					current = current.list[0];
					fallback = current?.meta.default;
				}
				if (isNullable(fallback)) return [data];
				data = clone(fallback);
			}
			const callback = resolvers[schema.type];
			if (!callback) throw new ValidationError(`unsupported type "${schema.type}"`, options);
			try {
				return callback(data, schema, options, strict);
			} catch (error) {
				if (!schema.meta.loose) throw error;
				return [schema.meta.default];
			}
		};
		Schema.from = function from(source) {
			if (isNullable(source)) return Schema.any();
			else if ([
				"string",
				"number",
				"boolean"
			].includes(typeof source)) return Schema.const(source).required();
			else if (source[kSchema]) return source;
			else if (typeof source === "function") switch (source) {
				case String: return Schema.string().required();
				case Number: return Schema.number().required();
				case Boolean: return Schema.boolean().required();
				case Function: return Schema.function().required();
				default: return Schema.is(source).required();
			}
			else throw new TypeError(`cannot infer schema from ${source}`);
		};
		Schema.lazy = function lazy(builder) {
			const toJSON = () => {
				if (!schema.inner[kSchema]) {
					schema.inner = schema.builder();
					schema.inner.meta = {
						...schema.meta,
						...schema.inner.meta
					};
				}
				return schema.inner.toJSON();
			};
			const schema = new Schema({
				type: "lazy",
				builder,
				inner: { toJSON }
			});
			return schema;
		};
		Schema.natural = function natural() {
			return Schema.number().step(1).min(0);
		};
		Schema.percent = function percent() {
			return Schema.number().step(.01).min(0).max(1).role("slider");
		};
		Schema.date = function date() {
			return Schema.union([Schema.is(Date), Schema.transform(Schema.string().role("datetime"), (value, options) => {
				const date = new Date(value);
				if (isNaN(+date)) throw new ValidationError(`invalid date "${value}"`, options);
				return date;
			}, true)]);
		};
		Schema.regExp = function regExp(flag = "") {
			return Schema.union([Schema.is(RegExp), Schema.transform(Schema.string().role("regexp", { flag }), (value, options) => {
				try {
					return new RegExp(value, flag);
				} catch (e) {
					throw new ValidationError(e.message, options);
				}
			}, true)]);
		};
		Schema.arrayBuffer = function arrayBuffer(encoding) {
			return Schema.union([
				Schema.is(ArrayBuffer),
				Schema.is(SharedArrayBuffer),
				Schema.transform(Schema.any(), (value, options) => {
					if (Binary.isSource(value)) return Binary.fromSource(value);
					throw new ValidationError(`expected ArrayBufferSource but got ${value}`, options);
				}, true),
				...encoding ? [Schema.transform(Schema.string(), (value, options) => {
					try {
						return encoding === "base64" ? Binary.fromBase64(value) : Binary.fromHex(value);
					} catch (e) {
						throw new ValidationError(e.message, options);
					}
				}, true)] : []
			]);
		};
		Schema.extend("lazy", (data, schema, options, strict) => {
			if (!schema.inner[kSchema]) {
				schema.inner = schema.builder();
				schema.inner.meta = {
					...schema.meta,
					...schema.inner.meta
				};
				validateVolatileSchema(schema.inner, options.path, true);
			}
			return Schema.resolve(data, schema.inner, options, strict);
		});
		Schema.extend("any", (data) => {
			return [data];
		});
		Schema.extend("never", (data, _, options) => {
			throw new ValidationError(`expected nullable but got ${data}`, options);
		});
		Schema.extend("const", (data, { value }, options) => {
			if (deepEqual(data, value)) return [value];
			throw new ValidationError(`expected ${value} but got ${data}`, options);
		});
		function checkWithinRange(data, meta, description, options, skipMin = false) {
			const { max = Infinity, min = -Infinity } = meta;
			if (data > max) throw new ValidationError(`expected ${description} <= ${max} but got ${data}`, options);
			if (data < min && !skipMin) throw new ValidationError(`expected ${description} >= ${min} but got ${data}`, options);
		}
		Schema.extend("string", (data, { meta }, options) => {
			if (typeof data !== "string") throw new ValidationError(`expected string but got ${data}`, options);
			if (meta.pattern) {
				const regexp = new RegExp(meta.pattern.source, meta.pattern.flags);
				if (!regexp.test(data)) throw new ValidationError(`expect string to match regexp ${regexp}`, options);
			}
			checkWithinRange(data.length, meta, "string length", options);
			return [data];
		});
		function decimalShift(data, digits) {
			const str = data.toString();
			if (str.includes("e")) return data * Math.pow(10, digits);
			const index = str.indexOf(".");
			if (index === -1) return data * Math.pow(10, digits);
			const frac = str.slice(index + 1);
			const integer = str.slice(0, index);
			if (frac.length <= digits) return +(integer + frac.padEnd(digits, "0"));
			return +(integer + frac.slice(0, digits) + "." + frac.slice(digits));
		}
		function isMultipleOf(data, min, step) {
			step = Math.abs(step);
			if (!/^\d+\.\d+$/.test(step.toString())) return (data - min) % step === 0;
			const index = step.toString().indexOf(".");
			const digits = step.toString().slice(index + 1).length;
			return Math.abs(decimalShift(data, digits) - decimalShift(min, digits)) % decimalShift(step, digits) === 0;
		}
		Schema.extend("number", (data, { meta }, options) => {
			if (typeof data !== "number") throw new ValidationError(`expected number but got ${data}`, options);
			checkWithinRange(data, meta, "number", options);
			const { step } = meta;
			if (step && !isMultipleOf(data, meta.min ?? 0, step)) throw new ValidationError(`expected number multiple of ${step} but got ${data}`, options);
			return [data];
		});
		Schema.extend("boolean", (data, _, options) => {
			if (typeof data === "boolean") return [data];
			throw new ValidationError(`expected boolean but got ${data}`, options);
		});
		Schema.extend("bitset", (data, { bits, meta }, options) => {
			let value = 0, keys = [];
			if (typeof data === "number") {
				value = data;
				for (const key in bits) if (data & bits[key]) keys.push(key);
			} else if (Array.isArray(data)) {
				keys = data;
				for (const key of keys) {
					if (typeof key !== "string") throw new ValidationError(`expected string but got ${key}`, options);
					if (key in bits) value |= bits[key];
				}
			} else throw new ValidationError(`expected number or array but got ${data}`, options);
			if (value === meta.default) return [value];
			return [value, keys];
		});
		Schema.extend("function", (data, _, options) => {
			if (typeof data === "function") return [data];
			throw new ValidationError(`expected function but got ${data}`, options);
		});
		Schema.extend("is", (data, { constructor }, options) => {
			if (typeof constructor === "function") {
				if (data instanceof constructor) return [data];
				throw new ValidationError(`expected ${constructor.name} but got ${data}`, options);
			} else {
				if (isNullable(data)) throw new ValidationError(`expected ${constructor} but got ${data}`, options);
				let prototype = Object.getPrototypeOf(data);
				while (prototype) {
					if (prototype.constructor?.name === constructor) return [data];
					prototype = Object.getPrototypeOf(prototype);
				}
				throw new ValidationError(`expected ${constructor} but got ${data}`, options);
			}
		});
		function property(data, key, schema, options) {
			try {
				const [value, adapted] = Schema.resolve(data[key], schema, {
					...options,
					path: [...options.path || [], key]
				});
				if (adapted !== void 0) data[key] = adapted;
				return value;
			} catch (e) {
				if (!options?.autofix) throw e;
				delete data[key];
				return schema.meta.volatile ? createVolatile(schema.meta.default) : schema.meta.default;
			}
		}
		Schema.extend("array", (data, { inner, meta }, options) => {
			if (!Array.isArray(data)) throw new ValidationError(`expected array but got ${data}`, options);
			checkWithinRange(data.length, meta, "array length", options, !isNullable(inner.meta.default));
			return [data.map((_, index) => property(data, index, inner, options))];
		});
		Schema.extend("dict", (data, { inner, sKey }, options, strict) => {
			if (!isPlainObject(data)) throw new ValidationError(`expected object but got ${data}`, options);
			const result = {};
			for (const key in data) {
				let rKey;
				try {
					rKey = Schema.resolve(key, sKey, options)[0];
				} catch (error) {
					if (strict) continue;
					throw error;
				}
				result[rKey] = property(data, key, inner, options);
				data[rKey] = data[key];
				if (key !== rKey) delete data[key];
			}
			return [result];
		});
		Schema.extend("tuple", (data, { list }, options, strict) => {
			if (!Array.isArray(data)) throw new ValidationError(`expected array but got ${data}`, options);
			const result = list.map((inner, index) => property(data, index, inner, options));
			if (strict) return [result];
			result.push(...data.slice(list.length));
			return [result];
		});
		function merge(result, data) {
			for (const key in data) {
				if (key in result) continue;
				result[key] = data[key];
			}
		}
		Schema.extend("object", (data, { dict }, options, strict) => {
			if (!isPlainObject(data)) throw new ValidationError(`expected object but got ${data}`, options);
			const result = {};
			for (const key in dict) {
				const value = property(data, key, dict[key], options);
				if (!isNullable(value) || key in data) result[key] = value;
			}
			if (!strict) merge(result, data);
			return [result];
		});
		Schema.extend("union", (data, { list, toString }, options, strict) => {
			const messages = [];
			for (const inner of list) try {
				return Schema.resolve(data, inner, options, strict);
			} catch (error) {
				messages.push(error);
			}
			throw new ValidationError(`expected ${toString()} but got ${JSON.stringify(data)}`, options);
		});
		Schema.extend("intersect", (data, { list, toString }, options, strict) => {
			if (!list.length) return [data];
			let result;
			for (const inner of list) {
				const value = Schema.resolve(data, inner, options, true)[0];
				if (isNullable(value)) continue;
				if (isNullable(result)) result = value;
				else if (typeof result !== typeof value) throw new ValidationError(`expected ${toString()} but got ${JSON.stringify(data)}`, options);
				else if (typeof value === "object") merge(result ??= {}, value);
				else if (result !== value) throw new ValidationError(`expected ${toString()} but got ${JSON.stringify(data)}`, options);
			}
			if (!strict && isPlainObject(data)) merge(result, data);
			return [result];
		});
		Schema.extend("transform", (data, { inner, callback, preserve }, options) => {
			const [result, adapted = data] = Schema.resolve(data, inner, options, true);
			if (preserve) return [callback(result)];
			else return [callback(result), callback(adapted)];
		});
		const formatters = {};
		function defineMethod(name, keys, format) {
			formatters[name] = format;
			Object.assign(Schema, { [name](...args) {
				const schema = new Schema({ type: name });
				keys.forEach((key, index) => {
					switch (key) {
						case "sKey":
							schema.sKey = args[index] ?? Schema.string();
							break;
						case "inner":
							schema.inner = Schema.from(args[index]);
							break;
						case "list":
							schema.list = args[index].map(Schema.from);
							break;
						case "dict":
							schema.dict = mapValues(args[index], Schema.from);
							break;
						case "bits":
							schema.bits = {};
							for (const key in args[index]) {
								if (typeof args[index][key] !== "number") continue;
								schema.bits[key] = args[index][key];
							}
							break;
						case "callback": {
							const callback = schema.callback = args[index];
							callback["toJSON"] ||= () => callback.toString();
							break;
						}
						case "constructor": {
							const constructor = schema.constructor = args[index];
							if (typeof constructor === "function") constructor["toJSON"] ||= () => constructor["name"];
							break;
						}
						default: schema[key] = args[index];
					}
				});
				if (name === "object" || name === "dict") schema.meta.default = {};
				else if (name === "array" || name === "tuple") schema.meta.default = [];
				else if (name === "bitset") schema.meta.default = 0;
				return schema;
			} });
		}
		defineMethod("is", ["constructor"], ({ constructor }) => {
			if (typeof constructor === "function") return constructor.name;
			else return constructor;
		});
		defineMethod("any", [], () => "any");
		defineMethod("never", [], () => "never");
		defineMethod("const", ["value"], ({ value }) => typeof value === "string" ? JSON.stringify(value) : value);
		defineMethod("string", [], () => "string");
		defineMethod("number", [], () => "number");
		defineMethod("boolean", [], () => "boolean");
		defineMethod("bitset", ["bits"], () => "bitset");
		defineMethod("function", [], () => "function");
		defineMethod("array", ["inner"], ({ inner }) => `${inner.toString(true)}[]`);
		defineMethod("dict", ["inner", "sKey"], ({ inner, sKey }) => `{ [key: ${sKey.toString()}]: ${inner.toString()} }`);
		defineMethod("tuple", ["list"], ({ list }) => `[${list.map((inner) => inner.toString()).join(", ")}]`);
		defineMethod("object", ["dict"], ({ dict }) => {
			if (Object.keys(dict).length === 0) return "{}";
			return `{ ${Object.entries(dict).map(([key, inner]) => {
				return `${key}${inner.meta.required ? "" : "?"}: ${inner.toString()}`;
			}).join(", ")} }`;
		});
		defineMethod("union", ["list"], ({ list }, inline) => {
			const result = list.map(({ toString: format }) => format()).join(" | ");
			return inline ? `(${result})` : result;
		});
		defineMethod("intersect", ["list"], ({ list }) => {
			return `${list.map((inner) => inner.toString(true)).join(" & ")}`;
		});
		defineMethod("transform", [
			"inner",
			"callback",
			"preserve"
		], ({ inner }, isInner) => inner.toString(isInner));
		//#endregion
		//#region src/chat-settings.ts
		/** Chat display preferences stored in the Host user-settings document. */
		/** Settings namespace owned by the Chat target. */
		const CHAT_SETTINGS_NAMESPACE = "ui-chat";
		/** Field carrying the work-details presentation mode. */
		const TRANSCRIPT_VIEW_FIELD = "transcriptView";
		/** Work-details presentation modes a user can choose. */
		const TRANSCRIPT_VIEW_MODES = [
			"compact",
			"standard",
			"detailed",
			"verbose"
		];
		/**
		* Saved value from the two-mode generation of this setting. Read as `standard`;
		* never offered as a choice and never written back.
		*/
		const LEGACY_TRANSCRIPT_VIEW_MODE = "normal";
		/** Saved `expanded` values read as `detailed`, without being offered or written back. */
		const LEGACY_EXPANDED_TRANSCRIPT_VIEW_MODE = "expanded";
		/** Every value the durable field accepts: current modes plus legacy saved values. */
		const TRANSCRIPT_VIEW_SETTING_VALUES = [
			...TRANSCRIPT_VIEW_MODES,
			LEGACY_TRANSCRIPT_VIEW_MODE,
			LEGACY_EXPANDED_TRANSCRIPT_VIEW_MODE
		];
		/** Standard process summaries for users without an explicit preference. */
		const DEFAULT_TRANSCRIPT_VIEW_MODE = "standard";
		/** Performance and usage detail levels accepted by user settings. */
		const PERFORMANCE_USAGE_MODES = ["compact", "detailed"];
		/** Preserve detailed accounting for users without an explicit preference. */
		const DEFAULT_PERFORMANCE_USAGE = "detailed";
		/** Durable Chat schema; also the wire envelope the browser scope validates against. */
		const ChatSettingsFields = {
			linkOpening: Schema.union(["sidebar", "new-tab"]).default("sidebar"),
			performanceUsage: Schema.union([...PERFORMANCE_USAGE_MODES]).default(DEFAULT_PERFORMANCE_USAGE),
			[TRANSCRIPT_VIEW_FIELD]: Schema.union([...TRANSCRIPT_VIEW_SETTING_VALUES]).default(DEFAULT_TRANSCRIPT_VIEW_MODE).loose()
		};
		Schema.object(ChatSettingsFields);
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-chat/src/client/settings/PreferenceRow.module.css.mjs
		const css = ".FAaaUW_row{border-bottom:.5px solid var(--dsw-alias-border-l2);align-items:center;gap:8px;padding:16px 0;display:flex}.FAaaUW_rowText{flex-direction:column;flex:1;gap:4px;min-width:0;padding-right:48px;display:flex}.FAaaUW_title{color:var(--dsw-alias-label-primary);font-size:14px;font-weight:400;line-height:22px}.FAaaUW_desc{color:var(--dsw-alias-label-tertiary);font-size:12px;font-weight:400;line-height:18px}.FAaaUW_selector{border-radius:var(--dsw-radius-md);background:var(--dsw-alias-bg-module-platform);height:36px;font:inherit;color:var(--dsw-alias-label-primary);cursor:pointer;border:none;align-items:center;gap:12px;padding:0 14px;font-size:14px;line-height:22px;display:inline-flex}.FAaaUW_selector:hover{background:var(--dsw-alias-interactive-bg-hover)}.FAaaUW_chevron{flex:none}";
		const tagId = "@deepseek-ai/dsh-client-ui-chat/PreferenceRow.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-chat";
			tag.dataset.pluginCss = tagId;
			tag.textContent = css;
			document.head.appendChild(tag);
		}
		var PreferenceRow_module_css_default = {
			"chevron": "FAaaUW_chevron",
			"desc": "FAaaUW_desc",
			"row": "FAaaUW_row",
			"rowText": "FAaaUW_rowText",
			"selector": "FAaaUW_selector",
			"title": "FAaaUW_title"
		};
		//#endregion
		//#region src/client/settings/PreferenceRow.tsx
		/** Localized two-column selector shared by Chat preference rows. */
		/**
		* Render a preference label and its menu; selection restores focus before publishing the new value.
		* @param props - localized copy, selected value, choices, and mutation callback.
		* @returns the settings row.
		*/
		function PreferenceRow({ title, description, value, selectedLabel, options, onSelect }) {
			const [open, setOpen] = (0, react.useState)(false);
			const selectorRef = (0, react.useRef)(null);
			const closeMenu = () => {
				setOpen(false);
			};
			const selectMode = (id) => {
				selectorRef.current?.focus({ preventScroll: true });
				closeMenu();
				onSelect(id);
			};
			const selector = /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
				ref: selectorRef,
				type: "button",
				className: PreferenceRow_module_css_default.selector,
				"aria-haspopup": "menu",
				"aria-expanded": open,
				onClick: () => {
					setOpen((value) => !value);
				},
				children: [selectedLabel, /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronDownOutlineRegular, { className: PreferenceRow_module_css_default.chevron })]
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: PreferenceRow_module_css_default.row,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: PreferenceRow_module_css_default.rowText,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						className: PreferenceRow_module_css_default.title,
						children: title
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						className: PreferenceRow_module_css_default.desc,
						children: description
					})]
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Menu, {
					open,
					onClose: closeMenu,
					items: options,
					selectedId: value,
					onSelect: selectMode,
					align: "end",
					portal: true,
					anchor: selector
				})]
			});
		}
		//#endregion
		//#region src/client/settings/TranscriptViewRow.tsx
		const LABELS = {
			compact: "settings.transcript.compact",
			standard: "settings.transcript.standard",
			detailed: "settings.transcript.detailed",
			verbose: "settings.transcript.verbose"
		};
		/**
		* Render the work-details mode selector.
		* @param props - composed Settings slot props.
		* @returns the preference row.
		*/
		function TranscriptViewRow({ useTranscriptView, setTranscriptView, t }) {
			const mode = useTranscriptView((value) => value);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(PreferenceRow, {
				title: t("settings.transcript.title"),
				description: t("settings.transcript.description"),
				value: mode,
				selectedLabel: t(LABELS[mode]),
				options: TRANSCRIPT_VIEW_MODES.map((id) => ({
					id,
					label: t(LABELS[id])
				})),
				onSelect: (value) => {
					setTranscriptView(value);
				}
			});
		}
		//#endregion
		//#region src/client/transcript-view.ts
		/** Host-backed work-details presentation policy. */
		/** Live work-details preference consumed by Chat and its Settings row. */
		var TranscriptViewPolicy = class {
			host;
			unsubscribe;
			/** Reactive current mode; defaults to Standard before Host settings arrive. */
			mode = (0, _deepseek_ai_dsh_client_store.createSnapshotStore)(DEFAULT_TRANSCRIPT_VIEW_MODE);
			/**
			* @param host - durable Chat settings scope.
			*/
			constructor(host) {
				this.host = host;
				this.unsubscribe = host.subscribe(() => {
					this.adopt();
				});
				this.adopt();
			}
			/** Release the accepted-value subscription. */
			dispose() {
				this.unsubscribe();
			}
			/**
			* Publish and persist one explicit user choice.
			* @param mode - Compact, Standard, Detailed, or Verbose work details.
			*/
			setMode(mode) {
				if (this.mode.getSnapshot() === mode) return;
				this.mode.set(mode);
				this.host.set(TRANSCRIPT_VIEW_FIELD, mode);
			}
			/** Adopt the latest accepted Host section without writing it back. */
			adopt() {
				const section = this.host.getSnapshot().value;
				if (section === void 0) return;
				const saved = section.transcriptView;
				const mode = saved === "normal" ? "standard" : saved === "expanded" ? "detailed" : saved;
				if (this.mode.getSnapshot() !== mode) this.mode.set(mode);
			}
		};
		//#endregion
		//#region src/client/presentation-policy.ts
		const POLICIES = {
			compact: {
				mode: "compact",
				foldCompletedTurns: true,
				stepGrouping: "collapsed",
				liveProcessDetail: false,
				settledReasoningPreview: false
			},
			standard: {
				mode: "standard",
				foldCompletedTurns: true,
				stepGrouping: "collapsed",
				liveProcessDetail: true,
				settledReasoningPreview: true
			},
			detailed: {
				mode: "detailed",
				foldCompletedTurns: true,
				stepGrouping: "history",
				liveProcessDetail: true,
				settledReasoningPreview: true
			},
			verbose: {
				mode: "verbose",
				foldCompletedTurns: false,
				stepGrouping: "none",
				liveProcessDetail: false,
				settledReasoningPreview: true
			}
		};
		/**
		* Derive a policy observable from the mode observable without a subscription of
		* its own: reads are a table lookup and change notifications are the mode's.
		* @param mode - live work-details mode.
		* @returns observable policy that changes exactly when the mode changes.
		*/
		function derivePresentationPolicy(mode) {
			return {
				getSnapshot: () => POLICIES[mode.getSnapshot()],
				subscribe: (listener) => mode.subscribe(listener)
			};
		}
		//#endregion
		//#region src/client/settings/LinkOpeningRow.tsx
		/**
		* Render the link-opening destination selector.
		* @param props - Composed Settings slot props.
		* @returns The preference row.
		*/
		function LinkOpeningRow({ useLinkOpening, useBrowserAvailable, setLinkOpening, t }) {
			const destination = useLinkOpening((value) => value);
			if (!useBrowserAvailable((value) => value)) return null;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(PreferenceRow, {
				title: t("settings.links.title"),
				description: t("settings.links.description"),
				value: destination,
				selectedLabel: t(destination === "sidebar" ? "settings.links.sidebar" : "settings.links.newTab"),
				options: [{
					id: "sidebar",
					label: t("settings.links.sidebar")
				}, {
					id: "new-tab",
					label: t("settings.links.newTab")
				}],
				onSelect: (value) => {
					setLinkOpening(value);
				}
			});
		}
		//#endregion
		//#region src/client/settings/PerformanceUsageRow.tsx
		const OPTIONS = [{
			id: "compact",
			label: "settings.performance.compact"
		}, {
			id: "detailed",
			label: "settings.performance.detailed"
		}];
		/**
		* Render the performance and usage detail selector.
		* @param props - composed Settings slot props.
		* @returns the preference row.
		*/
		function PerformanceUsageRow({ usePerformanceUsage, setPerformanceUsage, t }) {
			const mode = usePerformanceUsage((value) => value);
			const selectedLabel = mode === "detailed" ? "settings.performance.detailed" : "settings.performance.compact";
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(PreferenceRow, {
				title: t("settings.performance.title"),
				description: t("settings.performance.description"),
				value: mode,
				selectedLabel: t(selectedLabel),
				options: OPTIONS.map((option) => ({
					id: option.id,
					label: t(option.label)
				})),
				onSelect: (value) => {
					setPerformanceUsage(value);
				}
			});
		}
		//#endregion
		//#region src/client/performance-usage.ts
		/** Performance detail preference with process-local choices on memory-only settings scopes. */
		/** Shared live preference for the settings row and chat statistics. */
		var PerformanceUsagePolicy = class {
			host;
			unsubscribe;
			/** Current choice, reconciled with accepted Host settings when available. */
			mode = (0, _deepseek_ai_dsh_client_store.createSnapshotStore)(DEFAULT_PERFORMANCE_USAGE);
			/** @param host - Chat settings scope, durable on loopback and memory-only elsewhere. */
			constructor(host) {
				this.host = host;
				const adopt = () => {
					const accepted = host.getSnapshot().value?.performanceUsage;
					if (accepted !== void 0) this.mode.set(accepted);
				};
				this.unsubscribe = host.subscribe(adopt);
				adopt();
			}
			/** Release the accepted-value subscription. */
			dispose() {
				this.unsubscribe();
			}
			/**
			* Publish a choice immediately and persist it when the scope supports writes.
			* @param mode - Statistics detail selected by the user.
			*/
			setMode(mode) {
				if (mode === this.mode.getSnapshot()) return;
				this.mode.set(mode);
				this.host.set("performanceUsage", mode);
			}
		};
		//#endregion
		//#region src/client/chat/use-turn-data.ts
		const EMPTY_SOURCE = {
			getSnapshot: () => void 0,
			subscribe: () => () => {}
		};
		/**
		* Subscribe to one value from a Turn's keyed Location-data store.
		* @param data - current Turn data store, or absence for a Node outside a Turn.
		* @param key - declaration-merged business key.
		* @returns the current value for that key.
		*/
		function useTurnDataValue(data, key) {
			const source = data?.source(key) ?? EMPTY_SOURCE;
			return (0, react.useSyncExternalStore)(source.subscribe, source.getSnapshot);
		}
		//#endregion
		//#region src/client/apply.ts
		const CHAT_NODE_INJECT = { hooks: {
			turnData: (_standard, { turnData }) => function useTurnData(key) {
				return useTurnDataValue(turnData, key);
			},
			disclosure: (_standard, { disclosureReset }) => bindDisclosure(disclosureReset)
		} };
		/** Services required by the Chat target and its presentation registrations. */
		const inject = [
			"slots",
			"sessions",
			"uiWorkspace",
			"uiSession",
			"uiConversation",
			"locale",
			"configForms",
			"remote",
			"remote.session",
			"sidebarRight"
		];
		/**
		* Mount all Chat-owned contributions.
		* @param ctx - Client root context.
		*/
		function apply(ctx) {
			const quotaNotice = (0, _deepseek_ai_dsh_client_store.createSnapshotStore)(null);
			let quotaNoticeSeq = 0;
			const quotaNoticeHolds = /* @__PURE__ */ new Set();
			const chatSources = /* @__PURE__ */ new WeakMap();
			const quotaSubscriptions = /* @__PURE__ */ new Set();
			ctx.effect(() => async () => {
				await Promise.all([...quotaSubscriptions].map((dispose) => dispose()));
			}, "ui-chat: live quota notices");
			const chatSource = (binding) => {
				let source = chatSources.get(binding);
				if (source === void 0) {
					const dispose = binding.ctx.effect(() => {
						const stop = binding.eventSource.subscribe(() => {
							const { change } = binding.eventSource.getSnapshot();
							if (change.kind !== "append") return;
							for (const { event } of change.entries) {
								if (event.type !== "turn/end" || event.data.reason.kind !== "error") continue;
								const { code } = event.data.reason.error;
								if (quotaNoticeHolds.size > 0 || code !== "QUOTA" && code !== "ACCOUNT_QUOTA") continue;
								quotaNotice.set({
									code,
									seq: ++quotaNoticeSeq
								});
							}
						});
						return () => {
							stop();
							chatSources.delete(binding);
							quotaSubscriptions.delete(dispose);
						};
					}, "ui-chat: Provider binding quota notices");
					quotaSubscriptions.add(dispose);
					const target = ctx.uiConversation.binding(binding).target("chat");
					source = {
						getSnapshot: () => target.getSnapshot() ?? EMPTY_CHAT_SNAPSHOT,
						subscribe: (listener) => target.subscribe(listener)
					};
					chatSources.set(binding, source);
				}
				return source;
			};
			registerConversationNodes(ctx);
			ctx.uiSession.provide({
				hooks: ["chat"],
				resolve: (binding) => ({ hooks: { chat: chatSource(binding) } })
			});
			ctx.effect(() => ctx.locale.register(NS, {
				zh,
				en
			}), "ui-chat: dictionaries");
			const t = ctx.locale.bind(NS);
			const chatStore = createChatStore();
			const chatScrollPositions = /* @__PURE__ */ new Map();
			const chatSettings = ctx.configForms.get(CHAT_SETTINGS_NAMESPACE);
			const linkOpening = (0, _deepseek_ai_dsh_client_store.createSnapshotStore)(chatSettings.getSnapshot().value?.linkOpening ?? "sidebar");
			ctx.effect(() => chatSettings.subscribe(() => {
				const accepted = chatSettings.getSnapshot().value?.linkOpening;
				if (accepted !== void 0) linkOpening.set(accepted);
			}));
			ctx.inject(["sidebarRightTabs"], (scope) => {
				const tabs = scope.sidebarRightTabs;
				const browserAvailable = {
					getSnapshot: () => tabs.get("browser") !== void 0,
					subscribe: (listener) => tabs.subscribe(listener)
				};
				scope.slots.inject("settings.general.item", () => scope.slots.register({
					name: "settings.general.item",
					id: "link-opening",
					order: 14,
					locale: NS,
					inject: () => ({
						hooks: {
							linkOpening,
							browserAvailable
						},
						setLinkOpening: (destination) => {
							linkOpening.set(destination);
							chatSettings.set("linkOpening", destination).catch((_error) => {});
						}
					})
				}, LinkOpeningRow));
			});
			const transcriptView = new TranscriptViewPolicy(chatSettings);
			const presentation = derivePresentationPolicy(transcriptView.mode);
			const performancePolicy = new PerformanceUsagePolicy(chatSettings);
			ctx.effect(() => () => {
				transcriptView.dispose();
				performancePolicy.dispose();
			});
			const performanceUsage = performancePolicy.mode;
			registerChatNodeRenderers(ctx, performanceUsage, presentation);
			ctx.slots.inject("settings.general.item", () => ctx.slots.register({
				name: "settings.general.item",
				id: "performance-usage",
				order: 13,
				locale: NS,
				inject: () => ({
					hooks: { performanceUsage },
					setPerformanceUsage: (mode) => {
						performancePolicy.setMode(mode);
					}
				})
			}, PerformanceUsageRow));
			ctx.slots.inject("settings.general.item", () => ctx.slots.register({
				name: "settings.general.item",
				id: "transcript-view",
				order: 12,
				locale: NS,
				inject: () => ({
					hooks: { transcriptView: transcriptView.mode },
					setTranscriptView: (mode) => {
						transcriptView.setMode(mode);
					}
				})
			}, TranscriptViewRow));
			ctx.slots.inject("conversation.view", () => {
				return ctx.slots.register({
					name: "conversation.view",
					id: "chat",
					order: 0,
					label: () => t("view.chat"),
					locale: NS,
					children: {
						"conversation.chat.node": {
							kind: "keyed",
							scope: "session",
							inject: CHAT_NODE_INJECT
						},
						"conversation.message.images": {
							kind: "single",
							scope: "session"
						}
					},
					store: chatStore,
					inject: (sessionId) => {
						const binding = ctx.sessions.binding(sessionId);
						if (binding === void 0) throw new Error(`ui-chat: unknown session "${sessionId}"`);
						const session = binding.session;
						const chat = chatSource(binding);
						const conversation = ctx.uiConversation.binding(binding);
						return {
							hooks: { presentation },
							keyedHooks: {
								chatNode: (key) => chat.getSnapshot().nodes.source(key),
								chatNodeProcess: (key) => chat.getSnapshot().nodes.processSource(key),
								chatGroup: (key) => conversation.snapshot.getSnapshot().views.grouped("chat")?.groupSource(key)
							},
							fileMentions: (owner) => ctx.get("chatFileMentions")?.forClosing(owner, sessionId),
							openFile: async (path, options) => {
								const cwd = ctx.sessions.list.getSnapshot().byId[sessionId]?.cwd;
								const url = fileAddressFor(sessionId, cwd, path);
								if (options?.line === void 0) ctx.sidebarRight.openResource(url);
								else ctx.sidebarRight.openResource(url, { params: { line: options.line } });
								await Promise.resolve();
							},
							openSkill: (name) => {
								const scope = ctx.sessions.scope(sessionId);
								if (scope === void 0) return;
								ctx.get("inputTriggers")?.sessionOf(scope).openReference("skill", { ref: `/${name}` });
							},
							openExternalLink: (url) => {
								if (linkOpening.getSnapshot() === "sidebar" && ctx.get("sidebarRightTabs")?.get("browser") !== void 0) ctx.sidebarRight.openTab("browser", { params: { url } });
								else window.open(url, "_blank", "noopener,noreferrer");
							},
							loadOlder: () => {
								session.loadOlder();
							},
							loadThrough: (seq) => session.loadThrough(seq),
							loadImage: Object.assign((attachment) => ctx.uiConversation.imageUrl(sessionId, attachment), { peek: (attachment) => ctx.uiConversation.peekImageUrl(sessionId, attachment) }),
							chatScroll: {
								save: (position) => {
									if (position === null) chatScrollPositions.delete(sessionId);
									else chatScrollPositions.set(sessionId, position);
								},
								read: () => chatScrollPositions.get(sessionId) ?? null
							},
							forkAt: (seq) => {
								ctx.sessions.fork({
									sessionId,
									atSeq: seq,
									increaseTitle: true
								}).then((childId) => {
									ctx.uiWorkspace.openSession(childId);
								}).catch(() => {});
							}
						};
					}
				}, ChatView);
			});
			ctx.slots.inject("shell.overlay", () => ctx.slots.register({
				name: "shell.overlay",
				id: "chat.quota-notice",
				locale: NS,
				children: { "shell.quota-notice": {
					kind: "chain",
					scope: "root"
				} },
				inject: () => ({
					hooks: { notice: quotaNotice },
					dismissNotice: () => {
						quotaNoticeHolds.clear();
						quotaNotice.set(null);
					},
					keepNoticeOpen: () => {
						if (quotaNotice.getSnapshot() === null) return () => {};
						const token = Symbol("ui-chat quota notice hold");
						quotaNoticeHolds.add(token);
						return () => {
							quotaNoticeHolds.delete(token);
						};
					}
				})
			}, QuotaNoticeHost));
			ctx.slots.inject("conversation.composer.dock", () => ctx.slots.register({
				name: "conversation.composer.dock",
				id: "stats",
				order: 0,
				locale: NS,
				inject: () => ({ hooks: { performanceUsage } })
			}, StatsPills));
			ctx.slots.inject("conversation.approval.detail", () => ctx.slots.register({ name: "conversation.approval.detail" }, ApprovalCommand));
		}
		//#endregion
		exports.EMPTY_CHAT_SNAPSHOT = EMPTY_CHAT_SNAPSHOT;
		exports.apply = apply;
		exports.inject = inject;
		exports.isRunningTool = isRunningTool;
		exports.isSettledTool = isSettledTool;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map