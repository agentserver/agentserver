window.__ModuleLoader__.load({
	id: "@deepseek-ai/dsh-client-ui-sidebar-documentpreview",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		Object.defineProperty(exports, Symbol.toStringTag, { value: "Module" });
		let react = require("react");
		let _deepseek_ai_dsh_client_ui_primitives = require("@deepseek-ai/dsh-client-ui-primitives");
		let react_jsx_runtime = require("react/jsx-runtime");
		let _deepseek_ai_dsh_client_store = require("@deepseek-ai/dsh-client-store");
		let react_dom = require("react-dom");
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
		/** Whether a decoded first path segment is a Windows drive (`C:`). */
		function isDriveSegment(segment) {
			return segment !== void 0 && /^[A-Za-z]:$/.test(segment);
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
		/**
		* Read a file address back into its parts without resolving `.` or `..`.
		* Query and fragment suffixes are ignored; encoded path segments are decoded.
		* @param address - a candidate address.
		* @returns the parts, or `undefined` when the string is not a `dsh-resource://file/` URI in a known scope with a path, or a segment is not validly encoded.
		*/
		function parseFileAddress(address) {
			try {
				if (!address.startsWith(FILE_ADDRESS_PREFIX)) return void 0;
				const end = address.search(/[?#]/);
				const [scope, ...rest] = address.slice(20, end === -1 ? void 0 : end).split("/");
				if (scope === "session") {
					const [id, ...segments] = rest;
					if (id === void 0 || id === "" || segments.length === 0) return void 0;
					return {
						scope,
						sessionId: decodeURIComponent(id),
						path: segments.map(decodeURIComponent).join("/")
					};
				}
				if (scope === "absolute") {
					const unc = rest[0] === "" && rest.length > 1;
					const segments = (unc ? rest.slice(1) : rest).map(decodeURIComponent);
					if (segments.length === 0 || segments[0] === "") return void 0;
					if (unc) return {
						scope,
						path: `//${segments.join("/")}`
					};
					return {
						scope,
						path: isDriveSegment(segments[0]) ? segments.join("/") : `/${segments.join("/")}`
					};
				}
				return;
			} catch {
				return;
			}
		}
		//#endregion
		//#region ../../util/workspace-path/src/index.ts
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
		* Split a path for display: the directories through their last separator, and
		* the final segment after it. Both `/` and `\` separate, so a Windows path
		* splits where its own segments end; trailing separators are dropped first, so
		* a directory path names its own last segment. A path with no separator, or a
		* separator-only path, is all name.
		* @param path - file or directory path using POSIX or Windows separators.
		* @returns the directory prefix (possibly empty) and the final segment.
		*/
		function pathPartsOf(path) {
			const trimmed = path.replace(/[/\\]+$/, "");
			if (trimmed === "") return {
				directory: "",
				name: path
			};
			const cut = Math.max(trimmed.lastIndexOf("/"), trimmed.lastIndexOf("\\")) + 1;
			return {
				directory: trimmed.slice(0, cut),
				name: trimmed.slice(cut)
			};
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
		//#region src/client/failure-line.ts
		/** Render a byte count the way a person reads one. */
		function humanBytes(bytes) {
			if (bytes >= 1024 * 1024) return `${Math.round(bytes / (1024 * 1024))} MB`;
			if (bytes >= 1024) return `${Math.round(bytes / 1024)} KB`;
			return `${bytes} B`;
		}
		/**
		* Say what went wrong, in terms of the file rather than of the transport.
		* @param t - namespace-bound translate.
		* @param failure - the settled Remote failure.
		* @returns the line to show in place of the file.
		*/
		function failureLine(t, failure) {
			switch (failure.code) {
				case "workspace-file/not-found": return t("error.notFound");
				case "workspace-file/too-large": return t("error.tooLarge", { limit: humanBytes(failure.details.limit) });
				case "workspace-file/not-text": return t("error.notText");
				case "workspace-file/not-regular-file": return t("error.notRegularFile");
				default: return t("error.unavailable", { message: failure.message });
			}
		}
		/**
		* Choose the empty state's action for one settled read failure.
		* @param failure - the settled Remote failure.
		* @returns `open` for a readable file this preview cannot render, `none` for a
		* path with nothing to show or open, `retry` for the rest: carrier and
		* unclassified failures a second read may resolve.
		*/
		function emptyFailureRecourse(failure) {
			switch (failure.code) {
				case "workspace-file/not-text":
				case "workspace-file/too-large": return "open";
				case "workspace-file/not-found":
				case "workspace-file/not-regular-file": return "none";
				default: return "retry";
			}
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-sidebar-documentpreview/src/client/LoadingIndicator.module.css.mjs
		const css$9 = ".HAKWsq_loading{box-sizing:border-box;width:100%;height:100%;min-height:0;color:var(--dsw-alias-label-secondary);font-family:var(--dsw-font,sans-serif);font-size:var(--dsh-content-font-size,14px);text-align:center;white-space:normal;flex-direction:column;justify-content:center;align-items:center;gap:12px;padding:24px;line-height:1.5;display:flex}.HAKWsq_inline{vertical-align:middle;flex-direction:row;gap:8px;width:auto;height:auto;padding:0;display:inline-flex}";
		const tagId$9 = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview/LoadingIndicator.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$9) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview";
			tag.dataset.pluginCss = tagId$9;
			tag.textContent = css$9;
			document.head.appendChild(tag);
		}
		var LoadingIndicator_module_css_default = {
			"inline": "HAKWsq_inline",
			"loading": "HAKWsq_loading"
		};
		//#endregion
		//#region src/client/LoadingIndicator.tsx
		/**
		* @param props - localized status label and compact inline placement for additional pages.
		* @returns a centered document loading status or an accessible inline spinner.
		*/
		function LoadingIndicator({ label, inline = false }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
				className: clsx(LoadingIndicator_module_css_default.loading, inline && LoadingIndicator_module_css_default.inline),
				role: "status",
				"aria-label": label,
				"data-document-loading": true,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, {
					state: "ongoing",
					size: inline ? 14 : 28
				}), !inline && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: label })]
			});
		}
		//#endregion
		//#region src/client/rpc.ts
		/**
		* The session and path one `dsh-resource://file/…` address names.
		*
		* A `session` address names its own session and a relative or absolute path, so
		* a tab addressed into another session reads from that session. An `absolute`
		* address carries no session and cannot be read here. The registry routes only
		* session-scoped `file` addresses to this type, so an address `parseFileAddress`
		* rejects or that carries no session is a programming error and throws.
		* @param address - a tab's `dsh-resource://file/…` address.
		* @returns the session and the path to hand the endpoint.
		*/
		function hostFileOf(address) {
			const parsed = parseFileAddress(address);
			if (parsed?.scope !== "session") throw new Error(`ui-sidebar-documentpreview: not a session file address "${address}"`);
			return {
				sessionId: parsed.sessionId,
				path: parsed.path
			};
		}
		/**
		* Bind the paged read to one Remote face. The page length is the Host's
		* configured cap, so no `limit` travels.
		* @param remote - the Client Remote carrying the `workspaceFiles` namespace.
		* @returns the read the face performs.
		*/
		function createReadPage(remote) {
			return (sessionId, path, offset, signal) => remote.workspaceFiles.read(sessionId, path, { offset }, signal);
		}
		//#endregion
		//#region src/client/document/suffix.ts
		/**
		* Filename suffix matching shared by the preview registry and the owner's
		* unviewable list, so every consumer normalizes paths and suffixes alike.
		*/
		/**
		* Normalize one declared suffix for comparison.
		* @param extension - declared file suffix, with or without a leading dot.
		* @returns the suffix lowercased with any leading dot dropped.
		*/
		function normalizeSuffix(extension) {
			return extension.toLowerCase().replace(/^\./u, "");
		}
		/**
		* The filename a path's suffixes are matched against.
		* @param path - decoded filename or file path; `\` is accepted as a separator.
		* @returns the lowercased final path segment.
		*/
		function documentFileName(path) {
			const normalized = path.replaceAll("\\", "/").toLowerCase();
			return normalized.slice(normalized.lastIndexOf("/") + 1);
		}
		/**
		* The longest declared suffix ending the filename.
		* @param name - lowercased filename from {@link documentFileName}.
		* @param extensions - declared suffixes; compound suffixes such as `tar.gz` are accepted.
		* @returns the matched suffix's normalized length, or 0 when none matches.
		*/
		function matchedSuffixLength(name, extensions) {
			return Math.max(0, ...extensions.map(normalizeSuffix).filter((extension) => name.endsWith(`.${extension}`)).map((extension) => extension.length));
		}
		//#endregion
		//#region src/client/document/registry.ts
		/** File-extension preview registrations; component dispatch belongs to the keyed document slot. */
		/**
		* Rank an observed definition snapshot without consulting mutable service state.
		* @param definitions - registered implementations in registration order.
		* @param path - decoded filename or file path.
		* @returns matching implementations, external band first, then longest suffix.
		*/
		function matchingDocumentPreviews(definitions, path) {
			const name = documentFileName(path);
			return definitions.map((definition, order) => ({
				definition,
				order,
				rank: definition.priority === "builtin" ? 0 : 1,
				length: matchedSuffixLength(name, definition.extensions)
			})).filter((candidate) => candidate.length > 0).sort((left, right) => right.rank - left.rank || right.length - left.length || left.order - right.order).map((candidate) => candidate.definition);
		}
		/**
		* Whether any registered implementation declares the filename's suffix binary.
		* @param definitions - registered implementations.
		* @param path - decoded filename or file path.
		* @returns true when a declared binary suffix matches the filename.
		*/
		function binaryDocumentPath(definitions, path) {
			const name = documentFileName(path);
			return definitions.some((definition) => matchedSuffixLength(name, definition.binaryExtensions ?? []) > 0);
		}
		/** Observable registry of all live implementations, including lower-priority alternatives. */
		var DocumentPreviewRegistry = class {
			registered = /* @__PURE__ */ new Map();
			listeners = /* @__PURE__ */ new Set();
			snapshot = [];
			/**
			* Read the current registrations.
			* @returns the same snapshot until a registration changes.
			*/
			getSnapshot = () => this.snapshot;
			/**
			* Observe registration changes.
			* @param listener - registration-change observer.
			* @returns its disposer.
			*/
			subscribe = (listener) => {
				this.listeners.add(listener);
				return () => {
					this.listeners.delete(listener);
				};
			};
			/**
			* Register metadata separately from the matching keyed slot component.
			* @param definition - unique implementation and recognized suffixes; every
			* `binaryExtensions` entry must appear in `extensions`.
			* @returns an idempotent disposer; duplicate live implementation names and
			* binary suffixes outside `extensions` throw.
			*/
			register(definition) {
				if (this.registered.has(definition.id)) throw new Error(`documentPreviews: duplicate implementation "${definition.id}"`);
				const declared = new Set(definition.extensions.map(normalizeSuffix));
				for (const extension of definition.binaryExtensions ?? []) if (!declared.has(normalizeSuffix(extension))) throw new Error(`documentPreviews: "${definition.id}" declares binary suffix "${extension}" outside its extensions`);
				this.registered.set(definition.id, definition);
				this.publish();
				let active = true;
				return () => {
					if (!active) return;
					active = false;
					this.registered.delete(definition.id);
					this.publish();
				};
			}
			/**
			* List every matching implementation in automatic-selection order.
			* @param path - decoded file path; matching never resolves filesystem access.
			* @returns extension band first, then longest suffix, then registration order.
			*/
			candidates(path) {
				return matchingDocumentPreviews(this.snapshot, path);
			}
			publish() {
				this.snapshot = [...this.registered.values()];
				(0, _deepseek_ai_dsh_client_store.notifySubscribers)(this.listeners, "[document-previews] registry");
			}
		};
		//#endregion
		//#region src/client/document/unviewable.ts
		/**
		* Known binary container suffixes with no registered renderer. The preview
		* owner shows these the unsupported empty state instead of the plain-text
		* fallback; any renderer registration for a suffix takes precedence because
		* the owner consults this list only when no implementation matches. A suffix
		* belongs here only when its bytes are never readable text — an uncertain
		* suffix stays out and keeps the plain-text fallback.
		*/
		const UNVIEWABLE_BINARY_EXTENSIONS = [
			"mp4",
			"mov",
			"avi",
			"mkv",
			"webm",
			"flv",
			"wmv",
			"m4v",
			"mp3",
			"wav",
			"flac",
			"ogg",
			"m4a",
			"aac",
			"wma",
			"opus",
			"zip",
			"gz",
			"tgz",
			"bz2",
			"xz",
			"zst",
			"7z",
			"rar",
			"tar",
			"jar",
			"doc",
			"docx",
			"xls",
			"xlsx",
			"ppt",
			"pptx",
			"odt",
			"ods",
			"odp",
			"pages",
			"numbers",
			"exe",
			"dll",
			"so",
			"dylib",
			"bin",
			"o",
			"class",
			"pyc",
			"wasm",
			"ttf",
			"otf",
			"woff",
			"woff2",
			"eot",
			"dmg",
			"iso",
			"img",
			"sqlite",
			"db",
			"psd",
			"ai",
			"sketch",
			"tiff",
			"tif",
			"heic",
			"heif",
			"avif"
		];
		/**
		* Whether a filename's suffix is a known binary container that no renderer claims.
		* @param path - decoded filename or file path.
		* @returns true when the suffix belongs to the unviewable binary list.
		*/
		function unviewableBinaryPath(path) {
			return matchedSuffixLength(documentFileName(path), UNVIEWABLE_BINARY_EXTENSIONS) > 0;
		}
		//#endregion
		//#region src/client/text/lines.ts
		/**
		* Split a loaded page into its source lines.
		* @param page - source page.
		* @returns its lines, preserving one empty line but excluding a zero-line page.
		*/
		function linesOf(page) {
			return page.lines === 0 ? [] : page.text.split("\n");
		}
		/**
		* Order loaded pages by source position.
		* @param pages - stored page table.
		* @returns pages in source order.
		*/
		function loadedPages(pages) {
			return Object.entries(pages).map(([offset, page]) => ({
				offset: Number(offset),
				...page
			})).sort((left, right) => left.offset - right.offset);
		}
		/**
		* Find the end of the loaded source prefix.
		* @param pages - ordered pages.
		* @returns the last loaded source line, or zero.
		*/
		function lastLineLoaded(pages) {
			const last = pages.at(-1);
			return last === void 0 ? 0 : last.offset + last.lines - 1;
		}
		/**
		* Reveal a plain-text or highlighted source line.
		* @param body - scrolling document body or code-content viewport.
		* @param line - 1-based source line to reveal.
		* @returns Whether the current renderer exposes that line.
		*/
		function scrollToLine(body, line) {
			const innerCode = body.hasAttribute("data-code-block-content");
			const plain = body.querySelector(`[data-textpreview-line="${line}"]`);
			const code = innerCode ? body.querySelectorAll("pre .line").item(line - 1) : null;
			const row = plain ?? code;
			if (!(row instanceof HTMLElement)) return false;
			body.scrollTop = Math.max(0, row.offsetTop);
			return true;
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-sidebar-documentpreview/src/client/TextPreview.module.css.mjs
		const css$8 = ".aL4q3G_preview{flex-direction:column;flex:auto;height:100%;min-height:0;display:flex}.aL4q3G_header{box-sizing:border-box;border-bottom:.5px solid var(--dsw-alias-border-l3);flex:none;align-items:center;gap:4px;height:38px;padding:0 6px 0 16px;display:flex}.aL4q3G_path{margin-right:12px}.aL4q3G_changed{color:var(--dsw-alias-label-secondary);background:var(--dsw-alias-bg-layer-2);border-bottom:.5px solid var(--dsw-alias-border-l1);flex:none;align-items:center;gap:10px;margin:0;padding:6px 10px;font-size:12px;display:flex}.aL4q3G_body{--dsh-scrollbar-thumb:var(--dsw-alias-scrollbar-bg-l2);--dsh-scrollbar-thumb-hover:var(--dsw-alias-scrollbar-hover-l2);min-height:0;color:var(--dsw-alias-label-primary);font-size:var(--dsh-content-font-size-secondary,13px);font-family:var(--dsw-font-mono,ui-monospace, monospace);white-space:pre;flex:auto;margin:0;padding:0;line-height:1.6;position:relative;overflow:auto}.aL4q3G_body::-webkit-scrollbar-track{margin:2px}.aL4q3G_body:has([data-code-preview]){flex-direction:column;display:flex;overflow:hidden}.aL4q3G_body:has([data-pdf-preview]){background:var(--dsw-alias-bg-document-preview)}.aL4q3G_wrap{white-space:pre-wrap;word-break:break-word}.aL4q3G_textDocument{box-sizing:border-box;min-width:100%;min-height:100%;padding:8px}.aL4q3G_page{font:var(--dsw-font-markdown-code-block);white-space:inherit;margin:0}.aL4q3G_line{padding:0 10px}.aL4q3G_lineTarget{background:var(--dsw-alias-interactive-bg-hover)}.aL4q3G_empty{box-sizing:border-box;height:100%;color:var(--dsw-alias-label-secondary);font-size:var(--dsh-content-font-size-secondary,13px);font-family:var(--dsw-font-family);text-align:center;white-space:normal;flex-direction:column;justify-content:center;align-items:center;gap:16px;padding:0 24px;line-height:1.6;display:flex}.aL4q3G_empty:after{content:\"\";flex:0 12%}.aL4q3G_emptyIcon{opacity:.6;filter:grayscale();flex:none}.aL4q3G_emptyLine{margin:0}.aL4q3G_retry{height:32px;color:var(--dsw-alias-label-primary);font-size:var(--dsh-content-font-size-secondary,13px);border:.5px solid var(--dsw-alias-border-l2);border-radius:var(--dsw-radius-md);cursor:pointer;background:0 0;flex:none;align-items:center;gap:6px;padding:0 14px 0 12px;font-family:inherit;display:inline-flex}.aL4q3G_retry:hover{background:var(--dsw-alias-interactive-bg-hover)}.aL4q3G_titleIcon{flex:none}.aL4q3G_statusLine{color:var(--dsw-alias-label-secondary);font-size:var(--dsh-content-font-size-secondary,13px);white-space:normal;align-items:center;gap:10px;margin:0;padding:6px 10px;line-height:1.6;display:flex}.aL4q3G_status{box-sizing:border-box;flex-direction:column;justify-content:center;align-items:center;gap:8px;height:100%;padding:12px 10px;display:flex}.aL4q3G_action{color:var(--dsw-alias-label-primary);font-size:var(--dsh-content-font-size-secondary,13px);font-family:var(--dsw-font,inherit);white-space:normal;background:var(--dsw-alias-bg-layer-2);border:.5px solid var(--dsw-alias-border-l2);border-radius:var(--dsw-radius-sm);cursor:pointer;padding:4px 10px}.aL4q3G_action:hover{background:var(--dsw-alias-bg-layer-3)}.aL4q3G_more{color:var(--dsw-alias-label-secondary);font-size:12px;font-family:var(--dsw-font,inherit);white-space:normal;background:var(--dsw-alias-bg-layer-2);border:.5px solid var(--dsw-alias-border-l2);border-radius:var(--dsw-radius-sm);cursor:pointer;margin:8px 10px;padding:4px 10px;display:block}.aL4q3G_more:hover:not(:disabled){color:var(--dsw-alias-label-primary);background:var(--dsw-alias-bg-layer-3)}.aL4q3G_more:disabled{color:var(--dsw-alias-label-tertiary);cursor:default}.aL4q3G_tool{width:28px;height:28px;color:var(--dsw-alias-label-secondary);border-radius:var(--dsw-radius-sm);cursor:pointer;background:0 0;border:none;flex:none;justify-content:center;align-items:center;padding:6px;line-height:1;display:inline-flex}.aL4q3G_tool svg{width:15px;height:15px}.aL4q3G_tool:hover,.aL4q3G_tool[data-textpreview-tool=auto-refresh][aria-pressed=true]{color:var(--dsw-alias-label-primary);background:var(--dsw-alias-interactive-bg-hover)}.aL4q3G_viewerTool{width:auto;max-width:160px;color:var(--dsw-alias-label-secondary);white-space:nowrap;text-overflow:ellipsis;flex:none;padding:0 6px;font-size:12px;overflow:hidden}";
		const tagId$8 = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview/TextPreview.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$8) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview";
			tag.dataset.pluginCss = tagId$8;
			tag.textContent = css$8;
			document.head.appendChild(tag);
		}
		var TextPreview_module_css_default = {
			"action": "aL4q3G_action",
			"body": "aL4q3G_body",
			"changed": "aL4q3G_changed",
			"empty": "aL4q3G_empty",
			"emptyIcon": "aL4q3G_emptyIcon",
			"emptyLine": "aL4q3G_emptyLine",
			"header": "aL4q3G_header",
			"line": "aL4q3G_line",
			"lineTarget": "aL4q3G_lineTarget",
			"more": "aL4q3G_more",
			"page": "aL4q3G_page",
			"path": "aL4q3G_path",
			"preview": "aL4q3G_preview",
			"retry": "aL4q3G_retry",
			"status": "aL4q3G_status",
			"statusLine": "aL4q3G_statusLine",
			"textDocument": "aL4q3G_textDocument",
			"titleIcon": "aL4q3G_titleIcon",
			"tool": "aL4q3G_tool",
			"viewerTool": "aL4q3G_viewerTool",
			"wrap": "aL4q3G_wrap"
		};
		//#endregion
		//#region src/client/text/TextBody.tsx
		/** @param props - document contents and standard tab information. @returns source lines with navigation targets. */
		function TextBody({ content, useTabInfo }) {
			const { tab } = useTabInfo();
			const params = tab.navigation.params;
			const target = params !== void 0 && "line" in params ? params.line : void 0;
			if (content.kind !== "text") return null;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: TextPreview_module_css_default.textDocument,
				"data-textpreview-plain": true,
				children: content.pages.map((page) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)("pre", {
					className: TextPreview_module_css_default.page,
					"data-textpreview-page": page.offset,
					children: linesOf(page).map((text, index) => {
						const number = page.offset + index;
						return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							className: clsx(TextPreview_module_css_default.line, number === target && TextPreview_module_css_default.lineTarget),
							"data-textpreview-line": number,
							...number === target ? { "data-textpreview-target": number } : {},
							children: [text, "\n"]
						}, number);
					})
				}, page.offset))
			});
		}
		//#endregion
		//#region src/client/text/index.ts
		/** Stable plain-text implementation identity within this package. */
		const PLAIN_BODY_ID = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview/text";
		/**
		* Describe the plain-text fallback.
		* @param title - locale-owned implementation name.
		* @returns plain-text registration metadata.
		*/
		function textBodyDefinition(title) {
			return {
				id: PLAIN_BODY_ID,
				extensions: [],
				priority: "builtin",
				title,
				loading: "text-pages",
				wrap: true
			};
		}
		/** @param ctx - owning plugin context. Register the fallback metadata and keyed body. */
		function apply$9(ctx) {
			const t = ctx.locale.bind("sidebarDocumentPreview");
			ctx.effect(() => ctx.documentPreviews.register(textBodyDefinition(() => t("viewer.text"))));
			ctx.effect(() => ctx.slots.inject("sidebar.right.tab.document", () => ctx.slots.register({
				name: "sidebar.right.tab.document",
				key: PLAIN_BODY_ID
			}, TextBody)));
		}
		//#endregion
		//#region src/client/TextPreview.tsx
		/**
		* The text preview's body: a file's content, or the reason it is not showing.
		*
		* Two sources meet here. The standard `useResource` hook gives the file's
		* metadata — its version — and this type's
		* own store holds the content it read through its face. Metadata changes reload
		* the current preview while automatic refresh is enabled. A failed metadata frame — the file gone, its
		* workspace unknown — takes the same bar's place over the pages already loaded,
		* with the same reload. The type's controls, viewer choice, wrap and reload, sit at the end of
		* the path row; the Sidebar's strip carries none of them.
		*/
		/**
		* The text type's body, registered under `sidebar.right.pane.tab` as `text`.
		* @param props - composed slot props.
		* @returns the content read so far with its controls, or a progress line.
		*/
		function TextPreview({ useTabInfo, useResource, useStore, actions, loadPage, reloadPages, loadAll, reloadAll, prepareRenderer, useDocumentPreviews, renderSlot, t, addResource, setResources }) {
			const { tab } = useTabInfo();
			const { navigation, signal } = tab;
			const meta = useResource(tab.contentId);
			const canRead = meta.status !== "none";
			const file = (0, react.useMemo)(() => hostFileOf(tab.contentId), [tab.contentId]);
			const state = useStore((s) => s.byTab[tab.id]);
			const definitions = useDocumentPreviews((value) => value);
			const unviewable = (0, react.useMemo)(() => unviewableBinaryPath(file.path), [file.path]);
			const candidates = (0, react.useMemo)(() => {
				const matched = matchingDocumentPreviews(definitions, file.path);
				if (matched.length > 0 && binaryDocumentPath(definitions, file.path)) return matched;
				if (matched.length === 0 && unviewable) return matched;
				const fallback = definitions.find((definition) => definition.id === PLAIN_BODY_ID);
				return fallback === void 0 ? matched : [...matched, fallback];
			}, [
				definitions,
				file.path,
				unviewable
			]);
			const selected = candidates.find((candidate) => candidate.id === state?.rendererId) ?? candidates[0];
			const mode = selected?.loading;
			const contentRendererId = mode === "renderer" ? selected?.id : void 0;
			const current = (state?.mode ?? "text-pages") === mode && state?.contentRendererId === contentRendererId ? state : void 0;
			const add = (0, react.useCallback)((address) => {
				addResource(tab.id, address, signal);
			}, [
				addResource,
				tab.id,
				signal
			]);
			const set = (0, react.useCallback)((addresses) => {
				setResources(tab.id, [tab.contentId, ...addresses], signal);
			}, [
				setResources,
				tab.id,
				tab.contentId,
				signal
			]);
			(0, react.useEffect)(() => {
				set([]);
			}, [set, selected?.id]);
			const bodyRef = (0, react.useRef)(null);
			const scrollportRef = (0, react.useRef)(null);
			const storedScrollTopRef = (0, react.useRef)(0);
			const [menuOpen, setMenuOpen] = (0, react.useState)(false);
			const absolutePath = meta.value?.absolutePath ?? current?.complete?.absolutePath;
			const displayPath = absolutePath ?? file.path;
			const fileOwner = absolutePath === void 0 ? void 0 : { absolutePath };
			const line = navigation.params !== void 0 && "line" in navigation.params ? navigation.params.line : void 0;
			const pages = current?.pages;
			const loaded = (0, react.useMemo)(() => loadedPages(pages ?? {}), [pages]);
			const loadedThrough = lastLineLoaded(loaded);
			const hasContent = mode === "renderer" ? current?.version !== void 0 : loaded.length > 0 || current?.complete !== void 0;
			storedScrollTopRef.current = state?.scrollTop ?? 0;
			const bindBody = (0, react.useCallback)((body) => {
				const previous = bodyRef.current;
				bodyRef.current = body;
				if (scrollportRef.current === null || scrollportRef.current === previous) scrollportRef.current = body;
			}, []);
			const bindScrollport = (0, react.useCallback)((scrollport) => {
				const next = scrollport ?? bodyRef.current;
				scrollportRef.current = next;
				if (next !== null) next.scrollTop = storedScrollTopRef.current;
			}, []);
			const started = current !== void 0;
			(0, react.useEffect)(() => {
				if (started || !canRead || mode === void 0 || selected === void 0) return;
				if (mode === "text-pages") loadPage(tab.id, file, 1, signal, meta.value?.version);
				else if (mode === "bytes-complete") loadAll(tab.id, file, signal, meta.value?.version);
				else prepareRenderer(tab.id, signal, selected.id, meta.value?.version);
			}, [
				started,
				tab.id,
				file,
				signal,
				loadPage,
				loadAll,
				prepareRenderer,
				canRead,
				mode,
				selected,
				meta.value?.version
			]);
			(0, react.useEffect)(() => {
				const body = scrollportRef.current;
				if (hasContent && body !== null && state !== void 0) body.scrollTop = state.scrollTop;
			}, [hasContent, selected?.id]);
			(0, react.useEffect)(() => {
				const body = scrollportRef.current;
				if (current === void 0 || body === null || current.revision === navigation.revision) return;
				if (line === void 0 || mode !== "text-pages") {
					actions.navigated(tab.id, navigation.revision);
					return;
				}
				if (line > loadedThrough && !current.eof) {
					if (!current.loading && current.failure === void 0 && canRead) loadPage(tab.id, file, loadedThrough + 1, signal, meta.value?.version);
					return;
				}
				if (!scrollToLine(body, line) && line <= loadedThrough) return;
				actions.navigated(tab.id, navigation.revision);
				actions.scrolled(tab.id, body.scrollTop);
			}, [
				navigation.revision,
				line,
				loadedThrough,
				current?.eof,
				current?.loading,
				current?.failure,
				started,
				selected?.id,
				mode,
				file,
				canRead,
				meta.value?.version
			]);
			const rendererReload = (0, react.useCallback)(() => {
				if (canRead && selected !== void 0) prepareRenderer(tab.id, signal, selected.id, meta.value?.version, true);
			}, [
				canRead,
				prepareRenderer,
				tab.id,
				signal,
				selected?.id,
				meta.value?.version
			]);
			const observedVersion = meta.value?.version;
			const changed = current?.version !== void 0 && observedVersion !== void 0 && observedVersion !== current.version && observedVersion !== current.observedVersion || state?.resourcesDirty === true;
			const reload = (0, react.useCallback)(() => {
				if (!canRead) return;
				if (mode === "text-pages") reloadPages(tab.id, file, signal, observedVersion);
				else if (mode === "bytes-complete") reloadAll(tab.id, file, signal, observedVersion);
				else rendererReload();
			}, [
				canRead,
				mode,
				reloadPages,
				reloadAll,
				rendererReload,
				tab.id,
				file,
				signal,
				observedVersion
			]);
			(0, react.useEffect)(() => tab.actions.bindCommands({ refresh: reload }), [tab.actions, reload]);
			(0, react.useEffect)(() => {
				if (state?.autoRefresh && changed && current !== void 0 && !current.loading && meta.status === "live") reload();
			}, [
				state?.autoRefresh,
				changed,
				current?.loading,
				meta.status,
				reload
			]);
			const content = (0, react.useMemo)(() => {
				if (mode === "renderer") {
					if (current === void 0) return void 0;
					const revision = current.loadRevision;
					return {
						kind: "renderer",
						revision,
						reload: rendererReload,
						failed: () => {
							actions.rendererFailed(tab.id, revision);
						},
						loaded: (version) => {
							actions.rendered(tab.id, revision, version);
						}
					};
				}
				if (mode === "bytes-complete") return current?.complete === void 0 ? void 0 : {
					kind: "bytes",
					data: current.complete.data
				};
				if (current === void 0 || loaded.length === 0) return void 0;
				return {
					kind: "text",
					pages: loaded,
					text: loaded.filter((page) => page.lines > 0).map((page) => page.text).join("\n"),
					eof: current.eof
				};
			}, [
				mode,
				loaded,
				current?.complete,
				current?.eof,
				current?.loadRevision,
				rendererReload,
				actions,
				tab.id
			]);
			if (selected === void 0 && unviewable) {
				const { name: unsupportedName } = pathPartsOf(displayPath);
				return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: TextPreview_module_css_default.preview,
					"data-textpreview-state": "unsupported",
					"data-textpreview-url": tab.contentId,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: TextPreview_module_css_default.header,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.PathLabel, {
							path: displayPath,
							className: TextPreview_module_css_default.path,
							"data-textpreview-path": true
						}), fileOwner !== void 0 && renderSlot("sidebar.right.tab.document.actions", fileOwner)]
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						className: TextPreview_module_css_default.body,
						"data-textpreview-body": true,
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							className: TextPreview_module_css_default.empty,
							"data-textpreview-unsupported": true,
							children: [
								/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.FileTypeIcon, {
									kind: (0, _deepseek_ai_dsh_client_ui_primitives.classifyFileType)(unsupportedName),
									size: 36,
									className: TextPreview_module_css_default.emptyIcon
								}),
								/* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
									className: TextPreview_module_css_default.emptyLine,
									children: t("unsupportedFile")
								}),
								fileOwner !== void 0 && renderSlot("sidebar.right.tab.document.unpreviewable", fileOwner)
							]
						})
					})]
				});
			}
			if (state === void 0 || selected === void 0) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: TextPreview_module_css_default.status,
				"data-textpreview-state": "loading",
				children: meta.status === "none" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
					className: TextPreview_module_css_default.statusLine,
					children: t("resourceUnavailable")
				}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(LoadingIndicator, { label: t("loading") })
			});
			const next = loadedThrough + 1;
			const { name } = pathPartsOf(displayPath);
			const loadNext = () => {
				if (!canRead || current?.loading || current?.eof) return;
				loadPage(tab.id, file, next, signal, meta.value?.version);
			};
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: TextPreview_module_css_default.preview,
				"data-textpreview-state": "text",
				"data-textpreview-url": tab.contentId,
				"data-document-preview": selected.id,
				children: [
					meta.failure !== void 0 && hasContent ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("p", {
						className: TextPreview_module_css_default.changed,
						"data-textpreview-meta-failed": meta.failure.code,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: failureLine(t, meta.failure) }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
							type: "button",
							className: TextPreview_module_css_default.action,
							"data-textpreview-reload-now": true,
							onClick: reload,
							children: t("reloadNow")
						})]
					}) : changed && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("p", {
						className: TextPreview_module_css_default.changed,
						"data-textpreview-changed": true,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: t("changed") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
							type: "button",
							className: TextPreview_module_css_default.action,
							"data-textpreview-reload-now": true,
							onClick: reload,
							children: t("reloadNow")
						})]
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: TextPreview_module_css_default.header,
						children: [
							/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.PathLabel, {
								path: displayPath,
								className: TextPreview_module_css_default.path,
								"data-textpreview-path": true
							}),
							candidates.length > 1 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Menu, {
								open: menuOpen,
								anchor: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
									type: "button",
									className: clsx(TextPreview_module_css_default.tool, TextPreview_module_css_default.viewerTool),
									"aria-label": t("openWith"),
									title: selected.title(),
									"data-document-viewer-menu": true,
									onClick: () => {
										setMenuOpen((value) => !value);
									},
									children: selected.title()
								}),
								items: candidates.map((candidate) => ({
									id: candidate.id,
									label: candidate.title()
								})),
								selectedId: selected.id,
								onSelect: (id) => {
									actions.selected(tab.id, id);
									setMenuOpen(false);
								},
								onClose: () => {
									setMenuOpen(false);
								},
								align: "end",
								portal: true,
								dense: true
							}),
							selected.wrap === true && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
								label: t(state.wrap ? "wrap.disable" : "wrap.enable"),
								side: "bottom",
								delayMs: 500,
								children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
									type: "button",
									className: TextPreview_module_css_default.tool,
									"aria-pressed": state.wrap,
									"aria-label": t("wrap.aria"),
									"data-textpreview-tool": "wrap",
									onClick: () => {
										actions.toggledWrap(tab.id);
									},
									children: state.wrap ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconNowrapFillRegular, {}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconWrapFillRegular, {})
								})
							}),
							content !== void 0 && renderSlot("sidebar.right.tab.document.action", { content }, {
								entryKey: selected.id,
								hookContext: useTabInfo
							}),
							/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
								hidden: true,
								children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
									label: t(state.autoRefresh ? "autoRefresh.disable" : "autoRefresh.enable"),
									side: "bottom",
									delayMs: 500,
									children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
										type: "button",
										className: TextPreview_module_css_default.tool,
										"aria-label": t("autoRefresh"),
										"aria-pressed": state.autoRefresh,
										"data-textpreview-tool": "auto-refresh",
										onClick: () => {
											actions.toggledAutoRefresh(tab.id);
										},
										children: state.autoRefresh ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconPauseOutlineRegular, {}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconPlayOutlineRegular, {})
									})
								})
							}),
							/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
								label: t("reload"),
								shortcutKeys: tab.refreshShortcut?.keys,
								side: "bottom",
								delayMs: 500,
								children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
									type: "button",
									className: TextPreview_module_css_default.tool,
									"aria-label": t("reload"),
									"data-textpreview-tool": "reload",
									"aria-keyshortcuts": tab.refreshShortcut?.aria,
									onClick: reload,
									children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconRefreshOutlineRegular, {})
								})
							}),
							fileOwner !== void 0 && renderSlot("sidebar.right.tab.document.actions", fileOwner)
						]
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						ref: bindBody,
						className: clsx(TextPreview_module_css_default.body, state.wrap && TextPreview_module_css_default.wrap),
						"data-textpreview-body": true,
						"data-textpreview-wrap": state.wrap ? "" : void 0,
						onScrollCapture: (event) => {
							const body = scrollportRef.current;
							/* v8 ignore next -- callback refs bind the scrollport during commit, before user input. */
							if (body === null) return;
							if (event.target !== body) return;
							actions.scrolled(tab.id, body.scrollTop);
							if (mode === "text-pages" && current?.failure === void 0 && body.clientHeight > 0 && body.scrollTop + body.clientHeight >= body.scrollHeight - 1) loadNext();
						},
						children: [
							mode !== "renderer" && !hasContent && current?.failure === void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(LoadingIndicator, { label: t("loading") }),
							content !== void 0 && renderSlot("sidebar.right.tab.document", {
								resourceAddress: tab.contentId,
								content,
								wrap: state.wrap,
								scrollportRef: bindScrollport,
								addResource: add,
								setResources: set
							}, {
								entryKey: selected.id,
								hookContext: useTabInfo,
								fallback: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
									className: TextPreview_module_css_default.statusLine,
									children: t("rendererUnavailable", { name: selected.title() })
								})
							}),
							current?.failure !== void 0 && (hasContent ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("p", {
								className: TextPreview_module_css_default.statusLine,
								"data-textpreview-failed": current.failure.code,
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: failureLine(t, current.failure) }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
									type: "button",
									className: TextPreview_module_css_default.action,
									"data-textpreview-retry": true,
									onClick: loadNext,
									children: t("retry")
								})]
							}) : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
								className: TextPreview_module_css_default.empty,
								"data-textpreview-failed": current.failure.code,
								children: [
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.FileTypeIcon, {
										kind: (0, _deepseek_ai_dsh_client_ui_primitives.classifyFileType)(name),
										size: 36,
										className: TextPreview_module_css_default.emptyIcon
									}),
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
										className: TextPreview_module_css_default.emptyLine,
										children: failureLine(t, current.failure)
									}),
									emptyFailureRecourse(current.failure) === "open" && fileOwner !== void 0 && renderSlot("sidebar.right.tab.document.unpreviewable", fileOwner),
									emptyFailureRecourse(current.failure) === "retry" && /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
										type: "button",
										className: TextPreview_module_css_default.retry,
										"data-textpreview-retry": true,
										onClick: reload,
										children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconRefreshOutlineRegular, { size: 14 }), t("retry")]
									})
								]
							})),
							mode === "text-pages" && current !== void 0 && loaded.length > 0 && !current.eof && current.failure === void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
								type: "button",
								className: TextPreview_module_css_default.more,
								disabled: current.loading,
								"data-textpreview-more": true,
								onClick: loadNext,
								children: current.loading ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(LoadingIndicator, {
									inline: true,
									label: t("loading")
								}) : t("loadMore")
							})
						]
					})
				]
			});
		}
		//#endregion
		//#region src/client/TextTitle.tsx
		/**
		* The title as the chip and a floating panel's header show it.
		* @param props - the tab information hook.
		* @returns the type's 16px sheet followed by the tab's title text.
		*/
		function TextTitle({ useTabInfo }) {
			const { tab } = useTabInfo();
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.FileTypeIcon, {
				kind: (0, _deepseek_ai_dsh_client_ui_primitives.classifyFileType)(tab.title),
				size: 16,
				className: TextPreview_module_css_default.titleIcon
			}), tab.title] });
		}
		//#endregion
		//#region src/client/definition.ts
		/** The tab kind this package owns. */
		const TEXTPREVIEW_KIND = "text";
		/** This implementation's identity in the tab system: the key its body registers under. */
		const TEXTPREVIEW_ID = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview";
		/**
		* The tab title for one `file:` address: its decoded basename.
		*
		* The whole address stays the content identity, so two files with one name in
		* different directories are two tabs; only the chip text is shortened. Decoding
		* is per segment, matching how the address was built, so a name carrying `#`,
		* `?`, or a space reads as itself.
		* @param address - a `file:`-shaped address.
		* @returns the decoded last path segment, or the address itself when it has none.
		*/
		function basenameOf(address) {
			const name = address.slice(address.lastIndexOf("/") + 1);
			if (name === "") return address;
			try {
				return decodeURIComponent(name);
			} catch {
				return name;
			}
		}
		/**
		* The text type's registry definition.
		* @returns the definition to register.
		*/
		function textDefinition() {
			return {
				id: TEXTPREVIEW_ID,
				kind: TEXTPREVIEW_KIND,
				patterns: ["dsh-resource://file/**"],
				priority: "fallback",
				canOpen: (address) => parseFileAddress(address)?.scope === "session",
				title: basenameOf
			};
		}
		//#endregion
		//#region src/client/document/resource-group.ts
		/** Forwards member metadata changes to one document preview. */
		var ResourceGroup = class {
			resources;
			changed;
			members = /* @__PURE__ */ new Map();
			/** @param resources - shared file sources. @param changed - marks the owning preview stale. */
			constructor(resources, changed) {
				this.resources = resources;
				this.changed = changed;
			}
			/**
			* Subscribe once; initial metadata establishes the baseline for later changes.
			* @param address - complete resource address.
			*/
			add(address) {
				if (this.members.has(address)) return;
				const source = this.resources.source(address);
				let previous;
				const observe = () => {
					const next = source.getSnapshot();
					if (next.status !== "live" && next.status !== "failed") return;
					const key = next.status === "live" ? `live:${next.value?.version}` : `failed:${next.failure?.code}`;
					const changed = previous !== void 0 && key !== previous;
					previous = key;
					if (changed) this.changed();
				};
				this.members.set(address, source.subscribe(observe));
				observe();
			}
			/**
			* Release dependencies absent from the current document.
			* @param addresses - complete membership of the latest document load.
			*/
			set(addresses) {
				const retained = new Set(addresses);
				for (const address of retained) this.add(address);
				for (const [address, release] of this.members) {
					if (retained.has(address)) continue;
					this.members.delete(address);
					release();
				}
			}
			/** Release every member when the preview tab ends. */
			close() {
				this.set([]);
			}
		};
		//#endregion
		//#region src/client/face.ts
		/**
		* Bind the preview's face to one paged read and one complete-byte read.
		* @param read - the bound `workspaceFiles.read` call.
		* @param readAll - complete-byte workspace Remote read.
		* @param resources - shared metadata sources for the document and its dependencies.
		* @returns the Slot `inject` factory: bound actions in, face out. The slot's session id is unused because the address carries its own.
		*/
		function textFace(read, readAll, resources) {
			return (_sessionId, actions) => {
				const tabs = /* @__PURE__ */ new Map();
				const readsOf = (tabId, signal) => {
					const held = tabs.get(tabId);
					if (held !== void 0) return held;
					const created = {
						generation: 0,
						version: void 0,
						mode: "text-pages",
						group: new ResourceGroup(resources, () => {
							actions.resourceChanged(tabId);
						})
					};
					tabs.set(tabId, created);
					signal.addEventListener("abort", () => {
						created.controller?.abort();
						created.group.close();
						tabs.delete(tabId);
						actions.forget(tabId);
					}, { once: true });
					return created;
				};
				const modeOf = (tabId, signal, mode, rendererId) => {
					const reads = readsOf(tabId, signal);
					if (reads.mode !== mode || reads.rendererId !== rendererId) {
						reads.controller?.abort();
						if (rendererId === void 0) delete reads.rendererId;
						else reads.rendererId = rendererId;
						reads.mode = mode;
						reads.generation++;
						reads.version = void 0;
						actions.reset(tabId);
					}
					return reads;
				};
				const loadPage = (tabId, file, offset, signal, observedVersion) => {
					if (signal.aborted) return;
					const reads = modeOf(tabId, signal, "text-pages");
					const { generation } = reads;
					actions.loading(tabId, "text-pages", observedVersion);
					read(file.sessionId, file.path, offset, signal).then((result) => {
						if (signal.aborted || reads.generation !== generation) return;
						if (!result.ok) {
							actions.failed(tabId, result.error);
							return;
						}
						if (offset !== 1 && reads.version !== void 0 && result.value.version !== reads.version) {
							restart(tabId, file, signal, observedVersion);
							return;
						}
						reads.version = result.value.version;
						actions.page(tabId, result.value);
					});
				};
				const loadAll = (tabId, file, signal, observedVersion) => {
					if (signal.aborted) return;
					const reads = modeOf(tabId, signal, "bytes-complete");
					reads.controller?.abort();
					const controller = new AbortController();
					reads.controller = controller;
					const lifetime = AbortSignal.any([signal, controller.signal]);
					actions.loading(tabId, "bytes-complete", observedVersion);
					readAll(file, lifetime).then((result) => {
						if (lifetime.aborted) return;
						if (!result.ok) {
							actions.failed(tabId, result.error);
							return;
						}
						const file = result.value;
						reads.version = file.version;
						actions.complete(tabId, file);
					}, (error) => {
						if (lifetime.aborted) return;
						actions.failed(tabId, Object.assign(new Error(error instanceof Error ? error.message : String(error), { cause: error }), {
							name: "RemoteError",
							isDSHRemoteError: true,
							code: "gateway/internal",
							details: {}
						}));
					});
				};
				const restart = (tabId, file, signal, observedVersion, mode = "text-pages") => {
					if (signal.aborted) return;
					const reads = readsOf(tabId, signal);
					reads.controller?.abort();
					reads.generation += 1;
					reads.version = void 0;
					actions.reset(tabId);
					if (mode === "text-pages") loadPage(tabId, file, 1, signal, observedVersion);
					else loadAll(tabId, file, signal, observedVersion);
				};
				return {
					addResource: (tabId, address, signal) => {
						if (!signal.aborted) readsOf(tabId, signal).group.add(address);
					},
					setResources: (tabId, addresses, signal) => {
						if (!signal.aborted) readsOf(tabId, signal).group.set(addresses);
					},
					loadPage,
					reloadPages: restart,
					loadAll,
					prepareRenderer: (tabId, signal, rendererId, observedVersion, reload = false) => {
						if (signal.aborted) return;
						modeOf(tabId, signal, "renderer", rendererId);
						if (reload) actions.reset(tabId);
						actions.loading(tabId, "renderer", observedVersion, rendererId);
					},
					reloadAll: (tabId, file, signal, observedVersion) => {
						restart(tabId, file, signal, observedVersion, "bytes-complete");
					}
				};
			};
		}
		//#endregion
		//#region src/client/store.ts
		/**
		* A tab's state before it reads, scrolls, toggles, or answers anything.
		* @returns the empty bucket.
		*/
		function fresh() {
			return {
				autoRefresh: true,
				resourcesDirty: false,
				loadRevision: 0,
				version: void 0,
				observedVersion: void 0,
				pages: {},
				eof: false,
				loading: false,
				failure: void 0,
				scrollTop: 0,
				wrap: true,
				revision: void 0
			};
		}
		/** The bucket for one tab, created on first write. */
		function bucket(state, tabId) {
			return state.byTab[tabId] ??= fresh();
		}
		/**
		* Declare the preview's store.
		*
		* Constructed once in apply and shared by the body and the tools registrations,
		* which the slot runtime allows because both are session-scoped.
		* @returns the store handle to declare on both registrations.
		*/
		function createTextStore() {
			return (0, _deepseek_ai_dsh_client_store.defineStore)({
				init: () => ({ byTab: {} }),
				actions: {
					toggledAutoRefresh: (d, tabId) => {
						const state = bucket(d, tabId);
						state.autoRefresh = !state.autoRefresh;
					},
					resourceChanged: (d, tabId) => {
						bucket(d, tabId).resourcesDirty = true;
					},
					/** @param d - draft. @param tabId - owning tab. @param rendererId - manual choice, or automatic selection. */
					selected: (d, tabId, rendererId) => {
						if (rendererId === void 0) delete bucket(d, tabId).rendererId;
						else bucket(d, tabId).rendererId = rendererId;
					},
					/**
					* Mark a page read as in flight.
					* @param d - draft state.
					* @param tabId - the tab being drawn.
					* @param mode - selected renderer's loading mode.
					* @param observedVersion - metadata version at read start; later pages retain the initial observation.
					* @param contentRendererId - implementation owning source loading.
					*/
					loading: (d, tabId, mode, observedVersion, contentRendererId) => {
						const state = bucket(d, tabId);
						if (state.version === void 0 && !state.loading) state.observedVersion = observedVersion;
						if (contentRendererId === void 0) delete state.contentRendererId;
						else state.contentRendererId = contentRendererId;
						state.loading = true;
						state.failure = void 0;
						if (mode !== void 0) state.mode = mode;
					},
					/**
					* @param d - draft. @param tabId - owning tab.
					* @param revision - active content revision. @param version - displayed source version.
					*/
					rendered: (d, tabId, revision, version) => {
						const state = d.byTab[tabId];
						if (state?.mode !== "renderer" || state.loadRevision !== revision) return;
						state.version = version;
						state.loading = false;
					},
					/** @param d - draft. @param tabId - owning tab. @param revision - failed content revision. */
					rendererFailed: (d, tabId, revision) => {
						const state = d.byTab[tabId];
						if (state?.mode !== "renderer" || state.loadRevision !== revision) return;
						state.loading = false;
					},
					/** @param d - draft. @param tabId - owning tab. @param file - complete byte result for this view. */
					complete: (d, tabId, file) => {
						const state = bucket(d, tabId);
						state.complete = file;
						state.version = file.version;
						state.eof = true;
						state.loading = false;
						state.failure = void 0;
					},
					/**
					* Keep one page. A page from a newer file version invalidates the pages
					* of the older one, so the body never shows two versions at once.
					* @param d - draft state.
					* @param tabId - the tab being drawn.
					* @param page - the page the Host returned.
					*/
					page: (d, tabId, page) => {
						const state = bucket(d, tabId);
						if (state.version !== void 0 && state.version !== page.version) state.pages = {};
						state.version = page.version;
						state.pages[page.offset] = {
							text: page.text,
							lines: page.lines
						};
						state.eof = page.eof;
						state.loading = false;
						state.failure = void 0;
					},
					/**
					* Record why a page read failed; the pages already held stay.
					* @param d - draft state.
					* @param tabId - the tab being drawn.
					* @param failure - the settled Remote failure.
					*/
					failed: (d, tabId, failure) => {
						const state = bucket(d, tabId);
						state.loading = false;
						state.failure = failure;
					},
					/**
					* Drop every page, keeping the view, for a re-read from the first line.
					* @param d - draft state.
					* @param tabId - the tab being drawn.
					*/
					reset: (d, tabId) => {
						const state = bucket(d, tabId);
						state.resourcesDirty = false;
						state.loadRevision++;
						state.pages = {};
						delete state.complete;
						state.eof = false;
						state.version = void 0;
						state.observedVersion = void 0;
						state.loading = false;
						state.failure = void 0;
					},
					/**
					* Record where one tab's body is scrolled to.
					* @param d - draft state.
					* @param tabId - the tab being drawn.
					* @param scrollTop - the body's scroll offset, in px.
					*/
					scrolled: (d, tabId, scrollTop) => {
						bucket(d, tabId).scrollTop = scrollTop;
					},
					/**
					* Switch one tab between wrapped and unwrapped lines.
					* @param d - draft state.
					* @param tabId - the tab being drawn.
					*/
					toggledWrap: (d, tabId) => {
						const state = bucket(d, tabId);
						state.wrap = !state.wrap;
					},
					/**
					* Record that the body answered one navigation, so a remount restores the
					* reader's position instead of jumping again.
					* @param d - draft state.
					* @param tabId - the tab being drawn.
					* @param revision - the `navigation.revision` answered.
					*/
					navigated: (d, tabId, revision) => {
						bucket(d, tabId).revision = revision;
					},
					/**
					* Drop one tab's state, for a tab record that is gone.
					* @param d - draft state.
					* @param tabId - the tab that went away.
					*/
					forget: (d, tabId) => {
						const byTab = {};
						for (const [id, state] of Object.entries(d.byTab)) if (id !== tabId) byTab[id] = state;
						d.byTab = byTab;
					}
				}
			});
		}
		//#endregion
		//#region src/client/locales.ts
		/**
		* `sidebarDocumentPreview` namespace dictionaries.
		*
		* The failure lines are the point of this file: a preview that cannot show a
		* page has to say which of several different things went wrong, and each one
		* suggests a different next step for the reader.
		*/
		/** Simplified Chinese dictionary and key-set source of truth. */
		const zh$7 = {
			loading: "文档渲染中...",
			loadMore: "加载更多",
			changed: "文件已更新，当前显示为旧内容",
			reloadNow: "重新载入",
			reload: "重新读取文件",
			autoRefresh: "自动刷新",
			"autoRefresh.enable": "开启自动刷新",
			"autoRefresh.disable": "关闭自动刷新",
			"wrap.enable": "自动换行",
			"wrap.disable": "取消换行",
			"wrap.aria": "自动换行",
			openWith: "打开方式",
			"viewer.text": "纯文本",
			resourceUnavailable: "文件资源服务不可用",
			rendererUnavailable: "预览器 {name} 不可用",
			unsupportedFile: "该格式文件暂时无法预览",
			"error.notFound": "文件不存在，可能已被移动或删除",
			"error.tooLarge": "单页内容超过 {limit} 上限，无法读取",
			"error.notText": "该格式文件暂时无法预览",
			"error.notRegularFile": "该路径不是普通文件，没有可显示的内容",
			"error.unavailable": "读取失败：{message}",
			retry: "重试"
		};
		/** English dictionary, checked against the Chinese key set. */
		const en$7 = {
			loading: "Rendering document...",
			loadMore: "Load more",
			changed: "The file has changed, showing the previous content.",
			reloadNow: "Reload",
			reload: "Read the file again",
			autoRefresh: "Auto refresh",
			"autoRefresh.enable": "Enable auto refresh",
			"autoRefresh.disable": "Disable auto refresh",
			"wrap.enable": "Turn on line wrap",
			"wrap.disable": "Turn off line wrap",
			"wrap.aria": "Line wrap",
			openWith: "Open with",
			"viewer.text": "Plain text",
			resourceUnavailable: "The file resource service is unavailable.",
			rendererUnavailable: "The {name} preview is unavailable.",
			unsupportedFile: "Preview is not available for this file type yet.",
			"error.notFound": "File not found. It may have been moved or deleted.",
			"error.tooLarge": "This page exceeds the {limit} limit and cannot be read.",
			"error.notText": "Preview is not available for this file type yet.",
			"error.notRegularFile": "Not a regular file, nothing to display.",
			"error.unavailable": "Read failed: {message}",
			retry: "Retry"
		};
		//#endregion
		//#region src/client/document/contract.ts
		/**
		* Forward the framework's tab reader to the selected document body.
		* @param _standard - framework standard props.
		* @param useTabInfo - enclosing tab's bound reader.
		* @returns the same reader, without another subscription adapter.
		*/
		const documentTabInfoFactory = (_standard, useTabInfo) => useTabInfo;
		//#endregion
		//#region src/client/markdown/path-images.ts
		/** Local Markdown image destinations served by the authenticated file route. */
		/**
		* Build a file URL, resolving relative destinations beside the previewed file.
		* @param base - document base URI, including any deployment prefix.
		* @param documentPath - absolute source path reported by the Host, when available.
		* @param destination - authored Markdown image URL; query and fragment are not filename components.
		* @returns a Web or Desktop file URL, or undefined for unsupported or malformed destinations.
		*/
		function markdownImageUrl(base, documentPath, destination) {
			const suffix = destination.search(/[?#]/u);
			let path;
			try {
				path = decodeURIComponent(suffix === -1 ? destination : destination.slice(0, suffix));
			} catch (_error) {
				return;
			}
			if (path.length === 0) return void 0;
			if (!/^[a-z]:[/\\]/iu.test(path) && /^[a-z][a-z\d+.-]*:/iu.test(path)) return void 0;
			if (!isAbsoluteWorkspacePath(path)) {
				if (documentPath === void 0) return void 0;
				path = pathPartsOf(documentPath).directory + path;
			}
			return fileMediaUrl(base, path);
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-sidebar-documentpreview/src/client/markdown/MarkdownBody.module.css.mjs
		const css$7 = ".yUOm-G_document{min-width:0;font-family:var(--dsw-font,inherit);white-space:normal;padding:10px 12px}";
		const tagId$7 = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview/MarkdownBody.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$7) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview";
			tag.dataset.pluginCss = tagId$7;
			tag.textContent = css$7;
			document.head.appendChild(tag);
		}
		var MarkdownBody_module_css_default = { "document": "yUOm-G_document" };
		//#endregion
		//#region src/client/markdown/MarkdownBody.tsx
		/** One retained Markdown renderer over the document owner's accumulated text. */
		/**
		* Render one accumulated document; EOF completes the primitive's full parse.
		* @param props - owner-loaded contents and localized primitive labels.
		* @returns Markdown content, or nothing for a non-text delivery.
		*/
		function MarkdownBody({ content, resourceAddress, useResource, t }) {
			const absolutePath = useResource(resourceAddress).value?.absolutePath;
			const pathImages = (0, react.useMemo)(() => ({ resolve: (value) => markdownImageUrl(document.baseURI, absolutePath, value) }), [absolutePath]);
			const copyLabel = t("code.copy");
			const copiedLabel = t("code.copied");
			const footnotes = t("footnotes");
			const codeLabel = t("codeBlock.title");
			const wrapLabel = t("codeBlock.wrap");
			const unwrapLabel = t("codeBlock.unwrap");
			const labels = (0, react.useMemo)(() => ({
				code: {
					copyLabel,
					copiedLabel,
					toolbarLabels: {
						codeLabel,
						wrapLabel,
						unwrapLabel
					}
				},
				footnotes
			}), [
				copyLabel,
				copiedLabel,
				footnotes,
				codeLabel,
				wrapLabel,
				unwrapLabel
			]);
			if (content.kind !== "text") return null;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: MarkdownBody_module_css_default.document,
				"data-document-markdown": true,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.MarkdownText, {
					text: content.text,
					streaming: !content.eof,
					labels,
					pathImages
				})
			});
		}
		//#endregion
		//#region src/client/markdown/locales.ts
		/** Markdown implementation labels and primitive chrome. */
		const zh$6 = {
			"viewer.label": "Markdown",
			"code.copy": "复制",
			"code.copied": "已复制",
			"footnotes": "脚注"
		};
		/** English labels, paired with the Chinese key set. */
		const en$6 = {
			"viewer.label": "Markdown",
			"code.copy": "Copy",
			"code.copied": "Copied",
			"footnotes": "Footnotes"
		};
		//#endregion
		//#region src/client/markdown/index.ts
		/** Implementation identity shared by metadata and the document slot. */
		const MARKDOWN_BODY_ID = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview/markdown";
		/**
		* Describe the Markdown implementation without taking ownership of loading.
		* @param title - locale-owned implementation name.
		* @returns builtin Markdown registration metadata.
		*/
		function markdownDefinition(title) {
			return {
				id: MARKDOWN_BODY_ID,
				extensions: ["md", "markdown"],
				priority: "builtin",
				title,
				loading: "text-pages",
				wrap: false
			};
		}
		/**
		* Register locale, metadata, and the document body for the owning plugin lifetime.
		* @param ctx - plugin context carrying locale, document registry, and slots.
		*/
		function apply$8(ctx) {
			const t = ctx.locale.bind("documentMarkdown");
			ctx.effect(() => ctx.locale.register("documentMarkdown", {
				zh: zh$6,
				en: en$6
			}), "document-markdown: dictionaries");
			ctx.effect(() => ctx.documentPreviews.register(markdownDefinition(() => t("viewer.label"))), "document-markdown: metadata");
			ctx.effect(() => ctx.slots.inject("sidebar.right.tab.document", () => ctx.slots.register({
				name: "sidebar.right.tab.document",
				key: MARKDOWN_BODY_ID,
				locale: "documentMarkdown"
			}, MarkdownBody)), "document-markdown: body");
		}
		//#endregion
		//#region src/client/html/bytes.ts
		/** UTF-8 decoding for file bytes and encoding only for the iframe's script payload. */
		const BASE64_CHUNK_BYTES = 32768;
		/**
		* Decode complete UTF-8 text, rejecting invalid byte sequences.
		* @param data - complete UTF-8 bytes.
		* @returns decoded text; invalid UTF-8 throws.
		*/
		function decodeText(data) {
			return new TextDecoder("utf-8", { fatal: true }).decode(data);
		}
		/**
		* Encode Unicode text for the iframe's base64 payload.
		* @param text - Unicode text.
		* @returns base64 of its UTF-8 bytes.
		*/
		function encodeText(text) {
			const bytes = new TextEncoder().encode(text);
			const chunks = [];
			for (let offset = 0; offset < bytes.length; offset += BASE64_CHUNK_BYTES) chunks.push(String.fromCharCode(...bytes.subarray(offset, offset + BASE64_CHUNK_BYTES)));
			return btoa(chunks.join(""));
		}
		//#endregion
		//#region src/client/html/bootstrap.ts
		/** A fixed bootstrap runs inside the opaque iframe; no Host callbacks enter its document. */
		/**
		* Build the outer iframe document. Its resource URLs are created inside the sandbox,
		* because that opaque origin cannot load resource URLs created by the parent.
		* @param bundle - complete HTML bytes and optional static dependencies.
		* @returns bootstrap HTML; invalid UTF-8 throws before navigation.
		*/
		function createHtmlDocument(bundle) {
			return `<!doctype html><meta charset="utf-8"><script>(()=>{
const bytes=data=>Uint8Array.from(atob(data),character=>character.charCodeAt(0));
const text=data=>new TextDecoder('utf-8',{fatal:true}).decode(bytes(data));
const bundle=JSON.parse(text("${encodeText(JSON.stringify({
				html: decodeText(bundle.data),
				assets: bundle.assets.map((asset) => ({
					kind: asset.kind,
					reference: asset.reference,
					text: decodeText(asset.data)
				}))
			}))}"));
let html=bundle.html;
if(bundle.assets.length){
  const parsed=new DOMParser().parseFromString(html,'text/html');
  for(const asset of bundle.assets){
    const script=asset.kind==='script';
    const url=URL.createObjectURL(new Blob([asset.text],{type:script?'application/javascript':'text/css'}));
    const attribute=script?'src':'href';
    for(const element of parsed.querySelectorAll(script?'script[src]':'link[rel~="stylesheet" i][href]')){
      if(element.getAttribute(attribute)===asset.reference)element.setAttribute(attribute,url);
    }
  }
  html='<!doctype html>'+parsed.documentElement.outerHTML;
}
document.open();document.write(html);document.close();
})()<\/script>`;
		}
		//#endregion
		//#region ../../../node_modules/.pnpm/dompurify@3.4.11/node_modules/dompurify/dist/purify.es.mjs
		/*! @license DOMPurify 3.4.11 | (c) Cure53 and other contributors | Released under the Apache license 2.0 and Mozilla Public License 2.0 | github.com/cure53/DOMPurify/blob/3.4.11/LICENSE */
		function _arrayLikeToArray(r, a) {
			(null == a || a > r.length) && (a = r.length);
			for (var e = 0, n = Array(a); e < a; e++) n[e] = r[e];
			return n;
		}
		function _arrayWithHoles(r) {
			if (Array.isArray(r)) return r;
		}
		function _iterableToArrayLimit(r, l) {
			var t = null == r ? null : "undefined" != typeof Symbol && r[Symbol.iterator] || r["@@iterator"];
			if (null != t) {
				var e, n, i, u, a = [], f = true, o = false;
				try {
					if (i = (t = t.call(r)).next, 0 === l);
					else for (; !(f = (e = i.call(t)).done) && (a.push(e.value), a.length !== l); f = !0);
				} catch (r) {
					o = true, n = r;
				} finally {
					try {
						if (!f && null != t.return && (u = t.return(), Object(u) !== u)) return;
					} finally {
						if (o) throw n;
					}
				}
				return a;
			}
		}
		function _nonIterableRest() {
			throw new TypeError("Invalid attempt to destructure non-iterable instance.\nIn order to be iterable, non-array objects must have a [Symbol.iterator]() method.");
		}
		function _slicedToArray(r, e) {
			return _arrayWithHoles(r) || _iterableToArrayLimit(r, e) || _unsupportedIterableToArray(r, e) || _nonIterableRest();
		}
		function _unsupportedIterableToArray(r, a) {
			if (r) {
				if ("string" == typeof r) return _arrayLikeToArray(r, a);
				var t = {}.toString.call(r).slice(8, -1);
				return "Object" === t && r.constructor && (t = r.constructor.name), "Map" === t || "Set" === t ? Array.from(r) : "Arguments" === t || /^(?:Ui|I)nt(?:8|16|32)(?:Clamped)?Array$/.test(t) ? _arrayLikeToArray(r, a) : void 0;
			}
		}
		const entries = Object.entries, setPrototypeOf = Object.setPrototypeOf, isFrozen = Object.isFrozen, getPrototypeOf = Object.getPrototypeOf, getOwnPropertyDescriptor = Object.getOwnPropertyDescriptor;
		let freeze = Object.freeze, seal = Object.seal, create = Object.create;
		let _ref = typeof Reflect !== "undefined" && Reflect, apply$7 = _ref.apply, construct = _ref.construct;
		if (!freeze) freeze = function freeze(x) {
			return x;
		};
		if (!seal) seal = function seal(x) {
			return x;
		};
		if (!apply$7) apply$7 = function apply(func, thisArg) {
			for (var _len = arguments.length, args = new Array(_len > 2 ? _len - 2 : 0), _key = 2; _key < _len; _key++) args[_key - 2] = arguments[_key];
			return func.apply(thisArg, args);
		};
		if (!construct) construct = function construct(Func) {
			for (var _len2 = arguments.length, args = new Array(_len2 > 1 ? _len2 - 1 : 0), _key2 = 1; _key2 < _len2; _key2++) args[_key2 - 1] = arguments[_key2];
			return new Func(...args);
		};
		const arrayForEach = unapply(Array.prototype.forEach);
		const arrayLastIndexOf = unapply(Array.prototype.lastIndexOf);
		const arrayPop = unapply(Array.prototype.pop);
		const arrayPush = unapply(Array.prototype.push);
		const arraySplice = unapply(Array.prototype.splice);
		const arrayIsArray = Array.isArray;
		const stringToLowerCase = unapply(String.prototype.toLowerCase);
		const stringToString = unapply(String.prototype.toString);
		const stringMatch = unapply(String.prototype.match);
		const stringReplace = unapply(String.prototype.replace);
		const stringIndexOf = unapply(String.prototype.indexOf);
		const stringTrim = unapply(String.prototype.trim);
		const numberToString = unapply(Number.prototype.toString);
		const booleanToString = unapply(Boolean.prototype.toString);
		const bigintToString = typeof BigInt === "undefined" ? null : unapply(BigInt.prototype.toString);
		const symbolToString = typeof Symbol === "undefined" ? null : unapply(Symbol.prototype.toString);
		const objectHasOwnProperty = unapply(Object.prototype.hasOwnProperty);
		const objectToString = unapply(Object.prototype.toString);
		const regExpTest = unapply(RegExp.prototype.test);
		const typeErrorCreate = unconstruct(TypeError);
		/**
		* Creates a new function that calls the given function with a specified thisArg and arguments.
		*
		* @param func - The function to be wrapped and called.
		* @returns A new function that calls the given function with a specified thisArg and arguments.
		*/
		function unapply(func) {
			return function(thisArg) {
				if (thisArg instanceof RegExp) thisArg.lastIndex = 0;
				for (var _len3 = arguments.length, args = new Array(_len3 > 1 ? _len3 - 1 : 0), _key3 = 1; _key3 < _len3; _key3++) args[_key3 - 1] = arguments[_key3];
				return apply$7(func, thisArg, args);
			};
		}
		/**
		* Creates a new function that constructs an instance of the given constructor function with the provided arguments.
		*
		* @param func - The constructor function to be wrapped and called.
		* @returns A new function that constructs an instance of the given constructor function with the provided arguments.
		*/
		function unconstruct(Func) {
			return function() {
				for (var _len4 = arguments.length, args = new Array(_len4), _key4 = 0; _key4 < _len4; _key4++) args[_key4] = arguments[_key4];
				return construct(Func, args);
			};
		}
		/**
		* Add properties to a lookup table
		*
		* @param set - The set to which elements will be added.
		* @param array - The array containing elements to be added to the set.
		* @param transformCaseFunc - An optional function to transform the case of each element before adding to the set.
		* @returns The modified set with added elements.
		*/
		function addToSet(set, array) {
			let transformCaseFunc = arguments.length > 2 && arguments[2] !== void 0 ? arguments[2] : stringToLowerCase;
			if (setPrototypeOf) setPrototypeOf(set, null);
			if (!arrayIsArray(array)) return set;
			let l = array.length;
			while (l--) {
				let element = array[l];
				if (typeof element === "string") {
					const lcElement = transformCaseFunc(element);
					if (lcElement !== element) {
						if (!isFrozen(array)) array[l] = lcElement;
						element = lcElement;
					}
				}
				set[element] = true;
			}
			return set;
		}
		/**
		* Clean up an array to harden against CSPP
		*
		* @param array - The array to be cleaned.
		* @returns The cleaned version of the array
		*/
		function cleanArray(array) {
			for (let index = 0; index < array.length; index++) if (!objectHasOwnProperty(array, index)) array[index] = null;
			return array;
		}
		/**
		* Shallow clone an object
		*
		* @param object - The object to be cloned.
		* @returns A new object that copies the original.
		*/
		function clone$1(object) {
			const newObject = create(null);
			for (const _ref2 of entries(object)) {
				var _ref3 = _slicedToArray(_ref2, 2);
				const property = _ref3[0];
				const value = _ref3[1];
				if (objectHasOwnProperty(object, property)) if (arrayIsArray(value)) newObject[property] = cleanArray(value);
				else if (value && typeof value === "object" && value.constructor === Object) newObject[property] = clone$1(value);
				else newObject[property] = value;
			}
			return newObject;
		}
		/**
		* Convert non-node values into strings without depending on direct property access.
		*
		* @param value - The value to stringify.
		* @returns A string representation of the provided value.
		*/
		function stringifyValue(value) {
			switch (typeof value) {
				case "string": return value;
				case "number": return numberToString(value);
				case "boolean": return booleanToString(value);
				case "bigint": return bigintToString ? bigintToString(value) : "0";
				case "symbol": return symbolToString ? symbolToString(value) : "Symbol()";
				case "undefined": return objectToString(value);
				case "function":
				case "object": {
					if (value === null) return objectToString(value);
					const valueAsRecord = value;
					const valueToString = lookupGetter(valueAsRecord, "toString");
					if (typeof valueToString === "function") {
						const stringified = valueToString(valueAsRecord);
						return typeof stringified === "string" ? stringified : objectToString(stringified);
					}
					return objectToString(value);
				}
				default: return objectToString(value);
			}
		}
		/**
		* This method automatically checks if the prop is function or getter and behaves accordingly.
		*
		* @param object - The object to look up the getter function in its prototype chain.
		* @param prop - The property name for which to find the getter function.
		* @returns The getter function found in the prototype chain or a fallback function.
		*/
		function lookupGetter(object, prop) {
			while (object !== null) {
				const desc = getOwnPropertyDescriptor(object, prop);
				if (desc) {
					if (desc.get) return unapply(desc.get);
					if (typeof desc.value === "function") return unapply(desc.value);
				}
				object = getPrototypeOf(object);
			}
			function fallbackValue() {
				return null;
			}
			return fallbackValue;
		}
		function isRegex(value) {
			try {
				regExpTest(value, "");
				return true;
			} catch (_unused) {
				return false;
			}
		}
		const html$1 = freeze([
			"a",
			"abbr",
			"acronym",
			"address",
			"area",
			"article",
			"aside",
			"audio",
			"b",
			"bdi",
			"bdo",
			"big",
			"blink",
			"blockquote",
			"body",
			"br",
			"button",
			"canvas",
			"caption",
			"center",
			"cite",
			"code",
			"col",
			"colgroup",
			"content",
			"data",
			"datalist",
			"dd",
			"decorator",
			"del",
			"details",
			"dfn",
			"dialog",
			"dir",
			"div",
			"dl",
			"dt",
			"element",
			"em",
			"fieldset",
			"figcaption",
			"figure",
			"font",
			"footer",
			"form",
			"h1",
			"h2",
			"h3",
			"h4",
			"h5",
			"h6",
			"head",
			"header",
			"hgroup",
			"hr",
			"html",
			"i",
			"img",
			"input",
			"ins",
			"kbd",
			"label",
			"legend",
			"li",
			"main",
			"map",
			"mark",
			"marquee",
			"menu",
			"menuitem",
			"meter",
			"nav",
			"nobr",
			"ol",
			"optgroup",
			"option",
			"output",
			"p",
			"picture",
			"pre",
			"progress",
			"q",
			"rp",
			"rt",
			"ruby",
			"s",
			"samp",
			"search",
			"section",
			"select",
			"shadow",
			"slot",
			"small",
			"source",
			"spacer",
			"span",
			"strike",
			"strong",
			"style",
			"sub",
			"summary",
			"sup",
			"table",
			"tbody",
			"td",
			"template",
			"textarea",
			"tfoot",
			"th",
			"thead",
			"time",
			"tr",
			"track",
			"tt",
			"u",
			"ul",
			"var",
			"video",
			"wbr"
		]);
		const svg$1 = freeze([
			"svg",
			"a",
			"altglyph",
			"altglyphdef",
			"altglyphitem",
			"animatecolor",
			"animatemotion",
			"animatetransform",
			"circle",
			"clippath",
			"defs",
			"desc",
			"ellipse",
			"enterkeyhint",
			"exportparts",
			"filter",
			"font",
			"g",
			"glyph",
			"glyphref",
			"hkern",
			"image",
			"inputmode",
			"line",
			"lineargradient",
			"marker",
			"mask",
			"metadata",
			"mpath",
			"part",
			"path",
			"pattern",
			"polygon",
			"polyline",
			"radialgradient",
			"rect",
			"stop",
			"style",
			"switch",
			"symbol",
			"text",
			"textpath",
			"title",
			"tref",
			"tspan",
			"view",
			"vkern"
		]);
		const svgFilters = freeze([
			"feBlend",
			"feColorMatrix",
			"feComponentTransfer",
			"feComposite",
			"feConvolveMatrix",
			"feDiffuseLighting",
			"feDisplacementMap",
			"feDistantLight",
			"feDropShadow",
			"feFlood",
			"feFuncA",
			"feFuncB",
			"feFuncG",
			"feFuncR",
			"feGaussianBlur",
			"feImage",
			"feMerge",
			"feMergeNode",
			"feMorphology",
			"feOffset",
			"fePointLight",
			"feSpecularLighting",
			"feSpotLight",
			"feTile",
			"feTurbulence"
		]);
		const svgDisallowed = freeze([
			"animate",
			"color-profile",
			"cursor",
			"discard",
			"font-face",
			"font-face-format",
			"font-face-name",
			"font-face-src",
			"font-face-uri",
			"foreignobject",
			"hatch",
			"hatchpath",
			"mesh",
			"meshgradient",
			"meshpatch",
			"meshrow",
			"missing-glyph",
			"script",
			"set",
			"solidcolor",
			"unknown",
			"use"
		]);
		const mathMl$1 = freeze([
			"math",
			"menclose",
			"merror",
			"mfenced",
			"mfrac",
			"mglyph",
			"mi",
			"mlabeledtr",
			"mmultiscripts",
			"mn",
			"mo",
			"mover",
			"mpadded",
			"mphantom",
			"mroot",
			"mrow",
			"ms",
			"mspace",
			"msqrt",
			"mstyle",
			"msub",
			"msup",
			"msubsup",
			"mtable",
			"mtd",
			"mtext",
			"mtr",
			"munder",
			"munderover",
			"mprescripts"
		]);
		const mathMlDisallowed = freeze([
			"maction",
			"maligngroup",
			"malignmark",
			"mlongdiv",
			"mscarries",
			"mscarry",
			"msgroup",
			"mstack",
			"msline",
			"msrow",
			"semantics",
			"annotation",
			"annotation-xml",
			"mprescripts",
			"none"
		]);
		const text = freeze(["#text"]);
		const html = freeze([
			"accept",
			"action",
			"align",
			"alt",
			"autocapitalize",
			"autocomplete",
			"autopictureinpicture",
			"autoplay",
			"background",
			"bgcolor",
			"border",
			"capture",
			"cellpadding",
			"cellspacing",
			"checked",
			"cite",
			"class",
			"clear",
			"color",
			"cols",
			"colspan",
			"command",
			"commandfor",
			"controls",
			"controlslist",
			"coords",
			"crossorigin",
			"datetime",
			"decoding",
			"default",
			"dir",
			"disabled",
			"disablepictureinpicture",
			"disableremoteplayback",
			"download",
			"draggable",
			"enctype",
			"enterkeyhint",
			"exportparts",
			"face",
			"for",
			"headers",
			"height",
			"hidden",
			"high",
			"href",
			"hreflang",
			"id",
			"inert",
			"inputmode",
			"integrity",
			"ismap",
			"kind",
			"label",
			"lang",
			"list",
			"loading",
			"loop",
			"low",
			"max",
			"maxlength",
			"media",
			"method",
			"min",
			"minlength",
			"multiple",
			"muted",
			"name",
			"nonce",
			"noshade",
			"novalidate",
			"nowrap",
			"open",
			"optimum",
			"part",
			"pattern",
			"placeholder",
			"playsinline",
			"popover",
			"popovertarget",
			"popovertargetaction",
			"poster",
			"preload",
			"pubdate",
			"radiogroup",
			"readonly",
			"rel",
			"required",
			"rev",
			"reversed",
			"role",
			"rows",
			"rowspan",
			"spellcheck",
			"scope",
			"selected",
			"shape",
			"size",
			"sizes",
			"slot",
			"span",
			"srclang",
			"start",
			"src",
			"srcset",
			"step",
			"style",
			"summary",
			"tabindex",
			"title",
			"translate",
			"type",
			"usemap",
			"valign",
			"value",
			"width",
			"wrap",
			"xmlns"
		]);
		const svg = freeze([
			"accent-height",
			"accumulate",
			"additive",
			"alignment-baseline",
			"amplitude",
			"ascent",
			"attributename",
			"attributetype",
			"azimuth",
			"basefrequency",
			"baseline-shift",
			"begin",
			"bias",
			"by",
			"class",
			"clip",
			"clippathunits",
			"clip-path",
			"clip-rule",
			"color",
			"color-interpolation",
			"color-interpolation-filters",
			"color-profile",
			"color-rendering",
			"cx",
			"cy",
			"d",
			"dx",
			"dy",
			"diffuseconstant",
			"direction",
			"display",
			"divisor",
			"dur",
			"edgemode",
			"elevation",
			"end",
			"exponent",
			"fill",
			"fill-opacity",
			"fill-rule",
			"filter",
			"filterunits",
			"flood-color",
			"flood-opacity",
			"font-family",
			"font-size",
			"font-size-adjust",
			"font-stretch",
			"font-style",
			"font-variant",
			"font-weight",
			"fx",
			"fy",
			"g1",
			"g2",
			"glyph-name",
			"glyphref",
			"gradientunits",
			"gradienttransform",
			"height",
			"href",
			"id",
			"image-rendering",
			"in",
			"in2",
			"intercept",
			"k",
			"k1",
			"k2",
			"k3",
			"k4",
			"kerning",
			"keypoints",
			"keysplines",
			"keytimes",
			"lang",
			"lengthadjust",
			"letter-spacing",
			"kernelmatrix",
			"kernelunitlength",
			"lighting-color",
			"local",
			"marker-end",
			"marker-mid",
			"marker-start",
			"markerheight",
			"markerunits",
			"markerwidth",
			"maskcontentunits",
			"maskunits",
			"max",
			"mask",
			"mask-type",
			"media",
			"method",
			"mode",
			"min",
			"name",
			"numoctaves",
			"offset",
			"operator",
			"opacity",
			"order",
			"orient",
			"orientation",
			"origin",
			"overflow",
			"paint-order",
			"path",
			"pathlength",
			"patterncontentunits",
			"patterntransform",
			"patternunits",
			"points",
			"preservealpha",
			"preserveaspectratio",
			"primitiveunits",
			"r",
			"rx",
			"ry",
			"radius",
			"refx",
			"refy",
			"repeatcount",
			"repeatdur",
			"restart",
			"result",
			"rotate",
			"scale",
			"seed",
			"shape-rendering",
			"slope",
			"specularconstant",
			"specularexponent",
			"spreadmethod",
			"startoffset",
			"stddeviation",
			"stitchtiles",
			"stop-color",
			"stop-opacity",
			"stroke-dasharray",
			"stroke-dashoffset",
			"stroke-linecap",
			"stroke-linejoin",
			"stroke-miterlimit",
			"stroke-opacity",
			"stroke",
			"stroke-width",
			"style",
			"surfacescale",
			"systemlanguage",
			"tabindex",
			"tablevalues",
			"targetx",
			"targety",
			"transform",
			"transform-origin",
			"text-anchor",
			"text-decoration",
			"text-rendering",
			"textlength",
			"type",
			"u1",
			"u2",
			"unicode",
			"values",
			"viewbox",
			"visibility",
			"version",
			"vert-adv-y",
			"vert-origin-x",
			"vert-origin-y",
			"width",
			"word-spacing",
			"wrap",
			"writing-mode",
			"xchannelselector",
			"ychannelselector",
			"x",
			"x1",
			"x2",
			"xmlns",
			"y",
			"y1",
			"y2",
			"z",
			"zoomandpan"
		]);
		const mathMl = freeze([
			"accent",
			"accentunder",
			"align",
			"bevelled",
			"close",
			"columnalign",
			"columnlines",
			"columnspacing",
			"columnspan",
			"denomalign",
			"depth",
			"dir",
			"display",
			"displaystyle",
			"encoding",
			"fence",
			"frame",
			"height",
			"href",
			"id",
			"largeop",
			"length",
			"linethickness",
			"lquote",
			"lspace",
			"mathbackground",
			"mathcolor",
			"mathsize",
			"mathvariant",
			"maxsize",
			"minsize",
			"movablelimits",
			"notation",
			"numalign",
			"open",
			"rowalign",
			"rowlines",
			"rowspacing",
			"rowspan",
			"rspace",
			"rquote",
			"scriptlevel",
			"scriptminsize",
			"scriptsizemultiplier",
			"selection",
			"separator",
			"separators",
			"stretchy",
			"subscriptshift",
			"supscriptshift",
			"symmetric",
			"voffset",
			"width",
			"xmlns"
		]);
		const xml = freeze([
			"xlink:href",
			"xml:id",
			"xlink:title",
			"xml:space",
			"xmlns:xlink"
		]);
		const MUSTACHE_EXPR = seal(/{{[\w\W]*|^[\w\W]*}}/g);
		const ERB_EXPR = seal(/<%[\w\W]*|^[\w\W]*%>/g);
		const TMPLIT_EXPR = seal(/\${[\w\W]*/g);
		const DATA_ATTR = seal(/^data-[\-\w.\u00B7-\uFFFF]+$/);
		const ARIA_ATTR = seal(/^aria-[\-\w]+$/);
		const IS_ALLOWED_URI = seal(/^(?:(?:(?:f|ht)tps?|mailto|tel|callto|sms|cid|xmpp|matrix):|[^a-z]|[a-z+.\-]+(?:[^a-z+.\-:]|$))/i);
		const IS_SCRIPT_OR_DATA = seal(/^(?:\w+script|data):/i);
		const ATTR_WHITESPACE = seal(/[\u0000-\u0020\u00A0\u1680\u180E\u2000-\u2029\u205F\u3000]/g);
		const DOCTYPE_NAME = seal(/^html$/i);
		const CUSTOM_ELEMENT = seal(/^[a-z][.\w]*(-[.\w]+)+$/i);
		const ELEMENT_MARKUP_PROBE = seal(/<[/\w!]/g);
		const COMMENT_MARKUP_PROBE = seal(/<[/\w]/g);
		const FALLBACK_TAG_CLOSE = seal(/<\/no(script|embed|frames)/i);
		const SELF_CLOSING_TAG = seal(/\/>/i);
		const NODE_TYPE = {
			element: 1,
			attribute: 2,
			text: 3,
			cdataSection: 4,
			entityReference: 5,
			entityNode: 6,
			processingInstruction: 7,
			comment: 8,
			document: 9,
			documentType: 10,
			documentFragment: 11,
			notation: 12
		};
		const getGlobal = function getGlobal() {
			return typeof window === "undefined" ? null : window;
		};
		/**
		* Creates a no-op policy for internal use only.
		* Don't export this function outside this module!
		* @param trustedTypes The policy factory.
		* @param purifyHostElement The Script element used to load DOMPurify (to determine policy name suffix).
		* @return The policy created (or null, if Trusted Types
		* are not supported or creating the policy failed).
		*/
		const _createTrustedTypesPolicy = function _createTrustedTypesPolicy(trustedTypes, purifyHostElement) {
			if (typeof trustedTypes !== "object" || typeof trustedTypes.createPolicy !== "function") return null;
			let suffix = null;
			const ATTR_NAME = "data-tt-policy-suffix";
			if (purifyHostElement && purifyHostElement.hasAttribute(ATTR_NAME)) suffix = purifyHostElement.getAttribute(ATTR_NAME);
			const policyName = "dompurify" + (suffix ? "#" + suffix : "");
			try {
				return trustedTypes.createPolicy(policyName, {
					createHTML(html) {
						return html;
					},
					createScriptURL(scriptUrl) {
						return scriptUrl;
					}
				});
			} catch (_) {
				console.warn("TrustedTypes policy " + policyName + " could not be created.");
				return null;
			}
		};
		const _createHooksMap = function _createHooksMap() {
			return {
				afterSanitizeAttributes: [],
				afterSanitizeElements: [],
				afterSanitizeShadowDOM: [],
				beforeSanitizeAttributes: [],
				beforeSanitizeElements: [],
				beforeSanitizeShadowDOM: [],
				uponSanitizeAttribute: [],
				uponSanitizeElement: [],
				uponSanitizeShadowNode: []
			};
		};
		/**
		* Resolve a set-valued configuration option: a fresh set built from
		* cfg[key] when it is an own array property (seeded with a clone of
		* options.base when given, case-normalized via options.transform),
		* the fallback set otherwise.
		*
		* @param cfg the cloned, prototype-free configuration object
		* @param key the configuration property to read
		* @param fallback the set to use when the option is absent or not an array
		* @param options transform and optional base set to merge into
		* @returns the resolved set
		*/
		const _resolveSetOption = function _resolveSetOption(cfg, key, fallback, options) {
			return objectHasOwnProperty(cfg, key) && arrayIsArray(cfg[key]) ? addToSet(options.base ? clone$1(options.base) : {}, cfg[key], options.transform) : fallback;
		};
		function createDOMPurify() {
			let window = arguments.length > 0 && arguments[0] !== void 0 ? arguments[0] : getGlobal();
			const DOMPurify = (root) => createDOMPurify(root);
			DOMPurify.version = "3.4.11";
			DOMPurify.removed = [];
			if (!window || !window.document || window.document.nodeType !== NODE_TYPE.document || !window.Element) {
				DOMPurify.isSupported = false;
				return DOMPurify;
			}
			let document = window.document;
			const originalDocument = document;
			const currentScript = originalDocument.currentScript;
			window.DocumentFragment;
			const HTMLTemplateElement = window.HTMLTemplateElement, Node = window.Node, Element = window.Element, NodeFilter = window.NodeFilter;
			window.NamedNodeMap === void 0 && (window.NamedNodeMap || window.MozNamedAttrMap);
			window.HTMLFormElement;
			const DOMParser = window.DOMParser, trustedTypes = window.trustedTypes;
			const ElementPrototype = Element.prototype;
			const cloneNode = lookupGetter(ElementPrototype, "cloneNode");
			const remove = lookupGetter(ElementPrototype, "remove");
			const getNextSibling = lookupGetter(ElementPrototype, "nextSibling");
			const getChildNodes = lookupGetter(ElementPrototype, "childNodes");
			const getParentNode = lookupGetter(ElementPrototype, "parentNode");
			const getShadowRoot = lookupGetter(ElementPrototype, "shadowRoot");
			const getAttributes = lookupGetter(ElementPrototype, "attributes");
			const getNodeType = Node && Node.prototype ? lookupGetter(Node.prototype, "nodeType") : null;
			const getNodeName = Node && Node.prototype ? lookupGetter(Node.prototype, "nodeName") : null;
			if (typeof HTMLTemplateElement === "function") {
				const template = document.createElement("template");
				if (template.content && template.content.ownerDocument) document = template.content.ownerDocument;
			}
			let trustedTypesPolicy;
			let emptyHTML = "";
			let defaultTrustedTypesPolicy;
			let defaultTrustedTypesPolicyResolved = false;
			let IN_TRUSTED_TYPES_POLICY = 0;
			const _assertNotInTrustedTypesPolicy = function _assertNotInTrustedTypesPolicy() {
				if (IN_TRUSTED_TYPES_POLICY > 0) throw typeErrorCreate("A configured TRUSTED_TYPES_POLICY callback (createHTML or createScriptURL) must not call DOMPurify.sanitize, as that causes infinite recursion. Do not pass a policy whose callbacks wrap DOMPurify as TRUSTED_TYPES_POLICY; see the \"DOMPurify and Trusted Types\" section of the README.");
			};
			const _createTrustedHTML = function _createTrustedHTML(html) {
				_assertNotInTrustedTypesPolicy();
				IN_TRUSTED_TYPES_POLICY++;
				try {
					return trustedTypesPolicy.createHTML(html);
				} finally {
					IN_TRUSTED_TYPES_POLICY--;
				}
			};
			const _createTrustedScriptURL = function _createTrustedScriptURL(scriptUrl) {
				_assertNotInTrustedTypesPolicy();
				IN_TRUSTED_TYPES_POLICY++;
				try {
					return trustedTypesPolicy.createScriptURL(scriptUrl);
				} finally {
					IN_TRUSTED_TYPES_POLICY--;
				}
			};
			const _getDefaultTrustedTypesPolicy = function _getDefaultTrustedTypesPolicy() {
				if (!defaultTrustedTypesPolicyResolved) {
					defaultTrustedTypesPolicy = _createTrustedTypesPolicy(trustedTypes, currentScript);
					defaultTrustedTypesPolicyResolved = true;
				}
				return defaultTrustedTypesPolicy;
			};
			const _document = document, implementation = _document.implementation, createNodeIterator = _document.createNodeIterator, createDocumentFragment = _document.createDocumentFragment, getElementsByTagName = _document.getElementsByTagName;
			const importNode = originalDocument.importNode;
			let hooks = _createHooksMap();
			/**
			* Expose whether this browser supports running the full DOMPurify.
			*/
			DOMPurify.isSupported = typeof entries === "function" && typeof getParentNode === "function" && implementation && implementation.createHTMLDocument !== void 0;
			const MUSTACHE_EXPR$1 = MUSTACHE_EXPR, ERB_EXPR$1 = ERB_EXPR, TMPLIT_EXPR$1 = TMPLIT_EXPR, DATA_ATTR$1 = DATA_ATTR, ARIA_ATTR$1 = ARIA_ATTR, IS_SCRIPT_OR_DATA$1 = IS_SCRIPT_OR_DATA, ATTR_WHITESPACE$1 = ATTR_WHITESPACE, CUSTOM_ELEMENT$1 = CUSTOM_ELEMENT;
			let IS_ALLOWED_URI$1 = IS_ALLOWED_URI;
			/**
			* We consider the elements and attributes below to be safe. Ideally
			* don't add any new ones but feel free to remove unwanted ones.
			*/
			let ALLOWED_TAGS = null;
			const DEFAULT_ALLOWED_TAGS = addToSet({}, [
				...html$1,
				...svg$1,
				...svgFilters,
				...mathMl$1,
				...text
			]);
			let ALLOWED_ATTR = null;
			const DEFAULT_ALLOWED_ATTR = addToSet({}, [
				...html,
				...svg,
				...mathMl,
				...xml
			]);
			let CUSTOM_ELEMENT_HANDLING = Object.seal(create(null, {
				tagNameCheck: {
					writable: true,
					configurable: false,
					enumerable: true,
					value: null
				},
				attributeNameCheck: {
					writable: true,
					configurable: false,
					enumerable: true,
					value: null
				},
				allowCustomizedBuiltInElements: {
					writable: true,
					configurable: false,
					enumerable: true,
					value: false
				}
			}));
			let FORBID_TAGS = null;
			let FORBID_ATTR = null;
			const EXTRA_ELEMENT_HANDLING = Object.seal(create(null, {
				tagCheck: {
					writable: true,
					configurable: false,
					enumerable: true,
					value: null
				},
				attributeCheck: {
					writable: true,
					configurable: false,
					enumerable: true,
					value: null
				}
			}));
			let ALLOW_ARIA_ATTR = true;
			let ALLOW_DATA_ATTR = true;
			let ALLOW_UNKNOWN_PROTOCOLS = false;
			let ALLOW_SELF_CLOSE_IN_ATTR = true;
			let SAFE_FOR_TEMPLATES = false;
			let SAFE_FOR_XML = true;
			let WHOLE_DOCUMENT = false;
			let SET_CONFIG = false;
			let SET_CONFIG_ALLOWED_TAGS = null;
			let SET_CONFIG_ALLOWED_ATTR = null;
			let FORCE_BODY = false;
			let RETURN_DOM = false;
			let RETURN_DOM_FRAGMENT = false;
			let RETURN_TRUSTED_TYPE = false;
			let SANITIZE_DOM = true;
			let SANITIZE_NAMED_PROPS = false;
			const SANITIZE_NAMED_PROPS_PREFIX = "user-content-";
			let KEEP_CONTENT = true;
			let IN_PLACE = false;
			let USE_PROFILES = {};
			let FORBID_CONTENTS = null;
			const DEFAULT_FORBID_CONTENTS = addToSet({}, [
				"annotation-xml",
				"audio",
				"colgroup",
				"desc",
				"foreignobject",
				"head",
				"iframe",
				"math",
				"mi",
				"mn",
				"mo",
				"ms",
				"mtext",
				"noembed",
				"noframes",
				"noscript",
				"plaintext",
				"script",
				"selectedcontent",
				"style",
				"svg",
				"template",
				"thead",
				"title",
				"video",
				"xmp"
			]);
			let DATA_URI_TAGS = null;
			const DEFAULT_DATA_URI_TAGS = addToSet({}, [
				"audio",
				"video",
				"img",
				"source",
				"image",
				"track"
			]);
			let URI_SAFE_ATTRIBUTES = null;
			const DEFAULT_URI_SAFE_ATTRIBUTES = addToSet({}, [
				"alt",
				"class",
				"for",
				"id",
				"label",
				"name",
				"pattern",
				"placeholder",
				"role",
				"summary",
				"title",
				"value",
				"style",
				"xmlns"
			]);
			const MATHML_NAMESPACE = "http://www.w3.org/1998/Math/MathML";
			const SVG_NAMESPACE = "http://www.w3.org/2000/svg";
			const HTML_NAMESPACE = "http://www.w3.org/1999/xhtml";
			let NAMESPACE = HTML_NAMESPACE;
			let IS_EMPTY_INPUT = false;
			let ALLOWED_NAMESPACES = null;
			const DEFAULT_ALLOWED_NAMESPACES = addToSet({}, [
				MATHML_NAMESPACE,
				SVG_NAMESPACE,
				HTML_NAMESPACE
			], stringToString);
			const DEFAULT_MATHML_TEXT_INTEGRATION_POINTS = freeze([
				"mi",
				"mo",
				"mn",
				"ms",
				"mtext"
			]);
			let MATHML_TEXT_INTEGRATION_POINTS = addToSet({}, DEFAULT_MATHML_TEXT_INTEGRATION_POINTS);
			const DEFAULT_HTML_INTEGRATION_POINTS = freeze(["annotation-xml"]);
			let HTML_INTEGRATION_POINTS = addToSet({}, DEFAULT_HTML_INTEGRATION_POINTS);
			const COMMON_SVG_AND_HTML_ELEMENTS = addToSet({}, [
				"title",
				"style",
				"font",
				"a",
				"script"
			]);
			let PARSER_MEDIA_TYPE = null;
			const SUPPORTED_PARSER_MEDIA_TYPES = ["application/xhtml+xml", "text/html"];
			const DEFAULT_PARSER_MEDIA_TYPE = "text/html";
			let transformCaseFunc = null;
			let CONFIG = null;
			const formElement = document.createElement("form");
			const isRegexOrFunction = function isRegexOrFunction(testValue) {
				return testValue instanceof RegExp || testValue instanceof Function;
			};
			/**
			* _parseConfig
			*
			* @param cfg optional config literal
			*/
			const _parseConfig = function _parseConfig() {
				let cfg = arguments.length > 0 && arguments[0] !== void 0 ? arguments[0] : {};
				if (CONFIG && CONFIG === cfg) return;
				if (!cfg || typeof cfg !== "object") cfg = {};
				cfg = clone$1(cfg);
				PARSER_MEDIA_TYPE = SUPPORTED_PARSER_MEDIA_TYPES.indexOf(cfg.PARSER_MEDIA_TYPE) === -1 ? DEFAULT_PARSER_MEDIA_TYPE : cfg.PARSER_MEDIA_TYPE;
				transformCaseFunc = PARSER_MEDIA_TYPE === "application/xhtml+xml" ? stringToString : stringToLowerCase;
				ALLOWED_TAGS = _resolveSetOption(cfg, "ALLOWED_TAGS", DEFAULT_ALLOWED_TAGS, { transform: transformCaseFunc });
				ALLOWED_ATTR = _resolveSetOption(cfg, "ALLOWED_ATTR", DEFAULT_ALLOWED_ATTR, { transform: transformCaseFunc });
				ALLOWED_NAMESPACES = _resolveSetOption(cfg, "ALLOWED_NAMESPACES", DEFAULT_ALLOWED_NAMESPACES, { transform: stringToString });
				URI_SAFE_ATTRIBUTES = _resolveSetOption(cfg, "ADD_URI_SAFE_ATTR", DEFAULT_URI_SAFE_ATTRIBUTES, {
					transform: transformCaseFunc,
					base: DEFAULT_URI_SAFE_ATTRIBUTES
				});
				DATA_URI_TAGS = _resolveSetOption(cfg, "ADD_DATA_URI_TAGS", DEFAULT_DATA_URI_TAGS, {
					transform: transformCaseFunc,
					base: DEFAULT_DATA_URI_TAGS
				});
				FORBID_CONTENTS = _resolveSetOption(cfg, "FORBID_CONTENTS", DEFAULT_FORBID_CONTENTS, { transform: transformCaseFunc });
				FORBID_TAGS = _resolveSetOption(cfg, "FORBID_TAGS", clone$1({}), { transform: transformCaseFunc });
				FORBID_ATTR = _resolveSetOption(cfg, "FORBID_ATTR", clone$1({}), { transform: transformCaseFunc });
				USE_PROFILES = objectHasOwnProperty(cfg, "USE_PROFILES") ? cfg.USE_PROFILES && typeof cfg.USE_PROFILES === "object" ? clone$1(cfg.USE_PROFILES) : cfg.USE_PROFILES : false;
				ALLOW_ARIA_ATTR = cfg.ALLOW_ARIA_ATTR !== false;
				ALLOW_DATA_ATTR = cfg.ALLOW_DATA_ATTR !== false;
				ALLOW_UNKNOWN_PROTOCOLS = cfg.ALLOW_UNKNOWN_PROTOCOLS || false;
				ALLOW_SELF_CLOSE_IN_ATTR = cfg.ALLOW_SELF_CLOSE_IN_ATTR !== false;
				SAFE_FOR_TEMPLATES = cfg.SAFE_FOR_TEMPLATES || false;
				SAFE_FOR_XML = cfg.SAFE_FOR_XML !== false;
				WHOLE_DOCUMENT = cfg.WHOLE_DOCUMENT || false;
				RETURN_DOM = cfg.RETURN_DOM || false;
				RETURN_DOM_FRAGMENT = cfg.RETURN_DOM_FRAGMENT || false;
				RETURN_TRUSTED_TYPE = cfg.RETURN_TRUSTED_TYPE || false;
				FORCE_BODY = cfg.FORCE_BODY || false;
				SANITIZE_DOM = cfg.SANITIZE_DOM !== false;
				SANITIZE_NAMED_PROPS = cfg.SANITIZE_NAMED_PROPS || false;
				KEEP_CONTENT = cfg.KEEP_CONTENT !== false;
				IN_PLACE = cfg.IN_PLACE || false;
				IS_ALLOWED_URI$1 = isRegex(cfg.ALLOWED_URI_REGEXP) ? cfg.ALLOWED_URI_REGEXP : IS_ALLOWED_URI;
				NAMESPACE = typeof cfg.NAMESPACE === "string" ? cfg.NAMESPACE : HTML_NAMESPACE;
				MATHML_TEXT_INTEGRATION_POINTS = objectHasOwnProperty(cfg, "MATHML_TEXT_INTEGRATION_POINTS") && cfg.MATHML_TEXT_INTEGRATION_POINTS && typeof cfg.MATHML_TEXT_INTEGRATION_POINTS === "object" ? clone$1(cfg.MATHML_TEXT_INTEGRATION_POINTS) : addToSet({}, DEFAULT_MATHML_TEXT_INTEGRATION_POINTS);
				HTML_INTEGRATION_POINTS = objectHasOwnProperty(cfg, "HTML_INTEGRATION_POINTS") && cfg.HTML_INTEGRATION_POINTS && typeof cfg.HTML_INTEGRATION_POINTS === "object" ? clone$1(cfg.HTML_INTEGRATION_POINTS) : addToSet({}, DEFAULT_HTML_INTEGRATION_POINTS);
				const customElementHandling = objectHasOwnProperty(cfg, "CUSTOM_ELEMENT_HANDLING") && cfg.CUSTOM_ELEMENT_HANDLING && typeof cfg.CUSTOM_ELEMENT_HANDLING === "object" ? clone$1(cfg.CUSTOM_ELEMENT_HANDLING) : create(null);
				CUSTOM_ELEMENT_HANDLING = create(null);
				if (objectHasOwnProperty(customElementHandling, "tagNameCheck") && isRegexOrFunction(customElementHandling.tagNameCheck)) CUSTOM_ELEMENT_HANDLING.tagNameCheck = customElementHandling.tagNameCheck;
				if (objectHasOwnProperty(customElementHandling, "attributeNameCheck") && isRegexOrFunction(customElementHandling.attributeNameCheck)) CUSTOM_ELEMENT_HANDLING.attributeNameCheck = customElementHandling.attributeNameCheck;
				if (objectHasOwnProperty(customElementHandling, "allowCustomizedBuiltInElements") && typeof customElementHandling.allowCustomizedBuiltInElements === "boolean") CUSTOM_ELEMENT_HANDLING.allowCustomizedBuiltInElements = customElementHandling.allowCustomizedBuiltInElements;
				seal(CUSTOM_ELEMENT_HANDLING);
				if (SAFE_FOR_TEMPLATES) ALLOW_DATA_ATTR = false;
				if (RETURN_DOM_FRAGMENT) RETURN_DOM = true;
				if (USE_PROFILES) {
					ALLOWED_TAGS = addToSet({}, text);
					ALLOWED_ATTR = create(null);
					if (USE_PROFILES.html === true) {
						addToSet(ALLOWED_TAGS, html$1);
						addToSet(ALLOWED_ATTR, html);
					}
					if (USE_PROFILES.svg === true) {
						addToSet(ALLOWED_TAGS, svg$1);
						addToSet(ALLOWED_ATTR, svg);
						addToSet(ALLOWED_ATTR, xml);
					}
					if (USE_PROFILES.svgFilters === true) {
						addToSet(ALLOWED_TAGS, svgFilters);
						addToSet(ALLOWED_ATTR, svg);
						addToSet(ALLOWED_ATTR, xml);
					}
					if (USE_PROFILES.mathMl === true) {
						addToSet(ALLOWED_TAGS, mathMl$1);
						addToSet(ALLOWED_ATTR, mathMl);
						addToSet(ALLOWED_ATTR, xml);
					}
				}
				EXTRA_ELEMENT_HANDLING.tagCheck = null;
				EXTRA_ELEMENT_HANDLING.attributeCheck = null;
				if (objectHasOwnProperty(cfg, "ADD_TAGS")) {
					if (typeof cfg.ADD_TAGS === "function") EXTRA_ELEMENT_HANDLING.tagCheck = cfg.ADD_TAGS;
					else if (arrayIsArray(cfg.ADD_TAGS)) {
						if (ALLOWED_TAGS === DEFAULT_ALLOWED_TAGS) ALLOWED_TAGS = clone$1(ALLOWED_TAGS);
						addToSet(ALLOWED_TAGS, cfg.ADD_TAGS, transformCaseFunc);
					}
				}
				if (objectHasOwnProperty(cfg, "ADD_ATTR")) {
					if (typeof cfg.ADD_ATTR === "function") EXTRA_ELEMENT_HANDLING.attributeCheck = cfg.ADD_ATTR;
					else if (arrayIsArray(cfg.ADD_ATTR)) {
						if (ALLOWED_ATTR === DEFAULT_ALLOWED_ATTR) ALLOWED_ATTR = clone$1(ALLOWED_ATTR);
						addToSet(ALLOWED_ATTR, cfg.ADD_ATTR, transformCaseFunc);
					}
				}
				if (objectHasOwnProperty(cfg, "ADD_URI_SAFE_ATTR") && arrayIsArray(cfg.ADD_URI_SAFE_ATTR)) addToSet(URI_SAFE_ATTRIBUTES, cfg.ADD_URI_SAFE_ATTR, transformCaseFunc);
				if (objectHasOwnProperty(cfg, "FORBID_CONTENTS") && arrayIsArray(cfg.FORBID_CONTENTS)) {
					if (FORBID_CONTENTS === DEFAULT_FORBID_CONTENTS) FORBID_CONTENTS = clone$1(FORBID_CONTENTS);
					addToSet(FORBID_CONTENTS, cfg.FORBID_CONTENTS, transformCaseFunc);
				}
				if (objectHasOwnProperty(cfg, "ADD_FORBID_CONTENTS") && arrayIsArray(cfg.ADD_FORBID_CONTENTS)) {
					if (FORBID_CONTENTS === DEFAULT_FORBID_CONTENTS) FORBID_CONTENTS = clone$1(FORBID_CONTENTS);
					addToSet(FORBID_CONTENTS, cfg.ADD_FORBID_CONTENTS, transformCaseFunc);
				}
				if (KEEP_CONTENT) ALLOWED_TAGS["#text"] = true;
				if (WHOLE_DOCUMENT) addToSet(ALLOWED_TAGS, [
					"html",
					"head",
					"body"
				]);
				if (ALLOWED_TAGS.table) {
					addToSet(ALLOWED_TAGS, ["tbody"]);
					delete FORBID_TAGS.tbody;
				}
				if (cfg.TRUSTED_TYPES_POLICY) {
					if (typeof cfg.TRUSTED_TYPES_POLICY.createHTML !== "function") throw typeErrorCreate("TRUSTED_TYPES_POLICY configuration option must provide a \"createHTML\" hook.");
					if (typeof cfg.TRUSTED_TYPES_POLICY.createScriptURL !== "function") throw typeErrorCreate("TRUSTED_TYPES_POLICY configuration option must provide a \"createScriptURL\" hook.");
					const previousTrustedTypesPolicy = trustedTypesPolicy;
					trustedTypesPolicy = cfg.TRUSTED_TYPES_POLICY;
					try {
						emptyHTML = _createTrustedHTML("");
					} catch (error) {
						trustedTypesPolicy = previousTrustedTypesPolicy;
						throw error;
					}
				} else if (cfg.TRUSTED_TYPES_POLICY === null) {
					trustedTypesPolicy = void 0;
					emptyHTML = "";
				} else {
					if (trustedTypesPolicy === void 0) trustedTypesPolicy = _getDefaultTrustedTypesPolicy();
					if (trustedTypesPolicy && typeof emptyHTML === "string") emptyHTML = _createTrustedHTML("");
				}
				if (freeze) freeze(cfg);
				CONFIG = cfg;
			};
			const ALL_SVG_TAGS = addToSet({}, [
				...svg$1,
				...svgFilters,
				...svgDisallowed
			]);
			const ALL_MATHML_TAGS = addToSet({}, [...mathMl$1, ...mathMlDisallowed]);
			/**
			* Namespace rules for an element in the SVG namespace.
			*
			* @param tagName the element's lowercase tag name
			* @param parent the (possibly simulated) parent node
			* @param parentTagName the parent's lowercase tag name
			* @returns true if a spec-compliant parser could produce this element
			*/
			const _checkSvgNamespace = function _checkSvgNamespace(tagName, parent, parentTagName) {
				if (parent.namespaceURI === HTML_NAMESPACE) return tagName === "svg";
				if (parent.namespaceURI === MATHML_NAMESPACE) return tagName === "svg" && (parentTagName === "annotation-xml" || MATHML_TEXT_INTEGRATION_POINTS[parentTagName]);
				return Boolean(ALL_SVG_TAGS[tagName]);
			};
			/**
			* Namespace rules for an element in the MathML namespace.
			*
			* @param tagName the element's lowercase tag name
			* @param parent the (possibly simulated) parent node
			* @param parentTagName the parent's lowercase tag name
			* @returns true if a spec-compliant parser could produce this element
			*/
			const _checkMathMlNamespace = function _checkMathMlNamespace(tagName, parent, parentTagName) {
				if (parent.namespaceURI === HTML_NAMESPACE) return tagName === "math";
				if (parent.namespaceURI === SVG_NAMESPACE) return tagName === "math" && HTML_INTEGRATION_POINTS[parentTagName];
				return Boolean(ALL_MATHML_TAGS[tagName]);
			};
			/**
			* Namespace rules for an element in the HTML namespace.
			*
			* @param tagName the element's lowercase tag name
			* @param parent the (possibly simulated) parent node
			* @param parentTagName the parent's lowercase tag name
			* @returns true if a spec-compliant parser could produce this element
			*/
			const _checkHtmlNamespace = function _checkHtmlNamespace(tagName, parent, parentTagName) {
				if (parent.namespaceURI === SVG_NAMESPACE && !HTML_INTEGRATION_POINTS[parentTagName]) return false;
				if (parent.namespaceURI === MATHML_NAMESPACE && !MATHML_TEXT_INTEGRATION_POINTS[parentTagName]) return false;
				return !ALL_MATHML_TAGS[tagName] && (COMMON_SVG_AND_HTML_ELEMENTS[tagName] || !ALL_SVG_TAGS[tagName]);
			};
			/**
			* @param element a DOM element whose namespace is being checked
			* @returns Return false if the element has a
			*  namespace that a spec-compliant parser would never
			*  return. Return true otherwise.
			*/
			const _checkValidNamespace = function _checkValidNamespace(element) {
				let parent = getParentNode(element);
				if (!parent || !parent.tagName) parent = {
					namespaceURI: NAMESPACE,
					tagName: "template"
				};
				const tagName = stringToLowerCase(element.tagName);
				const parentTagName = stringToLowerCase(parent.tagName);
				if (!ALLOWED_NAMESPACES[element.namespaceURI]) return false;
				if (element.namespaceURI === SVG_NAMESPACE) return _checkSvgNamespace(tagName, parent, parentTagName);
				if (element.namespaceURI === MATHML_NAMESPACE) return _checkMathMlNamespace(tagName, parent, parentTagName);
				if (element.namespaceURI === HTML_NAMESPACE) return _checkHtmlNamespace(tagName, parent, parentTagName);
				if (PARSER_MEDIA_TYPE === "application/xhtml+xml" && ALLOWED_NAMESPACES[element.namespaceURI]) return true;
				return false;
			};
			/**
			* _forceRemove
			*
			* @param node a DOM node
			*/
			const _forceRemove = function _forceRemove(node) {
				arrayPush(DOMPurify.removed, { element: node });
				try {
					getParentNode(node).removeChild(node);
				} catch (_) {
					remove(node);
					if (!getParentNode(node)) throw typeErrorCreate("a node selected for removal could not be detached from its tree and cannot be safely returned; refusing to sanitize in place");
				}
			};
			/**
			* _neutralizeRoot
			*
			* Fail-closed teardown of an in-place root after the sanitize walk aborts
			* (campaign-3 F2). An internal throw mid-walk — e.g. a page-registered
			* custom element's reaction detaches a node so `_forceRemove`'s deliberate
			* parentless guard throws, or any other re-entrant engine mutation — would
			* otherwise leave the caller's *live* tree half-sanitized, with everything
			* after the abort point still carrying its handlers. There is no safe way
			* to resume the walk (the tree mutated under us), so we strip the root bare:
			* remove every child and every attribute, then let the caller's catch see
			* the original error. Clobber-safe (cached `remove`/`childNodes`/`attributes`
			* getters; the root was already clobber-pre-flighted at the IN_PLACE entry).
			*
			* @param root the in-place root to empty
			*/
			const _neutralizeRoot = function _neutralizeRoot(root) {
				const childNodes = getChildNodes(root);
				if (childNodes) {
					const snapshot = [];
					arrayForEach(childNodes, (child) => {
						arrayPush(snapshot, child);
					});
					arrayForEach(snapshot, (child) => {
						try {
							remove(child);
						} catch (_) {}
					});
				}
				const attributes = getAttributes(root);
				if (attributes) for (let i = attributes.length - 1; i >= 0; --i) {
					const attribute = attributes[i];
					const name = attribute && attribute.name;
					if (typeof name === "string") try {
						root.removeAttribute(name);
					} catch (_) {}
				}
			};
			/**
			* _removeAttribute
			*
			* @param name an Attribute name
			* @param element a DOM node
			*/
			const _removeAttribute = function _removeAttribute(name, element) {
				try {
					arrayPush(DOMPurify.removed, {
						attribute: element.getAttributeNode(name),
						from: element
					});
				} catch (_) {
					arrayPush(DOMPurify.removed, {
						attribute: null,
						from: element
					});
				}
				element.removeAttribute(name);
				if (name === "is") if (RETURN_DOM || RETURN_DOM_FRAGMENT) try {
					_forceRemove(element);
				} catch (_) {}
				else try {
					element.setAttribute(name, "");
				} catch (_) {}
			};
			/**
			* _stripDisallowedAttributes
			*
			* Removes every attribute the active configuration does not allow from a
			* single element, using the same allowlist as the main attribute pass (so
			* `on*` handlers go, but no `/^on/` blocklist is introduced). Used only to
			* neutralise nodes that are being discarded from an in-place tree.
			*
			* @param element the element to strip
			*/
			const _stripDisallowedAttributes = function _stripDisallowedAttributes(element) {
				const attributes = getAttributes(element);
				if (!attributes) return;
				for (let i = attributes.length - 1; i >= 0; --i) {
					const attribute = attributes[i];
					const name = attribute && attribute.name;
					if (typeof name !== "string" || ALLOWED_ATTR[transformCaseFunc(name)]) continue;
					try {
						element.removeAttribute(name);
					} catch (_) {}
				}
			};
			/**
			* _neutralizeSubtree
			*
			* Completes the audit-5 F1 fix across every removal path. The KEEP_CONTENT
			* move-hoist neutralises only disallowed-tag removals; clobber, mXSS-canary,
			* namespace, comment, processing-instruction and KEEP_CONTENT:false removals
			* all drop their subtree wholesale via `_forceRemove`. On the IN_PLACE path
			* those dropped nodes are detached from the caller's LIVE tree but a
			* handler-bearing original among them (an `<img onerror>`/`<video>` that was
			* loading) keeps its queued resource event, which fires in page scope after
			* sanitize returns. This walks a removed subtree and strips every attribute
			* the active configuration does not allow — so `on*` handlers are cancelled
			* through the SAME allowlist that governs kept nodes, not a separate `/^on/`
			* blocklist. Run synchronously before sanitize returns, i.e. before any
			* queued event can fire. Hook-free by design: these nodes leave the output,
			* so firing attribute hooks for them would be surprising. Clobber-safe reads;
			* a doomed clobbered node may shadow `removeAttribute` (its own attributes are
			* irrelevant — it is discarded — while its non-clobbered descendants, e.g.
			* the `<img>`, are reached and scrubbed).
			*
			* @param root the root of a removed subtree to neutralise
			*/
			const _neutralizeSubtree = function _neutralizeSubtree(root) {
				const stack = [root];
				while (stack.length > 0) {
					const node = stack.pop();
					if ((getNodeType ? getNodeType(node) : node.nodeType) === NODE_TYPE.element) _stripDisallowedAttributes(node);
					const childNodes = getChildNodes(node);
					if (childNodes) for (let i = childNodes.length - 1; i >= 0; --i) stack.push(childNodes[i]);
				}
			};
			/**
			* _initDocument
			*
			* @param dirty - a string of dirty markup
			* @return a DOM, filled with the dirty markup
			*/
			const _initDocument = function _initDocument(dirty) {
				let doc = null;
				let leadingWhitespace = null;
				if (FORCE_BODY) dirty = "<remove></remove>" + dirty;
				else {
					const matches = stringMatch(dirty, /^[\r\n\t ]+/);
					leadingWhitespace = matches && matches[0];
				}
				if (PARSER_MEDIA_TYPE === "application/xhtml+xml" && NAMESPACE === HTML_NAMESPACE) dirty = "<html xmlns=\"http://www.w3.org/1999/xhtml\"><head></head><body>" + dirty + "</body></html>";
				const dirtyPayload = trustedTypesPolicy ? _createTrustedHTML(dirty) : dirty;
				if (NAMESPACE === HTML_NAMESPACE) try {
					doc = new DOMParser().parseFromString(dirtyPayload, PARSER_MEDIA_TYPE);
				} catch (_) {}
				if (!doc || !doc.documentElement) {
					doc = implementation.createDocument(NAMESPACE, "template", null);
					try {
						doc.documentElement.innerHTML = IS_EMPTY_INPUT ? emptyHTML : dirtyPayload;
					} catch (_) {}
				}
				const body = doc.body || doc.documentElement;
				if (dirty && leadingWhitespace) body.insertBefore(document.createTextNode(leadingWhitespace), body.childNodes[0] || null);
				if (NAMESPACE === HTML_NAMESPACE) return getElementsByTagName.call(doc, WHOLE_DOCUMENT ? "html" : "body")[0];
				return WHOLE_DOCUMENT ? doc.documentElement : body;
			};
			/**
			* Creates a NodeIterator object that you can use to traverse filtered lists of nodes or elements in a document.
			*
			* @param root The root element or node to start traversing on.
			* @return The created NodeIterator
			*/
			const _createNodeIterator = function _createNodeIterator(root) {
				return createNodeIterator.call(root.ownerDocument || root, root, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_COMMENT | NodeFilter.SHOW_TEXT | NodeFilter.SHOW_PROCESSING_INSTRUCTION | NodeFilter.SHOW_CDATA_SECTION, null);
			};
			/**
			* Replace template expression syntax (mustache, ERB, template
			* literal) with a space; shared by all SAFE_FOR_TEMPLATES scrub
			* sites. Order matters: mustache, then ERB, then template literal.
			*
			* @param value the string to scrub
			* @returns the scrubbed string
			*/
			const _stripTemplateExpressions = function _stripTemplateExpressions(value) {
				value = stringReplace(value, MUSTACHE_EXPR$1, " ");
				value = stringReplace(value, ERB_EXPR$1, " ");
				value = stringReplace(value, TMPLIT_EXPR$1, " ");
				return value;
			};
			/**
			* Strip template-engine expressions ({{...}}, ${...}, <%...%>) from the
			* character data of an element subtree. Used as the final safety net for
			* SAFE_FOR_TEMPLATES on every DOM-returning code path so that expressions
			* which only form after text-node normalization (e.g. fragments split across
			* stripped elements) cannot survive into a template-evaluating framework.
			*
			* Walks text/comment/CDATA/processing-instruction nodes and mutates `.data`
			* in place rather than round-tripping through innerHTML. This preserves
			* descendant node references (important for IN_PLACE callers), avoids a
			* serialize/reparse cycle, and reads literal character data — which means
			* `<%...%>` in text content matches the ERB regex against its real bytes
			* instead of the HTML-entity-escaped form innerHTML would produce.
			*
			* Attribute values are not visited here; SAFE_FOR_TEMPLATES handling for
			* attributes is performed during the per-node `_sanitizeAttributes` pass.
			*
			* @param node The root element whose character data should be scrubbed.
			*/
			const _scrubTemplateExpressions2 = function _scrubTemplateExpressions(node) {
				var _node$querySelectorAl;
				node.normalize();
				const walker = createNodeIterator.call(node.ownerDocument || node, node, NodeFilter.SHOW_TEXT | NodeFilter.SHOW_COMMENT | NodeFilter.SHOW_CDATA_SECTION | NodeFilter.SHOW_PROCESSING_INSTRUCTION, null);
				let currentNode = walker.nextNode();
				while (currentNode) {
					currentNode.data = _stripTemplateExpressions(currentNode.data);
					currentNode = walker.nextNode();
				}
				const templates = (_node$querySelectorAl = node.querySelectorAll) === null || _node$querySelectorAl === void 0 ? void 0 : _node$querySelectorAl.call(node, "template");
				if (templates) arrayForEach(templates, (tmpl) => {
					if (_isDocumentFragment(tmpl.content)) _scrubTemplateExpressions2(tmpl.content);
				});
			};
			/**
			* _isClobbered
			*
			* Detect DOM-clobbering on HTMLFormElement nodes. Form is the only HTML
			* interface with [LegacyOverrideBuiltIns]; a descendant element with a
			* `name` attribute matching a prototype property shadows that property
			* on direct reads. We use this check at the IN_PLACE entry-point and
			* during attribute sanitization to refuse clobbered forms.
			*
			* @param element element to check for clobbering attacks
			* @return true if clobbered, false if safe
			*/
			const _isClobbered = function _isClobbered(element) {
				const realTagName = getNodeName ? getNodeName(element) : null;
				if (typeof realTagName !== "string") return false;
				if (transformCaseFunc(realTagName) !== "form") return false;
				return typeof element.nodeName !== "string" || typeof element.textContent !== "string" || typeof element.removeChild !== "function" || element.attributes !== getAttributes(element) || typeof element.removeAttribute !== "function" || typeof element.setAttribute !== "function" || typeof element.namespaceURI !== "string" || typeof element.insertBefore !== "function" || typeof element.hasChildNodes !== "function" || element.nodeType !== getNodeType(element) || element.childNodes !== getChildNodes(element);
			};
			/**
			* Checks whether the given value is a DocumentFragment from any realm.
			*
			* The realm-independent replacement reads `nodeType` through the cached
			* Node.prototype getter and compares to the DOCUMENT_FRAGMENT_NODE
			* constant (11). nodeType is a numeric value resolved from the node's
			* internal slot, identical across realms for the same kind of node.
			*
			* @param value object to check
			* @return true if value is a DocumentFragment-shaped node from any realm
			*/
			const _isDocumentFragment = function _isDocumentFragment(value) {
				if (!getNodeType || typeof value !== "object" || value === null) return false;
				try {
					return getNodeType(value) === NODE_TYPE.documentFragment;
				} catch (_) {
					return false;
				}
			};
			/**
			* Checks whether the given object is a DOM node, including nodes that
			* originate from a different window/realm (e.g. an iframe's
			* contentDocument). The previous `value instanceof Node` check was
			* realm-bound: nodes from a different window failed it, causing
			* sanitize() to silently stringify them and reset IN_PLACE to false,
			* returning the original node unsanitized. See GHSA-4w3q-35jp-p934.
			*
			* @param value object to check whether it's a DOM node
			* @return true if value is a DOM node from any realm
			*/
			const _isNode = function _isNode(value) {
				if (!getNodeType || typeof value !== "object" || value === null) return false;
				try {
					return typeof getNodeType(value) === "number";
				} catch (_) {
					return false;
				}
			};
			function _executeHooks(hooks, currentNode, data) {
				if (hooks.length === 0) return;
				arrayForEach(hooks, (hook) => {
					hook.call(DOMPurify, currentNode, data, CONFIG);
				});
			}
			/**
			* Structural-threat checks that condemn a node regardless of the
			* allowlists: mXSS via namespace confusion, risky CSS construction,
			* processing instructions, markup-bearing comments. Pure predicate;
			* the caller removes. Check order is load-bearing.
			*
			* @param currentNode the node to inspect
			* @param tagName the node's transformCaseFunc'd tag name
			* @return true if the node must be removed
			*/
			const _isUnsafeNode = function _isUnsafeNode(currentNode, tagName) {
				if (SAFE_FOR_XML && currentNode.hasChildNodes() && !_isNode(currentNode.firstElementChild) && regExpTest(ELEMENT_MARKUP_PROBE, currentNode.textContent) && regExpTest(ELEMENT_MARKUP_PROBE, currentNode.innerHTML)) return true;
				if (SAFE_FOR_XML && currentNode.namespaceURI === HTML_NAMESPACE && tagName === "style" && _isNode(currentNode.firstElementChild)) return true;
				if (currentNode.nodeType === NODE_TYPE.processingInstruction) return true;
				if (SAFE_FOR_XML && currentNode.nodeType === NODE_TYPE.comment && regExpTest(COMMENT_MARKUP_PROBE, currentNode.data)) return true;
				return false;
			};
			/**
			* Handle a node whose tag is forbidden or not allowlisted: keep
			* allowed custom elements (false return exits _sanitizeElements
			* early - namespace/fallback checks and the afterSanitizeElements
			* hook are intentionally skipped for kept custom elements), else
			* hoist content per KEEP_CONTENT and remove.
			*
			* @param currentNode the disallowed node
			* @param tagName the node's transformCaseFunc'd tag name
			* @return true if the node was removed, false if kept
			*/
			const _sanitizeDisallowedNode = function _sanitizeDisallowedNode(currentNode, tagName) {
				if (!FORBID_TAGS[tagName] && _isBasicCustomElement(tagName)) {
					if (CUSTOM_ELEMENT_HANDLING.tagNameCheck instanceof RegExp && regExpTest(CUSTOM_ELEMENT_HANDLING.tagNameCheck, tagName)) return false;
					if (CUSTOM_ELEMENT_HANDLING.tagNameCheck instanceof Function && CUSTOM_ELEMENT_HANDLING.tagNameCheck(tagName)) return false;
				}
				if (KEEP_CONTENT && !FORBID_CONTENTS[tagName]) {
					const parentNode = getParentNode(currentNode);
					const childNodes = getChildNodes(currentNode);
					if (childNodes && parentNode) {
						const childCount = childNodes.length;
						for (let i = childCount - 1; i >= 0; --i) {
							const hoisted = IN_PLACE ? childNodes[i] : cloneNode(childNodes[i], true);
							parentNode.insertBefore(hoisted, getNextSibling(currentNode));
						}
					}
				}
				_forceRemove(currentNode);
				return true;
			};
			/**
			* _sanitizeElements
			*
			* @protect nodeName
			* @protect textContent
			* @protect removeChild
			* @param currentNode to check for permission to exist
			* @return true if node was killed, false if left alive
			*/
			const _sanitizeElements = function _sanitizeElements(currentNode) {
				_executeHooks(hooks.beforeSanitizeElements, currentNode, null);
				if (_isClobbered(currentNode)) {
					_forceRemove(currentNode);
					return true;
				}
				const tagName = transformCaseFunc(getNodeName ? getNodeName(currentNode) : currentNode.nodeName);
				_executeHooks(hooks.uponSanitizeElement, currentNode, {
					tagName,
					allowedTags: ALLOWED_TAGS
				});
				if (_isUnsafeNode(currentNode, tagName)) {
					_forceRemove(currentNode);
					return true;
				}
				if (FORBID_TAGS[tagName] || !(EXTRA_ELEMENT_HANDLING.tagCheck instanceof Function && EXTRA_ELEMENT_HANDLING.tagCheck(tagName)) && !ALLOWED_TAGS[tagName]) return _sanitizeDisallowedNode(currentNode, tagName);
				if ((getNodeType ? getNodeType(currentNode) : currentNode.nodeType) === NODE_TYPE.element && !_checkValidNamespace(currentNode)) {
					_forceRemove(currentNode);
					return true;
				}
				if ((tagName === "noscript" || tagName === "noembed" || tagName === "noframes") && regExpTest(FALLBACK_TAG_CLOSE, currentNode.innerHTML)) {
					_forceRemove(currentNode);
					return true;
				}
				if (SAFE_FOR_TEMPLATES && currentNode.nodeType === NODE_TYPE.text) {
					const content = _stripTemplateExpressions(currentNode.textContent);
					if (currentNode.textContent !== content) {
						arrayPush(DOMPurify.removed, { element: currentNode.cloneNode() });
						currentNode.textContent = content;
					}
				}
				_executeHooks(hooks.afterSanitizeElements, currentNode, null);
				return false;
			};
			/**
			* _isValidAttribute
			*
			* @param lcTag Lowercase tag name of containing element.
			* @param lcName Lowercase attribute name.
			* @param value Attribute value.
			* @return Returns true if `value` is valid, otherwise false.
			*/
			const _isValidAttribute = function _isValidAttribute(lcTag, lcName, value) {
				if (FORBID_ATTR[lcName]) return false;
				if (SANITIZE_DOM && (lcName === "id" || lcName === "name") && (value in document || value in formElement)) return false;
				const nameIsPermitted = ALLOWED_ATTR[lcName] || EXTRA_ELEMENT_HANDLING.attributeCheck instanceof Function && EXTRA_ELEMENT_HANDLING.attributeCheck(lcName, lcTag);
				if (ALLOW_DATA_ATTR && regExpTest(DATA_ATTR$1, lcName));
				else if (ALLOW_ARIA_ATTR && regExpTest(ARIA_ATTR$1, lcName));
				else if (!nameIsPermitted) if (_isBasicCustomElement(lcTag) && (CUSTOM_ELEMENT_HANDLING.tagNameCheck instanceof RegExp && regExpTest(CUSTOM_ELEMENT_HANDLING.tagNameCheck, lcTag) || CUSTOM_ELEMENT_HANDLING.tagNameCheck instanceof Function && CUSTOM_ELEMENT_HANDLING.tagNameCheck(lcTag)) && (CUSTOM_ELEMENT_HANDLING.attributeNameCheck instanceof RegExp && regExpTest(CUSTOM_ELEMENT_HANDLING.attributeNameCheck, lcName) || CUSTOM_ELEMENT_HANDLING.attributeNameCheck instanceof Function && CUSTOM_ELEMENT_HANDLING.attributeNameCheck(lcName, lcTag)) || lcName === "is" && CUSTOM_ELEMENT_HANDLING.allowCustomizedBuiltInElements && (CUSTOM_ELEMENT_HANDLING.tagNameCheck instanceof RegExp && regExpTest(CUSTOM_ELEMENT_HANDLING.tagNameCheck, value) || CUSTOM_ELEMENT_HANDLING.tagNameCheck instanceof Function && CUSTOM_ELEMENT_HANDLING.tagNameCheck(value)));
				else return false;
				else if (URI_SAFE_ATTRIBUTES[lcName]);
				else if (regExpTest(IS_ALLOWED_URI$1, stringReplace(value, ATTR_WHITESPACE$1, "")));
				else if ((lcName === "src" || lcName === "xlink:href" || lcName === "href") && lcTag !== "script" && stringIndexOf(value, "data:") === 0 && DATA_URI_TAGS[lcTag]);
				else if (ALLOW_UNKNOWN_PROTOCOLS && !regExpTest(IS_SCRIPT_OR_DATA$1, stringReplace(value, ATTR_WHITESPACE$1, "")));
				else if (value) return false;
				return true;
			};
			const RESERVED_CUSTOM_ELEMENT_NAMES = addToSet({}, [
				"annotation-xml",
				"color-profile",
				"font-face",
				"font-face-format",
				"font-face-name",
				"font-face-src",
				"font-face-uri",
				"missing-glyph"
			]);
			/**
			* _isBasicCustomElement
			* checks if at least one dash is included in tagName, and it's not the first char
			* for more sophisticated checking see https://github.com/sindresorhus/validate-element-name
			*
			* @param tagName name of the tag of the node to sanitize
			* @returns Returns true if the tag name meets the basic criteria for a custom element, otherwise false.
			*/
			const _isBasicCustomElement = function _isBasicCustomElement(tagName) {
				return !RESERVED_CUSTOM_ELEMENT_NAMES[stringToLowerCase(tagName)] && regExpTest(CUSTOM_ELEMENT$1, tagName);
			};
			/**
			* Wrap an attribute value in the matching Trusted Types object when
			* the active policy requires it. Namespaced attributes pass through
			* unchanged (no TT support yet, see
			* https://bugs.chromium.org/p/chromium/issues/detail?id=1305293).
			*
			* @param lcTag lowercase tag name of the containing element
			* @param lcName lowercase attribute name
			* @param namespaceURI the attribute's namespace, if any
			* @param value the attribute value to wrap
			* @return the value, wrapped when Trusted Types demand it
			*/
			const _applyTrustedTypesToAttribute = function _applyTrustedTypesToAttribute(lcTag, lcName, namespaceURI, value) {
				if (trustedTypesPolicy && typeof trustedTypes === "object" && typeof trustedTypes.getAttributeType === "function" && !namespaceURI) switch (trustedTypes.getAttributeType(lcTag, lcName)) {
					case "TrustedHTML": return _createTrustedHTML(value);
					case "TrustedScriptURL": return _createTrustedScriptURL(value);
				}
				return value;
			};
			/**
			* Write a modified attribute value back onto the element. On
			* success, re-probe for clobbering introduced by the new value and
			* remove the element when found; otherwise pop the removal entry
			* recorded by the earlier _removeAttribute (long-standing pairing
			* with the SANITIZE_NAMED_PROPS path - do not "fix" casually). On
			* failure, remove the attribute instead.
			*
			* @param currentNode the element carrying the attribute
			* @param name the attribute name as present on the element
			* @param namespaceURI the attribute's namespace, if any
			* @param value the new attribute value
			*/
			const _setAttributeValue = function _setAttributeValue(currentNode, name, namespaceURI, value) {
				try {
					if (namespaceURI) currentNode.setAttributeNS(namespaceURI, name, value);
					else currentNode.setAttribute(name, value);
					if (_isClobbered(currentNode)) _forceRemove(currentNode);
					else arrayPop(DOMPurify.removed);
				} catch (_) {
					_removeAttribute(name, currentNode);
				}
			};
			/**
			* _sanitizeAttributes
			*
			* @protect attributes
			* @protect nodeName
			* @protect removeAttribute
			* @protect setAttribute
			*
			* @param currentNode to sanitize
			*/
			const _sanitizeAttributes = function _sanitizeAttributes(currentNode) {
				_executeHooks(hooks.beforeSanitizeAttributes, currentNode, null);
				const attributes = currentNode.attributes;
				if (!attributes || _isClobbered(currentNode)) return;
				const hookEvent = {
					attrName: "",
					attrValue: "",
					keepAttr: true,
					allowedAttributes: ALLOWED_ATTR,
					forceKeepAttr: void 0
				};
				let l = attributes.length;
				const lcTag = transformCaseFunc(currentNode.nodeName);
				while (l--) {
					const attr = attributes[l];
					const name = attr.name, namespaceURI = attr.namespaceURI, attrValue = attr.value;
					const lcName = transformCaseFunc(name);
					const initValue = attrValue;
					let value = name === "value" ? initValue : stringTrim(initValue);
					hookEvent.attrName = lcName;
					hookEvent.attrValue = value;
					hookEvent.keepAttr = true;
					hookEvent.forceKeepAttr = void 0;
					_executeHooks(hooks.uponSanitizeAttribute, currentNode, hookEvent);
					value = hookEvent.attrValue;
					if (SANITIZE_NAMED_PROPS && (lcName === "id" || lcName === "name") && stringIndexOf(value, SANITIZE_NAMED_PROPS_PREFIX) !== 0) {
						_removeAttribute(name, currentNode);
						value = SANITIZE_NAMED_PROPS_PREFIX + value;
					}
					if (SAFE_FOR_XML && regExpTest(/((--!?|])>)|<\/(style|script|title|xmp|textarea|noscript|iframe|noembed|noframes)/i, value)) {
						_removeAttribute(name, currentNode);
						continue;
					}
					if (lcName === "attributename" && stringMatch(value, "href")) {
						_removeAttribute(name, currentNode);
						continue;
					}
					if (hookEvent.forceKeepAttr) continue;
					if (!hookEvent.keepAttr) {
						_removeAttribute(name, currentNode);
						continue;
					}
					if (!ALLOW_SELF_CLOSE_IN_ATTR && regExpTest(SELF_CLOSING_TAG, value)) {
						_removeAttribute(name, currentNode);
						continue;
					}
					if (SAFE_FOR_TEMPLATES) value = _stripTemplateExpressions(value);
					if (!_isValidAttribute(lcTag, lcName, value)) {
						_removeAttribute(name, currentNode);
						continue;
					}
					value = _applyTrustedTypesToAttribute(lcTag, lcName, namespaceURI, value);
					if (value !== initValue) _setAttributeValue(currentNode, name, namespaceURI, value);
				}
				_executeHooks(hooks.afterSanitizeAttributes, currentNode, null);
			};
			/**
			* _sanitizeShadowDOM
			*
			* @param fragment to iterate over recursively
			*/
			const _sanitizeShadowDOM2 = function _sanitizeShadowDOM(fragment) {
				let shadowNode = null;
				const shadowIterator = _createNodeIterator(fragment);
				_executeHooks(hooks.beforeSanitizeShadowDOM, fragment, null);
				while (shadowNode = shadowIterator.nextNode()) {
					_executeHooks(hooks.uponSanitizeShadowNode, shadowNode, null);
					_sanitizeElements(shadowNode);
					_sanitizeAttributes(shadowNode);
					if (_isDocumentFragment(shadowNode.content)) _sanitizeShadowDOM2(shadowNode.content);
					if ((getNodeType ? getNodeType(shadowNode) : shadowNode.nodeType) === NODE_TYPE.element) {
						const innerSr = getShadowRoot(shadowNode);
						if (_isDocumentFragment(innerSr)) {
							_sanitizeAttachedShadowRoots(innerSr);
							_sanitizeShadowDOM2(innerSr);
						}
					}
				}
				_executeHooks(hooks.afterSanitizeShadowDOM, fragment, null);
			};
			/**
			* _sanitizeAttachedShadowRoots
			*
			* Walks `root` and feeds every attached shadow root we encounter into
			* the existing _sanitizeShadowDOM pipeline. The default node iterator
			* does not descend into shadow trees, so nodes inside an attached
			* shadow root would otherwise be skipped entirely.
			*
			* Two real input paths put attached shadow roots in front of us:
			*   1. IN_PLACE on a DOM node that already has shadow roots attached.
			*   2. DOM-node input where importNode(dirty, true) deep-clones the
			*      shadow root because it was created with `clonable: true`.
			*
			* This pass runs once, up front, so the main iteration loop (and the
			* existing _sanitizeShadowDOM template-content recursion) stay
			* untouched — string-input paths are not affected.
			*
			* @param root the subtree root to walk for attached shadow roots
			*/
			const _sanitizeAttachedShadowRoots = function _sanitizeAttachedShadowRoots(root) {
				const stack = [{
					node: root,
					shadow: null
				}];
				while (stack.length > 0) {
					const item = stack.pop();
					if (item.shadow) {
						_sanitizeShadowDOM2(item.shadow);
						continue;
					}
					const node = item.node;
					const isElement = (getNodeType ? getNodeType(node) : node.nodeType) === NODE_TYPE.element;
					const childNodes = getChildNodes(node);
					if (childNodes) for (let i = childNodes.length - 1; i >= 0; --i) stack.push({
						node: childNodes[i],
						shadow: null
					});
					if (isElement) {
						const rootName = getNodeName ? getNodeName(node) : null;
						if (typeof rootName === "string" && transformCaseFunc(rootName) === "template") {
							const content = node.content;
							if (_isDocumentFragment(content)) stack.push({
								node: content,
								shadow: null
							});
						}
					}
					if (isElement) {
						const sr = getShadowRoot(node);
						if (_isDocumentFragment(sr)) stack.push({
							node: null,
							shadow: sr
						}, {
							node: sr,
							shadow: null
						});
					}
				}
			};
			DOMPurify.sanitize = function(dirty) {
				let cfg = arguments.length > 1 && arguments[1] !== void 0 ? arguments[1] : {};
				let body = null;
				let importedNode = null;
				let currentNode = null;
				let returnNode = null;
				IS_EMPTY_INPUT = !dirty;
				if (IS_EMPTY_INPUT) dirty = "<!-->";
				if (typeof dirty !== "string" && !_isNode(dirty)) {
					dirty = stringifyValue(dirty);
					if (typeof dirty !== "string") throw typeErrorCreate("dirty is not a string, aborting");
				}
				if (!DOMPurify.isSupported) return dirty;
				if (SET_CONFIG) {
					ALLOWED_TAGS = SET_CONFIG_ALLOWED_TAGS;
					ALLOWED_ATTR = SET_CONFIG_ALLOWED_ATTR;
				} else _parseConfig(cfg);
				if (hooks.uponSanitizeElement.length > 0 || hooks.uponSanitizeAttribute.length > 0) ALLOWED_TAGS = clone$1(ALLOWED_TAGS);
				if (hooks.uponSanitizeAttribute.length > 0) ALLOWED_ATTR = clone$1(ALLOWED_ATTR);
				DOMPurify.removed = [];
				const inPlace = IN_PLACE && typeof dirty !== "string" && _isNode(dirty);
				if (inPlace) {
					const nn = getNodeName ? getNodeName(dirty) : dirty.nodeName;
					if (typeof nn === "string") {
						const tagName = transformCaseFunc(nn);
						if (!ALLOWED_TAGS[tagName] || FORBID_TAGS[tagName]) throw typeErrorCreate("root node is forbidden and cannot be sanitized in-place");
					}
					if (_isClobbered(dirty)) throw typeErrorCreate("root node is clobbered and cannot be sanitized in-place");
					try {
						_sanitizeAttachedShadowRoots(dirty);
					} catch (error) {
						_neutralizeRoot(dirty);
						throw error;
					}
				} else if (_isNode(dirty)) {
					body = _initDocument("<!---->");
					importedNode = body.ownerDocument.importNode(dirty, true);
					if (importedNode.nodeType === NODE_TYPE.element && importedNode.nodeName === "BODY") body = importedNode;
					else if (importedNode.nodeName === "HTML") body = importedNode;
					else body.appendChild(importedNode);
					_sanitizeAttachedShadowRoots(importedNode);
				} else {
					if (!RETURN_DOM && !SAFE_FOR_TEMPLATES && !WHOLE_DOCUMENT && dirty.indexOf("<") === -1) return trustedTypesPolicy && RETURN_TRUSTED_TYPE ? _createTrustedHTML(dirty) : dirty;
					body = _initDocument(dirty);
					if (!body) return RETURN_DOM ? null : RETURN_TRUSTED_TYPE ? emptyHTML : "";
				}
				if (body && FORCE_BODY) _forceRemove(body.firstChild);
				const nodeIterator = _createNodeIterator(inPlace ? dirty : body);
				try {
					while (currentNode = nodeIterator.nextNode()) {
						_sanitizeElements(currentNode);
						_sanitizeAttributes(currentNode);
						if (_isDocumentFragment(currentNode.content)) _sanitizeShadowDOM2(currentNode.content);
					}
				} catch (error) {
					if (inPlace) _neutralizeRoot(dirty);
					throw error;
				}
				if (inPlace) {
					arrayForEach(DOMPurify.removed, (entry) => {
						if (entry.element) _neutralizeSubtree(entry.element);
					});
					if (SAFE_FOR_TEMPLATES) _scrubTemplateExpressions2(dirty);
					return dirty;
				}
				if (RETURN_DOM) {
					if (SAFE_FOR_TEMPLATES) _scrubTemplateExpressions2(body);
					if (RETURN_DOM_FRAGMENT) {
						returnNode = createDocumentFragment.call(body.ownerDocument);
						while (body.firstChild) returnNode.appendChild(body.firstChild);
					} else returnNode = body;
					if (ALLOWED_ATTR.shadowroot || ALLOWED_ATTR.shadowrootmode) returnNode = importNode.call(originalDocument, returnNode, true);
					return returnNode;
				}
				let serializedHTML = WHOLE_DOCUMENT ? body.outerHTML : body.innerHTML;
				if (WHOLE_DOCUMENT && ALLOWED_TAGS["!doctype"] && body.ownerDocument && body.ownerDocument.doctype && body.ownerDocument.doctype.name && regExpTest(DOCTYPE_NAME, body.ownerDocument.doctype.name)) serializedHTML = "<!DOCTYPE " + body.ownerDocument.doctype.name + ">\n" + serializedHTML;
				if (SAFE_FOR_TEMPLATES) serializedHTML = _stripTemplateExpressions(serializedHTML);
				return trustedTypesPolicy && RETURN_TRUSTED_TYPE ? _createTrustedHTML(serializedHTML) : serializedHTML;
			};
			DOMPurify.setConfig = function() {
				_parseConfig(arguments.length > 0 && arguments[0] !== void 0 ? arguments[0] : {});
				SET_CONFIG = true;
				SET_CONFIG_ALLOWED_TAGS = ALLOWED_TAGS;
				SET_CONFIG_ALLOWED_ATTR = ALLOWED_ATTR;
			};
			DOMPurify.clearConfig = function() {
				CONFIG = null;
				SET_CONFIG = false;
				SET_CONFIG_ALLOWED_TAGS = null;
				SET_CONFIG_ALLOWED_ATTR = null;
				trustedTypesPolicy = defaultTrustedTypesPolicy;
				emptyHTML = "";
			};
			DOMPurify.isValidAttribute = function(tag, attr, value) {
				if (!CONFIG) _parseConfig({});
				return _isValidAttribute(transformCaseFunc(tag), transformCaseFunc(attr), value);
			};
			DOMPurify.addHook = function(entryPoint, hookFunction) {
				if (typeof hookFunction !== "function") return;
				if (!objectHasOwnProperty(hooks, entryPoint)) return;
				arrayPush(hooks[entryPoint], hookFunction);
			};
			DOMPurify.removeHook = function(entryPoint, hookFunction) {
				if (!objectHasOwnProperty(hooks, entryPoint)) return;
				if (hookFunction !== void 0) {
					const index = arrayLastIndexOf(hooks[entryPoint], hookFunction);
					return index === -1 ? void 0 : arraySplice(hooks[entryPoint], index, 1)[0];
				}
				return arrayPop(hooks[entryPoint]);
			};
			DOMPurify.removeHooks = function(entryPoint) {
				if (!objectHasOwnProperty(hooks, entryPoint)) return;
				hooks[entryPoint] = [];
			};
			DOMPurify.removeAllHooks = function() {
				hooks = _createHooksMap();
			};
			return DOMPurify;
		}
		var purify = createDOMPurify();
		//#endregion
		//#region src/client/html/basic-document.ts
		/** Static HTML preview with no scripts, network resources, forms or nested frames. */
		/**
		* Prepare static content before the browser can load any document resources.
		* @param data - complete UTF-8 HTML bytes.
		* @returns document with the restrictive CSP first in its head.
		*/
		function createBasicHtmlDocument(data) {
			const clean = purify.sanitize(decodeText(data), {
				WHOLE_DOCUMENT: true,
				FORBID_TAGS: [
					"noscript",
					"base",
					"link",
					"meta",
					"iframe",
					"frame",
					"object",
					"embed",
					"set",
					"animate",
					"animateMotion",
					"animateTransform"
				],
				FORBID_ATTR: ["href", "xlink:href"]
			});
			const parsed = new DOMParser().parseFromString(clean, "text/html");
			const policy = parsed.createElement("meta");
			policy.setAttribute("http-equiv", "Content-Security-Policy");
			policy.setAttribute("content", "default-src 'none'; script-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; media-src data:; connect-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'");
			parsed.head.prepend(policy);
			return `<!doctype html>${parsed.documentElement.outerHTML}`;
		}
		//#endregion
		//#region src/client/html/pack.ts
		const MAX_ASSET_BYTES = 4 * 1024 * 1024;
		const MAX_TOTAL_BYTES = 32 * 1024 * 1024;
		const MAX_ASSETS = 64;
		/** Whether this reference can be read relative to the original document, never the parent application URL. */
		function relative(reference) {
			return reference.length > 0 && !/^(?:[a-z][a-z\d+.-]*:|[/\\#?])/iu.test(reference) && !reference.includes("\0");
		}
		/**
		* Collect static dependencies without executing or mounting document elements in the parent page.
		* A base element leaves URL resolution to the browser. Only direct .js classic scripts and .css
		* links are packed; local CSS url/import, modules and dynamically constructed URLs are unsupported.
		* @param data - complete UTF-8 HTML bytes.
		* @param readRelative - original-document-scoped read, never exposed to the iframe.
		* @param signal - stops reads and prevents publication after cancellation.
		* @returns complete HTML and its finite static asset set; decoding, limits and read failures reject.
		*/
		async function packHtml(data, readRelative, signal) {
			signal.throwIfAborted();
			let total = data.byteLength;
			if (total > MAX_TOTAL_BYTES) throw new Error("HTML package exceeds its total byte limit");
			const template = document.createElement("template");
			template.innerHTML = decodeText(data);
			const assets = [];
			if (template.content.querySelector("base[href]") !== null) return {
				data,
				assets
			};
			const seen = /* @__PURE__ */ new Set();
			for (const element of template.content.querySelectorAll("script[src],link[href]")) {
				const script = element.localName === "script";
				const type = element.getAttribute("type")?.trim().toLowerCase() ?? "";
				if (script && ![
					"",
					"text/javascript",
					"application/javascript"
				].includes(type)) continue;
				if (!script && !(element.getAttribute("rel") ?? "").toLowerCase().split(/\s+/u).includes("stylesheet")) continue;
				const reference = element.getAttribute(script ? "src" : "href");
				const suffix = reference.search(/[?#]/u);
				const path = suffix === -1 ? reference : reference.slice(0, suffix);
				if (!relative(reference) || !(script ? /\.js$/iu : /\.css$/iu).test(path)) continue;
				const kind = script ? "script" : "stylesheet";
				const key = `${kind}:${reference}`;
				if (seen.has(key)) continue;
				if (assets.length >= MAX_ASSETS) throw new Error("HTML package exceeds its asset count limit");
				signal.throwIfAborted();
				const asset = await readRelative(reference, signal);
				signal.throwIfAborted();
				const size = asset.data.byteLength;
				if (size > MAX_ASSET_BYTES) throw new Error("HTML asset exceeds its byte limit");
				total += size;
				if (total > MAX_TOTAL_BYTES) throw new Error("HTML package exceeds its total byte limit");
				decodeText(asset.data);
				assets.push({
					kind,
					reference,
					data: asset.data
				});
				seen.add(key);
			}
			return {
				data,
				assets
			};
		}
		//#endregion
		//#region src/client/html/read-relative.ts
		/**
		* Bind a package reader to the original HTML file's address.
		* @param readRelated - workspace reader using the Session in the root HTML address.
		* @param address - root HTML file address.
		* @param lifetime - tab lifetime.
		* @param addResource - subscribes to the dependency path reported by the Host.
		* @returns a reader that strips URL query/fragment, decodes one path, and preserves Host failures.
		*/
		function createReadHtmlRelative(readRelated, address, lifetime, addResource) {
			return async (reference, signal) => {
				const suffix = reference.search(/[?#]/u);
				const path = decodeURIComponent(suffix === -1 ? reference : reference.slice(0, suffix));
				if (path.length === 0 || /^(?:[a-z][a-z\d+.-]*:|[/\\])/iu.test(path) || path.includes("\0") || path.includes("\\")) throw new Error("HTML dependency must use a relative file path");
				const combined = AbortSignal.any([lifetime, signal]);
				combined.throwIfAborted();
				const file = hostFileOf(address);
				const result = await readRelated(address, path, combined);
				combined.throwIfAborted();
				if (!result.ok) {
					if ("path" in result.error.details && typeof result.error.details.path === "string") addResource(sessionFileAddress(file.sessionId, result.error.details.path));
					throw new Error(result.error.message);
				}
				addResource(sessionFileAddress(file.sessionId, result.value.absolutePath));
				return result.value;
			};
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-sidebar-documentpreview/src/client/html/HtmlBody.module.css.mjs
		const css$6 = ".Vthqka_frame{background:var(--dsw-alias-bg-base);border:none;width:100%;height:100%;min-height:240px;display:block}.Vthqka_status{color:var(--dsw-alias-label-secondary);white-space:normal;margin:0;padding:10px}";
		const tagId$6 = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview/HtmlBody.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$6) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview";
			tag.dataset.pluginCss = tagId$6;
			tag.textContent = css$6;
			document.head.appendChild(tag);
		}
		var HtmlBody_module_css_default = {
			"frame": "Vthqka_frame",
			"status": "Vthqka_status"
		};
		//#endregion
		//#region src/client/html/HtmlBody.tsx
		/** Static or interactive HTML in an opaque iframe, without parent application access. */
		/** One mounted file owns its root Blob; replacing content also replaces the browsing context. */
		function HtmlFrame({ data, resourceAddress, readRelated, addResource, setResources, signal, frameName, t }) {
			const [frame, setFrame] = (0, react.useState)();
			(0, react.useEffect)(() => {
				const controller = new AbortController();
				const resources = /* @__PURE__ */ new Set();
				const readRelative = createReadHtmlRelative(readRelated, resourceAddress, signal, (address) => {
					resources.add(address);
					addResource(address);
				});
				let url;
				(async () => {
					try {
						const bundle = await packHtml(data, readRelative, controller.signal);
						controller.signal.throwIfAborted();
						const html = createHtmlDocument(bundle);
						url = URL.createObjectURL(new Blob([html], { type: "text/html" }));
						setFrame({
							data,
							readRelated,
							url
						});
					} catch {
						if (!controller.signal.aborted) setFrame({
							data,
							readRelated,
							url: void 0
						});
					} finally {
						if (!controller.signal.aborted && !signal.aborted) setResources([...resources]);
					}
				})();
				return () => {
					controller.abort();
					if (url !== void 0) URL.revokeObjectURL(url);
				};
			}, [
				data,
				readRelated,
				resourceAddress,
				addResource,
				setResources,
				signal
			]);
			if (frame?.data !== data || frame.readRelated !== readRelated) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(LoadingIndicator, { label: t("loading") });
			if (frame.url === void 0) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
				className: HtmlBody_module_css_default.status,
				role: "alert",
				children: t("failed")
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("iframe", {
				name: frameName,
				className: HtmlBody_module_css_default.frame,
				src: frame.url,
				sandbox: "allow-scripts",
				title: t("frame"),
				"data-html-preview": true
			}, frame.url);
		}
		/**
		* Render complete HTML with the standard file and tab hooks.
		* @param props - document bytes, hooks, related-file reader and locale.
		* @returns an isolated HTML document, or nothing for text delivery.
		*/
		function HtmlBody({ content, resourceAddress, readRelated, useTabInfo, useInteractivePreview, addResource, setResources, t }) {
			const interactivePreview = useInteractivePreview((value) => value);
			const { tab } = useTabInfo();
			(0, react.useEffect)(() => {
				if (!interactivePreview) setResources([]);
			}, [interactivePreview, setResources]);
			if (content.kind !== "bytes") return null;
			const frameName = `dsh-sidebar-html-${tab.id}`;
			if (!interactivePreview) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(BasicHtmlFrame, {
				data: content.data,
				frameName,
				t
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(HtmlFrame, {
				data: content.data,
				resourceAddress,
				readRelated,
				signal: tab.signal,
				frameName,
				addResource,
				setResources,
				t
			}, resourceAddress);
		}
		/** Static preview mounts a separate browsing context so a mode change retires running scripts. */
		function BasicHtmlFrame({ data, frameName, t }) {
			const html = (0, react.useMemo)(() => {
				try {
					return createBasicHtmlDocument(data);
				} catch {
					return;
				}
			}, [data]);
			if (html === void 0) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
				className: HtmlBody_module_css_default.status,
				role: "alert",
				children: t("failed")
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("iframe", {
				name: frameName,
				className: HtmlBody_module_css_default.frame,
				srcDoc: html,
				sandbox: "",
				title: t("frame"),
				"data-html-preview": true
			});
		}
		//#endregion
		//#region src/client/html/locales.ts
		/** Locale-owned HTML implementation name and iframe status text. */
		const zh$5 = {
			title: "HTML",
			frame: "HTML 文档预览",
			loading: "文档渲染中...",
			failed: "无法预览这份 HTML 文档"
		};
		/** English dictionary with the same keys as the Chinese dictionary. */
		const en$5 = {
			title: "HTML",
			frame: "HTML document preview",
			loading: "Rendering document...",
			failed: "This HTML document could not be previewed."
		};
		//#endregion
		//#region src/client/html/index.ts
		/** HTML implementation identity, shared by metadata and the keyed slot. */
		const HTML_BODY_ID = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview/html";
		/**
		* Describe the builtin HTML renderer's file types and loading mode.
		* @param title - locale-owned implementation name.
		* @returns metadata for complete HTML documents.
		*/
		function htmlBodyDefinition(title) {
			return {
				id: HTML_BODY_ID,
				extensions: ["html", "htm"],
				priority: "builtin",
				title,
				loading: "bytes-complete",
				wrap: false
			};
		}
		/**
		* Register the HTML dictionary, metadata and body with reversible effects.
		* @param ctx - owning plugin context.
		*/
		function apply$6(ctx) {
			const t = ctx.locale.bind("documentHtml");
			ctx.effect(() => ctx.locale.register("documentHtml", {
				zh: zh$5,
				en: en$5
			}));
			ctx.effect(() => ctx.documentPreviews.register(htmlBodyDefinition(() => t("title"))));
			ctx.effect(() => ctx.slots.inject("sidebar.right.tab.document", () => ctx.slots.register({
				name: "sidebar.right.tab.document",
				key: HTML_BODY_ID,
				locale: "documentHtml",
				inject: () => ({
					hooks: { interactivePreview: ctx.configForms.developerTools.enabled },
					readRelated: (address, relativePath, signal) => {
						const file = hostFileOf(address);
						return ctx.remote.workspaceFiles.readBytes(file.sessionId, relativePath, { baseFile: file.path }, signal);
					}
				})
			}, HtmlBody)));
		}
		//#endregion
		//#region src/client/document/tab-lifetime.ts
		/**
		* Release retained view state on tab closure or plugin disposal.
		* @param ctx - owning preview plugin context.
		* @returns a callback accepting the tab, its lifetime signal, and its store's forget action; repeated holds share one listener.
		*/
		function retainDocumentTabs(ctx) {
			const retained = /* @__PURE__ */ new Map();
			ctx.effect(() => () => {
				for (const forget of retained.values()) forget();
			});
			return (tabId, signal, forgetTab) => {
				if (signal.aborted) {
					forgetTab(tabId);
					return;
				}
				if (retained.has(signal)) return;
				const forget = () => {
					signal.removeEventListener("abort", forget);
					retained.delete(signal);
					forgetTab(tabId);
				};
				retained.set(signal, forget);
				signal.addEventListener("abort", forget, { once: true });
			};
		}
		//#endregion
		//#region src/client/zoom/types.ts
		/** Default preference for a newly opened zoomable preview. */
		const FIT_WIDTH = { kind: "fit-width" };
		/** Minimum scale retained by a fixed zoom preference. */
		const MIN_FIXED_ZOOM = .25;
		/** Scale interval used by the incremental controls. */
		const ZOOM_STEP = .25;
		/** Fixed scale choices shown in the zoom menu. */
		const ZOOM_OPTIONS = [
			.25,
			.5,
			1,
			1.5,
			2
		];
		//#endregion
		//#region src/client/zoom/store.ts
		/** Tab-local zoom preferences for renderers without additional view state. */
		/**
		* Create tab-local zoom state for a document renderer registration.
		* @returns a store declaration retaining zoom until its tab closes.
		*/
		function createZoomStore() {
			return (0, _deepseek_ai_dsh_client_store.defineStore)({
				init: () => ({ byTab: {} }),
				actions: {
					zoom: (draft, tabId, preference) => {
						draft.byTab[tabId] = preference;
					},
					forget: (draft, tabId) => {
						const { [tabId]: _closed, ...remaining } = draft.byTab;
						draft.byTab = remaining;
					}
				}
			});
		}
		/** Default zoom value for a tab without retained state. */
		const DEFAULT_ZOOM = FIT_WIDTH;
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-sidebar-documentpreview/src/client/zoom/ZoomControls.module.css.mjs
		const css$5 = ".pPVVza_panel{z-index:2;inset-inline-start:50%;box-sizing:border-box;border:.5px solid var(--dsw-alias-border-l3);border-radius:var(--dsw-radius-lg);color:var(--dsw-alias-label-primary);background:var(--dsw-alias-bg-layer-1);opacity:0;pointer-events:none;will-change:opacity, transform;align-items:center;gap:2px;padding:4px 6px;font-size:12px;line-height:18px;transition:opacity .16s ease-in,transform .16s cubic-bezier(.4,0,1,1);display:flex;position:absolute;bottom:14px;transform:translate(-50%,16px)scale(.98);box-shadow:0 8px 24px light-dark(#0000001a,#00000060),0 2px 5px light-dark(#00000009,#00000035)}.pPVVza_visible{opacity:1;pointer-events:auto;transition:opacity .14s ease-out,transform .18s cubic-bezier(.22,1,.36,1);transform:translate(-50%)scale(1)}.pPVVza_controlAnchor{display:flex}.pPVVza_zoomButton{flex:none;width:28px;padding:0}.pPVVza_percent{font-variant-numeric:tabular-nums;justify-content:center;align-items:center;gap:2px;min-width:58px;padding-inline:6px;display:inline-flex}.pPVVza_zoomMenu{min-width:112px}@media (hover:none),(pointer:coarse){.pPVVza_panel{opacity:1;pointer-events:auto;transform:translate(-50%)scale(1)}}@media (prefers-reduced-motion:reduce){.pPVVza_panel,.pPVVza_visible{transition-duration:.01ms}}";
		const tagId$5 = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview/ZoomControls.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$5) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview";
			tag.dataset.pluginCss = tagId$5;
			tag.textContent = css$5;
			document.head.appendChild(tag);
		}
		var ZoomControls_module_css_default = {
			"controlAnchor": "pPVVza_controlAnchor",
			"panel": "pPVVza_panel",
			"percent": "pPVVza_percent",
			"visible": "pPVVza_visible",
			"zoomButton": "pPVVza_zoomButton",
			"zoomMenu": "pPVVza_zoomMenu"
		};
		//#endregion
		//#region src/client/zoom/ZoomControls.tsx
		/** Floating controls shared by every zoomable document renderer. */
		function steppedZoom(zoom, direction) {
			const step = direction === -1 ? Math.ceil(zoom / ZOOM_STEP) - 1 : Math.floor(zoom / ZOOM_STEP) + 1;
			return Math.min(4, Math.max(MIN_FIXED_ZOOM, step * ZOOM_STEP));
		}
		/**
		* Present fit-width, fixed presets, and incremental controls.
		* @param props - current resolved zoom, preference mode, labels, and callbacks.
		* @returns a localized zoom toolbar.
		*/
		const ZoomControls = (0, react.forwardRef)(function ZoomControls({ zoom, fitWidth, labels, visible, onZoom, onFitWidth, onActiveChange }, ref) {
			const [displayZoom, setDisplayZoom] = (0, react.useState)(zoom);
			const [open, setOpen] = (0, react.useState)(false);
			const [hovered, setHovered] = (0, react.useState)(false);
			const [focused, setFocused] = (0, react.useState)(false);
			(0, react.useImperativeHandle)(ref, () => ({ showZoom: setDisplayZoom }), []);
			(0, react.useEffect)(() => {
				onActiveChange(open || hovered || focused);
			}, [
				focused,
				hovered,
				onActiveChange,
				open
			]);
			const items = [
				{
					id: "fit-width",
					label: labels.fitWidth
				},
				{
					type: "separator",
					id: "fit-scale"
				},
				...ZOOM_OPTIONS.map((value) => ({
					id: String(value),
					label: labels.value(value * 100)
				}))
			];
			const selectedId = fitWidth ? "fit-width" : ZOOM_OPTIONS.includes(displayZoom) ? String(displayZoom) : void 0;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: `${ZoomControls_module_css_default.panel} ${visible ? ZoomControls_module_css_default.visible : ""}`,
				role: "toolbar",
				"aria-label": labels.controls,
				"data-document-zoom-controls": true,
				"data-document-zoom-visible": visible || void 0,
				onPointerEnter: () => {
					setHovered(true);
				},
				onPointerLeave: () => {
					setHovered(false);
				},
				onFocusCapture: () => {
					setFocused(true);
				},
				onBlurCapture: () => {
					setFocused(false);
				},
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
						label: labels.out,
						side: "top",
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: ZoomControls_module_css_default.controlAnchor,
							children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
								size: "sm",
								className: ZoomControls_module_css_default.zoomButton,
								"aria-label": labels.out,
								disabled: displayZoom <= MIN_FIXED_ZOOM,
								onMouseDown: (event) => {
									event.preventDefault();
								},
								onClick: () => {
									onZoom(steppedZoom(displayZoom, -1));
								},
								children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("svg", {
									width: "14",
									height: "14",
									viewBox: "0 0 16 16",
									"aria-hidden": "true",
									children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
										d: "M2 8h12",
										fill: "none",
										stroke: "currentColor",
										strokeWidth: "1.5"
									})
								})
							})
						})
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Menu, {
						open,
						side: "top",
						align: "start",
						portal: true,
						compact: true,
						listClassName: ZoomControls_module_css_default.zoomMenu,
						anchor: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(_deepseek_ai_dsh_client_ui_primitives.Button, {
							size: "sm",
							className: ZoomControls_module_css_default.percent,
							"aria-label": labels.menu,
							"aria-expanded": open,
							onMouseDown: (event) => {
								event.preventDefault();
							},
							onClick: () => {
								setOpen((value) => !value);
							},
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: labels.value(Math.round(displayZoom * 100)) }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronDownOutlineRegular, { size: 12 })]
						}),
						items,
						selectedId,
						onClose: () => {
							setOpen(false);
						},
						onSelect: (selected) => {
							setOpen(false);
							if (selected === "fit-width") onFitWidth();
							else onZoom(Number(selected));
						}
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
						label: labels.into,
						side: "top",
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: ZoomControls_module_css_default.controlAnchor,
							children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
								size: "sm",
								className: ZoomControls_module_css_default.zoomButton,
								"aria-label": labels.into,
								disabled: displayZoom >= 4,
								onMouseDown: (event) => {
									event.preventDefault();
								},
								onClick: () => {
									onZoom(steppedZoom(displayZoom, 1));
								},
								children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconPlusOutlineRegular, { size: 14 })
							})
						})
					})
				]
			});
		});
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-sidebar-documentpreview/src/client/zoom/ZoomViewport.module.css.mjs
		const css$4 = ".j32JHa_frame{height:100%;min-height:0;position:relative;overflow:hidden}.j32JHa_scrollport{height:100%;overflow:auto}.j32JHa_surface{max-width:none}.j32JHa_revealZone{height:var(--document-zoom-reveal-height);pointer-events:none;position:absolute;inset:auto 0 0}.j32JHa_frame[data-document-zoom-mode=fit-width] .j32JHa_surface{width:min(var(--document-zoom-width), 100%)}.j32JHa_frame[data-document-zoom-mode=fixed] .j32JHa_surface{width:calc(var(--document-zoom-width) * var(--document-zoom))}";
		const tagId$4 = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview/ZoomViewport.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$4) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview";
			tag.dataset.pluginCss = tagId$4;
			tag.textContent = css$4;
			document.head.appendChild(tag);
		}
		var ZoomViewport_module_css_default = {
			"frame": "j32JHa_frame",
			"revealZone": "j32JHa_revealZone",
			"scrollport": "j32JHa_scrollport",
			"surface": "j32JHa_surface"
		};
		//#endregion
		//#region src/client/zoom/ZoomViewport.tsx
		/** Shared zoom viewport, gesture handling, anchoring, and fit-width measurement. */
		const PINCH_SETTLE_DELAY_MS = 120;
		const CONTROLS_HIDE_DELAY_MS = 420;
		const CONTROLS_REVEAL_HEIGHT_PX = 72;
		function pointerAnchor(clientX, clientY) {
			const lookup = Reflect.get(document, "elementFromPoint");
			if (typeof lookup !== "function") return void 0;
			const hit = Reflect.apply(lookup, document, [clientX, clientY]);
			const surface = hit instanceof Element ? hit.closest("[data-document-zoom-surface]") : null;
			if (surface === null) return void 0;
			const bounds = surface.getBoundingClientRect();
			return {
				surface,
				x: clientX - bounds.left,
				y: clientY - bounds.top,
				clientX,
				clientY
			};
		}
		/** @returns an actual-size scale that only shrinks content wider than the viewport. */
		function fitWidthZoom(viewportWidth, intrinsicWidth, horizontalInset = 0) {
			if (intrinsicWidth === void 0 || intrinsicWidth <= 0) return 1;
			return Math.min(1, Math.max(1, viewportWidth - horizontalInset) / intrinsicWidth);
		}
		/**
		* Keep zoom controls and trackpad gestures independent from document rendering.
		* @param props - renderer content, intrinsic width, tab preference, and localized labels.
		* @returns the owned scrollport and floating zoom controls.
		*/
		function ZoomViewport(props) {
			const [viewportWidth, setViewportWidth] = (0, react.useState)(0);
			const [controlsVisible, setControlsVisible] = (0, react.useState)(false);
			const fitZoom = fitWidthZoom(viewportWidth, props.intrinsicWidth, props.horizontalInset);
			const zoom = props.preference.kind === "fit-width" ? fitZoom : props.preference.scale;
			const zoomRef = (0, react.useRef)(zoom);
			const modeRef = (0, react.useRef)(props.preference.kind);
			const frame = (0, react.useRef)(null);
			const scrollport = (0, react.useRef)(null);
			const controls = (0, react.useRef)(null);
			const pinchTimer = (0, react.useRef)();
			const controlsHideTimer = (0, react.useRef)();
			const pointerInRevealZone = (0, react.useRef)(false);
			const controlsActive = (0, react.useRef)(false);
			const clearControlsHide = (0, react.useCallback)(() => {
				clearTimeout(controlsHideTimer.current);
				controlsHideTimer.current = void 0;
			}, []);
			const showControls = (0, react.useCallback)(() => {
				clearControlsHide();
				setControlsVisible(true);
			}, [clearControlsHide]);
			const scheduleControlsHide = (0, react.useCallback)(() => {
				clearControlsHide();
				if (pointerInRevealZone.current || controlsActive.current || pinchTimer.current !== void 0) return;
				controlsHideTimer.current = setTimeout(() => {
					controlsHideTimer.current = void 0;
					setControlsVisible(false);
				}, CONTROLS_HIDE_DELAY_MS);
			}, [clearControlsHide]);
			const handleControlsActive = (0, react.useCallback)((active) => {
				controlsActive.current = active;
				if (active) showControls();
				else scheduleControlsHide();
			}, [scheduleControlsHide, showControls]);
			const handlePointerMove = (0, react.useCallback)((event) => {
				const inRevealZone = event.pointerType === "mouse" && event.clientY >= event.currentTarget.getBoundingClientRect().bottom - CONTROLS_REVEAL_HEIGHT_PX;
				pointerInRevealZone.current = inRevealZone;
				if (inRevealZone) showControls();
				else scheduleControlsHide();
			}, [scheduleControlsHide, showControls]);
			const handlePointerLeave = (0, react.useCallback)(() => {
				pointerInRevealZone.current = false;
				scheduleControlsHide();
			}, [scheduleControlsHide]);
			const attachFrame = (0, react.useCallback)((node) => {
				frame.current = node;
				node?.style.setProperty("--document-zoom", String(zoomRef.current));
			}, []);
			const attachScrollport = (0, react.useCallback)((node) => {
				scrollport.current = node;
				props.scrollportRef(node);
			}, [props.scrollportRef]);
			const showZoom = (0, react.useCallback)((next, clientX, clientY) => {
				const node = scrollport.current;
				const previous = zoomRef.current;
				next = Math.min(4, Math.max(MIN_FIXED_ZOOM, next));
				if (next === previous) return previous;
				const bounds = node.getBoundingClientRect();
				const x = clientX === void 0 ? node.clientWidth / 2 : clientX - bounds.left;
				const y = clientY === void 0 ? node.clientHeight / 2 : clientY - bounds.top;
				const anchor = pointerAnchor(bounds.left + x, bounds.top + y);
				const previousScrollLeft = node.scrollLeft;
				const previousScrollTop = node.scrollTop;
				const ratio = next / previous;
				zoomRef.current = next;
				modeRef.current = "fixed";
				frame.current?.setAttribute("data-document-zoom-mode", "fixed");
				frame.current?.style.setProperty("--document-zoom", String(next));
				controls.current?.showZoom(next);
				if (anchor === void 0) {
					node.scrollLeft = (previousScrollLeft + x) * ratio - x;
					node.scrollTop = (previousScrollTop + y) * ratio - y;
				} else {
					const nextBounds = anchor.surface.getBoundingClientRect();
					node.scrollLeft += nextBounds.left + anchor.x * ratio - anchor.clientX;
					node.scrollTop += nextBounds.top + anchor.y * ratio - anchor.clientY;
				}
				return next;
			}, []);
			const setZoom = (0, react.useCallback)((next) => {
				clearTimeout(pinchTimer.current);
				pinchTimer.current = void 0;
				props.onPreference({
					kind: "fixed",
					scale: showZoom(next)
				});
			}, [props.onPreference, showZoom]);
			const fitWidth = (0, react.useCallback)(() => {
				clearTimeout(pinchTimer.current);
				pinchTimer.current = void 0;
				const node = frame.current;
				zoomRef.current = fitZoom;
				modeRef.current = "fit-width";
				node?.setAttribute("data-document-zoom-mode", "fit-width");
				node?.style.setProperty("--document-zoom", String(fitZoom));
				controls.current?.showZoom(fitZoom);
				props.onPreference(FIT_WIDTH);
			}, [fitZoom, props.onPreference]);
			(0, react.useLayoutEffect)(() => {
				const node = scrollport.current;
				const measure = () => {
					setViewportWidth(node.clientWidth);
				};
				measure();
				if (typeof ResizeObserver === "undefined") return;
				const observer = new ResizeObserver(measure);
				observer.observe(node);
				return () => {
					observer.disconnect();
				};
			}, []);
			(0, react.useEffect)(() => {
				if (pinchTimer.current !== void 0) return;
				zoomRef.current = zoom;
				modeRef.current = props.preference.kind;
				frame.current?.setAttribute("data-document-zoom-mode", props.preference.kind);
				frame.current?.style.setProperty("--document-zoom", String(zoom));
				controls.current?.showZoom(zoom);
				props.onRenderZoom?.(zoom);
			}, [
				props.onRenderZoom,
				props.preference.kind,
				zoom
			]);
			(0, react.useEffect)(() => {
				const node = scrollport.current;
				const clearPendingPinch = () => {
					clearTimeout(pinchTimer.current);
					pinchTimer.current = void 0;
					clearControlsHide();
				};
				const wheel = (event) => {
					if (!event.ctrlKey || props.signal.aborted) return;
					event.preventDefault();
					showControls();
					const unit = event.deltaMode === WheelEvent.DOM_DELTA_LINE ? 16 : event.deltaMode === WheelEvent.DOM_DELTA_PAGE ? node.clientHeight : 1;
					const delta = Math.max(-40, Math.min(40, event.deltaY * unit));
					showZoom(zoomRef.current * Math.exp(-delta * .01), event.clientX, event.clientY);
					clearTimeout(pinchTimer.current);
					pinchTimer.current = setTimeout(() => {
						pinchTimer.current = void 0;
						props.onPreference({
							kind: "fixed",
							scale: zoomRef.current
						});
						scheduleControlsHide();
					}, PINCH_SETTLE_DELAY_MS);
				};
				node.addEventListener("wheel", wheel, { passive: false });
				props.signal.addEventListener("abort", clearPendingPinch, { once: true });
				return () => {
					node.removeEventListener("wheel", wheel);
					props.signal.removeEventListener("abort", clearPendingPinch);
					clearPendingPinch();
				};
			}, [
				clearControlsHide,
				props.onPreference,
				props.signal,
				scheduleControlsHide,
				showControls,
				showZoom
			]);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
				ref: attachFrame,
				className: ZoomViewport_module_css_default.frame,
				"data-document-zoom-frame": true,
				"data-document-zoom-mode": modeRef.current,
				style: {
					"--document-zoom": zoomRef.current,
					"--document-zoom-reveal-height": `${String(CONTROLS_REVEAL_HEIGHT_PX)}px`
				},
				onPointerMove: handlePointerMove,
				onPointerLeave: handlePointerLeave,
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						ref: attachScrollport,
						className: ZoomViewport_module_css_default.scrollport,
						"data-document-zoom-scrollport": true,
						children: props.children
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						className: ZoomViewport_module_css_default.revealZone,
						"data-document-zoom-reveal-zone": true,
						"aria-hidden": "true"
					}),
					props.intrinsicWidth !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ZoomControls, {
						ref: controls,
						zoom: zoomRef.current,
						fitWidth: modeRef.current === "fit-width",
						labels: props.labels,
						visible: controlsVisible,
						onZoom: setZoom,
						onFitWidth: fitWidth,
						onActiveChange: handleControlsActive
					})
				]
			});
		}
		/** Shared surface class for actual-size and fit-width layout. */
		const zoomSurfaceClass = ZoomViewport_module_css_default.surface;
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-sidebar-documentpreview/src/client/image/ImageBody.module.css.mjs
		const css$3 = ".LwneHq_frame{box-sizing:border-box;width:100%;min-width:100%;height:max-content;min-height:100%;font-family:var(--dsw-font,sans-serif);white-space:normal;padding:12px;display:flex}.LwneHq_frame:has([data-document-loading]){height:100%}.LwneHq_surface{flex:none;max-width:none;margin:auto;display:block}.LwneHq_surface[hidden]{display:none}.LwneHq_image{border-radius:var(--dsw-radius-md);user-select:none;width:100%;max-width:none;height:auto;max-height:none;display:block}.LwneHq_status{box-sizing:border-box;width:100%;min-height:100%;color:var(--dsw-alias-label-secondary);white-space:normal;justify-content:center;align-items:center;margin:0;padding:10px;font-size:13px;line-height:1.5;display:flex}";
		const tagId$3 = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview/ImageBody.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$3) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview";
			tag.dataset.pluginCss = tagId$3;
			tag.textContent = css$3;
			document.head.appendChild(tag);
		}
		var ImageBody_module_css_default = {
			"frame": "LwneHq_frame",
			"image": "LwneHq_image",
			"status": "LwneHq_status",
			"surface": "LwneHq_surface"
		};
		//#endregion
		//#region src/client/image/ImageBody.tsx
		/** Complete image bytes rendered in a shared zoom viewport. */
		const IMAGE_MEDIA_TYPES = {
			png: "image/png",
			jpg: "image/jpeg",
			jpeg: "image/jpeg",
			gif: "image/gif",
			webp: "image/webp",
			bmp: "image/bmp",
			ico: "image/x-icon",
			svg: "image/svg+xml"
		};
		/**
		* Resolve a supported filename to the media type assigned to its Blob.
		* @param path - decoded workspace file path.
		* @returns the image media type, or undefined for an unregistered suffix.
		*/
		function imageMediaType(path) {
			const normalized = path.replaceAll("\\", "/");
			const name = normalized.slice(normalized.lastIndexOf("/") + 1).toLowerCase();
			return IMAGE_MEDIA_TYPES[name.slice(name.lastIndexOf(".") + 1)];
		}
		/**
		* Present complete image bytes with fit-width and fixed-scale viewing.
		* @param props - document bytes, resource identity, and locale.
		* @returns a rounded image fitted or scaled at its intrinsic aspect ratio.
		*/
		function ImageBody(props) {
			const { content, resourceAddress, t } = props;
			const { tab } = props.useTabInfo();
			const preference = props.useStore((state) => state.byTab[tab.id] ?? DEFAULT_ZOOM);
			const path = (0, react.useMemo)(() => hostFileOf(resourceAddress).path, [resourceAddress]);
			const mediaType = imageMediaType(path);
			const data = content.kind === "bytes" ? content.data : void 0;
			const [source, setSource] = (0, react.useState)();
			const setPreference = (0, react.useCallback)((value) => {
				props.actions.zoom(tab.id, value);
			}, [props.actions, tab.id]);
			(0, react.useEffect)(() => {
				props.retainTab(tab.id, tab.signal);
			}, [
				props.retainTab,
				tab.id,
				tab.signal
			]);
			(0, react.useEffect)(() => {
				if (data === void 0 || mediaType === void 0) return;
				let url;
				try {
					url = URL.createObjectURL(new Blob([data], { type: mediaType }));
					setSource({
						kind: "ready",
						data,
						mediaType,
						url
					});
				} catch {
					setSource({
						kind: "failed",
						data,
						mediaType
					});
				}
				return () => {
					if (url !== void 0) URL.revokeObjectURL(url);
				};
			}, [data, mediaType]);
			if (data === void 0 || mediaType === void 0) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
				className: ImageBody_module_css_default.status,
				role: "alert",
				children: t("unsupported")
			});
			if (source?.data !== data || source.mediaType !== mediaType) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(LoadingIndicator, { label: t("loading") });
			if (source.kind === "failed") return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
				className: ImageBody_module_css_default.status,
				role: "alert",
				children: t("failed")
			});
			const { name } = pathPartsOf(path);
			const labels = {
				controls: t("zoomControls"),
				menu: t("zoomMenu"),
				out: t("zoomOut"),
				into: t("zoomIn"),
				fitWidth: t("zoomFitWidth"),
				value: (percent) => t("zoomValue", { percent })
			};
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(LoadedImage, {
				url: source.url,
				name,
				preference,
				onPreference: setPreference,
				labels,
				signal: tab.signal,
				scrollportRef: props.scrollportRef,
				t
			}, source.url);
		}
		/** SVG stays in the browser's static image mode because its bytes only reach an img Blob URL. */
		function LoadedImage({ url, name, preference, onPreference, labels, signal, scrollportRef, t }) {
			const [state, setState] = (0, react.useState)("loading");
			const [width, setWidth] = (0, react.useState)();
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ZoomViewport, {
				preference,
				intrinsicWidth: width,
				horizontalInset: 24,
				labels,
				signal,
				scrollportRef,
				onPreference,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: ImageBody_module_css_default.frame,
					"data-image-preview": true,
					children: [
						state === "loading" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(LoadingIndicator, { label: t("loading") }),
						state === "failed" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
							className: ImageBody_module_css_default.status,
							role: "alert",
							children: t("failed")
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: `${ImageBody_module_css_default.surface} ${zoomSurfaceClass}`,
							"data-document-zoom-surface": true,
							style: { "--document-zoom-width": `${width ?? 0}px` },
							hidden: state !== "ready",
							children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("img", {
								className: ImageBody_module_css_default.image,
								src: url,
								alt: t("preview", { name }),
								decoding: "async",
								draggable: false,
								referrerPolicy: "no-referrer",
								onLoad: (event) => {
									setWidth(event.currentTarget.naturalWidth);
									setState("ready");
								},
								onError: () => {
									setState("failed");
								}
							})
						})
					]
				})
			});
		}
		//#endregion
		//#region src/client/zoom/locales.ts
		/** Shared zoom copy embedded into renderer-owned dictionaries. */
		const zoomZh = {
			zoomControls: "缩放控件",
			zoomMenu: "选择缩放比例",
			zoomOut: "缩小",
			zoomIn: "放大",
			zoomFitWidth: "适应宽度",
			zoomValue: "{percent}%"
		};
		/** Shared English zoom copy. */
		const zoomEn = {
			zoomControls: "Zoom controls",
			zoomMenu: "Choose zoom",
			zoomOut: "Zoom out",
			zoomIn: "Zoom in",
			zoomFitWidth: "Fit width",
			zoomValue: "{percent}%"
		};
		//#endregion
		//#region src/client/image/locales.ts
		/** Locale-owned image renderer labels and status text. */
		const zh$4 = {
			...zoomZh,
			title: "图片",
			preview: "图片预览：{name}",
			loading: "文档渲染中...",
			failed: "无法显示这张图片",
			unsupported: "图片预览需要完整文件内容"
		};
		/** English dictionary with the same keys as the Chinese dictionary. */
		const en$4 = {
			...zoomEn,
			title: "Image",
			preview: "Image preview: {name}",
			loading: "Rendering document...",
			failed: "This image could not be displayed.",
			unsupported: "Image preview requires the complete file contents."
		};
		//#endregion
		//#region src/client/image/index.ts
		/** Image implementation identity, shared by metadata and the keyed slot. */
		const IMAGE_BODY_ID = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview/image";
		/** File suffixes rendered by the builtin image body. */
		const IMAGE_EXTENSIONS = [
			"png",
			"jpg",
			"jpeg",
			"gif",
			"webp",
			"bmp",
			"ico",
			"svg"
		];
		/** Bitmap suffixes whose bytes are unreadable as text; SVG stays out because its XML source is worth reading. */
		const BINARY_IMAGE_EXTENSIONS = [
			"png",
			"jpg",
			"jpeg",
			"gif",
			"webp",
			"bmp",
			"ico"
		];
		/**
		* Describe the builtin image renderer independently from its keyed body slot.
		* @param title - locale-owned implementation name.
		* @returns metadata for complete image files.
		*/
		function imageBodyDefinition(title) {
			return {
				id: IMAGE_BODY_ID,
				extensions: IMAGE_EXTENSIONS,
				binaryExtensions: BINARY_IMAGE_EXTENSIONS,
				priority: "builtin",
				title,
				loading: "bytes-complete",
				wrap: false
			};
		}
		/**
		* Register the image dictionary, metadata, and body with reversible effects.
		* @param ctx - owning plugin context.
		*/
		function apply$5(ctx) {
			const t = ctx.locale.bind("sidebarImage");
			ctx.effect(() => ctx.locale.register("sidebarImage", {
				zh: zh$4,
				en: en$4
			}), "document-image: dictionaries");
			ctx.effect(() => ctx.documentPreviews.register(imageBodyDefinition(() => t("title"))), "document-image: metadata");
			const store = createZoomStore();
			const retainTab = retainDocumentTabs(ctx);
			ctx.effect(() => ctx.slots.inject("sidebar.right.tab.document", () => ctx.slots.register({
				name: "sidebar.right.tab.document",
				key: IMAGE_BODY_ID,
				locale: "sidebarImage",
				store,
				inject: (_sessionId, actions) => ({ retainTab: (tabId, signal) => {
					retainTab(tabId, signal, actions.forget);
				} })
			}, ImageBody)), "document-image: body");
		}
		//#endregion
		//#region src/client/pdf/LazyPdfBody.tsx
		/** Load the PDF renderer only after a PDF body is mounted. */
		const LoadedPdfBody = (0, react.lazy)(async () => ({ default: (await require.async("./client.pdf.js")).PdfBody }));
		/**
		* Suspend while the package-local PDF chunk arrives.
		* @param props - PDF body props supplied by the document slot.
		* @returns the deferred PDF renderer.
		*/
		function LazyPdfBody(props) {
			const loading = /* @__PURE__ */ (0, react_jsx_runtime.jsx)(LoadingIndicator, { label: props.t("loading") });
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(react.Suspense, {
				fallback: loading,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(LoadedPdfBody, {
					...props,
					loading
				})
			});
		}
		//#endregion
		//#region src/client/pdf/store.ts
		/** Restorable PDF viewing preferences; document objects and canvases remain component-local. */
		/**
		* Declare the last visible page and zoom preference isolated by tab identity.
		* @returns a store declaration instantiated by the document slot for each Session.
		*/
		function createPdfStore() {
			return (0, _deepseek_ai_dsh_client_store.defineStore)({
				init: () => ({ byTab: {} }),
				actions: {
					/** @param draft - view state. @param tabId - owning tab. @param page - selected 1-based page. */
					page: (draft, tabId, page) => {
						draft.byTab[tabId] = {
							...draft.byTab[tabId],
							page
						};
					},
					/** @param draft - view state. @param tabId - owning tab. @param zoom - selected scale multiplier. */
					zoom: (draft, tabId, zoom) => {
						draft.byTab[tabId] = {
							page: draft.byTab[tabId]?.page ?? 1,
							zoom
						};
					},
					/** @param draft - view state. @param tabId - closed tab whose preferences are discarded. */
					forget: (draft, tabId) => {
						const remaining = {};
						for (const [id, view] of Object.entries(draft.byTab)) if (id !== tabId) remaining[id] = view;
						draft.byTab = remaining;
					}
				}
			});
		}
		//#endregion
		//#region src/client/pdf/locales.ts
		/** Copy owned by the PDF renderer. */
		const zh$3 = {
			...zoomZh,
			title: "PDF",
			pageImage: "PDF 第 {page} 页",
			loading: "文档渲染中...",
			rendering: "正在绘制页面…",
			failed: "无法显示 PDF：{message}",
			password: "此 PDF 需要密码，暂不支持预览",
			workerFailed: "PDF 渲染进程无法继续，请重试",
			unsupported: "PDF 预览需要完整文件内容",
			retry: "重试"
		};
		/** English PDF-renderer dictionary. */
		const en$3 = {
			...zoomEn,
			title: "PDF",
			pageImage: "PDF page {page}",
			loading: "Rendering document...",
			rendering: "Rendering page…",
			failed: "Cannot display PDF: {message}",
			password: "This PDF requires a password; password-protected previews are not supported.",
			workerFailed: "The PDF rendering process could not continue. Please retry.",
			unsupported: "PDF preview requires the complete file contents.",
			retry: "Retry"
		};
		//#endregion
		//#region src/client/pdf/index.ts
		/** PDF metadata and keyed body share this package-local implementation identity. */
		const PDF_BODY_ID = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview/pdf";
		/**
		* Describe the builtin PDF renderer independently from its keyed body slot.
		* @param title - locale-owned implementation name.
		* @returns the complete-file PDF registration.
		*/
		function pdfBodyDefinition(title) {
			return {
				id: PDF_BODY_ID,
				extensions: ["pdf"],
				binaryExtensions: ["pdf"],
				priority: "builtin",
				title,
				loading: "bytes-complete",
				wrap: false
			};
		}
		/** @param ctx - context carrying the locale, document registry, and slot registry. */
		function apply$4(ctx) {
			ctx.effect(() => ctx.locale.register("sidebarPdf", {
				zh: zh$3,
				en: en$3
			}));
			const t = ctx.locale.bind("sidebarPdf");
			ctx.effect(() => ctx.documentPreviews.register(pdfBodyDefinition(() => t("title"))));
			const presentation = pdfBodyRegistration(ctx);
			ctx.effect(() => ctx.slots.inject("sidebar.right.tab.document", () => ctx.slots.register({
				name: "sidebar.right.tab.document",
				key: PDF_BODY_ID,
				locale: "sidebarPdf",
				...presentation
			}, LazyPdfBody)));
		}
		/**
		* Retain PDF viewing state for a document entry's tab lifetime.
		* @param ctx - owning registration context.
		* @returns the store and injection shared by ordinary and Office PDF registrations.
		*/
		function pdfBodyRegistration(ctx) {
			const store = createPdfStore();
			const retainTab = retainDocumentTabs(ctx);
			return {
				store,
				inject: (_sessionId, actions) => ({
					retainTab: (tabId, signal) => {
						retainTab(tabId, signal, actions.forget);
					},
					ZoomViewport,
					zoomSurfaceClass
				})
			};
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-sidebar-documentpreview/src/client/code/CodeBody.module.css.mjs
		const css$2 = ".V6B5hG_renderer{white-space:normal;flex-direction:column;flex:auto;width:100%;min-width:0;height:100%;min-height:0;display:flex;overflow:hidden}.V6B5hG_renderer .V6B5hG_code{--dsl-code-block-border-radius:0px;--dsl-code-block-line-white-space:pre;--dsl-code-block-background:transparent;flex-direction:column;flex:auto;min-width:0;height:100%;min-height:0;margin:0;display:flex;position:static}.V6B5hG_renderer .V6B5hG_code>[data-code-block-content]{flex:auto;min-width:0;min-height:0;display:block;position:relative;overflow:auto}.V6B5hG_renderer .V6B5hG_code>[data-code-block-content]::-webkit-scrollbar-track{margin:2px}.V6B5hG_renderer .V6B5hG_code pre{box-sizing:border-box;white-space:pre;word-break:normal;overflow-wrap:normal;min-width:100%;padding:16px;overflow:visible}.V6B5hG_renderer[data-wrap=true] .V6B5hG_code{--dsl-code-block-line-white-space:pre-wrap}.V6B5hG_renderer[data-wrap=true] .V6B5hG_code pre{white-space:pre-wrap;overflow-wrap:anywhere}";
		const tagId$2 = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview/CodeBody.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$2) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview";
			tag.dataset.pluginCss = tagId$2;
			tag.textContent = css$2;
			document.head.appendChild(tag);
		}
		var CodeBody_module_css_default = {
			"code": "V6B5hG_code",
			"renderer": "V6B5hG_renderer"
		};
		//#endregion
		//#region src/client/code/CodeBody.tsx
		/** @param props - accumulated document contents and framework props. @returns one stable CodeBlock, or no body for byte contents. */
		function CodeBody({ resourceAddress, content, wrap, scrollportRef, t }) {
			if (content.kind !== "text") return null;
			const file = parseFileAddress(resourceAddress);
			if (file === void 0) throw new Error(`ui-sidebar-documentpreview: not a file address "${resourceAddress}"`);
			const language = (0, _deepseek_ai_dsh_client_ui_primitives.languageForPath)(file.path);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: CodeBody_module_css_default.renderer,
				"data-code-preview": true,
				"data-wrap": wrap,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.CodeBlock, {
					className: CodeBody_module_css_default.code,
					contentRef: scrollportRef,
					code: content.text,
					lang: language,
					streaming: !content.eof,
					wrap,
					lineNumbers: true,
					copyLabel: t("copy"),
					copiedLabel: t("copied"),
					toolbarLabels: {
						codeLabel: t("codeBlock.title"),
						wrapLabel: t("codeBlock.wrap"),
						unwrapLabel: t("codeBlock.unwrap")
					}
				})
			});
		}
		//#endregion
		//#region src/client/code/locales.ts
		/** Simplified Chinese dictionary and key source. */
		const zh$2 = {
			title: "代码",
			copy: "复制",
			copied: "已复制"
		};
		/** English dictionary with the same keys. */
		const en$2 = {
			title: "Code",
			copy: "Copy",
			copied: "Copied"
		};
		//#endregion
		//#region src/client/code/index.ts
		const ID = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview/code";
		const NS$1 = "sidebarCodePreview";
		/** @param ctx - owning plugin context. Register localized metadata and the matching keyed document body. */
		function apply$3(ctx) {
			ctx.effect(() => ctx.locale.register(NS$1, {
				zh: zh$2,
				en: en$2
			}));
			const t = ctx.locale.bind(NS$1);
			ctx.effect(() => ctx.documentPreviews.register({
				id: ID,
				extensions: _deepseek_ai_dsh_client_ui_primitives.CODE_HIGHLIGHT_EXTENSIONS,
				priority: "builtin",
				title: () => t("title"),
				loading: "text-pages",
				wrap: true
			}));
			ctx.effect(() => ctx.slots.inject("sidebar.right.tab.document", () => ctx.slots.register({
				name: "sidebar.right.tab.document",
				key: ID,
				locale: NS$1
			}, CodeBody)));
		}
		//#endregion
		//#region src/client/office/locales.ts
		/** Office preview copy and Host render configuration guidance. */
		const zh$1 = {
			title: "Office 文档",
			loading: "文档渲染中...",
			retry: "重试",
			viewMissingFonts: "缺失 {count} 种字体，点击查看",
			missingFontsTitle: "缺失的字体",
			missingFontsDescription: "本次预览无法使用以下字体，预览中的文字和排版可能与原文档不同。",
			missingFontsCount: "{count} 种字体",
			closeDetails: "关闭字体详情",
			unavailable: "Office 预览不可用。请在运行 DeepSeek Harness 的主机上启用文档预览服务。",
			invalid: "无法预览此 Office 文件。文件可能已损坏、受密码保护，或与扩展名不符。",
			tooLarge: "Office 文件或转换后的 PDF 超过预览大小上限，请缩小文件或调整预览配置。",
			failed: "Office 转换失败，未生成可用的 PDF。请检查该文件后重试。",
			timeout: "Office 转换超时，请重试。",
			busy: "Office 预览任务较多，请稍后重试。",
			changed: "文件在读取时已更改，请重新打开预览。"
		};
		/** English translations checked against the Chinese key set. */
		const en$1 = {
			title: "Office document",
			loading: "Rendering document...",
			retry: "Retry",
			viewMissingFonts: "Missing fonts: {count}. Click to view.",
			missingFontsTitle: "Missing fonts",
			missingFontsDescription: "These fonts are unavailable for this preview. Text and layout may differ from the original document.",
			missingFontsCount: "Fonts: {count}",
			closeDetails: "Close font details",
			unavailable: "Office previews are unavailable. Enable the document preview service on the computer running DeepSeek Harness.",
			invalid: "This Office file cannot be previewed. It may be damaged, password protected, or have the wrong extension.",
			tooLarge: "The Office file or converted PDF exceeds the preview size limit. Reduce the file size or adjust the preview configuration.",
			failed: "Office conversion did not produce a usable PDF. Check the file and try again.",
			timeout: "Office conversion timed out. Try again.",
			busy: "Office preview is busy. Try again shortly.",
			changed: "The file changed while being read. Reopen the preview."
		};
		//#endregion
		//#region src/client/office/cache.ts
		/** Bounded successful results; each caller reauthorizes and checks source freshness before reuse. */
		var OfficePreviewCache = class {
			stat;
			convert;
			maxEntries;
			maxBytes;
			maxPending;
			maxReaders;
			currentGeneration;
			busy;
			ready = /* @__PURE__ */ new Map();
			pending = /* @__PURE__ */ new Map();
			bytes = 0;
			readers = 0;
			generation;
			generationQuery = 0;
			acceptedQuery = 0;
			superseded = /* @__PURE__ */ new Error();
			lifetime = new AbortController();
			tasks = /* @__PURE__ */ new Set();
			reads = /* @__PURE__ */ new Set();
			/**
			* @param stat - authorized source metadata lookup.
			* @param convert - Host render Remote returning binary PDF bytes borrowed read-only by callers.
			* @param maxEntries - maximum completed results retained.
			* @param maxBytes - maximum retained PDF byteLength.
			* @param maxPending - maximum unsettled Host conversion requests, including cancellation teardown.
			* @param maxReaders - maximum readers, including metadata lookups.
			* @param generation - current Host renderer generation, checked before cached reuse.
			* @param busy - localized capacity failure.
			*/
			constructor(stat, convert, maxEntries, maxBytes, maxPending, maxReaders, currentGeneration, busy) {
				this.stat = stat;
				this.convert = convert;
				this.maxEntries = maxEntries;
				this.maxBytes = maxBytes;
				this.maxPending = maxPending;
				this.maxReaders = maxReaders;
				this.currentGeneration = currentGeneration;
				this.busy = busy;
			}
			/**
			* Share a conversion without letting one caller cancel another caller's work.
			* Renderer replacement retries authorization once; repeated replacement reports localized capacity failure.
			* @param file - Session authorization scope and source path.
			* @param signal - this caller's lifetime.
			* @param priority - foreground preview or speculative read.
			* @returns current PDF bytes borrowed read-only, or a declared source-read failure; cancellation rejects.
			*/
			async read(file, signal, priority = "foreground") {
				signal.throwIfAborted();
				this.lifetime.signal.throwIfAborted();
				const readerLimit = priority === "background" ? this.maxReaders - 1 : this.maxReaders;
				if (this.readers >= readerLimit || priority === "background" && this.maxPending === 1) throw this.busy();
				this.readers++;
				const operation = this.lookup(file, signal, priority);
				this.reads.add(operation);
				try {
					return await operation;
				} finally {
					this.readers--;
					this.reads.delete(operation);
				}
			}
			async lookup(file, signal, priority) {
				for (let attempt = 0; attempt < 2; attempt++) try {
					return await this.lookupGeneration(file, signal, priority);
				} catch (error) {
					if (error !== this.superseded) throw error;
				}
				throw this.busy();
			}
			async lookupGeneration(file, signal, priority) {
				signal = AbortSignal.any([signal, this.lifetime.signal]);
				signal.throwIfAborted();
				const query = ++this.generationQuery;
				const generation = await this.currentGeneration(signal);
				signal.throwIfAborted();
				if (!generation.ok) return generation;
				if (query < this.acceptedQuery && generation.value !== this.generation) throw this.superseded;
				this.acceptedQuery = Math.max(query, this.acceptedQuery);
				if (generation.value !== this.generation) {
					this.generation = generation.value;
					this.ready.clear();
					this.bytes = 0;
					for (const pending of this.pending.values()) pending.controller.abort(this.superseded);
					this.pending.clear();
				}
				const metadata = await this.stat(file, signal);
				signal.throwIfAborted();
				if (!metadata.ok) return metadata;
				if (generation.value !== this.generation) throw this.superseded;
				const key = JSON.stringify([
					generation.value,
					file.sessionId,
					metadata.value.absolutePath,
					metadata.value.version
				]);
				const cached = this.ready.get(key);
				if (cached !== void 0) {
					this.ready.delete(key);
					this.ready.set(key, cached);
					return cached;
				}
				const pendingKey = JSON.stringify([key, priority]);
				let entry = this.pending.get(pendingKey);
				if (entry === void 0) {
					const pendingLimit = priority === "background" ? this.maxPending - 1 : this.maxPending;
					if (this.tasks.size >= pendingLimit) throw this.busy();
					const controller = new AbortController();
					const promise = Promise.resolve().then(() => {
						controller.signal.throwIfAborted();
						return this.convert(file, controller.signal, priority);
					}).then((result) => {
						controller.signal.throwIfAborted();
						if (generation.value === this.generation && result.ok && result.value.generation === generation.value && result.value.version === metadata.value.version && result.value.absolutePath === metadata.value.absolutePath) this.retain(key, result);
						return result;
					}).finally(() => {
						this.tasks.delete(promise);
						if (this.pending.get(pendingKey)?.controller === controller) this.pending.delete(pendingKey);
					});
					this.tasks.add(promise);
					entry = {
						controller,
						promise,
						users: 0
					};
					this.pending.set(pendingKey, entry);
				}
				const shared = entry;
				shared.users += 1;
				return new Promise((resolve, reject) => {
					let settled = false;
					const finish = () => {
						if (settled) return false;
						settled = true;
						signal.removeEventListener("abort", abort);
						shared.users -= 1;
						if (shared.users === 0 && this.pending.get(pendingKey) === shared) {
							this.pending.delete(pendingKey);
							shared.controller.abort();
						}
						return true;
					};
					const abort = () => {
						const reason = signal.reason;
						finish();
						reject(reason instanceof Error ? reason : new Error("Office preview cancelled", { cause: reason }));
					};
					signal.addEventListener("abort", abort, { once: true });
					shared.promise.then((result) => {
						if (finish()) resolve(result);
					}, (error) => {
						if (finish()) reject(error instanceof Error ? error : new Error("Office preview failed", { cause: error }));
					});
				});
			}
			/** Clear retained bytes, cancel outstanding conversions, and await their completion. */
			async dispose() {
				this.lifetime.abort();
				this.pending.clear();
				this.ready.clear();
				this.bytes = 0;
				await Promise.allSettled([...this.reads, ...this.tasks]);
			}
			retain(key, result) {
				const previous = this.ready.get(key);
				if (previous !== void 0) {
					this.ready.delete(key);
					this.bytes -= previous.value.data.byteLength;
				}
				const size = result.value.data.byteLength;
				if (size > this.maxBytes) return;
				while (this.ready.size >= this.maxEntries || this.bytes + size > this.maxBytes) {
					const oldest = this.ready.entries().next().value;
					this.ready.delete(oldest[0]);
					this.bytes -= oldest[1].value.data.byteLength;
				}
				this.ready.set(key, result);
				this.bytes += size;
			}
		};
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-sidebar-documentpreview/src/client/office/OfficeBody.module.css.mjs
		const css$1 = ".bX0WGG_body{flex-direction:column;height:100%;min-height:0;display:flex}";
		const tagId$1 = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview/OfficeBody.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$1) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview";
			tag.dataset.pluginCss = tagId$1;
			tag.textContent = css$1;
			document.head.appendChild(tag);
		}
		var OfficeBody_module_css_default = { "body": "bX0WGG_body" };
		//#endregion
		//#region src/client/office/OfficeBody.tsx
		/** Office presents retained conversion results and font notices around the shared PDF view. */
		/**
		* Load one Office revision and preserve its result while its tab remains open.
		* @param props - renderer loading request, tab state, conversion callbacks, and PDF slot.
		* @returns conversion status or the PDF scrollport.
		*/
		function OfficeBody(props) {
			const { tab } = props.useTabInfo();
			const { load, retainTab, resourceAddress, t } = props;
			const request = props.content.kind === "renderer" ? props.content : void 0;
			const revision = request?.revision;
			const held = props.useStore((state) => state.byTab[tab.id]);
			const view = held?.revision === revision ? held : void 0;
			const settled = view?.file !== void 0 || view?.failure !== void 0;
			(0, react.useEffect)(() => {
				retainTab(tab.id, tab.signal);
			}, [
				retainTab,
				tab.id,
				tab.signal
			]);
			(0, react.useEffect)(() => {
				if (request === void 0 || settled || tab.signal.aborted) return;
				const controller = new AbortController();
				const signal = AbortSignal.any([controller.signal, tab.signal]);
				load(tab.id, request.revision, hostFileOf(resourceAddress), signal, request.loaded, request.failed);
				return () => {
					controller.abort();
				};
			}, [
				revision,
				resourceAddress,
				tab.id,
				tab.signal,
				load,
				settled
			]);
			const file = view?.file;
			if (request === void 0) return null;
			if (view?.failure !== void 0) {
				const { name } = pathPartsOf(resourceAddress);
				return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: TextPreview_module_css_default.empty,
					"data-textpreview-failed": view.failure.code,
					children: [
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.FileTypeIcon, {
							kind: (0, _deepseek_ai_dsh_client_ui_primitives.classifyFileType)(name),
							size: 36
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
							className: TextPreview_module_css_default.emptyLine,
							children: view.failure.message
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
							size: "sm",
							onClick: request.reload,
							children: t("retry")
						})
					]
				});
			}
			if (file === void 0) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(LoadingIndicator, { label: t("loading") });
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: OfficeBody_module_css_default.body,
				children: props.renderSlot("sidebar.right.tab.document.office.pdf", {
					resourceAddress,
					content: {
						kind: "bytes",
						data: file.data
					},
					wrap: props.wrap,
					scrollportRef: props.scrollportRef,
					addResource: props.addResource,
					setResources: props.setResources
				}, {
					entryKey: "@deepseek-ai/dsh-client-ui-sidebar-documentpreview/office",
					hookContext: props.useTabInfo
				})
			});
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-sidebar-documentpreview/src/client/office/FontNotice.module.css.mjs
		const css = ".NRXtha_anchor{flex:none;display:inline-flex}.NRXtha_anchor .NRXtha_warning,.NRXtha_anchor .NRXtha_warning:hover{color:var(--dsw-alias-state-warn-label)}.NRXtha_anchor .NRXtha_warning:hover,.NRXtha_anchor .NRXtha_warning[aria-expanded=true]{background:var(--dsw-alias-state-warn-tertiary)}.NRXtha_panel{--dsh-scrollbar-thumb:var(--dsw-alias-scrollbar-bg-l2);--dsh-scrollbar-thumb-hover:var(--dsw-alias-scrollbar-hover-l2);z-index:100;box-sizing:border-box;width:min(340px,100vw - 24px);max-height:calc(100dvh - 24px);color:var(--dsw-alias-label-primary);background:var(--dsw-alias-bg-layer-2);border-radius:var(--dsw-radius-lg);box-shadow:var(--dsw-elevation-prominent);border:0;padding:16px;font-size:13px;line-height:1.6;position:fixed;overflow:auto}.NRXtha_panelHeader{justify-content:space-between;align-items:center;gap:12px;display:flex}.NRXtha_panelHeader h3{margin:0;font-size:14px;font-weight:600}.NRXtha_description{color:var(--dsw-alias-label-secondary);margin:8px 0 16px}.NRXtha_count{color:var(--dsw-alias-label-tertiary);margin:0 0 6px;font-size:12px}.NRXtha_fonts{overflow-wrap:anywhere;margin:0;padding:0;list-style:none}.NRXtha_fonts li{border-top:.5px solid var(--dsw-alias-border-l2);padding:8px 0}";
		const tagId = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview/FontNotice.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview";
			tag.dataset.pluginCss = tagId;
			tag.textContent = css;
			document.head.appendChild(tag);
		}
		var FontNotice_module_css_default = {
			"anchor": "NRXtha_anchor",
			"count": "NRXtha_count",
			"description": "NRXtha_description",
			"fonts": "NRXtha_fonts",
			"panel": "NRXtha_panel",
			"panelHeader": "NRXtha_panelHeader",
			"warning": "NRXtha_warning"
		};
		//#endregion
		//#region src/client/office/FontNotice.tsx
		/** Toolbar warning and non-modal details for the current preview’s missing fonts. */
		/**
		* Show a warning while fonts are unavailable for the current preview.
		* @param props - missing font families and localized copy.
		* @returns a warning button and its anchored details, or nothing when fonts are available.
		*/
		function FontNotice({ fonts, t }) {
			const [expanded, setExpanded] = (0, react.useState)(false);
			const open = fonts.length > 0 && expanded;
			const anchor = (0, react.useRef)(null);
			const panel = (0, react.useRef)(null);
			const id = (0, react.useId)();
			const position = (0, _deepseek_ai_dsh_client_ui_primitives.useAnchoredPosition)({
				open,
				anchorRef: anchor,
				panelRef: panel,
				gap: 8,
				margin: 12
			});
			(0, _deepseek_ai_dsh_client_ui_primitives.useDismissOnOutsidePointer)(anchor, open, () => {
				setExpanded(false);
			}, panel);
			const closeDetails = () => {
				setExpanded(false);
				anchor.current?.querySelector("button")?.focus();
			};
			const positioned = position !== null;
			(0, react.useLayoutEffect)(() => {
				if (!open || !positioned) return;
				panel.current?.focus();
			}, [open, positioned]);
			if (fonts.length === 0) return null;
			const label = t("viewMissingFonts", { count: fonts.length });
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tooltip, {
				label,
				side: "bottom",
				delayMs: 500,
				disabled: open,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
					ref: anchor,
					className: FontNotice_module_css_default.anchor,
					"data-office-font-warning": true,
					children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
						type: "button",
						className: clsx(TextPreview_module_css_default.tool, FontNotice_module_css_default.warning),
						"aria-label": label,
						"aria-expanded": open,
						"aria-controls": open ? id : void 0,
						"aria-haspopup": "dialog",
						onClick: () => {
							setExpanded((value) => !value);
						},
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconWarningTriangleOutlineRegular, {})
					})
				})
			}), open && (0, react_dom.createPortal)(/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				ref: panel,
				id,
				role: "dialog",
				"aria-labelledby": `${id}-title`,
				"aria-describedby": `${id}-description`,
				tabIndex: -1,
				className: FontNotice_module_css_default.panel,
				style: {
					...position,
					visibility: position === null ? "hidden" : void 0
				},
				onKeyDown: (event) => {
					if (event.key === "Escape") {
						event.preventDefault();
						event.stopPropagation();
						closeDetails();
					}
				},
				onBlur: (event) => {
					if (event.relatedTarget instanceof Node && !event.currentTarget.contains(event.relatedTarget) && !anchor.current?.contains(event.relatedTarget)) setExpanded(false);
				},
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: FontNotice_module_css_default.panelHeader,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("h3", {
							id: `${id}-title`,
							children: t("missingFontsTitle")
						}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
							size: "sm",
							"aria-label": t("closeDetails"),
							onClick: closeDetails,
							icon: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconCloseOutlineRegular, {})
						})]
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
						id: `${id}-description`,
						className: FontNotice_module_css_default.description,
						children: t("missingFontsDescription")
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
						className: FontNotice_module_css_default.count,
						children: t("missingFontsCount", { count: fonts.length })
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("ul", {
						className: FontNotice_module_css_default.fonts,
						children: fonts.map((font) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)("li", { children: font }, font))
					})
				]
			}), document.body)] });
		}
		//#endregion
		//#region src/client/office/OfficeFontAction.tsx
		/**
		* Expose font details only for the currently loaded Office revision.
		* @param props - toolbar revision, tab reader, Office store, and localized copy.
		* @returns the font warning, or nothing before conversion completes.
		*/
		function OfficeFontAction({ content, useTabInfo, useStore, t }) {
			const { tab } = useTabInfo();
			const held = useStore((state) => state.byTab[tab.id]);
			if (content.kind !== "renderer" || held?.revision !== content.revision || held.file === void 0) return null;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(FontNotice, {
				fonts: held.file.missingFonts,
				t
			}, content.revision);
		}
		//#endregion
		//#region src/client/office/face.ts
		/**
		* Bind Office reads to store actions without exposing Remote work to the component.
		* @param read - authorized conversion reader, including Client cache reuse.
		* @param describeFailure - localized conversion or source-access failure text.
		* @returns Slot inject factory; the file address supplies the read's Session authority.
		*/
		function officeFace(read, describeFailure) {
			return (_sessionId, actions) => ({ load: (tabId, revision, file, signal, loaded, failed) => {
				if (signal.aborted) return;
				actions.loading(tabId, revision);
				read(file, signal).then((result) => {
					if (signal.aborted) return;
					if (result.ok) {
						actions.complete(tabId, revision, result.value);
						loaded(result.value.version);
					} else {
						actions.failed(tabId, revision, {
							code: result.error.code,
							message: describeFailure(result.error)
						});
						failed();
					}
				}, (error) => {
					if (!signal.aborted) {
						actions.failed(tabId, revision, {
							code: "gateway/internal",
							message: describeFailure({ message: error instanceof Error ? error.message : String(error) })
						});
						failed();
					}
				});
			} });
		}
		//#endregion
		//#region src/client/office/store.ts
		/** Loaded Office previews survive body remounts until reload or tab closure. */
		/**
		* Retain Office contents across body remounts within a Session.
		* @returns the tab-content store declaration.
		*/
		function createOfficeStore() {
			return (0, _deepseek_ai_dsh_client_store.defineStore)({
				init: () => ({ byTab: {} }),
				actions: {
					/** @param state - draft. @param tab - owning tab. @param revision - new content revision. */
					loading(state, tab, revision) {
						state.byTab[tab] = { revision };
					},
					/** @param state - draft. @param tab - owning tab. @param revision - completed revision. @param file - borrowed PDF bytes. */
					complete(state, tab, revision, file) {
						state.byTab[tab] = {
							revision,
							file
						};
					},
					/** @param state - draft. @param tab - owning tab. @param revision - failed revision. @param failure - displayable failure. */
					failed(state, tab, revision, failure) {
						state.byTab[tab] = {
							revision,
							failure
						};
					},
					/** @param state - draft. @param tab - closed tab. */
					forget(state, tab) {
						const { [tab]: _closed, ...remaining } = state.byTab;
						state.byTab = remaining;
					}
				}
			});
		}
		//#endregion
		//#region src/client/office/index.ts
		/**
		* Register Office previews with versioned PDF reuse and missing-font notices.
		* @param ctx - Client renderer registry, localized copy, and optional Host Remotes.
		* @param config - Resolved Office preview cache limits.
		*/
		function apply$2(ctx, config) {
			const id = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview/office";
			const extensions = [
				"doc",
				"docx",
				"ppt",
				"pptx"
			];
			ctx.effect(() => ctx.locale.register("sidebarOffice", {
				zh: zh$1,
				en: en$1
			}));
			const t = ctx.locale.bind("sidebarOffice");
			const unavailable = (_file, signal) => {
				signal.throwIfAborted();
				return Promise.reject(new Error(t("unavailable")));
			};
			let read = unavailable;
			ctx.effect(() => ctx.documentPreviews.register({
				id,
				extensions,
				binaryExtensions: extensions,
				priority: "builtin",
				title: () => t("title"),
				loading: "renderer",
				wrap: false
			}));
			const store = createOfficeStore();
			ctx.effect(() => ctx.slots.inject("sidebar.right.tab.document.action", () => ctx.slots.register({
				name: "sidebar.right.tab.document.action",
				key: id,
				locale: "sidebarOffice",
				store
			}, OfficeFontAction)));
			const retainTab = retainDocumentTabs(ctx);
			const documentT = ctx.locale.bind("sidebarDocumentPreview");
			const face = officeFace((file, signal) => read(file, signal), (failure) => "code" in failure ? failureLine(documentT, failure) : documentT("error.unavailable", { message: failure.message }));
			ctx.effect(() => ctx.slots.inject("sidebar.right.tab.document", () => ctx.slots.register({
				name: "sidebar.right.tab.document",
				key: id,
				locale: "sidebarOffice",
				store,
				children: { "sidebar.right.tab.document.office.pdf": {
					kind: "keyed",
					scope: "session",
					inject: { hooks: { tabInfo: documentTabInfoFactory } }
				} },
				inject: (sessionId, actions) => ({
					...face(sessionId, actions),
					retainTab: (tabId, signal) => {
						retainTab(tabId, signal, actions.forget);
					}
				})
			}, OfficeBody)));
			const pdfPresentation = pdfBodyRegistration(ctx);
			ctx.effect(() => ctx.slots.inject("sidebar.right.tab.document.office.pdf", () => ctx.slots.register({
				name: "sidebar.right.tab.document.office.pdf",
				key: id,
				locale: "sidebarPdf",
				...pdfPresentation
			}, LazyPdfBody)));
			ctx.inject([
				"remote",
				"remote.officeToPdf",
				"remote.workspaceFiles"
			], (scope) => {
				const convert = async (file, signal, priority) => {
					signal.throwIfAborted();
					const result = await scope.remote.officeToPdf.render(file.sessionId, file.path, priority, signal);
					signal.throwIfAborted();
					if (!result.ok && result.error.code === "document-render/failed") throw new Error(t(conversionErrorKey(result.error.details.reason)), { cause: result.error });
					return result;
				};
				const createCache = () => new OfficePreviewCache(async (file, signal) => {
					const authorized = await scope.remote.workspaceFiles.readBytes(file.sessionId, file.path, { range: {
						offset: 0,
						length: 1
					} }, signal);
					signal.throwIfAborted();
					if (!authorized.ok) return authorized;
					const metadata = await scope.remote.workspaceFiles.stat(file.sessionId, file.path, signal);
					if (metadata.ok && (authorized.value.absolutePath !== metadata.value.absolutePath || authorized.value.version !== metadata.value.version)) throw new Error(t("changed"));
					return metadata;
				}, convert, config.maxCachedEntries, config.maxCachedBytes, config.maxPending, config.maxReaders, async (signal) => {
					const result = await scope.remote.officeToPdf.generation(signal);
					signal.throwIfAborted();
					if (!result.ok) throw new Error(t("unavailable"), { cause: result.error });
					return result;
				}, () => new Error(t("busy")));
				let cache = createCache();
				const retired = /* @__PURE__ */ new Set();
				read = async (file, signal) => {
					const result = await cache.read(file, signal);
					if (!result.ok && (result.error.code === "gateway/invocation-unavailable" || result.error.code === "gateway/service-unavailable")) throw new Error(t("unavailable"), { cause: result.error });
					return result;
				};
				scope.on("connection/reset", () => {
					const previous = cache;
					cache = createCache();
					const closing = previous.dispose().finally(() => {
						retired.delete(closing);
					});
					retired.add(closing);
				});
				scope.effect(() => async () => {
					read = unavailable;
					await Promise.all([...retired, cache.dispose()]);
				});
			});
		}
		function conversionErrorKey(code) {
			switch (code) {
				case "input-too-large":
				case "output-too-large": return "tooLarge";
				case "invalid-document":
				case "unsupported-format": return "invalid";
				case "timeout": return "timeout";
				case "unavailable": return "unavailable";
				case "busy": return "busy";
				case "source-changed": return "changed";
				default: return "failed";
			}
		}
		//#endregion
		//#region src/client/excel/error.ts
		/** Locale-independent spreadsheet parser failure categories. */
		var ExcelPreviewError = class extends Error {
			code;
			/** @param code - User-actionable parser failure. */
			constructor(code, options) {
				super(code, options);
				this.code = code;
				this.name = "ExcelPreviewError";
			}
		};
		//#endregion
		//#region src/client/excel/format.ts
		/** Filename-to-parser selection kept outside the heavy spreadsheet chunk. */
		/**
		* Resolve a registered filename to its parser.
		* @param path - Decoded file path.
		* @returns The supported suffix; rejects unsupported filenames.
		*/
		function excelFormat(path) {
			const suffix = path.slice(path.lastIndexOf(".") + 1).toLowerCase();
			if (suffix === "xlsx" || suffix === "xls" || suffix === "csv" || suffix === "tsv") return suffix;
			throw new ExcelPreviewError("invalid");
		}
		//#endregion
		//#region src/client/excel/LazyExcelBody.tsx
		/** Load the spreadsheet renderer only when a supported workbook is opened. */
		const LoadedExcelBody = (0, react.lazy)(async () => ({ default: (await require.async("./client.excel.js")).ExcelBody }));
		/**
		* Load the browser spreadsheet renderer for every registered format.
		* @param props - Complete file bytes and standard document seats.
		* @returns Localized loading state or Excel preview.
		*/
		function LazyExcelBody(props) {
			const format = excelFormat(hostFileOf(props.resourceAddress).path);
			const loading = /* @__PURE__ */ (0, react_jsx_runtime.jsx)(LoadingIndicator, { label: props.t("loading") });
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(react.Suspense, {
				fallback: loading,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(LoadedExcelBody, {
					...props,
					format,
					loading
				})
			});
		}
		//#endregion
		//#region src/client/excel/locales.ts
		/** Locale-owned Excel preview controls and parser feedback. */
		const zh = {
			title: "表格",
			language: "zh",
			loading: "文档渲染中...",
			invalid: "无法打开此表格。请检查文件格式、内容或密码保护。",
			tooLarge: "此表格超过预览大小限制。",
			timeout: "打开表格超时。请缩小文件后重试。",
			encoding: "无法识别此文本文件的编码。请另存为 UTF-8 或带 BOM 的 UTF-16 后重试。",
			formulaWarning: "此工作簿包含公式，显示结果可能缺失或不准确。",
			unsupportedNotice: "当前预览不支持此工作簿中的{features}。请使用系统应用打开，以获得完整体验。",
			charts: "图表",
			images: "图片",
			shapes: "形状",
			conditionalFormatting: "条件格式",
			featureSeparator: "、",
			retry: "重试"
		};
		/** English Excel preview copy. */
		const en = {
			title: "Spreadsheet",
			language: "en",
			loading: "Rendering document...",
			invalid: "This spreadsheet could not be opened. Check its format, contents, or password protection.",
			tooLarge: "This workbook exceeds the preview size limit.",
			timeout: "Opening this workbook timed out. Try a smaller file.",
			encoding: "This text encoding could not be read. Save the file as UTF-8 or UTF-16 with a BOM and retry.",
			formulaWarning: "This workbook contains formulas. Displayed results may be missing or inaccurate.",
			unsupportedNotice: "This preview does not support {features} in this workbook. Open it in a system application for the full experience.",
			charts: "charts",
			images: "images",
			shapes: "shapes",
			conditionalFormatting: "conditional formatting",
			featureSeparator: ", ",
			retry: "Retry"
		};
		//#endregion
		//#region src/client/excel/index.ts
		/**
		* Register the browser Excel viewer and its lifecycle-owned slot.
		* @param ctx - Preview and locale registries.
		* @param limits - Resolved parser limits.
		*/
		function apply$1(ctx, limits) {
			const id = "@deepseek-ai/dsh-client-ui-sidebar-documentpreview/excel";
			ctx.effect(() => ctx.locale.register("sidebarExcel", {
				zh,
				en
			}));
			const t = ctx.locale.bind("sidebarExcel");
			ctx.effect(() => ctx.documentPreviews.register({
				id,
				extensions: [
					"xlsx",
					"xls",
					"csv",
					"tsv"
				],
				binaryExtensions: ["xlsx", "xls"],
				priority: "builtin",
				title: () => t("title"),
				loading: "bytes-complete",
				wrap: false
			}));
			ctx.effect(() => ctx.slots.inject("sidebar.right.tab.document", () => ctx.slots.register({
				name: "sidebar.right.tab.document",
				key: id,
				locale: "sidebarExcel",
				inject: () => ({ limits })
			}, LazyExcelBody)));
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
		//#region src/config.ts
		/** Cache limits shared by the Host configuration and browser document previews. */
		/** Deployment limits applied before Office preview registration. */
		const Config = Schema.object({
			office: Schema.object({
				maxCachedEntries: Schema.natural().min(1).max(Number.MAX_SAFE_INTEGER).default(8),
				maxCachedBytes: Schema.natural().min(1).max(Number.MAX_SAFE_INTEGER).default(64 * 1024 * 1024),
				maxPending: Schema.natural().min(1).max(Number.MAX_SAFE_INTEGER).default(8),
				maxReaders: Schema.natural().min(1).max(Number.MAX_SAFE_INTEGER).default(32)
			}),
			excel: Schema.object({
				maxBytes: Schema.natural().min(1).max(Number.MAX_SAFE_INTEGER).default(16 * 1024 * 1024),
				maxCells: Schema.natural().min(1).max(Number.MAX_SAFE_INTEGER).default(25e4),
				timeoutMs: Schema.natural().min(1).max(2147483647).default(15e3)
			})
		});
		//#endregion
		//#region src/client/index.ts
		/** This package's copy namespace. */
		const NS = "sidebarDocumentPreview";
		/**
		* Required browser services: the tab registry, the slot registry, copy, and the
		* workspace Remote for bytes and paged text reads.
		*/
		const inject = [
			"slots",
			"locale",
			"sidebarRightTabs",
			"remote",
			"remote.workspaceFiles",
			"configForms",
			"resources"
		];
		/**
		* Client plugin body: register the type, its dictionaries, its body, and its chip title.
		* @param ctx - client root context carrying the registry, slots, copy, and file readers.
		*/
		function apply(ctx) {
			const config = Config(globalThis.__DSH_DOCUMENT_PREVIEW_CONFIG__ ?? {});
			const previews = new DocumentPreviewRegistry();
			const disposePreviews = ctx.reflect.provide("documentPreviews", previews);
			ctx.effect(() => disposePreviews);
			ctx.effect(() => ctx.sidebarRightTabs.register(textDefinition()), "ui-sidebar-documentpreview: text type");
			ctx.effect(() => ctx.locale.register(NS, {
				zh: zh$7,
				en: en$7
			}), "ui-sidebar-documentpreview: dictionaries");
			const store = createTextStore();
			const face = textFace(createReadPage(ctx.remote), (file, signal) => ctx.remote.workspaceFiles.readBytes(file.sessionId, file.path, {}, signal), ctx.resources);
			const source = {
				getSnapshot: previews.getSnapshot,
				subscribe: previews.subscribe
			};
			ctx.effect(() => ctx.slots.inject("sidebar.right.pane.tab", () => ctx.slots.register({
				name: "sidebar.right.pane.tab",
				key: TEXTPREVIEW_ID,
				locale: NS,
				store,
				children: {
					"sidebar.right.tab.document": {
						kind: "keyed",
						scope: "session",
						inject: { hooks: { tabInfo: documentTabInfoFactory } }
					},
					"sidebar.right.tab.document.actions": {
						kind: "list",
						scope: "session"
					},
					"sidebar.right.tab.document.unpreviewable": {
						kind: "list",
						scope: "session"
					},
					"sidebar.right.tab.document.action": {
						kind: "keyed",
						scope: "session",
						inject: { hooks: { tabInfo: documentTabInfoFactory } }
					}
				},
				inject: (sessionId, actions) => ({
					...face(sessionId, actions),
					hooks: { documentPreviews: source }
				})
			}, TextPreview)), "ui-sidebar-documentpreview: text body");
			ctx.effect(() => ctx.slots.inject("sidebar.right.pane.tab.title", () => ctx.slots.register({
				name: "sidebar.right.pane.tab.title",
				key: TEXTPREVIEW_ID
			}, TextTitle)), "ui-sidebar-documentpreview: text title");
			apply$9(ctx);
			apply$8(ctx);
			apply$6(ctx);
			apply$5(ctx);
			apply$4(ctx);
			apply$2(ctx, config.office);
			apply$1(ctx, config.excel);
			apply$3(ctx);
		}
		//#endregion
		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map