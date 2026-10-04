window.__ModuleLoader__.load({
	id: "@deepseek-ai/dsh-client-ui-settings-shell",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		Object.defineProperty(exports, Symbol.toStringTag, { value: "Module" });
		let _deepseek_ai_dsh_client_ui_primitives = require("@deepseek-ai/dsh-client-ui-primitives");
		let react_jsx_runtime = require("react/jsx-runtime");
		//#region src/client/locales.ts
		/** English copy. */
		const en = {
			title: "Shell",
			description: "Limit how long each command may run and how much it may output.",
			timeoutMs: "Command timeout (ms)",
			timeoutMsHint: "How long one command may run before it is terminated.",
			maxOutputBytes: "Output cap per stream (bytes)",
			maxOutputBytesHint: "Output beyond this spills to a temporary file rather than being lost.",
			overridden: "Overridden",
			reset: "Reset to default",
			readOnly: "This deployment stores settings read-only.",
			unavailable: "This plugin is not loaded, so it cannot be configured right now.",
			save: "Save",
			saving: "Saving…",
			saveFailed: "The deployment did not accept these values; they were left for you to correct.",
			invalidNumber: "Enter a number, or leave blank to use the default."
		};
		/** Simplified Chinese copy. */
		const zh = {
			title: "终端",
			description: "限制每条命令最多能跑多久、最多输出多少内容。",
			timeoutMs: "命令超时（毫秒）",
			timeoutMsHint: "单条命令允许运行多久，超时即终止。",
			maxOutputBytes: "单流输出上限（字节）",
			maxOutputBytesHint: "超出部分会转存到临时文件，而不是被丢弃。",
			overridden: "已覆盖",
			reset: "恢复默认",
			readOnly: "本部署的设置为只读。",
			unavailable: "该插件当前未加载，暂时无法配置。",
			save: "保存",
			saving: "保存中…",
			saveFailed: "本部署没有接受这些值，已保留供你修改。",
			invalidNumber: "请填数字；留空表示使用默认值。"
		};
		/**
		* The form frame's copy, read from this page's dictionary.
		* @param t - the page's locale reader.
		* @returns the labels the shared settings form renders.
		*/
		function formLabels(t) {
			return {
				unavailable: t("unavailable"),
				readOnly: t("readOnly"),
				saveFailed: t("saveFailed"),
				save: t("save"),
				saving: t("saving")
			};
		}
		//#endregion
		//#region src/client/ShellCard.tsx
		/**
		* Render the shell executor's one-liner or its settings form, as the Plugins page asks.
		* @param props - the view asked for, locale copy, the form snapshot, and its actions.
		* @returns the one-liner, or the form.
		*/
		function ShellCard(props) {
			const { t } = props;
			const state = props.useShellCard((snapshot) => snapshot);
			if (props.view === "summary") return t("description");
			const disabled = !state.writable;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(_deepseek_ai_dsh_client_ui_primitives.SettingsForm, {
				labels: formLabels(t),
				state,
				onSave: props.save,
				onDiscard: props.discard,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.SettingsValueField, {
					id: "plugin-config-shell-timeout",
					label: t("timeoutMs"),
					hint: t("timeoutMsHint"),
					overriddenLabel: t("overridden"),
					resetLabel: t("reset"),
					invalidLabel: t("invalidNumber"),
					numeric: true,
					disabled,
					...state.timeoutMs,
					onEdit: (text) => {
						props.edit("timeoutMs", text);
					},
					onReset: () => {
						props.resetField("timeoutMs");
					}
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.SettingsValueField, {
					id: "plugin-config-shell-output",
					label: t("maxOutputBytes"),
					hint: t("maxOutputBytesHint"),
					overriddenLabel: t("overridden"),
					resetLabel: t("reset"),
					invalidLabel: t("invalidNumber"),
					numeric: true,
					disabled,
					...state.maxOutputBytes,
					onEdit: (text) => {
						props.edit("maxOutputBytes", text);
					},
					onReset: () => {
						props.resetField("maxOutputBytes");
					}
				})]
			});
		}
		//#endregion
		//#region src/client/shell-card-controller.ts
		/** Profile entry id of the POSIX shell executor; the base bundle composes it off Windows. */
		const BASH_NS = "bash-sandbox";
		/** Profile entry id of the PowerShell executor; the base bundle composes it on Windows. */
		const PWSH_NS = "pwsh-sandbox";
		/** Bridges one shell executor entry's form onto the page's staged form. */
		var ShellCardController = class {
			form;
			store;
			/** @param scope - the shared configuration form of the composed shell executor entry. */
			constructor(scope) {
				this.form = new _deepseek_ai_dsh_client_ui_primitives.SettingsFormModel(scope, [(0, _deepseek_ai_dsh_client_ui_primitives.settingsNumberField)("timeoutMs"), (0, _deepseek_ai_dsh_client_ui_primitives.settingsNumberField)("maxOutputBytes")]);
				this.store = this.form.bind(() => this.projection());
			}
			projection() {
				return {
					...this.form.shell(),
					timeoutMs: this.form.field("timeoutMs"),
					maxOutputBytes: this.form.field("maxOutputBytes")
				};
			}
			/**
			* Build the face the page's slot registration injects.
			* @returns the page's snapshot and its form actions.
			*/
			inject() {
				return {
					hooks: { shellCard: this.store },
					...this.form.actions()
				};
			}
			/** Release the form subscription. */
			dispose() {
				this.form.dispose();
			}
		};
		//#endregion
		//#region src/client/index.ts
		/** Dictionary namespace owned by this plugin. */
		const NS = "settings.shell";
		/** Required services (cordis fiber inject). */
		const inject = [
			"slots",
			"locale",
			"configForms"
		];
		/**
		* Mount the shell settings page while the Host serves its namespace.
		* @param ctx - the browser plugin context.
		*/
		function apply(ctx) {
			const t = ctx.locale.bind(NS);
			ctx.effect(() => ctx.locale.register(NS, {
				zh,
				en
			}), "ui-settings-shell: dictionaries");
			const bash = new ShellCardController(ctx.configForms.get(BASH_NS));
			const pwsh = new ShellCardController(ctx.configForms.get(PWSH_NS));
			ctx.effect(() => () => {
				bash.dispose();
				pwsh.dispose();
			}, "ui-settings-shell: form subscriptions");
			ctx.effect(() => ctx.configForms.whileServed([BASH_NS, PWSH_NS], (served) => ctx.slots.inject("plugins.item", () => ctx.slots.register({
				name: "plugins.item",
				id: "shell",
				order: 10,
				label: () => t("title"),
				locale: NS,
				inject: () => (served.has("pwsh-sandbox") ? pwsh : bash).inject()
			}, ShellCard))), "ui-settings-shell: page");
		}
		//#endregion
		exports.NS = NS;
		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map