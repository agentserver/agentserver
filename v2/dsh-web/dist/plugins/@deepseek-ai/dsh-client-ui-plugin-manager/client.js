window.__ModuleLoader__.load({
	id: "@deepseek-ai/dsh-client-ui-plugin-manager",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		Object.defineProperty(exports, Symbol.toStringTag, { value: "Module" });
		let react = require("react");
		let react_dom = require("react-dom");
		let _deepseek_ai_dsh_client_ui_primitives = require("@deepseek-ai/dsh-client-ui-primitives");
		let _deepseek_ai_dsh_client_ui_slots = require("@deepseek-ai/dsh-client-ui-slots");
		let _deepseek_ai_dsh_client_store = require("@deepseek-ai/dsh-client-store");
		let react_jsx_runtime = require("react/jsx-runtime");
		//#region src/client/config-ledger.ts
		/**
		* The key a row's configuration registers under.
		* @param bundle - the bundle's package name.
		* @param rowId - the row id the bundle's patch declares.
		* @returns the `plugins.row.config` key.
		*/
		function rowConfigKey(bundle, rowId) {
			return `${bundle}#${rowId}`;
		}
		const SLOTS = [
			"plugins.item",
			"plugins.bundle.config",
			"plugins.row.config"
		];
		/**
		* Project the configuration ledgers as one observable the page binds.
		* @param ctx - the page plugin's context, whose slot registry and locale the projection follows.
		* @returns the ledger source; its snapshot changes only when a ledger or the locale does.
		*/
		function configLedgerSource(ctx) {
			let versions = [];
			let revision = -1;
			let ledger = {
				items: [],
				bundles: /* @__PURE__ */ new Set(),
				rows: /* @__PURE__ */ new Set()
			};
			const keysOf = (name) => new Set(ctx.slots.entries(name).flatMap((entry) => entry.options.key === void 0 ? [] : [entry.options.key]));
			return {
				getSnapshot: () => {
					const next = SLOTS.map((name) => ctx.slots.getVersion(name));
					const current = ctx.locale.getSnapshot().revision;
					if (current !== revision || next.some((version, index) => version !== versions[index])) {
						versions = next;
						revision = current;
						ledger = {
							items: ctx.slots.entries("plugins.item").map((entry) => ({
								/* v8 ignore next -- list-slot registration requires id */
								id: entry.options.id ?? "",
								label: (0, _deepseek_ai_dsh_client_ui_slots.resolveSlotLabel)(entry.options.label) ?? ""
							})),
							bundles: keysOf("plugins.bundle.config"),
							rows: keysOf("plugins.row.config")
						};
					}
					return ledger;
				},
				subscribe: (listener) => {
					const offs = [...SLOTS.map((name) => ctx.slots.subscribe(name, listener)), ctx.locale.subscribe(listener)];
					return () => {
						for (const off of offs) off();
					};
				}
			};
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
		/** Public npmmirror URL shared by the fallback configuration and public-registry comparison. */
		const NPMMIRROR_REGISTRY = "https://registry.npmmirror.com/";
		/** An http(s) URL, as pnpm's `--registry` takes it. */
		const REGISTRY_URL = /^https?:\/\/\S+$/;
		/**
		* Parse a registry URL into the form pnpm compares registries in: lower-case host, trailing slash.
		* @param url - the registry as configured or requested.
		* @returns the normalized URL.
		* @throws {Error} for anything but an http(s) URL.
		*/
		function normalizeRegistry(url) {
			let parsed;
			try {
				parsed = new URL(url);
			} catch {}
			if (parsed === void 0 || parsed.protocol !== "http:" && parsed.protocol !== "https:") throw new Error(`a registry must be an http(s) URL: ${url}`);
			if (!parsed.pathname.endsWith("/")) parsed.pathname += "/";
			return parsed.href;
		}
		//#endregion
		//#region src/client/presentation.ts
		/** The registries with a name of their own, by host. */
		const REGISTRY_COPY = new Map([["registry.npmmirror.com", "registryNpmmirror"]]);
		/** npm's own registry, which reads by name rather than by host. */
		const OFFICIAL_NPM_HOST = "registry.npmjs.org";
		/**
		* What a registry reads as: npm's own by its name, a known mirror by its name, any other registry by its host;
		* and the host each names, for where the name alone would leave it unsaid. The registry pnpm's own configuration
		* names reads by the registry it names, so the label never claims npm's own for another one.
		* @param registry - the registry, null for the one pnpm's own configuration names.
		* @param t - the manager's translate seat.
		* @param resolved - the URL pnpm's own configuration names, null while the Host could not read it.
		* @returns the name and the host.
		*/
		function registryText(registry, t, resolved) {
			const url = registry ?? resolved;
			if (url === null) return {
				name: t("registryDefault"),
				host: OFFICIAL_NPM_HOST
			};
			const host = registryHost(url);
			const key = host === OFFICIAL_NPM_HOST ? "registryOfficial" : REGISTRY_COPY.get(host);
			return {
				name: key === void 0 ? host : t(key),
				host
			};
		}
		/** The host of a registry URL; the URL as written when it does not parse. */
		function registryHost(registry) {
			try {
				return new URL(registry).host;
			} catch {
				return registry;
			}
		}
		/** The sentence each of the Host's refusal codes reads as. */
		const CODE_KEYS = {
			"management-required": "reasonManagementRequired",
			"unaddressable": "reasonUnaddressable",
			"unknown-plugin": "reasonUnknownPlugin",
			"invalid-spec": "reasonInvalidSpec",
			"ambiguous-install": "reasonAmbiguousInstall",
			"not-bundle": "reasonNotBundle",
			"not-removable": "reasonNotRemovable",
			"stop-profile": "reasonStopProfile",
			"bundle-in-use": "reasonBundleInUse",
			"stale-approval": "reasonStaleApproval",
			"incompatible-version": "reasonIncompatibleVersionUnnamed",
			"operation-error": "reasonOperationError"
		};
		/** The sentence a failed action opens with, by what was being done. */
		const FAILED_KEYS = {
			enable: "failedEnable",
			disable: "failedDisable",
			uninstall: "failedUninstall",
			rowEnable: "failedRowEnable",
			rowDisable: "failedRowDisable"
		};
		/**
		* What a management error reads as: the code's sentence, one sentence per
		* package an incompatibility names, or, for an operation error, the Host's diagnostic as it is.
		* @param error - the Host's code, its diagnostic, and the packages an incompatibility names.
		* @param t - the manager's translate seat.
		* @returns the sentence.
		*/
		function managementText(error, t) {
			if (error.code === "incompatible-version" && error.incompatible !== void 0 && error.incompatible.length > 0) return error.incompatible.map((plugin) => t("reasonIncompatibleVersion", {
				plugin: `${plugin.name}@${plugin.version}`,
				runtime: plugin.runtimeVersion,
				peers: Object.entries(plugin.peers).map(([name, range]) => `${name} ${range}`).join(", ")
			})).join(" ");
			if (error.code !== "operation-error") return t(CODE_KEYS[error.code]);
			return error.diagnostic === void 0 || error.diagnostic === "" ? t("reasonOperationError") : error.diagnostic;
		}
		/**
		* Compact a package name to what a person calls it.
		* @param name - the package name.
		* @returns the unscoped name without the harness prefixes.
		*/
		function shortName(name) {
			return (name.startsWith("@") ? name.slice(name.indexOf("/") + 1) : name).replace(/^dsh-(?:host-|client-)?/, "");
		}
		/**
		* Resolve installed package metadata without changing its technical identity.
		* @param pkg - package identity and local metadata.
		* @param resolveText - current-locale package text resolver.
		* @returns localized copy with a technical-name fallback and the independent beta status.
		*/
		function packageText(pkg, resolveText) {
			return {
				title: pkg.meta?.title === void 0 ? pkg.name : resolveText(pkg.meta.title),
				description: pkg.meta?.description === void 0 ? void 0 : resolveText(pkg.meta.description) || void 0,
				beta: pkg.name.startsWith("@deepseek-ai/dsh-experimental-")
			};
		}
		/**
		* Resolve a bundle row's plugin metadata, using its full module specifier as the final title fallback.
		* @param row - row identity and local metadata.
		* @param resolveText - current-locale package text resolver.
		* @returns the row's display title and optional description.
		*/
		function rowText(row, resolveText) {
			return {
				title: row.meta?.title === void 0 ? row.moduleName : resolveText(row.meta.title),
				description: row.meta?.description === void 0 ? void 0 : resolveText(row.meta.description) || void 0
			};
		}
		/**
		* The sentence one notice shows.
		* @param notice - the last action's outcome.
		* @param t - the manager's translate seat.
		* @returns the sentence.
		*/
		function noticeText(notice, t) {
			switch (notice.kind) {
				case "restart": return t("restartNotice");
				case "overridden": return t("overriddenNotice", { name: notice.packageName });
				case "cancelled": return t("installCancelled");
				case "install": return t({
					done: "installBackgroundDone",
					failed: "installBackgroundFailed",
					unconfirmed: "installBackgroundUnconfirmed",
					applying: "installBackgroundApplying",
					unknown: "installBackgroundUnknown"
				}[notice.outcome]);
				case "failed": {
					const reason = notice.code === void 0 ? notice.reason : managementText({
						code: notice.code,
						diagnostic: notice.reason,
						...notice.incompatible === void 0 ? {} : { incompatible: notice.incompatible }
					}, t);
					return t(FAILED_KEYS[notice.action], { reason: reason === "" ? t("reasonOperationError") : reason });
				}
			}
		}
		//#endregion
		//#region src/client/manager-store.ts
		/**
		* The plugin manager's state: the Host's bundles joined with its plugin
		* entries, the action in flight, the install run, and the confirmation an
		* uninstall waits on. Every fact comes from the Host — the store re-reads
		* after each action and after every `plugin-manager/changed` event, so a
		* change made on another surface shows here without a manual refresh.
		*/
		/** The choice shown until the Host has said which registry it asks first: the one pnpm's own configuration names. */
		const OFFICIAL_REGISTRY = {
			kind: "offered",
			registry: null
		};
		/**
		* The registry a choice asks, as the Host's install plan compares registries: pnpm's own configuration stands for
		* the URL it names, once the Host has read it.
		* @param registry - the registry, null for the one pnpm's own configuration names.
		* @param resolved - the URL pnpm's own configuration names, null while the Host could not read it.
		* @returns the comparison key; a registry that does not parse compares as written.
		*/
		function registryKey(registry, resolved) {
			const url = registry ?? resolved;
			if (url === null) return "";
			try {
				return normalizeRegistry(url);
			} catch {
				return url;
			}
		}
		/**
		* The registries the dialog offers: the Host's first, its fallbacks, and pnpm's own, each once. pnpm's own
		* configuration stands for the registry it names, so it never repeats a registry the Host already offers.
		* @param registries - what the Host configured, or null while unread.
		* @returns the registries in the order the dialog lists them.
		*/
		function offeredRegistries(registries) {
			const offered = [];
			const keys = [];
			for (const registry of [...registries === null ? [] : [registries.registry, ...registries.fallbackRegistries], null]) {
				const key = registryKey(registry, registries?.resolved ?? null);
				if (keys.includes(key)) continue;
				offered.push(registry);
				keys.push(key);
			}
			return offered;
		}
		/**
		* Whether an installation is still owned by the Host.
		* @param phase - the dialog's current installation phase.
		* @returns true while a Host result or a check for an active request is outstanding.
		*/
		function isInstallPending(phase) {
			return phase === "starting" || phase === "running" || phase === "cancelling" || phase === "applying" || phase === "unconfirmed";
		}
		/**
		* Offer the configured mainland mirror after a confirmed GitHub connection failure.
		* @param install - the installation and the Host's failure attribution.
		* @returns the offered entry that asks npmmirror, null when that entry is pnpm's own configuration, or undefined when
		* this recovery does not apply; test for undefined, because null is a valid entry.
		*/
		function githubRecoveryRegistry(install) {
			if (install.phase !== "failed" || install.failure?.failedAt !== "spec-host" || install.failure.kind !== "network" && install.failure.kind !== "timeout") return void 0;
			const host = install.subject?.host?.toLowerCase().split(":")[0];
			if (host !== "github.com" && !host?.endsWith(".github.com")) return void 0;
			const resolved = install.registries?.resolved ?? null;
			return offeredRegistries(install.registries).find((registry) => registryKey(registry, resolved) === NPMMIRROR_REGISTRY);
		}
		/**
		* Whether the install already asks npmmirror first, so switching to the offered mirror would not change the registry.
		* @param install - the installation and its registry choice.
		* @returns true when the chosen registry, offered or typed, compares as npmmirror.
		*/
		function asksMirror(install) {
			const choice = install.registry;
			return registryKey(choice.kind === "custom" ? choice.url.trim() : choice.registry, install.registries?.resolved ?? null) === NPMMIRROR_REGISTRY;
		}
		/** A refused answer or a change the Host could not apply, carrying what it said and, for a refusal, its code. */
		var RemoteAnswerError = class extends Error {
			reason;
			code;
			incompatible;
			constructor(reason, code, incompatible) {
				super(reason);
				this.reason = reason;
				this.code = code;
				this.incompatible = incompatible;
				this.name = "RemoteAnswerError";
			}
		};
		/** The dialog's reading of a failed change: the Host's code and diagnostic, and the run's classified failure. */
		function failureOf(error, kind, pendingBuilds, failedAt) {
			return {
				reason: error?.diagnostic ?? "",
				...error === void 0 ? {} : { code: error.code },
				...error?.incompatible === void 0 ? {} : { incompatible: error.incompatible },
				...kind === void 0 ? {} : { kind },
				...failedAt === void 0 ? {} : { failedAt },
				...pendingBuilds === void 0 || pendingBuilds.length === 0 ? {} : { pendingBuilds }
			};
		}
		/** The notice a thrown failure becomes: a refusal keeps its code, anything else its words. */
		function failedNotice(error, subject, seq) {
			const code = error instanceof RemoteAnswerError ? error.code : void 0;
			const incompatible = error instanceof RemoteAnswerError ? error.incompatible : void 0;
			return {
				kind: "failed",
				reason: reasonOf(error),
				...code === void 0 ? {} : { code },
				...incompatible === void 0 ? {} : { incompatible },
				...subject,
				seq
			};
		}
		/** The runs with every one still open settled at `exitCode`. */
		function settledRuns(runs, exitCode) {
			return runs.map((run) => run.exitCode === void 0 ? {
				...run,
				exitCode
			} : run);
		}
		/**
		* The key one row occupies in the busy list.
		* @param entryId - the row's Loader entry id.
		* @returns the busy key.
		*/
		function rowKey(entryId) {
			return `row:${entryId}`;
		}
		/**
		* One bundle as the page shows it: its rows joined with the Host's entries.
		* @param bundle - the Host's bundle.
		* @param plugins - the Host's plugin entries.
		* @returns the package view.
		*/
		function packageView(bundle, plugins) {
			const rows = bundle.rows.map((row) => {
				const live = row.entryId === void 0 ? void 0 : plugins.find((plugin) => plugin.entryId === row.entryId);
				return {
					rowId: row.rowId,
					moduleName: row.moduleName,
					enabled: live?.enabled ?? false,
					phase: live?.fiberPhase ?? null,
					...row.meta === void 0 ? {} : { meta: row.meta },
					...row.entryId === void 0 ? {} : { entryId: row.entryId },
					...live?.readOnlyReason === void 0 ? {} : { readOnlyReason: live.readOnlyReason }
				};
			});
			return {
				name: bundle.name,
				installed: bundle.installed,
				optional: bundle.optional,
				enabled: bundle.enabled,
				rows,
				...bundle.version === void 0 ? {} : { version: bundle.version },
				...bundle.description === void 0 ? {} : { description: bundle.description },
				...bundle.meta === void 0 ? {} : { meta: bundle.meta },
				...bundle.readOnlyReason === void 0 ? {} : { readOnlyReason: bundle.readOnlyReason },
				...bundle.error === void 0 ? {} : { error: bundle.error }
			};
		}
		/**
		* The order the list shows packages in: by the short name a person reads, so a
		* card stays put when its bundle is switched, whatever order the Host answers in.
		* @param packages - the Host's bundles as views.
		* @returns the views sorted by short name.
		*/
		function sortPackages(packages) {
			return [...packages].sort((a, b) => shortName(a.name).localeCompare(shortName(b.name)));
		}
		const IDLE_INSTALL = {
			open: false,
			spec: "",
			registries: null,
			registry: OFFICIAL_REGISTRY,
			registryOpen: false,
			registryError: false,
			attempts: null,
			phase: "idle",
			inputError: null,
			subject: null,
			runs: [],
			detailsOpen: false,
			installed: null,
			restartRequired: false,
			failure: null,
			approvedBuilds: [],
			enabling: false
		};
		/** The dialog back at its spec: the run and its outcome forgotten, the spec and the registries kept. */
		function specAgain(install) {
			const { open, spec, registries, registry, mirrorRecovery } = install;
			return {
				...IDLE_INSTALL,
				open,
				spec,
				registries,
				registry,
				...mirrorRecovery === void 0 ? {} : { mirrorRecovery }
			};
		}
		/**
		* The choice as the dialog can show it once the Host has answered: nothing remembered starts from the registry the
		* Host asks first; a remembered registry the Host now asks under another entry takes that entry, and one it no
		* longer offers is kept as a typed one.
		*/
		function reconciled(remembered, registries) {
			if (remembered === null) return {
				kind: "offered",
				registry: registries.registry
			};
			if (remembered.kind === "custom" || remembered.registry === null) return remembered;
			const key = registryKey(remembered.registry, registries.resolved);
			const offered = offeredRegistries(registries).find((registry) => registryKey(registry, registries.resolved) === key);
			return offered === void 0 ? {
				kind: "custom",
				url: remembered.registry
			} : {
				kind: "offered",
				registry: offered
			};
		}
		/** Only the shipped public mirror can replace an unconfigured official npm default. */
		function eligibleMirror(registries) {
			if (registries.registry !== null || registries.resolved === null) return void 0;
			let resolved;
			try {
				resolved = normalizeRegistry(registries.resolved);
			} catch (_error) {
				return;
			}
			if (resolved !== "https://registry.npmjs.org/") return void 0;
			return registries.fallbackRegistries.find((registry) => registry === NPMMIRROR_REGISTRY);
		}
		/** Reads and mutates the profile's plugins through the `pluginManager` Remote. */
		var PluginManagerController = class {
			ctx;
			store;
			inFlight;
			rerun = false;
			generation = 0;
			disposed = false;
			pendingConfirm;
			/** Cancels the check the dialog has in flight. */
			inspectAbort;
			request;
			noticeSeq = 0;
			registryRead;
			/** The registry last used from this browser, kept across dialogs and page loads; null until one was used. */
			registryMemory = (0, _deepseek_ai_dsh_client_store.createSnapshotStore)(null, { persist: { name: "dsh.plugin-manager.install-registry" } });
			/**
			* @param ctx - the tab plugin's context, whose `remote.pluginManager` and `remote.pluginInventory` namespaces answer.
			*/
			constructor(ctx) {
				this.ctx = ctx;
				this.store = (0, _deepseek_ai_dsh_client_store.createSnapshotStore)({
					status: "idle",
					packages: [],
					busy: [],
					notice: null,
					install: IDLE_INSTALL,
					confirm: null,
					highlight: null
				});
			}
			/**
			* Read the tab's state.
			* @returns the current sync snapshot (stable reference until the next change).
			*/
			getSnapshot() {
				return this.store.getSnapshot();
			}
			/** Stop publishing and drop every late settlement. */
			dispose() {
				this.disposed = true;
				this.registryRead = void 0;
				this.request = void 0;
				this.generation += 1;
			}
			/**
			* Build the face the tab's slot registration injects.
			* @param configLedger - the projection of the plugins carrying configuration, bound beside the tab's own state.
			* @param resolveText - render-time package text resolution supplied by the locale service.
			* @returns the tab's snapshot sources and its actions.
			*/
			inject(configLedger, resolveText) {
				return {
					resolveText,
					hooks: {
						pluginManager: this.store,
						configLedger,
						configurations: this.ctx.configForms.describe()
					},
					configForm: (id) => this.ctx.configForms.get(id),
					ensure: () => {
						if (this.getSnapshot().status === "idle") this.load();
					},
					refresh: () => {
						this.load();
					},
					openInstall: () => {
						if (this.getSnapshot().install.requestId === void 0) {
							this.patch({ install: {
								...IDLE_INSTALL,
								open: true,
								registry: this.registryMemory.getSnapshot() ?? OFFICIAL_REGISTRY
							} });
							const read = { choice: this.getSnapshot().install.registry };
							this.registryRead = read;
							read.done = this.readRegistries(read).finally(() => {
								delete read.done;
							});
						} else this.patchInstall({ open: true });
						this.reconcileInstall();
					},
					closeInstall: () => {
						if (isInstallPending(this.getSnapshot().install.phase)) {
							this.patchInstall({ open: false });
							this.cancelInstall();
							return;
						}
						this.abortInspect();
						this.registryRead = void 0;
						this.patch({ install: IDLE_INSTALL });
					},
					editInstallSpec: (text) => {
						const install = this.getSnapshot().install;
						if (install.phase === "checking" || isInstallPending(install.phase)) return;
						this.patchInstall(install.phase === "idle" ? {
							spec: text,
							inputError: null
						} : {
							...specAgain(install),
							spec: text
						});
					},
					runInstall: () => {
						this.runInstall();
					},
					toggleRegistryOptions: () => {
						this.patchInstall({ registryOpen: !this.getSnapshot().install.registryOpen });
					},
					chooseRegistry: (choice) => {
						if (this.getSnapshot().install.phase === "idle") this.patchInstall({
							registry: choice,
							registryError: false
						});
					},
					changeRegistry: () => {
						const install = this.getSnapshot().install;
						if (install.phase === "failed") this.patch({ install: {
							...specAgain(install),
							registryOpen: true
						} });
					},
					useGithubMirror: () => {
						const install = this.getSnapshot().install;
						const mirror = githubRecoveryRegistry(install);
						if (mirror === void 0) return;
						const recovered = {
							...specAgain(install),
							spec: "",
							mirrorRecovery: true
						};
						if (asksMirror(install)) {
							this.patch({ install: recovered });
							return;
						}
						const registry = {
							kind: "offered",
							registry: mirror
						};
						this.registryMemory.set(registry);
						this.patch({ install: {
							...recovered,
							registry
						} });
					},
					approveBuildsAndRetry: () => {
						this.approveBuildsAndRetry();
					},
					cancelInstall: () => {
						this.cancelInstall();
					},
					reconcileInstall: () => {
						this.reconcileInstall();
					},
					toggleInstallDetails: () => {
						this.patchInstall({ detailsOpen: !this.getSnapshot().install.detailsOpen });
					},
					enableInstalled: () => {
						this.enableInstalled();
					},
					clearHighlight: () => {
						if (this.getSnapshot().highlight !== null) this.patch({ highlight: null });
					},
					setEnabled: (packageName, enabled) => {
						this.run(packageName, {
							packageName,
							action: enabled ? "enable" : "disable"
						}, async () => {
							this.applied(await this.ctx.remote.pluginManager.setBundleEnabled(packageName, enabled), packageName);
						});
					},
					uninstall: (packageName) => {
						this.pendingConfirm = () => this.run(packageName, {
							packageName,
							action: "uninstall"
						}, async () => {
							this.applied(await this.ctx.remote.pluginManager.removeBundle(packageName), packageName);
						});
						this.patch({ confirm: {
							action: "uninstall",
							packageName
						} });
					},
					confirm: () => {
						this.confirm();
					},
					cancelConfirm: () => {
						this.pendingConfirm = void 0;
						this.patch({ confirm: null });
					},
					setRowEnabled: (entryId, enabled) => {
						this.run(rowKey(entryId), {
							packageName: entryId,
							action: enabled ? "rowEnable" : "rowDisable"
						}, async () => {
							this.applied(await this.ctx.remote.pluginManager.setPluginEnabled(entryId, enabled), entryId);
						});
					},
					dismissNotice: () => {
						this.patch({ notice: null });
					}
				};
			}
			/**
			* Follow the Host's cancellation window for this dialog's installation.
			* @param progress - a request id and phase received from the Host.
			*/
			installProgress(progress) {
				const install = this.getSnapshot().install;
				if (install.requestId !== progress.requestId || !isInstallPending(install.phase)) return;
				const request = this.request;
				if (request === void 0) return;
				request.acknowledged = true;
				if (install.phase === "applying") return;
				if (progress.phase === "installing" && request.cancellation !== void 0) {
					if (request.cancellation.waitingForStart) this.sendCancellation(request, request.cancellation);
					return;
				}
				const attempt = progress.attempt;
				this.patchInstall({
					phase: progress.phase === "installing" ? "running" : progress.phase,
					...attempt === void 0 ? {} : { attempts: {
						registries: [...install.attempts?.registries ?? [], attempt.registry],
						total: attempt.total
					} }
				});
			}
			currentRegistryRead(read) {
				return !this.disposed && this.registryRead === read;
			}
			/** Read the registries the Host offers, for the dialog just opened; a refused read leaves pnpm's own and a typed one. */
			async readRegistries(read) {
				const answer = await this.ctx.remote.pluginManager.registries();
				const install = this.getSnapshot().install;
				if (!this.currentRegistryRead(read) || !install.open || !answer.ok) return;
				const remembered = this.registryMemory.getSnapshot();
				const untouched = install.registry === read.choice && (install.phase === "idle" || install.phase === "checking");
				if (untouched) read.choice = reconciled(remembered, answer.value);
				this.patchInstall({
					registries: answer.value,
					...untouched ? { registry: read.choice } : {}
				});
				const mirror = eligibleMirror(answer.value);
				if (!untouched || remembered !== null || mirror === void 0) return;
				const fastest = await this.ctx.remote.pluginRegistryProbe.fastest();
				const current = this.getSnapshot().install;
				if (!this.currentRegistryRead(read) || !current.open || current.phase !== "idle" && current.phase !== "checking" || current.registry !== read.choice || !fastest.ok || fastest.value !== mirror) return;
				read.choice = {
					kind: "offered",
					registry: mirror
				};
				this.patchInstall({ registry: read.choice });
			}
			/**
			* Fold a chunk belonging to this installation into its pnpm command.
			* A final chunk may arrive after the install answer and still updates an existing run.
			* @param chunk - the chunk the Host forwarded.
			*/
			appendLog(chunk) {
				const install = this.getSnapshot().install;
				if (chunk.requestId !== install.requestId) return;
				if (install.requestId !== void 0 && (install.phase === "starting" || install.phase === "cancelling" || install.phase === "unconfirmed")) this.installProgress({
					requestId: install.requestId,
					phase: "installing"
				});
				const index = install.runs.findIndex((run) => run.jobId === chunk.jobId);
				if (index === -1 && !isInstallPending(install.phase)) return;
				const settled = chunk.exitCode === void 0 ? {} : { exitCode: chunk.exitCode };
				const runs = index === -1 ? [...install.runs, {
					jobId: chunk.jobId,
					command: chunk.argv.join(" "),
					cwd: chunk.cwd,
					output: chunk.text,
					...settled
				}] : install.runs.map((run, at) => at === index ? {
					...run,
					output: run.output + chunk.text,
					...settled
				} : run);
				this.patchInstall({ runs });
			}
			/**
			* Read the bundles and the entries their rows run as. A call during an
			* in-flight read marks one rerun after it settles.
			* @returns settlement after this call's freshness is reflected.
			*/
			load() {
				if (this.disposed) return Promise.resolve();
				this.reconcileInstall();
				if (this.inFlight !== void 0) {
					this.rerun = true;
					return this.inFlight;
				}
				const run = Promise.resolve().then(() => this.read());
				this.inFlight = run;
				return run;
			}
			async read() {
				try {
					do {
						this.rerun = false;
						const generation = ++this.generation;
						if (this.getSnapshot().status === "idle") this.patch({ status: "loading" });
						const inventory = await this.ctx.remote.pluginInventory.list();
						if (generation !== this.generation) return;
						if (!inventory.ok) {
							this.patch({ status: "error" });
							continue;
						}
						if (inventory.value.managementAvailable !== true) {
							this.patch({
								status: "unavailable",
								packages: []
							});
							continue;
						}
						const [bundles, plugins] = await Promise.all([this.ctx.remote.pluginManager.listBundles(), this.ctx.remote.pluginManager.listPlugins()]);
						if (generation !== this.generation) return;
						if (!bundles.ok || !plugins.ok) {
							this.patch({ status: "error" });
							continue;
						}
						this.patch({
							status: "ready",
							packages: sortPackages(bundles.value.map((bundle) => packageView(bundle, plugins.value)))
						});
					} while (this.shouldRerun());
				} finally {
					this.inFlight = void 0;
				}
			}
			shouldRerun() {
				return this.rerun;
			}
			async confirm() {
				const pending = this.pendingConfirm;
				this.pendingConfirm = void 0;
				this.patch({ confirm: null });
				if (pending !== void 0) await pending();
			}
			/** Drop the check in flight; its answer is ignored. */
			abortInspect() {
				this.inspectAbort?.abort();
				this.inspectAbort = void 0;
			}
			/** Whether a settlement arrives too late to matter: the store is disposed, or the dialog moved on. */
			gone(signal) {
				return this.disposed || signal.aborted;
			}
			/**
			* Check the typed spec, then install it. The Host reads what the spec
			* names first; a refused spec returns to the field with the reason, an
			* accepted one becomes the subject the next screens show while pnpm runs.
			*/
			async runInstall() {
				const state = this.getSnapshot();
				const install = state.install;
				const spec = install.spec.trim();
				if (install.phase === "checking" || isInstallPending(install.phase) || spec === "") return;
				if (state.packages.some((pkg) => pkg.name === spec)) {
					this.patchInstall({
						phase: "idle",
						inputError: {
							problem: "already-installed",
							reason: spec
						}
					});
					return;
				}
				const choice = install.registry;
				const typed = choice.kind === "custom" ? choice.url.trim() : void 0;
				if (typed !== void 0 && !REGISTRY_URL.test(typed)) {
					this.patchInstall({
						phase: "idle",
						registryError: true
					});
					return;
				}
				this.abortInspect();
				const controller = new AbortController();
				this.inspectAbort = controller;
				this.patchInstall({
					phase: "checking",
					inputError: null,
					subject: null,
					runs: [],
					detailsOpen: false,
					attempts: null,
					registryOpen: false,
					installed: null,
					restartRequired: false,
					failure: null,
					approvedBuilds: []
				});
				const read = this.registryRead;
				if (read !== void 0 && choice === read.choice && read.done !== void 0) {
					await read.done;
					if (this.gone(controller.signal) || this.registryRead !== read) return;
				}
				const currentChoice = this.getSnapshot().install.registry;
				const selected = currentChoice.kind === "custom" ? {
					...currentChoice,
					url: currentChoice.url.trim()
				} : currentChoice;
				const registry = selected.kind === "custom" ? selected.url : selected.registry;
				this.registryMemory.set(selected);
				const inspected = await this.ctx.remote.pluginManager.inspect(spec, { registry }, controller.signal);
				if (this.gone(controller.signal)) return;
				this.inspectAbort = void 0;
				if (!inspected.ok) {
					this.patchInstall({
						phase: "idle",
						inputError: {
							problem: "unknown",
							reason: inspected.error.message
						}
					});
					return;
				}
				if (inspected.value.status === "refused") {
					const { problem, reason, registries } = inspected.value;
					this.patchInstall({
						phase: "idle",
						inputError: {
							problem,
							reason,
							...registries === void 0 ? {} : { registries }
						}
					});
					return;
				}
				await this.startInstall({
					spec,
					...inspected.value
				});
			}
			/**
			* Hand the checked spec to the Host and settle the dialog from its answer.
			* `approvedBuilds` names the pending install scripts the person allowed;
			* the Host saves that permission for this profile before pnpm runs.
			*/
			async startInstall(subject, approvedBuilds) {
				const { spec, registry } = subject;
				const requestId = randomUUID();
				const request = {
					requestId,
					acknowledged: false,
					replyLost: false,
					recovering: false
				};
				this.request = request;
				this.patchInstall({
					phase: "starting",
					requestId,
					subject,
					runs: [],
					attempts: null,
					failure: null,
					installed: null,
					approvedBuilds: []
				});
				const result = await this.ctx.remote.pluginManager.installBundle(spec, {
					enabled: false,
					requestId,
					registry,
					...approvedBuilds === void 0 ? {} : { approvedBuilds: [...approvedBuilds] }
				});
				if (this.disposed || this.request !== request) return;
				if (!result.ok) {
					request.replyLost = true;
					this.installUncertain("result", result.error.message);
					this.reconcileInstall();
					return;
				}
				this.settleInstall(result.value);
			}
			/** A recovery call shares the Host's active result; absent results are explicitly unknown. */
			async reconcileInstall() {
				const request = this.request;
				if (request === void 0 || !request.replyLost || request.recovering) return;
				request.recovering = true;
				const result = await this.ctx.remote.pluginManager.waitForInstall(request.requestId);
				if (this.disposed || this.request !== request) return;
				request.recovering = false;
				if (!result.ok) {
					this.installUncertain("result", result.error.message);
					return;
				}
				if (result.value !== null) {
					this.settleInstall(result.value);
					return;
				}
				this.request = void 0;
				this.patchInstall({
					phase: "unknown",
					failure: null,
					runs: settledRuns(this.getSnapshot().install.runs, null)
				});
				this.notifyHiddenInstall("unknown");
				this.load();
			}
			settleInstall(result) {
				const { runs, attempts } = this.getSnapshot().install;
				this.request = void 0;
				const asked = result.registries === void 0 ? {} : { attempts: {
					registries: result.registries,
					total: Math.max(attempts?.total ?? 0, result.registries.length)
				} };
				if (result.application === "cancelled") this.offerSpecAgain({
					kind: "cancelled",
					seq: ++this.noticeSeq
				});
				else if (result.application === "failed") {
					const packages = result.packageResult;
					this.patchInstall({
						phase: "failed",
						runs: settledRuns(runs, packages?.exitCode ?? null),
						failure: failureOf(result.error, packages?.kind, result.pendingBuilds, result.failedAt),
						...asked
					});
				} else this.patchInstall({
					phase: "done",
					runs: settledRuns(runs, 0),
					failure: null,
					installed: result.bundle ?? null,
					restartRequired: result.application === "restart-required",
					approvedBuilds: result.approvedBuilds ?? [],
					...asked
				});
				const phase = this.getSnapshot().install.phase;
				if (phase === "done" || phase === "failed") this.notifyHiddenInstall(phase);
				this.load();
			}
			installUncertain(uncertainty, reason) {
				this.patchInstall({
					phase: this.getSnapshot().install.phase === "applying" ? "applying" : "unconfirmed",
					failure: {
						reason,
						uncertainty
					}
				});
				this.notifyHiddenInstall("unconfirmed");
			}
			/**
			* Allow the install scripts the failed run left pending and run the same
			* spec again. Only the failed screen with pending names offers this.
			*/
			async approveBuildsAndRetry() {
				const install = this.getSnapshot().install;
				const pending = install.failure?.pendingBuilds;
				if (install.phase !== "failed" || install.subject === null || pending === void 0 || pending.length === 0) return;
				await this.startInstall(install.subject, pending);
			}
			/**
			* Leave the check or the failed screen for the spec at once; a Host-owned
			* run is asked to stop and its state waits for the Host's word, since
			* neither a dropped RPC nor a closed connection means pnpm has stopped.
			*/
			async cancelInstall() {
				const install = this.getSnapshot().install;
				if (install.phase === "checking" || install.phase === "failed" || install.phase === "unknown") {
					this.abortInspect();
					this.offerSpecAgain();
					return;
				}
				const request = this.request;
				if (install.phase !== "starting" && install.phase !== "running" && install.phase !== "unconfirmed" || request === void 0) return;
				if (request.cancellation !== void 0 && !request.cancellation.waitingForStart) return;
				const cancellation = { waitingForStart: false };
				request.cancellation = cancellation;
				await this.sendCancellation(request, cancellation);
			}
			/** A cancellation that overtakes installation is retried after the Host acknowledges that request. */
			async sendCancellation(request, cancellation) {
				const acknowledged = request.acknowledged;
				cancellation.waitingForStart = false;
				this.patchInstall({
					phase: "cancelling",
					failure: null
				});
				const result = await this.ctx.remote.pluginManager.cancelInstall(request.requestId);
				if (this.disposed || this.request !== request || request.cancellation !== cancellation) return;
				if (!result.ok) {
					cancellation.waitingForStart = !request.acknowledged;
					if (request.acknowledged) request.cancellation = void 0;
					this.installUncertain(request.replyLost ? "result" : "cancellation", result.error.message);
					return;
				}
				if (result.value.status === "cancelled") {
					this.offerSpecAgain({
						kind: "cancelled",
						seq: ++this.noticeSeq
					});
					this.load();
				} else if (result.value.status === "too-late") {
					request.acknowledged = true;
					request.cancellation = void 0;
					this.patchInstall({ phase: "applying" });
					this.notifyHiddenInstall("applying");
					this.reconcileInstall();
				} else if (request.replyLost) {
					request.cancellation = void 0;
					this.installUncertain("result", "");
					this.reconcileInstall();
				} else if (!acknowledged && this.getSnapshot().install.phase !== "applying") if (request.acknowledged) await this.sendCancellation(request, cancellation);
				else {
					cancellation.waitingForStart = true;
					this.installUncertain("acceptance", "");
				}
				else {
					request.cancellation = void 0;
					this.installUncertain("cancellation", "");
				}
			}
			notifyHiddenInstall(outcome) {
				if (!this.getSnapshot().install.open) this.patch({ notice: {
					kind: "install",
					outcome,
					seq: ++this.noticeSeq
				} });
			}
			/**
			* Release the tracked request and return to editing with its spec retained.
			* A cancellation notice is supplied only after the Host confirms it stopped.
			*/
			offerSpecAgain(notice = null) {
				this.request = void 0;
				this.patch({
					install: specAgain(this.getSnapshot().install),
					...notice === null ? {} : { notice }
				});
			}
			/**
			* Enable the bundle the finished install added, then close the dialog and
			* mark it in the list. A refusal toasts and still closes: the list shows
			* what did not switch on.
			*/
			async enableInstalled() {
				const install = this.getSnapshot().install;
				if (install.phase !== "done" || install.enabling) return;
				const name = install.installed;
				this.patchInstall({ enabling: true });
				if (name !== null) {
					const result = await this.ctx.remote.pluginManager.setBundleEnabled(name, true);
					if (this.disposed) return;
					try {
						this.applied(result, name);
					} catch (error) {
						this.patch({ notice: failedNotice(error, {
							packageName: name,
							action: "enable"
						}, ++this.noticeSeq) });
					}
				}
				this.patch({
					install: IDLE_INSTALL,
					highlight: name
				});
				await this.load();
			}
			/**
			* Run one action under a busy key, turn its failure into the notice, and
			* re-read the Host afterwards whatever happened.
			*/
			async run(key, subject, action) {
				if (this.disposed || this.getSnapshot().busy.includes(key)) return;
				this.patch({
					busy: [...this.getSnapshot().busy, key],
					notice: null
				});
				try {
					await action();
				} catch (error) {
					this.patch({ notice: failedNotice(error, subject, ++this.noticeSeq) });
				} finally {
					this.patch({ busy: this.getSnapshot().busy.filter((entry) => entry !== key) });
				}
				await this.load();
			}
			/**
			* Publish a change's outcome: a refused answer or a change the Host could
			* not apply throws for {@link run} to report; a change that waits for the
			* next start, that a higher layer overrides, or that the Host stopped is
			* said in passing.
			*/
			applied(answer, packageName) {
				if (!answer.ok) throw new RemoteAnswerError(answer.error.message);
				const result = answer.value;
				switch (result.application) {
					case "failed": throw new RemoteAnswerError(result.error?.diagnostic ?? "", result.error?.code, result.error?.incompatible);
					case "cancelled":
						this.patch({ notice: {
							kind: "cancelled",
							seq: ++this.noticeSeq
						} });
						return;
					case "restart-required":
						this.patch({ notice: {
							kind: "restart",
							packageName,
							seq: ++this.noticeSeq
						} });
						return;
					case "overridden":
						this.patch({ notice: {
							kind: "overridden",
							packageName,
							seq: ++this.noticeSeq
						} });
						return;
					case "applied": return;
				}
			}
			patch(next) {
				if (this.disposed) return;
				this.store.set({
					...this.getSnapshot(),
					...next
				});
			}
			patchInstall(next) {
				this.patch({ install: {
					...this.getSnapshot().install,
					...next
				} });
			}
		};
		/** What a thrown failure said: a refused answer's reason, else the error's message. */
		function reasonOf(error) {
			return error instanceof Error ? error.message : String(error);
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-plugin-manager/src/client/PluginManagerPage.module.css.mjs
		const css = ".tWNbIG_page{box-sizing:border-box;height:100%;color:var(--dsw-alias-label-primary);flex-direction:column;align-items:center;gap:32px;padding:0 clamp(24px,4vw,48px) 48px;display:flex;overflow:auto}.tWNbIG_page>*{width:100%;max-width:960px}.tWNbIG_pageHead{box-sizing:border-box;justify-content:space-between;align-items:flex-start;gap:16px;padding-top:28px;display:flex}[data-platform=darwin] .tWNbIG_pageHead{padding-top:calc(28px + var(--dsh-frame-top-clearance,0px))}.tWNbIG_pageTitle{margin:0;font-size:20px;font-weight:500;line-height:28px}.tWNbIG_pageIntro{color:var(--dsw-alias-label-secondary);margin:4px 0 0;font-size:13px;line-height:20px}.tWNbIG_toolbar{justify-content:flex-end;align-items:center;gap:16px;display:flex}.tWNbIG_status,.tWNbIG_failure p,.tWNbIG_empty{color:var(--dsw-alias-label-tertiary);margin:0;font-size:13px;line-height:20px}.tWNbIG_failure{color:var(--dsw-alias-state-error-primary);align-items:center;gap:10px;display:flex}.tWNbIG_statusWithDot{align-items:center;gap:6px;display:inline-flex}.tWNbIG_banner{border-radius:var(--dsw-radius-md);background:color-mix(in srgb, var(--dsw-alias-state-warning-primary,var(--dsw-alias-state-business-primary)) 12%, transparent);color:var(--dsw-alias-label-primary);margin:0;padding:8px 12px;font-size:12px;line-height:18px}.tWNbIG_group{flex-direction:column;gap:8px;display:flex}.tWNbIG_groupHead{align-items:baseline;gap:8px;display:flex}.tWNbIG_groupTitle{margin:0;font-size:14px;font-weight:500;line-height:22px}.tWNbIG_count{color:var(--dsw-alias-label-caption);font-variant-numeric:tabular-nums;font-size:14px}.tWNbIG_groupInfo{color:var(--dsw-alias-label-caption);align-self:center;align-items:center;display:inline-flex}.tWNbIG_groupInfo:hover,.tWNbIG_groupInfo:focus-visible{color:var(--dsw-alias-label-secondary)}.tWNbIG_statusTag{height:18px;padding:0 7px;font-size:10px;line-height:1}.tWNbIG_card[data-plugin-highlight]{animation:2.4s ease-out tWNbIG_dsh-plugin-highlight}@keyframes tWNbIG_dsh-plugin-highlight{0%,55%{box-shadow:0 0 0 2px var(--dsw-alias-state-business-primary)}to{box-shadow:0 0 #0000}}@media (prefers-reduced-motion:reduce){.tWNbIG_card[data-plugin-highlight]{box-shadow:0 0 0 2px var(--dsw-alias-state-business-primary);animation:none}}.tWNbIG_iconButton:focus-visible{outline:var(--dsw-focus-ring-width) solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:1px}.tWNbIG_cards{flex-direction:column;gap:2px;margin:0;padding:0;list-style:none;display:flex}.tWNbIG_card{border-radius:var(--dsw-radius-xl);--dsh-scrollbar-thumb:var(--dsw-alias-scrollbar-bg-l2);--dsh-scrollbar-thumb-hover:var(--dsw-alias-scrollbar-hover-l2);min-width:0;margin:0 -8px}.tWNbIG_cardHead{align-items:center;gap:14px;padding:8px;display:flex}.tWNbIG_cardIcon{border:.5px solid var(--dsw-alias-border-l3);border-radius:var(--dsw-radius-lg);width:48px;height:48px;color:var(--dsw-alias-label-secondary);flex:none;justify-content:center;align-items:center;display:inline-flex}.tWNbIG_packageImage{object-fit:contain}.tWNbIG_cardMain{flex-direction:column;flex:1;gap:4px;min-width:0;display:flex}.tWNbIG_titleRow{flex-wrap:wrap;align-items:center;gap:8px;min-width:0;display:flex}.tWNbIG_cardTitle{text-overflow:ellipsis;white-space:nowrap;font-size:14px;font-weight:500;line-height:20px;overflow:hidden}.tWNbIG_cardLink{position:relative}.tWNbIG_cardLink:hover{background:var(--dsw-alias-interactive-bg-hover)}.tWNbIG_cardOpen{max-width:100%;color:inherit;font:inherit;text-align:left;cursor:pointer;background:0 0;border:0;padding:0;font-size:14px;font-weight:500;line-height:20px}.tWNbIG_cardOpen:after{content:\"\";border-radius:inherit;position:absolute;inset:0}.tWNbIG_cardOpen:focus-visible{outline:none}.tWNbIG_cardOpen:focus-visible:after{outline:var(--dsw-focus-ring-width) solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:2px}.tWNbIG_cardDesc{color:var(--dsw-alias-label-tertiary);-webkit-line-clamp:1;-webkit-box-orient:vertical;font-size:13px;line-height:18px;display:-webkit-box;overflow:hidden}.tWNbIG_cardEnd{z-index:1;flex:none;align-items:center;gap:8px;display:inline-flex;position:relative}.tWNbIG_iconButton{border-radius:var(--dsw-radius-sm);width:28px;height:28px;color:var(--dsw-alias-label-caption);cursor:pointer;background:0 0;border:0;justify-content:center;align-items:center;display:inline-flex}.tWNbIG_iconButton:hover:not(:disabled){background:var(--dsw-alias-interactive-bg-hover);color:var(--dsw-alias-label-secondary)}.tWNbIG_iconButton:disabled{opacity:.5;cursor:default}.tWNbIG_addButton{border-radius:var(--dsw-radius-md);height:32px;padding:0 12px;font-size:13px;line-height:20px}.tWNbIG_iconWrap{display:inline-flex}.tWNbIG_danger{color:var(--dsw-alias-state-error-primary);border-color:color-mix(in srgb, var(--dsw-alias-state-error-primary) 30%, transparent);--dsw-alias-interactive-bg-hover:color-mix(in srgb, var(--dsw-alias-state-error-primary) 8%, transparent)}.tWNbIG_detailActions{flex:none;align-items:center;gap:16px;display:flex}.tWNbIG_actions{align-items:center;gap:16px;display:flex}.tWNbIG_deleteButton{border-radius:var(--dsw-radius-sm);width:28px;height:28px;color:var(--dsw-alias-state-error-primary);cursor:pointer;background:0 0;border:0;justify-content:center;align-items:center;display:inline-flex}.tWNbIG_deleteButton:hover:not(:disabled){background:var(--dsw-alias-interactive-bg-hover-danger,var(--dsw-alias-interactive-bg-hover))}.tWNbIG_deleteButton:disabled{opacity:.5;cursor:default}.tWNbIG_deleteButton:focus-visible{outline:var(--dsw-focus-ring-width) solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:1px}.tWNbIG_reason{color:var(--dsw-alias-state-error-primary);overflow-wrap:anywhere;white-space:pre-wrap;margin:0;font-size:12px;line-height:18px}.tWNbIG_partsHead .tWNbIG_subLabel{margin:0}.tWNbIG_partsFilter{width:200px}.tWNbIG_partsFilter:focus-visible{outline:var(--dsw-focus-ring-width) solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:1px}.tWNbIG_installDialog{width:min(560px,100%);max-height:min(800px,100%)}.tWNbIG_installContent{min-height:0;overflow-y:auto}.tWNbIG_installBody{flex-direction:column;gap:12px;min-width:0;display:flex}.tWNbIG_installLocation{color:var(--dsw-alias-label-tertiary);overflow-wrap:anywhere;margin:0;font-size:12px;line-height:18px}.tWNbIG_installField{flex-direction:column;gap:6px;font-size:13px;display:flex}.tWNbIG_installField input[type=text]{border:.5px solid var(--dsw-alias-border-l4);border-radius:var(--dsw-radius-md);background:var(--dsw-alias-bg-layer-3);height:40px;font:inherit;color:var(--dsw-alias-label-primary);outline:none;padding:0 14px;font-size:13px}.tWNbIG_installField input[type=text]:focus{border-color:var(--dsw-alias-state-business-primary);box-shadow:inset 0 0 0 .5px var(--dsw-alias-state-business-primary)}.tWNbIG_installField input[aria-invalid=true]{border-color:var(--dsw-alias-state-error-primary)}.tWNbIG_guideToggle{font:inherit;color:var(--dsw-alias-label-secondary);cursor:pointer;background:0 0;border:0;align-self:flex-start;align-items:center;gap:4px;margin-top:4px;padding:0;font-size:12.5px;display:inline-flex}.tWNbIG_guideToggle:hover{color:var(--dsw-alias-label-primary)}.tWNbIG_guideChevron{transition:transform .16s}.tWNbIG_guideToggle[aria-expanded=true] .tWNbIG_guideChevron{transform:rotate(180deg)}.tWNbIG_guide{border:.5px solid var(--dsw-alias-border-l4);border-radius:var(--dsw-radius-lg);background:var(--dsw-alias-bg-layer-1);flex-direction:column;gap:10px;padding:8px 14px 14px;display:flex}.tWNbIG_guideHint{color:var(--dsw-alias-label-tertiary);margin:0;font-size:12px;line-height:18px}.tWNbIG_guideSafety{border-radius:var(--dsw-radius-md);background:color-mix(in srgb, var(--dsw-alias-state-warning-primary,var(--dsw-alias-state-business-primary)) 12%, transparent);color:var(--dsw-alias-state-warning-primary,var(--dsw-alias-state-business-primary));align-items:flex-start;gap:8px;margin:0;padding:10px 12px;font-size:12px;line-height:18px;display:flex}.tWNbIG_guideSafety>svg{flex:none;margin-top:2px}.tWNbIG_guideList{flex-direction:column;gap:2px;margin:0;padding:0;list-style:none;display:flex}.tWNbIG_guideItem{align-items:flex-start;gap:10px;padding:8px 0;display:flex}.tWNbIG_guideIndex{corner-shape:round;background:var(--dsw-alias-bg-module-platform);width:20px;height:20px;color:var(--dsw-alias-label-secondary);border-radius:999px;flex:none;justify-content:center;align-items:center;font-size:11px;font-weight:500;display:inline-flex}.tWNbIG_guideMain{flex-direction:column;flex:1;gap:0;min-width:0;display:flex}.tWNbIG_guideTitle{color:var(--dsw-alias-label-primary);font-size:13px;font-weight:500;line-height:20px}.tWNbIG_guideExample{color:var(--dsw-alias-label-secondary);overflow-wrap:anywhere;font-size:12px;line-height:20px}.tWNbIG_guideExample code{font-family:var(--dsw-font-mono,ui-monospace, SFMono-Regular, Menlo, monospace)}.tWNbIG_guideExampleLabel{color:var(--dsw-alias-label-tertiary)}.tWNbIG_optionsRow{flex-wrap:wrap;justify-content:space-between;align-items:center;gap:8px 12px;display:flex}.tWNbIG_registryToggle{border:.5px solid var(--dsw-alias-border-l4);border-radius:var(--dsw-radius-sm);background:var(--dsw-alias-bg-layer-2);height:26px;font:inherit;color:var(--dsw-alias-label-secondary);cursor:pointer;align-items:center;gap:6px;padding:0 8px 0 10px;font-size:12.5px;display:inline-flex}.tWNbIG_registryToggle:hover:not(:disabled){color:var(--dsw-alias-label-primary)}.tWNbIG_registryToggle:disabled{cursor:default;opacity:.6}.tWNbIG_registryToggle[aria-expanded=true]{border-color:var(--dsw-alias-border-l3);background:var(--dsw-alias-bg-layer-3)}.tWNbIG_registryToggle[aria-expanded=true] .tWNbIG_guideChevron{transform:rotate(180deg)}.tWNbIG_registryChosen{color:var(--dsw-alias-label-primary);font-weight:500}.tWNbIG_registry{z-index:1100;box-sizing:border-box;border-radius:var(--dsw-radius-lg);background:var(--dsw-alias-bg-layer-2);--dsw-elevation-stroke-color:var(--dsw-alias-border-l3);width:min(440px,100vw - 24px);box-shadow:var(--dsw-elevation-prominent);border:0;flex-direction:column;gap:4px;min-width:0;margin:0;padding:8px 10px 12px;display:flex;position:fixed}.tWNbIG_registryOption{border-radius:var(--dsw-radius-lg);cursor:pointer;border:.5px solid #0000;flex-direction:column;gap:4px;padding:8px 10px;display:flex}.tWNbIG_registryOption[data-checked=true]{border-color:color-mix(in srgb, var(--dsw-alias-brand-primary) 40%, transparent);background:var(--dsw-alias-bg-layer-2)}label.tWNbIG_registryOption{flex-direction:row;align-items:flex-start;gap:10px}.tWNbIG_registryOption input[type=radio]{width:16px;height:16px;accent-color:var(--dsw-alias-brand-primary);flex:none;margin:2px 0 0}.tWNbIG_registryTitle{color:var(--dsw-alias-label-primary);flex-wrap:wrap;align-items:center;gap:8px;font-size:13px;line-height:20px;display:flex}.tWNbIG_registryHint{color:var(--dsw-alias-label-tertiary);overflow-wrap:anywhere;font-size:12px;line-height:18px}.tWNbIG_registryCustomPick{cursor:pointer;align-items:flex-start;gap:10px;display:flex}.tWNbIG_registryCustomField{border:.5px solid var(--dsw-alias-border-l4);border-radius:var(--dsw-radius-md);background:var(--dsw-alias-bg-layer-3);height:32px;font:inherit;color:var(--dsw-alias-label-primary);margin-left:26px;padding:0 10px;font-size:13px}.tWNbIG_registryCustomField[aria-invalid=true]{border-color:var(--dsw-alias-state-error-primary)}.tWNbIG_registryOption>.tWNbIG_inputError,.tWNbIG_registryOption>.tWNbIG_registryHint{margin-left:26px}.tWNbIG_inputError{color:var(--dsw-alias-state-error-primary);margin:-4px 0 0;font-size:12px;line-height:18px}.tWNbIG_wide{border-radius:var(--dsw-radius-md);justify-content:center;width:100%;height:40px}.tWNbIG_wizard{flex-direction:column;flex:auto;gap:16px;min-width:0;min-height:0;padding:16px 20px 0;display:flex}.tWNbIG_wizardScroll{flex-direction:column;flex:auto;gap:16px;min-height:0;display:flex;overflow-y:auto}.tWNbIG_wizardHead{justify-content:space-between;align-items:center;min-height:24px;display:flex}.tWNbIG_wizardBack{font:inherit;color:var(--dsw-alias-label-primary);cursor:pointer;background:0 0;border:0;align-items:center;gap:4px;padding:0;font-size:15px;font-weight:600;display:inline-flex}.tWNbIG_wizardClose{border-radius:var(--dsw-radius-sm);width:24px;height:24px;color:var(--dsw-alias-label-tertiary);cursor:pointer;background:0 0;border:0;justify-content:center;align-items:center;padding:0;display:inline-flex}.tWNbIG_wizardClose:hover{background:var(--dsw-alias-bg-layer-3);color:var(--dsw-alias-label-primary)}.tWNbIG_wizardHero{text-align:center;flex-direction:column;align-items:center;gap:10px;padding:8px 0 4px;display:flex}.tWNbIG_wizardIcon{width:44px;height:44px;color:var(--dsw-alias-label-secondary);justify-content:center;align-items:center;display:inline-flex}.tWNbIG_wizardIcon[data-state=done]{color:var(--dsw-alias-state-success-secondary)}.tWNbIG_wizardIcon[data-state=error]{color:var(--dsw-alias-state-warn-label)}.tWNbIG_wizardTitle{margin:0;font-size:18px;font-weight:600;line-height:26px}.tWNbIG_wizardSub{color:var(--dsw-alias-label-secondary);overflow-wrap:anywhere;margin:0;font-size:13px;line-height:20px}.tWNbIG_subject{border:.5px solid var(--dsw-alias-border-l3);border-radius:var(--dsw-radius-lg);text-align:center;flex-direction:column;align-items:center;gap:6px;padding:16px;display:flex}.tWNbIG_subjectName{overflow-wrap:anywhere;margin:0;font-size:15px;font-weight:600;line-height:22px}.tWNbIG_subjectDesc,.tWNbIG_subjectMeta{color:var(--dsw-alias-label-secondary);overflow-wrap:anywhere;margin:0;font-size:13px;line-height:20px}.tWNbIG_wizardFoot{justify-content:space-between;align-items:center;gap:12px;display:flex}.tWNbIG_footAction{border-radius:var(--dsw-radius-md);height:32px}.tWNbIG_detailsToggle{font:inherit;color:var(--dsw-alias-label-secondary);cursor:pointer;background:0 0;border:0;align-items:center;gap:4px;padding:0;font-size:13px;display:inline-flex}.tWNbIG_detailsChevron{transition:transform .16s}.tWNbIG_detailsToggle[aria-expanded=true] .tWNbIG_detailsChevron{transform:rotate(180deg)}.tWNbIG_detailsBody{flex-direction:column;gap:8px;min-width:0;display:flex}.tWNbIG_wizardActions{align-items:center;gap:8px;display:flex}.tWNbIG_run{flex-direction:column;gap:4px;min-width:0;display:flex}.tWNbIG_attemptBadge{border-radius:var(--dsw-radius-sm);background:var(--dsw-alias-bg-layer-3);color:var(--dsw-alias-label-secondary);align-self:flex-start;margin:0;padding:0 6px;font-size:11px;line-height:18px}@media (prefers-reduced-motion:reduce){.tWNbIG_detailsChevron{transition:none}}.tWNbIG_result,.tWNbIG_resultWarn{border-radius:var(--dsw-radius-md);background:color-mix(in srgb, var(--dsw-alias-state-success-primary) 10%, transparent);color:var(--dsw-alias-label-primary);margin:0;padding:8px 12px;font-size:13px;line-height:20px}.tWNbIG_resultWarn{background:color-mix(in srgb, var(--dsw-alias-state-warning-primary,var(--dsw-alias-state-business-primary)) 12%, transparent)}.tWNbIG_approval{border:.5px solid color-mix(in srgb, var(--dsw-alias-state-warning-primary,var(--dsw-alias-state-business-primary)) 40%, transparent);border-radius:var(--dsw-radius-lg);background:color-mix(in srgb, var(--dsw-alias-state-warning-primary,var(--dsw-alias-state-business-primary)) 8%, transparent);flex-direction:column;gap:8px;padding:12px 14px;display:flex}.tWNbIG_approvalTitle{color:var(--dsw-alias-label-primary);margin:0;font-size:13px;font-weight:600}.tWNbIG_approvalText{color:var(--dsw-alias-label-secondary);margin:0;font-size:12.5px;line-height:18px}.tWNbIG_approvalCaution{color:var(--dsw-alias-state-warning-primary,var(--dsw-alias-state-business-primary));margin:0;font-size:12.5px;font-weight:600;line-height:18px}.tWNbIG_approvalList{flex-wrap:wrap;gap:6px;margin:0;padding:0;list-style:none;display:flex}.tWNbIG_approvalList code{border-radius:var(--dsw-radius-sm);background:var(--dsw-alias-bg-layer-3);font-family:var(--dsw-font-mono,ui-monospace, SFMono-Regular, Menlo, monospace);color:var(--dsw-alias-label-primary);padding:2px 8px;font-size:12px;display:inline-block}.tWNbIG_terminal{--dsl-terminal-font:var(--dsw-font-markdown-code-block-small);--dsl-terminal-line-height:18px;--dsl-terminal-output-max-height:240px;border:.5px solid var(--dsw-alias-border-l1);margin:4px 0 0}.tWNbIG_dependents{color:var(--dsw-alias-label-secondary);margin:8px 0 0;padding-left:18px;font-size:13px;line-height:20px}.tWNbIG_dangerButton{--dsw-alias-button-primary-fill:var(--dsw-alias-state-error-primary);--dsw-alias-button-primary-hover:var(--dsw-alias-state-error-primary)}.tWNbIG_detail{flex-direction:column;display:flex}.tWNbIG_detailTop{flex-direction:column;padding-top:28px;display:flex}[data-platform=darwin] .tWNbIG_detailTop{padding-top:calc(28px + var(--dsh-frame-top-clearance,0px))}.tWNbIG_crumb{color:var(--dsw-alias-label-tertiary);font:inherit;cursor:pointer;background:0 0;border:0;align-items:center;gap:6px;padding:0;font-size:12.5px;display:inline-flex}.tWNbIG_crumb:hover{color:var(--dsw-alias-label-primary)}.tWNbIG_crumb:focus-visible{outline:var(--dsw-focus-ring-width) solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:2px}.tWNbIG_crumbIcon{transform:rotate(90deg)}.tWNbIG_detailHead{justify-content:space-between;align-items:center;gap:12px;margin:32px 0 0;display:flex}.tWNbIG_detailMain{flex-direction:column;gap:8px;min-width:0;margin-top:20px;display:flex}.tWNbIG_detailTitle{margin:0;font-size:20px;font-weight:500;line-height:28px}.tWNbIG_versionTag{font-variant-numeric:tabular-nums;flex:none}.tWNbIG_detailDesc{color:var(--dsw-alias-label-secondary);margin:0;font-size:14px;line-height:22px}.tWNbIG_detailName{color:var(--dsw-alias-label-tertiary);overflow-wrap:anywhere;margin:0;font-size:12px;line-height:18px}.tWNbIG_detailName code{font-family:var(--dsw-font-mono,ui-monospace, SFMono-Regular, Menlo, monospace)}.tWNbIG_detail>.tWNbIG_detailDesc{margin-top:12px}.tWNbIG_detailSections{flex-direction:column;gap:32px;margin-top:32px;display:flex}.tWNbIG_detailSection{flex-direction:column;gap:12px;display:flex}.tWNbIG_sectionHead{align-items:baseline;gap:10px;display:flex}.tWNbIG_sectionTitle{margin:0;font-size:14px;font-weight:500;line-height:20px}.tWNbIG_sectionCount{color:var(--dsw-alias-label-secondary);font-size:12px;line-height:18px}.tWNbIG_cardArrow{color:var(--dsw-alias-label-tertiary);flex:none}.tWNbIG_rows{flex-direction:column;margin:0;padding:0;list-style:none;display:flex}.tWNbIG_row{border-bottom:.5px solid var(--dsw-alias-border-l2);padding:12px 2px}.tWNbIG_row:last-child{border-bottom:0}.tWNbIG_rowLine{align-items:center;gap:16px;min-width:0;display:flex}.tWNbIG_rowIcon{border:.5px solid var(--dsw-alias-border-l3);border-radius:var(--dsw-radius-md);width:40px;height:40px;color:var(--dsw-alias-label-secondary);flex:none;justify-content:center;align-items:center;display:inline-flex}.tWNbIG_rowMain{flex-direction:column;flex:1;gap:2px;min-width:0;display:flex}.tWNbIG_rowId{color:var(--dsw-alias-label-primary);overflow-wrap:anywhere;font-size:13.5px;font-weight:500;line-height:20px}.tWNbIG_row[data-state=off] .tWNbIG_rowId{color:var(--dsw-alias-label-secondary)}.tWNbIG_rowModule{font-family:var(--dsw-font-mono,ui-monospace, SFMono-Regular, Menlo, monospace);color:var(--dsw-alias-label-tertiary);overflow-wrap:anywhere;font-size:11.5px;line-height:16px}.tWNbIG_rowOpen{color:inherit;font:inherit;text-align:left;cursor:pointer;background:0 0;border:0;align-items:center;gap:2px;padding:0;display:inline-flex}.tWNbIG_rowOpen:hover .tWNbIG_rowId{text-underline-offset:3px;text-decoration:underline}.tWNbIG_rowOpenIcon{color:var(--dsw-alias-label-tertiary);flex:none}.tWNbIG_rowOpen:hover .tWNbIG_rowOpenIcon{color:var(--dsw-alias-label-primary)}.tWNbIG_rowState{color:var(--dsw-alias-label-secondary);white-space:nowrap;flex:none;align-items:center;gap:6px;font-size:12.5px;line-height:18px;display:inline-flex}.tWNbIG_row[data-state=failed] .tWNbIG_rowState{color:var(--dsw-alias-state-error-primary)}.tWNbIG_rowFailure{color:var(--dsw-alias-state-error-primary);overflow-wrap:anywhere;margin:4px 0 0 56px;font-size:12px;line-height:18px}";
		const tagId = "@deepseek-ai/dsh-client-ui-plugin-manager/PluginManagerPage.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-plugin-manager";
			tag.dataset.pluginCss = tagId;
			tag.textContent = css;
			document.head.appendChild(tag);
		}
		var PluginManagerPage_module_css_default = {
			"actions": "tWNbIG_actions",
			"addButton": "tWNbIG_addButton",
			"approval": "tWNbIG_approval",
			"approvalCaution": "tWNbIG_approvalCaution",
			"approvalList": "tWNbIG_approvalList",
			"approvalText": "tWNbIG_approvalText",
			"approvalTitle": "tWNbIG_approvalTitle",
			"attemptBadge": "tWNbIG_attemptBadge",
			"banner": "tWNbIG_banner",
			"card": "tWNbIG_card",
			"cardArrow": "tWNbIG_cardArrow",
			"cardDesc": "tWNbIG_cardDesc",
			"cardEnd": "tWNbIG_cardEnd",
			"cardHead": "tWNbIG_cardHead",
			"cardIcon": "tWNbIG_cardIcon",
			"cardLink": "tWNbIG_cardLink",
			"cardMain": "tWNbIG_cardMain",
			"cardOpen": "tWNbIG_cardOpen",
			"cardTitle": "tWNbIG_cardTitle",
			"cards": "tWNbIG_cards",
			"count": "tWNbIG_count",
			"crumb": "tWNbIG_crumb",
			"crumbIcon": "tWNbIG_crumbIcon",
			"danger": "tWNbIG_danger",
			"dangerButton": "tWNbIG_dangerButton",
			"deleteButton": "tWNbIG_deleteButton",
			"dependents": "tWNbIG_dependents",
			"detail": "tWNbIG_detail",
			"detailActions": "tWNbIG_detailActions",
			"detailDesc": "tWNbIG_detailDesc",
			"detailHead": "tWNbIG_detailHead",
			"detailMain": "tWNbIG_detailMain",
			"detailName": "tWNbIG_detailName",
			"detailSection": "tWNbIG_detailSection",
			"detailSections": "tWNbIG_detailSections",
			"detailTitle": "tWNbIG_detailTitle",
			"detailTop": "tWNbIG_detailTop",
			"detailsBody": "tWNbIG_detailsBody",
			"detailsChevron": "tWNbIG_detailsChevron",
			"detailsToggle": "tWNbIG_detailsToggle",
			"dsh-plugin-highlight": "tWNbIG_dsh-plugin-highlight",
			"empty": "tWNbIG_empty",
			"failure": "tWNbIG_failure",
			"footAction": "tWNbIG_footAction",
			"group": "tWNbIG_group",
			"groupHead": "tWNbIG_groupHead",
			"groupInfo": "tWNbIG_groupInfo",
			"groupTitle": "tWNbIG_groupTitle",
			"guide": "tWNbIG_guide",
			"guideChevron": "tWNbIG_guideChevron",
			"guideExample": "tWNbIG_guideExample",
			"guideExampleLabel": "tWNbIG_guideExampleLabel",
			"guideHint": "tWNbIG_guideHint",
			"guideIndex": "tWNbIG_guideIndex",
			"guideItem": "tWNbIG_guideItem",
			"guideList": "tWNbIG_guideList",
			"guideMain": "tWNbIG_guideMain",
			"guideSafety": "tWNbIG_guideSafety",
			"guideTitle": "tWNbIG_guideTitle",
			"guideToggle": "tWNbIG_guideToggle",
			"iconButton": "tWNbIG_iconButton",
			"iconWrap": "tWNbIG_iconWrap",
			"inputError": "tWNbIG_inputError",
			"installBody": "tWNbIG_installBody",
			"installContent": "tWNbIG_installContent",
			"installDialog": "tWNbIG_installDialog",
			"installField": "tWNbIG_installField",
			"installLocation": "tWNbIG_installLocation",
			"optionsRow": "tWNbIG_optionsRow",
			"packageImage": "tWNbIG_packageImage",
			"page": "tWNbIG_page",
			"pageHead": "tWNbIG_pageHead",
			"pageIntro": "tWNbIG_pageIntro",
			"pageTitle": "tWNbIG_pageTitle",
			"partsFilter": "tWNbIG_partsFilter",
			"partsHead": "tWNbIG_partsHead",
			"reason": "tWNbIG_reason",
			"registry": "tWNbIG_registry",
			"registryChosen": "tWNbIG_registryChosen",
			"registryCustomField": "tWNbIG_registryCustomField",
			"registryCustomPick": "tWNbIG_registryCustomPick",
			"registryHint": "tWNbIG_registryHint",
			"registryOption": "tWNbIG_registryOption",
			"registryTitle": "tWNbIG_registryTitle",
			"registryToggle": "tWNbIG_registryToggle",
			"result": "tWNbIG_result",
			"resultWarn": "tWNbIG_resultWarn",
			"row": "tWNbIG_row",
			"rowFailure": "tWNbIG_rowFailure",
			"rowIcon": "tWNbIG_rowIcon",
			"rowId": "tWNbIG_rowId",
			"rowLine": "tWNbIG_rowLine",
			"rowMain": "tWNbIG_rowMain",
			"rowModule": "tWNbIG_rowModule",
			"rowOpen": "tWNbIG_rowOpen",
			"rowOpenIcon": "tWNbIG_rowOpenIcon",
			"rowState": "tWNbIG_rowState",
			"rows": "tWNbIG_rows",
			"run": "tWNbIG_run",
			"sectionCount": "tWNbIG_sectionCount",
			"sectionHead": "tWNbIG_sectionHead",
			"sectionTitle": "tWNbIG_sectionTitle",
			"status": "tWNbIG_status",
			"statusTag": "tWNbIG_statusTag",
			"statusWithDot": "tWNbIG_statusWithDot",
			"subLabel": "tWNbIG_subLabel",
			"subject": "tWNbIG_subject",
			"subjectDesc": "tWNbIG_subjectDesc",
			"subjectMeta": "tWNbIG_subjectMeta",
			"subjectName": "tWNbIG_subjectName",
			"terminal": "tWNbIG_terminal",
			"titleRow": "tWNbIG_titleRow",
			"toolbar": "tWNbIG_toolbar",
			"versionTag": "tWNbIG_versionTag",
			"wide": "tWNbIG_wide",
			"wizard": "tWNbIG_wizard",
			"wizardActions": "tWNbIG_wizardActions",
			"wizardBack": "tWNbIG_wizardBack",
			"wizardClose": "tWNbIG_wizardClose",
			"wizardFoot": "tWNbIG_wizardFoot",
			"wizardHead": "tWNbIG_wizardHead",
			"wizardHero": "tWNbIG_wizardHero",
			"wizardIcon": "tWNbIG_wizardIcon",
			"wizardScroll": "tWNbIG_wizardScroll",
			"wizardSub": "tWNbIG_wizardSub",
			"wizardTitle": "tWNbIG_wizardTitle"
		};
		//#endregion
		//#region src/client/PluginManagerPage.tsx
		/**
		* Global plugin management: the Official group's cards for the bundles the
		* installation ships switched off and for the official plugins that register
		* their configuration, the Installed group's cards for the profile's bundles,
		* their row switches, the install dialog with its guide and folded pnpm
		* output, the uninstall confirmation, and the toasts an action's outcome
		* becomes. A bundle's page lists the rows it contributes as the Host runs
		* them; a plugin's configuration renders on its own page through the slots
		* the page declares.
		*/
		/** How long the list marks a package an install just enabled. */
		const HIGHLIGHT_MS = 2400;
		/** Built-in profile bundles stay out of this page even when the profile declares them as dependencies. */
		const BUILTIN_PROFILE_BUNDLES = new Set([
			"@deepseek-ai/dsh-base",
			"@deepseek-ai/dsh-web-app",
			"@deepseek-ai/dsh-headless",
			"@deepseek-ai/dsh-sdk-app",
			"@deepseek-ai/dsh-acp-app",
			"@deepseek-ai/dsh-sdk-minimal"
		]);
		/** How long a toast holds: long enough to read a failure that names what broke. */
		function toastHoldMs(text) {
			return Math.min(8e3, Math.max(3e3, text.length * 80));
		}
		const PHASE_KEYS = {
			pending: "rowPhasePending",
			loading: "rowPhaseLoading",
			active: "rowPhaseActive",
			failed: "rowPhaseFailed",
			unloading: "rowPhaseUnloading"
		};
		/** Status dot naming a root-fiber phase; loading and unloading are live transitions. */
		const PHASE_STATES = {
			pending: "idle",
			loading: "ongoing",
			active: "done",
			failed: "error",
			unloading: "ongoing"
		};
		/** The count line over a pack's components: the total, then only the states that occur. */
		function partsSummary(rows, t) {
			const failed = rows.filter((row) => row.phase === "failed").length;
			const off = rows.filter((row) => !row.enabled).length;
			const running = rows.filter((row) => row.enabled && row.phase === "active").length;
			return [
				t("partsCountTotal", { count: String(rows.length) }),
				...running > 0 ? [t("partsCountRunning", { count: String(running) })] : [],
				...off > 0 ? [t("partsCountOff", { count: String(off) })] : [],
				...failed > 0 ? [t("partsCountFailed", { count: String(failed) })] : []
			].join(" · ");
		}
		/** Rows beyond this count get a filter box above the list. */
		const ROW_FILTER_THRESHOLD = 10;
		/** The size the 36-viewBox plugin artwork renders at inside a card's 48px frame. */
		const CARD_ARTWORK_SIZE = 36;
		/** The size the artwork renders at inside a row's 40px frame. */
		const ROW_ARTWORK_SIZE = 30;
		/** The artwork of the official plugins that registered their configuration, by registration id. */
		const ITEM_ARTWORK = new Map([
			["shell", _deepseek_ai_dsh_client_ui_primitives.PluginArtworkTerminal],
			["agent-loop", _deepseek_ai_dsh_client_ui_primitives.PluginArtworkLoop],
			["subagent", _deepseek_ai_dsh_client_ui_primitives.PluginArtworkSubagent],
			["web-search", _deepseek_ai_dsh_client_ui_primitives.PluginArtworkSearch]
		]);
		/** An official plugin's card and page artwork; plugins without their own get the default. */
		function itemArtwork(id) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ITEM_ARTWORK.get(id) ?? _deepseek_ai_dsh_client_ui_primitives.PluginArtworkDefault, { size: CARD_ARTWORK_SIZE });
		}
		/** Manifest images remain isolated from the page DOM; a failed decode keeps the position's default artwork. */
		function PackageArtwork({ src, row = false, size = row ? ROW_ARTWORK_SIZE : CARD_ARTWORK_SIZE }) {
			const [failedSource, setFailedSource] = (0, react.useState)();
			return src === void 0 || src === failedSource ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(row ? _deepseek_ai_dsh_client_ui_primitives.PluginArtworkSubagent : _deepseek_ai_dsh_client_ui_primitives.PluginArtworkDefault, { size }) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("img", {
				className: PluginManagerPage_module_css_default.packageImage,
				src,
				width: size,
				height: size,
				alt: "",
				onError: () => {
					setFailedSource(src);
				}
			});
		}
		/** A row's switch: locked, saying why, when the Host refuses to address the row through the profile patch. */
		function RowSwitch({ row, title, t, busy, onChange }) {
			const locked = row.readOnlyReason !== void 0 || row.entryId === void 0;
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Switch, {
				checked: row.enabled,
				label: t("partToggle", { name: title }),
				disabled: busy || locked,
				...row.readOnlyReason === void 0 ? {} : { title: managementText({ code: row.readOnlyReason }, t) },
				onChange
			});
		}
		/** What a row's state line says: off, or the phase its fiber is in. */
		function rowStateText(row, t) {
			if (!row.enabled) return t("partOff");
			return row.phase === null ? t("rowStateIdle") : t(PHASE_KEYS[row.phase]);
		}
		/** The dot beside a row: its fiber phase, or idle. */
		function rowDotState(row) {
			if (!row.enabled || row.phase === null) return "idle";
			return PHASE_STATES[row.phase];
		}
		/** A Host metadata diagnostic does not change the package's management permissions. */
		function MetadataError({ error, t }) {
			return error === void 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
				className: PluginManagerPage_module_css_default.reason,
				role: "status",
				"data-package-meta-error": true,
				children: t("metadataError", { error })
			});
		}
		/**
		* A pack's rows as a list in the order the pack declares them: a state dot,
		* the row id, one line saying its state, a configure control for a row that
		* registered a page, and, when the pack is on, a switch. A pack like base
		* carries close to a hundred rows, so a long list gets a filter.
		*/
		function RowsSection({ rows, t, resolveText, toggle, configure }) {
			const [filter, setFilter] = (0, react.useState)("");
			const query = filter.trim().toLowerCase();
			const localized = rows.map((row) => ({
				row,
				...rowText(row, resolveText)
			}));
			const shown = query === "" ? localized : localized.filter(({ row, title, description }) => [
				title,
				description,
				row.rowId,
				row.moduleName
			].some((value) => value?.toLowerCase().includes(query)));
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
				className: PluginManagerPage_module_css_default.detailSection,
				"data-plugin-rows": true,
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: PluginManagerPage_module_css_default.sectionHead,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("h4", {
							className: PluginManagerPage_module_css_default.sectionTitle,
							children: t("partsLabel")
						}), rows.length === 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: PluginManagerPage_module_css_default.sectionCount,
							children: partsSummary(rows, t)
						})]
					}),
					rows.length === 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
						className: PluginManagerPage_module_css_default.status,
						children: t("partsEmpty")
					}) : null,
					rows.length > ROW_FILTER_THRESHOLD ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Input, {
						type: "search",
						className: PluginManagerPage_module_css_default.partsFilter,
						placeholder: t("partsFilter"),
						"aria-label": t("partsFilter"),
						value: filter,
						onChange: (event) => {
							setFilter(event.target.value);
						}
					}) : null,
					rows.length > 0 && shown.length === 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
						className: PluginManagerPage_module_css_default.status,
						children: t("partsFilterEmpty")
					}) : null,
					shown.length === 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("ul", {
						className: PluginManagerPage_module_css_default.rows,
						children: shown.map(({ row, title, description }) => /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("li", {
							className: PluginManagerPage_module_css_default.row,
							"data-plugin-row": row.entryId ?? row.rowId,
							...row.phase === "failed" ? { "data-state": "failed" } : row.enabled ? {} : { "data-state": "off" },
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
								className: PluginManagerPage_module_css_default.rowLine,
								children: [
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
										className: PluginManagerPage_module_css_default.rowIcon,
										"aria-hidden": "true",
										children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(PackageArtwork, {
											src: row.meta?.icon,
											row: true
										}, row.meta?.icon)
									}),
									/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
										className: PluginManagerPage_module_css_default.rowMain,
										children: [
											configure?.has(row) === true ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
												type: "button",
												className: PluginManagerPage_module_css_default.rowOpen,
												"aria-label": t("configureRow", { name: title }),
												onClick: () => {
													configure.open(row);
												},
												children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
													className: PluginManagerPage_module_css_default.rowId,
													children: title
												}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronRightOutlineRegular, {
													className: PluginManagerPage_module_css_default.rowOpenIcon,
													"aria-hidden": "true"
												})]
											}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
												className: PluginManagerPage_module_css_default.rowId,
												children: title
											}),
											description === void 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
												className: PluginManagerPage_module_css_default.rowModule,
												children: description
											}),
											title === row.rowId ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("code", {
												className: PluginManagerPage_module_css_default.rowModule,
												children: row.rowId
											}),
											title === row.moduleName ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("code", {
												className: PluginManagerPage_module_css_default.rowModule,
												children: row.moduleName
											})
										]
									}),
									/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
										className: PluginManagerPage_module_css_default.rowState,
										children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, { state: rowDotState(row) }), rowStateText(row, t)]
									}),
									toggle === void 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(RowSwitch, {
										row,
										title,
										t,
										busy: toggle.busy(row),
										onChange: (enabled) => {
											toggle.onSetEnabled(row, enabled);
										}
									})
								]
							}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(MetadataError, {
								error: row.meta?.error,
								t
							})]
						}, row.rowId))
					})
				]
			});
		}
		/**
		* A bundle's enable switch on its card and its page: locked, saying why, for
		* one the Host protects; off and locked for one it cannot read.
		*/
		function EnableSwitch({ pkg, title, t, busy, onSetEnabled }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Switch, {
				checked: pkg.enabled,
				label: t("enableToggle", { name: title }),
				disabled: busy || pkg.readOnlyReason !== void 0 || !pkg.enabled && pkg.error !== void 0,
				...pkg.readOnlyReason === void 0 ? {} : { title: managementText({ code: pkg.readOnlyReason }, t) },
				onChange: onSetEnabled
			});
		}
		/** The status one card carries: running, off, or a problem the Host reported. */
		function packageStatus(pkg) {
			if (pkg.error !== void 0) return "problem";
			return pkg.enabled ? "running" : "disabled";
		}
		/** The head every card shares: the artwork, the name that opens the page beside its tags, its one-liner, and what sits at the end. */
		function CardHead({ title, t, onOpen, icon, tags, description, end }) {
			const descriptionId = (0, react.useId)();
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: PluginManagerPage_module_css_default.cardHead,
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: PluginManagerPage_module_css_default.cardIcon,
						"aria-hidden": "true",
						children: icon
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: PluginManagerPage_module_css_default.cardMain,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							className: PluginManagerPage_module_css_default.titleRow,
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
								type: "button",
								className: `${PluginManagerPage_module_css_default.cardTitle} ${PluginManagerPage_module_css_default.cardOpen}`,
								"aria-label": t("openDetail", { name: title }),
								"aria-describedby": description === void 0 ? void 0 : descriptionId,
								onClick: onOpen,
								children: title
							}), tags]
						}), description === void 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: PluginManagerPage_module_css_default.cardDesc,
							id: descriptionId,
							children: description
						})]
					}),
					end === void 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						className: PluginManagerPage_module_css_default.cardEnd,
						children: end
					})
				]
			});
		}
		/** The top every page shares: the crumb that leads back, then the icon with the page's actions at its right. */
		function DetailTop({ crumbLabel, crumbText, onBack, icon, actions }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: PluginManagerPage_module_css_default.detailTop,
				"data-window-drag": true,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
					type: "button",
					className: PluginManagerPage_module_css_default.crumb,
					"aria-label": crumbLabel,
					onClick: onBack,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronDownOutlineRegular, {
						className: PluginManagerPage_module_css_default.crumbIcon,
						"aria-hidden": "true"
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: crumbText })]
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: PluginManagerPage_module_css_default.detailHead,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: PluginManagerPage_module_css_default.cardIcon,
						"aria-hidden": "true",
						children: icon
					}), actions]
				})]
			});
		}
		/** One package as a card that opens its page: its name, its one-liner, its tags, and its bundle switch. */
		function PackageCard({ pkg, t, resolveText, busy, highlighted, onOpen, onSetEnabled }) {
			const { title, description, beta } = packageText(pkg, resolveText);
			const status = packageStatus(pkg);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("li", {
				className: `${PluginManagerPage_module_css_default.card} ${PluginManagerPage_module_css_default.cardLink}`,
				"data-plugin-package": pkg.name,
				"data-plugin-status": status,
				...highlighted ? { "data-plugin-highlight": "" } : {},
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(CardHead, {
					title,
					t,
					onOpen,
					icon: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(PackageArtwork, { src: pkg.meta?.icon }, pkg.meta?.icon),
					tags: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [beta ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tag, {
						className: PluginManagerPage_module_css_default.statusTag,
						tone: "info",
						children: t("statusBeta")
					}) : null, status === "problem" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tag, {
						className: PluginManagerPage_module_css_default.statusTag,
						tone: "danger",
						children: t("statusProblem")
					}) : null] }),
					description,
					end: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(EnableSwitch, {
						pkg,
						title,
						t,
						busy,
						onSetEnabled
					})
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(MetadataError, {
					error: pkg.meta?.error,
					t
				})]
			});
		}
		/**
		* One official plugin as a card that opens its page: its icon, its title from
		* the registration, and the one-liner the entry renders in its summary view.
		*/
		function ItemCard({ item, t, onOpen, renderSlot }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("li", {
				className: `${PluginManagerPage_module_css_default.card} ${PluginManagerPage_module_css_default.cardLink}`,
				"data-plugin-item": item.id,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(CardHead, {
					title: item.label,
					t,
					onOpen,
					icon: itemArtwork(item.id),
					description: renderSlot("plugins.item", { view: "summary" }, { only: item.id })
				})
			});
		}
		/** One row as the detail slots see it. */
		function rowRef(row) {
			return {
				rowId: row.rowId,
				moduleName: row.moduleName,
				enabled: row.enabled
			};
		}
		/** One bundle as the detail slots see it. */
		function packageRef(pkg) {
			return {
				name: pkg.name,
				...pkg.version === void 0 ? {} : { version: pkg.version },
				installed: pkg.installed,
				enabled: pkg.enabled,
				rows: pkg.rows.map(rowRef)
			};
		}
		/**
		* An official plugin's page: the crumb back to the cards, its icon with the
		* contributed actions, its title with the contributed badges over its
		* one-liner, the form the entry renders, and the contributed sections.
		*/
		function ItemDetail({ item, t, onBack, renderSlot, form }) {
			const subject = {
				kind: "item",
				id: item.id
			};
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: PluginManagerPage_module_css_default.detail,
				"data-plugin-item-detail": item.id,
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)(DetailTop, {
						crumbLabel: t("backToList"),
						crumbText: t("crumbRoot"),
						onBack,
						icon: itemArtwork(item.id),
						actions: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: PluginManagerPage_module_css_default.detailActions,
							children: renderSlot("plugins.detail.actions", { subject })
						})
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: PluginManagerPage_module_css_default.detailMain,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							className: PluginManagerPage_module_css_default.titleRow,
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("h3", {
								className: PluginManagerPage_module_css_default.detailTitle,
								children: item.label
							}), renderSlot("plugins.detail.badge", { subject })]
						}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
							className: PluginManagerPage_module_css_default.detailDesc,
							children: renderSlot("plugins.item", { view: "summary" }, { only: item.id })
						})]
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: PluginManagerPage_module_css_default.detailSections,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("section", {
							className: PluginManagerPage_module_css_default.detailSection,
							"data-plugin-config": true,
							children: renderSlot("plugins.item", {
								view: "page",
								form
							}, { only: item.id })
						}), renderSlot("plugins.detail.section", { subject })]
					})
				]
			});
		}
		/**
		* A row's configuration page keeps its technical identity beside local package
		* text and the form supplied by its configuration entry.
		*/
		function RowDetail({ pkg, row, t, resolveText, onBack, renderSlot, form }) {
			const { title } = packageText(pkg, resolveText);
			const { title: rowTitle, description } = rowText(row, resolveText);
			const key = rowConfigKey(pkg.name, row.rowId);
			const subject = {
				kind: "row",
				pkg: packageRef(pkg),
				row: rowRef(row)
			};
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: PluginManagerPage_module_css_default.detail,
				"data-plugin-row-detail": key,
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)(DetailTop, {
						crumbLabel: t("backToPackage", { name: title }),
						crumbText: title,
						onBack,
						icon: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(PackageArtwork, {
							src: row.meta?.icon,
							row: true,
							size: CARD_ARTWORK_SIZE
						}, row.meta?.icon),
						actions: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: PluginManagerPage_module_css_default.detailActions,
							children: renderSlot("plugins.detail.actions", { subject })
						})
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: PluginManagerPage_module_css_default.detailMain,
						children: [
							/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
								className: PluginManagerPage_module_css_default.titleRow,
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("h3", {
									className: PluginManagerPage_module_css_default.detailTitle,
									children: rowTitle
								}), renderSlot("plugins.detail.badge", { subject })]
							}),
							rowTitle === row.rowId ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
								className: PluginManagerPage_module_css_default.detailName,
								children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("code", { children: row.rowId })
							}),
							/* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
								className: PluginManagerPage_module_css_default.detailName,
								children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("code", { children: row.moduleName })
							}),
							/* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
								className: PluginManagerPage_module_css_default.detailDesc,
								children: description ?? renderSlot("plugins.row.config", { view: "summary" }, { entryKey: key })
							})
						]
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)(MetadataError, {
						error: row.meta?.error,
						t
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: PluginManagerPage_module_css_default.detailSections,
						"data-plugin-config": true,
						children: [renderSlot("plugins.row.config", {
							view: "page",
							form
						}, { entryKey: key }), renderSlot("plugins.detail.section", { subject })]
					})
				]
			});
		}
		/**
		* One package's page: the crumb back to the list; its icon with its switch
		* and, for a package the profile installed, uninstall; its title beside its
		* version tag, its beta tag, and its problem tag; the package name the title
		* stands for, which is what installs it elsewhere; its one-liner; the Host's
		* problem when it reports one; the configuration the bundle registered for
		* itself; and its rows with their switches and configure controls.
		*/
		function PackageDetail({ pkg, t, resolveText, busy, rowBusy, configured, configure, renderSlot, onBack, onSetEnabled, onUninstall, onSetRowEnabled }) {
			const { title, description, beta } = packageText(pkg, resolveText);
			const status = packageStatus(pkg);
			const subject = {
				kind: "bundle",
				pkg: packageRef(pkg)
			};
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: PluginManagerPage_module_css_default.detail,
				"data-plugin-detail": pkg.name,
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)(DetailTop, {
						crumbLabel: t("backToList"),
						crumbText: t("crumbRoot"),
						onBack,
						icon: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(PackageArtwork, { src: pkg.meta?.icon }, pkg.meta?.icon),
						actions: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							className: PluginManagerPage_module_css_default.detailActions,
							children: [
								renderSlot("plugins.detail.actions", { subject }),
								pkg.installed ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
									variant: "outline",
									size: "sm",
									className: PluginManagerPage_module_css_default.danger,
									icon: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconTrashOutlineRegular, { size: 13 }),
									"aria-label": t("uninstallLabel", { name: title }),
									disabled: busy || pkg.readOnlyReason !== void 0,
									onClick: onUninstall,
									children: t("uninstall")
								}) : null,
								/* @__PURE__ */ (0, react_jsx_runtime.jsx)(EnableSwitch, {
									pkg,
									title,
									t,
									busy,
									onSetEnabled
								})
							]
						})
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: PluginManagerPage_module_css_default.detailMain,
						children: [
							/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
								className: PluginManagerPage_module_css_default.titleRow,
								children: [
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)("h3", {
										className: PluginManagerPage_module_css_default.detailTitle,
										children: title
									}),
									pkg.version === void 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tag, {
										className: PluginManagerPage_module_css_default.versionTag,
										tone: "neutral",
										children: t("versionTag", { version: pkg.version })
									}),
									beta ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tag, {
										className: PluginManagerPage_module_css_default.statusTag,
										tone: "info",
										children: t("statusBeta")
									}) : null,
									status === "problem" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Tag, {
										className: PluginManagerPage_module_css_default.statusTag,
										tone: "danger",
										children: t("statusProblem")
									}) : null,
									renderSlot("plugins.detail.badge", { subject })
								]
							}),
							/* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
								className: PluginManagerPage_module_css_default.detailName,
								children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("code", {
									"data-plugin-name": true,
									children: pkg.name
								})
							}),
							description === void 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
								className: PluginManagerPage_module_css_default.detailDesc,
								children: description
							})
						]
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)(MetadataError, {
						error: pkg.meta?.error,
						t
					}),
					pkg.error === void 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("p", {
						className: PluginManagerPage_module_css_default.reason,
						role: "status",
						children: [
							t("reasonLabel"),
							": ",
							managementText(pkg.error, t)
						]
					}),
					pkg.readOnlyReason === void 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
						className: PluginManagerPage_module_css_default.reason,
						role: "status",
						children: managementText({ code: pkg.readOnlyReason }, t)
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: PluginManagerPage_module_css_default.detailSections,
						children: [
							configured ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("section", {
								className: PluginManagerPage_module_css_default.detailSection,
								"data-plugin-config": true,
								children: renderSlot("plugins.bundle.config", { view: "page" }, { entryKey: pkg.name })
							}) : null,
							/* @__PURE__ */ (0, react_jsx_runtime.jsx)(RowsSection, {
								rows: pkg.rows,
								t,
								resolveText,
								toggle: pkg.enabled ? {
									busy: (row) => busy || rowBusy(row),
									onSetEnabled: onSetRowEnabled
								} : void 0,
								configure
							}),
							renderSlot("plugins.detail.section", { subject })
						]
					})
				]
			});
		}
		/** Output lines an install run's terminal shows before its middle folds: the first and last six of a long pnpm log. */
		const INSTALL_TERMINAL_LINES = 12;
		/** The install terminal's display copy, from the tab's dictionary. */
		function terminalLabels(t) {
			return {
				/* v8 ignore next -- the Host reports a killed pnpm as a null exit code, never a signal name; the label interface needs one */
				signal: (signal) => t("terminalSignal", { signal }),
				exitCode: (code) => t("terminalExitCode", { code: String(code) }),
				noExitCode: t("terminalNoExitCode"),
				running: t("terminalRunning"),
				failed: t("terminalFailed"),
				done: t("terminalDone"),
				copy: t("terminalCopy"),
				copied: t("terminalCopied"),
				noOutput: t("terminalNoOutput"),
				collapseAria: t("terminalCollapseAria"),
				collapse: t("terminalCollapse"),
				expandAria: (hidden) => t("terminalExpandAria", { n: String(hidden) }),
				expand: (hidden) => t("terminalExpand", { n: String(hidden) })
			};
		}
		/** The sentence under the field for a spec the check refused. */
		const INPUT_PROBLEM_KEYS = {
			"invalid-spec": "installProblemInvalid",
			"already-installed": "installProblemInstalled",
			"not-found": "installProblemNotFound",
			"not-a-package": "installProblemNotPackage",
			"not-a-bundle": "installProblemNotBundle",
			"network": "installProblemNetwork",
			"unknown": "installProblemUnknown"
		};
		/** The spec forms the install guide shows, each with an example the person can drop into the field. */
		const GUIDE_EXAMPLES = [
			{
				key: "id",
				titleKey: "installGuideIdTitle",
				exampleKey: "installGuideIdExample",
				hintKey: "installGuideIdHint"
			},
			{
				key: "git",
				titleKey: "installGuideGitTitle",
				exampleKey: "installGuideGitExample",
				hintKey: "installGuideGitHint"
			},
			{
				key: "path",
				titleKey: "installGuidePathTitle",
				exampleKey: "installGuidePathExample",
				hintKey: "installGuidePathHint"
			}
		];
		/** The one-line reading of a classified pnpm failure. */
		const FAILURE_KIND_KEYS = {
			"pnpm-missing": "installFailurePnpmMissing",
			"timeout": "installFailureTimeout",
			"not-found": "installFailureNotFound",
			"no-matching-version": "installFailureNoMatchingVersion",
			"network": "installFailureNetwork",
			"disk-full": "installFailureDiskFull",
			"permission": "installFailurePermission",
			"build-blocked": "installFailureBuildBlocked",
			"integrity": "installFailureIntegrity",
			"unknown": "installFailureGeneric"
		};
		/** The heading of each screen past the spec. */
		const SCREEN_TITLE_KEYS = {
			starting: "installStarting",
			running: "installingTitle",
			cancelling: "installCancelling",
			applying: "installApplying",
			unconfirmed: "installUnconfirmedTitle",
			unknown: "installUnknownTitle",
			done: "installedTitle",
			failed: "installFailedTitle"
		};
		/** What the spec's kind reads as when the package carries no description of its own. */
		const SUBJECT_KIND_KEYS = {
			registry: void 0,
			path: "installSubjectPath",
			git: "installSubjectGit",
			tarball: "installSubjectTarball"
		};
		/** The registries asked, by name, in the dictionary's list form. */
		function registryList(registries, t, resolved) {
			return registries.map((registry) => registryText(registry, t, resolved).name).join(t("registryListSeparator"));
		}
		/** An option's label: the registry's name with the host it names, unless the host is the name. */
		function registryOption(registry, t, resolved) {
			const { name, host } = registryText(registry, t, resolved);
			return name === host ? name : t("registryWithHost", {
				name,
				host
			});
		}
		/**
		* The failed screen's one line: a pnpm failure by its kind, a refusal by its
		* code, any other failure in the Host's words; the run's output stays behind the details.
		* A run the Host laid at the spec's own host says so; one it laid at a registry
		* names every registry asked when there were several.
		*/
		function failureText(failure, t, install) {
			if (failure === null) return t("installFailureGeneric");
			if (failure.code === "incompatible-version") return managementText({
				code: failure.code,
				...failure.incompatible === void 0 ? {} : { incompatible: failure.incompatible }
			}, t);
			if (failure.kind === "build-blocked" && !failure.pendingBuilds?.length) return t("installFailureBuildBlockedManual");
			const host = install?.subject?.host;
			if (failure.failedAt === "spec-host" && host !== void 0) return t("installFailureNetworkHost", { host });
			const asked = install?.attempts?.registries ?? [];
			if (failure.failedAt === "registry" && (failure.kind === "network" || failure.kind === "timeout") && asked.length > 1) return t("installFailureNetworkAll", { registries: registryList(asked, t, install?.registries?.resolved ?? null) });
			if (failure.kind !== void 0) return t(FAILURE_KIND_KEYS[failure.kind]);
			if (failure.code !== void 0) return managementText({
				code: failure.code,
				diagnostic: failure.reason
			}, t);
			return failure.reason === "" ? t("installFailureGeneric") : failure.reason;
		}
		/** The package the install is about: its name, one-liner, and version, as the Host read them before installing. */
		function SubjectCard({ subject, t }) {
			const title = subject.name ?? subject.spec;
			const kindKey = SUBJECT_KIND_KEYS[subject.kind];
			const description = subject.description ?? (kindKey === void 0 ? void 0 : t(kindKey));
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: PluginManagerPage_module_css_default.subject,
				"data-install-subject": subject.spec,
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
						className: PluginManagerPage_module_css_default.subjectName,
						children: title
					}),
					description === void 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
						className: PluginManagerPage_module_css_default.subjectDesc,
						children: description
					}),
					subject.version === void 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
						className: PluginManagerPage_module_css_default.subjectMeta,
						children: t("installVersion", { version: subject.version })
					})
				]
			});
		}
		/**
		* The install dialog: the spec and its check, then the installing, installed,
		* and failed screens over the same subject card. A failed run that left
		* install scripts undecided shows them for approval in place of plain retry.
		*/
		function InstallDialog({ install, t, onClose, onEditSpec, onRun, onCancel, onReconcile, onToggleDetails, onEnableNow, onApproveBuilds, onToggleRegistry, onChooseRegistry, onChangeRegistry, onUseGithubMirror }) {
			const errorId = (0, react.useId)();
			const guideId = (0, react.useId)();
			const approvalId = (0, react.useId)();
			const registryId = (0, react.useId)();
			const registryErrorId = (0, react.useId)();
			const [guideOpen, setGuideOpen] = (0, react.useState)(false);
			const { phase } = install;
			const registryToggleRef = (0, react.useRef)(null);
			const registryPanelRef = (0, react.useRef)(null);
			const registryShown = install.registryOpen && phase === "idle";
			const registryPosition = (0, _deepseek_ai_dsh_client_ui_primitives.useAnchoredPosition)({
				open: registryShown,
				anchorRef: registryToggleRef,
				panelRef: registryPanelRef,
				align: "end",
				gap: 6,
				margin: 12
			});
			(0, _deepseek_ai_dsh_client_ui_primitives.useDismissOnOutsidePointer)(registryToggleRef, registryShown, onToggleRegistry, registryPanelRef);
			(0, react.useEffect)(() => {
				if (!registryShown) return;
				const onKeyDown = (event) => {
					if (event.key !== "Escape") return;
					event.stopPropagation();
					onToggleRegistry();
				};
				document.addEventListener("keydown", onKeyDown, true);
				return () => {
					document.removeEventListener("keydown", onKeyDown, true);
				};
			}, [registryShown, onToggleRegistry]);
			if (githubRecoveryRegistry(install) !== void 0) {
				const anotherWay = asksMirror(install);
				return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Modal, {
					open: install.open,
					onClose,
					title: t(install.failure?.kind === "timeout" ? "installGithubTimeoutTitle" : "installGithubFailedTitle"),
					closeLabel: t("close"),
					description: t("installGithubFailedDescription"),
					footer: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
						variant: "outline",
						onClick: onClose,
						children: t("cancel")
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
						variant: "primary",
						autoFocus: true,
						onClick: () => {
							if (anotherWay) setGuideOpen(true);
							onUseGithubMirror();
						},
						children: t(anotherWay ? "installTryAnotherWay" : "installUseGithubMirror")
					})] })
				});
			}
			if (phase === "idle" || phase === "checking") {
				const checking = phase === "checking";
				const empty = install.spec.trim() === "";
				const choice = install.registry;
				const resolved = install.registries?.resolved ?? null;
				const chosenTitle = choice.kind === "custom" ? t("registryCustom") : registryText(choice.registry, t, resolved).name;
				const inputProblem = install.inputError;
				const askedByCheck = inputProblem?.registries ?? [];
				const inputSentence = inputProblem === null ? null : inputProblem.problem === "network" && askedByCheck.length > 1 ? t("installProblemNetworkAll", { registries: registryList(askedByCheck, t, resolved) }) : t(INPUT_PROBLEM_KEYS[inputProblem.problem], { reason: inputProblem.reason });
				return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Modal, {
					open: install.open,
					onClose,
					title: t("installTitle"),
					closeLabel: t("close"),
					...install.mirrorRecovery ? {} : { description: t("installDescription") },
					className: PluginManagerPage_module_css_default.installDialog,
					contentClassName: PluginManagerPage_module_css_default.installContent,
					footer: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(_deepseek_ai_dsh_client_ui_primitives.Button, {
						variant: "primary",
						className: PluginManagerPage_module_css_default.wide,
						disabled: checking || empty,
						"aria-busy": checking,
						onClick: onRun,
						children: [checking ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, { state: "ongoing" }) : null, t(checking ? "installChecking" : "installRun")]
					}),
					children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: PluginManagerPage_module_css_default.installBody,
						children: [
							/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
								className: PluginManagerPage_module_css_default.installField,
								children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("input", {
									type: "text",
									autoFocus: install.mirrorRecovery === true,
									value: install.spec,
									placeholder: t("installSpecPlaceholder"),
									disabled: checking,
									"aria-label": t(install.mirrorRecovery ? "installPackageLabel" : "installSpecLabel"),
									"aria-invalid": install.inputError !== null,
									"aria-describedby": install.inputError === null ? void 0 : errorId,
									onChange: (event) => {
										onEditSpec(event.currentTarget.value);
									},
									onKeyDown: (event) => {
										if (event.key === "Enter" && !empty && !checking) onRun();
									}
								})
							}),
							inputSentence === null ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
								id: errorId,
								className: PluginManagerPage_module_css_default.inputError,
								role: "alert",
								children: inputSentence
							}),
							/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
								className: PluginManagerPage_module_css_default.optionsRow,
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
									type: "button",
									className: PluginManagerPage_module_css_default.guideToggle,
									"aria-expanded": guideOpen,
									"aria-controls": guideId,
									onClick: () => {
										setGuideOpen((open) => !open);
									},
									children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronDownOutlineRegular, {
										className: PluginManagerPage_module_css_default.guideChevron,
										"aria-hidden": "true"
									}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: t(guideOpen ? "installGuideHide" : "installGuideToggle") })]
								}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
									ref: registryToggleRef,
									type: "button",
									className: PluginManagerPage_module_css_default.registryToggle,
									"aria-expanded": install.registryOpen,
									"aria-controls": registryId,
									"aria-haspopup": "dialog",
									disabled: checking,
									onClick: onToggleRegistry,
									children: [
										/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: t("registryToggle") }),
										" ",
										/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
											className: PluginManagerPage_module_css_default.registryChosen,
											children: chosenTitle
										}),
										/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronDownOutlineRegular, {
											className: PluginManagerPage_module_css_default.guideChevron,
											"aria-hidden": "true"
										})
									]
								})]
							}),
							guideOpen ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
								id: guideId,
								className: PluginManagerPage_module_css_default.guide,
								"data-install-guide": true,
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("ol", {
									className: PluginManagerPage_module_css_default.guideList,
									children: GUIDE_EXAMPLES.map(({ key, titleKey, exampleKey, hintKey }, index) => /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("li", {
										className: PluginManagerPage_module_css_default.guideItem,
										children: [
											/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
												className: PluginManagerPage_module_css_default.guideIndex,
												"aria-hidden": "true",
												children: index + 1
											}),
											/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
												className: PluginManagerPage_module_css_default.guideMain,
												children: [
													/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
														className: PluginManagerPage_module_css_default.guideTitle,
														children: t(titleKey)
													}),
													/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
														className: PluginManagerPage_module_css_default.guideHint,
														children: t(hintKey)
													}),
													/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
														className: PluginManagerPage_module_css_default.guideExample,
														children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
															className: PluginManagerPage_module_css_default.guideExampleLabel,
															children: t("installGuideExampleLabel")
														}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("code", { children: t(exampleKey) })]
													})
												]
											}),
											/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
												variant: "outline",
												size: "sm",
												"aria-label": t("installGuideFillAria", { example: t(exampleKey) }),
												disabled: checking,
												onClick: () => {
													onEditSpec(t(exampleKey));
												},
												children: t("installGuideFill")
											})
										]
									}, key))
								}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("p", {
									className: PluginManagerPage_module_css_default.guideSafety,
									role: "note",
									children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconWarningOutlineRegular, {
										size: 14,
										"aria-hidden": "true"
									}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: t("installGuideSafety") })]
								})]
							}) : null,
							registryShown ? (0, react_dom.createPortal)(/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("fieldset", {
								ref: registryPanelRef,
								id: registryId,
								className: PluginManagerPage_module_css_default.registry,
								style: registryPosition ?? {
									visibility: "hidden",
									left: 0,
									top: 0
								},
								"data-install-registry": true,
								"aria-label": t("registryLegend"),
								children: [offeredRegistries(install.registries).map((registry) => {
									const checked = choice.kind === "offered" && choice.registry === registry;
									return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("label", {
										className: PluginManagerPage_module_css_default.registryOption,
										"data-checked": checked,
										children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("input", {
											type: "radio",
											name: registryId,
											checked,
											onChange: () => {
												onChooseRegistry({
													kind: "offered",
													registry
												});
											}
										}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
											className: PluginManagerPage_module_css_default.registryTitle,
											children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: registryOption(registry, t, resolved) })
										})]
									}, registry ?? "");
								}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
									className: PluginManagerPage_module_css_default.registryOption,
									"data-checked": choice.kind === "custom",
									children: [
										/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("label", {
											className: PluginManagerPage_module_css_default.registryCustomPick,
											children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("input", {
												type: "radio",
												name: registryId,
												checked: choice.kind === "custom",
												onChange: () => {
													onChooseRegistry({
														kind: "custom",
														url: ""
													});
												}
											}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
												className: PluginManagerPage_module_css_default.registryTitle,
												children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: t("registryCustom") })
											})]
										}),
										/* @__PURE__ */ (0, react_jsx_runtime.jsx)("input", {
											type: "text",
											className: PluginManagerPage_module_css_default.registryCustomField,
											"aria-label": t("registryCustom"),
											placeholder: t("registryCustomPlaceholder"),
											value: choice.kind === "custom" ? choice.url : "",
											disabled: choice.kind !== "custom",
											"aria-invalid": install.registryError,
											"aria-describedby": install.registryError ? registryErrorId : void 0,
											onChange: (event) => {
												onChooseRegistry({
													kind: "custom",
													url: event.currentTarget.value
												});
											},
											onKeyDown: (event) => {
												if (event.key === "Enter" && !empty) onRun();
											}
										}),
										install.registryError ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
											id: registryErrorId,
											className: PluginManagerPage_module_css_default.inputError,
											role: "alert",
											children: t("registryCustomInvalid")
										}) : null,
										/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
											className: PluginManagerPage_module_css_default.registryHint,
											children: t("registryCustomHint")
										})
									]
								})]
							}), document.body) : null
						]
					})
				});
			}
			const heading = t(SCREEN_TITLE_KEYS[phase]);
			const pending = isInstallPending(phase);
			const cancellable = phase === "starting" || phase === "running" || phase === "unconfirmed";
			const stoppable = cancellable || phase === "failed" || phase === "unknown";
			const failure = install.failure;
			const uncertainty = failure?.uncertainty;
			const uncertaintyText = failure?.uncertainty === void 0 ? null : t({
				result: "installResultUnconfirmed",
				cancellation: phase === "applying" ? "installApplyingCancellationError" : "installCancelUnconfirmed",
				acceptance: "installAwaitingAcceptance"
			}[failure.uncertainty], { reason: failure.reason });
			const pendingBuilds = phase === "failed" ? install.failure?.pendingBuilds ?? [] : [];
			const approvable = pendingBuilds.length > 0;
			const firstRun = install.runs[0];
			const asked = install.attempts !== null && install.attempts.registries.length > 1 ? install.attempts : null;
			const current = asked === null ? void 0 : asked.registries.at(-1);
			const previous = asked === null ? void 0 : asked.registries.at(-2);
			const resolved = install.registries?.resolved ?? null;
			const attemptLine = pending && asked !== null && current !== void 0 && previous !== void 0 ? t("installAttempt", {
				previous: registryText(previous, t, resolved).name,
				registry: registryText(current, t, resolved).name,
				index: String(asked.registries.length),
				total: String(asked.total)
			}) : null;
			const changeable = phase === "failed" && !approvable && install.failure?.failedAt === "registry";
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Modal, {
				open: install.open,
				onClose,
				title: heading,
				headless: true,
				className: PluginManagerPage_module_css_default.installDialog,
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: PluginManagerPage_module_css_default.wizard,
					"data-install-phase": phase,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: PluginManagerPage_module_css_default.wizardHead,
						children: [phase === "done" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {}) : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
							type: "button",
							className: PluginManagerPage_module_css_default.wizardBack,
							"aria-label": t(pending ? "installCancelAndEdit" : "installEditAria"),
							disabled: !stoppable,
							onClick: onCancel,
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronLeftOutlineMedium, { "aria-hidden": "true" }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: t(pending ? "installCancelAndEdit" : "installEdit") })]
						}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
							type: "button",
							className: PluginManagerPage_module_css_default.wizardClose,
							"aria-label": t(cancellable ? "installCloseCancels" : "close"),
							onClick: onClose,
							children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconCloseOutlineMedium, { size: 14 })
						})]
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: PluginManagerPage_module_css_default.wizardScroll,
						children: [
							/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
								className: PluginManagerPage_module_css_default.wizardHero,
								children: [
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
										className: PluginManagerPage_module_css_default.wizardIcon,
										"data-state": pending ? "ongoing" : phase === "done" ? "done" : "error",
										"aria-hidden": "true",
										children: pending ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, {
											state: "ongoing",
											size: 28
										}) : phase === "done" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconCheckCircleFillRegular, { size: 28 }) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconWarningOutlineRegular, { size: 28 })
									}),
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)("h2", {
										className: PluginManagerPage_module_css_default.wizardTitle,
										role: phase === "failed" ? "alert" : "status",
										children: heading
									}),
									phase === "failed" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
										className: PluginManagerPage_module_css_default.wizardSub,
										children: failureText(install.failure, t, install)
									}) : null,
									attemptLine === null ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
										className: PluginManagerPage_module_css_default.wizardSub,
										children: attemptLine
									}),
									uncertaintyText === null ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
										className: PluginManagerPage_module_css_default.wizardSub,
										role: "alert",
										children: uncertaintyText
									}),
									phase === "unknown" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
										className: PluginManagerPage_module_css_default.wizardSub,
										children: t("installUnknownDescription")
									}) : null
								]
							}),
							install.subject === null ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(SubjectCard, {
								subject: install.subject,
								t
							}),
							approvable ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
								className: PluginManagerPage_module_css_default.approval,
								role: "group",
								"aria-labelledby": approvalId,
								"data-install-approval": true,
								children: [
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)("h3", {
										id: approvalId,
										className: PluginManagerPage_module_css_default.approvalTitle,
										children: t("installApprovalTitle")
									}),
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
										className: PluginManagerPage_module_css_default.approvalText,
										children: t("installApprovalDescription")
									}),
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)("ul", {
										className: PluginManagerPage_module_css_default.approvalList,
										children: pendingBuilds.map((name) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)("li", { children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("code", { children: name }) }, name))
									}),
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
										className: PluginManagerPage_module_css_default.approvalText,
										children: t("installApprovalConsequence")
									}),
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
										className: PluginManagerPage_module_css_default.approvalCaution,
										children: t("installApprovalCaution")
									}),
									/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
										variant: "primary",
										className: PluginManagerPage_module_css_default.wide,
										onClick: onApproveBuilds,
										children: t("installApproveAndRetry")
									})
								]
							}) : null,
							phase === "done" && install.installed === null ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
								className: PluginManagerPage_module_css_default.result,
								role: "status",
								children: t("installDoneNothing")
							}) : null,
							phase === "done" && install.restartRequired ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
								className: PluginManagerPage_module_css_default.resultWarn,
								role: "status",
								children: t("installDoneRestart")
							}) : null,
							phase === "done" && install.approvedBuilds.length > 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
								className: PluginManagerPage_module_css_default.result,
								role: "status",
								children: t("installDoneApproved", { names: install.approvedBuilds.join(", ") })
							}) : null,
							/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
								className: PluginManagerPage_module_css_default.wizardFoot,
								children: [
									/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
										type: "button",
										className: PluginManagerPage_module_css_default.detailsToggle,
										"aria-expanded": install.detailsOpen,
										onClick: onToggleDetails,
										children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { children: t(install.detailsOpen ? "installDetailsHide" : "installDetailsShow") }), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronDownOutlineRegular, {
											className: PluginManagerPage_module_css_default.detailsChevron,
											"aria-hidden": "true"
										})]
									}),
									uncertainty === "result" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
										variant: "outline",
										size: "sm",
										className: PluginManagerPage_module_css_default.footAction,
										onClick: onReconcile,
										children: t("installReconcile")
									}) : null,
									pending ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
										variant: "outline",
										size: "sm",
										className: PluginManagerPage_module_css_default.footAction,
										disabled: !cancellable,
										onClick: onCancel,
										children: t(phase === "cancelling" ? "installCancelling" : "installCancel")
									}) : null,
									phase === "failed" && !approvable ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
										className: PluginManagerPage_module_css_default.wizardActions,
										children: [changeable ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
											variant: "outline",
											size: "sm",
											className: PluginManagerPage_module_css_default.footAction,
											onClick: onChangeRegistry,
											children: t("installChangeRegistry")
										}) : null, /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
											variant: "primary",
											size: "sm",
											className: PluginManagerPage_module_css_default.footAction,
											onClick: onRun,
											children: t("installRetry")
										})]
									}) : null
								]
							}),
							install.detailsOpen ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
								className: PluginManagerPage_module_css_default.detailsBody,
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
									className: PluginManagerPage_module_css_default.installLocation,
									children: firstRun === void 0 ? t("terminalNoOutput") : t("installLocation", { dir: firstRun.cwd })
								}), install.runs.map((run, index) => {
									const registry = asked?.registries[index];
									return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
										className: PluginManagerPage_module_css_default.run,
										children: [registry === void 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
											className: PluginManagerPage_module_css_default.attemptBadge,
											children: t("installAttemptBadge", {
												index: String(index + 1),
												registry: registryText(registry, t, resolved).name
											})
										}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.TerminalBlock, {
											command: run.command,
											output: run.output,
											running: run.exitCode === void 0,
											exitCode: run.exitCode,
											maxLines: INSTALL_TERMINAL_LINES,
											labels: {
												...terminalLabels(t),
												...phase === "cancelling" ? { failed: t("installCancelledShort") } : {}
											},
											className: PluginManagerPage_module_css_default.terminal
										})]
									}, run.jobId);
								})]
							}) : null,
							phase !== "done" ? null : install.installed !== null ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
								variant: "primary",
								className: PluginManagerPage_module_css_default.wide,
								disabled: install.enabling,
								"aria-busy": install.enabling,
								onClick: onEnableNow,
								children: t("installEnableNow")
							}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
								variant: "primary",
								className: PluginManagerPage_module_css_default.wide,
								onClick: onClose,
								children: t("installClose")
							})
						]
					})]
				})
			});
		}
		/** The confirmation an uninstall waits on. */
		function ConfirmDialog({ name, t, onConfirm, onCancel }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Modal, {
				open: true,
				onClose: onCancel,
				title: t("confirmUninstallTitle", { name }),
				closeLabel: t("close"),
				description: t("confirmUninstallDescription"),
				footer: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
					variant: "outline",
					onClick: onCancel,
					children: t("cancel")
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
					variant: "primary",
					className: PluginManagerPage_module_css_default.dangerButton,
					onClick: onConfirm,
					children: t("confirmUninstall")
				})] })
			});
		}
		/** Render the plugin manager: the official plugins and installed bundles, their pages, the install dialog, and the confirmation. */
		function PluginManagerPage(props) {
			const { t, ensure, renderSlot, resolveText } = props;
			const configurations = props.useConfigurations((snapshot) => snapshot.view?.namespaces);
			const formFor = (id) => {
				if (!configurations?.some((view) => view.ns === id)) return void 0;
				const form = props.configForm(id);
				return {
					state: form.getSnapshot(),
					mutate: (ops, revision) => form.mutate(ops, revision)
				};
			};
			const state = props.usePluginManager((snapshot) => snapshot);
			const ledger = props.useConfigLedger((snapshot) => snapshot);
			const view = props.useStore((state) => state.view), { setView } = props.actions;
			const [activation, setActivation] = (0, react.useState)(null);
			(0, react.useEffect)(() => {
				ensure();
			}, [ensure]);
			const { highlight, clearHighlight } = {
				highlight: state.highlight,
				clearHighlight: props.clearHighlight
			};
			(0, react.useEffect)(() => {
				if (highlight === null) return;
				const card = document.querySelector(`[data-plugin-package="${highlight}"]`);
				if (card !== null && typeof card.scrollIntoView === "function") card.scrollIntoView({
					block: "center",
					behavior: "smooth"
				});
				const timer = setTimeout(clearHighlight, HIGHLIGHT_MS);
				return () => {
					clearTimeout(timer);
				};
			}, [highlight, clearHighlight]);
			const noticeLine = state.notice === null ? null : noticeText(state.notice, t);
			const listed = state.packages.filter((pkg) => !BUILTIN_PROFILE_BUNDLES.has(pkg.name) && (pkg.installed || pkg.optional || pkg.error !== void 0));
			const mine = listed.filter((pkg) => pkg.installed || !pkg.optional);
			const official = listed.filter((pkg) => pkg.optional && !pkg.installed);
			const loaded = state.status === "ready" || state.status === "error";
			const openPkg = view.kind === "package" || view.kind === "row" ? listed.find((pkg) => pkg.name === view.name) : void 0;
			const openItem = view.kind === "item" ? ledger.items.find((item) => item.id === view.id) : void 0;
			const openRow = view.kind === "row" && openPkg !== void 0 ? openPkg.rows.find((row) => row.rowId === view.rowId) : void 0;
			const showsCards = openPkg === void 0 && openItem === void 0;
			const activated = listed.find((pkg) => pkg.name === activation && pkg.enabled && !state.busy.includes(pkg.name));
			const setRowEnabled = (row, enabled) => {
				/* v8 ignore next -- a row without a live entry has its switch disabled */
				if (row.entryId !== void 0) props.setRowEnabled(row.entryId, enabled);
			};
			const configure = (pkg) => ({
				has: (row) => ledger.rows.has(rowConfigKey(pkg.name, row.rowId)),
				open: (row) => {
					setView({
						kind: "row",
						name: pkg.name,
						rowId: row.rowId
					});
				}
			});
			const packageCard = (pkg) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)(PackageCard, {
				pkg,
				t,
				resolveText,
				busy: state.busy.includes(pkg.name),
				highlighted: state.highlight === pkg.name,
				onOpen: () => {
					setActivation(null);
					setView({
						kind: "package",
						name: pkg.name
					});
				},
				onSetEnabled: (enabled) => {
					setActivation(enabled ? pkg.name : null);
					props.setEnabled(pkg.name, enabled);
				}
			}, pkg.name);
			const officialCards = [...official.map(packageCard), ...ledger.items.map((item) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ItemCard, {
				item,
				t,
				renderSlot,
				onOpen: () => {
					setView({
						kind: "item",
						id: item.id
					});
				}
			}, `item:${item.id}`))];
			const renderGroup = (id, heading, cards) => cards.length === 0 ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
				className: PluginManagerPage_module_css_default.group,
				"data-plugin-scope": "global",
				"data-plugin-group": id,
				children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: PluginManagerPage_module_css_default.groupHead,
					children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("h3", {
						className: PluginManagerPage_module_css_default.groupTitle,
						children: heading
					}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: PluginManagerPage_module_css_default.count,
						"data-plugin-count": cards.length,
						children: cards.length
					})]
				}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("ul", {
					className: PluginManagerPage_module_css_default.cards,
					children: cards
				})]
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("section", {
				className: PluginManagerPage_module_css_default.page,
				"data-plugin-panel": true,
				"aria-busy": state.status === "loading",
				children: [
					showsCards ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("header", {
						className: PluginManagerPage_module_css_default.pageHead,
						"data-window-drag": true,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("h1", {
							className: PluginManagerPage_module_css_default.pageTitle,
							children: t("title")
						}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
							className: PluginManagerPage_module_css_default.pageIntro,
							children: t("intro")
						})] }), /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							className: PluginManagerPage_module_css_default.toolbar,
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
								type: "button",
								className: PluginManagerPage_module_css_default.iconButton,
								"aria-label": t("refresh"),
								title: t("refresh"),
								disabled: !loaded,
								onClick: props.refresh,
								children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
									className: PluginManagerPage_module_css_default.iconWrap,
									"aria-hidden": "true",
									children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconRefreshOutlineRegular, {})
								})
							}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
								variant: "primary",
								size: "sm",
								className: PluginManagerPage_module_css_default.addButton,
								icon: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconPlusOutlineRegular, { size: 13 }),
								disabled: !loaded,
								onClick: props.openInstall,
								children: t(state.install.requestId === void 0 ? "addPlugin" : "installViewTask")
							})]
						})]
					}) : null,
					showsCards && state.status === "loading" ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("p", {
						className: `${PluginManagerPage_module_css_default.status} ${PluginManagerPage_module_css_default.statusWithDot}`,
						role: "status",
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, { state: "ongoing" }), t("loading")]
					}) : null,
					showsCards && state.status === "unavailable" ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("p", {
						className: `${PluginManagerPage_module_css_default.status} ${PluginManagerPage_module_css_default.statusWithDot}`,
						role: "status",
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, { state: "idle" }), t("unavailable")]
					}) : null,
					state.notice === null || noticeLine === null ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Toast, {
						text: noticeLine,
						icon: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconWarningOutlineRegular, {}),
						holdMs: toastHoldMs(noticeLine),
						onDone: props.dismissNotice
					}, state.notice.seq),
					!showsCards && state.status === "error" ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
						className: PluginManagerPage_module_css_default.failure,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("p", {
							className: PluginManagerPage_module_css_default.statusWithDot,
							role: "alert",
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, { state: "error" }), t("error")]
						}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
							variant: "outline",
							size: "sm",
							onClick: props.refresh,
							children: t("retry")
						})]
					}) : null,
					loaded && openPkg !== void 0 && openRow !== void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(RowDetail, {
						pkg: openPkg,
						row: openRow,
						form: formFor(openRow.rowId),
						t,
						resolveText,
						renderSlot,
						onBack: () => {
							setView({
								kind: "package",
								name: openPkg.name
							});
						}
					}) : null,
					loaded && openPkg !== void 0 && openRow === void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(PackageDetail, {
						pkg: openPkg,
						t,
						resolveText,
						busy: state.busy.includes(openPkg.name),
						rowBusy: (row) => row.entryId !== void 0 && state.busy.includes(rowKey(row.entryId)),
						configured: ledger.bundles.has(openPkg.name),
						configure: configure(openPkg),
						renderSlot,
						onBack: () => {
							setView({ kind: "list" });
						},
						onSetEnabled: (enabled) => {
							props.setEnabled(openPkg.name, enabled);
						},
						onUninstall: () => {
							props.uninstall(openPkg.name);
						},
						onSetRowEnabled: setRowEnabled
					}) : null,
					loaded && openItem !== void 0 ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ItemDetail, {
						form: formFor(openItem.id),
						item: openItem,
						t,
						renderSlot,
						onBack: () => {
							setView({ kind: "list" });
						}
					}) : null,
					loaded && showsCards ? officialCards.length === 0 && mine.length === 0 && state.status !== "error" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("p", {
						className: PluginManagerPage_module_css_default.empty,
						children: t("empty")
					}) : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [
						renderGroup("official", t("officialTitle"), officialCards),
						renderGroup("bundles", t("bundlesTitle"), mine.map(packageCard)),
						state.status === "error" ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
							className: PluginManagerPage_module_css_default.failure,
							children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("p", {
								className: PluginManagerPage_module_css_default.statusWithDot,
								role: "alert",
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.StateDot, { state: "error" }), t("error")]
							}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.Button, {
								variant: "outline",
								size: "sm",
								onClick: props.refresh,
								children: t("retry")
							})]
						}) : null
					] }) : null,
					showsCards && activated !== void 0 && !state.install.open ? renderSlot("plugins.bundle.activation", {
						packageName: activated.name,
						onDismiss: () => {
							setActivation(null);
						},
						onOpenDetails: () => {
							setActivation(null);
							setView({
								kind: "package",
								name: activated.name
							});
						}
					}, { entryKey: activated.name }) : null,
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)(InstallDialog, {
						install: state.install,
						t,
						onClose: props.closeInstall,
						onEditSpec: props.editInstallSpec,
						onRun: props.runInstall,
						onCancel: props.cancelInstall,
						onReconcile: props.reconcileInstall,
						onToggleDetails: props.toggleInstallDetails,
						onEnableNow: () => {
							setActivation(state.install.installed);
							props.enableInstalled();
						},
						onApproveBuilds: props.approveBuildsAndRetry,
						onToggleRegistry: props.toggleRegistryOptions,
						onChooseRegistry: props.chooseRegistry,
						onChangeRegistry: props.changeRegistry,
						onUseGithubMirror: props.useGithubMirror
					}),
					state.confirm === null ? null : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ConfirmDialog, {
						name: packageText(state.packages.find((pkg) => pkg.name === state.confirm?.packageName) ?? { name: state.confirm.packageName }, resolveText).title,
						t,
						onConfirm: props.confirm,
						onCancel: props.cancelConfirm
					})
				]
			});
		}
		//#endregion
		//#region src/client/PluginsPanelIcon.tsx
		/**
		* Render the plugin glyph at the size the sidebar asks for.
		* @param props - the sidebar's icon share: the requested edge and whether the panel is selected.
		* @returns the icon element.
		*/
		function PluginsPanelIcon({ size }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconPluginPinwheelOutlineRegular, { size });
		}
		//#endregion
		//#region src/client/locales.ts
		/** Plugin management interface copy. */
		/** Simplified Chinese dictionary and key source of truth. */
		const zh = {
			panel: "插件",
			title: "插件",
			intro: "添加和管理插件",
			loading: "正在读取插件…",
			error: "可能由于网络问题，无法读取全部插件",
			unavailable: "本部署没有可管理的 profile，无法安装或启停插件。",
			retry: "重试",
			refresh: "刷新",
			empty: "还没有安装任何插件。",
			addPlugin: "添加插件",
			restartNotice: "更改将在下次启动生效",
			overriddenNotice: "{name} 已保存，但被更高优先级的配置覆盖，当前未生效",
			bundlesTitle: "已安装",
			officialTitle: "官方",
			statusProblem: "异常",
			statusBeta: "实验性",
			reasonLabel: "原因",
			metadataError: "包元信息错误：{error}",
			versionTag: "v{version}",
			partsLabel: "包含的组件",
			partsEmpty: "这个插件包不包含任何组件。",
			partsCountTotal: "共 {count} 个",
			partsCountRunning: "{count} 运行中",
			partsCountOff: "{count} 已停用",
			partOff: "已关闭",
			partsCountFailed: "{count} 异常",
			partsFilter: "筛选组件",
			partsFilterEmpty: "没有匹配的组件。",
			partToggle: "启用组件 {name}",
			rowPhasePending: "等待依赖",
			rowPhaseLoading: "加载中",
			rowPhaseActive: "运行中",
			rowPhaseFailed: "异常",
			rowPhaseUnloading: "卸载中",
			enableToggle: "启用 {name}",
			openDetail: "查看 {name}",
			backToList: "返回插件列表",
			crumbRoot: "插件列表",
			backToPackage: "返回 {name}",
			configureRow: "配置 {name}",
			rowStateIdle: "未运行",
			uninstall: "卸载",
			uninstallLabel: "卸载 {name}",
			installTitle: "添加插件",
			installDescription: "输入插件的包名、GitHub 仓库地址或本地目录路径。",
			installSpecLabel: "包名或地址",
			installSpecPlaceholder: "例如 @deepseek-ai/dsh-subagent-codex",
			installGuideToggle: "插件安装引导和示例",
			installGuideHide: "收起引导",
			installGuideIdTitle: "包名",
			installGuideIdExample: "@deepseek-ai/dsh-subagent-codex",
			installGuideIdHint: "插件包名即 npm 包名（如 dsh-xxx 或 @作者/插件名），社区插件的 README 安装命令中 dsh plugin add 或 pnpm add 之后的部分。",
			installGuideGitTitle: "GitHub 仓库地址",
			installGuideGitExample: "https://github.com/author/dsh-plugin",
			installGuideGitHint: "插件在 GitHub 上的开源仓库地址，也支持其他 Git 仓库。",
			installGuidePathTitle: "本地插件目录",
			installGuidePathExample: "/Users/name/my-plugin",
			installGuidePathHint: "本机上插件目录的绝对路径，适用于自行开发或已下载的插件。",
			installGuideExampleLabel: "示例：",
			installGuideFill: "填入示例",
			installGuideFillAria: "填入示例 {example}",
			installGuideSafety: "请确认插件来源可信。插件在本机以你的权限运行，来源不明的插件可能损坏 DeepSeek Harness，或读取和泄露你的数据。",
			registryToggle: "安装源",
			registryLegend: "从哪个 npm 源下载插件",
			registryDefault: "默认安装源",
			registryOfficial: "npm 官方源",
			registryNpmmirror: "中国大陆镜像源",
			registryWithHost: "{name}（{host}）",
			registryCustom: "自定义地址",
			registryCustomPlaceholder: "https://npm.example.com/",
			registryCustomHint: "公司内网或私有 npm 源；需要登录的源，把凭据放在本机的 ~/.npmrc 里",
			registryCustomInvalid: "请输入以 http:// 或 https:// 开头的地址",
			registryListSeparator: "、",
			installRun: "安装",
			installChecking: "正在检查…",
			installProblemInvalid: "无法识别这个包名或地址：{reason}",
			installProblemInstalled: "该插件已安装",
			installProblemNotFound: "未找到相关插件",
			installProblemNotPackage: "该路径不存在或不是有效的插件包",
			installProblemNotBundle: "这个包没有声明组合包，无法作为插件安装：{reason}",
			installProblemNetwork: "无法连接插件源，请检查网络后重试",
			installProblemNetworkAll: "所有安装源都无法连接（已尝试：{registries}），请检查网络或代理设置，或更换安装源",
			installProblemUnknown: "无法获取插件信息：{reason}",
			installingTitle: "插件安装中…",
			installedTitle: "已安装",
			installFailedTitle: "插件安装失败",
			installGithubFailedTitle: "无法访问 GitHub",
			installGithubTimeoutTitle: "连接 GitHub 超时",
			installGithubFailedDescription: "请尝试其他安装来源。",
			installUseGithubMirror: "改用国内镜像",
			installTryAnotherWay: "试试其他方式",
			installPackageLabel: "插件包名",
			installEdit: "编辑",
			installEditAria: "返回编辑",
			installCancelAndEdit: "取消安装并返回编辑",
			installApplyingCancellationError: "取消请求未得到确认；安装已进入收尾阶段，请等待结果。{reason}",
			installReconcile: "核对安装状态",
			installUnknownTitle: "未能获取安装结果",
			installUnknownDescription: "后端当前没有此安装任务。请检查插件列表后再尝试安装。",
			installResultUnconfirmed: "未收到安装结果，请核对安装状态。{reason}",
			installAwaitingAcceptance: "正在等待后端接收安装任务，收到确认后会自动重试取消。",
			installBackgroundUnknown: "未能获取安装结果，请检查插件列表。",
			installCancel: "取消安装",
			installCloseCancels: "取消安装并关闭",
			installViewTask: "查看安装任务",
			installUnconfirmedTitle: "安装状态尚未确认",
			installBackgroundDone: "插件安装已完成，可查看安装结果。",
			installBackgroundFailed: "插件安装失败，可查看安装详情。",
			installBackgroundUnconfirmed: "安装状态暂未确认，请查看安装任务了解详情。",
			installBackgroundApplying: "安装已进入收尾阶段，无法取消，可查看安装进度。",
			installStarting: "正在准备安装…",
			installCancelling: "正在停止安装…",
			installApplying: "正在应用配置，请稍候…",
			installCancelledShort: "已取消",
			installCancelled: "已取消安装，插件未启用，下载的文件可能保留",
			installCancelUnconfirmed: "尚未确认安装已停止，请重试取消或等待安装结果。{reason}",
			installEnableNow: "立即启用",
			installDetailsShow: "查看安装详情",
			installDetailsHide: "收起安装详情",
			installVersion: "版本 {version}",
			installSubjectPath: "本地目录",
			installSubjectGit: "Git 仓库",
			installSubjectTarball: "压缩包",
			installLocation: "安装位置：{dir}",
			installRetry: "重试",
			installChangeRegistry: "更换安装源",
			installAttempt: "{previous} 不可用，正在改用 {registry} 重试（第 {index} 个源，共 {total} 个）",
			installAttemptBadge: "第 {index} 次 · {registry}",
			installFailureNetwork: "网络连接失败",
			installFailureNetworkAll: "所有安装源都无法连接（已尝试：{registries}）。请检查网络或代理设置，或更换安装源后重试。",
			installFailureNetworkHost: "无法连接 {host}。GitHub 地址和 .tgz 直链不经过安装源，需要本机能直接访问它或配置代理；如果这个插件也发布到了 npm，请改填包名。",
			installFailureNotFound: "未找到相关插件",
			installFailureNoMatchingVersion: "没有匹配的版本",
			installFailureDiskFull: "磁盘空间不足，安装已停止",
			installFailurePermission: "没有写入权限，无法安装",
			installFailureBuildBlocked: "有依赖的安装脚本需要你允许后才能继续",
			installFailureBuildBlockedManual: "有依赖的安装脚本被 pnpm 拦下，请在 profile 的 pnpm-workspace.yaml 的 allowBuilds 中放行后重试",
			installFailureIntegrity: "下载的安装包校验失败",
			installFailureTimeout: "安装超时",
			installFailurePnpmMissing: "没有找到 pnpm，无法安装",
			installFailureGeneric: "安装过程中出错，原因见安装详情",
			terminalRunning: "运行中",
			terminalFailed: "失败",
			terminalDone: "已完成",
			terminalCopy: "复制",
			terminalCopied: "复制成功",
			terminalNoOutput: "无输出",
			terminalCollapseAria: "收起输出",
			terminalCollapse: "收起",
			terminalExpandAria: "展开其余 {n} 行输出",
			terminalExpand: "… 其余 {n} 行",
			terminalExitCode: "退出码 {code}",
			terminalSignal: "信号 {signal}",
			terminalNoExitCode: "未正常退出",
			installDoneNothing: "安装完成，没有新增依赖。",
			installDoneRestart: "已安装，下次启动后加载。",
			installDoneApproved: "已允许运行安装脚本：{names}",
			installApprovalTitle: "需要允许安装脚本",
			installApprovalDescription: "以下包声明了安装脚本，pnpm 默认不运行。",
			installApprovalConsequence: "允许后，脚本会以你的权限在本机运行，授权保存在当前 profile，之后不再询问。",
			installApprovalCaution: "只在信任这些包时允许。",
			installApproveAndRetry: "允许这些脚本并重试",
			installClose: "完成",
			close: "关闭",
			cancel: "取消",
			confirmUninstallTitle: "卸载「{name}」？",
			confirmUninstallDescription: "卸载后它提供的功能会消失。",
			confirmUninstall: "卸载",
			failedEnable: "启用失败：{reason}",
			failedDisable: "停用失败：{reason}",
			failedUninstall: "卸载失败：{reason}",
			failedRowEnable: "组件启用失败：{reason}",
			failedRowDisable: "组件停用失败：{reason}",
			reasonManagementRequired: "插件管理所需，不能停用或卸载",
			reasonUnaddressable: "当前 profile 的 patch 无法唯一定位这一项",
			reasonUnknownPlugin: "找不到该插件",
			reasonInvalidSpec: "请输入有效的包名或地址",
			reasonAmbiguousInstall: "无法从依赖变更中确定安装了哪一个包",
			reasonNotBundle: "这个包没有声明组合包，不能作为插件管理",
			reasonNotRemovable: "这个包不属于当前 profile，或者是插件管理所需的组件",
			reasonStopProfile: "这个 profile 没有启用 HMR，正在使用的包要停止后用 dsh plugin 卸载",
			reasonBundleInUse: "其他配置仍在使用这个组合包的组件，请先停用它们",
			reasonStaleApproval: "待允许的安装脚本列表已变化，请重新安装以刷新",
			reasonIncompatibleVersion: "{plugin} 与 DSH {runtime} 不兼容（要求 {peers}），运行它可能导致崩溃或数据丢失。请安装与当前 DSH 兼容的插件版本。",
			reasonIncompatibleVersionUnnamed: "这个插件与当前 DSH 版本不兼容，运行它可能导致崩溃或数据丢失",
			reasonOperationError: "Host 报告了一个错误"
		};
		/** English dictionary checked against the Chinese key set. */
		const en = {
			panel: "Plugins",
			title: "Plugins",
			intro: "Add and manage plugins",
			loading: "Reading plugins…",
			error: "Could not read all plugins, possibly due to a network problem",
			unavailable: "This deployment runs without a manageable profile, so plugins cannot be installed or switched here.",
			retry: "Retry",
			refresh: "Refresh",
			empty: "No plugins are installed yet.",
			addPlugin: "Add plugin",
			restartNotice: "The change takes effect at the next start",
			overriddenNotice: "{name} was saved, but a higher-priority configuration overrides it, so it is not in effect",
			bundlesTitle: "Installed",
			officialTitle: "Official",
			statusProblem: "Problem",
			statusBeta: "Experimental",
			reasonLabel: "Reason",
			metadataError: "Package metadata error: {error}",
			versionTag: "v{version}",
			partsLabel: "Components",
			partsEmpty: "This plugin pack contains no components.",
			partsCountTotal: "{count} total",
			partsCountRunning: "{count} running",
			partsCountOff: "{count} off",
			partOff: "Off",
			partsCountFailed: "{count} failed",
			partsFilter: "Filter components",
			partsFilterEmpty: "No component matches.",
			partToggle: "Enable component {name}",
			rowPhasePending: "Waiting for dependencies",
			rowPhaseLoading: "Loading",
			rowPhaseActive: "Running",
			rowPhaseFailed: "Problem",
			rowPhaseUnloading: "Unloading",
			enableToggle: "Enable {name}",
			openDetail: "View {name}",
			backToList: "Back to plugins",
			crumbRoot: "Plugins",
			backToPackage: "Back to {name}",
			configureRow: "Configure {name}",
			rowStateIdle: "Not running",
			uninstall: "Uninstall",
			uninstallLabel: "Uninstall {name}",
			installTitle: "Add plugin",
			installDescription: "Enter the plugin's package name, GitHub repository address, or local directory path.",
			installSpecLabel: "Package name or address",
			installSpecPlaceholder: "for example @deepseek-ai/dsh-subagent-codex",
			installGuideToggle: "Install guide and examples",
			installGuideHide: "Hide the guide",
			installGuideIdTitle: "Package name",
			installGuideIdExample: "@deepseek-ai/dsh-subagent-codex",
			installGuideIdHint: "The plugin package name is the npm package name (like dsh-xxx or @author/plugin): the part after dsh plugin add or pnpm add in a community plugin's README install command.",
			installGuideGitTitle: "GitHub repository address",
			installGuideGitExample: "https://github.com/author/dsh-plugin",
			installGuideGitHint: "The address of the plugin's open-source repository on GitHub; other Git hosts work too.",
			installGuidePathTitle: "Local plugin directory",
			installGuidePathExample: "/Users/name/my-plugin",
			installGuidePathHint: "The absolute path of a plugin directory on this machine, developed here or downloaded.",
			installGuideExampleLabel: "Example: ",
			installGuideFill: "Use example",
			installGuideFillAria: "Use the example {example}",
			installGuideSafety: "Install only plugins you trust: they run with your permissions and can damage DeepSeek Harness or leak your data.",
			registryToggle: "Registry",
			registryLegend: "The npm registry the plugin is downloaded from",
			registryDefault: "Default registry",
			registryOfficial: "Official npm registry",
			registryNpmmirror: "Mainland China mirror",
			registryWithHost: "{name} ({host})",
			registryCustom: "Custom address",
			registryCustomPlaceholder: "https://npm.example.com/",
			registryCustomHint: "A company or private npm registry; for one that needs a login, keep the credentials in ~/.npmrc on this machine",
			registryCustomInvalid: "Enter an address starting with http:// or https://",
			registryListSeparator: ", ",
			installRun: "Install",
			installChecking: "Checking…",
			installProblemInvalid: "This is not a package name or address that can be installed: {reason}",
			installProblemInstalled: "This plugin is already installed",
			installProblemNotFound: "No such plugin was found",
			installProblemNotPackage: "The path does not exist or is not a valid plugin package",
			installProblemNotBundle: "This package declares no bundle, so it cannot be installed as a plugin: {reason}",
			installProblemNetwork: "The plugin registry could not be reached; check the network and try again",
			installProblemNetworkAll: "No registry could be reached (tried: {registries}); check the network or proxy settings, or change the registry",
			installProblemUnknown: "The plugin could not be looked up: {reason}",
			installingTitle: "Installing the plugin…",
			installedTitle: "Installed",
			installFailedTitle: "The plugin could not be installed",
			installGithubFailedTitle: "Cannot access GitHub",
			installGithubTimeoutTitle: "GitHub connection timed out",
			installGithubFailedDescription: "Try another installation source.",
			installUseGithubMirror: "Use mainland China mirror",
			installTryAnotherWay: "Try another way",
			installPackageLabel: "Plugin package name",
			installEdit: "Edit",
			installEditAria: "Back to editing",
			installCancelAndEdit: "Cancel installation and return to editing",
			installApplyingCancellationError: "Cancellation was not confirmed. Installation is being applied; wait for its result. {reason}",
			installReconcile: "Check installation status",
			installUnknownTitle: "Installation result unavailable",
			installUnknownDescription: "The Host has no active installation with this request id. Check the plugin list before trying again.",
			installResultUnconfirmed: "The installation result was not received. Check installation status. {reason}",
			installAwaitingAcceptance: "Waiting for the Host to accept installation. Cancellation will retry automatically after confirmation.",
			installBackgroundUnknown: "Installation result unavailable. Check the plugin list.",
			installCancel: "Cancel install",
			installCloseCancels: "Cancel install and close",
			installViewTask: "View installation",
			installUnconfirmedTitle: "Installation status unconfirmed",
			installBackgroundDone: "Installation finished. View installation details.",
			installBackgroundFailed: "Installation failed. View installation details.",
			installBackgroundUnconfirmed: "Installation status is unconfirmed. View the installation for details.",
			installBackgroundApplying: "Installation is being applied and cannot be cancelled. View installation progress.",
			installStarting: "Preparing installation…",
			installCancelling: "Stopping installation…",
			installApplying: "Applying configuration, please wait…",
			installCancelledShort: "Cancelled",
			installCancelled: "Installation cancelled; the plugin is not enabled, and downloaded files may remain",
			installCancelUnconfirmed: "Installation has not been confirmed stopped. Retry cancellation or wait for the installation result. {reason}",
			installEnableNow: "Enable now",
			installDetailsShow: "Show install details",
			installDetailsHide: "Hide install details",
			installVersion: "Version {version}",
			installSubjectPath: "Local directory",
			installSubjectGit: "Git repository",
			installSubjectTarball: "Tarball",
			installLocation: "Installs into {dir}",
			installRetry: "Retry",
			installChangeRegistry: "Change registry",
			installAttempt: "{previous} could not serve the package; retrying through {registry} (registry {index} of {total})",
			installAttemptBadge: "Attempt {index} · {registry}",
			installFailureNetwork: "The network connection failed",
			installFailureNetworkAll: "No registry could be reached (tried: {registries}). Check the network or proxy settings, or change the registry and retry.",
			installFailureNetworkHost: "{host} could not be reached. A GitHub address or a .tgz link is not fetched through the registry: this machine must reach it directly or through a proxy. If the plugin is also published to npm, enter its package name instead.",
			installFailureNotFound: "No such plugin was found",
			installFailureNoMatchingVersion: "No version matches the request",
			installFailureDiskFull: "The disk is full; the install stopped",
			installFailurePermission: "No write permission; the plugin cannot be installed",
			installFailureBuildBlocked: "A dependency's install scripts need your permission before the install can continue",
			installFailureBuildBlockedManual: "pnpm blocked install scripts; allow them under allowBuilds in pnpm-workspace.yaml and retry",
			installFailureIntegrity: "The downloaded package failed its integrity check",
			installFailureTimeout: "The install timed out",
			installFailurePnpmMissing: "pnpm was not found, so nothing can be installed",
			installFailureGeneric: "Something went wrong during the install; the details say what",
			terminalRunning: "Running",
			terminalFailed: "Failed",
			terminalDone: "Done",
			terminalCopy: "Copy",
			terminalCopied: "Copied",
			terminalNoOutput: "No output",
			terminalCollapseAria: "Collapse output",
			terminalCollapse: "Collapse",
			terminalExpandAria: "Expand the remaining {n} output lines",
			terminalExpand: "… {n} more lines",
			terminalExitCode: "exit code {code}",
			terminalSignal: "signal {signal}",
			terminalNoExitCode: "no exit code",
			installDoneNothing: "Install finished with no new dependency.",
			installDoneRestart: "Installed; it loads at the next start.",
			installDoneApproved: "Install scripts allowed for {names}",
			installApprovalTitle: "Install scripts need permission",
			installApprovalDescription: "These packages have install scripts that pnpm did not run.",
			installApprovalConsequence: "Once allowed, the scripts run here with your permissions, and the permission is saved in this profile.",
			installApprovalCaution: "Allow only packages you trust.",
			installApproveAndRetry: "Allow these scripts and retry",
			installClose: "Done",
			close: "Close",
			cancel: "Cancel",
			confirmUninstallTitle: "Uninstall \"{name}\"?",
			confirmUninstallDescription: "What it provides goes away once it is uninstalled.",
			confirmUninstall: "Uninstall",
			failedEnable: "Could not enable: {reason}",
			failedDisable: "Could not disable: {reason}",
			failedUninstall: "Could not uninstall: {reason}",
			failedRowEnable: "Could not enable the component: {reason}",
			failedRowDisable: "Could not disable the component: {reason}",
			reasonManagementRequired: "Plugin management needs it; it cannot be switched off or uninstalled.",
			reasonUnaddressable: "The profile patch cannot address this one uniquely.",
			reasonUnknownPlugin: "No such plugin.",
			reasonInvalidSpec: "Enter a valid package name or address.",
			reasonAmbiguousInstall: "Which package was installed cannot be told from the dependency change.",
			reasonNotBundle: "This package declares no bundle, so it cannot be managed as a plugin.",
			reasonNotRemovable: "This package is not owned by the profile, or plugin management needs it.",
			reasonStopProfile: "This profile runs without HMR; stop it and uninstall the package with dsh plugin.",
			reasonBundleInUse: "Other configuration still uses this bundle's components; switch them off first.",
			reasonStaleApproval: "The pending script approvals changed; install again to refresh them.",
			reasonIncompatibleVersion: "{plugin} is incompatible with DSH {runtime} (requires {peers}); running it may cause crashes or data loss. Install a plugin version compatible with this DSH.",
			reasonIncompatibleVersionUnnamed: "This plugin is incompatible with the running DSH version; running it may cause crashes or data loss.",
			reasonOperationError: "The Host reported an error."
		};
		//#endregion
		//#region src/client/navigation-store.ts
		/** Plugin page selection shared by the page and cross-plugin navigation. */
		/**
		* Create plugin page selection before the first page render.
		* @returns the registration-owned navigation store handle.
		*/
		function createNavigationStore() {
			return (0, _deepseek_ai_dsh_client_store.defineStore)({
				init: () => ({ view: { kind: "list" } }),
				actions: { setView: (draft, view) => {
					draft.view = view;
				} }
			});
		}
		//#endregion
		//#region src/client/index.ts
		/** Dictionary namespace owned by this plugin. */
		const NS = "pluginManager";
		/** The id shared by the sidebar entry and the main panel it opens. */
		const PANEL_ID = "plugins";
		/** Services required by the sidebar registration and the Remote methods; the inventory says whether the Host manages a profile. */
		const inject = [
			"slots",
			"locale",
			"remote",
			"remote.pluginManager",
			"remote.pluginInventory",
			"remote.pluginRegistryProbe",
			"configForms",
			"layout"
		];
		/**
		* Contribute the Plugins entry to the sidebar with the management page it
		* opens, and keep it current on the Host's change events.
		* @param ctx - the browser plugin context.
		*/
		function apply(ctx) {
			ctx.effect(() => ctx.locale.register(NS, {
				zh,
				en
			}), "ui-plugin-manager: dictionaries");
			const t = ctx.locale.bind(NS);
			const controller = new PluginManagerController(ctx);
			ctx.effect(() => () => {
				controller.dispose();
			}, "ui-plugin-manager: controller");
			ctx.effect(() => {
				const refresh = () => {
					if (controller.getSnapshot().status !== "idle") controller.load();
				};
				const disposers = [
					ctx.remote.$on("plugin-manager/changed", refresh),
					ctx.remote.$on("plugin-manager/install-log", (chunk) => {
						controller.appendLog(chunk);
					}),
					ctx.remote.$on("plugin-manager/install-state", (progress) => {
						controller.installProgress(progress);
					}),
					ctx.on("connection/reset", refresh)
				];
				return () => {
					for (const dispose of disposers) dispose();
				};
			}, "ui-plugin-manager: host invalidations");
			const configLedger = configLedgerSource(ctx);
			ctx.slots.inject("main", function* () {
				const handle = createNavigationStore(), instance = handle.create();
				const store = {
					...handle,
					create: () => instance
				};
				yield ctx.slots.register({
					name: "main",
					key: PANEL_ID,
					locale: NS,
					store,
					inject: () => controller.inject(configLedger, (text) => ctx.locale.resolveText(text)),
					children: {
						"plugins.item": {
							kind: "list",
							scope: "root"
						},
						"plugins.bundle.activation": {
							kind: "keyed",
							scope: "root"
						},
						"plugins.bundle.config": {
							kind: "keyed",
							scope: "root"
						},
						"plugins.row.config": {
							kind: "keyed",
							scope: "root"
						},
						"plugins.detail.actions": {
							kind: "list",
							scope: "root"
						},
						"plugins.detail.badge": {
							kind: "list",
							scope: "root"
						},
						"plugins.detail.section": {
							kind: "list",
							scope: "root"
						}
					}
				}, PluginManagerPage);
				yield ctx.layout.panelInfo.subscribe(() => {
					if (ctx.layout.panelInfo.getSnapshot().activePanelId !== "plugins") instance.actions.setView({ kind: "list" });
				});
				const disposeNavigation = ctx.reflect.provide("pluginNavigation", { openBundle: (packageName) => {
					ctx.layout.selectPanel(PANEL_ID);
					instance.actions.setView({
						kind: "package",
						name: packageName
					});
				} });
				yield () => {
					disposeNavigation();
				};
			});
			ctx.slots.inject("sidebar.panellist", () => ctx.slots.register({
				name: "sidebar.panellist",
				id: PANEL_ID,
				order: 0,
				label: () => t("panel"),
				locale: NS
			}, PluginsPanelIcon));
		}
		//#endregion
		exports.NS = NS;
		exports.PANEL_ID = PANEL_ID;
		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map