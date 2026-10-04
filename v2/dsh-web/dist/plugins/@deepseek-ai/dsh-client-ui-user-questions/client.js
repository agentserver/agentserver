window.__ModuleLoader__.load({
	id: "@deepseek-ai/dsh-client-ui-user-questions",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		Object.defineProperty(exports, Symbol.toStringTag, { value: "Module" });
		let _deepseek_ai_dsh_client_store = require("@deepseek-ai/dsh-client-store");
		let react = require("react");
		let _deepseek_ai_dsh_client_ui_primitives = require("@deepseek-ai/dsh-client-ui-primitives");
		let react_jsx_runtime = require("react/jsx-runtime");
		//#region src/client/contract/slots.ts
		function settlePendingComposer(settle, failureMessage) {
			try {
				settle();
				return Promise.resolve();
			} catch (error) {
				return Promise.reject(error instanceof Error ? error : new Error(failureMessage, { cause: error }));
			}
		}
		/**
		* Narrow a request to a renderable plan review, or return undefined to leave it
		* to the generic question flow.
		*
		* The card offers approval and a return to the composer for change requests.
		* It accepts one question carrying the plan as detail and the named approve
		* option, with at most one alternative and no multi-select. Larger choices
		* remain in the generic question flow.
		*
		* @param questions - the request's whole question batch.
		* @returns The narrowed review, or undefined when the generic flow owns it.
		*/
		function planReviewOf(questions) {
			if (questions.length !== 1) return void 0;
			const question = questions[0];
			const intent = question.intent;
			if (intent?.kind !== "plan-review" || question.detail === void 0) return void 0;
			if (question.multiSelect === true) return void 0;
			const options = question.options ?? [];
			if (options.length > 2) return void 0;
			const approve = options.find((option) => option.label === intent.approve);
			if (approve === void 0) return void 0;
			const decline = options.find((option) => option.label !== intent.approve);
			return {
				id: question.id,
				question: question.question,
				plan: question.detail,
				...intent.callId === void 0 ? {} : { callId: intent.callId },
				approve,
				...decline === void 0 ? {} : { decline }
			};
		}
		let nextQuestionKey = 0;
		/** Create a wire-preserved user-question rejection. */
		function questionError(message, code) {
			const error = new Error(message);
			error.name = "UserQuestionError";
			error.code = code;
			return error;
		}
		/** One answerable Client presentation of a pending Host waterfall. */
		var PendingQuestion = class {
			sessionId;
			/** Presentation discriminator used by Session pending-interaction consumers. */
			kind;
			/** Opaque render identity and request key for the Session-scoped draft store. */
			key;
			/** The request's question list. */
			questions;
			/** Result returned by the Remote Event listener to the Host waterfall. */
			result;
			#resolve;
			#reject;
			#signal;
			#onAbort;
			#delegated = Symbol("pending question delegated");
			#settled = false;
			/**
			* @param sessionId - Agent/Session identity owning the scoped request.
			* @param questions - complete question batch.
			* @param signal - Host request and delivery lifetime.
			*/
			constructor(sessionId, questions, signal) {
				this.sessionId = sessionId;
				nextQuestionKey += 1;
				this.key = `question:${String(nextQuestionKey)}`;
				this.questions = questions;
				this.kind = planReviewOf(questions) === void 0 ? "question" : "plan-review";
				const completion = Promise.withResolvers();
				this.result = completion.promise;
				this.#resolve = completion.resolve;
				this.#reject = completion.reject;
				this.#signal = signal;
				if (signal === void 0) {
					this.#onAbort = void 0;
					return;
				}
				const onAbort = () => {
					this.abort(questionError("ask_user_question was aborted before the user answered", "ASK_ABORTED"));
				};
				this.#onAbort = onAbort;
				signal.addEventListener("abort", onAbort, { once: true });
				if (signal.aborted) onAbort();
			}
			/**
			* Resolve the Host waterfall with the whole answer batch.
			* @param answer - complete structured answer batch.
			*/
			answer(answer) {
				return settlePendingComposer(() => {
					this.finish(() => {
						this.#resolve(answer);
					});
				}, "pending question settlement failed");
			}
			/** Delegate an unanswered request to the next waterfall listener. */
			delegate() {
				if (this.#settled) return;
				this.finish(() => {
					this.#reject(this.#delegated);
				});
			}
			/**
			* Test whether a rejection requests waterfall delegation.
			* @param reason - rejection received from {@link PendingQuestion.result}.
			* @returns whether {@link PendingQuestion.delegate} produced it.
			*/
			isDelegation(reason) {
				return reason === this.#delegated;
			}
			/** Reject the Host waterfall because the user closed the question. */
			cancel() {
				return settlePendingComposer(() => {
					this.finish(() => {
						this.#reject(questionError("the user cancelled ask_user_question", "ASK_CANCELLED"));
					});
				}, "pending question cancellation failed");
			}
			/**
			* End an unanswered presentation when its transport, scope, or plugin lifetime ends.
			* @param reason - rejection exposed to the waiting Remote Event listener.
			*/
			abort(reason) {
				if (this.#settled) return;
				this.finish(() => {
					this.#reject(reason);
				});
			}
			finish(settle) {
				if (this.#settled) throw new Error(`pending question ${this.key} is already settled`);
				this.#settled = true;
				if (this.#signal !== void 0 && this.#onAbort !== void 0) this.#signal.removeEventListener("abort", this.#onAbort);
				settle();
			}
		};
		//#endregion
		//#region src/client/draft-store.ts
		/**
		* Session-scoped draft state for the generic question composer. The Slot
		* registry owns store instances; this module exports only the factory so a
		* plugin reload cannot reuse a module-global handle.
		*/
		const emptyProgress = () => ({
			index: 0,
			drafts: []
		});
		/**
		* Declare the question composer's transient Session store.
		* @returns a non-persisted store handle whose instance is owned by the Slot registry.
		*/
		function createQuestionDraftStore() {
			return (0, _deepseek_ai_dsh_client_store.defineStore)({
				init: () => ({ progress: emptyProgress() }),
				actions: {
					replace: (draft, requestKey, progress) => {
						draft.requestKey = requestKey;
						draft.progress = progress;
					},
					clear: (draft, requestKey) => {
						if (draft.requestKey !== requestKey) return;
						delete draft.requestKey;
						draft.progress = emptyProgress();
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
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-user-questions/src/client/PlanReviewPanel.module.css.mjs
		const css$1 = ".i6UW3a_frame{padding:6px calc(var(--dsh-composer-side-clearance) + 16px) 10px;justify-content:center;display:flex}.i6UW3a_card{width:100%;max-width:var(--dsh-chat-content-width);--dsw-elevation-stroke-color:var(--dsw-alias-border-l2);border-radius:var(--dsw-radius-xl);background:var(--dsw-specific-input-major);box-shadow:var(--dsw-elevation-panel);color:var(--dsw-alias-label-primary);border:0;flex-direction:column;display:flex;overflow:hidden}.i6UW3a_card,.i6UW3a_card *{box-sizing:border-box}.i6UW3a_strip{background:var(--dsw-alias-state-warn-tertiary);color:var(--dsw-alias-state-warn-primary);flex-shrink:0;align-items:center;gap:8px;padding:12px 16px;font-size:14px;line-height:20px;display:flex}.i6UW3a_footer{flex-shrink:0;justify-content:space-between;align-items:center;gap:12px;padding:8px 16px 12px;display:flex}.i6UW3a_feedback{min-height:16px;color:var(--dsw-alias-state-error-primary);font-size:11px;line-height:16px}.i6UW3a_actions{flex-shrink:0;align-items:center;gap:8px;display:flex}.i6UW3a_summary{min-width:0;padding:14px 16px 12px}.i6UW3a_title{text-overflow:ellipsis;white-space:nowrap;margin:0;font-size:15px;font-weight:500;line-height:22px;overflow:hidden}.i6UW3a_description{-webkit-line-clamp:2;color:var(--dsw-alias-label-secondary);-webkit-box-orient:vertical;margin:8px 0 0;font-size:14px;line-height:24px;display:-webkit-box;overflow:hidden}.i6UW3a_discuss{gap:6px}@media (width<=720px){.i6UW3a_card{border-radius:var(--dsw-radius-xl)}.i6UW3a_footer{align-items:flex-end;padding:8px 12px 10px}}.i6UW3a_previewActions{align-items:center;margin-left:auto;display:flex}";
		const tagId$1 = "@deepseek-ai/dsh-client-ui-user-questions/PlanReviewPanel.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$1) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-user-questions";
			tag.dataset.pluginCss = tagId$1;
			tag.textContent = css$1;
			document.head.appendChild(tag);
		}
		var PlanReviewPanel_module_css_default = {
			"actions": "i6UW3a_actions",
			"card": "i6UW3a_card",
			"description": "i6UW3a_description",
			"discuss": "i6UW3a_discuss",
			"feedback": "i6UW3a_feedback",
			"footer": "i6UW3a_footer",
			"frame": "i6UW3a_frame",
			"previewActions": "i6UW3a_previewActions",
			"strip": "i6UW3a_strip",
			"summary": "i6UW3a_summary",
			"title": "i6UW3a_title"
		};
		//#endregion
		//#region src/client/PlanReviewPanel.tsx
		/**
		* Optional-prop spread for a decision button's tooltip: `title` is optional on
		* the DOM props, and exactOptionalPropertyTypes rejects an explicit undefined.
		*
		* @param description - the asker's option description, when it carries one.
		* @returns The `title` prop to spread, or nothing.
		*/
		function tooltip(description) {
			return description === void 0 ? {} : { title: description };
		}
		/**
		* Render plan review controls; the submitted document opens in the sidebar.
		*
		* @param props - the question domain face, the narrowed plan review, and `t`.
		* @returns The plan-review takeover for this request.
		*/
		function PlanReviewPanel({ pending, review, t, renderSlot }) {
			const [busy, setBusy] = (0, react.useState)(false);
			const [error, setError] = (0, react.useState)(null);
			const settle = (send) => {
				setBusy(true);
				setError(null);
				send().catch((cause) => {
					setBusy(false);
					setError(cause instanceof Error ? cause.message : String(cause));
				});
			};
			const decide = (label) => {
				settle(() => pending.answer({ answers: [{
					id: review.id,
					selected: [label]
				}] }));
			};
			const summary = (0, react.useMemo)(() => {
				const title = (0, _deepseek_ai_dsh_client_ui_primitives.extractMarkdownPlainText)(review.plan, { mode: "first-line" });
				const description = (0, _deepseek_ai_dsh_client_ui_primitives.extractMarkdownPlainText)(review.plan, { mode: "first-paragraph" });
				return {
					title,
					description: description === title ? "" : description
				};
			}, [review.plan]);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: PlanReviewPanel_module_css_default.frame,
				"data-plan-review-key": pending.key,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
					className: PlanReviewPanel_module_css_default.card,
					"aria-label": review.question,
					"aria-busy": busy,
					children: [
						/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							className: PlanReviewPanel_module_css_default.strip,
							children: [
								/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, { state: busy ? "ongoing" : "warning" }),
								t("plan.header"),
								/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
									className: PlanReviewPanel_module_css_default.previewActions,
									children: renderSlot("conversation.plan-review.actions", {
										review,
										requestKey: pending.key
									})
								})
							]
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							className: PlanReviewPanel_module_css_default.summary,
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("h3", {
								className: PlanReviewPanel_module_css_default.title,
								children: summary.title
							}), summary.description !== "" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
								className: PlanReviewPanel_module_css_default.description,
								children: summary.description
							})]
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							className: PlanReviewPanel_module_css_default.footer,
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
								className: PlanReviewPanel_module_css_default.feedback,
								role: "status",
								children: error
							}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
								className: PlanReviewPanel_module_css_default.actions,
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
									variant: "outline",
									className: PlanReviewPanel_module_css_default.discuss,
									icon: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconEditOutlineRegular, { size: 14 }),
									disabled: busy,
									onClick: () => {
										settle(() => pending.cancel());
									},
									children: t("plan.discuss")
								}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
									variant: "primary",
									...tooltip(review.approve.description),
									disabled: busy,
									onClick: () => {
										decide(review.approve.label);
									},
									children: t("plan.approve")
								})]
							})]
						})
					]
				})
			});
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-user-questions/src/client/QuestionComposer.module.css.mjs
		const css = "._0Vv1Cq_frame{padding:6px calc(var(--dsh-composer-side-clearance) + 16px) 10px;justify-content:center;display:flex}._0Vv1Cq_card{width:100%;max-width:var(--dsh-chat-content-width);--dsw-elevation-stroke-color:var(--dsw-alias-border-l2-darkmode-thin);border-radius:var(--dsw-radius-xl);background:var(--dsw-specific-input-major);max-height:min(60vh,520px);box-shadow:var(--dsw-elevation-panel);color:var(--dsw-alias-label-primary);--dsh-scrollbar-thumb:var(--dsw-alias-scrollbar-bg-l2);--dsh-scrollbar-thumb-hover:var(--dsw-alias-scrollbar-hover-l2);border:0;flex-direction:column;padding:0 0 10px;display:flex;overflow:hidden}._0Vv1Cq_card,._0Vv1Cq_card *{box-sizing:border-box}._0Vv1Cq_cardMinimized{max-height:none}._0Vv1Cq_cardMinimized ._0Vv1Cq_header{padding-bottom:14px}._0Vv1Cq_headerActions{flex-shrink:0;align-items:center;gap:4px;display:flex}._0Vv1Cq_header{flex-shrink:0;justify-content:space-between;align-items:flex-start;gap:16px;padding:20px 16px 0 24px;display:flex}._0Vv1Cq_headingBlock{min-width:0}._0Vv1Cq_eyebrow{color:var(--dsw-alias-label-tertiary);margin-bottom:5px;font-size:11px;line-height:16px}._0Vv1Cq_title{margin:0;font-size:16px;font-weight:500;line-height:22px}._0Vv1Cq_detail{margin:0 2px 8px}._0Vv1Cq_footerActions{flex-shrink:0;align-items:center;gap:12px;display:flex}._0Vv1Cq_pager{flex-shrink:0;align-items:center;gap:6px;display:flex}._0Vv1Cq_progress{color:var(--dsw-alias-label-secondary);white-space:nowrap;word-spacing:-2px;padding:0 4px;font-size:14px;font-weight:500;line-height:24px}._0Vv1Cq_iconButton{corner-shape:round;width:24px;height:24px;color:var(--dsw-alias-label-tertiary);cursor:pointer;background:0 0;border:none;border-radius:999px;place-items:center;padding:0;display:grid}._0Vv1Cq_iconButton:hover:not(:disabled){background:var(--dsw-alias-interactive-bg-hover);color:var(--dsw-alias-label-primary)}._0Vv1Cq_iconButton:disabled{color:var(--dsw-alias-label-dimmed);cursor:default}._0Vv1Cq_body{overscroll-behavior:contain;flex-direction:column;flex:auto;min-height:0;display:flex;overflow-y:auto}._0Vv1Cq_options{flex-direction:column;gap:1px;margin:8px 0 0;padding:4px 12px;display:flex}._0Vv1Cq_option{border-radius:var(--dsw-radius-md);width:100%;min-height:40px;color:inherit;text-align:left;cursor:pointer;background:0 0;border:1px solid #0000;flex-shrink:0;align-items:flex-start;gap:8px;padding:8px 12px 8px 8px;transition:background-color .12s,border-color .12s;display:flex}._0Vv1Cq_option:hover:not(:disabled),._0Vv1Cq_optionSelected{background:var(--dsw-alias-interactive-bg-hover)}._0Vv1Cq_optionSelected{border-color:var(--dsw-alias-border-l2)}._0Vv1Cq_option:disabled{cursor:default}._0Vv1Cq_number{border-radius:var(--dsw-radius-xs);background:var(--dsw-alias-bg-overlay);width:20px;height:20px;color:var(--dsw-alias-label-secondary);flex:0 0 20px;place-items:center;margin-top:2px;font-size:12px;font-weight:500;line-height:18px;display:grid}._0Vv1Cq_checkbox{flex:0 0 20px;place-items:center;width:20px;height:20px;margin-top:2px;display:grid}._0Vv1Cq_checkbox:before{content:\"\";border:.5px solid var(--dsw-alias-border-l4);border-radius:var(--dsw-radius-xs);grid-area:1/1;width:14px;height:14px;transition:background-color .12s,border-color .12s}._0Vv1Cq_checkbox>svg{grid-area:1/1}._0Vv1Cq_checkboxChecked{color:var(--dsw-alias-label-primary-foreground)}._0Vv1Cq_checkboxChecked:before{border-color:var(--dsw-alias-label-primary);background:var(--dsw-alias-label-primary)}._0Vv1Cq_optionCopy{flex:1;min-width:0}._0Vv1Cq_optionLine{flex-wrap:wrap;align-items:baseline;gap:2px 6px;display:flex}._0Vv1Cq_optionLabel{font-size:14px;font-weight:500;line-height:24px}._0Vv1Cq_badge{border-radius:var(--dsw-radius-xs);background:var(--dsw-specific-sidebar-nav-item-active-accent);color:var(--dsw-alias-button-info-fill);padding:0 4px;font-size:11px;font-weight:600;line-height:18px}._0Vv1Cq_description{color:var(--dsw-alias-label-tertiary);font-size:14px;font-weight:400;line-height:24px}._0Vv1Cq_customRow{border-radius:var(--dsw-radius-md);border:1px solid #0000;flex-shrink:0;align-items:flex-start;gap:8px;width:100%;min-height:40px;padding:8px 12px 8px 8px;transition:background-color .12s,border-color .12s;display:flex}._0Vv1Cq_customRow:hover,._0Vv1Cq_customRow:focus-within,._0Vv1Cq_customRowActive{background:var(--dsw-alias-interactive-bg-hover)}._0Vv1Cq_customRow:focus-within,._0Vv1Cq_customRowActive{border-color:var(--dsw-alias-border-l2)}._0Vv1Cq_field{--dsh-answer-field-padding:0;min-width:0;display:grid}._0Vv1Cq_field>*{min-width:0;padding:var(--dsh-answer-field-padding);font:inherit;white-space:pre-wrap;word-break:break-word;overflow-wrap:anywhere;grid-area:1/1;font-size:14px;line-height:24px}._0Vv1Cq_fieldMirror{box-sizing:content-box;visibility:hidden;max-height:144px;overflow:hidden}._0Vv1Cq_fieldInput{resize:none;color:var(--dsw-alias-label-primary);caret-color:var(--dsw-alias-state-business-primary);background:0 0;border:none;outline:none;overflow-y:auto}._0Vv1Cq_fieldInput::placeholder{color:var(--dsw-alias-label-caption)}._0Vv1Cq_customInline{flex:1}._0Vv1Cq_customBlock{border:.5px solid var(--dsw-alias-border-l4);border-radius:var(--dsw-radius-lg);background:var(--dsw-alias-bg-module-platform);--dsh-answer-field-padding:8px 12px;flex-shrink:0;min-height:64px;margin:0 12px}._0Vv1Cq_customBlock:focus-within{border-color:var(--dsw-alias-state-business-primary)}._0Vv1Cq_footer{flex-shrink:0;justify-content:space-between;align-items:center;gap:12px;margin-top:12px;padding:0 10px 0 18px;display:flex}._0Vv1Cq_feedback{min-height:16px;color:var(--dsw-alias-state-error-primary);text-align:right;flex:1;font-size:11px;line-height:16px}@media (width<=720px){._0Vv1Cq_card{border-radius:var(--dsw-radius-xl)}._0Vv1Cq_header{padding:10px 12px 0 18px}._0Vv1Cq_options{padding:4px 8px}._0Vv1Cq_title{font-size:15px;line-height:21px}._0Vv1Cq_option,._0Vv1Cq_customRow{padding:8px 6px}._0Vv1Cq_footer{align-items:flex-end;padding:0 10px}._0Vv1Cq_footerActions{flex-shrink:0}}@media (prefers-reduced-motion:reduce){._0Vv1Cq_option,._0Vv1Cq_customRow{transition:none}}";
		const tagId = "@deepseek-ai/dsh-client-ui-user-questions/QuestionComposer.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-user-questions";
			tag.dataset.pluginCss = tagId;
			tag.textContent = css;
			document.head.appendChild(tag);
		}
		var QuestionComposer_module_css_default = {
			"badge": "_0Vv1Cq_badge",
			"body": "_0Vv1Cq_body",
			"card": "_0Vv1Cq_card",
			"cardMinimized": "_0Vv1Cq_cardMinimized",
			"checkbox": "_0Vv1Cq_checkbox",
			"checkboxChecked": "_0Vv1Cq_checkboxChecked",
			"customBlock": "_0Vv1Cq_customBlock",
			"customInline": "_0Vv1Cq_customInline",
			"customRow": "_0Vv1Cq_customRow",
			"customRowActive": "_0Vv1Cq_customRowActive",
			"description": "_0Vv1Cq_description",
			"detail": "_0Vv1Cq_detail",
			"eyebrow": "_0Vv1Cq_eyebrow",
			"feedback": "_0Vv1Cq_feedback",
			"field": "_0Vv1Cq_field",
			"fieldInput": "_0Vv1Cq_fieldInput",
			"fieldMirror": "_0Vv1Cq_fieldMirror",
			"footer": "_0Vv1Cq_footer",
			"footerActions": "_0Vv1Cq_footerActions",
			"frame": "_0Vv1Cq_frame",
			"header": "_0Vv1Cq_header",
			"headerActions": "_0Vv1Cq_headerActions",
			"headingBlock": "_0Vv1Cq_headingBlock",
			"iconButton": "_0Vv1Cq_iconButton",
			"number": "_0Vv1Cq_number",
			"option": "_0Vv1Cq_option",
			"optionCopy": "_0Vv1Cq_optionCopy",
			"optionLabel": "_0Vv1Cq_optionLabel",
			"optionLine": "_0Vv1Cq_optionLine",
			"optionSelected": "_0Vv1Cq_optionSelected",
			"options": "_0Vv1Cq_options",
			"pager": "_0Vv1Cq_pager",
			"progress": "_0Vv1Cq_progress",
			"title": "_0Vv1Cq_title"
		};
		//#endregion
		//#region src/client/QuestionComposer.tsx
		/**
		* Split the conventional recommendation suffix without changing the answer value.
		* @param label - Original option label returned if selected.
		* @returns Display label plus recommendation state.
		*/
		function parseRecommendedLabel(label) {
			const suffix = /\s*(?:\((?:recommended|推荐)\)|（(?:recommended|推荐)）)\s*$/i;
			return suffix.test(label) ? {
				label: label.replace(suffix, ""),
				recommended: true
			} : {
				label,
				recommended: false
			};
		}
		/** Return whether a text-field key event belongs to an active IME composition. */
		function isComposing(event) {
			return event.nativeEvent.isComposing || Reflect.get(event.nativeEvent, "keyCode") === 229;
		}
		/**
		* Auto-growing free-text answer: a textarea, so a long answer soft-wraps and
		* Shift+Enter breaks a line, over a hidden mirror that owns the height.
		*
		* The mirror renders the draft plus a trailing newline in normal flow and so
		* sizes the grid row (counting rows by '\n' cannot see soft wraps); the
		* textarea shares that one cell and stretches to it, and `rows={1}` keeps the
		* control's own intrinsic height out of the row sizing so the mirror alone
		* decides. Past the mirror's cap the textarea scrolls itself — it is the only
		* scrollport in the stack, there being no second glyph layer to keep aligned.
		* Mirror and textarea MUST share font, line-height, padding and wrapping rules
		* or the two heights diverge.
		*
		* @param props - visual variant, draft text, and the field's event handlers.
		* @returns The mirrored auto-growing field.
		*/
		function AnswerField(props) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: clsx(QuestionComposer_module_css_default.field, props.variant === "inline" ? QuestionComposer_module_css_default.customInline : QuestionComposer_module_css_default.customBlock),
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
					"aria-hidden": true,
					className: QuestionComposer_module_css_default.fieldMirror,
					children: `${props.value}\n`
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("textarea", {
					autoFocus: props.autoFocus,
					className: QuestionComposer_module_css_default.fieldInput,
					value: props.value,
					disabled: props.disabled,
					rows: 1,
					placeholder: props.placeholder,
					onFocus: props.onFocus,
					onChange: props.onChange,
					onKeyDown: props.onKeyDown
				})]
			});
		}
		/**
		* Composer takeover router. Generic-question drafts live in this entry's
		* Session-scoped Slot store, keyed by the pending carrier, so a strict Session
		* entry remount restores the same request without exposing it to another one.
		*
		* One takeover, two presentations: a request that declares a presentation intent this
		* package renders uses that presentation (a plan review is one decision over one
		* plan, not a question set), and every other request takes the generic flow.
		* The routing lives here, at the one entry that owns the composer seat, so
		* neither presentation can claim a request the other is already rendering.
		*
		* @param props - the selector-matched pending question carrier plus the framework standard kit.
		* @returns The question flow, or the intent's own surface, for this request.
		*/
		function QuestionComposer(props) {
			const question = props.matched;
			const review = (0, react.useMemo)(() => planReviewOf(question.questions), [question]);
			return review === void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(QuestionFlow, {
				pending: question,
				t: props.t,
				useStore: props.useStore,
				actions: props.actions
			}, question.key) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(PlanReviewPanel, {
				pending: question,
				review,
				t: props.t,
				renderSlot: props.renderSlot
			}, question.key);
		}
		function QuestionFlow({ pending, t, useStore, actions }) {
			const questions = pending.questions;
			const markdownLabels = (0, react.useMemo)(() => ({
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
			}), [t]);
			const initialProgress = (0, react.useMemo)(() => ({
				index: 0,
				drafts: questions.map(() => ({
					selected: [],
					custom: "",
					skipped: false
				}))
			}), [questions]);
			const { index, drafts } = useStore((state) => state.requestKey === pending.key && state.progress.drafts.length === questions.length ? state.progress : void 0) ?? initialProgress;
			const [busy, setBusy] = (0, react.useState)(null);
			const [error, setError] = (0, react.useState)(null);
			const [minimized, setMinimized] = (0, react.useState)(false);
			const focusedQuestions = (0, react.useRef)(/* @__PURE__ */ new Set());
			const question = questions[index];
			const draft = drafts[index];
			const hasOptions = (question.options?.length ?? 0) > 0;
			const replaceProgress = (nextIndex, nextDrafts) => {
				actions.replace(pending.key, {
					index: nextIndex,
					drafts: nextDrafts
				});
			};
			const cancelFlow = () => {
				setBusy("cancel");
				setError(null);
				pending.cancel().then(() => {
					actions.clear(pending.key);
				}).catch((cause) => {
					setBusy(null);
					setError({ text: cause instanceof Error ? cause.message : String(cause) });
				});
			};
			const updateDraft = (update, nextIndex = index) => {
				replaceProgress(nextIndex, drafts.map((item, itemIndex) => itemIndex === index ? update(item) : item));
				setError(null);
			};
			const choose = (label) => {
				updateDraft((current) => {
					if (question.multiSelect === true) {
						const selected = current.selected.includes(label) ? current.selected.filter((item) => item !== label) : [...current.selected, label];
						return {
							...current,
							selected,
							skipped: false
						};
					}
					return {
						selected: [label],
						custom: "",
						skipped: false
					};
				}, question.multiSelect !== true && index < questions.length - 1 ? index + 1 : index);
			};
			const answered = (item) => item.selected.length > 0 || item.custom.trim() !== "";
			const completed = (item) => answered(item) || item.skipped;
			const submitDrafts = (values) => {
				const missing = values.findIndex((item) => !completed(item));
				if (missing >= 0) {
					replaceProgress(missing, values);
					setError({ key: "error.incomplete" });
					return;
				}
				const answer = { answers: questions.map((item, itemIndex) => {
					const value = values[itemIndex];
					if (value.skipped) return {
						id: item.id,
						selected: []
					};
					const custom = value.custom.trim();
					return {
						id: item.id,
						selected: custom === "" || item.multiSelect === true ? value.selected : [],
						...custom === "" ? {} : { custom }
					};
				}) };
				setBusy("answer");
				setError(null);
				pending.answer(answer).then(() => {
					actions.clear(pending.key);
				}).catch((cause) => {
					setBusy(null);
					setError({ text: cause instanceof Error ? cause.message : String(cause) });
				});
			};
			const continueFlow = () => {
				if (!answered(draft)) {
					setError({ key: "error.unanswered" });
					return;
				}
				if (index < questions.length - 1) {
					replaceProgress(index + 1, drafts);
					setError(null);
					return;
				}
				submitDrafts(drafts);
			};
			const draftCustom = (event) => {
				const value = event.target.value;
				updateDraft((current) => ({
					...current,
					selected: question.multiSelect === true ? current.selected : [],
					custom: value,
					skipped: false
				}));
			};
			const continueFromCustom = (event) => {
				if (event.key !== "Enter" || event.shiftKey || isComposing(event)) return;
				event.preventDefault();
				continueFlow();
			};
			const skipQuestion = () => {
				const nextDrafts = drafts.map((item, itemIndex) => itemIndex === index ? {
					selected: [],
					custom: "",
					skipped: true
				} : item);
				replaceProgress(index < questions.length - 1 ? index + 1 : index, nextDrafts);
				setError(null);
				if (index < questions.length - 1) return;
				submitDrafts(nextDrafts);
			};
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: QuestionComposer_module_css_default.frame,
				"data-question-key": pending.key,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
					className: clsx(QuestionComposer_module_css_default.card, minimized && QuestionComposer_module_css_default.cardMinimized),
					"aria-labelledby": `question-${pending.key}-${String(index)}`,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("header", {
						className: QuestionComposer_module_css_default.header,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							className: QuestionComposer_module_css_default.headingBlock,
							children: [question.header !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
								className: QuestionComposer_module_css_default.eyebrow,
								children: question.header
							}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("h2", {
								className: QuestionComposer_module_css_default.title,
								id: `question-${pending.key}-${String(index)}`,
								children: question.question
							})]
						}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							className: QuestionComposer_module_css_default.headerActions,
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
								type: "button",
								className: QuestionComposer_module_css_default.iconButton,
								"aria-label": t(minimized ? "nav.maximize" : "nav.minimize"),
								title: t(minimized ? "nav.maximize" : "nav.minimize"),
								"aria-expanded": !minimized,
								disabled: busy !== null,
								onClick: () => {
									setMinimized((current) => !current);
								},
								children: minimized ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronUpOutlineRegular, {}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronDownOutlineRegular, {})
							}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
								type: "button",
								className: QuestionComposer_module_css_default.iconButton,
								"aria-label": t("nav.cancel"),
								title: t("nav.cancel"),
								disabled: busy !== null,
								onClick: cancelFlow,
								children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconCloseOutlineRegular, {})
							})]
						})]
					}), !minimized && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: QuestionComposer_module_css_default.body,
						"data-question-scroll": true,
						children: [question.detail !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: QuestionComposer_module_css_default.detail,
							children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.MarkdownText, {
								text: question.detail,
								labels: markdownLabels
							})
						}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							className: QuestionComposer_module_css_default.options,
							role: question.multiSelect === true ? "group" : "radiogroup",
							children: [(question.options ?? []).map((option, optionIndex) => {
								const selected = draft.selected.includes(option.label);
								const display = parseRecommendedLabel(option.label);
								return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
									type: "button",
									className: clsx(QuestionComposer_module_css_default.option, selected && question.multiSelect !== true && QuestionComposer_module_css_default.optionSelected),
									role: question.multiSelect === true ? "checkbox" : "radio",
									"aria-checked": selected,
									"aria-label": display.label,
									disabled: busy !== null,
									onClick: () => {
										choose(option.label);
									},
									onKeyDown: (event) => {
										if (event.key !== "Enter" || !drafts.every(completed)) return;
										event.preventDefault();
										submitDrafts(drafts);
									},
									children: [question.multiSelect === true ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
										className: clsx(QuestionComposer_module_css_default.checkbox, selected && QuestionComposer_module_css_default.checkboxChecked),
										"aria-hidden": "true",
										children: selected && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconCheckOutlineRegular, { size: 12 })
									}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
										className: QuestionComposer_module_css_default.number,
										children: optionIndex + 1
									}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
										className: QuestionComposer_module_css_default.optionCopy,
										children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
											className: QuestionComposer_module_css_default.optionLine,
											children: [
												/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
													className: QuestionComposer_module_css_default.optionLabel,
													children: display.label
												}),
												display.recommended && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
													className: QuestionComposer_module_css_default.badge,
													children: t("option.recommended")
												}),
												option.description !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
													className: QuestionComposer_module_css_default.description,
													children: option.description
												})
											]
										})
									})]
								}, `${option.label}-${String(optionIndex)}`);
							}), hasOptions ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
								className: clsx(QuestionComposer_module_css_default.customRow, draft.custom !== "" && QuestionComposer_module_css_default.customRowActive),
								children: [question.multiSelect === true ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
									className: clsx(QuestionComposer_module_css_default.checkbox, draft.custom !== "" && QuestionComposer_module_css_default.checkboxChecked),
									"aria-hidden": "true",
									children: draft.custom !== "" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconCheckOutlineRegular, { size: 12 })
								}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
									className: QuestionComposer_module_css_default.number,
									"aria-hidden": "true",
									children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconEditOutlineRegular, { size: 12 })
								}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(AnswerField, {
									variant: "inline",
									value: draft.custom,
									disabled: busy !== null,
									placeholder: t("custom.placeholder"),
									onChange: draftCustom,
									onKeyDown: continueFromCustom
								})]
							}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(AnswerField, {
								autoFocus: !focusedQuestions.current.has(index),
								variant: "block",
								value: draft.custom,
								disabled: busy !== null,
								placeholder: t("custom.placeholder"),
								onFocus: () => {
									focusedQuestions.current.add(index);
								},
								onChange: draftCustom,
								onKeyDown: continueFromCustom
							})]
						})]
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("footer", {
						className: QuestionComposer_module_css_default.footer,
						children: [
							/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
								className: QuestionComposer_module_css_default.pager,
								children: [
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
										type: "button",
										className: QuestionComposer_module_css_default.iconButton,
										"aria-label": t("nav.prev"),
										disabled: index === 0 || busy !== null,
										onClick: () => {
											replaceProgress(index - 1, drafts);
											setError(null);
										},
										children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronLeftOutlineRegular, {})
									}),
									/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
										className: QuestionComposer_module_css_default.progress,
										children: [
											index + 1,
											" / ",
											questions.length
										]
									}),
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
										type: "button",
										className: QuestionComposer_module_css_default.iconButton,
										"aria-label": t("nav.next"),
										disabled: index === questions.length - 1 || busy !== null,
										onClick: () => {
											replaceProgress(index + 1, drafts);
											setError(null);
										},
										children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronRightOutlineRegular, {})
									})
								]
							}),
							/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
								className: QuestionComposer_module_css_default.feedback,
								role: "status",
								children: error === null ? null : "key" in error ? t(error.key) : error.text
							}),
							/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
								className: QuestionComposer_module_css_default.footerActions,
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
									variant: "outline",
									disabled: busy !== null,
									onClick: skipQuestion,
									children: t("action.skip")
								}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
									variant: "primary",
									disabled: busy !== null || !answered(draft),
									onClick: continueFlow,
									children: busy === "answer" ? t("submitting") : index === questions.length - 1 ? t("submit") : t("action.next")
								})]
							})
						]
					})] })]
				})
			});
		}
		//#endregion
		//#region src/client/locales.ts
		/** `question` namespace dictionaries. */
		/** Simplified Chinese dictionary (the key-set source of truth). */
		const zh = {
			"error.incomplete": "请先完成这道问题。",
			"error.unanswered": "请选择一个选项或填写自定义答案。",
			"nav.prev": "上一题",
			"nav.next": "下一题",
			"nav.minimize": "收起问题卡片",
			"nav.maximize": "展开问题卡片",
			"nav.cancel": "放弃整组问题",
			"option.recommended": "推荐",
			"custom.placeholder": "输入你的答案",
			"action.skip": "跳过",
			"action.next": "下一题",
			"plan.header": "计划待审",
			"plan.approve": "同意执行",
			"plan.decline": "拒绝",
			"plan.discuss": "要求修改"
		};
		/** English dictionary, checked complete against the zh key set. */
		const en = {
			"error.incomplete": "Please complete this question first.",
			"error.unanswered": "Please select an option or enter a custom answer.",
			"nav.prev": "Previous question",
			"nav.next": "Next question",
			"nav.minimize": "Collapse the question card",
			"nav.maximize": "Expand the question card",
			"nav.cancel": "Dismiss all questions",
			"option.recommended": "Recommended",
			"custom.placeholder": "Type your answer",
			"action.skip": "Skip",
			"action.next": "Next",
			"plan.header": "Plan review",
			"plan.approve": "Approve",
			"plan.decline": "Refuse",
			"plan.discuss": "Request changes"
		};
		//#endregion
		//#region src/client/index.ts
		/** Dictionary namespace owned by this plugin. */
		const NS = "question";
		/** Required services: Agent scopes, Remote Events, Session UI, Slot registry, and copy. */
		const inject = [
			"sessions",
			"remote",
			"uiSession",
			"slots",
			"locale"
		];
		/** Present one request until the user answers, cancels, or its lifetime ends. */
		async function answerQuestion(ctx, owner, request, next, registerPendingInteraction) {
			const sessionId = ctx.sessions.scopeOf(owner);
			if (sessionId === void 0) return next();
			const pending = new PendingQuestion(sessionId, request.questions, request.signal);
			const completed = Promise.withResolvers();
			const remove = registerPendingInteraction(pending, async () => {
				pending.delegate();
				await completed.promise;
			});
			try {
				try {
					return await pending.result;
				} catch (error) {
					if (pending.isDelegation(error)) return await next();
					throw error;
				}
			} finally {
				remove();
				completed.resolve();
			}
		}
		/**
		* Client plugin body: register the `question` dictionaries and the question
		* composer into the composer chain. Zero business face — data and verbs live
		* on the matched carrier; t rides the standard locale seat.
		* @param ctx - client root context.
		*/
		function apply(ctx) {
			ctx.effect(() => ctx.locale.register(NS, {
				zh,
				en
			}), "ui-user-questions: dictionaries");
			const questionDraftStore = createQuestionDraftStore();
			const registerPendingInteraction = ctx.uiSession.registerPendingInteraction((pending) => pending.kind === "plan-review" ? 2 : 1);
			ctx.slots.inject("conversation.composer", () => ctx.slots.register({
				name: "conversation.composer",
				select: ({ pendingInteraction }) => pendingInteraction instanceof PendingQuestion ? pendingInteraction : null,
				locale: NS,
				store: questionDraftStore,
				children: { "conversation.plan-review.actions": {
					kind: "list",
					scope: "session"
				} }
			}, QuestionComposer));
			ctx.remote.$on("user-questions/request", function(request, next) {
				return answerQuestion(ctx, this, request, next, registerPendingInteraction);
			});
		}
		//#endregion
		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map