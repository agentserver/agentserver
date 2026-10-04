window.__ModuleLoader__.load({
	id: "@deepseek-ai/dsh-api-terminal-controller",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		Object.defineProperty(exports, Symbol.toStringTag, { value: "Module" });
		let _deepseek_ai_cordis = require("@deepseek-ai/cordis");
		let _deepseek_ai_dsh_client_store = require("@deepseek-ai/dsh-client-store");
		let _deepseek_ai_dsh_api_gateway_client = require("@deepseek-ai/dsh-api-gateway/client");
		//#region ../../typert/protocol/src/remote-error.ts
		/**
		* One Remote call failure: a real Error carrying its stable code and typed
		* details. Owners throw it at the failure point; the Host Gateway encodes it
		* onto the wire unchanged; the Client face rebuilds an instance for the
		* `RemoteResult` error branch, so `throw result.error` keeps throw semantics.
		* Discrimination is always by `code`, never by instanceof.
		*/
		var RemoteError = class extends Error {
			/**
			* @param code - stable failure code declared in {@link RemoteErrorDetailsMap}.
			* @param message - human diagnostic carried across the wire.
			* @param details - structured payload typed by the code.
			* @param options - standard Error options (`cause` survives in-process only).
			*/
			constructor(code, message, details, options) {
				super(message, options);
				this.code = code;
				this.details = details;
				this.isDSHRemoteError = true;
				this.name = "RemoteError";
			}
		};
		/**
		* Structurally identify a RemoteError thrown across module or realm copies of
		* this class. Mechanism-internal: the Gateway and test assertions use it;
		* business code receives typed failures and never needs it.
		* @param value - a caught value.
		* @returns the failure when the marker matches, otherwise undefined.
		*/
		function remoteErrorOf(value) {
			if (typeof value === "object" && value !== null && value.isDSHRemoteError === true && typeof value.code === "string") return value;
		}
		//#endregion
		//#region ../../typert/protocol/src/index.ts
		/**
		* Remote decorators and explicit Gateway bindings backed by versioned
		* descriptors carried on decorated class prototypes. Strict reflection
		* remains a Typert compiler responsibility.
		* @module @deepseek-ai/dsh-typert-protocol
		*/
		//#endregion
		//#region src/client/shell-preference.ts
		/** Browser-local shell preference; Host discovery decides whether the saved path is usable. */
		const KEY = "dsh.terminal.shell";
		/**
		* Read the browser preference.
		* @returns the last selected shell path, or null when storage is unavailable.
		*/
		function preferredShell() {
			try {
				return typeof localStorage === "undefined" ? null : localStorage.getItem(KEY);
			} catch (_storageUnavailable) {
				return null;
			}
		}
		/**
		* Remember the selected shell without making storage a startup dependency.
		* @param path - verified executable path offered by the Host.
		*/
		function rememberShell(path) {
			try {
				if (typeof localStorage !== "undefined") localStorage.setItem(KEY, path);
			} catch (_storageUnavailable) {}
		}
		//#endregion
		//#region ../../util/crypto/src/index.ts
		/**
		* Random v4 UUID, minted from `crypto.getRandomValues`.
		* @returns the UUID string.
		*/
		function randomUUID() {
			const bytes = globalThis.crypto.getRandomValues(new Uint8Array(16));
			const hex = Array.from(bytes, (byte, index) => {
				return (index === 6 ? byte & 15 | 64 : index === 8 ? byte & 63 | 128 : byte).toString(16).padStart(2, "0");
			}).join("");
			return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
		}
		//#endregion
		//#region src/client/model.ts
		/** React-free browser terminal state and reconnecting Remote-stream ownership. */
		var TerminalViewError = class extends RemoteError {
			constructor(issue, message = issue) {
				super("terminal/view", message, { issue });
			}
		};
		/** A view survives DOM unmount; its process only ends on explicit close. */
		var TerminalView = class {
			sessionId;
			remote;
			gateway;
			id;
			createWhenMissing;
			shellPath;
			retain;
			/** Observable controls, process metadata and the next screen update awaiting acknowledgement. */
			state = (0, _deepseek_ai_dsh_client_store.createSnapshotStore)({
				phase: "idle",
				writable: false
			});
			lifetime = new AbortController();
			stream;
			mounted = false;
			attachmentId;
			pendingRender;
			revision = 0;
			creation;
			loading;
			closing;
			writes = Promise.resolve();
			queuedInput = 0;
			detaching = /* @__PURE__ */ new Set();
			/**
			* @param sessionId - Session owning the terminal.
			* @param remote - typed terminal Remote operations.
			* @param gateway - reconnecting stream factory.
			* @param id - Host terminal identity, reused when recovering an item from its Session list.
			* @param createWhenMissing - allow allocation only for a new tab, never a listed terminal.
			* @param shellPath - explicit shell chosen at the guide; omission uses the remembered available shell.
			* @param retain - window hold acknowledgement required before output attachment.
			*/
			constructor(sessionId, remote, gateway, id, createWhenMissing = true, shellPath, retain) {
				this.sessionId = sessionId;
				this.remote = remote;
				this.gateway = gateway;
				this.id = id;
				this.createWhenMissing = createWhenMissing;
				this.shellPath = shellPath;
				this.retain = retain;
			}
			/**
			* Attach the DOM lifetime, starting the chosen shell or reconnecting the saved process.
			* @returns a detach callback that leaves the terminal process alive.
			*/
			mount() {
				this.mounted = true;
				if (this.state.getSnapshot().info === void 0) this.refresh();
				else this.connect();
				return () => {
					this.mounted = false;
					this.detach();
				};
			}
			/**
			* Start or recover this tab, deduplicating mounts and retries during allocation.
			* Only a new tab may allocate a shell; listed terminals cannot be silently replaced.
			* @returns after environment lookup and creation or recovery settle.
			*/
			refresh() {
				if (this.creation !== void 0) return this.creation;
				if (this.loading !== void 0) return this.loading;
				if (this.closing !== void 0 || this.lifetime.signal.aborted) return Promise.resolve();
				this.patch({
					phase: "loading",
					error: void 0,
					issue: void 0
				});
				this.loading = (async () => {
					const [environment, available] = await Promise.all([this.remote.environment(this.sessionId, this.lifetime.signal), this.remote.list(this.sessionId)]);
					if (this.stopped()) return;
					this.patch({ environment: valueOf(environment) });
					const info = valueOf(available).find((item) => item.id === this.id);
					if (info !== void 0) this.adopt(info);
					else if (this.createWhenMissing) {
						let path = this.shellPath;
						if (path === void 0) {
							const shells = valueOf(await this.remote.shells(this.sessionId, this.lifetime.signal));
							const previous = preferredShell();
							path = shells.find((shell) => shell.path === previous)?.path ?? shells[0]?.path;
						}
						if (this.stopped()) return;
						if (path !== void 0) rememberShell(path);
						await this.create(valueOf(environment), path);
					} else throw new TerminalViewError("missingTerminal");
				})().catch((error) => {
					this.fail(error);
				}).finally(() => {
					this.loading = void 0;
				});
				return this.loading;
			}
			stopped() {
				return this.lifetime.signal.aborted || this.closing !== void 0;
			}
			async create(environment, shellPath) {
				this.patch({
					phase: "creating",
					error: void 0,
					issue: void 0
				});
				this.creation = (async () => {
					const info = valueOf(await this.remote.create(this.sessionId, {
						id: this.id,
						...shellPath === void 0 ? {} : { shellPath },
						cols: Math.min(80, environment.maxCols),
						rows: Math.min(24, environment.maxRows)
					}, this.lifetime.signal));
					if (!this.lifetime.signal.aborted) this.adopt(info);
				})().catch((error) => {
					this.fail(error);
				}).finally(() => {
					this.creation = void 0;
				});
				await this.creation;
			}
			adopt(info) {
				this.patch({
					info,
					title: info.title
				});
				if (this.retain === void 0) {
					if (this.mounted && this.closing === void 0) this.connect();
					return;
				}
				this.retain(this.lifetime.signal).then(() => {
					if (this.mounted && this.closing === void 0) this.connect();
				}).catch((error) => {
					if (!this.stopped()) this.fail(error);
				});
			}
			/** Reattach with a fresh screen and regain input control. */
			connect() {
				const info = this.state.getSnapshot().info;
				if (info === void 0 || !this.mounted || this.closing !== void 0 || this.lifetime.signal.aborted) return;
				this.detach();
				const stream = this.gateway.$stream({
					name: "Browser terminal output",
					open: async function* (signal) {
						await this.retain?.(signal);
						signal.throwIfAborted();
						const attachmentId = randomUUID();
						this.attachmentId = attachmentId;
						yield* this.remote.follow(this.sessionId, info.id, attachmentId, signal);
					}.bind(this),
					ended: () => new TerminalViewError("attachmentEnded"),
					carrierFailed: () => {
						if (this.stream === stream) this.patch({
							phase: "disconnected",
							writable: false
						});
					}
				});
				this.stream = stream;
				this.patch({
					phase: "connecting",
					writable: false,
					error: void 0,
					issue: void 0,
					render: void 0
				});
				this.consume(stream);
			}
			/**
			* Release the next stream item after xterm has parsed this frame.
			* @param revision - locally delivered render revision.
			*/
			acknowledge(revision) {
				if (this.pendingRender?.revision !== revision) return;
				this.pendingRender.resolve();
				this.pendingRender = void 0;
			}
			/**
			* Serialize raw input so concurrent RPC requests cannot reorder keystrokes.
			* @param data - input from the terminal emulator.
			*/
			write(data) {
				const state = this.state.getSnapshot();
				const attachmentId = this.attachmentId;
				if (!state.writable || state.info === void 0 || attachmentId === void 0) return;
				const bytes = new TextEncoder().encode(data).byteLength;
				if (this.queuedInput + bytes > (state.environment?.maxInputBytes ?? 0)) {
					this.fail(new TerminalViewError("inputFull"));
					return;
				}
				this.queuedInput += bytes;
				const id = state.info.id;
				this.writes = this.writes.then(async () => {
					if (this.attachmentId !== attachmentId || !this.state.getSnapshot().writable) return;
					valueOf(await this.remote.write(this.sessionId, id, attachmentId, data));
				}).catch((error) => {
					if (this.attachmentId === attachmentId) this.fail(error);
				}).finally(() => {
					this.queuedInput -= bytes;
				});
			}
			/**
			* Resize only from the currently writable view.
			* @param cols - measured column count.
			* @param rows - measured row count.
			*/
			resize(cols, rows) {
				const state = this.state.getSnapshot();
				const attachmentId = this.attachmentId;
				if (!state.writable || state.info === void 0 || attachmentId === void 0) return;
				if (state.info.cols === cols && state.info.rows === rows) return;
				const id = state.info.id;
				cols = Math.min(cols, state.environment?.maxCols ?? cols);
				rows = Math.min(rows, state.environment?.maxRows ?? rows);
				this.writes = this.writes.then(async () => {
					if (this.attachmentId !== attachmentId || !this.state.getSnapshot().writable) return;
					valueOf(await this.remote.resize(this.sessionId, id, attachmentId, cols, rows));
				}).catch((error) => {
					if (this.attachmentId === attachmentId) this.fail(error);
				});
			}
			/**
			* Update the Host terminal's display name.
			* @param title - user-entered terminal title.
			* @returns after the rename settles and its result is reflected in view state.
			*/
			async rename(title) {
				if (title.trim() === this.state.getSnapshot().title || this.lifetime.signal.aborted) return;
				try {
					valueOf(await this.remote.rename(this.sessionId, this.id, title));
					const current = this.state.getSnapshot().info;
					this.patch({
						...current === void 0 ? {} : { info: {
							...current,
							title: title.trim()
						} },
						title: title.trim()
					});
				} catch (error) {
					this.fail(error);
				}
			}
			/**
			* Explicitly terminate this view's process independently of its DOM lifetime.
			* @returns after Host process cleanup succeeds; failures remain retryable by the owner.
			*/
			close() {
				if (this.closing !== void 0) return this.closing;
				this.patch({
					phase: "closing",
					writable: false,
					error: void 0,
					issue: void 0
				});
				this.detach();
				this.closing = (async () => {
					await this.creation;
					valueOf(await this.remote.close(this.sessionId, this.id));
					this.detach();
					this.patch({
						phase: "closed",
						writable: false
					});
				})().catch((error) => {
					this.closing = void 0;
					this.fail(error);
					throw error;
				});
				return this.closing;
			}
			/**
			* Stop Client work on plugin unload without closing Host terminals.
			* @returns after active and previously detached stream iterators have closed.
			*/
			async dispose() {
				this.mounted = false;
				this.lifetime.abort();
				this.detach();
				await Promise.all(this.detaching);
			}
			detach() {
				const previous = this.stream;
				this.stream = void 0;
				this.attachmentId = void 0;
				this.pendingRender?.resolve();
				this.pendingRender = void 0;
				if (previous !== void 0) {
					const cleanup = previous.dispose().finally(() => {
						this.detaching.delete(cleanup);
					});
					this.detaching.add(cleanup);
				}
			}
			async consume(stream) {
				let generation = 0;
				let sequence = 0;
				try {
					for await (const item of stream) {
						if (this.stream !== stream) return;
						const frame = item.value;
						if (generation !== item.generation) {
							if (frame.type !== "snapshot") throw new TerminalViewError("invalidOutput", "Terminal output generation is missing its screen snapshot");
							generation = item.generation;
							sequence = frame.sequence;
							item.accept();
						} else if (frame.type === "output") {
							if (frame.sequence !== sequence + 1) throw new TerminalViewError("invalidOutput", "Terminal output sequence has a gap");
							sequence = frame.sequence;
						} else if (frame.type === "snapshot") throw new TerminalViewError("invalidOutput", "Unexpected terminal screen snapshot");
						if (frame.type !== "output") this.patch({
							info: frame.info,
							title: frame.info.title,
							phase: "connected",
							writable: frame.info.state === "running" && frame.info.controllerId === this.attachmentId
						});
						if (frame.type !== "state") {
							const revision = ++this.revision;
							await new Promise((resolve) => {
								this.pendingRender = {
									revision,
									resolve
								};
								const aborted = () => {
									this.acknowledge(revision);
								};
								item.signal.addEventListener("abort", aborted, { once: true });
								this.pendingRender.resolve = () => {
									item.signal.removeEventListener("abort", aborted);
									resolve();
								};
								this.patch({ render: {
									revision,
									frame
								} });
								if (item.signal.aborted) aborted();
							});
						}
					}
				} catch (error) {
					if (this.stream === stream) if (this.state.getSnapshot().info?.state === "exited") this.patch({
						phase: "closed",
						writable: false
					});
					else this.fail(error);
				}
			}
			patch(patch) {
				if (this.lifetime.signal.aborted) return;
				this.state.set({
					...this.state.getSnapshot(),
					...patch
				});
			}
			fail(error) {
				const failure = remoteErrorOf(error);
				if (failure?.code === "terminal/control-unavailable") {
					this.patch({
						writable: false,
						error: void 0,
						issue: void 0
					});
					return;
				}
				const issue = failure?.code === "terminal/view" ? failure.details.issue : failure?.code === "terminal/limit-reached" ? "terminalLimit" : failure?.code === "terminal/unavailable" ? "missingTerminal" : void 0;
				this.patch({
					phase: error instanceof _deepseek_ai_dsh_api_gateway_client.RemoteStreamCarrierError ? "disconnected" : "failed",
					writable: false,
					issue,
					error: error instanceof Error ? error.message : String(error)
				});
			}
		};
		function valueOf(result) {
			if (!result.ok) throw result.error;
			return result.value;
		}
		//#endregion
		//#region src/client/close-requests.ts
		const PREFIX$1 = "dsh.terminal.close.v1.";
		/** Each request has its own storage key, so other browser windows cannot overwrite its cleanup. */
		var TerminalCloseRequests = class {
			requests = /* @__PURE__ */ new Map();
			constructor() {
				try {
					if (typeof localStorage === "undefined") return;
					for (let index = 0; index < localStorage.length; index++) {
						const key = localStorage.key(index);
						if (key?.startsWith(PREFIX$1)) this.load(key);
					}
				} catch (error) {
					console.error("Terminal cleanup recovery failed:", error);
				}
			}
			/**
			* Read cleanup work still awaiting Host confirmation.
			* @returns unfinished requests owned by this browser instance.
			*/
			pending() {
				return [...this.requests.values()];
			}
			/**
			* Retain cleanup across reload before removing a tab.
			* @param request - close intent to save before removing its tab.
			*/
			save(request) {
				this.requests.set(request.id, request);
				try {
					if (typeof localStorage !== "undefined") localStorage.setItem(PREFIX$1 + request.id, JSON.stringify(request));
				} catch (error) {
					console.error("Terminal cleanup persistence failed:", error);
				}
			}
			/**
			* Forget confirmed cleanup in memory and browser storage.
			* @param id - terminal whose Host cleanup succeeded.
			*/
			remove(id) {
				this.requests.delete(id);
				try {
					if (typeof localStorage !== "undefined") localStorage.removeItem(PREFIX$1 + id);
				} catch (error) {
					console.error("Terminal cleanup persistence failed:", error);
				}
			}
			load(key) {
				try {
					const raw = localStorage.getItem(key);
					if (raw === null) return;
					const parsed = JSON.parse(raw);
					if (!isRequest(parsed) || key !== PREFIX$1 + parsed.id) throw new Error("Invalid terminal cleanup request");
					this.requests.set(parsed.id, parsed);
				} catch (error) {
					console.error("Terminal cleanup recovery failed:", error);
				}
			}
		};
		function isRequest(value) {
			if (typeof value !== "object" || value === null) return false;
			const request = value;
			return typeof request.sessionId === "string" && request.sessionId.length > 0 && typeof request.id === "string" && /^[\w-]{1,128}$/u.test(request.id) && typeof request.title === "string";
		}
		//#endregion
		//#region src/client/retention.ts
		/** A stream acknowledgement gates output attachment for each physical connection. */
		var TerminalWindowHold = class {
			stream;
			waiters = /* @__PURE__ */ new Set();
			generation;
			failure;
			/**
			* @param gateway - reconnecting stream owner.
			* @param remote - typed terminal namespace.
			* @param sessionId - saved layout's Session, without Agent activation.
			* @param id - existing Host terminal.
			*/
			constructor(gateway, remote, sessionId, id) {
				this.stream = gateway.$stream({
					name: "Browser terminal window hold",
					open: (signal) => remote.retain(sessionId, id, signal),
					ended: () => new RemoteError("terminal/unavailable", "Terminal hold ended", {})
				});
				this.consume();
			}
			/** Whether a terminal-domain failure ended this hold, allowing an explicit retry. */
			get failed() {
				return this.failure !== void 0;
			}
			/**
			* Wait for an acknowledged current physical hold before following its screen.
			* @param signal - output request or view lifetime.
			* @returns after acknowledgement, or rejects on cancellation/unavailability.
			*/
			async ready(signal) {
				signal.throwIfAborted();
				if (this.failure !== void 0) throw this.failure;
				if (this.generation !== void 0 && !this.generation.aborted) return;
				const waiting = Promise.withResolvers();
				const abort = () => {
					waiting.reject(signal.reason);
				};
				this.waiters.add(waiting);
				signal.addEventListener("abort", abort, { once: true });
				try {
					await waiting.promise;
				} finally {
					this.waiters.delete(waiting);
					signal.removeEventListener("abort", abort);
				}
			}
			/**
			* Release this window's stream and all acknowledgement waiters.
			* @returns after the stream consumer closes.
			*/
			dispose() {
				this.reject(/* @__PURE__ */ new Error("Terminal window hold released"));
				return this.stream.dispose();
			}
			async consume() {
				try {
					for await (const item of this.stream) {
						item.accept();
						this.generation = item.signal;
						for (const waiter of this.waiters) waiter.resolve();
						this.waiters.clear();
					}
				} catch (error) {
					this.reject(error);
				}
			}
			reject(error) {
				this.failure = error instanceof Error ? error : new Error("Terminal window hold failed", { cause: error });
				this.generation = void 0;
				for (const waiter of this.waiters) waiter.reject(error);
				this.waiters.clear();
			}
		};
		//#endregion
		//#region src/client/bindings.ts
		const PREFIX = "dsh.terminal.binding.v1.";
		/** Saved recovery targets keyed by Session and globally unique terminal content identity. */
		var TerminalBindings = class {
			memory = /* @__PURE__ */ new Map();
			/**
			* Read a saved target, retaining this window's value when storage is unavailable.
			* @param sessionId - owning Session.
			* @param contentId - terminal content identity, shared only by deliberate copies.
			* @returns the existing Host identity, if one has been saved.
			*/
			get(sessionId, contentId) {
				const key = this.key(sessionId, contentId);
				const known = this.memory.get(key);
				if (known !== void 0) return known;
				if (typeof localStorage === "undefined") return void 0;
				try {
					const raw = localStorage.getItem(key);
					if (raw === null) return void 0;
					const value = JSON.parse(raw);
					if (typeof value !== "string" || !/^[\w-]{1,128}$/u.test(value)) return void 0;
					this.memory.set(key, value);
					return value;
				} catch (_storageUnavailable) {
					return;
				}
			}
			/**
			* Save an identity before its Host allocation begins.
			* @param sessionId - owning Session.
			* @param contentId - globally unique terminal content identity.
			* @param id - existing or newly allocated Host identity.
			*/
			set(sessionId, contentId, id) {
				const key = this.key(sessionId, contentId);
				this.memory.set(key, id);
				if (typeof localStorage === "undefined") return;
				try {
					localStorage.setItem(key, JSON.stringify(id));
				} catch (error) {
					console.error("Terminal binding persistence failed:", error);
				}
			}
			/**
			* Remove this content's target after its close intent has been saved.
			* @param sessionId - owning Session.
			* @param contentId - closing terminal content identity.
			*/
			delete(sessionId, contentId) {
				const key = this.key(sessionId, contentId);
				this.memory.delete(key);
				if (typeof localStorage === "undefined") return;
				try {
					localStorage.removeItem(key);
				} catch (error) {
					console.error("Terminal binding cleanup failed:", error);
				}
			}
			/** Release cached values when the Client service is disposed. */
			clear() {
				this.memory.clear();
			}
			key(sessionId, contentId) {
				return PREFIX + JSON.stringify([sessionId, contentId]);
			}
		};
		//#endregion
		//#region src/client/index.ts
		/** Client terminal model service; views are keyed independently from Host terminal identities. */
		/** Session and occurrence lookup, independent tab and terminal identities and background cleanup. */
		var ClientTerminals = class extends _deepseek_ai_cordis.Service {
			remote;
			/** Failed cleanup tasks; successful and in-progress closes have no visible notification. */
			closeFailures = (0, _deepseek_ai_dsh_client_store.createSnapshotStore)([]);
			requests = new TerminalCloseRequests();
			closing = /* @__PURE__ */ new Map();
			closed = new Set(this.requests.pending().map((request) => request.id));
			disposed = false;
			views = /* @__PURE__ */ new Map();
			bindings = new TerminalBindings();
			holds = /* @__PURE__ */ new Map();
			releasing = /* @__PURE__ */ new Set();
			openTabs = [];
			/**
			* @param ctx - Client root Context with Gateway and terminal Remote namespace.
			* @param remote - generated terminal namespace.
			*/
			constructor(ctx, remote) {
				super(ctx, "webTerminals");
				this.remote = remote;
				ctx.effect(() => async () => {
					this.disposed = true;
					const detaching = [...this.views.values()].flatMap((views) => [...views.values()].map((view) => view.dispose()));
					this.views.clear();
					this.bindings.clear();
					const holds = [...this.holds.values()].flatMap((holds) => [...holds.values()].map((hold) => hold.dispose()));
					this.holds.clear();
					await Promise.all([
						...detaching,
						...holds,
						...this.releasing,
						...this.closing.values()
					]);
				}, "terminal-controller.client.views");
				for (const request of this.requests.pending()) this.cleanup(request);
			}
			/**
			* Return the stable model for one sidebar occurrence.
			* @param sessionId - owning Session.
			* @param key - sidebar occurrence key.
			* @param contentId - globally unique content identity; layout-local tab ids are not persistence keys.
			* @param terminalId - existing Host identity when restoring a listed terminal; otherwise reuse the saved content identity.
			* @param shellPath - explicit shell for a new terminal; restored terminals retain their own shell.
			* @returns its observable state and terminal commands.
			*/
			view(sessionId, key, contentId, terminalId, shellPath) {
				let views = this.views.get(sessionId);
				if (views === void 0) {
					views = /* @__PURE__ */ new Map();
					this.views.set(sessionId, views);
				}
				let view = views.get(key);
				if (view === void 0) {
					const saved = terminalId ?? this.bindings.get(sessionId, contentId);
					const id = saved ?? randomUUID();
					this.bindings.set(sessionId, contentId, id);
					view = new TerminalView(sessionId, this.remote, this.ctx.remote, id, saved === void 0, shellPath, (signal) => this.hold(sessionId, id).ready(signal));
					views.set(key, view);
					this.reconcileHolds();
					view.refresh();
				}
				return view;
			}
			/**
			* Discover available launch choices on demand without allocating a PTY.
			* @param sessionId - target Session.
			* @param signal - the menu request lifetime.
			* @returns installed shells and the currently usable browser preference.
			*/
			async launchShells(sessionId, signal) {
				const result = await this.remote.shells(sessionId, signal);
				if (!result.ok) throw new Error(result.error.message);
				const previous = preferredShell();
				return {
					shells: result.value,
					selectedShell: result.value.find((shell) => shell.path === previous)?.path ?? result.value[0]?.path
				};
			}
			/**
			* Remember the guide selection before allocating its terminal tab.
			* @param path - shell selected from Host discovery.
			*/
			selectShell(path) {
				rememberShell(path);
			}
			/**
			* Save a close intent and release the tab immediately; cleanup outlives DOM unmount and reload.
			* @param sessionId - owning Session.
			* @param key - sidebar occurrence key, including an inactive restored tab.
			* @param contentId - globally unique content identity whose binding is removed.
			* @param terminalId - restored identity if the tab has no model yet.
			*/
			close(sessionId, key, contentId, terminalId) {
				const views = this.views.get(sessionId);
				const view = views?.get(key);
				const id = view?.id ?? terminalId ?? this.bindings.get(sessionId, contentId);
				if (id === void 0) return;
				const request = {
					sessionId,
					id,
					title: view?.state.getSnapshot().title ?? key
				};
				this.closed.add(id);
				this.requests.save(request);
				this.bindings.delete(sessionId, contentId);
				views?.delete(key);
				if (views?.size === 0) this.views.delete(sessionId);
				this.cleanup(request, view);
				this.reconcileHolds();
			}
			/**
			* Reconcile this window's open terminal occurrences, including dormant saved Sessions.
			* @param tabs - terminal-kind membership supplied by the sidebar layout owner.
			*/
			retainTabs(tabs) {
				this.openTabs = tabs;
				this.reconcileHolds();
			}
			hold(sessionId, id) {
				let holds = this.holds.get(sessionId);
				if (holds === void 0) {
					holds = /* @__PURE__ */ new Map();
					this.holds.set(sessionId, holds);
				}
				let hold = holds.get(id);
				if (hold === void 0 || hold.failed) {
					if (hold !== void 0) this.release(hold);
					hold = new TerminalWindowHold(this.ctx.remote, this.remote, sessionId, id);
					holds.set(id, hold);
				}
				return hold;
			}
			reconcileHolds() {
				if (this.disposed) return;
				const wanted = /* @__PURE__ */ new Map();
				for (const tab of this.openTabs) {
					const id = this.bindings.get(tab.sessionId, tab.contentId);
					if (id === void 0 || this.closed.has(id)) continue;
					let ids = wanted.get(tab.sessionId);
					if (ids === void 0) {
						ids = /* @__PURE__ */ new Set();
						wanted.set(tab.sessionId, ids);
					}
					ids.add(id);
					const view = this.views.get(tab.sessionId)?.get(tab.tabId);
					if (view === void 0 || view.state.getSnapshot().info !== void 0) {
						if (!this.holds.get(tab.sessionId)?.has(id)) this.hold(tab.sessionId, id);
					}
				}
				for (const [sessionId, holds] of this.holds) {
					for (const [id, hold] of holds) if (!wanted.get(sessionId)?.has(id)) {
						holds.delete(id);
						this.release(hold);
					}
					if (holds.size === 0) this.holds.delete(sessionId);
				}
			}
			release(hold) {
				const releasing = hold.dispose().finally(() => {
					this.releasing.delete(releasing);
				});
				this.releasing.add(releasing);
				releasing.catch((error) => {
					this.ctx.logger.warn("Terminal hold release failed", error);
				});
			}
			/**
			* Query Host terminals without a live view or unfinished close.
			* @param sessionId - Session being displayed.
			* @returns terminals available for opening as recovered tabs.
			*/
			async recover(sessionId) {
				const result = await this.remote.list(sessionId);
				if (!result.ok) throw new Error(result.error.message);
				const held = new Set([...this.views.get(sessionId)?.values() ?? []].map((view) => view.id));
				const closing = new Set(this.requests.pending().map((request) => request.id));
				return result.value.filter((info) => !held.has(info.id) && !closing.has(info.id) && !this.closed.has(info.id));
			}
			/**
			* Retry a saved close request without reopening its tab.
			* @param id - failed terminal identity.
			*/
			retryClose(id) {
				const record = this.requests.pending().find((item) => item.id === id);
				if (record !== void 0) this.cleanup(record);
			}
			cleanup(record, view) {
				if (this.closing.has(record.id) || this.disposed) return;
				this.closeFailures.set(this.closeFailures.getSnapshot().filter((failure) => failure.id !== record.id));
				const pending = (async () => {
					if (view !== void 0) await view.close();
					else {
						const result = await this.remote.close(record.sessionId, record.id);
						if (!result.ok) throw result.error;
					}
					this.requests.remove(record.id);
				})().catch((error) => {
					if (remoteErrorOf(error)?.code === "session/not-found") {
						this.requests.remove(record.id);
						return;
					}
					if (!this.disposed) this.closeFailures.set([...this.closeFailures.getSnapshot(), {
						id: record.id,
						title: record.title,
						message: error instanceof Error ? error.message : String(error)
					}]);
				}).then(async () => {
					await view?.dispose();
					this.closing.delete(record.id);
				});
				this.closing.set(record.id, pending);
			}
		};
		/** Required Client transport and terminal namespace. */
		const inject = ["remote", "remote.terminal"];
		/**
		* Install the Client terminal models.
		* @param ctx - Client root Context.
		*/
		function apply(ctx) {
			new ClientTerminals(ctx, ctx.remote.terminal);
		}
		//#endregion
		exports.ClientTerminals = ClientTerminals;
		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map