window.__ModuleLoader__.load({
	id: "@deepseek-ai/dsh-experimental-inspector",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		Object.defineProperty(exports, Symbol.toStringTag, { value: "Module" });
		let _deepseek_ai_cordis = require("@deepseek-ai/cordis");
		//#region src/shared/json.ts
		/**
		* Test that a value can cross both MessagePort and JSON WebSocket carriers without coercion.
		* @param value - Candidate wire value.
		* @returns Whether the value is lossless JSON data.
		*/
		function isJsonValue(value) {
			return visitJson(value, /* @__PURE__ */ new Set());
		}
		/**
		* Compute the UTF-8 byte length of a JSON wire value.
		* @param value - Validated JSON value.
		* @returns Its encoded byte length.
		*/
		function jsonByteLength(value) {
			return new TextEncoder().encode(JSON.stringify(value)).byteLength;
		}
		/**
		* Test whether a value is a plain object with string own keys.
		* @param value - Candidate object.
		* @returns Whether the value has `Object.prototype` or a null prototype.
		*/
		function isPlainObject(value) {
			if (typeof value !== "object" || value === null || Array.isArray(value)) return false;
			const prototype = Reflect.getPrototypeOf(value);
			return prototype === Object.prototype || prototype === null;
		}
		function visitJson(value, ancestors) {
			if (value === null || typeof value === "string" || typeof value === "boolean") return true;
			if (typeof value === "number") return Number.isFinite(value) && !Object.is(value, -0);
			if (typeof value !== "object" || ancestors.has(value)) return false;
			ancestors.add(value);
			try {
				if (Array.isArray(value)) {
					if (Object.getPrototypeOf(value) !== Array.prototype || Reflect.ownKeys(value).length !== value.length + 1) return false;
					return value.every((item) => visitJson(item, ancestors));
				}
				if (!isPlainObject(value)) return false;
				for (const key of Reflect.ownKeys(value)) {
					if (typeof key !== "string") return false;
					const descriptor = Object.getOwnPropertyDescriptor(value, key);
					if (descriptor?.enumerable !== true || !("value" in descriptor) || !visitJson(descriptor.value, ancestors)) return false;
				}
				return true;
			} finally {
				ancestors.delete(value);
			}
		}
		//#endregion
		//#region src/shared/identity.ts
		/**
		* Validate and brand a non-empty identifier received from or sent across a runtime boundary.
		* @param value - Untrusted identifier text.
		* @param label - Field name used in validation errors.
		* @returns The role-branded identifier.
		*/
		function inspectorId(value, label) {
			if (value.length === 0 || value.length > 256) throw new Error(`inspector protocol: ${label} must contain 1 to 256 characters`);
			return value;
		}
		//#endregion
		//#region src/shared/validation.ts
		/** Shared exact-object readers for versioned Inspector wire protocols. */
		/**
		* Require a plain object containing only the listed fields.
		* @param value - Candidate object.
		* @param keys - Complete field allowlist.
		* @param label - Object name used in validation errors.
		* @returns The validated plain object.
		*/
		function exactObject(value, keys, label) {
			if (!isPlainObject(value)) throw new Error(`inspector protocol: ${label} must be an object`);
			exactKeys(value, keys, label);
			return value;
		}
		/**
		* Reject fields outside one versioned object's declared field set.
		* @param value - Plain object being validated.
		* @param keys - Complete field allowlist.
		* @param label - Object name used in validation errors.
		*/
		function exactKeys(value, keys, label) {
			const allowed = new Set(keys);
			for (const key of Reflect.ownKeys(value)) if (typeof key !== "string" || !allowed.has(key)) throw new Error(`inspector protocol: ${label} has unknown field ${JSON.stringify(String(key))}`);
		}
		/**
		* Read one non-empty opaque identifier.
		* @param value - Candidate identifier.
		* @param label - Field name used in validation errors.
		* @returns The role-branded identifier.
		*/
		function wireId(value, label) {
			if (typeof value !== "string") throw new Error(`inspector protocol: ${label} must be a string`);
			return inspectorId(value, label);
		}
		/**
		* Read one optional string field.
		* @param value - Object containing the field.
		* @param key - Field name.
		* @returns An empty object or the validated field.
		*/
		function optionalString(value, key) {
			const item = value[key];
			if (item === void 0) return {};
			if (typeof item !== "string") throw new Error(`inspector protocol: ${key} must be a string`);
			return { [key]: item };
		}
		/**
		* Read one optional boolean field.
		* @param value - Object containing the field.
		* @param key - Field name.
		* @returns An empty object or the validated field.
		*/
		function optionalBoolean(value, key) {
			const item = value[key];
			if (item === void 0) return {};
			if (typeof item !== "boolean") throw new Error(`inspector protocol: ${key} must be a boolean`);
			return { [key]: item };
		}
		/**
		* Read one optional non-negative finite number field.
		* @param value - Object containing the field.
		* @param key - Field name.
		* @returns An empty object or the validated field.
		*/
		function optionalNonNegativeNumber(value, key) {
			const item = value[key];
			if (item === void 0) return {};
			if (typeof item !== "number" || !Number.isFinite(item) || item < 0) throw new Error(`inspector protocol: ${key} must be a non-negative finite number`);
			return { [key]: item };
		}
		//#endregion
		//#region src/shared/bridge/control-codec.ts
		/**
		* Decode bootstrap data injected into the browser global.
		* @param value - Untrusted injected value.
		* @returns The validated Client bootstrap.
		*/
		function parseInspectorClientBootstrap(value) {
			const record = exactObject(value, [
				"endpoint",
				"protocol",
				"maxQueuedRecords",
				"maxQueuedBytes",
				"maxRecordsPerFrame",
				"maxFrameBytes",
				"reconnectBaseMs",
				"reconnectMaxMs",
				"queryTimeoutMs",
				"maxRuntimeObjectsPerSession",
				"maxRuntimePropertiesPerResult",
				"maxCordisNodes",
				"maxClientSourceBytes"
			], "Client bootstrap");
			if (typeof record.endpoint !== "string" || typeof record.protocol !== "string") throw new Error("inspector protocol: Client bootstrap endpoint and protocol must be strings");
			let endpoint;
			try {
				endpoint = new URL(record.endpoint);
			} catch {
				throw new Error("inspector protocol: Client bootstrap endpoint must be an absolute URL");
			}
			if (endpoint.protocol !== "ws:" || endpoint.hostname !== "127.0.0.1") throw new Error("inspector protocol: Client bootstrap endpoint must use ws on 127.0.0.1");
			if (record.protocol.length === 0 || record.protocol.length > 256) throw new Error("inspector protocol: Client bootstrap protocol must contain 1 to 256 characters");
			const bootstrap = {
				endpoint: record.endpoint,
				protocol: record.protocol,
				maxQueuedRecords: natural$2(record.maxQueuedRecords, "maxQueuedRecords"),
				maxQueuedBytes: natural$2(record.maxQueuedBytes, "maxQueuedBytes"),
				maxRecordsPerFrame: natural$2(record.maxRecordsPerFrame, "maxRecordsPerFrame"),
				maxFrameBytes: natural$2(record.maxFrameBytes, "maxFrameBytes"),
				reconnectBaseMs: natural$2(record.reconnectBaseMs, "reconnectBaseMs"),
				reconnectMaxMs: natural$2(record.reconnectMaxMs, "reconnectMaxMs"),
				queryTimeoutMs: natural$2(record.queryTimeoutMs, "queryTimeoutMs"),
				maxRuntimeObjectsPerSession: natural$2(record.maxRuntimeObjectsPerSession, "maxRuntimeObjectsPerSession"),
				maxRuntimePropertiesPerResult: natural$2(record.maxRuntimePropertiesPerResult, "maxRuntimePropertiesPerResult"),
				maxClientSourceBytes: natural$2(record.maxClientSourceBytes, "maxClientSourceBytes"),
				maxCordisNodes: natural$2(record.maxCordisNodes, "maxCordisNodes")
			};
			if (bootstrap.reconnectMaxMs < bootstrap.reconnectBaseMs) throw new Error("inspector protocol: reconnectMaxMs must be at least reconnectBaseMs");
			return bootstrap;
		}
		function natural$2(value, label, zero = false) {
			if (!Number.isSafeInteger(value) || value < (zero ? 0 : 1)) throw new Error(`inspector protocol: ${label} must be ${zero ? "a non-negative" : "a positive"} safe integer`);
			return value;
		}
		//#endregion
		//#region src/shared/bridge/query-reader.ts
		/**
		* Create a reader that obtains the tree through the typed Inspector query protocol.
		* @param requester - Active Host or Client query connection.
		* @returns A non-CDP Cordis tree reader.
		*/
		function createQueryCordisRuntimeTreeReader(requester) {
			return { async getTree() {
				return (await requester.request({ op: "cordis-tree/get" })).tree;
			} };
		}
		//#endregion
		//#region src/shared/service.ts
		/**
		* Create the shared service façade without exposing the carrier implementation.
		* @param connection - Realm-local observation and query transport.
		* @returns The Cordis service value.
		*/
		function createInspectorService(connection) {
			return {
				publish: (topic, payload, monotonicMs) => {
					connection.publish(topic, payload, monotonicMs);
				},
				cordis: createQueryCordisRuntimeTreeReader(connection)
			};
		}
		//#endregion
		//#region src/shared/bridge/messages/cordis.ts
		/** Bridge message metadata for Cordis runtime-tree snapshots. */
		/** Observation topic carrying the latest complete Cordis tree. */
		const CORDIS_TREE_TOPIC = "cordis/tree";
		//#endregion
		//#region ../../util/crypto/src/index.ts
		/**
		* Encode bytes as canonical base64 without overflowing function argument limits.
		* @param data - Bytes to encode.
		* @returns base64 text.
		*/
		function bytesToBase64(data) {
			let binary = "";
			const chunk = 32768;
			for (let offset = 0; offset < data.length; offset += chunk) binary += String.fromCharCode(...data.subarray(offset, offset + chunk));
			return btoa(binary);
		}
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
		//#region src/shared/cordis/object-registry.ts
		/** Realm-local retention and identity for live objects referenced by Inspector snapshots. */
		const REGISTRIES_SYMBOL = "dsh.inspector.realm-object-registries";
		const MAX_FIBER_WRAPPER_DEPTH = 8;
		`${JSON.stringify(REGISTRIES_SYMBOL)}`;
		/** One realm's bounded table of objects retained by its latest semantic snapshot. */
		var RealmObjectRegistry = class {
			/** Realm-unique id carried by every reference from this registry. */
			id = inspectorId(randomUUID(), "registryId");
			known = /* @__PURE__ */ new WeakMap();
			retained = /* @__PURE__ */ new Map();
			nextHandle = 1;
			disposed = false;
			constructor() {
				registries().set(this.id, this);
			}
			/**
			* Start one replacement generation.
			* @returns A collector that atomically installs exactly the retained objects on commit.
			*/
			begin() {
				if (this.disposed) throw new Error("inspector: realm object registry is disposed");
				return new RealmObjectGeneration(this);
			}
			/**
			* Resolve one current opaque handle.
			* @param handle - Handle from the latest committed snapshot.
			* @returns The live object, when it remains retained.
			*/
			resolve(handle) {
				return this.retained.get(handle);
			}
			/**
			* Identify one object retained by the latest snapshot. Cordis plugin calls may return nested thenable facades;
			* only objects whose prototype path consists exclusively of those `then` wrappers resolve to the retained Fiber.
			* @param value - Candidate live value.
			* @returns Its wire reference, when present in this registry.
			*/
			identify(value) {
				if ((typeof value !== "object" || value === null) && typeof value !== "function") return void 0;
				let candidate = value;
				for (let depth = 0; candidate !== null && depth <= MAX_FIBER_WRAPPER_DEPTH; depth++) {
					const handle = this.known.get(candidate);
					if (handle !== void 0 && this.retained.get(handle) === candidate) return {
						registryId: this.id,
						handle
					};
					try {
						const keys = Reflect.ownKeys(candidate);
						if (keys.length !== 1 || keys[0] !== "then") return void 0;
						candidate = Object.getPrototypeOf(candidate);
					} catch {
						return;
					}
				}
			}
			/** Remove this registry from the realm and release all strong references. */
			close() {
				if (this.disposed) return;
				this.disposed = true;
				registries().delete(this.id);
				this.retained.clear();
			}
			/**
			* Assign a stable handle and retain a value in one pending generation.
			* @param value - Object represented by the pending snapshot.
			* @param next - Pending generation's strong-reference table.
			* @returns The registry id and stable object handle.
			*/
			retain(value, next) {
				let handle = this.known.get(value);
				if (handle === void 0) {
					handle = inspectorId(`object-${String(this.nextHandle++)}`, "objectHandle");
					this.known.set(value, handle);
				}
				next.set(handle, value);
				return {
					registryId: this.id,
					handle
				};
			}
			/**
			* Replace the current strong-reference set with one completed generation.
			* @param next - Complete object table for the committed snapshot.
			*/
			commit(next) {
				this.retained = next;
			}
		};
		/** Mutable object set assembled before one snapshot becomes visible. */
		var RealmObjectGeneration = class {
			owner;
			retained = /* @__PURE__ */ new Map();
			committed = false;
			constructor(owner) {
				this.owner = owner;
			}
			/**
			* Retain one object and obtain its stable opaque reference.
			* @param value - Context or Fiber represented in the snapshot.
			* @returns Source-local wire reference.
			*/
			retain(value) {
				if (this.committed) throw new Error("inspector: realm object generation is already committed");
				return this.owner.retain(value, this.retained);
			}
			/**
			* Stop retaining an object omitted while bounding the pending snapshot.
			* @param handle - Opaque handle removed from this pending generation.
			*/
			release(handle) {
				if (this.committed) throw new Error("inspector: realm object generation is already committed");
				this.retained.delete(handle);
			}
			/** Atomically replace the registry's retained set. */
			commit() {
				if (this.committed) return;
				this.committed = true;
				this.owner.commit(this.retained);
			}
		};
		/**
		* Identify a retained object across all Inspector collectors in this realm.
		* @param value - Runtime value returned to a debugger.
		* @returns Its source-local reference, when the value is a visible entity.
		*/
		function identifyRealmObject(value) {
			for (const registry of registries().values()) {
				const reference = registry.identify(value);
				if (reference !== void 0) return reference;
			}
		}
		function registries() {
			const key = Symbol.for(REGISTRIES_SYMBOL);
			const existing = Reflect.get(globalThis, key);
			if (existing instanceof Map) return existing;
			const value = /* @__PURE__ */ new Map();
			Reflect.set(globalThis, key, value);
			return value;
		}
		//#endregion
		//#region src/shared/cordis/collector.ts
		/** Shared Host/Client projection from live Cordis objects to a bounded semantic tree. */
		const SHADOW = Symbol.for("cordis.shadow");
		/** Realm-local collector with a current live-object table. */
		var CordisTreeCollector = class {
			root;
			limits;
			/** Live-object table replaced atomically with each emitted snapshot. */
			objects = new RealmObjectRegistry();
			revision = 0;
			constructor(root, limits) {
				this.root = root;
				this.limits = limits;
			}
			/**
			* Capture the current reachable Context/Fiber tree.
			* @returns A detached JSON snapshot whose retained objects replace the prior generation atomically.
			*/
			snapshot() {
				const collected = collectContexts(this.root);
				const tree = collected.root;
				const objects = this.objects.begin();
				let nodeCount = 0;
				let truncated = collected.truncated;
				const contextNode = (info) => {
					if (nodeCount >= this.limits.maxNodes) {
						truncated = true;
						return;
					}
					nodeCount++;
					const node = {
						kind: "context",
						objectHandle: objects.retain(info.value).handle,
						children: []
					};
					for (const child of info.children) if (child.fiber !== void 0 && child.fiber.ctx === child.value) {
						const projected = fiberNode(child.fiber, child);
						if (projected !== void 0) node.children.push(projected);
					} else {
						const projected = contextNode(child);
						if (projected !== void 0) node.children.push(projected);
					}
					return node;
				};
				const fiberNode = (fiber, owned) => {
					if (fiber.uid === null) return void 0;
					if (nodeCount + 2 > this.limits.maxNodes) {
						truncated = true;
						return;
					}
					nodeCount++;
					const context = contextNode(owned);
					return {
						kind: "fiber",
						objectHandle: objects.retain(fiber).handle,
						uid: fiber.uid,
						children: [context]
					};
				};
				const root = contextNode(tree);
				if (root === void 0) throw new Error("inspector: maxNodes cannot retain the root Context");
				let snapshot = {
					schemaVersion: 0,
					revision: ++this.revision,
					objectRegistryId: this.objects.id,
					root,
					truncated
				};
				while (jsonByteLength(snapshot) > this.limits.maxBytes) {
					const removed = pruneLast(root);
					if (removed.length === 0) break;
					for (const handle of removed) objects.release(handle);
					snapshot = {
						...snapshot,
						truncated: true
					};
				}
				if (jsonByteLength(snapshot) > this.limits.maxBytes) throw new Error("inspector: Cordis root exceeds the source-frame byte limit");
				objects.commit();
				return snapshot;
			}
			/** Release the realm-global resolver and every retained object. */
			close() {
				this.objects.close();
			}
		};
		function collectContexts(root) {
			const contexts = /* @__PURE__ */ new Map();
			let truncated = false;
			const ensure = (candidate, depth = 0) => {
				if (depth > 100) {
					truncated = true;
					return;
				}
				const value = unwrapContext(candidate);
				if (!_deepseek_ai_cordis.Context.is(value)) return void 0;
				const existing = contexts.get(value);
				if (existing !== void 0) return existing;
				if (value === root) {
					const info = describeContext(value);
					contexts.set(value, info);
					return info;
				}
				const parent = ensure(unwrapContext(Object.getPrototypeOf(value)), depth + 1);
				if (parent === void 0) return void 0;
				const info = describeContext(value);
				contexts.set(value, info);
				parent.children.push(info);
				return info;
			};
			const rootInfo = ensure(root);
			for (const runtime of root.registry.values()) for (const fiber of runtime.fibers) {
				if (fiber.uid === null) continue;
				ensure(fiber.parent);
				ensure(fiber.ctx);
			}
			for (const key of Reflect.ownKeys(root.events._hooks)) for (const hook of root.events._hooks[key] ?? []) ensure(hook.ctx);
			const order = (info) => info.fiber?.uid ?? Number.MAX_SAFE_INTEGER;
			for (const info of contexts.values()) info.children.sort((left, right) => order(left) - order(right));
			return {
				root: rootInfo,
				truncated
			};
		}
		function describeContext(value) {
			return {
				value,
				children: [],
				fiber: ownValue(value, "fiber")
			};
		}
		function ownValue(value, key) {
			return Reflect.getOwnPropertyDescriptor(value, key)?.value;
		}
		function unwrapContext(value) {
			let current = value;
			while (typeof current === "object" && current !== null && Object.hasOwn(current, SHADOW)) current = Object.getPrototypeOf(current);
			return current;
		}
		function pruneLast(context) {
			const child = context.children.at(-1);
			if (child === void 0) return [];
			if (child.kind === "context") {
				const nested = pruneLast(child);
				if (nested.length > 0) return nested;
				context.children.pop();
				return [child.objectHandle];
			}
			const owned = child.children[0];
			const nested = pruneLast(owned);
			if (nested.length > 0) return nested;
			context.children.pop();
			return [child.objectHandle, owned.objectHandle];
		}
		//#endregion
		//#region src/shared/cordis/observer.ts
		/**
		* Observe one Cordis realm and publish immutable tree replacements.
		* @param ctx - Plugin context whose root is inspected and whose effects own listeners.
		* @param listener - Consumer of complete snapshots in the inspected realm.
		* @param limits - Snapshot node and encoded-byte limits.
		* @returns A disposer that unregisters listeners and releases retained objects.
		*/
		function observeCordisTree(ctx, listener, limits) {
			const collector = new CordisTreeCollector(ctx.root, limits);
			let scheduled = false;
			let closed = false;
			const publish = () => {
				scheduled = false;
				if (closed) return;
				listener(collector.snapshot());
			};
			const schedule = () => {
				if (scheduled || closed) return;
				scheduled = true;
				queueMicrotask(publish);
			};
			const disposers = [ctx.on("internal/plugin", schedule, { global: true }), ctx.on("internal/status", schedule, { global: true })];
			publish();
			return () => {
				if (closed) return;
				closed = true;
				for (const dispose of disposers) dispose();
				collector.close();
			};
		}
		//#endregion
		//#region src/shared/cordis/publisher.ts
		/**
		* Observe one Cordis runtime and retain its latest source snapshot.
		* @param ctx - Plugin context whose root is inspected.
		* @param publisher - Active Host or Client source publisher.
		* @param limits - Snapshot node and encoded-byte limits.
		* @returns A disposer that stops observation and releases retained objects.
		*/
		function publishCordisTree(ctx, publisher, limits) {
			return observeCordisTree(ctx, (snapshot) => {
				publisher.setState(CORDIS_TREE_TOPIC, snapshot);
			}, limits);
		}
		//#endregion
		//#region src/shared/bridge/messages/runtime/console-frames.ts
		/**
		* Parse a Worker-to-Client Console lifecycle frame.
		* @param value - Untrusted decoded frame.
		* @returns A validated enable or disable frame.
		*/
		function parseClientConsoleControlFrame(value) {
			exactKeys(value, [
				"v",
				"t",
				"sourceId",
				"generation",
				"sessionId"
			], "Client Console control frame");
			if (value.v !== 0 || value.t !== "client-console/enable" && value.t !== "client-console/disable") throw new Error("inspector protocol: invalid Client Console control frame");
			return {
				v: 0,
				t: value.t,
				sourceId: wireId(value.sourceId, "sourceId"),
				generation: wireId(value.generation, "generation"),
				sessionId: wireId(value.sessionId, "sessionId")
			};
		}
		//#endregion
		//#region src/shared/bridge/messages/runtime/command-codec.ts
		/** Exact wire decoder for Client Runtime commands. */
		/**
		* Parse and rebuild one Runtime command before it enters the Client realm.
		* @param value - Untrusted command value.
		* @returns The validated command union member.
		*/
		function parseClientRuntimeCommand(value) {
			if (!isPlainObject(value) || typeof value.op !== "string") throw new Error("inspector protocol: Client Runtime command must have an op");
			switch (value.op) {
				case "evaluate":
					exactKeys(value, [
						"op",
						"expression",
						"objectGroup",
						"includeCommandLineAPI",
						"silent",
						"returnByValue",
						"generatePreview",
						"userGesture",
						"awaitPromise",
						"disableBreaks",
						"replMode",
						"allowUnsafeEvalBlockedByCSP",
						"timeoutMs"
					], "evaluate command");
					if (typeof value.expression !== "string") throw new Error("inspector protocol: evaluate expression must be a string");
					return {
						op: "evaluate",
						expression: value.expression,
						...optionalString(value, "objectGroup"),
						...optionalBoolean(value, "includeCommandLineAPI"),
						...optionalBoolean(value, "silent"),
						...optionalBoolean(value, "returnByValue"),
						...optionalBoolean(value, "generatePreview"),
						...optionalBoolean(value, "userGesture"),
						...optionalBoolean(value, "awaitPromise"),
						...optionalBoolean(value, "disableBreaks"),
						...optionalBoolean(value, "replMode"),
						...optionalBoolean(value, "allowUnsafeEvalBlockedByCSP"),
						...optionalNonNegativeNumber(value, "timeoutMs")
					};
				case "get-properties":
					exactKeys(value, [
						"op",
						"handle",
						"ownProperties",
						"accessorPropertiesOnly",
						"generatePreview",
						"nonIndexedPropertiesOnly"
					], "get-properties command");
					return {
						op: "get-properties",
						handle: wireId(value.handle, "handle"),
						...optionalBoolean(value, "ownProperties"),
						...optionalBoolean(value, "accessorPropertiesOnly"),
						...optionalBoolean(value, "generatePreview"),
						...optionalBoolean(value, "nonIndexedPropertiesOnly")
					};
				case "call-function": return parseCallFunction(value);
				case "await-promise":
					exactKeys(value, [
						"op",
						"promise",
						"returnByValue",
						"generatePreview"
					], "await-promise command");
					return {
						op: "await-promise",
						promise: wireId(value.promise, "promise"),
						...optionalBoolean(value, "returnByValue"),
						...optionalBoolean(value, "generatePreview")
					};
				case "release-object":
					exactKeys(value, ["op", "handle"], "release-object command");
					return {
						op: "release-object",
						handle: wireId(value.handle, "handle")
					};
				case "release-object-group":
					exactKeys(value, ["op", "objectGroup"], "release-object-group command");
					if (typeof value.objectGroup !== "string") throw new Error("inspector protocol: objectGroup must be a string");
					return {
						op: "release-object-group",
						objectGroup: value.objectGroup
					};
				case "global-lexical-scope-names":
					exactKeys(value, ["op"], "global-lexical-scope-names command");
					return { op: "global-lexical-scope-names" };
				default: throw new Error(`inspector protocol: unknown Client Runtime command ${JSON.stringify(value.op)}`);
			}
		}
		function parseCallFunction(value) {
			exactKeys(value, [
				"op",
				"functionDeclaration",
				"receiver",
				"arguments",
				"objectGroup",
				"silent",
				"returnByValue",
				"generatePreview",
				"userGesture",
				"awaitPromise"
			], "call-function command");
			if (typeof value.functionDeclaration !== "string") throw new Error("inspector protocol: functionDeclaration must be a string");
			let args;
			if (value.arguments !== void 0) {
				if (!Array.isArray(value.arguments)) throw new Error("inspector protocol: call arguments must be an array");
				args = value.arguments.map(parseCallArgument);
			}
			return {
				op: "call-function",
				functionDeclaration: value.functionDeclaration,
				...value.receiver === void 0 ? {} : { receiver: wireId(value.receiver, "receiver") },
				...args === void 0 ? {} : { arguments: args },
				...optionalString(value, "objectGroup"),
				...optionalBoolean(value, "silent"),
				...optionalBoolean(value, "returnByValue"),
				...optionalBoolean(value, "generatePreview"),
				...optionalBoolean(value, "userGesture"),
				...optionalBoolean(value, "awaitPromise")
			};
		}
		function parseCallArgument(value) {
			if (!isPlainObject(value) || typeof value.kind !== "string") throw new Error("inspector protocol: invalid Client Runtime call argument");
			switch (value.kind) {
				case "value":
					exactKeys(value, ["kind", "value"], "value call argument");
					if (!isJsonValue(value.value)) throw new Error("inspector protocol: call argument value must be JSON");
					return {
						kind: "value",
						value: value.value
					};
				case "unserializable":
					exactKeys(value, ["kind", "value"], "unserializable call argument");
					if (typeof value.value !== "string") throw new Error("inspector protocol: unserializable argument must be a string");
					return {
						kind: "unserializable",
						value: value.value
					};
				case "object":
					exactKeys(value, ["kind", "handle"], "object call argument");
					return {
						kind: "object",
						handle: wireId(value.handle, "handle")
					};
				case "undefined":
					exactKeys(value, ["kind"], "undefined call argument");
					return { kind: "undefined" };
				default: throw new Error(`inspector protocol: unknown call argument ${JSON.stringify(value.kind)}`);
			}
		}
		//#endregion
		//#region src/shared/bridge/messages/runtime/frames.ts
		/**
		* Parse and rebuild one Worker-to-Client Runtime request.
		* @param value - Untrusted request frame.
		* @returns The validated request frame.
		*/
		function parseClientRuntimeRequestFrame(value) {
			exactKeys(value, [
				"v",
				"t",
				"sourceId",
				"generation",
				"sessionId",
				"requestId",
				"command"
			], "Client Runtime request");
			if (value.v !== 0 || value.t !== "client-runtime/request") throw new Error("inspector protocol: invalid Client Runtime request envelope");
			return {
				v: 0,
				t: "client-runtime/request",
				sourceId: wireId(value.sourceId, "sourceId"),
				generation: wireId(value.generation, "generation"),
				sessionId: wireId(value.sessionId, "sessionId"),
				requestId: wireId(value.requestId, "requestId"),
				command: parseClientRuntimeCommand(value.command)
			};
		}
		/**
		* Parse and rebuild one Worker-to-Client Runtime cancellation.
		* @param value - Untrusted cancellation frame.
		* @returns The validated cancellation frame.
		*/
		function parseClientRuntimeCancelFrame(value) {
			exactKeys(value, [
				"v",
				"t",
				"sourceId",
				"generation",
				"sessionId",
				"requestId"
			], "Client Runtime cancellation");
			if (value.v !== 0 || value.t !== "client-runtime/cancel") throw new Error("inspector protocol: invalid Client Runtime cancellation envelope");
			return {
				v: 0,
				t: "client-runtime/cancel",
				sourceId: wireId(value.sourceId, "sourceId"),
				generation: wireId(value.generation, "generation"),
				sessionId: wireId(value.sessionId, "sessionId"),
				requestId: wireId(value.requestId, "requestId")
			};
		}
		/**
		* Parse and rebuild one Worker acknowledgement for a Client Runtime response.
		* @param value - Untrusted acknowledgement frame.
		* @returns The validated acknowledgement frame.
		*/
		function parseClientRuntimeResponseAcknowledgedFrame(value) {
			exactKeys(value, [
				"v",
				"t",
				"sourceId",
				"generation",
				"sessionId",
				"requestId"
			], "Client Runtime response acknowledgement");
			if (value.v !== 0 || value.t !== "client-runtime/response-acknowledged") throw new Error("inspector protocol: invalid Client Runtime response acknowledgement envelope");
			return {
				v: 0,
				t: "client-runtime/response-acknowledged",
				sourceId: wireId(value.sourceId, "sourceId"),
				generation: wireId(value.generation, "generation"),
				sessionId: wireId(value.sessionId, "sessionId"),
				requestId: wireId(value.requestId, "requestId")
			};
		}
		/**
		* Parse and rebuild one Runtime-session cleanup notification.
		* @param value - Untrusted cleanup frame.
		* @returns The validated cleanup frame.
		*/
		function parseClientRuntimeSessionClosedFrame(value) {
			exactKeys(value, [
				"v",
				"t",
				"sourceId",
				"generation",
				"sessionId"
			], "Client Runtime session close");
			if (value.v !== 0 || value.t !== "client-runtime/session-closed") throw new Error("inspector protocol: invalid Client Runtime session close envelope");
			return {
				v: 0,
				t: "client-runtime/session-closed",
				sourceId: wireId(value.sourceId, "sourceId"),
				generation: wireId(value.generation, "generation"),
				sessionId: wireId(value.sessionId, "sessionId")
			};
		}
		//#endregion
		//#region src/shared/bridge/messages/sources/codec.ts
		/** Exact decoders for Client source catalog operations and values. */
		/**
		* Parse one Worker-to-Client source command.
		* @param value - Untrusted decoded command.
		* @returns The validated command.
		*/
		function parseClientSourceCommand(value) {
			if (!isPlainObject(value) || typeof value.op !== "string") throw new Error("inspector protocol: Client source command must have an op");
			if (value.op === "list-scripts") {
				exactKeys(value, ["op"], "Client source list command");
				return { op: "list-scripts" };
			}
			if (value.op !== "get-content-chunk") throw new Error(`inspector protocol: unknown Client source command ${JSON.stringify(value.op)}`);
			exactKeys(value, [
				"op",
				"scriptKey",
				"content",
				"offset",
				"maxBytes"
			], "Client source chunk command");
			return {
				op: "get-content-chunk",
				scriptKey: wireId(value.scriptKey, "scriptKey"),
				content: contentKind(value.content),
				offset: natural$1(value.offset, "offset", true),
				maxBytes: natural$1(value.maxBytes, "maxBytes", false)
			};
		}
		function contentKind(value) {
			if (value !== "source" && value !== "source-map") throw new Error("inspector protocol: invalid Client source content kind");
			return value;
		}
		function natural$1(value, label, zero) {
			if (!Number.isSafeInteger(value) || value < (zero ? 0 : 1)) throw new Error(`inspector protocol: ${label} must be ${zero ? "a non-negative" : "a positive"} integer`);
			return value;
		}
		//#endregion
		//#region src/shared/bridge/messages/sources/frames.ts
		/**
		* Parse one Worker-to-Client source request.
		* @param value - Untrusted decoded request.
		* @returns The validated request frame.
		*/
		function parseClientSourceRequestFrame(value) {
			exactKeys(value, [
				"v",
				"t",
				"sourceId",
				"generation",
				"sessionId",
				"requestId",
				"command"
			], "Client source request");
			if (value.v !== 0 || value.t !== "client-sources/request") throw new Error("inspector protocol: invalid Client source request envelope");
			return {
				v: 0,
				t: "client-sources/request",
				sourceId: wireId(value.sourceId, "sourceId"),
				generation: wireId(value.generation, "generation"),
				sessionId: wireId(value.sessionId, "sessionId"),
				requestId: wireId(value.requestId, "requestId"),
				command: parseClientSourceCommand(value.command)
			};
		}
		/**
		* Parse one Client source-session cleanup notification.
		* @param value - Untrusted decoded notification.
		* @returns The validated cleanup frame.
		*/
		function parseClientSourceSessionClosedFrame(value) {
			exactKeys(value, [
				"v",
				"t",
				"sourceId",
				"generation",
				"sessionId"
			], "Client source session close");
			if (value.v !== 0 || value.t !== "client-sources/session-closed") throw new Error("inspector protocol: invalid Client source session close envelope");
			return {
				v: 0,
				t: "client-sources/session-closed",
				sourceId: wireId(value.sourceId, "sourceId"),
				generation: wireId(value.generation, "generation"),
				sessionId: wireId(value.sessionId, "sessionId")
			};
		}
		//#endregion
		//#region src/shared/bridge/messages/observation.ts
		/** Versioned source lifecycle, observation, and extension frames shared by both carriers. */
		/**
		* Parse and rebuild one Worker control frame received by a source.
		* @param value - Untrusted decoded wire value.
		* @returns The validated Worker-to-source frame.
		*/
		function parseWorkerSourceFrame(value) {
			if (!isJsonValue(value) || !isPlainObject(value) || value.v !== 0 || typeof value.t !== "string") throw new Error("inspector protocol: invalid Worker source frame");
			if (value.t === "source/rejected") {
				exactKeys(value, [
					"v",
					"t",
					"code",
					"message"
				], "source/rejected frame");
				if (value.code !== "invalid-frame" && value.code !== "version-mismatch" && value.code !== "unauthorized" || typeof value.message !== "string") throw new Error("inspector protocol: invalid source/rejected frame");
				return {
					v: 0,
					t: "source/rejected",
					code: value.code,
					message: value.message
				};
			}
			if (value.t === "client-runtime/request") return parseClientRuntimeRequestFrame(value);
			if (value.t === "client-runtime/cancel") return parseClientRuntimeCancelFrame(value);
			if (value.t === "client-runtime/response-acknowledged") return parseClientRuntimeResponseAcknowledgedFrame(value);
			if (value.t === "client-runtime/session-closed") return parseClientRuntimeSessionClosedFrame(value);
			if (value.t === "client-sources/request") return parseClientSourceRequestFrame(value);
			if (value.t === "client-sources/session-closed") return parseClientSourceSessionClosedFrame(value);
			if (value.t === "client-console/enable" || value.t === "client-console/disable") return parseClientConsoleControlFrame(value);
			const common = {
				v: 0,
				sourceId: sourceId(value.sourceId),
				generation: generation(value.generation)
			};
			if (value.t === "source/accepted") {
				exactKeys(value, [
					"v",
					"t",
					"sourceId",
					"generation"
				], "source/accepted frame");
				return {
					...common,
					t: "source/accepted"
				};
			}
			if (value.t === "source/append-acknowledged") {
				exactKeys(value, [
					"v",
					"t",
					"sourceId",
					"generation",
					"nextSequence"
				], "source append acknowledgement");
				return {
					...common,
					t: "source/append-acknowledged",
					nextSequence: natural(value.nextSequence, "nextSequence")
				};
			}
			if (value.t === "source/resnapshot" && typeof value.reason === "string") {
				exactKeys(value, [
					"v",
					"t",
					"sourceId",
					"generation",
					"expectedSequence",
					"reason"
				], "source/resnapshot frame");
				return {
					...common,
					t: "source/resnapshot",
					expectedSequence: natural(value.expectedSequence, "expectedSequence"),
					reason: value.reason
				};
			}
			throw new Error(`inspector protocol: unknown Worker source frame ${JSON.stringify(value.t)}`);
		}
		function sourceId(value) {
			if (typeof value !== "string") throw new Error("inspector protocol: sourceId must be a string");
			return inspectorId(value, "sourceId");
		}
		function generation(value) {
			if (typeof value !== "string") throw new Error("inspector protocol: generation must be a string");
			return inspectorId(value, "generation");
		}
		function natural(value, label) {
			if (!Number.isSafeInteger(value) || value < 0) throw new Error(`inspector protocol: ${label} must be a non-negative safe integer`);
			return value;
		}
		//#endregion
		//#region src/shared/bridge/publisher.ts
		/** Shared observation and query delegation inherited by both source transports. */
		var InspectorSourceConnection = class {
			/** Publish one JSON observation without waiting on its carrier. */
			publish(topic, payload, monotonicMs = performance.now()) {
				this.publisher.publish(topic, payload, monotonicMs);
			}
			/** Retain and publish one state value for reconnect or replacement recovery. */
			setState(topic, payload, monotonicMs = performance.now()) {
				this.publisher.setState(topic, payload, monotonicMs);
			}
			/** Execute one non-CDP query through the active source generation. */
			request(query) {
				return this.queries.request(query);
			}
		};
		//#endregion
		//#region src/client/cdp/stack.ts
		/**
		* Capture the caller stack of a wrapped Client Console method.
		* @param resolveScript - Resolver for Client catalog script keys.
		* @returns Parsed call frames when the browser supplies a stack.
		*/
		function captureClientConsoleStack(resolveScript) {
			return parseClientStack((/* @__PURE__ */ new Error()).stack, resolveScript, 3);
		}
		/**
		* Parse the stack attached to an uncaught Client value when available.
		* @param value - Thrown or rejected value.
		* @param resolveScript - Resolver for Client catalog script keys.
		* @returns Parsed call frames when the value has a recognized stack string.
		*/
		function clientErrorStack(value, resolveScript = () => void 0) {
			if (typeof value !== "object" || value === null) return void 0;
			let stack;
			try {
				stack = Reflect.get(value, "stack");
			} catch {
				return;
			}
			return typeof stack === "string" ? parseClientStack(stack, resolveScript, 0) : void 0;
		}
		/**
		* Parse V8- and Firefox-style textual frames into the common stack model.
		* @param stack - Browser stack text.
		* @param resolveScript - Resolver for Client catalog script keys.
		* @param skipFrames - Parsed observer frames omitted from the result.
		* @returns Parsed call frames, or `undefined` when none remain.
		*/
		function parseClientStack(stack, resolveScript, skipFrames) {
			if (stack === void 0) return void 0;
			const frames = [];
			for (const line of stack.split("\n")) {
				const frame = parseFrame(line, resolveScript);
				if (frame !== void 0) frames.push(frame);
			}
			const callFrames = frames.slice(skipFrames);
			return callFrames.length === 0 ? void 0 : { callFrames };
		}
		function parseFrame(line, resolveScript) {
			const chrome = /^\s*at\s+(?:(.*?)\s+\()?(.+):(\d+):(\d+)\)?$/u.exec(line);
			const firefox = chrome === null ? /^(.*?)@(.+):(\d+):(\d+)$/u.exec(line) : null;
			const match = chrome ?? firefox;
			if (match === null) return void 0;
			const url = match[2];
			const lineNumber = Number(match[3]) - 1;
			const columnNumber = Number(match[4]) - 1;
			if (url === void 0 || !Number.isSafeInteger(lineNumber) || !Number.isSafeInteger(columnNumber)) return void 0;
			const scriptKey = resolveScript(url);
			return {
				functionName: match[1] ?? "",
				...scriptKey === void 0 ? {} : { scriptKey },
				url,
				lineNumber,
				columnNumber
			};
		}
		//#endregion
		//#region src/client/cdp/console.ts
		/**
		* Describe browser-side Console observation.
		* @returns The Console capability advertised by a browser Client source.
		*/
		function consoleBridgeCapability() {
			return { type: "client-console" };
		}
		const METHODS = [
			["log", "log"],
			["debug", "debug"],
			["info", "info"],
			["error", "error"],
			["warn", "warning"],
			["dir", "dir"],
			["dirxml", "dirxml"],
			["table", "table"],
			["trace", "trace"],
			["clear", "clear"],
			["group", "startGroup"],
			["groupCollapsed", "startGroupCollapsed"],
			["groupEnd", "endGroup"],
			["assert", "assert"],
			["profile", "profile"],
			["profileEnd", "profileEnd"],
			["count", "count"],
			["timeEnd", "timeEnd"]
		];
		/** Installs one transparent console/error observer and fans out session-local values. */
		var ClientConsoleObserver = class {
			runtime;
			sink;
			resolveScript;
			sessions = /* @__PURE__ */ new Set();
			installed = [];
			active = false;
			closed = false;
			constructor(runtime, sink, resolveScript = () => void 0) {
				this.runtime = runtime;
				this.sink = sink;
				this.resolveScript = resolveScript;
			}
			/**
			* Start producing events for one DevTools Runtime session.
			* @param sessionId - Session whose object table retains event arguments.
			*/
			enable(sessionId) {
				if (this.closed) return;
				this.sessions.add(sessionId);
				if (!this.active) this.install();
			}
			/**
			* Stop producing events and release Console objects for one session.
			* @param sessionId - Session being disabled or closed.
			*/
			disable(sessionId) {
				this.sessions.delete(sessionId);
				this.runtime.releaseObjectGroup(sessionId, "console");
				if (this.sessions.size === 0) this.uninstall();
			}
			/** Restore original browser hooks and clear every active session. */
			close() {
				if (this.closed) return;
				this.closed = true;
				this.reset();
			}
			/** Stop observing the current source generation while allowing a later reconnect. */
			reset() {
				this.sessions.clear();
				this.uninstall();
			}
			install() {
				this.active = true;
				for (const [name, type] of METHODS) {
					const candidate = Reflect.get(console, name);
					if (typeof candidate !== "function") continue;
					const original = candidate;
					const capture = (values) => {
						this.captureConsole(type, values);
					};
					const replacement = function(...args) {
						const result = Reflect.apply(original, this, args);
						const values = name === "assert" ? args.slice(1) : args;
						if (name !== "assert" || !args[0]) capture(values);
						return result;
					};
					if (Reflect.set(console, name, replacement)) this.installed.push({
						name,
						original,
						replacement
					});
				}
				addGlobalListener("error", this.onError);
				addGlobalListener("unhandledrejection", this.onUnhandledRejection);
			}
			uninstall() {
				if (!this.active) return;
				this.active = false;
				removeGlobalListener("error", this.onError);
				removeGlobalListener("unhandledrejection", this.onUnhandledRejection);
				for (const method of this.installed.splice(0).reverse()) if (Reflect.get(console, method.name) === method.replacement) Reflect.set(console, method.name, method.original);
			}
			onError = (event) => {
				const error = Reflect.get(event, "error");
				const message = Reflect.get(event, "message");
				this.captureException(error ?? new Error(typeof message === "string" ? message : "Client error"));
			};
			onUnhandledRejection = (event) => {
				this.captureException(Reflect.get(event, "reason"));
			};
			captureConsole(type, values) {
				const timestamp = Date.now();
				const stackTrace = captureClientConsoleStack(this.resolveScript);
				queueMicrotask(() => {
					for (const sessionId of [...this.sessions]) try {
						const event = this.runtime.consoleEvent(sessionId, type, values, timestamp, stackTrace);
						if (event !== void 0) this.sink(sessionId, event);
					} catch {}
				});
			}
			captureException(error) {
				const timestamp = Date.now();
				const stackTrace = clientErrorStack(error, this.resolveScript);
				queueMicrotask(() => {
					for (const sessionId of [...this.sessions]) try {
						const event = this.runtime.exceptionEvent(sessionId, error, timestamp, stackTrace);
						if (event !== void 0) this.sink(sessionId, event);
					} catch {}
				});
			}
		};
		function addGlobalListener(type, listener) {
			const add = Reflect.get(globalThis, "addEventListener");
			if (typeof add === "function") Reflect.apply(add, globalThis, [type, listener]);
		}
		function removeGlobalListener(type, listener) {
			const remove = Reflect.get(globalThis, "removeEventListener");
			if (typeof remove === "function") Reflect.apply(remove, globalThis, [type, listener]);
		}
		//#endregion
		//#region src/client/cdp/errors.ts
		/** Failure returned through the typed Client Runtime error outcome. */
		var ClientRuntimeExecutionError = class extends Error {
			code;
			constructor(code, message) {
				super(message);
				this.code = code;
			}
		};
		//#endregion
		//#region src/client/cdp/objects.ts
		/** Client-local object handles and CDP-compatible RemoteObject serialization. */
		const MAX_CLASS_PROTOTYPE_DEPTH = 32;
		/** Per-DevTools-session owner of all live Client object references. */
		var ClientObjectStore = class {
			maxObjects;
			objects = /* @__PURE__ */ new Map();
			groups = /* @__PURE__ */ new Map();
			allocations = /* @__PURE__ */ new Map();
			nextOrdinal = 1;
			constructor(maxObjects) {
				this.maxObjects = maxObjects;
			}
			/**
			* Start tracking handles allocated by one independently settling operation.
			* @returns An opaque allocation identity.
			*/
			beginAllocation() {
				const allocation = Symbol("Client Runtime object allocation");
				this.allocations.set(allocation, /* @__PURE__ */ new Set());
				return allocation;
			}
			/**
			* Keep an operation's handles and release its allocation bookkeeping.
			* @param allocation - Allocation returned by {@link beginAllocation}.
			*/
			commitAllocation(allocation) {
				this.allocations.delete(allocation);
			}
			/**
			* Resolve one handle or fail without exposing another session's objects.
			* @param handle - Client-local object handle.
			* @returns The retained JavaScript value.
			*/
			get(handle) {
				const object = this.objects.get(handle);
				if (object === void 0) throw new ClientRuntimeExecutionError("object-not-found", "Client RemoteObject was released");
				return object.value;
			}
			/**
			* Read the object group inherited by values reached through one handle.
			* @param handle - Client-local object handle.
			* @returns Its object group, or `undefined` when it is ungrouped.
			*/
			group(handle) {
				const object = this.objects.get(handle);
				if (object === void 0) throw new ClientRuntimeExecutionError("object-not-found", "Client RemoteObject was released");
				return object.group;
			}
			/**
			* Convert a live value to the JSON-safe RemoteObject protocol.
			* @param value - Value owned by this Client realm.
			* @param options - Object group and serialization options.
			* @param allocation - Optional operation that owns any newly retained handle.
			* @returns A primitive value or opaque Client handle with display metadata.
			*/
			serialize(value, options = {}, allocation) {
				const primitive = serializePrimitive(value);
				if (primitive !== void 0) return primitive;
				if (options.returnByValue === true) return { descriptor: {
					type: typeof value === "function" ? "function" : "object",
					value: serializeByValue(value),
					description: describe(value)
				} };
				const type = typeof value === "function" ? "function" : typeof value === "symbol" ? "symbol" : "object";
				const subtype = type === "object" ? subtypeOf(value) : void 0;
				const objectReference = identifyRealmObject(value);
				return {
					descriptor: {
						type,
						...subtype === void 0 ? {} : { subtype },
						className: className(value),
						description: describe(value),
						...options.generatePreview === true && type === "object" ? { preview: preview(value, type, subtype) } : {}
					},
					object: { handle: this.register(value, options.group, allocation) },
					...objectReference === void 0 ? {} : { semanticReference: objectReference }
				};
			}
			/**
			* Release exactly one handle. Releasing an unknown handle is idempotent.
			* @param handle - Client-local object handle.
			*/
			release(handle) {
				const object = this.objects.get(handle);
				if (object === void 0) return;
				this.objects.delete(handle);
				if (object.group === void 0) return;
				const members = this.groups.get(object.group);
				members?.delete(handle);
				if (members?.size === 0) this.groups.delete(object.group);
			}
			/**
			* Release every handle in one DevTools object group.
			* @param group - DevTools object-group name.
			*/
			releaseGroup(group) {
				const members = this.groups.get(group);
				if (members === void 0) return;
				for (const handle of members) this.objects.delete(handle);
				this.groups.delete(group);
			}
			/**
			* Discard exactly the handles allocated by one failed operation.
			* @param allocation - Allocation returned by {@link beginAllocation}.
			*/
			rollback(allocation) {
				const handles = this.allocations.get(allocation);
				if (handles === void 0) return;
				this.allocations.delete(allocation);
				for (const handle of handles) this.release(handle);
			}
			/** Release the whole DevTools session. */
			clear() {
				this.objects.clear();
				this.groups.clear();
				this.allocations.clear();
			}
			register(value, group, allocation) {
				if (this.objects.size >= this.maxObjects) throw new ClientRuntimeExecutionError("result-too-large", `Client Runtime retained-object limit ${String(this.maxObjects)} reached`);
				const ordinal = this.nextOrdinal++;
				const handle = inspectorId(`object-${String(ordinal)}`, "handle");
				this.objects.set(handle, {
					value,
					group
				});
				if (allocation !== void 0) this.allocations.get(allocation)?.add(handle);
				if (group !== void 0) {
					let members = this.groups.get(group);
					if (members === void 0) {
						members = /* @__PURE__ */ new Set();
						this.groups.set(group, members);
					}
					members.add(handle);
				}
				return handle;
			}
		};
		function serializePrimitive(value) {
			if (value === void 0) return { descriptor: { type: "undefined" } };
			if (value === null) return { descriptor: {
				type: "object",
				subtype: "null",
				value: null
			} };
			if (typeof value === "string") return { descriptor: {
				type: "string",
				value
			} };
			if (typeof value === "boolean") return { descriptor: {
				type: "boolean",
				value
			} };
			if (typeof value === "bigint") {
				const text = `${String(value)}n`;
				return { descriptor: {
					type: "bigint",
					unserializableValue: text,
					description: text
				} };
			}
			if (typeof value !== "number") return void 0;
			if (Number.isFinite(value) && !Object.is(value, -0)) return { descriptor: {
				type: "number",
				value,
				description: String(value)
			} };
			const text = Object.is(value, -0) ? "-0" : String(value);
			return { descriptor: {
				type: "number",
				unserializableValue: text,
				description: text
			} };
		}
		function serializeByValue(value) {
			let serialized;
			try {
				serialized = JSON.stringify(value);
			} catch (error) {
				throw new ClientRuntimeExecutionError("unsupported", `Value cannot be returned by value: ${renderError$4(error)}`);
			}
			if (typeof serialized !== "string") throw new ClientRuntimeExecutionError("unsupported", "Value cannot be returned by value");
			const result = JSON.parse(serialized);
			if (!isJsonValue(result)) throw new ClientRuntimeExecutionError("unsupported", "Value is outside the JSON value set");
			return result;
		}
		function preview(value, type, subtype) {
			const properties = [];
			let overflow = false;
			if (typeof value === "object" && value !== null || typeof value === "function") {
				let keys = [];
				try {
					keys = Reflect.ownKeys(value);
				} catch {
					overflow = true;
				}
				for (const key of keys) {
					if (properties.length === 5) {
						overflow = true;
						break;
					}
					let descriptor;
					try {
						descriptor = Reflect.getOwnPropertyDescriptor(value, key);
					} catch {
						continue;
					}
					if (descriptor === void 0) continue;
					if (!("value" in descriptor)) {
						properties.push({
							name: String(key),
							type: "accessor"
						});
						continue;
					}
					const propertyType = remoteType(descriptor.value);
					const propertySubtype = propertyType === "object" ? subtypeOf(descriptor.value) : void 0;
					properties.push({
						name: String(key),
						type: propertyType,
						value: previewText(descriptor.value),
						...propertySubtype === void 0 ? {} : { subtype: propertySubtype }
					});
				}
			}
			return {
				type,
				...subtype === void 0 ? {} : { subtype },
				description: describe(value),
				overflow,
				properties
			};
		}
		function remoteType(value) {
			if (value === null) return "object";
			return typeof value;
		}
		function subtypeOf(value) {
			if (value === null) return "null";
			if (Array.isArray(value)) return "array";
			if (ArrayBuffer.isView(value)) return value instanceof DataView ? "dataview" : "typedarray";
			if (typeof value !== "object") return void 0;
			for (const [prototype, subtype] of SUBTYPES_BY_PROTOTYPE) if (inheritsFrom(value, prototype)) return subtype;
		}
		function className(value) {
			if (typeof value === "function") return functionName(value);
			if (typeof value === "symbol") return "Symbol";
			if (typeof value !== "object" || value === null) return "Object";
			const visited = /* @__PURE__ */ new Set();
			let prototype = prototypeOf(value);
			while (prototype !== null && visited.size < MAX_CLASS_PROTOTYPE_DEPTH && !visited.has(prototype)) {
				visited.add(prototype);
				const constructor = Reflect.getOwnPropertyDescriptor(prototype, "constructor");
				const candidate = constructor !== void 0 && "value" in constructor ? constructor.value : void 0;
				if (typeof candidate === "function") return functionName(candidate);
				prototype = prototypeOf(prototype);
			}
			return "Object";
		}
		function describe(value) {
			if (typeof value === "function") try {
				return Function.prototype.toString.call(value);
			} catch {
				return functionName(value);
			}
			const subtype = subtypeOf(value);
			if (subtype === "array") {
				const descriptor = Reflect.getOwnPropertyDescriptor(value, "length");
				const length = descriptor !== void 0 && "value" in descriptor ? descriptor.value : void 0;
				return `Array(${typeof length === "number" ? String(length) : "?"})`;
			}
			if (subtype === "error") {
				const stack = ownString(value, "stack");
				if (stack !== void 0) return stack;
				const name = ownString(value, "name") ?? className(value);
				const message = ownString(value, "message");
				return message === void 0 || message.length === 0 ? name : `${name}: ${message}`;
			}
			if (subtype === "date") try {
				return Date.prototype.toString.call(value);
			} catch {
				return "Date";
			}
			if (subtype === "regexp") try {
				return RegExp.prototype.toString.call(value);
			} catch {
				return "RegExp";
			}
			return className(value);
		}
		function previewText(value) {
			if (typeof value === "string") return value.slice(0, 100);
			if (typeof value === "number" || typeof value === "boolean" || typeof value === "bigint" || typeof value === "symbol") return String(value);
			if (value === null) return "null";
			if (value === void 0) return "undefined";
			return describe(value).slice(0, 100);
		}
		function functionName(value) {
			try {
				const descriptor = Reflect.getOwnPropertyDescriptor(value, "name");
				const name = descriptor !== void 0 && "value" in descriptor ? descriptor.value : void 0;
				return typeof name === "string" && name.length > 0 ? name : "Function";
			} catch {
				return "Function";
			}
		}
		function prototypeOf(value) {
			try {
				return Reflect.getPrototypeOf(value);
			} catch {
				return null;
			}
		}
		function inheritsFrom(value, expected) {
			const visited = /* @__PURE__ */ new Set();
			let current = prototypeOf(value);
			while (current !== null && visited.size < MAX_CLASS_PROTOTYPE_DEPTH && !visited.has(current)) {
				if (current === expected) return true;
				visited.add(current);
				current = prototypeOf(current);
			}
			return false;
		}
		function ownString(value, key) {
			try {
				const descriptor = Reflect.getOwnPropertyDescriptor(value, key);
				return descriptor !== void 0 && "value" in descriptor && typeof descriptor.value === "string" ? descriptor.value : void 0;
			} catch {
				return;
			}
		}
		function renderError$4(error) {
			return error instanceof Error ? error.message : String(error);
		}
		const SUBTYPES_BY_PROTOTYPE = [
			[RegExp.prototype, "regexp"],
			[Date.prototype, "date"],
			[Map.prototype, "map"],
			[Set.prototype, "set"],
			[WeakMap.prototype, "weakmap"],
			[WeakSet.prototype, "weakset"],
			[Error.prototype, "error"],
			[Promise.prototype, "promise"],
			[ArrayBuffer.prototype, "arraybuffer"],
			[DataView.prototype, "dataview"]
		];
		//#endregion
		//#region src/client/cdp/properties.ts
		/**
		* Read property descriptors without invoking getters.
		* @param objects - Object table that owns the requested handle.
		* @param command - Validated property request.
		* @param maxProperties - Maximum descriptors returned by this operation.
		* @param allocation - Current operation's object-allocation identity.
		* @returns Own or inherited descriptors and the immediate prototype.
		*/
		function getClientProperties(objects, command, maxProperties, allocation) {
			const raw = objects.get(command.handle);
			if (!isObjectLike(raw)) return { properties: [] };
			const value = typeof raw === "symbol" ? Symbol.prototype : raw;
			const group = objects.group(command.handle);
			const properties = [];
			const seen = /* @__PURE__ */ new Set();
			const visited = /* @__PURE__ */ new Set();
			let owner = value;
			let own = true;
			while (owner !== null) {
				if (visited.has(owner) || visited.size >= maxProperties) throw new ClientRuntimeExecutionError("result-too-large", "Client prototype traversal exceeded its configured limit");
				visited.add(owner);
				const keys = readKeys(owner);
				for (const key of keys) {
					if (seen.has(key)) continue;
					seen.add(key);
					if (command.nonIndexedPropertiesOnly === true && typeof key === "string" && isArrayIndex(key)) continue;
					const descriptor = readDescriptor(owner, key);
					if (descriptor === void 0) continue;
					if (command.accessorPropertiesOnly === true && "value" in descriptor) continue;
					if (properties.length >= maxProperties) throw new ClientRuntimeExecutionError("result-too-large", `Client property result exceeds the configured ${String(maxProperties)}-property limit`);
					properties.push(toRemoteDescriptor(objects, key, descriptor, group, own, command.generatePreview === true, allocation));
				}
				if (command.ownProperties === true) break;
				owner = readPrototype(owner);
				own = false;
			}
			if (command.accessorPropertiesOnly === true) return { properties };
			const prototype = readPrototype(value);
			return {
				properties,
				internalProperties: prototype === null ? [] : [{
					name: "[[Prototype]]",
					value: objects.serialize(prototype, remoteOptions(group, command.generatePreview), allocation)
				}]
			};
		}
		function toRemoteDescriptor(objects, key, descriptor, group, own, generatePreview, allocation) {
			const common = {
				name: typeof key === "symbol" ? key.description ?? String(key) : String(key),
				configurable: descriptor.configurable ?? false,
				enumerable: descriptor.enumerable ?? false,
				isOwn: own,
				...typeof key === "symbol" ? { symbol: objects.serialize(key, remoteOptions(group), allocation) } : {}
			};
			if ("value" in descriptor) return {
				...common,
				value: objects.serialize(descriptor.value, remoteOptions(group, generatePreview), allocation),
				writable: descriptor.writable ?? false
			};
			const getter = Reflect.get(descriptor, "get");
			const setter = Reflect.get(descriptor, "set");
			return {
				...common,
				...getter === void 0 ? {} : { get: objects.serialize(getter, remoteOptions(group), allocation) },
				...setter === void 0 ? {} : { set: objects.serialize(setter, remoteOptions(group), allocation) }
			};
		}
		function readKeys(value) {
			try {
				return Reflect.ownKeys(value);
			} catch (error) {
				throw new ClientRuntimeExecutionError("internal-error", `Cannot enumerate Client object: ${renderError$3(error)}`);
			}
		}
		function readDescriptor(value, key) {
			try {
				return Reflect.getOwnPropertyDescriptor(value, key);
			} catch (error) {
				throw new ClientRuntimeExecutionError("internal-error", `Cannot read Client property ${String(key)}: ${renderError$3(error)}`);
			}
		}
		function readPrototype(value) {
			try {
				return Object.getPrototypeOf(value);
			} catch (error) {
				throw new ClientRuntimeExecutionError("internal-error", `Cannot read Client object prototype: ${renderError$3(error)}`);
			}
		}
		function isObjectLike(value) {
			return typeof value === "object" && value !== null || typeof value === "function" || typeof value === "symbol";
		}
		function isArrayIndex(value) {
			const number = Number(value);
			return Number.isInteger(number) && number >= 0 && number < 4294967295 && String(number) === value;
		}
		function renderError$3(error) {
			return error instanceof Error ? error.message : String(error);
		}
		function remoteOptions(group, generatePreview) {
			return {
				...group === void 0 ? {} : { group },
				...generatePreview === void 0 ? {} : { generatePreview }
			};
		}
		//#endregion
		//#region src/client/cdp/runtime.ts
		const MAX_RUNTIME_ERROR_MESSAGE_LENGTH = 2048;
		/**
		* Describe browser-side Runtime execution.
		* @param origin - Origin assigned to the synthetic execution context.
		* @returns The Runtime capability advertised by a browser Client source.
		*/
		function runtimeBridgeCapability(origin) {
			return {
				type: "client-runtime",
				origin
			};
		}
		/** Executes Runtime requests while isolating object handles by DevTools session. */
		var ClientRuntimeExecutor = class {
			limits;
			resolveScript;
			sessions = /* @__PURE__ */ new Map();
			responseAllocations = /* @__PURE__ */ new Map();
			constructor(limits, resolveScript = () => void 0) {
				this.limits = limits;
				this.resolveScript = resolveScript;
			}
			/**
			* Execute one request and preserve its source, generation, session, and request identities.
			* @param frame - Validated command envelope from the Worker.
			* @param signal - Optional cancellation for an operation awaiting user code.
			* @param deferObjectCommit - Keep new object handles provisional until {@link acknowledge}.
			* @returns A success or transport-error response for the same request.
			*/
			async execute(frame, signal, deferObjectCommit = false) {
				const session = this.session(frame.sessionId);
				const allocation = session.beginAllocation();
				try {
					const result = await session.execute(frame.command, allocation, signal);
					if (signal?.aborted === true) throw new ClientRuntimeExecutionError("timeout", "Client Runtime request was canceled");
					const response = responseFrame(frame, {
						ok: true,
						result
					});
					if (!isJsonValue(response) || jsonByteLength(response) > this.limits.maxResponseBytes) {
						session.rollback(allocation);
						return responseFrame(frame, {
							ok: false,
							error: {
								code: "result-too-large",
								message: "Client Runtime result exceeds the source-frame byte limit"
							}
						});
					}
					if (deferObjectCommit) {
						if (this.responseAllocations.has(frame.requestId)) {
							session.rollback(allocation);
							return responseFrame(frame, {
								ok: false,
								error: {
									code: "invalid-request",
									message: "Client Runtime request id is already pending"
								}
							});
						}
						this.responseAllocations.set(frame.requestId, {
							sessionId: frame.sessionId,
							session,
							allocation
						});
					} else session.commitAllocation(allocation);
					return response;
				} catch (error) {
					session.rollback(allocation);
					return responseFrame(frame, {
						ok: false,
						error: runtimeError(error)
					});
				}
			}
			/**
			* Commit handles after the Worker accepts one Runtime response.
			* @param sessionId - Session that owns the response.
			* @param requestId - Correlation id acknowledged by the Worker.
			*/
			acknowledge(sessionId, requestId) {
				const pending = this.responseAllocations.get(requestId);
				if (pending === void 0 || pending.sessionId !== sessionId) return;
				this.responseAllocations.delete(requestId);
				pending.session.commitAllocation(pending.allocation);
			}
			/**
			* Roll back handles from a canceled or otherwise unaccepted Runtime response.
			* @param sessionId - Session that owns the response.
			* @param requestId - Correlation id rejected by the Worker.
			*/
			cancel(sessionId, requestId) {
				const pending = this.responseAllocations.get(requestId);
				if (pending === void 0 || pending.sessionId !== sessionId) return;
				this.responseAllocations.delete(requestId);
				pending.session.rollback(pending.allocation);
			}
			/**
			* Release all values retained for one closed DevTools connection.
			* @param sessionId - Runtime session owned by that DevTools connection.
			*/
			closeSession(sessionId) {
				for (const [requestId, pending] of this.responseAllocations) if (pending.sessionId === sessionId) this.responseAllocations.delete(requestId);
				this.sessions.get(sessionId)?.close();
				this.sessions.delete(sessionId);
			}
			/**
			* Release one object group without closing the surrounding Runtime session.
			* @param sessionId - Session that owns the retained objects.
			* @param group - Object-group name to release.
			*/
			releaseObjectGroup(sessionId, group) {
				this.sessions.get(sessionId)?.releaseObjectGroup(group);
			}
			/**
			* Serialize one Console call for a specific DevTools Runtime session.
			* @param sessionId - Session receiving the Console event.
			* @param type - Console API operation.
			* @param values - Original arguments from the page call.
			* @param timestamp - Epoch timestamp in milliseconds.
			* @param stackTrace - Browser call frames captured before deferred delivery.
			* @returns A wire-safe event whose object handles belong only to this session.
			*/
			consoleEvent(sessionId, type, values, timestamp, stackTrace) {
				const session = this.session(sessionId);
				const allocation = session.beginAllocation();
				try {
					const event = {
						type: "console-api",
						event: {
							type,
							arguments: session.serializeAll(values, "console", allocation),
							timestamp,
							...stackTrace === void 0 ? {} : { stackTrace }
						}
					};
					if (!isJsonValue(event) || jsonByteLength(event) + 4096 > this.limits.maxResponseBytes) {
						session.rollback(allocation);
						return;
					}
					session.commitAllocation(allocation);
					return event;
				} catch (error) {
					session.rollback(allocation);
					throw error;
				}
			}
			/**
			* Serialize one uncaught Client exception for a DevTools Runtime session.
			* @param sessionId - Session receiving the exception event.
			* @param error - Thrown or rejected value.
			* @param timestamp - Epoch timestamp in milliseconds.
			* @param stackTrace - Browser call frames attached to the failure.
			* @returns A wire-safe exception event.
			*/
			exceptionEvent(sessionId, error, timestamp, stackTrace) {
				const session = this.session(sessionId);
				const allocation = session.beginAllocation();
				try {
					const event = {
						type: "exception",
						event: {
							timestamp,
							details: session.describeException(error, "console", stackTrace, allocation)
						}
					};
					if (!isJsonValue(event) || jsonByteLength(event) + 4096 > this.limits.maxResponseBytes) {
						session.rollback(allocation);
						return;
					}
					session.commitAllocation(allocation);
					return event;
				} catch (serializationError) {
					session.rollback(allocation);
					throw serializationError;
				}
			}
			/** Release all sessions when a source generation ends or reconnects. */
			reset() {
				this.responseAllocations.clear();
				for (const session of this.sessions.values()) session.close();
				this.sessions.clear();
			}
			session(sessionId) {
				let session = this.sessions.get(sessionId);
				if (session === void 0) {
					session = new ClientRuntimeSession(this.limits.maxObjectsPerSession, this.limits.maxPropertiesPerResult, this.resolveScript);
					this.sessions.set(sessionId, session);
				}
				return session;
			}
		};
		var ClientRuntimeSession = class {
			maxProperties;
			resolveScript;
			objects;
			constructor(maxObjects, maxProperties, resolveScript) {
				this.maxProperties = maxProperties;
				this.resolveScript = resolveScript;
				this.objects = new ClientObjectStore(maxObjects);
			}
			beginAllocation() {
				return this.objects.beginAllocation();
			}
			commitAllocation(allocation) {
				this.objects.commitAllocation(allocation);
			}
			rollback(allocation) {
				this.objects.rollback(allocation);
			}
			async execute(command, allocation, signal) {
				switch (command.op) {
					case "evaluate": return {
						op: command.op,
						completion: await this.evaluate(command, allocation, signal)
					};
					case "get-properties": {
						const result = getClientProperties(this.objects, command, this.maxProperties, allocation);
						return {
							op: command.op,
							...result
						};
					}
					case "call-function": return {
						op: command.op,
						completion: await this.callFunction(command, allocation, signal)
					};
					case "await-promise": return {
						op: command.op,
						completion: await this.awaitPromise(command, allocation, signal)
					};
					case "release-object":
						this.objects.release(command.handle);
						return { op: command.op };
					case "release-object-group":
						this.releaseObjectGroup(command.objectGroup);
						return { op: command.op };
					case "global-lexical-scope-names": return {
						op: command.op,
						names: []
					};
					default: return assertNever$1(command);
				}
			}
			close() {
				this.objects.clear();
			}
			releaseObjectGroup(group) {
				this.objects.releaseGroup(group);
			}
			serializeAll(values, group, allocation) {
				return values.map((value) => this.objects.serialize(value, {
					group,
					generatePreview: true
				}, allocation));
			}
			describeException(error, group, stackTrace, allocation) {
				const options = { ...group === void 0 ? {} : { group } };
				const resolvedStackTrace = stackTrace ?? clientErrorStack(error, this.resolveScript);
				const firstFrame = resolvedStackTrace?.callFrames[0];
				return {
					text: "Uncaught",
					lineNumber: firstFrame?.lineNumber ?? 0,
					columnNumber: firstFrame?.columnNumber ?? 0,
					...firstFrame === void 0 ? clientUrl() : { url: firstFrame.url },
					...resolvedStackTrace === void 0 ? {} : { stackTrace: resolvedStackTrace },
					exception: this.objects.serialize(error, options, allocation)
				};
			}
			async evaluate(command, allocation, signal) {
				let value;
				try {
					value = globalThis.eval(command.expression);
					if (command.awaitPromise === true) value = await awaitWithCancellation(value, signal, command.timeoutMs);
				} catch (error) {
					if (error instanceof ClientRuntimeExecutionError) throw error;
					return this.exception(error, command.objectGroup, allocation);
				}
				return this.completion(value, allocation, command.objectGroup, command.generatePreview, command.returnByValue);
			}
			async callFunction(command, allocation, signal) {
				const receiver = command.receiver === void 0 ? globalThis : this.objects.get(command.receiver);
				const inheritedGroup = command.receiver === void 0 ? void 0 : this.objects.group(command.receiver);
				const group = command.objectGroup ?? inheritedGroup;
				const args = (command.arguments ?? []).map((argument) => this.resolveArgument(argument));
				let value;
				try {
					const fn = globalThis.eval(`(${command.functionDeclaration}\n)`);
					if (typeof fn !== "function") throw new TypeError("functionDeclaration did not evaluate to a function");
					value = Reflect.apply(fn, receiver, args);
					if (command.awaitPromise === true) value = await awaitWithCancellation(value, signal);
				} catch (error) {
					if (error instanceof ClientRuntimeExecutionError) throw error;
					return this.exception(error, group, allocation);
				}
				return this.completion(value, allocation, group, command.generatePreview, command.returnByValue);
			}
			async awaitPromise(command, allocation, signal) {
				const group = this.objects.group(command.promise);
				let value;
				try {
					value = await awaitWithCancellation(this.objects.get(command.promise), signal);
				} catch (error) {
					if (error instanceof ClientRuntimeExecutionError) throw error;
					return this.exception(error, group, allocation);
				}
				return this.completion(value, allocation, group, command.generatePreview, command.returnByValue);
			}
			resolveArgument(argument) {
				switch (argument.kind) {
					case "value": return argument.value;
					case "object": return this.objects.get(argument.handle);
					case "undefined": return;
					case "unserializable": return parseUnserializable(argument.value);
					default: return assertNever$1(argument);
				}
			}
			exception(error, group, allocation) {
				const options = { ...group === void 0 ? {} : { group } };
				const details = this.describeException(error, group, void 0, allocation);
				return {
					result: this.objects.serialize(error, options, allocation),
					exceptionDetails: details
				};
			}
			completion(value, allocation, group, generatePreview, returnByValue) {
				return { result: this.objects.serialize(value, {
					...group === void 0 ? {} : { group },
					...generatePreview === void 0 ? {} : { generatePreview },
					...returnByValue === void 0 ? {} : { returnByValue }
				}, allocation) };
			}
		};
		function responseFrame(request, outcome) {
			return {
				v: 0,
				t: "client-runtime/response",
				sourceId: request.sourceId,
				generation: request.generation,
				sessionId: request.sessionId,
				requestId: request.requestId,
				outcome
			};
		}
		function runtimeError(error) {
			return {
				code: error instanceof ClientRuntimeExecutionError ? error.code : "internal-error",
				message: (error instanceof Error ? error.message : String(error)).slice(0, MAX_RUNTIME_ERROR_MESSAGE_LENGTH)
			};
		}
		function parseUnserializable(value) {
			if (value === "NaN") return NaN;
			if (value === "Infinity") return Number.POSITIVE_INFINITY;
			if (value === "-Infinity") return Number.NEGATIVE_INFINITY;
			if (value === "-0") return -0;
			if (/^-?(?:0|[1-9]\d*)n$/u.test(value)) return BigInt(value.slice(0, -1));
			throw new ClientRuntimeExecutionError("invalid-request", `Unsupported unserializable value ${JSON.stringify(value)}`);
		}
		function clientUrl() {
			const location = Reflect.get(globalThis, "location");
			if (typeof location !== "object" || location === null) return {};
			const href = Reflect.get(location, "href");
			return typeof href === "string" ? { url: href } : {};
		}
		async function awaitWithCancellation(value, signal, timeoutMs) {
			if (signal?.aborted === true) throw new ClientRuntimeExecutionError("timeout", "Client Runtime request was canceled");
			let timer;
			let onAbort;
			try {
				const limits = [];
				if (timeoutMs !== void 0) limits.push(new Promise((_resolve, reject) => {
					timer = setTimeout(() => {
						reject(new ClientRuntimeExecutionError("timeout", `Client evaluation exceeded ${String(timeoutMs)}ms`));
					}, timeoutMs);
				}));
				if (signal !== void 0) limits.push(new Promise((_resolve, reject) => {
					onAbort = () => {
						reject(new ClientRuntimeExecutionError("timeout", "Client Runtime request was canceled"));
					};
					signal.addEventListener("abort", onAbort, { once: true });
				}));
				return await Promise.race([Promise.resolve(value), ...limits]);
			} finally {
				if (timer !== void 0) clearTimeout(timer);
				if (onAbort !== void 0) signal?.removeEventListener("abort", onAbort);
			}
		}
		function assertNever$1(value) {
			throw new Error(`Unexpected Client Runtime variant: ${JSON.stringify(value)}`);
		}
		//#endregion
		//#region src/client/cdp/sources.ts
		/** Browser-side catalog for the Inspector Client bundle and its source map. */
		const PACKAGE_ID = "@deepseek-ai/dsh-experimental-inspector";
		const CLIENT_SCRIPT_KEY = inspectorId("client-bundle", "scriptKey");
		/**
		* Describe browser-side source access.
		* @param available - Whether the Client bundle was discovered.
		* @returns The Sources capability when this Client discovered its bundle.
		*/
		function sourcesBridgeCapability(available) {
			return available ? { type: "client-sources" } : void 0;
		}
		/** Deliberate error serialized by the Client source transport. */
		var ClientSourceCatalogError = class extends Error {
			code;
			constructor(code, message) {
				super(message);
				this.code = code;
			}
		};
		/** Executes bounded, read-only operations over Client script assets. */
		var ClientSourceCatalog = class {
			assets = /* @__PURE__ */ new Map();
			constructor(assets) {
				for (const asset of assets) {
					if (this.assets.has(asset.scriptKey)) throw new Error(`inspector: duplicate Client script key ${asset.scriptKey}`);
					this.assets.set(asset.scriptKey, { asset });
				}
			}
			/**
			* Resolve a stack-frame URL to this catalog's local script key.
			* @param url - Absolute or page-relative stack-frame URL.
			* @returns The matching script key when the URL belongs to this catalog.
			*/
			scriptKeyForUrl(url) {
				const normalized = normalizedUrl(url);
				for (const entry of this.assets.values()) if (normalizedUrl(entry.asset.url) === normalized) return entry.asset.scriptKey;
			}
			/**
			* Execute one validated source operation.
			* @param command - Read-only catalog command.
			* @param maxContentBytes - Maximum encoded bytes admitted for one asset.
			* @returns Script metadata or one bounded content chunk.
			*/
			async execute(command, maxContentBytes) {
				if (command.op === "list-scripts") return {
					op: command.op,
					scripts: await Promise.all([...this.assets.values()].map(async (entry) => this.describe(entry, maxContentBytes)))
				};
				const entry = this.assets.get(command.scriptKey);
				if (entry === void 0) throw new ClientSourceCatalogError("script-not-found", "Client script is not available");
				const bytes = command.content === "source" ? await this.sourceBytes(entry, maxContentBytes) : await this.sourceMapBytes(entry, maxContentBytes);
				if (bytes === void 0) return {
					op: command.op,
					scriptKey: command.scriptKey,
					content: command.content,
					available: false
				};
				if (command.offset > bytes.byteLength) throw new ClientSourceCatalogError("invalid-request", "Client source chunk offset exceeds content length");
				const nextOffset = Math.min(bytes.byteLength, command.offset + command.maxBytes);
				return {
					op: command.op,
					scriptKey: command.scriptKey,
					content: command.content,
					available: true,
					offset: command.offset,
					nextOffset,
					data: bytesToBase64(bytes.subarray(command.offset, nextOffset)),
					eof: nextOffset === bytes.byteLength
				};
			}
			async describe(entry, maxContentBytes) {
				const source = await this.source(entry, maxContentBytes);
				const newline = source.lastIndexOf("\n");
				return {
					scriptKey: entry.asset.scriptKey,
					url: entry.asset.url,
					hash: entry.asset.hash,
					buildId: "",
					...entry.asset.sourceMapUrl === void 0 ? {} : { sourceMapUrl: entry.asset.sourceMapUrl },
					startLine: 0,
					startColumn: 0,
					endLine: countNewlines(source),
					endColumn: newline === -1 ? source.length : source.length - newline - 1,
					...entry.asset.isModule === void 0 ? {} : { isModule: entry.asset.isModule },
					length: source.length
				};
			}
			source(entry, maxContentBytes) {
				entry.source ??= entry.asset.loadSource().catch((error) => {
					throw new ClientSourceCatalogError("load-failed", `Cannot load Client script: ${renderError$2(error)}`);
				});
				return entry.source.then((source) => {
					if (new TextEncoder().encode(source).byteLength > maxContentBytes) throw new ClientSourceCatalogError("result-too-large", "Client script exceeds the configured content limit");
					return source;
				});
			}
			sourceBytes(entry, maxContentBytes) {
				entry.sourceBytes ??= this.source(entry, maxContentBytes).then((source) => new TextEncoder().encode(source));
				return entry.sourceBytes;
			}
			sourceMapBytes(entry, maxContentBytes) {
				if (entry.asset.loadSourceMap === void 0) return Promise.resolve(void 0);
				entry.sourceMapBytes ??= entry.asset.loadSourceMap().then((value) => value === void 0 ? void 0 : new TextEncoder().encode(value)).catch((error) => {
					throw new ClientSourceCatalogError("load-failed", `Cannot load Client source map: ${renderError$2(error)}`);
				});
				return entry.sourceMapBytes.then((bytes) => {
					if (bytes !== void 0 && bytes.byteLength > maxContentBytes) throw new ClientSourceCatalogError("result-too-large", "Client source map exceeds the configured content limit");
					return bytes;
				});
			}
		};
		/**
		* Discover this package's bundle URL from the Host-injected web boot graph.
		* @returns A lazy catalog, or `undefined` outside the assembled web application.
		*/
		function discoverInspectorClientSourceCatalog() {
			const graph = Reflect.get(globalThis, "__DSH_BOOT__");
			if (typeof graph !== "object" || graph === null) return void 0;
			const entries = Reflect.get(graph, "entries");
			if (!Array.isArray(entries)) return void 0;
			const row = entries.find((value) => {
				if (typeof value !== "object" || value === null) return false;
				return Reflect.get(value, "id") === PACKAGE_ID;
			});
			if (row === void 0 || typeof row.url !== "string" || typeof row.rev !== "string") return void 0;
			const base = documentBase();
			if (base === void 0) return void 0;
			const sourceUrl = new URL(row.url, base);
			const sourceMapUrl = new URL(sourceUrl.href);
			sourceMapUrl.pathname = `${sourceMapUrl.pathname}.map`;
			return new ClientSourceCatalog([{
				scriptKey: CLIENT_SCRIPT_KEY,
				url: sourceUrl.href,
				hash: row.rev,
				sourceMapUrl: sourceMapUrl.href,
				isModule: false,
				loadSource: async () => fetchText(sourceUrl.href),
				loadSourceMap: async () => fetchText(sourceMapUrl.href)
			}]);
		}
		async function fetchText(url) {
			const response = await fetch(url);
			if (!response.ok) throw new Error(`${String(response.status)} ${response.statusText}`);
			return response.text();
		}
		/**
		* Base every app-owned route reference resolves against: the document's
		* `baseURI`, else the location URL, else undefined outside a browser.
		*/
		function documentBase() {
			const document = Reflect.get(globalThis, "document");
			if (typeof document === "object" && document !== null) {
				const baseURI = Reflect.get(document, "baseURI");
				if (typeof baseURI === "string" && baseURI !== "") return baseURI;
			}
			const location = Reflect.get(globalThis, "location");
			if (typeof location !== "object" || location === null) return void 0;
			const href = Reflect.get(location, "href");
			return typeof href === "string" ? href : void 0;
		}
		function countNewlines(value) {
			let count = 0;
			for (let index = 0; index < value.length; index++) if (value.charCodeAt(index) === 10) count++;
			return count;
		}
		function renderError$2(error) {
			return error instanceof Error ? error.message : String(error);
		}
		function normalizedUrl(value) {
			try {
				const url = new URL(value, documentBase());
				url.hash = "";
				return url.href;
			} catch {
				return value;
			}
		}
		//#endregion
		//#region src/client/cdp/index.ts
		/**
		* Describe Client operations that require Worker-to-page bridge messages.
		* @param origin - Origin assigned to the synthetic execution context.
		* @param hasSources - Whether the Client bundle source was discovered.
		* @returns Capabilities included in the Client source handshake.
		*/
		function bridgeCapabilities(origin, hasSources) {
			return [
				runtimeBridgeCapability(origin),
				consoleBridgeCapability(),
				sourcesBridgeCapability(hasSources),
				void 0,
				void 0,
				void 0
			].filter((capability) => capability !== void 0);
		}
		//#endregion
		//#region src/client/inspection/realm.ts
		/** Stable Client source identity with a fresh descriptor for each WebSocket generation. */
		const CLIENT_SOURCE_STORAGE_KEY = "dsh.experimental-inspector.client-source-id.v0";
		const CLIENT_SOURCE_LOCK_PREFIX = "dsh.experimental-inspector.client-source:";
		/** Owns one browser realm's stable source id across transport reconnects. */
		var ClientRealmSource = class ClientRealmSource {
			label;
			releaseClaim;
			/** Logical source id retained across reconnecting transport generations. */
			sourceId;
			constructor(label, sourceId = sessionClientSourceId(), releaseClaim) {
				this.label = label;
				this.releaseClaim = releaseClaim;
				this.sourceId = sourceId;
			}
			/**
			* Claim the tab identity before opening its source transport. Browsers with
			* Web Locks reject a copied `sessionStorage` identity while its original tab
			* remains live; a fresh id is persisted and claimed instead.
			* @param label - Human-readable Client label reported to the Worker.
			* @returns The claimed realm source.
			*/
			static async claim(label) {
				let sourceId = sessionClientSourceId();
				const locks = browserLockManager();
				if (locks === void 0) return new ClientRealmSource(label, sourceId);
				while (true) {
					const release = await tryClaimSourceId(locks, sourceId);
					if (release !== void 0) {
						persistClientSourceId(sourceId);
						return new ClientRealmSource(label, sourceId, release);
					}
					sourceId = generatedClientSourceId();
				}
			}
			/**
			* Create the descriptor for one newly admitted transport generation.
			* @param hasSources - Whether the built Client bundle is available for source reads.
			* @returns A source descriptor with a fresh generation.
			*/
			connect(hasSources) {
				return {
					sourceId: this.sourceId,
					generation: inspectorId(randomUUID(), "generation"),
					kind: "client",
					label: this.label,
					timeOriginMs: performance.timeOrigin,
					capabilities: bridgeCapabilities(clientOrigin(), hasSources)
				};
			}
			/** Release this page's identity claim. */
			close() {
				this.releaseClaim?.();
				this.releaseClaim = void 0;
			}
		};
		function sessionClientSourceId() {
			const generated = generatedClientSourceId();
			try {
				const stored = sessionStorage.getItem(CLIENT_SOURCE_STORAGE_KEY);
				if (stored !== null) try {
					return inspectorId(stored, "sourceId");
				} catch {}
				sessionStorage.setItem(CLIENT_SOURCE_STORAGE_KEY, generated);
			} catch {}
			return generated;
		}
		function generatedClientSourceId() {
			return inspectorId(`client-${randomUUID()}`, "sourceId");
		}
		function persistClientSourceId(sourceId) {
			try {
				sessionStorage.setItem(CLIENT_SOURCE_STORAGE_KEY, sourceId);
			} catch {}
		}
		function browserLockManager() {
			if (typeof navigator === "undefined") return void 0;
			return navigator.locks;
		}
		function tryClaimSourceId(locks, sourceId) {
			return new Promise((resolve, reject) => {
				let release;
				const held = new Promise((released) => {
					release = released;
				});
				locks.request(`${CLIENT_SOURCE_LOCK_PREFIX}${sourceId}`, { ifAvailable: true }, async (lock) => {
					if (lock === null) {
						resolve(void 0);
						return;
					}
					resolve(release);
					await held;
				}).catch(reject);
			});
		}
		function clientOrigin() {
			const location = Reflect.get(globalThis, "location");
			if (typeof location !== "object" || location === null) return "";
			const origin = Reflect.get(location, "origin");
			return typeof origin === "string" ? origin : "";
		}
		//#endregion
		//#region src/client/inspection/network.ts
		/** Client network observation is not enabled in the current source producer. */
		/** Observation topics published by the Client network adapter. */
		const NETWORK_TOPICS = [];
		//#endregion
		//#region src/client/bridge/lifecycle.ts
		/** Reconnection lifecycle for the browser Client bridge. */
		/** Owns one bounded-backoff timer and prevents reconnection after disposal. */
		var ClientBridgeLifecycle = class {
			baseDelayMs;
			maxDelayMs;
			reconnectAttempt = 0;
			reconnectTimer;
			closed = false;
			constructor(baseDelayMs, maxDelayMs) {
				this.baseDelayMs = baseDelayMs;
				this.maxDelayMs = maxDelayMs;
			}
			/** Reset backoff after the Worker accepts a source generation. */
			connected() {
				this.reconnectAttempt = 0;
			}
			/**
			* Schedule the next reconnect attempt unless one is already pending.
			* @param connect - Operation that opens the next transport generation.
			*/
			reconnect(connect) {
				if (this.reconnectTimer !== void 0 || this.closed) return;
				const cap = Math.min(this.maxDelayMs, this.baseDelayMs * 2 ** this.reconnectAttempt);
				this.reconnectAttempt++;
				this.reconnectTimer = setTimeout(() => {
					this.reconnectTimer = void 0;
					connect();
				}, cap / 2 + Math.random() * cap / 2);
			}
			/** Stop pending and future reconnect attempts. */
			close() {
				if (this.closed) return;
				this.closed = true;
				if (this.reconnectTimer !== void 0) clearTimeout(this.reconnectTimer);
				this.reconnectTimer = void 0;
			}
		};
		//#endregion
		//#region src/shared/bridge/buffer.ts
		const SOURCE_FRAME_OVERHEAD_BYTES = 4096;
		/**
		* Owns retained state, queued events, and source-local sequencing independently
		* of whether frames travel over MessagePort or WebSocket.
		*/
		var InspectorSourceBuffer = class {
			options;
			queue = [];
			state = /* @__PURE__ */ new Map();
			queuedBytes = 0;
			nextSequence = 1;
			expectedSequence = 1;
			constructor(options) {
				this.options = options;
			}
			/** Whether at least one observation is waiting for transport. */
			get hasPending() {
				return this.queue.length > 0;
			}
			/**
			* Validate and enqueue one observation, dropping the oldest prefix as needed.
			* A record larger than one transport frame is dropped after consuming its sequence number.
			* @param topic - Declared domain topic.
			* @param payload - Lossless JSON payload.
			* @param monotonicMs - Finite source-clock timestamp.
			*/
			publish(topic, payload, monotonicMs) {
				this.enqueue(this.record(topic, payload, monotonicMs));
			}
			/**
			* Replace one retained topic and enqueue the same observation for live delivery.
			* @param topic - Declared state topic.
			* @param payload - Lossless JSON payload retained for replacement frames.
			* @param monotonicMs - Finite source-clock timestamp.
			*/
			setState(topic, payload, monotonicMs) {
				const record = this.record(topic, payload, monotonicMs);
				const previous = this.state.get(topic);
				this.state.set(topic, record);
				if (!this.stateFits()) {
					if (previous === void 0) this.state.delete(topic);
					else this.state.set(topic, previous);
					throw new Error("inspector: source state exceeds the source-frame byte limit");
				}
				this.enqueue(record);
			}
			/**
			* Build a complete state replacement and absorb every preceding queue drop.
			* @param sourceId - Logical source identity.
			* @param generation - Current transport generation.
			* @returns A replacement frame whose sequence is the next append position.
			*/
			replacement(sourceId, generation) {
				const nextSequence = this.queue[0]?.sequence ?? this.nextSequence;
				this.expectedSequence = nextSequence;
				return {
					v: 0,
					t: "source/replace",
					sourceId,
					generation,
					nextSequence,
					records: [...this.state.values()]
				};
			}
			/**
			* Remove and sequence the next transport-sized observation batch.
			* @param sourceId - Logical source identity.
			* @param generation - Current transport generation.
			* @returns The next append frame, or `undefined` when the queue is empty.
			*/
			takeBatch(sourceId, generation) {
				if (this.queue.length === 0) return void 0;
				const batch = [];
				let batchBytes = SOURCE_FRAME_OVERHEAD_BYTES;
				const first = this.queue[0];
				while (batch.length < this.options.maxRecordsPerFrame && this.queue.length > 0) {
					const candidate = this.queue[0];
					if (candidate.sequence !== first.sequence + batch.length) break;
					if (batch.length > 0 && batchBytes + candidate.bytes > this.options.maxFrameBytes) break;
					this.queue.shift();
					batch.push(candidate);
					batchBytes += candidate.bytes;
				}
				this.queuedBytes -= batch.reduce((sum, item) => sum + item.bytes, 0);
				const firstSequence = first.sequence;
				const frame = {
					v: 0,
					t: "source/append",
					sourceId,
					generation,
					firstSequence,
					droppedBefore: firstSequence - this.expectedSequence,
					records: batch.map((item) => item.record)
				};
				this.expectedSequence = firstSequence + frame.records.length;
				return frame;
			}
			/** Discard observations that have not entered a transport frame. */
			discardPending() {
				this.queue.length = 0;
				this.queuedBytes = 0;
			}
			record(topic, payload, monotonicMs) {
				if (topic.length === 0 || topic.length > 128) throw new Error("inspector: topic must contain 1 to 128 characters");
				if (!this.options.topics.includes("*") && !this.options.topics.includes(topic)) throw new Error(`inspector: source does not declare topic ${JSON.stringify(topic)}`);
				if (!isJsonValue(payload)) throw new Error("inspector: source payload must be lossless JSON data");
				if (!Number.isFinite(monotonicMs)) throw new Error("inspector: monotonicMs must be finite");
				return {
					monotonicMs,
					topic,
					payload
				};
			}
			enqueue(record) {
				const bytes = jsonByteLength(record);
				const sequence = this.nextSequence++;
				if (bytes + SOURCE_FRAME_OVERHEAD_BYTES > this.options.maxFrameBytes) return;
				this.queue.push({
					sequence,
					bytes,
					record
				});
				this.queuedBytes += bytes;
				while (this.queue.length > this.options.maxQueuedRecords || this.queuedBytes > this.options.maxQueuedBytes) {
					const dropped = this.queue.shift();
					this.queuedBytes -= dropped.bytes;
				}
			}
			stateFits() {
				return jsonByteLength([...this.state.values()]) + SOURCE_FRAME_OVERHEAD_BYTES <= this.options.maxFrameBytes;
			}
		};
		//#endregion
		//#region src/client/bridge/publisher.ts
		/** Buffered Client observation publication across reconnecting WebSockets. */
		/** Non-blocking Client publisher whose bounded state survives transport reconnects. */
		var ClientBridgePublisher = class {
			maxBufferedBytes;
			records;
			active;
			flushTimer;
			closed = false;
			constructor(options, maxBufferedBytes) {
				this.maxBufferedBytes = maxBufferedBytes;
				this.records = new InspectorSourceBuffer(options);
			}
			publish(topic, payload, monotonicMs = performance.now()) {
				if (this.closed) return;
				this.records.publish(topic, payload, monotonicMs);
				this.flush();
			}
			setState(topic, payload, monotonicMs = performance.now()) {
				if (this.closed) throw new Error("inspector: Client source is closed");
				this.records.setState(topic, payload, monotonicMs);
				this.flush();
			}
			/**
			* Install one unopened transport generation.
			* @param socket - WebSocket carrying the generation.
			* @param source - Source identity and generation sent by the socket.
			*/
			connect(socket, source) {
				this.active = {
					socket,
					source,
					accepted: false
				};
			}
			/**
			* Send retained state and queued observations after Worker acceptance.
			* @param socket - Accepted active WebSocket.
			*/
			accept(socket) {
				const active = this.active;
				if (active?.socket !== socket) return;
				active.accepted = true;
				this.replace(socket);
				this.flush();
			}
			/**
			* Resend retained state for the active generation.
			* @param socket - WebSocket that received the resnapshot request.
			*/
			replace(socket) {
				const active = this.active;
				if (active?.socket !== socket || socket.readyState !== WebSocket.OPEN) return;
				socket.send(JSON.stringify(this.records.replacement(active.source.sourceId, active.source.generation)));
			}
			/**
			* Forget one closed transport while retaining buffered state for reconnect.
			* @param socket - WebSocket whose close event fired.
			*/
			disconnect(socket) {
				if (this.active?.socket === socket) this.active = void 0;
			}
			/** Stop delayed writes and reject later publication. */
			close() {
				if (this.closed) return;
				this.closed = true;
				this.active = void 0;
				if (this.flushTimer !== void 0) clearTimeout(this.flushTimer);
				this.flushTimer = void 0;
			}
			flush() {
				const active = this.active;
				if (!active?.accepted || active.socket.readyState !== WebSocket.OPEN) return;
				if (active.socket.bufferedAmount > this.maxBufferedBytes) {
					this.scheduleFlush();
					return;
				}
				while (this.records.hasPending && active.socket.bufferedAmount <= this.maxBufferedBytes) {
					const frame = this.records.takeBatch(active.source.sourceId, active.source.generation);
					if (frame === void 0) break;
					active.socket.send(JSON.stringify(frame));
				}
				if (this.records.hasPending) this.scheduleFlush();
			}
			scheduleFlush() {
				if (this.flushTimer !== void 0 || this.closed) return;
				this.flushTimer = setTimeout(() => {
					this.flushTimer = void 0;
					this.flush();
				}, 25);
			}
		};
		/**
		* Decode a consumer-neutral tree received across an Inspector transport.
		* @param value - Untrusted query result value.
		* @returns A detached tree containing only public semantic fields.
		*/
		function parseCordisRuntimeTree(value) {
			const record = exactObject(value, [
				"schemaVersion",
				"host",
				"clients"
			], "Cordis runtime tree");
			if (record.schemaVersion !== 0 || !Array.isArray(record.clients)) throw new Error("inspector protocol: invalid Cordis runtime tree");
			const host = record.host === null ? null : parseRealm(record.host, "host");
			const clients = record.clients.map((client) => parseRealm(client, "client"));
			const sourceIds = /* @__PURE__ */ new Set();
			for (const realm of host === null ? clients : [host, ...clients]) {
				if (sourceIds.has(realm.source.sourceId)) throw new Error("inspector protocol: Cordis runtime tree repeats a sourceId");
				sourceIds.add(realm.source.sourceId);
			}
			return {
				schemaVersion: 0,
				host,
				clients
			};
		}
		function parseRealm(value, kind) {
			const record = exactObject(value, [
				"source",
				"connection",
				"revision",
				"truncated",
				"root"
			], "Cordis runtime realm");
			const source = exactObject(record.source, [
				"sourceId",
				"kind",
				"label"
			], "Cordis runtime source");
			if (source.kind !== kind || typeof source.label !== "string" || source.label.length === 0 || source.label.length > 256) throw new Error(`inspector protocol: invalid ${kind} Cordis runtime source`);
			if (!Number.isSafeInteger(record.revision) || record.revision < 1 || typeof record.truncated !== "boolean") throw new Error("inspector protocol: invalid Cordis runtime realm header");
			const root = parseNode(record.root, { fiberUids: /* @__PURE__ */ new Set() }, 0);
			if (root.kind !== "context") throw new Error("inspector protocol: Cordis runtime root must be a Context");
			return {
				source: {
					sourceId: wireId(source.sourceId, "sourceId"),
					kind,
					label: source.label
				},
				connection: parseConnection(record.connection),
				revision: record.revision,
				truncated: record.truncated,
				root
			};
		}
		function parseConnection(value) {
			if (!isPlainObject(value)) throw new Error("inspector protocol: Cordis runtime connection must be an object");
			if (value.state === "connected") {
				exactKeys(value, ["state"], "connected Cordis runtime connection");
				return { state: "connected" };
			}
			if (value.state === "disconnected" && typeof value.reason === "string") {
				exactKeys(value, ["state", "reason"], "disconnected Cordis runtime connection");
				return {
					state: "disconnected",
					reason: value.reason
				};
			}
			throw new Error("inspector protocol: invalid Cordis runtime connection");
		}
		function parseNode(value, state, depth) {
			if (depth > 256) throw new Error("inspector protocol: Cordis runtime tree exceeds the depth limit");
			if (!isPlainObject(value) || value.kind !== "context" && value.kind !== "fiber") throw new Error("inspector protocol: Cordis runtime node must have a known kind");
			const record = exactObject(value, value.kind === "fiber" ? [
				"kind",
				"uid",
				"children"
			] : ["kind", "children"], "Cordis runtime node");
			if (!Array.isArray(record.children)) throw new Error("inspector protocol: Cordis runtime node children must be an array");
			if (record.kind === "context") return {
				kind: "context",
				children: record.children.map((child) => parseNode(child, state, depth + 1))
			};
			if (!Number.isSafeInteger(record.uid) || record.uid < 1 || record.children.length !== 1) throw new Error("inspector protocol: invalid Cordis runtime Fiber");
			const uid = record.uid;
			if (state.fiberUids.has(uid)) throw new Error("inspector protocol: Cordis runtime tree repeats a Fiber uid");
			state.fiberUids.add(uid);
			const context = parseNode(record.children[0], state, depth + 1);
			if (context.kind !== "context") throw new Error("inspector protocol: Cordis runtime Fiber child must be a Context");
			return {
				kind: "fiber",
				uid,
				children: [context]
			};
		}
		//#endregion
		//#region src/shared/bridge/messages/query/codec.ts
		/** Exact decoders for non-CDP Inspector query frames. */
		/**
		* Test whether a decoded carrier value belongs to the query response protocol.
		* @param value - Decoded carrier value.
		* @returns Whether the query response decoder owns the value.
		*/
		function isInspectorQueryResponseEnvelope(value) {
			return isPlainObject(value) && value.t === "query/response";
		}
		/**
		* Decode one Worker-to-source query response.
		* @param value - Untrusted decoded carrier value.
		* @returns The detached, validated response frame.
		*/
		function parseInspectorQueryResponseFrame(value) {
			const record = exactObject(value, [
				"v",
				"t",
				"sourceId",
				"generation",
				"requestId",
				"outcome"
			], "query response");
			if (record.v !== 0 || record.t !== "query/response") throw new Error("inspector protocol: invalid query response envelope");
			return {
				v: 0,
				t: "query/response",
				sourceId: wireId(record.sourceId, "sourceId"),
				generation: wireId(record.generation, "generation"),
				requestId: wireId(record.requestId, "requestId"),
				outcome: parseOutcome(record.outcome)
			};
		}
		function parseResult(value) {
			if (!isPlainObject(value) || typeof value.op !== "string") throw new Error("inspector protocol: query result must have an op");
			switch (value.op) {
				case "cordis-tree/get":
					exactKeys(value, ["op", "tree"], "Cordis tree query result");
					return {
						op: "cordis-tree/get",
						tree: parseCordisRuntimeTree(value.tree)
					};
				default: throw new Error(`inspector protocol: unknown query result ${JSON.stringify(value.op)}`);
			}
		}
		function parseOutcome(value) {
			if (!isPlainObject(value) || typeof value.ok !== "boolean") throw new Error("inspector protocol: invalid query outcome");
			if (value.ok) {
				exactKeys(value, ["ok", "result"], "successful query outcome");
				return {
					ok: true,
					result: parseResult(value.result)
				};
			}
			exactKeys(value, ["ok", "error"], "failed query outcome");
			const error = exactObject(value.error, ["code", "message"], "query error");
			if (!QUERY_ERROR_CODES.has(error.code) || typeof error.message !== "string") throw new Error("inspector protocol: invalid query error");
			return {
				ok: false,
				error: {
					code: error.code,
					message: error.message
				}
			};
		}
		const QUERY_ERROR_CODES = new Set([
			"invalid-request",
			"stale-source",
			"result-too-large",
			"internal-error"
		]);
		//#endregion
		//#region src/shared/bridge/rpc.ts
		/** Shared Host/Client owner of correlated non-CDP query requests. */
		/** Failure deliberately returned by the Worker query handler. */
		var InspectorQueryRemoteError = class extends Error {
			code;
			constructor(code, message) {
				super(message);
				this.code = code;
			}
		};
		/** Correlates requests for one reconnecting Host or Client source. */
		var InspectorQueryConnection = class {
			options;
			pending = /* @__PURE__ */ new Map();
			active;
			nextRequestId = 0;
			closed = false;
			constructor(options) {
				this.options = options;
			}
			/**
			* Admit the source generation acknowledged by the Worker.
			* @param sourceId - Stable source identity.
			* @param generation - Newly accepted transport generation.
			* @param sender - Carrier writer valid for that generation.
			*/
			connect(sourceId, generation, sender) {
				if (this.closed) throw new Error("inspector query connection is closed");
				this.disconnect("Inspector source generation replaced");
				this.active = {
					sourceId,
					generation,
					sender
				};
			}
			/**
			* Execute a query against the currently accepted source generation.
			* @param query - Closed typed query command.
			* @returns The result with the same operation discriminant.
			*/
			request(query) {
				const active = this.active;
				if (this.closed || active === void 0) return Promise.reject(/* @__PURE__ */ new Error("Inspector query transport is not connected"));
				const requestId = inspectorId(`query-${String(++this.nextRequestId)}`, "requestId");
				const frame = {
					v: 0,
					t: "query/request",
					sourceId: active.sourceId,
					generation: active.generation,
					requestId,
					query
				};
				if (jsonByteLength(frame) > this.options.maxFrameBytes) return Promise.reject(/* @__PURE__ */ new Error(`Inspector query request exceeds ${String(this.options.maxFrameBytes)} bytes`));
				return new Promise((resolve, reject) => {
					const timer = setTimeout(() => {
						this.pending.delete(requestId);
						reject(/* @__PURE__ */ new Error(`Inspector query ${query.op} timed out after ${String(this.options.timeoutMs)}ms`));
					}, this.options.timeoutMs);
					this.pending.set(requestId, {
						op: query.op,
						resolve,
						reject,
						timer
					});
					try {
						active.sender.send(frame);
					} catch (error) {
						this.rejectPending(requestId, renderError$1(error));
					}
				});
			}
			/**
			* Consume a decoded carrier value when it is a query response.
			* @param value - Untrusted Worker-to-source value.
			* @returns Whether the value belonged to the query protocol.
			*/
			receive(value) {
				if (!isInspectorQueryResponseEnvelope(value)) return false;
				let frame;
				try {
					frame = parseInspectorQueryResponseFrame(value);
					if (jsonByteLength(frame) > this.options.maxFrameBytes) throw new Error(`inspector protocol: query response exceeds ${String(this.options.maxFrameBytes)} bytes`);
				} catch (error) {
					this.disconnect(`Invalid Inspector query response: ${renderError$1(error).message}`);
					throw error;
				}
				const pending = this.pending.get(frame.requestId);
				if (pending === void 0) return true;
				const active = this.active;
				if (active === void 0 || frame.sourceId !== active.sourceId || frame.generation !== active.generation) {
					this.rejectPending(frame.requestId, /* @__PURE__ */ new Error("Inspector query response source generation does not match"));
					return true;
				}
				if (!frame.outcome.ok) {
					this.rejectPending(frame.requestId, new InspectorQueryRemoteError(frame.outcome.error.code, frame.outcome.error.message));
					return true;
				}
				if (frame.outcome.result.op !== pending.op) {
					this.rejectPending(frame.requestId, /* @__PURE__ */ new Error(`Inspector query response op ${frame.outcome.result.op} does not match ${pending.op}`));
					return true;
				}
				clearTimeout(pending.timer);
				this.pending.delete(frame.requestId);
				pending.resolve(frame.outcome.result);
				return true;
			}
			/**
			* Reject active requests while permitting a later source generation.
			* @param reason - Failure reported to every pending caller.
			*/
			disconnect(reason) {
				this.active = void 0;
				for (const requestId of [...this.pending.keys()]) this.rejectPending(requestId, new Error(reason));
			}
			/**
			* Permanently reject requests and prevent later reconnection.
			* @param reason - Failure reported to every pending caller.
			*/
			close(reason = "Inspector query connection closed") {
				if (this.closed) return;
				this.closed = true;
				this.disconnect(reason);
			}
			rejectPending(requestId, error) {
				const pending = this.pending.get(requestId);
				if (pending === void 0) return;
				clearTimeout(pending.timer);
				this.pending.delete(requestId);
				pending.reject(error);
			}
		};
		function renderError$1(error) {
			return error instanceof Error ? error : new Error(String(error));
		}
		//#endregion
		//#region src/client/bridge/rpc.ts
		/** Owns query correlation across reconnecting Client source generations. */
		var ClientBridgeRpc = class extends InspectorQueryConnection {
			/**
			* Connect query writes to one accepted Client WebSocket generation.
			* @param source - Accepted source descriptor.
			* @param socket - Active source WebSocket.
			*/
			connectSocket(source, socket) {
				this.connect(source.sourceId, source.generation, { send: (frame) => {
					if (socket.readyState !== WebSocket.OPEN) throw new Error("Inspector Client query socket is not connected");
					socket.send(JSON.stringify(frame));
				} });
			}
		};
		//#endregion
		//#region src/client/bridge/dispatcher.ts
		/**
		* Dispatch one validated Worker frame without exposing transport details to domain adapters.
		* @param frame - Decoded Worker-to-source frame.
		* @param handlers - Browser-realm operations for each frame family.
		*/
		function dispatchBridgeFrame(frame, handlers) {
			switch (frame.t) {
				case "source/accepted":
					handlers.accepted(frame);
					return;
				case "source/append-acknowledged":
					handlers.acknowledged(frame);
					return;
				case "source/resnapshot":
					handlers.resnapshot(frame);
					return;
				case "source/rejected":
					handlers.rejected(frame);
					return;
				case "client-runtime/request":
					handlers.runtime(frame);
					return;
				case "client-runtime/cancel":
					handlers.runtimeCanceled(frame);
					return;
				case "client-runtime/response-acknowledged":
					handlers.runtimeAcknowledged(frame);
					return;
				case "client-runtime/session-closed":
					handlers.runtimeClosed(frame);
					return;
				case "client-console/enable":
					handlers.consoleEnabled(frame);
					return;
				case "client-console/disable":
					handlers.consoleDisabled(frame);
					return;
				case "client-sources/request":
					handlers.sources(frame);
					return;
				case "client-sources/session-closed":
					handlers.sourcesClosed(frame);
					return;
				default: return assertNever(frame);
			}
		}
		function assertNever(value) {
			throw new Error(`Unexpected Worker source frame: ${JSON.stringify(value)}`);
		}
		//#endregion
		//#region src/client/bridge/transport.ts
		/** Reconnecting Client source whose bounded queue never blocks page work. */
		var ClientInspectorSource = class extends InspectorSourceConnection {
			bootstrap;
			sourceCatalog;
			realmSource;
			publisher;
			socket;
			generation;
			accepted = false;
			closed = false;
			runtime;
			runtimeRequests = /* @__PURE__ */ new Map();
			console;
			queries;
			lifecycle;
			constructor(bootstrap, label = document.title || "Client", sourceCatalog = discoverInspectorClientSourceCatalog(), realmSource = new ClientRealmSource(label)) {
				super();
				this.bootstrap = bootstrap;
				this.sourceCatalog = sourceCatalog;
				this.realmSource = realmSource;
				this.lifecycle = new ClientBridgeLifecycle(bootstrap.reconnectBaseMs, bootstrap.reconnectMaxMs);
				this.publisher = new ClientBridgePublisher({
					topics: ["*"],
					maxQueuedRecords: bootstrap.maxQueuedRecords,
					maxQueuedBytes: bootstrap.maxQueuedBytes,
					maxRecordsPerFrame: bootstrap.maxRecordsPerFrame,
					maxFrameBytes: bootstrap.maxFrameBytes
				}, bootstrap.maxQueuedBytes);
				this.runtime = new ClientRuntimeExecutor({
					maxObjectsPerSession: bootstrap.maxRuntimeObjectsPerSession,
					maxPropertiesPerResult: bootstrap.maxRuntimePropertiesPerResult,
					maxResponseBytes: bootstrap.maxFrameBytes
				}, (url) => this.sourceCatalog?.scriptKeyForUrl(url));
				this.console = new ClientConsoleObserver(this.runtime, (sessionId, event) => {
					const socket = this.socket;
					const generation = this.generation;
					if (this.closed || !this.accepted || socket?.readyState !== WebSocket.OPEN || generation === void 0) return;
					const frame = {
						v: 0,
						t: "client-console/event",
						sourceId: this.realmSource.sourceId,
						generation,
						sessionId,
						event
					};
					if (!isJsonValue(frame) || jsonByteLength(frame) > this.bootstrap.maxFrameBytes) return;
					try {
						socket.send(JSON.stringify(frame));
					} catch {}
				}, (url) => this.sourceCatalog?.scriptKeyForUrl(url));
				this.queries = new ClientBridgeRpc({
					timeoutMs: bootstrap.queryTimeoutMs,
					maxFrameBytes: bootstrap.maxFrameBytes
				});
				this.connect();
			}
			/** Permanently stop reconnecting and close the active source generation. */
			close() {
				if (this.closed) return;
				this.closed = true;
				this.console.close();
				this.cancelRuntimeRequests();
				this.runtime.reset();
				this.queries.close("Inspector Client source closed");
				this.lifecycle.close();
				this.publisher.close();
				const socket = this.socket;
				const generation = this.generation;
				try {
					if (socket?.readyState === WebSocket.OPEN && generation !== void 0) {
						const frame = {
							v: 0,
							t: "source/close",
							sourceId: this.realmSource.sourceId,
							generation
						};
						socket.send(JSON.stringify(frame));
						socket.close(1e3, "Client source closed");
					} else socket?.close();
				} finally {
					this.socket = void 0;
					this.realmSource.close();
				}
			}
			connect() {
				if (this.closed) return;
				this.console.reset();
				this.cancelRuntimeRequests();
				this.runtime.reset();
				this.queries.disconnect("Inspector Client source reconnecting");
				const source = this.realmSource.connect(this.sourceCatalog !== void 0);
				const generation = source.generation;
				const socket = new WebSocket(this.bootstrap.endpoint, this.bootstrap.protocol);
				this.socket = socket;
				this.generation = generation;
				this.accepted = false;
				this.publisher.connect(socket, source);
				socket.addEventListener("open", () => {
					if (this.socket !== socket || this.closed) return;
					const frame = {
						v: 0,
						t: "source/open",
						source,
						topics: ["*", ...NETWORK_TOPICS]
					};
					socket.send(JSON.stringify(frame));
				});
				socket.addEventListener("message", (event) => {
					if (this.socket !== socket || typeof event.data !== "string") return;
					try {
						if (new TextEncoder().encode(event.data).byteLength > this.bootstrap.maxFrameBytes) throw new Error(`inspector protocol: Worker frame exceeds ${String(this.bootstrap.maxFrameBytes)} bytes`);
						const value = JSON.parse(event.data);
						if (this.queries.receive(value)) return;
						const frame = parseWorkerSourceFrame(value);
						if (frame.t !== "source/rejected" && (frame.sourceId !== this.realmSource.sourceId || frame.generation !== generation)) return;
						dispatchBridgeFrame(frame, {
							accepted: () => {
								this.accepted = true;
								this.lifecycle.connected();
								this.queries.connectSocket(source, socket);
								this.publisher.accept(socket);
							},
							acknowledged: () => {},
							resnapshot: () => {
								this.publisher.replace(socket);
							},
							rejected: (rejected) => {
								console.error(`[inspector] Client source rejected: ${rejected.message}`);
								socket.close(1008, "source rejected");
							},
							runtime: (request) => {
								this.executeRuntime(socket, generation, request).catch((error) => {
									console.error("[inspector] Client Runtime transport failed:", error);
									socket.close(1011, "Client Runtime transport failed");
								});
							},
							runtimeCanceled: (canceled) => {
								this.cancelRuntime(canceled.sessionId, canceled.requestId);
							},
							runtimeAcknowledged: (acknowledged) => {
								this.acknowledgeRuntime(acknowledged.sessionId, acknowledged.requestId);
							},
							runtimeClosed: (closed) => {
								this.cancelRuntimeSession(closed.sessionId);
								this.console.disable(closed.sessionId);
								this.runtime.closeSession(closed.sessionId);
							},
							consoleEnabled: (enabled) => {
								this.console.enable(enabled.sessionId);
							},
							consoleDisabled: (disabled) => {
								this.console.disable(disabled.sessionId);
							},
							sources: (request) => {
								this.executeSourceRequest(socket, generation, request).catch((error) => {
									console.error("[inspector] Client Sources transport failed:", error);
									socket.close(1011, "Client Sources transport failed");
								});
							},
							sourcesClosed: () => {}
						});
					} catch (error) {
						console.error("[inspector] invalid Worker control frame:", error);
						socket.close(1008, "invalid Worker control frame");
					}
				});
				socket.addEventListener("close", () => {
					if (this.socket !== socket || this.closed) return;
					this.socket = void 0;
					this.accepted = false;
					this.publisher.disconnect(socket);
					this.console.reset();
					this.cancelRuntimeRequests();
					this.runtime.reset();
					this.queries.disconnect("Inspector Client source disconnected");
					this.lifecycle.reconnect(() => {
						this.connect();
					});
				});
				socket.addEventListener("error", () => {});
			}
			async executeRuntime(socket, generation, frame) {
				const controller = new AbortController();
				const operation = {
					controller,
					sessionId: frame.sessionId
				};
				this.runtimeRequests.set(frame.requestId, operation);
				const response = await this.runtime.execute(frame, controller.signal, true);
				if (this.runtimeRequests.get(frame.requestId) !== operation) return;
				if (this.closed || this.socket !== socket || this.generation !== generation || socket.readyState !== WebSocket.OPEN) {
					this.cancelRuntime(frame.sessionId, frame.requestId);
					return;
				}
				socket.send(JSON.stringify(response));
			}
			acknowledgeRuntime(sessionId, requestId) {
				const operation = this.runtimeRequests.get(requestId);
				if (operation === void 0 || operation.sessionId !== sessionId) return;
				this.runtimeRequests.delete(requestId);
				this.runtime.acknowledge(sessionId, requestId);
			}
			cancelRuntime(sessionId, requestId) {
				const operation = this.runtimeRequests.get(requestId);
				if (operation === void 0 || operation.sessionId !== sessionId) return;
				this.runtimeRequests.delete(requestId);
				operation.controller.abort();
				this.runtime.cancel(sessionId, requestId);
			}
			cancelRuntimeSession(sessionId) {
				for (const [requestId, operation] of this.runtimeRequests) {
					if (operation.sessionId !== sessionId) continue;
					operation.controller.abort();
					this.runtime.cancel(sessionId, requestId);
					this.runtimeRequests.delete(requestId);
				}
			}
			cancelRuntimeRequests() {
				for (const [requestId, operation] of this.runtimeRequests) {
					operation.controller.abort();
					this.runtime.cancel(operation.sessionId, requestId);
				}
				this.runtimeRequests.clear();
			}
			async executeSourceRequest(socket, generation, frame) {
				let outcome;
				try {
					if (this.sourceCatalog === void 0) throw new ClientSourceCatalogError("invalid-request", "Client source catalog is unavailable");
					outcome = {
						ok: true,
						result: await this.sourceCatalog.execute(frame.command, this.bootstrap.maxClientSourceBytes)
					};
				} catch (error) {
					outcome = {
						ok: false,
						error: {
							code: error instanceof ClientSourceCatalogError ? error.code : "internal-error",
							message: renderError(error).slice(0, 2048)
						}
					};
				}
				let response = {
					v: 0,
					t: "client-sources/response",
					sourceId: this.realmSource.sourceId,
					generation,
					sessionId: frame.sessionId,
					requestId: frame.requestId,
					outcome
				};
				if (!isJsonValue(response) || jsonByteLength(response) > this.bootstrap.maxFrameBytes) response = {
					...response,
					outcome: {
						ok: false,
						error: {
							code: "result-too-large",
							message: "Client source result exceeds the source-frame byte limit"
						}
					}
				};
				if (this.closed || this.socket !== socket || this.generation !== generation || socket.readyState !== WebSocket.OPEN) return;
				socket.send(JSON.stringify(response));
			}
		};
		function renderError(error) {
			return error instanceof Error ? error.message : String(error);
		}
		//#endregion
		//#region src/client/bridge/controller.ts
		/**
		* Start the browser source transport for one validated Host bootstrap.
		* @param bootstrap - Host-injected endpoint and resource limits.
		* @returns The active reconnecting Client source after its tab identity is claimed.
		*/
		async function startInspectorClient(bootstrap) {
			const label = document.title || "Client";
			const realmSource = await ClientRealmSource.claim(label);
			try {
				return new ClientInspectorSource(bootstrap, label, void 0, realmSource);
			} catch (error) {
				realmSource.close();
				throw error;
			}
		}
		//#endregion
		//#region src/client/plugin.ts
		/** Cordis plugin name shared with the Host face. */
		const name = "experimental-inspector";
		/** This transport root has no Client service dependencies. */
		const inject = [];
		/**
		* Mount the Client source and shared `ctx.inspector` publishing API.
		* @param ctx - Client Cordis context whose page identity and lifecycle own the source.
		*/
		async function apply(ctx) {
			const injected = globalThis.__DSH_INSPECTOR__;
			if (injected === void 0) throw new Error("experimental inspector: Host bootstrap is missing; reload the page after enabling the Inspector");
			const bootstrap = parseInspectorClientBootstrap(injected);
			await ctx.effect(async () => {
				const source = await startInspectorClient(bootstrap);
				const disposers = [];
				try {
					disposers.push(publishCordisTree(ctx, source, {
						maxNodes: bootstrap.maxCordisNodes,
						maxBytes: bootstrap.maxFrameBytes - 4096
					}));
					disposers.push(ctx.provide("inspector", createInspectorService(source)));
				} catch (error) {
					try {
						disposeInspectorClient(source, disposers);
					} catch (cleanupError) {
						ctx.logger.error("experimental-inspector: Client initialization rollback failed", cleanupError);
					}
					throw error;
				}
				return () => {
					disposeInspectorClient(source, disposers);
				};
			}, "experimental-inspector: Client source");
		}
		function disposeInspectorClient(source, disposers) {
			const failures = [];
			for (const dispose of [...disposers].reverse()) try {
				dispose();
			} catch (error) {
				failures.push(error);
			}
			try {
				source.close();
			} catch (error) {
				failures.push(error);
			}
			if (failures.length > 0) throw new AggregateError(failures, "experimental-inspector: Client disposal failed");
		}
		//#endregion
		exports.apply = apply;
		exports.inject = inject;
		exports.name = name;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map