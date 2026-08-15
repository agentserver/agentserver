import {
  Activity,
  AlertTriangle,
  Box,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Clock3,
  KeyRound,
  LoaderCircle,
  MessageSquare,
  Search,
  Sparkles,
  Timer,
  Wrench,
  X,
  XCircle,
} from "lucide-react"
import {
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from "react"
import { useTranslation } from "react-i18next"
import {
  Badge,
  Button,
  shortID,
  type SessionTrajectoryRecord,
} from "@agentserver/v2-web-shared"

type TimelineMode = "actual" | "sequence"
type InspectorTab = "summary" | "failure" | "input" | "output" | "details"

interface TrajectoryViewProps {
  records: SessionTrajectoryRecord[]
  activeRunId: string
  readAt: string
  hasMore: boolean
  truncated: boolean
  loading: boolean
  loadingEarlier: boolean
  error: string
  selectedId: string
  onSelect: (id: string) => void
  onLoadEarlier: () => void
  onRetry: () => void
}

export interface TrajectoryRunGroup {
  runId: string
  run: SessionTrajectoryRecord | null
  records: SessionTrajectoryRecord[]
  startedAt: number
  completedAt: number
}

export interface TrajectoryTimelineSpan {
  record: SessionTrajectoryRecord
  lane: number
  start: number
  end: number
}

export interface TrajectoryTimelineModel {
  start: number
  end: number
  spans: TrajectoryTimelineSpan[]
}

interface TrajectoryFilterOptions {
  query: string
  showLifecycle: boolean
  problemsOnly: boolean
}

interface TrajectoryRecordRowProps {
  record: SessionTrajectoryRecord
  depth: number
  runStartedAt: number
  selected: boolean
  onSelect: (id: string) => void
}

const PROBLEM_STATUSES = new Set<SessionTrajectoryRecord["status"]>(["failed", "unknown"])
const HIDDEN_LIFECYCLE_KINDS = new Set<SessionTrajectoryRecord["kind"]>([
  "attempt",
  "approval",
  "checkpoint",
  "credential",
  "event",
  "execution",
  "operation",
  "sandbox",
])
const TIMELINE_OMITTED_KINDS = new Set<SessionTrajectoryRecord["kind"]>([
  "run",
  "attempt",
])

export function groupTrajectoryRecords(records: SessionTrajectoryRecord[], readAt: string): TrajectoryRunGroup[] {
  const groups = new Map<string, TrajectoryRunGroup>()
  const readTime = timestamp(readAt) ?? Date.now()
  for (const record of records) {
    let group = groups.get(record.runId)
    if (!group) {
      const startedAt = timestamp(record.startedAt) ?? readTime
      group = { runId: record.runId, run: null, records: [], startedAt, completedAt: startedAt }
      groups.set(record.runId, group)
    }
    group.records.push(record)
    if (record.kind === "run") group.run = record
    const startedAt = timestamp(record.startedAt)
    if (startedAt !== null) group.startedAt = Math.min(group.startedAt, startedAt)
    group.completedAt = Math.max(group.completedAt, trajectoryRecordEnd(record, readTime))
  }
  return [...groups.values()].sort((left, right) => left.startedAt - right.startedAt)
}

export function filterTrajectoryRecords(records: SessionTrajectoryRecord[], options: TrajectoryFilterOptions): SessionTrajectoryRecord[] {
  const query = options.query.trim().toLocaleLowerCase()
  const byID = new Map(records.map((record) => [record.id, record]))
  const runRecords = new Map(records.filter((record) => record.kind === "run").map((record) => [record.runId, record]))
  const included = new Set<string>()

  for (const record of records) {
    if (!options.showLifecycle && HIDDEN_LIFECYCLE_KINDS.has(record.kind) && !PROBLEM_STATUSES.has(record.status)) continue
    if (options.problemsOnly && !PROBLEM_STATUSES.has(record.status)) continue
    if (query && !trajectoryRecordSearchText(record).includes(query)) continue
    included.add(record.id)
  }

  for (const id of [...included]) {
    const matched = byID.get(id)
    if (!matched) continue
    const run = runRecords.get(matched.runId)
    if (run) included.add(run.id)
    let parentID = matched.parentId
    const seen = new Set<string>()
    while (parentID && !seen.has(parentID)) {
      seen.add(parentID)
      const parent = byID.get(parentID)
      if (!parent) break
      if (parent.kind === "run" || options.showLifecycle || !HIDDEN_LIFECYCLE_KINDS.has(parent.kind) || PROBLEM_STATUSES.has(parent.status)) {
        included.add(parentID)
      }
      parentID = parent.parentId
    }
  }

  return records.filter((record) => included.has(record.id))
}

export function deriveTrajectoryTimeline(records: SessionTrajectoryRecord[], readAt: string, mode: TimelineMode): TrajectoryTimelineModel | null {
  const readTime = timestamp(readAt) ?? Date.now()
  const timed = records.filter((record) => !TIMELINE_OMITTED_KINDS.has(record.kind) && timestamp(record.startedAt) !== null)
  if (!timed.length) return null
  if (mode === "sequence") {
    return {
      start: 0,
      end: timed.length,
      spans: timed.map((record, index) => ({ record, lane: trajectoryLane(record.kind), start: index, end: index + 1 })),
    }
  }
  const spans = timed.map((record) => {
    const start = timestamp(record.startedAt) ?? readTime
    return { record, lane: trajectoryLane(record.kind), start, end: trajectoryRecordEnd(record, readTime) }
  })
  const start = Math.min(...spans.map((span) => span.start))
  const end = Math.max(start + 1, ...spans.map((span) => span.end))
  return { start, end, spans }
}

export function TrajectoryView({
  records,
  activeRunId,
  readAt,
  hasMore,
  truncated,
  loading,
  loadingEarlier,
  error,
  selectedId,
  onSelect,
  onLoadEarlier,
  onRetry,
}: TrajectoryViewProps) {
  const { t } = useTranslation()
  const [searchQuery, setSearchQuery] = useState("")
  const [showLifecycle, setShowLifecycle] = useState(false)
  const [problemsOnly, setProblemsOnly] = useState(false)
  const [timelineMode, setTimelineMode] = useState<TimelineMode>("actual")
  const [collapsedRuns, setCollapsedRuns] = useState<ReadonlySet<string>>(new Set())
  const [inspectorTab, setInspectorTab] = useState<InspectorTab>("summary")
  const ledgerRef = useRef<HTMLDivElement | null>(null)
  const followTailRef = useRef(true)
  const prependHeightRef = useRef<number | null>(null)
  const selected = records.find((record) => record.id === selectedId) ?? null
  const byID = useMemo(() => new Map(records.map((record) => [record.id, record])), [records])
  const problemCount = useMemo(() => records.filter((record) => PROBLEM_STATUSES.has(record.status)).length, [records])
  const lifecycleCount = useMemo(() => records.filter((record) => HIDDEN_LIFECYCLE_KINDS.has(record.kind)).length, [records])
  const visibleRecords = useMemo(() => filterTrajectoryRecords(records, {
    query: searchQuery,
    showLifecycle,
    problemsOnly,
  }), [problemsOnly, records, searchQuery, showLifecycle])
  const timelineRecords = useMemo(() => filterTrajectoryRecords(records, {
    query: searchQuery,
    showLifecycle: true,
    problemsOnly,
  }), [problemsOnly, records, searchQuery])
  const groups = useMemo(() => groupTrajectoryRecords(visibleRecords, readAt), [readAt, visibleRecords])
  const timeline = useMemo(() => deriveTrajectoryTimeline(timelineRecords, readAt, timelineMode), [readAt, timelineMode, timelineRecords])
  const selectedVisible = selected === null || visibleRecords.some((record) => record.id === selected.id)
  const allCollapsed = groups.length > 0 && groups.every((group) => collapsedRuns.has(group.runId))

  useEffect(() => {
    if (!selectedVisible) onSelect("")
  }, [onSelect, selectedVisible])

  useEffect(() => {
    setInspectorTab("summary")
  }, [selectedId])

  useEffect(() => {
    if (!followTailRef.current || searchQuery || problemsOnly) return
    const frame = requestAnimationFrame(() => {
      const ledger = ledgerRef.current
      if (ledger) ledger.scrollTop = ledger.scrollHeight
    })
    return () => cancelAnimationFrame(frame)
  }, [problemsOnly, readAt, records.length, searchQuery])

  useLayoutEffect(() => {
    if (loadingEarlier || prependHeightRef.current === null) return
    const ledger = ledgerRef.current
    if (ledger) ledger.scrollTop += ledger.scrollHeight - prependHeightRef.current
    prependHeightRef.current = null
  }, [loadingEarlier, records.length])

  const selectAndReveal = (id: string) => {
    onSelect(id)
    followTailRef.current = false
    requestAnimationFrame(() => {
      const row = [...(ledgerRef.current?.querySelectorAll<HTMLElement>("[data-trajectory-record]") ?? [])]
        .find((candidate) => candidate.dataset.trajectoryRecord === id)
      row?.scrollIntoView({ behavior: "smooth", block: "center" })
    })
  }

  const toggleRun = (runId: string) => {
    setCollapsedRuns((current) => {
      const next = new Set(current)
      if (next.has(runId)) next.delete(runId)
      else next.add(runId)
      return next
    })
  }

  const toggleAllRuns = () => {
    setCollapsedRuns(allCollapsed ? new Set() : new Set(groups.map((group) => group.runId)))
  }

  return <section className="trajectory-view" aria-label={t("browser.trajectoryTimeline")}>
    <div className="trajectory-toolbar" role="toolbar" aria-label={t("browser.trajectoryControls")}>
      <label className="trajectory-search">
        <Search size={14} />
        <input
          type="search"
          value={searchQuery}
          onChange={(event) => setSearchQuery(event.currentTarget.value)}
          placeholder={t("browser.trajectorySearchPlaceholder")}
          aria-label={t("browser.trajectorySearch")}
        />
      </label>
      <div className="trajectory-toolbar-actions">
        <button
          type="button"
          className="trajectory-toolbar-button"
          aria-pressed={timelineMode === "actual"}
          onClick={() => setTimelineMode((current) => current === "actual" ? "sequence" : "actual")}
          title={timelineMode === "actual" ? t("browser.trajectoryUseSequence") : t("browser.trajectoryUseActualTime")}
        ><Timer size={13} />{timelineMode === "actual" ? t("browser.trajectoryActualTime") : t("browser.trajectorySequence")}</button>
        <button
          type="button"
          className="trajectory-toolbar-button"
          aria-pressed={showLifecycle}
          onClick={() => setShowLifecycle((current) => !current)}
        ><Activity size={13} />{t("browser.lifecycleEvents")}<span>{lifecycleCount}</span></button>
        <button
          type="button"
          className="trajectory-toolbar-button"
          aria-pressed={problemsOnly}
          onClick={() => setProblemsOnly((current) => !current)}
          disabled={problemCount === 0}
        ><AlertTriangle size={13} />{t("browser.problems")}<span>{problemCount}</span></button>
        <button type="button" className="trajectory-toolbar-button" onClick={toggleAllRuns}>
          {allCollapsed ? <ChevronRight size={13} /> : <ChevronDown size={13} />}
          {allCollapsed ? t("browser.expandRuns") : t("browser.collapseRuns")}
        </button>
      </div>
      <span className="trajectory-toolbar-count">{visibleRecords.length}/{records.length} {t("browser.records")}</span>
    </div>

    <TrajectoryTimeline
      model={timeline}
      mode={timelineMode}
      selectedId={selectedId}
      hasMore={hasMore}
      loadingEarlier={loadingEarlier}
      onLoadEarlier={onLoadEarlier}
      onSelect={selectAndReveal}
    />

    {truncated ? <div className="trajectory-notice"><AlertTriangle size={14} />{t("browser.trajectoryTruncated")}</div> : null}
    {error ? <div className="trajectory-error"><div><AlertTriangle size={15} /><span>{error}</span></div><Button size="sm" variant="outline" onClick={onRetry}>{t("common.retry")}</Button></div> : null}

    <div className={`trajectory-workbench${selected ? " inspector-open" : ""}`}>
      <div className="trajectory-ledger-column">
        <div className="trajectory-column-header">
          <div><strong>{t("browser.eventLedger")}</strong><span>{activeRunId ? t("browser.live") : t("browser.settled")}</span></div>
          {!showLifecycle && lifecycleCount > 0 ? <button type="button" onClick={() => setShowLifecycle(true)}>{lifecycleCount} {t("browser.hiddenLifecycle")}</button> : null}
        </div>
        <div
          className="trajectory-ledger"
          ref={ledgerRef}
          data-trajectory-scroll
          onScroll={(event) => {
            const target = event.currentTarget
            followTailRef.current = target.scrollHeight - target.scrollTop - target.clientHeight < 48
          }}
        >
          {hasMore ? <div className="trajectory-load-earlier"><Button size="sm" variant="outline" disabled={loadingEarlier} onClick={() => {
            prependHeightRef.current = ledgerRef.current?.scrollHeight ?? null
            onLoadEarlier()
          }}>{loadingEarlier ? t("common.loading") : t("browser.loadEarlier")}</Button></div> : null}
          {loading && records.length === 0 ? <div className="trajectory-state"><LoaderCircle className="trajectory-spin" size={17} />{t("common.loading")}</div> : null}
          {!loading && records.length === 0 && !error ? <div className="trajectory-state"><Activity size={18} />{t("browser.noTrajectory")}</div> : null}
          {!loading && records.length > 0 && groups.length === 0 ? <div className="trajectory-state"><Search size={18} />{t("browser.noMatchingTrajectory")}</div> : null}
          <div className="trajectory-run-groups">
            {groups.map((group, groupIndex) => {
              const run = group.run
              const collapsed = collapsedRuns.has(group.runId)
              const children = group.records.filter((record) => record.kind !== "run")
              const models = children.filter((record) => record.kind === "model" || record.kind === "assistant" || record.kind === "reasoning").length
              const tools = children.filter((record) => record.kind === "tool").length
              const problems = children.filter((record) => PROBLEM_STATUSES.has(record.status)).length
              const status = run?.status ?? (problems ? "failed" : activeRunId === group.runId ? "running" : "info")
              return <section className="trajectory-run-group" key={group.runId} data-active={activeRunId === group.runId || undefined}>
                <div className={`trajectory-run-header trajectory-status-${status}${run?.id === selectedId ? " selected" : ""}`}>
                  <button type="button" className="trajectory-run-toggle" onClick={() => toggleRun(group.runId)} aria-label={collapsed ? t("browser.expandRuns") : t("browser.collapseRuns")}>
                    {collapsed ? <ChevronRight size={14} /> : <ChevronDown size={14} />}
                  </button>
                  <button type="button" className="trajectory-run-select" onClick={() => run && onSelect(run.id)} disabled={!run}>
                    <span className="trajectory-run-status">{trajectoryStatusIcon(status)}</span>
                    <span className="trajectory-run-copy"><strong>{t("browser.turn")} {groupIndex + 1}</strong><small>{run?.summary || (activeRunId === group.runId ? t("browser.inProgress") : t("browser.runTimeline"))}</small></span>
                    <span className="trajectory-run-stats">
                      <span>{models} {t("browser.modelSteps")}</span>
                      <span>{tools} {t("browser.toolCalls")}</span>
                      {problems ? <span className="danger">{problems} {t("browser.problems")}</span> : null}
                    </span>
                    <span className="trajectory-run-duration">{formatTrajectoryDuration(Math.max(0, group.completedAt - group.startedAt))}</span>
                  </button>
                </div>
                {!collapsed ? <div className="trajectory-run-records" role="list">
                  {children.map((record) => <TrajectoryRecordRow
                    key={record.id}
                    record={record}
                    depth={trajectoryRecordDepth(record, byID)}
                    runStartedAt={group.startedAt}
                    selected={record.id === selectedId}
                    onSelect={onSelect}
                  />)}
                </div> : <button type="button" className="trajectory-collapsed-summary" onClick={() => toggleRun(group.runId)}>… {children.length} {t("browser.records")}</button>}
              </section>
            })}
          </div>
        </div>
      </div>

      {selected ? <TrajectoryInspector
        record={selected}
        recordsByID={byID}
        activeTab={inspectorTab}
        onTabChange={setInspectorTab}
        onSelect={selectAndReveal}
        onClose={() => onSelect("")}
      /> : null}
    </div>
  </section>
}

function TrajectoryTimeline({ model, mode, selectedId, hasMore, loadingEarlier, onLoadEarlier, onSelect }: {
  model: TrajectoryTimelineModel | null
  mode: TimelineMode
  selectedId: string
  hasMore: boolean
  loadingEarlier: boolean
  onLoadEarlier: () => void
  onSelect: (id: string) => void
}) {
  const { t } = useTranslation()
  const duration = Math.max(1, (model?.end ?? 0) - (model?.start ?? 0))
  const extent = model
    ? mode === "actual"
      ? formatTrajectoryDuration(duration)
      : `${model.spans.length} ${t("browser.records")}`
    : ""
  return <section className="trajectory-timeline" aria-label={t("browser.runTimeline")}>
    <div className="trajectory-timeline-heading">
      <div><Activity size={14} /><strong>{t("browser.runTimeline")}</strong></div>
      <span>{mode === "actual" ? t("browser.trajectoryActualTime") : t("browser.trajectorySequence")}{extent ? ` · ${extent}` : ""}</span>
    </div>
    <div className="trajectory-timeline-plot">
      <div className="trajectory-lane-labels" aria-hidden="true"><span>{t("browser.inputLane")}</span><span>{t("browser.modelLane")}</span><span>{t("browser.toolsLane")}</span></div>
      <div className="trajectory-lane-track">
        {hasMore ? <button type="button" className="trajectory-earlier-marker" disabled={loadingEarlier} onClick={onLoadEarlier} title={t("browser.loadEarlier")}>…</button> : null}
        {!model ? <span className="trajectory-run-empty">{t("browser.noTrajectory")}</span> : model.spans.map((span) => {
          const left = (span.start - model.start) / duration * 100
          const width = Math.max(0, span.end - span.start) / duration * 100
          const point = width < .12
          const style = {
            "--trajectory-span-left": `${left}%`,
            "--trajectory-span-width": `${width}%`,
            "--trajectory-span-lane": span.lane,
          } as CSSProperties
          return <button
            key={span.record.id}
            type="button"
            className={`trajectory-timeline-span trajectory-kind-${span.record.kind} trajectory-status-${span.record.status}${span.record.id === selectedId ? " selected" : ""}${point ? " point" : ""}`}
            style={style}
            data-trajectory-timeline-record={span.record.id}
            title={`${span.record.title} · ${trajectoryStatusText(t, span.record.status)}${mode === "actual" ? ` · ${formatTrajectoryDuration(Math.max(0, span.end - span.start))}` : ` · #${span.start + 1}`}`}
            onClick={() => onSelect(span.record.id)}
          />
        })}
      </div>
    </div>
    {model ? <div className="trajectory-timeline-scale"><span>{mode === "actual" ? "+0 ms" : "#1"}</span><span>{mode === "actual" ? `+${formatTrajectoryDuration(duration)}` : `#${model.spans.length}`}</span></div> : null}
  </section>
}

function TrajectoryRecordRow({ record, depth, runStartedAt, selected, onSelect }: TrajectoryRecordRowProps) {
  const { t } = useTranslation()
  const style = { "--trajectory-depth": Math.max(0, depth - 1) } as CSSProperties
  const startedAt = timestamp(record.startedAt) ?? runStartedAt
  return <button
    type="button"
    role="listitem"
    className={`trajectory-record trajectory-status-${record.status}${selected ? " selected" : ""}`}
    style={style}
    onClick={() => onSelect(record.id)}
    data-trajectory-record={record.id}
  >
    <span className="trajectory-tree-guide" />
    <span className={`trajectory-kind-tag trajectory-kind-${record.kind}`}>{trajectoryKindIcon(record.kind)}<span>{record.kind}</span></span>
    <span className="trajectory-record-copy"><strong>{record.title}</strong><small>{record.summary || record.kind}</small></span>
    <span className="trajectory-record-offset">+{formatTrajectoryDuration(Math.max(0, startedAt - runStartedAt))}</span>
    <span className="trajectory-record-duration">{record.durationMillis !== undefined ? formatTrajectoryDuration(record.durationMillis) : t("browser.inProgress")}</span>
    <span className="trajectory-record-status">{trajectoryStatusIcon(record.status)}<span>{trajectoryStatusText(t, record.status)}</span></span>
  </button>
}

function TrajectoryInspector({ record, recordsByID, activeTab, onTabChange, onSelect, onClose }: {
  record: SessionTrajectoryRecord
  recordsByID: Map<string, SessionTrajectoryRecord>
  activeTab: InspectorTab
  onTabChange: (tab: InspectorTab) => void
  onSelect: (id: string) => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const tabs: { id: InspectorTab; label: string }[] = [
    { id: "summary", label: t("browser.summary") },
    ...(record.failure ? [{ id: "failure" as const, label: t("browser.failure") }] : []),
    ...(record.input !== undefined ? [{ id: "input" as const, label: t("browser.input") }] : []),
    ...(record.output !== undefined ? [{ id: "output" as const, label: t("browser.output") }] : []),
    ...(record.details.length ? [{ id: "details" as const, label: t("browser.details") }] : []),
  ]
  const effectiveTab = tabs.some((tab) => tab.id === activeTab) ? activeTab : "summary"
  const ancestry: SessionTrajectoryRecord[] = []
  let parentID = record.parentId
  const seen = new Set<string>()
  while (parentID && !seen.has(parentID)) {
    seen.add(parentID)
    const parent = recordsByID.get(parentID)
    if (!parent) break
    ancestry.unshift(parent)
    parentID = parent.parentId
  }

  return <aside className="trajectory-inspector" aria-label={t("browser.inspector")}>
    <div className="trajectory-inspector-header">
      <div className="trajectory-inspector-title">
        <span className={`trajectory-large-icon trajectory-status-${record.status}`}>{trajectoryStatusIcon(record.status)}</span>
        <div><span>{record.kind}</span><h2>{record.title}</h2><p>{record.summary}</p></div>
      </div>
      <Button variant="ghost" size="icon" onClick={onClose} aria-label={t("browser.closeInspector")}><X size={15} /></Button>
    </div>
    {ancestry.length ? <nav className="trajectory-breadcrumbs" aria-label={t("browser.identity")}>
      {ancestry.map((parent) => <span key={parent.id}><button type="button" onClick={() => onSelect(parent.id)}>{parent.kind === "run" ? shortID(parent.runId) : parent.title}</button><ChevronRight size={11} /></span>)}
      <strong>{record.kind}</strong>
    </nav> : null}
    <div className="trajectory-inspector-tabs" role="tablist" aria-label={t("browser.inspector")}>
      {tabs.map((tab) => <button key={tab.id} type="button" role="tab" aria-selected={effectiveTab === tab.id} onClick={() => onTabChange(tab.id)}>{tab.label}</button>)}
    </div>
    <div className="trajectory-inspector-scroll" role="tabpanel">
      {effectiveTab === "summary" ? <>
        {record.failure ? <button type="button" className="trajectory-failure-summary" onClick={() => onTabChange("failure")}><AlertTriangle size={15} /><span><strong>{record.failure.category}</strong>{record.failure.message}</span><ChevronRight size={14} /></button> : null}
        <TrajectoryInspectorSection title={t("browser.overview")}>
          <dl className="trajectory-facts">
            <div><dt>{t("common.status")}</dt><dd><Badge tone={trajectoryTone(record.status)}>{trajectoryStatusText(t, record.status)}</Badge></dd></div>
            <div><dt>{t("browser.started")}</dt><dd>{formatTrajectoryTime(record.startedAt, true)}</dd></div>
            {record.completedAt ? <div><dt>{t("browser.completedAt")}</dt><dd>{formatTrajectoryTime(record.completedAt, true)}</dd></div> : null}
            <div><dt>{t("browser.duration")}</dt><dd>{record.durationMillis !== undefined ? formatTrajectoryDuration(record.durationMillis) : t("browser.inProgress")}</dd></div>
            <div><dt>{t("browser.run")}</dt><dd title={record.runId}>{shortID(record.runId)}</dd></div>
            {record.runAttemptId ? <div><dt>Attempt</dt><dd title={record.runAttemptId}>{shortID(record.runAttemptId)} · {record.runAttemptGeneration}</dd></div> : null}
            {record.executionId ? <div><dt>Execution</dt><dd title={record.executionId}>{shortID(record.executionId)}</dd></div> : null}
            {record.operationId ? <div><dt>Operation</dt><dd title={record.operationId}>{shortID(record.operationId)}</dd></div> : null}
            {record.sandboxId ? <div><dt>Sandbox</dt><dd title={record.sandboxId}>{shortID(record.sandboxId)}</dd></div> : null}
          </dl>
        </TrajectoryInspectorSection>
        {(record.input !== undefined || record.output !== undefined || record.details.length) ? <div className="trajectory-inspector-previews">
          {record.input !== undefined ? <button type="button" onClick={() => onTabChange("input")}><span>{t("browser.input")}</span><code>{singleLinePreview(record.input)}</code></button> : null}
          {record.output !== undefined ? <button type="button" onClick={() => onTabChange("output")}><span>{t("browser.output")}</span><code>{singleLinePreview(record.output)}</code></button> : null}
          {record.details.length ? <button type="button" onClick={() => onTabChange("details")}><span>{t("browser.details")}</span><code>{record.details.length} fields</code></button> : null}
        </div> : null}
      </> : null}
      {effectiveTab === "failure" && record.failure ? <div className="trajectory-failure"><strong>{record.failure.category}</strong><p>{record.failure.message}</p><dl>
        <div><dt>Code</dt><dd>{record.failure.code}</dd></div><div><dt>Component</dt><dd>{record.failure.component}</dd></div>
        <div><dt>Phase</dt><dd>{record.failure.phase}</dd></div><div><dt>Retryable</dt><dd>{String(record.failure.retryable)}</dd></div>
        {record.failure.fingerprint ? <div><dt>Fingerprint</dt><dd>{record.failure.fingerprint}</dd></div> : null}
      </dl></div> : null}
      {effectiveTab === "input" && record.input !== undefined ? <pre>{record.input}{record.inputTruncated ? `\n\n${t("browser.contentTruncated")}` : ""}</pre> : null}
      {effectiveTab === "output" && record.output !== undefined ? <pre>{record.output}{record.outputTruncated ? `\n\n${t("browser.contentTruncated")}` : ""}</pre> : null}
      {effectiveTab === "details" ? <>
        <TrajectoryInspectorSection title={t("browser.details")}><dl className="trajectory-details">{record.details.map((detail) => <div key={`${detail.name}:${detail.value}`}><dt>{detail.name}</dt><dd>{detail.value}</dd></div>)}</dl></TrajectoryInspectorSection>
        <TrajectoryInspectorSection title={t("browser.identity")}><code className="trajectory-record-id">{record.id}</code></TrajectoryInspectorSection>
      </> : null}
    </div>
  </aside>
}

function TrajectoryInspectorSection({ title, children }: { title: string; children: ReactNode }) {
  return <section className="trajectory-inspector-section"><h3>{title}</h3>{children}</section>
}

function trajectoryRecordSearchText(record: SessionTrajectoryRecord): string {
  return [
    record.id,
    record.parentId,
    record.kind,
    record.status,
    record.title,
    record.summary,
    record.runId,
    record.runAttemptId,
    record.toolCallId,
    record.executionId,
    record.operationId,
    record.sandboxId,
    record.input,
    record.output,
    record.failure?.code,
    record.failure?.category,
    record.failure?.message,
    record.failure?.component,
    record.failure?.phase,
    ...record.details.flatMap((detail) => [detail.name, detail.value]),
  ].filter((value): value is string => Boolean(value)).join("\n").toLocaleLowerCase()
}

function trajectoryRecordDepth(record: SessionTrajectoryRecord, byID: Map<string, SessionTrajectoryRecord>): number {
  let parentID = record.parentId
  let depth = 0
  const seen = new Set<string>([record.id])
  while (parentID && depth < 6 && !seen.has(parentID)) {
    depth += 1
    seen.add(parentID)
    parentID = byID.get(parentID)?.parentId
  }
  if (depth) return depth
  const fallback: Record<SessionTrajectoryRecord["kind"], number> = {
    run: 0,
    input: 1,
    attempt: 1,
    model: 2,
    assistant: 2,
    reasoning: 2,
    tool: 2,
    approval: 3,
    execution: 3,
    operation: 4,
    sandbox: 2,
    credential: 3,
    checkpoint: 2,
    event: 2,
  }
  return fallback[record.kind]
}

function trajectoryLane(kind: SessionTrajectoryRecord["kind"]): number {
  if (kind === "input") return 0
  if (kind === "model" || kind === "assistant" || kind === "reasoning") return 1
  return 2
}

function trajectoryRecordEnd(record: SessionTrajectoryRecord, readTime: number): number {
  const start = timestamp(record.startedAt) ?? readTime
  const completed = timestamp(record.completedAt)
  if (completed !== null) return Math.max(start, completed)
  if (record.status === "running" || record.status === "queued") return Math.max(start, readTime)
  return start
}

function timestamp(value: string | undefined): number | null {
  if (!value) return null
  const parsed = Date.parse(value)
  return Number.isFinite(parsed) ? parsed : null
}

function formatTrajectoryDuration(milliseconds: number): string {
  if (milliseconds < 1000) return `${Math.round(milliseconds)} ms`
  if (milliseconds < 60_000) return `${(milliseconds / 1000).toFixed(milliseconds < 10_000 ? 1 : 0)} s`
  const minutes = Math.floor(milliseconds / 60_000)
  const seconds = Math.floor((milliseconds % 60_000) / 1000)
  return `${minutes}m ${seconds}s`
}

function formatTrajectoryTime(value: string, complete = false): string {
  const date = new Date(value)
  return new Intl.DateTimeFormat(undefined, complete
    ? { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit", fractionalSecondDigits: 3 }
    : { hour: "2-digit", minute: "2-digit", second: "2-digit" }).format(date)
}

function singleLinePreview(value: string): string {
  const compact = value.replace(/\s+/gu, " ").trim()
  return compact.length > 120 ? `${compact.slice(0, 117)}…` : compact || "—"
}

function trajectoryTone(status: SessionTrajectoryRecord["status"]): "neutral" | "success" | "danger" | "warning" {
  if (status === "succeeded") return "success"
  if (status === "failed" || status === "unknown") return "danger"
  if (status === "running" || status === "queued") return "warning"
  return "neutral"
}

function trajectoryStatusText(t: (key: string) => string, status: SessionTrajectoryRecord["status"]): string {
  const keys: Record<SessionTrajectoryRecord["status"], string> = {
    queued: "browser.trajectoryStatusQueued",
    running: "browser.trajectoryStatusRunning",
    succeeded: "browser.trajectoryStatusSucceeded",
    failed: "browser.trajectoryStatusFailed",
    cancelled: "browser.trajectoryStatusCancelled",
    unknown: "browser.trajectoryStatusUnknown",
    info: "browser.trajectoryStatusInfo",
  }
  return t(keys[status])
}

function trajectoryStatusIcon(status: SessionTrajectoryRecord["status"]): ReactNode {
  if (status === "succeeded") return <CheckCircle2 size={15} />
  if (status === "failed" || status === "unknown") return <XCircle size={15} />
  if (status === "running") return <LoaderCircle className="trajectory-spin" size={15} />
  if (status === "queued") return <Clock3 size={15} />
  return <Activity size={15} />
}

function trajectoryKindIcon(kind: SessionTrajectoryRecord["kind"]): ReactNode {
  if (kind === "input") return <MessageSquare size={13} />
  if (kind === "sandbox") return <Box size={13} />
  if (kind === "tool" || kind === "execution" || kind === "operation") return <Wrench size={13} />
  if (kind === "assistant" || kind === "reasoning" || kind === "model") return <Sparkles size={13} />
  if (kind === "credential" || kind === "approval") return <KeyRound size={13} />
  return <Activity size={13} />
}
