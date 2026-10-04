window.__ModuleLoader__.load({
	id: "@deepseek-ai/dsh-client-ui-attachment",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		Object.defineProperty(exports, Symbol.toStringTag, { value: "Module" });
		let react = require("react");
		let _deepseek_ai_dsh_client_ui_primitives = require("@deepseek-ai/dsh-client-ui-primitives");
		let react_jsx_runtime = require("react/jsx-runtime");
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
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-attachment/src/AttachmentRail.module.css.mjs
		const css$4 = ".X_svOW_root{min-width:0;position:relative}.X_svOW_rail{scrollbar-width:none;--dsh-scrollbar-thumb:var(--dsw-alias-scrollbar-bg-l2);--dsh-scrollbar-thumb-hover:var(--dsw-alias-scrollbar-hover-l2);align-items:stretch;gap:10px;display:flex;overflow:auto hidden}.X_svOW_rail::-webkit-scrollbar{display:none}.X_svOW_item{flex:none;height:64px}.X_svOW_thumbnail{border:.5px solid var(--dsw-alias-border-l2-darkmode-thin);border-radius:var(--dsw-radius-xl);background:var(--dsw-alias-interactive-bg-hover);cursor:zoom-in;width:64px;height:64px;padding:0;overflow:hidden}.X_svOW_thumbnail img{object-fit:cover;width:100%;height:100%;display:block}.X_svOW_remove{z-index:1;corner-shape:round;background:var(--dsw-alias-button-contrast-fill);width:18px;height:18px;color:var(--dsw-alias-label-primary-inverted);cursor:pointer;opacity:0;border:none;border-radius:50%;place-items:center;padding:0;transition:opacity .2s ease-in-out;display:grid;position:absolute;top:4px;right:4px}.X_svOW_item:hover .X_svOW_remove,.X_svOW_remove:focus-visible{opacity:1}@media (pointer:coarse){.X_svOW_remove{opacity:1}}@media (prefers-reduced-motion:reduce){.X_svOW_remove{transition:none}}.X_svOW_arrow{z-index:2;--dsw-elevation-stroke-color:var(--dsw-alias-border-l2-darkmode-thin);corner-shape:round;background:var(--dsw-specific-input-major);width:24px;height:24px;color:var(--dsw-alias-label-secondary);box-shadow:var(--dsw-elevation-panel);cursor:pointer;border:0;border-radius:999px;place-items:center;padding:0;display:grid;position:absolute;top:50%;transform:translateY(-50%)}.X_svOW_arrow:hover{background:var(--dsw-alias-interactive-bg-hover-solid)}.X_svOW_arrowLeft{left:4px}.X_svOW_arrowRight{right:4px}";
		const tagId$4 = "@deepseek-ai/dsh-client-ui-attachment/AttachmentRail.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$4) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-attachment";
			tag.dataset.pluginCss = tagId$4;
			tag.textContent = css$4;
			document.head.appendChild(tag);
		}
		var AttachmentRail_module_css_default = {
			"arrow": "X_svOW_arrow",
			"arrowLeft": "X_svOW_arrowLeft",
			"arrowRight": "X_svOW_arrowRight",
			"item": "X_svOW_item",
			"rail": "X_svOW_rail",
			"remove": "X_svOW_remove",
			"root": "X_svOW_root",
			"thumbnail": "X_svOW_thumbnail"
		};
		//#endregion
		//#region src/AttachmentRail.tsx
		/** Draft-attachment rail: scrollbar-less horizontal overflow paged by edge arrows. */
		/** Approximate pixels per wheel step for `deltaMode` LINE deltas (Firefox
		* notch wheels report lines, not pixels). */
		const WHEEL_LINE_PX = 16;
		/** Smooth paging unless the user asked for reduced motion. */
		function pageBehavior() {
			const matchMedia = window.matchMedia;
			return matchMedia?.("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth";
		}
		/**
		* Horizontal rail over the caller's ordered draft attachments.
		*
		* The rail scrolls with its scrollbar hidden; overflow is announced by edge
		* arrows recomputed from scroll geometry on scroll, item-count changes, and
		* rail size changes (a ResizeObserver on the rail element, so sidebar or
		* panel resizes count, not only window resizes). A vertical wheel pans the
		* rail horizontally and is consumed exclusively (non-passive listener), a
		* newly added item is revealed at the rail's end while a rail that mounts
		* over an existing draft keeps its start position. The owner renders each
		* item and decides mounting; it renders the rail only while items exist.
		*
		* @param props.items - attachments in draft order.
		* @param props.labels - rail-level strings (group name and paging arrows).
		* @param props.renderItem - render one attachment card in draft order.
		* @returns the rail group with its paging arrows.
		*/
		function AttachmentRail({ items, labels, renderItem }) {
			const railRef = (0, react.useRef)(null);
			const countRef = (0, react.useRef)(null);
			const [edges, setEdges] = (0, react.useState)({
				left: false,
				right: false
			});
			const updateEdges = (0, react.useCallback)(() => {
				const el = railRef.current;
				/* v8 ignore next -- defensive: every caller runs while the rail element is mounted. */
				if (el === null) return;
				const left = el.scrollLeft > 1;
				const right = el.scrollLeft < el.scrollWidth - el.clientWidth - 1;
				setEdges((prev) => prev.left === left && prev.right === right ? prev : {
					left,
					right
				});
			}, []);
			(0, react.useLayoutEffect)(() => {
				const grew = countRef.current !== null && items.length > countRef.current;
				countRef.current = items.length;
				const el = railRef.current;
				/* v8 ignore next -- defensive: the rail div renders unconditionally, so the layout effect always finds it. */
				if (el === null) return;
				if (grew) el.scrollLeft = el.scrollWidth - el.clientWidth;
				updateEdges();
			}, [items.length, updateEdges]);
			(0, react.useEffect)(() => {
				const el = railRef.current;
				/* v8 ignore next -- defensive: the rail div renders unconditionally, so the mount effect always finds it. */
				if (el === null) return;
				let disconnect = () => {};
				if (typeof ResizeObserver !== "undefined") {
					const observer = new ResizeObserver(updateEdges);
					observer.observe(el);
					disconnect = () => {
						observer.disconnect();
					};
				}
				const onWheel = (event) => {
					if (event.deltaY === 0) return;
					const scale = event.deltaMode === WheelEvent.DOM_DELTA_LINE ? WHEEL_LINE_PX : event.deltaMode === WheelEvent.DOM_DELTA_PAGE ? el.clientWidth : 1;
					event.preventDefault();
					el.scrollBy({
						left: event.deltaX !== 0 ? event.deltaX * scale : Math.sign(event.deltaY) * Math.min(Math.abs(event.deltaY) * scale, 60),
						behavior: "auto"
					});
				};
				el.addEventListener("wheel", onWheel, { passive: false });
				return () => {
					disconnect();
					el.removeEventListener("wheel", onWheel);
				};
			}, [updateEdges]);
			const page = (direction) => {
				const el = railRef.current;
				/* v8 ignore next -- defensive: the arrows render only while the rail is mounted, so a click cannot find a null ref. */
				if (el === null) return;
				el.scrollBy({
					left: direction * Math.max(el.clientWidth - 64, 200),
					behavior: pageBehavior()
				});
			};
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: AttachmentRail_module_css_default.root,
				children: [
					edges.left && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
						type: "button",
						className: clsx(AttachmentRail_module_css_default.arrow, AttachmentRail_module_css_default.arrowLeft),
						"aria-label": labels.scrollLeft,
						onClick: () => {
							page(-1);
						},
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronLeftOutlineRegular, {})
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
						ref: railRef,
						className: AttachmentRail_module_css_default.rail,
						role: "group",
						"aria-label": labels.group,
						onScroll: updateEdges,
						children: items.map((item) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: AttachmentRail_module_css_default.item,
							children: renderItem(item)
						}, item.id))
					}),
					edges.right && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
						type: "button",
						className: clsx(AttachmentRail_module_css_default.arrow, AttachmentRail_module_css_default.arrowRight),
						"aria-label": labels.scrollRight,
						onClick: () => {
							page(1);
						},
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconChevronRightOutlineRegular, {})
					})
				]
			});
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-attachment/src/DropOverlay.module.css.mjs
		const css$3 = ".us_2FG_mask{z-index:1000;pointer-events:none;background-color:var(--dsw-alias-bg-mask-drop);backdrop-filter:blur(10px);justify-content:center;align-items:center;animation:.16s ease-out us_2FG_fade-in;display:flex;position:fixed;inset:0}@keyframes us_2FG_fade-in{0%{opacity:0}to{opacity:1}}@media (prefers-reduced-motion:reduce){.us_2FG_mask{animation:none}}.us_2FG_wrap{color:var(--dsw-alias-label-primary);text-align:center;flex-direction:column;align-items:center;margin-top:-3%;padding:0 40px;display:flex}.us_2FG_illustration{width:115px;height:84px}.us_2FG_title{font:var(--dsw-font-l-20);margin-top:16px}.us_2FG_desc{font:var(--dsw-font-s-14);color:var(--dsw-alias-label-tertiary);white-space:pre-wrap;margin-top:16px}";
		const tagId$3 = "@deepseek-ai/dsh-client-ui-attachment/DropOverlay.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$3) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-attachment";
			tag.dataset.pluginCss = tagId$3;
			tag.textContent = css$3;
			document.head.appendChild(tag);
		}
		var DropOverlay_module_css_default = {
			"desc": "us_2FG_desc",
			"fade-in": "us_2FG_fade-in",
			"illustration": "us_2FG_illustration",
			"mask": "us_2FG_mask",
			"title": "us_2FG_title",
			"wrap": "us_2FG_wrap"
		};
		//#endregion
		//#region src/DropOverlay.tsx
		/**
		* Full-viewport invitation shown while a file drag is over the page
		* (DeepSeek Chat's DragMask). Decoration only: `pointer-events: none` keeps
		* drag targeting on the page below, so the owner's document-level listeners
		* keep an accurate enter/leave count and own accept/reject. Rendered through
		* a body portal for the same transformed-ancestor reason as the lightbox.
		*
		* @param props.disabled - drops are currently refused; renders the blocked
		* illustration and drops the desc line.
		* @param props.labels - resolved title and limits strings.
		* @returns the overlay layer.
		*/
		function DropOverlay({ disabled, labels }) {
			return (0, react_dom.createPortal)(/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: DropOverlay_module_css_default.mask,
				role: "status",
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
					className: DropOverlay_module_css_default.wrap,
					children: [
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: DropOverlay_module_css_default.illustration,
							"aria-hidden": "true",
							children: disabled ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(UploadDisabledIllustration, {}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(UploadIllustration, {})
						}),
						/* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: DropOverlay_module_css_default.title,
							children: labels.title
						}),
						!disabled && labels.desc !== void 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
							className: DropOverlay_module_css_default.desc,
							children: labels.desc
						})
					]
				})
			}), document.body);
		}
		/** Tilted photo-and-note cards (DeepSeek Chat upload illustration). */
		const UploadIllustration = () => /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("svg", {
			width: "115",
			height: "84",
			viewBox: "0 0 115 84",
			fill: "none",
			xmlns: "http://www.w3.org/2000/svg",
			children: [/* @__PURE__ */ (0, react_jsx_runtime.jsxs)("g", {
				clipPath: "url(#dshDropOverlayClip)",
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("rect", {
						y: "17.0742",
						width: "44.1832",
						height: "43.6431",
						rx: "12",
						transform: "rotate(-22.7338 0 17.0742)",
						fill: "#9CE5ED"
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("rect", {
						x: "73.4043",
						y: "8.54297",
						width: "43.7267",
						height: "50.5284",
						rx: "8",
						transform: "rotate(17.403 73.4043 8.54297)",
						fill: "#679EFE"
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
						d: "M30.4917 28.1369L40.8865 33.4564L37.2232 34.9524L29.5302 31.0159L26.7919 39.2122L23.1285 40.7082L26.8287 29.6338L16.8967 24.5516L20.5601 23.0556L27.7902 26.7549L30.3639 19.052L34.0273 17.556L30.4917 28.1369Z",
						fill: "white"
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
						d: "M77.5088 26.3047L101.057 33.7966",
						stroke: "white",
						strokeWidth: "3"
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
						d: "M72.2646 42.7871L86.3938 47.2823",
						stroke: "white",
						strokeWidth: "3"
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
						d: "M74.8867 34.5469L98.4353 42.0388",
						stroke: "white",
						strokeWidth: "3"
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("rect", {
						x: "31.583",
						y: "38.6641",
						width: "44.9157",
						height: "44.3666",
						rx: "12",
						transform: "rotate(-0.134233 31.583 38.6641)",
						fill: "#3964FE"
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
						d: "M38.9521 73.0337C39.6129 71.7086 41.7113 66.0937 43.5113 61.1663C44.1607 59.3885 46.7484 59.3923 47.4591 61.1465C48.9728 64.8828 50.7969 68.6922 51.9988 69.1925C54.2946 70.1482 57.9854 59.3573 68.0064 70.1801",
						stroke: "white",
						strokeWidth: "3"
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("circle", {
						cx: "60.6157",
						cy: "52.247",
						r: "4.38794",
						transform: "rotate(22.5996 60.6157 52.247)",
						fill: "white"
					})
				]
			}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("defs", { children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("clipPath", {
				id: "dshDropOverlayClip",
				children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("rect", {
					width: "115",
					height: "84",
					fill: "white"
				})
			}) })]
		});
		/** Greyed cards with a blocked badge (DeepSeek Chat disabled illustration). */
		const UploadDisabledIllustration = () => /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("svg", {
			width: "115",
			height: "84",
			viewBox: "0 0 115 84",
			fill: "none",
			xmlns: "http://www.w3.org/2000/svg",
			children: [
				/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
					d: "M29.6829 4.63701L11.0677 12.4368C4.95519 14.998 2.07624 22.0294 4.6374 28.1419L12.2285 46.259C14.7896 52.3715 21.8211 55.2505 27.9336 52.6893L46.5488 44.8895C52.6613 42.3283 55.5403 35.2969 52.9791 29.1844L45.388 11.0673C42.8269 4.9548 35.7954 2.07585 29.6829 4.63701Z",
					fill: "#979DA6"
				}),
				/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
					d: "M30.4915 28.1375L40.8863 33.4569L37.223 34.9529L29.53 31.0165L26.7917 39.2128L23.1283 40.7088L26.8285 29.6344L16.8965 24.5522L20.5599 23.0562L27.79 26.7555L30.3637 19.0526L34.0271 17.5566L30.4915 28.1375Z",
					fill: "white"
				}),
				/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
					d: "M107.496 19.2285L81.0381 10.9357C76.8221 9.61423 72.333 11.9607 71.0116 16.1768L60.6844 49.1246C59.363 53.3406 61.7095 57.8297 65.9255 59.1511L92.383 67.4439C96.599 68.7654 101.088 66.4189 102.41 62.2029L112.737 29.255C114.058 25.039 111.712 20.55 107.496 19.2285Z",
					fill: "#979DA6"
				}),
				/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
					d: "M77.5088 26.3047L101.057 33.7967",
					stroke: "white",
					strokeWidth: "3"
				}),
				/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
					d: "M72.2646 42.7871L86.3938 47.2823",
					stroke: "white",
					strokeWidth: "3"
				}),
				/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
					d: "M74.8867 34.5469L98.4353 42.0388",
					stroke: "white",
					strokeWidth: "3"
				}),
				/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
					d: "M66.5798 30.1418L41.481 30.2006C33.5281 30.2193 27.0962 36.6815 27.1148 44.6343L27.172 69.0742C27.1907 77.0271 33.6529 83.459 41.6057 83.4404L66.7045 83.3816C74.6574 83.363 81.0894 76.9008 81.0707 68.9479L81.0135 44.5081C80.9949 36.5552 74.5327 30.1232 66.5798 30.1418Z",
					fill: "#F59E0B"
				}),
				/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
					d: "M54 70.7969C61.732 70.7969 68 64.5289 68 56.7969C68 49.0649 61.732 42.7969 54 42.7969C46.268 42.7969 40 49.0649 40 56.7969C40 64.5289 46.268 70.7969 54 70.7969Z",
					stroke: "white",
					strokeWidth: "3.5"
				}),
				/* @__PURE__ */ (0, react_jsx_runtime.jsx)("path", {
					d: "M44 46.7969L64 66.7969",
					stroke: "white",
					strokeWidth: "3.5",
					strokeLinecap: "round"
				})
			]
		});
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-attachment/src/FileCard.module.css.mjs
		const css$2 = ".le64fq_card{border:.5px solid var(--dsw-alias-border-l2,#0000001f);border-radius:var(--dsw-radius-lg);background:var(--dsw-specific-input-major,transparent);box-sizing:border-box;text-align:left;align-items:center;gap:10px;width:240px;height:64px;padding:0 12px;display:inline-flex;position:relative}.le64fq_failed{border-color:var(--dsw-alias-state-error-primary,#d54941)}.le64fq_icon{flex:none;justify-content:center;align-items:center;width:28px;height:28px;display:inline-flex}.le64fq_spinner{corner-shape:round;border:2px solid;border-top-color:#0000;border-radius:50%;width:20px;height:20px;animation:.8s linear infinite le64fq_file-card-spin}@keyframes le64fq_file-card-spin{to{transform:rotate(360deg)}}.le64fq_body{flex-direction:column;flex:1;min-width:0;padding:8px 0;display:flex}.le64fq_retry{color:inherit;font:inherit;text-align:left;cursor:pointer;background:0 0;border:0;padding:0}.le64fq_name{white-space:nowrap;text-overflow:ellipsis;color:var(--dsw-alias-label-primary);font-size:14px;font-weight:500;line-height:22px;overflow:hidden}.le64fq_meta{white-space:nowrap;text-overflow:ellipsis;color:var(--dsw-alias-label-tertiary,#00000073);font-size:12px;line-height:15px;overflow:hidden}.le64fq_metaFailed{color:var(--dsw-alias-state-error-primary,#d54941)}.le64fq_remove{corner-shape:round;background:var(--dsw-alias-button-contrast-fill,#000000b8);width:18px;height:18px;color:var(--dsw-alias-label-primary-inverted,#fff);opacity:0;cursor:pointer;border:none;border-radius:50%;justify-content:center;align-items:center;padding:0;transition:opacity .2s ease-in-out;display:inline-flex;position:absolute;top:6px;right:6px}.le64fq_card:hover .le64fq_remove,.le64fq_remove:focus-visible{opacity:1}@media (pointer:coarse){.le64fq_remove{opacity:1}}@media (prefers-reduced-motion:reduce){.le64fq_remove{transition:none}}.le64fq_card:hover .le64fq_name,.le64fq_card:focus-within .le64fq_name{padding-right:18px}.le64fq_removeFailed{background:var(--dsw-alias-state-error-primary,#d54941);color:#fff;opacity:1}.le64fq_progressTrack{background:var(--dsw-alias-fill-tertiary,#00000014);border-radius:1px;height:2px;position:absolute;bottom:5px;left:12px;right:12px;overflow:hidden}.le64fq_progressBar{border-radius:inherit;background:var(--dsw-alias-brand-primary,#4d6bfe);width:35%;height:100%;animation:1.2s ease-in-out infinite alternate le64fq_file-card-progress;display:block}.le64fq_progressBar[style]{animation:none}@keyframes le64fq_file-card-progress{0%{transform:translate(-70%)}to{transform:translate(220%)}}@media (pointer:coarse){.le64fq_remove{opacity:1}}";
		const tagId$2 = "@deepseek-ai/dsh-client-ui-attachment/FileCard.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$2) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-attachment";
			tag.dataset.pluginCss = tagId$2;
			tag.textContent = css$2;
			document.head.appendChild(tag);
		}
		var FileCard_module_css_default = {
			"body": "le64fq_body",
			"card": "le64fq_card",
			"failed": "le64fq_failed",
			"file-card-progress": "le64fq_file-card-progress",
			"file-card-spin": "le64fq_file-card-spin",
			"icon": "le64fq_icon",
			"meta": "le64fq_meta",
			"metaFailed": "le64fq_metaFailed",
			"name": "le64fq_name",
			"progressBar": "le64fq_progressBar",
			"progressTrack": "le64fq_progressTrack",
			"remove": "le64fq_remove",
			"removeFailed": "le64fq_removeFailed",
			"retry": "le64fq_retry",
			"spinner": "le64fq_spinner"
		};
		//#endregion
		//#region src/FileCard.tsx
		/** One pending file card: type glyph, name, size or upload status, remove, retry. */
		function FileCard({ name, bytes, state, progress, labels, onRemove, onRetry }) {
			const extension = (0, _deepseek_ai_dsh_client_ui_primitives.fileExtension)(name).toUpperCase().slice(0, 8);
			const meta = state === "uploading" ? labels.uploading : state === "error" ? labels.failed : [extension, (0, _deepseek_ai_dsh_client_ui_primitives.fileSizeText)(bytes)].filter((part) => part !== "").join(" ");
			const retryable = state === "error";
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
				className: `${FileCard_module_css_default.card}${retryable ? ` ${FileCard_module_css_default.failed}` : ""}`,
				title: name,
				children: [
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: FileCard_module_css_default.icon,
						"aria-hidden": true,
						children: state === "uploading" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", { className: FileCard_module_css_default.spinner }) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.FileTypeIcon, { path: name })
					}),
					retryable ? /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("button", {
						type: "button",
						className: `${FileCard_module_css_default.body} ${FileCard_module_css_default.retry}`,
						"aria-label": labels.retry,
						onClick: onRetry,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: FileCard_module_css_default.name,
							children: name
						}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: `${FileCard_module_css_default.meta} ${FileCard_module_css_default.metaFailed}`,
							children: meta
						})]
					}) : /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("span", {
						className: FileCard_module_css_default.body,
						"aria-label": labels.label,
						children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: FileCard_module_css_default.name,
							children: name
						}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: FileCard_module_css_default.meta,
							children: meta
						})]
					}),
					/* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
						type: "button",
						className: retryable ? `${FileCard_module_css_default.remove} ${FileCard_module_css_default.removeFailed}` : FileCard_module_css_default.remove,
						"aria-label": labels.remove,
						onClick: onRemove,
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconCloseFillRegular, { size: 12 })
					}),
					state === "uploading" && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
						className: FileCard_module_css_default.progressTrack,
						"aria-hidden": true,
						children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
							className: FileCard_module_css_default.progressBar,
							style: progress === void 0 ? void 0 : { width: `${String(Math.min(1, Math.max(0, progress)) * 100)}%` }
						})
					})
				]
			});
		}
		//#endregion
		//#region src/client/labels.ts
		/**
		* Resolve original-image lightbox strings from the conversation namespace.
		* @param t - conversation namespace translator.
		* @returns translated lightbox labels.
		*/
		function lightboxLabels(t) {
			return {
				dialog: t("image.preview"),
				close: t("image.closePreview")
			};
		}
		/**
		* Resolve historical message-image strings from the conversation namespace.
		* @param t - conversation namespace translator.
		* @returns translated message-image labels.
		*/
		function messageImageLabels(t) {
			return {
				image: t("image.label"),
				open: t("image.openOriginal"),
				openNamed: (label) => t("image.openOriginalLabel", { label }),
				loading: t("image.loading"),
				loadFailed: t("image.loadFailed"),
				lightbox: lightboxLabels(t)
			};
		}
		/**
		* Resolve the document-level drop invitation and its optional limits line.
		* @param t - conversation namespace translator.
		* @param accepting - whether the composer can accept dropped files.
		* @param limits - optional translated count and size values.
		* @returns translated drop-overlay labels.
		*/
		function dropOverlayLabels(t, accepting, limits) {
			if (!accepting) return { title: t("attachment.dropBlocked") };
			return {
				title: t("attachment.dropTitle"),
				desc: limits === void 0 ? void 0 : t("attachment.dropDesc", limits)
			};
		}
		/**
		* Resolve pending-file card strings from the conversation namespace.
		* @param t - conversation namespace translator.
		* @param name - browser file name interpolated into remove/retry labels.
		* @returns translated file-card labels.
		*/
		function fileCardLabels(t, name) {
			return {
				label: t("file.pending"),
				remove: t("file.remove", { name }),
				uploading: t("file.uploading"),
				failed: t("file.uploadFailed"),
				retry: t("file.retry", { name })
			};
		}
		/**
		* Resolve the mixed draft-attachment rail strings from the conversation namespace.
		* @param t - conversation namespace translator.
		* @returns translated attachment-rail labels.
		*/
		function attachmentRailLabels(t) {
			return {
				group: t("attachment.pending"),
				scrollLeft: t("attachment.scrollLeft"),
				scrollRight: t("attachment.scrollRight")
			};
		}
		//#endregion
		//#region src/client/drop-events.ts
		/**
		* Members of a drop that are directories. The `File` a directory drop yields
		* is indistinguishable from an empty file, so the entry API is the only
		* source of that fact; browsers without it report no directories.
		*/
		function droppedDirectories(dataTransfer, files) {
			const directories = /* @__PURE__ */ new Set();
			let fileIndex = 0;
			for (const item of dataTransfer.items) {
				if (item.kind !== "file") continue;
				const file = files[fileIndex++];
				if (typeof item.webkitGetAsEntry !== "function") continue;
				if (item.webkitGetAsEntry()?.isDirectory !== true) continue;
				if (file !== void 0) directories.add(file);
			}
			return directories;
		}
		/**
		* Install one attachment view's file-drop listeners.
		* @param canAcceptDrop - whether this view accepts the dropped files.
		* @param onAddFiles - attachment intake callback.
		* @param dragDepth - the view's retained nested-drag counter.
		* @param setDragActive - publish whether a file drag is active.
		* @returns cleanup for exactly these listeners.
		*/
		function installDocumentDropEvents(canAcceptDrop, onAddFiles, dragDepth, setDragActive) {
			const fileTransfer = (event) => {
				const dataTransfer = event.dataTransfer;
				if (dataTransfer === null || !dataTransfer.types.includes("Files")) return null;
				return dataTransfer;
			};
			const reset = () => {
				dragDepth.current = 0;
				setDragActive(false);
			};
			const onDragEnter = (event) => {
				if (fileTransfer(event) === null) return;
				event.preventDefault();
				dragDepth.current += 1;
				setDragActive(true);
			};
			const onDragOver = (event) => {
				const dataTransfer = fileTransfer(event);
				if (dataTransfer === null) return;
				event.preventDefault();
				dataTransfer.dropEffect = canAcceptDrop ? "copy" : "none";
			};
			const onDragLeave = (event) => {
				if (fileTransfer(event) === null) return;
				dragDepth.current = Math.max(0, dragDepth.current - 1);
				if (dragDepth.current === 0) setDragActive(false);
				const leftViewport = event.clientX <= 0 || event.clientY <= 0 || event.clientX >= window.innerWidth || event.clientY >= window.innerHeight;
				if ((event.target === document.documentElement || event.target === document.body) && leftViewport) reset();
			};
			const onDrop = (event) => {
				const dataTransfer = fileTransfer(event);
				if (dataTransfer === null) return;
				event.preventDefault();
				reset();
				if (canAcceptDrop) {
					const files = [...dataTransfer.files];
					onAddFiles(files, droppedDirectories(dataTransfer, files));
				}
			};
			document.addEventListener("dragenter", onDragEnter);
			document.addEventListener("dragover", onDragOver);
			document.addEventListener("dragleave", onDragLeave);
			document.addEventListener("drop", onDrop);
			window.addEventListener("dragend", reset);
			return () => {
				document.removeEventListener("dragenter", onDragEnter);
				document.removeEventListener("dragover", onDragOver);
				document.removeEventListener("dragleave", onDragLeave);
				document.removeEventListener("drop", onDrop);
				window.removeEventListener("dragend", reset);
			};
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-attachment/src/client/ComposerAttachments.module.css.mjs
		const css$1 = ".cFbn4W_rail{min-width:0;margin-bottom:-6px;padding:2px 10px 0}.cFbn4W_imageItem{width:64px;height:64px;position:relative}.cFbn4W_thumbnail{border:.5px solid var(--dsw-alias-border-l2-darkmode-thin);border-radius:var(--dsw-radius-xl);background:var(--dsw-alias-interactive-bg-hover);cursor:zoom-in;width:64px;height:64px;padding:0;overflow:hidden}.cFbn4W_thumbnail img{object-fit:cover;width:100%;height:100%;display:block}.cFbn4W_remove{z-index:1;corner-shape:round;background:var(--dsw-alias-button-contrast-fill);width:18px;height:18px;color:var(--dsw-alias-label-primary-inverted);cursor:pointer;opacity:0;border:none;border-radius:50%;place-items:center;padding:0;transition:opacity .2s ease-in-out;display:grid;position:absolute;top:4px;right:4px}.cFbn4W_imageItem:hover .cFbn4W_remove,.cFbn4W_remove:focus-visible{opacity:1}@media (pointer:coarse){.cFbn4W_remove{opacity:1}}@media (prefers-reduced-motion:reduce){.cFbn4W_remove{transition:none}}";
		const tagId$1 = "@deepseek-ai/dsh-client-ui-attachment/ComposerAttachments.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId$1) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-attachment";
			tag.dataset.pluginCss = tagId$1;
			tag.textContent = css$1;
			document.head.appendChild(tag);
		}
		var ComposerAttachments_module_css_default = {
			"imageItem": "cFbn4W_imageItem",
			"rail": "cFbn4W_rail",
			"remove": "cFbn4W_remove",
			"thumbnail": "cFbn4W_thumbnail"
		};
		//#endregion
		//#region src/client/ComposerAttachments.tsx
		/** Draft image previews, pending-file cards, drop target, and original-image preview. */
		function ComposerAttachments({ attachments, canAcceptDrop, onAddFiles, onRemoveAttachment, uploads, onRetryFile, dropLimits, t }) {
			const [preview, setPreview] = (0, react.useState)(null);
			const [dragActive, setDragActive] = (0, react.useState)(false);
			const dragDepth = (0, react.useRef)(0);
			const closePreview = (0, react.useCallback)(() => {
				setPreview(null);
			}, []);
			(0, react.useEffect)(() => {
				if (preview !== null && !attachments.some((attachment) => attachment.id === preview.id)) setPreview(null);
			}, [attachments, preview]);
			(0, react.useEffect)(() => {
				return installDocumentDropEvents(canAcceptDrop, onAddFiles, dragDepth, setDragActive);
			}, [canAcceptDrop, onAddFiles]);
			const railItems = (0, react.useMemo)(() => attachments.map((attachment) => ({
				id: attachment.id,
				attachment
			})), [attachments]);
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [
				dragActive && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(DropOverlay, {
					disabled: !canAcceptDrop,
					labels: dropOverlayLabels(t, canAcceptDrop, dropLimits)
				}),
				railItems.length > 0 && /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
					className: ComposerAttachments_module_css_default.rail,
					children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(AttachmentRail, {
						items: railItems,
						labels: attachmentRailLabels(t),
						renderItem: (item) => {
							const attachment = item.attachment;
							if (attachment.kind === "file") {
								const upload = uploads[attachment.id];
								return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(FileCard, {
									name: attachment.file.name || t("file.label"),
									bytes: attachment.file.size,
									state: upload === void 0 || upload.status === "uploading" ? "uploading" : upload.status === "ready" ? "ready" : "error",
									...upload?.status === "uploading" && upload.total !== void 0 && upload.total > 0 ? { progress: upload.loaded / upload.total } : {},
									labels: fileCardLabels(t, attachment.file.name),
									onRemove: () => {
										onRemoveAttachment(attachment.id);
									},
									onRetry: () => {
										onRetryFile(attachment.id);
									}
								});
							}
							return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)("div", {
								className: ComposerAttachments_module_css_default.imageItem,
								children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
									type: "button",
									className: ComposerAttachments_module_css_default.thumbnail,
									title: t("image.openOriginal"),
									onClick: () => {
										setPreview(attachment);
									},
									children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)("img", {
										src: attachment.previewUrl,
										alt: attachment.file.name || t("image.pending")
									})
								}), /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
									type: "button",
									className: ComposerAttachments_module_css_default.remove,
									"aria-label": t("image.remove", { name: attachment.file.name }),
									onClick: () => {
										onRemoveAttachment(attachment.id);
									},
									children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconCloseFillRegular, { size: 12 })
								})]
							});
						}
					})
				}),
				preview !== null && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.ImageLightbox, {
					src: preview.previewUrl,
					alt: preview.file.name || t("image.original"),
					labels: lightboxLabels(t),
					onClose: closePreview
				})
			] });
		}
		//#endregion
		//#region \0dsh-css:/Users/bytedance/projects/dsh-agentserver/deepseek-harness/packages/client/ui-attachment/src/MessageImage.module.css.mjs
		const css = ".dpvGmG_gallery{flex-wrap:wrap;gap:10px;max-width:100%;display:flex}.dpvGmG_gallery[data-align=end]{justify-content:flex-end;align-self:flex-end}.dpvGmG_gallery[data-align=start]{justify-content:flex-start;align-self:flex-start}.dpvGmG_frame{border:.5px solid var(--dsw-alias-border-l2-darkmode-thin);border-radius:var(--dsw-radius-xl);background:var(--dsw-alias-interactive-bg-hover);cursor:zoom-in;flex:none;place-items:center;min-width:44px;min-height:44px;padding:0;display:grid;overflow:hidden}.dpvGmG_frame[data-variant=tile]{width:64px;min-width:64px;height:64px;min-height:64px}.dpvGmG_frame img{object-fit:cover;width:100%;height:100%;display:block}.dpvGmG_loading,.dpvGmG_error{color:var(--dsw-alias-label-tertiary);font-size:12px;line-height:18px}.dpvGmG_error{border:.5px solid var(--dsw-alias-border-l2-darkmode-thin);border-radius:var(--dsw-radius-md);background:var(--dsw-alias-interactive-bg-hover-danger);cursor:pointer;max-width:240px;padding:10px 12px}.dpvGmG_error[data-variant=tile]{border-radius:var(--dsw-radius-xl);width:64px;height:64px;padding:4px;overflow:hidden}.dpvGmG_frame[data-variant=thumbnail],.dpvGmG_error[data-variant=thumbnail]{box-sizing:border-box;border-radius:var(--dsw-radius-lg);place-items:center;width:48px;min-width:48px;height:48px;min-height:48px;padding:2px;display:grid;overflow:hidden}.dpvGmG_frame[data-variant=thumbnail] img{object-fit:contain}.dpvGmG_spinner{animation:.9s linear infinite dpvGmG_spinner-rotate}@keyframes dpvGmG_spinner-rotate{to{transform:rotate(360deg)}}@media (prefers-reduced-motion:reduce){.dpvGmG_spinner{animation:none}}.dpvGmG_frame:focus-visible,.dpvGmG_error:focus-visible{outline:var(--dsw-focus-ring-width) solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:2px}";
		const tagId = "@deepseek-ai/dsh-client-ui-attachment/MessageImage.module.css";
		if (typeof document !== "undefined" && document.querySelector("style[data-plugin-css=" + JSON.stringify(tagId) + "]") === null) {
			const tag = document.createElement("style");
			tag.dataset.plugin = "@deepseek-ai/dsh-client-ui-attachment";
			tag.dataset.pluginCss = tagId;
			tag.textContent = css;
			document.head.appendChild(tag);
		}
		var MessageImage_module_css_default = {
			"error": "dpvGmG_error",
			"frame": "dpvGmG_frame",
			"gallery": "dpvGmG_gallery",
			"loading": "dpvGmG_loading",
			"spinner": "dpvGmG_spinner",
			"spinner-rotate": "dpvGmG_spinner-rotate"
		};
		//#endregion
		//#region src/MessageImage.tsx
		/** Display box for a lone image (DeepSeek Chat rule): long edge 240px with
		* the rendered aspect ratio clamped to [0.25, 4] — the overflow is cropped by
		* `object-fit: cover` — and never upscaled past the image's natural size. The
		* crop anchor keeps the top of very tall images and the left of very wide
		* ones, where the informative content usually starts. */
		function singleFit(dimensions) {
			const natural = dimensions.width / dimensions.height;
			const ratio = Math.min(4, Math.max(.25, natural));
			const box = ratio >= 1 ? {
				width: 240,
				height: 240 / ratio
			} : {
				width: 240 * ratio,
				height: 240
			};
			const scale = Math.min(1, dimensions.width / box.width, dimensions.height / box.height);
			return {
				width: Math.max(1, Math.round(box.width * scale)),
				height: Math.max(1, Math.round(box.height * scale)),
				objectPosition: natural < .25 ? "center top" : natural > 4 ? "left center" : "center"
			};
		}
		/** Intrinsic dimensions of one gallery entry; a preview's stay unknown until its intake probe resolved. */
		function dimensionsOf(image) {
			if ("attachment" in image) return image.attachment;
			return image.preview.width !== void 0 && image.preview.height !== void 0 ? {
				width: image.preview.width,
				height: image.preview.height
			} : void 0;
		}
		/**
		* Compact history renderer with retryable loading and click-to-open original
		* preview. A lone image renders at its `singleFit` size; an image among
		* several renders as a fixed 64px square tile. The preview arm displays its
		* local URL directly — no loader round-trip, no failure/retry surface.
		* List thumbnails use status icons with localized tooltips and accessible names.
		*
		* @param props.image - the durable reference to load, or the local preview to display.
		* @param props.load - session-authorized URL loader for the durable arm.
		* @param props.variant - lone image, cropped gallery tile, or uncropped list thumbnail.
		* @param props.labels - resolved strings (tooltip, loading, retry, lightbox).
		* @returns the bounded thumbnail button, or the retry control on failure.
		*/
		function MessageImage({ image, load, variant, labels }) {
			const preview = "preview" in image ? image.preview : void 0;
			const attachment = "attachment" in image ? image.attachment : void 0;
			const [loaded, setLoaded] = (0, react.useState)(() => attachment === void 0 ? null : load.peek?.(attachment) ?? null);
			const [error, setError] = (0, react.useState)(false);
			const [open, setOpen] = (0, react.useState)(false);
			const [attempt, setAttempt] = (0, react.useState)(0);
			const request = (0, react.useCallback)(() => {
				setAttempt((a) => a + 1);
			}, []);
			const close = (0, react.useCallback)(() => {
				setOpen(false);
			}, []);
			const dimensions = (0, react.useMemo)(() => dimensionsOf(image), [image]);
			const fit = (0, react.useMemo)(() => {
				if (variant !== "single") return void 0;
				return dimensions === void 0 ? {
					width: 240,
					height: 240,
					objectPosition: "center"
				} : singleFit(dimensions);
			}, [dimensions, variant]);
			(0, react.useEffect)(() => {
				if (attachment === void 0) return;
				let live = true;
				setError(false);
				setLoaded(load.peek?.(attachment) ?? null);
				load(attachment).then((url) => {
					if (live) setLoaded(url);
				}).catch(() => {
					if (live) setError(true);
				});
				return () => {
					live = false;
				};
			}, [
				attachment,
				load,
				attempt
			]);
			const src = preview?.url ?? loaded;
			const label = ("attachment" in image ? image.label : void 0) ?? preview?.name ?? attachment?.name ?? labels.image;
			const loadingThumbnail = variant === "thumbnail" && src === null;
			if (error) return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
				type: "button",
				className: MessageImage_module_css_default.error,
				"data-variant": variant,
				title: variant === "thumbnail" ? labels.loadFailed : void 0,
				"aria-label": variant === "thumbnail" ? labels.loadFailed : void 0,
				onClick: request,
				children: variant === "thumbnail" ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
					"aria-hidden": "true",
					children: /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconRefreshOutlineRegular, {})
				}) : labels.loadFailed
			});
			return /* @__PURE__ */ (0, react_jsx_runtime.jsxs)(react_jsx_runtime.Fragment, { children: [/* @__PURE__ */ (0, react_jsx_runtime.jsx)("button", {
				type: "button",
				className: MessageImage_module_css_default.frame,
				"data-variant": variant,
				style: fit === void 0 ? void 0 : {
					width: fit.width,
					height: fit.height
				},
				title: loadingThumbnail ? labels.loading : labels.open,
				"aria-label": loadingThumbnail ? labels.loading : labels.openNamed(label),
				"aria-busy": loadingThumbnail || void 0,
				onClick: () => {
					if (src !== null) setOpen(true);
				},
				children: src === null ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)("span", {
					className: MessageImage_module_css_default.loading,
					"aria-hidden": loadingThumbnail || void 0,
					children: loadingThumbnail ? /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconLoadingOutlineRegular, { className: MessageImage_module_css_default.spinner }) : labels.loading
				}) : /* @__PURE__ */ (0, react_jsx_runtime.jsx)("img", {
					src,
					alt: label,
					style: fit === void 0 ? void 0 : { objectPosition: fit.objectPosition }
				})
			}), open && src !== null && /* @__PURE__ */ (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.ImageLightbox, {
				src,
				alt: label,
				labels: labels.lightbox,
				onClose: close
			})] });
		}
		/** Wrapping image group shared by user and assistant history: a lone image
		* renders large unless its owner requests compact tiles or contained list thumbnails. */
		function ImageGallery({ images, load, align, compact = false, thumbnail = false, labels }) {
			if (images.length === 0) return null;
			const variant = thumbnail ? "thumbnail" : compact || images.length > 1 ? "tile" : "single";
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)("div", {
				className: MessageImage_module_css_default.gallery,
				"data-align": align,
				children: images.map((image, index) => /* @__PURE__ */ (0, react_jsx_runtime.jsx)(MessageImage, {
					image,
					load,
					variant,
					labels
				}, `${"attachment" in image ? image.attachment.attachmentId : image.preview.url}:${index}`))
			});
		}
		//#endregion
		//#region src/client/MessageImages.tsx
		/** Historical message-image slot entry. */
		function MessageImages({ images, loadImage, align, compact = false, thumbnail = false, t }) {
			return /* @__PURE__ */ (0, react_jsx_runtime.jsx)(ImageGallery, {
				images,
				load: loadImage,
				align,
				compact,
				thumbnail,
				labels: messageImageLabels(t)
			});
		}
		//#endregion
		//#region src/client/index.ts
		/** Slot registry required by this presentation plugin. */
		const inject = ["slots"];
		/** Register attachment presentation without exporting React components as package values. */
		function apply(ctx) {
			ctx.slots.inject("conversation.input.attachments", () => ctx.slots.register({
				name: "conversation.input.attachments",
				locale: "conversation"
			}, ComposerAttachments));
			ctx.slots.inject("conversation.message.images", () => ctx.slots.register({
				name: "conversation.message.images",
				locale: "conversation"
			}, MessageImages));
			ctx.slots.inject("conversation.trajectory.images", () => ctx.slots.register({
				name: "conversation.trajectory.images",
				locale: "conversation"
			}, MessageImages));
			ctx.slots.inject("tool.call.images", () => ctx.slots.register({
				name: "tool.call.images",
				locale: "conversation"
			}, MessageImages));
		}
		//#endregion
		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map