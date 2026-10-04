window.__ModuleLoader__.load({
	id: "@deepseek-ai/dsh-client-hmr",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		Object.defineProperty(exports, Symbol.toStringTag, { value: "Module" });
		//#region src/events.ts
		/**
		* Validate the frame envelope; the module controller parses the complete graph before updating its index.
		* @param value - Parsed JSON value from the EventSource message.
		* @returns the known frame, an unknown-type marker, or an invalid marker.
		*/
		function parsePluginsEventFrame(value) {
			if (typeof value !== "object" || value === null) return { kind: "invalid" };
			const record = value;
			switch (record.type) {
				case "rebuilt": return typeof record.id === "string" && typeof record.rev === "string" ? {
					kind: "frame",
					frame: {
						type: "rebuilt",
						id: record.id,
						rev: record.rev
					}
				} : { kind: "invalid" };
				case "graph": return typeof record.graph === "object" && record.graph !== null ? {
					kind: "frame",
					frame: {
						type: "graph",
						graph: record.graph
					}
				} : { kind: "invalid" };
				default: return typeof record.type === "string" ? { kind: "unknown" } : { kind: "invalid" };
			}
		}
		/**
		* Document-relative form of {@link EVENTS_ENDPOINT} used by the browser half.
		* See .agents/notes/implemented/architecture/2026-09-14-web-document-relative-app-routes.md.
		*/
		const EVENTS_ROUTE = "/plugins/events".slice(1);
		//#endregion
		//#region src/client/index.ts
		/** Cordis plugin name. */
		const name = "client-hmr";
		/** Required service: the client module system whose entry controller handles received frames. */
		const inject = ["modules"];
		/**
		* Forward graph snapshots and rebuilds to the page's shared serial controller.
		* @param ctx - Plugin context with the client module system.
		*/
		function apply(ctx) {
			const entries = ctx.modules.entries;
			const handle = (frame) => {
				(frame.type === "graph" ? Promise.resolve().then(() => entries.sync(frame.graph)) : entries.reload(frame.id, frame.rev)).catch((error) => {
					ctx.logger.error(error);
				});
			};
			ctx.effect(() => {
				const source = new EventSource(EVENTS_ROUTE);
				source.addEventListener("message", (event) => {
					let value;
					try {
						value = JSON.parse(event.data);
					} catch {
						ctx.logger.warn(`client-hmr: unparseable event frame: ${event.data}`);
						return;
					}
					const parsed = parsePluginsEventFrame(value);
					if (parsed.kind === "invalid") ctx.logger.warn(`client-hmr: invalid event frame: ${event.data}`);
					else if (parsed.kind === "frame") handle(parsed.frame);
				});
				return () => {
					source.close();
				};
			}, "client-hmr: event source");
		}
		//#endregion
		exports.apply = apply;
		exports.inject = inject;
		exports.name = name;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map